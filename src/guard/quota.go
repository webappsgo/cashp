package guard

import (
	"math"
	"strconv"
	"sync"
	"time"
)

// ResourceKind names a metered resource a billing plan sets an allowance
// for. Plans gate quantity, never which of these exist: every tier runs
// the same code path with a different number.
type ResourceKind string

// The metered resources.
const (
	// QuotaSites limits hosted virtual hosts.
	QuotaSites ResourceKind = "sites"
	// QuotaApps limits PaaS applications.
	QuotaApps ResourceKind = "apps"
	// QuotaContainers limits tenant container workloads.
	QuotaContainers ResourceKind = "containers"
	// QuotaVMs limits tenant virtual machines.
	QuotaVMs ResourceKind = "vms"
	// QuotaDatabases limits provisioned database instances.
	QuotaDatabases ResourceKind = "databases"
	// QuotaMailboxes limits mailboxes.
	QuotaMailboxes ResourceKind = "mailboxes"
	// QuotaDNSZones limits authoritative zones.
	QuotaDNSZones ResourceKind = "dns_zones"
	// QuotaStorageBytes limits primary storage.
	QuotaStorageBytes ResourceKind = "storage_bytes"
	// QuotaBackupBytes limits backup storage after deduplication and compression.
	QuotaBackupBytes ResourceKind = "backup_bytes"
	// QuotaBandwidthBytes limits transfer per billing period.
	QuotaBandwidthBytes ResourceKind = "bandwidth_bytes"
	// QuotaCPUCores limits total allocated CPU.
	QuotaCPUCores ResourceKind = "cpu_cores"
	// QuotaMemoryBytes limits total allocated memory.
	QuotaMemoryBytes ResourceKind = "memory_bytes"
	// QuotaOutboundEmails limits outbound messages per period.
	QuotaOutboundEmails ResourceKind = "outbound_emails"
)

// Unlimited is the only way a plan expresses "no ceiling". It must be set
// explicitly: a resource kind simply absent from a plan has an allowance
// of zero and every request for it is refused.
const Unlimited int64 = -1

// Quota is a plan's allowance per resource kind.
type Quota map[ResourceKind]int64

// Usage is a tenant's measured consumption per resource kind. It is always
// what cashp counted server-side, never a figure the client supplied.
type Usage map[ResourceKind]int64

// Allowance returns the plan's ceiling for a kind. A kind the plan does
// not mention has an allowance of zero, which is what makes quota bypass
// by omission impossible: a newly metered resource is denied to every
// existing plan until that plan names it.
func (q Quota) Allowance(kind ResourceKind) int64 {
	if q == nil {
		return 0
	}
	limit, ok := q[kind]
	if !ok {
		return 0
	}
	return limit
}

// CheckQuota is the mandatory server-side check at every resource-creation
// and resource-growth path. The denial it returns maps to the generic
// quota code and never states the threshold, so a tenant cannot probe the
// endpoint to learn another tier's limits.
func CheckQuota(q Quota, current Usage, kind ResourceKind, requested int64) error {
	if kind == "" {
		return Deny(ReasonInvalidInput, "quota check has no resource kind")
	}
	if requested <= 0 {
		return Deny(ReasonInvalidInput, "quota request for "+string(kind)+" must be positive")
	}

	limit := q.Allowance(kind)
	if limit == Unlimited {
		return nil
	}
	if limit <= 0 {
		return Deny(ReasonQuotaExceeded, "plan grants no "+string(kind))
	}

	used := int64(0)
	if current != nil {
		used = current[kind]
	}
	if used < 0 {
		return Deny(ReasonInvalidInput, "recorded usage for "+string(kind)+" is negative")
	}
	if used > math.MaxInt64-requested {
		return Deny(ReasonQuotaExceeded, "quota arithmetic for "+string(kind)+" overflows")
	}
	if used+requested > limit {
		return Deny(ReasonQuotaExceeded, "quota for "+string(kind)+" exhausted at "+strconv.FormatInt(limit, 10))
	}
	return nil
}

// deniedOutboundPorts are destination ports a tenant workload may never
// open directly. The mail submission ports are the spam vector, the
// NetBIOS, SMB, and engine-API ports are the lateral-movement vector, and
// the stratum range is the crypto-mining vector. Legitimate tenant mail
// leaves through cashp's own managed relay, which is not routed through
// this control.
var deniedOutboundPorts = map[int]struct{}{
	25:    {},
	135:   {},
	137:   {},
	138:   {},
	139:   {},
	445:   {},
	465:   {},
	587:   {},
	1900:  {},
	2375:  {},
	2376:  {},
	3333:  {},
	4444:  {},
	5555:  {},
	7777:  {},
	9999:  {},
	14433: {},
	14444: {},
	45700: {},
}

// OutboundPolicy bounds what a tenant workload may connect out to.
type OutboundPolicy struct {
	// AllowedPorts is the exact set of destination ports permitted; empty means any port not on the denylist.
	AllowedPorts []int
	// MaxDistinctTargets is how many distinct host:port pairs one tenant may reach within Window before the pattern is treated as scanning.
	MaxDistinctTargets int
	// Window is the span MaxDistinctTargets is counted over.
	Window time.Duration
}

// DefaultOutboundPolicy returns the outbound posture a fresh install uses:
// no explicit port allowlist beyond the denylist, and a distinct-target
// ceiling low enough to stop a port sweep long before it finishes.
func DefaultOutboundPolicy() OutboundPolicy {
	return OutboundPolicy{MaxDistinctTargets: 200, Window: time.Minute}
}

// OutboundControl enforces the outbound abuse posture for tenant
// workloads: destination vetting plus a distinct-target ceiling that turns
// a port or host sweep into a denial rather than a completed scan. It is
// safe for concurrent use.
type OutboundControl struct {
	mu      sync.Mutex
	policy  OutboundPolicy
	targets map[string]map[string]time.Time
	nowFunc func() time.Time
}

// NewOutboundControl creates a control enforcing policy.
func NewOutboundControl(policy OutboundPolicy) *OutboundControl {
	return &OutboundControl{
		policy:  policy,
		targets: make(map[string]map[string]time.Time),
		nowFunc: time.Now,
	}
}

// SetClock replaces the control's clock, for tests and for a caller that
// drives time itself. A nil clock is ignored.
func (c *OutboundControl) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nowFunc = now
}

// CheckDestination vets a single destination without recording it. It
// enforces the port posture and refuses any host that resolves to a
// private, loopback, link-local, or metadata address, which is the SSRF
// and node-pivot guard for tenant-initiated traffic.
func (c *OutboundControl) CheckDestination(host string, port int, resolve Resolver) error {
	if port < 1 || port > 65535 {
		return Deny(ReasonOutboundBlocked, "port "+strconv.Itoa(port)+" is out of range")
	}
	if _, denied := deniedOutboundPorts[port]; denied {
		return Deny(ReasonOutboundBlocked, "port "+strconv.Itoa(port)+" is on the abuse denylist")
	}

	c.mu.Lock()
	allowed := c.policy.AllowedPorts
	c.mu.Unlock()

	if len(allowed) > 0 {
		permitted := false
		for _, p := range allowed {
			if p == port {
				permitted = true
				break
			}
		}
		if !permitted {
			return Deny(ReasonOutboundBlocked, "port "+strconv.Itoa(port)+" is not in the allowed set")
		}
	}
	return CheckOutboundHost(host, resolve)
}

// Allow vets a destination and records it against the tenant's
// distinct-target budget, denying once the budget for the window is spent.
func (c *OutboundControl) Allow(tenantID, host string, port int, resolve Resolver) error {
	if tenantID == "" {
		return Deny(ReasonOutboundBlocked, "outbound request has no tenant")
	}
	if err := c.CheckDestination(host, port, resolve); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.policy.MaxDistinctTargets <= 0 || c.policy.Window <= 0 {
		return nil
	}

	now := c.nowFunc()
	cutoff := now.Add(-c.policy.Window)
	seen := c.targets[tenantID]
	if seen == nil {
		seen = make(map[string]time.Time)
		c.targets[tenantID] = seen
	}
	for target, at := range seen {
		if at.Before(cutoff) {
			delete(seen, target)
		}
	}

	target := host + ":" + strconv.Itoa(port)
	if _, known := seen[target]; known {
		seen[target] = now
		return nil
	}
	if len(seen) >= c.policy.MaxDistinctTargets {
		return Deny(ReasonRateLimited, "tenant "+tenantID+" exceeded the distinct outbound target budget")
	}
	seen[target] = now
	return nil
}

// Cleanup drops tenants whose recorded targets have all aged out.
func (c *OutboundControl) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.policy.Window <= 0 {
		return
	}
	cutoff := c.nowFunc().Add(-c.policy.Window)
	for tenant, seen := range c.targets {
		for target, at := range seen {
			if at.Before(cutoff) {
				delete(seen, target)
			}
		}
		if len(seen) == 0 {
			delete(c.targets, tenant)
		}
	}
}

// LockoutPolicy governs how repeated authentication failure is punished.
type LockoutPolicy struct {
	// Threshold is the number of failures within Window that trips a lockout.
	Threshold int
	// Window is the span failures are counted over.
	Window time.Duration
	// Duration is how long a tripped lockout lasts.
	Duration time.Duration
	// BaseBackoff is the delay imposed after the first failure.
	BaseBackoff time.Duration
	// MaxBackoff caps the doubling backoff.
	MaxBackoff time.Duration
}

// DefaultLockoutPolicy returns the lockout posture matching the account
// lockout thresholds the auth layer already enforces, with an exponential
// backoff layered underneath so early failures are slowed before the
// lockout trips.
func DefaultLockoutPolicy() LockoutPolicy {
	return LockoutPolicy{
		Threshold:   10,
		Window:      15 * time.Minute,
		Duration:    15 * time.Minute,
		BaseBackoff: 250 * time.Millisecond,
		MaxBackoff:  8 * time.Second,
	}
}

// lockoutEntry is the per-key failure record.
type lockoutEntry struct {
	failures int
	first    time.Time
	last     time.Time
	until    time.Time
}

// Lockout tracks authentication failures per key and converts them into
// backoff and then a lockout. A key is whatever the caller decides to
// bound: a username, an account id, or a client address. It is safe for
// concurrent use.
type Lockout struct {
	mu      sync.Mutex
	policy  LockoutPolicy
	entries map[string]*lockoutEntry
	nowFunc func() time.Time
}

// NewLockout creates a tracker enforcing policy.
func NewLockout(policy LockoutPolicy) *Lockout {
	return &Lockout{
		policy:  policy,
		entries: make(map[string]*lockoutEntry),
		nowFunc: time.Now,
	}
}

// SetClock replaces the tracker's clock. A nil clock is ignored.
func (l *Lockout) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nowFunc = now
}

// Check reports whether a key may attempt authentication. The denial
// carries no count and no remaining time, so it cannot be used to tune an
// attack, and the empty key denies rather than sharing a bucket.
func (l *Lockout) Check(key string) error {
	if key == "" {
		return Deny(ReasonSubjectInvalid, "lockout check has no key")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.entries[key]
	if entry == nil {
		return nil
	}
	if l.nowFunc().Before(entry.until) {
		return Deny(ReasonLockedOut, "key "+key+" is locked out")
	}
	return nil
}

// Fail records a failed attempt and returns the backoff the caller should
// impose before answering. Once the threshold is reached inside the
// window, the key is locked for the policy duration.
func (l *Lockout) Fail(key string) time.Duration {
	if key == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.nowFunc()
	entry := l.entries[key]
	if entry == nil || (l.policy.Window > 0 && now.Sub(entry.first) > l.policy.Window) {
		entry = &lockoutEntry{first: now}
		l.entries[key] = entry
	}
	entry.failures++
	entry.last = now

	if l.policy.Threshold > 0 && entry.failures >= l.policy.Threshold && l.policy.Duration > 0 {
		entry.until = now.Add(l.policy.Duration)
	}

	backoff := l.policy.BaseBackoff
	if backoff <= 0 {
		return 0
	}
	for i := 1; i < entry.failures; i++ {
		if l.policy.MaxBackoff > 0 && backoff >= l.policy.MaxBackoff {
			break
		}
		backoff *= 2
	}
	if l.policy.MaxBackoff > 0 && backoff > l.policy.MaxBackoff {
		backoff = l.policy.MaxBackoff
	}
	return backoff
}

// Failures reports how many failures are currently recorded for a key. It
// is an operator and audit accessor; it must never be rendered into a
// client-visible response.
func (l *Lockout) Failures(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry := l.entries[key]; entry != nil {
		return entry.failures
	}
	return 0
}

// Succeed clears a key's failure record after a successful authentication.
func (l *Lockout) Succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// Cleanup drops records whose window and lockout have both elapsed.
func (l *Lockout) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.nowFunc()
	for key, entry := range l.entries {
		if now.Before(entry.until) {
			continue
		}
		if l.policy.Window > 0 && now.Sub(entry.last) <= l.policy.Window {
			continue
		}
		delete(l.entries, key)
	}
}
