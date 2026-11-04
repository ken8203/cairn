package host

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHcIdentity(t *testing.T) {
	clearEnv(t)
	t.Setenv("HC_TERMINAL_ID", "t3")
	fakeCLIs(t, map[string]string{"hc": `echo '{}'`})
	if got := Pane(); got != "" {
		t.Fatalf("missing core: %q", got)
	}
	fakeCLIs(t, map[string]string{"hc": `case "$1 $2" in
 "server status") echo '{"core":{"id":"core-1"}}' ;;
 "get t3") echo '{"address":"t3"}' ;;
 *) exit 1 ;;
 esac`})
	for _, scope := range []string{"", "old-shell", "recovered-shell"} {
		t.Setenv("HC_SCOPE", scope)
		if got := Pane(); got != "hc-t3.core-1" {
			t.Errorf("Pane = %q", got)
		}
		if got := Key("hc-t3"); got != "hc-t3.core-1" {
			t.Errorf("Key = %q", got)
		}
	}
	for _, bad := range []string{"hc-", "hc-t0", "hc-t3.", "hc-t3..", "hc-t3/remote", "hc-t3 x", "hc-t4", "hc-t3.extra/part", ".."} {
		if got := Key(bad); got != "" {
			t.Errorf("Key(%q) = %q", bad, got)
		}
	}
	if got := Key("hc-t3.old-scope"); got != "hc-t3.old-scope" {
		t.Errorf("legacy = %q", got)
	}
}

func TestHcSpace(t *testing.T) {
	for where, want := range map[string]string{
		"dotfiles / 1":        "dotfiles",
		"  my space  / build": "my space",
		"solo":                "solo",
		"":                    "",
	} {
		if got := hcSpace(where); got != want {
			t.Errorf("hcSpace(%q) = %q, want %q", where, got, want)
		}
	}
}

func TestHcTab(t *testing.T) {
	for where, want := range map[string]string{
		"dotfiles / 1":           "",
		"dotfiles / checkout v2": "checkout v2",
		"solo":                   "",
	} {
		clearEnv(t)
		t.Setenv("HC_TERMINAL_ID", "t3")
		t.Setenv("HC_SCOPE", hcScope)
		fakeCLIs(t, map[string]string{"hc": `echo '{"labels":{"where":"` + where + `"}}'`})
		if got := Tab(); got != want {
			t.Errorf("where %q: Tab = %q, want %q", where, got, want)
		}
	}
}

func TestHcPanel(t *testing.T) {
	const fake = `case "$1 $2" in
"get cairn-t3") [ -f live ] && echo '{"state":"live"}' || { echo '{"error":{"kind":"not_found"}}'; exit 3; } ;;
"get t3") echo '{"labels":{"herdr.space":"dotfiles","herdr.tab":"2","where":"dotfiles / 2"}}' ;;
"kill cairn-t3") rm live; echo '{"error":{"kind":"not_found"}}'; exit 3 ;;
"rm cairn-t3") exit 3 ;;
esac`
	tests := []struct {
		name        string
		live, close bool
		want        []string
	}{
		{"opens", false, false, []string{
			"hc get cairn-t3 --json",
			"hc get t3 --json",
			"hc run --bg --rm --no-notify --name cairn-t3 --label herdr.space=dotfiles --label herdr.tab=2 -- /opt/my bin/cairn watch hc-t3." + hcScope,
		}},
		{"closes", true, false, []string{
			"hc get cairn-t3 --json",
			"hc kill cairn-t3 --json",
			"hc rm cairn-t3 --json",
			"hc get cairn-t3 --json",
		}},
		{"only closes", false, true, []string{"hc get cairn-t3 --json"}},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("HC_TERMINAL_ID", "t3")
		t.Setenv("HC_SCOPE", hcScope)
		dir, log := fakeCLIs(t, map[string]string{"hc": fake})
		if tt.live {
			os.WriteFile(filepath.Join(dir, "live"), nil, 0o644)
		}
		if err := Panel(cairn, "hc-t3."+hcScope, "", tt.close); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		wantLog(t, log(), tt.want...)
	}
}

func TestHcAddressAlphabet(t *testing.T) {
	clearEnv(t)
	fakeCLIs(t, map[string]string{"hc": `case "$1" in
 server) echo '{"core":{"id":"core-1"}}' ;;
 get) printf '{"address":"%s"}\n' "$2" ;;
 *) exit 1 ;;
 esac`})
	for _, addr := range []string{"t19", "t1b", "t1z", "t20", "t9z"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("HC_TERMINAL_ID", addr)
			want := "hc-" + addr + ".core-1"
			if got := Pane(); got != want {
				t.Errorf("Pane = %q, want %q", got, want)
			}
			if got := Key("hc-" + addr); got != want {
				t.Errorf("shorthand = %q, want %q", got, want)
			}
			if got := Key(want); got != want {
				t.Errorf("full key = %q", got)
			}
		})
	}
	for _, addr := range []string{"t1a", "t1e", "t1i", "t1l", "t1o", "t1u", "tb1", "t01", "remote/t1b", "remote//t9z", "t1b/extra"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("HC_TERMINAL_ID", addr)
			if got := Pane(); got != "" {
				t.Errorf("invalid Pane = %q", got)
			}
			if got := Key("hc-" + addr); got != "" {
				t.Errorf("invalid shorthand = %q", got)
			}
		})
	}
}
