package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/resilience"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	shopCheckMaxConcurrency = 8
	shopCheckDefaultTimeout = 3 * time.Second
	shopCheckRetryAttempts  = 2
	shopCheckRetryBackoff   = 50 * time.Millisecond
)

// SetUserShopBreaker overrides the UserService.GetShopInternal circuit breaker (useful in tests).
func (s *OrderService) SetUserShopBreaker(cb *resilience.CircuitBreaker) {
	s.userShopBreaker = cb
}

func (s *OrderService) userShopAttemptTimeout() time.Duration {
	t := shopCheckDefaultTimeout
	if s.cfg.GRPCCallTimeout > 0 && s.cfg.GRPCCallTimeout < t {
		t = s.cfg.GRPCCallTimeout
	}
	return t
}

// verifyShopsActive synchronously checks UserService.GetShopInternal in parallel (bounded errgroup + per-attempt
// timeout + circuit breaker) for every distinct shopID and accepts ONLY SHOP_STATUS_ACTIVE (F12).
// Suspended shops fail with FailedPrecondition SHOP_SUSPENDED; User unavailability/timeout/open-circuit fails closed
// with Unavailable (never bypassed).
func (s *OrderService) verifyShopsActive(ctx context.Context, shopIDs []string) (map[string]*userv1.Shop, string, error) {
	seen := make(map[string]struct{}, len(shopIDs))
	unique := make([]string, 0, len(shopIDs))
	for _, raw := range shopIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return map[string]*userv1.Shop{}, "", nil
	}
	sort.Strings(unique)

	if s.clients.User == nil {
		return nil, "SHOP_STATUS_CHECK_UNAVAILABLE", status.Error(
			codes.Unavailable,
			"SHOP_STATUS_CHECK_UNAVAILABLE: user service is unavailable",
		)
	}

	type checkOutcome struct {
		shop       *userv1.Shop
		failReason string
		err        error
		cancelled  bool
	}
	outcomes := make([]checkOutcome, len(unique))
	attemptTimeout := s.userShopAttemptTimeout()

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(shopCheckMaxConcurrency)

	for idx, sid := range unique {
		i, shopID := idx, sid
		g.Go(func() error {
			resp, err := resilience.CallWithRetry(
				gCtx,
				s.userShopBreaker,
				shopCheckRetryAttempts,
				shopCheckRetryBackoff,
				attemptTimeout,
				func(attemptCtx context.Context) (*userv1.GetShopInternalResponse, error) {
					return s.clients.User.GetShopInternal(clients.WithInternalAdminContext(attemptCtx), &userv1.GetShopInternalRequest{ShopId: shopID})
				},
			)
			if err != nil {
				if status.Code(err) == codes.NotFound {
					notFoundErr := status.Errorf(
						codes.FailedPrecondition,
						"SHOP_NOT_ACTIVE: gian hàng %s không tồn tại hoặc không hoạt động",
						shopID,
					)
					outcomes[i] = checkOutcome{failReason: "SHOP_NOT_ACTIVE", err: notFoundErr}
					return notFoundErr
				}
				// If a sibling goroutine failed first and cancelled gCtx, record as peer-cancelled
				// so deterministic inspection reports the real failure rather than Canceled.
				if errors.Is(err, context.Canceled) && ctx.Err() == nil {
					outcomes[i] = checkOutcome{cancelled: true, err: err}
					return err
				}
				unavailErr := status.Errorf(
					codes.Unavailable,
					"SHOP_STATUS_CHECK_UNAVAILABLE: failed to verify status of shop %s: %v",
					shopID,
					err,
				)
				outcomes[i] = checkOutcome{failReason: "SHOP_STATUS_CHECK_UNAVAILABLE", err: unavailErr}
				return unavailErr
			}

			shop := resp.GetShop()
			if shop == nil {
				nilErr := status.Errorf(
					codes.FailedPrecondition,
					"SHOP_NOT_ACTIVE: gian hàng %s không tồn tại hoặc không hoạt động",
					shopID,
				)
				outcomes[i] = checkOutcome{failReason: "SHOP_NOT_ACTIVE", err: nilErr}
				return nilErr
			}
			if shop.GetStatus() == userv1.ShopStatus_SHOP_STATUS_SUSPENDED {
				suspErr := status.Errorf(
					codes.FailedPrecondition,
					"SHOP_SUSPENDED: gian hàng %s đang tạm ngưng hoạt động",
					shopID,
				)
				outcomes[i] = checkOutcome{failReason: "SHOP_SUSPENDED", err: suspErr}
				return suspErr
			}
			if shop.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
				inactErr := status.Errorf(
					codes.FailedPrecondition,
					"SHOP_NOT_ACTIVE: gian hàng %s hiện không ở trạng thái hoạt động (%s)",
					shopID,
					shop.GetStatus().String(),
				)
				outcomes[i] = checkOutcome{failReason: "SHOP_NOT_ACTIVE", err: inactErr}
				return inactErr
			}
			outcomes[i] = checkOutcome{shop: shop}
			return nil
		})
	}

	if waitErr := g.Wait(); waitErr != nil {
		// Deterministic priority: SHOP_SUSPENDED > SHOP_NOT_ACTIVE > SHOP_STATUS_CHECK_UNAVAILABLE.
		for _, want := range []string{"SHOP_SUSPENDED", "SHOP_NOT_ACTIVE", "SHOP_STATUS_CHECK_UNAVAILABLE"} {
			for _, oc := range outcomes {
				if oc.err != nil && !oc.cancelled && oc.failReason == want {
					return nil, oc.failReason, oc.err
				}
			}
		}
		return nil, "SHOP_STATUS_CHECK_UNAVAILABLE", status.Errorf(
			codes.Unavailable,
			"SHOP_STATUS_CHECK_UNAVAILABLE: shop status verification failed: %v",
			waitErr,
		)
	}

	shopsByID := make(map[string]*userv1.Shop, len(unique))
	for i, shopID := range unique {
		shopsByID[shopID] = outcomes[i].shop
	}
	return shopsByID, "", nil
}

// assessCOD enforces all E19 COD eligibility rules + F6 COD min/max order bounds (when checkAmountBounds=true):
//  1. Verified buyer phone when ORDER_COD_REQUIRE_PHONE_VERIFIED=true (COD_PHONE_NOT_VERIFIED).
//  2. Destination ward and chosen carrier quote support COD for every shop (COD_AREA_NOT_SUPPORTED).
//  3. Cumulative user cod_rejected_count < ORDER_COD_MAX_REJECTED_COUNT (COD_REJECTED_LIMIT_EXCEEDED).
//  4. Single indexed terminal COD rejection rate: total >= ORDER_COD_MIN_ORDERS_FOR_RATE and
//     rejected*10000 >= total*ORDER_COD_MAX_REJECT_RATE_BPS (COD_REJECTED_LIMIT_EXCEEDED).
//  5. Authoritative PaymentMethodConfigSource min/max bounds for COD (COD_LIMIT_EXCEEDED / ORDER_AMOUNT_OUT_OF_RANGE).
//
// Shared across BOTH CreateCheckout (including D11 retries) AND RetryPayment switching Online -> COD (F5).
func (s *OrderService) assessCOD(
	ctx context.Context,
	buyerID string,
	valResp *userv1.ValidateUserResponse,
	orderedShopIDs []string,
	shopCODSupport map[string]bool,
	totalPayableVND int64,
	codCfg *PaymentMethodConfig,
	checkAmountBounds bool,
) (string, error) {
	if valResp == nil {
		return "USER_VALIDATION_FAILED", status.Error(codes.Unavailable, "user validation response unavailable for COD check")
	}
	if s.cfg.CODRequirePhoneVerified && !valResp.GetPhoneVerified() {
		return "COD_PHONE_NOT_VERIFIED", status.Error(
			codes.FailedPrecondition,
			"COD_PHONE_NOT_VERIFIED: vui lòng xác minh số điện thoại trước khi đặt hàng thanh toán khi nhận hàng (COD)",
		)
	}
	for _, shopID := range orderedShopIDs {
		if !shopCODSupport[shopID] {
			return "COD_AREA_NOT_SUPPORTED", status.Errorf(
				codes.FailedPrecondition,
				"COD_AREA_NOT_SUPPORTED: khu vực giao hàng hoặc đơn vị vận chuyển của shop %s không hỗ trợ thanh toán khi nhận hàng (COD)",
				shopID,
			)
		}
	}
	maxRejected := s.codMaxRejectedCount()
	if valResp.GetCodRejectedCount() >= maxRejected {
		return "COD_REJECTED_LIMIT_EXCEEDED", status.Errorf(
			codes.FailedPrecondition,
			"COD_REJECTED_LIMIT_EXCEEDED: account has %d rejected COD deliveries (limit %d)",
			valResp.GetCodRejectedCount(),
			maxRejected,
		)
	}
	codStats, err := s.store.GetBuyerTerminalCODStats(ctx, buyerID)
	if err != nil {
		return "COD_RISK_CHECK_UNAVAILABLE", status.Errorf(
			codes.Unavailable,
			"COD_RISK_CHECK_UNAVAILABLE: failed to evaluate COD order history: %v",
			err,
		)
	}
	minOrders := s.codMinOrdersForRate()
	maxRateBPS := s.codMaxRejectRateBPS()
	if codStats.TotalTerminal >= minOrders && codStats.RejectedCount*10000 >= codStats.TotalTerminal*maxRateBPS {
		return "COD_REJECTED_LIMIT_EXCEEDED", status.Errorf(
			codes.FailedPrecondition,
			"COD_REJECTED_LIMIT_EXCEEDED: account COD delivery rejection rate (%d/%d) exceeds allowed limit (%d bps)",
			codStats.RejectedCount,
			codStats.TotalTerminal,
			maxRateBPS,
		)
	}
	if checkAmountBounds {
		if reason, bErr := ValidatePaymentMethodAmountBounds(statemachine.PaymentMethodCOD, codCfg, totalPayableVND); bErr != nil {
			return reason, bErr
		}
	}
	return "", nil
}

// verifyOrderCODShippingSupport queries ShippingService.CalculateFee for every sub-order when switching
// an existing PENDING online checkout group to COD (F5 + E19), checking that the destination ward and
// sub-order's carrier/service support COD without altering buyer shipping fees.
func (s *OrderService) verifyOrderCODShippingSupport(
	ctx context.Context,
	aggs []store.OrderAggregate,
	shopsByID map[string]*userv1.Shop,
) ([]string, map[string]bool, error) {
	if s.clients.Shipping == nil {
		return nil, nil, status.Error(codes.Unavailable, "COD_AREA_CHECK_UNAVAILABLE: shipping service is unavailable")
	}
	orderedShopIDs := make([]string, 0, len(aggs))
	support := make(map[string]bool, len(aggs))

	for _, agg := range aggs {
		ord := agg.Order
		orderedShopIDs = append(orderedShopIDs, ord.ShopID)

		var addr orderv1.OrderAddressSnapshot
		if len(ord.ShippingAddressJSON) > 0 {
			_ = json.Unmarshal(ord.ShippingAddressJSON, &addr)
		}
		if strings.TrimSpace(addr.GetProvinceCode()) == "" || strings.TrimSpace(addr.GetWardCode()) == "" {
			support[ord.ShopID] = false
			continue
		}

		var (
			pickup *shippingv1.ShipmentAddress
			pErr   error
		)
		if shop := shopsByID[ord.ShopID]; shop != nil {
			pickup, pErr = s.resolveShopPickupAddressFromShop(ctx, shop)
		} else {
			pickup, pErr = s.resolveShopPickupAddress(ctx, ord.ShopID)
		}
		if pErr != nil {
			if status.Code(pErr) == codes.FailedPrecondition {
				return nil, nil, pErr
			}
			return nil, nil, status.Errorf(codes.Unavailable, "COD_AREA_CHECK_UNAVAILABLE: %v", pErr)
		}

		totalWeight, _ := summarizeOrderParcel(agg.Items)
		feeResp, err := s.clients.Shipping.CalculateFee(ctx, &shippingv1.CalculateFeeRequest{
			ShopId:           ord.ShopID,
			FromProvinceCode: pickup.GetProvinceCode(),
			FromWardCode:     pickup.GetWardCode(),
			ToProvinceCode:   addr.GetProvinceCode(),
			ToWardCode:       addr.GetWardCode(),
			WeightGrams:      totalWeight,
			CodAmountVnd:     ord.TotalPayableVND,
		})
		if err != nil {
			return nil, nil, wrapDownstreamError("COD_AREA_CHECK_UNAVAILABLE: calculate shipping fee for COD switch", err)
		}
		if feeResp.GetIsEstimated() || len(feeResp.GetQuotes()) == 0 {
			support[ord.ShopID] = false
			continue
		}

		if carrier := strings.TrimSpace(ord.CarrierCode); carrier != "" && !strings.EqualFold(carrier, "fallback") {
			// Item H (#11): Order already has a fixed carrier/service — only accept COD support
			// from that exact carrier/service without falling back to another carrier.
			matched := matchExactCarrierQuote(feeResp.GetQuotes(), carrier, strings.TrimSpace(ord.ServiceCode))
			support[ord.ShopID] = matched != nil && matched.GetCodSupported()
		} else {
			chosen := pickShippingQuote(feeResp.GetQuotes(), nil, statemachine.PaymentMethodCOD)
			support[ord.ShopID] = chosen != nil && chosen.GetCodSupported()
		}
	}

	return orderedShopIDs, support, nil
}

func matchExactCarrierQuote(quotes []*shippingv1.CarrierQuote, carrierCode, serviceCode string) *shippingv1.CarrierQuote {
	var firstCarrierMatch *shippingv1.CarrierQuote
	for _, q := range quotes {
		if q == nil || !strings.EqualFold(q.GetCarrierCode(), carrierCode) {
			continue
		}
		if serviceCode != "" {
			if strings.EqualFold(q.GetServiceCode(), serviceCode) {
				return q
			}
			continue
		}
		if q.GetCodSupported() {
			return q
		}
		if firstCarrierMatch == nil {
			firstCarrierMatch = q
		}
	}
	return firstCarrierMatch
}

// RetrySwitchInput carries all pre-fetched authoritative inputs for the pure F5 retry/switch planner.
type RetrySwitchInput struct {
	Group           db.CheckoutGroup
	Aggs            []store.OrderAggregate
	ExpectedPayID   string
	RequestedMethod string // Empty or UNSPECIFIED keeps the group's current online method.
	MethodConfig    *PaymentMethodConfig
	PaymentFeeBPS   int32
	Now             time.Time
	FlashExpiryCap  time.Duration // ORDER_FLASH_PAYMENT_EXPIRY_MINUTES (defensive cap when group has flash items)
	// CODAssessmentErr carries the result of assessCOD (E19) when target method is COD.
	CODAssessmentErr error
}

// RetryOrderFeeUpdate holds recalculated seller-borne fee fields for one sub-order (buyer TotalPayableVND unchanged).
type RetryOrderFeeUpdate struct {
	OrderID         string
	ShopID          string
	SellerRevenue   int64
	CategoryCommVND int64
	PaymentFeeBPS   int32
	PaymentFeeVND   int64
	ShippingFeeVND  int64
	TotalPayableVND int64
}

// RetrySwitchPlan is the deterministic, side-effect-free plan produced by PlanRetryPaymentSwitch (F5).
type RetrySwitchPlan struct {
	CheckoutGroupID string
	CurrentMethod   string
	TargetMethod    string
	SwitchedToCOD   bool
	TotalPayableVND int64
	ExpiresAt       *time.Time // Original checkout group deadline for online methods; nil when SwitchedToCOD.
	HasFlashSale    bool
	OrderFees       []RetryOrderFeeUpdate
	StoreOrderFees  []store.RetryOrderFeeSpec
	ShopAllocations []*paymentv1.ShopAllocation
}

// ResolveRetryTargetMethod resolves the canonical target payment method string for RetryPayment.
// An empty or UNSPECIFIED request preserves currentMethod.
func ResolveRetryTargetMethod(currentMethod, requestedMethod string) (string, error) {
	reqNorm := statemachine.NormalizePaymentMethod(requestedMethod)
	if reqNorm == "" || reqNorm == "UNSPECIFIED" {
		curNorm := statemachine.NormalizePaymentMethod(currentMethod)
		if !statemachine.IsValidPaymentMethod(curNorm) {
			return "", status.Errorf(codes.FailedPrecondition, "order group has unsupported payment method %q", currentMethod)
		}
		return curNorm, nil
	}
	if !statemachine.IsValidPaymentMethod(reqNorm) {
		return "", status.Errorf(codes.InvalidArgument, "unsupported target payment method %q", requestedMethod)
	}
	return reqNorm, nil
}

// PlanRetryPaymentSwitch is the pure core planner for F5 (UC-B25) payment retry and method switching:
//   - Verifies every sub-order is PENDING, online (VIETQR/ZALOPAY), matches ExpectedPayID, and is unexpired.
//   - Validates target method config (enabled, min/max bounds) from F6 PaymentMethodConfig.
//   - Forbids switching to COD on flash sale orders and enforces E19 assessCOD result.
//   - Preserves the original checkout group payment_expires_at for online↔online retries (never extends).
//   - Recalculates seller-borne payment_fee_vnd/payment_fee_bps for the new method while keeping
//     buyer TotalPayableVND strictly unchanged and C_cat + C_pay <= R.
func PlanRetryPaymentSwitch(in RetrySwitchInput) (*RetrySwitchPlan, error) {
	if len(in.Aggs) == 0 {
		return nil, status.Error(codes.NotFound, "no orders found for checkout group")
	}
	if in.PaymentFeeBPS < 0 || in.PaymentFeeBPS > 10000 {
		return nil, status.Errorf(codes.Internal, "PAYMENT_FEE_CONFIG_INVALID: invalid fee_bps %d", in.PaymentFeeBPS)
	}

	var (
		totalPayableVND int64
		origExp         *time.Time
		currentMethod   string
	)
	for i, agg := range in.Aggs {
		ord := agg.Order
		if ord.PaymentID != in.ExpectedPayID {
			return nil, status.Error(codes.FailedPrecondition, "payment_id does not match sub-order current payment")
		}
		if !statemachine.IsOnlineMethod(ord.PaymentMethod) {
			return nil, status.Error(codes.FailedPrecondition, "only online payment orders can retry payment")
		}
		if i == 0 {
			currentMethod = statemachine.NormalizePaymentMethod(ord.PaymentMethod)
		}
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			return nil, status.Errorf(codes.FailedPrecondition,
				"cannot retry payment when order %s is in status %s", ord.ID, ord.Status)
		}
		if strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") ||
			strings.EqualFold(ord.PaymentStatus, "CANCELLED") ||
			strings.EqualFold(ord.PaymentStatus, "EXPIRED") ||
			strings.EqualFold(ord.PaymentStatus, "REFUNDED") {
			return nil, status.Errorf(codes.FailedPrecondition,
				"cannot retry payment in payment status %s", ord.PaymentStatus)
		}
		exp := ord.PaymentExpiresAt
		if exp == nil {
			exp = in.Group.PaymentExpiresAt
		}
		if exp == nil || !exp.After(in.Now) {
			return nil, status.Error(codes.FailedPrecondition, "payment window has expired; please place a new order")
		}
		if origExp == nil || exp.Before(*origExp) {
			ts := *exp
			origExp = &ts
		}
		totalPayableVND += ord.TotalPayableVND
	}

	if in.Group.PaymentExpiresAt != nil && in.Group.PaymentExpiresAt.Before(*origExp) {
		ts := *in.Group.PaymentExpiresAt
		origExp = &ts
	}
	if !origExp.After(in.Now) {
		return nil, status.Error(codes.FailedPrecondition, "payment window has expired; please place a new order")
	}

	targetMethod, err := ResolveRetryTargetMethod(currentMethod, in.RequestedMethod)
	if err != nil {
		return nil, err
	}

	// F6/F7: Target method must be enabled and totalPayableVND within configured min/max bounds.
	if _, vErr := ValidatePaymentMethodConfig(targetMethod, in.MethodConfig, totalPayableVND); vErr != nil {
		return nil, vErr
	}

	hasFlashSale := groupHasFlashSale(in.Aggs)
	switchedToCOD := statemachine.IsCODMethod(targetMethod)

	var expiresAt *time.Time
	if switchedToCOD {
		if hasFlashSale {
			return nil, status.Error(
				codes.FailedPrecondition,
				"FLASH_SALE_COD_NOT_ALLOWED: đơn hàng có sản phẩm Flash Sale không hỗ trợ chuyển sang thanh toán khi nhận hàng (COD)",
			)
		}
		if in.CODAssessmentErr != nil {
			return nil, in.CODAssessmentErr
		}
	} else {
		// F5: Always keep the original checkout group expiry deadline (never extend past original window).
		keptExp := *origExp
		if hasFlashSale && in.FlashExpiryCap > 0 {
			flashCap := in.Now.Add(in.FlashExpiryCap)
			if flashCap.Before(keptExp) {
				keptExp = flashCap
			}
		}
		if !keptExp.After(in.Now) {
			return nil, status.Error(codes.FailedPrecondition, "payment window has expired; please place a new order")
		}
		expiresAt = &keptExp
	}

	// F5: Recalculate seller-borne payment fee for the new method; buyer totalPayableVND is strictly unchanged.
	orderFees := make([]RetryOrderFeeUpdate, 0, len(in.Aggs))
	storeFees := make([]store.RetryOrderFeeSpec, 0, len(in.Aggs))
	allocs := make([]*paymentv1.ShopAllocation, 0, len(in.Aggs))

	for _, agg := range in.Aggs {
		ord := agg.Order
		sellerRevenue := computeSellerRevenue(ord)
		newPayFeeVND := ComputePaymentFeeVND(sellerRevenue, ord.CommissionVND, ord.TotalPayableVND, in.PaymentFeeBPS)
		ship := maxInt64(0, ord.ShippingFeeVND)
		platformDiscount := maxInt64(0, sellerRevenue+ship-ord.TotalPayableVND)
		commTotal := minInt64(maxInt64(0, ord.CommissionVND)+newPayFeeVND, sellerRevenue)

		orderFees = append(orderFees, RetryOrderFeeUpdate{
			OrderID:         ord.ID,
			ShopID:          ord.ShopID,
			SellerRevenue:   sellerRevenue,
			CategoryCommVND: ord.CommissionVND,
			PaymentFeeBPS:   in.PaymentFeeBPS,
			PaymentFeeVND:   newPayFeeVND,
			ShippingFeeVND:  ship,
			TotalPayableVND: ord.TotalPayableVND,
		})
		storeFees = append(storeFees, store.RetryOrderFeeSpec{
			OrderID:       ord.ID,
			PaymentFeeVND: newPayFeeVND,
			PaymentFeeBPS: in.PaymentFeeBPS,
		})
		allocs = append(allocs, &paymentv1.ShopAllocation{
			OrderId:             ord.ID,
			ShopId:              ord.ShopID,
			SubtotalVnd:         sellerRevenue,
			CommissionVnd:       commTotal,
			ShippingFeeVnd:      ship,
			PlatformDiscountVnd: platformDiscount,
			SalesChannel:        effectiveSalesChannel(ord.SalesChannel),
			StorefrontShopId:    ord.StorefrontShopID,
		})
	}

	groupID := in.Group.ID
	if groupID == "" {
		groupID = in.Aggs[0].Order.CheckoutGroupID
	}

	return &RetrySwitchPlan{
		CheckoutGroupID: groupID,
		CurrentMethod:   currentMethod,
		TargetMethod:    targetMethod,
		SwitchedToCOD:   switchedToCOD,
		TotalPayableVND: totalPayableVND,
		ExpiresAt:       expiresAt,
		HasFlashSale:    hasFlashSale,
		OrderFees:       orderFees,
		StoreOrderFees:  storeFees,
		ShopAllocations: allocs,
	}, nil
}
