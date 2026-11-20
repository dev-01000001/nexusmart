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
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OrderService implements all Order domain workflows, checkout saga, RBAC, and async handlers.
type OrderService struct {
	cfg           config.Config
	store         store.Store
	clients       *clients.Bundle
	logger        *slog.Logger
	outboxTrigger chan struct{}
	idemLocks     *keyedLocker
	nowFn         func() time.Time
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
	if caller.ShopID != "" && caller.ShopID == ord.ShopID {
		return true
	}
	return false
}

func canManageShopOrder(caller callerIdentity, ord db.Order, reqShopID string) bool {
	if caller.IsPrivileged {
		return true
	}
	if caller.FromAuthCtx && caller.Role != "" && caller.Role != "seller" {
		return false
	}
	if caller.FromAuthCtx && reqShopID != "" && caller.ShopID != "" && reqShopID != caller.ShopID {
		return false
	}
	return caller.ShopID != "" && caller.ShopID == ord.ShopID
}

func canActAsBuyer(caller callerIdentity, ord db.Order, reqBuyerID string) bool {
	if caller.IsPrivileged {
		return true
	}
	if caller.FromAuthCtx && reqBuyerID != "" && caller.UserID != "" && reqBuyerID != caller.UserID {
		return false
	}
	return caller.UserID != "" && caller.UserID == ord.BuyerID
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.NotFound, "order not found")
	}
	if errors.Is(err, statemachine.ErrInvalidTransition) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Internal, "internal order error: %v", err)
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

// AllocateProportionalVND splits total across positive weights using exact integer division,
// assigning any remainder to the last positive-weight bucket so sum(shares) == total.
func AllocateProportionalVND(total int64, weights []int64) []int64 {
	shares := make([]int64, len(weights))
	if total <= 0 || len(weights) == 0 {
		return shares
	}

	var sumWeights int64
	lastPositive := -1
	for i, w := range weights {
		if w > 0 {
			sumWeights += w
			lastPositive = i
		}
	}
	if sumWeights <= 0 || lastPositive < 0 {
		shares[0] = total
		return shares
	}

	var allocated int64
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		if i == lastPositive {
			shares[i] = total - allocated
		} else {
			share := (total * w) / sumWeights
			shares[i] = share
			allocated += share
		}
	}
	return shares
}

// ComputeRewardCoins calculates integer reward coins for a completed order.
func ComputeRewardCoins(subtotalVND int64, rewardBPS int32) int64 {
	if subtotalVND <= 0 || rewardBPS <= 0 {
		return 0
	}
	reward := (subtotalVND * int64(rewardBPS)) / 10000
	if reward == 0 {
		return 1
	}
	return reward
}

func aggregateToProto(agg store.OrderAggregate) *orderv1.Order {
	ord := agg.Order
	var addr orderv1.OrderAddressSnapshot
	if len(ord.ShippingAddressJSON) > 0 {
		_ = json.Unmarshal(ord.ShippingAddressJSON, &addr)
	}

	items := make([]*orderv1.OrderItem, 0, len(agg.Items))
	for _, it := range agg.Items {
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
		})
	}

	timeline := make([]*orderv1.OrderTimelineEvent, 0, len(agg.Timeline))
	for _, tl := range agg.Timeline {
		timeline = append(timeline, timelineToProto(tl))
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
		ShopVoucherDiscountVnd:     ord.ShopVoucherDiscountVND,
		PlatformVoucherDiscountVnd: ord.PlatformVoucherDiscountVND,
		FreeshipDiscountVnd:        ord.FreeshipDiscountVND,
		CoinsDiscountVnd:           ord.CoinsDiscountVND,
		ShippingFeeVnd:             ord.ShippingFeeVND,
		CommissionVnd:              ord.CommissionVND,
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
	}
	if ord.PaymentExpiresAt != nil {
		out.PaymentExpiresAt = timestamppb.New(*ord.PaymentExpiresAt)
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
