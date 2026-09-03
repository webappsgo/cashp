// Command cashp is the CasHp server binary — a self-hostable, all-in-one
// hosting control panel. See AI.md for the full specification and IDEA.md
// for the product definition.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/mode"
	"github.com/webappsgo/cashp/src/notifysvc"
	"github.com/webappsgo/cashp/src/scheduler"
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

	if err := run(cfg); err != nil {
		msg := "cashp: " + err.Error()
		if useColor {
			msg = "\033[1;31m" + msg + "\033[0m"
		}
		fmt.Fprintf(os.Stderr, "%s\n", msg)
		os.Exit(1)
	}
}

// run wires the subsystems that are safe to construct without a running
// HTTP server: the database, the notification package, and the built-in
// scheduler. It blocks until SIGINT/SIGTERM, then shuts everything down in
// reverse order. Route mounting and TLS are out of scope here — this is the
// composition root's foundation, not the whole server.
func run(cfg *config.Config) error {
	dbDir := cfg.Server.Database.Dir
	if dbDir == "" {
		dbDir = config.DataDir()
	}
	driver := cfg.Server.Database.Driver
	if driver == "" {
		driver = database.DriverSQLite
	}
	db, err := database.Open(database.Config{
		Driver: driver,
		URL:    cfg.Server.Database.URL,
		Dir:    dbDir,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close() //nolint:errcheck // best-effort close during shutdown

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := db.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}

	notifier, err := notifysvc.New(cfg, db)
	if err != nil {
		return fmt.Errorf("construct notifier: %w", err)
	}
	notifysvc.DetectSMTP(ctx, notifier)

	sched := scheduler.New(scheduler.Options{
		StateDir: config.DataDir(),
		LogDir:   config.LogDir(),
	})
	if err := notifysvc.RegisterScheduler(sched, notifier); err != nil {
		return fmt.Errorf("register scheduler tasks: %w", err)
	}
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	if err := notifysvc.NotifyStartup(ctx, notifier); err != nil {
		fmt.Fprintf(os.Stderr, "cashp: startup notification: %v\n", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := notifysvc.NotifyShutdown(shutdownCtx, notifier); err != nil {
		fmt.Fprintf(os.Stderr, "cashp: shutdown notification: %v\n", err)
	}
	if err := sched.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "cashp: scheduler stop: %v\n", err)
	}
	return nil
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
		fmt.Printf("cashp %s (commit %s, built %s)\n", Version, CommitID, buildDate())
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
