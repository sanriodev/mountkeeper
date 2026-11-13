// Package finder opens paths in the macOS Finder.
package finder

import (
	"fmt"
	"os/exec"
	"strings"
)

// Open shows the directory at path in a Finder window.
func Open(path string) error {
	return run("/usr/bin/open", path)
}

// Reveal selects path in its parent folder's Finder window.
func Reveal(path string) error {
	return run("/usr/bin/open", "-R", path)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("opening Finder: %s", msg)
		}
		return fmt.Errorf("opening Finder: %w", err)
	}
	return nil
}
