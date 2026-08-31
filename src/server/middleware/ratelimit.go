package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
)

// Rule is one rate-limit bucket: at most Limit requests per Window.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Allowed reports whether the rule is active. A zero limit disables it.
func (r Rule) Allowed() bool {
	return r.Limit > 0 && r.Window > 0
}

// The PART 11 defaults. Reads are generous, writes are tight, and the
// credential endpoints are tighter still.
var (
	// DefaultReadRule limits read requests to 120 per minute.
	DefaultReadRule = Rule{Limit: 120, Window: time.Minute}
	// DefaultWriteRule limits write requests to 10 per minute.
	DefaultWriteRule = Rule{Limit: 10, Window: time.Minute}
	// DefaultLoginRule limits login attempts to 5 per 15 minutes.
	DefaultLoginRule = Rule{Limit: 5, Window: 15 * time.Minute}
	// DefaultPasswordResetRule limits reset requests to 3 per hour.
	DefaultPasswordResetRule = Rule{Limit: 3, Window: time.Hour}
)

// RateLimitOptions configures the limiter.
type RateLimitOptions struct {
	// Disabled turns the limiter off entirely.
	Disabled bool
	// Read, Write, Login, and PasswordReset override the defaults.
	Read          Rule
	Write         Rule
	Login         Rule
	PasswordReset Rule
	// LoginPaths and PasswordResetPaths are matched as path suffixes so the
	// versioned route and its unversioned alias share one bucket.
	LoginPaths         []string
	PasswordResetPaths []string
	// Exempt skips the limiter for a request, used for loopback health
	// probes and other operator-controlled traffic.
	Exempt func(*http.Request) bool
	// Debug adds the debug-only counter detail to a rejection.
	Debug bool
}

// counter is one fixed window of requests for a single key.
type counter struct {
	count int
	reset time.Time
}

// Limiter is a fixed-window rate limiter keyed by client IP and bucket.
type Limiter struct {
	mu       sync.Mutex
	counters map[string]*counter
	lastGC   time.Time
	now      func() time.Time
}

// NewLimiter builds an empty limiter.
func NewLimiter() *Limiter {
	return &Limiter{counters: map[string]*counter{}, now: time.Now}
}

// Allow consumes one request against a rule and reports whether it fits
// inside the window, along with the current count and the time the window
// resets.
func (l *Limiter) Allow(key string, rule Rule) (ok bool, count int, reset time.Time) {
	if !rule.Allowed() {
		return true, 0, time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.collect(now)

	c, exists := l.counters[key]
	if !exists || now.After(c.reset) {
		c = &counter{reset: now.Add(rule.Window)}
		l.counters[key] = c
	}
	c.count++
	return c.count <= rule.Limit, c.count, c.reset
}

// collect drops expired windows so the map cannot grow without bound. It
// runs at most once a minute and only while the lock is already held.
func (l *Limiter) collect(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	for k, c := range l.counters {
		if now.After(c.reset) {
			delete(l.counters, k)
		}
	}
}

// RateLimit enforces the per-client request budgets. Rejections are generic:
// the response never names the threshold or the window, because that would
// tell an attacker exactly how to pace itself.
func RateLimit(limiter *Limiter, opts RateLimitOptions) func(http.Handler) http.Handler {
	if limiter == nil {
		limiter = NewLimiter()
	}
	rules := resolveRules(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if opts.Disabled || (opts.Exempt != nil && opts.Exempt(r)) {
				next.ServeHTTP(w, r)
				return
			}
			bucket, rule := classify(r, opts, rules)
			if !rule.Allowed() {
				next.ServeHTTP(w, r)
				return
			}
			key := ClientIPFrom(r.Context()) + "|" + bucket
			ok, count, reset := limiter.Allow(key, rule)
			if ok {
				next.ServeHTTP(w, r)
				return
			}
			retry := int(time.Until(reset).Seconds())
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			err := apperr.New(apperr.CodeRateLimited, http.StatusTooManyRequests,
				apperr.DefaultMessage(apperr.CodeRateLimited))
			if opts.Debug {
				err = err.WithDetails(map[string]any{
					"bucket":        bucket,
					"count":         count,
					"limit":         rule.Limit,
					"window":        rule.Window.String(),
					"reset_seconds": retry,
				})
			}
			api.WriteError(w, r, err)
		})
	}
}

// bucketRules holds the resolved rule for each bucket name.
type bucketRules struct {
	read          Rule
	write         Rule
	login         Rule
	passwordReset Rule
}

// resolveRules applies the defaults for any rule the caller left unset.
func resolveRules(opts RateLimitOptions) bucketRules {
	return bucketRules{
		read:          ruleOr(opts.Read, DefaultReadRule),
		write:         ruleOr(opts.Write, DefaultWriteRule),
		login:         ruleOr(opts.Login, DefaultLoginRule),
		passwordReset: ruleOr(opts.PasswordReset, DefaultPasswordResetRule),
	}
}

// ruleOr returns the configured rule when it is usable, otherwise the
// default.
func ruleOr(configured, fallback Rule) Rule {
	if configured.Limit != 0 || configured.Window != 0 {
		return configured
	}
	return fallback
}

// classify picks the bucket a request belongs to: the credential buckets win
// over the generic method-based buckets.
func classify(r *http.Request, opts RateLimitOptions, rules bucketRules) (string, Rule) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if matchesAny(path, opts.PasswordResetPaths) {
		return "password_reset", rules.passwordReset
	}
	if matchesAny(path, opts.LoginPaths) {
		return "login", rules.login
	}
	if safeMethods[r.Method] {
		return "read", rules.read
	}
	return "write", rules.write
}

// matchesAny reports whether a path equals or ends with one of the suffixes,
// which lets a versioned route and its unversioned alias share one bucket.
func matchesAny(path string, suffixes []string) bool {
	for _, s := range suffixes {
		s = strings.TrimSuffix(s, "/")
		if s == "" {
			continue
		}
		if path == s || strings.HasSuffix(path, s) {
			return true
		}
	}
	return false
}
