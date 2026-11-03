package host

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// ErrNoPanel is Panel's error in a terminal without panes.
var ErrNoPanel = errors.New("this terminal has no API to open it; run `cairn watch` in a split")

// panelTitle names the pane, layer or terminal holding the panel, which is how a toggle
// finds it again.
const panelTitle = "cairn"

const cliTimeout = 10 * time.Second

// Panel toggles the trail panel, `cairn watch <key>`, beside the pane a key names, or the
// current pane without one, or with close only closes it. cairn is the absolute path of
// this binary.
func Panel(cairn, key string, close bool) error {
	if key == "" {
		if h, _ := current(); h.panel == nil {
			return ErrNoPanel
		}
		if key = Pane(); key == "" {
			return fmt.Errorf("%s can't name this pane", Name())
		}
	}
	h, body := lookup(hosts, key)
	return h.panel(body, []string{cairn, "watch", key}, close)
}

// errNoPane is a panel's error for a key that names no pane in its host.
func errNoPane(h, body string) error { return fmt.Errorf("%s: no pane in %q", h, body) }

//go:embed panel.lua
var rexPanelLua string

// rexPanel floats the panel over the window holding the block, or closes the floating
// layer that holds it. -b scopes the script to the block's session.
func rexPanel(id string, watch []string, close bool) error {
	block := "block:" + id
	args, err := json.Marshal(struct {
		Block   string   `json:"block"`
		Command []string `json:"command"`
		Close   bool     `json:"close"`
		Label   string   `json:"label"`
	}{block, watch, close, panelTitle})
	if err != nil {
		return err
	}
	_, err = cli(rexPanelLua, "rex", "do", "-", "-b", block, "--args", string(args))
	return err
}

// tmuxPanel shows the panel in a popup over the pane. The popup is modal and q closes it,
// so there is nothing to toggle. display-popup waits for the popup to close, so a
// background run-shell starts it.
func tmuxPanel(body string, watch []string, close bool) error {
	pane := tmuxPane(body)
	if pane == "" {
		return errNoPane("tmux", body)
	}
	if close {
		return nil
	}
	popup := shellJoin("tmux", "display-popup", "-E", "-w", "80%", "-h", "50%", "-t", pane, shellJoin(watch...))
	_, err := cli("", "tmux", "run-shell", "-b", "-t", pane, popup)
	return err
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

// hcPanelLabels are the labels Herdr.app places a terminal by: a terminal that carries
// another's space and tab is shown beside it.
var hcPanelLabels = []string{"herdr.space", "herdr.tab"}

// hcPanel runs the panel as a terminal beside this one, or ends the one running.
func hcPanel(body string, watch []string, close bool) error {
	addr := hcAddr(body)
	name := panelTitle + "-" + addr
	if hcLive(name) {
		// With --rm, a killed panel is removed at once and kill reports it gone.
		cli("", "hc", "kill", name, "--json")
		cli("", "hc", "rm", name, "--json")
		if hcLive(name) {
			return fmt.Errorf("hc: %s is still running", name)
		}
		return nil
	}
	if close {
		return nil
	}
	out, err := cli("", "hc", "get", addr, "--json")
	if err != nil {
		return err
	}
	var t struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(out, &t); err != nil {
		return fmt.Errorf("hc get: %w", err)
	}
	args := []string{"run", "--bg", "--rm", "--no-notify", "--name", name}
	for _, k := range hcPanelLabels {
		if v := t.Labels[k]; v != "" {
			args = append(args, "--label", k+"="+v)
		}
	}
	_, err = cli("", "hc", slices.Concat(args, []string{"--"}, watch)...)
	return err
}

func hcLive(name string) bool {
	var t struct {
		State string `json:"state"`
	}
	out, err := cli("", "hc", "get", name, "--json")
	return err == nil && json.Unmarshal(out, &t) == nil && t.State == "live"
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

// cli runs a host's CLI with stdin, failing on the first line it printed about the error.
func cli(stdin, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := cmp.Or(strings.TrimSpace(stderr.String()), strings.TrimSpace(string(out)), err.Error())
		line, _, _ := strings.Cut(msg, "\n")
		return out, fmt.Errorf("%s %s: %s", name, args[0], line)
	}
	return out, nil
}

// shellJoin quotes each word for sh.
func shellJoin(words ...string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
