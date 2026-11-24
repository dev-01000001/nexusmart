package statemachine

import (
	"errors"
	"fmt"
	"strings"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
)

// ErrInvalidTransition is returned whenever an order transition violates the state machine.
var ErrInvalidTransition = errors.New("invalid order status transition")

var allowedTransitions = map[orderv1.OrderStatus]map[orderv1.OrderStatus]struct{}{
	orderv1.OrderStatus_ORDER_STATUS_PENDING: {
		orderv1.OrderStatus_ORDER_STATUS_CONFIRMED: {},
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED: {},
	},
	orderv1.OrderStatus_ORDER_STATUS_CONFIRMED: {
		orderv1.OrderStatus_ORDER_STATUS_PREPARING: {},
		orderv1.OrderStatus_ORDER_STATUS_SHIPPING:  {},
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED: {},
	},
	orderv1.OrderStatus_ORDER_STATUS_PREPARING: {
		orderv1.OrderStatus_ORDER_STATUS_SHIPPING:  {},
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED: {},
	},
	orderv1.OrderStatus_ORDER_STATUS_SHIPPING: {
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED: {},
		orderv1.OrderStatus_ORDER_STATUS_RETURNED:  {},
	},
	orderv1.OrderStatus_ORDER_STATUS_DELIVERED: {
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED:        {},
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED: {},
	},
	orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED: {
		orderv1.OrderStatus_ORDER_STATUS_RETURNED:  {},
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED: {},
	},
	// F11: ONLY for reverting an order when a large-refund approval is rejected before refund/compensation ran
	// (return transition REFUNDING -> ESCALATED). Buyer RequestReturn strictly requires DELIVERED.
	orderv1.OrderStatus_ORDER_STATUS_RETURNED: {
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED: {},
	},
}

// CanTransition returns true if transitioning from -> to is permitted by the order state machine.
func CanTransition(from, to orderv1.OrderStatus) bool {
	next, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	_, valid := next[to]
	return valid
}

// ValidateTransition checks whether from -> to is valid and returns a wrapped ErrInvalidTransition otherwise.
func ValidateTransition(from, to orderv1.OrderStatus) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, StatusToString(from), StatusToString(to))
	}
	return nil
}

// CanCancel reports whether an order in the given status can be cancelled.
func CanCancel(status orderv1.OrderStatus) bool {
	return CanTransition(status, orderv1.OrderStatus_ORDER_STATUS_CANCELLED)
}

// StatusToString converts a proto OrderStatus to its canonical persisted uppercase name.
func StatusToString(s orderv1.OrderStatus) string {
	switch s {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING:
		return "PENDING"
	case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED:
		return "CONFIRMED"
	case orderv1.OrderStatus_ORDER_STATUS_PREPARING:
		return "PREPARING"
	case orderv1.OrderStatus_ORDER_STATUS_SHIPPING:
		return "SHIPPING"
	case orderv1.OrderStatus_ORDER_STATUS_DELIVERED:
		return "DELIVERED"
	case orderv1.OrderStatus_ORDER_STATUS_COMPLETED:
		return "COMPLETED"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "CANCELLED"
	case orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED:
		return "RETURN_REQUESTED"
	case orderv1.OrderStatus_ORDER_STATUS_RETURNED:
		return "RETURNED"
	default:
		return "UNSPECIFIED"
	}
}

// StringToStatus converts a persisted/API string status into its proto OrderStatus enum.
func StringToStatus(raw string) orderv1.OrderStatus {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "ORDER_STATUS_")
	switch normalized {
	case "PENDING":
		return orderv1.OrderStatus_ORDER_STATUS_PENDING
	case "CONFIRMED":
		return orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
	case "PREPARING":
		return orderv1.OrderStatus_ORDER_STATUS_PREPARING
	case "SHIPPING":
		return orderv1.OrderStatus_ORDER_STATUS_SHIPPING
	case "DELIVERED":
		return orderv1.OrderStatus_ORDER_STATUS_DELIVERED
	case "COMPLETED":
		return orderv1.OrderStatus_ORDER_STATUS_COMPLETED
	case "CANCELLED":
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	case "RETURN_REQUESTED":
		return orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED
	case "RETURNED":
		return orderv1.OrderStatus_ORDER_STATUS_RETURNED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

// Canonical persisted payment method names (string-based core for COD, VIETQR, and ZALOPAY).
const (
	PaymentMethodCOD     = "COD"
	PaymentMethodVIETQR  = "VIETQR"
	PaymentMethodZALOPAY = "ZALOPAY"
)

// NormalizePaymentMethod normalizes a persisted or API payment method string into its canonical uppercase form.
func NormalizePaymentMethod(raw string) string {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "ORDER_PAYMENT_METHOD_")
	normalized = strings.TrimPrefix(normalized, "PAYMENT_METHOD_")
	return normalized
}

// IsValidPaymentMethod reports whether method is a supported payment method (COD, VIETQR, or ZALOPAY).
func IsValidPaymentMethod(method string) bool {
	switch NormalizePaymentMethod(method) {
	case PaymentMethodCOD, PaymentMethodVIETQR, PaymentMethodZALOPAY:
		return true
	default:
		return false
	}
}

// IsOnlineMethod reports whether method is a pre-paid online payment method (VIETQR or ZALOPAY).
func IsOnlineMethod(method string) bool {
	switch NormalizePaymentMethod(method) {
	case PaymentMethodVIETQR, PaymentMethodZALOPAY:
		return true
	default:
		return false
	}
}

// IsCODMethod reports whether method is cash on delivery (COD).
func IsCODMethod(method string) bool {
	return NormalizePaymentMethod(method) == PaymentMethodCOD
}

// PaymentMethodToString converts OrderPaymentMethod to canonical DB string.
func PaymentMethodToString(m orderv1.OrderPaymentMethod) string {
	switch m {
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD:
		return PaymentMethodCOD
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR:
		return PaymentMethodVIETQR
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY:
		return PaymentMethodZALOPAY
	default:
		return "UNSPECIFIED"
	}
}

// StringToPaymentMethod converts canonical DB string to OrderPaymentMethod.
func StringToPaymentMethod(raw string) orderv1.OrderPaymentMethod {
	switch NormalizePaymentMethod(raw) {
	case PaymentMethodCOD:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD
	case PaymentMethodVIETQR:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR
	case PaymentMethodZALOPAY:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY
	default:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED
	}
}

// ReturnRefundIdempotencyKey builds the deterministic CreateRefund idempotency key for a return refund attempt.
func ReturnRefundIdempotencyKey(orderID string, attempt int32) string {
	if attempt <= 0 {
		return "order-return-refund:" + orderID
	}
	return fmt.Sprintf("order-return-refund:%s:r%d", orderID, attempt)
}

// ReturnRefundMatchResult classifies how a payment.refunded / payment.refund_rejected event correlates to a return request.
type ReturnRefundMatchResult int

const (
	// ReturnRefundMatchNone indicates the event belongs to a different refund (e.g., unrelated manual admin refund).
	ReturnRefundMatchNone ReturnRefundMatchResult = iota
	// ReturnRefundMatchMatched indicates the event strictly matched the return's saved refund_id or expected idempotency_key.
	ReturnRefundMatchMatched
	// ReturnRefundMatchMissingKeys indicates the event lacked correlation keys (e.g., legacy payload missing idempotency_key
	// before refund_id was persisted, or missing both keys); caller must log WARN and ignore without guessing.
	ReturnRefundMatchMissingKeys
)

// MatchReturnRefundEvent matches a payment refund event against a REFUNDING return request strictly by
// saved refund_id (when persisted) or by idempotency_key == ReturnRefundIdempotencyKey(orderID, refundAttempt)
// (when refund_id has not been persisted yet). Never guesses by amount_vnd or payment_id.
func MatchReturnRefundEvent(orderID, savedRefundID string, refundAttempt int32, eventRefundID, eventIdempotencyKey string) ReturnRefundMatchResult {
	savedRef := strings.TrimSpace(savedRefundID)
	evRef := strings.TrimSpace(eventRefundID)
	evKey := strings.TrimSpace(eventIdempotencyKey)

	if savedRef != "" {
		if evRef != "" {
			if evRef == savedRef {
				return ReturnRefundMatchMatched
			}
			return ReturnRefundMatchNone
		}
		if evKey == "" {
			return ReturnRefundMatchMissingKeys
		}
		return ReturnRefundMatchNone
	}

	if evKey == "" {
		return ReturnRefundMatchMissingKeys
	}
	if evKey == ReturnRefundIdempotencyKey(orderID, refundAttempt) {
		return ReturnRefundMatchMatched
	}
	return ReturnRefundMatchNone
}
