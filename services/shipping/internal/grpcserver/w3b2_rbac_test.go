package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/secretbox"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func newW3B2RBACTestServer(t *testing.T) (*Server, *stubRepo) {
	t.Helper()
	repo := newStubRepo()
	agg := carrier.NewAggregator(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"}),
		carrier.NewMockExpressAdapter(),
	)
	cipher, err := secretbox.NewFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=", "SHIPPING_CONFIG_KEY")
	if err != nil {
		t.Fatalf("secretbox.NewFromBase64: %v", err)
	}
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTokenCipher(cipher)
	return New(svc), repo
}

func TestShippingGRPC_W3B2_AdminCarriersRBACMatrix(t *testing.T) {
	srv, _ := newW3B2RBACTestServer(t)

	cases := []struct {
		name      string
		ctx       context.Context
		wantCode  codes.Code
		wantAllow bool
	}{
		{name: "super_admin", ctx: ctxWithRole("sa-1", auth.RoleSuperAdmin, ""), wantAllow: true},
		{name: "admin", ctx: ctxWithRole("adm-1", auth.RoleAdmin, ""), wantAllow: true},
		{name: "ops", ctx: ctxWithRole("ops-1", auth.RoleOps, ""), wantAllow: true},
		{name: "moderator", ctx: ctxWithRole("mod-1", auth.RoleModerator, ""), wantCode: codes.PermissionDenied},
		{name: "cskh", ctx: ctxWithRole("cskh-1", auth.RoleCSKH, ""), wantCode: codes.PermissionDenied},
		{name: "accountant", ctx: ctxWithRole("acc-1", auth.RoleAccountant, ""), wantCode: codes.PermissionDenied},
		{name: "buyer", ctx: ctxWithRole("buyer-1", auth.RoleBuyer, ""), wantCode: codes.PermissionDenied},
		{name: "seller", ctx: ctxWithRole("seller-1", auth.RoleSeller, "shop-1"), wantCode: codes.PermissionDenied},
		{name: "service_token", ctx: ctxWithService("order-service"), wantCode: codes.PermissionDenied},
		{name: "unknown_role", ctx: ctxWithRole("unk-1", "unknown_staff", ""), wantCode: codes.PermissionDenied},
		{name: "anonymous", ctx: context.Background(), wantCode: codes.Unauthenticated},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("AdminListCarriers/"+tc.name, func(t *testing.T) {
			resp, err := srv.AdminListCarriers(tc.ctx, &shippingv1.AdminListCarriersRequest{})
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("AdminListCarriers(%s): expected nil error, got %v", tc.name, err)
				}
				if len(resp.GetCarriers()) < 2 {
					t.Fatalf("AdminListCarriers(%s): expected >=2 carriers, got %d", tc.name, len(resp.GetCarriers()))
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("AdminListCarriers(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})

		t.Run("AdminUpdateCarrier/"+tc.name, func(t *testing.T) {
			resp, err := srv.AdminUpdateCarrier(tc.ctx, &shippingv1.AdminUpdateCarrierRequest{
				CarrierCode: carrier.CodeMockExpress,
				Enabled:     true,
			})
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("AdminUpdateCarrier(%s): expected nil error, got %v", tc.name, err)
				}
				if resp.GetCarrier().GetCarrierCode() != carrier.CodeMockExpress {
					t.Fatalf("AdminUpdateCarrier(%s): unexpected carrier=%+v", tc.name, resp.GetCarrier())
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("AdminUpdateCarrier(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})

		t.Run("AdminTestCarrier/"+tc.name, func(t *testing.T) {
			resp, err := srv.AdminTestCarrier(tc.ctx, &shippingv1.AdminTestCarrierRequest{
				CarrierCode: carrier.CodeGHN,
			})
			if tc.wantAllow {
				if err != nil || !resp.GetOk() {
					t.Fatalf("AdminTestCarrier(%s): expected ok=true, got resp=%+v err=%v", tc.name, resp, err)
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("AdminTestCarrier(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})
	}
}

func TestShippingGRPC_W3B2_GetShipmentRBACMatrix(t *testing.T) {
	srv, repo := newW3B2RBACTestServer(t)

	_, _, err := repo.CreateShipment(context.Background(), &store.Shipment{
		ID:           uuid.NewString(),
		OrderID:      "ord-w3b2-get-1",
		ShopID:       "shop-1",
		BuyerID:      "buyer-owner",
		Direction:    store.DirectionForward,
		CarrierCode:  carrier.CodeGHN,
		ServiceCode:  "STANDARD",
		TrackingCode: "GHN-W3B2-GET-1",
		Status:       carrier.StatusCreated,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}, nil)
	if err != nil {
		t.Fatalf("seed shipment: %v", err)
	}

	req := &shippingv1.GetShipmentRequest{OrderId: "ord-w3b2-get-1"}

	cases := []struct {
		name      string
		ctx       context.Context
		wantAllow bool
		wantCode  codes.Code
	}{
		{name: "super_admin", ctx: ctxWithRole("sa-1", auth.RoleSuperAdmin, ""), wantAllow: true},
		{name: "admin", ctx: ctxWithRole("adm-1", auth.RoleAdmin, ""), wantAllow: true},
		{name: "cskh", ctx: ctxWithRole("cskh-1", auth.RoleCSKH, ""), wantAllow: true},
		{name: "accountant", ctx: ctxWithRole("acc-1", auth.RoleAccountant, ""), wantAllow: true},
		{name: "ops", ctx: ctxWithRole("ops-1", auth.RoleOps, ""), wantAllow: true},
		{name: "moderator_denied", ctx: ctxWithRole("mod-1", auth.RoleModerator, ""), wantCode: codes.PermissionDenied},
		{name: "moderator_with_stale_shop_id_denied", ctx: ctxWithRole("mod-2", auth.RoleModerator, "shop-1"), wantCode: codes.PermissionDenied},
		{name: "buyer_owner", ctx: ctxWithRole("buyer-owner", auth.RoleBuyer, ""), wantAllow: true},
		{name: "other_buyer_denied", ctx: ctxWithRole("buyer-other", auth.RoleBuyer, ""), wantCode: codes.PermissionDenied},
		{name: "seller_owner", ctx: ctxWithRole("seller-1", auth.RoleSeller, "shop-1"), wantAllow: true},
		{name: "other_seller_denied", ctx: ctxWithRole("seller-2", auth.RoleSeller, "shop-2"), wantCode: codes.PermissionDenied},
		{name: "service_token", ctx: ctxWithService("order-service"), wantAllow: true},
		{name: "anonymous", ctx: context.Background(), wantCode: codes.Unauthenticated},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := srv.GetShipment(tc.ctx, req)
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("GetShipment(%s): expected nil error, got %v", tc.name, err)
				}
				if resp.GetShipment().GetTrackingCode() != "GHN-W3B2-GET-1" {
					t.Fatalf("GetShipment(%s): unexpected shipment=%+v", tc.name, resp.GetShipment())
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("GetShipment(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})
	}
}

func TestShippingGRPC_W3B2_CancelAndReturnShipmentRBACMatrix(t *testing.T) {
	cases := []struct {
		name      string
		ctx       context.Context
		wantAllow bool
		wantCode  codes.Code
	}{
		{name: "service_token", ctx: ctxWithService("order-service"), wantAllow: true},
		{name: "super_admin", ctx: ctxWithRole("sa-1", auth.RoleSuperAdmin, ""), wantAllow: true},
		{name: "admin", ctx: ctxWithRole("adm-1", auth.RoleAdmin, ""), wantAllow: true},
		{name: "ops", ctx: ctxWithRole("ops-1", auth.RoleOps, ""), wantAllow: true},
		{name: "moderator", ctx: ctxWithRole("mod-1", auth.RoleModerator, ""), wantCode: codes.PermissionDenied},
		{name: "cskh", ctx: ctxWithRole("cskh-1", auth.RoleCSKH, ""), wantCode: codes.PermissionDenied},
		{name: "accountant", ctx: ctxWithRole("acc-1", auth.RoleAccountant, ""), wantCode: codes.PermissionDenied},
		{name: "buyer_owner", ctx: ctxWithRole("buyer-1", auth.RoleBuyer, ""), wantCode: codes.PermissionDenied},
		{name: "seller_owner", ctx: ctxWithRole("seller-1", auth.RoleSeller, "shop-1"), wantCode: codes.PermissionDenied},
		{name: "anonymous", ctx: context.Background(), wantCode: codes.Unauthenticated},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("CancelShipment/"+tc.name, func(t *testing.T) {
			srv, repo := newW3B2RBACTestServer(t)
			orderID := "ord-cancel-" + tc.name
			_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
				ID:           uuid.NewString(),
				OrderID:      orderID,
				ShopID:       "shop-1",
				BuyerID:      "buyer-1",
				Direction:    store.DirectionForward,
				CarrierCode:  carrier.CodeGHN,
				TrackingCode: "GHN-CXL-" + tc.name,
				Status:       carrier.StatusCreated,
				CreatedAt:    time.Now().UTC(),
				UpdatedAt:    time.Now().UTC(),
			}, nil)

			resp, err := srv.CancelShipment(tc.ctx, &shippingv1.CancelShipmentRequest{
				OrderId: orderID,
				Reason:  "rbac test",
			})
			if tc.wantAllow {
				if err != nil || resp.GetShipment().GetStatus() != shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED {
					t.Fatalf("CancelShipment(%s): expected cancelled shipment, got resp=%+v err=%v", tc.name, resp, err)
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("CancelShipment(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})

		t.Run("CreateReturnShipment/"+tc.name, func(t *testing.T) {
			srv, _ := newW3B2RBACTestServer(t)
			resp, err := srv.CreateReturnShipment(tc.ctx, &shippingv1.CreateReturnShipmentRequest{
				OrderId:         "ord-ret-" + tc.name,
				ReturnRequestId: "rr-" + tc.name,
				ShopId:          "shop-1",
				BuyerId:         "buyer-1",
				CarrierCode:     carrier.CodeGHN,
				WeightGrams:     500,
				FromAddress:     &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "26740"},
				ToAddress:       &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "00070"},
			})
			if tc.wantAllow {
				if err != nil || resp.GetShipment().GetDirection() != shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN {
					t.Fatalf("CreateReturnShipment(%s): expected return shipment, got resp=%+v err=%v", tc.name, resp, err)
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("CreateReturnShipment(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})
	}
}

func TestShippingGRPC_W3B2_ShopSettingsAndCreateShipmentRBACMatrix(t *testing.T) {
	srv, repo := newW3B2RBACTestServer(t)
	_, err := repo.UpsertShopShippingSettings(context.Background(), &store.ShopShippingSettings{
		ShopID:              "shop-1",
		EnabledCarrierCodes: []string{carrier.CodeGHN},
		DefaultWeightGrams:  1200,
		DefaultLengthCM:     25,
		DefaultWidthCM:      20,
		DefaultHeightCM:     15,
		FreeShipEnabled:     true,
		FreeShipMinOrderVND: 200000,
		FreeShipMaxVND:      30000,
	})
	if err != nil {
		t.Fatalf("seed shop settings: %v", err)
	}

	// 1. GetShopShippingSettings: full settings for service token, owning seller, and staff with carriers.read or shops.review
	// (super_admin, admin, ops, moderator); public projection only for cskh, accountant, buyer, other seller.
	getCases := []struct {
		name     string
		ctx      context.Context
		wantFull bool
		wantErr  codes.Code
	}{
		{name: "service_token", ctx: ctxWithService("order-service"), wantFull: true},
		{name: "seller_owner", ctx: ctxWithRole("seller-1", auth.RoleSeller, "shop-1"), wantFull: true},
		{name: "super_admin", ctx: ctxWithRole("sa-1", auth.RoleSuperAdmin, ""), wantFull: true},
		{name: "admin", ctx: ctxWithRole("adm-1", auth.RoleAdmin, ""), wantFull: true},
		{name: "ops", ctx: ctxWithRole("ops-1", auth.RoleOps, ""), wantFull: true},
		{name: "moderator", ctx: ctxWithRole("mod-1", auth.RoleModerator, ""), wantFull: true},
		{name: "cskh_public_only", ctx: ctxWithRole("cskh-1", auth.RoleCSKH, ""), wantFull: false},
		{name: "cskh_stale_shop_public_only", ctx: ctxWithRole("cskh-2", auth.RoleCSKH, "shop-1"), wantFull: false},
		{name: "accountant_public_only", ctx: ctxWithRole("acc-1", auth.RoleAccountant, ""), wantFull: false},
		{name: "buyer_public_only", ctx: ctxWithRole("buyer-1", auth.RoleBuyer, ""), wantFull: false},
		{name: "other_seller_public_only", ctx: ctxWithRole("seller-2", auth.RoleSeller, "shop-2"), wantFull: false},
		{name: "anonymous", ctx: context.Background(), wantErr: codes.Unauthenticated},
	}

	for _, tc := range getCases {
		tc := tc
		t.Run("GetShopShippingSettings/"+tc.name, func(t *testing.T) {
			resp, err := srv.GetShopShippingSettings(tc.ctx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-1"})
			if tc.wantErr != codes.OK {
				if status.Code(err) != tc.wantErr {
					t.Fatalf("GetShopShippingSettings(%s): code=%v, want %v", tc.name, status.Code(err), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetShopShippingSettings(%s): unexpected err=%v", tc.name, err)
			}
			st := resp.GetSettings()
			if st.GetShopId() != "shop-1" || st.GetDefaultWeightGrams() != 1200 || !st.GetFreeShipEnabled() {
				t.Fatalf("GetShopShippingSettings(%s): missing baseline fields: %+v", tc.name, st)
			}
			if tc.wantFull {
				if len(st.GetEnabledCarrierCodes()) != 1 || st.GetDefaultLengthCm() != 25 || st.GetUpdatedAt() == nil {
					t.Fatalf("GetShopShippingSettings(%s): expected full settings, got %+v", tc.name, st)
				}
			} else {
				if len(st.GetEnabledCarrierCodes()) != 0 || st.GetDefaultLengthCm() != 0 || st.GetDefaultWidthCm() != 0 || st.GetDefaultHeightCm() != 0 || st.GetUpdatedAt() != nil {
					t.Fatalf("GetShopShippingSettings(%s): expected public projection only, got %+v", tc.name, st)
				}
			}
		})
	}

	// 2. UpdateShopShippingSettings & CreateShipment: seller owner + super_admin/admin/ops allowed;
	// moderator, cskh, accountant, buyer, other seller, service token denied.
	mutCases := []struct {
		name      string
		ctx       context.Context
		wantAllow bool
		wantCode  codes.Code
	}{
		{name: "seller_owner", ctx: ctxWithRole("seller-1", auth.RoleSeller, "shop-1"), wantAllow: true},
		{name: "super_admin", ctx: ctxWithRole("sa-1", auth.RoleSuperAdmin, ""), wantAllow: true},
		{name: "admin", ctx: ctxWithRole("adm-1", auth.RoleAdmin, ""), wantAllow: true},
		{name: "ops", ctx: ctxWithRole("ops-1", auth.RoleOps, ""), wantAllow: true},
		{name: "moderator", ctx: ctxWithRole("mod-1", auth.RoleModerator, ""), wantCode: codes.PermissionDenied},
		{name: "cskh", ctx: ctxWithRole("cskh-1", auth.RoleCSKH, ""), wantCode: codes.PermissionDenied},
		{name: "accountant", ctx: ctxWithRole("acc-1", auth.RoleAccountant, ""), wantCode: codes.PermissionDenied},
		{name: "buyer", ctx: ctxWithRole("buyer-1", auth.RoleBuyer, ""), wantCode: codes.PermissionDenied},
		{name: "other_seller", ctx: ctxWithRole("seller-2", auth.RoleSeller, "shop-2"), wantCode: codes.PermissionDenied},
		{name: "service_token", ctx: ctxWithService("order-service"), wantCode: codes.PermissionDenied},
		{name: "anonymous", ctx: context.Background(), wantCode: codes.Unauthenticated},
	}

	for _, tc := range mutCases {
		tc := tc
		t.Run("UpdateShopShippingSettings/"+tc.name, func(t *testing.T) {
			_, err := srv.UpdateShopShippingSettings(tc.ctx, &shippingv1.UpdateShopShippingSettingsRequest{
				ShopId:              "shop-1",
				EnabledCarrierCodes: []string{carrier.CodeGHN},
				DefaultWeightGrams:  1200,
			})
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("UpdateShopShippingSettings(%s): expected nil error, got %v", tc.name, err)
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("UpdateShopShippingSettings(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})

		t.Run("CreateShipment/"+tc.name, func(t *testing.T) {
			_, err := srv.CreateShipment(tc.ctx, &shippingv1.CreateShipmentRequest{
				OrderId:     "ord-create-" + tc.name,
				ShopId:      "shop-1",
				BuyerId:     "buyer-1",
				CarrierCode: carrier.CodeGHN,
				WeightGrams: 500,
			})
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("CreateShipment(%s): expected nil error, got %v", tc.name, err)
				}
				return
			}
			if status.Code(err) != tc.wantCode {
				t.Fatalf("CreateShipment(%s): code=%v, want %v (err=%v)", tc.name, status.Code(err), tc.wantCode, err)
			}
		})
	}
}

func TestShippingGRPC_W3B2_InterceptorDefenseInDepth(t *testing.T) {
	srv, repo := newW3B2RBACTestServer(t)
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:           uuid.NewString(),
		OrderID:      "ord-e2e-int-1",
		ShopID:       "shop-1",
		BuyerID:      "buyer-1",
		Direction:    store.DirectionForward,
		CarrierCode:  carrier.CodeGHN,
		TrackingCode: "GHN-INT-001",
		Status:       carrier.StatusCreated,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}, nil)

	userMgr, err := auth.NewManager("test-w3b2-user-jwt-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-w3b2-svc-jwt-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	shippingv1.RegisterShippingServiceServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(func() {
		grpcSrv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := shippingv1.NewShippingServiceClient(conn)

	signUser := func(uid, role, shopID string) context.Context {
		tok, _, _, sErr := userMgr.SignAccessToken(uid, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken(%s): %v", role, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}

	opsCtx := signUser("ops-1", auth.RoleOps, "")
	cskhCtx := signUser("cskh-1", auth.RoleCSKH, "")
	modCtx := signUser("mod-1", auth.RoleModerator, "")
	// Attack: moderator attaches spoofed x-user-role: admin metadata alongside real JWT.
	modSpoofCtx := metadata.AppendToOutgoingContext(modCtx, "x-user-role", "admin", "x-user-id", "buyer-1", "x-shop-id", "shop-1")
	svcTok, err := svcMgr.SignServiceToken("order-service")
	if err != nil {
		t.Fatalf("SignServiceToken: %v", err)
	}
	svcCtx := auth.InjectOutgoingGRPC(context.Background(), svcTok)

	// ops can list/test carriers and read shipment.
	if _, err := client.AdminListCarriers(opsCtx, &shippingv1.AdminListCarriersRequest{}); err != nil {
		t.Fatalf("ops AdminListCarriers failed: %v", err)
	}
	if _, err := client.AdminTestCarrier(opsCtx, &shippingv1.AdminTestCarrierRequest{CarrierCode: carrier.CodeGHN}); err != nil {
		t.Fatalf("ops AdminTestCarrier failed: %v", err)
	}
	if _, err := client.GetShipment(opsCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-e2e-int-1"}); err != nil {
		t.Fatalf("ops GetShipment failed: %v", err)
	}

	// cskh can read shipment, denied AdminListCarriers.
	if _, err := client.GetShipment(cskhCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-e2e-int-1"}); err != nil {
		t.Fatalf("cskh GetShipment failed: %v", err)
	}
	if _, err := client.AdminListCarriers(cskhCtx, &shippingv1.AdminListCarriersRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cskh AdminListCarriers = %v, want PermissionDenied", err)
	}

	// Attack: moderator with spoofed admin/owner metadata is denied on both AdminListCarriers and GetShipment.
	if _, err := client.AdminListCarriers(modSpoofCtx, &shippingv1.AdminListCarriersRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("moderator spoofed AdminListCarriers = %v, want PermissionDenied", err)
	}
	if _, err := client.GetShipment(modSpoofCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-e2e-int-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("moderator spoofed GetShipment = %v, want PermissionDenied", err)
	}

	// Attack: service token is rejected by interceptor on AdminListCarriers, accepted on GetShipment/CancelShipment.
	if _, err := client.AdminListCarriers(svcCtx, &shippingv1.AdminListCarriersRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token AdminListCarriers = %v, want PermissionDenied", err)
	}
	if _, err := client.GetShipment(svcCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-e2e-int-1"}); err != nil {
		t.Fatalf("service token GetShipment failed: %v", err)
	}
}
