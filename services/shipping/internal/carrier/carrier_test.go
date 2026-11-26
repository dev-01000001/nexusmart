package carrier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	for _, raw := range []string{"picked", "3", "da_lay_hang"} {
		got, ok := MapCarrierStatus(raw)
		if !ok || got != StatusDelivering {
			t.Fatalf("MapCarrierStatus(%q) = (%q, %v), want (%q, true)", raw, got, ok, StatusDelivering)
		}
		if IsCancellableStatus(got) {
			t.Fatalf("%q -> %q must NOT be cancellable", raw, got)
		}
	}
	for _, raw := range []string{"ready_to_pick", "picking", "money_collect_picking", "12", "127", "128", "da_dieu_phoi_lay_hang"} {
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

	ghtk := NewGHTKAdapter(GHTKConfig{Mode: "mock"})
	res3, err := ghtk.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:     "ord-lbl-3",
		WeightGrams: 500,
		FromAddress: Address{ProvinceCode: "79"},
		ToAddress:   Address{ProvinceCode: "01"},
	})
	if err != nil {
		t.Fatalf("ghtk mock CreateShipment: %v", err)
	}
	if res3.LabelURL == "" || containsDotLocal(res3.LabelURL) {
		t.Fatalf("GHTK mock LabelURL must not use .local domain, got %q", res3.LabelURL)
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
	mappedTo := Address{ProvinceCode: "01", WardCode: "00070", GHNDistrictID: 1489, GHNWardCode: "1A0606"}
	res, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-100",
		ClientOrderCode: "ord-100-R1",
		WeightGrams:     500,
		ToAddress:       mappedTo,
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
		ToAddress:       mappedTo,
	})
	if err == nil || !IsNonRetryable(err) {
		t.Fatalf("expected NonRetryableError for HTTP 200 code=400 create, got %v", err)
	}
	if _, err := adapter.CalculateFee(context.Background(), FeeRequest{
		FromDistrictID: 1442, ToDistrictID: 1489, ToGHNWardCode: "1A0606", WeightGrams: 500,
	}); err == nil || !IsNonRetryable(err) {
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
			res, err := rc.CreateShipment(context.Background(), ShipmentRequest{
				OrderID: "ord-retry", WeightGrams: 500,
				ToAddress: Address{ProvinceCode: "01", WardCode: "00070", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
			})
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
			_, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
				OrderID: "ord-body-retry", WeightGrams: 500,
				ToAddress: Address{ProvinceCode: "01", WardCode: "00070", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
			})
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
			_, createErr := adapter.CreateShipment(context.Background(), ShipmentRequest{
				OrderID: "ord-auth", WeightGrams: 500,
				ToAddress: Address{ProvinceCode: "01", WardCode: "00070", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
			})
			if !errors.Is(createErr, ErrInvalidCarrierToken) {
				srv.Close()
				t.Fatalf("HTTP %d CreateShipment must wrap ErrInvalidCarrierToken, got %v", statusCode, createErr)
			}

			agg := NewAggregator(discardLogger(), adapter)
			quotes, isFallback, feeErr := agg.CalculateFeeForCarriers(context.Background(), FeeRequest{
				FromProvinceCode: "79", FromDistrictID: 1442,
				ToProvinceCode: "01", ToDistrictID: 1489, ToGHNWardCode: "1A0606",
				WeightGrams: 500,
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
					ToAddress: Address{ProvinceCode: "01", WardCode: "00070", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
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
			ToAddress: Address{ProvinceCode: "01", WardCode: "00070", GHNDistrictID: 1489, GHNWardCode: "1A0606"},
		})
		if err == nil {
			t.Fatalf("expected error when lookup fails")
		}
		if IsNonRetryable(err) {
			t.Fatalf("duplicate client_order_code error must NOT be NonRetryable (would trigger fallback to another carrier), got %v", err)
		}
	})
}

func TestMockCarrier_E18_TestEnvGatedFaultInjection(t *testing.T) {
	mock := NewMockExpressAdapter()
	// Disabled by default: injecting fault must fail and CreateShipment must succeed.
	if err := mock.InjectCreateFault("ord-fault-1", http.StatusServiceUnavailable, 3); err == nil {
		t.Fatalf("expected error when fault injection is disabled")
	}
	if res, err := mock.CreateShipment(context.Background(), ShipmentRequest{
		OrderID: "ord-fault-1", ClientOrderCode: "ord-fault-1", WeightGrams: 500,
	}); err != nil || res == nil {
		t.Fatalf("disabled fault injector must not fail CreateShipment: res=%+v err=%v", res, err)
	}

	// Enabled via test env flag: fails 503 exactly 3 times (retryable), then succeeds on attempt 4.
	mock.SetTestFaultInjectionEnabled(true)
	if err := mock.InjectCreateFault("ord-fault-2", http.StatusServiceUnavailable, 3); err != nil {
		t.Fatalf("InjectCreateFault(503): %v", err)
	}
	for i := 1; i <= 3; i++ {
		_, err := mock.CreateShipment(context.Background(), ShipmentRequest{
			OrderID: "ord-fault-2", ClientOrderCode: "ord-fault-2", WeightGrams: 500,
		})
		if err == nil || IsNonRetryable(err) {
			t.Fatalf("attempt %d: expected retryable 503 error, got %v", i, err)
		}
	}
	res, err := mock.CreateShipment(context.Background(), ShipmentRequest{
		OrderID: "ord-fault-2", ClientOrderCode: "ord-fault-2", WeightGrams: 500,
	})
	if err != nil || res == nil || res.TrackingCode == "" {
		t.Fatalf("attempt 4 after 3 injected 503s must succeed, got res=%+v err=%v", res, err)
	}

	// 400 injection must return NonRetryableError.
	if err := mock.InjectCreateFault("ord-fault-400", http.StatusBadRequest, 1); err != nil {
		t.Fatalf("InjectCreateFault(400): %v", err)
	}
	_, err = mock.CreateShipment(context.Background(), ShipmentRequest{
		OrderID: "ord-fault-400", ClientOrderCode: "ord-fault-400", WeightGrams: 500,
	})
	if err == nil || !IsNonRetryable(err) {
		t.Fatalf("injected 400 must be NonRetryableError, got %v", err)
	}
}

func TestGHNSandbox_MissingWardMapping_ZeroHTTPRequests(t *testing.T) {
	var httpCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	adapter := NewGHNAdapter(GHNConfig{
		Mode:       "sandbox",
		BaseURL:    srv.URL,
		Token:      "valid-sandbox-token",
		ShopID:     12345,
		HTTPClient: srv.Client(),
	})
	rc := NewResilientCarrier(adapter, ResilienceConfig{
		AttemptTimeout: 500 * time.Millisecond,
		MaxAttempts:    3,
		BaseBackoff:    5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		CBMaxFailures:  1,
		CBOpenTimeout:  30 * time.Second,
		Logger:         discardLogger(),
	})
	agg := NewAggregator(discardLogger(), rc)

	// 1. CalculateFee with missing destination GHN mapping: 0 HTTP calls, fallback quotes (NOT ErrRouteOutOfServiceArea), CB stays Closed.
	quotes, isFallback, err := agg.CalculateFeeForCarriers(context.Background(), FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "26740",
		FromDistrictID:   1442,
		FromGHNWardCode:  "20101",
		ToProvinceCode:   "96",
		ToWardCode:       "31825",
		ToDistrictID:     0,
		ToGHNWardCode:    "",
		WeightGrams:      500,
	}, []string{CodeGHN}, nil)
	if err != nil {
		t.Fatalf("GHN sandbox CalculateFee on unmapped ward must fall back (not error): got err=%v", err)
	}
	if !isFallback || len(quotes) == 0 || quotes[0].CarrierCode != CodeFallback {
		t.Fatalf("expected fallback quotes on unmapped GHN ward, got isFallback=%v quotes=%+v", isFallback, quotes)
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("GHN sandbox must send 0 HTTP requests for unmapped destination ward, got %d", httpCalls.Load())
	}
	if rc.State() != gobreaker.StateClosed {
		t.Fatalf("missing GHN mapping must NOT trip circuit breaker, got state=%s", rc.State())
	}

	// 2. CalculateFee with missing origin GHN district: 0 HTTP calls, fallback quotes.
	quotesOrig, isFallbackOrig, err := agg.CalculateFeeForCarriers(context.Background(), FeeRequest{
		FromProvinceCode: "96",
		FromWardCode:     "31825",
		FromDistrictID:   0,
		FromGHNWardCode:  "",
		ToProvinceCode:   "79",
		ToWardCode:       "26740",
		ToDistrictID:     1442,
		ToGHNWardCode:    "20101",
		WeightGrams:      500,
	}, []string{CodeGHN}, nil)
	if err != nil || !isFallbackOrig || len(quotesOrig) == 0 {
		t.Fatalf("GHN sandbox CalculateFee on unmapped origin must fall back, got quotes=%+v fallback=%v err=%v", quotesOrig, isFallbackOrig, err)
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("GHN sandbox must send 0 HTTP requests for unmapped origin ward, got %d", httpCalls.Load())
	}

	// 3. CreateShipment with missing destination GHN mapping: 0 HTTP calls, NonRetryableError with clear Vietnamese message.
	_, createErr := rc.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-ghn-unmapped",
		ClientOrderCode: "ord-ghn-unmapped",
		WeightGrams:     500,
		FromAddress:     Address{ProvinceCode: "79", WardCode: "26740", GHNDistrictID: 1442, GHNWardCode: "20101"},
		ToAddress:       Address{ProvinceCode: "96", WardCode: "31825", GHNDistrictID: 0, GHNWardCode: ""},
	})
	if createErr == nil || !IsNonRetryable(createErr) {
		t.Fatalf("GHN sandbox CreateShipment on unmapped ward must return NonRetryableError, got %v", createErr)
	}
	if !strings.Contains(createErr.Error(), "phường chưa có mã GHN, chọn hãng khác") {
		t.Fatalf("unexpected missing GHN mapping error message: %q", createErr.Error())
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("GHN sandbox CreateShipment must send 0 HTTP requests for unmapped ward, got %d", httpCalls.Load())
	}

	// 4. Mock mode remains unaffected even with GHNDistrictID=0.
	mockGHN := NewGHNAdapter(GHNConfig{Mode: "mock"})
	if mq, mErr := mockGHN.CalculateFee(context.Background(), FeeRequest{
		FromProvinceCode: "79", ToProvinceCode: "96", WeightGrams: 500,
	}); mErr != nil || len(mq) == 0 {
		t.Fatalf("GHN mock CalculateFee must still work with GHNDistrictID=0, got mq=%+v err=%v", mq, mErr)
	}
	if ms, mErr := mockGHN.CreateShipment(context.Background(), ShipmentRequest{
		OrderID: "ord-ghn-mock-ok", ClientOrderCode: "ord-ghn-mock-ok", WeightGrams: 500,
	}); mErr != nil || ms == nil {
		t.Fatalf("GHN mock CreateShipment must still work with GHNDistrictID=0, got ms=%+v err=%v", ms, mErr)
	}
}

func TestGHTKAdapterMockMode(t *testing.T) {
	// Default / placeholder token forces mock mode even when Mode == "sandbox".
	for _, cfg := range []GHTKConfig{
		{Mode: "mock", Token: "real-token"},
		{Mode: "sandbox", Token: ""},
		{Mode: "sandbox", Token: "sandbox-ghtk-token"},
	} {
		adapter := NewGHTKAdapter(cfg)
		if !adapter.IsMock() {
			t.Fatalf("expected IsMock=true for cfg=%+v", cfg)
		}
		if adapter.Code() != CodeGHTK || adapter.Name() != NameGHTK {
			t.Fatalf("unexpected identity code=%q name=%q", adapter.Code(), adapter.Name())
		}
		if adapter.CanPoll() {
			t.Fatalf("expected CanPoll=false before SetMockStatus")
		}
	}

	adapter := NewGHTKAdapter(GHTKConfig{Mode: "mock", Token: "valid-mock-token"})

	// TestConnection & SetToken in mock mode.
	if err := adapter.TestConnection(context.Background(), "bad-tok"); !errors.Is(err, ErrInvalidCarrierToken) {
		t.Fatalf("expected ErrInvalidCarrierToken for rejected mock token, got %v", err)
	}
	if err := adapter.TestConnection(context.Background(), "valid-mock-token"); err != nil {
		t.Fatalf("expected valid mock token to pass TestConnection, got %v", err)
	}
	adapter.SetToken("rotated-mock-token")
	if err := adapter.TestConnection(context.Background(), ""); err != nil {
		t.Fatalf("expected rotated mock token to pass TestConnection, got %v", err)
	}

	// Intra-province vs inter-region + weight + COD deterministic integer VND quotes.
	intraQuotes, err := adapter.CalculateFee(context.Background(), FeeRequest{
		FromProvinceCode: "79",
		ToProvinceCode:   "79",
		WeightGrams:      800,
		CODAmountVND:     0,
	})
	if err != nil || len(intraQuotes) != 2 {
		t.Fatalf("expected 2 GHTK mock quotes, got %+v err=%v", intraQuotes, err)
	}
	if intraQuotes[0].ServiceCode != "ROAD" || intraQuotes[0].ServiceName != "GHTK Đường Bộ" || intraQuotes[0].FeeVND != 20000 {
		t.Fatalf("unexpected ROAD intra quote: %+v", intraQuotes[0])
	}
	if intraQuotes[1].ServiceCode != "XFAST" || intraQuotes[1].ServiceName != "GHTK Nhanh" || intraQuotes[1].FeeVND != 31000 {
		t.Fatalf("unexpected XFAST intra quote: %+v", intraQuotes[1])
	}

	interQuotes, err := adapter.CalculateFee(context.Background(), FeeRequest{
		FromProvinceCode: "79",
		ToProvinceCode:   "01",
		WeightGrams:      1800, // 800g over 1000g -> 2 steps * 4500 = 9000
		CODAmountVND:     350000,
	})
	if err != nil || len(interQuotes) != 2 {
		t.Fatalf("unexpected inter quotes: %+v err=%v", interQuotes, err)
	}
	wantInterRoad := int64(35000 + 9000 + 1500)
	if interQuotes[0].FeeVND != wantInterRoad {
		t.Fatalf("expected inter ROAD fee %d, got %d", wantInterRoad, interQuotes[0].FeeVND)
	}

	// Deterministic tracking code & distinct forward vs return tracking codes on same order.
	fwd1, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:      "ord-ghtk-1",
		Direction:    "FORWARD",
		ServiceCode:  "ROAD",
		WeightGrams:  1000,
		CODAmountVND: 100000,
		FromAddress:  Address{ProvinceCode: "79", WardCode: "26740"},
		ToAddress:    Address{ProvinceCode: "01", WardCode: "00070"},
	})
	if err != nil {
		t.Fatalf("mock CreateShipment failed: %v", err)
	}
	fwd2, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:      "ord-ghtk-1",
		Direction:    "FORWARD",
		ServiceCode:  "ROAD",
		WeightGrams:  1000,
		CODAmountVND: 100000,
		FromAddress:  Address{ProvinceCode: "79", WardCode: "26740"},
		ToAddress:    Address{ProvinceCode: "01", WardCode: "00070"},
	})
	if err != nil {
		t.Fatalf("second mock CreateShipment failed: %v", err)
	}
	if fwd1.TrackingCode != fwd2.TrackingCode {
		t.Fatalf("expected deterministic GHTK tracking code, got %s vs %s", fwd1.TrackingCode, fwd2.TrackingCode)
	}
	if !strings.HasPrefix(fwd1.TrackingCode, "GHTK-") {
		t.Fatalf("expected GHTK-* tracking code, got %q", fwd1.TrackingCode)
	}
	wantLabel := DefaultLabelBaseURL + "/" + fwd1.TrackingCode
	if fwd1.LabelURL != wantLabel {
		t.Fatalf("expected label %q, got %q", wantLabel, fwd1.LabelURL)
	}

	// Distinct ClientOrderCode produces distinct tracking code (recreate after cancel).
	recreated, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-ghtk-1",
		ClientOrderCode: "ord-ghtk-1-R1",
		Direction:       "FORWARD",
		ServiceCode:     "ROAD",
		WeightGrams:     1000,
	})
	if err != nil || recreated.TrackingCode == fwd1.TrackingCode {
		t.Fatalf("recreated shipment with new ClientOrderCode must get distinct tracking code: %+v err=%v", recreated, err)
	}

	ret, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-ghtk-1",
		Direction:       "RETURN",
		ReturnRequestID: "ret-1",
		ServiceCode:     "XFAST",
		WeightGrams:     1000,
		FromAddress:     Address{ProvinceCode: "01", WardCode: "00070"},
		ToAddress:       Address{ProvinceCode: "79", WardCode: "26740"},
	})
	if err != nil {
		t.Fatalf("mock return CreateShipment failed: %v", err)
	}
	if ret.TrackingCode == fwd1.TrackingCode {
		t.Fatalf("return shipment must get distinct tracking code from forward shipment")
	}

	// Mock status polling map.
	stUnset, err := adapter.GetStatus(context.Background(), fwd1.TrackingCode)
	if err != nil || stUnset.Changed {
		t.Fatalf("expected Changed=false before SetMockStatus, got %+v err=%v", stUnset, err)
	}
	adapter.SetMockStatus(fwd1.TrackingCode, "6")
	if !adapter.CanPoll() {
		t.Fatalf("expected CanPoll=true after SetMockStatus")
	}
	stSet, err := adapter.GetStatus(context.Background(), fwd1.TrackingCode)
	if err != nil || !stSet.Changed || stSet.Status != StatusDelivered || stSet.RawStatus != "6" {
		t.Fatalf("expected delivered from GHTK mock status '6', got %+v err=%v", stSet, err)
	}
	adapter.SetMockStatus(fwd1.TrackingCode, "")
	if adapter.CanPoll() {
		t.Fatalf("expected CanPoll=false after clearing mock status")
	}

	if err := adapter.RequestRedelivery(context.Background(), fwd1.TrackingCode, "2026-11-28", "call before"); err != nil {
		t.Fatalf("mock RequestRedelivery failed: %v", err)
	}
	if err := adapter.CancelShipment(context.Background(), fwd1.TrackingCode); err != nil {
		t.Fatalf("mock CancelShipment failed: %v", err)
	}
}

func TestGHTKSandboxHTTPAdapter(t *testing.T) {
	var capturedFeeQuery string
	var capturedCreateOrder map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Token") != "live-ghtk-sandbox-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/services/shipment/fee":
			capturedFeeQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "ok",
				"fee": map[string]any{
					"name":          "area1",
					"fee":           27000,
					"insurance_fee": 1500,
					"delivery":      true,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/services/shipment/order":
			_ = json.NewDecoder(r.Body).Decode(&capturedCreateOrder)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "created",
				"order": map[string]any{
					"partner_id": "ord-sb-1",
					"label":      "S123456.BO.999888",
					"fee":        28500,
					"status_id":  2,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/services/shipment/cancel/S123456.BO.999888":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "cancelled"})
		case r.Method == http.MethodGet && r.URL.Path == "/services/shipment/v2/S123456.BO.999888":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "ok",
				"order": map[string]any{
					"label_id":    "S123456.BO.999888",
					"partner_id":  "ord-sb-1",
					"status":      6,
					"status_text": "Đã đối soát",
					"modified":    "2026-11-20 12:30:00",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/services/shipment/v2/GHTK-RETURNED":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"order": map[string]any{
					"label_id": "GHTK-RETURNED",
					"status":   "21",
					"modified": "2026-11-20T14:00:00Z",
				},
			})
		case r.URL.Path == "/services/shipment/v2/ERR-400":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"message":"invalid label"}`))
		case r.URL.Path == "/services/shipment/v2/ERR-503":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`service unavailable`))
		case r.URL.Path == "/services/shipment/v2/ERR-429":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`rate limited`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter := NewGHTKAdapter(GHTKConfig{
		Mode:         "sandbox",
		BaseURL:      srv.URL,
		Token:        "live-ghtk-sandbox-token",
		LabelBaseURL: "https://shop.example.com/api/v1/shipping/labels",
		HTTPClient:   srv.Client(),
	})
	if adapter.IsMock() {
		t.Fatalf("expected sandbox adapter IsMock=false")
	}
	if !adapter.CanPoll() {
		t.Fatalf("expected sandbox adapter CanPoll=true")
	}

	// TestConnection validates 401 -> ErrInvalidCarrierToken and valid token -> nil.
	if err := adapter.TestConnection(context.Background(), "wrong-token"); !errors.Is(err, ErrInvalidCarrierToken) {
		t.Fatalf("expected ErrInvalidCarrierToken on 401 TestConnection, got %v", err)
	}
	if err := adapter.TestConnection(context.Background(), "live-ghtk-sandbox-token"); err != nil {
		t.Fatalf("expected nil on valid sandbox GHTK TestConnection, got %v", err)
	}

	// CalculateFee resolves legacy 3-level names for merged province Binh Duong (25747) -> Hanoi (00070).
	quotes, err := adapter.CalculateFee(context.Background(), FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "25747",
		ToProvinceCode:   "01",
		ToWardCode:       "00070",
		WeightGrams:      900,
		CODAmountVND:     200000,
	})
	if err != nil || len(quotes) != 1 || quotes[0].FeeVND != 28500 || quotes[0].ServiceCode != "ROAD" {
		t.Fatalf("unexpected sandbox GHTK fee result: quotes=%+v err=%v", quotes, err)
	}
	if capturedFeeQuery == "" {
		t.Fatalf("expected fee query params to be sent")
	}

	created, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:      "ord-sb-1",
		ServiceCode:  "XFAST",
		WeightGrams:  1200,
		CODAmountVND: 250000,
		FromAddress: Address{
			Name:          "Shop Binh Duong",
			Phone:         "0901112222",
			ProvinceCode:  "79",
			WardCode:      "25747",
			StreetAddress: "12 Dai Lo Binh Duong",
		},
		ToAddress: Address{
			Name:          "Buyer Hanoi",
			Phone:         "0903334444",
			ProvinceCode:  "01",
			WardCode:      "00070",
			StreetAddress: "99 Hang Trong",
		},
		ItemsSummary: "Ao thun x2",
	})
	if err != nil || created.TrackingCode != "S123456.BO.999888" || created.FeeVND != 28500 {
		t.Fatalf("unexpected sandbox GHTK create result: %+v err=%v", created, err)
	}
	if created.LabelURL != "https://shop.example.com/api/v1/shipping/labels/S123456.BO.999888" {
		t.Fatalf("sandbox GHTK LabelURL = %q, want proxy labelBaseURL", created.LabelURL)
	}
	orderObj, _ := capturedCreateOrder["order"].(map[string]any)
	if orderObj["pick_province"] != "Tỉnh Bình Dương" || orderObj["pick_district"] != "Thành phố Thủ Dầu Một" {
		t.Fatalf("expected legacy 3-level Binh Duong translation, got %+v", orderObj)
	}
	if orderObj["province"] != "Thành phố Hà Nội" || orderObj["district"] != "Quận Hoàn Kiếm" {
		t.Fatalf("expected legacy 3-level Hanoi translation, got %+v", orderObj)
	}
	if orderObj["transport"] != "fly" {
		t.Fatalf("expected transport=fly for XFAST, got %v", orderObj["transport"])
	}

	st, err := adapter.GetStatus(context.Background(), "S123456.BO.999888")
	if err != nil || !st.Changed || st.Status != StatusDelivered || st.RawStatus != "6" || st.Description != "Đã đối soát" {
		t.Fatalf("unexpected GHTK sandbox status: %+v err=%v", st, err)
	}
	wantModifiedUTC := time.Date(2026, 11, 20, 5, 30, 0, 0, time.UTC)
	if !st.OccurredAt.Equal(wantModifiedUTC) {
		t.Fatalf("GHTK naive SQL modified '2026-11-20 12:30:00' must parse as %s (UTC+7), got %s", wantModifiedUTC.Format(time.RFC3339), st.OccurredAt.Format(time.RFC3339))
	}

	stRet, err := adapter.GetStatus(context.Background(), "GHTK-RETURNED")
	if err != nil || !stRet.Changed || stRet.Status != StatusReturned || stRet.RawStatus != "21" {
		t.Fatalf("unexpected GHTK returned status: %+v err=%v", stRet, err)
	}
	wantRetUTC := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	if !stRet.OccurredAt.Equal(wantRetUTC) {
		t.Fatalf("GHTK RFC3339 Z modified must preserve %s, got %s", wantRetUTC.Format(time.RFC3339), stRet.OccurredAt.Format(time.RFC3339))
	}

	if err := adapter.CancelShipment(context.Background(), "S123456.BO.999888"); err != nil {
		t.Fatalf("unexpected GHTK cancel error: %v", err)
	}

	// 4xx -> NonRetryableError; 5xx & 429 -> retryable error.
	if _, err := adapter.GetStatus(context.Background(), "ERR-400"); !IsNonRetryable(err) {
		t.Fatalf("expected NonRetryableError for HTTP 400, got %v", err)
	}
	if _, err := adapter.GetStatus(context.Background(), "ERR-503"); err == nil || IsNonRetryable(err) {
		t.Fatalf("expected retryable error for HTTP 503, got %v", err)
	}
	if _, err := adapter.GetStatus(context.Background(), "ERR-429"); err == nil || IsNonRetryable(err) {
		t.Fatalf("expected retryable error for HTTP 429, got %v", err)
	}
}

func TestMapCarrierStatusGHTKCodes(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"-1", StatusCancelled, true},
		{"1", StatusCreated, true},
		{"2", StatusCreated, true},
		{"3", StatusDelivering, true},
		{"da_lay_hang", StatusDelivering, true},
		{"12", StatusPicking, true},
		{"127", StatusPicking, true},
		{"128", StatusPicking, true},
		{"da_dieu_phoi_lay_hang", StatusPicking, true},
		{"4", StatusDelivering, true},
		{"5", StatusDelivering, true},
		{"123", StatusDelivering, true},
		{"6", StatusDelivered, true},
		{"45", StatusDelivered, true},
		{"9", StatusFailed, true},
		{"10", StatusFailed, true},
		{"49", StatusFailed, true},
		{"13", StatusReturning, true},
		{"20", StatusReturning, true},
		{"11", StatusReturned, true},
		{"21", StatusReturned, true},
		{"ready_to_pick", StatusPicking, true},
		{"waiting_to_return", StatusReturning, true},
		{"unknown_xyz", "", false},
	}
	for _, tc := range cases {
		got, ok := MapCarrierStatus(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("MapCarrierStatus(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestAggregatorGHNAndGHTKParallelAndCircuitBreakerFallback(t *testing.T) {
	req := FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "26740",
		FromDistrictID:   1442,
		FromGHNWardCode:  "20101",
		ToProvinceCode:   "01",
		ToWardCode:       "00070",
		ToDistrictID:     1489,
		ToGHNWardCode:    "1A0606",
		WeightGrams:      1500,
		CODAmountVND:     200000,
	}

	t.Run("GHN and GHTK return 4 sorted quotes in parallel", func(t *testing.T) {
		ghn := NewResilientCarrier(NewGHNAdapter(GHNConfig{Mode: "mock"}), DefaultResilienceConfig(discardLogger()))
		ghtk := NewResilientCarrier(NewGHTKAdapter(GHTKConfig{Mode: "mock"}), DefaultResilienceConfig(discardLogger()))
		agg := NewAggregator(discardLogger(), ghn, ghtk)

		quotes, usedFallback, err := agg.CalculateFeeAll(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if usedFallback {
			t.Fatalf("expected carrier quotes, not fallback")
		}
		if len(quotes) != 4 {
			t.Fatalf("expected 4 quotes (2 GHN + 2 GHTK), got %d", len(quotes))
		}
		seenGHN, seenGHTK := false, false
		for i, q := range quotes {
			if q.CarrierCode == CodeGHN {
				seenGHN = true
			}
			if q.CarrierCode == CodeGHTK {
				seenGHTK = true
			}
			if i > 0 && q.FeeVND < quotes[i-1].FeeVND {
				t.Fatalf("quotes not sorted ascending: %+v", quotes)
			}
		}
		if !seenGHN || !seenGHTK {
			t.Fatalf("expected quotes from both GHN and GHTK, got %+v", quotes)
		}
	})

	t.Run("GHTK circuit breaker open falls back to healthy GHN; both open falls back to table", func(t *testing.T) {
		downSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer downSrv.Close()

		fastRes := ResilienceConfig{
			AttemptTimeout:  100 * time.Millisecond,
			MaxAttempts:     2,
			BaseBackoff:     5 * time.Millisecond,
			MaxBackoff:      10 * time.Millisecond,
			CBMaxFailures:   2,
			CBOpenTimeout:   time.Second,
			CBHalfOpenLimit: 1,
			Logger:          discardLogger(),
		}
		ghtkDown := NewResilientCarrier(NewGHTKAdapter(GHTKConfig{
			Mode:       "sandbox",
			BaseURL:    downSrv.URL,
			Token:      "real-token",
			HTTPClient: downSrv.Client(),
		}), fastRes)
		ghnHealthy := NewResilientCarrier(NewGHNAdapter(GHNConfig{Mode: "mock"}), fastRes)

		agg := NewAggregator(discardLogger(), ghnHealthy, ghtkDown)
		quotes, usedFallback, err := agg.CalculateFeeAll(context.Background(), req)
		if err != nil || usedFallback || len(quotes) != 2 || quotes[0].CarrierCode != CodeGHN {
			t.Fatalf("expected only healthy GHN quotes while GHTK failed, got %+v fallback=%v err=%v", quotes, usedFallback, err)
		}
		if ghtkDown.State() != gobreaker.StateOpen {
			t.Fatalf("expected GHTK circuit breaker to be Open, got %s", ghtkDown.State())
		}

		// When GHN is also down, fallback fee table is served.
		ghnDown := NewResilientCarrier(NewGHNAdapter(GHNConfig{
			Mode:       "sandbox",
			BaseURL:    downSrv.URL,
			Token:      "real-token",
			HTTPClient: downSrv.Client(),
		}), fastRes)
		aggAllDown := NewAggregator(discardLogger(), ghnDown, ghtkDown)
		fbQuotes, usedFallback, err := aggAllDown.CalculateFeeAll(context.Background(), req)
		if err != nil || !usedFallback || len(fbQuotes) != 1 || !fbQuotes[0].IsFallback {
			t.Fatalf("expected fallback fee table when both GHN and GHTK fail, got %+v fallback=%v err=%v", fbQuotes, usedFallback, err)
		}
	})

	t.Run("ResilientCarrier skips retry on NonRetryableError and forwards CanPoll", func(t *testing.T) {
		var calls atomic.Int32
		stub := &stubCarrier{
			code: "strict",
			name: "Strict",
			createFn: func(_ context.Context, _ ShipmentRequest) (*ShipmentResult, error) {
				calls.Add(1)
				return nil, &NonRetryableError{Err: errors.New("400 invalid phone")}
			},
		}
		rc := NewResilientCarrier(stub, ResilienceConfig{
			AttemptTimeout: 100 * time.Millisecond,
			MaxAttempts:    3,
			BaseBackoff:    5 * time.Millisecond,
			CBMaxFailures:  1,
			Logger:         discardLogger(),
		})
		_, err := rc.CreateShipment(context.Background(), ShipmentRequest{})
		if !IsNonRetryable(err) {
			t.Fatalf("expected NonRetryableError, got %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("expected exactly 1 call without retry on 4xx, got %d", calls.Load())
		}
		if rc.State() != gobreaker.StateClosed {
			t.Fatalf("non-retryable 4xx must not trip circuit breaker, got %s", rc.State())
		}

		ghtkMock := NewGHTKAdapter(GHTKConfig{Mode: "mock"})
		resGHTK := NewResilientCarrier(ghtkMock, DefaultResilienceConfig(discardLogger()))
		if resGHTK.CanPoll() {
			t.Fatalf("expected wrapped mock GHTK CanPoll=false initially")
		}
		ghtkMock.SetMockStatus("GHTK-1234567890", "delivered")
		if !resGHTK.CanPoll() {
			t.Fatalf("expected wrapped mock GHTK CanPoll=true after SetMockStatus")
		}
		st, err := resGHTK.GetStatus(context.Background(), "GHTK-1234567890")
		if err != nil || !st.Changed || st.Status != StatusDelivered {
			t.Fatalf("unexpected wrapped GetStatus: %+v err=%v", st, err)
		}
		if err := resGHTK.CancelShipment(context.Background(), "GHTK-1234567890"); err != nil {
			t.Fatalf("unexpected wrapped CancelShipment err: %v", err)
		}
	})
}

type countingRedeliveryCarrier struct {
	Carrier
	calls atomic.Int32
}

func (c *countingRedeliveryCarrier) RequestRedelivery(ctx context.Context, trackingCode, preferredDate, note string) error {
	c.calls.Add(1)
	return c.Carrier.RequestRedelivery(ctx, trackingCode, preferredDate, note)
}

func TestGHTKRequestRedelivery_MockAllowedSandboxUnsupportedNoRetryNoCB(t *testing.T) {
	t.Run("mock mode preserves existing RequestRedelivery behavior", func(t *testing.T) {
		mockGHTK := NewGHTKAdapter(GHTKConfig{Mode: "mock"})
		if err := mockGHTK.RequestRedelivery(context.Background(), "GHTK-MOCK-1", "2026-11-28", "call"); err != nil {
			t.Fatalf("mock RequestRedelivery with tracking code must succeed, got %v", err)
		}
		if err := mockGHTK.RequestRedelivery(context.Background(), "   ", "2026-11-28", "call"); !IsNonRetryable(err) {
			t.Fatalf("mock RequestRedelivery with blank tracking code must return NonRetryableError, got %v", err)
		}
	})

	t.Run("sandbox mode returns non-retryable ErrRedeliveryUnsupported with 0 HTTP calls, 0 retries, CB closed", func(t *testing.T) {
		var httpCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httpCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		adapter := NewGHTKAdapter(GHTKConfig{
			Mode:       "sandbox",
			BaseURL:    srv.URL,
			Token:      "live-ghtk-sandbox-token",
			HTTPClient: srv.Client(),
		})
		counter := &countingRedeliveryCarrier{Carrier: adapter}
		rc := NewResilientCarrier(counter, ResilienceConfig{
			AttemptTimeout: 200 * time.Millisecond,
			MaxAttempts:    3,
			BaseBackoff:    5 * time.Millisecond,
			MaxBackoff:     10 * time.Millisecond,
			CBMaxFailures:  1,
			CBOpenTimeout:  30 * time.Second,
			Logger:         discardLogger(),
		})

		for i := 1; i <= 3; i++ {
			err := rc.RequestRedelivery(context.Background(), "S123456.BO.999888", "2026-11-28", "gọi trước")
			if err == nil {
				t.Fatalf("call #%d: sandbox GHTK RequestRedelivery must not falsely succeed", i)
			}
			if !errors.Is(err, ErrRedeliveryUnsupported) {
				t.Fatalf("call #%d: expected ErrRedeliveryUnsupported, got %v", i, err)
			}
			if !IsNonRetryable(err) {
				t.Fatalf("call #%d: expected NonRetryableError, got %v", i, err)
			}
			if rc.State() != gobreaker.StateClosed {
				t.Fatalf("call #%d: unsupported redelivery must NOT open circuit breaker, got state=%s", i, rc.State())
			}
		}
		if got := counter.calls.Load(); got != 3 {
			t.Fatalf("expected exactly 1 attempt per call (3 total, 0 retries), got %d", got)
		}
		if got := httpCalls.Load(); got != 0 {
			t.Fatalf("sandbox GHTK RequestRedelivery must send 0 HTTP requests, got %d", got)
		}
	})
}

func TestGHTKAdapter_DynamicSandboxModeViaSetTokenAndRace(t *testing.T) {
	const liveToken = "live-db-ghtk-token"
	var (
		httpCalls       atomic.Int32
		sawBadAuthToken atomic.Bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls.Add(1)
		tok := r.Header.Get("Token")
		if tok == "" || tok == placeholderGHTKToken {
			sawBadAuthToken.Store(true)
		}
		if tok != liveToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/services/shipment/fee":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "ok",
				"fee": map[string]any{
					"name":          "area1",
					"fee":           27000,
					"insurance_fee": 1500,
					"delivery":      true,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/services/shipment/order":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "created",
				"order": map[string]any{
					"partner_id": "ord-dyn-1",
					"label":      "S123456.DYN.0001",
					"fee":        28500,
					"status_id":  2,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/services/shipment/cancel/S123456.DYN.0001":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "cancelled"})
		case r.Method == http.MethodGet && r.URL.Path == "/services/shipment/v2/S123456.DYN.0001":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"message": "ok",
				"order": map[string]any{
					"label_id":    "S123456.DYN.0001",
					"status":      6,
					"status_text": "Đã giao hàng",
					"modified":    "2026-11-20 12:30:00",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	for _, initialTok := range []string{"", placeholderGHTKToken} {
		t.Run("initial_token="+initialTok, func(t *testing.T) {
			httpCalls.Store(0)
			adapter := NewGHTKAdapter(GHTKConfig{
				Mode:       "sandbox",
				BaseURL:    srv.URL,
				Token:      initialTok,
				HTTPClient: srv.Client(),
			})

			// Starts in mock mode when token is empty or placeholder.
			if !adapter.IsMock() {
				t.Fatalf("expected IsMock=true initially for token=%q", initialTok)
			}
			if adapter.CanPoll() {
				t.Fatalf("expected CanPoll=false initially for token=%q", initialTok)
			}
			if err := adapter.RequestRedelivery(context.Background(), "GHTK-MOCK-1", "2026-11-28", "note"); err != nil {
				t.Fatalf("mock RequestRedelivery failed before SetToken: %v", err)
			}
			if httpCalls.Load() != 0 {
				t.Fatalf("expected 0 HTTP calls in initial mock mode, got %d", httpCalls.Load())
			}

			// AdminUpdateCarrier calls TestConnection with candidate token BEFORE SetToken.
			if err := adapter.TestConnection(context.Background(), "wrong-candidate-token"); !errors.Is(err, ErrInvalidCarrierToken) {
				t.Fatalf("expected ErrInvalidCarrierToken from sandbox HTTP check on bad candidate token, got %v", err)
			}
			if err := adapter.TestConnection(context.Background(), liveToken); err != nil {
				t.Fatalf("expected nil from sandbox HTTP check on valid candidate token, got %v", err)
			}
			if httpCalls.Load() != 2 {
				t.Fatalf("expected TestConnection to hit sandbox HTTP server twice, got %d", httpCalls.Load())
			}

			// Loading real token via SetToken exits mock mode dynamically.
			adapter.SetToken(liveToken)
			if adapter.IsMock() {
				t.Fatalf("expected IsMock=false after SetToken(realToken) with GHTK_MODE=sandbox")
			}
			if !adapter.CanPoll() {
				t.Fatalf("expected CanPoll=true after SetToken(realToken) with GHTK_MODE=sandbox")
			}

			quotes, err := adapter.CalculateFee(context.Background(), FeeRequest{
				FromProvinceCode: "79", FromWardCode: "25747",
				ToProvinceCode: "01", ToWardCode: "00070",
				WeightGrams: 900, CODAmountVND: 200000,
			})
			if err != nil || len(quotes) != 1 || quotes[0].FeeVND != 28500 {
				t.Fatalf("expected sandbox HTTP fee 28500 after SetToken, got quotes=%+v err=%v", quotes, err)
			}
			created, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
				OrderID:     "ord-dyn-1",
				ServiceCode: "ROAD",
				WeightGrams: 900,
				FromAddress: Address{Name: "Shop", Phone: "0901112222", ProvinceCode: "79", WardCode: "25747", StreetAddress: "12 Le Loi"},
				ToAddress:   Address{Name: "Buyer", Phone: "0903334444", ProvinceCode: "01", WardCode: "00070", StreetAddress: "99 Hang Trong"},
			})
			if err != nil || created.TrackingCode != "S123456.DYN.0001" {
				t.Fatalf("expected sandbox HTTP CreateShipment tracking S123456.DYN.0001, got %+v err=%v", created, err)
			}
			st, err := adapter.GetStatus(context.Background(), created.TrackingCode)
			if err != nil || !st.Changed || st.Status != StatusDelivered {
				t.Fatalf("expected sandbox HTTP GetStatus delivered, got %+v err=%v", st, err)
			}
			if err := adapter.CancelShipment(context.Background(), created.TrackingCode); err != nil {
				t.Fatalf("expected sandbox HTTP CancelShipment nil, got %v", err)
			}
			if err := adapter.RequestRedelivery(context.Background(), created.TrackingCode, "2026-11-28", "note"); !errors.Is(err, ErrRedeliveryUnsupported) || !IsNonRetryable(err) {
				t.Fatalf("expected non-retryable ErrRedeliveryUnsupported in sandbox mode after SetToken, got %v", err)
			}

			// Reverting token to empty or placeholder returns to mock mode.
			for _, mockTok := range []string{"", "   ", placeholderGHTKToken} {
				adapter.SetToken(mockTok)
				if !adapter.IsMock() {
					t.Fatalf("expected IsMock=true after SetToken(%q)", mockTok)
				}
				if adapter.CanPoll() {
					t.Fatalf("expected CanPoll=false after SetToken(%q) with no mockStatuses", mockTok)
				}
				before := httpCalls.Load()
				mq, err := adapter.CalculateFee(context.Background(), FeeRequest{
					FromProvinceCode: "79", ToProvinceCode: "79", WeightGrams: 500,
				})
				if err != nil || len(mq) != 2 || httpCalls.Load() != before {
					t.Fatalf("expected mock quotes without HTTP call after SetToken(%q), got mq=%+v err=%v deltaCalls=%d", mockTok, mq, err, httpCalls.Load()-before)
				}
				if err := adapter.RequestRedelivery(context.Background(), "GHTK-MOCK-2", "2026-11-28", "note"); err != nil {
					t.Fatalf("expected mock RequestRedelivery to succeed after SetToken(%q), got %v", mockTok, err)
				}
			}
		})
	}

	t.Run("configured mock mode stays mock even after SetToken(realToken)", func(t *testing.T) {
		httpCalls.Store(0)
		adapter := NewGHTKAdapter(GHTKConfig{
			Mode:       "mock",
			BaseURL:    srv.URL,
			Token:      placeholderGHTKToken,
			HTTPClient: srv.Client(),
		})
		adapter.SetToken(liveToken)
		if !adapter.IsMock() {
			t.Fatalf("expected IsMock=true when configured mode is mock")
		}
		if adapter.CanPoll() {
			t.Fatalf("expected CanPoll=false when configured mode is mock and no mockStatuses")
		}
		if _, err := adapter.CalculateFee(context.Background(), FeeRequest{FromProvinceCode: "79", ToProvinceCode: "79", WeightGrams: 500}); err != nil {
			t.Fatalf("mock CalculateFee failed: %v", err)
		}
		if httpCalls.Load() != 0 {
			t.Fatalf("configured mock mode must send 0 HTTP calls even after SetToken, got %d", httpCalls.Load())
		}
	})

	t.Run("concurrent SetToken with IsMock/CanPoll/CalculateFee/RequestRedelivery is race-free", func(t *testing.T) {
		sawBadAuthToken.Store(false)
		adapter := NewGHTKAdapter(GHTKConfig{
			Mode:       "sandbox",
			BaseURL:    srv.URL,
			Token:      placeholderGHTKToken,
			HTTPClient: srv.Client(),
		})
		var wg sync.WaitGroup
		tokens := []string{liveToken, "", placeholderGHTKToken, liveToken}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 25; j++ {
					adapter.SetToken(tokens[(id+j)%len(tokens)])
					_ = adapter.IsMock()
					_ = adapter.CanPoll()
					adapter.SetMockStatus("GHTK-RACE-1", "6")
					_, _ = adapter.CalculateFee(context.Background(), FeeRequest{
						FromProvinceCode: "79", FromWardCode: "25747",
						ToProvinceCode: "01", ToWardCode: "00070",
						WeightGrams: 500,
					})
					_ = adapter.RequestRedelivery(context.Background(), "GHTK-RACE-1", "2026-11-28", "race")
					adapter.SetMockStatus("GHTK-RACE-1", "")
				}
			}(i)
		}
		wg.Wait()
		if sawBadAuthToken.Load() {
			t.Fatalf("sandbox HTTP request leaked empty or placeholder token due to TOCTOU race")
		}
	})
}

func TestGHTKAdapter_SandboxTestConnectionRejectsPlaceholderAndEmptyToken(t *testing.T) {
	const liveToken = "live-ghtk-sandbox-token-real"
	var httpCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls.Add(1)
		if r.Header.Get("Token") != liveToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "ok",
			"fee":     map[string]any{"fee": 25000, "insurance_fee": 0, "delivery": true},
		})
	}))
	defer srv.Close()

	t.Run("sandbox running real token rejects placeholder candidate without HTTP or mock fallback", func(t *testing.T) {
		httpCalls.Store(0)
		adapter := NewGHTKAdapter(GHTKConfig{
			Mode:       "sandbox",
			BaseURL:    srv.URL,
			Token:      liveToken,
			HTTPClient: srv.Client(),
		})
		if adapter.IsMock() {
			t.Fatalf("expected sandbox adapter with liveToken to have IsMock=false")
		}
		err := adapter.TestConnection(context.Background(), placeholderGHTKToken)
		if !errors.Is(err, ErrInvalidCarrierToken) || !IsNonRetryable(err) {
			t.Fatalf("sandbox TestConnection(placeholder): want non-retryable ErrInvalidCarrierToken, got %v", err)
		}
		if httpCalls.Load() != 0 {
			t.Fatalf("sandbox TestConnection(placeholder) must fail before HTTP, got %d calls", httpCalls.Load())
		}
	})

	t.Run("sandbox with placeholder or empty runtime token rejects empty and placeholder without HTTP", func(t *testing.T) {
		for _, runtimeTok := range []string{placeholderGHTKToken, "", "   "} {
			for _, candidateTok := range []string{"", "   ", placeholderGHTKToken} {
				httpCalls.Store(0)
				adapter := NewGHTKAdapter(GHTKConfig{
					Mode:       "sandbox",
					BaseURL:    srv.URL,
					Token:      runtimeTok,
					HTTPClient: srv.Client(),
				})
				err := adapter.TestConnection(context.Background(), candidateTok)
				if !errors.Is(err, ErrInvalidCarrierToken) || !IsNonRetryable(err) {
					t.Fatalf("sandbox(runtime=%q).TestConnection(%q): want non-retryable ErrInvalidCarrierToken, got %v", runtimeTok, candidateTok, err)
				}
				if httpCalls.Load() != 0 {
					t.Fatalf("sandbox(runtime=%q).TestConnection(%q) must send 0 HTTP calls, got %d", runtimeTok, candidateTok, httpCalls.Load())
				}
			}
		}
	})

	t.Run("configured mock mode preserves mock TestConnection behavior", func(t *testing.T) {
		httpCalls.Store(0)
		adapter := NewGHTKAdapter(GHTKConfig{
			Mode:       "mock",
			BaseURL:    srv.URL,
			Token:      placeholderGHTKToken,
			HTTPClient: srv.Client(),
		})
		if err := adapter.TestConnection(context.Background(), ""); err != nil {
			t.Fatalf("mock TestConnection with stored placeholder should still pass, got %v", err)
		}
		if err := adapter.TestConnection(context.Background(), placeholderGHTKToken); err != nil {
			t.Fatalf("mock TestConnection with explicit placeholder should still pass, got %v", err)
		}
		if err := adapter.TestConnection(context.Background(), "bad-token"); !errors.Is(err, ErrInvalidCarrierToken) || !IsNonRetryable(err) {
			t.Fatalf("mock TestConnection(bad-token): want non-retryable ErrInvalidCarrierToken, got %v", err)
		}
		if httpCalls.Load() != 0 {
			t.Fatalf("mock TestConnection must send 0 HTTP calls, got %d", httpCalls.Load())
		}
	})
}
