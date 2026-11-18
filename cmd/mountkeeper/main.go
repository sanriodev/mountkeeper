// Command mountkeeper manages SMB network shares on macOS.
package main

import (
	"os"

	"github.com/sanriodev/mountkeeper/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
