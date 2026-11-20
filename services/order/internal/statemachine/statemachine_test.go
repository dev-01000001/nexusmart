package statemachine

import (
	"errors"
	"testing"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
)

func TestStateMachine_ExhaustiveTransitions(t *testing.T) {
	allStatuses := []orderv1.OrderStatus{
		orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED,
		orderv1.OrderStatus_ORDER_STATUS_PENDING,
		orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
		orderv1.OrderStatus_ORDER_STATUS_PREPARING,
		orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNED,
	}

	type edge struct {
		from orderv1.OrderStatus
		to   orderv1.OrderStatus
	}

	expectedValid := map[edge]bool{
		{orderv1.OrderStatus_ORDER_STATUS_PENDING, orderv1.OrderStatus_ORDER_STATUS_CONFIRMED}:          true,
		{orderv1.OrderStatus_ORDER_STATUS_PENDING, orderv1.OrderStatus_ORDER_STATUS_CANCELLED}:          true,
		{orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, orderv1.OrderStatus_ORDER_STATUS_PREPARING}:        true,
		{orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, orderv1.OrderStatus_ORDER_STATUS_SHIPPING}:         true,
		{orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, orderv1.OrderStatus_ORDER_STATUS_CANCELLED}:        true,
		{orderv1.OrderStatus_ORDER_STATUS_PREPARING, orderv1.OrderStatus_ORDER_STATUS_SHIPPING}:         true,
		{orderv1.OrderStatus_ORDER_STATUS_PREPARING, orderv1.OrderStatus_ORDER_STATUS_CANCELLED}:        true,
		{orderv1.OrderStatus_ORDER_STATUS_SHIPPING, orderv1.OrderStatus_ORDER_STATUS_DELIVERED}:         true,
		{orderv1.OrderStatus_ORDER_STATUS_SHIPPING, orderv1.OrderStatus_ORDER_STATUS_RETURNED}:          true,
		{orderv1.OrderStatus_ORDER_STATUS_DELIVERED, orderv1.OrderStatus_ORDER_STATUS_COMPLETED}:        true,
		{orderv1.OrderStatus_ORDER_STATUS_DELIVERED, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED}: true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED, orderv1.OrderStatus_ORDER_STATUS_RETURNED}:  true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED, orderv1.OrderStatus_ORDER_STATUS_COMPLETED}: true,
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := expectedValid[edge{from: from, to: to}]
			got := CanTransition(from, to)
			if got != want {
				t.Errorf("CanTransition(%s -> %s) = %v, want %v", StatusToString(from), StatusToString(to), got, want)
			}

			err := ValidateTransition(from, to)
			if want && err != nil {
				t.Errorf("ValidateTransition(%s -> %s) unexpected error: %v", StatusToString(from), StatusToString(to), err)
			}
			if !want {
				if err == nil {
					t.Errorf("ValidateTransition(%s -> %s) expected error, got nil", StatusToString(from), StatusToString(to))
				} else if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("ValidateTransition(%s -> %s) error %v does not wrap ErrInvalidTransition", StatusToString(from), StatusToString(to), err)
				}
			}
		}
	}
}

func TestCanCancel(t *testing.T) {
	cancellable := map[orderv1.OrderStatus]bool{
		orderv1.OrderStatus_ORDER_STATUS_PENDING:   true,
		orderv1.OrderStatus_ORDER_STATUS_CONFIRMED: true,
		orderv1.OrderStatus_ORDER_STATUS_PREPARING: true,
	}

	all := []orderv1.OrderStatus{
		orderv1.OrderStatus_ORDER_STATUS_PENDING,
		orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
		orderv1.OrderStatus_ORDER_STATUS_PREPARING,
		orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNED,
	}

	for _, s := range all {
		if got := CanCancel(s); got != cancellable[s] {
			t.Errorf("CanCancel(%s) = %v, want %v", StatusToString(s), got, cancellable[s])
		}
	}
}

func TestStatusAndPaymentMethodRoundTrip(t *testing.T) {
	statuses := []orderv1.OrderStatus{
		orderv1.OrderStatus_ORDER_STATUS_PENDING,
		orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
		orderv1.OrderStatus_ORDER_STATUS_PREPARING,
		orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNED,
	}
	for _, s := range statuses {
		str := StatusToString(s)
		if back := StringToStatus(str); back != s {
			t.Errorf("round-trip status %v -> %q -> %v", s, str, back)
		}
		if back := StringToStatus("ORDER_STATUS_" + str); back != s {
			t.Errorf("prefixed status %q -> %v, want %v", str, back, s)
		}
	}

	if got := PaymentMethodToString(orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD); got != "COD" {
		t.Fatalf("got %q, want COD", got)
	}
	if got := StringToPaymentMethod("vietqr"); got != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR {
		t.Fatalf("got %v, want VIETQR", got)
	}
}
