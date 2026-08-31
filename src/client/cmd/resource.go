package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/webappsgo/cashp/src/client/api"
	"github.com/webappsgo/cashp/src/client/output"
	"github.com/webappsgo/cashp/src/config"
)

// Action is a non-CRUD operation on a resource, such as "sites restart".
type Action struct {
	// Name is the subcommand word.
	Name string
	// Summary is the help line.
	Summary string
	// Method is the HTTP method; empty means POST.
	Method string
	// Suffix is appended after the resource id.
	Suffix string
	// Collection makes the action operate on the collection instead of a
	// single item, so no id argument is required.
	Collection bool
}

// Resource describes one API resource exposed as a CLI command group.
type Resource struct {
	// Name is the command word, e.g. "sites".
	Name string
	// Path is the API path segment; defaults to Name.
	Path string
	// Summary is the help line.
	Summary string
	// Columns are the fields shown by the table renderer for list output.
	Columns []string
	// Actions are extra verbs beyond list/get/create/update/delete.
	Actions []Action
	// ReadOnly removes create/update/delete.
	ReadOnly bool
	// AdminOnly routes the resource through the admin namespace always.
	AdminOnly bool
}

// Resources is the full set of panel resources reachable from the CLI.
// IDEA.md requires that every panel action is also reachable through the
// API, so every panel resource has a command here.
var Resources = []Resource{
	{
		Name:    "sites",
		Summary: "Manage virtual hosts and websites",
		Columns: []string{"id", "domain", "php_version", "ssl", "status"},
		Actions: []Action{
			{Name: "enable", Summary: "Enable a site"},
			{Name: "disable", Summary: "Disable a site"},
			{Name: "restart", Summary: "Restart a site's web workers"},
			{Name: "deploy", Summary: "Trigger a git deploy"},
			{Name: "logs", Summary: "Show recent site logs", Method: http.MethodGet},
		},
	},
	{
		Name:    "apps",
		Summary: "Manage PaaS applications",
		Columns: []string{"id", "name", "runtime", "status", "replicas"},
		Actions: []Action{
			{Name: "start", Summary: "Start an application"},
			{Name: "stop", Summary: "Stop an application"},
			{Name: "restart", Summary: "Restart an application"},
			{Name: "deploy", Summary: "Deploy the current release"},
			{Name: "rollback", Summary: "Roll back to the previous release"},
			{Name: "logs", Summary: "Show recent application logs", Method: http.MethodGet},
		},
	},
	{
		Name:    "containers",
		Summary: "Manage Docker, Podman and Incus workloads",
		Columns: []string{"id", "name", "engine", "image", "status"},
		Actions: []Action{
			{Name: "start", Summary: "Start a container"},
			{Name: "stop", Summary: "Stop a container"},
			{Name: "restart", Summary: "Restart a container"},
			{Name: "logs", Summary: "Show recent container logs", Method: http.MethodGet},
		},
	},
	{
		Name:    "vms",
		Summary: "Manage libvirt/KVM virtual machines",
		Columns: []string{"id", "name", "arch", "vcpus", "memory", "status"},
		Actions: []Action{
			{Name: "start", Summary: "Start a virtual machine"},
			{Name: "stop", Summary: "Stop a virtual machine"},
			{Name: "restart", Summary: "Restart a virtual machine"},
			{Name: "console", Summary: "Show console connection details", Method: http.MethodGet},
			{Name: "migrate", Summary: "Live-migrate to another node"},
		},
	},
	{
		Name:    "databases",
		Summary: "Manage database instances and users",
		Columns: []string{"id", "name", "engine", "version", "status"},
		Actions: []Action{
			{Name: "backup", Summary: "Back up a database"},
			{Name: "restore", Summary: "Restore a database from a backup"},
			{Name: "users", Summary: "List database users", Method: http.MethodGet},
		},
	},
	{
		Name:    "mailboxes",
		Summary: "Manage mailboxes and aliases",
		Columns: []string{"id", "address", "domain", "quota", "status"},
		Actions: []Action{
			{Name: "aliases", Summary: "List aliases for a mailbox", Method: http.MethodGet},
			{Name: "reset-password", Summary: "Reset a mailbox password"},
		},
	},
	{
		Name:    "domains",
		Summary: "Manage hosted domains",
		Columns: []string{"id", "name", "registrar", "expires_at", "status"},
		Actions: []Action{
			{Name: "verify", Summary: "Verify domain ownership"},
		},
	},
	{
		Name:    "dns",
		Path:    "dns/zones",
		Summary: "Manage authoritative DNS zones and records",
		Columns: []string{"id", "zone", "records", "dnssec", "status"},
		Actions: []Action{
			{Name: "records", Summary: "List records in a zone", Method: http.MethodGet},
			{Name: "sign", Summary: "Enable or re-sign DNSSEC for a zone"},
			{Name: "export", Summary: "Export a zone file", Method: http.MethodGet},
		},
	},
	{
		Name:    "certificates",
		Summary: "Manage TLS certificates",
		Columns: []string{"id", "domain", "issuer", "expires_at", "status"},
		Actions: []Action{
			{Name: "renew", Summary: "Renew a certificate"},
		},
	},
	{
		Name:    "backups",
		Summary: "Manage backups and restores",
		Columns: []string{"id", "target", "size", "created_at", "status"},
		Actions: []Action{
			{Name: "restore", Summary: "Restore from a backup"},
			{Name: "verify", Summary: "Verify a backup's integrity"},
			{Name: "run", Summary: "Run the backup schedule now", Collection: true},
		},
	},
	{
		Name:    "jobs",
		Summary: "Inspect background jobs",
		Columns: []string{"id", "kind", "state", "progress", "started_at"},
		Actions: []Action{
			{Name: "cancel", Summary: "Cancel a running job"},
			{Name: "logs", Summary: "Show a job's log output", Method: http.MethodGet},
		},
	},
	{
		Name:    "nodes",
		Summary: "Inspect cluster nodes",
		Columns: []string{"id", "hostname", "role", "state", "version"},
		Actions: []Action{
			{Name: "drain", Summary: "Drain workloads from a node"},
			{Name: "resume", Summary: "Return a drained node to service"},
			{Name: "metrics", Summary: "Show node metrics", Method: http.MethodGet},
		},
	},
	{
		Name:    "agents",
		Summary: "Manage managed-node agents",
		Columns: []string{"id", "hostname", "os", "version", "last_seen", "status"},
		Actions: []Action{
			{Name: "approve", Summary: "Approve a pending agent enrollment"},
			{Name: "revoke", Summary: "Revoke an agent's token"},
			{Name: "tasks", Summary: "List tasks queued for an agent", Method: http.MethodGet},
		},
	},
	{
		Name:    "tokens",
		Summary: "Manage API tokens",
		Columns: []string{"id", "name", "prefix", "scope", "created_at", "last_used_at"},
		Actions: []Action{
			{Name: "revoke", Summary: "Revoke a token"},
		},
	},
	{
		Name:    "quotas",
		Summary: "Inspect resource quotas and usage",
		Columns: []string{"resource", "used", "limit", "unit"},
	},
	{
		Name:    "plans",
		Summary: "Inspect billing plans",
		Columns: []string{"id", "name", "price", "interval", "status"},
	},
	{
		Name:    "invoices",
		Summary: "Inspect invoices",
		Columns: []string{"id", "number", "total", "currency", "status", "issued_at"},
		Actions: []Action{
			{Name: "download", Summary: "Download an invoice document", Method: http.MethodGet},
			{Name: "pay", Summary: "Pay an outstanding invoice"},
		},
		ReadOnly: true,
	},
	{
		Name:    "billing",
		Summary: "Inspect subscription and balance state",
		Columns: []string{"field", "value"},
		Actions: []Action{
			{Name: "subscription", Summary: "Show the current subscription", Method: http.MethodGet, Collection: true},
			{Name: "balance", Summary: "Show the account balance", Method: http.MethodGet, Collection: true},
			{Name: "recharge", Summary: "Add credit to the account balance", Collection: true},
		},
		ReadOnly: true,
	},
	{
		Name:    "tickets",
		Summary: "Manage support tickets",
		Columns: []string{"id", "subject", "priority", "state", "updated_at"},
		Actions: []Action{
			{Name: "reply", Summary: "Reply to a ticket"},
			{Name: "close", Summary: "Close a ticket"},
			{Name: "reopen", Summary: "Reopen a closed ticket"},
		},
	},
	{
		Name:    "notifications",
		Summary: "Inspect and acknowledge notifications",
		Columns: []string{"id", "channel", "subject", "state", "created_at"},
		Actions: []Action{
			{Name: "read", Summary: "Mark a notification as read"},
			{Name: "read-all", Summary: "Mark every notification as read", Collection: true},
		},
		ReadOnly: true,
	},
	{
		Name:    "alerts",
		Summary: "Manage monitoring alerts",
		Columns: []string{"id", "name", "severity", "state", "triggered_at"},
		Actions: []Action{
			{Name: "acknowledge", Summary: "Acknowledge a firing alert"},
			{Name: "silence", Summary: "Silence an alert"},
		},
	},
	{
		Name:    "users",
		Summary: "Manage accounts under your scope",
		Columns: []string{"id", "username", "role", "state", "created_at"},
		Actions: []Action{
			{Name: "suspend", Summary: "Suspend an account"},
			{Name: "unsuspend", Summary: "Restore a suspended account"},
		},
	},
	{
		Name:    "orgs",
		Summary: "Manage organizations",
		Columns: []string{"id", "name", "members", "plan", "state"},
		Actions: []Action{
			{Name: "members", Summary: "List organization members", Method: http.MethodGet},
		},
	},
}

// buildResourceCommand turns a Resource into a command group with the CRUD
// verbs plus its declared actions.
func buildResourceCommand(resource Resource) *Command {
	group := &Command{
		Name:        resource.Name,
		Summary:     resource.Summary,
		NeedsClient: true,
	}

	group.Subcommands = append(group.Subcommands, &Command{
		Name:        "list",
		Summary:     "List " + resource.Name,
		Args:        "[key=value ...]",
		NeedsClient: true,
		Run: func(ctx *Context, args []string) error {
			return runResourceList(ctx, resource, args)
		},
	})

	group.Subcommands = append(group.Subcommands, &Command{
		Name:        "get",
		Summary:     "Show one item from " + resource.Name,
		Args:        "ID",
		NeedsClient: true,
		Run: func(ctx *Context, args []string) error {
			return runResourceGet(ctx, resource, args)
		},
	})

	if !resource.ReadOnly {
		group.Subcommands = append(group.Subcommands,
			&Command{
				Name:        "create",
				Summary:     "Create an item in " + resource.Name,
				Args:        "key=value [key=value ...]",
				NeedsClient: true,
				Run: func(ctx *Context, args []string) error {
					return runResourceCreate(ctx, resource, args)
				},
			},
			&Command{
				Name:        "update",
				Summary:     "Update an item in " + resource.Name,
				Args:        "ID key=value [key=value ...]",
				NeedsClient: true,
				Run: func(ctx *Context, args []string) error {
					return runResourceUpdate(ctx, resource, args)
				},
			},
			&Command{
				Name:        "delete",
				Summary:     "Delete an item from " + resource.Name,
				Args:        "ID",
				NeedsClient: true,
				Run: func(ctx *Context, args []string) error {
					return runResourceDelete(ctx, resource, args)
				},
			},
		)
	}

	for _, action := range resource.Actions {
		current := action
		args := "ID [key=value ...]"
		if current.Collection {
			args = "[key=value ...]"
		}
		group.Subcommands = append(group.Subcommands, &Command{
			Name:        current.Name,
			Summary:     current.Summary,
			Args:        args,
			NeedsClient: true,
			Run: func(ctx *Context, cmdArgs []string) error {
				return runResourceAction(ctx, resource, current, cmdArgs)
			},
		})
	}

	return group
}

// collectionPath resolves the API path for a resource collection, honouring
// --admin and --user scoping.
func collectionPath(ctx *Context, resource Resource) (string, map[string]string, error) {
	segment := resource.Path
	if segment == "" {
		segment = resource.Name
	}

	if resource.AdminOnly || ctx.Globals.Admin {
		return AdminResourcePath(ctx.Config.Server.AdminPath, segment), map[string]string{}, nil
	}

	scope, err := ResolveScope(ctx, ctx.Globals.User)
	if err != nil {
		return "", nil, err
	}
	path, params := scope.ResourcePath(segment)
	return path, params, nil
}

// runResourceList performs a filtered collection request.
func runResourceList(ctx *Context, resource Resource, args []string) error {
	path, params, err := collectionPath(ctx, resource)
	if err != nil {
		return err
	}

	query, err := parseQuery(args)
	if err != nil {
		return err
	}
	if limit := strings.TrimSpace(ctx.Globals.Limit); limit != "" {
		if _, err := strconv.Atoi(limit); err != nil {
			return usagef("--limit expects a number")
		}
		query["limit"] = limit
	} else if fallback := ctx.Config.Defaults["limit"]; fallback != "" {
		query["limit"] = fallback
	}

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Do(ctx.Ctx, api.Request{Path: client.VersionedPath(path), PathParams: params, Query: query})
	if err != nil {
		return err
	}

	return emitCollection(ctx, env, resource.Columns)
}

// runResourceGet fetches a single item by id.
func runResourceGet(ctx *Context, resource Resource, args []string) error {
	if len(args) != 1 {
		return usagef("%s get requires exactly one ID", resource.Name)
	}

	path, params, err := collectionPath(ctx, resource)
	if err != nil {
		return err
	}
	params["id"] = args[0]

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Do(ctx.Ctx, api.Request{Path: client.VersionedPath(path + "/{id}"), PathParams: params})
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}

// runResourceCreate posts a new item built from key=value pairs.
func runResourceCreate(ctx *Context, resource Resource, args []string) error {
	if len(args) == 0 {
		return usagef("%s create requires at least one key=value pair", resource.Name)
	}

	body, err := parseFields(args)
	if err != nil {
		return err
	}

	path, params, err := collectionPath(ctx, resource)
	if err != nil {
		return err
	}

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Do(ctx.Ctx, api.Request{
		Method:     http.MethodPost,
		Path:       client.VersionedPath(path),
		PathParams: params,
		Body:       body,
	})
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}

// runResourceUpdate patches an existing item.
func runResourceUpdate(ctx *Context, resource Resource, args []string) error {
	if len(args) < 2 {
		return usagef("%s update requires an ID and at least one key=value pair", resource.Name)
	}

	body, err := parseFields(args[1:])
	if err != nil {
		return err
	}

	path, params, err := collectionPath(ctx, resource)
	if err != nil {
		return err
	}
	params["id"] = args[0]

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Do(ctx.Ctx, api.Request{
		Method:     http.MethodPatch,
		Path:       client.VersionedPath(path + "/{id}"),
		PathParams: params,
		Body:       body,
	})
	if err != nil {
		return err
	}
	return emitItem(ctx, env)
}

// runResourceDelete removes an item, confirming first unless --yes was
// passed or the session is non-interactive.
func runResourceDelete(ctx *Context, resource Resource, args []string) error {
	if len(args) != 1 {
		return usagef("%s delete requires exactly one ID", resource.Name)
	}

	if !ctx.Globals.Yes {
		confirmed, err := Confirm(ctx, fmt.Sprintf("Delete %s %s?", strings.TrimSuffix(resource.Name, "s"), args[0]))
		if err != nil {
			return err
		}
		if !confirmed {
			ctx.Out.Message("aborted")
			return nil
		}
	}

	path, params, err := collectionPath(ctx, resource)
	if err != nil {
		return err
	}
	params["id"] = args[0]

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	env, err := client.Do(ctx.Ctx, api.Request{
		Method:     http.MethodDelete,
		Path:       client.VersionedPath(path + "/{id}"),
		PathParams: params,
	})
	if err != nil {
		return err
	}
	if len(env.Data) == 0 {
		ctx.Out.Success("deleted %s", args[0])
		return nil
	}
	return emitItem(ctx, env)
}

// runResourceAction performs a declared non-CRUD action.
func runResourceAction(ctx *Context, resource Resource, action Action, args []string) error {
	path, params, err := collectionPath(ctx, resource)
	if err != nil {
		return err
	}

	suffix := action.Suffix
	if suffix == "" {
		suffix = action.Name
	}

	fieldArgs := args
	if !action.Collection {
		if len(args) == 0 {
			return usagef("%s %s requires an ID", resource.Name, action.Name)
		}
		params["id"] = args[0]
		fieldArgs = args[1:]
		path += "/{id}"
	}
	path += "/" + suffix

	method := action.Method
	if method == "" {
		method = http.MethodPost
	}

	request := api.Request{Method: method, Path: "", PathParams: params}
	if method == http.MethodGet {
		query, err := parseQuery(fieldArgs)
		if err != nil {
			return err
		}
		request.Query = query
	} else if len(fieldArgs) > 0 {
		body, err := parseFields(fieldArgs)
		if err != nil {
			return err
		}
		request.Body = body
	}

	client, err := ctx.APIClient()
	if err != nil {
		return err
	}
	request.Path = client.VersionedPath(path)

	env, err := client.Do(ctx.Ctx, request)
	if err != nil {
		return err
	}
	if len(env.Data) == 0 {
		ctx.Out.Success("%s %s completed", resource.Name, action.Name)
		return nil
	}
	return emitAuto(ctx, env, resource.Columns)
}

// parseFields converts key=value arguments into a JSON object. A value of
// the form @path reads the value from a file, and a value that parses as
// JSON is embedded as structured data.
func parseFields(args []string) (map[string]any, error) {
	fields := make(map[string]any, len(args))
	for _, arg := range args {
		index := strings.Index(arg, "=")
		if index <= 0 {
			return nil, usagef("expected key=value, got %q", arg)
		}
		key := arg[:index]
		raw := arg[index+1:]

		if strings.HasPrefix(raw, "@") {
			data, err := os.ReadFile(strings.TrimPrefix(raw, "@"))
			if err != nil {
				return nil, usagef("could not read value file for %s", key)
			}
			raw = strings.TrimRight(string(data), "\n")
		}
		fields[key] = coerce(raw)
	}
	return fields, nil
}

// parseQuery converts key=value arguments into query parameters.
func parseQuery(args []string) (map[string]string, error) {
	query := make(map[string]string, len(args))
	for _, arg := range args {
		index := strings.Index(arg, "=")
		if index <= 0 {
			return nil, usagef("expected key=value, got %q", arg)
		}
		query[arg[:index]] = arg[index+1:]
	}
	return query, nil
}

// coerce converts a string field value to the most specific JSON type it
// clearly represents, leaving anything ambiguous as a string.
func coerce(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}
	if isPlainInteger(trimmed) {
		if number, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return number
		}
	}
	if config.IsTruthy(trimmed) {
		return true
	}
	if enabled, err := config.ParseBool(trimmed, true); err == nil && !enabled {
		return false
	}
	return raw
}

// isPlainInteger reports whether s is a decimal integer without a leading
// zero, so identifiers such as "007" stay strings.
func isPlainInteger(s string) bool {
	body := strings.TrimPrefix(s, "-")
	if body == "" || (len(body) > 1 && body[0] == '0') {
		return false
	}
	for _, char := range body {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// emitCollection renders a list payload as a table or structured document.
func emitCollection(ctx *Context, env *api.Envelope, columns []string) error {
	var items []map[string]any
	if err := env.Decode(&items); err != nil {
		return emitItem(ctx, env)
	}
	return ctx.Out.Emit(items, output.TableFromMaps(items, columns))
}

// emitItem renders a single-object payload.
func emitItem(ctx *Context, env *api.Envelope) error {
	var item map[string]any
	if err := env.Decode(&item); err != nil {
		var raw any
		if decodeErr := env.Decode(&raw); decodeErr != nil {
			return decodeErr
		}
		return ctx.Out.Emit(raw, output.Table{Headers: []string{"VALUE"}, Rows: [][]string{{output.Stringify(raw)}}})
	}
	return ctx.Out.Emit(item, output.TableFromMap(item))
}

// emitAuto renders either shape, choosing by the payload's JSON type.
func emitAuto(ctx *Context, env *api.Envelope, columns []string) error {
	trimmed := strings.TrimLeft(string(env.Data), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		return emitCollection(ctx, env, columns)
	}
	return emitItem(ctx, env)
}
