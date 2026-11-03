package setup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// runSetup runs setup and returns what it printed and the checks it ended with.
func runSetup(dryRun bool) ([]string, []Check) {
	var b strings.Builder
	cs := Run(&b, dryRun, nowhere)
	return lines(b.String()), cs
}

// logged are the CLI runs the fakes logged since the last call, then clears the log.
func logged(t *testing.T, dir string) []string {
	t.Helper()
	file := filepath.Join(dir, "log")
	b, _ := os.ReadFile(file)
	os.Remove(file)
	var out []string
	for _, l := range lines(string(b)) {
		if l != "" {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

func statuses(cs []Check) map[string]string {
	m := map[string]string{}
	for _, c := range cs {
		m[c.Check] = c.Status
	}
	return m
}

// Listing each CLI's state once, whatever setup then runs.
var probes = []string{
	"claude plugin list --json",
	"claude plugin marketplace list --json",
	"codex plugin list --json",
	"codex plugin marketplace list --json",
	"gh auth status",
}

func TestSetupInstallsOnceAndIsIdempotent(t *testing.T) {
	dir := fakes(t, "claude", "codex", "gh")
	touch(t, dir, "gh-authed", "")
	self, _ := os.Executable()

	got, cs := runSetup(true)
	if want := []string{
		"ok path: " + self,
		"ok gh: installed and logged in",
		"skip claude-marketplace: dry run; would run `claude plugin marketplace add ken8203/cairn`",
		"skip claude-plugin: dry run; would run `claude plugin install cairn@cairn`",
		"skip codex-marketplace: dry run; would run `codex plugin marketplace add ken8203/cairn`",
		"skip codex-plugin: dry run; would run `codex plugin add cairn@cairn`",
		"manual codex-hooks: " + codexTrustFix,
	}; !slices.Equal(got, want) {
		t.Errorf("dry run:\n%s", strings.Join(got, "\n"))
	}
	if got := logged(t, dir); !slices.Equal(got, probes) {
		t.Errorf("dry run ran %q", got)
	}
	if s := statuses(cs); s["claude-plugin"] != Fail || s["codex-plugin"] != Fail {
		t.Errorf("dry run checks: %v", cs)
	}

	got, cs = runSetup(false)
	if want := []string{
		"ok path: " + self,
		"ok gh: installed and logged in",
		"done claude-marketplace: ran `claude plugin marketplace add ken8203/cairn`",
		"done claude-plugin: ran `claude plugin install cairn@cairn`",
		"done codex-marketplace: ran `codex plugin marketplace add ken8203/cairn`",
		"done codex-plugin: ran `codex plugin add cairn@cairn`",
		"manual codex-hooks: " + codexTrustFix,
	}; !slices.Equal(got, want) {
		t.Errorf("first run:\n%s", strings.Join(got, "\n"))
	}
	installs := []string{
		"claude plugin install cairn@cairn",
		"claude plugin marketplace add ken8203/cairn",
		"codex plugin add cairn@cairn",
		"codex plugin marketplace add ken8203/cairn",
	}
	if got := logged(t, dir); !slices.Equal(got, slices.Sorted(slices.Values(slices.Concat(probes, installs)))) {
		t.Errorf("first run ran %q", got)
	}
	if s := statuses(cs); s["claude-plugin"] != OK || s["codex-plugin"] != OK || s["codex-hooks"] != Warn || Failed(cs) {
		t.Errorf("first run checks: %v", cs)
	}

	touch(t, filepath.Join(dir, "codex"), "config.toml", trusted)
	got, cs = runSetup(false)
	if want := []string{
		"ok path: " + self,
		"ok gh: installed and logged in",
		"ok claude-marketplace: cairn is added",
		"ok claude-plugin: cairn@cairn installed and enabled",
		"ok codex-marketplace: cairn is added",
		"ok codex-plugin: cairn@cairn installed and enabled",
		"ok codex-hooks: cairn's hooks are trusted",
	}; !slices.Equal(got, want) {
		t.Errorf("second run:\n%s", strings.Join(got, "\n"))
	}
	if got := logged(t, dir); !slices.Equal(got, probes) {
		t.Errorf("second run ran %q", got)
	}
	if Failed(cs) || !slices.Equal(Doctor(nowhere), cs) {
		t.Errorf("checks %v, doctor %v", cs, Doctor(nowhere))
	}
}

func TestSetupDisabledPlugins(t *testing.T) {
	dir := fakes(t, "claude", "codex", "gh")
	for _, f := range []string{"claude-mkt", "codex-mkt"} {
		touch(t, dir, f, "")
	}
	touch(t, dir, "claude-plugin", "false")
	touch(t, dir, "codex-plugin", "false")
	got, cs := runSetup(false)
	for _, want := range []string{
		"done claude-plugin: ran `claude plugin enable cairn@cairn`",
		"manual codex-plugin: cairn@cairn is disabled; enable cairn in Codex's /plugins",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, strings.Join(got, "\n"))
		}
	}
	if s := statuses(cs); s["claude-plugin"] != OK || s["codex-plugin"] != Fail {
		t.Errorf("checks: %v", cs)
	}
}

func TestSetupWithoutAgentsOrGh(t *testing.T) {
	fakes(t)
	got, cs := runSetup(false)
	want := []string{
		"manual gh: gh is not on PATH; cairn reads PRs through it; install the GitHub CLI: https://cli.github.com",
		"skip claude: claude is not on PATH",
		"skip codex: codex is not on PATH",
	}
	if !slices.Equal(got[1:], want) {
		t.Errorf("setup:\n%s", strings.Join(got, "\n"))
	}
	if len(cs) != 4 || !Failed(cs) {
		t.Errorf("checks: %v", cs)
	}
}
