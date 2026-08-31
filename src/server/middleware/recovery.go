package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/webappsgo/cashp/src/api"
	apperr "github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/logging"
)

// Recovery converts a panic into the canonical 500 error envelope. The panic
// value and its stack trace are written to the server log only — never to
// the response, in any mode — so a crash cannot disclose internals. The
// client receives the request identifier, which is what support needs to
// find the matching log record.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newRecorder(w)
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			logging.L().Log(context.WithoutCancel(r.Context()), slog.LevelError, "panic recovered",
				slog.String("request_id", api.RequestIDFrom(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("panic", fmt.Sprint(v)),
				slog.String("stack", string(debug.Stack())),
			)
			if rec.wrote {
				return
			}
			api.WriteError(rec, r, apperr.New(apperr.CodeInternal, http.StatusInternalServerError,
				apperr.DefaultMessage(apperr.CodeInternal)))
		}()
		next.ServeHTTP(rec, r)
	})
}
