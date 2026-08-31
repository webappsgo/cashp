package hostpkg

import (
	"errors"
	"strings"
	"testing"
)

// newManagerFor builds a manager plus its fake runner for a distribution.
func newManagerFor(t *testing.T, lines ...string) (Manager, *FakeRunner) {
	t.Helper()

	d := mustDistro(t, lines...)
	runner := NewFakeRunner()
	m, err := NewManager(d, runner)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	return m, runner
}

// notInstalled makes every installed-state query answer "not installed".
func notInstalled(runner *FakeRunner) {
	runner.Handler = func(cmd Command) (Result, error) {
		switch {
		case cmd.Name == "dpkg-query":
			return Result{}, errors.New("not installed")
		case cmd.Name == "rpm" && len(cmd.Args) > 0 && cmd.Args[0] == "--query":
			return Result{}, errors.New("not installed")
		case cmd.Name == "apk" && len(cmd.Args) > 0 && cmd.Args[0] == "info":
			return Result{}, errors.New("not installed")
		case cmd.Name == "pacman" && len(cmd.Args) > 0 && cmd.Args[0] == "-Q":
			return Result{}, errors.New("not installed")
		default:
			return Result{}, nil
		}
	}
}

// hasLine reports whether the runner recorded an exact argv line.
func hasLine(runner *FakeRunner, want string) bool {
	for _, line := range runner.Lines() {
		if line == want {
			return true
		}
	}

	return false
}

func TestManagerKindPerDistro(t *testing.T) {
	cases := []struct {
		lines []string
		want  ManagerKind
	}{
		{[]string{`ID=debian`, `VERSION_ID="12"`}, ManagerAPT},
		{[]string{`ID=alpine`, `VERSION_ID=3.20.3`}, ManagerAPK},
		{[]string{`ID=fedora`, `VERSION_ID=42`}, ManagerDNF},
		{[]string{`ID=arch`}, ManagerPacman},
	}

	for _, tc := range cases {
		m, _ := newManagerFor(t, tc.lines...)
		if m.Kind() != tc.want {
			t.Errorf("Kind() = %q, want %q", m.Kind(), tc.want)
		}
	}
}

func TestNewManagerRejectsNilDistro(t *testing.T) {
	if _, err := NewManager(nil, NewFakeRunner()); !errors.Is(err, ErrUnsupportedDistro) {
		t.Fatalf("error = %v, want ErrUnsupportedDistro", err)
	}
}

func TestAPTArgvConstruction(t *testing.T) {
	m, runner := newManagerFor(t, `ID=debian`, `VERSION_ID="12"`)
	notInstalled(runner)
	ctx := t.Context()

	if _, err := m.Install(ctx, "nginx", "redis"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !hasLine(runner, "apt-get install -y --no-install-recommends nginx redis") {
		t.Fatalf("install argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Remove(ctx, "nginx"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !hasLine(runner, "apt-get remove -y nginx") {
		t.Fatalf("remove argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Upgrade(ctx, "nginx"); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !hasLine(runner, "apt-get install -y --only-upgrade nginx") {
		t.Fatalf("upgrade argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !hasLine(runner, "apt-get update -qq") {
		t.Fatalf("refresh argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Hold(ctx, "nginx"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := m.Unhold(ctx, "nginx"); err != nil {
		t.Fatalf("Unhold: %v", err)
	}
	if !hasLine(runner, "apt-mark hold nginx") || !hasLine(runner, "apt-mark unhold nginx") {
		t.Fatalf("hold argv missing: %v", runner.Lines())
	}
	if !m.SupportsHold() {
		t.Error("apt should support hold")
	}
}

func TestAPTNonInteractiveEnvironment(t *testing.T) {
	m, runner := newManagerFor(t, `ID=ubuntu`, `VERSION_ID="24.04"`)
	notInstalled(runner)

	if _, err := m.Install(t.Context(), "nginx"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	var env []string
	for _, call := range runner.Calls {
		if CommandLine(call) == "apt-get install -y --no-install-recommends nginx" {
			env = call.Env
		}
	}
	joined := strings.Join(env, " ")
	for _, want := range []string{"LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env %q missing %q", joined, want)
		}
	}
}

func TestAPKArgvConstruction(t *testing.T) {
	m, runner := newManagerFor(t, `ID=alpine`, `VERSION_ID=3.20.3`)
	notInstalled(runner)
	ctx := t.Context()

	if _, err := m.Install(ctx, "nginx"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !hasLine(runner, "apk add --no-cache --no-interactive nginx") {
		t.Fatalf("install argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Remove(ctx, "nginx"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !hasLine(runner, "apk del --no-cache --no-interactive nginx") {
		t.Fatalf("remove argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !hasLine(runner, "apk update --no-interactive") {
		t.Fatalf("refresh argv missing: %v", runner.Lines())
	}

	if m.SupportsHold() {
		t.Error("apk should not support hold")
	}
	if err := m.Hold(ctx, "nginx"); !errors.Is(err, ErrHoldUnsupported) {
		t.Errorf("Hold error = %v, want ErrHoldUnsupported", err)
	}
	if err := m.Unhold(ctx, "nginx"); !errors.Is(err, ErrHoldUnsupported) {
		t.Errorf("Unhold error = %v, want ErrHoldUnsupported", err)
	}
}

func TestDNFArgvConstruction(t *testing.T) {
	m, runner := newManagerFor(t, `ID=fedora`, `VERSION_ID=42`)
	notInstalled(runner)
	ctx := t.Context()

	if _, err := m.Install(ctx, "nginx"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !hasLine(runner, "dnf --assumeyes --setopt=assumeyes=1 install nginx") {
		t.Fatalf("install argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Remove(ctx, "nginx"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !hasLine(runner, "dnf --assumeyes --setopt=assumeyes=1 remove nginx") {
		t.Fatalf("remove argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !hasLine(runner, "dnf --assumeyes --setopt=assumeyes=1 makecache") {
		t.Fatalf("refresh argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Hold(ctx, "nginx"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if !hasLine(runner, "dnf --assumeyes --setopt=assumeyes=1 versionlock add nginx") {
		t.Fatalf("hold argv missing: %v", runner.Lines())
	}
}

func TestPacmanArgvConstruction(t *testing.T) {
	m, runner := newManagerFor(t, `ID=arch`)
	notInstalled(runner)
	ctx := t.Context()

	if _, err := m.Install(ctx, "nginx"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !hasLine(runner, "pacman -S --needed --noconfirm nginx") {
		t.Fatalf("install argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Remove(ctx, "nginx"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !hasLine(runner, "pacman -R --noconfirm nginx") {
		t.Fatalf("remove argv missing: %v", runner.Lines())
	}

	runner.Reset()
	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !hasLine(runner, "pacman -Sy --noconfirm") {
		t.Fatalf("refresh argv missing: %v", runner.Lines())
	}

	if err := m.Hold(ctx, "nginx"); !errors.Is(err, ErrHoldUnsupported) {
		t.Errorf("Hold error = %v, want ErrHoldUnsupported", err)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	m, runner := newManagerFor(t, `ID=debian`, `VERSION_ID="12"`)
	runner.Handler = func(cmd Command) (Result, error) {
		if cmd.Name == "dpkg-query" {
			if cmd.Args[len(cmd.Args)-1] == "nginx" {
				return Result{Stdout: "installed\n"}, nil
			}
			return Result{}, errors.New("not installed")
		}

		return Result{}, nil
	}

	res, err := m.Install(t.Context(), "nginx", "redis", "nginx")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if strings.Join(res.Requested, ",") != "nginx,redis" {
		t.Errorf("Requested = %v, want [nginx redis]", res.Requested)
	}
	if strings.Join(res.AlreadyPresent, ",") != "nginx" {
		t.Errorf("AlreadyPresent = %v, want [nginx]", res.AlreadyPresent)
	}
	if strings.Join(res.Installed, ",") != "redis" {
		t.Errorf("Installed = %v, want [redis]", res.Installed)
	}
	if !res.Changed {
		t.Error("Changed should be true when a package was installed")
	}
	if !hasLine(runner, "apt-get install -y --no-install-recommends redis") {
		t.Fatalf("only the missing package should be installed: %v", runner.Lines())
	}

	// A second run with everything present must run no transaction at all.
	runner.Reset()
	runner.Handler = func(cmd Command) (Result, error) {
		if cmd.Name == "dpkg-query" {
			return Result{Stdout: "installed\n"}, nil
		}

		return Result{}, nil
	}
	res, err = m.Install(t.Context(), "nginx", "redis")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Changed || len(res.Installed) != 0 {
		t.Errorf("second install changed the host: %+v", res)
	}
	for _, line := range runner.Lines() {
		if strings.HasPrefix(line, "apt-get install") {
			t.Fatalf("an install transaction ran for an already-installed set: %v", runner.Lines())
		}
	}
}

func TestInstallRejectsInvalidPackageBeforeAnyCommand(t *testing.T) {
	m, runner := newManagerFor(t, `ID=debian`, `VERSION_ID="12"`)

	if _, err := m.Install(t.Context(), "nginx; rm -rf /"); !errors.Is(err, ErrInvalidPackageName) {
		t.Fatalf("error = %v, want ErrInvalidPackageName", err)
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("a command ran despite an invalid package name: %v", runner.Lines())
	}

	if _, err := m.Install(t.Context()); !errors.Is(err, ErrNoPackages) {
		t.Fatalf("error = %v, want ErrNoPackages", err)
	}
}

func TestQueryParsers(t *testing.T) {
	apt, aptRunner := newManagerFor(t, `ID=debian`, `VERSION_ID="12"`)
	aptRunner.SetResponse("apt-cache policy nginx", Result{Stdout: "nginx:\n  Installed: (none)\n  Candidate: 1.22.1-9\n"})
	version, err := apt.AvailableVersion(t.Context(), "nginx")
	if err != nil || version != "1.22.1-9" {
		t.Fatalf("apt AvailableVersion = %q, %v", version, err)
	}
	aptRunner.SetResponse("apt-cache policy ghost", Result{Stdout: "ghost:\n  Candidate: (none)\n"})
	if version, err = apt.AvailableVersion(t.Context(), "ghost"); err != nil || version != "" {
		t.Fatalf("apt AvailableVersion(ghost) = %q, %v", version, err)
	}
	aptRunner.SetResponse("dpkg-query -W -f=${db:Status-Status} nginx", Result{Stdout: "installed\n"})
	present, err := apt.IsInstalled(t.Context(), "nginx")
	if err != nil || !present {
		t.Fatalf("apt IsInstalled = %v, %v", present, err)
	}

	apk, apkRunner := newManagerFor(t, `ID=alpine`, `VERSION_ID=3.20.3`)
	apkRunner.SetResponse("apk policy nginx", Result{Stdout: "nginx policy:\n  1.26.2-r0:\n    lib/apk/db/installed\n"})
	if version, err = apk.AvailableVersion(t.Context(), "nginx"); err != nil || version != "1.26.2-r0" {
		t.Fatalf("apk AvailableVersion = %q, %v", version, err)
	}

	dnf, dnfRunner := newManagerFor(t, `ID=fedora`, `VERSION_ID=42`)
	dnfRunner.SetResponse("dnf --quiet repoquery --queryformat=%{version}-%{release} --latest-limit=1 nginx",
		Result{Stdout: "\n1.26.2-2.fc42\n"})
	if version, err = dnf.AvailableVersion(t.Context(), "nginx"); err != nil || version != "1.26.2-2.fc42" {
		t.Fatalf("dnf AvailableVersion = %q, %v", version, err)
	}

	pac, pacRunner := newManagerFor(t, `ID=arch`)
	pacRunner.SetResponse("pacman -Si nginx", Result{Stdout: "Repository : extra\nName : nginx\nVersion : 1.27.2-1\n"})
	if version, err = pac.AvailableVersion(t.Context(), "nginx"); err != nil || version != "1.27.2-1" {
		t.Fatalf("pacman AvailableVersion = %q, %v", version, err)
	}
}

func TestQueriesRejectInvalidNames(t *testing.T) {
	m, runner := newManagerFor(t, `ID=debian`, `VERSION_ID="12"`)

	if _, err := m.IsInstalled(t.Context(), "nginx redis"); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("IsInstalled error = %v, want ErrInvalidPackageName", err)
	}
	if _, err := m.AvailableVersion(t.Context(), "../nginx"); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("AvailableVersion error = %v, want ErrInvalidPackageName", err)
	}
	if err := m.Hold(t.Context(), "nginx&&id"); !errors.Is(err, ErrInvalidPackageName) {
		t.Errorf("Hold error = %v, want ErrInvalidPackageName", err)
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("a command ran for an invalid package name: %v", runner.Lines())
	}
}

func TestFirstFieldAfter(t *testing.T) {
	if got := firstFieldAfter("a:\n  Candidate: 1.0\n", "Candidate:"); got != "1.0" {
		t.Fatalf("firstFieldAfter = %q, want 1.0", got)
	}
	if got := firstFieldAfter("nothing here\n", "Candidate:"); got != "" {
		t.Fatalf("firstFieldAfter = %q, want empty", got)
	}
}
