package hosting

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// defaultMaxBodyMB is the upload ceiling written into a generated server block.
const defaultMaxBodyMB = 64

// defaultPHPSocketDir is where the PHP-FPM pools listen when the deployment
// does not override it. One socket per version supports the multi-version
// PHP-FPM layout IDEA.md requires.
const defaultPHPSocketDir = "/run/php"

// siteDocRootDir is the publicly served directory inside a site tree.
const siteDocRootDir = "public"

// siteLogDir holds the per-site nginx access and error logs.
const siteLogDir = "logs"

// nginxService names the subsystem in rollback errors and audit entries.
const nginxService = "web server"

// CreateSiteRequest is the tenant-supplied description of a new site.
type CreateSiteRequest struct {
	// Name is the tenant-unique short name; it also names the site directory.
	Name string
	// PrimaryDomain is the main hostname; it must be verified by the tenant.
	PrimaryDomain string
	// Aliases are additional hostnames, each verified by the tenant.
	Aliases []string
	// PHPVersion selects a PHP-FPM pool, or "none" for a static site.
	PHPVersion string
	// TLS requests certificate issuance and an HTTPS server block.
	TLS bool
	// DiskQuotaMB caps the site tree size; zero means the plan default.
	DiskQuotaMB int64
	// BandwidthQuotaMB caps monthly transfer; zero means the plan default.
	BandwidthQuotaMB int64
	// GitRemote and GitBranch record the deployment source for the panel.
	GitRemote string
	GitBranch string
}

// UpdateSiteRequest carries the fields a tenant may change on a site. A nil
// field is left untouched, so a partial update cannot blank a value by accident.
type UpdateSiteRequest struct {
	Aliases          *[]string
	PHPVersion       *string
	TLS              *bool
	DiskQuotaMB      *int64
	BandwidthQuotaMB *int64
	GitRemote        *string
	GitBranch        *string
}

// SiteUsage reports the accounting figures the panel and billing display.
type SiteUsage struct {
	SiteID           string
	DiskUsedMB       int64
	DiskQuotaMB      int64
	BandwidthQuotaMB int64
	Files            int64
}

// CreateSite provisions a document root, generates the nginx server block, and
// activates it. The site is persisted only after nginx has accepted the
// configuration, so a rejected config never leaves a half-created site behind.
func (s *Service) CreateSite(ctx context.Context, tenantID string, req CreateSiteRequest) (Site, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Site{}, err
	}
	if err := ValidateName("name", req.Name); err != nil {
		return Site{}, err
	}
	primary, err := ValidateDomain(req.PrimaryDomain)
	if err != nil {
		return Site{}, err
	}
	aliases, err := normalizeAliases(primary, req.Aliases)
	if err != nil {
		return Site{}, err
	}
	php := req.PHPVersion
	if php == "" {
		php = s.defaultPHP
	}
	if err = ValidatePHPVersion(php); err != nil {
		return Site{}, err
	}
	if err = ValidateQuotaMB("disk_quota_mb", req.DiskQuotaMB); err != nil {
		return Site{}, err
	}
	if err = ValidateQuotaMB("bandwidth_quota_mb", req.BandwidthQuotaMB); err != nil {
		return Site{}, err
	}
	if err = ValidateGitRemote(req.GitRemote); err != nil {
		return Site{}, err
	}
	if err = ValidateGitBranch(req.GitBranch); err != nil {
		return Site{}, err
	}

	for _, d := range append([]string{primary}, aliases...) {
		if err = s.requireOwnedDomain(ctx, tenantID, d); err != nil {
			return Site{}, err
		}
	}

	existing, err := s.store.ListSites(ctx, tenantID)
	if err != nil {
		return Site{}, err
	}
	for _, site := range existing {
		if strings.EqualFold(site.Name, req.Name) {
			return Site{}, apperr.New(apperr.CodeConflict, 409, "a site with that name already exists")
		}
	}
	if err = s.checkQuota(ctx, tenantID, ResourceSites, int64(len(existing))); err != nil {
		return Site{}, err
	}
	if _, err = s.store.SiteByDomain(ctx, primary); err == nil {
		return Site{}, apperr.New(apperr.CodeConflict, 409, "that domain is already served by a site")
	} else if !apperr.Is(err, apperr.CodeNotFound) {
		return Site{}, err
	}

	now := s.now().UTC()
	site := Site{
		ID:               s.newID(),
		TenantID:         tenantID,
		Name:             req.Name,
		PrimaryDomain:    primary,
		Aliases:          aliases,
		DocRoot:          path(req.Name, siteDocRootDir),
		PHPVersion:       php,
		TLSEnabled:       req.TLS,
		Enabled:          true,
		DiskQuotaMB:      req.DiskQuotaMB,
		BandwidthQuotaMB: req.BandwidthQuotaMB,
		GitRemote:        req.GitRemote,
		GitBranch:        req.GitBranch,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err = s.provisionSiteTree(site); err != nil {
		return Site{}, err
	}
	if req.TLS && s.certs != nil {
		for _, d := range append([]string{primary}, aliases...) {
			if certErr := s.certs.AddDomain(ctx, d); certErr != nil {
				return Site{}, apperr.Wrap(certErr, apperr.CodeUnavailable, 503,
					"a certificate could not be requested for that domain")
			}
		}
	}
	if err = s.activateSite(ctx, site); err != nil {
		return Site{}, err
	}
	if err = s.store.CreateSite(ctx, site); err != nil {
		if plan, planErr := s.sitePlan(site, true); planErr == nil {
			if applyErr := s.apply(ctx, plan); applyErr != nil {
				return Site{}, applyErr
			}
		}
		return Site{}, err
	}

	s.audit(ctx, "hosting.site.create", tenantID, site.ID, "domain", primary, "php", php)
	return site, nil
}

// GetSite returns one site owned by the tenant.
func (s *Service) GetSite(ctx context.Context, tenantID, siteID string) (Site, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return Site{}, err
	}
	if err := ValidateID("site", siteID); err != nil {
		return Site{}, err
	}
	return s.store.GetSite(ctx, tenantID, siteID)
}

// ListSites returns every site owned by the tenant.
func (s *Service) ListSites(ctx context.Context, tenantID string) ([]Site, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return nil, err
	}
	return s.store.ListSites(ctx, tenantID)
}

// UpdateSite changes the mutable fields of a site and regenerates its config.
func (s *Service) UpdateSite(ctx context.Context, tenantID, siteID string, req UpdateSiteRequest) (Site, error) {
	site, err := s.GetSite(ctx, tenantID, siteID)
	if err != nil {
		return Site{}, err
	}

	if req.Aliases != nil {
		aliases, aliasErr := normalizeAliases(site.PrimaryDomain, *req.Aliases)
		if aliasErr != nil {
			return Site{}, aliasErr
		}
		for _, d := range aliases {
			if ownErr := s.requireOwnedDomain(ctx, tenantID, d); ownErr != nil {
				return Site{}, ownErr
			}
		}
		site.Aliases = aliases
	}
	if req.PHPVersion != nil {
		if err = ValidatePHPVersion(*req.PHPVersion); err != nil {
			return Site{}, err
		}
		site.PHPVersion = *req.PHPVersion
	}
	if req.TLS != nil {
		site.TLSEnabled = *req.TLS
	}
	if req.DiskQuotaMB != nil {
		if err = ValidateQuotaMB("disk_quota_mb", *req.DiskQuotaMB); err != nil {
			return Site{}, err
		}
		site.DiskQuotaMB = *req.DiskQuotaMB
	}
	if req.BandwidthQuotaMB != nil {
		if err = ValidateQuotaMB("bandwidth_quota_mb", *req.BandwidthQuotaMB); err != nil {
			return Site{}, err
		}
		site.BandwidthQuotaMB = *req.BandwidthQuotaMB
	}
	if req.GitRemote != nil {
		if err = ValidateGitRemote(*req.GitRemote); err != nil {
			return Site{}, err
		}
		site.GitRemote = *req.GitRemote
	}
	if req.GitBranch != nil {
		if err = ValidateGitBranch(*req.GitBranch); err != nil {
			return Site{}, err
		}
		site.GitBranch = *req.GitBranch
	}
	site.UpdatedAt = s.now().UTC()

	if site.TLSEnabled && s.certs != nil {
		for _, d := range append([]string{site.PrimaryDomain}, site.Aliases...) {
			if certErr := s.certs.AddDomain(ctx, d); certErr != nil {
				return Site{}, apperr.Wrap(certErr, apperr.CodeUnavailable, 503,
					"a certificate could not be requested for that domain")
			}
		}
	}
	if err = s.activateSite(ctx, site); err != nil {
		return Site{}, err
	}
	if err = s.store.UpdateSite(ctx, site); err != nil {
		return Site{}, err
	}
	s.audit(ctx, "hosting.site.update", tenantID, site.ID)
	return site, nil
}

// EnableSite regenerates and activates the server block for a disabled site.
func (s *Service) EnableSite(ctx context.Context, tenantID, siteID string) (Site, error) {
	return s.setSiteEnabled(ctx, tenantID, siteID, true)
}

// DisableSite removes the server block so the site stops being served while
// its content and configuration remain intact.
func (s *Service) DisableSite(ctx context.Context, tenantID, siteID string) (Site, error) {
	return s.setSiteEnabled(ctx, tenantID, siteID, false)
}

// setSiteEnabled is the shared enable/disable path.
func (s *Service) setSiteEnabled(ctx context.Context, tenantID, siteID string, enabled bool) (Site, error) {
	site, err := s.GetSite(ctx, tenantID, siteID)
	if err != nil {
		return Site{}, err
	}
	if site.Enabled == enabled {
		return site, nil
	}
	site.Enabled = enabled
	site.UpdatedAt = s.now().UTC()
	if err = s.activateSite(ctx, site); err != nil {
		return Site{}, err
	}
	if err = s.store.UpdateSite(ctx, site); err != nil {
		return Site{}, err
	}
	event := "hosting.site.disable"
	if enabled {
		event = "hosting.site.enable"
	}
	s.audit(ctx, event, tenantID, site.ID)
	return site, nil
}

// DeleteSite removes the server block, the stored row, and the site tree. It
// is destructive, so it requires an explicit confirmation and is audit-logged.
func (s *Service) DeleteSite(ctx context.Context, tenantID, siteID string, confirm bool) error {
	if err := requireConfirm(confirm); err != nil {
		return err
	}
	site, err := s.GetSite(ctx, tenantID, siteID)
	if err != nil {
		return err
	}

	plan, err := s.sitePlan(site, true)
	if err != nil {
		return err
	}
	if err = s.apply(ctx, plan); err != nil {
		return err
	}
	if err = s.store.DeleteSite(ctx, tenantID, siteID); err != nil {
		return err
	}
	if s.certs != nil {
		for _, d := range append([]string{site.PrimaryDomain}, site.Aliases...) {
			if certErr := s.certs.RemoveDomain(d); certErr != nil {
				return apperr.Wrap(certErr, apperr.CodeInternal, 500, "the certificate could not be released")
			}
		}
	}

	tree, err := s.tenantDir(DirSites, tenantID, site.Name)
	if err != nil {
		return err
	}
	if err = os.RemoveAll(tree); err != nil {
		return internalErr(err, "the site content could not be removed")
	}
	s.audit(ctx, "hosting.site.delete", tenantID, site.ID, "domain", site.PrimaryDomain)
	return nil
}

// RenderSiteConfig returns the nginx server block a site would be served with.
// It is the read-only view the panel shows and the fixture the tests compare.
func (s *Service) RenderSiteConfig(ctx context.Context, tenantID, siteID string) ([]byte, error) {
	site, err := s.GetSite(ctx, tenantID, siteID)
	if err != nil {
		return nil, err
	}
	return s.renderVhost(site)
}

// SiteUsage measures the site tree and reports it against the site's quotas.
func (s *Service) SiteUsage(ctx context.Context, tenantID, siteID string) (SiteUsage, error) {
	site, err := s.GetSite(ctx, tenantID, siteID)
	if err != nil {
		return SiteUsage{}, err
	}
	tree, err := s.tenantDir(DirSites, tenantID, site.Name)
	if err != nil {
		return SiteUsage{}, err
	}
	bytesUsed, files, err := treeUsage(tree)
	if err != nil {
		return SiteUsage{}, err
	}
	return SiteUsage{
		SiteID:           site.ID,
		DiskUsedMB:       bytesUsed / (1 << 20),
		DiskQuotaMB:      site.DiskQuotaMB,
		BandwidthQuotaMB: site.BandwidthQuotaMB,
		Files:            files,
	}, nil
}

// provisionSiteTree creates the document root, the log directory, and a
// placeholder index page, all under the tenant's own root.
func (s *Service) provisionSiteTree(site Site) error {
	docRoot, err := s.siteDocRoot(site)
	if err != nil {
		return err
	}
	logDir, err := s.siteLogDir(site)
	if err != nil {
		return err
	}
	if err = ensureDir(docRoot, docRootMode); err != nil {
		return err
	}
	if err = ensureDir(logDir, dirMode); err != nil {
		return err
	}
	index := filepath.Join(docRoot, "index.html")
	if _, statErr := os.Stat(index); errors.Is(statErr, os.ErrNotExist) {
		if err = os.WriteFile(index, []byte(placeholderPage), 0o644); err != nil {
			return internalErr(err, "the document root could not be prepared")
		}
	}
	return nil
}

// activateSite writes or removes the server block and reloads nginx.
func (s *Service) activateSite(ctx context.Context, site Site) error {
	plan, err := s.sitePlan(site, !site.Enabled)
	if err != nil {
		return err
	}
	return s.apply(ctx, plan)
}

// sitePlan builds the activation plan for a site.
func (s *Service) sitePlan(site Site, remove bool) (applyPlan, error) {
	confPath, err := s.siteConfigPath(site)
	if err != nil {
		return applyPlan{}, err
	}
	file := configFile{Path: confPath, Mode: configMode, Remove: remove}
	if !remove {
		content, renderErr := s.renderVhost(site)
		if renderErr != nil {
			return applyPlan{}, renderErr
		}
		file.Content = content
	}
	return applyPlan{
		Files:   []configFile{file},
		Check:   s.cmds.NginxCheck,
		Reload:  s.cmds.NginxReload,
		Service: nginxService,
	}, nil
}

// renderVhost produces the server block for a site. Every value reaching the
// template has been validated, and the template guards re-check each one.
func (s *Service) renderVhost(site Site) ([]byte, error) {
	docRoot, err := s.siteDocRoot(site)
	if err != nil {
		return nil, err
	}
	logDir, err := s.siteLogDir(site)
	if err != nil {
		return nil, err
	}
	names := append([]string{site.PrimaryDomain}, site.Aliases...)
	data := vhostData{
		SiteID:      site.ID,
		ServerNames: names,
		DocRoot:     docRoot,
		AccessLog:   filepath.Join(logDir, "access.log"),
		ErrorLog:    filepath.Join(logDir, "error.log"),
		MaxBodyMB:   defaultMaxBodyMB,
	}
	if site.PHPVersion != "" && site.PHPVersion != PHPVersionNone {
		if err = ValidatePHPVersion(site.PHPVersion); err != nil {
			return nil, err
		}
		data.PHP = true
		data.PHPSocket = s.phpSocketDir + "/php" + site.PHPVersion + "-fpm.sock"
	}
	if site.TLSEnabled && s.certificateReady(site.PrimaryDomain) {
		dir := filepath.Join(s.tlsDir, "ssl", "letsencrypt", site.PrimaryDomain)
		data.TLS = true
		data.CertPath = filepath.Join(dir, "fullchain.pem")
		data.KeyPath = filepath.Join(dir, "privkey.pem")
	}

	var buf bytes.Buffer
	if err = vhostTemplate.Execute(&buf, data); err != nil {
		return nil, apperr.Wrap(err, apperr.CodeValidation, 422, "the site configuration could not be generated")
	}
	return buf.Bytes(), nil
}

// certificateReady reports whether a usable certificate exists for a domain.
// Until one does, the site is served over plain HTTP so the ACME challenge can
// complete instead of nginx refusing to start on a missing certificate file.
func (s *Service) certificateReady(domain string) bool {
	if s.certs == nil || s.tlsDir == "" {
		return false
	}
	_, ok := s.certs.CertificateFor(domain)
	return ok
}

// siteDir resolves the absolute tenant-scoped directory tree of a site.
func (s *Service) siteDir(site Site) (string, error) {
	if err := ValidateName("name", site.Name); err != nil {
		return "", err
	}
	return s.tenantDir(DirSites, site.TenantID, site.Name)
}

// siteDocRoot resolves the absolute document root of a site.
func (s *Service) siteDocRoot(site Site) (string, error) {
	return s.tenantDir(DirSites, site.TenantID, site.DocRoot)
}

// siteLogDir resolves the absolute log directory of a site.
func (s *Service) siteLogDir(site Site) (string, error) {
	return s.tenantDir(DirSites, site.TenantID, path(site.Name, siteLogDir))
}

// siteConfigPath resolves the generated server block path of a site.
func (s *Service) siteConfigPath(site Site) (string, error) {
	if err := ValidateID("tenant", site.TenantID); err != nil {
		return "", err
	}
	if err := ValidateName("name", site.Name); err != nil {
		return "", err
	}
	return s.systemPath(DirNginx, site.TenantID, site.Name+".conf")
}

// normalizeAliases validates every alias, lowercases it, drops duplicates, and
// refuses an alias that repeats the primary domain.
func normalizeAliases(primary string, raw []string) ([]string, error) {
	if len(raw) > maxSiteAliases {
		return nil, invalid("aliases", "too many aliases")
	}
	seen := map[string]bool{primary: true}
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		if strings.TrimSpace(a) == "" {
			continue
		}
		domain, err := ValidateDomain(a)
		if err != nil {
			return nil, err
		}
		if seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	return out, nil
}

// treeUsage sums the apparent size and the file count of a directory tree.
// A missing tree reports zero rather than failing, because usage is also read
// by the scheduled sweep while a site is being deleted.
func treeUsage(root string) (int64, int64, error) {
	var total, files int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			if errors.Is(infoErr, os.ErrNotExist) {
				return nil
			}
			return infoErr
		}
		total += info.Size()
		files++
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, internalErr(err, "disk usage could not be measured")
	}
	return total, files, nil
}

// maxSiteAliases caps how many extra hostnames one server block may carry.
const maxSiteAliases = 32

// placeholderPage is written into a fresh document root so a new site answers
// with a valid page instead of a directory listing or a 404.
const placeholderPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site ready</title>
</head>
<body>
<h1>Site ready</h1>
<p>Upload your content to replace this page.</p>
</body>
</html>
`
