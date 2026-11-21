package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Embed IANA timezone DB so Asia/Ho_Chi_Minh works in minimal Alpine containers.
	"unicode/utf8"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/hub"
	chatmetrics "github.com/nexusmart/core/services/chat/internal/metrics"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/nexusmart/core/services/chat/internal/userclient"
	"github.com/nexusmart/core/services/chat/internal/validator"
)

const (
	MaxBodyRunes        = 4000
	MaxAutoReplyRunes   = 2000
	MaxMetadataBytes    = 16 * 1024
	MaxKeywordRules     = 20
	MaxCooldownMinutes  = 24 * 60
	DefaultShopTimezone = "Asia/Ho_Chi_Minh"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("forbidden: access denied")
	ErrNotFound        = errors.New("not found")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrShopInactive    = errors.New("shop is not active")
)

// ShopChecker verifies that a target shop exists and is ACTIVE before creating a conversation.
type ShopChecker interface {
	VerifyActiveShop(ctx context.Context, shopID string) error
}

// TicketStore abstracts single-use WebSocket ticket issuance and redemption.
type TicketStore interface {
	Create(ctx context.Context, claims *auth.Claims) (string, time.Time, error)
	Redeem(ctx context.Context, rawTicket string) (ticket.Redeemed, error)
}

// Publisher broadcasts real-time frames across Chat instances.
type Publisher interface {
	Publish(ctx context.Context, ev hub.Event) error
	InstanceID() string
}

// WireMessage is the canonical JSON representation of a chat message over WebSocket.
type WireMessage struct {
	ID             string         `json:"id"`
	Seq            int64          `json:"seq"`
	ConversationID string         `json:"conversation_id"`
	SenderID       string         `json:"sender_id"`
	SenderRole     string         `json:"sender_role"`
	Kind           string         `json:"kind"`
	Body           string         `json:"body"`
	RefID          string         `json:"ref_id"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	IsAutoReply    bool           `json:"is_auto_reply"`
	ClientMsgID    string         `json:"client_msg_id,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

// ToWireMessage converts a persisted store.Message to its canonical WS JSON payload.
func ToWireMessage(m store.Message) WireMessage {
	return WireMessage{
		ID:             m.ID,
		Seq:            m.Seq,
		ConversationID: m.ConversationID,
		SenderID:       m.SenderID,
		SenderRole:     m.SenderRole,
		Kind:           m.Kind,
		Body:           m.Body,
		RefID:          m.RefID,
		Metadata:       m.Metadata,
		IsAutoReply:    m.IsAutoReply,
		ClientMsgID:    m.ClientMsgID,
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// SendMessageInput holds parameters for sending a chat message.
type SendMessageInput struct {
	ConversationID string
	Kind           string
	Body           string
	RefID          string
	Metadata       map[string]any
	MetadataJSON   string
	ClientMsgID    string
}

// SendMessageOutput holds the persisted user message and optional auto-reply.
type SendMessageOutput struct {
	Message   store.Message
	AutoReply *store.Message
}

// Option configures optional ChatService dependencies.
type Option func(*ChatService)

// WithShopChecker configures shop existence & ACTIVE status verification on conversation creation.
func WithShopChecker(sc ShopChecker) Option {
	return func(s *ChatService) { s.shopChecker = sc }
}

// WithSiteRootDomain configures the platform root domain for off-platform link warnings.
func WithSiteRootDomain(domain string) Option {
	return func(s *ChatService) { s.siteRootDomain = strings.TrimSpace(domain) }
}

// WithMetrics attaches Prometheus collectors.
func WithMetrics(m *chatmetrics.Metrics) Option {
	return func(s *ChatService) { s.metrics = m }
}

// WithNowFunc overrides the wall clock for deterministic tests.
func WithNowFunc(fn func() time.Time) Option {
	return func(s *ChatService) {
		if fn != nil {
			s.nowFn = fn
		}
	}
}

// ChatService implements chat domain logic, strict object-level authorization,
// content/image validation, auto-reply automation, and real-time event fan-out.
type ChatService struct {
	store          store.Store
	pub            Publisher
	tickets        TicketStore
	imgValidator   *validator.ImageValidator
	shopChecker    ShopChecker
	siteRootDomain string
	metrics        *chatmetrics.Metrics
	log            *slog.Logger
	nowFn          func() time.Time
}

// NewChatService constructs a ChatService.
func NewChatService(
	st store.Store,
	pub Publisher,
	tickets TicketStore,
	imgVal *validator.ImageValidator,
	log *slog.Logger,
	opts ...Option,
) *ChatService {
	if imgVal == nil {
		imgVal = validator.NewImageValidator(validator.ImageValidatorConfig{Mode: validator.ModeHybrid})
	}
	if log == nil {
		log = slog.Default()
	}
	s := &ChatService{
		store:        st,
		pub:          pub,
		tickets:      tickets,
		imgValidator: imgVal,
		log:          log,
		nowFn:        func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CanAccessConversation enforces strict object-level authorization:
// ONLY the buyer in the conversation OR a seller/staff member whose shop_id matches conv.ShopID.
func CanAccessConversation(claims *auth.Claims, conv store.Conversation) bool {
	if claims == nil || strings.TrimSpace(claims.UserID()) == "" || claims.Service != "" {
		return false
	}
	if claims.UserID() == conv.BuyerID {
		return true
	}
	if strings.TrimSpace(claims.ShopID) != "" && claims.ShopID == conv.ShopID {
		return true
	}
	return false
}

// CanManageShopAutoReply restricts shop auto-reply config strictly to seller/staff of shopID.
func CanManageShopAutoReply(claims *auth.Claims, shopID string) bool {
	if claims == nil || strings.TrimSpace(claims.UserID()) == "" || claims.Service != "" {
		return false
	}
	shopID = strings.TrimSpace(shopID)
	return shopID != "" && strings.TrimSpace(claims.ShopID) == shopID
}

// CreateOrGetConversation creates or retrieves the buyer's unique conversation with shopID.
func (s *ChatService) CreateOrGetConversation(
	ctx context.Context,
	claims *auth.Claims,
	shopID, siteHost, initialMessage string,
) (store.Conversation, bool, *store.Message, error) {
	if err := requireClaims(claims); err != nil {
		return store.Conversation{}, false, nil, err
	}
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		return store.Conversation{}, false, nil, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}

	if s.shopChecker != nil {
		if err := s.shopChecker.VerifyActiveShop(ctx, shopID); err != nil {
			if errors.Is(err, userclient.ErrShopNotFound) {
				return store.Conversation{}, false, nil, fmt.Errorf("%w: shop %s", ErrNotFound, shopID)
			}
			if errors.Is(err, userclient.ErrShopInactive) {
				return store.Conversation{}, false, nil, fmt.Errorf("%w: %w", ErrShopInactive, err)
			}
			return store.Conversation{}, false, nil, fmt.Errorf("verify shop: %w", err)
		}
	}

	conv, created, err := s.store.CreateOrGetConversation(ctx, claims.UserID(), shopID, strings.TrimSpace(siteHost))
	if err != nil {
		return store.Conversation{}, false, nil, mapStoreError(err)
	}

	var initMsg *store.Message
	if trimmed := strings.TrimSpace(initialMessage); trimmed != "" {
		out, sendErr := s.SendMessage(ctx, claims, SendMessageInput{
			ConversationID: conv.ID,
			Kind:           store.KindText,
			Body:           trimmed,
		})
		if sendErr != nil {
			return store.Conversation{}, false, nil, sendErr
		}
		initMsg = &out.Message
		if refreshed, getErr := s.store.GetConversation(ctx, conv.ID); getErr == nil {
			conv = refreshed
		}
	}

	if unread, uErr := s.store.CountUnread(ctx, conv.ID, callerRoleForConv(claims, conv)); uErr == nil {
		conv.UnreadCount = unread
	}
	return conv, created, initMsg, nil
}

// GetConversation returns a conversation after enforcing CanAccessConversation.
func (s *ChatService) GetConversation(ctx context.Context, claims *auth.Claims, conversationID string) (store.Conversation, error) {
	if err := requireClaims(claims); err != nil {
		return store.Conversation{}, err
	}
	conv, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return store.Conversation{}, mapStoreError(err)
	}
	if !CanAccessConversation(claims, conv) {
		return store.Conversation{}, ErrForbidden
	}
	if unread, uErr := s.store.CountUnread(ctx, conv.ID, callerRoleForConv(claims, conv)); uErr == nil {
		conv.UnreadCount = unread
	}
	return conv, nil
}

// ListConversations lists conversations accessible to claims with optional scope/shop/unreplied filters.
func (s *ChatService) ListConversations(
	ctx context.Context,
	claims *auth.Claims,
	scope, shopID string,
	unrepliedOnly bool,
	page, pageSize int32,
) (store.ListConversationsResult, error) {
	if err := requireClaims(claims); err != nil {
		return store.ListConversationsResult{}, err
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope != "" && scope != store.ScopeBuyer && scope != store.ScopeSeller {
		return store.ListConversationsResult{}, fmt.Errorf("%w: invalid scope %q", ErrInvalidArgument, scope)
	}
	filterShopID := strings.TrimSpace(shopID)
	if scope == store.ScopeSeller {
		if strings.TrimSpace(claims.ShopID) == "" {
			return store.ListConversationsResult{}, ErrForbidden
		}
		if filterShopID != "" && filterShopID != claims.ShopID {
			return store.ListConversationsResult{}, ErrForbidden
		}
	}

	res, err := s.store.ListConversations(ctx, store.ListConversationsFilter{
		UserID:        claims.UserID(),
		CallerShopID:  strings.TrimSpace(claims.ShopID),
		Scope:         scope,
		FilterShopID:  filterShopID,
		UnrepliedOnly: unrepliedOnly,
		Page:          page,
		PageSize:      pageSize,
	})
	if err != nil {
		return store.ListConversationsResult{}, mapStoreError(err)
	}
	return res, nil
}

// ListMessages returns cursor-paginated messages for a single conversation after enforcing access.
func (s *ChatService) ListMessages(
	ctx context.Context,
	claims *auth.Claims,
	conversationID, before, after string,
	limit int32,
) (store.Conversation, store.ListMessagesResult, error) {
	conv, err := s.GetConversation(ctx, claims, conversationID)
	if err != nil {
		return store.Conversation{}, store.ListMessagesResult{}, err
	}
	res, err := s.store.ListMessages(ctx, store.ListMessagesInput{
		ConversationID: conv.ID,
		Before:         strings.TrimSpace(before),
		After:          strings.TrimSpace(after),
		Limit:          limit,
	})
	if err != nil {
		return store.Conversation{}, store.ListMessagesResult{}, mapStoreError(err)
	}
	return conv, res, nil
}

// ListMissedMessages returns messages newer than afterCursor for reconnect sync.
// If conversationID is specified, it scopes to that conversation (with CanAccessConversation);
// otherwise it returns missed messages across ALL conversations accessible to claims.
func (s *ChatService) ListMissedMessages(
	ctx context.Context,
	claims *auth.Claims,
	conversationID, afterCursor string,
	limit int32,
) (store.ListMissedResult, error) {
	if err := requireClaims(claims); err != nil {
		return store.ListMissedResult{}, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID != "" {
		_, res, err := s.ListMessages(ctx, claims, conversationID, "", strings.TrimSpace(afterCursor), limit)
		if err != nil {
			return store.ListMissedResult{}, err
		}
		return store.ListMissedResult{
			Messages:     res.Messages,
			LatestCursor: res.LatestCursor,
			HasMore:      res.HasMore,
		}, nil
	}

	res, err := s.store.ListMissedMessages(ctx, claims.UserID(), strings.TrimSpace(claims.ShopID), strings.TrimSpace(afterCursor), limit)
	if err != nil {
		return store.ListMissedResult{}, mapStoreError(err)
	}
	return res, nil
}

// SendMessage validates permissions, message payload (text/image/product/order), persists
// atomically, broadcasts via Redis Pub/Sub Hub, and runs the shop auto-reply engine for buyer messages.
func (s *ChatService) SendMessage(ctx context.Context, claims *auth.Claims, in SendMessageInput) (SendMessageOutput, error) {
	if err := requireClaims(claims); err != nil {
		return SendMessageOutput{}, err
	}

	conv, err := s.store.GetConversation(ctx, in.ConversationID)
	if err != nil {
		return SendMessageOutput{}, mapStoreError(err)
	}
	if !CanAccessConversation(claims, conv) {
		return SendMessageOutput{}, ErrForbidden
	}

	meta, err := parseMetadata(in.Metadata, in.MetadataJSON)
	if err != nil {
		return SendMessageOutput{}, err
	}

	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	body := strings.TrimSpace(in.Body)
	refID := strings.TrimSpace(in.RefID)

	switch kind {
	case store.KindText:
		if body == "" {
			return SendMessageOutput{}, fmt.Errorf("%w: text message body must not be empty", ErrInvalidArgument)
		}
		if utf8.RuneCountInString(body) > MaxBodyRunes {
			return SendMessageOutput{}, fmt.Errorf("%w: text body exceeds %d characters", ErrInvalidArgument, MaxBodyRunes)
		}
		if validator.DetectExternalContactWarning(body, s.siteRootDomain) {
			if meta == nil {
				meta = make(map[string]any)
			}
			meta["warning"] = "external_transaction_warning"
		}

	case store.KindImage:
		if body == "" {
			return SendMessageOutput{}, fmt.Errorf("%w: image message requires url in body", ErrInvalidArgument)
		}
		valRes, valErr := s.imgValidator.Validate(ctx, body, meta)
		if valErr != nil {
			return SendMessageOutput{}, fmt.Errorf("%w: %w", ErrInvalidArgument, valErr)
		}
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["content_type"] = valRes.ContentType
		meta["size_bytes"] = valRes.SizeBytes

	case store.KindProduct:
		if refID == "" {
			return SendMessageOutput{}, fmt.Errorf("%w: product message requires non-empty ref_id", ErrInvalidArgument)
		}
		if utf8.RuneCountInString(body) > MaxBodyRunes {
			return SendMessageOutput{}, fmt.Errorf("%w: body exceeds %d characters", ErrInvalidArgument, MaxBodyRunes)
		}

	case store.KindOrder:
		if refID == "" {
			return SendMessageOutput{}, fmt.Errorf("%w: order message requires non-empty ref_id", ErrInvalidArgument)
		}
		if utf8.RuneCountInString(body) > MaxBodyRunes {
			return SendMessageOutput{}, fmt.Errorf("%w: body exceeds %d characters", ErrInvalidArgument, MaxBodyRunes)
		}

	default:
		return SendMessageOutput{}, fmt.Errorf("%w: unsupported message kind %q", ErrInvalidArgument, in.Kind)
	}

	senderRole := callerRoleForConv(claims, conv)
	now := s.nowFn().UTC()

	msg, created, err := s.store.CreateMessage(ctx, store.CreateMessageInput{
		ConversationID: conv.ID,
		SenderID:       claims.UserID(),
		SenderRole:     senderRole,
		Kind:           kind,
		Body:           body,
		RefID:          refID,
		Metadata:       meta,
		IsAutoReply:    false,
		ClientMsgID:    strings.TrimSpace(in.ClientMsgID),
		Now:            now,
	})
	if err != nil {
		return SendMessageOutput{}, mapStoreError(err)
	}

	out := SendMessageOutput{Message: msg}
	if !created {
		return out, nil
	}

	if s.metrics != nil {
		s.metrics.MessagesTotal.WithLabelValues(msg.Kind, msg.SenderRole, "false").Inc()
	}
	s.publishMessage(ctx, conv, msg)

	if senderRole == store.RoleBuyer {
		autoMsg := s.maybeSendAutoReply(ctx, conv, msg, now)
		if autoMsg != nil {
			out.AutoReply = autoMsg
		}
	}

	return out, nil
}

// MarkRead monotonically advances the caller's read cursor and broadcasts a read_receipt event.
func (s *ChatService) MarkRead(
	ctx context.Context,
	claims *auth.Claims,
	conversationID string,
	lastReadSeq int64,
	messageID string,
) (store.MarkReadResult, error) {
	if err := requireClaims(claims); err != nil {
		return store.MarkReadResult{}, err
	}

	conv, err := s.store.GetConversation(ctx, conversationID)
	if err != nil {
		return store.MarkReadResult{}, mapStoreError(err)
	}
	if !CanAccessConversation(claims, conv) {
		return store.MarkReadResult{}, ErrForbidden
	}

	readerRole := callerRoleForConv(claims, conv)
	res, err := s.store.MarkRead(ctx, conv.ID, readerRole, lastReadSeq, strings.TrimSpace(messageID))
	if err != nil {
		return store.MarkReadResult{}, mapStoreError(err)
	}

	s.publishReadReceipt(ctx, conv, claims.UserID(), readerRole, res.LastReadSeq)
	return res, nil
}

// CreateWSTicket issues a short-lived single-use WebSocket ticket for the authenticated caller.
func (s *ChatService) CreateWSTicket(ctx context.Context, claims *auth.Claims) (string, time.Time, error) {
	if err := requireClaims(claims); err != nil {
		return "", time.Time{}, err
	}
	if s.tickets == nil {
		return "", time.Time{}, errors.New("ws ticket store unavailable")
	}
	return s.tickets.Create(ctx, claims)
}

// ExchangeWSTicket atomically consumes a one-time ticket and returns verified claims + ws_token.
func (s *ChatService) ExchangeWSTicket(ctx context.Context, rawTicket string) (ticket.Redeemed, error) {
	if s.tickets == nil {
		return ticket.Redeemed{}, errors.New("ws ticket store unavailable")
	}
	return s.tickets.Redeem(ctx, rawTicket)
}

// GetAutoReplyConfig retrieves a shop's auto-reply configuration (restricted to that shop's staff/seller).
func (s *ChatService) GetAutoReplyConfig(ctx context.Context, claims *auth.Claims, shopID string) (store.AutoReplyConfig, error) {
	if err := requireClaims(claims); err != nil {
		return store.AutoReplyConfig{}, err
	}
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		shopID = strings.TrimSpace(claims.ShopID)
	}
	if !CanManageShopAutoReply(claims, shopID) {
		return store.AutoReplyConfig{}, ErrForbidden
	}

	cfg, err := s.store.GetAutoReplyConfig(ctx, shopID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.DefaultAutoReplyConfig(shopID), nil
		}
		return store.AutoReplyConfig{}, mapStoreError(err)
	}
	return cfg, nil
}

// UpsertAutoReplyConfig validates and saves a shop's auto-reply config (restricted to that shop's staff/seller).
func (s *ChatService) UpsertAutoReplyConfig(ctx context.Context, claims *auth.Claims, cfg store.AutoReplyConfig) (store.AutoReplyConfig, error) {
	if err := requireClaims(claims); err != nil {
		return store.AutoReplyConfig{}, err
	}
	cfg.ShopID = strings.TrimSpace(cfg.ShopID)
	if cfg.ShopID == "" {
		cfg.ShopID = strings.TrimSpace(claims.ShopID)
	}
	if !CanManageShopAutoReply(claims, cfg.ShopID) {
		return store.AutoReplyConfig{}, ErrForbidden
	}

	cfg.GreetingMessage = strings.TrimSpace(cfg.GreetingMessage)
	cfg.OfflineMessage = strings.TrimSpace(cfg.OfflineMessage)
	if utf8.RuneCountInString(cfg.GreetingMessage) > MaxAutoReplyRunes {
		return store.AutoReplyConfig{}, fmt.Errorf("%w: greeting_message exceeds %d characters", ErrInvalidArgument, MaxAutoReplyRunes)
	}
	if utf8.RuneCountInString(cfg.OfflineMessage) > MaxAutoReplyRunes {
		return store.AutoReplyConfig{}, fmt.Errorf("%w: offline_message exceeds %d characters", ErrInvalidArgument, MaxAutoReplyRunes)
	}
	if cfg.WorkStartHour < 0 || cfg.WorkStartHour > 23 || cfg.WorkEndHour < 0 || cfg.WorkEndHour > 23 {
		return store.AutoReplyConfig{}, fmt.Errorf("%w: work_start_hour and work_end_hour must be in 0..23", ErrInvalidArgument)
	}
	if cfg.CooldownMinutes < 0 || cfg.CooldownMinutes > MaxCooldownMinutes {
		return store.AutoReplyConfig{}, fmt.Errorf("%w: cooldown_minutes must be in 0..%d", ErrInvalidArgument, MaxCooldownMinutes)
	}
	cfg.Timezone = strings.TrimSpace(cfg.Timezone)
	if cfg.Timezone == "" {
		cfg.Timezone = DefaultShopTimezone
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return store.AutoReplyConfig{}, fmt.Errorf("%w: invalid timezone %q", ErrInvalidArgument, cfg.Timezone)
	}
	if len(cfg.KeywordRules) > MaxKeywordRules {
		return store.AutoReplyConfig{}, fmt.Errorf("%w: at most %d keyword rules allowed", ErrInvalidArgument, MaxKeywordRules)
	}

	cleanRules := make([]store.KeywordRule, 0, len(cfg.KeywordRules))
	for _, r := range cfg.KeywordRules {
		reply := strings.TrimSpace(r.Reply)
		if reply == "" {
			return store.AutoReplyConfig{}, fmt.Errorf("%w: keyword rule reply must not be empty", ErrInvalidArgument)
		}
		if utf8.RuneCountInString(reply) > MaxAutoReplyRunes {
			return store.AutoReplyConfig{}, fmt.Errorf("%w: keyword rule reply exceeds %d characters", ErrInvalidArgument, MaxAutoReplyRunes)
		}
		kws := make([]string, 0, len(r.Keywords))
		for _, kw := range r.Keywords {
			if t := strings.TrimSpace(kw); t != "" {
				kws = append(kws, t)
			}
		}
		if len(kws) == 0 {
			return store.AutoReplyConfig{}, fmt.Errorf("%w: keyword rule must have at least one keyword", ErrInvalidArgument)
		}
		cleanRules = append(cleanRules, store.KeywordRule{Keywords: kws, Reply: reply})
	}
	cfg.KeywordRules = cleanRules

	saved, err := s.store.UpsertAutoReplyConfig(ctx, cfg)
	if err != nil {
		return store.AutoReplyConfig{}, mapStoreError(err)
	}
	return saved, nil
}

func (s *ChatService) maybeSendAutoReply(ctx context.Context, conv store.Conversation, buyerMsg store.Message, now time.Time) *store.Message {
	cfg, err := s.store.GetAutoReplyConfig(ctx, conv.ShopID)
	if err != nil || !cfg.Enabled {
		return nil
	}

	replyText, trigger := selectAutoReply(cfg, buyerMsg, now)
	if replyText == "" {
		return nil
	}

	cooldown := time.Duration(cfg.CooldownMinutes) * time.Minute
	claimed, err := s.store.ClaimAutoReplySlot(ctx, conv.ID, now, cooldown)
	if err != nil {
		s.log.WarnContext(ctx, "claim auto reply slot failed", slog.String("conversation_id", conv.ID), slog.String("error", err.Error()))
		return nil
	}
	if !claimed {
		return nil
	}

	autoMsg, created, err := s.store.CreateMessage(ctx, store.CreateMessageInput{
		ConversationID: conv.ID,
		SenderID:       "shop:" + conv.ShopID,
		SenderRole:     store.RoleSeller,
		Kind:           store.KindText,
		Body:           replyText,
		Metadata:       map[string]any{"trigger": trigger},
		IsAutoReply:    true,
		Now:            now.Add(time.Millisecond),
	})
	if err != nil {
		s.log.WarnContext(ctx, "persist auto reply failed", slog.String("conversation_id", conv.ID), slog.String("error", err.Error()))
		return nil
	}
	if !created {
		return nil
	}

	if s.metrics != nil {
		s.metrics.MessagesTotal.WithLabelValues(autoMsg.Kind, autoMsg.SenderRole, "true").Inc()
	}
	s.publishMessage(ctx, conv, autoMsg)
	return &autoMsg
}

func selectAutoReply(cfg store.AutoReplyConfig, buyerMsg store.Message, now time.Time) (reply string, trigger string) {
	lowerBody := strings.ToLower(strings.TrimSpace(buyerMsg.Body))
	if lowerBody != "" {
		for _, rule := range cfg.KeywordRules {
			rText := strings.TrimSpace(rule.Reply)
			if rText == "" {
				continue
			}
			for _, kw := range rule.Keywords {
				k := strings.ToLower(strings.TrimSpace(kw))
				if k != "" && strings.Contains(lowerBody, k) {
					return rText, "keyword"
				}
			}
		}
	}

	if cfg.OfflineEnabled && strings.TrimSpace(cfg.OfflineMessage) != "" && isOutsideWorkHours(now, cfg.WorkStartHour, cfg.WorkEndHour, cfg.Timezone) {
		return strings.TrimSpace(cfg.OfflineMessage), "offline"
	}

	if g := strings.TrimSpace(cfg.GreetingMessage); g != "" {
		return g, "greeting"
	}

	return "", ""
}

func isOutsideWorkHours(now time.Time, startHour, endHour int32, tz string) bool {
	loc, err := time.LoadLocation(strings.TrimSpace(tz))
	if err != nil {
		loc, err = time.LoadLocation(DefaultShopTimezone)
		if err != nil {
			loc = time.FixedZone("ICT", 7*3600)
		}
	}
	h := int32(now.In(loc).Hour())
	if startHour == endHour {
		return false // 24h open
	}
	if startHour < endHour {
		return h < startHour || h >= endHour
	}
	// Overnight work window (e.g., 22..06): offline during [endHour, startHour).
	return h >= endHour && h < startHour
}

func (s *ChatService) publishMessage(ctx context.Context, conv store.Conversation, msg store.Message) {
	if s.pub == nil {
		return
	}
	frame, err := json.Marshal(struct {
		Type           string      `json:"type"`
		ConversationID string      `json:"conversation_id"`
		Message        WireMessage `json:"message"`
		InstanceID     string      `json:"instance_id"`
	}{
		Type:           hub.EventTypeMessage,
		ConversationID: conv.ID,
		Message:        ToWireMessage(msg),
		InstanceID:     s.pub.InstanceID(),
	})
	if err != nil {
		return
	}
	if err := s.pub.Publish(ctx, hub.Event{
		Type:       hub.EventTypeMessage,
		BuyerID:    conv.BuyerID,
		ShopID:     conv.ShopID,
		InstanceID: s.pub.InstanceID(),
		Frame:      frame,
	}); err != nil {
		s.log.WarnContext(ctx, "publish chat message event failed",
			slog.String("conversation_id", conv.ID),
			slog.String("seq", strconv.FormatInt(msg.Seq, 10)),
			slog.String("error", err.Error()),
		)
	}
}

func (s *ChatService) publishReadReceipt(ctx context.Context, conv store.Conversation, readerID, readerRole string, lastReadSeq int64) {
	if s.pub == nil {
		return
	}
	frame, err := json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		ReaderID       string `json:"reader_id"`
		ReaderRole     string `json:"reader_role"`
		LastReadSeq    int64  `json:"last_read_seq"`
		InstanceID     string `json:"instance_id"`
	}{
		Type:           hub.EventTypeReadReceipt,
		ConversationID: conv.ID,
		ReaderID:       readerID,
		ReaderRole:     readerRole,
		LastReadSeq:    lastReadSeq,
		InstanceID:     s.pub.InstanceID(),
	})
	if err != nil {
		return
	}
	if err := s.pub.Publish(ctx, hub.Event{
		Type:       hub.EventTypeReadReceipt,
		BuyerID:    conv.BuyerID,
		ShopID:     conv.ShopID,
		InstanceID: s.pub.InstanceID(),
		Frame:      frame,
	}); err != nil {
		s.log.WarnContext(ctx, "publish read receipt event failed",
			slog.String("conversation_id", conv.ID),
			slog.String("error", err.Error()),
		)
	}
}

func requireClaims(claims *auth.Claims) error {
	if claims == nil || strings.TrimSpace(claims.UserID()) == "" || claims.Service != "" {
		return ErrUnauthenticated
	}
	return nil
}

func callerRoleForConv(claims *auth.Claims, conv store.Conversation) string {
	if claims.UserID() == conv.BuyerID {
		return store.RoleBuyer
	}
	return store.RoleSeller
}

func parseMetadata(metaMap map[string]any, metaJSON string) (map[string]any, error) {
	if len(metaMap) > 0 {
		encoded, err := json.Marshal(metaMap)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid metadata: %w", ErrInvalidArgument, err)
		}
		if len(encoded) > MaxMetadataBytes {
			return nil, fmt.Errorf("%w: metadata exceeds %d bytes", ErrInvalidArgument, MaxMetadataBytes)
		}
		out := make(map[string]any, len(metaMap))
		for k, v := range metaMap {
			out[k] = v
		}
		return out, nil
	}

	trimmed := strings.TrimSpace(metaJSON)
	if trimmed == "" || trimmed == "{}" {
		return nil, nil
	}
	if len(trimmed) > MaxMetadataBytes {
		return nil, fmt.Errorf("%w: metadata_json exceeds %d bytes", ErrInvalidArgument, MaxMetadataBytes)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, fmt.Errorf("%w: metadata_json must be a valid JSON object", ErrInvalidArgument)
	}
	return parsed, nil
}

func mapStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.Is(err, store.ErrInvalidCursor), errors.Is(err, store.ErrInvalidArgument):
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	default:
		return err
	}
}
