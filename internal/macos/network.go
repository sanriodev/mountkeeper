package macos

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Network describes the network the Mac is currently on, using only
// information that needs no special permission. The Wi-Fi name is included
// when macOS reveals it (recent versions hide it from command-line tools
// unless Location Services are granted), so rules should not rely on it.
type Network struct {
	Interface string   // interface of the default route, e.g. "en0"
	RouterIP  string   // default gateway
	RouterMAC string   // its hardware address, lower case, e.g. "a0:b1:c2:d3:e4:f5"
	Domains   []string // DNS search domains, lower case, e.g. "corp.example.com"
	SSID      string   // Wi-Fi name, if available
}

// Rule kinds understood by Matches.
const (
	RuleDomain = "domain"
	RuleRouter = "router"
	RuleSSID   = "ssid"
)

// Online reports whether there is a default route at all.
func (n Network) Online() bool { return n.Interface != "" }

// Rules lists the rules that match this network, most useful first: a DNS
// domain also holds across office VLANs and VPN, a router only on one subnet.
func (n Network) Rules() []string {
	var rules []string
	for _, d := range n.Domains {
		rules = append(rules, RuleDomain+":"+d)
	}
	if n.SSID != "" {
		rules = append(rules, RuleSSID+":"+n.SSID)
	}
	if n.RouterMAC != "" {
		rules = append(rules, RuleRouter+":"+n.RouterMAC)
	}
	return rules
}

// Matches reports whether a rule such as "domain:corp.example.com",
// "router:a0:b1:c2:d3:e4:f5" or "ssid:Office" applies to this network.
func (n Network) Matches(rule string) bool {
	kind, value, ok := strings.Cut(rule, ":")
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case RuleDomain:
		for _, d := range n.Domains {
			if strings.EqualFold(d, value) {
				return true
			}
		}
	case RuleRouter:
		return n.RouterMAC != "" && normalizeMAC(value) == n.RouterMAC
	case RuleSSID:
		return n.SSID != "" && n.SSID == value
	}
	return false
}

// MatchesAny reports whether at least one rule applies.
func (n Network) MatchesAny(rules []string) bool {
	for _, r := range rules {
		if n.Matches(r) {
			return true
		}
	}
	return false
}

// ValidRule checks the syntax of a rule and returns it in canonical form.
func ValidRule(rule string) (string, bool) {
	kind, value, ok := strings.Cut(rule, ":")
	kind, value = strings.ToLower(strings.TrimSpace(kind)), strings.TrimSpace(value)
	if !ok || value == "" {
		return "", false
	}
	switch kind {
	case RuleDomain:
		return kind + ":" + strings.ToLower(strings.TrimSuffix(value, ".")), true
	case RuleRouter:
		mac := normalizeMAC(value)
		if !macPattern.MatchString(mac) {
			return "", false
		}
		return kind + ":" + mac, true
	case RuleSSID:
		return kind + ":" + value, true
	}
	return "", false
}

// DescribeRule turns a rule into words.
func DescribeRule(rule string) string {
	kind, value, _ := strings.Cut(rule, ":")
	switch kind {
	case RuleDomain:
		return "network domain " + value
	case RuleRouter:
		return "router " + value
	case RuleSSID:
		return "Wi-Fi " + value
	}
	return rule
}

// CurrentNetwork inspects the default route, the router's hardware address
// and the DNS configuration. Every lookup is bounded; missing pieces are
// simply left empty.
func CurrentNetwork() Network {
	var n Network
	if out, err := quick("/sbin/route", "-n", "get", "default"); err == nil {
		n.Interface, n.RouterIP = parseRoute(out)
	}
	if n.RouterIP != "" {
		if out, err := quick("/usr/sbin/arp", "-n", n.RouterIP); err == nil {
			n.RouterMAC = parseARP(out)
		}
	}
	if out, err := quick("/usr/sbin/scutil", "--dns"); err == nil {
		n.Domains = parseDNS(out)
	}
	if n.Interface != "" {
		if out, err := quick("/usr/sbin/ipconfig", "getsummary", n.Interface); err == nil {
			n.SSID = parseSSID(out)
		}
	}
	return n
}

func quick(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func parseRoute(out []byte) (iface, gateway string) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "interface":
			iface = strings.TrimSpace(value)
		case "gateway":
			gateway = strings.TrimSpace(value)
		}
	}
	return iface, gateway
}

var (
	arpPattern = regexp.MustCompile(`\bat\s+([0-9a-fA-F]{1,2}(?::[0-9a-fA-F]{1,2}){5})\b`)
	macPattern = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
)

func parseARP(out []byte) string {
	if m := arpPattern.FindSubmatch(out); m != nil {
		return normalizeMAC(string(m[1]))
	}
	return ""
}

// normalizeMAC lower-cases a hardware address and pads each byte to two
// digits (arp prints "a:b:c:1:2:3").
func normalizeMAC(mac string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(strings.ReplaceAll(mac, "-", ":"))), ":")
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return strings.Join(parts, ":")
}

var dnsDomain = regexp.MustCompile(`^\s*(?:search domain\[\d+\]|domain)\s*:\s*(\S+)\s*$`)

// parseDNS collects the search domains of all resolvers. Purely local
// pseudo-domains are ignored.
func parseDNS(out []byte) []string {
	seen := map[string]bool{}
	var domains []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := dnsDomain.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		d := strings.ToLower(strings.TrimSuffix(m[1], "."))
		if d == "" || d == "local" || strings.HasSuffix(d, ".arpa") || !strings.Contains(d, ".") || seen[d] {
			continue
		}
		seen[d] = true
		domains = append(domains, d)
	}
	sort.Strings(domains)
	return domains
}

var ssidLine = regexp.MustCompile(`(?m)^\s*SSID\s*:\s*(.+?)\s*$`)

func parseSSID(out []byte) string {
	m := ssidLine.FindSubmatch(out)
	if m == nil {
		return ""
	}
	ssid := string(m[1])
	if strings.Contains(ssid, "redacted") {
		return ""
	}
	return ssid
}
