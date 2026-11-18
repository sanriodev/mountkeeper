// Package macos wraps macOS system services Mountkeeper relies on besides
// mounting itself: launchd (automatic connecting) and Bonjour/smbutil
// (discovering servers and shares).
package macos

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AgentLabel identifies the per-user LaunchAgent that mounts auto-connect
// shares at login and re-checks them periodically.
const AgentLabel = "com.github.sanriodev.mountkeeper.autoconnect"

// AgentInterval is how often (in seconds) launchd re-runs the agent as a
// fallback. The agent is additionally started at login and whenever macOS
// rewrites its network configuration (joining a Wi-Fi, connecting a VPN,
// waking up on another network), which is what makes reconnecting prompt.
const AgentInterval = 300

// agentMarker is present in every plist written since the agent removes
// itself after an uninstall; older installations lack it and are replaced.
const agentMarker = "<string>" + agentArg0 + "</string>"

// agentArg0 is the name the agent's shell runs under ($0).
const agentArg0 = "mountkeeper-agent"

// AgentGrace is how long the agent waits, when the mountkeeper binary is
// missing, before it concludes that Mountkeeper was uninstalled. During
// "brew upgrade" the link is briefly missing; that must not remove it.
const AgentGrace = 60

// agentScript is what launchd runs: mountkeeper itself while it exists.
// Homebrew formulae cannot run anything on uninstall, so the agent cleans up
// after itself instead: once the binary has been gone for AgentGrace
// seconds, it deletes its plist and unloads itself.
//
// Everything variable arrives as an argument ($1 binary, $2 plist, $3 label,
// $4 grace in seconds); nothing is ever pasted into the script.
const agentScript = `if [ ! -x "$1" ]; then /bin/sleep "$4"; fi
if [ -x "$1" ]; then exec "$1" connect --auto; fi
echo "$(/bin/date '+%Y-%m-%d %H:%M:%S') $1 no longer exists (Mountkeeper was uninstalled); removing the background agent"
/bin/rm -f "$2"
exec /bin/launchctl bootout "gui/$(/usr/bin/id -u)/$3"`

// AgentOutdated reports whether an installed agent predates this version.
func AgentOutdated() bool {
	path, err := AgentPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && !bytes.Contains(data, []byte(agentMarker))
}

// AgentPath returns ~/Library/LaunchAgents/<label>.plist.
func AgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", AgentLabel+".plist"), nil
}

// AgentLogPath returns ~/Library/Logs/mountkeeper.log, where launchd writes
// the agent's output.
func AgentLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "mountkeeper.log"), nil
}

// AgentLogMax is the size at which the agent log is cut down to its newer half.
const AgentLogMax = 256 << 10

// AgentInstalled reports whether the LaunchAgent file exists.
func AgentInstalled() bool {
	p, err := AgentPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// InstallAgent writes the LaunchAgent and loads it into the user's launchd
// session. executable is the mountkeeper binary to run.
func InstallAgent(executable string) error {
	path, err := AgentPath()
	if err != nil {
		return err
	}
	logFile, err := AgentLogPath()
	if err != nil {
		return err
	}
	plist := agentPlist(executable, path, logFile)

	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, plist) && agentLoaded() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, plist, 0o644); err != nil {
		return err
	}
	// Replace a previously loaded copy; bootout fails harmlessly if none is loaded.
	exec.Command("/bin/launchctl", "bootout", agentTarget()).Run()
	if out, err := exec.Command("/bin/launchctl", "bootstrap", guiDomain(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("loading LaunchAgent: %s", firstLine(out, err))
	}
	return nil
}

// UninstallAgent unloads and deletes the LaunchAgent. It is a no-op if the
// agent is not installed.
func UninstallAgent() error {
	path, err := AgentPath()
	if err != nil {
		return err
	}
	exec.Command("/bin/launchctl", "bootout", agentTarget()).Run()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// AgentInfo describes the LaunchAgent as it is installed.
type AgentInfo struct {
	Installed bool
	Loaded    bool   // known to launchd in the user's session
	Program   string // the mountkeeper binary it runs
	// Current: the plist is exactly what this version writes for Program;
	// false means an older version wrote it.
	Current bool
}

// InspectAgent reads the installed LaunchAgent.
func InspectAgent() AgentInfo {
	var info AgentInfo
	path, err := AgentPath()
	if err != nil {
		return info
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return info
	}
	info.Installed = true
	info.Loaded = agentLoaded()
	info.Program = agentProgram(data)
	if log, err := AgentLogPath(); err == nil && info.Program != "" {
		info.Current = bytes.Equal(data, agentPlist(info.Program, path, log))
	}
	return info
}

// RefreshAgent rewrites an installed agent in the current format, keeping the
// binary it runs (which may be another installation than the one calling).
func RefreshAgent() error {
	info := InspectAgent()
	if !info.Installed || info.Program == "" {
		return nil
	}
	return InstallAgent(info.Program)
}

var (
	programArguments = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>(.*?)</array>`)
	plistString      = regexp.MustCompile(`<string>([^<]*)</string>`)
)

// agentProgram extracts the mountkeeper binary from a plist written by any
// version: the first argument, or the one after the script's name.
func agentProgram(plist []byte) string {
	m := programArguments.FindSubmatch(plist)
	if m == nil {
		return ""
	}
	var args []string
	for _, a := range plistString.FindAllSubmatch(m[1], -1) {
		args = append(args, unescapeXML(string(a[1])))
	}
	for i, a := range args {
		if a == agentArg0 && i+1 < len(args) {
			return args[i+1]
		}
	}
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func unescapeXML(s string) string {
	var v struct {
		Text string `xml:",chardata"`
	}
	if xml.Unmarshal([]byte("<s>"+s+"</s>"), &v) != nil {
		return s
	}
	return v.Text
}

func agentLoaded() bool {
	return exec.Command("/bin/launchctl", "print", agentTarget()).Run() == nil
}

func guiDomain() string   { return "gui/" + strconv.Itoa(os.Getuid()) }
func agentTarget() string { return guiDomain() + "/" + AgentLabel }

func agentPlist(executable, plistPath, logFile string) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + AgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string>
		<string>-c</string>
		<string>` + esc(agentScript) + `</string>
		<string>` + agentArg0 + `</string>
		<string>` + esc(executable) + `</string>
		<string>` + esc(plistPath) + `</string>
		<string>` + AgentLabel + `</string>
		<string>` + strconv.Itoa(AgentGrace) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>WatchPaths</key>
	<array>
		<string>/var/run/resolv.conf</string>
		<string>/Library/Preferences/SystemConfiguration</string>
	</array>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(AgentInterval) + `</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>` + esc(logFile) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(logFile) + `</string>
</dict>
</plist>
`)
}

func firstLine(out []byte, err error) string {
	if s := strings.TrimSpace(string(out)); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	return err.Error()
}
