package nodes

import (
	apperr "github.com/webappsgo/cashp/src/errors"
)

// Sentinel errors returned by this package. Every message is written for an
// operator or a tenant: none of them names an internal address, a token, a
// command line or a driver error. Enrollment and authentication failures are
// deliberately coarse so a caller cannot distinguish "unknown token" from
// "expired token" from "revoked token" by probing.
var (
	// ErrNodeNotFound is returned when no node matches the given id.
	ErrNodeNotFound = apperr.New(apperr.CodeNotFound, 0, "Node not found")
	// ErrNodeExists is returned when a node id or name is already taken.
	ErrNodeExists = apperr.New(apperr.CodeConflict, 0, "Node already exists")
	// ErrInvalidNodeID is returned for an id outside the allowlist.
	ErrInvalidNodeID = apperr.New(apperr.CodeValidation, 0, "Node ID is invalid")
	// ErrInvalidNodeName is returned for a name outside the allowlist.
	ErrInvalidNodeName = apperr.New(apperr.CodeValidation, 0, "Node name is invalid")
	// ErrInvalidRole is returned when a role is neither cluster nor managed.
	ErrInvalidRole = apperr.New(apperr.CodeValidation, 0, "Node role is invalid")
	// ErrInvalidFacts is returned when a node reports facts that fail
	// validation or exceed their size bound.
	ErrInvalidFacts = apperr.New(apperr.CodeValidation, 0, "Reported node facts are invalid")
	// ErrConcurrentUpdate is returned when another writer changed the node
	// row first and the optimistic-locking guard rejected this write.
	ErrConcurrentUpdate = apperr.New(apperr.CodeConflict, 0, "Node was changed by another request")
	// ErrInvalidTransition is returned when a lifecycle transition is not
	// permitted from the node's current state.
	ErrInvalidTransition = apperr.New(apperr.CodeConflict, 0, "Node state change is not allowed")
	// ErrEnrollmentRejected is the single answer to every unusable
	// enrollment token: unknown, malformed, expired, revoked or exhausted.
	ErrEnrollmentRejected = apperr.New(apperr.CodeTokenInvalid, 0, "Enrollment token is invalid or expired")
	// ErrCredentialRejected is the single answer to every unusable node
	// credential.
	ErrCredentialRejected = apperr.New(apperr.CodeUnauthorized, 0, "Node credentials are invalid or expired")
	// ErrNotClusterNode is returned when a managed node attempts an
	// operation reserved for the control plane.
	ErrNotClusterNode = apperr.New(apperr.CodeForbidden, 0, "This operation is limited to cluster nodes")
	// ErrActionNotAllowed is returned when a node's role may not perform the
	// requested action.
	ErrActionNotAllowed = apperr.New(apperr.CodeForbidden, 0, "Node is not allowed to perform this action")
	// ErrUnknownAction is returned for an action name that is not registered.
	ErrUnknownAction = apperr.New(apperr.CodeValidation, 0, "Unknown node action")
	// ErrPayloadTooLarge is returned when a dispatch payload exceeds the
	// action's bound.
	ErrPayloadTooLarge = apperr.New(apperr.CodePayloadTooLarge, 0, "Node task payload is too large")
	// ErrInvalidPayload is returned when a dispatch payload is not a JSON
	// object.
	ErrInvalidPayload = apperr.New(apperr.CodeValidation, 0, "Node task payload must be a JSON object")
	// ErrNodeNotSchedulable is returned when work is dispatched to a node
	// that is cordoned, in maintenance, drained or removed.
	ErrNodeNotSchedulable = apperr.New(apperr.CodeConflict, 0, "Node is not accepting new work")
	// ErrTaskNotFound is returned when no task matches the given id for the
	// reporting node.
	ErrTaskNotFound = apperr.New(apperr.CodeNotFound, 0, "Node task not found")
	// ErrTaskNotClaimed is returned when a node reports a result for a task
	// it does not currently hold.
	ErrTaskNotClaimed = apperr.New(apperr.CodeConflict, 0, "Node task is not currently dispatched")
	// ErrConfirmationRequired is returned when removal is attempted without
	// the explicit confirmation flag.
	ErrConfirmationRequired = apperr.New(apperr.CodeValidation, 0, "Removal requires explicit confirmation")
	// ErrCallbackNotAllowed is returned when a node's callback URL fails the
	// SSRF guard.
	ErrCallbackNotAllowed = apperr.New(apperr.CodeValidation, 0, "Callback URL is not allowed")
	// ErrNoCallback is returned when a wake-up push is requested for a node
	// that has no callback URL configured.
	ErrNoCallback = apperr.New(apperr.CodeConflict, 0, "Node has no callback URL configured")
	// ErrTransportUnavailable is returned when a wake-up push is requested
	// but no HTTP transport was supplied to the service.
	ErrTransportUnavailable = apperr.New(apperr.CodeUnavailable, 0, "Outbound node transport is not configured")
	// ErrNotifyFailed is returned when a node rejected or failed a wake-up
	// push. It never carries the node's address or response body.
	ErrNotifyFailed = apperr.New(apperr.CodeUnavailable, 0, "Node did not accept the notification")
)
