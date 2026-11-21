package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store backed by PostgreSQL (assistant_db).
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore constructs a PostgreSQL-backed Store.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) EnsureConversation(ctx context.Context, conv Conversation) (Conversation, error) {
	if conv.ID == "" {
		conv.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	conv.UpdatedAt = now

	var out Conversation
	err := s.pool.QueryRow(ctx, `
		INSERT INTO assistant_conversations (id, user_id, shop_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			user_id = CASE WHEN assistant_conversations.user_id = '' THEN EXCLUDED.user_id ELSE assistant_conversations.user_id END,
			shop_id = CASE WHEN assistant_conversations.shop_id = '' THEN EXCLUDED.shop_id ELSE assistant_conversations.shop_id END,
			updated_at = EXCLUDED.updated_at
		RETURNING id, user_id, shop_id, created_at, updated_at
	`, conv.ID, conv.UserID, conv.ShopID, conv.CreatedAt, conv.UpdatedAt).Scan(
		&out.ID, &out.UserID, &out.ShopID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return Conversation{}, fmt.Errorf("ensure conversation: %w", err)
	}
	return out, nil
}

func (s *PGStore) SaveMessage(ctx context.Context, msg Message) error {
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	msg.Content = RedactPII(msg.Content)
	toolCalls := normalizeJSONArray(msg.ToolCallsJSON)
	retrievedIDs := normalizeJSONArray(msg.RetrievedIDsJSON)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO assistant_messages (id, conversation_id, role, content, tool_calls_json, retrieved_ids_json, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7)
	`, msg.ID, msg.ConversationID, msg.Role, msg.Content, string(toolCalls), string(retrievedIDs), msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}
	return nil
}

func (s *PGStore) ListMessages(ctx context.Context, conversationID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, conversation_id, role, content, tool_calls_json, retrieved_ids_json, created_at
		FROM (
			SELECT id, conversation_id, role, content, tool_calls_json, retrieved_ids_json, created_at
			FROM assistant_messages
			WHERE conversation_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) sub
		ORDER BY created_at ASC
	`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		var tcRaw, retRaw []byte
		if scanErr := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &tcRaw, &retRaw, &m.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("scan message: %w", scanErr)
		}
		m.ToolCallsJSON = json.RawMessage(tcRaw)
		m.RetrievedIDsJSON = json.RawMessage(retRaw)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}

func (s *PGStore) SaveFeedback(ctx context.Context, fb Feedback) error {
	if fb.ID == "" {
		fb.ID = uuid.NewString()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now().UTC()
	}
	fb.Comment = RedactPII(fb.Comment)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO assistant_feedback (id, conversation_id, message_id, user_id, helpful, comment, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, fb.ID, fb.ConversationID, fb.MessageID, fb.UserID, fb.Helpful, fb.Comment, fb.CreatedAt)
	if err != nil {
		return fmt.Errorf("save feedback: %w", err)
	}
	return nil
}

func (s *PGStore) UpsertReviews(ctx context.Context, reviews []Review) error {
	if len(reviews) == 0 {
		return nil
	}
	now := time.Now().UTC()
	batch := &pgx.Batch{}
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
		batch.Queue(`
			INSERT INTO assistant_reviews (id, product_id, shop_id, rating, comment, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET
				rating = EXCLUDED.rating,
				comment = EXCLUDED.comment
		`, r.ID, r.ProductID, r.ShopID, r.Rating, r.Comment, r.CreatedAt)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert review %d: %w", i, err)
		}
	}
	return nil
}

func (s *PGStore) ListReviewsByProduct(ctx context.Context, productID string) ([]Review, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, product_id, shop_id, rating, comment, created_at
		FROM assistant_reviews
		WHERE product_id = $1
		ORDER BY created_at ASC
	`, productID)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()

	var out []Review
	for rows.Next() {
		var r Review
		if scanErr := rows.Scan(&r.ID, &r.ProductID, &r.ShopID, &r.Rating, &r.Comment, &r.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("scan review: %w", scanErr)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reviews: %w", err)
	}
	return out, nil
}

func (s *PGStore) GetReviewSummary(ctx context.Context, productID string) (*ReviewSummary, error) {
	var out ReviewSummary
	var prosRaw, consRaw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT product_id, shop_id, review_count, average_rating, summary, pros_json, cons_json, updated_at
		FROM assistant_review_summaries
		WHERE product_id = $1
	`, productID).Scan(
		&out.ProductID, &out.ShopID, &out.ReviewCount, &out.AverageRating,
		&out.Summary, &prosRaw, &consRaw, &out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get review summary: %w", err)
	}
	_ = json.Unmarshal(prosRaw, &out.Pros)
	_ = json.Unmarshal(consRaw, &out.Cons)
	return &out, nil
}

func (s *PGStore) UpsertReviewSummary(ctx context.Context, summary ReviewSummary) error {
	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = time.Now().UTC()
	}
	prosBytes, err := json.Marshal(nonNilSlice(summary.Pros))
	if err != nil {
		return fmt.Errorf("marshal pros: %w", err)
	}
	consBytes, err := json.Marshal(nonNilSlice(summary.Cons))
	if err != nil {
		return fmt.Errorf("marshal cons: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO assistant_review_summaries (product_id, shop_id, review_count, average_rating, summary, pros_json, cons_json, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8)
		ON CONFLICT (product_id) DO UPDATE SET
			shop_id = EXCLUDED.shop_id,
			review_count = EXCLUDED.review_count,
			average_rating = EXCLUDED.average_rating,
			summary = EXCLUDED.summary,
			pros_json = EXCLUDED.pros_json,
			cons_json = EXCLUDED.cons_json,
			updated_at = EXCLUDED.updated_at
	`, summary.ProductID, summary.ShopID, summary.ReviewCount, summary.AverageRating,
		summary.Summary, string(prosBytes), string(consBytes), summary.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert review summary: %w", err)
	}
	return nil
}

func (s *PGStore) SaveHandoffTicket(ctx context.Context, ticket HandoffTicket) error {
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

	_, err := s.pool.Exec(ctx, `
		INSERT INTO assistant_handoff_tickets (id, user_id, shop_id, conversation_id, chat_conversation_id, reason, summary, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, ticket.ID, ticket.UserID, ticket.ShopID, ticket.ConversationID, ticket.ChatConversationID,
		ticket.Reason, ticket.Summary, ticket.Status, ticket.CreatedAt)
	if err != nil {
		return fmt.Errorf("save handoff ticket: %w", err)
	}
	return nil
}

func (s *PGStore) GetHandoffTicket(ctx context.Context, id string) (*HandoffTicket, error) {
	var t HandoffTicket
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, shop_id, conversation_id, chat_conversation_id, reason, summary, status, created_at
		FROM assistant_handoff_tickets
		WHERE id = $1
	`, id).Scan(
		&t.ID, &t.UserID, &t.ShopID, &t.ConversationID, &t.ChatConversationID,
		&t.Reason, &t.Summary, &t.Status, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get handoff ticket: %w", err)
	}
	return &t, nil
}

func (s *PGStore) SaveUsageLog(ctx context.Context, log UsageLog) error {
	if log.ID == "" {
		log.ID = uuid.NewString()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO assistant_usage_logs (
			id, trace_id, user_id, shop_id, endpoint, model,
			input_tokens, output_tokens, cost_vnd, latency_ms, ttft_ms,
			cache_hit, blocked_reason, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, log.ID, log.TraceID, log.UserID, log.ShopID, log.Endpoint, log.Model,
		log.InputTokens, log.OutputTokens, log.CostVND, log.LatencyMs, log.TTFTMs,
		log.CacheHit, log.BlockedReason, log.CreatedAt)
	if err != nil {
		return fmt.Errorf("save usage log: %w", err)
	}
	return nil
}

func (s *PGStore) MarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO assistant_processed_events (event_id, subject, processed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, subject)
	if err != nil {
		return false, fmt.Errorf("mark event processed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PGStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func nonNilSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
