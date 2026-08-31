package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

// fastSchedule keeps retry tests instant while still exercising every attempt.
var fastSchedule = []time.Duration{0, time.Millisecond, time.Millisecond}

func TestRetrySucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := RetryWith(context.Background(), fastSchedule, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryRecoversFromTransientFailure(t *testing.T) {
	calls := 0
	err := RetryWith(context.Background(), fastSchedule, func(context.Context) error {
		calls++
		if calls < 3 {
			return New(CodeUnavailable, 0, "")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryStopsOnNonRetryable(t *testing.T) {
	calls := 0
	want := New(CodeValidation, 0, "")
	err := RetryWith(context.Background(), fastSchedule, func(context.Context) error {
		calls++
		return want
	})
	if !stderrors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryReturnsLastErrorWhenExhausted(t *testing.T) {
	calls := 0
	err := RetryWith(context.Background(), fastSchedule, func(context.Context) error {
		calls++
		return fmt.Errorf("attempt %d: %w", calls, syscall.ECONNREFUSED)
	})
	if calls != len(fastSchedule) {
		t.Fatalf("calls = %d, want %d", calls, len(fastSchedule))
	}
	if err == nil || !IsRetryable(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := RetryWith(ctx, []time.Duration{0, time.Minute}, func(context.Context) error {
		calls++
		cancel()
		return New(CodeUnavailable, 0, "")
	})
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryWithEmptyScheduleRunsOnce(t *testing.T) {
	calls := 0
	err := RetryWith(context.Background(), nil, func(context.Context) error {
		calls++
		return New(CodeUnavailable, 0, "")
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if err == nil {
		t.Fatal("err should be the last failure")
	}
}

func TestRetryUsesDefaultSchedule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Retry(ctx, func(context.Context) error { return nil }); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(DefaultBackoff) != 5 || DefaultBackoff[0] != 0 || DefaultBackoff[4] != 8*time.Second {
		t.Fatalf("unexpected default schedule: %v", DefaultBackoff)
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, false},
		{"conn refused", syscall.ECONNREFUSED, true},
		{"conn reset", syscall.ECONNRESET, true},
		{"broken pipe", syscall.EPIPE, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"wrapped conn refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{"net timeout", &net.DNSError{IsTimeout: true}, true},
		{"unavailable", New(CodeUnavailable, 0, ""), true},
		{"rate limited", New(CodeRateLimited, 0, ""), true},
		{"timeout code", New(CodeTimeout, 0, ""), true},
		{"not found", New(CodeNotFound, 0, ""), false},
		{"validation", New(CodeValidation, 0, ""), false},
		{"plain", stderrors.New("nope"), false},
	}
	for _, tc := range cases {
		if got := IsRetryable(tc.err); got != tc.want {
			t.Errorf("%s: IsRetryable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRetryCapsWaitAtMaxBackoff(t *testing.T) {
	if MaxBackoff != 30*time.Second {
		t.Fatalf("MaxBackoff = %v", MaxBackoff)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RetryWith(ctx, []time.Duration{0, time.Hour}, func(context.Context) error {
		return New(CodeUnavailable, 0, "")
	})
	if !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > MaxBackoff {
		t.Fatalf("waited %v", elapsed)
	}
}
