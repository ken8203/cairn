package setup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fake CLIs keep their state as files in $FAKE and log each run to $FAKE/log; PATH
// holds only them, so they call other programs by absolute path.
const (
	fakeClaude = `#!/bin/sh
echo "claude $*" >> "$FAKE/log"
case "$*" in
"plugin marketplace list --json")
	if [ -e "$FAKE/claude-mkt" ]; then echo '[{"name":"other"},{"name":"cairn"}]'; else echo '[{"name":"other"}]'; fi ;;
"plugin list --json")
	if [ -e "$FAKE/claude-plugin" ]; then echo "[{\"id\":\"cairn@cairn\",\"enabled\":$(/bin/cat "$FAKE/claude-plugin")}]"; else echo '[]'; fi ;;
"plugin marketplace add ken8203/cairn") : > "$FAKE/claude-mkt" ;;
"plugin install cairn@cairn"|"plugin enable cairn@cairn") echo true > "$FAKE/claude-plugin" ;;
*) echo "unexpected: $*" >&2; exit 2 ;;
esac
`
	fakeCodex = `#!/bin/sh
echo "codex $*" >> "$FAKE/log"
case "$*" in
"plugin marketplace list --json")
	if [ -e "$FAKE/codex-mkt" ]; then echo '{"marketplaces":[{"name":"cairn","root":"/r"}]}'; else echo '{"marketplaces":[]}'; fi ;;
"plugin list --json")
	if [ -e "$FAKE/codex-plugin" ]; then echo "{\"installed\":[{\"pluginId\":\"cairn@cairn\",\"installed\":true,\"enabled\":$(/bin/cat "$FAKE/codex-plugin")}]}"; else echo '{"installed":[]}'; fi ;;
"plugin marketplace add ken8203/cairn") : > "$FAKE/codex-mkt" ;;
"plugin add cairn@cairn") echo true > "$FAKE/codex-plugin" ;;
*) echo "unexpected: $*" >&2; exit 2 ;;
esac
`
	fakeGh = `#!/bin/sh
echo "gh $*" >> "$FAKE/log"
[ -e "$FAKE/gh-authed" ]
`
	fakeHerdr = `#!/bin/sh
echo "herdr $*" >> "$FAKE/log"
case "$*" in
"plugin list --plugin cairn --json")
	if [ -e "$FAKE/herdr-root" ]; then
		root=$(/bin/cat "$FAKE/herdr-root")
		cairn=$(/usr/bin/awk -F'"' '/^command = \[/ && /"panel"\]$/ {print $2; exit}' "$root/herdr-plugin.toml")
		enabled=$(/bin/cat "$FAKE/herdr-enabled" 2>/dev/null || echo true)
		echo "{\"result\":{\"plugins\":[{\"plugin_id\":\"cairn\",\"enabled\":$enabled,\"plugin_root\":\"$root\",\"actions\":[{\"id\":\"panel\",\"command\":[\"$cairn\",\"panel\"]}]}]}}"
	else echo '{"result":{"plugins":[]}}'; fi ;;
"plugin link "*) shift 2; echo "$*" > "$FAKE/herdr-root" ;;
*) echo "unexpected: $*" >&2; exit 2 ;;
esac
`
	trusted = `[hooks.state."cairn@cairn:hooks/hooks.json:session_start:0:0"]
trusted_hash = "sha256:aa"

[hooks.state."cairn@cairn:hooks/hooks.json:post_tool_use:0:0"]
trusted_hash = "sha256:bb"
`
)

// fakes puts the named fake CLIs, and this test binary as cairn, alone on PATH, with an
// empty CODEX_HOME. It returns the dir the fakes keep their state in.
func fakes(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0o755)
	scripts := map[string]string{"claude": fakeClaude, "codex": fakeCodex, "gh": fakeGh, "herdr": fakeHerdr}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(scripts[name]), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "cairn")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("FAKE", dir)
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	os.Mkdir(filepath.Join(dir, "codex"), 0o755)
	return dir
}

func touch(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nowhere is an Env outside any host or agent session.
var nowhere = Env{
	Host:  func() string { return "" },
	Pane:  func() string { return "" },
	Agent: func() string { return "" },
	Trail: func(string) string { return "" },
	Orca:  func() string { return "not installed" },
}

func agentCheck(name string) Check {
	for _, a := range agents {
		if a.name == name {
			return probeAgent(a, false).check()
		}
	}
	panic(name)
}

func TestDoctorPlugins(t *testing.T) {
	dir := fakes(t, "claude", "codex")
	for _, name := range []string{"claude", "codex"} {
		if c := agentCheck(name); c.Status != Fail || c.Fix != "run `cairn setup`" {
			t.Errorf("%s missing: %v", name, c)
		}
	}
	touch(t, dir, "claude-plugin", "false")
	touch(t, dir, "codex-plugin", "false")
	if c := agentCheck("claude"); c.Status != Fail || c.Fix != "run `claude plugin enable cairn@cairn`" {
		t.Errorf("claude disabled: %v", c)
	}
	if c := agentCheck("codex"); c.Status != Fail || c.Fix != "enable cairn in Codex's /plugins" {
		t.Errorf("codex disabled: %v", c)
	}
	touch(t, dir, "claude-plugin", "true")
	touch(t, dir, "codex-plugin", "true")
	for _, name := range []string{"claude", "codex"} {
		if c := agentCheck(name); c.Status != OK {
			t.Errorf("%s: %v", name, c)
		}
	}
	os.Remove(filepath.Join(dir, "bin", "claude"))
	os.WriteFile(filepath.Join(dir, "bin", "codex"), []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755)
	if c := agentCheck("codex"); c.Status != Warn || c.Detail != "`codex plugin list --json` failed: exit status 3 boom" {
		t.Errorf("codex failing: %v", c)
	}
	var names []string
	for _, c := range Doctor(nowhere) {
		names = append(names, c.Check)
	}
	if want := []string{"path", "gh", "codex-plugin", "codex-hooks", "host", "session", "orca"}; !slices.Equal(names, want) {
		t.Errorf("doctor without claude: %q", names)
	}
}

func TestDoctorWhere(t *testing.T) {
	fakes(t)
	env := Env{
		Host:  func() string { return "Rex" },
		Pane:  func() string { return "p1" },
		Agent: func() string { return "s1" },
		Trail: func(id string) string { return "t-" + id },
		Orca:  func() string { return "2 worktrees on cairn trails" },
	}
	cs := Doctor(env)
	if got := cs[len(cs)-3:]; got[0].String() != "ok host: Rex, pane p1" || got[1].String() != "ok session: s1, on trail t-s1" || got[2].String() != "ok orca: 2 worktrees on cairn trails" {
		t.Errorf("got %v", got)
	}
	cs = Doctor(nowhere)
	if got := cs[len(cs)-3:]; got[0].String() != "ok host: no terminal host with panes" || got[1].String() != "ok session: not in an agent session" || got[2].String() != "ok orca: not installed" {
		t.Errorf("got %v", got)
	}
}

func TestGhNotLoggedIn(t *testing.T) {
	fakes(t, "gh")
	if c := ghCheck(); c.Status != "fail" || c.Fix != "run `gh auth login`" {
		t.Errorf("got %v", c)
	}
}

func TestPathCheck(t *testing.T) {
	dir := fakes(t)
	if c := pathCheck(); c.Status != "ok" {
		t.Errorf("on PATH: %v", c)
	}
	other := filepath.Join(dir, "other")
	os.Mkdir(other, 0o755)
	touch(t, other, "cairn", "")
	os.Chmod(filepath.Join(other, "cairn"), 0o755)
	t.Setenv("PATH", other+string(os.PathListSeparator)+os.Getenv("PATH"))
	if c := pathCheck(); c.Status != "fail" || !strings.HasPrefix(c.Detail, "cairn on PATH is "+filepath.Join(other, "cairn")) {
		t.Errorf("shadowed: %v", c)
	}
	t.Setenv("PATH", other[:len(other)-1])
	if c := pathCheck(); c.Status != "fail" || c.Detail != "cairn is not on PATH" || !strings.Contains(c.Fix, "export PATH=") {
		t.Errorf("missing: %v", c)
	}
}

func TestCodexHooksCheck(t *testing.T) {
	tests := []struct {
		name, config, status, detail string
	}{
		{"no config", "", "warn", "cairn@cairn:hooks/hooks.json:session_start:0:0 not trusted, cairn@cairn:hooks/hooks.json:post_tool_use:0:0 not trusted"},
		{"trusted", trusted, "ok", "cairn's hooks are trusted"},
		{"one trusted", `[hooks.state."cairn@cairn:hooks/hooks.json:session_start:0:0"]
trusted_hash = "sha256:aa"
[hooks.state."other@x:hooks/hooks.json:post_tool_use:0:0"]
trusted_hash = "sha256:bb"
`, "warn", "cairn@cairn:hooks/hooks.json:post_tool_use:0:0 not trusted"},
		{"disabled", trusted + "enabled = false\n", "warn", "cairn@cairn:hooks/hooks.json:post_tool_use:0:0 disabled"},
		{"enabled", trusted + "enabled = true\n", "ok", "cairn's hooks are trusted"},
		{"hooks off", "[features]\nhooks = false\n" + trusted, "warn", "Codex hooks are turned off ([features] hooks = false)"},
		{"other tables", "model = \"m\"\n[mcp_servers.x]\ncommand = \"node\"\n" + trusted, "ok", "cairn's hooks are trusted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fakes(t)
			if tt.config != "" {
				touch(t, filepath.Join(dir, "codex"), "config.toml", tt.config)
			}
			if c := codexHooksCheck(); c.Status != tt.status || c.Detail != tt.detail {
				t.Errorf("got %v", c)
			}
		})
	}
	dir := fakes(t)
	touch(t, filepath.Join(dir, "codex"), "config.toml", "not = [toml")
	if c := codexHooksCheck(); c.Status != "warn" || !strings.HasPrefix(c.Detail, "can't read ") {
		t.Errorf("bad toml: %v", c)
	}
}
