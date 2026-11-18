package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sanriodev/mountkeeper/internal/kerberos"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

func fakeTickets(t *testing.T, creds ...kerberos.Credential) *[]kerberos.Credential {
	list := append([]kerberos.Credential(nil), creds...)
	old := scanTickets
	t.Cleanup(func() { scanTickets = old })
	scanTickets = func() ([]kerberos.Credential, error) { return list, nil }
	return &list
}

func validTicket(principal string) kerberos.Credential {
	return kerberos.Credential{Principal: principal, Expires: time.Now().Add(8 * time.Hour)}
}

func kerberosShare() *share.Share {
	return &share.Share{ID: "projects", Name: "projects", Host: "fs.corp.example.com", Path: "/projects",
		Domain: "CORP", Username: "alice", KeychainAccount: "projects", MountPoint: "~/Network/projects"}
}

func TestSetSignIn(t *testing.T) {
	cfg := testConfig(t)
	s := kerberosShare()
	cfg.Shares = []*share.Share{s}

	// Nothing suits: a clear error, nothing changed.
	fakeTickets(t, validTicket("bob@CORP.EXAMPLE.COM"))
	if _, err := setSignIn(cfg, s, "kerberos", ""); err == nil || !strings.Contains(hintOf(err), "kinit alice@CORP") {
		t.Fatalf("err = %v, hint %q", err, hintOf(err))
	}
	if s.Kerberos() {
		t.Fatal("switched without a ticket")
	}

	// Exactly one suits: chosen.
	fakeTickets(t, validTicket("bob@CORP.EXAMPLE.COM"), validTicket("alice@CORP.EXAMPLE.COM"))
	pauseShare(s, pauseRejected)
	notes, err := setSignIn(cfg, s, "kerberos", "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Kerberos() || s.KerberosPrincipal != "alice@CORP.EXAMPLE.COM" || s.KeychainAccount != "projects" {
		t.Errorf("share = %+v", s)
	}
	if authFailed(s) {
		t.Error("switching did not end the pause")
	}
	if !strings.Contains(strings.Join(notes, " "), "stays in the Keychain") {
		t.Errorf("notes = %v", notes)
	}
	if !strings.Contains(s.MountURL(), "alice%40CORP.EXAMPLE.COM@fs.corp.example.com") {
		t.Errorf("mount URL = %s", s.MountURL())
	}

	// Several suit: the user must choose.
	fakeTickets(t, validTicket("alice@CORP.EXAMPLE.COM"), validTicket("alice@CORP.EXAMPLE.ORG"))
	s.Domain = ""
	if _, err := setSignIn(cfg, s, "kerberos", ""); err == nil || !strings.Contains(err.Error(), "several") {
		t.Errorf("ambiguous choice: %v", err)
	}
	// A named principal without a ticket is accepted with a note.
	notes, err = setSignIn(cfg, s, "kerberos", "alice@OTHER.EXAMPLE.COM")
	if err != nil || !strings.Contains(strings.Join(notes, " "), "no valid ticket") {
		t.Errorf("named principal: %v %v", notes, err)
	}

	// Back to the password.
	if _, err := setSignIn(cfg, s, "password", ""); err != nil || s.Kerberos() || s.KerberosPrincipal != "" {
		t.Errorf("back to password: %+v %v", s, err)
	}

	s.Username = ""
	if _, err := setSignIn(cfg, s, "kerberos", ""); err == nil {
		t.Error("guest share switched to Kerberos")
	}
}

func TestKerberosNeverFallsBackToPassword(t *testing.T) {
	cfg := testConfig(t)
	s := kerberosShare()
	s.Auth, s.KerberosPrincipal = share.AuthKerberos, "alice@CORP.EXAMPLE.COM"
	cfg.Shares = []*share.Share{s}
	expired := validTicket("alice@CORP.EXAMPLE.COM")
	expired.Expires = time.Now().Add(-time.Minute)
	fakeTickets(t, expired)

	_, err := mountShare(s, nil)
	if !errors.Is(err, kerberos.ErrNoTicket) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(hintOf(err), "kinit alice@CORP.EXAMPLE.COM") {
		t.Errorf("hint = %q", hintOf(err))
	}
	if errors.Is(err, tui.ErrPasswordRequired) {
		t.Error("asked for the password although Kerberos is chosen")
	}

	c, ok := kerberosCheck(s)
	if !ok || c.Level != tui.CheckFail {
		t.Errorf("doctor: %+v", c)
	}
	fakeTickets(t, validTicket("alice@CORP.EXAMPLE.COM"))
	if c, _ := kerberosCheck(s); c.Level != tui.CheckOK || !strings.Contains(c.Detail, "valid until") {
		t.Errorf("doctor with ticket: %+v", c)
	}
}

func TestAgentRetriesWhileWaitingForTicket(t *testing.T) {
	cfg := testConfig(t)
	s := kerberosShare()
	s.Auth, s.KerberosPrincipal = share.AuthKerberos, "alice@CORP.EXAMPLE.COM"
	other := &share.Share{ID: "media", Name: "media", Host: "nas.example.com", Path: "/media", Username: "bob", AutoConnect: true}
	s.AutoConnect = true
	cfg.Shares = []*share.Share{s, other}

	pauseShare(s, pauseNoTicket)
	pauseShare(other, pauseRejected)
	var got []string
	for _, x := range cfg.Shares {
		if agentMayTry(x) {
			got = append(got, x.ID)
		}
	}
	if len(got) != 1 || got[0] != "projects" {
		t.Errorf("retried: %v — a share waiting for a ticket is retried, a rejected password is not", got)
	}
}
