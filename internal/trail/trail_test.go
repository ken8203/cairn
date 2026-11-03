package trail

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTrail(t *testing.T, s Store, name, content string) {
	t.Helper()
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.File(name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTrail(t *testing.T, s Store, name string) string {
	t.Helper()
	b, err := os.ReadFile(s.File(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const sample = `# t

- [ ] o/r#1
- [x] release
- [ ] flag on

## Notes

- 11-02 hi
`

func TestAdd(t *testing.T) {
	tests := []struct {
		name, before string
		next         bool
		args         []string
		want         string
	}{
		{"at the end", sample, false, []string{"deploy"},
			"# t\n\n- [ ] o/r#1\n- [x] release\n- [ ] flag on\n- [ ] deploy\n\n## Notes\n\n- 11-02 hi\n"},
		{"next ahead of the first unfinished step", sample, true, []string{"o/r#9"},
			"# t\n\n- [ ] o/r#1\n- [x] release\n- [ ] o/r#9\n- [ ] flag on\n\n## Notes\n\n- 11-02 hi\n"},
		{"next behind an open PR, ahead of planned work", "# t\n\n- [ ] o/r#9\n- [ ] plan\n", true, []string{"o/r#10"},
			"# t\n\n- [ ] o/r#9\n- [ ] o/r#10\n- [ ] plan\n"},
		{"next with no planned work at the end", "# t\n\n- [ ] o/r#9\n", true, []string{"o/r#10"},
			"# t\n\n- [ ] o/r#9\n- [ ] o/r#10\n"},
		{"before notes", "# t\n\n## Notes\n\n- 11-02 hi\n", false, []string{"a", "b"},
			"# t\n\n- [ ] a\n- [ ] b\n\n## Notes\n\n- 11-02 hi\n"},
		{"no steps and no notes", "# t\n", false, []string{"a"},
			"# t\n\n- [ ] a\n"},
		{"URL as a ref, control characters stripped", sample, false, []string{"https://github.com/o/r/pull/9/files", "be\x07ll\x1b"},
			"# t\n\n- [ ] o/r#1\n- [x] release\n- [ ] flag on\n- [ ] o/r#9\n- [ ] bell\n\n## Notes\n\n- 11-02 hi\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			writeTrail(t, s, "t", tt.before)
			if err := s.Add("t", tt.next, tt.args...); err != nil {
				t.Fatal(err)
			}
			if got := readTrail(t, s, "t"); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestAddCreatesTrail(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Add("new", false, "first"); err != nil {
		t.Fatal(err)
	}
	if got, want := readTrail(t, s, "new"), "# new\n\n- [ ] first\n\n## Notes\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAddConcurrently(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	writeTrail(t, s, "t", sample)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := s.Add("t", false, fmt.Sprintf("step %d", i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got := readTrail(t, s, "t")
	for i := range 20 {
		if !strings.Contains(got, fmt.Sprintf("- [ ] step %d\n", i)) {
			t.Errorf("step %d lost", i)
		}
	}
}

func TestTick(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	writeTrail(t, s, "t", sample)
	if err := s.Tick("t", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick("t", 2); err != nil {
		t.Fatal(err)
	}
	want := "# t\n\n- [ ] o/r#1\n- [ ] release\n- [x] flag on\n\n## Notes\n\n- 11-02 hi\n"
	if got := readTrail(t, s, "t"); got != want {
		t.Errorf("got\n%s", got)
	}
	if err := s.Tick("t", 1); err == nil || err.Error() != "step 1 is a PR; it ticks itself once merged" {
		t.Errorf("ticking a PR: %v", err)
	}
	if err := s.Tick("t", 4); err == nil || err.Error() != "no step 4" {
		t.Errorf("missing step: %v", err)
	}
}

func TestLink(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	writeTrail(t, s, "t", "# t\n\n- [x] open the PR\n- [ ] release\n- [ ] o/r#7\n\n## Notes\n")
	if err := s.Link("t", 1, "o/r#7"); err != nil {
		t.Fatal(err)
	}
	if got, want := readTrail(t, s, "t"), "# t\n\n- [ ] o/r#7\n- [ ] release\n\n## Notes\n"; got != want {
		t.Errorf("got\n%s", got)
	}
	if err := s.Link("t", 1, "o/r#8"); err == nil || err.Error() != "step 1 is already a PR" {
		t.Errorf("relinking: %v", err)
	}
	if err := s.Link("t", 5, "o/r#8"); err == nil || err.Error() != "no step 5" {
		t.Errorf("missing step: %v", err)
	}
}

func TestMissingTrail(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for name, err := range map[string]error{"tick": s.Tick("t", 1), "link": s.Link("t", 1, "o/r#9")} {
		if err == nil || err.Error() != "t: no such trail" {
			t.Errorf("%s on a missing trail: %v", name, err)
		}
	}
	if _, err := os.Stat(s.File("t")); err == nil {
		t.Error("created the trail")
	}
}

func TestNote(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	writeTrail(t, s, "t", "# t\n\n## Notes\n")
	for _, n := range []string{"2026-11-05 dated", "11-05 old", "12-01", "plain\x1b[31m", "1-2 not a date", "26-11-05 short year"} {
		if err := s.Note("t", n); err != nil {
			t.Fatal(err)
		}
	}
	today := time.Now().Format(time.DateOnly)
	want := "# t\n\n## Notes\n- 2026-11-05 dated\n- 11-05 old\n- 12-01\n- " + today + " plain[31m\n- " + today + " 1-2 not a date\n- " + today + " 26-11-05 short year\n"
	if got := readTrail(t, s, "t"); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestStepsAndNotes(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	writeTrail(t, s, "t", "# t\n\n- [ ] o/r#1 with words\n- [X] done\nnot a step\n- [ ] \x07bell\n\n## Notes\n\n- a\x07\n  \n- b\n")
	want := []Step{
		{PR: true, Value: "https://github.com/o/r/pull/1"},
		{Ticked: true, Value: "done"},
		{Value: "\x07bell"},
	}
	steps, notes := s.Load("t")
	if !slices.Equal(steps, want) {
		t.Errorf("steps = %+v", steps)
	}
	if !slices.Equal(notes, []string{"- a", "- b"}) {
		t.Errorf("notes = %q", notes)
	}
}

func TestRefs(t *testing.T) {
	for in, want := range map[string]string{
		"o/r#12":                                 "o/r#12",
		"o.x/r-y_z#3 trailing":                   "o.x/r-y_z#3",
		"https://github.com/o/r/pull/5":          "o/r#5",
		"https://github.com/o/r/pull/5/files":    "o/r#5",
		"https://gitlab.com/o/r/pull/5":          "",
		"#12":                                    "",
		"o/r":                                    "",
		"release api":                            "",
		"https://github.com/o/r/issues/5":        "",
		"https://github.com/o/r/pull/notanumber": "",
	} {
		if got := ParseRef(in); got != want {
			t.Errorf("ParseRef(%q) = %q, want %q", in, got, want)
		}
	}
	if got := URL("o/r#5"); got != "https://github.com/o/r/pull/5" {
		t.Errorf("URL = %q", got)
	}
	if got := Ref("https://github.com/o/r/pull/5"); got != "o/r#5" {
		t.Errorf("Ref = %q", got)
	}
}
