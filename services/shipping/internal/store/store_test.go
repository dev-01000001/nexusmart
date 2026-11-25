package store

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/services/shipping/migrations"
)

func TestEmbeddedMigrationsPresentAndIdempotentGuards(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations FS: %v", err)
	}
	found := map[string]bool{}
	var sqlFiles []string
	for _, e := range entries {
		found[e.Name()] = true
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)
	wantOrder := []string{
		"000001_init.sql",
		"000002_return_shipments.sql",
		"000003_shipping_ops.sql",
		"000004_return_recreate_index.sql",
		"000005_shop_freeship_settings.sql",
		"000006_outbox_lease.sql",
		"000007_w3b_wards_and_shipment_jobs.sql",
		"000008_w3b3_shipment_label_fields.sql",
	}
	if len(sqlFiles) != len(wantOrder) {
		t.Fatalf("expected %d migrations (%v), got %v", len(wantOrder), wantOrder, sqlFiles)
	}
	for i, w := range wantOrder {
		if sqlFiles[i] != w {
			t.Fatalf("migration[%d] = %q, want %q (all=%v)", i, sqlFiles[i], w, sqlFiles)
		}
	}

	raw2, err := fs.ReadFile(migrations.FS, "000002_return_shipments.sql")
	if err != nil {
		t.Fatalf("read 000002_return_shipments.sql: %v", err)
	}
	sql2 := string(raw2)
	for _, needle := range []string{
		"ADD COLUMN IF NOT EXISTS direction",
		"ADD COLUMN IF NOT EXISTS return_request_id",
		"DROP CONSTRAINT IF EXISTS shipments_order_id_key",
		"uq_shipments_order_direction",
	} {
		if !strings.Contains(sql2, needle) {
			t.Fatalf("000002_return_shipments.sql missing %q", needle)
		}
	}

	raw3, err := fs.ReadFile(migrations.FS, "000003_shipping_ops.sql")
	if err != nil {
		t.Fatalf("read 000003_shipping_ops.sql: %v", err)
	}
	sql3 := string(raw3)
	for _, mustContain := range []string{
		"ADD COLUMN IF NOT EXISTS direction",
		"ADD COLUMN IF NOT EXISTS return_request_id",
		"ADD COLUMN IF NOT EXISTS redelivery_count",
		"ADD COLUMN IF NOT EXISTS buyer_id",
		"DROP CONSTRAINT IF EXISTS shipments_order_id_key",
		"DROP INDEX IF EXISTS uq_shipments_order_direction",
		"ALTER TABLE shipments ALTER COLUMN direction SET DEFAULT 'FORWARD'",
		"SET direction = UPPER(direction)",
		"chk_shipments_direction",
		"CHECK (direction IN ('FORWARD', 'RETURN'))",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_shipments_order_direction_active",
		"WHERE status <> 'cancelled'",
		"CREATE TABLE IF NOT EXISTS shop_shipping_settings",
		"CREATE TABLE IF NOT EXISTS carrier_configs",
		"('ghtk', 'Giao Hang Tiet Kiem (GHTK)', TRUE)",
		"CREATE TABLE IF NOT EXISTS fallback_rates",
		"('ghtk', 'SAME_PROVINCE'",
	} {
		if !strings.Contains(sql3, mustContain) {
			t.Fatalf("000003_shipping_ops.sql missing %q", mustContain)
		}
	}

	raw4, err := fs.ReadFile(migrations.FS, "000004_return_recreate_index.sql")
	if err != nil {
		t.Fatalf("read 000004_return_recreate_index.sql: %v", err)
	}
	sql4 := string(raw4)
	for _, mustContain := range []string{
		"pg_indexes",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_shipments_order_direction_active",
		"NOT (direction = 'RETURN' AND status = 'returned')",
	} {
		if !strings.Contains(sql4, mustContain) {
			t.Fatalf("000004_return_recreate_index.sql missing %q", mustContain)
		}
	}

	raw5, err := fs.ReadFile(migrations.FS, "000005_shop_freeship_settings.sql")
	if err != nil {
		t.Fatalf("read 000005_shop_freeship_settings.sql: %v", err)
	}
	sql5 := string(raw5)
	for _, mustContain := range []string{
		"ADD COLUMN IF NOT EXISTS free_ship_enabled",
		"ADD COLUMN IF NOT EXISTS free_ship_min_order_vnd",
		"ADD COLUMN IF NOT EXISTS free_ship_max_vnd",
		"chk_shop_shipping_free_ship_max_positive",
	} {
		if !strings.Contains(sql5, mustContain) {
			t.Fatalf("000005_shop_freeship_settings.sql missing %q", mustContain)
		}
	}

	raw6, err := fs.ReadFile(migrations.FS, "000006_outbox_lease.sql")
	if err != nil {
		t.Fatalf("read 000006_outbox_lease.sql: %v", err)
	}
	sql6 := string(raw6)
	for _, mustContain := range []string{
		"DEPLOY NOTE",
		"idx_outbox_unpublished",
		"ADD COLUMN IF NOT EXISTS aggregate_id",
		"ADD COLUMN IF NOT EXISTS claimed_until",
		"ADD COLUMN IF NOT EXISTS claim_token",
		"ADD COLUMN IF NOT EXISTS attempts",
		"ADD COLUMN IF NOT EXISTS last_error",
		"column_name = 'seq'",
		"ADD COLUMN seq BIGSERIAL NOT NULL",
		"BTRIM(payload->>'shipment_id')",
		"AND published_at IS NULL",
		"nextval(seq_name)",
		"idx_shipping_outbox_claimable",
		"ON outbox_events (seq ASC)",
		"idx_shipping_outbox_aggregate_unpublished",
		"ON outbox_events (aggregate_id, seq ASC)",
	} {
		if !strings.Contains(sql6, mustContain) {
			t.Fatalf("000006_outbox_lease.sql missing %q", mustContain)
		}
	}

	raw7, err := fs.ReadFile(migrations.FS, "000007_w3b_wards_and_shipment_jobs.sql")
	if err != nil {
		t.Fatalf("read 000007_w3b_wards_and_shipment_jobs.sql: %v", err)
	}
	sql7 := string(raw7)
	for _, mustContain := range []string{
		"ADD COLUMN IF NOT EXISTS cod_supported",
		"ADD COLUMN IF NOT EXISTS is_active",
		"CREATE TABLE IF NOT EXISTS address_catalog_meta",
		"CREATE TABLE IF NOT EXISTS shipment_create_jobs",
		"uq_shipment_create_jobs_order_direction_active",
		"idx_shipment_create_jobs_claimable",
	} {
		if !strings.Contains(sql7, mustContain) {
			t.Fatalf("000007_w3b_wards_and_shipment_jobs.sql missing %q", mustContain)
		}
	}

	raw8, err := fs.ReadFile(migrations.FS, "000008_w3b3_shipment_label_fields.sql")
	if err != nil {
		t.Fatalf("read 000008_w3b3_shipment_label_fields.sql: %v", err)
	}
	sql8 := string(raw8)
	for _, mustContain := range []string{
		"ADD COLUMN IF NOT EXISTS from_name",
		"ADD COLUMN IF NOT EXISTS from_phone",
		"ADD COLUMN IF NOT EXISTS from_street_address",
		"ADD COLUMN IF NOT EXISTS to_name",
		"ADD COLUMN IF NOT EXISTS to_phone",
		"ADD COLUMN IF NOT EXISTS to_street_address",
	} {
		if !strings.Contains(sql8, mustContain) {
			t.Fatalf("000008_w3b3_shipment_label_fields.sql missing %q", mustContain)
		}
	}
}

func TestBootstrapMigrationVersions(t *testing.T) {
	// Fresh empty DB: nothing bootstrapped, all migrations run from 000001..000008.
	if got := bootstrapMigrationVersions(0, false, false); len(got) != 0 {
		t.Fatalf("fresh DB bootstrap = %v, want empty", got)
	}
	// Pre-000002 DB (only 000001_init.sql applied): bootstrap 000001 only.
	if got := bootstrapMigrationVersions(0, true, false); len(got) != 1 || got[0] != migration0001Init {
		t.Fatalf("init-only DB bootstrap = %v, want [%s]", got, migration0001Init)
	}
	// Phase-0b (or legacy unversioned main) DB with shipments.direction: bootstrap 000001 and 000002 so 000002 never re-runs.
	got := bootstrapMigrationVersions(0, true, true)
	if len(got) != 2 || got[0] != migration0001Init || got[1] != migration0002ReturnShipments {
		t.Fatalf("phase-0b DB bootstrap = %v, want [%s %s]", got, migration0001Init, migration0002ReturnShipments)
	}
	// Already versioned DB (schema_migrations non-empty): bootstrap does nothing.
	if got := bootstrapMigrationVersions(8, true, true); len(got) != 0 {
		t.Fatalf("already-versioned DB bootstrap = %v, want empty", got)
	}
}

func TestCanTransitionStatusMatrix(t *testing.T) {
	allStatuses := []string{"created", "picking", "delivering", "failed", "returning", "delivered", "returned", "cancelled"}
	allowed := map[string]map[string]bool{
		"created":    {"created": true, "picking": true, "delivering": true, "failed": true, "returning": true, "delivered": true, "returned": true, "cancelled": true},
		"picking":    {"picking": true, "delivering": true, "failed": true, "returning": true, "delivered": true, "returned": true, "cancelled": true},
		"delivering": {"delivering": true, "failed": true, "returning": true, "delivered": true, "returned": true, "cancelled": true},
		"failed":     {"delivering": true, "failed": true, "returning": true, "delivered": true, "returned": true, "cancelled": true},
		"returning":  {"returning": true, "returned": true, "cancelled": true},
		"delivered":  {},
		"returned":   {},
		"cancelled":  {},
	}
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := allowed[from][to]
			if got := CanTransitionStatus(from, to); got != want {
				t.Fatalf("CanTransitionStatus(%q -> %q) = %v, want %v", from, to, got, want)
			}
		}
	}
	if CanTransitionStatus("unknown", "delivering") {
		t.Fatalf("unknown source status must not allow transitions")
	}
}

func TestNormalizeDirectionAndDescriptionsVI(t *testing.T) {
	if got := NormalizeDirection(""); got != DirectionForward {
		t.Fatalf("NormalizeDirection('') = %q, want FORWARD", got)
	}
	if got := NormalizeDirection("return"); got != DirectionReturn {
		t.Fatalf("NormalizeDirection('return') = %q, want RETURN", got)
	}
	if got := FormatRedeliveryDescriptionVI(2, "2026-11-28", "Gọi trước"); !strings.Contains(got, "lần 2") || !strings.Contains(got, "2026-11-28") {
		t.Fatalf("unexpected redelivery description: %q", got)
	}
	if got := formatCancelDescriptionVI(""); !strings.Contains(got, "huỷ") {
		t.Fatalf("expected Vietnamese cancel description, got %q", got)
	}
}

// TestPGStoreMigrateAndSeedIdempotent runs against an isolated temporary shipping DB when TEST_SHIPPING_DB_URL is set.
func TestPGStoreMigrateAndSeedIdempotent(t *testing.T) {
	pool := openIsolatedShippingTestPool(t)
	ctx := context.Background()

	st := NewPGStore(pool)
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("first MigrateAndSeed failed: %v", err)
	}
	var oid1 uint32
	var idxDef1 string
	if err := pool.QueryRow(ctx, `
		SELECT c.oid, pg_get_indexdef(c.oid)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relname = 'uq_shipments_order_direction_active'
	`).Scan(&oid1, &idxDef1); err != nil {
		t.Fatalf("query uq_shipments_order_direction_active after 1st migrate: %v", err)
	}
	if !strings.Contains(idxDef1, "RETURN") || !strings.Contains(idxDef1, "returned") {
		t.Fatalf("expected updated partial index predicate in %q", idxDef1)
	}

	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("second MigrateAndSeed (idempotency check) failed: %v", err)
	}
	var oid2 uint32
	if err := pool.QueryRow(ctx, `
		SELECT c.oid
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relname = 'uq_shipments_order_direction_active'
	`).Scan(&oid2); err != nil {
		t.Fatalf("query uq_shipments_order_direction_active after 2nd migrate: %v", err)
	}
	if oid1 != oid2 {
		t.Fatalf("000003 migration must NOT drop/recreate index when already up to date (oid %d -> %d)", oid1, oid2)
	}

	// Mục 12: Concurrent startup migrations from multiple goroutines must serialize safely via pg_advisory_lock.
	const workers = 4
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- st.MigrateAndSeed(ctx)
		}()
	}
	wg.Wait()
	close(errCh)
	for mErr := range errCh {
		if mErr != nil {
			t.Fatalf("concurrent MigrateAndSeed failed: %v", mErr)
		}
	}

	provs, err := st.ListProvinces(ctx, "Ha Noi")
	if err != nil || len(provs) == 0 || provs[0].Code != "01" {
		t.Fatalf("PGStore ListProvinces('Ha Noi') = (%+v, %v), want code 01", provs, err)
	}
	wards, err := st.ListWards(ctx, "79", "Ben Thanh")
	if err != nil || len(wards) == 0 || wards[0].Code != "26743" {
		t.Fatalf("PGStore ListWards('79', 'Ben Thanh') = (%+v, %v), want official QD 19/2025 code 26743", wards, err)
	}

	// Verify CountInactiveShipmentsByOrder, RETURN recreate after returned, and RecordRedelivery 1-per-failed-attempt on real Postgres.
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	ordID := "pgtest-" + suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM shipments WHERE order_id = $1`, ordID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM webhook_events WHERE tracking_code LIKE $1`, "PG-"+suffix+"%")
	})

	now := time.Now().UTC().Truncate(time.Second)
	sh1, _, err := st.CreateShipment(ctx, &Shipment{
		ID: uuid.NewString(), OrderID: ordID, ShopID: "pg-shop", BuyerID: "pg-buyer",
		Direction: DirectionForward, CarrierCode: "ghn", ServiceCode: "STANDARD",
		TrackingCode: "PG-" + suffix + "-1", Status: "created", FeeVND: 22000, WeightGrams: 500,
		CreatedAt: now, UpdatedAt: now,
	}, &ShipmentEvent{
		ID: uuid.NewString(), Status: "created", RawStatus: "created", Description: "created", OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("CreateShipment #1: %v", err)
	}
	if _, err := st.CancelShipmentByID(ctx, sh1.ID, "cancel 1"); err != nil {
		t.Fatalf("CancelShipmentByID #1: %v", err)
	}
	cnt, err := st.CountInactiveShipmentsByOrder(ctx, ordID, DirectionForward)
	if err != nil || cnt != 1 {
		t.Fatalf("CountInactiveShipmentsByOrder(FORWARD) = (%d, %v), want 1", cnt, err)
	}

	// Verify RETURN recreate after returned + cancelled + GetShipmentByOrderID newest on real Postgres.
	ret1, _, err := st.CreateShipment(ctx, &Shipment{
		ID: uuid.NewString(), OrderID: ordID, ShopID: "pg-shop", BuyerID: "pg-buyer",
		Direction: DirectionReturn, ReturnRequestID: "pg-rr-1", CarrierCode: "ghn", ServiceCode: "STANDARD",
		TrackingCode: "PG-" + suffix + "-R1", Status: "created", FeeVND: 22000, WeightGrams: 500,
		CreatedAt: now, UpdatedAt: now,
	}, nil)
	if err != nil {
		t.Fatalf("CreateShipment RETURN #1: %v", err)
	}
	if _, _, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
		EventID: "pg-wh-ret1-" + ordID, CarrierCode: "ghn", TrackingCode: ret1.TrackingCode, OrderID: ordID,
		InternalStatus: "returned", RawStatus: "returned", Description: "Đã hoàn hàng", OccurredAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("ApplyWebhookEvent RETURN #1 returned: %v", err)
	}
	if _, err := st.GetActiveShipmentByOrderID(ctx, ordID, DirectionReturn); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for active RETURN after returned, got %v", err)
	}
	ret2, existedRet2, err := st.CreateShipment(ctx, &Shipment{
		ID: uuid.NewString(), OrderID: ordID, ShopID: "pg-shop", BuyerID: "pg-buyer",
		Direction: DirectionReturn, ReturnRequestID: "pg-rr-2", CarrierCode: "ghn", ServiceCode: "STANDARD",
		TrackingCode: "PG-" + suffix + "-R2", Status: "created", FeeVND: 22000, WeightGrams: 500,
		CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}, nil)
	if err != nil || existedRet2 || ret2.ID == ret1.ID {
		t.Fatalf("CreateShipment RETURN #2 after returned must succeed, got sh=%+v existed=%v err=%v", ret2, existedRet2, err)
	}
	gotLatestRet, err := st.GetShipmentByOrderID(ctx, ordID, DirectionReturn)
	if err != nil || gotLatestRet.ID != ret2.ID {
		t.Fatalf("GetShipmentByOrderID(RETURN) must return RETURN #2, got %+v err=%v", gotLatestRet, err)
	}
	if _, err := st.CancelShipmentByID(ctx, ret2.ID, "cancel ret2"); err != nil {
		t.Fatalf("CancelShipmentByID RETURN #2: %v", err)
	}
	gotLatestRetAfterCancel, err := st.GetShipmentByOrderID(ctx, ordID, DirectionReturn)
	if err != nil || gotLatestRetAfterCancel.ID != ret2.ID {
		t.Fatalf("GetShipmentByOrderID(RETURN) after cancel must still return newest RETURN #2, got %+v err=%v", gotLatestRetAfterCancel, err)
	}
	retInactiveCnt, err := st.CountInactiveShipmentsByOrder(ctx, ordID, DirectionReturn)
	if err != nil || retInactiveCnt != 2 {
		t.Fatalf("CountInactiveShipmentsByOrder(RETURN) = (%d, %v), want 2 (1 returned + 1 cancelled)", retInactiveCnt, err)
	}

	sh2, _, err := st.CreateShipment(ctx, &Shipment{
		ID: uuid.NewString(), OrderID: ordID, ShopID: "pg-shop", BuyerID: "pg-buyer",
		Direction: DirectionForward, CarrierCode: "ghn", ServiceCode: "STANDARD",
		TrackingCode: "PG-" + suffix + "-2", Status: "created", FeeVND: 22000, WeightGrams: 500,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}, &ShipmentEvent{
		ID: uuid.NewString(), Status: "created", RawStatus: "created", Description: "created", OccurredAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateShipment #2: %v", err)
	}
	// Apply failed webhook #1 -> redelivery #1 succeeds -> duplicate rejected -> failed webhook #2 -> redelivery #2 succeeds.
	if _, _, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
		EventID: "pg-wh-1-" + ordID, CarrierCode: "ghn", TrackingCode: sh2.TrackingCode, OrderID: ordID,
		InternalStatus: "failed", RawStatus: "delivery_fail", Description: "Giao hàng thất bại", OccurredAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("ApplyWebhookEvent fail #1: %v", err)
	}
	if got, err := st.RecordRedelivery(ctx, sh2.ID, "pg-buyer", 3, "2026-11-28", "lần 1"); err != nil || got.RedeliveryCount != 1 {
		t.Fatalf("RecordRedelivery #1 = (%+v, %v), want count=1", got, err)
	}
	if _, err := st.RecordRedelivery(ctx, sh2.ID, "pg-buyer", 3, "2026-11-29", "trùng"); !errors.Is(err, ErrRedeliveryAlreadyRequested) {
		t.Fatalf("duplicate RecordRedelivery must return ErrRedeliveryAlreadyRequested, got %v", err)
	}
	if _, _, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
		EventID: "pg-wh-2-" + ordID, CarrierCode: "ghn", TrackingCode: sh2.TrackingCode, OrderID: ordID,
		InternalStatus: "failed", RawStatus: "delivery_fail", Description: "Giao hàng thất bại", OccurredAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("ApplyWebhookEvent fail #2: %v", err)
	}
	if got, err := st.RecordRedelivery(ctx, sh2.ID, "pg-buyer", 3, "2026-11-30", "lần 2"); err != nil || got.RedeliveryCount != 2 {
		t.Fatalf("RecordRedelivery #2 = (%+v, %v), want count=2", got, err)
	}

	// Mục 3: Monotonic status transitions on real Postgres (even with equal or later occurred_at).
	// sh2 is currently in "failed" with last_event_at = now+3s.
	for i, bad := range []struct {
		raw      string
		internal string
		at       time.Time
	}{
		{"created", "created", now.Add(3 * time.Second)},
		{"picking", "picking", now.Add(4 * time.Second)},
	} {
		_, outcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
			EventID: "pg-wh-mono-fail-" + ordID + "-" + string(rune('a'+i)), CarrierCode: "ghn", TrackingCode: sh2.TrackingCode,
			InternalStatus: bad.internal, RawStatus: bad.raw, Description: bad.raw, OccurredAt: bad.at,
		})
		if err != nil || outcome != WebhookOutcomeStale {
			t.Fatalf("failed -> %s must return WebhookOutcomeStale, got outcome=%s err=%v", bad.internal, outcome, err)
		}
	}
	// Valid redelivery transition: failed -> delivering.
	if got, outcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
		EventID: "pg-wh-mono-redeliv-" + ordID, CarrierCode: "ghn", TrackingCode: sh2.TrackingCode,
		InternalStatus: "delivering", RawStatus: "delivering", Description: "Đang giao hàng", OccurredAt: now.Add(5 * time.Second),
	}); err != nil || outcome != WebhookOutcomeApplied || got.Status != "delivering" {
		t.Fatalf("failed -> delivering must apply, got sh=%+v outcome=%s err=%v", got, outcome, err)
	}
	// delivering -> picking (same timestamp and later timestamp) must be stale.
	if _, outcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
		EventID: "pg-wh-mono-dlv-pick-" + ordID, CarrierCode: "ghn", TrackingCode: sh2.TrackingCode,
		InternalStatus: "picking", RawStatus: "picking", Description: "Đang lấy hàng", OccurredAt: now.Add(6 * time.Second),
	}); err != nil || outcome != WebhookOutcomeStale {
		t.Fatalf("delivering -> picking must return WebhookOutcomeStale, got outcome=%s err=%v", outcome, err)
	}
	// delivering -> returning (applied), then returning -> delivering/picking/created/failed must all be stale!
	if got, outcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
		EventID: "pg-wh-mono-ret-" + ordID, CarrierCode: "ghn", TrackingCode: sh2.TrackingCode,
		InternalStatus: "returning", RawStatus: "returning", Description: "Đang hoàn hàng", OccurredAt: now.Add(7 * time.Second),
	}); err != nil || outcome != WebhookOutcomeApplied || got.Status != "returning" {
		t.Fatalf("delivering -> returning must apply, got sh=%+v outcome=%s err=%v", got, outcome, err)
	}
	for i, bad := range []string{"created", "picking", "delivering", "failed"} {
		_, outcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
			EventID: "pg-wh-mono-ret-back-" + ordID + "-" + string(rune('a'+i)), CarrierCode: "ghn", TrackingCode: sh2.TrackingCode,
			InternalStatus: bad, RawStatus: bad, Description: bad, OccurredAt: now.Add(time.Duration(8+i) * time.Second),
		})
		if err != nil || outcome != WebhookOutcomeStale {
			t.Fatalf("returning -> %s must return WebhookOutcomeStale, got outcome=%s err=%v", bad, outcome, err)
		}
	}

	// Wave 2b: verify shop_shipping_settings freeship columns, persistence, DB CHECK constraint, and idempotent re-migration.
	shopID := "pgtest-shop-" + suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM shop_shipping_settings WHERE shop_id = $1`, shopID)
	})
	savedCfg, err := st.UpsertShopShippingSettings(ctx, &ShopShippingSettings{
		ShopID:              shopID,
		EnabledCarrierCodes: []string{"ghn"},
		DefaultWeightGrams:  1200,
		DefaultLengthCM:     25,
		DefaultWidthCM:      20,
		DefaultHeightCM:     12,
		FreeShipEnabled:     true,
		FreeShipMinOrderVND: 250000,
		FreeShipMaxVND:      30000,
	})
	if err != nil {
		t.Fatalf("UpsertShopShippingSettings freeship: %v", err)
	}
	if !savedCfg.FreeShipEnabled || savedCfg.FreeShipMinOrderVND != 250000 || savedCfg.FreeShipMaxVND != 30000 {
		t.Fatalf("unexpected saved freeship settings: %+v", savedCfg)
	}
	fetchedCfg, err := st.GetShopShippingSettings(ctx, shopID)
	if err != nil {
		t.Fatalf("GetShopShippingSettings freeship: %v", err)
	}
	if !fetchedCfg.FreeShipEnabled || fetchedCfg.FreeShipMinOrderVND != 250000 || fetchedCfg.FreeShipMaxVND != 30000 {
		t.Fatalf("unexpected fetched freeship settings: %+v", fetchedCfg)
	}
	// DB CHECK constraint must reject free_ship_enabled=true with free_ship_max_vnd=0 even on direct write.
	if _, err := st.UpsertShopShippingSettings(ctx, &ShopShippingSettings{
		ShopID:              shopID,
		EnabledCarrierCodes: []string{"ghn"},
		DefaultWeightGrams:  1200,
		FreeShipEnabled:     true,
		FreeShipMinOrderVND: 250000,
		FreeShipMaxVND:      0,
	}); err == nil {
		t.Fatalf("expected DB CHECK constraint error for free_ship_enabled=true with free_ship_max_vnd=0")
	}
	// Re-running MigrateAndSeed with populated freeship rows must remain idempotent.
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("MigrateAndSeed after freeship rows failed: %v", err)
	}
}

func TestStoreHelpersAndMigrationsFS(t *testing.T) {
	if NormalizeDirection("") != DirectionForward ||
		NormalizeDirection("forward") != DirectionForward ||
		NormalizeDirection("FORWARD") != DirectionForward ||
		NormalizeDirection("return") != DirectionReturn ||
		NormalizeDirection(" RETURN ") != DirectionReturn {
		t.Fatalf("unexpected NormalizeDirection behavior")
	}

	if !isTerminalStatus("delivered") || !isTerminalStatus("returned") || !isTerminalStatus("cancelled") {
		t.Fatalf("expected delivered/returned/cancelled to be terminal")
	}
	if isTerminalStatus("created") || isTerminalStatus("delivering") || isTerminalStatus("returning") {
		t.Fatalf("expected active statuses not terminal")
	}

	// Empty ward code returns ErrNotFound without touching pool.
	pg := NewPGStore(nil)
	if _, err := pg.GetWard(context.Background(), "   "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for blank wardCode, got %v", err)
	}
}

// TestPGStorePhase0bUpgradeAndDoubleRun verifies upgrading a phase-0b DB (only 000001 + 000002,
// lowercase direction rows, non-partial uq_shipments_order_direction, no schema_migrations)
// followed by a second MigrateAndSeed run.
func TestPGStorePhase0bUpgradeAndDoubleRun(t *testing.T) {
	pool := openIsolatedShippingTestPool(t)
	ctx := context.Background()

	// Seed a pure phase-0b schema (000001 + 000002 only, no schema_migrations).
	for _, fname := range []string{"000001_init.sql", "000002_return_shipments.sql"} {
		raw, err := fs.ReadFile(migrations.FS, fname)
		if err != nil {
			t.Fatalf("read %s: %v", fname, err)
		}
		if _, err := pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("exec %s: %v", fname, err)
		}
	}

	// Insert legacy lowercase phase-0b rows (both forward and return, one cancelled).
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipments (id, order_id, shop_id, carrier_code, service_code, tracking_code, status, direction, return_request_id)
		VALUES
			($1, 'ord-p0b-1', 'shop-1', 'ghtk', 'ROAD', 'GHTK-P0B-FWD', 'cancelled', 'forward', ''),
			($2, 'ord-p0b-1', 'shop-1', 'ghtk', 'ROAD', 'GHTK-P0B-RET', 'created', 'return', 'rr-p0b-1')
	`, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatalf("insert phase-0b rows: %v", err)
	}

	st := NewPGStore(pool)
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("first MigrateAndSeed on phase-0b DB failed: %v", err)
	}
	// Second run must succeed and must NOT re-execute 000002_return_shipments.sql.
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("second MigrateAndSeed on upgraded phase-0b DB failed: %v", err)
	}

	// Verify lowercase rows were backfilled to uppercase FORWARD / RETURN.
	var fwdDir, retDir string
	if err := pool.QueryRow(ctx, `SELECT direction FROM shipments WHERE tracking_code = 'GHTK-P0B-FWD'`).Scan(&fwdDir); err != nil || fwdDir != DirectionForward {
		t.Fatalf("backfilled forward direction = (%q, %v), want %q", fwdDir, err, DirectionForward)
	}
	if err := pool.QueryRow(ctx, `SELECT direction FROM shipments WHERE tracking_code = 'GHTK-P0B-RET'`).Scan(&retDir); err != nil || retDir != DirectionReturn {
		t.Fatalf("backfilled return direction = (%q, %v), want %q", retDir, err, DirectionReturn)
	}

	// Verify uq_shipments_order_direction was dropped and recreated forward shipment after cancelled succeeds!
	var legacyIdxExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = current_schema() AND tablename = 'shipments' AND indexname = 'uq_shipments_order_direction'
		)
	`).Scan(&legacyIdxExists); err != nil || legacyIdxExists {
		t.Fatalf("uq_shipments_order_direction must be dropped after upgrade (exists=%v, err=%v)", legacyIdxExists, err)
	}
	now := time.Now().UTC()
	recreated, existed, err := st.CreateShipment(ctx, &Shipment{
		ID: uuid.NewString(), OrderID: "ord-p0b-1", ShopID: "shop-1", Direction: DirectionForward,
		CarrierCode: "ghtk", ServiceCode: "ROAD", TrackingCode: "GHTK-P0B-FWD-2",
		Status: "created", FeeVND: 25000, WeightGrams: 500, CreatedAt: now, UpdatedAt: now,
	}, nil)
	if err != nil || existed || recreated.TrackingCode != "GHTK-P0B-FWD-2" {
		t.Fatalf("recreating FORWARD shipment after phase-0b cancelled row must succeed: sh=%+v existed=%v err=%v", recreated, existed, err)
	}

	// Verify schema_migrations recorded all 8 migrations.
	var migCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migCount); err != nil || migCount != 8 {
		t.Fatalf("schema_migrations count = (%d, %v), want 8", migCount, err)
	}
}
