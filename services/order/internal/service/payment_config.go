package service

import (
	"context"

	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PaymentMethodConfig aliases clients.PaymentMethodConfig for convenience in service & tests.
type PaymentMethodConfig = clients.PaymentMethodConfig

// PaymentMethodConfigSource aliases clients.PaymentMethodConfigSource for convenience in service & tests.
type PaymentMethodConfigSource = clients.PaymentMethodConfigSource

// SetPaymentMethodConfigSource overrides the PaymentMethodConfigSource used on checkout and retry (F6).
func (s *OrderService) SetPaymentMethodConfigSource(src PaymentMethodConfigSource) {
	s.methodConfigs = src
}

// loadPaymentMethodConfig reads authoritative method settings directly from PaymentService without caching (F6).
// Always fails closed on missing source, transport/RPC errors, invalid configuration, or disabled method.
func (s *OrderService) loadPaymentMethodConfig(ctx context.Context, method string) (*PaymentMethodConfig, string, error) {
	norm := statemachine.NormalizePaymentMethod(method)
	if !statemachine.IsValidPaymentMethod(norm) {
		return nil, "INVALID_PAYMENT_METHOD", status.Errorf(codes.InvalidArgument, "unsupported payment method %q", method)
	}
	if s.methodConfigs == nil {
		return nil, "PAYMENT_METHOD_CONFIG_UNAVAILABLE", status.Error(
			codes.Unavailable,
			"PAYMENT_METHOD_CONFIG_UNAVAILABLE: payment method config source is not configured",
		)
	}
	cfg, err := s.methodConfigs.GetPaymentMethodConfig(clients.WithInternalAdminContext(ctx), norm)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, "PAYMENT_METHOD_NOT_CONFIGURED", status.Errorf(
				codes.FailedPrecondition,
				"PAYMENT_METHOD_NOT_CONFIGURED: payment method %s is not configured",
				norm,
			)
		}
		return nil, "PAYMENT_METHOD_CONFIG_UNAVAILABLE", status.Errorf(
			codes.Unavailable,
			"PAYMENT_METHOD_CONFIG_UNAVAILABLE: failed to load payment method config for %s: %v",
			norm,
			err,
		)
	}
	if cfg == nil {
		return nil, "PAYMENT_METHOD_CONFIG_UNAVAILABLE", status.Errorf(
			codes.Unavailable,
			"PAYMENT_METHOD_CONFIG_UNAVAILABLE: payment method config for %s is empty",
			norm,
		)
	}
	if reason, vErr := ValidatePaymentMethodConfigStructure(norm, cfg); vErr != nil {
		return nil, reason, vErr
	}
	return cfg, "", nil
}

// ValidatePaymentMethodConfigStructure checks that cfg is structurally valid and enabled (pure function, F6/F7).
func ValidatePaymentMethodConfigStructure(method string, cfg *PaymentMethodConfig) (string, error) {
	norm := statemachine.NormalizePaymentMethod(method)
	if !statemachine.IsValidPaymentMethod(norm) {
		return "INVALID_PAYMENT_METHOD", status.Errorf(codes.InvalidArgument, "unsupported payment method %q", method)
	}
	if cfg == nil {
		return "PAYMENT_METHOD_CONFIG_UNAVAILABLE", status.Errorf(
			codes.Unavailable,
			"PAYMENT_METHOD_CONFIG_UNAVAILABLE: missing payment method config for %s",
			norm,
		)
	}
	if cfg.MinOrderVND < 0 || cfg.MaxOrderVND < 0 || (cfg.MaxOrderVND > 0 && cfg.MinOrderVND > cfg.MaxOrderVND) {
		return "PAYMENT_METHOD_CONFIG_INVALID", status.Errorf(
			codes.FailedPrecondition,
			"PAYMENT_METHOD_CONFIG_INVALID: invalid min/max bounds (%d..%d VND) for %s",
			cfg.MinOrderVND,
			cfg.MaxOrderVND,
			norm,
		)
	}
	if statemachine.IsOnlineMethod(norm) && cfg.ExpiryMinutes <= 0 {
		return "PAYMENT_METHOD_CONFIG_INVALID", status.Errorf(
			codes.FailedPrecondition,
			"PAYMENT_METHOD_CONFIG_INVALID: online payment method %s has non-positive expiry_minutes %d",
			norm,
			cfg.ExpiryMinutes,
		)
	}
	if !cfg.Enabled {
		return "PAYMENT_METHOD_DISABLED", status.Errorf(
			codes.FailedPrecondition,
			"PAYMENT_METHOD_DISABLED: phương thức thanh toán %s hiện đang tạm tắt",
			norm,
		)
	}
	return "", nil
}

// ValidatePaymentMethodAmountBounds checks totalPayableVND against configured min/max VND bounds (pure function, F6).
// Zero-VND orders (100% covered by vouchers/coins) are exempt from min_order_vnd; disabled methods are already rejected earlier.
func ValidatePaymentMethodAmountBounds(method string, cfg *PaymentMethodConfig, totalPayableVND int64) (string, error) {
	norm := statemachine.NormalizePaymentMethod(method)
	if cfg == nil {
		return "PAYMENT_METHOD_CONFIG_UNAVAILABLE", status.Errorf(
			codes.Unavailable,
			"PAYMENT_METHOD_CONFIG_UNAVAILABLE: missing payment method config for %s",
			norm,
		)
	}
	if totalPayableVND > 0 && cfg.MinOrderVND > 0 && totalPayableVND < cfg.MinOrderVND {
		return "ORDER_AMOUNT_OUT_OF_RANGE", status.Errorf(
			codes.FailedPrecondition,
			"ORDER_AMOUNT_OUT_OF_RANGE: order total %d VND is below %s minimum %d VND",
			totalPayableVND,
			norm,
			cfg.MinOrderVND,
		)
	}
	if cfg.MaxOrderVND > 0 && totalPayableVND > cfg.MaxOrderVND {
		if statemachine.IsCODMethod(norm) {
			return "COD_LIMIT_EXCEEDED", status.Errorf(
				codes.FailedPrecondition,
				"COD_LIMIT_EXCEEDED: order total %d VND exceeds COD maximum %d VND",
				totalPayableVND,
				cfg.MaxOrderVND,
			)
		}
		return "ORDER_AMOUNT_OUT_OF_RANGE", status.Errorf(
			codes.FailedPrecondition,
			"ORDER_AMOUNT_OUT_OF_RANGE: order total %d VND exceeds %s maximum %d VND",
			totalPayableVND,
			norm,
			cfg.MaxOrderVND,
		)
	}
	return "", nil
}

// ValidatePaymentMethodConfig validates both config structure/enabled flag and order amount bounds (pure function, F6).
func ValidatePaymentMethodConfig(method string, cfg *PaymentMethodConfig, totalPayableVND int64) (string, error) {
	if reason, err := ValidatePaymentMethodConfigStructure(method, cfg); err != nil {
		return reason, err
	}
	return ValidatePaymentMethodAmountBounds(method, cfg, totalPayableVND)
}

// ComputePaymentFeeVND calculates seller-borne payment fee C_pay = floor(P * bps / 10000),
// capped at max(0, R - C_cat) so that C_total = C_cat + C_pay <= R and buyer total P is unchanged.
func ComputePaymentFeeVND(sellerRevenueVND, categoryCommissionVND, totalPayableVND int64, paymentFeeBPS int32) int64 {
	if paymentFeeBPS <= 0 || totalPayableVND <= 0 {
		return 0
	}
	rawPayFee := (totalPayableVND * int64(paymentFeeBPS)) / 10000
	remainingForPayFee := maxInt64(0, sellerRevenueVND-maxInt64(0, categoryCommissionVND))
	return minInt64(maxInt64(0, rawPayFee), remainingForPayFee)
}

// paymentProtoMethod maps a canonical method string to paymentv1.PaymentMethod.
func paymentProtoMethod(method string) (paymentv1.PaymentMethod, error) {
	switch statemachine.NormalizePaymentMethod(method) {
	case statemachine.PaymentMethodCOD:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_COD, nil
	case statemachine.PaymentMethodVIETQR:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, nil
	case statemachine.PaymentMethodZALOPAY:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY, nil
	default:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED, status.Errorf(
			codes.InvalidArgument,
			"unsupported payment method %q",
			method,
		)
	}
}
