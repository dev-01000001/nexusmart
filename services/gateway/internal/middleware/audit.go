package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
)

const (
	defaultAuditMaxAttempts         = 3
	defaultAuditFirstAttemptTimeout = 500 * time.Millisecond
	defaultAuditRetryTimeout        = 2 * time.Second
	defaultAuditRetryBackoff        = 150 * time.Millisecond
	defaultAuditMaxPendingRetries   = 64
	auditResponseCaptureLimit       = 4096
)

// userSelfAuditedRoutes lists admin mutation routes that services/user already writes
// to audit_logs transactionally, skipped by gateway middleware to prevent duplicate records.
var userSelfAuditedRoutes = map[string]struct{}{
	"PATCH /api/v1/admin/users/{id}/status": {},
	"PATCH /api/v1/admin/users/{id}/role":   {},
	"POST /api/v1/admin/shops/{id}/review":  {},
}

// AdminAuditConfig configures centralized audit logging for mutating /api/v1/admin/* routes.
type AdminAuditConfig struct {
	UserClient          userv1.UserServiceClient
	TrustedProxies      *TrustedProxies
	Logger              *slog.Logger
	MaxAttempts         int
	FirstAttemptTimeout time.Duration
	RetryTimeout        time.Duration
	RetryBackoff        time.Duration
	MaxPendingRetries   int
}

type auditResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	capture     bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if remaining := auditResponseCaptureLimit - w.capture.Len(); remaining > 0 {
		n := len(b)
		if n > remaining {
			n = remaining
		}
		_, _ = w.capture.Write(b[:n])
	}
	return w.ResponseWriter.Write(b)
}

func (w *auditResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *auditResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// AdminAudit records an audit entry via UserService.WriteAuditLog for every mutating
// (POST/PUT/PATCH/DELETE) request under /api/v1/admin/ that returns 2xx, skipping routes
// already self-audited by services/user. Audit write failures never fail or block the
// HTTP response; failed writes are logged and retried with bounded attempts.
func AdminAudit(cfg AdminAuditConfig) func(http.Handler) http.Handler {
	if cfg.UserClient == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultAuditMaxAttempts
	}
	firstTimeout := cfg.FirstAttemptTimeout
	if firstTimeout <= 0 {
		firstTimeout = defaultAuditFirstAttemptTimeout
	}
	retryTimeout := cfg.RetryTimeout
	if retryTimeout <= 0 {
		retryTimeout = defaultAuditRetryTimeout
	}
	retryBackoff := cfg.RetryBackoff
	if retryBackoff <= 0 {
		retryBackoff = defaultAuditRetryBackoff
	}
	maxPending := cfg.MaxPendingRetries
	if maxPending <= 0 {
		maxPending = defaultAuditMaxPendingRetries
	}
	retrySem := make(chan struct{}, maxPending)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutatingMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			aw := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(aw, r)

			if aw.status < http.StatusOK || aw.status >= http.StatusMultipleChoices {
				return
			}

			routePattern := r.URL.Path
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if pat := rctx.RoutePattern(); pat != "" {
					routePattern = pat
				}
			}
			action := r.Method + " " + routePattern
			if _, skip := userSelfAuditedRoutes[action]; skip {
				return
			}

			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok || claims.UserID() == "" {
				return
			}

			auditReq := &userv1.WriteAuditLogRequest{
				ActorId:    claims.UserID(),
				ActorRole:  claims.Role,
				Action:     action,
				TargetType: auditTargetType(routePattern),
				TargetId:   auditTargetID(r, aw.capture.Bytes(), routePattern),
				BeforeJson: "{}",
				AfterJson:  "{}",
				IpAddress:  cfg.TrustedProxies.ClientIP(r),
			}

			// Detach from request cancellation while preserving OTel trace context and verified admin JWT.
			baseCtx := auth.InjectOutgoingGRPC(context.WithoutCancel(r.Context()), auth.RawTokenFromContext(r.Context()))

			ctx1, cancel1 := context.WithTimeout(baseCtx, firstTimeout)
			_, err := cfg.UserClient.WriteAuditLog(ctx1, auditReq)
			cancel1()
			if err == nil {
				return
			}

			if cfg.Logger != nil {
				cfg.Logger.WarnContext(baseCtx, "admin audit log write failed on first attempt",
					slog.String("action", auditReq.Action),
					slog.String("target_type", auditReq.TargetType),
					slog.String("target_id", auditReq.TargetId),
					slog.String("error", err.Error()),
				)
			}
			if maxAttempts <= 1 {
				if cfg.Logger != nil {
					cfg.Logger.ErrorContext(baseCtx, "failed to write admin audit log",
						slog.String("action", auditReq.Action),
						slog.String("target_type", auditReq.TargetType),
						slog.String("target_id", auditReq.TargetId),
						slog.Int("attempts", 1),
						slog.String("error", err.Error()),
					)
				}
				return
			}

			select {
			case retrySem <- struct{}{}:
				go func() {
					defer func() { <-retrySem }()
					lastErr := err
					for attempt := 2; attempt <= maxAttempts; attempt++ {
						time.Sleep(retryBackoff * time.Duration(attempt-1))
						rctx, cancel := context.WithTimeout(baseCtx, retryTimeout)
						_, callErr := cfg.UserClient.WriteAuditLog(rctx, auditReq)
						cancel()
						if callErr == nil {
							return
						}
						lastErr = callErr
					}
					if cfg.Logger != nil {
						cfg.Logger.ErrorContext(baseCtx, "failed to write admin audit log after retries",
							slog.String("action", auditReq.Action),
							slog.String("target_type", auditReq.TargetType),
							slog.String("target_id", auditReq.TargetId),
							slog.Int("attempts", maxAttempts),
							slog.String("error", lastErr.Error()),
						)
					}
				}()
			default:
				if cfg.Logger != nil {
					cfg.Logger.ErrorContext(baseCtx, "dropping admin audit retry: retry queue full",
						slog.String("action", auditReq.Action),
						slog.String("target_type", auditReq.TargetType),
						slog.String("target_id", auditReq.TargetId),
						slog.String("error", err.Error()),
					)
				}
			}
		})
	}
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func auditTargetType(routePattern string) string {
	trimmed := strings.TrimPrefix(routePattern, "/api/v1/admin/")
	seg, _, _ := strings.Cut(trimmed, "/")
	switch seg {
	case "products":
		return "product"
	case "categories":
		return "category"
	case "vouchers":
		return "voucher"
	case "flash-sales":
		return "flash_sale"
	case "flash-sale-items":
		return "flash_sale_item"
	case "coins":
		return "coin"
	case "payments":
		return "payment"
	case "users":
		return "user"
	case "shops":
		return "shop"
	default:
		if seg != "" && !strings.HasPrefix(seg, "{") {
			return strings.ReplaceAll(seg, "-", "_")
		}
		return "admin"
	}
}

func auditTargetID(r *http.Request, respBody []byte, routePattern string) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		for _, key := range []string{"id", "item_id"} {
			if val := strings.TrimSpace(rctx.URLParam(key)); val != "" {
				return val
			}
		}
		for _, val := range rctx.URLParams.Values {
			if v := strings.TrimSpace(val); v != "" {
				return v
			}
		}
	}

	if id := extractTargetIDFromJSON(respBody); id != "" {
		return id
	}
	if routePattern != "" {
		return routePattern
	}
	return "-"
}

func extractTargetIDFromJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, key := range []string{"id", "run_id", "user_id", "refund_id"} {
		if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	for _, nestedKey := range []string{"category", "voucher", "flash_sale", "product", "item", "refund"} {
		if nested, ok := obj[nestedKey].(map[string]any); ok {
			for _, idKey := range []string{"id", "refund_id"} {
				if s, ok := nested[idKey].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}
