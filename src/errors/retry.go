package errors

import (
	"context"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// MaxBackoff caps any single wait between retry attempts.
const MaxBackoff = 30 * time.Second

// DefaultBackoff is the exponential schedule from AI.md PART 9: five
// attempts spread over fifteen seconds, the first one immediate.
var DefaultBackoff = []time.Duration{0, 1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// Retry runs fn with the default exponential backoff schedule, retrying
// only transient failures. A non-retryable error is returned immediately.
func Retry(ctx context.Context, fn func(context.Context) error) error {
	return RetryWith(ctx, DefaultBackoff, fn)
}

// RetryWith runs fn once per entry in schedule, waiting the entry's
// duration before the attempt. Waits are capped at MaxBackoff and are
// abandoned as soon as ctx is done. An empty schedule runs fn once.
func RetryWith(ctx context.Context, schedule []time.Duration, fn func(context.Context) error) error {
	if len(schedule) == 0 {
		schedule = []time.Duration{0}
	}

	var lastErr error
	for _, wait := range schedule {
		if wait > 0 {
			if wait > MaxBackoff {
				wait = MaxBackoff
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !IsRetryable(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// IsRetryable reports whether an error is transient and worth retrying.
// Client errors (4xx other than rate limiting) are never retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	if stderrors.Is(err, context.DeadlineExceeded) ||
		stderrors.Is(err, io.ErrUnexpectedEOF) ||
		stderrors.Is(err, syscall.ECONNREFUSED) ||
		stderrors.Is(err, syscall.ECONNRESET) ||
		stderrors.Is(err, syscall.ECONNABORTED) ||
		stderrors.Is(err, syscall.EPIPE) ||
		stderrors.Is(err, syscall.ETIMEDOUT) ||
		stderrors.Is(err, syscall.EHOSTUNREACH) ||
		stderrors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	var netErr net.Error
	if stderrors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var appErr *Error
	if stderrors.As(err, &appErr) {
		switch appErr.HTTPStatus {
		case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadGateway:
			return true
		}
		return false
	}

	return false
}
