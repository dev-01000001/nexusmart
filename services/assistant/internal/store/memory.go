package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is a thread-safe in-memory Store implementation for unit tests,
// standalone dev mode, and offline evaluation runners.
type MemoryStore struct {
	mu              sync.RWMutex
	conversations   map[string]Conversation
	messages        map[string][]Message
	feedback        []Feedback
	reviews         map[string]map[string]Review // productID -> reviewID -> Review
	reviewSummaries map[string]ReviewSummary
	handoffs        map[string]HandoffTicket
	usageLogs       []UsageLog
	processedEvents map[string]time.Time
}

// NewMemoryStore constructs an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		conversations:   make(map[string]Conversation),
		messages:        make(map[string][]Message),
		reviews:         make(map[string]map[string]Review),
		reviewSummaries: make(map[string]ReviewSummary),
		handoffs:        make(map[string]HandoffTicket),
		processedEvents: make(map[string]time.Time),
	}
}

func (m *MemoryStore) EnsureConversation(_ context.Context, conv Conversation) (Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	if conv.ID == "" {
		conv.ID = uuid.NewString()
	}
	if existing, ok := m.conversations[conv.ID]; ok {
		if conv.UserID != "" && existing.UserID == "" {
			existing.UserID = conv.UserID
		}
		if conv.ShopID != "" && existing.ShopID == "" {
			existing.ShopID = conv.ShopID
		}
		existing.UpdatedAt = now
		m.conversations[conv.ID] = existing
		return existing, nil
	}
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	conv.UpdatedAt = now
	m.conversations[conv.ID] = conv
	return conv, nil
}

func (m *MemoryStore) SaveMessage(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = now
	}
	msg.Content = RedactPII(msg.Content)
	msg.ToolCallsJSON = normalizeJSONArray(msg.ToolCallsJSON)
	msg.RetrievedIDsJSON = normalizeJSONArray(msg.RetrievedIDsJSON)

	if _, ok := m.conversations[msg.ConversationID]; !ok && msg.ConversationID != "" {
		m.conversations[msg.ConversationID] = Conversation{
			ID:        msg.ConversationID,
			CreatedAt: now,
			UpdatedAt: now,
		}
	} else if conv, ok := m.conversations[msg.ConversationID]; ok {
		conv.UpdatedAt = now
		m.conversations[msg.ConversationID] = conv
	}
	m.messages[msg.ConversationID] = append(m.messages[msg.ConversationID], msg)
	return nil
}

func (m *MemoryStore) ListMessages(_ context.Context, conversationID string, limit int) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := m.messages[conversationID]
	if len(list) == 0 {
		return nil, nil
	}
	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}
	out := make([]Message, len(list))
	copy(out, list)
	return out, nil
}

func (m *MemoryStore) SaveFeedback(_ context.Context, fb Feedback) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fb.ID == "" {
		fb.ID = uuid.NewString()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now().UTC()
	}
	fb.Comment = RedactPII(fb.Comment)
	m.feedback = append(m.feedback, fb)
	return nil
}

func (m *MemoryStore) UpsertReviews(_ context.Context, reviews []Review) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	for _, r := range reviews {
		if r.ProductID == "" {
			continue
		}
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		}
		if r.Rating < 1 {
			r.Rating = 1
		} else if r.Rating > 5 {
			r.Rating = 5
		}
		r.Comment = RedactPII(r.Comment)
		if m.reviews[r.ProductID] == nil {
			m.reviews[r.ProductID] = make(map[string]Review)
		}
		m.reviews[r.ProductID][r.ID] = r
	}
	return nil
}

func (m *MemoryStore) ListReviewsByProduct(_ context.Context, productID string) ([]Review, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byID := m.reviews[productID]
	if len(byID) == 0 {
		return nil, nil
	}
	out := make([]Review, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemoryStore) GetReviewSummary(_ context.Context, productID string) (*ReviewSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.reviewSummaries[productID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := s
	cp.Pros = append([]string(nil), s.Pros...)
	cp.Cons = append([]string(nil), s.Cons...)
	return &cp, nil
}

func (m *MemoryStore) UpsertReviewSummary(_ context.Context, summary ReviewSummary) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = time.Now().UTC()
	}
	cp := summary
	if existing, ok := m.reviewSummaries[summary.ProductID]; ok && existing.ShopID != "" {
		cp.ShopID = existing.ShopID
	}
	cp.Pros = append([]string(nil), summary.Pros...)
	cp.Cons = append([]string(nil), summary.Cons...)
	m.reviewSummaries[summary.ProductID] = cp
	return nil
}

func (m *MemoryStore) SaveHandoffTicket(_ context.Context, ticket HandoffTicket) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ticket.ID == "" {
		ticket.ID = uuid.NewString()
	}
	if ticket.CreatedAt.IsZero() {
		ticket.CreatedAt = time.Now().UTC()
	}
	if ticket.Status == "" {
		ticket.Status = "open"
	}
	ticket.Reason = RedactPII(ticket.Reason)
	ticket.Summary = RedactPII(ticket.Summary)
	m.handoffs[ticket.ID] = ticket
	return nil
}

func (m *MemoryStore) GetHandoffTicket(_ context.Context, id string) (*HandoffTicket, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.handoffs[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := t
	return &cp, nil
}

func (m *MemoryStore) SaveUsageLog(_ context.Context, log UsageLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if log.ID == "" {
		log.ID = uuid.NewString()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	m.usageLogs = append(m.usageLogs, log)
	return nil
}

func (m *MemoryStore) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.processedEvents[eventID]
	return exists, nil
}

func (m *MemoryStore) MarkEventProcessed(_ context.Context, eventID, _ string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processedEvents[eventID]; exists {
		return false, nil
	}
	m.processedEvents[eventID] = time.Now().UTC()
	return true, nil
}

func (m *MemoryStore) Ping(_ context.Context) error {
	return nil
}

// UsageLogs returns a snapshot of recorded usage logs (for tests and metrics verification).
func (m *MemoryStore) UsageLogs() []UsageLog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]UsageLog, len(m.usageLogs))
	copy(out, m.usageLogs)
	return out
}

// FeedbackEntries returns a snapshot of recorded feedback entries (for tests).
func (m *MemoryStore) FeedbackEntries() []Feedback {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Feedback, len(m.feedback))
	copy(out, m.feedback)
	return out
}
