package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// shellEnvValue returns the user's $SHELL for auto-detection.
func shellEnvValue() string {
	return os.Getenv("SHELL")
}

// SupportedShells lists every shell the completion generator handles.
var SupportedShells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"}

// GlobalFlagNames lists the flags offered by completion.
var GlobalFlagNames = []string{
	"--admin", "--color", "--config", "--debug", "--help", "--json", "--lang",
	"--limit", "--no-color", "--output", "--quiet", "--server", "--shell",
	"--token", "--token-file", "--update", "--user", "--verbose", "--version",
	"--yes",
}

// DetectShell resolves the shell name from $SHELL, defaulting to bash.
func DetectShell(shellEnv string) string {
	trimmed := strings.TrimSpace(shellEnv)
	if trimmed == "" {
		return "bash"
	}
	return filepath.Base(trimmed)
}

// IsSupportedShell reports whether completions can be generated for shell.
func IsSupportedShell(shell string) bool {
	for _, supported := range SupportedShells {
		if supported == shell {
			return true
		}
	}
	return false
}

// ShellUsage is the message shown for an unrecognised --shell argument.
const ShellUsage = "Usage: --shell [completions|init|help] [SHELL]"

// HandleShell implements --shell completions, --shell init and
// --shell help. It writes to out and returns a usage error for anything
// else so the caller can exit with code 64.
func HandleShell(out io.Writer, binaryName, action, shell string, root *Command) error {
	if shell == "" {
		shell = DetectShell(shellEnvValue())
	}

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "help":
		return printShellHelp(out, binaryName)
	case "completions":
		if !IsSupportedShell(shell) {
			return usagef("unsupported shell: %s", shell)
		}
		return printCompletions(out, binaryName, shell, root)
	case "init":
		if !IsSupportedShell(shell) {
			return usagef("unsupported shell: %s", shell)
		}
		return printInit(out, binaryName, shell)
	default:
		return usagef("%s", ShellUsage)
	}
}

// printShellHelp explains the completion subcommands.
func printShellHelp(out io.Writer, binaryName string) error {
	_, err := fmt.Fprintf(out, `%s

Print a completion script:
  %s --shell completions [SHELL]

Print the line to add to your shell rc file:
  %s --shell init [SHELL]

SHELL is optional and is auto-detected from $SHELL. Supported shells:
  %s
`, ShellUsage, binaryName, binaryName, strings.Join(SupportedShells, ", "))
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
	case "powershell", "pwsh":
		line = fmt.Sprintf("Invoke-Expression (& %s --shell completions powershell)", binaryName)
	}
	_, err := fmt.Fprintln(out, line)
	return err
}

// printCompletions writes the completion script for shell.
func printCompletions(out io.Writer, binaryName, shell string, root *Command) error {
	switch shell {
	case "bash":
		return bashCompletions(out, binaryName, root)
	case "zsh":
		return zshCompletions(out, binaryName, root)
	case "fish":
		return fishCompletions(out, binaryName, root)
	case "sh", "dash", "ksh":
		return posixCompletions(out, binaryName, root)
	default:
		return powershellCompletions(out, binaryName, root)
	}
}

// topLevelNames returns the sorted first-word command names.
func topLevelNames(root *Command) []string {
	names := make([]string, 0, len(root.Subcommands))
	for _, sub := range root.Subcommands {
		names = append(names, sub.Name)
	}
	sort.Strings(names)
	return names
}

// subNames returns the sorted subcommand names of one command.
func subNames(command *Command) []string {
	names := make([]string, 0, len(command.Subcommands))
	for _, sub := range command.Subcommands {
		names = append(names, sub.Name)
	}
	sort.Strings(names)
	return names
}

// functionSafeName turns a binary name into an identifier usable in shell
// function names.
func functionSafeName(binaryName string) string {
	var builder strings.Builder
	for _, char := range binaryName {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

// bashCompletions writes a two-level bash completion script.
func bashCompletions(out io.Writer, binaryName string, root *Command) error {
	safe := functionSafeName(binaryName)
	var builder strings.Builder

	fmt.Fprintf(&builder, "_%s_completions() {\n", safe)
	builder.WriteString("  local cur prev commands flags\n")
	builder.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	builder.WriteString("  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(&builder, "  commands=\"%s\"\n", strings.Join(topLevelNames(root), " "))
	fmt.Fprintf(&builder, "  flags=\"%s\"\n", strings.Join(GlobalFlagNames, " "))
	builder.WriteString("  if [[ \"$cur\" == -* ]]; then\n")
	builder.WriteString("    COMPREPLY=( $(compgen -W \"$flags\" -- \"$cur\") )\n")
	builder.WriteString("    return 0\n")
	builder.WriteString("  fi\n")
	builder.WriteString("  case \"$prev\" in\n")
	for _, command := range root.SortedSubcommands() {
		if command.IsLeaf() {
			continue
		}
		fmt.Fprintf(&builder, "    %s) COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") ); return 0 ;;\n",
			command.Name, strings.Join(subNames(command), " "))
	}
	builder.WriteString("  esac\n")
	builder.WriteString("  COMPREPLY=( $(compgen -W \"$commands\" -- \"$cur\") )\n")
	builder.WriteString("}\n")
	fmt.Fprintf(&builder, "complete -F _%s_completions %s\n", safe, binaryName)

	_, err := io.WriteString(out, builder.String())
	return err
}

// zshCompletions writes a zsh completion script.
func zshCompletions(out io.Writer, binaryName string, root *Command) error {
	safe := functionSafeName(binaryName)
	var builder strings.Builder

	fmt.Fprintf(&builder, "#compdef %s\n", binaryName)
	fmt.Fprintf(&builder, "_%s() {\n", safe)
	builder.WriteString("  local -a commands\n")
	builder.WriteString("  commands=(\n")
	for _, command := range root.SortedSubcommands() {
		fmt.Fprintf(&builder, "    '%s:%s'\n", command.Name, escapeSingleQuotes(command.Summary))
	}
	builder.WriteString("  )\n")
	builder.WriteString("  if (( CURRENT == 2 )); then\n")
	builder.WriteString("    _describe 'command' commands\n")
	builder.WriteString("    return\n")
	builder.WriteString("  fi\n")
	builder.WriteString("  case \"${words[2]}\" in\n")
	for _, command := range root.SortedSubcommands() {
		if command.IsLeaf() {
			continue
		}
		fmt.Fprintf(&builder, "    %s) compadd %s ;;\n", command.Name, strings.Join(subNames(command), " "))
	}
	builder.WriteString("  esac\n")
	builder.WriteString("}\n")
	fmt.Fprintf(&builder, "_%s \"$@\"\n", safe)

	_, err := io.WriteString(out, builder.String())
	return err
}

// fishCompletions writes a fish completion script.
func fishCompletions(out io.Writer, binaryName string, root *Command) error {
	var builder strings.Builder

	for _, command := range root.SortedSubcommands() {
		fmt.Fprintf(&builder, "complete -c %s -n '__fish_use_subcommand' -a %s -d '%s'\n",
			binaryName, command.Name, escapeSingleQuotes(command.Summary))
		for _, sub := range command.SortedSubcommands() {
			fmt.Fprintf(&builder, "complete -c %s -n '__fish_seen_subcommand_from %s' -a %s -d '%s'\n",
				binaryName, command.Name, sub.Name, escapeSingleQuotes(sub.Summary))
		}
	}
	for _, flag := range GlobalFlagNames {
		fmt.Fprintf(&builder, "complete -c %s -l %s\n", binaryName, strings.TrimPrefix(flag, "--"))
	}

	_, err := io.WriteString(out, builder.String())
	return err
}

// posixCompletions writes a basic POSIX completion script for sh, dash and
// ksh, which have no programmable completion beyond a word list.
func posixCompletions(out io.Writer, binaryName string, root *Command) error {
	safe := functionSafeName(binaryName)
	var builder strings.Builder

	fmt.Fprintf(&builder, "%s_commands=\"%s\"\n", safe, strings.Join(topLevelNames(root), " "))
	fmt.Fprintf(&builder, "%s_flags=\"%s\"\n", safe, strings.Join(GlobalFlagNames, " "))
	fmt.Fprintf(&builder, "%s_complete() {\n", safe)
	fmt.Fprintf(&builder, "  echo \"$%s_commands $%s_flags\"\n", safe, safe)
	builder.WriteString("}\n")

	_, err := io.WriteString(out, builder.String())
	return err
}

// powershellCompletions writes a PowerShell argument completer.
func powershellCompletions(out io.Writer, binaryName string, root *Command) error {
	var builder strings.Builder

	fmt.Fprintf(&builder, "Register-ArgumentCompleter -Native -CommandName %s -ScriptBlock {\n", binaryName)
	builder.WriteString("  param($wordToComplete, $commandAst, $cursorPosition)\n")
	fmt.Fprintf(&builder, "  $commands = @(%s)\n", quoteAll(topLevelNames(root)))
	fmt.Fprintf(&builder, "  $flags = @(%s)\n", quoteAll(GlobalFlagNames))
	builder.WriteString("  $candidates = $commands + $flags\n")
	builder.WriteString("  $candidates | Where-Object { $_ -like \"$wordToComplete*\" } | ForEach-Object {\n")
	builder.WriteString("    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)\n")
	builder.WriteString("  }\n")
	builder.WriteString("}\n")

	_, err := io.WriteString(out, builder.String())
	return err
}

// quoteAll renders a slice as a PowerShell single-quoted list.
func quoteAll(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+escapeSingleQuotes(value)+"'")
	}
	return strings.Join(quoted, ", ")
}

// escapeSingleQuotes makes a description safe inside a single-quoted shell
// string.
func escapeSingleQuotes(value string) string {
	return strings.ReplaceAll(value, "'", "")
}
