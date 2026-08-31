package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTemplateAcceptsPart18Format(t *testing.T) {
	tmpl, err := ParseTemplate("sample", "Subject: Hello {recipient_username}\n---\nYour account {recipient_email} is ready.\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tmpl.Subject != "Hello {recipient_username}" {
		t.Fatalf("unexpected subject %q", tmpl.Subject)
	}
	if !strings.HasSuffix(tmpl.Body, "\n") {
		t.Fatal("body must end with exactly one newline")
	}

	variables := tmpl.Variables()
	if len(variables) != 2 || variables[0] != "recipient_email" || variables[1] != "recipient_username" {
		t.Fatalf("unexpected variables %v", variables)
	}
}

func TestParseTemplateRejectsMalformedSource(t *testing.T) {
	cases := map[string]string{
		"no subject header": "Hello\n---\nbody\n",
		"empty subject":     "Subject:\n---\nbody\n",
		"no separator":      "Subject: Hello\nbody\n",
		"empty body":        "Subject: Hello\n---\n\n",
	}
	for name, source := range cases {
		if _, err := ParseTemplate("sample", source); err == nil {
			t.Fatalf("%s: expected a parse failure", name)
		}
	}
}

func TestRenderSubstitutesAndBlanksUnknownVariables(t *testing.T) {
	tmpl, err := ParseTemplate("sample", "Subject: {app_name} alert\n---\nHi {recipient_username}, code {missing}.\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	subject, body := tmpl.Render(map[string]string{"app_name": "cashp", "recipient_username": "ada"})
	if subject != "cashp alert" {
		t.Fatalf("unexpected subject %q", subject)
	}
	if strings.Contains(body, "{missing}") {
		t.Fatalf("an unset variable must not leak braces: %q", body)
	}
	if !strings.Contains(body, "Hi ada, code .") {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestRenderLeavesNonVariableBracesAlone(t *testing.T) {
	tmpl, err := ParseTemplate("sample", "Subject: Config\n---\nUse {\"key\": 1} and {Not A Var} verbatim.\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, body := tmpl.Render(map[string]string{})
	if !strings.Contains(body, `{"key": 1}`) || !strings.Contains(body, "{Not A Var}") {
		t.Fatalf("non-variable braces must survive rendering: %q", body)
	}
}

func TestTemplatesEmbeddedDefaultsAreValid(t *testing.T) {
	set := NewTemplates("", time.Now)
	names := set.Names()
	if len(names) == 0 {
		t.Fatal("the embedded template set must not be empty")
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("embedded templates must all parse: %v", err)
	}
	for _, name := range names {
		tmpl, err := set.Get(name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if tmpl.Custom {
			t.Fatalf("%s must not be reported as a custom override", name)
		}
	}
}

func TestTemplatesEveryEmailableEventHasATemplate(t *testing.T) {
	set := NewTemplates("", time.Now)
	for _, event := range Events() {
		if event.Template == "" {
			continue
		}
		if _, err := set.Get(event.Template); err != nil {
			t.Fatalf("event %s names template %s which does not exist: %v", event.Name, event.Template, err)
		}
	}
}

func TestTemplatesSaveOverridesAndResetRestores(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "template", "email")
	set := NewTemplates(dir, time.Now)

	original, err := set.Get(EventWelcome)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}

	if err := set.Save(EventWelcome, "Subject: Custom welcome\n---\nHello {recipient_email}.\n"); err != nil {
		t.Fatalf("save: %v", err)
	}
	overridden, err := set.Get(EventWelcome)
	if err != nil {
		t.Fatalf("get override: %v", err)
	}
	if !overridden.Custom || overridden.Subject != "Custom welcome" {
		t.Fatalf("the override was not picked up: %+v", overridden)
	}

	if err := set.Reset(EventWelcome); err != nil {
		t.Fatalf("reset: %v", err)
	}
	restored, err := set.Get(EventWelcome)
	if err != nil {
		t.Fatalf("get restored: %v", err)
	}
	if restored.Custom || restored.Subject != original.Subject {
		t.Fatalf("reset did not restore the embedded default: %+v", restored)
	}
}

func TestTemplatesSaveRejectsMalformedOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "template", "email")
	set := NewTemplates(dir, time.Now)

	if err := set.Save(EventWelcome, "no subject line at all\n"); err == nil {
		t.Fatal("a malformed override must be rejected before it is written")
	}
	if _, err := os.Stat(filepath.Join(dir, EventWelcome+".txt")); !os.IsNotExist(err) {
		t.Fatal("a rejected override must not be written to disk")
	}
}

func TestTemplatesRejectPathTraversalNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "template", "email")
	set := NewTemplates(dir, time.Now)

	for _, name := range []string{"../escape", "sub/dir", "..", "UPPER", "with space", ""} {
		if _, err := set.Get(name); err == nil {
			t.Fatalf("Get must reject %q", name)
		}
		if err := set.Save(name, "Subject: x\n---\nbody\n"); err == nil {
			t.Fatalf("Save must reject %q", name)
		}
		if err := set.Reset(name); err == nil {
			t.Fatalf("Reset must reject %q", name)
		}
	}
}

func TestTemplatesPreviewFillsEveryVariable(t *testing.T) {
	set := NewTemplates("", time.Now)
	for _, name := range set.Names() {
		subject, body, err := set.Preview(name, nil)
		if err != nil {
			t.Fatalf("preview %s: %v", name, err)
		}
		if subject == "" || body == "" {
			t.Fatalf("preview of %s produced an empty render", name)
		}
		if strings.Contains(subject, "{}") {
			t.Fatalf("preview of %s left an empty placeholder in the subject", name)
		}
	}
}

func TestTemplatesLiveEditIsPickedUpWithoutRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "template", "email")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, EventWelcome+".txt")
	set := NewTemplates(dir, time.Now)

	if err := os.WriteFile(path, []byte("Subject: First\n---\nBody one for {recipient_email}.\n"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	first, err := set.Get(EventWelcome)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if first.Subject != "First" {
		t.Fatalf("unexpected first subject %q", first.Subject)
	}

	if err := os.WriteFile(path, []byte("Subject: Second\n---\nBody two for {recipient_email}.\n"), 0o640); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	// The cache keys on the file's modification time, so an edit within the
	// same filesystem timestamp tick needs the stamp moved explicitly.
	stamp := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	second, err := set.Get(EventWelcome)
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if second.Subject != "Second" {
		t.Fatalf("an edited template must be reloaded, got %q", second.Subject)
	}
}

func TestGlobalVarsBuildsOnionAndI2PURLs(t *testing.T) {
	stamp := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	vars := GlobalVars("cashp", "https://panel.example.com", "panel.example.com", "abc.onion", "abc.b32.i2p", "admin@example.com", stamp)

	if vars["onion_url"] != "http://abc.onion" {
		t.Fatalf("unexpected onion_url %q", vars["onion_url"])
	}
	if vars["i2p_url"] != "http://abc.b32.i2p" {
		t.Fatalf("unexpected i2p_url %q", vars["i2p_url"])
	}
	if vars["year"] != "2026" {
		t.Fatalf("unexpected year %q", vars["year"])
	}

	bare := GlobalVars("cashp", "https://panel.example.com", "panel.example.com", "", "", "admin@example.com", stamp)
	if bare["onion_url"] != "" || bare["i2p_url"] != "" {
		t.Fatal("an instance without hidden services must resolve those URLs to empty strings")
	}
}

func TestSecurityTemplatesShowTheRecipientAddress(t *testing.T) {
	set := NewTemplates("", time.Now)
	for _, event := range Events() {
		if event.Category != CategorySecurity || event.Template == "" {
			continue
		}
		tmpl, err := set.Get(event.Template)
		if err != nil {
			t.Fatalf("get %s: %v", event.Template, err)
		}
		if !strings.Contains(tmpl.Body, "{recipient_email}") {
			t.Fatalf("security template %s must name the account it was sent to", event.Template)
		}
	}
}
