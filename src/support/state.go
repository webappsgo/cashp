package support

import (
	"sort"
	"strings"
)

// The nine ticket states. These names are stored verbatim in the database and
// returned verbatim by the API; renaming one is a breaking change.
const (
	StateDraft         = "DRAFT"
	StateOpen          = "OPEN"
	StateAssigned      = "ASSIGNED"
	StateInProgress    = "IN_PROGRESS"
	StateAwaitingUser  = "AWAITING_USER"
	StateAwaitingAgent = "AWAITING_AGENT"
	StateResolved      = "RESOLVED"
	StateClosed        = "CLOSED"
	StateReopened      = "REOPENED"
)

// Ticket priorities, ordered from least to most urgent.
const (
	PriorityLow    = "LOW"
	PriorityNormal = "NORMAL"
	PriorityHigh   = "HIGH"
	PriorityUrgent = "URGENT"
)

// Actors permitted to drive a transition. An actor is not a role: a global
// administrator acting on a ticket is an ActorAgent, because administrators
// always act as support agents and never as a distinct hierarchy level.
const (
	ActorUser   = "user"
	ActorAgent  = "agent"
	ActorSystem = "system"
)

// allStates lists every legal state in lifecycle order.
var allStates = []string{
	StateDraft,
	StateOpen,
	StateAssigned,
	StateInProgress,
	StateAwaitingUser,
	StateAwaitingAgent,
	StateResolved,
	StateClosed,
	StateReopened,
}

// allPriorities lists every legal priority from lowest to highest.
var allPriorities = []string{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}

// transition is one edge of the ticket state machine.
type transition struct {
	From string
	To   string
	// Actors that may drive this edge. A transition with only ActorSystem is
	// never reachable from an HTTP request.
	Actors []string
}

// transitions is the complete, closed set of legal ticket state changes. Any
// (from, to) pair absent from this table is rejected. There is no wildcard
// fallback and no "force" path that bypasses it.
var transitions = []transition{
	// The user submits the form the bot pre-filled. Nothing else creates an
	// OPEN ticket, and the bot itself never performs this transition.
	{From: StateDraft, To: StateOpen, Actors: []string{ActorUser}},

	// A draft the user abandons is discarded rather than closed, so DRAFT has
	// exactly one outgoing edge.

	{From: StateOpen, To: StateAssigned, Actors: []string{ActorAgent, ActorSystem}},
	{From: StateAssigned, To: StateInProgress, Actors: []string{ActorAgent}},
	{From: StateAssigned, To: StateOpen, Actors: []string{ActorAgent}},
	{From: StateInProgress, To: StateAwaitingUser, Actors: []string{ActorAgent}},
	{From: StateAwaitingUser, To: StateAwaitingAgent, Actors: []string{ActorUser}},
	{From: StateAwaitingAgent, To: StateAwaitingUser, Actors: []string{ActorAgent}},
	{From: StateAwaitingAgent, To: StateInProgress, Actors: []string{ActorAgent}},

	// An agent may resolve from any state in which work is actually under way.
	// OPEN is deliberately excluded: an unclaimed ticket must be claimed first,
	// so that a resolution always has a responsible agent recorded against it.
	{From: StateAssigned, To: StateResolved, Actors: []string{ActorAgent}},
	{From: StateInProgress, To: StateResolved, Actors: []string{ActorAgent}},
	{From: StateAwaitingUser, To: StateResolved, Actors: []string{ActorAgent}},
	{From: StateAwaitingAgent, To: StateResolved, Actors: []string{ActorAgent}},

	// The user may reopen the conversation instead of accepting the resolution.
	{From: StateResolved, To: StateAwaitingAgent, Actors: []string{ActorUser}},

	// The user confirms, or the auto-close sweep closes it once the resolution
	// has stood unchallenged for the configured window.
	{From: StateResolved, To: StateClosed, Actors: []string{ActorUser, ActorSystem}},

	{From: StateClosed, To: StateReopened, Actors: []string{ActorUser}},

	// REOPENED is a marker state the system immediately drains back into the
	// queue, which is why no actor other than the system drives this edge.
	{From: StateReopened, To: StateOpen, Actors: []string{ActorSystem}},
}

// transitionIndex keys the transition table by "FROM>TO" for constant-time lookup.
var transitionIndex = buildTransitionIndex()

func buildTransitionIndex() map[string]transition {
	idx := make(map[string]transition, len(transitions))
	for _, t := range transitions {
		idx[t.From+">"+t.To] = t
	}
	return idx
}

// States returns every legal ticket state in lifecycle order.
func States() []string {
	out := make([]string, len(allStates))
	copy(out, allStates)
	return out
}

// Priorities returns every legal priority, lowest first.
func Priorities() []string {
	out := make([]string, len(allPriorities))
	copy(out, allPriorities)
	return out
}

// IsState reports whether s is one of the nine legal ticket states.
func IsState(s string) bool {
	for _, v := range allStates {
		if v == s {
			return true
		}
	}
	return false
}

// IsPriority reports whether p is one of the four legal priorities.
func IsPriority(p string) bool {
	for _, v := range allPriorities {
		if v == p {
			return true
		}
	}
	return false
}

// PriorityRank returns an ordering value for a priority, lowest first. An
// unknown priority ranks alongside NORMAL so that a corrupted row still sorts
// sensibly instead of jumping to the top of an agent's queue.
func PriorityRank(p string) int {
	for i, v := range allPriorities {
		if v == p {
			return i
		}
	}
	return 1
}

// IsOpenState reports whether a ticket in this state is still live work. Closed
// tickets are read-only; drafts have not entered the queue yet.
func IsOpenState(s string) bool {
	switch s {
	case StateOpen, StateAssigned, StateInProgress, StateAwaitingUser, StateAwaitingAgent, StateReopened:
		return true
	}
	return false
}

// IsQueueState reports whether a ticket in this state waits on the support team
// rather than on the tenant. These are the states an agent queue lists.
func IsQueueState(s string) bool {
	switch s {
	case StateOpen, StateAssigned, StateInProgress, StateAwaitingAgent, StateReopened:
		return true
	}
	return false
}

// CanTransition reports whether actor may move a ticket from one state to
// another. It is the single authority for the state machine: every write path
// consults it, and no path may work around it.
func CanTransition(from, to, actor string) bool {
	t, ok := transitionIndex[from+">"+to]
	if !ok {
		return false
	}
	for _, a := range t.Actors {
		if a == actor {
			return true
		}
	}
	return false
}

// NextStates lists the states actor may move a ticket to from the given state,
// sorted so that the agent workspace renders its controls in a stable order.
func NextStates(from, actor string) []string {
	var out []string
	for _, t := range transitions {
		if t.From != from {
			continue
		}
		for _, a := range t.Actors {
			if a == actor {
				out = append(out, t.To)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// TransitionError describes a rejected state change without leaking anything
// about the ticket beyond the two state names the caller already supplied.
type TransitionError struct {
	From  string
	To    string
	Actor string
}

func (e *TransitionError) Error() string {
	var b strings.Builder
	b.WriteString("support: ")
	if !IsState(e.From) || !IsState(e.To) {
		b.WriteString("unknown ticket state")
		return b.String()
	}
	b.WriteString(e.From)
	b.WriteString(" cannot become ")
	b.WriteString(e.To)
	return b.String()
}
