package security

import (
	"bytes"
	"errors"
	"testing"
)

func TestRandomSecret(t *testing.T) {
	first, err := RandomSecret(SecretLen)
	if err != nil {
		t.Fatalf("RandomSecret: %v", err)
	}
	if len(first) != SecretLen {
		t.Fatalf("length = %d, want %d", len(first), SecretLen)
	}

	second, err := RandomSecret(SecretLen)
	if err != nil {
		t.Fatalf("RandomSecret: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two secrets must not be identical")
	}
}

func TestRandomSecretRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1} {
		if _, err := RandomSecret(n); !errors.Is(err, ErrInvalidSecretLength) {
			t.Fatalf("RandomSecret(%d) err = %v, want ErrInvalidSecretLength", n, err)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := RandomSecret(SecretLen)
	if err != nil {
		t.Fatalf("RandomSecret: %v", err)
	}

	plaintext := []byte("totp-secret:JBSWY3DPEHPK3PXP")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}

	got, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key, err := RandomSecret(SecretLen)
	if err != nil {
		t.Fatalf("RandomSecret: %v", err)
	}

	first, err := Encrypt(key, []byte("same input"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := Encrypt(key, []byte("same input"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(first, second) {
		t.Fatal("encrypting the same plaintext twice must produce different ciphertexts")
	}
}

func TestDecryptRejectsBadInput(t *testing.T) {
	key, err := RandomSecret(SecretLen)
	if err != nil {
		t.Fatalf("RandomSecret: %v", err)
	}
	wrongKey, err := RandomSecret(SecretLen)
	if err != nil {
		t.Fatalf("RandomSecret: %v", err)
	}

	ciphertext, err := Encrypt(key, []byte("sensitive"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := Decrypt(wrongKey, ciphertext); err == nil {
		t.Fatal("decrypting with the wrong key must fail")
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := Decrypt(key, tampered); err == nil {
		t.Fatal("decrypting tampered ciphertext must fail")
	}

	if _, err := Decrypt(key, ciphertext[:4]); !errors.Is(err, ErrCiphertextTooShort) {
		t.Fatalf("err = %v, want ErrCiphertextTooShort", err)
	}
}

func TestEncryptRejectsBadKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		key := make([]byte, n)

		if _, err := Encrypt(key, []byte("x")); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("Encrypt with %d-byte key: err = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := Decrypt(key, []byte("x")); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("Decrypt with %d-byte key: err = %v, want ErrInvalidKeyLength", n, err)
		}
	}
}
