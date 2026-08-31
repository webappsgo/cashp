// Package shell prints the agent's completion script and rc-file init line.
// The agent has its own generator rather than sharing the CLI's because the
// two binaries expose different flags and commands, and a completion script
// that offers flags the binary rejects is worse than none.
package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Usage is the message shown for an unrecognised --shell argument.
const Usage = "Usage: --shell [completions|init|help] [SHELL]"

// ErrUsage marks an argument error so the caller can exit with code 64.
var ErrUsage = errors.New(Usage)

// SupportedShells lists every shell the generator handles.
var SupportedShells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"}

// Commands are the agent subcommands offered by completion.
var Commands = []string{"register", "status", "test"}

// FlagNames are the agent flags offered by completion.
var FlagNames = []string{
	"--color", "--config", "--data", "--debug", "--help", "--lang", "--log",
	"--mode", "--server", "--service", "--shell", "--status", "--token",
	"--update", "--version",
}

// Detect resolves the shell name from $SHELL, defaulting to bash.
func Detect() string {
	trimmed := strings.TrimSpace(os.Getenv("SHELL"))
	if trimmed == "" {
		return "bash"
	}
	return filepath.Base(trimmed)
}

// IsSupported reports whether completions can be generated for shell.
func IsSupported(shell string) bool {
	for _, supported := range SupportedShells {
		if supported == shell {
			return true
		}
	}
	return false
}

// Handle implements --shell completions, --shell init and --shell help.
func Handle(out io.Writer, binaryName, action, shell string) error {
	if strings.TrimSpace(shell) == "" {
		shell = Detect()
	}

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "help":
		return printHelp(out, binaryName)
	case "completions":
		if !IsSupported(shell) {
			return fmt.Errorf("%w: unsupported shell: %s", ErrUsage, shell)
		}
		return printCompletions(out, binaryName, shell)
	case "init":
		if !IsSupported(shell) {
			return fmt.Errorf("%w: unsupported shell: %s", ErrUsage, shell)
		}
		return printInit(out, binaryName, shell)
	default:
		return ErrUsage
	}
}

// printHelp explains the completion subcommands.
func printHelp(out io.Writer, binaryName string) error {
	_, err := fmt.Fprintf(out, `%s

Print a completion script:
  %s --shell completions [SHELL]

Print the line to add to your shell rc file:
  %s --shell init [SHELL]

SHELL is optional and is auto-detected from $SHELL. Supported shells:
  %s
`, Usage, binaryName, binaryName, strings.Join(SupportedShells, ", "))
	return err
}

// printInit prints the eval-ready line for a shell rc file.
func printInit(out io.Writer, binaryName, shell string) error {
	var line string
	switch shell {
	case "bash", "zsh":
		line = fmt.Sprintf("source <(%s --shell completions %s)", binaryName, shell)
	case "fish":
		line = fmt.Sprintf("%s --shell completions fish | source", binaryName)
	case "sh", "dash", "ksh":
		line = fmt.Sprintf("eval \"$(%s --shell completions %s)\"", binaryName, shell)
	default:
		line = fmt.Sprintf("Invoke-Expression (& %s --shell completions powershell)", binaryName)
	}
	_, err := fmt.Fprintln(out, line)
	return err
}

// printCompletions writes the completion script for shell.
func printCompletions(out io.Writer, binaryName, shell string) error {
	switch shell {
	case "bash":
		return bashCompletions(out, binaryName)
	case "zsh":
		return zshCompletions(out, binaryName)
	case "fish":
		return fishCompletions(out, binaryName)
	case "sh", "dash", "ksh":
		return posixCompletions(out, binaryName)
	default:
		return powershellCompletions(out, binaryName)
	}
}

// functionName turns a binary name into an identifier usable in shell
// function names.
func functionName(binaryName string) string {
	builder := &strings.Builder{}
	for _, char := range binaryName {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "agent"
	}
	return builder.String()
}

// words is the completion word list: commands first, then flags.
func words() string {
	return strings.Join(append(append([]string{}, Commands...), FlagNames...), " ")
}

// bashCompletions writes a bash completion script.
func bashCompletions(out io.Writer, binaryName string) error {
	_, err := fmt.Fprintf(out, `_%s_complete() {
  local words="%s"
  COMPREPLY=($(compgen -W "${words}" -- "${COMP_WORDS[COMP_CWORD]}"))
}
complete -F _%s_complete %s
`, functionName(binaryName), words(), functionName(binaryName), binaryName)
	return err
}

// zshCompletions writes a zsh completion script.
func zshCompletions(out io.Writer, binaryName string) error {
	_, err := fmt.Fprintf(out, `#compdef %s
_%s_complete() {
  local -a words
  words=(%s)
  compadd -- ${words[@]}
}
compdef _%s_complete %s
`, binaryName, functionName(binaryName), words(), functionName(binaryName), binaryName)
	return err
}

// fishCompletions writes a fish completion script.
func fishCompletions(out io.Writer, binaryName string) error {
	for _, command := range Commands {
		if _, err := fmt.Fprintf(out, "complete -c %s -f -a %s\n", binaryName, command); err != nil {
			return err
		}
	}
	for _, flag := range FlagNames {
		if _, err := fmt.Fprintf(out, "complete -c %s -f -l %s\n", binaryName, strings.TrimPrefix(flag, "--")); err != nil {
			return err
		}
	}
	return nil
}

// posixCompletions writes a POSIX-shell completion script.
func posixCompletions(out io.Writer, binaryName string) error {
	_, err := fmt.Fprintf(out, `# %s completion words
%s_COMPLETIONS="%s"
export %s_COMPLETIONS
`, binaryName, strings.ToUpper(functionName(binaryName)), words(), strings.ToUpper(functionName(binaryName)))
	return err
}

// powershellCompletions writes a PowerShell completion script.
func powershellCompletions(out io.Writer, binaryName string) error {
	quoted := make([]string, 0, len(Commands)+len(FlagNames))
	for _, word := range append(append([]string{}, Commands...), FlagNames...) {
		quoted = append(quoted, "'"+word+"'")
	}

	_, err := fmt.Fprintf(out, `Register-ArgumentCompleter -Native -CommandName %s -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  @(%s) | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`, binaryName, strings.Join(quoted, ", "))
	return err
}
