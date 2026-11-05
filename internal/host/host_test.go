package host

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// clearEnv starts a test outside every host.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, h := range hosts {
		t.Setenv(h.env, "")
	}
	t.Setenv("HC_SCOPE", "")
}

const hcScope = "17396136072524-68dbd673"

func TestPane(t *testing.T) {
	fakeCLIs(t, map[string]string{"hc": `echo '{"core":{"id":"` + hcScope + `"}}'`})
	tests := []struct {
		env  map[string]string
		want string
	}{
		{nil, ""},
		{map[string]string{"REX_BLOCK": "block:b1"}, "b1"},
		{map[string]string{"HC_TERMINAL_ID": "t3"}, "hc-t3.17396136072524-68dbd673"},
		// Herdr.app, then Rex.
		{map[string]string{"REX_BLOCK": "block:b1", "HC_TERMINAL_ID": "t3"}, "hc-t3.17396136072524-68dbd673"},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("HC_SCOPE", hcScope)
		for k, v := range tt.env {
			t.Setenv(k, v)
		}
		if got := Pane(); got != tt.want {
			t.Errorf("%v: Pane = %q, want %q", tt.env, got, tt.want)
		}
	}
	// cairn watch takes every host's key as it is stored, and a Rex block as Rex names it.
	for arg, want := range map[string]string{
		"block:b1":                      "b1",
		"b1":                            "b1",
		"hc-t3.17396136072524-68dbd673": "hc-t3.17396136072524-68dbd673",
	} {
		if got := Key(arg); got != want {
			t.Errorf("Key(%q) = %q", arg, got)
		}
	}
}

func TestName(t *testing.T) {
	clearEnv(t)
	if got := Name(); got != "" {
		t.Errorf("Name outside every host = %q", got)
	}
	for _, h := range hosts {
		clearEnv(t)
		t.Setenv(h.env, "x1")
		if got := Name(); got != h.name || got == "" {
			t.Errorf("Name with %s set = %q", h.env, got)
		}
	}
}

// Each stored key goes to its own host's lookup with the key body.
func TestTitleDispatch(t *testing.T) {
	var asked []string
	hs := slices.Clone(hosts)
	for i := range hs {
		name := cmp.Or(hs[i].prefix, "rex")
		hs[i].title = func(body string) string {
			asked = append(asked, name+" "+body)
			return name
		}
	}
	tests := []struct{ key, want, asked string }{
		{"b1", "rex", "rex b1"},
		{"hc-t3.17396136072524-68dbd673", "hc", "hc t3.17396136072524-68dbd673"},
		{"hcish", "rex", "rex hcish"},
	}
	for _, tt := range tests {
		asked = nil
		if got := title(hs, tt.key); got != tt.want || strings.Join(asked, ",") != tt.asked {
			t.Errorf("title(%q) = %q, asked %q", tt.key, got, asked)
		}
	}
}

// Titles look hc panes up by what their key bodies name.
func TestBodies(t *testing.T) {
	for body, want := range map[string]string{"t3.1739-68db": "t3", "t3": "t3"} {
		if got := hcAddr(body); got != want {
			t.Errorf("hcAddr(%q) = %q, want %q", body, got, want)
		}
	}
}

// fakeCLIs puts the scripts, named after host CLIs, first on PATH. Each logs its
// arguments to the log it returns, then runs its body in the dir holding that log.
func fakeCLIs(t *testing.T, scripts map[string]string) (dir string, log func() []string) {
	t.Helper()
	dir = t.TempDir()
	for name, body := range scripts {
		script := "#!/bin/sh\ncd " + dir + "\necho \"" + name + " $*\" >> log\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return dir, func() []string {
		b, _ := os.ReadFile(filepath.Join(dir, "log"))
		return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	}
}

func wantLog(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

const cairn = "/opt/my bin/cairn"

func TestPanelNoPane(t *testing.T) {
	clearEnv(t)
	if err := Panel(cairn, "", "", false); !errors.Is(err, ErrNoPanel) {
		t.Errorf("Panel outside every host = %v", err)
	}
}

// A CLI's failure is one line naming it.
func TestPanelError(t *testing.T) {
	clearEnv(t)
	t.Setenv("REX_BLOCK", "block:b1")
	fakeCLIs(t, map[string]string{"rex": "echo 'not running\nstart rex' >&2; exit 1"})
	err := Panel(cairn, "", "", false)
	if err == nil || !strings.HasSuffix(err.Error(), "/rex do: not running") {
		t.Errorf("Panel = %v", err)
	}
}

// A key names the pane outside its host's env, as a host's own keybinding passes it.
func TestPanelForKey(t *testing.T) {
	clearEnv(t)
	tests := []struct{ key, want string }{
		{"b1", "rex do - -b block:b1"},
		{"hc-t3." + hcScope, "hc get cairn-t3 --json"},
	}
	for _, tt := range tests {
		_, log := fakeCLIs(t, map[string]string{"rex": "", "hc": ""})
		Panel(cairn, tt.key, "", false)
		if got := log()[0]; got != tt.want {
			t.Errorf("Panel(%q) ran %q", tt.key, got)
		}
	}
}
