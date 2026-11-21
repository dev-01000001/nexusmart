package store

import (
	"context"
	"math"
	"testing"
)

func TestFormatAndParseVector(t *testing.T) {
	in := []float32{0.5, -0.25, 0.0, 1.0}
	formatted := FormatVector(in)
	if formatted != "[0.500000,-0.250000,0.000000,1.000000]" {
		t.Fatalf("FormatVector() = %q", formatted)
	}
	parsed, err := ParseVector(formatted)
	if err != nil {
		t.Fatalf("ParseVector(): %v", err)
	}
	if len(parsed) != len(in) {
		t.Fatalf("len(parsed) = %d, want %d", len(parsed), len(in))
	}
	for i := range in {
		if math.Abs(float64(parsed[i]-in[i])) > 1e-5 {
			t.Fatalf("parsed[%d] = %v, want %v", i, parsed[i], in[i])
		}
	}
}

func TestMemoryStoreVersionGuardAndVectorSearch(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	applied, err := st.UpsertDocument(ctx, ProductDocument{
		ProductID:    "p1",
		ShopID:       "shop-1",
		CategoryID:   "cat-1",
		CategorySlug: "ao-thun",
		Name:         "Áo thun v10",
		MinPriceVND:  150000,
		MaxPriceVND:  150000,
		InStock:      true,
		Version:      10,
		Embedding:    []float32{1, 0, 0},
	})
	if err != nil || !applied {
		t.Fatalf("initial upsert failed: applied=%v err=%v", applied, err)
	}

	// Older version must be rejected.
	applied, err = st.UpsertDocument(ctx, ProductDocument{
		ProductID: "p1",
		Name:      "Stale v5",
		Version:   5,
		Embedding: []float32{0, 1, 0},
	})
	if err != nil || applied {
		t.Fatalf("expected stale version to be skipped: applied=%v err=%v", applied, err)
	}

	doc, err := st.GetDocument(ctx, "p1")
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if doc.Name != "Áo thun v10" || doc.Version != 10 {
		t.Fatalf("unexpected doc after stale upsert: %+v", doc)
	}

	// Add more docs to verify filtering and cosine ranking.
	if err := st.UpsertDocumentsBatch(ctx, []ProductDocument{
		{
			ProductID:    "p2",
			ShopID:       "shop-1",
			CategoryID:   "cat-1",
			CategorySlug: "ao-thun",
			Name:         "Áo thun chạy bộ",
			MinPriceVND:  250000,
			MaxPriceVND:  250000,
			InStock:      true,
			Version:      11,
			Embedding:    []float32{0.9, 0.43589, 0},
		},
		{
			ProductID:    "p3",
			ShopID:       "shop-2",
			CategoryID:   "cat-2",
			CategorySlug: "tai-nghe",
			Name:         "Tai nghe",
			MinPriceVND:  500000,
			MaxPriceVND:  500000,
			InStock:      false,
			Version:      12,
			Embedding:    []float32{0, 0, 1},
		},
	}); err != nil {
		t.Fatalf("UpsertDocumentsBatch: %v", err)
	}

	hits, err := st.VectorSearch(ctx, []float32{1, 0, 0}, VectorSearchFilter{
		ShopID:           "shop-1",
		Category:         "ao-thun",
		MinPriceVND:      100000,
		MaxPriceVND:      300000,
		InStockOnly:      true,
		ExcludeProductID: "p1",
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(hits) != 1 || hits[0].Doc.ProductID != "p2" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	if hits[0].Score < 0.85 {
		t.Fatalf("expected high cosine score, got %v", hits[0].Score)
	}

	// Idempotent event marking.
	first, err := st.TryMarkEventProcessed(ctx, "evt-1", "product.created")
	if err != nil || !first {
		t.Fatalf("TryMarkEventProcessed 1st = (%v, %v), want (true, nil)", first, err)
	}
	second, err := st.TryMarkEventProcessed(ctx, "evt-1", "product.created")
	if err != nil || second {
		t.Fatalf("TryMarkEventProcessed 2nd = (%v, %v), want (false, nil)", second, err)
	}
}
