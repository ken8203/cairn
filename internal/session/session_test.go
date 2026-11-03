package session

import (
	"fmt"
	"os"
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
	if err := s.Record("s1", "https://github.com/o/r/pull/1", false, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.sessionFile("s1"))
	if !regexp.MustCompile(`^\{"trail":null,"prs":\["https://github.com/o/r/pull/1"\],"updated":"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}(Z|[+-][0-9:]{5})"\}\n$`).Match(b) {
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
			if err := s.Record("s1", fmt.Sprintf("https://github.com/o/r/pull/%d", i), false, ""); err != nil {
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
