package host

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestTmuxPanel(t *testing.T) {
	clearEnv(t)
	t.Setenv("TMUX", tmuxEnv)
	t.Setenv("TMUX_PANE", "%3")
	_, log := fakeCLIs(t, map[string]string{"tmux": ""})
	if err := Panel(cairn, "", "", false); err != nil {
		t.Fatal(err)
	}
	wantLog(t, log(), `tmux run-shell -b -t %3 'tmux' 'display-popup' '-E' '-w' '80%' '-h' '50%' '-t' '%3' ''\''/opt/my bin/cairn'\'' '\''watch'\'' '\''tmux-4242.3'\'''`)
	// The popup is modal, so there is nothing to close.
	_, log = fakeCLIs(t, map[string]string{"tmux": ""})
	if err := Panel(cairn, "", "", true); err != nil {
		t.Fatal(err)
	}
	wantLog(t, log(), "")
}

func TestHerdrPanel(t *testing.T) {
	stubTerminal(t, "term_9")
	const other = `{"pane_id":"w1:p1","tab_id":"w1:t1","label":null},{"pane_id":"w1:p7","tab_id":"w1:t2","label":"cairn"}`
	tests := []struct {
		name, panes string
		close       bool
		want        []string
	}{
		{"opens", other, false, []string{
			"herdr --session work pane list",
			"herdr --session work pane split w1:p1 --direction right --ratio 0.35 --no-focus",
			"herdr --session work pane rename w1:p9 cairn",
			`herdr --session work pane run w1:p9 '/opt/my bin/cairn' 'watch' 'herdr-work.w1-p1.term_9'`,
		}},
		{"closes", other + `,{"pane_id":"w1:p5","tab_id":"w1:t1","label":"cairn"}`, false, []string{
			"herdr --session work pane list",
			"herdr --session work pane close w1:p5",
		}},
		{"only closes", other, true, []string{"herdr --session work pane list"}},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("HERDR_SESSION", "work")
		t.Setenv("HERDR_PANE_ID", "w1:p1")
		_, log := fakeCLIs(t, map[string]string{"herdr": `case "$4" in
list) echo '{"result":{"panes":[` + tt.panes + `]}}' ;;
split) echo '{"result":{"pane":{"pane_id":"w1:p9"}}}' ;;
esac`})
		if err := Panel(cairn, "", "", tt.close); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		wantLog(t, log(), tt.want...)
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

func TestOrcaPanel(t *testing.T) {
	const terminals = `{"handle":"term_1","tabId":"tab1","title":"claude","worktreeId":"w1"},{"handle":"term_7","tabId":"tab2","title":"Setup","paneTitle":"cairn","worktreeId":"w1"}`
	tests := []struct {
		name, terminals string
		close           bool
		want            []string
	}{
		{"opens", terminals, false, []string{
			"orca terminal list --json",
			`orca terminal split --terminal term_1 --direction horizontal --command '/opt/my bin/cairn' 'watch' 'orca-term_1' --json`,
			"orca terminal set-pane-title --terminal term_9 --title cairn",
		}},
		{"closes", terminals + `,{"handle":"term_5","tabId":"tab1","title":"Setup","paneTitle":"cairn"}`, false, []string{
			"orca terminal list --json",
			"orca terminal close --terminal term_5",
		}},
		{"only closes", terminals, true, []string{"orca terminal list --json"}},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("ORCA_TERMINAL_HANDLE", "term_1")
		_, log := fakeCLIs(t, map[string]string{"orca": `case "$2" in
list) echo '{"result":{"terminals":[` + tt.terminals + `]}}' ;;
split) echo '{"result":{"split":{"handle":"term_9","tabId":"tab1","paneRuntimeId":1,"leafId":"l9"}}}' ;;
esac`})
		if err := Panel(cairn, "", "", tt.close); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		wantLog(t, log(), tt.want...)
	}
}

// A CLI's failure is one line naming it.
func TestPanelError(t *testing.T) {
	clearEnv(t)
	t.Setenv("ORCA_TERMINAL_HANDLE", "term_1")
	fakeCLIs(t, map[string]string{"orca": "echo 'not running\nstart orca' >&2; exit 1"})
	err := Panel(cairn, "", "", false)
	if err == nil || err.Error() != "orca terminal: not running" {
		t.Errorf("Panel = %v", err)
	}
}

// A key names the pane outside its host's env, as a host's own keybinding passes it.
func TestPanelForKey(t *testing.T) {
	clearEnv(t)
	tests := []struct{ key, want string }{
		{"b1", "rex do - -b block:b1"},
		{"tmux-4242.3", `tmux run-shell -b -t %3 'tmux' 'display-popup' '-E' '-w' '80%' '-h' '50%' '-t' '%3' ''\''/opt/my bin/cairn'\'' '\''watch'\'' '\''tmux-4242.3'\'''`},
		{"herdr-v1.2.w1-p1.term_9", "herdr --session v1.2 pane list"},
		{"hc-t3." + hcScope, "hc get cairn-t3 --json"},
		{"orca-term_1", "orca terminal list --json"},
	}
	for _, tt := range tests {
		_, log := fakeCLIs(t, map[string]string{"rex": "", "tmux": "", "herdr": "", "hc": "", "orca": ""})
		Panel(cairn, tt.key, "", false)
		if got := log()[0]; got != tt.want {
			t.Errorf("Panel(%q) ran %q", tt.key, got)
		}
	}
	for _, key := range []string{"tmux-4242", "herdr-w1-p1"} {
		if err := Panel(cairn, key, "", false); err == nil || !strings.Contains(err.Error(), "no pane in") {
			t.Errorf("Panel(%q) = %v", key, err)
		}
	}
}
