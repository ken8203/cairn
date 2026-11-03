package setup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// herdrSetup runs setup with cairn's home in home, returning what it printed and the
// herdr-plugin check it ended with.
func herdrSetup(home string, dryRun bool) (string, Check) {
	var b strings.Builder
	env := nowhere
	env.Home = home
	c := herdrCheck(Run(&b, dryRun, env))
	return b.String(), c
}

func herdrCheck(cs []Check) Check {
	i := slices.IndexFunc(cs, func(c Check) bool { return c.Check == "herdr-plugin" })
	if i < 0 {
		return Check{}
	}
	return cs[i]
}

func TestSetupHerdrPlugin(t *testing.T) {
	fake := fakes(t, "herdr")
	home := t.TempDir()
	dir := filepath.Join(home, "herdr-plugin")
	file := filepath.Join(dir, "herdr-plugin.toml")
	self, _ := os.Executable()
	const list = "herdr plugin list --plugin cairn --json"
	keys := "manual herdr-keys: to toggle the panel with prefix+t, add to ~/.config/herdr/config.toml:\n" +
		"  [[keys.command]]\n  key = \"prefix+t\"\n  type = \"plugin_action\"\n  command = \"cairn.panel\"\n  description = \"toggle cairn\"\n"
	notLinked := Check{Check: "herdr-plugin", Status: Warn, Detail: "not linked", Fix: "run `cairn setup`"}
	linked := Check{Check: "herdr-plugin", Status: OK, Detail: "linked"}

	out, c := herdrSetup(home, true)
	if want := "skip herdr-plugin: dry run; would write " + file + "\n" +
		"skip herdr-plugin: dry run; would run `herdr plugin link " + dir + "`\n" + keys; !strings.HasSuffix(out, want) {
		t.Errorf("dry run:\n%s", out)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("dry run wrote the plugin")
	}
	if got := logged(t, fake); !slices.Equal(got, []string{list}) || c != notLinked {
		t.Errorf("dry run ran %q, check %v", got, c)
	}

	out, c = herdrSetup(home, false)
	if want := "done herdr-plugin: wrote " + file + "\n" +
		"done herdr-plugin: ran `herdr plugin link " + dir + "`\n" + keys; !strings.HasSuffix(out, want) {
		t.Errorf("first run:\n%s", out)
	}
	if b, _ := os.ReadFile(file); string(b) != string(herdrManifestFor(self)) {
		t.Errorf("wrote\n%s", b)
	}
	if got := logged(t, fake); !slices.Equal(got, []string{"herdr plugin link " + dir, list}) || c != linked {
		t.Errorf("first run ran %q, check %v", got, c)
	}

	out, c = herdrSetup(home, false)
	if want := "ok herdr-plugin: linked from " + dir + "\n" + keys; !strings.HasSuffix(out, want) {
		t.Errorf("second run:\n%s", out)
	}
	if got := logged(t, fake); !slices.Equal(got, []string{list}) || c != linked {
		t.Errorf("second run ran %q, check %v", got, c)
	}

	// cairn moved since the plugin was written.
	os.WriteFile(file, herdrManifestFor("/old/bin/cairn"), 0o644)
	if c := herdrCheck(Doctor(nowhere)); c.Status != Warn || c.Detail != "linked to an older cairn path, /old/bin/cairn" {
		t.Errorf("doctor after a move: %v", c)
	}
	logged(t, fake)
	out, c = herdrSetup(home, false)
	if want := "done herdr-plugin: wrote " + file + "\n" +
		"done herdr-plugin: ran `herdr plugin link " + dir + "`\n"; !strings.Contains(out, want) {
		t.Errorf("run after a move:\n%s", out)
	}
	if b, _ := os.ReadFile(file); string(b) != string(herdrManifestFor(self)) || c != linked {
		t.Errorf("wrote\n%s\ncheck %v", b, c)
	}
}

func TestDoctorHerdr(t *testing.T) {
	fakes(t)
	if c := herdrCheck(Doctor(nowhere)); c != (Check{}) {
		t.Errorf("without herdr: %v", c)
	}
	fake := fakes(t, "herdr")
	home := t.TempDir()
	herdrSetup(home, false)
	touch(t, fake, "herdr-enabled", "false")
	want := Check{Check: "herdr-plugin", Status: Warn, Detail: "linked but disabled", Fix: "run `herdr plugin enable cairn`"}
	if c := herdrCheck(Doctor(nowhere)); c != want {
		t.Errorf("disabled: %v", c)
	}
	if out, _ := herdrSetup(home, false); !strings.Contains(out, "manual herdr-plugin: linked but disabled; run `herdr plugin enable cairn`\n") {
		t.Errorf("setup with it disabled:\n%s", out)
	}
}
