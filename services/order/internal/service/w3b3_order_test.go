package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func registerBuyerAddr(h *testHarness, addrID, userID, name, phone, provCode, provName, wardCode, wardName, street string) string {
	h.user.mu.Lock()
	defer h.user.mu.Unlock()
	h.user.addresses[addrID] = &userv1.Address{
		Id:             addrID,
		UserId:         userID,
		RecipientName:  name,
		RecipientPhone: phone,
		ProvinceCode:   provCode,
		ProvinceName:   provName,
		WardCode:       wardCode,
		WardName:       wardName,
		StreetAddress:  street,
	}
	return addrID
}

func createTestCODOrder(t *testing.T, h *testHarness, buyerID, idemKey string) *orderv1.Order {
	t.Helper()
	resp, err := h.svc.CreateCheckout(ctxWithUser(buyerID, "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: idemKey,
		BuyerId:        buyerID,
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout COD: %v", err)
	}
	return resp.GetOrders()[0]
}

func createTestPaidVietQROrder(t *testing.T, h *testHarness, buyerID, idemKey string) *orderv1.Order {
	t.Helper()
	resp, err := h.svc.CreateCheckout(ctxWithUser(buyerID, "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: idemKey,
		BuyerId:        buyerID,
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout VIETQR: %v", err)
	}
	if err := h.svc.HandlePaymentResult(context.Background(), PaymentResultInput{
		EventID:         "ev-paid-" + idemKey,
		CheckoutGroupID: resp.GetCheckoutGroupId(),
		PaymentID:       resp.GetPaymentId(),
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("HandlePaymentResult SUCCEEDED: %v", err)
	}
	got, err := h.svc.GetOrder(ctxWithUser(buyerID, "buyer", ""), &orderv1.GetOrderRequest{OrderId: resp.GetOrders()[0].GetId()})
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	return got.GetOrder()
}

func countAddressUpdatedOutbox(t *testing.T, st *store.MemoryStore) []events.OrderAddressUpdatedPayload {
	t.Helper()
	var out []events.OrderAddressUpdatedPayload
	for _, ev := range st.OutboxEvents() {
		if ev.EventType != events.SubjectOrderAddressUpdated {
			continue
		}
		var p events.OrderAddressUpdatedPayload
		if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
			t.Fatalf("unmarshal order.address_updated outbox: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// --- G7: LookupOrder ---

func TestW3B3_LookupOrder_SuccessPhoneVariantsAndPIIRedaction(t *testing.T) {
	h := newTestHarness()
	registerBuyerAddr(h, "addr-buyer-1", "buyer-1",
		"Nguyen Van Hung", "0901234567",
		"79", "TP. Ho Chi Minh", "26734", "Phuong Ben Nghe", "99 Secret Nguyen Hue Street")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-lookup-1")

	shipResp, err := h.svc.SellerShipOrder(ctxWithUser("seller-1", "seller", "shop-1"), &orderv1.SellerShipOrderRequest{
		OrderId: ord.GetId(),
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	phoneVariants := []string{
		"0901234567",
		"+84901234567",
		"+84 901-234-567",
		"0084901234567",
		"84901234567",
		"0901 234 567",
	}
	for _, ph := range phoneVariants {
		resp, err := h.svc.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{
			OrderCode: ord.GetOrderCode(),
			Phone:     ph,
		})
		if err != nil {
			t.Fatalf("LookupOrder(%q): %v", ph, err)
		}
		sum := resp.GetOrder()
		if sum.GetOrderCode() != ord.GetOrderCode() {
			t.Fatalf("order_code = %q, want %q", sum.GetOrderCode(), ord.GetOrderCode())
		}
		if sum.GetTrackingCode() != shipResp.GetTrackingCode() {
			t.Fatalf("tracking_code = %q, want %q", sum.GetTrackingCode(), shipResp.GetTrackingCode())
		}
		if sum.GetProvinceName() != "TP. Ho Chi Minh" || sum.GetWardName() != "Phuong Ben Nghe" {
			t.Fatalf("province/ward = %q/%q", sum.GetProvinceName(), sum.GetWardName())
		}
		if sum.GetRecipientNameMasked() != "N*** H***" {
			t.Fatalf("recipient_name_masked = %q, want %q", sum.GetRecipientNameMasked(), "N*** H***")
		}
		if sum.GetRecipientPhoneMasked() != "090***4567" {
			t.Fatalf("recipient_phone_masked = %q, want %q", sum.GetRecipientPhoneMasked(), "090***4567")
		}
		if len(sum.GetTimeline()) == 0 {
			t.Fatalf("expected timeline events")
		}

		rawJSON, err := protojson.Marshal(resp)
		if err != nil {
			t.Fatalf("protojson.Marshal: %v", err)
		}
		body := string(rawJSON)
		for _, forbidden := range []string{
			"99 Secret Nguyen Hue Street",
			"Nguyen Van Hung",
			"0901234567",
			"buyer-1",
			"labels.sandbox",
			"Shipment created via",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("LookupOrder leaked forbidden string %q in response: %s", forbidden, body)
			}
		}
	}
}

func TestW3B3_LookupOrder_AntiEnumerationAndAuth(t *testing.T) {
	h := newTestHarness()
	ord := createTestCODOrder(t, h, "buyer-1", "idem-lookup-enum")

	if _, err := h.svc.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{OrderCode: "", Phone: "0901234567"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty order_code err = %v, want InvalidArgument", err)
	}
	if _, err := h.svc.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{OrderCode: ord.GetOrderCode(), Phone: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty phone err = %v, want InvalidArgument", err)
	}

	svcCtx := clients.WithInternalAdminContext(context.Background())
	if _, err := h.svc.LookupOrder(svcCtx, &orderv1.LookupOrderRequest{OrderCode: ord.GetOrderCode(), Phone: "0901234567"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token err = %v, want PermissionDenied", err)
	}

	_, errUnknown := h.svc.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{OrderCode: "NM-20990101-NOPE", Phone: "0901234567"})
	_, errWrongPhone := h.svc.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{OrderCode: ord.GetOrderCode(), Phone: "0999999999"})
	_, errMalformed := h.svc.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{OrderCode: ord.GetOrderCode(), Phone: "not-a-phone"})

	for name, e := range map[string]error{"unknown": errUnknown, "wrong_phone": errWrongPhone, "malformed": errMalformed} {
		if status.Code(e) != codes.NotFound {
			t.Fatalf("%s code = %v, want NotFound", name, status.Code(e))
		}
		if !strings.Contains(e.Error(), "ORDER_LOOKUP_NOT_FOUND") {
			t.Fatalf("%s msg = %q, want ORDER_LOOKUP_NOT_FOUND", name, e.Error())
		}
	}
	if status.Convert(errUnknown).Message() != status.Convert(errWrongPhone).Message() ||
		status.Convert(errUnknown).Message() != status.Convert(errMalformed).Message() {
		t.Fatalf("anti-enumeration broken: unknown=%q wrong=%q malformed=%q",
			errUnknown.Error(), errWrongPhone.Error(), errMalformed.Error())
	}
}

// --- G1-G2: UpdateOrderAddress RBAC, Address-Book Only (C), Status, Prepaid, COD Pricing, Idempotency ---

func TestW3B3_UpdateOrderAddress_RBACAddressBookAndStatusGuards(t *testing.T) {
	h := newTestHarness()
	ord := createTestCODOrder(t, h, "buyer-1", "idem-addr-rbac")
	addr2 := registerBuyerAddr(h, "addr-buyer-2", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26740", "Phuong Da Kao", "45 Dien Bien Phu")

	// Reject buyer B, seller, admin, super_admin, service token.
	deniedCallers := []struct {
		name string
		ctx  context.Context
	}{
		{"buyer_b", ctxWithUser("buyer-2", "buyer", "")},
		{"seller", ctxWithUser("seller-1", "seller", "shop-1")},
		{"admin", ctxWithUser("admin-1", "admin", "")},
		{"super_admin", ctxWithUser("sadmin-1", "super_admin", "")},
		{"service_token", clients.WithInternalAdminContext(context.Background())},
	}
	for _, tc := range deniedCallers {
		_, err := h.svc.UpdateOrderAddress(tc.ctx, &orderv1.UpdateOrderAddressRequest{
			OrderId:   ord.GetId(),
			AddressId: addr2,
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: got %v, want PermissionDenied", tc.name, err)
		}
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// Item C: empty address_id rejected with InvalidArgument; foreign buyer's address_id rejected with PermissionDenied.
	if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty address_id err = %v, want InvalidArgument", err)
	}
	if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: "addr-other"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer's address_id err = %v, want PermissionDenied", err)
	}

	// PENDING online order -> ADDRESS_UPDATE_NOT_ALLOWED.
	pendResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-pend-addr",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout PENDING: %v", err)
	}
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:   pendResp.GetOrders()[0].GetId(),
		AddressId: addr2,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_NOT_ALLOWED") {
		t.Fatalf("PENDING err = %v, want ADDRESS_UPDATE_NOT_ALLOWED", err)
	}

	// pending_shipment_cancel == true and QUEUED -> ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS.
	token, err := h.store.MarkOrderShipmentCreating(context.Background(), ord.GetId(), time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkOrderShipmentCreating: %v", err)
	}
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addr2})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
		t.Fatalf("pending_shipment_cancel err = %v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
	}
	if _, err := h.store.MarkOrderShipmentQueued(context.Background(), store.QueueShipmentParams{
		OrderID:               ord.GetId(),
		JobID:                 "job-q-1",
		ExpectedCreatingToken: token,
		ActorID:               "seller-1",
		ActorRole:             "seller",
	}); err != nil {
		t.Fatalf("MarkOrderShipmentQueued: %v", err)
	}
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addr2})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
		t.Fatalf("QUEUED err = %v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
	}

	// SHIPPING -> ADDRESS_UPDATE_NOT_ALLOWED.
	ordShip := createTestCODOrder(t, h, "buyer-1", "idem-addr-shipped")
	shipped, err := h.svc.SellerShipOrder(ctxWithUser("seller-1", "seller", "shop-1"), &orderv1.SellerShipOrderRequest{
		OrderId: ordShip.GetId(),
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-delivering-addr", events.ShipmentUpdatedPayload{
		OrderID:      ordShip.GetId(),
		Status:       "delivering",
		TrackingCode: shipped.GetTrackingCode(),
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated: %v", err)
	}
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ordShip.GetId(), AddressId: addr2})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_NOT_ALLOWED") {
		t.Fatalf("SHIPPING err = %v, want ADDRESS_UPDATE_NOT_ALLOWED", err)
	}
}

func TestW3B3_UpdateOrderAddress_PrepaidRulesAndVouchersUnchanged(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	ord := createTestPaidVietQROrder(t, h, "buyer-1", "idem-prepaid-addr")
	origFee := ord.GetShippingFeeVnd()
	origTotal := ord.GetTotalPayableVnd()

	addrFar := registerBuyerAddr(h, "addr-far", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00001", "Phuong Hoan Kiem", "10 Trang Tien")
	addrNear := registerBuyerAddr(h, "addr-near", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Phuong Tan Dinh", "22 Hai Ba Trung")

	// 1. Prepaid fee increase -> preview blocked_prepaid_increase=true, apply returns 409 ADDRESS_FEE_INCREASE_PREPAID.
	h.shipping.quotes = []*shippingv1.CarrierQuote{
		{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: origFee + 15000, CodSupported: true},
	}
	prevResp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:     ord.GetId(),
		AddressId:   addrFar,
		PreviewOnly: true,
	})
	if err != nil {
		t.Fatalf("prepaid preview increase: %v", err)
	}
	if !prevResp.GetBlockedPrepaidIncrease() || !prevResp.GetPreviewOnly() || prevResp.GetFeeAdjusted() || prevResp.GetFeeDeltaVnd() != 15000 {
		t.Fatalf("unexpected prepaid preview: %+v", prevResp)
	}
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:           ord.GetId(),
		AddressId:         addrFar,
		AcceptFeeIncrease: true,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_FEE_INCREASE_PREPAID") {
		t.Fatalf("prepaid apply increase err = %v, want ADDRESS_FEE_INCREASE_PREPAID", err)
	}

	// 2. Prepaid fee decrease: order fee/total unchanged, fee_adjusted=false, outbox old_fee_vnd == new_fee_vnd (Point 2).
	h.shipping.quotes = []*shippingv1.CarrierQuote{
		{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: origFee - 10000, CodSupported: true},
	}
	applyResp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:        ord.GetId(),
		AddressId:      addrNear,
		IdempotencyKey: "idem-prepaid-dec-1",
	})
	if err != nil {
		t.Fatalf("prepaid apply decrease: %v", err)
	}
	if applyResp.GetFeeAdjusted() {
		t.Fatalf("prepaid decrease must set fee_adjusted=false")
	}
	if applyResp.GetOrder().GetShippingFeeVnd() != origFee || applyResp.GetOrder().GetTotalPayableVnd() != origTotal {
		t.Fatalf("prepaid totals changed: fee=%d total=%d, want %d/%d",
			applyResp.GetOrder().GetShippingFeeVnd(), applyResp.GetOrder().GetTotalPayableVnd(), origFee, origTotal)
	}
	if len(h.payment.adjustCODCalls) != 0 {
		t.Fatalf("expected 0 AdjustCODShippingFee calls for prepaid order, got %d", len(h.payment.adjustCODCalls))
	}
	outbox := countAddressUpdatedOutbox(t, h.store)
	if len(outbox) != 1 || outbox[0].OldFeeVND != origFee || outbox[0].NewFeeVND != origFee ||
		outbox[0].ShipmentRecreated || outbox[0].ShipmentQueued || outbox[0].TrackingCode != "" {
		t.Fatalf("unexpected prepaid order.address_updated outbox: %+v", outbox)
	}
}

func TestW3B3_UpdateOrderAddress_CODPricingGuardsAndIdempotency(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-cod-addr-1")
	origFee := ord.GetShippingFeeVnd()
	origTotal := ord.GetTotalPayableVnd()
	origComm := ord.GetCommissionVnd()
	origPayFee := ord.GetPaymentFeeVnd()

	addrNew := registerBuyerAddr(h, "addr-hn-1", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00002", "Phuong Trang Tien", "55 Hang Bai")
	addrOther := registerBuyerAddr(h, "addr-hn-2", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00003", "Phuong Hang Dao", "88 Hang Dao")

	// 1. Unsupported COD ward -> COD_AREA_NOT_SUPPORTED.
	h.shipping.quotes = []*shippingv1.CarrierQuote{
		{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 45000, CodSupported: false},
	}
	_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrNew})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
		t.Fatalf("unsupported COD ward err = %v, want COD_AREA_NOT_SUPPORTED", err)
	}

	// 2. COD ceiling exceeded -> COD_LIMIT_EXCEEDED (reusing shared ValidatePaymentMethodAmountBounds, Item E).
	h.shipping.quotes = []*shippingv1.CarrierQuote{
		{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true},
	}
	h.methodConfigs.SetConfig(clients.PaymentMethodConfig{
		Method:      "COD",
		Enabled:     true,
		MaxOrderVND: origTotal + 10000,
	})
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   ord.GetId(),
		AddressId:                 addrNew,
		AcceptFeeIncrease:         true,
		ExpectedNewShippingFeeVnd: 50000,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_LIMIT_EXCEEDED") {
		t.Fatalf("COD ceiling err = %v, want COD_LIMIT_EXCEEDED", err)
	}
	h.methodConfigs.SetConfig(clients.PaymentMethodConfig{Method: "COD", Enabled: true, MaxOrderVND: 5000000})

	// 3. Preview COD increase -> requires_fee_confirmation=true.
	prevResp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:     ord.GetId(),
		AddressId:   addrNew,
		PreviewOnly: true,
	})
	if err != nil {
		t.Fatalf("COD preview: %v", err)
	}
	if !prevResp.GetRequiresFeeConfirmation() || !prevResp.GetFeeAdjusted() || prevResp.GetFeeDeltaVnd() != 20000 || prevResp.GetNewTotalPayableVnd() != origTotal+20000 {
		t.Fatalf("unexpected COD preview: %+v", prevResp)
	}

	// 4. Apply without accept_fee_increase -> ADDRESS_FEE_CONFIRMATION_REQUIRED.
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrNew})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_FEE_CONFIRMATION_REQUIRED") {
		t.Fatalf("missing confirm err = %v, want ADDRESS_FEE_CONFIRMATION_REQUIRED", err)
	}

	// 5. Stale expected_new_shipping_fee_vnd -> ADDRESS_FEE_CHANGED with JSON quote.
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   ord.GetId(),
		AddressId:                 addrNew,
		AcceptFeeIncrease:         true,
		ExpectedNewShippingFeeVnd: 40000,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_FEE_CHANGED") {
		t.Fatalf("stale expected fee err = %v, want ADDRESS_FEE_CHANGED", err)
	}
	msg := status.Convert(err).Message()
	idx := strings.Index(msg, "{")
	if idx < 0 {
		t.Fatalf("ADDRESS_FEE_CHANGED missing JSON quote: %q", msg)
	}
	var quotePayload struct {
		OldShippingFeeVND  int64 `json:"old_shipping_fee_vnd"`
		NewShippingFeeVND  int64 `json:"new_shipping_fee_vnd"`
		FeeDeltaVND        int64 `json:"fee_delta_vnd"`
		NewTotalPayableVND int64 `json:"new_total_payable_vnd"`
	}
	if err := json.Unmarshal([]byte(msg[idx:]), &quotePayload); err != nil {
		t.Fatalf("parse ADDRESS_FEE_CHANGED JSON %q: %v", msg[idx:], err)
	}
	if quotePayload.OldShippingFeeVND != origFee || quotePayload.NewShippingFeeVND != 50000 || quotePayload.FeeDeltaVND != 20000 || quotePayload.NewTotalPayableVND != origTotal+20000 {
		t.Fatalf("unexpected ADDRESS_FEE_CHANGED quote payload: %+v", quotePayload)
	}

	// 6. Apply with exact expected fee & Idempotency-Key -> succeeds, calls Payment.AdjustCODShippingFee with order-service token.
	applyResp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   ord.GetId(),
		AddressId:                 addrNew,
		AcceptFeeIncrease:         true,
		ExpectedNewShippingFeeVnd: 50000,
		IdempotencyKey:            "idem-cod-inc-1",
	})
	if err != nil {
		t.Fatalf("COD apply increase: %v", err)
	}
	if !applyResp.GetFeeAdjusted() || applyResp.GetOrder().GetShippingFeeVnd() != 50000 || applyResp.GetOrder().GetTotalPayableVnd() != origTotal+20000 {
		t.Fatalf("COD totals mismatch: %+v", applyResp)
	}
	if applyResp.GetOrder().GetCommissionVnd() != origComm || applyResp.GetOrder().GetPaymentFeeVnd() != origPayFee {
		t.Fatalf("commission/payment_fee must not change on address update")
	}
	if len(h.payment.adjustCODCalls) != 1 || h.payment.adjustCODAuth[0].service != clients.InternalServiceName {
		t.Fatalf("unexpected AdjustCODShippingFee calls/auth: %+v %+v", h.payment.adjustCODCalls, h.payment.adjustCODAuth)
	}

	// 7. Idempotent replay with same key + same address.
	replayResp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   ord.GetId(),
		AddressId:                 addrNew,
		AcceptFeeIncrease:         true,
		ExpectedNewShippingFeeVnd: 50000,
		IdempotencyKey:            "idem-cod-inc-1",
	})
	if err != nil || replayResp.GetNewShippingFeeVnd() != 50000 || len(h.payment.adjustCODCalls) != 1 {
		t.Fatalf("idempotent replay failed: err=%v calls=%d", err, len(h.payment.adjustCODCalls))
	}
	if len(countAddressUpdatedOutbox(t, h.store)) != 1 {
		t.Fatalf("expected 1 outbox event after replay")
	}

	// 8. Same key + DIFFERENT address -> IDEMPOTENCY_KEY_CONFLICT.
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   ord.GetId(),
		AddressId:                 addrOther,
		AcceptFeeIncrease:         true,
		ExpectedNewShippingFeeVnd: 50000,
		IdempotencyKey:            "idem-cod-inc-1",
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "IDEMPOTENCY_KEY_CONFLICT") {
		t.Fatalf("idem conflict err = %v, want IDEMPOTENCY_KEY_CONFLICT", err)
	}

	// 9. COD decrease (50,000 -> 25,000) without accept_fee_increase -> succeeds.
	h.shipping.quotes = []*shippingv1.CarrierQuote{
		{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 25000, CodSupported: true},
	}
	decResp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:        ord.GetId(),
		AddressId:      addrOther,
		IdempotencyKey: "idem-cod-dec-2",
	})
	if err != nil {
		t.Fatalf("COD decrease: %v", err)
	}
	if !decResp.GetFeeAdjusted() || decResp.GetOrder().GetShippingFeeVnd() != 25000 || decResp.GetOrder().GetTotalPayableVnd() != origTotal-5000 {
		t.Fatalf("COD decrease mismatch: %+v", decResp)
	}
	if len(h.payment.adjustCODCalls) != 2 || h.payment.adjustCODCalls[1].GetNewShippingFeeVnd() != 25000 {
		t.Fatalf("expected 2nd AdjustCODShippingFee=25000, got %+v", h.payment.adjustCODCalls)
	}
}

// --- R2 Item A: Pre-check newTotal < 0 & Step 3 permanent error restores OLD shipment + crash recovery ---

func TestW3B3_R2_ItemA_NegativeTotalPrecheckAndStep3PermanentFailRestoresOldShipment(t *testing.T) {
	t.Run("negative_new_total_rejected_before_cancelling_anything", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		// Order where discounts cover almost everything: subtotal=100k, vouchers=100k, shipping=30k, freeship=20k -> total_payable=10k.
		h.product.skus["sku-1"].PriceVnd = 100000
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                 "shop-1",
					SubtotalVnd:            100000,
					ShopVoucherDiscountVnd: 100000,
					FreeshipDiscountVnd:    20000,
					ShippingFeeVnd:         30000,
				},
			},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-neg-total",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			ShopVoucherIds:    map[string]string{"shop-1": "v-shop-1"},
			FreeshipVoucherId: "v-free-1",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord := co.GetOrders()[0]
		if ord.GetTotalPayableVnd() != 10000 {
			t.Fatalf("seed total_payable_vnd = %d, want 10000", ord.GetTotalPayableVnd())
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		// New shipping fee = 15,000 -> feeDelta = -15,000 -> newTotal = 10,000 - 15,000 = -5,000 < 0!
		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 15000, CodSupported: true}}
		addrCheap := registerBuyerAddr(h, "addr-cheap", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "12 Cheap St")

		_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:   ord.GetId(),
			AddressId: addrCheap,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_NOT_ALLOWED") {
			t.Fatalf("negative newTotal err = %v, want ADDRESS_UPDATE_NOT_ALLOWED", err)
		}
		if len(h.shipping.cancelShipCalls) != 0 {
			t.Fatalf("must NOT cancel existing shipment when newTotal < 0; got %d CancelShipment calls", len(h.shipping.cancelShipCalls))
		}
		if len(h.payment.adjustCODCalls) != 0 {
			t.Fatalf("must NOT call Payment when newTotal < 0")
		}
	})

	for _, crashDuringRestore := range []bool{false, true} {
		name := "step3_permanent_fail_restores_old_shipment_sync"
		if crashDuringRestore {
			name = "step3_permanent_fail_crash_then_watchdog_restores_old_shipment"
		}
		t.Run(name, func(t *testing.T) {
			h := newTestHarness()
			baseTime := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
			var clockMu sync.Mutex
			now := baseTime
			h.svc.nowFn = func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				return now
			}

			buyerCtx := ctxWithUser("buyer-1", "buyer", "")
			sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
			ord := createTestCODOrder(t, h, "buyer-1", "idem-s3-perm")
			origTotal := ord.GetTotalPayableVnd()

			var shipCalls []*shippingv1.CreateShipmentRequest
			var shipMu sync.Mutex
			h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
				shipMu.Lock()
				shipCalls = append(shipCalls, req)
				n := len(shipCalls)
				shipMu.Unlock()
				trk := "OLD-TRK-S3"
				if n > 1 {
					trk = "RESTORED-TRK-S3"
				}
				return &shippingv1.CreateShipmentResponse{
					Shipment: &shippingv1.Shipment{
						OrderId:      req.GetOrderId(),
						CarrierCode:  req.GetCarrierCode(),
						ServiceCode:  req.GetServiceCode(),
						TrackingCode: trk,
						LabelUrl:     "https://labels.sandbox/" + trk,
						CodAmountVnd: req.GetCodAmountVnd(),
					},
				}, nil
			}
			if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
				t.Fatalf("SellerShipOrder: %v", err)
			}

			// Step 3 fails PERMANENTLY after Step 2 cancelled OLD-TRK-S3.
			h.payment.adjustCODErr = status.Error(codes.FailedPrecondition, "COD_ALLOCATION_LOCKED: cannot adjust cod")
			h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
			addrNew := registerBuyerAddr(h, "addr-s3-new", "buyer-1", "New Person", "0912345678", "01", "Ha Noi", "00002", "Trang Tien", "99 New Road")

			var restoreChangeID string
			if crashDuringRestore {
				h.svc.afterAddressChangeStep = func(step int, changeID string) error {
					if step == addressChangeStepRestoreOld {
						restoreChangeID = changeID
						return errors.New("simulated crash before restoring old shipment")
					}
					return nil
				}
			}

			_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
				OrderId:                   ord.GetId(),
				AddressId:                 addrNew,
				AcceptFeeIncrease:         true,
				ExpectedNewShippingFeeVnd: 50000,
				IdempotencyKey:            "idem-s3-perm-req",
			})
			if err == nil {
				t.Fatalf("expected error on Step 3 permanent failure")
			}

			if crashDuringRestore {
				if restoreChangeID == "" {
					t.Fatalf("expected crash hook at addressChangeStepRestoreOld")
				}
				ch, gErr := h.store.GetOrderAddressChangeByID(context.Background(), restoreChangeID)
				if gErr != nil || ch.State != store.AddressChangeStateRestoringShipment {
					t.Fatalf("expected state RESTORING_SHIPMENT after crash, got err=%v state=%q", gErr, ch.State)
				}
				h.svc.afterAddressChangeStep = nil
				clockMu.Lock()
				now = baseTime.Add(35 * time.Second)
				clockMu.Unlock()
				handled, wErr := h.svc.ScanPendingAddressChanges(context.Background())
				if wErr != nil || handled != 1 {
					t.Fatalf("watchdog restore failed: handled=%d err=%v", handled, wErr)
				}
			} else {
				if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_ALLOCATION_LOCKED") {
					t.Fatalf("sync Step 3 permanent error = %v, want FailedPrecondition COD_ALLOCATION_LOCKED", err)
				}
			}

			// Order MUST keep OLD address and OLD fees, have RESTORED-TRK-S3, and intent MUST be ABORTED (no infinite retry!).
			finalAgg, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
			if finalAgg.Order.Status != "PREPARING" || finalAgg.Order.TrackingCode != "RESTORED-TRK-S3" {
				t.Fatalf("old shipment not restored: status=%s trk=%q", finalAgg.Order.Status, finalAgg.Order.TrackingCode)
			}
			if finalAgg.Order.ShippingFeeVND != 30000 || finalAgg.Order.TotalPayableVND != origTotal {
				t.Fatalf("old fees mutated: fee=%d total=%d", finalAgg.Order.ShippingFeeVND, finalAgg.Order.TotalPayableVND)
			}
			shipMu.Lock()
			if len(shipCalls) != 2 {
				shipMu.Unlock()
				t.Fatalf("expected 2 CreateShipment calls (initial + restore), got %d", len(shipCalls))
			}
			restoredReq := shipCalls[1]
			shipMu.Unlock()
			if restoredReq.GetCodAmountVnd() != origTotal ||
				restoredReq.GetToAddress().GetStreetAddress() != "123 Le Loi" ||
				restoredReq.GetToAddress().GetName() != "Nguyen Van A" ||
				restoredReq.GetToAddress().GetPhone() != "0901234567" {
				t.Fatalf("restored shipment did not use OLD address/COD: cod=%d to=%+v", restoredReq.GetCodAmountVnd(), restoredReq.GetToAddress())
			}
			if len(countAddressUpdatedOutbox(t, h.store)) != 0 {
				t.Fatalf("aborted address change must NOT emit order.address_updated")
			}
			// Intent is terminal ABORTED: another watchdog tick claims 0 rows!
			clockMu.Lock()
			now = now.Add(60 * time.Second)
			clockMu.Unlock()
			if again, _ := h.svc.ScanPendingAddressChanges(context.Background()); again != 0 {
				t.Fatalf("expected 0 open intents after restore abort, watchdog claimed %d", again)
			}
		})
	}
}

// --- R2 Item B: Transient Step 5 error returns 200 with shipment_queued=true, watchdog recreates later ---

func TestW3B3_R2_ItemB_Step5TransientErrorReturnsSuccessQueuedAndWatchdogFinishes(t *testing.T) {
	h := newTestHarness()
	baseTime := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	now := baseTime
	h.svc.nowFn = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-itemb")

	var shipAttempt atomic.Int32
	h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		n := shipAttempt.Add(1)
		switch n {
		case 1:
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "OLD-TRK-B"},
			}, nil
		case 2:
			return nil, status.Error(codes.Unavailable, "carrier gateway 503")
		default:
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "NEW-TRK-B-OK", LabelUrl: "https://labels.sandbox/b"},
			}, nil
		}
	}
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 45000, CodSupported: true}}
	addrNew := registerBuyerAddr(h, "addr-b-new", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00002", "Trang Tien", "45 ItemB St")

	resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   ord.GetId(),
		AddressId:                 addrNew,
		AcceptFeeIncrease:         true,
		ExpectedNewShippingFeeVnd: 45000,
		IdempotencyKey:            "idem-itemb-req",
	})
	if err != nil {
		t.Fatalf("Item B: Step 5 transient failure MUST return 200 success (not 503), got err=%v", err)
	}
	if !resp.GetShipmentQueued() || resp.GetShipmentRecreated() {
		t.Fatalf("Item B: expected shipment_queued=true shipment_recreated=false, got %+v", resp)
	}
	if resp.GetOrder().GetShippingAddress().GetStreetAddress() != "45 ItemB St" || resp.GetOrder().GetShippingFeeVnd() != 45000 {
		t.Fatalf("Item B: returned order missing committed address/fee: %+v", resp.GetOrder())
	}

	// Outbox emitted at Step 4 with truthful shipment_queued=true, shipment_recreated=false, tracking_code="" (Point 1).
	outbox := countAddressUpdatedOutbox(t, h.store)
	if len(outbox) != 1 || !outbox[0].ShipmentQueued || outbox[0].ShipmentRecreated || outbox[0].TrackingCode != "" {
		t.Fatalf("Point 1 outbox mismatch: %+v", outbox)
	}

	// Watchdog runs after backoff and recreates the shipment!
	clockMu.Lock()
	now = baseTime.Add(10 * time.Second)
	clockMu.Unlock()
	handled, err := h.svc.ScanPendingAddressChanges(context.Background())
	if err != nil || handled != 1 {
		t.Fatalf("watchdog Step 5 retry failed: handled=%d err=%v", handled, err)
	}
	finalAgg, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
	if finalAgg.Order.TrackingCode != "NEW-TRK-B-OK" || finalAgg.Order.ShipmentJobStatus != "" {
		t.Fatalf("watchdog did not attach new tracking: trk=%q job=%q", finalAgg.Order.TrackingCode, finalAgg.Order.ShipmentJobStatus)
	}
	// Still strictly 1 outbox event (no duplicate at Step 5)!
	if len(countAddressUpdatedOutbox(t, h.store)) != 1 {
		t.Fatalf("expected exactly 1 order.address_updated outbox event, got %d", len(countAddressUpdatedOutbox(t, h.store)))
	}
}

// --- R2 Item D & F: Concurrent empty Idempotency-Key & nil Payment client ---

func TestW3B3_R2_ItemD_ConcurrentEmptyIdempotencyKey_NeverAppliesTwice(t *testing.T) {
	t.Run("same_fee_fast_path_empty_idem_key", func(t *testing.T) {
		h := newTestHarness()
		// Freeze clock to stress-test the exact same-timestamp race!
		frozen := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return frozen }

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-empty-key-fast")
		addrNew := registerBuyerAddr(h, "addr-d-1", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "99 Double Click St")

		// Coordinate both goroutines so BOTH finish CalculateFee before either enters Step 1!
		var arrived atomic.Int32
		bothQuoted := make(chan struct{})
		h.shipping.onCalculateFee = func(_ *shippingv1.CalculateFeeRequest) (*shippingv1.CalculateFeeResponse, error) {
			if arrived.Add(1) == 2 {
				close(bothQuoted)
			} else {
				select {
				case <-bothQuoted:
				case <-time.After(2 * time.Second):
				}
			}
			return &shippingv1.CalculateFeeResponse{
				Quotes: []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: true}},
			}, nil
		}

		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			i := i
			go func() {
				defer wg.Done()
				_, errs[i] = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
					OrderId:        ord.GetId(),
					AddressId:      addrNew,
					IdempotencyKey: "", // Explicitly empty!
				})
			}()
		}
		wg.Wait()

		var okCount, inProgCount int
		for _, e := range errs {
			if e == nil {
				okCount++
			} else if status.Code(e) == codes.FailedPrecondition && strings.Contains(e.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
				inProgCount++
			} else {
				t.Fatalf("unexpected error in empty-key race: %v", e)
			}
		}
		if okCount != 1 || inProgCount != 1 {
			t.Fatalf("Item D: expected 1 success + 1 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS, got ok=%d inProg=%d errs=%v", okCount, inProgCount, errs)
		}
		if len(countAddressUpdatedOutbox(t, h.store)) != 1 {
			t.Fatalf("Item D: expected strictly 1 order.address_updated outbox event, got %d", len(countAddressUpdatedOutbox(t, h.store)))
		}
	})

	t.Run("item_f_nil_payment_client_rejected_with_unavailable", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-nil-pay")
		addrNew := registerBuyerAddr(h, "addr-f-1", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00002", "Trang Tien", "12 NilPay St")
		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
		h.svc.clients.Payment = nil

		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:                   ord.GetId(),
			AddressId:                 addrNew,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVnd: 50000,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("Item F: nil Payment client with feeDelta != 0 got err=%v, want Unavailable", err)
		}
		after, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
		if after.Order.ShippingFeeVND != 30000 {
			t.Fatalf("Item F: order fee mutated despite nil Payment client: %d", after.Order.ShippingFeeVND)
		}
	})
}

// --- G3: Crash Recovery After Each Step 1..4 + Points 1 & 3 ---

func TestW3B3_UpdateOrderAddress_CrashAfterEachStep_WatchdogConverges(t *testing.T) {
	for _, crashStep := range []int{1, 2, 3, 4} {
		t.Run("crash_after_step_"+string(rune('0'+crashStep)), func(t *testing.T) {
			h := newTestHarness()
			baseTime := time.Date(2026, 11, 25, 4, 0, 0, 0, time.UTC)
			var clockMu sync.Mutex
			now := baseTime
			h.svc.nowFn = func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				return now
			}

			buyerCtx := ctxWithUser("buyer-1", "buyer", "")
			sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
			ord := createTestCODOrder(t, h, "buyer-1", "idem-crash-step")

			var shipSeq atomic.Int32
			var lastShipReq *shippingv1.CreateShipmentRequest
			var shipMu sync.Mutex
			h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
				n := shipSeq.Add(1)
				shipMu.Lock()
				lastShipReq = req
				shipMu.Unlock()
				trk := "OLD-TRK-001"
				if n > 1 {
					trk = "NEW-TRK-002"
				}
				return &shippingv1.CreateShipmentResponse{
					Shipment: &shippingv1.Shipment{
						ShipmentId:   "shp-" + trk,
						OrderId:      req.GetOrderId(),
						ShopId:       req.GetShopId(),
						CarrierCode:  req.GetCarrierCode(),
						ServiceCode:  req.GetServiceCode(),
						TrackingCode: trk,
						LabelUrl:     "https://labels.sandbox/" + trk,
						Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
						CodAmountVnd: req.GetCodAmountVnd(),
					},
				}, nil
			}
			shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"})
			if err != nil || shipResp.GetTrackingCode() != "OLD-TRK-001" {
				t.Fatalf("SellerShipOrder: resp=%+v err=%v", shipResp, err)
			}

			appliedCODByIdem := map[string]int64{}
			var currentCODFee int64 = ord.GetShippingFeeVnd()
			h.payment.onAdjustCOD = func(_ context.Context, req *paymentv1.AdjustCODShippingFeeRequest) (*paymentv1.AdjustCODShippingFeeResponse, error) {
				if prev, ok := appliedCODByIdem[req.GetIdempotencyKey()]; ok {
					return &paymentv1.AdjustCODShippingFeeResponse{NewShippingFeeVnd: prev, AlreadyApplied: true}, nil
				}
				appliedCODByIdem[req.GetIdempotencyKey()] = req.GetNewShippingFeeVnd()
				currentCODFee = req.GetNewShippingFeeVnd()
				return &paymentv1.AdjustCODShippingFeeResponse{NewShippingFeeVnd: currentCODFee}, nil
			}

			h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
			addrNew := registerBuyerAddr(h, "addr-crash-new", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00009", "Phuong Ba Dinh", "77 Hung Vuong")

			var crashedChangeID string
			h.svc.afterAddressChangeStep = func(step int, changeID string) error {
				if step == crashStep {
					crashedChangeID = changeID
					return errors.New("simulated pod crash")
				}
				return nil
			}

			_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
				OrderId:                   ord.GetId(),
				AddressId:                 addrNew,
				AcceptFeeIncrease:         true,
				ExpectedNewShippingFeeVnd: 50000,
				IdempotencyKey:            "idem-crash-recovery",
			})
			if err == nil || crashedChangeID == "" {
				t.Fatalf("expected simulated crash at step %d, got err=%v changeID=%q", crashStep, err, crashedChangeID)
			}

			h.svc.afterAddressChangeStep = nil
			clockMu.Lock()
			now = baseTime.Add(35 * time.Second)
			clockMu.Unlock()

			handled, err := h.svc.ScanPendingAddressChanges(context.Background())
			if err != nil || handled != 1 {
				t.Fatalf("ScanPendingAddressChanges: handled=%d err=%v", handled, err)
			}

			finalAgg, err := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
			if err != nil {
				t.Fatalf("GetOrderWithDetails: %v", err)
			}
			wantTotal := ord.GetTotalPayableVnd() + 20000
			if finalAgg.Order.ShippingFeeVND != 50000 || finalAgg.Order.TotalPayableVND != wantTotal {
				t.Fatalf("order fees did not converge: fee=%d total=%d, want 50000/%d",
					finalAgg.Order.ShippingFeeVND, finalAgg.Order.TotalPayableVND, wantTotal)
			}
			if finalAgg.Order.TrackingCode != "NEW-TRK-002" || finalAgg.Order.Status != "PREPARING" {
				t.Fatalf("order shipment did not converge: status=%s trk=%q", finalAgg.Order.Status, finalAgg.Order.TrackingCode)
			}
			if currentCODFee != 50000 {
				t.Fatalf("payment COD fee = %d, want 50000", currentCODFee)
			}

			// Point 3: Recreated shipment MUST have exact new cod_amount_vnd AND full from_address/to_address!
			shipMu.Lock()
			gotShip := lastShipReq
			shipMu.Unlock()
			if gotShip.GetCodAmountVnd() != wantTotal {
				t.Fatalf("recreated shipment cod_amount_vnd = %d, want %d", gotShip.GetCodAmountVnd(), wantTotal)
			}
			if gotShip.GetFromAddress().GetName() != "Shop Alpha Warehouse" ||
				gotShip.GetFromAddress().GetPhone() != "0911111111" ||
				gotShip.GetFromAddress().GetStreetAddress() != "10 Ba Dinh" {
				t.Fatalf("recreated shipment from_address incomplete: %+v", gotShip.GetFromAddress())
			}
			if gotShip.GetToAddress().GetName() != "Tran Thi Binh" ||
				gotShip.GetToAddress().GetPhone() != "0912345678" ||
				gotShip.GetToAddress().GetProvinceCode() != "01" ||
				gotShip.GetToAddress().GetWardCode() != "00009" ||
				gotShip.GetToAddress().GetStreetAddress() != "77 Hung Vuong" {
				t.Fatalf("recreated shipment to_address incomplete: %+v", gotShip.GetToAddress())
			}
			if len(h.shipping.createShipAuth) < 2 || h.shipping.createShipAuth[1].service != clients.InternalServiceName {
				t.Fatalf("Step 5 CreateShipment auth = %+v, want service=%s", h.shipping.createShipAuth, clients.InternalServiceName)
			}
			ch, err := h.store.GetOrderAddressChangeByID(context.Background(), crashedChangeID)
			if err != nil || ch.State != store.AddressChangeStateCompleted || !ch.ShipmentRecreated || ch.NewTrackingCode != "NEW-TRK-002" {
				t.Fatalf("intent did not close COMPLETED: err=%v ch=%+v", err, ch)
			}

			// Point 1: order.address_updated emitted AT STEP 4 with truthful flags (shipment_queued=true, shipment_recreated=false, tracking_code="").
			outbox := countAddressUpdatedOutbox(t, h.store)
			if len(outbox) != 1 || outbox[0].ShipmentRecreated || !outbox[0].ShipmentQueued || outbox[0].TrackingCode != "" {
				t.Fatalf("Point 1: expected 1 Step-4 order.address_updated (queued=true, recreated=false, trk=''), got %+v", outbox)
			}
			for _, ev := range h.store.OutboxEvents() {
				if ev.EventType == events.SubjectOrderAddressUpdated {
					raw := string(ev.PayloadJSON)
					if strings.Contains(raw, "77 Hung Vuong") || strings.Contains(raw, "0912345678") || strings.Contains(raw, "Tran Thi Binh") {
						t.Fatalf("order.address_updated leaked PII: %s", raw)
					}
				}
			}
		})
	}
}

func TestW3B3_UpdateOrderAddress_CarrierPickedUp_AndCancelBeforeStep4RevertCOD(t *testing.T) {
	t.Run("carrier_picked_up_aborts_cleanly", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-picked-up")
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier already picked up parcel")
		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
		addrNew := registerBuyerAddr(h, "addr-pick", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00001", "W1", "123 New")

		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:                   ord.GetId(),
			AddressId:                 addrNew,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVnd: 50000,
			IdempotencyKey:            "idem-picked-up-abort",
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_CARRIER_PICKED_UP") {
			t.Fatalf("got err=%v, want ADDRESS_UPDATE_CARRIER_PICKED_UP", err)
		}
		if len(h.payment.adjustCODCalls) != 0 || len(countAddressUpdatedOutbox(t, h.store)) != 0 {
			t.Fatalf("must not adjust payment or emit outbox on carrier pickup abort")
		}
	})

	t.Run("cancel_before_step4_reverts_cod_including_watchdog_retry", func(t *testing.T) {
		h := newTestHarness()
		baseTime := time.Date(2026, 11, 25, 5, 0, 0, 0, time.UTC)
		now := baseTime
		h.svc.nowFn = func() time.Time { return now }

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-cancel-revert")
		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 55000, CodSupported: true}}
		addrNew := registerBuyerAddr(h, "addr-rev", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00001", "W1", "123 New")

		var revertAttempts atomic.Int32
		h.payment.onAdjustCOD = func(_ context.Context, req *paymentv1.AdjustCODShippingFeeRequest) (*paymentv1.AdjustCODShippingFeeResponse, error) {
			if strings.HasSuffix(req.GetIdempotencyKey(), ":revert") {
				if revertAttempts.Add(1) <= 3 {
					return nil, status.Error(codes.Unavailable, "payment temporarily unavailable during revert")
				}
			}
			return &paymentv1.AdjustCODShippingFeeResponse{NewShippingFeeVnd: req.GetNewShippingFeeVnd()}, nil
		}

		var savedChangeID string
		h.svc.afterAddressChangeStep = func(step int, changeID string) error {
			if step == 3 {
				savedChangeID = changeID
				if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord.GetId(), Reason: "changed mind"}); err != nil {
					t.Fatalf("CancelOrder during open intent failed: %v", err)
				}
			}
			return nil
		}

		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:                   ord.GetId(),
			AddressId:                 addrNew,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVnd: 55000,
			IdempotencyKey:            "idem-cancel-revert-req",
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("expected transient revert error, got %v", err)
		}

		ch, err := h.store.GetOrderAddressChangeByID(context.Background(), savedChangeID)
		if err != nil || ch.State != store.AddressChangeStateRevertingCOD {
			t.Fatalf("expected REVERTING_COD, got err=%v state=%s", err, ch.State)
		}

		now = baseTime.Add(10 * time.Second)
		handled, err := h.svc.ScanPendingAddressChanges(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("watchdog revert: handled=%d err=%v", handled, err)
		}
		ch, _ = h.store.GetOrderAddressChangeByID(context.Background(), savedChangeID)
		if ch.State != store.AddressChangeStateAborted || ch.CODAdjusted {
			t.Fatalf("expected ABORTED with cod_adjusted=false, got %+v", ch)
		}
		lastAdj := h.payment.adjustCODCalls[len(h.payment.adjustCODCalls)-1]
		if lastAdj.GetNewShippingFeeVnd() != 30000 || lastAdj.GetIdempotencyKey() != savedChangeID+":revert" {
			t.Fatalf("unexpected revert call: %+v", lastAdj)
		}
	})
}

func TestW3B3_UpdateOrderAddress_Step5PermanentFailAndQueued_AndLocksAndDelayedWebhook(t *testing.T) {
	t.Run("step5_permanent_fail_allows_seller_manual_ship", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-step5-fail")
		addrNew := registerBuyerAddr(h, "addr-s5-f", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "88 New Street")

		var calls atomic.Int32
		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			n := calls.Add(1)
			if n == 2 {
				return nil, status.Error(codes.FailedPrecondition, "UNSUPPORTED_WARD: carrier rejected new ward")
			}
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					OrderId:      req.GetOrderId(),
					CarrierCode:  req.GetCarrierCode(),
					ServiceCode:  req.GetServiceCode(),
					TrackingCode: "TRK-M-" + string(rune('0'+n)),
					LabelUrl:     "https://labels.sandbox/m",
				},
			}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("initial SellerShipOrder: %v", err)
		}

		resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrNew})
		if err != nil {
			t.Fatalf("UpdateOrderAddress with permanent Step 5 failure: %v", err)
		}
		if resp.GetShipmentRecreated() || resp.GetShipmentQueued() {
			t.Fatalf("expected recreated=false queued=false on permanent fail: %+v", resp)
		}
		if resp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_PREPARING ||
			resp.GetOrder().GetTrackingCode() != "" ||
			resp.GetOrder().GetShipmentJobStatus() != "FAILED" {
			t.Fatalf("unexpected order state after permanent Step 5 fail: status=%v trk=%q job=%q",
				resp.GetOrder().GetStatus(), resp.GetOrder().GetTrackingCode(), resp.GetOrder().GetShipmentJobStatus())
		}

		manualShip, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"})
		if err != nil || manualShip.GetTrackingCode() != "TRK-M-3" {
			t.Fatalf("manual SellerShipOrder failed: resp=%+v err=%v", manualShip, err)
		}
	})

	t.Run("step5_queued_and_delayed_old_webhook_ignored", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-step5-queued")
		addrNew := registerBuyerAddr(h, "addr-s5-q", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "100 Queued St")

		var calls atomic.Int32
		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			if calls.Add(1) == 1 {
				return &shippingv1.CreateShipmentResponse{
					Shipment: &shippingv1.Shipment{OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "OLD-TRK-DELAYED"},
				}, nil
			}
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-recreate-1"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrNew})
		if err != nil {
			t.Fatalf("UpdateOrderAddress queued: %v", err)
		}
		if !resp.GetShipmentQueued() || resp.GetShipmentJobId() != "job-recreate-1" || resp.GetOrder().GetShipmentJobStatus() != "QUEUED" {
			t.Fatalf("unexpected queued resp: %+v", resp)
		}

		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-delayed-old-trk", events.ShipmentUpdatedPayload{
			OrderID:      ord.GetId(),
			Status:       "delivering",
			TrackingCode: "OLD-TRK-DELAYED",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated old tracking: %v", err)
		}
		afterDelayed, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
		if afterDelayed.Order.Status != "PREPARING" || afterDelayed.Order.TrackingCode != "" {
			t.Fatalf("delayed old tracking wrongly mutated order: status=%s trk=%q", afterDelayed.Order.Status, afterDelayed.Order.TrackingCode)
		}

		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-new-trk-created", events.ShipmentUpdatedPayload{
			OrderID:      ord.GetId(),
			Status:       "created",
			TrackingCode: "NEW-TRK-ASYNC",
			CarrierCode:  "ghn",
			ServiceCode:  "standard",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated new tracking: %v", err)
		}
		afterNew, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
		if afterNew.Order.TrackingCode != "NEW-TRK-ASYNC" || afterNew.Order.ShipmentJobStatus != "" {
			t.Fatalf("new tracking not attached: trk=%q job=%q", afterNew.Order.TrackingCode, afterNew.Order.ShipmentJobStatus)
		}
	})

	t.Run("open_intent_blocks_ship_batch_ship_and_second_update_allows_cancel", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-open-locks")
		addrA := registerBuyerAddr(h, "addr-lock-a", "buyer-1", "Tran Thi Binh", "0912345678", "79", "HCM", "26735", "W", "10 A")
		addrB := registerBuyerAddr(h, "addr-lock-b", "buyer-1", "Tran Thi Binh", "0912345678", "79", "HCM", "26735", "W", "20 B")

		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 1 {
				return errors.New("pause after step 1")
			}
			return nil
		}
		if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrA,
			IdempotencyKey: "k-open-1",
		}); err == nil {
			t.Fatalf("expected pause error")
		}
		h.svc.afterAddressChangeStep = nil

		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("SellerShipOrder err=%v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
		}
		batchResp, err := h.svc.BatchShipOrders(sellerCtx, &orderv1.BatchShipOrdersRequest{OrderIds: []string{ord.GetId()}})
		if err != nil || batchResp.GetSkippedCount() != 1 || batchResp.GetResults()[0].GetReasonCode() != "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS" {
			t.Fatalf("BatchShipOrders result = %+v err=%v", batchResp, err)
		}
		if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrB,
			IdempotencyKey: "k-open-2",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("second UpdateOrderAddress err=%v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
		}
		cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord.GetId(), Reason: "cancel wins"})
		if err != nil || cancelResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("CancelOrder failed to win: resp=%+v err=%v", cancelResp, err)
		}
	})
}

func TestW3B3_UpdateOrderAddress_ConcurrentRaces(t *testing.T) {
	t.Run("two_concurrent_updates_one_wins", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-race-2upd")
		addr1 := registerBuyerAddr(h, "addr-r1", "buyer-1", "Tran Thi Binh", "0912345678", "01", "HN", "00001", "W1", "Street 1")
		addr2 := registerBuyerAddr(h, "addr-r2", "buyer-1", "Tran Thi Binh", "0912345678", "01", "HN", "00002", "W2", "Street 2")

		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 45000, CodSupported: true}}
		inStep3 := make(chan struct{}, 1)
		releaseStep3 := make(chan struct{})
		h.payment.onAdjustCOD = func(_ context.Context, req *paymentv1.AdjustCODShippingFeeRequest) (*paymentv1.AdjustCODShippingFeeResponse, error) {
			select {
			case inStep3 <- struct{}{}:
			default:
			}
			<-releaseStep3
			return &paymentv1.AdjustCODShippingFeeResponse{NewShippingFeeVnd: req.GetNewShippingFeeVnd()}, nil
		}

		var err1, err2 error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err1 = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
				OrderId:                   ord.GetId(),
				AddressId:                 addr1,
				AcceptFeeIncrease:         true,
				ExpectedNewShippingFeeVnd: 45000,
				IdempotencyKey:            "race-k1",
			})
		}()
		<-inStep3
		go func() {
			defer wg.Done()
			_, err2 = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
				OrderId:                   ord.GetId(),
				AddressId:                 addr2,
				AcceptFeeIncrease:         true,
				ExpectedNewShippingFeeVnd: 45000,
				IdempotencyKey:            "race-k2",
			})
		}()
		time.Sleep(15 * time.Millisecond)
		close(releaseStep3)
		wg.Wait()

		if err1 != nil {
			t.Fatalf("winner failed: %v", err1)
		}
		if status.Code(err2) != codes.FailedPrecondition || !strings.Contains(err2.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("loser err = %v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err2)
		}
	})

	t.Run("update_address_racing_delivering_webhook", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-race-delivering")
		addrRace := registerBuyerAddr(h, "addr-race-d", "buyer-1", "Tran Thi Binh", "0912345678", "79", "HCM", "26735", "W2", "New Race Street")

		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		oldTrk := shipResp.GetTrackingCode()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
				OrderId:        ord.GetId(),
				AddressId:      addrRace,
				IdempotencyKey: "race-deliv-k1",
			})
		}()
		go func() {
			defer wg.Done()
			_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-race-delivering", events.ShipmentUpdatedPayload{
				OrderID:      ord.GetId(),
				Status:       "delivering",
				TrackingCode: oldTrk,
			})
		}()
		wg.Wait()

		finalOrd, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
		var snap orderv1.OrderAddressSnapshot
		_ = json.Unmarshal(finalOrd.Order.ShippingAddressJSON, &snap)
		if finalOrd.Order.Status == "SHIPPING" && snap.GetStreetAddress() == "New Race Street" {
			t.Fatalf("DIVERGENCE: order is SHIPPING on old tracking %q with new address!", finalOrd.Order.TrackingCode)
		}
	})

	t.Run("two_watchdog_pods_claiming_same_crashed_intent", func(t *testing.T) {
		h := newTestHarness()
		baseTime := time.Date(2026, 11, 25, 6, 0, 0, 0, time.UTC)
		var clockMu sync.Mutex
		now := baseTime
		nowFn := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}
		h.svc.nowFn = nowFn

		pod2 := NewOrderService(h.svc.cfg, h.store, h.svc.clients, logger.New("pod2", logger.WithWriter(io.Discard)))
		pod2.nowFn = nowFn

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-2pods")
		addrPod := registerBuyerAddr(h, "addr-pod", "buyer-1", "Tran Thi Binh", "0912345678", "79", "HCM", "26735", "W2", "Pod2 St")

		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 1 {
				return errors.New("crash pod1")
			}
			return nil
		}
		_, _ = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrPod,
			IdempotencyKey: "k-2pods",
		})
		h.svc.afterAddressChangeStep = nil

		clockMu.Lock()
		now = baseTime.Add(35 * time.Second)
		clockMu.Unlock()

		var h1, h2 int
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			h1, _ = h.svc.ScanPendingAddressChanges(context.Background())
		}()
		go func() {
			defer wg.Done()
			h2, _ = pod2.ScanPendingAddressChanges(context.Background())
		}()
		wg.Wait()

		if h1+h2 != 1 {
			t.Fatalf("expected exactly 1 pod to claim & finish intent, got pod1=%d pod2=%d", h1, h2)
		}
		if len(countAddressUpdatedOutbox(t, h.store)) != 1 {
			t.Fatalf("expected exactly 1 outbox event across 2 pods")
		}
	})
}

// --- G4: BatchShipOrders (including Item G sanitized INTERNAL_ERROR) & GetPickingList ---

type failingPrepareStore struct {
	store.Store
	err error
}

func (f *failingPrepareStore) PrepareOrderForBatchShip(ctx context.Context, orderID, shopID, reqCarrier, reqService string, now time.Time) (*store.BatchShipPrepareOutcome, error) {
	return nil, f.err
}

func TestW3B3_BatchShipOrders_ValidationRBACMixedCancelRaceAndErrorSanitization(t *testing.T) {
	h := newTestHarness()
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	if _, err := h.svc.BatchShipOrders(seller1Ctx, &orderv1.BatchShipOrdersRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("0 items err=%v, want InvalidArgument", err)
	}
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	if _, err := h.svc.BatchShipOrders(seller1Ctx, &orderv1.BatchShipOrdersRequest{OrderIds: tooMany}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("51 items err=%v, want InvalidArgument", err)
	}
	if _, err := h.svc.BatchShipOrders(buyerCtx, &orderv1.BatchShipOrdersRequest{OrderIds: []string{"ord-x"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer err=%v, want PermissionDenied", err)
	}
	if _, err := h.svc.BatchShipOrders(clients.WithInternalAdminContext(context.Background()), &orderv1.BatchShipOrdersRequest{OrderIds: []string{"ord-x"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token err=%v, want PermissionDenied", err)
	}

	ordShop1 := createTestCODOrder(t, h, "buyer-1", "idem-bs-s1")
	respS2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-bs-s2",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-2", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout shop-2: %v", err)
	}
	ordShop2 := respS2.GetOrders()[0]

	if _, err := h.svc.BatchShipOrders(seller1Ctx, &orderv1.BatchShipOrdersRequest{
		OrderIds: []string{ordShop1.GetId(), ordShop2.GetId()},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign shop batch err=%v, want PermissionDenied", err)
	}
	if len(h.shipping.createShipCalls) != 0 {
		t.Fatalf("expected 0 CreateShipment calls after PermissionDenied, got %d", len(h.shipping.createShipCalls))
	}

	// Item G: DB error during PrepareOrderForBatchShip MUST NOT leak raw SQL/DB text into BatchShipOrderResult.Message!
	rawDBErr := errors.New("ERROR: relation \"orders\" deadlock detected on pgx conn 10.0.0.5")
	badSvc := NewOrderService(h.svc.cfg, &failingPrepareStore{Store: h.store, err: rawDBErr}, h.svc.clients, logger.New("t", logger.WithWriter(io.Discard)))
	errResp, err := badSvc.BatchShipOrders(seller1Ctx, &orderv1.BatchShipOrdersRequest{OrderIds: []string{ordShop1.GetId()}})
	if err != nil {
		t.Fatalf("BatchShipOrders with DB error: %v", err)
	}
	if errResp.GetResults()[0].GetReasonCode() != "INTERNAL_ERROR" {
		t.Fatalf("reason = %q, want INTERNAL_ERROR", errResp.GetResults()[0].GetReasonCode())
	}
	if strings.Contains(errResp.GetResults()[0].GetMessage(), "pgx") || strings.Contains(errResp.GetResults()[0].GetMessage(), "relation") {
		t.Fatalf("Item G violation: raw DB error leaked in BatchShipOrderResult.Message: %q", errResp.GetResults()[0].GetMessage())
	}

	// Mixed batch + CancelOrder race.
	ordCancelled := createTestCODOrder(t, h, "buyer-1", "idem-bs-canc")
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordCancelled.GetId(), Reason: "cancel"}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	ordShipped := createTestCODOrder(t, h, "buyer-1", "idem-bs-shipped")
	if _, err := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: ordShipped.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	ordBadWard := createTestCODOrder(t, h, "buyer-1", "idem-bs-ward")
	ordCancelRace := createTestCODOrder(t, h, "buyer-1", "idem-bs-race")

	var cancelCallsBefore int
	h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		if !req.GetEnqueueOnly() {
			t.Errorf("BatchShipOrders must set EnqueueOnly=true")
		}
		if req.GetOrderId() == ordBadWard.GetId() {
			return nil, status.Error(codes.InvalidArgument, "UNSUPPORTED_WARD: ward unknown")
		}
		if req.GetOrderId() == ordCancelRace.GetId() {
			cancelCallsBefore = len(h.shipping.cancelShipCalls)
			_, _ = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordCancelRace.GetId(), Reason: "race cancel"})
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-race-cancel"}, nil
		}
		return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-" + req.GetOrderId()}, nil
	}

	batchResp, err := h.svc.BatchShipOrders(seller1Ctx, &orderv1.BatchShipOrdersRequest{
		OrderIds: []string{
			ordShop1.GetId(),
			ordCancelled.GetId(),
			ordShipped.GetId(),
			ordBadWard.GetId(),
			ordCancelRace.GetId(),
			"missing-order-id",
		},
	})
	if err != nil {
		t.Fatalf("BatchShipOrders mixed: %v", err)
	}
	if batchResp.GetQueuedCount() != 1 || batchResp.GetSkippedCount() != 5 {
		t.Fatalf("counts = queued:%d skipped:%d, want 1/5", batchResp.GetQueuedCount(), batchResp.GetSkippedCount())
	}
	byID := map[string]*orderv1.BatchShipOrderResult{}
	for _, r := range batchResp.GetResults() {
		byID[r.GetOrderId()] = r
	}
	if byID[ordShop1.GetId()].GetOutcome() != orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_QUEUED ||
		byID[ordCancelled.GetId()].GetReasonCode() != "ORDER_CANCELLED" ||
		byID[ordShipped.GetId()].GetReasonCode() != "ALREADY_HAS_SHIPMENT" ||
		byID[ordBadWard.GetId()].GetReasonCode() != "UNSUPPORTED_WARD" ||
		byID[ordCancelRace.GetId()].GetReasonCode() != "ORDER_CANCELLED" ||
		byID["missing-order-id"].GetReasonCode() != "ORDER_NOT_FOUND" {
		t.Fatalf("unexpected mixed outcomes: %+v", byID)
	}
	if len(h.shipping.cancelShipCalls) <= cancelCallsBefore {
		t.Fatalf("expected compensating CancelShipment for raced order")
	}

	reResp, err := h.svc.BatchShipOrders(seller1Ctx, &orderv1.BatchShipOrdersRequest{OrderIds: []string{ordShop1.GetId()}})
	if err != nil || reResp.GetSkippedCount() != 1 || reResp.GetResults()[0].GetReasonCode() != "ALREADY_QUEUED" {
		t.Fatalf("expected ALREADY_QUEUED on re-run, got %+v err=%v", reResp, err)
	}
}

func TestW3B3_GetPickingList_AggregatesAndEnforcesShopRBAC(t *testing.T) {
	h := newTestHarness()
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	sku1b := proto.Clone(h.product.skus["sku-1"]).(*productv1.SKU)
	sku1b.Id = "sku-1b"
	sku1b.ProductId = "prod-1b"
	sku1b.ProductName = "Alpha Cap"
	sku1b.Name = "Black"
	h.product.skus["sku-1b"] = sku1b

	r1, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-pick-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}, {SkuId: "sku-1b", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("checkout 1: %v", err)
	}
	r2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-pick-2",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 3}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("checkout 2: %v", err)
	}
	rForeign, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-pick-s2",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-2", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("checkout foreign: %v", err)
	}

	if _, err := h.svc.GetPickingList(seller1Ctx, &orderv1.GetPickingListRequest{
		OrderIds: []string{r1.GetOrders()[0].GetId(), rForeign.GetOrders()[0].GetId()},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign shop picking list err=%v, want PermissionDenied", err)
	}

	pickResp, err := h.svc.GetPickingList(seller1Ctx, &orderv1.GetPickingListRequest{
		OrderIds: []string{r1.GetOrders()[0].GetId(), r2.GetOrders()[0].GetId()},
	})
	if err != nil {
		t.Fatalf("GetPickingList: %v", err)
	}
	if pickResp.GetOrderCount() != 2 || pickResp.GetTotalUnits() != 6 || len(pickResp.GetItems()) != 2 {
		t.Fatalf("unexpected picking list summary: %+v", pickResp)
	}
	bySKU := map[string]*orderv1.PickingListItem{}
	for _, it := range pickResp.GetItems() {
		bySKU[it.GetSkuId()] = it
	}
	if bySKU["sku-1"].GetTotalQuantity() != 5 || bySKU["sku-1"].GetOrderCount() != 2 || len(bySKU["sku-1"].GetOrderCodes()) != 2 {
		t.Fatalf("sku-1 picking item mismatch: %+v", bySKU["sku-1"])
	}
	if bySKU["sku-1b"].GetTotalQuantity() != 1 || bySKU["sku-1b"].GetOrderCount() != 1 {
		t.Fatalf("sku-1b picking item mismatch: %+v", bySKU["sku-1b"])
	}
}

// --- G8: Extended ListOrders Filters, Full-Shop Status Counts, and Item I Invalid Statuses ---

func TestW3B3_ListOrders_ExtendedFiltersStatusCountsAndInvalidStatuses(t *testing.T) {
	h := newTestHarness()
	baseTime := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	now := baseTime
	h.svc.nowFn = func() time.Time { return now }

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	h.user.addresses["addr-buyer-1"].RecipientName = "Nguyen Van An"
	ord1 := createTestCODOrder(t, h, "buyer-1", "idem-list-1")

	now = baseTime.Add(12 * time.Hour)
	h.user.addresses["addr-buyer-1"].RecipientName = "Le Thi Bao"
	resp2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-list-2",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		ShippingChoices: []*orderv1.ShopShippingChoice{
			{ShopId: "shop-1", CarrierCode: "mock_express", ServiceCode: "fast"},
		},
	})
	if err != nil {
		t.Fatalf("checkout 2: %v", err)
	}
	if err := h.svc.HandlePaymentResult(context.Background(), PaymentResultInput{
		EventID:         "ev-list-paid-2",
		CheckoutGroupID: resp2.GetCheckoutGroupId(),
		PaymentID:       resp2.GetPaymentId(),
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("paid 2: %v", err)
	}
	ord2ID := resp2.GetOrders()[0].GetId()
	if _, err := h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{OrderId: ord2ID, ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerConfirmOrder 2: %v", err)
	}

	now = baseTime.Add(24 * time.Hour)
	h.user.addresses["addr-buyer-1"].RecipientName = "Tran Van Cuong"
	ord3 := createTestCODOrder(t, h, "buyer-1", "idem-list-3")
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord3.GetId(), Reason: "cancel"}); err != nil {
		t.Fatalf("CancelOrder 3: %v", err)
	}

	// Item I: unknown / unspecified values in statuses -> InvalidArgument (must NOT silently drop filter!).
	if _, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
		RoleScope: "seller",
		Statuses:  []orderv1.OrderStatus{orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Item I: UNSPECIFIED in statuses got err=%v, want InvalidArgument", err)
	}
	if _, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
		RoleScope: "seller",
		Statuses:  []orderv1.OrderStatus{orderv1.OrderStatus(999)},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Item I: unknown enum 999 in statuses got err=%v, want InvalidArgument", err)
	}

	rCarrier, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
		RoleScope:           "seller",
		CarrierCode:         "mock_express",
		PageSize:            1,
		IncludeStatusCounts: true,
	})
	if err != nil {
		t.Fatalf("ListOrders carrier: %v", err)
	}
	if rCarrier.GetTotal() != 1 || len(rCarrier.GetOrders()) != 1 || rCarrier.GetOrders()[0].GetId() != ord2ID {
		t.Fatalf("carrier filter mismatch: total=%d", rCarrier.GetTotal())
	}
	countMap := map[orderv1.OrderStatus]int64{}
	for _, sc := range rCarrier.GetStatusCounts() {
		countMap[sc.GetStatus()] = sc.GetCount()
	}
	if countMap[orderv1.OrderStatus_ORDER_STATUS_CONFIRMED] != 1 ||
		countMap[orderv1.OrderStatus_ORDER_STATUS_PREPARING] != 1 ||
		countMap[orderv1.OrderStatus_ORDER_STATUS_CANCELLED] != 1 {
		t.Fatalf("expected full-shop status_counts {CONFIRMED:1, PREPARING:1, CANCELLED:1}, got %+v", countMap)
	}

	rKWName, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{RoleScope: "seller", Keyword: "thi bao"})
	if err != nil || rKWName.GetTotal() != 1 || rKWName.GetOrders()[0].GetId() != ord2ID {
		t.Fatalf("keyword recipient filter mismatch: err=%v total=%d", err, rKWName.GetTotal())
	}
	rKWCode, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{RoleScope: "seller", Keyword: ord1.GetOrderCode()})
	if err != nil || rKWCode.GetTotal() != 1 || rKWCode.GetOrders()[0].GetId() != ord1.GetId() {
		t.Fatalf("keyword order_code filter mismatch: err=%v total=%d", err, rKWCode.GetTotal())
	}
	rPay, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
		RoleScope:     "seller",
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil || rPay.GetTotal() != 1 || rPay.GetOrders()[0].GetId() != ord2ID {
		t.Fatalf("payment_method filter mismatch: err=%v total=%d", err, rPay.GetTotal())
	}
	rMulti, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
		RoleScope: "seller",
		Statuses:  []orderv1.OrderStatus{orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, orderv1.OrderStatus_ORDER_STATUS_PREPARING},
	})
	if err != nil || rMulti.GetTotal() != 2 {
		t.Fatalf("multi-status filter mismatch: err=%v total=%d", err, rMulti.GetTotal())
	}
	rDue, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
		RoleScope: "seller",
		Statuses:  []orderv1.OrderStatus{orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, orderv1.OrderStatus_ORDER_STATUS_PREPARING},
		DueBefore: timestamppb.New(baseTime.Add(50 * time.Hour)),
	})
	if err != nil || rDue.GetTotal() != 1 || rDue.GetOrders()[0].GetId() != ord1.GetId() {
		t.Fatalf("due_before filter mismatch: err=%v total=%d", err, rDue.GetTotal())
	}
}

// --- Real gRPC Interceptor Auth Verification ---

type w3b3GRPCOrderAdapter struct {
	orderv1.UnimplementedOrderServiceServer
	svc *OrderService
}

func (a *w3b3GRPCOrderAdapter) LookupOrder(ctx context.Context, req *orderv1.LookupOrderRequest) (*orderv1.LookupOrderResponse, error) {
	return a.svc.LookupOrder(ctx, req)
}
func (a *w3b3GRPCOrderAdapter) UpdateOrderAddress(ctx context.Context, req *orderv1.UpdateOrderAddressRequest) (*orderv1.UpdateOrderAddressResponse, error) {
	return a.svc.UpdateOrderAddress(ctx, req)
}
func (a *w3b3GRPCOrderAdapter) BatchShipOrders(ctx context.Context, req *orderv1.BatchShipOrdersRequest) (*orderv1.BatchShipOrdersResponse, error) {
	return a.svc.BatchShipOrders(ctx, req)
}
func (a *w3b3GRPCOrderAdapter) GetPickingList(ctx context.Context, req *orderv1.GetPickingListRequest) (*orderv1.GetPickingListResponse, error) {
	return a.svc.GetPickingList(ctx, req)
}

func TestW3B3_GRPCInterceptor_PublicLookupAndEndUserOnlyRPCs(t *testing.T) {
	userMgr, err := auth.NewManager("w3b3-user-secret-0123456789abcdef", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("w3b3-svc-secret-0123456789abcdef", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	h := newTestHarness()
	ord := createTestCODOrder(t, h, "buyer-1", "idem-grpc-auth")
	addrNew := registerBuyerAddr(h, "addr-grpc-1", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Phuong Tan Dinh", "10 Interceptor St")

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	orderv1.RegisterOrderServiceServer(srv, &w3b3GRPCOrderAdapter{svc: h.svc})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := orderv1.NewOrderServiceClient(conn)

	// 1. Anonymous LookupOrder succeeds through real interceptor.
	got, err := client.LookupOrder(context.Background(), &orderv1.LookupOrderRequest{
		OrderCode: ord.GetOrderCode(),
		Phone:     "0901234567",
	})
	if err != nil || got.GetOrder().GetOrderCode() != ord.GetOrderCode() {
		t.Fatalf("anonymous LookupOrder failed: resp=%+v err=%v", got, err)
	}

	// 2. Service token rejected by interceptor on LookupOrder, UpdateOrderAddress, BatchShipOrders, GetPickingList.
	svcTok, err := svcMgr.SignServiceToken("order-service")
	if err != nil {
		t.Fatalf("SignServiceToken: %v", err)
	}
	svcCtx := auth.InjectOutgoingGRPC(context.Background(), svcTok)

	if _, err := client.LookupOrder(svcCtx, &orderv1.LookupOrderRequest{OrderCode: ord.GetOrderCode(), Phone: "0901234567"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token LookupOrder = %v, want PermissionDenied", err)
	}
	if _, err := client.UpdateOrderAddress(svcCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrNew}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token UpdateOrderAddress = %v, want PermissionDenied", err)
	}
	if _, err := client.BatchShipOrders(svcCtx, &orderv1.BatchShipOrdersRequest{OrderIds: []string{ord.GetId()}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token BatchShipOrders = %v, want PermissionDenied", err)
	}
	if _, err := client.GetPickingList(svcCtx, &orderv1.GetPickingListRequest{OrderIds: []string{ord.GetId()}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token GetPickingList = %v, want PermissionDenied", err)
	}

	// 3. Buyer JWT passes interceptor and executes UpdateOrderAddress cleanly.
	buyerTok, _, _, err := userMgr.SignAccessToken("buyer-1", "buyer", "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	buyerOutCtx := auth.InjectOutgoingGRPC(context.Background(), buyerTok)
	updResp, err := client.UpdateOrderAddress(buyerOutCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId:   ord.GetId(),
		AddressId: addrNew,
	})
	if err != nil || updResp.GetOrder().GetShippingAddress().GetStreetAddress() != "10 Interceptor St" {
		t.Fatalf("buyer JWT UpdateOrderAddress failed: resp=%+v err=%v", updResp, err)
	}

	cfg := config.Config{}
	tmpSvc := NewOrderService(cfg, store.NewMemoryStore(), &clients.Bundle{}, logger.New("t", logger.WithWriter(io.Discard)))
	if tmpSvc.batchShipMaxItems() != 50 {
		t.Fatalf("default batchShipMaxItems = %d, want 50", tmpSvc.batchShipMaxItems())
	}
}

// Shipping being down after the address change was applied must not block the order forever: the watchdog stops retrying
// after addressChangeMaxShipmentAttempts and the seller can ship manually.
func TestW3B3_UpdateOrderAddress_ShippingDownHandsOrderBackToSeller(t *testing.T) {
	h := newTestHarness()
	now := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return now }
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-down")

	down := false
	h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		if down {
			return nil, status.Error(codes.Unavailable, "shipping down")
		}
		return &shippingv1.CreateShipmentResponse{Shipment: &shippingv1.Shipment{
			OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-" + uuid.NewString(),
		}}, nil
	}
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	down = true
	addr := registerBuyerAddr(h, "addr-down", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "1 Down St")
	resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addr})
	if err != nil || !resp.GetShipmentQueued() {
		t.Fatalf("applied change with shipping down: resp=%+v err=%v", resp, err)
	}
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); !strings.Contains(status.Convert(err).Message(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
		t.Fatalf("open intent must block manual ship, got %v", err)
	}

	for i := 0; i < addressChangeMaxShipmentAttempts+1; i++ {
		now = now.Add(addressChangeMaxBackoff + time.Second)
		if _, err := h.svc.ScanPendingAddressChanges(context.Background()); err != nil {
			t.Fatalf("watchdog: %v", err)
		}
	}
	got, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
	if got.Order.ShipmentJobStatus != "FAILED" || got.Order.TrackingCode != "" {
		t.Fatalf("order not handed back to seller: job=%q trk=%q", got.Order.ShipmentJobStatus, got.Order.TrackingCode)
	}
	down = false
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("manual ship after hand-back: %v", err)
	}
}

// Choosing the address the order already has keeps its shipment untouched.
func TestW3B3_UpdateOrderAddress_SameAddressKeepsShipment(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-same")
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: "addr-buyer-1"})
	if err != nil {
		t.Fatalf("same address: %v", err)
	}
	if resp.GetFeeDeltaVnd() != 0 || resp.GetOrder().GetTrackingCode() == "" || len(h.shipping.cancelShipCalls) != 0 || len(countAddressUpdatedOutbox(t, h.store)) != 0 {
		t.Fatalf("same address must be a no-op, got %+v", resp)
	}
}
