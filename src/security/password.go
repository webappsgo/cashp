// Package security implements cashp's cryptographic and input-validation
// primitives per AI.md PART 11: Argon2id password hashing, API token
// generation and hashing, HMAC CSRF tokens, AES-256-GCM at-rest
// encryption, path-traversal and SSRF guards, secret masking, and a
// reusable sliding-window rate limiter. HTTP middleware that consumes
// these primitives lives in the server package, not here.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Argon2id parameters (OWASP 2023). These are the only parameters used for
// new password hashes; stored hashes carrying weaker parameters verify
// successfully but are reported as needing a rehash.
const (
	// ArgonTime is the number of iterations.
	ArgonTime uint32 = 3
	// ArgonMemory is the memory cost in KiB (64 MB).
	ArgonMemory uint32 = 64 * 1024
	// ArgonThreads is the parallelism factor.
	ArgonThreads uint8 = 4
	// ArgonKeyLen is the derived key length in bytes.
	ArgonKeyLen uint32 = 32
	// ArgonSaltLen is the per-password salt length in bytes.
	ArgonSaltLen uint32 = 16
	// argon2Version is the argon2 version encoded in the PHC string.
	argon2Version = 19
)

// Password hashing errors. Callers must never surface these verbatim to an
// end user — they are for logs and internal control flow only.
var (
	// ErrEmptyPassword is returned when an empty password is submitted for hashing.
	ErrEmptyPassword = errors.New("security: password must not be empty")
	// ErrPasswordWhitespace is returned when a password has leading or trailing whitespace, which is rejected rather than trimmed.
	ErrPasswordWhitespace = errors.New("security: password must not have leading or trailing whitespace")
	// ErrInvalidHashFormat is returned when a stored hash is not a recognized Argon2id PHC string or legacy bcrypt hash.
	ErrInvalidHashFormat = errors.New("security: unrecognized password hash format")
	// ErrIncompatibleVersion is returned when a stored Argon2id hash was produced by an unsupported argon2 version.
	ErrIncompatibleVersion = errors.New("security: incompatible argon2 version")
)

// HashPassword derives an Argon2id hash of password and returns it in PHC
// string format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>. bcrypt is
// never used for new hashes.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	if strings.TrimSpace(password) != password {
		return "", ErrPasswordWhitespace
	}

	salt := make([]byte, ArgonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: read salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, ArgonTime, ArgonMemory, ArgonThreads, ArgonKeyLen)

	return encodeArgon2Hash(salt, hash, ArgonMemory, ArgonTime, ArgonThreads), nil
}

// VerifyPassword checks password against encodedHash. It accepts both
// Argon2id PHC strings and legacy bcrypt hashes. needsRehash is true when
// the stored hash is bcrypt, or when it is Argon2id with parameters weaker
// than the current constants — the caller should then re-hash the verified
// plaintext with HashPassword and persist the result.
func VerifyPassword(encodedHash, password string) (ok bool, needsRehash bool, err error) {
	switch {
	case strings.HasPrefix(encodedHash, "$argon2id$"):
		return verifyArgon2(encodedHash, password)
	case isBcryptHash(encodedHash):
		if err := bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password)); err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				return false, false, nil
			}
			return false, false, fmt.Errorf("security: bcrypt verify: %w", err)
		}
		return true, true, nil
	default:
		return false, false, ErrInvalidHashFormat
	}
}

// isBcryptHash reports whether h looks like one of the bcrypt variants
// cashp may find in a legacy database.
func isBcryptHash(h string) bool {
	return strings.HasPrefix(h, "$2a$") || strings.HasPrefix(h, "$2b$") || strings.HasPrefix(h, "$2y$")
}

// verifyArgon2 decodes a PHC string, recomputes the derived key with the
// stored parameters, and compares in constant time.
func verifyArgon2(encodedHash, password string) (bool, bool, error) {
	salt, want, memory, iterations, threads, err := decodeArgon2Hash(encodedHash)
	if err != nil {
		return false, false, err
	}

	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}

	outdated := memory < ArgonMemory || iterations < ArgonTime || threads < ArgonThreads || uint32(len(want)) < ArgonKeyLen
	return true, outdated, nil
}

// encodeArgon2Hash renders salt and hash into the PHC string format used
// for storage.
func encodeArgon2Hash(salt, hash []byte, memory, iterations uint32, threads uint8) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		memory,
		iterations,
		threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

// decodeArgon2Hash parses a PHC string back into its salt, hash, and cost
// parameters.
func decodeArgon2Hash(encodedHash string) (salt, hash []byte, memory, iterations uint32, threads uint8, err error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, ErrInvalidHashFormat
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHashFormat
	}
	if version != argon2Version {
		return nil, nil, 0, 0, 0, ErrIncompatibleVersion
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHashFormat
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHashFormat
	}

	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) == 0 {
		return nil, nil, 0, 0, 0, ErrInvalidHashFormat
	}

	return salt, hash, memory, iterations, threads, nil
}
