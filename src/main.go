// Command cashp is the CasHp server binary — a self-hostable, all-in-one
// hosting control panel. See AI.md for the full specification and IDEA.md
// for the product definition.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/mode"
)

func main() {
	modeFlag := flag.String("mode", "", "application mode: production, development, debug")
	debugFlag := flag.Bool("debug", false, "enable debug logging and debug endpoints")
	debugSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "debug" {
			debugSet = true
		}
	})
	flag.Parse()

	m := mode.Resolve(*modeFlag)
	var debugPtr *bool
	if debugSet {
		debugPtr = debugFlag
	}
	debug := mode.ResolveDebug(debugPtr, m)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cashp: config error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("cashp starting: mode=%s debug=%t config=%s\n", m, debug, config.ConfigFilePath())
	_ = cfg
}
