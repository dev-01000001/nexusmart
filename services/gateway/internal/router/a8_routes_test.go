package router

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type a8OrderClient struct {
	orderv1.OrderServiceClient
	mu                 sync.Mutex
	lookupCalls        int
	lastLookupReq      *orderv1.LookupOrderRequest
	lastLookupMD       metadata.MD
	lookupErr          error
	updateAddrCalls    int
	lastUpdateAddrReq  *orderv1.UpdateOrderAddressRequest
	lastUpdateAddrMD   metadata.MD
	updateAddrErr      error
	lastListOrdersReq  *orderv1.ListOrdersRequest
	lastBatchShipReq   *orderv1.BatchShipOrdersRequest
	lastPickingListReq *orderv1.GetPickingListRequest
}

func (o *a8OrderClient) LookupOrder(ctx context.Context, in *orderv1.LookupOrderRequest, _ ...grpc.CallOption) (*orderv1.LookupOrderResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	o.mu.Lock()
	o.lookupCalls++
	o.lastLookupReq = in
	o.lastLookupMD = md.Copy()
	err := o.lookupErr
	o.mu.Unlock()
	if err != nil {
		return nil, err
	}
	now := timestamppb.Now()
	return &orderv1.LookupOrderResponse{
		Order: &orderv1.PublicOrderSummary{
			OrderCode:            in.GetOrderCode(),
			Status:               orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
			PaymentMethod:        orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			PaymentStatus:        "PENDING",
			SubtotalVnd:          250000,
			ShippingFeeVnd:       25000,
			TotalPayableVnd:      275000,
			CarrierCode:          "mock_express",
			TrackingCode:         "ME-TRACK-1",
			ProvinceName:         "Hà Nội",
			WardName:             "Phường Hoàn Kiếm",
			RecipientNameMasked:  "N*** A",
			RecipientPhoneMasked: "090****567",
			Items: []*orderv1.PublicOrderItem{
				{ProductName: "Áo thun", SkuName: "Trắng / M", Quantity: 1, LineTotalVnd: 250000},
			},
			Timeline: []*orderv1.PublicOrderTimelineEvent{
				{ToStatus: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, CreatedAt: now},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}, nil
}

func (o *a8OrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	return &orderv1.GetOrderResponse{
		Order: &orderv1.Order{
			Id:        in.GetOrderId(),
			OrderCode: "NM-GET-1",
			BuyerId:   "buyer-1",
			ShopId:    "shop-1",
			Status:    orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
		},
	}, nil
}

func (o *a8OrderClient) UpdateOrderAddress(ctx context.Context, in *orderv1.UpdateOrderAddressRequest, _ ...grpc.CallOption) (*orderv1.UpdateOrderAddressResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	o.mu.Lock()
	o.updateAddrCalls++
	o.lastUpdateAddrReq = in
	o.lastUpdateAddrMD = md.Copy()
	err := o.updateAddrErr
	o.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &orderv1.UpdateOrderAddressResponse{
		Order: &orderv1.Order{
			Id:              in.GetOrderId(),
			OrderCode:       "NM-ADDR-1",
			BuyerId:         "buyer-1",
			ShopId:          "shop-1",
			Status:          orderv1.OrderStatus_ORDER_STATUS_PREPARING,
			PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			ShippingFeeVnd:  35000,
			TotalPayableVnd: 285000,
			CommissionVnd:   15000, // must be zeroed for buyer
			PaymentFeeVnd:   5000,  // must be zeroed for buyer
		},
		OldShippingFeeVnd:       20000,
		NewShippingFeeVnd:       35000,
		FeeDeltaVnd:             15000,
		OldTotalPayableVnd:      270000,
		NewTotalPayableVnd:      285000,
		PreviewOnly:             in.GetPreviewOnly(),
		FeeAdjusted:             !in.GetPreviewOnly(),
		RequiresFeeConfirmation: in.GetPreviewOnly(),
		BlockedPrepaidIncrease:  false,
		ShipmentRecreated:       !in.GetPreviewOnly(),
		ShipmentQueued:          false,
		ShipmentJobId:           "",
		ResolvedAddress: &orderv1.OrderAddressSnapshot{
			RecipientName:  "Nguyen Van A",
			RecipientPhone: "0901234567",
			ProvinceCode:   "79",
			ProvinceName:   "TP. Hồ Chí Minh",
			WardCode:       "26734",
			WardName:       "Phường Bến Nghé",
			StreetAddress:  "12 Lê Lợi",
		},
	}, nil
}

func (o *a8OrderClient) ListOrders(_ context.Context, in *orderv1.ListOrdersRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersResponse, error) {
	o.mu.Lock()
	o.lastListOrdersReq = in
	o.mu.Unlock()
	return &orderv1.ListOrdersResponse{
		Orders: []*orderv1.Order{
			{
				Id:            "ord-s-1",
				OrderCode:     "NM-S-1",
				ShopId:        in.GetShopId(),
				Status:        orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
				PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				CarrierCode:   "ghn",
			},
		},
		Total: 25,
		StatusCounts: []*orderv1.OrderStatusCount{
			{Status: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, Count: 10},
			{Status: orderv1.OrderStatus_ORDER_STATUS_PREPARING, Count: 15},
		},
	}, nil
}

func (o *a8OrderClient) BatchShipOrders(_ context.Context, in *orderv1.BatchShipOrdersRequest, _ ...grpc.CallOption) (*orderv1.BatchShipOrdersResponse, error) {
	o.mu.Lock()
	o.lastBatchShipReq = in
	o.mu.Unlock()
	return &orderv1.BatchShipOrdersResponse{
		Results: []*orderv1.BatchShipOrderResult{
			{
				OrderId:       "ord-1",
				OrderCode:     "NM-1",
				Outcome:       orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_QUEUED,
				ShipmentJobId: "job-ship-1",
			},
			{
				OrderId:    "ord-2",
				OrderCode:  "NM-2",
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: "ORDER_CANCELLED",
				Message:    "order already cancelled",
			},
		},
		QueuedCount:  1,
		SkippedCount: 1,
	}, nil
}

func (o *a8OrderClient) GetPickingList(_ context.Context, in *orderv1.GetPickingListRequest, _ ...grpc.CallOption) (*orderv1.GetPickingListResponse, error) {
	o.mu.Lock()
	o.lastPickingListReq = in
	o.mu.Unlock()
	return &orderv1.GetPickingListResponse{
		Items: []*orderv1.PickingListItem{
			{
				SkuId:         "sku-1",
				ProductId:     "prod-1",
				ProductName:   "Áo Polo",
				SkuName:       "Đen / L",
				TotalQuantity: 4,
				OrderCount:    2,
				OrderCodes:    []string{"NM-1", "NM-2"},
			},
		},
		OrderCount: 2,
		TotalUnits: 4,
	}, nil
}

type a8ProductClient struct {
	productv1.ProductServiceClient
	mu                 sync.Mutex
	lastStockHistReq   *productv1.ListStockHistoryRequest
	lastListProdsReq   *productv1.ListProductsRequest
	listProdsCalls     int
	lastCreateImport   *productv1.CreateProductImportJobRequest
	createImportCalls  int
	createImportErr    error
	jobByID            map[string]*productv1.ProductImportJob
	importRows         []*productv1.ProductImportRow
	lastListRowsReq    *productv1.ListProductImportRowsRequest
	lastConfirmJobReq  *productv1.ConfirmProductImportJobRequest
	confirmJobErr      error
	confirmAlreadyDone bool
}

func (p *a8ProductClient) ListProducts(_ context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	p.mu.Lock()
	p.listProdsCalls++
	p.lastListProdsReq = in
	p.mu.Unlock()
	return &productv1.ListProductsResponse{
		Products: []*productv1.Product{},
		Total:    0,
	}, nil
}

func (p *a8ProductClient) ListStockHistory(_ context.Context, in *productv1.ListStockHistoryRequest, _ ...grpc.CallOption) (*productv1.ListStockHistoryResponse, error) {
	p.mu.Lock()
	p.lastStockHistReq = in
	p.mu.Unlock()
	return &productv1.ListStockHistoryResponse{
		Items: []*productv1.StockHistoryItem{
			{
				Id:             "sh-1",
				SkuId:          in.GetSkuId(),
				ProductId:      "prod-1",
				ShopId:         in.GetShopId(),
				ProductName:    "Áo thun",
				SkuCode:        "AT-M",
				SkuName:        "M",
				ChangeType:     "MANUAL_UPDATE",
				Delta:          10,
				StockBefore:    50,
				StockAfter:     60,
				ReservedBefore: 0,
				ReservedAfter:  0,
				ActorId:        "seller-1",
				Reason:         "restock",
				CreatedAt:      timestamppb.Now(),
			},
		},
		Total: 1,
	}, nil
}

func (p *a8ProductClient) CreateProductImportJob(_ context.Context, in *productv1.CreateProductImportJobRequest, _ ...grpc.CallOption) (*productv1.CreateProductImportJobResponse, error) {
	p.mu.Lock()
	p.createImportCalls++
	p.lastCreateImport = in
	err := p.createImportErr
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	now := timestamppb.Now()
	return &productv1.CreateProductImportJobResponse{
		Job: &productv1.ProductImportJob{
			Id:        "imp-job-1",
			ShopId:    "shop-1",
			CreatedBy: "seller-1",
			Filename:  in.GetFilename(),
			Status:    productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_QUEUED,
			TotalRows: 2,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}, nil
}

func (p *a8ProductClient) GetProductImportJob(_ context.Context, in *productv1.GetProductImportJobRequest, _ ...grpc.CallOption) (*productv1.GetProductImportJobResponse, error) {
	p.mu.Lock()
	job, ok := p.jobByID[in.GetJobId()]
	p.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "import job not found")
	}
	return &productv1.GetProductImportJobResponse{Job: job}, nil
}

func (p *a8ProductClient) ListProductImportRows(_ context.Context, in *productv1.ListProductImportRowsRequest, _ ...grpc.CallOption) (*productv1.ListProductImportRowsResponse, error) {
	p.mu.Lock()
	p.lastListRowsReq = in
	all := append([]*productv1.ProductImportRow(nil), p.importRows...)
	p.mu.Unlock()

	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize <= 0 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	if start >= len(all) {
		return &productv1.ListProductImportRowsResponse{Rows: []*productv1.ProductImportRow{}, Total: int64(len(all))}, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return &productv1.ListProductImportRowsResponse{
		Rows:  all[start:end],
		Total: int64(len(all)),
	}, nil
}

func (p *a8ProductClient) ConfirmProductImportJob(_ context.Context, in *productv1.ConfirmProductImportJobRequest, _ ...grpc.CallOption) (*productv1.ConfirmProductImportJobResponse, error) {
	p.mu.Lock()
	p.lastConfirmJobReq = in
	err := p.confirmJobErr
	already := p.confirmAlreadyDone
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	now := timestamppb.Now()
	return &productv1.ConfirmProductImportJobResponse{
		Job: &productv1.ProductImportJob{
			Id:          in.GetJobId(),
			ShopId:      "shop-1",
			Status:      productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_CONFIRMING,
			ValidRows:   10,
			ConfirmedAt: now,
		},
		AlreadyConfirmed: already,
	}, nil
}

type a8RateLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newA8RateLimiter() *a8RateLimiter {
	return &a8RateLimiter{counts: make(map[string]int)}
}

func (r *a8RateLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (middleware.RateLimitResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts[key] >= limit {
		return middleware.RateLimitResult{Allowed: false, Remaining: 0, RetryAfter: 30 * time.Second}, nil
	}
	r.counts[key]++
	return middleware.RateLimitResult{Allowed: true, Remaining: limit - r.counts[key]}, nil
}

type a8Harness struct {
	handler     http.Handler
	authMgr     *auth.Manager
	order       *a8OrderClient
	product     *a8ProductClient
	labelSrv    *httptest.Server
	mu          sync.Mutex
	lastLblPath string
	lastLblHdr  http.Header
}

func newA8Harness(t *testing.T, withLookupLimit bool) *a8Harness {
	t.Helper()
	authMgr, err := auth.NewManager("w3b3-a8-gateway-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	hw := &a8Harness{
		authMgr: authMgr,
		order:   &a8OrderClient{},
		product: &a8ProductClient{
			jobByID: map[string]*productv1.ProductImportJob{
				"imp-job-1": {
					Id:          "imp-job-1",
					ShopId:      "shop-1",
					CreatedBy:   "seller-1",
					Filename:    "products.csv",
					Status:      productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY,
					TotalRows:   6,
					ValidRows:   0,
					InvalidRows: 6,
				},
				"imp-job-other": {
					Id:     "imp-job-other",
					ShopId: "shop-other",
					Status: productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY,
				},
			},
			importRows: []*productv1.ProductImportRow{
				{Id: "r1", JobId: "imp-job-1", RowNumber: 2, Status: productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID, ErrorCode: "UNKNOWN_CUSTOM_CODE", ErrorMessage: "=CMD|' /C calc'!A0", ProductName: "=CMD|calc", SkuCode: "SKU-1", PriceVnd: 100000, Stock: 5, WeightGrams: 300},
				{Id: "r2", JobId: "imp-job-1", RowNumber: 3, Status: productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID, ErrorCode: "INVALID_PRICE", ErrorMessage: "price_vnd must be > 0", ProductName: "+1+1", SkuCode: "SKU-2", PriceVnd: -10, Stock: 5, WeightGrams: 300},
				{Id: "r3", JobId: "imp-job-1", RowNumber: 4, Status: productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID, ErrorCode: "INVALID_PRODUCT_NAME", ErrorMessage: "product_name is required", ProductName: "-2+3", SkuCode: "SKU-3", PriceVnd: 50000, Stock: 5, WeightGrams: 300},
				{Id: "r4", JobId: "imp-job-1", RowNumber: 5, Status: productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID, ErrorCode: "BANNED_CONTENT", ErrorMessage: "matched banned keyword", ProductName: "@SUM(A1:A2)", SkuCode: "SKU-4", PriceVnd: 50000, Stock: 5, WeightGrams: 300},
				{Id: "r5", JobId: "imp-job-1", RowNumber: 6, Status: productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED, ErrorCode: "INTERNAL_ERROR", ErrorMessage: "db error", ProductName: "\t=1", SkuCode: "SKU-5", PriceVnd: 50000, Stock: 5, WeightGrams: 300},
				{Id: "r6", JobId: "imp-job-1", RowNumber: 7, Status: productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED, ErrorCode: "SHOP_SUSPENDED", ErrorMessage: "shop is suspended", ProductName: " =1", SkuCode: "SKU-6", PriceVnd: 50000, Stock: 5, WeightGrams: 300},
			},
		},
	}

	hw.labelSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hw.mu.Lock()
		hw.lastLblPath = r.URL.Path
		hw.lastLblHdr = r.Header.Clone()
		hw.mu.Unlock()
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="shipping-labels-batch.pdf"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-1.4\n%%EOF\n"))
	}))
	t.Cleanup(hw.labelSrv.Close)

	log := logger.New("a8-test", logger.WithWriter(io.Discard))
	lp, err := proxy.NewShippingLabelProxy(hw.labelSrv.URL, log)
	if err != nil {
		t.Fatalf("NewShippingLabelProxy: %v", err)
	}

	deps := Dependencies{
		ServiceName: "gateway-a8-test",
		Clients: &clients.Clients{
			Order:   hw.order,
			Product: hw.product,
			Timeout: 5 * time.Second,
		},
		AuthManager:           authMgr,
		ShippingLabelProxy:    lp,
		ProductImportMaxRows:  1000,
		ProductImportMaxBytes: 1048576,
		Logger:                log,
	}
	if withLookupLimit {
		deps.RateLimiter = newA8RateLimiter()
		deps.RateLimitOrderLookupPerMin = 10
		deps.RateLimitOrderLookupCodePer10Min = 5
	}
	hw.handler = New(deps)
	return hw
}

func (hw *a8Harness) tok(userID, role, shopID string) string {
	tok, _, _, _ := hw.authMgr.SignAccessToken(userID, role, shopID)
	return tok
}

func TestA8_OrderLookupPublicAndDualRateLimitG7(t *testing.T) {
	hw := newA8Harness(t, true)
	buyerTok := hw.tok("buyer-1", auth.RoleBuyer, "")

	// 1. Public lookup succeeds without JWT, does NOT forward JWT, and omits private PII/fees.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":"NM-PUB-1","phone":"0901234567"}`))
	req.RemoteAddr = "198.51.100.1:4000"
	req.Header.Set("Authorization", "Bearer "+buyerTok) // even if caller attaches token, must strip!
	rec := httptest.NewRecorder()
	hw.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /orders/lookup: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if vals := hw.order.lastLookupMD.Get(auth.MDAuthorization); len(vals) != 0 {
		t.Fatalf("POST /orders/lookup must not forward Authorization, got %v", vals)
	}
	bodyStr := rec.Body.String()
	for _, forbidden := range []string{"street_address", "buyer_id", "label_url", "commission_vnd", "payment_fee_vnd", `"email"`, `"note"`} {
		if strings.Contains(bodyStr, forbidden) {
			t.Fatalf("POST /orders/lookup response leaked %s: %s", forbidden, bodyStr)
		}
	}

	// 2. GET /api/v1/orders/lookup must NOT be allowed, while GET /api/v1/orders/ord-1 still works.
	recGetLookup := httptest.NewRecorder()
	hw.handler.ServeHTTP(recGetLookup, httptest.NewRequest(http.MethodGet, "/api/v1/orders/lookup", nil))
	if recGetLookup.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /orders/lookup: want 405, got %d", recGetLookup.Code)
	}
	reqGetOrd := httptest.NewRequest(http.MethodGet, "/api/v1/orders/ord-1", nil)
	reqGetOrd.Header.Set("Authorization", "Bearer "+buyerTok)
	recGetOrd := httptest.NewRecorder()
	hw.handler.ServeHTTP(recGetOrd, reqGetOrd)
	if recGetOrd.Code != http.StatusOK {
		t.Fatalf("GET /orders/ord-1: want 200, got %d (%s)", recGetOrd.Code, recGetOrd.Body.String())
	}

	// 3. Not found returns exact 404 ORDER_LOOKUP_NOT_FOUND.
	hw.order.mu.Lock()
	hw.order.lookupErr = status.Error(codes.NotFound, "ORDER_LOOKUP_NOT_FOUND: not found")
	hw.order.mu.Unlock()
	reqNF := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":"NM-NONE","phone":"0900000000"}`))
	reqNF.RemoteAddr = "198.51.100.2:4000"
	recNF := httptest.NewRecorder()
	hw.handler.ServeHTTP(recNF, reqNF)
	if recNF.Code != http.StatusNotFound || !strings.Contains(recNF.Body.String(), `"code":"ORDER_LOOKUP_NOT_FOUND"`) {
		t.Fatalf("lookup not found: want 404 ORDER_LOOKUP_NOT_FOUND, got %d (%s)", recNF.Code, recNF.Body.String())
	}
	hw.order.mu.Lock()
	hw.order.lookupErr = nil
	hw.order.mu.Unlock()

	// 4. Attack G7: 11th call from same IP in 1 min -> 429 RATE_LIMIT_EXCEEDED + Retry-After.
	for i := 1; i <= 10; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(fmt.Sprintf(`{"order_code":"NM-IP-%d","phone":"0901234567"}`, i)))
		r.RemoteAddr = "198.51.100.50:4000"
		w := httptest.NewRecorder()
		hw.handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("same-IP lookup %d: want 200, got %d", i, w.Code)
		}
	}
	r11 := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":"NM-IP-11","phone":"0901234567"}`))
	r11.RemoteAddr = "198.51.100.50:4000"
	w11 := httptest.NewRecorder()
	hw.handler.ServeHTTP(w11, r11)
	if w11.Code != http.StatusTooManyRequests || !strings.Contains(w11.Body.String(), `"code":"RATE_LIMIT_EXCEEDED"`) {
		t.Fatalf("11th same-IP lookup: want 429 RATE_LIMIT_EXCEEDED, got %d (%s)", w11.Code, w11.Body.String())
	}
	if sec, err := strconv.Atoi(w11.Header().Get("Retry-After")); err != nil || sec < 1 {
		t.Fatalf("11th same-IP Retry-After = %q, want >= 1", w11.Header().Get("Retry-After"))
	}

	// 5. Attack G7: 6th call for same order_code from different IPs in 10 min -> 429 RATE_LIMIT_EXCEEDED + Retry-After.
	for i := 1; i <= 5; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":"nm-same-code","phone":"0901234567"}`))
		r.RemoteAddr = fmt.Sprintf("203.0.113.%d:4000", i)
		w := httptest.NewRecorder()
		hw.handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("same-code lookup %d: want 200, got %d", i, w.Code)
		}
	}
	rCode6 := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":" NM-SAME-CODE ","phone":"0901234567"}`))
	rCode6.RemoteAddr = "203.0.113.6:4000"
	wCode6 := httptest.NewRecorder()
	hw.handler.ServeHTTP(wCode6, rCode6)
	if wCode6.Code != http.StatusTooManyRequests || !strings.Contains(wCode6.Body.String(), `"code":"RATE_LIMIT_EXCEEDED"`) {
		t.Fatalf("6th same-code lookup: want 429 RATE_LIMIT_EXCEEDED, got %d (%s)", wCode6.Code, wCode6.Body.String())
	}
	if sec, err := strconv.Atoi(wCode6.Header().Get("Retry-After")); err != nil || sec < 1 {
		t.Fatalf("6th same-code Retry-After = %q, want >= 1", wCode6.Header().Get("Retry-After"))
	}

	// 6. Attack G-a: bypass attempts (trailing data, duplicate JSON keys, mixed-case keys)
	// MUST ALL count against the exact order_code actually sent to LookupOrder.
	bypassBodies := []string{
		`{"order_code":"NM-BYPASS-1","phone":"0900000001"} x`,
		`{"order_code":"NM-DECOY","order_code":"NM-BYPASS-1","phone":"0900000001"}`,
		`{"ORDER_CODE":"nm-bypass-1","Phone":"0900000001"}`,
		`{"Order_Code":" nm-bypass-1 ","phone":"0900000001"} trailing`,
		`{"order_code":"decoy-2","Order_Code":"NM-BYPASS-1","phone":"0900000001"}`,
	}
	for i, payload := range bypassBodies {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(payload))
		r.RemoteAddr = fmt.Sprintf("203.0.114.%d:4000", i+1)
		w := httptest.NewRecorder()
		hw.handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("bypass variant %d (%s): want 200, got %d (%s)", i+1, payload, w.Code, w.Body.String())
		}
		if !strings.EqualFold(strings.TrimSpace(hw.order.lastLookupReq.GetOrderCode()), "NM-BYPASS-1") {
			t.Fatalf("bypass variant %d looked up unexpected order_code %q", i+1, hw.order.lastLookupReq.GetOrderCode())
		}
	}
	hw.order.mu.Lock()
	callsBefore6th := hw.order.lookupCalls
	hw.order.mu.Unlock()
	rBypass6 := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":"NM-BYPASS-1","phone":"0900000001"} trailing-garbage`))
	rBypass6.RemoteAddr = "203.0.114.6:4000"
	wBypass6 := httptest.NewRecorder()
	hw.handler.ServeHTTP(wBypass6, rBypass6)
	if wBypass6.Code != http.StatusTooManyRequests || !strings.Contains(wBypass6.Body.String(), `"code":"RATE_LIMIT_EXCEEDED"`) {
		t.Fatalf("6th lookup with trailing-garbage bypass: want 429 RATE_LIMIT_EXCEEDED, got %d (%s)", wBypass6.Code, wBypass6.Body.String())
	}
	hw.order.mu.Lock()
	callsAfter6th := hw.order.lookupCalls
	hw.order.mu.Unlock()
	if callsAfter6th != callsBefore6th {
		t.Fatalf("6th rate-limited bypass call must not reach Order.LookupOrder (before=%d after=%d)", callsBefore6th, callsAfter6th)
	}
}

func TestA8_OrderLookupStorefrontShopIDForwardingAndValidation(t *testing.T) {
	hw := newA8Harness(t, true)
	const shopUUID = "11111111-1111-4111-8111-111111111111"

	// 1. Valid storefront_shop_id UUID (trimmed) is forwarded to Order.LookupOrder.
	reqValid := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders/lookup",
		strings.NewReader(`{"order_code":"NM-SF-1","phone":"0901234567","storefront_shop_id":" `+shopUUID+` "}`),
	)
	reqValid.RemoteAddr = "198.51.100.70:4000"
	recValid := httptest.NewRecorder()
	hw.handler.ServeHTTP(recValid, reqValid)
	if recValid.Code != http.StatusOK {
		t.Fatalf("valid storefront_shop_id lookup: want 200, got %d (%s)", recValid.Code, recValid.Body.String())
	}
	if hw.order.lastLookupReq.GetStorefrontShopId() != shopUUID {
		t.Fatalf("forwarded storefront_shop_id = %q, want %q", hw.order.lastLookupReq.GetStorefrontShopId(), shopUUID)
	}

	// 2. Omitted storefront_shop_id forwards empty string (legacy behavior).
	reqEmpty := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders/lookup",
		strings.NewReader(`{"order_code":"NM-SF-2","phone":"0901234567"}`),
	)
	reqEmpty.RemoteAddr = "198.51.100.71:4000"
	recEmpty := httptest.NewRecorder()
	hw.handler.ServeHTTP(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusOK {
		t.Fatalf("empty storefront_shop_id lookup: want 200, got %d (%s)", recEmpty.Code, recEmpty.Body.String())
	}
	if hw.order.lastLookupReq.GetStorefrontShopId() != "" {
		t.Fatalf("forwarded storefront_shop_id = %q, want empty", hw.order.lastLookupReq.GetStorefrontShopId())
	}

	// 3. Invalid storefront_shop_id UUID -> 400 INVALID_ARGUMENT without calling Order.LookupOrder.
	hw.order.mu.Lock()
	callsBeforeInvalid := hw.order.lookupCalls
	hw.order.mu.Unlock()
	reqInvalid := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders/lookup",
		strings.NewReader(`{"order_code":"NM-SF-3","phone":"0901234567","storefront_shop_id":"not-a-uuid"}`),
	)
	reqInvalid.RemoteAddr = "198.51.100.72:4000"
	recInvalid := httptest.NewRecorder()
	hw.handler.ServeHTTP(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusBadRequest || !strings.Contains(recInvalid.Body.String(), "storefront_shop_id must be a UUID") {
		t.Fatalf("invalid storefront_shop_id: want 400 UUID error, got %d (%s)", recInvalid.Code, recInvalid.Body.String())
	}
	hw.order.mu.Lock()
	callsAfterInvalid := hw.order.lookupCalls
	hw.order.mu.Unlock()
	if callsAfterInvalid != callsBeforeInvalid {
		t.Fatalf("invalid storefront_shop_id must not invoke Order.LookupOrder (before=%d after=%d)", callsBeforeInvalid, callsAfterInvalid)
	}
}

func TestA8_UpdateOrderAddressAndFeeChangedQuoteG1G2G3(t *testing.T) {
	hw := newA8Harness(t, false)
	buyerTok := hw.tok("buyer-1", auth.RoleBuyer, "")

	// Anonymous -> 401.
	recAnon := httptest.NewRecorder()
	hw.handler.ServeHTTP(recAnon, httptest.NewRequest(http.MethodPatch, "/api/v1/orders/ord-1/address", strings.NewReader(`{"address_id":"a2"}`)))
	if recAnon.Code != http.StatusUnauthorized {
		t.Fatalf("anon PATCH /orders/ord-1/address: want 401, got %d", recAnon.Code)
	}

	// Authenticated apply forwards JWT + Idempotency-Key and zeroes buyer-hidden fee fields.
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/ord-1/address", strings.NewReader(`{"address_id":"a2","accept_fee_increase":true,"expected_new_shipping_fee_vnd":35000}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-addr-1")
	rec := httptest.NewRecorder()
	hw.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /orders/ord-1/address: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if hw.order.lastUpdateAddrReq.GetIdempotencyKey() != "idem-addr-1" ||
		hw.order.lastUpdateAddrReq.GetExpectedNewShippingFeeVnd() != 35000 ||
		!hw.order.lastUpdateAddrReq.GetAcceptFeeIncrease() {
		t.Fatalf("unexpected UpdateOrderAddressRequest: %+v", hw.order.lastUpdateAddrReq)
	}
	if vals := hw.order.lastUpdateAddrMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+buyerTok {
		t.Fatalf("expected JWT forwarded, got %v", vals)
	}
	if vals := hw.order.lastUpdateAddrMD.Get("idempotency-key"); len(vals) == 0 || vals[0] != "idem-addr-1" {
		t.Fatalf("expected idempotency-key metadata forwarded, got %v", vals)
	}
	if strings.Contains(rec.Body.String(), `"commission_vnd":15000`) || strings.Contains(rec.Body.String(), `"payment_fee_vnd":5000`) {
		t.Fatalf("PATCH /orders/ord-1/address leaked seller/admin fees to buyer: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"fee_delta_vnd":15000`) || !strings.Contains(rec.Body.String(), `"shipment_recreated":true`) {
		t.Fatalf("PATCH /orders/ord-1/address missing expected fields: %s", rec.Body.String())
	}

	// G-d: custom_address is removed (contract r2); missing address_id -> 400 without calling OrderService.
	hw.order.mu.Lock()
	addrCallsBefore := hw.order.updateAddrCalls
	hw.order.mu.Unlock()
	reqNoAddr := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/ord-1/address", strings.NewReader(`{"custom_address":{"street_address":"12 Le Loi"}}`))
	reqNoAddr.Header.Set("Authorization", "Bearer "+buyerTok)
	recNoAddr := httptest.NewRecorder()
	hw.handler.ServeHTTP(recNoAddr, reqNoAddr)
	if recNoAddr.Code != http.StatusBadRequest {
		t.Fatalf("missing address_id (custom_address only): want 400, got %d (%s)", recNoAddr.Code, recNoAddr.Body.String())
	}
	hw.order.mu.Lock()
	addrCallsAfter := hw.order.updateAddrCalls
	hw.order.mu.Unlock()
	if addrCallsAfter != addrCallsBefore {
		t.Fatalf("missing address_id must not call OrderService.UpdateOrderAddress")
	}

	// ADDRESS_FEE_CHANGED -> HTTP 409 with clean error.message (no JSON blob) AND quote object (G2 / G-b).
	hw.order.mu.Lock()
	hw.order.updateAddrErr = status.Error(codes.FailedPrecondition, `ADDRESS_FEE_CHANGED: {"old_shipping_fee_vnd":20000,"new_shipping_fee_vnd":42000,"fee_delta_vnd":22000,"new_total_payable_vnd":292000}`)
	hw.order.mu.Unlock()
	reqChg := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/ord-1/address", strings.NewReader(`{"address_id":"a2","accept_fee_increase":true,"expected_new_shipping_fee_vnd":35000}`))
	reqChg.Header.Set("Authorization", "Bearer "+buyerTok)
	recChg := httptest.NewRecorder()
	hw.handler.ServeHTTP(recChg, reqChg)
	if recChg.Code != http.StatusConflict {
		t.Fatalf("ADDRESS_FEE_CHANGED: want 409, got %d (%s)", recChg.Code, recChg.Body.String())
	}
	var chgResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Quote *struct {
			OldShippingFeeVND  int64 `json:"old_shipping_fee_vnd"`
			NewShippingFeeVND  int64 `json:"new_shipping_fee_vnd"`
			FeeDeltaVND        int64 `json:"fee_delta_vnd"`
			NewTotalPayableVND int64 `json:"new_total_payable_vnd"`
		} `json:"quote"`
	}
	if err := json.Unmarshal(recChg.Body.Bytes(), &chgResp); err != nil {
		t.Fatalf("decode ADDRESS_FEE_CHANGED: %v (%s)", err, recChg.Body.String())
	}
	if chgResp.Error.Code != "ADDRESS_FEE_CHANGED" || chgResp.Quote == nil ||
		chgResp.Quote.OldShippingFeeVND != 20000 || chgResp.Quote.NewShippingFeeVND != 42000 ||
		chgResp.Quote.FeeDeltaVND != 22000 || chgResp.Quote.NewTotalPayableVND != 292000 {
		t.Fatalf("unexpected ADDRESS_FEE_CHANGED body: %s", recChg.Body.String())
	}
	if strings.Contains(chgResp.Error.Message, "{") || strings.Contains(chgResp.Error.Message, "old_shipping_fee_vnd") {
		t.Fatalf("ADDRESS_FEE_CHANGED error.message must not contain raw JSON blob: %q", chgResp.Error.Message)
	}
}

func TestA8_SellerOrdersFiltersBatchShipPickingListAndBatchLabelsG4G5G8(t *testing.T) {
	hw := newA8Harness(t, false)
	sellerTok := hw.tok("seller-1", auth.RoleSeller, "shop-1")
	sellerNoShopTok := hw.tok("seller-empty", auth.RoleSeller, "")
	buyerTok := hw.tok("buyer-1", auth.RoleBuyer, "")
	opsTok := hw.tok("ops-1", auth.RoleOps, "")
	cskhTok := hw.tok("cskh-1", auth.RoleCSKH, "")
	modTok := hw.tok("mod-1", auth.RoleModerator, "")

	// 1. Extended GET /api/v1/seller/orders (G8)
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/seller/orders?statuses=CONFIRMED,PREPARING&carrier_code=ghn&payment_method=COD&keyword=NM-S&due_before=2026-11-30T00:00:00Z&include_status_counts=true&page=2&page_size=20", nil)
	reqList.Header.Set("Authorization", "Bearer "+sellerTok)
	recList := httptest.NewRecorder()
	hw.handler.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("GET /seller/orders extended: want 200, got %d (%s)", recList.Code, recList.Body.String())
	}
	lr := hw.order.lastListOrdersReq
	if len(lr.GetStatuses()) != 2 || lr.GetCarrierCode() != "ghn" || lr.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD ||
		lr.GetKeyword() != "NM-S" || lr.GetDueBefore() == nil || !lr.GetIncludeStatusCounts() || lr.GetShopId() != "shop-1" {
		t.Fatalf("unexpected forwarded ListOrdersRequest: %+v", lr)
	}
	if !strings.Contains(recList.Body.String(), `"status_counts"`) || !strings.Contains(recList.Body.String(), `"CONFIRMED"`) {
		t.Fatalf("GET /seller/orders missing status_counts: %s", recList.Body.String())
	}

	// 2. POST /api/v1/seller/orders/batch-ship & picking-list (G4)
	reqBS := httptest.NewRequest(http.MethodPost, "/api/v1/seller/orders/batch-ship", strings.NewReader(`{"order_ids":["ord-1","ord-2"],"carrier_code":"ghn","service_code":"STD"}`))
	reqBS.Header.Set("Authorization", "Bearer "+sellerTok)
	recBS := httptest.NewRecorder()
	hw.handler.ServeHTTP(recBS, reqBS)
	if recBS.Code != http.StatusOK || !strings.Contains(recBS.Body.String(), `"queued_count":1`) || !strings.Contains(recBS.Body.String(), `"ORDER_CANCELLED"`) {
		t.Fatalf("POST /seller/orders/batch-ship mismatch: code=%d body=%s", recBS.Code, recBS.Body.String())
	}

	reqPL := httptest.NewRequest(http.MethodPost, "/api/v1/seller/orders/picking-list", strings.NewReader(`{"order_ids":["ord-1","ord-2"]}`))
	reqPL.Header.Set("Authorization", "Bearer "+sellerTok)
	recPL := httptest.NewRecorder()
	hw.handler.ServeHTTP(recPL, reqPL)
	if recPL.Code != http.StatusOK || !strings.Contains(recPL.Body.String(), `"total_units":4`) || !strings.Contains(recPL.Body.String(), `"sku-1"`) {
		t.Fatalf("POST /seller/orders/picking-list mismatch: code=%d body=%s", recPL.Code, recPL.Body.String())
	}

	// Buyer blocked on seller batch-ship & picking-list.
	for _, p := range []string{"/api/v1/seller/orders/batch-ship", "/api/v1/seller/orders/picking-list"} {
		r := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{"order_ids":["ord-1"]}`))
		r.Header.Set("Authorization", "Bearer "+buyerTok)
		w := httptest.NewRecorder()
		hw.handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("buyer on %s: want 403, got %d", p, w.Code)
		}
	}

	// 3. POST /api/v1/shipping/labels/batch (G5): RBAC + strips X-User-*/X-Shop-Id + streams PDF.
	for _, tc := range []struct {
		name string
		tok  string
		want int
	}{
		{"anon", "", http.StatusUnauthorized},
		{"buyer", buyerTok, http.StatusForbidden},
		{"seller_no_shop", sellerNoShopTok, http.StatusForbidden},
		{"moderator", modTok, http.StatusForbidden},
		{"seller", sellerTok, http.StatusOK},
		{"ops", opsTok, http.StatusOK},
		{"cskh", cskhTok, http.StatusOK},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/shipping/labels/batch", strings.NewReader(`{"tracking_codes":["TRK-1"]}`))
		if tc.tok != "" {
			r.Header.Set("Authorization", "Bearer "+tc.tok)
		}
		r.Header.Set("X-User-Role", "super_admin")
		r.Header.Set("X-User-Id", "spoofed")
		r.Header.Set("X-Shop-Id", "spoofed-shop")
		w := httptest.NewRecorder()
		hw.handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("POST /shipping/labels/batch role=%s: want %d, got %d (%s)", tc.name, tc.want, w.Code, w.Body.String())
		}
		if tc.want == http.StatusOK {
			if w.Header().Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
				t.Fatalf("POST /shipping/labels/batch role=%s: expected PDF response, got ct=%q body=%q", tc.name, w.Header().Get("Content-Type"), w.Body.String())
			}
			hw.mu.Lock()
			path := hw.lastLblPath
			hdr := hw.lastLblHdr
			hw.mu.Unlock()
			if path != "/labels/batch" {
				t.Fatalf("upstream label path = %q, want /labels/batch", path)
			}
			for _, hName := range []string{"X-User-Role", "X-User-Id", "X-Shop-Id"} {
				if len(hdr.Values(hName)) != 0 {
					t.Fatalf("spoofed header %s leaked to shipping upstream: %v", hName, hdr.Values(hName))
				}
			}
			if hdr.Get("Authorization") != "Bearer "+tc.tok {
				t.Fatalf("Authorization not forwarded properly: got %q", hdr.Get("Authorization"))
			}
		}
	}
}

func TestA8_SellerStockHistoryAndCSVImportG6G8(t *testing.T) {
	hw := newA8Harness(t, false)
	sellerTok := hw.tok("seller-1", auth.RoleSeller, "shop-1")
	buyerTok := hw.tok("buyer-1", auth.RoleBuyer, "")

	// 1. Stock history (G8)
	reqSH := httptest.NewRequest(http.MethodGet, "/api/v1/seller/stock-history?product_id=prod-1&sku_id=sku-1&change_type=MANUAL_UPDATE&from_time=2026-11-01T00:00:00Z&to_time=2026-11-25T00:00:00Z", nil)
	reqSH.Header.Set("Authorization", "Bearer "+sellerTok)
	recSH := httptest.NewRecorder()
	hw.handler.ServeHTTP(recSH, reqSH)
	if recSH.Code != http.StatusOK || hw.product.lastStockHistReq.GetShopId() != "shop-1" || hw.product.lastStockHistReq.GetSkuId() != "sku-1" {
		t.Fatalf("GET /seller/stock-history mismatch: code=%d req=%+v body=%s", recSH.Code, hw.product.lastStockHistReq, recSH.Body.String())
	}

	reqSKUHist := httptest.NewRequest(http.MethodGet, "/api/v1/seller/skus/sku-99/stock-history", nil)
	reqSKUHist.Header.Set("Authorization", "Bearer "+sellerTok)
	recSKUHist := httptest.NewRecorder()
	hw.handler.ServeHTTP(recSKUHist, reqSKUHist)
	if recSKUHist.Code != http.StatusOK || hw.product.lastStockHistReq.GetSkuId() != "sku-99" || hw.product.lastStockHistReq.GetShopId() != "shop-1" {
		t.Fatalf("GET /seller/skus/sku-99/stock-history mismatch: code=%d req=%+v", recSKUHist.Code, hw.product.lastStockHistReq)
	}

	// 2. Import template (UTF-8 BOM CSV)
	reqTpl := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/template?category_id=cat-ao-1", nil)
	reqTpl.Header.Set("Authorization", "Bearer "+sellerTok)
	recTpl := httptest.NewRecorder()
	hw.handler.ServeHTTP(recTpl, reqTpl)
	if recTpl.Code != http.StatusOK || !strings.HasPrefix(recTpl.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("GET import/template: code=%d ct=%q", recTpl.Code, recTpl.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(recTpl.Body.Bytes(), []byte{0xEF, 0xBB, 0xBF}) || !strings.Contains(recTpl.Body.String(), "cat-ao-1") {
		t.Fatalf("template must start with UTF-8 BOM and contain category_id: %q", recTpl.Body.String())
	}

	// 3. POST /api/v1/seller/products/import (202): text/csv AND multipart/form-data.
	csvSample := "product_name,category_id,sku_code,price_vnd,stock,weight_grams\nAo Thun,cat-1,AT-01,150000,20,300\n"
	reqCSV := httptest.NewRequest(http.MethodPost, "/api/v1/seller/products/import?filename=batch1.csv", strings.NewReader(csvSample))
	reqCSV.Header.Set("Authorization", "Bearer "+sellerTok)
	reqCSV.Header.Set("Content-Type", "text/csv")
	recCSV := httptest.NewRecorder()
	hw.handler.ServeHTTP(recCSV, reqCSV)
	if recCSV.Code != http.StatusAccepted || !strings.Contains(recCSV.Body.String(), `"imp-job-1"`) {
		t.Fatalf("POST import text/csv: want 202, got %d (%s)", recCSV.Code, recCSV.Body.String())
	}

	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	part, _ := mw.CreateFormFile("file", "multi.csv")
	_, _ = part.Write([]byte(csvSample))
	_ = mw.Close()
	reqMP := httptest.NewRequest(http.MethodPost, "/api/v1/seller/products/import", &mpBuf)
	reqMP.Header.Set("Authorization", "Bearer "+sellerTok)
	reqMP.Header.Set("Content-Type", mw.FormDataContentType())
	recMP := httptest.NewRecorder()
	hw.handler.ServeHTTP(recMP, reqMP)
	if recMP.Code != http.StatusAccepted || hw.product.lastCreateImport.GetFilename() != "multi.csv" {
		t.Fatalf("POST import multipart: want 202 filename=multi.csv, got %d fn=%q (%s)", recMP.Code, hw.product.lastCreateImport.GetFilename(), recMP.Body.String())
	}

	// 4. Attack G6: Upload > 1 MiB (both text/csv and multipart) blocked cleanly without calling ProductService!
	callsBefore := hw.product.createImportCalls
	oversized := bytes.Repeat([]byte("a"), (1<<20)+1)
	reqBigRaw := httptest.NewRequest(http.MethodPost, "/api/v1/seller/products/import", bytes.NewReader(oversized))
	reqBigRaw.Header.Set("Authorization", "Bearer "+sellerTok)
	reqBigRaw.Header.Set("Content-Type", "text/csv")
	recBigRaw := httptest.NewRecorder()
	hw.handler.ServeHTTP(recBigRaw, reqBigRaw)
	if recBigRaw.Code != http.StatusRequestEntityTooLarge && recBigRaw.Code != http.StatusBadRequest {
		t.Fatalf("oversized raw CSV (>1MiB): want 413/400, got %d (%s)", recBigRaw.Code, recBigRaw.Body.String())
	}

	var bigMP bytes.Buffer
	bigMW := multipart.NewWriter(&bigMP)
	bigPart, _ := bigMW.CreateFormFile("file", "huge.csv")
	_, _ = bigPart.Write(oversized)
	_ = bigMW.Close()
	reqBigMP := httptest.NewRequest(http.MethodPost, "/api/v1/seller/products/import", &bigMP)
	reqBigMP.Header.Set("Authorization", "Bearer "+sellerTok)
	reqBigMP.Header.Set("Content-Type", bigMW.FormDataContentType())
	recBigMP := httptest.NewRecorder()
	hw.handler.ServeHTTP(recBigMP, reqBigMP)
	if recBigMP.Code != http.StatusRequestEntityTooLarge && recBigMP.Code != http.StatusBadRequest {
		t.Fatalf("oversized multipart CSV (>1MiB): want 413/400, got %d (%s)", recBigMP.Code, recBigMP.Body.String())
	}
	if hw.product.createImportCalls != callsBefore {
		t.Fatalf("oversized uploads must be rejected before calling ProductService, got %d extra calls", hw.product.createImportCalls-callsBefore)
	}

	// 5. GET job, GET rows, foreign shop blocked with 403.
	reqJob := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/imp-job-1", nil)
	reqJob.Header.Set("Authorization", "Bearer "+sellerTok)
	recJob := httptest.NewRecorder()
	hw.handler.ServeHTTP(recJob, reqJob)
	if recJob.Code != http.StatusOK || !strings.Contains(recJob.Body.String(), `"status":"READY"`) {
		t.Fatalf("GET import job: code=%d body=%s", recJob.Code, recJob.Body.String())
	}

	reqOtherJob := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/imp-job-other", nil)
	reqOtherJob.Header.Set("Authorization", "Bearer "+sellerTok)
	recOtherJob := httptest.NewRecorder()
	hw.handler.ServeHTTP(recOtherJob, reqOtherJob)
	if recOtherJob.Code != http.StatusForbidden {
		t.Fatalf("GET foreign shop import job: want 403, got %d", recOtherJob.Code)
	}

	reqRows := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/imp-job-1/rows?errors_only=true&page=1&page_size=20", nil)
	reqRows.Header.Set("Authorization", "Bearer "+sellerTok)
	recRows := httptest.NewRecorder()
	hw.handler.ServeHTTP(recRows, reqRows)
	if recRows.Code != http.StatusOK || !hw.product.lastListRowsReq.GetErrorsOnly() {
		t.Fatalf("GET import rows: code=%d req=%+v", recRows.Code, hw.product.lastListRowsReq)
	}

	// 6. Attack G6: errors.csv UTF-8 BOM + formula-injection prefix `'` on =CMD|..., +1+1, -2+3, @SUM, \t=1, " =1".
	reqErrCSV := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/imp-job-1/errors.csv", nil)
	reqErrCSV.Header.Set("Authorization", "Bearer "+sellerTok)
	recErrCSV := httptest.NewRecorder()
	hw.handler.ServeHTTP(recErrCSV, reqErrCSV)
	if recErrCSV.Code != http.StatusOK || !strings.HasPrefix(recErrCSV.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("GET errors.csv: code=%d ct=%q", recErrCSV.Code, recErrCSV.Header().Get("Content-Type"))
	}
	rawCSV := recErrCSV.Body.Bytes()
	if !bytes.HasPrefix(rawCSV, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("errors.csv must begin with UTF-8 BOM")
	}
	records, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rawCSV, []byte{0xEF, 0xBB, 0xBF}))).ReadAll()
	if err != nil {
		t.Fatalf("parse errors.csv: %v", err)
	}
	if len(records) != 7 { // 1 header + 6 rows
		t.Fatalf("expected 7 CSV records, got %d", len(records))
	}
	nameCol := -1
	errMsgCol := -1
	for i, col := range records[0] {
		if col == "product_name" {
			nameCol = i
		}
		if col == "error_message" {
			errMsgCol = i
		}
	}
	if nameCol < 0 || errMsgCol < 0 {
		t.Fatalf("errors.csv missing product_name/error_message headers: %v", records[0])
	}
	wantGuarded := []string{"'=CMD|calc", "'+1+1", "'-2+3", "'@SUM(A1:A2)", "'\t=1", "' =1"}
	for idx, want := range wantGuarded {
		got := records[idx+1][nameCol]
		if got != want {
			t.Fatalf("errors.csv row %d product_name = %q, want %q", idx+1, got, want)
		}
	}
	if got := records[1][errMsgCol]; got != "'=CMD|' /C calc'!A0" {
		t.Fatalf("errors.csv row 1 error_message = %q, want formula-prefixed", got)
	}

	// 7. Confirm job (200) & buyer blocked (403).
	reqConfirm := httptest.NewRequest(http.MethodPost, "/api/v1/seller/products/import/imp-job-1/confirm", nil)
	reqConfirm.Header.Set("Authorization", "Bearer "+sellerTok)
	recConfirm := httptest.NewRecorder()
	hw.handler.ServeHTTP(recConfirm, reqConfirm)
	if recConfirm.Code != http.StatusOK || !strings.Contains(recConfirm.Body.String(), `"already_confirmed":false`) {
		t.Fatalf("POST import confirm: code=%d body=%s", recConfirm.Code, recConfirm.Body.String())
	}

	reqBuyerImp := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/template", nil)
	reqBuyerImp.Header.Set("Authorization", "Bearer "+buyerTok)
	recBuyerImp := httptest.NewRecorder()
	hw.handler.ServeHTTP(recBuyerImp, reqBuyerImp)
	if recBuyerImp.Code != http.StatusForbidden {
		t.Fatalf("buyer on import/template: want 403, got %d", recBuyerImp.Code)
	}
}

func TestA8_ImportErrorsCSV_VietnameseMessagesAndRawValuesE1E2(t *testing.T) {
	hw := newA8Harness(t, false)
	sellerTok := hw.tok("seller-1", auth.RoleSeller, "shop-1")

	// 1. Verify all 18 known error_codes from docs/contracts.md produce the exact Vietnamese labels from web/src/lib/a8-types.ts.
	wantByCode := map[string]string{
		"INVALID_PRODUCT_NAME":  "Tên sản phẩm không hợp lệ hoặc bị bỏ trống.",
		"INVALID_PRODUCT_TEXT":  "Mô tả hoặc thông tin văn bản của sản phẩm không hợp lệ.",
		"INVALID_SKU_CODE":      "Mã SKU không hợp lệ hoặc bị bỏ trống.",
		"INVALID_SKU_TEXT":      "Tên phân loại SKU không hợp lệ.",
		"DUPLICATE_SKU_IN_FILE": "Mã SKU bị trùng lặp trong cùng tệp CSV.",
		"DUPLICATE_SKU_IN_SHOP": "Mã SKU đã tồn tại trong gian hàng.",
		"INVALID_CATEGORY":      "Mã danh mục không hợp lệ hoặc bị bỏ trống.",
		"CATEGORY_NOT_FOUND":    "Không tìm thấy danh mục tương ứng trên hệ thống.",
		"PROHIBITED_CATEGORY":   "Danh mục này thuộc nhóm hạn chế hoặc cấm kinh doanh.",
		"INVALID_PRICE":         "Giá bán không hợp lệ (phải là số nguyên VND lớn hơn 0).",
		"INVALID_STOCK":         "Số lượng tồn kho không hợp lệ (phải là số nguyên không âm).",
		"INVALID_WEIGHT":        "Khối lượng sản phẩm (gram) không hợp lệ.",
		"INVALID_SLUG":          "Đường dẫn (slug) sản phẩm không hợp lệ.",
		"DUPLICATE_SLUG":        "Đường dẫn (slug) sản phẩm đã tồn tại hoặc bị trùng lặp.",
		"INVALID_ATTRIBUTES":    "Thuộc tính phân loại sản phẩm không đúng định dạng.",
		"BANNED_CONTENT":        "Nội dung sản phẩm chứa từ khóa vi phạm quy định kiểm duyệt.",
		"SHOP_SUSPENDED":        "Gian hàng đang tạm ngưng hoạt động nên không thể tạo sản phẩm mới.",
		"INTERNAL_ERROR":        "Lỗi hệ thống tạm thời khi xử lý dòng này.",
	}

	testRows := []*productv1.ProductImportRow{
		// Known code + raw_row_json with non-numeric "abc" price & negative stock & non-numeric weight (parsed proto fields are 0).
		{
			Id:           "e1-raw",
			JobId:        "imp-job-1",
			RowNumber:    2,
			Status:       productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID,
			ErrorCode:    "INVALID_PRICE",
			ErrorMessage: "price_vnd must be > 0",
			ProductName:  "fallback-name",
			SkuCode:      "FALLBACK-SKU",
			CategoryId:   "fallback-cat",
			PriceVnd:     0,
			Stock:        0,
			WeightGrams:  0,
			RawRowJson:   `{"product_name":"Áo thun gốc","sku_code":"SKU-GOC-1","category_id":"cat-goc","price_vnd":"abc","stock":"-5","weight_grams":"nang"}`,
		},
		// Unknown error_code -> preserves raw error_message verbatim (and JSON /rows also stays unchanged).
		{
			Id:           "e1-unknown",
			JobId:        "imp-job-1",
			RowNumber:    3,
			Status:       productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID,
			ErrorCode:    "CUSTOM_FUTURE_CODE",
			ErrorMessage: "raw technical message preserved",
			ProductName:  "SP Unknown",
			SkuCode:      "SKU-UNK",
			CategoryId:   "cat-1",
			PriceVnd:     150000,
			Stock:        10,
			WeightGrams:  200,
		},
		// Formula injection cell inside raw_row_json -> MUST still be neutralized with leading single quote.
		{
			Id:           "e2-formula",
			JobId:        "imp-job-1",
			RowNumber:    4,
			Status:       productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID,
			ErrorCode:    "DUPLICATE_SKU_IN_FILE",
			ErrorMessage: `duplicate sku_code "SKU-DUP" in CSV file`,
			ProductName:  "ignored",
			SkuCode:      "ignored",
			CategoryId:   "cat-1",
			PriceVnd:     0,
			Stock:        0,
			WeightGrams:  0,
			RawRowJson:   `{"product_name":"=CMD|calc","sku_code":"SKU-DUP","category_id":"cat-1","price_vnd":"=1+1","stock":"5","weight_grams":"300"}`,
		},
		// Corrupted raw_row_json -> must NOT panic or 500; falls back cleanly to parsed fields.
		{
			Id:           "e2-corrupt",
			JobId:        "imp-job-1",
			RowNumber:    5,
			Status:       productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED,
			ErrorCode:    "SHOP_SUSPENDED",
			ErrorMessage: "shop is suspended and cannot create products",
			ProductName:  "SP Fallback",
			SkuCode:      "SKU-FB",
			CategoryId:   "cat-fb",
			PriceVnd:     99000,
			Stock:        7,
			WeightGrams:  250,
			RawRowJson:   `{"product_name": broken-json`,
		},
	}

	// Append remaining known codes so every single one of the 18 codes is verified end-to-end through the HTTP handler.
	rowNum := int32(6)
	covered := map[string]bool{"INVALID_PRICE": true, "DUPLICATE_SKU_IN_FILE": true, "SHOP_SUSPENDED": true}
	for code := range wantByCode {
		if covered[code] {
			continue
		}
		testRows = append(testRows, &productv1.ProductImportRow{
			Id:           "code-" + code,
			JobId:        "imp-job-1",
			RowNumber:    rowNum,
			Status:       productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID,
			ErrorCode:    code,
			ErrorMessage: "english technical error for " + code,
			ProductName:  "SP " + code,
			SkuCode:      "SKU-" + code,
			CategoryId:   "cat-1",
			PriceVnd:     100000,
			Stock:        5,
			WeightGrams:  300,
		})
		rowNum++
	}

	hw.product.mu.Lock()
	hw.product.importRows = testRows
	hw.product.mu.Unlock()

	// Verify API JSON /rows keeps raw English error_message unchanged (E1).
	reqJSONRows := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/imp-job-1/rows?errors_only=true&page=1&page_size=50", nil)
	reqJSONRows.Header.Set("Authorization", "Bearer "+sellerTok)
	recJSONRows := httptest.NewRecorder()
	hw.handler.ServeHTTP(recJSONRows, reqJSONRows)
	var rowsWrap struct {
		Items []struct {
			ErrorMessage string `json:"error_message"`
		} `json:"items"`
	}
	if recJSONRows.Code != http.StatusOK || json.Unmarshal(recJSONRows.Body.Bytes(), &rowsWrap) != nil ||
		len(rowsWrap.Items) == 0 || rowsWrap.Items[0].ErrorMessage != "price_vnd must be > 0" {
		t.Fatalf("GET /rows must keep raw English error_message unchanged: code=%d body=%s", recJSONRows.Code, recJSONRows.Body.String())
	}

	// Download errors.csv and parse all rows.
	reqCSV := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products/import/imp-job-1/errors.csv", nil)
	reqCSV.Header.Set("Authorization", "Bearer "+sellerTok)
	recCSV := httptest.NewRecorder()
	hw.handler.ServeHTTP(recCSV, reqCSV)
	if recCSV.Code != http.StatusOK {
		t.Fatalf("GET errors.csv: want 200, got %d (%s)", recCSV.Code, recCSV.Body.String())
	}
	rawCSV := recCSV.Body.Bytes()
	if !bytes.HasPrefix(rawCSV, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("errors.csv missing UTF-8 BOM")
	}
	records, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rawCSV, []byte{0xEF, 0xBB, 0xBF}))).ReadAll()
	if err != nil {
		t.Fatalf("parse errors.csv: %v", err)
	}
	if len(records) != len(testRows)+1 {
		t.Fatalf("errors.csv record count = %d, want %d", len(records), len(testRows)+1)
	}

	colIdx := make(map[string]int, len(records[0]))
	for i, h := range records[0] {
		colIdx[h] = i
	}
	cell := func(rowIdx int, col string) string {
		idx, ok := colIdx[col]
		if !ok {
			t.Fatalf("missing column %q in header %v", col, records[0])
		}
		return records[rowIdx][idx]
	}

	// Row 1 (e1-raw): Vietnamese message + raw values from raw_row_json ("abc", "-5", "nang").
	if got := cell(1, "error_message"); got != wantByCode["INVALID_PRICE"] {
		t.Fatalf("row 1 error_message = %q, want %q", got, wantByCode["INVALID_PRICE"])
	}
	if got := cell(1, "product_name"); got != "Áo thun gốc" {
		t.Fatalf("row 1 product_name = %q, want raw value", got)
	}
	if got := cell(1, "sku_code"); got != "SKU-GOC-1" {
		t.Fatalf("row 1 sku_code = %q, want SKU-GOC-1", got)
	}
	if got := cell(1, "category_id"); got != "cat-goc" {
		t.Fatalf("row 1 category_id = %q, want cat-goc", got)
	}
	if got := cell(1, "price_vnd"); got != "abc" {
		t.Fatalf("row 1 price_vnd = %q, want raw \"abc\"", got)
	}
	if got := cell(1, "stock"); got != "-5" && got != "'-5" {
		t.Fatalf("row 1 stock = %q, want raw \"-5\" (or guarded \"'-5\")", got)
	}
	if got := cell(1, "weight_grams"); got != "nang" {
		t.Fatalf("row 1 weight_grams = %q, want raw \"nang\"", got)
	}

	// Row 2 (e1-unknown): unknown code preserves raw error_message verbatim.
	if got := cell(2, "error_message"); got != "raw technical message preserved" {
		t.Fatalf("row 2 unknown code error_message = %q, want verbatim fallback", got)
	}

	// Row 3 (e2-formula): known Vietnamese message + formula cells from raw_row_json neutralized with '.
	if got := cell(3, "error_message"); got != wantByCode["DUPLICATE_SKU_IN_FILE"] {
		t.Fatalf("row 3 error_message = %q, want %q", got, wantByCode["DUPLICATE_SKU_IN_FILE"])
	}
	if got := cell(3, "product_name"); got != "'=CMD|calc" {
		t.Fatalf("row 3 product_name = %q, want formula-guarded '=CMD|calc", got)
	}
	if got := cell(3, "price_vnd"); got != "'=1+1" {
		t.Fatalf("row 3 price_vnd = %q, want formula-guarded '=1+1", got)
	}

	// Row 4 (e2-corrupt): corrupted raw_row_json falls back cleanly to parsed fields without crashing.
	if got := cell(4, "error_message"); got != wantByCode["SHOP_SUSPENDED"] {
		t.Fatalf("row 4 error_message = %q, want %q", got, wantByCode["SHOP_SUSPENDED"])
	}
	if got := cell(4, "product_name"); got != "SP Fallback" {
		t.Fatalf("row 4 fallback product_name = %q, want SP Fallback", got)
	}
	if got := cell(4, "price_vnd"); got != "99000" {
		t.Fatalf("row 4 fallback price_vnd = %q, want 99000", got)
	}
	if got := cell(4, "stock"); got != "7" {
		t.Fatalf("row 4 fallback stock = %q, want 7", got)
	}
	if got := cell(4, "weight_grams"); got != "250" {
		t.Fatalf("row 4 fallback weight_grams = %q, want 250", got)
	}

	// All 18 known error_codes verified across all exported rows.
	seenCodes := make(map[string]bool, len(wantByCode))
	for i := 1; i < len(records); i++ {
		code := cell(i, "error_code")
		if wantMsg, ok := wantByCode[code]; ok {
			seenCodes[code] = true
			if got := cell(i, "error_message"); got != wantMsg {
				t.Fatalf("code %s error_message = %q, want %q", code, got, wantMsg)
			}
		}
	}
	if len(seenCodes) != len(wantByCode) {
		t.Fatalf("verified %d known codes, want %d", len(seenCodes), len(wantByCode))
	}
}

func TestA8_SellerProducts_LowStockQueryParsing(t *testing.T) {
	hw := newA8Harness(t, false)
	sellerTok := hw.tok("seller-1", auth.RoleSeller, "shop-1")

	// 1. low_stock=true -> forwarded as LowStock=true with verified shop_id.
	reqTrue := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products?low_stock=true&keyword=SKU-1&category_id=cat-1&status=APPROVED", nil)
	reqTrue.Header.Set("Authorization", "Bearer "+sellerTok)
	recTrue := httptest.NewRecorder()
	hw.handler.ServeHTTP(recTrue, reqTrue)
	if recTrue.Code != http.StatusOK || !hw.product.lastListProdsReq.GetLowStock() ||
		hw.product.lastListProdsReq.GetShopId() != "shop-1" ||
		hw.product.lastListProdsReq.GetKeyword() != "SKU-1" {
		t.Fatalf("GET /seller/products?low_stock=true: code=%d req=%+v body=%s", recTrue.Code, hw.product.lastListProdsReq, recTrue.Body.String())
	}

	// 2. low_stock=false and omitted -> LowStock=false.
	for _, qs := range []string{"?low_stock=false", ""} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products"+qs, nil)
		req.Header.Set("Authorization", "Bearer "+sellerTok)
		rec := httptest.NewRecorder()
		hw.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || hw.product.lastListProdsReq.GetLowStock() {
			t.Fatalf("GET /seller/products%s: want 200 LowStock=false, got code=%d req=%+v", qs, rec.Code, hw.product.lastListProdsReq)
		}
	}

	// 3. Invalid low_stock values -> 400 INVALID_ARGUMENT without calling ProductService.
	callsBefore := hw.product.listProdsCalls
	for _, bad := range []string{"yes", "2", "notabool", "tru"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/seller/products?low_stock="+bad, nil)
		req.Header.Set("Authorization", "Bearer "+sellerTok)
		rec := httptest.NewRecorder()
		hw.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_ARGUMENT") {
			t.Fatalf("GET /seller/products?low_stock=%s: want 400 INVALID_ARGUMENT, got %d (%s)", bad, rec.Code, rec.Body.String())
		}
	}
	if hw.product.listProdsCalls != callsBefore {
		t.Fatalf("invalid low_stock must not call ProductService, got %d extra calls", hw.product.listProdsCalls-callsBefore)
	}

	// 4. Public GET /api/v1/products?low_stock=true ignores low_stock (always sends LowStock=false).
	reqPub := httptest.NewRequest(http.MethodGet, "/api/v1/products?low_stock=true", nil)
	recPub := httptest.NewRecorder()
	hw.handler.ServeHTTP(recPub, reqPub)
	if recPub.Code != http.StatusOK || hw.product.lastListProdsReq.GetLowStock() {
		t.Fatalf("GET /products?low_stock=true: want 200 LowStock=false, got code=%d req=%+v", recPub.Code, hw.product.lastListProdsReq)
	}
}
