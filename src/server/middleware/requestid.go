package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/webappsgo/cashp/src/api"
)

// RequestIDHeader is the header the request identifier travels in, both
// inbound from a trusted proxy and outbound on every response.
const RequestIDHeader = "X-Request-ID"

// maxRequestIDLen bounds an inbound identifier so a client cannot inflate
// every log record it triggers.
const maxRequestIDLen = 64

// RequestID assigns each request a stable identifier, reusing a well-formed
// inbound value so a request can be followed across a proxy hop, and echoes
// it on the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(RequestIDHeader))
		if id == "" {
			id = newRequestID()
		}
		r.Header.Set(RequestIDHeader, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(api.WithRequestID(r.Context(), id)))
	})
}

// sanitizeRequestID accepts an inbound identifier only when it is short and
// made of characters that cannot forge log structure.
func sanitizeRequestID(id string) string {
	if id == "" || len(id) > maxRequestIDLen {
		return ""
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_':
		default:
			return ""
		}
	}
	return id
}

// idFallback keeps identifiers unique if the system entropy source ever
// fails, so log records never collapse onto one shared identifier.
var idFallback atomic.Uint64

// newRequestID generates a random 128-bit identifier.
func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "fallback-" + strconv.FormatUint(idFallback.Add(1), 16)
	}
	return hex.EncodeToString(buf[:])
}
