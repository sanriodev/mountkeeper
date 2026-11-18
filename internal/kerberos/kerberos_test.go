package kerberos

import (
	"testing"
	"time"
)

const sample = `{ "version" : 1, "tickets" : [
 { "version" : 1, "cache" : "API:1", "principal" : "alice@CORP.EXAMPLE.COM", "tickets" : [
   {"Issued" : "20261118081502","Expires" : "20261118181502","Principal" : "krbtgt/CORP.EXAMPLE.COM@CORP.EXAMPLE.COM"},
   {"Issued" : "20261118081502","Expires" : "20261118181502","Principal" : "cifs/fs.corp.example.com@CORP.EXAMPLE.COM"}]},
 { "version" : 1, "cache" : "API:2", "principal" : "admin-alice@CORP.EXAMPLE.COM", "tickets" : [
   {"Issued" : "20261117081502","Expires" : "20261117181502","Principal" : "krbtgt/CORP.EXAMPLE.COM@CORP.EXAMPLE.COM"}]}
] }`

func TestParse(t *testing.T) {
	creds, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 2 {
		t.Fatalf("%d credentials", len(creds))
	}
	a := creds[0]
	if a.Principal != "alice@CORP.EXAMPLE.COM" || a.Name() != "alice" || a.Realm() != "CORP.EXAMPLE.COM" || a.Cache != "API:1" {
		t.Errorf("first = %+v", a)
	}
	want := time.Date(2026, 11, 18, 18, 15, 2, 0, time.Local)
	if !a.Expires.Equal(want) {
		t.Errorf("expires %v, want %v", a.Expires, want)
	}
	if !a.HasServiceTicket("FS.corp.example.com") || a.HasServiceTicket("other.example.com") {
		t.Errorf("service tickets: %v", a.Services)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)
	if !a.Valid(now) || creds[1].Valid(now) {
		t.Error("validity wrong")
	}
	if c, ok := Find(creds, "alice@corp.example.com"); !ok || c.Cache != "API:1" {
		t.Error("Find ignores the realm's case badly")
	}
	if _, ok := Find(creds, "Alice@CORP.EXAMPLE.COM"); ok {
		t.Error("Find must compare names exactly")
	}
}

func TestParseEmptyAndBroken(t *testing.T) {
	for _, in := range []string{`{ "version" : 1, "tickets" : [] }`, `{ "version" : 1 }`} {
		creds, err := Parse([]byte(in))
		if err != nil || len(creds) != 0 {
			t.Errorf("%s: %v %v", in, creds, err)
		}
	}
	if _, err := Parse([]byte("klist: No credentials cache found")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestMatches(t *testing.T) {
	c := Credential{Principal: "alice@CORP.EXAMPLE.COM"}
	cases := []struct {
		user, domain string
		want         bool
	}{
		{"alice", "CORP", true},
		{"Alice", "corp.example.com", true},
		{"alice", "", true},
		{"", "CORP", true},
		{"bob", "CORP", false},
		{"alice", "OTHER", false},
		{"alice", "CORPX", false},
	}
	for _, tc := range cases {
		if got := Matches(c, tc.user, tc.domain); got != tc.want {
			t.Errorf("Matches(%q, %q) = %v", tc.user, tc.domain, got)
		}
	}
}

func TestScanUsesKlist(t *testing.T) {
	old := klist
	t.Cleanup(func() { klist = old })
	klist = func() ([]byte, error) { return []byte(sample), nil }
	creds, err := Scan()
	if err != nil || len(creds) != 2 {
		t.Fatalf("%v %v", creds, err)
	}
}
