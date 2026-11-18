package cli

import (
	"strings"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

// runApp opens the interactive interface: the main menu, or directly the
// connection manager.
func runApp(cfg *config.Config, sharesOnly bool) error {
	trigger = history.Interactive
	// Warnings would garble the full-screen interface; operations report
	// through their return values there.
	quiet = true
	defer func() { quiet = false }()

	b := &appBackend{cfg: cfg}
	if sharesOnly {
		return tui.RunShares(b)
	}
	return tui.Run(b)
}

// appBackend connects the interface to the same operations the commands use.
type appBackend struct{ cfg *config.Config }

var _ tui.Backend = (*appBackend)(nil)

func (a *appBackend) Shares() []*share.Share { return a.cfg.Shares }

func (a *appBackend) Profiles() []tui.Profile {
	out := make([]tui.Profile, 0, len(a.cfg.Profiles))
	for _, p := range a.cfg.Profiles {
		out = append(out, tui.Profile{Name: p.Name, IDs: append([]string(nil), p.Shares...), Auto: append([]string(nil), p.Auto...)})
	}
	return out
}

func (a *appBackend) Statuses() (map[string]mount.Status, error) { return statuses(a.cfg) }

func (a *appBackend) Reachable() map[string]bool {
	return reachability(a.cfg.Shares, 1500*time.Millisecond)
}

func (a *appBackend) Paused(s *share.Share) (tui.Pause, bool) { return tuiPause(a.cfg, s) }

func (a *appBackend) Connect(s *share.Share, password *string) (string, error) {
	return mountShare(s, password)
}

func (a *appBackend) Disconnect(s *share.Share, path string, force bool) error {
	return unmountAt(s, path, force)
}

func (a *appBackend) Open(path string) error { return openInFinder(path) }

func specOf(f tui.ShareForm) shareSpec {
	return shareSpec{URL: f.URL, Name: f.Name, User: f.User, MountPoint: f.MountPoint,
		Keychain: f.Keychain, Password: f.Password, AutoOpen: f.AutoOpen, AutoConnect: f.AutoConnect}
}

func (a *appBackend) AddShare(f tui.ShareForm) (*share.Share, []string, error) {
	return createShare(a.cfg, specOf(f))
}

func (a *appBackend) EditShare(s *share.Share, f tui.ShareForm) ([]string, error) {
	n, err := editFromSpec(s, specOf(f))
	if err != nil {
		return nil, err
	}
	_, notes, err := applyEdit(a.cfg, s, n)
	return notes, err
}

func (a *appBackend) RemoveShare(s *share.Share) ([]string, error) {
	return deleteShare(a.cfg, s, true)
}

func (a *appBackend) UserField(s *share.Share) string { return userField(s) }

func (a *appBackend) SetAutoConnect(s *share.Share, on bool) error {
	return setAutoConnect(a.cfg, s, on)
}

func (a *appBackend) SetAutoOpen(s *share.Share, on bool) error {
	previous := s.AutoOpen
	s.AutoOpen = on
	if err := a.cfg.Save(); err != nil {
		s.AutoOpen = previous
		return err
	}
	return nil
}

func (a *appBackend) PasswordState(s *share.Share) string { return passwordState(s) }

func (a *appBackend) SetPassword(s *share.Share, password string) error {
	return storePassword(a.cfg, s, password)
}

func (a *appBackend) RemovePassword(s *share.Share) error {
	_, err := forgetPassword(a.cfg, s)
	return err
}

func (a *appBackend) Tickets(s *share.Share) ([]tui.Ticket, error) { return tuiTickets(s) }

func (a *appBackend) SetSignIn(s *share.Share, method, principal string) ([]string, error) {
	return setSignIn(a.cfg, s, method, principal)
}

func (a *appBackend) SaveProfile(name string, ids []string) error {
	if err := validProfileName(name); err != nil {
		return err
	}
	p, _ := a.cfg.AddToProfile(name)
	p.Shares = append([]string(nil), ids...)
	if err := a.cfg.Save(); err != nil {
		return err
	}
	return syncAgent(a.cfg)
}

func (a *appBackend) RemoveProfile(name string) error {
	a.cfg.RemoveProfile(name)
	if err := a.cfg.Save(); err != nil {
		return err
	}
	return syncAgent(a.cfg)
}

func (a *appBackend) SetProfileAuto(name string, rules []string) error {
	p, err := a.cfg.GetProfile(name)
	if err != nil {
		return err
	}
	return setProfileAuto(a.cfg, p, rules)
}

func (a *appBackend) DescribeRule(rule string) string { return macos.DescribeRule(rule) }

func (a *appBackend) Network() tui.NetworkInfo {
	n := macos.CurrentNetwork()
	info := tui.NetworkInfo{Rows: networkRows(n)}
	for _, r := range n.Rules() {
		info.Rules = append(info.Rules, tui.Rule{Value: r, Label: macos.DescribeRule(r)})
	}
	return info
}

func (a *appBackend) FindServers() ([]tui.Server, error) {
	found, err := macos.Browse(2 * time.Second)
	if err != nil {
		return nil, err
	}
	out := make([]tui.Server, 0, len(found))
	for _, s := range found {
		out = append(out, tui.Server{Name: s.Name, Host: s.Host})
	}
	return out, nil
}

func (a *appBackend) Doctor() []tui.Check { return runChecks(a.cfg, false) }

func (a *appBackend) RepairAgent() error { return repairAgent(a.cfg) }

func (a *appBackend) History(shareID string, limit int) []history.Event {
	events, _ := history.Read(shareID, limit)
	return events
}

// networkRows describes a network in label/value pairs.
func networkRows(n macos.Network) [][2]string {
	if !n.Online() {
		return [][2]string{{"Network", "not connected"}}
	}
	dash := func(v string) string {
		if v == "" {
			return "–"
		}
		return v
	}
	router := n.RouterIP
	if n.RouterMAC != "" {
		router += "  (" + n.RouterMAC + ")"
	}
	wifi := n.SSID
	if wifi == "" {
		wifi = "not available (macOS hides the Wi-Fi name from command-line tools)"
	}
	return [][2]string{
		{"Interface", n.Interface},
		{"Router", dash(router)},
		{"DNS domains", dash(strings.Join(n.Domains, ", "))},
		{"Wi-Fi", wifi},
	}
}
