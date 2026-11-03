package host

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var orca = host{
	name:   "Orca",
	prefix: "orca",
	env:    "ORCA_TERMINAL_HANDLE",
	key:    Sanitize,
	label:  func() string { return orcaLabel(os.Getenv("ORCA_WORKTREE_ID")) },
	title:  orcaTitle,
	panel:  orcaPanel,
}

func orcaTitle(handle string) string {
	var res struct {
		Result struct {
			Terminal struct {
				Title string `json:"title"`
			} `json:"terminal"`
		} `json:"result"`
	}
	decode(&res, "orca", "terminal", "show", "--terminal", handle, "--json")
	return res.Result.Terminal.Title
}

// orcaLabel is the worktree's directory name, from an id of the form <uuid>::<path>.
func orcaLabel(worktree string) string {
	_, path, ok := strings.Cut(worktree, "::")
	if !ok || path == "" {
		return ""
	}
	return filepath.Base(path)
}

type orcaTerminal struct {
	Handle string `json:"handle"`
	Tab    string `json:"tabId"`
	Title  string `json:"paneTitle"`
}

// orcaPanel splits the panel off the terminal, or closes the panel in the terminal's tab.
func orcaPanel(handle string, watch []string, close bool) error {
	out, err := cli("", "orca", "terminal", "list", "--json")
	if err != nil {
		return err
	}
	var list struct {
		Result struct {
			Terminals []orcaTerminal `json:"terminals"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("orca terminal list: %w", err)
	}
	ts := list.Result.Terminals
	if i := slices.IndexFunc(ts, func(t orcaTerminal) bool { return t.Handle == handle }); i >= 0 {
		tab := ts[i].Tab
		for _, t := range ts {
			if t.Tab == tab && t.Title == panelTitle {
				_, err := cli("", "orca", "terminal", "close", "--terminal", t.Handle)
				return err
			}
		}
	}
	if close {
		return nil
	}
	out, err = cli("", "orca", "terminal", "split", "--terminal", handle, "--direction", "horizontal", "--command", shellJoin(watch...), "--json")
	if err != nil {
		return err
	}
	var split struct {
		Result struct {
			Split orcaTerminal `json:"split"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &split) != nil || split.Result.Split.Handle == "" {
		return errors.New("orca terminal split: no terminal in its answer")
	}
	_, err = cli("", "orca", "terminal", "set-pane-title", "--terminal", split.Result.Split.Handle, "--title", panelTitle)
	return err
}
