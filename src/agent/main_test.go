package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"--help"}, stdout, stderr); code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}

	text := stdout.String()
	for _, want := range []string{"Usage:", "Commands:", "status", "test", "register", "--service", "--update"} {
		if !strings.Contains(text, want) {
			t.Errorf("help output is missing %q", want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("help wrote to stderr: %q", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"-v"}, stdout, stderr); code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}

	text := stdout.String()
	if !strings.Contains(text, Version) || !strings.Contains(text, "commit "+CommitID) {
		t.Fatalf("version output = %q", text)
	}
}

func TestRunShellCompletions(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"--shell", "completions", "bash"}, stdout, stderr); code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "complete -F") {
		t.Fatalf("bash completions = %q", stdout.String())
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"--frobnicate"}, stdout, stderr); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown flag") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("usage error wrote to stdout: %q", stdout.String())
	}
}

func TestRunRejectsUnsupportedShell(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"--shell", "completions", "elvish"}, stdout, stderr); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}
