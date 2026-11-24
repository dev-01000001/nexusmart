package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
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
	"PATCH /api/v1/admin/users/{id}/status":             {},
	"PATCH /api/v1/admin/users/{id}/role":               {},
	"POST /api/v1/admin/staff":                          {},
	"POST /api/v1/admin/shops/{id}/review":              {},
	"POST /api/v1/admin/shops/{id}/suspend":             {},
	"POST /api/v1/admin/shops/{id}/unsuspend":           {},
	"POST /api/v1/admin/approvals/shop/{id}/decide":     {},
	"POST /api/v1/admin/shops/{id}/bank-account/review": {},
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

type auditSummaryHolder struct {
	targetID string
	before   map[string]any
	summary  map[string]any
}

type auditSummaryContextKey struct{}

// SetAuditBefore attaches a structured before-state snapshot to the current request context
// for AdminAudit so mutations that read previous state (e.g. payment method config per F7)
// record both before_json and after_json safely sanitized.
func SetAuditBefore(ctx context.Context, before map[string]any) {
	if ctx == nil || len(before) == 0 {
		return
	}
	holder, ok := ctx.Value(auditSummaryContextKey{}).(*auditSummaryHolder)
	if !ok || holder == nil {
		return
	}
	holder.before = before
}

// SetAuditSummary attaches a compact handler-provided target ID and structured summary
// to the current request context for AdminAudit. AdminAudit prioritizes this summary over
// raw request/response capture so large (>4 KiB) responses never lose target_id or after_json
// and never log file contents or PII.
func SetAuditSummary(ctx context.Context, targetID string, summary map[string]any) {
	if ctx == nil {
		return
	}
	holder, ok := ctx.Value(auditSummaryContextKey{}).(*auditSummaryHolder)
	if !ok || holder == nil {
		return
	}
	if tid := strings.TrimSpace(targetID); tid != "" {
		holder.targetID = tid
	}
	if len(summary) > 0 {
		holder.summary = summary
	}
}

type auditResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	capture     bytes.Buffer
	truncated   bool
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
			w.truncated = true
		}
		_, _ = w.capture.Write(b[:n])
	} else if len(b) > 0 {
		w.truncated = true
	}
	return w.ResponseWriter.Write(b)
}

type cappedCaptureReadCloser struct {
	rc        io.ReadCloser
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedCaptureReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		if remaining := c.limit - c.buf.Len(); remaining > 0 {
			toWrite := n
			if toWrite > remaining {
				toWrite = remaining
				c.truncated = true
			}
			_, _ = c.buf.Write(p[:toWrite])
		} else {
			c.truncated = true
		}
	}
	return n, err
}

func (c *cappedCaptureReadCloser) Close() error {
	return c.rc.Close()
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

			var reqCapture *cappedCaptureReadCloser
			if r.Body != nil {
				reqCapture = &cappedCaptureReadCloser{rc: r.Body, limit: auditResponseCaptureLimit}
				r.Body = reqCapture
			}

			holder := &auditSummaryHolder{}
			r = r.WithContext(context.WithValue(r.Context(), auditSummaryContextKey{}, holder))

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

			var reqBytes []byte
			reqTrunc := false
			if reqCapture != nil {
				reqBytes = reqCapture.buf.Bytes()
				reqTrunc = reqCapture.truncated
			}

			targetID := holder.targetID
			if targetID == "" {
				targetID = auditTargetID(r, aw.capture.Bytes(), routePattern)
			}

			beforeJSON := "{}"
			if len(holder.before) > 0 {
				if encoded, encErr := json.Marshal(sanitizeAuditValue(holder.before)); encErr == nil {
					beforeJSON = string(encoded)
				}
			}

			auditReq := &userv1.WriteAuditLogRequest{
				ActorId:    claims.UserID(),
				ActorRole:  claims.Role,
				Action:     action,
				TargetType: auditTargetType(routePattern),
				TargetId:   targetID,
				BeforeJson: beforeJSON,
				AfterJson:  buildAuditAfterJSON(reqBytes, reqTrunc, aw.capture.Bytes(), aw.truncated, holder.summary),
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
	switch {
	case strings.Contains(trimmed, "/bank-account"):
		return "shop_bank_account"
	case strings.HasPrefix(trimmed, "fees/categories"):
		return "category_fee_schedule"
	case strings.HasPrefix(trimmed, "fees/payment"):
		return "payment_fee_config"
	case strings.HasPrefix(trimmed, "reconciliation/cod"):
		return "reconciliation_run"
	case strings.HasPrefix(trimmed, "approvals/refund"):
		return "refund"
	case strings.HasPrefix(trimmed, "approvals/shop"):
		return "shop_suspension_approval"
	case strings.HasPrefix(trimmed, "payment-methods"):
		return "payment_method_config"
	}
	seg, _, _ := strings.Cut(trimmed, "/")
	switch seg {
	case "products":
		return "product"
	case "categories":
		return "category"
	case "banned-keywords":
		return "banned_keyword"
	case "vouchers":
		return "voucher"
	case "flash-sales":
		return "flash_sale"
	case "flash-sale-items":
		return "flash_sale_item"
	case "coins":
		return "coin"
	case "orders":
		return "order"
	case "payments":
		return "payment"
	case "refunds":
		return "refund"
	case "reconciliation":
		return "reconciliation_item"
	case "returns":
		return "return_request"
	case "carriers":
		return "carrier"
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
		for _, key := range []string{"id", "item_id", "code", "method"} {
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
	for _, key := range []string{"id", "run_id", "user_id", "refund_id", "carrier_code"} {
		if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	for _, nestedKey := range []string{"category", "voucher", "flash_sale", "product", "item", "refund", "return_request", "carrier", "bank_account", "schedule", "config", "order", "keyword", "run"} {
		if nested, ok := obj[nestedKey].(map[string]any); ok {
			for _, idKey := range []string{"id", "run_id", "refund_id", "carrier_code"} {
				if s, ok := nested[idKey].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

const auditRedactedValue = "[REDACTED]"

var auditSensitiveSubstrings = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"api_key",
	"apikey",
	"authorization",
	"private_key",
	"credential",
	"signature",
	"hmac",
	"cookie",
	"otp",
	"verification_code",
	"bank_account_number",
	"account_number",
	"account_no",
	"acct_no",
	"iban",
	"bank_account",
	"credit_card",
	"cvv",
}

func isSensitiveAuditKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch lower {
	case "token_configured", "credentials_configured", "credential_preview", "bank_account_masked", "account_number_masked", "bank_account_id", "bank_account_name", "otp_sent":
		return false
	}
	for _, sub := range auditSensitiveSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func sanitizeAuditValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			lower := strings.ToLower(strings.TrimSpace(k))
			// Recurse into nested bank_account objects so non-sensitive metadata (id, status, masked)
			// stays auditable while account_number and any other sensitive fields inside are redacted.
			if lower == "bank_account" {
				if nested, ok := item.(map[string]any); ok {
					out[k] = sanitizeAuditValue(nested)
					continue
				}
			}
			if isSensitiveAuditKey(k) {
				out[k] = auditRedactedValue
			} else {
				out[k] = sanitizeAuditValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = sanitizeAuditValue(item)
		}
		return out
	case []string:
		out := make([]string, len(val))
		for i, item := range val {
			out[i] = logger.ScrubSecrets(item)
		}
		return out
	case string:
		return logger.ScrubSecrets(val)
	default:
		return val
	}
}

func parseAndSanitizeJSON(raw []byte, truncated bool) (any, bool) {
	if truncated || len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, false
	}
	return sanitizeAuditValue(parsed), true
}

func buildAuditAfterJSON(reqRaw []byte, reqTrunc bool, respRaw []byte, respTrunc bool, summary map[string]any) string {
	if len(summary) > 0 {
		encoded, err := json.Marshal(sanitizeAuditValue(summary))
		if err != nil {
			return "{}"
		}
		return string(encoded)
	}
	payload := make(map[string]any, 2)
	if reqVal, ok := parseAndSanitizeJSON(reqRaw, reqTrunc); ok {
		payload["request"] = reqVal
	}
	if respVal, ok := parseAndSanitizeJSON(respRaw, respTrunc); ok {
		payload["response"] = respVal
	}
	if len(payload) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
