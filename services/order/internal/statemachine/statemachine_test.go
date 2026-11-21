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
		orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNING,
		orderv1.OrderStatus_ORDER_STATUS_REFUNDED,
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

		// Extended return & refund state machine edges
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED, orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED}: true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED, orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED}: true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED, orderv1.OrderStatus_ORDER_STATUS_RETURNED}:        true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED, orderv1.OrderStatus_ORDER_STATUS_COMPLETED}:       true,

		{orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED, orderv1.OrderStatus_ORDER_STATUS_RETURNING}: true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED, orderv1.OrderStatus_ORDER_STATUS_REFUNDED}:  true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED, orderv1.OrderStatus_ORDER_STATUS_RETURNED}:  true,

		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED, orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED}: true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED, orderv1.OrderStatus_ORDER_STATUS_COMPLETED}:       true,

		{orderv1.OrderStatus_ORDER_STATUS_RETURNING, orderv1.OrderStatus_ORDER_STATUS_RETURNED}: true,
		{orderv1.OrderStatus_ORDER_STATUS_RETURNING, orderv1.OrderStatus_ORDER_STATUS_REFUNDED}: true,

		{orderv1.OrderStatus_ORDER_STATUS_RETURNED, orderv1.OrderStatus_ORDER_STATUS_REFUNDED}: true,
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
		orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNING,
		orderv1.OrderStatus_ORDER_STATUS_REFUNDED,
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
		orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNING,
		orderv1.OrderStatus_ORDER_STATUS_REFUNDED,
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
		if got := IsOnlinePaymentMethod(tc.m); got != tc.online {
			t.Fatalf("IsOnlinePaymentMethod(%v) = %v, want %v", tc.m, got, tc.online)
		}
		if got := IsOnlinePaymentMethodString(tc.str); got != tc.online {
			t.Fatalf("IsOnlinePaymentMethodString(%q) = %v, want %v", tc.str, got, tc.online)
		}
	}
	if PaymentMethodToString(orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED) != "UNSPECIFIED" {
		t.Fatalf("expected UNSPECIFIED for unknown payment method")
	}
	if StringToPaymentMethod("UNKNOWN") != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
		t.Fatalf("expected UNSPECIFIED for unknown payment string")
	}

	rrStatuses := []orderv1.ReturnRequestStatus{
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING,
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_APPROVED,
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED,
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING,
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNED,
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED,
		orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_ESCALATED,
	}
	for _, s := range rrStatuses {
		str := ReturnRequestStatusToString(s)
		if back := StringToReturnRequestStatus(str); back != s {
			t.Errorf("round-trip return status %v -> %q -> %v", s, str, back)
		}
		if back := StringToReturnRequestStatus("RETURN_REQUEST_STATUS_" + str); back != s {
			t.Errorf("prefixed return status %q -> %v, want %v", str, back, s)
		}
	}
	if ReturnRequestStatusToString(orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_UNSPECIFIED) != "UNSPECIFIED" ||
		StringToReturnRequestStatus("BAD") != orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_UNSPECIFIED {
		t.Fatalf("unexpected unspecified return status mapping")
	}

	cpStatuses := []orderv1.ComplaintStatus{
		orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN,
		orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED,
		orderv1.ComplaintStatus_COMPLAINT_STATUS_DISMISSED,
	}
	for _, s := range cpStatuses {
		str := ComplaintStatusToString(s)
		if back := StringToComplaintStatus(str); back != s {
			t.Errorf("round-trip complaint status %v -> %q -> %v", s, str, back)
		}
	}
	if ComplaintStatusToString(orderv1.ComplaintStatus_COMPLAINT_STATUS_UNSPECIFIED) != "UNSPECIFIED" ||
		StringToComplaintStatus("BAD") != orderv1.ComplaintStatus_COMPLAINT_STATUS_UNSPECIFIED {
		t.Fatalf("unexpected unspecified complaint status mapping")
	}

	cpResolutions := []orderv1.ComplaintResolution{
		orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND,
		orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE,
		orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS,
	}
	for _, r := range cpResolutions {
		str := ComplaintResolutionToString(r)
		if back := StringToComplaintResolution(str); back != r {
			t.Errorf("round-trip complaint resolution %v -> %q -> %v", r, str, back)
		}
	}
	if ComplaintResolutionToString(orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_UNSPECIFIED) != "" ||
		StringToComplaintResolution("BAD") != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_UNSPECIFIED {
		t.Fatalf("unexpected unspecified complaint resolution mapping")
	}
}
