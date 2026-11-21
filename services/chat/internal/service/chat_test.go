package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/hub"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/userclient"
	"github.com/nexusmart/core/services/chat/internal/validator"
)

func makeClaims(userID, role, shopID string) *auth.Claims {
	return &auth.Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
			ID:      "jti-" + userID,
		},
	}
}

type fakeShopChecker struct {
	err error
}

func (f fakeShopChecker) VerifyActiveShop(_ context.Context, _ string) error { return f.err }

func TestChatServicePermissionsAllMessageKindsAndWarnings(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemStore()
	h := hub.New(nil, "inst-test", nil, nil)
	imgVal := validator.NewImageValidator(validator.ImageValidatorConfig{Mode: validator.ModeMetadata})
	svc := NewChatService(st, h, nil, imgVal, nil, WithSiteRootDomain("nexusmart.localhost"))

	buyer := makeClaims("buyer-1", "buyer", "")
	otherBuyer := makeClaims("buyer-2", "buyer", "")
	seller := makeClaims("seller-1", "seller", "shop-1")
	staff := makeClaims("staff-1", "seller", "shop-1")
	otherSeller := makeClaims("seller-2", "seller", "shop-2")
	admin := makeClaims("admin-1", "admin", "")

	conv, created, _, err := svc.CreateOrGetConversation(ctx, buyer, "shop-1", "shop1.nexusmart.localhost", "")
	if err != nil || !created {
		t.Fatalf("create conversation failed: created=%v err=%v", created, err)
	}

	// Strictly enforce object-level access: only buyer-1 and shop-1 members allowed.
	for name, c := range map[string]*auth.Claims{
		"otherBuyer":  otherBuyer,
		"otherSeller": otherSeller,
		"admin":       admin,
	} {
		if _, err := svc.GetConversation(ctx, c, conv.ID); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s GetConversation: expected ErrForbidden, got %v", name, err)
		}
		if _, err := svc.SendMessage(ctx, c, SendMessageInput{ConversationID: conv.ID, Kind: store.KindText, Body: "hi"}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s SendMessage: expected ErrForbidden, got %v", name, err)
		}
		if _, err := svc.MarkRead(ctx, c, conv.ID, 1, ""); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s MarkRead: expected ErrForbidden, got %v", name, err)
		}
	}

	// Buyer sends text with VN phone -> non-blocking external_transaction_warning attached!
	outText, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Goi cho minh 0901234567 nhe",
	})
	if err != nil {
		t.Fatalf("send text failed: %v", err)
	}
	if outText.Message.Metadata["warning"] != "external_transaction_warning" {
		t.Fatalf("expected external_transaction_warning in metadata, got %+v", outText.Message.Metadata)
	}

	// Valid image passes; SSRF / svg / oversized rejected.
	outImg, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindImage,
		Body:           "https://cdn.example.com/photo.jpg",
		Metadata:       map[string]any{"content_type": "image/jpeg", "size_bytes": 4096},
	})
	if err != nil || outImg.Message.Metadata["content_type"] != "image/jpeg" {
		t.Fatalf("valid image failed: out=%+v err=%v", outImg, err)
	}
	if _, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindImage,
		Body:           "http://169.254.169.254/latest/meta-data/img.png",
		Metadata:       map[string]any{"content_type": "image/png", "size_bytes": 100},
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected SSRF image rejection, got %v", err)
	}

	// Product card & order reference kinds.
	outProd, err := svc.SendMessage(ctx, staff, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindProduct,
		RefID:          "prod-99",
		Body:           "Ao khoac kaki",
	})
	if err != nil || outProd.Message.SenderRole != store.RoleSeller || outProd.Message.RefID != "prod-99" {
		t.Fatalf("product message failed: out=%+v err=%v", outProd, err)
	}
	if _, err := svc.SendMessage(ctx, seller, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindOrder,
		RefID:          "",
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected order message without ref_id to fail, got %v", err)
	}
	outOrd, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindOrder,
		RefID:          "ORD-2026-001",
		Body:           "Kiem tra don giup minh",
	})
	if err != nil || outOrd.Message.RefID != "ORD-2026-001" {
		t.Fatalf("order message failed: out=%+v err=%v", outOrd, err)
	}

	// ShopChecker rejects inactive shop.
	svcInactive := NewChatService(st, h, nil, imgVal, nil, WithShopChecker(fakeShopChecker{err: userclient.ErrShopInactive}))
	if _, _, _, err := svcInactive.CreateOrGetConversation(ctx, buyer, "shop-suspended", "", ""); !errors.Is(err, ErrShopInactive) {
		t.Fatalf("expected ErrShopInactive, got %v", err)
	}
}

func TestChatServiceAutoReplyEngine(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemStore()
	h := hub.New(nil, "inst-auto", nil, nil)

	// Start at 23:30 ICT (16:30 UTC) -> outside 08:00..22:00 ICT work hours!
	now := time.Date(2026, 11, 21, 16, 30, 0, 0, time.UTC)
	svc := NewChatService(st, h, nil, nil, nil, WithNowFunc(func() time.Time { return now }))

	buyer := makeClaims("buyer-1", "buyer", "")
	seller := makeClaims("seller-1", "seller", "shop-1")
	otherSeller := makeClaims("seller-2", "seller", "shop-2")

	// Unauthorized shop config update rejected.
	if _, err := svc.UpsertAutoReplyConfig(ctx, buyer, store.AutoReplyConfig{ShopID: "shop-1", Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("buyer updating auto-reply must be forbidden, got %v", err)
	}
	if _, err := svc.UpsertAutoReplyConfig(ctx, otherSeller, store.AutoReplyConfig{ShopID: "shop-1", Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other seller updating auto-reply must be forbidden, got %v", err)
	}

	_, err := svc.UpsertAutoReplyConfig(ctx, seller, store.AutoReplyConfig{
		ShopID:          "shop-1",
		Enabled:         true,
		GreetingMessage: "Chao ban, shop se tra loi ngay!",
		OfflineEnabled:  true,
		OfflineMessage:  "Shop dang ngoai gio lam viec (08:00-22:00).",
		WorkStartHour:   8,
		WorkEndHour:     22,
		Timezone:        "Asia/Ho_Chi_Minh",
		CooldownMinutes: 30,
		KeywordRules: []store.KeywordRule{
			{Keywords: []string{"ship", "giao hàng"}, Reply: "Shop giao hang toan quoc 2-3 ngay a!"},
		},
	})
	if err != nil {
		t.Fatalf("upsert auto reply config: %v", err)
	}

	conv, _, _, err := svc.CreateOrGetConversation(ctx, buyer, "shop-1", "", "")
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}

	// 1. Keyword rule takes priority even during offline hours.
	out1, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Shop co Giao Hàng nhanh khong?",
	})
	if err != nil || out1.AutoReply == nil || out1.AutoReply.Body != "Shop giao hang toan quoc 2-3 ngay a!" || !out1.AutoReply.IsAutoReply || out1.AutoReply.SenderID != "shop:shop-1" {
		t.Fatalf("expected keyword auto-reply, got out=%+v err=%v", out1, err)
	}

	// 2. Within 30m cooldown -> NO auto-reply fired!
	now = now.Add(10 * time.Minute)
	out2, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Alo shop oi",
	})
	if err != nil || out2.AutoReply != nil {
		t.Fatalf("expected no auto-reply during cooldown, got auto=%+v err=%v", out2.AutoReply, err)
	}

	// 3. After cooldown (still 23:59+ -> 00:15 ICT offline) -> offline message fires!
	now = now.Add(35 * time.Minute)
	out3, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Cho minh hoi gia",
	})
	if err != nil || out3.AutoReply == nil || out3.AutoReply.Body != "Shop dang ngoai gio lam viec (08:00-22:00)." {
		t.Fatalf("expected offline auto-reply, got auto=%+v err=%v", out3.AutoReply, err)
	}

	// 4. Seller message NEVER triggers auto-reply.
	now = now.Add(10 * time.Hour) // 10:15 ICT (inside work hours)
	outSeller, err := svc.SendMessage(ctx, seller, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Chao khach a",
	})
	if err != nil || outSeller.AutoReply != nil {
		t.Fatalf("seller message must never trigger auto-reply, got auto=%+v err=%v", outSeller.AutoReply, err)
	}

	// 5. Buyer message inside work hours with no keyword -> greeting fires!
	out4, err := svc.SendMessage(ctx, buyer, SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Cam on shop",
	})
	if err != nil || out4.AutoReply == nil || out4.AutoReply.Body != "Chao ban, shop se tra loi ngay!" {
		t.Fatalf("expected greeting auto-reply, got auto=%+v err=%v", out4.AutoReply, err)
	}
}
