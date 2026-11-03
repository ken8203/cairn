package host

import (
	"os"
	"path/filepath"
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
