package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/client/urlutil"
)

// AutodiscoverRefreshInterval is how often the cached cluster list is
// refreshed, per AI.md PART 33.
const AutodiscoverRefreshInterval = 30 * time.Minute

// PromoteAfter is how long the primary must stay unreachable before an
// alternate cluster member is promoted in cli.yml.
const PromoteAfter = 5 * time.Minute

// CLIBuild describes one published CLI artifact for an os-arch pair.
type CLIBuild struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	URL     string `json:"url"`
}

// Autodiscover is the /api/autodiscover document.
type Autodiscover struct {
	Primary      string              `json:"primary"`
	Cluster      []string            `json:"cluster"`
	APIVersion   string              `json:"api_version"`
	CLIVersions  map[string]CLIBuild `json:"cli_versions"`
	CLIMinVer    string              `json:"cli_min_version"`
	AdminPath    string              `json:"admin_path"`
	ServerName   string              `json:"server_name"`
	Capabilities []string            `json:"capabilities"`
}

// Autodiscover fetches the unversioned discovery document. The endpoint is
// public, so it works before a token exists.
func (c *Client) Autodiscover(ctx context.Context) (*Autodiscover, error) {
	env, err := c.Do(ctx, Request{Method: http.MethodGet, Path: AutodiscoverPath})
	if err != nil {
		return nil, err
	}

	doc := &Autodiscover{}
	if err := env.Decode(doc); err != nil {
		return nil, err
	}
	doc.normalize()
	return doc, nil
}

// normalize trims and validates the URLs the server handed back. The panel
// is a trust boundary for the CLI too: a malformed or non-http entry is
// dropped rather than dialled.
func (d *Autodiscover) normalize() {
	d.Primary = urlutil.NormalizeBase(d.Primary)
	if !IsValidServerURL(d.Primary) {
		d.Primary = ""
	}

	cluster := make([]string, 0, len(d.Cluster))
	seen := make(map[string]bool, len(d.Cluster))
	for _, member := range d.Cluster {
		normalized := urlutil.NormalizeBase(member)
		if normalized == "" || seen[normalized] || !IsValidServerURL(normalized) {
			continue
		}
		seen[normalized] = true
		cluster = append(cluster, normalized)
	}
	d.Cluster = cluster

	d.APIVersion = strings.TrimSpace(d.APIVersion)
	d.CLIMinVer = strings.TrimSpace(d.CLIMinVer)
	d.AdminPath = strings.TrimSpace(strings.Trim(d.AdminPath, "/"))
}

// BuildFor returns the published artifact for an os-arch key such as
// "linux-amd64".
func (d *Autodiscover) BuildFor(osArch string) (CLIBuild, bool) {
	build, ok := d.CLIVersions[osArch]
	if !ok || strings.TrimSpace(build.Version) == "" {
		return CLIBuild{}, false
	}
	return build, true
}

// Servers returns the discovered primary followed by every cluster member.
func (d *Autodiscover) Servers() []string {
	servers := make([]string, 0, len(d.Cluster)+1)
	if d.Primary != "" {
		servers = append(servers, d.Primary)
	}
	servers = append(servers, d.Cluster...)
	return servers
}
