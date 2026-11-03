package github

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCI(t *testing.T) {
	run := func(status, conclusion string) check {
		return check{Type: "CheckRun", Status: status, Conclusion: conclusion}
	}
	ctx := func(state string) check { return check{Type: "StatusContext", State: state} }
	tests := []struct {
		name   string
		checks []check
		want   string
	}{
		{"no checks", nil, "none"},
		{"all passed", []check{run("COMPLETED", "SUCCESS"), ctx("SUCCESS"), run("COMPLETED", "SKIPPED")}, "pass"},
		{"failure beats pending", []check{run("IN_PROGRESS", ""), run("COMPLETED", "FAILURE")}, "fail"},
		{"status error", []check{ctx("ERROR")}, "fail"},
		{"timed out", []check{run("COMPLETED", "TIMED_OUT")}, "fail"},
		{"cancelled", []check{run("COMPLETED", "CANCELLED")}, "fail"},
		{"action required", []check{run("COMPLETED", "ACTION_REQUIRED")}, "fail"},
		{"startup failure", []check{run("COMPLETED", "STARTUP_FAILURE")}, "fail"},
		{"check run running", []check{run("QUEUED", ""), ctx("SUCCESS")}, "pending"},
		{"status pending", []check{ctx("PENDING")}, "pending"},
		{"status expected", []check{ctx("EXPECTED")}, "pending"},
	}
	for _, tt := range tests {
		if got := ciOf(tt.checks); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestParsePRs(t *testing.T) {
	body := `{"p0": {"pullRequest": {"number": 5, "state": "OPEN", "isDraft": true, "reviewDecision": null,
		"title": "T", "baseRefName": "main", "headRefName": "f", "url": "https://github.com/o/r/pull/5",
		"commits": {"nodes": [{"commit": {"statusCheckRollup": {"contexts": {"nodes": [
			{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "FAILURE"}]}}}}]}}},
		"p1": {"pullRequest": null},
		"p2": null,
		"p3": {"pullRequest": {"number": 6, "state": "MERGED", "isDraft": false, "reviewDecision": "APPROVED",
		"title": "U", "baseRefName": "main", "headRefName": "g", "url": "https://github.com/o/r/pull/6",
		"commits": {"nodes": [{"commit": {"statusCheckRollup": null}}]}}}}`
	var data map[string]*repoNode
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	urls := []string{"https://github.com/o/r/pull/5", "https://github.com/o/r/pull/404", "https://github.com/x/gone/pull/1", "https://github.com/o/r/pull/6"}
	got := parsePRs(urls, data)
	if len(got) != 2 {
		t.Fatalf("got %d PRs: %+v", len(got), got)
	}
	want5 := PR{Number: 5, State: "OPEN", Draft: true, CI: "fail", Title: "T", Base: "main", Head: "f"}
	if got[urls[0]] != want5 {
		t.Errorf("#5 = %+v", got[urls[0]])
	}
	want6 := PR{Number: 6, State: "MERGED", Review: "APPROVED", CI: "none", Title: "U", Base: "main", Head: "g"}
	if got[urls[3]] != want6 {
		t.Errorf("#6 = %+v", got[urls[3]])
	}
}

func TestBuildQuery(t *testing.T) {
	q, vars := buildQuery([]string{"https://github.com/o/r/pull/5", "https://github.com/a/b/pull/7"})
	if vars["o1"] != "a" || vars["n1"] != "b" || vars["p1"] != int64(7) || vars["p0"] != int64(5) {
		t.Errorf("vars = %v", vars)
	}
	for _, s := range []string{"p0: repository(owner: $o0, name: $n0)", "p1: repository(owner: $o1, name: $n1)", "fragment F on PullRequest"} {
		if !strings.Contains(q, s) {
			t.Errorf("query lacks %q:\n%s", s, q)
		}
	}
}

func TestBuildQuerySkipsOutOfRangeNumbers(t *testing.T) {
	q, vars := buildQuery([]string{"https://github.com/o/r/pull/3000000000", "https://github.com/o/r/pull/2147483647"})
	if _, ok := vars["p0"]; ok || strings.Contains(q, "p0:") {
		t.Errorf("out-of-range PR queried:\n%s", q)
	}
	if vars["p1"] != int64(2147483647) {
		t.Errorf("vars = %v", vars)
	}
	if q, vars := buildQuery([]string{"https://github.com/o/r/pull/99999999999"}); len(vars) != 0 {
		t.Errorf("vars = %v for %s", vars, q)
	}
}

func TestRefreshFetchesOnlyStale(t *testing.T) {
	fresh, stale := "https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"
	var asked [][]string
	c := Cache{Dir: t.TempDir(), TTL: time.Minute, Query: func(urls []string) (map[string]PR, error) {
		asked = append(asked, urls)
		return map[string]PR{stale: {Number: 2, State: "MERGED"}}, nil
	}}
	writeCache(t, c, fresh, PR{Number: 1, State: "OPEN"})
	writeCache(t, c, stale, PR{Number: 2, State: "OPEN"})
	old := time.Now().Add(-2 * c.TTL)
	os.Chtimes(c.file(stale), old, old)

	c.Refresh(false, []string{fresh, stale, stale})
	if len(asked) != 1 || len(asked[0]) != 1 || asked[0][0] != stale {
		t.Fatalf("asked %v", asked)
	}
	if c.Info(stale).State != "MERGED" {
		t.Error("stale PR not updated")
	}
	c.Refresh(true, []string{fresh, stale})
	if len(asked) != 2 || len(asked[1]) != 2 {
		t.Errorf("force asked %v", asked)
	}
}

func TestInfoUnknown(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	if got, want := c.Info("https://github.com/o/r/pull/42"), (PR{Number: 42, State: "UNKNOWN", CI: "none"}); got != want {
		t.Errorf("got %+v", got)
	}
}

func TestExists(t *testing.T) {
	known := "https://github.com/o/r/pull/1"
	c := Cache{Dir: t.TempDir(), TTL: time.Minute, Query: func(urls []string) (map[string]PR, error) {
		got := map[string]PR{}
		if slices.Contains(urls, known) {
			got[known] = PR{Number: 1, State: "OPEN"}
		}
		return got, nil
	}}
	if !c.Exists(known) {
		t.Error("known PR missing")
	}
	if _, ok := c.Read(known); !ok {
		t.Error("known PR not cached")
	}
	unknown := "https://github.com/o/r/pull/404"
	if c.Exists(unknown) {
		t.Error("unknown PR exists")
	}
	if _, err := os.Stat(c.file(unknown)); err == nil {
		t.Error("unknown PR got a cache file")
	}
}

func TestCacheFile(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	for url, want := range map[string]string{
		"https://github.com/o/r/pull/5":    "prs/o/r/5.json",
		"https://github.com/../../pull/5":  "prs/__/__/5.json",
		"https://github.com/o/../pull/5":   "prs/o/__/5.json",
		"https://github.com/o/r.go/pull/5": "prs/o/r.go/5.json",
	} {
		if got := c.file(url); got != filepath.Join(c.Dir, want) {
			t.Errorf("file(%q) = %q", url, got)
		}
	}
}

func TestTryLock(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	if !c.TryLock("s1") {
		t.Fatal("first lock refused")
	}
	if c.TryLock("s1") {
		t.Error("second lock taken")
	}
	old := time.Now().Add(-2 * staleLockAge)
	os.Chtimes(c.lock("s1"), old, old)
	if !c.TryLock("s1") {
		t.Error("stale lock kept")
	}
	c.Unlock("s1")
	if !c.TryLock("s1") {
		t.Error("lock not released")
	}
}

func writeCache(t *testing.T, c Cache, url string, p PR) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(c.file(url)), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	if err := os.WriteFile(c.file(url), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
