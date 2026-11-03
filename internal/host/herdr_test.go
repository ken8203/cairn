package host

import (
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
