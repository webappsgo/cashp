package guard

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

// webhookFixture is a signed delivery every test below tampers with.
type webhookFixture struct {
	policy    WebhookPolicy
	secret    Secret
	payload   []byte
	signature string
	timestamp time.Time
}

// newWebhookFixture builds a delivery that verifies, so each test can change
// exactly one thing and assert the change alone causes the refusal.
func newWebhookFixture(t *testing.T, policy WebhookPolicy) webhookFixture {
	t.Helper()
	secret := NewSecret("whsec_" + strings.Repeat("k", 26))
	timestamp := time.Unix(1700000000, 0)
	payload := []byte("1700000000." + `{"id":"evt_1","type":"invoice.paid"}`)
	signature, err := SignPayload(policy, secret, payload)
	if err != nil {
		t.Fatalf("SignPayload failed: %v", err)
	}
	return webhookFixture{policy: policy, secret: secret, payload: payload, signature: signature, timestamp: timestamp}
}

func TestVerifyWebhookAcceptsOnlyAnAuthenticDelivery(t *testing.T) {
	for _, policy := range []WebhookPolicy{
		{Algorithm: SignatureHMACSHA256, Encoding: EncodingHex},
		{Algorithm: SignatureHMACSHA512, Encoding: EncodingHex},
		{Algorithm: SignatureHMACSHA256, Encoding: EncodingBase64},
	} {
		f := newWebhookFixture(t, policy)
		if err := VerifyWebhook(policy, f.secret, f.payload, f.signature, f.timestamp, f.timestamp); err != nil {
			t.Fatalf("VerifyWebhook rejected an authentic delivery under %v: %v", policy, err)
		}
		// Hex is case-insensitive on the wire; nothing else may be.
		if policy.Encoding == EncodingHex {
			if err := VerifyWebhook(policy, f.secret, f.payload, strings.ToUpper(f.signature), f.timestamp, f.timestamp); err != nil {
				t.Fatalf("VerifyWebhook rejected an uppercase hex signature: %v", err)
			}
		}
	}
}

func TestVerifyWebhookRefusesForgedSignatures(t *testing.T) {
	policy := WebhookPolicy{Algorithm: SignatureHMACSHA256, Encoding: EncodingHex}
	f := newWebhookFixture(t, policy)
	zeroed := hex.EncodeToString(make([]byte, 32))

	cases := []struct {
		name      string
		secret    Secret
		payload   []byte
		signature string
	}{
		{"wrong secret", NewSecret("whsec_" + strings.Repeat("x", 26)), f.payload, f.signature},
		{"tampered payload", f.secret, []byte(`{"id":"evt_1","type":"invoice.paid","amount":0}`), f.signature},
		{"zeroed signature", f.secret, f.payload, zeroed},
		{"truncated signature", f.secret, f.payload, f.signature[:32]},
		{"empty signature", f.secret, f.payload, ""},
		{"whitespace signature", f.secret, f.payload, "   "},
		{"non-hex signature", f.secret, f.payload, strings.Repeat("z", 64)},
		{"empty payload", f.secret, nil, f.signature},
		{"unconfigured secret", NewSecret(""), f.payload, f.signature},
	}
	for _, tc := range cases {
		err := VerifyWebhook(policy, tc.secret, tc.payload, tc.signature, f.timestamp, f.timestamp)
		if err == nil {
			t.Fatalf("VerifyWebhook accepted the %s case", tc.name)
		}
		if code := AppErrorFor(err).Code; code != apperr.CodeUnauthorized {
			t.Fatalf("%s denied with code %q", tc.name, code)
		}
	}

	// A signature computed with the right secret but the wrong algorithm
	// must not verify either.
	other := WebhookPolicy{Algorithm: SignatureHMACSHA512, Encoding: EncodingHex}
	wrongAlgo, err := SignPayload(other, f.secret, f.payload)
	if err != nil {
		t.Fatalf("SignPayload failed: %v", err)
	}
	if err := VerifyWebhook(policy, f.secret, f.payload, wrongAlgo, f.timestamp, f.timestamp); err == nil {
		t.Fatal("VerifyWebhook accepted a signature made with a different algorithm")
	}
}

func TestVerifyWebhookRefusesStaleAndUnsignedTimestamps(t *testing.T) {
	policy := WebhookPolicy{Algorithm: SignatureHMACSHA256, Encoding: EncodingHex, Tolerance: time.Minute}
	f := newWebhookFixture(t, policy)

	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"stale delivery", f.timestamp.Add(2 * time.Minute)},
		{"future delivery", f.timestamp.Add(-2 * time.Minute)},
	} {
		err := VerifyWebhook(policy, f.secret, f.payload, f.signature, f.timestamp, tc.now)
		if err == nil {
			t.Fatalf("VerifyWebhook accepted the %s case", tc.name)
		}
		if DenialReason(err) != ReasonReplay {
			t.Fatalf("%s denied with %q", tc.name, DenialReason(err))
		}
	}

	if err := VerifyWebhook(policy, f.secret, f.payload, f.signature, time.Time{}, f.timestamp); err == nil {
		t.Fatal("VerifyWebhook accepted a delivery with no timestamp")
	}

	// A zero tolerance falls back to the default rather than accepting
	// everything or nothing.
	lenient := WebhookPolicy{Algorithm: SignatureHMACSHA256, Encoding: EncodingHex}
	g := newWebhookFixture(t, lenient)
	if err := VerifyWebhook(lenient, g.secret, g.payload, g.signature, g.timestamp, g.timestamp.Add(time.Minute)); err != nil {
		t.Fatalf("a zero tolerance refused a delivery inside the default window: %v", err)
	}
	if err := VerifyWebhook(lenient, g.secret, g.payload, g.signature, g.timestamp, g.timestamp.Add(DefaultWebhookTolerance+time.Second)); err == nil {
		t.Fatal("a zero tolerance accepted a delivery outside the default window")
	}
}

func TestUnknownAlgorithmAndEncodingNeverDefaultToAccepting(t *testing.T) {
	f := newWebhookFixture(t, WebhookPolicy{Algorithm: SignatureHMACSHA256, Encoding: EncodingHex})

	// A zero-valued policy names no algorithm and must refuse rather than
	// silently verify nothing.
	if err := VerifyWebhook(WebhookPolicy{}, f.secret, f.payload, f.signature, f.timestamp, f.timestamp); err == nil {
		t.Fatal("a zero-valued policy verified a delivery")
	}
	if _, err := SignPayload(WebhookPolicy{}, f.secret, f.payload); err == nil {
		t.Fatal("a zero-valued policy signed a payload")
	}

	bogus := WebhookPolicy{Algorithm: SignatureAlgorithm("md5"), Encoding: EncodingHex}
	if err := VerifyWebhook(bogus, f.secret, f.payload, f.signature, f.timestamp, f.timestamp); err == nil {
		t.Fatal("an unsupported algorithm verified a delivery")
	}

	unknownEncoding := WebhookPolicy{Algorithm: SignatureHMACSHA256, Encoding: SignatureEncoding("base32")}
	if err := VerifyWebhook(unknownEncoding, f.secret, f.payload, f.signature, f.timestamp, f.timestamp); err == nil {
		t.Fatal("an unsupported encoding verified a delivery")
	}
	if _, err := SignPayload(unknownEncoding, f.secret, f.payload); err == nil {
		t.Fatal("an unsupported encoding produced a signature")
	}

	// A base64 signature that decodes to nothing must be refused, not
	// compared as an empty slice.
	b64 := WebhookPolicy{Algorithm: SignatureHMACSHA256, Encoding: EncodingBase64}
	if err := VerifyWebhook(b64, f.secret, f.payload, base64.StdEncoding.EncodeToString(nil), f.timestamp, f.timestamp); err == nil {
		t.Fatal("an empty base64 signature verified a delivery")
	}
	if err := VerifyWebhook(b64, f.secret, f.payload, "!!!not base64!!!", f.timestamp, f.timestamp); err == nil {
		t.Fatal("an undecodable base64 signature verified a delivery")
	}
}

func TestReplayGuardRefusesASecondClaim(t *testing.T) {
	replay := NewReplayGuard(time.Minute)
	base := time.Unix(1700000000, 0)
	current := base
	replay.SetClock(func() time.Time { return current })

	if err := replay.Claim("stripe", "evt_1"); err != nil {
		t.Fatalf("the first claim was refused: %v", err)
	}
	err := replay.Claim("stripe", "evt_1")
	if err == nil {
		t.Fatal("ReplayGuard accepted a replayed delivery")
	}
	if code := AppErrorFor(err).Code; code != apperr.CodeConflict {
		t.Fatalf("the replay denial mapped to %q", code)
	}
	if msg := AppErrorFor(err).Message; strings.Contains(msg, "evt_1") {
		t.Fatalf("the replay denial echoed the event id: %q", msg)
	}

	// The identifier namespace is per provider, so two providers issuing the
	// same id do not collide.
	if err := replay.Claim("paypal", "evt_1"); err != nil {
		t.Fatalf("a different provider's identical id was refused: %v", err)
	}

	for _, tc := range [][2]string{{"", "evt_2"}, {"stripe", ""}, {"", ""}} {
		if err := replay.Claim(tc[0], tc[1]); err == nil {
			t.Fatalf("ReplayGuard accepted an unidentified delivery %v", tc)
		}
	}

	// Past the retention window the record is dropped, which is why the
	// retention must exceed the signature tolerance.
	current = base.Add(2 * time.Minute)
	if err := replay.Claim("stripe", "evt_1"); err != nil {
		t.Fatalf("the record outlived its retention: %v", err)
	}
	replay.Cleanup()
}

func TestNewReplayGuardHasNoZeroRetention(t *testing.T) {
	replay := NewReplayGuard(0)
	if err := replay.Claim("stripe", "evt_1"); err != nil {
		t.Fatalf("the first claim was refused: %v", err)
	}
	// With a zero retention every record would expire immediately and every
	// replay would be accepted, so the constructor must substitute a floor.
	if err := replay.Claim("stripe", "evt_1"); err == nil {
		t.Fatal("a zero retention accepted a replayed delivery")
	}
}
