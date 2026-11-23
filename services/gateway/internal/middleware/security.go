package middleware

import (
	"net/http"

	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

const defaultAPICSP = "default-src 'none'; frame-ancestors 'none'"

type securityHeadersWriter struct {
	http.ResponseWriter
	applied     bool
	wroteHeader bool
}

func (w *securityHeadersWriter) apply() {
	if w.applied {
		return
	}
	w.applied = true
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	if csp := h.Get("Content-Security-Policy"); csp != "" {
		h.Set("Content-Security-Policy", csp)
	} else {
		h.Set("Content-Security-Policy", defaultAPICSP)
	}
}

func (w *securityHeadersWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.apply()
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *securityHeadersWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *securityHeadersWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *securityHeadersWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// SecurityHeaders adds baseline HTTP security headers suitable for API gateway responses.
// Headers are finalized right before WriteHeader so reverse-proxied responses never emit
// duplicate security headers when httputil.ReverseProxy copies upstream headers via Add.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &securityHeadersWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if !sw.wroteHeader {
				sw.apply()
			}
		})
	}
}

// MaxBodyBytes rejects requests whose Content-Length exceeds limit with 413 PAYLOAD_TOO_LARGE
// and wraps r.Body with http.MaxBytesReader so chunked/unknown-length bodies also fail with 413.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limit <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
