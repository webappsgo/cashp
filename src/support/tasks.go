package support

import (
	"context"

	"github.com/webappsgo/cashp/src/scheduler"
)

// Task names registered with the shared scheduler. The support package never
// starts a goroutine of its own: everything periodic runs here.
const (
	TaskSLASweep   = "support-sla-sweep"
	TaskAutoClose  = "support-auto-close"
	TaskChatReaper = "support-chat-reaper"
)

// RegisterTasks adds the support subsystem's periodic work to the shared
// scheduler. Each task is cluster-wide, because running it on two nodes at once
// would send duplicate warnings and close the same tickets twice.
func (s *Service) RegisterTasks(sched *scheduler.Scheduler) error {
	if sched == nil {
		return nil
	}
	tasks := []scheduler.Task{
		{
			Name:        TaskSLASweep,
			Title:       "Support SLA sweep",
			Description: "Warns the assigned agent when a ticket passes its escalation point.",
			Schedule:    "@every 5m",
			ClusterWide: true,
			Run:         s.runSLASweep,
		},
		{
			Name:        TaskAutoClose,
			Title:       "Support auto-close",
			Description: "Closes resolved tickets the customer has not replied to.",
			Schedule:    "@every 15m",
			ClusterWide: true,
			Run:         s.runAutoClose,
		},
		{
			Name:        TaskChatReaper,
			Title:       "Support chat reaper",
			Description: "Ends chats that have been abandoned or left idle.",
			Schedule:    "@every 5m",
			ClusterWide: true,
			Run:         s.runChatReaper,
		},
	}
	for _, t := range tasks {
		if err := sched.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// systemIdentity is the actor used for the scheduler's own state changes. It
// carries no user, so an automatic change is never attributed to a person.
var systemIdentity = Identity{DisplayName: "system"}

// runSLASweep warns on tickets that have passed their escalation point. The
// warning is sent once per ticket: the audit log is what remembers that.
func (s *Service) runSLASweep(ctx context.Context) error {
	tickets, err := s.store.TicketsInState(ctx, StateOpen, StateAssigned, StateInProgress,
		StateAwaitingUser, StateAwaitingAgent)
	if err != nil {
		return err
	}
	policies, err := s.SLAPolicies(ctx)
	if err != nil {
		return err
	}

	at := s.nowUnix()
	for _, t := range tickets {
		policy, ok := policies[t.Priority]
		if !ok {
			policy = policies[PriorityNormal]
		}
		risk := EvaluateSLA(t, policy, at)
		if !risk.Escalate {
			continue
		}
		warned, err := s.store.HasAudit(ctx, "ticket", t.ID, "ticket.sla_warning")
		if err != nil {
			return err
		}
		if warned {
			continue
		}
		if err := s.audit(ctx, systemIdentity, AuditEntry{
			OrgID:      t.OrgID,
			Action:     "ticket.sla_warning",
			EntityType: "ticket",
			EntityID:   t.ID,
			Detail:     risk.Level,
		}); err != nil {
			return err
		}
		if t.AssignedTo != 0 {
			s.notify(ctx, "support.ticket.sla_warning", t.AssignedTo, map[string]string{
				"ticket_number": t.Number,
				"level":         risk.Level,
			})
		}
	}
	return nil
}

// runAutoClose closes resolved tickets the customer has left alone past the
// configured window. The customer can still reopen them afterwards.
func (s *Service) runAutoClose(ctx context.Context) error {
	hours := s.settingInt(ctx, SettingAutoCloseHours)
	if hours <= 0 {
		hours = int(AutoCloseAfter.Hours())
	}
	cutoff := s.nowUnix() - int64(hours)*3600

	tickets, err := s.store.TicketsInState(ctx, StateResolved)
	if err != nil {
		return err
	}
	for _, t := range tickets {
		if t.ResolvedAt == 0 || t.ResolvedAt > cutoff {
			continue
		}
		if _, err := s.applyTransition(ctx, systemIdentity, t, StateClosed, ActorSystem,
			"closed automatically after no reply"); err != nil {
			return err
		}
	}
	return nil
}

// runChatReaper ends conversations nobody is in any more: a queued chat the
// customer walked away from, and an active chat that has gone quiet.
func (s *Service) runChatReaper(ctx context.Context) error {
	sessions, err := s.store.ChatSessionsByStatus(ctx, ChatQueued, ChatActive)
	if err != nil {
		return err
	}
	at := s.nowUnix()
	idleQueue := int64(AwayAfter.Seconds())
	idleActive := idleQueue * 2

	for _, session := range sessions {
		last := session.LastEventAt
		if last == 0 {
			last = session.QueuedAt
		}
		idle := at - last
		switch {
		case session.Status == ChatQueued && idle >= idleQueue:
			session.Status = ChatAbandoned
		case session.Status == ChatActive && idle >= idleActive:
			session.Status = ChatClosed
		default:
			continue
		}

		from := ChatQueued
		if session.Status == ChatClosed {
			from = ChatActive
		}
		session.EndedAt = at
		session.LastEventAt = at
		if err := s.store.UpdateChatSession(ctx, session); err != nil {
			return err
		}
		if err := s.audit(ctx, systemIdentity, AuditEntry{
			OrgID:      session.OrgID,
			Action:     "chat.reap",
			EntityType: "chat",
			EntityID:   session.ID,
			FromState:  from,
			ToState:    session.Status,
		}); err != nil {
			return err
		}
	}
	return nil
}
