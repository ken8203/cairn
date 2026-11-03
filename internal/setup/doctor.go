// Package setup installs cairn's plugin into the agents found on PATH, through their own
// plugin CLIs, and checks the install without changing it.
package setup

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// Marketplace is the repo both agents add as a plugin marketplace.
	Marketplace = "ken8203/cairn"
	// Plugin is cairn's plugin in its marketplace, as both agents name it.
	Plugin          = "cairn@cairn"
	marketplaceName = "cairn"
)

// A check's status.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
)

// Installs clone from GitHub, so a CLI gets this long before it counts as hung.
const cliTimeout = 2 * time.Minute

// Check is one of doctor's findings, with the fix for anything not ok.
type Check struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func (c Check) String() string {
	s := fmt.Sprintf("%s %s: %s", c.Status, c.Check, c.Detail)
	if c.Fix != "" {
		s += "; fix: " + c.Fix
	}
	return s
}

// Env looks up where cairn runs, which doctor reports alongside its checks.
type Env struct {
	Host, Pane, Agent func() string
	Trail             func(session string) string
	// Orca says how many Orca worktrees are on trails, or that Orca isn't installed.
	Orca func() string
}

// plugin is an installed plugin as an agent's plugin list shows it.
type plugin struct {
	ID      string
	Enabled bool
}

// agent is a coding agent cairn's plugin installs into, driven through its CLI.
type agent struct {
	name                    string
	listMarketplaces        []string
	marketplaces            func(out []byte) ([]string, error)
	listPlugins             []string
	plugins                 func(out []byte) ([]plugin, error)
	addMarketplace, install []string
	// enable turns a disabled plugin on, when the CLI can.
	enable    []string
	enableFix string
	// hooks checks what the agent needs besides the plugin before it runs cairn's hooks.
	hooks func() Check
}

var agents = []agent{
	{
		name:             "claude",
		listMarketplaces: []string{"claude", "plugin", "marketplace", "list", "--json"},
		marketplaces: func(out []byte) ([]string, error) {
			var list []struct {
				Name string `json:"name"`
			}
			err := json.Unmarshal(out, &list)
			var names []string
			for _, m := range list {
				names = append(names, m.Name)
			}
			return names, err
		},
		listPlugins: []string{"claude", "plugin", "list", "--json"},
		plugins: func(out []byte) ([]plugin, error) {
			var list []struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			}
			err := json.Unmarshal(out, &list)
			var ps []plugin
			for _, p := range list {
				ps = append(ps, plugin(p))
			}
			return ps, err
		},
		addMarketplace: []string{"claude", "plugin", "marketplace", "add", Marketplace},
		install:        []string{"claude", "plugin", "install", Plugin},
		enable:         []string{"claude", "plugin", "enable", Plugin},
		enableFix:      "run `claude plugin enable " + Plugin + "`",
	},
	{
		name:             "codex",
		listMarketplaces: []string{"codex", "plugin", "marketplace", "list", "--json"},
		marketplaces: func(out []byte) ([]string, error) {
			var list struct {
				Marketplaces []struct {
					Name string `json:"name"`
				} `json:"marketplaces"`
			}
			err := json.Unmarshal(out, &list)
			var names []string
			for _, m := range list.Marketplaces {
				names = append(names, m.Name)
			}
			return names, err
		},
		listPlugins: []string{"codex", "plugin", "list", "--json"},
		plugins: func(out []byte) ([]plugin, error) {
			var list struct {
				Installed []struct {
					ID      string `json:"pluginId"`
					Enabled bool   `json:"enabled"`
				} `json:"installed"`
			}
			err := json.Unmarshal(out, &list)
			var ps []plugin
			for _, p := range list.Installed {
				ps = append(ps, plugin(p))
			}
			return ps, err
		},
		addMarketplace: []string{"codex", "plugin", "marketplace", "add", Marketplace},
		install:        []string{"codex", "plugin", "add", Plugin},
		enableFix:      "enable cairn in Codex's /plugins",
		hooks:          codexHooksCheck,
	},
}

// agentState is what an agent's CLI says about cairn's plugin.
type agentState struct {
	agent
	present                         bool
	err                             error
	marketplace, installed, enabled bool
	// trust is what hooks found.
	trust Check
}

// probeAgent asks the agent's CLI about the plugin, and its marketplace when asked to.
func probeAgent(a agent, withMarketplace bool) agentState {
	s := agentState{agent: a, present: onPath(a.name)}
	if !s.present {
		return s
	}
	if a.hooks != nil {
		s.trust = a.hooks()
	}
	if withMarketplace {
		names, err := list(a.listMarketplaces, a.marketplaces)
		if err != nil {
			s.err = err
			return s
		}
		s.marketplace = slices.Contains(names, marketplaceName)
	}
	ps, err := list(a.listPlugins, a.plugins)
	s.err = err
	for _, p := range ps {
		if p.ID == Plugin {
			s.installed, s.enabled = true, s.enabled || p.Enabled
		}
	}
	return s
}

func list[T any](argv []string, parse func([]byte) ([]T, error)) ([]T, error) {
	out, err := output(argv...)
	if err != nil {
		return nil, err
	}
	items, err := parse(out)
	if err != nil {
		return nil, fmt.Errorf("`%s`: %w", strings.Join(argv, " "), err)
	}
	return items, nil
}

func (s agentState) check() Check {
	c := Check{Check: s.name + "-plugin"}
	switch {
	case s.err != nil:
		c.Status, c.Detail = Warn, s.err.Error()
	case !s.installed:
		c.Status, c.Detail, c.Fix = Fail, Plugin+" is not installed in "+s.name, "run `cairn setup`"
	case !s.enabled:
		c.Status, c.Detail, c.Fix = Fail, Plugin+" is disabled in "+s.name, s.enableFix
	default:
		c.Status, c.Detail = OK, Plugin+" installed and enabled"
	}
	return c
}

// probe is everything doctor checks, each CLI asked once.
type probe struct {
	path, gh Check
	agents   []agentState
}

// probeAll runs the checks, the agents' and gh's at the same time.
func probeAll(withMarketplace bool) probe {
	p := probe{path: pathCheck(), agents: make([]agentState, len(agents))}
	var wg sync.WaitGroup
	wg.Go(func() { p.gh = ghCheck() })
	for i, a := range agents {
		wg.Go(func() { p.agents[i] = probeAgent(a, withMarketplace) })
	}
	wg.Wait()
	return p
}

func (p probe) checks(env Env) []Check {
	cs := []Check{p.path, p.gh}
	for _, a := range p.agents {
		if !a.present {
			continue
		}
		cs = append(cs, a.check())
		if a.hooks != nil {
			cs = append(cs, a.trust)
		}
	}
	h := Check{Check: "host", Status: OK, Detail: "no terminal host with panes"}
	if name := env.Host(); name != "" {
		h.Detail = name + ", pane " + cmp.Or(env.Pane(), "unknown")
	}
	s := Check{Check: "session", Status: OK, Detail: "not in an agent session"}
	if id := env.Agent(); id != "" {
		s.Detail = id + ", no trail yet"
		if name := env.Trail(id); name != "" {
			s.Detail = id + ", on trail " + name
		}
	}
	return append(cs, h, s, Check{Check: "orca", Status: OK, Detail: env.Orca()})
}

// Doctor checks cairn's install, changing nothing: this binary on PATH, gh, the plugin in
// each agent on PATH, and where cairn runs.
func Doctor(env Env) []Check { return probeAll(false).checks(env) }

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	return slices.ContainsFunc(cs, func(c Check) bool { return c.Status == Fail })
}

// Write prints checks as lines, or as a JSON array.
func Write(w io.Writer, cs []Check, asJSON bool) {
	if asJSON {
		json.MarshalWrite(w, cs)
		fmt.Fprintln(w)
		return
	}
	for _, c := range cs {
		fmt.Fprintln(w, c)
	}
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// output runs a CLI and returns its stdout, or an error carrying the last line it printed
// to stderr.
func output(argv ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		return out, fmt.Errorf("`%s` failed: %w %s", strings.Join(argv, " "), err, lines[len(lines)-1])
	}
	return out, nil
}

func pathCheck() Check {
	c := Check{Check: "path"}
	self, err := os.Executable()
	if err != nil {
		c.Status, c.Detail = Fail, "can't find this binary: "+err.Error()
		return c
	}
	dir := filepath.Dir(self)
	found, err := exec.LookPath("cairn")
	if err != nil {
		c.Status, c.Detail, c.Fix = Fail, "cairn is not on PATH", fmt.Sprintf(`add export PATH="%s:$PATH" to your shell profile`, dir)
		return c
	}
	if a, b := stat(found), stat(self); a == nil || b == nil || !os.SameFile(a, b) {
		c.Status, c.Detail, c.Fix = Fail, fmt.Sprintf("cairn on PATH is %s, not %s", found, self), "put "+dir+" ahead of "+filepath.Dir(found)+" in PATH"
		return c
	}
	c.Status, c.Detail = OK, self
	return c
}

func stat(p string) os.FileInfo {
	fi, _ := os.Stat(p)
	return fi
}

func ghCheck() Check {
	c := Check{Check: "gh"}
	if !onPath("gh") {
		c.Status, c.Detail, c.Fix = Fail, "gh is not on PATH; cairn reads PRs through it", "install the GitHub CLI: https://cli.github.com"
	} else if _, err := output("gh", "auth", "status"); err != nil {
		c.Status, c.Detail, c.Fix = Fail, "gh is not logged in", "run `gh auth login`"
	} else {
		c.Status, c.Detail = OK, "installed and logged in"
	}
	return c
}

// codexHooks are the hooks.state keys Codex stores trust under for hooks/hooks.json.
var codexHooks = []string{
	Plugin + ":hooks/hooks.json:session_start:0:0",
	Plugin + ":hooks/hooks.json:post_tool_use:0:0",
}

// codexConfig is the part of $CODEX_HOME/config.toml that says whether Codex runs hooks.
type codexConfig struct {
	Features struct {
		Hooks *bool `toml:"hooks"`
	} `toml:"features"`
	Hooks struct {
		State map[string]struct {
			TrustedHash string `toml:"trusted_hash"`
			Enabled     *bool  `toml:"enabled"`
		} `toml:"state"`
	} `toml:"hooks"`
}

const codexTrustFix = "open Codex, run /hooks, and trust and enable cairn's SessionStart and PostToolUse hooks"

// codexHooksCheck reads whether the user trusted cairn's hooks in Codex, which runs a
// hook only once trusted and skips it silently otherwise. Setup can't trust them, so a
// hook that isn't is a warning, not a failure.
func codexHooksCheck() Check {
	c := Check{Check: "codex-hooks"}
	home, _ := os.UserHomeDir()
	file := filepath.Join(cmp.Or(os.Getenv("CODEX_HOME"), filepath.Join(home, ".codex")), "config.toml")
	var cfg codexConfig
	if _, err := toml.DecodeFile(file, &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.Status, c.Detail = Warn, "can't read "+file+": "+err.Error()
		return c
	}
	if h := cfg.Features.Hooks; h != nil && !*h {
		c.Status, c.Detail, c.Fix = Warn, "Codex hooks are turned off ([features] hooks = false)", "set hooks = true under [features] in "+file
		return c
	}
	var problems []string
	for _, key := range codexHooks {
		switch s, ok := cfg.Hooks.State[key]; {
		case !ok || s.TrustedHash == "":
			problems = append(problems, key+" not trusted")
		case s.Enabled != nil && !*s.Enabled:
			problems = append(problems, key+" disabled")
		}
	}
	if len(problems) > 0 {
		c.Status, c.Detail, c.Fix = Warn, strings.Join(problems, ", "), codexTrustFix
		return c
	}
	c.Status, c.Detail = OK, "cairn's hooks are trusted"
	return c
}
