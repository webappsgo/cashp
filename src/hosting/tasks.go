package hosting

import (
	"context"

	"github.com/webappsgo/cashp/src/scheduler"
)

// Task names registered with src/scheduler. They are stable identifiers: the
// scheduler persists enabled state and last-run times under them.
const (
	TaskQuotaSweep      = "hosting_quota_sweep"
	TaskReleaseCleanup  = "hosting_release_cleanup"
	TaskDNSResync       = "hosting_dns_resync"
	TaskCertificateSync = "hosting_certificate_sync"
)

// Tasks returns the recurring work this package needs. The caller registers
// them with the scheduler; this package never starts a timer of its own.
func (s *Service) Tasks() []scheduler.Task {
	return []scheduler.Task{
		{
			Name:        TaskQuotaSweep,
			Title:       "Hosting quota sweep",
			Description: "Recomputes site disk usage so plan limits are enforced against measured storage.",
			Schedule:    "@hourly",
			ClusterWide: false,
			CatchUp:     true,
			Run:         s.SweepUsage,
		},
		{
			Name:        TaskReleaseCleanup,
			Title:       "PaaS release cleanup",
			Description: "Removes superseded application releases past the retention count.",
			Schedule:    "@daily",
			ClusterWide: true,
			CatchUp:     true,
			Run:         s.CleanupReleases,
		},
		{
			Name:        TaskDNSResync,
			Title:       "DNS zone resync",
			Description: "Rewrites any zone file on this node that drifted from the stored zone state.",
			Schedule:    "@every 15m",
			ClusterWide: false,
			CatchUp:     true,
			Run:         s.ResyncZones,
		},
		{
			Name:        TaskCertificateSync,
			Title:       "Site certificate sync",
			Description: "Re-renders TLS-enabled sites once a certificate has been issued or renewed.",
			Schedule:    "@every 15m",
			ClusterWide: false,
			CatchUp:     true,
			Run:         s.SyncCertificates,
		},
	}
}

// SyncCertificates re-renders every TLS site whose generated server block no
// longer matches the certificate state, so a vhost starts serving HTTPS as
// soon as src/tlsmgr has issued its certificate.
func (s *Service) SyncCertificates(ctx context.Context) error {
	if s.certs == nil {
		return nil
	}
	sites, err := s.store.ListAllSites(ctx)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !site.Enabled || !site.TLSEnabled {
			continue
		}
		desired, renderErr := s.renderVhost(site)
		if renderErr != nil {
			return renderErr
		}
		confPath, pathErr := s.siteConfigPath(site)
		if pathErr != nil {
			return pathErr
		}
		current, readErr := readFileIfExists(confPath)
		if readErr != nil {
			return readErr
		}
		if string(current) == string(desired) {
			continue
		}
		if err = s.activateSite(ctx, site); err != nil {
			return err
		}
	}
	return nil
}
