//go:build meili_real

package meili

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMeiliReal_S11_S14_FilterInjectionAndColorCodes runs against a real Meilisearch v1.58.0
// instance pointed to by TEST_MEILI_URL to prove:
//   - S11: valid attr values like "XL (60kg)" match cleanly
//   - S11: injection payload `x" OR status = "HIDDEN` CANNOT bypass public visibility on real Meili
//   - S14: color filter matches either colors_folded ("trang tinh") OR color_codes ("trang")
func TestMeiliReal_S11_S14_FilterInjectionAndColorCodes(t *testing.T) {
	meiliURL := strings.TrimSpace(os.Getenv("TEST_MEILI_URL"))
	if meiliURL == "" {
		t.Fatal("TEST_MEILI_URL must be set when running -tags=meili_real")
	}

	ctx := context.Background()
	client := NewClient(Options{
		URL:      meiliURL,
		APIKey:   os.Getenv("TEST_MEILI_KEY"),
		IndexUID: "products_s11_test",
		Timeout:  3 * time.Second,
	})
	if err := client.EnsureConfigured(ctx); err != nil {
		t.Fatalf("EnsureConfigured real Meili: %v", err)
	}

	docs := []ProductDoc{
		{
			ID:           "p-public-white",
			ShopID:       "shop-1",
			Name:         "Áo sơ mi trắng tinh",
			NameFolded:   "ao so mi trang tinh",
			MinPriceVND:  250000,
			MaxPriceVND:  250000,
			Status:       "APPROVED",
			InStock:      true,
			Colors:       []string{"Trắng tinh"},
			ColorsFolded: []string{"trang tinh"},
			ColorCodes:   []string{"trang"},
			Sizes:        []string{"XL (60kg)"},
			SizesFolded:  []string{"xl (60kg)"},
		},
		{
			ID:           "p-secret-hidden",
			ShopID:       "shop-1",
			Name:         "Áo ẩn bí mật",
			NameFolded:   "ao an bi mat",
			MinPriceVND:  100000,
			MaxPriceVND:  100000,
			Status:       "HIDDEN",
			InStock:      true,
			Colors:       []string{"Đen"},
			ColorsFolded: []string{"den"},
			ColorCodes:   []string{"den"},
			Sizes:        []string{"M"},
			SizesFolded:  []string{"m"},
		},
	}
	if err := client.UpsertProducts(ctx, docs, true); err != nil {
		t.Fatalf("UpsertProducts real Meili: %v", err)
	}

	// 1. S11: Valid size with parentheses "xl (60kg)" matches p-public-white.
	resSize, err := client.Search(ctx, SearchQuery{SizeFolded: "xl (60kg)", Limit: 10})
	if err != nil {
		t.Fatalf("Search size='xl (60kg)' failed: %v", err)
	}
	if len(resSize.Hits) != 1 || resSize.Hits[0].ID != "p-public-white" {
		t.Fatalf("S11 real Meili: size='xl (60kg)' want [p-public-white], got %+v", resSize.Hits)
	}

	// 2. S11: Injection payload `x" OR status = "HIDDEN` in size or color MUST return 0 hits (never leak HIDDEN!).
	for _, q := range []SearchQuery{
		{SizeFolded: `x" OR status = "HIDDEN`, Limit: 10},
		{ColorFolded: `x" OR status = "HIDDEN`, Limit: 10},
	} {
		resInj, err := client.Search(ctx, q)
		if err != nil {
			t.Fatalf("S11 real Meili injection query errored unexpectedly: %v", err)
		}
		if len(resInj.Hits) != 0 {
			t.Fatalf("S11 SECURITY FAILURE: injection leaked hits on real Meilisearch: %+v", resInj.Hits)
		}
	}

	// 3. S14: color="trang" (color_code) AND color="trang tinh" (folded color name) BOTH return p-public-white.
	for _, colorVal := range []string{"trang", "trang tinh"} {
		resColor, err := client.Search(ctx, SearchQuery{ColorFolded: colorVal, Limit: 10})
		if err != nil {
			t.Fatalf("S14 real Meili color=%q failed: %v", colorVal, err)
		}
		if len(resColor.Hits) != 1 || resColor.Hits[0].ID != "p-public-white" {
			t.Fatalf("S14 real Meili color=%q want [p-public-white], got %+v", colorVal, resColor.Hits)
		}
	}
}

// TestMeiliReal_PriceSortIsStrict: an explicit sort orders every match strictly by price (default Meilisearch ranking
// applies sort after words/typo/proximity/attribute) and never lets partial matches jump to the top.
func TestMeiliReal_PriceSortIsStrict(t *testing.T) {
	meiliURL := strings.TrimSpace(os.Getenv("TEST_MEILI_URL"))
	if meiliURL == "" {
		t.Fatal("TEST_MEILI_URL must be set when running -tags=meili_real")
	}
	ctx := context.Background()
	client := NewClient(Options{URL: meiliURL, APIKey: os.Getenv("TEST_MEILI_KEY"), IndexUID: "products_sort_test", Timeout: 3 * time.Second})
	if err := client.EnsureConfigured(ctx); err != nil {
		t.Fatalf("EnsureConfigured: %v", err)
	}
	doc := func(id, name, desc string, price int64) ProductDoc {
		return ProductDoc{ID: id, Name: name, NameFolded: name, DescriptionFolded: desc, MinPriceVND: price, MaxPriceVND: price, Status: "APPROVED", InStock: true}
	}
	if err := client.UpsertProducts(ctx, []ProductDoc{
		doc("exact", "ao thun cotton", "", 300000),
		doc("far", "ao khoac phoi vai thun", "", 100000),
		doc("in-description", "set do mua he", "gom ao thun va quan", 200000),
		doc("partial", "ao so mi", "", 50000),
	}, true); err != nil {
		t.Fatalf("UpsertProducts: %v", err)
	}
	for sortBy, want := range map[string][]string{
		"price_asc":  {"far", "in-description", "exact"},
		"price_desc": {"exact", "in-description", "far"},
	} {
		res, err := client.Search(ctx, SearchQuery{Query: "ao thun", SortBy: sortBy, Limit: 10})
		if err != nil {
			t.Fatalf("Search %s: %v", sortBy, err)
		}
		got := make([]string, 0, len(res.Hits))
		for _, h := range res.Hits {
			got = append(got, h.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", sortBy, got, want)
		}
	}

	// K4: Multi-word query with an extra word under price_asc falls back once to matchingStrategy=last
	// instead of dropping to 0, while still ordering strictly by price ascending.
	resExtra, err := client.Search(ctx, SearchQuery{Query: "ao thun gia re", SortBy: "price_asc", Limit: 10})
	if err != nil {
		t.Fatalf("K4 Search extra-word price_asc: %v", err)
	}
	if len(resExtra.Hits) == 0 || resExtra.Total == 0 {
		t.Fatalf("K4 price_asc extra-word must not drop to 0, got hits=%d total=%d", len(resExtra.Hits), resExtra.Total)
	}
	gotExtra := make([]string, 0, len(resExtra.Hits))
	for i, h := range resExtra.Hits {
		gotExtra = append(gotExtra, h.ID)
		if i > 0 && resExtra.Hits[i-1].MinPriceVND > h.MinPriceVND {
			t.Errorf("K4 price_asc extra-word not sorted strictly ascending: %+v", resExtra.Hits)
		}
	}
	for _, mustHave := range []string{"far", "in-description", "exact"} {
		if !slices.Contains(gotExtra, mustHave) {
			t.Errorf("K4 price_asc extra-word = %v, missing %q", gotExtra, mustHave)
		}
	}
}
