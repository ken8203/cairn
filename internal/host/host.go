// Package host knows the terminal cairn runs in: the pane running it, a label for the
// work there, and the title of any pane cairn has recorded.
package host

import (
	"bytes"
	"cmp"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
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
	// tab is the name the user gave the current pane's tab, or empty when they gave none.
	tab func() string
	// label names the wider place the current pane is in, such as its session.
	label func() string
	// title is the title of the pane a key body names.
	title func(body string) string
	// holds reports whether the pane a key body names runs cairn; nil trusts the env.
	holds func(body string) bool
	// dir is the folder of the program in the foreground of the pane a key body names.
	dir func(body string) string
	// panel toggles the panel running w beside the pane a key body names, or with close
	// only closes it.
	panel func(body string, w watch, close bool) error
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

// Tab is the name the user gave the current pane's tab, such as a renamed Rex window, or
// empty when they gave none. One tab holds one piece of work, so it names a trail best.
func Tab() string {
	h, id := current()
	if id == "" {
		return ""
	}
	return h.tab()
}

// Label names the wider place the current pane is in, such as its Rex session, or is
// empty. A session holds many tabs, so it names a trail after Tab and the PR's branch.
func Label() string {
	h, id := current()
	if id == "" {
		return ""
	}
	return h.label()
}

// named is a tab's name, or empty for one still called by its number, as herdr and
// Herdr.app call a tab nobody named.
func named(name string) string {
	if strings.Trim(name, "0123456789") == "" {
		return ""
	}
	return name
}

// Key validates a stored pane key or Rex block, and resolves hc-t1 on the local core.
// Invalid or unavailable keys return empty.
func Key(s string) string {
	s = strings.TrimPrefix(s, "block:")
	if s == "" || s == "." || s == ".." || Sanitize(s) != s {
		return ""
	}
	if body, ok := strings.CutPrefix(s, "hc-"); ok {
		addr, suffix, full := strings.Cut(body, ".")
		if !hcAddress.MatchString(addr) || (full && (suffix == "" || suffix == "." || suffix == "..")) {
			return ""
		}
		if !full {
			out, err := cli("", "hc", "get", addr, "--json")
			var terminal struct {
				Address string `json:"address"`
			}
			if err != nil || json.Unmarshal(out, &terminal) != nil || terminal.Address != addr {
				return ""
			}
			if key := hcKey(addr); key != "" {
				return "hc-" + key
			}
			return ""
		}
	}
	return s
}

// Title is the title of the pane a stored key names, or empty when its host doesn't know it.
func Title(key string) string { return title(hosts, key) }

func title(hs []host, key string) string {
	h, body := lookup(hs, key)
	return h.title(body)
}

// Holds reports whether the pane a key names runs cairn, as one inherited from an
// environment set elsewhere may not. Hosts that can't tell trust it.
func Holds(key string) bool {
	h, body := lookup(hosts, key)
	return h.holds == nil || h.holds(body)
}

// Dir is the folder of the program in the foreground of the pane a key names, or empty
// when its host doesn't know it.
func Dir(key string) string {
	h, body := lookup(hosts, key)
	if h.dir == nil {
		return ""
	}
	return h.dir(body)
}

// lineage is cairn's process and its ancestors, nearest first.
func lineage() []int {
	out, err := cli("", "ps", "-A", "-o", "pid=,ppid=")
	if err != nil {
		return nil
	}
	parent := map[int]int{}
	for line := range strings.Lines(string(out)) {
		var pid, ppid int
		if _, err := fmt.Sscan(line, &pid, &ppid); err == nil {
			parent[pid] = ppid
		}
	}
	var pids []int
	for pid := os.Getpid(); pid > 1 && !slices.Contains(pids, pid); pid = parent[pid] {
		pids = append(pids, pid)
	}
	return pids
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

// decode decodes what a CLI prints into out, leaving out as it was when the CLI fails.
func decode(out any, name string, args ...string) {
	if b, err := cli("", name, args...); err == nil {
		json.Unmarshal(b, out)
	}
}

// ErrNoPanel is Panel's error in a terminal without panes.
var ErrNoPanel = errors.New("this terminal has no API to open it; run `cairn watch` in a split")

// panelTitle names the pane, layer or terminal holding the panel, which is how a toggle
// finds it again.
const panelTitle = "cairn"

const cliTimeout = 10 * time.Second

// Panel toggles the trail panel, `cairn watch <key> --dir <dir>`, beside the pane a key
// names, or the current pane without one, or with close only closes it. cairn is the
// absolute path of this binary, and dir the folder whose trail shows when the pane's
// session has none.
func Panel(cairn, key, dir string, close bool) error {
	if key == "" {
		if h, _ := current(); h.panel == nil {
			return ErrNoPanel
		}
		if key = Pane(); key == "" {
			return fmt.Errorf("%s can't name this pane", Name())
		}
	}
	h, body := lookup(hosts, key)
	return h.panel(body, watch{cairn, key, dir}, close)
}

// watch is what the panel runs: the cairn binary, the pane key it watches, and the folder
// to fall back to.
type watch struct{ cairn, key, dir string }

// argv is the panel's command: `cairn watch <key>`, with the folder to fall back to.
func (w watch) argv() []string {
	argv := []string{w.cairn, "watch", w.key}
	if w.dir != "" {
		argv = append(argv, "--dir", w.dir)
	}
	return argv
}

// errNoPane is a panel's error for a key that names no pane in its host.
func errNoPane(h, body string) error { return fmt.Errorf("%s: no pane in %q", h, body) }

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
