package service

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func assertPublicShopOnly(t *testing.T, label string, sh *userv1.Shop, wantID, wantName, wantSlug string) {
	t.Helper()
	if sh == nil {
		t.Fatalf("%s: expected non-nil shop", label)
	}
	if sh.GetId() != wantID || sh.GetName() != wantName || sh.GetSlug() != wantSlug || sh.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
		t.Fatalf("%s: unexpected public identity: %+v", label, sh)
	}
	if sh.GetOwnerUserId() != "" ||
		sh.GetPickupAddressId() != "" ||
		sh.GetContactPhone() != "" ||
		sh.GetBusinessLicenseDoc() != "" ||
		sh.GetRejectionReason() != "" ||
		sh.GetSuspendedReason() != "" ||
		sh.GetSuspendedAt() != nil ||
		sh.GetStatusVersion() != 0 ||
		sh.GetBankReviewPending() {
		t.Fatalf("%s: leaked private fields: %+v", label, sh)
	}
}

func assertFullShopFields(t *testing.T, label string, sh *userv1.Shop, want db.Shop) {
	t.Helper()
	if sh == nil {
		t.Fatalf("%s: expected non-nil shop", label)
	}
	if sh.GetId() != want.ID ||
		sh.GetOwnerUserId() != want.OwnerUserID ||
		sh.GetPickupAddressId() != want.PickupAddressID ||
		sh.GetContactPhone() != want.ContactPhone ||
		sh.GetBusinessLicenseDoc() != want.BusinessLicenseDoc ||
		sh.GetRejectionReason() != want.RejectionReason ||
		sh.GetSuspendedReason() != want.SuspendedReason ||
		sh.GetStatusVersion() != want.StatusVersion ||
		sh.GetBankReviewPending() {
		t.Fatalf("%s: full shop mismatch: got=%+v want=%+v", label, sh, want)
	}
}

func TestGetShopVisibilityAndGetShopInternalAuth(t *testing.T) {
	srv, store, _, _, authMgr := newTestHarness(t)
	svcMgr, err := auth.NewManager("user-test-service-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(service): %v", err)
	}

	now := time.Now().UTC()
	susAt := now.Add(-time.Hour)

	seedShop := func(ownerID, name, slug, st, rejReason, susReason string, susTime *time.Time) db.Shop {
		sh := db.Shop{
			ID:                 uuid.NewString(),
			OwnerUserID:        ownerID,
			Name:               name,
			Slug:               slug,
			Description:        "Desc " + name,
			PickupAddressID:    uuid.NewString(),
			ContactPhone:       "0901234567",
			BusinessLicenseDoc: "GPKD-" + slug + ".pdf",
			Status:             st,
			RejectionReason:    rejReason,
			SuspendedReason:    susReason,
			SuspendedAt:        susTime,
			StatusVersion:      2,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		store.mu.Lock()
		store.shops[sh.ID] = sh
		store.mu.Unlock()
		return sh
	}

	ownerActiveID := uuid.NewString()
	ownerPendingID := uuid.NewString()
	ownerRejectedID := uuid.NewString()
	ownerSuspendedID := uuid.NewString()
	otherBuyerID := uuid.NewString()

	activeShop := seedShop(ownerActiveID, "Active Shop", "active-shop", ShopStatusActive, "", "", nil)
	pendingShop := seedShop(ownerPendingID, "Pending Shop", "pending-shop", ShopStatusPending, "", "", nil)
	rejectedShop := seedShop(ownerRejectedID, "Rejected Shop", "rejected-shop", ShopStatusRejected, "Thiếu giấy phép", "", nil)
	suspendedShop := seedShop(ownerSuspendedID, "Suspended Shop", "suspended-shop", ShopStatusSuspended, "", "Vi phạm chính sách", &susAt)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(authMgr, svcMgr)))
	userv1.RegisterUserServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := userv1.NewUserServiceClient(conn)

	signUser := func(userID, role, shopID string) context.Context {
		tok, _, _, sErr := authMgr.SignAccessToken(userID, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken(%s): %v", role, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}
	signSvc := func(serviceName string) context.Context {
		tok, sErr := svcMgr.SignServiceToken(serviceName)
		if sErr != nil {
			t.Fatalf("SignServiceToken(%s): %v", serviceName, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}

	anonCtx := context.Background()
	buyerCtx := signUser(otherBuyerID, RoleBuyer, "")
	ownerActiveCtx := signUser(ownerActiveID, RoleSeller, activeShop.ID)
	ownerPendingCtx := signUser(ownerPendingID, RoleBuyer, "")
	ownerRejectedCtx := signUser(ownerRejectedID, RoleBuyer, "")
	ownerSuspendedCtx := signUser(ownerSuspendedID, RoleSeller, suspendedShop.ID)
	adminCtx := signUser(uuid.NewString(), RoleAdmin, "")
	cskhCtx := signUser(uuid.NewString(), RoleCSKH, "")
	orderSvcCtx := signSvc("order-service")

	// 1. Anonymous & non-owner buyer on ACTIVE shop (by id & slug): only public fields returned.
	for label, callCtx := range map[string]context.Context{"anon": anonCtx, "other-buyer": buyerCtx} {
		byID, err := client.GetShop(callCtx, &userv1.GetShopRequest{ShopId: activeShop.ID})
		if err != nil {
			t.Fatalf("%s GetShop(active id): %v", label, err)
		}
		assertPublicShopOnly(t, label+"-by-id", byID.GetShop(), activeShop.ID, activeShop.Name, activeShop.Slug)

		bySlug, err := client.GetShop(callCtx, &userv1.GetShopRequest{Slug: activeShop.Slug})
		if err != nil {
			t.Fatalf("%s GetShop(active slug): %v", label, err)
		}
		assertPublicShopOnly(t, label+"-by-slug", bySlug.GetShop(), activeShop.ID, activeShop.Name, activeShop.Slug)
	}

	// 2. Anonymous & non-owner buyer on PENDING / REJECTED / SUSPENDED shops: NotFound (both by id and by slug).
	for _, hidden := range []db.Shop{pendingShop, rejectedShop, suspendedShop} {
		for label, callCtx := range map[string]context.Context{"anon": anonCtx, "other-buyer": buyerCtx} {
			if _, err := client.GetShop(callCtx, &userv1.GetShopRequest{ShopId: hidden.ID}); status.Code(err) != codes.NotFound {
				t.Fatalf("%s GetShop(%s by id): want NotFound, got %v", label, hidden.Status, err)
			}
			if _, err := client.GetShop(callCtx, &userv1.GetShopRequest{Slug: hidden.Slug}); status.Code(err) != codes.NotFound {
				t.Fatalf("%s GetShop(%s by slug): want NotFound, got %v", label, hidden.Status, err)
			}
		}
	}

	// 3. Lookup by owner_user_id is restricted strictly to the owner or staff with shops.read.
	if _, err := client.GetShop(anonCtx, &userv1.GetShopRequest{OwnerUserId: ownerActiveID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon GetShop(owner_user_id): want Unauthenticated, got %v", err)
	}
	if _, err := client.GetShop(buyerCtx, &userv1.GetShopRequest{OwnerUserId: ownerActiveID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer GetShop(owner_user_id): want PermissionDenied, got %v", err)
	}

	// 4. Shop owner sees full shop record for their own shop in ANY status (by id, slug, and owner_user_id).
	ownerCases := []struct {
		name string
		ctx  context.Context
		shop db.Shop
	}{
		{"owner-active", ownerActiveCtx, activeShop},
		{"owner-pending", ownerPendingCtx, pendingShop},
		{"owner-rejected", ownerRejectedCtx, rejectedShop},
		{"owner-suspended", ownerSuspendedCtx, suspendedShop},
	}
	for _, tc := range ownerCases {
		for reqLabel, req := range map[string]*userv1.GetShopRequest{
			"by-id":    {ShopId: tc.shop.ID},
			"by-slug":  {Slug: tc.shop.Slug},
			"by-owner": {OwnerUserId: tc.shop.OwnerUserID},
		} {
			resp, err := client.GetShop(tc.ctx, req)
			if err != nil {
				t.Fatalf("%s %s GetShop: %v", tc.name, reqLabel, err)
			}
			assertFullShopFields(t, tc.name+"-"+reqLabel, resp.GetShop(), tc.shop)
		}
	}

	// 5. Staff with shops.read (admin & cskh) see full shop record for ANY status (including by owner_user_id).
	for staffName, staffCtx := range map[string]context.Context{"admin": adminCtx, "cskh": cskhCtx} {
		for _, sh := range []db.Shop{activeShop, pendingShop, rejectedShop, suspendedShop} {
			resp, err := client.GetShop(staffCtx, &userv1.GetShopRequest{ShopId: sh.ID})
			if err != nil {
				t.Fatalf("%s GetShop(%s): %v", staffName, sh.Status, err)
			}
			assertFullShopFields(t, staffName+"-"+sh.Status, resp.GetShop(), sh)
		}
		respByOwner, err := client.GetShop(staffCtx, &userv1.GetShopRequest{OwnerUserId: ownerPendingID})
		if err != nil {
			t.Fatalf("%s GetShop(owner_user_id pending): %v", staffName, err)
		}
		assertFullShopFields(t, staffName+"-owner-pending", respByOwner.GetShop(), pendingShop)
	}

	// 6. Public GetShop rejects service tokens with PermissionDenied (at interceptor AND direct handler).
	if _, err := client.GetShop(orderSvcCtx, &userv1.GetShopRequest{ShopId: activeShop.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token on public GetShop via interceptor: want PermissionDenied, got %v", err)
	}
	if _, err := srv.GetShop(ctxWithService("order-service"), &userv1.GetShopRequest{ShopId: activeShop.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token on public GetShop direct handler: want PermissionDenied, got %v", err)
	}

	// 7. GetShopInternal rejects anonymous, forged metadata, wrong key, expired token, and ALL end-user JWTs.
	forgedMD := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"x-user-id", "order-service", "x-user-role", "admin", "x-shop-id", activeShop.ID,
	))
	wrongSvcMgr, _ := auth.NewManager("wrong-service-secret", auth.ServiceTokenTTL)
	wrongSvcTok, _ := wrongSvcMgr.SignServiceToken("order-service")
	wrongSvcCtx := auth.InjectOutgoingGRPC(context.Background(), wrongSvcTok)

	for label, callCtx := range map[string]context.Context{
		"anonymous":      anonCtx,
		"forged-headers": forgedMD,
		"wrong-svc-key":  wrongSvcCtx,
	} {
		if _, err := client.GetShopInternal(callCtx, &userv1.GetShopInternalRequest{ShopId: activeShop.ID}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("GetShopInternal(%s): want Unauthenticated, got %v", label, err)
		}
	}

	for label, callCtx := range map[string]context.Context{
		"buyer":       buyerCtx,
		"shop-owner":  ownerActiveCtx,
		"admin":       adminCtx,
		"super-admin": signUser(uuid.NewString(), RoleSuperAdmin, ""),
	} {
		if _, err := client.GetShopInternal(callCtx, &userv1.GetShopInternalRequest{ShopId: activeShop.ID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("GetShopInternal(%s JWT): want PermissionDenied, got %v", label, err)
		}
	}

	// 8. GetShopInternal accepts valid service tokens and returns full shop for ALL statuses.
	for _, sh := range []db.Shop{activeShop, pendingShop, rejectedShop, suspendedShop} {
		resp, err := client.GetShopInternal(orderSvcCtx, &userv1.GetShopInternalRequest{ShopId: sh.ID})
		if err != nil {
			t.Fatalf("GetShopInternal(service token, %s): %v", sh.Status, err)
		}
		assertFullShopFields(t, "internal-"+sh.Status, resp.GetShop(), sh)
	}
	byOwnerInt, err := client.GetShopInternal(signSvc("notification-service"), &userv1.GetShopInternalRequest{OwnerUserId: ownerSuspendedID})
	if err != nil {
		t.Fatalf("GetShopInternal(by owner_user_id): %v", err)
	}
	assertFullShopFields(t, "internal-owner-suspended", byOwnerInt.GetShop(), suspendedShop)
}
