package cmd

import (
	"net/http"
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
)

// newAdminCommands builds the command groups that only exist inside the
// administration namespace. They are registered only when --admin is
// passed, so a non-admin invocation cannot reach them by accident.
func newAdminCommands() []*Command {
	return []*Command{
		{
			Name:    "server",
			Summary: "Administer the server itself",
			Subcommands: []*Command{
				{
					Name:        "status",
					Summary:     "Show detailed server status",
					NeedsClient: true,
					Run:         adminRequest(http.MethodGet, "server/status"),
				},
				{
					Name:        "config",
					Summary:     "Show the effective server configuration",
					NeedsClient: true,
					Run:         adminRequest(http.MethodGet, "server/config"),
				},
				{
					Name:        "reload",
					Summary:     "Reload the server configuration",
					NeedsClient: true,
					Run:         adminRequest(http.MethodPost, "server/reload"),
				},
				{
					Name:        "restart",
					Summary:     "Restart the server",
					NeedsClient: true,
					Run:         adminRequest(http.MethodPost, "server/restart"),
				},
				{
					Name:        "maintenance",
					Summary:     "Enable or disable maintenance mode",
					Args:        "enabled=true|false",
					NeedsClient: true,
					Run:         adminRequest(http.MethodPost, "server/maintenance"),
				},
			},
		},
		{
			Name:    "settings",
			Summary: "Read and change platform settings",
			Subcommands: []*Command{
				{
					Name:        "list",
					Summary:     "List platform settings",
					NeedsClient: true,
					Run:         adminRequest(http.MethodGet, "settings"),
				},
				{
					Name:        "set",
					Summary:     "Change platform settings",
					Args:        "key=value [key=value ...]",
					NeedsClient: true,
					Run:         adminRequest(http.MethodPatch, "settings"),
				},
			},
		},
		{
			Name:    "setup-token",
			Summary: "Manage the one-time global-admin setup token",
			Subcommands: []*Command{
				{
					Name:        "status",
					Summary:     "Show whether a setup token is outstanding",
					NeedsClient: true,
					Run:         adminRequest(http.MethodGet, "setup-token"),
				},
			},
		},
	}
}

// adminRequest builds a handler that talks to a fixed admin endpoint,
// turning key=value arguments into a body or query as the method requires.
func adminRequest(method, endpoint string) func(*Context, []string) error {
	return func(ctx *Context, args []string) error {
		client, err := ctx.APIClient()
		if err != nil {
			return err
		}

		path := AdminResourcePath(ctx.Config.Server.AdminPath, endpoint)
		request := api.Request{Method: method, Path: client.VersionedPath(path)}

		if method == http.MethodGet {
			query, err := parseQuery(args)
			if err != nil {
				return err
			}
			request.Query = query
		} else if len(args) > 0 {
			body, err := parseFields(args)
			if err != nil {
				return err
			}
			request.Body = body
		}

		env, err := client.Do(ctx.Ctx, request)
		if err != nil {
			return err
		}
		if len(env.Data) == 0 {
			ctx.Out.Success("%s completed", strings.ReplaceAll(endpoint, "/", " "))
			return nil
		}
		return emitAuto(ctx, env, nil)
	}
}
