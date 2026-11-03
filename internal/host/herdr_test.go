package host

import (
	"path/filepath"
	"strings"
	"testing"
)

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

func TestHerdrPanel(t *testing.T) {
	stubTerminal(t, "term_9")
	const other = `{"pane_id":"w1:p1","tab_id":"w1:t1","label":null},{"pane_id":"w1:p7","tab_id":"w1:t2","label":"cairn"}`
	const linked = `{"plugin_id":"cairn","enabled":true}`
	tests := []struct {
		name, panes, plugins string
		close                bool
		want                 []string
	}{
		{"splits without the plugin", other, "", false, []string{
			"herdr --session work pane list",
			"herdr --session work plugin list --plugin cairn --json",
			"herdr --session work pane split w1:p1 --direction right --ratio 0.35 --no-focus",
			"herdr --session work pane rename w1:p9 cairn",
			`herdr --session work pane run w1:p9 '/opt/my bin/cairn' 'watch' 'herdr-work.w1-p1.term_9' '--dir' '/home/me/api'`,
		}},
		{"splits with the plugin disabled", other, `{"plugin_id":"cairn","enabled":false}`, false, []string{
			"herdr --session work pane list",
			"herdr --session work plugin list --plugin cairn --json",
			"herdr --session work pane split w1:p1 --direction right --ratio 0.35 --no-focus",
			"herdr --session work pane rename w1:p9 cairn",
			`herdr --session work pane run w1:p9 '/opt/my bin/cairn' 'watch' 'herdr-work.w1-p1.term_9' '--dir' '/home/me/api'`,
		}},
		{"opens the overlay", other, linked, false, []string{
			"herdr --session work pane list",
			"herdr --session work plugin list --plugin cairn --json",
			"herdr --session work plugin pane open --plugin cairn --entrypoint panel --placement overlay --env CAIRN_PANE=herdr-work.w1-p1.term_9 --env CAIRN_DIR=/home/me/api",
		}},
		{"closes", other + `,{"pane_id":"w1:p5","tab_id":"w1:t1","label":"cairn"}`, linked, false, []string{
			"herdr --session work pane list",
			"herdr --session work pane close w1:p5",
		}},
		{"only closes", other, linked, true, []string{"herdr --session work pane list"}},
	}
	for _, tt := range tests {
		clearEnv(t)
		t.Setenv("HERDR_SESSION", "work")
		t.Setenv("HERDR_PANE_ID", "w1:p1")
		_, log := fakeCLIs(t, map[string]string{"herdr": fakeHerdr(tt.panes, tt.plugins)})
		if err := Panel(cairn, "", "/home/me/api", tt.close); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		wantLog(t, log(), tt.want...)
	}
}

// Run as the plugin's action, the panel takes the focused pane's folder over the action's
// cwd, and the herdr that started it.
func TestHerdrPanelAction(t *testing.T) {
	stubTerminal(t, "term_9")
	clearEnv(t)
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"w1","tab_id":"w1:t1","focused_pane_id":"w1:p1","focused_pane_cwd":"/home/me/api"}`)
	dir, log := fakeCLIs(t, map[string]string{"herdr": fakeHerdr(`{"pane_id":"w1:p1","tab_id":"w1:t1"}`, `{"plugin_id":"cairn","enabled":true}`)})
	t.Setenv("HERDR_BIN_PATH", filepath.Join(dir, "herdr"))
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := Panel(cairn, "", "/home/me/.cairn/herdr-plugin", false); err != nil {
		t.Fatal(err)
	}
	wantLog(t, log(),
		"herdr --session default pane list",
		"herdr --session default plugin list --plugin cairn --json",
		"herdr --session default plugin pane open --plugin cairn --entrypoint panel --placement overlay --env CAIRN_PANE=herdr-default.w1-p1.term_9 --env CAIRN_DIR=/home/me/api",
	)
}

// fakeHerdr answers pane list and plugin list with the panes and plugins given.
func fakeHerdr(panes, plugins string) string {
	return `case "$3 $4" in
"pane list") echo '{"result":{"panes":[` + panes + `]}}' ;;
"plugin list") echo '{"result":{"plugins":[` + plugins + `]}}' ;;
"pane split") echo '{"result":{"pane":{"pane_id":"w1:p9"}}}' ;;
esac`
}
