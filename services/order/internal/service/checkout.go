package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type resolvedLineItem struct {
	sku               *productv1.SKU
	quantity          int32
	weightGrams       int32
	commissionBPS     int32
	flashSaleItemID   string
	shopProgramItemID string
	effectivePrice    int64
}

type shopCheckoutPlan struct {
	shopID                  string
	shop                    *userv1.Shop
	items                   []resolvedLineItem
	subtotalVND             int64
	defaultWeightGrams      int32
	weightGrams             int32
	freeShipEnabled         bool
	freeShipMinOrderVND     int64
	freeShipMaxVND          int64
	carrierCode             string
	serviceCode             string
	shippingFeeVND          int64
	codSupported            bool
	flashSaleDiscountVND    int64
	shopProgramDiscountVND  int64
	shopVoucherDiscount     int64
	shopFreeshipDiscountVND int64
	platformVoucherShare    int64
	freeshipDiscountShare   int64
	coinsDiscountShare      int64
	commissionVND           int64
	paymentFeeVND           int64
	paymentFeeBPS           int32
	totalPayableVND         int64
}

// CreateCheckout orchestrates the idempotent multi-shop checkout saga.
func (s *OrderService) CreateCheckout(ctx context.Context, req *orderv1.CreateCheckoutRequest) (*orderv1.CreateCheckoutResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	idemKey := strings.TrimSpace(req.GetIdempotencyKey())
	if idemKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}

	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	buyerID := caller.UserID
	if !caller.IsFullAdmin() && req.GetBuyerId() != "" && req.GetBuyerId() != buyerID {
		return nil, status.Error(codes.PermissionDenied, "cannot checkout on behalf of another buyer")
	}
	if caller.IsFullAdmin() && strings.TrimSpace(req.GetBuyerId()) != "" {
		buyerID = strings.TrimSpace(req.GetBuyerId())
	}

	addressID := strings.TrimSpace(req.GetAddressId())
	if addressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id is required")
	}

	methodStr := statemachine.PaymentMethodToString(req.GetPaymentMethod())
	if !statemachine.IsValidPaymentMethod(methodStr) {
		return nil, status.Error(codes.InvalidArgument, "payment_method must be COD, VIETQR, or ZALOPAY")
	}
	if req.GetCoinsToUse() < 0 {
		return nil, status.Error(codes.InvalidArgument, "coins_to_use cannot be negative")
	}

	salesChannel, storefrontShopID, err := normalizeSalesChannel(req.GetSalesChannel(), req.GetStorefrontShopId())
	if err != nil {
		return nil, err
	}
	if salesChannel == SalesChannelStorefront {
		if req.GetCoinsToUse() > 0 {
			return nil, status.Error(codes.InvalidArgument, "STOREFRONT_COINS_NOT_ALLOWED: platform coins cannot be used on STOREFRONT orders")
		}
		if strings.TrimSpace(req.GetPlatformVoucherId()) != "" {
			return nil, status.Error(codes.InvalidArgument, "STOREFRONT_PLATFORM_VOUCHER_NOT_ALLOWED: platform vouchers cannot be used on STOREFRONT orders")
		}
		if strings.TrimSpace(req.GetFreeshipVoucherId()) != "" {
			return nil, status.Error(codes.InvalidArgument, "STOREFRONT_FREESHIP_VOUCHER_NOT_ALLOWED: platform freeship vouchers cannot be used on STOREFRONT orders")
		}
		for vShopID, vID := range req.GetShopVoucherIds() {
			if strings.TrimSpace(vID) != "" && strings.TrimSpace(vShopID) != storefrontShopID {
				return nil, status.Errorf(
					codes.InvalidArgument,
					"STOREFRONT_VOUCHER_SHOP_MISMATCH: voucher for shop %s cannot be used on storefront %s",
					strings.TrimSpace(vShopID),
					storefrontShopID,
				)
			}
		}
	}

	reqHash := computeCheckoutRequestHash(buyerID, req)

	// Fast path: check for already completed idempotent checkout without blocking.
	if replay, err := s.tryLoadIdempotentReplay(ctx, buyerID, idemKey, reqHash, caller); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	} else if replay != nil {
		s.clearOrderedCart(ctx, buyerID, req, storefrontShopID, s.orderedSKUIDsForReplay(ctx, replay))
		return replay, nil
	}

	// Serialize concurrent in-process duplicate requests for the same (buyer_id, idempotency_key).
	unlock := s.idemLocks.Lock(buyerID + ":" + idemKey)
	defer unlock()

	checkoutGroupID, replay, err := s.acquireCheckoutGroup(ctx, buyerID, idemKey, reqHash, methodStr, salesChannel, storefrontShopID, caller)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		s.clearOrderedCart(ctx, buyerID, req, storefrontShopID, s.orderedSKUIDsForReplay(ctx, replay))
		return replay, nil
	}

	// Track cleanup on failure before orders are persisted.
	ordersPersisted := false
	failGroupBeforePersist := func(reason string) {
		if ordersPersisted {
			return
		}
		cleanCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()
		if fErr := s.store.FailCheckoutGroup(cleanCtx, checkoutGroupID, reason); fErr != nil {
			s.logger.WarnContext(ctx, "failed to mark checkout group failed", "checkout_group_id", checkoutGroupID, "error", fErr)
		}
	}

	// Step A: Validate buyer & resolve shipping address.
	if s.clients.User == nil {
		failGroupBeforePersist("USER_VALIDATION_FAILED")
		return nil, status.Error(codes.Unavailable, "user service is unavailable")
	}
	valResp, err := s.clients.User.ValidateUser(ctx, &userv1.ValidateUserRequest{UserId: buyerID})
	if err != nil {
		failGroupBeforePersist("USER_VALIDATION_FAILED")
		return nil, wrapDownstreamError("validate buyer account", err)
	}
	if !valResp.GetValid() || valResp.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE {
		failGroupBeforePersist("USER_NOT_ACTIVE")
		return nil, status.Error(codes.PermissionDenied, "buyer account is not active")
	}

	addrResp, err := s.clients.User.GetAddress(ctx, &userv1.GetAddressRequest{
		UserId:    buyerID,
		AddressId: addressID,
	})
	if err != nil {
		failGroupBeforePersist("ADDRESS_LOOKUP_FAILED")
		return nil, wrapDownstreamError("load shipping address", err)
	}
	addr := addrResp.GetAddress()
	if addr == nil {
		failGroupBeforePersist("ADDRESS_NOT_FOUND")
		return nil, status.Error(codes.NotFound, "shipping address not found")
	}
	if addr.GetUserId() != "" && addr.GetUserId() != buyerID {
		failGroupBeforePersist("ADDRESS_OWNERSHIP_DENIED")
		return nil, status.Error(codes.PermissionDenied, "shipping address does not belong to buyer")
	}
	// E17: Public fee estimates may omit ward_code, but checkout strictly requires a destination ward.
	if strings.TrimSpace(addr.GetProvinceCode()) == "" || strings.TrimSpace(addr.GetWardCode()) == "" {
		failGroupBeforePersist("ADDRESS_WARD_REQUIRED")
		return nil, status.Error(codes.FailedPrecondition, "ADDRESS_WARD_REQUIRED: shipping address must specify both province_code and ward_code")
	}

	addrSnapshot := &orderv1.OrderAddressSnapshot{
		RecipientName:  addr.GetRecipientName(),
		RecipientPhone: addr.GetRecipientPhone(),
		ProvinceCode:   addr.GetProvinceCode(),
		ProvinceName:   addr.GetProvinceName(),
		WardCode:       addr.GetWardCode(),
		WardName:       addr.GetWardName(),
		StreetAddress:  addr.GetStreetAddress(),
	}

	var shopsByID map[string]*userv1.Shop
	// For STOREFRONT orders, verify storefront_shop_id is an ACTIVE shop via verifyShopsActive
	// (shared userShopBreaker, retry, and F12 409 SHOP_SUSPENDED / SHOP_NOT_ACTIVE) BEFORE resolving items or touching stock.
	if salesChannel == SalesChannelStorefront {
		var shopFailReason string
		shopsByID, shopFailReason, err = s.verifyShopsActive(ctx, []string{storefrontShopID})
		if err != nil {
			failGroupBeforePersist(shopFailReason)
			return nil, err
		}
	}

	// Step B: Resolve items from cart or explicit items[] and fetch authoritative SKU details.
	requestedItems, resolveFailReason, err := s.resolveCheckoutInputs(ctx, buyerID, req, salesChannel, storefrontShopID)
	if err != nil {
		failGroupBeforePersist(fallbackStr(resolveFailReason, "ITEMS_RESOLVE_FAILED"))
		return nil, err
	}

	skuIDs := make([]string, 0, len(requestedItems))
	for _, it := range requestedItems {
		skuIDs = append(skuIDs, it.GetSkuId())
	}
	sort.Strings(skuIDs)

	skuResp, err := s.clients.Product.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: skuIDs})
	if err != nil {
		failGroupBeforePersist("SKU_LOOKUP_FAILED")
		return nil, err
	}

	skuByID := make(map[string]*productv1.SKU, len(skuResp.GetSkus()))
	for _, sku := range skuResp.GetSkus() {
		skuByID[sku.GetId()] = sku
	}

	// For STOREFRONT orders, reject cross-shop items BEFORE shop/stock checks so 0 stock is touched.
	if salesChannel == SalesChannelStorefront {
		for _, reqItem := range requestedItems {
			if sku := skuByID[reqItem.GetSkuId()]; sku != nil && sku.GetShopId() != "" && sku.GetShopId() != storefrontShopID {
				failGroupBeforePersist("STOREFRONT_SHOP_MISMATCH")
				return nil, status.Errorf(
					codes.InvalidArgument,
					"STOREFRONT_SHOP_MISMATCH: all checkout items must belong to storefront shop %s",
					storefrontShopID,
				)
			}
		}
	}

	// F12 + Item G (#8): Synchronously verify every remaining unverified shop in this checkout is ACTIVE via parallel User.GetShop
	// BEFORE checking sku.IsActive (buildShopPlans) and before reserving stock/promotions, so a suspended shop
	// always returns SHOP_SUSPENDED even after product-service redacts its SKUs to IsActive=false.
	preShopIDs := make([]string, 0, len(requestedItems))
	for _, reqItem := range requestedItems {
		if sku := skuByID[reqItem.GetSkuId()]; sku != nil {
			if sid := strings.TrimSpace(sku.GetShopId()); sid != "" {
				if _, alreadyVerified := shopsByID[sid]; !alreadyVerified {
					preShopIDs = append(preShopIDs, sid)
				}
			}
		}
	}
	if len(preShopIDs) > 0 {
		extraShops, shopFailReason, vErr := s.verifyShopsActive(ctx, preShopIDs)
		if vErr != nil {
			failGroupBeforePersist(shopFailReason)
			return nil, vErr
		}
		if shopsByID == nil {
			shopsByID = extraShops
		} else {
			for k, v := range extraShops {
				shopsByID[k] = v
			}
		}
	}

	plans, allStockItems, flashSaleItems, err := buildShopPlans(requestedItems, skuByID)
	if err != nil {
		failGroupBeforePersist("INVALID_ITEMS")
		return nil, err
	}

	// Defense-in-depth: for STOREFRONT orders, EVERY plan must belong to storefront_shop_id.
	if salesChannel == SalesChannelStorefront {
		if len(plans) != 1 || plans[0].shopID != storefrontShopID {
			failGroupBeforePersist("STOREFRONT_SHOP_MISMATCH")
			return nil, status.Errorf(
				codes.InvalidArgument,
				"STOREFRONT_SHOP_MISMATCH: all checkout items must belong to storefront shop %s",
				storefrontShopID,
			)
		}
	}

	// F6: Read authoritative payment method configuration directly from PaymentService on every checkout
	// (no caching; fails closed on disabled method or unavailable config source before any reservation).
	methodCfg, cfgFailReason, err := s.loadPaymentMethodConfig(ctx, methodStr)
	if err != nil {
		failGroupBeforePersist(cfgFailReason)
		return nil, err
	}

	planShopIDs := make([]string, 0, len(plans))
	for _, p := range plans {
		p.shop = shopsByID[p.shopID]
		planShopIDs = append(planShopIDs, p.shopID)
	}

	checkNow := s.nowFn()

	// Wave 2b Flash Sale Gate: reject COD when any flash item is present and require every
	// requested flash sale slot to belong to a currently live (active) flash sale window.
	var (
		flashPriceByItemID  map[string]int64
		earliestFlashEndsAt *time.Time
	)
	if len(flashSaleItems) > 0 {
		if statemachine.IsCODMethod(methodStr) {
			failGroupBeforePersist("FLASH_SALE_COD_NOT_ALLOWED")
			return nil, status.Error(
				codes.FailedPrecondition,
				"FLASH_SALE_COD_NOT_ALLOWED: đơn hàng có sản phẩm Flash Sale không hỗ trợ thanh toán khi nhận hàng (COD), vui lòng chọn phương thức thanh toán trực tuyến",
			)
		}
		flashPriceByItemID, earliestFlashEndsAt, err = s.resolveActiveFlashSales(ctx, flashSaleItems, skuByID, checkNow)
		if err != nil {
			failGroupBeforePersist("FLASH_SALE_NOT_ACTIVE")
			return nil, err
		}
	}

	// Wave 2b Shop Discount Programs (UC-S18): server-authoritative lookup via ListActiveShopDiscounts.
	// Single item-price rule: flash sale items never stack with shop discount programs.
	shopProgramItems, shopProgramPrices, err := s.resolveActiveShopDiscounts(ctx, plans, checkNow)
	if err != nil {
		failGroupBeforePersist("SHOP_DISCOUNT_LOOKUP_FAILED")
		return nil, err
	}

	// Step C: Fetch shop shipping settings (default_weight_grams + shop freeship) and calculate shipping fee per shop.
	if err := s.calculateShippingFees(ctx, plans, req.GetShippingChoices(), addrSnapshot, methodStr); err != nil {
		failReason := "SHIPPING_FEE_FAILED"
		if strings.Contains(err.Error(), "SHOP_PICKUP_ADDRESS_REQUIRED") {
			failReason = "SHOP_PICKUP_ADDRESS_REQUIRED"
		}
		failGroupBeforePersist(failReason)
		return nil, err
	}

	// Wave 2b Payment Fee Config (UC-A09): fetch authoritative payment_fee_bps from PaymentService.
	// Fail checkout clearly on error; never guess a fallback fee rate.
	paymentFeeBPS, err := s.lookupPaymentFeeBPS(ctx, methodStr, checkNow)
	if err != nil {
		failGroupBeforePersist("PAYMENT_FEE_CONFIG_FAILED")
		return nil, err
	}

	// Step D: Early payment method bounds (F6) and COD risk checks (UC-PAY02 / E19) before any stock or promotion reservation.
	hasPromotions := len(req.GetShopVoucherIds()) > 0 ||
		strings.TrimSpace(req.GetPlatformVoucherId()) != "" ||
		strings.TrimSpace(req.GetFreeshipVoucherId()) != "" ||
		req.GetCoinsToUse() > 0 ||
		len(flashSaleItems) > 0 ||
		len(shopProgramItems) > 0

	hasShopFreeship := false
	var preTotal int64
	shopCODSupport := make(map[string]bool, len(plans))
	for _, p := range plans {
		if p.freeShipEnabled && p.freeShipMaxVND > 0 {
			hasShopFreeship = true
		}
		preTotal += p.subtotalVND + p.shippingFeeVND
		shopCODSupport[p.shopID] = p.codSupported
	}

	if statemachine.IsCODMethod(methodStr) {
		if failReason, codErr := s.assessCOD(ctx, buyerID, valResp, planShopIDs, shopCODSupport, preTotal, methodCfg, !hasPromotions && !hasShopFreeship); codErr != nil {
			failGroupBeforePersist(failReason)
			return nil, codErr
		}
	} else if !hasPromotions && !hasShopFreeship {
		if failReason, boundErr := ValidatePaymentMethodAmountBounds(methodStr, methodCfg, preTotal); boundErr != nil {
			failGroupBeforePersist(failReason)
			return nil, boundErr
		}
	}

	// Saga Step 1: Reserve Stock (atomic optimistic locking, idempotent by checkoutGroupID).
	if _, err := s.clients.Product.ReserveStock(ctx, &productv1.ReserveStockRequest{
		OrderId: checkoutGroupID,
		Items:   allStockItems,
	}); err != nil {
		if isAmbiguousReserveError(err) {
			// Server may have committed the stock reservation despite timeout/network error:
			// compensate stock only (ReservePromotion has not been called yet, N12).
			s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, false, "STOCK_RESERVE_FAILED")
		} else {
			failGroupBeforePersist("STOCK_RESERVE_FAILED")
		}
		return nil, err
	}

	// Saga Step 2: Reserve Promotions (vouchers, freeship, coins, flash sale slots, shop discount program limits).
	// D3: Always call Promotion.ReservePromotion with order-service's internal service token (no buyer JWT forwarded).
	promoReserved := false
	var breakdown *promotionv1.DiscountBreakdown
	var promoWarnings []string
	if hasPromotions {
		promoReq := buildReservePromotionRequest(checkoutGroupID, buyerID, req, plans, flashSaleItems, shopProgramItems, salesChannel, storefrontShopID)
		promoResp, pErr := s.clients.Promotion.ReservePromotion(clients.WithInternalAdminContext(ctx), promoReq)
		if pErr != nil {
			s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, isAmbiguousReserveError(pErr), "PROMOTION_RESERVE_FAILED")
			return nil, pErr
		}
		promoReserved = true
		breakdown = promoResp.GetBreakdown()
		if breakdown != nil && len(breakdown.GetWarnings()) > 0 {
			promoWarnings = append([]string(nil), breakdown.GetWarnings()...)
		}
	}

	if err := applyDiscountsAndTotals(plans, promoReserved, breakdown, flashPriceByItemID, shopProgramPrices, paymentFeeBPS); err != nil {
		s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, promoReserved, "DISCOUNT_VALIDATION_FAILED")
		return nil, err
	}

	var grandTotalPayableVND int64
	for _, p := range plans {
		grandTotalPayableVND += p.totalPayableVND
	}

	// Post-promotion authoritative F6 payment method min/max check (compensating stock+promo on rejection).
	if failReason, boundErr := ValidatePaymentMethodAmountBounds(methodStr, methodCfg, grandTotalPayableVND); boundErr != nil {
		s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, promoReserved, failReason)
		return nil, boundErr
	}

	// Saga Step 3: Persist Checkout Group Sub-Orders + Order Items + Timeline + Outbox in ONE TX.
	now := s.nowFn()
	var paymentExpiresAt *time.Time
	initialStatus := orderv1.OrderStatus_ORDER_STATUS_PENDING
	initialPaymentStatus := "PENDING"
	if statemachine.IsCODMethod(methodStr) || grandTotalPayableVND == 0 {
		initialStatus = orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
		if grandTotalPayableVND == 0 {
			initialPaymentStatus = "SUCCEEDED"
		}
	} else {
		exp := now.Add(time.Duration(methodCfg.ExpiryMinutes) * time.Minute)
		if len(flashSaleItems) > 0 {
			flashExp := now.Add(s.flashPaymentExpiryDuration())
			if flashExp.Before(exp) {
				exp = flashExp
			}
			if earliestFlashEndsAt != nil && earliestFlashEndsAt.Before(exp) {
				exp = *earliestFlashEndsAt
			}
			if !exp.After(now) {
				s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, promoReserved, "FLASH_SALE_NOT_ACTIVE")
				return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_NOT_ACTIVE: khung giờ Flash Sale đã kết thúc")
			}
		}
		paymentExpiresAt = &exp
	}

	addrJSON, err := json.Marshal(addrSnapshot)
	if err != nil {
		s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, promoReserved, "ADDRESS_MARSHAL_FAILED")
		return nil, status.Errorf(codes.Internal, "marshal shipping address: %v", err)
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	batch, err := buildCheckoutBatch(
		checkoutGroupID,
		buyerID,
		methodStr,
		initialStatus,
		initialPaymentStatus,
		paymentExpiresAt,
		addrJSON,
		plans,
		salesChannel,
		storefrontShopID,
		now,
		traceID,
		traceparent,
	)
	if err != nil {
		s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, promoReserved, "BATCH_BUILD_FAILED")
		return nil, status.Errorf(codes.Internal, "prepare checkout batch: %v", err)
	}

	if len(promoWarnings) > 0 {
		if wBytes, wErr := json.Marshal(promoWarnings); wErr == nil {
			batch.WarningsJSON = wBytes
		}
	}

	// P0 N1: Run PersistCheckoutBatch with a detached timeout context so client disconnects cannot
	// race commit vs compensation. On error, acquire FOR UPDATE on checkout_groups to inspect committed orders:
	//   - orders exist -> commit succeeded; continue forward!
	//   - no orders exist -> confirmed rollback; compensate stock+promo and mark FAILED.
	//   - inspect failed -> state uncertain; DO NOT compensate or mark FAILED; return Unavailable for watchdog.
	persistCtx, persistCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer persistCancel()
	persistedAggs, err := s.store.PersistCheckoutBatch(persistCtx, batch)
	if err != nil {
		inspectCtx, inspectCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer inspectCancel()
		_, existingAggs, inspectErr := s.store.InspectCheckoutGroupOrdersLocked(inspectCtx, checkoutGroupID)
		if inspectErr != nil {
			s.logger.ErrorContext(ctx, "PersistCheckoutBatch failed and locked inspection also failed; leaving checkout group for watchdog",
				"checkout_group_id", checkoutGroupID, "persist_error", err, "inspect_error", inspectErr)
			return nil, status.Errorf(codes.Unavailable, "checkout persistence state uncertain; please retry with same idempotency_key: %v", err)
		}
		if len(existingAggs) > 0 {
			s.logger.WarnContext(ctx, "PersistCheckoutBatch returned error after commit succeeded; continuing forward with persisted orders",
				"checkout_group_id", checkoutGroupID, "order_count", len(existingAggs), "error", err)
			persistedAggs = existingAggs
		} else {
			s.compensateBeforePersistAndFail(ctx, checkoutGroupID, true, promoReserved, "DB_PERSIST_FAILED")
			if errors.Is(err, store.ErrCheckoutGroupNotInProgress) {
				return nil, status.Errorf(codes.FailedPrecondition, "persist checkout orders: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "persist checkout orders: %v", err)
		}
	}
	ordersPersisted = true

	// Saga Step 4: Create Payment via PaymentService (always called, including 0 VND orders per PRM-B1).
	var paymentID, paymentStatus, qrURL, qrCode, payURL string
	paymentStatus = initialPaymentStatus
	payReq, payReqErr := buildCreatePaymentRequest(
		checkoutGroupID,
		buyerID,
		grandTotalPayableVND,
		methodStr,
		paymentExpiresAt,
		persistedAggs,
		salesChannel,
		storefrontShopID,
	)
	var (
		payResp *paymentv1.CreatePaymentResponse
		payErr  error
	)
	if payReqErr != nil {
		payErr = payReqErr
	} else {
		// P2 M6: Run CreatePayment with detachedCallerContext (keeps buyer JWT metadata while insulating
		// payment initialization from client disconnect after orders have already been persisted).
		payCtx, payCancel := detachedCallerContext(ctx, s.cfg.GRPCCallTimeout)
		defer payCancel()
		payResp, payErr = s.clients.Payment.CreatePayment(payCtx, payReq)
	}
	wasAmbiguousPayErr := payErr != nil && isAmbiguousReserveError(payErr)
	if wasAmbiguousPayErr {
		// P0 N2: Ambiguous CreatePayment failure (timeout/disconnect/Unavailable) — payment may have committed!
		// Probe GetPayment(OrderGroupId) with detached ctx + retry before ever cancelling.
		probeCtx, probeCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer probeCancel()
		var probedPay *paymentv1.Payment
		probeErr := clients.RetryIdempotent(probeCtx, 3, func(c context.Context) error {
			gResp, gErr := s.clients.Payment.GetPayment(c, &paymentv1.GetPaymentRequest{
				OrderGroupId: checkoutGroupID,
			})
			if gErr != nil {
				if status.Code(gErr) == codes.NotFound {
					probedPay = nil
					return nil
				}
				return gErr
			}
			probedPay = gResp.GetPayment()
			return nil
		})
		if probeErr != nil {
			// Probe could not confirm absence -> NEVER cancel orders or release stock/promo!
			s.logger.ErrorContext(ctx, "CreatePayment ambiguous error and GetPayment probe failed; leaving checkout for watchdog recovery",
				"checkout_group_id", checkoutGroupID, "create_error", payErr, "probe_error", probeErr)
			return nil, status.Errorf(codes.Unavailable, "payment initialization status uncertain; please retry with same idempotency_key: %v", payErr)
		}
		if probedPay != nil && strings.TrimSpace(probedPay.GetPaymentId()) != "" {
			s.logger.WarnContext(ctx, "CreatePayment returned ambiguous error but payment already exists; continuing forward",
				"checkout_group_id", checkoutGroupID, "payment_id", probedPay.GetPaymentId(), "create_error", payErr)
			payResp = &paymentv1.CreatePaymentResponse{Payment: probedPay}
			payErr = nil
			if initialStatus == orderv1.OrderStatus_ORDER_STATUS_PENDING &&
				probedPay.GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
				// P2 M5: If reconciling the observed terminal/settled payment status fails, return Unavailable
				// immediately and NEVER call CompleteCheckoutWithPayment (prevents PENDING order + SUCCEEDED/CANCELLED payment_status).
				if rErr := s.reconcileObservedPaymentStatus(probeCtx, checkoutGroupID, probedPay); rErr != nil {
					s.logger.ErrorContext(ctx, "reconcile observed payment status after CreatePayment recovery failed; leaving group IN_PROGRESS",
						"checkout_group_id", checkoutGroupID, "payment_id", probedPay.GetPaymentId(), "error", rErr)
					return nil, status.Errorf(codes.Unavailable, "payment status sync incomplete; please retry with same idempotency_key: %v", rErr)
				}
			}
		} else if initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			// P2 M6: Order is already CONFIRMED (COD or 0-VND). On ambiguous CreatePayment error + probe NotFound,
			// do NOT cancel — return Unavailable so watchdog ScanIncompleteCheckoutGroups forward-recovers via
			// idempotent CreatePayment("checkout:"+groupID).
			s.logger.WarnContext(ctx, "CreatePayment ambiguous error and probe NotFound on CONFIRMED (COD/0-VND) order; deferring to watchdog forward recovery",
				"checkout_group_id", checkoutGroupID, "create_error", payErr)
			return nil, status.Errorf(codes.Unavailable, "payment initialization pending recovery; please retry with same idempotency_key: %v", payErr)
		}
	}
	if payErr != nil {
		// Definite CreatePayment rejection OR ambiguous error where probe confirmed NotFound:
		// Cancel persisted orders FIRST; if CancelCheckoutGroupOrders fails or live orders remain,
		// NEVER release group stock/promotions.
		compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()
		cancelRes, cancelErr := s.store.CancelCheckoutGroupOrders(compCtx, store.CancelGroupOrdersParams{
			CheckoutGroupID: checkoutGroupID,
			Reason:          "Payment initialization failed",
			ActorID:         "order-service",
			ActorRole:       "system",
			PaymentStatus:   "FAILED",
			TraceID:         traceID,
			Traceparent:     traceparent,
		})
		if cancelErr != nil {
			s.logger.ErrorContext(ctx, "failed to cancel sub-orders during payment failure compensation; skipping resource release for watchdog",
				"checkout_group_id", checkoutGroupID, "error", cancelErr)
			return nil, payErr
		}
		if cancelRes.HasLiveOrders {
			s.logger.WarnContext(ctx, "CreatePayment failure compensation skipped group-wide release because live orders remain",
				"checkout_group_id", checkoutGroupID, "cancelled_count", len(cancelRes.Cancelled))
			s.notifyOutbox()
			return nil, payErr
		}
		s.compensateCancelledCheckoutGroup(compCtx, checkoutGroupID, cancelRes.Cancelled, persistedAggs, promoReserved)
		s.notifyOutbox()
		return nil, payErr
	}
	if pay := payResp.GetPayment(); pay != nil {
		paymentID = pay.GetPaymentId()
		paymentStatus = paymentStatusToString(pay.GetStatus(), initialPaymentStatus)
		qrURL = pay.GetQrUrl()
		qrCode = pay.GetQrCode()
		payURL = pay.GetPayUrl()
		if pay.GetExpiresAt() != nil {
			exp := pay.GetExpiresAt().AsTime().UTC()
			// Never allow PaymentService response to extend our computed expiry window (F6 / flash sale).
			if paymentExpiresAt == nil || exp.Before(*paymentExpiresAt) {
				paymentExpiresAt = &exp
			}
		}
	}

	// For COD (or zero-payable), stock is immediately confirmed because sub-orders start CONFIRMED.
	// pending_stock_confirm was set atomically in PersistCheckoutBatch; cleared only when ConfirmStock succeeds.
	// Runs with detachedCompensationContext so client disconnect after CreatePayment cannot abort it.
	if initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		if err := s.confirmGroupStockAndClear(ctx, checkoutGroupID); err != nil {
			s.logger.WarnContext(ctx, "ConfirmStock after checkout failed; watchdog will retry",
				"checkout_group_id", checkoutGroupID, "error", err)
		}
	}

	// Persist checkout_groups finalization + response_snapshot_json atomically under lock order
	// (orders -> checkout_groups), using detached context + retry so client cancel or transient DB
	// hiccups cannot leave the group stuck IN_PROGRESS. BuildSnapshot constructs the snapshot from
	// the real locked post-update state so racing SUCCEEDED/CANCELLED transitions are never downgraded.
	var (
		finalGroup    db.CheckoutGroup
		finalAggs     []store.OrderAggregate
		builtSnapshot bool
	)
	resetFinalState := func() {
		finalGroup = db.CheckoutGroup{}
		finalAggs = nil
		builtSnapshot = false
	}
	if err := s.completeCheckoutGroupWithRetry(ctx, store.UpdateCheckoutPaymentParams{
		CheckoutGroupID:  checkoutGroupID,
		PaymentID:        paymentID,
		PaymentStatus:    paymentStatus,
		QRURL:            qrURL,
		QRCode:           qrCode,
		PayURL:           payURL,
		PaymentExpiresAt: paymentExpiresAt,
		BuildSnapshot:    s.makeBuyerCheckoutSnapshotBuilder(promoWarnings, &finalGroup, &finalAggs, &builtSnapshot),
	}, resetFinalState); err != nil {
		s.logger.ErrorContext(ctx, "failed to finalize checkout group payment info after retries; watchdog/retry will recover",
			"checkout_group_id", checkoutGroupID, "error", err)
		if errors.Is(err, store.ErrNotFound) {
			return nil, mapStoreError(err)
		}
		return nil, status.Error(codes.Unavailable, "checkout finalization temporarily unavailable; please retry with the same idempotency_key")
	}

	if !builtSnapshot {
		return nil, status.Error(codes.Unavailable, "checkout finalization incomplete; please retry with the same idempotency_key")
	}

	// Wave 3a: If the checkout group already transitioned to FAILED concurrently before completion,
	// NEVER return 200 OK with CANCELLED orders / empty payment_id or clear the cart.
	if finalGroup.Status == "FAILED" {
		s.notifyOutbox()
		return nil, checkoutFailedError(finalGroup.PaymentStatus)
	}

	effectiveWarnings := promoWarnings
	if len(effectiveWarnings) == 0 {
		effectiveWarnings = parseWarningsJSON(finalGroup.WarningsJSON)
	}
	resp := buildCheckoutResponseFromState(finalGroup, finalAggs, effectiveWarnings, caller)

	// Saga Step 5 (P1-3): Best-effort ClearCart for ordered SKUs ONLY when checking out from cart
	// (never on Buy Now / from_cart=false or FAILED), with RetryIdempotent(3) on a detached context.
	s.clearOrderedCart(ctx, buyerID, req, storefrontShopID, orderedSKUIDsFromAggs(finalAggs))

	s.notifyOutbox()
	return resp, nil
}

const idempotencyConflictMsg = "IDEMPOTENCY_KEY_CONFLICT: idempotency_key was already used with a different checkout payload"

// computeCheckoutRequestHash produces a deterministic SHA-256 hex digest of the canonicalized checkout payload.
func computeCheckoutRequestHash(buyerID string, req *orderv1.CreateCheckoutRequest) string {
	type canonicalItem struct {
		SKUID           string `json:"sku_id"`
		Quantity        int32  `json:"quantity"`
		FlashSaleItemID string `json:"flash_sale_item_id"`
	}
	type canonicalShippingChoice struct {
		ShopID      string `json:"shop_id"`
		CarrierCode string `json:"carrier_code"`
		ServiceCode string `json:"service_code"`
	}
	type canonicalPayload struct {
		BuyerID           string                    `json:"buyer_id"`
		AddressID         string                    `json:"address_id"`
		FromCart          bool                      `json:"from_cart"`
		Items             []canonicalItem           `json:"items"`
		ShopVouchers      map[string]string         `json:"shop_vouchers"`
		PlatformVoucherID string                    `json:"platform_voucher_id"`
		FreeshipVoucherID string                    `json:"freeship_voucher_id"`
		CoinsToUse        int64                     `json:"coins_to_use"`
		PaymentMethod     string                    `json:"payment_method"`
		SalesChannel      string                    `json:"sales_channel"`
		StorefrontShopID  string                    `json:"storefront_shop_id"`
		ShippingChoices   []canonicalShippingChoice `json:"shipping_choices"`
	}

	items := make([]canonicalItem, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		if it == nil {
			continue
		}
		items = append(items, canonicalItem{
			SKUID:           strings.TrimSpace(it.GetSkuId()),
			Quantity:        it.GetQuantity(),
			FlashSaleItemID: strings.TrimSpace(it.GetFlashSaleItemId()),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SKUID != items[j].SKUID {
			return items[i].SKUID < items[j].SKUID
		}
		if items[i].FlashSaleItemID != items[j].FlashSaleItemID {
			return items[i].FlashSaleItemID < items[j].FlashSaleItemID
		}
		return items[i].Quantity < items[j].Quantity
	})

	shopVouchers := make(map[string]string, len(req.GetShopVoucherIds()))
	for k, v := range req.GetShopVoucherIds() {
		shopID := strings.TrimSpace(k)
		voucherID := strings.TrimSpace(v)
		if shopID == "" && voucherID == "" {
			continue
		}
		shopVouchers[shopID] = voucherID
	}

	shippingChoices := make([]canonicalShippingChoice, 0, len(req.GetShippingChoices()))
	for _, sc := range req.GetShippingChoices() {
		if sc == nil {
			continue
		}
		shippingChoices = append(shippingChoices, canonicalShippingChoice{
			ShopID:      strings.TrimSpace(sc.GetShopId()),
			CarrierCode: strings.ToLower(strings.TrimSpace(sc.GetCarrierCode())),
			ServiceCode: strings.ToLower(strings.TrimSpace(sc.GetServiceCode())),
		})
	}
	sort.Slice(shippingChoices, func(i, j int) bool {
		if shippingChoices[i].ShopID != shippingChoices[j].ShopID {
			return shippingChoices[i].ShopID < shippingChoices[j].ShopID
		}
		if shippingChoices[i].CarrierCode != shippingChoices[j].CarrierCode {
			return shippingChoices[i].CarrierCode < shippingChoices[j].CarrierCode
		}
		return shippingChoices[i].ServiceCode < shippingChoices[j].ServiceCode
	})

	sc, sfShopID, _ := normalizeSalesChannel(req.GetSalesChannel(), req.GetStorefrontShopId())
	raw, _ := json.Marshal(canonicalPayload{
		BuyerID:           strings.TrimSpace(buyerID),
		AddressID:         strings.TrimSpace(req.GetAddressId()),
		FromCart:          req.GetFromCart(),
		Items:             items,
		ShopVouchers:      shopVouchers,
		PlatformVoucherID: strings.TrimSpace(req.GetPlatformVoucherId()),
		FreeshipVoucherID: strings.TrimSpace(req.GetFreeshipVoucherId()),
		CoinsToUse:        req.GetCoinsToUse(),
		PaymentMethod:     statemachine.PaymentMethodToString(req.GetPaymentMethod()),
		SalesChannel:      sc,
		StorefrontShopID:  sfShopID,
		ShippingChoices:   shippingChoices,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func checkoutFailedError(reason string) error {
	code := strings.ToUpper(strings.TrimSpace(reason))
	if code == "" {
		code = "FAILED"
	}
	return status.Errorf(
		codes.FailedPrecondition,
		"CHECKOUT_FAILED (%s): checkout attempt failed with reason %s; please retry with a new idempotency_key",
		code, code,
	)
}

func (s *OrderService) tryLoadIdempotentReplay(ctx context.Context, buyerID, idemKey, reqHash string, caller callerIdentity) (*orderv1.CreateCheckoutResponse, error) {
	lookup, err := s.store.GetCheckoutGroupForIdempotency(ctx, buyerID, idemKey)
	if err != nil {
		return nil, err
	}
	group := &lookup.Group
	if !lookup.HasOrders && group.NeedsCleanup {
		// P1-2 / D11: Pre-persist failure awaiting cleanup (IN_PROGRESS or FAILED, 0 orders)
		// must bypass RequestHash check so acquireCheckoutGroup cleans up and retries (or returns Unavailable).
		return nil, nil
	}
	if group.Status == "FAILED" {
		if lookup.HasOrders {
			return nil, checkoutFailedError(group.PaymentStatus)
		}
		// D11: Pre-persist FAILED group (0 orders) may be retried as a fresh checkout.
		return nil, nil
	}
	if group.RequestHash != "" && group.RequestHash != reqHash {
		return nil, status.Error(codes.FailedPrecondition, idempotencyConflictMsg)
	}
	if group.Status != "COMPLETED" {
		return nil, nil
	}
	return s.buildReplayResponse(ctx, group, caller)
}

const (
	inProgressRecoveryGrace       = 2 * time.Second
	checkoutAcquireMaxWait        = 15 * time.Second
	checkoutAcquireInitialBackoff = 50 * time.Millisecond
	checkoutAcquireMaxBackoff     = 1 * time.Second
)

func (s *OrderService) checkoutAcquireTimings() (maxWait, initialBackoff, maxBackoff time.Duration) {
	maxWait = s.acquireMaxWait
	if maxWait <= 0 {
		maxWait = checkoutAcquireMaxWait
	}
	initialBackoff = s.acquireInitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = checkoutAcquireInitialBackoff
	}
	maxBackoff = s.acquireMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = checkoutAcquireMaxBackoff
	}
	if initialBackoff > maxBackoff {
		initialBackoff = maxBackoff
	}
	return maxWait, initialBackoff, maxBackoff
}

func waitCheckoutAcquireBackoff(ctx context.Context, wait time.Duration, deadline time.Time) (waited bool, ctxErr error) {
	rem := time.Until(deadline)
	if rem <= 0 {
		return false, nil
	}
	if wait > rem {
		wait = rem
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, status.FromContextError(ctx.Err()).Err()
	case <-timer.C:
		return true, nil
	}
}

func (s *OrderService) acquireCheckoutGroup(ctx context.Context, buyerID, idemKey, reqHash, methodStr, salesChannel, storefrontShopID string, caller callerIdentity) (string, *orderv1.CreateCheckoutResponse, error) {
	maxWait, backoff, maxBackoff := s.checkoutAcquireTimings()
	deadline := time.Now().Add(maxWait)

	for {
		if ctx.Err() != nil {
			return "", nil, status.FromContextError(ctx.Err()).Err()
		}
		lookup, err := s.store.GetCheckoutGroupForIdempotency(ctx, buyerID, idemKey)
		if err == nil && lookup != nil {
			existing := &lookup.Group
			// P1-2 / D11 + N9: Handle 0-order needs_cleanup BEFORE RequestHash check regardless of
			// whether status is IN_PROGRESS (from MarkCheckoutGroupNeedsCleanup) or FAILED (from watchdog).
			if !lookup.HasOrders && existing.NeedsCleanup {
				if cErr := s.cleanupStaleCheckoutGroup(ctx, existing.ID); cErr != nil {
					return "", nil, status.Errorf(codes.Unavailable, "previous checkout cleanup still pending; please retry shortly: %v", cErr)
				}
				continue
			}
			if existing.Status == "FAILED" {
				if lookup.HasOrders {
					return "", nil, checkoutFailedError(existing.PaymentStatus)
				}
				// D11: Clean FAILED group with 0 persisted orders -> fall through to create a fresh group.
			} else {
				if existing.RequestHash != "" && existing.RequestHash != reqHash {
					return "", nil, status.Error(codes.FailedPrecondition, idempotencyConflictMsg)
				}
				if existing.Status == "COMPLETED" {
					replay, rErr := s.buildReplayResponse(ctx, existing, caller)
					return "", replay, rErr
				}
				// P2 N7: Only trigger self-recovery after inProgressRecoveryGrace (2s) so a healthy sibling pod
				// finishing CreatePayment -> CompleteCheckoutWithPayment is not prematurely raced.
				if s.nowFn().Sub(existing.UpdatedAt) >= inProgressRecoveryGrace {
					if recovered, _, recErr := s.tryFinalizeInProgressGroupFromPayment(ctx, existing.ID); recErr == nil && recovered {
						continue
					}
				}
				// Another pod is currently executing this idempotency key; wait with progressive backoff up to maxWait (15s).
				waited, wErr := waitCheckoutAcquireBackoff(ctx, backoff, deadline)
				if wErr != nil {
					return "", nil, wErr
				}
				if !waited {
					return "", nil, status.Error(codes.Aborted, "concurrent checkout with same idempotency_key is still in progress")
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", nil, mapStoreError(err)
		}

		groupID := uuid.NewString()
		_, err = s.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID:               groupID,
			BuyerID:          buyerID,
			IdempotencyKey:   idemKey,
			RequestHash:      reqHash,
			Status:           "IN_PROGRESS",
			PaymentMethod:    statemachine.NormalizePaymentMethod(methodStr),
			TotalPayableVND:  0,
			SalesChannel:     salesChannel,
			StorefrontShopID: storefrontShopID,
		})
		if err == nil {
			return groupID, nil, nil
		}
		var failedWithOrders *store.FailedCheckoutWithOrdersError
		if errors.As(err, &failedWithOrders) {
			return "", nil, checkoutFailedError(failedWithOrders.Reason)
		}
		if errors.Is(err, store.ErrDuplicateIdempotencyKey) {
			waited, wErr := waitCheckoutAcquireBackoff(ctx, backoff, deadline)
			if wErr != nil {
				return "", nil, wErr
			}
			if !waited {
				return "", nil, status.Error(codes.Aborted, "concurrent checkout conflict")
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		return "", nil, mapStoreError(err)
	}
}

func (s *OrderService) buildReplayResponse(ctx context.Context, group *db.CheckoutGroup, caller callerIdentity) (*orderv1.CreateCheckoutResponse, error) {
	aggs, err := s.store.ListOrdersByCheckoutGroup(ctx, group.ID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	groupChannel := effectiveSalesChannel(group.SalesChannel)
	savedWarnings := parseWarningsJSON(group.WarningsJSON)
	if len(group.ResponseSnapshotJSON) > 2 {
		var resp orderv1.CreateCheckoutResponse
		if err := protojson.Unmarshal(group.ResponseSnapshotJSON, &resp); err == nil && resp.CheckoutGroupId != "" {
			resp.IdempotentReplay = true
			if group.PaymentStatus != "" {
				resp.PaymentStatus = group.PaymentStatus
			}
			if resp.PayUrl == "" && group.PayURL != "" {
				resp.PayUrl = group.PayURL
			}
			if len(resp.Warnings) == 0 && len(savedWarnings) > 0 {
				resp.Warnings = savedWarnings
			}
			if resp.SalesChannel == "" {
				resp.SalesChannel = groupChannel
			}
			if resp.StorefrontShopId == "" {
				resp.StorefrontShopId = group.StorefrontShopID
			}
			orders := make([]*orderv1.Order, 0, len(aggs))
			for _, agg := range aggs {
				orders = append(orders, aggregateToProtoForCaller(agg, caller))
			}
			resp.Orders = orders
			return &resp, nil
		}
	}

	resp := buildCheckoutResponseFromState(*group, aggs, savedWarnings, caller)
	resp.IdempotentReplay = true
	return resp, nil
}

func parseWarningsJSON(raw []byte) []string {
	if len(raw) <= 2 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func (s *OrderService) clearOrderedCart(ctx context.Context, buyerID string, req *orderv1.CreateCheckoutRequest, storefrontShopID string, skuIDs []string) {
	if !req.GetFromCart() || s.clients.Cart == nil || len(skuIDs) == 0 {
		return
	}
	cartCtx, cartCancel := detachedCallerContext(ctx, s.cfg.GRPCCallTimeout)
	defer cartCancel()
	if err := clients.RetryIdempotent(cartCtx, 3, func(c context.Context) error {
		_, cErr := s.clients.Cart.ClearItems(c, &cartv1.ClearItemsRequest{
			UserId: buyerID,
			SkuIds: skuIDs,
			ShopId: storefrontShopID,
		})
		return cErr
	}); err != nil {
		s.logger.WarnContext(ctx, "best-effort ClearItems failed after checkout", "buyer_id", buyerID, "error", err)
	}
}

func orderedSKUIDsFromAggs(aggs []store.OrderAggregate) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, agg := range aggs {
		for _, it := range agg.Items {
			if id := strings.TrimSpace(it.SKUID); id != "" {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					out = append(out, id)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *OrderService) orderedSKUIDsForReplay(ctx context.Context, resp *orderv1.CreateCheckoutResponse) []string {
	if resp == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, ord := range resp.GetOrders() {
		for _, it := range ord.GetItems() {
			if id := strings.TrimSpace(it.GetSkuId()); id != "" {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					out = append(out, id)
				}
			}
		}
	}
	if len(out) > 0 {
		sort.Strings(out)
		return out
	}
	if gid := strings.TrimSpace(resp.GetCheckoutGroupId()); gid != "" {
		if aggs, err := s.store.ListOrdersByCheckoutGroup(ctx, gid); err == nil {
			return orderedSKUIDsFromAggs(aggs)
		}
	}
	return nil
}

// compensateCancelledCheckoutGroup runs full post-cancel cleanup after CancelCheckoutGroupOrders succeeds
// with zero remaining live orders: cancels any latent payment by OrderGroupId, releases promotion,
// releases stock (falling back to RestockOrderItems if ConfirmStock already committed), and clears
// pending_cancel_compensation ONLY when every step succeeds (P0 N2 & N3).
func (s *OrderService) compensateCancelledCheckoutGroup(
	compCtx context.Context,
	groupID string,
	cancelledOrders []db.Order,
	allAggs []store.OrderAggregate,
	releasePromo bool,
) {
	itemsByOrderID := make(map[string][]db.OrderItem, len(allAggs))
	for _, agg := range allAggs {
		itemsByOrderID[agg.Order.ID] = agg.Items
	}

	var compFailed bool
	if s.clients.Payment != nil {
		if err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			_, cErr := s.clients.Payment.CancelPayment(c, &paymentv1.CancelPaymentRequest{
				OrderGroupId: groupID,
				Reason:       "Checkout group orders cancelled",
			})
			if code := status.Code(cErr); code == codes.NotFound || code == codes.AlreadyExists || code == codes.FailedPrecondition {
				return nil
			}
			return cErr
		}); err != nil {
			s.logger.ErrorContext(compCtx, "saga compensation CancelPayment(OrderGroupId) failed",
				"checkout_group_id", groupID, "error", err)
			compFailed = true
		}
	}
	if releasePromo {
		if err := s.releasePromotionErr(compCtx, groupID); err != nil {
			s.logger.ErrorContext(compCtx, "saga compensation ReleasePromotion failed",
				"checkout_group_id", groupID, "error", err)
			compFailed = true
		}
	}
	if s.clients.Product != nil {
		released, rErr := s.releaseStockErr(compCtx, groupID)
		if rErr != nil {
			s.logger.ErrorContext(compCtx, "saga compensation ReleaseStock failed",
				"checkout_group_id", groupID, "error", rErr)
			compFailed = true
		} else if !released {
			// Stock reservation was already confirmed (e.g. COD/0-VND ConfirmStock ran): restock each cancelled order.
			for _, co := range cancelledOrders {
				items := itemsByOrderID[co.ID]
				if len(items) == 0 {
					loaded, gErr := s.store.GetOrderWithDetails(compCtx, co.ID)
					if gErr != nil {
						s.logger.ErrorContext(compCtx, "saga compensation GetOrderWithDetails failed before restock; keeping pending_cancel_compensation",
							"checkout_group_id", groupID, "order_id", co.ID, "error", gErr)
						compFailed = true
						continue
					}
					items = loaded.Items
				}
				if err := s.restockOrderItemsWithConfirmErr(compCtx, groupID, co.ID, items); err != nil {
					s.logger.ErrorContext(compCtx, "saga compensation RestockOrderItems failed",
						"checkout_group_id", groupID, "order_id", co.ID, "error", err)
					compFailed = true
				}
			}
		}
	}
	if !compFailed {
		for _, co := range cancelledOrders {
			_ = s.store.ClearOrderPendingCancelCompensation(compCtx, co.ID)
		}
	}
}

func buildCheckoutResponseFromState(
	grp db.CheckoutGroup,
	aggs []store.OrderAggregate,
	warnings []string,
	caller callerIdentity,
) *orderv1.CreateCheckoutResponse {
	orders := make([]*orderv1.Order, 0, len(aggs))
	for _, agg := range aggs {
		orders = append(orders, aggregateToProtoForCaller(agg, caller))
	}
	resp := &orderv1.CreateCheckoutResponse{
		CheckoutGroupId:  grp.ID,
		Orders:           orders,
		PaymentId:        grp.PaymentID,
		PaymentMethod:    statemachine.StringToPaymentMethod(grp.PaymentMethod),
		PaymentStatus:    grp.PaymentStatus,
		QrUrl:            grp.QRURL,
		QrCode:           grp.QRCode,
		PayUrl:           grp.PayURL,
		TotalPayableVnd:  grp.TotalPayableVND,
		IdempotentReplay: false,
		Warnings:         append([]string(nil), warnings...),
		SalesChannel:     effectiveSalesChannel(grp.SalesChannel),
		StorefrontShopId: grp.StorefrontShopID,
	}
	if grp.PaymentExpiresAt != nil {
		resp.PaymentExpiresAt = timestamppb.New(*grp.PaymentExpiresAt)
	}
	return resp
}

func (s *OrderService) makeBuyerCheckoutSnapshotBuilder(
	warnings []string,
	outGroup *db.CheckoutGroup,
	outAggs *[]store.OrderAggregate,
	outBuilt *bool,
) func(grp db.CheckoutGroup, aggs []store.OrderAggregate) ([]byte, error) {
	return func(grp db.CheckoutGroup, aggs []store.OrderAggregate) ([]byte, error) {
		if outGroup != nil {
			*outGroup = grp
		}
		if outAggs != nil {
			*outAggs = aggs
		}
		if outBuilt != nil {
			*outBuilt = true
		}
		effWarnings := warnings
		if len(effWarnings) == 0 {
			effWarnings = parseWarningsJSON(grp.WarningsJSON)
		}
		buyerResp := buildCheckoutResponseFromState(grp, aggs, effWarnings, callerIdentity{UserID: grp.BuyerID, Role: "buyer"})
		raw, err := protojson.Marshal(buyerResp)
		if err != nil {
			s.logger.Warn("failed to marshal checkout response snapshot", "checkout_group_id", grp.ID, "error", err)
			return []byte("{}"), nil
		}
		return raw, nil
	}
}

func (s *OrderService) completeCheckoutGroupWithRetry(ctx context.Context, params store.UpdateCheckoutPaymentParams, reset func()) error {
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	var lastErr error
	backoff := 25 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		if reset != nil {
			reset()
		}
		if err := s.store.CompleteCheckoutWithPayment(compCtx, params); err != nil {
			lastErr = err
			if errors.Is(err, store.ErrNotFound) {
				return err
			}
			if attempt < 2 {
				select {
				case <-compCtx.Done():
					return compCtx.Err()
				case <-time.After(backoff):
					backoff *= 2
				}
			}
			continue
		}
		return nil
	}
	return lastErr
}

// detachedCallerContext detaches cancellation/deadline from an incoming request context while
// preserving caller metadata (e.g. buyer JWT for Cart.ClearItems) and trace context.
func detachedCallerContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func isAmbiguousReserveError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.Unavailable, codes.Canceled, codes.Aborted, codes.Internal, codes.Unknown:
		return true
	default:
		return false
	}
}

// compensateBeforePersistAndFail releases stock/promotion reservations after a pre-persist failure.
// Only marks checkout_groups FAILED once all required compensations succeed; otherwise flags
// needs_cleanup = TRUE so ScanStaleCheckoutGroups retries cleanup and marks FAILED afterward.
func (s *OrderService) compensateBeforePersistAndFail(
	ctx context.Context,
	checkoutGroupID string,
	releaseStock, releasePromo bool,
	failReason string,
) {
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	compFailed := false
	if releasePromo {
		if err := s.releasePromotionErr(compCtx, checkoutGroupID); err != nil {
			s.logger.ErrorContext(ctx, "saga compensation ReleasePromotion failed; leaving checkout group for watchdog cleanup",
				"checkout_group_id", checkoutGroupID, "error", err)
			compFailed = true
		}
	}
	if releaseStock {
		if _, err := s.releaseStockErr(compCtx, checkoutGroupID); err != nil {
			s.logger.ErrorContext(ctx, "saga compensation ReleaseStock failed; leaving checkout group for watchdog cleanup",
				"checkout_group_id", checkoutGroupID, "error", err)
			compFailed = true
		}
	}

	if compFailed {
		if err := s.store.MarkCheckoutGroupNeedsCleanup(compCtx, checkoutGroupID, failReason); err != nil {
			s.logger.ErrorContext(ctx, "failed to flag checkout group needs_cleanup",
				"checkout_group_id", checkoutGroupID, "error", err)
		}
		return
	}
	if err := s.store.FailCheckoutGroup(compCtx, checkoutGroupID, failReason); err != nil {
		s.logger.WarnContext(ctx, "failed to mark checkout group failed",
			"checkout_group_id", checkoutGroupID, "error", err)
	}
}

func (s *OrderService) resolveCheckoutInputs(ctx context.Context, buyerID string, req *orderv1.CreateCheckoutRequest, salesChannel, storefrontShopID string) ([]*orderv1.CheckoutItemInput, string, error) {
	merged := make(map[string]*orderv1.CheckoutItemInput)

	if req.GetFromCart() {
		cartReq := &cartv1.GetCartRequest{UserId: buyerID}
		if salesChannel == SalesChannelStorefront {
			cartReq.ShopId = storefrontShopID
		}
		cartResp, err := s.clients.Cart.GetCart(ctx, cartReq)
		if err != nil {
			return nil, "ITEMS_RESOLVE_FAILED", err
		}
		cart := cartResp.GetCart()
		if cart == nil || len(cart.GetShopGroups()) == 0 {
			return nil, "ITEMS_RESOLVE_FAILED", status.Error(codes.FailedPrecondition, "shopping cart is empty")
		}

		// Map any flash_sale_item_id overrides supplied alongside from_cart=true.
		flashOverrides := make(map[string]string, len(req.GetItems()))
		for _, it := range req.GetItems() {
			skuID := strings.TrimSpace(it.GetSkuId())
			flashID := strings.TrimSpace(it.GetFlashSaleItemId())
			if skuID == "" || flashID == "" {
				continue
			}
			if prev, exists := flashOverrides[skuID]; exists && prev != flashID {
				return nil, "ITEMS_RESOLVE_FAILED", status.Errorf(codes.InvalidArgument, "duplicate sku_id %s with conflicting flash_sale_item_id", skuID)
			}
			flashOverrides[skuID] = flashID
		}

		anySelected := false
		for _, grp := range cart.GetShopGroups() {
			if salesChannel == SalesChannelStorefront && grp.GetShopId() != "" && grp.GetShopId() != storefrontShopID {
				continue
			}
			for _, ci := range grp.GetItems() {
				if salesChannel == SalesChannelStorefront && ci.GetShopId() != "" && ci.GetShopId() != storefrontShopID {
					continue
				}
				if ci.GetQuantity() > 0 && ci.GetSelected() {
					anySelected = true
					break
				}
			}
			if anySelected {
				break
			}
		}

		var unavailSelectedSKUs []string
		var unavailMissingShopSKUs []string
		var selectedShopIDs []string
		for _, grp := range cart.GetShopGroups() {
			if salesChannel == SalesChannelStorefront && grp.GetShopId() != "" && grp.GetShopId() != storefrontShopID {
				continue
			}
			for _, ci := range grp.GetItems() {
				if salesChannel == SalesChannelStorefront && ci.GetShopId() != "" && ci.GetShopId() != storefrontShopID {
					continue
				}
				if ci.GetQuantity() <= 0 {
					continue
				}
				if anySelected && !ci.GetSelected() {
					continue
				}
				skuID := strings.TrimSpace(ci.GetSkuId())
				if skuID == "" {
					continue
				}
				shopID := fallbackStr(strings.TrimSpace(ci.GetShopId()), strings.TrimSpace(grp.GetShopId()))
				if shopID != "" {
					selectedShopIDs = append(selectedShopIDs, shopID)
				}
				// E4 + F12/Item G (#8): Never silently drop a selected unavailable item, AND check shop
				// status BEFORE rejecting SKU_INACTIVE so a suspended shop always returns SHOP_SUSPENDED.
				if ci.GetUnavailable() {
					if ci.GetSelected() {
						unavailSelectedSKUs = append(unavailSelectedSKUs, skuID)
						if shopID == "" {
							unavailMissingShopSKUs = append(unavailMissingShopSKUs, skuID)
						}
					}
					continue
				}
				if prev, ok := merged[skuID]; ok {
					prev.Quantity += ci.GetQuantity()
				} else {
					merged[skuID] = &orderv1.CheckoutItemInput{
						SkuId:           skuID,
						Quantity:        ci.GetQuantity(),
						FlashSaleItemId: flashOverrides[skuID],
					}
				}
			}
		}
		if len(unavailSelectedSKUs) > 0 {
			if len(unavailMissingShopSKUs) > 0 && s.clients.Product != nil {
				sort.Strings(unavailMissingShopSKUs)
				if skuResp, sErr := s.clients.Product.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: unavailMissingShopSKUs}); sErr == nil {
					for _, sku := range skuResp.GetSkus() {
						if sid := strings.TrimSpace(sku.GetShopId()); sid != "" {
							selectedShopIDs = append(selectedShopIDs, sid)
						}
					}
				}
			}
			if salesChannel != SalesChannelStorefront && len(selectedShopIDs) > 0 {
				if _, shopFailReason, shopErr := s.verifyShopsActive(ctx, selectedShopIDs); shopErr != nil {
					return nil, shopFailReason, shopErr
				}
			}
			return nil, "ITEMS_RESOLVE_FAILED", status.Errorf(codes.FailedPrecondition, "SKU_INACTIVE: SKU %s is not available for purchase", unavailSelectedSKUs[0])
		}
		if len(merged) == 0 && salesChannel == SalesChannelStorefront {
			return nil, "ITEMS_RESOLVE_FAILED", status.Errorf(codes.FailedPrecondition, "shopping cart has no items for storefront shop %s", storefrontShopID)
		}
		if len(merged) == 0 {
			return nil, "ITEMS_RESOLVE_FAILED", status.Error(codes.FailedPrecondition, "shopping cart is empty")
		}
	} else {
		for _, it := range req.GetItems() {
			skuID := strings.TrimSpace(it.GetSkuId())
			if skuID == "" || it.GetQuantity() <= 0 {
				return nil, "ITEMS_RESOLVE_FAILED", status.Error(codes.InvalidArgument, "each checkout item must have sku_id and positive quantity")
			}
			flashID := strings.TrimSpace(it.GetFlashSaleItemId())
			if prev, ok := merged[skuID]; ok {
				if prev.FlashSaleItemId != flashID {
					return nil, "ITEMS_RESOLVE_FAILED", status.Errorf(codes.InvalidArgument, "duplicate sku_id %s with conflicting flash_sale_item_id", skuID)
				}
				prev.Quantity += it.GetQuantity()
			} else {
				merged[skuID] = &orderv1.CheckoutItemInput{
					SkuId:           skuID,
					Quantity:        it.GetQuantity(),
					FlashSaleItemId: flashID,
				}
			}
		}
	}

	if len(merged) == 0 {
		return nil, "ITEMS_RESOLVE_FAILED", status.Error(codes.InvalidArgument, "checkout must contain at least one item")
	}

	out := make([]*orderv1.CheckoutItemInput, 0, len(merged))
	for _, it := range merged {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkuId < out[j].SkuId })
	return out, "", nil
}

func buildShopPlans(
	requested []*orderv1.CheckoutItemInput,
	skuByID map[string]*productv1.SKU,
) ([]*shopCheckoutPlan, []*productv1.StockReservationItem, []*promotionv1.FlashSaleReservationItem, error) {
	byShop := make(map[string]*shopCheckoutPlan)
	stockItems := make([]*productv1.StockReservationItem, 0, len(requested))
	var flashItems []*promotionv1.FlashSaleReservationItem

	for _, reqItem := range requested {
		sku, ok := skuByID[reqItem.GetSkuId()]
		if !ok || sku == nil {
			return nil, nil, nil, status.Errorf(codes.NotFound, "SKU %s not found", reqItem.GetSkuId())
		}
		if sku.GetShopId() == "" {
			return nil, nil, nil, status.Errorf(codes.FailedPrecondition, "SKU %s has no shop_id", sku.GetId())
		}
		if !sku.GetIsActive() {
			return nil, nil, nil, status.Errorf(codes.FailedPrecondition, "SKU_INACTIVE: SKU %s is not available for purchase", sku.GetId())
		}
		if sku.GetPriceVnd() <= 0 {
			return nil, nil, nil, status.Errorf(codes.FailedPrecondition, "SKU %s has invalid price", sku.GetId())
		}
		if bps := sku.GetCommissionRateBps(); bps < 0 || bps > 10000 {
			return nil, nil, nil, status.Errorf(
				codes.FailedPrecondition,
				"INVALID_COMMISSION_RATE: SKU %s has invalid commission_rate_bps %d (expected 0..10000)",
				sku.GetId(),
				bps,
			)
		}
		if sku.GetAvailableStock() < reqItem.GetQuantity() {
			return nil, nil, nil, status.Errorf(
				codes.FailedPrecondition,
				"INSUFFICIENT_STOCK: SKU %s has %d available, requested %d",
				sku.GetId(),
				sku.GetAvailableStock(),
				reqItem.GetQuantity(),
			)
		}

		plan, exists := byShop[sku.GetShopId()]
		if !exists {
			plan = &shopCheckoutPlan{shopID: sku.GetShopId()}
			byShop[sku.GetShopId()] = plan
		}

		weight := sku.GetWeightGrams()

		plan.items = append(plan.items, resolvedLineItem{
			sku:             sku,
			quantity:        reqItem.GetQuantity(),
			weightGrams:     weight,
			flashSaleItemID: reqItem.GetFlashSaleItemId(),
			effectivePrice:  sku.GetPriceVnd(),
		})
		plan.subtotalVND += sku.GetPriceVnd() * int64(reqItem.GetQuantity())
		if weight > 0 {
			plan.weightGrams += weight * reqItem.GetQuantity()
		} else {
			plan.weightGrams += 500 * reqItem.GetQuantity()
		}

		stockItems = append(stockItems, &productv1.StockReservationItem{
			SkuId:    sku.GetId(),
			Quantity: reqItem.GetQuantity(),
		})

		if reqItem.GetFlashSaleItemId() != "" {
			flashItems = append(flashItems, &promotionv1.FlashSaleReservationItem{
				FlashSaleItemId: reqItem.GetFlashSaleItemId(),
				SkuId:           sku.GetId(),
				Quantity:        reqItem.GetQuantity(),
				ShopId:          sku.GetShopId(),
			})
		}
	}

	plans := make([]*shopCheckoutPlan, 0, len(byShop))
	for _, p := range byShop {
		sort.Slice(p.items, func(i, j int) bool { return p.items[i].sku.GetId() < p.items[j].sku.GetId() })
		plans = append(plans, p)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].shopID < plans[j].shopID })
	sort.Slice(stockItems, func(i, j int) bool { return stockItems[i].SkuId < stockItems[j].SkuId })

	return plans, stockItems, flashItems, nil
}

func (s *OrderService) calculateShippingFees(
	ctx context.Context,
	plans []*shopCheckoutPlan,
	choices []*orderv1.ShopShippingChoice,
	toAddr *orderv1.OrderAddressSnapshot,
	paymentMethod string,
) error {
	choiceByShop := make(map[string]*orderv1.ShopShippingChoice, len(choices))
	for _, c := range choices {
		if c != nil && c.GetShopId() != "" {
			choiceByShop[c.GetShopId()] = c
		}
	}

	if s.clients.Shipping == nil {
		return status.Error(codes.Unavailable, "shipping service is unavailable")
	}

	for _, plan := range plans {
		// Wave 2b: Fetch shop shipping settings using a service token (default_weight_grams + shop freeship).
		defaultWeight := int32(500)
		settingsResp, sErr := s.clients.Shipping.GetShopShippingSettings(
			clients.WithInternalAdminContext(ctx),
			&shippingv1.GetShopShippingSettingsRequest{ShopId: plan.shopID},
		)
		if sErr != nil {
			if status.Code(sErr) != codes.NotFound {
				return wrapDownstreamError(fmt.Sprintf("load shipping settings for shop %s", plan.shopID), sErr)
			}
		} else if st := settingsResp.GetSettings(); st != nil {
			if st.GetDefaultWeightGrams() > 0 {
				defaultWeight = st.GetDefaultWeightGrams()
			}
			if st.GetFreeShipEnabled() && st.GetFreeShipMaxVnd() > 0 && st.GetFreeShipMinOrderVnd() >= 0 {
				plan.freeShipEnabled = true
				plan.freeShipMinOrderVND = st.GetFreeShipMinOrderVnd()
				plan.freeShipMaxVND = st.GetFreeShipMaxVnd()
			}
		}
		plan.defaultWeightGrams = defaultWeight
		var resolvedWeight int32
		for idx := range plan.items {
			w := plan.items[idx].sku.GetWeightGrams()
			if w <= 0 {
				w = defaultWeight
			}
			plan.items[idx].weightGrams = w
			resolvedWeight += w * plan.items[idx].quantity
		}
		if resolvedWeight <= 0 {
			resolvedWeight = defaultWeight
		}
		plan.weightGrams = resolvedWeight

		var (
			pickup *shippingv1.ShipmentAddress
			pErr   error
		)
		if plan.shop != nil {
			pickup, pErr = s.resolveShopPickupAddressFromShop(ctx, plan.shop)
		} else {
			pickup, pErr = s.resolveShopPickupAddress(ctx, plan.shopID)
		}
		if pErr != nil {
			return pErr
		}
		fromProvince, fromWard := pickup.GetProvinceCode(), pickup.GetWardCode()
		var codEstimate int64
		if statemachine.IsCODMethod(paymentMethod) {
			codEstimate = plan.subtotalVND
		}

		feeResp, err := s.clients.Shipping.CalculateFee(ctx, &shippingv1.CalculateFeeRequest{
			ShopId:           plan.shopID,
			FromProvinceCode: fromProvince,
			FromWardCode:     fromWard,
			ToProvinceCode:   toAddr.GetProvinceCode(),
			ToWardCode:       toAddr.GetWardCode(),
			WeightGrams:      plan.weightGrams,
			CodAmountVnd:     codEstimate,
		})
		if err != nil {
			return wrapDownstreamError(fmt.Sprintf("calculate shipping fee for shop %s", plan.shopID), err)
		}
		if feeResp.GetIsEstimated() {
			return status.Errorf(codes.FailedPrecondition, "ADDRESS_WARD_REQUIRED: checkout requires a specific destination ward for shop %s", plan.shopID)
		}
		quotes := feeResp.GetQuotes()
		if len(quotes) == 0 {
			return status.Errorf(codes.FailedPrecondition, "no shipping quotes available for shop %s", plan.shopID)
		}

		chosen := pickShippingQuote(quotes, choiceByShop[plan.shopID], paymentMethod)
		plan.carrierCode = chosen.GetCarrierCode()
		plan.serviceCode = chosen.GetServiceCode()
		plan.shippingFeeVND = chosen.GetFeeVnd()
		plan.codSupported = chosen.GetCodSupported()
	}
	return nil
}

func wrapDownstreamError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Errorf(codes.DeadlineExceeded, "%s: %v", prefix, err)
	}
	if errors.Is(err, context.Canceled) {
		return status.Errorf(codes.Canceled, "%s: %v", prefix, err)
	}
	if st, ok := status.FromError(err); ok {
		return status.Errorf(st.Code(), "%s: %s", prefix, st.Message())
	}
	return status.Errorf(codes.Unavailable, "%s: %v", prefix, err)
}

func (s *OrderService) lookupPaymentFeeBPS(ctx context.Context, method string, atTime time.Time) (int32, error) {
	if s.clients.Payment == nil {
		return 0, status.Error(codes.Unavailable, "PAYMENT_FEE_CONFIG_UNAVAILABLE: payment client is not configured")
	}
	payMethod, err := paymentProtoMethod(method)
	if err != nil {
		return 0, err
	}
	resp, err := s.clients.Payment.GetFeeConfig(clients.WithInternalAdminContext(ctx), &paymentv1.GetFeeConfigRequest{
		PaymentMethod: payMethod,
		AtTime:        timestamppb.New(atTime),
	})
	if err != nil {
		return 0, wrapDownstreamError(fmt.Sprintf("PAYMENT_FEE_CONFIG_FAILED: load payment fee config for %s", payMethod.String()), err)
	}
	for _, cfg := range resp.GetActiveConfigs() {
		if cfg == nil {
			continue
		}
		if cfg.GetPaymentMethod() == payMethod {
			bps := cfg.GetFeeBps()
			if bps < 0 || bps > 10000 {
				return 0, status.Errorf(codes.Internal, "PAYMENT_FEE_CONFIG_INVALID: invalid fee_bps %d for %s", bps, payMethod.String())
			}
			return bps, nil
		}
	}
	return 0, status.Errorf(
		codes.FailedPrecondition,
		"PAYMENT_FEE_CONFIG_NOT_FOUND: no active payment fee config found for %s",
		payMethod.String(),
	)
}

func pickShippingQuote(quotes []*shippingv1.CarrierQuote, choice *orderv1.ShopShippingChoice, paymentMethod string) *shippingv1.CarrierQuote {
	if choice != nil && choice.GetCarrierCode() != "" {
		for _, q := range quotes {
			if strings.EqualFold(q.GetCarrierCode(), choice.GetCarrierCode()) {
				if choice.GetServiceCode() == "" || strings.EqualFold(q.GetServiceCode(), choice.GetServiceCode()) {
					return q
				}
			}
		}
		for _, q := range quotes {
			if strings.EqualFold(q.GetCarrierCode(), choice.GetCarrierCode()) {
				return q
			}
		}
	}

	if statemachine.IsCODMethod(paymentMethod) {
		var cheapestCOD *shippingv1.CarrierQuote
		for _, q := range quotes {
			if !q.GetCodSupported() {
				continue
			}
			if cheapestCOD == nil || q.GetFeeVnd() < cheapestCOD.GetFeeVnd() {
				cheapestCOD = q
			}
		}
		if cheapestCOD != nil {
			return cheapestCOD
		}
	}

	cheapest := quotes[0]
	for _, q := range quotes[1:] {
		if q.GetFeeVnd() < cheapest.GetFeeVnd() {
			cheapest = q
		}
	}
	return cheapest
}

func buildReservePromotionRequest(
	checkoutGroupID, buyerID string,
	req *orderv1.CreateCheckoutRequest,
	plans []*shopCheckoutPlan,
	flashItems []*promotionv1.FlashSaleReservationItem,
	shopProgramItems []*promotionv1.ShopDiscountProgramReservationItem,
	salesChannel, storefrontShopID string,
) *promotionv1.ReservePromotionRequest {
	shopInputs := make([]*promotionv1.ShopDiscountInput, 0, len(plans))
	for _, p := range plans {
		lineItems := make([]*promotionv1.DiscountLineItem, 0, len(p.items))
		for _, it := range p.items {
			lineItems = append(lineItems, &promotionv1.DiscountLineItem{
				SkuId:                     it.sku.GetId(),
				Quantity:                  it.quantity,
				UnitPriceVnd:              it.sku.GetPriceVnd(),
				FlashSaleItemId:           it.flashSaleItemID,
				CategoryId:                it.sku.GetCategoryId(),
				ShopDiscountProgramItemId: it.shopProgramItemID,
			})
		}
		shopInputs = append(shopInputs, &promotionv1.ShopDiscountInput{
			ShopId:              p.shopID,
			SubtotalVnd:         p.subtotalVND,
			ShippingFeeVnd:      p.shippingFeeVND,
			Items:               lineItems,
			FreeShipEnabled:     p.freeShipEnabled,
			FreeShipMinOrderVnd: p.freeShipMinOrderVND,
			FreeShipMaxVnd:      p.freeShipMaxVND,
		})
	}

	return &promotionv1.ReservePromotionRequest{
		OrderId:                  checkoutGroupID,
		UserId:                   buyerID,
		ShopVoucherIds:           req.GetShopVoucherIds(),
		PlatformVoucherId:        req.GetPlatformVoucherId(),
		FreeshipVoucherId:        req.GetFreeshipVoucherId(),
		CoinsToUse:               req.GetCoinsToUse(),
		FlashSaleItems:           flashItems,
		ShopItems:                shopInputs,
		ShopDiscountProgramItems: shopProgramItems,
		SalesChannel:             salesChannel,
		StorefrontShopId:         storefrontShopID,
	}
}

func (s *OrderService) resolveActiveFlashSales(
	ctx context.Context,
	flashItems []*promotionv1.FlashSaleReservationItem,
	skuByID map[string]*productv1.SKU,
	now time.Time,
) (map[string]int64, *time.Time, error) {
	prices := make(map[string]int64, len(flashItems))
	if len(flashItems) == 0 {
		return prices, nil, nil
	}
	if s.clients.Promotion == nil {
		return nil, nil, status.Error(codes.Unavailable, "FLASH_SALE_LOOKUP_FAILED: promotion client is not configured")
	}
	resp, err := s.clients.Promotion.ListActiveFlashSales(stripServiceAuth(ctx), &promotionv1.ListActiveFlashSalesRequest{IncludeUpcoming: false})
	if err != nil {
		return nil, nil, fmt.Errorf("FLASH_SALE_LOOKUP_FAILED: list active flash sales: %w", err)
	}

	type activeFlashEntry struct {
		item   *promotionv1.FlashSaleItem
		endsAt *time.Time
	}
	activeByID := make(map[string]activeFlashEntry)
	for _, fs := range resp.GetFlashSales() {
		if fs == nil {
			continue
		}
		if fs.GetStartsAt() != nil && now.Before(fs.GetStartsAt().AsTime().UTC()) {
			continue
		}
		var endsAt *time.Time
		if fs.GetEndsAt() != nil {
			et := fs.GetEndsAt().AsTime().UTC()
			if !now.Before(et) {
				continue
			}
			endsAt = &et
		}
		for _, it := range fs.GetItems() {
			if it == nil || strings.TrimSpace(it.GetId()) == "" {
				continue
			}
			if it.GetStatus() != promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_UNSPECIFIED &&
				it.GetStatus() != promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED {
				continue
			}
			activeByID[it.GetId()] = activeFlashEntry{item: it, endsAt: endsAt}
		}
	}

	var earliestEndsAt *time.Time
	for _, reqFI := range flashItems {
		entry, ok := activeByID[reqFI.GetFlashSaleItemId()]
		if !ok {
			return nil, nil, status.Errorf(
				codes.FailedPrecondition,
				"FLASH_SALE_NOT_ACTIVE: suất Flash Sale %s hiện không đang diễn ra hoặc chưa được duyệt",
				reqFI.GetFlashSaleItemId(),
			)
		}
		if entry.item.GetSkuId() != "" && entry.item.GetSkuId() != reqFI.GetSkuId() {
			return nil, nil, status.Errorf(
				codes.InvalidArgument,
				"FLASH_SALE_SKU_MISMATCH: flash sale item %s does not belong to SKU %s",
				reqFI.GetFlashSaleItemId(),
				reqFI.GetSkuId(),
			)
		}
		if entry.item.GetShopId() != "" && reqFI.GetShopId() != "" && entry.item.GetShopId() != reqFI.GetShopId() {
			return nil, nil, status.Errorf(
				codes.InvalidArgument,
				"FLASH_SALE_SHOP_MISMATCH: flash sale item %s does not belong to shop %s",
				reqFI.GetFlashSaleItemId(),
				reqFI.GetShopId(),
			)
		}
		fp := entry.item.GetFlashPriceVnd()
		if fp <= 0 {
			return nil, nil, status.Errorf(
				codes.FailedPrecondition,
				"FLASH_SALE_INVALID_PRICE: flash sale item %s has invalid flash price",
				reqFI.GetFlashSaleItemId(),
			)
		}
		if sku := skuByID[reqFI.GetSkuId()]; sku != nil && fp > sku.GetPriceVnd() {
			return nil, nil, status.Errorf(
				codes.FailedPrecondition,
				"FLASH_SALE_INVALID_PRICE: flash price %d exceeds SKU price %d",
				fp,
				sku.GetPriceVnd(),
			)
		}
		prices[reqFI.GetFlashSaleItemId()] = fp
		if entry.endsAt != nil && (earliestEndsAt == nil || entry.endsAt.Before(*earliestEndsAt)) {
			et := *entry.endsAt
			earliestEndsAt = &et
		}
	}
	return prices, earliestEndsAt, nil
}

const maxShopDiscountLookupBatch = 200

func (s *OrderService) resolveActiveShopDiscounts(
	ctx context.Context,
	plans []*shopCheckoutPlan,
	now time.Time,
) ([]*promotionv1.ShopDiscountProgramReservationItem, map[string]int64, error) {
	prices := make(map[string]int64)
	if s.clients.Promotion == nil {
		return nil, prices, nil
	}

	nonFlashSKUs := make([]string, 0)
	for _, p := range plans {
		for _, it := range p.items {
			// Single item-price rule: flash sale always takes precedence over shop discount program.
			if it.flashSaleItemID == "" {
				nonFlashSKUs = append(nonFlashSKUs, it.sku.GetId())
			}
		}
	}
	if len(nonFlashSKUs) == 0 {
		return nil, prices, nil
	}
	sort.Strings(nonFlashSKUs)

	bestBySKU := make(map[string]*promotionv1.ActiveShopDiscount)
	lookupCtx := stripServiceAuth(ctx)
	for start := 0; start < len(nonFlashSKUs); start += maxShopDiscountLookupBatch {
		end := start + maxShopDiscountLookupBatch
		if end > len(nonFlashSKUs) {
			end = len(nonFlashSKUs)
		}
		resp, err := s.clients.Promotion.ListActiveShopDiscounts(lookupCtx, &promotionv1.ListActiveShopDiscountsRequest{
			SkuIds: nonFlashSKUs[start:end],
		})
		if err != nil {
			return nil, nil, fmt.Errorf("SHOP_DISCOUNT_LOOKUP_FAILED: list active shop discounts: %w", err)
		}
		for _, d := range resp.GetDiscounts() {
			if d == nil || strings.TrimSpace(d.GetItemId()) == "" || strings.TrimSpace(d.GetSkuId()) == "" {
				continue
			}
			if d.GetStartsAt() != nil && now.Before(d.GetStartsAt().AsTime().UTC()) {
				continue
			}
			if d.GetEndsAt() != nil && !now.Before(d.GetEndsAt().AsTime().UTC()) {
				continue
			}
			if d.GetDiscountedPriceVnd() <= 0 {
				continue
			}
			prev, exists := bestBySKU[d.GetSkuId()]
			if !exists ||
				d.GetDiscountedPriceVnd() < prev.GetDiscountedPriceVnd() ||
				(d.GetDiscountedPriceVnd() == prev.GetDiscountedPriceVnd() && d.GetItemId() < prev.GetItemId()) {
				bestBySKU[d.GetSkuId()] = d
			}
		}
	}

	var programItems []*promotionv1.ShopDiscountProgramReservationItem
	for _, p := range plans {
		for idx := range p.items {
			it := &p.items[idx]
			if it.flashSaleItemID != "" {
				it.shopProgramItemID = ""
				continue
			}
			d, ok := bestBySKU[it.sku.GetId()]
			if !ok {
				continue
			}
			if d.GetShopId() != "" && d.GetShopId() != p.shopID {
				continue
			}
			if d.GetDiscountedPriceVnd() >= it.sku.GetPriceVnd() {
				continue
			}
			it.shopProgramItemID = d.GetItemId()
			prices[d.GetItemId()] = d.GetDiscountedPriceVnd()
			programItems = append(programItems, &promotionv1.ShopDiscountProgramReservationItem{
				ShopDiscountProgramItemId: d.GetItemId(),
				SkuId:                     it.sku.GetId(),
				Quantity:                  it.quantity,
				ShopId:                    p.shopID,
			})
		}
	}
	return programItems, prices, nil
}

func applyDiscountsAndTotals(
	plans []*shopCheckoutPlan,
	promoReserved bool,
	breakdown *promotionv1.DiscountBreakdown,
	flashPrices map[string]int64,
	shopProgramPrices map[string]int64,
	paymentFeeBPS int32,
) error {
	shopBreakdowns := make(map[string]*promotionv1.ShopDiscountBreakdown)
	if breakdown != nil {
		for _, sb := range breakdown.GetShops() {
			if sb != nil {
				shopBreakdowns[sb.GetShopId()] = sb
			}
		}
	}

	if breakdown != nil && len(shopBreakdowns) > 0 {
		for _, p := range plans {
			if sb, ok := shopBreakdowns[p.shopID]; ok {
				p.flashSaleDiscountVND = maxInt64(0, sb.GetFlashSaleDiscountVnd())
				p.shopProgramDiscountVND = maxInt64(0, sb.GetShopProgramDiscountVnd())
				p.shopVoucherDiscount = maxInt64(0, sb.GetShopVoucherDiscountVnd())
				p.shopFreeshipDiscountVND = maxInt64(0, sb.GetShopFreeshipDiscountVnd())
				p.platformVoucherShare = maxInt64(0, sb.GetPlatformVoucherDiscountVnd())
				p.freeshipDiscountShare = maxInt64(0, sb.GetFreeshipDiscountVnd())
				p.coinsDiscountShare = maxInt64(0, sb.GetCoinsDiscountVnd())
			}
		}
	} else if promoReserved && breakdown != nil &&
		(breakdown.GetTotalFlashSaleDiscountVnd() > 0 ||
			breakdown.GetTotalShopProgramDiscountVnd() > 0 ||
			breakdown.GetTotalShopVoucherDiscountVnd() > 0 ||
			breakdown.GetTotalShopFreeshipDiscountVnd() > 0 ||
			breakdown.GetTotalPlatformVoucherDiscountVnd() > 0 ||
			breakdown.GetTotalFreeshipDiscountVnd() > 0 ||
			breakdown.GetTotalCoinsDiscountVnd() > 0) {
		return status.Error(codes.FailedPrecondition, "PROMOTION_BREAKDOWN_MISSING: promotion service returned totals without per-shop breakdown")
	}

	for _, p := range plans {
		// Apply flash sale & shop discount program effective unit prices (strictly mutually exclusive per SKU).
		var flashItemsIdx []int
		var shopProgItemsIdx []int
		for idx := range p.items {
			if p.items[idx].flashSaleItemID != "" {
				p.items[idx].shopProgramItemID = ""
				flashItemsIdx = append(flashItemsIdx, idx)
			} else if p.items[idx].shopProgramItemID != "" {
				shopProgItemsIdx = append(shopProgItemsIdx, idx)
			}
		}

		// Flash sale discount F: ReservePromotion breakdown is the sole source of truth for F.
		// If breakdown disagrees with expected (unit_price - flash_price) * qty, fail checkout clearly
		// so caller compensates stock + promotion instead of guessing or reverse-engineering prices.
		var computedFlashDiscount int64
		for _, idx := range flashItemsIdx {
			it := &p.items[idx]
			if fp, ok := flashPrices[it.flashSaleItemID]; ok && fp > 0 && fp <= it.sku.GetPriceVnd() {
				computedFlashDiscount += (it.sku.GetPriceVnd() - fp) * int64(it.quantity)
			}
		}
		if promoReserved {
			if p.flashSaleDiscountVND != computedFlashDiscount {
				return status.Errorf(
					codes.FailedPrecondition,
					"FLASH_SALE_PRICE_CHANGED: giá Flash Sale của shop %s đã thay đổi hoặc không khớp (dự tính %d VND, thực tế %d VND); vui lòng đặt lại đơn",
					p.shopID,
					computedFlashDiscount,
					p.flashSaleDiscountVND,
				)
			}
		}
		for _, idx := range flashItemsIdx {
			it := &p.items[idx]
			if fp, ok := flashPrices[it.flashSaleItemID]; ok && fp > 0 && fp <= it.sku.GetPriceVnd() {
				it.effectivePrice = fp
			}
		}

		// Shop discount program (UC-S18): ReservePromotion breakdown and applied_shop_program_item_ids
		// are the sole source of truth for which lines received program pricing and for P_shop.
		// Lines sent by order but excluded from applied_shop_program_item_ids (e.g. exceeded per_user_limit)
		// are billed at regular price with shop_program_item_id = "" in order_items.
		sentProgByID := make(map[string]bool, len(shopProgItemsIdx))
		for _, idx := range shopProgItemsIdx {
			sentProgByID[p.items[idx].shopProgramItemID] = true
		}

		var computedShopProgramDiscount int64
		if promoReserved {
			appliedSet := make(map[string]bool)
			if sb, ok := shopBreakdowns[p.shopID]; ok {
				for _, rawID := range sb.GetAppliedShopProgramItemIds() {
					id := strings.TrimSpace(rawID)
					if id == "" || !sentProgByID[id] {
						return status.Errorf(
							codes.FailedPrecondition,
							"SHOP_PROGRAM_ITEM_UNKNOWN: promotion trả về mã chương trình giảm giá %q không thuộc danh sách sản phẩm đã gửi cho shop %s",
							id,
							p.shopID,
						)
					}
					appliedSet[id] = true
				}
			}

			for _, idx := range shopProgItemsIdx {
				it := &p.items[idx]
				if !appliedSet[it.shopProgramItemID] {
					it.effectivePrice = it.sku.GetPriceVnd()
					it.shopProgramItemID = ""
					continue
				}
				if sp, ok := shopProgramPrices[it.shopProgramItemID]; ok && sp > 0 && sp < it.sku.GetPriceVnd() {
					it.effectivePrice = sp
					computedShopProgramDiscount += (it.sku.GetPriceVnd() - sp) * int64(it.quantity)
				}
			}

			if p.shopProgramDiscountVND != computedShopProgramDiscount {
				return status.Errorf(
					codes.FailedPrecondition,
					"SHOP_PROGRAM_PRICE_CHANGED: giá chương trình giảm giá của shop %s đã thay đổi hoặc không còn hiệu lực (dự tính %d VND, thực tế %d VND); vui lòng đặt lại đơn",
					p.shopID,
					computedShopProgramDiscount,
					p.shopProgramDiscountVND,
				)
			}
		} else {
			for _, idx := range shopProgItemsIdx {
				p.items[idx].effectivePrice = p.items[idx].sku.GetPriceVnd()
				p.items[idx].shopProgramItemID = ""
			}
		}

		// Clamp seller merchandise discounts F + P_shop + V_shop <= G.
		p.flashSaleDiscountVND = minInt64(maxInt64(0, p.flashSaleDiscountVND), p.subtotalVND)
		p.shopProgramDiscountVND = minInt64(maxInt64(0, p.shopProgramDiscountVND), maxInt64(0, p.subtotalVND-p.flashSaleDiscountVND))
		p.shopVoucherDiscount = minInt64(maxInt64(0, p.shopVoucherDiscount), maxInt64(0, p.subtotalVND-p.flashSaleDiscountVND-p.shopProgramDiscountVND))

		merchAfterShopDisc := p.subtotalVND - p.flashSaleDiscountVND - p.shopProgramDiscountVND - p.shopVoucherDiscount

		// Shop freeship (UC-SHIP06): compute locally ONLY when ReservePromotion was NOT called (breakdown == nil).
		// When ReservePromotion returned a breakdown, breakdown.shop_freeship_discount_vnd is authoritative.
		// Always keep safety bounds (<= Ship and <= merchAfterShopDisc so R >= 0).
		if !promoReserved && breakdown == nil && p.freeShipEnabled && p.freeShipMaxVND > 0 && merchAfterShopDisc >= p.freeShipMinOrderVND {
			p.shopFreeshipDiscountVND = minInt64(p.shippingFeeVND, p.freeShipMaxVND)
		}
		if p.shopFreeshipDiscountVND > p.shippingFeeVND {
			p.shopFreeshipDiscountVND = p.shippingFeeVND
		}
		if p.shopFreeshipDiscountVND > merchAfterShopDisc {
			p.shopFreeshipDiscountVND = merchAfterShopDisc
		}
		if p.shopFreeshipDiscountVND < 0 {
			p.shopFreeshipDiscountVND = 0
		}

		// Seller net merchandise revenue R = G - F - P_shop - V_shop - S_shop (>= 0).
		sellerRevenue := merchAfterShopDisc - p.shopFreeshipDiscountVND

		// Platform freeship S_free applies AFTER shop freeship on remaining Ship - S_shop.
		remainingShip := maxInt64(0, p.shippingFeeVND-p.shopFreeshipDiscountVND)
		if p.freeshipDiscountShare > remainingShip {
			p.freeshipDiscountShare = remainingShip
		}
		if p.freeshipDiscountShare < 0 {
			p.freeshipDiscountShare = 0
		}

		// Cap platform discounts V_plat and X so D = V_plat + S_free + X <= R + Ship and P + D == R + Ship.
		maxAfterFreeship := sellerRevenue + p.shippingFeeVND - p.freeshipDiscountShare
		p.platformVoucherShare = minInt64(maxInt64(0, p.platformVoucherShare), maxAfterFreeship)
		maxCoins := maxInt64(0, maxAfterFreeship-p.platformVoucherShare)
		p.coinsDiscountShare = minInt64(maxInt64(0, p.coinsDiscountShare), maxCoins)

		platformDiscount := p.platformVoucherShare + p.freeshipDiscountShare + p.coinsDiscountShare
		p.totalPayableVND = sellerRevenue + p.shippingFeeVND - platformDiscount

		// Category commission C_cat computed per line on post-item-discount value using SKU's exact category bps
		// (0 bps is valid when admin sets a 0% category fee schedule); capped at R.
		var rawCommission int64
		for idx := range p.items {
			it := &p.items[idx]
			bps := it.sku.GetCommissionRateBps()
			if bps < 0 || bps > 10000 {
				return status.Errorf(
					codes.FailedPrecondition,
					"INVALID_COMMISSION_RATE: SKU %s has invalid commission_rate_bps %d (expected 0..10000)",
					it.sku.GetId(),
					bps,
				)
			}
			it.commissionBPS = bps
			lineVal := it.effectivePrice * int64(it.quantity)
			rawCommission += (lineVal * int64(bps)) / 10000
		}
		p.commissionVND = minInt64(maxInt64(0, rawCommission), sellerRevenue)

		// Payment fee C_pay = P * payment_fee_bps / 10000, seller-borne, capped so C_total = C_cat + C_pay <= R.
		p.paymentFeeBPS = paymentFeeBPS
		p.paymentFeeVND = ComputePaymentFeeVND(sellerRevenue, p.commissionVND, p.totalPayableVND, paymentFeeBPS)
	}
	return nil
}

func buildCheckoutBatch(
	checkoutGroupID, buyerID string,
	paymentMethod string,
	initialStatus orderv1.OrderStatus,
	initialPaymentStatus string,
	paymentExpiresAt *time.Time,
	addrJSON []byte,
	plans []*shopCheckoutPlan,
	salesChannel, storefrontShopID string,
	now time.Time,
	traceID, traceparent string,
) (store.CheckoutBatch, error) {
	batch := store.CheckoutBatch{
		CheckoutGroupID:     checkoutGroupID,
		Orders:              make([]store.NewOrderRecord, 0, len(plans)),
		PendingStockConfirm: initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
		Now:                 now,
	}

	statusStr := statemachine.StatusToString(initialStatus)
	methodStr := statemachine.NormalizePaymentMethod(paymentMethod)
	var confirmedAt *time.Time
	if initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		ts := now
		confirmedAt = &ts
	}

	for _, p := range plans {
		orderID := uuid.NewString()
		orderCode := generateOrderCode(now)

		orderParam := db.InsertOrderParams{
			ID:                         orderID,
			OrderCode:                  orderCode,
			CheckoutGroupID:            checkoutGroupID,
			BuyerID:                    buyerID,
			ShopID:                     p.shopID,
			Status:                     statusStr,
			PaymentMethod:              methodStr,
			PaymentID:                  "",
			PaymentStatus:              initialPaymentStatus,
			SubtotalVND:                p.subtotalVND,
			FlashSaleDiscountVND:       p.flashSaleDiscountVND,
			ShopProgramDiscountVND:     p.shopProgramDiscountVND,
			ShopVoucherDiscountVND:     p.shopVoucherDiscount,
			ShopFreeshipDiscountVND:    p.shopFreeshipDiscountVND,
			PlatformVoucherDiscountVND: p.platformVoucherShare,
			FreeshipDiscountVND:        p.freeshipDiscountShare,
			CoinsDiscountVND:           p.coinsDiscountShare,
			ShippingFeeVND:             p.shippingFeeVND,
			CommissionVND:              p.commissionVND,
			PaymentFeeVND:              p.paymentFeeVND,
			PaymentFeeBPS:              p.paymentFeeBPS,
			TotalPayableVND:            p.totalPayableVND,
			CarrierCode:                p.carrierCode,
			ServiceCode:                p.serviceCode,
			TrackingCode:               "",
			LabelURL:                   "",
			CancelReason:               "",
			ShippingAddressJSON:        addrJSON,
			PaymentExpiresAt:           paymentExpiresAt,
			ConfirmedAt:                confirmedAt,
			SalesChannel:               salesChannel,
			StorefrontShopID:           storefrontShopID,
		}

		itemParams := make([]db.InsertOrderItemParams, 0, len(p.items))
		for _, it := range p.items {
			itemWeight := it.weightGrams
			if itemWeight <= 0 {
				itemWeight = it.sku.GetWeightGrams()
			}
			itemParams = append(itemParams, db.InsertOrderItemParams{
				ID:                    uuid.NewString(),
				OrderID:               orderID,
				SKUID:                 it.sku.GetId(),
				ProductID:             it.sku.GetProductId(),
				ProductName:           it.sku.GetProductName(),
				SKUName:               it.sku.GetName(),
				UnitPriceVND:          it.sku.GetPriceVnd(),
				EffectiveUnitPriceVND: it.effectivePrice,
				Quantity:              it.quantity,
				LineTotalVND:          it.effectivePrice * int64(it.quantity),
				WeightGrams:           itemWeight,
				FlashSaleItemID:       it.flashSaleItemID,
				CommissionRateBPS:     it.commissionBPS,
				CategoryID:            it.sku.GetCategoryId(),
				ShopProgramItemID:     it.shopProgramItemID,
			})
		}

		initNote := "Order created awaiting payment"
		if initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			initNote = "Order created and confirmed (" + methodStr + ")"
		}
		timeline := []db.InsertTimelineEventParams{
			{
				ID:         uuid.NewString(),
				OrderID:    orderID,
				FromStatus: "UNSPECIFIED",
				ToStatus:   statusStr,
				ActorID:    buyerID,
				ActorRole:  "buyer",
				Note:       initNote,
			},
		}

		createdPayload, err := json.Marshal(events.OrderCreatedPayload{
			CheckoutGroupID:  checkoutGroupID,
			OrderID:          orderID,
			OrderCode:        orderCode,
			BuyerID:          buyerID,
			ShopID:           p.shopID,
			StorefrontShopID: storefrontShopID,
			PaymentMethod:    methodStr,
			TotalPayableVND:  p.totalPayableVND,
		})
		if err != nil {
			return store.CheckoutBatch{}, err
		}

		outbox := []db.InsertOutboxEventParams{
			{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderCreated,
				TraceID:     traceID,
				Traceparent: traceparent,
				PayloadJSON: createdPayload,
			},
		}

		if initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			confirmedPayload, err := json.Marshal(events.OrderConfirmedPayload{
				OrderID:          orderID,
				OrderCode:        orderCode,
				BuyerID:          buyerID,
				ShopID:           p.shopID,
				StorefrontShopID: storefrontShopID,
				PaymentMethod:    methodStr,
				TotalPayableVND:  p.totalPayableVND,
			})
			if err != nil {
				return store.CheckoutBatch{}, err
			}
			outbox = append(outbox, db.InsertOutboxEventParams{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderConfirmed,
				TraceID:     traceID,
				Traceparent: traceparent,
				PayloadJSON: confirmedPayload,
			})
		}

		batch.Orders = append(batch.Orders, store.NewOrderRecord{
			Order:    orderParam,
			Items:    itemParams,
			Timeline: timeline,
			Outbox:   outbox,
		})
	}

	return batch, nil
}

func buildShopAllocation(ord db.Order) *paymentv1.ShopAllocation {
	sellerRevenue := computeSellerRevenue(ord)
	ship := maxInt64(0, ord.ShippingFeeVND)
	platformDiscount := maxInt64(0, sellerRevenue+ship-ord.TotalPayableVND)
	commTotal := minInt64(maxInt64(0, ord.CommissionVND)+maxInt64(0, ord.PaymentFeeVND), sellerRevenue)
	return &paymentv1.ShopAllocation{
		OrderId:             ord.ID,
		ShopId:              ord.ShopID,
		SubtotalVnd:         sellerRevenue,
		CommissionVnd:       commTotal,
		ShippingFeeVnd:      ship,
		PlatformDiscountVnd: platformDiscount,
		SalesChannel:        effectiveSalesChannel(ord.SalesChannel),
		StorefrontShopId:    ord.StorefrontShopID,
	}
}

func buildCreatePaymentRequest(
	checkoutGroupID, buyerID string,
	totalPayableVND int64,
	method string,
	expiresAt *time.Time,
	aggs []store.OrderAggregate,
	salesChannel, storefrontShopID string,
) (*paymentv1.CreatePaymentRequest, error) {
	payMethod, err := paymentProtoMethod(method)
	if err != nil {
		return nil, err
	}

	allocs := make([]*paymentv1.ShopAllocation, 0, len(aggs))
	for _, agg := range aggs {
		allocs = append(allocs, buildShopAllocation(agg.Order))
	}

	req := &paymentv1.CreatePaymentRequest{
		OrderGroupId:     checkoutGroupID,
		OrderId:          checkoutGroupID,
		UserId:           buyerID,
		AmountVnd:        totalPayableVND,
		Method:           payMethod,
		ShopAllocations:  allocs,
		IdempotencyKey:   "checkout:" + checkoutGroupID,
		SalesChannel:     effectiveSalesChannel(salesChannel),
		StorefrontShopId: storefrontShopID,
	}
	if expiresAt != nil {
		req.ExpiresAt = timestamppb.New(*expiresAt)
	}
	return req, nil
}

func paymentStatusToString(ps paymentv1.PaymentStatus, fallback string) string {
	switch ps {
	case paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING:
		return "PENDING"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED:
		return "SUCCEEDED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED:
		return "FAILED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED:
		return "EXPIRED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED:
		return "CANCELLED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED:
		return "REFUNDED"
	default:
		return fallback
	}
}

func (s *OrderService) releaseStockErr(ctx context.Context, checkoutGroupID string) (bool, error) {
	if s.clients.Product == nil || strings.TrimSpace(checkoutGroupID) == "" {
		return false, nil
	}
	var released bool
	err := clients.RetryIdempotent(ctx, 3, func(c context.Context) error {
		resp, rErr := s.clients.Product.ReleaseStock(c, &productv1.ReleaseStockRequest{OrderId: checkoutGroupID})
		if rErr != nil {
			if status.Code(rErr) == codes.NotFound {
				released = false
				return nil
			}
			return rErr
		}
		released = resp.GetReleased()
		return nil
	})
	return released, err
}

func (s *OrderService) releasePromotionErr(ctx context.Context, checkoutGroupID string) error {
	if s.clients.Promotion == nil || strings.TrimSpace(checkoutGroupID) == "" {
		return nil
	}
	return clients.RetryIdempotent(ctx, 3, func(c context.Context) error {
		_, err := s.clients.Promotion.ReleasePromotion(c, &promotionv1.ReleasePromotionRequest{OrderId: checkoutGroupID})
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return err
	})
}

func (s *OrderService) releasePromotionShopErr(ctx context.Context, checkoutGroupID, shopID, subOrderID, reason string) error {
	return s.releasePromotionShopWithOpts(ctx, checkoutGroupID, shopID, subOrderID, reason, false)
}

func (s *OrderService) clawbackCompletedOrderPromotionErr(ctx context.Context, checkoutGroupID, shopID, subOrderID, reason string) error {
	return s.releasePromotionShopWithOpts(ctx, fallbackStr(strings.TrimSpace(checkoutGroupID), strings.TrimSpace(subOrderID)), shopID, subOrderID, reason, true)
}

func (s *OrderService) releasePromotionShopWithOpts(ctx context.Context, checkoutGroupID, shopID, subOrderID, reason string, clawbackOnly bool) error {
	if s.clients.Promotion == nil || strings.TrimSpace(checkoutGroupID) == "" || strings.TrimSpace(shopID) == "" {
		return nil
	}
	return clients.RetryIdempotent(ctx, 3, func(c context.Context) error {
		_, err := s.clients.Promotion.ReleasePromotionShop(c, &promotionv1.ReleasePromotionShopRequest{
			OrderId:      checkoutGroupID,
			ShopId:       shopID,
			SubOrderId:   strings.TrimSpace(subOrderID),
			Reason:       reason,
			ClawbackOnly: clawbackOnly,
		})
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return err
	})
}

func (s *OrderService) confirmGroupStockAndClear(ctx context.Context, checkoutGroupID string) error {
	if s.clients.Product == nil || strings.TrimSpace(checkoutGroupID) == "" {
		return nil
	}
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
		_, cErr := s.clients.Product.ConfirmStock(c, &productv1.ConfirmStockRequest{OrderId: checkoutGroupID})
		return cErr
	})
	if err != nil {
		// P2-5: Reservation already released by a concurrent cancel -> skip cleanly and clear flag.
		if status.Code(err) == codes.FailedPrecondition {
			if released, relErr := s.releaseStockErr(compCtx, checkoutGroupID); relErr == nil && released {
				_ = s.store.ClearCheckoutGroupPendingStockConfirm(compCtx, checkoutGroupID)
				return nil
			}
		}
		// P2-5: Legacy/seeded order with no stock_reservations row -> warn and clear flag so consumer/watchdog don't spin.
		if status.Code(err) == codes.NotFound {
			s.logger.WarnContext(ctx, "ConfirmStock returned NotFound (no reservation row); clearing pending_stock_confirm",
				"checkout_group_id", checkoutGroupID)
			_ = s.store.ClearCheckoutGroupPendingStockConfirm(compCtx, checkoutGroupID)
			return nil
		}
		return err
	}
	if clrErr := s.store.ClearCheckoutGroupPendingStockConfirm(compCtx, checkoutGroupID); clrErr != nil {
		return clrErr
	}
	return nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
