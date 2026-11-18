package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/kerberos"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

// scanTickets lists the user's Kerberos tickets; tests replace it.
var scanTickets = kerberos.Scan

// usesKerberos reports whether s signs in with a Kerberos ticket.
func usesKerberos(s *share.Share) bool { return s.Kerberos() }

// kerberosPrincipalHint names the principal to sign in as.
func kerberosPrincipalHint(s *share.Share) string {
	switch {
	case s.KerberosPrincipal != "":
		return s.KerberosPrincipal
	case s.Domain != "":
		return s.Username + "@" + strings.ToUpper(s.Domain)
	}
	return s.Username
}

// matchingTickets returns the tickets that suit s, valid ones first.
func matchingTickets(s *share.Share, creds []kerberos.Credential) []kerberos.Credential {
	var out []kerberos.Credential
	for _, c := range creds {
		if kerberos.Matches(c, s.Username, s.Domain) {
			out = append(out, c)
		}
	}
	return out
}

// ticketFor finds the valid ticket a Kerberos share signs in with. There is
// deliberately no fallback to the password: the user chose Kerberos.
func ticketFor(s *share.Share) (kerberos.Credential, error) {
	creds, err := scanTickets()
	if err != nil {
		return kerberos.Credential{}, err
	}
	now := time.Now()
	if s.KerberosPrincipal != "" {
		if c, ok := kerberos.Find(creds, s.KerberosPrincipal); ok && c.Valid(now) {
			return c, nil
		}
		return kerberos.Credential{}, fmt.Errorf("%w for %s", kerberos.ErrNoTicket, s.KerberosPrincipal)
	}
	for _, c := range matchingTickets(s, creds) {
		if c.Valid(now) {
			return c, nil
		}
	}
	return kerberos.Credential{}, fmt.Errorf("%w for %s", kerberos.ErrNoTicket, kerberosPrincipalHint(s))
}

// kerberosCheck diagnoses the ticket of a Kerberos share.
func kerberosCheck(s *share.Share) (tui.Check, bool) {
	if !usesKerberos(s) {
		return tui.Check{}, false
	}
	c := tui.Check{Group: "Shares", Label: s.ID + " (Kerberos)"}
	ticket, err := ticketFor(s)
	switch {
	case errors.Is(err, kerberos.ErrNoTicket):
		c.Level, c.Detail = tui.CheckFail, "no valid ticket for "+kerberosPrincipalHint(s)
		c.Hint = "Sign in again: kinit " + kerberosPrincipalHint(s) + " (or your company sign-in)"
	case err != nil:
		c.Level, c.Detail = tui.CheckFail, err.Error()
	case time.Until(ticket.Expires) < time.Hour:
		c.Level, c.Detail = tui.CheckWarn, "ticket of "+ticket.Principal+" expires at "+ticket.Expires.Format("15:04")
		c.Hint = "Renew it soon: kinit " + ticket.Principal + " (or your company sign-in)"
	default:
		c.Detail = "ticket of " + ticket.Principal + " valid until " + ticket.Expires.Format("2006-01-02 15:04")
	}
	return c, true
}

// setSignIn switches how s signs in. For Kerberos without a principal, the
// one ticket that suits the share is chosen. The Keychain password is kept
// either way, so that switching back needs nothing.
func setSignIn(cfg *config.Config, s *share.Share, method, principal string) ([]string, error) {
	var notes []string
	switch method {
	case share.AuthPassword, "":
		s.Auth, s.KerberosPrincipal = "", ""
	case share.AuthKerberos:
		if s.Guest() {
			return nil, withHint(fmt.Errorf("%s uses guest access, which needs no sign-in", s.ID), "Set a user first: mountkeeper edit %s --user DOMAIN;user", s.ID)
		}
		creds, err := scanTickets()
		if err != nil {
			return nil, err
		}
		if principal == "" {
			matches := matchingTickets(s, creds)
			switch len(matches) {
			case 0:
				return nil, withHint(fmt.Errorf("no Kerberos ticket suits %s (user %s)", s.ID, accountText(s)),
					"Sign in first (kinit %s, or your company sign-in), or name the principal: --principal user@REALM\n\"mountkeeper credentials scan %s\" lists the tickets found.", kerberosPrincipalHint(s), s.ID)
			case 1:
				principal = matches[0].Principal
			default:
				var names []string
				for _, c := range matches {
					names = append(names, c.Principal)
				}
				return nil, withHint(fmt.Errorf("several Kerberos tickets suit %s: %s", s.ID, strings.Join(names, ", ")),
					"Choose one: mountkeeper credentials use %s kerberos --principal <principal>", s.ID)
			}
		} else if c, ok := kerberos.Find(creds, principal); !ok || !c.Valid(time.Now()) {
			notes = append(notes, "There is no valid ticket for "+principal+" right now; connecting needs one (kinit "+principal+", or your company sign-in).")
		}
		s.Auth, s.KerberosPrincipal = share.AuthKerberos, principal
		if s.KeychainAccount != "" {
			notes = append(notes, "The stored password stays in the Keychain for switching back.")
		}
	default:
		return nil, fmt.Errorf("unknown sign-in method %q (use kerberos or password)", method)
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	clearAuthFailed(s)
	return notes, nil
}

// signInText describes in words how s signs in.
func signInText(s *share.Share) string {
	switch {
	case s.Guest():
		return "guest access"
	case usesKerberos(s):
		return "Kerberos ticket of " + kerberosPrincipalHint(s)
	}
	return "password"
}

// tuiTickets lists the tickets for the interface.
func tuiTickets(s *share.Share) ([]tui.Ticket, error) {
	creds, err := scanTickets()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]tui.Ticket, 0, len(creds))
	for _, c := range creds {
		out = append(out, tui.Ticket{
			Principal: c.Principal, Expires: c.Expires, Valid: c.Valid(now),
			Matches: kerberos.Matches(c, s.Username, s.Domain), Used: c.HasServiceTicket(s.Host),
		})
	}
	return out, nil
}
