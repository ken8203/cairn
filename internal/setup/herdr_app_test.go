package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHerdrAppHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HERDR_APP_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := HerdrAppHome(); got != filepath.Join(home, ".config", "herdr-app") {
		t.Fatal(got)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if got := HerdrAppHome(); got != filepath.Join(home, "xdg", "herdr-app") {
		t.Fatal(got)
	}
	t.Setenv("HERDR_APP_HOME", filepath.Join(home, "custom"))
	if got := HerdrAppHome(); got != filepath.Join(home, "custom") {
		t.Fatal(got)
	}
}

func TestHerdrAppInstall(t *testing.T) {
	home := filepath.Join(t.TempDir(), "app")
	t.Setenv("HERDR_APP_HOME", home)
	fakes(t)
	var out strings.Builder
	s := steps{w: &out}
	s.herdrApp()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("installed without app")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "herdr-app"), []byte("#!/bin/sh\nexit 99\n"), 0o755)
	t.Setenv("PATH", bin)
	s.dryRun = true
	s.herdrApp()
	if !strings.Contains(out.String(), "dry run; would write") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("dry run wrote assets")
	}
	if c := herdrAppCheck(); c.Status != Warn {
		t.Fatal(c)
	}
	s.dryRun = false
	out.Reset()
	s.herdrApp()
	if !strings.Contains(out.String(), "done herdr-mod") || !strings.Contains(out.String(), "mod.open:cairn") || strings.Contains(out.String(), "herdr-app-layout") {
		t.Fatal(out.String())
	}
	dir := filepath.Join(home, "mods", "cairn")
	manifest, err := os.ReadFile(filepath.Join(dir, "mod.json"))
	if err != nil || string(manifest) != "{\"place\":\"none\",\"overlay\":{\"kind\":\"window\",\"width\":920,\"height\":420}}\n" {
		t.Fatalf("floating manifest: %s, %v", manifest, err)
	}

	file := filepath.Join(dir, "main.js")
	info, _ := os.Stat(file)
	out.Reset()
	s.herdrApp()
	after, _ := os.Stat(file)
	if !strings.Contains(out.String(), "ok herdr-mod") || info.ModTime() != after.ModTime() {
		t.Fatal("not idempotent")
	}
	if c := herdrAppCheck(); c.Status != OK {
		t.Fatal(c)
	}
	self, _ := os.Executable()
	b, _ := os.ReadFile(filepath.Join(dir, "binary.js"))
	if !strings.Contains(string(b), self) {
		t.Fatalf("binary path: %s", b)
	}
	// Generated content from an older install is safe to upgrade.
	if err := writeMod(dir, modFiles("/old path/cairn")); err != nil {
		t.Fatal(err)
	}
	if c := herdrAppCheck(); c.Status != Warn {
		t.Fatal(c)
	}
	out.Reset()
	s.herdrApp()
	if c := herdrAppCheck(); c.Status != OK {
		t.Fatal(c)
	}
	// User edits stop the whole update, including stale generated binary.js.
	touch(t, dir, "main.js", "user edits")
	before, _ := os.ReadFile(filepath.Join(dir, modOwnership))
	out.Reset()
	s.herdrApp()
	if !strings.Contains(out.String(), "manual conflict") {
		t.Fatal(out.String())
	}
	if c := herdrAppCheck(); c.Status != Warn {
		t.Fatal(c)
	}
	b, _ = os.ReadFile(file)
	if string(b) != "user edits" {
		t.Fatal("lost user edits")
	}
	b, _ = os.ReadFile(filepath.Join(dir, modOwnership))
	if string(b) != string(before) {
		t.Fatal("changed ownership on conflict")
	}
	if _, err := os.Stat(filepath.Join(home, "layouts")); !os.IsNotExist(err) {
		t.Fatal("wrote layouts")
	}
	if _, err := os.Stat(filepath.Join(home, "keys.json")); !os.IsNotExist(err) {
		t.Fatal("wrote keys")
	}
}

func TestHerdrAppUnknownAssets(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "main.js", "unknown")
	if _, err := modChanges(dir, modFiles("/cairn")); err == nil {
		t.Fatal("accepted unknown content")
	}
	os.Remove(filepath.Join(dir, "main.js"))
	otherDir := t.TempDir()
	touch(t, otherDir, "main.js", "other")
	other := filepath.Join(otherDir, "main.js")
	os.Symlink(other, filepath.Join(dir, "main.js"))
	if _, err := modChanges(dir, modFiles("/cairn")); err == nil {
		t.Fatal("accepted symlink")
	}
}
