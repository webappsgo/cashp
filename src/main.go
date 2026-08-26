// Command cashp is the CasHp server binary — a self-hostable, all-in-one
// hosting control panel. See AI.md for the full specification and IDEA.md
// for the product definition.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/mode"
)

// Build info variables injected at link time by ldflags.
var (
	Version    = "devel"
	CommitID   = "unknown"
	BuildEpoch = "0"
	BuildDate  = "unknown"
)

func main() {
	modeStr, debugPtr, colorMode := parseFlags(os.Args[1:])

	m := mode.Resolve(modeStr)
	debug := mode.ResolveDebug(debugPtr, m)
	useColor := resolveColor(colorMode)

	cfg, err := config.Load()
	if err != nil {
		msg := "cashp: config error: " + err.Error()
		if useColor {
			msg = "\033[1;31m" + msg + "\033[0m"
		}
		fmt.Fprintf(os.Stderr, "%s\n", msg)
		os.Exit(1)
	}

	startMsg := fmt.Sprintf("cashp starting: mode=%s debug=%t config=%s", m, debug, config.ConfigFilePath())
	if useColor {
		startMsg = "\033[1;32m" + startMsg + "\033[0m"
	}
	fmt.Println(startMsg)
	_ = cfg
}

// parseFlags parses args and returns the raw --mode value (empty when not
// passed), a --debug pointer that is nil when the flag was not passed, and
// a --color value (auto/yes/no or empty when not passed). Distinguishing
// "not set" from "explicitly false" is required by mode.ResolveDebug's
// flag > env > mode-implied priority chain.
func parseFlags(args []string) (modeStr string, debugPtr *bool, colorMode string) {
	fs := flag.NewFlagSet("cashp", flag.ExitOnError)
	versionFlag := fs.Bool("version", false, "print version and exit")
	versionShortFlag := fs.Bool("v", false, "print version and exit (shorthand for --version)")
	modeFlag := fs.String("mode", "", "application mode: production, development, debug")
	debugFlag := fs.Bool("debug", false, "enable debug logging and debug endpoints")
	colorFlag := fs.String("color", "", "color output: auto (default), yes, no")

	fs.Parse(args) //nolint:errcheck // ExitOnError already handles parse failures

	if *versionFlag || *versionShortFlag {
		fmt.Printf("cashp %s (commit %s, built %s)\n", Version, CommitID, BuildDate)
		os.Exit(0)
	}

	debugSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "debug" {
			debugSet = true
		}
	})

	if debugSet {
		debugPtr = debugFlag
	}
	return *modeFlag, debugPtr, *colorFlag
}

// resolveColor determines whether to use color output based on the --color
// flag, COLOR env var, and NO_COLOR env var. Priority: flag > NO_COLOR >
// COLOR env var > default (auto). When auto, detect TTY.
func resolveColor(flagColor string) bool {
	if flagColor != "" {
		switch strings.ToLower(flagColor) {
		case "yes", "true", "1":
			return true
		case "no", "false", "0":
			return false
		case "auto":
			return isTTY(os.Stdout.Fd())
		}
	}

	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	if envColor := os.Getenv("COLOR"); envColor != "" {
		return strings.EqualFold(envColor, "yes") || envColor == "1"
	}

	return isTTY(os.Stdout.Fd())
}

// isTTY reports whether fd is a terminal.
func isTTY(fd uintptr) bool {
	file := os.NewFile(fd, "")
	if file == nil {
		return false
	}
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
