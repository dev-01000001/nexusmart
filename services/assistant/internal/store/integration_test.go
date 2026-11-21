package store_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/assistant/internal/quota"
	"github.com/nexusmart/core/services/assistant/internal/store"
	"github.com/nexusmart/core/services/assistant/migrations"
	"github.com/redis/go-redis/v9"
)

func TestPostgresStoreIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real Postgres integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migrations.Apply: %v", err)
	}
	// Idempotent re-apply must succeed.
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("second migrations.Apply: %v", err)
	}

	pg := store.NewPGStore(pool)
	if err := pg.Ping(ctx); err != nil {
		t.Fatalf("PGStore.Ping: %v", err)
	}

	conv, err := pg.EnsureConversation(ctx, store.Conversation{
		UserID: "buyer-it-1",
		ShopID: "shop-bach-dang",
	})
	if err != nil || conv.ID == "" {
		t.Fatalf("EnsureConversation: conv=%+v err=%v", conv, err)
	}

	// Verify PII masking persists redacted text in real Postgres.
	if err := pg.SaveMessage(ctx, store.Message{
		ConversationID: conv.ID,
		Role:           "user",
		Content:        "Mail buyer@test.vn, SĐT 0901234567, token Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.abc123456789, giá 179.000đ",
	}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	msgs, err := pg.ListMessages(ctx, conv.ID, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessages: len=%d err=%v", len(msgs), err)
	}
	if strings.Contains(msgs[0].Content, "buyer@test.vn") || strings.Contains(msgs[0].Content, "0901234567") || strings.Contains(msgs[0].Content, "eyJhbGci") {
		t.Fatalf("expected PII/secret redacted in Postgres message, got: %s", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "179.000đ") {
		t.Fatalf("expected VND price preserved, got: %s", msgs[0].Content)
	}

	if err := pg.SaveFeedback(ctx, store.Feedback{
		ConversationID: conv.ID,
		MessageID:      msgs[0].ID,
		UserID:         "buyer-it-1",
		Helpful:        true,
		Comment:        "Rất hữu ích, liên hệ test@example.com",
	}); err != nil {
		t.Fatalf("SaveFeedback: %v", err)
	}

	if err := pg.UpsertReviews(ctx, []store.Review{
		{ID: "rev-it-1", ProductID: "prod-it-1", ShopID: "shop-bach-dang", Rating: 5, Comment: "Vải mát lắm"},
		{ID: "rev-it-2", ProductID: "prod-it-1", ShopID: "shop-bach-dang", Rating: 4, Comment: "Giao nhanh"},
	}); err != nil {
		t.Fatalf("UpsertReviews: %v", err)
	}
	revs, err := pg.ListReviewsByProduct(ctx, "prod-it-1")
	if err != nil || len(revs) != 2 {
		t.Fatalf("ListReviewsByProduct: len=%d err=%v", len(revs), err)
	}

	if err := pg.UpsertReviewSummary(ctx, store.ReviewSummary{
		ProductID:     "prod-it-1",
		ShopID:        "shop-bach-dang",
		ReviewCount:   12,
		AverageRating: 4.5,
		Summary:       "Sản phẩm được đánh giá tốt.",
		Pros:          []string{"Vải mát"},
		Cons:          []string{"Form rộng"},
	}); err != nil {
		t.Fatalf("UpsertReviewSummary: %v", err)
	}
	gotSum, err := pg.GetReviewSummary(ctx, "prod-it-1")
	if err != nil || gotSum.ReviewCount != 12 || len(gotSum.Pros) != 1 {
		t.Fatalf("GetReviewSummary: %+v err=%v", gotSum, err)
	}

	tkt, err := pg.SaveHandoffTicket(ctx, store.HandoffTicket{
		UserID:             "buyer-it-1",
		ShopID:             "shop-bach-dang",
		ConversationID:     conv.ID,
		ChatConversationID: "chat-conv-it-1",
		Reason:             "Cần hỗ trợ đổi size",
		Summary:            "Khách muốn đổi từ M sang L",
		Status:             "transferred",
	})
	if err != nil || tkt.ID == "" {
		t.Fatalf("SaveHandoffTicket: tkt=%+v err=%v", tkt, err)
	}

	if err := pg.SaveUsageLog(ctx, store.UsageLog{
		TraceID:      "trace-it-1",
		UserID:       "buyer-it-1",
		ShopID:       "shop-bach-dang",
		Endpoint:     "chat",
		Model:        "mock",
		InputTokens:  120,
		OutputTokens: 80,
		CostVND:      198,
		LatencyMs:    12,
		TTFTMs:       3,
	}); err != nil {
		t.Fatalf("SaveUsageLog: %v", err)
	}

	// Idempotent NATS event deduplication.
	processed, err := pg.IsEventProcessed(ctx, "evt-it-1")
	if err != nil || processed {
		t.Fatalf("expected evt-it-1 unprocessed, got %v err=%v", processed, err)
	}
	if err := pg.MarkEventProcessed(ctx, "evt-it-1", "product.review_created"); err != nil {
		t.Fatalf("MarkEventProcessed: %v", err)
	}
	if err := pg.MarkEventProcessed(ctx, "evt-it-1", "product.review_created"); err != nil {
		t.Fatalf("duplicate MarkEventProcessed must be idempotent: %v", err)
	}
	processed, err = pg.IsEventProcessed(ctx, "evt-it-1")
	if err != nil || !processed {
		t.Fatalf("expected evt-it-1 processed, got %v err=%v", processed, err)
	}
}

func TestRealRedisQuotaIntegration(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping real Redis integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	_ = rdb.FlushDB(ctx).Err()

	mgr := quota.NewRedisManager(rdb, quota.Config{
		DailyUserTokenLimit: 1000,
		DailyCostLimitVND:   5000,
		AbuseStrikeLimit:    3,
		AbuseLockoutWindow:  5 * time.Minute,
	})

	dec, err := mgr.CheckAndReserveTokens(ctx, "user-redis-1", 600)
	if err != nil || !dec.Allowed || dec.Remaining != 400 {
		t.Fatalf("first reserve failed: dec=%+v err=%v", dec, err)
	}
	dec, err = mgr.CheckAndReserveTokens(ctx, "user-redis-1", 500)
	if err != nil || dec.Allowed || dec.RetryAfterSec <= 0 {
		t.Fatalf("expected over-quota denial: dec=%+v err=%v", dec, err)
	}

	cdec, err := mgr.RecordCostVND(ctx, 5500)
	if err != nil || cdec.Allowed {
		t.Fatalf("expected cost cap exceeded: cdec=%+v err=%v", cdec, err)
	}

	if err := mgr.SetCache(ctx, "it:key", []byte("hello-cache"), time.Minute); err != nil {
		t.Fatalf("SetCache: %v", err)
	}
	val, hit, err := mgr.GetCache(ctx, "it:key")
	if err != nil || !hit || string(val) != "hello-cache" {
		t.Fatalf("GetCache: val=%q hit=%v err=%v", string(val), hit, err)
	}

	for i := int64(1); i <= 2; i++ {
		ab, err := mgr.RecordAbuseStrike(ctx, "bad-actor")
		if err != nil || ab.Locked || ab.Strikes != i {
			t.Fatalf("strike %d unexpected: ab=%+v err=%v", i, ab, err)
		}
	}
	ab, err := mgr.RecordAbuseStrike(ctx, "bad-actor")
	if err != nil || !ab.Locked || ab.RetryAfterSec <= 0 {
		t.Fatalf("expected lockout on 3rd strike: ab=%+v err=%v", ab, err)
	}
}
