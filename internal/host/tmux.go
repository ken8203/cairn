package host

import (
	"os"
	"strings"
)

// tmux numbers panes %n afresh with each server, so keys hold the server's pid too:
// tmux-<pid>.<n>.
var tmux = host{
	name:   "tmux",
	prefix: "tmux",
	env:    "TMUX_PANE",
	tab:    func() string { return tmuxWindow(os.Getenv("TMUX_PANE")) },
	key: func(id string) string {
		pid := tmuxServer(os.Getenv("TMUX"))
		if pid == "" {
			return ""
		}
		return pid + "." + Sanitize(id)
	},
	label: func() string { return tmuxDisplay(os.Getenv("TMUX_PANE"), "#S") },
	title: func(body string) string { return tmuxDisplay(tmuxPane(body), "#{pane_title}") },
	panel: tmuxPanel,
}

// tmuxServer is the server's pid in $TMUX, which is <socket>,<pid>,<session>.
func tmuxServer(env string) string {
	parts := strings.Split(env, ",")
	if len(parts) < 3 {
		return ""
	}
	pid := parts[len(parts)-2]
	if pid == "" || strings.Trim(pid, "0123456789") != "" {
		return ""
	}
	return pid
}

// tmuxPane is the pane a tmux key body names, as %n.
func tmuxPane(body string) string {
	if _, n, ok := strings.Cut(body, "."); ok && n != "" {
		return "%" + n
	}
	return ""
}

// tmuxWindow is the name of the window holding a pane once renamed, which turns off its
// automatic-rename; until then tmux names it after what runs there.
func tmuxWindow(pane string) string {
	auto, name, _ := strings.Cut(tmuxDisplay(pane, "#{automatic-rename} #W"), " ")
	if auto != "0" {
		return ""
	}
	return name
}

func tmuxDisplay(pane, format string) string {
	if pane == "" {
		return ""
	}
	b, _ := cli("", "tmux", "display", "-p", "-t", pane, format)
	return strings.TrimRight(string(b), "\n")
}

// tmuxPanel shows the panel in a popup over the pane. The popup is modal and q closes it,
// so there is nothing to toggle. display-popup waits for the popup to close, so a
// background run-shell starts it.
func tmuxPanel(body string, w watch, close bool) error {
	pane := tmuxPane(body)
	if pane == "" {
		return errNoPane("tmux", body)
	}
	if close {
		return nil
	}
	popup := shellJoin("tmux", "display-popup", "-E", "-w", "80%", "-h", "50%", "-t", pane, shellJoin(w.argv()...))
	_, err := cli("", "tmux", "run-shell", "-b", "-t", pane, popup)
	return err
}
