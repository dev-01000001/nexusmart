package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/search/internal/events"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPG_K1_ReindexEmbeddingsOnly_SkipsMeiliAndOnlyUpdatesExistingEmbeddings(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	t1 := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	t2 := t1.Add(30 * time.Second)

	// 1. Seed p-k1-miss with embedding = NULL, text_hash = '' (embedder fails on initial upsert).
	h.emb.fail.Store(true)
	if err := h.idx.HandleProductUpserted(ctx, "ev-k1-miss", indexer.ProductUpsertInput{
		ProductID: "p-k1-miss", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Tên Gốc Trong PG", Slug: "ten-goc-trong-pg",
		MinPriceVND: 150000, MaxPriceVND: 150000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t1,
	}); err != nil {
		t.Fatalf("seed p-k1-miss: %v", err)
	}
	// Seed p-k1-stale at t2 (newer than snapshot t1) with missing embedding.
	if err := h.idx.HandleProductUpserted(ctx, "ev-k1-stale", indexer.ProductUpsertInput{
		ProductID: "p-k1-stale", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Sản phẩm mới hơn snapshot", Slug: "sp-moi-hon-snapshot",
		MinPriceVND: 200000, MaxPriceVND: 200000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t2,
	}); err != nil {
		t.Fatalf("seed p-k1-stale: %v", err)
	}
	h.emb.fail.Store(false)

	// 2. Seed p-k1-fresh WITH valid embedding + matching text_hash.
	if err := h.idx.HandleProductUpserted(ctx, "ev-k1-fresh", indexer.ProductUpsertInput{
		ProductID: "p-k1-fresh", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo đã có embedding", Slug: "ao-da-co-embedding",
		MinPriceVND: 300000, MaxPriceVND: 300000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t1,
	}); err != nil {
		t.Fatalf("seed p-k1-fresh: %v", err)
	}

	// 3. Seed p-k1-tomb as deleted tombstone.
	if err := h.idx.HandleProductDeleted(ctx, "ev-k1-tomb", "p-k1-tomb", "shop-1", t1); err != nil {
		t.Fatalf("seed p-k1-tomb: %v", err)
	}

	// ProductService snapshot returns:
	// - p-k1-miss with DIFFERENT name/price at t1 (non-embedding columns must NOT be overwritten!)
	// - p-k1-fresh unchanged (must NOT be re-embedded!)
	// - p-k1-stale with older UpdatedAt t1 < DB t2 (must be skipped!)
	// - p-k1-tomb at newer t2 (must be skipped because row is tombstoned!)
	// - p-k1-not-in-pg (not present in product_embeddings; must NOT be inserted!)
	h.prodClient.products = []*productv1.IndexableProduct{
		{
			Id: "p-k1-miss", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Tên Snapshot Khác", Slug: "ten-snapshot-khac",
			MinPriceVnd: 999000, MaxPriceVnd: 999000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 5,
			UpdatedAt: timestamppb.New(t1),
		},
		{
			Id: "p-k1-fresh", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Áo đã có embedding", Slug: "ao-da-co-embedding",
			MinPriceVnd: 300000, MaxPriceVnd: 300000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 5,
			UpdatedAt: timestamppb.New(t1),
		},
		{
			Id: "p-k1-stale", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Sản phẩm cũ snapshot", Slug: "sp-cu-snapshot",
			MinPriceVnd: 200000, MaxPriceVnd: 200000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 5,
			UpdatedAt: timestamppb.New(t1),
		},
		{
			Id: "p-k1-tomb", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Tombstone", Slug: "tombstone",
			MinPriceVnd: 100000, MaxPriceVnd: 100000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 5,
			UpdatedAt: timestamppb.New(t2),
		},
		{
			Id: "p-k1-not-in-pg", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Chưa có trong PG", Slug: "chua-co-trong-pg",
			MinPriceVnd: 120000, MaxPriceVnd: 120000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 5,
			UpdatedAt: timestamppb.New(t1),
		},
	}

	// Break Meilisearch completely and zero counters before running embeddings-only reindex.
	h.meili.failUpsert.Store(true)
	h.meili.failDelete.Store(true)
	h.meili.upsertCalls.Store(0)
	h.meili.deleteCalls.Store(0)
	embCallsBefore := h.emb.calls.Load()

	adminCtx := userCtx("admin-k1", auth.RoleAdmin)
	resp, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts:   false,
		ReindexEmbeddings: true,
		ReindexCategories: false,
		ReindexShops:      false,
	})
	if err != nil {
		t.Fatalf("ReindexAll K1: %v", err)
	}

	var finalStatus *searchv1.GetReindexStatusResponse
	waitUntil(t, 5*time.Second, "K1 embeddings-only reindex terminal", func() bool {
		st, stErr := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: resp.JobId})
		if stErr != nil {
			return false
		}
		if st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED ||
			st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED {
			finalStatus = st
			return true
		}
		return false
	})

	if finalStatus.Status != searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED {
		t.Fatalf("K1: embeddings-only job with broken Meilisearch got status=%s err=%q, want COMPLETED",
			finalStatus.Status, finalStatus.ErrorMessage)
	}
	if finalStatus.ProductsIndexed != 0 {
		t.Fatalf("K1: products_indexed = %d, want 0", finalStatus.ProductsIndexed)
	}
	if finalStatus.EmbeddingsUpdated != 1 {
		t.Fatalf("K1: embeddings_updated = %d, want 1 (only p-k1-miss)", finalStatus.EmbeddingsUpdated)
	}
	if h.meili.upsertCalls.Load() != 0 || h.meili.deleteCalls.Load() != 0 {
		t.Fatalf("K1: Meilisearch must receive 0 calls in embeddings-only mode, got upsert=%d delete=%d",
			h.meili.upsertCalls.Load(), h.meili.deleteCalls.Load())
	}
	if embDelta := h.emb.calls.Load() - embCallsBefore; embDelta != 1 {
		t.Fatalf("K1: embedder called %d times, want 1 (only p-k1-miss)", embDelta)
	}

	// Verify p-k1-miss has embedding + text_hash updated, while name & min_price_vnd were NOT overwritten.
	var (
		missName    string
		missPrice   int64
		missHash    string
		missHasEmb  bool
		staleHasEmb bool
		tombDeleted bool
		tombHasEmb  bool
		newExists   bool
	)
	if err := h.st.Pool().QueryRow(ctx,
		`SELECT name, min_price_vnd, text_hash, (embedding IS NOT NULL) FROM product_embeddings WHERE product_id = 'p-k1-miss'`,
	).Scan(&missName, &missPrice, &missHash, &missHasEmb); err != nil {
		t.Fatalf("query p-k1-miss: %v", err)
	}
	if !missHasEmb || missHash == "" {
		t.Fatalf("K1: p-k1-miss must have embedding + text_hash populated, got hasEmb=%v hash=%q", missHasEmb, missHash)
	}
	if missName != "Tên Gốc Trong PG" || missPrice != 150000 {
		t.Fatalf("K1: embeddings-only mode overwrote non-embedding columns: name=%q price=%d", missName, missPrice)
	}

	_ = h.st.Pool().QueryRow(ctx, `SELECT (embedding IS NOT NULL) FROM product_embeddings WHERE product_id = 'p-k1-stale'`).Scan(&staleHasEmb)
	if staleHasEmb {
		t.Fatalf("K1: p-k1-stale (DB newer than snapshot) must NOT be overwritten")
	}

	_ = h.st.Pool().QueryRow(ctx, `SELECT deleted, (embedding IS NOT NULL) FROM product_embeddings WHERE product_id = 'p-k1-tomb'`).Scan(&tombDeleted, &tombHasEmb)
	if !tombDeleted || tombHasEmb {
		t.Fatalf("K1: tombstoned row must stay deleted without embedding (deleted=%v hasEmb=%v)", tombDeleted, tombHasEmb)
	}

	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_embeddings WHERE product_id = 'p-k1-not-in-pg')`).Scan(&newExists)
	if newExists {
		t.Fatalf("K1: product absent from product_embeddings must NOT be inserted by embeddings-only mode")
	}

	// Verify S3 invariant in embeddings-only mode: if embedder fails when text_hash changes,
	// text_hash must NOT advance; once embedder recovers, embedding & text_hash update together.
	var freshHashBefore string
	_ = h.st.Pool().QueryRow(ctx, `SELECT text_hash FROM product_embeddings WHERE product_id = 'p-k1-fresh'`).Scan(&freshHashBefore)
	h.prodClient.products[1].Name = "Áo đã đổi mô tả hoàn toàn"
	h.prodClient.products[1].UpdatedAt = timestamppb.New(t2)

	h.emb.fail.Store(true)
	respFail, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts:   false,
		ReindexEmbeddings: true,
	})
	if err != nil {
		t.Fatalf("ReindexAll K1 embedder fail: %v", err)
	}
	var failStatus *searchv1.GetReindexStatusResponse
	waitUntil(t, 5*time.Second, "K1 embedder fail terminal", func() bool {
		st, stErr := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: respFail.JobId})
		if stErr == nil && (st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED ||
			st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED) {
			failStatus = st
			return true
		}
		return false
	})
	if failStatus.Status != searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED {
		t.Fatalf("K1 S3: expected FAILED when embedder errors, got %s", failStatus.Status)
	}
	var freshHashAfterFail string
	_ = h.st.Pool().QueryRow(ctx, `SELECT text_hash FROM product_embeddings WHERE product_id = 'p-k1-fresh'`).Scan(&freshHashAfterFail)
	if freshHashAfterFail != freshHashBefore {
		t.Fatalf("K1 S3: text_hash changed on embedder failure (%q -> %q)", freshHashBefore, freshHashAfterFail)
	}

	h.emb.fail.Store(false)
	respRecover, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts:   false,
		ReindexEmbeddings: true,
	})
	if err != nil {
		t.Fatalf("ReindexAll K1 recover: %v", err)
	}
	var recStatus *searchv1.GetReindexStatusResponse
	waitUntil(t, 5*time.Second, "K1 recover terminal", func() bool {
		st, stErr := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: respRecover.JobId})
		if stErr == nil && (st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED ||
			st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED) {
			recStatus = st
			return true
		}
		return false
	})
	if recStatus.Status != searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED || recStatus.ProductsIndexed != 0 || recStatus.EmbeddingsUpdated != 1 {
		t.Fatalf("K1 recover: status=%s indexed=%d embedded=%d err=%q", recStatus.Status, recStatus.ProductsIndexed, recStatus.EmbeddingsUpdated, recStatus.ErrorMessage)
	}
	var freshHashAfterRec, freshNameAfterRec string
	_ = h.st.Pool().QueryRow(ctx, `SELECT text_hash, name FROM product_embeddings WHERE product_id = 'p-k1-fresh'`).Scan(&freshHashAfterRec, &freshNameAfterRec)
	if freshHashAfterRec == freshHashBefore || freshNameAfterRec != "Áo đã có embedding" {
		t.Fatalf("K1 recover: want new text_hash + unchanged name, got hash=%q name=%q", freshHashAfterRec, freshNameAfterRec)
	}
	if h.meili.upsertCalls.Load() != 0 || h.meili.deleteCalls.Load() != 0 {
		t.Fatalf("K1 recover: Meili must still have 0 calls, got upsert=%d delete=%d", h.meili.upsertCalls.Load(), h.meili.deleteCalls.Load())
	}
}

type noopEventHandler struct{}

func (noopEventHandler) HandleProductUpserted(context.Context, string, indexer.ProductUpsertInput) error {
	return nil
}
func (noopEventHandler) HandleProductDeleted(context.Context, string, string, string, time.Time) error {
	return nil
}
func (noopEventHandler) HandleCategoryUpdated(context.Context, string, indexer.CategoryUpdateInput) error {
	return nil
}
func (noopEventHandler) HandleShopReviewed(context.Context, string, indexer.ShopReviewedInput) error {
	return nil
}
func (noopEventHandler) HandleShopStatusChanged(context.Context, string, indexer.ShopStatusChangedInput) error {
	return nil
}

func TestPG_K2_NATSConsumerAppliesMaxAckPending4AndAckWait120s(t *testing.T) {
	natsURL := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if natsURL == "" {
		t.Skip("TEST_NATS_URL not set")
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	if err := jetstream.EnsureStreams(js, jetstream.DefaultPrimaryStream, jetstream.DefaultDLQStream); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}

	suffix := uuid.NewString()[:8]
	prodDurable := "search-k2-prod-" + suffix
	userDurable := "search-k2-user-" + suffix
	consumer := events.NewConsumer(js, js, nil, noopEventHandler{}, nil, jetstream.DefaultPrimaryStream)
	consumer.SetSubscriptions([]events.SubscriptionSpec{
		{Subject: "product.>", Durable: prodDurable, Queue: prodDurable},
		{Subject: "user.>", Durable: userDurable, Queue: userDurable},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start: %v", err)
	}
	defer func() {
		_ = consumer.Stop()
		_ = js.DeleteConsumer(jetstream.DefaultPrimaryStream, prodDurable)
		_ = js.DeleteConsumer(jetstream.DefaultPrimaryStream, userDurable)
	}()

	for _, durable := range []string{prodDurable, userDurable} {
		info, err := js.ConsumerInfo(jetstream.DefaultPrimaryStream, durable)
		if err != nil {
			t.Fatalf("ConsumerInfo(%s): %v", durable, err)
		}
		if info.Config.MaxAckPending != 4 {
			t.Fatalf("K2 durable %s: MaxAckPending = %d, want 4", durable, info.Config.MaxAckPending)
		}
		if info.Config.AckWait != 120*time.Second {
			t.Fatalf("K2 durable %s: AckWait = %v, want 120s", durable, info.Config.AckWait)
		}
	}
}

func TestPG_K3_HangingMeiliTaskRollsBackEventAndRetriesCleanly(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	var (
		hangDocTasks atomic.Bool
		upsertsSeen  atomic.Int32
		deletesSeen  atomic.Int32
		nextUID      atomic.Int64
	)
	hangDocTasks.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"available"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":"products","status":"enqueued","type":"indexCreation"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/indexes/products/settings":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"taskUid":2,"indexUid":"products","status":"enqueued","type":"settingsUpdate"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes/products/documents":
			upsertsSeen.Add(1)
			uid := 100 + nextUID.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"documentAdditionOrUpdate"}`, uid)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/indexes/products/documents/"):
			deletesSeen.Add(1)
			uid := 100 + nextUID.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"documentDeletion"}`, uid)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			if r.URL.Path == "/tasks/1" || r.URL.Path == "/tasks/2" || !hangDocTasks.Load() {
				_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"documentAdditionOrUpdate"}`))
				return
			}
			_, _ = w.Write([]byte(`{"uid":101,"indexUid":"products","status":"processing","type":"documentAdditionOrUpdate"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	realMeili := meili.NewClient(meili.Options{URL: srv.URL, Timeout: 500 * time.Millisecond})
	if err := realMeili.EnsureConfigured(ctx); err != nil {
		t.Fatalf("EnsureConfigured: %v", err)
	}
	idx := indexer.New(ctx, h.st, realMeili, h.emb, h.prodClient, h.userClient, nil, nil)
	defer idx.Stop()

	// 1. Upsert with hanging Meili task (while parent DB TX context stays alive!) ->
	// waitTask MUST return DeadlineExceeded so DB TX rolls back processed_events.
	tCtx := meili.WithTaskWaitTimeout(ctx, 150*time.Millisecond)
	err := idx.HandleProductUpserted(tCtx, "ev-k3-up", indexer.ProductUpsertInput{
		ProductID: "p-k3", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo K3", Slug: "ao-k3",
		MinPriceVND: 150000, MaxPriceVND: 150000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("K3 upsert hanging task: got err=%v, want DeadlineExceeded", err)
	}
	var processed bool
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-k3-up')`).Scan(&processed)
	if processed {
		t.Fatalf("K3: processed_events must NOT commit when Meili upsert task times out")
	}

	// 2. Redelivery after task stops hanging -> succeeds and commits processed_events!
	hangDocTasks.Store(false)
	if err := idx.HandleProductUpserted(ctx, "ev-k3-up", indexer.ProductUpsertInput{
		ProductID: "p-k3", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo K3", Slug: "ao-k3",
		MinPriceVND: 150000, MaxPriceVND: 150000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("K3 upsert redelivery failed: %v", err)
	}
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-k3-up')`).Scan(&processed)
	if !processed || upsertsSeen.Load() != 2 {
		t.Fatalf("K3 upsert redelivery: processed=%v upsertsSeen=%d", processed, upsertsSeen.Load())
	}

	// 3. Delete with hanging Meili task (while parent DB TX context stays alive!) ->
	// waitTask MUST return DeadlineExceeded and roll back processed_events.
	hangDocTasks.Store(true)
	tCtx2 := meili.WithTaskWaitTimeout(ctx, 150*time.Millisecond)
	err = idx.HandleProductDeleted(tCtx2, "ev-k3-del", "p-k3", "shop-1", now.Add(time.Second))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("K3 delete hanging task: got err=%v, want DeadlineExceeded", err)
	}
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-k3-del')`).Scan(&processed)
	if processed {
		t.Fatalf("K3: processed_events must NOT commit when Meili delete task times out")
	}

	// 4. Redelivery of delete after recovery -> succeeds!
	hangDocTasks.Store(false)
	if err := idx.HandleProductDeleted(ctx, "ev-k3-del", "p-k3", "shop-1", now.Add(time.Second)); err != nil {
		t.Fatalf("K3 delete redelivery failed: %v", err)
	}
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-k3-del')`).Scan(&processed)
	if !processed || deletesSeen.Load() != 2 {
		t.Fatalf("K3 delete redelivery: processed=%v deletesSeen=%d", processed, deletesSeen.Load())
	}
}

func TestPG_K3_SearchDegradesCleanlyWhileSettingsStillPending(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed one live embedded product into PG (via harness fake Meili first).
	if err := h.idx.HandleProductUpserted(ctx, "ev-k3-deg-seed", indexer.ProductUpsertInput{
		ProductID: "p-k3-deg", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo khoác chạy bộ mùa đông", Slug: "ao-khoac-chay-bo",
		MinPriceVND: 450000, MaxPriceVND: 450000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	var (
		settingsCalls atomic.Int32
		searchCalls   atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"available"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":"products","status":"enqueued","type":"indexCreation"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/indexes/products/settings":
			settingsCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"taskUid":2,"indexUid":"products","status":"enqueued","type":"settingsUpdate"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/1":
			_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"indexCreation"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/2":
			// Settings still processing!
			_, _ = w.Write([]byte(`{"uid":2,"indexUid":"products","status":"processing","type":"settingsUpdate"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes/products/search":
			searchCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": "attribute status not filterable",
				"code":    "invalid_search_filter",
				"type":    "invalid_request",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pendingMeili := meili.NewClient(meili.Options{URL: srv.URL, Timeout: 150 * time.Millisecond})
	svc := service.New(h.st, h.trending, pendingMeili, h.emb, h.idx, service.Options{
		MeiliTimeout:  150 * time.Millisecond,
		VectorTimeout: 500 * time.Millisecond,
	})

	// 1. SearchProducts -> returns Unavailable (gateway degrades to product_fallback), NOT Internal (500).
	_, err := svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "áo khoác"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("K3 SearchProducts while settings pending: got code=%s err=%v, want Unavailable", status.Code(err), err)
	}

	// 2. HybridSearch -> degrades cleanly to vector_only with PG vector hit!
	hRes, err := svc.HybridSearch(userCtx("buyer-k3", auth.RoleBuyer), &searchv1.HybridSearchRequest{
		Query: "áo khoác chạy bộ mùa đông",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("K3 HybridSearch while settings pending failed: %v", err)
	}
	if hRes.DegradedMode != "vector_only" || len(hRes.Hits) == 0 {
		t.Fatalf("K3 HybridSearch degraded=%q hits=%d, want vector_only + >=1 hit", hRes.DegradedMode, len(hRes.Hits))
	}

	// 3. Suggest -> degrades cleanly to PG fallback products!
	sRes, err := svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao khoac"})
	if err != nil {
		t.Fatalf("K3 Suggest while settings pending failed: %v", err)
	}
	if len(sRes.Products) == 0 {
		t.Fatalf("K3 Suggest expected PG fallback product while Meili settings pending")
	}

	if settingsCalls.Load() != 1 {
		t.Fatalf("K3: PATCH /settings called %d times across Search/Hybrid/Suggest, want 1", settingsCalls.Load())
	}
	if searchCalls.Load() != 0 {
		t.Fatalf("K3: POST /search must not be called while settings task is still pending, got %d", searchCalls.Load())
	}
}
