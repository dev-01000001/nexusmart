package carrier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Canonical internal shipment statuses matching docs/contracts.md & shipping.proto.
const (
	StatusCreated    = "created"
	StatusPicking    = "picking"
	StatusDelivering = "delivering"
	StatusDelivered  = "delivered"
	StatusFailed     = "failed"
	StatusReturning  = "returning"
	StatusReturned   = "returned"
	StatusCancelled  = "cancelled"
	StatusQueued     = "queued"

	// DefaultLabelBaseURL is the fallback public label base URL when SHIPPING_LABEL_BASE_URL is unset.
	DefaultLabelBaseURL = "http://localhost:8080/api/v1/shipping/labels"
)

// ErrInvalidCarrierToken indicates a carrier rejected or could not accept the provided API token.
var ErrInvalidCarrierToken = errors.New("invalid carrier API token")

// ErrMissingGHNMapping indicates a ward lacks a GHN district/ward code mapping in sandbox mode.
// Aggregator treats it as fallback-eligible in CalculateFee (not out-of-service-area),
// while CreateShipment rejects it immediately as non-retryable without calling GHN.
var ErrMissingGHNMapping = errors.New("phường chưa có mã GHN, chọn hãng khác")

// ErrRedeliveryUnsupported indicates the carrier does not expose an API endpoint for redelivery requests.
var ErrRedeliveryUnsupported = errors.New("carrier does not support redelivery API")

// NonRetryableError wraps errors that should not be retried (e.g., 4xx validation errors).
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string {
	if e == nil || e.Err == nil {
		return "non-retryable carrier error"
	}
	return e.Err.Error()
}

func (e *NonRetryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsNonRetryable reports whether err is marked non-retryable.
func IsNonRetryable(err error) bool {
	var nr *NonRetryableError
	return errors.As(err, &nr)
}

// FeeRequest contains route and package parameters for shipping fee calculation.
type FeeRequest struct {
	ShopID                string
	FromProvinceCode      string
	FromProvinceName      string
	FromWardCode          string
	FromWardName          string
	FromLegacyProvince    string
	FromLegacyDistrict    string
	FromDistrictID        int
	FromGHNWardCode       string
	ToProvinceCode        string
	ToProvinceName        string
	ToWardCode            string
	ToWardName            string
	ToLegacyProvince      string
	ToLegacyDistrict      string
	ToDistrictID          int
	ToGHNWardCode         string
	WeightGrams           int32
	CODAmountVND          int64
	FallbackWeightItemQty int32
	AllowedCarriers       []string
}

// Quote represents a shipping option from a carrier or fallback table.
type Quote struct {
	CarrierCode   string
	CarrierName   string
	ServiceCode   string
	ServiceName   string
	FeeVND        int64
	EstimatedDays int32
	IsFallback    bool
	CODSupported  bool
}

// Address holds sender/recipient details for creating a carrier shipment.
type Address struct {
	Name           string
	Phone          string
	ProvinceCode   string
	ProvinceName   string
	WardCode       string
	WardName       string
	LegacyProvince string
	LegacyDistrict string
	StreetAddress  string
	GHNDistrictID  int
	GHNWardCode    string
}

// ShipmentRequest contains parameters to create a shipment with a carrier.
type ShipmentRequest struct {
	OrderID         string
	ClientOrderCode string
	ShopID          string
	Direction       string
	ReturnRequestID string
	ServiceCode     string
	CODAmountVND    int64
	WeightGrams     int32
	FromAddress     Address
	ToAddress       Address
	ItemsSummary    string
}

// ShipmentResult is returned by a carrier after creating a shipment.
type ShipmentResult struct {
	CarrierCode  string
	ServiceCode  string
	TrackingCode string
	LabelURL     string
	FeeVND       int64
	Status       string
}

// TrackingStatus is returned by carrier polling.
type TrackingStatus struct {
	TrackingCode string
	Status       string
	RawStatus    string
	Description  string
	OccurredAt   time.Time
	Changed      bool
}

// Carrier defines the uniform shipping provider adapter interface.
type Carrier interface {
	Code() string
	Name() string
	IsMock() bool
	CalculateFee(ctx context.Context, req FeeRequest) ([]Quote, error)
	CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error)
	CancelShipment(ctx context.Context, trackingCode string) error
	GetStatus(ctx context.Context, trackingCode string) (*TrackingStatus, error)
	RequestRedelivery(ctx context.Context, trackingCode, preferredDate, note string) error
	TestConnection(ctx context.Context, token string) error
	SetToken(token string)
}

// MapCarrierStatus maps raw provider statuses (GHN, GHTK, Mock Express, or canonical) to internal statuses.
// Per w2-plan §1.1 & DECISIONS: "picked", GHTK "3", and "da_lay_hang" map to delivering
// (carrier already holds parcel, no longer cancellable); "ready_to_pick", "picking", and GHTK "12"/"127"/"128"
// remain picking (cancellable).
func MapCarrierStatus(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "created", "new", "1", "2", "chua_tiep_nhan", "da_tiep_nhan":
		return StatusCreated, true
	case "ready_to_pick", "picking", "money_collect_picking", "12", "127", "128", "da_dieu_phoi_lay_hang":
		return StatusPicking, true
	case "picked", "3", "da_lay_hang", "storing", "transporting", "sorting", "delivering", "money_collect_delivering", "in_transit", "out_for_delivery", "shipping", "4", "5", "123", "da_dieu_phoi_giao_hang", "dang_giao_hang":
		return StatusDelivering, true
	case "delivered", "completed", "6", "45", "da_giao_hang", "da_doi_soat":
		return StatusDelivered, true
	case "delivery_fail", "failed", "delivery_failed", "exception", "damage", "lost", "9", "10", "49", "khong_giao_duoc_hang", "delay_giao_hang":
		return StatusFailed, true
	case "waiting_to_return", "return", "return_transporting", "return_sorting", "returning", "13", "20", "dang_tra_hang":
		return StatusReturning, true
	case "returned", "return_fail", "11", "21", "da_tra_hang":
		return StatusReturned, true
	case "cancel", "cancelled", "canceled", "-1", "huy", "hủy":
		return StatusCancelled, true
	default:
		return "", false
	}
}

// StatusDescriptionVI returns a natural Vietnamese status description for timeline events and notifications (SHP-B8).
func StatusDescriptionVI(status string) string {
	switch status {
	case StatusCreated:
		return "Vận đơn đã được tạo"
	case StatusPicking:
		return "Đang lấy hàng"
	case StatusDelivering:
		return "Đang giao hàng"
	case StatusDelivered:
		return "Giao hàng thành công"
	case StatusFailed:
		return "Giao hàng thất bại"
	case StatusReturning:
		return "Đang hoàn hàng về người bán"
	case StatusReturned:
		return "Đã hoàn hàng về người bán"
	case StatusCancelled:
		return "Vận đơn đã bị huỷ"
	default:
		return "Cập nhật trạng thái vận đơn"
	}
}

// ResolveDescriptionVI replaces empty or raw English carrier status strings with Vietnamese descriptions.
func ResolveDescriptionVI(internalStatus, provided string) string {
	desc := strings.TrimSpace(provided)
	if desc == "" {
		return StatusDescriptionVI(internalStatus)
	}
	if _, isRawStatus := MapCarrierStatus(desc); isRawStatus {
		return StatusDescriptionVI(internalStatus)
	}
	if strings.EqualFold(desc, "Synced from GHN status polling") || strings.EqualFold(desc, "Synced from GHTK status polling") {
		return StatusDescriptionVI(internalStatus)
	}
	return desc
}

// IsMockTokenRejected validates obvious invalid tokens in mock mode so admin enable/test checks work deterministically.
func IsMockTokenRejected(token string, requireNonEmpty bool) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return requireNonEmpty
	}
	if len(trimmed) < 6 {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, bad := range []string{"invalid", "wrong", "bad"} {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	return false
}

// IsAlreadyCancelledMessage reports whether a carrier error/message indicates the shipment was already cancelled.
func IsAlreadyCancelledMessage(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	for _, needle := range []string{
		"already cancel",
		"already_cancel",
		"order is cancel",
		"order_is_cancel",
		"status is cancel",
		"status_is_cancel",
		"đã hủy",
		"đã huỷ",
		"da huy",
		"hủy rồi",
		"huỷ rồi",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// IsDuplicateClientOrderCodeMessage reports whether a carrier error/message indicates client_order_code already exists.
func IsDuplicateClientOrderCodeMessage(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	hasClientCode := strings.Contains(lower, "client_order_code") ||
		strings.Contains(lower, "client order code") ||
		strings.Contains(lower, "mã đơn khách hàng") ||
		strings.Contains(lower, "ma don khach hang")
	if !hasClientCode {
		return false
	}
	for _, needle := range []string{
		"exist",
		"duplicate",
		"already",
		"tồn tại",
		"ton tai",
		"trùng",
		"trung",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// mockTrackingCode produces a deterministic 12-hex-char tracking code keyed by ClientOrderCode
// so retries or concurrent fallback calls for the same attempt never mint a second tracking code.
// Falls back to a random UUID suffix when ClientOrderCode is empty.
func mockTrackingCode(prefix, clientOrderCode string) string {
	key := strings.TrimSpace(clientOrderCode)
	if key == "" {
		return prefix + "-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	}
	sum := sha256.Sum256([]byte(prefix + ":" + key))
	return prefix + "-" + strings.ToUpper(hex.EncodeToString(sum[:6]))
}

// IsCancellableStatus reports whether a shipment in status can still be cancelled before pickup.
func IsCancellableStatus(status string) bool {
	return status == StatusCreated || status == StatusPicking
}

// IsTerminalStatus reports whether a shipment has reached a terminal state.
func IsTerminalStatus(status string) bool {
	return status == StatusDelivered || status == StatusReturned || status == StatusCancelled
}
