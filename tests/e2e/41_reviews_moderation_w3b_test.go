package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type w3b41ErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func w3b41AssertErrorCode(t *testing.T, label string, resp *RawResponse, wantCode string) {
	t.Helper()
	var envErr w3b41ErrorEnvelope
	if err := DecodeJSON(resp, &envErr); err != nil {
		t.Fatalf("%s: decode error envelope: %v (body=%s)", label, err, string(resp.Body))
	}
	if envErr.Error.Code != wantCode {
		t.Fatalf("%s: error.code = %q, want %q (body=%s)", label, envErr.Error.Code, wantCode, string(resp.Body))
	}
}

// w3b41AlphaTag maps hex chars in runID+seq into pure lowercase ASCII letters ('a'..'p') so unique
// test keywords never trigger leetspeak digit normalization (0->o, 1->i, 3->e, etc.).
func w3b41AlphaTag(env *testEnv) string {
	raw := fmt.Sprintf("%s%x", env.runID, env.nextSeq())
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= '0' && c <= '9':
			b.WriteByte('g' + (c - '0'))
		case c >= 'a' && c <= 'f':
			b.WriteByte(c)
		case c >= 'A' && c <= 'F':
			b.WriteByte('a' + (c - 'A'))
		default:
			b.WriteByte('z')
		}
	}
	return b.String()
}

// w3b41RedisCmd sends a minimal RESP command to Redis using stdlib net.TCP (zero extra dependencies).
func w3b41RedisCmd(t *testing.T, addr string, args ...string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial redis %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	var req strings.Builder
	fmt.Fprintf(&req, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&req, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := conn.Write([]byte(req.String())); err != nil {
		t.Fatalf("write redis command %v: %v", args[0], err)
	}
	rd := bufio.NewReader(conn)
	line, err := rd.ReadString('\n')
	if err != nil {
		t.Fatalf("read redis reply for %v: %v", args[0], err)
	}
	if strings.HasPrefix(line, "-") {
		t.Fatalf("redis error for %v: %s", args[0], strings.TrimSpace(line))
	}
	return strings.TrimSpace(line)
}

func w3b41HideProduct(t *testing.T, env *testEnv, seller *sellerFixture, prod ProductDTO) {
	t.Helper()
	skus := make([]map[string]any, 0, len(prod.SKUs))
	for _, sk := range prod.SKUs {
		skus = append(skus, map[string]any{
			"id":           sk.ID,
			"sku_code":     sk.SKUCode,
			"name":         sk.Name,
			"attributes":   sk.Attributes,
			"price_vnd":    sk.PriceVND,
			"stock":        sk.Stock,
			"weight_grams": sk.WeightGrams,
		})
	}
	path := fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prod.ID))
	env.mustDoJSON(t, http.MethodPut, path, seller.AccessToken, nil, map[string]any{
		"category_id": prod.CategoryID,
		"name":        prod.Name,
		"description": prod.Description,
		"images":      []string{"https://sandbox.nexusmart.local/img/p1.png"},
		"status":      "HIDDEN",
		"skus":        skus,
	}, http.StatusOK)
}

func w3b41CreateBannedKeyword(t *testing.T, env *testEnv, adminTok, term, category, action string) (string, string) {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/banned-keywords", adminTok, nil, map[string]any{
		"term":     term,
		"category": category,
		"action":   action,
	}, http.StatusCreated)
	var out struct {
		Keyword struct {
			ID             string `json:"id"`
			Term           string `json:"term"`
			NormalizedTerm string `json:"normalized_term"`
			Action         string `json:"action"`
			IsActive       bool   `json:"is_active"`
		} `json:"keyword"`
	}
	if err := DecodeJSON(resp, &out); err != nil || out.Keyword.ID == "" {
		t.Fatalf("decode create banned keyword: err=%v body=%s", err, string(resp.Body))
	}
	kwID := out.Keyword.ID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		delPath := fmt.Sprintf("/api/v1/admin/banned-keywords/%s", url.PathEscape(kwID))
		delResp, err := env.client.DoJSON(ctx, http.MethodDelete, delPath, adminTok, nil, nil)
		if err != nil {
			t.Errorf("cleanup delete banned keyword %s: %v", kwID, err)
			return
		}
		if delResp.StatusCode != http.StatusOK && delResp.StatusCode != http.StatusNotFound {
			t.Errorf("cleanup delete banned keyword %s returned HTTP %d: %s", kwID, delResp.StatusCode, string(delResp.Body))
		}
	})
	return kwID, out.Keyword.NormalizedTerm
}

func w3b41PlaceShipAndDeliverCOD(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	addr AddressDTO,
	seller *sellerFixture,
	sku SKUDTO,
) OrderDTO {
	t.Helper()
	env.verifyBuyerPhone(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, maxInt32(sku.WeightGrams, 300), sku.PriceVND)
	coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b41-cod-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:  "MARKETPLACE",
		AddressID:     addr.ID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)
	var co CheckoutResponse
	if err := DecodeJSON(coResp, &co); err != nil || len(co.Orders) != 1 {
		t.Fatalf("decode COD checkout: err=%v body=%s", err, string(coResp.Body))
	}
	ord := co.Orders[0]
	confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ord.ID))
	env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
		CarrierCode: quote.CarrierCode,
		ServiceCode: quote.ServiceCode,
	}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil || shipOut.TrackingCode == "" {
		t.Fatalf("decode seller ship: err=%v body=%s", err, string(shipResp.Body))
	}
	carrierCode := strings.ToLower(strings.TrimSpace(shipOut.Order.CarrierCode))
	if carrierCode == "" {
		carrierCode = strings.ToLower(strings.TrimSpace(quote.CarrierCode))
	}
	whResp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/shipping/"+url.PathEscape(carrierCode), ShippingWebhookPayload{
		EventID:      "w3b41-deliv-" + uuid.NewString(),
		TrackingCode: shipOut.TrackingCode,
		OrderID:      ord.ID,
		RawStatus:    "delivered",
		Description:  "Giao hàng thành công",
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil || whResp.StatusCode != http.StatusOK {
		t.Fatalf("post shipping delivered webhook: err=%v resp=%+v", err, whResp)
	}
	var delivered OrderDTO
	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "order DELIVERED", func() (bool, string) {
		delivered = env.getOrder(t, buyer.AccessToken, ord.ID)
		return StatusEquals(delivered.Status, "DELIVERED"), delivered.Status
	})
	return delivered
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// TestE2E_W3B_CartUnavailableAndCheckoutBlock verifies E3 and E4:
//   - Unavailable SKUs (SKU_INACTIVE from hidden product & SKU_NOT_FOUND from deleted/phantom Redis entry)
//     stay visible in GET /api/v1/cart with price_vnd=0, selected preserved, and excluded from totals.
//   - POST /api/v1/checkout (from_cart=true) rejects selected unavailable items with 409 SKU_INACTIVE,
//     never creating a 0-VND order or dropping items silently; stock and voucher remain intact.
//   - Removing unavailable items via DELETE or PATCH quantity=0 succeeds and unblocks checkout.
func TestE2E_W3B_CartUnavailableAndCheckoutBlock(t *testing.T) {
	env := getEnv()
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	buyer := env.registerVerifyAndLogin(t, "w3b41-cart-buyer")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	prodAvail1, skuAvail1 := env.createApprovedProduct(t, seller1, cat.ID, "W3B Cart Avail S1", 150000, 20, 400)
	prodHide1, skuHide1 := env.createApprovedProduct(t, seller1, cat.ID, "W3B Cart Hide S1", 250000, 20, 600)
	prodHide2, skuHide2 := env.createApprovedProduct(t, seller2, cat.ID, "W3B Cart Hide S2", 180000, 20, 500)

	shopVoucher := env.createShopVoucher(t, seller1, 15000, 100000, 5)
	env.saveVoucher(t, buyer.AccessToken, shopVoucher)

	// Add 3 SKUs across 2 shops to cart.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{SKUID: skuAvail1.ID, Quantity: 1}, http.StatusOK)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{SKUID: skuHide1.ID, Quantity: 2}, http.StatusOK)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{SKUID: skuHide2.ID, Quantity: 1}, http.StatusOK)

	// Hide 1 product in shop 1 and 1 product in shop 2.
	w3b41HideProduct(t, env, seller1, prodHide1)
	w3b41HideProduct(t, env, seller2, prodHide2)

	// 1. GET /api/v1/cart retains unavailable rows, flags SKU_INACTIVE, zeros price, excludes from totals.
	getCart := func() CartDTO {
		t.Helper()
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", buyer.AccessToken, nil, nil, http.StatusOK)
		var out CartResponse
		if err := DecodeJSON(resp, &out); err != nil {
			t.Fatalf("decode cart: %v", err)
		}
		return out.Cart
	}

	cart := getCart()
	if cart.TotalItems != 1 || cart.TotalVND != 150000 {
		t.Fatalf("E3: cart totals must include only active SKUs: got items=%d total_vnd=%d, want 1 / 150000", cart.TotalItems, cart.TotalVND)
	}
	foundBySKU := make(map[string]CartItemDTO)
	groupByShop := make(map[string]CartShopGroupDTO)
	for _, g := range cart.ShopGroups {
		groupByShop[g.ShopID] = g
		for _, it := range g.Items {
			foundBySKU[it.SKUID] = it
		}
	}
	if len(foundBySKU) != 3 {
		t.Fatalf("E3: cart must retain all 3 rows (1 active + 2 unavailable), got %d: %+v", len(foundBySKU), cart.ShopGroups)
	}
	if it := foundBySKU[skuAvail1.ID]; it.Unavailable || it.UnavailableReason != "" || it.PriceVND != 150000 || !it.Selected {
		t.Fatalf("E3: active item mismatch: %+v", it)
	}
	for _, hidID := range []string{skuHide1.ID, skuHide2.ID} {
		it := foundBySKU[hidID]
		if !it.Unavailable || it.UnavailableReason != "SKU_INACTIVE" || it.PriceVND != 0 || !it.Selected {
			t.Fatalf("E3: hidden SKU %s must be unavailable=true reason=SKU_INACTIVE price=0 selected=true, got %+v", hidID, it)
		}
	}
	if g1 := groupByShop[seller1.ShopID]; g1.SubtotalVND != 150000 || g1.TotalWeightGrams != 400 {
		t.Fatalf("E3: shop1 group totals must exclude hidden SKU: got subtotal=%d weight=%d, want 150000 / 400", g1.SubtotalVND, g1.TotalWeightGrams)
	}
	if g2 := groupByShop[seller2.ShopID]; g2.SubtotalVND != 0 || g2.TotalWeightGrams != 0 {
		t.Fatalf("E3: shop2 group with only hidden SKU must have subtotal=0 weight=0, got %+v", g2)
	}

	// 2. E4: Checkout from_cart=true with unavailable item -> 409 SKU_INACTIVE; stock, voucher & cart untouched.
	stockBefore := env.getProductSKU(t, prodAvail1.ID, skuAvail1.ID).AvailableStock
	voucherBefore := env.getVoucherRemainingQuota(t, buyer.AccessToken, "SHOP", shopVoucher.ID)
	quote1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 150000)

	blockResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b41-cart-block-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:   "MARKETPLACE",
		AddressID:      addr.ID,
		FromCart:       true,
		ShopVoucherIDs: map[string]string{seller1.ShopID: shopVoucher.ID},
		PaymentMethod:  "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
		},
	}, http.StatusConflict)
	w3b41AssertErrorCode(t, "checkout from_cart with hidden SKU", blockResp, "SKU_INACTIVE")

	if gotStock := env.getProductSKU(t, prodAvail1.ID, skuAvail1.ID).AvailableStock; gotStock != stockBefore {
		t.Fatalf("E4: blocked checkout altered stock: got %d, want %d", gotStock, stockBefore)
	}
	if gotV := env.getVoucherRemainingQuota(t, buyer.AccessToken, "SHOP", shopVoucher.ID); gotV != voucherBefore {
		t.Fatalf("E4: blocked checkout altered voucher quota: got %d, want %d", gotV, voucherBefore)
	}

	// 3. Remove skuHide2 via PATCH quantity=0; also verify hidden-only cart rejects with 409 SKU_INACTIVE (never 0-VND order).
	env.mustDoJSON(t, http.MethodPatch, "/api/v1/cart/items/"+url.PathEscape(skuHide2.ID), buyer.AccessToken, nil, map[string]any{
		"quantity": 0,
	}, http.StatusOK)

	// Temporarily remove skuAvail1 so cart contains ONLY hidden skuHide1.
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/cart/items/"+url.PathEscape(skuAvail1.ID), buyer.AccessToken, nil, nil, http.StatusOK)
	hiddenOnlyResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b41-cart-hiddenonly-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:  "MARKETPLACE",
		AddressID:     addr.ID,
		FromCart:      true,
		PaymentMethod: "COD",
	}, http.StatusConflict)
	w3b41AssertErrorCode(t, "checkout from_cart with only hidden SKU", hiddenOnlyResp, "SKU_INACTIVE")

	// Re-add skuAvail1, DELETE skuHide1 -> checkout from_cart=true succeeds for skuAvail1.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{SKUID: skuAvail1.ID, Quantity: 1}, http.StatusOK)
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/cart/items/"+url.PathEscape(skuHide1.ID), buyer.AccessToken, nil, nil, http.StatusOK)

	cleanCart := getCart()
	if cleanCart.TotalItems != 1 || cleanCart.TotalVND != 150000 {
		t.Fatalf("after removing hidden SKUs expected 1 item / 150000 VND, got %+v", cleanCart)
	}

	okCoResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b41-cart-ok-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:  "MARKETPLACE",
		AddressID:     addr.ID,
		FromCart:      true,
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)
	var okCo CheckoutResponse
	if err := DecodeJSON(okCoResp, &okCo); err != nil || len(okCo.Orders) != 1 || okCo.Orders[0].SubtotalVND != 150000 {
		t.Fatalf("checkout after cleaning unavailable SKUs failed: err=%v body=%s", err, string(okCoResp.Body))
	}

	// 4. E3: SKU_NOT_FOUND (phantom SKU in Redis hash not present in Product DB) is flagged and removable.
	phantomSKU := uuid.NewString()
	cartKey := "cart:" + buyer.UserID
	w3b41RedisCmd(t, env.cfg.RedisAddr, "HSET", cartKey, phantomSKU, `{"quantity":1,"added_price_vnd":99000,"selected":true}`)
	t.Cleanup(func() {
		conn, err := net.DialTimeout("tcp", env.cfg.RedisAddr, 3*time.Second)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		cmd := fmt.Sprintf("*3\r\n$4\r\nHDEL\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(cartKey), cartKey, len(phantomSKU), phantomSKU)
		_, _ = conn.Write([]byte(cmd))
	})

	phantomCart := getCart()
	if phantomCart.TotalItems != 0 || phantomCart.TotalVND != 0 {
		t.Fatalf("E3: phantom SKU_NOT_FOUND must not count toward totals, got %+v", phantomCart)
	}
	foundPhantom := false
	for _, g := range phantomCart.ShopGroups {
		for _, it := range g.Items {
			if it.SKUID == phantomSKU {
				foundPhantom = true
				if !it.Unavailable || it.UnavailableReason != "SKU_NOT_FOUND" || it.PriceVND != 0 {
					t.Fatalf("E3: phantom item expected unavailable=true reason=SKU_NOT_FOUND price=0, got %+v", it)
				}
			}
		}
	}
	if !foundPhantom {
		t.Fatalf("E3: phantom SKU %s missing from GET /api/v1/cart: %+v", phantomSKU, phantomCart)
	}

	env.mustDoJSON(t, http.MethodDelete, "/api/v1/cart/items/"+url.PathEscape(phantomSKU), buyer.AccessToken, nil, nil, http.StatusOK)
	afterDel := getCart()
	for _, g := range afterDel.ShopGroups {
		for _, it := range g.Items {
			if it.SKUID == phantomSKU {
				t.Fatalf("E3: phantom SKU %s persisted after DELETE", phantomSKU)
			}
		}
	}
}

// TestE2E_W3B_ProductReviewsAndPrivacy verifies E5, E6, E6-bis, E7, E8, E10 (review blocking), and E14:
//   - Completed order required (DELIVERED rejected), buyer-only ownership (buyer B & admin -> 403, anon -> 401).
//   - Buyer name masking ("Nguyễn Văn Linh" -> "N. Linh"), rating aggregate sync across product/list/reviews.
//   - Public GET /products/{id}/reviews omits buyer_id/order_id/order_item_id/sku_id (even with admin JWT),
//     whereas GET /orders/{id}/reviews returns full fields.
//   - Duplicate review -> 409; invalid image URLs -> 400; BLOCK & REVIEW banned keywords -> 400 BANNED_CONTENT.
//   - review.created event emitted to NATS stream NEXUSMART.
func TestE2E_W3B_ProductReviewsAndPrivacy(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)

	buyerA := env.registerVerifyAndLogin(t, "w3b41-rev-buyera")
	buyerB := env.registerVerifyAndLogin(t, "w3b41-rev-buyerb")
	addrA := env.addAddress(t, buyerA)

	// Set Vietnamese full name to verify E6 masking ("Nguyễn Văn Linh" -> "N. Linh").
	env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", buyerA.AccessToken, nil, map[string]any{
		"full_name": "Nguyễn Văn Linh",
	}, http.StatusOK)

	prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B Review Espresso "+env.runID, 220000, 20, 400)
	delivered := w3b41PlaceShipAndDeliverCOD(t, env, buyerA, addrA, seller1, sku)
	if len(delivered.Items) == 0 {
		t.Fatalf("delivered order %s has no items", delivered.ID)
	}
	orderItemID := delivered.Items[0].ID
	reviewPath := fmt.Sprintf("/api/v1/orders/%s/reviews", url.PathEscape(delivered.ID))

	// 1. Order in DELIVERED (not yet COMPLETED) -> rejected (409).
	env.mustDoJSON(t, http.MethodPost, reviewPath, buyerA.AccessToken, nil, map[string]any{
		"order_item_id": orderItemID,
		"rating":        5,
		"comment":       "Chưa xác nhận nhận hàng",
	}, http.StatusConflict, http.StatusBadRequest)

	// Transition order to COMPLETED.
	env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(delivered.ID)), buyerA.AccessToken, nil, nil, http.StatusOK)

	// 2. RBAC & URL validation checks.
	env.mustDoJSON(t, http.MethodPost, reviewPath, "", nil, map[string]any{
		"order_item_id": orderItemID, "rating": 5, "comment": "anon",
	}, http.StatusUnauthorized)
	env.mustDoJSON(t, http.MethodPost, reviewPath, buyerB.AccessToken, nil, map[string]any{
		"order_item_id": orderItemID, "rating": 5, "comment": "other buyer",
	}, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, map[string]any{
		"order_item_id": orderItemID, "rating": 5, "comment": "admin cannot review",
	}, http.StatusForbidden)

	for _, tc := range []struct {
		name string
		urls []string
	}{
		{"javascript_scheme", []string{"javascript:alert(1)"}},
		{"http_scheme", []string{"http://cdn.example.com/a.jpg"}},
		{"too_many_images", []string{
			"https://cdn.example.com/1.jpg",
			"https://cdn.example.com/2.jpg",
			"https://cdn.example.com/3.jpg",
			"https://cdn.example.com/4.jpg",
			"https://cdn.example.com/5.jpg",
			"https://cdn.example.com/6.jpg",
		}},
	} {
		env.mustDoJSON(t, http.MethodPost, reviewPath, buyerA.AccessToken, nil, map[string]any{
			"order_item_id": orderItemID,
			"rating":        5,
			"comment":       "bad image " + tc.name,
			"image_urls":    tc.urls,
		}, http.StatusBadRequest)
	}

	// 3. E10/E14: Reviews reject BOTH BLOCK (including obfuscated) and REVIEW keywords with 400 BANNED_CONTENT.
	tag := w3b41AlphaTag(env)
	blockTerm := "hangcam " + tag
	reviewTerm := "camket " + tag
	w3b41CreateBannedKeyword(t, env, adminTok, blockTerm, "e2e-review", "BLOCK")
	w3b41CreateBannedKeyword(t, env, adminTok, reviewTerm, "e2e-review", "REVIEW")

	respBlk := env.mustDoJSON(t, http.MethodPost, reviewPath, buyerA.AccessToken, nil, map[string]any{
		"order_item_id": orderItemID,
		"rating":        5,
		"comment":       "Sản phẩm chứa h.a.n.g.c.a.m " + tag + " không ổn",
	}, http.StatusBadRequest)
	w3b41AssertErrorCode(t, "review BLOCK keyword", respBlk, "BANNED_CONTENT")

	respRevKw := env.mustDoJSON(t, http.MethodPost, reviewPath, buyerA.AccessToken, nil, map[string]any{
		"order_item_id": orderItemID,
		"rating":        5,
		"comment":       "Chữ " + reviewTerm + " bị chặn trong đánh giá",
	}, http.StatusBadRequest)
	w3b41AssertErrorCode(t, "review REVIEW keyword", respRevKw, "BANNED_CONTENT")

	// 4. Valid 5-star review with both https:// and /products/... image URLs -> 201 + masked buyer_name.
	createResp := env.mustDoJSON(t, http.MethodPost, reviewPath, buyerA.AccessToken, nil, map[string]any{
		"order_item_id": orderItemID,
		"rating":        5,
		"comment":       "Cà phê thơm ngon, giao nhanh, đóng gói chắc chắn!",
		"image_urls":    []string{"https://cdn.example.com/review-espresso.jpg", "/products/review-espresso-2.jpg"},
	}, http.StatusCreated)
	var created struct {
		Review struct {
			ID          string   `json:"id"`
			ProductID   string   `json:"product_id"`
			OrderID     string   `json:"order_id"`
			OrderItemID string   `json:"order_item_id"`
			SKUID       string   `json:"sku_id"`
			SKUName     string   `json:"sku_name"`
			BuyerID     string   `json:"buyer_id"`
			BuyerName   string   `json:"buyer_name"`
			Rating      int32    `json:"rating"`
			ImageURLs   []string `json:"image_urls"`
		} `json:"review"`
	}
	if err := DecodeJSON(createResp, &created); err != nil {
		t.Fatalf("decode create review: %v (body=%s)", err, string(createResp.Body))
	}
	if created.Review.ID == "" || created.Review.BuyerName != "N. Linh" || created.Review.Rating != 5 || len(created.Review.ImageURLs) != 2 {
		t.Fatalf("E6: unexpected created review: %+v", created.Review)
	}
	if created.Review.OrderItemID != orderItemID || created.Review.SKUID != sku.ID || created.Review.BuyerID != buyerA.UserID {
		t.Fatalf("E6-bis: owner create response missing full fields: %+v", created.Review)
	}

	// Duplicate review on same order_item_id -> 409.
	env.mustDoJSON(t, http.MethodPost, reviewPath, buyerA.AccessToken, nil, map[string]any{
		"order_item_id": orderItemID,
		"rating":        4,
		"comment":       "Gửi trùng",
	}, http.StatusConflict)

	// 5. GET /orders/{id}/reviews returns full fields to buyerA, rejects buyerB.
	env.mustDoJSON(t, http.MethodGet, reviewPath, buyerB.AccessToken, nil, nil, http.StatusForbidden)
	ordRevResp := env.mustDoJSON(t, http.MethodGet, reviewPath, buyerA.AccessToken, nil, nil, http.StatusOK)
	var ordRevOut struct {
		Reviews []map[string]any `json:"reviews"`
	}
	if err := DecodeJSON(ordRevResp, &ordRevOut); err != nil || len(ordRevOut.Reviews) != 1 {
		t.Fatalf("decode GET /orders/{id}/reviews: err=%v body=%s", err, string(ordRevResp.Body))
	}
	for _, mustKey := range []string{"buyer_id", "order_id", "order_item_id", "sku_id", "sku_name"} {
		if v, ok := ordRevOut.Reviews[0][mustKey]; !ok || fmt.Sprint(v) == "" {
			t.Fatalf("E6-bis: GET /orders/{id}/reviews missing %q: %+v", mustKey, ordRevOut.Reviews[0])
		}
	}

	// 6. Public GET /products/{id}/reviews (both anonymous AND admin JWT) omits private IDs and syncs aggregate.
	pubRevPath := fmt.Sprintf("/api/v1/products/%s/reviews", url.PathEscape(prod.ID))
	for label, tok := range map[string]string{"anonymous": "", "admin_jwt": adminTok} {
		revResp := env.mustDoJSON(t, http.MethodGet, pubRevPath, tok, nil, nil, http.StatusOK)
		var revOut struct {
			Items         []map[string]any `json:"items"`
			RatingAvgX100 int32            `json:"rating_avg_x100"`
			ReviewCount   int32            `json:"review_count"`
			RatingCounts  map[string]int32 `json:"rating_counts"`
			Total         int64            `json:"total"`
		}
		if err := DecodeJSON(revResp, &revOut); err != nil {
			t.Fatalf("%s decode product reviews: %v", label, err)
		}
		if revOut.RatingAvgX100 != 500 || revOut.ReviewCount != 1 || revOut.Total != 1 || revOut.RatingCounts["5"] != 1 || len(revOut.Items) != 1 {
			t.Fatalf("%s product reviews aggregate mismatch: %+v", label, revOut)
		}
		item := revOut.Items[0]
		if item["buyer_name"] != "N. Linh" || fmt.Sprint(item["sku_name"]) == "" {
			t.Fatalf("%s public review missing buyer_name/sku_name: %+v", label, item)
		}
		for _, forbidden := range []string{"buyer_id", "order_id", "order_item_id", "sku_id"} {
			if _, exists := item[forbidden]; exists {
				t.Fatalf("E6-bis (%s): public product review leaked forbidden field %q: %+v", label, forbidden, item)
			}
		}
	}

	// Filter query params (rating=5&has_images=true vs rating=4).
	f5Resp := env.mustDoJSON(t, http.MethodGet, pubRevPath+"?rating=5&has_images=true", "", nil, nil, http.StatusOK)
	var f5 struct {
		Items []map[string]any `json:"items"`
	}
	if err := DecodeJSON(f5Resp, &f5); err != nil || len(f5.Items) != 1 {
		t.Fatalf("filtered product reviews (rating=5&has_images=true) want 1 item, got err=%v body=%s", err, string(f5Resp.Body))
	}
	f4Resp := env.mustDoJSON(t, http.MethodGet, pubRevPath+"?rating=4", "", nil, nil, http.StatusOK)
	var f4 struct {
		Items []map[string]any `json:"items"`
	}
	if err := DecodeJSON(f4Resp, &f4); err != nil || len(f4.Items) != 0 {
		t.Fatalf("filtered product reviews (rating=4) want 0 items, got err=%v body=%s", err, string(f4Resp.Body))
	}

	// 7. Public GET /products/{id}, /products/slug/{slug}, and /products list all show rating_avg_x100=500, review_count=1
	//    and NEVER leak commission_rate_bps or flagged_keywords (even when called with admin JWT!).
	checkPublicProductPayload := func(label string, body []byte, p ProductDTO) {
		t.Helper()
		if p.RatingAvgX100 != 500 || p.ReviewCount != 1 {
			t.Fatalf("%s: want rating_avg_x100=500 review_count=1, got %d / %d", label, p.RatingAvgX100, p.ReviewCount)
		}
		raw := string(body)
		if strings.Contains(raw, `"commission_rate_bps"`) {
			t.Fatalf("%s: public product route leaked commission_rate_bps: %s", label, raw)
		}
		if strings.Contains(raw, `"flagged_keywords"`) {
			t.Fatalf("%s: public product route leaked flagged_keywords: %s", label, raw)
		}
	}
	for label, tok := range map[string]string{"anon": "", "admin": adminTok} {
		pResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/"+url.PathEscape(prod.ID), tok, nil, nil, http.StatusOK)
		var pOut ProductResponse
		if err := DecodeJSON(pResp, &pOut); err != nil {
			t.Fatalf("decode GET /products/{id} (%s): %v", label, err)
		}
		checkPublicProductPayload("GET /products/{id} ("+label+")", pResp.Body, pOut.Product)

		if prod.Slug != "" {
			sResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/slug/"+url.PathEscape(prod.Slug), tok, nil, nil, http.StatusOK)
			var sOut ProductResponse
			if err := DecodeJSON(sResp, &sOut); err != nil {
				t.Fatalf("decode GET /products/slug/{slug} (%s): %v", label, err)
			}
			checkPublicProductPayload("GET /products/slug/{slug} ("+label+")", sResp.Body, sOut.Product)
		}
	}
	listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?shop_id="+url.PathEscape(seller1.ShopID)+"&page_size=50", "", nil, nil, http.StatusOK)
	var listOut struct {
		Items []ProductDTO `json:"items"`
	}
	if err := DecodeJSON(listResp, &listOut); err != nil {
		t.Fatalf("decode GET /products: %v", err)
	}
	foundInList := false
	for _, it := range listOut.Items {
		if it.ID == prod.ID {
			foundInList = true
			checkPublicProductPayload("GET /products", listResp.Body, it)
		}
	}
	if !foundInList {
		t.Fatalf("product %s not found in public GET /products?shop_id=%s", prod.ID, seller1.ShopID)
	}

	// 8. Verify review.created event landed on NATS JetStream NEXUSMART stream.
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "review.created event on NEXUSMART", func() (bool, string) {
		msg, err := w3bNATSGetLastMsgBySubject(env.cfg.NATSURL, "NEXUSMART", "review.created")
		if err != nil {
			return false, err.Error()
		}
		return strings.Contains(string(msg.Data), created.Review.ID), string(msg.Data)
	})
}

// TestE2E_W3B_BannedKeywordsProhibitedCategoriesAndRescan verifies E9, E9-bis, E10, E10-bis, E11,
// plus GET /api/v1/admin/audit-logs time range filtering. All keywords use run-unique terms cleaned up in t.Cleanup.
func TestE2E_W3B_BannedKeywordsProhibitedCategoriesAndRescan(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "w3b41-mod-buyer")

	// 1. Prohibited category tree + has_is_prohibited preservation on PUT.
	t.Run("ProhibitedCategoryTree", func(t *testing.T) {
		parentResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, CreateCategoryRequest{
			Name:              "W3B Prohibited Parent " + env.uniqueCode("PP"),
			Slug:              strings.ToLower(env.uniqueCode("w3bpp-")),
			CommissionRateBps: 500,
			SortOrder:         99,
			IsProhibited:      true,
		}, http.StatusCreated, http.StatusOK)
		var parentOut CategoryResponse
		if err := DecodeJSON(parentResp, &parentOut); err != nil || !parentOut.Category.IsProhibited {
			t.Fatalf("create prohibited parent failed: err=%v body=%s", err, string(parentResp.Body))
		}

		// PUT omitting is_prohibited must preserve true.
		updResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/categories/"+url.PathEscape(parentOut.Category.ID), adminTok, nil, map[string]any{
			"name": parentOut.Category.Name + " Edited",
		}, http.StatusOK)
		var updOut CategoryResponse
		if err := DecodeJSON(updResp, &updOut); err != nil || !updOut.Category.IsProhibited {
			t.Fatalf("PUT category omitting is_prohibited reset flag: err=%v body=%s", err, string(updResp.Body))
		}

		childResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, CreateCategoryRequest{
			Name:              "W3B Prohibited Child " + env.uniqueCode("PC"),
			Slug:              strings.ToLower(env.uniqueCode("w3bpc-")),
			ParentID:          parentOut.Category.ID,
			CommissionRateBps: 500,
			SortOrder:         1,
		}, http.StatusCreated, http.StatusOK)
		var childOut CategoryResponse
		if err := DecodeJSON(childResp, &childOut); err != nil || childOut.Category.ID == "" {
			t.Fatalf("create child category failed: err=%v body=%s", err, string(childResp.Body))
		}

		// Create product inside child of prohibited parent -> 400 PROHIBITED_CATEGORY.
		badCreate := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
			CategoryID:  childOut.Category.ID,
			Name:        "Product In Prohibited Subtree",
			Description: "Clean text",
			Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
			SKUs:        []SKUInputDTO{{SKUCode: env.uniqueCode("PRB"), Name: "S", PriceVND: 100000, Stock: 5, WeightGrams: 200}},
		}, http.StatusBadRequest)
		w3b41AssertErrorCode(t, "create product in prohibited subtree", badCreate, "PROHIBITED_CATEGORY")

		// Move an existing product into prohibited child -> 400 PROHIBITED_CATEGORY.
		cleanProd, cleanSKU := env.createApprovedProduct(t, seller1, cat.ID, "W3B Clean Move Test", 120000, 10, 300)
		badMove := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/products/"+url.PathEscape(cleanProd.ID), seller1.AccessToken, nil, map[string]any{
			"category_id": childOut.Category.ID,
			"name":        cleanProd.Name,
			"description": cleanProd.Description,
			"images":      []string{"https://sandbox.nexusmart.local/img/p1.png"},
			"skus": []map[string]any{{
				"id": cleanSKU.ID, "sku_code": cleanSKU.SKUCode, "name": cleanSKU.Name,
				"price_vnd": cleanSKU.PriceVND, "stock": cleanSKU.Stock, "weight_grams": cleanSKU.WeightGrams,
			}},
		}, http.StatusBadRequest)
		w3b41AssertErrorCode(t, "update product into prohibited subtree", badMove, "PROHIBITED_CATEGORY")
	})

	// 2. Banned keyword CRUD, RBAC, has_is_active, audit log time filter, evasion & REVIEW demotion + scoped rescan.
	t.Run("BannedKeywordCRUDModerationAndScopedRescan", func(t *testing.T) {
		// RBAC: anon 401, buyer 403, seller 403.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/banned-keywords", "", nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/banned-keywords", buyer.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/banned-keywords", seller1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/banned-keywords", seller1.AccessToken, nil, map[string]any{
			"term": "unauth", "action": "BLOCK",
		}, http.StatusForbidden)

		tag := w3b41AlphaTag(env)
		beforeAudit := time.Now().UTC().Add(-3 * time.Second).Format(time.RFC3339)
		blockTerm := "thuốc cấm " + tag
		kwBlockID, _ := w3b41CreateBannedKeyword(t, env, adminTok, blockTerm, "weapons", "BLOCK")

		// PUT omitting is_active must preserve is_active=true.
		updKWResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/banned-keywords/"+url.PathEscape(kwBlockID), adminTok, nil, map[string]any{
			"category": "restricted",
		}, http.StatusOK)
		var updKWOut struct {
			Keyword struct {
				IsActive bool   `json:"is_active"`
				Category string `json:"category"`
			} `json:"keyword"`
		}
		if err := DecodeJSON(updKWResp, &updKWOut); err != nil || !updKWOut.Keyword.IsActive || updKWOut.Keyword.Category != "restricted" {
			t.Fatalf("PUT banned-keyword omitting is_active broke state: err=%v body=%s", err, string(updKWResp.Body))
		}

		// Audit log with from_time/to_time finds banned_keyword entry; malformed from_time -> 400 INVALID_ARGUMENT.
		afterAudit := time.Now().UTC().Add(3 * time.Second).Format(time.RFC3339)
		auditPath := fmt.Sprintf(
			"/api/v1/admin/audit-logs?target_type=banned_keyword&target_id=%s&from_time=%s&to_time=%s",
			url.QueryEscape(kwBlockID), url.QueryEscape(beforeAudit), url.QueryEscape(afterAudit),
		)
		auditResp := env.mustDoJSON(t, http.MethodGet, auditPath, adminTok, nil, nil, http.StatusOK)
		var auditOut struct {
			Items []struct {
				TargetType string `json:"target_type"`
				TargetID   string `json:"target_id"`
			} `json:"items"`
		}
		if err := DecodeJSON(auditResp, &auditOut); err != nil || len(auditOut.Items) == 0 {
			t.Fatalf("expected banned_keyword audit log entry in time window, got err=%v body=%s", err, string(auditResp.Body))
		}
		badTimeResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/audit-logs?from_time=not-a-date", adminTok, nil, nil, http.StatusBadRequest)
		w3b41AssertErrorCode(t, "audit-logs invalid from_time", badTimeResp, "INVALID_ARGUMENT")

		// Evasion check: leetspeak/dot obfuscated BLOCK keyword rejected with 400 BANNED_CONTENT; clean text passes.
		evadeResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
			CategoryID:  cat.ID,
			Name:        "Hàng th.u.0.c c.a.m " + tag,
			Description: "Mô tả thử nghiệm",
			Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
			SKUs:        []SKUInputDTO{{SKUCode: env.uniqueCode("EVD"), Name: "M", PriceVND: 100000, Stock: 5, WeightGrams: 200}},
		}, http.StatusBadRequest)
		w3b41AssertErrorCode(t, "obfuscated BLOCK keyword", evadeResp, "BANNED_CONTENT")

		// Clean product with unrelated Vietnamese text passes.
		cleanProd, cleanSKU := env.createApprovedProduct(t, seller1, cat.ID, "Bút máy chính hãng "+tag, 120000, 15, 250)

		// E10/E10-bis: REVIEW keyword on edit demotes APPROVED -> PENDING; flagged_keywords visible to seller/admin, hidden on public.
		reviewTerm := "thần dược " + tag
		w3b41CreateBannedKeyword(t, env, adminTok, reviewTerm, "claims", "REVIEW")

		editResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/products/"+url.PathEscape(cleanProd.ID), seller1.AccessToken, nil, map[string]any{
			"category_id": cat.ID,
			"name":        cleanProd.Name,
			"description": "Quảng cáo " + reviewTerm + " đặc biệt",
			"images":      []string{"https://sandbox.nexusmart.local/img/p1.png"},
			"skus": []map[string]any{{
				"id": cleanSKU.ID, "sku_code": cleanSKU.SKUCode, "name": cleanSKU.Name,
				"price_vnd": cleanSKU.PriceVND, "stock": cleanSKU.Stock, "weight_grams": cleanSKU.WeightGrams,
			}},
		}, http.StatusOK)
		var editedOut ProductResponse
		if err := DecodeJSON(editResp, &editedOut); err != nil {
			t.Fatalf("decode edited REVIEW product: %v", err)
		}
		if !StatusEquals(editedOut.Product.Status, "PENDING") || len(editedOut.Product.FlaggedKeywords) == 0 {
			t.Fatalf("E10: expected PENDING with flagged_keywords, got status=%s flagged=%v", editedOut.Product.Status, editedOut.Product.FlaggedKeywords)
		}
		// Hidden from public storefront while PENDING.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/products/"+url.PathEscape(cleanProd.ID), "", nil, nil, http.StatusNotFound)
		// Visible with flagged_keywords to owner seller & admin.
		sellerGet := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/products/"+url.PathEscape(cleanProd.ID), seller1.AccessToken, nil, nil, http.StatusOK)
		adminGet := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/products/"+url.PathEscape(cleanProd.ID), adminTok, nil, nil, http.StatusOK)
		if !strings.Contains(string(sellerGet.Body), `"flagged_keywords"`) || !strings.Contains(string(adminGet.Body), `"flagged_keywords"`) {
			t.Fatalf("E10-bis: seller/admin must see flagged_keywords; seller=%s admin=%s", string(sellerGet.Body), string(adminGet.Body))
		}
		// Admin approves REVIEW-flagged product -> APPROVED; public route must omit flagged_keywords.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/products/"+url.PathEscape(cleanProd.ID)+"/review", adminTok, nil, ReviewRequest{
			Decision: "APPROVE",
			Reason:   "E2E verified REVIEW keyword false alarm",
		}, http.StatusOK)
		pubAfterApprove := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/"+url.PathEscape(cleanProd.ID), "", nil, nil, http.StatusOK)
		if strings.Contains(string(pubAfterApprove.Body), `"flagged_keywords"`) {
			t.Fatalf("E10-bis: public GET /products/{id} leaked flagged_keywords: %s", string(pubAfterApprove.Body))
		}

		// E10: AdminReviewProduct(APPROVE) rejects a PENDING product matching a newly added BLOCK keyword.
		pendingTerm := "vukhi " + tag
		pendResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
			CategoryID:  cat.ID,
			Name:        "Sản phẩm chờ duyệt " + pendingTerm,
			Description: "Tạo trước khi thêm từ cấm",
			Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
			SKUs:        []SKUInputDTO{{SKUCode: env.uniqueCode("PND"), Name: "S", PriceVND: 110000, Stock: 5, WeightGrams: 200}},
		}, http.StatusCreated, http.StatusOK)
		var pendOut ProductResponse
		if err := DecodeJSON(pendResp, &pendOut); err != nil {
			t.Fatalf("decode pending product: %v", err)
		}
		w3b41CreateBannedKeyword(t, env, adminTok, pendingTerm, "weapons", "BLOCK")
		badApprove := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/products/"+url.PathEscape(pendOut.Product.ID)+"/review", adminTok, nil, ReviewRequest{
			Decision: "APPROVE",
			Reason:   "Should fail due to BLOCK keyword",
		}, http.StatusBadRequest)
		w3b41AssertErrorCode(t, "admin approve product violating BLOCK keyword", badApprove, "BANNED_CONTENT")

		// E11: Preview + Scoped Rescan (dry_run=true -> dry_run=false -> repeat idempotent).
		rescanTerm := "hanggia " + tag
		rescanProd, _ := env.createApprovedProduct(t, seller1, cat.ID, "Đồng hồ "+rescanTerm, 300000, 10, 300)

		prevResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/banned-keywords/preview", adminTok, nil, map[string]any{
			"term": rescanTerm,
		}, http.StatusOK)
		var prevOut struct {
			AffectedCount int32 `json:"affected_count"`
			Samples       []struct {
				ProductID string `json:"product_id"`
			} `json:"samples"`
		}
		if err := DecodeJSON(prevResp, &prevOut); err != nil {
			t.Fatalf("decode preview: %v", err)
		}
		if prevOut.AffectedCount < 1 || len(prevOut.Samples) > 20 {
			t.Fatalf("E11 preview unexpected counts: affected=%d samples=%d", prevOut.AffectedCount, len(prevOut.Samples))
		}
		foundSample := false
		for _, s := range prevOut.Samples {
			if s.ProductID == rescanProd.ID {
				foundSample = true
			}
		}
		if !foundSample {
			t.Fatalf("E11 preview samples did not include %s: %+v", rescanProd.ID, prevOut.Samples)
		}

		kwRescanID, _ := w3b41CreateBannedKeyword(t, env, adminTok, rescanTerm, "counterfeit", "BLOCK")

		runScopedRescan := func(dryRun bool) (int32, int32) {
			t.Helper()
			var totalBlocked, totalFlagged int32
			cursor := ""
			hasMore := false
			for page := 0; page < 200; page++ {
				resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/banned-keywords/rescan", adminTok, nil, map[string]any{
					"keyword_ids": []string{kwRescanID},
					"dry_run":     dryRun,
					"cursor":      cursor,
					"limit":       500,
				}, http.StatusOK)
				var out struct {
					BlockedCount int32  `json:"blocked_count"`
					FlaggedCount int32  `json:"flagged_count"`
					HasMore      bool   `json:"has_more"`
					NextCursor   string `json:"next_cursor"`
				}
				if err := DecodeJSON(resp, &out); err != nil {
					t.Fatalf("decode rescan (dryRun=%v): %v", dryRun, err)
				}
				totalBlocked += out.BlockedCount
				totalFlagged += out.FlaggedCount
				hasMore = out.HasMore
				if !out.HasMore || out.NextCursor == "" {
					break
				}
				cursor = out.NextCursor
			}
			if hasMore {
				t.Fatalf("E11 rescan (dryRun=%v) exceeded 200 pages (limit=500) with has_more=true (cursor=%q)", dryRun, cursor)
			}
			return totalBlocked, totalFlagged
		}

		dryBlocked, _ := runScopedRescan(true)
		if dryBlocked < 1 {
			t.Fatalf("E11 dry_run rescan expected blocked_count >= 1, got %d", dryBlocked)
		}
		// Product remains APPROVED after dry_run=true.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/products/"+url.PathEscape(rescanProd.ID), "", nil, nil, http.StatusOK)

		realBlocked, _ := runScopedRescan(false)
		if realBlocked < 1 {
			t.Fatalf("E11 real rescan expected blocked_count >= 1, got %d", realBlocked)
		}
		admAfterRescan := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/products/"+url.PathEscape(rescanProd.ID), adminTok, nil, nil, http.StatusOK)
		var admProdAfter ProductResponse
		if err := DecodeJSON(admAfterRescan, &admProdAfter); err != nil || !StatusEquals(admProdAfter.Product.Status, "REJECTED") {
			t.Fatalf("E11: rescanProd must be REJECTED after rescan, got err=%v status=%s", err, admProdAfter.Product.Status)
		}

		// Verify product.reviewed event reached seller notifications.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "seller notification for rescan REJECT", func() (bool, string) {
			nResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=30", seller1.AccessToken, nil, nil, http.StatusOK)
			var nOut NotificationsListResponse
			if err := DecodeJSON(nResp, &nOut); err != nil {
				return false, err.Error()
			}
			for _, n := range nOut.Items {
				if strings.Contains(n.Title+" "+n.Body, rescanProd.Name) {
					return true, ""
				}
			}
			return false, fmt.Sprintf("notifications=%d", len(nOut.Items))
		})

		// Second real rescan must be idempotent (0 newly blocked, no duplicate product.reviewed).
		repeatBlocked, _ := runScopedRescan(false)
		if repeatBlocked != 0 {
			t.Fatalf("E11: second rescan must not re-block already REJECTED product, got blocked_count=%d", repeatBlocked)
		}
	})
}

// TestE2E_W3B_DiscontinuedProductStub verifies E13:
// - Previously approved HIDDEN product -> 200 {discontinued:true, category_id, name}, no SKUs/price.
// - Previously approved REJECTED or DELETED product -> 200 {discontinued:true, category_id}, no name, no SKUs.
// - Never-approved PENDING product & unknown slug -> 404.
func TestE2E_W3B_DiscontinuedProductStub(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)

	// (a) Approved -> HIDDEN.
	pHide, _ := env.createApprovedProduct(t, seller1, cat.ID, "W3B Discontinued Hide "+env.runID, 150000, 10, 300)
	w3b41HideProduct(t, env, seller1, pHide)
	hideResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/slug/"+url.PathEscape(pHide.Slug), "", nil, nil, http.StatusOK)
	var hideMap map[string]any
	if err := DecodeJSON(hideResp, &hideMap); err != nil {
		t.Fatalf("decode hidden stub: %v", err)
	}
	if hideMap["discontinued"] != true || hideMap["category_id"] != cat.ID || hideMap["name"] != pHide.Name {
		t.Fatalf("E13 HIDDEN stub mismatch: %+v", hideMap)
	}
	for _, forbidden := range []string{"product", "skus", "price_vnd", "min_price_vnd"} {
		if _, exists := hideMap[forbidden]; exists {
			t.Fatalf("E13 HIDDEN stub leaked %q: %+v", forbidden, hideMap)
		}
	}

	// (b) Approved -> REJECTED (name MUST be omitted).
	pRej, _ := env.createApprovedProduct(t, seller1, cat.ID, "W3B Discontinued Reject "+env.runID, 150000, 10, 300)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/products/"+url.PathEscape(pRej.ID)+"/review", adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "E13 policy takedown",
	}, http.StatusOK)
	rejResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/slug/"+url.PathEscape(pRej.Slug), "", nil, nil, http.StatusOK)
	var rejMap map[string]any
	if err := DecodeJSON(rejResp, &rejMap); err != nil {
		t.Fatalf("decode rejected stub: %v", err)
	}
	if rejMap["discontinued"] != true || rejMap["category_id"] != cat.ID {
		t.Fatalf("E13 REJECTED stub mismatch: %+v", rejMap)
	}
	if _, hasName := rejMap["name"]; hasName {
		t.Fatalf("E13 REJECTED stub MUST omit name, got %+v", rejMap)
	}

	// (c) Approved -> soft-deleted (name MUST be omitted).
	pDel, _ := env.createApprovedProduct(t, seller1, cat.ID, "W3B Discontinued Delete "+env.runID, 150000, 10, 300)
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/products/"+url.PathEscape(pDel.ID), seller1.AccessToken, nil, nil, http.StatusOK)
	delResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/slug/"+url.PathEscape(pDel.Slug), "", nil, nil, http.StatusOK)
	var delMap map[string]any
	if err := DecodeJSON(delResp, &delMap); err != nil {
		t.Fatalf("decode deleted stub: %v", err)
	}
	if delMap["discontinued"] != true || delMap["category_id"] != cat.ID {
		t.Fatalf("E13 DELETED stub mismatch: %+v", delMap)
	}
	if _, hasName := delMap["name"]; hasName {
		t.Fatalf("E13 DELETED stub MUST omit name, got %+v", delMap)
	}

	// (d) Never-approved PENDING product & unknown slug -> 404.
	pendResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
		CategoryID:  cat.ID,
		Name:        "W3B Never Approved " + env.uniqueCode("NA"),
		Description: "Never approved",
		Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
		SKUs:        []SKUInputDTO{{SKUCode: env.uniqueCode("NA"), Name: "S", PriceVND: 100000, Stock: 5, WeightGrams: 200}},
	}, http.StatusCreated, http.StatusOK)
	var pendOut ProductResponse
	if err := DecodeJSON(pendResp, &pendOut); err != nil || pendOut.Product.Slug == "" {
		t.Fatalf("decode never-approved product: err=%v body=%s", err, string(pendResp.Body))
	}
	env.mustDoJSON(t, http.MethodGet, "/api/v1/products/slug/"+url.PathEscape(pendOut.Product.Slug), "", nil, nil, http.StatusNotFound)
	env.mustDoJSON(t, http.MethodGet, "/api/v1/products/slug/does-not-exist-"+env.runID, "", nil, nil, http.StatusNotFound)
}

// TestE2E_W3B_ProductReviewAndModerationGRPCRBAC verifies contract-r0 gRPC auth rules:
// - ListProductReviews succeeds anonymously and rejects service tokens (PermissionDenied).
// - All mutating/private review and banned-keyword RPCs reject anonymous (Unauthenticated) and service tokens (PermissionDenied).
func TestE2E_W3B_ProductReviewAndModerationGRPCRBAC(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	prod, _ := env.createApprovedProduct(t, seller1, cat.ID, "W3B GRPC RBAC Prod", 100000, 10, 200)

	prodClient := prodW2BDialProduct(t, env)
	anonCtx := env.ctx(t)
	svcTok := promoMintServiceToken("order-service")
	svcCtx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+svcTok))

	// Public ListProductReviews: anonymous succeeds, service token -> PermissionDenied.
	if _, err := prodClient.ListProductReviews(anonCtx, &productv1.ListProductReviewsRequest{ProductId: prod.ID}); err != nil {
		t.Fatalf("anonymous ListProductReviews want nil, got %v", err)
	}
	if _, err := prodClient.ListProductReviews(svcCtx, &productv1.ListProductReviewsRequest{ProductId: prod.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service-token ListProductReviews want PermissionDenied, got %v", err)
	}

	assertAnonAndSvc := func(name string, call func(context.Context) error) {
		t.Helper()
		if err := call(anonCtx); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("%s (anonymous): want Unauthenticated, got %v", name, err)
		}
		if err := call(svcCtx); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s (service token): want PermissionDenied, got %v", name, err)
		}
	}

	dummyID := uuid.NewString()
	assertAnonAndSvc("CreateReview", func(c context.Context) error {
		_, err := prodClient.CreateReview(c, &productv1.CreateReviewRequest{OrderId: dummyID, OrderItemId: dummyID, Rating: 5})
		return err
	})
	assertAnonAndSvc("ListOrderReviews", func(c context.Context) error {
		_, err := prodClient.ListOrderReviews(c, &productv1.ListOrderReviewsRequest{OrderId: dummyID})
		return err
	})
	assertAnonAndSvc("ListBannedKeywords", func(c context.Context) error {
		_, err := prodClient.ListBannedKeywords(c, &productv1.ListBannedKeywordsRequest{})
		return err
	})
	assertAnonAndSvc("CreateBannedKeyword", func(c context.Context) error {
		_, err := prodClient.CreateBannedKeyword(c, &productv1.CreateBannedKeywordRequest{Term: "x", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK})
		return err
	})
	assertAnonAndSvc("UpdateBannedKeyword", func(c context.Context) error {
		_, err := prodClient.UpdateBannedKeyword(c, &productv1.UpdateBannedKeywordRequest{Id: dummyID, Term: "x"})
		return err
	})
	assertAnonAndSvc("DeleteBannedKeyword", func(c context.Context) error {
		_, err := prodClient.DeleteBannedKeyword(c, &productv1.DeleteBannedKeywordRequest{Id: dummyID})
		return err
	})
	assertAnonAndSvc("PreviewBannedKeyword", func(c context.Context) error {
		_, err := prodClient.PreviewBannedKeyword(c, &productv1.PreviewBannedKeywordRequest{Term: "x"})
		return err
	})
	assertAnonAndSvc("RescanProductsForBannedContent", func(c context.Context) error {
		_, err := prodClient.RescanProductsForBannedContent(c, &productv1.RescanProductsForBannedContentRequest{DryRun: true})
		return err
	})
}
