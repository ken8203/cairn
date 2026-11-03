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
	tab:    func() string { return herdrTab(herdrSession(), os.Getenv("HERDR_PANE_ID")) },
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

// herdrBin is the herdr CLI: the one that started a plugin action, else herdr on PATH.
func herdrBin() string { return cmp.Or(os.Getenv("HERDR_BIN_PATH"), "herdr") }

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
	Tab        string `json:"tab_id"`
}

func herdrGet(session, pane string) herdrPaneInfo {
	var res struct {
		Result struct {
			Pane herdrPaneInfo `json:"pane"`
		} `json:"result"`
	}
	if pane != "" {
		decode(&res, herdrBin(), "--session", session, "pane", "get", pane)
	}
	return res.Result.Pane
}

// herdrTerminal is the terminal id of a pane, a var so tests need no herdr.
var herdrTerminal = func(session, pane string) string { return herdrGet(session, pane).TerminalID }

// herdrTab is the label of the tab holding a pane, unless it is still the number herdr
// gave it.
func herdrTab(session, pane string) string {
	tab := herdrGet(session, pane).Tab
	if tab == "" {
		return ""
	}
	var res struct {
		Result struct {
			Tab struct {
				Label string `json:"label"`
			} `json:"tab"`
		} `json:"result"`
	}
	decode(&res, herdrBin(), "--session", session, "tab", "get", tab)
	return named(res.Result.Tab.Label)
}

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
	decode(&res, herdrBin(), "--session", session, "workspace", "get", workspace)
	return res.Result.Workspace.Label
}

// herdrPanelRatio sizes the split that holds the panel.
const herdrPanelRatio = "0.35"

type herdrListed struct {
	ID    string `json:"pane_id"`
	Tab   string `json:"tab_id"`
	Label string `json:"label"`
}

// cairn's herdr plugin, which cairn setup links: its overlay pane runs
// `cairn watch --from-env` with the key and folder in CAIRN_PANE and CAIRN_DIR.
const (
	herdrPlugin     = "cairn"
	herdrEntrypoint = "panel"
)

// herdrPanel opens the panel as the cairn plugin's overlay over the pane, else splits it
// off to the right, or closes the panel in the pane's tab. Run as the plugin's action,
// the folder is the focused pane's, the action's own cwd being the plugin's.
func herdrPanel(body string, w watch, close bool) error {
	session, pane := herdrPane(body)
	if pane == "" {
		return errNoPane("herdr", body)
	}
	bin := herdrBin()
	herdr := func(args ...string) ([]byte, error) {
		return cli("", bin, slices.Concat([]string{"--session", session}, args)...)
	}
	out, err := herdr("pane", "list")
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
			_, err := herdr("pane", "close", p.ID)
			return err
		}
	}
	if close {
		return nil
	}
	w.dir = cmp.Or(herdrActionCwd(), w.dir)
	if herdrLinked(herdr) {
		args := []string{"plugin", "pane", "open", "--plugin", herdrPlugin, "--entrypoint", herdrEntrypoint, "--placement", "overlay", "--env", "CAIRN_PANE=" + w.key}
		if w.dir != "" {
			args = append(args, "--env", "CAIRN_DIR="+w.dir)
		}
		_, err := herdr(args...)
		return err
	}
	out, err = herdr("pane", "split", pane, "--direction", "right", "--ratio", herdrPanelRatio, "--no-focus")
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
	if _, err := herdr("pane", "rename", id, panelTitle); err != nil {
		return err
	}
	_, err = herdr("pane", "run", id, shellJoin(w.argv()...))
	return err
}

// herdrLinked reports whether cairn's plugin is linked and enabled; when herdr can't say,
// the panel falls back to a split.
func herdrLinked(herdr func(args ...string) ([]byte, error)) bool {
	out, err := herdr("plugin", "list", "--plugin", herdrPlugin, "--json")
	if err != nil {
		return false
	}
	type plugin struct {
		ID      string `json:"plugin_id"`
		Enabled bool   `json:"enabled"`
	}
	var list struct {
		Result struct {
			Plugins []plugin `json:"plugins"`
		} `json:"result"`
	}
	json.Unmarshal(out, &list)
	return slices.ContainsFunc(list.Result.Plugins, func(p plugin) bool { return p.ID == herdrPlugin && p.Enabled })
}

// herdrActionCwd is the focused pane's folder when cairn runs as a plugin action.
func herdrActionCwd() string {
	var ctx struct {
		Cwd string `json:"focused_pane_cwd"`
	}
	json.Unmarshal([]byte(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")), &ctx)
	return ctx.Cwd
}
