package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/search/internal/embed"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/store"
)

type fakeEngine struct {
	mu      sync.Mutex
	docs    map[string]meili.Document
	deleted []string
	fail    error
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{docs: make(map[string]meili.Document)}
}

func (f *fakeEngine) UpsertDocuments(_ context.Context, docs []meili.Document) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return -1, f.fail
	}
	for _, d := range docs {
		f.docs[d.ID] = d
	}
	return 1, nil
}

func (f *fakeEngine) DeleteDocument(_ context.Context, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return -1, f.fail
	}
	delete(f.docs, id)
	f.deleted = append(f.deleted, id)
	return 2, nil
}

func (f *fakeEngine) WaitTask(_ context.Context, _ int64) error {
	return nil
}

type fakeFetcher struct {
	mu         sync.Mutex
	products   map[string]*productv1.Product
	categories []*productv1.Category
	getCalls   int
	getErr     error
}

func (f *fakeFetcher) GetProduct(_ context.Context, productID string) (*productv1.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	p, ok := f.products[productID]
	if !ok {
		return nil, ErrProductNotFound
	}
	return p, nil
}

func (f *fakeFetcher) ListProducts(_ context.Context, page, pageSize int32) ([]*productv1.Product, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := make([]*productv1.Product, 0, len(f.products))
	for _, p := range f.products {
		all = append(all, p)
	}
	start := int((page - 1) * pageSize)
	if start >= len(all) {
		return nil, int64(len(all)), nil
	}
	end := start + int(pageSize)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], int64(len(all)), nil
}

func (f *fakeFetcher) ListCategories(_ context.Context) ([]*productv1.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.categories, nil
}

func TestIndexerLifecycleVersionGuardAndIdempotency(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	eng := newFakeEngine()
	emb := embed.NewMockEmbedder()
	now := time.Now().UTC()

	fetcher := &fakeFetcher{
		categories: []*productv1.Category{
			{Id: "cat-1", Slug: "ao-thun", Name: "Áo thun"},
		},
		products: map[string]*productv1.Product{
			"p1": {
				Id:          "p1",
				ShopId:      "shop-1",
				CategoryId:  "cat-1",
				Name:        "Áo thun thể thao đẹp",
				Description: "Chất liệu thoáng khí",
				Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MinPriceVnd: 199000,
				MaxPriceVnd: 249000,
				Skus: []*productv1.SKU{
					{Id: "s1", SkuCode: "AT-001", Name: "Đen M", PriceVnd: 199000, Stock: 25, AvailableStock: 25},
				},
				CreatedAt: timestamppb.New(now),
				UpdatedAt: timestamppb.New(now),
			},
		},
	}

	idx := New(st, eng, emb, fetcher, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. product.updated at version 10 indexes product into Store & Meili.
	p1Payload, _ := json.Marshal(ProductEventPayload{ProductID: "p1", ShopID: "shop-1", Version: 10})
	dup, err := idx.ProcessEvent(ctx, SubjectProductUpdated, Envelope{
		EventID:   "evt-1",
		EventType: SubjectProductUpdated,
		Payload:   p1Payload,
	})
	if err != nil || dup {
		t.Fatalf("ProcessEvent 1st = (%v, %v), want (false, nil)", dup, err)
	}
	doc, err := st.GetDocument(ctx, "p1")
	if err != nil {
		t.Fatalf("expected p1 in store: %v", err)
	}
	if doc.CategorySlug != "ao-thun" || doc.TotalStock != 25 || !doc.InStock || doc.Version != 10 {
		t.Fatalf("unexpected store doc: %+v", doc)
	}
	if eng.docs["p1"].NameFolded != "ao thun the thao dep" || eng.docs["p1"].PriceBucket != "0-200000" {
		t.Fatalf("unexpected meili doc: %+v", eng.docs["p1"])
	}

	// 2. Duplicate event_id is skipped idempotently.
	dup, err = idx.ProcessEvent(ctx, SubjectProductUpdated, Envelope{
		EventID:   "evt-1",
		EventType: SubjectProductUpdated,
		Payload:   p1Payload,
	})
	if err != nil || !dup {
		t.Fatalf("ProcessEvent duplicate = (%v, %v), want (true, nil)", dup, err)
	}
	if fetcher.getCalls != 1 {
		t.Fatalf("expected fetcher not called on duplicate event, got %d calls", fetcher.getCalls)
	}

	// 3. Stale event (version 5 < stored 10) is skipped without overwriting.
	stalePayload, _ := json.Marshal(ProductEventPayload{ProductID: "p1", ShopID: "shop-1", Version: 5})
	dup, err = idx.ProcessEvent(ctx, SubjectProductUpdated, Envelope{
		EventID:   "evt-stale",
		EventType: SubjectProductUpdated,
		Payload:   stalePayload,
	})
	if err != nil || dup {
		t.Fatalf("ProcessEvent stale = (%v, %v), want (false, nil)", dup, err)
	}
	if fetcher.getCalls != 1 {
		t.Fatalf("expected fetcher skipped for stale version, got %d calls", fetcher.getCalls)
	}

	// 4. product.deleted removes from both Store & Meili.
	delPayload, _ := json.Marshal(ProductEventPayload{ProductID: "p1", ShopID: "shop-1", Version: 11})
	if _, err := idx.ProcessEvent(ctx, SubjectProductDeleted, Envelope{
		EventID:   "evt-del",
		EventType: SubjectProductDeleted,
		Payload:   delPayload,
	}); err != nil {
		t.Fatalf("ProcessEvent deleted: %v", err)
	}
	if _, err := st.GetDocument(ctx, "p1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected p1 deleted from store, got err=%v", err)
	}
	if _, exists := eng.docs["p1"]; exists {
		t.Fatal("expected p1 deleted from meili")
	}

	// 5. Malformed payload returns ErrMalformedPayload.
	if _, err := idx.ProcessEvent(ctx, SubjectProductCreated, Envelope{
		EventID: "evt-bad",
		Payload: json.RawMessage(`{"product_id":""}`),
	}); !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("expected ErrMalformedPayload, got %v", err)
	}
}

func TestIndexerDeletesHiddenOrNotFoundProduct(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	eng := newFakeEngine()
	emb := embed.NewMockEmbedder()

	fetcher := &fakeFetcher{
		products: map[string]*productv1.Product{
			"p-pending": {
				Id:     "p-pending",
				ShopId: "shop-1",
				Name:   "Pending product",
				Status: productv1.ProductStatus_PRODUCT_STATUS_PENDING,
			},
		},
	}

	idx := New(st, eng, emb, fetcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Pre-populate to prove it gets evicted.
	_, _ = st.UpsertDocument(ctx, store.ProductDocument{ProductID: "p-pending", Embedding: make([]float32, 256)})
	eng.docs["p-pending"] = meili.Document{ID: "p-pending"}

	payload, _ := json.Marshal(ProductEventPayload{ProductID: "p-pending", Version: 2})
	if _, err := idx.ProcessEvent(ctx, SubjectProductUpdated, Envelope{
		EventID: "evt-hide",
		Payload: payload,
	}); err != nil {
		t.Fatalf("ProcessEvent pending: %v", err)
	}
	if _, err := st.GetDocument(ctx, "p-pending"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected pending product evicted from store, got err=%v", err)
	}
	if _, ok := eng.docs["p-pending"]; ok {
		t.Fatal("expected pending product evicted from meili")
	}
}

func TestSyncAllFromProduct(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	eng := newFakeEngine()
	emb := embed.NewMockEmbedder()

	fetcher := &fakeFetcher{
		categories: []*productv1.Category{
			{Id: "c1", Slug: "giay-dep", Name: "Giày dép"},
		},
		products: map[string]*productv1.Product{
			"p1": {Id: "p1", ShopId: "s1", CategoryId: "c1", Name: "Giày chạy bộ", Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, MinPriceVnd: 550000},
			"p2": {Id: "p2", ShopId: "s1", CategoryId: "c1", Name: "Giày ẩn", Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN, MinPriceVnd: 300000},
		},
	}

	idx := New(st, eng, emb, fetcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n, err := idx.SyncAllFromProduct(ctx)
	if err != nil {
		t.Fatalf("SyncAllFromProduct: %v", err)
	}
	if n != 1 {
		t.Fatalf("indexed = %d, want 1", n)
	}
	labels := idx.CategoryLabels()
	if labels["giay-dep"] != "Giày dép" {
		t.Fatalf("expected cached category label, got %v", labels)
	}
}
