package ui

import (
	"cmp"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/ken8203/cairn/internal/trail"
)

const keysHelp = "1-9 open PR / tick step · e edit · r refresh · q close"

// panel is the view of the session last run in a pane, kept on screen. A
// refresh reads the session, its files and PRs into a board off the UI goroutine; layout
// and View only lay it out, so resizing and scrolling stay cheap.
type panel struct {
	app           App
	target, title string
	b             *board // nil until the first refresh
	v             *view
	clock         string
	top           int
	width, height int
	gen           int // the latest refresh; only it schedules the next
	err           error
}

type (
	refreshedMsg struct {
		gen int
		b   *board
	}
	tickMsg   struct{ gen int }
	editedMsg struct{}
)

// Watch runs the panel for a pane until it is closed.
func (a App) Watch(pane string) error {
	m := &panel{app: a, target: pane, title: trail.Clean(a.Title(pane)), v: &view{}}
	_, err := tea.NewProgram(m).Run()
	fmt.Print(altScrollOff)
	return cmp.Or(err, m.err)
}

// The panel leaves the mouse to the terminal, so clicks, links and selection keep working;
// alternate scroll mode turns the wheel into arrow keys instead.
const (
	altScrollOn  = "\x1b[?1007h"
	altScrollOff = "\x1b[?1007l"
)

func (m *panel) Init() tea.Cmd { return tea.Batch(tea.Raw(altScrollOn), m.refresh(false)) }

func (m *panel) refresh(force bool) tea.Cmd {
	m.gen++
	gen, target, a := m.gen, m.target, m.app
	return func() tea.Msg {
		// The pane follows whichever session last ran there.
		return refreshedMsg{gen, a.gather(a.Sessions.PaneSession(target), force)}
	}
}

func (m *panel) layout() {
	if m.b != nil {
		m.v = render(m.b, m.width)
	}
	m.scroll(0)
}

// The heading, a blank, a blank and the footer frame the body.
const (
	frameLines = 4
	minRoom    = 3
)

func (m *panel) room() int { return max(m.height-frameLines, minRoom) }

func (m *panel) scroll(by int) {
	m.top = max(0, min(m.top+by, len(m.v.lines)-m.room()))
}

func (m *panel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case refreshedMsg:
		// A refresh superseded by a newer one is dropped; the newer one shows.
		if msg.gen == m.gen {
			m.b, m.clock = msg.b, time.Now().Format("15:04")
			m.layout()
			return m, tea.Tick(m.app.Cache.TTL, func(time.Time) tea.Msg { return tickMsg{msg.gen} })
		}
	case tickMsg:
		if msg.gen == m.gen {
			return m, m.refresh(false)
		}
	case editedMsg:
		return m, m.refresh(false)
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *panel) key(k string) tea.Cmd {
	switch k {
	case "q", "ctrl+c":
		return tea.Quit
	case "r":
		return m.refresh(true)
	case "j", "down":
		m.scroll(1)
	case "k", "up":
		m.scroll(-1)
	case "e":
		if m.b == nil || m.b.name == "" {
			return nil
		}
		return tea.ExecProcess(Editor(m.app.Sessions.Trails.File(m.b.name)), func(error) tea.Msg { return editedMsg{} })
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		i := int(k[0] - '0')
		if i > len(m.v.targets) {
			return nil
		}
		picked := m.v.targets[i-1]
		if s, ok := strings.CutPrefix(picked, "step:"); ok {
			n, _ := strconv.Atoi(s)
			if err := m.app.Sessions.Trails.Tick(m.b.name, n); err != nil {
				m.err = err
				return tea.Quit
			}
			m.b.steps, m.b.notes = m.app.loadSteps(m.b.name)
			m.app.readPRs(m.b)
			m.layout()
			return nil
		}
		return func() tea.Msg {
			browser.New("", io.Discard, io.Discard).Browse(picked)
			return nil
		}
	}
	return nil
}

func (m *panel) View() tea.View {
	room, total := m.room(), len(m.v.lines)
	footer := keysHelp
	if total > room {
		footer = fmt.Sprintf("%d-%d/%d · j/k scroll · %s", m.top+1, m.top+room, total, footer)
	}
	var b strings.Builder
	clock := " · " + m.clock
	b.WriteString(dim(ansi.Truncate(cmp.Or(m.title, "this pane"), m.width-ansi.StringWidth(clock), "")+clock) + "\n\n")
	for _, l := range m.v.lines[m.top:min(m.top+room, total)] {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n" + dim(footer))
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
