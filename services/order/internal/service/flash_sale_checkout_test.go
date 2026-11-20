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
	flashRemain *atomic.Int32
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
	if c.flashRemain != nil {
		for _, it := range req.GetItems() {
			if it.GetFlashSaleItemId() == "" {
				return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK")
			}
			if c.flashRemain.Add(-it.GetQuantity()) < 0 {
				c.flashRemain.Add(it.GetQuantity())
				return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_FLASH_STOCK")
			}
		}
	}
	return c.mockProductClient.ReserveStock(ctx, req, opts...)
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

func newFlashCheckoutEnv(slotQuota, flashRemain *atomic.Int32) (*OrderService, *mockUserClient, *orderingProductClient, *orderingPromoClient, *[]string, *sync.Mutex) {
	st := store.NewMemoryStore()
	u := newMockUserClient()
	baseProd := newMockProductClient()
	baseProd.skus["sku-flash-only"] = &productv1.SKU{
		Id:                     "sku-flash-only",
		ProductId:              "prod-flash",
		ShopId:                 "shop-1",
		ProductName:            "Flash Deal Earbuds",
		Name:                   "Black",
		PriceVnd:               500000,
		Stock:                  20,
		ReservedStock:          0,
		AvailableStock:         0,  // All regular stock sold out!
		FlashSaleReservedStock: 20, // Only flash-reserved stock remains.
		Version:                1,
		WeightGrams:            200,
		CommissionRateBps:      500,
		IsActive:               true,
	}
	ca := newMockCartClient()
	basePromo := &mockPromotionClient{
		flashSales: []*promotionv1.FlashSale{
			{
				Id: "fs-window-1",
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-1", SkuId: "sku-flash-only", FlashPriceVnd: 199000, TotalSlots: 20, RemainingSlots: 20},
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
		flashRemain:       flashRemain,
	}
	ordPromo := &orderingPromoClient{
		mockPromotionClient: basePromo,
		orderLog:            &orderLog,
		logMu:               &logMu,
		slotQuota:           slotQuota,
	}

	cfg := config.Config{
		ServiceName:          "order",
		CODMaxVND:            5000000,
		CODMaxRejectedCount:  3,
		PaymentExpiryMinutes: 15,
		DefaultCommissionBPS: 500,
		RewardCoinsBPS:       100,
		GRPCCallTimeout:      5 * time.Second,
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

func TestCreateCheckout_FlashSalePassThroughAndRegularStockIsolation(t *testing.T) {
	svc, _, prod, promo, orderLog, logMu := newFlashCheckoutEnv(nil, nil)
	ctx := ctxWithUser("buyer-1", "buyer", "")

	// 1. Regular checkout on sku-flash-only (available=0, flash_reserved=20) MUST fail with INSUFFICIENT_STOCK.
	_, err := svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-regular-blocked",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash-only", Quantity: 1},
		},
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("regular checkout against flash-only stock: want FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}
	if len(prod.reserveReqs) != 0 || len(promo.reserveCalls) != 0 {
		t.Fatalf("regular checkout should fail pre-check before RPC reservations: prod=%d promo=%d", len(prod.reserveReqs), len(promo.reserveCalls))
	}

	// 2. Flash sale checkout with flash_sale_item_id succeeds even when available_stock == 0!
	resp, err := svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-ok",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash-only", Quantity: 2, FlashSaleItemId: "fsi-1"},
		},
	})
	if err != nil {
		t.Fatalf("flash sale CreateCheckout failed: %v", err)
	}
	if len(resp.GetOrders()) != 1 {
		t.Fatalf("expected 1 order, got %d", len(resp.GetOrders()))
	}

	// Verify ProductService.ReserveStock received flash_sale_item_id="fsi-1".
	if len(prod.reserveReqs) != 1 || len(prod.reserveReqs[0].GetItems()) != 1 {
		t.Fatalf("expected 1 ReserveStock call with 1 item, got %+v", prod.reserveReqs)
	}
	item := prod.reserveReqs[0].GetItems()[0]
	if item.GetSkuId() != "sku-flash-only" || item.GetQuantity() != 2 || item.GetFlashSaleItemId() != "fsi-1" {
		t.Fatalf("ReserveStock item mismatch: got %+v", item)
	}

	// Verify ReservePromotion ran BEFORE ReserveStock for flash sale checkout!
	logMu.Lock()
	gotOrder := append([]string(nil), (*orderLog)...)
	logMu.Unlock()
	if len(gotOrder) != 2 || gotOrder[0] != "promotion.ReservePromotion" || gotOrder[1] != "product.ReserveStock" {
		t.Fatalf("expected [promotion.ReservePromotion, product.ReserveStock], got %v", gotOrder)
	}
}

func TestCreateCheckout_FlashSale_PromotionExhaustedSkipsProductReserve(t *testing.T) {
	svc, _, prod, promo, _, _ := newFlashCheckoutEnv(nil, nil)
	promo.reserveErr = status.Error(codes.ResourceExhausted, "FLASH_SALE_SOLD_OUT")

	_, err := svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-sold-out",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash-only", Quantity: 1, FlashSaleItemId: "fsi-1"},
		},
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("want ResourceExhausted, got %v", err)
	}
	if len(prod.reserveReqs) != 0 {
		t.Fatalf("expected 0 Product.ReserveStock calls when Promotion gate rejects, got %d", len(prod.reserveReqs))
	}
}

func TestCreateCheckout_FlashSale_StockReserveFailureCompensatesPromotion(t *testing.T) {
	svc, _, prod, promo, _, _ := newFlashCheckoutEnv(nil, nil)
	prod.reserveErr = status.Error(codes.FailedPrecondition, "INSUFFICIENT_FLASH_STOCK")

	_, err := svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-flash-stock-fail",
		AddressId:      "addr-buyer-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-flash-only", Quantity: 1, FlashSaleItemId: "fsi-1"},
		},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
	if len(promo.reserveCalls) != 1 || len(promo.releaseCalls) != 1 {
		t.Fatalf("expected 1 ReservePromotion and 1 compensating ReleasePromotion, got reserve=%d release=%d",
			len(promo.reserveCalls), len(promo.releaseCalls))
	}
	if promo.releaseCalls[0] != promo.reserveCalls[0] {
		t.Fatalf("ReleasePromotion order_id=%q != ReservePromotion order_id=%q", promo.releaseCalls[0], promo.reserveCalls[0])
	}
}

func TestCreateCheckout_FlashSale_100ConcurrentBuyersFor20Slots(t *testing.T) {
	var slotQuota, flashRemain atomic.Int32
	slotQuota.Store(20)
	flashRemain.Store(20)

	svc, u, prod, promo, _, _ := newFlashCheckoutEnv(&slotQuota, &flashRemain)

	const (
		totalSlots  = 20
		concurrency = 100
	)
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
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				Items: []*orderv1.CheckoutItemInput{
					{SkuId: "sku-flash-only", Quantity: 1, FlashSaleItemId: "fsi-1"},
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
	if len(prod.reserveReqs) != totalSlots {
		t.Fatalf("expected EXACTLY %d Product.ReserveStock calls (0 wasted calls for losers), got %d", totalSlots, len(prod.reserveReqs))
	}
	if len(promo.releaseCalls) != 0 {
		t.Fatalf("expected 0 compensating releases, got %d", len(promo.releaseCalls))
	}
	if slotQuota.Load() != 0 || flashRemain.Load() != 0 {
		t.Fatalf("expected slotQuota=0 flashRemain=0, got slots=%d stock=%d", slotQuota.Load(), flashRemain.Load())
	}
}
