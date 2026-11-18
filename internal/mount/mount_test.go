package mount

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		{"other share", []Entry{{FSType: "smbfs", From: "//alice@files.example.com/other", On: "/Volumes/other"}}, Disconnected},
		{"other fs on mount point", []Entry{{FSType: "apfs", From: "/dev/disk9", On: filepath.Join(dir, "team")}}, Disconnected},
		{"on mount point", []Entry{{FSType: "smbfs", From: "//alice@files.example.com/Team%20Share", On: filepath.Join(dir, "team")}}, Connected},
		{"via finder", []Entry{{FSType: "smbfs", From: "//CORP;alice@FILES.example.com/Team%20Share", On: "/Volumes/Team Share"}}, Elsewhere},
		{"explicit default port", []Entry{{FSType: "smbfs", From: "//files.example.com:445/team%20share/", On: "/Volumes/x"}}, Elsewhere},
		{"different port", []Entry{{FSType: "smbfs", From: "//files.example.com:4450/Team%20Share", On: "/Volumes/x"}}, Disconnected},
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
	if _, err := PrepareMountPoint(busy, []Entry{{FSType: "apfs", From: "/dev/disk9", On: busy}}); err == nil {
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

func TestErrorKinds(t *testing.T) {
	if _, err := PrepareMountPoint("/", nil); !errors.Is(err, ErrMountPoint) {
		t.Errorf("mount point error is not ErrMountPoint: %v", err)
	}
	for _, rc := range []int{60, 61, 65, 51} {
		if err := netfsError(rc); !errors.Is(err, ErrUnreachable) {
			t.Errorf("errno %d is not ErrUnreachable: %v", rc, err)
		}
	}
	err := Reachable(&share.Share{Host: "does-not-exist.invalid"}, time.Second)
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("unresolvable host is not ErrUnreachable: %v", err)
	}
}

func TestParseBusy(t *testing.T) {
	out := "Volume media on disk5 failed to unmount: dissented by PID 812 (/System/Applications/Preview.app/Contents/MacOS/Preview) Dissenter parent PPID 1 (/sbin/launchd)"
	procs, busy := parseBusy(out)
	if !busy || len(procs) != 1 || procs[0] != "Preview (PID 812)" {
		t.Errorf("parseBusy = %q, %v", procs, busy)
	}
	e := &BusyError{Path: "/Volumes/media", Processes: procs}
	if got := e.Error(); got != "/Volumes/media is still in use by Preview (PID 812)" {
		t.Errorf("BusyError = %q", got)
	}
	if _, busy := parseBusy("Unmount failed for /x: resource busy"); !busy {
		t.Error("\"resource busy\" not recognised")
	}
	if _, busy := parseBusy("Unable to find disk for /x"); busy {
		t.Error("unrelated failure reported as busy")
	}
}

func TestStatusCarriesSizes(t *testing.T) {
	dir := resolve(t.TempDir())
	s := &share.Share{ID: "a", Host: "h.example.com", Path: "/a", MountPoint: filepath.Join(dir, "a")}
	st := StatusOf(s, []Entry{{FSType: "smbfs", From: "//h.example.com/a", On: filepath.Join(dir, "a"), Total: 1000, Free: 250}})
	if st.State != Connected || st.Total != 1000 || st.Free != 250 {
		t.Errorf("status = %+v", st)
	}
}

func TestNetfsError(t *testing.T) {
	if netfsError(80) != ErrAuth {
		t.Error("EAUTH not mapped to ErrAuth")
	}
	if netfsError(2) != ErrShareNotFound {
		t.Error("ENOENT not mapped to ErrShareNotFound")
	}
	if netfsError(-5998) == nil || netfsError(99) == nil || netfsError(-1) == nil {
		t.Error("errors must not be nil")
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[uint64]string{
		0:                 "0 B",
		999:               "999 B",
		1000:              "1.0 kB",
		38_200_000_000:    "38.2 GB",
		500_000_000_000:   "500 GB",
		2_000_000_000_000: "2.0 TB",
	} {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
