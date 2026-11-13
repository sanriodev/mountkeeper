package mount

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sanriodev/mountkeeper/internal/share"
)

func TestTable(t *testing.T) {
	table, err := Table()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range table {
		if e.On == "/" {
			return
		}
	}
	t.Fatalf("root filesystem missing from mount table: %+v", table)
}

func TestStatusOf(t *testing.T) {
	dir := resolve(t.TempDir())
	s := &share.Share{ID: "team", Host: "files.example.com", Path: "/Team Share", Username: "alice", MountPoint: filepath.Join(dir, "team")}

	cases := []struct {
		name  string
		table []Entry
		want  State
	}{
		{"empty", nil, Disconnected},
		{"other share", []Entry{{"smbfs", "//alice@files.example.com/other", "/Volumes/other"}}, Disconnected},
		{"other fs on mount point", []Entry{{"apfs", "/dev/disk9", filepath.Join(dir, "team")}}, Disconnected},
		{"on mount point", []Entry{{"smbfs", "//alice@files.example.com/Team%20Share", filepath.Join(dir, "team")}}, Connected},
		{"via finder", []Entry{{"smbfs", "//CORP;alice@FILES.example.com/Team%20Share", "/Volumes/Team Share"}}, Elsewhere},
		{"explicit default port", []Entry{{"smbfs", "//files.example.com:445/team%20share/", "/Volumes/x"}}, Elsewhere},
		{"different port", []Entry{{"smbfs", "//files.example.com:4450/Team%20Share", "/Volumes/x"}}, Disconnected},
	}
	for _, c := range cases {
		if got := StatusOf(s, c.table); got.State != c.want {
			t.Errorf("%s: state = %v, want %v", c.name, got.State, c.want)
		}
	}
}

func TestPrepareMountPoint(t *testing.T) {
	dir := resolve(t.TempDir())

	fresh := filepath.Join(dir, "a", "b")
	if got, err := PrepareMountPoint(fresh, nil); err != nil || got != fresh {
		t.Fatalf("fresh: %q %v", got, err)
	}
	if info, err := os.Stat(fresh); err != nil || !info.IsDir() {
		t.Fatal("mount point was not created")
	}

	// An empty directory (apart from Finder metadata) is fine.
	os.WriteFile(filepath.Join(fresh, ".DS_Store"), nil, 0o644)
	if _, err := PrepareMountPoint(fresh, nil); err != nil {
		t.Errorf("empty dir rejected: %v", err)
	}

	os.WriteFile(filepath.Join(fresh, "data.txt"), []byte("x"), 0o644)
	if _, err := PrepareMountPoint(fresh, nil); err == nil {
		t.Error("non-empty directory accepted")
	}

	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if _, err := PrepareMountPoint(file, nil); err == nil {
		t.Error("regular file accepted")
	}

	busy := filepath.Join(dir, "busy")
	os.Mkdir(busy, 0o755)
	if _, err := PrepareMountPoint(busy, []Entry{{"apfs", "/dev/disk9", busy}}); err == nil {
		t.Error("foreign mount point accepted")
	}

	for _, p := range []string{"/", "relative/path"} {
		if _, err := PrepareMountPoint(p, nil); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := PrepareMountPoint(home, nil); err == nil {
			t.Error("home directory accepted")
		}
	}
}

func TestNetfsError(t *testing.T) {
	if netfsError(80) != ErrAuth {
		t.Error("EAUTH not mapped to ErrAuth")
	}
	if netfsError(2) != ErrShareNotFound {
		t.Error("ENOENT not mapped to ErrShareNotFound")
	}
	if netfsError(-5998) == nil || netfsError(60) == nil || netfsError(-1) == nil {
		t.Error("errors must not be nil")
	}
}
