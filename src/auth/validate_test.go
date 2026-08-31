package auth

import "testing"

func TestNormalizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Foo-Bar  ", "foo-bar"},
		{"ALLCAPS", "allcaps"},
		{"", ""},
		{"already-lower", "already-lower"},
	}
	for _, c := range cases {
		if got := NormalizeName(c.in); got != c.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Foo@Example.COM  ", "foo@example.com"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeEmail(c.in); got != c.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateUsernameFormat(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrUsernameTooShort},
		{"one char", "a", ErrUsernameTooShort},
		{"minimum valid", "ab", nil},
		{"too long", "a234567890123456789012345678901234567890", ErrUsernameTooLong}, // 41 chars
		{"exactly max 39", "a23456789012345678901234567890123456789", nil},            // 39 chars
		{"consecutive hyphens", "fo--bar", ErrUsernameHyphen},
		{"leading hyphen", "-foobar", ErrUsernameHyphen},
		{"trailing hyphen", "foobar-", ErrUsernameHyphen},
		// ValidateUsernameFormat normalizes (lowercases) the input before checking
		// charset, so mixed-case input is valid — only genuinely disallowed
		// characters trigger ErrUsernameCharset.
		{"uppercase char is normalized, not rejected", "FooBar", nil},
		{"invalid symbol", "foo_bar", ErrUsernameCharset},
		{"space", "foo bar", ErrUsernameCharset},
		{"valid with hyphen", "foo-bar", nil},
		{"valid alnum", "foo123", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateUsernameFormat(c.in)
			if err != c.wantErr {
				t.Errorf("ValidateUsernameFormat(%q) = %v, want %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidateSlugFormat(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrSlugLength},
		{"one char", "a", ErrSlugLength},
		{"minimum valid", "ab", nil},
		{"too long", "a234567890123456789012345678901234567890", ErrSlugLength}, // 41 chars
		{"exactly max 39", "a23456789012345678901234567890123456789", nil},
		{"consecutive hyphens", "fo--bar", ErrSlugHyphen},
		// Same normalize-before-check behavior as ValidateUsernameFormat.
		{"uppercase is normalized, not rejected", "FooBar", nil},
		{"symbol", "foo_bar", ErrSlugCharset},
		{"valid", "foo-bar", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSlugFormat(c.in)
			if err != c.wantErr {
				t.Errorf("ValidateSlugFormat(%q) = %v, want %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	longLocal := make([]byte, 65)
	for i := range longLocal {
		longLocal[i] = 'a'
	}
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid", "user@example.com", false},
		{"valid with dots and plus", "user.name+tag@example.co", false},
		{"missing at", "userexample.com", true},
		{"missing domain", "user@", true},
		{"missing local", "@example.com", true},
		{"leading dot local", ".user@example.com", true},
		{"trailing dot local", "user.@example.com", true},
		{"double dot local", "us..er@example.com", true},
		{"no tld", "user@example", true},
		{"single letter tld", "user@example.c", true},
		{"blocklisted domain", "user@mailinator.com", true},
		{"blocklisted domain 2", "user@tempmail.com", true},
		{"too long local", string(longLocal) + "@example.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateEmail(c.in)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateEmail(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"too short", "short1", ErrPasswordTooShort},
		{"exactly 8", "12345678", nil},
		{"leading space", " password", ErrPasswordWhitespce},
		{"trailing space", "password ", ErrPasswordWhitespce},
		{"valid", "a-good-password", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePassword(c.in)
			if err != c.wantErr {
				t.Errorf("ValidatePassword(%q) = %v, want %v", c.in, err, c.wantErr)
			}
		})
	}

	long := make([]byte, 1025)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePassword(string(long)); err != ErrPasswordTooLong {
		t.Errorf("ValidatePassword(1025 chars) = %v, want ErrPasswordTooLong", err)
	}
	maxLen := make([]byte, 1024)
	for i := range maxLen {
		maxLen[i] = 'a'
	}
	if err := ValidatePassword(string(maxLen)); err != nil {
		t.Errorf("ValidatePassword(1024 chars) = %v, want nil", err)
	}
}

func TestDetectIdentifierType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "username"},
		{"12345", "user_id"},
		{"user@example.com", "email"},
		{"someuser", "username"},
		{"123abc", "username"},
	}
	for _, c := range cases {
		if got := DetectIdentifierType(c.in); got != c.want {
			t.Errorf("DetectIdentifierType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
