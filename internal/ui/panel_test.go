package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ken8203/cairn/internal/github"
)

func newPanel(t *testing.T, stepCount, height int) *panel {
	t.Helper()
	a := newApp(t, nil)
	var b strings.Builder
	b.WriteString("# t\n\n")
	for i := range stepCount {
		fmt.Fprintf(&b, "- [ ] step %d\n", i+1)
	}
	b.WriteString("\n## Notes\n")
	writeTrail(t, a, "t", b.String())
	if err := errors.Join(a.Sessions.Bind("s1", "t"), a.Sessions.NotePane("s1", "p1")); err != nil {
		t.Fatal(err)
	}
	m := &panel{app: a, target: "p1", title: "my pane", v: &view{}}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: height})
	m.Update(m.refresh(false)())
	return m
}

func press(m *panel, k string) tea.Cmd {
	var key tea.Key
	switch k {
	case "up":
		key.Code = tea.KeyUp
	case "down":
		key.Code = tea.KeyDown
	default:
		key.Code, key.Text = rune(k[0]), k
	}
	_, cmd := m.Update(tea.KeyPressMsg(key))
	return cmd
}

func lines(m *panel) []string { return strings.Split(m.View().Content, "\n") }

func TestPanelLayout(t *testing.T) {
	m := newPanel(t, 3, 20)
	got := lines(m)
	if !strings.HasPrefix(got[0], "\x1b[2mmy pane · ") || got[1] != "" {
		t.Errorf("heading = %q, %q", got[0], got[1])
	}
	// The trail's heading, a blank and three steps, then a blank and the footer.
	if len(got) != 2+5+2 {
		t.Fatalf("got %d lines:\n%s", len(got), strings.Join(got, "\n"))
	}
	if want := dim(keysHelp); got[len(got)-1] != want {
		t.Errorf("footer = %q", got[len(got)-1])
	}
	v := m.View()
	if !v.AltScreen || v.Cursor != nil || v.MouseMode != tea.MouseModeNone {
		t.Errorf("view modes: alt %v cursor %v mouse %v", v.AltScreen, v.Cursor, v.MouseMode)
	}
}

func TestPanelScroll(t *testing.T) {
	m := newPanel(t, 12, 10) // 14 lines in a room of 6
	if got := lines(m); len(got) != 10 || !strings.Contains(got[9], "1-6/14 · j/k scroll · ") {
		t.Fatalf("frame:\n%s", strings.Join(got, "\n"))
	}
	for _, k := range []string{"j", "down", "j"} {
		press(m, k)
	}
	if m.top != 3 {
		t.Errorf("top = %d after scrolling down three", m.top)
	}
	for range 20 {
		press(m, "j")
	}
	if m.top != 8 {
		t.Errorf("top = %d, want it held at 8", m.top)
	}
	if got := lines(m); !strings.Contains(got[len(got)-1], "9-14/14") || !strings.Contains(got[len(got)-3], "step 12") {
		t.Errorf("bottom frame:\n%s", strings.Join(got, "\n"))
	}
	press(m, "k")
	press(m, "k")
	press(m, "up")
	if m.top != 5 {
		t.Errorf("top = %d after scrolling up three", m.top)
	}

	// Growing the window redraws and pulls the frame back into range.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	if m.top != 0 || strings.Contains(lines(m)[len(lines(m))-1], "scroll") {
		t.Errorf("after resize top = %d, footer %q", m.top, lines(m)[len(lines(m))-1])
	}
}

func TestPanelScrollDoesNotReadFiles(t *testing.T) {
	m := newPanel(t, 12, 10)
	writeTrail(t, m.app, "t", "# t\n\n- [ ] changed\n")
	press(m, "j")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	if strings.Contains(m.View().Content, "changed") {
		t.Error("scrolling or resizing re-read the trail")
	}
}

func TestPanelKeys(t *testing.T) {
	m := newPanel(t, 3, 20)
	if cmd := press(m, "2"); cmd != nil {
		t.Errorf("ticking returned a command")
	}
	if !strings.Contains(readTrail(t, m.app, "t"), "- [x] step 2\n") {
		t.Errorf("step 2 not ticked:\n%s", readTrail(t, m.app, "t"))
	}
	if !strings.Contains(m.View().Content, dim("✓ step 2")) {
		t.Error("tick not redrawn")
	}
	if cmd := press(m, "9"); cmd != nil {
		t.Error("a key past the rows did something")
	}
	if cmd := press(m, "e"); cmd == nil {
		t.Error("e did not open the editor")
	}
	if cmd := press(m, "q"); cmd == nil || cmd() != tea.Quit() {
		t.Error("q did not quit")
	}
}

func TestPanelRefreshSchedule(t *testing.T) {
	m := newPanel(t, 1, 20)
	var asked int
	m.app.Cache.Query = func(urls []string) (map[string]github.PR, error) { asked++; return nil, nil }
	writeTrail(t, m.app, "t", "# t\n\n- [ ] o/r#1\n")

	first := m.refresh(false)
	gen := m.gen
	stale := m.refresh(true) // r pressed while the first refresh runs
	if _, cmd := m.Update(first()); cmd != nil {
		t.Error("a superseded refresh scheduled the next one")
	}
	if _, cmd := m.Update(stale()); cmd == nil {
		t.Error("the latest refresh scheduled nothing")
	}
	if _, cmd := m.Update(tickMsg{gen}); cmd != nil {
		t.Error("an old timer refreshed")
	}
	if _, cmd := m.Update(tickMsg{gen + 1}); cmd == nil {
		t.Error("the timer did not refresh")
	}
	if asked != 2 {
		t.Errorf("asked GitHub %d times", asked)
	}
}

func TestPanelAltScroll(t *testing.T) {
	m := newPanel(t, 1, 20)
	m.app.Cache.Query = func([]string) (map[string]github.PR, error) { return nil, nil }
	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init is not a batch")
	}
	var raw []any
	for _, cmd := range batch {
		if r, ok := cmd().(tea.RawMsg); ok {
			raw = append(raw, r.Msg)
		}
	}
	if len(raw) != 1 || raw[0] != altScrollOn {
		t.Errorf("raw output = %q", raw)
	}
}

func TestPanelFollowsPaneSession(t *testing.T) {
	a := newApp(t, nil)
	writeTrail(t, a, "a", "# a\n\n- [ ] alpha\n")
	writeTrail(t, a, "b", "# b\n\n- [ ] beta\n")
	a.Sessions.Bind("p1", "a") // the pane as its own session
	a.Sessions.Bind("s2", "b")

	m := &panel{app: a, target: "p1", v: &view{}}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.Update(m.refresh(false)())
	if m.b.key != "p1" || !strings.Contains(m.View().Content, "alpha") {
		t.Fatalf("without a pointer: session %q\n%s", m.b.key, m.View().Content)
	}

	a.Sessions.NotePane("s2", "p1")
	m.Update(m.refresh(false)())
	if m.b.key != "s2" || !strings.Contains(m.View().Content, "beta") {
		t.Errorf("after the pointer moved: session %q\n%s", m.b.key, m.View().Content)
	}
	press(m, "1")
	if !strings.Contains(readTrail(t, a, "b"), "- [x] beta") {
		t.Error("tick went to the wrong trail")
	}
}
