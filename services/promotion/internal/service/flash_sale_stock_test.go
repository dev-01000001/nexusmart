package service_test

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

// fakeProductFlashServer simulates ProductService's flash sale stock reservation RPCs
// over a real bufconn gRPC server protected by auth.GRPCUnaryServerInterceptor.
type fakeProductFlashServer struct {
	productv1.UnimplementedProductServiceServer
	mu           sync.Mutex
	skuStock     map[string]int32
	skuFlashRes  map[string]int32
	reservations map[string]*fakeFlashRes
	reserveCalls int
	releaseCalls int
}

type fakeFlashRes struct {
	skuID    string
	qty      int32
	released bool
}

func newFakeProductFlashServer(initialStock map[string]int32) *fakeProductFlashServer {
	s := make(map[string]int32, len(initialStock))
	for k, v := range initialStock {
		s[k] = v
	}
	return &fakeProductFlashServer{
		skuStock:     s,
		skuFlashRes:  make(map[string]int32),
		reservations: make(map[string]*fakeFlashRes),
	}
}

func (f *fakeProductFlashServer) ReserveFlashSaleStock(ctx context.Context, req *productv1.ReserveFlashSaleStockRequest) (*productv1.ReserveFlashSaleStockResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "promotion-service" && !auth.HasRole(claims, "admin", "super_admin") {
		return nil, status.Error(codes.PermissionDenied, "only promotion-service or admin allowed")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserveCalls++

	if existing, ok := f.reservations[req.GetFlashSaleItemId()]; ok {
		if !existing.released && existing.skuID == req.GetSkuId() && existing.qty == req.GetQuantity() {
			avail := f.skuStock[existing.skuID] - f.skuFlashRes[existing.skuID]
			return &productv1.ReserveFlashSaleStockResponse{
				ReservedQty:     existing.qty,
				AvailableStock:  avail,
				AlreadyReserved: true,
			}, nil
		}
		return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_RESERVATION_CONFLICT")
	}

	stock, ok := f.skuStock[req.GetSkuId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "sku not found")
	}
	avail := stock - f.skuFlashRes[req.GetSkuId()]
	if avail < req.GetQuantity() {
		return nil, status.Errorf(codes.FailedPrecondition, "INSUFFICIENT_STOCK: sku %s has %d available, requested %d", req.GetSkuId(), avail, req.GetQuantity())
	}
	f.skuFlashRes[req.GetSkuId()] += req.GetQuantity()
	f.reservations[req.GetFlashSaleItemId()] = &fakeFlashRes{
		skuID: req.GetSkuId(),
		qty:   req.GetQuantity(),
	}
	return &productv1.ReserveFlashSaleStockResponse{
		ReservedQty:     req.GetQuantity(),
		AvailableStock:  stock - f.skuFlashRes[req.GetSkuId()],
		AlreadyReserved: false,
	}, nil
}

func (f *fakeProductFlashServer) ReleaseFlashSaleStock(ctx context.Context, req *productv1.ReleaseFlashSaleStockRequest) (*productv1.ReleaseFlashSaleStockResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "promotion-service" && !auth.HasRole(claims, "admin", "super_admin") {
		return nil, status.Error(codes.PermissionDenied, "only promotion-service or admin allowed")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++

	existing, ok := f.reservations[req.GetFlashSaleItemId()]
	if !ok || existing.released {
		return &productv1.ReleaseFlashSaleStockResponse{
			ReleasedQty:     0,
			AlreadyReleased: true,
		}, nil
	}
	existing.released = true
	f.skuFlashRes[existing.skuID] -= existing.qty
	avail := f.skuStock[existing.skuID] - f.skuFlashRes[existing.skuID]
	return &productv1.ReleaseFlashSaleStockResponse{
		ReleasedQty:     existing.qty,
		AvailableStock:  avail,
		AlreadyReleased: false,
	}, nil
}

func (f *fakeProductFlashServer) flashReserved(skuID string) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skuFlashRes[skuID]
}

func setupServiceWithProductClient(t *testing.T, initialStock map[string]int32) (*service.PromotionService, *store.MemoryStore, *lua.Engine, *fakeProductFlashServer) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	userAuth, err := auth.NewManager("promo-user-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("user auth manager: %v", err)
	}
	svcAuth, err := auth.NewManager("promo-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("service auth manager: %v", err)
	}

	fakeProd := newFakeProductFlashServer(initialStock)
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(userAuth, svcAuth)))
	productv1.RegisterProductServiceServer(gs, fakeProd)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(func(
			ctx context.Context,
			method string,
			req, reply any,
			cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker,
			opts ...grpc.CallOption,
		) error {
			tok, signErr := svcAuth.SignServiceToken("promotion-service")
			if signErr != nil {
				return status.Error(codes.Internal, "sign service token")
			}
			ctx = auth.InjectOutgoingGRPC(ctx, tok)
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		t.Fatalf("dial fake product bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	mem := store.NewMemoryStore()
	eng := lua.NewEngine(rdb)
	log := logger.New("promotion-flash-stock-test", logger.WithWriter(io.Discard))
	svc := service.NewPromotionService(mem, eng, log)
	svc.SetProductClient(productv1.NewProductServiceClient(conn))
	return svc, mem, eng, fakeProd
}

func TestApproveFlashSaleItem_InsufficientProductStockRejected(t *testing.T) {
	svc, mem, eng, fakeProd := setupServiceWithProductClient(t, map[string]int32{"sku-tight": 10})
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Tight Stock Sale",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}
	reg, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fs.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-tight",
		FlashPriceVnd: 50000,
		TotalSlots:    15, // > 10 available!
		PerUserLimit:  1,
	})
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem: %v", err)
	}

	_, err = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: true,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("expected FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}

	// Item MUST remain PENDING and Redis slot key MUST NOT exist.
	got, err := mem.GetFlashSaleItem(context.Background(), reg.Item.Id)
	if err != nil || got.Status != "PENDING" {
		t.Fatalf("expected item to stay PENDING after failed stock reserve, got %+v err=%v", got, err)
	}
	if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(reg.Item.Id)); exists {
		t.Fatalf("Redis slot key must NOT be initialized when ProductService rejects reservation")
	}
	if fakeProd.flashReserved("sku-tight") != 0 {
		t.Fatalf("expected 0 reserved on sku-tight, got %d", fakeProd.flashReserved("sku-tight"))
	}
}

func TestApproveFlashSaleItem_SuccessIdempotentAndRevoke(t *testing.T) {
	svc, _, eng, fakeProd := setupServiceWithProductClient(t, map[string]int32{"sku-ok": 20})
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Idempotent Sale",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}
	reg, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fs.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-ok",
		FlashPriceVnd: 99000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem: %v", err)
	}

	// First approve reserves 10 in ProductService and loads 10 in Redis.
	app1, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: true,
	})
	if err != nil {
		t.Fatalf("ApproveFlashSaleItem 1st: %v", err)
	}
	if app1.Item.RemainingSlots != 10 || fakeProd.flashReserved("sku-ok") != 10 {
		t.Fatalf("post-approve mismatch: remaining=%d productReserved=%d", app1.Item.RemainingSlots, fakeProd.flashReserved("sku-ok"))
	}

	// Consume 3 slots via ReservePromotion.
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "ord-consume-3",
		UserId:  "buyer-1",
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: reg.Item.Id, SkuId: "sku-ok", Quantity: 3},
		},
	}); err != nil {
		t.Fatalf("ReservePromotion(3): %v", err)
	}

	// Idempotent re-approve MUST NOT reset Redis remaining slots (stays 7) and MUST NOT double-reserve product stock (stays 10).
	app2, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: true,
	})
	if err != nil {
		t.Fatalf("ApproveFlashSaleItem 2nd (idempotent): %v", err)
	}
	if app2.Item.RemainingSlots != 7 || fakeProd.flashReserved("sku-ok") != 10 {
		t.Fatalf("idempotent re-approve mutated state: remaining=%d (want 7), productReserved=%d (want 10)",
			app2.Item.RemainingSlots, fakeProd.flashReserved("sku-ok"))
	}

	// Revoking/rejecting an approved item deletes Redis slots and releases ProductService reservation.
	rej, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: false,
		Reason:  "seller requested cancellation",
	})
	if err != nil {
		t.Fatalf("revoke approved item: %v", err)
	}
	if rej.Item.Status != promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_REJECTED {
		t.Fatalf("expected REJECTED status, got %v", rej.Item.Status)
	}
	if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(reg.Item.Id)); exists {
		t.Fatalf("expected Redis slot key deleted after revoke")
	}
	if fakeProd.flashReserved("sku-ok") != 0 {
		t.Fatalf("expected product flash reserve released to 0, got %d", fakeProd.flashReserved("sku-ok"))
	}
}

func TestEndFlashSaleWindow_ActiveAndUpcoming_ReleasesStockAndIdempotent(t *testing.T) {
	svc, _, eng, fakeProd := setupServiceWithProductClient(t, map[string]int32{
		"sku-a": 30,
		"sku-b": 25,
		"sku-c": 20,
		"sku-u": 15,
	})
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Active window with 2 approved items + 1 pending item.
	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Active Window",
		StartsAt: timestamppb.New(now.Add(-30 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale active: %v", err)
	}
	regA, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-a", FlashPriceVnd: 10000, TotalSlots: 10,
	})
	regB, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-b", FlashPriceVnd: 20000, TotalSlots: 5,
	})
	regPending, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-c", FlashPriceVnd: 30000, TotalSlots: 7,
	})
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regA.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve A: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regB.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve B: %v", err)
	}

	// End active window -> releases 2 approved items, clears Redis, marks window ENDED.
	end1, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fs.FlashSale.Id})
	if err != nil {
		t.Fatalf("EndFlashSaleWindow 1st: %v", err)
	}
	if end1.GetReleasedItems() != 2 || end1.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("unexpected EndFlashSaleWindow 1st resp: %+v", end1)
	}
	if fakeProd.flashReserved("sku-a") != 0 || fakeProd.flashReserved("sku-b") != 0 {
		t.Fatalf("expected product reserves released to 0, got a=%d b=%d", fakeProd.flashReserved("sku-a"), fakeProd.flashReserved("sku-b"))
	}
	for _, id := range []string{regA.Item.Id, regB.Item.Id} {
		if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(id)); exists {
			t.Fatalf("expected Redis slot key deleted for item %s", id)
		}
	}

	// Idempotent second EndFlashSaleWindow returns released_items=0.
	end2, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fs.FlashSale.Id})
	if err != nil || end2.GetReleasedItems() != 0 || end2.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("unexpected EndFlashSaleWindow 2nd resp: %+v err=%v", end2, err)
	}

	// Ended window no longer appears in ListActiveFlashSales, blocks ReservePromotion and ApproveFlashSaleItem.
	active, err := svc.ListActiveFlashSales(context.Background(), &promotionv1.ListActiveFlashSalesRequest{IncludeUpcoming: true})
	if err != nil || len(active.GetFlashSales()) != 0 {
		t.Fatalf("ended window should not appear in ListActiveFlashSales: resp=%+v err=%v", active, err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "ord-after-end",
		UserId:  "buyer-1",
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: regA.Item.Id, SkuId: "sku-a", Quantity: 1},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion on ended window: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  regPending.Item.Id,
		Approve: true,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ApproveFlashSaleItem on ended window: want FailedPrecondition, got %v", err)
	}
	if fakeProd.flashReserved("sku-c") != 0 {
		t.Fatalf("sku-c should NOT be reserved when approving in ended window, got %d", fakeProd.flashReserved("sku-c"))
	}

	// Upcoming window (starts_at in the future) can also be canceled cleanly without violating ends_at > starts_at.
	fsUp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Upcoming Window",
		StartsAt: timestamppb.New(now.Add(2 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(4 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale upcoming: %v", err)
	}
	regU, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-u", FlashPriceVnd: 15000, TotalSlots: 8,
	})
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regU.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve upcoming item: %v", err)
	}
	if fakeProd.flashReserved("sku-u") != 8 {
		t.Fatalf("expected 8 reserved on upcoming sku-u, got %d", fakeProd.flashReserved("sku-u"))
	}
	endUp, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fsUp.FlashSale.Id})
	if err != nil || endUp.GetReleasedItems() != 1 || endUp.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("EndFlashSaleWindow upcoming unexpected: resp=%+v err=%v", endUp, err)
	}
	if !endUp.GetWindow().GetEndsAt().AsTime().After(endUp.GetWindow().GetStartsAt().AsTime()) {
		t.Fatalf("upcoming canceled window violated ends_at > starts_at invariant")
	}
	if fakeProd.flashReserved("sku-u") != 0 {
		t.Fatalf("expected upcoming sku-u released to 0, got %d", fakeProd.flashReserved("sku-u"))
	}
}

func TestSweepExpiredFlashSales_ReleasesStockOnce(t *testing.T) {
	svc, _, eng, fakeProd := setupServiceWithProductClient(t, map[string]int32{
		"sku-exp-1": 20,
		"sku-exp-2": 20,
	})
	curTime := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return curTime })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Expiring Window",
		StartsAt: timestamppb.New(curTime.Add(-10 * time.Minute)),
		EndsAt:   timestamppb.New(curTime.Add(10 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}
	r1, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-exp-1", FlashPriceVnd: 10000, TotalSlots: 6,
	})
	r2, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-exp-2", FlashPriceVnd: 10000, TotalSlots: 4,
	})
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: r1.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve r1: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: r2.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve r2: %v", err)
	}

	// Before expiry: sweep does nothing.
	n, err := svc.SweepExpiredFlashSales(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("sweep before expiry: want n=0 err=nil, got n=%d err=%v", n, err)
	}
	if fakeProd.flashReserved("sku-exp-1") != 6 || fakeProd.flashReserved("sku-exp-2") != 4 {
		t.Fatalf("expected stock still reserved before expiry")
	}

	// Advance past expiry -> sweep releases both items, deletes Redis keys, marks released.
	curTime = curTime.Add(15 * time.Minute)
	n, err = svc.SweepExpiredFlashSales(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("sweep after expiry: want n=2 err=nil, got n=%d err=%v", n, err)
	}
	if fakeProd.flashReserved("sku-exp-1") != 0 || fakeProd.flashReserved("sku-exp-2") != 0 {
		t.Fatalf("expected stock released to 0 after sweep, got %d / %d",
			fakeProd.flashReserved("sku-exp-1"), fakeProd.flashReserved("sku-exp-2"))
	}
	for _, id := range []string{r1.Item.Id, r2.Item.Id} {
		if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(id)); exists {
			t.Fatalf("expected Redis slot key %s deleted by sweeper", id)
		}
	}

	// Second sweep is a no-op (idempotent).
	n, err = svc.SweepExpiredFlashSales(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second sweep: want n=0 err=nil, got n=%d err=%v", n, err)
	}
}
