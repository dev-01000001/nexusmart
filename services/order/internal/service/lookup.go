package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const orderLookupNotFoundMsg = "ORDER_LOOKUP_NOT_FOUND: không tìm thấy đơn hàng hoặc số điện thoại không khớp"

// LookupOrder provides guest-safe public order tracking by order_code + recipient phone (UC-B08, G7).
// Constant-time phone comparison and identical NotFound responses prevent order-code enumeration.
func (s *OrderService) LookupOrder(ctx context.Context, req *orderv1.LookupOrderRequest) (*orderv1.LookupOrderResponse, error) {
	if claims, ok := auth.ClaimsFromContext(ctx); ok && strings.TrimSpace(claims.Service) != "" {
		return nil, status.Error(codes.PermissionDenied, "service tokens cannot call public LookupOrder")
	}

	orderCode := strings.TrimSpace(req.GetOrderCode())
	rawPhone := strings.TrimSpace(req.GetPhone())
	if orderCode == "" || rawPhone == "" {
		return nil, status.Error(codes.InvalidArgument, "order_code and phone are required")
	}

	inputNorm, inputValid := normalizeVNPhone(rawPhone)

	agg, err := s.store.GetOrderByCode(ctx, orderCode)
	found := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, mapStoreError(err)
	}

	var storedNorm string
	var storedValid bool
	var snap orderv1.OrderAddressSnapshot
	if found && agg != nil {
		if len(agg.Order.ShippingAddressJSON) > 0 {
			_ = json.Unmarshal(agg.Order.ShippingAddressJSON, &snap)
		}
		storedNorm, storedValid = normalizeVNPhone(snap.GetRecipientPhone())
	}

	// Always perform a fixed-width constant-time comparison even on lookup miss or invalid format.
	matched := constantTimePhoneEqual(inputNorm, storedNorm)
	if !found || agg == nil || !inputValid || !storedValid || !matched {
		return nil, status.Error(codes.NotFound, orderLookupNotFoundMsg)
	}

	return &orderv1.LookupOrderResponse{
		Order: aggregateToPublicOrderSummary(*agg, &snap),
	}, nil
}

// normalizeVNPhone normalizes Vietnamese phone numbers to canonical 0xxxxxxxxx digits.
// Accepts formats like "+84 901-234-567", "0084901234567", "84901234567", "0901 234 567".
func normalizeVNPhone(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}

	hasLeadingPlus := strings.HasPrefix(s, "+")
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		if r == '+' && i == 0 {
			continue
		}
		if unicode.IsSpace(r) || r == '-' || r == '.' || r == '(' || r == ')' {
			continue
		}
		if r < '0' || r > '9' {
			return "", false
		}
		b.WriteRune(r)
	}
	digits := b.String()
	if digits == "" {
		return "", false
	}

	switch {
	case strings.HasPrefix(digits, "0084"):
		digits = "0" + digits[4:]
	case hasLeadingPlus && strings.HasPrefix(digits, "84"):
		digits = "0" + digits[2:]
	case !hasLeadingPlus && strings.HasPrefix(digits, "84") && (len(digits) == 11 || len(digits) == 12):
		digits = "0" + digits[2:]
	}

	if !strings.HasPrefix(digits, "0") || len(digits) < 10 || len(digits) > 11 {
		return "", false
	}
	return digits, true
}

func constantTimePhoneEqual(a, b string) bool {
	var bufA, bufB [16]byte
	copy(bufA[:], a)
	copy(bufB[:], b)
	lenMatch := subtle.ConstantTimeEq(int32(len(a)), int32(len(b)))
	bytesMatch := subtle.ConstantTimeCompare(bufA[:], bufB[:])
	return (lenMatch & bytesMatch) == 1
}

func maskRecipientName(raw string) string {
	words := strings.Fields(strings.TrimSpace(raw))
	if len(words) == 0 {
		return ""
	}
	firstRunes := []rune(words[0])
	if len(words) == 1 {
		if len(firstRunes) <= 1 {
			return "*"
		}
		return string(firstRunes[0]) + "***"
	}
	lastRunes := []rune(words[len(words)-1])
	if len(lastRunes) <= 1 {
		return string(firstRunes[0]) + "*** " + string(lastRunes[0])
	}
	return string(firstRunes[0]) + "*** " + string(lastRunes[0]) + "***"
}

func maskRecipientPhone(raw string) string {
	norm, ok := normalizeVNPhone(raw)
	if !ok {
		var b strings.Builder
		for _, r := range raw {
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		norm = b.String()
	}
	if len(norm) == 0 {
		return ""
	}
	if len(norm) >= 7 {
		return norm[:3] + "***" + norm[len(norm)-4:]
	}
	if len(norm) >= 4 {
		return norm[:2] + "***" + norm[len(norm)-2:]
	}
	return "***"
}

func aggregateToPublicOrderSummary(agg store.OrderAggregate, snap *orderv1.OrderAddressSnapshot) *orderv1.PublicOrderSummary {
	ord := agg.Order
	items := make([]*orderv1.PublicOrderItem, 0, len(agg.Items))
	for _, it := range agg.Items {
		items = append(items, &orderv1.PublicOrderItem{
			ProductName:  it.ProductName,
			SkuName:      it.SKUName,
			Quantity:     it.Quantity,
			LineTotalVnd: it.LineTotalVND,
		})
	}

	// Contract r1 (G7): PublicOrderTimelineEvent exposes ONLY to_status and created_at (no internal notes/actor IDs).
	timeline := make([]*orderv1.PublicOrderTimelineEvent, 0, len(agg.Timeline))
	for _, tl := range agg.Timeline {
		timeline = append(timeline, &orderv1.PublicOrderTimelineEvent{
			ToStatus:  statemachine.StringToStatus(tl.ToStatus),
			CreatedAt: timestamppb.New(tl.CreatedAt),
		})
	}

	out := &orderv1.PublicOrderSummary{
		OrderCode:            ord.OrderCode,
		Status:               statemachine.StringToStatus(ord.Status),
		PaymentMethod:        statemachine.StringToPaymentMethod(ord.PaymentMethod),
		PaymentStatus:        ord.PaymentStatus,
		SubtotalVnd:          ord.SubtotalVND,
		ShippingFeeVnd:       ord.ShippingFeeVND,
		TotalPayableVnd:      ord.TotalPayableVND,
		CarrierCode:          ord.CarrierCode,
		TrackingCode:         ord.TrackingCode,
		ProvinceName:         strings.TrimSpace(snap.GetProvinceName()),
		WardName:             strings.TrimSpace(snap.GetWardName()),
		RecipientNameMasked:  maskRecipientName(snap.GetRecipientName()),
		RecipientPhoneMasked: maskRecipientPhone(snap.GetRecipientPhone()),
		Items:                items,
		Timeline:             timeline,
		CreatedAt:            timestamppb.New(ord.CreatedAt),
		UpdatedAt:            timestamppb.New(ord.UpdatedAt),
	}
	if ord.DeliveredAt != nil {
		out.DeliveredAt = timestamppb.New(*ord.DeliveredAt)
	}
	return out
}
