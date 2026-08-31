package notify

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/errors"
)

// Channel categories used to group the plugins in the admin panel and in
// the provider comparison table.
const (
	// CategoryEmail holds the SMTP channel.
	CategoryEmail = "email"
	// CategoryInApp holds the always-available WebUI channel.
	CategoryInApp = "in_app"
	// CategoryChat holds the team-chat webhook transports.
	CategoryChat = "chat"
	// CategoryPush holds the mobile push webhook transports.
	CategoryPush = "push"
	// CategoryGeneric holds the raw JSON webhook transport.
	CategoryGeneric = "generic"
)

// Channel is one notification transport plugin. Every channel is
// self-contained: it validates its own configuration, proves itself with a
// live test, ships its own help content and reports its own health.
type Channel interface {
	// Name is the unique channel identifier, for example "smtp".
	Name() string
	// Category is one of the Category* channel constants.
	Category() string
	// Validate reports whether the current configuration is complete enough
	// to attempt a delivery.
	Validate() error
	// Test performs a live connectivity check and returns an itemised
	// result for the admin panel.
	Test(ctx context.Context) TestResult
	// Send delivers one rendered notification.
	Send(ctx context.Context, r Rendered) error
	// AutoEnable reports whether a passing test activates the channel
	// without a separate administrator action.
	AutoEnable() bool
	// Accepts reports whether this channel handles the given rendered
	// message at all, before any state or preference check.
	Accepts(r Rendered) bool
	// ConfigSchema describes the configuration fields for the admin form,
	// including the contextual help behind each field's [?] control.
	ConfigSchema() []Field
	// Help returns the channel's setup guide, troubleshooting entries and
	// comparison metadata.
	Help() Help
}

// Field describes one configuration input in the admin panel. Help is not
// optional: PART 18's admin panel requires a [?] control on every field.
type Field struct {
	// Name is the config key this field writes.
	Name string
	// Label is the form label.
	Label string
	// Kind is one of "text", "password", "number", "select" or "url".
	Kind string
	// Required marks a field the channel cannot work without.
	Required bool
	// Secret marks a value that must be masked in every response.
	Secret bool
	// Options lists the accepted values for a "select" field.
	Options []string
	// Placeholder is the greyed-out sample value.
	Placeholder string
	// Help is the [?] tooltip: what the field does and where to find it.
	Help string
	// Example is a concrete sample value shown under the tooltip.
	Example string
	// Security is the tooltip's handling note for sensitive values.
	Security string
	// EnvVar names the environment variable that pre-fills this field. Only
	// the SMTP channel sets it; PART 18 grants env var support to SMTP
	// alone.
	EnvVar string
}

// Help is a channel's embedded documentation. Nothing here links out: the
// admin panel renders these values directly so an air-gapped operator can
// still set the channel up.
type Help struct {
	// Summary is the one-line description in the channel list.
	Summary string
	// Setup is the ordered step-by-step guide for obtaining credentials.
	Setup []string
	// Troubleshooting maps a symptom to its resolution.
	Troubleshooting []HelpEntry
	// Comparison is the row this channel contributes to the provider
	// comparison table.
	Comparison Comparison
}

// HelpEntry is one troubleshooting symptom and its fix.
type HelpEntry struct {
	// Symptom is the error or behaviour the operator sees.
	Symptom string
	// Resolution is what to change.
	Resolution string
}

// Comparison is the provider comparison row for one channel.
type Comparison struct {
	// Speed is the typical delivery latency band: "instant", "seconds" or
	// "minutes".
	Speed string
	// Reliability is the dependability tier: "high", "medium" or "low".
	Reliability string
	// RequiresAccount reports whether a third-party account is needed.
	RequiresAccount bool
	// Pricing is the cost model: "free", "freemium" or "paid".
	Pricing string
}

// ChannelStatus is the health and metrics view of one channel.
type ChannelStatus struct {
	// Name is the channel identifier.
	Name string
	// Category is the channel category.
	Category string
	// State is the current lifecycle state.
	State State
	// ChangedAt is when the state last changed.
	ChangedAt time.Time
	// ConsecutiveFails is the current failure streak.
	ConsecutiveFails int
	// Sends is the total delivery attempts since start.
	Sends int64
	// Successes is the number of those that succeeded.
	Successes int64
	// SuccessRate is Successes over Sends, or zero with no sends.
	SuccessRate float64
	// AverageLatency is the mean attempt duration.
	AverageLatency time.Duration
	// LastError is the most recent failure message, already redacted.
	LastError string
	// LastTestAt is when Test last ran.
	LastTestAt time.Time
	// LastTest is the last itemised test result.
	LastTest TestResult
}

// Registry errors.
var (
	// ErrChannelNotFound names a channel that was never registered.
	ErrChannelNotFound = errors.New(errors.CodeNotFound, http.StatusNotFound, "notification channel not found")
	// ErrChannelExists rejects a duplicate registration.
	ErrChannelExists = errors.New(errors.CodeConflict, http.StatusConflict, "notification channel already registered")
	// ErrBadTransition rejects a lifecycle jump the state machine forbids.
	ErrBadTransition = errors.New(errors.CodeConflict, http.StatusConflict, "notification channel state transition not allowed")
	// ErrChannelNotActive rejects a delivery through a channel that is not
	// in the ACTIVE state.
	ErrChannelNotActive = errors.New(errors.CodeUnavailable, http.StatusServiceUnavailable, "notification channel is not active")
	// ErrNotTested rejects activating a channel that has not passed a test.
	ErrNotTested = errors.New(errors.CodeValidation, http.StatusBadRequest, "notification channel must pass a test before activation")
)

// Registry holds every registered channel with its state machine. It is
// safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	order []string
	items map[string]*registryEntry
	now   func() time.Time
}

// registryEntry pairs a channel with its lifecycle state.
type registryEntry struct {
	channel Channel
	machine *stateMachine
}

// NewRegistry returns an empty registry. now may be nil, in which case
// time.Now is used; tests inject a fixed clock through it.
func NewRegistry(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{items: map[string]*registryEntry{}, now: now}
}

// Register adds a channel in StateDisabled. Every channel starts disabled;
// activation always runs through Configure, Test and Activate.
func (r *Registry) Register(ch Channel) error {
	if ch == nil {
		return errors.New(errors.CodeInternal, http.StatusInternalServerError, "notification channel must not be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.items[ch.Name()]; dup {
		return ErrChannelExists.WithDetails(map[string]any{"channel": ch.Name()})
	}
	r.items[ch.Name()] = &registryEntry{channel: ch, machine: newStateMachine(r.now)}
	r.order = append(r.order, ch.Name())
	sort.Strings(r.order)
	return nil
}

// Names returns the registered channel names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Channel returns a registered channel by name.
func (r *Registry) Channel(name string) (Channel, error) {
	entry, err := r.entry(name)
	if err != nil {
		return nil, err
	}
	return entry.channel, nil
}

// State returns a channel's current state.
func (r *Registry) State(name string) (State, error) {
	entry, err := r.entry(name)
	if err != nil {
		return "", err
	}
	return entry.machine.State(), nil
}

// Status returns the health and metrics view of one channel.
func (r *Registry) Status(name string) (ChannelStatus, error) {
	entry, err := r.entry(name)
	if err != nil {
		return ChannelStatus{}, err
	}
	status := entry.machine.snapshot()
	status.Name = entry.channel.Name()
	status.Category = entry.channel.Category()
	return status, nil
}

// Statuses returns every channel's status, sorted by name, for the channel
// health dashboard.
func (r *Registry) Statuses() []ChannelStatus {
	out := make([]ChannelStatus, 0, len(r.Names()))
	for _, name := range r.Names() {
		if status, err := r.Status(name); err == nil {
			out = append(out, status)
		}
	}
	return out
}

// Configure moves a channel into CONFIGURING, which is the only entry point
// to the activation path. It is called when an administrator opens the
// channel's configuration panel or when a configuration change is saved.
func (r *Registry) Configure(name string) error {
	return r.transition(name, StateConfiguring)
}

// Disable parks a channel in DISABLED from any state.
func (r *Registry) Disable(name string) error {
	return r.transition(name, StateDisabled)
}

// Maintenance parks a channel for planned downtime.
func (r *Registry) Maintenance(name string) error {
	return r.transition(name, StateMaintenance)
}

// Activate moves a tested channel to ACTIVE. It refuses a channel that has
// not passed a test, which is what keeps DISABLED from reaching ACTIVE in
// one step.
func (r *Registry) Activate(name string) error {
	entry, err := r.entry(name)
	if err != nil {
		return err
	}
	if !entry.machine.snapshot().LastTest.OK() {
		return ErrNotTested.WithDetails(map[string]any{"channel": name})
	}
	if !entry.machine.To(StateActive) {
		return ErrBadTransition.WithDetails(map[string]any{"channel": name, "state": string(entry.machine.State())})
	}
	return nil
}

// Test validates the configuration, runs the channel's live check and
// applies the resulting transition. A channel whose AutoEnable reports true
// becomes ACTIVE on success; the rest wait in TESTING for Activate.
func (r *Registry) Test(ctx context.Context, name string) (TestResult, error) {
	entry, err := r.entry(name)
	if err != nil {
		return TestResult{}, err
	}

	if err := entry.channel.Validate(); err != nil {
		res := TestResult{Detail: "configuration incomplete", Err: err}
		entry.machine.recordTest(res, entry.channel.AutoEnable())
		return res, err
	}

	// CONFIGURING is the only legal predecessor of TESTING, so a channel
	// tested straight from DISABLED is walked through it first.
	if entry.machine.State() == StateDisabled {
		entry.machine.To(StateConfiguring)
	}
	if !entry.machine.To(StateTesting) {
		return TestResult{}, ErrBadTransition.WithDetails(map[string]any{"channel": name, "state": string(entry.machine.State())})
	}

	res := entry.channel.Test(ctx)
	entry.machine.recordTest(res, entry.channel.AutoEnable())
	return res, res.Err
}

// Deliver sends one rendered notification through a named channel. It
// refuses any channel that is not ACTIVE or DEGRADED — a degraded channel
// is still tried so it has a chance to recover — and folds the outcome into
// the channel's health.
func (r *Registry) Deliver(ctx context.Context, name string, rendered Rendered) error {
	entry, err := r.entry(name)
	if err != nil {
		return err
	}

	switch entry.machine.State() {
	case StateActive, StateDegraded:
	default:
		return ErrChannelNotActive.WithDetails(map[string]any{"channel": name, "state": string(entry.machine.State())})
	}

	start := r.now()
	sendErr := entry.channel.Send(ctx, rendered)
	entry.machine.recordSend(r.now().Sub(start), sendErr)
	return sendErr
}

// Active returns the names of the channels currently able to deliver.
func (r *Registry) Active() []string {
	var out []string
	for _, name := range r.Names() {
		if state, err := r.State(name); err == nil && (state == StateActive || state == StateDegraded) {
			out = append(out, name)
		}
	}
	return out
}

// transition applies a state change by name.
func (r *Registry) transition(name string, next State) error {
	entry, err := r.entry(name)
	if err != nil {
		return err
	}
	if !entry.machine.To(next) {
		return ErrBadTransition.WithDetails(map[string]any{"channel": name, "from": string(entry.machine.State()), "to": string(next)})
	}
	return nil
}

// entry looks up a registry entry or returns ErrChannelNotFound.
func (r *Registry) entry(name string) (*registryEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.items[name]
	if !ok {
		return nil, ErrChannelNotFound.WithDetails(map[string]any{"channel": name})
	}
	return entry, nil
}
