package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/finder"
	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

// trigger says who is acting; it is recorded in the connection history.
var trigger = history.Manual

// record adds an event to the connection history. A log that cannot be
// written must never get in the way, so errors are dropped.
func record(s *share.Share, action, detail string) {
	history.Append(history.Event{Share: s.ID, Action: action, Trigger: trigger, Detail: detail})
}

// mountShare connects one share without talking to the user.
//
// password is nil when none was supplied; the Keychain is consulted then, and
// tui.ErrPasswordRequired is returned if nothing is stored. A supplied
// password that the server accepts is saved to the Keychain when the share is
// configured for it.
func mountShare(s *share.Share, password *string) (string, error) {
	st, err := mount.StatusNow(s)
	if err != nil {
		return "", err
	}
	if st.Mounted() {
		return st.Path, nil
	}

	// needPassword asks the caller for a password — but only if the server
	// is there at all: nobody should type a password for a server that is
	// offline anyway.
	needPassword := func() error {
		if err := mount.Reachable(s, 4*time.Second); err != nil {
			return explain(s, err)
		}
		return tui.ErrPasswordRequired
	}

	pw, fromKeychain := "", false
	switch {
	case usesKerberos(s):
		// The chosen ticket must be there; the password is never a fallback.
		if _, err := ticketFor(s); err != nil {
			if trigger != history.Auto {
				record(s, history.Failed, err.Error())
			}
			return "", explain(s, err)
		}
	case s.Guest():
	case password != nil:
		pw = *password
	case s.KeychainAccount == "":
		return "", needPassword()
	default:
		pw, err = keychain.Get(s.KeychainAccount)
		if errors.Is(err, keychain.ErrNotFound) || errors.Is(err, keychain.ErrUnavailable) {
			return "", needPassword()
		}
		if err != nil {
			return "", explain(s, err)
		}
		fromKeychain = true
	}

	path, err := mount.Mount(s, pw)
	if err != nil {
		// Being off the network is the everyday case for the background
		// agent; it is not worth a history entry there.
		if !(trigger == history.Auto && errors.Is(err, mount.ErrUnreachable)) {
			record(s, history.Failed, err.Error())
		}
		if errors.Is(err, mount.ErrAuth) && usesKerberos(s) {
			return "", withHint(fmt.Errorf("%w — the server did not accept the Kerberos ticket of %s", err, kerberosPrincipalHint(s)),
				"Choose another ticket, or sign in with the password again:\nmountkeeper credentials scan %s\nmountkeeper credentials use %s password", s.ID, s.ID)
		}
		if errors.Is(err, mount.ErrAuth) && fromKeychain {
			return "", withHint(fmt.Errorf("%w — the password stored in the Keychain was rejected", err),
				"Update it with: mountkeeper credentials set %s", s.ID)
		}
		return "", explain(s, err)
	}

	record(s, history.Connected, path)
	clearMarker(s, markAuthFailed)
	if trigger != history.Auto {
		// Connecting by hand ends a hold set by disconnecting by hand.
		clearMarker(s, markHeld)
	}
	if password != nil && pw != "" && s.KeychainAccount != "" {
		if err := keychain.Set(s.KeychainAccount, s.URL(), pw); errors.Is(err, keychain.ErrUnavailable) {
			warn("Connected, but the password could not be saved: %v.\n  %s", err, keychainUnavailableHint())
		} else if err != nil {
			warn("Connected, but the password could not be saved: %v", err)
		}
	}
	return path, nil
}

// connectInteractive is mountShare plus a terminal password prompt when one
// is needed and a person is present.
func connectInteractive(s *share.Share) (string, error) {
	path, err := mountShare(s, nil)
	for attempt := 0; attempt < 2; attempt++ {
		needsPassword := errors.Is(err, tui.ErrPasswordRequired) ||
			(attempt > 0 && errors.Is(err, mount.ErrAuth))
		if !needsPassword {
			break
		}
		if !interactive() {
			return "", withHint(fmt.Errorf("%s needs a password but none is stored", s.ID),
				"Save one with: mountkeeper credentials set %s", s.ID)
		}
		if attempt > 0 {
			warn("%v", err)
		}
		pw, perr := askPassword(fmt.Sprintf("Password for %s@%s", s.Username, s.Host))
		if perr != nil {
			return "", perr
		}
		path, err = mountShare(s, &pw)
	}
	return path, err
}

// unmountAt disconnects the volume of share s mounted at path and records it.
// A disconnect by hand puts automatic connecting on hold, so that the
// background agent does not bring the share straight back.
func unmountAt(s *share.Share, path string, force bool) error {
	return unmountWhy(s, path, force, "")
}

// unmountWhy is unmountAt with a reason for the history.
func unmountWhy(s *share.Share, path string, force bool, why string) error {
	if err := unmount(path, force); err != nil {
		return explain(s, err)
	}
	record(s, history.Disconnected, why)
	clearMarker(s, markAutoMounted)
	clearMarker(s, markAgentMounted)
	clearMarker(s, markStale)
	if trigger != history.Auto {
		setMarker(s, markHeld)
	}
	return nil
}

// unmountShare disconnects a share wherever it is mounted. It reports false
// if the share was not mounted at all.
func unmountShare(s *share.Share, force bool) (bool, error) {
	st, err := mount.StatusNow(s)
	if err != nil {
		return false, err
	}
	if !st.Mounted() {
		return false, nil
	}
	return true, unmountAt(s, st.Path, force)
}

// statuses returns the live state of all shares, keyed by id.
func statuses(cfg *config.Config) (map[string]mount.Status, error) {
	tbl, err := mount.Table()
	if err != nil {
		return nil, err
	}
	m := make(map[string]mount.Status, len(cfg.Shares))
	for _, s := range cfg.Shares {
		m[s.ID] = mount.StatusOf(s, tbl)
	}
	return m, nil
}

// openInFinder shows a mounted path in Finder.
func openInFinder(path string) error { return finder.Open(path) }

// syncAgent installs the background LaunchAgent while something automatic is
// configured (an auto-connect share or a network-bound profile) and removes
// it otherwise.
func syncAgent(cfg *config.Config) error {
	// The agent always works on the default configuration. A run against
	// another file (tests, experiments) must not install, replace or remove
	// the user's real agent.
	if os.Getenv(config.EnvPath) != "" {
		return nil
	}
	if !cfg.NeedsAgent() {
		return macos.UninstallAgent()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return macos.InstallAgent(exe)
}

// Markers are empty files that remember small facts about a share between
// runs of the background agent.
const (
	// markAuthFailed stops the agent from retrying a rejected password every
	// few minutes, which could lock the account on the server. A successful
	// connect or a credentials update clears it.
	markAuthFailed = "auth-failed"
	// markHeld: the user disconnected the share by hand; the agent leaves it
	// alone until they connect it again or the server goes out of reach
	// (i.e. they left that network).
	markHeld = "held"
	// markAutoMounted: the agent connected the share because a profile's
	// network rule matched, so it is the agent's to disconnect again.
	markAutoMounted = "auto-mounted"
	// markAgentMounted: the agent made this connection (for any reason), so
	// it may clean it up when the server stops answering.
	markAgentMounted = "agent-mounted"
	// markStale: the share is mounted but its server has not answered since
	// the marker's modification time.
	markStale = "stale"
)

// unmount is mount.Unmount; tests replace it.
var unmount = mount.Unmount

func markerPath(s *share.Share, kind string) (string, error) {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cfgPath), "state", s.ID+"."+kind), nil
}

// markerSince returns when a marker was set; ok is false if it is not set.
func markerSince(s *share.Share, kind string) (since time.Time, ok bool) {
	p, err := markerPath(s, kind)
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func setMarker(s *share.Share, kind string) {
	if p, err := markerPath(s, kind); err == nil {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
	}
}

func clearMarker(s *share.Share, kind string) {
	if p, err := markerPath(s, kind); err == nil {
		os.Remove(p)
	}
}

func hasMarker(s *share.Share, kind string) bool {
	p, err := markerPath(s, kind)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func clearAllMarkers(s *share.Share) {
	for _, kind := range []string{markAuthFailed, markHeld, markAutoMounted, markAgentMounted, markStale} {
		clearMarker(s, kind)
	}
}

// authFailed reports whether automatic connecting is paused for a share.
func authFailed(s *share.Share) bool { return hasMarker(s, markAuthFailed) }

func clearAuthFailed(s *share.Share) { clearMarker(s, markAuthFailed) }
