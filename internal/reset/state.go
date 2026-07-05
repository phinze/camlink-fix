package reset

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// stateFile records the last-known hub location of the Cam Link so a reset that
// gets killed mid-cycle (leaving ports powered off) can be healed on the next
// daemon start. It lives in the temp dir on purpose: we only need it to survive
// a process restart within a boot session, and a reboot re-powers USB anyway.
var stateFile = filepath.Join(os.TempDir(), "camlink-fix.location.json")

type savedLocation struct {
	Hub       string `json:"hub"`
	Port      string `json:"port"`
	Companion string `json:"companion"`
}

// saveLocation persists where the Cam Link lives so Heal can find its ports
// even when the device is currently powered off (and thus invisible to
// uhubctl's device scan).
func saveLocation(loc Location, companion string) {
	data, err := json.Marshal(savedLocation{Hub: loc.Hub, Port: loc.Port, Companion: companion})
	if err != nil {
		return
	}
	if err := os.WriteFile(stateFile, data, 0o644); err != nil {
		log.Printf("reset: could not persist location: %v", err)
	}
}

func loadLocation() (savedLocation, bool) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return savedLocation{}, false
	}
	var s savedLocation
	if err := json.Unmarshal(data, &s); err != nil {
		return savedLocation{}, false
	}
	if s.Hub == "" || s.Port == "" {
		return savedLocation{}, false
	}
	return s, true
}

// Heal powers the last-known Cam Link ports back on. It's a no-op if the ports
// are already on, so it's safe to call unconditionally at startup. This is the
// backstop for a reset that was interrupted (crash, SIGKILL) after powering
// ports off but before turning them back on — the exact way an aborted reset
// can strand the camera dark. KeepAlive restarts the daemon, startup calls
// Heal, and the ports come back.
func Heal(uhubctlPath string) {
	s, ok := loadLocation()
	if !ok {
		return
	}
	log.Printf("reset: healing — ensuring Cam Link ports at %s/%s are powered on", s.Hub, s.Port)
	hubctl(uhubctlPath, s.Hub, s.Port, "on")
	if s.Companion != "" {
		hubctl(uhubctlPath, s.Companion, s.Port, "on")
	}
}
