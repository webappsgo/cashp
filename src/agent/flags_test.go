package main

import (
	"errors"
	"testing"
)

func TestParseArgsInformationalFlags(t *testing.T) {
	opts, err := ParseArgs([]string{"-h"})
	if err != nil {
		t.Fatalf("ParseArgs(-h): %v", err)
	}
	if !opts.Help {
		t.Fatal("expected --help to be set")
	}

	opts, err = ParseArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("ParseArgs(--version): %v", err)
	}
	if !opts.Version {
		t.Fatal("expected --version to be set")
	}
}

func TestParseArgsValueFlags(t *testing.T) {
	opts, err := ParseArgs([]string{
		"--config", "/etc/agent",
		"--data=/var/lib/agent",
		"--log", "/var/log/agent",
		"--server", "https://panel.example.com",
		"--token", "adm_agt_0123456789abcdef0123456789abcdef",
		"--org", "acme",
		"--mode", "debug",
		"--color", "no",
		"--lang", "en",
		"--debug",
	})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}

	cases := map[string][2]string{
		"config": {opts.ConfigDir, "/etc/agent"},
		"data":   {opts.DataDir, "/var/lib/agent"},
		"log":    {opts.LogDir, "/var/log/agent"},
		"server": {opts.Server, "https://panel.example.com"},
		"token":  {opts.Token, "adm_agt_0123456789abcdef0123456789abcdef"},
		"org":    {opts.Org, "acme"},
		"mode":   {opts.Mode, "debug"},
		"color":  {opts.Color, "no"},
		"lang":   {opts.Lang, "en"},
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("--%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if !opts.Debug {
		t.Error("expected --debug to be set")
	}
}

func TestParseArgsCommands(t *testing.T) {
	for _, command := range []string{CommandStatus, CommandTest, CommandRegister} {
		opts, err := ParseArgs([]string{command})
		if err != nil {
			t.Fatalf("ParseArgs(%s): %v", command, err)
		}
		if opts.Command != command {
			t.Errorf("Command = %q, want %q", opts.Command, command)
		}
	}
}

func TestParseArgsRejectsUnknownInput(t *testing.T) {
	cases := [][]string{
		{"deploy"},
		{"--nope"},
		{"status", "test"},
		{"--server"},
		{"--mode", "turbo"},
		{"--color", "rainbow"},
		{"--update", "maybe"},
		{"--service", "frobnicate"},
	}
	for _, args := range cases {
		if _, err := ParseArgs(args); err == nil {
			t.Errorf("ParseArgs(%v) accepted invalid input", args)
		} else if !errors.Is(err, ErrUsage) {
			t.Errorf("ParseArgs(%v) error = %v, want ErrUsage", args, err)
		}
	}
}

func TestParseArgsServiceAcceptsBothForms(t *testing.T) {
	plain, err := ParseArgs([]string{"--service", "install"})
	if err != nil {
		t.Fatalf("ParseArgs(--service install): %v", err)
	}
	dashed, err := ParseArgs([]string{"--service", "--install"})
	if err != nil {
		t.Fatalf("ParseArgs(--service --install): %v", err)
	}
	inline, err := ParseArgs([]string{"--service=restart"})
	if err != nil {
		t.Fatalf("ParseArgs(--service=restart): %v", err)
	}

	if plain.Service != "install" || dashed.Service != "install" {
		t.Errorf("service = %q/%q, want install", plain.Service, dashed.Service)
	}
	if inline.Service != "restart" {
		t.Errorf("service = %q, want restart", inline.Service)
	}
}

func TestParseArgsUpdateOptionalArgument(t *testing.T) {
	bare, err := ParseArgs([]string{"--update"})
	if err != nil {
		t.Fatalf("ParseArgs(--update): %v", err)
	}
	if !bare.UpdateSet || bare.UpdateMode != "check" {
		t.Fatalf("--update defaulted to %q, want check", bare.UpdateMode)
	}

	confirmed, err := ParseArgs([]string{"--update", "yes"})
	if err != nil {
		t.Fatalf("ParseArgs(--update yes): %v", err)
	}
	if confirmed.UpdateMode != "yes" {
		t.Fatalf("--update = %q, want yes", confirmed.UpdateMode)
	}

	// A command after a bare --update must stay a command, not be eaten as
	// the flag's optional argument.
	mixed, err := ParseArgs([]string{"--update", "status"})
	if err != nil {
		t.Fatalf("ParseArgs(--update status): %v", err)
	}
	if mixed.Command != CommandStatus || mixed.UpdateMode != "check" {
		t.Fatalf("mixed parse = command %q update %q", mixed.Command, mixed.UpdateMode)
	}
}

func TestParseArgsShellOptionalArguments(t *testing.T) {
	bare, err := ParseArgs([]string{"--shell"})
	if err != nil {
		t.Fatalf("ParseArgs(--shell): %v", err)
	}
	if !bare.ShellSet || bare.ShellAction != "" {
		t.Fatalf("bare --shell parsed action %q", bare.ShellAction)
	}

	full, err := ParseArgs([]string{"--shell", "completions", "fish"})
	if err != nil {
		t.Fatalf("ParseArgs(--shell completions fish): %v", err)
	}
	if full.ShellAction != "completions" || full.ShellName != "fish" {
		t.Fatalf("--shell parsed %q/%q", full.ShellAction, full.ShellName)
	}
}
