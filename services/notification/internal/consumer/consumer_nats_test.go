package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/notification/internal/service"
)

func requireNATSURL(t *testing.T) string {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if url == "" {
		t.Skip("TEST_NATS_URL not set; run via scripts/test-db.sh")
	}
	return url
}

func TestConsumerRestartTwiceDoesNotReplayStreamOrDeleteDurable(t *testing.T) {
	natsURL := requireNATSURL(t)
	streamName := fmt.Sprintf("NEXUSMART_TEST_%d", time.Now().UnixNano())
	dlqStreamName := streamName + "_DLQ"
	subjectPrefix := fmt.Sprintf("notiftest.%d", time.Now().UnixNano())

	adminNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect admin nats: %v", err)
	}
	adminJS, err := adminNC.JetStream()
	if err != nil {
		adminNC.Close()
		t.Fatalf("admin jetstream: %v", err)
	}
	t.Cleanup(func() {
		_ = adminJS.DeleteStream(streamName)
		_ = adminJS.DeleteStream(dlqStreamName)
		adminNC.Close()
	})
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subjectPrefix + ".>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create test stream: %v", err)
	}
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      dlqStreamName,
		Subjects:  []string{"dlq." + subjectPrefix + ".>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create test dlq stream: %v", err)
	}

	publishEvent := func(eventID string) {
		t.Helper()
		raw, _ := json.Marshal(service.Envelope{
			EventID:   eventID,
			EventType: "order.created",
			Payload:   json.RawMessage(`{"order_id":"ord-1","buyer_id":"buyer-1"}`),
		})
		if _, err := adminJS.Publish(subjectPrefix+".order.created", raw); err != nil {
			t.Fatalf("publish %s: %v", eventID, err)
		}
	}

	var mu sync.Mutex
	counts := make(map[string]int)
	var totalHandled atomic.Int32
	receivedCh := make(chan string, 16)

	proc := stubProcessor{fn: func(_ context.Context, _ string, env service.Envelope) (bool, error) {
		mu.Lock()
		counts[env.EventID]++
		mu.Unlock()
		totalHandled.Add(1)
		receivedCh <- env.EventID
		return false, nil
	}}

	waitForEvent := func(wantID string) {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case got := <-receivedCh:
				if got == wantID {
					return
				}
			case <-timeout:
				t.Fatalf("timed out waiting for event %s", wantID)
			}
		}
	}

	startInstance := func() (*nats.Conn, context.CancelFunc, *Consumer) {
		t.Helper()
		nc, connErr := nats.Connect(natsURL)
		if connErr != nil {
			t.Fatalf("connect consumer nats: %v", connErr)
		}
		js, jsErr := nc.JetStream()
		if jsErr != nil {
			nc.Close()
			t.Fatalf("consumer jetstream: %v", jsErr)
		}
		ctx, cancel := context.WithCancel(context.Background())
		c := NewConsumer(js, proc, nil, streamName, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if startErr := c.Start(ctx); startErr != nil {
			cancel()
			nc.Close()
			t.Fatalf("consumer start: %v", startErr)
		}
		return nc, cancel, c
	}

	// Boot 1: publish 2 events, consume them, then shut down gracefully.
	publishEvent("evt-boot1-a")
	publishEvent("evt-boot1-b")
	nc1, cancel1, c1 := startInstance()
	waitForEvent("evt-boot1-a")
	waitForEvent("evt-boot1-b")
	// Give async Ack a moment before shutdown; Close() must preserve durable consumer
	// even if nc1 remains open briefly (sub.Drain() asynchronously calls DeleteConsumer!).
	time.Sleep(100 * time.Millisecond)
	cancel1()
	if err := c1.Close(); err != nil {
		t.Fatalf("boot 1 Close: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := adminJS.ConsumerInfo(streamName, "notification-events-v1"); err != nil {
		t.Fatalf("E1: durable consumer notification-events-v1 was deleted by Close() while nc still open: %v", err)
	}
	nc1.Close()

	// Durable consumer MUST STILL EXIST on server after Close() + nc.Close()!
	info1, err := adminJS.ConsumerInfo(streamName, "notification-events-v1")
	if err != nil {
		t.Fatalf("E1: durable consumer notification-events-v1 must NOT be deleted on Close(): %v", err)
	}
	if info1.AckFloor.Stream < 2 {
		t.Fatalf("E1: expected AckFloor.Stream >= 2 after boot 1, got %+v", info1.AckFloor)
	}

	// Boot 2 (restart #1): must NOT replay boot 1 events; only process new evt-boot2-c.
	nc2, cancel2, c2 := startInstance()
	publishEvent("evt-boot2-c")
	waitForEvent("evt-boot2-c")
	time.Sleep(150 * time.Millisecond)
	cancel2()
	if err := c2.Close(); err != nil {
		t.Fatalf("boot 2 Close: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	nc2.Close()

	if _, err := adminJS.ConsumerInfo(streamName, "notification-events-v1"); err != nil {
		t.Fatalf("E1: durable consumer missing after restart #1: %v", err)
	}

	// Boot 3 (restart #2): must NOT replay boot 1 or boot 2 events; only process evt-boot3-d.
	nc3, cancel3, c3 := startInstance()
	publishEvent("evt-boot3-d")
	waitForEvent("evt-boot3-d")
	time.Sleep(150 * time.Millisecond)
	cancel3()
	if err := c3.Close(); err != nil {
		t.Fatalf("boot 3 Close: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	nc3.Close()

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"evt-boot1-a", "evt-boot1-b", "evt-boot2-c", "evt-boot3-d"} {
		if counts[id] != 1 {
			t.Errorf("E1: event %s handled %d times across 2 restarts, want exactly 1 (counts=%v)", id, counts[id], counts)
		}
	}
	if got := totalHandled.Load(); got != 4 {
		t.Fatalf("E1: expected total 4 handled messages across 2 restarts, got %d (counts=%v)", got, counts)
	}
}

func TestConsumerRealNATSInFlightShutdownWaitAndNoDLQ(t *testing.T) {
	natsURL := requireNATSURL(t)
	streamName := fmt.Sprintf("NEXUSMART_SHUT_%d", time.Now().UnixNano())
	subjectPrefix := fmt.Sprintf("shuttest.%d", time.Now().UnixNano())

	adminNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect admin nats: %v", err)
	}
	adminJS, err := adminNC.JetStream()
	if err != nil {
		adminNC.Close()
		t.Fatalf("admin jetstream: %v", err)
	}
	t.Cleanup(func() {
		_ = adminJS.DeleteStream(streamName)
		adminNC.Close()
	})
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subjectPrefix + ".>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	dlq := &stubDLQ{}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect consumer nats: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("consumer jetstream: %v", err)
	}

	c := NewConsumer(
		js,
		stubProcessor{fn: func(ctx context.Context, _ string, _ service.Envelope) (bool, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, errors.New("shutdown induced failure")
		}},
		nil,
		streamName,
		1, // maxAttempts=1 so any non-shutdown error would immediately DLQ
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	c.dlqStore = dlq

	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start consumer: %v", err)
	}

	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-inflight-shutdown",
		EventType: "order.created",
		Payload:   json.RawMessage(`{"order_id":"ord-1","buyer_id":"b-1"}`),
	})
	if _, err := adminJS.Publish(subjectPrefix+".order.created", raw); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for in-flight handler to start")
	}

	// Cancel context and call Close(): Close() MUST wait until in-flight handler exits.
	cancel()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- c.Close()
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("E1: Close() returned early while in-flight handler was still running (err=%v)", err)
	case <-time.After(80 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return after in-flight handler finished")
	}

	if len(dlq.records) != 0 {
		t.Fatalf("E1: shutdown-canceled handler must NOT write to dead_letter_events, got %d", len(dlq.records))
	}
}

type failFirstDLQPublisher struct {
	calls atomic.Int32
	inner DLQPublisher
}

func (f *failFirstDLQPublisher) PublishDLQ(ctx context.Context, subject string, data []byte) error {
	if f.calls.Add(1) == 1 {
		return errors.New("transient nats dlq failure on attempt 1")
	}
	return f.inner.PublishDLQ(ctx, subject, data)
}

func TestConsumerRealNATSDLQRetryWithMaxDeliverHeadroomAndSanitization(t *testing.T) {
	natsURL := requireNATSURL(t)
	streamName := fmt.Sprintf("NEXUSMART_DLQTEST_%d", time.Now().UnixNano())
	dlqStreamName := streamName + "_DLQ"

	adminNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect admin nats: %v", err)
	}
	adminJS, err := adminNC.JetStream()
	if err != nil {
		adminNC.Close()
		t.Fatalf("admin jetstream: %v", err)
	}
	t.Cleanup(func() {
		_ = adminJS.DeleteStream(streamName)
		_ = adminJS.DeleteStream(dlqStreamName)
		adminNC.Close()
	})
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"user.phone_otp_requested"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      dlqStreamName,
		Subjects:  []string{"dlq.user.phone_otp_requested"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create dlq stream: %v", err)
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect consumer nats: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("consumer jetstream: %v", err)
	}

	// Attempt 1 fails BOTH DB DLQ and NATS DLQ -> consumer MUST NakWithDelay (not Ack).
	// Because MaxDeliver = maxAttempts + 1 (= 2), JetStream redelivers attempt 2, where real
	// jsDLQPublisher publishes to dlq.user.phone_otp_requested with PubAck and primary msg is Acked!
	var attempts atomic.Int32
	dlqStore := &stubDLQ{storeErr: errors.New("db unavailable")}
	flakyPub := &failFirstDLQPublisher{inner: jsDLQPublisher{js: js}}

	c := NewConsumer(
		js,
		stubProcessor{fn: func(_ context.Context, _ string, _ service.Envelope) (bool, error) {
			attempts.Add(1)
			return false, errors.New("permanent smtp error")
		}},
		nil,
		streamName,
		1, // maxAttempts=1 -> MaxDeliver=2 (headroom for 1 retry after dual DLQ sink failure)
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	c.dlqStore = dlqStore
	c.dlqPub = flakyPub

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	defer func() { _ = c.Close() }()

	secretOTP := "618293"
	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-real-dlq-headroom",
		EventType: "user.phone_otp_requested",
		Payload:   json.RawMessage(`{"user_id":"u-1","email":"u1@nexusmart.local","phone_masked":"090****123","otp":"` + secretOTP + `"}`),
	})
	if _, err := adminJS.Publish("user.phone_otp_requested", raw); err != nil {
		t.Fatalf("publish phone otp event: %v", err)
	}

	// Wait for attempt 2 redelivery to succeed in publishing to dlq.user.phone_otp_requested and Acking primary message.
	deadline := time.Now().Add(8 * time.Second)
	var dlqMsg *nats.RawStreamMsg
	for time.Now().Before(deadline) {
		msg, getErr := adminJS.GetMsg(dlqStreamName, 1)
		if getErr == nil && msg != nil {
			dlqMsg = msg
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dlqMsg == nil {
		t.Fatalf("E1: expected message to reach %s on MaxDeliver headroom retry (attempts=%d)", dlqStreamName, attempts.Load())
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("E1: expected exactly 2 delivery attempts (1st Nak on dual DLQ fail, 2nd Ack on DLQ recovery), got %d", got)
	}
	if dlqMsg.Subject != "dlq.user.phone_otp_requested" {
		t.Fatalf("unexpected DLQ subject: %q", dlqMsg.Subject)
	}
	if strings.Contains(string(dlqMsg.Data), secretOTP) {
		t.Fatalf("SECURITY: phone OTP leaked into real JetStream DLQ message: %s", string(dlqMsg.Data))
	}
	if !strings.Contains(string(dlqMsg.Data), "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in JetStream DLQ message, got: %s", string(dlqMsg.Data))
	}
}

func TestConsumerRealNATSB1ProductEventsAndMalformedAddressUpdatedDLQ(t *testing.T) {
	natsURL := requireNATSURL(t)
	streamName := fmt.Sprintf("NEXUSMART_B1G3_%d", time.Now().UnixNano())
	dlqStreamName := streamName + "_DLQ"

	adminNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect admin nats: %v", err)
	}
	adminJS, err := adminNC.JetStream()
	if err != nil {
		adminNC.Close()
		t.Fatalf("admin jetstream: %v", err)
	}
	t.Cleanup(func() {
		_ = adminJS.DeleteStream(streamName)
		_ = adminJS.DeleteStream(dlqStreamName)
		adminNC.Close()
	})
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"product.>", "order.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      dlqStreamName,
		Subjects:  []string{"dlq.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create dlq stream: %v", err)
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect consumer nats: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("consumer jetstream: %v", err)
	}

	repo := newNoopConsumerRepo()
	svc := service.NewNotificationService(repo, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dlqStore := &stubDLQ{}

	c := NewConsumer(js, svc, nil, streamName, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.dlqStore = dlqStore

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	defer func() { _ = c.Close() }()

	// 1. Publish B1 product events -> all 3 must be Acked cleanly with 0 DLQ rows/messages.
	b1Events := []struct {
		subject string
		eventID string
		payload string
	}{
		{"product.upserted", "evt-nats-b1-upsert", `{"product_id":"p1","shop_id":"s1","name":"Áo Polo","min_price_vnd":199000,"status":"APPROVED"}`},
		{"product.deleted", "evt-nats-b1-delete", `{"product_id":"p1","shop_id":"s1","reason":"HIDDEN"}`},
		{"product.category_updated", "evt-nats-b1-cat", `{"category_id":"c1","name":"Áo Nam","action":"UPSERT"}`},
	}
	for _, ev := range b1Events {
		raw, _ := json.Marshal(service.Envelope{
			EventID:   ev.eventID,
			EventType: ev.subject,
			Payload:   json.RawMessage(ev.payload),
		})
		if _, err := adminJS.Publish(ev.subject, raw); err != nil {
			t.Fatalf("publish %s: %v", ev.subject, err)
		}
	}

	// 2. Publish malformed order.address_updated (missing shop_id, with rogue street_address/phone) ->
	// must route to dlq.order.address_updated + dead_letter_events with PII redacted.
	badAddrRaw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-nats-addr-bad",
		EventType: "order.address_updated",
		Payload:   json.RawMessage(`{"order_id":"ord-bad-nats","buyer_id":"b-1","street_address":"77 Hai Bà Trưng","recipient_phone":"0905554444"}`),
	})
	if _, err := adminJS.Publish("order.address_updated", badAddrRaw); err != nil {
		t.Fatalf("publish malformed order.address_updated: %v", err)
	}

	// Wait until all 4 messages are acknowledged on primary stream AND the malformed event appears in DLQ stream.
	deadline := time.Now().Add(8 * time.Second)
	var dlqMsg *nats.RawStreamMsg
	for time.Now().Before(deadline) {
		info, infoErr := adminJS.ConsumerInfo(streamName, "notification-events-v1")
		msg, getErr := adminJS.GetMsg(dlqStreamName, 1)
		if infoErr == nil && info.AckFloor.Stream >= 4 && getErr == nil && msg != nil {
			dlqMsg = msg
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dlqMsg == nil {
		t.Fatalf("expected all 4 events acked and 1 malformed address event in %s", dlqStreamName)
	}

	// Ensure ONLY the 1 malformed order.address_updated event entered DLQ (0 B1 events in DLQ).
	dlqInfo, err := adminJS.StreamInfo(dlqStreamName)
	if err != nil {
		t.Fatalf("dlq stream info: %v", err)
	}
	if dlqInfo.State.Msgs != 1 {
		t.Fatalf("expected exactly 1 DLQ message (malformed order.address_updated), got %d", dlqInfo.State.Msgs)
	}
	if dlqMsg.Subject != "dlq.order.address_updated" {
		t.Fatalf("unexpected DLQ subject: %q", dlqMsg.Subject)
	}
	for _, secret := range []string{"77 Hai Bà Trưng", "0905554444"} {
		if strings.Contains(string(dlqMsg.Data), secret) {
			t.Fatalf("SECURITY: PII %q leaked into real JetStream DLQ: %s", secret, string(dlqMsg.Data))
		}
	}
	if !strings.Contains(string(dlqMsg.Data), "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in real JetStream DLQ message, got: %s", string(dlqMsg.Data))
	}

	dlqStore.mu.Lock()
	dbDLQCount := len(dlqStore.records)
	dlqStore.mu.Unlock()
	if dbDLQCount != 1 {
		t.Fatalf("expected exactly 1 dead_letter_events row (0 for B1 events), got %d", dbDLQCount)
	}
	for _, ev := range b1Events {
		if ok, _ := repo.IsEventProcessed(context.Background(), ev.eventID); !ok {
			t.Errorf("expected B1 event %s processed cleanly", ev.eventID)
		}
	}
}
