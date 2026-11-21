package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RoleBuyer  = "buyer"
	RoleSeller = "seller"

	KindText    = "text"
	KindImage   = "image"
	KindProduct = "product"
	KindOrder   = "order"

	ScopeBuyer  = "buyer"
	ScopeSeller = "seller"

	DefaultPageSize     = 20
	MaxPageSize         = 100
	DefaultMessageLimit = 50
	MaxMessageLimit     = 100
	MaxPreviewRunes     = 120
)

var (
	ErrNotFound        = errors.New("chat record not found")
	ErrInvalidCursor   = errors.New("invalid pagination cursor")
	ErrInvalidArgument = errors.New("invalid store argument")
)

// Conversation represents a buyer-shop chat thread.
type Conversation struct {
	ID                    string
	BuyerID               string
	ShopID                string
	SiteHost              string
	LastMessageID         string
	LastMessageSeq        int64
	LastMessagePreview    string
	LastMessageSenderRole string
	LastMessageAt         *time.Time
	BuyerLastReadSeq      int64
	BuyerLastReadAt       *time.Time
	SellerLastReadSeq     int64
	SellerLastReadAt      *time.Time
	LastAutoReplyAt       *time.Time
	UnreadCount           int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Message represents a single persisted chat message.
type Message struct {
	ID             string
	Seq            int64
	ConversationID string
	SenderID       string
	SenderRole     string
	Kind           string
	Body           string
	RefID          string
	Metadata       map[string]any
	IsAutoReply    bool
	ClientMsgID    string
	CreatedAt      time.Time
}

// KeywordRule defines a case-insensitive keyword auto-reply rule.
type KeywordRule struct {
	Keywords []string `json:"keywords"`
	Reply    string   `json:"reply"`
}

// AutoReplyConfig stores a shop's auto-reply settings.
type AutoReplyConfig struct {
	ShopID          string
	Enabled         bool
	GreetingMessage string
	OfflineEnabled  bool
	OfflineMessage  string
	WorkStartHour   int32
	WorkEndHour     int32
	Timezone        string
	CooldownMinutes int32
	KeywordRules    []KeywordRule
	UpdatedAt       time.Time
}

// DefaultAutoReplyConfig returns the default auto-reply configuration for a shop.
func DefaultAutoReplyConfig(shopID string) AutoReplyConfig {
	return AutoReplyConfig{
		ShopID:          shopID,
		Enabled:         false,
		WorkStartHour:   8,
		WorkEndHour:     22,
		Timezone:        "Asia/Ho_Chi_Minh",
		CooldownMinutes: 30,
		KeywordRules:    []KeywordRule{},
	}
}

// ListConversationsFilter filters conversations visible to a caller.
type ListConversationsFilter struct {
	UserID        string
	CallerShopID  string
	Scope         string // "", "buyer", or "seller"
	FilterShopID  string // optional explicit shop_id filter
	UnrepliedOnly bool
	Page          int32
	PageSize      int32
}

// ListConversationsResult returns paginated conversations and aggregate counts.
type ListConversationsResult struct {
	Conversations []Conversation
	Total         int64
	TotalUnread   int64
}

// CreateMessageInput describes a message to persist.
type CreateMessageInput struct {
	ConversationID string
	SenderID       string
	SenderRole     string
	Kind           string
	Body           string
	RefID          string
	Metadata       map[string]any
	IsAutoReply    bool
	ClientMsgID    string
	Now            time.Time
}

// ListMessagesInput specifies cursor-paginated message retrieval.
type ListMessagesInput struct {
	ConversationID string
	Before         string
	After          string
	Limit          int32
}

// ListMessagesResult holds chronologically ordered (seq ASC) messages and cursor tokens.
type ListMessagesResult struct {
	Messages     []Message
	NextBefore   string
	LatestCursor string
	HasMore      bool
}

// ListMissedResult holds cross-conversation catch-up messages in chronological seq ASC order.
type ListMissedResult struct {
	Messages     []Message
	LatestCursor string
	HasMore      bool
}

// MarkReadResult holds the monotonically updated read cursor and remaining unread count.
type MarkReadResult struct {
	ConversationID string
	LastReadSeq    int64
	UnreadCount    int64
}

// Store defines the persistence contract for the chat service.
type Store interface {
	CreateOrGetConversation(ctx context.Context, buyerID, shopID, siteHost string) (Conversation, bool, error)
	GetConversation(ctx context.Context, conversationID string) (Conversation, error)
	CountUnread(ctx context.Context, conversationID, viewerRole string) (int64, error)
	ListConversations(ctx context.Context, filter ListConversationsFilter) (ListConversationsResult, error)
	CreateMessage(ctx context.Context, in CreateMessageInput) (Message, bool, error)
	ListMessages(ctx context.Context, in ListMessagesInput) (ListMessagesResult, error)
	ListMissedMessages(ctx context.Context, userID, shopID, afterCursor string, limit int32) (ListMissedResult, error)
	MarkRead(ctx context.Context, conversationID, readerRole string, lastReadSeq int64, messageID string) (MarkReadResult, error)
	ClaimAutoReplySlot(ctx context.Context, conversationID string, now time.Time, cooldown time.Duration) (bool, error)
	GetAutoReplyConfig(ctx context.Context, shopID string) (AutoReplyConfig, error)
	UpsertAutoReplyConfig(ctx context.Context, cfg AutoReplyConfig) (AutoReplyConfig, error)
}

// BuildMessagePreview generates a bounded conversation preview snippet from a message.
func BuildMessagePreview(kind, body, refID string) string {
	trimmed := strings.TrimSpace(body)
	switch kind {
	case KindImage:
		return "[Image]"
	case KindProduct:
		if trimmed != "" {
			return truncateRunes(trimmed, MaxPreviewRunes)
		}
		if strings.TrimSpace(refID) != "" {
			return "[Product] " + strings.TrimSpace(refID)
		}
		return "[Product]"
	case KindOrder:
		if trimmed != "" {
			return truncateRunes(trimmed, MaxPreviewRunes)
		}
		if strings.TrimSpace(refID) != "" {
			return "[Order] " + strings.TrimSpace(refID)
		}
		return "[Order]"
	default:
		return truncateRunes(trimmed, MaxPreviewRunes)
	}
}

func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func normalizePagination(page, pageSize int32) (int32, int32) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}

func normalizeMessageLimit(limit int32) int32 {
	if limit <= 0 {
		return DefaultMessageLimit
	}
	if limit > MaxMessageLimit {
		return MaxMessageLimit
	}
	return limit
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
