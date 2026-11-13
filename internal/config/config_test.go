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
