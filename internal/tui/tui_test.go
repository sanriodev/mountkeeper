package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// fake is an in-memory Backend that records what the interface asked for.
type fake struct {
	shares     []*share.Share
	profiles   []Profile
	mounted    map[string]bool
	password   map[string]string // shares that need this password to connect
	stored     map[string]string // passwords "in the Keychain"
	opened     []string
	reach      map[string]bool
	connectErr error
	rules      []Rule
	// disconnectErr is returned by a disconnect that is not forced.
	disconnectErr error
	forced        bool
	paused        map[string]Pause
	repaired      bool
	tickets       []Ticket
	events        []history.Event
}

func newFake(ids ...string) *fake {
	f := &fake{mounted: map[string]bool{}, password: map[string]string{}, stored: map[string]string{}}
	for _, id := range ids {
		f.shares = append(f.shares, &share.Share{ID: id, Name: id, Host: id + ".example.com", Path: "/" + id, Username: "alice", MountPoint: "~/Network/" + id})
	}
	return f
}

func (f *fake) Shares() []*share.Share { return f.shares }
func (f *fake) Profiles() []Profile    { return f.profiles }
func (f *fake) Statuses() (map[string]mount.Status, error) {
	st := map[string]mount.Status{}
	for id, on := range f.mounted {
		if on {
			st[id] = mount.Status{State: mount.Connected, Path: "/mnt/" + id, Total: 500e9, Free: 120e9}
		}
	}
	return st, nil
}
func (f *fake) Reachable() map[string]bool { return f.reach }
func (f *fake) Paused(s *share.Share) (Pause, bool) {
	p, ok := f.paused[s.ID]
	return p, ok
}
func (f *fake) Connect(s *share.Share, password *string) (string, error) {
	if f.connectErr != nil {
		return "", f.connectErr
	}
	if want, needs := f.password[s.ID]; needs {
		if password == nil {
			return "", ErrPasswordRequired
		}
		if *password != want {
			return "", mount.ErrAuth
		}
	}
	f.mounted[s.ID] = true
	return "/mnt/" + s.ID, nil
}
func (f *fake) Disconnect(s *share.Share, path string, force bool) error {
	if f.disconnectErr != nil && !force {
		return f.disconnectErr
	}
	f.forced = f.forced || force
	f.mounted[s.ID] = false
	return nil
}
func (f *fake) Open(path string) error { f.opened = append(f.opened, path); return nil }
func (f *fake) AddShare(form ShareForm) (*share.Share, []string, error) {
	s, err := share.ParseURL(form.URL)
	if err != nil {
		return nil, nil, err
	}
	if form.Name != "" {
		s.Name, s.ID = form.Name, share.Slug(form.Name)
	}
	s.Username, s.AutoOpen, s.AutoConnect = form.User, form.AutoOpen, form.AutoConnect
	if form.Password != "" && form.Keychain {
		f.stored[s.ID] = form.Password
	}
	f.shares = append(f.shares, s)
	return s, nil, nil
}
func (f *fake) EditShare(s *share.Share, form ShareForm) ([]string, error) {
	if form.Name == "" {
		return nil, errors.New("the name must not be empty")
	}
	s.Name, s.MountPoint = form.Name, form.MountPoint
	return nil, nil
}
func (f *fake) RemoveShare(s *share.Share) ([]string, error) {
	for i, o := range f.shares {
		if o == s {
			f.shares = append(f.shares[:i], f.shares[i+1:]...)
		}
	}
	delete(f.mounted, s.ID)
	return nil, nil
}
func (f *fake) UserField(s *share.Share) string              { return s.Username }
func (f *fake) SetAutoConnect(s *share.Share, on bool) error { s.AutoConnect = on; return nil }
func (f *fake) SetAutoOpen(s *share.Share, on bool) error    { s.AutoOpen = on; return nil }
func (f *fake) SetPassword(s *share.Share, pw string) error  { f.stored[s.ID] = pw; return nil }
func (f *fake) RemovePassword(s *share.Share) error          { delete(f.stored, s.ID); return nil }
func (f *fake) DescribeRule(rule string) string              { return "network " + rule }
func (f *fake) Network() NetworkInfo {
	return NetworkInfo{Rows: [][2]string{{"Interface", "en0"}}, Rules: f.rules}
}
func (f *fake) FindServers() ([]Server, error) {
	return []Server{{Name: "Office NAS", Host: "nas.local"}}, nil
}
func (f *fake) History(id string, n int) []history.Event { return f.events }
func (f *fake) Doctor() []Check {
	if f.repaired {
		return []Check{{Group: "Agent", Label: "Background agent", Detail: "loaded"}}
	}
	return []Check{
		{Group: "Agent", Label: "Background agent", Level: CheckWarn, Detail: "written by an older version", Hint: "mountkeeper doctor --fix", Fix: true},
		{Group: "Servers", Label: "fs.example.com", Level: CheckFail, Detail: "the host name cannot be resolved", Hint: "Connect the VPN"},
	}
}
func (f *fake) RepairAgent() error                       { f.repaired = true; return nil }
func (f *fake) Tickets(s *share.Share) ([]Ticket, error) { return f.tickets, nil }
func (f *fake) SetSignIn(s *share.Share, method, principal string) ([]string, error) {
	if method == "kerberos" {
		s.Auth, s.KerberosPrincipal = share.AuthKerberos, principal
		return []string{"The stored password stays in the Keychain for switching back."}, nil
	}
	s.Auth, s.KerberosPrincipal = "", ""
	return nil, nil
}
func (f *fake) PasswordState(s *share.Share) string {
	if _, ok := f.stored[s.ID]; ok {
		return "stored in Keychain"
	}
	return "not stored yet"
}
func (f *fake) SaveProfile(name string, ids []string) error {
	for i := range f.profiles {
		if f.profiles[i].Name == name {
			f.profiles[i].IDs = ids
			return nil
		}
	}
	f.profiles = append(f.profiles, Profile{Name: name, IDs: ids})
	return nil
}
func (f *fake) RemoveProfile(name string) error {
	for i := range f.profiles {
		if f.profiles[i].Name == name {
			f.profiles = append(f.profiles[:i], f.profiles[i+1:]...)
			return nil
		}
	}
	return nil
}
func (f *fake) SetProfileAuto(name string, rules []string) error {
	for i := range f.profiles {
		if f.profiles[i].Name == name {
			f.profiles[i].Auto = rules
		}
	}
	return nil
}

// harness drives an app the way Bubble Tea would, without a terminal.
type harness struct {
	t    *testing.T
	app  *app
	quit bool
}

// settle runs a command and everything it leads to, feeding the resulting
// messages back into the app. Timers (ticks, spinner) are not awaited.
func (h *harness) settle(cmd tea.Cmd) {
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		var msg tea.Msg
		select {
		case msg = <-ch:
		case <-time.After(30 * time.Millisecond):
			continue // a timer
		}
		switch m := msg.(type) {
		case tea.BatchMsg:
			pending = append(pending, m...)
		case tea.QuitMsg:
			h.quit = true
		default:
			_, more := h.app.Update(msg)
			pending = append(pending, more)
		}
	}
}

var namedKeys = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft,
	"right": tea.KeyRight, "tab": tea.KeyTab, "backspace": tea.KeyBackspace, "space": tea.KeySpace, "ctrl+s": tea.KeyCtrlS,
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		if t, ok := namedKeys[k]; ok {
			msg = tea.KeyMsg{Type: t}
			if k == "space" {
				msg.Runes = []rune{' '}
			}
		}
		_, cmd := h.app.Update(msg)
		h.settle(cmd)
	}
}

func (h *harness) view() string { return h.app.View() }

// choose moves the cursor of the menu on top to the item with this label.
func (h *harness) choose(label string) {
	h.t.Helper()
	m, ok := h.app.top().(*menuScreen)
	if !ok {
		h.t.Fatalf("top screen is %T, not a menu", h.app.top())
	}
	for i, it := range m.items {
		if it.label == label {
			m.cursor = i
			return
		}
	}
	h.t.Fatalf("menu has no item %q:\n%s", label, h.view())
}

func (h *harness) shares() *sharesScreen {
	s, ok := h.app.top().(*sharesScreen)
	if !ok {
		h.t.Fatalf("top screen is %T, not the share list\n%s", h.app.top(), h.view())
	}
	return s
}

func (h *harness) expect(parts ...string) {
	h.t.Helper()
	view := h.view()
	for _, p := range parts {
		if !strings.Contains(view, p) {
			h.t.Fatalf("screen lacks %q:\n%s", p, view)
		}
	}
}

func start(t *testing.T, first screen) *harness {
	h := &harness{t: t, app: &app{stack: []screen{first}}}
	h.settle(h.app.Init())
	return h
}

func startShares(t *testing.T, f *fake) *harness { return start(t, newSharesScreen(f)) }

// ---- connection manager -----------------------------------------------------

func TestConnectAndDisconnect(t *testing.T) {
	f := newFake("projects", "development", "media")
	h := startShares(t, f)

	h.press("down", "enter")
	m := h.shares()
	if !f.mounted["development"] || !m.statuses["development"].Mounted() {
		t.Fatalf("development not connected: %+v", f.mounted)
	}
	if !strings.Contains(m.msg.text, "Connected development") || m.msg.isError || len(m.busy) != 0 {
		t.Errorf("message = %q busy = %v", m.msg.text, m.busy)
	}
	h.expect("Enter Disconnect", "120 GB free", "1 of 3 connected")

	h.press("enter")
	if f.mounted["development"] || !strings.Contains(m.msg.text, "Disconnected") {
		t.Errorf("not disconnected: %v %q", f.mounted, m.msg.text)
	}
}

func TestPasswordPrompt(t *testing.T) {
	f := newFake("projects", "development", "media")
	f.password["projects"] = "s3cret"
	h := startShares(t, f)
	m := h.shares()

	h.press("enter")
	if m.asking == nil || m.asking.ID != "projects" {
		t.Fatal("no password prompt")
	}
	// While asking, letters are password input and not commands.
	h.press("q", "x", "backspace", "backspace")
	if h.quit || m.asking == nil || len(m.password) != 0 {
		t.Fatalf("password editing broken: quit=%v %q", h.quit, string(m.password))
	}
	h.expect("Password for alice@projects.example.com")

	// A wrong password reports the failure and asks again.
	h.press("hunter2zz", "enter")
	if f.mounted["projects"] || m.asking == nil || !m.msg.isError {
		t.Fatalf("wrong password: mounted=%v asking=%v err=%v", f.mounted["projects"], m.asking, m.msg.isError)
	}
	if strings.Contains(h.view(), "hunter2zz") {
		t.Error("the typed password is visible")
	}

	h.press("s3cret", "enter")
	if !f.mounted["projects"] || m.asking != nil {
		t.Fatalf("correct password did not connect: %+v", f.mounted)
	}

	f.password["media"] = "x"
	h.press("down", "down", "enter", "esc")
	if m.asking != nil || f.mounted["media"] || m.msg.text != "Cancelled" {
		t.Errorf("cancel: asking=%v message=%q", m.asking, m.msg.text)
	}
}

func TestFinderConnectsFirst(t *testing.T) {
	f := newFake("projects")
	f.password["projects"] = "pw"
	h := startShares(t, f)

	h.press("f", "pw", "enter")
	if !f.mounted["projects"] || len(f.opened) != 1 || f.opened[0] != "/mnt/projects" {
		t.Fatalf("mounted=%v opened=%v", f.mounted, f.opened)
	}
	h.press("f") // already connected: just open
	if len(f.opened) != 2 {
		t.Errorf("opened = %v", f.opened)
	}
}

func TestBulkFilterAndTabs(t *testing.T) {
	f := newFake("projects", "development", "media")
	f.profiles = []Profile{{Name: "office", IDs: []string{"projects", "development"}}, {Name: "home", IDs: []string{"media"}}}
	h := startShares(t, f)
	m := h.shares()

	// Filter: typed letters are not commands (c = connect all).
	h.press("/", "dev", "c")
	if len(f.mounted) != 0 || m.filter != "devc" {
		t.Fatalf("filter input leaked into commands: %v %q", f.mounted, m.filter)
	}
	h.expect("No shares match")
	h.press("backspace", "enter", "c") // keep "dev", then connect what is shown
	if !f.mounted["development"] || f.mounted["projects"] || f.mounted["media"] {
		t.Fatalf("C ignored the filter: %v", f.mounted)
	}
	// The first Esc clears the filter instead of leaving.
	h.press("esc")
	if m.filter != "" || len(m.visible()) != 3 || h.quit {
		t.Fatal("esc did not just clear the filter")
	}

	h.press("tab", "tab")
	if v := m.visible(); len(v) != 1 || v[0].ID != "media" {
		t.Fatalf("home tab shows %d shares", len(v))
	}
	h.press("c", "tab")
	if !f.mounted["media"] || f.mounted["projects"] || m.tab != 0 {
		t.Errorf("C on the home tab: %v, tab %d", f.mounted, m.tab)
	}
	h.press("x")
	for id, on := range f.mounted {
		if on {
			t.Errorf("%s still connected after X", id)
		}
	}
	h.press("x")
	if m.msg.text != "Nothing shown is connected" {
		t.Errorf("message = %q", m.msg.text)
	}
}

func TestUnreachableAutoAndErrors(t *testing.T) {
	f := newFake("projects", "development", "media")
	f.reach = map[string]bool{"projects": true, "development": false, "media": true}
	h := startShares(t, f)
	m := h.shares()

	h.press("a")
	if !f.shares[0].AutoConnect || !strings.Contains(m.msg.text, "enabled") {
		t.Errorf("toggle: %v %q", f.shares[0].AutoConnect, m.msg.text)
	}
	if view := h.view(); strings.Count(view, "Unreachable") != 1 || !strings.Contains(view, "auto") {
		t.Errorf("exactly development should be unreachable, projects auto:\n%s", view)
	}

	f.connectErr = hinted{errors.New("the server does not answer"), "Check the VPN."}
	h.press("enter")
	if !m.msg.isError {
		t.Fatal("error not reported")
	}
	h.expect("the server does not answer", "Check the VPN.")
}

type hinted struct {
	error
	hint string
}

func (h hinted) Hint() string { return h.hint }

func TestScrollingAndWidth(t *testing.T) {
	f := newFake()
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("share%02d", i)
		f.shares = append(f.shares, &share.Share{ID: id, Name: id, Host: "h.example.com", Path: "/" + id,
			MountPoint: "/a/very/long/path/" + strings.Repeat("deep/", 30) + id})
	}
	h := startShares(t, f)
	h.app.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	m := h.shares()

	for i := 0; i < 39; i++ {
		h.press("down")
		view := h.view()
		if got := strings.Count(view, "\n"); got > 24 {
			t.Fatalf("view is %d lines tall in a 24-line window at cursor %d", got, m.cursor)
		}
		if !strings.Contains(view, "❯") || !strings.Contains(view, f.shares[m.cursor].Name) {
			t.Fatalf("selected share %s scrolled out of view", f.shares[m.cursor].Name)
		}
		for n, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > 70 {
				t.Fatalf("line %d is %d columns wide in a 70-column window", n, w)
			}
		}
	}
	h.press("down", "g")
	if m.cursor != 0 || m.offset != 0 {
		t.Errorf("after g: cursor %d offset %d", m.cursor, m.offset)
	}
	h.press("?")
	h.expect("Options: edit, password", "Press any key")
	h.press("q")
	if m.showHelp || h.quit {
		t.Error("the key that closes help must not act")
	}
}

// ---- menus ------------------------------------------------------------------

func TestMainMenuNavigation(t *testing.T) {
	f := newFake("projects")
	h := start(t, mainMenu(f))
	h.expect("Shares", "0 of 1 connected", "Add a share", "Profiles", "Find servers", "Network", "Check", "History", "Quit")

	h.press("enter")
	h.shares()
	h.expect("Mountkeeper", "Shares", "projects")
	h.press("esc")
	if _, ok := h.app.top().(*menuScreen); !ok || h.quit {
		t.Fatalf("Esc should lead back to the menu, top is %T", h.app.top())
	}

	// Each entry opens its screen and Esc returns.
	for _, entry := range []string{"Add a share", "Profiles", "Find servers", "Network", "Check", "History"} {
		h.press("down", "enter")
		if len(h.app.stack) != 2 {
			t.Fatalf("%s did not open a screen", entry)
		}
		h.expect(entry)
		h.press("esc")
		if len(h.app.stack) != 1 {
			t.Fatalf("Esc did not return from %s", entry)
		}
	}
	h.press("down", "enter") // Quit
	if !h.quit {
		t.Error("the Quit entry did not quit")
	}
}

func TestMainMenuLogo(t *testing.T) {
	h := start(t, mainMenu(newFake("projects")))
	items := []string{"Shares", "Add a share", "Profiles", "Find servers", "Network", "History", "Quit"}

	// A standard 80×24 terminal has room for the logo and every item.
	h.app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.expect(append([]string{"┏┳┓┏━┓", logoTagline}, items...)...)
	if got := strings.Count(h.view(), "\n"); got > 24 {
		t.Fatalf("view is %d lines tall in a 24-line window:\n%s", got, h.view())
	}

	// Too low or too narrow: the logo gives way, the items stay.
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 20}, {Width: 50, Height: 40}} {
		h.app.Update(size)
		if view := h.view(); strings.Contains(view, logoTagline) {
			t.Fatalf("logo drawn in a %d×%d window:\n%s", size.Width, size.Height, view)
		}
		h.expect(items...)
	}
}

func TestEscOnRootQuits(t *testing.T) {
	h := start(t, mainMenu(newFake()))
	h.press("esc")
	if !h.quit {
		t.Error("Esc on the main menu should quit")
	}
}

func TestAddShareForm(t *testing.T) {
	f := newFake()
	h := start(t, mainMenu(f))
	h.press("down", "enter")
	h.expect("Address", "Store password in Keychain", "[ Add share ]")

	// An invalid address keeps the form open and explains.
	h.press("nonsense", "ctrl+s")
	if len(f.shares) != 0 || len(h.app.stack) != 2 {
		t.Fatal("invalid address accepted")
	}
	h.expect("not an SMB URL")

	form := h.app.top().(*formScreen)
	form.fields[0].input.SetValue("")
	// Address, name, user, password; then toggle "open in Finder" two fields on.
	h.press("smb://nas.example.com/media", "down", "Media", "down", "alice", "down", "pw-123", "down", "down", "down", "space")
	if strings.Contains(h.view(), "pw-123") {
		t.Error("the password is visible in the form")
	}
	h.press("ctrl+s")
	if len(f.shares) != 1 {
		t.Fatalf("share not added:\n%s", h.view())
	}
	s := f.shares[0]
	if s.ID != "media" || s.Host != "nas.example.com" || s.Username != "alice" || !s.AutoOpen || s.AutoConnect {
		t.Errorf("share = %+v", s)
	}
	if f.stored["media"] != "pw-123" {
		t.Errorf("password not stored: %v", f.stored)
	}
	if len(h.app.stack) != 1 {
		t.Fatal("form did not close")
	}
	h.expect("Added Media", "0 of 1 connected")
}

func TestShareOptions(t *testing.T) {
	f := newFake("projects", "media")
	f.profiles = []Profile{{Name: "office"}}
	h := startShares(t, f)

	h.press("o")
	h.expect("projects", "Connect", "Open in Finder", "Edit", "Password", "Connect automatically", "Profiles", "History", "Remove")

	h.press("enter") // Connect
	if !f.mounted["projects"] {
		t.Fatal("Connect from the options menu did nothing")
	}
	h.expect("Disconnect", "Connected at /mnt/projects")

	// Toggles act in place and show the new state.
	h.choose("Connect automatically")
	h.press("enter")
	if !f.shares[0].AutoConnect {
		t.Fatal("auto-connect not toggled")
	}
	h.expect("Automatic connecting is now on")
	h.press("down", "enter")
	if !f.shares[0].AutoOpen {
		t.Fatal("auto-open not toggled")
	}

	// Profiles membership.
	h.press("down", "enter", "space", "enter")
	if got := f.profiles[0].IDs; len(got) != 1 || got[0] != "projects" {
		t.Fatalf("profile membership = %v", got)
	}
	h.expect("Profiles updated")

	// Edit.
	h.choose("Edit")
	h.press("enter")
	form := h.app.top().(*formScreen)
	form.fields[0].input.SetValue("Projects 2")
	h.press("ctrl+s")
	if f.shares[0].Name != "Projects 2" {
		t.Fatalf("name = %q", f.shares[0].Name)
	}
	h.expect("Saved Projects 2")

	// Remove asks first; "No" is the default.
	h.press("G", "enter")
	h.expect("Remove Projects 2?", "will be disconnected first")
	h.press("enter")
	if len(f.shares) != 2 {
		t.Fatal("Enter on the default answer removed the share")
	}
	h.press("G", "enter", "y")
	if len(f.shares) != 1 || f.shares[0].ID != "media" {
		t.Fatalf("share not removed: %d left", len(f.shares))
	}
	h.shares() // back on the list, two levels up
	h.expect("Removed Projects 2", "media")
}

func TestPasswordMenu(t *testing.T) {
	f := newFake("projects")
	h := start(t, shareMenu(f, f.shares[0]))
	h.press("down", "down", "down", "enter")
	h.expect("Currently: not stored yet", "Set password", "Remove stored password")

	h.press("enter", "new-secret", "enter", "enter")
	if f.stored["projects"] != "new-secret" {
		t.Fatalf("password not stored: %v\n%s", f.stored, h.view())
	}
	if strings.Contains(h.view(), "new-secret") {
		t.Error("password visible")
	}
	if len(h.app.stack) != 1 {
		t.Fatalf("expected to be back on the share menu, depth %d", len(h.app.stack))
	}
	h.expect("Password saved", "stored in Keychain")

	h.press("enter", "down", "enter", "y")
	if _, still := f.stored["projects"]; still {
		t.Fatal("password not removed")
	}
	h.expect("Password removed", "not stored yet")

	// A share without a user has no password to manage.
	f.shares[0].Username = ""
	h.press("enter")
	h.expect("guest access")
}

func TestMenuConnectAsksForPassword(t *testing.T) {
	f := newFake("projects")
	f.password["projects"] = "pw"
	h := start(t, shareMenu(f, f.shares[0]))

	h.press("enter")
	h.expect("Password for alice@projects.example.com")
	h.press("bad", "enter", "enter")
	if f.mounted["projects"] {
		t.Fatal("connected with a wrong password")
	}
	h.expect("authentication failed")
	form := h.app.top().(*formScreen)
	form.fields[0].input.SetValue("")
	h.press("up", "pw", "ctrl+s")
	if !f.mounted["projects"] || len(h.app.stack) != 1 {
		t.Fatalf("mounted=%v depth=%d\n%s", f.mounted, len(h.app.stack), h.view())
	}
	h.expect("Disconnect")
}

func TestProfiles(t *testing.T) {
	f := newFake("projects", "development", "media")
	f.rules = []Rule{{Value: "domain:corp.example.com", Label: "network domain corp.example.com"}}
	h := start(t, profilesMenu(f))
	h.expect("A profile is a group of shares", "New profile")

	// Create: name, then choose the shares.
	h.press("n", "office", "ctrl+s")
	h.expect("Shares in profile office")
	h.press("space", "down", "space", "enter")
	if len(f.profiles) != 1 || strings.Join(f.profiles[0].IDs, ",") != "projects,development" {
		t.Fatalf("profiles = %+v", f.profiles)
	}
	h.expect("office", "2 shares")

	// A second profile of the same name is refused.
	h.press("n", "office", "ctrl+s")
	h.expect("already exists")
	h.press("esc")

	h.press("enter")
	h.expect("Connect all", "Disconnect all", "Connect automatically", "Delete profile")

	// Bind to the current network.
	h.press("down", "down", "down", "enter")
	h.expect("Off", "On network domain corp.example.com")
	h.press("down", "enter")
	if got := f.profiles[0].Auto; len(got) != 1 || got[0] != "domain:corp.example.com" {
		t.Fatalf("auto = %v", got)
	}
	h.expect("connects automatically on network domain corp.example.com")

	// Connect all opens the list on the profile's tab and connects it.
	h.press("g", "enter")
	m := h.shares()
	if m.tab != 1 || !f.mounted["projects"] || !f.mounted["development"] || f.mounted["media"] {
		t.Fatalf("connect all: tab %d mounted %v", m.tab, f.mounted)
	}
	h.press("esc")

	// Delete.
	h.press("G", "enter", "y")
	if len(f.profiles) != 0 {
		t.Fatal("profile not deleted")
	}
	h.expect("Deleted profile office")
}

func TestServersNetworkHistory(t *testing.T) {
	f := newFake("projects")
	f.events = []history.Event{
		{Time: time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local), Share: "projects", Action: history.Connected, Trigger: history.Auto, Detail: "/mnt/projects"},
		{Time: time.Date(2026, 11, 16, 17, 0, 0, 0, time.Local), Share: "projects", Action: history.Failed, Trigger: history.Manual, Detail: "authentication failed"},
	}
	f.profiles = []Profile{{Name: "office", Auto: []string{"domain:corp.example.com"}}}
	f.rules = []Rule{{Value: "domain:corp.example.com", Label: "network domain corp.example.com"}}

	h := start(t, serversMenu(f))
	h.expect("Office NAS", "smb://nas.local", "Add a share by address")
	h.press("enter")
	form, ok := h.app.top().(*formScreen)
	if !ok || form.fields[0].input.Value() != "smb://nas.local/" {
		t.Fatalf("server not carried into the add form")
	}

	h = start(t, networkScreen(f))
	h.expect("Interface", "en0", "office", "active now")

	h = start(t, historyScreen(f, nil))
	view := h.view()
	if !strings.Contains(view, "authentication failed") || !strings.Contains(view, "automatically") {
		t.Fatalf("history incomplete:\n%s", view)
	}
	if strings.Index(view, "failed") > strings.Index(view, "connected ") {
		t.Errorf("newest entry should come first:\n%s", view)
	}
	h.press("esc")
	if !h.quit {
		t.Error("Esc on the only screen should quit")
	}
}

func TestNotRespondingAndForceDisconnect(t *testing.T) {
	f := newFake("projects", "media")
	f.mounted["projects"] = true
	f.reach = map[string]bool{"projects": false, "media": true}
	h := startShares(t, f)

	h.expect("Not responding", "Enter Force disconnect", "does not answer")
	h.press("enter")
	h.expect("Force disconnect projects?", "no longer answers", "unsaved changes")
	if _, ok := h.app.top().(*confirmScreen); !ok {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("enter") // "No" is preselected
	if !f.mounted["projects"] {
		t.Fatal("Enter on the preselected No disconnected anyway")
	}
	h.press("enter", "y")
	if f.mounted["projects"] || !f.forced {
		t.Fatalf("not force-disconnected: mounted %v, forced %v", f.mounted["projects"], f.forced)
	}
	h.shares()
	h.expect("Disconnected projects")
}

func TestBusyDisconnectOffersForce(t *testing.T) {
	f := newFake("projects")
	f.mounted["projects"] = true
	f.reach = map[string]bool{"projects": true}
	f.disconnectErr = &mount.BusyError{Path: "/mnt/projects", Processes: []string{"Preview (PID 812)"}}
	h := startShares(t, f)

	h.press("enter")
	h.expect("Force disconnect projects?", "Preview (PID 812)")
	h.press("y")
	if f.mounted["projects"] || !f.forced {
		t.Fatal("forcing after a busy disconnect did not work")
	}
}

func TestShareOptionsForceDisconnect(t *testing.T) {
	f := newFake("projects")
	f.mounted["projects"] = true
	h := startShares(t, f)
	h.press("o")
	h.expect("Force disconnect")
	h.choose("Force disconnect")
	h.press("enter", "y")
	if f.mounted["projects"] || !f.forced {
		t.Fatal("Force disconnect from the options did not force")
	}
}

func TestPausedShareStandsOut(t *testing.T) {
	f := newFake("projects", "media")
	f.shares[0].AutoConnect = true
	f.paused = map[string]Pause{"projects": {Code: "rejected", Text: "password rejected", Since: time.Date(2026, 11, 16, 8, 57, 0, 0, time.Local)}}
	h := startShares(t, f)
	h.expect("1 paused", "auto paused", "auto-connect stopped: password rejected", "since 2026-11-16 08:57", "O › Password")

	main := start(t, mainMenu(f))
	main.expect("0 of 2 connected · 1 paused")
}

func TestDoctorScreen(t *testing.T) {
	f := newFake("projects")
	h := start(t, doctorScreen(f))
	h.expect("2 findings need attention", "! Agent › Background agent", "written by an older version",
		"✗ Servers › fs.example.com", "Enter repairs the background agent")
	h.press("down")
	h.expect("→ Connect the VPN")
	h.press("up", "enter")
	if !f.repaired {
		t.Fatal("Enter did not repair the agent")
	}
	h.expect("Background agent repaired", "Everything looks fine.", "✓ Agent › Background agent")
}

func TestSignInWithKerberos(t *testing.T) {
	f := newFake("projects")
	f.tickets = []Ticket{{Principal: "alice@CORP.EXAMPLE.COM", Expires: time.Date(2026, 11, 18, 18, 15, 0, 0, time.Local), Valid: true, Matches: true, Used: true}}
	h := startShares(t, f)
	h.press("o")
	h.expect("Sign-in", "password from the Keychain")
	h.choose("Sign-in")
	h.press("enter")
	h.expect("1 Kerberos ticket found", "● Password from the Keychain", "○ Kerberos · alice@CORP.EXAMPLE.COM",
		"valid until 2026-11-18 18:15 · suits this share · already signed in to projects.example.com")

	h.press("down", "enter")
	s := f.shares[0]
	if !s.Kerberos() || s.KerberosPrincipal != "alice@CORP.EXAMPLE.COM" {
		t.Fatalf("not switched: %+v", s)
	}
	h.expect("signs in with the Kerberos ticket of alice@CORP.EXAMPLE.COM", "stays in the Keychain",
		"○ Password from the Keychain", "● Kerberos · alice@CORP.EXAMPLE.COM")

	// The ticket disappears: the choice stays and says so.
	f.tickets = nil
	h.press("r")
	h.expect("No Kerberos tickets found", "● Kerberos · alice@CORP.EXAMPLE.COM", "no ticket right now")

	h.press("home", "enter")
	if s.Kerberos() {
		t.Fatal("switching back to the password did not work")
	}
	h.press("esc")
	h.expect("password from the Keychain")
}
