// Package session knows who is acting: the agent session or pane running cairn, the
// trail and PRs each session has, and which session each pane last ran.
package session

import (
	"cmp"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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

// Store is cairn's state dir: sessions/<key>.json for each session, panes/<pane> for
// each pane, and the trails.
type Store struct {
	Dir    string
	Trails trail.Store
	// Label is the host's label for the current pane, which names a trail a session starts.
	Label func() string
}

// state is a session's file: the trail it is on and the PRs it touched.
type state struct {
	Trail   *string   `json:"trail"`
	PRs     []string  `json:"prs"`
	Updated time.Time `json:"updated"`
}

func (s Store) sessionsDir() string { return filepath.Join(s.Dir, "sessions") }
func (s Store) panesDir() string    { return filepath.Join(s.Dir, "panes") }

func (s Store) sessionFile(key string) string { return filepath.Join(s.sessionsDir(), key+".json") }

// pointerFile holds the last agent session seen in a pane, which is what its panel shows.
func (s Store) pointerFile(pane string) string { return filepath.Join(s.panesDir(), pane) }

func readState(file string) state {
	var st state
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func (s Store) load(key string) state { return readState(s.sessionFile(key)) }

// update edits a session's file under its lock and replaces it atomically, so a hook's
// record and an agent's `cairn use` don't lose each other's change.
func (s Store) update(key string, edit func(*state)) error {
	if err := os.MkdirAll(s.sessionsDir(), 0o755); err != nil {
		return err
	}
	unlock, err := trail.Lock(filepath.Join(s.sessionsDir(), "."+key+".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	st := s.load(key)
	edit(&st)
	st.Updated = time.Now().Truncate(time.Second) // written as RFC 3339, without fractions
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return trail.WriteAtomic(s.sessionFile(key), append(b, '\n'))
}

// Trail is the session's trail, or empty without one.
func (s Store) Trail(key string) string {
	if t := s.load(key).Trail; t != nil {
		return *t
	}
	return ""
}

// URLs are the PRs the session touched.
func (s Store) URLs(key string) []string { return s.load(key).PRs }

func readName(file string) string {
	b, _ := os.ReadFile(file)
	return strings.TrimRight(string(b), "\n")
}

// Bind attaches the session to a trail, creating the trail if needed.
func (s Store) Bind(key, name string) error {
	if _, err := s.Trails.Create(name); err != nil {
		return err
	}
	return s.update(key, func(st *state) { st.Trail = &name })
}

// PaneSession is the session a pane last ran, or the pane itself when none has.
func (s Store) PaneSession(pane string) string { return cmp.Or(readName(s.pointerFile(pane)), pane) }

// paneTTL is how long a pane pointer lasts untouched: by then its pane is long gone, as
// hosts never reuse a pane key.
const (
	paneTTL      = 30 * 24 * time.Hour
	paneTouchAge = 24 * time.Hour
)

// NotePane points the pane at the agent's session. Only an agent does: the statusline may
// run without the agent's session id, and would point the pane back at itself. A
// pointer already right is only touched, at most every paneTouchAge, so it doesn't expire.
func (s Store) NotePane(agent, pane string) error {
	if agent == "" || pane == "" {
		return nil
	}
	file := s.pointerFile(pane)
	if readName(file) == agent {
		if fi, err := os.Stat(file); err == nil && time.Since(fi.ModTime()) > paneTouchAge {
			now := time.Now()
			os.Chtimes(file, now, now)
		}
		return nil
	}
	if err := os.MkdirAll(s.panesDir(), 0o755); err != nil {
		return err
	}
	if err := trail.WriteAtomic(file, []byte(agent+"\n")); err != nil {
		return err
	}
	for _, f := range s.pointers() {
		if fi, err := os.Stat(f); err == nil && time.Since(fi.ModTime()) > paneTTL {
			os.Remove(f)
		}
	}
	return nil
}

// pointers are the pane pointer files, leaving out writes in progress.
func (s Store) pointers() []string {
	files, _ := filepath.Glob(filepath.Join(s.panesDir(), "*"))
	return slices.DeleteFunc(files, func(f string) bool { return strings.HasPrefix(filepath.Base(f), ".") })
}

// Record remembers a PR, one GitHub knows, for the session. A PR the session's agent
// authored (own) joins its trail ahead of the first unfinished free-text step, starting a
// trail if the session has none, named after head (the PR's branch) when the pane's host
// gives no label; being on another trail never joins the session to it.
func (s Store) Record(key, url string, own bool, head string) error {
	err := s.update(key, func(st *state) {
		if !slices.Contains(st.PRs, url) {
			st.PRs = append(st.PRs, url)
		}
	})
	if err != nil {
		return err
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

// sessions reads every session's file, by key.
func (s Store) sessions() map[string]state {
	files, _ := filepath.Glob(filepath.Join(s.sessionsDir(), "*.json"))
	out := map[string]state{}
	for _, f := range files {
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = readState(f)
	}
	return out
}

// TouchedBy maps each PR to the sessions that touched it, in key order.
func (s Store) TouchedBy() map[string][]string {
	all := s.sessions()
	out := map[string][]string{}
	for _, key := range slices.Sorted(maps.Keys(all)) {
		for _, url := range all[key].PRs {
			out[url] = append(out[url], key)
		}
	}
	return out
}

// Panes maps each session to the pane it last ran in.
func (s Store) Panes() map[string]string {
	panes := map[string]string{}
	for _, f := range s.pointers() {
		if key := readName(f); key != "" && panes[key] == "" {
			panes[key] = filepath.Base(f)
		}
	}
	return panes
}

// Attached counts the sessions on each trail.
func (s Store) Attached() map[string]int {
	out := map[string]int{}
	for _, st := range s.sessions() {
		if st.Trail != nil {
			out[*st.Trail]++
		}
	}
	return out
}
