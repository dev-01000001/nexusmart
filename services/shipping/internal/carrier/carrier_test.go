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
	code        string
	name        string
	feeFn       func(ctx context.Context, req FeeRequest) ([]Quote, error)
	createFn    func(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error)
	cancelFn    func(ctx context.Context, trackingCode string) error
	statusFn    func(ctx context.Context, trackingCode string) (*TrackingStatus, error)
	feeCalls    atomic.Int32
	createCalls atomic.Int32
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

		quotes, usedFallback := agg.CalculateFeeAll(context.Background(), req)
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

		quotes, usedFallback := agg.CalculateFeeAll(context.Background(), req)
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

		quotes, usedFallback := agg.CalculateFeeAll(context.Background(), req)
		if !usedFallback {
			t.Fatalf("expected fallback flag true when all carriers fail")
		}
		if len(quotes) != 1 || !quotes[0].IsFallback || quotes[0].FeeVND <= 0 {
			t.Fatalf("expected 1 valid fallback quote, got %+v", quotes)
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
		Mode:       "sandbox",
		BaseURL:    srv.URL,
		Token:      "test-token",
		ShopID:     12345,
		HTTPClient: srv.Client(),
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

	st, err := adapter.GetStatus(context.Background(), "GHN-SANDBOX-999")
	if err != nil || st.Status != StatusDelivered || !st.Changed {
		t.Fatalf("unexpected sandbox status: %+v err=%v", st, err)
	}

	if err := adapter.CancelShipment(context.Background(), "GHN-SANDBOX-999"); err != nil {
		t.Fatalf("unexpected sandbox cancel error: %v", err)
	}
}
