package host

import (
	"cmp"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// herdr pane ids such as w1:p1 are positional: they repeat across herdr sessions, and a
// restored session brings them back. Only the pane's terminal id is new each time, so
// keys are herdr-<session>.<pane>.<terminal>, as in herdr-default.w1-p1.term_65f5.
var herdr = host{
	name:   "herdr",
	prefix: "herdr",
	env:    "HERDR_PANE_ID",
	key: func(id string) string {
		session := herdrSession()
		terminal := herdrTerminal(session, id)
		if terminal == "" {
			return ""
		}
		return Sanitize(session) + "." + Sanitize(id) + "." + Sanitize(terminal)
	},
	label: func() string { return herdrLabel(herdrSession(), os.Getenv("HERDR_WORKSPACE_ID")) },
	title: func(body string) string { return herdrGet(herdrPane(body)).Label },
	panel: herdrPanel,
}

// herdrSession is the herdr session cairn runs in; herdr calls an unnamed one default.
func herdrSession() string { return cmp.Or(os.Getenv("HERDR_SESSION"), "default") }

// herdrPane splits a herdr key body into the session and the pane id; the session may
// hold dots, the pane and terminal ids never do.
func herdrPane(body string) (session, pane string) {
	parts := strings.Split(body, ".")
	if len(parts) < 3 {
		return "", ""
	}
	return strings.Join(parts[:len(parts)-2], "."), strings.ReplaceAll(parts[len(parts)-2], "-", ":")
}

type herdrPaneInfo struct {
	// Label is the name the pane was renamed to.
	Label      string `json:"label"`
	TerminalID string `json:"terminal_id"`
}

func herdrGet(session, pane string) herdrPaneInfo {
	var res struct {
		Result struct {
			Pane herdrPaneInfo `json:"pane"`
		} `json:"result"`
	}
	if pane != "" {
		decode(&res, "herdr", "--session", session, "pane", "get", pane)
	}
	return res.Result.Pane
}

// herdrTerminal is the terminal id of a pane, a var so tests need no herdr.
var herdrTerminal = func(session, pane string) string { return herdrGet(session, pane).TerminalID }

func herdrLabel(session, workspace string) string {
	if workspace == "" {
		return ""
	}
	var res struct {
		Result struct {
			Workspace struct {
				Label string `json:"label"`
			} `json:"workspace"`
		} `json:"result"`
	}
	decode(&res, "herdr", "--session", session, "workspace", "get", workspace)
	return res.Result.Workspace.Label
}

// herdrPanelRatio sizes the split that holds the panel.
const herdrPanelRatio = "0.35"

type herdrListed struct {
	ID    string `json:"pane_id"`
	Tab   string `json:"tab_id"`
	Label string `json:"label"`
}

// herdrPanel splits the panel off to the right of the pane, or closes the panel in the
// pane's tab.
func herdrPanel(body string, watch []string, close bool) error {
	session, pane := herdrPane(body)
	if pane == "" {
		return errNoPane("herdr", body)
	}
	herdr := func(args ...string) ([]byte, error) {
		return cli("", "herdr", slices.Concat([]string{"--session", session, "pane"}, args)...)
	}
	out, err := herdr("list")
	if err != nil {
		return err
	}
	var list struct {
		Result struct {
			Panes []herdrListed `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("herdr pane list: %w", err)
	}
	panes := list.Result.Panes
	i := slices.IndexFunc(panes, func(p herdrListed) bool { return p.ID == pane })
	if i < 0 {
		return errNoPane("herdr", pane)
	}
	tab := panes[i].Tab
	for _, p := range panes {
		if p.Tab == tab && p.Label == panelTitle {
			_, err := herdr("close", p.ID)
			return err
		}
	}
	if close {
		return nil
	}
	out, err = herdr("split", pane, "--direction", "right", "--ratio", herdrPanelRatio, "--no-focus")
	if err != nil {
		return err
	}
	var split struct {
		Result struct {
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &split) != nil || split.Result.Pane.ID == "" {
		return errors.New("herdr pane split: no pane in its answer")
	}
	id := split.Result.Pane.ID
	if _, err := herdr("rename", id, panelTitle); err != nil {
		return err
	}
	_, err = herdr("run", id, shellJoin(watch...))
	return err
}
