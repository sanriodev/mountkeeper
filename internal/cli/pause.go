package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

// The background agent pauses a share instead of retrying when retrying
// cannot help (or could lock the account). The reason is kept in the
// auth-failed marker; its modification time says since when.
const (
	pauseNoPassword     = "no-password"
	pauseRejected       = "rejected"
	pauseNoTicket       = "kerberos-no-ticket"
	pauseTicketRejected = "kerberos-rejected"
)

// pauseText describes a pause reason in words.
func pauseText(reason string) string {
	switch reason {
	case pauseNoPassword:
		return "no password stored"
	case pauseRejected:
		return "password rejected"
	case pauseNoTicket:
		return "no valid Kerberos ticket"
	case pauseTicketRejected:
		return "Kerberos ticket rejected"
	}
	return "password missing or rejected" // markers written by 1.0
}

// pauseHint is the command that ends a pause.
func pauseHint(s *share.Share, reason string) string {
	switch reason {
	case pauseNoTicket:
		return "Sign in again (kinit " + kerberosPrincipalHint(s) + ", or your company sign-in); it resumes by itself"
	case pauseTicketRejected:
		return "mountkeeper credentials scan " + s.ID
	}
	return "mountkeeper credentials set " + s.ID
}

// pauseShare suspends automatic connecting of s. It reports whether the share
// was not paused before, so that the user is told only once.
func pauseShare(s *share.Share, reason string) (isNew bool) {
	p, err := markerPath(s, markAuthFailed)
	if err != nil {
		return false
	}
	_, statErr := os.Stat(p)
	isNew = statErr != nil
	if old, err := os.ReadFile(p); err == nil && string(old) == reason {
		return false // unchanged: keep the original time
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(reason), 0o600)
	return isNew
}

// pauseOf returns why and since when automatic connecting of s is paused.
func pauseOf(s *share.Share) (reason string, since time.Time, ok bool) {
	since, ok = markerSince(s, markAuthFailed)
	if !ok {
		return "", time.Time{}, false
	}
	if p, err := markerPath(s, markAuthFailed); err == nil {
		if data, err := os.ReadFile(p); err == nil {
			reason = strings.TrimSpace(string(data))
		}
	}
	return reason, since, true
}

// autoWanted reports whether anything asks for s to be connected
// automatically: its own flag or a profile bound to a network. A pause only
// matters then.
func autoWanted(cfg *config.Config, s *share.Share) bool {
	if s.AutoConnect {
		return true
	}
	for _, p := range cfg.Profiles {
		if len(p.Auto) > 0 && p.Has(s.ID) {
			return true
		}
	}
	return false
}

// pausedNow is pauseOf limited to shares that connect automatically.
func pausedNow(cfg *config.Config, s *share.Share) (reason string, since time.Time, ok bool) {
	if !autoWanted(cfg, s) {
		return "", time.Time{}, false
	}
	return pauseOf(s)
}

// tuiPause is the pause of s for the interactive interface.
func tuiPause(cfg *config.Config, s *share.Share) (tui.Pause, bool) {
	reason, since, ok := pausedNow(cfg, s)
	if !ok {
		return tui.Pause{}, false
	}
	return tui.Pause{Code: reason, Text: pauseText(reason), Since: since}, true
}

// notify shows a macOS notification; tests replace it.
var notify = macos.Notify

// notifyPaused tells the user once that a share stopped connecting by itself.
func notifyPaused(cfg *config.Config, s *share.Share, reason string) {
	if !cfg.NotificationsEnabled() {
		return
	}
	text := fmt.Sprintf("%s no longer connects automatically: %s.", s.Name, pauseText(reason))
	if hint := pauseHint(s, reason); strings.HasPrefix(hint, "mountkeeper ") {
		text += " Run: " + hint
	} else {
		text += " " + hint + "."
	}
	notify("Mountkeeper", text)
}
