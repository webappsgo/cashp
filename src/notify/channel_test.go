package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeChannel is a controllable Channel used to exercise the registry and
// the state machine without touching the network.
type fakeChannel struct {
	name       string
	autoEnable bool
	testErr    error
	sendErr    error
	sends      int
	accept     bool
}

func (f *fakeChannel) Name() string { return f.name }

func (f *fakeChannel) Category() string { return CategoryGeneric }

func (f *fakeChannel) Validate() error { return nil }

func (f *fakeChannel) Test(context.Context) TestResult {
	if f.testErr != nil {
		return TestResult{Detail: "failed", Err: f.testErr}
	}
	return TestResult{Connected: true, Authenticated: true, Delivered: true, Detail: "ok"}
}

func (f *fakeChannel) Send(context.Context, Rendered) error {
	f.sends++
	return f.sendErr
}

func (f *fakeChannel) AutoEnable() bool { return f.autoEnable }

func (f *fakeChannel) Accepts(Rendered) bool { return f.accept }

func (f *fakeChannel) ConfigSchema() []Field { return nil }

func (f *fakeChannel) Help() Help { return Help{Summary: "fake"} }

// fixedClock returns a clock that advances by a fixed step on every read, so
// latency measurements are deterministic.
func fixedClock(step time.Duration) func() time.Time {
	current := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	return func() time.Time {
		current = current.Add(step)
		return current
	}
}

func TestCanTransitionRejectsDisabledToActive(t *testing.T) {
	if CanTransition(StateDisabled, StateActive) {
		t.Fatal("DISABLED must not reach ACTIVE without CONFIGURING and TESTING")
	}
	if !CanTransition(StateDisabled, StateConfiguring) {
		t.Fatal("DISABLED must reach CONFIGURING")
	}
	if !CanTransition(StateConfiguring, StateTesting) {
		t.Fatal("CONFIGURING must reach TESTING")
	}
	if !CanTransition(StateTesting, StateActive) {
		t.Fatal("TESTING must reach ACTIVE")
	}
	if CanTransition(StateFailed, StateActive) {
		t.Fatal("FAILED must be reconfigured before it can be active again")
	}
}

func TestRegistryActivateRequiresPassingTest(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	channel := &fakeChannel{name: "manual", accept: true}
	if err := registry.Register(channel); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := registry.Activate("manual"); err == nil {
		t.Fatal("an untested channel must not activate")
	}

	if _, err := registry.Test(context.Background(), "manual"); err != nil {
		t.Fatalf("test: %v", err)
	}
	state, err := registry.State("manual")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state != StateTesting {
		t.Fatalf("a channel that does not auto-enable must wait in TESTING, got %s", state)
	}

	if err := registry.Activate("manual"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if state, _ := registry.State("manual"); state != StateActive {
		t.Fatalf("expected ACTIVE, got %s", state)
	}
}

func TestRegistryAutoEnableOnlyAppliesToOptedInChannel(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	auto := &fakeChannel{name: "auto", autoEnable: true, accept: true}
	manual := &fakeChannel{name: "manual", accept: true}
	for _, channel := range []Channel{auto, manual} {
		if err := registry.Register(channel); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	if _, err := registry.Test(context.Background(), "auto"); err != nil {
		t.Fatalf("test auto: %v", err)
	}
	if state, _ := registry.State("auto"); state != StateActive {
		t.Fatalf("an auto-enabling channel must activate on a passing test, got %s", state)
	}

	if _, err := registry.Test(context.Background(), "manual"); err != nil {
		t.Fatalf("test manual: %v", err)
	}
	if state, _ := registry.State("manual"); state == StateActive {
		t.Fatal("a channel that does not auto-enable must not activate itself")
	}
}

func TestRegistryFailedTestReturnsToDisabled(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	channel := &fakeChannel{name: "auto", autoEnable: true, testErr: errors.New("no route to host")}
	if err := registry.Register(channel); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := registry.Test(context.Background(), "auto"); err == nil {
		t.Fatal("a failing test must report its error")
	}
	if state, _ := registry.State("auto"); state == StateActive {
		t.Fatal("a failing test must never leave a channel ACTIVE")
	}
}

func TestRegistryDeliverRefusesInactiveChannel(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	channel := &fakeChannel{name: "manual", accept: true}
	if err := registry.Register(channel); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := registry.Deliver(context.Background(), "manual", Rendered{Event: EventTest}); err == nil {
		t.Fatal("a disabled channel must refuse deliveries")
	}
	if channel.sends != 0 {
		t.Fatalf("expected no send attempt, got %d", channel.sends)
	}

	if _, err := registry.Test(context.Background(), "manual"); err != nil {
		t.Fatalf("test: %v", err)
	}
	if err := registry.Activate("manual"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := registry.Deliver(context.Background(), "manual", Rendered{Event: EventTest}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if channel.sends != 1 {
		t.Fatalf("expected one send, got %d", channel.sends)
	}
}

func TestRegistryRejectsDuplicateRegistration(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	if err := registry.Register(&fakeChannel{name: "dup"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(&fakeChannel{name: "dup"}); err == nil {
		t.Fatal("registering the same channel name twice must fail")
	}
}

func TestRegistryStatusReportsMetrics(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	channel := &fakeChannel{name: "auto", autoEnable: true, accept: true}
	if err := registry.Register(channel); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := registry.Test(context.Background(), "auto"); err != nil {
		t.Fatalf("test: %v", err)
	}
	if err := registry.Deliver(context.Background(), "auto", Rendered{Event: EventTest}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	status, err := registry.Status("auto")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Sends != 1 || status.Successes != 1 {
		t.Fatalf("expected one successful send, got %d/%d", status.Successes, status.Sends)
	}
	if status.SuccessRate != 1 {
		t.Fatalf("expected a perfect success rate, got %v", status.SuccessRate)
	}
	if len(registry.Statuses()) != 1 {
		t.Fatalf("expected one status row, got %d", len(registry.Statuses()))
	}
}

func TestRegistryUnknownChannel(t *testing.T) {
	registry := NewRegistry(fixedClock(time.Millisecond))
	if _, err := registry.Channel("nope"); err == nil {
		t.Fatal("an unknown channel must not resolve")
	}
	if err := registry.Disable("nope"); err == nil {
		t.Fatal("an unknown channel must not transition")
	}
}
