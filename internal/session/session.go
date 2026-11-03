// Package session knows who is acting: the agent session or pane running cairn, the
// trail and PRs each session has, and which session each pane last ran.
package session

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"os/exec"
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
// each pane, hooked/<key> for each agent session that ran cairn's hook, and the trails.
type Store struct {
	Dir    string
	Trails trail.Store
	// Tab is the name the user gave the current pane's tab, and Label the host's label for
	// the wider place it is in; they name a trail a session starts.
	Tab, Label func() string
}

// state is a session's file: the trail it is on, the PRs it touched and the places it
// recorded them from.
type state struct {
	Trail   *string   `json:"trail"`
	PRs     []string  `json:"prs"`
	Places  []Place   `json:"places"`
	Updated time.Time `json:"updated"`
}

// Place is where a PR was recorded from: a git worktree's toplevel, the owner/repo of its
// origin remote, and its branch, empty on a detached HEAD.
type Place struct {
	Dir    string `json:"dir"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

const gitTimeout = 2 * time.Second

// Where is the place dir is in, or false outside a git worktree.
func Where(dir string) (Place, bool) {
	if dir == "" {
		return Place{}, false
	}
	top, branch, ok := strings.Cut(git(dir, "rev-parse", "--show-toplevel", "--abbrev-ref", "HEAD"), "\n")
	if !ok || top == "" {
		return Place{}, false
	}
	if branch == "HEAD" {
		branch = ""
	}
	return Place{Dir: top, Repo: remoteRepo(git(dir, "remote", "get-url", "origin")), Branch: branch}, true
}

// git is what git prints in dir, trimmed, or empty when it fails or takes too long.
func git(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// remoteRepo is the owner/repo a remote URL ends in: https://host/o/r.git,
// ssh://git@host/o/r or git@host:o/r.git.
func remoteRepo(url string) string {
	if _, rest, ok := strings.Cut(url, "://"); ok {
		_, url, _ = strings.Cut(rest, "/")
	} else if _, path, ok := strings.Cut(url, ":"); ok {
		url = path
	} else {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git"), "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

func (s Store) sessionsDir() string { return filepath.Join(s.Dir, "sessions") }
func (s Store) panesDir() string    { return filepath.Join(s.Dir, "panes") }
func (s Store) hookedDir() string   { return filepath.Join(s.Dir, "hooked") }

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

// stateTTL is how long a pane pointer or hook marker lasts untouched: by then its pane or
// session is long gone. One still in use is touched at most every touchAge.
const (
	stateTTL = 30 * 24 * time.Hour
	touchAge = 24 * time.Hour
)

// NotePane points the pane at the agent's session. Only an agent does: the statusline may
// run without the agent's session id, and would point the pane back at itself. A
// pointer already right is only touched, at most every touchAge, so it doesn't expire.
func (s Store) NotePane(agent, pane string) error {
	if agent == "" || pane == "" {
		return nil
	}
	file := s.pointerFile(pane)
	if readName(file) == agent {
		touch(file)
		return nil
	}
	if err := os.MkdirAll(s.panesDir(), 0o755); err != nil {
		return err
	}
	if err := trail.WriteAtomic(file, []byte(agent+"\n")); err != nil {
		return err
	}
	expire(s.panesDir())
	return nil
}

// touch bumps a file's time once it is touchAge old.
func touch(file string) {
	if fi, err := os.Stat(file); err == nil && time.Since(fi.ModTime()) > touchAge {
		now := time.Now()
		os.Chtimes(file, now, now)
	}
}

// expire removes the files in dir untouched for stateTTL.
func expire(dir string) {
	for _, f := range listed(dir) {
		if fi, err := os.Stat(f); err == nil && time.Since(fi.ModTime()) > stateTTL {
			os.Remove(f)
		}
	}
}

// NoteHooked marks that the agent session ran cairn's hook.
func (s Store) NoteHooked(agent string) error {
	file := filepath.Join(s.hookedDir(), agent)
	if _, err := os.Stat(file); err == nil {
		touch(file)
		return nil
	}
	if err := os.MkdirAll(s.hookedDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		return err
	}
	expire(s.hookedDir())
	return nil
}

// Hooked reports whether the agent session ran cairn's hook.
func (s Store) Hooked(agent string) bool {
	_, err := os.Stat(filepath.Join(s.hookedDir(), agent))
	return err == nil
}

// pointers are the pane pointer files.
func (s Store) pointers() []string { return listed(s.panesDir()) }

// listed are the files in dir, leaving out writes in progress.
func listed(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	return slices.DeleteFunc(files, func(f string) bool { return strings.HasPrefix(filepath.Base(f), ".") })
}

// Found is the trail a view shows, how it was found, and the session whose own PRs show
// beside it.
type Found struct {
	Key   string
	Trail string
	// Via is session, pane or dir, and empty without a trail.
	Via string
	// Place is the folder's, when the trail was found through it.
	Place Place
}

// Find is what a view shows for the agent's session, else the pane's: its own trail,
// else that of the session the pane last ran, else that of the folder dir. Without the
// agent's id, as a panel or statusline may run, the pane's last session comes first. The
// folder only shows a trail; joining it takes `cairn use`. head is a PR's cached head
// branch.
func (s Store) Find(agent, pane, dir string, head func(url string) string) Found {
	own, last := cmp.Or(agent, pane), ""
	if pane != "" {
		last = s.PaneSession(pane)
	}
	keys := []string{agent, last}
	if agent == "" {
		keys = []string{last, pane}
	}
	keys = slices.DeleteFunc(slices.Compact(keys), func(k string) bool { return k == "" })
	for _, key := range keys {
		if t := s.Trail(key); t != "" {
			if key == own {
				return Found{Key: key, Trail: t, Via: "session"}
			}
			return Found{Key: key, Trail: t, Via: "pane"}
		}
	}
	var f Found
	if len(keys) > 0 {
		f.Key = keys[0]
	}
	if t, at := s.ResolveDir(dir, head); t != "" {
		f.Trail, f.Via, f.Place = t, "dir", at
	}
	return f
}

// ResolveDir is the trail of the branch dir is on, or empty: a trail with a PR step in
// the folder's repo whose head is that branch, else the trail of a session that recorded
// a PR from the same worktree and branch; the most recently changed trail when several
// match. head is a PR's cached head branch, empty while GitHub hasn't been asked.
func (s Store) ResolveDir(dir string, head func(url string) string) (string, Place) {
	at, ok := Where(dir)
	if !ok || at.Branch == "" {
		return "", at
	}
	var found []string
	if at.Repo != "" {
		for _, name := range s.Trails.Names() {
			if slices.ContainsFunc(s.Trails.URLs(name), func(url string) bool {
				repo, _, _ := strings.Cut(trail.Ref(url), "#")
				return strings.EqualFold(repo, at.Repo) && head(url) == at.Branch
			}) {
				found = append(found, name)
			}
		}
	}
	if len(found) == 0 {
		for _, st := range s.sessions() {
			if st.Trail != nil && slices.ContainsFunc(st.Places, func(p Place) bool { return p.Dir == at.Dir && p.Branch == at.Branch }) {
				found = append(found, *st.Trail)
			}
		}
	}
	return s.newest(found), at
}

// newest is the trail among names changed last, the first by name on a tie; one without
// a file is left out.
func (s Store) newest(names []string) string {
	slices.Sort(names)
	var best string
	var at time.Time
	for _, name := range slices.Compact(names) {
		if fi, err := os.Stat(s.Trails.File(name)); err == nil && (best == "" || fi.ModTime().After(at)) {
			best, at = name, fi.ModTime()
		}
	}
	return best
}

// Record remembers a PR, one GitHub knows, for the session, and the place it was
// recorded from unless that is zero. A PR the session's agent
// authored (own) joins its trail ahead of the first unfinished free-text step, starting a
// trail if the session has none, named after head (the PR's branch) when the pane's host
// gives no label; being on another trail never joins the session to it.
func (s Store) Record(key, url string, own bool, head string, at Place) error {
	err := s.update(key, func(st *state) {
		if !slices.Contains(st.PRs, url) {
			st.PRs = append(st.PRs, url)
		}
		if at != (Place{}) && !slices.Contains(st.Places, at) {
			st.Places = append(st.Places, at)
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

// claim creates a trail for this session named after its tab, the PR's branch, its host's
// label or the session key, numbered past any trail that exists so another session's
// trail is never joined by accident.
func (s Store) claim(key, head string) (string, error) {
	base := cmp.Or(host.Sanitize(s.Tab()), host.Sanitize(head), host.Sanitize(s.Label()), "trail-"+key[:min(len(key), sessionPrefixLen)])
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
