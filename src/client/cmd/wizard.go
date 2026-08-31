package cmd

import (
	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/auth"
	"github.com/webappsgo/cashp/src/client/settings"
)

// RunSetupWizard is the first-run flow: ask for a server, optionally take a
// token, test the connection and save the result. It is line-based because
// it must work identically over SSH, in a container and on a bare console.
func RunSetupWizard(ctx *Context) error {
	if !ctx.Interactive {
		return &api.Error{
			Kind:    api.KindConfig,
			Message: "no server configured and no terminal available; pass --server URL and --token",
		}
	}

	ctx.Out.Message("cashp CLI setup")
	ctx.Out.Message("Connect this machine to a cashp server.")

	server, err := Prompt(ctx, "Server URL", ctx.Config.Server.Primary)
	if err != nil {
		return err
	}
	if err := api.ValidateServerURL(server); err != nil {
		return err
	}

	token, err := PromptSecret(ctx, "API token (leave blank to stay anonymous)")
	if err != nil {
		return err
	}
	if token != "" && auth.IsAgentToken(token) {
		return &api.Error{
			Kind:    api.KindAuth,
			Message: "that is an agent token; agent tokens cannot be used with the CLI",
		}
	}

	client, err := api.New(clientOptions(ctx, server, token))
	if err != nil {
		return err
	}
	ctx.Client = client

	discovered, err := client.Autodiscover(ctx.Ctx)
	if err != nil {
		return err
	}

	ctx.Config.Server.Primary = server
	applyAutodiscover(ctx.Config, discovered)

	if token != "" {
		if _, err := client.Get(ctx.Ctx, WhoAmIPath, nil); err != nil {
			return err
		}
		if err := auth.Store(token); err != nil {
			return err
		}
	}

	ctx.Config.Auth.Token = ""
	if err := settings.Save(ctx.ConfigPath, ctx.Config); err != nil {
		return err
	}

	name := discovered.ServerName
	if name == "" {
		name = server
	}
	ctx.Out.Success("connected to %s", name)
	ctx.Out.Message("Run '%s --help' to see what you can do.", ctx.BinaryName)
	return nil
}
