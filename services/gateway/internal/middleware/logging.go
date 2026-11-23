package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// flushingStatusWriter wraps http.ResponseWriter while preserving http.Flusher and Unwrap().
type flushingStatusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *flushingStatusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *flushingStatusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *flushingStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *flushingStatusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type requestLogMetaKey struct{}

type requestLogMeta struct {
	userID string
}

func setRequestLogUserID(ctx context.Context, userID string) {
	if userID == "" || ctx == nil {
		return
	}
	if meta, ok := ctx.Value(requestLogMetaKey{}).(*requestLogMeta); ok && meta != nil {
		meta.userID = userID
	}
}

// RequestLogger emits structured JSON request logs with trace_id, client_ip, user_id, and redacted sensitive values.
// It also attaches the resolved client IP to the request context so outgoing gRPC calls carry x-client-ip.
func RequestLogger(log *slog.Logger, tp *TrustedProxies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &flushingStatusWriter{ResponseWriter: w, status: http.StatusOK}
			clientIP := tp.ClientIP(r)
			meta := &requestLogMeta{}
			ctx := context.WithValue(r.Context(), requestLogMetaKey{}, meta)
			ctx = httpx.WithLogger(ctx, log)
			ctx = clients.WithClientIP(ctx, clientIP)
			req := r.WithContext(ctx)

			next.ServeHTTP(sw, req)

			if log == nil {
				return
			}
			route := req.URL.Path
			if rctx := chi.RouteContext(req.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					route = pat
				}
			}

			attrs := []slog.Attr{
				slog.String("method", req.Method),
				slog.String("path", req.URL.Path),
				slog.String("route", route),
				slog.Int("status", sw.status),
				slog.Int("bytes", sw.bytes),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000.0),
				slog.String("client_ip", clientIP),
			}
			uid := meta.userID
			if uid == "" {
				uid = auth.UserIDFromContext(req.Context())
			}
			if uid != "" {
				attrs = append(attrs, slog.String("user_id", uid))
			}

			level := slog.LevelInfo
			if sw.status >= http.StatusInternalServerError {
				level = slog.LevelError
			} else if sw.status >= http.StatusBadRequest {
				level = slog.LevelWarn
			}
			log.LogAttrs(req.Context(), level, "http request", attrs...)
		})
	}
}

// FlusherTraceMiddleware starts an OTel server span and sets X-Trace-Id while preserving http.Flusher
// (required for SSE endpoints where non-flushing ResponseWriter wrappers would buffer events).
func FlusherTraceMiddleware(service string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(service)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.target", r.URL.Path),
				),
			)
			defer span.End()

			if traceID := telemetry.TraceIDFromContext(ctx); traceID != "" {
				w.Header().Set("X-Trace-Id", traceID)
			}

			sw := &flushingStatusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(ctx))

			span.SetAttributes(attribute.Int("http.status_code", sw.status))
			if sw.status >= http.StatusInternalServerError {
				span.SetStatus(otelcodes.Error, http.StatusText(sw.status))
			}
		})
	}
}

// Recoverer catches panics, logs them with structured JSON, and writes the canonical
// NexusMart error envelope {"error":{"code":"INTERNAL_ERROR","message":"...","trace_id":"..."}}.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if recErr, ok := rec.(error); ok && errors.Is(recErr, http.ErrAbortHandler) {
						panic(rec)
					}
					if log != nil {
						log.ErrorContext(r.Context(), "panic recovered",
							slog.String("panic", fmt.Sprint(rec)),
							slog.String("path", r.URL.Path),
							slog.String("stack", string(debug.Stack())),
						)
					}
					httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
