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

// touch records PRs the session touched without authoring them.
func touch(t *testing.T, a App, key string, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if err := a.Sessions.Record(key, u, false, "", session.Place{}); err != nil {
			t.Fatal(err)
		}
	}
}

func pointTo(t *testing.T, a App, pane, key string) {
	t.Helper()
	if err := a.Sessions.NotePane(key, pane); err != nil {
		t.Fatal(err)
	}
}

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
	if got := a.Statusline(a.Find("", "p1", "")); got != "b 0/1 · ▶ beta" {
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
	for _, s := range []string{"s2", "s3", "s4", "s5"} {
		touch(t, a, s, url)
	}
	touch(t, a, "s1", mine)
	pointTo(t, a, "pa", "s2")
	pointTo(t, a, "pb", "s3")
	pointTo(t, a, "gone", "s4") // a pane its host no longer knows
	pointTo(t, a, "idle", "s9") // a pane whose session touched nothing here
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
	touch(t, a, "s4", "https://github.com/o/r/pull/1")
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
	line := render(a.gather(a.Find("s1", "", ""), false), width).lines[2]
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
	touch(t, a, "s1", stacked, other)
	b, err := json.Marshal(a.Data(a.Find("s1", "", "")))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"trail":{"name":"t","done":2,"total":4,"steps":[` +
		`{"n":1,"kind":"pr","done":true,"ref":"o/r#1","url":"` + base + `","title":"Add API","state":"merged","ci":"pass"},` +
		`{"n":2,"kind":"pr","done":false,"ref":"o/r#2","url":"` + stacked + `","title":"Use API","state":"draft","ci":"pending","stacked_on":"o/r#1"},` +
		`{"n":3,"kind":"text","text":"release","done":true},` +
		`{"n":4,"kind":"text","text":"flag","done":false}],` +
		`"notes":["11-02 waiting"]},"via":"session",` +
		`"prs":[{"ref":"o/s#3","url":"` + other + `","title":"Side fix","state":"open","ci":"fail"}]}`
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
	if spawned != nil {
		t.Errorf("refreshed fresh PRs: %q", spawned)
	}

	// Without a trail: null, and the touched PRs, refreshing the one never fetched in the
	// background.
	touch(t, a, "s2", unknown)
	b, _ = json.Marshal(a.Data(a.Find("s2", "", "")))
	if want := `{"trail":null,"prs":[{"ref":"o/s#4","url":"` + unknown + `","state":"unknown","ci":"none"}]}`; string(b) != want {
		t.Errorf("got %s", b)
	}
	if len(spawned) == 0 || spawned[0] != "__refresh" {
		t.Errorf("spawned %q", spawned)
	}
	b, _ = json.Marshal(a.Data(a.Find("s3", "", "")))
	if string(b) != `{"trail":null,"prs":[]}` {
		t.Errorf("got %s", b)
	}
}

func TestShowFoundByFolder(t *testing.T) {
	a := newApp(t, nil)
	writeTrail(t, a, "t", "# t\n\n- [ ] x\n")
	f := session.Found{Key: "s1", Trail: "t", Via: "dir", Place: session.Place{Dir: "/w/cairn", Repo: "o/cairn", Branch: "feat/x"}}
	got := a.Show(f, 0)
	if len(got) < 3 || got[1] != dim("found by folder: cairn on feat/x — `cairn use t` to join") || got[2] != "" {
		t.Errorf("show:\n%s", strings.Join(got, "\n"))
	}
	b, _ := json.Marshal(a.Data(f))
	if !strings.Contains(string(b), `"via":"dir"`) {
		t.Errorf("data = %s", b)
	}
	f.Via = "session"
	if got := a.Show(f, 0); got[1] != "" {
		t.Errorf("own trail says %q", got[1])
	}
}
