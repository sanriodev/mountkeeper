package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/share"
	"github.com/sanriodev/mountkeeper/internal/tui"
)

func fakeDoctor(t *testing.T, info macos.AgentInfo, self string) {
	oldInspect, oldExe, oldLookup, oldProbe := inspectAgent, executable, lookupHost, probePort
	t.Cleanup(func() { inspectAgent, executable, lookupHost, probePort = oldInspect, oldExe, oldLookup, oldProbe })
	inspectAgent = func() macos.AgentInfo { return info }
	executable = func() (string, error) { return self, nil }
}

func binary(t *testing.T, dir, name string) string {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAgentChecks(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()
	brew, dev := binary(t, dir, "brew-mountkeeper"), binary(t, dir, "dev-mountkeeper")

	find := func(checks []tui.Check) tui.Check {
		for _, c := range checks {
			if c.Label == "Background agent" {
				return c
			}
		}
		t.Fatal("no agent check")
		return tui.Check{}
	}

	fakeDoctor(t, macos.AgentInfo{}, brew)
	if c := find(agentChecks(cfg)); c.Level != tui.CheckOK || !strings.Contains(c.Detail, "not needed") {
		t.Errorf("nothing automatic: %+v", c)
	}

	cfg.Shares = []*share.Share{{ID: "p", AutoConnect: true}}
	if c := find(agentChecks(cfg)); c.Level != tui.CheckFail || !c.Fix {
		t.Errorf("needed but missing: %+v", c)
	}

	cases := []struct {
		info  macos.AgentInfo
		level tui.CheckLevel
		want  string
	}{
		{macos.AgentInfo{Installed: true, Loaded: true, Program: brew, Current: true}, tui.CheckOK, "loaded · runs"},
		{macos.AgentInfo{Installed: true, Loaded: false, Program: brew, Current: true}, tui.CheckFail, "not loaded"},
		{macos.AgentInfo{Installed: true, Loaded: true, Program: dev, Current: true}, tui.CheckWarn, "but this is"},
		{macos.AgentInfo{Installed: true, Loaded: true, Program: filepath.Join(dir, "gone"), Current: true}, tui.CheckFail, "does not exist"},
		{macos.AgentInfo{Installed: true, Loaded: true, Program: brew, Current: false}, tui.CheckWarn, "older version"},
	}
	for _, tc := range cases {
		fakeDoctor(t, tc.info, brew)
		c := find(agentChecks(cfg))
		if c.Level != tc.level || !strings.Contains(c.Detail, tc.want) {
			t.Errorf("%+v: got %v %q, want %v %q", tc.info, c.Level, c.Detail, tc.level, tc.want)
		}
	}

	// Installed although nothing is automatic any more.
	cfg.Shares = nil
	fakeDoctor(t, macos.AgentInfo{Installed: true, Loaded: true, Program: brew, Current: true}, brew)
	if c := find(agentChecks(cfg)); c.Level != tui.CheckWarn || !strings.Contains(c.Hint, "removes it") {
		t.Errorf("superfluous agent: %+v", c)
	}
}

func TestServerAndShareChecks(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()
	full := filepath.Join(dir, "full")
	os.MkdirAll(full, 0o755)
	os.WriteFile(filepath.Join(full, "file"), nil, 0o600)
	cfg.Shares = []*share.Share{
		{ID: "a", Host: "up.example.com", Path: "/a", MountPoint: filepath.Join(dir, "a")},
		{ID: "b", Host: "up.example.com", Path: "/b", MountPoint: full},
		{ID: "c", Host: "nowhere.example.com", Path: "/c", MountPoint: filepath.Join(dir, "c")},
		{ID: "d", Host: "silent.example.com", Path: "/d", MountPoint: filepath.Join(dir, "d")},
	}
	fakeDoctor(t, macos.AgentInfo{}, "")
	lookupHost = func(h string) error {
		if h == "nowhere.example.com" {
			return errors.New("no such host")
		}
		return nil
	}
	probes := 0
	probePort = func(s *share.Share) error {
		probes++
		if s.Host == "silent.example.com" {
			return errors.New("timeout")
		}
		return nil
	}

	servers, answers := serverChecks(cfg)
	if len(servers) != 3 || probes != 2 {
		t.Fatalf("%d server checks, %d probes: %+v", len(servers), probes, servers)
	}
	if !answers["a"] || !answers["b"] || answers["c"] || answers["d"] {
		t.Errorf("answers = %v", answers)
	}
	if servers[1].Level != tui.CheckWarn || !strings.Contains(servers[1].Detail, "cannot be resolved") {
		t.Errorf("unresolvable: %+v", servers[1])
	}

	shares := shareChecks(cfg, answers)
	if shares[0].Level != tui.CheckOK || shares[1].Level != tui.CheckFail || !strings.Contains(shares[1].Detail, "not empty") {
		t.Errorf("share checks: %+v", shares)
	}
}
