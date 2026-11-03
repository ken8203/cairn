package host

import (
	"cmp"
	json "encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"strings"
)

// hc is Herdr.app's terminal runtime, a different stack from the herdr TUI. Its
// addresses may be handed out again, but HC_SCOPE is new in every terminal, so keys are
// hc-<address>.<scope>.
var hc = host{
	name:   "Herdr.app",
	prefix: "hc",
	env:    "HC_TERMINAL_ID",
	tab:    func() string { return named(hcTab(hcGet(os.Getenv("HC_TERMINAL_ID")).Labels.Where)) },
	key: func(addr string) string {
		scope := Sanitize(os.Getenv("HC_SCOPE"))
		if scope == "" {
			return ""
		}
		return Sanitize(addr) + "." + scope
	},
	label: func() string { return hcSpace(hcGet(os.Getenv("HC_TERMINAL_ID")).Labels.Where) },
	title: func(body string) string {
		t := hcGet(hcAddr(body))
		return cmp.Or(t.Name, t.Title)
	},
	panel: hcPanel,
}

type hcTerminal struct {
	Title  string `json:"title"`
	Name   string `json:"name"`
	Labels struct {
		// Where is "<space> / <tab>" for a terminal shown in the app.
		Where string `json:"where"`
	} `json:"labels"`
}

func hcGet(addr string) hcTerminal {
	var t hcTerminal
	if addr != "" {
		decode(&t, "hc", "get", addr, "--json")
	}
	return t
}

// hcAddr is the terminal address an hc key body names.
func hcAddr(body string) string {
	addr, _, _ := strings.Cut(body, ".")
	return addr
}

// hcSpace is the space in a terminal's where label.
func hcSpace(where string) string {
	space, _, _ := strings.Cut(where, " / ")
	return strings.TrimSpace(space)
}

// hcTab is the tab in a terminal's where label; one nobody renamed is its number.
func hcTab(where string) string {
	_, tab, _ := strings.Cut(where, " / ")
	return strings.TrimSpace(tab)
}

// hcPanelLabels are the labels Herdr.app places a terminal by: a terminal that carries
// another's space and tab is shown beside it.
var hcPanelLabels = []string{"herdr.space", "herdr.tab"}

// hcPanel runs the panel as a terminal beside this one, or ends the one running.
func hcPanel(body string, w watch, close bool) error {
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
	_, err = cli("", "hc", slices.Concat(args, []string{"--"}, w.argv())...)
	return err
}

func hcLive(name string) bool {
	var t struct {
		State string `json:"state"`
	}
	out, err := cli("", "hc", "get", name, "--json")
	return err == nil && json.Unmarshal(out, &t) == nil && t.State == "live"
}
