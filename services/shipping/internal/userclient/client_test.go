package userclient

import (
	"context"
	"net"
	"testing"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer
	shops     map[string]*userv1.Shop
	addresses map[string]*userv1.Address
}

func (f *fakeUserServer) GetShop(_ context.Context, req *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
	sh, ok := f.shops[req.GetShopId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "shop not found")
	}
	return &userv1.GetShopResponse{Shop: sh}, nil
}

func (f *fakeUserServer) GetAddress(ctx context.Context, req *userv1.GetAddressRequest) (*userv1.GetAddressResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.Service == "" || claims.Role != auth.ServiceRole {
		return nil, status.Error(codes.PermissionDenied, "expected internal service token")
	}
	addr, ok := f.addresses[req.GetAddressId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "address not found")
	}
	return &userv1.GetAddressResponse{Address: addr}, nil
}

func TestClient_GetShopPickupCodesWithRealAuthInterceptor(t *testing.T) {
	userMgr, err := auth.NewManager("test-user-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-service-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	fakeSrv := &fakeUserServer{
		shops: map[string]*userv1.Shop{
			"shop-sg": {
				Id:              "shop-sg",
				OwnerUserId:     "owner-1",
				PickupAddressId: "addr-sg",
			},
			"shop-no-pickup": {
				Id:          "shop-no-pickup",
				OwnerUserId: "owner-2",
			},
		},
		addresses: map[string]*userv1.Address{
			"addr-sg": {
				Id:           "addr-sg",
				UserId:       "owner-1",
				ProvinceCode: "79",
				WardCode:     "79001",
			},
		},
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	userv1.RegisterUserServiceServer(srv, fakeSrv)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	client, err := New(lis.Addr().String(), "shipping", svcMgr)
	if err != nil {
		t.Fatalf("New userclient: %v", err)
	}
	defer func() { _ = client.Close() }()

	// 1. Valid shop resolves real pickup province/ward via GetShop (no svc token) + GetAddress (svc token).
	prov, ward, err := client.GetShopPickupCodes(context.Background(), "shop-sg")
	if err != nil {
		t.Fatalf("GetShopPickupCodes(shop-sg) failed: %v", err)
	}
	if prov != "79" || ward != "79001" {
		t.Fatalf("got (%q, %q), want (79, 79001)", prov, ward)
	}

	// 2. Missing shop returns gRPC NotFound.
	if _, _, err := client.GetShopPickupCodes(context.Background(), "shop-unknown"); status.Code(err) != codes.NotFound {
		t.Fatalf("expected codes.NotFound for unknown shop, got %v", err)
	}

	// 3. Shop without pickup address returns empty codes without transport error.
	prov, ward, err = client.GetShopPickupCodes(context.Background(), "shop-no-pickup")
	if err != nil || prov != "" || ward != "" {
		t.Fatalf("expected empty pickup for shop-no-pickup, got prov=%q ward=%q err=%v", prov, ward, err)
	}
}
