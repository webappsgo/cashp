package security

import (
	"sync"
	"time"
)

// Rate limit defaults from AI.md PART 11. Every value is admin-configurable
// at runtime through Limiter.SetRule and Limits.Set.
var (
	// RuleRead is the default limit for GET and HEAD requests.
	RuleRead = Rule{Requests: 120, Window: time.Minute}
	// RuleWrite is the default limit for POST, PUT, PATCH, and DELETE requests.
	RuleWrite = Rule{Requests: 10, Window: time.Minute}
	// RuleHealth is the default limit for health and status endpoints.
	RuleHealth = Rule{Requests: 120, Window: time.Minute}
	// RuleGlobalBurst is the absolute per-IP ceiling across all endpoint classes.
	RuleGlobalBurst = Rule{Requests: 240, Window: time.Minute}
	// RuleLogin is the default limit for login attempts, keyed per IP and per identifier.
	RuleLogin = Rule{Requests: 5, Window: 15 * time.Minute}
	// RulePasswordReset is the default limit for password reset requests.
	RulePasswordReset = Rule{Requests: 3, Window: time.Hour}
	// RuleRegistration is the default limit for account registrations.
	RuleRegistration = Rule{Requests: 5, Window: time.Hour}
	// RuleFileUpload is the default limit for file uploads.
	RuleFileUpload = Rule{Requests: 10, Window: time.Hour}
)

// Rule is a sliding-window allowance: at most Requests events per key
// within any Window-long span.
type Rule struct {
	Requests int
	Window   time.Duration
}

// Enabled reports whether the rule imposes any limit. A zero or negative
// Requests or Window disables enforcement, which is how the admin panel
// turns a limit off without deleting it.
func (r Rule) Enabled() bool {
	return r.Requests > 0 && r.Window > 0
}

// Limiter is a concurrency-safe in-memory sliding-window counter. One
// Limiter enforces one Rule across many keys, where a key is typically a
// client IP, an account identifier, or a Tor circuit ID.
type Limiter struct {
	mu      sync.Mutex
	rule    Rule
	events  map[string][]time.Time
	nowFunc func() time.Time
}

// NewLimiter creates a Limiter enforcing rule.
func NewLimiter(rule Rule) *Limiter {
	return &Limiter{
		rule:    rule,
		events:  make(map[string][]time.Time),
		nowFunc: time.Now,
	}
}

// Rule returns the rule currently enforced.
func (l *Limiter) Rule() Rule {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rule
}

// SetRule replaces the enforced rule, supporting live reconfiguration from
// the admin panel. Existing counters are kept and re-evaluated against the
// new window.
func (l *Limiter) SetRule(rule Rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rule = rule
}

// Allow records an event for key and reports whether it is within the
// limit. When it is not, retryAfter is the time until the oldest event in
// the window expires, which the caller sends as the Retry-After header.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.rule.Enabled() {
		return true, 0
	}

	now := l.nowFunc()
	cutoff := now.Add(-l.rule.Window)
	kept := prune(l.events[key], cutoff)

	if len(kept) >= l.rule.Requests {
		l.events[key] = kept
		wait := kept[0].Add(l.rule.Window).Sub(now)
		if wait < time.Second {
			wait = time.Second
		}
		return false, wait
	}

	l.events[key] = append(kept, now)
	return true, 0
}

// Remaining reports how many further events key may record in the current
// window without recording one. A disabled rule reports -1, meaning
// unlimited.
func (l *Limiter) Remaining(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.rule.Enabled() {
		return -1
	}

	kept := prune(l.events[key], l.nowFunc().Add(-l.rule.Window))
	l.events[key] = kept

	if remaining := l.rule.Requests - len(kept); remaining > 0 {
		return remaining
	}
	return 0
}

// Reset clears the counter for a single key, used when an admin lifts a
// block or when a login succeeds.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.events, key)
}

// Cleanup drops every key whose events have all fallen out of the window.
// A caller runs this periodically so idle keys do not accumulate.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.nowFunc().Add(-l.rule.Window)
	for key, events := range l.events {
		kept := prune(events, cutoff)
		if len(kept) == 0 {
			delete(l.events, key)
			continue
		}
		l.events[key] = kept
	}
}

// prune returns the suffix of events at or after cutoff. events is always
// stored in ascending time order.
func prune(events []time.Time, cutoff time.Time) []time.Time {
	idx := 0
	for idx < len(events) && !events[idx].After(cutoff) {
		idx++
	}
	if idx == 0 {
		return events
	}
	return append([]time.Time(nil), events[idx:]...)
}

// Limits is the named set of limiters the HTTP layer consults, seeded with
// the PART 11 defaults.
type Limits struct {
	mu       sync.RWMutex
	limiters map[string]*Limiter
}

// Named limiter keys used by the HTTP layer.
const (
	// LimitRead covers GET and HEAD requests.
	LimitRead = "read"
	// LimitWrite covers POST, PUT, PATCH, and DELETE requests.
	LimitWrite = "write"
	// LimitHealth covers health and status endpoints.
	LimitHealth = "health"
	// LimitGlobalBurst is the absolute ceiling across all endpoint classes.
	LimitGlobalBurst = "global_burst"
	// LimitLogin covers login attempts.
	LimitLogin = "login"
	// LimitPasswordReset covers password reset requests.
	LimitPasswordReset = "password_reset"
	// LimitRegistration covers account registrations.
	LimitRegistration = "registration"
	// LimitFileUpload covers file uploads.
	LimitFileUpload = "file_upload"
)

// NewLimits builds the default limiter set.
func NewLimits() *Limits {
	return &Limits{
		limiters: map[string]*Limiter{
			LimitRead:          NewLimiter(RuleRead),
			LimitWrite:         NewLimiter(RuleWrite),
			LimitHealth:        NewLimiter(RuleHealth),
			LimitGlobalBurst:   NewLimiter(RuleGlobalBurst),
			LimitLogin:         NewLimiter(RuleLogin),
			LimitPasswordReset: NewLimiter(RulePasswordReset),
			LimitRegistration:  NewLimiter(RuleRegistration),
			LimitFileUpload:    NewLimiter(RuleFileUpload),
		},
	}
}

// Get returns the limiter registered under name, or nil when name is
// unknown.
func (s *Limits) Get(name string) *Limiter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limiters[name]
}

// Set registers or replaces the rule for name, creating the limiter when
// the name is new. This is the entry point for admin-panel reconfiguration.
func (s *Limits) Set(name string, rule Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if l, ok := s.limiters[name]; ok {
		l.SetRule(rule)
		return
	}
	s.limiters[name] = NewLimiter(rule)
}

// Allow records an event against the named limiter. An unknown name is
// allowed rather than denied, so a missing registration can never lock the
// server out of an endpoint class.
func (s *Limits) Allow(name, key string) (allowed bool, retryAfter time.Duration) {
	l := s.Get(name)
	if l == nil {
		return true, 0
	}
	return l.Allow(key)
}

// Cleanup prunes idle keys across every registered limiter.
func (s *Limits) Cleanup() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, l := range s.limiters {
		l.Cleanup()
	}
}
