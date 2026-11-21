package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store backed by PostgreSQL (pgxpool).
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore constructs a PostgreSQL-backed chat store.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// Ping verifies database connectivity.
func (s *PGStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PGStore) CreateOrGetConversation(ctx context.Context, buyerID, shopID, siteHost string) (Conversation, bool, error) {
	buyerID = strings.TrimSpace(buyerID)
	shopID = strings.TrimSpace(shopID)
	siteHost = strings.TrimSpace(siteHost)
	if buyerID == "" || shopID == "" {
		return Conversation{}, false, fmt.Errorf("%w: buyer_id and shop_id are required", ErrInvalidArgument)
	}

	id := uuid.New()
	now := time.Now().UTC()

	row := s.pool.QueryRow(ctx, `
		INSERT INTO chat_conversations (id, buyer_id, shop_id, site_host, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (buyer_id, shop_id) DO NOTHING
		RETURNING id, buyer_id, shop_id, site_host,
		          last_message_id, last_message_seq, last_message_preview, last_message_sender_role, last_message_at,
		          buyer_last_read_seq, buyer_last_read_at, seller_last_read_seq, seller_last_read_at,
		          last_auto_reply_at, created_at, updated_at
	`, id, buyerID, shopID, siteHost, now)

	conv, err := scanConversation(row)
	if err == nil {
		return conv, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, false, fmt.Errorf("insert conversation: %w", err)
	}

	// Already exists: return existing conversation (idempotent).
	row = s.pool.QueryRow(ctx, `
		SELECT id, buyer_id, shop_id, site_host,
		       last_message_id, last_message_seq, last_message_preview, last_message_sender_role, last_message_at,
		       buyer_last_read_seq, buyer_last_read_at, seller_last_read_seq, seller_last_read_at,
		       last_auto_reply_at, created_at, updated_at
		FROM chat_conversations
		WHERE buyer_id = $1 AND shop_id = $2
	`, buyerID, shopID)
	conv, err = scanConversation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, false, ErrNotFound
		}
		return Conversation{}, false, fmt.Errorf("get existing conversation: %w", err)
	}
	return conv, false, nil
}

func (s *PGStore) GetConversation(ctx context.Context, conversationID string) (Conversation, error) {
	id, err := uuid.Parse(strings.TrimSpace(conversationID))
	if err != nil {
		return Conversation{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, buyer_id, shop_id, site_host,
		       last_message_id, last_message_seq, last_message_preview, last_message_sender_role, last_message_at,
		       buyer_last_read_seq, buyer_last_read_at, seller_last_read_seq, seller_last_read_at,
		       last_auto_reply_at, created_at, updated_at
		FROM chat_conversations
		WHERE id = $1
	`, id)
	conv, err := scanConversation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrNotFound
		}
		return Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return conv, nil
}

func (s *PGStore) CountUnread(ctx context.Context, conversationID, viewerRole string) (int64, error) {
	id, err := uuid.Parse(strings.TrimSpace(conversationID))
	if err != nil {
		return 0, ErrNotFound
	}
	var query string
	if viewerRole == RoleSeller {
		query = `
			SELECT COUNT(*)
			FROM chat_messages m
			JOIN chat_conversations c ON c.id = m.conversation_id
			WHERE c.id = $1 AND m.seq > c.seller_last_read_seq AND m.sender_role <> 'seller'
		`
	} else {
		query = `
			SELECT COUNT(*)
			FROM chat_messages m
			JOIN chat_conversations c ON c.id = m.conversation_id
			WHERE c.id = $1 AND m.seq > c.buyer_last_read_seq AND m.sender_role <> 'buyer'
		`
	}
	var unread int64
	if err := s.pool.QueryRow(ctx, query, id).Scan(&unread); err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return unread, nil
}

func (s *PGStore) ListConversations(ctx context.Context, filter ListConversationsFilter) (ListConversationsResult, error) {
	page, pageSize := normalizePagination(filter.Page, filter.PageSize)
	scope := strings.ToLower(strings.TrimSpace(filter.Scope))

	if scope == ScopeSeller && strings.TrimSpace(filter.CallerShopID) == "" {
		return ListConversationsResult{Conversations: []Conversation{}}, nil
	}

	var (
		where []string
		args  []any
	)
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	var unreadExpr string
	switch scope {
	case ScopeBuyer:
		pUser := addArg(filter.UserID)
		where = append(where, "c.buyer_id = "+pUser)
		unreadExpr = "(SELECT COUNT(*) FROM chat_messages m WHERE m.conversation_id = c.id AND m.seq > c.buyer_last_read_seq AND m.sender_role <> 'buyer')"
	case ScopeSeller:
		pShop := addArg(filter.CallerShopID)
		where = append(where, "c.shop_id = "+pShop)
		unreadExpr = "(SELECT COUNT(*) FROM chat_messages m WHERE m.conversation_id = c.id AND m.seq > c.seller_last_read_seq AND m.sender_role <> 'seller')"
	default:
		pUser := addArg(filter.UserID)
		if strings.TrimSpace(filter.CallerShopID) != "" {
			pShop := addArg(filter.CallerShopID)
			where = append(where, fmt.Sprintf("(c.buyer_id = %s OR c.shop_id = %s)", pUser, pShop))
			unreadExpr = fmt.Sprintf(`CASE
				WHEN c.buyer_id = %s THEN (SELECT COUNT(*) FROM chat_messages m WHERE m.conversation_id = c.id AND m.seq > c.buyer_last_read_seq AND m.sender_role <> 'buyer')
				ELSE (SELECT COUNT(*) FROM chat_messages m WHERE m.conversation_id = c.id AND m.seq > c.seller_last_read_seq AND m.sender_role <> 'seller')
			END`, pUser)
		} else {
			where = append(where, "c.buyer_id = "+pUser)
			unreadExpr = "(SELECT COUNT(*) FROM chat_messages m WHERE m.conversation_id = c.id AND m.seq > c.buyer_last_read_seq AND m.sender_role <> 'buyer')"
		}
	}

	if fs := strings.TrimSpace(filter.FilterShopID); fs != "" {
		pFilterShop := addArg(fs)
		where = append(where, "c.shop_id = "+pFilterShop)
	}
	if filter.UnrepliedOnly {
		where = append(where, "c.last_message_sender_role = 'buyer'")
	}

	whereSQL := strings.Join(where, " AND ")

	var total, totalUnread int64
	countSQL := fmt.Sprintf(`
		SELECT COUNT(*), COALESCE(SUM(sub.unread_count), 0)
		FROM (
			SELECT %s AS unread_count
			FROM chat_conversations c
			WHERE %s
		) sub
	`, unreadExpr, whereSQL)
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total, &totalUnread); err != nil {
		return ListConversationsResult{}, fmt.Errorf("count conversations: %w", err)
	}

	offset := (page - 1) * pageSize
	pLimit := addArg(pageSize)
	pOffset := addArg(offset)

	listSQL := fmt.Sprintf(`
		SELECT c.id, c.buyer_id, c.shop_id, c.site_host,
		       c.last_message_id, c.last_message_seq, c.last_message_preview, c.last_message_sender_role, c.last_message_at,
		       c.buyer_last_read_seq, c.buyer_last_read_at, c.seller_last_read_seq, c.seller_last_read_at,
		       c.last_auto_reply_at, c.created_at, c.updated_at,
		       %s AS unread_count
		FROM chat_conversations c
		WHERE %s
		ORDER BY COALESCE(c.last_message_at, c.created_at) DESC, c.created_at DESC
		LIMIT %s OFFSET %s
	`, unreadExpr, whereSQL, pLimit, pOffset)

	rows, err := s.pool.Query(ctx, listSQL, args...)
	if err != nil {
		return ListConversationsResult{}, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	convs := make([]Conversation, 0)
	for rows.Next() {
		conv, scanErr := scanConversationWithUnread(rows)
		if scanErr != nil {
			return ListConversationsResult{}, fmt.Errorf("scan conversation: %w", scanErr)
		}
		convs = append(convs, conv)
	}
	if err := rows.Err(); err != nil {
		return ListConversationsResult{}, fmt.Errorf("iterate conversations: %w", err)
	}

	return ListConversationsResult{
		Conversations: convs,
		Total:         total,
		TotalUnread:   totalUnread,
	}, nil
}

func (s *PGStore) CreateMessage(ctx context.Context, in CreateMessageInput) (Message, bool, error) {
	convUUID, err := uuid.Parse(strings.TrimSpace(in.ConversationID))
	if err != nil {
		return Message{}, false, ErrNotFound
	}

	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	metaBytes := []byte("{}")
	if len(in.Metadata) > 0 {
		metaBytes, err = json.Marshal(in.Metadata)
		if err != nil {
			return Message{}, false, fmt.Errorf("marshal message metadata: %w", err)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, false, fmt.Errorf("begin create message tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	msgID := uuid.New()
	clientMsgID := strings.TrimSpace(in.ClientMsgID)

	var row pgx.Row
	if clientMsgID != "" {
		row = tx.QueryRow(ctx, `
			INSERT INTO chat_messages (id, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)
			ON CONFLICT (conversation_id, sender_id, client_msg_id) WHERE client_msg_id <> '' DO NOTHING
			RETURNING id, seq, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at
		`, msgID, convUUID, in.SenderID, in.SenderRole, in.Kind, in.Body, in.RefID, string(metaBytes), in.IsAutoReply, clientMsgID, now)
	} else {
		row = tx.QueryRow(ctx, `
			INSERT INTO chat_messages (id, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, '', $10)
			RETURNING id, seq, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at
		`, msgID, convUUID, in.SenderID, in.SenderRole, in.Kind, in.Body, in.RefID, string(metaBytes), in.IsAutoReply, now)
	}

	msg, err := scanMessage(row)
	if err != nil {
		if clientMsgID != "" && errors.Is(err, pgx.ErrNoRows) {
			// Idempotent retry: return the original persisted message without updating conversation again.
			existingRow := tx.QueryRow(ctx, `
				SELECT id, seq, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at
				FROM chat_messages
				WHERE conversation_id = $1 AND sender_id = $2 AND client_msg_id = $3
			`, convUUID, in.SenderID, clientMsgID)
			existing, getErr := scanMessage(existingRow)
			if getErr != nil {
				return Message{}, false, fmt.Errorf("fetch idempotent message: %w", getErr)
			}
			return existing, false, nil
		}
		return Message{}, false, fmt.Errorf("insert message: %w", err)
	}

	preview := BuildMessagePreview(msg.Kind, msg.Body, msg.RefID)
	tag, err := tx.Exec(ctx, `
		UPDATE chat_conversations
		SET last_message_id = $1,
		    last_message_seq = GREATEST(last_message_seq, $2),
		    last_message_preview = $3,
		    last_message_sender_role = $4,
		    last_message_at = $5,
		    updated_at = GREATEST(updated_at, $5)
		WHERE id = $6
	`, msgID, msg.Seq, preview, msg.SenderRole, msg.CreatedAt, convUUID)
	if err != nil {
		return Message{}, false, fmt.Errorf("update conversation last message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Message{}, false, ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return Message{}, false, fmt.Errorf("commit create message tx: %w", err)
	}
	return msg, true, nil
}

func (s *PGStore) ListMessages(ctx context.Context, in ListMessagesInput) (ListMessagesResult, error) {
	convUUID, err := uuid.Parse(strings.TrimSpace(in.ConversationID))
	if err != nil {
		return ListMessagesResult{}, ErrNotFound
	}

	before := strings.TrimSpace(in.Before)
	after := strings.TrimSpace(in.After)
	if before != "" && after != "" {
		return ListMessagesResult{}, fmt.Errorf("%w: before and after cursors are mutually exclusive", ErrInvalidArgument)
	}

	limit := normalizeMessageLimit(in.Limit)
	fetchLimit := limit + 1

	var rows pgx.Rows
	var reverseOrder bool

	switch {
	case after != "":
		afterSeq, rErr := s.resolveConversationCursor(ctx, convUUID, after, true)
		if rErr != nil {
			return ListMessagesResult{}, rErr
		}
		rows, err = s.pool.Query(ctx, `
			SELECT id, seq, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at
			FROM chat_messages
			WHERE conversation_id = $1 AND seq > $2
			ORDER BY seq ASC
			LIMIT $3
		`, convUUID, afterSeq, fetchLimit)
	case before != "":
		beforeSeq, rErr := s.resolveConversationCursor(ctx, convUUID, before, false)
		if rErr != nil {
			return ListMessagesResult{}, rErr
		}
		reverseOrder = true
		rows, err = s.pool.Query(ctx, `
			SELECT id, seq, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at
			FROM chat_messages
			WHERE conversation_id = $1 AND seq < $2
			ORDER BY seq DESC
			LIMIT $3
		`, convUUID, beforeSeq, fetchLimit)
	default:
		reverseOrder = true
		rows, err = s.pool.Query(ctx, `
			SELECT id, seq, conversation_id, sender_id, sender_role, kind, body, ref_id, metadata, is_auto_reply, client_msg_id, created_at
			FROM chat_messages
			WHERE conversation_id = $1
			ORDER BY seq DESC
			LIMIT $2
		`, convUUID, fetchLimit)
	}
	if err != nil {
		return ListMessagesResult{}, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	msgs := make([]Message, 0, limit)
	for rows.Next() {
		m, scanErr := scanMessage(rows)
		if scanErr != nil {
			return ListMessagesResult{}, fmt.Errorf("scan message: %w", scanErr)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return ListMessagesResult{}, fmt.Errorf("iterate messages: %w", err)
	}

	hasMore := false
	if int32(len(msgs)) > limit {
		hasMore = true
		msgs = msgs[:limit]
	}

	if reverseOrder {
		reverseMessages(msgs)
	}

	res := ListMessagesResult{
		Messages: msgs,
		HasMore:  hasMore,
	}
	if len(msgs) > 0 {
		if reverseOrder && hasMore {
			res.NextBefore = strconv.FormatInt(msgs[0].Seq, 10)
		} else if !reverseOrder {
			res.NextBefore = strconv.FormatInt(msgs[0].Seq, 10)
		}
		res.LatestCursor = strconv.FormatInt(msgs[len(msgs)-1].Seq, 10)
	} else if after != "" {
		res.LatestCursor = after
	}
	return res, nil
}

func (s *PGStore) ListMissedMessages(ctx context.Context, userID, shopID, afterCursor string, limit int32) (ListMissedResult, error) {
	afterSeq, err := s.resolveGlobalCursor(ctx, afterCursor)
	if err != nil {
		return ListMissedResult{}, err
	}

	lim := normalizeMessageLimit(limit)
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.seq, m.conversation_id, m.sender_id, m.sender_role, m.kind, m.body, m.ref_id, m.metadata, m.is_auto_reply, m.client_msg_id, m.created_at
		FROM chat_messages m
		JOIN chat_conversations c ON c.id = m.conversation_id
		WHERE (c.buyer_id = $1 OR ($2 <> '' AND c.shop_id = $2))
		  AND m.seq > $3
		ORDER BY m.seq ASC
		LIMIT $4
	`, userID, strings.TrimSpace(shopID), afterSeq, lim+1)
	if err != nil {
		return ListMissedResult{}, fmt.Errorf("list missed messages: %w", err)
	}
	defer rows.Close()

	msgs := make([]Message, 0, lim)
	for rows.Next() {
		m, scanErr := scanMessage(rows)
		if scanErr != nil {
			return ListMissedResult{}, fmt.Errorf("scan missed message: %w", scanErr)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return ListMissedResult{}, fmt.Errorf("iterate missed messages: %w", err)
	}

	hasMore := false
	if int32(len(msgs)) > lim {
		hasMore = true
		msgs = msgs[:lim]
	}

	latest := strconv.FormatInt(afterSeq, 10)
	if afterSeq == 0 {
		latest = ""
	}
	if len(msgs) > 0 {
		latest = strconv.FormatInt(msgs[len(msgs)-1].Seq, 10)
	}
	return ListMissedResult{
		Messages:     msgs,
		LatestCursor: latest,
		HasMore:      hasMore,
	}, nil
}

func (s *PGStore) MarkRead(ctx context.Context, conversationID, readerRole string, lastReadSeq int64, messageID string) (MarkReadResult, error) {
	convUUID, err := uuid.Parse(strings.TrimSpace(conversationID))
	if err != nil {
		return MarkReadResult{}, ErrNotFound
	}
	if readerRole != RoleBuyer && readerRole != RoleSeller {
		return MarkReadResult{}, fmt.Errorf("%w: invalid reader role %q", ErrInvalidArgument, readerRole)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MarkReadResult{}, fmt.Errorf("begin mark read tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var convLastSeq int64
	if err := tx.QueryRow(ctx, `SELECT last_message_seq FROM chat_conversations WHERE id = $1`, convUUID).Scan(&convLastSeq); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MarkReadResult{}, ErrNotFound
		}
		return MarkReadResult{}, fmt.Errorf("fetch conversation last seq: %w", err)
	}

	targetSeq := convLastSeq
	if trimmedMsgID := strings.TrimSpace(messageID); trimmedMsgID != "" {
		msgUUID, parseErr := uuid.Parse(trimmedMsgID)
		if parseErr != nil {
			return MarkReadResult{}, fmt.Errorf("%w: invalid message_id", ErrInvalidArgument)
		}
		if err := tx.QueryRow(ctx, `SELECT seq FROM chat_messages WHERE id = $1 AND conversation_id = $2`, msgUUID, convUUID).Scan(&targetSeq); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return MarkReadResult{}, ErrNotFound
			}
			return MarkReadResult{}, fmt.Errorf("lookup message seq: %w", err)
		}
	} else if lastReadSeq > 0 {
		targetSeq = lastReadSeq
		if convLastSeq > 0 && targetSeq > convLastSeq {
			targetSeq = convLastSeq
		}
	}

	var updatedSeq int64
	now := time.Now().UTC()
	if readerRole == RoleBuyer {
		err = tx.QueryRow(ctx, `
			UPDATE chat_conversations
			SET buyer_last_read_seq = GREATEST(buyer_last_read_seq, $2),
			    buyer_last_read_at = $3,
			    updated_at = GREATEST(updated_at, $3)
			WHERE id = $1
			RETURNING buyer_last_read_seq
		`, convUUID, targetSeq, now).Scan(&updatedSeq)
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE chat_conversations
			SET seller_last_read_seq = GREATEST(seller_last_read_seq, $2),
			    seller_last_read_at = $3,
			    updated_at = GREATEST(updated_at, $3)
			WHERE id = $1
			RETURNING seller_last_read_seq
		`, convUUID, targetSeq, now).Scan(&updatedSeq)
	}
	if err != nil {
		return MarkReadResult{}, fmt.Errorf("update last read seq: %w", err)
	}

	var unread int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_messages
		WHERE conversation_id = $1 AND seq > $2 AND sender_role <> $3
	`, convUUID, updatedSeq, readerRole).Scan(&unread); err != nil {
		return MarkReadResult{}, fmt.Errorf("count remaining unread: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return MarkReadResult{}, fmt.Errorf("commit mark read tx: %w", err)
	}

	return MarkReadResult{
		ConversationID: convUUID.String(),
		LastReadSeq:    updatedSeq,
		UnreadCount:    unread,
	}, nil
}

func (s *PGStore) ClaimAutoReplySlot(ctx context.Context, conversationID string, now time.Time, cooldown time.Duration) (bool, error) {
	convUUID, err := uuid.Parse(strings.TrimSpace(conversationID))
	if err != nil {
		return false, ErrNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if cooldown < 0 {
		cooldown = 0
	}
	cutoff := now.Add(-cooldown)

	tag, err := s.pool.Exec(ctx, `
		UPDATE chat_conversations
		SET last_auto_reply_at = $2,
		    updated_at = GREATEST(updated_at, $2)
		WHERE id = $1
		  AND (last_auto_reply_at IS NULL OR last_auto_reply_at <= $3)
	`, convUUID, now.UTC(), cutoff.UTC())
	if err != nil {
		return false, fmt.Errorf("claim auto reply slot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PGStore) GetAutoReplyConfig(ctx context.Context, shopID string) (AutoReplyConfig, error) {
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		return AutoReplyConfig{}, ErrNotFound
	}
	var (
		cfg       AutoReplyConfig
		rulesJSON []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT shop_id, enabled, greeting_message, offline_enabled, offline_message,
		       work_start_hour, work_end_hour, timezone, cooldown_minutes, keyword_rules, updated_at
		FROM chat_shop_auto_replies
		WHERE shop_id = $1
	`, shopID).Scan(
		&cfg.ShopID,
		&cfg.Enabled,
		&cfg.GreetingMessage,
		&cfg.OfflineEnabled,
		&cfg.OfflineMessage,
		&cfg.WorkStartHour,
		&cfg.WorkEndHour,
		&cfg.Timezone,
		&cfg.CooldownMinutes,
		&rulesJSON,
		&cfg.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AutoReplyConfig{}, ErrNotFound
		}
		return AutoReplyConfig{}, fmt.Errorf("get auto reply config: %w", err)
	}
	if len(rulesJSON) > 0 {
		if err := json.Unmarshal(rulesJSON, &cfg.KeywordRules); err != nil {
			return AutoReplyConfig{}, fmt.Errorf("unmarshal keyword rules: %w", err)
		}
	}
	if cfg.KeywordRules == nil {
		cfg.KeywordRules = []KeywordRule{}
	}
	return cfg, nil
}

func (s *PGStore) UpsertAutoReplyConfig(ctx context.Context, cfg AutoReplyConfig) (AutoReplyConfig, error) {
	cfg.ShopID = strings.TrimSpace(cfg.ShopID)
	if cfg.ShopID == "" {
		return AutoReplyConfig{}, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if cfg.KeywordRules == nil {
		cfg.KeywordRules = []KeywordRule{}
	}
	rulesJSON, err := json.Marshal(cfg.KeywordRules)
	if err != nil {
		return AutoReplyConfig{}, fmt.Errorf("marshal keyword rules: %w", err)
	}
	now := time.Now().UTC()
	var savedRules []byte
	var out AutoReplyConfig
	err = s.pool.QueryRow(ctx, `
		INSERT INTO chat_shop_auto_replies (
			shop_id, enabled, greeting_message, offline_enabled, offline_message,
			work_start_hour, work_end_hour, timezone, cooldown_minutes, keyword_rules, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
		ON CONFLICT (shop_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			greeting_message = EXCLUDED.greeting_message,
			offline_enabled = EXCLUDED.offline_enabled,
			offline_message = EXCLUDED.offline_message,
			work_start_hour = EXCLUDED.work_start_hour,
			work_end_hour = EXCLUDED.work_end_hour,
			timezone = EXCLUDED.timezone,
			cooldown_minutes = EXCLUDED.cooldown_minutes,
			keyword_rules = EXCLUDED.keyword_rules,
			updated_at = EXCLUDED.updated_at
		RETURNING shop_id, enabled, greeting_message, offline_enabled, offline_message,
		          work_start_hour, work_end_hour, timezone, cooldown_minutes, keyword_rules, updated_at
	`, cfg.ShopID, cfg.Enabled, cfg.GreetingMessage, cfg.OfflineEnabled, cfg.OfflineMessage,
		cfg.WorkStartHour, cfg.WorkEndHour, cfg.Timezone, cfg.CooldownMinutes, string(rulesJSON), now,
	).Scan(
		&out.ShopID,
		&out.Enabled,
		&out.GreetingMessage,
		&out.OfflineEnabled,
		&out.OfflineMessage,
		&out.WorkStartHour,
		&out.WorkEndHour,
		&out.Timezone,
		&out.CooldownMinutes,
		&savedRules,
		&out.UpdatedAt,
	)
	if err != nil {
		return AutoReplyConfig{}, fmt.Errorf("upsert auto reply config: %w", err)
	}
	if len(savedRules) > 0 {
		if err := json.Unmarshal(savedRules, &out.KeywordRules); err != nil {
			return AutoReplyConfig{}, fmt.Errorf("unmarshal saved keyword rules: %w", err)
		}
	}
	if out.KeywordRules == nil {
		out.KeywordRules = []KeywordRule{}
	}
	return out, nil
}

func (s *PGStore) resolveConversationCursor(ctx context.Context, convUUID uuid.UUID, raw string, allowZero bool) (int64, error) {
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 0 || (allowZero && n == 0) {
			return n, nil
		}
		return 0, fmt.Errorf("%w: non-positive sequence cursor", ErrInvalidCursor)
	}
	msgUUID, err := uuid.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: cursor must be numeric seq or message uuid", ErrInvalidCursor)
	}
	var seq int64
	if err := s.pool.QueryRow(ctx, `SELECT seq FROM chat_messages WHERE id = $1 AND conversation_id = $2`, msgUUID, convUUID).Scan(&seq); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: cursor message not found", ErrInvalidCursor)
		}
		return 0, fmt.Errorf("resolve cursor uuid: %w", err)
	}
	return seq, nil
}

func (s *PGStore) resolveGlobalCursor(ctx context.Context, raw string) (int64, error) {
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
	msgUUID, err := uuid.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: cursor must be numeric seq or message uuid", ErrInvalidCursor)
	}
	var seq int64
	if err := s.pool.QueryRow(ctx, `SELECT seq FROM chat_messages WHERE id = $1`, msgUUID).Scan(&seq); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: cursor message not found", ErrInvalidCursor)
		}
		return 0, fmt.Errorf("resolve global cursor uuid: %w", err)
	}
	return seq, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanConversation(r rowScanner) (Conversation, error) {
	var (
		c             Conversation
		id            pgtype.UUID
		lastMsgID     pgtype.UUID
		lastMsgAt     pgtype.Timestamptz
		buyerReadAt   pgtype.Timestamptz
		sellerReadAt  pgtype.Timestamptz
		lastAutoReply pgtype.Timestamptz
	)
	err := r.Scan(
		&id,
		&c.BuyerID,
		&c.ShopID,
		&c.SiteHost,
		&lastMsgID,
		&c.LastMessageSeq,
		&c.LastMessagePreview,
		&c.LastMessageSenderRole,
		&lastMsgAt,
		&c.BuyerLastReadSeq,
		&buyerReadAt,
		&c.SellerLastReadSeq,
		&sellerReadAt,
		&lastAutoReply,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		return Conversation{}, err
	}
	c.ID = formatPGUUID(id)
	c.LastMessageID = formatPGUUID(lastMsgID)
	c.LastMessageAt = formatPGTimestamptz(lastMsgAt)
	c.BuyerLastReadAt = formatPGTimestamptz(buyerReadAt)
	c.SellerLastReadAt = formatPGTimestamptz(sellerReadAt)
	c.LastAutoReplyAt = formatPGTimestamptz(lastAutoReply)
	return c, nil
}

func scanConversationWithUnread(r rowScanner) (Conversation, error) {
	var (
		c             Conversation
		id            pgtype.UUID
		lastMsgID     pgtype.UUID
		lastMsgAt     pgtype.Timestamptz
		buyerReadAt   pgtype.Timestamptz
		sellerReadAt  pgtype.Timestamptz
		lastAutoReply pgtype.Timestamptz
	)
	err := r.Scan(
		&id,
		&c.BuyerID,
		&c.ShopID,
		&c.SiteHost,
		&lastMsgID,
		&c.LastMessageSeq,
		&c.LastMessagePreview,
		&c.LastMessageSenderRole,
		&lastMsgAt,
		&c.BuyerLastReadSeq,
		&buyerReadAt,
		&c.SellerLastReadSeq,
		&sellerReadAt,
		&lastAutoReply,
		&c.CreatedAt,
		&c.UpdatedAt,
		&c.UnreadCount,
	)
	if err != nil {
		return Conversation{}, err
	}
	c.ID = formatPGUUID(id)
	c.LastMessageID = formatPGUUID(lastMsgID)
	c.LastMessageAt = formatPGTimestamptz(lastMsgAt)
	c.BuyerLastReadAt = formatPGTimestamptz(buyerReadAt)
	c.SellerLastReadAt = formatPGTimestamptz(sellerReadAt)
	c.LastAutoReplyAt = formatPGTimestamptz(lastAutoReply)
	return c, nil
}

func scanMessage(r rowScanner) (Message, error) {
	var (
		m        Message
		id       pgtype.UUID
		convID   pgtype.UUID
		metaJSON []byte
	)
	err := r.Scan(
		&id,
		&m.Seq,
		&convID,
		&m.SenderID,
		&m.SenderRole,
		&m.Kind,
		&m.Body,
		&m.RefID,
		&metaJSON,
		&m.IsAutoReply,
		&m.ClientMsgID,
		&m.CreatedAt,
	)
	if err != nil {
		return Message{}, err
	}
	m.ID = formatPGUUID(id)
	m.ConversationID = formatPGUUID(convID)
	if len(metaJSON) > 0 && string(metaJSON) != "{}" {
		var meta map[string]any
		if unmarshalErr := json.Unmarshal(metaJSON, &meta); unmarshalErr == nil && len(meta) > 0 {
			m.Metadata = meta
		}
	}
	return m, nil
}

func formatPGUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func formatPGTimestamptz(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	utc := t.Time.UTC()
	return &utc
}

func reverseMessages(msgs []Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}
