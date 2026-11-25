package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReturnUpdatedNotificationsBuyerAndSellerAllStatuses(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-ret":  "buyer-ret@nexusmart.local",
			"seller-ret": "seller-ret@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-ret": "seller-ret",
		},
		shopNames: map[string]string{
			"shop-ret": "Xưởng Dệt Bạch Đằng",
		},
		txChecker: &repo.inTx,
	}
	siteResolver := &stubSiteResolver{
		hosts: map[string]string{"shop-ret": "bach-dang.nexusmart.vn"},
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSiteLinks(siteResolver, SiteLinkConfig{}))

	// 1. Out-of-order: order.return_updated missing buyer_id/shop_id before order.created MUST fail and NOT record processed_events.
	oooPayload, _ := json.Marshal(map[string]any{
		"return_request_id": "ret-ooo",
		"order_id":          "ord-ret-ooo",
		"status":            "PENDING",
	})
	oooEnv := Envelope{EventID: "evt-ret-ooo", EventType: "order.return_updated", Payload: oooPayload}
	if _, err := svc.ProcessEvent(context.Background(), "order.return_updated", oooEnv); !errors.Is(err, ErrOrderParticipantNotFound) {
		t.Fatalf("expected ErrOrderParticipantNotFound for out-of-order order.return_updated, got %v", err)
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-ret-ooo"); processed {
		t.Fatalf("out-of-order order.return_updated must NOT write processed_events early")
	}

	// Once order.created populates order_participants, replaying evt-ret-ooo succeeds for BOTH buyer and seller!
	ordCreatedPayload, _ := json.Marshal(map[string]any{
		"checkout_group_id": "grp-ret-ooo",
		"order_id":          "ord-ret-ooo",
		"order_code":        "NM-RET-OOO",
		"buyer_id":          "buyer-ret",
		"shop_id":           "shop-ret",
		"payment_method":    "VIETQR",
		"total_payable_vnd": 300000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-ord-ret-ooo",
		EventType: "order.created",
		Payload:   ordCreatedPayload,
	}); err != nil {
		t.Fatalf("order.created failed: %v", err)
	}
	if _, err := svc.ProcessEvent(context.Background(), "order.return_updated", oooEnv); err != nil {
		t.Fatalf("retry of evt-ret-ooo after order.created failed: %v", err)
	}

	// 2. All 10 return statuses notify BOTH buyer (/don-hang/<id>) and seller (/seller/don-hang/<id>) in Vietnamese with zero raw enums.
	statuses := []struct {
		status       string
		wantBuyer    string
		wantSeller   string
		trackingCode string
		reason       string
	}{
		{"PENDING", "yêu cầu trả hàng", "Yêu cầu trả hàng mới", "", "Sản phẩm lỗi đường may"},
		{"AWAITING_SHIPMENT", "đã được chấp nhận", "chờ", "RET-GHN-001", ""},
		{"IN_TRANSIT", "đang được vận chuyển về", "đang trên đường về kho", "RET-GHN-001", ""},
		{"RECEIVED", "đã nhận hàng trả", "đã về kho", "RET-GHN-001", ""},
		{"REJECTED", "từ chối", "từ chối", "", "Không đủ điều kiện trả hàng"},
		{"ESCALATED", "khiếu nại", "khiếu nại", "", "Hàng giao sai màu"},
		{"EVIDENCE_REQUESTED", "bằng chứng", "bằng chứng", "", "Cần ảnh rõ tem mác"},
		{"REFUNDING", "đang", "hoàn tiền", "", ""},
		{"REFUNDED", "hoàn tiền", "hoàn tất", "", ""},
		{"CLOSED", "đã đóng", "đã đóng", "", "Quá hạn gửi hàng trả"},
	}

	rawEnums := []string{
		"RETURN_STATUS_", "PENDING", "AWAITING_SHIPMENT", "IN_TRANSIT",
		"RECEIVED", "REJECTED", "ESCALATED", "EVIDENCE_REQUESTED",
		"REFUNDING", "REFUNDED", "CLOSED",
	}

	for _, tc := range statuses {
		beforeNotifs := len(repo.notifs)
		beforeMails := len(mail.sent)

		payload, _ := json.Marshal(map[string]any{
			"return_request_id":    "ret-1",
			"order_id":             "ord-ret-1",
			"order_code":           "NM-RET-1",
			"checkout_group_id":    "grp-ret-1",
			"buyer_id":             "buyer-ret",
			"shop_id":              "shop-ret",
			"storefront_shop_id":   "shop-ret",
			"status":               tc.status,
			"previous_status":      "PENDING",
			"reason":               tc.reason,
			"return_tracking_code": tc.trackingCode,
			"deadline_at":          "2026-11-26T10:00:00Z",
			"actor_role":           "buyer",
		})
		if _, err := svc.ProcessEvent(context.Background(), "order.return_updated", Envelope{
			EventID:   "evt-ret-" + tc.status,
			EventType: "order.return_updated",
			Payload:   payload,
		}); err != nil {
			t.Fatalf("order.return_updated (%s) failed: %v", tc.status, err)
		}

		newNotifs := repo.notifs[beforeNotifs:]
		newMails := mail.sent[beforeMails:]
		if len(newNotifs) != 2 {
			t.Fatalf("status %s: expected 2 in-app notifications (buyer + seller), got %d", tc.status, len(newNotifs))
		}
		if len(newMails) != 2 {
			t.Fatalf("status %s: expected 2 emails (buyer + seller), got %d", tc.status, len(newMails))
		}

		var buyerNotif, sellerNotif *store.Notification
		for i := range newNotifs {
			switch newNotifs[i].UserID {
			case "buyer-ret":
				buyerNotif = &newNotifs[i]
			case "seller-ret":
				sellerNotif = &newNotifs[i]
			}
		}
		if buyerNotif == nil || buyerNotif.Link != "https://bach-dang.nexusmart.vn/don-hang/ord-ret-1" {
			t.Fatalf("status %s: missing/wrong buyer notification: %+v", tc.status, buyerNotif)
		}
		if sellerNotif == nil || sellerNotif.Link != "/seller/don-hang/ord-ret-1" {
			t.Fatalf("status %s: missing/wrong seller notification: %+v", tc.status, sellerNotif)
		}

		buyerText := strings.ToLower(buyerNotif.Title + " " + buyerNotif.Body)
		sellerText := strings.ToLower(sellerNotif.Title + " " + sellerNotif.Body)
		if !strings.Contains(buyerText, strings.ToLower(tc.wantBuyer)) {
			t.Errorf("status %s: buyer notification %q missing %q", tc.status, buyerText, tc.wantBuyer)
		}
		if !strings.Contains(sellerText, strings.ToLower(tc.wantSeller)) {
			t.Errorf("status %s: seller notification %q missing %q", tc.status, sellerText, tc.wantSeller)
		}

		allText := buyerNotif.Title + "\n" + buyerNotif.Body + "\n" + sellerNotif.Title + "\n" + sellerNotif.Body
		for _, m := range newMails {
			allText += "\n" + m.Subject + "\n" + m.Body
		}
		for _, enum := range rawEnums {
			if strings.Contains(allText, enum) {
				t.Errorf("status %s: output leaked raw enum %q in:\n%s", tc.status, enum, allText)
			}
		}
	}

	if resolver.calledInTx {
		t.Fatalf("UserService must NEVER be called inside Postgres tx")
	}
}

func TestPaymentRefundSettledNotification(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{
		emails: map[string]string{"buyer-ref": "buyer-ref@nexusmart.local"},
	}
	siteResolver := &stubSiteResolver{
		hosts: map[string]string{"shop-1": "bach-dang.nexusmart.vn"},
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSiteLinks(siteResolver, SiteLinkConfig{}))

	// Seed order_participants for single-shop order and multi-shop group.
	repo.participants["ord-ref-1"] = store.OrderParticipant{OrderID: "ord-ref-1", BuyerID: "buyer-ref", ShopID: "shop-1", CheckoutGroupID: "grp-single", StorefrontShopID: "shop-1"}
	repo.participants["ord-m1"] = store.OrderParticipant{OrderID: "ord-m1", BuyerID: "buyer-ref", ShopID: "shop-1", CheckoutGroupID: "grp-multi", StorefrontShopID: "shop-1"}
	repo.participants["ord-m2"] = store.OrderParticipant{OrderID: "ord-m2", BuyerID: "buyer-ref", ShopID: "shop-2", CheckoutGroupID: "grp-multi", StorefrontShopID: "shop-1"}

	payload, _ := json.Marshal(map[string]any{
		"refund_id":  "ref-settled-1",
		"payment_id": "pay-1",
		"order_id":   "ord-ref-1",
		"user_id":    "buyer-ref",
		"amount_vnd": 450000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "payment.refund_settled", Envelope{
		EventID:   "evt-ref-settled-1",
		EventType: "payment.refund_settled",
		Payload:   payload,
	}); err != nil {
		t.Fatalf("payment.refund_settled failed: %v", err)
	}

	notifs, _, total, _ := svc.ListNotifications(context.Background(), "buyer-ref", store.CategoryOrder, false, 1, 20)
	if total != 1 || len(notifs) != 1 {
		t.Fatalf("expected 1 in-app notification, got total=%d len=%d", total, len(notifs))
	}
	if notifs[0].Link != "https://bach-dang.nexusmart.vn/don-hang/ord-ref-1" {
		t.Errorf("link = %q, want https://bach-dang.nexusmart.vn/don-hang/ord-ref-1", notifs[0].Link)
	}
	if !strings.Contains(notifs[0].Body, "450.000₫") || !strings.Contains(notifs[0].Body, "ref-settled-1") {
		t.Errorf("unexpected body: %q", notifs[0].Body)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "buyer-ref@nexusmart.local" || !strings.Contains(mail.sent[0].Subject, "450.000₫") {
		t.Errorf("expected Vietnamese settled refund email with 450.000₫, got %+v", mail.sent)
	}

	// Group-level refund_settled for multi-shop checkout resolves to <site>/don-hang (never /don-hang/<group_id>).
	multiPayload, _ := json.Marshal(map[string]any{
		"refund_id":  "ref-settled-2",
		"payment_id": "pay-multi",
		"order_id":   "grp-multi",
		"user_id":    "buyer-ref",
		"amount_vnd": 750000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "payment.refund_settled", Envelope{
		EventID:   "evt-ref-settled-2",
		EventType: "payment.refund_settled",
		Payload:   multiPayload,
	}); err != nil {
		t.Fatalf("multi payment.refund_settled failed: %v", err)
	}
	if repo.notifs[len(repo.notifs)-1].Link != "https://bach-dang.nexusmart.vn/don-hang" {
		t.Errorf("multi-shop refund_settled link = %q, want https://bach-dang.nexusmart.vn/don-hang", repo.notifs[len(repo.notifs)-1].Link)
	}
}

func TestPasswordResetRequestedEmailAndSecurity(t *testing.T) {
	var logBuf bytes.Buffer
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewJSONHandler(&logBuf, nil)))
	svc.SetWebBaseURL("https://shop.nexusmart.vn/")

	// Even if user disabled system_email, password reset email MUST still be sent.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:      "u-reset-1",
		OrderInApp:  true,
		SystemInApp: true,
		SystemEmail: false,
	})

	secretToken := "reset-tok-secret-9911223344"
	payload, _ := json.Marshal(map[string]any{
		"user_id":     "u-reset-1",
		"email":       "reset@nexusmart.local",
		"full_name":   "Lê Minh Anh",
		"reset_token": secretToken,
		"expires_at":  "2026-11-23T11:00:00Z",
	})
	env := Envelope{
		EventID:   "evt-pw-reset-1",
		EventType: "user.password_reset_requested",
		Payload:   payload,
	}

	// Simulate transient SMTP failure first: token must NOT appear in error or logs.
	mail.err = errors.New("smtp dial timeout")
	_, err := svc.ProcessEvent(context.Background(), "user.password_reset_requested", env)
	if err == nil {
		t.Fatalf("expected error on SMTP failure")
	}
	if strings.Contains(err.Error(), secretToken) || strings.Contains(logBuf.String(), secretToken) {
		t.Fatalf("SECURITY: reset_token leaked into error/logs")
	}

	// Recover SMTP and retry.
	mail.err = nil
	if dup, err := svc.ProcessEvent(context.Background(), "user.password_reset_requested", env); err != nil || dup {
		t.Fatalf("retry failed: dup=%v err=%v", dup, err)
	}

	wantLink := "https://shop.nexusmart.vn/dat-lai-mat-khau?token=" + secretToken
	if len(mail.sent) != 1 {
		t.Fatalf("expected 1 password reset email, got %d", len(mail.sent))
	}
	if mail.sent[0].To != "reset@nexusmart.local" {
		t.Errorf("email To = %q, want reset@nexusmart.local", mail.sent[0].To)
	}
	if strings.Contains(mail.sent[0].Subject, secretToken) {
		t.Errorf("SECURITY: reset_token leaked into email Subject: %q", mail.sent[0].Subject)
	}
	if !strings.Contains(mail.sent[0].Body, wantLink) {
		t.Errorf("password reset email body missing link %q, got %q", wantLink, mail.sent[0].Body)
	}

	// Token MUST NEVER be stored in notifications table (title, body, or link).
	for _, n := range repo.notifs {
		if strings.Contains(n.Title, secretToken) || strings.Contains(n.Body, secretToken) || strings.Contains(n.Link, secretToken) {
			t.Fatalf("SECURITY: reset_token persisted in notifications table: %+v", n)
		}
	}
	if strings.Contains(logBuf.String(), secretToken) {
		t.Fatalf("SECURITY: reset_token leaked into logs: %s", logBuf.String())
	}
}

func TestSellerNotificationsForOrderConfirmedCancelledAndShipments(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-s":  "buyer-s@nexusmart.local",
			"seller-s": "seller-s@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-s": "seller-s",
		},
		shopNames: map[string]string{
			"shop-s": "Gốm Sứ Bát Tràng",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Transient GetShop error MUST return error and NOT write processed_events.
	resolver.shopErr = status.Error(codes.Unavailable, "user service down")
	confirmPayload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-s-1",
		"order_code":        "NM-S-1",
		"buyer_id":          "buyer-s",
		"shop_id":           "shop-s",
		"payment_method":    "COD",
		"total_payable_vnd": 380000,
	})
	confirmEnv := Envelope{EventID: "evt-s-confirm", EventType: "order.confirmed", Payload: confirmPayload}
	if _, err := svc.ProcessEvent(context.Background(), "order.confirmed", confirmEnv); err == nil {
		t.Fatalf("expected error when UserService.GetShop fails transiently")
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-s-confirm"); processed {
		t.Fatalf("failed event must NOT mark processed_events")
	}

	// 2. Recover UserService -> retry succeeds, persists shop_owners in DB, notifies BOTH buyer & seller!
	resolver.shopErr = nil
	if _, err := svc.ProcessEvent(context.Background(), "order.confirmed", confirmEnv); err != nil {
		t.Fatalf("order.confirmed retry failed: %v", err)
	}
	if resolver.getShopCalls != 2 {
		t.Fatalf("expected 2 GetShop calls (1 failed + 1 succeeded), got %d", resolver.getShopCalls)
	}
	if so, ok := repo.shopOwners["shop-s"]; !ok || so.OwnerUserID != "seller-s" {
		t.Fatalf("expected shop_owners cached in DB, got %+v", repo.shopOwners)
	}

	sellerNotifs, _, _, _ := svc.ListNotifications(context.Background(), "seller-s", store.CategoryOrder, false, 1, 20)
	if len(sellerNotifs) != 1 || sellerNotifs[0].Link != "/seller/don-hang/ord-s-1" {
		t.Fatalf("expected 1 seller new-order notification with /seller/don-hang/ord-s-1, got %+v", sellerNotifs)
	}
	if !strings.Contains(sellerNotifs[0].Body, "380.000₫") || strings.Contains(sellerNotifs[0].Body, "COD") {
		t.Errorf("seller new-order body must format VND and localize method, got %q", sellerNotifs[0].Body)
	}
	if len(mail.sent) != 2 {
		t.Fatalf("expected 2 emails (buyer + seller) on order.confirmed, got %d", len(mail.sent))
	}

	// 3. Subsequent events for shop-s MUST use memory cache (zero extra GetShop calls!).
	cancelPayload, _ := json.Marshal(map[string]any{
		"order_id":   "ord-s-1",
		"order_code": "NM-S-1",
		"buyer_id":   "buyer-s",
		"shop_id":    "shop-s",
		"reason":     "seller_confirm_timeout",
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.cancelled", Envelope{
		EventID:   "evt-s-cancel",
		EventType: "order.cancelled",
		Payload:   cancelPayload,
	}); err != nil {
		t.Fatalf("order.cancelled failed: %v", err)
	}
	if resolver.getShopCalls != 2 {
		t.Fatalf("expected GetShop cache hit (still 2 calls), got %d", resolver.getShopCalls)
	}
	sellerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "seller-s", store.CategoryOrder, false, 1, 20)
	if len(sellerNotifs) != 2 || sellerNotifs[1].Link != "/seller/don-hang/ord-s-1" {
		t.Fatalf("expected seller cancel notification, got %+v", sellerNotifs)
	}
	if strings.Contains(sellerNotifs[1].Body, "seller_confirm_timeout") {
		t.Errorf("seller cancel body leaked raw reason enum: %q", sellerNotifs[1].Body)
	}

	// 4. Cold-memory new service instance must hit DB shop_owners cache (still zero extra GetShop calls!).
	svcCold := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Shipment failed, returning, returned -> seller gets in-app + email (all 3 statuses).
	for _, st := range []string{"failed", "returning", "returned"} {
		beforeMails := len(mail.sent)
		beforeSellerNotifs, _, _, _ := svcCold.ListNotifications(context.Background(), "seller-s", store.CategoryOrder, false, 1, 50)
		shipPayload, _ := json.Marshal(map[string]any{
			"shipment_id":   "sh-" + st,
			"order_id":      "ord-s-1",
			"shop_id":       "shop-s",
			"carrier_code":  "ghn",
			"tracking_code": "GHN-S-1",
			"status":        st,
			"direction":     "FORWARD",
			"description":   "Cập nhật từ hãng",
		})
		if _, err := svcCold.ProcessEvent(context.Background(), "shipment.updated", Envelope{
			EventID:   "evt-s-ship-" + st,
			EventType: "shipment.updated",
			Payload:   shipPayload,
		}); err != nil {
			t.Fatalf("shipment.updated (%s) failed: %v", st, err)
		}
		afterSellerNotifs, _, _, _ := svcCold.ListNotifications(context.Background(), "seller-s", store.CategoryOrder, false, 1, 50)
		if len(afterSellerNotifs) != len(beforeSellerNotifs)+1 {
			t.Fatalf("status %s: expected 1 new seller notification, got before=%d after=%d", st, len(beforeSellerNotifs), len(afterSellerNotifs))
		}
		gotSellerNotif := afterSellerNotifs[len(afterSellerNotifs)-1]
		if gotSellerNotif.Link != "/seller/don-hang/ord-s-1" {
			t.Errorf("status %s: seller link = %q, want /seller/don-hang/ord-s-1", st, gotSellerNotif.Link)
		}
		if strings.Contains(strings.ToLower(gotSellerNotif.Title+" "+gotSellerNotif.Body), st) {
			t.Errorf("status %s: seller notification leaked raw enum %q: %+v", st, st, gotSellerNotif)
		}
		var sellerGotEmail bool
		for _, m := range mail.sent[beforeMails:] {
			if m.To == "seller-s@nexusmart.local" {
				sellerGotEmail = true
			}
		}
		if !sellerGotEmail {
			t.Errorf("status %s: expected seller email to be sent", st)
		}
	}
	if resolver.getShopCalls != 2 {
		t.Fatalf("expected DB shop_owners cache hit on cold service (still 2 GetShop calls), got %d", resolver.getShopCalls)
	}

	// 5. Seller turns off order_email (and attempts order_in_app=false): still gets in-app, NO email.
	_, _ = svcCold.UpdatePreferences(context.Background(), store.Preferences{
		UserID:     "seller-s",
		OrderInApp: false,
		OrderEmail: false,
	})
	beforeMails := len(mail.sent)
	beforeSeller, _, _, _ := svcCold.ListNotifications(context.Background(), "seller-s", store.CategoryOrder, false, 1, 50)
	confirm2, _ := json.Marshal(map[string]any{
		"order_id":          "ord-s-2",
		"order_code":        "NM-S-2",
		"buyer_id":          "buyer-s",
		"shop_id":           "shop-s",
		"payment_method":    "VIETQR",
		"total_payable_vnd": 199000,
	})
	if _, err := svcCold.ProcessEvent(context.Background(), "order.confirmed", Envelope{
		EventID:   "evt-s-confirm-2",
		EventType: "order.confirmed",
		Payload:   confirm2,
	}); err != nil {
		t.Fatalf("order.confirmed 2 failed: %v", err)
	}
	afterSeller, _, _, _ := svcCold.ListNotifications(context.Background(), "seller-s", store.CategoryOrder, false, 1, 50)
	if len(afterSeller) != len(beforeSeller)+1 {
		t.Fatalf("UC-B23: seller must still receive in-app order notification when order_in_app=false was requested")
	}
	for _, m := range mail.sent[beforeMails:] {
		if m.To == "seller-s@nexusmart.local" {
			t.Fatalf("seller email must be suppressed when order_email=false, got %+v", m)
		}
	}
	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestShipmentUpdatedBuyerEmailsDedupOutOfOrderAndReturnDirection(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-sh":  "buyer-sh@nexusmart.local",
			"seller-sh": "seller-sh@nexusmart.local",
		},
		shopOwners: map[string]string{"shop-sh": "seller-sh"},
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Out-of-order shipment.updated before order.created MUST return ErrOrderParticipantNotFound and NOT write processed_events.
	oooShip, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-ooo",
		"order_id":      "ord-sh-1",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-SH-1",
		"status":        "delivering",
		"direction":     "FORWARD",
	})
	oooEnv := Envelope{EventID: "evt-sh-ooo", EventType: "shipment.updated", Payload: oooShip}
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", oooEnv); !errors.Is(err, ErrOrderParticipantNotFound) {
		t.Fatalf("expected ErrOrderParticipantNotFound when shipment.updated arrives before order.created, got %v", err)
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-sh-ooo"); processed {
		t.Fatalf("out-of-order shipment.updated MUST NOT write processed_events")
	}

	// Now order.created arrives and populates order_participants.
	ord1, _ := json.Marshal(map[string]any{
		"checkout_group_id": "grp-sh-1",
		"order_id":          "ord-sh-1",
		"order_code":        "NM-SH-1",
		"buyer_id":          "buyer-sh",
		"shop_id":           "shop-sh",
		"payment_method":    "COD",
		"total_payable_vnd": 210000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-ord-sh-1",
		EventType: "order.created",
		Payload:   ord1,
	}); err != nil {
		t.Fatalf("order.created failed: %v", err)
	}

	// Retry evt-sh-ooo (FORWARD delivering) -> succeeds and sends buyer email!
	beforeMails := len(mail.sent)
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", oooEnv); err != nil {
		t.Fatalf("retry of evt-sh-ooo failed: %v", err)
	}
	if len(mail.sent) != beforeMails+1 || mail.sent[len(mail.sent)-1].To != "buyer-sh@nexusmart.local" {
		t.Fatalf("FORWARD delivering must send 1 buyer email, got %+v", mail.sent[beforeMails:])
	}

	// 2. FORWARD failed -> sends email to BOTH buyer AND seller.
	beforeMails = len(mail.sent)
	failShip, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-fail",
		"order_id":      "ord-sh-1",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-SH-1",
		"status":        "failed",
		"direction":     "FORWARD",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-sh-fail",
		EventType: "shipment.updated",
		Payload:   failShip,
	}); err != nil {
		t.Fatalf("FORWARD failed shipment.updated failed: %v", err)
	}
	if len(mail.sent)-beforeMails != 2 {
		t.Fatalf("FORWARD failed must send 2 emails (buyer + seller), got %+v", mail.sent[beforeMails:])
	}

	// 3. Dedup: shipment.updated=delivered followed by order.delivered -> ONLY 1 delivered email!
	beforeMails = len(mail.sent)
	delivShip, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-deliv",
		"order_id":      "ord-sh-1",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-SH-1",
		"status":        "delivered",
		"direction":     "FORWARD",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-sh-deliv-1",
		EventType: "shipment.updated",
		Payload:   delivShip,
	}); err != nil {
		t.Fatalf("FORWARD delivered shipment.updated failed: %v", err)
	}
	ordDeliv, _ := json.Marshal(map[string]any{
		"order_id":      "ord-sh-1",
		"order_code":    "NM-SH-1",
		"buyer_id":      "buyer-sh",
		"shop_id":       "shop-sh",
		"tracking_code": "GHN-SH-1",
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.delivered", Envelope{
		EventID:   "evt-ord-deliv-1",
		EventType: "order.delivered",
		Payload:   ordDeliv,
	}); err != nil {
		t.Fatalf("order.delivered failed: %v", err)
	}
	if got := len(mail.sent) - beforeMails; got != 1 {
		t.Fatalf("shipment.updated=delivered + order.delivered must send ONLY 1 email, got %d: %+v", got, mail.sent[beforeMails:])
	}

	// 4. Reverse order dedup on ord-sh-2: order.delivered FIRST, then shipment.updated=delivered -> STILL ONLY 1 email!
	repo.participants["ord-sh-2"] = store.OrderParticipant{OrderID: "ord-sh-2", BuyerID: "buyer-sh", ShopID: "shop-sh", OrderCode: "NM-SH-2"}
	beforeMails = len(mail.sent)
	ordDeliv2, _ := json.Marshal(map[string]any{
		"order_id":      "ord-sh-2",
		"order_code":    "NM-SH-2",
		"buyer_id":      "buyer-sh",
		"shop_id":       "shop-sh",
		"tracking_code": "GHN-SH-2",
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.delivered", Envelope{
		EventID:   "evt-ord-deliv-2",
		EventType: "order.delivered",
		Payload:   ordDeliv2,
	}); err != nil {
		t.Fatalf("order.delivered 2 failed: %v", err)
	}
	delivShip2, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-deliv-2",
		"order_id":      "ord-sh-2",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-SH-2",
		"status":        "delivered",
		"direction":     "FORWARD",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-sh-deliv-2",
		EventType: "shipment.updated",
		Payload:   delivShip2,
	}); err != nil {
		t.Fatalf("shipment.updated delivered 2 failed: %v", err)
	}
	if got := len(mail.sent) - beforeMails; got != 1 {
		t.Fatalf("order.delivered + shipment.updated=delivered must send ONLY 1 email, got %d: %+v", got, mail.sent[beforeMails:])
	}

	// 5. RETURN direction: NEVER says "đang giao cho bạn" or forward delivery copy!
	beforeNotifs := len(repo.notifs)
	beforeMails = len(mail.sent)
	retShip, _ := json.Marshal(map[string]any{
		"shipment_id":       "sh-ret-1",
		"order_id":          "ord-sh-1",
		"shop_id":           "shop-sh",
		"carrier_code":      "ghn",
		"tracking_code":     "GHN-RET-1",
		"status":            "delivering",
		"direction":         "RETURN",
		"return_request_id": "ret-req-1",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-sh-ret-delivering",
		EventType: "shipment.updated",
		Payload:   retShip,
	}); err != nil {
		t.Fatalf("RETURN delivering shipment.updated failed: %v", err)
	}
	if len(repo.notifs) != beforeNotifs+1 {
		t.Fatalf("expected 1 buyer in-app notification for RETURN delivering, got %d", len(repo.notifs)-beforeNotifs)
	}
	retNotif := repo.notifs[len(repo.notifs)-1]
	lowerRet := strings.ToLower(retNotif.Title + " " + retNotif.Body)
	if strings.Contains(lowerRet, "đang giao cho bạn") || strings.Contains(lowerRet, "đơn hàng đang được giao") {
		t.Errorf("RETURN shipment must NOT say 'đang giao cho bạn', got title=%q body=%q", retNotif.Title, retNotif.Body)
	}
	if !strings.Contains(lowerRet, "trả hàng") && !strings.Contains(lowerRet, "hàng trả") {
		t.Errorf("RETURN shipment must clearly describe return parcel, got title=%q body=%q", retNotif.Title, retNotif.Body)
	}
	for _, m := range mail.sent[beforeMails:] {
		if strings.Contains(strings.ToLower(m.Subject+" "+m.Body), "đang giao cho bạn") {
			t.Errorf("RETURN shipment email must NOT say 'đang giao cho bạn', got %+v", m)
		}
	}

	// 6. FORWARD picking -> buyer in-app only, NO buyer email.
	beforeMails = len(mail.sent)
	pickShip, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-pick-1",
		"order_id":      "ord-sh-1",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-SH-1",
		"status":        "picking",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-sh-pick-1",
		EventType: "shipment.updated",
		Payload:   pickShip,
	}); err != nil {
		t.Fatalf("FORWARD picking failed: %v", err)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("FORWARD picking must NOT send buyer email, got %+v", mail.sent[beforeMails:])
	}

	// 7. RETURN delivered does NOT claim forward delivered email dedup (so forward order.delivered on ord-sh-3 still emails).
	repo.participants["ord-sh-3"] = store.OrderParticipant{OrderID: "ord-sh-3", BuyerID: "buyer-sh", ShopID: "shop-sh", OrderCode: "NM-SH-3"}
	beforeMails = len(mail.sent)
	retDeliv, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-ret-3",
		"order_id":      "ord-sh-3",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-RET-3",
		"status":        "delivered",
		"direction":     "RETURN",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-sh-ret-deliv-3",
		EventType: "shipment.updated",
		Payload:   retDeliv,
	}); err != nil {
		t.Fatalf("RETURN delivered failed: %v", err)
	}
	ordDeliv3, _ := json.Marshal(map[string]any{
		"order_id":      "ord-sh-3",
		"order_code":    "NM-SH-3",
		"buyer_id":      "buyer-sh",
		"shop_id":       "shop-sh",
		"tracking_code": "GHN-FWD-3",
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.delivered", Envelope{
		EventID:   "evt-ord-deliv-3",
		EventType: "order.delivered",
		Payload:   ordDeliv3,
	}); err != nil {
		t.Fatalf("order.delivered 3 failed: %v", err)
	}
	if got := len(mail.sent) - beforeMails; got != 1 {
		t.Fatalf("RETURN delivered must not suppress forward order.delivered email; got %d emails", got)
	}

	// 8. SMTP transient failure during delivered email dedup still delivers exactly 1 email after retry.
	repo.participants["ord-sh-4"] = store.OrderParticipant{OrderID: "ord-sh-4", BuyerID: "buyer-sh", ShopID: "shop-sh", OrderCode: "NM-SH-4"}
	beforeMails = len(mail.sent)
	mail.err = errors.New("smtp temporary failure")
	ship4Payload, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-deliv-4",
		"order_id":      "ord-sh-4",
		"shop_id":       "shop-sh",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-SH-4",
		"status":        "delivered",
		"direction":     "FORWARD",
	})
	ship4Env := Envelope{EventID: "evt-sh-deliv-4", EventType: "shipment.updated", Payload: ship4Payload}
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", ship4Env); err == nil {
		t.Fatalf("expected SMTP error on first attempt")
	}
	mail.err = nil
	ordDeliv4, _ := json.Marshal(map[string]any{
		"order_id":      "ord-sh-4",
		"order_code":    "NM-SH-4",
		"buyer_id":      "buyer-sh",
		"shop_id":       "shop-sh",
		"tracking_code": "GHN-SH-4",
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.delivered", Envelope{
		EventID:   "evt-ord-deliv-4",
		EventType: "order.delivered",
		Payload:   ordDeliv4,
	}); err != nil {
		t.Fatalf("order.delivered 4 failed: %v", err)
	}
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", ship4Env); err != nil {
		t.Fatalf("retry of evt-sh-deliv-4 failed: %v", err)
	}
	if got := len(mail.sent) - beforeMails; got != 1 {
		t.Fatalf("delivered email dedup under SMTP retry must send exactly 1 email, got %d", got)
	}
}

func TestGetShopNotFoundDoesNotBlockBuyerNotification(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{
		emails:  map[string]string{"buyer-nf": "buyer-nf@nexusmart.local"},
		shopErr: status.Error(codes.NotFound, "shop not found"),
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-nf-1",
		"order_code":        "NM-NF-1",
		"buyer_id":          "buyer-nf",
		"shop_id":           "shop-deleted",
		"payment_method":    "COD",
		"total_payable_vnd": 120000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.confirmed", Envelope{
		EventID:   "evt-nf-1",
		EventType: "order.confirmed",
		Payload:   payload,
	}); err != nil {
		t.Fatalf("permanent NotFound on GetShop must not fail buyer notification, got %v", err)
	}
	notifs, _, total, _ := svc.ListNotifications(context.Background(), "buyer-nf", "", false, 1, 20)
	if total != 1 || len(notifs) != 1 {
		t.Fatalf("expected 1 buyer notification even when shop not found, got total=%d", total)
	}
}

func TestUserRegisteredResendOTPSkipsWelcomeInApp(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Initial registration (is_resend omitted -> defaults false): 1 welcome in-app + 1 OTP email + cached contact.
	initPayload, _ := json.Marshal(map[string]any{
		"user_id":    "u-resend-1",
		"email":      "resend1@nexusmart.local",
		"full_name":  "Nguyễn Resend",
		"otp":        "111222",
		"expires_at": "2026-11-23T11:00:00Z",
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.registered", Envelope{
		EventID:   "evt-reg-init-1",
		EventType: "user.registered",
		Payload:   initPayload,
	}); err != nil {
		t.Fatalf("initial user.registered failed: %v", err)
	}
	if _, _, total, _ := svc.ListNotifications(context.Background(), "u-resend-1", "", false, 1, 20); total != 1 {
		t.Fatalf("initial registration must create 1 welcome in-app notification, got %d", total)
	}

	// Turn off system_email to verify resend OTP still forces email delivery.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:      "u-resend-1",
		OrderInApp:  true,
		SystemInApp: true,
		SystemEmail: false,
	})

	// 2. Resend OTP (is_resend: true): sends OTP email (forceEmail) & updates contact cache, NO duplicate welcome in-app!
	resendPayload, _ := json.Marshal(map[string]any{
		"user_id":    "u-resend-1",
		"email":      "resend1-new@nexusmart.local",
		"full_name":  "Nguyễn Resend Mới",
		"otp":        "333444",
		"expires_at": "2026-11-23T11:15:00Z",
		"is_resend":  true,
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.registered", Envelope{
		EventID:   "evt-reg-resend-1",
		EventType: "user.registered",
		Payload:   resendPayload,
	}); err != nil {
		t.Fatalf("resend user.registered failed: %v", err)
	}

	notifs, _, total, _ := svc.ListNotifications(context.Background(), "u-resend-1", "", false, 1, 20)
	if total != 1 || len(notifs) != 1 {
		t.Fatalf("is_resend=true must NOT create duplicate welcome in-app notification, got total=%d notifs=%+v", total, notifs)
	}
	if len(mail.sent) != 2 || mail.sent[1].To != "resend1-new@nexusmart.local" || !strings.Contains(mail.sent[1].Body, "333444") {
		t.Fatalf("is_resend=true must still send OTP email (forceEmail), got %+v", mail.sent)
	}
	contact, ok, _ := repo.GetUserContact(context.Background(), "u-resend-1")
	if !ok || contact.Email != "resend1-new@nexusmart.local" || contact.FullName != "Nguyễn Resend Mới" {
		t.Fatalf("is_resend=true must still cache user contact, got ok=%v contact=%+v", ok, contact)
	}

	// 3. Standalone resend for a user with no prior welcome notification -> 0 in-app, 1 OTP email, contact cached.
	standalonePayload, _ := json.Marshal(map[string]any{
		"user_id":    "u-resend-2",
		"email":      "resend2@nexusmart.local",
		"full_name":  "Trần Resend",
		"otp":        "555666",
		"expires_at": "2026-11-23T11:20:00Z",
		"is_resend":  true,
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.registered", Envelope{
		EventID:   "evt-reg-resend-2",
		EventType: "user.registered",
		Payload:   standalonePayload,
	}); err != nil {
		t.Fatalf("standalone resend user.registered failed: %v", err)
	}
	if _, _, total2, _ := svc.ListNotifications(context.Background(), "u-resend-2", "", false, 1, 20); total2 != 0 {
		t.Fatalf("standalone is_resend=true must create 0 in-app notifications, got %d", total2)
	}
	if len(mail.sent) != 3 || !strings.Contains(mail.sent[2].Body, "555666") {
		t.Fatalf("standalone is_resend=true must send OTP email, got %+v", mail.sent)
	}
	if c2, ok2, _ := repo.GetUserContact(context.Background(), "u-resend-2"); !ok2 || c2.Email != "resend2@nexusmart.local" {
		t.Fatalf("standalone is_resend=true must cache contact, got ok=%v contact=%+v", ok2, c2)
	}
}

type recipientFailMailer struct {
	mu         sync.Mutex
	sent       []mailer.Message
	failFor    map[string]int // recipient email -> remaining failures before succeeding
	txChecker  *atomic.Bool
	calledInTx bool
}

func (m *recipientFailMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.txChecker != nil && m.txChecker.Load() {
		m.calledInTx = true
	}
	if rem := m.failFor[msg.To]; rem > 0 {
		m.failFor[msg.To] = rem - 1
		return errors.New("smtp transient error for second recipient")
	}
	m.sent = append(m.sent, msg)
	return nil
}

func TestTwoRecipientSMTPRetryDoesNotDuplicateFirstRecipientEmail(t *testing.T) {
	repo := newMemRepo()
	mail := &recipientFailMailer{
		failFor:   map[string]int{"seller-2r@nexusmart.local": 2},
		txChecker: &repo.inTx,
	}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-2r":  "buyer-2r@nexusmart.local",
			"seller-2r": "seller-2r@nexusmart.local",
		},
		shopOwners: map[string]string{"shop-2r": "seller-2r"},
		txChecker:  &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-2r-1",
		"order_code":        "NM-2R-1",
		"buyer_id":          "buyer-2r",
		"shop_id":           "shop-2r",
		"payment_method":    "COD",
		"total_payable_vnd": 420000,
	})
	env := Envelope{
		EventID:   "evt-2r-confirm-1",
		EventType: "order.confirmed",
		Payload:   payload,
	}

	// Attempt 1: buyer email succeeds & gets recorded in notification_email_dedups; seller email fails transiently.
	if _, err := svc.ProcessEvent(context.Background(), "order.confirmed", env); err == nil {
		t.Fatalf("attempt 1: expected SMTP error for seller")
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), env.EventID); processed {
		t.Fatalf("attempt 1: processed_events must be cleared for retry")
	}

	// Attempt 2: seller fails again; buyer MUST NOT receive a duplicate email!
	if _, err := svc.ProcessEvent(context.Background(), "order.confirmed", env); err == nil {
		t.Fatalf("attempt 2: expected second SMTP error for seller")
	}

	// Attempt 3: SMTP recovers -> seller receives email; buyer STILL has exactly 1 email total!
	if dup, err := svc.ProcessEvent(context.Background(), "order.confirmed", env); err != nil || dup {
		t.Fatalf("attempt 3: expected clean retry success, got dup=%v err=%v", dup, err)
	}

	buyerMails, sellerMails := 0, 0
	for _, m := range mail.sent {
		switch m.To {
		case "buyer-2r@nexusmart.local":
			buyerMails++
		case "seller-2r@nexusmart.local":
			sellerMails++
		}
	}
	if buyerMails != 1 || sellerMails != 1 {
		t.Fatalf("expected exactly 1 buyer email and 1 seller email across retries, got buyer=%d seller=%d sent=%+v", buyerMails, sellerMails, mail.sent)
	}

	if _, _, bTotal, _ := svc.ListNotifications(context.Background(), "buyer-2r", "", false, 1, 20); bTotal != 1 {
		t.Fatalf("expected 1 buyer in-app notification, got %d", bTotal)
	}
	if _, _, sTotal, _ := svc.ListNotifications(context.Background(), "seller-2r", "", false, 1, 20); sTotal != 1 {
		t.Fatalf("expected 1 seller in-app notification, got %d", sTotal)
	}
	if mail.calledInTx || resolver.calledInTx {
		t.Fatalf("external SMTP/gRPC must never run inside DB tx")
	}
}

func TestPreferenceGuardServerEnforcedUCB23(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Attempting to disable ALL channels -> server MUST force order_in_app=true and system_in_app=true.
	saved, err := svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:         "u-guard",
		OrderInApp:     false,
		OrderEmail:     false,
		OrderPush:      false,
		PromotionInApp: false,
		PromotionEmail: false,
		PromotionPush:  false,
		SystemInApp:    false,
		SystemEmail:    false,
		SystemPush:     false,
	})
	if err != nil {
		t.Fatalf("UpdatePreferences failed: %v", err)
	}
	if !saved.OrderInApp || !saved.SystemInApp {
		t.Fatalf("UC-B23 violated on UpdatePreferences: %+v", saved)
	}

	// 2. Even if stored row had OrderInApp=false & SystemInApp=false, GetPreferences & ProcessEvent MUST enforce true.
	repo.prefs["u-legacy"] = store.Preferences{
		UserID:      "u-legacy",
		OrderInApp:  false,
		OrderEmail:  false,
		SystemInApp: false,
		SystemEmail: false,
	}
	got, err := svc.GetPreferences(context.Background(), "u-legacy")
	if err != nil {
		t.Fatalf("GetPreferences failed: %v", err)
	}
	if !got.OrderInApp || !got.SystemInApp {
		t.Fatalf("UC-B23 violated on GetPreferences: %+v", got)
	}

	// Process an ORDER event and a SYSTEM event -> both MUST still produce in-app notifications!
	ordPayload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-g-1",
		"order_code":        "NM-G-1",
		"buyer_id":          "u-legacy",
		"shop_id":           "shop-1",
		"payment_method":    "COD",
		"total_payable_vnd": 100000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-g-ord",
		EventType: "order.created",
		Payload:   ordPayload,
	}); err != nil {
		t.Fatalf("order.created failed: %v", err)
	}
	shopPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-g",
		"owner_user_id": "u-legacy",
		"shop_name":     "Shop Guard",
		"decision":      "APPROVE",
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_reviewed", Envelope{
		EventID:   "evt-g-sys",
		EventType: "user.shop_reviewed",
		Payload:   shopPayload,
	}); err != nil {
		t.Fatalf("user.shop_reviewed failed: %v", err)
	}

	notifs, _, total, _ := svc.ListNotifications(context.Background(), "u-legacy", "", false, 1, 20)
	if total != 2 || len(notifs) != 2 {
		t.Fatalf("expected both ORDER and SYSTEM in-app notifications despite DB false flags, got total=%d notifs=%+v", total, notifs)
	}
	if len(mail.sent) != 0 {
		t.Fatalf("expected 0 non-security emails when order_email=false and system_email=false, got %+v", mail.sent)
	}
}
