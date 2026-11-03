// Package host knows the terminal cairn runs in: the pane running it, a label for the
// work there, and the title of any pane cairn has recorded.
package host

import (
	"cmp"
	json "encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// host is a terminal whose panes cairn can tell apart.
type host struct {
	// name is what the user knows the host by.
	name string
	// prefix starts its pane keys, as <prefix>-<body>; Rex's keys have none.
	prefix string
	// env holds the current pane's id inside this host.
	env string
	// key is the body of a pane id's key, empty when it can't name the pane process alone.
	key func(id string) string
	// label names the work in the current pane.
	label func() string
	// title is the title of the pane a key body names.
	title func(body string) string
	// panel toggles the panel running watch beside the pane a key body names, or with
	// close only closes it.
	panel func(body string, watch []string, close bool) error
}

var rex = host{
	name: "Rex",
	env:  "REX_BLOCK",
	// Rex keys are bare block ids, as cairn has always stored them.
	key:   Key,
	label: func() string { return rexLabel(os.Getenv("REX_SESSION")) },
	title: rexTitle,
	panel: rexPanel,
}

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

// tmux numbers panes %n afresh with each server, so keys hold the server's pid too:
// tmux-<pid>.<n>.
var tmux = host{
	name:   "tmux",
	prefix: "tmux",
	env:    "TMUX_PANE",
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

// hc is Herdr.app's terminal runtime, a different stack from the herdr TUI. Its
// addresses may be handed out again, but HC_SCOPE is new in every terminal, so keys are
// hc-<address>.<scope>.
var hc = host{
	name:   "Herdr.app",
	prefix: "hc",
	env:    "HC_TERMINAL_ID",
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

var orca = host{
	name:   "Orca",
	prefix: "orca",
	env:    "ORCA_TERMINAL_HANDLE",
	key:    Sanitize,
	label:  func() string { return orcaLabel(os.Getenv("ORCA_WORKTREE_ID")) },
	title:  orcaTitle,
	panel:  orcaPanel,
}

// hosts in detection order, innermost first: herdr usually runs inside another terminal
// or tmux, tmux inside a terminal app, and either may run in Herdr.app. Any other
// terminal has no panes. Rex, which has no prefix, comes last.
var hosts = []host{herdr, tmux, hc, orca, rex}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Sanitize turns what a file name can't hold into -.
func Sanitize(s string) string { return strings.Trim(unsafeName.ReplaceAllString(s, "-"), "-") }

// current is the host cairn runs in and the pane id it gives, if any.
func current() (host, string) {
	for _, h := range hosts {
		if id := os.Getenv(h.env); id != "" {
			return h, id
		}
	}
	return host{}, ""
}

// Pane is the key of the pane cairn runs in, or empty outside a host with panes.
func Pane() string {
	h, id := current()
	if id == "" {
		return ""
	}
	body := h.key(id)
	if body == "" || h.prefix == "" {
		return body
	}
	return h.prefix + "-" + body
}

// Name is the host whose pane cairn runs in, from the environment alone, or empty; Pane
// may still find no pane there.
func Name() string {
	h, _ := current()
	return h.name
}

// Label names the work in the current pane, such as its Rex session's label, or is empty.
func Label() string {
	h, id := current()
	if id == "" {
		return ""
	}
	return h.label()
}

// Key is the pane key a user names: a key as stored, or a Rex block with its block: prefix.
func Key(s string) string { return strings.TrimPrefix(s, "block:") }

// Title is the title of the pane a stored key names, or empty when its host doesn't know it.
func Title(key string) string { return title(hosts, key) }

func title(hs []host, key string) string {
	h, body := lookup(hs, key)
	return h.title(body)
}

// lookup is the host a stored key belongs to, by its prefix, and the key's body; a key
// without one is Rex's, the last host.
func lookup(hs []host, key string) (host, string) {
	for _, h := range hs {
		if body, ok := strings.CutPrefix(key, h.prefix+"-"); ok {
			return h, body
		}
	}
	return hs[len(hs)-1], key
}

func rexTitle(block string) string {
	var res struct {
		Title string `json:"title"`
	}
	decode(&res, "rex", "block", "call", "-s", os.Getenv("REX_SESSION"), "-b", "block:"+block, "title", "{}")
	return res.Title
}

func rexLabel(session string) string {
	if session == "" {
		return ""
	}
	var res struct {
		Label string `json:"label"`
	}
	decode(&res, "rex", "session", "inspect", session, "--json")
	return res.Label
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

func tmuxDisplay(pane, format string) string {
	if pane == "" {
		return ""
	}
	b, _ := exec.Command("tmux", "display", "-p", "-t", pane, format).Output()
	return strings.TrimRight(string(b), "\n")
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

// decode decodes what a CLI prints into out, leaving out as it was when the CLI fails.
func decode(out any, name string, args ...string) {
	if b, err := exec.Command(name, args...).Output(); err == nil {
		json.Unmarshal(b, out)
	}
}
