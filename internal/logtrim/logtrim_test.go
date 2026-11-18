package logtrim

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTrimKeepsNewerHalfAtLineBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	var b bytes.Buffer
	for i := 0; b.Len() < 300<<10; i++ {
		fmt.Fprintf(&b, "line %06d some text\n", i)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	// A descriptor opened the way launchd opens StandardOutPath.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := Trim(path, 256<<10); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("after trim\n"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 160<<10 {
		t.Errorf("trimmed log is %d bytes, want about half of %d", len(got), b.Len())
	}
	if !bytes.HasPrefix(got, []byte("line ")) {
		t.Errorf("trimmed log does not start at a line: %q", got[:20])
	}
	if bytes.IndexByte(got, 0) >= 0 {
		t.Error("trimmed log contains NUL bytes")
	}
	if !bytes.HasSuffix(got, []byte("some text\nafter trim\n")) {
		t.Errorf("append after trim did not land at the end: %q", got[len(got)-40:])
	}
}

func TestTrimLeavesSmallAndMissingFilesAlone(t *testing.T) {
	dir := t.TempDir()
	if err := Trim(filepath.Join(dir, "missing"), 10); err != nil {
		t.Errorf("missing file: %v", err)
	}
	path := filepath.Join(dir, "small")
	os.WriteFile(path, []byte("a\nb\n"), 0o600)
	if err := Trim(path, 10); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "a\nb\n" {
		t.Errorf("small file changed: %q", got)
	}
}
