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
		kick        = flag.Bool("kick", false, "Send SIGUSR1 to a running camlink-fix daemon to trigger an immediate check")
		uhubctlPath = flag.String("uhubctl-path", "uhubctl", "Path to uhubctl binary")
		ffmpegPath  = flag.String("ffmpeg-path", "ffmpeg", "Path to ffmpeg binary")
		deviceName  = flag.String("device-name", "Cam Link 4K", "Camera device name as shown in system_profiler")
		wakeDelay   = flag.Duration("wake-delay", 5*time.Second, "Delay after wake before checking camera")
		enableNotify = flag.Bool("notify", true, "Send macOS notifications")
	)
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[camlink-fix] ")

	if *kick {
		kickDaemon()
		return
	}

	log.Printf("starting (device=%q, wake-delay=%s)", *deviceName, *wakeDelay)

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

	healthCfg := health.Config{
		FFmpegPath: *ffmpegPath,
		DeviceName: *deviceName,
		Timeout:    3 * time.Second,
	}

	// Debounce: only one reset at a time
	var resetting atomic.Bool

	handleEvent := func(eventName string, delay time.Duration) {
		if !resetting.CompareAndSwap(false, true) {
			log.Printf("reset already in progress, dropping %s event", eventName)
			return
		}
		defer resetting.Store(false)

		log.Printf("%s event — waiting %s before check", eventName, delay)
		time.Sleep(delay)

		log.Printf("checking camera health...")
		if health.Check(healthCfg) {
			log.Printf("camera is healthy")
			return
		}

		log.Printf("camera not responding, attempting reset...")
		if *enableNotify {
			notify.Send("Camera not responding, resetting...")
		}

		loc, err := reset.FindCamLink(*uhubctlPath)
		if err != nil {
			log.Printf("ERROR: %v", err)
			if *enableNotify {
				notify.Send("Could not find Cam Link in USB hub tree")
			}
			return
		}

		log.Printf("found Cam Link at hub %s port %s", loc.Hub, loc.Port)
		companion := reset.FindCompanionHub(*uhubctlPath, loc)

		if reset.Run(*uhubctlPath, loc, companion, healthCfg) {
			if *enableNotify {
				notify.Send("Camera recovered successfully")
			}
		} else {
			if *enableNotify {
				notify.Send("Camera reset failed — try unplugging Cam Link")
			}
		}
	}

	log.Printf("ready, waiting for events...")

	for {
		select {
		case <-wakeCh:
			go handleEvent("wake", *wakeDelay)
		case <-usbCh:
			go handleEvent("usb-arrival", 2*time.Second)
		case <-usr1Ch:
			go handleEvent("manual (SIGUSR1)", 0)
		case sig := <-sigCh:
			log.Printf("received %s, shutting down", sig)
			cancel()
			return
		}
	}
}
