package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var buildDir string

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

var build = sync.OnceValues(func() (string, error) {
	var err error
	if buildDir, err = os.MkdirTemp("", "cairn-test"); err != nil {
		return "", err
	}
	bin := filepath.Join(buildDir, "cairn-bin")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		return "", errors.New(string(out))
	}
	return bin, nil
})

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// waitLog waits for the detached record to land.
func waitLog(path string, n int) []string {
	for range 50 {
		if l := readURLs(path); len(l) >= n {
			return l
		}
		time.Sleep(20 * time.Millisecond)
	}
	return readURLs(path)
}

// hookEnv puts the built cairn in a temp dir beside a stub cairn that logs what it would
// record, with a temp home.
func hookEnv(t *testing.T) (dir string, env []string) {
	t.Helper()
	bin, err := build()
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	copyFile(t, bin, filepath.Join(dir, "cairn-bin"))
	stub := `#!/bin/sh
echo "$* pane=$REX_BLOCK" >> "$LOG"
`
	if err := os.WriteFile(filepath.Join(dir, "cairn"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + dir,
		"LOG=" + filepath.Join(dir, "log"),
		"CAIRN_HOME=" + filepath.Join(dir, "home"),
	}
	return dir, env
}

func runHookBin(t *testing.T, dir string, env []string, payload string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, "cairn-bin"), "hook")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	return string(out)
}

func TestHookSpawnsRecord(t *testing.T) {
	tests := []struct {
		name, payload string
		record        []string
	}{
		{
			"Claude Code push after cd",
			`{"session_id":"a61fbdc6-1","cwd":"/abs","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"cd sub && git push -u origin HEAD"},"tool_response":{"stdout":"","stderr":"To github.com:o/r.git","interrupted":false,"isImage":false,"noOutputExpected":false}}`,
			[]string{"record --session a61fbdc6-1 --own -C /abs/sub pane=block:p1"},
		},
		{
			"Codex pr create",
			`{"session_id":"01a19fee-0b76","turn_id":"t","cwd":"/abs","hook_event_name":"PostToolUse","model":"m","tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":"https://github.com/o/r/pull/998\n"}`,
			[]string{"record --session 01a19fee-0b76 --own -C /abs https://github.com/o/r/pull/998 pane=block:p1"},
		},
		{
			"unrelated command",
			`{"session_id":"s1","cwd":"/abs","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":""}`,
			nil,
		},
		{
			"invalid session id",
			`{"session_id":"../x","cwd":"/abs","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"git push"},"tool_response":""}`,
			nil,
		},
		{"not JSON", `{`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, env := hookEnv(t)
			if out := runHookBin(t, dir, append(env, "REX_BLOCK=block:p1"), tt.payload); out != "" {
				t.Errorf("stdout = %q", out)
			}
			got := waitLog(filepath.Join(dir, "log"), len(tt.record))
			if len(tt.record) == 0 {
				time.Sleep(100 * time.Millisecond)
				got = readURLs(filepath.Join(dir, "log"))
			}
			if !slices.Equal(got, tt.record) {
				t.Errorf("spawned %q, want %q", got, tt.record)
			}
		})
	}
}

func TestHookSessionStart(t *testing.T) {
	dir, env := hookEnv(t)
	start := `{"session_id":"s1","cwd":"/abs","hook_event_name":"SessionStart","source":"startup"}`
	if out := runHookBin(t, dir, env, start); out != "" {
		t.Errorf("without a trail: %q", out)
	}

	state := filepath.Join(dir, "home")
	for file, content := range map[string]string{
		"trails/t.md":      "# t\n\n- [x] release\n- [ ] flag on\n\n## Notes\n",
		"sessions/s1.json": `{"trail":"t","prs":[],"updated":"2026-11-03T10:00:00Z"}` + "\n",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(state, file)), 0o755)
		if err := os.WriteFile(filepath.Join(state, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"cairn: this session is on trail t, 1/2 steps done, next \"flag on\". Run ` + "`cairn`" + ` to see it; ` + "`cairn skills get cairn`" + ` has the guide."}}` + "\n"
	if out := runHookBin(t, dir, append(env, "REX_BLOCK=block:p1"), start); out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	if got := readName(filepath.Join(state, "panes", "p1")); got != "s1" {
		t.Errorf("pane points at %q", got)
	}
}

func TestHookSessionStartFolderHint(t *testing.T) {
	dir, env := hookEnv(t)
	repo := folderRepo(t, newApp(filepath.Join(dir, "home")))
	start := `{"session_id":"s2","cwd":"` + repo + `","hook_event_name":"SessionStart","source":"startup"}`
	want := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"cairn: this folder's branch feat/x is on trail t (0/2 steps done, next o/r#1). If you're continuing that work, run ` + "`cairn use t`" + `."}}` + "\n"
	if out := runHookBin(t, dir, env, start); out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	// Only a hint: the session stays off the trail, and no place is recorded.
	if _, err := os.Stat(filepath.Join(dir, "home", "sessions", "s2.json")); err == nil {
		t.Error("SessionStart wrote the session")
	}

	// In a folder whose branch is on no trail, nothing.
	start = `{"session_id":"s2","cwd":"` + t.TempDir() + `","hook_event_name":"SessionStart","source":"startup"}`
	if out := runHookBin(t, dir, env, start); out != "" {
		t.Errorf("outside the repo: %q", out)
	}
}

// Codex asks the user to trust a hook again whenever its definition in hooks.json
// changes, so the file stays fixed and every change of behavior goes into cairn hook.
func TestHooksFileUnchanged(t *testing.T) {
	b, err := os.ReadFile("../hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != "6733d9def30e2d6d86ac425a0029e4af062036d13f0c957cbbd5adad29bce13a" {
		t.Errorf("hooks/hooks.json changed (sha256 %s); users would have to trust its hooks in Codex again", got)
	}
}
