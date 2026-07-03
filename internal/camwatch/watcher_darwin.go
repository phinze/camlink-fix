package camwatch

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// predicate matches the CoreMediaIO DAL markers that a client process emits
// when it reaches for a camera. There isn't one clean "camera opened" line:
//
//   - "CheckOutInstance The System is starting" fires only on a *cold* start,
//     when the app is the first CMIO client after the system was torn down.
//   - "Get System unsuspended" fires on a *warm* open, when the DAL system was
//     already alive and just resumes to service the app.
//   - "setting deviceControlPID" fires when a client takes (or releases)
//     control of a specific camera device — the closest thing to "about to
//     stream".
//
// All three are logged by the client app itself (so processImagePath is the
// app, not a system daemon). We capture all three during this observe-only
// phase and tag each with which signal fired, so we can learn from real-world
// data which one best tracks "I actually joined a call" before wiring any of
// this to a reset.
const predicate = `subsystem == "com.apple.cmio" AND (` +
	`eventMessage CONTAINS "The System is starting" OR ` +
	`eventMessage CONTAINS "Get System unsuspended" OR ` +
	`eventMessage CONTAINS "setting deviceControlPID")`

// signalFor maps an eventMessage to a short signal name for logging/analysis.
// Order matters: the device-control marker is the most specific, so check it
// first.
func signalFor(msg string) string {
	switch {
	case strings.Contains(msg, "setting deviceControlPID"):
		return "device-control"
	case strings.Contains(msg, "The System is starting"):
		return "cold-start"
	case strings.Contains(msg, "Get System unsuspended"):
		return "warm-open"
	default:
		return "unknown"
	}
}

// debounceWindow collapses the burst of markers a single open produces (a warm
// open logs "Get System unsuspended" ~17 times in a few ms) into one Event per
// (process, signal).
const debounceWindow = 3 * time.Second

// Event describes a camera-open observed on the log stream.
type Event struct {
	// Process is the app that opened the camera subsystem, e.g. "Photo Booth".
	Process string
	// Signal is which CMIO marker fired: "cold-start", "warm-open", or
	// "device-control".
	Signal string
}

// logEntry is the subset of `log stream --style ndjson` fields we care about.
// process is often null in ndjson, so we fall back to processImagePath.
type logEntry struct {
	Process          string `json:"process"`
	ProcessImagePath string `json:"processImagePath"`
	EventMessage     string `json:"eventMessage"`
}

// Watch returns a channel that receives an Event each time an application opens
// the camera subsystem. It tails the unified log via `log stream` for the CMIO
// markers above.
//
// This is observe-only: it reports demand for a camera, it does not touch the
// device. The watcher stops when ctx is cancelled.
func Watch(ctx context.Context) <-chan Event {
	ch := make(chan Event, 16)

	go func() {
		cmd := exec.CommandContext(ctx, "log", "stream", "--style", "ndjson", "--predicate", predicate)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("camwatch: could not open log stream pipe: %v", err)
			return
		}
		if err := cmd.Start(); err != nil {
			log.Printf("camwatch: could not start log stream: %v", err)
			return
		}

		log.Printf("camwatch: listening for camera-open events")

		// lastSeen debounces the per-open burst, keyed by "process\x00signal".
		// Only the scanner goroutine touches it, so no lock is needed.
		lastSeen := make(map[string]time.Time)

		scanner := bufio.NewScanner(stdout)
		// Log entries can carry long backtraces; give the scanner room.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "{") {
				continue // skip the "Filtering the log data..." header and blanks
			}

			var e logEntry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				continue
			}

			proc := e.Process
			if proc == "" {
				proc = filepath.Base(e.ProcessImagePath)
			}
			signal := signalFor(e.EventMessage)

			key := proc + "\x00" + signal
			now := time.Now()
			if last, ok := lastSeen[key]; ok && now.Sub(last) < debounceWindow {
				lastSeen[key] = now
				continue
			}
			lastSeen[key] = now

			log.Printf("camwatch: camera activity: app=%q signal=%s", proc, signal)
			select {
			case ch <- Event{Process: proc, Signal: signal}:
			default:
			}
		}

		// Scanner ended: either ctx was cancelled (expected) or the stream died.
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			log.Printf("camwatch: log stream scanner error: %v", err)
		}
		_ = cmd.Wait()
		log.Println("camwatch: stopped")
	}()

	return ch
}
