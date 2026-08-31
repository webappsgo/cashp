package dbservice

import (
	"io"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/security"
)

// This file holds the primitives every engine adapter builds its commands
// from. Two rules are enforced here rather than trusted to each adapter:
// nothing secret ever reaches an argv slice, and every generated secret is
// restricted to an alphabet that cannot terminate a quoted SQL literal.

// passwordAlphabet is the character set generated credentials draw from. It
// is deliberately alphanumeric only: no quote, backslash, semicolon,
// whitespace or shell metacharacter can appear in a generated password, so a
// password embedded in an engine statement can never end its own literal.
const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// passwordLen is the length of every generated credential. At 32 characters
// from a 62 symbol alphabet this is well past 128 bits of entropy.
const passwordLen = 32

// generatePassword returns a fresh alphanumeric secret drawn from the
// system CSPRNG. Bytes outside the unbiased range are discarded rather than
// folded with a modulus, so every character of the alphabet is equally
// likely.
func generatePassword(n int) (string, error) {
	if n <= 0 {
		n = passwordLen
	}
	// limit is the largest multiple of the alphabet size that fits in a byte;
	// any draw at or above it is rejected to keep the distribution uniform.
	limit := byte(256 / len(passwordAlphabet) * len(passwordAlphabet))
	out := make([]byte, 0, n)
	for len(out) < n {
		raw, err := security.RandomSecret(n)
		if err != nil {
			return "", ErrInternal(err, "A credential could not be generated.")
		}
		for _, b := range raw {
			if b >= limit {
				continue
			}
			out = append(out, passwordAlphabet[int(b)%len(passwordAlphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// assertSafeSecret is the last line of defence before a secret is embedded in
// engine statement text. Only generated passwords ever reach that path, and
// they are alphanumeric by construction; anything else is a programming error
// and is refused rather than escaped.
func assertSafeSecret(secret string) error {
	if secret == "" {
		return ErrInternal(nil, "A credential could not be applied.")
	}
	for i := 0; i < len(secret); i++ {
		c := secret[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		default:
			return ErrInternal(nil, "A credential could not be applied.")
		}
	}
	return nil
}

// argvSafe reports whether a command argument is free of the control and
// whitespace characters that would indicate a caller smuggled structure into
// a single argument. Arguments are never concatenated into a shell line, so
// this is a defensive invariant rather than an escaping mechanism.
func argvSafe(arg string) bool {
	if arg == "" {
		return false
	}
	for i := 0; i < len(arg); i++ {
		if arg[i] < 0x20 || arg[i] == 0x7f {
			return false
		}
	}
	return true
}

// checkArgv validates a fully built argv slice before it is handed to the
// orchestrator. A slice that fails this check is never executed.
func checkArgv(argv []string) error {
	if len(argv) == 0 {
		return ErrInternal(nil, "That operation could not be prepared.")
	}
	for _, arg := range argv {
		if !argvSafe(arg) {
			return ErrInternal(nil, "That operation could not be prepared.")
		}
	}
	return nil
}

// execRequest assembles an ExecRequest. Argv is passed through untouched:
// there is no shell, no string splitting and no interpolation anywhere in
// this package.
func execRequest(argv []string, env map[string]string, stdin io.Reader, timeout time.Duration) ExecRequest {
	return ExecRequest{
		Argv:    argv,
		Env:     env,
		Stdin:   stdin,
		Timeout: timeout,
	}
}

// statementCommand builds a command that feeds statement text to an engine
// client on standard input. Statement text stays out of the argument list so
// it never appears in a process table or a container audit log.
func statementCommand(argv []string, env map[string]string, statements []string, timeout time.Duration) (command, error) {
	if err := checkArgv(argv); err != nil {
		return command{}, err
	}
	body := strings.Join(statements, "\n") + "\n"
	return command{Exec: execRequest(argv, env, strings.NewReader(body), timeout)}, nil
}

// streamCommand builds a command whose output is streamed to out rather than
// captured, used for dumps that can be far larger than memory.
func streamCommand(argv []string, env map[string]string, out io.Writer, timeout time.Duration) (command, error) {
	if err := checkArgv(argv); err != nil {
		return command{}, err
	}
	req := execRequest(argv, env, nil, timeout)
	req.Stdout = out
	return command{Exec: req}, nil
}

// inputCommand builds a command that consumes a stream on standard input,
// used for restores.
func inputCommand(argv []string, env map[string]string, in io.Reader, timeout time.Duration) (command, error) {
	if err := checkArgv(argv); err != nil {
		return command{}, err
	}
	return command{Exec: execRequest(argv, env, in, timeout)}, nil
}

// probeCommand builds a short command whose output is captured for parsing.
func probeCommand(argv []string, env map[string]string, timeout time.Duration) command {
	return command{Exec: execRequest(argv, env, nil, timeout)}
}

// trimLines splits captured output into non-empty trimmed lines.
func trimLines(out string) []string {
	raw := strings.Split(out, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
