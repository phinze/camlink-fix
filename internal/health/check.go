package health

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Config holds paths and parameters for camera health checks.
type Config struct {
	FFmpegPath string
	DeviceName string
	Timeout    time.Duration
}

// Check returns true if the camera is detected and can produce frames.
func Check(cfg Config) bool {
	if !isListed(cfg.DeviceName) {
		log.Printf("health: %q not found in system_profiler", cfg.DeviceName)
		return false
	}

	if !canCapture(cfg) {
		log.Printf("health: %q failed frame capture", cfg.DeviceName)
		return false
	}

	return true
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

// canCapture tries to grab a single frame from the camera via ffmpeg.
func canCapture(cfg Config) bool {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.FFmpegPath,
		"-f", "avfoundation",
		"-video_size", "1920x1080",
		"-framerate", "59.94",
		"-i", cfg.DeviceName,
		"-frames:v", "1",
		"-f", "null", "-",
	)

	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
