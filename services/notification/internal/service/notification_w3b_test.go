package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPhoneOTPRequestedEmailOnlyAndSecurity(t *testing.T) {
	var logBuf bytes.Buffer
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewJSONHandler(&logBuf, nil)))

	// Even when user disabled system_email, phone OTP SMS-mock email MUST still send.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:      "buyer-phone-1",
		SystemInApp: true,
		SystemEmail: false,
	})

	secretOTP := "583921"
	payload, _ := json.Marshal(map[string]any{
		"user_id":      "buyer-phone-1",
		"email":        "phone-buyer@nexusmart.local",
		"full_name":    "Lê Hoàng Nam",
		"phone_masked": "090****789",
		"otp":          secretOTP,
		"expires_at":   "2026-11-24T11:05:00Z",
	})
	env := Envelope{
		EventID:   "evt-phone-otp-1",
		EventType: "user.phone_otp_requested",
		Payload:   payload,
	}

	// 1. Transient SMTP failure: OTP must NOT leak into error or JSON logs, and must return error for retry.
	mail.err = errors.New("smtp connection refused")
	if _, err := svc.ProcessEvent(context.Background(), "user.phone_otp_requested", env); err == nil {
		t.Fatalf("expected error on SMTP failure for user.phone_otp_requested")
	} else if strings.Contains(err.Error(), secretOTP) {
		t.Fatalf("SECURITY: phone OTP leaked into error: %v", err)
	}
	if strings.Contains(logBuf.String(), secretOTP) {
		t.Fatalf("SECURITY: phone OTP leaked into logs on SMTP failure: %s", logBuf.String())
	}

	// 2. Recover SMTP -> retry delivers 1 SMS-mock OTP email, 0 in-app notifications, caches contact.
	mail.err = nil
	if dup, err := svc.ProcessEvent(context.Background(), "user.phone_otp_requested", env); err != nil || dup {
		t.Fatalf("retry user.phone_otp_requested failed: dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("expected 1 phone OTP email, got %d", len(mail.sent))
	}
	sent := mail.sent[0]
	if sent.To != "phone-buyer@nexusmart.local" {
		t.Errorf("email To = %q, want phone-buyer@nexusmart.local", sent.To)
	}
	sixDigitsRe := regexp.MustCompile(`\d{6}`)
	if sixDigitsRe.MatchString(sent.Subject) || strings.Contains(sent.Subject, secretOTP) {
		t.Fatalf("SECURITY: phone OTP leaked into email Subject: %q", sent.Subject)
	}
	e2eOTPRe := regexp.MustCompile(`(?i)(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b`)
	m := e2eOTPRe.FindStringSubmatch(sent.Body)
	if len(m) != 2 || m[1] != secretOTP {
		t.Errorf("phone OTP email body must allow E2E regex extraction of %s, got %q", secretOTP, sent.Body)
	}
	if !strings.Contains(sent.Body, "090****789") || !strings.Contains(sent.Body, "Lê Hoàng Nam") || !strings.Contains(sent.Body, "18:05 ngày 24/11/2026 (GMT+7)") {
		t.Errorf("phone OTP email body missing expected greeting/phone_masked/expiry: %q", sent.Body)
	}

	// Zero in-app notifications (OTP must NEVER appear in notifications table).
	notifs, unread, total, _ := svc.ListNotifications(context.Background(), "buyer-phone-1", "", false, 1, 20)
	if total != 0 || unread != 0 || len(notifs) != 0 {
		t.Fatalf("user.phone_otp_requested must be email-only (0 in-app notifications), got total=%d notifs=%+v", total, notifs)
	}
	for _, n := range repo.notifs {
		if strings.Contains(n.Title+n.Body+n.Link, secretOTP) {
			t.Fatalf("SECURITY: phone OTP persisted in notifications table: %+v", n)
		}
	}
	if strings.Contains(logBuf.String(), secretOTP) {
		t.Fatalf("SECURITY: phone OTP leaked into logs: %s", logBuf.String())
	}
	if c, ok, _ := repo.GetUserContact(context.Background(), "buyer-phone-1"); !ok || c.Email != "phone-buyer@nexusmart.local" {
		t.Errorf("expected contact cached, got ok=%v c=%+v", ok, c)
	}

	// 3. Duplicate replay of same event_id does not resend email.
	if dup, err := svc.ProcessEvent(context.Background(), "user.phone_otp_requested", env); err != nil || !dup {
		t.Fatalf("expected duplicate=true on replay, got dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("duplicate event must not resend phone OTP email, got %d", len(mail.sent))
	}

	// 4. Malformed payloads (missing otp, missing email, missing user_id) return ErrMalformedPayload.
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"MissingOTP", map[string]any{"user_id": "buyer-phone-1", "email": "phone-buyer@nexusmart.local"}},
		{"MissingEmail", map[string]any{"user_id": "buyer-phone-1", "otp": "123456"}},
		{"MissingUserID", map[string]any{"email": "phone-buyer@nexusmart.local", "otp": "123456"}},
	} {
		badPayload, _ := json.Marshal(tc.body)
		if _, err := svc.ProcessEvent(context.Background(), "user.phone_otp_requested", Envelope{
			EventID:   "evt-phone-otp-bad-" + tc.name,
			EventType: "user.phone_otp_requested",
			Payload:   badPayload,
		}); !errors.Is(err, ErrMalformedPayload) {
			t.Fatalf("%s: expected ErrMalformedPayload, got %v", tc.name, err)
		}
	}
	if mail.calledInTx {
		t.Fatalf("SMTP must never execute inside DB tx")
	}
}

func TestShipmentCreateFailedNotifiesSellerAndDedup(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails: map[string]string{
			"seller-ship": "seller-ship@nexusmart.local",
			"buyer-ship":  "buyer-ship@nexusmart.local",
		},
		shopOwners: map[string]string{
			"shop-ship": "seller-ship",
		},
		shopNames: map[string]string{
			"shop-ship": "Cửa Hàng Gió Mới",
		},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Seed order_participants so order_code is enriched in notification copy.
	repo.participants["ord-sf-1"] = store.OrderParticipant{
		OrderID:   "ord-sf-1",
		BuyerID:   "buyer-ship",
		ShopID:    "shop-ship",
		OrderCode: "NM-2026-SF01",
	}

	failPayload, _ := json.Marshal(map[string]any{
		"job_id":        "job-sf-1",
		"order_id":      "ord-sf-1",
		"shop_id":       "shop-ship",
		"carrier_code":  "GHN",
		"service_code":  "STANDARD",
		"attempts":      8,
		"error_message": "carrier returned HTTP 503",
		"occurred_at":   "2026-11-24T10:00:00Z",
	})
	failEnv := Envelope{
		EventID:   "evt-sf-1",
		EventType: "shipment.create_failed",
		Payload:   failPayload,
	}

	// 1. Transient GetShop failure must NOT record processed_events and must return error for retry.
	resolver.shopErr = status.Error(codes.Unavailable, "user service down")
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", failEnv); err == nil {
		t.Fatalf("expected error on transient UserService.GetShop failure")
	}
	if processed, _ := repo.IsEventProcessed(context.Background(), "evt-sf-1"); processed {
		t.Fatalf("failed shipment.create_failed must NOT mark processed_events")
	}

	// 2. Recover UserService -> delivers 1 ORDER in-app + 1 email to seller ONLY (0 to buyer).
	resolver.shopErr = nil
	if dup, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", failEnv); err != nil || dup {
		t.Fatalf("retry shipment.create_failed failed: dup=%v err=%v", dup, err)
	}

	sellerNotifs, unread, total, err := svc.ListNotifications(context.Background(), "seller-ship", store.CategoryOrder, false, 1, 20)
	if err != nil || total != 1 || unread != 1 || len(sellerNotifs) != 1 {
		t.Fatalf("expected 1 unread ORDER notification for seller, got len=%d unread=%d total=%d err=%v", len(sellerNotifs), unread, total, err)
	}
	sn := sellerNotifs[0]
	if sn.Link != "/seller/don-hang/ord-sf-1" {
		t.Errorf("seller link = %q, want /seller/don-hang/ord-sf-1", sn.Link)
	}
	if !strings.Contains(sn.Title+" "+sn.Body, "NM-2026-SF01") || !strings.Contains(sn.Body, "Giao Hàng Nhanh") || !strings.Contains(sn.Body, "8") {
		t.Errorf("seller notification missing order code, carrier label, or attempts: title=%q body=%q", sn.Title, sn.Body)
	}
	if strings.Contains(sn.Title+"\n"+sn.Body, "FAILED") {
		t.Errorf("seller notification leaked raw enum FAILED: title=%q body=%q", sn.Title, sn.Body)
	}
	if strings.Contains(sn.Title+"\n"+sn.Body, "carrier returned HTTP 503") || strings.Contains(sn.Body, "HTTP 503") {
		t.Errorf("seller notification must not expose raw technical carrier error_message: title=%q body=%q", sn.Title, sn.Body)
	}
	if _, _, buyerTotal, _ := svc.ListNotifications(context.Background(), "buyer-ship", "", false, 1, 20); buyerTotal != 0 {
		t.Fatalf("shipment.create_failed must notify seller only, got %d buyer notifications", buyerTotal)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "seller-ship@nexusmart.local" {
		t.Fatalf("expected 1 email to seller-ship, got %+v", mail.sent)
	}
	if strings.Contains(mail.sent[0].Subject+"\n"+mail.sent[0].Body, "FAILED") || !strings.Contains(mail.sent[0].Subject, "NM-2026-SF01") {
		t.Errorf("unexpected seller email subject/body: %+v", mail.sent[0])
	}
	if strings.Contains(mail.sent[0].Subject+"\n"+mail.sent[0].Body, "carrier returned HTTP 503") || strings.Contains(mail.sent[0].Body, "HTTP 503") {
		t.Errorf("seller email must not expose raw technical carrier error_message: %+v", mail.sent[0])
	}

	// 3. Duplicate event with DIFFERENT event_id for the SAME job_id -> deduped (0 extra in-app, 0 extra email).
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", Envelope{
		EventID:   "evt-sf-1-dup-job",
		EventType: "shipment.create_failed",
		Payload:   failPayload,
	}); err != nil {
		t.Fatalf("duplicate job_id event failed: %v", err)
	}
	if _, _, totalAfterDup, _ := svc.ListNotifications(context.Background(), "seller-ship", store.CategoryOrder, false, 1, 20); totalAfterDup != 1 {
		t.Fatalf("same job_id with new event_id must dedup in-app notification, got %d", totalAfterDup)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("same job_id with new event_id must dedup email, got %d", len(mail.sent))
	}

	// 4. Seller retries creating shipment (NEW job_id) and that also fails -> notifies seller again!
	retryFailPayload, _ := json.Marshal(map[string]any{
		"job_id":        "job-sf-2",
		"order_id":      "ord-sf-1",
		"shop_id":       "shop-ship",
		"carrier_code":  "MOCK_EXPRESS",
		"attempts":      8,
		"error_message": "timeout contacting carrier",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", Envelope{
		EventID:   "evt-sf-2",
		EventType: "shipment.create_failed",
		Payload:   retryFailPayload,
	}); err != nil {
		t.Fatalf("second job failure failed: %v", err)
	}
	if _, _, total2, _ := svc.ListNotifications(context.Background(), "seller-ship", store.CategoryOrder, false, 1, 20); total2 != 2 {
		t.Fatalf("new retry job_id must notify seller again, got total=%d", total2)
	}
	if len(mail.sent) != 2 {
		t.Fatalf("new retry job_id must send second email, got %d", len(mail.sent))
	}

	// 5. Missing shop_id in payload falls back to order_participants; if absent there too, returns ErrOrderParticipantNotFound.
	noShopKnownOrder, _ := json.Marshal(map[string]any{
		"job_id":   "job-sf-3",
		"order_id": "ord-sf-1",
		"attempts": 8,
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", Envelope{
		EventID:   "evt-sf-3",
		EventType: "shipment.create_failed",
		Payload:   noShopKnownOrder,
	}); err != nil {
		t.Fatalf("shipment.create_failed missing shop_id should resolve from order_participants, got %v", err)
	}
	if _, _, total3, _ := svc.ListNotifications(context.Background(), "seller-ship", store.CategoryOrder, false, 1, 20); total3 != 3 {
		t.Fatalf("expected 3rd notification resolved via order_participants, got %d", total3)
	}

	noShopUnknownOrder, _ := json.Marshal(map[string]any{
		"job_id":   "job-sf-unknown",
		"order_id": "ord-not-cached-yet",
		"attempts": 8,
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", Envelope{
		EventID:   "evt-sf-unknown",
		EventType: "shipment.create_failed",
		Payload:   noShopUnknownOrder,
	}); !errors.Is(err, ErrOrderParticipantNotFound) {
		t.Fatalf("expected ErrOrderParticipantNotFound when both payload shop_id and order_participants are missing, got %v", err)
	}

	// 6. Seller disables order_email (and attempts order_in_app=false): still gets in-app (UC-B23), email suppressed.
	_, _ = svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:     "seller-ship",
		OrderInApp: false,
		OrderEmail: false,
	})
	beforeMails := len(mail.sent)
	prefPayload, _ := json.Marshal(map[string]any{
		"job_id":   "job-sf-4",
		"order_id": "ord-sf-1",
		"shop_id":  "shop-ship",
		"attempts": 8,
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", Envelope{
		EventID:   "evt-sf-4",
		EventType: "shipment.create_failed",
		Payload:   prefPayload,
	}); err != nil {
		t.Fatalf("shipment.create_failed with order_email=false failed: %v", err)
	}
	if _, _, total4, _ := svc.ListNotifications(context.Background(), "seller-ship", store.CategoryOrder, false, 1, 20); total4 != 4 {
		t.Fatalf("UC-B23: seller must still get ORDER in-app notification, got %d", total4)
	}
	if len(mail.sent) != beforeMails {
		t.Fatalf("expected seller email suppressed when order_email=false, got %+v", mail.sent[beforeMails:])
	}

	// 7. Malformed payload (missing order_id) returns ErrMalformedPayload.
	badPayload, _ := json.Marshal(map[string]any{"job_id": "job-bad", "shop_id": "shop-ship"})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.create_failed", Envelope{
		EventID:   "evt-sf-bad",
		EventType: "shipment.create_failed",
		Payload:   badPayload,
	}); !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("expected ErrMalformedPayload for missing order_id, got %v", err)
	}
	if resolver.calledInTx || mail.calledInTx {
		t.Fatalf("external gRPC/SMTP I/O must never run inside DB tx")
	}
}

func TestReviewCreatedSafelyAcknowledgedWithoutNotifications(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"review_id":       "rev-1",
		"product_id":      "prod-1",
		"shop_id":         "shop-1",
		"order_id":        "ord-1",
		"order_item_id":   "item-1",
		"buyer_id":        "buyer-1",
		"rating":          5,
		"review_count":    1,
		"rating_avg_x100": 500,
		"occurred_at":     "2026-11-24T10:00:00Z",
	})
	env := Envelope{EventID: "evt-rev-1", EventType: "review.created", Payload: payload}
	if dup, err := svc.ProcessEvent(context.Background(), "review.created", env); err != nil || dup {
		t.Fatalf("review.created should ack safely without error, got dup=%v err=%v", dup, err)
	}
	if len(repo.notifs) != 0 || len(mail.sent) != 0 {
		t.Fatalf("review.created should create 0 notifications and 0 emails, got notifs=%d mails=%d", len(repo.notifs), len(mail.sent))
	}
	if dup, err := svc.ProcessEvent(context.Background(), "review.created", env); err != nil || !dup {
		t.Fatalf("replayed review.created should report duplicate=true, got dup=%v err=%v", dup, err)
	}
}

type ctxCheckedRepo struct {
	*memRepo
	deleteProcessedCtxErr error
}

func (r *ctxCheckedRepo) DeleteProcessedEvent(ctx context.Context, eventID string) error {
	r.deleteProcessedCtxErr = ctx.Err()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.memRepo.DeleteProcessedEvent(ctx, eventID)
}

type cancelOnSendMailer struct {
	cancel context.CancelFunc
	sent   []mailer.Message
}

func (m *cancelOnSendMailer) Send(ctx context.Context, msg mailer.Message) error {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
		return ctx.Err()
	}
	m.sent = append(m.sent, msg)
	return nil
}

func TestSMTPFailureUnderCanceledContextClearsProcessedEventViaDetachedCtx(t *testing.T) {
	baseRepo := newMemRepo()
	repo := &ctxCheckedRepo{memRepo: baseRepo}
	ctx, cancel := context.WithCancel(context.Background())
	mail := &cancelOnSendMailer{cancel: cancel}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"user_id":      "buyer-cancel-1",
		"email":        "cancel@nexusmart.local",
		"full_name":    "Test Cancel",
		"phone_masked": "091****222",
		"otp":          "112233",
		"expires_at":   "2026-11-24T12:00:00Z",
	})
	env := Envelope{
		EventID:   "evt-smtp-cancel-1",
		EventType: "user.phone_otp_requested",
		Payload:   payload,
	}

	if _, err := svc.ProcessEvent(ctx, "user.phone_otp_requested", env); err == nil {
		t.Fatalf("expected error when SMTP fails due to context cancellation")
	}
	if repo.deleteProcessedCtxErr != nil {
		t.Fatalf("DeleteProcessedEvent must use detached context (err=nil), got %v", repo.deleteProcessedCtxErr)
	}
	if processed, _ := baseRepo.IsEventProcessed(context.Background(), "evt-smtp-cancel-1"); processed {
		t.Fatalf("processed_events must be cleared even when SMTP failed under canceled context")
	}

	// Retry after restart with live context must send the email once!
	if dup, err := svc.ProcessEvent(context.Background(), "user.phone_otp_requested", env); err != nil || dup {
		t.Fatalf("retry after cancel failed: dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("expected 1 email sent on retry, got %d", len(mail.sent))
	}
}
