package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ChatStreamer is the minimal service interface required by the SSE chat HTTP handler.
type ChatStreamer interface {
	StreamChat(ctx context.Context, cc tools.CallContext, req service.ChatRequest, emit func(service.SSEEvent) error) error
}

// Config configures the assistant HTTP server.
type Config struct {
	ServiceName string
	Service     ChatStreamer
	AuthManager *auth.Manager
	RED         *metrics.RED
	Gatherer    prometheus.Gatherer
	ReadyCheck  func(context.Context) error
	Logger      *slog.Logger
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

// New constructs the chi HTTP handler exposing /assistant/chat (SSE) plus /healthz, /readyz, /metrics.
func New(cfg Config) http.Handler {
	svcName := cfg.ServiceName
	if svcName == "" {
		svcName = "assistant"
	}
	gatherer := cfg.Gatherer
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(telemetry.HTTPMiddleware(svcName))
	if cfg.RED != nil {
		r.Use(cfg.RED.HTTPMiddleware())
	}

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": svcName,
		})
	})

	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if cfg.ReadyCheck != nil {
			if err := cfg.ReadyCheck(req.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status":  "not_ready",
					"service": svcName,
					"error":   err.Error(),
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ready",
			"service": svcName,
		})
	})

	r.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))

	chatHandler := handleChatSSE(cfg.Service, cfg.AuthManager, log)
	r.Post("/assistant/chat", chatHandler)

	return r
}

func handleChatSSE(svc ChatStreamer, jwtMgr *auth.Manager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if svc == nil {
			writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "assistant service not ready", 0)
			return
		}

		// Optional Bearer JWT: invalid token -> 401; missing token -> anonymous public RAG.
		var cc tools.CallContext
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader != "" {
			if jwtMgr == nil {
				writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication verifier unavailable", 0)
				return
			}
			claims, err := jwtMgr.VerifyAccessToken(authHeader)
			if err != nil {
				writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired access token", 0)
				return
			}
			cc.UserID = claims.UserID()
			cc.UserRole = claims.Role
			cc.SellerShopID = claims.ShopID
			cc.RawBearer = authHeader
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req service.ChatRequest
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON request body", 0)
			return
		}
		req.ClientIP = extractClientIP(r)
		if strings.TrimSpace(req.ShopID) != "" {
			cc.ShopID = strings.TrimSpace(req.ShopID)
		}

		// Clear write deadline so long-lived SSE streams are not terminated prematurely.
		rc := http.NewResponseController(w) //nolint:bodyclose // wraps ResponseWriter, not an *http.Response body
		_ = rc.SetWriteDeadline(time.Time{})

		flusher, _ := w.(http.Flusher)
		started := false

		emit := func(ev service.SSEEvent) error {
			if !started {
				started = true
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
			}
			dataBytes, err := json.Marshal(ev.Data)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, dataBytes); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			} else {
				_ = rc.Flush()
			}
			return nil
		}

		err := svc.StreamChat(ctx, cc, req, emit)
		if err == nil {
			return
		}

		statusCode, errCode, msg, retryAfter := mapDomainError(err)
		if !started {
			writeError(w, r, statusCode, errCode, msg, retryAfter)
			return
		}

		// Already streaming SSE: emit terminal "error" SSE event and flush.
		log.WarnContext(ctx, "assistant SSE stream ended with error",
			slog.String("code", errCode),
			slog.String("error", err.Error()),
		)
		_ = emit(service.SSEEvent{
			Type: "error",
			Data: errorBody{
				Code:    errCode,
				Message: msg,
				TraceID: telemetry.TraceIDFromContext(ctx),
			},
		})
	}
}

func mapDomainError(err error) (statusCode int, code, message string, retryAfterSec int64) {
	code = service.ErrorCode(err)
	retryAfterSec = service.RetryAfterSeconds(err)
	switch {
	case errors.Is(err, service.ErrUnauthorized):
		return http.StatusUnauthorized, code, err.Error(), 0
	case errors.Is(err, service.ErrForbidden):
		return http.StatusForbidden, code, err.Error(), 0
	case errors.Is(err, service.ErrInvalidArgument):
		return http.StatusBadRequest, code, err.Error(), 0
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound, code, err.Error(), 0
	case errors.Is(err, service.ErrRateLimited), errors.Is(err, service.ErrBudgetExceeded), errors.Is(err, service.ErrAbuseLocked):
		if retryAfterSec <= 0 {
			retryAfterSec = 60
		}
		return http.StatusTooManyRequests, code, err.Error(), retryAfterSec
	default:
		return http.StatusBadGateway, "UPSTREAM_ERROR", "trợ lý AI tạm thời gặp sự cố, vui lòng thử lại sau", 0
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryAfterSec int64) {
	if retryAfterSec > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSec, 10))
	}
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    code,
			Message: message,
			TraceID: telemetry.TraceIDFromContext(r.Context()),
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func extractClientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return xff
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
