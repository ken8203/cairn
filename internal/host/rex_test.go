package host

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRexBin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("REX_BIN_DIR", dir)
	if got, want := rexBin(), filepath.Join(dir, "rex"); got != want {
		t.Errorf("rexBin() off PATH = %q, want %q", got, want)
	}
	t.Setenv("REX_BIN_DIR", "")
	if got := rexBin(); got != "rex" {
		t.Errorf("rexBin() without REX_BIN_DIR = %q, want rex", got)
	}
	onPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(onPath, "rex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath)
	if got, want := rexBin(), filepath.Join(onPath, "rex"); got != want {
		t.Errorf("rexBin() on PATH = %q, want %q", got, want)
	}
}

func TestRexTab(t *testing.T) {
	for window, want := range map[string]string{
		"Window 0c450b66": "",
		"checkout v2":     "checkout v2",
	} {
		clearEnv(t)
		t.Setenv("REX_BLOCK", "block:b1")
		_, log := fakeCLIs(t, map[string]string{"rex": `case "$1" in
block) echo '{"block":{"block_id":"block:b1","window_id":"window:01a1-af47-e30c450b66"}}' ;;
window) echo '{"label":"` + window + `"}' ;;
esac`})
		if got := Tab(); got != want {
			t.Errorf("window %q: Tab = %q, want %q", window, got, want)
		}
		wantLog(t, log(), "rex block inspect block:b1 --json", "rex window inspect window:01a1-af47-e30c450b66 --json")
	}
}

func TestRexPanel(t *testing.T) {
	clearEnv(t)
	t.Setenv("REX_BLOCK", "block:b1")
	dir, log := fakeCLIs(t, map[string]string{"rex": "cat > stdin"})
	if err := Panel(cairn, "", "", true); err != nil {
		t.Fatal(err)
	}
	wantLog(t, log(), "rex do - -b block:b1")
	want := rexPanelLua + `return toggle(rex.json.decode([==[{"block":"block:b1","command":["/opt/my bin/cairn","watch","b1"],"close":true,"label":"cairn"}]==]))` + "\n"
	if b, _ := os.ReadFile(filepath.Join(dir, "stdin")); string(b) != want {
		t.Errorf("rex read %q", b)
	}

	// The panel falls back to the caller's folder.
	dir, _ = fakeCLIs(t, map[string]string{"rex": "cat > stdin"})
	if err := Panel(cairn, "", "/w/my repo", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "stdin")); !strings.Contains(string(b), `"command":["/opt/my bin/cairn","watch","b1","--dir","/w/my repo"],"close":false`) {
		t.Errorf("rex read %q", b)
	}
}

func TestRexPanelLua(t *testing.T) {
	clearEnv(t)
	t.Setenv("REX_BLOCK", "block:b1")
	got, err := RexPanelLua(cairn, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, rexPanelLua) || !strings.Contains(got, `"block":"block:b1"`) {
		t.Errorf("RexPanelLua = %q", got)
	}
	if got2, _ := RexPanelLua(cairn, "b1", "", false); got2 != got {
		t.Errorf("RexPanelLua for the named block = %q, want %q", got2, got)
	}
	if _, err := RexPanelLua(cairn, "tmux-1.2", "", false); err == nil {
		t.Error("RexPanelLua for a tmux pane succeeded")
	}
	clearEnv(t)
	if _, err := RexPanelLua(cairn, "", "", false); err == nil {
		t.Error("RexPanelLua outside Rex succeeded")
	}
}

func TestRexDir(t *testing.T) {
	clearEnv(t)
	t.Setenv("REX_SESSION", "session:s1")
	_, log := fakeCLIs(t, map[string]string{"rex": `case "$*" in
*block:b1*) echo '{"child":{"pid":1},"foreground":{"pid":9,"cwd":"/w/api"}}' ;;
*) echo 'Error: block target not found' >&2; exit 1 ;;
esac`})
	for block, want := range map[string]string{"b1": "/w/api", "gone": ""} {
		if got := Dir(block); got != want {
			t.Errorf("Dir(%q) = %q, want %q", block, got, want)
		}
	}
	if !slices.Contains(log(), "rex block call -s session:s1 -b block:b1 process {}") {
		t.Errorf("ran %q", log())
	}
	if Dir("tmux-4242.3") != "" {
		t.Error("tmux pane has a folder")
	}
}

// A block holds cairn when its foreground program is cairn or an ancestor of it. A Codex
// app-server started from another tab's shell, or from a block that is gone, is not.
func TestRexHolds(t *testing.T) {
	clearEnv(t)
	t.Setenv("REX_SESSION", "session:s1")
	_, log := fakeCLIs(t, map[string]string{"rex": `case "$*" in
*block:mine*) echo "{\"child\":{\"pid\":1},\"foreground\":{\"pid\":$PPID}}" ;;
*block:other*) echo '{"child":{"pid":1},"ancestors":[{"pid":'$PPID'}],"foreground":{"pid":9}}' ;;
*) echo 'Error: block target not found' >&2; exit 1 ;;
esac`})
	for block, want := range map[string]bool{"mine": true, "other": false, "gone": false} {
		if got := Holds(block); got != want {
			t.Errorf("Holds(%q) = %v, want %v", block, got, want)
		}
	}
	if !slices.Contains(log(), "rex block call -s session:s1 -b block:mine process {}") {
		t.Errorf("ran %q", log())
	}
	// Hosts that can't tell trust their env.
	if !Holds("tmux-4242.3") {
		t.Error("tmux pane not trusted")
	}
}
