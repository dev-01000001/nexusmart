package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

// Conversation represents an AI assistant conversation thread.
type Conversation struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ShopID    string    `json:"shop_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message represents a persisted, PII-redacted turn in a conversation.
type Message struct {
	ID               string          `json:"id"`
	ConversationID   string          `json:"conversation_id"`
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ToolCallsJSON    json.RawMessage `json:"tool_calls_json,omitempty"`
	RetrievedIDsJSON json.RawMessage `json:"retrieved_ids_json,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// Feedback records buyer/seller helpfulness feedback on an assistant response.
type Feedback struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	UserID         string    `json:"user_id"`
	Helpful        bool      `json:"helpful"`
	Comment        string    `json:"comment"`
	CreatedAt      time.Time `json:"created_at"`
}

// Review stores a product review synced for batch AI summarization.
type Review struct {
	ID        string    `json:"id"`
	ProductID string    `json:"product_id"`
	ShopID    string    `json:"shop_id"`
	Rating    int       `json:"rating"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"created_at"`
}

// ReviewSummary stores a cached AI-generated pros/cons summary for a product.
type ReviewSummary struct {
	ProductID     string    `json:"product_id"`
	ShopID        string    `json:"shop_id"`
	ReviewCount   int       `json:"review_count"`
	AverageRating float64   `json:"average_rating"`
	Summary       string    `json:"summary"`
	Pros          []string  `json:"pros"`
	Cons          []string  `json:"cons"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// HandoffTicket records an escalation from AI assistant to a human support agent.
type HandoffTicket struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"user_id"`
	ShopID             string    `json:"shop_id"`
	ConversationID     string    `json:"conversation_id"`
	ChatConversationID string    `json:"chat_conversation_id"`
	Reason             string    `json:"reason"`
	Summary            string    `json:"summary"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
}

// UsageLog records token consumption, integer VND cost, latency, and guardrail blocks.
type UsageLog struct {
	ID            string    `json:"id"`
	TraceID       string    `json:"trace_id"`
	UserID        string    `json:"user_id"`
	ShopID        string    `json:"shop_id"`
	Endpoint      string    `json:"endpoint"`
	Model         string    `json:"model"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	CostVND       int64     `json:"cost_vnd"`
	LatencyMs     int64     `json:"latency_ms"`
	TTFTMs        int64     `json:"ttft_ms"`
	CacheHit      bool      `json:"cache_hit"`
	BlockedReason string    `json:"blocked_reason"`
	CreatedAt     time.Time `json:"created_at"`
}

// Store persists AI assistant state, PII-redacted evaluation logs, review summaries, and idempotency keys.
type Store interface {
	EnsureConversation(ctx context.Context, conv Conversation) (Conversation, error)
	SaveMessage(ctx context.Context, msg Message) error
	ListMessages(ctx context.Context, conversationID string, limit int) ([]Message, error)
	SaveFeedback(ctx context.Context, fb Feedback) error
	UpsertReviews(ctx context.Context, reviews []Review) error
	ListReviewsByProduct(ctx context.Context, productID string) ([]Review, error)
	GetReviewSummary(ctx context.Context, productID string) (*ReviewSummary, error)
	UpsertReviewSummary(ctx context.Context, summary ReviewSummary) error
	SaveHandoffTicket(ctx context.Context, ticket HandoffTicket) error
	GetHandoffTicket(ctx context.Context, id string) (*HandoffTicket, error)
	SaveUsageLog(ctx context.Context, log UsageLog) error
	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, subject string) (inserted bool, err error)
	Ping(ctx context.Context) error
}

var (
	reBearerToken = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-~+/]+=*`)
	reJWTToken    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\b`)
	reAPIKey      = regexp.MustCompile(`\b(?:sk-ant-[A-Za-z0-9_-]{10,}|sk-[A-Za-z0-9_-]{16,}|pa-[A-Za-z0-9_-]{12,})\b`)
	reEmail       = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	reVNPhone     = regexp.MustCompile(`(?:^|[^0-9A-Za-z])((?:\+84|84|0)[.\-\s]?(?:3|5|7|8|9)(?:[.\-\s]?[0-9]){8})(?:[^0-9A-Za-z]|$)`)
)

// RedactPII masks email addresses, Vietnamese phone numbers, Bearer tokens, JWTs,
// and API keys before messages or comments are persisted (UC-AI13).
func RedactPII(text string) string {
	if text == "" {
		return ""
	}
	out := reBearerToken.ReplaceAllString(text, "Bearer [REDACTED_TOKEN]")
	out = reJWTToken.ReplaceAllString(out, "[REDACTED_JWT]")
	out = reAPIKey.ReplaceAllString(out, "[REDACTED_KEY]")
	out = reEmail.ReplaceAllString(out, "[REDACTED_EMAIL]")
	out = redactVNPhones(out)
	return out
}

func redactVNPhones(s string) string {
	for {
		loc := reVNPhone.FindStringSubmatchIndex(s)
		if loc == nil {
			return s
		}
		start, end := loc[2], loc[3]
		candidate := s[start:end]
		digits := digitsOnly(candidate)
		valid := (strings.HasPrefix(digits, "0") && len(digits) == 10) ||
			(strings.HasPrefix(digits, "84") && len(digits) == 11)
		if !valid {
			// Advance past this candidate by continuing on the remainder.
			return s[:end] + redactVNPhones(s[end:])
		}
		s = s[:start] + "[REDACTED_PHONE]" + s[end:]
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func normalizeJSONArray(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage("[]")
	}
	return raw
}
