package api

import (
	"fmt"
	"html"
	"strings"
)

// pageCSS is the self-contained stylesheet used by the health, version, and
// documentation pages. These pages must render correctly even when the web
// frontend or its static assets are unavailable, so nothing is loaded from
// another package or another host. Dark is the default palette and light is
// served only when the visitor asks for it.
const pageCSS = `:root {
  --bg: #282a36;
  --bg-alt: #21222c;
  --fg: #f8f8f2;
  --muted: #6272a4;
  --accent: #8be9fd;
  --ok: #50fa7b;
  --warn: #ffb86c;
  --error: #ff5555;
  --purple: #bd93f9;
  --border: #44475a;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #ffffff;
    --bg-alt: #f4f4f6;
    --fg: #1a1a1a;
    --muted: #5a5a68;
    --accent: #0066cc;
    --ok: #008000;
    --warn: #ff8c00;
    --error: #cc0000;
    --purple: #6600cc;
    --border: #d5d5dd;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  padding: 1.5rem;
  background: var(--bg);
  color: var(--fg);
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  line-height: 1.5;
}
.container { max-width: 60rem; margin: 0 auto; }
h1 { font-size: 1.5rem; margin: 0 0 1rem; }
h2 { font-size: 1.1rem; margin: 0 0 0.75rem; color: var(--accent); }
a { color: var(--accent); }
.status-banner {
  border: 1px solid var(--border);
  border-left-width: 0.35rem;
  border-radius: 0.4rem;
  padding: 0.75rem 1rem;
  margin-bottom: 1.5rem;
  background: var(--bg-alt);
  font-weight: 600;
}
.status-healthy { border-left-color: var(--ok); color: var(--ok); }
.status-degraded, .status-restart_required { border-left-color: var(--warn); color: var(--warn); }
.status-unhealthy, .status-maintenance, .status-shutting_down { border-left-color: var(--error); color: var(--error); }
.section-card {
  background: var(--bg-alt);
  border: 1px solid var(--border);
  border-radius: 0.4rem;
  padding: 1rem;
  margin-bottom: 1rem;
}
.info-list { margin: 0; display: grid; grid-template-columns: minmax(8rem, 14rem) 1fr; gap: 0.35rem 1rem; }
.info-list dt { color: var(--muted); }
.info-list dd { margin: 0; word-break: break-word; }
.value-ok { color: var(--ok); }
.value-error { color: var(--error); }
.code-block {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 0.3rem;
  padding: 0.75rem;
  overflow-x: auto;
  white-space: pre;
  color: var(--fg);
}
.muted { color: var(--muted); }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border); vertical-align: top; }
th { color: var(--muted); font-weight: 600; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
@media (max-width: 40rem) {
  .info-list { grid-template-columns: 1fr; gap: 0.1rem; }
  .info-list dt { margin-top: 0.5rem; }
}`

// Page wraps rendered body markup in a complete, self-contained document
// with the shared stylesheet inlined. The swagger and graphql packages build
// their explorers on it so every server-owned page shares one palette.
func Page(title, body string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n  <head>\n")
	b.WriteString("    <meta charset=\"UTF-8\">\n")
	b.WriteString("    <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	fmt.Fprintf(&b, "    <title>%s</title>\n", html.EscapeString(title))
	b.WriteString("    <style>\n")
	b.WriteString(pageCSS)
	b.WriteString("\n    </style>\n  </head>\n  <body>\n    <main class=\"container\">\n")
	b.WriteString(body)
	b.WriteString("    </main>\n  </body>\n</html>\n")
	return b.String()
}

// infoRow renders one definition-list row of a section card.
func infoRow(label, value string) string {
	return fmt.Sprintf("          <dt>%s</dt>\n          <dd>%s</dd>\n",
		html.EscapeString(label), html.EscapeString(value))
}

// checkRow renders one component-check row, colouring ok and error states.
func checkRow(label, value string) string {
	class := "value-ok"
	if value != CheckOK {
		class = "value-error"
	}
	return fmt.Sprintf("          <dt>%s</dt>\n          <dd class=\"%s\">%s</dd>\n",
		html.EscapeString(label), class, html.EscapeString(value))
}

// sectionCard wraps definition-list rows in a titled card.
func sectionCard(title, rows string) string {
	var b strings.Builder
	b.WriteString("      <section class=\"section-card\">\n")
	fmt.Fprintf(&b, "        <h2>%s</h2>\n", html.EscapeString(title))
	b.WriteString("        <dl class=\"info-list\">\n")
	b.WriteString(rows)
	b.WriteString("        </dl>\n      </section>\n")
	return b.String()
}

// boolText renders a boolean the same way the JSON and text formats do.
func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// RenderHTML renders the health payload as a self-contained status page.
func (hr HealthResponse) RenderHTML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "      <h1>%s</h1>\n", html.EscapeString(hr.Project.Name))
	if hr.Project.Tagline != "" {
		fmt.Fprintf(&b, "      <p class=\"muted\">%s</p>\n", html.EscapeString(hr.Project.Tagline))
	}
	fmt.Fprintf(&b, "      <div class=\"status-banner status-%s\">%s</div>\n",
		html.EscapeString(hr.Status), html.EscapeString(statusHeadline(hr.Status)))

	project := infoRow("Name", hr.Project.Name) +
		infoRow("Tagline", hr.Project.Tagline) +
		infoRow("Description", hr.Project.Description)
	b.WriteString(sectionCard("Project", project))

	build := infoRow("Version", hr.Version) +
		infoRow("Go version", hr.GoVersion) +
		infoRow("Commit", hr.Build.Commit) +
		infoRow("Build date", hr.Build.Date)
	b.WriteString(sectionCard("Version & Build", build))

	runtimeRows := infoRow("Uptime", hr.Uptime) +
		infoRow("Mode", hr.Mode) +
		infoRow("Timestamp", hr.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
	if hr.PendingRestart {
		runtimeRows += infoRow("Pending restart", "true")
		runtimeRows += infoRow("Restart reason", strings.Join(hr.RestartReason, ", "))
	}
	b.WriteString(sectionCard("Runtime", runtimeRows))

	cluster := infoRow("Enabled", boolText(hr.Cluster.Enabled))
	if hr.Cluster.Enabled {
		cluster += infoRow("Status", hr.Cluster.Status) +
			infoRow("Primary", hr.Cluster.Primary) +
			infoRow("Nodes", strings.Join(hr.Cluster.Nodes, ", ")) +
			infoRow("Node count", fmt.Sprintf("%d", hr.Cluster.NodeCount)) +
			infoRow("Role", hr.Cluster.Role)
	}
	b.WriteString(sectionCard("Cluster", cluster))

	features := infoRow("Tor", overlayText(hr.Features.Tor.Enabled, hr.Features.Tor.Running, hr.Features.Tor.Status))
	if hr.Features.Tor.Enabled && hr.Features.Tor.Hostname != "" {
		features += infoRow("Tor hostname", hr.Features.Tor.Hostname)
	}
	features += infoRow("I2P", overlayText(hr.Features.I2P.Enabled, hr.Features.I2P.Running, hr.Features.I2P.Status))
	if hr.Features.I2P.Enabled && hr.Features.I2P.Hostname != "" {
		features += infoRow("I2P hostname", hr.Features.I2P.Hostname)
	}
	features += infoRow("GeoIP", boolText(hr.Features.GeoIP)) +
		infoRow("Multi user", boolText(hr.Features.MultiUser)) +
		infoRow("Organizations", boolText(hr.Features.Organizations)) +
		infoRow("Custom domains", boolText(hr.Features.CustomDomains))
	for _, p := range extraPairs(hr.Features.Extra) {
		features += infoRow(p.Key, fmt.Sprintf("%v", p.Value))
	}
	b.WriteString(sectionCard("Features", features))

	checks := checkRow("Database", hr.Checks.Database) +
		checkRow("Cache", hr.Checks.Cache) +
		checkRow("Disk", hr.Checks.Disk) +
		checkRow("Scheduler", hr.Checks.Scheduler)
	for _, opt := range []struct{ label, value string }{
		{"Cluster", hr.Checks.Cluster},
		{"Tor", hr.Checks.Tor},
		{"I2P", hr.Checks.I2P},
	} {
		if opt.value != "" {
			checks += checkRow(opt.label, opt.value)
		}
	}
	for _, k := range sortedCheckKeys(hr.Checks.Extra) {
		checks += checkRow(k, hr.Checks.Extra[k])
	}
	b.WriteString(sectionCard("Checks", checks))

	stats := infoRow("Requests total", fmt.Sprintf("%d", hr.Stats.RequestsTotal)) +
		infoRow("Requests 24h", fmt.Sprintf("%d", hr.Stats.Requests24h)) +
		infoRow("Active connections", fmt.Sprintf("%d", hr.Stats.ActiveConns))
	for _, p := range extraPairs(hr.Stats.Extra) {
		stats += infoRow(p.Key, fmt.Sprintf("%v", p.Value))
	}
	b.WriteString(sectionCard("Statistics", stats))

	return Page(hr.Project.Name+" - Health Status", b.String())
}

// sortedCheckKeys returns app-specific check names in a stable order.
func sortedCheckKeys(m map[string]string) []string {
	converted := make(map[string]any, len(m))
	for k, v := range m {
		converted[k] = v
	}
	pairs := extraPairs(converted)
	keys := make([]string, 0, len(pairs))
	for _, p := range pairs {
		keys = append(keys, p.Key)
	}
	return keys
}

// overlayText summarises an overlay network state for the HTML view.
func overlayText(enabled, running bool, status string) string {
	if !enabled {
		return "disabled"
	}
	if status != "" {
		return status
	}
	if running {
		return "running"
	}
	return "stopped"
}

// statusHeadline turns a status value into the banner sentence.
func statusHeadline(status string) string {
	switch status {
	case StatusHealthy:
		return "All systems operational"
	case StatusDegraded:
		return "Degraded - a non-critical component is failing"
	case StatusRestartRequired:
		return "Restart required to apply configuration changes"
	case StatusUnhealthy:
		return "Unhealthy - a critical component is failing"
	case StatusMaintenance:
		return "Maintenance mode"
	case StatusShuttingDown:
		return "Shutting down"
	default:
		return status
	}
}

// RenderHTML renders the version payload as a self-contained page.
func (v VersionResponse) RenderHTML() string {
	rows := infoRow("Name", v.Name) +
		infoRow("Version", v.Version) +
		infoRow("Channel", v.Channel) +
		infoRow("Commit", v.Commit) +
		infoRow("Build epoch", v.BuildEpoch) +
		infoRow("Build date", v.BuildDate) +
		infoRow("Go version", v.GoVersion)
	body := fmt.Sprintf("      <h1>%s</h1>\n", html.EscapeString(v.Name+" version")) +
		sectionCard("Build", rows)
	return Page(v.Name+" - Version", body)
}
