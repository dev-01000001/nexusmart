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

	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProductReviewedNotificationsApproveAndReject(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"seller-prod": "seller-prod@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-prod": "seller-prod",
		},
		shopNames: map[string]string{
			"shop-prod": "Xưởng Dệt Bạch Đằng",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Transient GetShop failure must NOT mark processed_events and must return error for NATS retry.
	resolver.shopErr = status.Error(codes.Unavailable, "user service unavailable")
	rejectPayload, _ := json.Marshal(map[string]any{
		"product_id":   "prod-101",
		"product_name": "Áo Khoác Gió 2 Lớp",
		"shop_id":      "shop-prod",
		"decision":     "REJECT",
		"reason":       "Thiếu ảnh chụp tem mác thực tế",
		"occurred_at":  "2026-11-23T09:00:00Z",
	})
	rejectEnv := Envelope{
		EventID:   "evt-prod-reject-1",
		EventType: "product.reviewed",
		Payload:   rejectPayload,
	}
	if _, err := svc.ProcessEvent(context.Background(), "product.reviewed", rejectEnv); err == nil {
		t.Fatalf("expected transient GetShop error on product.reviewed")
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-prod-reject-1"); processed {
		t.Fatalf("failed product.reviewed must NOT record processed_events")
	}

	// 2. Recover UserService -> REJECT delivers SYSTEM in-app (/seller/san-pham/prod-101) + Vietnamese email with reason, zero raw enum.
	resolver.shopErr = nil
	if dup, err := svc.ProcessEvent(context.Background(), "product.reviewed", rejectEnv); err != nil || dup {
		t.Fatalf("retry product.reviewed REJECT failed: dup=%v err=%v", dup, err)
	}
	notifs, unread, total, err := svc.ListNotifications(context.Background(), "seller-prod", store.CategorySystem, false, 1, 20)
	if err != nil || total != 1 || unread != 1 || len(notifs) != 1 {
		t.Fatalf("expected 1 unread SYSTEM notification for rejected product, got len=%d unread=%d total=%d err=%v", len(notifs), unread, total, err)
	}
	rejNotif := notifs[0]
	if rejNotif.Link != "/seller/san-pham/prod-101" {
		t.Errorf("rejected product link = %q, want /seller/san-pham/prod-101", rejNotif.Link)
	}
	if !strings.Contains(rejNotif.Title+" "+rejNotif.Body, "Áo Khoác Gió 2 Lớp") || !strings.Contains(rejNotif.Body, "Thiếu ảnh chụp tem mác thực tế") {
		t.Errorf("rejected product notification missing product name or reason: title=%q body=%q", rejNotif.Title, rejNotif.Body)
	}
	if strings.Contains(rejNotif.Title+"\n"+rejNotif.Body, "REJECT") {
		t.Errorf("rejected product notification leaked raw enum REJECT: title=%q body=%q", rejNotif.Title, rejNotif.Body)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "seller-prod@nexusmart.local" {
		t.Fatalf("expected 1 email to seller-prod, got %+v", mail.sent)
	}
	if !strings.Contains(mail.sent[0].Body, "Thiếu ảnh chụp tem mác thực tế") || strings.Contains(mail.sent[0].Subject+"\n"+mail.sent[0].Body, "REJECT") {
		t.Errorf("rejected product email must include reason in Vietnamese without raw enum, got %+v", mail.sent[0])
	}

	// 3. APPROVE delivers Vietnamese in-app (/seller/san-pham/prod-101) + email without raw enum APPROVE.
	approvePayload, _ := json.Marshal(map[string]any{
		"product_id":   "prod-101",
		"product_name": "Áo Khoác Gió 2 Lớp",
		"shop_id":      "shop-prod",
		"decision":     "APPROVE",
		"reason":       "Đạt tiêu chuẩn hiển thị",
		"occurred_at":  "2026-11-23T09:30:00Z",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "product.reviewed", Envelope{
		EventID:   "evt-prod-approve-1",
		EventType: "product.reviewed",
		Payload:   approvePayload,
	}); err != nil || dup {
		t.Fatalf("product.reviewed APPROVE failed: dup=%v err=%v", dup, err)
	}
	notifs, unread, total, _ = svc.ListNotifications(context.Background(), "seller-prod", store.CategorySystem, false, 1, 20)
	if total != 2 || unread != 2 || len(notifs) != 2 {
		t.Fatalf("expected 2 SYSTEM notifications after approve, got len=%d unread=%d total=%d", len(notifs), unread, total)
	}
	appNotif := notifs[1]
	if appNotif.Link != "/seller/san-pham/prod-101" {
		t.Errorf("approved product link = %q, want /seller/san-pham/prod-101", appNotif.Link)
	}
	if strings.Contains(appNotif.Title+"\n"+appNotif.Body, "APPROVE") {
		t.Errorf("approved product notification leaked raw enum APPROVE: title=%q body=%q", appNotif.Title, appNotif.Body)
	}
	if len(mail.sent) != 2 || strings.Contains(mail.sent[1].Subject+"\n"+mail.sent[1].Body, "APPROVE") {
		t.Errorf("approved product email must be Vietnamese without raw enum, got %+v", mail.sent)
	}

	// 4. Seller disables system_email (and attempts system_in_app=false): still gets in-app (UC-B23), email suppressed.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:      "seller-prod",
		SystemInApp: false,
		SystemEmail: false,
	})
	beforeMails := len(mail.sent)
	if _, err := svc.ProcessEvent(context.Background(), "product.reviewed", Envelope{
		EventID:   "evt-prod-approve-2",
		EventType: "product.reviewed",
		Payload:   approvePayload,
	}); err != nil {
		t.Fatalf("product.reviewed with system_email=false failed: %v", err)
	}
	if _, _, total3, _ := svc.ListNotifications(context.Background(), "seller-prod", store.CategorySystem, false, 1, 20); total3 != 3 {
		t.Fatalf("UC-B23: seller must still receive SYSTEM in-app notification, got total=%d", total3)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("expected seller email suppressed when system_email=false, got %+v", mail.sent[beforeMails:])
	}

	// 5. Malformed payload (missing shop_id or product_id) returns ErrMalformedPayload.
	badPayload, _ := json.Marshal(map[string]any{"product_id": "prod-x", "decision": "APPROVE"})
	if _, err := svc.ProcessEvent(context.Background(), "product.reviewed", Envelope{
		EventID:   "evt-prod-bad",
		EventType: "product.reviewed",
		Payload:   badPayload,
	}); !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("expected ErrMalformedPayload for missing shop_id, got %v", err)
	}

	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestPromotionVoucherExpiringPreferencesAndDedup(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-promo-1": "promo1@nexusmart.local",
			"buyer-promo-2": "promo2@nexusmart.local",
			"buyer-promo-3": "promo3@nexusmart.local",
			"buyer-promo-4": "promo4@nexusmart.local",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Default prefs (promotion_in_app=true, promotion_email=true): delivers 1 PROMOTION in-app + 1 email.
	payload1, _ := json.Marshal(map[string]any{
		"user_id":    "buyer-promo-1",
		"voucher_id": "vch-001",
		"code":       "GIAM50K",
		"ends_at":    "2026-11-24T12:00:00Z",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-1",
		EventType: "promotion.voucher_expiring",
		Payload:   payload1,
	}); err != nil || dup {
		t.Fatalf("promotion.voucher_expiring failed: dup=%v err=%v", dup, err)
	}

	notifs, unread, total, err := svc.ListNotifications(context.Background(), "buyer-promo-1", store.CategoryPromotion, false, 1, 20)
	if err != nil || total != 1 || unread != 1 || len(notifs) != 1 {
		t.Fatalf("expected 1 PROMOTION notification, got len=%d unread=%d total=%d err=%v", len(notifs), unread, total, err)
	}
	if notifs[0].Category != store.CategoryPromotion || notifs[0].Link != "/tai-khoan?tab=voucher" {
		t.Errorf("unexpected category/link: %+v", notifs[0])
	}
	if !strings.Contains(notifs[0].Title+" "+notifs[0].Body, "GIAM50K") || !strings.Contains(notifs[0].Body, "19:00 ngày 24/11/2026 (GMT+7)") {
		t.Errorf("expected voucher code and GMT+7 expiry in Vietnamese copy, got title=%q body=%q", notifs[0].Title, notifs[0].Body)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "promo1@nexusmart.local" || !strings.Contains(mail.sent[0].Subject, "GIAM50K") {
		t.Fatalf("expected 1 voucher expiring email, got %+v", mail.sent)
	}

	// 2. Dedup across DIFFERENT event_id for the SAME (user_id, voucher_id): MUST NOT duplicate in-app or email!
	if dup, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-1-duplicate-run",
		EventType: "promotion.voucher_expiring",
		Payload:   payload1,
	}); err != nil || dup {
		t.Fatalf("duplicate logical voucher_expiring failed: dup=%v err=%v", dup, err)
	}
	if _, _, totalAfterDup, _ := svc.ListNotifications(context.Background(), "buyer-promo-1", store.CategoryPromotion, false, 1, 20); totalAfterDup != 1 {
		t.Fatalf("logical dedup failed: expected still 1 in-app notification for (buyer-promo-1, vch-001), got %d", totalAfterDup)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("logical dedup failed: expected still 1 email for (buyer-promo-1, vch-001), got %d", len(mail.sent))
	}

	// 3. Same user with a DIFFERENT voucher_id receives a new notification + email.
	payloadDiffVoucher, _ := json.Marshal(map[string]any{
		"user_id":    "buyer-promo-1",
		"voucher_id": "vch-002",
		"code":       "FREESHIP30",
		"ends_at":    "2026-11-24T14:00:00Z",
	})
	if _, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-2",
		EventType: "promotion.voucher_expiring",
		Payload:   payloadDiffVoucher,
	}); err != nil {
		t.Fatalf("different voucher_id failed: %v", err)
	}
	if _, _, total2, _ := svc.ListNotifications(context.Background(), "buyer-promo-1", store.CategoryPromotion, false, 1, 20); total2 != 2 {
		t.Fatalf("expected 2 PROMOTION notifications for 2 distinct vouchers, got %d", total2)
	}
	if len(mail.sent) != 2 {
		t.Fatalf("expected 2 emails for 2 distinct vouchers, got %d", len(mail.sent))
	}

	// 4. Respect preferences: buyer-promo-2 turns off BOTH promotion_in_app & promotion_email -> 0 in-app, 0 email!
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:         "buyer-promo-2",
		PromotionInApp: false,
		PromotionEmail: false,
	})
	payloadU2, _ := json.Marshal(map[string]any{
		"user_id":    "buyer-promo-2",
		"voucher_id": "vch-001",
		"code":       "GIAM50K",
		"ends_at":    "2026-11-24T12:00:00Z",
	})
	beforeMails := len(mail.sent)
	if _, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-u2",
		EventType: "promotion.voucher_expiring",
		Payload:   payloadU2,
	}); err != nil {
		t.Fatalf("u2 promotion.voucher_expiring failed: %v", err)
	}
	if _, _, totalU2, _ := svc.ListNotifications(context.Background(), "buyer-promo-2", store.CategoryPromotion, false, 1, 20); totalU2 != 0 {
		t.Fatalf("promotion_in_app=false must suppress PROMOTION in-app notification, got %d", totalU2)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("promotion_email=false must suppress PROMOTION email, got %+v", mail.sent[beforeMails:])
	}

	// 5. Respect preferences: buyer-promo-3 disables promotion_in_app only -> 0 in-app, 1 email (still deduped on second event!).
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:         "buyer-promo-3",
		PromotionInApp: false,
		PromotionEmail: true,
	})
	payloadU3, _ := json.Marshal(map[string]any{
		"user_id":    "buyer-promo-3",
		"voucher_id": "vch-001",
		"code":       "GIAM50K",
		"ends_at":    "2026-11-24T12:00:00Z",
	})
	beforeMails = len(mail.sent)
	if _, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-u3-a",
		EventType: "promotion.voucher_expiring",
		Payload:   payloadU3,
	}); err != nil {
		t.Fatalf("u3 first event failed: %v", err)
	}
	if _, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-u3-b",
		EventType: "promotion.voucher_expiring",
		Payload:   payloadU3,
	}); err != nil {
		t.Fatalf("u3 second event failed: %v", err)
	}
	if _, _, totalU3, _ := svc.ListNotifications(context.Background(), "buyer-promo-3", "", false, 1, 20); totalU3 != 0 {
		t.Fatalf("u3 promotion_in_app=false must create 0 in-app notifications, got %d", totalU3)
	}
	if len(mail.sent)-beforeMails != 1 {
		t.Fatalf("u3 promotion_email=true must send exactly 1 deduped email across 2 events, got %d", len(mail.sent)-beforeMails)
	}

	// 6. Respect preferences: buyer-promo-4 disables promotion_email only -> 1 in-app, 0 email; transient SMTP test on retry.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:         "buyer-promo-4",
		PromotionInApp: true,
		PromotionEmail: false,
	})
	payloadU4, _ := json.Marshal(map[string]any{
		"user_id":    "buyer-promo-4",
		"voucher_id": "vch-001",
		"code":       "GIAM50K",
		"ends_at":    "2026-11-24T12:00:00Z",
	})
	beforeMails = len(mail.sent)
	if _, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-exp-u4",
		EventType: "promotion.voucher_expiring",
		Payload:   payloadU4,
	}); err != nil {
		t.Fatalf("u4 event failed: %v", err)
	}
	if _, _, totalU4, _ := svc.ListNotifications(context.Background(), "buyer-promo-4", store.CategoryPromotion, false, 1, 20); totalU4 != 1 {
		t.Fatalf("u4 promotion_in_app=true must create 1 in-app notification, got %d", totalU4)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("u4 promotion_email=false must send 0 emails, got %+v", mail.sent[beforeMails:])
	}

	// 7. Malformed payload returns ErrMalformedPayload.
	badPayload, _ := json.Marshal(map[string]any{"user_id": "buyer-promo-1", "voucher_id": "vch-missing-code"})
	if _, err := svc.ProcessEvent(context.Background(), "promotion.voucher_expiring", Envelope{
		EventID:   "evt-vch-bad",
		EventType: "promotion.voucher_expiring",
		Payload:   badPayload,
	}); !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("expected ErrMalformedPayload for missing voucher code, got %v", err)
	}

	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestShopBankAccountOTPEmailOnlyAndSecurity(t *testing.T) {
	var logBuf bytes.Buffer
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewJSONHandler(&logBuf, nil)))

	// Even when seller disabled system_email, bank OTP security email MUST still send.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:      "seller-bank-1",
		SystemInApp: true,
		SystemEmail: false,
	})

	secretOTP := "749205"
	payload, _ := json.Marshal(map[string]any{
		"user_id":               "seller-bank-1",
		"shop_id":               "shop-bank-1",
		"email":                 "bank-seller@nexusmart.local",
		"full_name":             "Trần Thị Mai",
		"otp":                   secretOTP,
		"expires_at":            "2026-11-23T11:15:00Z",
		"bank_code":             "TCB",
		"account_number_masked": "******8899",
	})
	env := Envelope{
		EventID:   "evt-bank-otp-1",
		EventType: "user.shop_bank_account_otp",
		Payload:   payload,
	}

	// 1. Transient SMTP failure: OTP must NOT leak into error or JSON logs.
	mail.err = errors.New("smtp dial refused")
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_bank_account_otp", env); err == nil {
		t.Fatalf("expected error on SMTP failure")
	} else if strings.Contains(err.Error(), secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into error: %v", err)
	}
	if strings.Contains(logBuf.String(), secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into logs on SMTP failure: %s", logBuf.String())
	}

	// 2. Recover SMTP -> retry delivers 1 OTP email, 0 in-app notifications, caches contact & shop owner.
	mail.err = nil
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_bank_account_otp", env); err != nil || dup {
		t.Fatalf("retry bank OTP failed: dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("expected 1 bank OTP email, got %d", len(mail.sent))
	}
	sent := mail.sent[0]
	if sent.To != "bank-seller@nexusmart.local" {
		t.Errorf("email To = %q, want bank-seller@nexusmart.local", sent.To)
	}
	if strings.Contains(sent.Subject, secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into email Subject: %q", sent.Subject)
	}
	if !strings.Contains(sent.Body, secretOTP) || !strings.Contains(sent.Body, "TCB") || !strings.Contains(sent.Body, "******8899") {
		t.Errorf("bank OTP email body missing expected details: %q", sent.Body)
	}

	// Zero in-app notifications (OTP must NEVER appear in notifications table).
	notifs, unread, total, _ := svc.ListNotifications(context.Background(), "seller-bank-1", "", false, 1, 20)
	if total != 0 || unread != 0 || len(notifs) != 0 {
		t.Fatalf("user.shop_bank_account_otp must be email-only (0 in-app notifications), got total=%d notifs=%+v", total, notifs)
	}
	for _, n := range repo.notifs {
		if strings.Contains(n.Title+n.Body+n.Link, secretOTP) {
			t.Fatalf("SECURITY: bank OTP persisted in notifications table: %+v", n)
		}
	}
	if strings.Contains(logBuf.String(), secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into logs: %s", logBuf.String())
	}
	if c, ok, _ := repo.GetUserContact(context.Background(), "seller-bank-1"); !ok || c.Email != "bank-seller@nexusmart.local" {
		t.Errorf("expected contact cached, got ok=%v c=%+v", ok, c)
	}
	if so, ok, _ := repo.GetShopOwner(context.Background(), "shop-bank-1"); !ok || so.OwnerUserID != "seller-bank-1" {
		t.Errorf("expected shop owner cached, got ok=%v so=%+v", ok, so)
	}

	// 3. Duplicate replay of same event_id does not resend email.
	if dup, err := svc.ProcessEvent(context.Background(), "user.shop_bank_account_otp", env); err != nil || !dup {
		t.Fatalf("expected duplicate=true on replay, got dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("duplicate event must not resend bank OTP email, got %d", len(mail.sent))
	}

	// 4. Malformed payload (missing otp) returns ErrMalformedPayload without leaking anything.
	badPayload, _ := json.Marshal(map[string]any{"user_id": "seller-bank-1", "email": "bank-seller@nexusmart.local"})
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_bank_account_otp", Envelope{
		EventID:   "evt-bank-otp-bad",
		EventType: "user.shop_bank_account_otp",
		Payload:   badPayload,
	}); !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("expected ErrMalformedPayload for missing otp, got %v", err)
	}
	if mail.calledInTx {
		t.Fatalf("SMTP must never execute inside DB tx")
	}
}

func TestHeaderUnreadCountAcrossCategoriesAndMarkRead(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{
		emails:     map[string]string{"u-hdr": "hdr@nexusmart.local"},
		shopOwners: map[string]string{"shop-hdr": "u-hdr"},
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Create 3 notifications across ORDER, SYSTEM, and PROMOTION for u-hdr.
	ordPayload, _ := json.Marshal(map[string]any{
		"order_id": "ord-hdr-1", "order_code": "NM-HDR-1", "buyer_id": "u-hdr", "shop_id": "shop-hdr",
		"payment_method": "COD", "total_payable_vnd": 150000,
	})
	prodPayload, _ := json.Marshal(map[string]any{
		"product_id": "prod-hdr-1", "product_name": "Quần Kaki", "shop_id": "shop-hdr", "decision": "APPROVE",
	})
	promoPayload, _ := json.Marshal(map[string]any{
		"user_id": "u-hdr", "voucher_id": "vch-hdr-1", "code": "HDR20", "ends_at": "2026-11-24T12:00:00Z",
	})

	for _, tc := range []struct {
		id, subj string
		raw      []byte
	}{
		{"evt-hdr-1", "order.created", ordPayload},
		{"evt-hdr-2", "product.reviewed", prodPayload},
		{"evt-hdr-3", "promotion.voucher_expiring", promoPayload},
	} {
		if _, err := svc.ProcessEvent(context.Background(), tc.subj, Envelope{
			EventID: tc.id, EventType: tc.subj, Payload: tc.raw,
		}); err != nil {
			t.Fatalf("process %s failed: %v", tc.subj, err)
		}
	}

	// Header poll with page_size=1 and category filter still returns TOTAL unread_count across all categories (3).
	items, unread, total, err := svc.ListNotifications(context.Background(), "u-hdr", store.CategoryPromotion, false, 1, 1)
	if err != nil || len(items) != 1 || total != 1 || unread != 3 {
		t.Fatalf("header unread_count mismatch with category filter: len=%d total=%d unread=%d err=%v", len(items), total, unread, err)
	}
	// Header poll with page=1, page_size=5 returns all 3 items and unread_count=3.
	allItems, unreadAll, totalAll, err := svc.ListNotifications(context.Background(), "u-hdr", "", true, 1, 5)
	if err != nil || len(allItems) != 3 || totalAll != 3 || unreadAll != 3 {
		t.Fatalf("header unread_only poll mismatch: len=%d total=%d unread=%d err=%v", len(allItems), totalAll, unreadAll, err)
	}
	// Isolated for another user.
	_, otherUnread, otherTotal, _ := svc.ListNotifications(context.Background(), "u-other", "", false, 1, 5)
	if otherUnread != 0 || otherTotal != 0 {
		t.Fatalf("expected 0 unread for other user, got unread=%d total=%d", otherUnread, otherTotal)
	}

	// Mark 1 read -> unread_count becomes 2; repeating same ID is idempotent (updated=0, unread=2).
	upd, unreadAfter1, err := svc.MarkRead(context.Background(), "u-hdr", []string{allItems[0].ID}, false)
	if err != nil || upd != 1 || unreadAfter1 != 2 {
		t.Fatalf("MarkRead single failed: upd=%d unread=%d err=%v", upd, unreadAfter1, err)
	}
	updDup, unreadDup, err := svc.MarkRead(context.Background(), "u-hdr", []string{allItems[0].ID}, false)
	if err != nil || updDup != 0 || unreadDup != 2 {
		t.Fatalf("idempotent MarkRead failed: upd=%d unread=%d err=%v", updDup, unreadDup, err)
	}

	// Mark all read -> unread_count becomes 0.
	updAll, unreadZero, err := svc.MarkRead(context.Background(), "u-hdr", nil, true)
	if err != nil || updAll != 2 || unreadZero != 0 {
		t.Fatalf("MarkRead all failed: upd=%d unread=%d err=%v", updAll, unreadZero, err)
	}
	_, verifyZero, _, _ := svc.ListNotifications(context.Background(), "u-hdr", "", false, 1, 5)
	if verifyZero != 0 {
		t.Fatalf("expected header unread_count=0 after mark_all, got %d", verifyZero)
	}
}

func TestPutCachedShopOwnerPreservesExistingShopName(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Seed RAM cache & DB with full shop metadata via user.shop_reviewed.
	reviewedPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-preserve",
		"owner_user_id": "seller-preserve",
		"shop_name":     "Gốm Sứ Bát Tràng",
		"decision":      "APPROVE",
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_reviewed", Envelope{
		EventID:   "evt-preserve-1",
		EventType: "user.shop_reviewed",
		Payload:   reviewedPayload,
	}); err != nil {
		t.Fatalf("user.shop_reviewed failed: %v", err)
	}

	// 2. Process user.shop_bank_account_otp (carries shop_id/user_id without shop_name).
	otpPayload, _ := json.Marshal(map[string]any{
		"user_id":               "seller-preserve",
		"shop_id":               "shop-preserve",
		"email":                 "preserve@nexusmart.local",
		"full_name":             "Chủ Shop",
		"otp":                   "123456",
		"expires_at":            "2026-11-23T11:30:00Z",
		"bank_code":             "VCB",
		"account_number_masked": "******0001",
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_bank_account_otp", Envelope{
		EventID:   "evt-preserve-2",
		EventType: "user.shop_bank_account_otp",
		Payload:   otpPayload,
	}); err != nil {
		t.Fatalf("user.shop_bank_account_otp failed: %v", err)
	}

	cached, ok := svc.getCachedShopOwner("shop-preserve")
	if !ok {
		t.Fatalf("expected shop-preserve in RAM cache")
	}
	if cached.ShopName != "Gốm Sứ Bát Tràng" {
		t.Fatalf("putCachedShopOwner overwrote ShopName with empty value: got %q, want %q", cached.ShopName, "Gốm Sứ Bát Tràng")
	}

	// 3. Non-empty ShopName still updates the cached value.
	svc.putCachedShopOwner(store.ShopOwner{
		ShopID:      "shop-preserve",
		OwnerUserID: "seller-preserve",
		ShopName:    "Gốm Sứ Bát Tràng Mới",
	})
	updated, ok := svc.getCachedShopOwner("shop-preserve")
	if !ok || updated.ShopName != "Gốm Sứ Bát Tràng Mới" {
		t.Fatalf("expected non-empty ShopName to update cache, got ok=%v cached=%+v", ok, updated)
	}
}
