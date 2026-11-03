package orca

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeOrca answers from the files in $FAKE and logs each call that changes a card to
// $FAKE/log.
const fakeOrca = `#!/bin/sh
case "$1 $2" in
"status --json") /bin/cat "$FAKE/status.json" ;;
"worktree list") /bin/cat "$FAKE/list.json" ;;
"worktree show") /bin/cat "$FAKE/show.json" ;;
*) echo "$*" >> "$FAKE/log" ;;
esac
`

const (
	api    = "/Users/me/orca/workspaces/api/feat-x"
	web    = "/Users/me/orca/workspaces/web/feat-y"
	u121   = "https://github.com/acme/api/pull/121"
	u120   = "https://github.com/acme/api/pull/120"
	webPR  = "https://github.com/acme/web/pull/45"
	status = `{"result":{"runtime":{"reachable":true}}}`
)

// fake puts a fake orca alone on PATH, answering with the worktrees in list and the
// linked items in show; it returns the file the changes are logged to.
func fake(t *testing.T, list, show string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0o755)
	files := map[string]string{
		"bin/orca":    fakeOrca,
		"status.json": status,
		"list.json":   `{"result":{"hostScope":"local","totalCount":2,"truncated":false,"worktrees":[` + list + `]}}`,
		"show.json":   `{"result":{"worktree":{"path":"` + api + `","linkedItems":[` + show + `]}}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", dir)
	t.Setenv("FAKE", dir)
	return filepath.Join(dir, "log")
}

func wtJSON(path, comment string, linked int) string {
	return `{"id":"d058::` + path + `","repoId":"d058","path":"` + path + `","branch":"refs/heads/me/feat-x","displayName":"feat-x","comment":"` + comment + `","linkedPR":` + strconv.Itoa(linked) + `,"isMainWorktree":false}`
}

func calls(log string) []string {
	b, _ := os.ReadFile(log)
	return slices.DeleteFunc(strings.Split(string(b), "\n"), func(s string) bool { return s == "" })
}

// onAPI finds trail checkout-v2 for the api worktree alone.
func onAPI(c Card) func(string) (Card, bool) {
	return func(path string) (Card, bool) { return c, path == api }
}

func TestSync(t *testing.T) {
	note := Note("checkout-v2", 1, 3, "api#121")
	tests := []struct {
		name, comment string
		linked        int
		card          Card
		show          string
		want          []string
	}{
		{
			name: "links the branch's PR and writes an empty note",
			card: Card{Repo: "acme/api", PR: 121, Note: note},
			want: []string{"worktree set --worktree path:" + api + " --pr 121 --comment " + note + " --json"},
		},
		{
			name:    "rewrites its own note",
			comment: "cairn · checkout-v2 0/3 ▶ api#120",
			linked:  121,
			card:    Card{Repo: "acme/api", PR: 121, Note: note},
			want:    []string{"worktree set --worktree path:" + api + " --comment " + note + " --json"},
		},
		{
			name:    "leaves the user's note alone",
			comment: "ask Sam about retries",
			linked:  120,
			card:    Card{Repo: "acme/api", PR: 121, Note: note},
			want:    []string{"worktree set --worktree path:" + api + " --pr 121 --json"},
		},
		{
			name:    "nothing changed",
			comment: note,
			linked:  121,
			card:    Card{Repo: "acme/api", PR: 121, Note: note, Refs: []string{u120, webPR}},
			show:    `{"provider":"github","type":"pr","number":121},{"provider":"github","type":"pr","number":120},{"provider":"github","type":"pr","number":45,"url":"` + webPR + `"}`,
		},
		{
			name:    "adds only the missing references",
			comment: note,
			linked:  121,
			card:    Card{Repo: "acme/api", PR: 121, Note: note, Refs: []string{u120, webPR}},
			show:    `{"provider":"github","type":"pr","number":121},{"provider":"github","type":"pr","number":45,"url":"` + webPR + `"}`,
			want:    []string{"reference add " + u120 + " --worktree path:" + api + " --json"},
		},
		{
			name:    "a same-numbered PR in another repo is not the reference",
			comment: note,
			linked:  121,
			card:    Card{Repo: "acme/api", PR: 121, Note: note, Refs: []string{"https://github.com/acme/web/pull/121"}},
			show:    `{"provider":"github","type":"pr","number":121}`,
			want:    []string{"reference add https://github.com/acme/web/pull/121 --worktree path:" + api + " --json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := fake(t, wtJSON(api, tt.comment, tt.linked)+","+wtJSON(web, "", 0), tt.show)
			Sync(onAPI(tt.card))
			if got := calls(log); !slices.Equal(got, tt.want) {
				t.Errorf("calls %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSyncWithoutOrca(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	asked := false
	Sync(func(string) (Card, bool) { asked = true; return Card{}, false })
	if asked {
		t.Error("looked up a card without Orca")
	}
	if _, err := Count(onAPI(Card{})); err != ErrNotInstalled {
		t.Errorf("Count = %v", err)
	}
}

func TestSyncRuntimeDown(t *testing.T) {
	log := fake(t, wtJSON(api, "", 0), "")
	os.WriteFile(filepath.Join(filepath.Dir(log), "status.json"), []byte(`{"result":{"runtime":{"reachable":false}}}`), 0o644)
	Sync(onAPI(Card{Repo: "acme/api", PR: 121, Note: "x"}))
	if got := calls(log); len(got) > 0 {
		t.Errorf("calls %q", got)
	}
}

func TestCount(t *testing.T) {
	fake(t, wtJSON(api, "", 0)+","+wtJSON(web, "", 0), "")
	if n, err := Count(onAPI(Card{})); n != 1 || err != nil {
		t.Errorf("Count = %d, %v", n, err)
	}
}

func TestPathInApplications(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", home)
	if got := Path(); got != "" {
		t.Errorf("Path = %q", got)
	}
	bin := filepath.Join(home, "Applications", "Orca.app", "Contents", "Resources", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "orca"), []byte("#!/bin/sh\n"), 0o755)
	if got := Path(); got != filepath.Join(bin, "orca") {
		t.Errorf("Path = %q", got)
	}
}

func TestNote(t *testing.T) {
	if got := Note("checkout-v2", 2, 4, "api#121"); got != "cairn · checkout-v2 2/4 ▶ api#121" {
		t.Errorf("Note = %q", got)
	}
	if got := Note("checkout-v2", 4, 4, ""); got != "cairn · checkout-v2 ✓ all done" {
		t.Errorf("Note = %q", got)
	}
}
