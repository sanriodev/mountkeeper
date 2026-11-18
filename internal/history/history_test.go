package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sanriodev/mountkeeper/internal/config"
)

func TestAppendAndRead(t *testing.T) {
	t.Setenv(config.EnvPath, filepath.Join(t.TempDir(), "sub", "config.yaml"))

	if events, err := Read("", 0); err != nil || len(events) != 0 {
		t.Fatalf("empty log: %v %v", events, err)
	}
	base := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for i, e := range []Event{
		{Share: "a", Action: Connected, Trigger: Manual, Detail: "/mnt/a"},
		{Share: "b", Action: Failed, Trigger: Auto, Detail: "authentication failed"},
		{Share: "a", Action: Disconnected, Trigger: Interactive},
		{Share: "a", Action: Connected, Trigger: Auto},
	} {
		e.Time = base.Add(time.Duration(i) * time.Minute)
		if err := Append(e); err != nil {
			t.Fatal(err)
		}
	}

	all, _ := Read("", 0)
	if len(all) != 4 || all[0].Detail != "/mnt/a" || !all[0].Time.Equal(base) {
		t.Fatalf("all = %+v", all)
	}
	if a, _ := Read("a", 0); len(a) != 3 {
		t.Errorf("share a: %d events", len(a))
	}
	if newest, _ := Read("", 2); len(newest) != 2 || newest[1].Trigger != Auto {
		t.Errorf("limit: %+v", newest)
	}
	if e, ok := Last("a", Connected); !ok || e.Trigger != Auto {
		t.Errorf("Last = %+v %v", e, ok)
	}
	if _, ok := Last("b", Connected); ok {
		t.Error("Last found a connect for b")
	}

	path, _ := Path()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v", info.Mode().Perm())
	}

	// A damaged line is skipped, not fatal.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json\n")
	f.Close()
	if all, err := Read("", 0); err != nil || len(all) != 4 {
		t.Errorf("after damage: %d events, %v", len(all), err)
	}

	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	if all, _ := Read("", 0); len(all) != 0 {
		t.Error("Clear left events behind")
	}
	if err := Clear(); err != nil {
		t.Errorf("clearing a missing log: %v", err)
	}
}

func TestTrim(t *testing.T) {
	t.Setenv(config.EnvPath, filepath.Join(t.TempDir(), "config.yaml"))
	path, _ := Path()
	line := `{"time":"2026-11-16T09:00:00Z","share":"old","action":"connected","trigger":"manual"}` + "\n"
	os.WriteFile(path, []byte(strings.Repeat(line, maxSize/len(line)+10)), 0o600)

	if err := Append(Event{Share: "new", Action: Connected, Trigger: Manual}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() > maxSize*3/4 {
		t.Errorf("log not trimmed: %d bytes", info.Size())
	}
	events, err := Read("", 0)
	if err != nil || len(events) == 0 || events[len(events)-1].Share != "new" {
		t.Fatalf("after trim: %d events, %v", len(events), err)
	}
	for _, e := range events {
		if e.Share == "" {
			t.Fatal("trim produced a broken line")
		}
	}
}
