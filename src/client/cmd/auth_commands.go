package cmd

import (
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/auth"
	"github.com/webappsgo/cashp/src/client/output"
	"github.com/webappsgo/cashp/src/client/settings"
)

// WhoAmIPath is the identity endpoint used to validate a credential.
const WhoAmIPath = "auth/whoami"

// newAuthCommands builds login, logout and whoami.
func newAuthCommands() []*Command {
	return []*Command{
		{
			Name:    "login",
			Summary: "Store an API token for this machine",
			Long: "Validates the token against the server, saves it to the user token file\n" +
				"with owner-only permissions, and caches the cluster URL list.",
			Run: runLogin,
		},
		{
			Name:    "logout",
			Summary: "Remove the stored API token",
			Run:     runLogout,
		},
		{
			Name:        "whoami",
			Summary:     "Show the identity behind the current token",
			NeedsClient: true,
			Run:         runWhoAmI,
		},
	}
}

// runLogin resolves a server and token, verifies them, then persists both.
func runLogin(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("login takes no positional arguments")
	}

	server := firstNonEmpty(ctx.Globals.Server, ctx.Config.Server.Primary)
	if server == "" {
		if !ctx.Interactive {
			return &api.Error{Kind: api.KindConfig, Message: "no server configured; pass --server URL"}
		}
		answer, err := Prompt(ctx, "Server URL", "")
		if err != nil {
			return err
		}
		server = answer
	}
	if err := api.ValidateServerURL(server); err != nil {
		return err
	}
	ctx.Config.Server.Primary = server

	token, err := loginToken(ctx)
	if err != nil {
		return err
	}
	if auth.IsAgentToken(token) {
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

	env, err := client.Get(ctx.Ctx, WhoAmIPath, nil)
	if err != nil {
		return err
	}

	if discovered, err := client.Autodiscover(ctx.Ctx); err == nil {
		applyAutodiscover(ctx.Config, discovered)
	}

	if err := auth.Store(token); err != nil {
		return err
	}
	// The credential lives in the 0600 token file only; cli.yml keeps the
	// server list so a config copied elsewhere can never carry a token.
	ctx.Config.Auth.Token = ""
	if err := settings.Save(ctx.ConfigPath, ctx.Config); err != nil {
		return err
	}

	ctx.Out.Success("logged in to %s as %s", server, whoamiName(env))
	return nil
}

// loginToken collects the credential from flags, the environment or a
// prompt.
func loginToken(ctx *Context) (string, error) {
	resolved, err := auth.Resolve(auth.Options{
		Flag:            ctx.Globals.Token,
		FlagFile:        ctx.Globals.TokenFile,
		ConfigTokenFile: ctx.Config.Auth.TokenFile,
	})
	if err == nil && !resolved.Empty() {
		return resolved.Value, nil
	}

	if !ctx.Interactive {
		return "", &api.Error{
			Kind:    api.KindConfig,
			Message: "no token supplied; pass --token, --token-file, or set the environment variable " + auth.EnvVar,
		}
	}
	return PromptSecret(ctx, "API token")
}

// runLogout clears the stored credential.
func runLogout(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("logout takes no positional arguments")
	}
	if err := auth.Clear(); err != nil {
		return err
	}
	if ctx.Config.Auth.Token != "" {
		ctx.Config.Auth.Token = ""
		if err := settings.Save(ctx.ConfigPath, ctx.Config); err != nil {
			return err
		}
	}
	ctx.Out.Success("logged out")
	return nil
}

// runWhoAmI shows the identity the current token maps to.
func runWhoAmI(ctx *Context, args []string) error {
	if len(args) > 0 {
		return usagef("whoami takes no positional arguments")
	}
	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Get(ctx.Ctx, WhoAmIPath, nil)
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}

// whoamiName extracts a display name from the identity payload without
// failing the command when the shape is unexpected.
func whoamiName(env *api.Envelope) string {
	var identity map[string]any
	if err := env.Decode(&identity); err != nil {
		return "the current token owner"
	}
	for _, key := range []string{"username", "name", "email", "id"} {
		if value := output.Stringify(identity[key]); value != "" {
			return value
		}
	}
	return "the current token owner"
}

// applyAutodiscover copies the discovered server list into the config.
func applyAutodiscover(cfg *settings.Config, discovered *api.Autodiscover) {
	if discovered.Primary != "" {
		cfg.Server.Primary = discovered.Primary
	}
	cfg.Server.Cluster = discovered.Cluster
	if discovered.APIVersion != "" {
		cfg.Server.APIVersion = discovered.APIVersion
	}
	if discovered.AdminPath != "" {
		cfg.Server.AdminPath = discovered.AdminPath
	}
}

// firstNonEmpty returns the first value that is not blank.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
