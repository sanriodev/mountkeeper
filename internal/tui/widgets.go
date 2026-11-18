package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// ---- menu -------------------------------------------------------------------

// menuItem is one line of a menu.
type menuItem struct {
	label  string
	value  string         // current state, shown beside the label
	desc   string         // explanation shown while the item is selected
	run    func() tea.Cmd // Enter
	danger bool           // destructive: drawn in red
}

// menuScreen is a list of actions. Its items are rebuilt whenever the screen
// comes back on top, so that labels always show the current state.
type menuScreen struct {
	name    string
	build   func() []menuItem
	banner  func() string             // optional art on top, left out when the window is too small
	intro   func() string             // optional text above the items
	keys    map[string]func() tea.Cmd // extra single-key actions
	extra   string                    // their description for the footer
	load    func() tea.Cmd            // optional background loader started by init
	root    bool                      // Esc quits instead of going back
	items   []menuItem
	loading string // shown instead of the items while the loader runs
	cursor  int
	offset  int
	height  int
	msg     message
}

// menuLoadedMsg delivers what a menu's background loader found.
type menuLoadedMsg struct {
	screen *menuScreen
	items  []menuItem
	err    error
	note   string // shown below the items
}

func (m *menuScreen) init() tea.Cmd {
	m.items = m.build()
	if m.load != nil {
		return m.load()
	}
	return nil
}

func (m *menuScreen) crumb() string { return m.name }

func (m *menuScreen) help() string {
	h := "↑/↓ Navigate   Enter Select"
	if m.extra != "" {
		h += "   " + m.extra
	}
	if m.root {
		return h + "   Q Quit"
	}
	return h + "   Esc Back   Q Quit"
}

// room is the number of lines the items need without scrolling, with the
// longest description and the current message below them.
func (m *menuScreen) room() int {
	desc := 0
	for _, it := range m.items {
		if it.desc != "" {
			desc = max(desc, strings.Count(it.desc, "\n")+2)
		}
	}
	return len(m.items) + 1 + desc + m.msg.lines()
}

func (m *menuScreen) rebuild() {
	m.items = m.build()
	m.cursor = max(0, min(m.cursor, len(m.items)-1))
}

func (m *menuScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case resumeMsg:
		m.rebuild()
		m.msg.say(msg.note)
	case tickMsg:
		m.rebuild()
	case menuLoadedMsg:
		if msg.screen != m {
			return nil
		}
		m.loading = ""
		if msg.err != nil {
			m.msg.fail(msg.err)
			return nil
		}
		items := msg.items
		m.build = func() []menuItem { return items }
		m.rebuild()
		m.msg.say(msg.note)
	case noteMsg:
		m.rebuild()
		if msg.err != nil {
			m.msg.fail(msg.err)
		} else {
			m.msg.say(msg.text)
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return tea.Quit
		case "esc", "left", "h":
			if m.root && msg.String() != "esc" {
				return nil
			}
			return pop()
		case "up", "k":
			m.cursor--
		case "down", "j":
			m.cursor++
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.items) - 1
		case "enter", " ", "right", "l":
			if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].run != nil {
				m.msg.say("")
				return m.items[m.cursor].run()
			}
		default:
			if fn := m.keys[msg.String()]; fn != nil {
				m.msg.say("")
				return fn()
			}
		}
		m.cursor = max(0, min(m.cursor, len(m.items)-1))
	}
	return nil
}

func (m *menuScreen) view(width, height int) string {
	var b strings.Builder
	showAll := false // the banner is only drawn when every item fits below it
	if m.banner != nil && m.loading == "" {
		art := m.banner()
		lines := strings.Count(art, "\n") + 2 // and a blank line
		if (width <= 0 || lipgloss.Width(art)+2 <= width) &&
			(height <= 0 || lines+m.room() <= height) {
			for _, line := range strings.Split(art, "\n") {
				b.WriteString(strings.TrimRight("  "+line, " ") + "\n")
			}
			b.WriteString("\n")
			showAll = true
		}
	}
	if m.intro != nil {
		if text := m.intro(); text != "" {
			for _, line := range strings.Split(text, "\n") {
				b.WriteString("  " + line + "\n")
			}
			b.WriteString("\n")
		}
	}

	if m.loading != "" {
		b.WriteString("  " + busyStyle.Render(m.loading) + "\n\n")
		m.msg.render(&b)
		return b.String()
	}

	labelWidth := 0
	for _, it := range m.items {
		labelWidth = max(labelWidth, lipgloss.Width(it.label))
	}
	rows := 0
	if height > 0 && !showAll {
		// Room for the "more" markers, the description and a message.
		rows = max(3, height-8-strings.Count(b.String(), "\n"))
	}
	first, last := window(len(m.items), m.cursor, rows, &m.offset)
	if first > 0 {
		b.WriteString("    " + helpStyle.Render(fmt.Sprintf("↑ %d more", first)) + "\n")
	}
	for i := first; i < last; i++ {
		it := m.items[i]
		cur, label := "  ", fmt.Sprintf("%-*s", labelWidth, it.label)
		switch {
		case i == m.cursor && it.danger:
			cur, label = cursorStyle.Render("❯ "), errStyle.Bold(true).Render(label)
		case i == m.cursor:
			cur, label = cursorStyle.Render("❯ "), nameStyle.Render(label)
		case it.danger:
			label = errStyle.Render(label)
		}
		line := "  " + cur + label
		if it.value != "" {
			line += "   " + helpStyle.Render(it.value)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	if last < len(m.items) {
		b.WriteString("    " + helpStyle.Render(fmt.Sprintf("↓ %d more", len(m.items)-last)) + "\n")
	}
	b.WriteString("\n")

	if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].desc != "" {
		for _, line := range strings.Split(m.items[m.cursor].desc, "\n") {
			b.WriteString("  " + helpStyle.Render(line) + "\n")
		}
		b.WriteString("\n")
	}
	m.msg.render(&b)
	return b.String()
}

// ---- form -------------------------------------------------------------------

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldSecret
	fieldToggle
)

// field is one input of a form.
type field struct {
	key, label string
	kind       fieldKind
	hint       string // shown while the field has the focus
	input      textinput.Model
	on         bool
}

func textField(key, label, value, placeholder, hint string) *field {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	in.SetValue(value)
	in.CharLimit = 512
	in.Width = 48
	in.Cursor.SetMode(cursor.CursorStatic)
	return &field{key: key, label: label, hint: hint, input: in}
}

func secretField(key, label, hint string) *field {
	f := textField(key, label, "", "", hint)
	f.kind = fieldSecret
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '•'
	return f
}

func toggleField(key, label string, on bool, hint string) *field {
	return &field{key: key, label: label, kind: fieldToggle, on: on, hint: hint}
}

// formValues gives a submit function access to what was entered.
type formValues map[string]*field

func (v formValues) text(key string) string { return strings.TrimSpace(v[key].input.Value()) }
func (v formValues) raw(key string) string  { return v[key].input.Value() }
func (v formValues) on(key string) bool     { return v[key].on }

// formResultMsg carries the outcome of a submit back to its form.
type formResultMsg struct {
	form  *formScreen
	after tea.Cmd
	err   error
}

// formScreen collects a few values and hands them to submit, which runs in
// the background and returns what to do afterwards, or an error to show.
type formScreen struct {
	name   string
	intro  string
	fields []*field
	button string
	submit func(v formValues) (after tea.Cmd, err error)
	focus  int // len(fields) = the button
	busy   bool
	msg    message
}

func newForm(name, intro, button string, submit func(formValues) (tea.Cmd, error), fields ...*field) *formScreen {
	f := &formScreen{name: name, intro: intro, button: button, submit: submit, fields: fields}
	f.setFocus(0)
	return f
}

func (f *formScreen) init() tea.Cmd { return nil }
func (f *formScreen) crumb() string { return f.name }
func (f *formScreen) help() string {
	return "↑/↓ Field   Space Toggle   Enter Next / Confirm   Ctrl+S Save   Esc Cancel"
}

func (f *formScreen) setFocus(i int) {
	f.focus = (i + len(f.fields) + 1) % (len(f.fields) + 1)
	for j, fl := range f.fields {
		if fl.kind == fieldToggle {
			continue
		}
		if j == f.focus {
			fl.input.Focus()
		} else {
			fl.input.Blur()
		}
	}
}

func (f *formScreen) values() formValues {
	v := formValues{}
	for _, fl := range f.fields {
		v[fl.key] = fl
	}
	return v
}

func (f *formScreen) send() tea.Cmd {
	if f.busy {
		return nil
	}
	f.busy = true
	f.msg.say("")
	v := f.values()
	return func() tea.Msg {
		after, err := f.submit(v)
		return formResultMsg{form: f, after: after, err: err}
	}
}

func (f *formScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case formResultMsg:
		if msg.form != f {
			return nil
		}
		f.busy = false
		if msg.err != nil {
			f.msg.fail(msg.err)
			return nil
		}
		return msg.after

	case tea.KeyMsg:
		if f.busy {
			return nil
		}
		var cur *field
		if f.focus < len(f.fields) {
			cur = f.fields[f.focus]
		}
		switch msg.Type {
		case tea.KeyEsc:
			return pop()
		case tea.KeyCtrlS:
			return f.send()
		case tea.KeyUp, tea.KeyShiftTab:
			f.setFocus(f.focus - 1)
			return nil
		case tea.KeyDown, tea.KeyTab:
			f.setFocus(f.focus + 1)
			return nil
		case tea.KeyEnter:
			if cur == nil {
				return f.send()
			}
			f.setFocus(f.focus + 1)
			return nil
		}
		if cur == nil {
			return nil
		}
		if cur.kind == fieldToggle {
			switch msg.String() {
			case " ", "left", "right", "y", "n":
				switch msg.String() {
				case "y":
					cur.on = true
				case "n":
					cur.on = false
				default:
					cur.on = !cur.on
				}
			}
			return nil
		}
		var cmd tea.Cmd
		cur.input, cmd = cur.input.Update(msg)
		return cmd
	}
	return nil
}

func (f *formScreen) view(width, height int) string {
	var b strings.Builder
	if f.intro != "" {
		for _, line := range strings.Split(f.intro, "\n") {
			b.WriteString("  " + helpStyle.Render(line) + "\n")
		}
		b.WriteString("\n")
	}
	labelWidth := 0
	for _, fl := range f.fields {
		labelWidth = max(labelWidth, lipgloss.Width(fl.label))
	}
	for i, fl := range f.fields {
		cur, label := "  ", fmt.Sprintf("%-*s", labelWidth, fl.label)
		if i == f.focus {
			cur, label = cursorStyle.Render("❯ "), nameStyle.Render(label)
		}
		var value string
		if fl.kind == fieldToggle {
			value = offStyle.Render("[ ] no")
			if fl.on {
				value = onStyle.Render("[x] yes")
			}
		} else {
			value = fl.input.View()
		}
		b.WriteString("  " + cur + label + "   " + value + "\n")
	}
	b.WriteString("\n")

	button := "[ " + f.button + " ]"
	switch {
	case f.busy:
		button = busyStyle.Render("Working…")
	case f.focus == len(f.fields):
		button = cursorStyle.Render("❯ ") + nameStyle.Render(button)
	default:
		button = "  " + helpStyle.Render(button)
	}
	b.WriteString("  " + button + "\n\n")

	if f.focus < len(f.fields) && f.fields[f.focus].hint != "" {
		for _, line := range strings.Split(f.fields[f.focus].hint, "\n") {
			b.WriteString("  " + helpStyle.Render(line) + "\n")
		}
		b.WriteString("\n")
	}
	f.msg.render(&b)
	return b.String()
}

// ---- confirm ----------------------------------------------------------------

// confirmScreen asks before something that cannot be undone. "No" is
// preselected.
type confirmScreen struct {
	name     string
	question string
	detail   string
	yes      string
	run      func() (after tea.Cmd, err error)
	onYes    bool
	busy     bool
	msg      message
}

type confirmResultMsg struct {
	screen *confirmScreen
	after  tea.Cmd
	err    error
}

func (c *confirmScreen) init() tea.Cmd { return nil }
func (c *confirmScreen) crumb() string { return c.name }
func (c *confirmScreen) help() string  { return "←/→ Choose   Enter Confirm   Y Yes   N / Esc No" }

func (c *confirmScreen) confirm() tea.Cmd {
	if c.busy {
		return nil
	}
	c.busy = true
	return func() tea.Msg {
		after, err := c.run()
		return confirmResultMsg{screen: c, after: after, err: err}
	}
}

func (c *confirmScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case confirmResultMsg:
		if msg.screen != c {
			return nil
		}
		c.busy = false
		if msg.err != nil {
			c.msg.fail(msg.err)
			return nil
		}
		return msg.after
	case tea.KeyMsg:
		if c.busy {
			return nil
		}
		switch msg.String() {
		case "esc", "n", "q":
			return pop()
		case "y":
			return c.confirm()
		case "left", "right", "tab", "h", "l", "up", "down":
			c.onYes = !c.onYes
		case "enter":
			if c.onYes {
				return c.confirm()
			}
			return pop()
		}
	}
	return nil
}

func (c *confirmScreen) view(width, height int) string {
	var b strings.Builder
	b.WriteString("  " + nameStyle.Render(c.question) + "\n")
	if c.detail != "" {
		for _, line := range strings.Split(c.detail, "\n") {
			b.WriteString("  " + helpStyle.Render(line) + "\n")
		}
	}
	b.WriteString("\n")
	yes, no := "  "+c.yes+"  ", "  No  "
	if c.onYes {
		yes = errStyle.Bold(true).Reverse(true).Render(yes)
	} else {
		no = lipgloss.NewStyle().Bold(true).Reverse(true).Render(no)
	}
	if c.busy {
		b.WriteString("  " + busyStyle.Render("Working…") + "\n\n")
	} else {
		b.WriteString("  " + no + "   " + yes + "\n\n")
	}
	c.msg.render(&b)
	return b.String()
}

// ---- picker -----------------------------------------------------------------

type pickOption struct {
	id, label, detail string
	on                bool
}

// pickScreen is a multiple-choice list.
type pickScreen struct {
	name    string
	intro   string
	options []pickOption
	save    func(ids []string) (after tea.Cmd, err error)
	cursor  int
	offset  int
	height  int
	busy    bool
	msg     message
}

type pickResultMsg struct {
	screen *pickScreen
	after  tea.Cmd
	err    error
}

func (p *pickScreen) init() tea.Cmd { return nil }
func (p *pickScreen) crumb() string { return p.name }
func (p *pickScreen) help() string {
	return "↑/↓ Navigate   Space Toggle   A All / None   Enter Save   Esc Cancel"
}

func (p *pickScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.height = msg.Height
	case pickResultMsg:
		if msg.screen != p {
			return nil
		}
		p.busy = false
		if msg.err != nil {
			p.msg.fail(msg.err)
			return nil
		}
		return msg.after
	case tea.KeyMsg:
		if p.busy {
			return nil
		}
		switch msg.String() {
		case "esc":
			return pop()
		case "q":
			return tea.Quit
		case "up", "k":
			p.cursor--
		case "down", "j":
			p.cursor++
		case " ", "x":
			if p.cursor < len(p.options) {
				p.options[p.cursor].on = !p.options[p.cursor].on
			}
		case "a":
			all := true
			for _, o := range p.options {
				all = all && o.on
			}
			for i := range p.options {
				p.options[i].on = !all
			}
		case "enter":
			var ids []string
			for _, o := range p.options {
				if o.on {
					ids = append(ids, o.id)
				}
			}
			p.busy = true
			return func() tea.Msg {
				after, err := p.save(ids)
				return pickResultMsg{screen: p, after: after, err: err}
			}
		}
		p.cursor = max(0, min(p.cursor, len(p.options)-1))
	}
	return nil
}

func (p *pickScreen) view(width, height int) string {
	var b strings.Builder
	if p.intro != "" {
		b.WriteString("  " + helpStyle.Render(p.intro) + "\n\n")
	}
	if len(p.options) == 0 {
		b.WriteString("  " + helpStyle.Render("Nothing to choose from.") + "\n\n")
	}
	labelWidth := 0
	for _, o := range p.options {
		labelWidth = max(labelWidth, lipgloss.Width(o.label))
	}
	rows := 0
	if height > 0 {
		rows = max(3, height-6)
	}
	first, last := window(len(p.options), p.cursor, rows, &p.offset)
	for i := first; i < last; i++ {
		o := p.options[i]
		cur, box, label := "  ", offStyle.Render("[ ]"), fmt.Sprintf("%-*s", labelWidth, o.label)
		if o.on {
			box = onStyle.Render("[x]")
		}
		if i == p.cursor {
			cur, label = cursorStyle.Render("❯ "), nameStyle.Render(label)
		}
		line := "  " + cur + box + " " + label
		if o.detail != "" {
			line += "   " + helpStyle.Render(o.detail)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	b.WriteString("\n")
	p.msg.render(&b)
	return b.String()
}

// ---- text -------------------------------------------------------------------

// textScreen shows read-only lines that are loaded in the background.
type textScreen struct {
	name    string
	load    func() []string
	lines   []string
	loading bool
	offset  int
	height  int
}

type textLoadedMsg struct {
	screen *textScreen
	lines  []string
}

func (t *textScreen) reload() tea.Cmd {
	t.loading = true
	return func() tea.Msg { return textLoadedMsg{screen: t, lines: t.load()} }
}

func (t *textScreen) init() tea.Cmd { return t.reload() }
func (t *textScreen) crumb() string { return t.name }
func (t *textScreen) help() string  { return "↑/↓ Scroll   R Refresh   Esc Back   Q Quit" }

func (t *textScreen) rows() int {
	if t.height <= 0 {
		return 0
	}
	return max(3, bodyHeight(t.height)-3)
}

func (t *textScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.height = msg.Height
	case textLoadedMsg:
		if msg.screen == t {
			t.lines, t.loading = msg.lines, false
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "left", "h", "enter":
			return pop()
		case "q":
			return tea.Quit
		case "r":
			return t.reload()
		case "up", "k":
			t.offset--
		case "down", "j":
			t.offset++
		case "pgup":
			t.offset -= max(1, t.rows())
		case "pgdown", " ":
			t.offset += max(1, t.rows())
		case "home", "g":
			t.offset = 0
		case "end", "G":
			t.offset = len(t.lines)
		}
	}
	limit := 0
	if r := t.rows(); r > 0 {
		limit = max(0, len(t.lines)-r)
	}
	t.offset = max(0, min(t.offset, limit))
	return nil
}

func (t *textScreen) view(width, height int) string {
	var b strings.Builder
	if t.loading && len(t.lines) == 0 {
		return "  " + busyStyle.Render("Loading…") + "\n\n"
	}
	first, last := 0, len(t.lines)
	if r := t.rows(); r > 0 && len(t.lines) > r {
		first, last = t.offset, min(t.offset+r, len(t.lines))
	}
	if first > 0 {
		b.WriteString("  " + helpStyle.Render(fmt.Sprintf("↑ %d more", first)) + "\n")
	}
	for _, line := range t.lines[first:last] {
		b.WriteString("  " + line + "\n")
	}
	if last < len(t.lines) {
		b.WriteString("  " + helpStyle.Render(fmt.Sprintf("↓ %d more", len(t.lines)-last)) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}
