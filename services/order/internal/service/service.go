package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OrderService implements all Order domain workflows, checkout saga, RBAC, and async handlers.
type OrderService struct {
	cfg                   config.Config
	store                 store.Store
	clients               *clients.Bundle
	logger                *slog.Logger
	outboxTrigger         chan struct{}
	idemLocks             *keyedLocker
	nowFn                 func() time.Time
	acquireMaxWait        time.Duration
	acquireInitialBackoff time.Duration
	acquireMaxBackoff     time.Duration
}

// NewOrderService constructs an OrderService.
func NewOrderService(cfg config.Config, st store.Store, cl *clients.Bundle, logger *slog.Logger) *OrderService {
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderService{
		cfg:           cfg,
		store:         st,
		clients:       cl,
		logger:        logger,
		outboxTrigger: make(chan struct{}, 1),
		idemLocks:     newKeyedLocker(),
		nowFn:         func() time.Time { return time.Now().UTC() },
	}
}

// OutboxTrigger returns a signal channel notified whenever new outbox rows are committed.
func (s *OrderService) OutboxTrigger() <-chan struct{} {
	return s.outboxTrigger
}

func (s *OrderService) notifyOutbox() {
	select {
	case s.outboxTrigger <- struct{}{}:
	default:
	}
}

type callerIdentity struct {
	UserID       string
	Role         string
	ShopID       string
	FromAuthCtx  bool
	IsPrivileged bool
}

func requireAuthenticatedCaller(ctx context.Context) (callerIdentity, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || strings.TrimSpace(claims.UserID()) == "" {
		return callerIdentity{}, status.Error(codes.Unauthenticated, "authentication required")
	}
	role := strings.ToLower(strings.TrimSpace(claims.Role))
	return callerIdentity{
		UserID:       strings.TrimSpace(claims.UserID()),
		Role:         role,
		ShopID:       strings.TrimSpace(claims.ShopID),
		FromAuthCtx:  true,
		IsPrivileged: isAdminRole(role),
	}, nil
}

func canViewOrderFees(caller callerIdentity, shopID string) bool {
	if caller.IsPrivileged {
		return true
	}
	return caller.Role == "seller" && caller.ShopID != "" && caller.ShopID == shopID
}

func isAdminRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	return r == "admin" || r == "super_admin"
}

func canViewOrCancelOrder(caller callerIdentity, ord db.Order) bool {
	if caller.IsPrivileged {
		return true
	}
	if caller.UserID != "" && caller.UserID == ord.BuyerID {
		return true
	}
	if caller.Role == "seller" && caller.ShopID != "" && caller.ShopID == ord.ShopID {
		return true
	}
	return false
}

func canManageShopOrder(caller callerIdentity, ord db.Order, reqShopID string) bool {
	if caller.IsPrivileged {
		return true
	}
	if caller.FromAuthCtx && caller.Role != "seller" {
		return false
	}
	if caller.FromAuthCtx && reqShopID != "" && caller.ShopID != "" && reqShopID != caller.ShopID {
		return false
	}
	return caller.ShopID != "" && caller.ShopID == ord.ShopID
}

func canActAsBuyer(caller callerIdentity, ord db.Order, reqBuyerID string) bool {
	if caller.FromAuthCtx && reqBuyerID != "" && caller.UserID != "" && reqBuyerID != caller.UserID {
		return false
	}
	return caller.UserID != "" && caller.UserID == ord.BuyerID
}

const paymentNotInitializedMsg = "PAYMENT_NOT_INITIALIZED: đơn đang khởi tạo thanh toán, vui lòng thử lại sau ít phút"

// requireCheckoutPaymentInitialized blocks shipping / post-shipping transitions while the order's
// checkout_group is still IN_PROGRESS (payment not yet created & attached). Because checkout_groups
// never transition back to IN_PROGRESS, a point read without locking is sufficient (P0 M1).
func (s *OrderService) requireCheckoutPaymentInitialized(ctx context.Context, checkoutGroupID string) error {
	gid := strings.TrimSpace(checkoutGroupID)
	if gid == "" {
		return nil
	}
	grp, err := s.store.GetCheckoutGroupByID(ctx, gid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return mapStoreError(err)
	}
	if grp.Status == "IN_PROGRESS" {
		return status.Error(codes.FailedPrecondition, paymentNotInitializedMsg)
	}
	return nil
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.NotFound, "record not found")
	}
	if errors.Is(err, statemachine.ErrInvalidTransition) ||
		errors.Is(err, statemachine.ErrInvalidReturnTransition) ||
		errors.Is(err, store.ErrRetryPrecondition) ||
		errors.Is(err, store.ErrDuplicateReturnRequest) ||
		errors.Is(err, store.ErrReturnDeadlineNotReached) ||
		errors.Is(err, store.ErrOrderNotNeedingReview) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Internal, "internal order error: %v", err)
}

func (s *OrderService) codMaxRejectedCount() int32 {
	if s.cfg.CODMaxRejectedCount <= 0 {
		return 3
	}
	return s.cfg.CODMaxRejectedCount
}

func (s *OrderService) codMinOrdersForRate() int64 {
	if s.cfg.CODMinOrdersForRate <= 0 {
		return 3
	}
	return int64(s.cfg.CODMinOrdersForRate)
}

func (s *OrderService) codMaxRejectRateBPS() int64 {
	if s.cfg.CODMaxRejectRateBPS <= 0 {
		return 3000
	}
	return int64(s.cfg.CODMaxRejectRateBPS)
}

func (s *OrderService) paymentExpiryDuration() time.Duration {
	mins := s.cfg.PaymentExpiryMinutes
	if mins <= 0 {
		mins = 15
	}
	return time.Duration(mins) * time.Minute
}

func (s *OrderService) flashPaymentExpiryDuration() time.Duration {
	mins := s.cfg.FlashPaymentExpiryMinutes
	if mins <= 0 {
		mins = 5
	}
	return time.Duration(mins) * time.Minute
}

func (s *OrderService) autoCompleteDuration() time.Duration {
	days := s.cfg.AutoCompleteDays
	if days <= 0 {
		days = 7
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *OrderService) sellerConfirmTimeout() time.Duration {
	hours := s.cfg.SellerConfirmHours
	if hours <= 0 {
		hours = 48
	}
	return time.Duration(hours) * time.Hour
}

func (s *OrderService) returnSellerResponseDuration() time.Duration {
	days := s.cfg.ReturnSellerResponseDays
	if days <= 0 {
		days = 3
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *OrderService) returnShipDuration() time.Duration {
	days := s.cfg.ReturnShipDays
	if days <= 0 {
		days = 5
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *OrderService) returnInspectDuration() time.Duration {
	days := s.cfg.ReturnInspectDays
	if days <= 0 {
		days = 3
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *OrderService) buildOrderCompletedOutbox(ord db.Order, traceID, traceparent string) (db.InsertOutboxEventParams, error) {
	coinsReward := ComputeOrderRewardCoins(ord, s.cfg.RewardCoinsBPS)
	payloadBytes, err := json.Marshal(events.OrderCompletedPayload{
		OrderID:         ord.ID,
		OrderCode:       ord.OrderCode,
		BuyerID:         ord.BuyerID,
		ShopID:          ord.ShopID,
		SubtotalVND:     ord.SubtotalVND,
		CommissionVND:   ord.CommissionVND,
		PaymentFeeVND:   ord.PaymentFeeVND,
		TotalPayableVND: ord.TotalPayableVND,
		CoinsReward:     coinsReward,
	})
	if err != nil {
		return db.InsertOutboxEventParams{}, fmt.Errorf("marshal order.completed event: %w", err)
	}
	return db.InsertOutboxEventParams{
		ID:          uuid.NewString(),
		EventType:   events.SubjectOrderCompleted,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
	}, nil
}

func traceMetaFromContext(ctx context.Context) (string, string) {
	traceID := telemetry.TraceIDFromContext(ctx)
	carrier := map[string]string{}
	telemetry.InjectTraceHeaders(ctx, carrier)
	return traceID, carrier["traceparent"]
}

func detachedCompensationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	base := context.WithoutCancel(parent)
	base = clients.WithInternalAdminContext(base)
	return context.WithTimeout(base, timeout)
}

func generateOrderCode(now time.Time) string {
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	return fmt.Sprintf("NM-%s-%s", now.UTC().Format("20060102"), strings.ToUpper(raw[:8]))
}

// computeSellerRevenue returns seller net merchandise revenue R = max(0, G - F - P_shop - V_shop - S_shop).
func computeSellerRevenue(ord db.Order) int64 {
	return maxInt64(0, ord.SubtotalVND-ord.FlashSaleDiscountVND-ord.ShopProgramDiscountVND-ord.ShopVoucherDiscountVND-ord.ShopFreeshipDiscountVND)
}

// ComputeOrderRewardCoins calculates reward coins for a completed sub-order based on
// buyer's actual merchandise payment: min(P, max(0, (G - F - P_shop - V_shop) - V_plat - X)).
// Shop freeship S_shop subsidizes shipping rather than reducing merchandise paid by the buyer.
// Zero-payable orders always earn 0 coins.
func ComputeOrderRewardCoins(ord db.Order, rewardBPS int32) int64 {
	if ord.TotalPayableVND <= 0 || rewardBPS <= 0 {
		return 0
	}
	merchAfterShopMerch := maxInt64(0, ord.SubtotalVND-ord.FlashSaleDiscountVND-ord.ShopProgramDiscountVND-ord.ShopVoucherDiscountVND)
	actualMerchPaid := maxInt64(0, merchAfterShopMerch-maxInt64(0, ord.PlatformVoucherDiscountVND)-maxInt64(0, ord.CoinsDiscountVND))
	actualMerchPaid = minInt64(ord.TotalPayableVND, actualMerchPaid)
	if actualMerchPaid <= 0 {
		return 0
	}
	return (actualMerchPaid * int64(rewardBPS)) / 10000
}

func aggregateToProtoForCaller(agg store.OrderAggregate, caller callerIdentity) *orderv1.Order {
	ord := agg.Order
	isAdmin := caller.IsPrivileged
	showFees := canViewOrderFees(caller, ord.ShopID)
	var addr orderv1.OrderAddressSnapshot
	if len(ord.ShippingAddressJSON) > 0 {
		_ = json.Unmarshal(ord.ShippingAddressJSON, &addr)
	}

	items := make([]*orderv1.OrderItem, 0, len(agg.Items))
	for _, it := range agg.Items {
		var itemCommBPS int32
		if showFees {
			itemCommBPS = it.CommissionRateBPS
		}
		items = append(items, &orderv1.OrderItem{
			Id:                    it.ID,
			OrderId:               it.OrderID,
			SkuId:                 it.SKUID,
			ProductId:             it.ProductID,
			ProductName:           it.ProductName,
			SkuName:               it.SKUName,
			UnitPriceVnd:          it.UnitPriceVND,
			EffectiveUnitPriceVnd: it.EffectiveUnitPriceVND,
			Quantity:              it.Quantity,
			LineTotalVnd:          it.LineTotalVND,
			WeightGrams:           it.WeightGrams,
			FlashSaleItemId:       it.FlashSaleItemID,
			CommissionBps:         itemCommBPS,
			ShopProgramItemId:     it.ShopProgramItemID,
		})
	}

	timeline := make([]*orderv1.OrderTimelineEvent, 0, len(agg.Timeline))
	for _, tl := range agg.Timeline {
		timeline = append(timeline, timelineToProto(tl))
	}

	var commVND, payFeeVND int64
	var payFeeBPS int32
	if showFees {
		commVND = ord.CommissionVND
		payFeeVND = ord.PaymentFeeVND
		payFeeBPS = ord.PaymentFeeBPS
	}

	out := &orderv1.Order{
		Id:                         ord.ID,
		OrderCode:                  ord.OrderCode,
		CheckoutGroupId:            ord.CheckoutGroupID,
		BuyerId:                    ord.BuyerID,
		ShopId:                     ord.ShopID,
		Status:                     statemachine.StringToStatus(ord.Status),
		PaymentMethod:              statemachine.StringToPaymentMethod(ord.PaymentMethod),
		PaymentId:                  ord.PaymentID,
		PaymentStatus:              ord.PaymentStatus,
		SubtotalVnd:                ord.SubtotalVND,
		FlashSaleDiscountVnd:       ord.FlashSaleDiscountVND,
		ShopProgramDiscountVnd:     ord.ShopProgramDiscountVND,
		ShopVoucherDiscountVnd:     ord.ShopVoucherDiscountVND,
		ShopFreeshipDiscountVnd:    ord.ShopFreeshipDiscountVND,
		PlatformVoucherDiscountVnd: ord.PlatformVoucherDiscountVND,
		FreeshipDiscountVnd:        ord.FreeshipDiscountVND,
		CoinsDiscountVnd:           ord.CoinsDiscountVND,
		ShippingFeeVnd:             ord.ShippingFeeVND,
		CommissionVnd:              commVND,
		PaymentFeeVnd:              payFeeVND,
		PaymentFeeBps:              payFeeBPS,
		TotalPayableVnd:            ord.TotalPayableVND,
		CarrierCode:                ord.CarrierCode,
		ServiceCode:                ord.ServiceCode,
		TrackingCode:               ord.TrackingCode,
		LabelUrl:                   ord.LabelURL,
		CancelReason:               ord.CancelReason,
		ShippingAddress:            &addr,
		Items:                      items,
		Timeline:                   timeline,
		CreatedAt:                  timestamppb.New(ord.CreatedAt),
		UpdatedAt:                  timestamppb.New(ord.UpdatedAt),
		NeedsManualReview:          isAdmin && ord.OrphanShipmentNeedsManualReview,
		ShipmentJobStatus:          ord.ShipmentJobStatus,
	}
	if ord.PaymentExpiresAt != nil {
		out.PaymentExpiresAt = timestamppb.New(*ord.PaymentExpiresAt)
	}
	if ord.DeliveredAt != nil {
		out.DeliveredAt = timestamppb.New(*ord.DeliveredAt)
	}
	if agg.ReturnRequestID != "" {
		out.ReturnRequestId = agg.ReturnRequestID
		out.ReturnStatus = statemachine.StringToReturnStatus(agg.ReturnStatus)
	}
	return out
}

func returnAggregateToProto(agg store.ReturnAggregate) *orderv1.ReturnRequest {
	rr := agg.Request
	var evidenceURLs []string
	if len(rr.EvidenceURLsJSON) > 0 {
		_ = json.Unmarshal(rr.EvidenceURLsJSON, &evidenceURLs)
	}
	var sellerEvidenceURLs []string
	if len(rr.SellerEvidenceURLsJSON) > 0 {
		_ = json.Unmarshal(rr.SellerEvidenceURLsJSON, &sellerEvidenceURLs)
	}
	evs := make([]*orderv1.ReturnEvent, 0, len(agg.Events))
	for _, ev := range agg.Events {
		evs = append(evs, &orderv1.ReturnEvent{
			Id:         ev.ID,
			FromStatus: statemachine.StringToReturnStatus(ev.FromStatus),
			ToStatus:   statemachine.StringToReturnStatus(ev.ToStatus),
			ActorId:    ev.ActorID,
			ActorRole:  ev.ActorRole,
			Note:       ev.Note,
			CreatedAt:  timestamppb.New(ev.CreatedAt),
		})
	}
	out := &orderv1.ReturnRequest{
		Id:                 rr.ID,
		OrderId:            rr.OrderID,
		OrderCode:          rr.OrderCode,
		BuyerId:            rr.BuyerID,
		ShopId:             rr.ShopID,
		Status:             statemachine.StringToReturnStatus(rr.Status),
		Reason:             rr.Reason,
		EvidenceUrls:       evidenceURLs,
		SellerResponse:     rr.SellerResponse,
		SellerEvidenceUrls: sellerEvidenceURLs,
		DisputeReason:      rr.DisputeReason,
		AdminNote:          rr.AdminNote,
		RefundAmountVnd:    rr.RefundAmountVND,
		ReturnTrackingCode: rr.ReturnTrackingCode,
		ReturnLabelUrl:     rr.ReturnLabelURL,
		ReturnFeeVnd:       rr.ReturnFeeVND,
		ReturnFeeBearer:    rr.ReturnFeeBearer,
		GoodsReceived:      rr.GoodsReceived,
		CreatedAt:          timestamppb.New(rr.CreatedAt),
		UpdatedAt:          timestamppb.New(rr.UpdatedAt),
		Events:             evs,
	}
	if rr.DeadlineAt != nil {
		out.DeadlineAt = timestamppb.New(*rr.DeadlineAt)
	}
	return out
}

func timelineToProto(tl db.OrderTimelineEvent) *orderv1.OrderTimelineEvent {
	return &orderv1.OrderTimelineEvent{
		Id:         tl.ID,
		OrderId:    tl.OrderID,
		FromStatus: statemachine.StringToStatus(tl.FromStatus),
		ToStatus:   statemachine.StringToStatus(tl.ToStatus),
		ActorId:    tl.ActorID,
		ActorRole:  tl.ActorRole,
		Note:       tl.Note,
		CreatedAt:  timestamppb.New(tl.CreatedAt),
	}
}

type lockEntry struct {
	mu   sync.Mutex
	refs int
}

type keyedLocker struct {
	mu      sync.Mutex
	entries map[string]*lockEntry
}

func newKeyedLocker() *keyedLocker {
	return &keyedLocker{entries: make(map[string]*lockEntry)}
}

func (k *keyedLocker) Lock(key string) func() {
	k.mu.Lock()
	entry, ok := k.entries[key]
	if !ok {
		entry = &lockEntry{}
		k.entries[key] = entry
	}
	entry.refs++
	k.mu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}
}
