package setup

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// A setup step's status, besides OK.
const (
	Done   = "done"
	Skip   = "skip"
	Manual = "manual"
)

// Run installs cairn's plugin where it's missing, herdr's from a folder in env.Home,
// printing one `ok|done|skip|manual <step>: <detail>` line per step; a manual line names
// what a person has to do. With dryRun it changes nothing and says what it would run. It
// never edits a config file: agents and herdr are changed only through their own plugin
// CLIs. It returns doctor's checks as they stand after it, each CLI having been asked
// once.
func Run(w io.Writer, dryRun bool, env Env) []Check {
	s := steps{w: w, dryRun: dryRun}
	p := probeAll(true)
	for _, c := range []Check{p.path, p.gh} {
		if c.Status == OK {
			s.line(OK, c.Check, c.Detail)
		} else {
			s.line(Manual, c.Check, c.Detail+"; "+c.Fix)
		}
	}
	for i := range p.agents {
		a := &p.agents[i]
		switch {
		case !a.present:
			s.line(Skip, a.name, a.name+" is not on PATH")
			continue
		case a.err != nil:
			s.line(Manual, a.name, a.err.Error())
			continue
		}
		if a.marketplace {
			s.line(OK, a.name+"-marketplace", marketplaceName+" is added")
		} else {
			s.run(a.name+"-marketplace", a.addMarketplace)
		}
		step := a.name + "-plugin"
		switch {
		case !a.installed:
			if s.run(step, a.install) {
				a.installed, a.enabled = true, true
			}
		case a.enabled:
			s.line(OK, step, Plugin+" installed and enabled")
		case a.enable == nil:
			s.line(Manual, step, Plugin+" is disabled; "+a.enableFix)
		default:
			a.enabled = s.run(step, a.enable)
		}
		if a.hooks == nil {
			continue
		}
		if c := a.trust; c.Status == OK {
			s.line(OK, c.Check, c.Detail)
		} else {
			s.line(Manual, c.Check, cmp.Or(c.Fix, c.Detail))
		}
	}
	s.herdr(&p.herdr, filepath.Join(env.Home, "herdr-plugin"))
	return p.checks(env)
}

type steps struct {
	w      io.Writer
	dryRun bool
}

func (s steps) line(status, step, detail string) {
	fmt.Fprintf(s.w, "%s %s: %s\n", status, step, detail)
}

// run runs a step's command, or in a dry run says it would, and reports whether it ran.
func (s steps) run(step string, argv []string) bool {
	cmd := "`" + strings.Join(argv, " ") + "`"
	if s.dryRun {
		s.line(Skip, step, "dry run; would run "+cmd)
		return false
	}
	if _, err := output(argv...); err != nil {
		s.line(Manual, step, err.Error()+"; run "+cmd+" yourself")
		return false
	}
	s.line(Done, step, "ran "+cmd)
	return true
}
