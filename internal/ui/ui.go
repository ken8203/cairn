// Package ui lays out sessions' trails for the terminal: show, ls, the Claude Code
// statusline, and the panel.
package ui

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/ken8203/cairn/internal/github"
	"github.com/ken8203/cairn/internal/session"
	"github.com/ken8203/cairn/internal/trail"
)

// Not ✓: that marks a finished step.
var ciMark = map[string]string{"pass": "\x1b[32m●\x1b[0m", "fail": "\x1b[31m✗\x1b[0m", "pending": "\x1b[33m◌\x1b[0m", "none": " "}

var stateStyle = map[string]struct{ label, color string }{
	"OPEN":    {"open", "32"},
	"MERGED":  {"merged", "35"},
	"CLOSED":  {"closed", "31"},
	"UNKNOWN": {"…", "2"},
}

// A PR row's width besides its ref, title and hint.
const (
	prRowChrome   = 21
	hintGap       = 3
	minTitleWidth = 12
)

const (
	ownerTitleWidth = 24
	statusStepWidth = 32
	lsNameWidth     = 24
	lsNextWidth     = 60
)

// App is what the views read: sessions and their trails, and cached PR state.
type App struct {
	Sessions session.Store
	Cache    github.Cache
	// Title is the title of the pane a stored key names, in whichever host it belongs to.
	Title func(pane string) string
	// Spawn starts a detached `cairn <args>`, which runs the statusline's refresh.
	Spawn func(args ...string) error
}

// Editor is $EDITOR, or vi, opening file.
func Editor(file string) *exec.Cmd {
	argv := strings.Fields(cmp.Or(os.Getenv("EDITOR"), "vi"))
	return exec.Command(argv[0], append(argv[1:], file)...)
}

// Find is what the views show for the agent's session, else the pane's, else the folder
// dir; see session.Store.Find.
func (a App) Find(agent, pane, dir string) session.Found {
	return a.Sessions.Find(agent, pane, dir, a.Head)
}

// Head is a PR's cached head branch, empty until fetched.
func (a App) Head(url string) string {
	p, _ := a.Cache.Read(url)
	return p.Head
}

// Show is a trail and the PRs its session touched as terminal lines, refreshing stale PRs
// first; width 0 leaves titles whole.
func (a App) Show(f session.Found, width int) []string {
	return render(a.gather(f, false), width).lines
}

// Data is a trail and the PRs its session touched as `cairn show --json` prints them,
// read from the cache alone; stale PRs are refreshed in the background.
func (a App) Data(f session.Found) Data {
	b := a.load(f)
	urls := b.urls()
	if slices.ContainsFunc(urls, func(u string) bool { return !a.Cache.Fresh(u) }) {
		a.refreshInBackground(b.key, urls)
	}
	a.readPRs(b)

	d := Data{PRs: []PRData{}}
	var untracked []string
	if b.name != "" {
		headOf := map[string]string{}
		for _, s := range b.steps {
			if s.PR && s.state.Head != "" {
				headOf[repoOf(s.Value)+":"+s.state.Head] = s.Value
			}
		}
		t := &TrailData{Name: b.name, Total: len(b.steps), Steps: []StepData{}, Notes: []string{}}
		for _, n := range b.notes {
			t.Notes = append(t.Notes, trail.Clean(strings.TrimPrefix(n, "- ")))
		}
		for i, s := range b.steps {
			sd := StepData{N: i + 1, Kind: "text", Done: s.done()}
			if !s.PR {
				sd.Text = trail.Clean(s.Value)
			} else {
				sd.Kind, sd.PRData = "pr", prData(s.Value, s.state)
				if parent := headOf[repoOf(s.Value)+":"+s.state.Base]; s.state.Base != "" && parent != "" && parent != s.Value {
					sd.StackedOn = trail.Ref(parent)
				}
			}
			if sd.Done {
				t.Done++
			}
			t.Steps = append(t.Steps, sd)
		}
		d.Trail, d.Via = t, b.via
		for _, url := range b.touched {
			if !slices.ContainsFunc(b.steps, func(s step) bool { return s.Value == url }) {
				untracked = append(untracked, url)
			}
		}
	} else {
		untracked = b.touched
	}
	for _, url := range untracked {
		d.PRs = append(d.PRs, prData(url, b.prs[url]))
	}
	return d
}

// Data is what `cairn show --json` prints; Trail is nil for a session without one.
type Data struct {
	Trail *TrailData `json:"trail"`
	// Via is how the trail was found: session, pane or dir.
	Via string `json:"via,omitempty"`
	// PRs are those the session touched that aren't on its trail.
	PRs []PRData `json:"prs"`
}

type TrailData struct {
	Name  string     `json:"name"`
	Done  int        `json:"done"`
	Total int        `json:"total"`
	Steps []StepData `json:"steps"`
	Notes []string   `json:"notes"`
}

// StepData is a step: a text step's Text, or a PR step's PR.
type StepData struct {
	N    int    `json:"n"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	Done bool   `json:"done"`
	PRData
	// StackedOn is the owner/repo#n of the step whose head is this PR's base.
	StackedOn string `json:"stacked_on,omitempty"`
}

// PRData is a PR as owner/repo#n and its cached state: State is open, draft, merged,
// closed, or unknown until fetched; CI is none, pending, pass or fail.
type PRData struct {
	Ref   string `json:"ref,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	State string `json:"state,omitempty"`
	CI    string `json:"ci,omitempty"`
}

func prData(url string, p github.PR) PRData {
	state := strings.ToLower(p.State)
	if p.State == "OPEN" && p.Draft {
		state = "draft"
	}
	return PRData{Ref: trail.Ref(url), URL: url, Title: trail.Clean(p.Title), State: state, CI: p.CI}
}

// step is a trail step with its PR's cached state, once read.
type step struct {
	trail.Step
	state github.PR
}

// done is a PR step once GitHub says merged or closed, a text step once ticked.
func (s step) done() bool {
	if s.PR {
		return s.state.Finished()
	}
	return s.Ticked
}

func shortRef(ref string) string {
	_, rest, _ := strings.Cut(ref, "/")
	return rest
}

// loadSteps reads a trail's steps, without PR state yet, and its notes.
func (a App) loadSteps(name string) ([]step, []string) {
	ts, notes := a.Sessions.Trails.Load(name)
	ss := make([]step, len(ts))
	for i, s := range ts {
		ss[i].Step = s
	}
	return ss, notes
}

func dim(s string) string  { return "\x1b[2m" + s + "\x1b[0m" }
func bold(s string) string { return "\x1b[1m" + s + "\x1b[0m" }

// view is a session's trail as terminal lines, and what each number key acts on: a PR URL to
// open, or step:<n> to tick.
type view struct {
	lines   []string
	targets []string
}

// board is what a session shows, each file read once: its trail's steps and notes and how
// the trail was found, the PRs it touched, and once read, every PR's cached state and the
// other sessions on them.
type board struct {
	key, name string
	via       string
	place     session.Place
	steps     []step
	notes     []string
	touched   []string
	prs       map[string]github.PR
	owners    map[string]string // pane titles of the other sessions that touched a PR
}

func (a App) load(f session.Found) *board {
	b := &board{key: f.Key, name: f.Trail, via: f.Via, place: f.Place, touched: a.Sessions.URLs(f.Key)}
	if b.name != "" {
		b.steps, b.notes = a.loadSteps(b.name)
	}
	return b
}

// gather loads a board for its view, refreshing stale PRs first, or all with force.
func (a App) gather(f session.Found, force bool) *board {
	b := a.load(f)
	a.Cache.Refresh(force, b.urls())
	a.readPRs(b)
	a.readOwners(b)
	return b
}

// urls are the PRs a session watches: those it touched and those on its trail.
func (b *board) urls() []string {
	urls := slices.Clone(b.touched)
	for _, s := range b.steps {
		if s.PR {
			urls = append(urls, s.Value)
		}
	}
	slices.Sort(urls)
	return slices.Compact(urls)
}

func (a App) readPRs(b *board) {
	b.prs = map[string]github.PR{}
	for _, url := range b.urls() {
		b.prs[url] = a.Cache.Info(url)
	}
	for i, s := range b.steps {
		if s.PR {
			b.steps[i].state = b.prs[s.Value]
		}
	}
}

// readOwners names the other sessions that touched each trail PR this one didn't by the
// title of the pane each last ran in; a session whose pane is gone has no title and is
// left out.
func (a App) readOwners(b *board) {
	var want []string
	for _, s := range b.steps {
		if s.PR && !slices.Contains(b.touched, s.Value) {
			want = append(want, s.Value)
		}
	}
	if len(want) == 0 {
		return
	}
	touchedBy := a.Sessions.TouchedBy()
	for url, keys := range touchedBy {
		touchedBy[url] = slices.DeleteFunc(keys, func(k string) bool { return k == b.key })
	}
	if !slices.ContainsFunc(want, func(url string) bool { return len(touchedBy[url]) > 0 }) {
		return
	}
	panes := a.Sessions.Panes()
	var need []string
	for _, url := range want {
		for _, other := range touchedBy[url] {
			if p := panes[other]; p != "" && !slices.Contains(need, p) {
				need = append(need, p)
			}
		}
	}
	titles := a.paneTitles(need)
	b.owners = map[string]string{}
	for _, url := range want {
		var names []string
		for _, other := range touchedBy[url] {
			if t := titles[panes[other]]; t != "" {
				names = append(names, ansi.Truncate(t, ownerTitleWidth, ""))
			}
		}
		b.owners[url] = strings.Join(names, ", ")
	}
}

// key is the number of the target just added; steps keep their `cairn done` number.
func (v *view) key() string {
	s := fmt.Sprintf("%2d", len(v.targets))
	return s[len(s)-2:]
}

func (v *view) prLine(p github.PR, url string, width int, marker string, refWidth int, indent string, hints []string) {
	title := trail.Clean(p.Title)
	style := stateStyle[p.State]
	if p.State == "OPEN" && p.Draft {
		style.label, style.color = "draft", "2"
	}
	switch p.Review {
	case "APPROVED":
		hints = append(hints, "approved")
	case "CHANGES_REQUESTED":
		hints = append(hints, "changes requested")
	}
	if p.State == "OPEN" && p.CI == "fail" {
		hints = append(hints, "CI failing")
	}
	hint := strings.Join(hints, " · ")
	ref := indent + shortRef(trail.Ref(url))
	// Open PRs show CI; merged or closed ones are finished steps, ticked like the rest.
	mark := cmp.Or(ciMark[p.CI], " ")
	if p.Finished() {
		mark = dim("✓")
	}
	v.targets = append(v.targets, url)
	if width > 0 {
		rest := width - refWidth - prRowChrome - ansi.StringWidth(hint)
		if hint != "" {
			rest -= hintGap
		}
		title = ansi.Truncate(title, max(rest, minTitleWidth), "…")
	}
	// OSC 8 links the ref; its padding is added outside the escape sequence.
	line := fmt.Sprintf(" %s %s %s \x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\%*s \x1b[%sm%-6s\x1b[0m %s",
		dim(v.key()), marker, mark, url, ref, refWidth-ansi.StringWidth(ref), "", style.color, style.label, title)
	if hint != "" {
		line += "  " + dim(hint)
	}
	v.lines = append(v.lines, line)
}

func repoOf(url string) string {
	repo, _, _ := strings.Cut(trail.Ref(url), "#")
	return repo
}

// render lays a session's board out; width 0 leaves titles whole.
func render(b *board, width int) *view {
	v := &view{}
	var untracked []string

	if b.name != "" {
		headOf := map[string]string{}
		next, done, refWidth := -1, 0, 0
		for i, s := range b.steps {
			if s.PR {
				if s.state.Head != "" {
					headOf[repoOf(s.Value)+":"+s.state.Head] = s.Value
				}
				refWidth = max(refWidth, ansi.StringWidth(shortRef(trail.Ref(s.Value)))+2)
			}
			if s.done() {
				done++
			} else if next < 0 {
				next = i
			}
		}

		v.lines = append(v.lines, bold(b.name)+"  "+dim(fmt.Sprintf("%d/%d done", done, len(b.steps))))
		if b.via == "dir" {
			v.lines = append(v.lines, dim(fmt.Sprintf("found by folder: %s on %s — `cairn use %s` to join", filepath.Base(b.place.Dir), b.place.Branch, b.name)))
		}
		v.lines = append(v.lines, "")
		for i, s := range b.steps {
			marker := " "
			if i == next {
				marker = "\x1b[1;36m▶\x1b[0m"
			}
			if !s.PR {
				v.targets = append(v.targets, fmt.Sprintf("step:%d", i+1))
				if s.done() {
					v.lines = append(v.lines, fmt.Sprintf(" %s %s %s", dim(v.key()), marker, dim("✓ "+trail.Clean(s.Value))))
				} else {
					v.lines = append(v.lines, fmt.Sprintf(" %s %s ☐ %s", dim(v.key()), marker, trail.Clean(s.Value)))
				}
				continue
			}
			url, p := s.Value, s.state
			var indent string
			var hints []string
			if parent := headOf[repoOf(url)+":"+p.Base]; p.Base != "" && parent != "" && parent != url {
				indent = "└ "
				if p.State == "OPEN" && b.prs[parent].State == "MERGED" {
					hints = append(hints, "base merged, retarget")
				}
			}
			if slices.Contains(b.touched, url) {
				hints = append(hints, "this session")
			} else if others := b.owners[url]; others != "" {
				hints = append(hints, others)
			}
			v.prLine(p, url, width, marker, refWidth, indent, hints)
		}

		if len(b.notes) > 0 {
			v.lines = append(v.lines, "", bold("Notes"))
			for _, n := range b.notes {
				v.lines = append(v.lines, "  "+n)
			}
		}
		for _, url := range b.touched {
			if !slices.ContainsFunc(b.steps, func(s step) bool { return s.Value == url }) {
				untracked = append(untracked, url)
			}
		}
	} else {
		untracked = b.touched
	}

	if len(untracked) > 0 {
		heading := "PRs touched in this session"
		if b.name != "" {
			heading = "Other " + heading
		}
		v.lines = append(v.lines, "", bold(heading))
		refWidth := 0
		for _, url := range untracked {
			refWidth = max(refWidth, ansi.StringWidth(shortRef(trail.Ref(url))))
		}
		for _, url := range untracked {
			v.prLine(b.prs[url], url, width, " ", refWidth, "", nil)
		}
	}
	if b.name == "" {
		v.lines = append(v.lines, "", dim("No trail yet. `cairn use <name>` starts one for this session."))
	}
	return v
}

// progress counts a trail's finished steps and finds the first unfinished one.
func progress(ss []step) (finished, total int, next *step) {
	for i, s := range ss {
		if s.done() {
			finished++
		} else if next == nil {
			next = &ss[i]
		}
	}
	return finished, len(ss), next
}

// withStates reads each PR step's cached state.
func (a App) withStates(ss []step) []step {
	for i, s := range ss {
		if s.PR {
			ss[i].state = a.Cache.Info(s.Value)
		}
	}
	return ss
}

func withMark(s, ci string) string {
	if m := ciMark[ci]; m != "" {
		return s + " " + m
	}
	return s
}

// Statusline is the trail, its progress and next step, or without a trail the open PRs
// its session touched. It answers from the cache and refreshes stale PRs in the
// background.
func (a App) Statusline(f session.Found) string {
	b := a.load(f)
	urls := b.urls()
	if slices.ContainsFunc(urls, func(u string) bool { return !a.Cache.Fresh(u) }) {
		a.refreshInBackground(b.key, urls)
	}
	a.readPRs(b)

	if b.name != "" {
		finished, total, next := progress(b.steps)
		s := fmt.Sprintf("%s %d/%d", b.name, finished, total)
		switch {
		case next == nil:
		case next.PR:
			s += " · ▶ " + withMark(shortRef(trail.Ref(next.Value)), next.state.CI)
		default:
			s += " · ▶ " + ansi.Truncate(trail.Clean(next.Value), statusStepWidth, "")
		}
		return s
	}

	var segments []string
	for _, url := range b.touched {
		p := b.prs[url]
		if p.Finished() {
			continue
		}
		segments = append(segments, withMark(fmt.Sprintf("#%d", p.Number), p.CI))
	}
	if len(segments) == 0 {
		return ""
	}
	return "PR " + strings.Join(segments, " · ")
}

// Context tells an agent starting a session which trail it's on, how far along and
// what's next, from the cache alone. A session without a trail hears of the trail of its
// folder dir instead, and how to join it; without either it is empty.
func (a App) Context(key, dir string) string {
	if name := a.Sessions.Trail(key); name != "" {
		return fmt.Sprintf("cairn: this session is on trail %s, %s. Run `cairn` to see it; `cairn skills get cairn` has the guide.", name, a.summary(name))
	}
	name, at := a.Sessions.ResolveDir(dir, a.Head)
	if name == "" {
		return ""
	}
	return fmt.Sprintf("cairn: this folder's branch %s is on trail %s (%s). If you're continuing that work, run `cairn use %s`.", at.Branch, name, a.summary(name), name)
}

// summary is a trail's progress and next step, from the cache alone.
func (a App) summary(name string) string {
	steps, _ := a.loadSteps(name)
	finished, total, next := progress(a.withStates(steps))
	s := fmt.Sprintf("%d/%d steps done", finished, total)
	switch {
	case next == nil:
	case next.PR:
		s += ", next " + trail.Ref(next.Value)
	default:
		s += ", next " + strconv.Quote(trail.Clean(next.Value))
	}
	return s
}

// Progress is a trail's finished and total steps and its next step, as a short ref or
// its text, empty when all are done; from the cache alone.
func (a App) Progress(name string) (finished, total int, next string) {
	steps, _ := a.loadSteps(name)
	finished, total, n := progress(a.withStates(steps))
	switch {
	case n == nil:
	case n.PR:
		next = shortRef(trail.Ref(n.Value))
	default:
		next = trail.Clean(n.Value)
	}
	return finished, total, next
}

// refreshInBackground starts a refresh unless one for the session is running.
func (a App) refreshInBackground(key string, urls []string) {
	if !a.Cache.TryLock(key) {
		return
	}
	if a.Spawn(append([]string{"__refresh", key}, urls...)...) != nil {
		a.Cache.Unlock(key)
	}
}

// List is every trail with its progress and next step, most recently changed first.
func (a App) List() string {
	names := a.Sessions.Trails.Names()
	if len(names) == 0 {
		return "No trails yet. `cairn use <name>` starts one.\n"
	}
	mtimes := map[string]time.Time{}
	for _, name := range names {
		if fi, err := os.Stat(a.Sessions.Trails.File(name)); err == nil {
			mtimes[name] = fi.ModTime()
		}
	}
	slices.SortStableFunc(names, func(a, b string) int { return mtimes[b].Compare(mtimes[a]) })
	stepsOf := map[string][]step{}
	var all []string
	for _, name := range names {
		stepsOf[name], _ = a.loadSteps(name)
		for _, s := range stepsOf[name] {
			if s.PR {
				all = append(all, s.Value)
			}
		}
	}
	a.Cache.Refresh(false, all)

	attached := a.Sessions.Attached()
	var b strings.Builder
	for _, name := range names {
		finished, total, next := progress(a.withStates(stepsOf[name]))
		var desc string
		switch {
		case next == nil:
		case next.PR:
			desc = "▶ " + ansi.Truncate(shortRef(trail.Ref(next.Value))+" "+trail.Clean(next.state.Title), lsNextWidth, "")
		default:
			desc = "▶ " + ansi.Truncate(trail.Clean(next.Value), lsNextWidth, "")
		}
		suffix := ""
		if n := attached[name]; n > 0 {
			suffix = "  " + dim(fmt.Sprintf("%d session(s)", n))
		}
		fmt.Fprintf(&b, "\x1b[1m%-*s\x1b[0m %5s  %s%s\n", lsNameWidth, name, fmt.Sprintf("%d/%d", finished, total), desc, suffix)
	}
	return b.String()
}

// paneTitles asks the hosts for every pane's title at once.
func (a App) paneTitles(panes []string) map[string]string {
	titles := make([]string, len(panes))
	var wg sync.WaitGroup
	for i, pane := range panes {
		wg.Go(func() { titles[i] = trail.Clean(a.Title(pane)) })
	}
	wg.Wait()
	out := map[string]string{}
	for i, pane := range panes {
		out[pane] = titles[i]
	}
	return out
}
