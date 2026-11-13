package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/finder"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

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

	pw, fromKeychain := "", false
	switch {
	case s.Guest():
	case password != nil:
		pw = *password
	case s.KeychainAccount == "":
		return "", tui.ErrPasswordRequired
	default:
		pw, err = keychain.Get(s.KeychainAccount)
		if errors.Is(err, keychain.ErrNotFound) || errors.Is(err, keychain.ErrUnavailable) {
			return "", tui.ErrPasswordRequired
		}
		if err != nil {
			return "", err
		}
		fromKeychain = true
	}

	path, err := mount.Mount(s, pw)
	if err != nil {
		if errors.Is(err, mount.ErrAuth) && fromKeychain {
			return "", fmt.Errorf("%w — the password stored in the Keychain was rejected.\nUpdate it with: mountkeeper credentials set %s", err, s.ID)
		}
		return "", err
	}

	clearAuthFailed(s)
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
			return "", fmt.Errorf("%s needs a password but none is stored.\nSave one with: mountkeeper credentials set %s", s.Name, s.ID)
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
	return true, mount.Unmount(st.Path, force)
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

// syncAgent installs the login LaunchAgent while at least one share has
// auto-connect enabled and removes it otherwise.
func syncAgent(cfg *config.Config) error {
	if !cfg.AnyAutoConnect() {
		return macos.UninstallAgent()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return macos.InstallAgent(exe)
}

// The auth-failed marker stops the background agent from retrying a rejected
// password every few minutes, which could lock the account on the server.
// Any successful manual connect or a credentials update clears it.

func authFailedPath(s *share.Share) (string, error) {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(cfgPath), "state", s.ID+".auth-failed"), nil
}

func markAuthFailed(s *share.Share) {
	if p, err := authFailedPath(s); err == nil {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
	}
}

func clearAuthFailed(s *share.Share) {
	if p, err := authFailedPath(s); err == nil {
		os.Remove(p)
	}
}

func authFailed(s *share.Share) bool {
	p, err := authFailedPath(s)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
