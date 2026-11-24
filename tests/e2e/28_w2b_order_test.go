package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ordW2bOrderDTO struct {
	ID                         string         `json:"id"`
	OrderCode                  string         `json:"order_code"`
	CheckoutGroupID            string         `json:"checkout_group_id"`
	BuyerID                    string         `json:"buyer_id"`
	ShopID                     string         `json:"shop_id"`
	Status                     string         `json:"status"`
	PaymentMethod              string         `json:"payment_method"`
	PaymentID                  string         `json:"payment_id"`
	PaymentStatus              string         `json:"payment_status"`
	SubtotalVND                int64          `json:"subtotal_vnd"`
	FlashSaleDiscountVND       int64          `json:"flash_sale_discount_vnd"`
	ShopProgramDiscountVND     int64          `json:"shop_program_discount_vnd"`
	ShopVoucherDiscountVND     int64          `json:"shop_voucher_discount_vnd"`
	ShopFreeshipDiscountVND    int64          `json:"shop_freeship_discount_vnd"`
	PlatformVoucherDiscountVND int64          `json:"platform_voucher_discount_vnd"`
	FreeshipDiscountVND        int64          `json:"freeship_discount_vnd"`
	CoinsDiscountVND           int64          `json:"coins_discount_vnd"`
	ShippingFeeVND             int64          `json:"shipping_fee_vnd"`
	CommissionVND              int64          `json:"commission_vnd"`
	PaymentFeeVND              int64          `json:"payment_fee_vnd"`
	PaymentFeeBps              int32          `json:"payment_fee_bps"`
	NeedsManualReview          bool           `json:"needs_manual_review"`
	TotalPayableVND            int64          `json:"total_payable_vnd"`
	CarrierCode                string         `json:"carrier_code"`
	ServiceCode                string         `json:"service_code"`
	TrackingCode               string         `json:"tracking_code"`
	PaymentExpiresAt           string         `json:"payment_expires_at"`
	Items                      []OrderItemDTO `json:"items"`
}

type ordW2bCheckoutResponse struct {
	CheckoutGroupID  string           `json:"checkout_group_id"`
	Orders           []ordW2bOrderDTO `json:"orders"`
	PaymentID        string           `json:"payment_id"`
	PaymentMethod    string           `json:"payment_method"`
	PaymentStatus    string           `json:"payment_status"`
	PaymentExpiresAt string           `json:"payment_expires_at"`
	TotalPayableVND  int64            `json:"total_payable_vnd"`
	IdempotentReplay bool             `json:"idempotent_replay"`
	Warnings         []string         `json:"warnings"`
}

type ordW2bOrderResponse struct {
	Order ordW2bOrderDTO `json:"order"`
}

type ordW2bRetryPaymentResponse struct {
	Payment struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
		ExpiresAt string `json:"expires_at"`
	} `json:"payment"`
}

type ordW2bShopAllocationDTO struct {
	OrderID             string `json:"order_id"`
	ShopID              string `json:"shop_id"`
	SubtotalVND         int64  `json:"subtotal_vnd"`
	CommissionVND       int64  `json:"commission_vnd"`
	ShippingFeeVND      int64  `json:"shipping_fee_vnd"`
	PlatformDiscountVND int64  `json:"platform_discount_vnd"`
}

type ordW2bPaymentDTO struct {
	PaymentID       string                    `json:"payment_id"`
	OrderGroupID    string                    `json:"order_group_id"`
	AmountVND       int64                     `json:"amount_vnd"`
	Method          string                    `json:"method"`
	Status          string                    `json:"status"`
	ShopAllocations []ordW2bShopAllocationDTO `json:"shop_allocations"`
}

type ordW2bPaymentResponse struct {
	Payment ordW2bPaymentDTO `json:"payment"`
}

type ordW2bTopProductDTO struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	UnitsSold   int64  `json:"units_sold"`
	RevenueVND  int64  `json:"revenue_vnd"`
	OrderCount  int64  `json:"order_count"`
}

type ordW2bDailyReportDTO struct {
	Date               string `json:"date"`
	GMVVND             int64  `json:"gmv_vnd"`
	NetRevenueVND      int64  `json:"net_revenue_vnd"`
	CommissionVND      int64  `json:"commission_vnd"`
	PaymentFeeVND      int64  `json:"payment_fee_vnd"`
	PlatformSubsidyVND int64  `json:"platform_subsidy_vnd"`
	OrderCount         int64  `json:"order_count"`
	CompletedCount     int64  `json:"completed_count"`
	CancelledCount     int64  `json:"cancelled_count"`
	ReturnedCount      int64  `json:"returned_count"`
}

type ordW2bStatusCountDTO struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type ordW2bSellerReportDTO struct {
	ShopID          string                 `json:"shop_id"`
	FromDate        string                 `json:"from_date"`
	ToDate          string                 `json:"to_date"`
	GMVVND          int64                  `json:"gmv_vnd"`
	NetRevenueVND   int64                  `json:"net_revenue_vnd"`
	CommissionVND   int64                  `json:"commission_vnd"`
	PaymentFeeVND   int64                  `json:"payment_fee_vnd"`
	TotalOrders     int64                  `json:"total_orders"`
	CompletedOrders int64                  `json:"completed_orders"`
	CancelledOrders int64                  `json:"cancelled_orders"`
	ReturnedOrders  int64                  `json:"returned_orders"`
	CancelRateBps   int32                  `json:"cancel_rate_bps"`
	ReturnRateBps   int32                  `json:"return_rate_bps"`
	OrdersByStatus  []ordW2bStatusCountDTO `json:"orders_by_status"`
	Daily           []ordW2bDailyReportDTO `json:"daily"`
	TopProducts     []ordW2bTopProductDTO  `json:"top_products"`
}

type ordW2bSellerReportWrap struct {
	Report ordW2bSellerReportDTO `json:"report"`
}

type ordW2bPlatformReportDTO struct {
	FromDate           string                 `json:"from_date"`
	ToDate             string                 `json:"to_date"`
	GMVVND             int64                  `json:"gmv_vnd"`
	NetRevenueVND      int64                  `json:"net_revenue_vnd"`
	CommissionVND      int64                  `json:"commission_vnd"`
	PaymentFeeVND      int64                  `json:"payment_fee_vnd"`
	PlatformSubsidyVND int64                  `json:"platform_subsidy_vnd"`
	TotalOrders        int64                  `json:"total_orders"`
	CompletedOrders    int64                  `json:"completed_orders"`
	CancelledOrders    int64                  `json:"cancelled_orders"`
	ReturnedOrders     int64                  `json:"returned_orders"`
	CancelRateBps      int32                  `json:"cancel_rate_bps"`
	ReturnRateBps      int32                  `json:"return_rate_bps"`
	OrdersByStatus     []ordW2bStatusCountDTO `json:"orders_by_status"`
	Daily              []ordW2bDailyReportDTO `json:"daily"`
	TopProducts        []ordW2bTopProductDTO  `json:"top_products"`
	UserStats          map[string]any         `json:"user_stats"`
}

type ordW2bPlatformReportWrap struct {
	Report ordW2bPlatformReportDTO `json:"report"`
}

type ordW2bNeedsReviewListResponse struct {
	Items    []ordW2bOrderDTO `json:"items"`
	Page     int32            `json:"page"`
	PageSize int32            `json:"page_size"`
	Total    int64            `json:"total"`
}

func ordW2bOrderGRPCAddr() string {
	if v := strings.TrimSpace(os.Getenv("ORDER_GRPC_ADDR")); v != "" {
		return v
	}
	return "localhost:50055"
}

func ordW2bServiceJWTSecret() string {
	if v := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); v != "" {
		return v
	}
	return "dev-only-nexusmart-service-hs256-secret-change-me"
}

func ordW2bMintServiceToken(svcName string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":     svcName,
		"role":    "admin",
		"service": svcName,
		"iss":     "nexusmart",
		"jti":     uuid.NewString(),
		"iat":     now,
		"exp":     now + 120,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(ordW2bServiceJWTSecret()))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func ordW2bAuthCtx(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func ordW2bDialOrderGRPC(t *testing.T) orderv1.OrderServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(ordW2bOrderGRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial OrderService gRPC (%s): %v", ordW2bOrderGRPCAddr(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return orderv1.NewOrderServiceClient(conn)
}

func ordW2bDialPaymentGRPC(t *testing.T, env *testEnv) paymentv1.PaymentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PaymentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PaymentService gRPC (%s): %v", env.cfg.PaymentGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return paymentv1.NewPaymentServiceClient(conn)
}

func ordW2bActivePaymentFeeBps(t *testing.T, env *testEnv, method paymentv1.PaymentMethod) int32 {
	t.Helper()
	pc := ordW2bDialPaymentGRPC(t, env)
	svcTok := ordW2bMintServiceToken("order-service")
	resp, err := pc.GetFeeConfig(ordW2bAuthCtx(env.ctx(t), svcTok), &paymentv1.GetFeeConfigRequest{
		PaymentMethod: method,
	})
	if err != nil {
		t.Fatalf("Payment.GetFeeConfig(%v): %v", method, err)
	}
	for _, c := range resp.GetActiveConfigs() {
		if c.GetPaymentMethod() == method {
			return c.GetFeeBps()
		}
	}
	t.Fatalf("Payment.GetFeeConfig(%v) returned no active config: %+v", method, resp.GetActiveConfigs())
	return 0
}

func ordW2bGetOrder(t *testing.T, env *testEnv, token, orderID string) ordW2bOrderDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/orders/%s", url.PathEscape(orderID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordW2bOrderResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode order %s: %v", orderID, err)
	}
	return out.Order
}

func ordW2bGetPayment(t *testing.T, env *testEnv, token, paymentID string) ordW2bPaymentDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/payments/%s", url.PathEscape(paymentID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordW2bPaymentResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode payment %s: %v", paymentID, err)
	}
	return out.Payment
}

func ordW2bSendPaymentWebhook(t *testing.T, env *testEnv, paymentID, groupID string, amountVND int64, st string) {
	t.Helper()
	whResp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/payment", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: "TXN-ORDW2B-" + uuid.NewString(),
		PaymentID:    paymentID,
		OrderGroupID: groupID,
		AmountVND:    amountVND,
		Status:       st,
		Reference:    "NEXUSMART " + groupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST /webhooks/payment failed: %v", err)
	}
	if whResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /webhooks/payment HTTP %d: %s", whResp.StatusCode, string(whResp.Body))
	}
}

func ordW2bSendShippingWebhook(t *testing.T, env *testEnv, carrier, orderID, trackingCode, rawStatus, desc string) {
	t.Helper()
	if strings.TrimSpace(carrier) == "" {
		carrier = "ghn"
	}
	path := fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(carrier))
	resp, err := env.client.PostSignedWebhook(env.ctx(t), path, ShippingWebhookPayload{
		EventID:      "ordw2b-shp-" + uuid.NewString(),
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s HTTP %d: %s", path, resp.StatusCode, string(resp.Body))
	}
}

func ordW2bCreateFlashSaleWithApprovedItem(
	t *testing.T,
	env *testEnv,
	adminTok string,
	seller *sellerFixture,
	skuID string,
	flashPriceVND int64,
	startsAt, endsAt time.Time,
	totalSlots, perUserLimit int32,
) (string, string) {
	t.Helper()
	fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", adminTok, nil, CreateFlashSaleRequest{
		Name:     "OrdW2B Flash " + env.uniqueCode("FS"),
		StartsAt: startsAt.UTC().Format(time.RFC3339),
		EndsAt:   endsAt.UTC().Format(time.RFC3339),
	}, http.StatusCreated, http.StatusOK)
	var fsOut FlashSaleResponse
	if err := DecodeJSON(fsResp, &fsOut); err != nil {
		t.Fatalf("decode create flash sale: %v", err)
	}
	fsID := fsOut.FlashSale.ID

	regPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(fsID))
	regResp := env.mustDoJSON(t, http.MethodPost, regPath, seller.AccessToken, nil, RegisterFlashSaleItemRequest{
		SKUID:         skuID,
		FlashPriceVND: flashPriceVND,
		TotalSlots:    totalSlots,
		PerUserLimit:  perUserLimit,
	}, http.StatusCreated, http.StatusOK)
	var regOut FlashSaleItemResponse
	if err := DecodeJSON(regResp, &regOut); err != nil {
		t.Fatalf("decode register flash item: %v", err)
	}
	itemID := regOut.Item.ID

	approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(itemID))
	env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
		Approve: true,
		Reason:  "OrdW2B E2E approval",
	}, http.StatusOK)
	return fsID, itemID
}

func ordW2bCreateShopDiscountProgram(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	skuID string,
	discountedPriceVND int64,
	perUserLimit int32,
	startsAt, endsAt time.Time,
) {
	t.Helper()
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/discount-programs", seller.AccessToken, nil, map[string]any{
		"name":      "OrdW2B Shop Promo " + env.uniqueCode("SP"),
		"starts_at": startsAt.UTC().Format(time.RFC3339),
		"ends_at":   endsAt.UTC().Format(time.RFC3339),
		"items": []map[string]any{
			{
				"sku_id":               skuID,
				"discounted_price_vnd": discountedPriceVND,
				"per_user_limit":       perUserLimit,
			},
		},
	}, http.StatusCreated, http.StatusOK)
}

func ordW2bTodayVN() string {
	loc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
	return time.Now().In(loc).Format("2006-01-02")
}

// TestE2E_W2B_Order verifies all 5 Wave 2b order service milestones end-to-end across
// gateway, order, promotion, payment, shipping, and product services.
func TestE2E_W2B_Order(t *testing.T) {
	env := getEnv()
	cat := env.ensureCategory(t)
	adminTok := env.adminLogin(t)
	orderGRPC := ordW2bDialOrderGRPC(t)

	// 1. Checkout: Shop discount program (server lookup), shop freeship, default_weight_grams fallback,
	// category_id + commission_rate_bps snapshot, payment_fee_bps from Payment.GetFeeConfig, and ShopAllocation invariant.
	t.Run("1_Checkout_ShopProgram_ShopFreeship_PaymentFee_DefaultWeight", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s1")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b1")
		addr := env.addAddress(t, buyer)

		// Enable shop freeship + custom default_weight_grams=1500g.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", seller.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn", "mock_express"},
			"default_weight_grams":    1500,
			"default_length_cm":       20,
			"default_width_cm":        15,
			"default_height_cm":       10,
			"free_ship_enabled":       true,
			"free_ship_min_order_vnd": 100000,
			"free_ship_max_vnd":       15000,
		}, http.StatusOK)

		// Create SKU with weight_grams=0 so order falls back to shop default_weight_grams=1500g.
		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B ShopPromo Item", 300000, 20, 0)

		now := time.Now().UTC()
		// Active shop discount program: 300,000 -> 240,000 (P_shop = 60,000).
		ordW2bCreateShopDiscountProgram(t, env, seller, sku.ID, 240000, 5, now.Add(-5*time.Minute), now.Add(2*time.Hour))

		shopV := env.createShopVoucher(t, seller, 20000, 100000, 10)
		platV := env.createAdminVoucher(t, "PLATFORM", 10000, 100000, 10)
		env.saveVoucher(t, buyer.AccessToken, shopV)
		env.saveVoucher(t, buyer.AccessToken, platV)

		expectedWeight := sku.WeightGrams
		if expectedWeight <= 0 {
			expectedWeight = 1500
		}
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, expectedWeight, 0)
		vietqrFeeBps := ordW2bActivePaymentFeeBps(t, env, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR)

		// Checkout WITHOUT sending shop_program_item_id: order must resolve it server-side via ListActiveShopDiscounts.
		idemKey := "ordw2b-c1-" + uuid.NewString()
		coReq := CheckoutRequest{
			AddressID:         addr.ID,
			Items:             []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			ShopVoucherIDs:    map[string]string{seller.ShopID: shopV.ID},
			PlatformVoucherID: platV.ID,
			PaymentMethod:     "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}
		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, coReq, http.StatusCreated, http.StatusOK)

		var co ordW2bCheckoutResponse
		if err := DecodeJSON(resp, &co); err != nil {
			t.Fatalf("decode checkout: %v", err)
		}
		if len(co.Orders) != 1 || co.PaymentID == "" {
			t.Fatalf("unexpected checkout response: %s", string(resp.Body))
		}
		ord := co.Orders[0]
		wantShip := quote.FeeVND
		wantSShop := int64(15000)
		if wantShip < wantSShop {
			wantSShop = wantShip
		}
		// G=300000, P_shop=60000, V_shop=20000, S_shop=min(Ship, 15000)
		wantR := int64(300000) - 60000 - 20000 - wantSShop
		wantD := int64(10000)
		wantP := wantR + wantShip - wantD
		wantCCat := (int64(240000) * int64(cat.CommissionRateBps)) / 10000
		if wantCCat > wantR {
			wantCCat = wantR
		}
		wantCPay := (wantP * int64(vietqrFeeBps)) / 10000
		if wantCPay > wantR-wantCCat {
			wantCPay = wantR - wantCCat
		}

		if ord.SubtotalVND != 300000 || ord.ShopProgramDiscountVND != 60000 || ord.ShopVoucherDiscountVND != 20000 {
			t.Fatalf("unexpected shop discounts: sub=%d p_shop=%d v_shop=%d", ord.SubtotalVND, ord.ShopProgramDiscountVND, ord.ShopVoucherDiscountVND)
		}
		if ord.ShippingFeeVND != wantShip {
			t.Fatalf("shipping_fee_vnd=%d, want %d (default_weight_grams=%d)", ord.ShippingFeeVND, wantShip, expectedWeight)
		}
		if ord.ShopFreeshipDiscountVND != wantSShop {
			t.Fatalf("shop_freeship_discount_vnd=%d, want %d", ord.ShopFreeshipDiscountVND, wantSShop)
		}
		if ord.PlatformVoucherDiscountVND != 10000 || ord.TotalPayableVND != wantP {
			t.Fatalf("v_plat=%d total_payable=%d, want 10000 / %d", ord.PlatformVoucherDiscountVND, ord.TotalPayableVND, wantP)
		}
		// Wave 2b buyer fee privacy (order r4 + gateway): buyer sees 0 for all fee fields across
		// CreateCheckout, idempotent replay, GetOrder, ListOrders, and direct gRPC GetOrder.
		assertBuyerFeesZero := func(label string, o ordW2bOrderDTO) {
			t.Helper()
			if o.CommissionVND != 0 || o.PaymentFeeBps != 0 || o.PaymentFeeVND != 0 {
				t.Fatalf("%s must mask fees to 0 for buyer, got comm=%d pay_fee_bps=%d pay_fee=%d",
					label, o.CommissionVND, o.PaymentFeeBps, o.PaymentFeeVND)
			}
			for _, it := range o.Items {
				if it.CommissionBps != 0 {
					t.Fatalf("%s must mask item commission_bps to 0 for buyer, got %d", label, it.CommissionBps)
				}
			}
		}
		assertBuyerFeesZero("buyer CreateCheckout", ord)

		replayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, coReq, http.StatusOK, http.StatusCreated)
		var replayCo ordW2bCheckoutResponse
		if err := DecodeJSON(replayResp, &replayCo); err != nil {
			t.Fatalf("decode replay checkout: %v", err)
		}
		if !replayCo.IdempotentReplay || len(replayCo.Orders) != 1 || replayCo.Orders[0].ID != ord.ID {
			t.Fatalf("unexpected replay checkout: %+v", replayCo)
		}
		assertBuyerFeesZero("buyer CreateCheckout replay", replayCo.Orders[0])
		assertBuyerFeesZero("buyer GET /orders/{id}", ordW2bGetOrder(t, env, buyer.AccessToken, ord.ID))

		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?page=1&page_size=20", buyer.AccessToken, nil, nil, http.StatusOK)
		var buyerList struct {
			Items []ordW2bOrderDTO `json:"items"`
		}
		if err := DecodeJSON(listResp, &buyerList); err != nil {
			t.Fatalf("decode buyer GET /orders: %v", err)
		}
		foundInList := false
		for _, item := range buyerList.Items {
			if item.ID == ord.ID {
				foundInList = true
				assertBuyerFeesZero("buyer GET /orders list", item)
			}
		}
		if !foundInList {
			t.Fatalf("order %s missing from buyer GET /orders", ord.ID)
		}

		sellerOrd := ordW2bGetOrder(t, env, seller.AccessToken, ord.ID)
		if sellerOrd.CommissionVND != wantCCat || sellerOrd.PaymentFeeBps != vietqrFeeBps || sellerOrd.PaymentFeeVND != wantCPay {
			t.Fatalf("seller order fees mismatch: comm=%d (want %d), pay_fee_bps=%d (want %d), pay_fee=%d (want %d)",
				sellerOrd.CommissionVND, wantCCat, sellerOrd.PaymentFeeBps, vietqrFeeBps, sellerOrd.PaymentFeeVND, wantCPay)
		}

		// Verify Payment ShopAllocation privacy & values:
		// buyer sees commission_vnd = 0; admin sees full R, Ship, D, and C_total = C_cat + C_pay.
		buyerPay := ordW2bGetPayment(t, env, buyer.AccessToken, co.PaymentID)
		if len(buyerPay.ShopAllocations) != 1 || buyerPay.ShopAllocations[0].CommissionVND != 0 {
			t.Fatalf("buyer GET /payments/{id} must mask commission_vnd to 0, got %+v", buyerPay.ShopAllocations)
		}
		adminPay := ordW2bGetPayment(t, env, adminTok, co.PaymentID)
		if len(adminPay.ShopAllocations) != 1 {
			t.Fatalf("expected 1 ShopAllocation, got %+v", adminPay.ShopAllocations)
		}
		al := adminPay.ShopAllocations[0]
		if al.SubtotalVND != wantR || al.ShippingFeeVND != wantShip || al.PlatformDiscountVND != wantD || al.CommissionVND != wantCCat+wantCPay {
			t.Fatalf("ShopAllocation mismatch: got=%+v want R=%d Ship=%d D=%d C_tot=%d", al, wantR, wantShip, wantD, wantCCat+wantCPay)
		}
		if al.SubtotalVND+al.ShippingFeeVND-al.PlatformDiscountVND != adminPay.AmountVND {
			t.Fatalf("allocation invariant violated: %d+%d-%d != %d", al.SubtotalVND, al.ShippingFeeVND, al.PlatformDiscountVND, adminPay.AmountVND)
		}

		// Verify direct gRPC GetOrder masks fees for buyer and exposes persisted snapshot to owning seller.
		buyerGRPCOrd, err := orderGRPC.GetOrder(ordW2bAuthCtx(env.ctx(t), buyer.AccessToken), &orderv1.GetOrderRequest{OrderId: ord.ID})
		if err != nil {
			t.Fatalf("buyer Order.GetOrder gRPC: %v", err)
		}
		if bo := buyerGRPCOrd.GetOrder(); bo.GetCommissionVnd() != 0 || bo.GetPaymentFeeVnd() != 0 || bo.GetPaymentFeeBps() != 0 || bo.GetItems()[0].GetCommissionBps() != 0 {
			t.Fatalf("buyer gRPC GetOrder leaked fees: comm=%d pay_fee=%d pay_bps=%d item_bps=%d",
				bo.GetCommissionVnd(), bo.GetPaymentFeeVnd(), bo.GetPaymentFeeBps(), bo.GetItems()[0].GetCommissionBps())
		}
		grpcOrd, err := orderGRPC.GetOrder(ordW2bAuthCtx(env.ctx(t), seller.AccessToken), &orderv1.GetOrderRequest{OrderId: ord.ID})
		if err != nil {
			t.Fatalf("seller Order.GetOrder gRPC: %v", err)
		}
		item := grpcOrd.GetOrder().GetItems()[0]
		if item.GetWeightGrams() != expectedWeight || item.GetCommissionBps() != cat.CommissionRateBps || item.GetShopProgramItemId() == "" || item.GetEffectiveUnitPriceVnd() != 240000 {
			t.Fatalf("order item snapshot mismatch: weight=%d (want %d), commission_bps=%d (want %d), effective=%d (want 240000), shop_program_item_id=%q",
				item.GetWeightGrams(), expectedWeight, item.GetCommissionBps(), cat.CommissionRateBps, item.GetEffectiveUnitPriceVnd(), item.GetShopProgramItemId())
		}
	})

	// 2. Flash sale rules: reject COD, reject upcoming, flash wins over shop program (no stacking),
	// short 5m expiry, RetryPayment never extends window, and expiry capped by flash ends_at.
	t.Run("2_FlashSale_Rules_NoCOD_ActiveOnly_ShortExpiry_NoStack", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s2")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b2")
		addr := env.addAddress(t, buyer)

		_, skuActive := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Flash Active", 400000, 20, 400)
		_, skuUpcoming := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Flash Upcoming", 400000, 20, 400)
		_, skuShortWin := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Flash ShortWin", 300000, 20, 400)

		now := time.Now().UTC()
		// Put skuActive in BOTH an active shop discount program (320k) AND an active flash sale (200k).
		ordW2bCreateShopDiscountProgram(t, env, seller, skuActive.ID, 320000, 5, now.Add(-5*time.Minute), now.Add(2*time.Hour))
		_, activeFSItemID := ordW2bCreateFlashSaleWithApprovedItem(t, env, adminTok, seller, skuActive.ID, 200000, now.Add(-5*time.Minute), now.Add(45*time.Minute), 10, 5)
		_, upcomingFSItemID := ordW2bCreateFlashSaleWithApprovedItem(t, env, adminTok, seller, skuUpcoming.ID, 180000, now.Add(2*time.Hour), now.Add(3*time.Hour), 10, 5)
		shortEndsAt := now.Add(90 * time.Second)
		_, shortFSItemID := ordW2bCreateFlashSaleWithApprovedItem(t, env, adminTok, seller, skuShortWin.ID, 150000, now.Add(-1*time.Minute), shortEndsAt, 10, 5)

		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 400, 0)

		// (a) Reject COD on flash sale item.
		codResp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-fs-cod-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuActive.ID, Quantity: 1, FlashSaleItemID: activeFSItemID}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		})
		if err != nil {
			t.Fatalf("COD flash checkout request failed: %v", err)
		}
		if codResp.StatusCode < 400 || codResp.StatusCode >= 500 {
			t.Fatalf("expected 4xx rejecting COD + flash sale, got HTTP %d body=%s", codResp.StatusCode, string(codResp.Body))
		}

		// (b) Reject upcoming flash sale item.
		upResp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-fs-up-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuUpcoming.ID, Quantity: 1, FlashSaleItemID: upcomingFSItemID}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		})
		if err != nil {
			t.Fatalf("upcoming flash checkout request failed: %v", err)
		}
		if upResp.StatusCode < 400 || upResp.StatusCode >= 500 {
			t.Fatalf("expected 4xx rejecting upcoming flash sale, got HTTP %d body=%s", upResp.StatusCode, string(upResp.Body))
		}

		// (c) Active flash sale via VIETQR: wins over shop program (P_shop=0) and gets ~5m expiry.
		beforeCo := time.Now().UTC()
		okResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-fs-ok-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuActive.ID, Quantity: 1, FlashSaleItemID: activeFSItemID}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var co ordW2bCheckoutResponse
		if err := DecodeJSON(okResp, &co); err != nil {
			t.Fatalf("decode active flash checkout: %v", err)
		}
		ord := co.Orders[0]
		if ord.FlashSaleDiscountVND != 200000 || ord.ShopProgramDiscountVND != 0 {
			t.Fatalf("flash must win over shop discount program: flash=%d shop_program=%d", ord.FlashSaleDiscountVND, ord.ShopProgramDiscountVND)
		}
		origExp, err := time.Parse(time.RFC3339, co.PaymentExpiresAt)
		if err != nil {
			t.Fatalf("parse payment_expires_at %q: %v", co.PaymentExpiresAt, err)
		}
		expDelta := origExp.Sub(beforeCo)
		if expDelta < 2*time.Minute || expDelta > 6*time.Minute {
			t.Fatalf("flash payment_expires_at delta = %s, want ~5m (not 15m)", expDelta)
		}

		// (d) RetryPayment must NOT extend flash expiry past original hold window.
		retryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(co.PaymentID))
		retryResp := env.mustDoJSON(t, http.MethodPost, retryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-fs-retry-" + uuid.NewString(),
		}, map[string]any{"method": "VIETQR"}, http.StatusOK)
		var retryOut ordW2bRetryPaymentResponse
		if err := DecodeJSON(retryResp, &retryOut); err != nil {
			t.Fatalf("decode retry payment: %v", err)
		}
		if retryOut.Payment.PaymentID == "" {
			t.Fatalf("retry payment returned empty payment_id: %s", string(retryResp.Body))
		}
		ordAfterRetry := ordW2bGetOrder(t, env, buyer.AccessToken, ord.ID)
		retryExp, err := time.Parse(time.RFC3339, ordAfterRetry.PaymentExpiresAt)
		if err != nil {
			t.Fatalf("parse order payment_expires_at after retry %q: %v", ordAfterRetry.PaymentExpiresAt, err)
		}
		if retryExp.After(origExp.Add(2 * time.Second)) {
			t.Fatalf("RetryPayment extended flash deadline from %s to %s", origExp, retryExp)
		}

		// (e) Short flash sale ending in ~90s must cap payment_expires_at <= ends_at.
		shortResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-fs-short-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuShortWin.ID, Quantity: 1, FlashSaleItemID: shortFSItemID}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var shortCo ordW2bCheckoutResponse
		if err := DecodeJSON(shortResp, &shortCo); err != nil {
			t.Fatalf("decode short flash checkout: %v", err)
		}
		shortExp, err := time.Parse(time.RFC3339, shortCo.PaymentExpiresAt)
		if err != nil {
			t.Fatalf("parse short payment_expires_at %q: %v", shortCo.PaymentExpiresAt, err)
		}
		if shortExp.After(shortEndsAt.Add(2 * time.Second)) {
			t.Fatalf("flash payment_expires_at %s exceeded flash ends_at %s", shortExp, shortEndsAt)
		}
	})

	// 3. Seller & Platform reports: exact metrics, VN date boundaries, top products, >366d rejection, RBAC.
	t.Run("3_Reports_SellerAndPlatform_RBAC_AndMetrics", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s3")
		otherSeller := env.createApprovedSeller(t, "ordw2b-s3-other")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b3")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)

		prodA, skuA := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Report ProdA", 200000, 20, 500)
		prodB, skuB := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Report ProdB", 100000, 20, 500)
		platV := env.createAdminVoucher(t, "PLATFORM", 15000, 50000, 10)
		env.saveVoucher(t, buyer.AccessToken, platV)

		quoteA := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 1000, 400000)
		quoteB := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

		// Order 1: ProdA x2 (COD) -> Ship -> Delivered -> Buyer Confirm Received -> COMPLETED.
		co1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-rep-o1-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 2}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quoteA.CarrierCode, ServiceCode: quoteA.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co1 ordW2bCheckoutResponse
		if err := DecodeJSON(co1Resp, &co1); err != nil {
			t.Fatalf("decode co1: %v", err)
		}
		ord1 := co1.Orders[0]
		ship1Resp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord1.ID)), seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
		var ship1 SellerShipResponse
		if err := DecodeJSON(ship1Resp, &ship1); err != nil {
			t.Fatalf("decode ship1: %v", err)
		}
		ordW2bSendShippingWebhook(t, env, ship1.Order.CarrierCode, ord1.ID, ship1.TrackingCode, "delivered", "Giao thành công O1")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ord1 DELIVERED", func() (bool, string) {
			o := ordW2bGetOrder(t, env, buyer.AccessToken, ord1.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord1.ID)), buyer.AccessToken, nil, nil, http.StatusOK)

		// Order 2: ProdB x1 (VIETQR + 15k platform voucher) -> Pay -> Ship -> Delivered -> Confirm -> COMPLETED.
		co2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-rep-o2-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:         addr.ID,
			Items:             []CheckoutItemInputDTO{{SKUID: skuB.ID, Quantity: 1}},
			PlatformVoucherID: platV.ID,
			PaymentMethod:     "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quoteB.CarrierCode, ServiceCode: quoteB.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co2 ordW2bCheckoutResponse
		if err := DecodeJSON(co2Resp, &co2); err != nil {
			t.Fatalf("decode co2: %v", err)
		}
		ord2 := co2.Orders[0]
		ordW2bSendPaymentWebhook(t, env, co2.PaymentID, co2.CheckoutGroupID, co2.TotalPayableVND, "SUCCEEDED")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ord2 CONFIRMED", func() (bool, string) {
			o := ordW2bGetOrder(t, env, buyer.AccessToken, ord2.ID)
			return StatusEquals(o.Status, "CONFIRMED"), o.Status
		})
		ship2Resp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord2.ID)), seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
		var ship2 SellerShipResponse
		if err := DecodeJSON(ship2Resp, &ship2); err != nil {
			t.Fatalf("decode ship2: %v", err)
		}
		ordW2bSendShippingWebhook(t, env, ship2.Order.CarrierCode, ord2.ID, ship2.TrackingCode, "delivered", "Giao thành công O2")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ord2 DELIVERED", func() (bool, string) {
			o := ordW2bGetOrder(t, env, buyer.AccessToken, ord2.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord2.ID)), buyer.AccessToken, nil, nil, http.StatusOK)

		// Order 3: ProdB x1 (COD) -> Cancelled by buyer.
		co3Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-rep-o3-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuB.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quoteB.CarrierCode, ServiceCode: quoteB.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co3 ordW2bCheckoutResponse
		if err := DecodeJSON(co3Resp, &co3); err != nil {
			t.Fatalf("decode co3: %v", err)
		}
		ord3 := co3.Orders[0]
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord3.ID)), buyer.AccessToken, nil, map[string]any{
			"reason": "Đổi ý",
		}, http.StatusOK)

		todayVN := ordW2bTodayVN()
		repPath := fmt.Sprintf("/api/v1/seller/reports?from_date=%s&to_date=%s&top_limit=10", todayVN, todayVN)
		repResp := env.mustDoJSON(t, http.MethodGet, repPath, seller.AccessToken, nil, nil, http.StatusOK)
		var sRepWrap ordW2bSellerReportWrap
		if err := DecodeJSON(repResp, &sRepWrap); err != nil {
			t.Fatalf("decode seller report: %v", err)
		}
		sr := sRepWrap.Report

		sellerOrd1 := ordW2bGetOrder(t, env, seller.AccessToken, ord1.ID)
		sellerOrd2 := ordW2bGetOrder(t, env, seller.AccessToken, ord2.ID)
		// Report GMV rule (contracts §4.5): gmv_vnd = SUM(P + D) across orders not in PENDING/CANCELLED/RETURNED.
		ord1PD := sellerOrd1.TotalPayableVND + sellerOrd1.PlatformVoucherDiscountVND + sellerOrd1.FreeshipDiscountVND + sellerOrd1.CoinsDiscountVND
		ord2PD := sellerOrd2.TotalPayableVND + sellerOrd2.PlatformVoucherDiscountVND + sellerOrd2.FreeshipDiscountVND + sellerOrd2.CoinsDiscountVND
		wantGMV := ord1PD + ord2PD
		wantComm := sellerOrd1.CommissionVND + sellerOrd2.CommissionVND
		wantPayFee := sellerOrd1.PaymentFeeVND + sellerOrd2.PaymentFeeVND
		wantNet := (sellerOrd1.SubtotalVND - sellerOrd1.CommissionVND - sellerOrd1.PaymentFeeVND) + (sellerOrd2.SubtotalVND - sellerOrd2.CommissionVND - sellerOrd2.PaymentFeeVND)

		if sr.ShopID != seller.ShopID || sr.FromDate != todayVN || sr.ToDate != todayVN {
			t.Fatalf("seller report header mismatch: %+v", sr)
		}
		if sr.TotalOrders != 3 || sr.CompletedOrders != 2 || sr.CancelledOrders != 1 || sr.ReturnedOrders != 0 {
			t.Fatalf("seller report counts mismatch: total=%d comp=%d canc=%d ret=%d", sr.TotalOrders, sr.CompletedOrders, sr.CancelledOrders, sr.ReturnedOrders)
		}
		if sr.CancelRateBps != 3333 || sr.ReturnRateBps != 0 {
			t.Fatalf("seller report rates mismatch: cancel_bps=%d return_bps=%d", sr.CancelRateBps, sr.ReturnRateBps)
		}
		if sr.GMVVND != wantGMV || sr.CommissionVND != wantComm || sr.PaymentFeeVND != wantPayFee || sr.NetRevenueVND != wantNet {
			t.Fatalf("seller report money mismatch: gmv=%d/%d comm=%d/%d pay_fee=%d/%d net=%d/%d",
				sr.GMVVND, wantGMV, sr.CommissionVND, wantComm, sr.PaymentFeeVND, wantPayFee, sr.NetRevenueVND, wantNet)
		}
		if len(sr.TopProducts) != 2 || sr.TopProducts[0].ProductID != prodA.ID || sr.TopProducts[0].UnitsSold != 2 || sr.TopProducts[1].ProductID != prodB.ID {
			t.Fatalf("seller top_products mismatch: %+v", sr.TopProducts)
		}
		if len(sr.Daily) != 1 || sr.Daily[0].Date != todayVN || sr.Daily[0].GMVVND != wantGMV || sr.Daily[0].PlatformSubsidyVND != 0 {
			t.Fatalf("seller daily series mismatch (expect platform_subsidy_vnd=0): %+v", sr.Daily)
		}

		// RBAC & scoping checks:
		// (a) Buyer cannot access seller reports.
		env.mustDoJSON(t, http.MethodGet, repPath, buyer.AccessToken, nil, nil, http.StatusForbidden)
		// (b) Seller cannot access platform reports.
		adminRepPath := fmt.Sprintf("/api/v1/admin/reports?from_date=%s&to_date=%s&top_limit=10", todayVN, todayVN)
		env.mustDoJSON(t, http.MethodGet, adminRepPath, seller.AccessToken, nil, nil, http.StatusForbidden)
		// (c) Direct gRPC GetSellerReport rejects foreign shop_id with PermissionDenied and scopes empty shop_id to JWT shop_id.
		if _, err := orderGRPC.GetSellerReport(ordW2bAuthCtx(env.ctx(t), otherSeller.AccessToken), &orderv1.GetSellerReportRequest{
			ShopId:   seller.ShopID,
			FromDate: todayVN,
			ToDate:   todayVN,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied when seller queries foreign shop_id, got %v", err)
		}
		grpcRep, err := orderGRPC.GetSellerReport(ordW2bAuthCtx(env.ctx(t), otherSeller.AccessToken), &orderv1.GetSellerReportRequest{
			FromDate: todayVN,
			ToDate:   todayVN,
		})
		if err != nil {
			t.Fatalf("direct gRPC GetSellerReport for otherSeller own shop: %v", err)
		}
		if grpcRep.GetShopId() != otherSeller.ShopID || grpcRep.GetTotalOrders() != 0 {
			t.Fatalf("seller report leaked foreign shop data: shop_id=%s orders=%d (want %s / 0)",
				grpcRep.GetShopId(), grpcRep.GetTotalOrders(), otherSeller.ShopID)
		}
		// (d) Date span > 366 days must be rejected with 400.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/reports?from_date=2025-01-01&to_date=2026-11-23", seller.AccessToken, nil, nil, http.StatusBadRequest)

		// (e) Admin platform report aggregates across shops including platform subsidy >= 15,000.
		pResp := env.mustDoJSON(t, http.MethodGet, adminRepPath, adminTok, nil, nil, http.StatusOK)
		var pWrap ordW2bPlatformReportWrap
		if err := DecodeJSON(pResp, &pWrap); err != nil {
			t.Fatalf("decode platform report: %v", err)
		}
		pr := pWrap.Report
		if pr.GMVVND < wantGMV || pr.CompletedOrders < 2 || pr.PlatformSubsidyVND < 15000 {
			t.Fatalf("platform report unexpectedly low: gmv=%d comp=%d subsidy=%d", pr.GMVVND, pr.CompletedOrders, pr.PlatformSubsidyVND)
		}
		if len(pr.Daily) != 1 || pr.Daily[0].Date != todayVN || pr.Daily[0].PlatformSubsidyVND != pr.PlatformSubsidyVND || pr.Daily[0].PlatformSubsidyVND < 15000 {
			t.Fatalf("platform daily series must populate platform_subsidy_vnd == %d: %+v", pr.PlatformSubsidyVND, pr.Daily)
		}
	})

	// 4. Admin manual review queue: RBAC, non-flagged precondition rejection, and needs_manual_review hiding.
	t.Run("4_AdminManualReviewQueue_RBAC_AndVisibility", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s4")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b4")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Review Item", 120000, 10, 300)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 300, 120000)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-rev-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co ordW2bCheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode review checkout: %v", err)
		}
		ordID := co.Orders[0].ID

		// Buyer & seller are rejected from manual review REST & gRPC endpoints.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/orders/needs-review", buyer.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/orders/needs-review", seller.AccessToken, nil, nil, http.StatusForbidden)
		resolvePath := fmt.Sprintf("/api/v1/admin/orders/%s/resolve-review", url.PathEscape(ordID))
		env.mustDoJSON(t, http.MethodPost, resolvePath, buyer.AccessToken, nil, map[string]any{"note": "nope"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, resolvePath, seller.AccessToken, nil, map[string]any{"note": "nope"}, http.StatusForbidden)

		if _, err := orderGRPC.ListOrdersNeedingReview(ordW2bAuthCtx(env.ctx(t), seller.AccessToken), &orderv1.ListOrdersNeedingReviewRequest{}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied for seller ListOrdersNeedingReview gRPC, got %v", err)
		}
		if _, err := orderGRPC.ResolveOrderReview(ordW2bAuthCtx(env.ctx(t), buyer.AccessToken), &orderv1.ResolveOrderReviewRequest{OrderId: ordID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied for buyer ResolveOrderReview gRPC, got %v", err)
		}

		// Admin can list the queue cleanly.
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/orders/needs-review?page=1&page_size=20", adminTok, nil, nil, http.StatusOK)
		var listOut ordW2bNeedsReviewListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode needs-review list: %v", err)
		}

		// Resolving a non-flagged order returns FailedPrecondition (412 / 400).
		resResp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, resolvePath, adminTok, nil, map[string]any{
			"note": "Attempting resolve on unflagged order",
		})
		if err != nil {
			t.Fatalf("POST resolve-review failed: %v", err)
		}
		if resResp.StatusCode < 400 || resResp.StatusCode >= 500 {
			t.Fatalf("expected 4xx resolving non-flagged order, got HTTP %d body=%s", resResp.StatusCode, string(resResp.Body))
		}

		// Normal order shows needs_manual_review=false for buyer/seller.
		if ordW2bGetOrder(t, env, buyer.AccessToken, ordID).NeedsManualReview {
			t.Fatalf("expected buyer needs_manual_review=false")
		}
		if ordW2bGetOrder(t, env, seller.AccessToken, ordID).NeedsManualReview {
			t.Fatalf("expected seller needs_manual_review=false")
		}
	})

	// 5. Cancel paid order & Return delivered order with Shop Discount Program + Shop Freeship:
	// refund == exact P (never exceeds P) and shop discount program per-user quota is released on cancel.
	t.Run("5_CancelAndReturn_WithShopProgramAndShopFreeship", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s5")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b5")
		addr := env.addAddress(t, buyer)

		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", seller.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn"},
			"default_weight_grams":    500,
			"free_ship_enabled":       true,
			"free_ship_min_order_vnd": 100000,
			"free_ship_max_vnd":       15000,
		}, http.StatusOK)

		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Refund Promo Item", 250000, 20, 500)
		now := time.Now().UTC()
		// per_user_limit = 1 so cancelling MUST release the quota for the buyer to buy again!
		ordW2bCreateShopDiscountProgram(t, env, seller, sku.ID, 200000, 1, now.Add(-5*time.Minute), now.Add(2*time.Hour))
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

		// (a) Paid VIETQR order with P_shop=50k + S_shop -> Cancel -> refund == P & per_user_limit released.
		co1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-ref-c1-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co1 ordW2bCheckoutResponse
		if err := DecodeJSON(co1Resp, &co1); err != nil {
			t.Fatalf("decode co1: %v", err)
		}
		ord1 := co1.Orders[0]
		if ord1.ShopProgramDiscountVND != 50000 || ord1.ShopFreeshipDiscountVND <= 0 {
			t.Fatalf("expected P_shop=50000 & S_shop>0, got p_shop=%d s_shop=%d", ord1.ShopProgramDiscountVND, ord1.ShopFreeshipDiscountVND)
		}
		ordW2bSendPaymentWebhook(t, env, co1.PaymentID, co1.CheckoutGroupID, co1.TotalPayableVND, "SUCCEEDED")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ord1 CONFIRMED", func() (bool, string) {
			o := ordW2bGetOrder(t, env, buyer.AccessToken, ord1.ID)
			return StatusEquals(o.Status, "CONFIRMED"), o.Status
		})

		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord1.ID)), buyer.AccessToken, nil, map[string]any{
			"reason": "Hủy đơn để kiểm tra hoàn tiền P_shop + S_shop",
		}, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "cancel refund == P", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer.AccessToken, ord1.ID)
			for _, r := range refunds {
				if r.AmountVND == ord1.TotalPayableVND {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d", refunds, ord1.TotalPayableVND)
		})

		// (b) Re-purchase with the same buyer (proving per_user_limit=1 was released!), deliver, and complete return -> refund == P.
		co2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-ref-c2-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co2 ordW2bCheckoutResponse
		if err := DecodeJSON(co2Resp, &co2); err != nil {
			t.Fatalf("decode co2: %v", err)
		}
		ord2 := co2.Orders[0]
		if ord2.ShopProgramDiscountVND != 50000 || ord2.ShopFreeshipDiscountVND <= 0 {
			t.Fatalf("re-checkout after cancel lost shop program quota: p_shop=%d s_shop=%d", ord2.ShopProgramDiscountVND, ord2.ShopFreeshipDiscountVND)
		}
		ordW2bSendPaymentWebhook(t, env, co2.PaymentID, co2.CheckoutGroupID, co2.TotalPayableVND, "SUCCEEDED")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ord2 CONFIRMED", func() (bool, string) {
			o := ordW2bGetOrder(t, env, buyer.AccessToken, ord2.ID)
			return StatusEquals(o.Status, "CONFIRMED"), o.Status
		})

		shipResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord2.ID)), seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil {
			t.Fatalf("decode ship2: %v", err)
		}
		carrier := shipOut.Order.CarrierCode
		ordW2bSendShippingWebhook(t, env, carrier, ord2.ID, shipOut.TrackingCode, "delivered", "Đã giao hàng")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ord2 DELIVERED", func() (bool, string) {
			o := ordW2bGetOrder(t, env, buyer.AccessToken, ord2.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})

		retResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(ord2.ID)), buyer.AccessToken, nil, map[string]any{
			"reason":        "Hoàn hàng có P_shop và S_shop",
			"evidence_urls": []string{"https://cdn.example.com/ordw2b-r1.jpg"},
		}, http.StatusOK)
		var retOut ordW2RequestReturnResponse
		if err := DecodeJSON(retResp, &retOut); err != nil {
			t.Fatalf("decode request return: %v", err)
		}
		retID := retOut.ReturnRequestID
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID)), seller.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_RETURN",
			"note":     "Đồng ý nhận lại",
		}, http.StatusOK)

		var retTrack string
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return tracking assigned", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			retTrack = rr.ReturnTrackingCode
			return StatusEquals(rr.Status, "AWAITING_SHIPMENT") && retTrack != "", rr.Status
		})
		ordW2bSendShippingWebhook(t, env, carrier, ord2.ID, retTrack, "delivered", "Hàng hoàn đã về kho")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return RECEIVED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, seller.AccessToken, retID)
			return StatusEquals(rr.Status, "RECEIVED") && rr.GoodsReceived, rr.Status
		})
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/returns/%s/confirm-received", url.PathEscape(retID)), seller.AccessToken, nil, nil, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return refund == P", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer.AccessToken, ord2.ID)
			for _, r := range refunds {
				if r.AmountVND == ord2.TotalPayableVND {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d", refunds, ord2.TotalPayableVND)
		})
	})

	// 6. Review P1: 0% category commission fee schedule must charge 0 VND commission and store 0 bps on order_items (never 5%).
	t.Run("6_ZeroPercentCategoryCommission_NoFallback", func(t *testing.T) {
		catResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, map[string]any{
			"name":                "OrdW2B ZeroFee Cat " + env.uniqueCode("ZC"),
			"slug":                strings.ToLower(env.uniqueCode("zc-")),
			"commission_rate_bps": 0,
			"sort_order":          1,
		}, http.StatusCreated, http.StatusOK)
		var catOut CategoryResponse
		if err := DecodeJSON(catResp, &catOut); err != nil {
			t.Fatalf("decode zero-fee category: %v", err)
		}
		if catOut.Category.ID == "" || catOut.Category.CommissionRateBps != 0 {
			t.Fatalf("expected 0 bps category, got %+v", catOut.Category)
		}

		seller := env.createApprovedSeller(t, "ordw2b-s6")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b6")
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller, catOut.Category.ID, "OrdW2B ZeroFee Product", 300000, 10, 500)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-zfee-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co ordW2bCheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode zero-fee checkout: %v", err)
		}
		ord := co.Orders[0]
		if ord.CommissionVND != 0 || ord.PaymentFeeVND != 0 || ord.PaymentFeeBps != 0 {
			t.Fatalf("buyer checkout must mask fees to 0, got comm=%d pay_fee=%d pay_fee_bps=%d",
				ord.CommissionVND, ord.PaymentFeeVND, ord.PaymentFeeBps)
		}
		sellerOrd := ordW2bGetOrder(t, env, seller.AccessToken, ord.ID)
		if sellerOrd.CommissionVND != 0 {
			t.Fatalf("0 bps category order commission_vnd = %d, want 0", sellerOrd.CommissionVND)
		}
		buyerPay := ordW2bGetPayment(t, env, buyer.AccessToken, co.PaymentID)
		if len(buyerPay.ShopAllocations) != 1 || buyerPay.ShopAllocations[0].CommissionVND != 0 {
			t.Fatalf("buyer GET /payments/{id} must mask commission_vnd to 0, got %+v", buyerPay.ShopAllocations)
		}
		adminPay := ordW2bGetPayment(t, env, adminTok, co.PaymentID)
		if len(adminPay.ShopAllocations) != 1 || adminPay.ShopAllocations[0].CommissionVND != sellerOrd.PaymentFeeVND {
			t.Fatalf("admin allocation commission_vnd = %+v, want %d (C_cat=0 + C_pay)", adminPay.ShopAllocations, sellerOrd.PaymentFeeVND)
		}
		grpcOrd, err := orderGRPC.GetOrder(ordW2bAuthCtx(env.ctx(t), seller.AccessToken), &orderv1.GetOrderRequest{OrderId: ord.ID})
		if err != nil {
			t.Fatalf("seller Order.GetOrder gRPC: %v", err)
		}
		if gotBPS := grpcOrd.GetOrder().GetItems()[0].GetCommissionBps(); gotBPS != 0 {
			t.Fatalf("order_items.commission_bps = %d, want 0", gotBPS)
		}
	})

	// 7. Review round 3: Buyer exhausts shop discount program per_user_limit, then purchases again successfully at regular price.
	t.Run("7_ShopProgramPerUserLimitExhausted_NextPurchaseSucceedsAtRegularPrice", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s7")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b7")
		addr := env.addAddress(t, buyer)

		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B ShopPromo Limit Item", 300000, 20, 500)
		now := time.Now().UTC()
		// Active shop discount program: 300,000 -> 240,000, per_user_limit = 1.
		ordW2bCreateShopDiscountProgram(t, env, seller, sku.ID, 240000, 1, now.Add(-5*time.Minute), now.Add(2*time.Hour))
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

		// First order (qty=1): uses the 1 allowed program slot at 240,000 (P_shop = 60,000).
		co1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-lim-1-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co1 ordW2bCheckoutResponse
		if err := DecodeJSON(co1Resp, &co1); err != nil {
			t.Fatalf("decode first limit order: %v", err)
		}
		ord1 := co1.Orders[0]
		if ord1.ShopProgramDiscountVND != 60000 {
			t.Fatalf("order 1 shop_program_discount_vnd = %d, want 60000", ord1.ShopProgramDiscountVND)
		}
		grpcOrd1, err := orderGRPC.GetOrder(ordW2bAuthCtx(env.ctx(t), buyer.AccessToken), &orderv1.GetOrderRequest{OrderId: ord1.ID})
		if err != nil {
			t.Fatalf("Order.GetOrder(ord1) gRPC: %v", err)
		}
		if it1 := grpcOrd1.GetOrder().GetItems()[0]; it1.GetEffectiveUnitPriceVnd() != 240000 || it1.GetShopProgramItemId() == "" {
			t.Fatalf("order 1 item must apply program price: effective=%d prog_id=%q", it1.GetEffectiveUnitPriceVnd(), it1.GetShopProgramItemId())
		}

		// Second order by the SAME buyer (per_user_limit=1 already exhausted):
		// MUST succeed at regular price 300,000 with shop_program_discount_vnd=0 and item shop_program_item_id="".
		co2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-lim-2-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co2 ordW2bCheckoutResponse
		if err := DecodeJSON(co2Resp, &co2); err != nil {
			t.Fatalf("decode second regular-price order: %v", err)
		}
		ord2 := co2.Orders[0]
		if len(co2.Warnings) == 0 {
			t.Fatalf("expected non-empty checkout warnings when shop discount program per_user_limit is exceeded, got body=%s", string(co2Resp.Body))
		}
		sellerOrd2 := ordW2bGetOrder(t, env, seller.AccessToken, ord2.ID)
		wantComm2 := (int64(300000) * int64(cat.CommissionRateBps)) / 10000
		if ord2.ShopProgramDiscountVND != 0 || ord2.TotalPayableVND != 300000+ord2.ShippingFeeVND || sellerOrd2.CommissionVND != wantComm2 {
			t.Fatalf("order 2 must bill at regular price: P_shop=%d P=%d (want %d) C_cat=%d (want %d)",
				ord2.ShopProgramDiscountVND, ord2.TotalPayableVND, 300000+ord2.ShippingFeeVND, sellerOrd2.CommissionVND, wantComm2)
		}
		grpcOrd2, err := orderGRPC.GetOrder(ordW2bAuthCtx(env.ctx(t), seller.AccessToken), &orderv1.GetOrderRequest{OrderId: ord2.ID})
		if err != nil {
			t.Fatalf("Order.GetOrder(ord2) gRPC: %v", err)
		}
		if it2 := grpcOrd2.GetOrder().GetItems()[0]; it2.GetEffectiveUnitPriceVnd() != 300000 || it2.GetShopProgramItemId() != "" {
			t.Fatalf("order 2 item must have regular price & empty shop_program_item_id: effective=%d prog_id=%q",
				it2.GetEffectiveUnitPriceVnd(), it2.GetShopProgramItemId())
		}
	})

	// 8. Concurrent checkouts with identical Idempotency-Key return the same checkout_group_id cleanly (no Aborted/409).
	t.Run("8_ConcurrentCheckout_SameIdempotencyKey_NoAborted", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s8")
		buyer := env.registerVerifyAndLogin(t, "ordw2b-b8")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		prod, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B Concurrent Idem", 180000, 20, 400)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 400, 180000)
		stockBefore := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock

		idemKey := "ordw2b-conc-" + uuid.NewString()
		reqBody := CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}

		type res struct {
			status int
			out    ordW2bCheckoutResponse
			body   string
			err    error
		}
		results := make([]res, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				resp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
					"Idempotency-Key": idemKey,
				}, reqBody)
				if err != nil {
					results[idx] = res{err: err}
					return
				}
				r := res{status: resp.StatusCode, body: string(resp.Body)}
				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
					r.err = DecodeJSON(resp, &r.out)
				}
				results[idx] = r
			}(i)
		}
		close(start)
		wg.Wait()

		for i, r := range results {
			if r.err != nil {
				t.Fatalf("concurrent checkout[%d] error: %v", i, r.err)
			}
			if r.status != http.StatusOK && r.status != http.StatusCreated {
				t.Fatalf("concurrent checkout[%d] expected 200/201 (no Aborted/409), got HTTP %d body=%s", i, r.status, r.body)
			}
			if r.out.CheckoutGroupID == "" || len(r.out.Orders) != 1 {
				t.Fatalf("concurrent checkout[%d] invalid payload: %s", i, r.body)
			}
			o := r.out.Orders[0]
			if o.CommissionVND != 0 || o.PaymentFeeVND != 0 || o.PaymentFeeBps != 0 {
				t.Fatalf("concurrent checkout[%d] leaked fees to buyer: comm=%d pay_fee=%d pay_fee_bps=%d", i, o.CommissionVND, o.PaymentFeeVND, o.PaymentFeeBps)
			}
			for _, it := range o.Items {
				if it.CommissionBps != 0 {
					t.Fatalf("concurrent checkout[%d] leaked item commission_bps=%d to buyer", i, it.CommissionBps)
				}
			}
		}
		if results[0].out.CheckoutGroupID != results[1].out.CheckoutGroupID || results[0].out.Orders[0].ID != results[1].out.Orders[0].ID {
			t.Fatalf("concurrent checkout mismatch: [0]=(%s,%s) [1]=(%s,%s)",
				results[0].out.CheckoutGroupID, results[0].out.Orders[0].ID,
				results[1].out.CheckoutGroupID, results[1].out.Orders[0].ID)
		}
		if stockAfter := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock; stockAfter != stockBefore-2 {
			t.Fatalf("concurrent checkout deducted stock incorrectly: before=%d after=%d want=%d", stockBefore, stockAfter, stockBefore-2)
		}

		// Confirm only ONE order exists for this fresh buyer and fees stay masked.
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?page=1&page_size=20", buyer.AccessToken, nil, nil, http.StatusOK)
		var listOut struct {
			Items []ordW2bOrderDTO `json:"items"`
		}
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode buyer orders list: %v", err)
		}
		if len(listOut.Items) != 1 || listOut.Items[0].ID != results[0].out.Orders[0].ID {
			t.Fatalf("expected exactly 1 order after concurrent checkout, got %d orders: %+v", len(listOut.Items), listOut.Items)
		}
		if listOut.Items[0].CommissionVND != 0 || listOut.Items[0].PaymentFeeVND != 0 || listOut.Items[0].PaymentFeeBps != 0 {
			t.Fatalf("ListOrders leaked fees to buyer: %+v", listOut.Items[0])
		}

		// Subsequent replay of POST /api/v1/checkout with same Idempotency-Key after success returns same order & masks fees.
		replayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, reqBody, http.StatusOK, http.StatusCreated)
		var replayOut ordW2bCheckoutResponse
		if err := DecodeJSON(replayResp, &replayOut); err != nil {
			t.Fatalf("decode post-success replay: %v", err)
		}
		if !replayOut.IdempotentReplay || replayOut.CheckoutGroupID != results[0].out.CheckoutGroupID ||
			len(replayOut.Orders) != 1 || replayOut.Orders[0].ID != results[0].out.Orders[0].ID {
			t.Fatalf("post-success replay mismatch: got %+v want group=%s order=%s",
				replayOut, results[0].out.CheckoutGroupID, results[0].out.Orders[0].ID)
		}
		if ro := replayOut.Orders[0]; ro.CommissionVND != 0 || ro.PaymentFeeVND != 0 || ro.PaymentFeeBps != 0 {
			t.Fatalf("post-success replay leaked fees to buyer: comm=%d pay_fee=%d pay_fee_bps=%d",
				ro.CommissionVND, ro.PaymentFeeVND, ro.PaymentFeeBps)
		}

		// Buyer JWT must remain denied on internal OrderService.MarkPaymentResult RPC.
		if _, err := orderGRPC.MarkPaymentResult(ordW2bAuthCtx(env.ctx(t), buyer.AccessToken), &orderv1.MarkPaymentResultRequest{
			CheckoutGroupId: results[0].out.CheckoutGroupID,
			OrderId:         results[0].out.Orders[0].ID,
			PaymentId:       results[0].out.PaymentID,
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
			Reason:          "buyer spoof attempt",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer JWT MarkPaymentResult: want PermissionDenied, got %v", err)
		}
	})

	// 9. Sold-out flash sale in preview falls back to active shop discount program with warnings;
	// corresponding checkout (without sold-out flash_sale_item_id) matches preview total exactly.
	t.Run("9_PreviewSoldOutFlash_FallsBackToShopProgram_MatchesCheckout", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2b-s9")
		buyer1 := env.registerVerifyAndLogin(t, "ordw2b-b9-1")
		buyer2 := env.registerVerifyAndLogin(t, "ordw2b-b9-2")
		addr1 := env.addAddress(t, buyer1)
		addr2 := env.addAddress(t, buyer2)

		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2B FlashFallback Item", 300000, 20, 400)
		now := time.Now().UTC()
		// Active shop discount program: 300,000 -> 250,000 (P_shop = 50,000).
		ordW2bCreateShopDiscountProgram(t, env, seller, sku.ID, 250000, 5, now.Add(-5*time.Minute), now.Add(2*time.Hour))
		// Active flash sale with only 1 total slot: 300,000 -> 180,000.
		_, fsItemID := ordW2bCreateFlashSaleWithApprovedItem(t, env, adminTok, seller, sku.ID, 180000, now.Add(-5*time.Minute), now.Add(45*time.Minute), 1, 1)

		q1 := env.calculateShippingQuote(t, buyer1.AccessToken, seller.ShopID, 400, 0)
		// Buyer1 consumes the sole flash sale slot.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer1.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-ff-b1-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr1.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1, FlashSaleItemID: fsItemID}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: q1.CarrierCode, ServiceCode: q1.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		q2 := env.calculateShippingQuote(t, buyer2.AccessToken, seller.ShopID, 400, 0)
		// Buyer2 previews with sold-out flash_sale_item_id -> falls back to shop program (250k) with warning!
		prevResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer2.AccessToken, nil, map[string]any{
			"shop_items": []map[string]any{
				{
					"shop_id":          seller.ShopID,
					"shipping_fee_vnd": q2.FeeVND,
					"items": []map[string]any{
						{
							"sku_id":             sku.ID,
							"quantity":           1,
							"flash_sale_item_id": fsItemID,
						},
					},
				},
			},
		}, http.StatusOK)
		var prevOut struct {
			Breakdown struct {
				TotalFlashSaleDiscountVND   int64    `json:"total_flash_sale_discount_vnd"`
				TotalShopProgramDiscountVND int64    `json:"total_shop_program_discount_vnd"`
				FinalPayableVND             int64    `json:"final_payable_vnd"`
				Warnings                    []string `json:"warnings"`
				Shops                       []struct {
					ShopID                    string   `json:"shop_id"`
					ShopProgramDiscountVND    int64    `json:"shop_program_discount_vnd"`
					AppliedShopProgramItemIDs []string `json:"applied_shop_program_item_ids"`
				} `json:"shops"`
			} `json:"breakdown"`
		}
		if err := DecodeJSON(prevResp, &prevOut); err != nil {
			t.Fatalf("decode sold-out flash preview: %v", err)
		}
		bd := prevOut.Breakdown
		wantPayable := int64(250000) + q2.FeeVND
		if bd.TotalFlashSaleDiscountVND != 0 || bd.TotalShopProgramDiscountVND != 50000 || bd.FinalPayableVND != wantPayable {
			t.Fatalf("sold-out flash preview must fall back to shop program: flash=%d p_shop=%d payable=%d (want 0/50000/%d) warnings=%v",
				bd.TotalFlashSaleDiscountVND, bd.TotalShopProgramDiscountVND, bd.FinalPayableVND, wantPayable, bd.Warnings)
		}
		if len(bd.Warnings) == 0 {
			t.Fatalf("expected non-empty warnings when flash slot is sold out in preview: %s", string(prevResp.Body))
		}
		if len(bd.Shops) != 1 || len(bd.Shops[0].AppliedShopProgramItemIDs) != 1 {
			t.Fatalf("expected 1 applied_shop_program_item_id on fallback preview, got %+v", bd.Shops)
		}

		// Checkout with sold-out flash_sale_item_id must be rejected (contracts §4.5).
		badCoResp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/api/v1/checkout", buyer2.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-ff-b2-bad-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr2.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1, FlashSaleItemID: fsItemID}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: q2.CarrierCode, ServiceCode: q2.ServiceCode},
			},
		})
		if err != nil {
			t.Fatalf("sold-out flash checkout request failed: %v", err)
		}
		if badCoResp.StatusCode < 400 || badCoResp.StatusCode >= 500 {
			t.Fatalf("expected 4xx rejecting sold-out flash_sale_item_id in checkout, got HTTP %d body=%s", badCoResp.StatusCode, string(badCoResp.Body))
		}

		// Corresponding checkout (without sold-out flash_sale_item_id) resolves shop program and matches preview total exactly.
		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer2.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2b-ff-b2-ok-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr2.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: q2.CarrierCode, ServiceCode: q2.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co ordW2bCheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode fallback checkout: %v", err)
		}
		if co.TotalPayableVND != bd.FinalPayableVND || len(co.Orders) != 1 ||
			co.Orders[0].ShopProgramDiscountVND != bd.TotalShopProgramDiscountVND ||
			co.Orders[0].FlashSaleDiscountVND != 0 {
			t.Fatalf("fallback checkout total mismatch vs preview: checkout=%+v preview=%+v", co, bd)
		}
	})
}
