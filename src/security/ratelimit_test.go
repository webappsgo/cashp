package security

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock used to exercise the sliding
// window without sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the current fake time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake clock forward.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestLimiter builds a limiter driven by a fake clock.
func newTestLimiter(rule Rule) (*Limiter, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	l := NewLimiter(rule)
	l.nowFunc = clock.Now

	return l, clock
}

func TestLimiterAllowsUpToLimit(t *testing.T) {
	l, _ := newTestLimiter(Rule{Requests: 3, Window: time.Minute})

	for i := 0; i < 3; i++ {
		allowed, retryAfter := l.Allow("1.2.3.4")
		if !allowed {
			t.Fatalf("request %d was denied within the limit", i+1)
		}
		if retryAfter != 0 {
			t.Fatalf("retryAfter = %v, want 0 for an allowed request", retryAfter)
		}
	}

	allowed, retryAfter := l.Allow("1.2.3.4")
	if allowed {
		t.Fatal("the fourth request exceeded the limit but was allowed")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Fatalf("retryAfter = %v, want a positive duration no larger than the window", retryAfter)
	}
}

func TestLimiterIsPerKey(t *testing.T) {
	l, _ := newTestLimiter(Rule{Requests: 1, Window: time.Minute})

	if allowed, _ := l.Allow("1.2.3.4"); !allowed {
		t.Fatal("first request for the first key was denied")
	}
	if allowed, _ := l.Allow("1.2.3.4"); allowed {
		t.Fatal("second request for the first key was allowed")
	}
	if allowed, _ := l.Allow("5.6.7.8"); !allowed {
		t.Fatal("a different key must have its own budget")
	}
}

func TestLimiterWindowSlides(t *testing.T) {
	l, clock := newTestLimiter(Rule{Requests: 2, Window: time.Minute})

	l.Allow("ip")
	clock.Advance(30 * time.Second)
	l.Allow("ip")

	if allowed, _ := l.Allow("ip"); allowed {
		t.Fatal("third request inside the window was allowed")
	}

	// The first event ages out 60s after it was recorded, freeing one slot.
	clock.Advance(31 * time.Second)
	if allowed, _ := l.Allow("ip"); !allowed {
		t.Fatal("a slot should have freed once the oldest event left the window")
	}

	clock.Advance(2 * time.Minute)
	if remaining := l.Remaining("ip"); remaining != 2 {
		t.Fatalf("Remaining = %d, want 2 after the window fully drained", remaining)
	}
}

func TestLimiterRetryAfterShrinksAsWindowAges(t *testing.T) {
	l, clock := newTestLimiter(Rule{Requests: 1, Window: time.Minute})

	l.Allow("ip")

	_, first := l.Allow("ip")
	clock.Advance(30 * time.Second)
	_, second := l.Allow("ip")

	if second >= first {
		t.Fatalf("retryAfter did not shrink: first=%v second=%v", first, second)
	}
	if second < time.Second {
		t.Fatalf("retryAfter = %v, want at least one second", second)
	}
}

func TestLimiterDisabledRuleAllowsEverything(t *testing.T) {
	l, _ := newTestLimiter(Rule{Requests: 0, Window: time.Minute})

	for i := 0; i < 50; i++ {
		if allowed, _ := l.Allow("ip"); !allowed {
			t.Fatal("a disabled rule must never deny a request")
		}
	}
	if remaining := l.Remaining("ip"); remaining != -1 {
		t.Fatalf("Remaining = %d, want -1 for a disabled rule", remaining)
	}
}

func TestLimiterResetAndSetRule(t *testing.T) {
	l, _ := newTestLimiter(Rule{Requests: 1, Window: time.Minute})

	l.Allow("ip")
	if allowed, _ := l.Allow("ip"); allowed {
		t.Fatal("limit was not enforced before Reset")
	}

	l.Reset("ip")
	if allowed, _ := l.Allow("ip"); !allowed {
		t.Fatal("Reset must clear the counter for the key")
	}

	l.SetRule(Rule{Requests: 10, Window: time.Minute})
	if got := l.Rule(); got.Requests != 10 {
		t.Fatalf("Rule().Requests = %d, want 10", got.Requests)
	}
	for i := 0; i < 9; i++ {
		if allowed, _ := l.Allow("ip"); !allowed {
			t.Fatalf("request %d denied after the limit was raised", i+1)
		}
	}
}

func TestLimiterCleanupDropsIdleKeys(t *testing.T) {
	l, clock := newTestLimiter(Rule{Requests: 5, Window: time.Minute})

	l.Allow("idle")
	l.Allow("busy")

	clock.Advance(2 * time.Minute)
	l.Allow("busy")
	l.Cleanup()

	l.mu.Lock()
	_, idleExists := l.events["idle"]
	_, busyExists := l.events["busy"]
	l.mu.Unlock()

	if idleExists {
		t.Fatal("Cleanup must drop keys with no events left in the window")
	}
	if !busyExists {
		t.Fatal("Cleanup must keep keys with live events")
	}
}

func TestLimiterConcurrentAllow(t *testing.T) {
	l := NewLimiter(Rule{Requests: 100, Window: time.Minute})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Allow("shared")
		}()
	}
	wg.Wait()

	if remaining := l.Remaining("shared"); remaining != 50 {
		t.Fatalf("Remaining = %d, want 50 after 50 concurrent requests", remaining)
	}
}

func TestLimitsDefaults(t *testing.T) {
	limits := NewLimits()

	tests := []struct {
		name string
		want Rule
	}{
		{LimitRead, RuleRead},
		{LimitWrite, RuleWrite},
		{LimitHealth, RuleHealth},
		{LimitGlobalBurst, RuleGlobalBurst},
		{LimitLogin, RuleLogin},
		{LimitPasswordReset, RulePasswordReset},
		{LimitRegistration, RuleRegistration},
		{LimitFileUpload, RuleFileUpload},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := limits.Get(tc.name)
			if l == nil {
				t.Fatalf("limiter %q is not registered", tc.name)
			}
			if got := l.Rule(); got != tc.want {
				t.Fatalf("rule = %+v, want %+v", got, tc.want)
			}
		})
	}

	if RuleRead.Requests != 120 || RuleRead.Window != time.Minute {
		t.Fatalf("read default = %+v, want 120/min", RuleRead)
	}
	if RuleWrite.Requests != 10 || RuleWrite.Window != time.Minute {
		t.Fatalf("write default = %+v, want 10/min", RuleWrite)
	}
	if RuleLogin.Requests != 5 || RuleLogin.Window != 15*time.Minute {
		t.Fatalf("login default = %+v, want 5/15m", RuleLogin)
	}
	if RulePasswordReset.Requests != 3 || RulePasswordReset.Window != time.Hour {
		t.Fatalf("password reset default = %+v, want 3/hr", RulePasswordReset)
	}
}

func TestLimitsSetAndAllow(t *testing.T) {
	limits := NewLimits()

	limits.Set(LimitWrite, Rule{Requests: 1, Window: time.Minute})
	if allowed, _ := limits.Allow(LimitWrite, "ip"); !allowed {
		t.Fatal("first write was denied")
	}
	if allowed, retryAfter := limits.Allow(LimitWrite, "ip"); allowed || retryAfter <= 0 {
		t.Fatal("second write must be denied with a positive retry-after")
	}

	limits.Set("custom", Rule{Requests: 1, Window: time.Minute})
	if limits.Get("custom") == nil {
		t.Fatal("Set must register a limiter under a new name")
	}

	if allowed, _ := limits.Allow("unregistered", "ip"); !allowed {
		t.Fatal("an unknown limiter name must not deny the request")
	}

	limits.Cleanup()
}
