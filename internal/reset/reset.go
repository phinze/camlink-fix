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
	// Remember where the device lives so a killed reset can be healed on the
	// next startup — once ports are off, uhubctl can't find the device to
	// locate it again.
	saveLocation(loc, companionHub)

	for _, s := range stages {
		log.Printf("reset: trying %s (%s off)...", s.name, s.offTime)

		if s.bothPorts && companionHub != "" {
			cycleBothPorts(uhubctlPath, loc.Hub, companionHub, loc.Port, s.offTime)
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

// cycleBothPorts powers the device's USB3 hub port and its USB2 companion off
// for offTime, then back on. The power-on is deferred so it runs even if the
// off window is interrupted by a panic — a reset must never leave the ports
// dark. (SIGKILL can't be caught; that case is covered by Heal at startup.)
func cycleBothPorts(uhubctlPath, hub, companion, port string, offTime time.Duration) {
	defer func() {
		hubctl(uhubctlPath, hub, port, "on")
		hubctl(uhubctlPath, companion, port, "on")
	}()

	hubctl(uhubctlPath, hub, port, "off")
	hubctl(uhubctlPath, companion, port, "off")
	time.Sleep(offTime)
}

func hubctl(uhubctlPath, hub, port, action string) {
	cmd := exec.Command(uhubctlPath, "-l", hub, "-p", port, "-a", action)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("reset: uhubctl %s %s port %s: %v: %s", action, hub, port, err, out)
	}
}
