package nodes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/security"
)

// Notification headers a wake-up push carries. The signature proves the push
// came from the control plane that issued the node's credential, so a node
// never acts on an unauthenticated poke from a third party.
const (
	// HeaderNodeID names the node the push is addressed to.
	HeaderNodeID = "X-Cashp-Node"
	// HeaderTimestamp carries the Unix second the push was signed at, which
	// bounds how long a captured push stays replayable.
	HeaderTimestamp = "X-Cashp-Timestamp"
	// HeaderSignature carries the hex HMAC-SHA256 over the timestamp, the
	// node id and the request body.
	HeaderSignature = "X-Cashp-Signature"
)

// NotifySkew is how far a wake-up push timestamp may drift before a node
// should reject it as a replay.
const NotifySkew = 5 * time.Minute

// SetCallbackURL stores the optional wake-up endpoint of a managed node.
//
// Only a managed node can have one: a cluster node shares the control-plane
// database and is reached through it, never over an outbound HTTP call. The
// URL passes the SSRF guard here and again immediately before every use, so
// a value that became unsafe after it was stored still cannot be dialled.
func (s *Service) SetCallbackURL(ctx context.Context, nodeID, rawURL, actor string) (Node, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if node.Role != RoleManaged {
		return Node{}, ErrCallbackNotAllowed
	}
	if node.State == StateRemoved {
		return Node{}, ErrInvalidTransition
	}

	clean := truncate(rawURL, MaxAddressLen)
	if clean != "" {
		if err := security.ValidateOutboundURL(clean); err != nil {
			return Node{}, ErrCallbackNotAllowed
		}
	}

	now := s.now()
	if err := s.db.UpdateVersioned(ctx,
		`UPDATE node_registry SET callback_url = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ?`,
		clean, unixOf(now), node.ID, node.Version); err != nil {
		return Node{}, wrapVersioned(err, "update node callback")
	}
	node.CallbackURL = clean
	node.UpdatedAt = now
	node.Version++

	// The URL itself is not audited: it is operator-supplied and may carry a
	// query parameter that should not be duplicated into the audit log.
	s.audit("nodes.callback_set", truncate(actor, MaxNodeNameLen), node.ID,
		"configured", clean != "")
	return node, nil
}

// notification is the body of a wake-up push. It carries no secret and no
// task payload: it only tells the node that work is waiting, and the node
// then claims it over its own authenticated channel.
type notification struct {
	// NodeID is the node being woken.
	NodeID string `json:"node_id"`
	// Event is the reason for the push.
	Event string `json:"event"`
	// IssuedAt is the Unix second the push was signed at.
	IssuedAt int64 `json:"issued_at"`
}

// Notify sends a best-effort signed wake-up push to a managed node's
// callback URL. It never carries work: the node still claims its tasks over
// the authenticated pull channel, so a failed push only delays delivery
// until the node's next poll.
func (s *Service) Notify(ctx context.Context, node Node) error {
	if s.http == nil {
		return ErrTransportUnavailable
	}
	if node.Role != RoleManaged {
		return ErrCallbackNotAllowed
	}
	if node.CallbackURL == "" {
		return ErrNoCallback
	}
	// Re-validated at use, not only at storage: the guard has to hold for the
	// value as it is about to be dialled.
	if err := security.ValidateOutboundURL(node.CallbackURL); err != nil {
		return ErrCallbackNotAllowed
	}

	key, err := s.signingKey(ctx, node.ID)
	if err != nil {
		return err
	}

	now := s.now()
	body, err := json.Marshal(notification{
		NodeID:   node.ID,
		Event:    "tasks_available",
		IssuedAt: now.Unix(),
	})
	if err != nil {
		return wrapInternal(err, "encode node notification")
	}

	reqCtx, cancel := context.WithTimeout(ctx, NotifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, node.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return ErrCallbackNotAllowed
	}
	stamp := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderNodeID, node.ID)
	req.Header.Set(HeaderTimestamp, stamp)
	req.Header.Set(HeaderSignature, SignNotification(key, stamp, node.ID, body))

	resp, err := s.http.Do(req)
	if err != nil {
		// The transport error may name an internal address, so it is not
		// returned to a caller; only the fact of failure is.
		return ErrNotifyFailed
	}
	defer func() { _ = resp.Body.Close() }()

	// A node is untrusted and may answer with an endless body, so the read is
	// bounded and the content is discarded rather than parsed.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, MaxNotifyResponseBytes)); err != nil {
		return ErrNotifyFailed
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ErrNotifyFailed
	}
	return nil
}

// SignNotification returns the hex HMAC-SHA256 a wake-up push carries. The
// key is the node's stored credential digest, which both sides already hold
// and which never travels on the wire.
func SignNotification(key, timestamp, nodeID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(nodeID))
	mac.Write([]byte{'\n'})
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyNotification checks a wake-up push signature in constant time and
// rejects a timestamp outside NotifySkew, so a captured push cannot be
// replayed indefinitely. It is exported for the agent side of the channel.
func VerifyNotification(key, timestamp, nodeID string, body []byte, signature string, now time.Time) bool {
	issued, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	drift := now.Sub(time.Unix(issued, 0))
	if drift < 0 {
		drift = -drift
	}
	if drift > NotifySkew {
		return false
	}
	return security.ConstantTimeEqualString(SignNotification(key, timestamp, nodeID, body), signature)
}

// signingKey returns the digest of the node's current live credential, which
// keys the wake-up signature. It is never returned to a caller outside this
// package and never logged.
func (s *Service) signingKey(ctx context.Context, nodeID string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT token_hash FROM node_credentials
		 WHERE node_id = ? AND revoked_at = 0
		 ORDER BY issued_at DESC, id LIMIT 1`, nodeID).Scan(&hash)
	if database.IsNotFound(err) {
		return "", ErrCredentialRejected
	}
	if err != nil {
		return "", wrapInternal(err, "load node signing key")
	}
	return hash, nil
}
