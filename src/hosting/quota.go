package hosting

import (
	"context"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// bytesPerMB converts a measured byte total to the megabytes quotas use.
const bytesPerMB = 1 << 20

// TenantUsage is the accounted footprint of one tenant across every hosting
// service. Billing reads it to meter storage.
type TenantUsage struct {
	TenantID   string
	SiteDiskMB int64
	AppDiskMB  int64
	MailDiskMB int64
	TotalMB    int64
	Sites      int64
	Zones      int64
	Mailboxes  int64
	Apps       int64
}

// SweepUsage recomputes disk usage for every site and stores the result. It is
// the body of the quota sweep scheduler task.
func (s *Service) SweepUsage(ctx context.Context) error {
	sites, err := s.store.ListAllSites(ctx)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		root, dirErr := s.siteDir(site)
		if dirErr != nil {
			return dirErr
		}
		used, _, usageErr := treeUsage(root)
		if usageErr != nil {
			return usageErr
		}
		usedMB := used / bytesPerMB
		if usedMB == site.DiskUsedMB {
			continue
		}
		site.DiskUsedMB = usedMB
		site.UpdatedAt = s.now().UTC()
		if err = s.store.UpdateSite(ctx, site); err != nil {
			return err
		}
	}
	return nil
}

// TenantUsage reports the current footprint of one tenant.
func (s *Service) TenantUsage(ctx context.Context, tenantID string) (TenantUsage, error) {
	if err := ValidateID("tenant", tenantID); err != nil {
		return TenantUsage{}, err
	}
	usage := TenantUsage{TenantID: tenantID}

	siteRoot, err := s.tenantDir(DirSites, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	siteBytes, _, err := treeUsage(siteRoot)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.SiteDiskMB = siteBytes / bytesPerMB

	appRoot, err := s.tenantDir(DirApps, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	appBytes, _, err := treeUsage(appRoot)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.AppDiskMB = appBytes / bytesPerMB

	mailRoot, err := s.tenantDir(DirMail, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	mailBytes, _, err := treeUsage(mailRoot)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.MailDiskMB = mailBytes / bytesPerMB
	usage.TotalMB = usage.SiteDiskMB + usage.AppDiskMB + usage.MailDiskMB

	sites, err := s.store.ListSites(ctx, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.Sites = int64(len(sites))
	zones, err := s.store.ListZones(ctx, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.Zones = int64(len(zones))
	apps, err := s.store.ListApps(ctx, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.Apps = int64(len(apps))
	mailboxes, err := s.countMailboxes(ctx, tenantID)
	if err != nil {
		return TenantUsage{}, err
	}
	usage.Mailboxes = mailboxes
	return usage, nil
}

// EnforceDiskQuota rejects a write-side operation when a site is already over
// its own quota or the tenant is over its plan storage ceiling.
func (s *Service) EnforceDiskQuota(ctx context.Context, tenantID, siteID string) error {
	site, err := s.store.GetSite(ctx, tenantID, siteID)
	if err != nil {
		return err
	}
	root, err := s.siteDir(site)
	if err != nil {
		return err
	}
	used, _, err := treeUsage(root)
	if err != nil {
		return err
	}
	usedMB := used / bytesPerMB
	if site.DiskQuotaMB > 0 && usedMB >= site.DiskQuotaMB {
		return apperr.New(apperr.CodeQuotaExceeded, 429, "the site is at its storage limit").
			WithDetails(map[string]any{"resource": ResourceDiskMB, "limit": site.DiskQuotaMB})
	}
	usage, err := s.TenantUsage(ctx, tenantID)
	if err != nil {
		return err
	}
	return s.checkQuota(ctx, tenantID, ResourceDiskMB, usage.TotalMB)
}
