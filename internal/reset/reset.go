package reset

import (
	"log"
	"os/exec"
	"time"

	"github.com/phinze/camlink-fix/internal/health"
)

// stage defines one escalating reset attempt.
type stage struct {
	name     string
	offTime  time.Duration
	bothPorts bool
}

var stages = []stage{
	{"quick cycle", 2 * time.Second, false},
	{"full reset", 10 * time.Second, true},
	{"extended reset", 30 * time.Second, true},
}

// Run executes the escalating reset strategy. Returns true if the camera
// recovers at any stage.
func Run(uhubctlPath string, loc Location, companionHub string, healthCfg health.Config) bool {
	for _, s := range stages {
		log.Printf("reset: trying %s (%s off)...", s.name, s.offTime)

		if s.bothPorts && companionHub != "" {
			// Power off both ports
			hubctl(uhubctlPath, loc.Hub, loc.Port, "off")
			hubctl(uhubctlPath, companionHub, loc.Port, "off")
			time.Sleep(s.offTime)
			hubctl(uhubctlPath, loc.Hub, loc.Port, "on")
			hubctl(uhubctlPath, companionHub, loc.Port, "on")
		} else {
			// Quick cycle using uhubctl's built-in cycle
			hubctl(uhubctlPath, loc.Hub, loc.Port, "cycle")
		}

		// Wait for device to settle
		settleTime := 3 * time.Second
		if s.bothPorts {
			settleTime = 5 * time.Second
		}
		time.Sleep(settleTime)

		if health.Check(healthCfg) {
			log.Printf("reset: camera recovered after %s", s.name)
			return true
		}
	}

	log.Printf("reset: camera still not working after all reset stages")
	return false
}

func hubctl(uhubctlPath, hub, port, action string) {
	cmd := exec.Command(uhubctlPath, "-l", hub, "-p", port, "-a", action)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("reset: uhubctl %s %s port %s: %v: %s", action, hub, port, err, out)
	}
}
