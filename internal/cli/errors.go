package cli

import (
	"errors"
	"fmt"

	"github.com/sanriodev/mountkeeper/internal/kerberos"
	"github.com/sanriodev/mountkeeper/internal/keychain"
	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// hintError carries a suggestion for what to do next. The hint is printed on
// its own line below the error and never becomes part of the message, so
// errors stay comparable and short.
type hintError struct {
	err  error
	hint string
}

func (e *hintError) Error() string { return e.err.Error() }
func (e *hintError) Unwrap() error { return e.err }
func (e *hintError) Hint() string  { return e.hint }

// withHint attaches a hint to err unless it already carries one.
func withHint(err error, format string, a ...any) error {
	if err == nil || hintOf(err) != "" {
		return err
	}
	return &hintError{err: err, hint: fmt.Sprintf(format, a...)}
}

// hintOf returns the hint attached to err, or "".
func hintOf(err error) string {
	var h interface{ Hint() string }
	if errors.As(err, &h) {
		return h.Hint()
	}
	return ""
}

// explain adds the most useful next step to an error that occurred while
// working on share s. Errors that already carry a hint are left alone.
func explain(s *share.Share, err error) error {
	var busy *mount.BusyError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &busy):
		return withHint(err, "Close the files or apps using it, or force it (unsaved changes may be lost):\nmountkeeper disconnect %s --force", s.ID)
	case errors.Is(err, kerberos.ErrNoTicket):
		return withHint(err, "Sign in again (kinit %s, or your company sign-in), then connect.\nOr sign in with the password instead: mountkeeper credentials use %s password", kerberosPrincipalHint(s), s.ID)
	case errors.Is(err, mount.ErrNotResponding):
		return withHint(err, "Its server no longer answers. Force it (unsaved changes may be lost):\nmountkeeper disconnect %s --force", s.ID)
	case errors.Is(err, mount.ErrAuth):
		return withHint(err, "Check the user name (mountkeeper info %s) or enter the password again:\nmountkeeper credentials set %s", s.ID, s.ID)
	case errors.Is(err, mount.ErrUnreachable):
		return withHint(err, "Check the network or VPN connection. \"mountkeeper status\" shows which servers answer.")
	case errors.Is(err, mount.ErrShareNotFound):
		return withHint(err, "See what the server offers: mountkeeper find %s\nThen correct the address: mountkeeper edit %s --url smb://%s/<share>", s.Host, s.ID, s.Host)
	case errors.Is(err, mount.ErrMountPoint):
		return withHint(err, "Use another directory: mountkeeper edit %s --mount-point <dir>", s.ID)
	case errors.Is(err, keychain.ErrDenied):
		return withHint(err, "Choose \"Always Allow\" in the Keychain dialog, or store the password again:\nmountkeeper credentials set %s", s.ID)
	case errors.Is(err, keychain.ErrUnavailable):
		return withHint(err, "%s", keychainUnavailableHint())
	}
	return err
}
