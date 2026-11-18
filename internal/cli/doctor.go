package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

// Tests replace these.
var (
	inspectAgent = macos.InspectAgent
	executable   = os.Executable
	lookupHost   = func(host string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := net.DefaultResolver.LookupHost(ctx, host)
		return err
	}
	probePort = func(s *share.Share) error { return mount.Reachable(s, 2*time.Second) }
)

// runChecks diagnoses everything Mountkeeper depends on. deep also reads the
// stored passwords, which may make macOS ask for Keychain access.
func runChecks(cfg *config.Config, deep bool) []tui.Check {
	var checks []tui.Check
	checks = append(checks, agentChecks(cfg)...)
	checks = append(checks, keychainChecks(cfg, deep)...)
	servers, answers := serverChecks(cfg)
	checks = append(checks, servers...)
	checks = append(checks, shareChecks(cfg, answers)...)
	return checks
}

func agentChecks(cfg *config.Config) []tui.Check {
	const group, label = "Agent", "Background agent"
	fix := "mountkeeper doctor --fix"
	info := inspectAgent()
	c := tui.Check{Group: group, Label: label}

	switch {
	case !cfg.NeedsAgent() && !info.Installed:
		c.Detail = "not needed: nothing connects automatically"
		return []tui.Check{c}
	case !cfg.NeedsAgent():
		c.Level, c.Detail, c.Hint, c.Fix = tui.CheckWarn, "installed, but nothing connects automatically any more", fix+" removes it", true
		return []tui.Check{c}
	case !info.Installed:
		c.Level, c.Detail, c.Hint, c.Fix = tui.CheckFail, "not installed, so nothing connects automatically", fix+" installs it", true
		return []tui.Check{c}
	case !info.Loaded:
		c.Level, c.Detail, c.Hint, c.Fix = tui.CheckFail, "installed but not loaded by launchd", fix+" loads it", true
	}

	exe, _ := executable()
	if c.Level == tui.CheckOK {
		switch st, err := os.Stat(info.Program); {
		case info.Program == "":
			c.Level, c.Detail, c.Hint, c.Fix = tui.CheckFail, "its plist names no program", fix+" rewrites it", true
		case err != nil || st.IsDir() || st.Mode()&0o111 == 0:
			c.Level, c.Detail, c.Hint, c.Fix = tui.CheckFail, "runs "+share.CollapsePath(info.Program)+", which does not exist (any more)", fix+" points it to this mountkeeper", true
		case exe != "" && !sameFile(exe, info.Program):
			c.Level, c.Detail, c.Hint, c.Fix = tui.CheckWarn, "runs "+share.CollapsePath(info.Program)+", but this is "+share.CollapsePath(exe), fix+" points it to this mountkeeper", true
		case !info.Current:
			c.Level, c.Detail, c.Hint, c.Fix = tui.CheckWarn, "written by an older version (it may miss network changes)", fix+" updates it", true
		default:
			c.Detail = "loaded · runs " + share.CollapsePath(info.Program)
		}
	}
	checks := []tui.Check{c}

	log := tui.Check{Group: group, Label: "Agent log"}
	path, _ := macos.AgentLogPath()
	if st, err := os.Stat(path); err == nil {
		log.Detail = "last run " + st.ModTime().Format("2006-01-02 15:04") + " · " + humanBytes(uint64(st.Size())) + " · " + share.CollapsePath(path)
	} else {
		log.Level, log.Detail = tui.CheckWarn, "no run recorded yet ("+share.CollapsePath(path)+" is missing)"
	}
	return append(checks, log)
}

// sameFile compares two paths to an executable, following symlinks (Homebrew
// links /opt/homebrew/bin/mountkeeper into its Cellar).
func sameFile(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

func keychainChecks(cfg *config.Config, deep bool) []tui.Check {
	const group = "Keychain"
	if err := keychain.Available(); err != nil {
		return []tui.Check{{Group: group, Label: "Login Keychain", Level: tui.CheckFail, Detail: err.Error(), Hint: keychainUnavailableHint()}}
	}
	checks := []tui.Check{{Group: group, Label: "Login Keychain", Detail: "available"}}
	for _, s := range cfg.Shares {
		if s.Guest() || s.KeychainAccount == "" || usesKerberos(s) {
			continue
		}
		c := tui.Check{Group: group, Label: s.ID}
		switch {
		case !keychain.Has(s.KeychainAccount):
			c.Level, c.Detail, c.Hint = tui.CheckWarn, "no password stored yet", "mountkeeper credentials set "+s.ID
			if autoWanted(cfg, s) {
				c.Detail += " — automatic connecting needs one"
			}
		case deep:
			if _, err := keychain.Get(s.KeychainAccount); err != nil {
				err = explain(s, err)
				c.Level, c.Detail, c.Hint = tui.CheckFail, "stored, but cannot be read: "+err.Error(), hintOf(err)
			} else {
				c.Detail = "password stored and readable"
			}
		default:
			c.Detail = "password stored"
		}
		checks = append(checks, c)
	}
	return checks
}

// serverChecks resolves and probes every server once. answers tells per
// share id whether its server answered.
func serverChecks(cfg *config.Config) ([]tui.Check, map[string]bool) {
	type server struct {
		host   string
		share  *share.Share // one share on it, for port and probing
		ids    []string
		check  tui.Check
		answer bool
	}
	byKey := map[string]*server{}
	var keys []string
	for _, s := range cfg.Shares {
		port := s.Port
		if port == 0 {
			port = 445
		}
		key := net.JoinHostPort(s.Host, strconv.Itoa(port))
		sv := byKey[key]
		if sv == nil {
			sv = &server{host: s.Host, share: s}
			byKey[key] = sv
			keys = append(keys, key)
		}
		sv.ids = append(sv.ids, s.ID)
	}

	var wg sync.WaitGroup
	for _, key := range keys {
		sv := byKey[key]
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := tui.Check{Group: "Servers", Label: sv.host}
			port := sv.share.Port
			if port == 0 {
				port = 445
			}
			switch {
			case lookupHost(sv.host) != nil:
				c.Level, c.Detail, c.Hint = tui.CheckWarn, "the host name cannot be resolved", "Not on the right network? Connect the VPN, or check the address."
			case probePort(sv.share) != nil:
				c.Level, c.Detail, c.Hint = tui.CheckWarn, fmt.Sprintf("resolves, but does not answer on port %d", port), "Check the network, VPN or firewall."
			default:
				c.Detail, sv.answer = fmt.Sprintf("answers on port %d", port), true
			}
			sv.check = c
		}()
	}
	wg.Wait()

	var checks []tui.Check
	answers := map[string]bool{}
	for _, key := range keys {
		sv := byKey[key]
		checks = append(checks, sv.check)
		for _, id := range sv.ids {
			answers[id] = sv.answer
		}
	}
	return checks, answers
}

func shareChecks(cfg *config.Config, answers map[string]bool) []tui.Check {
	table, err := mount.Table()
	if err != nil {
		return []tui.Check{{Group: "Shares", Label: "Mount table", Level: tui.CheckFail, Detail: err.Error()}}
	}
	var checks []tui.Check
	for _, s := range cfg.Shares {
		c := tui.Check{Group: "Shares", Label: s.ID}
		st := mount.StatusOf(s, table)
		reason, since, paused := pausedNow(cfg, s)
		switch {
		case st.Mounted() && !answers[s.ID]:
			c.Level, c.Detail, c.Hint = tui.CheckWarn, "connected, but its server does not answer", "mountkeeper disconnect "+s.ID+" --force"
		case paused:
			c.Level = tui.CheckWarn
			c.Detail = "automatic connecting paused since " + since.Format("2006-01-02 15:04") + ": " + pauseText(reason)
			c.Hint = pauseHint(s, reason)
		case st.Mounted():
			c.Detail = st.String()
		default:
			switch problem := mountPointProblem(s, table); {
			case problem != "":
				c.Level, c.Detail, c.Hint = tui.CheckFail, problem, "mountkeeper edit "+s.ID+" --mount-point <dir>"
			case !answers[s.ID]:
				c.Detail = "not connected · its server is not reachable from here"
			default:
				c.Detail = "ready to connect"
			}
		}
		if kc, ok := kerberosCheck(s); ok {
			checks = append(checks, c, kc)
			continue
		}
		checks = append(checks, c)
	}
	return checks
}

// mountPointProblem reports what would stop a disconnected share from being
// mounted on its mount point, without creating anything.
func mountPointProblem(s *share.Share, table []mount.Entry) string {
	mp, err := s.MountPath()
	if err != nil {
		return "the mount point is invalid: " + err.Error()
	}
	for _, e := range table {
		if e.On == mp {
			return share.CollapsePath(mp) + " is already in use by " + e.From
		}
	}
	info, err := os.Lstat(mp)
	switch {
	case err != nil:
		return "" // created on connect
	case !info.IsDir():
		return share.CollapsePath(mp) + " exists and is not a directory"
	}
	entries, err := os.ReadDir(mp)
	if err != nil {
		return "cannot read " + share.CollapsePath(mp) + ": " + err.Error()
	}
	for _, e := range entries {
		if e.Name() != ".DS_Store" {
			return share.CollapsePath(mp) + " is not empty"
		}
	}
	return ""
}

func levelName(l tui.CheckLevel) string {
	switch l {
	case tui.CheckWarn:
		return "warn"
	case tui.CheckFail:
		return "fail"
	}
	return "ok"
}

func levelMark(l tui.CheckLevel) string {
	switch l {
	case tui.CheckWarn:
		return warnStyle.Render("!")
	case tui.CheckFail:
		return errStyle.Render("✗")
	}
	return okStyle.Render("✓")
}

func printChecks(checks []tui.Check) {
	group := ""
	var rows [][]string
	flush := func() {
		table(rows)
		rows = nil
	}
	for _, c := range checks {
		if c.Group != group {
			if group != "" {
				flush()
				fmt.Println()
			}
			group = c.Group
			fmt.Println("  " + boldStyle.Render(group))
		}
		detail := c.Detail
		switch c.Level {
		case tui.CheckWarn:
			detail = warnStyle.Render(detail)
		case tui.CheckFail:
			detail = errStyle.Render(detail)
		}
		rows = append(rows, []string{levelMark(c.Level) + " " + c.Label, detail})
		if c.Hint != "" {
			rows = append(rows, []string{"", dimStyle.Render("→ " + c.Hint)})
		}
	}
	flush()
}

type jsonCheck struct {
	Group  string `json:"group"`
	Label  string `json:"label"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

func newDoctorCmd() *cobra.Command {
	var asJSON, fix, deep bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check everything Mountkeeper depends on",
		Long: `Check in one place what Mountkeeper depends on and say what to do about
anything that is wrong:

  Agent      whether the background agent is installed, loaded, up to date
             and runs this mountkeeper, and when it last ran
  Keychain   whether the login Keychain is usable and holds the passwords
  Servers    whether every server resolves and answers on its SMB port
  Shares     shares that are paused, connected to a server that no longer
             answers, or whose mount point is in the way; for Kerberos
             shares, whether a valid ticket exists

--fix repairs the background agent (install, update, point it to this
binary, or remove it). Nothing else is changed.

--deep also reads every stored password. macOS may then ask whether
mountkeeper may use the Keychain; choose "Always Allow".

The exit status is 1 if any check failed.`,
		Example: `  mountkeeper doctor
  mountkeeper doctor --fix
  mountkeeper doctor --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if fix {
				if err := repairAgent(cfg); err != nil {
					return err
				}
				if !asJSON {
					success("Background agent repaired")
					fmt.Println()
				}
			}
			checks := runChecks(cfg, deep)

			failed := 0
			for _, c := range checks {
				if c.Level == tui.CheckFail {
					failed++
				}
			}
			if asJSON {
				out := make([]jsonCheck, len(checks))
				for i, c := range checks {
					out[i] = jsonCheck{Group: c.Group, Label: c.Label, Level: levelName(c.Level), Detail: c.Detail, Hint: c.Hint}
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
			} else {
				printChecks(checks)
				fmt.Println()
				warned := 0
				for _, c := range checks {
					if c.Level == tui.CheckWarn {
						warned++
					}
				}
				switch {
				case failed == 0 && warned == 0:
					fmt.Println("  " + okStyle.Render("Everything looks fine."))
				default:
					var parts []string
					if failed > 0 {
						parts = append(parts, plural(failed, "problem", "problems"))
					}
					if warned > 0 {
						parts = append(parts, plural(warned, "warning", "warnings"))
					}
					fmt.Println("  " + dimStyle.Render(strings.Join(parts, ", ")))
				}
			}
			if failed > 0 {
				return errSilent
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	f.BoolVar(&fix, "fix", false, "repair the background agent")
	f.BoolVar(&deep, "deep", false, "also read the stored passwords (may ask for Keychain access)")
	return cmd
}

// repairAgent makes the background agent match the configuration and this
// binary.
func repairAgent(cfg *config.Config) error {
	if os.Getenv(config.EnvPath) != "" {
		return fmt.Errorf("the background agent only works with the default configuration; unset %s first", config.EnvPath)
	}
	return syncAgent(cfg)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
