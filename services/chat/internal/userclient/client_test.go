package userclient

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer
	shops    map[string]*userv1.Shop
	failOnce atomic.Bool
	calls    atomic.Int32
}

func (f *fakeUserServer) GetShop(_ context.Context, req *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
	f.calls.Add(1)
	if f.failOnce.CompareAndSwap(true, false) {
		return nil, status.Error(codes.Unavailable, "transient user outage")
	}
	sh, ok := f.shops[req.GetShopId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "shop not found")
	}
	return &userv1.GetShopResponse{Shop: sh}, nil
}

func TestVerifyActiveShop_MapsNotFoundToErrShopInactive(t *testing.T) {
	userMgr, err := auth.NewManager("chat-test-user-secret", time.Minute)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("chat-test-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	fake := &fakeUserServer{
		shops: map[string]*userv1.Shop{
			"shop-active": {
				Id:     "shop-active",
				Name:   "Active Shop",
				Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			},
			"shop-suspended-owner-view": {
				Id:     "shop-suspended-owner-view",
				Name:   "Suspended Shop",
				Status: userv1.ShopStatus_SHOP_STATUS_SUSPENDED,
			},
		},
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	userv1.RegisterUserServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := New(lis.Addr().String(), "chat-test")
	if err != nil {
		t.Fatalf("New userclient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	buyerTok, _, _, err := userMgr.SignAccessToken("buyer-1", auth.RoleBuyer, "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	buyerCtx := auth.WithRawToken(context.Background(), "Bearer "+buyerTok)

	// 1. ACTIVE shop succeeds (forwarding buyer JWT cleanly to public GetShop).
	if err := client.VerifyActiveShop(buyerCtx, "shop-active"); err != nil {
		t.Fatalf("VerifyActiveShop(active) failed: %v", err)
	}

	// 2. Public GetShop returning NotFound (for SUSPENDED/PENDING/missing shop) maps to ErrShopInactive.
	if err := client.VerifyActiveShop(buyerCtx, "shop-hidden-or-missing"); !errors.Is(err, ErrShopInactive) {
		t.Fatalf("VerifyActiveShop(NotFound): want ErrShopInactive, got %v", err)
	}

	// 3. Non-ACTIVE status returned directly (e.g., when owner/admin calls GetShop) also maps to ErrShopInactive.
	if err := client.VerifyActiveShop(buyerCtx, "shop-suspended-owner-view"); !errors.Is(err, ErrShopInactive) {
		t.Fatalf("VerifyActiveShop(SUSPENDED): want ErrShopInactive, got %v", err)
	}

	// 4. Transient Unavailable retries and succeeds.
	fake.failOnce.Store(true)
	before := fake.calls.Load()
	if err := client.VerifyActiveShop(buyerCtx, "shop-active"); err != nil {
		t.Fatalf("VerifyActiveShop(retry transient): %v", err)
	}
	if fake.calls.Load()-before != 2 {
		t.Fatalf("expected 2 attempts on transient failure, got %d", fake.calls.Load()-before)
	}
}
