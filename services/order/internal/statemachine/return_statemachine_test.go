package statemachine

import (
	"errors"
	"testing"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
)

func TestReturnStateMachine_ExhaustiveTransitions(t *testing.T) {
	allStatuses := []orderv1.ReturnStatus{
		orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED,
		orderv1.ReturnStatus_RETURN_STATUS_PENDING,
		orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
		orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT,
		orderv1.ReturnStatus_RETURN_STATUS_RECEIVED,
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		orderv1.ReturnStatus_RETURN_STATUS_REJECTED,
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED,
		orderv1.ReturnStatus_RETURN_STATUS_CLOSED,
	}

	type edge struct {
		from orderv1.ReturnStatus
		to   orderv1.ReturnStatus
	}

	expectedValid := map[edge]bool{
		{orderv1.ReturnStatus_RETURN_STATUS_PENDING, orderv1.ReturnStatus_RETURN_STATUS_REFUNDING}:            true,
		{orderv1.ReturnStatus_RETURN_STATUS_PENDING, orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT}:    true,
		{orderv1.ReturnStatus_RETURN_STATUS_PENDING, orderv1.ReturnStatus_RETURN_STATUS_REJECTED}:             true,
		{orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT, orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT}: true,
		{orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT, orderv1.ReturnStatus_RETURN_STATUS_RECEIVED}:   true,
		{orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT, orderv1.ReturnStatus_RETURN_STATUS_ESCALATED}:  true,
		{orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT, orderv1.ReturnStatus_RETURN_STATUS_CLOSED}:     true,
		{orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT, orderv1.ReturnStatus_RETURN_STATUS_RECEIVED}:          true,
		{orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT, orderv1.ReturnStatus_RETURN_STATUS_ESCALATED}:         true,
		{orderv1.ReturnStatus_RETURN_STATUS_RECEIVED, orderv1.ReturnStatus_RETURN_STATUS_REFUNDING}:           true,
		{orderv1.ReturnStatus_RETURN_STATUS_RECEIVED, orderv1.ReturnStatus_RETURN_STATUS_ESCALATED}:           true,
		{orderv1.ReturnStatus_RETURN_STATUS_REJECTED, orderv1.ReturnStatus_RETURN_STATUS_ESCALATED}:           true,
		{orderv1.ReturnStatus_RETURN_STATUS_REJECTED, orderv1.ReturnStatus_RETURN_STATUS_CLOSED}:              true,
		{orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED}: true,
		{orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, orderv1.ReturnStatus_RETURN_STATUS_REFUNDING}:          true,
		{orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT}:  true,
		{orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, orderv1.ReturnStatus_RETURN_STATUS_CLOSED}:             true,
		{orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED, orderv1.ReturnStatus_RETURN_STATUS_ESCALATED}: true,
		{orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED, orderv1.ReturnStatus_RETURN_STATUS_CLOSED}:    true,
		{orderv1.ReturnStatus_RETURN_STATUS_REFUNDING, orderv1.ReturnStatus_RETURN_STATUS_REFUNDED}:           true,
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := expectedValid[edge{from: from, to: to}]
			got := CanReturnTransition(from, to)
			if got != want {
				t.Errorf("CanReturnTransition(%s -> %s) = %v, want %v", ReturnStatusToString(from), ReturnStatusToString(to), got, want)
			}
			err := ValidateReturnTransition(from, to)
			if want && err != nil {
				t.Errorf("ValidateReturnTransition(%s -> %s) unexpected err: %v", ReturnStatusToString(from), ReturnStatusToString(to), err)
			}
			if !want {
				if err == nil {
					t.Errorf("ValidateReturnTransition(%s -> %s) expected error, got nil", ReturnStatusToString(from), ReturnStatusToString(to))
				} else if !errors.Is(err, ErrInvalidReturnTransition) {
					t.Errorf("ValidateReturnTransition(%s -> %s) err %v does not wrap ErrInvalidReturnTransition", ReturnStatusToString(from), ReturnStatusToString(to), err)
				}
			}
		}
	}
}

func TestReturnStatusRoundTrip(t *testing.T) {
	statuses := []orderv1.ReturnStatus{
		orderv1.ReturnStatus_RETURN_STATUS_PENDING,
		orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
		orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT,
		orderv1.ReturnStatus_RETURN_STATUS_RECEIVED,
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		orderv1.ReturnStatus_RETURN_STATUS_REJECTED,
		orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED,
		orderv1.ReturnStatus_RETURN_STATUS_CLOSED,
	}
	for _, s := range statuses {
		str := ReturnStatusToString(s)
		if back := StringToReturnStatus(str); back != s {
			t.Errorf("round-trip %v -> %q -> %v", s, str, back)
		}
		if back := StringToReturnStatus("RETURN_STATUS_" + str); back != s {
			t.Errorf("prefixed %q -> %v, want %v", str, back, s)
		}
	}
	if !IsTerminalReturnStatus(orderv1.ReturnStatus_RETURN_STATUS_REFUNDED) || !IsTerminalReturnStatus(orderv1.ReturnStatus_RETURN_STATUS_CLOSED) {
		t.Fatalf("REFUNDED and CLOSED must be terminal")
	}
	if IsTerminalReturnStatus(orderv1.ReturnStatus_RETURN_STATUS_REFUNDING) || IsTerminalReturnStatus(orderv1.ReturnStatus_RETURN_STATUS_PENDING) {
		t.Fatalf("REFUNDING and PENDING must not be terminal")
	}
}
