// Package tui implements Mountkeeper's interactive interface: a menu-driven
// application in which everything the commands can do is reachable with the
// arrow keys, Enter and Esc. It knows nothing about the Keychain, NetFS or
// the configuration file; all of that goes through the Backend supplied by
// the CLI layer.
package tui

import (
	"errors"
	"time"

	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// ErrPasswordRequired is returned by Backend.Connect when the share needs a
// password that is not available; the interface then asks for it.
var ErrPasswordRequired = errors.New("password required")

// Profile is a named group of shares.
type Profile struct {
	Name string
	IDs  []string
	Auto []string // network rules; empty = not bound to a network
}

// ShareForm is what a person enters to add or edit a share.
type ShareForm struct {
	URL, Name, User, MountPoint string
	Keychain                    bool
	Password                    string
	AutoOpen, AutoConnect       bool
}

// Server is an SMB server found on the local network.
type Server struct {
	Name string
	Host string
}

// Rule is a network rule together with its description in words.
type Rule struct {
	Value string // e.g. "domain:corp.example.com"
	Label string // e.g. "network domain corp.example.com"
}

// NetworkInfo describes the network the Mac is on.
type NetworkInfo struct {
	Rows  [][2]string // label and value, for display
	Rules []Rule      // rules that match this network
}

// Pause says why the background agent stopped connecting a share by itself.
type Pause struct {
	Code  string // "no-password", "rejected", "kerberos-no-ticket", "kerberos-rejected", or "" (unknown)
	Text  string // the reason in words, e.g. "password rejected"
	Since time.Time
}

// Ticket is a Kerberos ticket of the user, as offered for signing in.
type Ticket struct {
	Principal string // e.g. alice@CORP.EXAMPLE.COM
	Expires   time.Time
	Valid     bool
	Matches   bool // suits the share's user and domain
	Used      bool // a ticket for this share's server is held already
}

// CheckLevel grades the outcome of one diagnostic check.
type CheckLevel int

const (
	CheckOK CheckLevel = iota
	CheckWarn
	CheckFail
)

// Check is one result of the diagnosis ("mountkeeper doctor").
type Check struct {
	Group  string // "Agent", "Keychain", "Servers", "Shares"
	Label  string // what was checked, e.g. "Background agent" or a share id
	Level  CheckLevel
	Detail string // the finding
	Hint   string // what to do about it; empty when nothing needs doing
	// Fix: RepairAgent resolves this finding.
	Fix bool
}

// Backend performs the actual work on behalf of the interface.
type Backend interface {
	Shares() []*share.Share
	Profiles() []Profile
	// Statuses returns the live state of every share, keyed by share id.
	Statuses() (map[string]mount.Status, error)
	// Reachable reports, per share id, whether its server answers — for
	// connected shares too, whose server may have gone away since.
	Reachable() map[string]bool
	// Paused reports whether, why and since when automatic connecting is
	// suspended for a share.
	Paused(s *share.Share) (Pause, bool)

	// Connect mounts a share. password is nil unless the user just typed one.
	Connect(s *share.Share, password *string) (path string, err error)
	// Disconnect unmounts the volume of s at path. force unmounts even with
	// open files or an unresponsive server (unsaved changes may be lost).
	Disconnect(s *share.Share, path string, force bool) error
	// Open shows a mounted path in Finder.
	Open(path string) error

	// The following return notes: remarks for the user that are not errors.
	AddShare(f ShareForm) (*share.Share, []string, error)
	EditShare(s *share.Share, f ShareForm) ([]string, error)
	RemoveShare(s *share.Share) ([]string, error)
	UserField(s *share.Share) string
	SetAutoConnect(s *share.Share, on bool) error
	SetAutoOpen(s *share.Share, on bool) error
	PasswordState(s *share.Share) string
	SetPassword(s *share.Share, password string) error
	RemovePassword(s *share.Share) error
	// Tickets lists the user's Kerberos tickets.
	Tickets(s *share.Share) ([]Ticket, error)
	// SetSignIn switches between "password" and "kerberos" (with principal).
	SetSignIn(s *share.Share, method, principal string) ([]string, error)

	// SaveProfile creates a profile or replaces its shares.
	SaveProfile(name string, ids []string) error
	RemoveProfile(name string) error
	SetProfileAuto(name string, rules []string) error
	DescribeRule(rule string) string

	Network() NetworkInfo
	FindServers() ([]Server, error)
	// Doctor runs the diagnosis.
	Doctor() []Check
	// RepairAgent installs, updates or removes the background agent so that
	// it matches the configuration and this binary.
	RepairAgent() error

	// History returns the newest events (oldest first) of one share, or of
	// all shares when shareID is empty.
	History(shareID string, limit int) []history.Event
}
