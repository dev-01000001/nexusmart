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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer
	shopErr              error
	shop                 *userv1.Shop
	user                 *userv1.User
	site                 *userv1.Site
	getShopInternalCalls int
}

func (f *fakeUserServer) GetShopInternal(ctx context.Context, req *userv1.GetShopInternalRequest) (*userv1.GetShopInternalResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.Service != serviceTokenName || claims.Role != auth.ServiceRole {
		return nil, status.Error(codes.PermissionDenied, "expected notification-service internal token")
	}
	f.getShopInternalCalls++
	if f.shopErr != nil {
		return nil, f.shopErr
	}
	if f.shop != nil && f.shop.GetId() == req.GetShopId() {
		return &userv1.GetShopInternalResponse{Shop: f.shop}, nil
	}
	return nil, status.Error(codes.NotFound, "shop not found")
}

func (f *fakeUserServer) GetProfile(_ context.Context, req *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
	if f.user != nil && f.user.GetId() == req.GetUserId() {
		return &userv1.GetProfileResponse{User: f.user}, nil
	}
	return nil, status.Error(codes.NotFound, "user not found")
}

func (f *fakeUserServer) GetSiteByShop(_ context.Context, req *userv1.GetSiteByShopRequest) (*userv1.GetSiteByShopResponse, error) {
	if f.site != nil && req.GetShopId() == "shop-42" {
		return &userv1.GetSiteByShopResponse{Site: f.site}, nil
	}
	return nil, status.Error(codes.NotFound, "site not found")
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
		site: &userv1.Site{
			PrimaryHost: "bach-dang.nexusmart.vn",
			Status:      userv1.SiteStatus_SITE_STATUS_PUBLISHED,
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

	// 1. GetShopOwner must call GetShopInternal WITH a service token through auth.GRPCUnaryServerInterceptor.
	ownerID, shopName, err := client.GetShopOwner(context.Background(), "shop-42")
	if err != nil {
		t.Fatalf("GetShopOwner failed: %v", err)
	}
	if ownerID != "seller-42" || shopName != "Xưởng Dệt Bạch Đằng" {
		t.Fatalf("unexpected shop owner result: owner=%q name=%q", ownerID, shopName)
	}
	if fake.getShopInternalCalls != 1 {
		t.Fatalf("expected 1 GetShopInternal call with service token, got %d", fake.getShopInternalCalls)
	}

	// 2. GetUserContact must call GetProfile WITH a service token.
	email, fullName, err := client.GetUserContact(context.Background(), "seller-42")
	if err != nil {
		t.Fatalf("GetUserContact failed: %v", err)
	}
	if email != "seller42@nexusmart.local" || fullName != "Nguyễn Chủ Shop" {
		t.Fatalf("unexpected user contact: email=%q fullName=%q", email, fullName)
	}

	// 3. GetSitePrimaryHost must call public GetSiteByShop WITHOUT a token through auth.GRPCUnaryServerInterceptor.
	host, err := client.GetSitePrimaryHost(context.Background(), "shop-42")
	if err != nil || host != "bach-dang.nexusmart.vn" {
		t.Fatalf("GetSitePrimaryHost with auth interceptor: host=%q err=%v", host, err)
	}

	// 4. Transient error from UserService.GetShopInternal must propagate.
	fake.shopErr = status.Error(codes.Unavailable, "user service temporarily unavailable")
	if _, _, err := client.GetShopOwner(context.Background(), "shop-42"); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable error from GetShopOwner, got %v", err)
	}

	// 5. Client without service token manager fails closed instead of calling GetShopInternal anonymously.
	noSvcClient, err := New(lis.Addr().String(), "notification-test", nil)
	if err != nil {
		t.Fatalf("New client(nil svc): %v", err)
	}
	defer func() { _ = noSvcClient.Close() }()
	if _, _, err := noSvcClient.GetShopOwner(context.Background(), "shop-42"); err == nil {
		t.Fatalf("expected error when service token manager is nil")
	}
}

type fakeSiteServer struct {
	userv1.UnimplementedUserServiceServer
}

func (fakeSiteServer) GetSiteByShop(_ context.Context, req *userv1.GetSiteByShopRequest) (*userv1.GetSiteByShopResponse, error) {
	switch req.GetShopId() {
	case "published":
		return &userv1.GetSiteByShopResponse{Site: &userv1.Site{
			PrimaryHost: " bach-dang.nexusmart.vn ",
			Status:      userv1.SiteStatus_SITE_STATUS_PUBLISHED,
		}}, nil
	case "draft":
		return &userv1.GetSiteByShopResponse{Site: &userv1.Site{
			PrimaryHost: "draft.nexusmart.vn",
			Status:      userv1.SiteStatus_SITE_STATUS_DRAFT,
		}}, nil
	case "broken":
		return nil, status.Error(codes.Unavailable, "down")
	default:
		return nil, status.Error(codes.NotFound, "site not found")
	}
}

func TestGetSitePrimaryHost(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	userv1.RegisterUserServiceServer(srv, fakeSiteServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &Client{conn: conn, client: userv1.NewUserServiceClient(conn), timeout: time.Second}

	cases := []struct {
		shop    string
		want    string
		wantErr bool
	}{
		{"published", "bach-dang.nexusmart.vn", false},
		{"draft", "", false},
		{"missing", "", false},
		{"", "", false},
		{"broken", "", true},
	}
	for _, tc := range cases {
		got, err := c.GetSitePrimaryHost(context.Background(), tc.shop)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("shop %q: got (%q, %v), want (%q, err=%v)", tc.shop, got, err, tc.want, tc.wantErr)
		}
	}
}
