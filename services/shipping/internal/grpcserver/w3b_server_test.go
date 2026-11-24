package grpcserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubShopPickup struct {
	prov string
	ward string
	err  error
}

func (s stubShopPickup) GetShopPickupCodes(_ context.Context, _ string) (string, string, error) {
	return s.prov, s.ward, s.err
}

type memQuoteCache struct {
	entries map[string][]carrier.Quote
	gets    int
	sets    int
}

func newMemQuoteCache() *memQuoteCache {
	return &memQuoteCache{entries: make(map[string][]carrier.Quote)}
}

func (c *memQuoteCache) key(req carrier.FeeRequest) string {
	return req.FromProvinceCode + ":" + req.FromWardCode + ":" + req.ToProvinceCode + ":" + req.ToWardCode + ":" + string(rune(req.WeightGrams))
}

func (c *memQuoteCache) Get(_ context.Context, req carrier.FeeRequest) ([]carrier.Quote, bool) {
	c.gets++
	q, ok := c.entries[c.key(req)]
	if !ok {
		return nil, false
	}
	cp := make([]carrier.Quote, len(q))
	copy(cp, q)
	return cp, true
}

func (c *memQuoteCache) Set(_ context.Context, req carrier.FeeRequest, quotes []carrier.Quote) {
	c.sets++
	cp := make([]carrier.Quote, len(quotes))
	copy(cp, quotes)
	c.entries[c.key(req)] = cp
}

func TestShippingGRPC_W3B_E16_E17_PublicCalculateFeeAndCODSupported(t *testing.T) {
	repo := newStubRepo()
	_, _ = repo.UpsertShopShippingSettings(context.Background(), &store.ShopShippingSettings{
		ShopID:              "shop-e17",
		EnabledCarrierCodes: []string{carrier.CodeGHN, carrier.CodeMockExpress},
		DefaultWeightGrams:  800,
	})
	qc := newMemQuoteCache()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)),
		carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
		carrier.NewMockExpressAdapter(),
	)
	svc := service.NewShippingService(repo, agg, qc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetShopPickupResolver(stubShopPickup{prov: "79", ward: "26740"})
	srv := New(svc)

	// 1. E16: ListWards maps cod_supported=false for Hoang Sa (20333) and true for mainland Da Nang wards.
	dnWards, err := srv.ListWards(context.Background(), &shippingv1.ListWardsRequest{ProvinceCode: "48"})
	if err != nil {
		t.Fatalf("ListWards(48): %v", err)
	}
	foundHoangSa := false
	foundHaiChau := false
	for _, w := range dnWards.GetWards() {
		if w.GetCode() == "20333" {
			foundHoangSa = true
			if w.GetCodSupported() {
				t.Fatalf("E16: Hoang Sa (20333) must have cod_supported=false")
			}
		}
		if w.GetCode() == "20242" {
			foundHaiChau = true
			if !w.GetCodSupported() {
				t.Fatalf("E16: Hai Chau (20242) must have cod_supported=true")
			}
		}
	}
	if !foundHoangSa || !foundHaiChau {
		t.Fatalf("expected Hoang Sa (20333) and Hai Chau (20242) in Da Nang wards (foundHS=%v foundHC=%v)", foundHoangSa, foundHaiChau)
	}

	// 2. E17: Anonymous CalculateFee with province only (empty to_ward_code) uses representative ward & sets is_estimated=true.
	estResp, err := srv.CalculateFee(context.Background(), &shippingv1.CalculateFeeRequest{
		ShopId:         "shop-e17",
		ToProvinceCode: "48", // Da Nang (must NOT pick Hoang Sa as representative!)
		ToWardCode:     "",
		WeightGrams:    500,
	})
	if err != nil {
		t.Fatalf("E17 anonymous province-only CalculateFee: %v", err)
	}
	if !estResp.GetIsEstimated() {
		t.Fatalf("E17: province-only CalculateFee must return is_estimated=true")
	}
	if len(estResp.GetQuotes()) == 0 {
		t.Fatalf("E17: expected non-empty quotes for province-only CalculateFee")
	}
	for _, q := range estResp.GetQuotes() {
		if !q.GetCodSupported() {
			t.Fatalf("E17/E16: representative ward for Da Nang must be COD-supported mainland ward, got quote=%+v", q)
		}
	}

	// 3. E16: Explicit Hoang Sa (20333) sets is_estimated=false and cod_supported=false on every quote (incl. cache hit).
	for attempt := 1; attempt <= 2; attempt++ {
		hsResp, err := srv.CalculateFee(context.Background(), &shippingv1.CalculateFeeRequest{
			ShopId:         "shop-e17",
			ToProvinceCode: "48",
			ToWardCode:     "20333",
			WeightGrams:    500,
		})
		if err != nil {
			t.Fatalf("attempt %d CalculateFee(Hoang Sa): %v", attempt, err)
		}
		if hsResp.GetIsEstimated() {
			t.Fatalf("attempt %d explicit ward must return is_estimated=false", attempt)
		}
		for _, q := range hsResp.GetQuotes() {
			if q.GetCodSupported() {
				t.Fatalf("E16 attempt %d: Hoang Sa quote must have cod_supported=false, got %+v", attempt, q)
			}
		}
	}
	if qc.sets == 0 || qc.gets < 2 {
		t.Fatalf("E17: expected quote cache to be used, gets=%d sets=%d", qc.gets, qc.sets)
	}

	// 4. E17: fallback_weight_item_qty adds shop default_weight_grams * qty internally for anonymous PDP callers.
	baseResp, err := srv.CalculateFee(context.Background(), &shippingv1.CalculateFeeRequest{
		ShopId:         "shop-e17",
		ToProvinceCode: "79",
		ToWardCode:     "26740",
		WeightGrams:    200,
	})
	if err != nil {
		t.Fatalf("base CalculateFee: %v", err)
	}
	fbResp, err := srv.CalculateFee(context.Background(), &shippingv1.CalculateFeeRequest{
		ShopId:                "shop-e17",
		ToProvinceCode:        "79",
		ToWardCode:            "26740",
		WeightGrams:           200,
		FallbackWeightItemQty: 3, // + 3 * 800g = 2600g total
	})
	if err != nil {
		t.Fatalf("E17 fallback_weight_item_qty CalculateFee: %v", err)
	}
	if fbResp.GetQuotes()[0].GetFeeVnd() <= baseResp.GetQuotes()[0].GetFeeVnd() {
		t.Fatalf("E17: fallback_weight_item_qty=3 (2600g) must produce higher fee than 200g: base=%d fallback=%d",
			baseResp.GetQuotes()[0].GetFeeVnd(), fbResp.GetQuotes()[0].GetFeeVnd())
	}

	// 5. E17: Shop pickup validation failure must NOT leak warehouse province/ward code in error message.
	svcBadPickup := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svcBadPickup.SetShopPickupResolver(stubShopPickup{prov: "99", ward: "99999"})
	srvBadPickup := New(svcBadPickup)
	_, err = srvBadPickup.CalculateFee(context.Background(), &shippingv1.CalculateFeeRequest{
		ShopId:         "shop-e17",
		ToProvinceCode: "79",
		ToWardCode:     "26740",
		WeightGrams:    500,
	})
	if err == nil {
		t.Fatalf("expected error for invalid shop pickup address")
	}
	if strings.Contains(err.Error(), "99999") || strings.Contains(err.Error(), `"99"`) {
		t.Fatalf("E17: CalculateFee error must NOT leak shop warehouse codes, got %q", err.Error())
	}
}

func TestShippingGRPC_W3B_E18_CreateShipmentQueueAndRBAC(t *testing.T) {
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	svcCtx := ctxWithService("order-service")

	// 1. Service token remains strictly rejected on CreateShipment (only seller/admin JWT).
	repo := newStubRepo()
	c503 := &failingCreateCarrier{
		Carrier:   carrier.NewMockExpressAdapter(),
		createErr: errors.New("carrier 503 service unavailable"),
	}
	svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), c503), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	req := &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-e18-grpc-1",
		ShopId:      "shop-1",
		BuyerId:     "buyer-1",
		CarrierCode: carrier.CodeMockExpress,
		WeightGrams: 500,
	}
	if _, err := srv.CreateShipment(svcCtx, req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("E18: service token must remain PermissionDenied on CreateShipment, got %v", err)
	}

	// 2. Transient 503 persists job and returns queued=true, job_id != "".
	resp1, err := srv.CreateShipment(sellerCtx, req)
	if err != nil {
		t.Fatalf("E18: transient 503 CreateShipment must queue and return nil error, got %v", err)
	}
	if !resp1.GetQueued() || resp1.GetJobId() == "" || resp1.GetShipment() != nil {
		t.Fatalf("E18: expected queued=true with job_id and nil shipment, got %+v", resp1)
	}

	// 3. Repeated CreateShipment while queued is idempotent (same job_id).
	resp2, err := srv.CreateShipment(sellerCtx, req)
	if err != nil || !resp2.GetQueued() || resp2.GetJobId() != resp1.GetJobId() {
		t.Fatalf("E18: repeated CreateShipment while queued must return same job_id=%s, got resp=%+v err=%v", resp1.GetJobId(), resp2, err)
	}

	// 4. 4xx carrier rejection fails immediately with FailedPrecondition and creates 0 jobs.
	repo4xx := newStubRepo()
	c4xx := &failingCreateCarrier{
		Carrier:   carrier.NewMockExpressAdapter(),
		createErr: &carrier.NonRetryableError{Err: errors.New("400 bad address")},
	}
	srv4xx := New(service.NewShippingService(repo4xx, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), c4xx), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))))
	if _, err := srv4xx.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
		OrderId: "ord-e18-4xx", ShopId: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("E18: 4xx rejection must return FailedPrecondition immediately, got %v", err)
	}
	if len(repo4xx.jobs) != 0 {
		t.Fatalf("E18: 4xx rejection must persist 0 retry jobs, got %d", len(repo4xx.jobs))
	}

	// 5. E18: CancelShipment RPC (FORWARD) cancels active pending/processing job while preserving NotFound
	// when no shipment row exists yet; RETURN cancel must leave FORWARD job untouched.
	if _, err := srv.CancelShipment(svcCtx, &shippingv1.CancelShipmentRequest{
		OrderId:   "ord-e18-grpc-1",
		Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
		Reason:    "return_cancel_should_not_touch_forward_job",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("RETURN CancelShipment without shipment must return NotFound, got %v", err)
	}
	if j, err := repo.GetShipmentCreateJobByID(context.Background(), resp1.GetJobId()); err != nil || j.Status != store.JobStatusPending {
		t.Fatalf("RETURN CancelShipment must NOT cancel FORWARD job, got job=%+v err=%v", j, err)
	}
	if _, err := srv.CancelShipment(svcCtx, &shippingv1.CancelShipmentRequest{
		OrderId:   "ord-e18-grpc-1",
		Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
		Reason:    "order_cancelled_compensation",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("FORWARD CancelShipment without shipment must preserve NotFound, got %v", err)
	}
	if j, err := repo.GetShipmentCreateJobByID(context.Background(), resp1.GetJobId()); err != nil || j.Status != store.JobStatusCancelled {
		t.Fatalf("FORWARD CancelShipment RPC must cancel active job, got job=%+v err=%v", j, err)
	}
}
