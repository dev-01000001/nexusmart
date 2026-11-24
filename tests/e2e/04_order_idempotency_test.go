package e2e

import (
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestE2E_OrderIdempotency(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	cat, seller1, _ := env.ensureBaseMarketplace(t)
	prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "Idempotent SSD E2E", 150000, 50, 300)
	buyer := env.registerVerifyAndLogin(t, "buyer-idem")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 600, 300000)

	// Subtest 1: Sequential replay with identical Idempotency-Key.
	t.Run("SequentialReplay", func(t *testing.T) {
		stockBefore := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		idemKey := "idem-seq-" + uuid.NewString()
		reqBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}

		resp1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, reqBody, http.StatusCreated, http.StatusOK)
		var out1 CheckoutResponse
		if err := DecodeJSON(resp1, &out1); err != nil {
			t.Fatalf("decode first checkout: %v", err)
		}
		if out1.IdempotentReplay {
			t.Fatalf("expected idempotent_replay=false on first request")
		}
		if len(out1.Orders) != 1 {
			t.Fatalf("expected 1 order, got %d", len(out1.Orders))
		}

		resp2 := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, reqBody, http.StatusOK, http.StatusCreated)
		var out2 CheckoutResponse
		if err := DecodeJSON(resp2, &out2); err != nil {
			t.Fatalf("decode second checkout: %v", err)
		}
		if !out2.IdempotentReplay {
			t.Fatalf("expected idempotent_replay=true on duplicate Idempotency-Key replay (body=%s)", string(resp2.Body))
		}
		if out2.CheckoutGroupID != out1.CheckoutGroupID {
			t.Fatalf("replay created different checkout_group_id: first=%s second=%s", out1.CheckoutGroupID, out2.CheckoutGroupID)
		}
		if len(out2.Orders) != 1 || out2.Orders[0].ID != out1.Orders[0].ID {
			t.Fatalf("replay returned different orders: first=%+v second=%+v", out1.Orders, out2.Orders)
		}

		stockAfter := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		if stockAfter != stockBefore-2 {
			t.Fatalf("stock must be deducted only once (qty=2): before=%d after=%d", stockBefore, stockAfter)
		}
	})

	// Subtest 2: Concurrent burst with the exact same Idempotency-Key.
	t.Run("ConcurrentBurst", func(t *testing.T) {
		stockBefore := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		idemKey := "idem-conc-" + uuid.NewString()
		reqBody := CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 3}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}

		const concurrency = 5
		start := make(chan struct{})
		type result struct {
			status int
			out    CheckoutResponse
			body   string
			err    error
		}
		results := make([]result, concurrency)
		var wg sync.WaitGroup
		wg.Add(concurrency)

		for i := 0; i < concurrency; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				resp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
					"Idempotency-Key": idemKey,
				}, reqBody)
				if err != nil {
					results[idx] = result{err: err}
					return
				}
				r := result{status: resp.StatusCode, body: string(resp.Body)}
				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
					r.err = DecodeJSON(resp, &r.out)
				}
				results[idx] = r
			}(i)
		}

		close(start)
		wg.Wait()

		var canonicalGroupID, canonicalOrderID string
		successCount := 0
		for i, r := range results {
			if r.err != nil {
				t.Fatalf("concurrent checkout[%d] error: %v", i, r.err)
			}
			switch r.status {
			case http.StatusOK, http.StatusCreated:
				successCount++
				if r.out.CheckoutGroupID == "" || len(r.out.Orders) != 1 {
					t.Fatalf("concurrent checkout[%d] invalid payload: %s", i, r.body)
				}
				if canonicalGroupID == "" {
					canonicalGroupID = r.out.CheckoutGroupID
					canonicalOrderID = r.out.Orders[0].ID
				} else if r.out.CheckoutGroupID != canonicalGroupID || r.out.Orders[0].ID != canonicalOrderID {
					t.Fatalf("concurrent idempotency VIOLATION: goroutine %d got group=%s order=%s, want group=%s order=%s",
						i, r.out.CheckoutGroupID, r.out.Orders[0].ID, canonicalGroupID, canonicalOrderID)
				}
			case http.StatusConflict:
				// Acceptable for in-flight lock contention on simultaneous identical key.
			default:
				t.Fatalf("concurrent checkout[%d] unexpected HTTP %d: %s", i, r.status, r.body)
			}
		}
		if successCount == 0 {
			t.Fatalf("expected at least 1 successful response among %d concurrent checkouts", concurrency)
		}

		// Subsequent replay after burst must return the canonical checkout group and order.
		replayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, reqBody, http.StatusOK, http.StatusCreated)
		var replayOut CheckoutResponse
		if err := DecodeJSON(replayResp, &replayOut); err != nil {
			t.Fatalf("decode post-burst replay: %v", err)
		}
		if !replayOut.IdempotentReplay || replayOut.CheckoutGroupID != canonicalGroupID || replayOut.Orders[0].ID != canonicalOrderID {
			t.Fatalf("post-burst replay mismatch: got replay=%v group=%s order=%s, want group=%s order=%s",
				replayOut.IdempotentReplay, replayOut.CheckoutGroupID, replayOut.Orders[0].ID, canonicalGroupID, canonicalOrderID)
		}

		// Stock must be deducted strictly once (3 units, never 5*3=15).
		stockAfter := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		if stockAfter != stockBefore-3 {
			t.Fatalf("concurrent idempotency stock mismatch: before=%d after=%d (want %d)",
				stockBefore, stockAfter, stockBefore-3)
		}

		// Buyer should have exactly 2 orders total (1 from SequentialReplay + 1 from ConcurrentBurst).
		ordersResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?page=1&page_size=20", buyer.AccessToken, nil, nil, http.StatusOK)
		var ordersList OrdersListResponse
		if err := DecodeJSON(ordersResp, &ordersList); err != nil {
			t.Fatalf("decode buyer orders list: %v", err)
		}
		if ordersList.Total != 2 || len(ordersList.Items) != 2 {
			t.Fatalf("expected exactly 2 total orders for idempotency buyer, got total=%d len=%d",
				ordersList.Total, len(ordersList.Items))
		}
	})
}
