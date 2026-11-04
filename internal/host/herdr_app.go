package host

import (
	"os"
	"os/exec"
	"path/filepath"
)

// HerdrPanel toggles the native floating mod. False means it is unavailable and
// the caller may use the hc terminal fallback.
func HerdrPanel(home string, close bool) (bool, error) {
	if _, err := exec.LookPath("herdr-app"); err != nil {
		return false, nil
	}
	for _, name := range []string{"main.js", "mod.json", "binary.js"} {
		if info, err := os.Stat(filepath.Join(home, "mods", "cairn", name)); err != nil || !info.Mode().IsRegular() {
			return false, nil
		}
	}
	if _, err := cli("", "herdr-app", "status"); err != nil {
		return false, nil
	}
	args := []string{"do", "mod.open", "--name", "cairn"}
	if close {
		args = []string{"do", "overlay.close", "--id", "mod-cairn"}
	}
	_, err := cli("", "herdr-app", args...)
	return true, err
}
