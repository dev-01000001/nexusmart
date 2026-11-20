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

// PaymentMethodToString converts OrderPaymentMethod to canonical DB string.
func PaymentMethodToString(m orderv1.OrderPaymentMethod) string {
	switch m {
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD:
		return "COD"
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR:
		return "VIETQR"
	default:
		return "UNSPECIFIED"
	}
}

// StringToPaymentMethod converts canonical DB string to OrderPaymentMethod.
func StringToPaymentMethod(raw string) orderv1.OrderPaymentMethod {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "ORDER_PAYMENT_METHOD_")
	switch normalized {
	case "COD":
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD
	case "VIETQR":
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR
	default:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED
	}
}
