package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type loggerContextKey struct{}

// WithLogger attaches a structured logger to ctx for downstream error logging.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	if log == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerContextKey{}, log)
}

func loggerFromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if log, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok && log != nil {
			return log
		}
	}
	return slog.Default()
}

// ErrorBody represents the standard NexusMart JSON error envelope body.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

// ErrorEnvelope matches docs/contracts.md Section 3 error schema.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// WriteJSON encodes payload as JSON with statusCode.
func WriteJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError writes the standard NexusMart error envelope including trace_id.
func WriteError(w http.ResponseWriter, r *http.Request, statusCode int, code, message string) {
	traceID := ""
	if r != nil {
		traceID = telemetry.TraceIDFromContext(r.Context())
	}
	WriteJSON(w, statusCode, ErrorEnvelope{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			TraceID: traceID,
		},
	})
}

func extractDomainCode(msg string, allowed ...string) string {
	trimmed := strings.TrimSpace(msg)
	for _, code := range allowed {
		if !strings.HasPrefix(trimmed, code) {
			continue
		}
		if len(trimmed) == len(code) {
			return code
		}
		switch trimmed[len(code)] {
		case ':', ' ', '(':
			return code
		}
	}
	return ""
}

// MapGRPCError translates a gRPC status error to HTTP status code, canonical code, and message.
func MapGRPCError(err error) (int, string, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error"
	}

	msg := st.Message()
	switch st.Code() {
	case codes.InvalidArgument:
		if code := extractDomainCode(msg, "BANNED_CONTENT", "PROHIBITED_CATEGORY"); code != "" {
			return http.StatusBadRequest, code, msg
		}
		return http.StatusBadRequest, "INVALID_ARGUMENT", msg
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "UNAUTHENTICATED", msg
	case codes.PermissionDenied:
		if code := extractDomainCode(msg, "MAKER_CHECKER_CONFLICT"); code != "" {
			return http.StatusForbidden, code, msg
		}
		return http.StatusForbidden, "PERMISSION_DENIED", msg
	case codes.NotFound:
		return http.StatusNotFound, "NOT_FOUND", msg
	case codes.AlreadyExists:
		return http.StatusConflict, "ALREADY_EXISTS", msg
	case codes.Aborted:
		return http.StatusConflict, "ABORTED", msg
	case codes.FailedPrecondition:
		if code := extractDomainCode(msg,
			"SKU_INACTIVE",
			"COD_PHONE_NOT_VERIFIED",
			"COD_AREA_NOT_SUPPORTED",
			"COD_REJECTED_LIMIT_EXCEEDED",
			"OWNER_NAME_CHANGED",
			"SHOP_SUSPENDED",
			"PAYMENT_METHOD_DISABLED",
			"ORDER_AMOUNT_OUT_OF_RANGE",
			"VERSION_CONFLICT",
		); code != "" {
			return http.StatusConflict, code, msg
		}
		return http.StatusConflict, "FAILED_PRECONDITION", msg
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", msg
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, "DEADLINE_EXCEEDED", "upstream service timed out"
	case codes.Unavailable:
		return http.StatusServiceUnavailable, "UNAVAILABLE", "service temporarily unavailable"
	case codes.Unimplemented:
		return http.StatusNotImplemented, "UNIMPLEMENTED", msg
	case codes.Internal, codes.Unknown, codes.DataLoss:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error"
	}
}

// WriteGRPCError maps a gRPC error to the standard REST error envelope.
// Internal/Unknown/DataLoss/Unavailable/DeadlineExceeded details are logged server-side with trace_id and never leaked to clients.
func WriteGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	httpStatus, code, msg := MapGRPCError(err)
	if httpStatus >= http.StatusInternalServerError && err != nil {
		ctx := context.Background()
		method := ""
		path := ""
		if r != nil {
			ctx = r.Context()
			method = r.Method
			if r.URL != nil {
				path = r.URL.Path
			}
		}
		grpcCode := "NON_GRPC"
		if st, ok := status.FromError(err); ok {
			grpcCode = st.Code().String()
		}
		loggerFromContext(ctx).ErrorContext(ctx, "upstream grpc internal error",
			slog.Int("http_status", httpStatus),
			slog.String("grpc_code", grpcCode),
			slog.String("method", method),
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
	}
	WriteError(w, r, httpStatus, code, msg)
}

// MaxBodyBytes is the maximum allowed HTTP request body size (2 MiB).
const MaxBodyBytes = 2 << 20

// DecodeJSON decodes a required JSON request body (max MaxBodyBytes).
func DecodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// DecodeOptionalJSON decodes an optional JSON request body; empty body is ignored.
func DecodeOptionalJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// WriteDecodeError writes 413 PAYLOAD_TOO_LARGE when err is caused by an oversized body,
// and 400 INVALID_JSON for all other JSON decode errors.
func WriteDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		WriteError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
		return
	}
	WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
}

// ParsePagination parses page and page_size query params with defaults (1, 20)
// and caps page_size at 100. It rejects page values whose 0-based SQL offset
// (page-1)*page_size (or page itself) would exceed math.MaxInt32.
func ParsePagination(r *http.Request) (page int32, pageSize int32, err error) {
	pageSize = parsePageSize(r)

	rawPage := strings.TrimSpace(r.URL.Query().Get("page"))
	if rawPage == "" {
		return 1, pageSize, nil
	}
	page64, parseErr := strconv.ParseInt(rawPage, 10, 64)
	if parseErr != nil {
		if errors.Is(parseErr, strconv.ErrRange) && !strings.HasPrefix(rawPage, "-") {
			return 0, 0, errors.New("page exceeds maximum pagination offset")
		}
		return 1, pageSize, nil
	}
	if page64 <= 0 {
		return 1, pageSize, nil
	}
	if page64 > math.MaxInt32 || (page64-1)*int64(pageSize) > math.MaxInt32 {
		return 0, 0, errors.New("page exceeds maximum pagination offset")
	}
	return int32(page64), pageSize, nil
}

func parsePageSize(r *http.Request) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("page_size"))
	if raw == "" {
		return 20
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(raw, "-") {
			return 100
		}
		return 20
	}
	if n <= 0 {
		return 20
	}
	if n > 100 {
		return 100
	}
	return int32(n)
}

func ParseInt32Query(r *http.Request, key string, def int32) int32 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return def
	}
	return int32(n)
}

func ParseInt64Query(r *http.Request, key string, def int64) int64 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func ParseBoolQuery(r *http.Request, key string) bool {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return b
}

// ParseTimestamp parses an RFC3339 timestamp string into protobuf Timestamp.
func ParseTimestamp(s string) (*timestamppb.Timestamp, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, err
		}
	}
	return timestamppb.New(t.UTC()), nil
}

// FormatTimestamp formats a protobuf Timestamp as RFC3339 UTC string.
func FormatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil || !ts.IsValid() || ts.AsTime().IsZero() {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}
