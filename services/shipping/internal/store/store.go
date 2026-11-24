package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/pkg/common/jetstream"
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
	ErrJobCancelled               = errors.New("shipment creation job was cancelled")
	ErrStaleClaimToken            = errors.New("shipment creation job claim token is stale")
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

// ShopShippingSettings represents per-shop carrier, parcel defaults, and shop freeship policy (UC-SHIP06).
type ShopShippingSettings struct {
	ShopID              string
	EnabledCarrierCodes []string
	DefaultWeightGrams  int32
	DefaultLengthCM     int32
	DefaultWidthCM      int32
	DefaultHeightCM     int32
	FreeShipEnabled     bool
	FreeShipMinOrderVND int64
	FreeShipMaxVND      int64
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
	ID           string
	EventID      string
	Subject      string
	AggregateID  string
	Payload      []byte
	Traceparent  string
	TraceID      string
	PublishedAt  *time.Time
	Attempts     int32
	LastError    string
	CreatedAt    time.Time
	Seq          int64
	ClaimedUntil *time.Time
	ClaimToken   string
}

// OutboxAggregateKey returns the per-aggregate ordering key for an outbox event
// (explicit AggregateID, falling back to payload.shipment_id then payload.order_id).
func OutboxAggregateKey(ev OutboxEvent) string {
	if id := strings.TrimSpace(ev.AggregateID); id != "" {
		return id
	}
	if len(ev.Payload) == 0 {
		return ""
	}
	var p struct {
		ShipmentID string `json:"shipment_id"`
		OrderID    string `json:"order_id"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err == nil {
		if id := strings.TrimSpace(p.ShipmentID); id != "" {
			return id
		}
		if id := strings.TrimSpace(p.OrderID); id != "" {
			return id
		}
	}
	return ""
}

// OutboxEventBefore defines strict deterministic creation order by monotonic seq.
func OutboxEventBefore(a, b OutboxEvent) bool {
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	return a.ID < b.ID
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
	GetRepresentativeWard(ctx context.Context, provinceCode string) (*address.Ward, error)
	MapLegacyAddress(ctx context.Context, legacyProv, legacyDist, legacyWard string) (*address.LegacyMatch, error)

	GetShipmentByOrderID(ctx context.Context, orderID, direction string) (*Shipment, error)
	GetActiveShipmentByOrderID(ctx context.Context, orderID, direction string) (*Shipment, error)
	CountInactiveShipmentsByOrder(ctx context.Context, orderID, direction string) (int, error)
	AcquireOrderDirectionLock(ctx context.Context, orderID, direction string) (func(), error)
	GetShipmentByTrackingCode(ctx context.Context, trackingCode string) (*Shipment, error)
	ListShipmentEvents(ctx context.Context, shipmentID string) ([]ShipmentEvent, error)
	CreateShipment(ctx context.Context, s *Shipment, initialEvent *ShipmentEvent) (*Shipment, bool, error)
	CreateShipmentWithOutbox(ctx context.Context, s *Shipment, initialEvent *ShipmentEvent, outbox *JobOutboxParams) (*Shipment, bool, error)
	CancelShipmentByID(ctx context.Context, shipmentID, reason string) (*Shipment, error)
	CancelShipmentWithEvent(ctx context.Context, eventID, subject, orderID, reason string) (*Shipment, bool, error)
	RecordRedelivery(ctx context.Context, shipmentID, buyerID string, maxRedelivery int32, preferredDate, note string) (*Shipment, error)
	ApplyWebhookEvent(ctx context.Context, params WebhookUpdateParams) (*Shipment, WebhookUpdateOutcome, error)
	ListActiveShipments(ctx context.Context, limit int, afterUpdatedAt *time.Time, afterID string) ([]Shipment, error)

	GetActiveShipmentCreateJob(ctx context.Context, orderID, direction string) (*ShipmentCreateJob, error)
	GetShipmentCreateJobByID(ctx context.Context, jobID string) (*ShipmentCreateJob, error)
	EnqueueShipmentCreateJob(ctx context.Context, job *ShipmentCreateJob) (*ShipmentCreateJob, bool, error)
	CancelActiveShipmentCreateJobs(ctx context.Context, orderID, direction, reason string) (int64, error)
	ClaimDueShipmentCreateJobs(ctx context.Context, limit int, lease time.Duration, now time.Time) ([]ShipmentCreateJob, string, error)
	CompleteShipmentCreateJob(ctx context.Context, jobID, claimToken string, sh *Shipment, initialEvent *ShipmentEvent, outbox *JobOutboxParams) (*Shipment, bool, error)
	RecordShipmentCreateJobFailure(ctx context.Context, jobID, claimToken, errMsg string, nextRetryAt *time.Time, terminal bool, outbox *JobOutboxParams, now time.Time) (*ShipmentCreateJob, bool, error)

	GetShopShippingSettings(ctx context.Context, shopID string) (*ShopShippingSettings, error)
	UpsertShopShippingSettings(ctx context.Context, settings *ShopShippingSettings) (*ShopShippingSettings, error)

	ListCarrierConfigs(ctx context.Context) ([]CarrierConfig, error)
	GetCarrierConfig(ctx context.Context, carrierCode string) (*CarrierConfig, error)
	UpdateCarrierConfig(ctx context.Context, cfg *CarrierConfig, rates []FallbackRate) (*CarrierConfig, error)
	SeedCarrierTokenIfEmpty(ctx context.Context, carrierCode string, encryptedToken []byte) error
	ListFallbackRates(ctx context.Context, carrierCode string) ([]FallbackRate, error)

	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error)
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// PGStore implements Repository backed by PostgreSQL via pgx/v5.
type PGStore struct {
	pool                 *pgxpool.Pool
	lockPool             *pgxpool.Pool
	outboxLease          atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxLease
	outboxPublishTimeout atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxPublishTimeout
	outboxClockMu        sync.RWMutex
	outboxNowFn          func() time.Time
	logger               atomic.Pointer[slog.Logger]
}

// NewPGStore wraps an existing pgxpool.Pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	st := &PGStore{pool: pool}
	st.outboxLease.Store(int64(jetstream.DefaultOutboxLease))
	st.outboxPublishTimeout.Store(int64(jetstream.DefaultOutboxPublishTimeout))
	return st
}

// NewPGStoreWithLockPool wraps a query pool and a dedicated advisory-lock pool so
// session locks held across external carrier calls never starve regular DB queries.
func NewPGStoreWithLockPool(pool, lockPool *pgxpool.Pool) *PGStore {
	st := &PGStore{pool: pool, lockPool: lockPool}
	st.outboxLease.Store(int64(jetstream.DefaultOutboxLease))
	st.outboxPublishTimeout.Store(int64(jetstream.DefaultOutboxPublishTimeout))
	return st
}

// SetLogger attaches a structured logger used for outbox poison-event ERROR alerts.
func (s *PGStore) SetLogger(l *slog.Logger) {
	s.logger.Store(l)
}

func (s *PGStore) outboxLogger() *slog.Logger {
	if l := s.logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// SetOutboxLease overrides the outbox claim lease duration (defaults to jetstream.DefaultOutboxLease).
func (s *PGStore) SetOutboxLease(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxLease
	}
	s.outboxLease.Store(int64(d))
}

// SetOutboxPublishTimeout overrides the max per-event publish timeout budget (defaults to jetstream.DefaultOutboxPublishTimeout).
func (s *PGStore) SetOutboxPublishTimeout(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxPublishTimeout
	}
	s.outboxPublishTimeout.Store(int64(d))
}

// SetOutboxClock overrides the Go clock used for lease-budget checks (defaults to time.Now with monotonic clock).
func (s *PGStore) SetOutboxClock(fn func() time.Time) {
	s.outboxClockMu.Lock()
	defer s.outboxClockMu.Unlock()
	s.outboxNowFn = fn
}

func (s *PGStore) outboxNow() time.Time {
	s.outboxClockMu.RLock()
	fn := s.outboxNowFn
	s.outboxClockMu.RUnlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
}

func (s *PGStore) outboxLeaseDuration() time.Duration {
	d := time.Duration(s.outboxLease.Load())
	if d <= 0 {
		return jetstream.DefaultOutboxLease
	}
	return d
}

func (s *PGStore) outboxPublishTimeoutDuration() time.Duration {
	d := time.Duration(s.outboxPublishTimeout.Load())
	if d <= 0 {
		return jetstream.DefaultOutboxPublishTimeout
	}
	return d
}

func (s *PGStore) outboxLeaseMs() int64 {
	ms := s.outboxLeaseDuration().Milliseconds()
	if ms <= 0 {
		return 1
	}
	return ms
}

// Fixed advisory lock keys serializing concurrent startup migrations ("SHIPMIGR")
// and short outbox claim transactions ("SHIPOUTB") across replicas.
const (
	migrateAdvisoryLockKey     int64 = 0x534849504D494752
	outboxClaimAdvisoryLockKey int64 = 0x534849504F555442
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

const addressCatalogMetaKey = "vn_admin_2025"

func (s *PGStore) seedAddressCatalog(ctx context.Context, conn *pgxpool.Conn) error {
	wantChecksum := address.CatalogChecksum()
	var existingChecksum string
	err := conn.QueryRow(ctx, `SELECT checksum FROM address_catalog_meta WHERE key = $1`, addressCatalogMetaKey).Scan(&existingChecksum)
	if err == nil && existingChecksum == wantChecksum {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read address_catalog_meta: %w", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	provinces := address.Provinces()
	pCodes := make([]string, len(provinces))
	pNames := make([]string, len(provinces))
	pNorms := make([]string, len(provinces))
	pRegions := make([]string, len(provinces))
	pGHNIDs := make([]int32, len(provinces))
	for i, p := range provinces {
		pCodes[i] = p.Code
		pNames[i] = p.Name
		pNorms[i] = address.StripDiacritics(p.Name)
		pRegions[i] = p.Region
		pGHNIDs[i] = int32(p.GHNProvinceID)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO provinces (code, name, name_norm, region, ghn_province_id)
		SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[], $4::text[], $5::int[])
		ON CONFLICT (code) DO UPDATE
		SET name = EXCLUDED.name,
		    name_norm = EXCLUDED.name_norm,
		    region = EXCLUDED.region,
		    ghn_province_id = EXCLUDED.ghn_province_id
	`, pCodes, pNames, pNorms, pRegions, pGHNIDs); err != nil {
		return fmt.Errorf("batch seed provinces: %w", err)
	}

	wards := address.Wards()
	wCodes := make([]string, len(wards))
	wProvs := make([]string, len(wards))
	wNames := make([]string, len(wards))
	wNorms := make([]string, len(wards))
	wGHNDists := make([]int32, len(wards))
	wGHNWards := make([]string, len(wards))
	wCODs := make([]bool, len(wards))
	for i, w := range wards {
		wCodes[i] = w.Code
		wProvs[i] = w.ProvinceCode
		wNames[i] = w.Name
		wNorms[i] = address.StripDiacritics(w.Name)
		wGHNDists[i] = int32(w.GHNDistrictID)
		wGHNWards[i] = w.GHNWardCode
		wCODs[i] = w.CODSupported
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO wards (code, province_code, name, name_norm, ghn_district_id, ghn_ward_code, cod_supported, is_active)
		SELECT code, province_code, name, name_norm, ghn_district_id, ghn_ward_code, cod_supported, TRUE
		FROM UNNEST($1::text[], $2::text[], $3::text[], $4::text[], $5::int[], $6::text[], $7::boolean[])
			AS t(code, province_code, name, name_norm, ghn_district_id, ghn_ward_code, cod_supported)
		ON CONFLICT (code) DO UPDATE
		SET province_code = EXCLUDED.province_code,
		    name = EXCLUDED.name,
		    name_norm = EXCLUDED.name_norm,
		    ghn_district_id = EXCLUDED.ghn_district_id,
		    ghn_ward_code = EXCLUDED.ghn_ward_code,
		    cod_supported = EXCLUDED.cod_supported,
		    is_active = TRUE
	`, wCodes, wProvs, wNames, wNorms, wGHNDists, wGHNWards, wCODs); err != nil {
		return fmt.Errorf("batch seed wards: %w", err)
	}

	// E15: Never delete legacy sample ward rows; mark absent codes inactive so they are
	// excluded from ListWards while remaining resolvable by GetWard for saved addresses.
	if _, err := tx.Exec(ctx, `
		UPDATE wards
		SET is_active = FALSE
		WHERE code <> ALL($1::text[])
		  AND is_active = TRUE
	`, wCodes); err != nil {
		return fmt.Errorf("deactivate legacy wards: %w", err)
	}

	mappings := address.LegacyMappings()
	mLP := make([]string, len(mappings))
	mLPN := make([]string, len(mappings))
	mLD := make([]string, len(mappings))
	mLDN := make([]string, len(mappings))
	mLW := make([]string, len(mappings))
	mLWN := make([]string, len(mappings))
	mPC := make([]string, len(mappings))
	mWC := make([]string, len(mappings))
	mLPC := make([]string, len(mappings))
	mLDC := make([]string, len(mappings))
	mLWC := make([]string, len(mappings))
	for i, m := range mappings {
		mLP[i] = m.LegacyProvince
		mLPN[i] = address.NormalizeName(m.LegacyProvince)
		mLD[i] = m.LegacyDistrict
		mLDN[i] = address.NormalizeName(m.LegacyDistrict)
		mLW[i] = m.LegacyWard
		mLWN[i] = address.NormalizeName(m.LegacyWard)
		mPC[i] = m.ProvinceCode
		mWC[i] = m.WardCode
		mLPC[i] = m.LegacyProvinceCode
		mLDC[i] = m.LegacyDistrictCode
		mLWC[i] = m.LegacyWardCode
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO legacy_address_mappings (
			legacy_province, legacy_province_norm,
			legacy_district, legacy_district_norm,
			legacy_ward, legacy_ward_norm,
			province_code, ward_code,
			legacy_province_code, legacy_district_code, legacy_ward_code
		)
		SELECT * FROM UNNEST(
			$1::text[], $2::text[], $3::text[], $4::text[], $5::text[], $6::text[],
			$7::text[], $8::text[], $9::text[], $10::text[], $11::text[]
		)
		ON CONFLICT (legacy_province_norm, legacy_district_norm, legacy_ward_norm) DO UPDATE
		SET legacy_province = EXCLUDED.legacy_province,
		    legacy_district = EXCLUDED.legacy_district,
		    legacy_ward = EXCLUDED.legacy_ward,
		    province_code = EXCLUDED.province_code,
		    ward_code = EXCLUDED.ward_code,
		    legacy_province_code = EXCLUDED.legacy_province_code,
		    legacy_district_code = EXCLUDED.legacy_district_code,
		    legacy_ward_code = EXCLUDED.legacy_ward_code
	`, mLP, mLPN, mLD, mLDN, mLW, mLWN, mPC, mWC, mLPC, mLDC, mLWC); err != nil {
		return fmt.Errorf("batch seed legacy mappings: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO address_catalog_meta (key, checksum, ward_count, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (key) DO UPDATE
		SET checksum = EXCLUDED.checksum,
		    ward_count = EXCLUDED.ward_count,
		    updated_at = NOW()
	`, addressCatalogMetaKey, wantChecksum, len(wards)); err != nil {
		return fmt.Errorf("update address_catalog_meta: %w", err)
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
			SELECT code, province_code, name, ghn_district_id, ghn_ward_code, cod_supported, is_active
			FROM wards
			WHERE province_code = $1 AND is_active = TRUE
			ORDER BY code ASC
		`, provinceCode)
	} else {
		qNorm := address.StripDiacritics(q)
		qAlias := address.NormalizeName(q)
		rows, err = s.pool.Query(ctx, `
			SELECT code, province_code, name, ghn_district_id, ghn_ward_code, cod_supported, is_active
			FROM wards
			WHERE province_code = $1
			  AND is_active = TRUE
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
		if err := rows.Scan(&w.Code, &w.ProvinceCode, &w.Name, &w.GHNDistrictID, &w.GHNWardCode, &w.CODSupported, &w.IsActive); err != nil {
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
		SELECT code, province_code, name, ghn_district_id, ghn_ward_code, cod_supported, is_active
		FROM wards
		WHERE code = $1
	`, wardCode).Scan(&w.Code, &w.ProvinceCode, &w.Name, &w.GHNDistrictID, &w.GHNWardCode, &w.CODSupported, &w.IsActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ward: %w", err)
	}
	return &w, nil
}

// GetRepresentativeWard returns a deterministic active ward for provinceCode (preferring COD-supported
// mainland wards) for public province-only shipping fee estimates (E17).
func (s *PGStore) GetRepresentativeWard(ctx context.Context, provinceCode string) (*address.Ward, error) {
	if strings.TrimSpace(provinceCode) == "" {
		return nil, ErrNotFound
	}
	var w address.Ward
	err := s.pool.QueryRow(ctx, `
		SELECT code, province_code, name, ghn_district_id, ghn_ward_code, cod_supported, is_active
		FROM wards
		WHERE province_code = $1 AND is_active = TRUE
		ORDER BY cod_supported DESC, code ASC
		LIMIT 1
	`, provinceCode).Scan(&w.Code, &w.ProvinceCode, &w.Name, &w.GHNDistrictID, &w.GHNWardCode, &w.CODSupported, &w.IsActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get representative ward: %w", err)
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
		// aggregate_id matches the shipments(id) row locked FOR UPDATE above so concurrent
		// writers on the same aggregate serialize strictly in seq order before COMMIT.
		aggID := strings.TrimSpace(updated.ID)
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, traceparent, trace_id)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
		`, uuid.NewString(), params.OutboxEventID, params.OutboxSubject, aggID, string(outboxPayload), params.Traceparent, params.TraceID); err != nil {
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
		       default_length_cm, default_width_cm, default_height_cm,
		       free_ship_enabled, free_ship_min_order_vnd, free_ship_max_vnd,
		       updated_at
		FROM shop_shipping_settings
		WHERE shop_id = $1
	`, shopID).Scan(
		&cfg.ShopID,
		&cfg.EnabledCarrierCodes,
		&cfg.DefaultWeightGrams,
		&cfg.DefaultLengthCM,
		&cfg.DefaultWidthCM,
		&cfg.DefaultHeightCM,
		&cfg.FreeShipEnabled,
		&cfg.FreeShipMinOrderVND,
		&cfg.FreeShipMaxVND,
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
			default_length_cm, default_width_cm, default_height_cm,
			free_ship_enabled, free_ship_min_order_vnd, free_ship_max_vnd,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (shop_id) DO UPDATE
		SET enabled_carrier_codes = EXCLUDED.enabled_carrier_codes,
		    default_weight_grams = EXCLUDED.default_weight_grams,
		    default_length_cm = EXCLUDED.default_length_cm,
		    default_width_cm = EXCLUDED.default_width_cm,
		    default_height_cm = EXCLUDED.default_height_cm,
		    free_ship_enabled = EXCLUDED.free_ship_enabled,
		    free_ship_min_order_vnd = EXCLUDED.free_ship_min_order_vnd,
		    free_ship_max_vnd = EXCLUDED.free_ship_max_vnd,
		    updated_at = EXCLUDED.updated_at
		RETURNING shop_id, enabled_carrier_codes, default_weight_grams,
		          default_length_cm, default_width_cm, default_height_cm,
		          free_ship_enabled, free_ship_min_order_vnd, free_ship_max_vnd,
		          updated_at
	`,
		settings.ShopID,
		settings.EnabledCarrierCodes,
		settings.DefaultWeightGrams,
		settings.DefaultLengthCM,
		settings.DefaultWidthCM,
		settings.DefaultHeightCM,
		settings.FreeShipEnabled,
		settings.FreeShipMinOrderVND,
		settings.FreeShipMaxVND,
		now,
	).Scan(
		&saved.ShopID,
		&saved.EnabledCarrierCodes,
		&saved.DefaultWeightGrams,
		&saved.DefaultLengthCM,
		&saved.DefaultWidthCM,
		&saved.DefaultHeightCM,
		&saved.FreeShipEnabled,
		&saved.FreeShipMinOrderVND,
		&saved.FreeShipMaxVND,
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

// ClaimPendingOutbox claims a batch of unpublished outbox rows inside a short transaction
// using claimed_until lease + FOR UPDATE SKIP LOCKED + claim_token, commits before returning,
// and restores strict seq order so publishing runs completely outside any TX.
func (s *PGStore) ClaimPendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, string, error) {
	if limit <= 0 {
		limit = 50
	}
	claimToken := uuid.NewString()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin outbox claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, outboxClaimAdvisoryLockKey); err != nil {
		return nil, "", fmt.Errorf("lock outbox claim: %w", err)
	}

	// ponytail: LIMIT applies on locked before eligible; if an aggregate's head row is briefly row-locked
	// (e.g. concurrent MarkPublished) with >=50 queued rows behind it, one 250ms poll may claim 0 rows and
	// recover on the next tick once the short update commits (accepted D9-bis ceiling).
	rows, err := tx.Query(ctx, `
		WITH locked AS (
			SELECT o.id, o.aggregate_id, o.seq
			FROM outbox_events o
			WHERE o.published_at IS NULL
			  AND (o.claimed_until IS NULL OR o.claimed_until <= NOW())
			  AND (
				o.aggregate_id = ''
				OR NOT EXISTS (
					SELECT 1
					FROM outbox_events earlier
					WHERE earlier.aggregate_id = o.aggregate_id
					  AND earlier.published_at IS NULL
					  AND earlier.claimed_until > NOW()
					  AND earlier.seq < o.seq
				)
			  )
			ORDER BY o.seq ASC
			LIMIT $1
			FOR UPDATE OF o SKIP LOCKED
		),
		eligible AS (
			SELECT l.id
			FROM locked l
			WHERE l.aggregate_id = ''
			   OR NOT EXISTS (
				SELECT 1
				FROM outbox_events earlier
				WHERE earlier.aggregate_id = l.aggregate_id
				  AND earlier.published_at IS NULL
				  AND earlier.seq < l.seq
				  AND NOT EXISTS (SELECT 1 FROM locked l2 WHERE l2.id = earlier.id)
			   )
		)
		UPDATE outbox_events o
		SET claimed_until = NOW() + ($2::bigint * INTERVAL '1 millisecond'),
		    claim_token = $3::text
		FROM eligible e
		WHERE o.id = e.id
		RETURNING o.id, o.event_id, o.subject, o.aggregate_id, o.payload, o.traceparent, o.trace_id,
		          o.published_at, o.attempts, o.last_error, o.created_at, o.seq, o.claimed_until, o.claim_token
	`, limit, s.outboxLeaseMs(), claimToken)
	if err != nil {
		return nil, "", fmt.Errorf("claim pending outbox: %w", err)
	}

	var out []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		if scanErr := rows.Scan(
			&ev.ID,
			&ev.EventID,
			&ev.Subject,
			&ev.AggregateID,
			&ev.Payload,
			&ev.Traceparent,
			&ev.TraceID,
			&ev.PublishedAt,
			&ev.Attempts,
			&ev.LastError,
			&ev.CreatedAt,
			&ev.Seq,
			&ev.ClaimedUntil,
			&ev.ClaimToken,
		); scanErr != nil {
			rows.Close()
			return nil, "", fmt.Errorf("scan claimed outbox event: %w", scanErr)
		}
		out = append(out, ev)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, "", fmt.Errorf("iterate claimed outbox events: %w", rowsErr)
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit outbox claim tx: %w", err)
	}

	// PostgreSQL UPDATE ... FROM ... RETURNING does not guarantee output order; restore seq order.
	sort.Slice(out, func(i, j int) bool {
		return OutboxEventBefore(out[i], out[j])
	})
	return out, claimToken, nil
}

// MarkOutboxPublished marks an outbox event published in a standalone query guarded by claim_token.
func (s *PGStore) MarkOutboxPublished(ctx context.Context, id, claimToken string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE outbox_events
		SET published_at = NOW(),
		    attempts = attempts + 1,
		    last_error = '',
		    claimed_until = NULL,
		    claim_token = ''
		WHERE id = $1
		  AND claim_token = $2
		  AND published_at IS NULL
	`, id, claimToken)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkOutboxAttemptFailed records a publish failure guarded by claim_token.
// When backoff > 0 (non-infra failure), claimed_until is set to NOW() + backoff;
// otherwise claimed_until is cleared so the relay can retry after broker recovery.
func (s *PGStore) MarkOutboxAttemptFailed(ctx context.Context, id, claimToken, errMsg string, backoff time.Duration) (int32, bool, error) {
	var backoffMs int64
	if backoff > 0 {
		backoffMs = backoff.Milliseconds()
		if backoffMs <= 0 {
			backoffMs = 1
		}
	}
	var attempts int32
	err := s.pool.QueryRow(ctx, `
		UPDATE outbox_events
		SET attempts = attempts + 1,
		    last_error = $3,
		    claimed_until = CASE
		        WHEN $4::bigint > 0 THEN NOW() + ($4::bigint * INTERVAL '1 millisecond')
		        ELSE NULL
		    END,
		    claim_token = ''
		WHERE id = $1
		  AND claim_token = $2
		  AND published_at IS NULL
		RETURNING attempts
	`, id, claimToken, errMsg, backoffMs).Scan(&attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return attempts, true, nil
}

func (s *PGStore) releaseRemainingOutboxClaims(ctx context.Context, remaining []OutboxEvent, claimToken string) {
	if len(remaining) == 0 {
		return
	}
	ids := make([]string, len(remaining))
	for i, rem := range remaining {
		ids[i] = rem.ID
	}
	relCtx, relCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer relCancel()
	_, _ = s.pool.Exec(relCtx, `
		UPDATE outbox_events
		SET claimed_until = NULL,
		    claim_token = ''
		WHERE id = ANY($1::uuid[])
		  AND claim_token = $2
		  AND published_at IS NULL
	`, ids, claimToken)
}

// ProcessPendingOutbox implements D9 / D9-bis transactional outbox relay:
//  1. Claims a batch in a short transaction (claimed_until + two-tier CTE + FOR UPDATE SKIP LOCKED + claim_token) and commits.
//  2. Publishes every claimed event OUTSIDE any DB transaction in strict seq order.
//  3. Checks remaining lease budget before each publish (must have >= max PubAck timeout remaining); if exhausted,
//     releases remaining claimed rows in one batch UPDATE and stops without publishing past lease expiry.
//  4. On broker/transport infrastructure errors, records failure (without row backoff), releases remaining claimed rows,
//     and aborts the batch immediately so the relay loop backs off. On non-infra errors, applies exponential per-row
//     backoff min(2^attempts, 60)s, logs ERROR from attempts >= 10 without payload, blocks later events of the same
//     aggregate in the batch, and continues publishing other aggregates.
//  5. Marks published events in a separate query guarded by claim_token; on DB error or lost lease (!marked),
//     releases remaining claimed rows in one batch UPDATE and aborts.
func (s *PGStore) ProcessPendingOutbox(ctx context.Context, limit int, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error) {
	if limit <= 0 {
		limit = 50
	}
	lease := s.outboxLeaseDuration()
	pubTimeout := s.outboxPublishTimeoutDuration()
	// Capture lease deadline BEFORE claiming so leaseDeadline <= DB claimed_until.
	leaseDeadline := s.outboxNow().Add(lease)

	claimed, claimToken, err := s.ClaimPendingOutbox(ctx, limit)
	if err != nil {
		return 0, err
	}
	if len(claimed) == 0 {
		return 0, nil
	}

	var toRelease []OutboxEvent
	defer func() {
		if len(toRelease) > 0 {
			s.releaseRemainingOutboxClaims(ctx, toRelease, claimToken)
		}
	}()

	published := 0
	failedAggs := make(map[string]bool)
	for i, ev := range claimed {
		if ctx.Err() != nil {
			toRelease = append(toRelease, claimed[i:]...)
			return published, ctx.Err()
		}
		if leaseDeadline.Sub(s.outboxNow()) < pubTimeout {
			toRelease = append(toRelease, claimed[i:]...)
			return published, nil
		}

		aggKey := OutboxAggregateKey(ev)
		if aggKey != "" && failedAggs[aggKey] {
			toRelease = append(toRelease, ev)
			continue
		}

		pubCtx, pubCancel := context.WithTimeout(ctx, pubTimeout)
		pubErr := publishFn(pubCtx, ev)
		pubCancel()
		if pubErr != nil {
			infraErr := jetstream.IsInfraError(pubErr)
			var backoff time.Duration
			if !infraErr {
				backoff = jetstream.OutboxRetryBackoff(ev.Attempts + 1)
			}
			failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			newAttempts, updated, failErr := s.MarkOutboxAttemptFailed(failCtx, ev.ID, claimToken, pubErr.Error(), backoff)
			failCancel()
			if infraErr {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, pubErr
			}
			if failErr != nil {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, fmt.Errorf("mark outbox event failed: %w", failErr)
			}
			if !updated {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, nil
			}
			if newAttempts >= jetstream.OutboxPoisonLogAttempt {
				s.outboxLogger().ErrorContext(ctx, "outbox event repeatedly failed",
					slog.String("event_id", ev.EventID),
					slog.String("event_type", ev.Subject),
					slog.String("aggregate_id", aggKey),
					slog.Int("attempts", int(newAttempts)),
					slog.String("error", pubErr.Error()),
				)
			}
			if aggKey != "" {
				failedAggs[aggKey] = true
			}
			continue
		}

		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		marked, markErr := s.MarkOutboxPublished(ackCtx, ev.ID, claimToken)
		ackCancel()
		if markErr != nil {
			toRelease = append(toRelease, claimed[i+1:]...)
			return published, fmt.Errorf("mark outbox event published: %w", markErr)
		}
		if !marked {
			toRelease = append(toRelease, claimed[i+1:]...)
			return published, nil
		}
		published++
	}
	return published, nil
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
