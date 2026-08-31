package dbservice

import (
	"context"
)

// Health probes one instance at the engine's own protocol level and records
// the result. A container that is merely running is never reported healthy:
// the engine has to answer its own client before that word is used.
func (s *Service) Health(ctx context.Context, tenantID, instanceID string) (*Health, error) {
	inst, err := s.live(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	return s.refreshHealth(ctx, inst)
}

// refreshHealth runs the probe and persists its outcome on the instance.
func (s *Service) refreshHealth(ctx context.Context, inst *Instance) (*Health, error) {
	now := s.now().UTC()
	if inst.State != StateRunning {
		inst.Health = HealthUnknown
		inst.HealthDetail = "The instance is not running."
		inst.HealthCheckedAt = now
		inst.UpdatedAt = now
		if err := s.store.UpdateInstance(ctx, inst); err != nil {
			return nil, err
		}
		return &Health{
			InstanceID: inst.ID,
			Engine:     inst.Engine,
			State:      inst.Health,
			Detail:     inst.HealthDetail,
			CheckedAt:  now,
		}, nil
	}
	a, c, err := s.engineContext(ctx, inst)
	if err != nil {
		return nil, err
	}
	state, detail := s.probe(ctx, a, c)
	inst.Health = state
	inst.HealthDetail = detail
	inst.HealthCheckedAt = now
	inst.UpdatedAt = now
	if err := s.store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	return &Health{
		InstanceID: inst.ID,
		Engine:     inst.Engine,
		State:      state,
		Detail:     detail,
		CheckedAt:  now,
	}, nil
}
