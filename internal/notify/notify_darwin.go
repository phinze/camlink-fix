package notify

import (
	"log"
	"os/exec"
)

// Send displays a macOS notification with the given message.
func Send(message string) {
	script := `display notification "` + message + `" with title "Cam Link Fix"`
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		log.Printf("notify: osascript failed: %v", err)
	}
}
