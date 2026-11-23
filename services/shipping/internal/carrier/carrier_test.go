package carrier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"
)

type stubCarrier struct {
	code         string
	name         string
	feeFn        func(ctx context.Context, req FeeRequest) ([]Quote, error)
	createFn     func(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error)
	cancelFn     func(ctx context.Context, trackingCode string) error
	statusFn     func(ctx context.Context, trackingCode string) (*TrackingStatus, error)
	redeliveryFn func(ctx context.Context, trackingCode, preferredDate, note string) error
	testConnFn   func(ctx context.Context, token string) error
	feeCalls     atomic.Int32
	createCalls  atomic.Int32
}

func (s *stubCarrier) Code() string { return s.code }
func (s *stubCarrier) Name() string { return s.name }
func (s *stubCarrier) IsMock() bool { return false }

func (s *stubCarrier) CalculateFee(ctx context.Context, req FeeRequest) ([]Quote, error) {
	s.feeCalls.Add(1)
	if s.feeFn != nil {
		return s.feeFn(ctx, req)
	}
	return nil, nil
}

func (s *stubCarrier) CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error) {
	s.createCalls.Add(1)
	if s.createFn != nil {
		return s.createFn(ctx, req)
	}
	return &ShipmentResult{CarrierCode: s.code, TrackingCode: "TRK-1", Status: StatusCreated}, nil
}

func (s *stubCarrier) CancelShipment(ctx context.Context, trackingCode string) error {
	if s.cancelFn != nil {
		return s.cancelFn(ctx, trackingCode)
	}
	return nil
}

func (s *stubCarrier) GetStatus(ctx context.Context, trackingCode string) (*TrackingStatus, error) {
	if s.statusFn != nil {
		return s.statusFn(ctx, trackingCode)
	}
	return &TrackingStatus{TrackingCode: trackingCode}, nil
}

func (s *stubCarrier) RequestRedelivery(ctx context.Context, trackingCode, preferredDate, note string) error {
	if s.redeliveryFn != nil {
		return s.redeliveryFn(ctx, trackingCode, preferredDate, note)
	}
	return nil
}

func (s *stubCarrier) TestConnection(ctx context.Context, token string) error {
	if s.testConnFn != nil {
		return s.testConnFn(ctx, token)
	}
	return nil
}

func (s *stubCarrier) SetToken(_ string) {}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestResilientCarrierRetryAndBackoff(t *testing.T) {
	var attempts atomic.Int32
	stub := &stubCarrier{
		code: "flaky",
		name: "Flaky Express",
		feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
			n := attempts.Add(1)
			if n < 3 {
				return nil, errors.New("temporary 503")
			}
			return []Quote{{CarrierCode: "flaky", ServiceCode: "STD", FeeVND: 20000, EstimatedDays: 2}}, nil
		},
	}

	rc := NewResilientCarrier(stub, ResilienceConfig{
		AttemptTimeout: 500 * time.Millisecond,
		MaxAttempts:    3,
		BaseBackoff:    15 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		CBMaxFailures:  5,
		CBOpenTimeout:  time.Second,
		Logger:         discardLogger(),
	})

	start := time.Now()
	quotes, err := rc.CalculateFee(context.Background(), FeeRequest{WeightGrams: 500})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected eventual success on 3rd attempt, got err=%v", err)
	}
	if len(quotes) != 1 || quotes[0].FeeVND != 20000 {
		t.Fatalf("unexpected quotes: %+v", quotes)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("expected backoff delay between retries, elapsed=%v", elapsed)
	}
}

func TestResilientCarrierCircuitBreakerTripsAndRecovers(t *testing.T) {
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	stub := &stubCarrier{
		code: "down",
		name: "Down Express",
		feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
			if shouldFail.Load() {
				return nil, errors.New("upstream unavailable")
			}
			return []Quote{{CarrierCode: "down", ServiceCode: "STD", FeeVND: 21000, EstimatedDays: 2}}, nil
		},
	}

	rc := NewResilientCarrier(stub, ResilienceConfig{
		AttemptTimeout:  200 * time.Millisecond,
		MaxAttempts:     3,
		BaseBackoff:     5 * time.Millisecond,
		MaxBackoff:      20 * time.Millisecond,
		CBMaxFailures:   2,
		CBOpenTimeout:   60 * time.Millisecond,
		CBHalfOpenLimit: 1,
		Logger:          discardLogger(),
	})

	// First call: 2 consecutive failures trip the breaker; 3rd retry hits ErrOpenState immediately.
	_, err := rc.CalculateFee(context.Background(), FeeRequest{})
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected ErrOpenState after 2 failures, got %v", err)
	}
	if rc.State() != gobreaker.StateOpen {
		t.Fatalf("expected circuit breaker Open, got %s", rc.State())
	}
	callsBefore := stub.feeCalls.Load()

	// Subsequent call while open must short-circuit without calling underlying carrier.
	_, err = rc.CalculateFee(context.Background(), FeeRequest{})
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected ErrOpenState while open, got %v", err)
	}
	if stub.feeCalls.Load() != callsBefore {
		t.Fatalf("expected zero underlying calls while circuit is open")
	}

	// Wait for open timeout -> half-open -> recovers when upstream succeeds.
	shouldFail.Store(false)
	time.Sleep(80 * time.Millisecond)

	quotes, err := rc.CalculateFee(context.Background(), FeeRequest{})
	if err != nil {
		t.Fatalf("expected circuit breaker recovery in half-open state, got %v", err)
	}
	if len(quotes) != 1 || rc.State() != gobreaker.StateClosed {
		t.Fatalf("expected Closed breaker and 1 quote, state=%s quotes=%+v", rc.State(), quotes)
	}
}

func TestResilientCarrierTimeout(t *testing.T) {
	stub := &stubCarrier{
		code: "slow",
		name: "Slow Express",
		feeFn: func(ctx context.Context, _ FeeRequest) ([]Quote, error) {
			select {
			case <-time.After(300 * time.Millisecond):
				return []Quote{{CarrierCode: "slow", FeeVND: 10000}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	rc := NewResilientCarrier(stub, ResilienceConfig{
		AttemptTimeout: 25 * time.Millisecond,
		MaxAttempts:    2,
		BaseBackoff:    5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		CBMaxFailures:  5,
		Logger:         discardLogger(),
	})

	_, err := rc.CalculateFee(context.Background(), FeeRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if stub.feeCalls.Load() != 2 {
		t.Fatalf("expected 2 timed-out attempts, got %d", stub.feeCalls.Load())
	}
}

func TestAggregatorParallelPartialAndFullFallback(t *testing.T) {
	req := FeeRequest{
		FromProvinceCode: "79",
		ToProvinceCode:   "01",
		WeightGrams:      1500,
		CODAmountVND:     200000,
	}

	t.Run("both healthy carriers return combined sorted quotes in parallel", func(t *testing.T) {
		ghn := NewResilientCarrier(NewGHNAdapter(GHNConfig{Mode: "mock"}), DefaultResilienceConfig(discardLogger()))
		mockExp := NewResilientCarrier(NewMockExpressAdapter(), DefaultResilienceConfig(discardLogger()))
		agg := NewAggregator(discardLogger(), ghn, mockExp)

		quotes, usedFallback, err := agg.CalculateFeeAll(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if usedFallback {
			t.Fatalf("expected live/mock carrier quotes, not fallback")
		}
		if len(quotes) != 4 {
			t.Fatalf("expected 4 quotes (2 GHN + 2 Mock Express), got %d", len(quotes))
		}
		for i := 1; i < len(quotes); i++ {
			if quotes[i].FeeVND < quotes[i-1].FeeVND {
				t.Fatalf("quotes not sorted ascending by FeeVND: %+v", quotes)
			}
		}
	})

	t.Run("one failing carrier is omitted while healthy carrier succeeds", func(t *testing.T) {
		failing := &stubCarrier{
			code: "ghn",
			name: "GHN",
			feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
				return nil, errors.New("ghn timeout")
			},
		}
		mockExp := NewMockExpressAdapter()
		agg := NewAggregator(discardLogger(), failing, mockExp)

		quotes, usedFallback, err := agg.CalculateFeeAll(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if usedFallback {
			t.Fatalf("expected healthy carrier quotes, not fallback")
		}
		if len(quotes) == 0 {
			t.Fatalf("expected non-empty quotes")
		}
		for _, q := range quotes {
			if q.CarrierCode != CodeMockExpress || q.IsFallback {
				t.Fatalf("unexpected quote when ghn failed: %+v", q)
			}
		}
	})

	t.Run("all carriers failing serves fallback fee table (UC-SHIP02)", func(t *testing.T) {
		fail1 := &stubCarrier{
			code: "ghn",
			name: "GHN",
			feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
				return nil, errors.New("ghn down")
			},
		}
		fail2 := &stubCarrier{
			code: "mock_express",
			name: "Mock Express",
			feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
				return nil, errors.New("mock down")
			},
		}
		agg := NewAggregator(discardLogger(), fail1, fail2)

		quotes, usedFallback, err := agg.CalculateFeeAll(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error when falling back on transient failures: %v", err)
		}
		if !usedFallback {
			t.Fatalf("expected fallback flag true when all carriers fail")
		}
		if len(quotes) != 1 || !quotes[0].IsFallback || quotes[0].FeeVND <= 0 {
			t.Fatalf("expected 1 valid fallback quote, got %+v", quotes)
		}
	})

	t.Run("all carriers rejecting route as non-retryable returns ErrRouteOutOfServiceArea", func(t *testing.T) {
		reject1 := &stubCarrier{
			code: "ghn",
			name: "GHN",
			feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
				return nil, &NonRetryableError{Err: errors.New("route outside GHN coverage")}
			},
		}
		reject2 := &stubCarrier{
			code: "mock_express",
			name: "Mock Express",
			feeFn: func(_ context.Context, _ FeeRequest) ([]Quote, error) {
				return nil, &NonRetryableError{Err: errors.New("route outside Mock Express coverage")}
			},
		}
		agg := NewAggregator(discardLogger(), reject1, reject2)

		quotes, usedFallback, err := agg.CalculateFeeAll(context.Background(), req)
		if !errors.Is(err, ErrRouteOutOfServiceArea) {
			t.Fatalf("expected ErrRouteOutOfServiceArea, got quotes=%+v fallback=%v err=%v", quotes, usedFallback, err)
		}
		if usedFallback || len(quotes) != 0 {
			t.Fatalf("must not serve fallback quotes when all carriers reject route, got quotes=%+v fallback=%v", quotes, usedFallback)
		}
	})
}

func TestGHNSandboxHTTPAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Token") != "test-token" || r.Header.Get("ShopId") != "12345" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/shipping-order/fee":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "Success",
				"data":    map[string]any{"total": 33000},
			})
		case "/v2/shipping-order/create":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "Success",
				"data":    map[string]any{"order_code": "GHN-SANDBOX-999", "total_fee": 33000},
			})
		case "/v2/switch-status/cancel":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "Success"})
		case "/v2/shipping-order/detail":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "Success",
				"data":    map[string]any{"order_code": "GHN-SANDBOX-999", "status": "delivered", "updated_date": "2026-11-20T09:00:00Z"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter := NewGHNAdapter(GHNConfig{
		Mode:         "sandbox",
		BaseURL:      srv.URL,
		Token:        "test-token",
		ShopID:       12345,
		LabelBaseURL: "https://shop.example.com/api/v1/shipping/labels",
		HTTPClient:   srv.Client(),
	})

	quotes, err := adapter.CalculateFee(context.Background(), FeeRequest{
		FromProvinceCode: "79",
		ToProvinceCode:   "01",
		FromDistrictID:   1442,
		ToDistrictID:     1489,
		ToGHNWardCode:    "1A0606",
		WeightGrams:      800,
	})
	if err != nil || len(quotes) != 1 || quotes[0].FeeVND != 33000 {
		t.Fatalf("unexpected sandbox fee result: quotes=%+v err=%v", quotes, err)
	}

	created, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:     "ord-123",
		WeightGrams: 800,
		ToAddress:   Address{Name: "Buyer", Phone: "0901234567", StreetAddress: "123 Le Loi", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
	})
	if err != nil || created.TrackingCode != "GHN-SANDBOX-999" || created.FeeVND != 33000 {
		t.Fatalf("unexpected sandbox create result: %+v err=%v", created, err)
	}
	if created.LabelURL != "https://shop.example.com/api/v1/shipping/labels/GHN-SANDBOX-999" {
		t.Fatalf("sandbox LabelURL = %q, want proxy labelBaseURL", created.LabelURL)
	}

	st, err := adapter.GetStatus(context.Background(), "GHN-SANDBOX-999")
	if err != nil || st.Status != StatusDelivered || !st.Changed {
		t.Fatalf("unexpected sandbox status: %+v err=%v", st, err)
	}

	if err := adapter.CancelShipment(context.Background(), "GHN-SANDBOX-999"); err != nil {
		t.Fatalf("unexpected sandbox cancel error: %v", err)
	}
}

func TestMapCarrierStatus_PickedIsDeliveringAndNotCancellable(t *testing.T) {
	got, ok := MapCarrierStatus("picked")
	if !ok || got != StatusDelivering {
		t.Fatalf("MapCarrierStatus(picked) = (%q, %v), want (%q, true)", got, ok, StatusDelivering)
	}
	if IsCancellableStatus(got) {
		t.Fatalf("picked -> %q must NOT be cancellable", got)
	}
	for _, raw := range []string{"ready_to_pick", "picking", "money_collect_picking"} {
		st, ok := MapCarrierStatus(raw)
		if !ok || st != StatusPicking || !IsCancellableStatus(st) {
			t.Fatalf("MapCarrierStatus(%q) = (%q, %v), want cancellable picking", raw, st, ok)
		}
	}
}

func TestMockLabelURLs_NoDotLocalDomain(t *testing.T) {
	ghn := NewGHNAdapter(GHNConfig{Mode: "mock"})
	res1, err := ghn.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:     "ord-lbl-1",
		WeightGrams: 500,
		FromAddress: Address{ProvinceCode: "79"},
		ToAddress:   Address{ProvinceCode: "01"},
	})
	if err != nil {
		t.Fatalf("ghn mock CreateShipment: %v", err)
	}
	if res1.LabelURL == "" || containsDotLocal(res1.LabelURL) {
		t.Fatalf("GHN mock LabelURL must not use .local domain, got %q", res1.LabelURL)
	}

	mockExp := NewMockExpressAdapter()
	res2, err := mockExp.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:     "ord-lbl-2",
		WeightGrams: 500,
		FromAddress: Address{ProvinceCode: "79"},
		ToAddress:   Address{ProvinceCode: "01"},
	})
	if err != nil {
		t.Fatalf("mock_express CreateShipment: %v", err)
	}
	if res2.LabelURL == "" || containsDotLocal(res2.LabelURL) {
		t.Fatalf("MockExpress LabelURL must not use .local domain, got %q", res2.LabelURL)
	}
}

func containsDotLocal(u string) bool {
	for i := 0; i+6 <= len(u); i++ {
		if u[i:i+6] == ".local" {
			return true
		}
	}
	return false
}

func TestStatusDescriptionVIAndResolve(t *testing.T) {
	if got := ResolveDescriptionVI(StatusDelivering, ""); got != "Đang giao hàng" {
		t.Fatalf("empty desc -> %q, want Đang giao hàng", got)
	}
	if got := ResolveDescriptionVI(StatusDelivering, "picked"); got != "Đang giao hàng" {
		t.Fatalf("raw English 'picked' -> %q, want Đang giao hàng", got)
	}
	if got := ResolveDescriptionVI(StatusFailed, "delivery_fail"); got != "Giao hàng thất bại" {
		t.Fatalf("raw English 'delivery_fail' -> %q, want Giao hàng thất bại", got)
	}
	if got := ResolveDescriptionVI(StatusDelivered, "Giao thành công cho lễ tân"); got != "Giao thành công cho lễ tân" {
		t.Fatalf("custom Vietnamese desc should be preserved, got %q", got)
	}
}

func TestFallbackQuotesWithRates_AndCarrierFiltering(t *testing.T) {
	customRates := []FallbackRate{
		{Region: RegionSameProvince, BaseFeeVND: 30000, BaseWeightGrams: 500, ExtraFeeVNDPer500g: 7000},
		{Region: RegionSameRegion, BaseFeeVND: 40000, BaseWeightGrams: 500, ExtraFeeVNDPer500g: 7000},
		{Region: RegionCrossRegion, BaseFeeVND: 55000, BaseWeightGrams: 500, ExtraFeeVNDPer500g: 7000},
	}
	req := FeeRequest{FromProvinceCode: "79", ToProvinceCode: "79", WeightGrams: 1000}
	quotes := FallbackQuotesWithRates(req, customRates)
	if len(quotes) != 1 || quotes[0].FeeVND != 37000 || !quotes[0].IsFallback {
		t.Fatalf("expected custom fallback fee 37000 (30000 + 7000), got %+v", quotes)
	}

	ghn := NewGHNAdapter(GHNConfig{Mode: "mock"})
	mockExp := NewMockExpressAdapter()
	agg := NewAggregator(discardLogger(), ghn, mockExp)

	// Filtering to only ghn omits mock_express.
	onlyGHN, _, err := agg.CalculateFeeForCarriers(context.Background(), req, []string{"ghn"}, nil)
	if err != nil || len(onlyGHN) == 0 {
		t.Fatalf("expected ghn quotes, got %+v err=%v", onlyGHN, err)
	}
	for _, q := range onlyGHN {
		if q.CarrierCode != CodeGHN {
			t.Fatalf("unexpected carrier %q when filtered to ghn", q.CarrierCode)
		}
	}

	// Explicitly empty allowed list returns ErrNoEnabledCarriers.
	if _, _, err := agg.CalculateFeeForCarriers(context.Background(), req, []string{}, nil); !errors.Is(err, ErrNoEnabledCarriers) {
		t.Fatalf("expected ErrNoEnabledCarriers for empty allowed list, got %v", err)
	}
}

func TestGHNSandboxTestConnectionAndRedelivery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Token") != "valid-sandbox-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401,"message":"Unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/master-data/province", "/v2/switch-status/storing":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "Success"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter := NewGHNAdapter(GHNConfig{
		Mode:       "sandbox",
		BaseURL:    srv.URL,
		Token:      "valid-sandbox-token",
		HTTPClient: srv.Client(),
	})

	if err := adapter.TestConnection(context.Background(), "bad-tok"); !errors.Is(err, ErrInvalidCarrierToken) {
		t.Fatalf("expected ErrInvalidCarrierToken on 401, got %v", err)
	}
	if err := adapter.TestConnection(context.Background(), "valid-sandbox-token"); err != nil {
		t.Fatalf("expected nil on valid sandbox token, got %v", err)
	}
	if err := adapter.RequestRedelivery(context.Background(), "GHN-123", "2026-11-25", "call first"); err != nil {
		t.Fatalf("expected nil on sandbox redelivery, got %v", err)
	}
}

func TestReview_GHNSandboxCancelAlreadyCancelledIsIdempotent(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"http 400 English already cancelled", http.StatusBadRequest, `{"code":400,"message":"Order is already cancelled"}`},
		{"http 200 code 400 Vietnamese da huy", http.StatusOK, `{"code":400,"message":"Đơn hàng đã huỷ trước đó"}`},
		{"http 200 code_message ORDER_IS_CANCELLED", http.StatusOK, `{"code":400,"code_message":"ORDER_IS_CANCELLED","message":"Cannot update"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			adapter := NewGHNAdapter(GHNConfig{
				Mode:       "sandbox",
				BaseURL:    srv.URL,
				Token:      "valid-sandbox-token",
				HTTPClient: srv.Client(),
			})
			if err := adapter.CancelShipment(context.Background(), "GHN-ALREADY-CANCELLED"); err != nil {
				t.Fatalf("expected nil when GHN reports already cancelled, got %v", err)
			}
		})
	}

	// Mock adapters must also treat repeated CancelShipment calls as idempotent success.
	ghnMock := NewGHNAdapter(GHNConfig{Mode: "mock"})
	mockExp := NewMockExpressAdapter()
	for i := 0; i < 2; i++ {
		if err := ghnMock.CancelShipment(context.Background(), "GHN-MOCK-DUP"); err != nil {
			t.Fatalf("ghn mock cancel #%d failed: %v", i+1, err)
		}
		if err := mockExp.CancelShipment(context.Background(), "MOCK-DUP"); err != nil {
			t.Fatalf("mock_express cancel #%d failed: %v", i+1, err)
		}
	}
}

func TestReview_GHNSandboxCreateClientOrderCodeAndBody400NonRetryable(t *testing.T) {
	var seenClientCodes []string
	var rejectNext bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/shipping-order/create":
			var body ghnCreatePayload
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			seenClientCodes = append(seenClientCodes, body.ClientOrderCode)
			if rejectNext {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    400,
					"message": "Invalid to_ward_code",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "Success",
				"data":    map[string]any{"order_code": "GHN-SB-" + body.ClientOrderCode, "total_fee": 28000},
			})
		case "/v2/shipping-order/fee":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    400,
				"message": "Route not supported",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter := NewGHNAdapter(GHNConfig{
		Mode:       "sandbox",
		BaseURL:    srv.URL,
		Token:      "valid-sandbox-token",
		HTTPClient: srv.Client(),
	})

	// Explicit ClientOrderCode must be sent as client_order_code in GHN create JSON.
	res, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-100",
		ClientOrderCode: "ord-100-R1",
		WeightGrams:     500,
	})
	if err != nil || res.TrackingCode != "GHN-SB-ord-100-R1" {
		t.Fatalf("expected GHN-SB-ord-100-R1, got res=%+v err=%v", res, err)
	}
	if len(seenClientCodes) != 1 || seenClientCodes[0] != "ord-100-R1" {
		t.Fatalf("expected client_order_code=ord-100-R1, got %v", seenClientCodes)
	}

	// HTTP 200 with JSON code 400 must be wrapped as NonRetryableError.
	rejectNext = true
	_, err = adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-100",
		ClientOrderCode: "RET-rr-1",
		WeightGrams:     500,
	})
	if err == nil || !IsNonRetryable(err) {
		t.Fatalf("expected NonRetryableError for HTTP 200 code=400 create, got %v", err)
	}
	if _, err := adapter.CalculateFee(context.Background(), FeeRequest{WeightGrams: 500}); err == nil || !IsNonRetryable(err) {
		t.Fatalf("expected NonRetryableError for HTTP 200 code=400 fee, got %v", err)
	}

	// Mock adapters must generate distinct tracking codes across repeated creations for the same order.
	ghnMock := NewGHNAdapter(GHNConfig{Mode: "mock"})
	mockExp := NewMockExpressAdapter()
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		r1, err := ghnMock.CreateShipment(context.Background(), ShipmentRequest{OrderID: "same-ord", WeightGrams: 500})
		if err != nil {
			t.Fatalf("ghn mock create: %v", err)
		}
		if seen[r1.TrackingCode] {
			t.Fatalf("duplicate GHN mock tracking code: %s", r1.TrackingCode)
		}
		seen[r1.TrackingCode] = true

		r2, err := mockExp.CreateShipment(context.Background(), ShipmentRequest{OrderID: "same-ord", WeightGrams: 500})
		if err != nil {
			t.Fatalf("mock_express create: %v", err)
		}
		if seen[r2.TrackingCode] {
			t.Fatalf("duplicate MockExpress tracking code: %s", r2.TrackingCode)
		}
		seen[r2.TrackingCode] = true
	}
}

func TestReview2_GHNStatusCodeClassification(t *testing.T) {
	t.Run("HTTP 408 and 429 are transient and retried by ResilientCarrier", func(t *testing.T) {
		for _, statusCode := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) == 1 {
					w.WriteHeader(statusCode)
					_, _ = w.Write([]byte(`{"code":408,"message":"try again"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 200, "message": "Success",
					"data": map[string]any{"order_code": "GHN-RETRY-OK", "total_fee": 25000},
				})
			}))
			adapter := NewGHNAdapter(GHNConfig{
				Mode: "sandbox", BaseURL: srv.URL, Token: "valid-tok", HTTPClient: srv.Client(),
			})
			rc := NewResilientCarrier(adapter, ResilienceConfig{
				AttemptTimeout: 500 * time.Millisecond,
				MaxAttempts:    3,
				BaseBackoff:    5 * time.Millisecond,
				MaxBackoff:     10 * time.Millisecond,
				CBMaxFailures:  5,
				Logger:         discardLogger(),
			})
			res, err := rc.CreateShipment(context.Background(), ShipmentRequest{OrderID: "ord-retry", WeightGrams: 500})
			srv.Close()
			if err != nil || res.TrackingCode != "GHN-RETRY-OK" || attempts.Load() != 2 {
				t.Fatalf("HTTP %d must retry transiently and succeed on attempt 2, got res=%+v err=%v attempts=%d", statusCode, res, err, attempts.Load())
			}
		}
	})

	t.Run("HTTP 200 body code 408 and 429 are transient (not NonRetryable)", func(t *testing.T) {
		for _, bodyCode := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": bodyCode, "message": "transient"})
			}))
			adapter := NewGHNAdapter(GHNConfig{
				Mode: "sandbox", BaseURL: srv.URL, Token: "valid-tok", HTTPClient: srv.Client(),
			})
			_, err := adapter.CreateShipment(context.Background(), ShipmentRequest{OrderID: "ord-body-retry", WeightGrams: 500})
			srv.Close()
			if err == nil || IsNonRetryable(err) {
				t.Fatalf("body code %d must be retryable (not NonRetryable), got err=%v", bodyCode, err)
			}
		}
	})

	t.Run("HTTP 401 and 403 return ErrInvalidCarrierToken and Aggregator serves fallback instead of ErrRouteOutOfServiceArea", func(t *testing.T) {
		for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(statusCode)
				_, _ = w.Write([]byte(`{"code":401,"message":"Invalid token"}`))
			}))
			adapter := NewGHNAdapter(GHNConfig{
				Mode: "sandbox", BaseURL: srv.URL, Token: "expired-tok", HTTPClient: srv.Client(),
			})
			_, createErr := adapter.CreateShipment(context.Background(), ShipmentRequest{OrderID: "ord-auth", WeightGrams: 500})
			if !errors.Is(createErr, ErrInvalidCarrierToken) {
				srv.Close()
				t.Fatalf("HTTP %d CreateShipment must wrap ErrInvalidCarrierToken, got %v", statusCode, createErr)
			}

			agg := NewAggregator(discardLogger(), adapter)
			quotes, isFallback, feeErr := agg.CalculateFeeForCarriers(context.Background(), FeeRequest{
				FromProvinceCode: "79", ToProvinceCode: "01", WeightGrams: 500,
			}, []string{CodeGHN}, nil)
			srv.Close()
			if feeErr != nil || !isFallback || len(quotes) == 0 {
				t.Fatalf("HTTP %d fee failure must fall back to fallback table (not ErrRouteOutOfServiceArea), got quotes=%+v fallback=%v err=%v", statusCode, quotes, isFallback, feeErr)
			}
		}
	})
}

func TestReview4_MockDeterministicTrackingAndGHNDuplicateClientOrderCode(t *testing.T) {
	t.Run("mock_express and ghn mock produce deterministic tracking per ClientOrderCode", func(t *testing.T) {
		mockExp := NewMockExpressAdapter()
		m1, err := mockExp.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-det", ClientOrderCode: "RET-rr-1", WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("mock_express #1: %v", err)
		}
		m2, err := mockExp.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-det", ClientOrderCode: "RET-rr-1", WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("mock_express #2: %v", err)
		}
		if m1.TrackingCode != m2.TrackingCode {
			t.Fatalf("same ClientOrderCode must produce identical mock_express tracking: %s vs %s", m1.TrackingCode, m2.TrackingCode)
		}
		m3, err := mockExp.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-det", ClientOrderCode: "RET-rr-1-R1", WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("mock_express #3: %v", err)
		}
		if m3.TrackingCode == m1.TrackingCode {
			t.Fatalf("different ClientOrderCode must produce distinct mock_express tracking: %s", m3.TrackingCode)
		}

		ghnMock := NewGHNAdapter(GHNConfig{Mode: "mock"})
		g1, _ := ghnMock.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-det", ClientOrderCode: "ord-det", WeightGrams: 500,
		})
		g2, _ := ghnMock.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-det", ClientOrderCode: "ord-det", WeightGrams: 500,
		})
		g3, _ := ghnMock.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-det", ClientOrderCode: "ord-det-R1", WeightGrams: 500,
		})
		if g1.TrackingCode != g2.TrackingCode || g3.TrackingCode == g1.TrackingCode {
			t.Fatalf("ghn mock tracking must be deterministic per ClientOrderCode: g1=%s g2=%s g3=%s", g1.TrackingCode, g2.TrackingCode, g3.TrackingCode)
		}
	})

	t.Run("GHN sandbox duplicate client_order_code looks up existing order instead of failing NonRetryable", func(t *testing.T) {
		cases := []struct {
			name       string
			httpStatus int
			createBody string
		}{
			{"http 400 English already exists", http.StatusBadRequest, `{"code":400,"message":"client_order_code already exists"}`},
			{"http 200 code 400 code_message existed", http.StatusOK, `{"code":400,"code_message":"CLIENT_ORDER_CODE_EXISTED","message":"mã đơn khách hàng đã tồn tại"}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var lookedUpClientCode string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v2/shipping-order/create":
						w.WriteHeader(tc.httpStatus)
						_, _ = w.Write([]byte(tc.createBody))
					case "/v2/shipping-order/detail-by-client-code":
						var reqBody struct {
							ClientOrderCode string `json:"client_order_code"`
						}
						_ = json.NewDecoder(r.Body).Decode(&reqBody)
						lookedUpClientCode = reqBody.ClientOrderCode
						_ = json.NewEncoder(w).Encode(map[string]any{
							"code":    200,
							"message": "Success",
							"data": map[string]any{
								"order_code":        "GHN-EXISTING-777",
								"client_order_code": reqBody.ClientOrderCode,
								"status":            "ready_to_pick",
								"total_fee":         31000,
							},
						})
					default:
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer srv.Close()

				adapter := NewGHNAdapter(GHNConfig{
					Mode:         "sandbox",
					BaseURL:      srv.URL,
					Token:        "valid-tok",
					LabelBaseURL: "https://shop.example.com/api/v1/shipping/labels",
					HTTPClient:   srv.Client(),
				})
				res, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
					OrderID: "ord-dup", ClientOrderCode: "RET-rr-dup", WeightGrams: 500,
				})
				if err != nil {
					t.Fatalf("duplicate client_order_code must recover via detail-by-client-code, got err=%v", err)
				}
				if lookedUpClientCode != "RET-rr-dup" {
					t.Fatalf("looked up client_order_code = %q, want RET-rr-dup", lookedUpClientCode)
				}
				if res.TrackingCode != "GHN-EXISTING-777" || res.FeeVND != 31000 || res.LabelURL != "https://shop.example.com/api/v1/shipping/labels/GHN-EXISTING-777" {
					t.Fatalf("unexpected recovered shipment result: %+v", res)
				}
			})
		}
	})

	t.Run("GHN duplicate client_order_code lookup failure is retryable (NOT NonRetryable)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v2/shipping-order/create":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":400,"message":"client_order_code already exists"}`))
			case "/v2/shipping-order/detail-by-client-code":
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":404,"message":"not visible yet"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		adapter := NewGHNAdapter(GHNConfig{
			Mode: "sandbox", BaseURL: srv.URL, Token: "valid-tok", HTTPClient: srv.Client(),
		})
		_, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-dup-fail", ClientOrderCode: "RET-rr-dup-fail", WeightGrams: 500,
		})
		if err == nil {
			t.Fatalf("expected error when lookup fails")
		}
		if IsNonRetryable(err) {
			t.Fatalf("duplicate client_order_code error must NOT be NonRetryable (would trigger fallback to another carrier), got %v", err)
		}
	})
}
