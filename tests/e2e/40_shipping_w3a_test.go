package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type shpW3ACarrierDTO struct {
	CarrierCode     string `json:"carrier_code"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	TokenConfigured bool   `json:"token_configured"`
}

type shpW3AListCarriersResponse struct {
	Carriers []shpW3ACarrierDTO `json:"carriers"`
}

type shpW3ACarrierResponse struct {
	Carrier shpW3ACarrierDTO `json:"carrier"`
}

type shpW3ATestCarrierResponse struct {
	Ok      bool   `json:"ok"`
	Message string `json:"message"`
}

type shpW3AShipmentDTO struct {
	ShipmentID   string `json:"shipment_id"`
	OrderID      string `json:"order_id"`
	CarrierCode  string `json:"carrier_code"`
	TrackingCode string `json:"tracking_code"`
	Status       string `json:"status"`
}

type shpW3AShipmentEventDTO struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	RawStatus   string `json:"raw_status"`
	Description string `json:"description"`
}

type shpW3AGetShipmentResponse struct {
	Shipment shpW3AShipmentDTO        `json:"shipment"`
	Events   []shpW3AShipmentEventDTO `json:"events"`
}

func shpW3APostWebhook(t *testing.T, env *testEnv, eventID, carrierCode, trackingCode, orderID, rawStatus, desc string, occurredAt time.Time) {
	t.Helper()
	resp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/shipping/"+url.PathEscape(carrierCode), ShippingWebhookPayload{
		EventID:      eventID,
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   occurredAt.UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("post shipping webhook (%s/%s): %v", carrierCode, rawStatus, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post shipping webhook (%s/%s) returned HTTP %d body=%s", carrierCode, rawStatus, resp.StatusCode, string(resp.Body))
	}
}

// TestE2E_ShippingWave3a verifies Wave 3a shipping guarantees against the live stack:
//  1. D9 outbox end-to-end smoke check: sequential shipping webhooks (picked -> delivering -> delivered,
//     plus duplicate & stale out-of-order webhooks) propagate over NATS JetStream so the order reaches
//     DELIVERED. Note: this subtest is ONLY an end-to-end wiring smoke test; D9/D9-bis concurrency,
//     out-of-tx lease locking, seq ordering, crash recovery, poison backoff, and NATS-down relay backoff
//     invariants are verified directly against real PostgreSQL in services/shipping/internal/{store,events}.
//  2. D5 secretbox carrier token encryption: existing/rotated GHN carrier tokens encrypted via
//     pkg/common/secretbox remain readable for admin connectivity checks without leaking plaintext.
func TestE2E_ShippingWave3a(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	adminTok := env.adminLogin(t)

	// End-to-end smoke test only (proves webhook -> outbox -> NATS -> order DELIVERED wiring on live stack);
	// lease/concurrency/ordering/backoff invariants of D9/D9-bis are covered by real-Postgres tests in services/shipping.
	t.Run("D9_WebhookOutboxOrderedDeliveryToOrder", func(t *testing.T) {
		buyer := env.registerVerifyAndLogin(t, "shpw3a-buyer")
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "ShipW3A Outbox Parcel", 180000, 10, 500)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 180000)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "shpw3a-co-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co CheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil || len(co.Orders) != 1 {
			t.Fatalf("decode checkout: out=%+v err=%v", co, err)
		}
		ord := co.Orders[0]

		confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ord.ID))
		env.mustDoJSON(t, http.MethodPost, confirmPath, seller1.AccessToken, nil, nil, http.StatusOK)

		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
		shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: quote.CarrierCode,
			ServiceCode: quote.ServiceCode,
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil || shipOut.TrackingCode == "" {
			t.Fatalf("decode ship response: out=%+v err=%v", shipOut, err)
		}

		// Fire rapid sequential webhooks for the same order aggregate plus a duplicate and a stale regression.
		t0 := time.Now().UTC()
		evtPicked := "shpw3a-pick-" + uuid.NewString()
		evtDelivered := "shpw3a-deliv-" + uuid.NewString()
		shpW3APostWebhook(t, env, evtPicked, quote.CarrierCode, shipOut.TrackingCode, ord.ID, "picked", "", t0)
		shpW3APostWebhook(t, env, evtPicked, quote.CarrierCode, shipOut.TrackingCode, ord.ID, "picked", "", t0) // duplicate event_id
		shpW3APostWebhook(t, env, evtDelivered, quote.CarrierCode, shipOut.TrackingCode, ord.ID, "delivered", "Giao hàng thành công", t0.Add(2*time.Second))
		shpW3APostWebhook(t, env, "shpw3a-stale-"+uuid.NewString(), quote.CarrierCode, shipOut.TrackingCode, ord.ID, "picking", "", t0.Add(5*time.Second)) // terminal regression

		// D9 outbox relay publishes ordered shipment.updated events -> order reaches DELIVERED.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order DELIVERED via D9 outbox", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ord.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})

		shResp := env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/shipping/shipments/%s?direction=FORWARD", url.PathEscape(ord.ID)),
			seller1.AccessToken, nil, nil, http.StatusOK)
		var shOut shpW3AGetShipmentResponse
		if err := DecodeJSON(shResp, &shOut); err != nil {
			t.Fatalf("decode shipment: %v", err)
		}
		if !StatusEquals(shOut.Shipment.Status, "DELIVERED") {
			t.Fatalf("expected shipment DELIVERED, got %q", shOut.Shipment.Status)
		}
		// Expect exactly 3 timeline events: created -> delivering (picked) -> delivered (duplicate & stale ignored).
		if len(shOut.Events) != 3 {
			t.Fatalf("expected 3 shipment timeline events (created, delivering, delivered), got %d: %+v", len(shOut.Events), shOut.Events)
		}
	})

	t.Run("D5_AdminCarrierEncryptedTokenRoundTrip", func(t *testing.T) {
		// 1. Existing seeded/pre-existing encrypted GHN token must be decryptable via pkg/common/secretbox.
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/carriers", adminTok, nil, nil, http.StatusOK)
		var listOut shpW3AListCarriersResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode admin carriers: %v", err)
		}
		var foundGHN bool
		for _, c := range listOut.Carriers {
			if strings.EqualFold(c.CarrierCode, "ghn") {
				foundGHN = true
				if !c.Enabled || !c.TokenConfigured {
					t.Fatalf("expected ghn enabled with token_configured=true, got %+v", c)
				}
			}
		}
		if !foundGHN {
			t.Fatalf("ghn carrier missing from admin list: %+v", listOut.Carriers)
		}

		preTestResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/carriers/ghn/test", adminTok, nil, nil, http.StatusOK)
		var preTest shpW3ATestCarrierResponse
		if err := DecodeJSON(preTestResp, &preTest); err != nil || !preTest.Ok {
			t.Fatalf("pre-update AdminTestCarrier(ghn) failed: out=%+v err=%v", preTest, err)
		}

		// 2. Rotate token via admin API, verify secretbox round-trip without leaking plaintext, and restore sandbox token.
		rotatedToken := "ghn-w3a-rotated-" + uuid.NewString()[:8]
		t.Cleanup(func() {
			_, _ = env.client.DoJSON(env.ctx(t), http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, nil, map[string]any{
				"enabled":   true,
				"api_token": "sandbox-ghn-token",
			})
		})

		updResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, nil, map[string]any{
			"enabled":   true,
			"api_token": rotatedToken,
		}, http.StatusOK)
		if strings.Contains(string(updResp.Body), rotatedToken) {
			t.Fatalf("PUT /api/v1/admin/carriers/ghn leaked plaintext api_token in response body")
		}
		var updOut shpW3ACarrierResponse
		if err := DecodeJSON(updResp, &updOut); err != nil || !updOut.Carrier.TokenConfigured || !updOut.Carrier.Enabled {
			t.Fatalf("unexpected PUT /api/v1/admin/carriers/ghn response: out=%+v err=%v", updOut, err)
		}

		postTestResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/carriers/ghn/test", adminTok, nil, nil, http.StatusOK)
		if strings.Contains(string(postTestResp.Body), rotatedToken) {
			t.Fatalf("POST /api/v1/admin/carriers/ghn/test leaked plaintext api_token in response body")
		}
		var postTest shpW3ATestCarrierResponse
		if err := DecodeJSON(postTestResp, &postTest); err != nil || !postTest.Ok {
			t.Fatalf("post-rotation AdminTestCarrier(ghn) failed: out=%+v err=%v", postTest, err)
		}
	})
}
