package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	SubjectShipmentUpdated = "shipment.updated"
	SubjectOrderCancelled  = "order.cancelled"
)

// Envelope is the canonical NexusMart NATS JetStream event wrapper (docs/contracts.md §5).
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ShipmentUpdatedPayload matches docs/contracts.md §5 for shipment.updated.
type ShipmentUpdatedPayload struct {
	ShipmentID   string `json:"shipment_id"`
	OrderID      string `json:"order_id"`
	ShopID       string `json:"shop_id"`
	CarrierCode  string `json:"carrier_code"`
	TrackingCode string `json:"tracking_code"`
	Status       string `json:"status"`
	RawStatus    string `json:"raw_status"`
	Description  string `json:"description"`
	OccurredAt   string `json:"occurred_at"`
}

// OrderCancelledPayload matches docs/contracts.md §5 for order.cancelled.
type OrderCancelledPayload struct {
	OrderID   string `json:"order_id"`
	OrderCode string `json:"order_code"`
	BuyerID   string `json:"buyer_id"`
	ShopID    string `json:"shop_id"`
	Reason    string `json:"reason"`
	WasPaid   bool   `json:"was_paid"`
}

// EnsureStreams idempotently creates the NEXUSMART and NEXUSMART_DLQ JetStream streams.
func EnsureStreams(js nats.JetStreamContext, primaryStream, dlqStream string) error {
	if primaryStream == "" {
		primaryStream = "NEXUSMART"
	}
	if dlqStream == "" {
		dlqStream = "NEXUSMART_DLQ"
	}

	if err := ensureSingleStream(js, &nats.StreamConfig{
		Name:       primaryStream,
		Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>"},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 2 * time.Minute,
	}); err != nil {
		return fmt.Errorf("ensure primary stream %s: %w", primaryStream, err)
	}

	if err := ensureSingleStream(js, &nats.StreamConfig{
		Name:      dlqStream,
		Subjects:  []string{"dlq.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		return fmt.Errorf("ensure dlq stream %s: %w", dlqStream, err)
	}
	return nil
}

func ensureSingleStream(js nats.JetStreamContext, cfg *nats.StreamConfig) error {
	if _, err := js.StreamInfo(cfg.Name); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}

	if _, err := js.AddStream(cfg); err != nil {
		// Handle concurrent creation by sibling services during startup.
		if strings.Contains(strings.ToLower(err.Error()), "already in use") {
			if _, infoErr := js.StreamInfo(cfg.Name); infoErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}
