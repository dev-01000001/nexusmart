package statemachine

import (
	"errors"
	"fmt"
	"strings"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
)

// ErrInvalidReturnTransition is returned whenever a return request transition violates the return state machine.
var ErrInvalidReturnTransition = errors.New("invalid return status transition")

var allowedReturnTransitions = map[orderv1.ReturnStatus]map[orderv1.ReturnStatus]struct{}{
	orderv1.ReturnStatus_RETURN_STATUS_PENDING: {
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:         {},
		orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT: {},
		orderv1.ReturnStatus_RETURN_STATUS_REJECTED:          {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT: {
		orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT: {},
		orderv1.ReturnStatus_RETURN_STATUS_RECEIVED:   {},
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED:  {},
		orderv1.ReturnStatus_RETURN_STATUS_CLOSED:     {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT: {
		orderv1.ReturnStatus_RETURN_STATUS_RECEIVED:  {},
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED: {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_RECEIVED: {
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDING: {},
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED: {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_REJECTED: {
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED: {},
		orderv1.ReturnStatus_RETURN_STATUS_CLOSED:    {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_ESCALATED: {
		orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED: {},
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:          {},
		orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:  {},
		orderv1.ReturnStatus_RETURN_STATUS_CLOSED:             {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED: {
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED: {},
		orderv1.ReturnStatus_RETURN_STATUS_CLOSED:    {},
	},
	orderv1.ReturnStatus_RETURN_STATUS_REFUNDING: {
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDED: {},
	},
}

// CanReturnTransition reports whether from -> to is permitted for return_requests.status.
func CanReturnTransition(from, to orderv1.ReturnStatus) bool {
	next, ok := allowedReturnTransitions[from]
	if !ok {
		return false
	}
	_, valid := next[to]
	return valid
}

// ValidateReturnTransition checks whether from -> to is valid and returns a wrapped ErrInvalidReturnTransition otherwise.
func ValidateReturnTransition(from, to orderv1.ReturnStatus) error {
	if !CanReturnTransition(from, to) {
		return fmt.Errorf("%w: cannot transition return from %s to %s", ErrInvalidReturnTransition, ReturnStatusToString(from), ReturnStatusToString(to))
	}
	return nil
}

// IsTerminalReturnStatus reports whether a return request status is terminal (REFUNDED or CLOSED).
func IsTerminalReturnStatus(s orderv1.ReturnStatus) bool {
	return s == orderv1.ReturnStatus_RETURN_STATUS_REFUNDED || s == orderv1.ReturnStatus_RETURN_STATUS_CLOSED
}

// ReturnStatusToString converts ReturnStatus to its canonical persisted uppercase name.
func ReturnStatusToString(s orderv1.ReturnStatus) string {
	switch s {
	case orderv1.ReturnStatus_RETURN_STATUS_PENDING:
		return "PENDING"
	case orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:
		return "AWAITING_SHIPMENT"
	case orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT:
		return "IN_TRANSIT"
	case orderv1.ReturnStatus_RETURN_STATUS_RECEIVED:
		return "RECEIVED"
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:
		return "REFUNDING"
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDED:
		return "REFUNDED"
	case orderv1.ReturnStatus_RETURN_STATUS_REJECTED:
		return "REJECTED"
	case orderv1.ReturnStatus_RETURN_STATUS_ESCALATED:
		return "ESCALATED"
	case orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED:
		return "EVIDENCE_REQUESTED"
	case orderv1.ReturnStatus_RETURN_STATUS_CLOSED:
		return "CLOSED"
	default:
		return "UNSPECIFIED"
	}
}

// StringToReturnStatus converts a persisted/API string status into its proto ReturnStatus enum.
func StringToReturnStatus(raw string) orderv1.ReturnStatus {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.TrimPrefix(normalized, "RETURN_STATUS_")
	switch normalized {
	case "PENDING":
		return orderv1.ReturnStatus_RETURN_STATUS_PENDING
	case "AWAITING_SHIPMENT":
		return orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT
	case "IN_TRANSIT":
		return orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT
	case "RECEIVED":
		return orderv1.ReturnStatus_RETURN_STATUS_RECEIVED
	case "REFUNDING":
		return orderv1.ReturnStatus_RETURN_STATUS_REFUNDING
	case "REFUNDED":
		return orderv1.ReturnStatus_RETURN_STATUS_REFUNDED
	case "REJECTED":
		return orderv1.ReturnStatus_RETURN_STATUS_REJECTED
	case "ESCALATED":
		return orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
	case "EVIDENCE_REQUESTED":
		return orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED
	case "CLOSED":
		return orderv1.ReturnStatus_RETURN_STATUS_CLOSED
	default:
		return orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED
	}
}
