package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ordW3aErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

type ordW3aCheckoutOrderDTO struct {
	ID                         string `json:"id"`
	PlatformVoucherDiscountVND int64  `json:"platform_voucher_discount_vnd"`
}

type ordW3aCheckoutResponse struct {
	CheckoutGroupID string                   `json:"checkout_group_id"`
	Orders          []ordW3aCheckoutOrderDTO `json:"orders"`
}

func ordW3aAssertCheckoutFailed(t *testing.T, resp *RawResponse, wantReasonSubstr string) {
	t.Helper()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Fatalf("retry on FAILED checkout with persisted orders MUST NOT return %d OK (body=%s)", resp.StatusCode, string(resp.Body))
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected HTTP 409 Conflict for FAILED checkout retry, got HTTP %d (body=%s)", resp.StatusCode, string(resp.Body))
	}
	var errEnv ordW3aErrorEnvelope
	if err := DecodeJSON(resp, &errEnv); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, string(resp.Body))
	}
	msg := strings.ToUpper(errEnv.Error.Message)
	if !strings.Contains(msg, "CHECKOUT_FAILED") {
		t.Fatalf("expected CHECKOUT_FAILED in error message, got code=%q message=%q", errEnv.Error.Code, errEnv.Error.Message)
	}
	if wantReasonSubstr != "" && !strings.Contains(msg, strings.ToUpper(wantReasonSubstr)) {
		t.Fatalf("expected reason %q in error message, got code=%q message=%q", wantReasonSubstr, errEnv.Error.Code, errEnv.Error.Message)
	}
}

// ordW3aMintRolelessBuyerJWT signs an HS256 user JWT with sub=userID and empty role.
// Go services (order/user/product/shipping) accept any verified user JWT with non-empty sub,
// while payment's GrpcAuth rejects empty role with Unauthenticated — deterministically
// triggering a post-persist CreatePayment failure so order persists the cancelled order and
// marks checkout_groups.status='FAILED' for D11(iii) verification without touching the DB.
func ordW3aMintRolelessBuyerJWT(secret, userID string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":  userID,
		"role": "",
		"iss":  "nexusmart",
		"jti":  uuid.NewString(),
		"iat":  now,
		"exp":  now + 120,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func ordW3aCountBuyerOrders(t *testing.T, env *testEnv, buyerToken string) int64 {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?page=1&page_size=50", buyerToken, nil, nil, http.StatusOK)
	var list OrdersListResponse
	if err := DecodeJSON(resp, &list); err != nil {
		t.Fatalf("decode buyer orders list: %v", err)
	}
	return list.Total
}

// TestE2E_OrderWave3a covers Wave 3a order guarantees end-to-end:
//  1. D11 FAILED checkout group semantics:
//     - 1a–1c (non-regression guards — also passed pre-3a via partial unique index, verifying W3a did not
//     regress retryability of 0-order pre-persist failures): COD>20M -> VIETQR same key -> 201;
//     insufficient stock -> valid qty same key -> 201; 10 concurrent retries after 0-order FAILED -> 1 order.
//     - 1d (core Wave 3a D11(iii) guard — fails without W3a patch): FAILED checkout group WITH persisted
//     orders -> retrying with the SAME Idempotency-Key returns 409 CHECKOUT_FAILED (FAILED), leaves
//     buyer order count and SKU stock unchanged, while a fresh key succeeds.
//  2. IN_PROGRESS checkout group: concurrent retries with the same Idempotency-Key converge to one
//     canonical group/order/payment (mismatched payload returns 409 IDEMPOTENCY_KEY_CONFLICT);
//     client-disconnect retry recovers cleanly per D11 without duplicate orders or double stock deduction.
//  3. D3 end-to-end smoke: checkout with a platform voucher calls Promotion.ReservePromotion via
//     order-service's internal service token and succeeds cleanly.
func TestE2E_OrderWave3a(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "buyer-w3a-ord")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	// 1. D11: Pre-persist 0-order FAILED (non-regression 1a-1c) + post-persist FAILED-with-orders (core W3a 1d).
	t.Run("1_FailedCheckoutGroup_D11ZeroOrderRetryAndPersistedOrders409", func(t *testing.T) {
		ctx := env.ctx(t)

		// 1a (D11 non-regression): COD total > 20,000,000 VND -> pre-persist FAILED (0 orders) ->
		// switch to VIETQR on SAME key -> 201 ONE order.
		expProd, expensiveSKU := env.createApprovedProduct(t, seller1, cat.ID, "W3A HighValue Laptop", 12000000, 10, 1500)
		quoteExp := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 3000, 0)
		idemCOD := "w3a-d11-cod-vqr-" + uuid.NewString()

		overCODBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: expensiveSKU.ID, Quantity: 2}}, // 24M > 20M COD limit
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteExp.CarrierCode, ServiceCode: quoteExp.ServiceCode},
			},
		}
		firstResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemCOD,
		}, overCODBody)
		if err != nil {
			t.Fatalf("first over-COD checkout request error: %v", err)
		}
		if firstResp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 on over-COD checkout, got HTTP %d body=%s", firstResp.StatusCode, string(firstResp.Body))
		}

		stockBeforeExp := env.getProductSKU(t, expProd.ID, expensiveSKU.ID).AvailableStock
		vietqrBody := overCODBody
		vietqrBody.PaymentMethod = "VIETQR"
		retryVietqrResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemCOD,
		}, vietqrBody, http.StatusCreated, http.StatusOK)
		var vietqrOut CheckoutResponse
		if err := DecodeJSON(retryVietqrResp, &vietqrOut); err != nil {
			t.Fatalf("decode D11(i) VIETQR retry checkout: %v", err)
		}
		if vietqrOut.IdempotentReplay || len(vietqrOut.Orders) != 1 || vietqrOut.PaymentID == "" {
			t.Fatalf("D11(i) unexpected VIETQR retry response: %+v", vietqrOut)
		}
		if gotStock := env.getProductSKU(t, expProd.ID, expensiveSKU.ID).AvailableStock; gotStock != stockBeforeExp-2 {
			t.Fatalf("D11(i) stock mismatch: before=%d after=%d (want -2)", stockBeforeExp, gotStock)
		}

		// 1b (D11 non-regression): Insufficient stock -> pre-persist FAILED (0 orders) ->
		// fix quantity on SAME key -> 201 ONE order.
		lowProd, lowStockSKU := env.createApprovedProduct(t, seller1, cat.ID, "W3A LowStock Mouse", 200000, 1, 300)
		quoteLow := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 300, 200000)
		idemStock := "w3a-d11-stock-" + uuid.NewString()

		overStockBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: lowStockSKU.ID, Quantity: 5}}, // exceeds stock=1
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteLow.CarrierCode, ServiceCode: quoteLow.ServiceCode},
			},
		}
		stockFailResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemStock,
		}, overStockBody)
		if err != nil {
			t.Fatalf("over-stock checkout request error: %v", err)
		}
		if stockFailResp.StatusCode == http.StatusOK || stockFailResp.StatusCode == http.StatusCreated {
			t.Fatalf("expected failure on insufficient stock, got HTTP %d body=%s", stockFailResp.StatusCode, string(stockFailResp.Body))
		}

		validStockRetry := overStockBody
		validStockRetry.Items = []CheckoutItemInputDTO{{SKUID: lowStockSKU.ID, Quantity: 1}}
		stockRetryResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemStock,
		}, validStockRetry, http.StatusCreated, http.StatusOK)
		var stockOut CheckoutResponse
		if err := DecodeJSON(stockRetryResp, &stockOut); err != nil {
			t.Fatalf("decode D11(ii) fixed-qty retry checkout: %v", err)
		}
		if stockOut.IdempotentReplay || len(stockOut.Orders) != 1 {
			t.Fatalf("D11(ii) unexpected retry checkout response: %+v", stockOut)
		}
		if gotStock := env.getProductSKU(t, lowProd.ID, lowStockSKU.ID).AvailableStock; gotStock != 0 {
			t.Fatalf("D11(ii) expected stock=0 after single order, got %d", gotStock)
		}

		// 1c (D11(iv) concurrency guard): 10 concurrent goroutines calling checkout with the SAME Idempotency-Key
		// right after a 0-order FAILED attempt -> strictly ONE checkout_group_id / ONE order / single stock deduction.
		raceProd, raceSKU := env.createApprovedProduct(t, seller1, cat.ID, "W3A Race Watch", 12000000, 15, 400)
		quoteRace := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 0)
		idemRace := "w3a-d11-race10-" + uuid.NewString()

		raceFailResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemRace,
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: raceSKU.ID, Quantity: 2}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteRace.CarrierCode, ServiceCode: quoteRace.ServiceCode},
			},
		})
		if err != nil || raceFailResp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 on initial race over-COD attempt, got status=%v err=%v", raceFailResp, err)
		}

		stockBeforeRace := env.getProductSKU(t, raceProd.ID, raceSKU.ID).AvailableStock
		raceRetryBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: raceSKU.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteRace.CarrierCode, ServiceCode: quoteRace.ServiceCode},
			},
		}
		const concurrency = 10
		type raceOutcome struct {
			status int
			body   string
			out    CheckoutResponse
			err    error
		}
		raceOutcomes := make([]raceOutcome, concurrency)
		startGate := make(chan struct{})
		var raceWG sync.WaitGroup
		raceWG.Add(concurrency)
		for i := 0; i < concurrency; i++ {
			go func(idx int) {
				defer raceWG.Done()
				<-startGate
				r, e := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
					"Idempotency-Key": idemRace,
				}, raceRetryBody)
				if e != nil {
					raceOutcomes[idx] = raceOutcome{err: e}
					return
				}
				o := raceOutcome{status: r.StatusCode, body: string(r.Body)}
				if r.StatusCode == http.StatusOK || r.StatusCode == http.StatusCreated {
					o.err = DecodeJSON(r, &o.out)
				}
				raceOutcomes[idx] = o
			}(i)
		}
		close(startGate)
		raceWG.Wait()

		var canonGroup, canonOrder string
		for i, o := range raceOutcomes {
			if o.err != nil {
				t.Fatalf("D11(iv) race[%d] err: %v", i, o.err)
			}
			if o.status != http.StatusOK && o.status != http.StatusCreated {
				t.Fatalf("D11(iv) race[%d] expected 200/201, got HTTP %d body=%s", i, o.status, o.body)
			}
			if o.out.CheckoutGroupID == "" || len(o.out.Orders) != 1 {
				t.Fatalf("D11(iv) race[%d] invalid checkout response: %s", i, o.body)
			}
			if canonGroup == "" {
				canonGroup = o.out.CheckoutGroupID
				canonOrder = o.out.Orders[0].ID
			} else if o.out.CheckoutGroupID != canonGroup || o.out.Orders[0].ID != canonOrder {
				t.Fatalf("D11(iv) race[%d] created duplicate order set: got (%s,%s) want (%s,%s)",
					i, o.out.CheckoutGroupID, o.out.Orders[0].ID, canonGroup, canonOrder)
			}
		}
		if gotStock := env.getProductSKU(t, raceProd.ID, raceSKU.ID).AvailableStock; gotStock != stockBeforeRace-1 {
			t.Fatalf("D11(iv) stock deducted more than once: before=%d after=%d (want -1)", stockBeforeRace, gotStock)
		}

		// 1d (core Wave 3a D11(iii) guard — fails without W3a patch):
		// Persist order then fail CreatePayment deterministically -> checkout_group is FAILED WITH persisted order ->
		// retrying the SAME Idempotency-Key via Gateway MUST return 409 CHECKOUT_FAILED (FAILED) and keep
		// buyer order count and SKU stock unchanged; only a fresh Idempotency-Key may create a new order.
		d11Prod, d11SKU := env.createApprovedProduct(t, seller1, cat.ID, "W3A D11iii PersistedFail Headset", 300000, 10, 400)
		quoteD11 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 300000)
		idemPersisted := "w3a-d11-iii-persisted-" + uuid.NewString()

		ordersBeforeD11 := ordW3aCountBuyerOrders(t, env, buyer.AccessToken)
		stockBeforeD11 := env.getProductSKU(t, d11Prod.ID, d11SKU.ID).AvailableStock

		ordClient := ordW2bDialOrderGRPC(t)
		rolelessTok := ordW3aMintRolelessBuyerJWT(env.cfg.JWTSecret, buyer.UserID)
		_, grpcErr := ordClient.CreateCheckout(ordW2bAuthCtx(ctx, rolelessTok), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemPersisted,
			AddressId:      addr.ID,
			FromCart:       false,
			Items:          []*orderv1.CheckoutItemInput{{SkuId: d11SKU.ID, Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			ShippingChoices: []*orderv1.ShopShippingChoice{
				{ShopId: seller1.ShopID, CarrierCode: quoteD11.CarrierCode, ServiceCode: quoteD11.ServiceCode},
			},
		})
		if status.Code(grpcErr) != codes.Unauthenticated {
			t.Fatalf("D11(iii) setup expected Payment.CreatePayment Unauthenticated after PersistCheckoutBatch, got %v", grpcErr)
		}

		// Confirm 1 cancelled order was persisted and stock was cleanly released.
		ordersAfterFail := ordW3aCountBuyerOrders(t, env, buyer.AccessToken)
		if ordersAfterFail != ordersBeforeD11+1 {
			t.Fatalf("D11(iii) setup expected +1 persisted cancelled order, before=%d after=%d", ordersBeforeD11, ordersAfterFail)
		}
		if stockAfterFail := env.getProductSKU(t, d11Prod.ID, d11SKU.ID).AvailableStock; stockAfterFail != stockBeforeD11 {
			t.Fatalf("D11(iii) setup expected stock released back to %d, got %d", stockBeforeD11, stockAfterFail)
		}

		// Retry the EXACT SAME Idempotency-Key through Gateway with valid buyer JWT & valid COD payload:
		// MUST return 409 CHECKOUT_FAILED (FAILED) and NEVER create a second order set!
		retryBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: d11SKU.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteD11.CarrierCode, ServiceCode: quoteD11.ServiceCode},
			},
		}
		retryPersistedResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemPersisted,
		}, retryBody)
		if err != nil {
			t.Fatalf("D11(iii) retry request error: %v", err)
		}
		ordW3aAssertCheckoutFailed(t, retryPersistedResp, "FAILED")

		if ordersAfterRetry := ordW3aCountBuyerOrders(t, env, buyer.AccessToken); ordersAfterRetry != ordersAfterFail {
			t.Fatalf("D11(iii) violation: order count changed on retry (%d -> %d)", ordersAfterFail, ordersAfterRetry)
		}
		if stockAfterRetry := env.getProductSKU(t, d11Prod.ID, d11SKU.ID).AvailableStock; stockAfterRetry != stockBeforeD11 {
			t.Fatalf("D11(iii) violation: stock changed on rejected retry (%d -> %d)", stockBeforeD11, stockAfterRetry)
		}

		// Fresh Idempotency-Key succeeds cleanly and creates 1 new order.
		freshResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3a-d11-iii-fresh-" + uuid.NewString(),
		}, retryBody, http.StatusCreated, http.StatusOK)
		var freshOut CheckoutResponse
		if err := DecodeJSON(freshResp, &freshOut); err != nil || freshOut.IdempotentReplay || len(freshOut.Orders) != 1 {
			t.Fatalf("D11(iii) fresh key checkout failed: resp=%+v err=%v", freshOut, err)
		}
		if ordersAfterFresh := ordW3aCountBuyerOrders(t, env, buyer.AccessToken); ordersAfterFresh != ordersAfterFail+1 {
			t.Fatalf("expected +1 order on fresh key, got %d -> %d", ordersAfterFail, ordersAfterFresh)
		}
	})

	// 2. IN_PROGRESS checkout group: concurrent retry while running + recovery after client timeout.
	// Note: 2a verifies concurrent same-key convergence and payload-conflict rejection (non-regression + backoff).
	t.Run("2_InProgressCheckoutGroup_ConcurrentRetryAndRecovery", func(t *testing.T) {
		ctx := env.ctx(t)
		prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3A InProgress Keyboard", 350000, 30, 600)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 1200, 0)

		// 2a. Staggered concurrent retries with identical Idempotency-Key while checkout is IN_PROGRESS,
		//     plus a conflicting payload sharing the same key -> matching retries converge to the single
		//     canonical group/order/payment; conflicting payload gets 409 IDEMPOTENCY_KEY_CONFLICT.
		stockBefore := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		idemInProg := "w3a-inprog-" + uuid.NewString()
		baseBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}
		conflictBody := baseBody
		conflictBody.Items = []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 3}}

		type callOutcome struct {
			status int
			body   string
			out    CheckoutResponse
			err    error
		}
		const matchingCalls = 4
		outcomes := make([]callOutcome, matchingCalls)

		var wg sync.WaitGroup
		wg.Add(matchingCalls)
		for i := 0; i < matchingCalls; i++ {
			go func(idx int) {
				defer wg.Done()
				if idx > 0 {
					time.Sleep(time.Duration(idx*15) * time.Millisecond)
				}
				resp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
					"Idempotency-Key": idemInProg,
				}, baseBody)
				if err != nil {
					outcomes[idx] = callOutcome{err: err}
					return
				}
				o := callOutcome{status: resp.StatusCode, body: string(resp.Body)}
				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
					o.err = DecodeJSON(resp, &o.out)
				}
				outcomes[idx] = o
			}(i)
		}
		wg.Wait()

		var canonGroup, canonOrder, canonPay string
		replayCount := 0
		for i, o := range outcomes {
			if o.err != nil {
				t.Fatalf("in-progress retry[%d] err: %v", i, o.err)
			}
			if o.status != http.StatusOK && o.status != http.StatusCreated {
				t.Fatalf("in-progress retry[%d] expected 200/201, got HTTP %d body=%s", i, o.status, o.body)
			}
			if o.out.CheckoutGroupID == "" || o.out.PaymentID == "" || len(o.out.Orders) != 1 {
				t.Fatalf("in-progress retry[%d] incomplete checkout payload: %s", i, o.body)
			}
			if canonGroup == "" {
				canonGroup = o.out.CheckoutGroupID
				canonOrder = o.out.Orders[0].ID
				canonPay = o.out.PaymentID
			} else if o.out.CheckoutGroupID != canonGroup || o.out.Orders[0].ID != canonOrder || o.out.PaymentID != canonPay {
				t.Fatalf("in-progress retry[%d] diverged: got (%s,%s,%s) want (%s,%s,%s)",
					i, o.out.CheckoutGroupID, o.out.Orders[0].ID, o.out.PaymentID, canonGroup, canonOrder, canonPay)
			}
			if o.out.IdempotentReplay {
				replayCount++
			}
		}
		if replayCount == 0 {
			t.Fatalf("expected at least one staggered retry to return idempotent_replay=true")
		}
		conflictResp, conflictErr := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemInProg,
		}, conflictBody)
		if conflictErr != nil {
			t.Fatalf("conflicting payload request err: %v", conflictErr)
		}
		if conflictResp.StatusCode != http.StatusConflict || !strings.Contains(strings.ToUpper(string(conflictResp.Body)), "IDEMPOTENCY_KEY_CONFLICT") {
			t.Fatalf("expected 409 IDEMPOTENCY_KEY_CONFLICT for mismatched payload on same key, got HTTP %d body=%s",
				conflictResp.StatusCode, string(conflictResp.Body))
		}

		stockAfter := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		if stockAfter != stockBefore-2 {
			t.Fatalf("stock must be reserved strictly once (qty=2): before=%d after=%d", stockBefore, stockAfter)
		}

		// 2b. Sequential idempotent replay stability on a separate key: two sequential calls with the same
		// Idempotency-Key return identical checkout_group_id/order_id/payment_id, set idempotent_replay=true
		// on the second call, and deduct stock strictly once.
		stockBeforeSeq := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		idemSeq := "w3a-seq-replay-" + uuid.NewString()
		seqBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}
		firstSeqResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemSeq,
		}, seqBody, http.StatusCreated, http.StatusOK)
		var firstSeqOut CheckoutResponse
		if err := DecodeJSON(firstSeqResp, &firstSeqOut); err != nil {
			t.Fatalf("decode first sequential checkout: %v", err)
		}
		if firstSeqOut.CheckoutGroupID == "" || len(firstSeqOut.Orders) != 1 || firstSeqOut.PaymentID == "" {
			t.Fatalf("incomplete first sequential checkout: %s", string(firstSeqResp.Body))
		}
		replayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemSeq,
		}, seqBody, http.StatusOK, http.StatusCreated)
		var replayOut CheckoutResponse
		if err := DecodeJSON(replayResp, &replayOut); err != nil {
			t.Fatalf("decode sequential replay: %v", err)
		}
		if !replayOut.IdempotentReplay || replayOut.CheckoutGroupID != firstSeqOut.CheckoutGroupID ||
			replayOut.Orders[0].ID != firstSeqOut.Orders[0].ID || replayOut.PaymentID != firstSeqOut.PaymentID {
			t.Fatalf("sequential replay mismatch: got %+v want group=%s order=%s pay=%s",
				replayOut, firstSeqOut.CheckoutGroupID, firstSeqOut.Orders[0].ID, firstSeqOut.PaymentID)
		}
		stockAfterSeq := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		if stockAfterSeq != stockBeforeSeq-1 {
			t.Fatalf("sequential replay stock mismatch: before=%d after=%d (want -1)", stockBeforeSeq, stockAfterSeq)
		}
	})

	// 3. D3 smoke: checkout with a platform voucher calls Promotion.ReservePromotion using order-service token.
	t.Run("3_D3_CheckoutWithPlatformVoucherSucceedsViaOrderServiceToken", func(t *testing.T) {
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3A Promo Item", 400000, 10, 500)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 400000)
		v := env.createAdminVoucher(t, "PLATFORM", 40000, 200000, 10)
		env.saveVoucher(t, buyer.AccessToken, v)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3a-d3-voucher-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:         addr.ID,
			FromCart:          false,
			Items:             []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PlatformVoucherID: v.ID,
			PaymentMethod:     "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var co ordW3aCheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode D3 voucher checkout: %v", err)
		}
		if len(co.Orders) != 1 || co.Orders[0].PlatformVoucherDiscountVND != 40000 {
			t.Fatalf("expected 40,000 VND platform voucher discount via D3 service-token ReservePromotion, got %+v", co.Orders)
		}
	})

	// 4. Buy Now (from_cart=false) MUST NOT remove items from the buyer's cart even when the
	//    same SKU is in cart, whereas from_cart=true clears the ordered SKU from cart.
	t.Run("4_BuyNowDoesNotClearCart_FromCartClearsCart", func(t *testing.T) {
		cartBuyer := env.registerVerifyAndLogin(t, "buyer-w3a-buynow-cart")
		env.verifyBuyerPhone(t, cartBuyer)
		cartAddr := env.addAddress(t, cartBuyer)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3A BuyNow Cart Item", 180000, 20, 400)

		// Put SKU X (qty=2) into buyer's cart.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", cartBuyer.AccessToken, nil, AddCartItemRequest{
			SKUID:    sku.ID,
			Quantity: 2,
		}, http.StatusOK)

		readCartQty := func() (int32, int) {
			t.Helper()
			cResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", cartBuyer.AccessToken, nil, nil, http.StatusOK)
			var out CartResponse
			if err := DecodeJSON(cResp, &out); err != nil {
				t.Fatalf("decode cart: %v", err)
			}
			var qty int32
			for _, g := range out.Cart.ShopGroups {
				for _, it := range g.Items {
					if it.SKUID == sku.ID {
						qty += it.Quantity
					}
				}
			}
			return qty, int(out.Cart.TotalItems)
		}

		if qty, _ := readCartQty(); qty != 2 {
			t.Fatalf("expected SKU %s qty=2 in cart before Buy Now, got %d", sku.ID, qty)
		}

		// Buy Now (from_cart=false) for SKU X qty=1.
		quoteBuyNow := env.calculateShippingQuote(t, cartBuyer.AccessToken, seller1.ShopID, 400, 180000)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", cartBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3a-buynow-keep-cart-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     cartAddr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteBuyNow.CarrierCode, ServiceCode: quoteBuyNow.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		// Cart MUST still contain SKU X with qty=2!
		if qty, total := readCartQty(); qty != 2 || total < 1 {
			t.Fatalf("Buy Now (from_cart=false) wiped cart item: want sku qty=2, got qty=%d total_items=%d", qty, total)
		}

		// Contrast: checkout from cart (from_cart=true) MUST clear SKU X from cart.
		quoteCart := env.calculateShippingQuote(t, cartBuyer.AccessToken, seller1.ShopID, 800, 360000)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", cartBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3a-fromcart-clear-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     cartAddr.ID,
			FromCart:      true,
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteCart.CarrierCode, ServiceCode: quoteCart.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		if qty, total := readCartQty(); qty != 0 || total != 0 {
			t.Fatalf("from_cart=true checkout expected cart cleared, got sku qty=%d total_items=%d", qty, total)
		}
	})
}
