package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/phinze/camlink-fix/internal/camwatch"
	"github.com/phinze/camlink-fix/internal/health"
	"github.com/phinze/camlink-fix/internal/notify"
	"github.com/phinze/camlink-fix/internal/reset"
	"github.com/phinze/camlink-fix/internal/sleepwatch"
	"github.com/phinze/camlink-fix/internal/usbwatch"
)

// Elgato Cam Link 4K USB IDs
const (
	camLinkVendorID  = 0x0fd9
	camLinkProductID = 0x007b
)

// demandCooldown rate-limits camera-demand-triggered health checks so a live
// meeting (which grabs the camera repeatedly) doesn't spawn a probe every few
// seconds. One check per window is plenty to catch a wedge promptly.
const demandCooldown = 30 * time.Second

// isOwnProbe reports whether a camera-open came from our own health check
// rather than a real app. The health check runs system_profiler and ffmpeg,
// which open the camera and show up in camwatch just like any other client;
// treating them as demand would make a check trigger itself in a loop.
func isOwnProbe(process string) bool {
	switch process {
	case "ffmpeg", "system_profiler":
		return true
	default:
		return false
	}
}

func kickDaemon() {
	// Find other camlink-fix processes (exclude our own PID)
	out, err := exec.Command("pgrep", "-x", "camlink-fix").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "no running camlink-fix daemon found")
		os.Exit(1)
	}

	myPID := os.Getpid()
	var targets []int
	for _, line := range strings.Split(strings.TrimSpace(string(bytes.TrimSpace(out))), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid == myPID {
			continue
		}
		targets = append(targets, pid)
	}

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "no running camlink-fix daemon found")
		os.Exit(1)
	}

	for _, pid := range targets {
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not find process %d: %v\n", pid, err)
			continue
		}
		if err := proc.Signal(syscall.SIGUSR1); err != nil {
			fmt.Fprintf(os.Stderr, "could not signal process %d: %v\n", pid, err)
			continue
		}
		fmt.Printf("sent SIGUSR1 to camlink-fix (pid %d)\n", pid)
	}
}

func main() {
	var (
		kick         = flag.Bool("kick", false, "Send SIGUSR1 to a running camlink-fix daemon to trigger an immediate check")
		uhubctlPath  = flag.String("uhubctl-path", "uhubctl", "Path to uhubctl binary")
		ffmpegPath   = flag.String("ffmpeg-path", "ffmpeg", "Path to ffmpeg binary")
		deviceName   = flag.String("device-name", "Cam Link 4K", "Camera device name as shown in system_profiler")
		wakeDelay    = flag.Duration("wake-delay", 5*time.Second, "Delay after wake before checking camera")
		enableNotify = flag.Bool("notify", true, "Send macOS notifications")
		retryDelay   = flag.Duration("retry-delay", 30*time.Second, "Delay between retries after failed health check")
		maxRetries   = flag.Int("max-retries", 10, "Maximum number of retries after a failed health check")
	)
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[camlink-fix] ")

	if *kick {
		kickDaemon()
		return
	}

	log.Printf("starting (device=%q, wake-delay=%s, retry=%s×%d)", *deviceName, *wakeDelay, *retryDelay, *maxRetries)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	usr1Ch := make(chan os.Signal, 1)
	signal.Notify(usr1Ch, syscall.SIGUSR1)

	// Start watchers
	wakeCh := sleepwatch.Watch(ctx)
	usbCh := usbwatch.Watch(ctx, camLinkVendorID, camLinkProductID)

	// camwatch is observe-only for now: it logs when any app opens a camera so
	// we can learn whether "someone reached for the camera" is a viable edge
	// trigger (and whether the signal still fires when the Cam Link is wedged).
	// It does not drive resets yet.
	camCh := camwatch.Watch(ctx)

	healthCfg := health.Config{
		FFmpegPath: *ffmpegPath,
		DeviceName: *deviceName,
		Timeout:    3 * time.Second,
	}

	// Debounce: only one check/reset cycle at a time
	var resetting atomic.Bool

	// tryFix attempts a health check and reset. Returns true if camera is healthy
	// (either already healthy or recovered after reset).
	tryFix := func(eventName string, uhubctlPath string, enableNotify bool) bool {
		if health.Check(healthCfg) {
			return true
		}

		log.Printf("%s: camera not responding, attempting reset...", eventName)

		loc, err := reset.FindCamLink(uhubctlPath)
		if err != nil {
			log.Printf("ERROR: %v", err)
			return false
		}

		// Device is present and broken — now we notify.
		log.Printf("found Cam Link at hub %s port %s", loc.Hub, loc.Port)
		if enableNotify {
			notify.Send("Camera not responding, resetting...")
		}

		companion := reset.FindCompanionHub(uhubctlPath, loc)

		if reset.Run(uhubctlPath, loc, companion, healthCfg) {
			if enableNotify {
				notify.Send("Camera recovered successfully")
			}
			return true
		}

		if enableNotify {
			notify.Send("Camera reset failed — try unplugging Cam Link")
		}
		return false
	}

	handleEvent := func(eventName string, delay time.Duration) {
		if !resetting.CompareAndSwap(false, true) {
			log.Printf("reset already in progress, dropping %s event", eventName)
			return
		}
		defer resetting.Store(false)

		if delay > 0 {
			log.Printf("%s event — waiting %s before check", eventName, delay)
			time.Sleep(delay)
		} else {
			log.Printf("%s event — checking camera health", eventName)
		}

		if tryFix(eventName, *uhubctlPath, *enableNotify) {
			log.Printf("camera is healthy")
			return
		}

		// Camera didn't recover — enter retry loop, but only if the device
		// is actually on the bus. No point retrying if it's not plugged in.
		if !health.Listed(healthCfg) {
			log.Printf("device not present, skipping retries")
			return
		}

		log.Printf("entering retry loop (every %s, up to %d attempts)", *retryDelay, *maxRetries)
		for attempt := 1; attempt <= *maxRetries; attempt++ {
			time.Sleep(*retryDelay)
			if !health.Listed(healthCfg) {
				log.Printf("retry %d/%d: device disappeared, stopping retries", attempt, *maxRetries)
				return
			}
			log.Printf("retry %d/%d: checking camera health...", attempt, *maxRetries)
			if tryFix(fmt.Sprintf("%s/retry-%d", eventName, attempt), *uhubctlPath, *enableNotify) {
				log.Printf("camera recovered on retry %d", attempt)
				return
			}
		}
		log.Printf("giving up after %d retries", *maxRetries)
		if *enableNotify {
			notify.Send("Camera still not working after retries — try unplugging Cam Link")
		}
	}

	// If a previous reset was killed mid-cycle it may have left the Cam Link's
	// USB ports powered off. Power them back on before anything else so we
	// never start up staring at a camera we ourselves stranded dark.
	reset.Heal(*uhubctlPath)

	log.Printf("ready, waiting for events...")

	// Run one health check at startup so we catch a camera that's already
	// on the bus but broken (e.g. daemon restarted, or machine booted docked).
	go handleEvent("startup", 2*time.Second)

	// lastDemand tracks the most recent camera-demand-triggered check for the
	// cooldown. Only the select loop touches it, so no synchronization needed.
	var lastDemand time.Time

	for {
		select {
		case <-wakeCh:
			go handleEvent("wake", *wakeDelay)
		case <-usbCh:
			go handleEvent("usb-arrival", 2*time.Second)
		case ev := <-camCh:
			// A device-control grab means an app is actually trying to stream,
			// which is our "someone needs the camera" edge. Check the camera
			// right then, so a wedge that developed while the daemon was idle
			// gets fixed before the user notices. warm-open (mere enumeration)
			// and our own probes don't count.
			switch {
			case ev.Signal != "device-control" || isOwnProbe(ev.Process):
				log.Printf("camera activity (app=%q signal=%s) — not a trigger", ev.Process, ev.Signal)
			case time.Since(lastDemand) < demandCooldown:
				log.Printf("camera demand by %q — within %s cooldown, skipping check", ev.Process, demandCooldown)
			default:
				lastDemand = time.Now()
				go handleEvent(fmt.Sprintf("camera-demand (%s)", ev.Process), 0)
			}
		case <-usr1Ch:
			go handleEvent("manual (SIGUSR1)", 0)
		case sig := <-sigCh:
			log.Printf("received %s, shutting down", sig)
			cancel()
			return
		}
	}
}
