package auth

import "testing"

func TestValidRegistrationMode(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{RegistrationOpen, true},
		{RegistrationInvite, true},
		{RegistrationAdminOnly, true},
		{RegistrationDisabled, true},
		{"", false},
		{"bogus", false},
	}
	for _, c := range cases {
		if got := validRegistrationMode(c.in); got != c.want {
			t.Errorf("validRegistrationMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidOrgCreationMode(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{OrgCreationOpen, true},
		{OrgCreationInvite, true},
		{OrgCreationAdminOnly, true},
		{OrgCreationDisabled, true},
		{"", false},
		{"bogus", false},
	}
	for _, c := range cases {
		if got := validOrgCreationMode(c.in); got != c.want {
			t.Errorf("validOrgCreationMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDefaultConfigIsInternallyValid(t *testing.T) {
	c := DefaultConfig()
	if !validRegistrationMode(c.RegistrationMode) {
		t.Errorf("DefaultConfig().RegistrationMode = %q is not a valid mode", c.RegistrationMode)
	}
	if !validOrgCreationMode(c.OrgCreationMode) {
		t.Errorf("DefaultConfig().OrgCreationMode = %q is not a valid mode", c.OrgCreationMode)
	}
	if c.SiteName == "" || c.BaseURL == "" || c.AdminPath == "" || c.APIVersion == "" {
		t.Error("DefaultConfig must not leave core identity fields blank")
	}
}

func TestConfigNormalizeFillsBlanksWithDefaults(t *testing.T) {
	c := Config{}
	c.normalize()

	if c.SiteName == "" {
		t.Error("normalize must fill blank SiteName")
	}
	if c.BaseURL == "" {
		t.Error("normalize must fill blank BaseURL")
	}
	if c.AdminPath == "" {
		t.Error("normalize must fill blank AdminPath")
	}
	if c.APIVersion == "" {
		t.Error("normalize must fill blank APIVersion")
	}
	if c.SessionTTL <= 0 {
		t.Error("normalize must fill a non-positive SessionTTL with a default")
	}
	if c.MaxSessionsPerUser <= 0 {
		t.Error("normalize must fill a non-positive MaxSessionsPerUser with a default")
	}
	if c.DomainVerificationPrefix == "" {
		t.Error("normalize must fill blank DomainVerificationPrefix")
	}
	if c.DomainVerificationTTL <= 0 {
		t.Error("normalize must fill a non-positive DomainVerificationTTL with a default")
	}
	if !validRegistrationMode(c.RegistrationMode) {
		t.Errorf("normalize left an invalid RegistrationMode %q", c.RegistrationMode)
	}
	if !validOrgCreationMode(c.OrgCreationMode) {
		t.Errorf("normalize left an invalid OrgCreationMode %q", c.OrgCreationMode)
	}
}

func TestConfigNormalizeRejectsInvalidModesWithDefault(t *testing.T) {
	c := Config{RegistrationMode: "not-a-real-mode", OrgCreationMode: "also-bogus"}
	c.normalize()
	if !validRegistrationMode(c.RegistrationMode) {
		t.Errorf("normalize left invalid RegistrationMode %q", c.RegistrationMode)
	}
	if !validOrgCreationMode(c.OrgCreationMode) {
		t.Errorf("normalize left invalid OrgCreationMode %q", c.OrgCreationMode)
	}
}

func TestConfigNormalizePreservesExplicitValidValues(t *testing.T) {
	c := Config{
		SiteName:                "myhost",
		BaseURL:                 "https://example.com",
		AdminPath:               "backoffice",
		APIVersion:              "v2",
		RegistrationMode:        RegistrationInvite,
		OrgCreationMode:         OrgCreationAdminOnly,
		SessionTTL:              SessionTTL,
		MaxSessionsPerUser:      5,
		DomainVerificationTTL:   SessionTTL,
		DomainVerificationPrefix: "_verify",
	}
	c.normalize()
	if c.SiteName != "myhost" || c.BaseURL != "https://example.com" || c.AdminPath != "backoffice" || c.APIVersion != "v2" {
		t.Error("normalize must not overwrite explicitly set identity fields")
	}
	if c.RegistrationMode != RegistrationInvite {
		t.Errorf("RegistrationMode = %q, want %q", c.RegistrationMode, RegistrationInvite)
	}
	if c.OrgCreationMode != OrgCreationAdminOnly {
		t.Errorf("OrgCreationMode = %q, want %q", c.OrgCreationMode, OrgCreationAdminOnly)
	}
	if c.MaxSessionsPerUser != 5 {
		t.Errorf("MaxSessionsPerUser = %d, want 5 (must not overwrite a valid positive value)", c.MaxSessionsPerUser)
	}
	if c.DomainVerificationPrefix != "_verify" {
		t.Errorf("DomainVerificationPrefix = %q, want _verify", c.DomainVerificationPrefix)
	}
}
