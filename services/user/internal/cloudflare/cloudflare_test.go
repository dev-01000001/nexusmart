package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMockClientLifecycleAndSuffixTransitions(t *testing.T) {
	ctx := context.Background()
	client := NewMock("shops.nexusmart.localhost")

	recID, err := client.EnsureSubdomainRecord(ctx, "bach-dang.nexusmart.localhost")
	if err != nil {
		t.Fatalf("EnsureSubdomainRecord error: %v", err)
	}
	if recID != "mock-dns-bach-dang.nexusmart.localhost" {
		t.Fatalf("EnsureSubdomainRecord id = %q, want %q", recID, "mock-dns-bach-dang.nexusmart.localhost")
	}
	if err := client.DeleteDNSRecord(ctx, recID); err != nil {
		t.Fatalf("DeleteDNSRecord error: %v", err)
	}

	assertVerificationRecords := func(t *testing.T, host string, recs []VerificationRecord) {
		t.Helper()
		if len(recs) != 3 {
			t.Fatalf("expected 3 verification records for %s, got %d: %+v", host, len(recs), recs)
		}
		want := []VerificationRecord{
			{Type: "CNAME", Name: host, Value: "shops.nexusmart.localhost"},
			{Type: "TXT", Name: "_cf-custom-hostname." + host, Value: "mock-verify-" + host},
			{Type: "TXT", Name: "_acme-challenge." + host, Value: "mock-ssl-" + host},
		}
		for i := range want {
			if recs[i] != want[i] {
				t.Errorf("record[%d] = %+v, want %+v", i, recs[i], want[i])
			}
		}
	}

	// .verified.test: PENDING on create, ACTIVE on first Get
	created, err := client.CreateCustomHostname(ctx, "hanoi-knit.verified.test")
	if err != nil {
		t.Fatalf("CreateCustomHostname error: %v", err)
	}
	if created.ID != "mock-ch-hanoi-knit.verified.test" || created.Status != StatusPending || created.LastError != "" {
		t.Fatalf("unexpected create result: %+v", created)
	}
	assertVerificationRecords(t, "hanoi-knit.verified.test", created.Verification)

	checked, err := client.GetCustomHostname(ctx, created.ID, "hanoi-knit.verified.test")
	if err != nil {
		t.Fatalf("GetCustomHostname verified error: %v", err)
	}
	if checked.ID != created.ID || checked.Status != StatusActive || checked.LastError != "" {
		t.Fatalf("expected ACTIVE for .verified.test, got %+v", checked)
	}
	assertVerificationRecords(t, "hanoi-knit.verified.test", checked.Verification)

	// .pending.test: stays PENDING, fallback ID when empty
	pending, err := client.GetCustomHostname(ctx, "", "danang.pending.test")
	if err != nil {
		t.Fatalf("GetCustomHostname pending error: %v", err)
	}
	if pending.ID != "mock-ch-danang.pending.test" || pending.Status != StatusPending || pending.LastError != "" {
		t.Fatalf("expected PENDING for .pending.test, got %+v", pending)
	}
	assertVerificationRecords(t, "danang.pending.test", pending.Verification)

	// other domain: stays PENDING
	other, err := client.GetCustomHostname(ctx, "custom-id-1", "shop.example.com")
	if err != nil {
		t.Fatalf("GetCustomHostname other error: %v", err)
	}
	if other.ID != "custom-id-1" || other.Status != StatusPending || other.LastError != "" {
		t.Fatalf("expected PENDING for other domain, got %+v", other)
	}

	// .fail.test: transitions to FAILED with last_error
	failed, err := client.GetCustomHostname(ctx, "mock-ch-shop.fail.test", "shop.fail.test")
	if err != nil {
		t.Fatalf("GetCustomHostname fail error: %v", err)
	}
	if failed.Status != StatusFailed || failed.LastError == "" {
		t.Fatalf("expected FAILED with LastError for .fail.test, got %+v", failed)
	}
	assertVerificationRecords(t, "shop.fail.test", failed.Verification)

	if err := client.DeleteCustomHostname(ctx, created.ID); err != nil {
		t.Fatalf("DeleteCustomHostname error: %v", err)
	}
	recreated, err := client.CreateCustomHostname(ctx, "hanoi-knit.verified.test")
	if err != nil {
		t.Fatalf("re-CreateCustomHostname after delete error: %v", err)
	}
	if recreated.ID != created.ID || recreated.Status != StatusPending {
		t.Fatalf("unexpected recreated result after delete: %+v", recreated)
	}
}

func TestNewLiveValidation(t *testing.T) {
	base := LiveConfig{
		APIToken:    "tok-123",
		ZoneID:      "zone-1",
		OriginHost:  "origin.nexusmart.vn",
		CNAMETarget: "shops.nexusmart.vn",
	}

	cases := []struct {
		name   string
		mutate func(*LiveConfig)
	}{
		{"missing token", func(c *LiveConfig) { c.APIToken = "" }},
		{"missing zone", func(c *LiveConfig) { c.ZoneID = "  " }},
		{"missing origin", func(c *LiveConfig) { c.OriginHost = "" }},
		{"missing cname target", func(c *LiveConfig) { c.CNAMETarget = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			if _, err := NewLive(cfg); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func newTestLiveClient(t *testing.T, serverURL, token string) *LiveClient {
	t.Helper()
	client, err := NewLive(LiveConfig{
		APIToken:    token,
		ZoneID:      "zone-abc",
		OriginHost:  "origin.nexusmart.vn",
		CNAMETarget: "shops.nexusmart.vn",
		BaseURL:     serverURL,
		Timeout:     2 * time.Second,
		MaxRetries:  3,
		BaseBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLive error: %v", err)
	}
	return client
}

func TestLiveEnsureSubdomainRecordCreateAndIdempotentReuse(t *testing.T) {
	const secretToken = "cf-live-secret-token-xyz"
	var getCalls, postCalls atomic.Int32
	var createdExisting atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secretToken {
			t.Errorf("Authorization header = %q, want Bearer token", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/zones/zone-abc/dns_records" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			if r.URL.Query().Get("type") != "CNAME" || r.URL.Query().Get("name") != "bach-dang.nexusmart.vn" {
				t.Errorf("unexpected query %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			if createdExisting.Load() {
				_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"dns-rec-001"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
			}
		case http.MethodPost:
			postCalls.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode POST body: %v", err)
			}
			if payload["type"] != "CNAME" || payload["name"] != "bach-dang.nexusmart.vn" ||
				payload["content"] != "origin.nexusmart.vn" || payload["proxied"] != true || payload["ttl"] != float64(1) {
				t.Errorf("unexpected create DNS payload: %+v", payload)
			}
			createdExisting.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"dns-rec-001"}}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	client := newTestLiveClient(t, srv.URL, secretToken)
	ctx := context.Background()

	// First call: GET returns empty -> POST creates record
	id1, err := client.EnsureSubdomainRecord(ctx, "bach-dang.nexusmart.vn")
	if err != nil {
		t.Fatalf("first EnsureSubdomainRecord error: %v", err)
	}
	if id1 != "dns-rec-001" {
		t.Fatalf("id1 = %q, want dns-rec-001", id1)
	}
	if getCalls.Load() != 1 || postCalls.Load() != 1 {
		t.Fatalf("after first call: get=%d post=%d, want 1, 1", getCalls.Load(), postCalls.Load())
	}

	// Second call: GET finds existing -> does NOT POST
	id2, err := client.EnsureSubdomainRecord(ctx, "bach-dang.nexusmart.vn")
	if err != nil {
		t.Fatalf("second EnsureSubdomainRecord error: %v", err)
	}
	if id2 != "dns-rec-001" {
		t.Fatalf("id2 = %q, want dns-rec-001", id2)
	}
	if getCalls.Load() != 2 || postCalls.Load() != 1 {
		t.Fatalf("after second call: get=%d post=%d, want 2, 1", getCalls.Load(), postCalls.Load())
	}
}

func TestLiveDeleteDNSRecordSuccessAnd404(t *testing.T) {
	const secretToken = "cf-delete-dns-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer "+secretToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/zones/zone-abc/dns_records/rec-ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"rec-ok"}}`))
		case "/zones/zone-abc/dns_records/rec-missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := newTestLiveClient(t, srv.URL, secretToken)
	ctx := context.Background()

	if err := client.DeleteDNSRecord(ctx, "rec-ok"); err != nil {
		t.Fatalf("DeleteDNSRecord ok error: %v", err)
	}
	if err := client.DeleteDNSRecord(ctx, "rec-missing"); err != nil {
		t.Fatalf("DeleteDNSRecord 404 should return nil, got: %v", err)
	}
}

func TestLiveCustomHostnameCreateGetDelete(t *testing.T) {
	const secretToken = "cf-ch-secret-token"
	var getStep atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secretToken {
			t.Errorf("missing or wrong Bearer token")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-abc/custom_hostnames":
			var body struct {
				Hostname string `json:"hostname"`
				SSL      struct {
					Method string `json:"method"`
					Type   string `json:"type"`
				} `json:"ssl"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Hostname != "shop.example.vn" || body.SSL.Method != "txt" || body.SSL.Type != "dv" {
				t.Errorf("unexpected create custom hostname payload: %+v", body)
			}
			_, _ = w.Write([]byte(`{
				"success": true,
				"errors": [],
				"result": {
					"id": "ch-123",
					"hostname": "shop.example.vn",
					"status": "pending",
					"verification_errors": [],
					"ownership_verification": {
						"type": "txt",
						"name": "_cf-custom-hostname.shop.example.vn",
						"value": "own-tok-1"
					},
					"ssl": {
						"status": "pending_validation",
						"validation_records": [
							{"txt_name": "_acme-challenge.shop.example.vn", "txt_value": "ssl-tok-1"},
							{"txt_name": "_acme-challenge.shop.example.vn", "txt_value": "ssl-tok-1"}
						],
						"validation_errors": []
					}
				}
			}`))

		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-abc/custom_hostnames/ch-123":
			step := getStep.Add(1)
			switch step {
			case 1:
				// host active, ssl still pending -> must remain PENDING
				_, _ = w.Write([]byte(`{
					"success": true,
					"errors": [],
					"result": {
						"id": "ch-123",
						"hostname": "shop.example.vn",
						"status": "active",
						"ssl": {
							"status": "pending_validation",
							"validation_records": [{"txt_name": "_acme-challenge.shop.example.vn", "txt_value": "ssl-tok-1"}]
						}
					}
				}`))
			case 2:
				// both active -> ACTIVE
				_, _ = w.Write([]byte(`{
					"success": true,
					"errors": [],
					"result": {
						"id": "ch-123",
						"hostname": "shop.example.vn",
						"status": "active",
						"ssl": {
							"status": "active"
						}
					}
				}`))
			case 3:
				// ssl validation_timed_out -> FAILED
				_, _ = w.Write([]byte(`{
					"success": true,
					"errors": [],
					"result": {
						"id": "ch-123",
						"hostname": "shop.example.vn",
						"status": "pending",
						"verification_errors": ["custom hostname does not CNAME to this zone"],
						"ssl": {
							"status": "validation_timed_out",
							"validation_errors": [{"message": "TXT record not found"}]
						}
					}
				}`))
			default:
				// host blocked -> FAILED
				_, _ = w.Write([]byte(`{
					"success": true,
					"errors": [],
					"result": {
						"id": "ch-123",
						"hostname": "shop.example.vn",
						"status": "blocked",
						"ssl": {"status": "initializing"}
					}
				}`))
			}

		case r.Method == http.MethodDelete && r.URL.Path == "/zones/zone-abc/custom_hostnames/ch-123":
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"ch-123"}}`))

		case r.Method == http.MethodDelete && r.URL.Path == "/zones/zone-abc/custom_hostnames/ch-gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1436,"message":"Not found"}]}`))

		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := newTestLiveClient(t, srv.URL, secretToken)
	ctx := context.Background()

	// Create
	st, err := client.CreateCustomHostname(ctx, "shop.example.vn")
	if err != nil {
		t.Fatalf("CreateCustomHostname error: %v", err)
	}
	if st.ID != "ch-123" || st.Hostname != "shop.example.vn" || st.Status != StatusPending {
		t.Fatalf("unexpected create status: %+v", st)
	}
	wantRecs := []VerificationRecord{
		{Type: "CNAME", Name: "shop.example.vn", Value: "shops.nexusmart.vn"},
		{Type: "TXT", Name: "_cf-custom-hostname.shop.example.vn", Value: "own-tok-1"},
		{Type: "TXT", Name: "_acme-challenge.shop.example.vn", Value: "ssl-tok-1"},
	}
	if len(st.Verification) != len(wantRecs) {
		t.Fatalf("verification records = %+v, want %+v", st.Verification, wantRecs)
	}
	for i := range wantRecs {
		if st.Verification[i] != wantRecs[i] {
			t.Errorf("verification[%d] = %+v, want %+v", i, st.Verification[i], wantRecs[i])
		}
	}

	// Get #1: host active but ssl pending -> PENDING
	g1, err := client.GetCustomHostname(ctx, "ch-123", "shop.example.vn")
	if err != nil {
		t.Fatalf("GetCustomHostname #1 error: %v", err)
	}
	if g1.Status != StatusPending {
		t.Fatalf("expected PENDING when ssl not active, got %+v", g1)
	}

	// Get #2: both active -> ACTIVE
	g2, err := client.GetCustomHostname(ctx, "ch-123", "shop.example.vn")
	if err != nil {
		t.Fatalf("GetCustomHostname #2 error: %v", err)
	}
	if g2.Status != StatusActive || g2.LastError != "" {
		t.Fatalf("expected ACTIVE, got %+v", g2)
	}

	// Get #3: ssl validation_timed_out -> FAILED with combined errors
	g3, err := client.GetCustomHostname(ctx, "ch-123", "shop.example.vn")
	if err != nil {
		t.Fatalf("GetCustomHostname #3 error: %v", err)
	}
	if g3.Status != StatusFailed {
		t.Fatalf("expected FAILED, got %+v", g3)
	}
	if !strings.Contains(g3.LastError, "CNAME") || !strings.Contains(g3.LastError, "TXT record not found") {
		t.Fatalf("expected combined error messages, got %q", g3.LastError)
	}

	// Get #4: blocked with empty errors -> FAILED with fallback error
	g4, err := client.GetCustomHostname(ctx, "ch-123", "shop.example.vn")
	if err != nil {
		t.Fatalf("GetCustomHostname #4 error: %v", err)
	}
	if g4.Status != StatusFailed || g4.LastError == "" {
		t.Fatalf("expected FAILED with fallback error, got %+v", g4)
	}

	// Delete ok & 404
	if err := client.DeleteCustomHostname(ctx, "ch-123"); err != nil {
		t.Fatalf("DeleteCustomHostname error: %v", err)
	}
	if err := client.DeleteCustomHostname(ctx, "ch-gone"); err != nil {
		t.Fatalf("DeleteCustomHostname 404 should return nil, got: %v", err)
	}
}

func TestLiveRetries429And500(t *testing.T) {
	t.Run("429 retry with Retry-After succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := attempts.Add(1)
			if n <= 2 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"rate limited"}]}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"dns-after-429"}]}`))
		}))
		defer srv.Close()

		client := newTestLiveClient(t, srv.URL, "retry-token")
		id, err := client.EnsureSubdomainRecord(context.Background(), "shop.nexusmart.vn")
		if err != nil {
			t.Fatalf("expected success after 429 retries, got error: %v", err)
		}
		if id != "dns-after-429" {
			t.Fatalf("id = %q, want dns-after-429", id)
		}
		if attempts.Load() != 3 {
			t.Fatalf("attempts = %d, want 3", attempts.Load())
		}
	})

	t.Run("500 retry with exponential backoff succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := attempts.Add(1)
			if n <= 2 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":500,"message":"internal error"}]}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"ch-after-500","hostname":"shop.example.vn","status":"active","ssl":{"status":"active"}}}`))
		}))
		defer srv.Close()

		client := newTestLiveClient(t, srv.URL, "retry-token")
		st, err := client.GetCustomHostname(context.Background(), "ch-after-500", "shop.example.vn")
		if err != nil {
			t.Fatalf("expected success after 500 retries, got error: %v", err)
		}
		if st.Status != StatusActive {
			t.Fatalf("status = %q, want ACTIVE", st.Status)
		}
		if attempts.Load() != 3 {
			t.Fatalf("attempts = %d, want 3", attempts.Load())
		}
	})

	t.Run("backoff caps Retry-After at 5s", func(t *testing.T) {
		client := newTestLiveClient(t, "http://127.0.0.1:1", "tok")
		if got := client.backoffDelay(0, "120"); got != 5*time.Second {
			t.Fatalf("backoffDelay Retry-After=120 = %v, want 5s", got)
		}
		if got := client.backoffDelay(0, "2"); got != 2*time.Second {
			t.Fatalf("backoffDelay Retry-After=2 = %v, want 2s", got)
		}
		if got := client.backoffDelay(1, "invalid"); got != 10*time.Millisecond {
			t.Fatalf("backoffDelay invalid Retry-After = %v, want 10ms", got)
		}
	})

	t.Run("aborts retry immediately on canceled context", func(t *testing.T) {
		var attempts atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			cancel()
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		client := newTestLiveClient(t, srv.URL, "tok")
		start := time.Now()
		_, err := client.EnsureSubdomainRecord(ctx, "shop.nexusmart.vn")
		elapsed := time.Since(start)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if elapsed > time.Second {
			t.Fatalf("expected immediate abort on context cancel, took %v", elapsed)
		}
		if attempts.Load() != 1 {
			t.Fatalf("attempts = %d, want 1", attempts.Load())
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestLiveNeverLeaksAPITokenInErrors(t *testing.T) {
	const secretToken = "cf_super_secret_live_token_987654321"

	t.Run("API error body echoes token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":9109,"message":"Invalid access token ` + secretToken + ` for zone"}]}`))
		}))
		defer srv.Close()

		client := newTestLiveClient(t, srv.URL, secretToken)
		_, err := client.CreateCustomHostname(context.Background(), "shop.example.vn")
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("API error leaked token: %q", err.Error())
		}
		if !strings.Contains(err.Error(), "[REDACTED]") {
			t.Fatalf("expected [REDACTED] marker in sanitized error, got %q", err.Error())
		}
	})

	t.Run("transport error echoes token", func(t *testing.T) {
		httpClient := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return nil, errors.New("proxy rejected Authorization: Bearer " + secretToken)
			}),
		}
		client, err := NewLive(LiveConfig{
			APIToken:    secretToken,
			ZoneID:      "zone-abc",
			OriginHost:  "origin.nexusmart.vn",
			CNAMETarget: "shops.nexusmart.vn",
			BaseURL:     "https://api.cloudflare.com/client/v4",
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			HTTPClient:  httpClient,
		})
		if err != nil {
			t.Fatalf("NewLive error: %v", err)
		}

		_, err = client.EnsureSubdomainRecord(context.Background(), "shop.nexusmart.vn")
		if err == nil {
			t.Fatalf("expected transport error, got nil")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("transport error leaked token: %q", err.Error())
		}
		if unwrapped := errors.Unwrap(err); unwrapped != nil && strings.Contains(unwrapped.Error(), secretToken) {
			t.Fatalf("unwrapped transport error leaked token: %q", unwrapped.Error())
		}
	})

	t.Run("SanitizeError redacts multiple secrets", func(t *testing.T) {
		rawErr := errors.New("failed auth with " + secretToken + " and secondary=sec_abc_999")
		sanitized := SanitizeError(rawErr, secretToken, "sec_abc_999")
		if strings.Contains(sanitized, secretToken) || strings.Contains(sanitized, "sec_abc_999") {
			t.Fatalf("SanitizeError failed to redact: %q", sanitized)
		}
		if SanitizeError(nil, secretToken) != "" {
			t.Fatalf("expected empty string for nil error")
		}
	})
}

func TestLiveGetCustomHostname404ReturnsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/zones/zone-abc/custom_hostnames/ch-missing" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1436,"message":"Not found"}]}`))
	}))
	defer srv.Close()

	client := newTestLiveClient(t, srv.URL, "live-404-token")
	st, err := client.GetCustomHostname(context.Background(), "ch-missing", "Shop.Example.VN")
	if err != nil {
		t.Fatalf("GetCustomHostname 404 must not return error, got: %v", err)
	}
	if st.ID != "ch-missing" || st.Hostname != "shop.example.vn" || st.Status != StatusFailed || st.LastError == "" {
		t.Fatalf("GetCustomHostname 404 must return StatusFailed with LastError, got %+v", st)
	}
}
