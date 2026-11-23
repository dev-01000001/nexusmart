package service

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ctxWithService(serviceName string) context.Context {
	claims := &auth.Claims{
		Role:    auth.ServiceRole,
		Service: serviceName,
	}
	claims.Subject = serviceName
	claims.ID = uuid.NewString()
	return auth.WithClaims(context.Background(), claims)
}

func registerActiveUser(t *testing.T, srv *Server, pub *memPublisher, email, phone string) string {
	t.Helper()
	ctx := context.Background()
	reg, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Test User " + email,
		Email:    email,
		Phone:    phone,
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Register(%s): %v", email, err)
	}
	otp := pub.userRegistered[len(pub.userRegistered)-1].OTP
	if _, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: email,
		Otp:   otp,
	}); err != nil {
		t.Fatalf("VerifyOTP(%s): %v", email, err)
	}
	return reg.GetUserId()
}

// 1. R6: RecordCODRejection (service token only, idempotent per order_id).
func TestRecordCODRejection_AuthAndIdempotency(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	buyerID := registerActiveUser(t, srv, pub, "codbuyer@example.com", "0901112233")
	adminID := registerActiveUser(t, srv, pub, "codadmin@example.com", "0901112244")

	// Anonymous -> Unauthenticated.
	if _, err := srv.RecordCODRejection(context.Background(), &userv1.RecordCODRejectionRequest{
		UserId:  buyerID,
		OrderId: "order-1",
		Reason:  "refused delivery",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous RecordCODRejection: want Unauthenticated, got %v", err)
	}

	// Buyer JWT -> PermissionDenied.
	if _, err := srv.RecordCODRejection(ctxWithUser(buyerID, RoleBuyer, ""), &userv1.RecordCODRejectionRequest{
		UserId:  buyerID,
		OrderId: "order-1",
		Reason:  "refused delivery",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer RecordCODRejection: want PermissionDenied, got %v", err)
	}

	// End-user Admin JWT -> PermissionDenied (service token only per R6).
	if _, err := srv.RecordCODRejection(ctxWithUser(adminID, RoleAdmin, ""), &userv1.RecordCODRejectionRequest{
		UserId:  buyerID,
		OrderId: "order-1",
		Reason:  "refused delivery",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("admin user JWT RecordCODRejection: want PermissionDenied, got %v", err)
	}

	svcCtx := ctxWithService("order-service")

	// Missing fields -> InvalidArgument.
	if _, err := srv.RecordCODRejection(svcCtx, &userv1.RecordCODRejectionRequest{
		UserId: buyerID,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing order_id: want InvalidArgument, got %v", err)
	}

	// Unknown user -> NotFound.
	if _, err := srv.RecordCODRejection(svcCtx, &userv1.RecordCODRejectionRequest{
		UserId:  uuid.NewString(),
		OrderId: "order-unknown",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown user: want NotFound, got %v", err)
	}

	// First record -> count=1, already_recorded=false.
	resp1, err := srv.RecordCODRejection(svcCtx, &userv1.RecordCODRejectionRequest{
		UserId:  buyerID,
		OrderId: "order-cod-1",
		Reason:  "buyer refused COD",
	})
	if err != nil {
		t.Fatalf("first RecordCODRejection failed: %v", err)
	}
	if resp1.GetCodRejectedCount() != 1 || resp1.GetAlreadyRecorded() {
		t.Fatalf("first RecordCODRejection: want count=1 already=false, got count=%d already=%v",
			resp1.GetCodRejectedCount(), resp1.GetAlreadyRecorded())
	}

	// Replay same order_id -> count stays 1, already_recorded=true.
	respReplay, err := srv.RecordCODRejection(svcCtx, &userv1.RecordCODRejectionRequest{
		UserId:  buyerID,
		OrderId: "order-cod-1",
		Reason:  "duplicate delivery event",
	})
	if err != nil {
		t.Fatalf("replay RecordCODRejection failed: %v", err)
	}
	if respReplay.GetCodRejectedCount() != 1 || !respReplay.GetAlreadyRecorded() {
		t.Fatalf("replay RecordCODRejection: want count=1 already=true, got count=%d already=%v",
			respReplay.GetCodRejectedCount(), respReplay.GetAlreadyRecorded())
	}

	// Distinct second order -> count=2, already_recorded=false.
	resp2, err := srv.RecordCODRejection(svcCtx, &userv1.RecordCODRejectionRequest{
		UserId:  buyerID,
		OrderId: "order-cod-2",
		Reason:  "second refusal",
	})
	if err != nil {
		t.Fatalf("second RecordCODRejection failed: %v", err)
	}
	if resp2.GetCodRejectedCount() != 2 || resp2.GetAlreadyRecorded() {
		t.Fatalf("second RecordCODRejection: want count=2 already=false, got count=%d already=%v",
			resp2.GetCodRejectedCount(), resp2.GetAlreadyRecorded())
	}

	// ValidateUser reflects incremented count.
	valResp, err := srv.ValidateUser(ctxWithUser(buyerID, RoleBuyer, ""), &userv1.ValidateUserRequest{
		UserId: buyerID,
	})
	if err != nil || valResp.GetCodRejectedCount() != 2 {
		t.Fatalf("ValidateUser cod_rejected_count: want 2, got %d err=%v", valResp.GetCodRejectedCount(), err)
	}
}

// 2. USR-B2: Shop required fields, required review reason, resubmission after REQUEST_INFO and REJECTED.
func TestShopRegisterValidationAndResubmit(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ownerID := registerActiveUser(t, srv, pub, "shopresub@example.com", "0902223344")
	otherID := registerActiveUser(t, srv, pub, "otherowner@example.com", "0902223355")
	adminID := registerActiveUser(t, srv, pub, "shopadmin@example.com", "0902223366")

	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	otherCtx := ctxWithUser(otherID, RoleBuyer, "")
	adminCtx := ctxWithUser(adminID, RoleAdmin, "")

	ownerAddr, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Kho Chủ Shop",
		RecipientPhone: "0902223344",
		ProvinceCode:   "79",
		ProvinceName:   "Thành phố Hồ Chí Minh",
		WardCode:       "79001",
		WardName:       "Phường Sài Gòn",
		StreetAddress:  "100 Nguyễn Huệ",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress owner: %v", err)
	}
	otherAddr, err := srv.CreateAddress(otherCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Kho Người Khác",
		RecipientPhone: "0902223355",
		ProvinceCode:   "01",
		ProvinceName:   "Thành phố Hà Nội",
		WardCode:       "01001",
		WardName:       "Phường Hoàn Kiếm",
		StreetAddress:  "10 Tràng Tiền",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress other: %v", err)
	}

	// Missing pickup_address_id -> InvalidArgument.
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Hợp Lệ",
		ContactPhone:       "0902223344",
		BusinessLicenseDoc: "GPKD-001.pdf",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing pickup_address_id: want InvalidArgument, got %v", err)
	}

	// Pickup address belongs to another user -> PermissionDenied.
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Hợp Lệ",
		PickupAddressId:    otherAddr.GetAddress().GetId(),
		ContactPhone:       "0902223344",
		BusinessLicenseDoc: "GPKD-001.pdf",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other user's pickup_address_id: want PermissionDenied, got %v", err)
	}

	// Invalid contact_phone -> InvalidArgument.
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Hợp Lệ",
		PickupAddressId:    ownerAddr.GetAddress().GetId(),
		ContactPhone:       "NOT-A-PHONE",
		BusinessLicenseDoc: "GPKD-001.pdf",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid contact_phone: want InvalidArgument, got %v", err)
	}

	// Missing business_license_doc -> InvalidArgument.
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:            "Xưởng Hợp Lệ",
		PickupAddressId: ownerAddr.GetAddress().GetId(),
		ContactPhone:    "0902223344",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing business_license_doc: want InvalidArgument, got %v", err)
	}

	// Valid registration succeeds.
	created, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Kim Cương",
		Description:        "Hồ sơ lần 1",
		PickupAddressId:    ownerAddr.GetAddress().GetId(),
		ContactPhone:       "0902223344",
		BusinessLicenseDoc: "GPKD-v1.pdf",
	})
	if err != nil || created.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_PENDING {
		t.Fatalf("valid RegisterShop failed: resp=%+v err=%v", created, err)
	}
	shopID := created.GetShop().GetId()

	// Fresh PENDING shop cannot be registered again -> AlreadyExists.
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Kim Cương",
		PickupAddressId:    ownerAddr.GetAddress().GetId(),
		ContactPhone:       "0902223344",
		BusinessLicenseDoc: "GPKD-v2.pdf",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("resubmit fresh PENDING shop: want AlreadyExists, got %v", err)
	}

	// REQUEST_INFO with empty reason -> InvalidArgument.
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopID,
		Decision: userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO,
		Reason:   "   ",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("REQUEST_INFO empty reason: want InvalidArgument, got %v", err)
	}

	// REQUEST_INFO with reason succeeds.
	reqInfoResp, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopID,
		Decision: userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO,
		Reason:   "Bổ sung bản scan GPKD rõ nét",
	})
	if err != nil || reqInfoResp.GetShop().GetRejectionReason() != "Bổ sung bản scan GPKD rõ nét" {
		t.Fatalf("REQUEST_INFO failed: resp=%+v err=%v", reqInfoResp, err)
	}

	// Resubmit after REQUEST_INFO reusing SAME shop name -> updates existing shop back to clean PENDING.
	resub1, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Kim Cương",
		Description:        "Hồ sơ bổ sung GPKD",
		PickupAddressId:    ownerAddr.GetAddress().GetId(),
		ContactPhone:       "0902223344",
		BusinessLicenseDoc: "GPKD-v2-hd.pdf",
	})
	if err != nil {
		t.Fatalf("resubmit after REQUEST_INFO failed: %v", err)
	}
	if resub1.GetShop().GetId() != shopID ||
		resub1.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_PENDING ||
		resub1.GetShop().GetRejectionReason() != "" ||
		resub1.GetShop().GetBusinessLicenseDoc() != "GPKD-v2-hd.pdf" {
		t.Fatalf("unexpected shop after REQUEST_INFO resubmit: %+v", resub1.GetShop())
	}

	// REJECT with empty reason -> InvalidArgument.
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopID,
		Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT,
		Reason:   "",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("REJECT empty reason: want InvalidArgument, got %v", err)
	}

	// REJECT with reason -> REJECTED.
	rejResp, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopID,
		Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT,
		Reason:   "GPKD hết hạn",
	})
	if err != nil || rejResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_REJECTED {
		t.Fatalf("REJECT failed: resp=%+v err=%v", rejResp, err)
	}

	// Resubmit after REJECTED reusing SAME shop name -> updates existing shop to PENDING.
	resub2, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Kim Cương",
		Description:        "Hồ sơ nộp lại lần 3",
		PickupAddressId:    ownerAddr.GetAddress().GetId(),
		ContactPhone:       "0902223344",
		BusinessLicenseDoc: "GPKD-v3-renewed.pdf",
	})
	if err != nil {
		t.Fatalf("resubmit after REJECTED with same name failed: %v", err)
	}
	if resub2.GetShop().GetId() != shopID ||
		resub2.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_PENDING ||
		resub2.GetShop().GetRejectionReason() != "" {
		t.Fatalf("unexpected shop after REJECTED resubmit: %+v", resub2.GetShop())
	}

	// Another user still cannot take this shop's name.
	if _, err := srv.RegisterShop(otherCtx, &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Kim Cương",
		PickupAddressId:    otherAddr.GetAddress().GetId(),
		ContactPhone:       "0902223355",
		BusinessLicenseDoc: "GPKD-other.pdf",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("other user taking existing shop name: want AlreadyExists, got %v", err)
	}
}

// 3. USR-B3: AdminReviewShop only allows transitions from PENDING; review on ACTIVE/REJECTED -> FailedPrecondition (409).
func TestAdminReviewShop_OnlyFromPending(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ownerID := registerActiveUser(t, srv, pub, "guardowner@example.com", "0903334455")
	owner2ID := registerActiveUser(t, srv, pub, "guardowner2@example.com", "0903334466")
	adminID := registerActiveUser(t, srv, pub, "guardadmin@example.com", "0903334477")

	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	owner2Ctx := ctxWithUser(owner2ID, RoleBuyer, "")
	adminCtx := ctxWithUser(adminID, RoleAdmin, "")

	addr1, _ := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Kho 1",
		RecipientPhone: "0903334455",
		ProvinceCode:   "79",
		WardCode:       "79001",
		StreetAddress:  "1 Lê Lợi",
		IsDefault:      true,
	})
	addr2, _ := srv.CreateAddress(owner2Ctx, &userv1.CreateAddressRequest{
		RecipientName:  "Kho 2",
		RecipientPhone: "0903334466",
		ProvinceCode:   "79",
		WardCode:       "79001",
		StreetAddress:  "2 Lê Lợi",
		IsDefault:      true,
	})

	shop1, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Shop Guard Active",
		PickupAddressId:    addr1.GetAddress().GetId(),
		ContactPhone:       "0903334455",
		BusinessLicenseDoc: "GPKD-A.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop 1: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shop1.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("APPROVE shop 1: %v", err)
	}

	// Reviewing an ACTIVE shop with REJECT / REQUEST_INFO / APPROVE must all fail with FailedPrecondition (409).
	for _, tc := range []struct {
		name     string
		decision userv1.ReviewDecision
		reason   string
	}{
		{"REJECT active", userv1.ReviewDecision_REVIEW_DECISION_REJECT, "attempt reject active"},
		{"REQUEST_INFO active", userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO, "attempt request info active"},
		{"APPROVE active", userv1.ReviewDecision_REVIEW_DECISION_APPROVE, ""},
	} {
		if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
			ShopId:   shop1.GetShop().GetId(),
			Decision: tc.decision,
			Reason:   tc.reason,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("%s: want FailedPrecondition, got %v", tc.name, err)
		}
	}

	// Reviewing a REJECTED shop without resubmission also fails with FailedPrecondition.
	shop2, err := srv.RegisterShop(owner2Ctx, &userv1.RegisterShopRequest{
		Name:               "Shop Guard Rejected",
		PickupAddressId:    addr2.GetAddress().GetId(),
		ContactPhone:       "0903334466",
		BusinessLicenseDoc: "GPKD-B.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop 2: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shop2.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT,
		Reason:   "thiếu thông tin",
	}); err != nil {
		t.Fatalf("REJECT shop 2: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shop2.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("APPROVE rejected shop: want FailedPrecondition, got %v", err)
	}
}

// 4. USR-B6: VN phone validation, PUT preserving is_default, blocking delete of ACTIVE shop pickup address.
func TestAddressValidationUpdateDefaultAndPickupDeleteGuard(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	userID := registerActiveUser(t, srv, pub, "addruser@example.com", "0904445566")
	adminID := registerActiveUser(t, srv, pub, "addradmin@example.com", "0904445577")

	userCtx := ctxWithUser(userID, RoleBuyer, "")
	adminCtx := ctxWithUser(adminID, RoleAdmin, "")

	// Invalid VN phone rejected.
	for _, badPhone := range []string{"NOT-A-PHONE", "0123456789", "09012345", "1234567890"} {
		if _, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
			RecipientName:  "Nguyen Van A",
			RecipientPhone: badPhone,
			ProvinceCode:   "79",
			WardCode:       "79001",
			StreetAddress:  "123 Le Loi",
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("CreateAddress bad phone %q: want InvalidArgument, got %v", badPhone, err)
		}
	}

	// Valid VN phone succeeds with is_default=true.
	addr1Resp, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van A",
		RecipientPhone: "0904445566",
		ProvinceCode:   "79",
		ProvinceName:   "Thành phố Hồ Chí Minh",
		WardCode:       "79001",
		WardName:       "Phường Sài Gòn",
		StreetAddress:  "123 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress valid: %v", err)
	}
	addr1ID := addr1Resp.GetAddress().GetId()

	// UpdateAddress with invalid phone -> InvalidArgument.
	if _, err := srv.UpdateAddress(userCtx, &userv1.UpdateAddressRequest{
		AddressId:      addr1ID,
		RecipientPhone: "INVALID",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateAddress invalid phone: want InvalidArgument, got %v", err)
	}

	// UpdateAddress omitting is_default (false in proto3) MUST NOT reset is_default from true to false.
	updResp, err := srv.UpdateAddress(userCtx, &userv1.UpdateAddressRequest{
		AddressId:     addr1ID,
		StreetAddress: "Số 22 đường Xuân Thủy",
	})
	if err != nil {
		t.Fatalf("UpdateAddress street only: %v", err)
	}
	if !updResp.GetAddress().GetIsDefault() {
		t.Fatalf("UpdateAddress omitted is_default reset default flag to false; want true")
	}

	// Creating second non-default address, then explicitly setting it default clears addr1.
	addr2Resp, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van B",
		RecipientPhone: "+84904445599",
		ProvinceCode:   "01",
		WardCode:       "01001",
		StreetAddress:  "456 Tran Phu",
		IsDefault:      false,
	})
	if err != nil {
		t.Fatalf("CreateAddress second: %v", err)
	}
	addr2ID := addr2Resp.GetAddress().GetId()

	upd2Resp, err := srv.UpdateAddress(userCtx, &userv1.UpdateAddressRequest{
		AddressId: addr2ID,
		IsDefault: true,
	})
	if err != nil || !upd2Resp.GetAddress().GetIsDefault() {
		t.Fatalf("UpdateAddress set default true failed: resp=%+v err=%v", upd2Resp, err)
	}
	got1, _ := srv.GetAddress(userCtx, &userv1.GetAddressRequest{AddressId: addr1ID})
	if got1.GetAddress().GetIsDefault() {
		t.Fatalf("expected previous default address to be cleared when new address set default=true")
	}

	// Register + approve shop using addr1ID as pickup_address_id.
	shopResp, err := srv.RegisterShop(userCtx, &userv1.RegisterShopRequest{
		Name:               "Shop Kho Guard",
		PickupAddressId:    addr1ID,
		ContactPhone:       "0904445566",
		BusinessLicenseDoc: "GPKD-KHO.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopResp.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewShop approve: %v", err)
	}

	// Deleting pickup address of an ACTIVE shop must be rejected with FailedPrecondition (409).
	if _, err := srv.DeleteAddress(userCtx, &userv1.DeleteAddressRequest{
		AddressId: addr1ID,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("DeleteAddress active shop pickup: want FailedPrecondition, got %v", err)
	}

	// Deleting another non-pickup address of the same seller still succeeds.
	if _, err := srv.DeleteAddress(userCtx, &userv1.DeleteAddressRequest{
		AddressId: addr2ID,
	}); err != nil {
		t.Fatalf("DeleteAddress non-pickup address failed: %v", err)
	}
}

// 5. USR-B7: Email OTP verify does NOT set phone_verified=true; PATCH phone sets phone_verified=false.
func TestVerifyOTPAndUpdateProfilePhoneVerified(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	// Case A: register with empty phone, verify email OTP -> phone_verified MUST be false.
	regNoPhone, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "No Phone User",
		Email:    "nophone@example.com",
		Phone:    "",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Register nophone: %v", err)
	}
	otp1 := pub.userRegistered[len(pub.userRegistered)-1].OTP
	ver1, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "nophone@example.com",
		Otp:   otp1,
	})
	if err != nil {
		t.Fatalf("VerifyOTP nophone: %v", err)
	}
	if ver1.GetUser().GetPhoneVerified() {
		t.Fatalf("VerifyOTP with empty phone set phone_verified=true; want false")
	}

	// Case B: register with phone, verify email OTP -> phone_verified MUST still be false.
	regWithPhone, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "With Phone User",
		Email:    "withphone@example.com",
		Phone:    "0905551111",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Register withphone: %v", err)
	}
	otp2 := pub.userRegistered[len(pub.userRegistered)-1].OTP
	ver2, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "withphone@example.com",
		Otp:   otp2,
	})
	if err != nil {
		t.Fatalf("VerifyOTP withphone: %v", err)
	}
	if ver2.GetUser().GetPhoneVerified() {
		t.Fatalf("VerifyOTP email OTP set phone_verified=true; want false")
	}

	// Case C: user whose phone WAS verified changes full_name (stays true), then changes phone (becomes false).
	store.mu.Lock()
	u := store.users[regWithPhone.GetUserId()]
	u.PhoneVerified = true
	store.users[u.ID] = u
	store.mu.Unlock()

	userCtx := ctxWithUser(regWithPhone.GetUserId(), RoleBuyer, "")

	updName, err := srv.UpdateProfile(userCtx, &userv1.UpdateProfileRequest{
		FullName: "Renamed User",
	})
	if err != nil {
		t.Fatalf("UpdateProfile name only: %v", err)
	}
	if !updName.GetUser().GetPhoneVerified() {
		t.Fatalf("UpdateProfile changing only full_name unexpectedly cleared phone_verified")
	}

	updPhone, err := srv.UpdateProfile(userCtx, &userv1.UpdateProfileRequest{
		Phone: "0905552222",
	})
	if err != nil {
		t.Fatalf("UpdateProfile change phone: %v", err)
	}
	if updPhone.GetUser().GetPhone() != "0905552222" {
		t.Fatalf("expected updated phone 0905552222, got %q", updPhone.GetUser().GetPhone())
	}
	if updPhone.GetUser().GetPhoneVerified() {
		t.Fatalf("UpdateProfile changing phone kept phone_verified=true; want false")
	}

	_ = regNoPhone
}

// 6. USR-B5: WriteAuditLog auth + IP trust; ValidateUser auth per Phase 1A (user JWT only, no anonymous/service).
func TestWriteAuditLogAndValidateUserAuth(t *testing.T) {
	srv, _, sec, pub, _ := newTestHarness(t)
	buyerID := registerActiveUser(t, srv, pub, "auditbuyer@example.com", "0906661111")
	otherID := registerActiveUser(t, srv, pub, "auditother@example.com", "0906662222")
	adminID := registerActiveUser(t, srv, pub, "auditadmin@example.com", "0906663333")

	// --- WriteAuditLog ---

	// Anonymous -> Unauthenticated.
	if _, err := srv.WriteAuditLog(context.Background(), &userv1.WriteAuditLogRequest{
		ActorId:    "spoof",
		ActorRole:  "admin",
		Action:     "admin.product.review",
		TargetType: "product",
		TargetId:   "prod-1",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous WriteAuditLog: want Unauthenticated, got %v", err)
	}

	// Buyer -> PermissionDenied.
	if _, err := srv.WriteAuditLog(ctxWithUser(buyerID, RoleBuyer, ""), &userv1.WriteAuditLogRequest{
		Action:     "admin.product.review",
		TargetType: "product",
		TargetId:   "prod-1",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer WriteAuditLog: want PermissionDenied, got %v", err)
	}

	// Service token -> allowed, trusts req.IpAddress and req.ActorId/ActorRole.
	svcCtx := peer.NewContext(ctxWithService("gateway"), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("172.20.0.15"), Port: 41234},
	})
	svcAudit, err := srv.WriteAuditLog(svcCtx, &userv1.WriteAuditLogRequest{
		ActorId:    adminID,
		ActorRole:  RoleAdmin,
		Action:     "admin.product.review",
		TargetType: "product",
		TargetId:   "prod-svc",
		IpAddress:  "203.0.113.10",
	})
	if err != nil {
		t.Fatalf("service token WriteAuditLog failed: %v", err)
	}
	if svcAudit.GetAuditLog().GetIpAddress() != "203.0.113.10" || svcAudit.GetAuditLog().GetActorId() != adminID {
		t.Fatalf("service token WriteAuditLog unexpected record: %+v", svcAudit.GetAuditLog())
	}

	// Admin JWT from trusted internal gateway peer -> trusts req.IpAddress, pins actor to JWT claims.
	gwAdminCtx := peer.NewContext(ctxWithUser(adminID, RoleAdmin, ""), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("172.20.0.15"), Port: 41235},
	})
	gwAudit, err := srv.WriteAuditLog(gwAdminCtx, &userv1.WriteAuditLogRequest{
		ActorId:    "spoofed-actor",
		ActorRole:  "super_admin",
		Action:     "admin.voucher.create",
		TargetType: "voucher",
		TargetId:   "v-1",
		IpAddress:  "203.0.113.20",
	})
	if err != nil {
		t.Fatalf("gateway admin WriteAuditLog failed: %v", err)
	}
	if gwAudit.GetAuditLog().GetIpAddress() != "203.0.113.20" {
		t.Fatalf("gateway admin WriteAuditLog IP: want 203.0.113.20, got %q", gwAudit.GetAuditLog().GetIpAddress())
	}
	if gwAudit.GetAuditLog().GetActorId() != adminID || gwAudit.GetAuditLog().GetActorRole() != RoleAdmin {
		t.Fatalf("admin JWT must not spoof actor_id/actor_role: got id=%q role=%q",
			gwAudit.GetAuditLog().GetActorId(), gwAudit.GetAuditLog().GetActorRole())
	}

	// Admin JWT from untrusted external peer -> ignores spoofed req.IpAddress and records peer IP.
	extAdminCtx := peer.NewContext(ctxWithUser(adminID, RoleAdmin, ""), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.9"), Port: 55555},
	})
	extAudit, err := srv.WriteAuditLog(extAdminCtx, &userv1.WriteAuditLogRequest{
		Action:     "admin.voucher.create",
		TargetType: "voucher",
		TargetId:   "v-2",
		IpAddress:  "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("external admin WriteAuditLog failed: %v", err)
	}
	if extAudit.GetAuditLog().GetIpAddress() == "1.2.3.4" || extAudit.GetAuditLog().GetIpAddress() != "198.51.100.9" {
		t.Fatalf("untrusted external peer spoofed IP: got %q, want 198.51.100.9", extAudit.GetAuditLog().GetIpAddress())
	}

	// --- ValidateUser ---

	// Anonymous -> Unauthenticated.
	if _, err := srv.ValidateUser(context.Background(), &userv1.ValidateUserRequest{
		UserId: buyerID,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous ValidateUser: want Unauthenticated, got %v", err)
	}

	// Service token -> PermissionDenied (ValidateUser=false in Phase 1A).
	if _, err := srv.ValidateUser(ctxWithService("order-service"), &userv1.ValidateUserRequest{
		UserId: buyerID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ValidateUser: want PermissionDenied, got %v", err)
	}

	// Buyer validating self -> OK.
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")
	valSelf, err := srv.ValidateUser(buyerCtx, &userv1.ValidateUserRequest{UserId: buyerID})
	if err != nil || !valSelf.GetValid() {
		t.Fatalf("buyer ValidateUser self: resp=%+v err=%v", valSelf, err)
	}

	// Buyer validating another user -> PermissionDenied.
	if _, err := srv.ValidateUser(buyerCtx, &userv1.ValidateUserRequest{UserId: otherID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ValidateUser other: want PermissionDenied, got %v", err)
	}

	// Admin validating another user -> OK.
	valAdmin, err := srv.ValidateUser(ctxWithUser(adminID, RoleAdmin, ""), &userv1.ValidateUserRequest{UserId: buyerID})
	if err != nil || !valAdmin.GetValid() {
		t.Fatalf("admin ValidateUser buyer: resp=%+v err=%v", valAdmin, err)
	}

	// Revoked JTI -> Unauthenticated.
	claims, _ := auth.ClaimsFromContext(buyerCtx)
	_ = sec.RevokeJTI(context.Background(), claims.JTI(), 0)
	if _, err := srv.ValidateUser(buyerCtx, &userv1.ValidateUserRequest{UserId: buyerID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("revoked JTI ValidateUser: want Unauthenticated, got %v", err)
	}
}

type fakeAddressCatalog struct {
	provinces []*shippingv1.Province
	wards     []*shippingv1.Ward
	err       error
}

func (f *fakeAddressCatalog) ListProvinces(_ context.Context, in *shippingv1.ListProvincesRequest, _ ...grpc.CallOption) (*shippingv1.ListProvincesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []*shippingv1.Province
	for _, p := range f.provinces {
		if in.GetQuery() == "" || p.GetCode() == in.GetQuery() {
			out = append(out, p)
		}
	}
	return &shippingv1.ListProvincesResponse{Provinces: out}, nil
}

func (f *fakeAddressCatalog) ListWards(_ context.Context, in *shippingv1.ListWardsRequest, _ ...grpc.CallOption) (*shippingv1.ListWardsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []*shippingv1.Ward
	for _, w := range f.wards {
		if w.GetProvinceCode() == in.GetProvinceCode() && (in.GetQuery() == "" || w.GetCode() == in.GetQuery()) {
			out = append(out, w)
		}
	}
	return &shippingv1.ListWardsResponse{Wards: out}, nil
}

// 4b. USR-B6: Address province_code / ward_code validation via ShippingService catalog.
func TestAddressCatalogValidation(t *testing.T) {
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	log := logger.New("user-test", logger.WithWriter(io.Discard))
	catalog := &fakeAddressCatalog{
		provinces: []*shippingv1.Province{
			{Code: "01", Name: "Thành phố Hà Nội"},
			{Code: "79", Name: "Thành phố Hồ Chí Minh"},
		},
		wards: []*shippingv1.Ward{
			{Code: "01001", ProvinceCode: "01", Name: "Phường Hoàn Kiếm"},
			{Code: "79001", ProvinceCode: "79", Name: "Phường Sài Gòn"},
		},
	}
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{
		AddressCatalog: catalog,
	})

	userID := registerActiveUser(t, srv, pub, "catalogbuyer@example.com", "0907771122")
	userCtx := ctxWithUser(userID, RoleBuyer, "")

	// Invalid province_code -> InvalidArgument.
	if _, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van A",
		RecipientPhone: "0907771122",
		ProvinceCode:   "INVALID_999",
		WardCode:       "79001",
		StreetAddress:  "123 Le Loi",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid province_code: want InvalidArgument, got %v", err)
	}

	// Valid province_code, invalid ward_code -> InvalidArgument.
	if _, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van A",
		RecipientPhone: "0907771122",
		ProvinceCode:   "79",
		WardCode:       "FAKE_WARD_000",
		StreetAddress:  "123 Le Loi",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid ward_code: want InvalidArgument, got %v", err)
	}

	// Ward belongs to a DIFFERENT province -> InvalidArgument.
	if _, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van A",
		RecipientPhone: "0907771122",
		ProvinceCode:   "79",
		WardCode:       "01001",
		StreetAddress:  "123 Le Loi",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("cross-province ward_code: want InvalidArgument, got %v", err)
	}

	// Valid province + ward succeeds and populates names when omitted.
	created, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van A",
		RecipientPhone: "0907771122",
		ProvinceCode:   "79",
		WardCode:       "79001",
		StreetAddress:  "123 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("valid catalog CreateAddress failed: %v", err)
	}
	if created.GetAddress().GetProvinceName() != "Thành phố Hồ Chí Minh" || created.GetAddress().GetWardName() != "Phường Sài Gòn" {
		t.Fatalf("expected catalog names populated, got prov=%q ward=%q",
			created.GetAddress().GetProvinceName(), created.GetAddress().GetWardName())
	}

	// UpdateAddress with invalid ward_code -> InvalidArgument.
	if _, err := srv.UpdateAddress(userCtx, &userv1.UpdateAddressRequest{
		AddressId: created.GetAddress().GetId(),
		WardCode:  "FAKE_WARD_000",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateAddress invalid ward_code: want InvalidArgument, got %v", err)
	}

	// Shipping service outage -> Unavailable (fail-closed).
	catalog.err = status.Error(codes.Unavailable, "shipping down")
	if _, err := srv.CreateAddress(userCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van A",
		RecipientPhone: "0907771122",
		ProvinceCode:   "79",
		WardCode:       "79001",
		StreetAddress:  "123 Le Loi",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("shipping outage: want Unavailable, got %v", err)
	}
}

type raceResubmitStore struct {
	*memStore
	beforeResubmit func(id string) error
}

func (r *raceResubmitStore) ResubmitShop(ctx context.Context, arg db.ResubmitShopParams) (db.Shop, error) {
	if r.beforeResubmit != nil {
		if err := r.beforeResubmit(arg.ID); err != nil {
			return db.Shop{}, err
		}
	}
	return r.memStore.ResubmitShop(ctx, arg)
}

// Round 2 P2-5: ResubmitShop must guard shop status in WHERE and map no-rows to FailedPrecondition (409).
func TestResubmitShop_ConcurrentApprovalReturnsConflict(t *testing.T) {
	baseStore := newMemStore()
	rStore := &raceResubmitStore{memStore: baseStore}
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	log := logger.New("user-test", logger.WithWriter(io.Discard))
	srv := NewServer(rStore, sec, hasher, authMgr, pub, log, Options{})

	ownerID := registerActiveUser(t, srv, pub, "raceowner@example.com", "0908881122")
	adminID := registerActiveUser(t, srv, pub, "raceadmin@example.com", "0908881133")
	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	adminCtx := ctxWithUser(adminID, RoleAdmin, "")

	addrResp, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Kho Race",
		RecipientPhone: "0908881122",
		ProvinceCode:   "79",
		WardCode:       "79001",
		StreetAddress:  "100 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	shopResp, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Shop Race Guard",
		PickupAddressId:    addrResp.GetAddress().GetId(),
		ContactPhone:       "0908881122",
		BusinessLicenseDoc: "GPKD-RACE-1.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop: %v", err)
	}
	shopID := shopResp.GetShop().GetId()

	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopID,
		Decision: userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO,
		Reason:   "Bo sung GPKD",
	}); err != nil {
		t.Fatalf("AdminReviewShop REQUEST_INFO: %v", err)
	}

	// Case A: Direct pgx.ErrNoRows from ResubmitShop must map to FailedPrecondition (409), not Internal.
	rStore.beforeResubmit = func(_ string) error {
		return pgx.ErrNoRows
	}
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Shop Race Guard",
		PickupAddressId:    addrResp.GetAddress().GetId(),
		ContactPhone:       "0908881122",
		BusinessLicenseDoc: "GPKD-RACE-2.pdf",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ResubmitShop ErrNoRows: want FailedPrecondition, got %v", err)
	}

	// Case B: Concurrent admin APPROVE moves shop to ACTIVE before ResubmitShop executes;
	// ResubmitShop must reject with FailedPrecondition (409) and NOT overwrite ACTIVE back to PENDING.
	rStore.beforeResubmit = func(id string) error {
		baseStore.mu.Lock()
		sh := baseStore.shops[id]
		sh.Status = ShopStatusActive
		sh.RejectionReason = ""
		baseStore.shops[id] = sh
		baseStore.mu.Unlock()
		return nil
	}
	if _, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               "Shop Race Guard",
		PickupAddressId:    addrResp.GetAddress().GetId(),
		ContactPhone:       "0908881122",
		BusinessLicenseDoc: "GPKD-RACE-3.pdf",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("concurrent APPROVE during ResubmitShop: want FailedPrecondition, got %v", err)
	}

	got, err := baseStore.GetShopByID(context.Background(), shopID)
	if err != nil {
		t.Fatalf("GetShopByID: %v", err)
	}
	if got.Status != ShopStatusActive {
		t.Fatalf("shop status overwritten after concurrent APPROVE: want ACTIVE, got %s", got.Status)
	}
}

// Round 2 P2-13: peerIPFromContext strips :port via net.SplitHostPort; missing peer fails closed on x-client-ip.
func TestAuditIP_SplitHostPortAndFailClosedWithoutPeer(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	adminID := registerActiveUser(t, srv, pub, "ipadmin@example.com", "0908882211")

	// 1. peerIPFromContext strips port for IPv4 and IPv6.
	v4Ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("172.20.0.15"), Port: 58914},
	})
	if got := peerIPFromContext(v4Ctx); got != "172.20.0.15" {
		t.Fatalf("peerIPFromContext IPv4: want 172.20.0.15, got %q", got)
	}
	v6Ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 41234},
	})
	if got := peerIPFromContext(v6Ctx); got != "::1" {
		t.Fatalf("peerIPFromContext IPv6: want ::1, got %q", got)
	}
	if got := peerIPFromContext(context.Background()); got != "" {
		t.Fatalf("peerIPFromContext without peer: want empty, got %q", got)
	}

	// 2. Missing peer with end-user admin JWT MUST fail closed: ignore x-client-ip and req.IpAddress.
	noPeerCtx := metadata.NewIncomingContext(
		ctxWithUser(adminID, RoleAdmin, ""),
		metadata.Pairs(mdClientIP, "203.0.113.88"),
	)
	noPeerAudit, err := srv.WriteAuditLog(noPeerCtx, &userv1.WriteAuditLogRequest{
		Action:     "admin.test.nopeer",
		TargetType: "user",
		TargetId:   adminID,
		IpAddress:  "203.0.113.77",
	})
	if err != nil {
		t.Fatalf("WriteAuditLog no peer: %v", err)
	}
	if got := noPeerAudit.GetAuditLog().GetIpAddress(); got != "" {
		t.Fatalf("missing peer trusted spoofed IP: want empty, got %q", got)
	}

	// 3. Trusted internal peer with x-client-ip metadata uses x-client-ip; without x-client-ip strips :port.
	gwMetaCtx := peer.NewContext(
		metadata.NewIncomingContext(
			ctxWithUser(adminID, RoleAdmin, ""),
			metadata.Pairs(mdClientIP, "203.0.113.88"),
		),
		&peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("172.20.0.15"), Port: 58914}},
	)
	gwMetaAudit, err := srv.WriteAuditLog(gwMetaCtx, &userv1.WriteAuditLogRequest{
		Action:     "admin.test.gwmeta",
		TargetType: "user",
		TargetId:   adminID,
	})
	if err != nil {
		t.Fatalf("WriteAuditLog gw meta: %v", err)
	}
	if got := gwMetaAudit.GetAuditLog().GetIpAddress(); got != "203.0.113.88" {
		t.Fatalf("trusted gateway x-client-ip: want 203.0.113.88, got %q", got)
	}

	gwFallbackCtx := peer.NewContext(
		ctxWithUser(adminID, RoleAdmin, ""),
		&peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("172.20.0.15"), Port: 58914}},
	)
	gwFallbackAudit, err := srv.WriteAuditLog(gwFallbackCtx, &userv1.WriteAuditLogRequest{
		Action:     "admin.test.gwfallback",
		TargetType: "user",
		TargetId:   adminID,
	})
	if err != nil {
		t.Fatalf("WriteAuditLog gw fallback: %v", err)
	}
	if got := gwFallbackAudit.GetAuditLog().GetIpAddress(); got != "172.20.0.15" {
		t.Fatalf("gateway peer fallback should strip port: want 172.20.0.15, got %q", got)
	}

	// 4. Service token without peer still trusts explicit IP.
	svcNoPeerAudit, err := srv.WriteAuditLog(ctxWithService("gateway"), &userv1.WriteAuditLogRequest{
		ActorId:    adminID,
		ActorRole:  RoleAdmin,
		Action:     "admin.test.svcnopeer",
		TargetType: "user",
		TargetId:   adminID,
		IpAddress:  "203.0.113.99",
	})
	if err != nil {
		t.Fatalf("WriteAuditLog service no peer: %v", err)
	}
	if got := svcNoPeerAudit.GetAuditLog().GetIpAddress(); got != "203.0.113.99" {
		t.Fatalf("service token explicit IP: want 203.0.113.99, got %q", got)
	}
}
