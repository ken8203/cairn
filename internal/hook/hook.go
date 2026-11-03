// Package hook reads the payloads Claude Code and Codex hand their hooks, and tells its
// caller what to record when an agent's shell command touched a PR.
package hook

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Payload is what both agents send: the session, its cwd and the event, and for a
// PostToolUse of Bash the command and what it printed.
type Payload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Event     string `json:"hook_event_name"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	// ToolResponse is an object with stdout and stderr from Claude Code, a string from Codex.
	ToolResponse jsontext.Value `json:"tool_response"`
}

// Parse reads a payload.
func Parse(r io.Reader) (Payload, error) {
	var p Payload
	err := json.UnmarshalRead(r, &p)
	return p, err
}

// Output is what the tool printed, stdout then stderr.
func (p Payload) Output() string {
	var s string
	if json.Unmarshal(p.ToolResponse, &s) == nil {
		return s
	}
	var out struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	json.Unmarshal(p.ToolResponse, &out)
	return out.Stdout + "\n" + out.Stderr
}

// SessionStartOutput is what a SessionStart hook prints to give the agent context, the
// same for Claude Code and Codex.
func SessionStartOutput(context string) []byte {
	type specific struct {
		Event   string `json:"hookEventName"`
		Context string `json:"additionalContext"`
	}
	b, _ := json.Marshal(struct {
		Specific specific `json:"hookSpecificOutput"`
	}{specific{"SessionStart", context}})
	return append(b, '\n')
}

var urlPattern = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+`)

// Records are the `cairn record` arguments for each PR a Bash command touched, once each:
// the PR it opened (the last PR URL it printed), the branch it pushed, and the targets of
// gh pr commands. Directories are absolute, resolved against the payload's cwd.
func Records(p Payload) [][]string {
	command := p.ToolInput.Command
	if p.ToolName != "Bash" || !filepath.IsAbs(p.Cwd) || !strings.Contains(command, "gh") && !strings.Contains(command, "git") {
		return nil
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	w := walker{payload: p}
	w.stmts(f.Stmts, p.Cwd)
	return w.records
}

type walker struct {
	payload Payload
	records [][]string
}

func (w *walker) add(args ...string) {
	if !slices.ContainsFunc(w.records, func(r []string) bool { return slices.Equal(r, args) }) {
		w.records = append(w.records, args)
	}
}

// stmts walks statements in order, carrying the dir a cd moves to; an empty dir is one
// cairn can't know, where nothing is recorded.
func (w *walker) stmts(ss []*syntax.Stmt, dir string) string {
	for _, s := range ss {
		dir = w.stmt(s, dir)
	}
	return dir
}

func (w *walker) stmt(s *syntax.Stmt, dir string) string {
	switch c := s.Cmd.(type) {
	case *syntax.CallExpr:
		return w.call(c, dir)
	case *syntax.BinaryCmd:
		// Each side of a pipe runs in a subshell.
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			w.stmt(c.X, dir)
			w.stmt(c.Y, dir)
			return dir
		}
		return w.stmt(c.Y, w.stmt(c.X, dir))
	case *syntax.Subshell:
		w.stmts(c.Stmts, dir)
		return dir
	case nil:
		return dir
	}
	// Blocks, loops, conditionals, case and functions: a cd in one may run once, many
	// times or never, so where anything after it runs is unknown.
	if changesDir(s.Cmd) {
		dir = ""
	}
	syntax.Walk(s.Cmd, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			w.call(call, dir)
		}
		return true
	})
	return dir
}

// call records what one simple command touched and returns the dir after it.
func (w *walker) call(c *syntax.CallExpr, dir string) string {
	if len(c.Args) == 0 {
		return dir
	}
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = literal(a)
	}
	switch filepath.Base(args[0]) {
	case "cd":
		return cd(dir, args[1:])
	case "pushd", "popd":
		return ""
	case "git":
		if at, push := gitPush(dir, args[1:]); push && at != "" {
			w.add("--own", "-C", at)
		}
	case "gh":
		if len(args) < 3 || args[1] != "pr" {
			break
		}
		switch args[2] {
		case "create", "new":
			urls := urlPattern.FindAllString(w.payload.Output(), -1)
			if len(urls) == 0 {
				break
			}
			// The dir only tells record where the PR was opened from.
			if dir != "" {
				w.add("--own", "-C", dir, urls[len(urls)-1])
			} else {
				w.add("--own", urls[len(urls)-1])
			}
		case "checkout", "co", "comment", "review", "edit", "merge", "ready", "close", "reopen", "update-branch":
			if dir == "" {
				break
			}
			// Without a number or URL, gh means the current branch's PR, and so does record.
			w.add(append([]string{"-C", dir}, ghTarget(args[3:], envRepo(c.Assigns))...)...)
		}
	}
	return dir
}

// changesDir reports whether a cd, pushd or popd runs anywhere in n.
func changesDir(n syntax.Node) bool {
	found := false
	syntax.Walk(n, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 {
			switch filepath.Base(literal(c.Args[0])) {
			case "cd", "pushd", "popd":
				found = true
			}
		}
		return !found
	})
	return found
}

// cd is the dir `cd args` moves to from dir.
func cd(dir string, args []string) string {
	for len(args) > 0 && slices.Contains([]string{"-P", "-L", "-e", "-@"}, args[0]) {
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	} else if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return ""
	}
	if len(args) == 0 {
		home, _ := os.UserHomeDir()
		return home
	}
	return resolve(dir, args[0])
}

// envRepo is the GH_REPO a command sets for itself, as in GH_REPO=o/r gh pr merge 3.
func envRepo(assigns []*syntax.Assign) string {
	for _, a := range assigns {
		if a.Name != nil && a.Name.Value == "GH_REPO" && a.Value != nil {
			return literal(a.Value)
		}
	}
	return ""
}

// unknown stands for a word that isn't plain text, such as $x or $(cmd).
const unknown = "\x00"

// literal is a word's text once quotes are removed, or unknown.
func literal(w *syntax.Word) string {
	var b strings.Builder
	var parts func(ps []syntax.WordPart) bool
	parts = func(ps []syntax.WordPart) bool {
		for _, p := range ps {
			switch p := p.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
			case *syntax.DblQuoted:
				if !parts(p.Parts) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !parts(w.Parts) {
		return unknown
	}
	return b.String()
}

// resolve is path seen from dir, or empty when either can't be known.
func resolve(dir, path string) string {
	if path == "" || path == "-" || strings.Contains(path, unknown) {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = home + path[1:]
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, path)
}

// gitPush finds git's subcommand past the global options, and the dir its -C options lead to.
func gitPush(dir string, args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "-C" || arg == "-c") && i+1 < len(args):
			i++
			if arg == "-C" {
				dir = resolve(dir, args[i])
			}
		case strings.HasPrefix(arg, "-"):
		default:
			return dir, arg == "push"
		}
	}
	return dir, false
}

// ghBool are the flags of gh's PR-touching commands that take no value; any other flag
// written apart from its value takes the next argument.
var ghBool = []string{
	"-a", "-c", "-d", "-e", "-f", "-h", "-m", "-r", "-s", "-w",
	"--admin", "--approve", "--auto", "--comment", "--create-if-none", "--delete-branch",
	"--delete-last", "--detach", "--disable-auto", "--edit-last", "--editor", "--force",
	"--help", "--merge", "--rebase", "--recurse-submodules", "--remove-milestone",
	"--request-changes", "--squash", "--undo", "--web", "--yes",
}

// ghTarget turns a gh pr command's -R repo (else the GH_REPO it sets) and PR number or URL
// into record's arguments.
func ghTarget(args []string, repo string) []string {
	var target string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-R" || arg == "--repo":
			if i+1 < len(args) {
				i++
				repo = args[i]
			}
		case strings.HasPrefix(arg, "--repo="):
			repo = strings.TrimPrefix(arg, "--repo=")
		case strings.HasPrefix(arg, "-"):
			if !strings.Contains(arg, "=") && !slices.Contains(ghBool, arg) {
				i++
			}
		case target != "":
		case startsWithURL(arg):
			target = arg
		case isNumber(strings.TrimPrefix(arg, "#")):
			target = strings.TrimPrefix(arg, "#")
		}
	}
	var out []string
	if repo != "" && repo != unknown {
		out = append(out, "-R", repo)
	}
	if target != "" {
		out = append(out, target)
	}
	return out
}

func startsWithURL(s string) bool {
	loc := urlPattern.FindStringIndex(s)
	return loc != nil && loc[0] == 0
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}
