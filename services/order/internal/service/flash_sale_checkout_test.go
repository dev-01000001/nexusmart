package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/store"
)

type orderingProductClient struct {
	*mockProductClient
	mu          sync.Mutex
	orderLog    *[]string
	logMu       *sync.Mutex
	reserveReqs []*productv1.ReserveStockRequest
	stockRemain *atomic.Int32
}

func (c *orderingProductClient) ReserveStock(ctx context.Context, req *productv1.ReserveStockRequest, opts ...grpc.CallOption) (*productv1.ReserveStockResponse, error) {
	if c.logMu != nil && c.orderLog != nil {
		c.logMu.Lock()
		*c.orderLog = append(*c.orderLog, "product.ReserveStock")
		c.logMu.Unlock()
	}
	c.mu.Lock()
	c.reserveReqs = append(c.reserveReqs, req)
	c.mu.Unlock()
	if c.stockRemain != nil {
		for _, it := range req.GetItems() {
			if c.stockRemain.Add(-it.GetQuantity()) < 0 {
				c.stockRemain.Add(it.GetQuantity())
				return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK")
			}
		}
	}
	return c.mockProductClient.ReserveStock(ctx, req, opts...)
}

func (c *orderingProductClient) ReleaseStock(ctx context.Context, req *productv1.ReleaseStockRequest, opts ...grpc.CallOption) (*productv1.ReleaseStockResponse, error) {
	if c.stockRemain != nil {
		c.stockRemain.Add(1)
	}
	return c.mockProductClient.ReleaseStock(ctx, req, opts...)
}

type orderingPromoClient struct {
	*mockPromotionClient
	orderLog  *[]string
	logMu     *sync.Mutex
	slotQuota *atomic.Int32
}

func (c *orderingPromoClient) ReservePromotion(ctx context.Context, req *promotionv1.ReservePromotionRequest, opts ...grpc.CallOption) (*promotionv1.ReservePromotionResponse, error) {
	if c.logMu != nil && c.orderLog != nil {
		c.logMu.Lock()
		*c.orderLog = append(*c.orderLog, "promotion.ReservePromotion")
		c.logMu.Unlock()
	}
	if c.slotQuota != nil {
		for _, it := range req.GetFlashSaleItems() {
			if c.slotQuota.Add(-it.GetQuantity()) < 0 {
				c.slotQuota.Add(it.GetQuantity())
				return nil, status.Error(codes.ResourceExhausted, "FLASH_SALE_SOLD_OUT")
			}
		}
	}
	return c.mockPromotionClient.ReservePromotion(ctx, req, opts...)
}

func newFlashCheckoutEnv(availStock int32, slotQuota, stockRemain *atomic.Int32) (*OrderService, *mockUserClient, *orderingProductClient, *orderingPromoClient, *[]string, *sync.Mutex) {
	st := store.NewMemoryStore()
	u := newMockUserClient()
	baseProd := newMockProductClient()
	baseProd.skus["sku-flash"] = &productv1.SKU{
		Id:                "sku-flash",
		ProductId:         "prod-flash",
		ShopId:            "shop-1",
		ProductName:       "Flash Deal Earbuds",
		Name:              "Black",
		PriceVnd:          500000,
		Stock:             availStock,
		ReservedStock:     0,
		AvailableStock:    availStock,
		Version:           1,
		WeightGrams:       200,
		CommissionRateBps: 500,
		IsActive:          true,
	}
	ca := newMockCartClient()
	basePromo := &mockPromotionClient{
		flashSales: []*promotionv1.FlashSale{
			{
				Id: "fs-window-1",
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-1", SkuId: "sku-flash", ShopId: "shop-1", FlashPriceVnd: 199000, TotalSlots: 20, RemainingSlots: 20},
				},
			},
		},
		breakdown: &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:               "shop-1",
					SubtotalVnd:          500000,
					FlashSaleDiscountVnd: 301000,
					ShippingFeeVnd:       30000,
				},
			},
		},
	}
	py := &mockPaymentClient{}
	sh := newMockShippingClient()

	var logMu sync.Mutex
	var orderLog []string

	ordProd := &orderingProductClient{
		mockProductClient: baseProd,
		orderLog:          &orderLog,
		logMu:             &logMu,
		stockRemain:       stockRemain,
	}
	ordPromo := &orderingPromoClient{
		mockPromotionClient: basePromo,
		orderLog:            &orderLog,
		logMu:               &logMu,
		slotQuota:           slotQuota,
	}

	cfg := config.Config{
		ServiceName:               "order",
		CODMaxRejectedCount:       3,
		FlashPaymentExpiryMinutes: 5,
		RewardCoinsBPS:            100,
		GRPCCallTimeout:           5 * time.Second,
	}
	bundle := &clients.Bundle{
		User:      u,
		Product:   ordProd,
		Cart:      ca,
		Promotion: ordPromo,
		Payment:   py,
		Shipping:  sh,
	}
	log := logger.New("order-flash-test", logger.WithWriter(io.Discard))
	return NewOrderService(cfg, st, bundle, log), u, ordProd, ordPromo, &orderLog, &logMu
}

func TestCreateCheckout_FlashSale_MainSagaOrderAndNoPreLock(t *testing.T) {
	// 1. AvailableStock == 0 rejects even with FlashSaleItemId (no phase-0b flash_sale_reserved_stock bypass).
	svcZero, _, prodZero, promoZero, _, _ := newFlashCheckoutEnv(0, nil, nil)
	ctx := ctxWithUser("buyer-1", "buyer", "")
	_, err := svcZero.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-zero-avail",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash", Quantity: 1, FlashSaleItemId: "fsi-1"},
		},
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("zero available stock: want FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}
	if len(prodZero.reserveReqs) != 0 || len(promoZero.reserveCalls) != 0 {
		t.Fatalf("pre-check should reject before RPC reservations: prod=%d promo=%d", len(prodZero.reserveReqs), len(promoZero.reserveCalls))
	}

	// 2. COD rejected for Flash Sale; VIETQR follows main saga order: ReserveStock -> ReservePromotion (service token).
	svc, _, prod, promo, orderLog, logMu := newFlashCheckoutEnv(20, nil, nil)
	_, err = svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-cod",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash", Quantity: 1, FlashSaleItemId: "fsi-1"},
		},
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_SALE_COD_NOT_ALLOWED") {
		t.Fatalf("COD on flash sale: want FailedPrecondition FLASH_SALE_COD_NOT_ALLOWED, got %v", err)
	}

	promo.breakdown.Shops[0].SubtotalVnd = 1000000
	promo.breakdown.Shops[0].FlashSaleDiscountVnd = 602000
	resp, err := svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-ok",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash", Quantity: 2, FlashSaleItemId: "fsi-1"},
		},
	})
	if err != nil {
		t.Fatalf("flash sale VIETQR CreateCheckout failed: %v", err)
	}
	if len(resp.GetOrders()) != 1 {
		t.Fatalf("expected 1 order, got %d", len(resp.GetOrders()))
	}
	if len(prod.reserveReqs) != 1 || len(prod.reserveReqs[0].GetItems()) != 1 {
		t.Fatalf("expected 1 ReserveStock call with 1 item, got %+v", prod.reserveReqs)
	}
	if item := prod.reserveReqs[0].GetItems()[0]; item.GetSkuId() != "sku-flash" || item.GetQuantity() != 2 || item.GetFlashSaleItemId() != "" {
		t.Fatalf("ReserveStock item mismatch (must not set flash_sale_item_id): got %+v", item)
	}
	logMu.Lock()
	gotOrder := append([]string(nil), (*orderLog)...)
	logMu.Unlock()
	if len(gotOrder) != 2 || gotOrder[0] != "product.ReserveStock" || gotOrder[1] != "promotion.ReservePromotion" {
		t.Fatalf("expected main saga order [product.ReserveStock, promotion.ReservePromotion], got %v", gotOrder)
	}
}

func TestCreateCheckout_FlashSale_PromotionExhaustedCompensatesStock(t *testing.T) {
	svc, _, prod, promo, _, _ := newFlashCheckoutEnv(20, nil, nil)
	promo.reserveErr = status.Error(codes.ResourceExhausted, "FLASH_SALE_SOLD_OUT")

	_, err := svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-sold-out",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash", Quantity: 1, FlashSaleItemId: "fsi-1"},
		},
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("want ResourceExhausted, got %v", err)
	}
	if len(prod.reserveCalls) != 1 || len(prod.releaseCalls) != 1 {
		t.Fatalf("expected 1 ReserveStock + 1 compensating ReleaseStock, got reserve=%d release=%d",
			len(prod.reserveCalls), len(prod.releaseCalls))
	}
	if prod.releaseCalls[0] != prod.reserveCalls[0] {
		t.Fatalf("ReleaseStock order_id=%q != ReserveStock order_id=%q", prod.releaseCalls[0], prod.reserveCalls[0])
	}
}

func TestCreateCheckout_FlashSale_100ConcurrentBuyersFor20Slots(t *testing.T) {
	const (
		totalSlots  = 20
		concurrency = 100
	)
	var slotQuota, stockRemain atomic.Int32
	slotQuota.Store(totalSlots)
	stockRemain.Store(concurrency)

	svc, u, prod, promo, _, _ := newFlashCheckoutEnv(concurrency, &slotQuota, &stockRemain)

	for i := 0; i < concurrency; i++ {
		buyerID := fmt.Sprintf("flash-buyer-%03d", i)
		addrID := fmt.Sprintf("addr-%03d", i)
		u.addresses[addrID] = &userv1.Address{
			Id:             addrID,
			UserId:         buyerID,
			RecipientName:  buyerID,
			RecipientPhone: "0901234567",
			ProvinceCode:   "79",
			ProvinceName:   "Ho Chi Minh",
			WardCode:       "26734",
			WardName:       "Ben Nghe",
			StreetAddress:  "123 Le Loi",
		}
	}

	var (
		wg         sync.WaitGroup
		startCh    = make(chan struct{})
		wins       atomic.Int32
		exhausted  atomic.Int32
		unexpected atomic.Int32
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			buyerID := fmt.Sprintf("flash-buyer-%03d", idx)
			addrID := fmt.Sprintf("addr-%03d", idx)
			_, err := svc.CreateCheckout(ctxWithUser(buyerID, "buyer", ""), &orderv1.CreateCheckoutRequest{
				IdempotencyKey: fmt.Sprintf("flash-conc-key-%03d", idx),
				AddressId:      addrID,
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				Items: []*orderv1.CheckoutItemInput{
					{SkuId: "sku-flash", Quantity: 1, FlashSaleItemId: "fsi-1"},
				},
			})
			if err == nil {
				wins.Add(1)
				return
			}
			if status.Code(err) == codes.ResourceExhausted {
				exhausted.Add(1)
				return
			}
			unexpected.Add(1)
		}(i)
	}
	close(startCh)
	wg.Wait()

	if unexpected.Load() != 0 {
		t.Fatalf("unexpected non-ResourceExhausted errors: %d", unexpected.Load())
	}
	if wins.Load() != totalSlots {
		t.Fatalf("expected EXACTLY %d flash sale winners, got %d", totalSlots, wins.Load())
	}
	if exhausted.Load() != concurrency-totalSlots {
		t.Fatalf("expected EXACTLY %d ResourceExhausted rejections, got %d", concurrency-totalSlots, exhausted.Load())
	}
	if len(prod.releaseCalls) != concurrency-totalSlots {
		t.Fatalf("expected EXACTLY %d compensating ReleaseStock calls, got %d", concurrency-totalSlots, len(prod.releaseCalls))
	}
	if len(promo.releaseCalls) != 0 {
		t.Fatalf("expected 0 compensating ReleasePromotion calls, got %d", len(promo.releaseCalls))
	}
	if slotQuota.Load() != 0 || stockRemain.Load() != concurrency-totalSlots {
		t.Fatalf("expected slotQuota=0 stockRemain=%d (zero leaked stock), got slots=%d stock=%d",
			concurrency-totalSlots, slotQuota.Load(), stockRemain.Load())
	}
}
