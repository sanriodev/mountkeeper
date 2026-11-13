// Package tui implements the interactive connection manager shown by
// `mountkeeper connect`. It knows nothing about the Keychain or NetFS; all
// side effects go through the Backend supplied by the CLI layer.
package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// ErrPasswordRequired is returned by Backend.Connect when the share needs a
// password that is not available; the TUI then asks for it.
var ErrPasswordRequired = errors.New("password required")

// Backend performs the actual work on behalf of the interface.
type Backend struct {
	// Statuses returns the live state of every share, keyed by share id.
	Statuses func() (map[string]mount.Status, error)
	// Connect mounts a share. password is nil unless the user just typed one.
	Connect func(s *share.Share, password *string) (path string, err error)
	// Disconnect unmounts the volume at path.
	Disconnect func(s *share.Share, path string) error
	// Open shows a mounted path in Finder.
	Open func(path string) error
}

// Run shows the connection manager until the user quits.
func Run(shares []*share.Share, b Backend) error {
	m := model{shares: shares, backend: b, statuses: map[string]mount.Status{}, busy: map[string]string{}}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	onStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	offStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	busyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	nameStyle   = lipgloss.NewStyle().Bold(true)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type (
	statusMsg struct {
		statuses map[string]mount.Status
		err      error
	}
	doneMsg struct {
		id   string
		text string
		err  error
		// askPassword asks the user for the password of share id.
		askPassword bool
	}
	tickMsg struct{}
)

type model struct {
	shares   []*share.Share
	backend  Backend
	statuses map[string]mount.Status
	busy     map[string]string // share id → "Connecting…"
	cursor   int

	message string
	isError bool

	// Password entry for one share.
	asking   *share.Share
	password []rune

	width int
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) refresh() tea.Cmd {
	return func() tea.Msg {
		st, err := m.backend.Statuses()
		return statusMsg{statuses: st, err: err}
	}
}

func (m model) connect(s *share.Share, password *string) tea.Cmd {
	return func() tea.Msg {
		path, err := m.backend.Connect(s, password)
		switch {
		case errors.Is(err, ErrPasswordRequired):
			return doneMsg{id: s.ID, askPassword: true}
		case errors.Is(err, mount.ErrAuth) && password != nil:
			return doneMsg{id: s.ID, err: err, askPassword: true}
		case err != nil:
			return doneMsg{id: s.ID, err: err}
		}
		return doneMsg{id: s.ID, text: fmt.Sprintf("Connected %s at %s", s.Name, share.CollapsePath(path))}
	}
}

func (m model) disconnect(s *share.Share, path string) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Disconnect(s, path); err != nil {
			return doneMsg{id: s.ID, err: err}
		}
		return doneMsg{id: s.ID, text: "Disconnected " + s.Name}
	}
}

func (m model) open(s *share.Share, path string) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Open(path); err != nil {
			return doneMsg{id: s.ID, err: err}
		}
		return doneMsg{id: s.ID, text: "Opened " + s.Name + " in Finder"}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())

	case statusMsg:
		if msg.err != nil {
			m.message, m.isError = msg.err.Error(), true
		} else {
			m.statuses = msg.statuses
		}
		return m, nil

	case doneMsg:
		delete(m.busy, msg.id)
		if msg.err != nil {
			m.message, m.isError = msg.err.Error(), true
		} else if msg.text != "" {
			m.message, m.isError = msg.text, false
		}
		if msg.askPassword {
			m.asking, m.password = m.byID(msg.id), nil
		}
		return m, m.refresh()

	case tea.KeyMsg:
		if m.asking != nil {
			return m.updatePassword(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m model) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.shares)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(len(m.shares)-1, 0)
	case "r":
		m.message = ""
		return m, m.refresh()
	}

	s := m.selected()
	if s == nil || m.busy[s.ID] != "" {
		return m, nil
	}
	st := m.statuses[s.ID]

	switch key.String() {
	case "enter", " ":
		m.message = ""
		if st.Mounted() {
			m.busy[s.ID] = "Disconnecting…"
			return m, m.disconnect(s, st.Path)
		}
		m.busy[s.ID] = "Connecting…"
		return m, m.connect(s, nil)
	case "d":
		if st.Mounted() {
			m.message = ""
			m.busy[s.ID] = "Disconnecting…"
			return m, m.disconnect(s, st.Path)
		}
	case "o":
		if st.Mounted() {
			return m, m.open(s, st.Path)
		}
		m.message, m.isError = s.Name+" is not connected — press Enter to connect first", true
	}
	return m, nil
}

func (m model) updatePassword(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.asking, m.password = nil, nil
		m.message, m.isError = "Cancelled", false
	case tea.KeyEnter:
		s, pw := m.asking, string(m.password)
		m.asking, m.password = nil, nil
		m.message = ""
		m.busy[s.ID] = "Connecting…"
		return m, m.connect(s, &pw)
	case tea.KeyBackspace:
		if n := len(m.password); n > 0 {
			m.password = m.password[:n-1]
		}
	case tea.KeyCtrlU:
		m.password = nil
	case tea.KeySpace:
		m.password = append(m.password, ' ')
	case tea.KeyRunes:
		m.password = append(m.password, key.Runes...)
	}
	return m, nil
}

func (m model) selected() *share.Share {
	if m.cursor < 0 || m.cursor >= len(m.shares) {
		return nil
	}
	return m.shares[m.cursor]
}

func (m model) byID(id string) *share.Share {
	for _, s := range m.shares {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render("Mountkeeper") + "\n\n")

	if len(m.shares) == 0 {
		b.WriteString("  No network shares registered yet.\n\n")
		b.WriteString("  Add one with:  mountkeeper add smb://server/share\n\n")
		b.WriteString("  " + helpStyle.Render("Q Quit") + "\n")
		return b.String()
	}

	b.WriteString("  Available network shares:\n\n")

	nameWidth := 0
	for _, s := range m.shares {
		nameWidth = max(nameWidth, lipgloss.Width(s.Name))
	}

	for i, s := range m.shares {
		cursor := "  "
		name := fmt.Sprintf("%-*s", nameWidth, s.Name)
		if i == m.cursor {
			cursor = cursorStyle.Render("❯ ")
			name = nameStyle.Render(name)
		}

		st := m.statuses[s.ID]
		dot, state := offStyle.Render("○"), offStyle.Render(st.String())
		if st.Mounted() {
			dot, state = onStyle.Render("●"), onStyle.Render(st.String())
		}
		if busy := m.busy[s.ID]; busy != "" {
			dot, state = busyStyle.Render("◌"), busyStyle.Render(busy)
		}
		fmt.Fprintf(&b, "  %s%s %s   %s\n", cursor, dot, name, state)
	}
	b.WriteString("\n")

	if m.asking != nil {
		who := m.asking.Host
		if m.asking.Username != "" {
			who = m.asking.Username + "@" + who
		}
		fmt.Fprintf(&b, "  Password for %s: %s\n\n", who, strings.Repeat("•", len(m.password)))
		b.WriteString("  " + helpStyle.Render("Enter Connect   Esc Cancel") + "\n")
		if m.message != "" && m.isError {
			b.WriteString("\n  " + errStyle.Render(m.message) + "\n")
		}
		return b.String()
	}

	if m.message != "" {
		style := onStyle
		if m.isError {
			style = errStyle
		}
		for _, line := range strings.Split(m.message, "\n") {
			b.WriteString("  " + style.Render(line) + "\n")
		}
		b.WriteString("\n")
	}

	action := "Connect"
	if s := m.selected(); s != nil && m.statuses[s.ID].Mounted() {
		action = "Disconnect"
	}
	b.WriteString("  " + helpStyle.Render("↑/↓ Navigate   Enter "+action+"   O Open in Finder   R Refresh   Q Quit") + "\n")
	return b.String()
}
