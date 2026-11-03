package host

import (
	"os"
	"path/filepath"
	"testing"
)

// Without a scope an hc address could be one handed out again, so there is no pane.
func TestHcNoScope(t *testing.T) {
	clearEnv(t)
	t.Setenv("HC_TERMINAL_ID", "t3")
	if got := Pane(); got != "" || Name() == "" {
		t.Errorf("Pane = %q, Name = %q", got, Name())
	}
	t.Setenv("HC_SCOPE", "17396/x y")
	if got := Pane(); got != "hc-t3.17396-x-y" {
		t.Errorf("Pane = %q", got)
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
		if err := Panel(cairn, "", "", tt.close); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		wantLog(t, log(), tt.want...)
	}
}
