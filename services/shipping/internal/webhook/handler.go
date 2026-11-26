package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

const (
	maxBodyBytes     = 1 << 20 // 1 MiB
	maxTimestampSkew = 5 * time.Minute
)

// Payload mirrors docs/contracts.md §4.9 for POST /webhooks/shipping/{carrier},
// and also accepts optional native GHTK webhook fields as fallbacks.
type Payload struct {
	EventID      string          `json:"event_id"`
	TrackingCode string          `json:"tracking_code"`
	OrderID      string          `json:"order_id"`
	RawStatus    string          `json:"raw_status"`
	Status       string          `json:"status"`
	Description  string          `json:"description"`
	OccurredAt   string          `json:"occurred_at"`
	Hash         string          `json:"hash,omitempty"`
	LabelID      string          `json:"label_id,omitempty"`
	PartnerID    string          `json:"partner_id,omitempty"`
	StatusID     json.RawMessage `json:"status_id,omitempty"`
	ActionTime   string          `json:"action_time,omitempty"`
	Reason       string          `json:"reason,omitempty"`
}

// Handler verifies HMAC-SHA256 signatures and delegates shipping carrier webhooks.
type Handler struct {
	secret []byte
	svc    *service.ShippingService
	logger *slog.Logger
	nowFn  func() time.Time
}

// NewHandler creates a webhook HTTP handler.
func NewHandler(secret string, svc *service.ShippingService, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		secret: []byte(secret),
		svc:    svc,
		logger: logger,
		nowFn:  func() time.Time { return time.Now().UTC() },
	}
}

// Sign computes lowercase hex HMAC-SHA256 for body using secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature performs constant-time HMAC-SHA256 verification.
func VerifySignature(secret []byte, body []byte, provided string) bool {
	provided = strings.TrimSpace(strings.ToLower(provided))
	provided = strings.TrimPrefix(provided, "sha256=")
	if provided == "" || len(secret) == 0 {
		return false
	}
	providedBytes, err := hex.DecodeString(provided)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(expected, providedBytes)
}

// ServeHTTP handles POST /webhooks/shipping/{carrier}.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	carrierCode := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "carrier")))

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_BODY", "failed to read request body")
		return
	}

	sig := r.Header.Get("X-Signature")
	if !VerifySignature(h.secret, rawBody, sig) {
		h.logger.WarnContext(ctx, "rejected shipping webhook with invalid signature",
			slog.String("carrier", carrierCode),
			slog.Bool("signature_present", sig != ""),
		)
		h.writeError(w, r, http.StatusUnauthorized, "INVALID_SIGNATURE", "invalid or missing webhook signature")
		return
	}

	// Mandatory replay protection via X-Timestamp (Unix seconds, <= 5m skew).
	tsHeader := strings.TrimSpace(r.Header.Get("X-Timestamp"))
	if tsHeader == "" {
		h.writeError(w, r, http.StatusUnauthorized, "INVALID_TIMESTAMP", "missing X-Timestamp header")
		return
	}
	epochSec, parseErr := strconv.ParseInt(tsHeader, 10, 64)
	if parseErr != nil {
		h.writeError(w, r, http.StatusUnauthorized, "INVALID_TIMESTAMP", "invalid X-Timestamp header")
		return
	}
	skew := math.Abs(h.nowFn().Sub(time.Unix(epochSec, 0).UTC()).Seconds())
	if skew > maxTimestampSkew.Seconds() {
		h.logger.WarnContext(ctx, "rejected stale shipping webhook timestamp",
			slog.String("carrier", carrierCode),
			slog.Float64("skew_seconds", skew),
		)
		h.writeError(w, r, http.StatusUnauthorized, "STALE_TIMESTAMP", "webhook timestamp outside allowed window")
		return
	}

	var payload Payload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "INVALID_JSON", "malformed webhook payload")
		return
	}

	eventID := strings.TrimSpace(payload.EventID)
	if eventID == "" {
		eventID = strings.TrimSpace(payload.Hash)
	}
	trackingCode := strings.TrimSpace(payload.TrackingCode)
	if trackingCode == "" {
		trackingCode = strings.TrimSpace(payload.LabelID)
	}
	orderID := strings.TrimSpace(payload.OrderID)
	if orderID == "" {
		orderID = strings.TrimSpace(payload.PartnerID)
	}
	rawStatus := strings.TrimSpace(payload.RawStatus)
	if rawStatus == "" {
		rawStatus = strings.TrimSpace(payload.Status)
	}
	if rawStatus == "" && len(payload.StatusID) > 0 {
		rawStatus = parseStatusID(payload.StatusID)
	}
	description := strings.TrimSpace(payload.Description)
	if description == "" {
		description = strings.TrimSpace(payload.Reason)
	}

	occurredAtRaw := strings.TrimSpace(payload.OccurredAt)
	if occurredAtRaw == "" {
		occurredAtRaw = strings.TrimSpace(payload.ActionTime)
	}
	var occurredAt time.Time
	if occurredAtRaw != "" {
		parsed, err := parseWebhookTime(occurredAtRaw)
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "INVALID_OCCURRED_AT", "occurred_at must be RFC3339")
			return
		}
		occurredAt = parsed
	} else {
		occurredAt = h.nowFn()
	}

	sh, outcome, err := h.svc.HandleWebhook(ctx, service.WebhookInput{
		EventID:      eventID,
		CarrierCode:  carrierCode,
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  description,
		OccurredAt:   occurredAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidArgument), errors.Is(err, service.ErrUnknownStatus):
			h.writeError(w, r, http.StatusBadRequest, "INVALID_WEBHOOK", err.Error())
		case errors.Is(err, store.ErrCarrierMismatch):
			h.logger.WarnContext(ctx, "rejected shipping webhook with mismatched carrier",
				slog.String("carrier", carrierCode),
				slog.String("event_id", eventID),
			)
			h.writeError(w, r, http.StatusBadRequest, "CARRIER_MISMATCH", "webhook carrier does not match shipment carrier")
		case errors.Is(err, store.ErrNotFound):
			h.writeError(w, r, http.StatusNotFound, "SHIPMENT_NOT_FOUND", "shipment not found")
		default:
			h.logger.ErrorContext(ctx, "process shipping webhook failed",
				slog.String("carrier", carrierCode),
				slog.String("event_id", eventID),
				slog.String("error", err.Error()),
			)
			h.writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to process shipping webhook")
		}
		return
	}

	resp := map[string]any{
		"ok":      true,
		"outcome": string(outcome),
	}
	if sh != nil {
		resp["shipment_id"] = sh.ID
		resp["order_id"] = sh.OrderID
		resp["status"] = sh.Status
		resp["tracking_code"] = sh.TrackingCode
		if sh.Direction != "" {
			resp["direction"] = sh.Direction
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func parseStatusID(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		return strings.TrimSpace(asStr)
	}
	var asNum int64
	if err := json.Unmarshal(raw, &asNum); err == nil {
		return strconv.FormatInt(asNum, 10)
	}
	return strings.Trim(s, "\"")
}

// vietnamLocation is fixed UTC+7 (ICT) so timezone-less GHTK timestamps never depend on host tzdata.
var vietnamLocation = time.FixedZone("ICT", 7*60*60)

func parseWebhookTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC(), nil
	}
	if ts, err := time.ParseInLocation("2006-01-02 15:04:05", s, vietnamLocation); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, errors.New("invalid timestamp")
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":     errCode,
			"message":  msg,
			"trace_id": telemetry.TraceIDFromContext(r.Context()),
		},
	})
}
