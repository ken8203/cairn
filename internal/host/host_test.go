package host

import (
	"cmp"
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
	t.Setenv("ORCA_WORKTREE_ID", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HC_SCOPE", "")
}

// stubTerminal makes herdr report id as every pane's terminal id.
func stubTerminal(t *testing.T, id string) {
	t.Helper()
	old := herdrTerminal
	herdrTerminal = func(string, string) string { return id }
	t.Cleanup(func() { herdrTerminal = old })
}

const (
	tmuxEnv = "/tmp/tmux-501/default,4242,0"
	hcScope = "17396136072524-68dbd673"
)

func TestPane(t *testing.T) {
	stubTerminal(t, "term_9")
	tests := []struct {
		env  map[string]string
		want string
	}{
		{nil, ""},
		{map[string]string{"REX_BLOCK": "block:b1"}, "b1"},
		{map[string]string{"TMUX_PANE": "%3"}, "tmux-4242.3"},
		{map[string]string{"HERDR_PANE_ID": "w1:p2"}, "herdr-default.w1-p2.term_9"},
		{map[string]string{"HERDR_PANE_ID": "w1:p2", "HERDR_SESSION": "work"}, "herdr-work.w1-p2.term_9"},
		{map[string]string{"ORCA_TERMINAL_HANDLE": "term_0b1c-9f"}, "orca-term_0b1c-9f"},
		{map[string]string{"HC_TERMINAL_ID": "t3"}, "hc-t3.17396136072524-68dbd673"},
		// Innermost first: herdr, then tmux, then Herdr.app, then Orca, then Rex.
		{map[string]string{"REX_BLOCK": "block:b1", "ORCA_TERMINAL_HANDLE": "term_1"}, "orca-term_1"},
		{map[string]string{"REX_BLOCK": "block:b1", "ORCA_TERMINAL_HANDLE": "term_1", "HC_TERMINAL_ID": "t3"}, "hc-t3.17396136072524-68dbd673"},
		{map[string]string{"ORCA_TERMINAL_HANDLE": "term_1", "HC_TERMINAL_ID": "t3", "TMUX_PANE": "%3"}, "tmux-4242.3"},
		{map[string]string{"HC_TERMINAL_ID": "t3", "HERDR_PANE_ID": "w1:p2"}, "herdr-default.w1-p2.term_9"},
		{map[string]string{"REX_BLOCK": "block:b1", "TMUX_PANE": "%3", "HERDR_PANE_ID": "w1:p2"}, "herdr-default.w1-p2.term_9"},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("TMUX", tmuxEnv)
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
		"tmux-4242.3":                   "tmux-4242.3",
		"herdr-default.w1-p2.term_9":    "herdr-default.w1-p2.term_9",
		"hc-t3.17396136072524-68dbd673": "hc-t3.17396136072524-68dbd673",
		"orca-term_0b1c-9f":             "orca-term_0b1c-9f",
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

// A herdr key carries its session, or default without one, back to the lookups.
func TestHerdrKeyRoundTrip(t *testing.T) {
	stubTerminal(t, "term_65f5")
	tests := []struct{ session, pane, key, wantSession string }{
		{"", "w1:p2", "herdr-default.w1-p2.term_65f5", "default"},
		{"work", "w12:p3", "herdr-work.w12-p3.term_65f5", "work"},
		{"v1.2", "w1:p1", "herdr-v1.2.w1-p1.term_65f5", "v1.2"},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("HERDR_SESSION", tt.session)
		t.Setenv("HERDR_PANE_ID", tt.pane)
		key := Pane()
		if key != tt.key {
			t.Errorf("%+v: Pane = %q", tt, key)
		}
		if session, pane := herdrPane(strings.TrimPrefix(key, "herdr-")); session != tt.wantSession || pane != tt.pane {
			t.Errorf("%+v: herdrPane = %q, %q", tt, session, pane)
		}
	}
	for _, key := range []string{"w1-p1", "default.w1-p1"} {
		if session, pane := herdrPane(key); session != "" || pane != "" {
			t.Errorf("herdrPane(%q) = %q, %q", key, session, pane)
		}
	}
}

// Without a terminal id the pane could be one herdr brought back, so there is none.
func TestHerdrNoTerminal(t *testing.T) {
	stubTerminal(t, "")
	clearEnv(t)
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	if got := Pane(); got != "" || Name() == "" {
		t.Errorf("Pane = %q, Name = %q", got, Name())
	}
}

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

func TestOrcaLabel(t *testing.T) {
	clearEnv(t)
	t.Setenv("ORCA_TERMINAL_HANDLE", "term_1")
	t.Setenv("ORCA_WORKTREE_ID", "0b1c::/Users/me/code/cairn.feat-x")
	if got := Label(); got != "cairn.feat-x" {
		t.Errorf("Label = %q", got)
	}
	for _, id := range []string{"", "0b1c", "0b1c::"} {
		if got := orcaLabel(id); got != "" {
			t.Errorf("orcaLabel(%q) = %q", id, got)
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
		{"tmux-4242.3", "tmux", "tmux 4242.3"},
		{"herdr-default.w1-p2.term_9", "herdr", "herdr default.w1-p2.term_9"},
		{"orca-term_0b1c-9f", "orca", "orca term_0b1c-9f"},
		{"hc-t3.17396136072524-68dbd673", "hc", "hc t3.17396136072524-68dbd673"},
		{"tmuxish", "rex", "rex tmuxish"},
	}
	for _, tt := range tests {
		asked = nil
		if got := title(hs, tt.key); got != tt.want || strings.Join(asked, ",") != tt.asked {
			t.Errorf("title(%q) = %q, asked %q", tt.key, got, asked)
		}
	}
}

// Titles look tmux and hc panes up by what their key bodies name.
func TestBodies(t *testing.T) {
	for body, want := range map[string]string{"4242.3": "%3", "3": "", "4242.": ""} {
		if got := tmuxPane(body); got != want {
			t.Errorf("tmuxPane(%q) = %q, want %q", body, got, want)
		}
	}
	for body, want := range map[string]string{"t3.1739-68db": "t3", "t3": "t3"} {
		if got := hcAddr(body); got != want {
			t.Errorf("hcAddr(%q) = %q, want %q", body, got, want)
		}
	}
}
