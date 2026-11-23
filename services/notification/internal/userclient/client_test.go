package userclient

import (
	"context"
	"net"
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
	shopErr error
	shop    *userv1.Shop
	user    *userv1.User
}

func (f *fakeUserServer) GetShop(_ context.Context, req *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
	if f.shopErr != nil {
		return nil, f.shopErr
	}
	if f.shop != nil && f.shop.GetId() == req.GetShopId() {
		return &userv1.GetShopResponse{Shop: f.shop}, nil
	}
	return nil, status.Error(codes.NotFound, "shop not found")
}

func (f *fakeUserServer) GetProfile(_ context.Context, req *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
	if f.user != nil && f.user.GetId() == req.GetUserId() {
		return &userv1.GetProfileResponse{User: f.user}, nil
	}
	return nil, status.Error(codes.NotFound, "user not found")
}

func TestClientGetShopOwnerAndGetUserContactWithAuthInterceptor(t *testing.T) {
	userMgr, err := auth.NewManager("test-user-secret", time.Minute)
	if err != nil {
		t.Fatalf("new user auth manager: %v", err)
	}
	svcMgr, err := auth.NewManager("test-service-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("new service auth manager: %v", err)
	}

	fake := &fakeUserServer{
		shop: &userv1.Shop{
			Id:          "shop-42",
			OwnerUserId: "seller-42",
			Name:        "Xưởng Dệt Bạch Đằng",
		},
		user: &userv1.User{
			Id:       "seller-42",
			Email:    "seller42@nexusmart.local",
			FullName: "Nguyễn Chủ Shop",
		},
	}

	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	userv1.RegisterUserServiceServer(srv, fake)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		srv.Stop()
		_ = lis.Close()
	}()
	go func() { _ = srv.Serve(lis) }()

	client, err := New(lis.Addr().String(), "notification-test", svcMgr)
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// 1. GetShopOwner must call GetShop WITHOUT a service token so auth.GRPCUnaryServerInterceptor allows it.
	ownerID, shopName, err := client.GetShopOwner(context.Background(), "shop-42")
	if err != nil {
		t.Fatalf("GetShopOwner failed: %v", err)
	}
	if ownerID != "seller-42" || shopName != "Xưởng Dệt Bạch Đằng" {
		t.Fatalf("unexpected shop owner result: owner=%q name=%q", ownerID, shopName)
	}

	// 2. GetUserContact must call GetProfile WITH a service token.
	email, fullName, err := client.GetUserContact(context.Background(), "seller-42")
	if err != nil {
		t.Fatalf("GetUserContact failed: %v", err)
	}
	if email != "seller42@nexusmart.local" || fullName != "Nguyễn Chủ Shop" {
		t.Fatalf("unexpected user contact: email=%q fullName=%q", email, fullName)
	}

	// 3. Transient error from UserService.GetShop must propagate.
	fake.shopErr = status.Error(codes.Unavailable, "user service temporarily unavailable")
	if _, _, err := client.GetShopOwner(context.Background(), "shop-42"); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable error from GetShopOwner, got %v", err)
	}
}
