package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"sort"
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
	shopOwners   map[string]store.ShopOwner
	emailDedups  map[string]string
	dlqCount     int
}

func newMemRepo() *memRepo {
	return &memRepo{
		prefs:        make(map[string]store.Preferences),
		processed:    make(map[string]bool),
		contacts:     make(map[string]store.UserContact),
		participants: make(map[string]store.OrderParticipant),
		shopOwners:   make(map[string]store.ShopOwner),
		emailDedups:  make(map[string]string),
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

func (m *memRepo) ListOrderParticipantsByGroup(_ context.Context, checkoutGroupID string) ([]store.OrderParticipant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.OrderParticipant
	for _, p := range m.participants {
		if checkoutGroupID != "" && p.CheckoutGroupID == checkoutGroupID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrderID < out[j].OrderID })
	return out, nil
}

func (m *memRepo) GetShopOwner(_ context.Context, shopID string) (store.ShopOwner, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	so, ok := m.shopOwners[shopID]
	return so, ok, nil
}

func (m *memRepo) RecordEmailDedup(_ context.Context, dedupKey, eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.emailDedups[dedupKey]; !exists {
		m.emailDedups[dedupKey] = eventID
	}
	return nil
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
		stagedShops:  make(map[string]store.ShopOwner),
		stagedDedups: make(map[string]string),
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
		existing := m.participants[k]
		if v.BuyerID == "" {
			v.BuyerID = existing.BuyerID
		}
		if v.ShopID == "" {
			v.ShopID = existing.ShopID
		}
		if v.OrderCode == "" {
			v.OrderCode = existing.OrderCode
		}
		if v.CheckoutGroupID == "" {
			v.CheckoutGroupID = existing.CheckoutGroupID
		}
		m.participants[k] = v
	}
	for k, v := range tx.stagedShops {
		existing := m.shopOwners[k]
		if v.OwnerUserID == "" {
			v.OwnerUserID = existing.OwnerUserID
		}
		if v.ShopName == "" {
			v.ShopName = existing.ShopName
		}
		m.shopOwners[k] = v
	}
	for k, v := range tx.stagedDedups {
		if _, exists := m.emailDedups[k]; !exists {
			m.emailDedups[k] = v
		}
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
	stagedShops  map[string]store.ShopOwner
	stagedDedups map[string]string
}

func (t *memTxStore) InsertNotification(_ context.Context, n store.Notification) (store.Notification, error) {
	if n.DedupKey != "" {
		for _, existing := range t.parent.notifs {
			if existing.DedupKey == n.DedupKey {
				return store.Notification{}, nil
			}
		}
		for _, existing := range t.stagedNotifs {
			if existing.DedupKey == n.DedupKey {
				return store.Notification{}, nil
			}
		}
	}
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

func (t *memTxStore) ListOrderParticipantsByGroup(_ context.Context, checkoutGroupID string) ([]store.OrderParticipant, error) {
	if checkoutGroupID == "" {
		return nil, nil
	}
	merged := make(map[string]store.OrderParticipant, len(t.parent.participants)+len(t.stagedPart))
	for k, v := range t.parent.participants {
		merged[k] = v
	}
	for k, v := range t.stagedPart {
		if existing, ok := merged[k]; ok && v.CheckoutGroupID == "" {
			v.CheckoutGroupID = existing.CheckoutGroupID
		}
		merged[k] = v
	}
	var out []store.OrderParticipant
	for _, p := range merged {
		if p.CheckoutGroupID == checkoutGroupID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrderID < out[j].OrderID })
	return out, nil
}

func (t *memTxStore) UpsertShopOwner(_ context.Context, s store.ShopOwner) error {
	if s.ShopID == "" || s.OwnerUserID == "" {
		return nil
	}
	t.stagedShops[s.ShopID] = s
	return nil
}

func (t *memTxStore) GetShopOwner(_ context.Context, shopID string) (store.ShopOwner, bool, error) {
	if s, ok := t.stagedShops[shopID]; ok {
		return s, true, nil
	}
	s, ok := t.parent.shopOwners[shopID]
	return s, ok, nil
}

func (t *memTxStore) ClaimEmailDedup(_ context.Context, dedupKey, eventID string) (string, error) {
	if existing, ok := t.parent.emailDedups[dedupKey]; ok {
		return existing, nil
	}
	if existing, ok := t.stagedDedups[dedupKey]; ok {
		return existing, nil
	}
	t.stagedDedups[dedupKey] = eventID
	return eventID, nil
}

func (t *memTxStore) HasEmailDedup(_ context.Context, dedupKey string) (bool, error) {
	if _, ok := t.stagedDedups[dedupKey]; ok {
		return true, nil
	}
	_, ok := t.parent.emailDedups[dedupKey]
	return ok, nil
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
	mu           sync.Mutex
	emails       map[string]string
	shopOwners   map[string]string
	shopNames    map[string]string
	shopErr      error
	userErr      error
	getShopCalls int
	getUserCalls int
	txChecker    *atomic.Bool
	calledInTx   bool
}

func (s *stubUserResolver) GetUserContact(_ context.Context, userID string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getUserCalls++
	if s.txChecker != nil && s.txChecker.Load() {
		s.calledInTx = true
	}
	if s.userErr != nil {
		return "", "", s.userErr
	}
	return s.emails[userID], "Resolved User", nil
}

func (s *stubUserResolver) GetShopOwner(_ context.Context, shopID string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getShopCalls++
	if s.txChecker != nil && s.txChecker.Load() {
		s.calledInTx = true
	}
	if s.shopErr != nil {
		return "", "", s.shopErr
	}
	return s.shopOwners[shopID], s.shopNames[shopID], nil
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
	if len(mail.sent) != 1 || strings.Contains(mail.sent[0].Subject, "739201") || !strings.Contains(mail.sent[0].Body, "739201") {
		t.Fatalf("expected 1 OTP email with code in body only (not subject), got %+v", mail.sent)
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

func TestCODVsVietQRNotificationContentAndSubjects(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{emails: map[string]string{
		"buyer-cod":    "buyer-cod@nexusmart.local",
		"buyer-vietqr": "buyer-vietqr@nexusmart.local",
	}}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. COD order.created: must say "Đặt hàng thành công, thanh toán khi nhận hàng", formatted VND, no raw COD/VND.
	codOrderPayload, _ := json.Marshal(map[string]any{
		"checkout_group_id": "grp-cod-1",
		"order_id":          "ord-cod-1",
		"order_code":        "NM-20261123-COD1",
		"buyer_id":          "buyer-cod",
		"shop_id":           "shop-1",
		"payment_method":    "COD",
		"total_payable_vnd": 256500,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-cod-created-1",
		EventType: "order.created",
		Payload:   codOrderPayload,
	}); err != nil {
		t.Fatalf("COD order.created failed: %v", err)
	}

	codNotifs, _, _, _ := svc.ListNotifications(context.Background(), "buyer-cod", "", false, 1, 20)
	if len(codNotifs) != 1 {
		t.Fatalf("expected 1 COD notification, got %d", len(codNotifs))
	}
	codText := codNotifs[0].Title + " | " + codNotifs[0].Body
	if !strings.Contains(codText, "Đặt hàng thành công, thanh toán khi nhận hàng") {
		t.Errorf("PRM-B5: COD order.created must announce COD order placement cleanly, got %q", codText)
	}
	if strings.Contains(codText, "qua COD thành công") || strings.Contains(codText, "256500 VND") {
		t.Errorf("PRM-B5/B7: COD order.created must not use raw enum/money, got %q", codText)
	}
	if !strings.Contains(codNotifs[0].Body, "256.500₫") {
		t.Errorf("PRM-B7: expected formatted currency 256.500₫ in COD body, got %q", codNotifs[0].Body)
	}
	if len(mail.sent) != 1 || strings.Contains(mail.sent[0].Subject, "Order Created") {
		t.Errorf("PRM-B7: COD order.created email subject must be Vietnamese, got %+v", mail.sent)
	}

	// 2. COD payment.succeeded (emitted after delivery & cash collection): must announce cash collected.
	codPayPayload, _ := json.Marshal(map[string]any{
		"payment_id":     "pay-cod-1",
		"order_group_id": "grp-cod-1",
		"order_id":       "ord-cod-1",
		"user_id":        "buyer-cod",
		"amount_vnd":     256500,
		"method":         "COD",
		"provider_tx_id": "COD-TX-001",
	})
	if _, err := svc.ProcessEvent(context.Background(), "payment.succeeded", Envelope{
		EventID:   "evt-cod-paid-1",
		EventType: "payment.succeeded",
		Payload:   codPayPayload,
	}); err != nil {
		t.Fatalf("COD payment.succeeded failed: %v", err)
	}

	codNotifs, _, _, _ = svc.ListNotifications(context.Background(), "buyer-cod", "", false, 1, 20)
	if len(codNotifs) != 2 {
		t.Fatalf("expected 2 COD notifications, got %d", len(codNotifs))
	}
	codPaid := codNotifs[1]
	codPaidText := codPaid.Title + " | " + codPaid.Body
	if !strings.Contains(strings.ToLower(codPaidText), "thu tiền") {
		t.Errorf("PRM-B5: COD payment.succeeded must report cash collected ('thu tiền'), got %q", codPaidText)
	}
	if strings.Contains(codPaidText, "qua COD thành công") || strings.Contains(codPaidText, "256500 VND") {
		t.Errorf("PRM-B5/B7: COD payment.succeeded must not say 'qua COD thành công' or raw VND, got %q", codPaidText)
	}
	if !strings.Contains(codPaid.Body, "256.500₫") {
		t.Errorf("PRM-B7: expected 256.500₫ in COD payment.succeeded body, got %q", codPaid.Body)
	}
	if strings.Contains(mail.sent[1].Subject, "Payment Succeeded") || !strings.Contains(mail.sent[1].Subject, "256.500₫") {
		t.Errorf("PRM-B5/B7: COD payment.succeeded email subject must be Vietnamese with 256.500₫, got %q", mail.sent[1].Subject)
	}

	// 3. VIETQR order.created & payment.succeeded: must use Vietnamese label "chuyển khoản VietQR" and formatted VND.
	qrOrderPayload, _ := json.Marshal(map[string]any{
		"checkout_group_id": "grp-qr-1",
		"order_id":          "ord-qr-1",
		"order_code":        "NM-20261123-QR1",
		"buyer_id":          "buyer-vietqr",
		"shop_id":           "shop-1",
		"payment_method":    "VIETQR",
		"total_payable_vnd": 1250000,
	})
	if _, err := svc.ProcessEvent(context.Background(), "order.created", Envelope{
		EventID:   "evt-qr-created-1",
		EventType: "order.created",
		Payload:   qrOrderPayload,
	}); err != nil {
		t.Fatalf("VIETQR order.created failed: %v", err)
	}
	qrPayPayload, _ := json.Marshal(map[string]any{
		"payment_id":     "pay-qr-1",
		"order_group_id": "grp-qr-1",
		"order_id":       "grp-qr-1",
		"user_id":        "buyer-vietqr",
		"amount_vnd":     1250000,
		"method":         "VIETQR",
		"provider_tx_id": "VQR-TX-001",
	})
	if _, err := svc.ProcessEvent(context.Background(), "payment.succeeded", Envelope{
		EventID:   "evt-qr-paid-1",
		EventType: "payment.succeeded",
		Payload:   qrPayPayload,
	}); err != nil {
		t.Fatalf("VIETQR payment.succeeded failed: %v", err)
	}

	qrNotifs, _, _, _ := svc.ListNotifications(context.Background(), "buyer-vietqr", "", false, 1, 20)
	if len(qrNotifs) != 2 {
		t.Fatalf("expected 2 VIETQR notifications, got %d", len(qrNotifs))
	}
	if strings.Contains(qrNotifs[0].Body, "VIETQR") || !strings.Contains(qrNotifs[0].Body, "chuyển khoản VietQR") || !strings.Contains(qrNotifs[0].Body, "1.250.000₫") {
		t.Errorf("PRM-B7: VIETQR order.created must localize method & currency, got %q", qrNotifs[0].Body)
	}
	if qrNotifs[1].Title != "Thanh toán thành công" || strings.Contains(qrNotifs[1].Body, "VIETQR") || !strings.Contains(qrNotifs[1].Body, "chuyển khoản VietQR") || !strings.Contains(qrNotifs[1].Body, "1.250.000₫") {
		t.Errorf("PRM-B7: VIETQR payment.succeeded must localize method & currency, got title=%q body=%q", qrNotifs[1].Title, qrNotifs[1].Body)
	}
	if strings.Contains(mail.sent[3].Subject, "Payment Succeeded") || !strings.Contains(mail.sent[3].Subject, "1.250.000₫") {
		t.Errorf("PRM-B7: VIETQR payment.succeeded subject must be Vietnamese with 1.250.000₫, got %q", mail.sent[3].Subject)
	}
}

func TestVietnameseEmailsAndNoRawEnumsAcrossEvents(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	resolver := &stubUserResolver{emails: map[string]string{
		"seller-1": "seller1@nexusmart.local",
		"buyer-1":  "buyer1@nexusmart.local",
	}}
	svc := NewNotificationService(repo, mail, resolver, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cases := []struct {
		eventID   string
		eventType string
		payload   map[string]any
		forbidden []string
		required  []string
	}{
		{
			eventID:   "evt-shop-approve",
			eventType: "user.shop_reviewed",
			payload: map[string]any{
				"shop_id":       "shop-1",
				"owner_user_id": "seller-1",
				"shop_name":     "Xưởng Dệt Bạch Đằng",
				"decision":      "APPROVE",
			},
			forbidden: []string{"Shop review update", "APPROVE"},
			required:  []string{"Xưởng Dệt Bạch Đằng"},
		},
		{
			eventID:   "evt-shop-reject",
			eventType: "user.shop_reviewed",
			payload: map[string]any{
				"shop_id":       "shop-1",
				"owner_user_id": "seller-1",
				"shop_name":     "Xưởng Dệt Bạch Đằng",
				"decision":      "REJECT",
				"reason":        "Thiếu giấy phép kinh doanh",
			},
			forbidden: []string{"Shop review update", "REJECT"},
			required:  []string{"Thiếu giấy phép kinh doanh"},
		},
		{
			eventID:   "evt-shipped",
			eventType: "order.shipped",
			payload: map[string]any{
				"order_id":      "ord-10",
				"order_code":    "NM-10",
				"buyer_id":      "buyer-1",
				"shop_id":       "shop-1",
				"carrier_code":  "mock_express",
				"tracking_code": "MOCK-10",
			},
			forbidden: []string{"Order Shipped", "mock_express"},
			required:  []string{"Vận chuyển tiêu chuẩn NexusMart", "MOCK-10"},
		},
		{
			eventID:   "evt-ship-upd",
			eventType: "shipment.updated",
			payload: map[string]any{
				"shipment_id":   "sh-10",
				"order_id":      "ord-10",
				"shop_id":       "shop-1",
				"buyer_id":      "buyer-1",
				"carrier_code":  "mock_express",
				"tracking_code": "MOCK-10",
				"status":        "delivering",
			},
			forbidden: []string{"delivering", "mock_express"},
			required:  []string{"Đang vận chuyển"},
		},
		{
			eventID:   "evt-refund",
			eventType: "payment.refunded",
			payload: map[string]any{
				"refund_id":  "ref-10",
				"payment_id": "pay-10",
				"order_id":   "ord-10",
				"user_id":    "buyer-1",
				"amount_vnd": 141000,
				"reason":     "late_payment",
			},
			forbidden: []string{"Payment Refunded", "141000 VND", "late_payment"},
			required:  []string{"141.000₫"},
		},
		{
			eventID:   "evt-pay-failed",
			eventType: "payment.failed",
			payload: map[string]any{
				"payment_id":     "pay-11",
				"order_group_id": "grp-11",
				"order_id":       "grp-11",
				"user_id":        "buyer-1",
				"reason":         "payment_timeout_expired",
			},
			forbidden: []string{"Payment Failed", "payment_timeout_expired"},
			required:  []string{"Quá thời hạn thanh toán"},
		},
		{
			eventID:   "evt-confirmed-cod",
			eventType: "order.confirmed",
			payload: map[string]any{
				"order_id":          "ord-12",
				"order_code":        "NM-12",
				"buyer_id":          "buyer-1",
				"shop_id":           "shop-1",
				"payment_method":    "COD",
				"total_payable_vnd": 256500,
			},
			forbidden: []string{"Order Confirmed", "256500 VND", "COD"},
			required:  []string{"Xác nhận đơn hàng NM-12", "thanh toán khi nhận hàng", "256.500₫"},
		},
		{
			eventID:   "evt-confirmed-vietqr",
			eventType: "order.confirmed",
			payload: map[string]any{
				"order_id":          "ord-13",
				"order_code":        "NM-13",
				"buyer_id":          "buyer-1",
				"shop_id":           "shop-1",
				"payment_method":    "VIETQR",
				"total_payable_vnd": 350000,
			},
			forbidden: []string{"Order Confirmed", "350000 VND", "VIETQR"},
			required:  []string{"Xác nhận đơn hàng NM-13", "chuyển khoản VietQR", "350.000₫"},
		},
		{
			eventID:   "evt-cancelled",
			eventType: "order.cancelled",
			payload: map[string]any{
				"order_id":   "ord-14",
				"order_code": "NM-14",
				"buyer_id":   "buyer-1",
				"shop_id":    "shop-1",
				"reason":     "Payment expired after timeout",
			},
			forbidden: []string{"Order Cancelled", "Payment expired after timeout"},
			required:  []string{"Hủy đơn hàng NM-14", "Quá thời hạn thanh toán"},
		},
		{
			eventID:   "evt-delivered",
			eventType: "order.delivered",
			payload: map[string]any{
				"order_id":      "ord-15",
				"order_code":    "NM-15",
				"buyer_id":      "buyer-1",
				"shop_id":       "shop-1",
				"tracking_code": "GHN-15",
			},
			forbidden: []string{"Order Delivered"},
			required:  []string{"Đơn hàng NM-15 đã giao thành công", "GHN-15"},
		},
		{
			eventID:   "evt-completed-coins",
			eventType: "order.completed",
			payload: map[string]any{
				"order_id":     "ord-16",
				"order_code":   "NM-16",
				"buyer_id":     "buyer-1",
				"shop_id":      "shop-1",
				"coins_reward": 2000,
			},
			forbidden: []string{"Order Completed", "2000 xu"},
			required:  []string{"Đơn hàng NM-16 đã hoàn tất", "2.000 xu"},
		},
		{
			eventID:   "evt-completed-zero",
			eventType: "order.completed",
			payload: map[string]any{
				"order_id":     "ord-17",
				"order_code":   "NM-17",
				"buyer_id":     "buyer-1",
				"shop_id":      "shop-1",
				"coins_reward": 0,
			},
			forbidden: []string{"Order Completed", "0 xu"},
			required:  []string{"Đơn hàng NM-17 đã hoàn tất"},
		},
		{
			eventID:   "evt-pay-expired",
			eventType: "payment.expired",
			payload: map[string]any{
				"payment_id":     "pay-18",
				"order_group_id": "grp-18",
				"order_id":       "grp-18",
				"user_id":        "buyer-1",
				"reason":         "payment_timeout_expired",
			},
			forbidden: []string{"Payment Expired"},
			required:  []string{"Thanh toán đã hết hạn", "pay-18"},
		},
	}

	for _, tc := range cases {
		raw, _ := json.Marshal(tc.payload)
		beforeMails := len(mail.sent)
		beforeNotifs := len(repo.notifs)
		if _, err := svc.ProcessEvent(context.Background(), tc.eventType, Envelope{
			EventID:   tc.eventID,
			EventType: tc.eventType,
			Payload:   raw,
		}); err != nil {
			t.Fatalf("%s failed: %v", tc.eventType, err)
		}
		var combined string
		for _, n := range repo.notifs[beforeNotifs:] {
			combined += n.Title + "\n" + n.Body + "\n"
		}
		for _, m := range mail.sent[beforeMails:] {
			combined += m.Subject + "\n" + m.Body + "\n"
		}
		for _, bad := range tc.forbidden {
			if strings.Contains(combined, bad) {
				t.Errorf("PRM-B7 (%s): output must not contain %q, got:\n%s", tc.eventType, bad, combined)
			}
		}
		for _, want := range tc.required {
			if !strings.Contains(combined, want) {
				t.Errorf("PRM-B7 (%s): output must contain %q, got:\n%s", tc.eventType, want, combined)
			}
		}
	}
}

func TestInAppLinksPointToRealWebRoutesAndUseOrderID(t *testing.T) {
	repo := newMemRepo()
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mustSend := func(eventID, eventType string, payload map[string]any) store.Notification {
		t.Helper()
		raw, _ := json.Marshal(payload)
		before := len(repo.notifs)
		if _, err := svc.ProcessEvent(context.Background(), eventType, Envelope{
			EventID:   eventID,
			EventType: eventType,
			Payload:   raw,
		}); err != nil {
			t.Fatalf("%s failed: %v", eventType, err)
		}
		if len(repo.notifs) != before+1 {
			t.Fatalf("%s did not create an in-app notification", eventType)
		}
		return repo.notifs[len(repo.notifs)-1]
	}

	// 1. user.registered -> /dang-ky (not /verify-otp)
	nReg := mustSend("evt-link-reg", "user.registered", map[string]any{
		"user_id":   "u-link-1",
		"email":     "link@nexusmart.local",
		"full_name": "Link Buyer",
		"otp":       "112233",
	})
	if nReg.Link != "/dang-ky" {
		t.Errorf("PRM-B6/USR-B9: user.registered link = %q, want /dang-ky", nReg.Link)
	}

	// 2. user.shop_reviewed APPROVE -> /seller (not /seller/dashboard); REJECT/REQUEST_INFO -> /tai-khoan?tab=gian-hang
	nApprove := mustSend("evt-link-shop-ok", "user.shop_reviewed", map[string]any{
		"shop_id":       "shop-1",
		"owner_user_id": "u-link-1",
		"shop_name":     "Shop A",
		"decision":      "APPROVE",
	})
	if nApprove.Link != "/seller" {
		t.Errorf("PRM-B6/USR-B9: shop APPROVE link = %q, want /seller", nApprove.Link)
	}
	nReject := mustSend("evt-link-shop-rej", "user.shop_reviewed", map[string]any{
		"shop_id":       "shop-1",
		"owner_user_id": "u-link-1",
		"shop_name":     "Shop A",
		"decision":      "REJECT",
		"reason":        "Thiếu thông tin",
	})
	if nReject.Link != "/tai-khoan?tab=gian-hang" {
		t.Errorf("USR-B9: shop REJECT link = %q, want /tai-khoan?tab=gian-hang", nReject.Link)
	}

	// 3. Single-shop checkout: order.created -> /don-hang/<order_id>, then payment.succeeded carrying checkout_group_id -> MUST resolve to /don-hang/<order_id>!
	nSingleOrder := mustSend("evt-link-single-ord", "order.created", map[string]any{
		"checkout_group_id": "grp-single-1",
		"order_id":          "ord-real-1",
		"order_code":        "NM-SINGLE-1",
		"buyer_id":          "u-link-1",
		"shop_id":           "shop-1",
		"payment_method":    "VIETQR",
		"total_payable_vnd": 200000,
	})
	if nSingleOrder.Link != "/don-hang/ord-real-1" {
		t.Errorf("PRM-B6: order.created link = %q, want /don-hang/ord-real-1", nSingleOrder.Link)
	}
	nSinglePay := mustSend("evt-link-single-pay", "payment.succeeded", map[string]any{
		"payment_id":     "pay-single-1",
		"order_group_id": "grp-single-1",
		"order_id":       "grp-single-1",
		"user_id":        "u-link-1",
		"amount_vnd":     200000,
		"method":         "VIETQR",
		"provider_tx_id": "TX-1",
	})
	if nSinglePay.Link != "/don-hang/ord-real-1" {
		t.Errorf("ORD-B10/PRM-B6: single-shop payment.succeeded link = %q, want /don-hang/ord-real-1 (must resolve order_id, not checkout_group_id)", nSinglePay.Link)
	}

	// 4. Multi-shop checkout: payment.succeeded carrying checkout_group_id -> /don-hang (never /don-hang/<checkout_group_id>).
	_ = mustSend("evt-link-multi-1", "order.created", map[string]any{
		"checkout_group_id": "grp-multi-1",
		"order_id":          "ord-multi-1",
		"order_code":        "NM-MULTI-1",
		"buyer_id":          "u-link-1",
		"shop_id":           "shop-1",
		"payment_method":    "VIETQR",
		"total_payable_vnd": 100000,
	})
	_ = mustSend("evt-link-multi-2", "order.created", map[string]any{
		"checkout_group_id": "grp-multi-1",
		"order_id":          "ord-multi-2",
		"order_code":        "NM-MULTI-2",
		"buyer_id":          "u-link-1",
		"shop_id":           "shop-2",
		"payment_method":    "VIETQR",
		"total_payable_vnd": 150000,
	})
	nMultiPay := mustSend("evt-link-multi-pay", "payment.succeeded", map[string]any{
		"payment_id":     "pay-multi-1",
		"order_group_id": "grp-multi-1",
		"order_id":       "grp-multi-1",
		"user_id":        "u-link-1",
		"amount_vnd":     250000,
		"method":         "VIETQR",
		"provider_tx_id": "TX-MULTI",
	})
	if nMultiPay.Link != "/don-hang" {
		t.Errorf("ORD-B10/PRM-B6: multi-shop payment.succeeded link = %q, want /don-hang", nMultiPay.Link)
	}

	// 5. Unseen group race: payment.failed with order_id == order_group_id -> /don-hang (never /don-hang/<group_id>).
	nRace := mustSend("evt-link-race", "payment.failed", map[string]any{
		"payment_id":     "pay-race-1",
		"order_group_id": "grp-unseen-99",
		"order_id":       "grp-unseen-99",
		"user_id":        "u-link-1",
		"reason":         "payment_timeout_expired",
	})
	if nRace.Link != "/don-hang" {
		t.Errorf("ORD-B10: payment.failed for unseen checkout_group_id link = %q, want /don-hang", nRace.Link)
	}

	// 6. Per-sub-order COD payment.succeeded (distinct order_id != order_group_id) -> /don-hang/<order_id>.
	nSubCOD := mustSend("evt-link-sub-cod", "payment.succeeded", map[string]any{
		"payment_id":     "pay-cod-multi",
		"order_group_id": "grp-multi-1",
		"order_id":       "ord-multi-2",
		"user_id":        "u-link-1",
		"amount_vnd":     150000,
		"method":         "COD",
		"provider_tx_id": "COD-M2",
	})
	if nSubCOD.Link != "/don-hang/ord-multi-2" {
		t.Errorf("ORD-B10: per-sub-order COD payment.succeeded link = %q, want /don-hang/ord-multi-2", nSubCOD.Link)
	}

	// 7. Per-sub-order payment.refunded -> /don-hang/<order_id>.
	nRefund := mustSend("evt-link-refund", "payment.refunded", map[string]any{
		"refund_id":  "ref-1",
		"payment_id": "pay-single-1",
		"order_id":   "ord-real-1",
		"user_id":    "u-link-1",
		"amount_vnd": 50000,
		"reason":     "Hoàn một phần",
	})
	if nRefund.Link != "/don-hang/ord-real-1" {
		t.Errorf("PRM-B6: payment.refunded link = %q, want /don-hang/ord-real-1", nRefund.Link)
	}

	// 8. Group-level payment.refunded (single-shop group ID in order_id) -> resolves to /don-hang/<order_id>.
	nGroupRefundSingle := mustSend("evt-link-grp-refund-1", "payment.refunded", map[string]any{
		"refund_id":  "ref-grp-1",
		"payment_id": "pay-single-1",
		"order_id":   "grp-single-1",
		"user_id":    "u-link-1",
		"amount_vnd": 200000,
	})
	if nGroupRefundSingle.Link != "/don-hang/ord-real-1" {
		t.Errorf("ORD-B10: group-level single-shop payment.refunded link = %q, want /don-hang/ord-real-1", nGroupRefundSingle.Link)
	}

	// 9. Group-level payment.refunded (multi-shop group ID in order_id) -> /don-hang.
	nGroupRefundMulti := mustSend("evt-link-grp-refund-2", "payment.refunded", map[string]any{
		"refund_id":  "ref-grp-2",
		"payment_id": "pay-multi-1",
		"order_id":   "grp-multi-1",
		"user_id":    "u-link-1",
		"amount_vnd": 250000,
	})
	if nGroupRefundMulti.Link != "/don-hang" {
		t.Errorf("ORD-B10: group-level multi-shop payment.refunded link = %q, want /don-hang", nGroupRefundMulti.Link)
	}

	// 10. user.shop_reviewed REQUEST_INFO -> /tai-khoan?tab=gian-hang.
	nReqInfo := mustSend("evt-link-shop-reqinfo", "user.shop_reviewed", map[string]any{
		"shop_id":       "shop-1",
		"owner_user_id": "u-link-1",
		"shop_name":     "Shop A",
		"decision":      "REQUEST_INFO",
		"reason":        "Bổ sung GPKD",
	})
	if nReqInfo.Link != "/tai-khoan?tab=gian-hang" {
		t.Errorf("USR-B9: shop REQUEST_INFO link = %q, want /tai-khoan?tab=gian-hang", nReqInfo.Link)
	}

	// 11. order.returned -> /don-hang/<order_id> and preserves checkout_group_id.
	nReturned := mustSend("evt-link-returned", "order.returned", map[string]any{
		"order_id":          "ord-real-1",
		"order_code":        "NM-SINGLE-1",
		"checkout_group_id": "grp-single-1",
		"buyer_id":          "u-link-1",
		"shop_id":           "shop-1",
		"reason":            "Khách từ chối nhận hàng",
	})
	if nReturned.Link != "/don-hang/ord-real-1" {
		t.Errorf("order.returned link = %q, want /don-hang/ord-real-1", nReturned.Link)
	}
}

func TestFormatVND(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0₫"},
		{500, "500₫"},
		{999, "999₫"},
		{1000, "1.000₫"},
		{256500, "256.500₫"},
		{1250000, "1.250.000₫"},
		{1000000000, "1.000.000.000₫"},
		{-50000, "-50.000₫"},
		{-100, "-100₫"},
		{math.MinInt64, "-9.223.372.036.854.775.808₫"},
	}
	for _, tc := range tests {
		if got := FormatVND(tc.in); got != tc.want {
			t.Errorf("FormatVND(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
