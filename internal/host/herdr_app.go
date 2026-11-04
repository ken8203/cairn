package host

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// HerdrPanel toggles the native mod in its saved mode. False means it is unavailable and
// the caller may use the hc terminal fallback.
func HerdrPanel(home string, close bool) (bool, error) {
	if _, err := exec.LookPath("herdr-app"); err != nil {
		return false, nil
	}
	for _, name := range []string{"main.js", "mod.json", "binary.js", "dock.js"} {
		if info, err := os.Stat(filepath.Join(home, "mods", "cairn", name)); err != nil || !info.Mode().IsRegular() {
			return false, nil
		}
	}
	status, err := cli("", "herdr-app", "status")
	if err != nil {
		return false, nil
	}
	args := []string{"do", "mod.open", "--name", "cairn"}
	if close {
		_, overlayErr := cli("", "herdr-app", "do", "overlay.close", "--id", "mod-cairn")
		return true, errors.Join(overlayErr, hideHerdrDock(home, status))
	}
	_, err = cli("", "herdr-app", args...)
	return true, err
}

// The layout file supplies node IDs only; layout.hide updates native runtime state.
func hideHerdrDock(home string, status []byte) error {
	var s struct {
		Layout string `json:"layout"`
	}
	if err := json.Unmarshal(status, &s); err != nil {
		return fmt.Errorf("herdr-app status: %w", err)
	}
	if s.Layout == "" {
		return nil
	}
	if s.Layout == "." || s.Layout == ".." || filepath.Base(s.Layout) != s.Layout {
		return fmt.Errorf("herdr-app: invalid layout name %q", s.Layout)
	}
	b, err := os.ReadFile(filepath.Join(home, "layouts", s.Layout+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var root herdrLayoutNode
	if err := json.Unmarshal(b, &root); err != nil {
		return fmt.Errorf("herdr-app layout: %w", err)
	}
	return root.hideCairn()
}

type herdrLayoutNode struct {
	ID      string `json:"id"`
	Content struct {
		Mod   string `json:"mod"`
		Props struct {
			Mode string `json:"cairnMode"`
		} `json:"props"`
	} `json:"content"`
	Children []herdrLayoutNode `json:"children"`
}

func (n herdrLayoutNode) hideCairn() error {
	var errs []error
	if n.ID != "" && n.Content.Mod == "cairn" && n.Content.Props.Mode == "dock" {
		_, err := cli("", "herdr-app", "do", "layout.hide", "--id", n.ID)
		errs = append(errs, err)
	}
	for _, child := range n.Children {
		errs = append(errs, child.hideCairn())
	}
	return errors.Join(errs...)
}
