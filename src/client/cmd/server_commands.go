package cmd

import (
	"runtime"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/output"
)

// newServerCommands builds the read-only commands that describe the server
// itself rather than a tenant resource.
func newServerCommands() []*Command {
	return []*Command{
		{
			Name:        "status",
			Summary:     "Show server and cluster status",
			NeedsClient: true,
			Run:         runStatus,
		},
		{
			Name:        "version",
			Summary:     "Show client and server versions",
			NeedsClient: true,
			Run:         runVersionCommand,
		},
		{
			Name:        "autodiscover",
			Summary:     "Show the server's discovery document",
			NeedsClient: true,
			Run:         runAutodiscover,
		},
		{
			Name:        "metrics",
			Summary:     "Show server metrics",
			NeedsClient: true,
			Run:         runMetrics,
		},
		{
			Name:        "health",
			Summary:     "Check server health",
			NeedsClient: true,
			Run:         runHealth,
		},
	}
}

// runStatus reports the panel's own status document.
func runStatus(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("status takes no positional arguments")
	}
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Get(ctx.Ctx, "status", nil)
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}

// runVersionCommand shows the client build alongside the server version and
// warns when the two disagree.
func runVersionCommand(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("version takes no positional arguments")
	}

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Get(ctx.Ctx, "version", nil)
	if err != nil {
		return err
	}

	var server map[string]any
	if err := env.Decode(&server); err != nil {
		return err
	}

	serverVersion := output.Stringify(server["version"])
	combined := map[string]any{
		"client_version": ctx.Version,
		"server_version": serverVersion,
		"server":         client.ActiveServer(),
		"api_version":    client.APIVersion(),
		"go":             runtime.Version(),
		"os_arch":        runtime.GOOS + "/" + runtime.GOARCH,
	}

	table := output.Table{
		Headers: []string{"FIELD", "VALUE"},
		Rows: [][]string{
			{"client_version", ctx.Version},
			{"server_version", serverVersion},
			{"server", client.ActiveServer()},
			{"api_version", client.APIVersion()},
			{"go", runtime.Version()},
			{"os_arch", runtime.GOOS + "/" + runtime.GOARCH},
		},
	}

	if serverVersion != "" && ctx.Version != "devel" && serverVersion != ctx.Version {
		ctx.Out.Warn("warning: client %s and server %s differ; run '%s --update' if commands misbehave", ctx.Version, serverVersion, ctx.BinaryName)
	}
	return ctx.Out.Emit(combined, table)
}

// runAutodiscover prints the unversioned discovery document.
func runAutodiscover(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("autodiscover takes no positional arguments")
	}
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	discovered, err := client.Autodiscover(ctx.Ctx)
	if err != nil {
		return err
	}

	rows := [][]string{
		{"primary", discovered.Primary},
		{"api_version", discovered.APIVersion},
		{"cli_min_version", discovered.CLIMinVer},
	}
	for _, member := range discovered.Cluster {
		rows = append(rows, []string{"cluster", member})
	}
	for _, capability := range discovered.Capabilities {
		rows = append(rows, []string{"capability", capability})
	}
	return ctx.Out.Emit(discovered, output.Table{Headers: []string{"FIELD", "VALUE"}, Rows: rows})
}

// runMetrics prints the server metrics document.
func runMetrics(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("metrics takes no positional arguments")
	}
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Get(ctx.Ctx, "metrics", nil)
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}

// runHealth checks the bare health endpoint, which is not enveloped.
func runHealth(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("health takes no positional arguments")
	}
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Do(ctx.Ctx, api.Request{Path: "/healthz", Raw: true})
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}
