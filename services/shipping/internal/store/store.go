package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/migrations"
)

const (
	DirectionForward = "FORWARD"
	DirectionReturn  = "RETURN"
)

// Sentinel errors returned by Repository implementations.
var (
	ErrNotFound                   = errors.New("not found")
	ErrCannotCancel               = errors.New("shipment cannot be cancelled in current status")
	ErrCarrierMismatch            = errors.New("webhook carrier does not match shipment carrier")
	ErrRedeliveryNotAllowed       = errors.New("shipment is not eligible for redelivery")
	ErrRedeliveryAlreadyRequested = errors.New("redelivery already requested for current failed delivery attempt")
	ErrMaxRedeliveryExceeded      = errors.New("maximum redelivery attempts exceeded")
	ErrLegacyNoBuyer              = errors.New("legacy shipment has no buyer_id recorded")
	ErrBuyerMismatch              = errors.New("buyer does not own this shipment")
)

func isTerminalStatus(status string) bool {
	return status == "delivered" || status == "returned" || status == "cancelled"
}

// CanTransitionStatus enforces monotonic webhook state transitions so late or clock-skewed
// webhooks cannot regress a shipment backwards (e.g., delivering/failed/returning -> created/picking,
// or returning -> delivering), while permitting valid redelivery (failed -> delivering) and repeat fails.
func CanTransitionStatus(from, to string) bool {
	if isTerminalStatus(from) {
		return false
	}
	switch from {
	case "created":
		return to == "created" || to == "picking" || to == "delivering" || to == "failed" ||
			to == "returning" || to == "delivered" || to == "returned" || to == "cancelled"
	case "picking":
		return to == "picking" || to == "delivering" || to == "failed" ||
			to == "returning" || to == "delivered" || to == "returned" || to == "cancelled"
	case "delivering":
		return to == "delivering" || to == "failed" || to == "returning" ||
			to == "delivered" || to == "returned" || to == "cancelled"
	case "failed":
		return to == "delivering" || to == "failed" || to == "returning" ||
			to == "delivered" || to == "returned" || to == "cancelled"
	case "returning":
		return to == "returning" || to == "returned" || to == "cancelled"
	default:
		return false
	}
}

// IsInactiveShipment reports whether a shipment no longer occupies the active slot for (order_id, direction).
// FORWARD: only cancelled is inactive. RETURN: both cancelled and returned are inactive so a new return shipment can be created.
func IsInactiveShipment(direction, status string) bool {
	return status == "cancelled" || (NormalizeDirection(direction) == DirectionReturn && status == "returned")
}

// NormalizeDirection normalizes empty/unspecified direction to FORWARD.
func NormalizeDirection(dir string) string {
	switch strings.ToUpper(strings.TrimSpace(dir)) {
	case "", DirectionForward:
		return DirectionForward
	case DirectionReturn:
		return DirectionReturn
	default:
		return strings.ToUpper(strings.TrimSpace(dir))
	}
}

// Shipment represents a persisted shipping consignment (FORWARD or RETURN).
type Shipment struct {
	ID               string
	OrderID          string
	ShopID           string
	BuyerID          string
	Direction        string
	ReturnRequestID  string
	RedeliveryCount  int32
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
	CreatedAt   time.Time
}

// ShopShippingSettings represents per-shop carrier and parcel defaults (UC-SHIP06).
type ShopShippingSettings struct {
	ShopID              string
	EnabledCarrierCodes []string
	DefaultWeightGrams  int32
	DefaultLengthCM     int32
	DefaultWidthCM      int32
	DefaultHeightCM     int32
	UpdatedAt           time.Time
}

// CarrierConfig represents platform-level carrier settings and encrypted API token (UC-A12).
type CarrierConfig struct {
	CarrierCode    string
	Name           string
	Enabled        bool
	EncryptedToken []byte
	UpdatedAt      time.Time
}

// FallbackRate represents one region row in the DB-backed fallback fee table (UC-A12).
type FallbackRate struct {
	CarrierCode        string
	Region             string
	BaseFeeVND         int64
	BaseWeightGrams    int32
	ExtraFeeVNDPer500g int64
	UpdatedAt          time.Time
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

	GetShipmentByOrderID(ctx context.Context, orderID, direction string) (*Shipment, error)
	GetActiveShipmentByOrderID(ctx context.Context, orderID, direction string) (*Shipment, error)
	CountInactiveShipmentsByOrder(ctx context.Context, orderID, direction string) (int, error)
	AcquireOrderDirectionLock(ctx context.Context, orderID, direction string) (func(), error)
	GetShipmentByTrackingCode(ctx context.Context, trackingCode string) (*Shipment, error)
	ListShipmentEvents(ctx context.Context, shipmentID string) ([]ShipmentEvent, error)
	CreateShipment(ctx context.Context, s *Shipment, initialEvent *ShipmentEvent) (*Shipment, bool, error)
	CancelShipmentByID(ctx context.Context, shipmentID, reason string) (*Shipment, error)
	CancelShipmentWithEvent(ctx context.Context, eventID, subject, orderID, reason string) (*Shipment, bool, error)
	RecordRedelivery(ctx context.Context, shipmentID, buyerID string, maxRedelivery int32, preferredDate, note string) (*Shipment, error)
	ApplyWebhookEvent(ctx context.Context, params WebhookUpdateParams) (*Shipment, WebhookUpdateOutcome, error)
	ListActiveShipments(ctx context.Context, limit int, afterUpdatedAt *time.Time, afterID string) ([]Shipment, error)

	GetShopShippingSettings(ctx context.Context, shopID string) (*ShopShippingSettings, error)
	UpsertShopShippingSettings(ctx context.Context, settings *ShopShippingSettings) (*ShopShippingSettings, error)

	ListCarrierConfigs(ctx context.Context) ([]CarrierConfig, error)
	GetCarrierConfig(ctx context.Context, carrierCode string) (*CarrierConfig, error)
	UpdateCarrierConfig(ctx context.Context, cfg *CarrierConfig, rates []FallbackRate) (*CarrierConfig, error)
	SeedCarrierTokenIfEmpty(ctx context.Context, carrierCode string, encryptedToken []byte) error
	ListFallbackRates(ctx context.Context, carrierCode string) ([]FallbackRate, error)

	FetchPendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, id string) error

	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error)
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// PGStore implements Repository backed by PostgreSQL via pgx/v5.
type PGStore struct {
	pool     *pgxpool.Pool
	lockPool *pgxpool.Pool
}

// NewPGStore wraps an existing pgxpool.Pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// NewPGStoreWithLockPool wraps a query pool and a dedicated advisory-lock pool so
// session locks held across external carrier calls never starve regular DB queries.
func NewPGStoreWithLockPool(pool, lockPool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool, lockPool: lockPool}
}

// Fixed advisory lock key ("SHIPMIGR") serializing concurrent startup migrations across replicas.
const (
	migrateAdvisoryLockKey     int64 = 0x534849504D494752
	defaultLockPoolMaxConns    int32 = 16
	defaultAdvisoryLockTimeout       = 5 * time.Second
)

// ponytail: dedicated advisory-lock pool (MaxConns=16) caps concurrent in-flight shipment creations
// per shipping replica at 16 without ever competing with the main query pool; excess callers fail fast
// with Unavailable after 5s. Upgrade path: raise MaxConns or use a DB reservation row if concurrency > 16/replica.
func NewLockPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse lock pool config: %w", err)
	}
	cfg.MaxConns = defaultLockPoolMaxConns
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create lock pool: %w", err)
	}
	return pool, nil
}

func orderDirectionAdvisoryLockKey(orderID, direction string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("shipping:create:"))
	_, _ = h.Write([]byte(NormalizeDirection(direction)))
	_, _ = h.Write([]byte(":"))
	_, _ = h.Write([]byte(strings.TrimSpace(orderID)))
	return int64(h.Sum64())
}

// AcquireOrderDirectionLock serializes shipment creation for (orderID, direction) across replicas using a
// session-scoped PostgreSQL advisory lock on the dedicated lock pool (without holding a DB transaction
// during external carrier HTTP calls). Waits at most 5s for a lock slot/lock before failing fast.
func (s *PGStore) AcquireOrderDirectionLock(ctx context.Context, orderID, direction string) (func(), error) {
	lockCtx, cancel := context.WithTimeout(ctx, defaultAdvisoryLockTimeout)
	defer cancel()

	lp := s.lockPool
	if lp == nil {
		lp = s.pool
	}

	conn, err := lp.Acquire(lockCtx)
	if err != nil {
		return nil, fmt.Errorf("acquire shipment lock connection: %w", err)
	}

	key := orderDirectionAdvisoryLockKey(orderID, direction)
	if _, err := conn.Exec(lockCtx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		// Close the backend session on lock acquisition failure/cancellation so Postgres guarantees
		// no session-level advisory lock can ever leak back into the connection pool.
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = conn.Conn().Close(closeCtx)
		closeCancel()
		conn.Release()
		return nil, fmt.Errorf("acquire shipment advisory lock: %w", err)
	}

	var once sync.Once
	unlock := func() {
		once.Do(func() {
			unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer unlockCancel()
			if _, unlockErr := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, key); unlockErr != nil {
				closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = conn.Conn().Close(closeCtx)
				closeCancel()
			}
			conn.Release()
		})
	}
	return unlock, nil
}

// MigrateAndSeed applies embedded SQL migrations and idempotently seeds 34 provinces, wards, and legacy mappings.
// Acquires a session-scoped PostgreSQL advisory lock so concurrent replicas starting simultaneously never race on DDL or seeds.
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

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrateAdvisoryLockKey)
	}()

	for _, name := range files {
		raw, readErr := fs.ReadFile(migrations.FS, name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		if _, execErr := conn.Exec(ctx, string(raw)); execErr != nil {
			return fmt.Errorf("apply migration %s: %w", name, execErr)
		}
	}

	return s.seedAddressCatalog(ctx, conn)
}

func (s *PGStore) seedAddressCatalog(ctx context.Context, conn *pgxpool.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range address.Provinces() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO provinces (code, name, name_norm, region, ghn_province_id)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (code) DO UPDATE
			SET name = EXCLUDED.name,
			    name_norm = EXCLUDED.name_norm,
			    region = EXCLUDED.region,
			    ghn_province_id = EXCLUDED.ghn_province_id
		`, p.Code, p.Name, address.StripDiacritics(p.Name), p.Region, p.GHNProvinceID); err != nil {
			return fmt.Errorf("seed province %s: %w", p.Code, err)
		}
	}

	for _, w := range address.Wards() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO wards (code, province_code, name, name_norm, ghn_district_id, ghn_ward_code)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (code) DO UPDATE
			SET province_code = EXCLUDED.province_code,
			    name = EXCLUDED.name,
			    name_norm = EXCLUDED.name_norm,
			    ghn_district_id = EXCLUDED.ghn_district_id,
			    ghn_ward_code = EXCLUDED.ghn_ward_code
		`, w.Code, w.ProvinceCode, w.Name, address.StripDiacritics(w.Name), w.GHNDistrictID, w.GHNWardCode); err != nil {
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
		qNorm := address.StripDiacritics(q)
		qAlias := address.NormalizeName(q)
		rows, err = s.pool.Query(ctx, `
			SELECT code, name, region, ghn_province_id
			FROM provinces
			WHERE name ILIKE '%' || $1 || '%'
			   OR ($2 <> '' AND name_norm LIKE '%' || $2 || '%')
			   OR ($3 <> '' AND name_norm LIKE '%' || $3 || '%')
			   OR code = $1
			ORDER BY code ASC
		`, q, qNorm, qAlias)
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
		qNorm := address.StripDiacritics(q)
		qAlias := address.NormalizeName(q)
		rows, err = s.pool.Query(ctx, `
			SELECT code, province_code, name, ghn_district_id, ghn_ward_code
			FROM wards
			WHERE province_code = $1
			  AND (
				name ILIKE '%' || $2 || '%'
				OR ($3 <> '' AND name_norm LIKE '%' || $3 || '%')
				OR ($4 <> '' AND name_norm LIKE '%' || $4 || '%')
				OR code = $2
			  )
			ORDER BY code ASC
		`, provinceCode, q, qNorm, qAlias)
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

const shipmentColumns = `
	id, order_id, shop_id, buyer_id, direction, return_request_id, redelivery_count,
	carrier_code, service_code, tracking_code, label_url,
	status, fee_vnd, cod_amount_vnd, weight_grams,
	from_province_code, from_ward_code, to_province_code, to_ward_code,
	last_event_at, created_at, updated_at
`

func scanShipment(row pgx.Row) (*Shipment, error) {
	var sh Shipment
	err := row.Scan(
		&sh.ID,
		&sh.OrderID,
		&sh.ShopID,
		&sh.BuyerID,
		&sh.Direction,
		&sh.ReturnRequestID,
		&sh.RedeliveryCount,
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

// GetShipmentByOrderID returns the active shipment for (order_id, direction),
// or the latest inactive shipment (cancelled; for RETURN also returned) if none is active.
func (s *PGStore) GetShipmentByOrderID(ctx context.Context, orderID, direction string) (*Shipment, error) {
	dir := NormalizeDirection(direction)
	return scanShipment(s.pool.QueryRow(ctx, `
		SELECT `+shipmentColumns+`
		FROM shipments
		WHERE order_id = $1 AND direction = $2
		ORDER BY (status = 'cancelled' OR (direction = 'RETURN' AND status = 'returned')) ASC, created_at DESC
		LIMIT 1
	`, orderID, dir))
}

// GetActiveShipmentByOrderID returns the active shipment for (order_id, direction).
// FORWARD excludes cancelled; RETURN excludes both cancelled and returned.
func (s *PGStore) GetActiveShipmentByOrderID(ctx context.Context, orderID, direction string) (*Shipment, error) {
	dir := NormalizeDirection(direction)
	return scanShipment(s.pool.QueryRow(ctx, `
		SELECT `+shipmentColumns+`
		FROM shipments
		WHERE order_id = $1 AND direction = $2
		  AND status <> 'cancelled'
		  AND NOT (direction = 'RETURN' AND status = 'returned')
		ORDER BY created_at DESC
		LIMIT 1
	`, orderID, dir))
}

// CountInactiveShipmentsByOrder returns how many inactive shipments exist for (order_id, direction)
// (FORWARD: cancelled; RETURN: cancelled + returned), used to derive a stable, unique carrier client_order_code.
func (s *PGStore) CountInactiveShipmentsByOrder(ctx context.Context, orderID, direction string) (int, error) {
	dir := NormalizeDirection(direction)
	var count int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM shipments
		WHERE order_id = $1 AND direction = $2
		  AND (status = 'cancelled' OR (direction = 'RETURN' AND status = 'returned'))
	`, orderID, dir).Scan(&count); err != nil {
		return 0, fmt.Errorf("count inactive shipments: %w", err)
	}
	return count, nil
}

func (s *PGStore) GetShipmentByTrackingCode(ctx context.Context, trackingCode string) (*Shipment, error) {
	return scanShipment(s.pool.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE tracking_code = $1`, trackingCode))
}

func (s *PGStore) ListShipmentEvents(ctx context.Context, shipmentID string) ([]ShipmentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, shipment_id, status, raw_status, description, occurred_at, created_at
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
		if err := rows.Scan(&ev.ID, &ev.ShipmentID, &ev.Status, &ev.RawStatus, &ev.Description, &ev.OccurredAt, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan shipment event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CreateShipment inserts a shipment and its initial event atomically.
// Enforces the partial unique index uq_shipments_order_direction_active
// (status <> 'cancelled' AND NOT (direction = 'RETURN' AND status = 'returned')):
// inactive shipments do not block recreating a shipment, while concurrent active inserts return alreadyExisted=true.
func (s *PGStore) CreateShipment(ctx context.Context, sh *Shipment, initialEvent *ShipmentEvent) (*Shipment, bool, error) {
	sh.Direction = NormalizeDirection(sh.Direction)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin create shipment tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO shipments (
			id, order_id, shop_id, buyer_id, direction, return_request_id, redelivery_count,
			carrier_code, service_code, tracking_code, label_url,
			status, fee_vnd, cod_amount_vnd, weight_grams,
			from_province_code, from_ward_code, to_province_code, to_ward_code,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT (order_id, direction) WHERE status <> 'cancelled' AND NOT (direction = 'RETURN' AND status = 'returned') DO NOTHING
	`,
		sh.ID, sh.OrderID, sh.ShopID, sh.BuyerID, sh.Direction, sh.ReturnRequestID, sh.RedeliveryCount,
		sh.CarrierCode, sh.ServiceCode, sh.TrackingCode, sh.LabelURL,
		sh.Status, sh.FeeVND, sh.CODAmountVND, sh.WeightGrams,
		sh.FromProvinceCode, sh.FromWardCode, sh.ToProvinceCode, sh.ToWardCode,
		sh.CreatedAt, sh.UpdatedAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert shipment: %w", err)
	}

	if tag.RowsAffected() == 0 {
		existing, fetchErr := scanShipment(tx.QueryRow(ctx, `
			SELECT `+shipmentColumns+`
			FROM shipments
			WHERE order_id = $1 AND direction = $2
			  AND status <> 'cancelled'
			  AND NOT (direction = 'RETURN' AND status = 'returned')
			ORDER BY created_at DESC
			LIMIT 1
		`, sh.OrderID, sh.Direction))
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

// CancelShipmentByID locks the shipment row FOR UPDATE and transitions it to cancelled
// only while still in CREATED or PICKING (guards against concurrent pickup webhooks).
func (s *PGStore) CancelShipmentByID(ctx context.Context, shipmentID, reason string) (*Shipment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel shipment tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, err := scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE id = $1 FOR UPDATE`, shipmentID))
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
		WHERE id = $1 AND status IN ('created', 'picking')
		RETURNING `+shipmentColumns, shipmentID, now))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrCannotCancel
		}
		return nil, err
	}

	desc := formatCancelDescriptionVI(reason)
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

// CancelShipmentWithEvent atomically records event_id in processed_events and cancels the active FORWARD shipment
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

	existing, err := scanShipment(tx.QueryRow(ctx, `
		SELECT `+shipmentColumns+`
		FROM shipments
		WHERE order_id = $1 AND direction = 'FORWARD' AND status <> 'cancelled'
		LIMIT 1
		FOR UPDATE
	`, orderID))
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
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, false, fmt.Errorf("commit non-cancellable cancel-with-event tx: %w", commitErr)
		}
		return existing, false, nil
	}

	now := time.Now().UTC()
	sh, err := scanShipment(tx.QueryRow(ctx, `
		UPDATE shipments
		SET status = 'cancelled', updated_at = $2
		WHERE id = $1 AND status IN ('created', 'picking')
		RETURNING `+shipmentColumns, existing.ID, now))
	if err != nil {
		return nil, false, err
	}

	desc := formatCancelDescriptionVI(reason)
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

// RecordRedelivery locks the shipment row FOR UPDATE, validates buyer ownership, failed status,
// and max redelivery attempts, increments redelivery_count, and appends a Vietnamese timeline event.
func (s *PGStore) RecordRedelivery(ctx context.Context, shipmentID, buyerID string, maxRedelivery int32, preferredDate, note string) (*Shipment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin redelivery tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, err := scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE id = $1 FOR UPDATE`, shipmentID))
	if err != nil {
		return nil, err
	}
	if existing.Direction != DirectionForward {
		return nil, ErrRedeliveryNotAllowed
	}
	if strings.TrimSpace(existing.BuyerID) == "" {
		return nil, ErrLegacyNoBuyer
	}
	if existing.BuyerID != buyerID {
		return nil, ErrBuyerMismatch
	}
	if existing.Status != "failed" {
		return nil, fmt.Errorf("%w: current status is %s", ErrRedeliveryNotAllowed, existing.Status)
	}
	if existing.RedeliveryCount >= maxRedelivery {
		return nil, fmt.Errorf("%w: %d/%d", ErrMaxRedeliveryExceeded, existing.RedeliveryCount, maxRedelivery)
	}

	// Enforce at most one redelivery request per failed delivery attempt:
	// require a newer carrier failed event after the previous redelivery_requested event.
	var latestFailedRaw string
	evErr := tx.QueryRow(ctx, `
		SELECT raw_status
		FROM shipment_events
		WHERE shipment_id = $1 AND status = 'failed'
		ORDER BY created_at DESC, occurred_at DESC
		LIMIT 1
	`, shipmentID).Scan(&latestFailedRaw)
	if evErr != nil && !errors.Is(evErr, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check latest failed event: %w", evErr)
	}
	if (evErr == nil && latestFailedRaw == "redelivery_requested") ||
		(errors.Is(evErr, pgx.ErrNoRows) && existing.RedeliveryCount > 0) {
		return nil, ErrRedeliveryAlreadyRequested
	}

	now := time.Now().UTC()
	updated, err := scanShipment(tx.QueryRow(ctx, `
		UPDATE shipments
		SET redelivery_count = redelivery_count + 1, updated_at = $2
		WHERE id = $1 AND status = 'failed' AND redelivery_count < $3
		RETURNING `+shipmentColumns, shipmentID, now, maxRedelivery))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrMaxRedeliveryExceeded
		}
		return nil, fmt.Errorf("increment redelivery count: %w", err)
	}

	desc := FormatRedeliveryDescriptionVI(updated.RedeliveryCount, preferredDate, note)
	if _, err := tx.Exec(ctx, `
		INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at, created_at)
		VALUES ($1, $2, 'failed', 'redelivery_requested', $3, $4, clock_timestamp())
	`, uuid.NewString(), updated.ID, desc, now); err != nil {
		return nil, fmt.Errorf("insert redelivery event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit redelivery tx: %w", err)
	}
	return updated, nil
}

// FormatRedeliveryDescriptionVI formats a natural Vietnamese timeline description for a redelivery request.
func FormatRedeliveryDescriptionVI(attempt int32, preferredDate, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Khách hàng yêu cầu giao lại lần %d", attempt)
	if d := strings.TrimSpace(preferredDate); d != "" {
		fmt.Fprintf(&b, " (ngày mong muốn: %s)", d)
	}
	if n := strings.TrimSpace(note); n != "" {
		fmt.Fprintf(&b, " — %s", n)
	}
	return b.String()
}

func formatCancelDescriptionVI(reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		return "Vận đơn đã bị huỷ trước khi hãng lấy hàng"
	}
	return "Vận đơn đã bị huỷ: " + r
}

// ApplyWebhookEvent deduplicates by event_id, rejects stale out-of-order updates (UC-SHIP04),
// updates shipment + shipment_events, and writes shipment.updated to outbox_events in ONE transaction.
// When tracking_code is omitted, falls back to the FORWARD shipment for order_id (never touches RETURN).
func (s *PGStore) ApplyWebhookEvent(ctx context.Context, params WebhookUpdateParams) (*Shipment, WebhookUpdateOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin webhook tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sh *Shipment
	if params.TrackingCode != "" {
		sh, err = scanShipment(tx.QueryRow(ctx, `SELECT `+shipmentColumns+` FROM shipments WHERE tracking_code = $1 FOR UPDATE`, params.TrackingCode))
	} else if params.OrderID != "" {
		sh, err = scanShipment(tx.QueryRow(ctx, `
			SELECT `+shipmentColumns+`
			FROM shipments
			WHERE order_id = $1 AND direction = 'FORWARD'
			ORDER BY (status = 'cancelled') ASC, created_at DESC
			LIMIT 1
			FOR UPDATE
		`, params.OrderID))
	} else {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}

	if strings.TrimSpace(params.CarrierCode) == "" || !strings.EqualFold(strings.TrimSpace(params.CarrierCode), sh.CarrierCode) {
		return nil, "", fmt.Errorf("%w: webhook carrier %q does not match shipment carrier %q", ErrCarrierMismatch, params.CarrierCode, sh.CarrierCode)
	}

	trackingForLog := params.TrackingCode
	if trackingForLog == "" {
		trackingForLog = sh.TrackingCode
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO webhook_events (event_id, carrier_code, tracking_code, raw_status, occurred_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (event_id) DO NOTHING
	`, params.EventID, params.CarrierCode, trackingForLog, params.RawStatus, params.OccurredAt)
	if err != nil {
		return nil, "", fmt.Errorf("insert webhook_event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, "", fmt.Errorf("commit dup webhook tx: %w", err)
		}
		return nil, WebhookOutcomeDuplicate, nil
	}

	if isTerminalStatus(sh.Status) ||
		!CanTransitionStatus(sh.Status, params.InternalStatus) ||
		(sh.LastEventAt != nil && params.OccurredAt.Before(*sh.LastEventAt)) {
		if err := tx.Commit(ctx); err != nil {
			return nil, "", fmt.Errorf("commit stale webhook tx: %w", err)
		}
		return sh, WebhookOutcomeStale, nil
	}

	now := time.Now().UTC()
	updated, err := scanShipment(tx.QueryRow(ctx, `
		UPDATE shipments
		SET status = $2, last_event_at = $3, updated_at = $4
		WHERE id = $1
		RETURNING `+shipmentColumns, sh.ID, params.InternalStatus, params.OccurredAt, now))
	if err != nil {
		return nil, "", fmt.Errorf("update shipment status: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp())
	`, uuid.NewString(), updated.ID, params.InternalStatus, params.RawStatus, params.Description, params.OccurredAt); err != nil {
		return nil, "", fmt.Errorf("insert shipment_event: %w", err)
	}

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

// ListActiveShipments returns a cursor-paginated batch of active non-terminal shipments (both FORWARD and RETURN).
func (s *PGStore) ListActiveShipments(ctx context.Context, limit int, afterUpdatedAt *time.Time, afterID string) ([]Shipment, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows pgx.Rows
		err  error
	)
	if afterUpdatedAt == nil || strings.TrimSpace(afterID) == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT `+shipmentColumns+`
			FROM shipments
			WHERE status IN ('created', 'picking', 'delivering', 'failed', 'returning')
			ORDER BY updated_at ASC, id ASC
			LIMIT $1
		`, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT `+shipmentColumns+`
			FROM shipments
			WHERE status IN ('created', 'picking', 'delivering', 'failed', 'returning')
			  AND (updated_at, id) > ($1, $2::uuid)
			ORDER BY updated_at ASC, id ASC
			LIMIT $3
		`, *afterUpdatedAt, afterID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list active shipments: %w", err)
	}
	defer rows.Close()

	var out []Shipment
	for rows.Next() {
		sh, scanErr := scanShipment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan active shipment: %w", scanErr)
		}
		out = append(out, *sh)
	}
	return out, rows.Err()
}

// GetShopShippingSettings fetches a shop's persisted shipping configuration (UC-SHIP06).
func (s *PGStore) GetShopShippingSettings(ctx context.Context, shopID string) (*ShopShippingSettings, error) {
	if strings.TrimSpace(shopID) == "" {
		return nil, ErrNotFound
	}
	var cfg ShopShippingSettings
	err := s.pool.QueryRow(ctx, `
		SELECT shop_id, enabled_carrier_codes, default_weight_grams,
		       default_length_cm, default_width_cm, default_height_cm, updated_at
		FROM shop_shipping_settings
		WHERE shop_id = $1
	`, shopID).Scan(
		&cfg.ShopID,
		&cfg.EnabledCarrierCodes,
		&cfg.DefaultWeightGrams,
		&cfg.DefaultLengthCM,
		&cfg.DefaultWidthCM,
		&cfg.DefaultHeightCM,
		&cfg.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get shop shipping settings: %w", err)
	}
	return &cfg, nil
}

// UpsertShopShippingSettings creates or updates a shop's shipping configuration atomically.
func (s *PGStore) UpsertShopShippingSettings(ctx context.Context, settings *ShopShippingSettings) (*ShopShippingSettings, error) {
	now := time.Now().UTC()
	var saved ShopShippingSettings
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shop_shipping_settings (
			shop_id, enabled_carrier_codes, default_weight_grams,
			default_length_cm, default_width_cm, default_height_cm, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (shop_id) DO UPDATE
		SET enabled_carrier_codes = EXCLUDED.enabled_carrier_codes,
		    default_weight_grams = EXCLUDED.default_weight_grams,
		    default_length_cm = EXCLUDED.default_length_cm,
		    default_width_cm = EXCLUDED.default_width_cm,
		    default_height_cm = EXCLUDED.default_height_cm,
		    updated_at = EXCLUDED.updated_at
		RETURNING shop_id, enabled_carrier_codes, default_weight_grams,
		          default_length_cm, default_width_cm, default_height_cm, updated_at
	`,
		settings.ShopID,
		settings.EnabledCarrierCodes,
		settings.DefaultWeightGrams,
		settings.DefaultLengthCM,
		settings.DefaultWidthCM,
		settings.DefaultHeightCM,
		now,
	).Scan(
		&saved.ShopID,
		&saved.EnabledCarrierCodes,
		&saved.DefaultWeightGrams,
		&saved.DefaultLengthCM,
		&saved.DefaultWidthCM,
		&saved.DefaultHeightCM,
		&saved.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert shop shipping settings: %w", err)
	}
	return &saved, nil
}

func (s *PGStore) ListCarrierConfigs(ctx context.Context) ([]CarrierConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT carrier_code, name, enabled, encrypted_token, updated_at
		FROM carrier_configs
		ORDER BY carrier_code ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list carrier configs: %w", err)
	}
	defer rows.Close()

	var out []CarrierConfig
	for rows.Next() {
		var c CarrierConfig
		if err := rows.Scan(&c.CarrierCode, &c.Name, &c.Enabled, &c.EncryptedToken, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan carrier config: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) GetCarrierConfig(ctx context.Context, carrierCode string) (*CarrierConfig, error) {
	code := strings.ToLower(strings.TrimSpace(carrierCode))
	if code == "" {
		return nil, ErrNotFound
	}
	var c CarrierConfig
	err := s.pool.QueryRow(ctx, `
		SELECT carrier_code, name, enabled, encrypted_token, updated_at
		FROM carrier_configs
		WHERE carrier_code = $1
	`, code).Scan(&c.CarrierCode, &c.Name, &c.Enabled, &c.EncryptedToken, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get carrier config: %w", err)
	}
	return &c, nil
}

// UpdateCarrierConfig updates carrier_configs and optional fallback_rates atomically.
func (s *PGStore) UpdateCarrierConfig(ctx context.Context, cfg *CarrierConfig, rates []FallbackRate) (*CarrierConfig, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update carrier config tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	var saved CarrierConfig
	err = tx.QueryRow(ctx, `
		INSERT INTO carrier_configs (carrier_code, name, enabled, encrypted_token, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (carrier_code) DO UPDATE
		SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE carrier_configs.name END,
		    enabled = EXCLUDED.enabled,
		    encrypted_token = COALESCE(EXCLUDED.encrypted_token, carrier_configs.encrypted_token),
		    updated_at = EXCLUDED.updated_at
		RETURNING carrier_code, name, enabled, encrypted_token, updated_at
	`, cfg.CarrierCode, cfg.Name, cfg.Enabled, cfg.EncryptedToken, now).Scan(
		&saved.CarrierCode, &saved.Name, &saved.Enabled, &saved.EncryptedToken, &saved.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert carrier config: %w", err)
	}

	for _, r := range rates {
		if _, err := tx.Exec(ctx, `
			INSERT INTO fallback_rates (carrier_code, region, base_fee_vnd, base_weight_grams, extra_fee_vnd_per_500g, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (carrier_code, region) DO UPDATE
			SET base_fee_vnd = EXCLUDED.base_fee_vnd,
			    base_weight_grams = EXCLUDED.base_weight_grams,
			    extra_fee_vnd_per_500g = EXCLUDED.extra_fee_vnd_per_500g,
			    updated_at = EXCLUDED.updated_at
		`, cfg.CarrierCode, r.Region, r.BaseFeeVND, r.BaseWeightGrams, r.ExtraFeeVNDPer500g, now); err != nil {
			return nil, fmt.Errorf("upsert fallback rate (%s, %s): %w", cfg.CarrierCode, r.Region, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update carrier config tx: %w", err)
	}
	return &saved, nil
}

// SeedCarrierTokenIfEmpty stores an initial encrypted token only when no token is persisted yet.
func (s *PGStore) SeedCarrierTokenIfEmpty(ctx context.Context, carrierCode string, encryptedToken []byte) error {
	if len(encryptedToken) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE carrier_configs
		SET encrypted_token = $2, updated_at = NOW()
		WHERE carrier_code = $1 AND (encrypted_token IS NULL OR octet_length(encrypted_token) = 0)
	`, strings.ToLower(strings.TrimSpace(carrierCode)), encryptedToken)
	return err
}

func (s *PGStore) ListFallbackRates(ctx context.Context, carrierCode string) ([]FallbackRate, error) {
	code := strings.ToLower(strings.TrimSpace(carrierCode))
	var (
		rows pgx.Rows
		err  error
	)
	if code == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT carrier_code, region, base_fee_vnd, base_weight_grams, extra_fee_vnd_per_500g, updated_at
			FROM fallback_rates
			ORDER BY carrier_code ASC, region ASC
		`)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT carrier_code, region, base_fee_vnd, base_weight_grams, extra_fee_vnd_per_500g, updated_at
			FROM fallback_rates
			WHERE carrier_code = $1
			ORDER BY region ASC
		`, code)
	}
	if err != nil {
		return nil, fmt.Errorf("list fallback rates: %w", err)
	}
	defer rows.Close()

	var out []FallbackRate
	for rows.Next() {
		var r FallbackRate
		if err := rows.Scan(&r.CarrierCode, &r.Region, &r.BaseFeeVND, &r.BaseWeightGrams, &r.ExtraFeeVNDPer500g, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan fallback rate: %w", err)
		}
		out = append(out, r)
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
