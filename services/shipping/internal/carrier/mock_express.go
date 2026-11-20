package carrier

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	CodeMockExpress = "mock_express"
	NameMockExpress = "Mock Express"
)

// MockExpressAdapter is the second carrier adapter proving multi-carrier pluggability.
type MockExpressAdapter struct{}

// NewMockExpressAdapter creates a Mock Express adapter.
func NewMockExpressAdapter() *MockExpressAdapter {
	return &MockExpressAdapter{}
}

func (m *MockExpressAdapter) Code() string { return CodeMockExpress }
func (m *MockExpressAdapter) Name() string { return NameMockExpress }
func (m *MockExpressAdapter) IsMock() bool { return true }

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
	shortID := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
	tracking := "MOCK-" + shortID
	return &ShipmentResult{
		CarrierCode:  CodeMockExpress,
		ServiceCode:  svc,
		TrackingCode: tracking,
		LabelURL:     fmt.Sprintf("https://mock-express.local/labels/%s.pdf", tracking),
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
