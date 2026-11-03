package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ken8203/cairn/internal/trail"
)

// newStore is a state dir in a temp dir, with a host that gives no label.
func newStore(t *testing.T) Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cairn")
	return Store{Dir: dir, Trails: trail.Store{Dir: filepath.Join(dir, "trails")}, Label: func() string { return "" }}
}

func TestAgentAndPane(t *testing.T) {
	tests := []struct {
		codex, claude, block, agent, pane string
	}{
		{"", "", "", "", ""},
		{"", "", "block:b1", "", "b1"},
		{"", "c1", "", "c1", ""},
		{"", "c1", "block:b1", "c1", "b1"},
		{"x1", "", "", "x1", ""},
		{"x1", "", "block:b1", "x1", "b1"},
		{"x1", "c1", "", "x1", ""},
		{"x1", "c1", "block:b1", "x1", "b1"},
		{"../x", "c1", "block:../b", "c1", ""},
	}
	for _, k := range []string{"TMUX_PANE", "HERDR_PANE_ID", "HC_TERMINAL_ID", "ORCA_TERMINAL_HANDLE"} {
		t.Setenv(k, "")
	}
	for _, tt := range tests {
		t.Setenv("CODEX_THREAD_ID", tt.codex)
		t.Setenv("CLAUDE_CODE_SESSION_ID", tt.claude)
		t.Setenv("REX_BLOCK", tt.block)
		if agent, pane := Agent(), Pane(); agent != tt.agent || pane != tt.pane {
			t.Errorf("%+v: got %q, %q", tt, agent, pane)
		}
	}
}

func TestNotePane(t *testing.T) {
	s := newStore(t)
	if err := s.NotePane("s1", "p1"); err != nil {
		t.Fatal(err)
	}
	if got := readName(s.pointerFile("p1")); got != "s1" {
		t.Fatalf("pointer = %q", got)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(s.pointerFile("p1"), old, old)
	if err := s.NotePane("s1", "p1"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(s.pointerFile("p1")); !fi.ModTime().Equal(old) {
		t.Error("rewrote a current pointer")
	}

	s.NotePane("s2", "p1")
	if got := readName(s.pointerFile("p1")); got != "s2" {
		t.Errorf("pointer = %q after a new session", got)
	}

	s.NotePane("s3", "")
	if files := s.pointers(); len(files) != 1 {
		t.Errorf("pointers = %q outside a pane", files)
	}
}

func TestNotePaneExpires(t *testing.T) {
	s := newStore(t)
	for _, p := range []string{"old", "kept", "recent"} {
		s.NotePane("s1", p)
	}
	expired := time.Now().Add(-paneTTL - time.Hour)
	os.Chtimes(s.pointerFile("old"), expired, expired)
	os.Chtimes(s.pointerFile("kept"), expired, expired)

	// An agent still running in a pane keeps its pointer alive without rewriting it.
	if err := s.NotePane("s1", "kept"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(s.pointerFile("kept")); time.Since(fi.ModTime()) > time.Minute {
		t.Error("a pointer in use was not touched")
	}
	if _, err := os.Stat(s.pointerFile("old")); err != nil {
		t.Error("expired before any pointer was written")
	}

	if err := s.NotePane("s2", "new"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range s.pointers() {
		got = append(got, filepath.Base(f))
	}
	if !slices.Equal(got, []string{"kept", "new", "recent"}) {
		t.Errorf("pointers = %q", got)
	}
}

func TestSessionFile(t *testing.T) {
	s := newStore(t)
	if err := s.Record("s1", "https://github.com/o/r/pull/1", false, "", Place{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.sessionFile("s1"))
	if !regexp.MustCompile(`^\{"trail":null,"prs":\["https://github.com/o/r/pull/1"\],"places":\[\],"updated":"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}(Z|[+-][0-9:]{5})"\}\n$`).Match(b) {
		t.Errorf("file = %s", b)
	}
	if err := s.Bind("s1", "t"); err != nil {
		t.Fatal(err)
	}
	if got, urls := s.Trail("s1"), s.URLs("s1"); got != "t" || !slices.Equal(urls, []string{"https://github.com/o/r/pull/1"}) {
		t.Errorf("trail %q, prs %q", got, urls)
	}
	if s.Trail("nobody") != "" || s.URLs("nobody") != nil {
		t.Error("a session without a file has state")
	}
}

// A hook's record and an agent's `cairn use` run at once without losing either.
func TestSessionConcurrentWrites(t *testing.T) {
	s := newStore(t)
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if err := s.Record("s1", fmt.Sprintf("https://github.com/o/r/pull/%d", i), false, "", Place{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Go(func() {
		if err := s.Bind("s1", "t"); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if got := len(s.URLs("s1")); got != n || s.Trail("s1") != "t" {
		t.Errorf("%d PRs, trail %q", got, s.Trail("s1"))
	}
}

func TestClaim(t *testing.T) {
	s := newStore(t)
	const key = "0123456789abcdef"
	label := " My Session: v2! "
	s.Label = func() string { return label }

	for _, want := range []string{"My-Session-v2", "My-Session-v2-2", "My-Session-v2-3"} {
		got, err := s.claim(key, "")
		if err != nil || got != want {
			t.Errorf("claim = %q, %v; want %q", got, err, want)
		}
	}

	label = "///"
	os.WriteFile(s.Trails.File("jay-fix-it"), []byte("# jay-fix-it\n"), 0o644) // another session's trail
	if got, _ := s.claim(key, "jay/fix: it"); got != "jay-fix-it-2" {
		t.Errorf("branch name = %q", got)
	}

	if got, _ := s.claim(key, ""); got != "trail-01234567" {
		t.Errorf("key name = %q", got)
	}
}

// gitRepo is a git repo with an origin remote and a commit on branch, and a worktree of
// it on a second branch.
func gitRepo(t *testing.T, origin, branch string) (repo, worktree string) {
	t.Helper()
	dir := t.TempDir()
	repo, worktree = filepath.Join(dir, "repo"), filepath.Join(dir, "wt")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", branch, repo)
	run("-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	if origin != "" {
		run("-C", repo, "remote", "add", "origin", origin)
	}
	run("-C", repo, "worktree", "add", "-q", "-b", "wt-branch", worktree)
	// Temp dirs may sit behind a symlink, which git resolves.
	repo, _ = filepath.EvalSymlinks(repo)
	worktree, _ = filepath.EvalSymlinks(worktree)
	return repo, worktree
}

func TestWhere(t *testing.T) {
	repo, wt := gitRepo(t, "git@github.com:o/r.git", "feat/x")
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	for dir, want := range map[string]Place{
		repo:                       {repo, "o/r", "feat/x"},
		filepath.Join(repo, "sub"): {repo, "o/r", "feat/x"},
		wt:                         {wt, "o/r", "wt-branch"},
	} {
		if got, ok := Where(dir); !ok || got != want {
			t.Errorf("Where(%q) = %+v, %v", dir, got, ok)
		}
	}
	for _, dir := range []string{t.TempDir(), "", filepath.Join(repo, "missing")} {
		if got, ok := Where(dir); ok {
			t.Errorf("Where(%q) = %+v", dir, got)
		}
	}

	bare, _ := gitRepo(t, "", "main")
	if got, _ := Where(bare); got.Repo != "" || got.Branch != "main" {
		t.Errorf("without origin: %+v", got)
	}
}

func TestRemoteRepo(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/o/r.git":    "o/r",
		"https://github.com/o/r":        "o/r",
		"https://github.com/o/r/":       "o/r",
		"git@github.com:o/r.git":        "o/r",
		"ssh://git@github.com/o/r.git":  "o/r",
		"ssh://git@github.com:22/o/r":   "o/r",
		"https://ghe.example.com/a/o/r": "o/r",
		"/local/path":                   "",
		"https://github.com/o":          "",
		"":                              "",
	} {
		if got := remoteRepo(url); got != want {
			t.Errorf("remoteRepo(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestRecordPlaces(t *testing.T) {
	s := newStore(t)
	a, b := Place{"/w/a", "o/r", "x"}, Place{"/w/a", "o/r", "y"}
	for _, at := range []Place{a, b, a, {}} {
		if err := s.Record("s1", "https://github.com/o/r/pull/1", false, "", at); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.load("s1").Places; !slices.Equal(got, []Place{a, b}) {
		t.Errorf("places = %+v", got)
	}
}

func TestResolveDir(t *testing.T) {
	repo, wt := gitRepo(t, "git@github.com:o/r.git", "feat/x")
	s := newStore(t)
	heads := map[string]string{}
	head := func(url string) string { return heads[url] }
	write := func(name, content string, age time.Duration) {
		t.Helper()
		os.MkdirAll(s.Trails.Dir, 0o755)
		os.WriteFile(s.Trails.File(name), []byte(content), 0o644)
		at := time.Now().Add(-age)
		os.Chtimes(s.Trails.File(name), at, at)
	}
	resolve := func(dir string) string {
		t.Helper()
		name, _ := s.ResolveDir(dir, head)
		return name
	}

	if got := resolve(repo); got != "" {
		t.Errorf("without trails: %q", got)
	}

	// A PR whose head GitHub hasn't told yet matches nothing.
	write("old", "# old\n\n- [ ] O/R#1\n", 2*time.Hour)
	write("other-repo", "# other-repo\n\n- [ ] o/s#2\n", 0)
	heads["https://github.com/o/s/pull/2"] = "feat/x"
	if got := resolve(repo); got != "" {
		t.Errorf("unknown head: %q", got)
	}
	heads["https://github.com/O/R/pull/1"] = "feat/x"
	if got := resolve(filepath.Join(repo)); got != "old" {
		t.Errorf("by PR head = %q", got)
	}

	// Of several matching trails, the one changed last.
	write("new", "# new\n\n- [ ] free\n- [ ] o/r#3\n", time.Hour)
	heads["https://github.com/o/r/pull/3"] = "feat/x"
	if got := resolve(repo); got != "new" {
		t.Errorf("several = %q", got)
	}

	// The worktree's branch has no PR yet, but a session recorded one from it.
	if got := resolve(wt); got != "" {
		t.Errorf("worktree without a match: %q", got)
	}
	write("w", "# w\n\n- [ ] o/q#9\n", 0)
	s.Record("s2", "https://github.com/o/q/pull/9", false, "", Place{wt, "o/r", "wt-branch"}) // not on a trail
	if got := resolve(wt); got != "" {
		t.Errorf("a session without a trail: %q", got)
	}
	s.Record("s1", "https://github.com/o/q/pull/9", false, "", Place{wt, "o/r", "wt-branch"})
	s.Bind("s1", "w")
	if got := resolve(wt); got != "w" {
		t.Errorf("by place = %q", got)
	}
	// A PR match beats a place.
	s.Record("s1", "https://github.com/o/q/pull/9", false, "", Place{repo, "o/r", "feat/x"})
	if got := resolve(repo); got != "new" {
		t.Errorf("PR head vs place = %q", got)
	}

	if got := resolve(t.TempDir()); got != "" {
		t.Errorf("outside a repo: %q", got)
	}
}

func TestFind(t *testing.T) {
	repo, _ := gitRepo(t, "https://github.com/o/r.git", "feat/x")
	s := newStore(t)
	os.MkdirAll(s.Trails.Dir, 0o755)
	os.WriteFile(s.Trails.File("folder"), []byte("# folder\n\n- [ ] o/r#1\n"), 0o644)
	head := func(string) string { return "feat/x" }

	tests := []struct {
		name, agent, pane, dir string
		setup                  func()
		want                   Found
	}{
		{"nothing", "a1", "", "", nil, Found{Key: "a1"}},
		{"folder", "a1", "", repo, nil, Found{Key: "a1", Trail: "folder", Via: "dir", Place: Place{repo, "o/r", "feat/x"}}},
		{"pane's session", "", "p1", repo, func() { s.Bind("a2", "theirs"); s.NotePane("a2", "p1") }, Found{Key: "a2", Trail: "theirs", Via: "pane"}},
		{"pane as a session", "", "p2", repo, func() { s.Bind("p2", "human") }, Found{Key: "p2", Trail: "human", Via: "session"}},
		{"agent beats the pane", "a1", "p1", repo, func() { s.Bind("a1", "mine") }, Found{Key: "a1", Trail: "mine", Via: "session"}},
		{"agent without a trail follows the pane", "a3", "p1", repo, nil, Found{Key: "a2", Trail: "theirs", Via: "pane"}},
		{"no pane nor agent", "", "", repo, nil, Found{Trail: "folder", Via: "dir", Place: Place{repo, "o/r", "feat/x"}}},
	}
	for _, tt := range tests {
		if tt.setup != nil {
			tt.setup()
		}
		if got := s.Find(tt.agent, tt.pane, tt.dir, head); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
