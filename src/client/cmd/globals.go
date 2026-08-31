package cmd

import (
	"fmt"
	"strings"

	"github.com/webappsgo/cashp/src/config"
)

// Globals holds every flag that may appear before, between or after
// subcommand words. They are extracted first so command dispatch only ever
// sees positional arguments.
type Globals struct {
	Server     string
	Config     string
	Token      string
	TokenFile  string
	Output     string
	Color      string
	User       string
	Lang       string
	Limit      string
	Shell      string
	Update     string
	Admin      bool
	JSON       bool
	Debug      bool
	Quiet      bool
	Verbose    bool
	Yes        bool
	Help       bool
	Version    bool
	ShellSet   bool
	UpdateSet  bool
	NoColorSet bool
}

// valueFlags are global flags that consume a value, mapped to the field
// they populate.
var valueFlags = map[string]func(*Globals, string){
	"server":     func(g *Globals, v string) { g.Server = v },
	"config":     func(g *Globals, v string) { g.Config = v },
	"token":      func(g *Globals, v string) { g.Token = v },
	"token-file": func(g *Globals, v string) { g.TokenFile = v },
	"output":     func(g *Globals, v string) { g.Output = v },
	"format":     func(g *Globals, v string) { g.Output = v },
	"color":      func(g *Globals, v string) { g.Color = v },
	"user":       func(g *Globals, v string) { g.User = v },
	"lang":       func(g *Globals, v string) { g.Lang = v },
	"limit":      func(g *Globals, v string) { g.Limit = v },
}

// boolFlags are global flags that take no value.
var boolFlags = map[string]func(*Globals){
	"admin":    func(g *Globals) { g.Admin = true },
	"json":     func(g *Globals) { g.JSON = true; g.Output = "json" },
	"debug":    func(g *Globals) { g.Debug = true },
	"quiet":    func(g *Globals) { g.Quiet = true },
	"verbose":  func(g *Globals) { g.Verbose = true },
	"yes":      func(g *Globals) { g.Yes = true },
	"help":     func(g *Globals) { g.Help = true },
	"version":  func(g *Globals) { g.Version = true },
	"no-color": func(g *Globals) { g.Color = "never"; g.NoColorSet = true },
}

// shortFlags maps single-letter flags to their long form.
var shortFlags = map[string]string{
	"h": "help",
	"v": "version",
	"q": "quiet",
	"d": "debug",
	"o": "output",
	"u": "user",
	"y": "yes",
}

// optionalValueFlags may appear with or without a value; --shell alone
// prints shell help and --update alone checks for an update.
var optionalValueFlags = map[string]bool{
	"shell":  true,
	"update": true,
}

// UsageError is a flag or argument error that maps to exit code 64.
type UsageError struct {
	Message string
}

// Error implements the error interface.
func (e *UsageError) Error() string {
	return e.Message
}

// usagef builds a UsageError.
func usagef(format string, args ...any) *UsageError {
	return &UsageError{Message: fmt.Sprintf(format, args...)}
}

// ParseGlobals extracts global flags from argv, returning the remaining
// positional arguments in order. Both --flag=value and --flag value forms
// are accepted, and a bare "--" ends flag parsing.
func ParseGlobals(argv []string) (*Globals, []string, error) {
	globals := &Globals{}
	positional := make([]string, 0, len(argv))

	for index := 0; index < len(argv); index++ {
		arg := argv[index]

		if arg == "--" {
			positional = append(positional, argv[index+1:]...)
			break
		}
		if arg == "" || !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		name, inlineValue, hasInline := splitFlag(arg)
		if long, ok := shortFlags[name]; ok {
			name = long
		}

		switch {
		case optionalValueFlags[name]:
			value := inlineValue
			if !hasInline && index+1 < len(argv) && !strings.HasPrefix(argv[index+1], "-") {
				value = argv[index+1]
				index++
			}
			if name == "shell" {
				globals.Shell = value
				globals.ShellSet = true
				continue
			}
			globals.Update = value
			globals.UpdateSet = true
		case valueFlags[name] != nil:
			value := inlineValue
			if !hasInline {
				if index+1 >= len(argv) {
					return nil, nil, usagef("flag --%s requires a value", name)
				}
				index++
				value = argv[index]
			}
			valueFlags[name](globals, value)
		case boolFlags[name] != nil:
			if hasInline {
				enabled, err := config.ParseBool(inlineValue, true)
				if err != nil {
					return nil, nil, usagef("flag --%s expects true or false", name)
				}
				if !enabled {
					continue
				}
			}
			boolFlags[name](globals)
		default:
			return nil, nil, usagef("unknown flag: %s", arg)
		}
	}

	return globals, positional, nil
}

// splitFlag strips leading dashes and splits an inline =value.
func splitFlag(arg string) (name string, value string, hasValue bool) {
	trimmed := strings.TrimLeft(arg, "-")
	if index := strings.Index(trimmed, "="); index >= 0 {
		return trimmed[:index], trimmed[index+1:], true
	}
	return trimmed, "", false
}
