package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowExplicitPane(t *testing.T) {
	app := testApp(t, nil)
	repo := folderRepo(t, app)
	t.Chdir(repo)
	t.Setenv("CODEX_THREAD_ID", "caller")
	// Any accidental caller lookup would ask this unavailable CLI.
	t.Setenv("HC_TERMINAL_ID", "t99")
	app.Sessions.Bind("caller", "mine")
	app.Sessions.Bind("other", "theirs")
	app.Sessions.NotePane("other", "hc-t1.core")
	app.Sessions.NotePane("older", "caller-pane")
	snapshot := func() string {
		var result strings.Builder
		for _, sub := range []string{"panes", "sessions"} {
			filepath.WalkDir(filepath.Join(app.Sessions.Dir, sub), func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					b, _ := os.ReadFile(path)
					result.WriteString(path + string(b))
				}
				return nil
			})
		}
		return result.String()
	}
	before := snapshot()
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"show", "--pane", "hc-t1.core", "--json"}, `"name":"theirs"`},
		{[]string{"show", "--pane", "absent", "--json"}, `{"trail":null,"prs":[]}`},
		{[]string{"show", "--pane", "absent", "--json", "--dir", repo}, `"via":"dir"`},
		{[]string{"show", "--pane", "hc-t1.core"}, "theirs"},
	} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		saved := os.Stdout
		os.Stdout = w
		err = run(app, tt.args)
		os.Stdout = saved
		w.Close()
		out, _ := io.ReadAll(r)
		r.Close()
		if err != nil || !strings.Contains(string(out), tt.want) {
			t.Errorf("%v: %s, %v", tt.args, out, err)
		}
	}
	if after := snapshot(); after != before {
		t.Fatal("explicit reads changed sessions or pane pointers")
	}
}

func TestShowInvalidPane(t *testing.T) {
	app := testApp(t, nil)
	for _, args := range [][]string{
		{"show", "--pane"}, {"show", "--pane", ""}, {"show", "--pane", "--json"},
		{"show", "--pane", "../other"}, {"show", "--pane", "hc-t1."},
		{"show", "--pane=foo"}, {"show", "--pane", "p1", "--pane", "p2"},
		{"show", "--pane", "p1", "--dir"},
	} {
		if err := run(app, args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
