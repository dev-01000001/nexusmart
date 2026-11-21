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
		orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED: {},
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED: {},
		orderv1.OrderStatus_ORDER_STATUS_RETURNED:        {},
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED:       {},
	},
	orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED: {
		orderv1.OrderStatus_ORDER_STATUS_RETURNING: {},
		orderv1.OrderStatus_ORDER_STATUS_REFUNDED:  {},
		orderv1.OrderStatus_ORDER_STATUS_RETURNED:  {},
	},
	orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED: {
		orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED: {},
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED:       {},
	},
	orderv1.OrderStatus_ORDER_STATUS_RETURNING: {
		orderv1.OrderStatus_ORDER_STATUS_RETURNED: {},
		orderv1.OrderStatus_ORDER_STATUS_REFUNDED: {},
	},
	orderv1.OrderStatus_ORDER_STATUS_RETURNED: {
		orderv1.OrderStatus_ORDER_STATUS_REFUNDED: {},
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
	case orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED:
		return "RETURN_APPROVED"
	case orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED:
		return "RETURN_REJECTED"
	case orderv1.OrderStatus_ORDER_STATUS_RETURNING:
		return "RETURNING"
	case orderv1.OrderStatus_ORDER_STATUS_REFUNDED:
		return "REFUNDED"
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
	case "RETURN_APPROVED":
		return orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED
	case "RETURN_REJECTED":
		return orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED
	case "RETURNING":
		return orderv1.OrderStatus_ORDER_STATUS_RETURNING
	case "REFUNDED":
		return orderv1.OrderStatus_ORDER_STATUS_REFUNDED
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
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY:
		return "ZALOPAY"
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
	case "ZALOPAY":
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY
	default:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED
	}
}

// IsOnlinePaymentMethod reports whether m is a prepaid online payment method (VietQR or ZaloPay).
func IsOnlinePaymentMethod(m orderv1.OrderPaymentMethod) bool {
	return m == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR ||
		m == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY
}

// IsOnlinePaymentMethodString reports whether a persisted payment_method string is an online payment method.
func IsOnlinePaymentMethodString(raw string) bool {
	return IsOnlinePaymentMethod(StringToPaymentMethod(raw))
}

// ReturnRequestStatusToString converts proto ReturnRequestStatus to its canonical uppercase DB string.
func ReturnRequestStatusToString(s orderv1.ReturnRequestStatus) string {
	switch s {
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING:
		return "PENDING"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_APPROVED:
		return "APPROVED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED:
		return "REJECTED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING:
		return "RETURNING"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNED:
		return "RETURNED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED:
		return "REFUNDED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_ESCALATED:
		return "ESCALATED"
	default:
		return "UNSPECIFIED"
	}
}

// StringToReturnRequestStatus converts a persisted string status into proto ReturnRequestStatus.
func StringToReturnRequestStatus(raw string) orderv1.ReturnRequestStatus {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "RETURN_REQUEST_STATUS_")
	switch normalized {
	case "PENDING":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING
	case "APPROVED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_APPROVED
	case "REJECTED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED
	case "RETURNING":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING
	case "RETURNED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNED
	case "REFUNDED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED
	case "ESCALATED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_ESCALATED
	default:
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_UNSPECIFIED
	}
}

// ComplaintStatusToString converts proto ComplaintStatus to its canonical uppercase DB string.
func ComplaintStatusToString(s orderv1.ComplaintStatus) string {
	switch s {
	case orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN:
		return "OPEN"
	case orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED:
		return "RESOLVED"
	case orderv1.ComplaintStatus_COMPLAINT_STATUS_DISMISSED:
		return "DISMISSED"
	default:
		return "UNSPECIFIED"
	}
}

// StringToComplaintStatus converts a persisted string status into proto ComplaintStatus.
func StringToComplaintStatus(raw string) orderv1.ComplaintStatus {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "COMPLAINT_STATUS_")
	switch normalized {
	case "OPEN":
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN
	case "RESOLVED":
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED
	case "DISMISSED":
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_DISMISSED
	default:
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_UNSPECIFIED
	}
}

// ComplaintResolutionToString converts proto ComplaintResolution to its canonical uppercase DB string.
func ComplaintResolutionToString(r orderv1.ComplaintResolution) string {
	switch r {
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND:
		return "REFUND"
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE:
		return "REPLACE"
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS:
		return "DISMISS"
	default:
		return ""
	}
}

// StringToComplaintResolution converts a persisted string resolution into proto ComplaintResolution.
func StringToComplaintResolution(raw string) orderv1.ComplaintResolution {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "COMPLAINT_RESOLUTION_")
	switch normalized {
	case "REFUND":
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND
	case "REPLACE":
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE
	case "DISMISS":
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS
	default:
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_UNSPECIFIED
	}
}
