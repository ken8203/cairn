// Package trail reads and edits trail files:
//
//	# name
//
//	- [ ] owner/repo#123        PR step: done once GitHub says merged or closed
//	- [ ] release api 5.7       free-text step: ticked by hand
//
//	## Notes
//
//	- 11-02 free text
package trail

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
)

var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	refPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]+`)
	urlPattern  = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+`)
	stepLine    = regexp.MustCompile(`^- \[([ xX])\] (.*)$`)
	datedNote   = regexp.MustCompile(`^[0-9]{2}-[0-9]{2}( |$)`)
)

// ValidName reports whether s is safe as a trail or file name.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// Clean drops control characters: text from GitHub or trail files reaches the terminal.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// Ref turns a PR URL into owner/repo#n.
func Ref(url string) string {
	rest := strings.TrimPrefix(url, "https://github.com/")
	repo, n, _ := strings.CutLast(rest, "/pull/")
	return repo + "#" + n
}

// URL turns owner/repo#n into a PR URL.
func URL(ref string) string {
	repo, n, _ := strings.CutLast(ref, "#")
	return "https://github.com/" + repo + "/pull/" + n
}

// LeadingURL is the PR URL s starts with, if any.
func LeadingURL(s string) string {
	if loc := urlPattern.FindStringIndex(s); loc != nil && loc[0] == 0 {
		return s[:loc[1]]
	}
	return ""
}

// ParseRef reads owner/repo#n, or a PR URL as one, from the start of s.
func ParseRef(s string) string {
	if u := LeadingURL(s); u != "" {
		return Ref(u)
	}
	return refPattern.FindString(s)
}

// Store is the directory of trail files.
type Store struct {
	Dir string
}

// File is the path of a trail's file.
func (s Store) File(name string) string { return filepath.Join(s.Dir, name+".md") }

// Names lists every trail.
func (s Store) Names() []string {
	files, _ := filepath.Glob(filepath.Join(s.Dir, "*.md"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".md"))
	}
	return out
}

// readLines splits a file into lines without its trailing newlines; blank lines in
// between are kept.
func readLines(file string) ([]string, error) {
	b, err := os.ReadFile(file)
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n"), err
}

// Create creates a trail unless one by that name exists, reporting whether it did.
func (s Store) Create(name string) (bool, error) {
	if !ValidName(name) {
		return false, errors.New("trail names use letters, digits, '.', '_' and '-'")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(s.File(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintf(f, "# %s\n\n## Notes\n", name)
	return err == nil, errors.Join(err, f.Close())
}

// lock takes an advisory lock on a sibling of the trail, since the trail itself is
// replaced on every write.
func (s Store) lock(name string) (unlock func(), err error) {
	return Lock(filepath.Join(s.Dir, "."+name+".lock"))
}

// Lock takes an exclusive advisory lock on file, creating it if needed.
func Lock(file string) (unlock func(), err error) {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// update rewrites an existing trail's lines under its lock, replacing the file
// atomically.
func (s Store) update(name string, edit func([]string) ([]string, error)) error {
	unlock, err := s.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	lines, err := readLines(s.File(name))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: no such trail", name)
	}
	if err != nil {
		return err
	}
	if lines, err = edit(lines); err != nil {
		return err
	}
	return WriteAtomic(s.File(name), []byte(strings.Join(lines, "\n")+"\n"))
}

// WriteAtomic replaces file with data through a rename, so no reader sees it half
// written.
func WriteAtomic(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Chmod(0o644), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), file)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Step is one checklist line of a trail.
type Step struct {
	PR     bool
	Ticked bool
	Value  string // URL for PR steps, text otherwise
}

func parseStep(m []string) Step {
	if ref := refPattern.FindString(m[2]); ref != "" {
		return Step{PR: true, Value: URL(ref)}
	}
	return Step{Ticked: m[1] != " ", Value: m[2]}
}

func isNotes(line string) bool { return strings.HasPrefix(line, "## Notes") }

// stepLines yields the index of each step line ahead of the notes, with its mark and text.
func stepLines(lines []string) iter.Seq2[int, []string] {
	return func(yield func(int, []string) bool) {
		for i, line := range lines {
			if isNotes(line) {
				return
			}
			if m := stepLine.FindStringSubmatch(line); m != nil && !yield(i, m) {
				return
			}
		}
	}
}

// Load reads a trail's steps and its notes, cleaned for the terminal.
func (s Store) Load(name string) (steps []Step, notes []string) {
	lines, _ := readLines(s.File(name))
	for _, m := range stepLines(lines) {
		steps = append(steps, parseStep(m))
	}
	if at := slices.IndexFunc(lines, isNotes); at >= 0 {
		for _, line := range lines[at+1:] {
			if strings.TrimSpace(line) != "" {
				notes = append(notes, Clean(line))
			}
		}
	}
	return steps, notes
}

// URLs are the trail's PR steps.
func (s Store) URLs(name string) []string {
	steps, _ := s.Load(name)
	var out []string
	for _, st := range steps {
		if st.PR {
			out = append(out, st.Value)
		}
	}
	return out
}

// Add creates the trail if needed and inserts steps after the last step, or with next
// ahead of the first unticked free-text step, behind the PRs already there so they stay
// in the order they were opened; always ahead of the notes. Each step is owner/repo#n, a
// PR URL (written as its ref) or free text.
func (s Store) Add(name string, next bool, steps ...string) error {
	if _, err := s.Create(name); err != nil {
		return err
	}
	var add []string
	for _, st := range steps {
		st = Clean(st)
		if u := LeadingURL(st); u != "" {
			st = Ref(u)
		}
		if st != "" {
			add = append(add, "- [ ] "+st)
		}
	}
	return s.update(name, func(lines []string) ([]string, error) {
		insertAt, before := -1, -1
		notesAt := slices.IndexFunc(lines, isNotes)
		for i, m := range stepLines(lines) {
			if st := parseStep(m); next && !st.PR && !st.Ticked {
				before = i
				break
			}
			insertAt = i
		}
		switch {
		case before >= 0:
			return slices.Insert(lines, before, add...), nil
		case insertAt >= 0:
			return slices.Insert(lines, insertAt+1, add...), nil
		case notesAt >= 0:
			// With no steps yet, the new ones replace whatever sits between the title and
			// the notes.
			out := append([]string{lines[0], ""}, add...)
			out = append(out, "")
			return append(out, lines[notesAt:]...), nil
		default:
			return append(append(lines, ""), add...), nil
		}
	})
}

// Tick ticks, or unticks, free-text step target.
func (s Store) Tick(name string, target int) error {
	return s.update(name, func(lines []string) ([]string, error) {
		n := 0
		for i, m := range stepLines(lines) {
			if n++; n != target {
				continue
			}
			if refPattern.MatchString(m[2]) {
				return nil, fmt.Errorf("step %d is a PR; it ticks itself once merged", target)
			}
			if m[1] == " " {
				lines[i] = "- [x] " + m[2]
			} else {
				lines[i] = "- [ ] " + m[2]
			}
			return lines, nil
		}
		return nil, fmt.Errorf("no step %d", target)
	})
}

// Link turns free-text step target into a step for ref and drops that PR's other entry.
func (s Store) Link(name string, target int, ref string) error {
	return s.update(name, func(lines []string) ([]string, error) {
		n, at, text := 0, -1, ""
		var dupes []int
		for i, m := range stepLines(lines) {
			if n++; n == target {
				at, text = i, m[2]
				continue
			}
			if refPattern.FindString(m[2]) == ref {
				dupes = append(dupes, i)
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("no step %d", target)
		}
		if refPattern.MatchString(text) {
			return nil, fmt.Errorf("step %d is already a PR", target)
		}
		lines[at] = "- [ ] " + ref
		for _, i := range slices.Backward(dupes) {
			lines = slices.Delete(lines, i, i+1)
		}
		return lines, nil
	})
}

// Note appends a dated note; a date the text already starts with is kept.
func (s Store) Note(name, text string) error {
	text = Clean(text)
	if !datedNote.MatchString(text) {
		text = time.Now().Format("01-02") + " " + text
	}
	unlock, err := s.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(s.File(name), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "- %s\n", text)
	return errors.Join(err, f.Close())
}
