package service

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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestUserGRPCTrust_B1RejectsAnonymousAndUnauthorizedCallers(t *testing.T) {
	srv, _, _, pub, users := newTestHarness(t)
	svcMgr, err := auth.NewManager("user-test-service-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("new service auth manager: %v", err)
	}

	// Register, verify, and log in two buyers so we have real user IDs and addresses.
	registerActive := func(email, phone string) (userID, token string) {
		t.Helper()
		reg, err := srv.Register(context.Background(), &userv1.RegisterRequest{
			FullName: "User " + email,
			Email:    email,
			Phone:    phone,
			Password: "Password123!",
		})
		if err != nil {
			t.Fatalf("register %s: %v", email, err)
		}
		otp := pub.userRegistered[len(pub.userRegistered)-1].OTP
		if _, err := srv.VerifyOTP(context.Background(), &userv1.VerifyOTPRequest{
			Email: email,
			Otp:   otp,
		}); err != nil {
			t.Fatalf("verify otp %s: %v", email, err)
		}
		login, err := srv.Login(context.Background(), &userv1.LoginRequest{
			Email:    email,
			Password: "Password123!",
		})
		if err != nil {
			t.Fatalf("login %s: %v", email, err)
		}
		return reg.GetUserId(), login.GetAccessToken()
	}

	buyer1ID, buyer1Tok := registerActive("trust-b1@example.com", "0901000001")
	buyer2ID, buyer2Tok := registerActive("trust-b2@example.com", "0901000002")
	adminTok, _, _, _ := users.SignAccessToken("admin-1", RoleAdmin, "")
	sellerTok, _, _, _ := users.SignAccessToken("seller-1", RoleSeller, "shop-1")
	svcTok, _ := svcMgr.SignServiceToken("order-service")

	addrResp, err := srv.CreateAddress(ctxWithUser(buyer1ID, RoleBuyer, ""), &userv1.CreateAddressRequest{
		UserId:         buyer1ID,
		RecipientName:  "Buyer One",
		RecipientPhone: "0901000001",
		ProvinceCode:   "79",
		WardCode:       "26734",
		StreetAddress:  "123 Le Loi",
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	addr1ID := addrResp.GetAddress().GetId()

	// 1. Direct handler checks without claims MUST return Unauthenticated (never trust nil claims).
	if _, err := srv.ValidateUser(context.Background(), &userv1.ValidateUserRequest{UserId: buyer1ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anon ValidateUser: want Unauthenticated, got %v", err)
	}
	if _, err := srv.GetProfile(context.Background(), &userv1.GetProfileRequest{UserId: buyer1ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anon GetProfile: want Unauthenticated, got %v", err)
	}
	if _, err := srv.GetAddress(context.Background(), &userv1.GetAddressRequest{UserId: buyer1ID, AddressId: addr1ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anon GetAddress: want Unauthenticated, got %v", err)
	}
	if _, err := srv.WriteAuditLog(context.Background(), &userv1.WriteAuditLogRequest{
		ActorId: "forged", ActorRole: "admin", Action: "fake", TargetType: "user", TargetId: buyer1ID,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anon WriteAuditLog: want Unauthenticated, got %v", err)
	}
	if _, err := srv.Logout(context.Background(), &userv1.LogoutRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anon Logout: want Unauthenticated, got %v", err)
	}

	// 2. Full bufconn gRPC stack with auth.GRPCUnaryServerInterceptor.
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	userv1.RegisterUserServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := userv1.NewUserServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string, kv ...string) context.Context {
		return md(append(kv, "authorization", "Bearer "+tok)...)
	}

	spoof := []string{"x-user-id", buyer1ID, "x-user-role", "admin"}

	// ValidateUser: anon -> Unauthenticated; service token -> PermissionDenied; cross-user -> PermissionDenied; self/admin -> OK.
	if _, err := client.ValidateUser(md(spoof...), &userv1.ValidateUserRequest{UserId: buyer1ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon ValidateUser: want Unauthenticated, got %v", err)
	}
	if _, err := client.ValidateUser(bearer(svcTok), &userv1.ValidateUserRequest{UserId: buyer1ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ValidateUser: want PermissionDenied, got %v", err)
	}
	if _, err := client.ValidateUser(bearer(buyer2Tok), &userv1.ValidateUserRequest{UserId: buyer1ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ValidateUser: want PermissionDenied, got %v", err)
	}
	if valResp, err := client.ValidateUser(bearer(buyer1Tok), &userv1.ValidateUserRequest{UserId: buyer1ID}); err != nil || !valResp.GetValid() {
		t.Fatalf("self ValidateUser: want valid=true, got resp=%+v err=%v", valResp, err)
	}
	if valResp, err := client.ValidateUser(bearer(adminTok), &userv1.ValidateUserRequest{UserId: buyer2ID}); err != nil || !valResp.GetValid() {
		t.Fatalf("admin ValidateUser: want valid=true, got resp=%+v err=%v", valResp, err)
	}

	// GetProfile: anon -> Unauthenticated; cross-user -> PermissionDenied; service token -> OK.
	if _, err := client.GetProfile(md(spoof...), &userv1.GetProfileRequest{UserId: buyer1ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon GetProfile: want Unauthenticated, got %v", err)
	}
	if _, err := client.GetProfile(bearer(buyer2Tok), &userv1.GetProfileRequest{UserId: buyer1ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user GetProfile: want PermissionDenied, got %v", err)
	}
	if profResp, err := client.GetProfile(bearer(svcTok), &userv1.GetProfileRequest{UserId: buyer1ID}); err != nil || profResp.GetUser().GetId() != buyer1ID {
		t.Fatalf("service token GetProfile: want buyer1, got resp=%+v err=%v", profResp, err)
	}

	// GetAddress: anon -> Unauthenticated; cross-user -> PermissionDenied; service token -> OK.
	if _, err := client.GetAddress(md(spoof...), &userv1.GetAddressRequest{UserId: buyer1ID, AddressId: addr1ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon GetAddress: want Unauthenticated, got %v", err)
	}
	if _, err := client.GetAddress(bearer(buyer2Tok), &userv1.GetAddressRequest{UserId: buyer1ID, AddressId: addr1ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user GetAddress: want PermissionDenied, got %v", err)
	}
	if gotAddr, err := client.GetAddress(bearer(svcTok), &userv1.GetAddressRequest{UserId: buyer1ID, AddressId: addr1ID}); err != nil || gotAddr.GetAddress().GetId() != addr1ID {
		t.Fatalf("service token GetAddress: want addr1, got resp=%+v err=%v", gotAddr, err)
	}

	// WriteAuditLog: anon -> Unauthenticated; buyer/seller -> PermissionDenied; admin & service token -> OK.
	auditReq := &userv1.WriteAuditLogRequest{
		Action: "test.audit", TargetType: "user", TargetId: buyer1ID,
	}
	if _, err := client.WriteAuditLog(md(spoof...), auditReq); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon WriteAuditLog: want Unauthenticated, got %v", err)
	}
	if _, err := client.WriteAuditLog(bearer(buyer1Tok), auditReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer WriteAuditLog: want PermissionDenied, got %v", err)
	}
	if _, err := client.WriteAuditLog(bearer(sellerTok), auditReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller WriteAuditLog: want PermissionDenied, got %v", err)
	}
	if _, err := client.WriteAuditLog(bearer(adminTok), auditReq); err != nil {
		t.Fatalf("admin WriteAuditLog: %v", err)
	}
	if _, err := client.WriteAuditLog(bearer(svcTok), auditReq); err != nil {
		t.Fatalf("service token WriteAuditLog: %v", err)
	}

	// Logout: anon -> Unauthenticated; service token -> PermissionDenied; owner -> OK.
	if _, err := client.Logout(md(spoof...), &userv1.LogoutRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon Logout: want Unauthenticated, got %v", err)
	}
	if _, err := client.Logout(bearer(svcTok), &userv1.LogoutRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token Logout: want PermissionDenied, got %v", err)
	}
	if _, err := client.Logout(bearer(buyer2Tok), &userv1.LogoutRequest{}); err != nil {
		t.Fatalf("owner Logout: %v", err)
	}
}
