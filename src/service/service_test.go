package service

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusStringRunning(t *testing.T) {
	status := Status{Manager: "systemd", Installed: true, State: StateRunning, Enabled: true, PID: 1234}
	got := status.String()
	want := "  Service:    installed\n  State:      running\n  Auto-start: enabled\n  PID:        1234\n"
	if got != want {
		t.Errorf("Status.String() =\n%q\nwant\n%q", got, want)
	}
}

func TestStatusStringNotInstalled(t *testing.T) {
	got := Status{Manager: "systemd", State: StateStopped}.String()
	want := "  Service:    not installed\n  State:      stopped\n  Auto-start: disabled\n"
	if got != want {
		t.Errorf("Status.String() =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "PID:") {
		t.Error("a stopped service must not report a PID line")
	}
}

func TestConfirmDestructive(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{"lowercase y", "y\n", true},
		{"yes", "yes\n", true},
		{"uppercase Y", "Y\n", true},
		{"padded yes", "  YES  \n", true},
		{"explicit no", "n\n", false},
		{"empty default", "\n", false},
		{"eof", "", false},
		{"unrelated input", "maybe\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			ok, err := confirmDestructive(strings.NewReader(tc.answer), &out, confirmPrompt)
			if err != nil {
				t.Fatalf("confirmDestructive: %v", err)
			}
			if ok != tc.want {
				t.Errorf("confirmDestructive(%q) = %v, want %v", tc.answer, ok, tc.want)
			}
			if out.String() != confirmPrompt {
				t.Errorf("prompt = %q, want %q", out.String(), confirmPrompt)
			}
		})
	}
}

func TestConfirmPromptMatchesSpec(t *testing.T) {
	want := "This will delete ALL data, configs, and the system user. Continue? [y/N] "
	if confirmPrompt != want {
		t.Errorf("confirmPrompt = %q, want the AI.md PART 24 wording %q", confirmPrompt, want)
	}
}

func TestUninstallNoticeKeepsBinary(t *testing.T) {
	got := uninstallNotice("/usr/local/bin/cashp")
	want := "Service uninstalled. Delete binary manually: rm /usr/local/bin/cashp"
	if got != want {
		t.Errorf("uninstallNotice = %q, want %q", got, want)
	}
}

func TestTemplateDataStateDirsOrder(t *testing.T) {
	d := testData(false)
	dirs := d.StateDirs()
	want := []string{d.CacheDir, d.LogDir, d.DataDir, d.BackupDir, d.ConfigDir}
	if len(dirs) != len(want) {
		t.Fatalf("StateDirs() returned %d entries, want %d", len(dirs), len(want))
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("StateDirs()[%d] = %q, want %q", i, dirs[i], want[i])
		}
	}
}

func TestDefaultTemplateDataIdentity(t *testing.T) {
	system := DefaultTemplateData(false)
	if system.Name != "cashp" || system.Org != "webappsgo" {
		t.Errorf("identity = %q/%q, want cashp/webappsgo", system.Name, system.Org)
	}
	if system.PlistName != "com.webappsgo.cashp" {
		t.Errorf("PlistName = %q, want com.webappsgo.cashp", system.PlistName)
	}
	if system.DocumentationURL != "https://webappsgo.github.io/cashp" {
		t.Errorf("DocumentationURL = %q", system.DocumentationURL)
	}
	if system.UserMode {
		t.Error("DefaultTemplateData(false) must not be a user-mode definition")
	}
	if filepath.Base(system.PIDFile) != "cashp.pid" {
		t.Errorf("PIDFile = %q, want a cashp.pid file", system.PIDFile)
	}
	user := DefaultTemplateData(true)
	if !user.UserMode {
		t.Error("DefaultTemplateData(true) must be a user-mode definition")
	}
	if user.PIDFile != filepath.Join(user.DataDir, "cashp.pid") {
		t.Errorf("user PIDFile = %q, want it inside the user data dir %q", user.PIDFile, user.DataDir)
	}
}

func TestRunDirPlacement(t *testing.T) {
	if got := runDir(true, "/home/alice/.local/share/cashp"); got != "/home/alice/.local/share/cashp" {
		t.Errorf("user runDir = %q, want the data dir", got)
	}
	got := runDir(false, "/var/lib/webappsgo/cashp")
	if got != "/var/run/webappsgo" && got != "/var/lib/webappsgo/cashp" {
		t.Errorf("system runDir = %q, want /var/run/webappsgo on Unix", got)
	}
}

func TestWriteAndRemoveServiceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "cashp.service")
	if err := writeServiceFile(path, "unit\n", 0o644); err != nil {
		t.Fatalf("writeServiceFile: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(content) != "unit\n" {
		t.Errorf("content = %q, want %q", content, "unit\n")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	if !fileExists(path) {
		t.Error("fileExists must report a written file as present")
	}
	if err := removeServiceFile(path); err != nil {
		t.Fatalf("removeServiceFile: %v", err)
	}
	if fileExists(path) {
		t.Error("fileExists must report a removed file as absent")
	}
	// Removing an already-missing file keeps uninstall idempotent.
	if err := removeServiceFile(path); err != nil {
		t.Errorf("removeServiceFile on a missing file = %v, want nil", err)
	}
}

func TestPathExistsSeesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "cashp")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if fileExists(link) {
		t.Error("fileExists follows symlinks, so a dangling link must not count as present")
	}
	if !pathExists(link) {
		t.Error("pathExists must report a dangling symlink as present")
	}
}

func TestReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"plain pid", "1234", 1234},
		{"trailing newline", "4321\n", 4321},
		{"padded", "  77  \n", 77},
		{"zero", "0\n", 0},
		{"negative", "-5\n", 0},
		{"garbage", "not-a-pid\n", 0},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".pid")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write pid file: %v", err)
			}
			if got := readPIDFile(path); got != tc.want {
				t.Errorf("readPIDFile(%q) = %d, want %d", tc.content, got, tc.want)
			}
		})
	}
	if got := readPIDFile(filepath.Join(dir, "absent.pid")); got != 0 {
		t.Errorf("readPIDFile on a missing file = %d, want 0", got)
	}
}

func TestRequireElevationNeverPrompts(t *testing.T) {
	err := requireElevation("installing the service")
	if IsElevated() {
		if err != nil {
			t.Fatalf("requireElevation as root = %v, want nil", err)
		}
		return
	}
	if err == nil {
		t.Fatal("requireElevation must fail for an unprivileged caller")
	}
	if !strings.Contains(err.Error(), "installing the service requires root privileges") {
		t.Errorf("error = %q, want it to name the action and the privilege requirement", err)
	}
	ok, reason := CanEscalate()
	if ok {
		if !strings.Contains(err.Error(), "re-run it with sudo, doas or pkexec") {
			t.Errorf("error = %q, want re-run guidance when escalation is possible", err)
		}
		return
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("error = %q, want it to explain why escalation is impossible (%q)", err, reason)
	}
}

func TestConfirmUninstallSkipsPromptWhenConfirmed(t *testing.T) {
	if err := confirmUninstall(true); err != nil {
		t.Errorf("confirmUninstall(true) = %v, want nil", err)
	}
}

func TestManagerInterfaceIsSatisfiedOnThisPlatform(t *testing.T) {
	manager, err := Detect()
	if err != nil {
		// A host without a supported init system is a valid outcome; the
		// contract under test is that Detect never returns a partial manager.
		if manager != nil {
			t.Errorf("Detect returned both a manager and the error %v", err)
		}
		return
	}
	if manager == nil {
		t.Fatal("Detect returned no manager and no error")
	}
	if manager.Name() == "" {
		t.Error("a detected manager must report its init system name")
	}
}
