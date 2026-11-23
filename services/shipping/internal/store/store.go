package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/migrations"
)

// ErrNotFound is returned when a shipment or resource does not exist.
var (
	ErrNotFound        = errors.New("not found")
	ErrCannotCancel    = errors.New("shipment cannot be cancelled in current status")
	ErrCarrierMismatch = errors.New("webhook carrier does not match shipment carrier")
)

func isTerminalStatus(status string) bool {
	return status == "delivered" || status == "returned" || status == "cancelled"
}

// Shipment represents a persisted shipping consignment.
type Shipment struct {
	ID               string
	OrderID          string
	ShopID           string
	CarrierCode      string
	ServiceCode      string
	TrackingCode     string
	LabelURL         string
	Status           string
	FeeVND           int64
	CODAmountVND     int64
	WeightGrams      int32
	FromProvinceCode string
	FromWardCode     string
	ToProvinceCode   string
	ToWardCode       string
	LastEventAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ShipmentEvent represents a status transition entry in a shipment timeline.
type ShipmentEvent struct {
	ID          string
	ShipmentID  string
	Status      string
	RawStatus   string
	Description string
	OccurredAt  time.Time
}

// OutboxEvent represents an event queued for NATS JetStream publication.
type OutboxEvent struct {
	ID          string
	EventID     string
	Subject     string
	Payload     []byte
	Traceparent string
	TraceID     string
	CreatedAt   time.Time
}

// WebhookUpdateParams carries normalized carrier webhook data to apply atomically.
type WebhookUpdateParams struct {
	EventID            string
	CarrierCode        string
	TrackingCode       string
	OrderID            string
	InternalStatus     string
	RawStatus          string
	Description        string
	OccurredAt         time.Time
	OutboxEventID      string
	OutboxSubject      string
	BuildOutboxPayload func(sh *Shipment) ([]byte, error)
	Traceparent        string
	TraceID            string
}

// WebhookUpdateOutcome indicates how a carrier webhook was handled.
type WebhookUpdateOutcome string

const (
	WebhookOutcomeApplied   WebhookUpdateOutcome = "applied"
	WebhookOutcomeDuplicate WebhookUpdateOutcome = "duplicate"
	WebhookOutcomeStale     WebhookUpdateOutcome = "stale"
)

// Repository defines persistence operations for the shipping service.
type Repository interface {
	ListProvinces(ctx context.Context, query string) ([]address.Province, error)
	GetProvince(ctx context.Context, provinceCode string) (*address.Province, error)
	ListWards(ctx context.Context, provinceCode, query string) ([]address.Ward, error)
	GetWard(ctx context.Context, wardCode string) (*address.Ward, error)
	MapLegacyAddress(ctx context.Context, legacyProv, legacyDist, legacyWard string) (*address.LegacyMatch, error)

	GetShipmentByOrderID(ctx context.Context, orderID string) (*Shipment, error)
	GetShipmentByTrackingCode(ctx context.Context, trackingCode string) (*Shipment, error)
	ListShipmentEvents(ctx context.Context, shipmentID string) ([]ShipmentEvent, error)
	CreateShipment(ctx context.Context, s *Shipment, initialEvent *ShipmentEvent) (*Shipment, bool, error)
	CancelShipment(ctx context.Context, orderID, reason string) (*Shipment, error)
	CancelShipmentWithEvent(ctx context.Context, eventID, subject, orderID, reason string) (*Shipment, bool, error)
	ApplyWebhookEvent(ctx context.Context, params WebhookUpdateParams) (*Shipment, WebhookUpdateOutcome, error)
	ListActiveShipments(ctx context.Context, limit int) ([]Shipment, error)

	FetchPendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, id string) error

	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error)
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// PGStore implements Repository backed by PostgreSQL via pgx/v5.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore wraps an existing pgxpool.Pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// MigrateAndSeed applies embedded SQL migrations and idempotently seeds 34 provinces, wards, and legacy mappings.
func (s *PGStore) MigrateAndSeed(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		raw, readErr := fs.ReadFile(migrations.FS, name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		if _, execErr := s.pool.Exec(ctx, string(raw)); execErr != nil {
			return fmt.Errorf("apply migration %s: %w", name, execErr)
		}
	}

	return s.seedAddressCatalog(ctx)
}

func (s *PGStore) seedAddressCatalog(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range address.Provinces() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO provinces (code, name, region, ghn_province_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (code) DO UPDATE
			SET name = EXCLUDED.name, region = EXCLUDED.region, ghn_province_id = EXCLUDED.ghn_province_id
		`, p.Code, p.Name, p.Region, p.GHNProvinceID); err != nil {
			return fmt.Errorf("seed province %s: %w", p.Code, err)
		}
	}

	for _, w := range address.Wards() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO wards (code, province_code, name, ghn_district_id, ghn_ward_code)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (code) DO UPDATE
			SET province_code = EXCLUDED.province_code,
			    name = EXCLUDED.name,
			    ghn_district_id = EXCLUDED.ghn_district_id,
			    ghn_ward_code = EXCLUDED.ghn_ward_code
		`, w.Code, w.ProvinceCode, w.Name, w.GHNDistrictID, w.GHNWardCode); err != nil {
			return fmt.Errorf("seed ward %s: %w", w.Code, err)
		}
	}

	for _, m := range address.LegacyMappings() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO legacy_address_mappings (
				legacy_province, legacy_province_norm,
				legacy_district, legacy_district_norm,
				legacy_ward, legacy_ward_norm,
				province_code, ward_code,
				legacy_province_code, legacy_district_code, legacy_ward_code
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (legacy_province_norm, legacy_district_norm, legacy_ward_norm) DO NOTHING
		`,
			m.LegacyProvince, address.NormalizeName(m.LegacyProvince),
			m.LegacyDistrict, address.NormalizeName(m.LegacyDistrict),
			m.LegacyWard, address.NormalizeName(m.LegacyWard),
			m.ProvinceCode, m.WardCode,
			m.LegacyProvinceCode, m.LegacyDistrictCode, m.LegacyWardCode,
		); err != nil {
			return fmt.Errorf("seed legacy mapping %s: %w", m.LegacyProvince, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed tx: %w", err)
	}
	return nil
}

func (s *PGStore) ListProvinces(ctx context.Context, query string) ([]address.Province, error) {
	q := strings.TrimSpace(query)
	var (
		rows pgx.Rows
		err  error
	)
	if q == "" {
		rows, err = s.pool.Query(ctx, `SELECT code, name, region, ghn_province_id FROM provinces ORDER BY code ASC`)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT code, name, region, ghn_province_id
			FROM provinces
			WHERE name ILIKE '%' || $1 || '%' OR code = $1
			ORDER BY code ASC
		`, q)
	}
	if err != nil {
		return nil, fmt.Errorf("list provinces: %w", err)
	}
	defer rows.Close()

	var result []address.Province
	for rows.Next() {
		var p address.Province
		if err := rows.Scan(&p.Code, &p.Name, &p.Region, &p.GHNProvinceID); err != nil {
			return nil, fmt.Errorf("scan province: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *PGStore) GetProvince(ctx context.Context, provinceCode string) (*address.Province, error) {
	if strings.TrimSpace(provinceCode) == "" {
		return nil, ErrNotFound
	}
	var p address.Province
	err := s.pool.QueryRow(ctx, `
		SELECT code, name, region, ghn_province_id
		FROM provinces
		WHERE code = $1
	`, provinceCode).Scan(&p.Code, &p.Name, &p.Region, &p.GHNProvinceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get province: %w", err)
	}
	return &p, nil
}

func (s *PGStore) ListWards(ctx context.Context, provinceCode, query string) ([]address.Ward, error) {
	q := strings.TrimSpace(query)
	var (
		rows pgx.Rows
		err  error
	)
	if q == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT code, province_code, name, ghn_district_id, ghn_ward_code
			FROM wards
			WHERE province_code = $1
			ORDER BY code ASC
		`, provinceCode)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT code, province_code, name, ghn_district_id, ghn_ward_code
			FROM wards
			WHERE province_code = $1 AND (name ILIKE '%' || $2 || '%' OR code = $2)
			ORDER BY code ASC
		`, provinceCode, q)
	}
	if err != nil {
		return nil, fmt.Errorf("list wards: %w", err)
	}
	defer rows.Close()

	var result []address.Ward
	for rows.Next() {
		var w address.Ward
		if err := rows.Scan(&w.Code, &w.ProvinceCode, &w.Name, &w.GHNDistrictID, &w.GHNWardCode); err != nil {
			return nil, fmt.Errorf("scan ward: %w", err)
		}
		result = append(result, w)
	}
	return result, rows.Err()
}

func (s *PGStore) GetWard(ctx context.Context, wardCode string) (*address.Ward, error) {
	if strings.TrimSpace(wardCode) == "" {
		return nil, ErrNotFound
	}
	var w address.Ward
	err := s.pool.QueryRow(ctx, `
		SELECT code, province_code, name, ghn_district_id, ghn_ward_code
		FROM wards
		WHERE code = $1
	`, wardCode).Scan(&w.Code, &w.ProvinceCode, &w.Name, &w.GHNDistrictID, &w.GHNWardCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ward: %w", err)
	}
	return &w, nil
}

func (s *PGStore) MapLegacyAddress(ctx context.Context, legacyProv, legacyDist, legacyWard string) (*address.LegacyMatch, error) {
	pNorm := address.NormalizeName(legacyProv)
	dNorm := address.NormalizeName(legacyDist)
	wNorm := address.NormalizeName(legacyWard)
	if pNorm == "" {
		return &address.LegacyMatch{Found: false}, nil
	}

	// Score best match: exact (prov+dist+ward) > (prov+dist) > (prov default).
	var match address.LegacyMatch
	err := s.pool.QueryRow(ctx, `
		SELECT
			m.province_code, p.name,
			m.ward_code, w.name,
			m.legacy_province_code, m.legacy_district_code, m.legacy_ward_code
		FROM legacy_address_mappings m
		JOIN provinces p ON p.code = m.province_code
		JOIN wards w ON w.code = m.ward_code
		WHERE m.legacy_province_norm = $1
		  AND (
			(m.legacy_district_norm = $2 AND m.legacy_ward_norm = $3)
			OR (m.legacy_district_norm = $2 AND $2 <> '')
			OR (m.legacy_district_norm = '' AND m.legacy_ward_norm = '')
		  )
		ORDER BY
			CASE
				WHEN m.legacy_district_norm = $2 AND m.legacy_ward_norm = $3 AND $3 <> '' THEN 3
				WHEN m.legacy_district_norm = $2 AND $2 <> '' THEN 2
				ELSE 1
			END DESC,
			m.id ASC
		LIMIT 1
	`, pNorm, dNorm, wNorm).Scan(
		&match.ProvinceCode,
		&match.ProvinceName,
		&match.WardCode,
		&match.WardName,
		&match.LegacyProvinceCode,
		&match.LegacyDistrictCode,
		&match.LegacyWardCode,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &address.LegacyMatch{Found: false}, nil
		}
		return nil, fmt.Errorf("map legacy address: %w", err)
	}
	match.Found = true
	return &match, nil
}

func scanShipment(row pgx.Row) (*Shipment, error) {
	var sh Shipment
	err := row.Scan(
		&sh.ID,
		&sh.OrderID,
		&sh.ShopID,
		&sh.CarrierCode,
		&sh.ServiceCode,
		&sh.TrackingCode,
		&sh.LabelURL,
		&sh.Status,
		&sh.FeeVND,
		&sh.CODAmountVND,
		&sh.WeightGrams,
		&sh.FromProvinceCode,
		&sh.FromWardCode,
		&sh.ToProvinceCode,
		&sh.ToWardCode,
		&sh.LastEventAt,
		&sh.CreatedAt,
		&sh.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sh, nil
}

const shipmentColumns = `
	id, order_id, shop_id, carrier_code, service_code, tracking_code, label_url,
	status, fee_vnd, cod_amount_vnd, weight_grams,
	from_province_code, from_ward_code, to_province_code, to_ward_code,
	last_event_at, created_at, updated_at
`

func (s *PGStore) GetShipmentByOrderID(ctx context.Context, orderID string) (*Shipment, error) {
	return scanShipment(s.pool.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE order_id = $1`, orderID))
}

func (s *PGStore) GetShipmentByTrackingCode(ctx context.Context, trackingCode string) (*Shipment, error) {
	return scanShipment(s.pool.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE tracking_code = $1`, trackingCode))
}

func (s *PGStore) ListShipmentEvents(ctx context.Context, shipmentID string) ([]ShipmentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, shipment_id, status, raw_status, description, occurred_at
		FROM shipment_events
		WHERE shipment_id = $1
		ORDER BY occurred_at ASC, created_at ASC
	`, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("list shipment events: %w", err)
	}
	defer rows.Close()

	var out []ShipmentEvent
	for rows.Next() {
		var ev ShipmentEvent
		if err := rows.Scan(&ev.ID, &ev.ShipmentID, &ev.Status, &ev.RawStatus, &ev.Description, &ev.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan shipment event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CreateShipment inserts a shipment and its initial event atomically. If order_id already exists
// due to a concurrent request, it returns the existing shipment and alreadyExisted=true.
func (s *PGStore) CreateShipment(ctx context.Context, sh *Shipment, initialEvent *ShipmentEvent) (*Shipment, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin create shipment tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO shipments (
			id, order_id, shop_id, carrier_code, service_code, tracking_code, label_url,
			status, fee_vnd, cod_amount_vnd, weight_grams,
			from_province_code, from_ward_code, to_province_code, to_ward_code,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (order_id) DO NOTHING
	`,
		sh.ID, sh.OrderID, sh.ShopID, sh.CarrierCode, sh.ServiceCode, sh.TrackingCode, sh.LabelURL,
		sh.Status, sh.FeeVND, sh.CODAmountVND, sh.WeightGrams,
		sh.FromProvinceCode, sh.FromWardCode, sh.ToProvinceCode, sh.ToWardCode,
		sh.CreatedAt, sh.UpdatedAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert shipment: %w", err)
	}

	if tag.RowsAffected() == 0 {
		existing, fetchErr := scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE order_id = $1`, sh.OrderID))
		if fetchErr != nil {
			return nil, false, fmt.Errorf("fetch existing shipment on conflict: %w", fetchErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, false, fmt.Errorf("commit conflict read tx: %w", commitErr)
		}
		return existing, true, nil
	}

	if initialEvent != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, initialEvent.ID, sh.ID, initialEvent.Status, initialEvent.RawStatus, initialEvent.Description, initialEvent.OccurredAt); err != nil {
			return nil, false, fmt.Errorf("insert initial shipment event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit create shipment tx: %w", err)
	}
	return sh, false, nil
}

// CancelShipment marks a shipment as cancelled atomically only while still in CREATED/PICKING.
func (s *PGStore) CancelShipment(ctx context.Context, orderID, reason string) (*Shipment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel shipment tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, err := scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE order_id = $1 FOR UPDATE`, orderID))
	if err != nil {
		return nil, err
	}
	if existing.Status == "cancelled" {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit idempotent cancel tx: %w", err)
		}
		return existing, nil
	}
	if existing.Status != "created" && existing.Status != "picking" {
		return nil, fmt.Errorf("%w: current status is %s", ErrCannotCancel, existing.Status)
	}

	now := time.Now().UTC()
	sh, err := scanShipment(tx.QueryRow(ctx, `
		UPDATE shipments
		SET status = 'cancelled', updated_at = $2
		WHERE order_id = $1 AND status IN ('created', 'picking')
		RETURNING `+shipmentColumns, orderID, now))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrCannotCancel
		}
		return nil, err
	}

	desc := strings.TrimSpace(reason)
	if desc == "" {
		desc = "Shipment cancelled before pickup"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at)
		VALUES ($1, $2, 'cancelled', 'cancelled', $3, $4)
	`, uuid.NewString(), sh.ID, desc, now); err != nil {
		return nil, fmt.Errorf("insert cancel event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel tx: %w", err)
	}
	return sh, nil
}

// CancelShipmentWithEvent atomically records event_id in processed_events and cancels the shipment
// in ONE PostgreSQL transaction ONLY if the shipment is still in CREATED/PICKING.
func (s *PGStore) CancelShipmentWithEvent(ctx context.Context, eventID, subject, orderID, reason string) (*Shipment, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin cancel-with-event tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_events (event_id, subject)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, subject)
	if err != nil {
		return nil, false, fmt.Errorf("insert processed_event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit dup cancel-with-event tx: %w", err)
		}
		return nil, true, nil
	}

	existing, err := scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE order_id = $1 FOR UPDATE`, orderID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, false, fmt.Errorf("commit missing shipment cancel-with-event tx: %w", commitErr)
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	if existing.Status != "created" && existing.Status != "picking" {
		// Concurrent webhook moved shipment past pickup or already cancelled; do not regress status.
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, false, fmt.Errorf("commit non-cancellable cancel-with-event tx: %w", commitErr)
		}
		return existing, false, nil
	}

	now := time.Now().UTC()
	sh, err := scanShipment(tx.QueryRow(ctx, `
		UPDATE shipments
		SET status = 'cancelled', updated_at = $2
		WHERE order_id = $1 AND status IN ('created', 'picking')
		RETURNING `+shipmentColumns, orderID, now))
	if err != nil {
		return nil, false, err
	}

	desc := strings.TrimSpace(reason)
	if desc == "" {
		desc = "Shipment cancelled before pickup"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at)
		VALUES ($1, $2, 'cancelled', 'cancelled', $3, $4)
	`, uuid.NewString(), sh.ID, desc, now); err != nil {
		return nil, false, fmt.Errorf("insert cancel event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit cancel-with-event tx: %w", err)
	}
	return sh, false, nil
}

// ApplyWebhookEvent deduplicates by event_id, rejects stale out-of-order updates (UC-SHIP04),
// updates shipment + shipment_events, and writes shipment.updated to outbox_events in ONE transaction.
func (s *PGStore) ApplyWebhookEvent(ctx context.Context, params WebhookUpdateParams) (*Shipment, WebhookUpdateOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin webhook tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Lock shipment row by tracking_code (fallback order_id).
	var sh *Shipment
	if params.TrackingCode != "" {
		sh, err = scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE tracking_code = $1 FOR UPDATE`, params.TrackingCode))
	}
	if (sh == nil || errors.Is(err, ErrNotFound)) && params.OrderID != "" {
		sh, err = scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE order_id = $1 FOR UPDATE`, params.OrderID))
	}
	if err != nil {
		return nil, "", err
	}

	// 2. Verify webhook path carrier matches the shipment's persisted carrier_code (SHP-B6).
	if strings.TrimSpace(params.CarrierCode) == "" || !strings.EqualFold(strings.TrimSpace(params.CarrierCode), sh.CarrierCode) {
		return nil, "", fmt.Errorf("%w: webhook carrier %q does not match shipment carrier %q", ErrCarrierMismatch, params.CarrierCode, sh.CarrierCode)
	}

	// 3. Idempotency deduplication by event_id (after carrier validation so rejected calls never poison dedup).
	tag, err := tx.Exec(ctx, `
		INSERT INTO webhook_events (event_id, carrier_code, tracking_code, raw_status, occurred_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (event_id) DO NOTHING
	`, params.EventID, params.CarrierCode, params.TrackingCode, params.RawStatus, params.OccurredAt)
	if err != nil {
		return nil, "", fmt.Errorf("insert webhook_event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, "", fmt.Errorf("commit dup webhook tx: %w", err)
		}
		return nil, WebhookOutcomeDuplicate, nil
	}

	// 4. Reject out-of-order stale carrier events (UC-SHIP04) and prevent terminal state regressions.
	if isTerminalStatus(sh.Status) || (sh.LastEventAt != nil && params.OccurredAt.Before(*sh.LastEventAt)) {
		if err := tx.Commit(ctx); err != nil {
			return nil, "", fmt.Errorf("commit stale webhook tx: %w", err)
		}
		return sh, WebhookOutcomeStale, nil
	}

	// 4. Update shipment status + last_event_at.
	now := time.Now().UTC()
	updated, err := scanShipment(tx.QueryRow(ctx, `
		UPDATE shipments
		SET status = $2, last_event_at = $3, updated_at = $4
		WHERE id = $1
		RETURNING `+shipmentColumns, sh.ID, params.InternalStatus, params.OccurredAt, now))
	if err != nil {
		return nil, "", fmt.Errorf("update shipment status: %w", err)
	}

	// 5. Append shipment timeline event.
	if _, err := tx.Exec(ctx, `
		INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.NewString(), updated.ID, params.InternalStatus, params.RawStatus, params.Description, params.OccurredAt); err != nil {
		return nil, "", fmt.Errorf("insert shipment_event: %w", err)
	}

	// 6. Enqueue canonical shipment.updated event in outbox_events inside the SAME transaction.
	if params.BuildOutboxPayload != nil && params.OutboxSubject != "" {
		outboxPayload, buildErr := params.BuildOutboxPayload(updated)
		if buildErr != nil {
			return nil, "", fmt.Errorf("build outbox payload: %w", buildErr)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, payload, traceparent, trace_id)
			VALUES ($1, $2, $3, $4::jsonb, $5, $6)
		`, uuid.NewString(), params.OutboxEventID, params.OutboxSubject, string(outboxPayload), params.Traceparent, params.TraceID); err != nil {
			return nil, "", fmt.Errorf("insert outbox_event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit webhook tx: %w", err)
	}
	return updated, WebhookOutcomeApplied, nil
}

func (s *PGStore) ListActiveShipments(ctx context.Context, limit int) ([]Shipment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+shipmentColumns+`
		FROM shipments
		WHERE status IN ('created', 'picking', 'delivering', 'failed', 'returning')
		ORDER BY updated_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list active shipments: %w", err)
	}
	defer rows.Close()

	var out []Shipment
	for rows.Next() {
		var sh Shipment
		if err := rows.Scan(
			&sh.ID, &sh.OrderID, &sh.ShopID, &sh.CarrierCode, &sh.ServiceCode,
			&sh.TrackingCode, &sh.LabelURL, &sh.Status, &sh.FeeVND, &sh.CODAmountVND,
			&sh.WeightGrams, &sh.FromProvinceCode, &sh.FromWardCode, &sh.ToProvinceCode,
			&sh.ToWardCode, &sh.LastEventAt, &sh.CreatedAt, &sh.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active shipment: %w", err)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *PGStore) FetchPendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, event_id, subject, payload, traceparent, trace_id, created_at
		FROM outbox_events
		WHERE published_at IS NULL
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch pending outbox: %w", err)
	}
	defer rows.Close()

	var out []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		if err := rows.Scan(&ev.ID, &ev.EventID, &ev.Subject, &ev.Payload, &ev.Traceparent, &ev.TraceID, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *PGStore) MarkOutboxPublished(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox_events SET published_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *PGStore) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&exists)
	return exists, err
}

func (s *PGStore) MarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO processed_events (event_id, subject)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, subject)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PGStore) InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dead_letter_events (id, event_id, subject, payload, error_message, attempts)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.NewString(), eventID, subject, payload, errMsg, attempts)
	return err
}
