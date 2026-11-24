package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/telemetry"
)

// Envelope matches docs/contracts.md Section 5 canonical JSON event envelope.
type Envelope struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	TraceID    string `json:"trace_id"`
	OccurredAt string `json:"occurred_at"`
	Payload    any    `json:"payload"`
}

// UserRegisteredPayload is emitted on `user.registered`.
type UserRegisteredPayload struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	OTP       string `json:"otp"`
	ExpiresAt string `json:"expires_at"`
	IsResend  bool   `json:"is_resend,omitempty"`
}

// PasswordResetRequestedPayload is emitted on `user.password_reset_requested`.
// SECURITY: ResetToken must never be logged by publisher or consumer.
type PasswordResetRequestedPayload struct {
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	FullName   string `json:"full_name"`
	ResetToken string `json:"reset_token"`
	ExpiresAt  string `json:"expires_at"`
}

// ShopReviewedPayload is emitted on `user.shop_reviewed`.
type ShopReviewedPayload struct {
	ShopID      string `json:"shop_id"`
	OwnerUserID string `json:"owner_user_id"`
	ShopName    string `json:"shop_name"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
}

// ShopBankAccountOTPPayload is emitted on `user.shop_bank_account_otp`.
// SECURITY: OTP must never be logged by publisher or consumer.
type ShopBankAccountOTPPayload struct {
	UserID              string `json:"user_id"`
	ShopID              string `json:"shop_id"`
	Email               string `json:"email"`
	FullName            string `json:"full_name"`
	OTP                 string `json:"otp"`
	ExpiresAt           string `json:"expires_at"`
	BankCode            string `json:"bank_code"`
	AccountNumberMasked string `json:"account_number_masked"`
}

// PhoneOTPRequestedPayload is emitted on `user.phone_otp_requested`.
// SECURITY: OTP must never be logged by publisher or consumer.
type PhoneOTPRequestedPayload struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	FullName    string `json:"full_name"`
	PhoneMasked string `json:"phone_masked"`
	OTP         string `json:"otp"`
	ExpiresAt   string `json:"expires_at"`
}

// SubjectShopStatusChanged is the NATS JetStream subject for shop status transitions (F12).
const SubjectShopStatusChanged = "user.shop_status_changed"

// ShopStatusChangedPayload is emitted on `user.shop_status_changed` via outbox_events (docs/contracts.md §5).
type ShopStatusChangedPayload struct {
	ShopID      string `json:"shop_id"`
	OwnerUserID string `json:"owner_user_id"`
	ShopName    string `json:"shop_name"`
	FromStatus  string `json:"from_status"`
	ToStatus    string `json:"to_status"`
	Reason      string `json:"reason"`
	ActorID     string `json:"actor_id"`
	ApproverID  string `json:"approver_id"`
	Version     int64  `json:"version"`
	OccurredAt  string `json:"occurred_at"`
}

// Publisher publishes domain events to NATS JetStream.
type Publisher interface {
	PublishUserRegistered(ctx context.Context, payload UserRegisteredPayload) error
	PublishPasswordResetRequested(ctx context.Context, payload PasswordResetRequestedPayload) error
	PublishShopReviewed(ctx context.Context, payload ShopReviewedPayload) error
	PublishShopBankAccountOTP(ctx context.Context, payload ShopBankAccountOTPPayload) error
	PublishPhoneOTPRequested(ctx context.Context, payload PhoneOTPRequestedPayload) error
}

// NATSPublisher implements Publisher using NATS JetStream.
type NATSPublisher struct {
	js     nats.JetStreamContext
	stream string
}

// NewNATSPublisher initializes JetStream and ensures the NEXUSMART stream exists idempotently.
func NewNATSPublisher(nc *nats.Conn, stream string) (*NATSPublisher, error) {
	if stream == "" {
		stream = "NEXUSMART"
	}
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("init jetstream: %w", err)
	}

	if err := ensureStream(js, stream); err != nil {
		return nil, err
	}
	return &NATSPublisher{js: js, stream: stream}, nil
}

func ensureStream(js nats.JetStreamContext, stream string) error {
	return jetstream.EnsurePrimaryStream(js, stream)
}

// JetStream returns the underlying JetStreamContext for OutboxRelay publishing.
func (p *NATSPublisher) JetStream() nats.JetStreamContext {
	return p.js
}

func (p *NATSPublisher) PublishUserRegistered(ctx context.Context, payload UserRegisteredPayload) error {
	return p.publish(ctx, "user.registered", payload)
}

func (p *NATSPublisher) PublishPasswordResetRequested(ctx context.Context, payload PasswordResetRequestedPayload) error {
	return p.publish(ctx, "user.password_reset_requested", payload)
}

func (p *NATSPublisher) PublishShopReviewed(ctx context.Context, payload ShopReviewedPayload) error {
	return p.publish(ctx, "user.shop_reviewed", payload)
}

func (p *NATSPublisher) PublishShopBankAccountOTP(ctx context.Context, payload ShopBankAccountOTPPayload) error {
	return p.publish(ctx, "user.shop_bank_account_otp", payload)
}

func (p *NATSPublisher) PublishPhoneOTPRequested(ctx context.Context, payload PhoneOTPRequestedPayload) error {
	return p.publish(ctx, "user.phone_otp_requested", payload)
}

func (p *NATSPublisher) publish(ctx context.Context, subject string, payload any) error {
	eventID := uuid.NewString()
	traceID := telemetry.TraceIDFromContext(ctx)

	env := Envelope{
		EventID:    eventID,
		EventType:  subject,
		TraceID:    traceID,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Payload:    payload,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal event %s: %w", subject, err)
	}

	msg := nats.NewMsg(subject)
	msg.Data = data
	msg.Header.Set(nats.MsgIdHdr, eventID)

	carrier := make(map[string]string)
	telemetry.InjectTraceHeaders(ctx, carrier)
	for k, v := range carrier {
		msg.Header.Set(k, v)
	}

	if _, err := p.js.PublishMsg(msg, nats.Context(ctx)); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}
