package macos

import (
	"context"
	"os/exec"
	"time"
)

// notifyScript shows a notification with the title and text it receives as
// arguments. They are never pasted into the script, so no text can change
// what it does.
const notifyScript = `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`

// Notify shows a macOS notification. It is best effort: errors are ignored,
// because a missing notification must never break connecting.
func Notify(title, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "/usr/bin/osascript", "-e", notifyScript, title, text).Run()
}
