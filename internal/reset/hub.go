package reset

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
)

// Location identifies where a device is in the USB hub tree.
type Location struct {
	Hub  string
	Port string
}

var (
	hubRe  = regexp.MustCompile(`^Current status for hub ([0-9.\-]+)\s+\[([0-9a-f:]+)`)
	portRe = regexp.MustCompile(`^\s+Port\s+(\d+):`)
)

// FindCamLink discovers the Cam Link's hub location and port by parsing
// uhubctl output. Returns the location or an error if not found.
func FindCamLink(uhubctlPath string) (Location, error) {
	out, err := exec.Command(uhubctlPath).Output()
	if err != nil {
		// uhubctl may return non-zero even on success; use combined output
		out, err = exec.Command(uhubctlPath).CombinedOutput()
		if err != nil && len(out) == 0 {
			return Location{}, fmt.Errorf("uhubctl failed: %w", err)
		}
	}

	var currentHub string
	for _, line := range strings.Split(string(out), "\n") {
		if m := hubRe.FindStringSubmatch(line); m != nil {
			currentHub = m[1]
		}
		if m := portRe.FindStringSubmatch(line); m != nil {
			port := m[1]
			if strings.Contains(line, "Cam Link") && currentHub != "" {
				return Location{Hub: currentHub, Port: port}, nil
			}
		}
	}

	return Location{}, fmt.Errorf("Cam Link not found in USB hub tree")
}

// FindCompanionHub finds the companion USB 2.0/3.0 hub for a given hub
// location. VIA Labs hubs have USB2 (2109:2813) and USB3 (2109:0813)
// companions that share port topology.
func FindCompanionHub(uhubctlPath string, loc Location) string {
	out, err := exec.Command(uhubctlPath).CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}

	var currentHub, currentVID string
	for _, line := range strings.Split(string(out), "\n") {
		if m := hubRe.FindStringSubmatch(line); m != nil {
			currentHub = m[1]
			currentVID = m[2]
		}

		// Look for VIA Labs companion hubs
		if currentVID == "2109:2813" || currentVID == "2109:0813" {
			if currentHub != loc.Hub {
				if m := portRe.FindStringSubmatch(line); m != nil {
					if m[1] == loc.Port {
						log.Printf("reset: found companion hub at %s", currentHub)
						return currentHub
					}
				}
			}
		}
	}

	return ""
}
