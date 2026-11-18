package macos

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBrowse(t *testing.T) {
	out := `Browsing for _smb._tcp.local.
DATE: ---Fri 13 Nov 2026---
12:00:00.000  ...STARTING...
Timestamp     A/R    Flags  if Domain               Service Type         Instance Name
12:00:00.100  Add        3   6 local.               _smb._tcp.           Office NAS
12:00:00.101  Add        2   6 local.               _smb._tcp.           mini
12:00:00.102  Add        2   7 local.               _smb._tcp.           Office NAS
12:00:01.000  Rmv        0   6 local.               _smb._tcp.           gone
`
	want := []string{"Office NAS", "mini"}
	if got := parseBrowse([]byte(out)); !reflect.DeepEqual(got, want) {
		t.Errorf("parseBrowse = %q, want %q", got, want)
	}
}

func TestResolveLine(t *testing.T) {
	m := resolveLine.FindStringSubmatch("12:00:00.000  Office\\032NAS._smb._tcp.local. can be reached at office-nas.local.:445 (interface 6)")
	if m == nil || m[1] != "office-nas.local" {
		t.Errorf("resolveLine = %q", m)
	}
}

func TestAgentPlist(t *testing.T) {
	p := agentPlist("/opt/home brew/bin/mount<keeper>&", "/Users/x/Library/LaunchAgents/a.plist", "/Users/x/Library/Logs/mountkeeper.log")
	if err := xml.Unmarshal(p, new(any)); err != nil {
		t.Fatalf("plist is not well-formed XML: %v", err)
	}
	s := string(p)
	for _, want := range []string{AgentLabel, "mount&lt;keeper&gt;&amp;", "connect --auto", "<key>RunAtLoad</key>", agentMarker, "/var/run/resolv.conf", "<string>/bin/sh</string>"} {
		if !strings.Contains(s, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
}

func TestParseNetwork(t *testing.T) {
	route := `   route to: default
destination: default
       mask: default
    gateway: 10.20.0.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
`
	if iface, gw := parseRoute([]byte(route)); iface != "en0" || gw != "10.20.0.1" {
		t.Errorf("parseRoute = %q %q", iface, gw)
	}
	if mac := parseARP([]byte("? (10.20.0.1) at a0:b:c2:d3:4:f5 on en0 ifscope [ethernet]\n")); mac != "a0:0b:c2:d3:04:f5" {
		t.Errorf("parseARP = %q", mac)
	}
	if mac := parseARP([]byte("? (10.20.0.1) at (incomplete) on en0 ifscope [ethernet]\n")); mac != "" {
		t.Errorf("incomplete ARP entry gave %q", mac)
	}
	dns := `DNS configuration

resolver #1
  search domain[0] : Corp.Example.com
  search domain[1] : lan.example.org.
  nameserver[0] : 10.20.0.2

resolver #2
  domain   : local
  options  : mdns

resolver #3
  domain   : 254.169.in-addr.arpa
  domain   : corp.example.com
`
	want := []string{"corp.example.com", "lan.example.org"}
	if got := parseDNS([]byte(dns)); !reflect.DeepEqual(got, want) {
		t.Errorf("parseDNS = %q, want %q", got, want)
	}
	if ssid := parseSSID([]byte("  InterfaceType : WiFi\n  SSID : Office Net\n  BSSID : aa:bb\n")); ssid != "Office Net" {
		t.Errorf("parseSSID = %q", ssid)
	}
	if ssid := parseSSID([]byte("  SSID : <redacted>\n")); ssid != "" {
		t.Errorf("redacted SSID gave %q", ssid)
	}
}

func TestNetworkRules(t *testing.T) {
	n := Network{Interface: "en0", RouterIP: "10.20.0.1", RouterMAC: "a0:0b:c2:d3:04:f5", Domains: []string{"corp.example.com"}, SSID: "Office"}
	for rule, want := range map[string]bool{
		"domain:corp.example.com":  true,
		"domain:CORP.example.com":  true,
		"domain:other.example.com": false,
		"router:A0:B:C2:D3:4:F5":   true,
		"router:00:00:00:00:00:00": false,
		"ssid:Office":              true,
		"ssid:office":              false,
		"nonsense":                 false,
		"domain:":                  false,
	} {
		if got := n.Matches(rule); got != want {
			t.Errorf("Matches(%q) = %v, want %v", rule, got, want)
		}
	}
	if !n.MatchesAny([]string{"ssid:Home", "domain:corp.example.com"}) || n.MatchesAny(nil) {
		t.Error("MatchesAny is wrong")
	}
	wantRules := []string{"domain:corp.example.com", "ssid:Office", "router:a0:0b:c2:d3:04:f5"}
	if got := n.Rules(); !reflect.DeepEqual(got, wantRules) {
		t.Errorf("Rules = %q", got)
	}
	if (Network{}).Online() || !n.Online() {
		t.Error("Online is wrong")
	}

	for in, want := range map[string]string{
		"Domain: Corp.Example.com.": "domain:corp.example.com",
		"router:A0-B-C2-D3-4-F5":    "router:a0:0b:c2:d3:04:f5",
		"ssid:My Wi-Fi":             "ssid:My Wi-Fi",
	} {
		if got, ok := ValidRule(in); !ok || got != want {
			t.Errorf("ValidRule(%q) = %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "domain", "router:zz", "vlan:7"} {
		if _, ok := ValidRule(bad); ok {
			t.Errorf("ValidRule(%q) accepted", bad)
		}
	}
}

func TestAgentProgram(t *testing.T) {
	plist := agentPlist("/Users/a & b/bin/mountkeeper", "/tmp/a.plist", "/tmp/log")
	if got := agentProgram(plist); got != "/Users/a & b/bin/mountkeeper" {
		t.Errorf("agentProgram = %q", got)
	}
	// As written by 1.0 and earlier.
	old := []byte(`<key>ProgramArguments</key>
	<array>
		<string>/opt/homebrew/bin/mountkeeper</string>
		<string>connect</string>
		<string>--auto</string>
	</array>`)
	if got := agentProgram(old); got != "/opt/homebrew/bin/mountkeeper" {
		t.Errorf("agentProgram of an old plist = %q", got)
	}
	if got := agentProgram([]byte("<plist/>")); got != "" {
		t.Errorf("agentProgram of an empty plist = %q", got)
	}
}

// TestAgentScript runs the agent's shell script the way launchd does, with a
// label that does not exist so that the final launchctl call does nothing.
func TestAgentScript(t *testing.T) {
	dir := t.TempDir()
	run := func(binary, plist string) string {
		out, _ := exec.Command("/bin/sh", "-c", agentScript, agentArg0, binary, plist,
			"com.example.mountkeeper.test-does-not-exist", "0").CombinedOutput()
		return string(out)
	}

	// The binary exists: it is run with "connect --auto", the plist stays.
	binary := filepath.Join(dir, "mountkeeper")
	os.WriteFile(binary, []byte("#!/bin/sh\necho \"ran $*\"\n"), 0o755)
	plist := filepath.Join(dir, "agent.plist")
	os.WriteFile(plist, []byte("plist"), 0o644)
	if out := run(binary, plist); !strings.Contains(out, "ran connect --auto") {
		t.Errorf("binary not run: %q", out)
	}
	if _, err := os.Stat(plist); err != nil {
		t.Error("the plist was removed although the binary exists")
	}

	// Uninstalled: the plist is removed.
	os.Remove(binary)
	if out := run(binary, plist); !strings.Contains(out, "no longer exists") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(plist); err == nil {
		t.Error("the plist survived the uninstall")
	}
}

// TestAgentPlistRoundTrip reads the plist back with macOS's own parser.
func TestAgentPlistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.plist")
	if err := os.WriteFile(path, agentPlist("/opt/homebrew/bin/mountkeeper", path, "/tmp/log"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		t.Fatalf("plutil rejects the plist: %v", err)
	}
	var v struct {
		ProgramArguments []string
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/sh", "-c", agentScript, agentArg0, "/opt/homebrew/bin/mountkeeper", path, AgentLabel, "60"}
	if !reflect.DeepEqual(v.ProgramArguments, want) {
		t.Errorf("ProgramArguments = %q", v.ProgramArguments)
	}
}
