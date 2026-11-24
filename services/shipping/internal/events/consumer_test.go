package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/logger"
)

type consumerStubStore struct {
	mu          sync.Mutex
	processed   map[string]bool
	dlqErr      error
	lastDLQCtx  error
	dlqHasDeadl bool
	dlqRecords  []dlqRecord
}

type dlqRecord struct {
	eventID  string
	subject  string
	payload  []byte
	errMsg   string
	attempts int
}

func newConsumerStubStore() *consumerStubStore {
	return &consumerStubStore{processed: make(map[string]bool)}
}

func (s *consumerStubStore) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processed[eventID], nil
}

func (s *consumerStubStore) InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastDLQCtx = ctx.Err()
	_, s.dlqHasDeadl = ctx.Deadline()
	if s.dlqErr != nil {
		return s.dlqErr
	}
	s.dlqRecords = append(s.dlqRecords, dlqRecord{
		eventID:  eventID,
		subject:  subject,
		payload:  append([]byte(nil), payload...),
		errMsg:   errMsg,
		attempts: attempts,
	})
	return nil
}

type consumerStubJS struct {
	nats.JetStreamContext
	pub *recordingPublisher
}

func (f *consumerStubJS) PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	return f.pub.PublishMsg(m, opts...)
}

type cancelTestHandler struct {
	mu       sync.Mutex
	fn       func(ctx context.Context, eventID, orderID, reason string) error
	calls    atomic.Int32
	lastArgs [3]string
}

func (h *cancelTestHandler) HandleOrderCancelled(ctx context.Context, eventID, orderID, reason string) error {
	h.calls.Add(1)
	h.mu.Lock()
	h.lastArgs = [3]string{eventID, orderID, reason}
	fn := h.fn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, eventID, orderID, reason)
	}
	return nil
}

func makeOrderCancelledMsg(t *testing.T, eventID, orderID, reason string) []byte {
	t.Helper()
	payload, err := json.Marshal(OrderCancelledPayload{
		OrderID: orderID,
		Reason:  reason,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := json.Marshal(Envelope{
		EventID:   eventID,
		EventType: SubjectOrderCancelled,
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func TestConsumer_E1_ShutdownDLQAndNoDrain(t *testing.T) {
	t.Run("CanceledCtxAtMaxAttempts_NoDLQAndNoAckOrNak", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		st := newConsumerStubStore()
		pub := &recordingPublisher{}
		js := &consumerStubJS{pub: pub}
		h := &cancelTestHandler{
			fn: func(hCtx context.Context, _, _, _ string) error {
				cancel()
				return hCtx.Err()
			},
		}
		var logBuf bytes.Buffer
		log := logger.New("shipping-test", logger.WithWriter(&logBuf))
		c := NewConsumer(js, st, h, "NEXUSMART", defaultConsumerMaxAttempts, log)

		raw := makeOrderCancelledMsg(t, "evt-cancel-shutdown", "ord-1", "buyer")
		action, err := c.ProcessRawMessage(ctx, SubjectOrderCancelled, raw, nil, defaultConsumerMaxAttempts)
		if err == nil || action != ActionNak {
			t.Fatalf("E1: canceled ctx at maxAttempts must return ActionNak, got action=%v err=%v", action, err)
		}
		st.mu.Lock()
		dlqCount := len(st.dlqRecords)
		st.mu.Unlock()
		pub.mu.Lock()
		pubCount := len(pub.msgs)
		pub.mu.Unlock()
		if dlqCount != 0 || pubCount != 0 {
			t.Fatalf("E1: canceled ctx during shutdown must NOT write DLQ (db=%d, nats=%d)", dlqCount, pubCount)
		}

		// handleNATSMessage with canceled baseCtx must neither Ack nor Nak.
		ctx2, cancel2 := context.WithCancel(context.Background())
		h.mu.Lock()
		h.fn = func(hCtx context.Context, _, _, _ string) error {
			cancel2()
			return hCtx.Err()
		}
		h.mu.Unlock()
		logBuf.Reset()
		c.handleNATSMessage(ctx2, &nats.Msg{Subject: SubjectOrderCancelled, Data: raw})
		if strings.Contains(logBuf.String(), "failed to ack") || strings.Contains(logBuf.String(), "failed to nak") {
			t.Fatalf("E1: shutdown handleNATSMessage must neither Ack nor Nak, got logs: %s", logBuf.String())
		}
	})

	t.Run("BothDLQSinksFail_ReturnsNakNeverAck_IncludingMalformedAndMissingFields", func(t *testing.T) {
		st := newConsumerStubStore()
		st.dlqErr = errors.New("postgres unavailable")
		pub := &recordingPublisher{globalErr: nats.ErrDisconnected}
		js := &consumerStubJS{pub: pub}
		h := &cancelTestHandler{
			fn: func(_ context.Context, _, _, _ string) error {
				return errors.New("poison handler error")
			},
		}
		var logBuf bytes.Buffer
		log := logger.New("shipping-test", logger.WithWriter(&logBuf))
		c := NewConsumer(js, st, h, "NEXUSMART", defaultConsumerMaxAttempts, log)

		cases := []struct {
			name    string
			data    []byte
			attempt uint64
		}{
			{"MaxAttemptsHandlerErr", makeOrderCancelledMsg(t, "evt-dlq-fail-5", "ord-5", "buyer"), defaultConsumerMaxAttempts},
			{"MalformedJSON", []byte("{not-json"), 1},
			{"MissingEventID", []byte(`{"event_type":"order.cancelled","payload":{"order_id":"ord-1"}}`), 1},
			{"MalformedPayload", []byte(`{"event_id":"evt-bad-p","event_type":"order.cancelled","payload":"not-obj"}`), 1},
			{"MissingOrderID", []byte(`{"event_id":"evt-no-ord","event_type":"order.cancelled","payload":{"reason":"x"}}`), 1},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				action, err := c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, tc.data, nil, tc.attempt)
				if err == nil || action != ActionNak {
					t.Fatalf("E1 (%s): when both DLQ sinks fail, must return ActionNak, got action=%v err=%v", tc.name, action, err)
				}
				// handleNATSMessage must attempt NakWithDelay (never Ack) when DLQ fails.
				if tc.attempt == 1 {
					logBuf.Reset()
					c.handleNATSMessage(context.Background(), &nats.Msg{Subject: SubjectOrderCancelled, Data: tc.data})
					if strings.Contains(logBuf.String(), "failed to ack") {
						t.Fatalf("E1 (%s): handleNATSMessage attempted Ack despite DLQ failure! logs=%s", tc.name, logBuf.String())
					}
					if !strings.Contains(logBuf.String(), "failed to nak") {
						t.Fatalf("E1 (%s): handleNATSMessage must attempt NakWithDelay when DLQ fails, logs=%s", tc.name, logBuf.String())
					}
				}
			})
		}
	})

	t.Run("SingleDLQSinkSucceedsWithExpiredHandlerDeadline_UsesWithoutCancel", func(t *testing.T) {
		t.Run("DBSinkSucceeds_NATSFails", func(t *testing.T) {
			expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			st := newConsumerStubStore()
			pub := &recordingPublisher{globalErr: nats.ErrTimeout}
			js := &consumerStubJS{pub: pub}
			h := &cancelTestHandler{
				fn: func(hCtx context.Context, _, _, _ string) error {
					return hCtx.Err() // DeadlineExceeded, NOT shutdown Canceled
				},
			}
			c := NewConsumer(js, st, h, "NEXUSMART", defaultConsumerMaxAttempts, logger.New("shipping-test", logger.WithWriter(&bytes.Buffer{})))
			raw := makeOrderCancelledMsg(t, "evt-dl-db", "ord-2", "buyer")

			action, err := c.ProcessRawMessage(expiredCtx, SubjectOrderCancelled, raw, nil, defaultConsumerMaxAttempts)
			if err == nil || action != ActionDLQ {
				t.Fatalf("E1: expected ActionDLQ when DB DLQ succeeds, got action=%v err=%v", action, err)
			}
			st.mu.Lock()
			dlqCount := len(st.dlqRecords)
			lastErr := st.lastDLQCtx
			hasDL := st.dlqHasDeadl
			st.mu.Unlock()
			if dlqCount != 1 || lastErr != nil || !hasDL {
				t.Fatalf("E1: DLQ DB write must use detached bounded context, got records=%d ctxErr=%v hasDeadline=%v", dlqCount, lastErr, hasDL)
			}
			if !pub.sawPubOpt {
				t.Fatalf("E1: DLQ NATS PublishMsg must pass nats.Context(dlqCtx) PubOpt")
			}
		})

		t.Run("NATSSinkSucceeds_DBFails", func(t *testing.T) {
			expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			st := newConsumerStubStore()
			st.dlqErr = errors.New("db down")
			pub := &recordingPublisher{}
			js := &consumerStubJS{pub: pub}
			h := &cancelTestHandler{
				fn: func(hCtx context.Context, _, _, _ string) error {
					return hCtx.Err()
				},
			}
			c := NewConsumer(js, st, h, "NEXUSMART", defaultConsumerMaxAttempts, logger.New("shipping-test", logger.WithWriter(&bytes.Buffer{})))
			raw := makeOrderCancelledMsg(t, "evt-dl-nats", "ord-2b", "buyer")

			action, err := c.ProcessRawMessage(expiredCtx, SubjectOrderCancelled, raw, nil, defaultConsumerMaxAttempts)
			if err == nil || action != ActionDLQ {
				t.Fatalf("E1: expected ActionDLQ when NATS DLQ PubAck succeeds, got action=%v err=%v", action, err)
			}
			if len(pub.msgs) != 1 || pub.msgs[0].Subject != "dlq."+SubjectOrderCancelled || !pub.sawPubOpt {
				t.Fatalf("E1: expected 1 DLQ NATS msg with PubOpt, got msgs=%d sawPubOpt=%v", len(pub.msgs), pub.sawPubOpt)
			}
			if pub.msgs[0].Header.Get("X-DLQ-Event-Id") != "evt-dl-nats" {
				t.Fatalf("expected X-DLQ-Event-Id header, got %q", pub.msgs[0].Header.Get("X-DLQ-Event-Id"))
			}
		})
	})

	t.Run("CloseWaitsForInFlightHandlerAndNeverDrainsDurable", func(t *testing.T) {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		st := newConsumerStubStore()
		pub := &recordingPublisher{}
		js := &consumerStubJS{pub: pub}
		h := &cancelTestHandler{
			fn: func(_ context.Context, _, _, _ string) error {
				started <- struct{}{}
				<-release
				return nil
			},
		}
		var logBuf bytes.Buffer
		log := logger.New("shipping-test", logger.WithWriter(&logBuf))
		c := NewConsumer(js, st, h, "NEXUSMART", defaultConsumerMaxAttempts, log)
		raw := makeOrderCancelledMsg(t, "evt-inflight", "ord-3", "buyer")

		go c.onMessage(context.Background(), &nats.Msg{Subject: SubjectOrderCancelled, Data: raw})
		<-started

		closeDone := make(chan error, 1)
		go func() {
			closeDone <- c.Close()
		}()

		select {
		case err := <-closeDone:
			close(release)
			t.Fatalf("E1: Consumer.Close returned early while handler was still in-flight (err=%v)", err)
		case <-time.After(60 * time.Millisecond):
		}

		close(release)
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatalf("Consumer.Close returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Consumer.Close did not return after in-flight handler finished")
		}

		// Calling Close() again is idempotent.
		if err := c.Close(); err != nil {
			t.Fatalf("second Close() returned error: %v", err)
		}

		// Delivery while stopping must skip handler quietly without Ack/Nak.
		logBuf.Reset()
		c.onMessage(context.Background(), &nats.Msg{Subject: SubjectOrderCancelled, Data: raw})
		if got := h.calls.Load(); got != 1 {
			t.Fatalf("E1: stopping consumer must not invoke handler, got %d calls", got)
		}
		if strings.TrimSpace(logBuf.String()) != "" {
			t.Fatalf("E1: stopping consumer must return quietly without Ack/Nak, got logs: %s", logBuf.String())
		}

		// Delivery with pre-canceled ctx (even before Close) must skip handler quietly.
		h2 := &cancelTestHandler{}
		c2 := NewConsumer(js, st, h2, "NEXUSMART", defaultConsumerMaxAttempts, log)
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		c2.onMessage(canceledCtx, &nats.Msg{Subject: SubjectOrderCancelled, Data: raw})
		if got := h2.calls.Load(); got != 0 {
			t.Fatalf("E1: canceled ctx must skip handler, got %d calls", got)
		}

		// Guard via AST against calling Drain() or Unsubscribe() on durable subscriptions.
		fset := token.NewFileSet()
		fileAST, err := parser.ParseFile(fset, "consumer.go", nil, 0)
		if err != nil {
			t.Fatalf("parse consumer.go: %v", err)
		}
		ast.Inspect(fileAST, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (sel.Sel.Name == "Drain" || sel.Sel.Name == "Unsubscribe") {
				t.Fatalf("E1: consumer.go must NEVER call .%s() on durable subscriptions", sel.Sel.Name)
			}
			return true
		})
	})

	t.Run("AlreadyProcessedAndHappyPath", func(t *testing.T) {
		st := newConsumerStubStore()
		st.processed["evt-done"] = true
		h := &cancelTestHandler{}
		c := NewConsumer(nil, st, h, "NEXUSMART", defaultConsumerMaxAttempts, logger.New("shipping-test", logger.WithWriter(&bytes.Buffer{})))

		action, err := c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, makeOrderCancelledMsg(t, "evt-done", "ord-done", "dup"), nil, 1)
		if err != nil || action != ActionAck {
			t.Fatalf("already processed event must ActionAck, got action=%v err=%v", action, err)
		}
		if h.calls.Load() != 0 {
			t.Fatalf("already processed event must not invoke handler, got %d calls", h.calls.Load())
		}

		action, err = c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, makeOrderCancelledMsg(t, "evt-ok", "ord-ok", "buyer_cancelled"), nil, 1)
		if err != nil || action != ActionAck {
			t.Fatalf("happy path must ActionAck, got action=%v err=%v", action, err)
		}
		if h.calls.Load() != 1 || h.lastArgs != [3]string{"evt-ok", "ord-ok", "buyer_cancelled"} {
			t.Fatalf("unexpected handler invocation: calls=%d args=%v", h.calls.Load(), h.lastArgs)
		}
	})
}
