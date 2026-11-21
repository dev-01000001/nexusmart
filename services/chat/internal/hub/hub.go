package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	chatmetrics "github.com/nexusmart/core/services/chat/internal/metrics"
	"github.com/redis/go-redis/v9"
)

const (
	// DefaultChannel is the shared Redis Pub/Sub channel used across Chat instances.
	DefaultChannel = "chat:events"

	// DefaultClientBuffer caps queued outbound frames per WS connection so slow
	// consumers never stall hub fan-out.
	DefaultClientBuffer = 64

	EventTypeMessage     = "message"
	EventTypeReadReceipt = "read_receipt"
)

// Event is the envelope published on Redis Pub/Sub to fan out messages and read receipts
// across horizontally scaled Chat instances.
type Event struct {
	Type       string          `json:"type"`
	BuyerID    string          `json:"buyer_id"`
	ShopID     string          `json:"shop_id"`
	InstanceID string          `json:"instance_id"`
	Frame      json.RawMessage `json:"frame"`
}

// Client represents a single live WebSocket connection registered with the Hub.
type Client struct {
	ID     string
	UserID string
	ShopID string
	Send   chan []byte
}

// NewClient constructs a Hub client with a bounded outbound send buffer.
func NewClient(id, userID, shopID string) *Client {
	return &Client{
		ID:     id,
		UserID: userID,
		ShopID: shopID,
		Send:   make(chan []byte, DefaultClientBuffer),
	}
}

// Hub maintains O(1) local WebSocket connection indexes by user_id and shop_id and
// synchronizes real-time events across multiple Chat instances via one shared Redis Pub/Sub channel.
//
// ponytail: all chat instances subscribe to a single Redis Pub/Sub channel ("chat:events").
// Ceiling: ~5,000-10,000 events/sec cluster-wide before every instance spends excessive CPU
// filtering events for conversations without local sockets.
// Upgrade path: shard into N channels ("chat:events:<hash(shop_id)%N>") or migrate to Redis
// Sharded Pub/Sub (SPUBLISH/SSUBSCRIBE) / NATS subjects scoped per shop_id and user_id.
type Hub struct {
	rdb        *redis.Client
	channel    string
	instanceID string
	log        *slog.Logger
	metrics    *chatmetrics.Metrics

	mu     sync.RWMutex
	byUser map[string]map[*Client]struct{}
	byShop map[string]map[*Client]struct{}
	active int

	subOnce  sync.Once
	subReady chan struct{}
}

// New creates a Hub. rdb may be nil in local-only unit tests (events dispatch directly in-process).
func New(rdb *redis.Client, instanceID string, m *chatmetrics.Metrics, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		rdb:        rdb,
		channel:    DefaultChannel,
		instanceID: instanceID,
		log:        log,
		metrics:    m,
		byUser:     make(map[string]map[*Client]struct{}),
		byShop:     make(map[string]map[*Client]struct{}),
		subReady:   make(chan struct{}),
	}
}

// InstanceID returns the unique identifier of this Chat instance.
func (h *Hub) InstanceID() string {
	return h.instanceID
}

// ActiveConnections returns the number of currently registered local WebSocket clients.
func (h *Hub) ActiveConnections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.active
}

// WaitSubscribed blocks until the Redis Pub/Sub subscription handshake succeeds or ctx expires.
func (h *Hub) WaitSubscribed(ctx context.Context) error {
	if h.rdb == nil {
		return nil
	}
	select {
	case <-h.subReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Register indexes c by UserID and ShopID in O(1) and updates the active gauge.
func (h *Hub) Register(c *Client) {
	if c == nil {
		return
	}
	h.mu.Lock()
	alreadyPresent := false
	if c.UserID != "" {
		set := h.byUser[c.UserID]
		if set == nil {
			set = make(map[*Client]struct{})
			h.byUser[c.UserID] = set
		}
		if _, exists := set[c]; exists {
			alreadyPresent = true
		}
		set[c] = struct{}{}
	}
	if c.ShopID != "" {
		set := h.byShop[c.ShopID]
		if set == nil {
			set = make(map[*Client]struct{})
			h.byShop[c.ShopID] = set
		}
		if _, exists := set[c]; exists {
			alreadyPresent = true
		}
		set[c] = struct{}{}
	}
	if !alreadyPresent {
		h.active++
		if h.metrics != nil {
			h.metrics.WSConnectionsActive.Set(float64(h.active))
		}
	}
	h.mu.Unlock()
}

// Unregister removes c from all indexes idempotently and updates the active gauge.
func (h *Hub) Unregister(c *Client) {
	if c == nil {
		return
	}
	h.mu.Lock()
	removed := false
	if c.UserID != "" {
		if set, ok := h.byUser[c.UserID]; ok {
			if _, exists := set[c]; exists {
				delete(set, c)
				removed = true
			}
			if len(set) == 0 {
				delete(h.byUser, c.UserID)
			}
		}
	}
	if c.ShopID != "" {
		if set, ok := h.byShop[c.ShopID]; ok {
			if _, exists := set[c]; exists {
				delete(set, c)
				removed = true
			}
			if len(set) == 0 {
				delete(h.byShop, c.ShopID)
			}
		}
	}
	if removed && h.active > 0 {
		h.active--
		if h.metrics != nil {
			h.metrics.WSConnectionsActive.Set(float64(h.active))
		}
	}
	h.mu.Unlock()
}

// Publish sends ev to Redis Pub/Sub (or dispatches locally if Redis is not configured).
func (h *Hub) Publish(ctx context.Context, ev Event) error {
	if ev.InstanceID == "" {
		ev.InstanceID = h.instanceID
	}
	if h.metrics != nil {
		h.metrics.PubSubEventsTotal.WithLabelValues("publish", ev.Type).Inc()
	}
	if h.rdb == nil {
		h.dispatch(ev)
		return nil
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return h.rdb.Publish(ctx, h.channel, raw).Err()
}

// Run maintains ONE shared Redis Pub/Sub subscription with exponential reconnect backoff
// until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	if h.rdb == nil {
		h.subOnce.Do(func() { close(h.subReady) })
		<-ctx.Done()
		return
	}

	backoff := 200 * time.Millisecond
	const maxBackoff = 5 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}
		err := h.subscribeLoop(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			h.log.Warn("redis pubsub disconnected; reconnecting",
				slog.String("channel", h.channel),
				slog.Duration("backoff", backoff),
				slog.String("error", err.Error()),
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (h *Hub) subscribeLoop(ctx context.Context) error {
	ps := h.rdb.Subscribe(ctx, h.channel)
	defer func() { _ = ps.Close() }()

	// Confirm subscription handshake before reading messages.
	if _, err := ps.Receive(ctx); err != nil {
		return err
	}
	h.subOnce.Do(func() { close(h.subReady) })

	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return errors.New("redis pubsub channel closed")
			}
			var ev Event
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				h.log.Warn("ignoring malformed pubsub event", slog.String("error", err.Error()))
				continue
			}
			if h.metrics != nil {
				h.metrics.PubSubEventsTotal.WithLabelValues("receive", ev.Type).Inc()
			}
			h.dispatch(ev)
		}
	}
}

func (h *Hub) dispatch(ev Event) {
	if len(ev.Frame) == 0 {
		return
	}

	h.mu.RLock()
	targets := make(map[*Client]struct{})
	if ev.BuyerID != "" {
		for c := range h.byUser[ev.BuyerID] {
			targets[c] = struct{}{}
		}
	}
	if ev.ShopID != "" {
		for c := range h.byShop[ev.ShopID] {
			targets[c] = struct{}{}
		}
	}
	h.mu.RUnlock()

	for c := range targets {
		select {
		case c.Send <- ev.Frame:
		default:
			if h.metrics != nil {
				h.metrics.WSDroppedTotal.Inc()
			}
		}
	}
}
