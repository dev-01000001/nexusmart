package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	chatmetrics "github.com/nexusmart/core/services/chat/internal/metrics"
	"github.com/redis/go-redis/v9"
)

func TestHubMultiInstancePubSubAndDeduplication(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mA := chatmetrics.New(nil)
	mB := chatmetrics.New(nil)
	hubA := New(rdb, "inst-a", mA, nil)
	hubB := New(rdb, "inst-b", mB, nil)

	go hubA.Run(ctx)
	go hubB.Run(ctx)

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	if err := hubA.WaitSubscribed(waitCtx); err != nil {
		t.Fatalf("hubA subscribe: %v", err)
	}
	if err := hubB.WaitSubscribed(waitCtx); err != nil {
		t.Fatalf("hubB subscribe: %v", err)
	}

	buyerClient := NewClient("c-buyer", "buyer-1", "")
	sellerClient := NewClient("c-seller", "seller-1", "shop-1")
	bothClient := NewClient("c-both", "buyer-1", "shop-1") // Matches both buyer and shop: must receive ONCE!
	unrelatedClient := NewClient("c-other", "buyer-2", "shop-2")

	hubA.Register(buyerClient)
	hubA.Register(bothClient)
	hubB.Register(sellerClient)
	hubB.Register(unrelatedClient)

	if hubA.ActiveConnections() != 2 || hubB.ActiveConnections() != 2 {
		t.Fatalf("unexpected active counts: A=%d B=%d", hubA.ActiveConnections(), hubB.ActiveConnections())
	}

	frame := json.RawMessage(`{"type":"message","body":"hello across instances"}`)
	if err := hubA.Publish(ctx, Event{
		Type:    EventTypeMessage,
		BuyerID: "buyer-1",
		ShopID:  "shop-1",
		Frame:   frame,
	}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	// Both buyer on inst-A and seller on inst-B must receive the event.
	for name, c := range map[string]*Client{"buyer": buyerClient, "seller": sellerClient, "both": bothClient} {
		select {
		case got := <-c.Send:
			if string(got) != string(frame) {
				t.Errorf("%s got %s, want %s", name, string(got), string(frame))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s frame", name)
		}
	}

	// Ensure deduplication for bothClient and isolation for unrelatedClient.
	select {
	case dup := <-bothClient.Send:
		t.Fatalf("expected deduplicated single frame for bothClient, got duplicate %s", string(dup))
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case leaked := <-unrelatedClient.Send:
		t.Fatalf("unrelated client should never receive event, got %s", string(leaked))
	case <-time.After(50 * time.Millisecond):
	}

	// Slow consumer bounded buffer must not block hub publish.
	slow := NewClient("c-slow", "buyer-slow", "")
	hubA.Register(slow)
	for i := 0; i < DefaultClientBuffer+20; i++ {
		hubA.dispatch(Event{Type: EventTypeMessage, BuyerID: "buyer-slow", Frame: frame})
	}
	if len(slow.Send) != DefaultClientBuffer {
		t.Fatalf("expected bounded buffer %d, got %d", DefaultClientBuffer, len(slow.Send))
	}

	// Idempotent unregister decrements gauge cleanly.
	hubA.Unregister(slow)
	hubA.Unregister(slow)
	if hubA.ActiveConnections() != 2 {
		t.Fatalf("expected active count 2 after double unregister, got %d", hubA.ActiveConnections())
	}
}
