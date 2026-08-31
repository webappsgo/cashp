package guard

import (
	"math"
	"strconv"
	"testing"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

func TestCheckQuotaDeniesByOmission(t *testing.T) {
	plan := Quota{QuotaSites: 5}

	// A resource the plan never names has an allowance of zero, so a newly
	// metered resource cannot be created by every existing plan for free.
	if err := CheckQuota(plan, Usage{}, QuotaContainers, 1); err == nil {
		t.Fatal("CheckQuota allowed a resource the plan does not name")
	} else if DenialReason(err) != ReasonQuotaExceeded {
		t.Fatalf("expected quota_exceeded, got %q", DenialReason(err))
	}

	if err := CheckQuota(nil, nil, QuotaSites, 1); err == nil {
		t.Fatal("CheckQuota allowed a request against a nil plan")
	}
	if Quota(nil).Allowance(QuotaSites) != 0 {
		t.Fatal("a nil plan reported a non-zero allowance")
	}
}

func TestCheckQuotaEnforcesTheCeiling(t *testing.T) {
	plan := Quota{QuotaSites: 5, QuotaDatabases: Unlimited, QuotaVMs: 0}

	if err := CheckQuota(plan, Usage{QuotaSites: 4}, QuotaSites, 1); err != nil {
		t.Fatalf("CheckQuota denied a request inside the ceiling: %v", err)
	}
	if err := CheckQuota(plan, Usage{QuotaSites: 5}, QuotaSites, 1); err == nil {
		t.Fatal("CheckQuota allowed a request at the ceiling")
	}
	if err := CheckQuota(plan, Usage{QuotaSites: 0}, QuotaSites, 6); err == nil {
		t.Fatal("CheckQuota allowed a single request over the ceiling")
	}
	if err := CheckQuota(plan, Usage{QuotaVMs: 0}, QuotaVMs, 1); err == nil {
		t.Fatal("CheckQuota allowed a resource the plan explicitly zeroed")
	}
	if err := CheckQuota(plan, Usage{QuotaDatabases: 1_000_000}, QuotaDatabases, 1); err != nil {
		t.Fatalf("CheckQuota denied an explicitly unlimited resource: %v", err)
	}
}

func TestCheckQuotaRejectsHostileArithmetic(t *testing.T) {
	plan := Quota{QuotaStorageBytes: math.MaxInt64}

	for _, tc := range []struct {
		name      string
		usage     Usage
		kind      ResourceKind
		requested int64
	}{
		{"overflow", Usage{QuotaStorageBytes: math.MaxInt64}, QuotaStorageBytes, 1},
		{"negative request", Usage{}, QuotaStorageBytes, -1},
		{"zero request", Usage{}, QuotaStorageBytes, 0},
		{"negative usage", Usage{QuotaStorageBytes: -100}, QuotaStorageBytes, 1},
		{"empty kind", Usage{}, ResourceKind(""), 1},
	} {
		if err := CheckQuota(plan, tc.usage, tc.kind, tc.requested); err == nil {
			t.Fatalf("CheckQuota accepted the %s case", tc.name)
		}
	}
}

func TestCheckQuotaDenialCarriesNoThreshold(t *testing.T) {
	err := CheckQuota(Quota{QuotaSites: 5}, Usage{QuotaSites: 5}, QuotaSites, 1)
	appError := AppErrorFor(err)
	if appError.Code != apperr.CodeQuotaExceeded {
		t.Fatalf("quota denial mapped to %q", appError.Code)
	}
	if appError.Message != apperr.DefaultMessage(apperr.CodeQuotaExceeded) {
		t.Fatalf("quota denial leaked a specific message: %q", appError.Message)
	}
}

func TestCheckDestinationBlocksAbusePorts(t *testing.T) {
	control := NewOutboundControl(DefaultOutboundPolicy())
	resolve := FixedResolver(publicIP)

	for _, port := range []int{25, 465, 587, 135, 139, 445, 1900, 2375, 2376, 3333, 4444, 14444, 45700} {
		if err := control.CheckDestination("mail.example.com", port, resolve); err == nil {
			t.Fatalf("CheckDestination allowed the abuse port %d", port)
		}
	}
	for _, port := range []int{0, -1, 65536, 70000} {
		if err := control.CheckDestination("example.com", port, resolve); err == nil {
			t.Fatalf("CheckDestination allowed the out-of-range port %d", port)
		}
	}
	if err := control.CheckDestination("example.com", 443, resolve); err != nil {
		t.Fatalf("CheckDestination denied an ordinary HTTPS destination: %v", err)
	}
	// The SSRF posture applies to tenant-initiated traffic too.
	if err := control.CheckDestination("169.254.169.254", 80, resolve); err == nil {
		t.Fatal("CheckDestination allowed the cloud metadata address")
	}
}

func TestCheckDestinationHonoursAnExplicitPortAllowlist(t *testing.T) {
	control := NewOutboundControl(OutboundPolicy{AllowedPorts: []int{443}})
	resolve := FixedResolver(publicIP)

	if err := control.CheckDestination("example.com", 443, resolve); err != nil {
		t.Fatalf("CheckDestination denied an allowlisted port: %v", err)
	}
	if err := control.CheckDestination("example.com", 8080, resolve); err == nil {
		t.Fatal("CheckDestination allowed a port outside the allowlist")
	}
}

func TestAllowStopsAPortSweepWithinTheWindow(t *testing.T) {
	control := NewOutboundControl(OutboundPolicy{MaxDistinctTargets: 3, Window: time.Minute})
	base := time.Unix(1700000000, 0)
	current := base
	control.SetClock(func() time.Time { return current })
	resolve := FixedResolver(publicIP)

	for i := 0; i < 3; i++ {
		host := "host" + strconv.Itoa(i) + ".example.com"
		if err := control.Allow("t1", host, 443, resolve); err != nil {
			t.Fatalf("Allow denied target %d inside the budget: %v", i, err)
		}
	}
	// A fourth distinct target inside the window is the sweep signature.
	if err := control.Allow("t1", "host3.example.com", 443, resolve); err == nil {
		t.Fatal("Allow permitted a distinct target beyond the budget")
	} else if DenialReason(err) != ReasonRateLimited {
		t.Fatalf("expected rate_limited, got %q", DenialReason(err))
	}
	// Repeating an already-counted target is ordinary traffic, not a sweep.
	if err := control.Allow("t1", "host0.example.com", 443, resolve); err != nil {
		t.Fatalf("Allow denied a repeat of a counted target: %v", err)
	}
	// The budget is per tenant, so one tenant cannot exhaust another's.
	if err := control.Allow("t2", "host3.example.com", 443, resolve); err != nil {
		t.Fatalf("Allow leaked one tenant's budget into another: %v", err)
	}
	// Once the window has passed the budget refills.
	current = base.Add(2 * time.Minute)
	if err := control.Allow("t1", "host9.example.com", 443, resolve); err != nil {
		t.Fatalf("Allow did not refill after the window: %v", err)
	}

	control.Cleanup()

	if err := control.Allow("", "example.com", 443, resolve); err == nil {
		t.Fatal("Allow accepted an outbound request with no tenant")
	}
}

func TestLockoutTripsAndBacksOff(t *testing.T) {
	policy := DefaultLockoutPolicy()
	lock := NewLockout(policy)
	base := time.Unix(1700000000, 0)
	current := base
	lock.SetClock(func() time.Time { return current })

	if err := lock.Check("u1"); err != nil {
		t.Fatalf("Check denied an untouched key: %v", err)
	}
	if err := lock.Check(""); err == nil {
		t.Fatal("Check let an empty key share a bucket")
	}

	backoffs := make([]time.Duration, 0, policy.Threshold)
	for i := 0; i < policy.Threshold; i++ {
		backoffs = append(backoffs, lock.Fail("u1"))
	}
	if backoffs[0] != policy.BaseBackoff {
		t.Fatalf("the first failure produced %v", backoffs[0])
	}
	if backoffs[1] != 2*policy.BaseBackoff {
		t.Fatalf("the backoff did not double: %v", backoffs[1])
	}
	for _, b := range backoffs {
		if b > policy.MaxBackoff {
			t.Fatalf("the backoff exceeded its cap: %v", b)
		}
	}
	if backoffs[len(backoffs)-1] != policy.MaxBackoff {
		t.Fatalf("the backoff did not saturate at the cap: %v", backoffs[len(backoffs)-1])
	}

	err := lock.Check("u1")
	if err == nil {
		t.Fatal("Check permitted an attempt after the threshold was reached")
	}
	if DenialReason(err) != ReasonLockedOut {
		t.Fatalf("expected locked_out, got %q", DenialReason(err))
	}
	if msg := AppErrorFor(err).Message; msg != apperr.DefaultMessage(apperr.CodeAccountLocked) {
		t.Fatalf("the lockout denial leaked detail: %q", msg)
	}
	if lock.Failures("u1") != policy.Threshold {
		t.Fatalf("Failures reported %d", lock.Failures("u1"))
	}

	// An unrelated key must be unaffected.
	if err := lock.Check("u2"); err != nil {
		t.Fatalf("one key's lockout spilled onto another: %v", err)
	}

	// The lockout expires on its own schedule.
	current = base.Add(policy.Duration + time.Second)
	if err := lock.Check("u1"); err != nil {
		t.Fatalf("the lockout outlived its duration: %v", err)
	}

	lock.Succeed("u1")
	if lock.Failures("u1") != 0 {
		t.Fatal("a successful authentication did not clear the failure record")
	}

	current = base.Add(policy.Duration + policy.Window + time.Hour)
	lock.Cleanup()
	if lock.Fail("") != 0 {
		t.Fatal("Fail recorded a failure for the empty key")
	}
}

func TestLockoutWithoutBackoffPolicyStillDenies(t *testing.T) {
	lock := NewLockout(LockoutPolicy{Threshold: 2, Window: time.Minute, Duration: time.Minute})
	if d := lock.Fail("u1"); d != 0 {
		t.Fatalf("a zero BaseBackoff produced a backoff of %v", d)
	}
	lock.Fail("u1")
	if err := lock.Check("u1"); err == nil {
		t.Fatal("the threshold did not trip without a backoff configured")
	}
}
