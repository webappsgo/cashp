package guard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/security"
)

// leaked is the sentinel value every test below looks for. If it appears in
// any rendered output, a secret escaped.
const leaked = "hunter2-super-secret"

func TestSecretNeverRendersItsValue(t *testing.T) {
	s := NewSecret(leaked)

	renderings := map[string]string{
		"String":     s.String(),
		"%v":         fmt.Sprintf("%v", s),
		"%s":         fmt.Sprintf("%s", s),
		"%q":         fmt.Sprintf("%q", s),
		"%#v":        fmt.Sprintf("%#v", s),
		"%+v":        fmt.Sprintf("%+v", s),
		"Errorf":     fmt.Errorf("failed for %v", s).Error(),
		"MarshalTxt": mustText(t, s),
	}
	for name, rendered := range renderings {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("%s leaked the secret: %q", name, rendered)
		}
		if !strings.Contains(rendered, security.MaskedValue) {
			t.Fatalf("%s did not mask: %q", name, rendered)
		}
	}

	encoded, err := json.Marshal(struct {
		Token Secret `json:"token"`
	}{Token: s})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), leaked) {
		t.Fatalf("json.Marshal leaked the secret: %s", encoded)
	}

	if value := s.LogValue().String(); strings.Contains(value, leaked) {
		t.Fatalf("slog.LogValuer leaked the secret: %q", value)
	}

	if s.Reveal() != leaked {
		t.Fatal("Reveal did not return the wrapped value")
	}
	if s.Empty() || !NewSecret("").Empty() {
		t.Fatal("Empty misreported the wrapped value")
	}
}

// mustText renders a Secret through encoding.TextMarshaler.
func mustText(t *testing.T, s Secret) string {
	t.Helper()
	b, err := s.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText failed: %v", err)
	}
	return string(b)
}

func TestSecretComparisonIsConstantTimeAndExact(t *testing.T) {
	s := NewSecret(leaked)
	if !s.Equal(leaked) {
		t.Fatal("Equal rejected the correct value")
	}
	for _, candidate := range []string{"", "hunter2", leaked + "x", strings.ToUpper(leaked)} {
		if s.Equal(candidate) {
			t.Fatalf("Equal accepted %q", candidate)
		}
	}
	if !s.EqualSecret(NewSecret(leaked)) || s.EqualSecret(NewSecret("other")) {
		t.Fatal("EqualSecret compared incorrectly")
	}
}

func TestScrubTextMasksTokensAndCredentialPairs(t *testing.T) {
	body := "auth failed for adm_" + strings.Repeat("a", 32) +
		" using password=" + leaked +
		" and api_key: " + leaked +
		" plus usr_agt_" + strings.Repeat("b", 32)

	scrubbed := ScrubText(body)
	if strings.Contains(scrubbed, leaked) {
		t.Fatalf("ScrubText left a credential in place: %q", scrubbed)
	}
	if strings.Contains(scrubbed, strings.Repeat("a", 32)) || strings.Contains(scrubbed, strings.Repeat("b", 32)) {
		t.Fatalf("ScrubText left a token body in place: %q", scrubbed)
	}
	if ScrubText("") != "" {
		t.Fatal("ScrubText altered the empty string")
	}
}

func TestRedactPayloadMasksSensitiveFieldsAndSecrets(t *testing.T) {
	type nested struct {
		DBPassword string `json:"db_password"`
		Note       string `json:"note"`
		Skipped    string `json:"-"`
		hidden     string
	}
	payload := map[string]any{
		"username":      "tenant-one",
		"password":      leaked,
		"api_key":       leaked,
		"session_token": leaked,
		"wrapped":       NewSecret(leaked),
		"raw_bytes":     []byte(leaked),
		"nested":        nested{DBPassword: leaked, Note: "password=" + leaked, Skipped: leaked, hidden: leaked},
		"list":          []any{map[string]any{"secret": leaked}, "plain"},
	}

	rendered := fmt.Sprintf("%v", RedactPayload(payload))
	if strings.Contains(rendered, leaked) {
		t.Fatalf("RedactPayload leaked a secret: %s", rendered)
	}
	if !strings.Contains(rendered, "tenant-one") {
		t.Fatal("RedactPayload dropped a non-sensitive value")
	}

	// The original must be untouched so the caller can still use it.
	if payload["password"] != leaked {
		t.Fatal("RedactPayload mutated its input")
	}
}

func TestRedactPayloadBoundsRecursionDepth(t *testing.T) {
	type link struct {
		Next *link
		Leaf string
	}
	head := &link{Leaf: "top"}
	node := head
	for i := 0; i < maxRedactDepth*3; i++ {
		node.Next = &link{Leaf: "deep"}
		node = node.Next
	}
	// A structure deeper than the ceiling must terminate rather than
	// exhausting the stack on a logging path.
	rendered := fmt.Sprintf("%v", RedactPayload(head))
	if !strings.Contains(rendered, truncatedMarker) {
		t.Fatal("RedactPayload did not truncate a deeply nested structure")
	}
}

func TestRedactMapAndAttrs(t *testing.T) {
	out := RedactMap(map[string]any{"password": leaked, "user": "tenant-one"})
	if out["password"] != security.MaskedValue {
		t.Fatalf("RedactMap did not mask: %v", out)
	}

	attrs := RedactAttrs([]slog.Attr{
		slog.String("api_key", leaked),
		slog.String("note", "token=adm_"+strings.Repeat("c", 32)),
		slog.Any("payload", map[string]any{"secret": leaked}),
		slog.Group("group", slog.String("password", leaked)),
		slog.Int("count", 3),
	})
	rendered := fmt.Sprintf("%v", attrs)
	if strings.Contains(rendered, leaked) || strings.Contains(rendered, strings.Repeat("c", 32)) {
		t.Fatalf("RedactAttrs leaked a credential: %s", rendered)
	}
	if !strings.Contains(rendered, "count=3") {
		t.Fatalf("RedactAttrs dropped a benign attribute: %s", rendered)
	}
}

func TestRedactPayloadHandlesNilAndFunctions(t *testing.T) {
	if RedactPayload(nil) != nil {
		t.Fatal("RedactPayload did not pass nil through")
	}
	var nilMap map[string]string
	if out := RedactPayload(nilMap); out == nil {
		t.Fatal("RedactPayload dropped a nil map entirely")
	}
	rendered := fmt.Sprintf("%v", RedactPayload(map[string]any{"fn": func() {}}))
	if !strings.Contains(rendered, truncatedMarker) {
		t.Fatalf("RedactPayload rendered a function value: %s", rendered)
	}
}
