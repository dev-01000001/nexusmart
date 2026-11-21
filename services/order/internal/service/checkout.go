package service

import (
	"context"
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
	sku             *productv1.SKU
	quantity        int32
	flashSaleItemID string
	effectivePrice  int64
}

type shopCheckoutPlan struct {
	shopID                string
	items                 []resolvedLineItem
	subtotalVND           int64
	weightGrams           int32
	carrierCode           string
	serviceCode           string
	shippingFeeVND        int64
	flashSaleDiscountVND  int64
	shopVoucherDiscount   int64
	platformVoucherShare  int64
	freeshipDiscountShare int64
	coinsDiscountShare    int64
	commissionVND         int64
	totalPayableVND       int64
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
	if !caller.IsPrivileged && req.GetBuyerId() != "" && req.GetBuyerId() != buyerID {
		return nil, status.Error(codes.PermissionDenied, "cannot checkout on behalf of another buyer")
	}
	if caller.IsPrivileged && strings.TrimSpace(req.GetBuyerId()) != "" {
		buyerID = strings.TrimSpace(req.GetBuyerId())
	}

	addressID := strings.TrimSpace(req.GetAddressId())
	if addressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id is required")
	}

	if req.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
		req.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
		req.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
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

	// Fast path: check for already completed idempotent checkout without blocking.
	if replay, err := s.tryLoadIdempotentReplay(ctx, buyerID, idemKey); err == nil && replay != nil {
		return replay, nil
	}

	// Serialize concurrent in-process duplicate requests for the same (buyer_id, idempotency_key).
	unlock := s.idemLocks.Lock(buyerID + ":" + idemKey)
	defer unlock()

	checkoutGroupID, replay, err := s.acquireCheckoutGroup(ctx, buyerID, idemKey, req.GetPaymentMethod(), salesChannel, storefrontShopID)
	if err != nil {
		return nil, err
	}
	if replay != nil {
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
	valResp, err := s.clients.User.ValidateUser(ctx, &userv1.ValidateUserRequest{UserId: buyerID})
	if err != nil {
		failGroupBeforePersist("USER_VALIDATION_FAILED")
		return nil, err
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
		return nil, err
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

	addrSnapshot := &orderv1.OrderAddressSnapshot{
		RecipientName:  addr.GetRecipientName(),
		RecipientPhone: addr.GetRecipientPhone(),
		ProvinceCode:   addr.GetProvinceCode(),
		ProvinceName:   addr.GetProvinceName(),
		WardCode:       addr.GetWardCode(),
		WardName:       addr.GetWardName(),
		StreetAddress:  addr.GetStreetAddress(),
	}

	// For STOREFRONT orders, verify storefront_shop_id is an ACTIVE shop BEFORE touching stock.
	if salesChannel == SalesChannelStorefront {
		shopResp, sErr := s.clients.User.GetShop(ctx, &userv1.GetShopRequest{ShopId: storefrontShopID})
		if sErr != nil {
			if status.Code(sErr) == codes.NotFound {
				failGroupBeforePersist("STOREFRONT_SHOP_NOT_FOUND")
				return nil, status.Errorf(codes.InvalidArgument, "STOREFRONT_SHOP_INACTIVE: storefront shop %s not found or not active", storefrontShopID)
			}
			failGroupBeforePersist("STOREFRONT_SHOP_LOOKUP_FAILED")
			return nil, sErr
		}
		if shopResp.GetShop() == nil || shopResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
			failGroupBeforePersist("STOREFRONT_SHOP_INACTIVE")
			return nil, status.Errorf(codes.InvalidArgument, "STOREFRONT_SHOP_INACTIVE: storefront shop %s is not active", storefrontShopID)
		}
	}

	// Step B: Resolve items from cart or explicit items[] and fetch authoritative SKU details.
	requestedItems, err := s.resolveCheckoutInputs(ctx, buyerID, req, salesChannel, storefrontShopID)
	if err != nil {
		failGroupBeforePersist("ITEMS_RESOLVE_FAILED")
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

	plans, allStockItems, flashSaleItems, err := buildShopPlans(requestedItems, skuByID)
	if err != nil {
		failGroupBeforePersist("INVALID_ITEMS")
		return nil, err
	}

	// For STOREFRONT orders, EVERY line item must belong to storefront_shop_id (checked BEFORE stock reservation).
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

	// Step C: Calculate authoritative shipping fee per shop.
	if err := s.calculateShippingFees(ctx, plans, req.GetShippingChoices(), addrSnapshot, req.GetPaymentMethod()); err != nil {
		failGroupBeforePersist("SHIPPING_FEE_FAILED")
		return nil, err
	}

	// Step D: Early COD risk checks (UC-PAY02).
	hasPromotions := len(req.GetShopVoucherIds()) > 0 ||
		strings.TrimSpace(req.GetPlatformVoucherId()) != "" ||
		strings.TrimSpace(req.GetFreeshipVoucherId()) != "" ||
		req.GetCoinsToUse() > 0 ||
		len(flashSaleItems) > 0

	if req.GetPaymentMethod() == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
		if valResp.GetCodRejectedCount() >= s.cfg.CODMaxRejectedCount {
			failGroupBeforePersist("COD_REJECTED_LIMIT_EXCEEDED")
			return nil, status.Errorf(
				codes.FailedPrecondition,
				"COD_REJECTED_LIMIT_EXCEEDED: account has %d rejected COD deliveries (limit %d)",
				valResp.GetCodRejectedCount(),
				s.cfg.CODMaxRejectedCount,
			)
		}
		if !hasPromotions {
			var preTotal int64
			for _, p := range plans {
				preTotal += p.subtotalVND + p.shippingFeeVND
			}
			if preTotal > s.cfg.CODMaxVND {
				failGroupBeforePersist("COD_LIMIT_EXCEEDED")
				return nil, status.Errorf(
					codes.FailedPrecondition,
					"COD_LIMIT_EXCEEDED: order total %d VND exceeds COD maximum %d VND",
					preTotal,
					s.cfg.CODMaxVND,
				)
			}
		}
	}

	// Saga Steps 1 & 2: Reserve Stock and Reserve Promotions.
	// For flash-sale orders, gate via Redis Lua (ReservePromotion) BEFORE touching Product DB
	// so 1,000 concurrent requests competing for 100 slots reject the 900 losers in memory with
	// zero Product DB contention and zero leaked stock.
	promoReserved := false
	var breakdown *promotionv1.DiscountBreakdown
	if len(flashSaleItems) > 0 {
		promoReq := buildReservePromotionRequest(checkoutGroupID, buyerID, req, plans, flashSaleItems, salesChannel, storefrontShopID)
		promoResp, pErr := s.clients.Promotion.ReservePromotion(ctx, promoReq)
		if pErr != nil {
			failGroupBeforePersist("PROMOTION_RESERVE_FAILED")
			return nil, pErr
		}
		promoReserved = true
		breakdown = promoResp.GetBreakdown()

		if _, err := s.clients.Product.ReserveStock(ctx, &productv1.ReserveStockRequest{
			OrderId: checkoutGroupID,
			Items:   allStockItems,
		}); err != nil {
			s.compensateReleasePromotion(ctx, checkoutGroupID)
			failGroupBeforePersist("STOCK_RESERVE_FAILED")
			return nil, err
		}
	} else {
		// Regular order: Reserve Stock first (atomic optimistic locking, idempotent by checkoutGroupID).
		if _, err := s.clients.Product.ReserveStock(ctx, &productv1.ReserveStockRequest{
			OrderId: checkoutGroupID,
			Items:   allStockItems,
		}); err != nil {
			failGroupBeforePersist("STOCK_RESERVE_FAILED")
			return nil, err
		}

		if hasPromotions {
			promoReq := buildReservePromotionRequest(checkoutGroupID, buyerID, req, plans, flashSaleItems, salesChannel, storefrontShopID)
			promoResp, pErr := s.clients.Promotion.ReservePromotion(ctx, promoReq)
			if pErr != nil {
				s.compensateReleaseStock(ctx, checkoutGroupID)
				failGroupBeforePersist("PROMOTION_RESERVE_FAILED")
				return nil, pErr
			}
			promoReserved = true
			breakdown = promoResp.GetBreakdown()
		}
	}

	// Resolve exact flash sale prices if multiple flash sale items exist in a shop.
	flashPriceByItemID := s.lookupFlashSalePrices(ctx, flashSaleItems)
	applyDiscountsAndTotals(plans, breakdown, flashPriceByItemID, s.cfg.DefaultCommissionBPS)

	var grandTotalPayableVND int64
	for _, p := range plans {
		grandTotalPayableVND += p.totalPayableVND
	}

	// Post-promotion COD max check (for orders where discounts might still leave total > COD limit).
	if req.GetPaymentMethod() == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD && grandTotalPayableVND > s.cfg.CODMaxVND {
		if promoReserved {
			s.compensateReleasePromotion(ctx, checkoutGroupID)
		}
		s.compensateReleaseStock(ctx, checkoutGroupID)
		failGroupBeforePersist("COD_LIMIT_EXCEEDED")
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"COD_LIMIT_EXCEEDED: order total %d VND exceeds COD maximum %d VND",
			grandTotalPayableVND,
			s.cfg.CODMaxVND,
		)
	}

	// Saga Step 3: Persist Checkout Group Sub-Orders + Order Items + Timeline + Outbox in ONE TX.
	now := s.nowFn()
	var paymentExpiresAt *time.Time
	initialStatus := orderv1.OrderStatus_ORDER_STATUS_PENDING
	initialPaymentStatus := "PENDING"
	if req.GetPaymentMethod() == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD || grandTotalPayableVND == 0 {
		initialStatus = orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
		initialPaymentStatus = "COD_PENDING"
		if grandTotalPayableVND == 0 {
			initialPaymentStatus = "SUCCEEDED"
		}
	} else {
		exp := now.Add(time.Duration(s.cfg.PaymentExpiryMinutes) * time.Minute)
		paymentExpiresAt = &exp
	}

	addrJSON, err := json.Marshal(addrSnapshot)
	if err != nil {
		if promoReserved {
			s.compensateReleasePromotion(ctx, checkoutGroupID)
		}
		s.compensateReleaseStock(ctx, checkoutGroupID)
		failGroupBeforePersist("ADDRESS_MARSHAL_FAILED")
		return nil, status.Errorf(codes.Internal, "marshal shipping address: %v", err)
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	batch, err := buildCheckoutBatch(
		checkoutGroupID,
		buyerID,
		req.GetPaymentMethod(),
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
		if promoReserved {
			s.compensateReleasePromotion(ctx, checkoutGroupID)
		}
		s.compensateReleaseStock(ctx, checkoutGroupID)
		failGroupBeforePersist("BATCH_BUILD_FAILED")
		return nil, status.Errorf(codes.Internal, "prepare checkout batch: %v", err)
	}

	persistedAggs, err := s.store.PersistCheckoutBatch(ctx, batch)
	if err != nil {
		if promoReserved {
			s.compensateReleasePromotion(ctx, checkoutGroupID)
		}
		s.compensateReleaseStock(ctx, checkoutGroupID)
		failGroupBeforePersist("DB_PERSIST_FAILED")
		return nil, status.Errorf(codes.Internal, "persist checkout orders: %v", err)
	}
	ordersPersisted = true

	// Saga Step 4: Create Payment via PaymentService.
	var paymentID, paymentStatus, qrURL, qrCode string
	paymentStatus = initialPaymentStatus
	if grandTotalPayableVND > 0 {
		payReq := buildCreatePaymentRequest(
			checkoutGroupID,
			buyerID,
			grandTotalPayableVND,
			req.GetPaymentMethod(),
			paymentExpiresAt,
			persistedAggs,
			salesChannel,
			storefrontShopID,
		)
		payResp, payErr := s.clients.Payment.CreatePayment(ctx, payReq)
		if payErr != nil {
			// Full Saga compensation: cancel persisted orders, release promotions, release stock.
			compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			defer cancel()
			_, cancelErr := s.store.CancelCheckoutGroupOrders(compCtx, store.CancelGroupOrdersParams{
				CheckoutGroupID: checkoutGroupID,
				Reason:          "Payment initialization failed",
				ActorID:         "order-service",
				ActorRole:       "system",
				PaymentStatus:   "FAILED",
				TraceID:         traceID,
				Traceparent:     traceparent,
			})
			if cancelErr != nil {
				s.logger.ErrorContext(ctx, "failed to cancel sub-orders during payment failure compensation",
					"checkout_group_id", checkoutGroupID, "error", cancelErr)
			}
			if promoReserved {
				s.compensateReleasePromotion(ctx, checkoutGroupID)
			}
			s.compensateReleaseStock(ctx, checkoutGroupID)
			s.notifyOutbox()
			return nil, payErr
		}
		if pay := payResp.GetPayment(); pay != nil {
			paymentID = pay.GetPaymentId()
			paymentStatus = paymentStatusToString(pay.GetStatus(), initialPaymentStatus)
			qrURL = pay.GetQrUrl()
			qrCode = pay.GetQrCode()
			if pay.GetExpiresAt() != nil {
				exp := pay.GetExpiresAt().AsTime().UTC()
				paymentExpiresAt = &exp
			}
		}
	}

	// For COD (or zero-payable), stock is immediately confirmed because sub-orders start CONFIRMED.
	if initialStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		s.confirmStockBestEffort(ctx, checkoutGroupID)
	}

	// Build final response & persist snapshot atomically into checkout_groups.
	protoOrders := make([]*orderv1.Order, 0, len(persistedAggs))
	for i := range persistedAggs {
		persistedAggs[i].Order.PaymentID = paymentID
		persistedAggs[i].Order.PaymentStatus = paymentStatus
		if paymentExpiresAt != nil {
			persistedAggs[i].Order.PaymentExpiresAt = paymentExpiresAt
		}
		protoOrders = append(protoOrders, aggregateToProto(persistedAggs[i]))
	}

	resp := &orderv1.CreateCheckoutResponse{
		CheckoutGroupId:  checkoutGroupID,
		Orders:           protoOrders,
		PaymentId:        paymentID,
		PaymentMethod:    req.GetPaymentMethod(),
		PaymentStatus:    paymentStatus,
		QrUrl:            qrURL,
		QrCode:           qrCode,
		TotalPayableVnd:  grandTotalPayableVND,
		IdempotentReplay: false,
		SalesChannel:     salesChannel,
		StorefrontShopId: storefrontShopID,
	}
	if paymentExpiresAt != nil {
		resp.PaymentExpiresAt = timestamppb.New(*paymentExpiresAt)
	}

	snapshotBytes, err := protojson.Marshal(resp)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to marshal checkout response snapshot", "checkout_group_id", checkoutGroupID, "error", err)
		snapshotBytes = []byte("{}")
	}

	if err := s.store.CompleteCheckoutWithPayment(ctx, store.UpdateCheckoutPaymentParams{
		CheckoutGroupID:      checkoutGroupID,
		PaymentID:            paymentID,
		PaymentStatus:        paymentStatus,
		QRURL:                qrURL,
		QRCode:               qrCode,
		PaymentExpiresAt:     paymentExpiresAt,
		TotalPayableVND:      grandTotalPayableVND,
		ResponseSnapshotJSON: snapshotBytes,
	}); err != nil {
		s.logger.ErrorContext(ctx, "failed to finalize checkout group payment info", "checkout_group_id", checkoutGroupID, "error", err)
	}

	// Saga Step 5: Best-effort ClearCart for ordered SKUs (scoped to storefront shop when STOREFRONT).
	if s.clients.Cart != nil {
		if _, cErr := s.clients.Cart.ClearItems(ctx, &cartv1.ClearItemsRequest{
			UserId: buyerID,
			SkuIds: skuIDs,
			ShopId: storefrontShopID,
		}); cErr != nil {
			s.logger.WarnContext(ctx, "best-effort ClearItems failed after checkout", "buyer_id", buyerID, "error", cErr)
		}
	}

	s.notifyOutbox()
	return resp, nil
}

func (s *OrderService) tryLoadIdempotentReplay(ctx context.Context, buyerID, idemKey string) (*orderv1.CreateCheckoutResponse, error) {
	group, err := s.store.GetActiveCheckoutGroup(ctx, buyerID, idemKey)
	if err != nil {
		return nil, err
	}
	if group.Status != "COMPLETED" {
		return nil, nil
	}
	return s.buildReplayResponse(ctx, group)
}

func (s *OrderService) acquireCheckoutGroup(ctx context.Context, buyerID, idemKey string, method orderv1.OrderPaymentMethod, salesChannel, storefrontShopID string) (string, *orderv1.CreateCheckoutResponse, error) {
	const maxWait = 15 * time.Second
	deadline := time.Now().Add(maxWait)

	for {
		existing, err := s.store.GetActiveCheckoutGroup(ctx, buyerID, idemKey)
		if err == nil && existing != nil {
			if existing.Status == "COMPLETED" {
				replay, rErr := s.buildReplayResponse(ctx, existing)
				return "", replay, rErr
			}
			// Another pod is currently executing this idempotency key.
			if time.Now().After(deadline) {
				return "", nil, status.Error(codes.Aborted, "concurrent checkout with same idempotency_key is still in progress")
			}
			select {
			case <-ctx.Done():
				return "", nil, status.FromContextError(ctx.Err()).Err()
			case <-time.After(50 * time.Millisecond):
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
			Status:           "IN_PROGRESS",
			PaymentMethod:    statemachine.PaymentMethodToString(method),
			TotalPayableVND:  0,
			SalesChannel:     salesChannel,
			StorefrontShopID: storefrontShopID,
		})
		if err == nil {
			return groupID, nil, nil
		}
		if errors.Is(err, store.ErrDuplicateIdempotencyKey) {
			if time.Now().After(deadline) {
				return "", nil, status.Error(codes.Aborted, "concurrent checkout conflict")
			}
			select {
			case <-ctx.Done():
				return "", nil, status.FromContextError(ctx.Err()).Err()
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		return "", nil, mapStoreError(err)
	}
}

func (s *OrderService) buildReplayResponse(ctx context.Context, group *db.CheckoutGroup) (*orderv1.CreateCheckoutResponse, error) {
	groupChannel := effectiveSalesChannel(group.SalesChannel)
	if len(group.ResponseSnapshotJSON) > 2 {
		var resp orderv1.CreateCheckoutResponse
		if err := protojson.Unmarshal(group.ResponseSnapshotJSON, &resp); err == nil && resp.CheckoutGroupId != "" {
			resp.IdempotentReplay = true
			if resp.SalesChannel == "" {
				resp.SalesChannel = groupChannel
			}
			if resp.StorefrontShopId == "" {
				resp.StorefrontShopId = group.StorefrontShopID
			}
			for _, ord := range resp.Orders {
				if ord != nil && ord.SalesChannel == "" {
					ord.SalesChannel = groupChannel
				}
			}
			return &resp, nil
		}
	}

	aggs, err := s.store.ListOrdersByCheckoutGroup(ctx, group.ID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	orders := make([]*orderv1.Order, 0, len(aggs))
	for _, agg := range aggs {
		orders = append(orders, aggregateToProto(agg))
	}
	resp := &orderv1.CreateCheckoutResponse{
		CheckoutGroupId:  group.ID,
		Orders:           orders,
		PaymentId:        group.PaymentID,
		PaymentMethod:    statemachine.StringToPaymentMethod(group.PaymentMethod),
		PaymentStatus:    group.PaymentStatus,
		QrUrl:            group.QRURL,
		QrCode:           group.QRCode,
		TotalPayableVnd:  group.TotalPayableVND,
		IdempotentReplay: true,
		SalesChannel:     groupChannel,
		StorefrontShopId: group.StorefrontShopID,
	}
	if group.PaymentExpiresAt != nil {
		resp.PaymentExpiresAt = timestamppb.New(*group.PaymentExpiresAt)
	}
	return resp, nil
}

func (s *OrderService) resolveCheckoutInputs(ctx context.Context, buyerID string, req *orderv1.CreateCheckoutRequest, salesChannel, storefrontShopID string) ([]*orderv1.CheckoutItemInput, error) {
	merged := make(map[string]*orderv1.CheckoutItemInput)

	if req.GetFromCart() {
		cartReq := &cartv1.GetCartRequest{UserId: buyerID}
		if salesChannel == SalesChannelStorefront {
			cartReq.ShopId = storefrontShopID
		}
		cartResp, err := s.clients.Cart.GetCart(ctx, cartReq)
		if err != nil {
			return nil, err
		}
		cart := cartResp.GetCart()
		if cart == nil || len(cart.GetShopGroups()) == 0 {
			return nil, status.Error(codes.FailedPrecondition, "shopping cart is empty")
		}

		// Map any flash_sale_item_id overrides supplied alongside from_cart=true.
		flashOverrides := make(map[string]string, len(req.GetItems()))
		for _, it := range req.GetItems() {
			if it.GetSkuId() != "" && it.GetFlashSaleItemId() != "" {
				flashOverrides[it.GetSkuId()] = it.GetFlashSaleItemId()
			}
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
				if ci.GetSelected() {
					anySelected = true
					break
				}
			}
		}

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
		if len(merged) == 0 && salesChannel == SalesChannelStorefront {
			return nil, status.Errorf(codes.FailedPrecondition, "shopping cart has no items for storefront shop %s", storefrontShopID)
		}
	} else {
		for _, it := range req.GetItems() {
			skuID := strings.TrimSpace(it.GetSkuId())
			if skuID == "" || it.GetQuantity() <= 0 {
				return nil, status.Error(codes.InvalidArgument, "each checkout item must have sku_id and positive quantity")
			}
			if prev, ok := merged[skuID]; ok {
				prev.Quantity += it.GetQuantity()
				if prev.FlashSaleItemId == "" {
					prev.FlashSaleItemId = strings.TrimSpace(it.GetFlashSaleItemId())
				}
			} else {
				merged[skuID] = &orderv1.CheckoutItemInput{
					SkuId:           skuID,
					Quantity:        it.GetQuantity(),
					FlashSaleItemId: strings.TrimSpace(it.GetFlashSaleItemId()),
				}
			}
		}
	}

	if len(merged) == 0 {
		return nil, status.Error(codes.InvalidArgument, "checkout must contain at least one item")
	}

	out := make([]*orderv1.CheckoutItemInput, 0, len(merged))
	for _, it := range merged {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkuId < out[j].SkuId })
	return out, nil
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
		if reqItem.GetFlashSaleItemId() == "" {
			if sku.GetAvailableStock() < reqItem.GetQuantity() {
				return nil, nil, nil, status.Errorf(
					codes.FailedPrecondition,
					"INSUFFICIENT_STOCK: SKU %s has %d available, requested %d",
					sku.GetId(),
					sku.GetAvailableStock(),
					reqItem.GetQuantity(),
				)
			}
		} else if sku.GetAvailableStock()+sku.GetFlashSaleReservedStock() < reqItem.GetQuantity() {
			return nil, nil, nil, status.Errorf(
				codes.FailedPrecondition,
				"INSUFFICIENT_STOCK: SKU %s has %d flash-sale/available stock, requested %d",
				sku.GetId(),
				sku.GetAvailableStock()+sku.GetFlashSaleReservedStock(),
				reqItem.GetQuantity(),
			)
		}

		plan, exists := byShop[sku.GetShopId()]
		if !exists {
			plan = &shopCheckoutPlan{shopID: sku.GetShopId()}
			byShop[sku.GetShopId()] = plan
		}

		weight := sku.GetWeightGrams()
		if weight <= 0 {
			weight = 500
		}

		plan.items = append(plan.items, resolvedLineItem{
			sku:             sku,
			quantity:        reqItem.GetQuantity(),
			flashSaleItemID: reqItem.GetFlashSaleItemId(),
			effectivePrice:  sku.GetPriceVnd(),
		})
		plan.subtotalVND += sku.GetPriceVnd() * int64(reqItem.GetQuantity())
		plan.weightGrams += weight * reqItem.GetQuantity()

		stockItems = append(stockItems, &productv1.StockReservationItem{
			SkuId:           sku.GetId(),
			Quantity:        reqItem.GetQuantity(),
			FlashSaleItemId: reqItem.GetFlashSaleItemId(),
		})

		if reqItem.GetFlashSaleItemId() != "" {
			flashItems = append(flashItems, &promotionv1.FlashSaleReservationItem{
				FlashSaleItemId: reqItem.GetFlashSaleItemId(),
				SkuId:           sku.GetId(),
				Quantity:        reqItem.GetQuantity(),
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
	paymentMethod orderv1.OrderPaymentMethod,
) error {
	choiceByShop := make(map[string]*orderv1.ShopShippingChoice, len(choices))
	for _, c := range choices {
		if c != nil && c.GetShopId() != "" {
			choiceByShop[c.GetShopId()] = c
		}
	}

	for _, plan := range plans {
		fromProvince, fromWard := s.resolveShopPickupCodes(ctx, plan.shopID, toAddr.GetProvinceCode(), toAddr.GetWardCode())
		var codEstimate int64
		if paymentMethod == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
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
			return fmt.Errorf("calculate shipping fee for shop %s: %w", plan.shopID, err)
		}
		quotes := feeResp.GetQuotes()
		if len(quotes) == 0 {
			return status.Errorf(codes.FailedPrecondition, "no shipping quotes available for shop %s", plan.shopID)
		}

		chosen := pickShippingQuote(quotes, choiceByShop[plan.shopID])
		plan.carrierCode = chosen.GetCarrierCode()
		plan.serviceCode = chosen.GetServiceCode()
		plan.shippingFeeVND = chosen.GetFeeVnd()
	}
	return nil
}

func (s *OrderService) resolveShopPickupCodes(ctx context.Context, shopID, fallbackProvince, fallbackWard string) (string, string) {
	if s.clients.User == nil {
		return fallbackProvince, fallbackWard
	}
	shopResp, err := s.clients.User.GetShop(ctx, &userv1.GetShopRequest{ShopId: shopID})
	if err != nil || shopResp.GetShop() == nil {
		return fallbackProvince, fallbackWard
	}
	shop := shopResp.GetShop()
	if shop.GetPickupAddressId() == "" || shop.GetOwnerUserId() == "" {
		return fallbackProvince, fallbackWard
	}
	pickupResp, err := s.clients.User.GetAddress(clients.WithInternalAdminContext(ctx), &userv1.GetAddressRequest{
		UserId:    shop.GetOwnerUserId(),
		AddressId: shop.GetPickupAddressId(),
	})
	if err != nil || pickupResp.GetAddress() == nil {
		return fallbackProvince, fallbackWard
	}
	pAddr := pickupResp.GetAddress()
	prov := pAddr.GetProvinceCode()
	ward := pAddr.GetWardCode()
	if prov == "" {
		prov = fallbackProvince
	}
	if ward == "" {
		ward = fallbackWard
	}
	return prov, ward
}

func pickShippingQuote(quotes []*shippingv1.CarrierQuote, choice *orderv1.ShopShippingChoice) *shippingv1.CarrierQuote {
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
	salesChannel, storefrontShopID string,
) *promotionv1.ReservePromotionRequest {
	shopInputs := make([]*promotionv1.ShopDiscountInput, 0, len(plans))
	for _, p := range plans {
		lineItems := make([]*promotionv1.DiscountLineItem, 0, len(p.items))
		for _, it := range p.items {
			lineItems = append(lineItems, &promotionv1.DiscountLineItem{
				SkuId:           it.sku.GetId(),
				Quantity:        it.quantity,
				UnitPriceVnd:    it.sku.GetPriceVnd(),
				FlashSaleItemId: it.flashSaleItemID,
			})
		}
		shopInputs = append(shopInputs, &promotionv1.ShopDiscountInput{
			ShopId:         p.shopID,
			SubtotalVnd:    p.subtotalVND,
			ShippingFeeVnd: p.shippingFeeVND,
			Items:          lineItems,
		})
	}

	return &promotionv1.ReservePromotionRequest{
		OrderId:           checkoutGroupID,
		UserId:            buyerID,
		ShopVoucherIds:    req.GetShopVoucherIds(),
		PlatformVoucherId: req.GetPlatformVoucherId(),
		FreeshipVoucherId: req.GetFreeshipVoucherId(),
		CoinsToUse:        req.GetCoinsToUse(),
		FlashSaleItems:    flashItems,
		ShopItems:         shopInputs,
		SalesChannel:      salesChannel,
		StorefrontShopId:  storefrontShopID,
	}
}

func (s *OrderService) lookupFlashSalePrices(ctx context.Context, flashItems []*promotionv1.FlashSaleReservationItem) map[string]int64 {
	prices := make(map[string]int64)
	if len(flashItems) == 0 || s.clients.Promotion == nil {
		return prices
	}
	resp, err := s.clients.Promotion.ListActiveFlashSales(ctx, &promotionv1.ListActiveFlashSalesRequest{IncludeUpcoming: false})
	if err != nil {
		return prices
	}
	for _, fs := range resp.GetFlashSales() {
		for _, it := range fs.GetItems() {
			if it.GetId() != "" && it.GetFlashPriceVnd() > 0 {
				prices[it.GetId()] = it.GetFlashPriceVnd()
			}
		}
	}
	return prices
}

func applyDiscountsAndTotals(
	plans []*shopCheckoutPlan,
	breakdown *promotionv1.DiscountBreakdown,
	flashPrices map[string]int64,
	defaultCommissionBPS int32,
) {
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
				p.shopVoucherDiscount = maxInt64(0, sb.GetShopVoucherDiscountVnd())
				p.platformVoucherShare = maxInt64(0, sb.GetPlatformVoucherDiscountVnd())
				p.freeshipDiscountShare = maxInt64(0, sb.GetFreeshipDiscountVnd())
				p.coinsDiscountShare = maxInt64(0, sb.GetCoinsDiscountVnd())
			}
		}
	} else if breakdown != nil {
		// Fallback proportional allocation if PromotionService only populated aggregate totals.
		subWeights := make([]int64, len(plans))
		shipWeights := make([]int64, len(plans))
		for i, p := range plans {
			subWeights[i] = p.subtotalVND
			shipWeights[i] = p.shippingFeeVND
		}
		flashShares := AllocateProportionalVND(breakdown.GetTotalFlashSaleDiscountVnd(), subWeights)
		shopShares := AllocateProportionalVND(breakdown.GetTotalShopVoucherDiscountVnd(), subWeights)
		platShares := AllocateProportionalVND(breakdown.GetTotalPlatformVoucherDiscountVnd(), subWeights)
		freeShares := AllocateProportionalVND(breakdown.GetTotalFreeshipDiscountVnd(), shipWeights)
		coinShares := AllocateProportionalVND(breakdown.GetTotalCoinsDiscountVnd(), subWeights)
		for i, p := range plans {
			p.flashSaleDiscountVND = flashShares[i]
			p.shopVoucherDiscount = shopShares[i]
			p.platformVoucherShare = platShares[i]
			p.freeshipDiscountShare = freeShares[i]
			p.coinsDiscountShare = coinShares[i]
		}
	}

	for _, p := range plans {
		// Apply flash sale effective unit prices to line items.
		var flashItemsIdx []int
		for idx := range p.items {
			if p.items[idx].flashSaleItemID != "" {
				flashItemsIdx = append(flashItemsIdx, idx)
			}
		}
		var computedFlashDiscount int64
		for _, idx := range flashItemsIdx {
			it := &p.items[idx]
			if fp, ok := flashPrices[it.flashSaleItemID]; ok && fp > 0 && fp <= it.sku.GetPriceVnd() {
				it.effectivePrice = fp
				computedFlashDiscount += (it.sku.GetPriceVnd() - fp) * int64(it.quantity)
			}
		}
		if p.flashSaleDiscountVND == 0 && computedFlashDiscount > 0 {
			p.flashSaleDiscountVND = computedFlashDiscount
		} else if p.flashSaleDiscountVND > 0 && computedFlashDiscount == 0 && len(flashItemsIdx) == 1 {
			it := &p.items[flashItemsIdx[0]]
			perUnitDisc := p.flashSaleDiscountVND / int64(it.quantity)
			eff := it.sku.GetPriceVnd() - perUnitDisc
			if eff < 0 {
				eff = 0
			}
			it.effectivePrice = eff
		}

		// Clamp freeship discount to shipping fee.
		if p.freeshipDiscountShare > p.shippingFeeVND {
			p.freeshipDiscountShare = p.shippingFeeVND
		}

		// Compute total payable VND cleanly in integer arithmetic.
		payable := p.subtotalVND -
			p.flashSaleDiscountVND -
			p.shopVoucherDiscount -
			p.platformVoucherShare +
			p.shippingFeeVND -
			p.freeshipDiscountShare -
			p.coinsDiscountShare
		if payable < 0 {
			payable = 0
		}
		p.totalPayableVND = payable

		// Compute commission_vnd from effective item totals using category commission rate or default BPS.
		var rawCommission int64
		for _, it := range p.items {
			bps := it.sku.GetCommissionRateBps()
			if bps <= 0 {
				bps = defaultCommissionBPS
			}
			lineVal := it.effectivePrice * int64(it.quantity)
			rawCommission += (lineVal * int64(bps)) / 10000
		}

		// Cap commission to net merchandise allocation so payment escrow split stays strictly valid.
		finalShipping := p.shippingFeeVND - p.freeshipDiscountShare
		allocShipping := minInt64(finalShipping, p.totalPayableVND)
		allocSubtotal := p.totalPayableVND - allocShipping
		if rawCommission > allocSubtotal {
			rawCommission = allocSubtotal
		}
		if rawCommission < 0 {
			rawCommission = 0
		}
		p.commissionVND = rawCommission
	}
}

func buildCheckoutBatch(
	checkoutGroupID, buyerID string,
	paymentMethod orderv1.OrderPaymentMethod,
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
		CheckoutGroupID: checkoutGroupID,
		Orders:          make([]store.NewOrderRecord, 0, len(plans)),
	}

	statusStr := statemachine.StatusToString(initialStatus)
	methodStr := statemachine.PaymentMethodToString(paymentMethod)

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
			ShopVoucherDiscountVND:     p.shopVoucherDiscount,
			PlatformVoucherDiscountVND: p.platformVoucherShare,
			FreeshipDiscountVND:        p.freeshipDiscountShare,
			CoinsDiscountVND:           p.coinsDiscountShare,
			ShippingFeeVND:             p.shippingFeeVND,
			CommissionVND:              p.commissionVND,
			TotalPayableVND:            p.totalPayableVND,
			CarrierCode:                p.carrierCode,
			ServiceCode:                p.serviceCode,
			TrackingCode:               "",
			LabelURL:                   "",
			CancelReason:               "",
			ShippingAddressJSON:        addrJSON,
			PaymentExpiresAt:           paymentExpiresAt,
			SalesChannel:               salesChannel,
			StorefrontShopID:           storefrontShopID,
		}

		itemParams := make([]db.InsertOrderItemParams, 0, len(p.items))
		for _, it := range p.items {
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
				WeightGrams:           it.sku.GetWeightGrams(),
				FlashSaleItemID:       it.flashSaleItemID,
				CommissionRateBPS:     it.sku.GetCommissionRateBps(),
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

func buildCreatePaymentRequest(
	checkoutGroupID, buyerID string,
	totalPayableVND int64,
	method orderv1.OrderPaymentMethod,
	expiresAt *time.Time,
	aggs []store.OrderAggregate,
	salesChannel, storefrontShopID string,
) *paymentv1.CreatePaymentRequest {
	payMethod := paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR
	switch method {
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD:
		payMethod = paymentv1.PaymentMethod_PAYMENT_METHOD_COD
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY:
		payMethod = paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY
	}

	allocs := make([]*paymentv1.ShopAllocation, 0, len(aggs))
	for _, agg := range aggs {
		ord := agg.Order
		finalShip := maxInt64(0, ord.ShippingFeeVND-ord.FreeshipDiscountVND)
		allocShip := minInt64(finalShip, ord.TotalPayableVND)
		allocSub := ord.TotalPayableVND - allocShip
		allocComm := minInt64(ord.CommissionVND, allocSub)
		allocs = append(allocs, &paymentv1.ShopAllocation{
			OrderId:          ord.ID,
			ShopId:           ord.ShopID,
			SubtotalVnd:      allocSub,
			CommissionVnd:    allocComm,
			ShippingFeeVnd:   allocShip,
			SalesChannel:     effectiveSalesChannel(ord.SalesChannel),
			StorefrontShopId: ord.StorefrontShopID,
		})
	}

	req := &paymentv1.CreatePaymentRequest{
		OrderGroupId:     checkoutGroupID,
		OrderId:          checkoutGroupID,
		UserId:           buyerID,
		AmountVnd:        totalPayableVND,
		Method:           payMethod,
		ShopAllocations:  allocs,
		IdempotencyKey:   "checkout:" + checkoutGroupID,
		SalesChannel:     salesChannel,
		StorefrontShopId: storefrontShopID,
	}
	if expiresAt != nil {
		req.ExpiresAt = timestamppb.New(*expiresAt)
	}
	return req
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

func (s *OrderService) compensateReleaseStock(ctx context.Context, checkoutGroupID string) {
	if s.clients.Product == nil {
		return
	}
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
		_, err := s.clients.Product.ReleaseStock(c, &productv1.ReleaseStockRequest{OrderId: checkoutGroupID})
		return err
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "saga compensation ReleaseStock failed", "checkout_group_id", checkoutGroupID, "error", err)
	}
}

func (s *OrderService) compensateReleasePromotion(ctx context.Context, checkoutGroupID string) {
	if s.clients.Promotion == nil {
		return
	}
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
		_, err := s.clients.Promotion.ReleasePromotion(c, &promotionv1.ReleasePromotionRequest{OrderId: checkoutGroupID})
		return err
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "saga compensation ReleasePromotion failed", "checkout_group_id", checkoutGroupID, "error", err)
	}
}

func (s *OrderService) confirmStockBestEffort(ctx context.Context, checkoutGroupID string) {
	if s.clients.Product == nil {
		return
	}
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
		_, err := s.clients.Product.ConfirmStock(c, &productv1.ConfirmStockRequest{OrderId: checkoutGroupID})
		return err
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "ConfirmStock failed", "checkout_group_id", checkoutGroupID, "error", err)
	}
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
