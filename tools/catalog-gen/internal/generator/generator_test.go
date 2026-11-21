package generator

import (
	"strings"
	"testing"
)

func TestGenerate20000ProductsDeterminismQualityAndDistribution(t *testing.T) {
	shops := DefaultShops()
	cats := DefaultCategories()

	g1 := New(DefaultSeed, shops, cats)
	g2 := New(DefaultSeed, shops, cats)
	gDiff := New(DefaultSeed+1, shops, cats)

	prods1 := g1.Generate(DefaultCount)
	prods2 := g2.Generate(DefaultCount)
	prodsDiff := gDiff.Generate(10)

	if len(prods1) != DefaultCount {
		t.Fatalf("expected %d products, got %d", DefaultCount, len(prods1))
	}

	// Deterministic check across full 20,000 items.
	for i := 0; i < len(prods1); i++ {
		if prods1[i].Name != prods2[i].Name ||
			prods1[i].Description != prods2[i].Description ||
			prods1[i].ShopID != prods2[i].ShopID ||
			prods1[i].CategoryID != prods2[i].CategoryID ||
			len(prods1[i].SKUs) != len(prods2[i].SKUs) {
			t.Fatalf("non-deterministic product at index %d", i)
		}
		for j := range prods1[i].SKUs {
			if prods1[i].SKUs[j].SKUCode != prods2[i].SKUs[j].SKUCode ||
				prods1[i].SKUs[j].PriceVND != prods2[i].SKUs[j].PriceVND ||
				prods1[i].SKUs[j].Stock != prods2[i].SKUs[j].Stock {
				t.Fatalf("non-deterministic SKU at product %d sku %d", i, j)
			}
		}
	}

	// Different seed must produce different output.
	sameCount := 0
	for i := 0; i < len(prodsDiff); i++ {
		if prodsDiff[i].Name == prods1[i].Name {
			sameCount++
		}
	}
	if sameCount == len(prodsDiff) {
		t.Fatal("different seed produced identical product names")
	}

	globalSKUCodes := make(map[string]struct{}, DefaultCount*2)
	shopCounts := make(map[string]int, len(shops))
	catCounts := make(map[string]int, len(cats))
	shopCatCounts := make(map[string]int, len(shops)*len(cats))

	var (
		outOfStockProds int
		diacriticsCount int
		dStrokeCount    int
	)
	requiredPhrases := map[string]int{
		"Đồ lót":                  0,
		"Áo thun thể thao đẹp":    0,
		"Quần đùi chạy bộ":        0,
		"Đèn bàn học":             0,
		"Áo khoác gió chống nước": 0,
		"Tai nghe không dây":      0,
	}

	for i, p := range prods1 {
		if strings.TrimSpace(p.Name) == "" {
			t.Fatalf("product %d has empty name", i)
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Fatalf("product %d has empty description", i)
		}
		if len(p.Images) == 0 {
			t.Fatalf("product %d has no images", i)
		}
		if len(p.SKUs) < 1 || len(p.SKUs) > 3 {
			t.Fatalf("product %d has %d SKUs, want 1..3", i, len(p.SKUs))
		}

		shopCounts[p.ShopID]++
		catCounts[p.CategoryID]++
		shopCatCounts[p.ShopID+"|"+p.CategoryID]++

		if hasVietnameseDiacritics(p.Name) {
			diacriticsCount++
		}
		if strings.ContainsAny(p.Name+" "+p.Description, "đĐ") {
			dStrokeCount++
		}
		for phrase := range requiredPhrases {
			if strings.Contains(p.Name, phrase) {
				requiredPhrases[phrase]++
			}
		}

		prodSKUs := make(map[string]struct{}, len(p.SKUs))
		allZero := true
		for j, sk := range p.SKUs {
			if sk.SKUCode == "" {
				t.Fatalf("product %d sku %d empty sku_code", i, j)
			}
			if _, dup := prodSKUs[sk.SKUCode]; dup {
				t.Fatalf("product %d duplicate sku_code %q within product", i, sk.SKUCode)
			}
			prodSKUs[sk.SKUCode] = struct{}{}
			if _, dup := globalSKUCodes[sk.SKUCode]; dup {
				t.Fatalf("product %d duplicate global sku_code %q", i, sk.SKUCode)
			}
			globalSKUCodes[sk.SKUCode] = struct{}{}

			if sk.PriceVND < MinPriceVND || sk.PriceVND > MaxPriceVND {
				t.Fatalf("product %d sku %d price %d out of range [%d,%d]", i, j, sk.PriceVND, MinPriceVND, MaxPriceVND)
			}
			if sk.PriceVND%PriceStep != 0 {
				t.Fatalf("product %d sku %d price %d not rounded to %d VND", i, j, sk.PriceVND, PriceStep)
			}
			if sk.Stock < 0 || sk.Stock > MaxStock {
				t.Fatalf("product %d sku %d stock %d out of range [0,%d]", i, j, sk.Stock, MaxStock)
			}
			if sk.Stock > 0 {
				allZero = false
			}
			if sk.WeightGrams <= 0 {
				t.Fatalf("product %d sku %d weight_grams=%d must be > 0", i, j, sk.WeightGrams)
			}
			if sk.Attributes["color"] == "" || sk.Attributes["size"] == "" {
				t.Fatalf("product %d sku %d missing color/size attributes: %+v", i, j, sk.Attributes)
			}
			if sk.ImageURL == "" {
				t.Fatalf("product %d sku %d empty image_url", i, j)
			}
		}
		if allZero {
			outOfStockProds++
		}
	}

	// Out-of-stock ratio should be roughly ~5% (allow 3%..7% margin).
	if outOfStockProds < 600 || outOfStockProds > 1400 {
		t.Fatalf("expected ~5%% out-of-stock products (~1000), got %d", outOfStockProds)
	}

	// Vietnamese diacritics and đ/Đ presence.
	if diacriticsCount != DefaultCount {
		t.Fatalf("expected all %d products to contain Vietnamese diacritics, got %d", DefaultCount, diacriticsCount)
	}
	if dStrokeCount < DefaultCount/2 {
		t.Fatalf("expected rich đ/Đ vocabulary (>50%% of products), got %d/%d", dStrokeCount, DefaultCount)
	}
	for phrase, cnt := range requiredPhrases {
		if cnt == 0 {
			t.Fatalf("expected Vietnamese search phrase %q to appear in generated catalog, got 0", phrase)
		}
	}

	// Balanced distribution across shops and categories.
	expectedPerShop := DefaultCount / len(shops)
	for _, s := range shops {
		if shopCounts[s.ID] != expectedPerShop {
			t.Fatalf("shop %s got %d products, expected %d", s.ID, shopCounts[s.ID], expectedPerShop)
		}
	}
	minCat, maxCat := DefaultCount, 0
	for _, c := range cats {
		cnt := catCounts[c.ID]
		if cnt < minCat {
			minCat = cnt
		}
		if cnt > maxCat {
			maxCat = cnt
		}
	}
	if maxCat-minCat > 1 {
		t.Fatalf("imbalanced category spread: min=%d max=%d counts=%v", minCat, maxCat, catCounts)
	}
	// Verify every (shop, category) pair is populated evenly too.
	for _, s := range shops {
		for _, c := range cats {
			if shopCatCounts[s.ID+"|"+c.ID] < DefaultCount/(len(shops)*len(cats))-1 {
				t.Fatalf("imbalanced shop-category pair (%s, %s): %d", s.ID, c.ID, shopCatCounts[s.ID+"|"+c.ID])
			}
		}
	}
}

func hasVietnameseDiacritics(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}
