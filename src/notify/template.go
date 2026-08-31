package notify

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/webappsgo/cashp/src/errors"
)

// defaultTemplates holds the templates compiled into the binary. AI.md
// PART 18 -> "Template Storage" requires working defaults with no files on
// disk; a custom template in {config_dir}/template/email/ overrides one.
//
//go:embed template/email/*.txt
var defaultTemplates embed.FS

// embeddedDir is the path prefix of the embedded template set.
const embeddedDir = "template/email"

// CustomTemplateDir returns the directory holding operator overrides for a
// given config directory.
func CustomTemplateDir(configDir string) string {
	return filepath.Join(configDir, "template", "email")
}

// Template errors.
var (
	// ErrTemplateNotFound names a template that has neither a custom file
	// nor an embedded default.
	ErrTemplateNotFound = errors.New(errors.CodeNotFound, http.StatusNotFound, "email template not found")
	// ErrTemplateInvalid rejects a template whose source is malformed.
	ErrTemplateInvalid = errors.New(errors.CodeValidation, http.StatusBadRequest, "email template is invalid")
)

// separator is the line dividing the subject header from the body.
const separator = "---"

// subjectPrefix is the required first-line header.
const subjectPrefix = "Subject:"

// Template is one parsed email template.
type Template struct {
	// Name is the template name without extension, for example
	// "password_reset".
	Name string
	// Subject is the raw subject line, variables unsubstituted.
	Subject string
	// Body is the raw plain-text body, variables unsubstituted.
	Body string
	// Custom reports whether this came from the operator override
	// directory rather than the embedded defaults.
	Custom bool
	// Source is the unparsed template text, for the admin panel editor.
	Source string
}

// Variables returns every distinct {variable} the template references, in
// sorted order.
func (t Template) Variables() []string {
	seen := map[string]struct{}{}
	for _, text := range []string{t.Subject, t.Body} {
		for _, name := range scanVariables(text) {
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Render substitutes vars into the subject and body. Unknown placeholders
// are replaced with an empty string so a partially populated variable set
// never leaks raw braces into a delivered email.
func (t Template) Render(vars map[string]string) (subject, body string) {
	return expand(t.Subject, vars), expand(t.Body, vars)
}

// ParseTemplate parses raw template source in the PART 18 format: a
// "Subject: ..." first line, a --- separator line, then a plain-text body.
func ParseTemplate(name, source string) (Template, error) {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), subjectPrefix) {
		return Template{}, ErrTemplateInvalid.WithDetails(map[string]any{
			"template": name,
			"reason":   "first line must start with " + subjectPrefix,
		})
	}

	subject := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), subjectPrefix))
	if subject == "" {
		return Template{}, ErrTemplateInvalid.WithDetails(map[string]any{"template": name, "reason": "subject is empty"})
	}

	sep := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == separator {
			sep = i
			break
		}
	}
	if sep < 0 {
		return Template{}, ErrTemplateInvalid.WithDetails(map[string]any{
			"template": name,
			"reason":   "missing --- separator line",
		})
	}

	body := strings.TrimLeft(strings.Join(lines[sep+1:], "\n"), "\n")
	if strings.TrimSpace(body) == "" {
		return Template{}, ErrTemplateInvalid.WithDetails(map[string]any{"template": name, "reason": "body is empty"})
	}

	return Template{Name: name, Subject: subject, Body: strings.TrimRight(body, "\n") + "\n", Source: source}, nil
}

// Templates loads, caches and renders the email template set. It is safe
// for concurrent use.
type Templates struct {
	mu        sync.RWMutex
	customDir string
	cache     map[string]cachedTemplate
	now       func() time.Time
}

// cachedTemplate remembers a parsed template with the modification time of
// the custom file it came from, so a live edit is picked up without a
// restart as AI.md requires.
type cachedTemplate struct {
	tmpl    Template
	modTime time.Time
}

// NewTemplates returns a template set overlaying customDir on the embedded
// defaults. customDir may be empty, in which case only defaults are used.
func NewTemplates(customDir string, now func() time.Time) *Templates {
	if now == nil {
		now = time.Now
	}
	return &Templates{customDir: customDir, cache: map[string]cachedTemplate{}, now: now}
}

// Names returns every known template name, custom and embedded, sorted.
func (t *Templates) Names() []string {
	seen := map[string]struct{}{}
	if entries, err := defaultTemplates.ReadDir(embeddedDir); err == nil {
		for _, entry := range entries {
			if name := templateName(entry.Name()); name != "" {
				seen[name] = struct{}{}
			}
		}
	}
	if t.customDir != "" {
		if entries, err := os.ReadDir(t.customDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				if name := templateName(entry.Name()); name != "" {
					seen[name] = struct{}{}
				}
			}
		}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Get returns a template by name, preferring the operator override.
func (t *Templates) Get(name string) (Template, error) {
	if !validTemplateName(name) {
		return Template{}, ErrTemplateNotFound.WithDetails(map[string]any{"template": name})
	}

	modTime, hasCustom := t.customModTime(name)

	t.mu.RLock()
	cached, ok := t.cache[name]
	t.mu.RUnlock()
	if ok && cached.modTime.Equal(modTime) {
		return cached.tmpl, nil
	}

	tmpl, err := t.load(name, hasCustom)
	if err != nil {
		return Template{}, err
	}

	t.mu.Lock()
	t.cache[name] = cachedTemplate{tmpl: tmpl, modTime: modTime}
	t.mu.Unlock()
	return tmpl, nil
}

// Save writes an operator override and invalidates the cache. The template
// is parsed first so a malformed override can never reach a recipient.
func (t *Templates) Save(name, source string) error {
	if !validTemplateName(name) {
		return ErrTemplateNotFound.WithDetails(map[string]any{"template": name})
	}
	if t.customDir == "" {
		return ErrTemplateInvalid.WithDetails(map[string]any{"reason": "no custom template directory configured"})
	}
	if _, err := ParseTemplate(name, source); err != nil {
		return err
	}
	if err := os.MkdirAll(t.customDir, 0o750); err != nil {
		return errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "create template directory")
	}
	if err := os.WriteFile(t.customPath(name), []byte(source), 0o640); err != nil {
		return errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "write email template")
	}

	t.mu.Lock()
	delete(t.cache, name)
	t.mu.Unlock()
	return nil
}

// Reset removes an operator override so the embedded default applies again.
// Resetting a template that was never overridden is not an error.
func (t *Templates) Reset(name string) error {
	if !validTemplateName(name) {
		return ErrTemplateNotFound.WithDetails(map[string]any{"template": name})
	}
	if t.customDir != "" {
		if err := os.Remove(t.customPath(name)); err != nil && !os.IsNotExist(err) {
			return errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "remove email template")
		}
	}

	t.mu.Lock()
	delete(t.cache, name)
	t.mu.Unlock()
	return nil
}

// Render loads a template and substitutes vars in one step.
func (t *Templates) Render(name string, vars map[string]string) (subject, body string, err error) {
	tmpl, err := t.Get(name)
	if err != nil {
		return "", "", err
	}
	subject, body = tmpl.Render(vars)
	return subject, body, nil
}

// Preview renders a template against sample data for the admin panel's
// preview pane, filling any variable the caller did not supply.
func (t *Templates) Preview(name string, vars map[string]string) (subject, body string, err error) {
	tmpl, err := t.Get(name)
	if err != nil {
		return "", "", err
	}
	merged := SampleVars()
	for key, value := range vars {
		if value != "" {
			merged[key] = value
		}
	}
	for _, variable := range tmpl.Variables() {
		if _, ok := merged[variable]; !ok {
			merged[variable] = "{" + variable + "}"
		}
	}
	subject, body = tmpl.Render(merged)
	return subject, body, nil
}

// Validate parses every known template and reports the first failure. It
// backs the admin panel's "check templates" action and the startup check.
func (t *Templates) Validate() error {
	for _, name := range t.Names() {
		tmpl, err := t.Get(name)
		if err != nil {
			return err
		}
		if event, ok := Lookup(name); ok && event.Category == CategorySecurity {
			if !strings.Contains(tmpl.Body, "{recipient_email}") {
				return ErrTemplateInvalid.WithDetails(map[string]any{
					"template": name,
					"reason":   "account email must show {recipient_email} in the body",
				})
			}
		}
	}
	return nil
}

// load reads a template from the override directory or the embedded set.
func (t *Templates) load(name string, custom bool) (Template, error) {
	if custom {
		data, err := os.ReadFile(t.customPath(name))
		if err == nil {
			tmpl, parseErr := ParseTemplate(name, string(data))
			if parseErr != nil {
				return Template{}, parseErr
			}
			tmpl.Custom = true
			return tmpl, nil
		}
		if !os.IsNotExist(err) {
			return Template{}, errors.Wrap(err, errors.CodeInternal, http.StatusInternalServerError, "read email template")
		}
	}

	data, err := defaultTemplates.ReadFile(embeddedDir + "/" + name + ".txt")
	if err != nil {
		return Template{}, ErrTemplateNotFound.WithDetails(map[string]any{"template": name})
	}
	return ParseTemplate(name, string(data))
}

// customModTime reports the modification time of an override file and
// whether one exists.
func (t *Templates) customModTime(name string) (time.Time, bool) {
	if t.customDir == "" {
		return time.Time{}, false
	}
	info, err := os.Stat(t.customPath(name))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// customPath returns the override file path for a template name.
func (t *Templates) customPath(name string) string {
	return filepath.Join(t.customDir, name+".txt")
}

// templateName strips the .txt suffix, returning an empty string for any
// other file so unrelated files in the override directory are ignored.
func templateName(fileName string) string {
	if !strings.HasSuffix(fileName, ".txt") {
		return ""
	}
	name := strings.TrimSuffix(fileName, ".txt")
	if !validTemplateName(name) {
		return ""
	}
	return name
}

// validTemplateName rejects anything that is not a bare lowercase
// identifier, which also blocks path traversal through a template name.
func validTemplateName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// scanVariables returns the {variable} names appearing in text, with
// duplicates.
func scanVariables(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		end := strings.IndexByte(text[i:], '}')
		if end < 0 {
			break
		}
		name := text[i+1 : i+end]
		if isVariableName(name) {
			out = append(out, name)
		}
		i += end
	}
	return out
}

// expand substitutes every {variable} in text. A placeholder with no value
// resolves to an empty string; anything that is not a well-formed variable
// name is copied through untouched.
func expand(text string, vars map[string]string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			b.WriteByte(text[i])
			continue
		}
		end := strings.IndexByte(text[i:], '}')
		if end < 0 {
			b.WriteString(text[i:])
			break
		}
		name := text[i+1 : i+end]
		if !isVariableName(name) {
			b.WriteByte(text[i])
			continue
		}
		b.WriteString(vars[name])
		i += end
	}
	return b.String()
}

// isVariableName reports whether name is a legal {variable} identifier:
// lowercase letters, digits and underscores only.
func isVariableName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// GlobalVars builds the variable set AI.md PART 18 makes available in every
// template. Values that do not apply to this instance resolve to an empty
// string rather than a placeholder.
func GlobalVars(appName, appURL, fqdn, onion, i2p, adminEmail string, now time.Time) map[string]string {
	vars := map[string]string{
		"app_name":           appName,
		"app_url":            appURL,
		"fqdn":               fqdn,
		"onion_url":          "",
		"onion_address":      onion,
		"i2p_url":            "",
		"i2p_address":        i2p,
		"admin_email":        adminEmail,
		"recipient_email":    "",
		"recipient_username": "",
		"timestamp":          now.UTC().Format(time.RFC1123),
		"year":               fmt.Sprintf("%d", now.UTC().Year()),
	}
	if onion != "" {
		vars["onion_url"] = "http://" + onion
	}
	if i2p != "" {
		vars["i2p_url"] = "http://" + i2p
	}
	return vars
}

// SampleVars returns representative values for the admin panel's template
// preview. No real recipient data is ever used for a preview.
func SampleVars() map[string]string {
	vars := GlobalVars(
		"cashp",
		"https://panel.example.com",
		"panel.example.com",
		"exampleonionaddress23456789abcdefghijklmnopqrstuvwxyz234567.onion",
		"exampleeepsiteaddress23456789abcdefghijklmnopqrstuv.b32.i2p",
		"admin@example.com",
		time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC),
	)
	sample := map[string]string{
		"recipient_email":    "user@example.com",
		"recipient_username": "example-user",
		"reset_url":          "https://panel.example.com/account/reset?token=sample-token",
		"verify_url":         "https://panel.example.com/account/verify?token=sample-token",
		"setup_url":          "https://panel.example.com/setup?token=sample-token",
		"expiry":             "24 hours",
		"ip_address":         "203.0.113.10",
		"location":           "Unknown",
		"user_agent":         "Mozilla/5.0",
		"device":             "Firefox on Linux",
		"filename":           "cashp-backup-20260102-150405.tar.zst",
		"size":               "412 MB",
		"duration":           "1m12s",
		"domain":             "panel.example.com",
		"days_left":          "14",
		"expires_at":         "2026-01-16 15:04:05 UTC",
		"error":              "connection refused",
		"task":               "backup_daily",
		"version":            "1.2.3",
		"previous_version":   "1.2.2",
		"severity":           "high",
		"detail":             "Sample detail line for preview only.",
		"count":              "3",
		"action_url":         "https://panel.example.com/account/security",
	}
	for key, value := range sample {
		vars[key] = value
	}
	return vars
}
