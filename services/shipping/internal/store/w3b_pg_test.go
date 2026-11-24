package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPGStore_W3B_E15_E16_WardCatalogChecksumAndLegacyPreservation verifies E15 & E16 on real Postgres:
//   - Pre-existing legacy sample ward codes (e.g. 01001, 79001, 79002) are NOT deleted on migration/seed;
//     they are marked is_active = false, excluded from ListWards, and remain resolvable via GetWard.
//   - Official catalog seeds 34 provinces & 3,321 active wards; Hoang Sa (20333) & Truong Sa (22736) have cod_supported=false.
//   - Re-running MigrateAndSeed with unchanged address_catalog_meta checksum skips full upsert.
func TestPGStore_W3B_E15_E16_WardCatalogChecksumAndLegacyPreservation(t *testing.T) {
	pool := openIsolatedShippingTestPool(t)
	ctx := context.Background()

	st := NewPGStore(pool)
	// First run creates schema and seeds the catalog.
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("initial MigrateAndSeed: %v", err)
	}

	// Simulate an upgraded DB that previously held old sample ward codes (01001, 79001, 79002) and stale checksum.
	if _, err := pool.Exec(ctx, `
		INSERT INTO wards (code, province_code, name, name_norm, ghn_district_id, ghn_ward_code, cod_supported, is_active)
		VALUES
			('01001', '01', 'Phường Hoàn Kiếm (mẫu cũ)', 'phuong hoan kiem mau cu', 1489, '1A0606', TRUE, TRUE),
			('79001', '79', 'Phường Sài Gòn (mẫu cũ)', 'phuong sai gon mau cu', 1442, '20101', TRUE, TRUE),
			('79002', '79', 'Phường Bến Thành (mẫu cũ)', 'phuong ben thanh mau cu', 1442, '20102', TRUE, TRUE)
		ON CONFLICT (code) DO UPDATE SET is_active = TRUE
	`); err != nil {
		t.Fatalf("insert simulated legacy sample wards: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM address_catalog_meta`); err != nil {
		t.Fatalf("clear address_catalog_meta: %v", err)
	}

	// Re-run MigrateAndSeed simulating upgrade from pre-3b-1 DB.
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("upgrade MigrateAndSeed: %v", err)
	}

	// 1. Exactly 34 provinces and 3,321 active wards; 3 legacy sample wards kept with is_active=false.
	var provCount, activeWardCount, inactiveWardCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM provinces`).Scan(&provCount); err != nil {
		t.Fatalf("count provinces: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wards WHERE is_active = TRUE`).Scan(&activeWardCount); err != nil {
		t.Fatalf("count active wards: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wards WHERE is_active = FALSE`).Scan(&inactiveWardCount); err != nil {
		t.Fatalf("count inactive wards: %v", err)
	}
	if provCount != 34 || activeWardCount != 3321 || inactiveWardCount != 3 {
		t.Fatalf("E15: got provinces=%d active_wards=%d inactive_wards=%d, want 34 / 3321 / 3",
			provCount, activeWardCount, inactiveWardCount)
	}

	// 2. Legacy sample codes excluded from ListWards, but still resolvable by GetWard!
	hcmWards, err := st.ListWards(ctx, "79", "Ben Thanh")
	if err != nil {
		t.Fatalf("ListWards(79, Ben Thanh): %v", err)
	}
	foundOfficialBT := false
	for _, w := range hcmWards {
		if w.Code == "79002" {
			t.Fatalf("E15: inactive legacy sample ward 79002 must NOT appear in ListWards")
		}
		if w.Code == "26743" {
			foundOfficialBT = true
			if !w.CODSupported {
				t.Fatalf("E16: official ward 26743 must have cod_supported=true")
			}
		}
	}
	if !foundOfficialBT {
		t.Fatalf("E15: expected official Phuong Ben Thanh (26743) in ListWards(79, Ben Thanh), got %+v", hcmWards)
	}

	legacyW, err := st.GetWard(ctx, "79001")
	if err != nil {
		t.Fatalf("E15: saved legacy ward 79001 must remain resolvable via GetWard, got err=%v", err)
	}
	if legacyW.IsActive {
		t.Fatalf("E15: legacy ward 79001 must have IsActive=false")
	}

	// 3. E16: Hoang Sa (20333) & Truong Sa (22736) have cod_supported=false.
	for _, island := range []struct {
		code string
		prov string
	}{
		{"20333", "48"},
		{"22736", "56"},
	} {
		w, err := st.GetWard(ctx, island.code)
		if err != nil {
			t.Fatalf("GetWard(%s): %v", island.code, err)
		}
		if w.ProvinceCode != island.prov || w.CODSupported || !w.IsActive {
			t.Fatalf("E16: island ward %s unexpected: %+v", island.code, w)
		}
	}

	// 4. E15: Checksum skip — restarting MigrateAndSeed when checksum matches must NOT re-upsert wards.
	var metaUpdated1 time.Time
	var storedChecksum string
	if err := pool.QueryRow(ctx, `SELECT checksum, updated_at FROM address_catalog_meta WHERE key = 'vn_admin_2025'`).
		Scan(&storedChecksum, &metaUpdated1); err != nil {
		t.Fatalf("read address_catalog_meta: %v", err)
	}
	if storedChecksum == "" {
		t.Fatalf("expected non-empty address_catalog_meta.checksum")
	}
	// Plant a sentinel value in wards.name_norm; if MigrateAndSeed truly skips upsert when checksum matches,
	// the sentinel MUST remain untouched after the second MigrateAndSeed!
	if _, err := pool.Exec(ctx, `UPDATE wards SET name_norm = '__skip_sentinel__' WHERE code = '26743'`); err != nil {
		t.Fatalf("plant sentinel: %v", err)
	}
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("second MigrateAndSeed (checksum unchanged): %v", err)
	}
	var gotNorm string
	if err := pool.QueryRow(ctx, `SELECT name_norm FROM wards WHERE code = '26743'`).Scan(&gotNorm); err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if gotNorm != "__skip_sentinel__" {
		t.Fatalf("E15: MigrateAndSeed re-upserted wards despite unchanged checksum (name_norm=%q)", gotNorm)
	}

	// Mutating checksum forces re-seed and restores canonical name_norm.
	if _, err := pool.Exec(ctx, `UPDATE address_catalog_meta SET checksum = 'stale' WHERE key = 'vn_admin_2025'`); err != nil {
		t.Fatalf("force stale checksum: %v", err)
	}
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("third MigrateAndSeed (checksum changed): %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT name_norm FROM wards WHERE code = '26743'`).Scan(&gotNorm); err != nil {
		t.Fatalf("read restored name_norm: %v", err)
	}
	if gotNorm == "__skip_sentinel__" {
		t.Fatalf("E15: MigrateAndSeed failed to re-upsert when checksum changed")
	}

	// Verify unknown ward still returns ErrNotFound.
	if _, err := st.GetWard(ctx, "99999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown ward, got %v", err)
	}
}

// TestPGStore_W3B_E18_ShipmentCreateJobsLifecycleAndSkipLocked exercises E18 persistence on real Postgres:
//   - Partial unique active index deduplicates concurrent EnqueueShipmentCreateJob calls,
//     while permitting a new job after failed/cancelled.
//   - ClaimDueShipmentCreateJobs uses FOR UPDATE SKIP LOCKED + lease token so 2 concurrent callers claim disjoint rows.
//   - Expired claimed_until lease is re-claimable; stale claim_token is rejected by RecordShipmentCreateJobFailure.
//   - CompleteShipmentCreateJob atomically writes shipments + shipment_events + shipment.updated outbox + succeeded status,
//     and returns ErrJobCancelled (rolling back shipment/outbox) if order.cancelled already cancelled the job.
//   - Exhaustion transitions job to failed and atomically writes shipment.create_failed outbox (aggregate_id = job.ID).
func TestPGStore_W3B_E18_ShipmentCreateJobsLifecycleAndSkipLocked(t *testing.T) {
	pool := openIsolatedShippingTestPool(t)
	ctx := context.Background()

	st := NewPGStore(pool)
	if err := st.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("MigrateAndSeed: %v", err)
	}

	t0 := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)

	// 1. Idempotent EnqueueShipmentCreateJob on active (order_id, FORWARD).
	reqPayload := []byte(`{"OrderID":"ord-pg-1","ShopID":"shop-1","CarrierCode":"mock_express","WeightGrams":500}`)
	j1, existed1, err := st.EnqueueShipmentCreateJob(ctx, &ShipmentCreateJob{
		OrderID: "ord-pg-1", ShopID: "shop-1", Direction: DirectionForward,
		CarrierCode: "mock_express", ServiceCode: "STANDARD", ClientOrderCode: "ord-pg-1",
		RequestJSON: reqPayload, MaxAttempts: 2, NextRetryAt: t0, CreatedAt: t0,
	})
	if err != nil || existed1 || j1.ID == "" {
		t.Fatalf("first EnqueueShipmentCreateJob: job=%+v existed=%v err=%v", j1, existed1, err)
	}
	j1Dup, existed2, err := st.EnqueueShipmentCreateJob(ctx, &ShipmentCreateJob{
		OrderID: "ord-pg-1", ShopID: "shop-1", Direction: DirectionForward,
		CarrierCode: "mock_express", ServiceCode: "STANDARD", ClientOrderCode: "ord-pg-1",
		RequestJSON: reqPayload, MaxAttempts: 2, NextRetryAt: t0, CreatedAt: t0,
	})
	if err != nil || !existed2 || j1Dup.ID != j1.ID {
		t.Fatalf("duplicate EnqueueShipmentCreateJob must return existing active job: got=%+v existed=%v err=%v", j1Dup, existed2, err)
	}

	// Enqueue 3 more due jobs for SKIP LOCKED concurrency check.
	for _, oid := range []string{"ord-pg-2", "ord-pg-3", "ord-pg-4"} {
		_, _, err := st.EnqueueShipmentCreateJob(ctx, &ShipmentCreateJob{
			OrderID: oid, ShopID: "shop-1", Direction: DirectionForward,
			CarrierCode: "mock_express", ServiceCode: "STANDARD", ClientOrderCode: oid,
			RequestJSON: []byte(`{"OrderID":"` + oid + `"}`), MaxAttempts: 2, NextRetryAt: t0, CreatedAt: t0,
		})
		if err != nil {
			t.Fatalf("enqueue %s: %v", oid, err)
		}
	}

	// 2. Two concurrent callers claiming limit=2 MUST receive disjoint jobs via FOR UPDATE SKIP LOCKED.
	type claimRes struct {
		jobs  []ShipmentCreateJob
		token string
		err   error
	}
	results := make([]claimRes, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			jobs, tok, cErr := st.ClaimDueShipmentCreateJobs(ctx, 2, 30*time.Second, t0)
			results[idx] = claimRes{jobs: jobs, token: tok, err: cErr}
		}(i)
	}
	wg.Wait()

	claimed := map[string]string{} // jobID -> claimToken
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("worker %d ClaimDueShipmentCreateJobs: %v", i, r.err)
		}
		for _, j := range r.jobs {
			if _, dup := claimed[j.ID]; dup {
				t.Fatalf("E18 SKIP LOCKED violation: job %s claimed by both workers", j.ID)
			}
			claimed[j.ID] = r.token
		}
	}
	if len(claimed) != 4 {
		t.Fatalf("expected all 4 due jobs claimed across 2 workers, got %d", len(claimed))
	}

	// 3. Lease expiry re-claim + stale claim_token rejection.
	staleToken := claimed[j1.ID]
	reclaimed, newToken, err := st.ClaimDueShipmentCreateJobs(ctx, 10, 30*time.Second, t0.Add(31*time.Second))
	if err != nil || len(reclaimed) != 4 || newToken == staleToken {
		t.Fatalf("expected 4 jobs reclaimed after lease expiry, got len=%d err=%v", len(reclaimed), err)
	}
	nextRetry := t0.Add(35 * time.Second)
	_, ok, err := st.RecordShipmentCreateJobFailure(ctx, j1.ID, staleToken, "stale worker err", &nextRetry, false, nil, t0.Add(31*time.Second))
	if err != nil || ok {
		t.Fatalf("stale claim_token must be rejected (ok=false), got ok=%v err=%v", ok, err)
	}

	// 4. Exhaustion (max_attempts=2): attempt 1 reschedules, attempt 2 marks failed + writes shipment.create_failed outbox.
	jAfter1, ok, err := st.RecordShipmentCreateJobFailure(ctx, j1.ID, newToken, "503 #1", &nextRetry, false, nil, t0.Add(31*time.Second))
	if err != nil || !ok || jAfter1.Status != JobStatusPending || jAfter1.Attempts != 1 {
		t.Fatalf("attempt 1 failure unexpected: job=%+v ok=%v err=%v", jAfter1, ok, err)
	}
	claimed2, tok2, err := st.ClaimDueShipmentCreateJobs(ctx, 1, 30*time.Second, nextRetry)
	if err != nil || len(claimed2) != 1 || claimed2[0].ID != j1.ID {
		t.Fatalf("re-claim j1 after backoff: jobs=%+v err=%v", claimed2, err)
	}
	failOutbox := &JobOutboxParams{
		EventID:     uuid.NewString(),
		Subject:     "shipment.create_failed",
		AggregateID: j1.ID,
		Payload:     []byte(`{"job_id":"` + j1.ID + `","order_id":"ord-pg-1","attempts":2}`),
	}
	jFailed, ok, err := st.RecordShipmentCreateJobFailure(ctx, j1.ID, tok2, "503 #2", &nextRetry, false, failOutbox, nextRetry)
	if err != nil || !ok || jFailed.Status != JobStatusFailed || jFailed.Attempts != 2 {
		t.Fatalf("attempt 2 exhaustion unexpected: job=%+v ok=%v err=%v", jFailed, ok, err)
	}

	var failOutboxAgg string
	if err := pool.QueryRow(ctx, `SELECT aggregate_id FROM outbox_events WHERE event_id = $1 AND subject = 'shipment.create_failed'`, failOutbox.EventID).Scan(&failOutboxAgg); err != nil {
		t.Fatalf("query shipment.create_failed outbox: %v", err)
	}
	if failOutboxAgg != j1.ID {
		t.Fatalf("shipment.create_failed aggregate_id = %q, want %q", failOutboxAgg, j1.ID)
	}

	// After FAILED, seller can enqueue a fresh retry job for the same order_id!
	jFresh, existedFresh, err := st.EnqueueShipmentCreateJob(ctx, &ShipmentCreateJob{
		OrderID: "ord-pg-1", ShopID: "shop-1", Direction: DirectionForward,
		CarrierCode: "mock_express", ServiceCode: "STANDARD", ClientOrderCode: "ord-pg-1",
		RequestJSON: reqPayload, MaxAttempts: 8, NextRetryAt: t0.Add(time.Hour), CreatedAt: t0.Add(time.Hour),
	})
	if err != nil || existedFresh || jFresh.ID == j1.ID {
		t.Fatalf("expected fresh job after FAILED, got job=%+v existed=%v err=%v", jFresh, existedFresh, err)
	}

	// 5. Stale claim_token MUST be rejected on both processing and failed jobs (never flip failed -> succeeded or insert shipment).
	var jobOrd2 string
	var jobOrd3 string
	for _, j := range reclaimed {
		if j.OrderID == "ord-pg-2" {
			jobOrd2 = j.ID
		}
		if j.OrderID == "ord-pg-3" {
			jobOrd3 = j.ID
		}
	}
	shStaleFailed := &Shipment{
		ID: uuid.NewString(), OrderID: "ord-pg-1", ShopID: "shop-1", BuyerID: "buyer-1",
		Direction: DirectionForward, CarrierCode: "mock_express", ServiceCode: "STANDARD",
		TrackingCode: "MOCK-PG-STALE-FAIL", LabelURL: "/labels/MOCK-PG-STALE-FAIL", Status: "created",
		FeeVND: 19500, WeightGrams: 500, CreatedAt: t0, UpdatedAt: t0,
	}
	if _, _, staleFailErr := st.CompleteShipmentCreateJob(ctx, j1.ID, tok2, shStaleFailed, nil, &JobOutboxParams{
		EventID: uuid.NewString(), Subject: "shipment.updated", AggregateID: shStaleFailed.ID, Payload: []byte(`{}`),
	}); !errors.Is(staleFailErr, ErrStaleClaimToken) {
		t.Fatalf("CompleteShipmentCreateJob on failed job must return ErrStaleClaimToken, got %v", staleFailErr)
	}
	j1AfterStale, _ := st.GetShipmentCreateJobByID(ctx, j1.ID)
	if j1AfterStale.Status != JobStatusFailed {
		t.Fatalf("failed job must never be flipped to %q", j1AfterStale.Status)
	}
	var sh1Count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shipments WHERE order_id = 'ord-pg-1'`).Scan(&sh1Count); err != nil || sh1Count != 0 {
		t.Fatalf("stale completion on failed job must insert 0 shipments, got %d err=%v", sh1Count, err)
	}

	sh2 := &Shipment{
		ID: uuid.NewString(), OrderID: "ord-pg-2", ShopID: "shop-1", BuyerID: "buyer-1",
		Direction: DirectionForward, CarrierCode: "mock_express", ServiceCode: "STANDARD",
		TrackingCode: "MOCK-PG-2", LabelURL: "/labels/MOCK-PG-2", Status: "created",
		FeeVND: 19500, WeightGrams: 500, FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "01", ToWardCode: "00070", CreatedAt: t0, UpdatedAt: t0,
	}
	ev2 := &ShipmentEvent{
		ID: uuid.NewString(), ShipmentID: sh2.ID, Status: "created", RawStatus: "created",
		Description: "Đã tạo vận đơn", OccurredAt: t0,
	}
	createdPayload, _ := json.Marshal(map[string]any{
		"shipment_id": sh2.ID, "order_id": sh2.OrderID, "status": "created", "tracking_code": sh2.TrackingCode,
	})
	outbox2 := &JobOutboxParams{
		EventID: uuid.NewString(), Subject: "shipment.updated", AggregateID: sh2.ID, Payload: createdPayload,
	}
	// Stale token on active processing jobOrd2 must return ErrStaleClaimToken and leave 0 shipments/outbox.
	if _, _, staleProcErr := st.CompleteShipmentCreateJob(ctx, jobOrd2, staleToken, sh2, ev2, outbox2); !errors.Is(staleProcErr, ErrStaleClaimToken) {
		t.Fatalf("CompleteShipmentCreateJob with stale token must return ErrStaleClaimToken, got %v", staleProcErr)
	}
	var sh2Before int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shipments WHERE order_id = 'ord-pg-2'`).Scan(&sh2Before); err != nil || sh2Before != 0 {
		t.Fatalf("stale token rollback must leave 0 shipments for ord-pg-2, got %d err=%v", sh2Before, err)
	}

	// Matching claim_token completes atomically (shipment + event + shipment.updated outbox + succeeded).
	saved2, existedSh2, err := st.CompleteShipmentCreateJob(ctx, jobOrd2, newToken, sh2, ev2, outbox2)
	if err != nil || existedSh2 || saved2.ID != sh2.ID {
		t.Fatalf("CompleteShipmentCreateJob: sh=%+v existed=%v err=%v", saved2, existedSh2, err)
	}
	jDone2, err := st.GetShipmentCreateJobByID(ctx, jobOrd2)
	if err != nil || jDone2.Status != JobStatusSucceeded {
		t.Fatalf("expected jobOrd2 succeeded, got %+v err=%v", jDone2, err)
	}
	var obCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE event_id = $1 AND aggregate_id = $2`, outbox2.EventID, sh2.ID).Scan(&obCount); err != nil || obCount != 1 {
		t.Fatalf("expected 1 shipment.updated outbox row, got count=%d err=%v", obCount, err)
	}

	// 6. CancelActiveShipmentCreateJobs -> CompleteShipmentCreateJob returns ErrJobCancelled and inserts 0 shipments/outbox!
	cancelledN, err := st.CancelActiveShipmentCreateJobs(ctx, "ord-pg-3", DirectionForward, "buyer_cancelled")
	if err != nil || cancelledN != 1 {
		t.Fatalf("CancelActiveShipmentCreateJobs: n=%d err=%v", cancelledN, err)
	}
	sh3 := &Shipment{
		ID: uuid.NewString(), OrderID: "ord-pg-3", ShopID: "shop-1", Direction: DirectionForward,
		CarrierCode: "mock_express", ServiceCode: "STANDARD", TrackingCode: "MOCK-PG-3",
		Status: "created", WeightGrams: 500, CreatedAt: t0, UpdatedAt: t0,
	}
	_, _, compCancelErr := st.CompleteShipmentCreateJob(ctx, jobOrd3, newToken, sh3, nil, &JobOutboxParams{
		EventID: uuid.NewString(), Subject: "shipment.updated", AggregateID: sh3.ID, Payload: []byte(`{}`),
	})
	if !errors.Is(compCancelErr, ErrJobCancelled) {
		t.Fatalf("expected ErrJobCancelled on cancelled job completion, got %v", compCancelErr)
	}
	var sh3Count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shipments WHERE order_id = 'ord-pg-3'`).Scan(&sh3Count); err != nil || sh3Count != 0 {
		t.Fatalf("cancelled job rollback must leave 0 shipments for ord-pg-3, got count=%d err=%v", sh3Count, err)
	}
}
