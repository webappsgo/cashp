package task

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/webappsgo/cashp/src/agent/transport"
)

// stubRunner records the argv it was handed instead of running anything.
func stubRunner(recorded *[]string) Runner {
	return func(ctx context.Context, argv []string, env []string) (string, int, error) {
		*recorded = append([]string(nil), argv...)
		return "ok", 0, nil
	}
}

func TestValidateRejectsForeignAgent(t *testing.T) {
	executor := &Executor{AgentID: "agt-1"}

	_, _, err := executor.Validate(transport.Task{ID: "t1", AgentID: "agt-2", Action: "agent.ping"})
	if !errors.Is(err, ErrNotForThisAgent) {
		t.Fatalf("error = %v, want ErrNotForThisAgent", err)
	}

	_, _, err = executor.Validate(transport.Task{ID: "t1", AgentID: "", Action: "agent.ping"})
	if !errors.Is(err, ErrNotForThisAgent) {
		t.Fatalf("error = %v, want ErrNotForThisAgent for an unaddressed task", err)
	}
}

func TestValidateRejectsUnknownAction(t *testing.T) {
	executor := &Executor{AgentID: "agt-1"}

	for _, action := range []string{"", "shell.exec", "rm", "service.status; rm -rf /"} {
		_, _, err := executor.Validate(transport.Task{ID: "t1", AgentID: "agt-1", Action: action})
		if !errors.Is(err, ErrUnknownAction) {
			t.Errorf("action %q error = %v, want ErrUnknownAction", action, err)
		}
	}
}

func TestValidateRejectsMissingTaskID(t *testing.T) {
	executor := &Executor{AgentID: "agt-1"}
	if _, _, err := executor.Validate(transport.Task{AgentID: "agt-1", Action: "agent.ping"}); !errors.Is(err, ErrNoTaskID) {
		t.Fatalf("error = %v, want ErrNoTaskID", err)
	}
}

func TestValidateRejectsHostileArguments(t *testing.T) {
	executor := &Executor{AgentID: "agt-1"}
	hostile := [][]string{
		{"nginx; rm -rf /"},
		{"$(reboot)"},
		{"`id`"},
		{"../../etc/shadow"},
		{"nginx", "extra"},
		{},
	}

	for _, args := range hostile {
		_, _, err := executor.Validate(transport.Task{
			ID:      "t1",
			AgentID: "agt-1",
			Action:  "service.status",
			Args:    args,
		})
		if !errors.Is(err, ErrBadArguments) && !errors.Is(err, ErrUnsupportedPlatform) {
			t.Errorf("args %v error = %v, want a rejection", args, err)
		}
	}
}

func TestSafeEnvFiltersEverythingOutsideTheNamespace(t *testing.T) {
	env, err := SafeEnv(map[string]string{"CASHP_TASK_TARGET": "primary"})
	if err != nil {
		t.Fatalf("SafeEnv: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CASHP_TASK_TARGET=primary") {
		t.Fatalf("env = %v, want the namespaced variable", env)
	}
	if !strings.Contains(joined, "PATH=/usr/local/sbin") && runtime.GOOS != "windows" {
		t.Fatalf("env = %v, want a fixed PATH", env)
	}

	hostile := []map[string]string{
		{"PATH": "/tmp/evil"},
		{"LD_PRELOAD": "/tmp/evil.so"},
		{"cashp_task_lower": "x"},
		{"CASHP_TASK_BAD": "value\nPATH=/tmp"},
	}
	for _, supplied := range hostile {
		if _, err := SafeEnv(supplied); err == nil {
			t.Errorf("SafeEnv accepted %v", supplied)
		}
	}
}

func TestTimeoutIsClamped(t *testing.T) {
	if got := Timeout(0); got != DefaultTimeout {
		t.Errorf("Timeout(0) = %s, want the default", got)
	}
	if got := Timeout(-5); got != DefaultTimeout {
		t.Errorf("Timeout(-5) = %s, want the default", got)
	}
	if got := Timeout(int((2 * MaxTimeout) / time.Millisecond)); got != MaxTimeout {
		t.Errorf("Timeout(huge) = %s, want the maximum", got)
	}
	if got := Timeout(1500); got != 1500*time.Millisecond {
		t.Errorf("Timeout(1500) = %s", got)
	}
}

func TestTruncateBoundsOutput(t *testing.T) {
	short := "all good"
	if got := Truncate(short); got != short {
		t.Errorf("Truncate rewrote short output: %q", got)
	}

	long := strings.Repeat("x", MaxOutputBytes*2)
	got := Truncate(long)
	if len(got) > MaxOutputBytes+64 {
		t.Errorf("Truncate returned %d bytes, want it bounded", len(got))
	}
}

func TestExecuteAnswersWithoutTouchingTheSystem(t *testing.T) {
	executor := &Executor{AgentID: "agt-1", Run: func(context.Context, []string, []string) (string, int, error) {
		t.Fatal("agent.ping must not spawn a process")
		return "", 0, nil
	}}

	result := executor.Execute(context.Background(), transport.Task{ID: "t1", AgentID: "agt-1", Action: "agent.ping"})
	if result.Status != transport.TaskStatusSucceeded || result.Output != "pong" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecuteReportsRejectionInsteadOfDropping(t *testing.T) {
	executor := &Executor{AgentID: "agt-1", Run: func(context.Context, []string, []string) (string, int, error) {
		t.Fatal("a rejected task must never reach the runner")
		return "", 0, nil
	}}

	result := executor.Execute(context.Background(), transport.Task{ID: "t1", AgentID: "intruder", Action: "agent.ping"})
	if result.Status != transport.TaskStatusRejected {
		t.Fatalf("status = %q, want rejected", result.Status)
	}
	if result.TaskID != "t1" || result.ExitCode != -1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecuteBuildsAnArgvSliceNotAShellString(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the service argv is platform specific")
	}

	recorded := []string{}
	executor := &Executor{AgentID: "agt-1", Run: stubRunner(&recorded)}

	result := executor.Execute(context.Background(), transport.Task{
		ID:      "t1",
		AgentID: "agt-1",
		Action:  "service.status",
		Args:    []string{"nginx"},
	})
	if result.Status != transport.TaskStatusSucceeded {
		t.Fatalf("result = %+v", result)
	}

	if len(recorded) < 2 {
		t.Fatalf("argv = %v, want a multi-element slice", recorded)
	}
	for _, arg := range recorded {
		if strings.ContainsAny(arg, ";|&$`") {
			t.Fatalf("argv element %q contains shell metacharacters", arg)
		}
	}
	if strings.Contains(recorded[0], "sh") {
		t.Fatalf("argv[0] = %q, want a direct binary, never a shell", recorded[0])
	}
}

func TestLookupOnlyReturnsAllowlistedActions(t *testing.T) {
	if _, ok := Lookup("agent.ping"); !ok {
		t.Error("agent.ping should be allowlisted")
	}
	if _, ok := Lookup("agent.PING"); ok {
		t.Error("Lookup must be case sensitive")
	}
	if _, ok := Lookup("shell.exec"); ok {
		t.Error("shell.exec must never be allowlisted")
	}
}
