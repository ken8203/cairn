package host

import "testing"

const (
	tmuxEnv = "/tmp/tmux-501/default,4242,0"
	hcScope = "17396136072524-68dbd673"
)

func TestTmuxServer(t *testing.T) {
	for env, want := range map[string]string{
		tmuxEnv:                   "4242",
		"/tmp/a,b/default,77,1":   "77",
		"":                        "",
		"garbage":                 "",
		"/tmp/tmux-501/default,1": "",
		"/tmp/s,pid,0":            "",
		"/tmp/s,,0":               "",
	} {
		if got := tmuxServer(env); got != want {
			t.Errorf("tmuxServer(%q) = %q, want %q", env, got, want)
		}
	}
	clearEnv(t)
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("TMUX", "garbage")
	if got := Pane(); got != "" {
		t.Errorf("Pane with a garbage $TMUX = %q", got)
	}
}

func TestTmuxTab(t *testing.T) {
	for out, want := range map[string]string{
		"1 zsh":         "",
		"0 checkout v2": "checkout v2",
		"":              "",
	} {
		clearEnv(t)
		t.Setenv("TMUX", tmuxEnv)
		t.Setenv("TMUX_PANE", "%3")
		_, log := fakeCLIs(t, map[string]string{"tmux": "echo '" + out + "'"})
		if got := Tab(); got != want {
			t.Errorf("%q: Tab = %q, want %q", out, got, want)
		}
		wantLog(t, log(), "tmux display -p -t %3 #{automatic-rename} #W")
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
