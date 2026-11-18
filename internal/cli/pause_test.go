package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/share"
)

func TestPauseReasonsAndOnce(t *testing.T) {
	cfg := testConfig(t)
	s := &share.Share{ID: "projects", Name: "projects", Host: "fs.example.com", Path: "/projects", Username: "alice", AutoConnect: true}
	cfg.Shares = []*share.Share{s}

	if !pauseShare(s, pauseRejected) {
		t.Fatal("first pause not reported as new")
	}
	if pauseShare(s, pauseRejected) {
		t.Error("the same pause was reported as new again")
	}
	reason, since, ok := pausedNow(cfg, s)
	if !ok || reason != pauseRejected || since.IsZero() {
		t.Fatalf("pausedNow = %q %v %v", reason, since, ok)
	}
	if !authFailed(s) {
		t.Error("authFailed does not see the pause")
	}

	// A different reason replaces the old one but is not a new pause.
	if pauseShare(s, pauseNoPassword) {
		t.Error("changing the reason was reported as a new pause")
	}
	if reason, _, _ := pauseOf(s); reason != pauseNoPassword {
		t.Errorf("reason = %q", reason)
	}

	// Markers of version 1.0 are empty files.
	p, _ := markerPath(s, markAuthFailed)
	os.WriteFile(p, nil, 0o600)
	if reason, _, ok := pauseOf(s); !ok || pauseText(reason) != "password missing or rejected" {
		t.Errorf("old marker: %q %v", reason, ok)
	}

	// Without anything automatic, a leftover marker is no pause.
	s.AutoConnect = false
	if _, _, ok := pausedNow(cfg, s); ok {
		t.Error("a share that does not connect automatically shows as paused")
	}
	cfg.Profiles = []*config.Profile{{Name: "office", Shares: []string{"projects"}, Auto: []string{"domain:corp.example.com"}}}
	if _, _, ok := pausedNow(cfg, s); !ok {
		t.Error("a share of a network-bound profile is not shown as paused")
	}
}

func TestNotifyPaused(t *testing.T) {
	cfg := testConfig(t)
	s := &share.Share{ID: "projects", Name: "Projects", Username: "alice"}
	var sent []string
	old := notify
	t.Cleanup(func() { notify = old })
	notify = func(title, text string) { sent = append(sent, title+": "+text) }

	notifyPaused(cfg, s, pauseRejected)
	if len(sent) != 1 || !strings.Contains(sent[0], "Projects no longer connects automatically: password rejected") ||
		!strings.Contains(sent[0], "mountkeeper credentials set projects") {
		t.Fatalf("sent %q", sent)
	}

	off := false
	cfg.Notifications = &off
	notifyPaused(cfg, s, pauseRejected)
	if len(sent) != 1 {
		t.Error("notified although notifications are off")
	}
}

func TestStatusShowsPause(t *testing.T) {
	r := report{Share: &share.Share{ID: "projects"}, Paused: true, PauseReason: pauseRejected}
	_, detail := r.describe()
	if !strings.Contains(detail, "auto-connect paused: password rejected") {
		t.Errorf("detail = %q", detail)
	}
	if got := r.json().PausedReason; got != "rejected" {
		t.Errorf("json reason = %q", got)
	}
	r.PauseReason = ""
	if got := r.json().PausedReason; got != "unknown" {
		t.Errorf("json reason for an old marker = %q", got)
	}
}
