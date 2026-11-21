package meili

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/services/search/internal/resilience"
)

func testClient(baseURL string) *Client {
	exec := resilience.NewExecutor(resilience.Config{
		Name:           "meili-test",
		AttemptTimeout: time.Second,
		MaxAttempts:    2,
		BaseBackoff:    5 * time.Millisecond,
		CBMaxFailures:  5,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return NewClient(Config{
		BaseURL:    baseURL,
		APIKey:     "master-key-test",
		Index:      "products",
		Resilience: exec,
	})
}

func TestPriceBucket(t *testing.T) {
	cases := []struct {
		price int64
		want  string
	}{
		{0, "0-200000"},
		{199000, "0-200000"},
		{200000, "200000-500000"},
		{499000, "200000-500000"},
		{500000, "500000-1000000"},
		{999000, "500000-1000000"},
		{1000000, "1000000+"},
		{2500000, "1000000+"},
	}
	for _, tc := range cases {
		if got := PriceBucket(tc.price); got != tc.want {
			t.Errorf("PriceBucket(%d) = %q, want %q", tc.price, got, tc.want)
		}
	}
}

func TestEscapeFilterStringPreventsInjection(t *testing.T) {
	malicious := `shop-1" OR shop_id != "x` + "\n"
	escaped := EscapeFilterString(malicious)
	if escaped != `"shop-1\" OR shop_id != \"x"` {
		t.Fatalf("unexpected escaped filter string: %s", escaped)
	}
}

func TestEnsureIndexAndSettings(t *testing.T) {
	var (
		mu              sync.Mutex
		createdIndex    bool
		settingsApplied map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer master-key-test" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/indexes/products":
			if !createdIndex {
				http.Error(w, `{"code":"index_not_found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"uid": "products"})
		case r.Method == http.MethodPost && r.URL.Path == "/indexes":
			createdIndex = true
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 1})
		case r.Method == http.MethodPatch && r.URL.Path == "/indexes/products/settings":
			_ = json.NewDecoder(r.Body).Decode(&settingsApplied)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 2})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"uid": 1, "status": "succeeded"})
		default:
			http.Error(w, "unexpected route "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	if err := c.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("EnsureIndex: %v", err)
	}
	if !createdIndex {
		t.Fatal("expected index to be created")
	}
	if settingsApplied == nil {
		t.Fatal("expected settings to be patched")
	}
	searchable, _ := settingsApplied["searchableAttributes"].([]any)
	if len(searchable) != 4 || searchable[0] != "name_folded" {
		t.Fatalf("unexpected searchableAttributes: %v", searchable)
	}
}

func TestSearchNormalizesQueryEscapesFiltersAndParsesFacets(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/indexes/products/search" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": []Document{
				{
					ID:           "p1",
					ShopID:       "shop-1",
					CategorySlug: "ao-thun",
					CategoryName: "Áo thun",
					Name:         "Áo thun thể thao đẹp",
					MinPriceVND:  199000,
					MaxPriceVND:  249000,
					InStock:      true,
					RankingScore: 0.95,
				},
			},
			"estimatedTotalHits": 42,
			"facetDistribution": map[string]map[string]int64{
				"category_slug": {"ao-thun": 42},
				"shop_id":       {"shop-1": 42},
				"price_bucket":  {"0-200000": 15},
			},
		})
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	res, err := c.Search(context.Background(), SearchParams{
		Query:       "Áo thun thể thao đẹp",
		ShopID:      `shop-1" OR 1=1`,
		Category:    "ao-thun",
		MinPriceVND: 100000,
		MaxPriceVND: 300000,
		InStockOnly: true,
		Sort:        "price_asc",
		Limit:       20,
		Offset:      0,
		Facets:      []string{"category_slug", "shop_id", "price_bucket"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if capturedBody["q"] != "ao thun the thao dep" {
		t.Fatalf("q = %v, want normalized Vietnamese", capturedBody["q"])
	}
	filters, _ := capturedBody["filter"].([]any)
	if len(filters) != 5 {
		t.Fatalf("expected 5 filter clauses, got %v", filters)
	}
	if filters[0] != `shop_id = "shop-1\" OR 1=1"` {
		t.Fatalf("filter injection not escaped: %v", filters[0])
	}
	if res.TotalHits != 42 || len(res.Hits) != 1 || res.Hits[0].ID != "p1" {
		t.Fatalf("unexpected search result: %+v", res)
	}
	if res.FacetDistribution["price_bucket"]["0-200000"] != 15 {
		t.Fatalf("unexpected facet distribution: %+v", res.FacetDistribution)
	}
}

func TestSuggestDeduplicatesCategoriesAndProducts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": []Document{
				{ID: "p1", Name: "Áo thun chạy bộ", CategorySlug: "ao-thun", CategoryName: "Áo thun", CategoryNameFolded: "ao thun", MinPriceVND: 150000},
				{ID: "p2", Name: "Áo thun chạy bộ", CategorySlug: "ao-thun", CategoryName: "Áo thun", CategoryNameFolded: "ao thun", MinPriceVND: 160000},
				{ID: "p3", Name: "Áo thun cotton", CategorySlug: "ao-thun", CategoryName: "Áo thun", CategoryNameFolded: "ao thun", MinPriceVND: 180000},
			},
			"estimatedTotalHits": 3,
		})
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	suggestions, err := c.Suggest(context.Background(), "Áo thun", "", 5)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	// Expected: 1 category ("Áo thun") + 2 deduplicated products ("Áo thun chạy bộ", "Áo thun cotton").
	if len(suggestions) != 3 {
		t.Fatalf("len(suggestions) = %d, want 3: %+v", len(suggestions), suggestions)
	}
	if suggestions[0].Kind != "category" || suggestions[0].CategorySlug != "ao-thun" {
		t.Fatalf("expected category first, got %+v", suggestions[0])
	}
	if suggestions[1].Kind != "product" || suggestions[1].ProductID != "p1" {
		t.Fatalf("unexpected first product suggestion: %+v", suggestions[1])
	}
	if suggestions[2].Kind != "product" || suggestions[2].ProductID != "p3" {
		t.Fatalf("unexpected second product suggestion: %+v", suggestions[2])
	}
}

func TestUpsertDeleteAndPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "available"})
		case r.Method == http.MethodPut && r.URL.Path == "/indexes/products/documents":
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 7})
		case r.Method == http.MethodDelete && r.URL.Path == "/indexes/products/documents/p1":
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 8})
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/7":
			_ = json.NewEncoder(w).Encode(map[string]any{"uid": 7, "status": "succeeded"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	uid, err := c.UpsertDocuments(context.Background(), []Document{{ID: "p1", Name: "Test"}})
	if err != nil || uid != 7 {
		t.Fatalf("UpsertDocuments = (%d, %v), want (7, nil)", uid, err)
	}
	if err := c.WaitTask(context.Background(), uid); err != nil {
		t.Fatalf("WaitTask: %v", err)
	}
	delUID, err := c.DeleteDocument(context.Background(), "p1")
	if err != nil || delUID != 8 {
		t.Fatalf("DeleteDocument = (%d, %v), want (8, nil)", delUID, err)
	}
}
