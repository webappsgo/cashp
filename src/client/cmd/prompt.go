package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/webappsgo/cashp/src/client/term"
	"github.com/webappsgo/cashp/src/config"
)

// Confirm asks a yes/no question. In a non-interactive session it refuses
// rather than guessing, so scripts must pass --yes explicitly.
func Confirm(ctx *Context, question string) (bool, error) {
	if ctx.Globals.Yes {
		return true, nil
	}
	if !ctx.Interactive {
		return false, usagef("%s refusing to continue without --yes in a non-interactive session", question)
	}

	fmt.Fprintf(os.Stdout, "%s [y/N]: ", question)
	answer, err := term.ReadLine(ctx.Stdin)
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	return config.IsTruthy(strings.TrimSpace(answer)), nil
}

// Prompt asks for a visible value, returning fallback when the user just
// presses enter.
func Prompt(ctx *Context, question, fallback string) (string, error) {
	if fallback != "" {
		fmt.Fprintf(os.Stdout, "%s [%s]: ", question, fallback)
	} else {
		fmt.Fprintf(os.Stdout, "%s: ", question)
	}

	answer, err := term.ReadLine(ctx.Stdin)
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

// PromptSecret asks for a credential without echoing it. When echo cannot
// be disabled the user is warned that the value will be visible rather
// than silently typing a token into a logged terminal.
func PromptSecret(ctx *Context, question string) (string, error) {
	fmt.Fprintf(os.Stdout, "%s: ", question)
	secret, hidden, err := term.ReadSecret(ctx.Stdin)
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	if !hidden {
		ctx.Out.Warn("warning: this terminal could not hide input, so the value was shown on screen")
	}
	return strings.TrimSpace(secret), nil
}
