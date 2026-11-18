package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sanriodev/mountkeeper/internal/share"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	t.Setenv(EnvPath, path)

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Shares) != 0 || c.Version != CurrentVersion {
		t.Fatalf("unexpected empty config: %+v", c)
	}

	s, err := share.ParseURL("smb://alice@nas.local/media")
	if err != nil {
		t.Fatal(err)
	}
	s.KeychainAccount = s.ID
	s.AutoConnect = true
	if err := c.Add(s); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(s); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "password") {
		t.Errorf("config must not mention passwords:\n%s", raw)
	}

	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := c2.Find("MEDIA")
	if got == nil || got.Username != "alice" || !got.AutoConnect || got.KeychainAccount != "media" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if !c2.AnyAutoConnect() {
		t.Error("AnyAutoConnect = false")
	}
	if !c2.Remove("media") || c2.Find("media") != nil {
		t.Error("Remove failed")
	}
}

func TestDuplicateMountPoint(t *testing.T) {
	t.Setenv(EnvPath, filepath.Join(t.TempDir(), "config.yaml"))
	c, _ := Load()
	a, _ := share.ParseURL("smb://nas.local/a")
	b, _ := share.ParseURL("smb://nas.local/b")
	b.MountPoint = a.MountPoint
	if err := c.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(b); err == nil {
		t.Fatal("duplicate mount point accepted")
	}
}

func TestLoadRejectsBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv(EnvPath, path)
	os.WriteFile(path, []byte("version: 1\nshares:\n  - id: a\n    host: h\n    path: /a\n  - id: a\n    host: h\n    path: /b\n"), 0o600)
	if _, err := Load(); err == nil {
		t.Fatal("duplicate ids accepted")
	}
}

func TestProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv(EnvPath, path)
	c, _ := Load()
	for _, u := range []string{"smb://nas.local/a", "smb://nas.local/b", "smb://nas.local/c"} {
		s, _ := share.ParseURL(u)
		if err := c.Add(s); err != nil {
			t.Fatal(err)
		}
	}

	p, created := c.AddToProfile("Office", "a", "b")
	if !created || len(p.Shares) != 2 {
		t.Fatalf("AddToProfile: %+v created=%v", p, created)
	}
	if _, created := c.AddToProfile("office", "b", "c"); created || len(p.Shares) != 3 {
		t.Fatalf("extending is case-insensitive and de-duplicates: %+v", p)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c2.GetProfile("OFFICE")
	if err != nil || len(c2.ProfileShares(p2)) != 3 {
		t.Fatalf("profile lost in round trip: %+v %v", p2, err)
	}

	// Removing a share removes it from every profile.
	c2.Remove("b")
	if p2.Has("b") || len(p2.Shares) != 2 {
		t.Errorf("share still in profile: %+v", p2)
	}
	p2.Remove("a")
	if len(p2.Shares) != 1 || !p2.Has("c") {
		t.Errorf("Profile.Remove: %+v", p2)
	}
	if !c2.RemoveProfile("office") || c2.FindProfile("office") != nil {
		t.Error("RemoveProfile failed")
	}
	if _, err := c2.GetProfile("office"); err == nil {
		t.Error("GetProfile found a removed profile")
	}
}

func TestLoadRejectsUnknownProfileShare(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv(EnvPath, path)
	os.WriteFile(path, []byte("version: 1\nshares:\n  - id: a\n    host: h\n    path: /a\nprofiles:\n  - name: p\n    shares: [a, ghost]\n"), 0o600)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown share in profile accepted: %v", err)
	}
}

func TestDidYouMean(t *testing.T) {
	t.Setenv(EnvPath, filepath.Join(t.TempDir(), "config.yaml"))
	c, _ := Load()
	for _, u := range []string{"smb://nas.local/projects", "smb://nas.local/media"} {
		s, _ := share.ParseURL(u)
		c.Add(s)
	}
	for typo, want := range map[string]string{"projcts": "projects", "proj": "projects", "meida": "media"} {
		if _, err := c.Get(typo); err == nil || !strings.Contains(err.Error(), `did you mean "`+want+`"`) {
			t.Errorf("Get(%q): %v", typo, err)
		}
	}
	if _, err := c.Get("zzz"); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("unrelated name got a suggestion: %v", err)
	}
}

func TestNeedsAgent(t *testing.T) {
	t.Setenv(EnvPath, filepath.Join(t.TempDir(), "config.yaml"))
	c, _ := Load()
	a, _ := share.ParseURL("smb://nas.local/a")
	c.Add(a)
	if c.NeedsAgent() {
		t.Error("nothing automatic configured, yet NeedsAgent")
	}
	p, _ := c.AddToProfile("office", "a")
	p.Auto = []string{"domain:corp.example.com"}
	if !c.NeedsAgent() {
		t.Error("a network-bound profile needs the agent")
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, _ := Load()
	if got := c2.FindProfile("office").Auto; len(got) != 1 || got[0] != "domain:corp.example.com" {
		t.Errorf("auto rules lost: %v", got)
	}
	p.Auto = nil
	a.AutoConnect = true
	if !c.NeedsAgent() {
		t.Error("an auto-connect share needs the agent")
	}
}
