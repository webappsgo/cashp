package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/auth"
)

// InteractiveMaxLine caps one typed line so a pasted binary blob cannot
// exhaust memory.
const InteractiveMaxLine = 64 * 1024

// RunInteractive is the no-argument mode: a line-based session that accepts
// the same commands as the command line. It is deliberately terminal-only
// and line-oriented so it behaves identically over SSH, in a container and
// on a bare console.
func RunInteractive(ctx *Context, root *Command) error {
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}

	ctx.Out.Message("%s %s — interactive mode", ctx.BinaryName, ctx.Version)
	ctx.Out.Message("Connected to %s.", client.ActiveServer())
	ctx.Out.Message("Type a command, 'help' for the command list, or 'exit' to quit.")

	scanner := bufio.NewScanner(ctx.Stdin)
	scanner.Buffer(make([]byte, 0, 4096), InteractiveMaxLine)

	for {
		fmt.Fprintf(ctx.Stdout, "%s> ", ctx.BinaryName)
		if !scanner.Scan() {
			break
		}

		args, err := splitLine(scanner.Text())
		if err != nil {
			ctx.Out.Error("error: %s", err)
			continue
		}
		if len(args) == 0 {
			continue
		}

		switch strings.ToLower(args[0]) {
		case "exit", "quit":
			return nil
		case "help", "?":
			if len(args) == 1 {
				if err := PrintRootHelp(ctx.Stdout, ctx.BinaryName, ctx.Version, root); err != nil {
					return err
				}
				continue
			}
			args = append(args[1:], "help")
		}

		if err := Dispatch(ctx, root, args); err != nil {
			reportInteractive(ctx, err)
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read input: %w", err)
	}
	ctx.Out.Message("")
	return nil
}

// reportInteractive prints a command failure without ending the session. A
// rejected credential is dropped immediately so the rest of the session
// cannot keep replaying it.
func reportInteractive(ctx *Context, err error) {
	ctx.Out.Error("error: %s", err)

	apiErr, ok := api.AsError(err)
	if !ok || !apiErr.IsTokenRejected() {
		return
	}
	if clearErr := auth.Clear(); clearErr != nil {
		ctx.Out.Warn("warning: could not remove the stored token")
	}
	ctx.Client = nil
	ctx.Out.Warn("Run 'login' to authenticate again.")
}

// splitLine splits a typed line into arguments, honouring single and double
// quotes so values containing spaces survive. Unterminated quotes are a
// user error rather than a silent truncation.
func splitLine(line string) ([]string, error) {
	args := []string{}
	current := strings.Builder{}
	quote := rune(0)
	started := false

	for _, char := range line {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			current.WriteRune(char)
		case char == '\'' || char == '"':
			quote = char
			started = true
		case char == ' ' || char == '\t':
			if started || current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(char)
		}
	}

	if quote != 0 {
		return nil, usagef("unterminated %c quote", quote)
	}
	if started || current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}
