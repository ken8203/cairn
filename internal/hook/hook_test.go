package hook

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	claudePayload = `{"session_id":"a61fbdc6-1","cwd":"/abs","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo https://github.com/ken8203/cairn/pull/999","description":"…"},"tool_response":{"stdout":"https://github.com/ken8203/cairn/pull/999","stderr":"","interrupted":false,"isImage":false,"noOutputExpected":false},"tool_use_id":"toolu_1","transcript_path":"/t","permission_mode":"auto"}`
	codexPayload  = `{"session_id":"01a19fee-0b76","turn_id":"t1","cwd":"/abs","hook_event_name":"PostToolUse","model":"m","tool_name":"Bash","tool_input":{"command":"echo https://github.com/ken8203/cairn/pull/998"},"tool_response":"https://github.com/ken8203/cairn/pull/998\n","tool_use_id":"exec-1","transcript_path":"/t"}`
	startPayload  = `{"session_id":"s1","cwd":"/abs","hook_event_name":"SessionStart","source":"startup"}`
)

func TestParse(t *testing.T) {
	tests := []struct {
		payload, session, event, command, output string
	}{
		{claudePayload, "a61fbdc6-1", "PostToolUse", "echo https://github.com/ken8203/cairn/pull/999", "https://github.com/ken8203/cairn/pull/999\n"},
		{codexPayload, "01a19fee-0b76", "PostToolUse", "echo https://github.com/ken8203/cairn/pull/998", "https://github.com/ken8203/cairn/pull/998\n"},
		{startPayload, "s1", "SessionStart", "", "\n"},
	}
	for _, tt := range tests {
		p, err := Parse(strings.NewReader(tt.payload))
		if err != nil {
			t.Fatal(err)
		}
		if p.SessionID != tt.session || p.Event != tt.event || p.Cwd != "/abs" || p.ToolInput.Command != tt.command || p.Output() != tt.output {
			t.Errorf("%s: got %+v, output %q", tt.session, p, p.Output())
		}
	}
	if _, err := Parse(strings.NewReader("not json")); err == nil {
		t.Error("parsed garbage")
	}
}

func TestRecords(t *testing.T) {
	const created = "Creating pull request\nhttps://github.com/o/r/pull/4\nhttps://github.com/o/r/pull/5\n"
	home, _ := os.UserHomeDir()
	tests := []struct {
		command, output string
		want            []string
	}{
		{"gh pr create --fill", created, []string{"--own https://github.com/o/r/pull/5"}},
		{"cd x && gh pr create --title 'a b' --body \"$(cat f)\"", created, []string{"--own https://github.com/o/r/pull/5"}},
		{"gh pr create --fill", "aborted", nil},
		{"git push -u origin HEAD", "", []string{"--own -C /abs"}},
		{"cd sub && git push", "", []string{"--own -C /abs/sub"}},
		{"cd /repo; git push", "", []string{"--own -C /repo"}},
		{"git -C y push", "", []string{"--own -C /abs/y"}},
		{"cd a && git -C ../b -C c push", "", []string{"--own -C /abs/b/c"}},
		{"cd ~/w && git push", "", []string{"--own -C " + home + "/w"}},
		{"/usr/bin/git -c a=b --no-pager push", "", []string{"--own -C /abs"}},
		{"(cd sub && git push) && git push", "", []string{"--own -C /abs/sub", "--own -C /abs"}},
		{"cd \"$REPO\" && git push", "", nil},
		{"cd $REPO && git -C /repo push", "", []string{"--own -C /repo"}},
		{"git push 2>&1 | tail -3", "", []string{"--own -C /abs"}},
		{"cd sub | cat; git push", "", []string{"--own -C /abs"}},
		{"git push && git push", "", []string{"--own -C /abs"}},
		{"for r in a b; do git push; done", "", []string{"--own -C /abs"}},
		{"if cd sub; then git push; fi", "", nil},
		{"if cd sub; then :; fi; git push", "", nil},
		{"{ cd sub; git push; }", "", nil},
		{"f() { cd sub; }; f; git push", "", nil},
		{"while true; do cd sub; done; gh pr merge 3", "", nil},
		{"if true; then git push; fi; git -C /repo push", "", []string{"--own -C /abs", "--own -C /repo"}},
		{"pushd sub && git push", "", nil},
		{"pushd sub; popd; git push", "", nil},
		{"cd -P sub && git push", "", []string{"--own -C /abs/sub"}},
		{"cd -L -- sub && git push", "", []string{"--own -C /abs/sub"}},
		{"cd -- -x && git push", "", []string{"--own -C /abs/-x"}},
		{"cd -x && git push", "", nil},
		{"cd - && git push", "", nil},
		{"cd && git push", "", []string{"--own -C " + home}},
		{"git log push", "", nil},
		{"git -c push status", "", nil},
		{"gh pr merge -R o/r '#12' --squash", "", []string{"-C /abs -R o/r 12"}},
		{"gh pr merge #12", "", []string{"-C /abs"}},
		{"cd sub && gh pr checkout", "", []string{"-C /abs/sub"}},
		{"gh pr comment 3 --body 'see #4' && gh pr view 3", "", []string{"-C /abs 3"}},
		{"gh pr edit --repo=o/r https://github.com/o/r/pull/9", "", []string{"-C /abs -R o/r https://github.com/o/r/pull/9"}},
		{"gh pr edit --milestone 3 12", "", []string{"-C /abs 12"}},
		{"gh pr comment -b 99", "", []string{"-C /abs"}},
		{"gh pr comment --body=7 8", "", []string{"-C /abs 8"}},
		{"gh pr merge --squash --delete-branch 12", "", []string{"-C /abs 12"}},
		{"gh pr review -r -b 'fix 2' 5", "", []string{"-C /abs 5"}},
		{"GH_REPO=o/r gh pr merge 3", "", []string{"-C /abs -R o/r 3"}},
		{"GH_REPO=o/r gh pr merge -R x/y 3", "", []string{"-C /abs -R x/y 3"}},
		{"gh pr view 3; gh issue create; gh api repos/o/r/pulls", "", nil},
		{"echo 'git push'; ls", "", nil},
		{"if true; then", "", nil},
	}
	for _, tt := range tests {
		p := Payload{Cwd: "/abs", ToolName: "Bash"}
		p.ToolInput.Command = tt.command
		p.ToolResponse = []byte(`"` + strings.ReplaceAll(tt.output, "\n", `\n`) + `"`)
		var got []string
		for _, r := range Records(p) {
			got = append(got, strings.Join(r, " "))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q: got %q, want %q", tt.command, got, tt.want)
		}
	}
}

func TestRecordsNeedsBashAndCwd(t *testing.T) {
	p := Payload{Cwd: "/abs", ToolName: "Edit"}
	p.ToolInput.Command = "git push"
	if got := Records(p); got != nil {
		t.Errorf("non-Bash tool: %q", got)
	}
	p.ToolName, p.Cwd = "Bash", "rel"
	if got := Records(p); got != nil {
		t.Errorf("relative cwd: %q", got)
	}
}

func TestSessionStartOutput(t *testing.T) {
	want := `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"on \"t\""}}` + "\n"
	if got := string(SessionStartOutput(`on "t"`)); got != want {
		t.Errorf("got %s", got)
	}
}
