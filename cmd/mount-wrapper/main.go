// Command mount-wrapper is the CLI and daemon entrypoint for the archive
// auto-mounter orchestrator.
package main

import (
	"os"

	"github.com/hilather/mount-wrapper/internal/cli"
)

// Set via -ldflags at release build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	}))
}
