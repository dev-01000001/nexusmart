package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/store"
)

type memRepo struct {
	mu           sync.Mutex
	inTx         atomic.Bool
	notifs       []store.Notification
	prefs        map[string]store.Preferences
	processed    map[string]bool
	contacts     map[string]store.UserContact
	participants map[string]store.OrderParticipant
	dlqCount     int
}

func newMemRepo() *memRepo {
	return &memRepo{
		prefs:        make(map[string]store.Preferences),
		processed:    make(map[string]bool),
		contacts:     make(map[string]store.UserContact),
		participants: make(map[string]store.OrderParticipant),
	}
}

func (m *memRepo) ListNotifications(_ context.Context, userID, category string, unreadOnly bool, limit, offset int32) ([]store.Notification, int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var filtered []store.Notification
	var unread int64
	for _, n := range m.notifs {
		if n.UserID != userID {
			continue
		}
		if !n.IsRead {
			unread++
		}
		if category != "" && n.Category != category {
			continue
		}
		if unreadOnly && n.IsRead {
			continue
		}
		filtered = append(filtered, n)
	}
	total := int64(len(filtered))
	start := int(offset)
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + int(limit)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], unread, total, nil
}

func (m *memRepo) MarkRead(_ context.Context, userID string, ids []string, markAll bool) (int32, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var updated int32
	var unread int64
	for i := range m.notifs {
		if m.notifs[i].UserID != userID {
			continue
		}
		if !m.notifs[i].IsRead && (markAll || idSet[m.notifs[i].ID]) {
			m.notifs[i].IsRead = true
			updated++
		}
		if !m.notifs[i].IsRead {
			unread++
		}
	}
	return updated, unread, nil
}

func (m *memRepo) GetPreferences(_ context.Context, userID string) (store.Preferences, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.prefs[userID]; ok {
		return p, nil
	}
	return store.DefaultPreferences(userID), nil
}

func (m *memRepo) UpsertPreferences(_ context.Context, prefs store.Preferences) (store.Preferences, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prefs[prefs.UserID] = prefs
	return prefs, nil
}

func (m *memRepo) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processed[eventID], nil
}

func (m *memRepo) DeleteProcessedEvent(_ context.Context, eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processed, eventID)
	return nil
}

func (m *memRepo) GetUserContact(_ context.Context, userID string) (store.UserContact, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contacts[userID]
	return c, ok, nil
}

func (m *memRepo) GetOrderParticipant(_ context.Context, orderID string) (store.OrderParticipant, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.participants[orderID]
	return p, ok, nil
}

// ProcessEventTx simulates PostgreSQL transactional rollback on fn error and dedup on event_id.
func (m *memRepo) ProcessEventTx(_ context.Context, eventID, _ string, fn func(tx store.TxStore) error) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.processed[eventID] {
		return true, nil
	}

	m.inTx.Store(true)
	defer m.inTx.Store(false)

	tx := &memTxStore{
		parent:       m,
		stagedNotifs: nil,
		stagedCont:   make(map[string]store.UserContact),
		stagedPart:   make(map[string]store.OrderParticipant),
	}
	if err := fn(tx); err != nil {
		// Rollback: do not mark processed or persist staged mutations.
		return false, err
	}

	m.processed[eventID] = true
	m.notifs = append(m.notifs, tx.stagedNotifs...)
	for k, v := range tx.stagedCont {
		m.contacts[k] = v
	}
	for k, v := range tx.stagedPart {
		m.participants[k] = v
	}
	return false, nil
}

func (m *memRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dlqCount++
	return nil
}

type memTxStore struct {
	parent       *memRepo
	stagedNotifs []store.Notification
	stagedCont   map[string]store.UserContact
	stagedPart   map[string]store.OrderParticipant
}

func (t *memTxStore) InsertNotification(_ context.Context, n store.Notification) (store.Notification, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	t.stagedNotifs = append(t.stagedNotifs, n)
	return n, nil
}

func (t *memTxStore) HasNotificationForEvent(_ context.Context, eventID, userID string) (bool, error) {
	for _, n := range t.parent.notifs {
		if n.EventID == eventID && n.UserID == userID {
			return true, nil
		}
	}
	for _, n := range t.stagedNotifs {
		if n.EventID == eventID && n.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (t *memTxStore) GetPreferences(_ context.Context, userID string) (store.Preferences, error) {
	if p, ok := t.parent.prefs[userID]; ok {
		return p, nil
	}
	return store.DefaultPreferences(userID), nil
}

func (t *memTxStore) UpsertUserContact(_ context.Context, c store.UserContact) error {
	t.stagedCont[c.UserID] = c
	return nil
}

func (t *memTxStore) GetUserContact(_ context.Context, userID string) (store.UserContact, bool, error) {
	if c, ok := t.stagedCont[userID]; ok {
		return c, true, nil
	}
	c, ok := t.parent.contacts[userID]
	return c, ok, nil
}

func (t *memTxStore) UpsertOrderParticipant(_ context.Context, p store.OrderParticipant) error {
	t.stagedPart[p.OrderID] = p
	return nil
}

func (t *memTxStore) GetOrderParticipant(_ context.Context, orderID string) (store.OrderParticipant, bool, error) {
	if p, ok := t.stagedPart[orderID]; ok {
		return p, true, nil
	}
	p, ok := t.parent.participants[orderID]
	return p, ok, nil
}

type recordingMailer struct {
	mu         sync.Mutex
	sent       []mailer.Message
	err        error
	txChecker  *atomic.Bool
	calledInTx bool
}

func (r *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.txChecker != nil && r.txChecker.Load() {
		r.calledInTx = true
	}
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, msg)
	return nil
}

type stubUserResolver struct {
	emails     map[string]string
	txChecker  *atomic.Bool
	calledInTx bool
}

func (s *stubUserResolver) GetUserContact(_ context.Context, userID string) (string, string, error) {
	if s.txChecker != nil && s.txChecker.Load() {
		s.calledInTx = true
	}
	return s.emails[userID], "Resolved User", nil
}

func TestIdempotentEventDeduplicationAndOTP(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"user_id":    "user-1",
		"email":      "buyer@nexusmart.local",
		"full_name":  "Nguyen Buyer",
		"otp":        "739201",
		"expires_at": "2026-11-20T03:15:00Z",
	})
	env := Envelope{
		EventID:    "evt-reg-001",
		EventType:  "user.registered",
		OccurredAt: "2026-11-20T03:00:00Z",
		Payload:    payload,
	}

	// First delivery: creates 1 welcome notification + sends 1 OTP email.
	dup, err := svc.ProcessEvent(context.Background(), "user.registered", env)
	if err != nil || dup {
		t.Fatalf("first ProcessEvent failed: dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 || !strings.Contains(mail.sent[0].Subject, "739201") {
		t.Fatalf("expected 1 OTP email, got %+v", mail.sent)
	}

	// Duplicate redelivery with identical event_id: zero duplicate notifications & zero duplicate emails!
	dup2, err := svc.ProcessEvent(context.Background(), "user.registered", env)
	if err != nil || !dup2 {
		t.Fatalf("expected duplicate=true on replay, got dup=%v err=%v", dup2, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("duplicate event must not resend email, got %d emails", len(mail.sent))
	}

	notifs, unread, total, err := svc.ListNotifications(context.Background(), "user-1", "", false, 1, 20)
	if err != nil || total != 1 || unread != 1 || len(notifs) != 1 {
		t.Fatalf("expected exactly 1 unread notification, got len=%d unread=%d total=%d err=%v", len(notifs), unread, total, err)
	}
}

func TestOrderShipmentUserLookupAndPreferenceGuard(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{emails: map[string]string{
		"buyer-9":  "buyer9@nexusmart.local",
		"seller-2": "seller2@nexusmart.local",
	}}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// UC-B23: attempt to turn off order_in_app & system_in_app -> must remain TRUE, while order_email=false is honored.
	saved, err := svc.UpdatePreferences(context.Background(), store.Preferences{
		UserID:         "buyer-9",
		OrderInApp:     false,
		OrderEmail:     false,
		OrderPush:      false,
		PromotionInApp: false,
		PromotionEmail: false,
		PromotionPush:  false,
		SystemInApp:    false,
		SystemEmail:    true,
		SystemPush:     false,
	})
	if err != nil {
		t.Fatalf("UpdatePreferences failed: %v", err)
	}
	if !saved.OrderInApp || !saved.SystemInApp {
		t.Fatalf("UC-B23 violated: order_in_app and system_in_app must remain true, got %+v", saved)
	}
	if saved.OrderEmail || saved.PromotionInApp {
		t.Fatalf("expected order_email=false and promotion_in_app=false to be saved, got %+v", saved)
	}

	// order.created: creates in-app notification (UC-B23), suppresses order email (order_email=false), caches order_participants.
	orderPayload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-55",
		"order_code":        "NM-2026-0055",
		"buyer_id":          "buyer-9",
		"shop_id":           "shop-2",
		"payment_method":    "COD",
		"total_payable_vnd": 320000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-ord-55",
		EventType: "order.created",
		Payload:   orderPayload,
	}); err != nil {
		t.Fatalf("order.created failed: %v", err)
	}
	if len(mail.sent) != 0 {
		t.Fatalf("expected order email suppressed when order_email=false, got %d", len(mail.sent))
	}

	// shipment.updated (without buyer_id in payload) must resolve buyer-9 from order_participants!
	shipPayload, _ := json.Marshal(map[string]any{
		"shipment_id":   "sh-55",
		"order_id":      "ord-55",
		"shop_id":       "shop-2",
		"carrier_code":  "ghn",
		"tracking_code": "GHN-55",
		"status":        "delivered",
		"description":   "Giao hàng thành công",
	})
	if _, err := svc.ProcessEvent(context.Background(), "shipment.updated", Envelope{
		EventID:   "evt-ship-55",
		EventType: "shipment.updated",
		Payload:   shipPayload,
	}); err != nil {
		t.Fatalf("shipment.updated failed: %v", err)
	}

	// user.shop_reviewed resolves seller-2 email via UserService fallback.
	shopPayload, _ := json.Marshal(map[string]any{
		"shop_id":       "shop-2",
		"owner_user_id": "seller-2",
		"shop_name":     "Tech Store",
		"decision":      "APPROVE",
	})
	if _, err := svc.ProcessEvent(context.Background(), "user.shop_reviewed", Envelope{
		EventID:   "evt-shop-2",
		EventType: "user.shop_reviewed",
		Payload:   shopPayload,
	}); err != nil {
		t.Fatalf("user.shop_reviewed failed: %v", err)
	}
	if len(mail.sent) != 1 || mail.sent[0].To != "seller2@nexusmart.local" {
		t.Fatalf("expected 1 shop review email to seller2, got %+v", mail.sent)
	}

	notifs, unread, total, err := svc.ListNotifications(context.Background(), "buyer-9", store.CategoryOrder, false, 1, 20)
	if err != nil || total != 2 || unread != 2 || len(notifs) != 2 {
		t.Fatalf("expected 2 unread ORDER notifications for buyer-9, got len=%d unread=%d total=%d err=%v", len(notifs), unread, total, err)
	}

	updatedCount, unreadAfter, err := svc.MarkRead(context.Background(), "buyer-9", []string{notifs[0].ID}, false)
	if err != nil || updatedCount != 1 || unreadAfter != 1 {
		t.Fatalf("MarkRead single failed: updated=%d unread=%d err=%v", updatedCount, unreadAfter, err)
	}

	updatedAll, unreadAll, err := svc.MarkRead(context.Background(), "buyer-9", nil, true)
	if err != nil || updatedAll != 1 || unreadAll != 0 {
		t.Fatalf("MarkRead all failed: updated=%d unread=%d err=%v", updatedAll, unreadAll, err)
	}
}

func TestTransientEmailErrorRetriesWithoutDuplicateNotificationOrTxIO(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{err: errors.New("smtp connection refused"), txChecker: &repo.inTx}
	resolver := &stubUserResolver{
		emails:    map[string]string{"user-retry": "retry@nexusmart.local"},
		txChecker: &repo.inTx,
	}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	payload, _ := json.Marshal(map[string]any{
		"order_id":          "ord-retry-1",
		"order_code":        "NM-RETRY-1",
		"buyer_id":          "user-retry",
		"shop_id":           "shop-1",
		"payment_method":    "COD",
		"total_payable_vnd": 150000,
	})
	env := Envelope{EventID: "evt-retry-1", EventType: "order.created", Payload: payload}

	if _, err := svc.ProcessEvent(context.Background(), "order.created", env); err == nil {
		t.Fatalf("expected error when SMTP fails")
	}

	// Once SMTP recovers, replaying the same event_id MUST succeed, send 1 email,
	// keep exactly 1 in-app notification, and NEVER invoke gRPC or SMTP inside a DB tx.
	mail.err = nil
	dup, err := svc.ProcessEvent(context.Background(), "order.created", env)
	if err != nil || dup {
		t.Fatalf("expected clean retry success, got dup=%v err=%v", dup, err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("expected 1 email after retry recovery, got %d", len(mail.sent))
	}
	notifs, _, total, err := svc.ListNotifications(context.Background(), "user-retry", "", false, 1, 20)
	if err != nil || total != 1 || len(notifs) != 1 {
		t.Fatalf("expected exactly 1 in-app notification after SMTP retry, got len=%d total=%d err=%v", len(notifs), total, err)
	}
	if mail.calledInTx {
		t.Fatalf("SMTP mailer.Send must NEVER execute while Postgres tx is open")
	}
	if resolver.calledInTx {
		t.Fatalf("UserService gRPC lookup must NEVER execute while Postgres tx is open")
	}
}
