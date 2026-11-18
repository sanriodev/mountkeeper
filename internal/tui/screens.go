package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func withNotes(text string, notes []string) string {
	if len(notes) == 0 {
		return text
	}
	return text + "\n" + strings.Join(notes, "\n")
}

// act runs fn in the background and reports its outcome on the current menu.
func act(done string, fn func() error) tea.Cmd {
	return func() tea.Msg {
		if err := fn(); err != nil {
			return noteMsg{err: err}
		}
		return noteMsg{text: done}
	}
}

// ---- main menu --------------------------------------------------------------

func mainMenu(b Backend) *menuScreen {
	m := &menuScreen{root: true, banner: logo}
	m.build = func() []menuItem {
		shares, profiles := b.Shares(), b.Profiles()
		state := "none yet"
		if len(shares) > 0 {
			st, _ := b.Statuses()
			connected := 0
			for _, s := range shares {
				if st[s.ID].Mounted() {
					connected++
				}
			}
			state = fmt.Sprintf("%d of %d connected", connected, len(shares))
			if n := countPaused(b, shares); n > 0 {
				state += " · " + warnStyle.Render(fmt.Sprintf("%d paused", n))
			}
		}
		profileState := "none yet"
		if len(profiles) > 0 {
			profileState = plural(len(profiles), "profile", "profiles")
		}
		return []menuItem{
			{label: "Shares", value: state,
				desc: "Connect and disconnect your network shares, open them in Finder,\nand change their settings, passwords and automatic connecting.",
				run:  func() tea.Cmd { return push(newSharesScreen(b)) }},
			{label: "Add a share",
				desc: "Register a new network share by its address (smb://server/share).",
				run:  func() tea.Cmd { return push(addShareForm(b, "")) }},
			{label: "Profiles", value: profileState,
				desc: "Group shares that belong together, connect them at once,\nor automatically whenever you are on a certain network.",
				run:  func() tea.Cmd { return push(profilesMenu(b)) }},
			{label: "Find servers",
				desc: "Look for file servers that announce themselves on the local network.",
				run:  func() tea.Cmd { return push(serversMenu(b)) }},
			{label: "Network",
				desc: "Show the network this Mac is on and which profiles it activates.",
				run:  func() tea.Cmd { return push(networkScreen(b)) }},
			{label: "Check",
				desc: "Check the background agent, the Keychain, the servers and the shares,\nand say what to do about anything that is wrong.",
				run:  func() tea.Cmd { return push(doctorScreen(b)) }},
			{label: "History",
				desc: "See when shares were connected or disconnected, and what failed.",
				run:  func() tea.Cmd { return push(historyScreen(b, nil)) }},
			{label: "Quit", run: func() tea.Cmd { return tea.Quit }},
		}
	}
	return m
}

// ---- adding and editing shares ----------------------------------------------

func addShareForm(b Backend, url string) *formScreen {
	return newForm("Add a share", "", "Add share",
		func(v formValues) (tea.Cmd, error) {
			s, notes, err := b.AddShare(ShareForm{
				URL:         v.text("url"),
				Name:        v.text("name"),
				User:        v.text("user"),
				Password:    v.raw("password"),
				Keychain:    v.on("keychain"),
				MountPoint:  v.text("mount"),
				AutoOpen:    v.on("open"),
				AutoConnect: v.on("auto"),
			})
			if err != nil {
				return nil, err
			}
			return popWith(1, withNotes("Added "+s.Name, notes)), nil
		},
		textField("url", "Address", url, "smb://server/share", "The address of the share, as you would enter it in Finder's \"Connect to Server\"."),
		textField("name", "Name", "", "(the share name)", "A short name to call the share by. Leave empty to use the share name."),
		textField("user", "User", "", "(guest)", "Your account on the server, optionally as DOMAIN;user. Leave empty for guest access."),
		secretField("password", "Password", "Stored in the macOS Keychain only. Leave empty to be asked on the first connect."),
		toggleField("keychain", "Store password in Keychain", true, "If off, the password is asked on every connect."),
		textField("mount", "Mount point", "", "(~/Network/<name>)", "The folder the share appears in. Leave empty for ~/Network/<name>."),
		toggleField("open", "Open Finder after connecting", false, ""),
		toggleField("auto", "Connect automatically at login", false, "Needs a stored password (or guest access)."),
	)
}

func editShareForm(b Backend, s *share.Share) *formScreen {
	return newForm("Edit", "The address, user and mount point can only be changed while the share is disconnected.", "Save",
		func(v formValues) (tea.Cmd, error) {
			notes, err := b.EditShare(s, ShareForm{
				URL:        v.text("url"),
				Name:       v.text("name"),
				User:       v.text("user"),
				MountPoint: v.text("mount"),
				AutoOpen:   s.AutoOpen,
			})
			if err != nil {
				return nil, err
			}
			return popWith(1, withNotes("Saved "+s.Name, notes)), nil
		},
		textField("name", "Name", s.Name, "", "Only the display name changes; commands keep using the id \""+s.ID+"\"."),
		textField("url", "Address", s.URL(), "smb://server/share", ""),
		textField("user", "User", b.UserField(s), "(guest)", "Optionally as DOMAIN;user. Leave empty for guest access."),
		textField("mount", "Mount point", s.MountPoint, "", "The folder the share appears in."),
	)
}

// connectCmd connects a share from a menu. If a password is needed, the
// password prompt is pushed; it then connects itself.
func connectCmd(b Backend, s *share.Share, thenOpen bool) tea.Cmd {
	return func() tea.Msg {
		path, err := b.Connect(s, nil)
		if errors.Is(err, ErrPasswordRequired) {
			return pushMsg{passwordPrompt(b, s, thenOpen)}
		}
		if err != nil {
			return noteMsg{err: err}
		}
		if thenOpen || s.AutoOpen {
			if err := b.Open(path); err != nil {
				return noteMsg{err: err}
			}
		}
		return noteMsg{text: "Connected at " + share.CollapsePath(path)}
	}
}

func passwordPrompt(b Backend, s *share.Share, thenOpen bool) *formScreen {
	who := s.Host
	if s.Username != "" {
		who = s.Username + "@" + who
	}
	return newForm("Password", "Password for "+who, "Connect",
		func(v formValues) (tea.Cmd, error) {
			pw := v.raw("password")
			path, err := b.Connect(s, &pw)
			if err != nil {
				return nil, err
			}
			if thenOpen || s.AutoOpen {
				if err := b.Open(path); err != nil {
					return nil, err
				}
			}
			return popWith(1, "Connected at "+share.CollapsePath(path)), nil
		},
		secretField("password", "Password", ""),
	)
}

// ---- one share --------------------------------------------------------------

func shareMenu(b Backend, s *share.Share) *menuScreen {
	m := &menuScreen{name: s.Name}
	status := func() mount.Status {
		st, _ := b.Statuses()
		return st[s.ID]
	}
	m.intro = func() string {
		st := status()
		line := offStyle.Render("○ Disconnected")
		if st.Mounted() {
			line = onStyle.Render("● " + st.String())
			if st.Total > 0 {
				line += helpStyle.Render("   " + mount.FormatBytes(st.Free) + " free of " + mount.FormatBytes(st.Total))
			}
		}
		return line + "\n" + helpStyle.Render(s.URL()+"   as "+accountOf(s))
	}
	m.build = func() []menuItem {
		st := status()
		var items []menuItem
		if st.Mounted() {
			items = append(items, menuItem{label: "Disconnect", run: func() tea.Cmd {
				return func() tea.Msg {
					err := b.Disconnect(s, st.Path, false)
					var busy *mount.BusyError
					if errors.As(err, &busy) || errors.Is(err, mount.ErrNotResponding) {
						return pushMsg{forceDisconnect(b, s, st.Path, err)}
					}
					if err != nil {
						return noteMsg{err: err}
					}
					return noteMsg{text: "Disconnected"}
				}
			}})
		} else {
			items = append(items, menuItem{label: "Connect", run: func() tea.Cmd { return connectCmd(b, s, false) }})
		}
		items = append(items, menuItem{label: "Open in Finder",
			desc: "Connects first if necessary.",
			run: func() tea.Cmd {
				if st.Mounted() {
					return act("Opened in Finder", func() error { return b.Open(st.Path) })
				}
				return connectCmd(b, s, true)
			}})

		items = append(items, menuItem{label: "Edit", value: s.MountPoint,
			desc: "Change the name, address, user or mount point.",
			run:  func() tea.Cmd { return push(editShareForm(b, s)) }})

		items = append(items, menuItem{label: "Password", value: b.PasswordState(s),
			desc: "Set, replace or remove the password kept in the macOS Keychain.",
			run: func() tea.Cmd {
				if s.Guest() {
					return note(s.Name+" uses guest access and needs no password. Set a user under Edit first.", nil)
				}
				return push(passwordMenu(b, s))
			}})
		if !s.Guest() {
			items = append(items, menuItem{label: "Sign-in", value: signInValue(s),
				desc: "Sign in with the password from the Keychain, or with a Kerberos ticket\n(Active Directory): Mountkeeper looks for the tickets you have.",
				run:  func() tea.Cmd { return push(signInMenu(b, s)) }})
		}

		auto := onOff(s.AutoConnect)
		if p, paused := b.Paused(s); paused {
			auto = "paused: " + p.Text
		}
		items = append(items, menuItem{label: "Connect automatically", value: auto,
			desc: "Connect at login and whenever the server comes into reach.\nNeeds a stored password (or guest access).",
			run: func() tea.Cmd {
				on := !s.AutoConnect
				return act("Automatic connecting is now "+onOff(on), func() error { return b.SetAutoConnect(s, on) })
			}})

		items = append(items, menuItem{label: "Open Finder after connecting", value: onOff(s.AutoOpen),
			run: func() tea.Cmd {
				on := !s.AutoOpen
				return act("Opening Finder after connecting is now "+onOff(on), func() error { return b.SetAutoOpen(s, on) })
			}})

		if profiles := b.Profiles(); len(profiles) > 0 {
			var member []string
			for _, p := range profiles {
				for _, id := range p.IDs {
					if id == s.ID {
						member = append(member, p.Name)
					}
				}
			}
			value := "none"
			if len(member) > 0 {
				value = strings.Join(member, ", ")
			}
			items = append(items, menuItem{label: "Profiles", value: value,
				desc: "Choose the profiles this share belongs to.",
				run:  func() tea.Cmd { return push(shareProfilesPicker(b, s)) }})
		}

		items = append(items, menuItem{label: "History",
			run: func() tea.Cmd { return push(historyScreen(b, s)) }})

		if st.Mounted() {
			items = append(items, menuItem{label: "Force disconnect", danger: true,
				desc: "For a share whose server no longer answers (after sleep, a dropped VPN\nor a change of network), or that an app keeps busy.",
				run:  func() tea.Cmd { return push(forceDisconnect(b, s, st.Path, nil)) }})
		}
		items = append(items, menuItem{label: "Remove", danger: true,
			desc: "Unregister the share and delete its password from the Keychain.",
			run: func() tea.Cmd {
				detail := s.URL() + "\nIts stored password is deleted as well. Files on the server are not touched."
				if status().Mounted() {
					detail += "\nThe share is connected and will be disconnected first."
				}
				return push(&confirmScreen{name: "Remove", question: "Remove " + s.Name + "?", detail: detail, yes: "Remove",
					run: func() (tea.Cmd, error) {
						notes, err := b.RemoveShare(s)
						if err != nil {
							return nil, err
						}
						return popWith(2, withNotes("Removed "+s.Name, notes)), nil
					}})
			}})
		return items
	}
	return m
}

// forceDisconnect asks before unmounting a share by force. cause is the error
// of a normal attempt, if there was one.
func forceDisconnect(b Backend, s *share.Share, path string, cause error) *confirmScreen {
	detail := "Its server no longer answers, so it cannot be disconnected normally."
	var busy *mount.BusyError
	if errors.As(cause, &busy) {
		detail = cause.Error() + "."
	}
	detail += "\nForcing it may lose unsaved changes in files that are open on it."
	return &confirmScreen{name: "Force disconnect", question: "Force disconnect " + s.Name + "?", detail: detail, yes: "Force disconnect",
		run: func() (tea.Cmd, error) {
			if err := b.Disconnect(s, path, true); err != nil {
				return nil, err
			}
			return popWith(1, "Disconnected "+s.Name), nil
		}}
}

func passwordMenu(b Backend, s *share.Share) *menuScreen {
	m := &menuScreen{name: "Password"}
	m.intro = func() string { return helpStyle.Render("Currently: " + b.PasswordState(s)) }
	m.build = func() []menuItem {
		return []menuItem{
			{label: "Set password",
				desc: "Store a new password in the macOS Keychain. It is used from the next connect on.",
				run: func() tea.Cmd {
					return push(newForm("Set", "New password for "+accountOf(s)+" on "+s.Host, "Save",
						func(v formValues) (tea.Cmd, error) {
							if err := b.SetPassword(s, v.raw("password")); err != nil {
								return nil, err
							}
							return popWith(2, "Password saved to the Keychain"), nil
						},
						secretField("password", "Password", "")))
				}},
			{label: "Remove stored password", danger: true,
				desc: "Delete it from the Keychain. You are then asked on every connect,\nand the share can no longer connect automatically.",
				run: func() tea.Cmd {
					return push(&confirmScreen{name: "Remove", question: "Remove the stored password of " + s.Name + "?", yes: "Remove",
						run: func() (tea.Cmd, error) {
							if err := b.RemovePassword(s); err != nil {
								return nil, err
							}
							return popWith(2, "Password removed from the Keychain"), nil
						}})
				}},
		}
	}
	return m
}

func shareProfilesPicker(b Backend, s *share.Share) *pickScreen {
	p := &pickScreen{name: "Profiles", intro: "Profiles that " + s.Name + " belongs to"}
	for _, pr := range b.Profiles() {
		on := false
		for _, id := range pr.IDs {
			on = on || id == s.ID
		}
		p.options = append(p.options, pickOption{id: pr.Name, label: pr.Name, detail: plural(len(pr.IDs), "share", "shares"), on: on})
	}
	p.save = func(chosen []string) (tea.Cmd, error) {
		want := map[string]bool{}
		for _, name := range chosen {
			want[name] = true
		}
		for _, pr := range b.Profiles() {
			var ids []string
			had := false
			for _, id := range pr.IDs {
				if id == s.ID {
					had = true
					continue
				}
				ids = append(ids, id)
			}
			if want[pr.Name] {
				ids = append(ids, s.ID)
			}
			if had != want[pr.Name] {
				if err := b.SaveProfile(pr.Name, ids); err != nil {
					return nil, err
				}
			}
		}
		return popWith(1, "Profiles updated"), nil
	}
	return p
}

// ---- profiles ---------------------------------------------------------------

func findProfile(b Backend, name string) (Profile, bool) {
	for _, p := range b.Profiles() {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

func autoText(b Backend, p Profile) string {
	if len(p.Auto) == 0 {
		return "off"
	}
	var parts []string
	for _, r := range p.Auto {
		parts = append(parts, b.DescribeRule(r))
	}
	return "on " + strings.Join(parts, " or ")
}

func profilesMenu(b Backend) *menuScreen {
	m := &menuScreen{name: "Profiles", extra: "N New profile"}
	newProfile := func() tea.Cmd { return push(newProfileForm(b)) }
	m.keys = map[string]func() tea.Cmd{"n": newProfile}
	m.intro = func() string {
		if len(b.Profiles()) > 0 {
			return ""
		}
		return helpStyle.Render("A profile is a group of shares that connect together —\nfor example everything you need at the office.")
	}
	m.build = func() []menuItem {
		st, _ := b.Statuses()
		var items []menuItem
		for _, p := range b.Profiles() {
			connected := 0
			for _, id := range p.IDs {
				if st[id].Mounted() {
					connected++
				}
			}
			value := fmt.Sprintf("%s, %d connected", plural(len(p.IDs), "share", "shares"), connected)
			if len(p.Auto) > 0 {
				value += " · automatic " + autoText(b, p)
			}
			name := p.Name
			items = append(items, menuItem{label: name, value: value,
				run: func() tea.Cmd { return push(profileMenu(b, name)) }})
		}
		return append(items, menuItem{label: "New profile…", run: newProfile})
	}
	return m
}

func newProfileForm(b Backend) *formScreen {
	return newForm("New profile", "", "Continue",
		func(v formValues) (tea.Cmd, error) {
			name := v.text("name")
			if name == "" {
				return nil, errors.New("enter a name for the profile")
			}
			if _, exists := findProfile(b, name); exists {
				return nil, fmt.Errorf("a profile named %q already exists", name)
			}
			if err := b.SaveProfile(name, nil); err != nil {
				return nil, err
			}
			return replace(profileSharesPicker(b, name)), nil
		},
		textField("name", "Name", "", "office", "A short name without spaces, for example office or home."),
	)
}

func profileSharesPicker(b Backend, name string) *pickScreen {
	p := &pickScreen{name: "Shares", intro: "Shares in profile " + name}
	current, _ := findProfile(b, name)
	in := map[string]bool{}
	for _, id := range current.IDs {
		in[id] = true
	}
	for _, s := range b.Shares() {
		p.options = append(p.options, pickOption{id: s.ID, label: s.Name, detail: s.URL(), on: in[s.ID]})
	}
	p.save = func(ids []string) (tea.Cmd, error) {
		if err := b.SaveProfile(name, ids); err != nil {
			return nil, err
		}
		return popWith(1, fmt.Sprintf("Profile %s has %s", name, plural(len(ids), "share", "shares"))), nil
	}
	return p
}

func profileMenu(b Backend, name string) *menuScreen {
	m := &menuScreen{name: name}
	m.build = func() []menuItem {
		p, ok := findProfile(b, name)
		if !ok {
			return []menuItem{{label: "Back", run: pop}}
		}
		members := "none"
		if len(p.IDs) > 0 {
			members = strings.Join(p.IDs, ", ")
		}
		return []menuItem{
			{label: "Connect all",
				run: func() tea.Cmd { return push(newProfileShares(b, name, "c")) }},
			{label: "Disconnect all",
				run: func() tea.Cmd { return push(newProfileShares(b, name, "x")) }},
			{label: "Shares", value: members,
				desc: "Choose which shares belong to this profile.",
				run:  func() tea.Cmd { return push(profileSharesPicker(b, name)) }},
			{label: "Connect automatically", value: autoText(b, p),
				desc: "Keep this profile connected while you are on a certain network,\nand disconnect it again when you leave.",
				run:  func() tea.Cmd { return push(autoRuleMenu(b, name)) }},
			{label: "Delete profile", danger: true,
				desc: "The shares themselves stay registered and connected.",
				run: func() tea.Cmd {
					return push(&confirmScreen{name: "Delete", question: "Delete profile " + name + "?",
						detail: "Its shares stay registered and connected.", yes: "Delete",
						run: func() (tea.Cmd, error) {
							if err := b.RemoveProfile(name); err != nil {
								return nil, err
							}
							return popWith(2, "Deleted profile "+name), nil
						}})
				}},
		}
	}
	return m
}

// autoRuleMenu binds a profile to the network the Mac is on right now.
func autoRuleMenu(b Backend, name string) *menuScreen {
	m := &menuScreen{name: "Connect automatically"}
	var info *NetworkInfo // looked up once; it runs system tools
	network := func() NetworkInfo {
		if info == nil {
			n := b.Network()
			info = &n
		}
		return *info
	}
	set := func(rules []string, done string) func() tea.Cmd {
		return func() tea.Cmd {
			return func() tea.Msg {
				if err := b.SetProfileAuto(name, rules); err != nil {
					return noteMsg{err: err}
				}
				return popMsg{n: 1, note: done}
			}
		}
	}
	m.intro = func() string {
		if len(network().Rules) == 0 {
			return warnStyle.Render("This network offers nothing to recognise it by (no DNS domain, no router address).") +
				"\n" + helpStyle.Render("Connect to the network the profile is meant for and come back.")
		}
		return helpStyle.Render("Choose how Mountkeeper should recognise the network you are on right now.\nA network domain also holds on other floors and over VPN; a router only here.")
	}
	m.build = func() []menuItem {
		p, _ := findProfile(b, name)
		current := map[string]bool{}
		for _, r := range p.Auto {
			current[r] = true
		}
		mark := func(on bool) string {
			if on {
				return "current setting"
			}
			return ""
		}
		items := []menuItem{{label: "Off", value: mark(len(p.Auto) == 0),
			desc: "Connect this profile only when you ask for it.",
			run:  set(nil, "Profile "+name+" no longer connects automatically")}}
		for _, r := range network().Rules {
			rule := r
			items = append(items, menuItem{label: "On " + rule.Label, value: mark(current[rule.Value]),
				run: set([]string{rule.Value}, "Profile "+name+" connects automatically on "+rule.Label)})
			delete(current, rule.Value)
		}
		for r := range current {
			items = append(items, menuItem{label: "Keep: on " + b.DescribeRule(r), value: "current setting, not this network", run: pop})
		}
		return items
	}
	return m
}

// ---- servers, network, history ----------------------------------------------

func serversMenu(b Backend) *menuScreen {
	m := &menuScreen{name: "Find servers", extra: "R Search again", loading: "Searching the local network…"}
	m.build = func() []menuItem { return nil }
	m.load = func() tea.Cmd {
		m.loading = "Searching the local network…"
		return func() tea.Msg {
			servers, err := b.FindServers()
			if err != nil {
				return menuLoadedMsg{screen: m, err: err}
			}
			var items []menuItem
			for _, sv := range servers {
				sv := sv
				if sv.Host == "" {
					items = append(items, menuItem{label: sv.Name, value: "address unknown"})
					continue
				}
				items = append(items, menuItem{label: sv.Name, value: "smb://" + sv.Host,
					desc: "Enter adds a share from this server: you only need to type the share name.",
					run:  func() tea.Cmd { return push(addShareForm(b, "smb://"+sv.Host+"/")) }})
			}
			items = append(items, menuItem{label: "Add a share by address…",
				desc: "Servers in a company domain usually do not announce themselves.\nAdd their shares by typing the address.",
				run:  func() tea.Cmd { return push(addShareForm(b, "")) }})
			return menuLoadedMsg{screen: m, items: items}
		}
	}
	m.keys = map[string]func() tea.Cmd{"r": func() tea.Cmd { return m.load() }}
	m.intro = func() string {
		if m.loading == "" && len(m.items) <= 1 {
			return helpStyle.Render("No file servers announce themselves on this network.")
		}
		return ""
	}
	return m
}

func networkScreen(b Backend) *textScreen {
	return &textScreen{name: "Network", load: func() []string {
		info := b.Network()
		var lines []string
		for _, r := range info.Rows {
			lines = append(lines, helpStyle.Render(fmt.Sprintf("%-14s", r[0]))+r[1])
		}
		active := map[string]bool{}
		for _, r := range info.Rules {
			active[r.Value] = true
		}
		lines = append(lines, "", nameStyle.Render("Profiles that connect automatically"))
		bound := 0
		for _, p := range b.Profiles() {
			if len(p.Auto) == 0 {
				continue
			}
			bound++
			state := offStyle.Render("○ not on this network")
			for _, r := range p.Auto {
				if active[r] {
					state = onStyle.Render("● active now")
				}
			}
			lines = append(lines, fmt.Sprintf("%-14s", p.Name)+state+helpStyle.Render("   "+autoText(b, p)))
		}
		if bound == 0 {
			lines = append(lines, helpStyle.Render("None. Bind one under Profiles › <profile> › Connect automatically."))
		}
		return lines
	}}
}

func historyScreen(b Backend, s *share.Share) *textScreen {
	id := ""
	if s != nil {
		id = s.ID
	}
	return &textScreen{name: "History", load: func() []string {
		events := b.History(id, 300)
		if len(events) == 0 {
			return []string{helpStyle.Render("Nothing recorded yet.")}
		}
		width := 0
		for _, e := range events {
			width = max(width, len(e.Share))
		}
		lines := make([]string, 0, len(events))
		for i := len(events) - 1; i >= 0; i-- { // newest first
			lines = append(lines, eventLine(events[i], width, id == ""))
		}
		return lines
	}}
}

func eventLine(e history.Event, width int, withShare bool) string {
	action := fmt.Sprintf("%-12s", e.Action)
	switch e.Action {
	case history.Connected:
		action = onStyle.Render(action)
	case history.Failed:
		action = errStyle.Render(action)
	default:
		action = offStyle.Render(action)
	}
	line := helpStyle.Render(e.Time.Local().Format("2006-01-02 15:04")) + "  " + action
	if withShare {
		line += "  " + fmt.Sprintf("%-*s", width, e.Share)
	}
	by := "by you"
	if e.Trigger == history.Auto {
		by = "automatically"
	}
	line += "  " + helpStyle.Render(by)
	if e.Detail != "" {
		line += "  " + helpStyle.Render(share.CollapsePath(e.Detail))
	}
	return line
}

// doctorScreen shows the diagnosis; Enter on a finding about the background
// agent repairs it.
func doctorScreen(b Backend) *menuScreen {
	m := &menuScreen{name: "Check", extra: "R Check again", loading: "Checking…"}
	m.build = func() []menuItem { return nil }
	var items func(checks []Check) []menuItem
	items = func(checks []Check) []menuItem {
		var out []menuItem
		for _, c := range checks {
			mark, value := "✓", c.Detail
			switch c.Level {
			case CheckWarn:
				mark, value = "!", warnStyle.Render(c.Detail)
			case CheckFail:
				mark, value = "✗", errStyle.Render(c.Detail)
			}
			it := menuItem{label: mark + " " + c.Group + " › " + c.Label, value: value, desc: c.Hint}
			if c.Fix {
				it.desc = "Enter repairs the background agent."
				it.run = func() tea.Cmd {
					return func() tea.Msg {
						if err := b.RepairAgent(); err != nil {
							return noteMsg{err: err}
						}
						return menuLoadedMsg{screen: m, items: items(b.Doctor()), note: "Background agent repaired"}
					}
				}
			} else if c.Hint != "" {
				it.desc = "→ " + c.Hint
			}
			out = append(out, it)
		}
		return out
	}
	m.load = func() tea.Cmd {
		m.loading = "Checking…"
		return func() tea.Msg { return menuLoadedMsg{screen: m, items: items(b.Doctor())} }
	}
	m.keys = map[string]func() tea.Cmd{"r": func() tea.Cmd { return m.load() }}
	m.intro = func() string {
		if m.loading != "" {
			return ""
		}
		problems := 0
		for _, it := range m.items {
			if strings.HasPrefix(it.label, "✗") || strings.HasPrefix(it.label, "!") {
				problems++
			}
		}
		if problems == 0 {
			return onStyle.Render("Everything looks fine.")
		}
		return warnStyle.Render(plural(problems, "finding needs", "findings need") + " attention.")
	}
	return m
}

func signInValue(s *share.Share) string {
	if s.Kerberos() {
		if s.KerberosPrincipal != "" {
			return "Kerberos · " + s.KerberosPrincipal
		}
		return "Kerberos"
	}
	return "password from the Keychain"
}

// signInMenu chooses between the Keychain password and one of the user's
// Kerberos tickets. The tickets are scanned when it opens and with R.
func signInMenu(b Backend, s *share.Share) *menuScreen {
	m := &menuScreen{name: "Sign-in", extra: "R Scan again", loading: "Looking for Kerberos tickets…"}
	m.build = func() []menuItem { return nil }
	var found int
	var items func(tickets []Ticket) []menuItem
	choose := func(method, principal, done string) func() tea.Cmd {
		return func() tea.Cmd {
			return func() tea.Msg {
				notes, err := b.SetSignIn(s, method, principal)
				if err != nil {
					return noteMsg{err: err}
				}
				tickets, _ := b.Tickets(s)
				return menuLoadedMsg{screen: m, items: items(tickets), note: withNotes(done, notes)}
			}
		}
	}
	items = func(tickets []Ticket) []menuItem {
		found = len(tickets)
		mark := func(on bool) string {
			if on {
				return "● "
			}
			return "○ "
		}
		out := []menuItem{{label: mark(!s.Kerberos()) + "Password from the Keychain", value: b.PasswordState(s),
			desc: "Sign in with the stored password (or be asked for it).",
			run:  choose("password", "", s.Name+" signs in with its password")}}
		listed := false
		for _, t := range tickets {
			current := s.Kerberos() && strings.EqualFold(t.Principal, s.KerberosPrincipal)
			listed = listed || current
			var info []string
			if t.Valid {
				info = append(info, "valid until "+t.Expires.Format("2006-01-02 15:04"))
			} else {
				info = append(info, warnStyle.Render("expired"))
			}
			if t.Matches {
				info = append(info, "suits this share")
			}
			if t.Used {
				info = append(info, "already signed in to "+s.Host)
			}
			out = append(out, menuItem{label: mark(current) + "Kerberos · " + t.Principal, value: strings.Join(info, " · "),
				desc: "Sign in with this ticket. There is no fallback to the password:\nwithout a valid ticket, connecting waits until you sign in again.",
				run:  choose("kerberos", t.Principal, s.Name+" signs in with the Kerberos ticket of "+t.Principal)})
		}
		if s.Kerberos() && !listed {
			out = append(out, menuItem{label: "● Kerberos · " + s.KerberosPrincipal, value: warnStyle.Render("no ticket right now"),
				desc: "Sign in again (kinit " + s.KerberosPrincipal + ", or your company sign-in), then R."})
		}
		return append(out, menuItem{label: "Scan again", run: func() tea.Cmd { return m.load() }})
	}
	m.load = func() tea.Cmd {
		m.loading = "Looking for Kerberos tickets…"
		return func() tea.Msg {
			tickets, err := b.Tickets(s)
			if err != nil {
				return menuLoadedMsg{screen: m, items: items(nil), err: err}
			}
			return menuLoadedMsg{screen: m, items: items(tickets)}
		}
	}
	m.keys = map[string]func() tea.Cmd{"r": func() tea.Cmd { return m.load() }}
	m.intro = func() string {
		if m.loading != "" {
			return ""
		}
		text := "Currently: " + signInValue(s)
		if found == 0 {
			return helpStyle.Render(text) + "\n" + warnStyle.Render("No Kerberos tickets found.") + "\n" +
				helpStyle.Render("Sign in with kinit user@REALM or your company sign-in, then press R.")
		}
		return helpStyle.Render(text + "\n" + plural(found, "Kerberos ticket", "Kerberos tickets") + " found. Enter chooses.")
	}
	return m
}
