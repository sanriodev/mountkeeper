// Package kerberos finds the Kerberos tickets of the logged-in user, for
// shares in an Active Directory domain that sign in with a ticket instead of
// a password. It only reads: tickets are obtained by the user (kinit, the
// macOS Kerberos SSO extension or Platform SSO), never by Mountkeeper, and
// the default credential cache is never switched.
package kerberos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ErrNoTicket means no valid ticket exists for the chosen principal.
var ErrNoTicket = errors.New("no valid Kerberos ticket")

// Credential is one credential cache: a principal and its tickets.
type Credential struct {
	Principal string    // e.g. alice@CORP.EXAMPLE.COM
	Cache     string    // e.g. API:45A0…
	Expires   time.Time // end of the ticket-granting ticket
	// Services lists the service tickets held, e.g. cifs/fs.corp.example.com@CORP.EXAMPLE.COM.
	Services []string
}

// Name is the principal without its realm ("alice").
func (c Credential) Name() string {
	name, _, _ := strings.Cut(c.Principal, "@")
	return name
}

// Realm is the principal's realm ("CORP.EXAMPLE.COM").
func (c Credential) Realm() string {
	_, realm, _ := strings.Cut(c.Principal, "@")
	return realm
}

// Valid reports whether the ticket can still be used at now.
func (c Credential) Valid(now time.Time) bool { return !c.Expires.IsZero() && c.Expires.After(now) }

// HasServiceTicket reports whether a ticket for the SMB service on host is
// already held — a sign that connecting with this credential has worked.
func (c Credential) HasServiceTicket(host string) bool {
	for _, s := range c.Services {
		svc, _, _ := strings.Cut(s, "@")
		if strings.EqualFold(svc, "cifs/"+host) || strings.EqualFold(svc, "smb/"+host) {
			return true
		}
	}
	return false
}

// klist runs /usr/bin/klist; tests replace it.
var klist = func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/klist", "-A", "--json").Output()
}

// Scan returns the credential caches of the user, newest expiry first.
func Scan() ([]Credential, error) {
	out, err := klist()
	if err != nil {
		return nil, fmt.Errorf("listing Kerberos tickets: %w", err)
	}
	return Parse(out)
}

// klistJSON is the output of `klist -A --json` (Heimdal, as shipped with
// macOS).
type klistJSON struct {
	Tickets []struct {
		Cache     string `json:"cache"`
		Principal string `json:"principal"`
		Tickets   []struct {
			Expires   string `json:"Expires"`
			Principal string `json:"Principal"`
		} `json:"tickets"`
	} `json:"tickets"`
}

// Parse reads the output of `klist -A --json`.
func Parse(data []byte) ([]Credential, error) {
	var v klistJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("reading the Kerberos ticket list: %w", err)
	}
	var creds []Credential
	for _, c := range v.Tickets {
		if c.Principal == "" {
			continue
		}
		cred := Credential{Principal: c.Principal, Cache: c.Cache}
		realm := cred.Realm()
		var latest time.Time
		for _, t := range c.Tickets {
			exp, err := time.ParseInLocation("20060102150405", t.Expires, time.Local)
			if err != nil {
				continue
			}
			svc, _, _ := strings.Cut(t.Principal, "@")
			if strings.EqualFold(svc, "krbtgt/"+realm) {
				cred.Expires = exp
			} else if !strings.HasPrefix(strings.ToLower(svc), "krbtgt/") {
				cred.Services = append(cred.Services, t.Principal)
			}
			if exp.After(latest) {
				latest = exp
			}
		}
		if cred.Expires.IsZero() {
			cred.Expires = latest
		}
		creds = append(creds, cred)
	}
	sort.SliceStable(creds, func(i, j int) bool { return creds[i].Expires.After(creds[j].Expires) })
	return creds, nil
}

// Find returns the credential of principal (the realm compared without
// regard to case).
func Find(creds []Credential, principal string) (Credential, bool) {
	for _, c := range creds {
		if SamePrincipal(c.Principal, principal) {
			return c, true
		}
	}
	return Credential{}, false
}

// SamePrincipal compares two principals; names are compared exactly, realms
// without regard to case.
func SamePrincipal(a, b string) bool {
	an, ar, _ := strings.Cut(a, "@")
	bn, br, _ := strings.Cut(b, "@")
	return an == bn && strings.EqualFold(ar, br)
}

// Matches reports whether a credential suits a share with the given user
// name and domain: the names agree (ignoring case, as Active Directory does),
// and the realm is the domain or begins with it (NetBIOS domain CORP, realm
// CORP.EXAMPLE.COM). Without a domain on the share, the name decides.
func Matches(c Credential, user, domain string) bool {
	if user != "" && !strings.EqualFold(c.Name(), user) {
		return false
	}
	if domain == "" {
		return true
	}
	realm := strings.ToUpper(c.Realm())
	domain = strings.ToUpper(domain)
	return realm == domain || strings.HasPrefix(realm, domain+".")
}
