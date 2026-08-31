// Package auth resolves, stores and masks the cashp-cli bearer credential
// following the token source priority in AI.md PART 33.
package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/webappsgo/cashp/src/client/paths"
	"github.com/webappsgo/cashp/src/config"
	"github.com/webappsgo/cashp/src/security"
)

// Source identifies where a resolved token came from, for --debug output.
type Source string

// Token sources in priority order, highest first.
const (
	SourceFlag      Source = "flag"
	SourceFlagFile  Source = "token-file"
	SourceEnv       Source = "environment"
	SourceConfig    Source = "config"
	SourceTokenFile Source = "token-file-default"
	SourceNone      Source = "none"
)

// ErrNoToken means no credential was found in any supported location.
var ErrNoToken = errors.New("no authentication token found")

// EnvVar is the environment variable holding a bearer token.
var EnvVar = strings.ToUpper(config.InternalName) + "_TOKEN"

// Token is a resolved credential and the location it came from.
type Token struct {
	Value  string
	Source Source
}

// Empty reports whether no credential was resolved.
func (t Token) Empty() bool {
	return strings.TrimSpace(t.Value) == ""
}

// Masked renders the credential for display: the token prefix plus a fixed
// mask. The secret body is never returned.
func (t Token) Masked() string {
	return Mask(t.Value)
}

// Options describes the credential inputs for one CLI invocation.
type Options struct {
	// Flag is the --token value.
	Flag string
	// FlagFile is the --token-file path.
	FlagFile string
	// ConfigToken is auth.token from cli.yml.
	ConfigToken string
	// ConfigTokenFile is auth.token_file from cli.yml.
	ConfigTokenFile string
}

// Resolve applies the priority order: --token, --token-file, the
// environment variable, cli.yml auth.token, then {config_dir}/token.
func Resolve(opts Options) (Token, error) {
	if value := strings.TrimSpace(opts.Flag); value != "" {
		return Token{Value: value, Source: SourceFlag}, nil
	}

	if path := strings.TrimSpace(opts.FlagFile); path != "" {
		value, err := ReadTokenFile(path)
		if err != nil {
			return Token{Source: SourceNone}, err
		}
		return Token{Value: value, Source: SourceFlagFile}, nil
	}

	if value := strings.TrimSpace(os.Getenv(EnvVar)); value != "" {
		return Token{Value: value, Source: SourceEnv}, nil
	}

	if value := strings.TrimSpace(opts.ConfigToken); value != "" {
		return Token{Value: value, Source: SourceConfig}, nil
	}

	if path := strings.TrimSpace(opts.ConfigTokenFile); path != "" {
		value, err := ReadTokenFile(path)
		if err != nil {
			return Token{Source: SourceNone}, err
		}
		return Token{Value: value, Source: SourceConfig}, nil
	}

	value, err := ReadTokenFile(paths.TokenFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Token{Source: SourceNone}, ErrNoToken
		}
		return Token{Source: SourceNone}, err
	}
	if value == "" {
		return Token{Source: SourceNone}, ErrNoToken
	}
	return Token{Value: value, Source: SourceTokenFile}, nil
}

// ReadTokenFile loads a credential file, refusing any file readable by
// group or other.
func ReadTokenFile(path string) (string, error) {
	if err := paths.CheckFilePerms(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("token file not found: %w", os.ErrNotExist)
		}
		return "", fmt.Errorf("read token file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Store writes the credential to {config_dir}/token with owner-only
// permissions.
func Store(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("refusing to store an empty token")
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	return paths.WriteSecureFile(paths.TokenFile(), []byte(trimmed+"\n"))
}

// Clear removes the stored credential. A missing file is not an error, so
// logout is idempotent.
func Clear() error {
	if err := os.Remove(paths.TokenFile()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token file: %w", err)
	}
	return nil
}

// Mask renders a credential safe for logs and terminal output: the token
// prefix is kept so the user can tell which credential is in play, and the
// random body is replaced.
func Mask(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if prefix, _, err := security.ParseToken(trimmed); err == nil {
		return prefix + security.MaskedValue
	}
	return security.MaskedValue
}

// IsAgentToken reports whether a credential carries an agent prefix, which
// the CLI must never send to user-facing endpoints.
func IsAgentToken(value string) bool {
	prefix, _, err := security.ParseToken(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return strings.HasSuffix(prefix, "_agt_")
}
