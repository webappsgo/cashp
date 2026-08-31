package security

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"

	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("unexpected PHC prefix: %q", encoded)
	}
	if strings.Contains(encoded, password) {
		t.Fatal("encoded hash leaks the plaintext password")
	}

	ok, needsRehash, err := VerifyPassword(encoded, password)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword rejected the correct password")
	}
	if needsRehash {
		t.Fatal("a freshly minted hash must not need a rehash")
	}
}

func TestHashPasswordUniqueSalt(t *testing.T) {
	first, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if first == second {
		t.Fatal("two hashes of the same password must differ (per-password salt)")
	}
}

func TestVerifyPasswordWrongPassword(t *testing.T) {
	encoded, err := HashPassword("right-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, _, err := VerifyPassword(encoded, "wrong-password")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

func TestHashPasswordRejectsBadInput(t *testing.T) {
	tests := []struct {
		name     string
		password string
		want     error
	}{
		{"empty", "", ErrEmptyPassword},
		{"leading space", " padded", ErrPasswordWhitespace},
		{"trailing space", "padded ", ErrPasswordWhitespace},
		{"trailing newline", "padded\n", ErrPasswordWhitespace},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := HashPassword(tc.password); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyPasswordBcryptLegacyNeedsRehash(t *testing.T) {
	const password = "legacy-password"

	legacy, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}

	ok, needsRehash, err := VerifyPassword(string(legacy), password)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword rejected a valid legacy bcrypt hash")
	}
	if !needsRehash {
		t.Fatal("a bcrypt hash must always be reported as needing a rehash")
	}

	ok, _, err = VerifyPassword(string(legacy), "not-the-password")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword accepted a wrong password against a bcrypt hash")
	}
}

func TestVerifyPasswordOutdatedParamsNeedRehash(t *testing.T) {
	const password = "weak-params"

	salt := []byte("0123456789abcdef")
	weak := encodeArgon2Hash(salt, argon2IDKeyForTest(password, salt), 8*1024, 1, 1)

	ok, needsRehash, err := VerifyPassword(weak, password)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword rejected a valid hash with weak parameters")
	}
	if !needsRehash {
		t.Fatal("a hash with weaker-than-current parameters must need a rehash")
	}
}

func TestVerifyPasswordInvalidFormat(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{"plaintext", "hunter2", ErrInvalidHashFormat},
		{"truncated phc", "$argon2id$v=19$m=65536,t=3,p=4$onlysalt", ErrInvalidHashFormat},
		{"bad version", "$argon2id$v=16$m=65536,t=3,p=4$c2FsdA$aGFzaA", ErrIncompatibleVersion},
		{"bad base64 salt", "$argon2id$v=19$m=65536,t=3,p=4$!!!!$aGFzaA", ErrInvalidHashFormat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := VerifyPassword(tc.encoded, "x"); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// argon2IDKeyForTest derives a key with deliberately weak parameters so
// the rehash-detection path can be exercised without a slow hash.
func argon2IDKeyForTest(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 1, 8*1024, 1, ArgonKeyLen)
}
