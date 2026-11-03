// Package orca decorates the cards of Orca's worktrees with their trails, through the
// orca CLI: the trail's PR for the worktree's branch, its other PRs as references, and a
// note with its progress.
package orca

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Prefix starts every note cairn writes; a note without it is the user's and stays.
const Prefix = "cairn · "

const callTimeout = 5 * time.Second

// ErrNotInstalled is what Count says without an orca CLI.
var ErrNotInstalled = errors.New("not installed")

// Card is what a worktree's card should show of its trail.
type Card struct {
	// Repo is the owner/repo of the worktree's origin.
	Repo string
	// PR is the number of the trail's PR in Repo whose head is the worktree's branch, or 0.
	PR int
	// Refs are the URLs of the trail's other PRs.
	Refs []string
	Note string
}

// Note is a card's note for a trail with done of total steps finished, next being the
// next step's short ref or text, empty once all are done.
func Note(trail string, done, total int, next string) string {
	if next == "" {
		return Prefix + trail + " ✓ all done"
	}
	return fmt.Sprintf("%s%s %d/%d ▶ %s", Prefix, trail, done, total, next)
}

type worktree struct {
	Path     string `json:"path"`
	Comment  string `json:"comment"`
	LinkedPR int    `json:"linkedPR"`
}

type item struct {
	Type   string `json:"type"`
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Path is the orca CLI: on PATH, else inside Orca.app.
func Path() string {
	if p, err := exec.LookPath("orca"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		p := filepath.Join(dir, "Orca.app", "Contents", "Resources", "bin", "orca")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// Sync brings the card of every Orca worktree that card finds a trail for up to date:
// it links the PR, adds the references the card lacks, and rewrites a note that is empty
// or cairn's. Without Orca, or with its runtime down, it does nothing; errors are dropped.
func Sync(card func(path string) (Card, bool)) {
	bin := Path()
	if bin == "" {
		return
	}
	wts, err := list(bin)
	if err != nil {
		return
	}
	for _, w := range wts {
		if c, ok := card(w.Path); ok {
			syncOne(bin, w, c)
		}
	}
}

// Count is how many Orca worktrees card finds a trail for.
func Count(card func(path string) (Card, bool)) (int, error) {
	bin := Path()
	if bin == "" {
		return 0, ErrNotInstalled
	}
	wts, err := list(bin)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, w := range wts {
		if _, ok := card(w.Path); ok {
			n++
		}
	}
	return n, nil
}

func syncOne(bin string, w worktree, c Card) {
	sel := "path:" + w.Path
	var set []string
	if c.PR != 0 && c.PR != w.LinkedPR {
		set = append(set, "--pr", strconv.Itoa(c.PR))
	}
	if c.Note != w.Comment && (w.Comment == "" || strings.HasPrefix(w.Comment, Prefix)) {
		set = append(set, "--comment", c.Note)
	}
	if len(set) > 0 {
		if _, err := call(bin, slices.Concat([]string{"worktree", "set", "--worktree", sel}, set, []string{"--json"})...); err != nil {
			return
		}
	}
	if len(c.Refs) == 0 {
		return
	}
	var show struct {
		Result struct {
			Worktree struct {
				LinkedItems []item `json:"linkedItems"`
			} `json:"worktree"`
		} `json:"result"`
	}
	out, err := call(bin, "worktree", "show", "--worktree", sel, "--json")
	if err != nil || json.Unmarshal(out, &show) != nil {
		return
	}
	linked := show.Result.Worktree.LinkedItems
	missing := slices.DeleteFunc(slices.Clone(c.Refs), func(url string) bool {
		return slices.ContainsFunc(linked, func(it item) bool { return it.Type == "pr" && it.matches(url, c.Repo) })
	})
	if len(missing) > 0 {
		call(bin, slices.Concat([]string{"reference", "add"}, missing, []string{"--worktree", sel, "--json"})...)
	}
}

// matches reports whether a linked PR is the PR at url; one without a URL is in the
// worktree's own repo.
func (it item) matches(url, repo string) bool {
	if it.URL != "" {
		return it.URL == url
	}
	r, n, ok := strings.CutLast(strings.TrimPrefix(url, "https://github.com/"), "/pull/")
	return ok && strings.EqualFold(r, repo) && n == strconv.Itoa(it.Number)
}

// list is every Orca worktree, unless Orca's runtime can't be reached.
func list(bin string) ([]worktree, error) {
	var status struct {
		Result struct {
			Runtime struct {
				Reachable *bool `json:"reachable"`
			} `json:"runtime"`
		} `json:"result"`
	}
	out, err := call(bin, "status", "--json")
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(out, &status) == nil && status.Result.Runtime.Reachable != nil && !*status.Result.Runtime.Reachable {
		return nil, errors.New("not reachable")
	}
	var res struct {
		Result struct {
			Worktrees []worktree `json:"worktrees"`
		} `json:"result"`
	}
	if out, err = call(bin, "worktree", "list", "--json"); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res.Result.Worktrees, nil
}

func call(bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).Output()
}
