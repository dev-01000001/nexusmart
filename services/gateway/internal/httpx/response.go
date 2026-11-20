package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

// MapGRPCError translates a gRPC status error to HTTP status code, canonical code, and message.
func MapGRPCError(err error) (int, string, string) {
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error"
	}

	msg := st.Message()
	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest, "INVALID_ARGUMENT", msg
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "UNAUTHENTICATED", msg
	case codes.PermissionDenied:
		return http.StatusForbidden, "PERMISSION_DENIED", msg
	case codes.NotFound:
		return http.StatusNotFound, "NOT_FOUND", msg
	case codes.AlreadyExists:
		return http.StatusConflict, "ALREADY_EXISTS", msg
	case codes.Aborted:
		return http.StatusConflict, "ABORTED", msg
	case codes.FailedPrecondition:
		return http.StatusConflict, "FAILED_PRECONDITION", msg
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", msg
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, "DEADLINE_EXCEEDED", msg
	case codes.Unavailable:
		return http.StatusServiceUnavailable, "UNAVAILABLE", msg
	case codes.Unimplemented:
		return http.StatusNotImplemented, "UNIMPLEMENTED", msg
	default:
		if msg == "" {
			msg = "internal server error"
		}
		return http.StatusInternalServerError, "INTERNAL_ERROR", msg
	}
}

// WriteGRPCError maps a gRPC error to the standard REST error envelope.
func WriteGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	httpStatus, code, msg := MapGRPCError(err)
	WriteError(w, r, httpStatus, code, msg)
}

// DecodeJSON decodes a required JSON request body (max 2 MiB).
func DecodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
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
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// ParsePagination parses page and page_size query params with defaults (1, 20).
func ParsePagination(r *http.Request) (page int32, pageSize int32) {
	page = ParseInt32Query(r, "page", 1)
	if page <= 0 {
		page = 1
	}
	pageSize = ParseInt32Query(r, "page_size", 20)
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
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
