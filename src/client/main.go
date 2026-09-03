// Command cashp-cli is the CasHp command-line client. It talks to a CasHp
// panel over its HTTP API so every action available in the web panel is
// also available from a terminal. See AI.md PART 33 for the specification.
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/webappsgo/cashp/src/client/cmd"
)

// Build info variables injected at link time by ldflags.
var (
	Version    = "devel"
	CommitID   = "unknown"
	BuildEpoch = "0"
)

// buildDate renders BuildEpoch as an RFC 3339 timestamp, or "unknown" when
// no epoch was injected. BuildDate is never itself an ldflag (AI.md PART
// 28: BUILD_DATE is Docker OCI label-only) so it is always derived here.
func buildDate() string {
	epoch, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil || epoch <= 0 {
		return "unknown"
	}
	return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}

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
			BuildDate:  buildDate(),
		},
		BinaryName: binaryName,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}))
}
