package macos

import (
	"encoding/xml"
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
	p := agentPlist("/opt/home brew/bin/mount<keeper>&", "/Users/x/Library/Logs/mountkeeper.log")
	if err := xml.Unmarshal(p, new(any)); err != nil {
		t.Fatalf("plist is not well-formed XML: %v", err)
	}
	s := string(p)
	for _, want := range []string{AgentLabel, "mount&lt;keeper&gt;&amp;", "<string>--auto</string>", "<key>RunAtLoad</key>"} {
		if !strings.Contains(s, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
}
