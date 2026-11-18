package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A screen is one level of the interface. Screens form a stack: Enter usually
// pushes a new one, Esc pops back to the previous one.
type screen interface {
	init() tea.Cmd
	update(msg tea.Msg) tea.Cmd
	// view renders the body for a window of the given size (0 = unknown).
	view(width, height int) string
	crumb() string // name in the breadcrumb line
	help() string  // key summary for the footer
}

type (
	pushMsg    struct{ s screen }
	replaceMsg struct{ s screen }
	// popMsg leaves n screens; note is shown on the screen that comes back.
	popMsg struct {
		n    int
		note string
	}
	// resumeMsg tells a screen that it is on top again.
	resumeMsg struct{ note string }
	// tickMsg asks the top screen to refresh live data.
	tickMsg struct{}
	// noteMsg reports the outcome of an action started from a menu.
	noteMsg struct {
		text string
		err  error
	}
)

func push(s screen) tea.Cmd    { return func() tea.Msg { return pushMsg{s} } }
func replace(s screen) tea.Cmd { return func() tea.Msg { return replaceMsg{s} } }
func pop() tea.Cmd             { return func() tea.Msg { return popMsg{n: 1} } }

func popWith(n int, note string) tea.Cmd {
	return func() tea.Msg { return popMsg{n: n, note: note} }
}

func note(text string, err error) tea.Cmd {
	return func() tea.Msg { return noteMsg{text: text, err: err} }
}

func tick() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// app is the Bubble Tea model: the screen stack plus the window size.
type app struct {
	stack         []screen
	width, height int
}

// Run starts the interface with the main menu.
func Run(b Backend) error { return run(mainMenu(b)) }

// RunShares starts directly in the connection manager.
func RunShares(b Backend) error { return run(newSharesScreen(b)) }

func run(first screen) error {
	_, err := tea.NewProgram(&app{stack: []screen{first}}, tea.WithAltScreen()).Run()
	return err
}

func (a *app) top() screen { return a.stack[len(a.stack)-1] }

func (a *app) Init() tea.Cmd { return tea.Batch(a.top().init(), tick()) }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		var cmds []tea.Cmd
		for _, s := range a.stack {
			cmds = append(cmds, s.update(msg))
		}
		return a, tea.Batch(cmds...)

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return a, tea.Quit
		}
		return a, a.top().update(msg)

	case pushMsg:
		a.stack = append(a.stack, msg.s)
		return a, tea.Batch(msg.s.init(), msg.s.update(tea.WindowSizeMsg{Width: a.width, Height: a.height}))

	case replaceMsg:
		a.stack[len(a.stack)-1] = msg.s
		return a, tea.Batch(msg.s.init(), msg.s.update(tea.WindowSizeMsg{Width: a.width, Height: a.height}))

	case popMsg:
		// Leaving the only screen quits; otherwise never pop past the root.
		if len(a.stack) == 1 {
			return a, tea.Quit
		}
		n := max(1, min(msg.n, len(a.stack)-1))
		a.stack = a.stack[:len(a.stack)-n]
		return a, a.top().update(resumeMsg{note: msg.note})

	case tickMsg:
		return a, tea.Batch(a.top().update(msg), tick())
	}

	// Results of background work go to every screen: the one that started it
	// may no longer be on top. Screens ignore what is not theirs.
	var cmds []tea.Cmd
	for _, s := range a.stack {
		cmds = append(cmds, s.update(msg))
	}
	return a, tea.Batch(cmds...)
}

// View draws breadcrumbs, the top screen and its key summary. Lines wider
// than the window are cut: a wrapped line would push the layout off screen.
func (a *app) View() string {
	crumbs := []string{titleStyle.Render("Mountkeeper")}
	for _, s := range a.stack {
		if c := s.crumb(); c != "" {
			crumbs = append(crumbs, c)
		}
	}
	var b strings.Builder
	b.WriteString("\n  " + strings.Join(crumbs, helpStyle.Render("  ›  ")) + "\n\n")
	b.WriteString(a.top().view(a.width, bodyHeight(a.height)))
	if h := a.top().help(); h != "" {
		b.WriteString("\n  " + helpStyle.Render(h) + "\n")
	}

	view := b.String()
	if a.width <= 0 {
		return view
	}
	fit := lipgloss.NewStyle().MaxWidth(a.width)
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > a.width {
			lines[i] = fit.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	onStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	offStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	busyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	nameStyle   = lipgloss.NewStyle().Bold(true)
	tabStyle    = lipgloss.NewStyle().Bold(true).Underline(true)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// message is the status line shared by all screens: a result or an error,
// optionally with a hint on what to do next.
type message struct {
	text, hint string
	isError    bool
}

func (m *message) say(text string) { *m = message{text: text} }

func (m *message) fail(err error) {
	*m = message{text: err.Error(), isError: true}
	var h interface{ Hint() string }
	if errorsAs(err, &h) {
		m.hint = h.Hint()
	}
}

// lines is the number of lines render writes.
func (m message) lines() int {
	if m.text == "" {
		return 0
	}
	n := strings.Count(m.text, "\n") + 2
	if m.hint != "" {
		n += strings.Count(m.hint, "\n") + 1
	}
	return n
}

func (m message) render(b *strings.Builder) {
	if m.text == "" {
		return
	}
	style := onStyle
	if m.isError {
		style = errStyle
	}
	for _, line := range strings.Split(m.text, "\n") {
		b.WriteString("  " + style.Render(line) + "\n")
	}
	if m.hint != "" {
		for _, line := range strings.Split(m.hint, "\n") {
			b.WriteString("  " + helpStyle.Render(line) + "\n")
		}
	}
	b.WriteString("\n")
}

// chrome is the number of lines the frame around a screen takes: the
// breadcrumb line with its spacing above, the key summary below.
const chrome = 5

// bodyHeight is the room a screen has in a window of the given height
// (0 = unknown, no limit).
func bodyHeight(windowHeight int) int {
	if windowHeight <= 0 {
		return 0
	}
	return max(1, windowHeight-chrome)
}

// window returns the slice [first,last) of n rows to show so that the cursor
// stays visible in a list of the given height (0 = show everything).
func window(n, cursor, height int, offset *int) (first, last int) {
	if height <= 0 || n <= height {
		*offset = 0
		return 0, n
	}
	if cursor < *offset {
		*offset = cursor
	}
	if cursor >= *offset+height {
		*offset = cursor - height + 1
	}
	*offset = max(0, min(*offset, n-height))
	return *offset, *offset + height
}
