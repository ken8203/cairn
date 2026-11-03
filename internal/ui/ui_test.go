package ui

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/ken8203/cairn/internal/github"
	"github.com/ken8203/cairn/internal/session"
	"github.com/ken8203/cairn/internal/trail"
)

// newApp points the views at temp state and cache dirs, with a Rex that knows no titles
// and a GitHub that knows only the PRs given.
func newApp(t *testing.T, known map[string]github.PR) App {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state", "cairn")
	return App{
		Sessions: session.Store{Dir: state, Trails: trail.Store{Dir: filepath.Join(state, "trails")}, Label: func() string { return "" }},
		Cache: github.Cache{Dir: filepath.Join(dir, "cache", "cairn"), TTL: time.Minute, Query: func(urls []string) (map[string]github.PR, error) {
			got := map[string]github.PR{}
			for _, u := range urls {
				if p, ok := known[u]; ok {
					got[u] = p
				}
			}
			return got, nil
		}},
		Title: func(string) string { return "" },
	}
}

func writeTrail(t *testing.T, a App, name, content string) {
	t.Helper()
	if err := os.MkdirAll(a.Sessions.Trails.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Sessions.Trails.File(name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTrail(t *testing.T, a App, name string) string {
	t.Helper()
	b, err := os.ReadFile(a.Sessions.Trails.File(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sessionsDir(a App) string                  { return filepath.Join(a.Sessions.Dir, "sessions") }
func panesDir(a App) string                     { return filepath.Join(a.Sessions.Dir, "panes") }
func sessionPRsFile(a App, key string) string   { return filepath.Join(sessionsDir(a), key+".prs") }
func sessionTrailFile(a App, key string) string { return filepath.Join(sessionsDir(a), key+".trail") }
func pointerFile(a App, pane string) string     { return filepath.Join(panesDir(a), pane+".session") }

func TestShortRef(t *testing.T) {
	if got := shortRef("o/r#5"); got != "r#5" {
		t.Errorf("shortRef = %q", got)
	}
}

func TestStatuslineFollowsPaneSession(t *testing.T) {
	a := newApp(t, nil)
	writeTrail(t, a, "b", "# b\n\n- [ ] beta\n")
	a.Sessions.Bind("s2", "b")
	a.Sessions.NotePane("s2", "p1")
	if got := a.Statusline(a.Sessions.PaneSession("p1")); got != "b 0/1 · ▶ beta" {
		t.Errorf("statusline = %q", got)
	}
}

func TestOwnersAcrossSessions(t *testing.T) {
	url, mine := "https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"
	a := newApp(t, nil)
	var asked atomic.Int32
	a.Title = func(pane string) string {
		asked.Add(1)
		return map[string]string{"pa": "Pane A", "pb": "Pane B"}[pane]
	}
	os.MkdirAll(sessionsDir(a), 0o755)
	os.MkdirAll(panesDir(a), 0o755)
	for _, s := range []string{"s2", "s3", "s4", "s5"} {
		os.WriteFile(sessionPRsFile(a, s), []byte(url+"\n"), 0o644)
	}
	os.WriteFile(sessionPRsFile(a, "s1"), []byte(mine+"\n"), 0o644)
	os.WriteFile(pointerFile(a, "pa"), []byte("s2\n"), 0o644)
	os.WriteFile(pointerFile(a, "pb"), []byte("s3\n"), 0o644)
	os.WriteFile(pointerFile(a, "gone"), []byte("s4\n"), 0o644) // a pane its host no longer knows
	os.WriteFile(pointerFile(a, "idle"), []byte("s9\n"), 0o644) // a pane whose session touched nothing here
	// s5 last ran in no pane.
	b := &board{key: "s1", touched: []string{mine}, steps: []step{{Step: trail.Step{PR: true, Value: url}}, {Step: trail.Step{PR: true, Value: mine}}}}
	a.readOwners(b)
	if got := b.owners[url]; got != "Pane A, Pane B" || b.owners[mine] != "" {
		t.Errorf("owners = %q", b.owners)
	}
	if asked.Load() != 3 {
		t.Errorf("asked for %d titles", asked.Load())
	}

	// With only its own PRs on the trail, nobody is asked.
	asked.Store(0)
	b = &board{key: "s1", touched: []string{mine}, steps: []step{{Step: trail.Step{PR: true, Value: mine}}}}
	a.readOwners(b)
	if asked.Load() != 0 || b.owners != nil {
		t.Errorf("asked %d times for %q", asked.Load(), b.owners)
	}
}

func TestListCountsSessions(t *testing.T) {
	a := newApp(t, nil)
	writeTrail(t, a, "t", "# t\n\n- [ ] x\n")
	writeTrail(t, a, "u", "# u\n\n- [ ] y\n")
	for _, s := range []string{"s1", "s2"} {
		a.Sessions.Bind(s, "t")
	}
	a.Sessions.Bind("s3", "u")
	os.WriteFile(sessionTrailFile(a, "s4"), nil, 0o644)
	out := a.List()
	if !strings.Contains(out, dim("2 session(s)")) || !strings.Contains(out, dim("1 session(s)")) || strings.Contains(out, "pane") {
		t.Errorf("ls:\n%s", out)
	}
}

func TestRenderWideTitles(t *testing.T) {
	url := "https://github.com/o/r/pull/1"
	a := newApp(t, map[string]github.PR{url: {Number: 1, State: "OPEN", CI: "none", Title: strings.Repeat("修正登入流程", 10)}})
	writeTrail(t, a, "t", "# t\n\n- [ ] o/r#1\n")
	a.Sessions.Bind("s1", "t")
	const width = 60
	line := render(a.gather("s1", false), width).lines[2]
	if w := ansi.StringWidth(line); w > width || !strings.HasSuffix(line, "…") {
		t.Errorf("%d columns: %q", w, line)
	}
}

func TestData(t *testing.T) {
	const (
		base    = "https://github.com/o/r/pull/1"
		stacked = "https://github.com/o/r/pull/2"
		other   = "https://github.com/o/s/pull/3"
		unknown = "https://github.com/o/s/pull/4"
	)
	a := newApp(t, map[string]github.PR{
		base:    {Number: 1, State: "MERGED", CI: "pass", Title: "Add API", Head: "api"},
		stacked: {Number: 2, State: "OPEN", Draft: true, CI: "pending", Title: "Use API", Base: "api"},
		other:   {Number: 3, State: "OPEN", CI: "fail", Title: "Side fix"},
	})
	var spawned []string
	a.Spawn = func(args ...string) error { spawned = args; return nil }
	a.Cache.Fetch([]string{base, stacked, other})

	writeTrail(t, a, "t", "# t\n\n- [ ] o/r#1\n- [ ] o/r#2\n- [x] release\n- [ ] flag\n\n## Notes\n\n- 11-02 waiting\n")
	a.Sessions.Bind("s1", "t")
	os.WriteFile(sessionPRsFile(a, "s1"), []byte(stacked+"\n"+other+"\n"), 0o644)
	b, err := json.Marshal(a.Data("s1"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"trail":{"name":"t","done":2,"total":4,"steps":[` +
		`{"n":1,"kind":"pr","done":true,"ref":"o/r#1","url":"` + base + `","title":"Add API","state":"merged","ci":"pass"},` +
		`{"n":2,"kind":"pr","done":false,"ref":"o/r#2","url":"` + stacked + `","title":"Use API","state":"draft","ci":"pending","stacked_on":"o/r#1"},` +
		`{"n":3,"kind":"text","text":"release","done":true},` +
		`{"n":4,"kind":"text","text":"flag","done":false}],` +
		`"notes":["11-02 waiting"]},` +
		`"prs":[{"ref":"o/s#3","url":"` + other + `","title":"Side fix","state":"open","ci":"fail"}]}`
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
	if spawned != nil {
		t.Errorf("refreshed fresh PRs: %q", spawned)
	}

	// Without a trail: null, and the touched PRs, refreshing the one never fetched in the
	// background.
	os.WriteFile(sessionPRsFile(a, "s2"), []byte(unknown+"\n"), 0o644)
	b, _ = json.Marshal(a.Data("s2"))
	if want := `{"trail":null,"prs":[{"ref":"o/s#4","url":"` + unknown + `","state":"unknown","ci":"none"}]}`; string(b) != want {
		t.Errorf("got %s", b)
	}
	if len(spawned) == 0 || spawned[0] != "__refresh" {
		t.Errorf("spawned %q", spawned)
	}
	b, _ = json.Marshal(a.Data("s3"))
	if string(b) != `{"trail":null,"prs":[]}` {
		t.Errorf("got %s", b)
	}
}
