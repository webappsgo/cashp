package notify

import (
	"sync"
	"time"
)

// State is a notification channel's lifecycle state. A channel only ever
// delivers in StateActive; every other state is either a configuration step
// or a fault.
type State string

// The channel states. Names are stable: they are persisted in the admin
// panel view and asserted on in tests.
const (
	// StateDisabled is the shipped default for every channel.
	StateDisabled State = "DISABLED"
	// StateConfiguring means credentials are being entered or were saved
	// but not yet proven.
	StateConfiguring State = "CONFIGURING"
	// StateTesting means a connectivity test is running or has just passed
	// and the channel awaits activation.
	StateTesting State = "TESTING"
	// StateActive means the channel delivers.
	StateActive State = "ACTIVE"
	// StateDegraded means consecutive delivery failures crossed the warning
	// threshold but the channel is still tried.
	StateDegraded State = "DEGRADED"
	// StateFailed means the channel stopped delivering and needs operator
	// attention.
	StateFailed State = "FAILED"
	// StateMaintenance means an operator parked the channel deliberately.
	StateMaintenance State = "MAINTENANCE"
)

// Failure thresholds driving the DEGRADED and FAILED transitions.
const (
	// DegradeThreshold is the number of consecutive delivery failures that
	// moves an active channel to DEGRADED.
	DegradeThreshold = 3
	// FailThreshold is the number of consecutive delivery failures that
	// moves a degraded channel to FAILED.
	FailThreshold = 10
)

// transitions is the complete allowed edge set. It is the executable form
// of the state diagram: anything not listed here is rejected, so a channel
// can never jump from DISABLED straight to ACTIVE.
var transitions = map[State][]State{
	StateDisabled:    {StateConfiguring, StateMaintenance},
	StateConfiguring: {StateTesting, StateDisabled, StateMaintenance},
	StateTesting:     {StateActive, StateDisabled, StateConfiguring, StateMaintenance},
	StateActive:      {StateDegraded, StateFailed, StateConfiguring, StateDisabled, StateMaintenance},
	StateDegraded:    {StateActive, StateFailed, StateConfiguring, StateDisabled, StateMaintenance},
	StateFailed:      {StateConfiguring, StateDisabled, StateMaintenance},
	StateMaintenance: {StateActive, StateDisabled, StateConfiguring},
}

// CanTransition reports whether a channel may move directly from one state
// to another. A self-transition is always allowed and is a no-op.
func CanTransition(from, to State) bool {
	if from == to {
		return true
	}
	for _, allowed := range transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// TestResult is one itemised connectivity test outcome, rendered in the
// admin panel's test results panel.
type TestResult struct {
	// Connected reports whether a transport connection was established.
	Connected bool
	// Authenticated reports whether credentials were accepted. It is false
	// for channels that need none.
	Authenticated bool
	// Delivered reports whether a test message was accepted by the peer.
	Delivered bool
	// Latency is how long the whole test took.
	Latency time.Duration
	// Detail is a human-readable summary, already free of secrets.
	Detail string
	// Err is the failure, or nil on success.
	Err error
}

// OK reports whether the test passed end to end.
func (r TestResult) OK() bool { return r.Err == nil && r.Connected && r.Delivered }

// stateMachine tracks one channel's state, failure streak and last test.
// It is safe for concurrent use; the registry holds one per channel.
type stateMachine struct {
	mu           sync.Mutex
	state        State
	failures     int
	successes    int64
	sends        int64
	latencyTotal time.Duration
	changedAt    time.Time
	lastTest     TestResult
	lastTestAt   time.Time
	lastError    string
	now          func() time.Time
}

// newStateMachine returns a machine parked in StateDisabled.
func newStateMachine(now func() time.Time) *stateMachine {
	if now == nil {
		now = time.Now
	}
	return &stateMachine{state: StateDisabled, changedAt: now(), now: now}
}

// State returns the current state.
func (m *stateMachine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// To attempts a transition and reports whether it was allowed.
func (m *stateMachine) To(next State) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toLocked(next)
}

// toLocked performs the transition with the mutex already held.
func (m *stateMachine) toLocked(next State) bool {
	if !CanTransition(m.state, next) {
		return false
	}
	if m.state != next {
		m.state = next
		m.changedAt = m.now()
	}
	if next == StateActive {
		m.failures = 0
	}
	return true
}

// recordTest stores a test outcome and applies the resulting transition.
// A pass leaves the channel in TESTING when autoEnable is false, so an
// administrator still has to activate it explicitly; a failure always
// returns the channel to DISABLED.
func (m *stateMachine) recordTest(res TestResult, autoEnable bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastTest = res
	m.lastTestAt = m.now()
	if res.Err != nil {
		m.lastError = res.Err.Error()
	} else {
		m.lastError = ""
	}

	if !res.OK() {
		m.toLocked(StateDisabled)
		return
	}
	if autoEnable {
		m.toLocked(StateActive)
	}
}

// recordSend folds one delivery outcome into the failure streak and the
// metrics counters, moving the channel between ACTIVE, DEGRADED and FAILED.
func (m *stateMachine) recordSend(latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sends++
	m.latencyTotal += latency

	if err == nil {
		m.successes++
		m.failures = 0
		m.lastError = ""
		if m.state == StateDegraded {
			m.toLocked(StateActive)
		}
		return
	}

	m.failures++
	m.lastError = err.Error()
	switch {
	case m.failures >= FailThreshold:
		m.toLocked(StateFailed)
	case m.failures >= DegradeThreshold:
		m.toLocked(StateDegraded)
	}
}

// snapshot returns the machine's public view.
func (m *stateMachine) snapshot() ChannelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := ChannelStatus{
		State:            m.state,
		ChangedAt:        m.changedAt,
		ConsecutiveFails: m.failures,
		Sends:            m.sends,
		Successes:        m.successes,
		LastError:        m.lastError,
		LastTestAt:       m.lastTestAt,
		LastTest:         m.lastTest,
	}
	if m.sends > 0 {
		status.SuccessRate = float64(m.successes) / float64(m.sends)
		status.AverageLatency = m.latencyTotal / time.Duration(m.sends)
	}
	return status
}
