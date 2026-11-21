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

	adapter := NewGHTKAdapter(GHTKConfig{Mode: "mock"})

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
		Direction:    "forward",
		ServiceCode:  "ROAD",
		WeightGrams:  1000,
		CODAmountVND: 100000,
		FromAddress:  Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:    Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil {
		t.Fatalf("mock CreateShipment failed: %v", err)
	}
	fwd2, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:      "ord-ghtk-1",
		Direction:    "forward",
		ServiceCode:  "ROAD",
		WeightGrams:  1000,
		CODAmountVND: 100000,
		FromAddress:  Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:    Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil {
		t.Fatalf("second mock CreateShipment failed: %v", err)
	}
	if fwd1.TrackingCode != fwd2.TrackingCode {
		t.Fatalf("expected deterministic GHTK tracking code, got %s vs %s", fwd1.TrackingCode, fwd2.TrackingCode)
	}
	if len(fwd1.TrackingCode) != len("GHTK-")+10 || fwd1.TrackingCode[:5] != "GHTK-" {
		t.Fatalf("expected GHTK-<10HEX>, got %q", fwd1.TrackingCode)
	}
	wantLabel := "https://sandbox.ghtk.local/labels/" + fwd1.TrackingCode + ".pdf"
	if fwd1.LabelURL != wantLabel {
		t.Fatalf("expected label %q, got %q", wantLabel, fwd1.LabelURL)
	}

	ret, err := adapter.CreateShipment(context.Background(), ShipmentRequest{
		OrderID:         "ord-ghtk-1",
		Direction:       "return",
		ReturnRequestID: "ret-1",
		ServiceCode:     "XFAST",
		WeightGrams:     1000,
		FromAddress:     Address{ProvinceCode: "01", WardCode: "01001"},
		ToAddress:       Address{ProvinceCode: "79", WardCode: "79001"},
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
		Mode:       "sandbox",
		BaseURL:    srv.URL,
		Token:      "live-ghtk-sandbox-token",
		HTTPClient: srv.Client(),
	})
	if adapter.IsMock() {
		t.Fatalf("expected sandbox adapter IsMock=false")
	}
	if !adapter.CanPoll() {
		t.Fatalf("expected sandbox adapter CanPoll=true")
	}

	// CalculateFee resolves legacy 3-level names for merged province Binh Duong (79006) -> Hanoi (01001).
	quotes, err := adapter.CalculateFee(context.Background(), FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "79006",
		ToProvinceCode:   "01",
		ToWardCode:       "01001",
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
			WardCode:      "79006",
			StreetAddress: "12 Dai Lo Binh Duong",
		},
		ToAddress: Address{
			Name:          "Buyer Hanoi",
			Phone:         "0903334444",
			ProvinceCode:  "01",
			WardCode:      "01001",
			StreetAddress: "99 Hang Trong",
		},
		ItemsSummary: "Ao thun x2",
	})
	if err != nil || created.TrackingCode != "S123456.BO.999888" || created.FeeVND != 28500 {
		t.Fatalf("unexpected sandbox GHTK create result: %+v err=%v", created, err)
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

	stRet, err := adapter.GetStatus(context.Background(), "GHTK-RETURNED")
	if err != nil || !stRet.Changed || stRet.Status != StatusReturned || stRet.RawStatus != "21" {
		t.Fatalf("unexpected GHTK returned status: %+v err=%v", stRet, err)
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
		{"3", StatusPicking, true},
		{"12", StatusPicking, true},
		{"127", StatusPicking, true},
		{"128", StatusPicking, true},
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
		ToProvinceCode:   "01",
		WeightGrams:      1500,
		CODAmountVND:     200000,
	}

	t.Run("GHN and GHTK return 4 sorted quotes in parallel", func(t *testing.T) {
		ghn := NewResilientCarrier(NewGHNAdapter(GHNConfig{Mode: "mock"}), DefaultResilienceConfig(discardLogger()))
		ghtk := NewResilientCarrier(NewGHTKAdapter(GHTKConfig{Mode: "mock"}), DefaultResilienceConfig(discardLogger()))
		agg := NewAggregator(discardLogger(), ghn, ghtk)

		quotes, usedFallback := agg.CalculateFeeAll(context.Background(), req)
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
		quotes, usedFallback := agg.CalculateFeeAll(context.Background(), req)
		if usedFallback || len(quotes) != 2 || quotes[0].CarrierCode != CodeGHN {
			t.Fatalf("expected only healthy GHN quotes while GHTK failed, got %+v fallback=%v", quotes, usedFallback)
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
		fbQuotes, usedFallback := aggAllDown.CalculateFeeAll(context.Background(), req)
		if !usedFallback || len(fbQuotes) != 1 || !fbQuotes[0].IsFallback {
			t.Fatalf("expected fallback fee table when both GHN and GHTK fail, got %+v fallback=%v", fbQuotes, usedFallback)
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
