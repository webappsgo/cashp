package guard

import (
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/security"
)

// NameStatus is the answer a username or email availability probe gets. It
// is deliberately coarse: only a blacklisted name gets a distinguishable
// answer, because a reserved name reveals nothing about who is registered.
type NameStatus string

// The three availability answers.
const (
	// StatusAvailable means the name may be claimed.
	StatusAvailable NameStatus = "available"
	// StatusUnavailable means the name may not be claimed, without saying why.
	StatusUnavailable NameStatus = "unavailable"
	// StatusReserved means the name is on the reserved or blacklisted list.
	StatusReserved NameStatus = "reserved"
)

// MinAuthResponseTime is the fixed floor every authentication and
// existence-revealing response is padded to. It is long enough to swamp
// the difference between an Argon2id verification and an early return, and
// short enough not to be felt by a legitimate user.
const MinAuthResponseTime = 350 * time.Millisecond

// TakenFunc reports whether a name is already claimed. The caller must
// wire it so that tombstoned names of deleted tenants count as taken: a
// released username would let a new registrant inherit a former tenant's
// inbound mail and DNS references.
type TakenFunc func(name string) (bool, error)

// BlockedFunc reports whether a name is reserved or blacklisted.
type BlockedFunc func(name string) bool

// CheckNameAvailability answers an availability probe without disclosing
// account existence. A blacklisted name answers "reserved"; a name that is
// taken, tombstoned, or otherwise unusable answers the identical
// "unavailable" as any other refusal, so probing cannot separate the two.
//
// The blocklist is consulted first and without touching storage, so a
// reserved name is answered at the same cost regardless of the database.
func CheckNameAvailability(name string, blocked BlockedFunc, taken TakenFunc) (NameStatus, error) {
	if blocked != nil && blocked(name) {
		return StatusReserved, nil
	}
	if err := ValidateUsername(name); err != nil {
		return StatusUnavailable, nil
	}
	if taken == nil {
		return StatusUnavailable, nil
	}
	claimed, err := taken(name)
	if err != nil {
		// A storage failure must not answer "available" — an unknown
		// answer is an unavailable answer.
		return StatusUnavailable, err
	}
	if claimed {
		return StatusUnavailable, nil
	}
	return StatusAvailable, nil
}

// Pacer pads an operation to a fixed minimum duration. Both the clock and
// the sleep are injectable so timing behavior is unit-testable without
// actually waiting.
type Pacer struct {
	// Now returns the current time; a nil value falls back to time.Now.
	Now func() time.Time
	// Sleep blocks for a duration; a nil value falls back to time.Sleep.
	Sleep func(time.Duration)
}

// NewPacer returns a Pacer backed by the real clock.
func NewPacer() Pacer {
	return Pacer{Now: time.Now, Sleep: time.Sleep}
}

// now reads the pacer's clock, falling back to the real one.
func (p Pacer) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// sleep blocks on the pacer's sleeper, falling back to the real one.
func (p Pacer) sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	if p.Sleep != nil {
		p.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Start marks the beginning of a paced operation.
func (p Pacer) Start() time.Time {
	return p.now()
}

// PadTo blocks until at least minimum has elapsed since start. Applied to
// every outcome of an authentication or existence check — success and
// failure alike — it removes the timing channel that distinguishes an
// unknown identifier from a wrong password.
func (p Pacer) PadTo(start time.Time, minimum time.Duration) {
	if minimum <= 0 {
		return
	}
	p.sleep(minimum - p.now().Sub(start))
}

// PadAuth pads to MinAuthResponseTime, the standard floor for every
// authentication and account-existence response.
func (p Pacer) PadAuth(start time.Time) {
	p.PadTo(start, MinAuthResponseTime)
}

// decoyHash holds a valid Argon2id encoding of a random value, computed
// once on first use. Verifying against it costs the same as verifying a
// real hash, which is what makes an unknown identifier indistinguishable
// from a known one by work performed.
var (
	decoyOnce sync.Once
	decoyHash string
)

// decoy returns the process-lifetime decoy hash. The secret it encodes is
// random and never stored anywhere else, so the decoy cannot be used as a
// credential.
func decoy() string {
	decoyOnce.Do(func() {
		secret, err := security.RandomSecret(security.SecretLen)
		if err != nil {
			return
		}
		hash, err := security.HashPassword(string(secret))
		if err != nil {
			return
		}
		decoyHash = hash
	})
	return decoyHash
}

// VerifyOrDecoy verifies a password against a stored hash, and against a
// decoy hash of equivalent cost when no stored hash exists. The caller
// gets the same amount of work whether or not the account exists, so a
// login endpoint cannot be used to enumerate accounts by response time.
//
// needsRehash is meaningful only when ok is true.
func VerifyOrDecoy(encodedHash, password string) (ok bool, needsRehash bool) {
	if encodedHash == "" {
		if d := decoy(); d != "" {
			_, _, _ = security.VerifyPassword(d, password)
		}
		return false, false
	}
	valid, rehash, err := security.VerifyPassword(encodedHash, password)
	if err != nil {
		return false, false
	}
	return valid, rehash
}

// UniformNotFound is the denial returned for every failed lookup of a
// resource the caller may or may not be entitled to see. It maps to
// NOT_FOUND regardless of whether the resource was absent or merely
// foreign, so the two cases are indistinguishable on the wire.
func UniformNotFound(resourceType, resourceID string) error {
	return Deny(ReasonCrossTenant, "no visible "+resourceType+" with id "+resourceID)
}

// UniformAuthFailure is the denial returned for every failed
// authentication, whatever the underlying cause. The cause travels in the
// log-only detail; the client always sees the same generic code.
func UniformAuthFailure(detail string) error {
	return Deny(ReasonSubjectInvalid, detail)
}
