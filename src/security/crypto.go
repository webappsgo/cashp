package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// SecretLen is the standard length in bytes of every project-level secret
// defined in AI.md PART 11: installation_secret, cookie_signing_key,
// csrf_token_secret, and server.security.encryption_key.
const SecretLen = 32

// Encryption errors.
var (
	// ErrInvalidKeyLength is returned when an AES-256-GCM key is not exactly SecretLen bytes.
	ErrInvalidKeyLength = errors.New("security: encryption key must be 32 bytes")
	// ErrCiphertextTooShort is returned when a ciphertext is shorter than the GCM nonce it must carry.
	ErrCiphertextTooShort = errors.New("security: ciphertext too short")
	// ErrInvalidSecretLength is returned when a non-positive secret length is requested.
	ErrInvalidSecretLength = errors.New("security: secret length must be positive")
)

// RandomSecret returns n cryptographically random bytes, used to mint the
// project-level secrets and any other key material. Callers persist the
// result base64-encoded and never log it.
func RandomSecret(n int) ([]byte, error) {
	if n <= 0 {
		return nil, ErrInvalidSecretLength
	}

	out := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, out); err != nil {
		return nil, fmt.Errorf("security: read random: %w", err)
	}

	return out, nil
}

// Encrypt seals plaintext with AES-256-GCM under key, which must be
// SecretLen bytes. The randomly generated nonce is prepended to the
// returned ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("security: read nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a ciphertext produced by Encrypt. A tampered or truncated
// ciphertext, or the wrong key, yields an error and never partial output.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrCiphertextTooShort
	}

	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("security: decrypt: %w", err)
	}

	return plaintext, nil
}

// newGCM builds an AES-256-GCM AEAD from key after validating its length.
func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != SecretLen {
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("security: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("security: new gcm: %w", err)
	}

	return gcm, nil
}
