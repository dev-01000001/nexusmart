package clients

import (
	"context"
	"strings"
	"sync"

	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PaymentMethodConfig holds the authoritative runtime rules for a payment method
// (backed by payment_method_configs in PaymentService; F6).
type PaymentMethodConfig struct {
	Method        string // Canonical uppercase: "COD", "VIETQR", "ZALOPAY"
	Enabled       bool
	MinOrderVND   int64 // 0 means no minimum
	MaxOrderVND   int64 // 0 means no maximum (unlimited)
	ExpiryMinutes int32 // >0 for online methods; 0 for COD
}

// PaymentMethodConfigSource fetches authoritative payment method configuration directly
// from PaymentService on every checkout/retry without caching (F6).
type PaymentMethodConfigSource interface {
	GetPaymentMethodConfig(ctx context.Context, method string) (*PaymentMethodConfig, error)
}

// GRPCPaymentMethodConfigSource reads payment method configs directly from PaymentService.GetPaymentMethodConfigs
// using an internal service token with zero local caching (F6).
type GRPCPaymentMethodConfigSource struct {
	client PaymentClient
}

// NewGRPCPaymentMethodConfigSource constructs a direct gRPC PaymentMethodConfigSource backed by client.
func NewGRPCPaymentMethodConfigSource(client PaymentClient) *GRPCPaymentMethodConfigSource {
	return &GRPCPaymentMethodConfigSource{client: client}
}

// GetPaymentMethodConfig queries PaymentService.GetPaymentMethodConfigs for method using a service token.
func (s *GRPCPaymentMethodConfigSource) GetPaymentMethodConfig(ctx context.Context, method string) (*PaymentMethodConfig, error) {
	if s == nil || s.client == nil {
		return nil, status.Error(codes.Unavailable, "payment client is not configured")
	}
	norm := statemachine.NormalizePaymentMethod(method)
	protoMethod, err := toPaymentProtoMethod(norm)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.GetPaymentMethodConfigs(WithInternalAdminContext(ctx), &paymentv1.GetPaymentMethodConfigsRequest{
		Method: protoMethod,
	})
	if err != nil {
		return nil, err
	}
	for _, cfg := range resp.GetConfigs() {
		if cfg == nil {
			continue
		}
		if cfg.GetMethod() == protoMethod {
			return &PaymentMethodConfig{
				Method:        norm,
				Enabled:       cfg.GetEnabled(),
				MinOrderVND:   cfg.GetMinOrderVnd(),
				MaxOrderVND:   cfg.GetMaxOrderVnd(),
				ExpiryMinutes: cfg.GetExpiryMinutes(),
			}, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "payment method %s not configured", norm)
}

func toPaymentProtoMethod(norm string) (paymentv1.PaymentMethod, error) {
	switch norm {
	case statemachine.PaymentMethodCOD:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_COD, nil
	case statemachine.PaymentMethodVIETQR:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, nil
	case statemachine.PaymentMethodZALOPAY:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY, nil
	default:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED, status.Errorf(codes.InvalidArgument, "unsupported payment method %q", norm)
	}
}

// FakePaymentMethodConfigSource is a thread-safe in-memory PaymentMethodConfigSource for unit/integration tests.
type FakePaymentMethodConfigSource struct {
	mu      sync.Mutex
	configs map[string]PaymentMethodConfig
	err     error
	errBy   map[string]error
	calls   []string
	onGet   func(ctx context.Context, method string) (*PaymentMethodConfig, error)
}

// DefaultTestPaymentMethodConfigs returns the seeded F6 defaults (COD max 5,000,000 VND; VIETQR/ZALOPAY 15m).
func DefaultTestPaymentMethodConfigs() map[string]PaymentMethodConfig {
	return map[string]PaymentMethodConfig{
		statemachine.PaymentMethodCOD: {
			Method:        statemachine.PaymentMethodCOD,
			Enabled:       true,
			MinOrderVND:   0,
			MaxOrderVND:   5000000,
			ExpiryMinutes: 0,
		},
		statemachine.PaymentMethodVIETQR: {
			Method:        statemachine.PaymentMethodVIETQR,
			Enabled:       true,
			MinOrderVND:   0,
			MaxOrderVND:   0,
			ExpiryMinutes: 15,
		},
		statemachine.PaymentMethodZALOPAY: {
			Method:        statemachine.PaymentMethodZALOPAY,
			Enabled:       true,
			MinOrderVND:   0,
			MaxOrderVND:   0,
			ExpiryMinutes: 15,
		},
	}
}

// NewFakePaymentMethodConfigSource constructs a FakePaymentMethodConfigSource initialized with a copy of cfgs.
// Passing nil uses DefaultTestPaymentMethodConfigs().
func NewFakePaymentMethodConfigSource(cfgs map[string]PaymentMethodConfig) *FakePaymentMethodConfigSource {
	if cfgs == nil {
		cfgs = DefaultTestPaymentMethodConfigs()
	}
	copied := make(map[string]PaymentMethodConfig, len(cfgs))
	for k, v := range cfgs {
		norm := statemachine.NormalizePaymentMethod(k)
		v.Method = norm
		copied[norm] = v
	}
	return &FakePaymentMethodConfigSource{
		configs: copied,
		errBy:   make(map[string]error),
	}
}

// GetPaymentMethodConfig returns the configured rule for method or an error if unavailable/missing.
func (f *FakePaymentMethodConfigSource) GetPaymentMethodConfig(ctx context.Context, method string) (*PaymentMethodConfig, error) {
	norm := statemachine.NormalizePaymentMethod(method)
	f.mu.Lock()
	f.calls = append(f.calls, norm)
	hook := f.onGet
	globalErr := f.err
	methodErr := f.errBy[norm]
	cfg, ok := f.configs[norm]
	f.mu.Unlock()

	if hook != nil {
		return hook(ctx, norm)
	}
	if globalErr != nil {
		return nil, globalErr
	}
	if methodErr != nil {
		return nil, methodErr
	}
	if !ok {
		return nil, status.Errorf(codes.NotFound, "payment method %s not configured", strings.TrimSpace(method))
	}
	cp := cfg
	return &cp, nil
}

// SetConfig inserts or replaces a method's configuration safely for tests.
func (f *FakePaymentMethodConfigSource) SetConfig(cfg PaymentMethodConfig) {
	norm := statemachine.NormalizePaymentMethod(cfg.Method)
	cfg.Method = norm
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.configs == nil {
		f.configs = make(map[string]PaymentMethodConfig)
	}
	f.configs[norm] = cfg
}

// SetError configures a global error returned for all GetPaymentMethodConfig lookups.
func (f *FakePaymentMethodConfigSource) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// SetMethodError configures an error for a single payment method lookup.
func (f *FakePaymentMethodConfigSource) SetMethodError(method string, err error) {
	norm := statemachine.NormalizePaymentMethod(method)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errBy == nil {
		f.errBy = make(map[string]error)
	}
	if err == nil {
		delete(f.errBy, norm)
		return
	}
	f.errBy[norm] = err
}

// SetHook installs a custom callback for GetPaymentMethodConfig.
func (f *FakePaymentMethodConfigSource) SetHook(fn func(ctx context.Context, method string) (*PaymentMethodConfig, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onGet = fn
}

// Calls returns a snapshot of normalized methods queried so far.
func (f *FakePaymentMethodConfigSource) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
