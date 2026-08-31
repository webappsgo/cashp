package dbservice

import (
	"context"
	"log/slog"
)

// Resource accounting. Limits are what the tenant was granted; usage is what
// the orchestration backend measures right now. Both are reported so the
// billing and admin layers can compare them without reaching into a container.

// Usage measures one instance against its configured envelope.
func (s *Service) Usage(ctx context.Context, tenantID, instanceID string) (*Usage, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	return s.measure(ctx, inst), nil
}

// Quota aggregates every live instance a tenant holds, so a plan check needs a
// single call rather than one per instance.
func (s *Service) Quota(ctx context.Context, tenantID string) (*QuotaReport, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	instances, err := s.store.ListInstances(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	report := &QuotaReport{
		TenantID:    tenantID,
		Instances:   len(instances),
		PerInstance: make([]Usage, 0, len(instances)),
		MeasuredAt:  s.now().UTC(),
	}
	for _, inst := range instances {
		databases, err := s.store.ListDatabases(ctx, inst.TenantID, inst.ID)
		if err != nil {
			return nil, err
		}
		report.Databases += len(databases)
		usage := s.measure(ctx, inst)
		report.CPUCores += usage.Limits.CPUCores
		report.MemoryBytes += usage.Limits.MemoryBytes
		report.DiskLimitBytes += usage.Limits.DiskBytes
		report.DiskUsedBytes += usage.DiskUsedBytes
		report.PerInstance = append(report.PerInstance, *usage)
	}
	return report, nil
}

// measure reads what the backend currently reports for one instance. A backend
// that cannot answer is not an error: the configured limits are still the
// truth, and the measured fields simply stay at zero.
func (s *Service) measure(ctx context.Context, inst *Instance) *Usage {
	usage := &Usage{
		InstanceID: inst.ID,
		Engine:     inst.Engine,
		Limits:     inst.Limits,
		MeasuredAt: s.now().UTC(),
	}
	if inst.ContainerID != "" {
		state, err := s.orch.Inspect(ctx, inst.ContainerID)
		if err != nil {
			s.log.Warn("managed database usage could not be read from the backend",
				slog.String("instance_id", inst.ID))
		} else {
			usage.Running = state.Running
			usage.MemoryUsedBytes = state.MemoryUsedBytes
			usage.CPUPercent = state.CPUPercent
		}
	}
	if inst.VolumeName != "" {
		used, err := s.orch.VolumeUsage(ctx, inst.VolumeName)
		if err != nil {
			s.log.Warn("managed database storage usage could not be read from the backend",
				slog.String("instance_id", inst.ID))
		} else {
			usage.DiskUsedBytes = used
		}
	}
	return usage
}
