package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/notification/internal/db"
)

// Category constants matching proto NotificationCategory.
const (
	CategoryOrder     = "ORDER"
	CategoryPromotion = "PROMOTION"
	CategorySystem    = "SYSTEM"
)

// Notification is the domain model for an in-app notification.
type Notification struct {
	ID        string
	UserID    string
	Category  string
	Title     string
	Body      string
	Link      string
	EventID   string
	DedupKey  string
	IsRead    bool
	CreatedAt time.Time
}

// Preferences represents a user's channel preferences across notification categories.
type Preferences struct {
	UserID         string
	OrderInApp     bool
	OrderEmail     bool
	OrderPush      bool
	PromotionInApp bool
	PromotionEmail bool
	PromotionPush  bool
	SystemInApp    bool
	SystemEmail    bool
	SystemPush     bool
}

// DefaultPreferences returns all-enabled preferences for userID.
func DefaultPreferences(userID string) Preferences {
	return Preferences{
		UserID:         userID,
		OrderInApp:     true,
		OrderEmail:     true,
		OrderPush:      true,
		PromotionInApp: true,
		PromotionEmail: true,
		PromotionPush:  true,
		SystemInApp:    true,
		SystemEmail:    true,
		SystemPush:     true,
	}
}

// UserContact holds cached email/name for a user.
type UserContact struct {
	UserID   string
	Email    string
	FullName string
}

// OrderParticipant holds cached buyer/shop/order_code/checkout_group/storefront mapping for an order_id.
type OrderParticipant struct {
	OrderID         string
	BuyerID         string
	ShopID          string
	OrderCode       string
	CheckoutGroupID string
	// StorefrontShopID is the shop whose site the order was placed on; empty for MARKETPLACE orders.
	StorefrontShopID string
}

// ShopOwner holds cached shop_id -> owner_user_id mapping resolved via UserService.GetShop.
type ShopOwner struct {
	ShopID      string
	OwnerUserID string
	ShopName    string
}

// TxStore exposes DB-only operations executed atomically inside an idempotent event transaction.
type TxStore interface {
	InsertNotification(ctx context.Context, n Notification) (Notification, error)
	HasNotificationForEvent(ctx context.Context, eventID, userID string) (bool, error)
	GetPreferences(ctx context.Context, userID string) (Preferences, error)
	UpsertUserContact(ctx context.Context, c UserContact) error
	GetUserContact(ctx context.Context, userID string) (UserContact, bool, error)
	UpsertOrderParticipant(ctx context.Context, p OrderParticipant) error
	GetOrderParticipant(ctx context.Context, orderID string) (OrderParticipant, bool, error)
	ListOrderParticipantsByGroup(ctx context.Context, checkoutGroupID string) ([]OrderParticipant, error)
	UpsertShopOwner(ctx context.Context, s ShopOwner) error
	GetShopOwner(ctx context.Context, shopID string) (ShopOwner, bool, error)
	ClaimEmailDedup(ctx context.Context, dedupKey, eventID string) (ownerEventID string, err error)
	HasEmailDedup(ctx context.Context, dedupKey string) (bool, error)
}

// Repository defines all persistence operations for the notification service.
type Repository interface {
	ListNotifications(ctx context.Context, userID, category string, unreadOnly bool, limit, offset int32) ([]Notification, int64, int64, error)
	MarkRead(ctx context.Context, userID string, ids []string, markAll bool) (int32, int64, error)
	GetPreferences(ctx context.Context, userID string) (Preferences, error)
	UpsertPreferences(ctx context.Context, prefs Preferences) (Preferences, error)
	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	DeleteProcessedEvent(ctx context.Context, eventID string) error
	GetUserContact(ctx context.Context, userID string) (UserContact, bool, error)
	GetOrderParticipant(ctx context.Context, orderID string) (OrderParticipant, bool, error)
	ListOrderParticipantsByGroup(ctx context.Context, checkoutGroupID string) ([]OrderParticipant, error)
	GetShopOwner(ctx context.Context, shopID string) (ShopOwner, bool, error)
	RecordEmailDedup(ctx context.Context, dedupKey, eventID string) error
	ProcessEventTx(ctx context.Context, eventID, subject string, fn func(tx TxStore) error) (duplicate bool, err error)
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// PGStore implements Repository using pgx/v5 and sqlc-generated queries.
type PGStore struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewPGStore wraps pool with sqlc Queries.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{
		pool: pool,
		q:    db.New(pool),
	}
}

func (s *PGStore) ListNotifications(ctx context.Context, userID, category string, unreadOnly bool, limit, offset int32) ([]Notification, int64, int64, error) {
	rows, err := s.q.ListNotifications(ctx, db.ListNotificationsParams{
		UserID:  userID,
		Column2: category,
		Column3: unreadOnly,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list notifications: %w", err)
	}
	total, err := s.q.CountNotifications(ctx, db.CountNotificationsParams{
		UserID:  userID,
		Column2: category,
		Column3: unreadOnly,
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("count notifications: %w", err)
	}
	unread, err := s.q.CountUnreadNotifications(ctx, userID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("count unread notifications: %w", err)
	}

	out := make([]Notification, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBNotification(r))
	}
	return out, unread, total, nil
}

func (s *PGStore) MarkRead(ctx context.Context, userID string, ids []string, markAll bool) (int32, int64, error) {
	var updated int64
	var err error
	if markAll {
		updated, err = s.q.MarkAllNotificationsRead(ctx, userID)
	} else if len(ids) > 0 {
		pgIDs := make([]pgtype.UUID, 0, len(ids))
		for _, id := range ids {
			u, parseErr := db.ParseUUID(id)
			if parseErr != nil {
				continue
			}
			pgIDs = append(pgIDs, u)
		}
		if len(pgIDs) > 0 {
			updated, err = s.q.MarkSelectedNotificationsRead(ctx, db.MarkSelectedNotificationsReadParams{
				UserID:  userID,
				Column2: pgIDs,
			})
		}
	}
	if err != nil {
		return 0, 0, fmt.Errorf("mark notifications read: %w", err)
	}

	unread, err := s.q.CountUnreadNotifications(ctx, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("count unread after mark read: %w", err)
	}
	return int32(updated), unread, nil
}

func (s *PGStore) GetPreferences(ctx context.Context, userID string) (Preferences, error) {
	return getPreferencesFromQueries(ctx, s.q, userID)
}

func (s *PGStore) UpsertPreferences(ctx context.Context, prefs Preferences) (Preferences, error) {
	row, err := s.q.UpsertPreferences(ctx, db.UpsertPreferencesParams{
		UserID:         prefs.UserID,
		OrderInApp:     prefs.OrderInApp,
		OrderEmail:     prefs.OrderEmail,
		OrderPush:      prefs.OrderPush,
		PromotionInApp: prefs.PromotionInApp,
		PromotionEmail: prefs.PromotionEmail,
		PromotionPush:  prefs.PromotionPush,
		SystemInApp:    prefs.SystemInApp,
		SystemEmail:    prefs.SystemEmail,
		SystemPush:     prefs.SystemPush,
	})
	if err != nil {
		return Preferences{}, fmt.Errorf("upsert preferences: %w", err)
	}
	return fromDBPreferences(row), nil
}

// ProcessEventTx atomically records event_id in processed_events and runs fn inside the SAME Postgres transaction.
// If event_id was already recorded, duplicate=true is returned and fn is NOT called.
func (s *PGStore) ProcessEventTx(ctx context.Context, eventID, subject string, fn func(tx TxStore) error) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin event tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.q.WithTx(tx)
	affected, err := qtx.InsertProcessedEvent(ctx, db.InsertProcessedEventParams{
		EventID: eventID,
		Subject: subject,
	})
	if err != nil {
		return false, fmt.Errorf("insert processed_event: %w", err)
	}
	if affected == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit duplicate tx: %w", err)
		}
		return true, nil
	}

	if err := fn(&pgTxStore{q: qtx}); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit event tx: %w", err)
	}
	return false, nil
}

func (s *PGStore) InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error {
	return s.q.InsertDeadLetterEvent(ctx, db.InsertDeadLetterEventParams{
		ID:           db.NewUUID(),
		EventID:      eventID,
		Subject:      subject,
		Payload:      payload,
		ErrorMessage: errMsg,
		Attempts:     int32(attempts),
	})
}

func (s *PGStore) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	return s.q.IsEventProcessed(ctx, eventID)
}

func (s *PGStore) DeleteProcessedEvent(ctx context.Context, eventID string) error {
	return s.q.DeleteProcessedEvent(ctx, eventID)
}

func (s *PGStore) GetUserContact(ctx context.Context, userID string) (UserContact, bool, error) {
	return (&pgTxStore{q: s.q}).GetUserContact(ctx, userID)
}

func (s *PGStore) GetOrderParticipant(ctx context.Context, orderID string) (OrderParticipant, bool, error) {
	return (&pgTxStore{q: s.q}).GetOrderParticipant(ctx, orderID)
}

func (s *PGStore) ListOrderParticipantsByGroup(ctx context.Context, checkoutGroupID string) ([]OrderParticipant, error) {
	return (&pgTxStore{q: s.q}).ListOrderParticipantsByGroup(ctx, checkoutGroupID)
}

func (s *PGStore) GetShopOwner(ctx context.Context, shopID string) (ShopOwner, bool, error) {
	return (&pgTxStore{q: s.q}).GetShopOwner(ctx, shopID)
}

func (s *PGStore) RecordEmailDedup(ctx context.Context, dedupKey, eventID string) error {
	_, err := s.q.InsertEmailDedup(ctx, db.InsertEmailDedupParams{
		DedupKey: dedupKey,
		EventID:  eventID,
	})
	if err != nil {
		return fmt.Errorf("record email dedup: %w", err)
	}
	return nil
}

type pgTxStore struct {
	q *db.Queries
}

func (t *pgTxStore) HasNotificationForEvent(ctx context.Context, eventID, userID string) (bool, error) {
	return t.q.HasNotificationForEvent(ctx, db.HasNotificationForEventParams{
		EventID: eventID,
		UserID:  userID,
	})
}

func (t *pgTxStore) InsertNotification(ctx context.Context, n Notification) (Notification, error) {
	id := db.NewUUID()
	if n.ID != "" {
		if parsed, err := db.ParseUUID(n.ID); err == nil {
			id = parsed
		}
	}
	createdAt := n.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	row, err := t.q.InsertNotification(ctx, db.InsertNotificationParams{
		ID:        id,
		UserID:    n.UserID,
		Category:  n.Category,
		Title:     n.Title,
		Body:      n.Body,
		Link:      n.Link,
		EventID:   n.EventID,
		DedupKey:  n.DedupKey,
		CreatedAt: db.ToTimestamptz(createdAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Notification{}, nil
		}
		return Notification{}, fmt.Errorf("insert notification: %w", err)
	}
	return fromDBNotification(row), nil
}

func (t *pgTxStore) GetPreferences(ctx context.Context, userID string) (Preferences, error) {
	return getPreferencesFromQueries(ctx, t.q, userID)
}

func (t *pgTxStore) UpsertUserContact(ctx context.Context, c UserContact) error {
	if c.UserID == "" || c.Email == "" {
		return nil
	}
	return t.q.UpsertUserContact(ctx, db.UpsertUserContactParams{
		UserID:   c.UserID,
		Email:    c.Email,
		FullName: c.FullName,
	})
}

func (t *pgTxStore) GetUserContact(ctx context.Context, userID string) (UserContact, bool, error) {
	row, err := t.q.GetUserContact(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserContact{}, false, nil
		}
		return UserContact{}, false, err
	}
	return UserContact{
		UserID:   row.UserID,
		Email:    row.Email,
		FullName: row.FullName,
	}, true, nil
}

func (t *pgTxStore) UpsertOrderParticipant(ctx context.Context, p OrderParticipant) error {
	if p.OrderID == "" || p.BuyerID == "" {
		return nil
	}
	return t.q.UpsertOrderParticipant(ctx, db.UpsertOrderParticipantParams{
		OrderID:          p.OrderID,
		BuyerID:          p.BuyerID,
		ShopID:           p.ShopID,
		OrderCode:        p.OrderCode,
		CheckoutGroupID:  p.CheckoutGroupID,
		StorefrontShopID: p.StorefrontShopID,
	})
}

func (t *pgTxStore) GetOrderParticipant(ctx context.Context, orderID string) (OrderParticipant, bool, error) {
	row, err := t.q.GetOrderParticipant(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrderParticipant{}, false, nil
		}
		return OrderParticipant{}, false, err
	}
	return OrderParticipant{
		OrderID:          row.OrderID,
		BuyerID:          row.BuyerID,
		ShopID:           row.ShopID,
		OrderCode:        row.OrderCode,
		CheckoutGroupID:  row.CheckoutGroupID,
		StorefrontShopID: row.StorefrontShopID,
	}, true, nil
}

func (t *pgTxStore) ListOrderParticipantsByGroup(ctx context.Context, checkoutGroupID string) ([]OrderParticipant, error) {
	if checkoutGroupID == "" {
		return nil, nil
	}
	rows, err := t.q.ListOrderParticipantsByCheckoutGroup(ctx, checkoutGroupID)
	if err != nil {
		return nil, fmt.Errorf("list order participants by checkout group: %w", err)
	}
	out := make([]OrderParticipant, 0, len(rows))
	for _, row := range rows {
		out = append(out, OrderParticipant{
			OrderID:          row.OrderID,
			BuyerID:          row.BuyerID,
			ShopID:           row.ShopID,
			OrderCode:        row.OrderCode,
			CheckoutGroupID:  row.CheckoutGroupID,
			StorefrontShopID: row.StorefrontShopID,
		})
	}
	return out, nil
}

func (t *pgTxStore) UpsertShopOwner(ctx context.Context, s ShopOwner) error {
	if s.ShopID == "" || s.OwnerUserID == "" {
		return nil
	}
	return t.q.UpsertShopOwner(ctx, db.UpsertShopOwnerParams{
		ShopID:      s.ShopID,
		OwnerUserID: s.OwnerUserID,
		ShopName:    s.ShopName,
	})
}

func (t *pgTxStore) GetShopOwner(ctx context.Context, shopID string) (ShopOwner, bool, error) {
	if shopID == "" {
		return ShopOwner{}, false, nil
	}
	row, err := t.q.GetShopOwner(ctx, shopID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShopOwner{}, false, nil
		}
		return ShopOwner{}, false, err
	}
	return ShopOwner{
		ShopID:      row.ShopID,
		OwnerUserID: row.OwnerUserID,
		ShopName:    row.ShopName,
	}, true, nil
}

func (t *pgTxStore) ClaimEmailDedup(ctx context.Context, dedupKey, eventID string) (string, error) {
	affected, err := t.q.InsertEmailDedup(ctx, db.InsertEmailDedupParams{
		DedupKey: dedupKey,
		EventID:  eventID,
	})
	if err != nil {
		return "", fmt.Errorf("insert email dedup: %w", err)
	}
	if affected > 0 {
		return eventID, nil
	}
	existing, err := t.q.GetEmailDedupEventID(ctx, dedupKey)
	if err != nil {
		return "", fmt.Errorf("get email dedup: %w", err)
	}
	return existing, nil
}

func (t *pgTxStore) HasEmailDedup(ctx context.Context, dedupKey string) (bool, error) {
	_, err := t.q.GetEmailDedupEventID(ctx, dedupKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check email dedup: %w", err)
	}
	return true, nil
}

func getPreferencesFromQueries(ctx context.Context, q *db.Queries, userID string) (Preferences, error) {
	row, err := q.GetPreferences(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultPreferences(userID), nil
		}
		return Preferences{}, fmt.Errorf("get preferences: %w", err)
	}
	return fromDBPreferences(row), nil
}

func fromDBNotification(r db.Notification) Notification {
	return Notification{
		ID:        db.FormatUUID(r.ID),
		UserID:    r.UserID,
		Category:  r.Category,
		Title:     r.Title,
		Body:      r.Body,
		Link:      r.Link,
		EventID:   r.EventID,
		DedupKey:  r.DedupKey,
		IsRead:    r.IsRead,
		CreatedAt: r.CreatedAt.Time.UTC(),
	}
}

func fromDBPreferences(r db.NotificationPreference) Preferences {
	return Preferences{
		UserID:         r.UserID,
		OrderInApp:     r.OrderInApp,
		OrderEmail:     r.OrderEmail,
		OrderPush:      r.OrderPush,
		PromotionInApp: r.PromotionInApp,
		PromotionEmail: r.PromotionEmail,
		PromotionPush:  r.PromotionPush,
		SystemInApp:    r.SystemInApp,
		SystemEmail:    r.SystemEmail,
		SystemPush:     r.SystemPush,
	}
}
