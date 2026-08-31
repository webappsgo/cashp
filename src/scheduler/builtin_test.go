package scheduler

import (
	"testing"
	"time"
)

func TestBuiltinsCoverEveryRequiredTask(t *testing.T) {
	want := map[string]string{
		TaskSSLRenewal:       "0 3 * * *",
		TaskGeoIPUpdate:      "0 3 * * 0",
		TaskBlocklistUpdate:  "0 4 * * *",
		TaskCVEUpdate:        "0 5 * * *",
		TaskUpdateCheck:      "0 6 * * *",
		TaskSessionCleanup:   "@every 15m",
		TaskTokenCleanup:     "@every 15m",
		TaskLogRotation:      "0 0 * * *",
		TaskBackupDaily:      "0 2 * * *",
		TaskBackupHourly:     "@hourly",
		TaskHealthcheckSelf:  "@every 5m",
		TaskTorHealth:        "@every 10m",
		TaskI2PHealth:        "@every 10m",
		TaskClusterHeartbeat: "@every 30s",
	}
	specs := Builtins()
	if len(specs) != len(want) {
		t.Fatalf("Builtins() has %d entries, want %d", len(specs), len(want))
	}
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		schedule, ok := want[spec.Name]
		if !ok {
			t.Errorf("unexpected built-in %q", spec.Name)
			continue
		}
		if spec.Schedule != schedule {
			t.Errorf("%s schedule = %q, want %q", spec.Name, spec.Schedule, schedule)
		}
		if spec.Title == "" || spec.Description == "" {
			t.Errorf("%s must have a title and description", spec.Name)
		}
		if _, err := ParseSchedule(spec.Schedule, time.UTC); err != nil {
			t.Errorf("%s schedule does not parse: %v", spec.Name, err)
		}
		seen[spec.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("built-in %q is missing", name)
		}
	}
}

func TestBuiltinsClusterClassification(t *testing.T) {
	global := map[string]bool{
		TaskSSLRenewal:      true,
		TaskGeoIPUpdate:     true,
		TaskBlocklistUpdate: true,
		TaskCVEUpdate:       true,
		TaskBackupDaily:     true,
		TaskBackupHourly:    true,
		TaskUpdateCheck:     true,
	}
	for _, spec := range Builtins() {
		if spec.ClusterWide != global[spec.Name] {
			t.Errorf("%s ClusterWide = %t, want %t", spec.Name, spec.ClusterWide, global[spec.Name])
		}
	}
}

func TestBuiltinsRequiredSet(t *testing.T) {
	required := map[string]bool{
		TaskSSLRenewal:      true,
		TaskSessionCleanup:  true,
		TaskTokenCleanup:    true,
		TaskLogRotation:     true,
		TaskHealthcheckSelf: true,
	}
	conditional := map[string]bool{
		TaskTorHealth:        true,
		TaskI2PHealth:        true,
		TaskClusterHeartbeat: true,
	}
	for _, spec := range Builtins() {
		if spec.Required != required[spec.Name] {
			t.Errorf("%s Required = %t, want %t", spec.Name, spec.Required, required[spec.Name])
		}
		if conditional[spec.Name] && spec.Conditional == "" {
			t.Errorf("%s must document its runtime condition", spec.Name)
		}
	}
}

func TestBuiltinsDefaultEnabled(t *testing.T) {
	for _, spec := range Builtins() {
		if spec.Name == TaskBackupHourly {
			if spec.DefaultEnabled {
				t.Error("backup_hourly must be disabled by default")
			}
			continue
		}
		if !spec.DefaultEnabled {
			t.Errorf("%s must be enabled by default", spec.Name)
		}
	}
}

func TestBuiltinsRetrySettings(t *testing.T) {
	for _, spec := range Builtins() {
		switch spec.Name {
		case TaskBlocklistUpdate, TaskCVEUpdate:
			if !spec.RetryOnFail || spec.RetryDelay != time.Hour {
				t.Errorf("%s must retry after 1h, got retry=%t delay=%s", spec.Name, spec.RetryOnFail, spec.RetryDelay)
			}
		default:
			if spec.RetryOnFail {
				t.Errorf("%s must not set retry_on_fail", spec.Name)
			}
		}
	}
}

func TestBuiltinLookup(t *testing.T) {
	spec, ok := Builtin(TaskLogRotation)
	if !ok || spec.Schedule != "0 0 * * *" {
		t.Fatalf("Builtin(log_rotation) = %+v, %t", spec, ok)
	}
	if _, ok := Builtin("not_a_task"); ok {
		t.Error("unknown built-in must not be found")
	}
}

func TestBuiltinsReturnsCopy(t *testing.T) {
	first := Builtins()
	first[0].Schedule = "mutated"
	if Builtins()[0].Schedule == "mutated" {
		t.Error("Builtins() must return a copy of the registry")
	}
}

func TestBuiltinSpecTaskConversion(t *testing.T) {
	spec, ok := Builtin(TaskBackupHourly)
	if !ok {
		t.Fatal("backup_hourly missing")
	}
	task := spec.task()
	if task.Name != TaskBackupHourly || !task.Disabled || !task.ClusterWide {
		t.Errorf("task conversion wrong: %+v", task)
	}
	if task.Run != nil {
		t.Error("built-in tasks must start without an implementation")
	}
	if task.MaxRetries != DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", task.MaxRetries, DefaultMaxRetries)
	}
}
