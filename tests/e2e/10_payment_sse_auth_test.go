package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type sseEventFrame struct {
	Event string
	Data  string
}

func openSSERequest(ctx context.Context, targetURL, token string) (*http.Response, error) {
	// Do not set a global http.Client Timeout on SSE streams; rely on request context cancellation.
	sseClient := &http.Client{}
	var totalWaited time.Duration
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create SSE request %s: %w", targetURL, err)
		}
		req.Header.Set("Accept", "text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := sseClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		wait := parseRetryAfter(resp.Header.Get("Retry-After")) + 250*time.Millisecond
		if totalWaited+wait > rateLimitMaxWait {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		totalWaited += wait

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait retry-after SSE %s: %w", targetURL, ctx.Err())
		case <-timer.C:
		}
	}
}

func assertSSEStatus(t *testing.T, targetURL, token string, wantStatus int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	resp, err := openSSERequest(ctx, targetURL, token)
	if err != nil {
		t.Fatalf("GET %s failed: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("GET %s returned HTTP %d (want %d): %s", targetURL, resp.StatusCode, wantStatus, string(body))
	}
}

func assertSSEFirstPendingEvent(t *testing.T, targetURL, token, wantPaymentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	resp, err := openSSERequest(ctx, targetURL, token)
	if err != nil {
		t.Fatalf("GET %s failed: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cancel before closing body (LIFO) so streaming connections unwind immediately.
	defer cancel()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("GET %s returned HTTP %d (want 200): %s", targetURL, resp.StatusCode, string(body))
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		t.Fatalf("GET %s Content-Type=%q, want prefix text/event-stream", targetURL, ct)
	}

	frame, err := readFirstSSEEvent(resp.Body)
	if err != nil {
		t.Fatalf("read first SSE event from %s: %v", targetURL, err)
	}
	if frame.Event != "payment.pending" {
		t.Fatalf("first SSE event=%q (want payment.pending), data=%s", frame.Event, frame.Data)
	}

	var payload struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
		t.Fatalf("decode SSE event data %q: %v", frame.Data, err)
	}
	if payload.PaymentID != wantPaymentID {
		t.Fatalf("SSE payload payment_id=%q (want %q), data=%s", payload.PaymentID, wantPaymentID, frame.Data)
	}
}

func readFirstSSEEvent(r io.Reader) (sseEventFrame, error) {
	scanner := bufio.NewScanner(r)
	var eventName string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ":") {
			continue
		}
		if line == "" {
			if eventName != "" || len(dataLines) > 0 {
				break
			}
			continue
		}
		if after, ok := strings.CutPrefix(line, "event:"); ok {
			eventName = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(after))
		}
	}
	if err := scanner.Err(); err != nil {
		return sseEventFrame{}, err
	}
	if eventName == "" || len(dataLines) == 0 {
		return sseEventFrame{}, fmt.Errorf("incomplete SSE frame: event=%q data=%q", eventName, strings.Join(dataLines, "\n"))
	}
	return sseEventFrame{
		Event: eventName,
		Data:  strings.Join(dataLines, "\n"),
	}, nil
}

func TestE2E_PaymentSSEAuthAndOwnership(t *testing.T) {
	env := getEnv()

	cat, seller1, _ := env.ensureBaseMarketplace(t)
	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "Payment SSE Auth E2E", 260000, 20, 500)
	owner := env.registerVerifyAndLogin(t, "buyer-sse-owner")
	otherBuyer := env.registerVerifyAndLogin(t, "buyer-sse-other")
	addr := env.addAddress(t, owner)
	quote := env.calculateShippingQuote(t, owner.AccessToken, seller1.ShopID, 500, 0)

	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", owner.AccessToken, map[string]string{
		"Idempotency-Key": "sse-auth-" + uuid.NewString(),
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

	escapedID := url.PathEscape(checkout.PaymentID)
	gatewaySSEURL := fmt.Sprintf("%s/api/v1/payments/%s/events", env.cfg.GatewayURL, escapedID)
	directSSEURL := fmt.Sprintf("%s/payments/%s/events", env.cfg.PaymentHTTPURL, escapedID)

	t.Run("OwnerViaGateway", func(t *testing.T) {
		assertSSEFirstPendingEvent(t, gatewaySSEURL, owner.AccessToken, checkout.PaymentID)
	})

	t.Run("OtherBuyerViaGatewayForbidden", func(t *testing.T) {
		assertSSEStatus(t, gatewaySSEURL, otherBuyer.AccessToken, http.StatusForbidden)
	})

	t.Run("DirectWithoutTokenUnauthorized", func(t *testing.T) {
		assertSSEStatus(t, directSSEURL, "", http.StatusUnauthorized)
	})

	t.Run("DirectOtherBuyerForbidden", func(t *testing.T) {
		assertSSEStatus(t, directSSEURL, otherBuyer.AccessToken, http.StatusForbidden)
	})

	t.Run("DirectOwnerAllowed", func(t *testing.T) {
		assertSSEFirstPendingEvent(t, directSSEURL, owner.AccessToken, checkout.PaymentID)
	})

	t.Run("DirectUnknownPaymentNotFound", func(t *testing.T) {
		unknownURL := fmt.Sprintf("%s/payments/%s/events", env.cfg.PaymentHTTPURL, uuid.NewString())
		assertSSEStatus(t, unknownURL, owner.AccessToken, http.StatusNotFound)
	})
}
