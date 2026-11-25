package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemStore is a thread-safe in-memory implementation of Store intended for unit tests.
type MemStore struct {
	mu           sync.RWMutex
	convsByID    map[string]*Conversation
	convByPair   map[string]string
	messages     []Message
	msgByID      map[string]int64
	clientMsgIdx map[string]int64
	autoReplies  map[string]AutoReplyConfig
	nextSeq      int64
}

// NewMemStore creates an empty in-memory Store.
func NewMemStore() *MemStore {
	return &MemStore{
		convsByID:    make(map[string]*Conversation),
		convByPair:   make(map[string]string),
		msgByID:      make(map[string]int64),
		clientMsgIdx: make(map[string]int64),
		autoReplies:  make(map[string]AutoReplyConfig),
	}
}

func pairKey(buyerID, shopID string) string {
	return buyerID + "\x00" + shopID
}

func clientMsgKey(convID, senderID, clientMsgID string) string {
	return convID + "\x00" + senderID + "\x00" + clientMsgID
}

func copyConversation(c *Conversation) Conversation {
	out := *c
	if c.LastMessageAt != nil {
		t := *c.LastMessageAt
		out.LastMessageAt = &t
	}
	if c.BuyerLastReadAt != nil {
		t := *c.BuyerLastReadAt
		out.BuyerLastReadAt = &t
	}
	if c.SellerLastReadAt != nil {
		t := *c.SellerLastReadAt
		out.SellerLastReadAt = &t
	}
	if c.LastAutoReplyAt != nil {
		t := *c.LastAutoReplyAt
		out.LastAutoReplyAt = &t
	}
	return out
}

func copyMessage(m Message) Message {
	m.Metadata = cloneMetadata(m.Metadata)
	return m
}

func (m *MemStore) CreateOrGetConversation(_ context.Context, buyerID, shopID, siteHost string) (Conversation, bool, error) {
	buyerID = strings.TrimSpace(buyerID)
	shopID = strings.TrimSpace(shopID)
	siteHost = strings.TrimSpace(siteHost)
	if buyerID == "" || shopID == "" {
		return Conversation{}, false, fmt.Errorf("%w: buyer_id and shop_id are required", ErrInvalidArgument)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pk := pairKey(buyerID, shopID)
	if existingID, ok := m.convByPair[pk]; ok {
		return copyConversation(m.convsByID[existingID]), false, nil
	}

	now := time.Now().UTC()
	conv := &Conversation{
		ID:        uuid.NewString(),
		BuyerID:   buyerID,
		ShopID:    shopID,
		SiteHost:  siteHost,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.convsByID[conv.ID] = conv
	m.convByPair[pk] = conv.ID
	return copyConversation(conv), true, nil
}

func (m *MemStore) GetConversation(_ context.Context, conversationID string) (Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.convsByID[strings.TrimSpace(conversationID)]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	return copyConversation(c), nil
}

func (m *MemStore) CountUnread(_ context.Context, conversationID, viewerRole string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.convsByID[strings.TrimSpace(conversationID)]
	if !ok {
		return 0, ErrNotFound
	}
	return m.countUnreadLocked(c, viewerRole), nil
}

func (m *MemStore) countUnreadLocked(c *Conversation, viewerRole string) int64 {
	lastRead := c.BuyerLastReadSeq
	skipRole := RoleBuyer
	if viewerRole == RoleSeller {
		lastRead = c.SellerLastReadSeq
		skipRole = RoleSeller
	}
	var count int64
	for _, msg := range m.messages {
		if msg.ConversationID == c.ID && msg.Seq > lastRead && msg.SenderRole != skipRole {
			count++
		}
	}
	return count
}

func (m *MemStore) ListConversations(_ context.Context, filter ListConversationsFilter) (ListConversationsResult, error) {
	page, pageSize := normalizePagination(filter.Page, filter.PageSize)
	scope := strings.ToLower(strings.TrimSpace(filter.Scope))

	if scope == ScopeSeller && strings.TrimSpace(filter.CallerShopID) == "" {
		return ListConversationsResult{Conversations: []Conversation{}}, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	matched := make([]Conversation, 0)
	var totalUnread int64
	for _, c := range m.convsByID {
		var viewerRole string
		switch scope {
		case ScopeBuyer:
			if c.BuyerID != filter.UserID {
				continue
			}
			viewerRole = RoleBuyer
		case ScopeSeller:
			if c.ShopID != filter.CallerShopID {
				continue
			}
			viewerRole = RoleSeller
		default:
			if c.BuyerID == filter.UserID {
				viewerRole = RoleBuyer
			} else if filter.CallerShopID != "" && c.ShopID == filter.CallerShopID {
				viewerRole = RoleSeller
			} else {
				continue
			}
		}
		if filter.FilterShopID != "" && c.ShopID != filter.FilterShopID {
			continue
		}
		if filter.UnrepliedOnly && c.LastMessageSenderRole != RoleBuyer {
			continue
		}

		cp := copyConversation(c)
		cp.UnreadCount = m.countUnreadLocked(c, viewerRole)
		totalUnread += cp.UnreadCount
		matched = append(matched, cp)
	}

	sort.Slice(matched, func(i, j int) bool {
		ti := matched[i].CreatedAt
		if matched[i].LastMessageAt != nil {
			ti = *matched[i].LastMessageAt
		}
		tj := matched[j].CreatedAt
		if matched[j].LastMessageAt != nil {
			tj = *matched[j].LastMessageAt
		}
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := int64(len(matched))
	start := int((page - 1) * pageSize)
	if start >= len(matched) {
		return ListConversationsResult{
			Conversations: []Conversation{},
			Total:         total,
			TotalUnread:   totalUnread,
		}, nil
	}
	end := start + int(pageSize)
	if end > len(matched) {
		end = len(matched)
	}
	return ListConversationsResult{
		Conversations: matched[start:end],
		Total:         total,
		TotalUnread:   totalUnread,
	}, nil
}

func (m *MemStore) CreateMessage(_ context.Context, in CreateMessageInput) (Message, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.convsByID[strings.TrimSpace(in.ConversationID)]
	if !ok {
		return Message{}, false, ErrNotFound
	}

	clientMsgID := strings.TrimSpace(in.ClientMsgID)
	if clientMsgID != "" {
		ck := clientMsgKey(c.ID, in.SenderID, clientMsgID)
		if seq, exists := m.clientMsgIdx[ck]; exists {
			for _, msg := range m.messages {
				if msg.Seq == seq {
					return copyMessage(msg), false, nil
				}
			}
		}
	}

	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	m.nextSeq++
	msg := Message{
		ID:             uuid.NewString(),
		Seq:            m.nextSeq,
		ConversationID: c.ID,
		SenderID:       in.SenderID,
		SenderRole:     in.SenderRole,
		Kind:           in.Kind,
		Body:           in.Body,
		RefID:          in.RefID,
		Metadata:       cloneMetadata(in.Metadata),
		IsAutoReply:    in.IsAutoReply,
		ClientMsgID:    clientMsgID,
		CreatedAt:      now,
	}
	m.messages = append(m.messages, msg)
	m.msgByID[msg.ID] = msg.Seq
	if clientMsgID != "" {
		m.clientMsgIdx[clientMsgKey(c.ID, in.SenderID, clientMsgID)] = msg.Seq
	}

	c.LastMessageID = msg.ID
	if msg.Seq > c.LastMessageSeq {
		c.LastMessageSeq = msg.Seq
	}
	c.LastMessagePreview = BuildMessagePreview(msg.Kind, msg.Body, msg.RefID)
	c.LastMessageSenderRole = msg.SenderRole
	ts := msg.CreatedAt
	c.LastMessageAt = &ts
	if ts.After(c.UpdatedAt) {
		c.UpdatedAt = ts
	}

	return copyMessage(msg), true, nil
}

func (m *MemStore) ListMessages(_ context.Context, in ListMessagesInput) (ListMessagesResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.convsByID[strings.TrimSpace(in.ConversationID)]
	if !ok {
		return ListMessagesResult{}, ErrNotFound
	}

	before := strings.TrimSpace(in.Before)
	after := strings.TrimSpace(in.After)
	if before != "" && after != "" {
		return ListMessagesResult{}, fmt.Errorf("%w: before and after cursors are mutually exclusive", ErrInvalidArgument)
	}
	limit := int(normalizeMessageLimit(in.Limit))

	convMsgs := make([]Message, 0)
	for _, msg := range m.messages {
		if msg.ConversationID == c.ID {
			convMsgs = append(convMsgs, copyMessage(msg))
		}
	}

	var (
		filtered []Message
		hasMore  bool
		isAfter  bool
	)
	switch {
	case after != "":
		isAfter = true
		afterSeq, err := m.resolveCursorLocked(c.ID, after, true)
		if err != nil {
			return ListMessagesResult{}, err
		}
		for _, msg := range convMsgs {
			if msg.Seq > afterSeq {
				filtered = append(filtered, msg)
			}
		}
		if len(filtered) > limit {
			hasMore = true
			filtered = filtered[:limit]
		}
	case before != "":
		beforeSeq, err := m.resolveCursorLocked(c.ID, before, false)
		if err != nil {
			return ListMessagesResult{}, err
		}
		for _, msg := range convMsgs {
			if msg.Seq < beforeSeq {
				filtered = append(filtered, msg)
			}
		}
		if len(filtered) > limit {
			hasMore = true
			filtered = filtered[len(filtered)-limit:]
		}
	default:
		filtered = convMsgs
		if len(filtered) > limit {
			hasMore = true
			filtered = filtered[len(filtered)-limit:]
		}
	}

	res := ListMessagesResult{
		Messages: filtered,
		HasMore:  hasMore,
	}
	if len(filtered) > 0 {
		if (!isAfter && hasMore) || isAfter {
			res.NextBefore = strconv.FormatInt(filtered[0].Seq, 10)
		}
		res.LatestCursor = strconv.FormatInt(filtered[len(filtered)-1].Seq, 10)
	} else if after != "" {
		res.LatestCursor = after
	}
	return res, nil
}

func (m *MemStore) ListMissedMessages(_ context.Context, userID, shopID, afterCursor string, limit int32) (ListMissedResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	afterSeq, err := m.resolveGlobalCursorLocked(userID, shopID, afterCursor)
	if err != nil {
		return ListMissedResult{}, err
	}
	lim := int(normalizeMessageLimit(limit))

	var matched []Message
	for _, msg := range m.messages {
		if msg.Seq <= afterSeq {
			continue
		}
		c, ok := m.convsByID[msg.ConversationID]
		if !ok {
			continue
		}
		if c.BuyerID == userID || (shopID != "" && c.ShopID == shopID) {
			matched = append(matched, copyMessage(msg))
		}
	}

	hasMore := false
	if len(matched) > lim {
		hasMore = true
		matched = matched[:lim]
	}

	latest := ""
	if afterSeq > 0 {
		latest = strconv.FormatInt(afterSeq, 10)
	}
	if len(matched) > 0 {
		latest = strconv.FormatInt(matched[len(matched)-1].Seq, 10)
	}
	return ListMissedResult{
		Messages:     matched,
		LatestCursor: latest,
		HasMore:      hasMore,
	}, nil
}

func (m *MemStore) MarkRead(_ context.Context, conversationID, readerRole string, lastReadSeq int64, messageID string) (MarkReadResult, error) {
	if readerRole != RoleBuyer && readerRole != RoleSeller {
		return MarkReadResult{}, fmt.Errorf("%w: invalid reader role %q", ErrInvalidArgument, readerRole)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.convsByID[strings.TrimSpace(conversationID)]
	if !ok {
		return MarkReadResult{}, ErrNotFound
	}

	targetSeq := c.LastMessageSeq
	if trimmedMsgID := strings.TrimSpace(messageID); trimmedMsgID != "" {
		found := false
		for _, msg := range m.messages {
			if msg.ID == trimmedMsgID && msg.ConversationID == c.ID {
				targetSeq = msg.Seq
				found = true
				break
			}
		}
		if !found {
			return MarkReadResult{}, ErrNotFound
		}
	} else if lastReadSeq > 0 {
		targetSeq = lastReadSeq
		if c.LastMessageSeq > 0 && targetSeq > c.LastMessageSeq {
			targetSeq = c.LastMessageSeq
		}
	}

	now := time.Now().UTC()
	var updatedSeq int64
	if readerRole == RoleBuyer {
		if targetSeq > c.BuyerLastReadSeq {
			c.BuyerLastReadSeq = targetSeq
		}
		c.BuyerLastReadAt = &now
		updatedSeq = c.BuyerLastReadSeq
	} else {
		if targetSeq > c.SellerLastReadSeq {
			c.SellerLastReadSeq = targetSeq
		}
		c.SellerLastReadAt = &now
		updatedSeq = c.SellerLastReadSeq
	}
	if now.After(c.UpdatedAt) {
		c.UpdatedAt = now
	}

	return MarkReadResult{
		ConversationID: c.ID,
		LastReadSeq:    updatedSeq,
		UnreadCount:    m.countUnreadLocked(c, readerRole),
	}, nil
}

func (m *MemStore) ClaimAutoReplySlot(_ context.Context, conversationID string, now time.Time, cooldown time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.convsByID[strings.TrimSpace(conversationID)]
	if !ok {
		return false, ErrNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if cooldown < 0 {
		cooldown = 0
	}
	cutoff := now.Add(-cooldown)
	if c.LastAutoReplyAt != nil && c.LastAutoReplyAt.After(cutoff) {
		return false, nil
	}
	ts := now.UTC()
	c.LastAutoReplyAt = &ts
	if ts.After(c.UpdatedAt) {
		c.UpdatedAt = ts
	}
	return true, nil
}

func (m *MemStore) GetAutoReplyConfig(_ context.Context, shopID string) (AutoReplyConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, ok := m.autoReplies[strings.TrimSpace(shopID)]
	if !ok {
		return AutoReplyConfig{}, ErrNotFound
	}
	return cloneAutoReplyConfig(cfg), nil
}

func (m *MemStore) UpsertAutoReplyConfig(_ context.Context, cfg AutoReplyConfig) (AutoReplyConfig, error) {
	cfg.ShopID = strings.TrimSpace(cfg.ShopID)
	if cfg.ShopID == "" {
		return AutoReplyConfig{}, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	cfg.UpdatedAt = time.Now().UTC()
	if cfg.KeywordRules == nil {
		cfg.KeywordRules = []KeywordRule{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoReplies[cfg.ShopID] = cloneAutoReplyConfig(cfg)
	return cloneAutoReplyConfig(cfg), nil
}

func (m *MemStore) resolveCursorLocked(convID, raw string, allowZero bool) (int64, error) {
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 0 || (allowZero && n == 0) {
			return n, nil
		}
		return 0, fmt.Errorf("%w: non-positive cursor", ErrInvalidCursor)
	}
	if _, err := uuid.Parse(raw); err != nil {
		return 0, fmt.Errorf("%w: invalid cursor format", ErrInvalidCursor)
	}
	for _, msg := range m.messages {
		if msg.ID == raw && msg.ConversationID == convID {
			return msg.Seq, nil
		}
	}
	return 0, fmt.Errorf("%w: cursor message not found", ErrInvalidCursor)
}

func (m *MemStore) resolveGlobalCursorLocked(userID, shopID, raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("%w: negative cursor", ErrInvalidCursor)
		}
		return n, nil
	}
	if _, err := uuid.Parse(raw); err != nil {
		return 0, fmt.Errorf("%w: invalid cursor format", ErrInvalidCursor)
	}
	for _, msg := range m.messages {
		if msg.ID == raw {
			if c, ok := m.convsByID[msg.ConversationID]; ok && (c.BuyerID == userID || (shopID != "" && c.ShopID == shopID)) {
				return msg.Seq, nil
			}
			break
		}
	}
	return 0, fmt.Errorf("%w: cursor message not found", ErrInvalidCursor)
}

func cloneAutoReplyConfig(in AutoReplyConfig) AutoReplyConfig {
	out := in
	out.KeywordRules = make([]KeywordRule, len(in.KeywordRules))
	for i, r := range in.KeywordRules {
		kws := make([]string, len(r.Keywords))
		copy(kws, r.Keywords)
		out.KeywordRules[i] = KeywordRule{
			Keywords: kws,
			Reply:    r.Reply,
		}
	}
	return out
}
