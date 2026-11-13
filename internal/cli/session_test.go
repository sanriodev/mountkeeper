package cli

import (
	"os"
	"strings"
	"testing"
)

func TestSessionMismatch(t *testing.T) {
	me := os.Geteuid()
	if msg := sessionMismatch(me, me, true); msg != "" {
		t.Errorf("same user: %q", msg)
	}
	// Nobody at the console (login window, headless): nothing to compare with.
	if msg := sessionMismatch(me, 0, false); msg != "" {
		t.Errorf("unknown console user: %q", msg)
	}
	msg := sessionMismatch(0, me, true)
	if !strings.Contains(msg, "root") || !strings.Contains(msg, userName(me)) {
		t.Errorf("root vs console user: %q", msg)
	}
}

func TestParseBool(t *testing.T) {
	for in, want := range map[string]bool{"true": true, "ON": true, "1": true, "false": false, "Off": false, "no": false} {
		if got, err := parseBool(in); err != nil || got != want {
			t.Errorf("parseBool(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := parseBool("maybe"); err == nil {
		t.Error("parseBool accepted garbage")
	}
}
