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

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type (
	statusMsg struct {
		statuses map[string]mount.Status
		err      error
	}
	reachMsg map[string]bool
	doneMsg  struct {
		id   string
		text string
		err  error
		// askPassword asks the user for the password of share id.
		askPassword bool
		// forcePath is set when disconnecting failed in a way only forcing
		// can overcome (open files, or a server that no longer answers).
		forcePath string
	}
	autoMsg struct {
		id      string
		enabled bool
		err     error
	}
	spinMsg struct{}
)

// sharesScreen is the connection manager: the list of shares with their live
// state, where Enter connects or disconnects and O opens a share's options.
type sharesScreen struct {
	backend  Backend
	shares   []*share.Share
	profiles []Profile

	statuses   map[string]mount.Status
	loaded     bool
	reach      map[string]bool
	reachKnown bool
	lastProbe  time.Time
	busy       map[string]string // share id → "Connecting…"
	spinning   bool
	frame      int

	tab       int // 0 = all shares, n = profiles[n-1]
	startTab  string
	startKey  string // "c" or "x": run once the first status is known
	filter    string
	filtering bool
	cursor    int // index into visible()
	offset    int // first visible row

	msg message

	// Password entry: one share at a time, further ones wait in the queue.
	asking    *share.Share
	password  []rune
	askQueue  []*share.Share
	openAfter map[string]bool // open Finder once this share is connected

	showHelp bool
	height   int
}

func newSharesScreen(b Backend) *sharesScreen {
	m := &sharesScreen{
		backend:   b,
		statuses:  map[string]mount.Status{},
		busy:      map[string]string{},
		openAfter: map[string]bool{},
	}
	m.reload()
	return m
}

// newProfileShares opens the manager on a profile's tab and, if key is "c" or
// "x", connects or disconnects everything in it right away.
func newProfileShares(b Backend, profile, key string) *sharesScreen {
	m := newSharesScreen(b)
	m.startTab, m.startKey = profile, key
	for i, p := range m.profiles {
		if p.Name == profile {
			m.tab = i + 1
		}
	}
	return m
}

// reload re-reads shares and profiles, which other screens may have changed.
func (m *sharesScreen) reload() {
	m.shares, m.profiles = m.backend.Shares(), m.backend.Profiles()
	if m.tab > len(m.profiles) {
		m.tab = 0
	}
	m.clamp()
}

func (m *sharesScreen) crumb() string { return "Shares" }

func (m *sharesScreen) help() string {
	switch {
	case m.showHelp:
		return "Press any key to return"
	case m.asking != nil:
		return "Enter Connect   Esc Cancel"
	case m.filtering:
		return "Type to filter   Enter Keep   Esc Clear"
	case len(m.shares) == 0:
		return "N New share   Esc Back   Q Quit"
	}
	action := "Connect"
	if s := m.selected(); s != nil && m.stale(s) {
		action = "Force disconnect"
	} else if s != nil && m.statuses[s.ID].Mounted() {
		action = "Disconnect"
	}
	return "↑/↓ Navigate   Enter " + action + "   O Options   F Finder   / Filter   ? Help   Esc Back"
}

// ---- commands ---------------------------------------------------------------

func (m *sharesScreen) init() tea.Cmd { return tea.Batch(m.refresh(), m.probe()) }

func spinTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func (m *sharesScreen) refresh() tea.Cmd {
	return func() tea.Msg {
		st, err := m.backend.Statuses()
		return statusMsg{statuses: st, err: err}
	}
}

func (m *sharesScreen) probe() tea.Cmd {
	m.lastProbe = time.Now()
	return func() tea.Msg { return reachMsg(m.backend.Reachable()) }
}

func (m *sharesScreen) connect(s *share.Share, password *string, thenOpen bool) tea.Cmd {
	return func() tea.Msg {
		path, err := m.backend.Connect(s, password)
		switch {
		case errors.Is(err, ErrPasswordRequired):
			return doneMsg{id: s.ID, askPassword: true}
		case errors.Is(err, mount.ErrAuth) && password != nil:
			// A typed password was wrong: say so and ask again.
			return doneMsg{id: s.ID, err: err, askPassword: true}
		case err != nil:
			return doneMsg{id: s.ID, err: err}
		}
		text := fmt.Sprintf("Connected %s at %s", s.Name, share.CollapsePath(path))
		if thenOpen || s.AutoOpen {
			if err := m.backend.Open(path); err != nil {
				return doneMsg{id: s.ID, err: err}
			}
		}
		return doneMsg{id: s.ID, text: text}
	}
}

func (m *sharesScreen) disconnect(s *share.Share, path string) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Disconnect(s, path, false); err != nil {
			var busy *mount.BusyError
			if errors.As(err, &busy) || errors.Is(err, mount.ErrNotResponding) {
				return doneMsg{id: s.ID, err: err, forcePath: path}
			}
			return doneMsg{id: s.ID, err: err}
		}
		return doneMsg{id: s.ID, text: "Disconnected " + s.Name}
	}
}

func (m *sharesScreen) open(s *share.Share, path string) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Open(path); err != nil {
			return doneMsg{id: s.ID, err: err}
		}
		return doneMsg{id: s.ID, text: "Opened " + s.Name + " in Finder"}
	}
}

func (m *sharesScreen) toggleAuto(s *share.Share) tea.Cmd {
	on := !s.AutoConnect
	return func() tea.Msg {
		return autoMsg{id: s.ID, enabled: on, err: m.backend.SetAutoConnect(s, on)}
	}
}

// start marks a share busy and makes sure the spinner runs.
func (m *sharesScreen) start(id, label string) tea.Cmd {
	m.busy[id] = label
	if m.spinning {
		return nil
	}
	m.spinning = true
	return spinTick()
}

// ---- state helpers ----------------------------------------------------------

// visible returns the shares of the current tab that match the filter.
func (m *sharesScreen) visible() []*share.Share {
	var ids map[string]bool
	if m.tab > 0 && m.tab <= len(m.profiles) {
		ids = map[string]bool{}
		for _, id := range m.profiles[m.tab-1].IDs {
			ids[id] = true
		}
	}
	needle := strings.ToLower(strings.TrimSpace(m.filter))
	var out []*share.Share
	for _, s := range m.shares {
		if ids != nil && !ids[s.ID] {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(s.Name+" "+s.ID+" "+s.Host+s.Path), needle) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (m *sharesScreen) selected() *share.Share {
	v := m.visible()
	if m.cursor < 0 || m.cursor >= len(v) {
		return nil
	}
	return v[m.cursor]
}

func (m *sharesScreen) byID(id string) *share.Share {
	for _, s := range m.shares {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (m *sharesScreen) unreachable(s *share.Share) bool {
	if !m.reachKnown || m.statuses[s.ID].Mounted() {
		return false
	}
	ok, probed := m.reach[s.ID]
	return probed && !ok
}

// stale reports whether a share is mounted although its server no longer
// answers.
func (m *sharesScreen) stale(s *share.Share) bool {
	if !m.reachKnown || !m.statuses[s.ID].Mounted() {
		return false
	}
	ok, probed := m.reach[s.ID]
	return probed && !ok
}

// showDetails reports whether the window is tall enough for the detail pane.
func (m *sharesScreen) showDetails() bool { return m.height == 0 || m.height >= 24 }

// listHeight is how many share rows fit; 0 means "no limit known".
func (m *sharesScreen) listHeight() int {
	if m.height == 0 {
		return 0
	}
	used := 7 // counter line, "more" markers, spacing, message
	if len(m.profiles) > 0 {
		used += 2
	}
	if m.filtering || m.filter != "" {
		used += 2
	}
	if m.showDetails() {
		used += 9
	}
	return max(3, bodyHeight(m.height)-used)
}

// clamp keeps cursor and scroll offset inside the visible list.
func (m *sharesScreen) clamp() {
	n := len(m.visible())
	m.cursor = max(0, min(m.cursor, n-1))
	window(n, m.cursor, m.listHeight(), &m.offset)
}

func (m *sharesScreen) nextPassword() {
	m.asking, m.password = nil, nil
	if len(m.askQueue) > 0 {
		m.asking, m.askQueue = m.askQueue[0], m.askQueue[1:]
	}
}

// ---- update -----------------------------------------------------------------

func (m *sharesScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.clamp()

	case resumeMsg:
		m.reload()
		m.msg.say(msg.note)
		return tea.Batch(m.refresh(), m.probe())

	case tickMsg:
		m.reload()
		if time.Since(m.lastProbe) > 15*time.Second {
			return tea.Batch(m.refresh(), m.probe())
		}
		return m.refresh()

	case spinMsg:
		if len(m.busy) == 0 {
			m.spinning = false
			return nil
		}
		m.frame = (m.frame + 1) % len(spinner)
		return spinTick()

	case statusMsg:
		if msg.err != nil {
			m.msg.fail(msg.err)
			return nil
		}
		m.statuses = msg.statuses
		if !m.loaded {
			m.loaded = true
			if key := m.startKey; key != "" {
				m.startKey = ""
				return m.bulk(key)
			}
		}

	case reachMsg:
		m.reach, m.reachKnown = msg, true

	case autoMsg:
		s := m.byID(msg.id)
		switch {
		case msg.err != nil:
			m.msg.fail(msg.err)
		case s != nil && msg.enabled:
			m.msg.say("Auto-connect enabled for " + s.Name)
		case s != nil:
			m.msg.say("Auto-connect disabled for " + s.Name)
		}

	case doneMsg:
		delete(m.busy, msg.id)
		if msg.err != nil {
			m.msg.fail(msg.err)
		} else if msg.text != "" {
			m.msg.say(msg.text)
		}
		if s := m.byID(msg.id); msg.askPassword && s != nil {
			if m.asking == nil {
				m.asking, m.password = s, nil
			} else if m.asking != s {
				m.askQueue = append(m.askQueue, s)
			}
		} else {
			delete(m.openAfter, msg.id)
		}
		if s := m.byID(msg.id); msg.forcePath != "" && s != nil {
			return tea.Batch(m.refresh(), m.probe(), push(forceDisconnect(m.backend, s, msg.forcePath, msg.err)))
		}
		return tea.Batch(m.refresh(), m.probe())

	case tea.KeyMsg:
		switch {
		case m.showHelp:
			m.showHelp = false
			return nil
		case m.asking != nil:
			return m.updatePassword(msg)
		case m.filtering:
			return m.updateFilter(msg)
		}
		return m.updateList(msg)
	}
	return nil
}

// bulk connects ("c") or disconnects ("x") everything shown.
func (m *sharesScreen) bulk(key string) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range m.visible() {
		st := m.statuses[s.ID]
		if m.busy[s.ID] != "" {
			continue
		}
		switch {
		case key == "c" && !st.Mounted():
			cmds = append(cmds, m.start(s.ID, "Connecting…"), m.connect(s, nil, false))
		case key == "x" && st.Mounted():
			cmds = append(cmds, m.start(s.ID, "Disconnecting…"), m.disconnect(s, st.Path))
		}
	}
	switch {
	case len(cmds) > 0:
		m.msg.say("")
	case key == "c":
		m.msg.say("Everything shown is already connected")
	default:
		m.msg.say("Nothing shown is connected")
	}
	return tea.Batch(cmds...)
}

func (m *sharesScreen) updateList(key tea.KeyMsg) tea.Cmd {
	visible := m.visible()

	switch key.String() {
	case "q":
		return tea.Quit
	case "esc":
		if m.filter == "" {
			return pop()
		}
		m.filter = ""
	case "?":
		m.showHelp = true
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= max(1, m.listHeight())
	case "pgdown":
		m.cursor += max(1, m.listHeight())
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(visible) - 1
	case "tab":
		if len(m.profiles) > 0 {
			m.tab, m.cursor = (m.tab+1)%(len(m.profiles)+1), 0
		}
	case "shift+tab":
		if len(m.profiles) > 0 {
			m.tab, m.cursor = (m.tab+len(m.profiles))%(len(m.profiles)+1), 0
		}
	case "/":
		m.filtering = true
	case "n":
		return push(addShareForm(m.backend, ""))
	case "r":
		m.msg.say("")
		return tea.Batch(m.refresh(), m.probe())
	case "c", "x":
		return m.bulk(key.String())
	}
	m.clamp()

	s := m.selected()
	if s == nil {
		return nil
	}
	if key.String() == "o" || key.String() == "right" || key.String() == "l" {
		return push(shareMenu(m.backend, s))
	}
	if m.busy[s.ID] != "" {
		return nil
	}
	st := m.statuses[s.ID]

	switch key.String() {
	case "enter", " ":
		m.msg.say("")
		if m.stale(s) {
			return push(forceDisconnect(m.backend, s, st.Path, nil))
		}
		if st.Mounted() {
			return tea.Batch(m.start(s.ID, "Disconnecting…"), m.disconnect(s, st.Path))
		}
		return tea.Batch(m.start(s.ID, "Connecting…"), m.connect(s, nil, false))
	case "d":
		if st.Mounted() {
			m.msg.say("")
			return tea.Batch(m.start(s.ID, "Disconnecting…"), m.disconnect(s, st.Path))
		}
	case "f":
		if st.Mounted() {
			return m.open(s, st.Path)
		}
		// Not connected yet: connect, then open.
		m.msg.say("")
		m.openAfter[s.ID] = true
		return tea.Batch(m.start(s.ID, "Connecting…"), m.connect(s, nil, true))
	case "a":
		return m.toggleAuto(s)
	}
	return nil
}

func (m *sharesScreen) updateFilter(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEsc:
		m.filter, m.filtering = "", false
	case tea.KeyEnter:
		m.filtering = false
	case tea.KeyUp:
		m.cursor--
	case tea.KeyDown:
		m.cursor++
	case tea.KeyBackspace:
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		} else {
			m.filtering = false
		}
	case tea.KeyCtrlU:
		m.filter = ""
	case tea.KeySpace:
		m.filter += " "
	case tea.KeyRunes:
		m.filter += string(key.Runes)
		m.cursor = 0
	}
	m.clamp()
	return nil
}

func (m *sharesScreen) updatePassword(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEsc:
		delete(m.openAfter, m.asking.ID)
		m.nextPassword()
		m.msg.say("Cancelled")
	case tea.KeyEnter:
		s, pw := m.asking, string(m.password)
		m.nextPassword()
		m.msg.say("")
		return tea.Batch(m.start(s.ID, "Connecting…"), m.connect(s, &pw, m.openAfter[s.ID]))
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
	return nil
}

// ---- view -------------------------------------------------------------------

func (m *sharesScreen) view(width, height int) string {
	if m.showHelp {
		return m.helpView()
	}
	var b strings.Builder

	if len(m.shares) == 0 {
		b.WriteString("  No network shares registered yet.\n\n")
		b.WriteString("  Press " + nameStyle.Render("N") + " to add one.\n\n")
		m.msg.render(&b)
		return b.String()
	}

	connected := 0
	for _, s := range m.shares {
		if m.statuses[s.ID].Mounted() {
			connected++
		}
	}
	counter := helpStyle.Render(fmt.Sprintf("%d of %d connected", connected, len(m.shares)))
	if n := countPaused(m.backend, m.shares); n > 0 {
		counter += helpStyle.Render(" · ") + warnStyle.Render(fmt.Sprintf("%d paused", n))
	}
	b.WriteString("  " + counter + "\n\n")

	if len(m.profiles) > 0 {
		tabs := []string{"All"}
		for _, p := range m.profiles {
			tabs = append(tabs, p.Name)
		}
		for i, t := range tabs {
			if i == m.tab {
				tabs[i] = tabStyle.Render(t)
			} else {
				tabs[i] = helpStyle.Render(t)
			}
		}
		b.WriteString("  " + strings.Join(tabs, "   ") + helpStyle.Render("     Tab switches") + "\n\n")
	}
	if m.filtering {
		b.WriteString("  Filter: " + m.filter + cursorStyle.Render("▏") + "\n\n")
	} else if m.filter != "" {
		b.WriteString("  Filter: " + m.filter + helpStyle.Render("   (Esc clears)") + "\n\n")
	}

	visible := m.visible()
	if len(visible) == 0 {
		b.WriteString("  " + helpStyle.Render("No shares match.") + "\n\n")
	} else {
		m.writeList(&b, visible)
	}

	if m.asking != nil {
		who := m.asking.Host
		if m.asking.Username != "" {
			who = m.asking.Username + "@" + who
		}
		if m.msg.text != "" && m.msg.isError {
			b.WriteString("  " + errStyle.Render(m.msg.text) + "\n")
		}
		fmt.Fprintf(&b, "  Password for %s: %s%s\n\n", who, strings.Repeat("•", len(m.password)), cursorStyle.Render("▏"))
		return b.String()
	}

	if s := m.selected(); s != nil && m.showDetails() {
		m.writeDetails(&b, s)
	}
	m.msg.render(&b)
	return b.String()
}

func (m *sharesScreen) writeList(b *strings.Builder, visible []*share.Share) {
	nameWidth, stateWidth := 0, 0
	states := make([]string, len(visible))
	for i, s := range visible {
		nameWidth = max(nameWidth, lipgloss.Width(s.Name))
		st := m.statuses[s.ID]
		switch {
		case m.busy[s.ID] != "":
			states[i] = m.busy[s.ID]
		case m.stale(s):
			states[i] = "Not responding"
		case m.unreachable(s):
			states[i] = "Unreachable"
		default:
			states[i] = st.String()
		}
		stateWidth = max(stateWidth, lipgloss.Width(states[i]))
	}

	first, last := window(len(visible), m.cursor, m.listHeight(), &m.offset)
	if first > 0 {
		b.WriteString("    " + helpStyle.Render(fmt.Sprintf("↑ %d more", first)) + "\n")
	}
	for i := first; i < last; i++ {
		s, st := visible[i], m.statuses[visible[i].ID]

		cursor := "  "
		name := fmt.Sprintf("%-*s", nameWidth, s.Name)
		if i == m.cursor {
			cursor = cursorStyle.Render("❯ ")
			name = nameStyle.Render(name)
		}

		state := fmt.Sprintf("%-*s", stateWidth, states[i])
		var dot string
		switch {
		case m.busy[s.ID] != "":
			dot, state = busyStyle.Render(spinner[m.frame]), busyStyle.Render(state)
		case m.stale(s):
			dot, state = warnStyle.Render("●"), warnStyle.Render(state)
		case st.Mounted():
			dot, state = onStyle.Render("●"), onStyle.Render(state)
		case m.unreachable(s):
			dot, state = offStyle.Render("○"), warnStyle.Render(state)
		default:
			dot, state = offStyle.Render("○"), offStyle.Render(state)
		}

		var extra []string
		if st.Mounted() && st.Total > 0 {
			extra = append(extra, mount.FormatBytes(st.Free)+" free")
		}
		_, paused := m.backend.Paused(s)
		if s.AutoConnect && !paused {
			extra = append(extra, "auto")
		}
		line := fmt.Sprintf("  %s%s %s   %s", cursor, dot, name, state)
		if len(extra) > 0 {
			line += "   " + helpStyle.Render(strings.Join(extra, " · "))
		}
		if paused {
			line += "   " + warnStyle.Render("auto paused")
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	if last < len(visible) {
		b.WriteString("    " + helpStyle.Render(fmt.Sprintf("↓ %d more", len(visible)-last)) + "\n")
	}
	b.WriteString("\n")
}

func accountOf(s *share.Share) string {
	switch {
	case s.Guest():
		return "guest"
	case s.Domain != "":
		return s.Domain + `\` + s.Username
	}
	return s.Username
}

func (m *sharesScreen) writeDetails(b *strings.Builder, s *share.Share) {
	st := m.statuses[s.ID]
	row := func(label, value string) {
		fmt.Fprintf(b, "  %s %s\n", helpStyle.Render(fmt.Sprintf("%-12s", label)), value)
	}

	where := s.MountPoint
	if st.State == mount.Elsewhere {
		where = share.CollapsePath(st.Path) + helpStyle.Render("  (mounted outside Mountkeeper)")
	}

	var options []string
	pause, paused := m.backend.Paused(s)
	if s.AutoConnect && !paused {
		options = append(options, "auto-connect")
	}
	if s.AutoOpen {
		options = append(options, "opens in Finder")
	}
	if len(options) == 0 {
		options = []string{helpStyle.Render("–")}
	}

	b.WriteString("  " + helpStyle.Render(strings.Repeat("─", 44)) + "\n")
	row("Address", s.URL())
	row("User", accountOf(s))
	row("Mount point", where)
	if m.stale(s) {
		row("Server", warnStyle.Render("does not answer — Enter force-disconnects, so it can connect again later"))
	} else if st.Mounted() && st.Total > 0 {
		row("Space", mount.FormatBytes(st.Free)+" free of "+mount.FormatBytes(st.Total))
	} else if m.unreachable(s) {
		row("Server", warnStyle.Render("does not answer — check the network or VPN"))
	}
	row("Options", strings.Join(options, " · "))
	if paused {
		row("Paused", warnStyle.Render("auto-connect stopped: "+pause.Text)+helpStyle.Render("  since "+pause.Since.Format("2006-01-02 15:04")))
		row("", helpStyle.Render(pauseFix(pause)))
	}
	var member []string
	for _, p := range m.profiles {
		for _, id := range p.IDs {
			if id == s.ID {
				member = append(member, p.Name)
			}
		}
	}
	if len(member) > 0 {
		row("Profiles", strings.Join(member, ", "))
	}
	b.WriteString("\n")
}

func (m *sharesScreen) helpView() string {
	keys := [][2]string{
		{"↑/↓  k/j", "Move the selection"},
		{"Enter  Space", "Connect or disconnect the selected share;\n" + strings.Repeat(" ", 17) + "force-disconnect it if its server no longer answers"},
		{"O  →", "Options: edit, password, auto-connect, remove, …"},
		{"F", "Open in Finder (connects first if necessary)"},
		{"C", "Connect all shares shown"},
		{"X", "Disconnect all shares shown"},
		{"A", "Toggle connecting automatically at login"},
		{"N", "Add a new share"},
		{"/", "Filter by name or server"},
		{"Tab", "Switch profile"},
		{"R", "Refresh now"},
		{"Esc", "Back"},
		{"Q", "Quit"},
	}
	var b strings.Builder
	for _, k := range keys {
		if k[0] == "Tab" && len(m.profiles) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s %s\n", nameStyle.Render(fmt.Sprintf("%-14s", k[0])), k[1])
	}
	b.WriteString("\n")
	return b.String()
}

// pauseFix says in the interface's words what ends a pause.
func pauseFix(p Pause) string {
	switch p.Code {
	case "kerberos-no-ticket":
		return "Sign in again (kinit or your company sign-in); it resumes by itself."
	case "kerberos-rejected":
		return "Choose another ticket or the password under O › Sign-in."
	}
	return "Store the password under O › Password; that resumes it."
}

func countPaused(b Backend, shares []*share.Share) int {
	n := 0
	for _, s := range shares {
		if _, ok := b.Paused(s); ok {
			n++
		}
	}
	return n
}
