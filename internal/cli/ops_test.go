package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sanriodev/mountkeeper/internal/config"
	"github.com/sanriodev/mountkeeper/internal/history"
	"github.com/sanriodev/mountkeeper/internal/macos"
	"github.com/sanriodev/mountkeeper/internal/share"
)

// testConfig gives each test its own configuration file. With the path
// overridden, the operations never touch the real LaunchAgent.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv(config.EnvPath, filepath.Join(t.TempDir(), "config.yaml"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCreateShare(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()

	s, _, err := createShare(cfg, shareSpec{URL: "smb://nas.example.com/Team%20Share", Name: "Team", User: "CORP;alice",
		MountPoint: filepath.Join(dir, "team"), AutoOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "team" || s.Path != "/Team Share" || s.Domain != "CORP" || s.Username != "alice" || !s.AutoOpen {
		t.Errorf("share = %+v", s)
	}
	if s.KeychainAccount != "" {
		t.Errorf("Keychain was not requested, yet account = %q", s.KeychainAccount)
	}

	// Defaults: name from the share, guest access, ~/Network/<id>.
	g, _, err := createShare(cfg, shareSpec{URL: "//nas.example.com/media"})
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != "media" || !g.Guest() || g.MountPoint != "~/Network/media" {
		t.Errorf("defaults = %+v", g)
	}

	for name, spec := range map[string]shareSpec{
		"duplicate name":        {URL: "smb://other.example.com/x", Name: "team"},
		"duplicate mount point": {URL: "smb://other.example.com/x", MountPoint: filepath.Join(dir, "team")},
		"bad address":           {URL: "nas/media"},
		"password in address":   {URL: "smb://alice:pw@nas.example.com/x"},
		"unusable name":         {URL: "smb://nas.example.com/x", Name: "***"},
	} {
		if _, _, err := createShare(cfg, spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	reloaded, err := config.Load()
	if err != nil || len(reloaded.Shares) != 2 {
		t.Fatalf("stored shares: %v, %v", reloaded.IDs(), err)
	}
	raw, _ := os.ReadFile(cfg.Path())
	if strings.Contains(string(raw), "password") {
		t.Errorf("the configuration mentions a password:\n%s", raw)
	}
}

func TestEditAndDelete(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()
	s, _, err := createShare(cfg, shareSpec{URL: "smb://nas.example.com/media", User: "alice", MountPoint: filepath.Join(dir, "media")})
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := createShare(cfg, shareSpec{URL: "smb://nas.example.com/other", MountPoint: filepath.Join(dir, "other")})
	cfg.AddToProfile("office", s.ID, other.ID)

	n, err := editFromSpec(s, shareSpec{Name: "Media Library", URL: "smb://new.example.com/media2", User: "-", MountPoint: filepath.Join(dir, "lib")})
	if err != nil {
		t.Fatal(err)
	}
	changes, _, err := applyEdit(cfg, s, n)
	if err != nil {
		t.Fatal(err)
	}
	var what []string
	for _, c := range changes {
		what = append(what, c.What)
	}
	sort.Strings(what)
	if !reflect.DeepEqual(what, []string{"Address", "Mount point", "Name", "User"}) {
		t.Errorf("changes = %v", what)
	}
	if s.ID != "media" || s.Name != "Media Library" || s.Host != "new.example.com" || !s.Guest() {
		t.Errorf("after edit: %+v", s)
	}

	// Unchanged input changes nothing; a taken mount point is refused.
	same, _ := editFromSpec(s, shareSpec{Name: s.Name, URL: s.URL(), User: userField(s), MountPoint: s.MountPoint})
	if changes, _, err := applyEdit(cfg, s, same); err != nil || len(changes) != 0 {
		t.Errorf("no-op edit: %v %v", changes, err)
	}
	clash, _ := editFromSpec(s, shareSpec{Name: s.Name, MountPoint: filepath.Join(dir, "other")})
	if _, _, err := applyEdit(cfg, s, clash); err == nil {
		t.Error("edit onto another share's mount point accepted")
	}
	empty, _ := editFromSpec(s, shareSpec{Name: "  ", MountPoint: s.MountPoint})
	if _, _, err := applyEdit(cfg, s, empty); err == nil {
		t.Error("empty name accepted")
	}

	setMarker(s, markHeld)
	setMarker(s, markAuthFailed)
	if _, err := deleteShare(cfg, s, false); err != nil {
		t.Fatal(err)
	}
	if cfg.Find("media") != nil || cfg.FindProfile("office").Has("media") {
		t.Error("share or its profile membership survived")
	}
	if hasMarker(s, markHeld) || hasMarker(s, markAuthFailed) {
		t.Error("markers survived")
	}
}

func TestMarkers(t *testing.T) {
	testConfig(t)
	s := &share.Share{ID: "a"}
	if hasMarker(s, markHeld) {
		t.Fatal("fresh share is held")
	}
	setMarker(s, markHeld)
	setMarker(s, markAutoMounted)
	if !hasMarker(s, markHeld) || !hasMarker(s, markAutoMounted) || authFailed(s) {
		t.Error("markers are not independent")
	}
	clearMarker(s, markHeld)
	if hasMarker(s, markHeld) || !hasMarker(s, markAutoMounted) {
		t.Error("clearMarker removed the wrong marker")
	}
	clearAllMarkers(s)
	if hasMarker(s, markAutoMounted) {
		t.Error("clearAllMarkers left one behind")
	}
}

func TestAutoTargets(t *testing.T) {
	cfg := testConfig(t)
	for _, id := range []string{"always", "office1", "office2", "home", "manual"} {
		cfg.Shares = append(cfg.Shares, &share.Share{ID: id, Name: id, Host: "h.example.com", Path: "/" + id, MountPoint: "~/Network/" + id})
	}
	cfg.Find("always").AutoConnect = true
	office, _ := cfg.AddToProfile("office", "office1", "office2", "always")
	office.Auto = []string{"domain:corp.example.com"}
	home, _ := cfg.AddToProfile("home", "home")
	home.Auto = []string{"router:a0:b1:c2:d3:e4:f5"}
	cfg.AddToProfile("unbound", "manual")

	keys := func(m map[string]bool) []string {
		var out []string
		for k, v := range m {
			if v {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}

	atOffice := macos.Network{Interface: "en0", Domains: []string{"corp.example.com"}}
	wanted, via := autoTargets(cfg, atOffice)
	if got := keys(wanted); !reflect.DeepEqual(got, []string{"always", "office1", "office2"}) {
		t.Errorf("at the office wanted = %v", got)
	}
	// "always" connects on its own account, so the agent must not disconnect
	// it when the office network is left.
	if got := keys(via); !reflect.DeepEqual(got, []string{"office1", "office2"}) {
		t.Errorf("at the office via profile = %v", got)
	}

	atHome := macos.Network{Interface: "en0", RouterMAC: "a0:b1:c2:d3:e4:f5"}
	wanted, _ = autoTargets(cfg, atHome)
	if got := keys(wanted); !reflect.DeepEqual(got, []string{"always", "home"}) {
		t.Errorf("at home wanted = %v", got)
	}

	wanted, via = autoTargets(cfg, macos.Network{})
	if got := keys(wanted); !reflect.DeepEqual(got, []string{"always"}) || len(keys(via)) != 0 {
		t.Errorf("offline wanted = %v via = %v", got, keys(via))
	}
}

func TestSetProfileAutoAndHistory(t *testing.T) {
	cfg := testConfig(t)
	p, _ := cfg.AddToProfile("office")

	if err := setProfileAuto(cfg, p, []string{"Domain: Corp.Example.com."}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Auto, []string{"domain:corp.example.com"}) {
		t.Errorf("rule not canonical: %v", p.Auto)
	}
	if err := setProfileAuto(cfg, p, []string{"vlan:7"}); err == nil || hintOf(err) == "" {
		t.Errorf("invalid rule: %v (hint %q)", err, hintOf(err))
	}
	if !reflect.DeepEqual(p.Auto, []string{"domain:corp.example.com"}) {
		t.Errorf("a rejected rule changed the profile: %v", p.Auto)
	}
	if err := setProfileAuto(cfg, p, nil); err != nil || len(p.Auto) != 0 {
		t.Errorf("unbinding: %v %v", p.Auto, err)
	}

	// record uses the current trigger and never fails the caller.
	s := &share.Share{ID: "a"}
	old := trigger
	defer func() { trigger = old }()
	trigger = history.Auto
	record(s, history.Connected, "/mnt/a")
	events, err := history.Read("a", 0)
	if err != nil || len(events) != 1 || events[0].Trigger != history.Auto || events[0].Detail != "/mnt/a" {
		t.Errorf("history = %+v, %v", events, err)
	}
}

func TestPasswordStateWithoutKeychain(t *testing.T) {
	if got := passwordState(&share.Share{ID: "g"}); !strings.Contains(got, "guest") {
		t.Errorf("guest: %q", got)
	}
	if got := passwordState(&share.Share{ID: "u", Username: "alice"}); got != "asked on every connect" {
		t.Errorf("no keychain account: %q", got)
	}
}
