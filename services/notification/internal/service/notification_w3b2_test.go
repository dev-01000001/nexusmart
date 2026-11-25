package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestZaloPayNotificationContentAndEmails(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-zp":  "buyer-zp@nexusmart.local",
			"seller-zp": "seller-zp@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-zp": "seller-zp",
		},
		shopNames: map[string]string{
			"shop-zp": "Cửa Hàng Bình Minh",
		},
		txChecker: &repo.inTx,
	}
	siteResolver := &stubSiteResolver{
		hosts: map[string]string{"shop-zp": "binh-minh.nexusmart.vn"},
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSiteLinks(siteResolver, SiteLinkConfig{}))

	// Verify helper retains VietQR & COD labels while adding ZaloPay (case-insensitive).
	labelCases := []struct {
		in   string
		want string
	}{
		{"ZALOPAY", "ví ZaloPay"},
		{"zalopay", "ví ZaloPay"},
		{" ZaloPay ", "ví ZaloPay"},
		{"VIETQR", "chuyển khoản VietQR"},
		{"COD", "thanh toán khi nhận hàng"},
		{"UNKNOWN", "thanh toán trực tuyến"},
	}
	for _, tc := range labelCases {
		if got := paymentMethodLabel(tc.in); got != tc.want {
			t.Errorf("paymentMethodLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// 1. order.created with payment_method = "ZALOPAY" -> in-app + email show "ví ZaloPay" and formatted VND.
	orderPayload, _ := json.Marshal(map[string]any{
		"checkout_group_id":  "grp-zp-1",
		"order_id":           "ord-zp-1",
		"order_code":         "NM-20261124-ZP01",
		"buyer_id":           "buyer-zp",
		"shop_id":            "shop-zp",
		"storefront_shop_id": "shop-zp",
		"payment_method":     "ZALOPAY",
		"total_payable_vnd":  450000,
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-zp-order-created-1",
		EventType: "order.created",
		Payload:   orderPayload,
	}); err != nil || dup {
		t.Fatalf("ZALOPAY order.created failed: dup=%v err=%v", dup, err)
	}

	buyerNotifs, _, total, err := svc.ListNotifications(context.Background(), "buyer-zp", store.CategoryOrder, false, 1, 20)
	if err != nil || total != 1 || len(buyerNotifs) != 1 {
		t.Fatalf("expected 1 buyer order.created notification, got len=%d total=%d err=%v", len(buyerNotifs), total, err)
	}
	if !strings.Contains(buyerNotifs[0].Body, "ví ZaloPay") || !strings.Contains(buyerNotifs[0].Body, "450.000₫") {
		t.Errorf("ZALOPAY order.created in-app body must contain 'ví ZaloPay' and '450.000₫', got %q", buyerNotifs[0].Body)
	}
	if strings.Contains(buyerNotifs[0].Title+"\n"+buyerNotifs[0].Body, "ZALOPAY") {
		t.Errorf("ZALOPAY order.created in-app leaked raw enum ZALOPAY: %+v", buyerNotifs[0])
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "buyer-zp@nexusmart.local" {
		t.Fatalf("expected 1 buyer order.created email, got %+v", mail.sent)
	}
	if !strings.Contains(mail.sent[0].Body, "ví ZaloPay") || !strings.Contains(mail.sent[0].Body, "450.000₫") || strings.Contains(mail.sent[0].Subject+"\n"+mail.sent[0].Body, "ZALOPAY") {
		t.Errorf("ZALOPAY order.created email must contain 'ví ZaloPay' without raw enum, got %+v", mail.sent[0])
	}

	// 2. payment.succeeded with method = "ZALOPAY" -> in-app + email show "ví ZaloPay", formatted VND, tx id, and order link.
	payPayload, _ := json.Marshal(map[string]any{
		"payment_id":     "pay-zp-1",
		"order_group_id": "grp-zp-1",
		"order_id":       "grp-zp-1",
		"user_id":        "buyer-zp",
		"amount_vnd":     450000,
		"method":         "ZALOPAY",
		"provider_tx_id": "261124000000123",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "payment.succeeded", Envelope{
		EventID:   "evt-zp-pay-succeeded-1",
		EventType: "payment.succeeded",
		Payload:   payPayload,
	}); err != nil || dup {
		t.Fatalf("ZALOPAY payment.succeeded failed: dup=%v err=%v", dup, err)
	}

	buyerNotifs, _, total, err = svc.ListNotifications(context.Background(), "buyer-zp", store.CategoryOrder, false, 1, 20)
	if err != nil || total != 2 || len(buyerNotifs) != 2 {
		t.Fatalf("expected 2 buyer notifications after payment.succeeded, got len=%d total=%d err=%v", len(buyerNotifs), total, err)
	}
	payNotif := buyerNotifs[1]
	if payNotif.Link != "https://binh-minh.nexusmart.vn/don-hang/ord-zp-1" {
		t.Errorf("ZALOPAY payment.succeeded link = %q, want https://binh-minh.nexusmart.vn/don-hang/ord-zp-1", payNotif.Link)
	}
	if payNotif.Title != "Thanh toán thành công" || !strings.Contains(payNotif.Body, "ví ZaloPay") || !strings.Contains(payNotif.Body, "450.000₫") || !strings.Contains(payNotif.Body, "261124000000123") {
		t.Errorf("ZALOPAY payment.succeeded in-app mismatch: title=%q body=%q", payNotif.Title, payNotif.Body)
	}
	if strings.Contains(payNotif.Title+"\n"+payNotif.Body, "ZALOPAY") {
		t.Errorf("ZALOPAY payment.succeeded in-app leaked raw enum ZALOPAY: %+v", payNotif)
	}
	if len(mail.sent) != 2 || mail.sent[1].To != "buyer-zp@nexusmart.local" {
		t.Fatalf("expected 2 emails after payment.succeeded, got %+v", mail.sent)
	}
	if !strings.Contains(mail.sent[1].Body, "ví ZaloPay") || !strings.Contains(mail.sent[1].Subject, "450.000₫") || strings.Contains(mail.sent[1].Subject+"\n"+mail.sent[1].Body, "ZALOPAY") {
		t.Errorf("ZALOPAY payment.succeeded email mismatch: %+v", mail.sent[1])
	}

	// 3. order.confirmed with payment_method = "ZALOPAY" -> both buyer & seller notifications/emails show "ví ZaloPay".
	beforeMails := len(mail.sent)
	confirmPayload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-zp-1",
		"order_code":        "NM-20261124-ZP01",
		"buyer_id":          "buyer-zp",
		"shop_id":           "shop-zp",
		"payment_method":    "ZALOPAY",
		"total_payable_vnd": 450000,
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.confirmed", Envelope{
		EventID:   "evt-zp-order-confirmed-1",
		EventType: "order.confirmed",
		Payload:   confirmPayload,
	}); err != nil || dup {
		t.Fatalf("ZALOPAY order.confirmed failed: dup=%v err=%v", dup, err)
	}

	buyerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "buyer-zp", store.CategoryOrder, false, 1, 20)
	sellerNotifs, _, _, _ := svc.ListNotifications(context.Background(), "seller-zp", store.CategoryOrder, false, 1, 20)
	if len(buyerNotifs) != 3 || len(sellerNotifs) != 1 {
		t.Fatalf("expected 3 buyer and 1 seller notifications, got buyer=%d seller=%d", len(buyerNotifs), len(sellerNotifs))
	}
	if !strings.Contains(buyerNotifs[2].Body, "ví ZaloPay") || strings.Contains(buyerNotifs[2].Body, "ZALOPAY") {
		t.Errorf("ZALOPAY order.confirmed buyer body mismatch: %q", buyerNotifs[2].Body)
	}
	if !strings.Contains(sellerNotifs[0].Body, "ví ZaloPay") || strings.Contains(sellerNotifs[0].Body, "ZALOPAY") {
		t.Errorf("ZALOPAY order.confirmed seller body mismatch: %q", sellerNotifs[0].Body)
	}
	if len(mail.sent)-beforeMails != 2 {
		t.Fatalf("expected 2 emails (buyer+seller) on order.confirmed, got %d", len(mail.sent)-beforeMails)
	}
	for _, m := range mail.sent[beforeMails:] {
		if !strings.Contains(m.Body, "ví ZaloPay") || strings.Contains(m.Subject+"\n"+m.Body, "ZALOPAY") {
			t.Errorf("ZALOPAY order.confirmed email mismatch: %+v", m)
		}
	}
	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestShopStatusChangedNotificationsIdempotencyAndValidation(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"owner-1": "owner1@nexusmart.local",
			"owner-2": "owner2@nexusmart.local",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. ACTIVE -> SUSPENDED: transient UserService lookup error must NOT record processed_events.
	suspendPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-gov-1",
		"owner_user_id": "owner-1",
		"shop_name":     "Cửa Hàng Ánh Dương",
		"from_status":   "ACTIVE",
		"to_status":     "SUSPENDED",
		"reason":        "Vi phạm chính sách hàng giả",
		"actor_id":      "ops-1",
		"approver_id":   "admin-2",
		"version":       2,
		"occurred_at":   "2026-11-24T10:00:00Z",
	})
	suspendEnv := Envelope{
		EventID:   "evt-shop-suspend-1",
		EventType: "user.shop_status_changed",
		Payload:   suspendPayload,
	}

	resolver.userErr = status.Error(codes.Unavailable, "user service temporarily unavailable")
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", suspendEnv); err == nil {
		t.Fatalf("expected error on transient UserService failure")
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-shop-suspend-1"); processed {
		t.Fatalf("failed user.shop_status_changed must NOT mark processed_events")
	}

	// Recover UserService -> delivers 1 SYSTEM in-app + 1 email with shop_name, reason, and ongoing-orders notice.
	resolver.userErr = nil
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", suspendEnv); err != nil || dup {
		t.Fatalf("ACTIVE -> SUSPENDED failed: dup=%v err=%v", dup, err)
	}

	notifs, unread, total, err := svc.ListNotifications(context.Background(), "owner-1", store.CategorySystem, false, 1, 20)
	if err != nil || total != 1 || unread != 1 || len(notifs) != 1 {
		t.Fatalf("expected 1 unread SYSTEM notification for owner-1, got len=%d unread=%d total=%d err=%v", len(notifs), unread, total, err)
	}
	sn := notifs[0]
	if sn.Category != store.CategorySystem || sn.Link != "/seller" {
		t.Errorf("unexpected category/link for suspended shop: %+v", sn)
	}
	if !strings.Contains(sn.Title+" "+sn.Body, "Cửa Hàng Ánh Dương") ||
		!strings.Contains(sn.Body, "Vi phạm chính sách hàng giả") ||
		!strings.Contains(strings.ToLower(sn.Body), "đơn hàng") {
		t.Errorf("SUSPENDED in-app missing shop_name, reason, or ongoing-orders note: title=%q body=%q", sn.Title, sn.Body)
	}
	for _, rawEnum := range []string{"SUSPENDED", "ACTIVE", "SHOP_STATUS_"} {
		if strings.Contains(sn.Title+"\n"+sn.Body, rawEnum) {
			t.Errorf("SUSPENDED in-app leaked raw enum %q: %+v", rawEnum, sn)
		}
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "owner1@nexusmart.local" {
		t.Fatalf("expected 1 suspension email to owner-1, got %+v", mail.sent)
	}
	suspMail := mail.sent[0]
	if !strings.Contains(suspMail.Subject, "Cửa Hàng Ánh Dương") ||
		!strings.Contains(suspMail.Body, "Cửa Hàng Ánh Dương") ||
		!strings.Contains(suspMail.Body, "Vi phạm chính sách hàng giả") ||
		!strings.Contains(strings.ToLower(suspMail.Body), "đơn hàng") {
		t.Errorf("SUSPENDED email missing shop_name, reason, or ongoing-orders note: %+v", suspMail)
	}
	for _, rawEnum := range []string{"SUSPENDED", "ACTIVE", "SHOP_STATUS_"} {
		if strings.Contains(suspMail.Subject+"\n"+suspMail.Body, rawEnum) {
			t.Errorf("SUSPENDED email leaked raw enum %q: %+v", rawEnum, suspMail)
		}
	}
	if so, ok, _ := repo.GetShopOwner(context.Background(), "shop-gov-1"); !ok || so.OwnerUserID != "owner-1" || so.ShopName != "Cửa Hàng Ánh Dương" {
		t.Errorf("expected shop_owners cached from user.shop_status_changed, got ok=%v so=%+v", ok, so)
	}

	// 2. Replay same event_id -> idempotent, zero duplicate notifications or emails.
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", suspendEnv); err != nil || !dup {
		t.Fatalf("expected duplicate=true on replay, got dup=%v err=%v", dup, err)
	}
	if _, _, totalAfterReplay, _ := svc.ListNotifications(context.Background(), "owner-1", store.CategorySystem, false, 1, 20); totalAfterReplay != 1 {
		t.Fatalf("replay must not create duplicate notification, got total=%d", totalAfterReplay)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("replay must not resend suspension email, got %d emails", len(mail.sent))
	}

	// 3. SUSPENDED -> ACTIVE (unsuspend) -> delivers 1 SYSTEM in-app + 1 unlock email.
	unsuspendPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-gov-1",
		"owner_user_id": "owner-1",
		"shop_name":     "Cửa Hàng Ánh Dương",
		"from_status":   "SUSPENDED",
		"to_status":     "ACTIVE",
		"reason":        "Đã khắc phục vi phạm theo yêu cầu",
		"actor_id":      "ops-2",
		"approver_id":   "",
		"version":       3,
		"occurred_at":   "2026-11-24T12:00:00Z",
	})
	unsuspendEnv := Envelope{
		EventID:   "evt-shop-unsuspend-1",
		EventType: "user.shop_status_changed",
		Payload:   unsuspendPayload,
	}
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", unsuspendEnv); err != nil || dup {
		t.Fatalf("SUSPENDED -> ACTIVE failed: dup=%v err=%v", dup, err)
	}

	notifs, unread, total, err = svc.ListNotifications(context.Background(), "owner-1", store.CategorySystem, false, 1, 20)
	if err != nil || total != 2 || unread != 2 || len(notifs) != 2 {
		t.Fatalf("expected 2 SYSTEM notifications after unsuspend, got len=%d unread=%d total=%d err=%v", len(notifs), unread, total, err)
	}
	un := notifs[1]
	if un.Category != store.CategorySystem || un.Link != "/seller" {
		t.Errorf("unexpected category/link for unsuspended shop: %+v", un)
	}
	if !strings.Contains(un.Title+" "+un.Body, "Cửa Hàng Ánh Dương") ||
		!strings.Contains(strings.ToLower(un.Title+" "+un.Body), "mở khóa") ||
		!strings.Contains(un.Body, "Đã khắc phục vi phạm theo yêu cầu") {
		t.Errorf("ACTIVE unlock in-app missing expected copy: title=%q body=%q", un.Title, un.Body)
	}
	for _, rawEnum := range []string{"SUSPENDED", "ACTIVE", "SHOP_STATUS_"} {
		if strings.Contains(un.Title+"\n"+un.Body, rawEnum) {
			t.Errorf("ACTIVE unlock in-app leaked raw enum %q: %+v", rawEnum, un)
		}
	}
	if len(mail.sent) != 2 || mail.sent[1].To != "owner1@nexusmart.local" {
		t.Fatalf("expected 2 emails after unsuspend, got %+v", mail.sent)
	}
	unlockMail := mail.sent[1]
	if !strings.Contains(unlockMail.Subject, "Cửa Hàng Ánh Dương") ||
		!strings.Contains(strings.ToLower(unlockMail.Subject+" "+unlockMail.Body), "mở khóa") {
		t.Errorf("ACTIVE unlock email missing expected copy: %+v", unlockMail)
	}
	for _, rawEnum := range []string{"SUSPENDED", "ACTIVE", "SHOP_STATUS_"} {
		if strings.Contains(unlockMail.Subject+"\n"+unlockMail.Body, rawEnum) {
			t.Errorf("ACTIVE unlock email leaked raw enum %q: %+v", rawEnum, unlockMail)
		}
	}

	// Replay unsuspend event_id -> idempotent.
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", unsuspendEnv); err != nil || !dup {
		t.Fatalf("expected duplicate=true on unsuspend replay, got dup=%v err=%v", dup, err)
	}
	if _, _, totalAfterUnlockReplay, _ := svc.ListNotifications(context.Background(), "owner-1", store.CategorySystem, false, 1, 20); totalAfterUnlockReplay != 2 {
		t.Fatalf("unsuspend replay must not duplicate notification, got %d", totalAfterUnlockReplay)
	}
	if len(mail.sent) != 2 {
		t.Fatalf("unsuspend replay must not resend email, got %d", len(mail.sent))
	}

	// 4. Non-SUSPENDED -> ACTIVE (e.g. PENDING -> ACTIVE) is safely acked without misleading unlock notice.
	beforeNotifs := len(repo.notifs)
	beforeMails := len(mail.sent)
	nonUnlockPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-gov-1",
		"owner_user_id": "owner-1",
		"shop_name":     "Cửa Hàng Ánh Dương",
		"from_status":   "PENDING",
		"to_status":     "ACTIVE",
		"version":       1,
	})
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", Envelope{
		EventID:   "evt-shop-pending-active",
		EventType: "user.shop_status_changed",
		Payload:   nonUnlockPayload,
	}); err != nil || dup {
		t.Fatalf("PENDING -> ACTIVE should ack safely, got dup=%v err=%v", dup, err)
	}
	if len(repo.notifs) != beforeNotifs || len(mail.sent) != beforeMails {
		t.Fatalf("PENDING -> ACTIVE must not emit misleading unlock notification/email")
	}

	// 5. UC-B23 preferences: owner-2 disables system_email (and attempts system_in_app=false) -> still gets SYSTEM in-app, 0 emails.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:      "owner-2",
		SystemInApp: false,
		SystemEmail: false,
	})
	beforeMails = len(mail.sent)
	prefPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-gov-2",
		"owner_user_id": "owner-2",
		"shop_name":     "",
		"from_status":   "ACTIVE",
		"to_status":     "SUSPENDED",
		"reason":        "Bán sản phẩm cấm",
		"version":       2,
	})
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", Envelope{
		EventID:   "evt-shop-suspend-owner2",
		EventType: "user.shop_status_changed",
		Payload:   prefPayload,
	}); err != nil || dup {
		t.Fatalf("owner-2 suspension event failed: dup=%v err=%v", dup, err)
	}
	owner2Notifs, _, owner2Total, _ := svc.ListNotifications(context.Background(), "owner-2", store.CategorySystem, false, 1, 20)
	if owner2Total != 1 || len(owner2Notifs) != 1 || !strings.Contains(owner2Notifs[0].Body, "shop-gov-2") {
		t.Fatalf("UC-B23: expected 1 SYSTEM in-app fallback to shop_id for owner-2, got total=%d notifs=%+v", owner2Total, owner2Notifs)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("expected email suppressed when system_email=false, got %+v", mail.sent[beforeMails:])
	}

	// 6. Malformed payloads (missing shop_id/owner_user_id, missing/invalid to_status, version <= 0, bad JSON) -> ErrMalformedPayload (DLQ, not Acked away).
	badCases := []struct {
		name string
		raw  []byte
	}{
		{
			name: "MissingOwnerUserID",
			raw:  []byte(`{"shop_id":"shop-gov-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"SUSPENDED","reason":"x","version":2}`),
		},
		{
			name: "MissingShopID",
			raw:  []byte(`{"owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"SUSPENDED","reason":"x","version":2}`),
		},
		{
			name: "EmptyToStatus",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"","version":2}`),
		},
		{
			name: "InvalidToStatus",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"BANNED","version":2}`),
		},
		{
			name: "PendingToStatus",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"PENDING","version":2}`),
		},
		{
			name: "MissingVersion",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"SUSPENDED","reason":"x"}`),
		},
		{
			name: "ZeroVersion",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"SUSPENDED","reason":"x","version":0}`),
		},
		{
			name: "NegativeVersion",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"SUSPENDED","reason":"x","version":-1}`),
		},
		{
			name: "LegacyStatusVersionOnly",
			raw:  []byte(`{"shop_id":"shop-gov-1","owner_user_id":"owner-1","shop_name":"Shop A","from_status":"ACTIVE","to_status":"SUSPENDED","reason":"x","status_version":2}`),
		},
		{
			name: "InvalidJSON",
			raw:  []byte(`{not-json`),
		},
	}
	notifsBeforeBad := len(repo.notifs)
	mailsBeforeBad := len(mail.sent)
	for _, tc := range badCases {
		evtID := "evt-shop-status-bad-" + tc.name
		if _, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", Envelope{
			EventID:   evtID,
			EventType: "user.shop_status_changed",
			Payload:   tc.raw,
		}); !errors.Is(err, ErrMalformedPayload) {
			t.Fatalf("%s: expected ErrMalformedPayload, got %v", tc.name, err)
		}
		if processed, _ := repo.IsEventProcessed(context.Background(), evtID); processed {
			t.Fatalf("%s: malformed event must NOT be recorded in processed_events", tc.name)
		}
	}
	if len(repo.notifs) != notifsBeforeBad || len(mail.sent) != mailsBeforeBad {
		t.Fatalf("malformed payloads must not create notifications or emails: notifs %d->%d, mails %d->%d", notifsBeforeBad, len(repo.notifs), mailsBeforeBad, len(mail.sent))
	}

	// 7. SMTP transient failure retry -> sends email once without duplicating in-app notification.
	retryPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-gov-1",
		"owner_user_id": "owner-1",
		"shop_name":     "Cửa Hàng Ánh Dương",
		"from_status":   "ACTIVE",
		"to_status":     "SUSPENDED",
		"reason":        "Tạm ngưng xác minh",
		"version":       4,
	})
	retryEnv := Envelope{
		EventID:   "evt-shop-suspend-smtp-retry",
		EventType: "user.shop_status_changed",
		Payload:   retryPayload,
	}
	beforeOwner1Total := 2
	beforeMails = len(mail.sent)
	mail.err = errors.New("smtp connection reset")
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", retryEnv); err == nil {
		t.Fatalf("expected error on SMTP failure")
	}
	mail.err = nil
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_status_changed", retryEnv); err != nil || dup {
		t.Fatalf("SMTP retry failed: dup=%v err=%v", dup, err)
	}
	if _, _, afterOwner1Total, _ := svc.ListNotifications(context.Background(), "owner-1", store.CategorySystem, false, 1, 20); afterOwner1Total != int64(beforeOwner1Total+1) {
		t.Fatalf("SMTP retry must not duplicate in-app notification: got %d, want %d", afterOwner1Total, beforeOwner1Total+1)
	}
	if len(mail.sent) != beforeMails+1 {
		t.Fatalf("SMTP retry must send exactly 1 email, got %d", len(mail.sent)-beforeMails)
	}

	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestStaffOnboardingPasswordResetNeverLogsOrPersistsToken(t *testing.T) {
	var logBuf bytes.Buffer
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewJSONHandler(&logBuf, nil)))
	svc.SetWebBaseURL("https://admin.nexusmart.vn")

	secretToken := "staff-onboard-reset-secret-xyz987654"
	payload, _ := json.Marshal(map[string]any{
		"user_id":     "staff-mod-1",
		"email":       "moderator1@nexusmart.local",
		"full_name":   "Nguyễn Kiểm Duyệt",
		"reset_token": secretToken,
		"expires_at":  "2026-11-24T15:00:00Z",
	})
	env := Envelope{
		EventID:   "evt-staff-reset-1",
		EventType: "user.password_reset_requested",
		Payload:   payload,
	}

	mail.err = errors.New("smtp temporary error")
	if _, err := svc.ProcessEvent(context.Background(), "user.password_reset_requested", env); err == nil {
		t.Fatalf("expected SMTP error")
	} else if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("SECURITY: staff reset_token leaked into error: %v", err)
	}
	if strings.Contains(logBuf.String(), secretToken) {
		t.Fatalf("SECURITY: staff reset_token leaked into logs on error: %s", logBuf.String())
	}

	mail.err = nil
	if dup, err := svc.ProcessEvent(context.Background(), "user.password_reset_requested", env); err != nil || dup {
		t.Fatalf("retry failed: dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "moderator1@nexusmart.local" {
		t.Fatalf("expected 1 staff password reset email, got %+v", mail.sent)
	}
	if strings.Contains(mail.sent[0].Subject, secretToken) {
		t.Fatalf("SECURITY: staff reset_token leaked into email Subject: %q", mail.sent[0].Subject)
	}
	if !strings.Contains(mail.sent[0].Body, "https://admin.nexusmart.vn/dat-lai-mat-khau?token="+secretToken) {
		t.Fatalf("expected reset link in email body, got %q", mail.sent[0].Body)
	}
	for _, n := range repo.notifs {
		if strings.Contains(n.Title+n.Body+n.Link, secretToken) {
			t.Fatalf("SECURITY: staff reset_token persisted in notifications table: %+v", n)
		}
	}
	if strings.Contains(logBuf.String(), secretToken) {
		t.Fatalf("SECURITY: staff reset_token leaked into logs: %s", logBuf.String())
	}
}

func TestReturnUpdatedRefundApprovalAndRejectionNotifications(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-ret-app":  "buyer-ret-app@nexusmart.local",
			"seller-ret-app": "seller-ret-app@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-ret-app": "seller-ret-app",
		},
		shopNames: map[string]string{
			"shop-ret-app": "Cửa Hàng Minh Tâm",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rawEnums := []string{
		"RETURN_STATUS_", "PENDING_APPROVAL", "APPROVED", "REJECTED",
		"REFUNDING", "ESCALATED", "refund_rejected",
	}
	assertNoRawEnums := func(t *testing.T, label string, notifs []store.Notification, mails []mailer.Message) {
		t.Helper()
		var all string
		for _, n := range notifs {
			all += n.Title + "\n" + n.Body + "\n"
		}
		for _, m := range mails {
			all += m.Subject + "\n" + m.Body + "\n"
		}
		for _, bad := range rawEnums {
			if strings.Contains(all, bad) {
				t.Errorf("%s: leaked raw enum %q in:\n%s", label, bad, all)
			}
		}
	}

	splitByRole := func(t *testing.T, notifs []store.Notification, mails []mailer.Message) (store.Notification, store.Notification, mailer.Message, mailer.Message) {
		t.Helper()
		if len(notifs) != 2 || len(mails) != 2 {
			t.Fatalf("expected 2 notifications and 2 emails (buyer+seller), got notifs=%d mails=%d", len(notifs), len(mails))
		}
		var bN, sN store.Notification
		var bM, sM mailer.Message
		for _, n := range notifs {
			switch n.UserID {
			case "buyer-ret-app":
				bN = n
			case "seller-ret-app":
				sN = n
			}
		}
		for _, m := range mails {
			switch m.To {
			case "buyer-ret-app@nexusmart.local":
				bM = m
			case "seller-ret-app@nexusmart.local":
				sM = m
			}
		}
		if bN.UserID == "" || sN.UserID == "" || bM.To == "" || sM.To == "" {
			t.Fatalf("missing buyer or seller delivery: notifs=%+v mails=%+v", notifs, mails)
		}
		return bN, sN, bM, sM
	}

	// 1. Initial REFUNDING (refund_approval_status="" emitted before CreateRefund):
	// must NOT promise immediate payout ("đang tiến hành hoàn tiền"), must state accepted & being processed.
	beforeN, beforeM := len(repo.notifs), len(mail.sent)
	initRefundingPayload, _ := json.Marshal(map[string]any{
		"return_request_id":      "ret-app-1",
		"order_id":               "ord-app-1",
		"order_code":             "NM-2026-APP1",
		"checkout_group_id":      "grp-app-1",
		"buyer_id":               "buyer-ret-app",
		"shop_id":                "shop-ret-app",
		"status":                 "REFUNDING",
		"previous_status":        "ESCALATED",
		"reason":                 "",
		"actor_role":             "admin",
		"refund_approval_status": "",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", Envelope{
		EventID:   "evt-ret-refunding-init",
		EventType: "order.return_updated",
		Payload:   initRefundingPayload,
	}); err != nil || dup {
		t.Fatalf("initial REFUNDING failed: dup=%v err=%v", dup, err)
	}
	bN, sN, bM, sM := splitByRole(t, repo.notifs[beforeN:], mail.sent[beforeM:])
	assertNoRawEnums(t, "REFUNDING(empty)", repo.notifs[beforeN:], mail.sent[beforeM:])
	if !strings.Contains(strings.ToLower(bN.Body), "đã được chấp thuận, khoản hoàn đang được xử lý") {
		t.Errorf("initial REFUNDING buyer body must state 'đã được chấp thuận, khoản hoàn đang được xử lý', got %q", bN.Body)
	}
	if strings.Contains(strings.ToLower(bN.Body+" "+bM.Body), "đang tiến hành hoàn tiền") {
		t.Errorf("initial REFUNDING must not say 'đang tiến hành hoàn tiền' before CreateRefund, got %q", bN.Body)
	}
	if !strings.Contains(strings.ToLower(sN.Body), "khoản hoàn đang được xử lý") || !strings.Contains(strings.ToLower(sM.Body), "khoản hoàn đang được xử lý") {
		t.Errorf("initial REFUNDING seller copy mismatch: inApp=%q email=%q", sN.Body, sM.Body)
	}

	// 2. REFUNDING with refund_approval_status="PENDING_APPROVAL":
	// must state return approved & waiting for refund approval; must NOT say "đang tiến hành hoàn tiền".
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	pendingApprovalPayload, _ := json.Marshal(map[string]any{
		"return_request_id":      "ret-app-1",
		"order_id":               "ord-app-1",
		"order_code":             "NM-2026-APP1",
		"checkout_group_id":      "grp-app-1",
		"buyer_id":               "buyer-ret-app",
		"shop_id":                "shop-ret-app",
		"status":                 "REFUNDING",
		"previous_status":        "ESCALATED",
		"reason":                 "",
		"actor_role":             "admin",
		"refund_approval_status": "PENDING_APPROVAL",
	})
	pendingApprovalEnv := Envelope{
		EventID:   "evt-ret-refunding-pending-approval",
		EventType: "order.return_updated",
		Payload:   pendingApprovalPayload,
	}
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", pendingApprovalEnv); err != nil || dup {
		t.Fatalf("REFUNDING PENDING_APPROVAL failed: dup=%v err=%v", dup, err)
	}
	bN, sN, bM, sM = splitByRole(t, repo.notifs[beforeN:], mail.sent[beforeM:])
	assertNoRawEnums(t, "REFUNDING(PENDING_APPROVAL)", repo.notifs[beforeN:], mail.sent[beforeM:])
	for _, pair := range []struct {
		who, text string
	}{
		{"buyer-inapp", bN.Title + " " + bN.Body},
		{"buyer-email", bM.Subject + " " + bM.Body},
		{"seller-inapp", sN.Title + " " + sN.Body},
		{"seller-email", sM.Subject + " " + sM.Body},
	} {
		lower := strings.ToLower(pair.text)
		if !strings.Contains(lower, "đã được chấp thuận") || !strings.Contains(lower, "chờ phê duyệt hoàn tiền") {
			t.Errorf("%s (PENDING_APPROVAL) missing expected wording: %q", pair.who, pair.text)
		}
		if strings.Contains(lower, "đang tiến hành hoàn tiền") {
			t.Errorf("%s (PENDING_APPROVAL) must NOT say 'đang tiến hành hoàn tiền': %q", pair.who, pair.text)
		}
	}

	// 3. REFUNDING with refund_approval_status="APPROVED": preserves existing wording.
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	approvedPayload, _ := json.Marshal(map[string]any{
		"return_request_id":      "ret-app-1",
		"order_id":               "ord-app-1",
		"order_code":             "NM-2026-APP1",
		"checkout_group_id":      "grp-app-1",
		"buyer_id":               "buyer-ret-app",
		"shop_id":                "shop-ret-app",
		"status":                 "REFUNDING",
		"previous_status":        "REFUNDING",
		"reason":                 "",
		"actor_role":             "system",
		"refund_approval_status": "APPROVED",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", Envelope{
		EventID:   "evt-ret-refunding-approved",
		EventType: "order.return_updated",
		Payload:   approvedPayload,
	}); err != nil || dup {
		t.Fatalf("REFUNDING APPROVED failed: dup=%v err=%v", dup, err)
	}
	bN, sN, bM, sM = splitByRole(t, repo.notifs[beforeN:], mail.sent[beforeM:])
	assertNoRawEnums(t, "REFUNDING(APPROVED)", repo.notifs[beforeN:], mail.sent[beforeM:])
	if !strings.Contains(bN.Body, "Hệ thống đang tiến hành hoàn tiền cho bạn") || !strings.Contains(bM.Body, "Hệ thống đang tiến hành hoàn tiền cho bạn") {
		t.Errorf("REFUNDING APPROVED buyer copy should keep legacy phrasing, got inApp=%q email=%q", bN.Body, bM.Body)
	}
	if !strings.Contains(sN.Body, "đang được xử lý hoàn tiền cho người mua") || !strings.Contains(sM.Body, "đang được xử lý hoàn tiền cho người mua") {
		t.Errorf("REFUNDING APPROVED seller copy should keep legacy phrasing, got inApp=%q email=%q", sN.Body, sM.Body)
	}

	// 4. ESCALATED with reason="refund_rejected" (REFUNDING -> ESCALATED due to large refund rejection):
	// must clearly say refund needs re-review, not yet refunded, team will re-check/contact;
	// must NOT say generic dispute copy and must NOT leak approver name or internal notes.
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	rejectedPayload, _ := json.Marshal(map[string]any{
		"return_request_id":      "ret-app-1",
		"order_id":               "ord-app-1",
		"order_code":             "NM-2026-APP1",
		"checkout_group_id":      "grp-app-1",
		"buyer_id":               "buyer-ret-app",
		"shop_id":                "shop-ret-app",
		"status":                 "ESCALATED",
		"previous_status":        "REFUNDING",
		"reason":                 "refund_rejected",
		"actor_role":             "system",
		"refund_approval_status": "REJECTED",
		"rejected_by":            "Kế Toán Trưởng Nguyễn Văn Z",
		"approval_note":          "Ghi chú nội bộ: nghi ngờ chia nhỏ giao dịch",
	})
	rejectedEnv := Envelope{
		EventID:   "evt-ret-escalated-refund-rejected",
		EventType: "order.return_updated",
		Payload:   rejectedPayload,
	}
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", rejectedEnv); err != nil || dup {
		t.Fatalf("ESCALATED refund_rejected failed: dup=%v err=%v", dup, err)
	}
	bN, sN, bM, sM = splitByRole(t, repo.notifs[beforeN:], mail.sent[beforeM:])
	assertNoRawEnums(t, "ESCALATED(refund_rejected)", repo.notifs[beforeN:], mail.sent[beforeM:])
	for _, pair := range []struct {
		who, text string
	}{
		{"buyer-inapp", bN.Title + " " + bN.Body},
		{"buyer-email", bM.Subject + " " + bM.Body},
		{"seller-inapp", sN.Title + " " + sN.Body},
		{"seller-email", sM.Subject + " " + sM.Body},
	} {
		lower := strings.ToLower(pair.text)
		if strings.Contains(lower, "chuyển tới quản trị viên") || strings.Contains(lower, "khiếu nại trả hàng") {
			t.Errorf("%s (refund_rejected) must NOT use generic dispute copy: %q", pair.who, pair.text)
		}
		if !strings.Contains(lower, "xem xét lại") || !strings.Contains(lower, "chưa được hoàn tiền") || !strings.Contains(lower, "kiểm tra lại") {
			t.Errorf("%s (refund_rejected) missing re-review / not-refunded / team follow-up wording: %q", pair.who, pair.text)
		}
		if strings.Contains(pair.text, "Nguyễn Văn Z") || strings.Contains(pair.text, "Ghi chú nội bộ") {
			t.Errorf("SECURITY: %s leaked approver name or internal note: %q", pair.who, pair.text)
		}
	}
	if !strings.Contains(strings.ToLower(bN.Body), "liên hệ") || !strings.Contains(strings.ToLower(bM.Body), "liên hệ") {
		t.Errorf("buyer refund_rejected copy should mention team will contact buyer: inApp=%q email=%q", bN.Body, bM.Body)
	}

	// 4b. Security defense-in-depth: even if refund_approval_status="REJECTED" carries internal note in reason, never leak it.
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	leakGuardPayload, _ := json.Marshal(map[string]any{
		"return_request_id":      "ret-app-1",
		"order_id":               "ord-app-1",
		"order_code":             "NM-2026-APP1",
		"buyer_id":               "buyer-ret-app",
		"shop_id":                "shop-ret-app",
		"status":                 "ESCALATED",
		"previous_status":        "REFUNDING",
		"reason":                 "Checker Lê Thị Y từ chối: hồ sơ nội bộ chưa đủ chữ ký",
		"actor_role":             "system",
		"refund_approval_status": "REJECTED",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", Envelope{
		EventID:   "evt-ret-escalated-rejected-leak-guard",
		EventType: "order.return_updated",
		Payload:   leakGuardPayload,
	}); err != nil || dup {
		t.Fatalf("ESCALATED REJECTED leak guard failed: dup=%v err=%v", dup, err)
	}
	for _, n := range repo.notifs[beforeN:] {
		if strings.Contains(n.Title+" "+n.Body, "Lê Thị Y") || strings.Contains(n.Body, "hồ sơ nội bộ") {
			t.Fatalf("SECURITY: internal checker note/name leaked into in-app notification: %+v", n)
		}
	}
	for _, m := range mail.sent[beforeM:] {
		if strings.Contains(m.Subject+" "+m.Body, "Lê Thị Y") || strings.Contains(m.Body, "hồ sơ nội bộ") {
			t.Fatalf("SECURITY: internal checker note/name leaked into email: %+v", m)
		}
	}

	// 5. Normal ESCALATED (non-refund_rejected) keeps legacy dispute template unchanged.
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	normalEscPayload, _ := json.Marshal(map[string]any{
		"return_request_id": "ret-app-2",
		"order_id":          "ord-app-1",
		"order_code":        "NM-2026-APP1",
		"buyer_id":          "buyer-ret-app",
		"shop_id":           "shop-ret-app",
		"status":            "ESCALATED",
		"previous_status":   "REJECTED",
		"reason":            "Sản phẩm không đúng mô tả",
		"actor_role":        "buyer",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", Envelope{
		EventID:   "evt-ret-escalated-normal",
		EventType: "order.return_updated",
		Payload:   normalEscPayload,
	}); err != nil || dup {
		t.Fatalf("normal ESCALATED failed: dup=%v err=%v", dup, err)
	}
	bN, sN, _, _ = splitByRole(t, repo.notifs[beforeN:], mail.sent[beforeM:])
	if !strings.Contains(bN.Body, "Khiếu nại trả hàng cho đơn NM-2026-APP1 đã được chuyển tới quản trị viên NexusMart xem xét phân xử.") ||
		!strings.Contains(bN.Body, "Sản phẩm không đúng mô tả") {
		t.Errorf("normal ESCALATED buyer template changed unexpectedly: %q", bN.Body)
	}
	if !strings.Contains(sN.Body, "chuyển sang trạng thái khiếu nại để quản trị viên NexusMart phân xử") {
		t.Errorf("normal ESCALATED seller template changed unexpectedly: %q", sN.Body)
	}

	// 6. Idempotency on duplicate replay & out-of-order delivery by event_id.
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", rejectedEnv); err != nil || !dup {
		t.Fatalf("expected duplicate=true on replay of rejectedEnv, got dup=%v err=%v", dup, err)
	}
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", pendingApprovalEnv); err != nil || !dup {
		t.Fatalf("expected duplicate=true on replay of pendingApprovalEnv, got dup=%v err=%v", dup, err)
	}
	if len(repo.notifs) != beforeN || len(mail.sent) != beforeM {
		t.Fatalf("replayed events must not emit duplicate notifications or emails")
	}

	// Out-of-order: ESCALATED(refund_rejected) arrives before order.created (no buyer_id/shop_id in payload) ->
	// fails with ErrOrderParticipantNotFound without recording processed_events, then succeeds after order.created.
	oooRejPayload, _ := json.Marshal(map[string]any{
		"return_request_id":      "ret-app-ooo",
		"order_id":               "ord-app-ooo",
		"status":                 "ESCALATED",
		"previous_status":        "REFUNDING",
		"reason":                 "refund_rejected",
		"refund_approval_status": "REJECTED",
	})
	oooRejEnv := Envelope{
		EventID:   "evt-ret-ooo-refund-rejected",
		EventType: "order.return_updated",
		Payload:   oooRejPayload,
	}
	if _, err := svc.ProcessEvent(context.Background(), "order.return_updated", oooRejEnv); !errors.Is(err, ErrOrderParticipantNotFound) {
		t.Fatalf("expected ErrOrderParticipantNotFound for out-of-order refund_rejected before order.created, got %v", err)
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), oooRejEnv.EventID); processed {
		t.Fatalf("out-of-order event must NOT mark processed_events")
	}
	repo.participants["ord-app-ooo"] = store.OrderParticipant{
		OrderID:   "ord-app-ooo",
		BuyerID:   "buyer-ret-app",
		ShopID:    "shop-ret-app",
		OrderCode: "NM-2026-OOO1",
	}
	beforeN, beforeM = len(repo.notifs), len(mail.sent)
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", oooRejEnv); err != nil || dup {
		t.Fatalf("retry of out-of-order refund_rejected failed: dup=%v err=%v", dup, err)
	}
	bN, sN, _, _ = splitByRole(t, repo.notifs[beforeN:], mail.sent[beforeM:])
	if !strings.Contains(bN.Body, "NM-2026-OOO1") || !strings.Contains(sN.Body, "NM-2026-OOO1") || !strings.Contains(strings.ToLower(bN.Body), "chưa được hoàn tiền") {
		t.Errorf("out-of-order retry copy mismatch: buyer=%q seller=%q", bN.Body, sN.Body)
	}
	// Late REFUNDING(PENDING_APPROVAL) arriving after ESCALATED(refund_rejected) with a distinct event_id processes once cleanly; replaying either stays idempotent.
	latePendingEnv := Envelope{
		EventID:   "evt-ret-ooo-late-pending",
		EventType: "order.return_updated",
		Payload:   []byte(`{"return_request_id":"ret-app-ooo","order_id":"ord-app-ooo","status":"REFUNDING","refund_approval_status":"PENDING_APPROVAL"}`),
	}
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", latePendingEnv); err != nil || dup {
		t.Fatalf("late out-of-order PENDING_APPROVAL failed: dup=%v err=%v", dup, err)
	}
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", latePendingEnv); err != nil || !dup {
		t.Fatalf("replay of latePendingEnv must be idempotent, got dup=%v err=%v", dup, err)
	}
	if dup, err := svc.ProcessEvent(context.Background(), "order.return_updated", oooRejEnv); err != nil || !dup {
		t.Fatalf("replay of oooRejEnv must be idempotent, got dup=%v err=%v", dup, err)
	}
	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}
