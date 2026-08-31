package nodes

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/webappsgo/cashp/src/database"
	"github.com/webappsgo/cashp/src/security"
)

// EnrollmentToken is an issued join credential as stored. It never carries
// the secret: only the SHA-256 digest is persisted and only the short
// display prefix is ever rendered.
type EnrollmentToken struct {
	// ID is the token record identifier.
	ID string
	// Role is the role the redeeming node will be created with.
	Role Role
	// NodeID binds the token to an existing node for rejoin or re-key. It is
	// empty for a token that enrolls a brand new node.
	NodeID string
	// DisplayPrefix is the leading characters kept for display.
	DisplayPrefix string
	// MaxUses is the redemption budget.
	MaxUses int64
	// Uses is how many times the token has been redeemed.
	Uses int64
	// ExpiresAt is when the token stops being redeemable.
	ExpiresAt time.Time
	// RevokedAt is when the token was revoked or exhausted; zero while live.
	RevokedAt time.Time
	// CreatedBy is the actor who issued the token.
	CreatedBy string
	// CreatedAt is when the token was issued.
	CreatedAt time.Time
}

// Display renders the token for an API or UI payload: the display prefix
// followed by the standard mask. The secret itself is unavailable here by
// construction, so no caller can leak it.
func (t EnrollmentToken) Display() string {
	return t.DisplayPrefix + security.MaskedValue
}

// Live reports whether the token can still be redeemed at the given time.
func (t EnrollmentToken) Live(now time.Time) bool {
	if !t.RevokedAt.IsZero() {
		return false
	}
	if !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt) {
		return false
	}
	return t.Uses < t.MaxUses
}

// EnrollmentRequest asks for a new enrollment token.
type EnrollmentRequest struct {
	// Role is the role the token grants. Required.
	Role Role
	// NodeID binds the token to an existing node so it can rejoin or re-key
	// without being deleted. Empty enrolls a new node.
	NodeID string
	// MaxUses is the redemption budget. Zero means single use.
	MaxUses int64
	// TTL is how long the token stays redeemable. Zero uses
	// DefaultEnrollmentTTL and the value is capped at MaxEnrollmentTTL.
	TTL time.Duration
	// Actor is the administrator issuing the token, recorded in the audit
	// log.
	Actor string
}

// IssuedEnrollment carries a freshly issued token. Secret is shown exactly
// once, at issue time, and is never stored or logged.
type IssuedEnrollment struct {
	// Token is the stored record.
	Token EnrollmentToken
	// Secret is the plaintext join credential, shown once.
	Secret string
}

// EnrollRequest is what a joining node presents.
type EnrollRequest struct {
	// Secret is the enrollment token the node was given.
	Secret string
	// NodeID is the identifier the node asks for. Ignored when the token is
	// bound to an existing node.
	NodeID string
	// Name is the operator-facing display name. Defaults to NodeID.
	Name string
	// Address is the reachable host:port the node reports.
	Address string
	// AgentVersion is the node's binary version.
	AgentVersion string
	// Facts is the node's self-reported inventory.
	Facts Facts
}

// Enrollment is the result of a successful join.
type Enrollment struct {
	// Node is the registry record after enrollment.
	Node Node
	// Credential is the long-lived node credential, shown exactly once.
	Credential string
	// CredentialExpiresAt is when the credential lapses; zero when it does
	// not expire on its own.
	CredentialExpiresAt time.Time
}

// Identity is an authenticated node.
type Identity struct {
	// Node is the authenticated node record.
	Node Node
	// CredentialID identifies the credential that authenticated the call.
	CredentialID string
}

// enrollmentColumns is the fixed column list every enrollment-token read
// uses.
const enrollmentColumns = `id, token_hash, display_prefix, role, node_id,
	max_uses, uses, expires_at, revoked_at, created_by, created_at`

// IssueEnrollmentToken mints a join credential for a role. The plaintext is
// returned once and only its SHA-256 digest is stored.
func (s *Service) IssueEnrollmentToken(ctx context.Context, req EnrollmentRequest) (IssuedEnrollment, error) {
	if !req.Role.Valid() {
		return IssuedEnrollment{}, ErrInvalidRole
	}
	if req.NodeID != "" {
		node, err := s.Get(ctx, req.NodeID)
		if err != nil {
			return IssuedEnrollment{}, err
		}
		if node.Role != req.Role {
			return IssuedEnrollment{}, ErrInvalidRole
		}
		if node.State == StateRemoved {
			return IssuedEnrollment{}, ErrInvalidTransition
		}
	}

	uses := req.MaxUses
	if uses <= 0 {
		uses = DefaultEnrollmentUses
	}
	if uses > MaxEnrollmentUses {
		uses = MaxEnrollmentUses
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = DefaultEnrollmentTTL
	}
	if ttl > MaxEnrollmentTTL {
		ttl = MaxEnrollmentTTL
	}

	secret, hash, err := security.GenerateToken(TokenPrefixFor(req.Role))
	if err != nil {
		return IssuedEnrollment{}, wrapInternal(err, "generate enrollment token")
	}
	id, err := newID()
	if err != nil {
		return IssuedEnrollment{}, wrapInternal(err, "generate enrollment token id")
	}

	now := s.now()
	token := EnrollmentToken{
		ID:            id,
		Role:          req.Role,
		NodeID:        req.NodeID,
		DisplayPrefix: security.TokenDisplayPrefix(secret),
		MaxUses:       uses,
		ExpiresAt:     now.Add(ttl),
		CreatedBy:     truncate(req.Actor, MaxNodeNameLen),
		CreatedAt:     now,
	}

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`INSERT INTO node_enrollment_tokens (`+enrollmentColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?)`,
		token.ID, hash, token.DisplayPrefix, string(token.Role), token.NodeID,
		token.MaxUses, unixOf(token.ExpiresAt), token.CreatedBy,
		unixOf(token.CreatedAt)); err != nil {
		return IssuedEnrollment{}, wrapInternal(err, "store enrollment token")
	}

	s.audit("nodes.enrollment_token_issued", token.CreatedBy, token.NodeID,
		"token_id", token.ID, "role", string(token.Role),
		"max_uses", token.MaxUses, "expires_at", token.ExpiresAt.Format(time.RFC3339))

	return IssuedEnrollment{Token: token, Secret: secret}, nil
}

// Rekey issues a fresh enrollment token bound to an existing node so it can
// re-key or rejoin without the record being deleted. Its previous
// credentials stay valid until the new token is redeemed, so a re-key that
// is never completed cannot lock the node out.
func (s *Service) Rekey(ctx context.Context, nodeID, actor string) (IssuedEnrollment, error) {
	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return IssuedEnrollment{}, err
	}
	issued, err := s.IssueEnrollmentToken(ctx, EnrollmentRequest{
		Role:   node.Role,
		NodeID: node.ID,
		Actor:  actor,
	})
	if err != nil {
		return IssuedEnrollment{}, err
	}
	s.audit("nodes.rekey_requested", truncate(actor, MaxNodeNameLen), node.ID,
		"token_id", issued.Token.ID, "role", string(node.Role))
	return issued, nil
}

// RevokeEnrollmentToken revokes a token by id. Revoking an already revoked
// token is a no-op so the call is safe to retry.
func (s *Service) RevokeEnrollmentToken(ctx context.Context, tokenID, actor string) error {
	now := s.now()
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE node_enrollment_tokens SET revoked_at = ?
		 WHERE id = ? AND revoked_at = 0`, unixOf(now), tokenID)
	if err != nil {
		return wrapInternal(err, "revoke enrollment token")
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return wrapInternal(err, "revoke enrollment token")
	}
	if rows == 0 {
		return nil
	}
	s.audit("nodes.enrollment_token_revoked", truncate(actor, MaxNodeNameLen), "",
		"token_id", tokenID)
	return nil
}

// ListEnrollmentTokens returns every token record, newest first. The result
// is inherently masked: no stored field can reconstruct a secret.
func (s *Service) ListEnrollmentTokens(ctx context.Context) ([]EnrollmentToken, error) {
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT `+enrollmentColumns+` FROM node_enrollment_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, wrapInternal(err, "list enrollment tokens")
	}
	defer func() { _ = rows.Close() }()

	var out []EnrollmentToken
	for rows.Next() {
		token, _, err := scanEnrollment(rows)
		if err != nil {
			return nil, wrapInternal(err, "scan enrollment token")
		}
		out = append(out, token)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapInternal(err, "list enrollment tokens")
	}
	return out, nil
}

// ExpireEnrollmentTokens marks every lapsed token revoked so an expired
// secret can never be redeemed even if the expiry comparison were ever
// bypassed. It returns how many rows it closed.
func (s *Service) ExpireEnrollmentTokens(ctx context.Context) (int64, error) {
	now := unixOf(s.now())
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE node_enrollment_tokens SET revoked_at = ?
		 WHERE revoked_at = 0 AND expires_at > 0 AND expires_at <= ?`, now, now)
	if err != nil {
		return 0, wrapInternal(err, "expire enrollment tokens")
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, wrapInternal(err, "expire enrollment tokens")
	}
	if rows > 0 {
		s.audit("nodes.enrollment_tokens_expired", "scheduler", "", "count", rows)
	}
	return rows, nil
}

// scanEnrollment reads one enrollment-token row and returns the record plus
// its stored hash, which stays inside this package.
func scanEnrollment(sc rowScanner) (EnrollmentToken, string, error) {
	var (
		token     EnrollmentToken
		hash      string
		role      string
		expiresAt int64
		revokedAt int64
		createdAt int64
	)
	err := sc.Scan(&token.ID, &hash, &token.DisplayPrefix, &role, &token.NodeID,
		&token.MaxUses, &token.Uses, &expiresAt, &revokedAt, &token.CreatedBy,
		&createdAt)
	if err != nil {
		return EnrollmentToken{}, "", err
	}
	token.Role = Role(role)
	token.ExpiresAt = unixOrZero(expiresAt)
	token.RevokedAt = unixOrZero(revokedAt)
	token.CreatedAt = unixOrZero(createdAt)
	return token, hash, nil
}

// Enroll redeems an enrollment token and returns the node's long-lived
// credential. It is the only path that creates a node record. Every failure
// mode answers with the same coarse ErrEnrollmentRejected so the endpoint
// cannot be used to probe which tokens exist.
func (s *Service) Enroll(ctx context.Context, req EnrollRequest) (Enrollment, error) {
	if _, _, err := security.ParseToken(req.Secret); err != nil {
		return Enrollment{}, ErrEnrollmentRejected
	}
	facts, err := ValidateFacts(req.Facts)
	if err != nil {
		return Enrollment{}, err
	}

	hash := security.HashToken(req.Secret)
	now := s.now()

	var (
		result     Enrollment
		credential string
		rejoin     bool
	)

	txErr := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		row := s.db.TxQueryRow(ctx, tx,
			`SELECT `+enrollmentColumns+` FROM node_enrollment_tokens WHERE token_hash = ?`, hash)
		token, storedHash, err := scanEnrollment(row)
		if database.IsNotFound(err) {
			return ErrEnrollmentRejected
		}
		if err != nil {
			return wrapInternal(err, "load enrollment token")
		}
		if !security.ConstantTimeEqualString(storedHash, hash) {
			return ErrEnrollmentRejected
		}
		if !token.Live(now) {
			return ErrEnrollmentRejected
		}
		if !token.Role.Valid() {
			return ErrEnrollmentRejected
		}

		nodeID := token.NodeID
		rejoin = nodeID != ""
		if !rejoin {
			nodeID = strings.ToLower(strings.TrimSpace(req.NodeID))
			if err := ValidateNodeID(nodeID); err != nil {
				return err
			}
		}

		name := truncate(req.Name, MaxNodeNameLen)
		if name == "" {
			name = nodeID
		}
		if err := ValidateNodeName(name); err != nil {
			return err
		}
		address := truncate(req.Address, MaxAddressLen)
		agentVersion := truncate(req.AgentVersion, MaxVersionLen)
		if agentVersion != "" && !isPrintableToken(agentVersion) {
			return ErrInvalidFacts
		}

		node, err := s.enrollNodeRow(ctx, tx, token, nodeID, name, address, agentVersion, now, rejoin)
		if err != nil {
			return err
		}

		if err := s.storeFacts(ctx, tx, node.ID, facts, now); err != nil {
			return err
		}

		plaintext, err := s.issueCredential(ctx, tx, node, now)
		if err != nil {
			return err
		}
		credential = plaintext

		if err := s.consumeEnrollment(ctx, tx, token, now); err != nil {
			return err
		}

		result.Node = node
		return nil
	})
	if txErr != nil {
		return Enrollment{}, txErr
	}

	result.Credential = credential
	if s.credTTL > 0 {
		result.CredentialExpiresAt = now.Add(s.credTTL)
	}

	event := "nodes.enrolled"
	if rejoin {
		event = "nodes.rejoined"
	}
	s.audit(event, "node", result.Node.ID,
		"role", string(result.Node.Role), "state", string(result.Node.State),
		"agent_version", result.Node.AgentVersion)

	return result, nil
}

// enrollNodeRow creates a new node record or refreshes an existing one for
// a rejoin, returning the record as stored.
func (s *Service) enrollNodeRow(ctx context.Context, tx *sql.Tx, token EnrollmentToken,
	nodeID, name, address, agentVersion string, now time.Time, rejoin bool) (Node, error) {

	if rejoin {
		row := s.db.TxQueryRow(ctx, tx,
			`SELECT `+nodeColumns+` FROM node_registry WHERE id = ?`, nodeID)
		node, err := scanNodeRow(row)
		if database.IsNotFound(err) {
			return Node{}, ErrNodeNotFound
		}
		if err != nil {
			return Node{}, wrapInternal(err, "load node")
		}
		if node.Role != token.Role {
			return Node{}, ErrInvalidRole
		}
		if !CanTransition(node.State, StateEnrolled) {
			return Node{}, ErrInvalidTransition
		}
		if _, err := s.db.TxExec(ctx, tx,
			`UPDATE node_registry SET name = ?, address = ?, agent_version = ?,
				state = ?, state_reason = ?, state_changed_at = ?, enrolled_at = ?,
				last_seen = ?, updated_at = ?, version = version + 1
			 WHERE id = ? AND version = ?`,
			name, address, agentVersion, string(StateEnrolled), "rejoined",
			unixOf(now), unixOf(now), unixOf(now), unixOf(now), node.ID, node.Version); err != nil {
			return Node{}, wrapInternal(err, "update node")
		}
		node.Name = name
		node.Address = address
		node.AgentVersion = agentVersion
		node.State = StateEnrolled
		node.StateReason = "rejoined"
		node.StateChangedAt = now
		node.EnrolledAt = now
		node.LastSeen = now
		node.UpdatedAt = now
		node.Version++
		return node, nil
	}

	var existing string
	err := s.db.TxQueryRow(ctx, tx,
		`SELECT id FROM node_registry WHERE id = ?`, nodeID).Scan(&existing)
	switch {
	case err == nil:
		return Node{}, ErrNodeExists
	case !database.IsNotFound(err):
		return Node{}, wrapInternal(err, "check node id")
	}

	node := Node{
		ID:             nodeID,
		Name:           name,
		Role:           token.Role,
		State:          StateEnrolled,
		StateReason:    "enrolled",
		Address:        address,
		AgentVersion:   agentVersion,
		EnrolledAt:     now,
		LastSeen:       now,
		StateChangedAt: now,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if _, err := s.db.TxExec(ctx, tx,
		`INSERT INTO node_registry (`+nodeColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, '', ?, 0, 0, ?, ?, ?, ?, ?, 1)`,
		node.ID, node.Name, string(node.Role), string(node.State), node.StateReason,
		node.Address, node.AgentVersion, unixOf(node.EnrolledAt), unixOf(node.LastSeen),
		unixOf(node.StateChangedAt), unixOf(node.CreatedAt), unixOf(node.UpdatedAt)); err != nil {
		return Node{}, wrapInternal(err, "create node")
	}
	return node, nil
}

// consumeEnrollment increments the token's use count and revokes it once the
// budget is exhausted, which is what makes a single-use token single use.
func (s *Service) consumeEnrollment(ctx context.Context, tx *sql.Tx, token EnrollmentToken, now time.Time) error {
	uses := token.Uses + 1
	revokedAt := int64(0)
	if uses >= token.MaxUses {
		revokedAt = unixOf(now)
	}
	res, err := s.db.TxExec(ctx, tx,
		`UPDATE node_enrollment_tokens SET uses = ?, revoked_at = ?
		 WHERE id = ? AND uses = ? AND revoked_at = 0`,
		uses, revokedAt, token.ID, token.Uses)
	if err != nil {
		return wrapInternal(err, "consume enrollment token")
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return wrapInternal(err, "consume enrollment token")
	}
	if rows == 0 {
		// Another enrollment redeemed the same token concurrently.
		return ErrEnrollmentRejected
	}
	return nil
}

// issueCredential mints the node's long-lived credential, revoking every
// previous credential for that node in the same transaction so a re-key
// retires the old secret atomically.
func (s *Service) issueCredential(ctx context.Context, tx *sql.Tx, node Node, now time.Time) (string, error) {
	if _, err := s.db.TxExec(ctx, tx,
		`UPDATE node_credentials SET revoked_at = ? WHERE node_id = ? AND revoked_at = 0`,
		unixOf(now), node.ID); err != nil {
		return "", wrapInternal(err, "revoke previous credentials")
	}

	secret, hash, err := security.GenerateToken(TokenPrefixFor(node.Role))
	if err != nil {
		return "", wrapInternal(err, "generate node credential")
	}
	id, err := newID()
	if err != nil {
		return "", wrapInternal(err, "generate credential id")
	}
	expires := int64(0)
	if s.credTTL > 0 {
		expires = unixOf(now.Add(s.credTTL))
	}
	if _, err := s.db.TxExec(ctx, tx,
		`INSERT INTO node_credentials
			(id, node_id, token_hash, display_prefix, issued_at, expires_at, revoked_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0)`,
		id, node.ID, hash, security.TokenDisplayPrefix(secret), unixOf(now), expires); err != nil {
		return "", wrapInternal(err, "store node credential")
	}
	return secret, nil
}

// Authenticate resolves a presented node credential to an identity. It
// answers with the same coarse error for every failure so the endpoint
// cannot be used to enumerate nodes or credentials, and it compares the
// stored digest in constant time.
func (s *Service) Authenticate(ctx context.Context, secret string) (Identity, error) {
	prefix, _, err := security.ParseToken(secret)
	if err != nil {
		return Identity{}, ErrCredentialRejected
	}
	if prefix != ClusterTokenPrefix && prefix != ManagedTokenPrefix {
		return Identity{}, ErrCredentialRejected
	}

	hash := security.HashToken(secret)
	now := s.now()

	var (
		credID    string
		nodeID    string
		stored    string
		expiresAt int64
		revokedAt int64
	)
	err = s.db.QueryRowContext(ctx, database.TimeoutSelect,
		`SELECT id, node_id, token_hash, expires_at, revoked_at
		 FROM node_credentials WHERE token_hash = ?`, hash).
		Scan(&credID, &nodeID, &stored, &expiresAt, &revokedAt)
	if database.IsNotFound(err) {
		return Identity{}, ErrCredentialRejected
	}
	if err != nil {
		return Identity{}, wrapInternal(err, "load node credential")
	}
	if !security.ConstantTimeEqualString(stored, hash) {
		return Identity{}, ErrCredentialRejected
	}
	if revokedAt != 0 {
		return Identity{}, ErrCredentialRejected
	}
	if expiresAt != 0 && expiresAt <= now.Unix() {
		return Identity{}, ErrCredentialRejected
	}

	node, err := s.Get(ctx, nodeID)
	if err != nil {
		return Identity{}, ErrCredentialRejected
	}
	if node.State == StateRemoved {
		return Identity{}, ErrCredentialRejected
	}
	// The credential prefix and the stored role must agree, so a managed
	// node's credential can never be presented as a cluster node's.
	if prefix != TokenPrefixFor(node.Role) {
		return Identity{}, ErrCredentialRejected
	}

	if _, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE node_credentials SET last_used_at = ? WHERE id = ?`, unixOf(now), credID); err != nil {
		return Identity{}, wrapInternal(err, "record credential use")
	}

	return Identity{Node: node, CredentialID: credID}, nil
}

// RevokeCredentials revokes every live credential of a node without
// removing it, so an operator can cut a suspect node off and re-key it
// afterwards. It returns how many credentials it revoked.
func (s *Service) RevokeCredentials(ctx context.Context, nodeID, actor string) (int64, error) {
	if err := ValidateNodeID(nodeID); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, database.TimeoutWrite,
		`UPDATE node_credentials SET revoked_at = ? WHERE node_id = ? AND revoked_at = 0`,
		unixOf(s.now()), nodeID)
	if err != nil {
		return 0, wrapInternal(err, "revoke node credentials")
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, wrapInternal(err, "revoke node credentials")
	}
	if rows > 0 {
		s.audit("nodes.credentials_revoked", truncate(actor, MaxNodeNameLen), nodeID, "count", rows)
	}
	return rows, nil
}

// factKeys is the fixed set of inventory keys stored per node. Only these
// keys are ever written, so a node cannot invent columns of its own.
var factKeys = []string{
	"os", "arch", "kernel", "hostname",
	"cpu_cores", "memory_bytes", "disk_bytes", "backends",
}

// factValues maps a validated Facts value onto the fixed key set.
func factValues(f Facts) map[string]string {
	return map[string]string{
		"os":           f.OS,
		"arch":         f.Arch,
		"kernel":       f.Kernel,
		"hostname":     f.Hostname,
		"cpu_cores":    strconv.FormatInt(f.CPUCores, 10),
		"memory_bytes": strconv.FormatInt(f.MemoryBytes, 10),
		"disk_bytes":   strconv.FormatInt(f.DiskBytes, 10),
		"backends":     strings.Join(f.Backends, ","),
	}
}

// storeFacts writes a node's validated inventory as one row per key. The
// upsert is expressed as UPDATE-then-INSERT so it works on every supported
// driver without dialect-specific conflict syntax.
func (s *Service) storeFacts(ctx context.Context, tx *sql.Tx, nodeID string, f Facts, now time.Time) error {
	values := factValues(f)
	for _, key := range factKeys {
		value := values[key]
		res, err := s.db.TxExec(ctx, tx,
			`UPDATE node_facts SET fact_value = ?, reported_at = ?
			 WHERE node_id = ? AND fact_key = ?`, value, unixOf(now), nodeID, key)
		if err != nil {
			return wrapInternal(err, "store node facts")
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return wrapInternal(err, "store node facts")
		}
		if rows > 0 {
			continue
		}
		if _, err := s.db.TxExec(ctx, tx,
			`INSERT INTO node_facts (node_id, fact_key, fact_value, reported_at)
			 VALUES (?, ?, ?, ?)`, nodeID, key, value, unixOf(now)); err != nil {
			return wrapInternal(err, "store node facts")
		}
	}
	return nil
}

// Facts returns a node's last reported inventory.
func (s *Service) Facts(ctx context.Context, nodeID string) (Facts, error) {
	if err := ValidateNodeID(nodeID); err != nil {
		return Facts{}, err
	}
	rows, err := s.db.QueryContext(ctx, database.TimeoutSelect,
		`SELECT fact_key, fact_value FROM node_facts WHERE node_id = ?`, nodeID)
	if err != nil {
		return Facts{}, wrapInternal(err, "load node facts")
	}
	defer func() { _ = rows.Close() }()

	stored := make(map[string]string, len(factKeys))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Facts{}, wrapInternal(err, "scan node facts")
		}
		stored[key] = value
	}
	if err := rows.Err(); err != nil {
		return Facts{}, wrapInternal(err, "load node facts")
	}
	if len(stored) == 0 {
		return Facts{}, ErrNodeNotFound
	}

	facts := Facts{
		OS:       stored["os"],
		Arch:     stored["arch"],
		Kernel:   stored["kernel"],
		Hostname: stored["hostname"],
	}
	facts.CPUCores, _ = strconv.ParseInt(stored["cpu_cores"], 10, 64)
	facts.MemoryBytes, _ = strconv.ParseInt(stored["memory_bytes"], 10, 64)
	facts.DiskBytes, _ = strconv.ParseInt(stored["disk_bytes"], 10, 64)
	if list := stored["backends"]; list != "" {
		facts.Backends = strings.Split(list, ",")
	}
	return facts, nil
}

// ReportFacts records a fresh inventory from an authenticated node. The
// identity is required so a node can only ever rewrite its own facts.
func (s *Service) ReportFacts(ctx context.Context, id Identity, f Facts) error {
	facts, err := ValidateFacts(f)
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.db.Tx(ctx, database.TimeoutWrite, func(tx *sql.Tx) error {
		return s.storeFacts(ctx, tx, id.Node.ID, facts, now)
	}); err != nil {
		return err
	}
	s.audit("nodes.facts_reported", "node", id.Node.ID,
		"os", facts.OS, "arch", facts.Arch, "backends", strings.Join(facts.Backends, ","))
	return nil
}
