package auth

import "testing"

func TestMessageKnownKey(t *testing.T) {
	got := message("auth.signed_in")
	want := "You are signed in."
	if got != want {
		t.Errorf("message(%q) = %q, want %q", "auth.signed_in", got, want)
	}
}

func TestMessageEmptyKeyReturnsEmpty(t *testing.T) {
	if got := message(""); got != "" {
		t.Errorf("message(\"\") = %q, want empty string", got)
	}
}

// TestMessageUnknownKeyNeverEchoesInput is a regression/security guard: an
// unknown or attacker-crafted flash key must never be reflected back as
// text, only a key present in the Messages table may render.
func TestMessageUnknownKeyNeverEchoesInput(t *testing.T) {
	crafted := "<script>alert(1)</script>"
	if got := message(crafted); got != "" {
		t.Errorf("message(%q) = %q, want empty string (unknown keys must not render)", crafted, got)
	}
}

func TestMessagesTableHasNoEmptyValues(t *testing.T) {
	for k, v := range Messages {
		if v == "" {
			t.Errorf("Messages[%q] is empty", k)
		}
	}
}
