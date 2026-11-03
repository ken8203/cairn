package setup

import (
	"bytes"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// herdrPlugin is cairn's herdr plugin; its action is cairn.panel to a herdr keybinding.
const herdrPlugin = "cairn"

// herdrKeys binds the plugin's action in ~/.config/herdr/config.toml, which only the user
// edits.
const herdrKeys = `[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "cairn.panel"
description = "toggle cairn"`

type herdrManifest struct {
	ID              string         `toml:"id"`
	Name            string         `toml:"name"`
	Version         string         `toml:"version"`
	MinHerdrVersion string         `toml:"min_herdr_version"`
	Platforms       []string       `toml:"platforms"`
	Actions         []herdrCommand `toml:"actions"`
	Panes           []herdrCommand `toml:"panes"`
}

type herdrCommand struct {
	ID        string   `toml:"id"`
	Title     string   `toml:"title"`
	Contexts  []string `toml:"contexts,omitempty"`
	Placement string   `toml:"placement,omitempty"`
	Command   []string `toml:"command"`
}

// herdrManifestFor is the plugin's herdr-plugin.toml for the cairn binary at self: the
// panel action runs `cairn panel`, which opens the overlay pane running `cairn watch
// --from-env`. herdr runs both as argv from the plugin's folder.
func herdrManifestFor(self string) []byte {
	var b bytes.Buffer
	enc := toml.NewEncoder(&b)
	enc.Indent = ""
	enc.Encode(herdrManifest{
		ID:              herdrPlugin,
		Name:            "cairn",
		Version:         "1.0.0",
		MinHerdrVersion: "0.9.3",
		Platforms:       []string{"macos", "linux"},
		Actions:         []herdrCommand{{ID: "panel", Title: "Toggle cairn", Contexts: []string{"workspace"}, Command: []string{self, "panel"}}},
		Panes:           []herdrCommand{{ID: "panel", Title: "cairn", Placement: "overlay", Command: []string{self, "watch", "--from-env"}}},
	})
	return b.Bytes()
}

// herdrState is what herdr says about cairn's plugin.
type herdrState struct {
	present, linked, enabled bool
	err                      error
	// root is the folder the plugin is linked from, and cairn the binary its action runs.
	root, cairn string
}

func probeHerdr() herdrState {
	s := herdrState{present: onPath("herdr")}
	if !s.present {
		return s
	}
	argv := []string{"herdr", "plugin", "list", "--plugin", herdrPlugin, "--json"}
	type plugin struct {
		ID      string `json:"plugin_id"`
		Enabled bool   `json:"enabled"`
		Root    string `json:"plugin_root"`
		Actions []struct {
			ID      string   `json:"id"`
			Command []string `json:"command"`
		} `json:"actions"`
	}
	ps, err := list(argv, func(out []byte) ([]plugin, error) {
		var list struct {
			Result struct {
				Plugins []plugin `json:"plugins"`
			} `json:"result"`
		}
		err := json.Unmarshal(out, &list)
		return list.Result.Plugins, err
	})
	s.err = err
	for _, p := range ps {
		if p.ID != herdrPlugin {
			continue
		}
		s.linked, s.enabled, s.root = true, p.Enabled, p.Root
		for _, a := range p.Actions {
			if a.ID == "panel" && len(a.Command) > 0 {
				s.cairn = a.Command[0]
			}
		}
	}
	return s
}

func (s herdrState) check() Check {
	c := Check{Check: "herdr-plugin", Status: Warn}
	self, _ := os.Executable()
	switch {
	case s.err != nil:
		c.Detail = s.err.Error()
	case !s.linked:
		c.Detail, c.Fix = "not linked", "run `cairn setup`"
	case s.cairn != self:
		c.Detail, c.Fix = "linked to an older cairn path, "+s.cairn, "run `cairn setup`"
	case !s.enabled:
		c.Detail, c.Fix = "linked but disabled", "run `herdr plugin enable "+herdrPlugin+"`"
	default:
		c.Status, c.Detail = OK, "linked"
	}
	return c
}

// herdr writes the plugin into dir when it differs from what this binary would write, as
// after cairn moved, links it when herdr doesn't have it from dir or it was rewritten, and
// names the keybinding the user adds.
func (s steps) herdr(h *herdrState, dir string) {
	const step = "herdr-plugin"
	switch {
	case !h.present:
		s.line(Skip, "herdr", "herdr is not on PATH")
		return
	case h.err != nil:
		s.line(Manual, step, h.err.Error())
		return
	}
	self, err := os.Executable()
	if err != nil {
		s.line(Manual, step, "can't find this binary: "+err.Error())
		return
	}
	file := filepath.Join(dir, "herdr-plugin.toml")
	manifest := herdrManifestFor(self)
	old, _ := os.ReadFile(file)
	stale := !bytes.Equal(old, manifest)
	switch {
	case !stale:
	case s.dryRun:
		s.line(Skip, step, "dry run; would write "+file)
	default:
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			err = os.WriteFile(file, manifest, 0o644)
		}
		if err != nil {
			s.line(Manual, step, err.Error())
			return
		}
		s.line(Done, step, "wrote "+file)
	}
	// herdr keeps a copy of a linked manifest, so a rewritten one is linked again.
	if stale || !h.linked || !sameFile(h.root, dir) {
		if s.run(step, []string{"herdr", "plugin", "link", dir}) {
			h.linked, h.enabled, h.root, h.cairn = true, true, dir, self
		}
	} else if h.enabled {
		s.line(OK, step, "linked from "+dir)
	} else {
		s.line(Manual, step, "linked but disabled; run `herdr plugin enable "+herdrPlugin+"`")
	}
	s.line(Manual, "herdr-keys", "to toggle the panel with prefix+t, add to ~/.config/herdr/config.toml:\n  "+strings.ReplaceAll(herdrKeys, "\n", "\n  "))
}

func sameFile(a, b string) bool {
	fa, fb := stat(a), stat(b)
	return fa != nil && fb != nil && os.SameFile(fa, fb)
}
