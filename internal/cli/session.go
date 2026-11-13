package cli

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
)

// Mountkeeper works on behalf of the person sitting at the Mac: the mount
// table entry, the Keychain and Finder all belong to the user logged in at
// the console. Running it as somebody else — in a `su` shell of an admin
// account, or with sudo — still executes, but the Keychain of that other
// account is not available and the mounted share is not usable in Finder.

// consoleUID returns the uid of the user logged in at the console; ok is
// false when nobody is (login window) or it cannot be determined.
func consoleUID() (uid int, ok bool) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return 0, false
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat || st.Uid == 0 {
		return 0, false
	}
	return int(st.Uid), true
}

func userName(uid int) string {
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return "uid " + strconv.Itoa(uid)
}

// sessionMismatch explains the problem when the process runs as a different
// user than the one logged in at the console; it returns "" when all is well.
func sessionMismatch(uid, console int, consoleKnown bool) string {
	if !consoleKnown || uid == console {
		return ""
	}
	return fmt.Sprintf("You are running Mountkeeper as %s, but %s is logged in at this Mac.\n"+
		"  Shares, the Keychain and Finder belong to the logged-in user:\n"+
		"  leave the su/sudo shell and run it as %s.",
		userName(uid), userName(console), userName(console))
}

func currentSessionMismatch() string {
	console, ok := consoleUID()
	return sessionMismatch(os.Geteuid(), console, ok)
}

// keychainUnavailableHint is appended to errors caused by a missing Keychain.
func keychainUnavailableHint() string {
	if msg := currentSessionMismatch(); msg != "" {
		return msg
	}
	return "The login Keychain of " + userName(os.Geteuid()) + " cannot be used here (for example in an SSH session).\n" +
		"  Run Mountkeeper in a terminal on the Mac itself."
}

// warnSessionMismatch is the root command's pre-run hook.
func warnSessionMismatch(cmd *cobra.Command, args []string) {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return
		}
	}
	if msg := currentSessionMismatch(); msg != "" {
		warn("%s", msg)
	}
}
