package host

import "testing"

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
