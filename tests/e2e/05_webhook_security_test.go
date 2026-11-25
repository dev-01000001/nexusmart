package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestE2E_PaymentWebhookIdempotencyAndSecurity(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	cat, seller1, _ := env.ensureBaseMarketplace(t)
	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "Webhook Security Router E2E", 320000, 30, 700)
	buyer := env.registerVerifyAndLogin(t, "buyer-webhook")
	addr := env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 700, 0)

	// Create a PENDING VietQR order.
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "wh-sec-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:  "MARKETPLACE",
		AddressID:     addr.ID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode VietQR checkout: %v", err)
	}
	if len(checkout.Orders) != 1 || checkout.PaymentID == "" {
		t.Fatalf("unexpected checkout response: %s", string(checkoutResp.Body))
	}
	orderID := checkout.Orders[0].ID

	ledgerBefore := env.getLedgerSummary(t)

	// 1. Invalid X-Signature and missing X-Signature MUST be rejected (401/403), payment stays PENDING.
	t.Run("RejectInvalidAndMissingSignature", func(t *testing.T) {
		payload := PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-BADSIG-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}

		badResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", payload, "wrong-hmac-secret", time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("invalid signature webhook request failed: %v", err)
		}
		if badResp.StatusCode != http.StatusUnauthorized && badResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected HTTP 401/403 for invalid X-Signature, got HTTP %d: %s", badResp.StatusCode, string(badResp.Body))
		}

		missingResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", payload, "", time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("missing signature webhook request failed: %v", err)
		}
		if missingResp.StatusCode != http.StatusUnauthorized && missingResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected HTTP 401/403 for missing X-Signature, got HTTP %d: %s", missingResp.StatusCode, string(missingResp.Body))
		}

		pay := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
		if !StatusEquals(pay.Status, "PENDING") {
			t.Fatalf("payment must remain PENDING after rejected signatures, got %q", pay.Status)
		}
	})

	// 2. Stale X-Timestamp (> 5 minutes old) MUST be rejected with 401, payment stays PENDING.
	t.Run("RejectStaleTimestamp", func(t *testing.T) {
		payload := PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-STALE-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}

		staleTime := time.Now().UTC().Add(-10 * time.Minute)
		staleResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", payload, env.cfg.PaymentWebhookSecret, staleTime, "")
		if err != nil {
			t.Fatalf("stale timestamp webhook request failed: %v", err)
		}
		if staleResp.StatusCode != http.StatusUnauthorized && staleResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected HTTP 401 for stale X-Timestamp (>5m), got HTTP %d: %s", staleResp.StatusCode, string(staleResp.Body))
		}

		pay := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
		if !StatusEquals(pay.Status, "PENDING") {
			t.Fatalf("payment must remain PENDING after stale timestamp rejection, got %q", pay.Status)
		}
	})

	// 3. Send the EXACT SAME valid HMAC webhook (same provider_tx_id) 10 times (5 concurrent + 5 sequential):
	// payment recorded ONCE, ledger posted ONCE, zero duplicate confirmed events.
	t.Run("Replay10Times_RecordedOnce", func(t *testing.T) {
		payload := PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-10X-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}

		type whOutcome struct {
			status int
			res    PaymentWebhookResult
			body   string
			err    error
		}
		outcomes := make([]whOutcome, 10)

		// First 5 deliveries concurrently.
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(5)
		for i := 0; i < 5; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				resp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", payload, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
				if err != nil {
					outcomes[idx] = whOutcome{err: err}
					return
				}
				o := whOutcome{status: resp.StatusCode, body: string(resp.Body)}
				if resp.StatusCode == http.StatusOK {
					o.err = DecodeJSON(resp, &o.res)
				}
				outcomes[idx] = o
			}(i)
		}
		close(start)
		wg.Wait()

		// Wait until payment confirmed & order CONFIRMED, then snapshot ledger before sequential repeats.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CONFIRMED after webhook", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, orderID)
			return StatusEquals(o.Status, "CONFIRMED"), fmt.Sprintf("status=%s", o.Status)
		})
		ledgerAfterFirstWave := env.getLedgerSummary(t)

		// Next 5 deliveries sequentially with the exact same provider_tx_id.
		for i := 5; i < 10; i++ {
			resp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", payload, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
			if err != nil {
				t.Fatalf("sequential webhook delivery #%d failed: %v", i+1, err)
			}
			o := whOutcome{status: resp.StatusCode, body: string(resp.Body)}
			if resp.StatusCode == http.StatusOK {
				o.err = DecodeJSON(resp, &o.res)
			}
			outcomes[i] = o
		}

		processedFresh := 0
		duplicates := 0
		for i, o := range outcomes {
			if o.err != nil {
				t.Fatalf("webhook delivery #%d failed: %v", i+1, o.err)
			}
			if o.status != http.StatusOK {
				t.Fatalf("webhook delivery #%d returned HTTP %d (want 200): %s", i+1, o.status, o.body)
			}
			if o.res.Duplicate {
				duplicates++
			} else {
				processedFresh++
			}
		}
		if processedFresh != 1 || duplicates != 9 {
			t.Fatalf("expected exactly 1 fresh processing and 9 duplicate responses out of 10 deliveries; got fresh=%d duplicate=%d",
				processedFresh, duplicates)
		}

		// Verify ledger posted ONCE (zero delta from repeats, and delta from before matches single payment).
		ledgerAfterAll10 := env.getLedgerSummary(t)
		if !ledgerAfterAll10.IsBalanced || ledgerAfterAll10.TotalDebitsVND != ledgerAfterAll10.TotalCreditsVND {
			t.Fatalf("ledger imbalanced after 10x webhook: debits=%d credits=%d balanced=%v",
				ledgerAfterAll10.TotalDebitsVND, ledgerAfterAll10.TotalCreditsVND, ledgerAfterAll10.IsBalanced)
		}
		if ledgerAfterAll10.TotalDebitsVND != ledgerAfterFirstWave.TotalDebitsVND {
			t.Fatalf("duplicate webhooks posted extra ledger entries! mid=%d after10=%d",
				ledgerAfterFirstWave.TotalDebitsVND, ledgerAfterAll10.TotalDebitsVND)
		}
		delta := ledgerAfterAll10.TotalDebitsVND - ledgerBefore.TotalDebitsVND
		if delta <= 0 || delta >= 2*checkout.TotalPayableVND {
			t.Fatalf("expected single payment ledger debit delta (~%d VND), got delta=%d (before=%d after=%d)",
				checkout.TotalPayableVND, delta, ledgerBefore.TotalDebitsVND, ledgerAfterAll10.TotalDebitsVND)
		}

		// Verify no duplicate CONFIRMED transitions on order timeline.
		tlPath := fmt.Sprintf("/api/v1/orders/%s/timeline", url.PathEscape(orderID))
		tlResp := env.mustDoJSON(t, http.MethodGet, tlPath, buyer.AccessToken, nil, nil, http.StatusOK)
		var tlOut OrderTimelineResponse
		if err := DecodeJSON(tlResp, &tlOut); err != nil {
			t.Fatalf("decode timeline: %v", err)
		}
		confirmedEvents := 0
		for _, ev := range tlOut.Events {
			if StatusEquals(ev.ToStatus, "CONFIRMED") {
				confirmedEvents++
			}
		}
		if confirmedEvents != 1 {
			t.Fatalf("expected exactly 1 CONFIRMED timeline transition after 10x webhook, got %d (events=%+v)",
				confirmedEvents, tlOut.Events)
		}
	})
}
