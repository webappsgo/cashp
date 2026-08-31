// Command cashp-cli is the CasHp command-line client. It talks to a CasHp
// panel over its HTTP API so every action available in the web panel is
// also available from a terminal. See AI.md PART 33 for the specification.
package main

import (
	"os"
	"path/filepath"

	"github.com/webappsgo/cashp/src/client/cmd"
)

// Build info variables injected at link time by ldflags.
var (
	Version    = "devel"
	CommitID   = "unknown"
	BuildEpoch = "0"
	BuildDate  = "unknown"
)

func main() {
	binaryName := "cashp-cli"
	if len(os.Args) > 0 && os.Args[0] != "" {
		binaryName = filepath.Base(os.Args[0])
	}

	os.Exit(cmd.Execute(cmd.ExecuteOptions{
		Argv: os.Args[1:],
		Build: cmd.BuildInfo{
			Version:    Version,
			CommitID:   CommitID,
			BuildEpoch: BuildEpoch,
			BuildDate:  BuildDate,
		},
		BinaryName: binaryName,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}))
}
