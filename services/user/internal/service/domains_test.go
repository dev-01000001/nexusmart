package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/cloudflare"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type spyCFClient struct {
	mu   sync.Mutex
	base cloudflare.Client

	ensureSubdomainCalls []string
	deleteDNSCalls       []string
	createCustomCalls    []string
	getCustomCalls       []string
	deleteCustomCalls    []string

	ensureSubdomainErr error
	deleteDNSErr       error
	createCustomErr    error
	getCustomErr       error
	deleteCustomErr    error

	onEnsureSubdomain func(host string)
	onCreateCustom    func(hostname string)
	onGetCustom       func(cfHostnameID, hostname string)
	onDeleteCustom    func(cfHostnameID string)
}

func newSpyCFClient(cnameTarget string) *spyCFClient {
	return &spyCFClient{base: cloudflare.NewMock(cnameTarget)}
}

func (s *spyCFClient) EnsureSubdomainRecord(ctx context.Context, host string) (string, error) {
	s.mu.Lock()
	s.ensureSubdomainCalls = append(s.ensureSubdomainCalls, host)
	err := s.ensureSubdomainErr
	cb := s.onEnsureSubdomain
	s.mu.Unlock()
	if cb != nil {
		cb(host)
	}
	if err != nil {
		return "", err
	}
	return s.base.EnsureSubdomainRecord(ctx, host)
}

func (s *spyCFClient) DeleteDNSRecord(ctx context.Context, recordID string) error {
	s.mu.Lock()
	s.deleteDNSCalls = append(s.deleteDNSCalls, recordID)
	err := s.deleteDNSErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.base.DeleteDNSRecord(ctx, recordID)
}

func (s *spyCFClient) CreateCustomHostname(ctx context.Context, hostname string) (cloudflare.CustomHostnameStatus, error) {
	s.mu.Lock()
	s.createCustomCalls = append(s.createCustomCalls, hostname)
	err := s.createCustomErr
	cb := s.onCreateCustom
	s.mu.Unlock()
	if cb != nil {
		cb(hostname)
	}
	if err != nil {
		return cloudflare.CustomHostnameStatus{}, err
	}
	return s.base.CreateCustomHostname(ctx, hostname)
}

func (s *spyCFClient) GetCustomHostname(ctx context.Context, cfHostnameID, hostname string) (cloudflare.CustomHostnameStatus, error) {
	s.mu.Lock()
	s.getCustomCalls = append(s.getCustomCalls, hostname)
	err := s.getCustomErr
	cb := s.onGetCustom
	s.mu.Unlock()
	if cb != nil {
		cb(cfHostnameID, hostname)
	}
	if err != nil {
		return cloudflare.CustomHostnameStatus{}, err
	}
	return s.base.GetCustomHostname(ctx, cfHostnameID, hostname)
}

func (s *spyCFClient) DeleteCustomHostname(ctx context.Context, cfHostnameID string) error {
	s.mu.Lock()
	s.deleteCustomCalls = append(s.deleteCustomCalls, cfHostnameID)
	err := s.deleteCustomErr
	cb := s.onDeleteCustom
	s.mu.Unlock()
	if cb != nil {
		cb(cfHostnameID)
	}
	if err != nil {
		return err
	}
	return s.base.DeleteCustomHostname(ctx, cfHostnameID)
}

func startDomainBufconn(t *testing.T, opts Options) (*Server, userv1.UserServiceClient, *memStore, *auth.Manager, *auth.Manager) {
	t.Helper()
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	users, err := auth.NewManager("domain-test-user-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	svcMgr, err := auth.NewManager("domain-test-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("new service auth manager: %v", err)
	}
	log := logger.New("user-domain-test", logger.WithWriter(io.Discard))
	srv := NewServer(store, sec, hasher, users, pub, log, opts)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	userv1.RegisterUserServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, userv1.NewUserServiceClient(conn), store, users, svcMgr
}

func TestDomains_RBACAcrossAllFourRPCs(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	_, client, store, users, svcMgr := startDomainBufconn(t, Options{
		Cloudflare: cf,
		CFMode:     "mock",
	})

	sellerA := uuid.NewString()
	sellerB := uuid.NewString()
	buyerID := uuid.NewString()
	adminID := uuid.NewString()

	shopA := seedActiveShop(t, store, sellerA, "Shop A", ShopStatusActive)
	shopB := seedActiveShop(t, store, sellerB, "Shop B", ShopStatusActive)

	tokA, _, _, _ := users.SignAccessToken(sellerA, RoleSeller, shopA.ID)
	tokB, _, _, _ := users.SignAccessToken(sellerB, RoleSeller, shopB.ID)
	tokBuyer, _, _, _ := users.SignAccessToken(buyerID, RoleBuyer, "")
	tokAdmin, _, _, _ := users.SignAccessToken(adminID, RoleAdmin, "")
	tokSvc, _ := svcMgr.SignServiceToken("gateway-service")

	// Seed published sites for Shop A and Shop B.
	for _, pair := range []struct {
		tok string
		sub string
	}{
		{tokA, "shop-alpha"},
		{tokB, "shop-beta"},
	} {
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, pair.tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  pair.sub,
			ConfigJson: sampleSiteConfigJSON(t, pair.sub, "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft %s: %v", pair.sub, err)
		}
		if _, err := client.PublishSite(ctxWithBearer(t, pair.tok), &userv1.PublishSiteRequest{}); err != nil {
			t.Fatalf("PublishSite %s: %v", pair.sub, err)
		}
	}

	// Seller A adds a custom domain so we have a real domain_id to test against.
	addResp, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
		Hostname: "alpha.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain shopA: %v", err)
	}
	domA := addResp.GetDomain()

	spoof := []string{"x-user-id", adminID, "x-user-role", "admin", "x-shop-id", shopA.ID}

	// 1. Anon / spoofed metadata -> Unauthenticated on all 4 RPCs.
	if _, err := client.ListSiteDomains(ctxWithBearer(t, "", spoof...), &userv1.ListSiteDomainsRequest{ShopId: shopA.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListSiteDomains: want Unauthenticated, got %v", err)
	}
	if _, err := client.AddCustomDomain(ctxWithBearer(t, "", spoof...), &userv1.AddCustomDomainRequest{ShopId: shopA.ID, Hostname: "spoof.verified.test"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon AddCustomDomain: want Unauthenticated, got %v", err)
	}
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, "", spoof...), &userv1.CheckCustomDomainRequest{ShopId: shopA.ID, DomainId: domA.GetId()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon CheckCustomDomain: want Unauthenticated, got %v", err)
	}
	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, "", spoof...), &userv1.RemoveCustomDomainRequest{ShopId: shopA.ID, DomainId: domA.GetId()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon RemoveCustomDomain: want Unauthenticated, got %v", err)
	}

	// 2. Service token & buyer -> PermissionDenied on all 4 RPCs.
	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"service", tokSvc},
		{"buyer", tokBuyer},
	} {
		if _, err := client.ListSiteDomains(ctxWithBearer(t, tc.tok), &userv1.ListSiteDomainsRequest{ShopId: shopA.ID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s ListSiteDomains: want PermissionDenied, got %v", tc.name, err)
		}
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tc.tok), &userv1.AddCustomDomainRequest{ShopId: shopA.ID, Hostname: "x.verified.test"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s AddCustomDomain: want PermissionDenied, got %v", tc.name, err)
		}
		if _, err := client.CheckCustomDomain(ctxWithBearer(t, tc.tok), &userv1.CheckCustomDomainRequest{ShopId: shopA.ID, DomainId: domA.GetId()}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s CheckCustomDomain: want PermissionDenied, got %v", tc.name, err)
		}
		if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tc.tok), &userv1.RemoveCustomDomainRequest{ShopId: shopA.ID, DomainId: domA.GetId()}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s RemoveCustomDomain: want PermissionDenied, got %v", tc.name, err)
		}
	}

	// 3. Cross-shop seller B passing Shop A's shop_id -> PermissionDenied on all 4 RPCs.
	if _, err := client.ListSiteDomains(ctxWithBearer(t, tokB), &userv1.ListSiteDomainsRequest{ShopId: shopA.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop ListSiteDomains: want PermissionDenied, got %v", err)
	}
	if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{ShopId: shopA.ID, Hostname: "hijack.verified.test"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop AddCustomDomain: want PermissionDenied, got %v", err)
	}
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tokB), &userv1.CheckCustomDomainRequest{ShopId: shopA.ID, DomainId: domA.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop CheckCustomDomain: want PermissionDenied, got %v", err)
	}
	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tokB), &userv1.RemoveCustomDomainRequest{ShopId: shopA.ID, DomainId: domA.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop RemoveCustomDomain: want PermissionDenied, got %v", err)
	}

	// 4. Cross-shop seller B passing own/empty shop_id with Shop A's domain_id -> NotFound.
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tokB), &userv1.CheckCustomDomainRequest{DomainId: domA.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("cross-shop domain_id CheckCustomDomain: want NotFound, got %v", err)
	}
	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tokB), &userv1.RemoveCustomDomainRequest{DomainId: domA.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("cross-shop domain_id RemoveCustomDomain: want NotFound, got %v", err)
	}

	// 5. Admin requires shop_id, and can ListSiteDomains + RemoveCustomDomain for Shop A.
	if _, err := client.ListSiteDomains(ctxWithBearer(t, tokAdmin), &userv1.ListSiteDomainsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("admin ListSiteDomains missing shop_id: want InvalidArgument, got %v", err)
	}
	adminList, err := client.ListSiteDomains(ctxWithBearer(t, tokAdmin), &userv1.ListSiteDomainsRequest{ShopId: shopA.ID})
	if err != nil || len(adminList.GetDomains()) != 2 {
		t.Fatalf("admin ListSiteDomains: want 2 domains, got %+v err=%v", adminList, err)
	}
	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tokAdmin), &userv1.RemoveCustomDomainRequest{
		ShopId:   shopA.ID,
		DomainId: domA.GetId(),
	}); err != nil {
		t.Fatalf("admin RemoveCustomDomain: %v", err)
	}
	afterAdminRemove, err := client.ListSiteDomains(ctxWithBearer(t, tokAdmin), &userv1.ListSiteDomainsRequest{ShopId: shopA.ID})
	if err != nil || len(afterAdminRemove.GetDomains()) != 1 {
		t.Fatalf("expected 1 domain after admin remove, got %+v err=%v", afterAdminRemove, err)
	}
}

func TestDomains_ValidationMockVsLiveMode(t *testing.T) {
	// Live mode rejects .test hostnames; mock mode allows them.
	for _, tc := range []struct {
		name          string
		cfMode        string
		hostname      string
		wantCode      codes.Code
		wantErrSubstr string
	}{
		{"mock allows .verified.test", "mock", "hanoi-knit.verified.test", codes.OK, ""},
		{"live rejects .verified.test", "live", "hanoi-knit.verified.test", codes.InvalidArgument, "invalid_hostname:"},
		{"live accepts real domain", "live", "Shop.HanoiKnit.VN", codes.OK, ""},
		{"rejects empty", "mock", "   ", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects single label", "mock", "localhost", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects platform root domain", "mock", "nexusmart.localhost", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects subdomain of platform root", "mock", "sub.nexusmart.localhost", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects ipv4", "mock", "127.0.0.1", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects wildcard", "mock", "*.example.com", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects port", "mock", "shop.example.com:8443", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects .local", "mock", "shop.local", codes.InvalidArgument, "invalid_hostname:"},
		{"rejects .internal", "mock", "shop.internal", codes.InvalidArgument, "invalid_hostname:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cf := newSpyCFClient("shops.nexusmart.localhost")
			_, client, store, users, _ := startDomainBufconn(t, Options{
				Cloudflare:     cf,
				CFMode:         tc.cfMode,
				SiteRootDomain: "nexusmart.localhost",
			})
			owner := uuid.NewString()
			shop := seedActiveShop(t, store, owner, "Val Shop", ShopStatusActive)
			tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, shop.ID)
			if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
				Subdomain:  "val-shop",
				ConfigJson: sampleSiteConfigJSON(t, "Val", "Hero"),
			}); err != nil {
				t.Fatalf("SaveSiteDraft: %v", err)
			}

			resp, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
				Hostname: tc.hostname,
			})
			if status.Code(err) != tc.wantCode {
				t.Fatalf("AddCustomDomain(%q) in %s: want %v, got %v", tc.hostname, tc.cfMode, tc.wantCode, err)
			}
			if tc.wantErrSubstr != "" && !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("want error containing %q, got %q", tc.wantErrSubstr, err.Error())
			}
			if tc.wantCode == codes.OK && resp.GetDomain().GetHost() != strings.ToLower(strings.TrimSpace(tc.hostname)) {
				t.Fatalf("expected normalized lowercase host, got %q", resp.GetDomain().GetHost())
			}
		})
	}
}

func TestDomains_MockSuffixTransitionsLimitAndDuplicate(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	_, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare: cf,
		CFMode:     "mock",
	})

	mkPublishedShop := func(name, sub string) (string, string) {
		owner := uuid.NewString()
		shop := seedActiveShop(t, store, owner, name, ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, shop.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  sub,
			ConfigJson: sampleSiteConfigJSON(t, name, "Hero "+name),
		}); err != nil {
			t.Fatalf("SaveSiteDraft %s: %v", sub, err)
		}
		if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
			t.Fatalf("PublishSite %s: %v", sub, err)
		}
		return shop.ID, tok
	}

	shop1ID, tok1 := mkPublishedShop("Hanoi Knit", "hanoi-knit")
	_, tok2 := mkPublishedShop("Da Nang Craft", "da-nang")
	_, tok3 := mkPublishedShop("Hue Silk", "hue-silk")

	// 1. .verified.test: starts PENDING, not served by GetSiteByHost yet.
	add1, err := client.AddCustomDomain(ctxWithBearer(t, tok1), &userv1.AddCustomDomainRequest{
		Hostname: "Hanoi-Knit.Verified.Test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain verified: %v", err)
	}
	d1 := add1.GetDomain()
	if d1.GetHost() != "hanoi-knit.verified.test" || d1.GetStatus() != DomainStatusPending || d1.GetIsPrimary() || d1.GetKind() != DomainKindCustom {
		t.Fatalf("unexpected initial custom domain: %+v", d1)
	}
	if len(d1.GetVerification()) < 2 {
		t.Fatalf("expected CNAME+TXT verification records, got %+v", d1.GetVerification())
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "hanoi-knit.verified.test"}); status.Code(err) != codes.NotFound {
		t.Fatalf("PENDING custom domain must return NotFound from GetSiteByHost, got %v", err)
	}

	// Max 1 custom domain per site -> FailedPrecondition (domain_limit:).
	createCallsBefore := len(cf.createCustomCalls)
	if _, err := client.AddCustomDomain(ctxWithBearer(t, tok1), &userv1.AddCustomDomainRequest{
		Hostname: "second.verified.test",
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "domain_limit:") {
		t.Fatalf("second custom domain on same site: want FailedPrecondition(domain_limit:), got %v", err)
	}
	if len(cf.createCustomCalls) != createCallsBefore {
		t.Fatalf("pre-check should avoid calling Cloudflare on domain_limit")
	}

	// Case-insensitive duplicate host across sites -> AlreadyExists (domain_taken:).
	if _, err := client.AddCustomDomain(ctxWithBearer(t, tok2), &userv1.AddCustomDomainRequest{
		Hostname: "HANOI-KNIT.VERIFIED.TEST",
	}); status.Code(err) != codes.AlreadyExists || !strings.Contains(err.Error(), "domain_taken:") {
		t.Fatalf("duplicate host across sites: want AlreadyExists(domain_taken:), got %v", err)
	}
	if len(cf.createCustomCalls) != createCallsBefore {
		t.Fatalf("pre-check should avoid calling Cloudflare on domain_taken")
	}

	// First CheckCustomDomain promotes .verified.test to ACTIVE and flips primary!
	chk1, err := client.CheckCustomDomain(ctxWithBearer(t, tok1), &userv1.CheckCustomDomainRequest{
		DomainId: d1.GetId(),
	})
	if err != nil {
		t.Fatalf("CheckCustomDomain verified: %v", err)
	}
	if chk1.GetDomain().GetStatus() != DomainStatusActive || !chk1.GetDomain().GetIsPrimary() || chk1.GetDomain().GetVerifiedAt() == nil {
		t.Fatalf("expected ACTIVE primary custom domain, got %+v", chk1.GetDomain())
	}

	// Public lookups: custom host serves site; subdomain also serves site with custom primary_host; GetSiteByShop returns custom primary_host.
	byCustom, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "hanoi-knit.verified.test"})
	if err != nil || byCustom.GetSite().GetPrimaryHost() != "hanoi-knit.verified.test" {
		t.Fatalf("GetSiteByHost(custom): resp=%+v err=%v", byCustom, err)
	}
	bySub, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "hanoi-knit.nexusmart.localhost"})
	if err != nil || bySub.GetSite().GetPrimaryHost() != "hanoi-knit.verified.test" {
		t.Fatalf("GetSiteByHost(subdomain): want primary_host=custom, resp=%+v err=%v", bySub, err)
	}
	byShop, err := client.GetSiteByShop(context.Background(), &userv1.GetSiteByShopRequest{ShopId: shop1ID})
	if err != nil || byShop.GetSite().GetPrimaryHost() != "hanoi-knit.verified.test" {
		t.Fatalf("GetSiteByShop: want primary_host=custom, resp=%+v err=%v", byShop, err)
	}

	// ListSiteDomains returns primary custom first, non-primary subdomain second.
	listResp, err := client.ListSiteDomains(ctxWithBearer(t, tok1), &userv1.ListSiteDomainsRequest{})
	if err != nil || len(listResp.GetDomains()) != 2 {
		t.Fatalf("ListSiteDomains: want 2 domains, got %+v err=%v", listResp, err)
	}
	if listResp.GetDomains()[0].GetKind() != DomainKindCustom || !listResp.GetDomains()[0].GetIsPrimary() ||
		listResp.GetDomains()[1].GetKind() != DomainKindSubdomain || listResp.GetDomains()[1].GetIsPrimary() {
		t.Fatalf("unexpected domain ordering/primary flags: %+v", listResp.GetDomains())
	}

	// Cannot check or remove a SUBDOMAIN domain.
	subDomainID := listResp.GetDomains()[1].GetId()
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tok1), &userv1.CheckCustomDomainRequest{DomainId: subDomainID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CheckCustomDomain on SUBDOMAIN: want FailedPrecondition, got %v", err)
	}
	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tok1), &userv1.RemoveCustomDomainRequest{DomainId: subDomainID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("RemoveCustomDomain on SUBDOMAIN: want FailedPrecondition, got %v", err)
	}

	// 2. .pending.test stays PENDING on check and returns NotFound from GetSiteByHost.
	add2, err := client.AddCustomDomain(ctxWithBearer(t, tok2), &userv1.AddCustomDomainRequest{
		Hostname: "danang.pending.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain pending: %v", err)
	}
	chk2, err := client.CheckCustomDomain(ctxWithBearer(t, tok2), &userv1.CheckCustomDomainRequest{
		DomainId: add2.GetDomain().GetId(),
	})
	if err != nil || chk2.GetDomain().GetStatus() != DomainStatusPending || chk2.GetDomain().GetIsPrimary() {
		t.Fatalf("expected .pending.test to stay PENDING non-primary, got %+v err=%v", chk2, err)
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "danang.pending.test"}); status.Code(err) != codes.NotFound {
		t.Fatalf(".pending.test GetSiteByHost: want NotFound, got %v", err)
	}

	// 3. .fail.test transitions to FAILED with last_error.
	add3, err := client.AddCustomDomain(ctxWithBearer(t, tok3), &userv1.AddCustomDomainRequest{
		Hostname: "hue.fail.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain fail: %v", err)
	}
	chk3, err := client.CheckCustomDomain(ctxWithBearer(t, tok3), &userv1.CheckCustomDomainRequest{
		DomainId: add3.GetDomain().GetId(),
	})
	if err != nil || chk3.GetDomain().GetStatus() != DomainStatusFailed || chk3.GetDomain().GetIsPrimary() || chk3.GetDomain().GetLastError() == "" {
		t.Fatalf("expected .fail.test to transition to FAILED with last_error, got %+v err=%v", chk3, err)
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "hue.fail.test"}); status.Code(err) != codes.NotFound {
		t.Fatalf(".fail.test GetSiteByHost: want NotFound, got %v", err)
	}
}

func TestDomains_RemoveHappyPathAndCFDeleteErrorRetry(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	srv, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare: cf,
		CFMode:     "mock",
	})

	owner := uuid.NewString()
	shop := seedActiveShop(t, store, owner, "Remove Shop", ShopStatusActive)
	tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, shop.ID)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "remove-shop",
		ConfigJson: sampleSiteConfigJSON(t, "Remove Shop", "Hero"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft: %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
		t.Fatalf("PublishSite: %v", err)
	}

	// Add + activate custom domain.
	add1, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
		Hostname: "clean-remove.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain: %v", err)
	}
	dom1ID := add1.GetDomain().GetId()
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tok), &userv1.CheckCustomDomainRequest{DomainId: dom1ID}); err != nil {
		t.Fatalf("CheckCustomDomain: %v", err)
	}

	// Verify CF DELETE is called BEFORE the DB row is deleted, and row is already REMOVING + subdomain already primary during CF DELETE!
	rowExistedDuringCFDelete := false
	rowStatusDuringCFDelete := ""
	subPrimaryDuringCFDelete := false
	cf.onDeleteCustom = func(_ string) {
		store.mu.Lock()
		defer store.mu.Unlock()
		if d, ok := store.domains[dom1ID]; ok {
			rowExistedDuringCFDelete = true
			rowStatusDuringCFDelete = d.Status
		}
		for _, d := range store.domains {
			if d.Kind == DomainKindSubdomain && d.IsPrimary {
				subPrimaryDuringCFDelete = true
			}
		}
	}

	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tok), &userv1.RemoveCustomDomainRequest{
		DomainId: dom1ID,
	}); err != nil {
		t.Fatalf("RemoveCustomDomain happy path: %v", err)
	}
	cf.onDeleteCustom = nil

	if !rowExistedDuringCFDelete || rowStatusDuringCFDelete != DomainStatusRemoving || !subPrimaryDuringCFDelete {
		t.Fatalf("expected DB row REMOVING and subdomain primary before CF delete: existed=%v status=%q subPrimary=%v",
			rowExistedDuringCFDelete, rowStatusDuringCFDelete, subPrimaryDuringCFDelete)
	}
	if len(cf.deleteCustomCalls) != 1 {
		t.Fatalf("expected 1 CF delete call, got %d", len(cf.deleteCustomCalls))
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "clean-remove.verified.test"}); status.Code(err) != codes.NotFound {
		t.Fatalf("removed custom host GetSiteByHost: want NotFound, got %v", err)
	}
	bySub, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "remove-shop.nexusmart.localhost"})
	if err != nil || bySub.GetSite().GetPrimaryHost() != "remove-shop.nexusmart.localhost" {
		t.Fatalf("subdomain should be primary again, got %+v err=%v", bySub, err)
	}

	// Now test CF DELETE error -> returns Unavailable, keeps REMOVING row, subdomain already primary, poller retries and deletes!
	add2, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
		Hostname: "sticky-remove.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain #2: %v", err)
	}
	dom2ID := add2.GetDomain().GetId()
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tok), &userv1.CheckCustomDomainRequest{DomainId: dom2ID}); err != nil {
		t.Fatalf("CheckCustomDomain #2: %v", err)
	}

	cf.mu.Lock()
	cf.deleteCustomErr = errors.New("cloudflare 503 upstream timeout")
	cf.mu.Unlock()

	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tok), &userv1.RemoveCustomDomainRequest{
		DomainId: dom2ID,
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("RemoveCustomDomain on CF error: want Unavailable, got %v", err)
	}

	// CheckCustomDomain on REMOVING must return FailedPrecondition.
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tok), &userv1.CheckCustomDomainRequest{DomainId: dom2ID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CheckCustomDomain on REMOVING: want FailedPrecondition, got %v", err)
	}

	// Row stays REMOVING with is_primary=false and last_error != "", while subdomain is already primary.
	listWhileRemoving, err := client.ListSiteDomains(ctxWithBearer(t, tok), &userv1.ListSiteDomainsRequest{})
	if err != nil || len(listWhileRemoving.GetDomains()) != 2 {
		t.Fatalf("expected 2 domains while REMOVING, got %+v err=%v", listWhileRemoving, err)
	}
	var foundSub, foundCustom *userv1.Domain
	for _, d := range listWhileRemoving.GetDomains() {
		if d.GetKind() == DomainKindSubdomain {
			foundSub = d
		} else {
			foundCustom = d
		}
	}
	if foundSub == nil || !foundSub.GetIsPrimary() {
		t.Fatalf("subdomain must already be primary when custom is REMOVING: %+v", foundSub)
	}
	if foundCustom == nil || foundCustom.GetStatus() != DomainStatusRemoving || foundCustom.GetIsPrimary() || foundCustom.GetLastError() == "" {
		t.Fatalf("custom domain must stay REMOVING with last_error: %+v", foundCustom)
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "sticky-remove.verified.test"}); status.Code(err) != codes.NotFound {
		t.Fatalf("REMOVING custom domain must not be served by GetSiteByHost, got %v", err)
	}

	// Cloudflare recovers -> poller retries and deletes the REMOVING row.
	cf.mu.Lock()
	cf.deleteCustomErr = nil
	cf.mu.Unlock()

	srv.PollCustomDomainsOnce(context.Background())

	listAfterPoll, err := client.ListSiteDomains(ctxWithBearer(t, tok), &userv1.ListSiteDomainsRequest{})
	if err != nil || len(listAfterPoll.GetDomains()) != 1 || listAfterPoll.GetDomains()[0].GetKind() != DomainKindSubdomain {
		t.Fatalf("poller must delete REMOVING row on CF recovery, got %+v err=%v", listAfterPoll, err)
	}
}

func TestDomains_PollerPromotesExpiresAndStopsCleanly(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	srv, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare:     cf,
		CFMode:         "mock",
		CFPollInterval: 10 * time.Millisecond,
		Now:            getNow,
	})

	mkSite := func(name, sub string) (string, string) {
		owner := uuid.NewString()
		shop := seedActiveShop(t, store, owner, name, ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, shop.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  sub,
			ConfigJson: sampleSiteConfigJSON(t, name, "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft %s: %v", sub, err)
		}
		if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
			t.Fatalf("PublishSite %s: %v", sub, err)
		}
		return shop.ID, tok
	}

	shopFreshID, tokFresh := mkSite("Fresh Shop", "fresh-shop")
	_, tokExpired := mkSite("Expired Shop", "expired-shop")

	freshAdd, err := client.AddCustomDomain(ctxWithBearer(t, tokFresh), &userv1.AddCustomDomainRequest{
		Hostname: "fresh.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain fresh: %v", err)
	}
	expiredAdd, err := client.AddCustomDomain(ctxWithBearer(t, tokExpired), &userv1.AddCustomDomainRequest{
		Hostname: "stale.pending.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain expired: %v", err)
	}

	// Set CreatedAt so fresh is 1 day old and stale is 8 days old relative to `now`.
	store.mu.Lock()
	dFresh := store.domains[freshAdd.GetDomain().GetId()]
	dFresh.CreatedAt = now.Add(-24 * time.Hour)
	store.domains[dFresh.ID] = dFresh

	dStale := store.domains[expiredAdd.GetDomain().GetId()]
	dStale.CreatedAt = now.Add(-8 * 24 * time.Hour)
	store.domains[dStale.ID] = dStale
	store.mu.Unlock()

	srv.PollCustomDomainsOnce(context.Background())

	// 1. Fresh .verified.test promoted to ACTIVE + primary.
	byShop, err := client.GetSiteByShop(context.Background(), &userv1.GetSiteByShopRequest{ShopId: shopFreshID})
	if err != nil || byShop.GetSite().GetPrimaryHost() != "fresh.verified.test" {
		t.Fatalf("poller must promote fresh .verified.test to primary, got %+v err=%v", byShop, err)
	}

	// 2. Stale PENDING (>7d) expired to FAILED with last_error WITHOUT calling CF GetCustomHostname.
	expiredList, err := client.ListSiteDomains(ctxWithBearer(t, tokExpired), &userv1.ListSiteDomainsRequest{})
	if err != nil {
		t.Fatalf("ListSiteDomains expired: %v", err)
	}
	var staleProto *userv1.Domain
	for _, d := range expiredList.GetDomains() {
		if d.GetId() == dStale.ID {
			staleProto = d
		}
	}
	if staleProto == nil || staleProto.GetStatus() != DomainStatusFailed || staleProto.GetIsPrimary() ||
		!strings.Contains(staleProto.GetLastError(), "7 days") {
		t.Fatalf("expected stale PENDING domain to expire to FAILED after 7 days, got %+v", staleProto)
	}
	cf.mu.Lock()
	for _, calledHost := range cf.getCustomCalls {
		if calledHost == "stale.pending.test" {
			cf.mu.Unlock()
			t.Fatalf("poller must NOT call Cloudflare for expired (>7d) PENDING domain")
		}
	}
	cf.mu.Unlock()

	// 3. RunDomainPoller exits cleanly on ctx cancel.
	pollCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.RunDomainPoller(pollCtx)
		close(done)
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunDomainPoller did not stop cleanly after ctx cancel")
	}
}

func TestDomains_SubdomainDNSRecordLifecycle(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	_, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare: cf,
		CFMode:     "mock",
	})

	owner := uuid.NewString()
	adminID := uuid.NewString()
	shop := seedActiveShop(t, store, owner, "DNS Lifecycle Shop", ShopStatusActive)
	tokSeller, _, _, _ := users.SignAccessToken(owner, RoleSeller, shop.ID)
	tokAdmin, _, _, _ := users.SignAccessToken(adminID, RoleAdmin, "")

	// 1. DRAFT creates 0 DNS records.
	save1, err := client.SaveSiteDraft(ctxWithBearer(t, tokSeller), &userv1.SaveSiteDraftRequest{
		Subdomain:  "dns-shop-v1",
		ConfigJson: sampleSiteConfigJSON(t, "DNS V1", "Hero 1"),
	})
	if err != nil {
		t.Fatalf("SaveSiteDraft v1: %v", err)
	}
	if len(cf.ensureSubdomainCalls) != 0 || len(cf.deleteDNSCalls) != 0 {
		t.Fatalf("DRAFT must create 0 DNS records, got ensure=%v delete=%v", cf.ensureSubdomainCalls, cf.deleteDNSCalls)
	}

	// Changing subdomain while still DRAFT creates 0 DNS records.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, tokSeller), &userv1.SaveSiteDraftRequest{
		Subdomain:  "dns-shop-v2",
		ConfigJson: sampleSiteConfigJSON(t, "DNS V2", "Hero 2"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft draft rename: %v", err)
	}
	if len(cf.ensureSubdomainCalls) != 0 || len(cf.deleteDNSCalls) != 0 {
		t.Fatalf("DRAFT rename must create 0 DNS records, got ensure=%v delete=%v", cf.ensureSubdomainCalls, cf.deleteDNSCalls)
	}

	// If Cloudflare fails on first publish -> returns Unavailable, site stays DRAFT.
	cf.mu.Lock()
	cf.ensureSubdomainErr = errors.New("cf api down")
	cf.mu.Unlock()
	if _, err := client.PublishSite(ctxWithBearer(t, tokSeller), &userv1.PublishSiteRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("PublishSite on CF error: want Unavailable, got %v", err)
	}
	cf.mu.Lock()
	cf.ensureSubdomainErr = nil
	cf.ensureSubdomainCalls = nil
	cf.mu.Unlock()

	// 2. First Publish creates 1 DNS record and persists cf_record_id.
	if _, err := client.PublishSite(ctxWithBearer(t, tokSeller), &userv1.PublishSiteRequest{Note: "first"}); err != nil {
		t.Fatalf("PublishSite #1: %v", err)
	}
	if len(cf.ensureSubdomainCalls) != 1 || cf.ensureSubdomainCalls[0] != "dns-shop-v2.nexusmart.localhost" {
		t.Fatalf("first Publish must call EnsureSubdomainRecord once, got %v", cf.ensureSubdomainCalls)
	}
	subDom1, err := store.GetSiteSubdomainDomain(context.Background(), save1.GetSite().GetId())
	if err != nil || subDom1.CfRecordID == nil || *subDom1.CfRecordID != "mock-dns-dns-shop-v2.nexusmart.localhost" {
		t.Fatalf("expected persisted cf_record_id, got %+v err=%v", subDom1, err)
	}
	oldRecID := *subDom1.CfRecordID

	// 3. Second Publish with same subdomain creates 0 new DNS records.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, tokSeller), &userv1.SaveSiteDraftRequest{
		Subdomain:  "dns-shop-v2",
		ConfigJson: sampleSiteConfigJSON(t, "DNS V2b", "Hero 2b"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft same subdomain: %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, tokSeller), &userv1.PublishSiteRequest{Note: "second"}); err != nil {
		t.Fatalf("PublishSite #2: %v", err)
	}
	if len(cf.ensureSubdomainCalls) != 1 {
		t.Fatalf("second Publish with same subdomain must NOT call EnsureSubdomainRecord again, got %v", cf.ensureSubdomainCalls)
	}

	// 4. SuspendSite deletes 0 DNS records.
	if _, err := client.SuspendSite(ctxWithBearer(t, tokAdmin), &userv1.SuspendSiteRequest{
		SiteId:  save1.GetSite().GetId(),
		Suspend: true,
		Reason:  "audit",
	}); err != nil {
		t.Fatalf("SuspendSite true: %v", err)
	}
	if len(cf.deleteDNSCalls) != 0 {
		t.Fatalf("SuspendSite must NOT delete DNS records, got %v", cf.deleteDNSCalls)
	}
	// Unsuspend so site is PUBLISHED again.
	if _, err := client.SuspendSite(ctxWithBearer(t, tokAdmin), &userv1.SuspendSiteRequest{
		SiteId:  save1.GetSite().GetId(),
		Suspend: false,
	}); err != nil {
		t.Fatalf("SuspendSite false: %v", err)
	}

	// Also attach & activate a custom domain to verify renaming subdomain preserves custom domain as primary!
	customAdd, err := client.AddCustomDomain(ctxWithBearer(t, tokSeller), &userv1.AddCustomDomainRequest{
		Hostname: "keep-primary.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain: %v", err)
	}
	if _, err := client.CheckCustomDomain(ctxWithBearer(t, tokSeller), &userv1.CheckCustomDomainRequest{
		DomainId: customAdd.GetDomain().GetId(),
	}); err != nil {
		t.Fatalf("CheckCustomDomain: %v", err)
	}

	// 5. Changing subdomain on a PUBLISHED site creates new DNS record, updates cf_record_id, and deletes old cf_record_id.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, tokSeller), &userv1.SaveSiteDraftRequest{
		Subdomain:  "dns-shop-v3",
		ConfigJson: sampleSiteConfigJSON(t, "DNS V3", "Hero 3"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft rename published: %v", err)
	}
	if len(cf.ensureSubdomainCalls) != 2 || cf.ensureSubdomainCalls[1] != "dns-shop-v3.nexusmart.localhost" {
		t.Fatalf("expected EnsureSubdomainRecord for new subdomain, got %v", cf.ensureSubdomainCalls)
	}
	if len(cf.deleteDNSCalls) != 1 || cf.deleteDNSCalls[0] != oldRecID {
		t.Fatalf("expected DeleteDNSRecord(%q), got %v", oldRecID, cf.deleteDNSCalls)
	}
	subDom2, err := store.GetSiteSubdomainDomain(context.Background(), save1.GetSite().GetId())
	if err != nil || subDom2.CfRecordID == nil || *subDom2.CfRecordID != "mock-dns-dns-shop-v3.nexusmart.localhost" || subDom2.IsPrimary {
		t.Fatalf("expected updated cf_record_id and non-primary subdomain, got %+v err=%v", subDom2, err)
	}
	// Custom domain must remain primary!
	byNewSub, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "dns-shop-v3.nexusmart.localhost"})
	if err != nil || byNewSub.GetSite().GetPrimaryHost() != "keep-primary.verified.test" {
		t.Fatalf("expected custom domain to remain primary after subdomain rename, got %+v err=%v", byNewSub, err)
	}
}

func TestDomains_AntiSquattingRules(t *testing.T) {
	setup := func(t *testing.T, baseNow time.Time) (*spyCFClient, userv1.UserServiceClient, *memStore, func(time.Time), string, string) {
		t.Helper()
		cf := newSpyCFClient("shops.nexusmart.localhost")
		var clockMu sync.Mutex
		curNow := baseNow
		getNow := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return curNow
		}
		setNow := func(next time.Time) {
			clockMu.Lock()
			defer clockMu.Unlock()
			curNow = next
		}

		_, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
			Now:        getNow,
		})

		mkPublished := func(name, sub string) string {
			owner := uuid.NewString()
			shop := seedActiveShop(t, store, owner, name, ShopStatusActive)
			tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, shop.ID)
			if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
				Subdomain:  sub,
				ConfigJson: sampleSiteConfigJSON(t, name, "Hero "+name),
			}); err != nil {
				t.Fatalf("SaveSiteDraft %s: %v", sub, err)
			}
			if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
				t.Fatalf("PublishSite %s: %v", sub, err)
			}
			return tok
		}

		tokA := mkPublished("Shop Alpha", "squat-alpha")
		tokB := mkPublished("Shop Beta", "squat-beta")
		return cf, client, store, setNow, tokA, tokB
	}

	t.Run("Rule1_ActiveAndRemovingBlock_FailedDoesNotBlock", func(t *testing.T) {
		base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		cf, client, store, _, tokA, tokB := setup(t, base)

		// 1a. ACTIVE blocks other site without calling Cloudflare.
		addAct, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "brand-active.verified.test",
		})
		if err != nil {
			t.Fatalf("shopA add active: %v", err)
		}
		if _, err := client.CheckCustomDomain(ctxWithBearer(t, tokA), &userv1.CheckCustomDomainRequest{
			DomainId: addAct.GetDomain().GetId(),
		}); err != nil {
			t.Fatalf("shopA activate: %v", err)
		}

		createBefore := len(cf.createCustomCalls)
		deleteBefore := len(cf.deleteCustomCalls)
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "BRAND-ACTIVE.VERIFIED.TEST",
		}); status.Code(err) != codes.AlreadyExists || !strings.Contains(err.Error(), "domain_taken:") {
			t.Fatalf("shopB claiming ACTIVE host: want AlreadyExists(domain_taken:), got %v", err)
		}
		if len(cf.createCustomCalls) != createBefore || len(cf.deleteCustomCalls) != deleteBefore {
			t.Fatalf("ACTIVE block must not call Cloudflare: create=%d->%d delete=%d->%d",
				createBefore, len(cf.createCustomCalls), deleteBefore, len(cf.deleteCustomCalls))
		}

		// 1b. REMOVING blocks other site without calling Cloudflare.
		cf.mu.Lock()
		cf.deleteCustomErr = errors.New("cf 503 temporary")
		cf.mu.Unlock()
		if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tokA), &userv1.RemoveCustomDomainRequest{
			DomainId: addAct.GetDomain().GetId(),
		}); status.Code(err) != codes.Unavailable {
			t.Fatalf("shopA remove with CF error: want Unavailable, got %v", err)
		}
		cf.mu.Lock()
		cf.deleteCustomErr = nil
		cf.mu.Unlock()

		createBefore = len(cf.createCustomCalls)
		deleteBefore = len(cf.deleteCustomCalls)
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "brand-active.verified.test",
		}); status.Code(err) != codes.AlreadyExists || !strings.Contains(err.Error(), "domain_taken:") {
			t.Fatalf("shopB claiming REMOVING host: want AlreadyExists(domain_taken:), got %v", err)
		}
		if len(cf.createCustomCalls) != createBefore || len(cf.deleteCustomCalls) != deleteBefore {
			t.Fatalf("REMOVING block must not call Cloudflare")
		}

		// Clear Shop A's REMOVING row so Shop A can test a FAILED domain.
		if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tokA), &userv1.RemoveCustomDomainRequest{
			DomainId: addAct.GetDomain().GetId(),
		}); err != nil {
			t.Fatalf("shopA finalize remove: %v", err)
		}

		// 1c. FAILED of other site does NOT block: B reclaims immediately.
		addFail, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "victim.fail.test",
		})
		if err != nil {
			t.Fatalf("shopA add fail: %v", err)
		}
		oldFailID := addFail.GetDomain().GetId()
		chkFail, err := client.CheckCustomDomain(ctxWithBearer(t, tokA), &userv1.CheckCustomDomainRequest{
			DomainId: oldFailID,
		})
		if err != nil || chkFail.GetDomain().GetStatus() != DomainStatusFailed {
			t.Fatalf("expected FAILED after check, got %+v err=%v", chkFail, err)
		}

		reclaimResp, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "VICTIM.FAIL.TEST",
		})
		if err != nil {
			t.Fatalf("shopB reclaiming FAILED host: %v", err)
		}
		gotB := reclaimResp.GetDomain()
		if gotB.GetId() == oldFailID || gotB.GetHost() != "victim.fail.test" || gotB.GetStatus() != DomainStatusPending {
			t.Fatalf("unexpected reclaimed domain for shopB: %+v", gotB)
		}

		listA, err := client.ListSiteDomains(ctxWithBearer(t, tokA), &userv1.ListSiteDomainsRequest{})
		if err != nil || len(listA.GetDomains()) != 1 || listA.GetDomains()[0].GetKind() != DomainKindSubdomain {
			t.Fatalf("shopA must no longer have reclaimed FAILED domain, got %+v err=%v", listA, err)
		}
		listB, err := client.ListSiteDomains(ctxWithBearer(t, tokB), &userv1.ListSiteDomainsRequest{})
		if err != nil || len(listB.GetDomains()) != 2 {
			t.Fatalf("shopB must have subdomain + reclaimed custom domain, got %+v err=%v", listB, err)
		}

		// Verify old row is deleted from store and only B's row exists for the host.
		store.mu.Lock()
		_, oldExists := store.domains[oldFailID]
		_, newExists := store.domains[gotB.GetId()]
		store.mu.Unlock()
		if oldExists || !newExists {
			t.Fatalf("expected old row deleted and new row present: oldExists=%v newExists=%v", oldExists, newExists)
		}
	})

	t.Run("Rule2_PendingHold48h_BlocksWithin48h_ReclaimsAfter48hWithout7DayJob", func(t *testing.T) {
		base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		cf, client, store, setNow, tokA, tokB := setup(t, base)

		addA, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "squat.pending.test",
		})
		if err != nil {
			t.Fatalf("shopA add pending: %v", err)
		}
		oldID := addA.GetDomain().GetId()

		// Anchor CreatedAt to base so 48h boundary assertions are exact.
		store.mu.Lock()
		d := store.domains[oldID]
		d.CreatedAt = base
		store.domains[oldID] = d
		store.mu.Unlock()

		// Within 48h (47h59m and exact 48h00m00s) -> blocked with domain_taken and 0 CF calls.
		for _, offset := range []time.Duration{47*time.Hour + 59*time.Minute, 48 * time.Hour} {
			setNow(base.Add(offset))
			createBefore := len(cf.createCustomCalls)
			deleteBefore := len(cf.deleteCustomCalls)
			if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
				Hostname: "squat.pending.test",
			}); status.Code(err) != codes.AlreadyExists || !strings.Contains(err.Error(), "domain_taken:") {
				t.Fatalf("shopB at +%s: want AlreadyExists(domain_taken:), got %v", offset, err)
			}
			if len(cf.createCustomCalls) != createBefore || len(cf.deleteCustomCalls) != deleteBefore {
				t.Fatalf("at +%s: blocked claim must not call Cloudflare", offset)
			}
		}

		// Past 48h (48h00m01s, still well below 7 days and status still PENDING) -> B reclaims!
		setNow(base.Add(48*time.Hour + time.Second))
		reclaimResp, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "squat.pending.test",
		})
		if err != nil {
			t.Fatalf("shopB reclaim after 48h: %v", err)
		}
		if reclaimResp.GetDomain().GetId() == oldID || reclaimResp.GetDomain().GetStatus() != DomainStatusPending {
			t.Fatalf("unexpected reclaimed domain after 48h: %+v", reclaimResp.GetDomain())
		}
		listA, err := client.ListSiteDomains(ctxWithBearer(t, tokA), &userv1.ListSiteDomainsRequest{})
		if err != nil || len(listA.GetDomains()) != 1 || listA.GetDomains()[0].GetKind() != DomainKindSubdomain {
			t.Fatalf("shopA must no longer own expired PENDING domain, got %+v err=%v", listA, err)
		}
	})

	t.Run("Rule3_ReclaimCallsCFDeleteBeforeRowDeleteAndPreservesOldRowOnCFError", func(t *testing.T) {
		base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		cf, client, store, setNow, tokA, tokB := setup(t, base)

		addA, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "order-check.pending.test",
		})
		if err != nil {
			t.Fatalf("shopA add: %v", err)
		}
		oldID := addA.GetDomain().GetId()
		store.mu.Lock()
		d := store.domains[oldID]
		d.CreatedAt = base
		oldCFID := *d.CfCustomHostnameID
		store.domains[oldID] = d
		store.mu.Unlock()

		setNow(base.Add(49 * time.Hour))

		// 3a. Cloudflare DeleteCustomHostname error -> returns Unavailable, does NOT delete A's old row, does NOT call CreateCustomHostname.
		cf.mu.Lock()
		cf.deleteCustomErr = errors.New("cloudflare 502 bad gateway")
		createBefore := len(cf.createCustomCalls)
		cf.mu.Unlock()

		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "order-check.pending.test",
		}); status.Code(err) != codes.Unavailable {
			t.Fatalf("reclaim when CF delete fails: want Unavailable, got %v", err)
		}
		cf.mu.Lock()
		createAfterErr := len(cf.createCustomCalls)
		cf.deleteCustomErr = nil
		cf.mu.Unlock()
		if createAfterErr != createBefore {
			t.Fatalf("CreateCustomHostname must NOT be called when DeleteCustomHostname fails")
		}
		store.mu.Lock()
		stillThere, ok := store.domains[oldID]
		store.mu.Unlock()
		if !ok || stillThere.ID != oldID {
			t.Fatalf("old domain row must NOT be deleted when CF DeleteCustomHostname fails")
		}

		// 3b. Cloudflare CreateCustomHostname error after Delete -> oldCFID is already deleted on Cloudflare,
		// so neither oldID (pointing to deleted oldCFID) nor any dangling placeholder may remain.
		cf.mu.Lock()
		cf.createCustomErr = errors.New("cloudflare 503 create failure")
		cf.mu.Unlock()
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "order-check.pending.test",
		}); status.Code(err) != codes.Unavailable {
			t.Fatalf("reclaim when CF create fails: want Unavailable, got %v", err)
		}
		cf.mu.Lock()
		cf.createCustomErr = nil
		cf.mu.Unlock()
		store.mu.Lock()
		_, stillThereAfterCreateErr := store.domains[oldID]
		store.mu.Unlock()
		if stillThereAfterCreateErr {
			t.Fatalf("old domain row must NOT remain pointing to already-deleted CF hostname when CF CreateCustomHostname fails")
		}

		// Re-seed stale domain on Shop A for happy-path reclaim ordering check (3c).
		setNow(base)
		addA2, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "order-check.pending.test",
		})
		if err != nil {
			t.Fatalf("shopA re-add for 3c: %v", err)
		}
		oldID = addA2.GetDomain().GetId()
		store.mu.Lock()
		d2 := store.domains[oldID]
		d2.CreatedAt = base
		oldCFID = *d2.CfCustomHostnameID
		store.domains[oldID] = d2
		store.mu.Unlock()
		setNow(base.Add(49 * time.Hour))

		// 3c. Happy reclaim: verify DeleteCustomHostname(oldCFID) runs while old row still exists and BEFORE CreateCustomHostname.
		cf.mu.Lock()
		createCountBeforeReclaim := len(cf.createCustomCalls)
		cf.mu.Unlock()

		var (
			sawDeleteForOldCFID     bool
			oldRowStillPresentOnDel bool
			createNotYetCalledOnDel bool
		)
		cf.mu.Lock()
		cf.onDeleteCustom = func(gotCFID string) {
			if gotCFID == oldCFID {
				sawDeleteForOldCFID = true
			}
			store.mu.Lock()
			_, oldRowStillPresentOnDel = store.domains[oldID]
			store.mu.Unlock()
			cf.mu.Lock()
			createNotYetCalledOnDel = len(cf.createCustomCalls) == createCountBeforeReclaim
			cf.mu.Unlock()
		}
		cf.mu.Unlock()

		reclaimResp, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "order-check.pending.test",
		})
		cf.mu.Lock()
		cf.onDeleteCustom = nil
		cf.mu.Unlock()
		if err != nil {
			t.Fatalf("happy reclaim: %v", err)
		}
		if !sawDeleteForOldCFID || !oldRowStillPresentOnDel || !createNotYetCalledOnDel {
			t.Fatalf("unexpected reclaim call ordering: sawOldCFID=%v oldRowPresent=%v createNotYetCalled=%v",
				sawDeleteForOldCFID, oldRowStillPresentOnDel, createNotYetCalledOnDel)
		}
		store.mu.Lock()
		_, oldExistsAfter := store.domains[oldID]
		_, newExistsAfter := store.domains[reclaimResp.GetDomain().GetId()]
		store.mu.Unlock()
		if oldExistsAfter || !newExistsAfter {
			t.Fatalf("after happy reclaim: oldExists=%v newExists=%v", oldExistsAfter, newExistsAfter)
		}
	})

	t.Run("Rule4_SameSiteReAddOwnFailedHost_ReplacesRowWithoutDomainLimit", func(t *testing.T) {
		base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		cf, client, store, _, tokA, tokB := setup(t, base)

		// Shop A adds retry.fail.test and checks -> FAILED.
		add1, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "retry.fail.test",
		})
		if err != nil {
			t.Fatalf("shopA add #1: %v", err)
		}
		oldID := add1.GetDomain().GetId()
		chk1, err := client.CheckCustomDomain(ctxWithBearer(t, tokA), &userv1.CheckCustomDomainRequest{
			DomainId: oldID,
		})
		if err != nil || chk1.GetDomain().GetStatus() != DomainStatusFailed {
			t.Fatalf("expected FAILED after check, got %+v err=%v", chk1, err)
		}

		// Adding a DIFFERENT host while own host is FAILED still hits domain_limit.
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "different.verified.test",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "domain_limit:") {
			t.Fatalf("adding different host while own is FAILED: want FailedPrecondition(domain_limit:), got %v", err)
		}

		// If Shop B already has its own custom domain, Shop B cannot reclaim Shop A's FAILED host (hits domain_limit without touching A's row).
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "beta-own.pending.test",
		}); err != nil {
			t.Fatalf("shopB add own: %v", err)
		}
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "retry.fail.test",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "domain_limit:") {
			t.Fatalf("shopB at domain_limit reclaiming A's FAILED host: want FailedPrecondition(domain_limit:), got %v", err)
		}
		store.mu.Lock()
		_, aStillIntact := store.domains[oldID]
		store.mu.Unlock()
		if !aStillIntact {
			t.Fatalf("shopB hitting domain_limit must NOT delete shopA's FAILED domain")
		}

		// Re-adding own FAILED host when CF delete fails -> returns Unavailable and preserves old row.
		cf.mu.Lock()
		cf.deleteCustomErr = errors.New("cf delete transient error")
		cf.mu.Unlock()
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "RETRY.FAIL.TEST",
		}); status.Code(err) != codes.Unavailable {
			t.Fatalf("same-site re-add when CF delete fails: want Unavailable, got %v", err)
		}
		cf.mu.Lock()
		cf.deleteCustomErr = nil
		deleteBefore := len(cf.deleteCustomCalls)
		cf.mu.Unlock()

		// Re-adding own FAILED host (case-insensitive) succeeds, replaces old row, no domain_limit!
		add2, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "RETRY.FAIL.TEST",
		})
		if err != nil {
			t.Fatalf("same-site re-add own FAILED host: %v", err)
		}
		dom2 := add2.GetDomain()
		if dom2.GetId() == oldID || dom2.GetHost() != "retry.fail.test" || dom2.GetStatus() != DomainStatusPending || dom2.GetLastError() != "" {
			t.Fatalf("unexpected replaced custom domain: %+v", dom2)
		}
		cf.mu.Lock()
		deleteAfter := len(cf.deleteCustomCalls)
		cf.mu.Unlock()
		if deleteAfter != deleteBefore+1 {
			t.Fatalf("expected CF DeleteCustomHostname for replaced FAILED row: before=%d after=%d", deleteBefore, deleteAfter)
		}
		listA, err := client.ListSiteDomains(ctxWithBearer(t, tokA), &userv1.ListSiteDomainsRequest{})
		if err != nil || len(listA.GetDomains()) != 2 {
			t.Fatalf("expected 1 subdomain + 1 replaced custom domain, got %+v err=%v", listA, err)
		}

		// Re-adding own host now that it is PENDING (not FAILED) must return domain_limit.
		if _, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "retry.fail.test",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "domain_limit:") {
			t.Fatalf("same-site re-add while PENDING: want FailedPrecondition(domain_limit:), got %v", err)
		}
	})
}

type txTrackingStore struct {
	*memStore
	inTx atomic.Int32
}

func (s *txTrackingStore) WithTx(ctx context.Context, fn func(q db.Querier) error) error {
	s.inTx.Add(1)
	defer s.inTx.Add(-1)
	return s.memStore.WithTx(ctx, fn)
}

func TestDomains_AddCustomDomainNeverCallsCloudflareInsideTx(t *testing.T) {
	base := newMemStore()
	tracked := &txTrackingStore{memStore: base}
	cf := newSpyCFClient("shops.nexusmart.localhost")

	var clockMu sync.Mutex
	curNow := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}

	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("tx-track-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	log := logger.New("user-tx-track-test", logger.WithWriter(io.Discard))
	srv := NewServer(tracked, sec, hasher, authMgr, pub, log, Options{
		Cloudflare: cf,
		CFMode:     "mock",
		Now:        getNow,
	})

	var cfCalledInTx atomic.Bool
	cf.onCreateCustom = func(_ string) {
		if tracked.inTx.Load() > 0 {
			cfCalledInTx.Store(true)
		}
	}
	cf.onDeleteCustom = func(_ string) {
		if tracked.inTx.Load() > 0 {
			cfCalledInTx.Store(true)
		}
	}

	mkPublished := func(name, sub string) (string, context.Context) {
		owner := uuid.NewString()
		sh := seedActiveShop(t, base, owner, name, ShopStatusActive)
		ctx := ctxWithUser(owner, RoleSeller, sh.ID)
		if _, err := srv.SaveSiteDraft(ctx, &userv1.SaveSiteDraftRequest{
			Subdomain:  sub,
			ConfigJson: sampleSiteConfigJSON(t, name, "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft %s: %v", sub, err)
		}
		if _, err := srv.PublishSite(ctx, &userv1.PublishSiteRequest{}); err != nil {
			t.Fatalf("PublishSite %s: %v", sub, err)
		}
		return sh.ID, ctx
	}

	_, ctxA := mkPublished("Tx Shop A", "tx-shop-a")
	_, ctxB := mkPublished("Tx Shop B", "tx-shop-b")

	// 1. Fresh AddCustomDomain must call CreateCustomHostname outside WithTx.
	addA, err := srv.AddCustomDomain(ctxA, &userv1.AddCustomDomainRequest{Hostname: "no-tx.pending.test"})
	if err != nil {
		t.Fatalf("fresh AddCustomDomain: %v", err)
	}
	if cfCalledInTx.Load() {
		t.Fatalf("fresh AddCustomDomain called Cloudflare inside a DB transaction")
	}

	// 2. Reclaim AddCustomDomain must call DeleteCustomHostname and CreateCustomHostname outside WithTx.
	base.mu.Lock()
	d := base.domains[addA.GetDomain().GetId()]
	d.CreatedAt = curNow
	base.domains[d.ID] = d
	base.mu.Unlock()

	clockMu.Lock()
	curNow = curNow.Add(49 * time.Hour)
	clockMu.Unlock()

	if _, err := srv.AddCustomDomain(ctxB, &userv1.AddCustomDomainRequest{Hostname: "no-tx.pending.test"}); err != nil {
		t.Fatalf("reclaim AddCustomDomain: %v", err)
	}
	if cfCalledInTx.Load() {
		t.Fatalf("reclaim AddCustomDomain called Cloudflare inside a DB transaction")
	}
}

func TestDomains_AddCustomDomainReclaimCreateErrorLeavesNoDanglingRowOrOrphan(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	var clockMu sync.Mutex
	curNow := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}

	_, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare: cf,
		CFMode:     "mock",
		Now:        getNow,
	})

	mkPublished := func(name, sub string) string {
		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, name, ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  sub,
			ConfigJson: sampleSiteConfigJSON(t, name, "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft %s: %v", sub, err)
		}
		if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
			t.Fatalf("PublishSite %s: %v", sub, err)
		}
		return tok
	}

	tokA := mkPublished("Dangling Shop A", "dangle-a")
	tokB := mkPublished("Dangling Shop B", "dangle-b")

	addA, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
		Hostname: "no-dangle.pending.test",
	})
	if err != nil {
		t.Fatalf("shopA add: %v", err)
	}
	oldID := addA.GetDomain().GetId()
	store.mu.Lock()
	d := store.domains[oldID]
	d.CreatedAt = curNow
	oldCFID := *d.CfCustomHostnameID
	store.domains[oldID] = d
	store.mu.Unlock()

	clockMu.Lock()
	curNow = curNow.Add(49 * time.Hour)
	clockMu.Unlock()

	// DeleteCustomHostname succeeds, then CreateCustomHostname fails.
	cf.mu.Lock()
	cf.createCustomErr = errors.New("cloudflare 503 create failure")
	cf.mu.Unlock()

	if _, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
		Hostname: "no-dangle.pending.test",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("reclaim when CF create fails: want Unavailable, got %v", err)
	}

	cf.mu.Lock()
	cf.createCustomErr = nil
	cf.mu.Unlock()

	// MUST NOT leave any DB row pointing to the already-deleted oldCFID, nor any dangling placeholder for Shop B.
	store.mu.Lock()
	for _, dom := range store.domains {
		if dom.Host == "no-dangle.pending.test" {
			store.mu.Unlock()
			t.Fatalf("mid-flight reclaim CreateCustomHostname error left dangling domain row: %+v (deleted oldCFID=%q)", dom, oldCFID)
		}
	}
	store.mu.Unlock()

	// Retry by Shop B succeeds cleanly.
	retryResp, err := client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
		Hostname: "no-dangle.pending.test",
	})
	if err != nil || retryResp.GetDomain().GetStatus() != DomainStatusPending {
		t.Fatalf("retry after failed reclaim create: resp=%+v err=%v", retryResp, err)
	}
}

func TestDomains_AddCustomDomainPhase3ConcurrentRemovalAndSuspensionCleansUpCF(t *testing.T) {
	t.Run("ConcurrentRemovalDuringPhase2DeletesCreatedCFHostname", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
		})
		_ = srv

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Conc Remove Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "conc-rem",
			ConfigJson: sampleSiteConfigJSON(t, "Conc", "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		cf.onCreateCustom = func(host string) {
			// Simulate concurrent removal of the reserved row while CreateCustomHostname is in-flight.
			store.mu.Lock()
			for id, d := range store.domains {
				if d.Host == host {
					delete(store.domains, id)
				}
			}
			store.mu.Unlock()
		}

		_, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
			Hostname: "conc-rem.verified.test",
		})
		cf.onCreateCustom = nil
		if err == nil {
			t.Fatalf("expected error when reserved domain was removed during Phase 2")
		}

		// Orphan created CF hostname MUST be deleted!
		cf.mu.Lock()
		dels := append([]string(nil), cf.deleteCustomCalls...)
		cf.mu.Unlock()
		foundCleanup := false
		for _, id := range dels {
			if id == "mock-ch-conc-rem.verified.test" {
				foundCleanup = true
				break
			}
		}
		if !foundCleanup {
			t.Fatalf("expected Phase 3 abort to delete orphan CF hostname, deleteCustomCalls=%v", dels)
		}
	})

	t.Run("ShopSuspendedDuringPhase2CleansUpCFAndPlaceholder", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		_, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Conc Suspend Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "conc-susp",
			ConfigJson: sampleSiteConfigJSON(t, "ConcS", "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		cf.onCreateCustom = func(_ string) {
			store.mu.Lock()
			cur := store.shops[sh.ID]
			cur.Status = ShopStatusSuspended
			store.shops[sh.ID] = cur
			store.mu.Unlock()
		}

		_, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
			Hostname: "conc-susp.verified.test",
		})
		cf.onCreateCustom = nil
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
			t.Fatalf("shop suspended during Phase 2: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
		}

		cf.mu.Lock()
		dels := append([]string(nil), cf.deleteCustomCalls...)
		cf.mu.Unlock()
		if len(dels) != 1 || dels[0] != "mock-ch-conc-susp.verified.test" {
			t.Fatalf("expected orphan CF hostname cleanup, got %v", dels)
		}
		store.mu.Lock()
		for _, dom := range store.domains {
			if dom.Kind == DomainKindCustom {
				store.mu.Unlock()
				t.Fatalf("expected reserved placeholder deleted after Phase 3 suspension abort, found %+v", dom)
			}
		}
		store.mu.Unlock()
	})

	t.Run("Phase3AbortWithCFDeleteFailureHandsOffToPollerRemoving", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Poller Handoff Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "handoff-shop",
			ConfigJson: sampleSiteConfigJSON(t, "Handoff", "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		cf.onCreateCustom = func(_ string) {
			store.mu.Lock()
			cur := store.shops[sh.ID]
			cur.Status = ShopStatusSuspended
			store.shops[sh.ID] = cur
			store.mu.Unlock()
			cf.mu.Lock()
			cf.deleteCustomErr = errors.New("cf delete 503 during cleanup")
			cf.mu.Unlock()
		}

		_, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
			Hostname: "handoff.verified.test",
		})
		cf.onCreateCustom = nil
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
			t.Fatalf("want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
		}

		// Row must be REMOVING with cf_custom_hostname_id populated so poller cleans up CF hostname!
		var customRow *db.Domain
		store.mu.Lock()
		for _, d := range store.domains {
			if d.Kind == DomainKindCustom {
				cp := d
				customRow = &cp
			}
		}
		store.mu.Unlock()
		if customRow == nil || customRow.Status != DomainStatusRemoving || customRow.CfCustomHostnameID == nil || *customRow.CfCustomHostnameID != "mock-ch-handoff.verified.test" {
			t.Fatalf("expected REMOVING row with cf_custom_hostname_id for poller handoff, got %+v", customRow)
		}

		// CF recovers -> poller deletes CF hostname and removes DB row.
		cf.mu.Lock()
		cf.deleteCustomErr = nil
		cf.mu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())

		store.mu.Lock()
		_, stillThere := store.domains[customRow.ID]
		store.mu.Unlock()
		if stillThere {
			t.Fatalf("poller must clean up handed-off REMOVING row")
		}
	})

	t.Run("PollerGracePeriodForUnprovisionedPlaceholderAndCheckRejectsInProgress", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		var clockMu sync.Mutex
		baseNow := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		curNow := baseNow
		getNow := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return curNow
		}

		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
			Now:        getNow,
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Placeholder Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		saveResp, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "placeholder-shop",
			ConfigJson: sampleSiteConfigJSON(t, "Placeholder", "Hero"),
		})
		if err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		// Insert an unprovisioned PENDING reservation (simulating an in-flight or crashed Phase 2).
		placeholder, err := store.CreateCustomDomain(context.Background(), db.CreateCustomDomainParams{
			ID:                 uuid.NewString(),
			SiteID:             saveResp.GetSite().GetId(),
			Lower:              "inflight.verified.test",
			IsPrimary:          false,
			TlsStatus:          "PENDING",
			Status:             DomainStatusPending,
			CfCustomHostnameID: nil,
			Verification:       []byte("[]"),
		})
		if err != nil {
			t.Fatalf("CreateCustomDomain placeholder: %v", err)
		}
		store.mu.Lock()
		d := store.domains[placeholder.ID]
		d.CreatedAt = baseNow
		store.domains[placeholder.ID] = d
		store.mu.Unlock()

		// CheckCustomDomain rejects while provisioning is in progress (0 CF calls).
		if _, err := client.CheckCustomDomain(ctxWithBearer(t, tok), &userv1.CheckCustomDomainRequest{
			DomainId: placeholder.ID,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CheckCustomDomain on unprovisioned placeholder: want FailedPrecondition, got %v", err)
		}

		// Within grace period (1m): poller skips without calling CF or failing the row.
		clockMu.Lock()
		curNow = baseNow.Add(1 * time.Minute)
		clockMu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())

		gotWithinGrace, err := store.GetDomainByID(context.Background(), placeholder.ID)
		if err != nil || gotWithinGrace.Status != DomainStatusPending {
			t.Fatalf("within grace period placeholder must stay PENDING, got %+v err=%v", gotWithinGrace, err)
		}
		if len(cf.getCustomCalls) != 0 {
			t.Fatalf("poller must not call CF GetCustomHostname for unprovisioned placeholder, got %v", cf.getCustomCalls)
		}

		// Past grace period (3m): poller marks abandoned placeholder FAILED without calling CF.
		clockMu.Lock()
		curNow = baseNow.Add(3 * time.Minute)
		clockMu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())

		gotAfterGrace, err := store.GetDomainByID(context.Background(), placeholder.ID)
		if err != nil || gotAfterGrace.Status != DomainStatusFailed || gotAfterGrace.LastError == nil || *gotAfterGrace.LastError == "" {
			t.Fatalf("past grace period abandoned placeholder must become FAILED, got %+v err=%v", gotAfterGrace, err)
		}
		if len(cf.getCustomCalls) != 0 {
			t.Fatalf("poller must not call CF GetCustomHostname when failing abandoned placeholder, got %v", cf.getCustomCalls)
		}
	})
}

func TestDomains_PollerAndCheckHandleLive404AsFailedNoLoop(t *testing.T) {
	var getCalls atomic.Int32
	cfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/custom_hostnames"):
			_, _ = w.Write([]byte(`{
				"success": true, "errors": [],
				"result": {
					"id": "ch-live-404",
					"hostname": "gone.example.vn",
					"status": "pending",
					"ssl": {"status": "pending_validation"}
				}
			}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/custom_hostnames/ch-live-404"):
			getCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1436,"message":"Not found"}]}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"dns-live-1"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer cfServer.Close()

	liveCF, err := cloudflare.NewLive(cloudflare.LiveConfig{
		APIToken:    "live-test-token",
		ZoneID:      "zone-1",
		OriginHost:  "origin.nexusmart.vn",
		CNAMETarget: "shops.nexusmart.vn",
		BaseURL:     cfServer.URL,
		Timeout:     2 * time.Second,
		MaxRetries:  1,
		BaseBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("cloudflare.NewLive: %v", err)
	}

	srv, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare:     liveCF,
		CFMode:         "live",
		SiteRootDomain: "nexusmart.vn",
	})

	owner := uuid.NewString()
	sh := seedActiveShop(t, store, owner, "Live 404 Shop", ShopStatusActive)
	tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "live-404",
		ConfigJson: sampleSiteConfigJSON(t, "Live404", "Hero"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft: %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
		t.Fatalf("PublishSite: %v", err)
	}

	addResp, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
		Hostname: "gone.example.vn",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain live: %v", err)
	}
	domID := addResp.GetDomain().GetId()

	// 1st poll: Cloudflare returns 404 -> domain transitions to FAILED with last_error.
	srv.PollCustomDomainsOnce(context.Background())
	if getCalls.Load() != 1 {
		t.Fatalf("expected 1 GET call on first poll, got %d", getCalls.Load())
	}
	domAfter1, err := store.GetDomainByID(context.Background(), domID)
	if err != nil {
		t.Fatalf("GetDomainByID: %v", err)
	}
	if domAfter1.Status != DomainStatusFailed || domAfter1.LastError == nil || *domAfter1.LastError == "" {
		t.Fatalf("poller must mark 404 custom hostname FAILED with last_error, got %+v", domAfter1)
	}

	// 2nd poll: row is now FAILED, NOT polled again (no repeated error loop).
	srv.PollCustomDomainsOnce(context.Background())
	if getCalls.Load() != 1 {
		t.Fatalf("second poll must not re-poll FAILED domain (loop!), got %d total GET calls", getCalls.Load())
	}

	// CheckCustomDomain on a previously ACTIVE primary domain that returns 404 from Cloudflare
	// transitions it to FAILED and restores the site's subdomain as primary.
	store.mu.Lock()
	domActive := store.domains[domID]
	domActive.Status = DomainStatusActive
	domActive.IsPrimary = true
	domActive.LastError = nil
	store.domains[domID] = domActive
	for id, d := range store.domains {
		if d.SiteID == domActive.SiteID && d.Kind == DomainKindSubdomain {
			d.IsPrimary = false
			store.domains[id] = d
		}
	}
	store.mu.Unlock()

	chkResp, err := client.CheckCustomDomain(ctxWithBearer(t, tok), &userv1.CheckCustomDomainRequest{DomainId: domID})
	if err != nil {
		t.Fatalf("CheckCustomDomain on 404: %v", err)
	}
	if chkResp.GetDomain().GetStatus() != DomainStatusFailed || chkResp.GetDomain().GetIsPrimary() || chkResp.GetDomain().GetLastError() == "" {
		t.Fatalf("CheckCustomDomain 404 must transition to FAILED non-primary, got %+v", chkResp.GetDomain())
	}
	subDom, err := store.GetSiteSubdomainDomain(context.Background(), domActive.SiteID)
	if err != nil || !subDom.IsPrimary {
		t.Fatalf("CheckCustomDomain 404 must restore subdomain as primary, got %+v err=%v", subDom, err)
	}
}

func TestDomains_PollerRemovingGraceAndLockPreventsReclaimRace(t *testing.T) {
	t.Run("ReclaimPhase1RaceWithPollerPreservesShopADomainOnCFError", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		var clockMu sync.Mutex
		baseNow := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		curNow := baseNow
		getNow := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return curNow
		}

		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
			Now:        getNow,
		})

		mkPublished := func(name, sub string) string {
			owner := uuid.NewString()
			sh := seedActiveShop(t, store, owner, name, ShopStatusActive)
			tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
			if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
				Subdomain:  sub,
				ConfigJson: sampleSiteConfigJSON(t, name, "Hero"),
			}); err != nil {
				t.Fatalf("SaveSiteDraft %s: %v", sub, err)
			}
			if _, err := client.PublishSite(ctxWithBearer(t, tok), &userv1.PublishSiteRequest{}); err != nil {
				t.Fatalf("PublishSite %s: %v", sub, err)
			}
			return tok
		}

		tokA := mkPublished("Reclaim Race A", "reclaim-race-a")
		tokB := mkPublished("Reclaim Race B", "reclaim-race-b")

		addA, err := client.AddCustomDomain(ctxWithBearer(t, tokA), &userv1.AddCustomDomainRequest{
			Hostname: "race-reclaim.pending.test",
		})
		if err != nil {
			t.Fatalf("shopA add: %v", err)
		}
		oldID := addA.GetDomain().GetId()

		// Simulate prior verification error/check on Shop A's stale (>48h) PENDING row.
		prevErr := "dns txt not found yet"
		checkedAt := baseNow.Add(1 * time.Hour)
		store.mu.Lock()
		d := store.domains[oldID]
		d.CreatedAt = baseNow
		d.LastCheckedAt = &checkedAt
		d.LastError = &prevErr
		store.domains[oldID] = d
		store.mu.Unlock()

		clockMu.Lock()
		curNow = baseNow.Add(49 * time.Hour)
		clockMu.Unlock()

		// While Shop B's reclaim is in-flight in DeleteCustomHostname (which will fail with 502),
		// run PollCustomDomainsOnce concurrently: poller MUST skip the freshly-marked REMOVING row
		// so Shop A's PENDING domain is restored rather than destroyed!
		var pollerRan atomic.Bool
		cf.mu.Lock()
		cf.deleteCustomErr = errors.New("cf 502 transient during reclaim")
		cf.onDeleteCustom = func(_ string) {
			if pollerRan.CompareAndSwap(false, true) {
				// Anchor UpdatedAt to fake clock now (matching Postgres NOW() == s.now()).
				store.mu.Lock()
				cur := store.domains[oldID]
				cur.UpdatedAt = getNow()
				store.domains[oldID] = cur
				store.mu.Unlock()
				srv.PollCustomDomainsOnce(context.Background())
			}
		}
		cf.mu.Unlock()

		_, err = client.AddCustomDomain(ctxWithBearer(t, tokB), &userv1.AddCustomDomainRequest{
			Hostname: "race-reclaim.pending.test",
		})
		cf.mu.Lock()
		cf.onDeleteCustom = nil
		cf.deleteCustomErr = nil
		cf.mu.Unlock()

		if status.Code(err) != codes.Unavailable {
			t.Fatalf("reclaim with CF delete error: want Unavailable, got %v", err)
		}
		if !pollerRan.Load() {
			t.Fatalf("expected poller callback to have executed during reclaim DeleteCustomHostname")
		}
		if len(cf.deleteCustomCalls) != 1 {
			t.Fatalf("poller must NOT call DeleteCustomHostname for freshly-marked REMOVING row, got %d calls: %v", len(cf.deleteCustomCalls), cf.deleteCustomCalls)
		}
		restored, err := store.GetDomainByID(context.Background(), oldID)
		if err != nil {
			t.Fatalf("Shop A domain must NOT be destroyed by poller during failed reclaim: %v", err)
		}
		if restored.Status != DomainStatusPending {
			t.Fatalf("Shop A domain must be restored to PENDING, got status=%s", restored.Status)
		}
	})

	t.Run("PollerRetriesFailedRemovingWithoutInfinitePostponementAndGuardsDeleteWithLock", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		var clockMu sync.Mutex
		baseNow := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		curNow := baseNow
		getNow := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return curNow
		}

		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
			Now:        getNow,
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Removing Grace Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "rem-grace",
			ConfigJson: sampleSiteConfigJSON(t, "RemGrace", "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}
		addResp, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
			Hostname: "crashed-rem.pending.test",
		})
		if err != nil {
			t.Fatalf("AddCustomDomain: %v", err)
		}
		domID := addResp.GetDomain().GetId()

		// Simulate a crashed in-flight REMOVING row (cleared error/check timestamps, UpdatedAt = baseNow).
		store.mu.Lock()
		d := store.domains[domID]
		d.Status = DomainStatusRemoving
		d.LastCheckedAt = nil
		d.LastError = nil
		d.UpdatedAt = baseNow
		store.domains[domID] = d
		store.mu.Unlock()

		// 1. Within grace (1m): poller skips.
		clockMu.Lock()
		curNow = baseNow.Add(1 * time.Minute)
		clockMu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())
		if len(cf.deleteCustomCalls) != 0 {
			t.Fatalf("within grace period poller must skip in-flight REMOVING row, got calls=%v", cf.deleteCustomCalls)
		}

		// 2. Past grace (3m): poller retries; CF fails -> UpdateDomainError refreshes UpdatedAt.
		clockMu.Lock()
		curNow = baseNow.Add(3 * time.Minute)
		clockMu.Unlock()
		cf.mu.Lock()
		cf.deleteCustomErr = errors.New("cf 503 during poller retry")
		cf.mu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())
		if len(cf.deleteCustomCalls) != 1 {
			t.Fatalf("past grace period poller must retry REMOVING row, got calls=%v", cf.deleteCustomCalls)
		}
		// Anchor UpdatedAt to fake clock now (simulating Postgres UpdateDomainError setting updated_at = NOW()).
		store.mu.Lock()
		d = store.domains[domID]
		d.UpdatedAt = getNow()
		store.domains[domID] = d
		store.mu.Unlock()

		// 3. Only 5 seconds later, CF recovers -> poller MUST retry immediately (NOT postponed again by UpdateDomainError refreshing UpdatedAt!).
		//    First verify that if row concurrently transitioned back to PENDING during DeleteCustomHostname,
		//    poller's locked delete does NOT wipe the restored row.
		clockMu.Lock()
		curNow = baseNow.Add(3*time.Minute + 5*time.Second)
		clockMu.Unlock()
		cf.mu.Lock()
		cf.deleteCustomErr = nil
		cf.onDeleteCustom = func(_ string) {
			store.mu.Lock()
			cur := store.domains[domID]
			cur.Status = DomainStatusPending
			store.domains[domID] = cur
			store.mu.Unlock()
		}
		cf.mu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())
		cf.mu.Lock()
		cf.onDeleteCustom = nil
		cf.mu.Unlock()
		if len(cf.deleteCustomCalls) != 2 {
			t.Fatalf("poller must retry failed REMOVING row immediately on next tick without infinite postponement, got calls=%v", cf.deleteCustomCalls)
		}
		restored, err := store.GetDomainByID(context.Background(), domID)
		if err != nil || restored.Status != DomainStatusPending {
			t.Fatalf("poller must NOT delete row whose status changed away from REMOVING, got %+v err=%v", restored, err)
		}

		// 4. Now back to REMOVING with recorded error -> next poll immediately deletes row cleanly.
		store.mu.Lock()
		d = store.domains[domID]
		d.Status = DomainStatusRemoving
		d.UpdatedAt = getNow()
		store.domains[domID] = d
		store.mu.Unlock()
		clockMu.Lock()
		curNow = baseNow.Add(3*time.Minute + 10*time.Second)
		clockMu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())
		if _, err := store.GetDomainByID(context.Background(), domID); !isNotFoundErr(err) {
			t.Fatalf("expected poller to delete REMOVING row on CF recovery, err=%v", err)
		}
	})
}

func TestDomains_RemoveCustomDomainProvisioningGraceAndPhase3OrphanHandoff(t *testing.T) {
	t.Run("RemoveBlockedWithinGraceAllowedAfterGrace", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		var clockMu sync.Mutex
		baseNow := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
		curNow := baseNow
		getNow := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return curNow
		}

		_, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
			Now:        getNow,
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Remove Grace Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		saveResp, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "rem-prov-grace",
			ConfigJson: sampleSiteConfigJSON(t, "RemProv", "Hero"),
		})
		if err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		placeholder, err := store.CreateCustomDomain(context.Background(), db.CreateCustomDomainParams{
			ID:                 uuid.NewString(),
			SiteID:             saveResp.GetSite().GetId(),
			Lower:              "inflight-remove.verified.test",
			IsPrimary:          false,
			TlsStatus:          "PENDING",
			Status:             DomainStatusPending,
			CfCustomHostnameID: nil,
			Verification:       []byte("[]"),
		})
		if err != nil {
			t.Fatalf("CreateCustomDomain placeholder: %v", err)
		}
		store.mu.Lock()
		d := store.domains[placeholder.ID]
		d.CreatedAt = baseNow
		store.domains[placeholder.ID] = d
		store.mu.Unlock()

		// Within grace (1m): RemoveCustomDomain MUST reject with same FailedPrecondition message as CheckCustomDomain.
		clockMu.Lock()
		curNow = baseNow.Add(1 * time.Minute)
		clockMu.Unlock()
		_, err = client.RemoveCustomDomain(ctxWithBearer(t, tok), &userv1.RemoveCustomDomainRequest{
			DomainId: placeholder.ID,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "custom domain provisioning is in progress") {
			t.Fatalf("RemoveCustomDomain within grace: want FailedPrecondition('custom domain provisioning is in progress'), got %v", err)
		}
		if _, getErr := store.GetDomainByID(context.Background(), placeholder.ID); getErr != nil {
			t.Fatalf("placeholder must remain intact when RemoveCustomDomain is rejected within grace: %v", getErr)
		}

		// Past grace (3m): RemoveCustomDomain MUST succeed and delete the abandoned placeholder.
		clockMu.Lock()
		curNow = baseNow.Add(3 * time.Minute)
		clockMu.Unlock()
		if _, err := client.RemoveCustomDomain(ctxWithBearer(t, tok), &userv1.RemoveCustomDomainRequest{
			DomainId: placeholder.ID,
		}); err != nil {
			t.Fatalf("RemoveCustomDomain after grace: want nil, got %v", err)
		}
		if _, getErr := store.GetDomainByID(context.Background(), placeholder.ID); !isNotFoundErr(getErr) {
			t.Fatalf("placeholder must be deleted after post-grace RemoveCustomDomain, err=%v", getErr)
		}
	})

	t.Run("Phase3ErrDomainGoneWithCleanupFailureHandsOffToPollerRemoving", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Gone Handoff Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "gone-handoff",
			ConfigJson: sampleSiteConfigJSON(t, "GoneHandoff", "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		cf.onCreateCustom = func(host string) {
			// Simulate post-grace RemoveCustomDomain deleting the placeholder during Phase 2,
			// followed by a transient Cloudflare error during cleanupCF.
			store.mu.Lock()
			for id, d := range store.domains {
				if d.Host == host {
					delete(store.domains, id)
				}
			}
			store.mu.Unlock()
			cf.mu.Lock()
			cf.deleteCustomErr = errors.New("cf 503 cleanup failed")
			cf.mu.Unlock()
		}

		_, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
			Hostname: "gone-handoff.verified.test",
		})
		cf.onCreateCustom = nil
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Phase 3 errDomainGone: want FailedPrecondition, got %v", err)
		}

		var handoffRow *db.Domain
		store.mu.Lock()
		for _, d := range store.domains {
			if d.Kind == DomainKindCustom {
				cp := d
				handoffRow = &cp
			}
		}
		store.mu.Unlock()
		if handoffRow == nil || handoffRow.Status != DomainStatusRemoving ||
			handoffRow.CfCustomHostnameID == nil || *handoffRow.CfCustomHostnameID != "mock-ch-gone-handoff.verified.test" {
			t.Fatalf("Phase 3 errDomainGone + cleanupCF failure must hand off createdCFID to REMOVING row, got %+v", handoffRow)
		}

		// CF recovers -> poller cleans up createdCFID and removes the row.
		cf.mu.Lock()
		cf.deleteCustomErr = nil
		cf.mu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())

		if _, getErr := store.GetDomainByID(context.Background(), handoffRow.ID); !isNotFoundErr(getErr) {
			t.Fatalf("poller must clean up handed-off REMOVING row, err=%v", getErr)
		}
	})

	t.Run("Phase3ErrDomainRemovingWithCleanupFailureHandsOffToNewRemovingRow", func(t *testing.T) {
		cf := newSpyCFClient("shops.nexusmart.localhost")
		srv, client, store, users, _ := startDomainBufconn(t, Options{
			Cloudflare: cf,
			CFMode:     "mock",
		})

		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, "Removing Handoff Shop", ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, tok), &userv1.SaveSiteDraftRequest{
			Subdomain:  "rem-handoff",
			ConfigJson: sampleSiteConfigJSON(t, "RemHandoff", "Hero"),
		}); err != nil {
			t.Fatalf("SaveSiteDraft: %v", err)
		}

		var origReservedID string
		cf.onCreateCustom = func(host string) {
			// Simulate concurrent RemoveCustomDomain marking reserved row REMOVING (still cfID=nil),
			// while cleanupCF hits a transient CF 503.
			store.mu.Lock()
			for id, d := range store.domains {
				if d.Host == host {
					origReservedID = id
					d.Status = DomainStatusRemoving
					store.domains[id] = d
				}
			}
			store.mu.Unlock()
			cf.mu.Lock()
			cf.deleteCustomErr = errors.New("cf 503 cleanup failed")
			cf.mu.Unlock()
		}

		_, err := client.AddCustomDomain(ctxWithBearer(t, tok), &userv1.AddCustomDomainRequest{
			Hostname: "rem-handoff.verified.test",
		})
		cf.onCreateCustom = nil
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Phase 3 errDomainRemoving: want FailedPrecondition, got %v", err)
		}

		// Simulate the concurrent RemoveCustomDomain finishing its DeleteDomain(origReservedID):
		// it MUST NOT wipe the handed-off REMOVING row carrying createdCFID!
		if err := store.DeleteDomain(context.Background(), origReservedID); err != nil {
			t.Fatalf("simulated RemoveCustomDomain DeleteDomain(origReservedID): %v", err)
		}

		var handoffRow *db.Domain
		store.mu.Lock()
		for _, d := range store.domains {
			if d.Kind == DomainKindCustom {
				cp := d
				handoffRow = &cp
			}
		}
		store.mu.Unlock()
		if handoffRow == nil || handoffRow.ID == origReservedID || handoffRow.Status != DomainStatusRemoving ||
			handoffRow.CfCustomHostnameID == nil || *handoffRow.CfCustomHostnameID != "mock-ch-rem-handoff.verified.test" {
			t.Fatalf("Phase 3 errDomainRemoving + cleanupCF failure must hand off createdCFID to fresh REMOVING row, got %+v (origReservedID=%q)", handoffRow, origReservedID)
		}

		// CF recovers -> poller cleans up createdCFID and deletes row.
		cf.mu.Lock()
		cf.deleteCustomErr = nil
		cf.mu.Unlock()
		srv.PollCustomDomainsOnce(context.Background())

		if _, getErr := store.GetDomainByID(context.Background(), handoffRow.ID); !isNotFoundErr(getErr) {
			t.Fatalf("poller must clean up handed-off REMOVING row, err=%v", getErr)
		}
	})
}
