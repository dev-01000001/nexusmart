package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/internal/indexer"
)

type stubDLQPub struct {
	fail bool
	msgs []*nats.Msg
}

func (p *stubDLQPub) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	if p.fail {
		return nil, errors.New("nats dlq unavailable")
	}
	p.msgs = append(p.msgs, m)
	return &nats.PubAck{Stream: "NEXUSMART_DLQ", Sequence: uint64(len(p.msgs))}, nil
}

type stubDLQStore struct {
	fail bool
	rows []db.InsertDeadLetterEventParams
}

func (s *stubDLQStore) InsertDeadLetterEvent(_ context.Context, arg db.InsertDeadLetterEventParams) error {
	if s.fail {
		return errors.New("db dlq unavailable")
	}
	s.rows = append(s.rows, arg)
	return nil
}

type stubEventHandler struct {
	shopStatusErr error
	upsertErr     error
	upserts       int
}

func (h *stubEventHandler) HandleProductUpserted(context.Context, string, indexer.ProductUpsertInput) error {
	h.upserts++
	return h.upsertErr
}
func (h *stubEventHandler) HandleProductDeleted(context.Context, string, string, string, time.Time) error {
	return nil
}
func (h *stubEventHandler) HandleCategoryUpdated(context.Context, string, indexer.CategoryUpdateInput) error {
	return nil
}
func (h *stubEventHandler) HandleShopReviewed(context.Context, string, indexer.ShopReviewedInput) error {
	return nil
}
func (h *stubEventHandler) HandleShopStatusChanged(_ context.Context, _ string, in indexer.ShopStatusChangedInput) error {
	if in.Version <= 0 {
		return fmt.Errorf("%w: non-positive version %d", indexer.ErrPermanentPayload, in.Version)
	}
	return h.shopStatusErr
}

func TestConsumer_E1_DLQSemanticsAndUnknownSubjects(t *testing.T) {
	ctx := context.Background()

	t.Run("malformed JSON routes to DLQ and acks only if sink succeeds", func(t *testing.T) {
		dlqPub := &stubDLQPub{}
		dlqStore := &stubDLQStore{}
		c := NewConsumer(nil, dlqPub, dlqStore, &stubEventHandler{}, nil, "NEXUSMART")

		act, err := c.ProcessRawMessage(ctx, SubjectProductUpserted, []byte(`{bad`), nil, 1)
		if err == nil || act != ActionDLQ {
			t.Fatalf("got action=%v err=%v, want ActionDLQ + err", act, err)
		}
		if len(dlqStore.rows) != 1 || len(dlqPub.msgs) != 1 {
			t.Fatalf("expected both DLQ sinks written, got db=%d nats=%d", len(dlqStore.rows), len(dlqPub.msgs))
		}
	})

	t.Run("both DLQ sinks failing returns ActionNak instead of dropping poison message", func(t *testing.T) {
		dlqPub := &stubDLQPub{fail: true}
		dlqStore := &stubDLQStore{fail: true}
		c := NewConsumer(nil, dlqPub, dlqStore, &stubEventHandler{}, nil, "NEXUSMART")

		act, err := c.ProcessRawMessage(ctx, SubjectProductUpserted, []byte(`{bad`), nil, 1)
		if err == nil || act != ActionNak {
			t.Fatalf("expected ActionNak when both DLQ sinks fail, got action=%v err=%v", act, err)
		}
	})

	t.Run("shop_status_changed version<=0 routes immediately to DLQ", func(t *testing.T) {
		dlqPub := &stubDLQPub{}
		dlqStore := &stubDLQStore{}
		c := NewConsumer(nil, dlqPub, dlqStore, &stubEventHandler{}, nil, "NEXUSMART")

		raw, _ := json.Marshal(Envelope{
			EventID:    "ev-bad-v0",
			EventType:  SubjectUserShopStatusChanged,
			OccurredAt: time.Now().UTC(),
			Payload:    json.RawMessage(`{"shop_id":"shop-1","to_status":"SUSPENDED","version":0}`),
		})
		act, err := c.ProcessRawMessage(ctx, SubjectUserShopStatusChanged, raw, nil, 1)
		if err == nil || act != ActionDLQ {
			t.Fatalf("expected ActionDLQ for version=0, got action=%v err=%v", act, err)
		}
		if len(dlqStore.rows) != 1 {
			t.Fatalf("expected dead_letter_events row for version=0")
		}
	})

	t.Run("unrelated user/product subjects safely Ack without touching DLQ", func(t *testing.T) {
		dlqPub := &stubDLQPub{}
		dlqStore := &stubDLQStore{}
		c := NewConsumer(nil, dlqPub, dlqStore, &stubEventHandler{}, nil, "NEXUSMART")

		for _, subj := range []string{"product.reviewed", "user.registered", "user.phone_otp_requested", "user.password_reset_requested"} {
			raw, _ := json.Marshal(Envelope{
				EventID:    "ev-" + subj,
				EventType:  subj,
				OccurredAt: time.Now().UTC(),
				Payload:    json.RawMessage(`{"user_id":"u1"}`),
			})
			act, err := c.ProcessRawMessage(ctx, subj, raw, nil, 1)
			if err != nil || act != ActionAck {
				t.Fatalf("subject %s got action=%v err=%v, want ActionAck", subj, act, err)
			}
		}
		if len(dlqStore.rows) != 0 || len(dlqPub.msgs) != 0 {
			t.Fatalf("unrelated subjects must not write DLQ")
		}
	})

	t.Run("transient error retries until maxAttempts then DLQ", func(t *testing.T) {
		dlqPub := &stubDLQPub{}
		dlqStore := &stubDLQStore{}
		h := &stubEventHandler{upsertErr: errors.New("temporary db hiccup")}
		c := NewConsumer(nil, dlqPub, dlqStore, h, nil, "NEXUSMART")
		c.SetMaxAttempts(3)

		raw, _ := json.Marshal(Envelope{
			EventID:    "ev-transient",
			EventType:  SubjectProductUpserted,
			OccurredAt: time.Now().UTC(),
			Payload:    json.RawMessage(`{"product_id":"p1","status":"APPROVED"}`),
		})
		act1, _ := c.ProcessRawMessage(ctx, SubjectProductUpserted, raw, nil, 1)
		act2, _ := c.ProcessRawMessage(ctx, SubjectProductUpserted, raw, nil, 2)
		act3, _ := c.ProcessRawMessage(ctx, SubjectProductUpserted, raw, nil, 3)
		if act1 != ActionNak || act2 != ActionNak || act3 != ActionDLQ {
			t.Fatalf("got actions [%v, %v, %v], want [Nak, Nak, DLQ]", act1, act2, act3)
		}
		if len(dlqStore.rows) != 1 || len(dlqPub.msgs) != 1 {
			t.Fatalf("expected 1 DLQ write at attempt 3, got db=%d nats=%d", len(dlqStore.rows), len(dlqPub.msgs))
		}
	})

	t.Run("shutdown cancellation never DLQs even on attempt >= maxAttempts", func(t *testing.T) {
		dlqPub := &stubDLQPub{}
		dlqStore := &stubDLQStore{}
		c := NewConsumer(nil, dlqPub, dlqStore, &stubEventHandler{}, nil, "NEXUSMART")
		_ = c.Stop()

		raw, _ := json.Marshal(Envelope{
			EventID:    "ev-shut",
			EventType:  SubjectProductUpserted,
			OccurredAt: time.Now().UTC(),
			Payload:    json.RawMessage(`{"product_id":"p1","status":"APPROVED"}`),
		})
		act, _ := c.ProcessRawMessage(ctx, SubjectProductUpserted, raw, nil, 25)
		if act != ActionNak {
			t.Fatalf("expected ActionNak while stopping, got %v", act)
		}
		if len(dlqStore.rows) != 0 || len(dlqPub.msgs) != 0 {
			t.Fatalf("stopping consumer must never write DLQ")
		}
	})

	t.Run("S13 default maxAttempts is 20 for transient errors", func(t *testing.T) {
		dlqPub := &stubDLQPub{}
		dlqStore := &stubDLQStore{}
		h := &stubEventHandler{upsertErr: errors.New("meili restarting")}
		c := NewConsumer(nil, dlqPub, dlqStore, h, nil, "NEXUSMART")

		raw, _ := json.Marshal(Envelope{
			EventID:    "ev-s13-transient",
			EventType:  SubjectProductUpserted,
			OccurredAt: time.Now().UTC(),
			Payload:    json.RawMessage(`{"product_id":"p1","status":"APPROVED"}`),
		})
		for attempt := uint64(1); attempt <= 19; attempt++ {
			act, _ := c.ProcessRawMessage(ctx, SubjectProductUpserted, raw, nil, attempt)
			if act != ActionNak {
				t.Fatalf("attempt %d: got %v, want ActionNak (S13 maxAttempts=20)", attempt, act)
			}
		}
		act20, _ := c.ProcessRawMessage(ctx, SubjectProductUpserted, raw, nil, 20)
		if act20 != ActionDLQ {
			t.Fatalf("attempt 20: got %v, want ActionDLQ", act20)
		}
		if len(dlqStore.rows) != 1 || len(dlqPub.msgs) != 1 {
			t.Fatalf("expected 1 DLQ write at attempt 20, got db=%d nats=%d", len(dlqStore.rows), len(dlqPub.msgs))
		}

		// Verify S13 backoff schedule: min(2s * n, 30s).
		wantDelays := map[uint64]time.Duration{
			0:  2 * time.Second,
			1:  2 * time.Second,
			5:  10 * time.Second,
			14: 28 * time.Second,
			15: 30 * time.Second,
			20: 30 * time.Second,
		}
		for n, want := range wantDelays {
			if got := nakDelay(n); got != want {
				t.Fatalf("S13 nakDelay(%d) = %v, want %v", n, got, want)
			}
		}
	})
}

func TestConsumer_K2_MaxAckPendingBudget(t *testing.T) {
	if maxAckPending != 4 {
		t.Fatalf("K2: maxAckPending = %d, want 4", maxAckPending)
	}
	if processTimeout != 25*time.Second {
		t.Fatalf("K2: processTimeout = %v, want 25s", processTimeout)
	}
	if ackWait != 120*time.Second {
		t.Fatalf("K2: ackWait = %v, want 120s", ackWait)
	}
	if budget := time.Duration(maxAckPending) * processTimeout; budget >= ackWait {
		t.Fatalf("K2 invariant violated: maxAckPending(%d) * processTimeout(%v) = %v must be < ackWait(%v)",
			maxAckPending, processTimeout, budget, ackWait)
	}
}
