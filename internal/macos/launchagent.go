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
	"strconv"
	"strings"
)

// AgentLabel identifies the per-user LaunchAgent that mounts auto-connect
// shares at login and re-checks them periodically.
const AgentLabel = "com.github.sanriodev.mountkeeper.autoconnect"

// AgentInterval is how often (in seconds) launchd re-runs the agent, which
// re-establishes shares after sleep or a network change.
const AgentInterval = 300

// AgentPath returns ~/Library/LaunchAgents/<label>.plist.
func AgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", AgentLabel+".plist"), nil
}

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
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	logFile := filepath.Join(home, "Library", "Logs", "mountkeeper.log")
	plist := agentPlist(executable, logFile)

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

func agentLoaded() bool {
	return exec.Command("/bin/launchctl", "print", agentTarget()).Run() == nil
}

func guiDomain() string   { return "gui/" + strconv.Itoa(os.Getuid()) }
func agentTarget() string { return guiDomain() + "/" + AgentLabel }

func agentPlist(executable, logFile string) []byte {
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
		<string>` + esc(executable) + `</string>
		<string>connect</string>
		<string>--auto</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
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
