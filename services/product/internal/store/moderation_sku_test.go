package store

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore_ListModerationSKUsByProductIDs(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC()

	if _, err := m.CreateCategory(ctx, Category{ID: "cat-1", Name: "Cat", Slug: "cat", CommissionRateBps: 500}); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := m.CreateProduct(ctx, Product{ID: "p-1", ShopID: "shop-1", CategoryID: "cat-1", Name: "P1", Slug: "p1", Status: ProductStatusApproved}); err != nil {
		t.Fatalf("CreateProduct p-1: %v", err)
	}
	if _, err := m.CreateProduct(ctx, Product{ID: "p-2", ShopID: "shop-1", CategoryID: "cat-1", Name: "P2", Slug: "p2", Status: ProductStatusApproved}); err != nil {
		t.Fatalf("CreateProduct p-2: %v", err)
	}

	if _, err := m.CreateSKU(ctx, SKU{
		ID: "s-1", ProductID: "p-1", SKUCode: "S1", Name: "Active SKU",
		Attributes: map[string]string{"color": "Đen"}, Status: SKUStatusActive, CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSKU s-1: %v", err)
	}
	if _, err := m.CreateSKU(ctx, SKU{
		ID: "s-2", ProductID: "p-1", SKUCode: "S2", Name: "Inactive SKU",
		Attributes: map[string]string{"color": "Trắng"}, Status: SKUStatusInactive, CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateSKU s-2: %v", err)
	}
	if _, err := m.CreateSKU(ctx, SKU{
		ID: "s-3", ProductID: "p-2", SKUCode: "S3", Name: "Other Product SKU",
		Attributes: map[string]string{"size": "M"}, Status: SKUStatusOutOfStock, CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("CreateSKU s-3: %v", err)
	}

	empty, err := m.ListModerationSKUsByProductIDs(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input: got (%+v, %v), want empty", empty, err)
	}

	got, err := m.ListModerationSKUsByProductIDs(ctx, []string{"p-1"})
	if err != nil {
		t.Fatalf("ListModerationSKUsByProductIDs: %v", err)
	}
	if len(got) != 1 || got[0].ProductID != "p-1" || got[0].Name != "Active SKU" || got[0].Attributes["color"] != "Đen" {
		t.Fatalf("unexpected moderation SKUs for p-1: %+v", got)
	}
	// Mutating returned attributes must not mutate store state.
	got[0].Attributes["color"] = "MUTATED"
	again, _ := m.ListModerationSKUsByProductIDs(ctx, []string{"p-1"})
	if again[0].Attributes["color"] != "Đen" {
		t.Fatalf("returned Attributes map aliased store state: %+v", again[0].Attributes)
	}

	// Verify memTx reflects staged creates and staged deactivations.
	err = m.WithTx(ctx, func(q Queries) error {
		if dErr := q.DeactivateSKU(ctx, "s-1"); dErr != nil {
			return dErr
		}
		if _, cErr := q.CreateSKU(ctx, SKU{
			ID: "s-4", ProductID: "p-1", SKUCode: "S4", Name: "Staged Active SKU",
			Attributes: map[string]string{"size": "XL"}, Status: SKUStatusActive, CreatedAt: now.Add(3 * time.Second),
		}); cErr != nil {
			return cErr
		}
		txGot, lErr := q.ListModerationSKUsByProductIDs(ctx, []string{"p-1", "p-2"})
		if lErr != nil {
			return lErr
		}
		if len(txGot) != 2 {
			t.Fatalf("memTx expected 2 active moderation SKUs (s-3, s-4), got %+v", txGot)
		}
		if txGot[0].Name != "Other Product SKU" || txGot[1].Name != "Staged Active SKU" {
			t.Fatalf("memTx unexpected order/content: %+v", txGot)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
}
