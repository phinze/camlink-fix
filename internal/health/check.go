package health

import (
	"context"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Config holds paths and parameters for camera health checks.
type Config struct {
	FFmpegPath string
	DeviceName string
	Timeout    time.Duration
}

// Listed returns true if the camera appears in system_profiler output.
func Listed(cfg Config) bool {
	return isListed(cfg.DeviceName)
}

// Check returns true if the camera is detected and can produce a frame at its
// currently-advertised mode.
//
// The subtle part: a Cam Link advertises a *different* capture mode depending
// on whether an HDMI source is present (e.g. 1920x1080@59.94 locked to a
// 1080p60 source, but 3840x2160@30 with no signal). A healthy device produces
// a frame at whichever mode it currently advertises — with no source it emits
// the "no signal" pane, which comes across the capture stream just like real
// video. Only a genuinely wedged device fails to produce a frame at its own
// advertised mode.
//
// So instead of demanding one hardcoded format (which false-fails a perfectly
// healthy camera the instant the mode isn't exactly that), we ask the device
// what it's offering and grab a frame at that. This is what lets us tell
// "wedged, reset it" apart from "idle with no signal, leave it alone".
func Check(cfg Config) bool {
	if !isListed(cfg.DeviceName) {
		log.Printf("health: %q not found in system_profiler", cfg.DeviceName)
		return false
	}

	return canCapture(cfg)
}

// isListed checks whether the device appears in system_profiler output.
func isListed(deviceName string) bool {
	out, err := exec.Command("system_profiler", "SPCameraDataType").Output()
	if err != nil {
		log.Printf("health: system_profiler failed: %v", err)
		return false
	}
	return strings.Contains(string(out), deviceName)
}

// modeRe matches a mode line from ffmpeg's "Supported modes:" list, e.g.
//
//	1920x1080@[59.940180 59.940180]fps
//
// capturing the resolution and the (first) framerate.
var modeRe = regexp.MustCompile(`(\d+x\d+)@\[([0-9.]+)`)

// detectMode asks ffmpeg what mode the device currently advertises by
// requesting a deliberately-invalid size (1x1). ffmpeg responds by printing
// the device's "Supported modes:" list, which reflects the current
// source/no-signal state. Returns the first advertised mode. ok is false if
// the device reported no modes at all, which is itself a strong sign it's
// wedged (a live device always answers with its capabilities).
func detectMode(cfg Config) (size, framerate, output string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutOr(cfg, 3*time.Second))
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.FFmpegPath,
		"-f", "avfoundation",
		"-video_size", "1x1",
		"-i", cfg.DeviceName,
		"-frames:v", "1",
		"-f", "null", "-",
	)
	// This call always exits non-zero (1x1 is never valid); we want its stderr.
	out, _ := cmd.CombinedOutput()
	output = string(out)

	m := modeRe.FindStringSubmatch(output)
	if m == nil {
		return "", "", output, false
	}
	return m[1], m[2], output, true
}

// canCapture detects the device's advertised mode and tries to grab a single
// frame at it. A healthy device delivers one near-instantly (tens of ms); a
// wedged device does not. ffmpeg's stderr is logged on any failure so we can
// build up a library of real-world wedge signatures.
func canCapture(cfg Config) bool {
	size, framerate, detectOut, ok := detectMode(cfg)
	if !ok {
		log.Printf("health: %q advertised no modes (likely wedged); ffmpeg said:\n%s",
			cfg.DeviceName, lastLines(detectOut, 12))
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeoutOr(cfg, 3*time.Second))
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.FFmpegPath,
		"-f", "avfoundation",
		"-video_size", size,
		"-framerate", framerate,
		"-i", cfg.DeviceName,
		"-frames:v", "1",
		"-f", "null", "-",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("health: %q failed frame capture at %s@%s; ffmpeg said:\n%s",
			cfg.DeviceName, size, framerate, lastLines(string(out), 12))
		return false
	}
	return true
}

func timeoutOr(cfg Config, def time.Duration) time.Duration {
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}
	return def
}

// lastLines returns the final n non-empty lines of s, trimmed, so failure logs
// carry the useful ffmpeg error tail without the full banner.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
