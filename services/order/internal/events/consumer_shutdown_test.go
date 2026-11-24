package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/db"
)

type shutdownTestHandler struct {
	mu          sync.Mutex
	onSucceeded func(ctx context.Context, eventID string) error
	calls       atomic.Int32
}

func (h *shutdownTestHandler) HandlePaymentSucceeded(ctx context.Context, eventID string, _ PaymentSucceededPayload) error {
	h.calls.Add(1)
	h.mu.Lock()
	fn := h.onSucceeded
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, eventID)
	}
	return nil
}

func (h *shutdownTestHandler) HandlePaymentFailed(_ context.Context, _ string, _ PaymentFailedPayload) error {
	return nil
}
func (h *shutdownTestHandler) HandlePaymentExpired(_ context.Context, _ string, _ PaymentFailedPayload) error {
	return nil
}
func (h *shutdownTestHandler) HandlePaymentRefunded(_ context.Context, _ string, _ PaymentRefundedPayload) error {
	return nil
}
func (h *shutdownTestHandler) HandlePaymentRefundRejected(_ context.Context, _ string, _ PaymentRefundRejectedPayload) error {
	return nil
}
func (h *shutdownTestHandler) HandleShipmentUpdated(_ context.Context, _ string, _ ShipmentUpdatedPayload) error {
	return nil
}
func (h *shutdownTestHandler) HandleShipmentCreateFailed(_ context.Context, _ string, _ ShipmentCreateFailedPayload) error {
	return nil
}

type stubDLQStore struct {
	mu      sync.Mutex
	err     error
	lastCtx error
	records []db.InsertDeadLetterEventParams
}

func (s *stubDLQStore) InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCtx = ctx.Err()
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, arg)
	return nil
}

func makeTestPaymentEvent(t *testing.T, eventID string) []byte {
	t.Helper()
	body, err := json.Marshal(Envelope{
		EventID:   eventID,
		EventType: SubjectPaymentSucceeded,
		Payload:   json.RawMessage(`{"payment_id":"p1","order_group_id":"g1","amount_vnd":100000}`),
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

// TestConsumer_P0_2_P1_4_ShutdownAndDLQGuarantees verifies directives 4, 5, and 6:
//   - Canceled ctx at attempt 5 -> 0 DLQ writes, returns ActionNak, and handleNATSMessage neither Acks nor Naks.
//   - Both DLQ sinks failing (attempt 5, malformed JSON, missing event_id) -> returns ActionNak (never ActionDLQ/Ack).
//   - DLQ write uses context.WithoutCancel + 5s timeout so a timed-out handler (DeadlineExceeded) still persists DLQ.
//   - Stop() waits for in-flight handler without Drain/Unsubscribe; once stopping (or canceled ctx), onMessage skips handler with 0 Nak.
func TestConsumer_P0_2_P1_4_ShutdownAndDLQGuarantees(t *testing.T) {
	t.Run("CanceledCtxAtAttempt5_NoDLQAndNoAck", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var logBuf bytes.Buffer
		log := logger.New("order-test", logger.WithWriter(&logBuf))
		dlqStore := &stubDLQStore{}
		dlqPub := &recordingPublisher{}
		h := &shutdownTestHandler{
			onSucceeded: func(hCtx context.Context, _ string) error {
				cancel()
				return hCtx.Err()
			},
		}
		c := NewConsumer(nil, dlqPub, dlqStore, h, log, "NEXUSMART")
		body := makeTestPaymentEvent(t, "evt-cancel-att5")

		action, err := c.ProcessRawMessage(ctx, SubjectPaymentSucceeded, body, nil, defaultMaxDeliverAttempts)
		if err == nil || action != ActionNak {
			t.Fatalf("P0-2: canceled ctx at attempt 5 must return ActionNak (not ActionDLQ), got action=%v err=%v", action, err)
		}
		if len(dlqStore.records) != 0 || len(dlqPub.msgs) != 0 {
			t.Fatalf("P0-2: canceled ctx at attempt 5 must NOT write DLQ (store=%d pub=%d)", len(dlqStore.records), len(dlqPub.msgs))
		}

		// Also verify handleNATSMessage with canceled baseCtx neither calls msg.Ack() nor msg.NakWithDelay().
		ctx2, cancel2 := context.WithCancel(context.Background())
		h.onSucceeded = func(hCtx context.Context, _ string) error {
			cancel2()
			return hCtx.Err()
		}
		logBuf.Reset()
		c.handleNATSMessage(ctx2, &nats.Msg{Subject: SubjectPaymentSucceeded, Data: body})
		if strings.Contains(logBuf.String(), "failed to ack") || strings.Contains(logBuf.String(), "failed to nak") {
			t.Fatalf("P0-2/P1-4: shutdown handleNATSMessage must neither Ack nor Nak, got logs: %s", logBuf.String())
		}
	})

	t.Run("DLQWriteFails_ReturnsNakNeverAck_IncludingMalformed", func(t *testing.T) {
		var logBuf bytes.Buffer
		log := logger.New("order-test", logger.WithWriter(&logBuf))
		dlqStore := &stubDLQStore{err: errors.New("postgres down")}
		dlqPub := &recordingPublisher{err: nats.ErrDisconnected}
		h := &shutdownTestHandler{
			onSucceeded: func(_ context.Context, _ string) error {
				return errors.New("poison handler error")
			},
		}
		c := NewConsumer(nil, dlqPub, dlqStore, h, log, "NEXUSMART")

		cases := []struct {
			name    string
			payload []byte
			attempt uint64
		}{
			{"Attempt5HandlerError", makeTestPaymentEvent(t, "evt-dlq-fail-5"), defaultMaxDeliverAttempts},
			{"MalformedJSON", []byte("{not-json"), 1},
			{"MissingEventID", []byte(`{"event_type":"payment.succeeded","payload":{}}`), 1},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				action, err := c.ProcessRawMessage(context.Background(), SubjectPaymentSucceeded, tc.payload, nil, tc.attempt)
				if err == nil || action != ActionNak {
					t.Fatalf("P0-2 (%s): when both DLQ sinks fail, must return ActionNak (never ActionDLQ/Ack), got action=%v err=%v", tc.name, action, err)
				}
				// Verify handleNATSMessage attempts NakWithDelay (not Ack!).
				logBuf.Reset()
				c.handleNATSMessage(context.Background(), &nats.Msg{Subject: SubjectPaymentSucceeded, Data: tc.payload})
				if strings.Contains(logBuf.String(), "failed to ack") {
					t.Fatalf("P0-2 (%s): handleNATSMessage attempted Ack despite DLQ failure! logs=%s", tc.name, logBuf.String())
				}
				if !strings.Contains(logBuf.String(), "failed to nak") {
					t.Fatalf("P0-2 (%s): handleNATSMessage should attempt NakWithDelay when DLQ fails, logs=%s", tc.name, logBuf.String())
				}
			})
		}
	})

	t.Run("DLQOneSinkSucceedsWithExpiredHandlerDeadline_UsesWithoutCancel", func(t *testing.T) {
		dlqStore := &stubDLQStore{}
		dlqPub := &recordingPublisher{err: nats.ErrTimeout} // NATS DLQ down, DB DLQ succeeds
		expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		h := &shutdownTestHandler{
			onSucceeded: func(hCtx context.Context, _ string) error {
				return hCtx.Err() // context.DeadlineExceeded (NOT shutdown context.Canceled)
			},
		}
		c := NewConsumer(nil, dlqPub, dlqStore, h, logger.New("order-test", logger.WithWriter(&bytes.Buffer{})), "NEXUSMART")
		action, err := c.ProcessRawMessage(expiredCtx, SubjectPaymentSucceeded, makeTestPaymentEvent(t, "evt-dl-exp"), nil, defaultMaxDeliverAttempts)
		if err == nil || action != ActionDLQ {
			t.Fatalf("expected ActionDLQ when DB DLQ succeeds via WithoutCancel, got action=%v err=%v", action, err)
		}
		if len(dlqStore.records) != 1 || dlqStore.lastCtx != nil {
			t.Fatalf("expected 1 DLQ record saved with live detached ctx (err=nil), got records=%d lastCtx=%v", len(dlqStore.records), dlqStore.lastCtx)
		}
	})

	t.Run("StopWaitsInFlightAndStoppingSkipsHandlerWithoutNak", func(t *testing.T) {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		var logBuf bytes.Buffer
		log := logger.New("order-test", logger.WithWriter(&logBuf))
		h := &shutdownTestHandler{
			onSucceeded: func(_ context.Context, _ string) error {
				started <- struct{}{}
				<-release
				return nil
			},
		}
		c := NewConsumer(nil, nil, nil, h, log, "NEXUSMART")
		msg := &nats.Msg{Subject: SubjectPaymentSucceeded, Data: makeTestPaymentEvent(t, "evt-inflight-1")}

		go c.onMessage(context.Background(), msg)
		<-started

		stopDone := make(chan error, 1)
		go func() {
			stopDone <- c.Stop()
		}()

		select {
		case err := <-stopDone:
			t.Fatalf("Consumer.Stop returned early while handler was still in-flight (err=%v)", err)
		case <-time.After(60 * time.Millisecond):
		}

		close(release)
		select {
		case err := <-stopDone:
			if err != nil {
				t.Fatalf("Consumer.Stop returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Consumer.Stop did not return after in-flight handler completed")
		}

		// Delivery while stopping MUST skip handler AND NOT Nak.
		logBuf.Reset()
		c.onMessage(context.Background(), msg)
		if got := h.calls.Load(); got != 1 {
			t.Fatalf("P1-4: stopping consumer must not invoke handler, got %d calls", got)
		}
		if logBuf.Len() != 0 {
			t.Fatalf("P1-4: stopping consumer must return quietly without Ack/Nak, got logs: %s", logBuf.String())
		}

		// Delivery with canceled ctx (even before Stop) MUST skip handler AND NOT Nak.
		c2 := NewConsumer(nil, nil, nil, &shutdownTestHandler{}, log, "NEXUSMART")
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		c2.onMessage(canceledCtx, msg)
		if got := c2.handler.(*shutdownTestHandler).calls.Load(); got != 0 {
			t.Fatalf("P1-4: canceled ctx must skip handler, got %d calls", got)
		}
	})
}
