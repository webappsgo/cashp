package cmd

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/output"
	"github.com/webappsgo/cashp/src/client/settings"
)

// Context carries everything a command needs: parsed globals, loaded
// configuration, the output writer and a lazily built API client.
type Context struct {
	// Ctx bounds every request the command makes.
	Ctx context.Context
	// Globals holds the parsed global flags.
	Globals *Globals
	// Config is the loaded cli.yml document.
	Config *settings.Config
	// ConfigPath is the file Config was loaded from.
	ConfigPath string
	// Out renders results.
	Out *output.Writer
	// Client talks to the panel; nil until requireClient succeeds.
	Client *api.Client
	// Version is the compiled CLI version.
	Version string
	// BinaryName is filepath.Base(os.Args[0]) for display only.
	BinaryName string
	// Stdin is the input stream used by prompts.
	Stdin io.Reader
	// Interactive reports whether stdin and stdout are both terminals.
	Interactive bool
	// Stdout is where help text is written.
	Stdout io.Writer
	// Argv is the original argument vector, used when re-execing after a
	// self-update.
	Argv []string
	// newClient builds the API client on demand.
	newClient func() (*api.Client, error)
}

// Command is one node in the CLI command tree.
type Command struct {
	// Name is the word typed on the command line.
	Name string
	// Summary is the one-line description shown in help listings.
	Summary string
	// Args describes positional arguments for the usage line.
	Args string
	// Long is optional extended help.
	Long string
	// Subcommands are the child commands, if any.
	Subcommands []*Command
	// Run executes a leaf command.
	Run func(ctx *Context, args []string) error
	// NeedsClient makes the dispatcher build an authenticated client first.
	NeedsClient bool
}

// Lookup finds a direct subcommand by name.
func (c *Command) Lookup(name string) *Command {
	for _, sub := range c.Subcommands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

// IsLeaf reports whether the command executes directly.
func (c *Command) IsLeaf() bool {
	return len(c.Subcommands) == 0
}

// SortedSubcommands returns the child commands in alphabetical order.
func (c *Command) SortedSubcommands() []*Command {
	sorted := make([]*Command, len(c.Subcommands))
	copy(sorted, c.Subcommands)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted
}

// Path returns the space-joined command path for usage output.
func (c *Command) Path(parents []string) string {
	return strings.TrimSpace(strings.Join(append(parents, c.Name), " "))
}

// Client returns the API client, building it on first use so commands that
// never talk to the panel (help, completions) work without configuration.
func (ctx *Context) APIClient() (*api.Client, error) {
	if ctx.Client != nil {
		return ctx.Client, nil
	}
	client, err := ctx.newClient()
	if err != nil {
		return nil, err
	}
	ctx.Client = client
	return client, nil
}
