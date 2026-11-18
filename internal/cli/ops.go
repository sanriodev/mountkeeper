package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// This file holds the operations behind the commands, free of prompts and
// printing, so that the commands and the interactive interface share them.
// Each returns notes: things worth telling the user that are not errors.

// shareSpec is what a person enters to describe a share.
type shareSpec struct {
	URL, Name, User, MountPoint string
	Keychain                    bool
	Password                    string
	AutoOpen, AutoConnect       bool
}

// applyUser sets user and domain from "user", "DOMAIN;user" or "DOMAIN\user";
// "-" or "" means guest access.
func applyUser(s *share.Share, v string) {
	v = strings.TrimSpace(v)
	if v == "-" {
		v = ""
	}
	d, u := share.SplitUser(v)
	s.Username = u
	if d != "" || u == "" {
		s.Domain = d
	}
}

// userField renders user and domain the way applyUser reads them.
func userField(s *share.Share) string {
	if s.Domain != "" && s.Username != "" {
		return s.Domain + ";" + s.Username
	}
	return s.Username
}

// createShare registers a new share from a description.
func createShare(cfg *config.Config, spec shareSpec) (*share.Share, []string, error) {
	s, err := share.ParseURL(spec.URL)
	if err != nil {
		return nil, nil, err
	}
	if name := strings.TrimSpace(spec.Name); name != "" {
		s.Name = name
	}
	if s.ID = share.Slug(s.Name); s.ID == "" {
		return nil, nil, fmt.Errorf("the name %q contains no usable characters", s.Name)
	}
	if cfg.Find(s.ID) != nil {
		return nil, nil, fmt.Errorf("a share named %q already exists — choose another name", s.ID)
	}
	if strings.TrimSpace(spec.User) != "" {
		applyUser(s, spec.User)
	}

	s.MountPoint = share.DefaultMountRoot + "/" + s.ID
	if mp := strings.TrimSpace(spec.MountPoint); mp != "" {
		s.MountPoint = mp
	}
	abs, err := s.MountPath()
	if err != nil {
		return nil, nil, fmt.Errorf("mount point: %w", err)
	}
	s.MountPoint = share.CollapsePath(abs)

	var notes []string
	if !s.Guest() && spec.Keychain {
		if keychain.Available() != nil {
			notes = append(notes, "The password cannot be stored in the Keychain; it will be asked on each connect. "+keychainUnavailableHint())
		} else {
			s.KeychainAccount = s.ID
		}
	}
	s.AutoOpen, s.AutoConnect = spec.AutoOpen, spec.AutoConnect

	more, err := saveNewShare(cfg, s, spec.Password)
	return s, append(notes, more...), err
}

// saveNewShare stores a fully described share, its password and, if it
// connects automatically, the background agent.
func saveNewShare(cfg *config.Config, s *share.Share, password string) ([]string, error) {
	if err := cfg.Add(s); err != nil {
		return nil, err
	}
	stored := false
	if password != "" && s.KeychainAccount != "" {
		if err := keychain.Set(s.KeychainAccount, s.URL(), password); err != nil {
			cfg.Remove(s.ID)
			return nil, fmt.Errorf("saving the password: %w", err)
		}
		stored = true
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	var notes []string
	if s.AutoConnect {
		if err := syncAgent(cfg); err != nil {
			notes = append(notes, fmt.Sprintf("Automatic connecting could not be set up: %v", err))
		}
		if !s.Guest() && !stored {
			notes = append(notes, "Automatic connecting needs a stored password: mountkeeper credentials set "+s.ID)
		}
	}
	return notes, nil
}

// change is one edited setting.
type change struct{ What, From, To string }

func accountText(s *share.Share) string {
	switch {
	case s.Guest():
		return "guest"
	case s.Domain != "":
		return s.Domain + `\` + s.Username
	}
	return s.Username
}

// applyEdit validates the edited copy n of share s and stores it. The id
// never changes. It returns what changed; an empty list means nothing did.
func applyEdit(cfg *config.Config, s *share.Share, n share.Share) ([]change, []string, error) {
	if strings.TrimSpace(n.Name) == "" {
		return nil, nil, errors.New("the name must not be empty")
	}
	n.Name = strings.TrimSpace(n.Name)
	abs, err := n.MountPath()
	if err != nil {
		return nil, nil, fmt.Errorf("mount point: %w", err)
	}
	n.MountPoint = share.CollapsePath(abs)
	if o := cfg.MountPointOwner(abs, s); o != nil {
		return nil, nil, fmt.Errorf("mount point %s is already used by share %q", n.MountPoint, o.ID)
	}

	var changes []change
	note := func(what, from, to string) {
		if from != to {
			changes = append(changes, change{what, from, to})
		}
	}
	note("Name", s.Name, n.Name)
	note("Address", s.URL(), n.URL())
	note("User", accountText(s), accountText(&n))
	note("Mount point", s.MountPoint, n.MountPoint)
	note("Open in Finder", yesNo(s.AutoOpen), yesNo(n.AutoOpen))
	if len(changes) == 0 {
		return nil, nil, nil
	}

	if s.URL() != n.URL() || accountText(s) != accountText(&n) || s.MountPoint != n.MountPoint {
		st, err := mount.StatusNow(s)
		if err != nil {
			return nil, nil, err
		}
		if st.State == mount.Connected {
			return nil, nil, withHint(fmt.Errorf("%s is connected; its address, user and mount point cannot be changed now", s.ID),
				"Disconnect it first: mountkeeper disconnect %s", s.ID)
		}
	}

	var notes []string
	credentialsChanged := s.Host != n.Host || accountText(s) != accountText(&n)
	if n.Guest() && s.KeychainAccount != "" {
		// A guest share has no password to keep.
		if err := keychain.Delete(s.KeychainAccount); err != nil {
			notes = append(notes, fmt.Sprintf("The Keychain item could not be deleted: %v", err))
		}
		n.KeychainAccount = ""
	}

	*s = n
	if err := cfg.Save(); err != nil {
		return nil, nil, err
	}
	clearAuthFailed(s)
	if credentialsChanged && s.KeychainAccount != "" && keychain.Has(s.KeychainAccount) {
		notes = append(notes, "The stored password was kept. If the new account needs another one: mountkeeper credentials set "+s.ID)
	}
	return changes, notes, nil
}

// editFromSpec builds the edited copy of s from what a person entered.
func editFromSpec(s *share.Share, spec shareSpec) (share.Share, error) {
	n := *s
	n.Name = spec.Name
	if url := strings.TrimSpace(spec.URL); url != "" && url != s.URL() {
		p, err := share.ParseURL(url)
		if err != nil {
			return n, err
		}
		n.Host, n.Port, n.Path = p.Host, p.Port, p.Path
	}
	applyUser(&n, spec.User)
	if mp := strings.TrimSpace(spec.MountPoint); mp != "" {
		n.MountPoint = mp
	}
	n.AutoOpen = spec.AutoOpen
	return n, nil
}

// deleteShare unregisters a share with everything that belongs to it. A
// share connected on its own mount point is only removed with force, which
// disconnects it first.
func deleteShare(cfg *config.Config, s *share.Share, force bool) ([]string, error) {
	st, err := mount.StatusNow(s)
	if err != nil {
		return nil, err
	}
	// Only a mount on our own mount point is ours to tear down.
	if st.State == mount.Connected {
		if !force {
			return nil, withHint(fmt.Errorf("%s is connected", s.ID), "Disconnect it first, or remove it anyway: mountkeeper remove %s --force", s.ID)
		}
		if err := unmountAt(s, st.Path, false); err != nil {
			return nil, err
		}
	}
	var notes []string
	if s.KeychainAccount != "" {
		if err := keychain.Delete(s.KeychainAccount); err != nil {
			notes = append(notes, fmt.Sprintf("The Keychain item could not be deleted: %v", err))
		}
	}
	clearAllMarkers(s)
	cfg.Remove(s.ID)
	if err := cfg.Save(); err != nil {
		return notes, err
	}
	if err := syncAgent(cfg); err != nil {
		notes = append(notes, fmt.Sprintf("Automatic connecting could not be updated: %v", err))
	}
	return notes, nil
}

// storePassword saves the password of a share in the Keychain.
func storePassword(cfg *config.Config, s *share.Share, password string) error {
	if s.Guest() {
		return fmt.Errorf("%s uses guest access and needs no password", s.ID)
	}
	if err := keychain.Available(); err != nil {
		return withHint(err, "%s", keychainUnavailableHint())
	}
	if password == "" {
		return errors.New("empty password; nothing was changed")
	}
	account := s.KeychainAccount
	if account == "" {
		account = s.ID
	}
	if err := keychain.Set(account, s.URL(), password); err != nil {
		return err
	}
	if s.KeychainAccount != account {
		s.KeychainAccount = account
		if err := cfg.Save(); err != nil {
			return err
		}
	}
	clearAuthFailed(s)
	return nil
}

// forgetPassword deletes the stored password; the share then asks on every
// connect. It reports false when nothing was stored.
func forgetPassword(cfg *config.Config, s *share.Share) (bool, error) {
	if s.KeychainAccount == "" {
		return false, nil
	}
	if err := keychain.Delete(s.KeychainAccount); err != nil {
		return false, err
	}
	s.KeychainAccount = ""
	return true, cfg.Save()
}

// passwordState describes in words where the password of a share is.
func passwordState(s *share.Share) string {
	state := passwordStateOnly(s)
	if usesKerberos(s) && !s.Guest() {
		state += " · not used: signs in with Kerberos"
	}
	return state
}

func passwordStateOnly(s *share.Share) string {
	switch {
	case s.Guest():
		return "not needed (guest access)"
	case s.KeychainAccount == "":
		return "asked on every connect"
	case keychain.Available() != nil:
		return "Keychain not available"
	case keychain.Has(s.KeychainAccount):
		return "stored in Keychain"
	}
	return "not stored yet (saved on next connect)"
}

// setAutoConnect switches automatic connecting of a share.
func setAutoConnect(cfg *config.Config, s *share.Share, on bool) error {
	previous := s.AutoConnect
	s.AutoConnect = on
	if err := cfg.Save(); err != nil {
		s.AutoConnect = previous
		return err
	}
	if on {
		clearMarker(s, markAuthFailed)
		clearMarker(s, markHeld)
	}
	if err := syncAgent(cfg); err != nil {
		return fmt.Errorf("the setting was saved, but the background agent could not be updated: %w", err)
	}
	return nil
}

// setProfileAuto binds a profile to networks (or unbinds it with no rules).
func setProfileAuto(cfg *config.Config, p *config.Profile, rules []string) error {
	var clean []string
	for _, r := range rules {
		rule, ok := macos.ValidRule(r)
		if !ok {
			return withHint(fmt.Errorf("%q is not a network rule", r),
				"Rules look like domain:corp.example.com, router:a0:b1:c2:d3:e4:f5 or ssid:Office. \"mountkeeper network\" shows the ones for the current network.")
		}
		clean = append(clean, rule)
	}
	previous := p.Auto
	p.Auto = clean
	if err := cfg.Save(); err != nil {
		p.Auto = previous
		return err
	}
	if err := syncAgent(cfg); err != nil {
		return fmt.Errorf("the setting was saved, but the background agent could not be updated: %w", err)
	}
	return nil
}
