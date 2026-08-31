package guard

import (
	"errors"
	"testing"
	"time"

	apperr "github.com/webappsgo/cashp/src/errors"
)

func TestCheckNameAvailabilityNeverRevealsAccountExistence(t *testing.T) {
	blocked := func(name string) bool { return name == "admin" }
	taken := func(name string) (bool, error) { return name == "tenant-one", nil }

	cases := []struct {
		name string
		want NameStatus
	}{
		{"admin", StatusReserved},
		{"tenant-one", StatusUnavailable},
		{"tenant-two", StatusAvailable},
		{"ab", StatusUnavailable},
		{"bad;name", StatusUnavailable},
		{"", StatusUnavailable},
	}
	for _, tc := range cases {
		got, err := CheckNameAvailability(tc.name, blocked, taken)
		if err != nil {
			t.Fatalf("CheckNameAvailability(%q) returned an error: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("CheckNameAvailability(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}

	// A taken name and a malformed name must be indistinguishable, which is
	// the whole point of collapsing both onto StatusUnavailable.
	takenAnswer, _ := CheckNameAvailability("tenant-one", blocked, taken)
	malformedAnswer, _ := CheckNameAvailability("bad;name", blocked, taken)
	if takenAnswer != malformedAnswer {
		t.Fatal("a registered name is distinguishable from a malformed one")
	}
}

func TestCheckNameAvailabilityFailsClosedOnStorageError(t *testing.T) {
	boom := errors.New("database unreachable")
	status, err := CheckNameAvailability("tenant-two", nil, func(string) (bool, error) { return false, boom })
	if status == StatusAvailable {
		t.Fatal("a storage failure answered available")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("the storage error was swallowed: %v", err)
	}

	// A missing lookup must also fail closed rather than assume the name is
	// free.
	if status, _ := CheckNameAvailability("tenant-two", nil, nil); status != StatusUnavailable {
		t.Fatalf("a nil lookup answered %q", status)
	}
}

func TestPacerPadsEveryOutcomeToTheSameFloor(t *testing.T) {
	var slept []time.Duration
	base := time.Unix(1700000000, 0)
	current := base
	pacer := Pacer{
		Now:   func() time.Time { return current },
		Sleep: func(d time.Duration) { slept = append(slept, d) },
	}

	// A fast rejection (unknown identifier) must be padded up to the floor.
	start := pacer.Start()
	current = base.Add(5 * time.Millisecond)
	pacer.PadAuth(start)
	if len(slept) != 1 || slept[0] != MinAuthResponseTime-5*time.Millisecond {
		t.Fatalf("a fast rejection was not padded to the floor: %v", slept)
	}

	// A slow rejection (full Argon2id verification) must not sleep at all,
	// and must never sleep a negative duration.
	slept = nil
	start = pacer.Start()
	current = current.Add(MinAuthResponseTime + time.Second)
	pacer.PadAuth(start)
	if len(slept) != 0 {
		t.Fatalf("an already-slow response slept anyway: %v", slept)
	}

	// A non-positive floor disables padding rather than blocking forever.
	pacer.PadTo(pacer.Start(), 0)
	if len(slept) != 0 {
		t.Fatalf("a zero floor produced a sleep: %v", slept)
	}
}

func TestVerifyOrDecoyDoesTheWorkForAnUnknownAccount(t *testing.T) {
	ok, rehash := VerifyOrDecoy("", "any-password")
	if ok || rehash {
		t.Fatal("VerifyOrDecoy authenticated against an absent hash")
	}
	if decoy() == "" {
		t.Fatal("the decoy hash was never built, so an unknown account is cheap to detect")
	}

	// A malformed stored hash must fail closed rather than error into a
	// success path.
	if ok, _ := VerifyOrDecoy("not-a-valid-argon2id-encoding", "any-password"); ok {
		t.Fatal("VerifyOrDecoy accepted a malformed stored hash")
	}
}

func TestUniformDenialsCarryGenericCodes(t *testing.T) {
	notFound := UniformNotFound("site", "s9")
	if code := AppErrorFor(notFound).Code; code != apperr.CodeNotFound {
		t.Fatalf("UniformNotFound produced code %q", code)
	}
	if msg := AppErrorFor(notFound).Message; msg != apperr.DefaultMessage(apperr.CodeNotFound) {
		t.Fatalf("UniformNotFound leaked a specific message: %q", msg)
	}

	authFailure := UniformAuthFailure("password mismatch for user u1")
	rendered := AppErrorFor(authFailure).Message
	if rendered != apperr.DefaultMessage(apperr.CodeUnauthorized) {
		t.Fatalf("UniformAuthFailure leaked the cause: %q", rendered)
	}
}
