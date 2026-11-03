package main

import (
	"cmp"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/ken8203/cairn/internal/github"
	"github.com/ken8203/cairn/internal/hook"
	"github.com/ken8203/cairn/internal/host"
	"github.com/ken8203/cairn/internal/orca"
	"github.com/ken8203/cairn/internal/session"
	"github.com/ken8203/cairn/internal/setup"
	"github.com/ken8203/cairn/internal/skills"
	"github.com/ken8203/cairn/internal/trail"
	"github.com/ken8203/cairn/internal/ui"
)

const help = `Where each chain of work stands. A trail is an ordered checklist of PRs and free-text
steps (releases, flags, rollouts) that belongs to an agent session, and to any other
session that joins it. cairn's agent hook records the PRs each session touches.

  cairn [show] [--json] [--dir <path>]   this session's trail, else this folder's, and the PRs it touched
  cairn ls                   every trail with its progress and next step
  cairn use <name>           attach this session to a trail, creating it if needed
  cairn add <step>...        append steps: owner/repo#n, a PR URL, or free text
  cairn link <n> <pr>        turn free-text step n into that PR once it's open
  cairn done <n>             tick (or untick) free-text step n
  cairn note <text>          append a note
  cairn edit [name]          open a trail in $EDITOR
  cairn watch [pane] [--dir <path>]   keep a pane's view on screen (the panel)
  cairn watch --from-env     the same, for $CAIRN_PANE and $CAIRN_DIR (herdr's overlay)
  cairn panel [--close] [pane]   open or close a pane's panel in the terminal it runs in
  cairn panel --lua [block]  print Rex's toggle as Lua, for an init.lua action
  cairn statusline           Claude Code statusline segment
  cairn setup [--dry-run]    install cairn's plugin in Claude Code, Codex and herdr, then run doctor
  cairn doctor [--json]      check the install without changing it; exit 1 on a failure
  cairn skills list          the guides for agents built into cairn
  cairn skills get <topic> [--full]   print a guide: cairn (using it), setup
  cairn hook                 handle a Claude Code or Codex hook payload on stdin
  cairn record [--own] [--session id] [-C dir] [-R repo] [pr]   remember a PR (the hook runs this)
`

func main() {
	home, _ := os.UserHomeDir()
	app := newApp(cmp.Or(os.Getenv("CAIRN_HOME"), filepath.Join(home, ".cairn")))
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		runHook(app, os.Stdin, os.Stdout)
		return
	}
	if err := run(app, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "cairn: %v\n", err)
		os.Exit(1)
	}
}

const ttl = 60 * time.Second

// where is what doctor reports about the terminal and agent session cairn runs in.
func where(app ui.App) setup.Env {
	return setup.Env{Host: host.Name, Pane: session.Pane, Agent: session.Agent, Trail: app.Sessions.Trail, Hooked: app.Sessions.Hooked, Orca: func() string { return orcaDetail(app) }, Home: app.Sessions.Dir}
}

// orcaDetail counts the Orca worktrees on trails, for doctor.
func orcaDetail(app ui.App) string {
	n, err := orca.Count(func(dir string) (orca.Card, bool) { return card(app, dir) })
	switch {
	case errors.Is(err, orca.ErrNotInstalled):
		return "not installed"
	case err != nil:
		return "not reachable"
	}
	return fmt.Sprintf("%d worktrees on cairn trails", n)
}

// newApp wires cairn's home, its state and cache, to the real GitHub and terminal hosts.
func newApp(home string) ui.App {
	trails := trail.Store{Dir: filepath.Join(home, "trails")}
	return ui.App{
		Sessions: session.Store{Dir: home, Trails: trails, Label: host.Label},
		Cache:    github.Cache{Dir: filepath.Join(home, "cache"), TTL: ttl, Query: github.Query},
		Title:    host.Title,
		Spawn:    spawn,
	}
}

func run(app ui.App, args []string) error {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	trails, sessions := app.Sessions.Trails, app.Sessions
	agent := session.Agent()
	// Some hosts are asked for the pane, so only commands that use it pay for that.
	pane := sync.OnceValue(session.Pane)
	key := func() string { return cmp.Or(agent, pane()) }
	// enter is how a command acts as the agent's session, else the pane's: the pane points
	// at it.
	enter := func(agent string) error {
		if cmp.Or(agent, pane()) == "" {
			return errors.New("not in an agent session or a pane")
		}
		return sessions.NotePane(agent, pane())
	}
	// dir is the folder whose trail a view falls back to: --dir, else the cwd.
	dir := func() string {
		if i := slices.Index(args, "--dir"); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
		wd, _ := os.Getwd()
		return wd
	}
	// trailOr is this session's own trail, or an error naming the folder's trail to join.
	trailOr := func() (string, error) {
		if err := enter(agent); err != nil {
			return "", err
		}
		if name := sessions.Trail(key()); name != "" {
			return name, nil
		}
		if name, at := sessions.ResolveDir(dir(), app.Head); name != "" {
			return "", fmt.Errorf("this session has no trail; this folder's branch %s is on trail %s, run `cairn use %s` to join it", at.Branch, name, name)
		}
		return "", errors.New("this session has no trail; run `cairn use <name>` first")
	}

	switch arg(0) {
	case "", "show", "--json", "--dir":
		// Outside a session, as in a plain terminal, the folder's trail still shows.
		err := enter(agent)
		found := app.Find(agent, pane(), dir())
		if err != nil && found.Trail == "" {
			return err
		}
		if slices.Contains(args, "--json") {
			b, err := json.Marshal(app.Data(found))
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		}
		width := 0
		if term.IsTerminal(os.Stdout.Fd()) {
			width = termWidth()
		}
		for _, l := range app.Show(found, width) {
			fmt.Println(l)
		}
	case "ls":
		fmt.Print(app.List())
	case "use":
		if err := enter(agent); err != nil {
			return err
		}
		if arg(1) == "" {
			return errors.New("usage: cairn use <name>")
		}
		return synced(app, sessions.Bind(key(), arg(1)))
	case "add":
		name, err := trailOr()
		if err != nil {
			return err
		}
		if len(args) < 2 {
			return errors.New("usage: cairn add <owner/repo#n | PR URL | text>...")
		}
		return synced(app, add(app, name, args[1:]))
	case "link":
		name, err := trailOr()
		if err != nil {
			return err
		}
		n, ok := stepNumber(arg(1))
		ref := trail.ParseRef(arg(2))
		if !ok || ref == "" {
			return errors.New("usage: cairn link <step number> <owner/repo#n | PR URL>")
		}
		if !app.Cache.Exists(trail.URL(ref)) {
			return fmt.Errorf("%s: no such PR", ref)
		}
		return synced(app, trails.Link(name, n, ref))
	case "done":
		name, err := trailOr()
		if err != nil {
			return err
		}
		n, ok := stepNumber(arg(1))
		if !ok {
			return errors.New("usage: cairn done <step number>")
		}
		return synced(app, trails.Tick(name, n))
	case "note":
		name, err := trailOr()
		if err != nil {
			return err
		}
		if len(args) < 2 {
			return errors.New("usage: cairn note <text>")
		}
		return synced(app, trails.Note(name, strings.Join(args[1:], " ")))
	case "edit":
		name := arg(1)
		if name == "" && key() != "" {
			if err := enter(agent); err != nil {
				return err
			}
			name = sessions.Trail(key())
		}
		if name == "" {
			return errors.New("usage: cairn edit <name>")
		}
		if _, err := trails.Create(name); err != nil {
			return err
		}
		cmd := ui.Editor(trails.File(name))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	case "watch":
		if slices.Contains(args[1:], "--from-env") {
			// herdr's overlay is a pane of its own, so the panel's pane and folder come in
			// its env.
			target := host.Key(os.Getenv("CAIRN_PANE"))
			if target == "" {
				return errors.New("cairn watch --from-env: no CAIRN_PANE")
			}
			return app.Watch(target, cmp.Or(os.Getenv("CAIRN_DIR"), dir()))
		}
		var target string
		for i := 1; i < len(args); i++ {
			if args[i] == "--dir" {
				i++
			} else {
				target = host.Key(args[i])
			}
		}
		target = cmp.Or(target, pane())
		if target == "" {
			return errors.New("usage: cairn watch [pane] [--dir <path>]")
		}
		return app.Watch(target, dir())
	case "panel":
		self, err := os.Executable()
		if err != nil {
			return err
		}
		var target string
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "--") {
				target = host.Key(a)
			}
		}
		wd, _ := os.Getwd()
		closeOnly := slices.Contains(args[1:], "--close")
		if slices.Contains(args[1:], "--lua") {
			lua, err := host.RexPanelLua(self, target, wd, closeOnly)
			if err != nil {
				return err
			}
			fmt.Print(lua)
			return nil
		}
		err = host.Panel(self, target, wd, closeOnly)
		if errors.Is(err, host.ErrNoPanel) {
			fmt.Println("cairn panel:", err)
			return nil
		}
		return err
	case "statusline":
		if key() == "" {
			return nil
		}
		if err := enter(agent); err != nil {
			return err
		}
		fmt.Print(app.Statusline(app.Find(agent, pane(), dir())))
	case "record":
		o := recordArgs(args[1:])
		if o.session != "" && !trail.ValidName(o.session) {
			return fmt.Errorf("invalid session id %q", o.session)
		}
		// The hook names the session; run by hand, it's this one.
		as := cmp.Or(o.session, agent)
		if cmp.Or(as, pane()) == "" {
			return nil
		}
		if err := enter(as); err != nil {
			return err
		}
		return record(app, cmp.Or(as, pane()), o)
	case "__refresh":
		// The statusline's background refresh, holding the session's lock until done.
		if len(args) < 2 {
			return nil
		}
		defer app.Cache.Unlock(args[1])
		app.Cache.Refresh(false, args[2:])
		syncOrca(app)
	case "__orca":
		// The detached Orca sync, run again while commands asked for one during it.
		for {
			time.Sleep(orcaDebounce)
			os.Remove(orcaAgain(app))
			orca.Sync(func(dir string) (orca.Card, bool) { return card(app, dir) })
			app.Cache.Unlock(orcaLock)
			if _, err := os.Stat(orcaAgain(app)); err != nil || !app.Cache.TryLock(orcaLock) {
				return nil
			}
		}
	case "doctor":
		return report(setup.Doctor(where(app)), slices.Contains(args[1:], "--json"))
	case "setup":
		return report(setup.Run(os.Stdout, slices.Contains(args[1:], "--dry-run"), where(app)), false)
	case "skills":
		return skillsCmd(args[1:])
	case "-h", "--help":
		fmt.Print(help)
	default:
		return fmt.Errorf("unknown command '%s'; see `cairn --help`", arg(0))
	}
	return nil
}

// skillsCmd lists the built-in guides or prints one.
func skillsCmd(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		for _, t := range skills.Topics {
			fmt.Printf("%-8s %s\n", t.Name, t.Summary)
		}
		return nil
	case len(args) >= 2 && args[0] == "get":
		guide, err := skills.Get(args[1], slices.Contains(args[2:], "--full"))
		if err != nil {
			return err
		}
		fmt.Print(guide)
		return nil
	}
	return errors.New("usage: cairn skills list | cairn skills get <topic> [--full]")
}

func termWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil {
		return w
	}
	return 80
}

// report prints doctor's checks and fails when any check did.
func report(cs []setup.Check, asJSON bool) error {
	setup.Write(os.Stdout, cs, asJSON)
	if setup.Failed(cs) {
		return errors.New("some checks failed")
	}
	return nil
}

// recordOpts are record's arguments: --own, --session id, -C dir and -R repo anywhere
// around the PR.
type recordOpts struct {
	own                        bool
	session, dir, repo, target string
}

func recordArgs(args []string) recordOpts {
	o := recordOpts{dir: "."}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--own":
			o.own = true
		case (a == "-C" || a == "-R" || a == "--session") && i+1 < len(args):
			i++
			switch a {
			case "-C":
				o.dir = args[i]
			case "-R":
				o.repo = args[i]
			default:
				o.session = args[i]
			}
		case strings.HasPrefix(a, "-C") && len(a) > 2:
			o.dir = a[2:]
		case strings.HasPrefix(a, "-R") && len(a) > 2:
			o.repo = a[2:]
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) > 0 {
		o.target = rest[0]
	}
	return o
}

// record remembers a PR GitHub knows for this session; see session.Store.Record.
func record(app ui.App, key string, o recordOpts) error {
	url := trail.LeadingURL(o.target)
	if url == "" {
		gh := []string{"pr", "view"}
		if o.target != "" {
			gh = append(gh, o.target)
		}
		if o.repo != "" {
			gh = append(gh, "-R", o.repo)
		}
		cmd := exec.Command("gh", append(gh, "--json", "url", "-q", ".url")...)
		cmd.Dir = o.dir
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		url = strings.TrimRight(string(out), "\n")
		if trail.LeadingURL(url) != url {
			return nil
		}
	}
	app.Cache.Fetch([]string{url})
	p, ok := app.Cache.Read(url)
	if !ok {
		return nil
	}
	at, _ := session.Where(o.dir)
	return synced(app, app.Sessions.Record(key, url, o.own, p.Head, at))
}

// orcaLock is the refresh lock the Orca sync holds, and orcaDebounce how long it waits
// for the rest of a burst of commands.
const (
	orcaLock     = "__orca"
	orcaDebounce = 300 * time.Millisecond
)

// orcaAgain marks that a sync was asked for, so a running one runs again.
func orcaAgain(app ui.App) string { return filepath.Join(app.Cache.Dir, ".orca-again") }

// synced starts an Orca sync after a change that succeeded.
func synced(app ui.App, err error) error {
	if err == nil {
		syncOrca(app)
	}
	return err
}

// syncOrca starts a detached `cairn __orca` when Orca is installed, unless one is
// running: that one then runs again.
func syncOrca(app ui.App) {
	if orca.Path() == "" || os.MkdirAll(app.Cache.Dir, 0o755) != nil {
		return
	}
	os.WriteFile(orcaAgain(app), nil, 0o644)
	if app.Cache.TryLock(orcaLock) && app.Spawn("__orca") != nil {
		app.Cache.Unlock(orcaLock)
	}
}

// card is what the card of an Orca worktree at dir shows of the trail its folder is on.
func card(app ui.App, dir string) (orca.Card, bool) {
	name, at := app.Sessions.ResolveDir(dir, app.Head)
	if name == "" {
		return orca.Card{}, false
	}
	c := orca.Card{Repo: at.Repo}
	for _, url := range app.Sessions.Trails.URLs(name) {
		repo, _, _ := strings.Cut(trail.Ref(url), "#")
		if p, ok := app.Cache.Read(url); ok && c.PR == 0 && strings.EqualFold(repo, at.Repo) && p.Head == at.Branch {
			c.PR = p.Number
			continue
		}
		c.Refs = append(c.Refs, url)
	}
	done, total, next := app.Progress(name)
	c.Note = orca.Note(name, done, total, next)
	return c, true
}

// runHook handles a Claude Code or Codex hook payload. It never fails the agent: errors
// and panics are dropped, GitHub is asked from a detached `cairn record`, and only
// SessionStart prints, for a session on a trail or in a folder that has one.
func runHook(app ui.App, in io.Reader, out io.Writer) {
	defer func() { recover() }()
	p, err := hook.Parse(in)
	if err != nil || !trail.ValidName(p.SessionID) {
		return
	}
	switch p.Event {
	case "PostToolUse":
		for _, args := range hook.Records(p) {
			app.Spawn(slices.Concat([]string{"record", "--session", p.SessionID}, args)...)
		}
	case "SessionStart":
		app.Sessions.NoteHooked(p.SessionID)
		// The pane's panel switches to the new session at once.
		app.Sessions.NotePane(p.SessionID, session.Pane())
		if ctx := app.Context(p.SessionID, p.Cwd); ctx != "" {
			out.Write(hook.SessionStartOutput(ctx))
		}
	}
}

// add appends steps to the trail, refusing a PR GitHub doesn't know before writing any.
func add(app ui.App, name string, steps []string) error {
	trails := app.Sessions.Trails
	if _, err := trails.Create(name); err != nil {
		return err
	}
	for _, s := range steps {
		if ref := trail.ParseRef(trail.Clean(s)); ref != "" && !app.Cache.Exists(trail.URL(ref)) {
			return fmt.Errorf("%s: no such PR", ref)
		}
	}
	return trails.Add(name, false, steps...)
}

// spawn starts `cairn <args>` from next to this executable, detached with its stdio on
// /dev/null, so the caller is never slowed or killed by it.
func spawn(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := &exec.Cmd{
		Path:        filepath.Join(filepath.Dir(self), "cairn"),
		Args:        slices.Concat([]string{"cairn"}, args),
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func stepNumber(s string) (int, bool) {
	if !isNumber(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}
