package host

import (
	"os"
	"path/filepath"
	"testing"
)

func nativeMod(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "mods", "cairn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.js", "mod.json", "binary.js", "dock.js"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestHerdrNativePanel(t *testing.T) {
	home := nativeMod(t)
	dir, log := fakeCLIs(t, map[string]string{"herdr-app": `case "$*" in
 status) echo '{"layout":"main"}' ;;
 'do mod.open --name cairn') if [ -f open ]; then rm open; else touch open; fi ;;
 'do overlay.close --id mod-cairn') rm -f open ;;
 *) exit 1 ;;
 esac`})
	// No layout files: the shipped layout works without a personal block or node id.
	for _, tt := range []struct{ close, open bool }{{false, true}, {false, false}, {false, true}, {true, false}, {true, false}} {
		handled, err := HerdrPanel(home, tt.close)
		if !handled || err != nil {
			t.Fatalf("handled %v, %v", handled, err)
		}
		_, err = os.Stat(filepath.Join(dir, "open"))
		if (err == nil) != tt.open {
			t.Fatalf("close=%v: open=%v", tt.close, err == nil)
		}
	}
	wantLog(t, log(),
		"herdr-app status", "herdr-app do mod.open --name cairn",
		"herdr-app status", "herdr-app do mod.open --name cairn",
		"herdr-app status", "herdr-app do mod.open --name cairn",
		"herdr-app status", "herdr-app do overlay.close --id mod-cairn",
		"herdr-app status", "herdr-app do overlay.close --id mod-cairn")
	if _, err := os.Stat(filepath.Join(home, "layouts")); !os.IsNotExist(err) {
		t.Fatal("created layouts")
	}
}

func TestHerdrNativePanelUnavailable(t *testing.T) {
	for _, missing := range []string{"main.js", "mod.json", "binary.js", "dock.js"} {
		t.Run(missing, func(t *testing.T) {
			home := nativeMod(t)
			os.Remove(filepath.Join(home, "mods", "cairn", missing))
			fakeCLIs(t, map[string]string{"herdr-app": "exit 99"})
			for _, close := range []bool{false, true} {
				if handled, err := HerdrPanel(home, close); handled || err != nil {
					t.Fatalf("missing asset fallback: %v %v", handled, err)
				}
			}
		})
	}
	home := nativeMod(t)
	for _, scripts := range []map[string]string{{}, {"herdr-app": "exit 1"}} {
		fakeCLIs(t, scripts)
		if handled, err := HerdrPanel(home, false); handled || err != nil {
			t.Fatalf("unavailable app fallback: %v %v", handled, err)
		}
	}
}

func TestHerdrNativePanelCommandFailure(t *testing.T) {
	home := nativeMod(t)
	fakeCLIs(t, map[string]string{"herdr-app": `if [ "$1" = status ]; then echo '{}'; else echo 'command failed' >&2; exit 1; fi`})
	for _, close := range []bool{false, true} {
		if handled, err := HerdrPanel(home, close); !handled || err == nil {
			t.Fatalf("command failure: %v %v", handled, err)
		}
	}
}

func TestHerdrCloseDock(t *testing.T) {
	home := nativeMod(t)
	dir := filepath.Join(home, "layouts")
	os.MkdirAll(dir, 0o755)
	// A colliding ID and a standalone Cairn copy are not installed docks.
	content := `{"children":[{"id":"cairn-dock","content":{"mod":"files"}},
 {"id":"custom-cairn","content":{"mod":"cairn"}},
 {"id":"installed-1","content":{"mod":"cairn","props":{"cairnMode":"dock"}}},
 {"children":[{"id":"installed-2","content":{"mod":"cairn","props":{"cairnMode":"dock"}}}]}]}`
	file := filepath.Join(dir, "personal.json")
	os.WriteFile(file, []byte(content), 0o644)
	_, log := fakeCLIs(t, map[string]string{"herdr-app": `case "$1" in status) echo '{"layout":"personal"}' ;; esac`})
	for range 2 {
		if handled, err := HerdrPanel(home, true); !handled || err != nil {
			t.Fatalf("close: %v %v", handled, err)
		}
	}
	calls := []string{"herdr-app status", "herdr-app do overlay.close --id mod-cairn", "herdr-app do layout.hide --id installed-1", "herdr-app do layout.hide --id installed-2"}
	wantLog(t, log(), append(calls, calls...)...)
	got, _ := os.ReadFile(file)
	if string(got) != content {
		t.Fatal("close edited layout")
	}
}

func TestHerdrCloseMalformedLayout(t *testing.T) {
	home := nativeMod(t)
	for _, status := range []string{`not json`, `{"layout":"../other"}`} {
		_, log := fakeCLIs(t, map[string]string{"herdr-app": `if [ "$1" = status ]; then echo '` + status + `'; fi`})
		if handled, err := HerdrPanel(home, true); !handled || err == nil {
			t.Fatalf("unsafe status: %v %v", handled, err)
		}
		wantLog(t, log(), "herdr-app status", "herdr-app do overlay.close --id mod-cairn")
	}
}
