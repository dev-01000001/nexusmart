package carrier

import (
	"context"
	"errors"
	"strings"
	"time"
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
)

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
	ShopID           string
	FromProvinceCode string
	FromWardCode     string
	FromDistrictID   int
	FromGHNWardCode  string
	ToProvinceCode   string
	ToWardCode       string
	ToDistrictID     int
	ToGHNWardCode    string
	WeightGrams      int32
	CODAmountVND     int64
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
}

// Address holds sender/recipient details for creating a carrier shipment.
type Address struct {
	Name          string
	Phone         string
	ProvinceCode  string
	WardCode      string
	StreetAddress string
	GHNDistrictID int
	GHNWardCode   string
}

// ShipmentRequest contains parameters to create a shipment with a carrier.
type ShipmentRequest struct {
	OrderID      string
	ShopID       string
	ServiceCode  string
	CODAmountVND int64
	WeightGrams  int32
	FromAddress  Address
	ToAddress    Address
	ItemsSummary string
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
}

// MapCarrierStatus maps raw provider statuses (GHN, Mock Express, or canonical) to internal statuses.
func MapCarrierStatus(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "created", "new":
		return StatusCreated, true
	case "ready_to_pick", "picking", "money_collect_picking", "picked":
		return StatusPicking, true
	case "storing", "transporting", "sorting", "delivering", "money_collect_delivering", "in_transit", "out_for_delivery", "shipping":
		return StatusDelivering, true
	case "delivered", "completed":
		return StatusDelivered, true
	case "delivery_fail", "failed", "delivery_failed", "exception", "damage", "lost":
		return StatusFailed, true
	case "waiting_to_return", "return", "return_transporting", "return_sorting", "returning":
		return StatusReturning, true
	case "returned", "return_fail":
		return StatusReturned, true
	case "cancel", "cancelled", "canceled":
		return StatusCancelled, true
	default:
		return "", false
	}
}

// IsCancellableStatus reports whether a shipment in status can still be cancelled before pickup.
func IsCancellableStatus(status string) bool {
	return status == StatusCreated || status == StatusPicking
}

// IsTerminalStatus reports whether a shipment has reached a terminal state.
func IsTerminalStatus(status string) bool {
	return status == StatusDelivered || status == StatusReturned || status == StatusCancelled
}
