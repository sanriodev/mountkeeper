package share

import (
	"errors"
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		in       string
		host     string
		port     int
		path     string
		domain   string
		user     string
		id       string
		mountURL string
	}{
		{"smb://nas.local/media", "nas.local", 0, "/media", "", "", "media", "smb://nas.local/media"},
		{"//NAS.local/Media/", "nas.local", 0, "/Media", "", "", "media", "smb://nas.local/Media"},
		{"smb://alice@nas.local:4450/dev", "nas.local", 4450, "/dev", "", "alice", "dev", "smb://alice@nas.local:4450/dev"},
		{"smb://CORP;alice@files.example.com/Team%20Share/sub", "files.example.com", 0, "/Team Share/sub", "CORP", "alice", "sub", "smb://CORP;alice@files.example.com/Team%20Share/sub"},
		{"smb://nas.local:445/x", "nas.local", 0, "/x", "", "", "x", "smb://nas.local/x"},
	}
	for _, tt := range tests {
		s, err := ParseURL(tt.in)
		if err != nil {
			t.Errorf("ParseURL(%q): %v", tt.in, err)
			continue
		}
		if s.Host != tt.host || s.Port != tt.port || s.Path != tt.path || s.Domain != tt.domain || s.Username != tt.user || s.ID != tt.id {
			t.Errorf("ParseURL(%q) = %+v", tt.in, s)
		}
		if got := s.MountURL(); got != tt.mountURL {
			t.Errorf("MountURL(%q) = %q, want %q", tt.in, got, tt.mountURL)
		}
	}
}

func TestParseURLErrors(t *testing.T) {
	for _, in := range []string{"", "nas.local/media", "afp://nas.local/media", "smb://nas.local", "smb://nas.local/", "smb:///media", "smb://nas.local/a/../b", "smb://nas.local:99999/a"} {
		if _, err := ParseURL(in); err == nil {
			t.Errorf("ParseURL(%q): expected an error", in)
		}
	}
	if _, err := ParseURL("smb://alice:secret@nas.local/media"); !errors.Is(err, ErrPasswordInURL) {
		t.Errorf("inline password: got %v, want ErrPasswordInURL", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"ematric":       "ematric",
		"Team Share":    "team-share",
		"  Dev/Ops!! ":  "dev-ops",
		"Größe":         "gr-e",
		"a__b":          "a__b",
		"***":           "",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitUser(t *testing.T) {
	if d, u := SplitUser(`CORP\alice`); d != "CORP" || u != "alice" {
		t.Errorf("got %q %q", d, u)
	}
	if d, u := SplitUser("alice"); d != "" || u != "alice" {
		t.Errorf("got %q %q", d, u)
	}
}
