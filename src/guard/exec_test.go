package guard

import (
	"context"
	"strings"
	"testing"

	"github.com/webappsgo/cashp/src/security"
)

// testExecPolicy registers a small binary set with a narrow flag allowlist.
func testExecPolicy(t *testing.T) ExecPolicy {
	t.Helper()
	policy, err := NewExecPolicy(
		map[string]string{
			"systemctl": "/usr/bin/systemctl",
			"nginx":     "/usr/sbin/nginx",
		},
		[]string{"-t", "--reload", "--token"},
	)
	if err != nil {
		t.Fatalf("NewExecPolicy failed: %v", err)
	}
	return policy
}

func TestNewExecPolicyRefusesInterpretersAndRelativePaths(t *testing.T) {
	for name, path := range map[string]string{
		"bash":    "/bin/bash",
		"sh":      "/bin/sh",
		"python3": "/usr/bin/python3",
		"perl":    "/usr/bin/perl",
		"env":     "/usr/bin/env",
		"node":    "/usr/bin/node",
		"ssh":     "/usr/bin/ssh",
		"xargs":   "/usr/bin/xargs",
	} {
		if _, err := NewExecPolicy(map[string]string{name: path}, nil); err == nil {
			t.Fatalf("NewExecPolicy registered the interpreter %q", path)
		}
	}

	for _, path := range []string{"systemctl", "./systemctl", "/usr/bin/../bin/systemctl", "/usr/bin/"} {
		if _, err := NewExecPolicy(map[string]string{"systemctl": path}, nil); err == nil {
			t.Fatalf("NewExecPolicy accepted the path %q", path)
		}
	}

	if _, err := NewExecPolicy(map[string]string{"sys;ctl": "/usr/bin/systemctl"}, nil); err == nil {
		t.Fatal("NewExecPolicy accepted a binary name containing a metacharacter")
	}
}

func TestNewExecPolicyRefusesCodeEvaluatingFlags(t *testing.T) {
	for _, flag := range []string{"-c", "--command", "-e", "--eval", "--exec"} {
		if _, err := NewExecPolicy(map[string]string{"nginx": "/usr/sbin/nginx"}, []string{flag}); err == nil {
			t.Fatalf("NewExecPolicy allowlisted the code-evaluating flag %q", flag)
		}
	}
	for _, flag := range []string{"reload", "--token=abc", "--token value"} {
		if _, err := NewExecPolicy(map[string]string{"nginx": "/usr/sbin/nginx"}, []string{flag}); err == nil {
			t.Fatalf("NewExecPolicy accepted the malformed flag entry %q", flag)
		}
	}
}

func TestNewCommandRefusesUnregisteredBinaries(t *testing.T) {
	policy := testExecPolicy(t)
	for _, name := range []string{"bash", "sh", "curl", "", "systemctl; rm -rf /", "SYSTEMCTL"} {
		if _, err := NewCommand(policy, name); err == nil {
			t.Fatalf("NewCommand ran the unregistered binary %q", name)
		}
	}
}

func TestNewCommandRefusesArgumentInjection(t *testing.T) {
	policy := testExecPolicy(t)
	for _, arg := range []string{
		"-c",
		"--eval",
		"--exec",
		"-e",
		"--rm",
		"-v",
		"--privileged",
		"--net=host",
		"-oProxyCommand=id",
		"arg\x00extra",
		"arg\nsecond-line",
		"arg\rsecond",
	} {
		if _, err := NewCommand(policy, "nginx", arg); err == nil {
			t.Fatalf("NewCommand accepted the hostile argument %q", arg)
		}
	}
}

func TestNewCommandKeepsMetacharactersInert(t *testing.T) {
	policy := testExecPolicy(t)
	// A shell metacharacter inside a value is harmless because no shell is
	// ever involved; the value must survive intact as one argv element.
	payload := "reload; rm -rf / #"
	cmd, err := NewCommand(policy, "nginx", payload)
	if err != nil {
		t.Fatalf("NewCommand rejected an inert value: %v", err)
	}
	args := cmd.Args()
	if len(args) != 1 || args[0] != payload {
		t.Fatalf("NewCommand mangled or split the argument: %v", args)
	}
	if cmd.Path() != "/usr/sbin/nginx" {
		t.Fatalf("NewCommand resolved to %q", cmd.Path())
	}
}

func TestCommandPinsTheBinaryPathAndEnvironment(t *testing.T) {
	policy := testExecPolicy(t)
	policy.Env = map[string]string{"APP_ENV": "production"}
	cmd, err := NewCommand(policy, "systemctl", "--reload")
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}

	built := cmd.Cmd(context.Background())
	if built.Path != "/usr/bin/systemctl" {
		t.Fatalf("Cmd did not pin the registered path, got %q", built.Path)
	}
	// An empty environment plus only what the policy set means PATH cannot
	// be inherited and used to redirect a lookup.
	if len(built.Env) != 1 || built.Env[0] != "APP_ENV=production" {
		t.Fatalf("Cmd carried an unexpected environment: %v", built.Env)
	}
}

func TestNewCommandRejectsHostilePolicyEnvironment(t *testing.T) {
	policy := testExecPolicy(t)
	policy.Env = map[string]string{"LD_PRELOAD": "/tmp/evil.so"}
	if _, err := NewCommand(policy, "nginx"); err == nil {
		t.Fatal("NewCommand accepted a policy environment setting LD_PRELOAD")
	}

	policy = testExecPolicy(t)
	policy.Dir = "../etc"
	if _, err := NewCommand(policy, "nginx"); err == nil {
		t.Fatal("NewCommand accepted a relative working directory")
	}
}

func TestCommandStringMasksSensitiveArguments(t *testing.T) {
	policy := testExecPolicy(t)
	cmd, err := NewCommand(policy, "nginx", "--token=s3cr3t-value", "--reload")
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}
	rendered := cmd.String()
	if strings.Contains(rendered, "s3cr3t-value") {
		t.Fatalf("Command.String leaked a credential: %q", rendered)
	}
	if !strings.Contains(rendered, security.MaskedValue) {
		t.Fatalf("Command.String did not mask the credential: %q", rendered)
	}

	separated, err := NewCommand(policy, "nginx", "--token", "s3cr3t-value")
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}
	if strings.Contains(separated.String(), "s3cr3t-value") {
		t.Fatalf("Command.String leaked a separated credential: %q", separated.String())
	}
}

func TestNilCommandIsInert(t *testing.T) {
	var cmd *Command
	if cmd.Path() != "" || cmd.Args() != nil || cmd.Env() != nil || cmd.String() != "" {
		t.Fatal("a nil Command returned non-zero values")
	}
	if cmd.Cmd(context.Background()) != nil {
		t.Fatal("a nil Command produced a runnable exec.Cmd")
	}
}
