package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/agent/paths"
	"github.com/webappsgo/cashp/src/agent/settings"
)

// writeConfig drops an agent.yml with credential-safe permissions into dir.
func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, paths.ConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write agent.yml: %v", err)
	}
}

func TestLoadConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "server:\n  primary: https://file.example.com\nlang: fr\n")
	t.Setenv(settings.EnvPrefix+"SERVER", "https://env.example.com")
	t.Setenv(settings.EnvPrefix+"LANG", "de")

	// The environment beats the file.
	cfg, _, err := LoadConfig(&Options{ConfigDir: dir})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Server.Primary != "https://env.example.com" {
		t.Errorf("primary = %q, want the environment value", cfg.Server.Primary)
	}
	if cfg.Lang != "de" {
		t.Errorf("lang = %q, want de", cfg.Lang)
	}

	// The command line beats both.
	cfg, overrides, err := LoadConfig(&Options{
		ConfigDir: dir,
		Server:    "https://flag.example.com/",
		Lang:      "en",
		Debug:     true,
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Server.Primary != "https://flag.example.com" {
		t.Errorf("primary = %q, want the flag value without a trailing slash", cfg.Server.Primary)
	}
	if cfg.Lang != "en" || !cfg.Debug {
		t.Errorf("lang = %q debug = %v", cfg.Lang, cfg.Debug)
	}
	if overrides.Config != dir {
		t.Errorf("overrides.Config = %q, want %q", overrides.Config, dir)
	}
	if cfg.Server.APIVersion == "" {
		t.Error("normalization did not backfill the API version")
	}
}

func TestLoadConfigRejectsWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "server:\n  primary: https://panel.example.com\n")
	if err := os.Chmod(filepath.Join(dir, paths.ConfigFileName), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, _, err := LoadConfig(&Options{ConfigDir: dir}); !errors.Is(err, paths.ErrInsecurePerms) {
		t.Fatalf("LoadConfig error = %v, want ErrInsecurePerms", err)
	}
}

func TestResolveTokenPrecedence(t *testing.T) {
	dir := t.TempDir()
	overrides := paths.Overrides{Config: dir, Data: dir, Log: dir}
	fileToken := "adm_agt_" + repeat("a", 32)
	if err := os.WriteFile(paths.TokenFile(overrides), []byte(fileToken+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	cfg := settings.Defaults()
	token, err := ResolveToken(cfg, overrides, "")
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != fileToken {
		t.Errorf("token = %q, want the file token", token)
	}

	cfg.Auth.Token = "usr_agt_" + repeat("b", 32)
	token, err = ResolveToken(cfg, overrides, "")
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != cfg.Auth.Token {
		t.Errorf("token = %q, want the configured token", token)
	}

	flagToken := "org_agt_" + repeat("c", 32)
	token, err = ResolveToken(cfg, overrides, flagToken)
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != flagToken {
		t.Errorf("token = %q, want the flag token", token)
	}
}

func TestResolveTokenMissing(t *testing.T) {
	overrides := paths.Overrides{Config: t.TempDir()}
	if _, err := ResolveToken(settings.Defaults(), overrides, ""); !errors.Is(err, ErrNoToken) {
		t.Fatalf("ResolveToken error = %v, want ErrNoToken", err)
	}
}

func TestResolveTokenRejectsWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	overrides := paths.Overrides{Config: dir}
	if err := os.WriteFile(paths.TokenFile(overrides), []byte("adm_agt_"+repeat("a", 32)), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	if _, err := ResolveToken(settings.Defaults(), overrides, ""); !errors.Is(err, paths.ErrInsecurePerms) {
		t.Fatalf("ResolveToken error = %v, want ErrInsecurePerms", err)
	}
}

func TestParseDuration(t *testing.T) {
	fallback := 7 * time.Second
	cases := map[string]time.Duration{
		"30s":      30 * time.Second,
		"2m":       2 * time.Minute,
		"":         fallback,
		"nonsense": fallback,
		"-1s":      fallback,
	}
	for input, want := range cases {
		if got := ParseDuration(input, fallback); got != want {
			t.Errorf("ParseDuration(%q) = %s, want %s", input, got, want)
		}
	}
}

// repeat builds a fixed-length filler string for synthetic tokens.
func repeat(char string, count int) string {
	return strings.Repeat(char, count)
}
