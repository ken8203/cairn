// Package session knows who is acting: the agent session or pane running cairn, the
// trail and PRs each session has, and which session each pane last ran.
package session

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ken8203/cairn/internal/host"
	"github.com/ken8203/cairn/internal/trail"
)

// Agent is the agent session running cairn, read from the environment alone. A trail
// binds to the agent's session, else to the Pane as a human's session. Codex comes first
// because a Codex launched from Claude Code inherits its session id.
func Agent() string {
	return cmp.Or(valid(os.Getenv("CODEX_THREAD_ID")), valid(os.Getenv("CLAUDE_CODE_SESSION_ID")))
}

// Pane is the pane running cairn, which some hosts are asked for.
func Pane() string { return valid(host.Pane()) }

func valid(s string) string {
	if trail.ValidName(s) {
		return s
	}
	return ""
}

// Store is cairn's state dir: sessions/<key>.trail and .prs for each session,
// panes/<pane>.session for each pane, and the trails.
type Store struct {
	Dir    string
	Trails trail.Store
	// Label is the host's label for the current pane, which names a trail a session starts.
	Label func() string
}

func (s Store) sessionsDir() string { return filepath.Join(s.Dir, "sessions") }
func (s Store) panesDir() string    { return filepath.Join(s.Dir, "panes") }

func (s Store) trailFile(key string) string { return filepath.Join(s.sessionsDir(), key+".trail") }
func (s Store) prsFile(key string) string   { return filepath.Join(s.sessionsDir(), key+".prs") }

// pointerFile holds the last agent session seen in a pane, which is what its panel shows.
func (s Store) pointerFile(pane string) string { return filepath.Join(s.panesDir(), pane+".session") }

// Trail is the session's trail, or empty without one.
func (s Store) Trail(key string) string { return readName(s.trailFile(key)) }

// URLs are the PRs the session touched.
func (s Store) URLs(key string) []string { return readURLs(s.prsFile(key)) }

func readName(file string) string {
	b, _ := os.ReadFile(file)
	return strings.TrimRight(string(b), "\n")
}

func readURLs(file string) []string {
	b, _ := os.ReadFile(file)
	var out []string
	for l := range strings.SplitSeq(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Bind attaches the session to a trail, creating the trail if needed.
func (s Store) Bind(key, name string) error {
	if _, err := s.Trails.Create(name); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessionsDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.trailFile(key), []byte(name+"\n"), 0o644)
}

// PaneSession is the session a pane last ran, or the pane itself when none has.
func (s Store) PaneSession(pane string) string { return cmp.Or(readName(s.pointerFile(pane)), pane) }

// NotePane points the pane at the agent's session. Only an agent does: the statusline may
// run without the agent's session id, and would point the pane back at itself.
func (s Store) NotePane(agent, pane string) error {
	if agent == "" || pane == "" || readName(s.pointerFile(pane)) == agent {
		return nil
	}
	if err := os.MkdirAll(s.panesDir(), 0o755); err != nil {
		return err
	}
	return trail.WriteAtomic(s.pointerFile(pane), []byte(agent+"\n"))
}

// Record remembers a PR, one GitHub knows, for the session. A PR the session's agent
// authored (own) joins its trail ahead of the first unfinished free-text step, starting a
// trail if the session has none, named after head (the PR's branch) when the pane's host
// gives no label; being on another trail never joins the session to it.
func (s Store) Record(key, url string, own bool, head string) error {
	if !slices.Contains(s.URLs(key), url) {
		if err := os.MkdirAll(s.sessionsDir(), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(s.prsFile(key), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(f, url)
		if err := errors.Join(err, f.Close()); err != nil {
			return err
		}
	}

	if !own {
		return nil
	}
	name := s.Trail(key)
	if name == "" {
		var err error
		if name, err = s.claim(key, head); err != nil {
			return err
		}
		if err := s.Bind(key, name); err != nil {
			return err
		}
	}
	if slices.Contains(s.Trails.URLs(name), url) {
		return nil
	}
	return s.Trails.Add(name, true, url)
}

// A trail named after its session takes this much of the key.
const sessionPrefixLen = 8

// claim creates a trail for this session named after its host's label, the PR's branch,
// or the session key, numbered past any trail that exists so another session's trail is
// never joined by accident.
func (s Store) claim(key, head string) (string, error) {
	base := cmp.Or(host.Sanitize(s.Label()), host.Sanitize(head), "trail-"+key[:min(len(key), sessionPrefixLen)])
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		created, err := s.Trails.Create(name)
		if err != nil || created {
			return name, err
		}
	}
}

// TouchedBy maps each PR to the sessions that touched it, in key order.
func (s Store) TouchedBy() map[string][]string {
	files, _ := filepath.Glob(filepath.Join(s.sessionsDir(), "*.prs"))
	out := map[string][]string{}
	for _, f := range files {
		key := strings.TrimSuffix(filepath.Base(f), ".prs")
		for _, url := range readURLs(f) {
			out[url] = append(out[url], key)
		}
	}
	return out
}

// Panes maps each session to the pane it last ran in.
func (s Store) Panes() map[string]string {
	files, _ := filepath.Glob(filepath.Join(s.panesDir(), "*.session"))
	panes := map[string]string{}
	for _, f := range files {
		if key := readName(f); key != "" && panes[key] == "" {
			panes[key] = strings.TrimSuffix(filepath.Base(f), ".session")
		}
	}
	return panes
}

// Attached counts the sessions on each trail.
func (s Store) Attached() map[string]int {
	files, _ := filepath.Glob(filepath.Join(s.sessionsDir(), "*.trail"))
	out := map[string]int{}
	for _, f := range files {
		out[readName(f)]++
	}
	return out
}
