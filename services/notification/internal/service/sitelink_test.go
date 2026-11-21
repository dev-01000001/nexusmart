package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/services/notification/internal/store"
)

type stubSiteResolver struct {
	mu    sync.Mutex
	hosts map[string]string
	err   error
	delay time.Duration
	calls []string
}

func (r *stubSiteResolver) GetSitePrimaryHost(ctx context.Context, shopID string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, shopID)
	r.mu.Unlock()
	if r.delay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(r.delay):
		}
	}
	if r.err != nil {
		return "", r.err
	}
	return r.hosts[shopID], nil
}

const (
	siteShopID = "11111111-1111-1111-1111-111111111111"
	siteOrder  = "22222222-2222-2222-2222-222222222222"
)

func newSiteLinkService(t *testing.T, resolver *stubSiteResolver, cfg SiteLinkConfig) (*NotificationService, *memRepo, *recordingMailer) {
	t.Helper()
	repo := newMemRepo()
	repo.contacts["buyer-site"] = store.UserContact{UserID: "buyer-site", Email: "buyer@nexusmart.local"}
	mail := &recordingMailer{}
	svc := NewNotificationService(repo, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSiteLinks(resolver, cfg))
	return svc, repo, mail
}

func processOrderEvent(t *testing.T, svc *NotificationService, eventID, eventType string, payload map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	if _, err := svc.ProcessEvent(context.Background(), eventType, Envelope{
		EventID: eventID, EventType: eventType, Payload: raw,
	}); err != nil {
		t.Fatalf("%s failed: %v", eventType, err)
	}
}

func lastNotification(t *testing.T, repo *memRepo) store.Notification {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.notifs) == 0 {
		t.Fatal("expected an in-app notification")
	}
	return repo.notifs[len(repo.notifs)-1]
}

func TestStorefrontOrderLinksToSiteHost(t *testing.T) {
	resolver := &stubSiteResolver{hosts: map[string]string{siteShopID: "bach-dang.nexusmart.vn"}}
	svc, repo, mail := newSiteLinkService(t, resolver, SiteLinkConfig{})

	processOrderEvent(t, svc, "evt-site-created", "order.created", map[string]any{
		"order_id": siteOrder, "order_code": "NM-1", "buyer_id": "buyer-site",
		"shop_id": siteShopID, "storefront_shop_id": siteShopID,
		"payment_method": "COD", "total_payable_vnd": 150000,
	})

	want := "https://bach-dang.nexusmart.vn/don-hang/" + siteOrder
	if got := lastNotification(t, repo).Link; got != want {
		t.Fatalf("in-app link = %q, want %q", got, want)
	}
	if len(mail.sent) != 1 || !strings.Contains(mail.sent[0].Body, want) {
		t.Fatalf("email must contain absolute site link %q, got %+v", want, mail.sent)
	}
	if got := repo.participants[siteOrder].StorefrontShopID; got != siteShopID {
		t.Fatalf("storefront_shop_id must be cached, got %q", got)
	}

	// payment.* carries only order_id: storefront comes from the cached order participant.
	processOrderEvent(t, svc, "evt-site-paid", "payment.succeeded", map[string]any{
		"payment_id": "pay-1", "order_id": siteOrder, "user_id": "buyer-site",
		"amount_vnd": 150000, "method": "VIETQR", "provider_tx_id": "tx-1",
	})
	if got := lastNotification(t, repo).Link; got != want {
		t.Fatalf("payment in-app link = %q, want %q", got, want)
	}
	if len(mail.sent) != 2 || !strings.Contains(mail.sent[1].Body, want) {
		t.Fatalf("payment email must contain site link, got %+v", mail.sent)
	}

	// Checkout-group payment.succeeded (order_id == order_group_id, carrying storefront_shop_id)
	// links to the site's order list rather than a non-existent /don-hang/<groupID>.
	groupID := "33333333-3333-3333-3333-333333333333"
	processOrderEvent(t, svc, "evt-site-paid-group", "payment.succeeded", map[string]any{
		"payment_id": "pay-2", "order_group_id": groupID, "order_id": groupID,
		"user_id": "buyer-site", "amount_vnd": 150000, "method": "COD",
		"provider_tx_id": "tx-2", "storefront_shop_id": siteShopID,
	})
	wantList := "https://bach-dang.nexusmart.vn/don-hang"
	if got := lastNotification(t, repo).Link; got != wantList {
		t.Fatalf("group payment in-app link = %q, want %q", got, wantList)
	}
	if len(mail.sent) != 3 || !strings.Contains(mail.sent[2].Body, wantList) {
		t.Fatalf("group payment email must contain site order list link, got %+v", mail.sent[2].Body)
	}

	// payment.refunded carrying sub-order ID and storefront_shop_id links directly to the order page.
	processOrderEvent(t, svc, "evt-site-refunded", "payment.refunded", map[string]any{
		"refund_id": "ref-1", "payment_id": "pay-2", "order_id": siteOrder,
		"user_id": "buyer-site", "amount_vnd": 150000, "reason": "Hủy đơn",
		"storefront_shop_id": siteShopID,
	})
	if got := lastNotification(t, repo).Link; got != want {
		t.Fatalf("refund in-app link = %q, want %q", got, want)
	}
}

func TestSiteLinkSchemeAndPortFromConfig(t *testing.T) {
	resolver := &stubSiteResolver{hosts: map[string]string{siteShopID: "Sai-Gon.nexusmart.localhost"}}
	svc, repo, _ := newSiteLinkService(t, resolver, SiteLinkConfig{Scheme: "http", Port: "3300"})

	processOrderEvent(t, svc, "evt-dev", "order.shipped", map[string]any{
		"order_id": siteOrder, "buyer_id": "buyer-site", "shop_id": siteShopID,
		"storefront_shop_id": siteShopID, "carrier_code": "ghn", "tracking_code": "GHN-1",
	})
	want := "http://sai-gon.nexusmart.localhost:3300/don-hang/" + siteOrder
	if got := lastNotification(t, repo).Link; got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

func TestOrderWithoutPublishedSiteHasNoLink(t *testing.T) {
	cases := []struct {
		name      string
		resolver  *stubSiteResolver
		payload   map[string]any
		wantCalls int
	}{
		{
			name:     "marketplace order (no storefront_shop_id) skips lookup",
			resolver: &stubSiteResolver{hosts: map[string]string{siteShopID: "bach-dang.nexusmart.vn"}},
			payload: map[string]any{
				"order_id": siteOrder, "buyer_id": "buyer-site", "shop_id": siteShopID,
				"payment_method": "COD", "total_payable_vnd": 1000,
			},
			wantCalls: 0,
		},
		{
			name:     "storefront shop without PUBLISHED site",
			resolver: &stubSiteResolver{hosts: map[string]string{}},
			payload: map[string]any{
				"order_id": siteOrder, "buyer_id": "buyer-site", "shop_id": siteShopID,
				"storefront_shop_id": siteShopID, "payment_method": "COD", "total_payable_vnd": 1000,
			},
			wantCalls: 1,
		},
		{
			name:     "lookup error does not fail delivery",
			resolver: &stubSiteResolver{err: errors.New("user service unavailable")},
			payload: map[string]any{
				"order_id": siteOrder, "buyer_id": "buyer-site", "shop_id": siteShopID,
				"storefront_shop_id": siteShopID, "payment_method": "COD", "total_payable_vnd": 1000,
			},
			wantCalls: 1,
		},
		{
			name:     "malformed host is rejected",
			resolver: &stubSiteResolver{hosts: map[string]string{siteShopID: "evil.com/path?x=1"}},
			payload: map[string]any{
				"order_id": siteOrder, "buyer_id": "buyer-site", "shop_id": siteShopID,
				"storefront_shop_id": siteShopID, "payment_method": "COD", "total_payable_vnd": 1000,
			},
			wantCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, mail := newSiteLinkService(t, tc.resolver, SiteLinkConfig{})
			processOrderEvent(t, svc, "evt-nosite", "order.created", tc.payload)

			n := lastNotification(t, repo)
			if n.Link != "" {
				t.Fatalf("expected no link, got %q", n.Link)
			}
			if len(mail.sent) != 1 {
				t.Fatalf("email must still be sent, got %d", len(mail.sent))
			}
			if strings.Contains(mail.sent[0].Body, "http") || strings.Contains(mail.sent[0].Body, "/orders") {
				t.Fatalf("email must not carry a link, got %q", mail.sent[0].Body)
			}
			if len(tc.resolver.calls) != tc.wantCalls {
				t.Fatalf("resolver calls = %d, want %d", len(tc.resolver.calls), tc.wantCalls)
			}
		})
	}
}

func TestSiteLookupTimeoutDoesNotBlockDelivery(t *testing.T) {
	resolver := &stubSiteResolver{hosts: map[string]string{siteShopID: "bach-dang.nexusmart.vn"}, delay: time.Second}
	svc, repo, mail := newSiteLinkService(t, resolver, SiteLinkConfig{LookupTimeout: 20 * time.Millisecond})

	start := time.Now()
	processOrderEvent(t, svc, "evt-slow", "order.cancelled", map[string]any{
		"order_id": siteOrder, "buyer_id": "buyer-site", "shop_id": siteShopID,
		"storefront_shop_id": siteShopID, "reason": "Khách hủy",
	})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("site lookup must be bounded by timeout, took %v", elapsed)
	}
	if got := lastNotification(t, repo).Link; got != "" {
		t.Fatalf("timed-out lookup must drop the link, got %q", got)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("email must still be sent, got %d", len(mail.sent))
	}
}

func TestNoSiteResolverMeansNoOrderLink(t *testing.T) {
	repo := newMemRepo()
	svc := NewNotificationService(repo, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	processOrderEvent(t, svc, "evt-plain", "order.completed", map[string]any{
		"order_id": siteOrder, "buyer_id": "buyer-site", "storefront_shop_id": siteShopID,
	})
	if got := lastNotification(t, repo).Link; got != "" {
		t.Fatalf("expected no link without a site resolver, got %q", got)
	}
}

func TestSiteOrderURL(t *testing.T) {
	cfg := SiteLinkConfig{Scheme: "https"}
	cases := map[string]struct {
		host, order, want string
	}{
		"plain":            {"ha-noi.nexusmart.vn", "abc", "https://ha-noi.nexusmart.vn/don-hang/abc"},
		"stored port":      {"ha-noi.nexusmart.vn:8443", "abc", "https://ha-noi.nexusmart.vn/don-hang/abc"},
		"empty host":       {"", "abc", ""},
		"single label":     {"localhost", "abc", ""},
		"userinfo":         {"user@evil.com", "abc", ""},
		"order with slash": {"ha-noi.nexusmart.vn", "../admin", ""},
		"empty order":      {"ha-noi.nexusmart.vn", " ", ""},
	}
	for name, tc := range cases {
		if got := cfg.siteOrderURL(tc.host, tc.order); got != tc.want {
			t.Errorf("%s: siteOrderURL(%q,%q) = %q, want %q", name, tc.host, tc.order, got, tc.want)
		}
	}
}
