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

	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestOrderAddressUpdatedNotificationsLabelReprintFeesAndIdempotency(t *testing.T) {
	var logBuf bytes.Buffer
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-addr":    "buyer-addr@nexusmart.local",
			"seller-addr":   "seller-addr@nexusmart.local",
			"seller-addr-2": "seller-addr-2@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-addr":   "seller-addr",
			"shop-addr-2": "seller-addr-2",
		},
		shopNames: map[string]string{
			"shop-addr":   "Cửa Hàng Tràng An",
			"shop-addr-2": "Cửa Hàng Phương Nam",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewJSONHandler(&logBuf, nil)))

	// 1. shipment_recreated=true, fee increase (30.000 -> 45.000), tracking_code present.
	// First simulate transient UserService.GetShop failure -> must return error and NOT mark processed_events.
	recreatedPayload, _ := json.Marshal(map[string]any{
		"order_id":           "ord-addr-1",
		"order_code":         "NM-2026-ADDR1",
		"buyer_id":           "buyer-addr",
		"shop_id":            "shop-addr",
		"old_fee_vnd":        30000,
		"new_fee_vnd":        45000,
		"tracking_code":      "MOCK-NEW-01",
		"shipment_recreated": true,
		"shipment_queued":    false,
		"province_name":      "TP. Hồ Chí Minh",
		"ward_name":          "Phường Bến Nghé",
		// Rogue PII fields that MUST be ignored and never persisted or logged:
		"street_address":  "123 Đường Lê Lợi, Chung cư X",
		"recipient_phone": "0909888777",
		"phone":           "0912345678",
		"recipient_name":  "Nguyễn Thị Bí Mật",
	})
	recreatedEnv := Envelope{
		EventID:   "evt-addr-recreated-1",
		EventType: "order.address_updated",
		Payload:   recreatedPayload,
	}

	resolver.shopErr = status.Error(codes.Unavailable, "user service temporarily unavailable")
	if _, err := svc.ProcessEvent(context.Background(), "order.address_updated", recreatedEnv); err == nil {
		t.Fatalf("expected error on transient UserService.GetShop failure")
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-addr-recreated-1"); processed {
		t.Fatalf("failed order.address_updated must NOT mark processed_events")
	}

	// Recover UserService -> creates 1 seller in-app + 1 buyer in-app + 1 seller email.
	resolver.shopErr = nil
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", recreatedEnv); err != nil || dup {
		t.Fatalf("order.address_updated (recreated) failed: dup=%v err=%v", dup, err)
	}

	sellerNotifs, sellerUnread, sellerTotal, err := svc.ListNotifications(context.Background(), "seller-addr", store.CategoryOrder, false, 1, 20)
	if err != nil || sellerTotal != 1 || sellerUnread != 1 || len(sellerNotifs) != 1 {
		t.Fatalf("expected 1 unread ORDER notification for seller, got len=%d unread=%d total=%d err=%v", len(sellerNotifs), sellerUnread, sellerTotal, err)
	}
	sn := sellerNotifs[0]
	if sn.Link != "/seller/don-hang/ord-addr-1" {
		t.Errorf("seller link = %q, want /seller/don-hang/ord-addr-1", sn.Link)
	}
	snAll := sn.Title + " " + sn.Body
	for _, want := range []string{"NM-2026-ADDR1", "Phường Bến Nghé", "TP. Hồ Chí Minh", "30.000₫", "45.000₫", "+15.000₫", "MOCK-NEW-01", "in lại nhãn"} {
		if !strings.Contains(strings.ToLower(snAll), strings.ToLower(want)) {
			t.Errorf("seller recreated in-app missing %q: title=%q body=%q", want, sn.Title, sn.Body)
		}
	}

	buyerNotifs, buyerUnread, buyerTotal, err := svc.ListNotifications(context.Background(), "buyer-addr", store.CategoryOrder, false, 1, 20)
	if err != nil || buyerTotal != 1 || buyerUnread != 1 || len(buyerNotifs) != 1 {
		t.Fatalf("expected 1 unread ORDER notification for buyer, got len=%d unread=%d total=%d err=%v", len(buyerNotifs), buyerUnread, buyerTotal, err)
	}
	bn := buyerNotifs[0]
	if bn.Link != "/don-hang/ord-addr-1" {
		t.Errorf("buyer link = %q, want /don-hang/ord-addr-1", bn.Link)
	}
	bnAll := bn.Title + " " + bn.Body
	for _, want := range []string{"NM-2026-ADDR1", "Phường Bến Nghé", "TP. Hồ Chí Minh", "30.000₫", "45.000₫", "MOCK-NEW-01"} {
		if !strings.Contains(strings.ToLower(bnAll), strings.ToLower(want)) {
			t.Errorf("buyer recreated in-app missing %q: title=%q body=%q", want, bn.Title, bn.Body)
		}
	}

	// Strictly 1 email (to seller ONLY; buyer gets in-app confirmation only).
	if len(mail.sent) != 1 || mail.sent[0].To != "seller-addr@nexusmart.local" {
		t.Fatalf("expected exactly 1 email to seller-addr, got %+v", mail.sent)
	}
	sm := mail.sent[0]
	smAll := sm.Subject + " " + sm.Body
	for _, want := range []string{"NM-2026-ADDR1", "Phường Bến Nghé", "TP. Hồ Chí Minh", "30.000₫", "45.000₫", "+15.000₫", "MOCK-NEW-01", "in lại nhãn"} {
		if !strings.Contains(strings.ToLower(smAll), strings.ToLower(want)) {
			t.Errorf("seller recreated email missing %q: subject=%q body=%q", want, sm.Subject, sm.Body)
		}
	}

	// PII check: street address, phone, recipient name must NEVER appear anywhere (notifications, email, or logs).
	allRendered := snAll + "\n" + bnAll + "\n" + smAll + "\n" + logBuf.String()
	for _, secret := range []string{"123 Đường Lê Lợi", "Chung cư X", "0909888777", "0912345678", "Nguyễn Thị Bí Mật"} {
		if strings.Contains(allRendered, secret) {
			t.Fatalf("SECURITY: PII %q leaked into notification/email/logs:\n%s", secret, allRendered)
		}
	}

	// Idempotency replay: same event_id returns dup=true and creates 0 extra notifications/emails.
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", recreatedEnv); err != nil || !dup {
		t.Fatalf("expected dup=true on replay of order.address_updated, got dup=%v err=%v", dup, err)
	}
	if _, _, sellerTotal2, _ := svc.ListNotifications(context.Background(), "seller-addr", store.CategoryOrder, false, 1, 20); sellerTotal2 != 1 {
		t.Fatalf("replay duplicated seller notification: got %d", sellerTotal2)
	}
	if _, _, buyerTotal2, _ := svc.ListNotifications(context.Background(), "buyer-addr", store.CategoryOrder, false, 1, 20); buyerTotal2 != 1 {
		t.Fatalf("replay duplicated buyer notification: got %d", buyerTotal2)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("replay duplicated email: got %d", len(mail.sent))
	}

	// 2. shipment_queued=true (shipment_recreated=false), fee decrease (45.000 -> 25.000).
	// Must STILL remind seller to reprint label!
	beforeMails := len(mail.sent)
	queuedPayload, _ := json.Marshal(map[string]any{
		"order_id":           "ord-addr-2",
		"order_code":         "NM-2026-ADDR2",
		"buyer_id":           "buyer-addr",
		"shop_id":            "shop-addr",
		"old_fee_vnd":        45000,
		"new_fee_vnd":        25000,
		"tracking_code":      "",
		"shipment_recreated": false,
		"shipment_queued":    true,
		"province_name":      "Hà Nội",
		"ward_name":          "Phường Ba Đình",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", Envelope{
		EventID:   "evt-addr-queued-1",
		EventType: "order.address_updated",
		Payload:   queuedPayload,
	}); err != nil || dup {
		t.Fatalf("order.address_updated (queued) failed: dup=%v err=%v", dup, err)
	}

	sellerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "seller-addr", store.CategoryOrder, false, 1, 20)
	if len(sellerNotifs) != 2 {
		t.Fatalf("expected 2 seller notifications after queued event, got %d", len(sellerNotifs))
	}
	snQueued := sellerNotifs[1]
	snQueuedAll := strings.ToLower(snQueued.Title + " " + snQueued.Body)
	for _, want := range []string{"nm-2026-addr2", "phường ba đình", "hà nội", "45.000₫", "25.000₫", "-20.000₫", "in lại nhãn"} {
		if !strings.Contains(snQueuedAll, want) {
			t.Errorf("seller queued in-app missing %q: title=%q body=%q", want, snQueued.Title, snQueued.Body)
		}
	}
	if len(mail.sent) != beforeMails+1 || mail.sent[beforeMails].To != "seller-addr@nexusmart.local" {
		t.Fatalf("expected 1 seller email for queued event, got %+v", mail.sent[beforeMails:])
	}
	smQueued := mail.sent[beforeMails]
	if !strings.Contains(strings.ToLower(smQueued.Subject+" "+smQueued.Body), "in lại nhãn") {
		t.Errorf("seller queued email must remind seller to reprint label: %+v", smQueued)
	}
	buyerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "buyer-addr", store.CategoryOrder, false, 1, 20)
	if len(buyerNotifs) != 2 || buyerNotifs[1].Link != "/don-hang/ord-addr-2" {
		t.Fatalf("expected 2 buyer notifications after queued event, got %+v", buyerNotifs)
	}

	// 3. shipment_recreated=false && shipment_queued=false, fee unchanged (30.000 -> 30.000).
	// Must NOT remind seller to reprint label and must NOT mention fee change.
	beforeMails = len(mail.sent)
	noShipPayload, _ := json.Marshal(map[string]any{
		"order_id":           "ord-addr-3",
		"order_code":         "NM-2026-ADDR3",
		"buyer_id":           "buyer-addr",
		"shop_id":            "shop-addr",
		"old_fee_vnd":        30000,
		"new_fee_vnd":        30000,
		"tracking_code":      "",
		"shipment_recreated": false,
		"shipment_queued":    false,
		"province_name":      "Đà Nẵng",
		"ward_name":          "Phường Hải Châu",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", Envelope{
		EventID:   "evt-addr-noship-1",
		EventType: "order.address_updated",
		Payload:   noShipPayload,
	}); err != nil || dup {
		t.Fatalf("order.address_updated (no shipment) failed: dup=%v err=%v", dup, err)
	}

	sellerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "seller-addr", store.CategoryOrder, false, 1, 20)
	if len(sellerNotifs) != 3 {
		t.Fatalf("expected 3 seller notifications after no-shipment event, got %d", len(sellerNotifs))
	}
	snNoShip := sellerNotifs[2]
	snNoShipAll := strings.ToLower(snNoShip.Title + " " + snNoShip.Body)
	if !strings.Contains(snNoShipAll, "phường hải châu") || !strings.Contains(snNoShipAll, "đà nẵng") {
		t.Errorf("seller no-shipment in-app missing area: title=%q body=%q", snNoShip.Title, snNoShip.Body)
	}
	for _, forbidden := range []string{"in lại nhãn", "nhãn vận đơn", "phí vận chuyển", "30.000₫"} {
		if strings.Contains(snNoShipAll, forbidden) {
			t.Errorf("seller no-shipment/unchanged-fee in-app must NOT contain %q: title=%q body=%q", forbidden, snNoShip.Title, snNoShip.Body)
		}
	}
	if len(mail.sent) != beforeMails+1 {
		t.Fatalf("expected 1 seller email for no-shipment event, got %+v", mail.sent[beforeMails:])
	}
	smNoShip := mail.sent[beforeMails]
	smNoShipAll := strings.ToLower(smNoShip.Subject + " " + smNoShip.Body)
	for _, forbidden := range []string{"in lại nhãn", "nhãn vận đơn", "phí vận chuyển", "30.000₫"} {
		if strings.Contains(smNoShipAll, forbidden) {
			t.Errorf("seller no-shipment/unchanged-fee email must NOT contain %q: %+v", forbidden, smNoShip)
		}
	}
	buyerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "buyer-addr", store.CategoryOrder, false, 1, 20)
	if len(buyerNotifs) != 3 || !strings.Contains(buyerNotifs[2].Body, "Phường Hải Châu") {
		t.Fatalf("expected 3rd buyer notification with area, got %+v", buyerNotifs)
	}

	// 4. UC-B23 preferences: seller-addr-2 disables order_email (and attempts order_in_app=false) ->
	// still receives ORDER in-app, 0 email.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:     "seller-addr-2",
		OrderInApp: false,
		OrderEmail: false,
	})
	beforeMails = len(mail.sent)
	prefPayload, _ := json.Marshal(map[string]any{
		"order_id":           "ord-addr-4",
		"order_code":         "NM-2026-ADDR4",
		"buyer_id":           "buyer-addr",
		"shop_id":            "shop-addr-2",
		"old_fee_vnd":        20000,
		"new_fee_vnd":        20000,
		"shipment_recreated": true,
		"shipment_queued":    false,
		"province_name":      "Cần Thơ",
		"ward_name":          "Phường Ninh Kiều",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", Envelope{
		EventID:   "evt-addr-pref-1",
		EventType: "order.address_updated",
		Payload:   prefPayload,
	}); err != nil || dup {
		t.Fatalf("order.address_updated with order_email=false failed: dup=%v err=%v", dup, err)
	}
	if _, _, s2Total, _ := svc.ListNotifications(context.Background(), "seller-addr-2", store.CategoryOrder, false, 1, 20); s2Total != 1 {
		t.Fatalf("UC-B23: seller-addr-2 must still receive ORDER in-app notification, got %d", s2Total)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("expected seller email suppressed when order_email=false, got %+v", mail.sent[beforeMails:])
	}

	// 5. Transient SMTP error -> retry succeeds cleanly with 1 email and no duplicate in-app notifications.
	beforeMails = len(mail.sent)
	beforeSellerNotifs := 3
	beforeBuyerNotifs := 4
	smtpRetryPayload, _ := json.Marshal(map[string]any{
		"order_id":           "ord-addr-5",
		"order_code":         "NM-2026-ADDR5",
		"buyer_id":           "buyer-addr",
		"shop_id":            "shop-addr",
		"old_fee_vnd":        30000,
		"new_fee_vnd":        35000,
		"tracking_code":      "MOCK-RETRY-05",
		"shipment_recreated": true,
		"province_name":      "Hải Phòng",
		"ward_name":          "Phường Hồng Bàng",
	})
	smtpRetryEnv := Envelope{
		EventID:   "evt-addr-smtp-retry-1",
		EventType: "order.address_updated",
		Payload:   smtpRetryPayload,
	}
	mail.err = errors.New("smtp connection reset")
	if _, err := svc.ProcessEvent(context.Background(), "order.address_updated", smtpRetryEnv); err == nil {
		t.Fatalf("expected error on SMTP failure")
	}
	mail.err = nil
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", smtpRetryEnv); err != nil || dup {
		t.Fatalf("SMTP retry failed: dup=%v err=%v", dup, err)
	}
	if _, _, st, _ := svc.ListNotifications(context.Background(), "seller-addr", store.CategoryOrder, false, 1, 20); st != int64(beforeSellerNotifs+1) {
		t.Fatalf("SMTP retry duplicated seller in-app notification: got %d, want %d", st, beforeSellerNotifs+1)
	}
	if _, _, bt, _ := svc.ListNotifications(context.Background(), "buyer-addr", store.CategoryOrder, false, 1, 20); bt != int64(beforeBuyerNotifs+1) {
		t.Fatalf("SMTP retry duplicated buyer in-app notification: got %d, want %d", bt, beforeBuyerNotifs+1)
	}
	if len(mail.sent) != beforeMails+1 {
		t.Fatalf("SMTP retry must send exactly 1 email, got %d", len(mail.sent)-beforeMails)
	}

	// 6. Malformed payloads (missing order_id / shop_id / invalid JSON) -> ErrMalformedPayload, never recorded in processed_events.
	badCases := []struct {
		name string
		raw  []byte
	}{
		{"MissingOrderID", []byte(`{"shop_id":"shop-addr","buyer_id":"buyer-addr","province_name":"Hà Nội"}`)},
		{"BlankOrderID", []byte(`{"order_id":"   ","shop_id":"shop-addr","buyer_id":"buyer-addr"}`)},
		{"MissingShopID", []byte(`{"order_id":"ord-bad-1","buyer_id":"buyer-addr","province_name":"Hà Nội"}`)},
		{"BlankShopID", []byte(`{"order_id":"ord-bad-2","shop_id":"  ","buyer_id":"buyer-addr"}`)},
		{"InvalidJSON", []byte(`{broken-json`)},
	}
	notifsBeforeBad := len(repo.notifs)
	mailsBeforeBad := len(mail.sent)
	for _, tc := range badCases {
		evtID := "evt-addr-bad-" + tc.name
		if _, err := svc.ProcessEvent(context.Background(), "order.address_updated", Envelope{
			EventID:   evtID,
			EventType: "order.address_updated",
			Payload:   tc.raw,
		}); !errors.Is(err, ErrMalformedPayload) {
			t.Fatalf("%s: expected ErrMalformedPayload, got %v", tc.name, err)
		}
		if processed, _ := repo.IsEventProcessed(context.Background(), evtID); processed {
			t.Fatalf("%s: malformed order.address_updated must NOT mark processed_events", tc.name)
		}
	}
	if len(repo.notifs) != notifsBeforeBad || len(mail.sent) != mailsBeforeBad {
		t.Fatalf("malformed payloads must not create notifications or emails")
	}

	// 7. Participant fallback when buyer_id/order_code omitted: enriches from order_participants when cached,
	// or still notifies seller cleanly when buyer_id is absent and order_participants has no row.
	repo.participants["ord-addr-part-1"] = store.OrderParticipant{
		OrderID:   "ord-addr-part-1",
		BuyerID:   "buyer-addr",
		ShopID:    "shop-addr",
		OrderCode: "NM-2026-PART1",
	}
	beforeMails = len(mail.sent)
	partPayload, _ := json.Marshal(map[string]any{
		"order_id":      "ord-addr-part-1",
		"shop_id":       "shop-addr",
		"province_name": "Huế",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", Envelope{
		EventID:   "evt-addr-part-1",
		EventType: "order.address_updated",
		Payload:   partPayload,
	}); err != nil || dup {
		t.Fatalf("order.address_updated participant fallback failed: dup=%v err=%v", dup, err)
	}
	sellerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "seller-addr", store.CategoryOrder, false, 1, 20)
	lastSeller := sellerNotifs[len(sellerNotifs)-1]
	if !strings.Contains(lastSeller.Body, "NM-2026-PART1") || !strings.Contains(lastSeller.Body, "Huế") {
		t.Errorf("participant fallback seller copy mismatch: %+v", lastSeller)
	}
	buyerNotifs, _, _, _ = svc.ListNotifications(context.Background(), "buyer-addr", store.CategoryOrder, false, 1, 20)
	if !strings.Contains(buyerNotifs[len(buyerNotifs)-1].Body, "NM-2026-PART1") {
		t.Errorf("participant fallback buyer copy mismatch: %+v", buyerNotifs[len(buyerNotifs)-1])
	}
	if len(mail.sent) != beforeMails+1 {
		t.Fatalf("expected 1 seller email for participant fallback, got %d", len(mail.sent)-beforeMails)
	}

	// Missing buyer_id without order_participants row: seller still receives notification + email cleanly.
	beforeMails = len(mail.sent)
	minimalPayload, _ := json.Marshal(map[string]any{
		"order_id": "ord-addr-minimal-1",
		"shop_id":  "shop-addr",
	})
	if dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", Envelope{
		EventID:   "evt-addr-minimal-1",
		EventType: "order.address_updated",
		Payload:   minimalPayload,
	}); err != nil || dup {
		t.Fatalf("minimal order.address_updated failed: dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != beforeMails+1 {
		t.Fatalf("expected 1 seller email for minimal payload, got %d", len(mail.sent)-beforeMails)
	}

	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestOrderAddressUpdatedConcurrentIdempotencyRace(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"buyer-race":  "buyer-race@nexusmart.local",
			"seller-race": "seller-race@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-race": "seller-race",
		},
		shopNames: map[string]string{
			"shop-race": "Cửa Hàng Đồng Thời",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"order_id":           "ord-race-1",
		"order_code":         "NM-2026-RACE1",
		"buyer_id":           "buyer-race",
		"shop_id":            "shop-race",
		"old_fee_vnd":        30000,
		"new_fee_vnd":        40000,
		"tracking_code":      "MOCK-RACE-01",
		"shipment_recreated": true,
		"shipment_queued":    false,
		"province_name":      "TP. Hồ Chí Minh",
		"ward_name":          "Phường Tân Định",
	})
	env := Envelope{
		EventID:   "evt-addr-race-same-id",
		EventType: "order.address_updated",
		Payload:   payload,
	}

	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	var firstCount, dupCount, errCount atomic.Int32

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			dup, err := svc.ProcessEvent(context.Background(), "order.address_updated", env)
			if err != nil {
				errCount.Add(1)
				return
			}
			if dup {
				dupCount.Add(1)
			} else {
				firstCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if errCount.Load() != 0 {
		t.Fatalf("unexpected errors in concurrent ProcessEvent: %d", errCount.Load())
	}
	if firstCount.Load() != 1 || dupCount.Load() != workers-1 {
		t.Fatalf("expected exactly 1 primary and %d duplicate results, got primary=%d dup=%d", workers-1, firstCount.Load(), dupCount.Load())
	}
	if _, _, sTotal, _ := svc.ListNotifications(context.Background(), "seller-race", store.CategoryOrder, false, 1, 20); sTotal != 1 {
		t.Fatalf("expected exactly 1 seller notification under concurrent race, got %d", sTotal)
	}
	if _, _, bTotal, _ := svc.ListNotifications(context.Background(), "buyer-race", store.CategoryOrder, false, 1, 20); bTotal != 1 {
		t.Fatalf("expected exactly 1 buyer notification under concurrent race, got %d", bTotal)
	}
	mail.mu.Lock()
	mailCount := len(mail.sent)
	mail.mu.Unlock()
	if mailCount != 1 {
		t.Fatalf("expected exactly 1 seller email under concurrent race, got %d", mailCount)
	}
	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestB1ProductEventsSafelyAcknowledgedWithoutNotifications(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cases := []struct {
		eventID   string
		eventType string
		payload   map[string]any
	}{
		{
			eventID:   "evt-b1-upserted-1",
			eventType: "product.upserted",
			payload: map[string]any{
				"product_id":            "prod-b1-1",
				"shop_id":               "shop-b1-1",
				"category_id":           "cat-1",
				"category_ancestor_ids": []string{"cat-root", "cat-1"},
				"name":                  "Áo Thun Cotton 250GSM",
				"slug":                  "ao-thun-cotton-250gsm",
				"min_price_vnd":         199000,
				"max_price_vnd":         249000,
				"status":                "APPROVED",
				"rating_avg_x100":       480,
				"review_count":          12,
				"shop_suspended":        false,
				"updated_at":            "2026-11-25T02:00:00Z",
			},
		},
		{
			eventID:   "evt-b1-deleted-1",
			eventType: "product.deleted",
			payload: map[string]any{
				"product_id": "prod-b1-1",
				"shop_id":    "shop-b1-1",
				"reason":     "HIDDEN",
				"updated_at": "2026-11-25T02:05:00Z",
			},
		},
		{
			eventID:   "evt-b1-cat-1",
			eventType: "product.category_updated",
			payload: map[string]any{
				"category_id": "cat-1",
				"name":        "Áo Nam",
				"slug":        "ao-nam",
				"parent_id":   "cat-root",
				"sort_order":  1,
				"action":      "UPSERT",
				"updated_at":  "2026-11-25T02:10:00Z",
			},
		},
	}

	for _, tc := range cases {
		raw, _ := json.Marshal(tc.payload)
		env := Envelope{EventID: tc.eventID, EventType: tc.eventType, Payload: raw}
		if dup, err := svc.ProcessEvent(context.Background(), tc.eventType, env); err != nil || dup {
			t.Fatalf("%s should ack safely without error, got dup=%v err=%v", tc.eventType, dup, err)
		}
		if dup, err := svc.ProcessEvent(context.Background(), tc.eventType, env); err != nil || !dup {
			t.Fatalf("replayed %s should return dup=true, got dup=%v err=%v", tc.eventType, dup, err)
		}
	}
	if len(repo.notifs) != 0 || len(mail.sent) != 0 {
		t.Fatalf("B1 product events must create 0 notifications and 0 emails, got notifs=%d mails=%d", len(repo.notifs), len(mail.sent))
	}
}
