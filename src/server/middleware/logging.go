package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/webappsgo/cashp/src/api"
	"github.com/webappsgo/cashp/src/logging"
)

// Logging emits one structured record per request. Every record carries the
// request identifier, the HTTP status, and the machine-readable error code
// when the response failed, so a support request can be traced from a single
// identifier. Levels follow AI.md PART 11: ERROR at 500 and above, WARN at
// 400 and above, INFO otherwise.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := api.WithErrorRecorder(r.Context())
		r = r.WithContext(ctx)
		rec := newRecorder(w)

		next.ServeHTTP(rec, r)

		attrs := []any{
			slog.String("request_id", api.RequestIDFrom(ctx)),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("http_status", rec.status),
			slog.Int64("bytes", rec.written),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("client_ip", ClientIPFrom(ctx)),
		}
		if code := api.RecordedErrorCode(ctx); code != "" {
			attrs = append(attrs, slog.String("error_code", code))
		}
		logging.L().Log(context.WithoutCancel(ctx), levelFor(rec.status), "http request", attrs...)
	})
}

// levelFor maps an HTTP status to the log level it is recorded at.
func levelFor(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
