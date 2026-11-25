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
		{orderv1.OrderStatus_ORDER_STATUS_RETURNED, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED}:  true,
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
	if StatusToString(orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED) != "UNSPECIFIED" {
		t.Fatalf("expected UNSPECIFIED")
	}
	if StringToStatus("UNKNOWN") != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED {
		t.Fatalf("expected ORDER_STATUS_UNSPECIFIED")
	}

	methods := []struct {
		m      orderv1.OrderPaymentMethod
		str    string
		online bool
	}{
		{orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD, "COD", false},
		{orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR, "VIETQR", true},
		{orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY, "ZALOPAY", true},
	}
	for _, tc := range methods {
		if got := PaymentMethodToString(tc.m); got != tc.str {
			t.Fatalf("PaymentMethodToString(%v) = %q, want %q", tc.m, got, tc.str)
		}
		if got := StringToPaymentMethod(tc.str); got != tc.m {
			t.Fatalf("StringToPaymentMethod(%q) = %v, want %v", tc.str, got, tc.m)
		}
		if got := StringToPaymentMethod("order_payment_method_" + tc.str); got != tc.m {
			t.Fatalf("prefixed StringToPaymentMethod(%q) = %v, want %v", tc.str, got, tc.m)
		}
		if got := IsOnlineMethod(tc.str); got != tc.online {
			t.Fatalf("IsOnlineMethod(%q) = %v, want %v", tc.str, got, tc.online)
		}
	}
	if PaymentMethodToString(orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED) != "UNSPECIFIED" {
		t.Fatalf("expected UNSPECIFIED for unknown payment method")
	}
	if StringToPaymentMethod("UNKNOWN") != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
		t.Fatalf("expected UNSPECIFIED for unknown payment string")
	}
	if got := PaymentMethodToString(orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY); got != "ZALOPAY" {
		t.Fatalf("got %q, want ZALOPAY", got)
	}
	if got := StringToPaymentMethod("zalopay"); got != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("got %v, want ZALOPAY", got)
	}
}

func TestPaymentMethodHelpers_OnlineAndValid(t *testing.T) {
	cases := []struct {
		raw      string
		norm     string
		valid    bool
		isOnline bool
		isCOD    bool
	}{
		{"COD", "COD", true, false, true},
		{" cod ", "COD", true, false, true},
		{"ORDER_PAYMENT_METHOD_COD", "COD", true, false, true},
		{"PAYMENT_METHOD_COD", "COD", true, false, true},
		{"VIETQR", "VIETQR", true, true, false},
		{"vietqr", "VIETQR", true, true, false},
		{"ORDER_PAYMENT_METHOD_VIETQR", "VIETQR", true, true, false},
		{"ZALOPAY", "ZALOPAY", true, true, false},
		{" zalopay ", "ZALOPAY", true, true, false},
		{"ORDER_PAYMENT_METHOD_ZALOPAY", "ZALOPAY", true, true, false},
		{"PAYMENT_METHOD_ZALOPAY", "ZALOPAY", true, true, false},
		{"UNSPECIFIED", "UNSPECIFIED", false, false, false},
		{"", "", false, false, false},
		{"CRYPTO", "CRYPTO", false, false, false},
	}

	for _, tc := range cases {
		if got := NormalizePaymentMethod(tc.raw); got != tc.norm {
			t.Errorf("NormalizePaymentMethod(%q) = %q, want %q", tc.raw, got, tc.norm)
		}
		if got := IsValidPaymentMethod(tc.raw); got != tc.valid {
			t.Errorf("IsValidPaymentMethod(%q) = %v, want %v", tc.raw, got, tc.valid)
		}
		if got := IsOnlineMethod(tc.raw); got != tc.isOnline {
			t.Errorf("IsOnlineMethod(%q) = %v, want %v", tc.raw, got, tc.isOnline)
		}
		if got := IsCODMethod(tc.raw); got != tc.isCOD {
			t.Errorf("IsCODMethod(%q) = %v, want %v", tc.raw, got, tc.isCOD)
		}
	}
}
