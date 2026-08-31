package main

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUsage marks a command line the agent could not parse. It is reported
// with exit code 64 so scripts can tell a bad invocation from a failure.
var ErrUsage = errors.New("usage")

// Commands the agent accepts as a positional argument.
const (
	CommandStatus   = "status"
	CommandTest     = "test"
	CommandRegister = "register"
)

// ServiceCommands are the verbs accepted by --service. They are also
// accepted in the documented --service --install form.
var ServiceCommands = []string{"install", "uninstall", "start", "stop", "restart", "status"}

// UpdateModes are the optional arguments accepted by --update.
var UpdateModes = []string{"check", "yes"}

// Options is the fully parsed command line.
type Options struct {
	// Help and Version are the informational flags.
	Help    bool
	Version bool

	// ShellSet reports that --shell was given; ShellAction and ShellName
	// carry its arguments, both of which are optional.
	ShellSet    bool
	ShellAction string
	ShellName   string

	// Directory overrides.
	ConfigDir string
	DataDir   string
	LogDir    string

	// Connection settings.
	Server string
	Token  string
	Org    string

	// Runtime settings.
	Mode  string
	Debug bool
	Color string
	Lang  string

	// Status requests the health summary and exits.
	Status bool

	// Service is the --service verb, empty when the flag was not given.
	Service string

	// UpdateSet reports that --update was given; UpdateMode is its optional
	// argument, defaulting to check.
	UpdateSet  bool
	UpdateMode string

	// Command is the positional subcommand, empty for a foreground run.
	Command string

	// Force is the --force modifier accepted by the register command.
	Force bool
}

// ParseArgs turns a raw argument list into Options. It is hand written
// rather than built on the stdlib flag package because two documented
// flags take an optional argument (--shell, --update), which the flag
// package cannot express.
func ParseArgs(args []string) (*Options, error) {
	opts := &Options{}

	index := 0
	for index < len(args) {
		arg := args[index]
		index++

		if arg == "--" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			if err := opts.setCommand(arg); err != nil {
				return nil, err
			}
			continue
		}

		name, inline, hasInline := splitFlag(arg)
		next := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if index >= len(args) {
				return "", fmt.Errorf("%w: %s needs a value", ErrUsage, name)
			}
			value := args[index]
			index++
			return value, nil
		}

		var err error
		switch name {
		case "-h", "--help":
			opts.Help = true
		case "-v", "--version":
			opts.Version = true
		case "--debug":
			opts.Debug = true
		case "--status":
			opts.Status = true
		case "--force":
			opts.Force = true
		case "--shell":
			opts.ShellSet = true
			if hasInline {
				opts.ShellAction = inline
				break
			}
			index = readShell(opts, args, index)
		case "--update":
			opts.UpdateSet = true
			opts.UpdateMode = UpdateModes[0]
			if hasInline {
				if !isOneOf(inline, UpdateModes) {
					return nil, fmt.Errorf("%w: unknown --update argument: %s", ErrUsage, inline)
				}
				opts.UpdateMode = inline
				break
			}
			if index < len(args) && isOneOf(args[index], UpdateModes) {
				opts.UpdateMode = args[index]
				index++
			}
		case "--service":
			value := ""
			if hasInline {
				value = inline
			} else if index < len(args) {
				value = strings.TrimPrefix(args[index], "--")
				if !isOneOf(value, ServiceCommands) {
					return nil, fmt.Errorf("%w: --service needs one of: %s", ErrUsage, strings.Join(ServiceCommands, ", "))
				}
				index++
			}
			if !isOneOf(value, ServiceCommands) {
				return nil, fmt.Errorf("%w: --service needs one of: %s", ErrUsage, strings.Join(ServiceCommands, ", "))
			}
			opts.Service = value
		case "--config":
			opts.ConfigDir, err = next()
		case "--data":
			opts.DataDir, err = next()
		case "--log":
			opts.LogDir, err = next()
		case "--server":
			opts.Server, err = next()
		case "--token":
			opts.Token, err = next()
		case "--org":
			opts.Org, err = next()
		case "--mode":
			opts.Mode, err = next()
		case "--color":
			opts.Color, err = next()
		case "--lang":
			opts.Lang, err = next()
		default:
			return nil, fmt.Errorf("%w: unknown flag: %s", ErrUsage, name)
		}
		if err != nil {
			return nil, err
		}
	}

	if err := opts.validate(); err != nil {
		return nil, err
	}
	return opts, nil
}

// readShell consumes the optional action and shell name that follow
// --shell, returning the new argument index.
func readShell(opts *Options, args []string, index int) int {
	if index < len(args) && !strings.HasPrefix(args[index], "-") {
		opts.ShellAction = args[index]
		index++
	}
	if index < len(args) && !strings.HasPrefix(args[index], "-") {
		opts.ShellName = args[index]
		index++
	}
	return index
}

// setCommand records the positional subcommand, rejecting a second one.
func (o *Options) setCommand(value string) error {
	if o.Command != "" {
		return fmt.Errorf("%w: unexpected argument: %s", ErrUsage, value)
	}
	switch value {
	case CommandStatus, CommandTest, CommandRegister:
		o.Command = value
		return nil
	default:
		return fmt.Errorf("%w: unknown command: %s", ErrUsage, value)
	}
}

// validate rejects flag values the rest of the program would otherwise
// have to re-check.
func (o *Options) validate() error {
	switch strings.ToLower(strings.TrimSpace(o.Mode)) {
	case "", "production", "development", "debug":
	default:
		return fmt.Errorf("%w: --mode must be production, development or debug", ErrUsage)
	}

	switch strings.ToLower(strings.TrimSpace(o.Color)) {
	case "", "auto", "yes", "no", "always", "never", "true", "false":
	default:
		return fmt.Errorf("%w: --color must be auto, yes or no", ErrUsage)
	}
	return nil
}

// splitFlag separates --name=value into its parts.
func splitFlag(arg string) (name, value string, hasValue bool) {
	if index := strings.Index(arg, "="); index > 0 {
		return arg[:index], arg[index+1:], true
	}
	return arg, "", false
}

// isOneOf reports whether value appears in allowed.
func isOneOf(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}
