// Package github keeps what GitHub says about PRs: one batched GraphQL request for many
// PRs, a short-lived cache shared by every cairn process, and a CI summary per PR.
package github

import (
	"cmp"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// PR is a pull request's state as cached; CI is none, fail, pending or pass.
type PR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Draft  bool   `json:"isDraft"`
	Review string `json:"reviewDecision"`
	CI     string `json:"ci"`
	Title  string `json:"title"`
	Base   string `json:"baseRefName"`
	Head   string `json:"headRefName"`
}

// Finished reports whether the PR is merged or closed.
func (p PR) Finished() bool { return p.State == "MERGED" || p.State == "CLOSED" }

type check struct {
	Type       string `json:"__typename"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

var failing = []string{"FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"}

// ciOf sums up a PR's checks as none, fail, pending or pass; any failure wins over
// anything still running.
func ciOf(checks []check) string {
	if len(checks) == 0 {
		return "none"
	}
	if slices.ContainsFunc(checks, func(c check) bool {
		return slices.Contains(failing, cmp.Or(c.Conclusion, c.State))
	}) {
		return "fail"
	}
	if slices.ContainsFunc(checks, func(c check) bool {
		return c.Type == "CheckRun" && c.Status != "COMPLETED" || c.State == "PENDING" || c.State == "EXPECTED"
	}) {
		return "pending"
	}
	return "pass"
}

// Cache keeps each PR's state in Dir for TTL, looking stale ones up with Query.
type Cache struct {
	Dir   string
	TTL   time.Duration
	Query func(urls []string) (map[string]PR, error)
}

// file is prs/<owner>/<repo>/<n>.json under Dir.
func (c Cache) file(url string) string {
	repo, n, _ := strings.CutLast(strings.TrimPrefix(url, "https://github.com/"), "/pull/")
	owner, name, _ := strings.Cut(repo, "/")
	return filepath.Join(c.Dir, "prs", dirName(owner), dirName(name), n+".json")
}

// dirName keeps a URL's owner or repo inside the cache: no GitHub name is . or .., but a
// URL may say so.
func dirName(s string) string {
	if s == "." || s == ".." {
		return strings.ReplaceAll(s, ".", "_")
	}
	return s
}

// closedTTL is how long a closed PR stays fresh: it may be reopened, but seldom is.
const closedTTL = time.Hour

// Fresh reports whether the PR was fetched within TTL. A merged PR can't change and
// stays fresh; a closed one stays fresh for closedTTL.
func (c Cache) Fresh(url string) bool {
	fi, err := os.Stat(c.file(url))
	if err != nil {
		return false
	}
	age := time.Since(fi.ModTime())
	if age < c.TTL {
		return true
	}
	p, ok := c.Read(url)
	return ok && (p.State == "MERGED" || p.State == "CLOSED" && age < closedTTL)
}

// Read is the PR's cached state, if it has been fetched.
func (c Cache) Read(url string) (PR, bool) {
	var p PR
	b, err := os.ReadFile(c.file(url))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return PR{}, false
	}
	return p, true
}

// Info is a PR's cached state, or UNKNOWN until it has been fetched.
func (c Cache) Info(url string) PR {
	if p, ok := c.Read(url); ok {
		return p
	}
	_, n, _ := strings.CutLast(url, "/")
	num, _ := strconv.Atoi(n)
	return PR{Number: num, State: "UNKNOWN", CI: "none"}
}

// Unknown are the PRs among urls GitHub doesn't know. Those not cached yet are asked
// about in one request, and cached on the way.
func (c Cache) Unknown(urls ...string) []string {
	cached := func(url string) bool { _, ok := c.Read(url); return ok }
	ask := slices.DeleteFunc(slices.Clone(urls), cached)
	if len(ask) > 0 {
		c.Fetch(ask)
	}
	return slices.DeleteFunc(slices.Clone(ask), cached)
}

// Refresh fetches the PRs whose cache is stale, or all of them with force.
func (c Cache) Refresh(force bool, urls []string) {
	var stale []string
	for _, u := range urls {
		if (force || !c.Fresh(u)) && !slices.Contains(stale, u) {
			stale = append(stale, u)
		}
	}
	if len(stale) > 0 {
		c.Fetch(stale)
	}
}

// Fetch caches what GitHub says about each PR. A PR it doesn't know gets no cache file,
// which is how Unknown tells it doesn't exist.
func (c Cache) Fetch(urls []string) {
	got, err := c.Query(urls)
	if err != nil {
		return
	}
	for url, p := range got {
		b, err := json.Marshal(p)
		if err == nil && os.MkdirAll(filepath.Dir(c.file(url)), 0o755) == nil {
			writeAtomic(c.file(url), b)
		}
	}
}

const staleLockAge = 5 * time.Minute

func (c Cache) lock(key string) string { return filepath.Join(c.Dir, ".lock-"+key) }

// TryLock takes key's refresh lock unless another refresh holds it, so only one runs per
// session at a time; a stale lock is left over from a refresh that died.
func (c Cache) TryLock(key string) bool {
	lock := c.lock(key)
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return false
	}
	if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > staleLockAge {
		os.Remove(lock)
	}
	return os.Mkdir(lock, 0o755) == nil
}

// Unlock releases key's refresh lock.
func (c Cache) Unlock(key string) { os.Remove(c.lock(key)) }

func writeAtomic(file string, data []byte) error {
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

const prFields = `fragment F on PullRequest {
  number state isDraft reviewDecision title baseRefName headRefName url
  commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
    __typename
    ... on CheckRun { status conclusion }
    ... on StatusContext { state }
  } } } } } }
}`

type prNode struct {
	PR      `json:",inline"`
	URL     string `json:"url"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				Rollup *struct {
					Contexts struct {
						Nodes []check `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

const queryTimeout = 20 * time.Second

// Query looks up every PR in one GraphQL request, one aliased repository per PR. A PR
// GitHub doesn't know is missing from the result.
func Query(urls []string) (map[string]PR, error) {
	query, vars := buildQuery(urls)
	if len(vars) == 0 {
		return nil, nil
	}
	client, err := api.NewGraphQLClient(api.ClientOptions{Timeout: queryTimeout})
	if err != nil {
		return nil, err
	}
	var data map[string]*repoNode
	// A PR GitHub doesn't know comes back as a NOT_FOUND error beside the others' data.
	if err := client.Do(query, vars, &data); err != nil {
		gqlErr, ok := errors.AsType[*api.GraphQLError](err)
		if !ok || slices.ContainsFunc(gqlErr.Errors, func(e api.GraphQLErrorItem) bool { return e.Type != "NOT_FOUND" }) {
			return nil, err
		}
	}
	return parsePRs(urls, data), nil
}

func buildQuery(urls []string) (string, map[string]any) {
	var params, fields []string
	vars := map[string]any{}
	for i, url := range urls {
		repo, n, _ := strings.CutLast(strings.TrimPrefix(url, "https://github.com/"), "/pull/")
		owner, name, _ := strings.Cut(repo, "/")
		// GraphQL's Int is 32-bit, and one number past it would fail the whole request.
		num, err := strconv.ParseInt(n, 10, 32)
		if err != nil {
			continue
		}
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!, $p%d: Int!", i, i, i))
		fields = append(fields, fmt.Sprintf("p%d: repository(owner: $o%d, name: $n%d) { pullRequest(number: $p%d) { ...F } }", i, i, i, i))
		vars[fmt.Sprintf("o%d", i)] = owner
		vars[fmt.Sprintf("n%d", i)] = name
		vars[fmt.Sprintf("p%d", i)] = num
	}
	return fmt.Sprintf("query(%s) {\n%s\n}\n%s", strings.Join(params, ", "), strings.Join(fields, "\n"), prFields), vars
}

type repoNode struct {
	PullRequest *prNode `json:"pullRequest"`
}

func parsePRs(urls []string, data map[string]*repoNode) map[string]PR {
	got := map[string]PR{}
	for i, url := range urls {
		repo := data[fmt.Sprintf("p%d", i)]
		if repo == nil || repo.PullRequest == nil {
			continue
		}
		node := repo.PullRequest
		var checks []check
		if len(node.Commits.Nodes) > 0 && node.Commits.Nodes[0].Commit.Rollup != nil {
			checks = node.Commits.Nodes[0].Commit.Rollup.Contexts.Nodes
		}
		node.CI = ciOf(checks)
		got[url] = node.PR
	}
	return got
}
