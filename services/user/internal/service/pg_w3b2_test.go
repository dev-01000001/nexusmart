package service

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/migrations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func applyMigrationsUpTo0008(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	versions := []string{
		"0001_init.sql",
		"0002_add_shop_slug.sql",
		"0003_cod_rejections.sql",
		"0004_password_resets_and_lock_reason.sql",
		"0005_shop_bank_accounts.sql",
		"0006_shop_verified_owner_name_and_otp_publish.sql",
		"0007_shop_bank_account_encryption.sql",
		"0008_phone_canonical_verified_unique.sql",
	}
	for _, v := range versions {
		sqlBytes, err := fs.ReadFile(migrations.Files, v)
		if err != nil {
			t.Fatalf("read migration %s: %v", v, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("exec migration %s: %v", v, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, v); err != nil {
			t.Fatalf("record migration %s: %v", v, err)
		}
	}
}

func insertPGUserWithRole(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fullName, email, role string) string {
	t.Helper()
	uid := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, full_name, email, phone, password_hash, role, status)
		VALUES ($1, $2, $3, '', 'hash', $4, 'ACTIVE')
	`, uid, fullName, email, role); err != nil {
		t.Fatalf("insert user %s (%s): %v", email, role, err)
	}
	return uid
}

// 1. Upgrade from 0008 on a populated DB (and idempotency on second RunMigrations).
func TestPG_W3B2_Migration0009UpgradeFrom0008(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	applyMigrationsUpTo0008(ctx, t, pool)

	// Populate existing users & shops before 0009.
	_, activeShopID := insertPGUserShop(ctx, t, pool, "Chu Shop Active", RoleSeller, ShopStatusActive)
	_, suspendedShopID := insertPGUserShop(ctx, t, pool, "Chu Shop Suspended", RoleSeller, ShopStatusSuspended)
	_, pendingShopID := insertPGUserShop(ctx, t, pool, "Chu Shop Pending", RoleBuyer, ShopStatusPending)

	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations (0009 on populated 0008 DB): %v", err)
	}
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}

	// Verify existing shops upgraded cleanly with status_version=1 and suspended_at backfill.
	checkShop := func(shopID string, wantSuspendedAt bool) {
		t.Helper()
		var (
			ver    int64
			reason string
			susAt  *time.Time
		)
		if err := pool.QueryRow(ctx, `SELECT status_version, suspended_reason, suspended_at FROM shops WHERE id = $1`, shopID).
			Scan(&ver, &reason, &susAt); err != nil {
			t.Fatalf("query upgraded shop %s: %v", shopID, err)
		}
		if ver != 1 || reason != "" {
			t.Fatalf("shop %s after 0009: version=%d reason=%q; want 1/empty", shopID, ver, reason)
		}
		if wantSuspendedAt && susAt == nil {
			t.Fatalf("pre-existing SUSPENDED shop %s missing backfilled suspended_at", shopID)
		}
		if !wantSuspendedAt && susAt != nil {
			t.Fatalf("non-suspended shop %s unexpectedly has suspended_at=%v", shopID, susAt)
		}
	}
	checkShop(activeShopID, false)
	checkShop(suspendedShopID, true)
	checkShop(pendingShopID, false)

	// Verify 0009 tables and indexes exist.
	for _, tbl := range []string{"shop_suspension_approvals", "user_activity_logs", "outbox_events"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tbl).Scan(&exists); err != nil || !exists {
			t.Fatalf("expected table %s after 0009: exists=%v err=%v", tbl, exists, err)
		}
	}
}

// 2. Concurrent dual approval race (strictly 1 winner) + app & DB CHECK maker!=checker + unique partial PENDING index.
func TestPG_W3B2_ConcurrentApprovalsAndMakerCheckerConstraints(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	box := newTestBankAccountBox(t)
	srv, _, _ := newPGServerWithLog(t, pool, box, nil)

	makerID := insertPGUserWithRole(ctx, t, pool, "Ops Maker", "pg-maker@example.com", RoleOps)
	checker1ID := insertPGUserWithRole(ctx, t, pool, "Ops Checker 1", "pg-chk1@example.com", RoleOps)
	checker2ID := insertPGUserWithRole(ctx, t, pool, "Admin Checker 2", "pg-chk2@example.com", RoleAdmin)

	makerCtx := ctxWithUser(makerID, RoleOps, "")
	checker1Ctx := ctxWithUser(checker1ID, RoleOps, "")
	checker2Ctx := ctxWithUser(checker2ID, RoleAdmin, "")

	// 2a. Self-approval blocked at BOTH app level (MAKER_CHECKER_CONFLICT) AND Postgres CHECK constraint, plus unique PENDING partial index.
	_, shopCheckID := insertPGUserShop(ctx, t, pool, "Shop Check Constraint", RoleSeller, ShopStatusActive)
	appCheckRes, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{
		ShopID: shopCheckID,
		Reason: "kiem tra tu duyet",
	})
	if err != nil {
		t.Fatalf("RequestShopSuspension: %v", err)
	}
	appCheck := appCheckRes.Approval
	if _, err := srv.AdminDecideShopSuspension(makerCtx, &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: appCheck.ID,
		Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "MAKER_CHECKER_CONFLICT") {
		t.Fatalf("app self-approve: want PermissionDenied MAKER_CHECKER_CONFLICT, got %v", err)
	}

	// Live DB demotion attack (F11): demote checker directly in DB (without revoking JWT) -> MUST fail PermissionDenied!
	demotedCheckerID := insertPGUserWithRole(ctx, t, pool, "Demoted Ops Checker", "pg-demoted-chk@example.com", RoleOps)
	demotedOpsCtx := ctxWithUser(demotedCheckerID, RoleOps, "")
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'moderator' WHERE id = $1`, demotedCheckerID); err != nil {
		t.Fatalf("demote checker in DB: %v", err)
	}
	if _, err := srv.AdminDecideShopSuspension(demotedOpsCtx, &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: appCheck.ID,
		Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("DB-demoted checker with old ops claims: want PermissionDenied, got %v", err)
	}

	// AdminListShopApprovals returns joined shop_name from real Postgres.
	listedApps, err := srv.AdminListShopApprovals(checker1Ctx, &userv1.AdminListShopApprovalsRequest{
		Status: userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING,
		ShopId: shopCheckID,
	})
	if err != nil || listedApps.GetTotal() != 1 || len(listedApps.GetItems()) != 1 || listedApps.GetItems()[0].GetShopName() == "" {
		t.Fatalf("AdminListShopApprovals PG join: err=%v resp=%+v", err, listedApps)
	}

	// Direct SQL bypass attempt MUST fail Postgres CHECK constraint chk_shop_suspension_maker_neq_checker (23514).
	_, sqlErr := pool.Exec(ctx, `
		UPDATE shop_suspension_approvals
		SET status = 'APPROVED', decided_by = requested_by, decided_role = 'ops', decided_at = NOW()
		WHERE id = $1
	`, appCheck.ID)
	if !isCheckViolation(sqlErr) {
		t.Fatalf("DB CHECK maker!=checker expected code 23514, got %v", sqlErr)
	}

	// Direct SQL second PENDING insert for same shop MUST fail unique partial index (23505).
	_, dupErr := pool.Exec(ctx, `
		INSERT INTO shop_suspension_approvals (id, shop_id, status, reason, requested_by, requested_role)
		VALUES ($1, $2, 'PENDING', 'second pending', $3, 'ops')
	`, uuid.NewString(), shopCheckID, checker1ID)
	if !isUniqueViolation(dupErr) {
		t.Fatalf("DB partial unique PENDING index expected code 23505, got %v", dupErr)
	}

	// 2b. Five trials of two checkers racing concurrently behind a barrier on real Postgres -> strictly 1 wins!
	for trial := 0; trial < 5; trial++ {
		ownerID, shopID := insertPGUserShop(ctx, t, pool, "Shop Race Approve", RoleSeller, ShopStatusActive)
		// Insert an active refresh token for the shop owner to verify atomic revocation.
		rtID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, NOW() + INTERVAL '7 days')
		`, rtID, ownerID, uuid.NewString(), "hash-"+rtID); err != nil {
			t.Fatalf("trial %d seed refresh token: %v", trial, err)
		}

		reqRes, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{
			ShopID: shopID,
			Reason: "dua hai nguoi duyet",
		})
		if err != nil {
			t.Fatalf("trial %d RequestShopSuspension: %v", trial, err)
		}
		req := reqRes.Approval

		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = srv.DecideShopSuspension(checker1Ctx, DecideShopSuspensionInput{
				ApprovalID: req.ID,
				Approve:    true,
				Note:       "checker1",
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = srv.DecideShopSuspension(checker2Ctx, DecideShopSuspensionInput{
				ApprovalID: req.ID,
				Approve:    true,
				Note:       "checker2",
			})
		}()
		close(start)
		wg.Wait()

		okCnt, conflictCnt := 0, 0
		for _, e := range errs {
			switch {
			case e == nil:
				okCnt++
			case status.Code(e) == codes.FailedPrecondition:
				conflictCnt++
			default:
				t.Fatalf("trial %d unexpected concurrent approval error: %v", trial, e)
			}
		}
		if okCnt != 1 || conflictCnt != 1 {
			t.Fatalf("trial %d concurrent approval: want 1 ok + 1 FailedPrecondition, got ok=%d conflict=%d", trial, okCnt, conflictCnt)
		}

		// Verify DB invariants: status=SUSPENDED, status_version=2, strictly 1 outbox event, refresh token revoked.
		var (
			shopStatus string
			shopVer    int64
			outboxCnt  int64
			revokedAt  *time.Time
		)
		if err := pool.QueryRow(ctx, `SELECT status, status_version FROM shops WHERE id = $1`, shopID).Scan(&shopStatus, &shopVer); err != nil {
			t.Fatalf("trial %d query shop: %v", trial, err)
		}
		if shopStatus != ShopStatusSuspended || shopVer != 2 {
			t.Fatalf("trial %d shop state = (%q, %d); want (SUSPENDED, 2)", trial, shopStatus, shopVer)
		}
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, shopID, events.SubjectShopStatusChanged).Scan(&outboxCnt); err != nil {
			t.Fatalf("trial %d count outbox: %v", trial, err)
		}
		if outboxCnt != 1 {
			t.Fatalf("trial %d outbox count = %d; want 1", trial, outboxCnt)
		}
		if err := pool.QueryRow(ctx, `SELECT revoked_at FROM refresh_tokens WHERE id = $1`, rtID).Scan(&revokedAt); err != nil {
			t.Fatalf("trial %d query refresh_token: %v", trial, err)
		}
		if revokedAt == nil {
			t.Fatalf("trial %d owner's refresh token was not revoked on shop suspension", trial)
		}
	}
}

// 3. Suspend -> Unsuspend increments status_version monotonically and OutboxRelay delivers in order to real NATS JetStream.
func TestPG_W3B2_SuspendUnsuspendVersionAndOutboxRelay(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	box := newTestBankAccountBox(t)
	pgStore := db.NewSQLStore(pool)
	srv, _, _ := newPGServerWithLog(t, pool, box, nil)

	makerID := insertPGUserWithRole(ctx, t, pool, "Maker Outbox", "pg-ob-maker@example.com", RoleModerator)
	checkerID := insertPGUserWithRole(ctx, t, pool, "Checker Outbox", "pg-ob-checker@example.com", RoleOps)
	ownerID, shopID := insertPGUserShop(ctx, t, pool, "Owner Outbox", RoleSeller, ShopStatusActive)

	// Step 1: Request & approve suspension via gRPC handlers -> status_version 1 -> 2.
	reqResp, err := srv.AdminRequestShopSuspension(ctxWithUser(makerID, RoleModerator, ""), &userv1.AdminRequestShopSuspensionRequest{
		ShopId: shopID,
		Reason: "tam ngung de kiem tra",
	})
	if err != nil || reqResp.GetApproval().GetShopName() == "" {
		t.Fatalf("AdminRequestShopSuspension: err=%v resp=%+v", err, reqResp)
	}
	decResp, err := srv.AdminDecideShopSuspension(ctxWithUser(checkerID, RoleOps, ""), &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: reqResp.GetApproval().GetId(),
		Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		Note:       "duyet",
	})
	if err != nil {
		t.Fatalf("AdminDecideShopSuspension: %v", err)
	}
	if decResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_SUSPENDED ||
		decResp.GetShop().GetStatusVersion() != 2 ||
		decResp.GetShop().GetSuspendedReason() != "tam ngung de kiem tra" ||
		decResp.GetShop().GetSuspendedAt() == nil {
		t.Fatalf("after suspend proto Shop mismatch: %+v", decResp.GetShop())
	}

	// Step 2: Unsuspend via gRPC handler -> status_version 2 -> 3.
	unsuspResp, err := srv.AdminUnsuspendShop(ctxWithUser(checkerID, RoleOps, ""), &userv1.AdminUnsuspendShopRequest{
		ShopId: shopID,
		Reason: "mo lai gian hang",
	})
	if err != nil {
		t.Fatalf("AdminUnsuspendShop: %v", err)
	}
	if unsuspResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE ||
		unsuspResp.GetShop().GetStatusVersion() != 3 ||
		unsuspResp.GetShop().GetSuspendedAt() != nil ||
		unsuspResp.GetShop().GetSuspendedReason() != "" {
		t.Fatalf("after unsuspend proto Shop mismatch: %+v", unsuspResp.GetShop())
	}

	// Step 3: Relay pending outbox events to real NATS JetStream (if TEST_NATS_URL set, which test-db.sh always sets!).
	natsURL := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if natsURL == "" {
		t.Skip("set TEST_NATS_URL to run real NATS JetStream outbox relay test")
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("nats.Connect(%s): %v", natsURL, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("nc.JetStream: %v", err)
	}
	if err := jetstream.EnsurePrimaryStream(js, jetstream.DefaultPrimaryStream); err != nil {
		t.Fatalf("EnsurePrimaryStream: %v", err)
	}

	sub, err := js.SubscribeSync(events.SubjectShopStatusChanged, nats.DeliverNew())
	if err != nil {
		t.Fatalf("SubscribeSync: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	relay := events.NewOutboxRelay(pgStore, js, nil, 25*time.Millisecond, nil)
	published := relay.FlushOnce(ctx)
	if published != 2 {
		t.Fatalf("OutboxRelay.FlushOnce = %d; want 2", published)
	}

	// Verify both events arrived on NEXUSMART in exact version order 2 -> 3 with Nats-Msg-Id == event_id.
	var received []events.ShopStatusChangedPayload
	for i := 0; i < 2; i++ {
		msg, err := sub.NextMsg(3 * time.Second)
		if err != nil {
			t.Fatalf("NextMsg[%d]: %v", i, err)
		}
		var env struct {
			EventID   string          `json:"event_id"`
			EventType string          `json:"event_type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("unmarshal msg[%d]: %v", i, err)
		}
		if env.EventType != events.SubjectShopStatusChanged || msg.Header.Get(nats.MsgIdHdr) != env.EventID {
			t.Fatalf("msg[%d] invalid envelope/header: type=%q id=%q hdr=%q", i, env.EventType, env.EventID, msg.Header.Get(nats.MsgIdHdr))
		}
		var rawMap map[string]any
		if err := json.Unmarshal(env.Payload, &rawMap); err != nil {
			t.Fatalf("unmarshal raw payload[%d]: %v", i, err)
		}
		if _, badKey := rawMap["status_version"]; badKey {
			t.Fatalf("msg[%d] payload leaked deprecated status_version key: %s", i, string(env.Payload))
		}
		var payload events.ShopStatusChangedPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload[%d]: %v", i, err)
		}
		if _, err := time.Parse(time.RFC3339, payload.OccurredAt); err != nil {
			t.Fatalf("msg[%d] invalid occurred_at %q: %v", i, payload.OccurredAt, err)
		}
		received = append(received, payload)
	}

	if received[0].ShopID != shopID || received[0].OwnerUserID != ownerID ||
		received[0].FromStatus != ShopStatusActive || received[0].ToStatus != ShopStatusSuspended || received[0].Version != 2 {
		t.Fatalf("first event mismatch: %+v", received[0])
	}
	if received[1].ShopID != shopID || received[1].OwnerUserID != ownerID ||
		received[1].FromStatus != ShopStatusSuspended || received[1].ToStatus != ShopStatusActive || received[1].Version != 3 {
		t.Fatalf("second event mismatch: %+v", received[1])
	}

	// Second flush must publish 0 (idempotent) and DB must have 0 unpublished rows.
	if second := relay.FlushOnce(ctx); second != 0 {
		t.Fatalf("second FlushOnce = %d; want 0", second)
	}
	var pendingCnt int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pendingCnt); err != nil {
		t.Fatalf("count pending outbox: %v", err)
	}
	if pendingCnt != 0 {
		t.Fatalf("expected 0 unpublished outbox rows, got %d", pendingCnt)
	}

	// Step 4: Verify per-aggregate lease ordering & backoff on real Postgres:
	// Insert 2 pending events for shopA (e1, e2) and 1 for shopB (e3).
	// Fail e1 with non-infra error -> e2 (same shopA) must be skipped (never published ahead of e1),
	// while e3 (different shopB) MUST still be published!
	shopA := uuid.NewString()
	shopB := uuid.NewString()
	e1, e2, e3 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, ev := range []struct {
		id  string
		agg string
	}{
		{e1, shopA},
		{e2, shopA},
		{e3, shopB},
	} {
		if err := pgStore.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			ID:          ev.id,
			AggregateID: ev.agg,
			EventType:   events.SubjectShopStatusChanged,
			PayloadJSON: []byte(`{"shop_id":"` + ev.agg + `"}`),
		}); err != nil {
			t.Fatalf("InsertOutboxEvent %s: %v", ev.id, err)
		}
	}

	var deliveredIDs []string
	n, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
		if ev.ID == e1 {
			return status.Error(codes.InvalidArgument, "simulated non-infra failure")
		}
		deliveredIDs = append(deliveredIDs, ev.ID)
		return nil
	})
	if err != nil || n != 1 || len(deliveredIDs) != 1 || deliveredIDs[0] != e3 {
		t.Fatalf("per-aggregate failure isolation: n=%d err=%v delivered=%v; want only e3 (%s)", n, err, deliveredIDs, e3)
	}
	// While e1 is in backoff (claimed_until > NOW()), a second poll MUST NOT claim e2!
	n2, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
		t.Fatalf("unexpected out-of-order claim of %s while earlier event e1 is in backoff", ev.ID)
		return nil
	})
	if err != nil || n2 != 0 {
		t.Fatalf("second poll during e1 backoff: n=%d err=%v; want 0/nil", n2, err)
	}
}

// 4. Real Postgres RBAC: cskh cannot lock staff; concurrent lock of last 2 super_admins preserves >= 1 active super_admin.
func TestPG_W3B2_RBAC_CSKHStaffProtectionAndLastSuperAdminRace(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	box := newTestBankAccountBox(t)
	srv, _, _ := newPGServerWithLog(t, pool, box, nil)

	super1ID := insertPGUserWithRole(ctx, t, pool, "Super 1", "pg-sa1@example.com", RoleSuperAdmin)
	super2ID := insertPGUserWithRole(ctx, t, pool, "Super 2", "pg-sa2@example.com", RoleSuperAdmin)
	adminID := insertPGUserWithRole(ctx, t, pool, "Admin 1", "pg-adm1@example.com", RoleAdmin)
	cskhID := insertPGUserWithRole(ctx, t, pool, "CSKH 1", "pg-cskh1@example.com", RoleCSKH)
	modID := insertPGUserWithRole(ctx, t, pool, "Mod 1", "pg-mod1@example.com", RoleModerator)
	opsID := insertPGUserWithRole(ctx, t, pool, "Ops 1", "pg-ops1@example.com", RoleOps)
	buyerID := insertPGUserWithRole(ctx, t, pool, "Buyer 1", "pg-buyer1@example.com", RoleBuyer)

	cskhCtx := ctxWithUser(cskhID, RoleCSKH, "")

	// cskh CANNOT lock any staff member (moderator, ops, admin, super_admin).
	for _, staffID := range []string{modID, opsID, adminID, super1ID} {
		if _, err := srv.AdminUpdateUserStatus(cskhCtx, &userv1.AdminUpdateUserStatusRequest{
			TargetUserId: staffID,
			Status:       userv1.UserStatus_USER_STATUS_LOCKED,
			Reason:       "cskh unauthorized lock",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cskh lock staff %s: want PermissionDenied, got %v", staffID, err)
		}
		var st string
		if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, staffID).Scan(&st); err != nil || st != StatusActive {
			t.Fatalf("staff %s status changed unexpectedly: st=%q err=%v", staffID, st, err)
		}
	}

	// cskh CAN lock buyer.
	if _, err := srv.AdminUpdateUserStatus(cskhCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: buyerID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "bom hang",
	}); err != nil {
		t.Fatalf("cskh lock buyer: %v", err)
	}

	// Strict CanActOnTargetRole (F10): even two super_admins cannot lock each other (equal rank).
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = srv.AdminUpdateUserStatus(ctxWithUser(super1ID, RoleSuperAdmin, ""), &userv1.AdminUpdateUserStatusRequest{
			TargetUserId: super2ID,
			Status:       userv1.UserStatus_USER_STATUS_LOCKED,
			Reason:       "lock super2",
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errs[1] = srv.AdminUpdateUserStatus(ctxWithUser(super2ID, RoleSuperAdmin, ""), &userv1.AdminUpdateUserStatusRequest{
			TargetUserId: super1ID,
			Status:       userv1.UserStatus_USER_STATUS_LOCKED,
			Reason:       "lock super1",
		})
	}()
	close(start)
	wg.Wait()

	for i, e := range errs {
		if status.Code(e) != codes.PermissionDenied {
			t.Fatalf("peer super_admin lock[%d]: want PermissionDenied, got %v", i, e)
		}
	}

	var remainingSuperAdmins int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'super_admin' AND status = 'ACTIVE'`).Scan(&remainingSuperAdmins); err != nil {
		t.Fatalf("count active super_admins: %v", err)
	}
	if remainingSuperAdmins != 2 {
		t.Fatalf("expected both peer super_admins still ACTIVE, got %d", remainingSuperAdmins)
	}

	// Verify ListAuditLogs scoping on real Postgres: cskh sees only own rows (not super_admin's), and PermissionDenied when querying another actor_id.
	if _, err := srv.AdminUpdateUserStatus(ctxWithUser(super1ID, RoleSuperAdmin, ""), &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: adminID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "super locks admin",
	}); err != nil {
		t.Fatalf("super1 lock admin: %v", err)
	}
	cskhAudit, err := srv.ListAuditLogs(cskhCtx, &userv1.ListAuditLogsRequest{Page: 1, PageSize: 50})
	if err != nil || cskhAudit.GetTotal() == 0 {
		t.Fatalf("cskh ListAuditLogs own: err=%v total=%d", err, cskhAudit.GetTotal())
	}
	for _, entry := range cskhAudit.GetLogs() {
		if entry.GetActorId() != cskhID {
			t.Fatalf("cskh ListAuditLogs leaked another actor's entry: actor_id=%s", entry.GetActorId())
		}
	}
	if _, err := srv.ListAuditLogs(cskhCtx, &userv1.ListAuditLogsRequest{ActorId: super1ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cskh ListAuditLogs(super1ID): want PermissionDenied, got %v", err)
	}
	superAudit, err := srv.ListAuditLogs(ctxWithUser(super1ID, RoleSuperAdmin, ""), &userv1.ListAuditLogsRequest{Page: 1, PageSize: 50})
	if err != nil || superAudit.GetTotal() < 2 {
		t.Fatalf("super_admin ListAuditLogs all: err=%v total=%d", err, superAudit.GetTotal())
	}
}

// 5. Real Postgres user_activity_logs UPDATE/DELETE trigger + 180-day retention purge job.
func TestPG_W3B2_ActivityLogsTriggerAndRetention(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	box := newTestBankAccountBox(t)
	srv, _, _ := newPGServerWithLog(t, pool, box, nil)

	userID := insertPGUserWithRole(ctx, t, pool, "Activity Target", "pg-act@example.com", RoleBuyer)

	// Insert 3 recent rows (now, 30 days ago, 179 days ago) and 5 expired rows (181..185 days ago).
	nowID := uuid.NewString()
	days179ID := uuid.NewString()
	days181DirectID := uuid.NewString()

	if _, err := pool.Exec(ctx, `
		INSERT INTO user_activity_logs (id, user_id, event_type, category, ip_address, metadata_json, created_at)
		VALUES
			($1, $4, 'login_succeeded', 'auth', '203.0.113.xxx', '{}', NOW()),
			($2, $4, 'profile_updated', 'profile', '203.0.113.xxx', '{}', NOW() - INTERVAL '179 days'),
			($3, $4, 'login_failed', 'auth', '203.0.113.xxx', '{}', NOW() - INTERVAL '181 days')
	`, nowID, days179ID, days181DirectID, userID); err != nil {
		t.Fatalf("seed activity logs: %v", err)
	}

	// Trigger MUST reject UPDATE on user_activity_logs.
	if _, err := pool.Exec(ctx, `UPDATE user_activity_logs SET event_type = 'tampered' WHERE id = $1`, nowID); err == nil {
		t.Fatal("expected trigger to forbid UPDATE on user_activity_logs, got nil")
	}

	// Trigger MUST reject DELETE on rows newer than 180 days (both 0-day and 179-day rows).
	if _, err := pool.Exec(ctx, `DELETE FROM user_activity_logs WHERE id = $1`, nowID); err == nil {
		t.Fatal("expected trigger to forbid DELETE on fresh user_activity_logs row, got nil")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM user_activity_logs WHERE id = $1`, days179ID); err == nil {
		t.Fatal("expected trigger to forbid DELETE on 179-day-old user_activity_logs row, got nil")
	}

	// Trigger MUST allow DELETE on row strictly older than 180 days (181 days).
	if tag, err := pool.Exec(ctx, `DELETE FROM user_activity_logs WHERE id = $1`, days181DirectID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expected DELETE of 181-day-old row to succeed, got rows=%d err=%v", tag.RowsAffected(), err)
	}

	// Seed 5 more expired rows (182..186 days old) and 1 more recent row (30 days old) to test batched retention job.
	for i := 0; i < 5; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_activity_logs (id, user_id, event_type, category, ip_address, metadata_json, created_at)
			VALUES ($1, $2, 'login_succeeded', 'auth', '203.0.113.xxx', '{}', NOW() - (($3)::int * INTERVAL '1 day'))
		`, uuid.NewString(), userID, 182+i); err != nil {
			t.Fatalf("seed expired row %d: %v", i, err)
		}
	}
	days30ID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_activity_logs (id, user_id, event_type, category, ip_address, metadata_json, created_at)
		VALUES ($1, $2, 'status_changed', 'status', '203.0.113.xxx', '{}', NOW() - INTERVAL '30 days')
	`, days30ID, userID); err != nil {
		t.Fatalf("seed 30-day row: %v", err)
	}

	// Run retention job with batchSize=2 -> must delete all 5 expired rows across 3 batches and keep the 3 recent rows!
	deleted, err := srv.PurgeExpiredUserActivities(ctx, 2)
	if err != nil {
		t.Fatalf("PurgeExpiredUserActivities: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("PurgeExpiredUserActivities deleted = %d; want 5", deleted)
	}

	// Verify strictly the 3 recent rows remain.
	var remaining int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_activity_logs WHERE user_id = $1`, userID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining activity logs: %v", err)
	}
	if remaining != 3 {
		t.Fatalf("remaining activity logs = %d; want 3", remaining)
	}

	// Verify ListUserActivities returns those 3 rows ordered newest-first for any staff role (e.g. accountant).
	accID := insertPGUserWithRole(ctx, t, pool, "PG Accountant", "pg-act-acc@example.com", RoleAccountant)
	listRes, err := srv.ListUserActivities(ctxWithUser(accID, RoleAccountant, ""), ListUserActivitiesInput{
		UserID:   userID,
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListUserActivities: %v", err)
	}
	if listRes.Total != 3 || len(listRes.Entries) != 3 {
		t.Fatalf("ListUserActivities: want 3 entries, got total=%d len=%d", listRes.Total, len(listRes.Entries))
	}
	if listRes.Entries[0].ID != nowID || listRes.Entries[1].ID != days30ID || listRes.Entries[2].ID != days179ID {
		t.Fatalf("ListUserActivities order mismatch: got [%s, %s, %s]",
			listRes.Entries[0].ID, listRes.Entries[1].ID, listRes.Entries[2].ID)
	}
}

func waitForPGBlockedBackends(ctx context.Context, t *testing.T, pool *pgxpool.Pool, minBlocked int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var blocked int
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
		`).Scan(&blocked)
		if err == nil && blocked >= minBlocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for >= %d lock-blocked backend(s) in Postgres", timeout, minBlocked)
}

// 6. Real Postgres FOR SHARE serialization & deadlock-freedom:
// DecideShopSuspension || UpdateUserRole (demote) / AdminUpdateUserStatus (lock):
// - If demotion commits before decision commits -> DecideShopSuspension MUST return PermissionDenied.
// - Once DecideShopSuspension locks checker FOR SHARE, demotion cannot commit ahead of it.
// - Parallel demote/lock/owner-mutation || DecideShopSuspension never deadlocks (SQLSTATE 40P01).
func TestPG_W3B2_DecideShopSuspensionDemoteRaceForShare(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	box := newTestBankAccountBox(t)
	srv, _, _ := newPGServerWithLog(t, pool, box, nil)

	superID := insertPGUserWithRole(ctx, t, pool, "Race Super Admin", "pg-race-super@example.com", RoleSuperAdmin)
	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")
	makerID := insertPGUserWithRole(ctx, t, pool, "Race Maker", "pg-race-maker@example.com", RoleModerator)
	makerCtx := ctxWithUser(makerID, RoleModerator, "")

	t.Run("DemoteCommitsWhileDecideInFlight_ReturnsPermissionDenied", func(t *testing.T) {
		checkerID := insertPGUserWithRole(ctx, t, pool, "Race Checker A", "pg-race-chk-a@example.com", RoleOps)
		checkerCtx := ctxWithUser(checkerID, RoleOps, "")
		_, shopID := insertPGUserShop(ctx, t, pool, "Race Shop A", RoleSeller, ShopStatusActive)

		reqRes, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{
			ShopID: shopID,
			Reason: "kiem tra dua giang chuc truoc commit",
		})
		if err != nil {
			t.Fatalf("RequestShopSuspension: %v", err)
		}

		// Hold FOR UPDATE on the shop row so DecideShopSuspension enters its tx and blocks on shops.
		holdTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin holdTx: %v", err)
		}
		defer func() { _ = holdTx.Rollback(ctx) }()
		var lockedShopID string
		if err := holdTx.QueryRow(ctx, `SELECT id FROM shops WHERE id = $1 FOR UPDATE`, shopID).Scan(&lockedShopID); err != nil {
			t.Fatalf("lock shop row in holdTx: %v", err)
		}

		decErrCh := make(chan error, 1)
		go func() {
			_, dErr := srv.AdminDecideShopSuspension(checkerCtx, &userv1.AdminDecideShopSuspensionRequest{
				ApprovalId: reqRes.Approval.ID,
				Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				Note:       "approve during demotion",
			})
			decErrCh <- dErr
		}()

		// Wait deterministically until DecideShopSuspension is blocked on the shops row lock.
		waitForPGBlockedBackends(ctx, t, pool, 1, 5*time.Second)

		// Demote checker and commit BEFORE releasing holdTx.
		if _, err := srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
			TargetUserID: checkerID,
			Role:         RoleBuyer,
			Reason:       "demote while decide waits on shop",
		}); err != nil {
			t.Fatalf("UpdateUserRole demote checker: %v", err)
		}

		// Release holdTx so DecideShopSuspension resumes.
		if err := holdTx.Rollback(ctx); err != nil {
			t.Fatalf("rollback holdTx: %v", err)
		}

		decErr := <-decErrCh
		if status.Code(decErr) != codes.PermissionDenied {
			t.Fatalf("DecideShopSuspension after committed demotion: want PermissionDenied, got %v", decErr)
		}

		// Verify shop stayed ACTIVE (version 1) and approval stayed PENDING with 0 outbox rows.
		var shopStatus string
		var shopVer int64
		if err := pool.QueryRow(ctx, `SELECT status, status_version FROM shops WHERE id = $1`, shopID).Scan(&shopStatus, &shopVer); err != nil {
			t.Fatalf("query shop state: %v", err)
		}
		if shopStatus != ShopStatusActive || shopVer != 1 {
			t.Fatalf("shop state after denied decide: status=%s ver=%d; want ACTIVE/1", shopStatus, shopVer)
		}
		var appStatus string
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_suspension_approvals WHERE id = $1`, reqRes.Approval.ID).Scan(&appStatus); err != nil {
			t.Fatalf("query approval state: %v", err)
		}
		if appStatus != ApprovalStatusPending {
			t.Fatalf("approval status = %s; want PENDING", appStatus)
		}
		var outboxCnt int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1`, shopID).Scan(&outboxCnt); err != nil {
			t.Fatalf("count outbox: %v", err)
		}
		if outboxCnt != 0 {
			t.Fatalf("expected 0 outbox events for denied decide, got %d", outboxCnt)
		}
	})

	t.Run("ForShareBlocksDemoteUntilDecideCommits", func(t *testing.T) {
		checkerID := insertPGUserWithRole(ctx, t, pool, "Race Checker B", "pg-race-chk-b@example.com", RoleOps)
		checkerCtx := ctxWithUser(checkerID, RoleOps, "")
		ownerID, shopID := insertPGUserShop(ctx, t, pool, "Race Shop B", RoleSeller, ShopStatusActive)

		reqRes, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{
			ShopID: shopID,
			Reason: "kiem tra FOR SHARE chan giang chuc giua tx",
		})
		if err != nil {
			t.Fatalf("RequestShopSuspension: %v", err)
		}

		// Hold FOR UPDATE on the shop owner row so DecideShopSuspension locks shops -> approvals ->
		// checker (FOR SHARE) and then blocks on owner before COMMIT.
		holdOwnerTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin holdOwnerTx: %v", err)
		}
		defer func() { _ = holdOwnerTx.Rollback(ctx) }()
		var lockedOwnerID string
		if err := holdOwnerTx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, ownerID).Scan(&lockedOwnerID); err != nil {
			t.Fatalf("lock owner row in holdOwnerTx: %v", err)
		}

		decErrCh := make(chan error, 1)
		go func() {
			_, dErr := srv.AdminDecideShopSuspension(checkerCtx, &userv1.AdminDecideShopSuspensionRequest{
				ApprovalId: reqRes.Approval.ID,
				Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				Note:       "approve holding FOR SHARE",
			})
			decErrCh <- dErr
		}()

		// Wait until DecideShopSuspension has passed checker check and is blocked on ownerID.
		waitForPGBlockedBackends(ctx, t, pool, 1, 5*time.Second)

		// Now attempt to demote checker while DecideShopSuspension holds FOR SHARE on checker.
		demoteErrCh := make(chan error, 1)
		go func() {
			_, uErr := srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
				TargetUserID: checkerID,
				Role:         RoleBuyer,
				Reason:       "demote must wait on FOR SHARE",
			})
			demoteErrCh <- uErr
		}()

		// UpdateUserRole MUST block waiting for checker's FOR SHARE lock (2 blocked backends total)
		// and MUST NOT commit before holdOwnerTx releases DecideShopSuspension!
		waitForPGBlockedBackends(ctx, t, pool, 2, 5*time.Second)
		select {
		case uErr := <-demoteErrCh:
			t.Fatalf("UpdateUserRole committed BEFORE DecideShopSuspension released FOR SHARE (err=%v)", uErr)
		default:
		}

		// Release holdOwnerTx: DecideShopSuspension commits first, then UpdateUserRole commits with no deadlock.
		if err := holdOwnerTx.Rollback(ctx); err != nil {
			t.Fatalf("rollback holdOwnerTx: %v", err)
		}

		if decErr := <-decErrCh; decErr != nil {
			t.Fatalf("DecideShopSuspension failed after release: %v", decErr)
		}
		if demoteErr := <-demoteErrCh; demoteErr != nil {
			t.Fatalf("UpdateUserRole failed after release: %v", demoteErr)
		}
	})

	t.Run("ParallelDemoteLockAndOwnerMutation_NoDeadlock", func(t *testing.T) {
		for trial := 0; trial < 3; trial++ {
			// A) UpdateUserRole(demote ops checker) || DecideShopSuspension(ops checker)
			chkDemoteID := insertPGUserWithRole(ctx, t, pool, "Par Demote", "pg-par-dem-"+uuid.NewString()+"@example.com", RoleOps)
			_, shopDemID := insertPGUserShop(ctx, t, pool, "Par Shop Dem", RoleSeller, ShopStatusActive)
			reqDem, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{ShopID: shopDemID, Reason: "parallel demote"})
			if err != nil {
				t.Fatalf("trial %d request dem: %v", trial, err)
			}

			start1 := make(chan struct{})
			var wg1 sync.WaitGroup
			var demErr, decDemErr error
			wg1.Add(2)
			go func() {
				defer wg1.Done()
				<-start1
				_, demErr = srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
					TargetUserID: chkDemoteID,
					Role:         RoleModerator,
					Reason:       "parallel demote",
				})
			}()
			go func() {
				defer wg1.Done()
				<-start1
				_, decDemErr = srv.AdminDecideShopSuspension(ctxWithUser(chkDemoteID, RoleOps, ""), &userv1.AdminDecideShopSuspensionRequest{
					ApprovalId: reqDem.Approval.ID,
					Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				})
			}()
			close(start1)
			wg1.Wait()
			if demErr != nil {
				t.Fatalf("trial %d UpdateUserRole err: %v", trial, demErr)
			}
			if decDemErr != nil && status.Code(decDemErr) != codes.PermissionDenied {
				t.Fatalf("trial %d DecideShopSuspension unexpected err (possible deadlock): %v", trial, decDemErr)
			}

			// B) AdminUpdateUserStatus(lock ops checker) || DecideShopSuspension(ops checker)
			chkLockID := insertPGUserWithRole(ctx, t, pool, "Par Lock", "pg-par-lock-"+uuid.NewString()+"@example.com", RoleOps)
			_, shopLockID := insertPGUserShop(ctx, t, pool, "Par Shop Lock", RoleSeller, ShopStatusActive)
			reqLock, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{ShopID: shopLockID, Reason: "parallel lock"})
			if err != nil {
				t.Fatalf("trial %d request lock: %v", trial, err)
			}

			start2 := make(chan struct{})
			var wg2 sync.WaitGroup
			var lockErr, decLockErr error
			wg2.Add(2)
			go func() {
				defer wg2.Done()
				<-start2
				_, lockErr = srv.AdminUpdateUserStatus(superCtx, &userv1.AdminUpdateUserStatusRequest{
					TargetUserId: chkLockID,
					Status:       userv1.UserStatus_USER_STATUS_LOCKED,
					Reason:       "parallel lock",
				})
			}()
			go func() {
				defer wg2.Done()
				<-start2
				_, decLockErr = srv.AdminDecideShopSuspension(ctxWithUser(chkLockID, RoleOps, ""), &userv1.AdminDecideShopSuspensionRequest{
					ApprovalId: reqLock.Approval.ID,
					Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				})
			}()
			close(start2)
			wg2.Wait()
			if lockErr != nil {
				t.Fatalf("trial %d AdminUpdateUserStatus err: %v", trial, lockErr)
			}
			if decLockErr != nil && status.Code(decLockErr) != codes.PermissionDenied {
				t.Fatalf("trial %d DecideShopSuspension (lock race) unexpected err: %v", trial, decLockErr)
			}

			// C) UpdateUserRole(demote seller owner) || DecideShopSuspension(super_admin checker, owner's shop)
			// Proves super_admin decide (shops -> approvals -> checker FOR SHARE -> owner FOR UPDATE) never
			// deadlocks with UpdateUserRole (single target owner FOR UPDATE -> refresh_tokens).
			ownerMutID, shopOwnID := insertPGUserShop(ctx, t, pool, "Par Shop Owner", RoleSeller, ShopStatusActive)
			reqOwn, err := srv.RequestShopSuspension(makerCtx, RequestShopSuspensionInput{ShopID: shopOwnID, Reason: "parallel owner"})
			if err != nil {
				t.Fatalf("trial %d request owner: %v", trial, err)
			}

			start3 := make(chan struct{})
			var wg3 sync.WaitGroup
			var ownRoleErr, decOwnErr error
			wg3.Add(2)
			go func() {
				defer wg3.Done()
				<-start3
				_, ownRoleErr = srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
					TargetUserID: ownerMutID,
					Role:         RoleBuyer,
					Reason:       "parallel owner role",
				})
			}()
			go func() {
				defer wg3.Done()
				<-start3
				_, decOwnErr = srv.AdminDecideShopSuspension(superCtx, &userv1.AdminDecideShopSuspensionRequest{
					ApprovalId: reqOwn.Approval.ID,
					Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				})
			}()
			close(start3)
			wg3.Wait()
			if ownRoleErr != nil || decOwnErr != nil {
				t.Fatalf("trial %d super_admin decide || owner role change: ownRoleErr=%v decOwnErr=%v", trial, ownRoleErr, decOwnErr)
			}
		}
	})
}

// 7. Real Postgres pickup_address_id deletion guard across ACTIVE, SUSPENDED, PENDING (blocked)
// vs REJECTED (allowed), plus migration 0010 partial index upgrade from 0008.
func TestPG_W3B2_DeletePickupAddressGuardAllInUseStatuses(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	applyMigrationsUpTo0008(ctx, t, pool)

	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations (0009+0010 on 0008 DB): %v", err)
	}
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}

	// Verify 0010 partial index covers ACTIVE, SUSPENDED, PENDING and drops obsolete ACTIVE-only index.
	var newIdxDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'shops' AND indexname = 'idx_shops_in_use_pickup_address'
	`).Scan(&newIdxDef); err != nil {
		t.Fatalf("expected idx_shops_in_use_pickup_address after migration 0010: %v", err)
	}
	for _, st := range []string{"ACTIVE", "SUSPENDED", "PENDING"} {
		if !strings.Contains(newIdxDef, st) {
			t.Fatalf("idx_shops_in_use_pickup_address missing status %s: %s", st, newIdxDef)
		}
	}
	var oldIdxExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE tablename = 'shops' AND indexname = 'idx_shops_active_pickup_address'
		)
	`).Scan(&oldIdxExists); err != nil || oldIdxExists {
		t.Fatalf("obsolete idx_shops_active_pickup_address should be dropped: exists=%v err=%v", oldIdxExists, err)
	}

	box := newTestBankAccountBox(t)
	srv, _, _ := newPGServerWithLog(t, pool, box, nil)

	for i, tc := range []struct {
		status      string
		wantBlocked bool
	}{
		{ShopStatusActive, true},
		{ShopStatusSuspended, true},
		{ShopStatusPending, true},
		{ShopStatusRejected, false},
	} {
		ownerID := insertPGUserWithRole(ctx, t, pool, "PG Pickup "+tc.status, "pg-pickup-"+strings.ToLower(tc.status)+"@example.com", RoleSeller)
		ownerCtx := ctxWithUser(ownerID, RoleSeller, "")
		addrResp, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
			RecipientName:  "Kho PG " + tc.status,
			RecipientPhone: "090820001" + string(rune('1'+i)),
			ProvinceCode:   "79",
			ProvinceName:   "TP. Ho Chi Minh",
			WardCode:       "26734",
			WardName:       "Phuong Ben Nghe",
			StreetAddress:  "99 Nguyen Hue",
			IsDefault:      true,
		})
		if err != nil {
			t.Fatalf("CreateAddress(%s): %v", tc.status, err)
		}
		addrID := addrResp.GetAddress().GetId()

		shopID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO shops (id, owner_user_id, name, slug, pickup_address_id, status)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, shopID, ownerID, "PG Shop "+tc.status, "pg-shop-pickup-"+strings.ToLower(tc.status), addrID, tc.status); err != nil {
			t.Fatalf("insert %s shop: %v", tc.status, err)
		}

		_, delErr := srv.DeleteAddress(ownerCtx, &userv1.DeleteAddressRequest{AddressId: addrID})
		if tc.wantBlocked {
			if status.Code(delErr) != codes.FailedPrecondition {
				t.Errorf("PG DeleteAddress for %s shop pickup: want FailedPrecondition, got %v", tc.status, delErr)
			}
			var stillExists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM addresses WHERE id = $1)`, addrID).Scan(&stillExists); err != nil || !stillExists {
				t.Errorf("PG address for %s shop should still exist: exists=%v err=%v", tc.status, stillExists, err)
			}
		} else {
			if delErr != nil {
				t.Errorf("PG DeleteAddress for %s shop pickup: want nil, got %v", tc.status, delErr)
			}
			var stillExists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM addresses WHERE id = $1)`, addrID).Scan(&stillExists); err != nil || stillExists {
				t.Errorf("PG address for %s shop should be deleted: exists=%v err=%v", tc.status, stillExists, err)
			}
		}
	}
}
