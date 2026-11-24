package carrier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	CodeMockExpress = "mock_express"
	NameMockExpress = "Mock Express"
)

type mockCreateFault struct {
	statusCode int
	remaining  int
}

// mockFaultInjector holds test-only CreateShipment faults, disabled unless explicitly enabled
// via SHIPPING_TEST_FAULT_INJECTION=true (E18).
type mockFaultInjector struct {
	enabled atomic.Bool
	mu      sync.Mutex
	byOrder map[string]*mockCreateFault // "" matches any order
}

func (f *mockFaultInjector) setEnabled(v bool) {
	f.enabled.Store(v)
	if !v {
		f.mu.Lock()
		f.byOrder = nil
		f.mu.Unlock()
	}
}

func (f *mockFaultInjector) inject(orderID string, statusCode, count int) error {
	if !f.enabled.Load() {
		return errors.New("mock carrier fault injection is disabled (set SHIPPING_TEST_FAULT_INJECTION=true)")
	}
	if statusCode < 400 || statusCode > 599 {
		return fmt.Errorf("invalid fault status code %d", statusCode)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimSpace(orderID)
	if count <= 0 {
		delete(f.byOrder, key)
		return nil
	}
	if f.byOrder == nil {
		f.byOrder = make(map[string]*mockCreateFault)
	}
	f.byOrder[key] = &mockCreateFault{statusCode: statusCode, remaining: count}
	return nil
}

func (f *mockFaultInjector) consume(carrierCode, orderID string) error {
	if !f.enabled.Load() {
		return nil
	}
	f.mu.Lock()
	var code int
	if len(f.byOrder) > 0 {
		key := strings.TrimSpace(orderID)
		if fault, ok := f.byOrder[key]; ok && fault.remaining > 0 {
			code = fault.statusCode
			fault.remaining--
			if fault.remaining == 0 {
				delete(f.byOrder, key)
			}
		} else if fault, ok := f.byOrder[""]; ok && fault.remaining > 0 {
			code = fault.statusCode
			fault.remaining--
			if fault.remaining == 0 {
				delete(f.byOrder, "")
			}
		}
	}
	f.mu.Unlock()

	if code == 0 {
		return nil
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return &NonRetryableError{Err: fmt.Errorf("%w: mock %s injected auth error status=%d", ErrInvalidCarrierToken, carrierCode, code)}
	}
	if code >= 500 || code == http.StatusRequestTimeout || code == http.StatusTooManyRequests {
		return fmt.Errorf("mock %s injected transient error status=%d", carrierCode, code)
	}
	return &NonRetryableError{Err: fmt.Errorf("mock %s injected rejection status=%d", carrierCode, code)}
}

// MockExpressAdapter is the second carrier adapter proving multi-carrier pluggability.
type MockExpressAdapter struct {
	mu           sync.RWMutex
	token        string
	labelBaseURL string
	faults       mockFaultInjector
}

// SetTestFaultInjectionEnabled toggles test-only fault injection (gated by SHIPPING_TEST_FAULT_INJECTION).
func (m *MockExpressAdapter) SetTestFaultInjectionEnabled(enabled bool) {
	m.faults.setEnabled(enabled)
}

// InjectCreateFault schedules count simulated HTTP error responses on CreateShipment for orderID ("" = any order).
func (m *MockExpressAdapter) InjectCreateFault(orderID string, statusCode, count int) error {
	return m.faults.inject(orderID, statusCode, count)
}

// NewMockExpressAdapter creates a Mock Express adapter with the default public label base URL.
func NewMockExpressAdapter() *MockExpressAdapter {
	return NewMockExpressAdapterWithLabelBase(DefaultLabelBaseURL)
}

// NewMockExpressAdapterWithLabelBase creates a Mock Express adapter with a custom public label base URL.
func NewMockExpressAdapterWithLabelBase(labelBaseURL string) *MockExpressAdapter {
	base := strings.TrimRight(strings.TrimSpace(labelBaseURL), "/")
	if base == "" {
		base = DefaultLabelBaseURL
	}
	return &MockExpressAdapter{labelBaseURL: base}
}

func (m *MockExpressAdapter) Code() string { return CodeMockExpress }
func (m *MockExpressAdapter) Name() string { return NameMockExpress }
func (m *MockExpressAdapter) IsMock() bool { return true }

func (m *MockExpressAdapter) SetToken(token string) {
	m.mu.Lock()
	m.token = strings.TrimSpace(token)
	m.mu.Unlock()
}

func (m *MockExpressAdapter) getToken() string {
	m.mu.RLock()
	tok := m.token
	m.mu.RUnlock()
	return tok
}

func (m *MockExpressAdapter) CalculateFee(_ context.Context, req FeeRequest) ([]Quote, error) {
	baseFee, stdDays := routeZoneFee(req.FromProvinceCode, req.ToProvinceCode, 18000, 26000, 34000)
	weightSurcharge := weightFeeVND(req.WeightGrams, 1000, 500, 4000)
	codFee := int64(0)
	if req.CODAmountVND > 0 {
		codFee = 1500
	}
	stdTotal := baseFee + weightSurcharge + codFee
	fastDays := stdDays - 1
	if fastDays < 1 {
		fastDays = 1
	}

	return []Quote{
		{
			CarrierCode:   CodeMockExpress,
			CarrierName:   NameMockExpress,
			ServiceCode:   "STANDARD",
			ServiceName:   "Mock Express Tiêu Chuẩn",
			FeeVND:        stdTotal,
			EstimatedDays: stdDays,
			IsFallback:    false,
		},
		{
			CarrierCode:   CodeMockExpress,
			CarrierName:   NameMockExpress,
			ServiceCode:   "FAST",
			ServiceName:   "Mock Express Nhanh",
			FeeVND:        stdTotal + 10000,
			EstimatedDays: fastDays,
			IsFallback:    false,
		},
	}, nil
}

func (m *MockExpressAdapter) CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error) {
	if err := m.faults.consume(CodeMockExpress, req.OrderID); err != nil {
		return nil, err
	}
	svc := strings.ToUpper(strings.TrimSpace(req.ServiceCode))
	if svc == "" {
		svc = "STANDARD"
	}
	quotes, _ := m.CalculateFee(ctx, FeeRequest{
		FromProvinceCode: req.FromAddress.ProvinceCode,
		ToProvinceCode:   req.ToAddress.ProvinceCode,
		WeightGrams:      req.WeightGrams,
		CODAmountVND:     req.CODAmountVND,
	})
	fee := quotes[0].FeeVND
	for _, q := range quotes {
		if strings.EqualFold(q.ServiceCode, svc) {
			fee = q.FeeVND
			break
		}
	}
	tracking := mockTrackingCode("MOCK", req.ClientOrderCode)
	return &ShipmentResult{
		CarrierCode:  CodeMockExpress,
		ServiceCode:  svc,
		TrackingCode: tracking,
		LabelURL:     fmt.Sprintf("%s/%s", m.labelBaseURL, tracking),
		FeeVND:       fee,
		Status:       StatusCreated,
	}, nil
}

func (m *MockExpressAdapter) CancelShipment(_ context.Context, _ string) error {
	return nil
}

func (m *MockExpressAdapter) GetStatus(_ context.Context, trackingCode string) (*TrackingStatus, error) {
	return &TrackingStatus{
		TrackingCode: trackingCode,
		Changed:      false,
	}, nil
}

func (m *MockExpressAdapter) RequestRedelivery(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *MockExpressAdapter) TestConnection(_ context.Context, token string) error {
	tok := strings.TrimSpace(token)
	if tok == "" {
		tok = m.getToken()
	}
	if IsMockTokenRejected(tok, false) {
		return &NonRetryableError{Err: fmt.Errorf("%w: mock_express rejected API token", ErrInvalidCarrierToken)}
	}
	return nil
}
