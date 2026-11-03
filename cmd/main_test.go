package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ken8203/cairn/internal/github"
	"github.com/ken8203/cairn/internal/trail"
	"github.com/ken8203/cairn/internal/ui"
)

// testApp points cairn at temp state and cache dirs, outside any agent session or pane, with
// a host that knows no titles or labels and a GitHub that knows only the PRs in known,
// read at each lookup.
func testApp(t *testing.T, known map[string]github.PR) ui.App {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID", "REX_BLOCK", "TMUX_PANE", "HERDR_PANE_ID", "HC_TERMINAL_ID", "ORCA_TERMINAL_HANDLE"} {
		t.Setenv(k, "")
	}
	app := newApp(filepath.Join(dir, "cairn"))
	app.Title = func(string) string { return "" }
	app.Sessions.Label = func() string { return "" }
	app.Cache.Query = func(urls []string) (map[string]github.PR, error) {
		got := map[string]github.PR{}
		for _, u := range urls {
			if p, ok := known[u]; ok {
				got[u] = p
			}
		}
		return got, nil
	}
	return app
}

func writeTrail(t *testing.T, app ui.App, name, content string) {
	t.Helper()
	if err := os.MkdirAll(app.Sessions.Trails.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.Sessions.Trails.File(name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTrail(t *testing.T, app ui.App, name string) string {
	t.Helper()
	b, err := os.ReadFile(app.Sessions.Trails.File(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sessionFile(app ui.App, name string) string {
	return filepath.Join(app.Sessions.Dir, "sessions", name)
}

func pointerFile(app ui.App, pane string) string {
	return filepath.Join(app.Sessions.Dir, "panes", pane)
}

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

func TestRecordArgs(t *testing.T) {
	tests := []struct {
		args []string
		want recordOpts
	}{
		{[]string{"--own", "-C", "sub"}, recordOpts{own: true, dir: "sub"}},
		{[]string{"-R", "o/r", "12"}, recordOpts{dir: ".", repo: "o/r", target: "12"}},
		{[]string{"12", "-Ro/r"}, recordOpts{dir: ".", repo: "o/r", target: "12"}},
		{[]string{"--session", "s1", "--own", "https://github.com/o/r/pull/5"}, recordOpts{own: true, session: "s1", dir: ".", target: "https://github.com/o/r/pull/5"}},
		{[]string{"-C", "/abs", "--session", "s1", "--own"}, recordOpts{own: true, session: "s1", dir: "/abs"}},
		{nil, recordOpts{dir: "."}},
	}
	for _, tt := range tests {
		if got := recordArgs(tt.args); got != tt.want {
			t.Errorf("recordArgs(%q) = %+v", tt.args, got)
		}
	}
}

func TestRecord(t *testing.T) {
	const (
		u1 = "https://github.com/o/r/pull/1"
		u2 = "https://github.com/o/r/pull/2"
		u3 = "https://github.com/o/r/pull/3"
	)
	app := testApp(t, map[string]github.PR{u1: {Number: 1, State: "MERGED"}, u2: {Number: 2, State: "OPEN"}, u3: {Number: 3, State: "OPEN", Head: "feat/x"}})
	sessions, trails := app.Sessions, app.Sessions.Trails
	writeTrail(t, app, "t", "# t\n\n- [ ] o/r#1\n- [ ] release\n\n## Notes\n")
	writeTrail(t, app, "u", "# u\n\n- [ ] o/r#3\n\n## Notes\n")
	session := "s1"
	if err := sessions.Bind("s1", "t"); err != nil {
		t.Fatal(err)
	}

	// A PR the session's agent opened joins its trail ahead of the first unfinished step.
	if err := record(app, session, recordArgs([]string{"--own", u2})); err != nil {
		t.Fatal(err)
	}
	if got, want := readTrail(t, app, "t"), "# t\n\n- [ ] o/r#1\n- [ ] o/r#2\n- [ ] release\n\n## Notes\n"; got != want {
		t.Errorf("trail:\n%s", got)
	}
	// Recording it again neither duplicates the session's PR nor the step.
	if err := record(app, session, recordArgs([]string{"--own", u2})); err != nil {
		t.Fatal(err)
	}
	if got := sessions.URLs(session); !slices.Equal(got, []string{u2}) {
		t.Errorf("session PRs = %q", got)
	}
	if got := trails.URLs("t"); !slices.Equal(got, []string{u1, u2}) {
		t.Errorf("trail PRs = %q", got)
	}

	// A PR someone else authored is only remembered.
	if err := record(app, session, recordArgs([]string{u3})); err != nil {
		t.Fatal(err)
	}
	if got := trails.URLs("t"); !slices.Equal(got, []string{u1, u2}) {
		t.Errorf("trail PRs = %q", got)
	}

	// A session without a trail doesn't join the one its PR is on.
	session = "s2"
	if err := record(app, session, recordArgs([]string{u3})); err != nil {
		t.Fatal(err)
	}
	if got := sessions.Trail(session); got != "" {
		t.Errorf("session joined %q", got)
	}
	if got := sessions.URLs(session); !slices.Equal(got, []string{u3}) {
		t.Errorf("session PRs = %q", got)
	}

	// Authoring one starts a trail of its own instead, named after the branch here.
	if err := record(app, session, recordArgs([]string{"--own", u3})); err != nil {
		t.Fatal(err)
	}
	if got := sessions.Trail(session); got != "feat-x" {
		t.Errorf("session trail = %q", got)
	}
	if got := trails.URLs("feat-x"); !slices.Equal(got, []string{u3}) {
		t.Errorf("new trail PRs = %q", got)
	}
	if got := trails.URLs("u"); !slices.Equal(got, []string{u3}) {
		t.Errorf("other trail changed: %q", got)
	}

	// A PR GitHub doesn't know is not remembered.
	session = "s3"
	if err := record(app, session, recordArgs([]string{"--own", "https://github.com/o/r/pull/404"})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionFile(app, session+".json")); err == nil {
		t.Error("unknown PR recorded")
	}
	if sessions.Trail(session) != "" {
		t.Error("unknown PR started a trail")
	}
}

const sample = `# t

- [ ] o/r#1
- [x] release
- [ ] flag on

## Notes

- 11-02 hi
`

func TestAddRefusesUnknownPR(t *testing.T) {
	app := testApp(t, nil)
	writeTrail(t, app, "t", sample)
	err := add(app, "t", []string{"ok", "o/r#404"})
	if err == nil || err.Error() != "o/r#404: no such PR" {
		t.Fatalf("err = %v", err)
	}
	if got := readTrail(t, app, "t"); got != sample {
		t.Errorf("trail changed:\n%s", got)
	}
	if _, ok := app.Cache.Read(trail.URL("o/r#404")); ok {
		t.Error("unknown PR got a cache file")
	}
}

func TestCLIErrors(t *testing.T) {
	known := map[string]github.PR{}
	app := testApp(t, known)
	tests := []struct {
		session string
		args    []string
		want    string
	}{
		{"", []string{"show"}, "not in an agent session or a pane"},
		{"s1", []string{"add", "x"}, "this session has no trail; run `cairn use <name>` first"},
		{"s1", []string{"use"}, "usage: cairn use <name>"},
		{"s1", []string{"use", "a b"}, "trail names use letters, digits, '.', '_' and '-'"},
		{"", []string{"edit"}, "usage: cairn edit <name>"},
		{"", []string{"nope"}, "unknown command 'nope'; see `cairn --help`"},
	}
	for _, tt := range tests {
		t.Setenv("CLAUDE_CODE_SESSION_ID", tt.session)
		if err := run(app, tt.args); err == nil || err.Error() != tt.want {
			t.Errorf("%q: err = %v, want %q", tt.args, err, tt.want)
		}
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s1")
	if err := run(app, []string{"use", "t"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(app.Sessions.Trails.File("t"))
	for _, args := range []string{"done 1", "link 1 o/r#9"} {
		if args == "link 1 o/r#9" {
			known[trail.URL("o/r#9")] = github.PR{Number: 9, State: "OPEN"}
		}
		if err := run(app, strings.Fields(args)); err == nil || err.Error() != "t: no such trail" {
			t.Errorf("%q on a missing trail: %v", args, err)
		}
		if _, err := os.Stat(app.Sessions.Trails.File("t")); err == nil {
			t.Errorf("%q created the trail", args)
		}
	}
	delete(known, trail.URL("o/r#9"))
	os.RemoveAll(app.Cache.Dir)
	run(app, []string{"use", "t"})
	for args, want := range map[string]string{
		"done":         "usage: cairn done <step number>",
		"link 1":       "usage: cairn link <step number> <owner/repo#n | PR URL>",
		"link x y":     "usage: cairn link <step number> <owner/repo#n | PR URL>",
		"link 1 o/r#9": "o/r#9: no such PR",
		"note":         "usage: cairn note <text>",
		"add":          "usage: cairn add <owner/repo#n | PR URL | text>...",
	} {
		if err := run(app, strings.Fields(args)); err == nil || err.Error() != want {
			t.Errorf("%q: err = %v, want %q", args, err, want)
		}
	}
}

// Without the agent's id, as a statusline may run, the pane keeps its pointer.
func TestStatuslineKeepsPanePointer(t *testing.T) {
	app := testApp(t, nil)
	app.Sessions.NotePane("s2", "p1")
	t.Setenv("REX_BLOCK", "block:p1")
	if err := run(app, []string{"statusline"}); err != nil {
		t.Fatal(err)
	}
	if got := readName(pointerFile(app, "p1")); got != "s2" {
		t.Errorf("pointer = %q after a pane-keyed run", got)
	}
}

func TestSessionSideEffects(t *testing.T) {
	app := testApp(t, nil)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s1")
	t.Setenv("REX_BLOCK", "block:p1")
	t.Setenv("EDITOR", "true")
	stdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	t.Cleanup(func() { os.Stdout.Close(); os.Stdout = stdout })
	for _, args := range [][]string{{"ls"}, {"--help"}, {"__refresh", "s1"}, {"edit", "t"}} {
		if err := run(app, args); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(pointerFile(app, "p1")); err == nil {
			t.Fatalf("%q pointed the pane at the session", args)
		}
	}
	if err := run(app, []string{"show"}); err != nil {
		t.Fatal(err)
	}
	if readName(pointerFile(app, "p1")) != "s1" {
		t.Error("show did not point the pane at the session")
	}
}

// cairnEnv runs the built cairn with a temp home, in the session and host env given.
func cairnEnv(t *testing.T, env ...string) []string {
	t.Helper()
	dir := t.TempDir()
	base := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains([]string{"CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID", "REX_BLOCK", "TMUX_PANE", "HERDR_PANE_ID", "HC_TERMINAL_ID", "ORCA_TERMINAL_HANDLE"}, k)
	})
	return slices.Concat(base, []string{"CAIRN_HOME=" + dir}, env)
}

func TestShowJSON(t *testing.T) {
	bin, err := build()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"show", "--json"}, {"--json"}} {
		cmd := exec.Command(bin, args...)
		cmd.Env = cairnEnv(t, "CLAUDE_CODE_SESSION_ID=s1")
		out, err := cmd.Output()
		if err != nil || string(out) != `{"trail":null,"prs":[]}`+"\n" {
			t.Errorf("%q = %q, %v", args, out, err)
		}
	}
}

// Without the agent's id, as a sidebar may run it, the JSON follows the pane to its
// session.
func TestShowJSONFollowsPane(t *testing.T) {
	app := testApp(t, nil)
	writeTrail(t, app, "t", "# t\n\n- [ ] x\n")
	app.Sessions.Bind("s2", "t")
	app.Sessions.NotePane("s2", "p1")
	t.Setenv("REX_BLOCK", "block:p1")
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	err := run(app, []string{"--json"})
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if err != nil || !strings.HasPrefix(string(out), `{"trail":{"name":"t",`) {
		t.Errorf("--json = %q, %v", out, err)
	}
}

func TestPanel(t *testing.T) {
	bin, err := build()
	if err != nil {
		t.Fatal(err)
	}
	// Outside a host with panes, a hint and success.
	cmd := exec.Command(bin, "panel")
	cmd.Env = cairnEnv(t)
	out, err := cmd.Output()
	if err != nil || string(out) != "cairn panel: this terminal has no API to open it; run `cairn watch` in a split\n" {
		t.Errorf("panel = %q, %v", out, err)
	}
	// A host whose CLI fails: one line on stderr and exit 1.
	cmd = exec.Command(bin, "panel")
	cmd.Env = cairnEnv(t, "ORCA_TERMINAL_HANDLE=term_1", "PATH="+t.TempDir())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 || strings.Count(stderr.String(), "\n") != 1 || !strings.HasPrefix(stderr.String(), "cairn: orca terminal: ") {
		t.Errorf("panel = %v, stderr %q", err, stderr.String())
	}
}
