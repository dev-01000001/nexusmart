package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
)

type noopConsumerRepo struct {
	mu        sync.Mutex
	processed map[string]bool
	notifs    []store.Notification
}

func newNoopConsumerRepo() *noopConsumerRepo {
	return &noopConsumerRepo{processed: make(map[string]bool)}
}

func (r *noopConsumerRepo) ListNotifications(_ context.Context, _, _ string, _ bool, _, _ int32) ([]store.Notification, int64, int64, error) {
	return nil, 0, 0, nil
}
func (r *noopConsumerRepo) MarkRead(_ context.Context, _ string, _ []string, _ bool) (int32, int64, error) {
	return 0, 0, nil
}
func (r *noopConsumerRepo) GetPreferences(_ context.Context, userID string) (store.Preferences, error) {
	return store.DefaultPreferences(userID), nil
}
func (r *noopConsumerRepo) UpsertPreferences(_ context.Context, prefs store.Preferences) (store.Preferences, error) {
	return prefs, nil
}
func (r *noopConsumerRepo) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processed[eventID], nil
}
func (r *noopConsumerRepo) DeleteProcessedEvent(_ context.Context, eventID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.processed, eventID)
	return nil
}
func (r *noopConsumerRepo) GetUserContact(_ context.Context, _ string) (store.UserContact, bool, error) {
	return store.UserContact{}, false, nil
}
func (r *noopConsumerRepo) GetOrderParticipant(_ context.Context, _ string) (store.OrderParticipant, bool, error) {
	return store.OrderParticipant{}, false, nil
}
func (r *noopConsumerRepo) ListOrderParticipantsByGroup(_ context.Context, _ string) ([]store.OrderParticipant, error) {
	return nil, nil
}
func (r *noopConsumerRepo) GetShopOwner(_ context.Context, _ string) (store.ShopOwner, bool, error) {
	return store.ShopOwner{}, false, nil
}
func (r *noopConsumerRepo) RecordEmailDedup(_ context.Context, _, _ string) error {
	return nil
}
func (r *noopConsumerRepo) ProcessEventTx(_ context.Context, eventID, _ string, fn func(tx store.TxStore) error) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventID] {
		return true, nil
	}
	tx := &noopConsumerTx{repo: r}
	if err := fn(tx); err != nil {
		return false, err
	}
	r.processed[eventID] = true
	return false, nil
}
func (r *noopConsumerRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	return nil
}

type noopConsumerTx struct {
	repo *noopConsumerRepo
}

func (t *noopConsumerTx) InsertNotification(_ context.Context, n store.Notification) (store.Notification, error) {
	t.repo.notifs = append(t.repo.notifs, n)
	return n, nil
}
func (t *noopConsumerTx) HasNotificationForEvent(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (t *noopConsumerTx) GetPreferences(_ context.Context, userID string) (store.Preferences, error) {
	return store.DefaultPreferences(userID), nil
}
func (t *noopConsumerTx) UpsertUserContact(_ context.Context, _ store.UserContact) error {
	return nil
}
func (t *noopConsumerTx) GetUserContact(_ context.Context, _ string) (store.UserContact, bool, error) {
	return store.UserContact{}, false, nil
}
func (t *noopConsumerTx) UpsertOrderParticipant(_ context.Context, _ store.OrderParticipant) error {
	return nil
}
func (t *noopConsumerTx) GetOrderParticipant(_ context.Context, _ string) (store.OrderParticipant, bool, error) {
	return store.OrderParticipant{}, false, nil
}
func (t *noopConsumerTx) ListOrderParticipantsByGroup(_ context.Context, _ string) ([]store.OrderParticipant, error) {
	return nil, nil
}
func (t *noopConsumerTx) UpsertShopOwner(_ context.Context, _ store.ShopOwner) error {
	return nil
}
func (t *noopConsumerTx) GetShopOwner(_ context.Context, _ string) (store.ShopOwner, bool, error) {
	return store.ShopOwner{}, false, nil
}
func (t *noopConsumerTx) ClaimEmailDedup(_ context.Context, _, eventID string) (string, error) {
	return eventID, nil
}
func (t *noopConsumerTx) HasEmailDedup(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestConsumerOrderAddressUpdatedMalformedRoutesToDLQAndRedactsPII(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	svc := service.NewNotificationService(&failOnProcessRepo{t: t}, nil, nil, logger)

	cases := []struct {
		name    string
		eventID string
		payload string
	}{
		{
			name:    "MissingOrderIDWithRoguePII",
			eventID: "evt-addr-dlq-no-order",
			payload: `{"shop_id":"shop-1","buyer_id":"buyer-1","province_name":"TP. Hồ Chí Minh","ward_name":"Phường Bến Nghé","street_address":"99 Nguyễn Huệ","recipient_phone":"0988777666","phone":"0911222333","recipient_name":"Trần Văn Mật"}`,
		},
		{
			name:    "MissingShopIDWithRoguePII",
			eventID: "evt-addr-dlq-no-shop",
			payload: `{"order_id":"ord-1","buyer_id":"buyer-1","province_name":"Hà Nội","ward_name":"Phường Ba Đình","street_address":"99 Nguyễn Huệ","recipient_phone":"0988777666","phone":"0911222333","recipient_name":"Trần Văn Mật"}`,
		},
	}

	piiSecrets := []string{"99 Nguyễn Huệ", "0988777666", "0911222333", "Trần Văn Mật"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dlq := &stubDLQ{}
			c := NewTestableConsumer(svc, dlq, dlq, 5, logger)

			raw, _ := json.Marshal(service.Envelope{
				EventID:   tc.eventID,
				EventType: "order.address_updated",
				Payload:   json.RawMessage(tc.payload),
			})
			msg := &fakeMsg{subject: "order.address_updated", data: raw, delivered: 1}
			c.HandleMessage(context.Background(), msg)

			if !msg.acked || msg.naked {
				t.Fatalf("(%s): malformed order.address_updated must route to DLQ and Ack, acked=%v naked=%v", tc.name, msg.acked, msg.naked)
			}
			if len(dlq.records) != 1 || dlq.records[0].eventID != tc.eventID || dlq.records[0].subject != "order.address_updated" {
				t.Fatalf("(%s): expected 1 dead_letter_events record, got %+v", tc.name, dlq.records)
			}
			if len(dlq.pubSubj) != 1 || dlq.pubSubj[0] != "dlq.order.address_updated" {
				t.Fatalf("(%s): expected publish to dlq.order.address_updated, got %+v", tc.name, dlq.pubSubj)
			}
			for _, secret := range piiSecrets {
				if strings.Contains(string(dlq.records[0].payload), secret) {
					t.Fatalf("SECURITY (%s): PII %q leaked into dead_letter_events: %s", tc.name, secret, string(dlq.records[0].payload))
				}
				if strings.Contains(string(dlq.pubData[0]), secret) {
					t.Fatalf("SECURITY (%s): PII %q leaked into NEXUSMART_DLQ: %s", tc.name, secret, string(dlq.pubData[0]))
				}
				if strings.Contains(logBuf.String(), secret) {
					t.Fatalf("SECURITY (%s): PII %q leaked into logs: %s", tc.name, secret, logBuf.String())
				}
			}
			if !strings.Contains(string(dlq.records[0].payload), "[REDACTED]") {
				t.Fatalf("(%s): expected [REDACTED] marker in sanitized DLQ payload: %s", tc.name, string(dlq.records[0].payload))
			}

			// E1 guard: when BOTH DLQ sinks fail, message must NakWithDelay and NEVER Ack.
			failingDLQ := &stubDLQ{
				storeErr: errors.New("postgres down"),
				pubErr:   errors.New("nats down"),
			}
			cFail := NewTestableConsumer(svc, failingDLQ, failingDLQ, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))
			msgFail := &fakeMsg{subject: "order.address_updated", data: raw, delivered: 1}
			cFail.HandleMessage(context.Background(), msgFail)
			if msgFail.acked || !msgFail.naked || msgFail.nakDelay <= 0 {
				t.Fatalf("E1 (%s): must NakWithDelay when both DLQ sinks fail, acked=%v naked=%v delay=%v", tc.name, msgFail.acked, msgFail.naked, msgFail.nakDelay)
			}
		})
	}

	// Broken JSON on order.address_updated containing street address / phone must be fully redacted in DLQ.
	dlq := &stubDLQ{}
	c := NewTestableConsumer(svc, dlq, dlq, 5, logger)
	badRaw := []byte(`{"event_id":"evt-addr-broken","payload":{"street_address":"88 Lý Thường Kiệt","recipient_phone":"0977666555" broken`)
	badMsg := &fakeMsg{subject: "order.address_updated", data: badRaw, delivered: 1}
	c.HandleMessage(context.Background(), badMsg)

	if !badMsg.acked || len(dlq.records) != 1 || len(dlq.pubData) != 1 {
		t.Fatalf("broken order.address_updated JSON must Ack after DLQ, acked=%v records=%d pub=%d", badMsg.acked, len(dlq.records), len(dlq.pubData))
	}
	for _, secret := range []string{"88 Lý Thường Kiệt", "0977666555"} {
		if strings.Contains(string(dlq.records[0].payload), secret) {
			t.Fatalf("SECURITY: broken JSON PII %q leaked into dead_letter_events: %s", secret, string(dlq.records[0].payload))
		}
		if strings.Contains(string(dlq.pubData[0]), secret) {
			t.Fatalf("SECURITY: broken JSON PII %q leaked into NEXUSMART_DLQ: %s", secret, string(dlq.pubData[0]))
		}
		if strings.Contains(logBuf.String(), secret) {
			t.Fatalf("SECURITY: broken JSON PII %q leaked into logs: %s", secret, logBuf.String())
		}
	}
}

func TestConsumerB1ProductEventsAckCleanlyWithZeroDLQ(t *testing.T) {
	repo := newNoopConsumerRepo()
	svc := service.NewNotificationService(repo, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dlq := &stubDLQ{}
	c := NewTestableConsumer(svc, dlq, dlq, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))

	events := []struct {
		subject string
		eventID string
		payload string
	}{
		{
			subject: "product.upserted",
			eventID: "evt-b1-cons-upsert",
			payload: `{"product_id":"p1","shop_id":"s1","category_id":"c1","name":"Áo Khoác","slug":"ao-khoac","min_price_vnd":250000,"max_price_vnd":250000,"status":"APPROVED","rating_avg_x100":500,"review_count":3,"shop_suspended":false,"updated_at":"2026-11-25T02:00:00Z"}`,
		},
		{
			subject: "product.deleted",
			eventID: "evt-b1-cons-delete",
			payload: `{"product_id":"p1","shop_id":"s1","reason":"DELETED","updated_at":"2026-11-25T02:05:00Z"}`,
		},
		{
			subject: "product.category_updated",
			eventID: "evt-b1-cons-cat",
			payload: `{"category_id":"c1","name":"Thời trang nam","slug":"thoi-trang-nam","parent_id":"","sort_order":1,"action":"UPSERT","updated_at":"2026-11-25T02:10:00Z"}`,
		},
	}

	for _, ev := range events {
		raw, _ := json.Marshal(service.Envelope{
			EventID:   ev.eventID,
			EventType: ev.subject,
			Payload:   json.RawMessage(ev.payload),
		})
		for attempt := 1; attempt <= 2; attempt++ {
			msg := &fakeMsg{subject: ev.subject, data: raw, delivered: 1}
			c.HandleMessage(context.Background(), msg)
			if !msg.acked || msg.naked {
				t.Fatalf("%s (run %d): expected clean Ack, got acked=%v naked=%v", ev.subject, attempt, msg.acked, msg.naked)
			}
		}
	}

	if len(dlq.records) != 0 || len(dlq.pubSubj) != 0 {
		t.Fatalf("B1 product events must produce 0 DLQ entries, got records=%+v pub=%+v", dlq.records, dlq.pubSubj)
	}
	if len(repo.notifs) != 0 {
		t.Fatalf("B1 product events must produce 0 notifications, got %d", len(repo.notifs))
	}
}
