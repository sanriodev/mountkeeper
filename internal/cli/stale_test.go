package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sanriodev/mountkeeper/internal/mount"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// fakeAgentWorld replaces the mount table, the server probe, sleeping and
// unmounting for the stale-mount cleanup.
type fakeAgentWorld struct {
	mounted   map[string]bool
	answers   map[string]bool
	unmounted []string
	slept     time.Duration
	logs      []string
}

func newFakeAgentWorld(t *testing.T) *fakeAgentWorld {
	w := &fakeAgentWorld{mounted: map[string]bool{}, answers: map[string]bool{}}
	oldStatus, oldProbe, oldSleep, oldUnmount := statusNow, probe, agentSleep, unmount
	t.Cleanup(func() { statusNow, probe, agentSleep, unmount = oldStatus, oldProbe, oldSleep, oldUnmount })
	statusNow = func(s *share.Share) (mount.Status, error) {
		if w.mounted[s.ID] {
			return mount.Status{State: mount.Connected, Path: "/mnt/" + s.ID}, nil
		}
		return mount.Status{}, nil
	}
	probe = func(shares []*share.Share) map[string]bool {
		out := map[string]bool{}
		for _, s := range shares {
			out[s.ID] = w.answers[s.ID]
		}
		return out
	}
	// Sleeping ages the stale markers instead of waiting.
	agentSleep = func(d time.Duration) { w.slept += d; w.age(d) }
	unmount = func(path string, force bool) error {
		if !force {
			panic("the agent must force the unmount of a dead mount")
		}
		id := strings.TrimPrefix(path, "/mnt/")
		w.unmounted = append(w.unmounted, id)
		w.mounted[id] = false
		return nil
	}
	return w
}

// age moves every stale marker back by d.
func (w *fakeAgentWorld) age(d time.Duration) {
	for _, id := range []string{"office", "byhand", "fine"} {
		s := &share.Share{ID: id}
		if p, err := markerPath(s, markStale); err == nil {
			if info, err := os.Stat(p); err == nil {
				os.Chtimes(p, info.ModTime().Add(-d), info.ModTime().Add(-d))
			}
		}
	}
}

func (w *fakeAgentWorld) logf(format string, a ...any) {
	w.logs = append(w.logs, strings.TrimSpace(fmt.Sprintf(format, a...)))
}

func TestCleanupStaleOnlyAgentMounts(t *testing.T) {
	cfg := testConfig(t)
	for _, id := range []string{"office", "byhand", "fine"} {
		cfg.Shares = append(cfg.Shares, &share.Share{ID: id, Name: id, Host: id + ".example.com", Path: "/" + id})
	}
	w := newFakeAgentWorld(t)
	w.mounted = map[string]bool{"office": true, "byhand": true, "fine": true}
	w.answers = map[string]bool{"fine": true}
	setMarker(cfg.Shares[0], markAgentMounted) // office: connected by the agent
	setMarker(cfg.Shares[2], markAgentMounted) // fine: connected by the agent, server answers

	cleanupStale(cfg, w.logf)

	if len(w.unmounted) != 1 || w.unmounted[0] != "office" {
		t.Fatalf("unmounted %v, want only office", w.unmounted)
	}
	if w.slept != staleGrace {
		t.Errorf("waited %v within the run, want %v", w.slept, staleGrace)
	}
	if hasMarker(cfg.Shares[0], markAgentMounted) || hasMarker(cfg.Shares[0], markStale) {
		t.Error("markers of the cleaned-up share were kept")
	}
	if !hasMarker(cfg.Shares[1], markStale) || !w.mounted["byhand"] {
		t.Error("the share connected by hand must stay mounted and be marked stale")
	}
	if !hasMarker(cfg.Shares[2], markAgentMounted) || hasMarker(cfg.Shares[2], markStale) {
		t.Error("the share whose server answers must be left alone")
	}
	logs := strings.Join(w.logs, "\n")
	if !strings.Contains(logs, "disconnected office") || !strings.Contains(logs, "byhand: its server no longer answers") {
		t.Errorf("log:\n%s", logs)
	}

	// The note about the share connected by hand is written once only.
	w.logs = nil
	cleanupStale(cfg, w.logf)
	if strings.Contains(strings.Join(w.logs, "\n"), "byhand") {
		t.Errorf("repeated log line: %v", w.logs)
	}
}

func TestCleanupStaleSitsOutShortOutages(t *testing.T) {
	cfg := testConfig(t)
	s := &share.Share{ID: "office", Name: "office", Host: "office.example.com", Path: "/office"}
	cfg.Shares = []*share.Share{s}
	w := newFakeAgentWorld(t)
	w.mounted["office"] = true
	setMarker(s, markAgentMounted)

	// The server comes back during the grace period.
	agentSleep = func(d time.Duration) { w.answers["office"] = true }
	cleanupStale(cfg, w.logf)
	if len(w.unmounted) != 0 || hasMarker(s, markStale) {
		t.Fatalf("a short outage led to unmounting %v (stale marker %v)", w.unmounted, hasMarker(s, markStale))
	}
}

func TestStatusReportsNotResponding(t *testing.T) {
	r := report{Status: mount.Status{State: mount.Connected}, Stale: true, Share: &share.Share{ID: "x"}}
	if r.state() != "not_responding" || !r.json().Connected {
		t.Errorf("state %q, connected %v", r.state(), r.json().Connected)
	}
}
