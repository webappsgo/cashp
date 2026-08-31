package guard

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/webappsgo/cashp/src/security"
)

// shellBinaries are interpreters that turn a single argument back into a
// command line. Registering one would undo the whole point of an argv-only
// helper, so the registry refuses them at construction time rather than at
// call time.
var shellBinaries = map[string]struct{}{
	"ash":     {},
	"awk":     {},
	"bash":    {},
	"busybox": {},
	"csh":     {},
	"dash":    {},
	"env":     {},
	"eval":    {},
	"expect":  {},
	"fish":    {},
	"gawk":    {},
	"ksh":     {},
	"lua":     {},
	"mawk":    {},
	"nc":      {},
	"ncat":    {},
	"node":    {},
	"perl":    {},
	"php":     {},
	"python":  {},
	"python2": {},
	"python3": {},
	"ruby":    {},
	"sh":      {},
	"socat":   {},
	"ssh":     {},
	"tclsh":   {},
	"xargs":   {},
	"zsh":     {},
}

// interpreterFlags are argv elements that make even a non-shell binary
// evaluate a string as code. They are refused in every argument position.
var interpreterFlags = map[string]struct{}{
	"-c":        {},
	"--command": {},
	"-e":        {},
	"--eval":    {},
	"--exec":    {},
}

// ExecPolicy is the closed registry of binaries cashp may run and the
// flags it may pass. There is no fallback to a PATH lookup and no escape
// hatch: a name the registry does not hold cannot be executed through this
// package at all.
type ExecPolicy struct {
	// Binaries maps a logical name to the absolute path that will be executed.
	Binaries map[string]string
	// AllowedFlags is the exact set of option arguments that may be passed; a "--flag=value" argument matches the "--flag" entry.
	AllowedFlags map[string]struct{}
	// Dir is the working directory for every command built from this policy; an empty value inherits the process directory.
	Dir string
	// Env is the fixed environment for every command; a nil map runs the child with an empty environment.
	Env map[string]string
}

// NewExecPolicy builds a registry from a logical-name-to-path map and a
// flag allowlist, rejecting any registration that would reintroduce shell
// interpretation or a relative binary path.
func NewExecPolicy(binaries map[string]string, allowedFlags []string) (ExecPolicy, error) {
	policy := ExecPolicy{
		Binaries:     make(map[string]string, len(binaries)),
		AllowedFlags: make(map[string]struct{}, len(allowedFlags)),
	}
	for name, path := range binaries {
		if err := ValidateIdentifier("binary name", name); err != nil {
			return ExecPolicy{}, err
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ExecPolicy{}, Deny(ReasonInvalidInput, "binary path "+path+" is not a clean absolute path")
		}
		if _, isShell := shellBinaries[strings.ToLower(filepath.Base(path))]; isShell {
			return ExecPolicy{}, Deny(ReasonInvalidInput, "binary path "+path+" is an interpreter")
		}
		policy.Binaries[name] = path
	}
	for _, flag := range allowedFlags {
		if !strings.HasPrefix(flag, "-") {
			return ExecPolicy{}, Deny(ReasonInvalidInput, "allowed flag "+flag+" does not start with a hyphen")
		}
		if strings.ContainsAny(flag, "= ") {
			return ExecPolicy{}, Deny(ReasonInvalidInput, "allowed flag "+flag+" must be the bare flag, without a value")
		}
		if _, isInterp := interpreterFlags[flag]; isInterp {
			return ExecPolicy{}, Deny(ReasonInvalidInput, "allowed flag "+flag+" evaluates code")
		}
		policy.AllowedFlags[flag] = struct{}{}
	}
	return policy, nil
}

// Command is a validated, argv-only invocation. It holds a resolved
// absolute path and a fully checked argument vector; there is no field
// that can carry a command line, so shell interpretation is structurally
// impossible rather than merely avoided by convention.
type Command struct {
	path string
	args []string
	env  []string
	dir  string
}

// NewCommand validates a logical binary name and its arguments against the
// policy and returns a runnable Command. Arguments reach the kernel as
// separate argv elements, so shell metacharacters inside an argument body
// are inert; what is rejected is anything that could split an argument or
// be read as an unintended option.
func NewCommand(policy ExecPolicy, name string, args ...string) (*Command, error) {
	path, ok := policy.Binaries[name]
	if !ok {
		return nil, Deny(ReasonInvalidInput, "binary "+name+" is not registered")
	}

	checked := make([]string, 0, len(args))
	for _, arg := range args {
		if err := ValidateExecArg(arg); err != nil {
			return nil, err
		}
		if _, isInterp := interpreterFlags[arg]; isInterp {
			return nil, Deny(ReasonInvalidInput, "argument "+arg+" evaluates code")
		}
		if strings.HasPrefix(arg, "-") {
			bare := arg
			if idx := strings.IndexByte(arg, '='); idx > 0 {
				bare = arg[:idx]
			}
			if _, allowed := policy.AllowedFlags[bare]; !allowed {
				return nil, Deny(ReasonInvalidInput, "flag "+bare+" is not in the allowed set")
			}
		}
		checked = append(checked, arg)
	}

	env, err := ValidateEnvVars(policy.Env)
	if err != nil {
		return nil, err
	}
	if policy.Dir != "" && (!filepath.IsAbs(policy.Dir) || filepath.Clean(policy.Dir) != policy.Dir) {
		return nil, Deny(ReasonInvalidInput, "working directory "+policy.Dir+" is not a clean absolute path")
	}

	return &Command{path: path, args: checked, env: env, dir: policy.Dir}, nil
}

// Path returns the absolute binary path the command will execute.
func (c *Command) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// Args returns a copy of the validated argument vector, excluding argv[0].
func (c *Command) Args() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.args...)
}

// Env returns a copy of the fixed child environment as KEY=VALUE entries.
func (c *Command) Env() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.env...)
}

// Cmd builds the standard-library command. It assigns Path directly after
// construction so no PATH lookup can change which binary runs: the process
// executed is exactly the absolute path the policy registered.
func (c *Command) Cmd(ctx context.Context) *exec.Cmd {
	if c == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, c.path, c.args...)
	cmd.Path = c.path
	cmd.Env = c.Env()
	cmd.Dir = c.dir
	return cmd
}

// String renders the invocation for a log line with sensitive argument
// values masked. A "--token=abc" argument logs as "--token=xxxxx", and the
// value following a sensitive flag is masked too, so an audit line can
// never carry a credential that was passed on an argv.
func (c *Command) String() string {
	if c == nil {
		return ""
	}
	parts := make([]string, 0, len(c.args)+1)
	parts = append(parts, c.path)
	maskNext := false
	for _, arg := range c.args {
		switch {
		case maskNext:
			parts = append(parts, security.MaskedValue)
			maskNext = false
		case strings.HasPrefix(arg, "-") && strings.Contains(arg, "="):
			name := arg[:strings.IndexByte(arg, '=')]
			if security.IsSensitiveName(name) {
				parts = append(parts, name+"="+security.MaskedValue)
				continue
			}
			parts = append(parts, arg)
		case strings.HasPrefix(arg, "-") && security.IsSensitiveName(arg):
			parts = append(parts, arg)
			maskNext = true
		default:
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}
