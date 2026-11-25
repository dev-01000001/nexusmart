package meili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildFilters_BlocksFilterInjectionAndEnforcesPublicStatus(t *testing.T) {
	injectionCases := []struct {
		name string
		q    SearchQuery
	}{
		{name: "shop_id OR injection", q: SearchQuery{ShopID: `x" OR status="HIDDEN`}},
		{name: "category_id OR injection", q: SearchQuery{CategoryID: `cat-1" OR status="PENDING`}},
		{name: "size control char", q: SearchQuery{SizeFolded: "xl\x00"}},
		{name: "color newline control char", q: SearchQuery{ColorFolded: "den\nOR true"}},
		{name: "size exceeds max length", q: SearchQuery{SizeFolded: strings.Repeat("a", 65)}},
		{name: "shop_id newline injection", q: SearchQuery{ShopID: "shop-1\nOR true"}},
	}
	for _, tc := range injectionCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildFilters(tc.q); err == nil {
				t.Fatalf("expected filter rejection for %q", tc.name)
			}
		})
	}

	// S11: Valid attribute values with parens/operators (e.g. "XL (60kg)", "black and white")
	// MUST be accepted and safely escaped instead of returning 400.
	validAttrFilters, err := BuildFilters(SearchQuery{
		SizeFolded:  "xl (60kg)",
		ColorFolded: "black and white",
	})
	if err != nil {
		t.Fatalf("S11: valid size/color with parens/AND should be accepted, got: %v", err)
	}
	validJoined := strings.Join(validAttrFilters, " && ")
	if !strings.Contains(validJoined, `sizes_folded = "xl (60kg)"`) {
		t.Fatalf("S11: missing escaped size filter in %q", validJoined)
	}

	// S11: Quote injection inside attr value is escaped so it cannot break out of string literal.
	escapedAttrFilters, err := BuildFilters(SearchQuery{
		ColorFolded: `x" OR status = "HIDDEN`,
	})
	if err != nil {
		t.Fatalf("S11: expected escaped literal rather than reject for printable quote chars, got: %v", err)
	}
	escapedJoined := strings.Join(escapedAttrFilters, " && ")
	if !strings.Contains(escapedJoined, `x\" OR status = \"HIDDEN`) {
		t.Fatalf("S11: expected quotes escaped in %q", escapedJoined)
	}

	filters, err := BuildFilters(SearchQuery{
		CategoryID:      "cat-ao-thun",
		ShopID:          "shop-1",
		ExcludedShopIDs: []string{"shop-suspended-1"},
		MinPriceVND:     100000,
		MaxPriceVND:     3500000000,
		MinRatingX100:   450,
		InStockOnly:     true,
		SizeFolded:      "xl",
		ColorFolded:     "xanh navy",
	})
	if err != nil {
		t.Fatalf("valid BuildFilters returned error: %v", err)
	}
	joined := strings.Join(filters, " && ")
	for _, want := range []string{
		`status IN ["APPROVED", "OUT_OF_STOCK"]`,
		`category_ancestor_ids = "cat-ao-thun"`,
		`shop_id = "shop-1"`,
		`shop_id NOT IN ["shop-suspended-1"]`,
		`max_price_vnd >= 100000`,
		`min_price_vnd <= 3500000000`,
		`rating_avg_x100 >= 450`,
		`in_stock = true`,
		`sizes_folded = "xl"`,
		`(colors_folded = "xanh navy" OR color_codes = "xanh navy")`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected filter %q in %q", want, joined)
		}
	}
}

func TestMeiliClient_OfflineAtStartup_AutoReconnectsAndReloadsConfig(t *testing.T) {
	var (
		online           atomic.Bool
		indexCreated     atomic.Bool
		settingsApplied  atomic.Int32
		mu               sync.Mutex
		capturedSettings map[string]any
		docs             = []ProductDoc{
			{
				ID:            "prod-1",
				ShopID:        "shop-1",
				Name:          "Áo thun cotton",
				NameFolded:    "ao thun cotton",
				MinPriceVND:   2500000000,
				MaxPriceVND:   3000000000,
				Status:        "APPROVED",
				RatingAvgX100: 480,
				ReviewCount:   19,
				InStock:       true,
			},
		}
	)
	// Start offline!
	online.Store(false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !online.Load() {
			http.Error(w, "meilisearch down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"available"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes":
			indexCreated.Store(true)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":"products","status":"enqueued","type":"indexCreation"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/indexes/products/settings":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			capturedSettings = body
			mu.Unlock()
			settingsApplied.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"taskUid":2,"indexUid":"products","status":"enqueued","type":"settingsUpdate"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"settingsUpdate"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes/products/search":
			if !indexCreated.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Index products not found","code":"index_not_found","type":"invalid_request"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hits":               docs,
				"estimatedTotalHits": len(docs),
				"query":              "ao thun",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(Options{
		URL:               srv.URL,
		APIKey:            "test-master-key",
		Timeout:           200 * time.Millisecond,
		ReconnectInterval: 25 * time.Millisecond,
	})

	// 1. Offline at startup: EnsureConfigured and Search fail cleanly without panicking.
	if err := client.EnsureConfigured(context.Background()); err == nil {
		t.Fatalf("expected EnsureConfigured error while Meilisearch is down")
	}
	if client.IsConfigured() {
		t.Fatalf("expected IsConfigured()=false while down")
	}
	if _, err := client.Search(context.Background(), SearchQuery{Query: "ao thun", Limit: 10}); err == nil {
		t.Fatalf("expected Search error while Meilisearch is down")
	}

	// 2. Start reconnect loop; bring Meilisearch online and verify automatic index & settings reload.
	loopCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.StartReconnectLoop(loopCtx)

	online.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.IsConfigured() {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !client.IsConfigured() {
		t.Fatalf("expected Meili client to auto-reconnect and configure index once online")
	}
	if !indexCreated.Load() || settingsApplied.Load() < 1 {
		t.Fatalf("expected index creation and settings reload on reconnect")
	}

	mu.Lock()
	syns, hasSyns := capturedSettings["synonyms"].(map[string]any)
	mu.Unlock()
	if !hasSyns || len(syns) == 0 {
		t.Fatalf("expected synonyms.json to be uploaded to Meilisearch settings")
	}

	// 3. Search works and preserves int64 VND prices > MaxInt32 and rating_avg_x100.
	res, err := client.Search(context.Background(), SearchQuery{Query: "ao thun", Limit: 10})
	if err != nil {
		t.Fatalf("Search after reconnect failed: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].MinPriceVND != 2500000000 || res.Hits[0].RatingAvgX100 != 480 {
		t.Fatalf("unexpected search hit after reconnect: %+v", res.Hits)
	}

	// 4. Simulate Meilisearch restarting with an empty index (index_not_found) -> auto-reconfigures!
	indexCreated.Store(false)
	prevApplies := settingsApplied.Load()
	if _, err := client.Search(context.Background(), SearchQuery{Query: "ao thun", Limit: 10}); err == nil {
		t.Fatalf("expected index_not_found error")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.IsConfigured() && settingsApplied.Load() > prevApplies {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !client.IsConfigured() || settingsApplied.Load() <= prevApplies {
		t.Fatalf("expected client to re-configure index after index_not_found")
	}
}

func TestMeiliClient_K3_WaitTaskDeadlineExceededIsError(t *testing.T) {
	var (
		hangDocTasks atomic.Bool
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
			uid := 100 + nextUID.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"documentAdditionOrUpdate"}`, uid)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/indexes/products/documents/"):
			uid := 100 + nextUID.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"documentDeletion"}`, uid)
		case r.Method == http.MethodPost && r.URL.Path == "/indexes/products/documents/delete-batch":
			uid := 100 + nextUID.Add(1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"documentDeletion"}`, uid)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			if r.URL.Path == "/tasks/1" || r.URL.Path == "/tasks/2" || !hangDocTasks.Load() {
				_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"documentAdditionOrUpdate"}`))
				return
			}
			_, _ = w.Write([]byte(`{"uid":101,"indexUid":"products","status":"enqueued","type":"documentAdditionOrUpdate"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(Options{
		URL:     srv.URL,
		Timeout: 500 * time.Millisecond,
	})
	if err := client.EnsureConfigured(context.Background()); err != nil {
		t.Fatalf("EnsureConfigured: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	err := client.UpsertProducts(shortCtx, []ProductDoc{{ID: "p-1", Name: "Ao"}}, true)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("K3: UpsertProducts with hanging task got err=%v, want wrapped context.DeadlineExceeded", err)
	}

	shortCtx2, cancel2 := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel2()
	err = client.DeleteProduct(shortCtx2, "p-1", true)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("K3: DeleteProduct with hanging task got err=%v, want wrapped context.DeadlineExceeded", err)
	}

	shortCtx3, cancel3 := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel3()
	err = client.DeleteProducts(shortCtx3, []string{"p-1"}, true)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("K3: DeleteProducts with hanging task got err=%v, want wrapped context.DeadlineExceeded", err)
	}

	// Verify K3 configured timeout budgets and WithTaskWaitTimeout override on a live parent context.
	if EventTaskWaitTimeout != 15*time.Second || ReindexTaskWaitTimeout != 60*time.Second || httpWriteTimeout != 5*time.Second {
		t.Fatalf("K3 timeouts mismatch: event=%v reindex=%v httpWrite=%v", EventTaskWaitTimeout, ReindexTaskWaitTimeout, httpWriteTimeout)
	}
	overrideCtx := WithTaskWaitTimeout(context.Background(), 100*time.Millisecond)
	err = client.UpsertProducts(overrideCtx, []ProductDoc{{ID: "p-2", Name: "Ao"}}, true)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("K3: WithTaskWaitTimeout override got err=%v, want wrapped context.DeadlineExceeded", err)
	}
}

func TestMeiliClient_K3_SlowSettingsDedupedAndFailureResetsUID(t *testing.T) {
	t.Run("slow settings submits once and waits on same task UID", func(t *testing.T) {
		var (
			settingsCalls atomic.Int32
			settingsReady atomic.Bool
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
				n := settingsCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"settingsUpdate"}`, 10+n)
			case r.Method == http.MethodGet && r.URL.Path == "/tasks/1":
				_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"indexCreation"}`))
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
				if settingsReady.Load() {
					_, _ = w.Write([]byte(`{"uid":11,"indexUid":"products","status":"succeeded","type":"settingsUpdate"}`))
					return
				}
				_, _ = w.Write([]byte(`{"uid":11,"indexUid":"products","status":"processing","type":"settingsUpdate"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		client := NewClient(Options{
			URL:     srv.URL,
			Timeout: 120 * time.Millisecond,
		})

		// First two calls time out while settings task is still processing.
		for i := 1; i <= 2; i++ {
			cctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			err := client.EnsureConfigured(cctx)
			cancel()
			if err == nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("K3 call %d: got err=%v, want DeadlineExceeded", i, err)
			}
			if client.IsConfigured() {
				t.Fatalf("K3 call %d: client must NOT mark configured=true before settings task succeeds", i)
			}
			if got := settingsCalls.Load(); got != 1 {
				t.Fatalf("K3 call %d: PATCH /settings called %d times, want exactly 1", i, got)
			}
		}

		// Search while unconfigured also respects short timeout and does NOT submit another settings task.
		if _, err := client.Search(context.Background(), SearchQuery{Query: "ao", Limit: 10}); err == nil {
			t.Fatalf("K3: Search while settings pending must return error so service degrades")
		}
		if got := settingsCalls.Load(); got != 1 {
			t.Fatalf("K3 after Search: PATCH /settings called %d times, want 1", got)
		}

		// Once settings task finishes, next call completes without re-submitting settings.
		settingsReady.Store(true)
		if err := client.EnsureConfigured(context.Background()); err != nil {
			t.Fatalf("K3 final EnsureConfigured: %v", err)
		}
		if !client.IsConfigured() {
			t.Fatalf("K3: expected configured=true after settings task succeeded")
		}
		if got := settingsCalls.Load(); got != 1 {
			t.Fatalf("K3 final: PATCH /settings called %d times, want 1", got)
		}
	})

	t.Run("failed settings task forgets UID so next call re-submits", func(t *testing.T) {
		var settingsCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/health":
				_, _ = w.Write([]byte(`{"status":"available"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/indexes":
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":"products","status":"enqueued","type":"indexCreation"}`))
			case r.Method == http.MethodPatch && r.URL.Path == "/indexes/products/settings":
				n := settingsCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":"products","status":"enqueued","type":"settingsUpdate"}`, 20+n)
			case r.Method == http.MethodGet && r.URL.Path == "/tasks/1":
				_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"indexCreation"}`))
			case r.Method == http.MethodGet && r.URL.Path == "/tasks/21":
				_, _ = w.Write([]byte(`{"uid":21,"indexUid":"products","status":"failed","type":"settingsUpdate","error":{"code":"internal","message":"oom"}}`))
			case r.Method == http.MethodGet && r.URL.Path == "/tasks/22":
				_, _ = w.Write([]byte(`{"uid":22,"indexUid":"products","status":"succeeded","type":"settingsUpdate"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		client := NewClient(Options{URL: srv.URL, Timeout: 500 * time.Millisecond})
		if err := client.EnsureConfigured(context.Background()); err == nil {
			t.Fatalf("K3: expected error when first settings task fails")
		}
		if client.IsConfigured() {
			t.Fatalf("K3: configured must remain false after failed settings task")
		}
		if err := client.EnsureConfigured(context.Background()); err != nil {
			t.Fatalf("K3: second EnsureConfigured after failure should re-submit and succeed: %v", err)
		}
		if !client.IsConfigured() || settingsCalls.Load() != 2 {
			t.Fatalf("K3: want configured=true and 2 settings calls, got configured=%v calls=%d", client.IsConfigured(), settingsCalls.Load())
		}
	})
}

func TestMeiliClient_K3_BadRequestFilterSortFacetsResetsConfigured(t *testing.T) {
	for _, tc := range []struct {
		code      string
		wantReset bool
	}{
		{code: "invalid_search_filter", wantReset: true},
		{code: "invalid_search_sort", wantReset: true},
		{code: "invalid_search_facets", wantReset: true},
		{code: "invalid_search_limit", wantReset: false},
	} {
		t.Run(tc.code, func(t *testing.T) {
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
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
					_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"settingsUpdate"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/indexes/products/search":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprintf(w, `{"message":"bad search config","code":%q,"type":"invalid_request"}`, tc.code)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			client := NewClient(Options{URL: srv.URL, Timeout: 500 * time.Millisecond})
			if err := client.EnsureConfigured(context.Background()); err != nil {
				t.Fatalf("EnsureConfigured: %v", err)
			}
			if _, err := client.Search(context.Background(), SearchQuery{Query: "ao", Limit: 10}); err == nil {
				t.Fatalf("expected 400 search error for %s", tc.code)
			}
			gotReset := !client.IsConfigured()
			if gotReset != tc.wantReset {
				t.Fatalf("K3 code=%s: configured reset=%v, want %v", tc.code, gotReset, tc.wantReset)
			}
		})
	}
}

func TestMeiliClient_K4_ExplicitSortFallbackToLastOnZeroResults(t *testing.T) {
	type recordedReq struct {
		Q                string   `json:"q"`
		MatchingStrategy string   `json:"matchingStrategy"`
		Sort             []string `json:"sort"`
		Filter           any      `json:"filter"`
		Facets           []string `json:"facets"`
		Limit            int64    `json:"limit"`
		Offset           int64    `json:"offset"`
	}

	catalog := []ProductDoc{
		{ID: "p-cheap", Name: "Áo thun cotton", NameFolded: "ao thun cotton", MinPriceVND: 100000, MaxPriceVND: 100000, Status: "APPROVED", InStock: true},
		{ID: "p-mid", Name: "Áo thun nam", NameFolded: "ao thun nam", MinPriceVND: 200000, MaxPriceVND: 200000, Status: "APPROVED", InStock: true},
		{ID: "p-partial", Name: "Áo sơ mi", NameFolded: "ao so mi", MinPriceVND: 50000, MaxPriceVND: 50000, Status: "APPROVED", InStock: true},
	}

	var (
		mu       sync.Mutex
		recorded []recordedReq
	)
	resetRecorded := func() {
		mu.Lock()
		recorded = nil
		mu.Unlock()
	}
	getRecorded := func() []recordedReq {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedReq(nil), recorded...)
	}

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
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			_, _ = w.Write([]byte(`{"uid":1,"indexUid":"products","status":"succeeded","type":"settingsUpdate"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/indexes/products/search":
			var body recordedReq
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			recorded = append(recorded, body)
			mu.Unlock()

			words := strings.Fields(body.Q)
			hits := make([]ProductDoc, 0)
			// Simulate Meilisearch "all" vs "last" matching strategy.
			for n := len(words); n >= 1; n-- {
				prefix := words[:n]
				for _, d := range catalog {
					ok := true
					for _, wToken := range prefix {
						if !strings.Contains(d.NameFolded, wToken) {
							ok = false
							break
						}
					}
					if ok {
						hits = append(hits, d)
					}
				}
				if body.MatchingStrategy == "all" || len(hits) > 0 {
					break
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hits":               hits,
				"estimatedTotalHits": len(hits),
				"query":              body.Q,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(Options{URL: srv.URL, Timeout: 500 * time.Millisecond})
	if err := client.EnsureConfigured(context.Background()); err != nil {
		t.Fatalf("EnsureConfigured: %v", err)
	}

	// 1. Multi-word query with extra word under price_asc: "all" returns 0 -> retries ONCE with "last" (same filter/sort/facets).
	resetRecorded()
	res, err := client.Search(context.Background(), SearchQuery{
		Query:      "ao thun gia re",
		CategoryID: "cat-1",
		SortBy:     "price_asc",
		Facets:     []string{"category_ancestor_ids", "shop_id"},
		Limit:      20,
		Offset:     0,
	})
	if err != nil {
		t.Fatalf("K4 extra-word price_asc failed: %v", err)
	}
	reqs := getRecorded()
	if len(reqs) != 2 {
		t.Fatalf("K4 extra-word price_asc: got %d Meili search calls, want 2 (all -> last)", len(reqs))
	}
	if reqs[0].MatchingStrategy != "all" || reqs[1].MatchingStrategy != "last" {
		t.Fatalf("K4 strategies = [%q, %q], want [all, last]", reqs[0].MatchingStrategy, reqs[1].MatchingStrategy)
	}
	if !slices.Equal(reqs[0].Sort, reqs[1].Sort) || !slices.Equal(reqs[0].Facets, reqs[1].Facets) || reqs[0].Limit != reqs[1].Limit || reqs[0].Offset != reqs[1].Offset {
		t.Fatalf("K4 fallback must preserve sort/facets/limit/offset: req0=%+v req1=%+v", reqs[0], reqs[1])
	}
	if len(res.Hits) != 2 || res.Total != 2 {
		t.Fatalf("K4 extra-word price_asc: want 2 hits, got len=%d total=%d", len(res.Hits), res.Total)
	}

	// 2. Multi-word query where all words match ("ao thun") -> NO second call, only full-match products returned!
	resetRecorded()
	resExact, err := client.Search(context.Background(), SearchQuery{
		Query:  "ao thun",
		SortBy: "price_asc",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("K4 full-match price_asc failed: %v", err)
	}
	reqs = getRecorded()
	if len(reqs) != 1 {
		t.Fatalf("K4 full-match price_asc: got %d calls, want 1 (must NOT retry when hits > 0)", len(reqs))
	}
	if reqs[0].MatchingStrategy != "all" {
		t.Fatalf("K4 full-match strategy = %q, want all", reqs[0].MatchingStrategy)
	}
	gotIDs := make([]string, 0, len(resExact.Hits))
	for _, h := range resExact.Hits {
		gotIDs = append(gotIDs, h.ID)
	}
	if !slices.Equal(gotIDs, []string{"p-cheap", "p-mid"}) {
		t.Fatalf("K4 full-match must NOT include partial match p-partial; got %v", gotIDs)
	}

	// 3. Single-word query returning 0 -> NO second call!
	resetRecorded()
	resSingle, err := client.Search(context.Background(), SearchQuery{
		Query:  "khongtontai",
		SortBy: "rating_desc",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("K4 single-word rating_desc failed: %v", err)
	}
	if len(getRecorded()) != 1 || resSingle.Total != 0 {
		t.Fatalf("K4 single-word 0-hit: got calls=%d total=%d, want calls=1 total=0", len(getRecorded()), resSingle.Total)
	}

	// 4. Relevance search returning 0 -> path unchanged (matchingStrategy empty, 1 call)!
	resetRecorded()
	resRel, err := client.Search(context.Background(), SearchQuery{
		Query:  "khong co gi",
		SortBy: "relevance",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("K4 relevance search failed: %v", err)
	}
	reqs = getRecorded()
	if len(reqs) != 1 || reqs[0].MatchingStrategy != "" || resRel.Total != 0 {
		t.Fatalf("K4 relevance path must remain unchanged: calls=%d strategy=%q total=%d", len(reqs), reqs[0].MatchingStrategy, resRel.Total)
	}
}
