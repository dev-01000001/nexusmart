package service

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Thread-safe mock gRPC clients ---

type authCallSnapshot struct {
	role     string
	rawToken string
}

type mockUserClient struct {
	mu               sync.Mutex
	codRejectedCount int32
	userStatus       userv1.UserStatus
	userValid        bool
	addresses        map[string]*userv1.Address
	shops            map[string]*userv1.Shop
	getAddressAuth   map[string]authCallSnapshot
}

func newMockUserClient() *mockUserClient {
	return &mockUserClient{
		userStatus:     userv1.UserStatus_USER_STATUS_ACTIVE,
		userValid:      true,
		getAddressAuth: make(map[string]authCallSnapshot),
		addresses: map[string]*userv1.Address{
			"addr-buyer-1": {
				Id:             "addr-buyer-1",
				UserId:         "buyer-1",
				RecipientName:  "Nguyen Van A",
				RecipientPhone: "0901234567",
				ProvinceCode:   "79",
				ProvinceName:   "Ho Chi Minh",
				WardCode:       "26734",
				WardName:       "Ben Nghe",
				StreetAddress:  "123 Le Loi",
			},
			"addr-shop-1": {
				Id:             "addr-shop-1",
				UserId:         "seller-1",
				RecipientName:  "Shop Alpha Warehouse",
				RecipientPhone: "0911111111",
				ProvinceCode:   "01",
				ProvinceName:   "Ha Noi",
				WardCode:       "00001",
				WardName:       "Phuc Xa",
				StreetAddress:  "10 Ba Dinh",
			},
			"addr-other": {
				Id:             "addr-other",
				UserId:         "buyer-2",
				RecipientName:  "Other Buyer",
				RecipientPhone: "0909999999",
				ProvinceCode:   "01",
				WardCode:       "00001",
				StreetAddress:  "99 Trang Tien",
			},
		},
		shops: map[string]*userv1.Shop{
			"shop-1":         {Id: "shop-1", OwnerUserId: "seller-1", Name: "Shop Alpha", PickupAddressId: "addr-shop-1", ContactPhone: "0911111111", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE},
			"shop-2":         {Id: "shop-2", OwnerUserId: "seller-2", Name: "Shop Beta", ContactPhone: "0922222222", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE},
			"shop-pending":   {Id: "shop-pending", OwnerUserId: "seller-3", Name: "Pending Shop", Status: userv1.ShopStatus_SHOP_STATUS_PENDING},
			"shop-suspended": {Id: "shop-suspended", OwnerUserId: "seller-4", Name: "Suspended Shop", Status: userv1.ShopStatus_SHOP_STATUS_SUSPENDED},
		},
	}
}

func (m *mockUserClient) ValidateUser(_ context.Context, _ *userv1.ValidateUserRequest, _ ...grpc.CallOption) (*userv1.ValidateUserResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &userv1.ValidateUserResponse{
		Valid:            m.userValid,
		Role:             userv1.UserRole_USER_ROLE_BUYER,
		Status:           m.userStatus,
		CodRejectedCount: m.codRejectedCount,
	}, nil
}

func (m *mockUserClient) GetAddress(ctx context.Context, req *userv1.GetAddressRequest, _ ...grpc.CallOption) (*userv1.GetAddressResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getAddressAuth[req.GetAddressId()] = authCallSnapshot{
		role:     auth.RoleFromContext(ctx),
		rawToken: auth.RawTokenFromContext(ctx),
	}
	a, ok := m.addresses[req.GetAddressId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "address not found")
	}
	// Mirror UserService ownership check: non-admin callers cannot fetch another user's address.
	callerID := auth.UserIDFromContext(ctx)
	callerRole := auth.RoleFromContext(ctx)
	rawToken := auth.RawTokenFromContext(ctx)
	if rawToken != "" && callerRole == "admin" {
		return nil, status.Error(codes.PermissionDenied, "raw buyer token leaked onto admin call")
	}
	if callerID != "" && callerID != a.GetUserId() && callerRole != "admin" && callerRole != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "cannot access another user's address")
	}
	return &userv1.GetAddressResponse{Address: a}, nil
}

func (m *mockUserClient) GetShop(_ context.Context, req *userv1.GetShopRequest, _ ...grpc.CallOption) (*userv1.GetShopResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.shops[req.GetShopId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "shop not found")
	}
	return &userv1.GetShopResponse{Shop: s}, nil
}

type mockProductClient struct {
	mu               sync.Mutex
	skus             map[string]*productv1.SKU
	reserveErr       error
	releaseErr       error
	releaseReturnsOk bool
	reserveCalls     []string
	releaseCalls     []string
	confirmCalls     []string
	updateStockCalls []*productv1.UpdateStockRequest
	updateStockAuth  []authCallSnapshot
}

func newMockProductClient() *mockProductClient {
	return &mockProductClient{
		releaseReturnsOk: true,
		skus: map[string]*productv1.SKU{
			"sku-1": {
				Id:                "sku-1",
				ProductId:         "prod-1",
				ShopId:            "shop-1",
				ProductName:       "Mechanical Keyboard",
				Name:              "Black / Tactile",
				PriceVnd:          1000000,
				Stock:             50,
				AvailableStock:    50,
				Version:           1,
				WeightGrams:       800,
				CommissionRateBps: 500,
				IsActive:          true,
			},
			"sku-2": {
				Id:                "sku-2",
				ProductId:         "prod-2",
				ShopId:            "shop-2",
				ProductName:       "Wireless Mouse",
				Name:              "White",
				PriceVnd:          500000,
				Stock:             30,
				AvailableStock:    30,
				Version:           1,
				WeightGrams:       300,
				CommissionRateBps: 800,
				IsActive:          true,
			},
			"sku-expensive": {
				Id:                "sku-expensive",
				ProductId:         "prod-3",
				ShopId:            "shop-1",
				ProductName:       "Gaming Laptop",
				Name:              "RTX",
				PriceVnd:          6000000,
				Stock:             10,
				AvailableStock:    10,
				Version:           1,
				WeightGrams:       2500,
				CommissionRateBps: 500,
				IsActive:          true,
			},
		},
	}
}

func (m *mockProductClient) GetSKUsBatch(_ context.Context, req *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*productv1.SKU, 0, len(req.GetSkuIds()))
	for _, id := range req.GetSkuIds() {
		if s, ok := m.skus[id]; ok {
			out = append(out, s)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

func (m *mockProductClient) UpdateStock(ctx context.Context, req *productv1.UpdateStockRequest, _ ...grpc.CallOption) (*productv1.UpdateStockResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateStockCalls = append(m.updateStockCalls, req)
	m.updateStockAuth = append(m.updateStockAuth, authCallSnapshot{
		role:     auth.RoleFromContext(ctx),
		rawToken: auth.RawTokenFromContext(ctx),
	})
	if sku, ok := m.skus[req.GetSkuId()]; ok {
		sku.Stock = req.GetNewStock()
		sku.AvailableStock = req.GetNewStock()
		sku.Version++
		return &productv1.UpdateStockResponse{Sku: sku}, nil
	}
	return nil, status.Error(codes.NotFound, "sku not found")
}

func (m *mockProductClient) ReserveStock(_ context.Context, req *productv1.ReserveStockRequest, _ ...grpc.CallOption) (*productv1.ReserveStockResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserveCalls = append(m.reserveCalls, req.GetOrderId())
	if m.reserveErr != nil {
		return nil, m.reserveErr
	}
	return &productv1.ReserveStockResponse{Reserved: true, ReservationId: "res-" + req.GetOrderId()}, nil
}

func (m *mockProductClient) ReleaseStock(_ context.Context, req *productv1.ReleaseStockRequest, _ ...grpc.CallOption) (*productv1.ReleaseStockResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseCalls = append(m.releaseCalls, req.GetOrderId())
	if m.releaseErr != nil {
		return nil, m.releaseErr
	}
	return &productv1.ReleaseStockResponse{Released: m.releaseReturnsOk}, nil
}

func (m *mockProductClient) ConfirmStock(_ context.Context, req *productv1.ConfirmStockRequest, _ ...grpc.CallOption) (*productv1.ConfirmStockResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirmCalls = append(m.confirmCalls, req.GetOrderId())
	return &productv1.ConfirmStockResponse{Confirmed: true}, nil
}

type mockCartClient struct {
	mu            sync.Mutex
	cart          *cartv1.Cart
	getCartCalls  []*cartv1.GetCartRequest
	clearCalls    [][]string
	clearReqCalls []*cartv1.ClearItemsRequest
}

func newMockCartClient() *mockCartClient {
	return &mockCartClient{
		cart: &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-1", ShopId: "shop-1", Quantity: 1, PriceVnd: 1000000},
					},
				},
				{
					ShopId: "shop-2",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-2", ShopId: "shop-2", Quantity: 2, PriceVnd: 500000},
					},
				},
			},
		},
	}
}

func (m *mockCartClient) GetCart(_ context.Context, req *cartv1.GetCartRequest, _ ...grpc.CallOption) (*cartv1.GetCartResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCartCalls = append(m.getCartCalls, req)
	if m.cart == nil {
		return &cartv1.GetCartResponse{}, nil
	}
	filterShop := req.GetShopId()
	if filterShop == "" {
		return &cartv1.GetCartResponse{Cart: m.cart}, nil
	}
	filtered := &cartv1.Cart{UserId: m.cart.GetUserId()}
	for _, grp := range m.cart.GetShopGroups() {
		if grp.GetShopId() == filterShop {
			filtered.ShopGroups = append(filtered.ShopGroups, grp)
			for _, it := range grp.GetItems() {
				filtered.TotalItems += it.GetQuantity()
				filtered.TotalVnd += it.GetPriceVnd() * int64(it.GetQuantity())
			}
		}
	}
	return &cartv1.GetCartResponse{Cart: filtered}, nil
}

func (m *mockCartClient) ClearItems(_ context.Context, req *cartv1.ClearItemsRequest, _ ...grpc.CallOption) (*cartv1.ClearItemsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCalls = append(m.clearCalls, append([]string(nil), req.GetSkuIds()...))
	m.clearReqCalls = append(m.clearReqCalls, req)
	if m.cart != nil && len(req.GetSkuIds()) > 0 {
		toRemove := make(map[string]bool, len(req.GetSkuIds()))
		for _, id := range req.GetSkuIds() {
			toRemove[id] = true
		}
		var remainingGroups []*cartv1.CartShopGroup
		for _, grp := range m.cart.ShopGroups {
			var kept []*cartv1.CartItem
			for _, it := range grp.Items {
				if !toRemove[it.SkuId] {
					kept = append(kept, it)
				}
			}
			if len(kept) > 0 {
				remainingGroups = append(remainingGroups, &cartv1.CartShopGroup{
					ShopId:   grp.GetShopId(),
					ShopName: grp.GetShopName(),
					Items:    kept,
				})
			}
		}
		m.cart.ShopGroups = remainingGroups
	}
	return &cartv1.ClearItemsResponse{Ok: true}, nil
}

type mockPromotionClient struct {
	mu           sync.Mutex
	reserveErr   error
	breakdown    *promotionv1.DiscountBreakdown
	flashSales   []*promotionv1.FlashSale
	reserveCalls []string
	reserveReqs  []*promotionv1.ReservePromotionRequest
	releaseCalls []string
}

func (m *mockPromotionClient) ListActiveFlashSales(_ context.Context, _ *promotionv1.ListActiveFlashSalesRequest, _ ...grpc.CallOption) (*promotionv1.ListActiveFlashSalesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &promotionv1.ListActiveFlashSalesResponse{FlashSales: m.flashSales}, nil
}

func (m *mockPromotionClient) PreviewDiscounts(_ context.Context, _ *promotionv1.PreviewDiscountsRequest, _ ...grpc.CallOption) (*promotionv1.PreviewDiscountsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &promotionv1.PreviewDiscountsResponse{Breakdown: m.breakdown}, nil
}

func (m *mockPromotionClient) ReservePromotion(_ context.Context, req *promotionv1.ReservePromotionRequest, _ ...grpc.CallOption) (*promotionv1.ReservePromotionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserveCalls = append(m.reserveCalls, req.GetOrderId())
	m.reserveReqs = append(m.reserveReqs, req)
	if m.reserveErr != nil {
		return nil, m.reserveErr
	}
	return &promotionv1.ReservePromotionResponse{
		Reserved:  true,
		Breakdown: m.breakdown,
	}, nil
}

func (m *mockPromotionClient) ReleasePromotion(_ context.Context, req *promotionv1.ReleasePromotionRequest, _ ...grpc.CallOption) (*promotionv1.ReleasePromotionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseCalls = append(m.releaseCalls, req.GetOrderId())
	return &promotionv1.ReleasePromotionResponse{Released: true}, nil
}

type mockPaymentClient struct {
	mu                 sync.Mutex
	createErr          error
	getPaymentStatus   paymentv1.PaymentStatus
	getPaymentErr      error
	createCalls        []*paymentv1.CreatePaymentRequest
	cancelCalls        []*paymentv1.CancelPaymentRequest
	refundCalls        []*paymentv1.CreateRefundRequest
	releaseEscrowCalls []*paymentv1.ReleaseEscrowRequest
}

func (m *mockPaymentClient) CreatePayment(_ context.Context, req *paymentv1.CreatePaymentRequest, _ ...grpc.CallOption) (*paymentv1.CreatePaymentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls = append(m.createCalls, req)
	if m.createErr != nil {
		return nil, m.createErr
	}
	st := paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	qrURL, qrCode := "https://qr.sandbox/pay-1", "000201010212"
	if req.GetMethod() == paymentv1.PaymentMethod_PAYMENT_METHOD_COD {
		st = paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
		qrURL, qrCode = "", ""
	}
	return &paymentv1.CreatePaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId:    "pay-" + req.GetOrderGroupId(),
			OrderGroupId: req.GetOrderGroupId(),
			OrderId:      req.GetOrderId(),
			UserId:       req.GetUserId(),
			AmountVnd:    req.GetAmountVnd(),
			Method:       req.GetMethod(),
			Status:       st,
			QrUrl:        qrURL,
			QrCode:       qrCode,
			ExpiresAt:    req.GetExpiresAt(),
		},
	}, nil
}

func (m *mockPaymentClient) GetPayment(_ context.Context, req *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getPaymentErr != nil {
		return nil, m.getPaymentErr
	}
	st := m.getPaymentStatus
	if st == paymentv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED {
		st = paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	}
	return &paymentv1.GetPaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId:    req.GetPaymentId(),
			OrderGroupId: req.GetOrderGroupId(),
			Status:       st,
		},
	}, nil
}

func (m *mockPaymentClient) CancelPayment(_ context.Context, req *paymentv1.CancelPaymentRequest, _ ...grpc.CallOption) (*paymentv1.CancelPaymentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelCalls = append(m.cancelCalls, req)
	return &paymentv1.CancelPaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId: req.GetPaymentId(),
			Status:    paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED,
		},
	}, nil
}

func (m *mockPaymentClient) CreateRefund(_ context.Context, req *paymentv1.CreateRefundRequest, _ ...grpc.CallOption) (*paymentv1.CreateRefundResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refundCalls = append(m.refundCalls, req)
	return &paymentv1.CreateRefundResponse{
		Refund: &paymentv1.Refund{
			RefundId:  "ref-" + req.GetOrderId(),
			PaymentId: req.GetPaymentId(),
			OrderId:   req.GetOrderId(),
			AmountVnd: req.GetAmountVnd(),
			Status:    paymentv1.RefundStatus_REFUND_STATUS_DONE,
		},
	}, nil
}

func (m *mockPaymentClient) ReleaseEscrow(_ context.Context, req *paymentv1.ReleaseEscrowRequest, _ ...grpc.CallOption) (*paymentv1.ReleaseEscrowResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseEscrowCalls = append(m.releaseEscrowCalls, req)
	return &paymentv1.ReleaseEscrowResponse{Released: true}, nil
}

type mockShippingClient struct {
	mu                    sync.Mutex
	quotes                []*shippingv1.CarrierQuote
	createShipCalls       []*shippingv1.CreateShipmentRequest
	createReturnShipCalls []*shippingv1.CreateReturnShipmentRequest
	createReturnShipErr   error
	cancelShipCalls       []*shippingv1.CancelShipmentRequest
	calculateFeeReqs      []*shippingv1.CalculateFeeRequest
	calculateFeeCall      int
}

func newMockShippingClient() *mockShippingClient {
	return &mockShippingClient{
		quotes: []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", CarrierName: "GHN", ServiceCode: "standard", FeeVnd: 30000, EstimatedDays: 3},
			{CarrierCode: "mock_express", CarrierName: "MockExpress", ServiceCode: "fast", FeeVnd: 45000, EstimatedDays: 1},
		},
	}
}

func (m *mockShippingClient) CalculateFee(_ context.Context, req *shippingv1.CalculateFeeRequest, _ ...grpc.CallOption) (*shippingv1.CalculateFeeResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calculateFeeCall++
	m.calculateFeeReqs = append(m.calculateFeeReqs, req)
	return &shippingv1.CalculateFeeResponse{Quotes: m.quotes}, nil
}

func (m *mockShippingClient) CreateShipment(_ context.Context, req *shippingv1.CreateShipmentRequest, _ ...grpc.CallOption) (*shippingv1.CreateShipmentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createShipCalls = append(m.createShipCalls, req)
	return &shippingv1.CreateShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:   "shp-" + req.GetOrderId(),
			OrderId:      req.GetOrderId(),
			ShopId:       req.GetShopId(),
			CarrierCode:  req.GetCarrierCode(),
			ServiceCode:  req.GetServiceCode(),
			TrackingCode: "TRK-" + req.GetOrderId()[:8],
			LabelUrl:     "https://labels.sandbox/" + req.GetOrderId(),
			Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			CodAmountVnd: req.GetCodAmountVnd(),
		},
	}, nil
}

func (m *mockShippingClient) CreateReturnShipment(_ context.Context, req *shippingv1.CreateReturnShipmentRequest, _ ...grpc.CallOption) (*shippingv1.CreateReturnShipmentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createReturnShipCalls = append(m.createReturnShipCalls, req)
	if m.createReturnShipErr != nil {
		return nil, m.createReturnShipErr
	}
	suffix := req.GetReturnRequestId()
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return &shippingv1.CreateReturnShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:      "rshp-" + req.GetReturnRequestId(),
			OrderId:         req.GetOrderId(),
			ShopId:          req.GetShopId(),
			CarrierCode:     req.GetCarrierCode(),
			ServiceCode:     req.GetServiceCode(),
			TrackingCode:    "RET-" + suffix,
			LabelUrl:        "https://labels.sandbox/return/" + req.GetReturnRequestId(),
			Status:          shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			Direction:       "return",
			ReturnRequestId: req.GetReturnRequestId(),
		},
	}, nil
}

func (m *mockShippingClient) CancelShipment(_ context.Context, req *shippingv1.CancelShipmentRequest, _ ...grpc.CallOption) (*shippingv1.CancelShipmentResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelShipCalls = append(m.cancelShipCalls, req)
	return &shippingv1.CancelShipmentResponse{}, nil
}

type testHarness struct {
	svc      *OrderService
	store    *store.MemoryStore
	user     *mockUserClient
	product  *mockProductClient
	cart     *mockCartClient
	promo    *mockPromotionClient
	payment  *mockPaymentClient
	shipping *mockShippingClient
}

func newTestHarness() *testHarness {
	st := store.NewMemoryStore()
	u := newMockUserClient()
	pr := newMockProductClient()
	ca := newMockCartClient()
	pm := &mockPromotionClient{}
	py := &mockPaymentClient{}
	sh := newMockShippingClient()

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
		Product:   pr,
		Cart:      ca,
		Promotion: pm,
		Payment:   py,
		Shipping:  sh,
	}

	log := logger.New("order-test", logger.WithWriter(io.Discard))
	return &testHarness{
		svc:      NewOrderService(cfg, st, bundle, log),
		store:    st,
		user:     u,
		product:  pr,
		cart:     ca,
		promo:    pm,
		payment:  py,
		shipping: sh,
	}
}

func ctxWithUser(userID, role, shopID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	})
}

// --- Tests ---

func TestAllocateProportionalVND_ExactSumConservation(t *testing.T) {
	weights := []int64{100000, 200000, 300000}
	shares := AllocateProportionalVND(10000, weights)
	var sum int64
	for _, s := range shares {
		sum += s
	}
	if sum != 10000 {
		t.Fatalf("expected sum 10000, got %d (%v)", sum, shares)
	}
	if shares[0] != 1666 || shares[1] != 3333 || shares[2] != 5001 {
		t.Fatalf("unexpected proportional shares: %v", shares)
	}
}

func TestCreateCheckout_MultiShopCOD_WithPromotionsAndFlashSale(t *testing.T) {
	h := newTestHarness()
	h.promo.flashSales = []*promotionv1.FlashSale{
		{
			Id: "fs-1",
			Items: []*promotionv1.FlashSaleItem{
				{Id: "fsi-1", SkuId: "sku-1", FlashPriceVnd: 800000},
			},
		},
	}
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{
				ShopId:                     "shop-1",
				SubtotalVnd:                1000000,
				FlashSaleDiscountVnd:       200000,
				ShopVoucherDiscountVnd:     50000,
				PlatformVoucherDiscountVnd: 25000,
				FreeshipDiscountVnd:        15000,
				CoinsDiscountVnd:           10000,
				ShippingFeeVnd:             30000,
			},
			{
				ShopId:                     "shop-2",
				SubtotalVnd:                1000000,
				FlashSaleDiscountVnd:       0,
				ShopVoucherDiscountVnd:     0,
				PlatformVoucherDiscountVnd: 25000,
				FreeshipDiscountVnd:        15000,
				CoinsDiscountVnd:           10000,
				ShippingFeeVnd:             45000,
			},
		},
	}

	ctx := ctxWithUser("buyer-1", "buyer", "")
	resp, err := h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-cod-multi-1",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		FromCart:          true,
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", FlashSaleItemId: "fsi-1"}},
		ShopVoucherIds:    map[string]string{"shop-1": "v-shop-1"},
		PlatformVoucherId: "v-plat-1",
		FreeshipVoucherId: "v-free-1",
		CoinsToUse:        20000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		ShippingChoices: []*orderv1.ShopShippingChoice{
			{ShopId: "shop-2", CarrierCode: "mock_express", ServiceCode: "fast"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	if resp.IdempotentReplay {
		t.Fatalf("expected idempotent_replay=false on first call")
	}
	if len(resp.Orders) != 2 {
		t.Fatalf("expected 2 shop sub-orders, got %d", len(resp.Orders))
	}

	// Verify Shop 1 math: 1,000,000 - 200,000 (flash) - 50,000 (shop) - 25,000 (plat) + 30,000 (ship) - 15,000 (freeship) - 10,000 (coins) = 730,000
	o1 := resp.Orders[0]
	if o1.ShopId != "shop-1" || o1.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		t.Fatalf("unexpected shop-1 order: shop=%s status=%v", o1.ShopId, o1.Status)
	}
	if o1.TotalPayableVnd != 730000 {
		t.Fatalf("shop-1 total_payable_vnd = %d, want 730000", o1.TotalPayableVnd)
	}
	if o1.Items[0].EffectiveUnitPriceVnd != 800000 {
		t.Fatalf("shop-1 flash sale effective unit price = %d, want 800000", o1.Items[0].EffectiveUnitPriceVnd)
	}
	if o1.CommissionVnd != 40000 { // 5% of 800,000
		t.Fatalf("shop-1 commission_vnd = %d, want 40000", o1.CommissionVnd)
	}
	if o1.CarrierCode != "ghn" { // cheapest default quote
		t.Fatalf("shop-1 carrier = %q, want ghn", o1.CarrierCode)
	}

	// Verify Shop 2 math: 1,000,000 - 25,000 (plat) + 45,000 (ship) - 15,000 (freeship) - 10,000 (coins) = 995,000
	o2 := resp.Orders[1]
	if o2.ShopId != "shop-2" || o2.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		t.Fatalf("unexpected shop-2 order: shop=%s status=%v", o2.ShopId, o2.Status)
	}
	if o2.TotalPayableVnd != 995000 {
		t.Fatalf("shop-2 total_payable_vnd = %d, want 995000", o2.TotalPayableVnd)
	}
	if o2.CarrierCode != "mock_express" {
		t.Fatalf("shop-2 carrier = %q, want mock_express", o2.CarrierCode)
	}
	if o2.CommissionVnd != 80000 { // 8% of 1,000,000
		t.Fatalf("shop-2 commission_vnd = %d, want 80000", o2.CommissionVnd)
	}

	if resp.TotalPayableVnd != 1725000 {
		t.Fatalf("grand total_payable_vnd = %d, want 1725000", resp.TotalPayableVnd)
	}

	// Verify PaymentService received exact matching ShopAllocations summing to grand total.
	if len(h.payment.createCalls) != 1 {
		t.Fatalf("expected 1 CreatePayment call, got %d", len(h.payment.createCalls))
	}
	payReq := h.payment.createCalls[0]
	var allocSum int64
	for _, al := range payReq.ShopAllocations {
		if al.SubtotalVnd < al.CommissionVnd {
			t.Fatalf("allocation commission %d exceeds subtotal %d", al.CommissionVnd, al.SubtotalVnd)
		}
		allocSum += al.SubtotalVnd + al.ShippingFeeVnd
	}
	if allocSum != payReq.AmountVnd {
		t.Fatalf("payment allocations sum %d != amount_vnd %d", allocSum, payReq.AmountVnd)
	}

	// Verify ConfirmStock & ClearItems were invoked for COD checkout.
	if len(h.product.confirmCalls) != 1 {
		t.Fatalf("expected 1 ConfirmStock call for COD, got %d", len(h.product.confirmCalls))
	}
	if len(h.cart.clearCalls) != 1 || len(h.cart.clearCalls[0]) != 2 {
		t.Fatalf("expected cart ClearItems with 2 SKUs, got %v", h.cart.clearCalls)
	}

	// Verify outbox contains 2 order.created + 2 order.confirmed events.
	outbox := h.store.OutboxEvents()
	createdCount, confirmedCount := 0, 0
	for _, ev := range outbox {
		if ev.EventType == events.SubjectOrderCreated {
			createdCount++
		}
		if ev.EventType == events.SubjectOrderConfirmed {
			confirmedCount++
		}
	}
	if createdCount != 2 || confirmedCount != 2 {
		t.Fatalf("expected 2 order.created and 2 order.confirmed outbox events, got created=%d confirmed=%d", createdCount, confirmedCount)
	}
}

func TestCreateCheckout_IdempotencyReplay_SequentialAndConcurrent(t *testing.T) {
	h := newTestHarness()
	ctx := ctxWithUser("buyer-1", "buyer", "")

	makeReq := func() *orderv1.CreateCheckoutRequest {
		return &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-concurrent-key",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       false,
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
	}

	const concurrency = 10
	var wg sync.WaitGroup
	results := make([]*orderv1.CreateCheckoutResponse, concurrency)
	errs := make([]error, concurrency)
	var firstCount, replayCount atomic.Int32

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			resp, err := h.svc.CreateCheckout(ctx, makeReq())
			results[idx] = resp
			errs[idx] = err
			if err == nil && resp != nil {
				if resp.IdempotentReplay {
					replayCount.Add(1)
				} else {
					firstCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	var groupID, orderID string
	for i := 0; i < concurrency; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d failed: %v", i, errs[i])
		}
		if i == 0 {
			groupID = results[i].CheckoutGroupId
			orderID = results[i].Orders[0].Id
		} else {
			if results[i].CheckoutGroupId != groupID {
				t.Fatalf("goroutine %d got different checkout_group_id %s != %s", i, results[i].CheckoutGroupId, groupID)
			}
			if results[i].Orders[0].Id != orderID {
				t.Fatalf("goroutine %d got different order_id %s != %s", i, results[i].Orders[0].Id, orderID)
			}
		}
	}

	if firstCount.Load() != 1 || replayCount.Load() != concurrency-1 {
		t.Fatalf("expected 1 fresh and %d replays, got fresh=%d replay=%d", concurrency-1, firstCount.Load(), replayCount.Load())
	}
	if len(h.product.reserveCalls) != 1 {
		t.Fatalf("ReserveStock must be called exactly ONCE, got %d", len(h.product.reserveCalls))
	}
	if len(h.payment.createCalls) != 1 {
		t.Fatalf("CreatePayment must be called exactly ONCE, got %d", len(h.payment.createCalls))
	}

	// Sequential replay check.
	seqResp, err := h.svc.CreateCheckout(ctx, makeReq())
	if err != nil {
		t.Fatalf("sequential replay failed: %v", err)
	}
	if !seqResp.IdempotentReplay || seqResp.CheckoutGroupId != groupID {
		t.Fatalf("expected sequential replay=true and group=%s, got replay=%v group=%s", groupID, seqResp.IdempotentReplay, seqResp.CheckoutGroupId)
	}
}

func TestCreateCheckout_SagaCompensation_OnPromotionFailure(t *testing.T) {
	h := newTestHarness()
	h.promo.reserveErr = status.Error(codes.FailedPrecondition, "VOUCHER_EXHAUSTED")

	ctx := ctxWithUser("buyer-1", "buyer", "")
	_, err := h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-promo-fail",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-empty",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err == nil {
		t.Fatalf("expected error when promotion reserve fails")
	}

	if len(h.product.reserveCalls) != 1 {
		t.Fatalf("expected 1 ReserveStock call, got %d", len(h.product.reserveCalls))
	}
	if len(h.product.releaseCalls) != 1 || h.product.releaseCalls[0] != h.product.reserveCalls[0] {
		t.Fatalf("expected ReleaseStock compensation for group %s, got %v", h.product.reserveCalls[0], h.product.releaseCalls)
	}
	if len(h.payment.createCalls) != 0 {
		t.Fatalf("CreatePayment should NOT be called when promotion fails")
	}
}

func TestCreateCheckout_SagaCompensation_OnPaymentFailure(t *testing.T) {
	h := newTestHarness()
	h.payment.createErr = status.Error(codes.Unavailable, "payment gateway unavailable")

	ctx := ctxWithUser("buyer-1", "buyer", "")
	_, err := h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-pay-fail",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-plat-1",
		CoinsToUse:        5000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err == nil {
		t.Fatalf("expected error when CreatePayment fails")
	}

	groupID := h.product.reserveCalls[0]
	if len(h.product.releaseCalls) != 1 || h.product.releaseCalls[0] != groupID {
		t.Fatalf("expected ReleaseStock(%s), got %v", groupID, h.product.releaseCalls)
	}
	if len(h.promo.releaseCalls) != 1 || h.promo.releaseCalls[0] != groupID {
		t.Fatalf("expected ReleasePromotion(%s), got %v", groupID, h.promo.releaseCalls)
	}

	// Sub-orders must be marked CANCELLED and order.cancelled emitted.
	aggs, err := h.store.ListOrdersByCheckoutGroup(context.Background(), groupID)
	if err != nil || len(aggs) != 1 {
		t.Fatalf("expected 1 cancelled sub-order record, got %d (err=%v)", len(aggs), err)
	}
	if aggs[0].Order.Status != "CANCELLED" {
		t.Fatalf("expected sub-order status CANCELLED, got %s", aggs[0].Order.Status)
	}

	foundCancelledOutbox := false
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderCancelled {
			foundCancelledOutbox = true
		}
	}
	if !foundCancelledOutbox {
		t.Fatalf("expected order.cancelled outbox event after CreatePayment failure")
	}
}

func TestHandlePaymentFailedAndSucceeded_SagaCompensationAndIdempotency(t *testing.T) {
	// Case 1: payment.failed restores stock + promotions + cancels PENDING orders.
	h := newTestHarness()
	ctx := ctxWithUser("buyer-1", "buyer", "")
	resp, err := h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-vietqr-fail-event",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}, {SkuId: "sku-2", Quantity: 1}},
		PlatformVoucherId: "v-plat-1",
		CoinsToUse:        1000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout VietQR failed: %v", err)
	}
	for _, o := range resp.Orders {
		if o.Status != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			t.Fatalf("expected PENDING status for VietQR order, got %v", o.Status)
		}
	}

	if err := h.svc.HandlePaymentFailed(context.Background(), "evt-pay-fail-1", events.PaymentFailedPayload{
		PaymentID:    resp.PaymentId,
		OrderGroupID: resp.CheckoutGroupId,
		Reason:       "bank declined",
	}); err != nil {
		t.Fatalf("HandlePaymentFailed error: %v", err)
	}

	// Duplicate delivery of exact same event_id must be a complete no-op.
	if err := h.svc.HandlePaymentFailed(context.Background(), "evt-pay-fail-1", events.PaymentFailedPayload{
		PaymentID:    resp.PaymentId,
		OrderGroupID: resp.CheckoutGroupId,
		Reason:       "bank declined",
	}); err != nil {
		t.Fatalf("duplicate HandlePaymentFailed error: %v", err)
	}

	if len(h.product.releaseCalls) != 1 || h.product.releaseCalls[0] != resp.CheckoutGroupId {
		t.Fatalf("expected exactly 1 ReleaseStock compensation, got %v", h.product.releaseCalls)
	}
	if len(h.promo.releaseCalls) != 1 || h.promo.releaseCalls[0] != resp.CheckoutGroupId {
		t.Fatalf("expected exactly 1 ReleasePromotion compensation, got %v", h.promo.releaseCalls)
	}

	aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), resp.CheckoutGroupId)
	for _, a := range aggs {
		if a.Order.Status != "CANCELLED" {
			t.Fatalf("expected order %s to be CANCELLED, got %s", a.Order.ID, a.Order.Status)
		}
	}

	// Case 2: payment.succeeded transitions PENDING -> CONFIRMED and calls ConfirmStock once.
	resp2, err := h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-vietqr-ok-event",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout VietQR 2 failed: %v", err)
	}
	if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-pay-ok-1", events.PaymentSucceededPayload{
		PaymentID:    resp2.PaymentId,
		OrderGroupID: resp2.CheckoutGroupId,
		ProviderTxID: "TXN-123",
	}); err != nil {
		t.Fatalf("HandlePaymentSucceeded error: %v", err)
	}
	if len(h.product.confirmCalls) != 1 || h.product.confirmCalls[0] != resp2.CheckoutGroupId {
		t.Fatalf("expected ConfirmStock(%s), got %v", resp2.CheckoutGroupId, h.product.confirmCalls)
	}
	gotOrder, err := h.svc.GetOrder(ctx, &orderv1.GetOrderRequest{OrderId: resp2.Orders[0].Id})
	if err != nil || gotOrder.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		t.Fatalf("expected CONFIRMED order, got %v (err=%v)", gotOrder, err)
	}
}

func TestCreateCheckout_CODRiskControlsAndAddressOwnership(t *testing.T) {
	// 1. Exceeds COD max VND (without promotions -> fails BEFORE stock reserve).
	h := newTestHarness()
	ctx := ctxWithUser("buyer-1", "buyer", "")
	_, err := h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-cod-too-high",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-expensive", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for COD max limit, got %v", err)
	}
	if len(h.product.reserveCalls) != 0 {
		t.Fatalf("stock should not be reserved when COD max limit rejected upfront")
	}

	// 2. Exceeds COD max VND AFTER small promotion -> compensates stock & promo!
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-1", SubtotalVnd: 6000000, PlatformVoucherDiscountVnd: 100000, ShippingFeeVnd: 30000},
		},
	}
	_, err = h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-cod-too-high-promo",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-expensive", Quantity: 1}},
		PlatformVoucherId: "v-small",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition post-promo COD limit, got %v", err)
	}
	if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
		t.Fatalf("expected stock & promo compensation when post-promo COD limit exceeded")
	}

	// 3. Exceeds COD rejected count limit.
	h.user.codRejectedCount = 3
	_, err = h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-cod-rejected-limit",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for COD rejected count >= 3, got %v", err)
	}

	// 4. Address belonging to another buyer -> PermissionDenied.
	h.user.codRejectedCount = 0
	_, err = h.svc.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-wrong-addr",
		BuyerId:        "buyer-1",
		AddressId:      "addr-other",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for another buyer's address, got %v", err)
	}
}

func TestOrderLifecycle_CompleteFlowAndRBAC403(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithUser("buyer-2", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithUser("seller-2", "seller", "shop-2")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-lifecycle-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	orderID := resp.Orders[0].Id

	// RBAC 403 checks: another buyer & another seller MUST be denied!
	if _, err := h.svc.GetOrder(otherBuyerCtx, &orderv1.GetOrderRequest{OrderId: orderID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer GetOrder, got %v", err)
	}
	if _, err := h.svc.GetOrderTimeline(otherBuyerCtx, &orderv1.GetOrderTimelineRequest{OrderId: orderID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer GetOrderTimeline, got %v", err)
	}
	if _, err := h.svc.GetOrder(seller2Ctx, &orderv1.GetOrderRequest{OrderId: orderID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other seller GetOrder, got %v", err)
	}
	if _, err := h.svc.SellerConfirmOrder(seller2Ctx, &orderv1.SellerConfirmOrderRequest{OrderId: orderID, ShopId: "shop-2"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other seller SellerConfirmOrder, got %v", err)
	}
	if _, err := h.svc.SellerConfirmOrder(buyerCtx, &orderv1.SellerConfirmOrderRequest{OrderId: orderID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer calling SellerConfirmOrder, got %v", err)
	}
	if _, err := h.svc.CancelOrder(otherBuyerCtx, &orderv1.CancelOrderRequest{OrderId: orderID, Reason: "hack"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer CancelOrder, got %v", err)
	}
	if _, err := h.svc.ListOrders(buyerCtx, &orderv1.ListOrdersRequest{RoleScope: "admin"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer listing admin orders, got %v", err)
	}

	// Admin CAN access!
	if _, err := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: orderID}); err != nil {
		t.Fatalf("admin should be able to GetOrder, got %v", err)
	}

	// Seller confirms: CONFIRMED -> PREPARING
	confResp, err := h.svc.SellerConfirmOrder(seller1Ctx, &orderv1.SellerConfirmOrderRequest{OrderId: orderID, ShopId: "shop-1"})
	if err != nil || confResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		t.Fatalf("expected PREPARING, got %v (err=%v)", confResp, err)
	}

	// Buyer cannot confirm receipt before DELIVERED.
	if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: orderID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for premature BuyerConfirmReceived, got %v", err)
	}

	// Seller ships: PREPARING -> SHIPPING (passes COD amount = TotalPayableVnd!)
	shipResp, err := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: orderID, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder failed: %v", err)
	}
	if shipResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_SHIPPING || shipResp.TrackingCode == "" {
		t.Fatalf("unexpected SellerShipOrder resp: %+v", shipResp)
	}
	if len(h.shipping.createShipCalls) != 1 || h.shipping.createShipCalls[0].CodAmountVnd != resp.Orders[0].TotalPayableVnd {
		t.Fatalf("expected CreateShipment COD amount %d, got %+v", resp.Orders[0].TotalPayableVnd, h.shipping.createShipCalls)
	}

	// Cannot cancel once SHIPPING!
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: orderID, Reason: "too late"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition cancelling SHIPPING order, got %v", err)
	}

	// Intermediate shipment.updated (delivering) logs timeline without changing status.
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ship-1", events.ShipmentUpdatedPayload{
		OrderID:      orderID,
		Status:       "delivering",
		TrackingCode: shipResp.TrackingCode,
		Description:  "Out for delivery",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(delivering) failed: %v", err)
	}

	// Delivered shipment.updated transitions SHIPPING -> DELIVERED + order.delivered outbox.
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ship-2", events.ShipmentUpdatedPayload{
		OrderID:      orderID,
		Status:       "delivered",
		TrackingCode: shipResp.TrackingCode,
		Description:  "Delivered successfully",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(delivered) failed: %v", err)
	}

	// Other buyer cannot confirm received.
	if _, err := h.svc.BuyerConfirmReceived(otherBuyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: orderID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer BuyerConfirmReceived, got %v", err)
	}

	// Owner buyer confirms received: DELIVERED -> COMPLETED, releases escrow, emits order.completed with coins_reward.
	doneResp, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: orderID})
	if err != nil || doneResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED, got %v (err=%v)", doneResp, err)
	}
	if len(h.payment.releaseEscrowCalls) != 1 || h.payment.releaseEscrowCalls[0].OrderId != orderID {
		t.Fatalf("expected ReleaseEscrow(%s), got %+v", orderID, h.payment.releaseEscrowCalls)
	}

	// Verify order.completed outbox payload has exact financial fields & reward coins.
	var completedPayload events.OrderCompletedPayload
	foundCompleted := false
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderCompleted {
			if err := json.Unmarshal(ev.PayloadJSON, &completedPayload); err != nil {
				t.Fatalf("unmarshal order.completed payload: %v", err)
			}
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Fatalf("expected order.completed outbox event")
	}
	if completedPayload.CoinsReward != 10000 || completedPayload.SubtotalVND != 1000000 || completedPayload.CommissionVND != 50000 {
		t.Fatalf("unexpected order.completed payload: %+v", completedPayload)
	}

	// Verify timeline recorded every step.
	tlResp, err := h.svc.GetOrderTimeline(buyerCtx, &orderv1.GetOrderTimelineRequest{OrderId: orderID})
	if err != nil {
		t.Fatalf("GetOrderTimeline failed: %v", err)
	}
	if len(tlResp.Events) < 6 {
		t.Fatalf("expected >= 6 timeline events (create, confirm, ship, delivering, delivered, completed), got %d", len(tlResp.Events))
	}
}

func TestCancelConfirmedPaidOrder_TriggersRefundAndRestock(t *testing.T) {
	h := newTestHarness()
	h.product.releaseReturnsOk = false // Simulate already-confirmed stock reservation
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-cancel-paid-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	orderID := resp.Orders[0].Id

	// Mark payment succeeded so order is CONFIRMED + PAID.
	if _, err := h.svc.MarkPaymentResult(ctxWithUser("payment-service", "system", ""), &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: resp.CheckoutGroupId,
		PaymentId:       resp.PaymentId,
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("MarkPaymentResult failed: %v", err)
	}

	// Buyer cancels before shipping -> triggers refund + restock.
	cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: orderID,
		Reason:  "changed mind",
	})
	if err != nil || cancelResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		t.Fatalf("CancelOrder failed: %v (resp=%+v)", err, cancelResp)
	}
	if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].AmountVnd != resp.Orders[0].TotalPayableVnd {
		t.Fatalf("expected refund call for %d VND, got %+v", resp.Orders[0].TotalPayableVnd, h.payment.refundCalls)
	}
	if len(h.product.updateStockCalls) != 1 || h.product.updateStockCalls[0].NewStock != 52 {
		t.Fatalf("expected UpdateStock restock to 52, got %+v", h.product.updateStockCalls)
	}
}

func TestRequestReturnAndShipmentReturned_RestocksAndRefunds(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-return-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	orderID := resp.Orders[0].Id

	_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-ret-pay", events.PaymentSucceededPayload{
		PaymentID:    resp.PaymentId,
		OrderGroupID: resp.CheckoutGroupId,
	})
	_, err = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: orderID, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder failed: %v", err)
	}
	if h.shipping.createShipCalls[0].CodAmountVnd != 0 {
		t.Fatalf("expected COD amount 0 for VietQR shipment, got %d", h.shipping.createShipCalls[0].CodAmountVnd)
	}

	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-deliv", events.ShipmentUpdatedPayload{
		OrderID: orderID,
		Status:  "delivered",
	})

	retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId:      orderID,
		Reason:       "defective key",
		EvidenceUrls: []string{"https://cdn.local/img1.jpg"},
	})
	if err != nil || retResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED || retResp.ReturnRequestId == "" {
		t.Fatalf("RequestReturn unexpected result: %+v err=%v", retResp, err)
	}

	// Shipment returned completes RETURN_REQUESTED -> RETURNED, restocks, and refunds!
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-done", events.ShipmentUpdatedPayload{
		OrderID: orderID,
		Status:  "returned",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(returned) failed: %v", err)
	}

	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: orderID})
	if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
		t.Fatalf("expected RETURNED, got %v", got.Order.Status)
	}
	if len(h.payment.refundCalls) != 1 {
		t.Fatalf("expected 1 refund call on returned paid order, got %d", len(h.payment.refundCalls))
	}
	if len(h.product.updateStockCalls) != 1 {
		t.Fatalf("expected 1 restock call on returned order, got %d", len(h.product.updateStockCalls))
	}
}

func TestExpiryWatchdog_UC_PAY06(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// Case A: Payment still PENDING past expiry -> cancel + compensate stock & promo.
	h := newTestHarness()
	h.payment.getPaymentStatus = paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-exp-1",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-1",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	h.store.SetOrderPaymentExpiresAt(resp.Orders[0].Id, time.Now().UTC().Add(-time.Minute))

	n, err := h.svc.ScanExpiredPendingOrders(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("expected 1 expired group handled, got %d (err=%v)", n, err)
	}
	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
	if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		t.Fatalf("expected expired order to be CANCELLED, got %v", got.Order.Status)
	}
	if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
		t.Fatalf("expected stock & promo release on expiry, got stock=%v promo=%v", h.product.releaseCalls, h.promo.releaseCalls)
	}

	// Case B: Payment actually SUCCEEDED at gateway -> watchdog CONFIRMS instead of cancelling!
	h2 := newTestHarness()
	h2.payment.getPaymentStatus = paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
	resp2, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-exp-2",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout 2 failed: %v", err)
	}
	h2.store.SetOrderPaymentExpiresAt(resp2.Orders[0].Id, time.Now().UTC().Add(-time.Minute))

	if _, err := h2.svc.ScanExpiredPendingOrders(context.Background()); err != nil {
		t.Fatalf("ScanExpiredPendingOrders 2 failed: %v", err)
	}
	got2, _ := h2.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp2.Orders[0].Id})
	if got2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		t.Fatalf("expected late-paid order to be CONFIRMED by watchdog, got %v", got2.Order.Status)
	}
	if len(h2.product.confirmCalls) != 1 {
		t.Fatalf("expected ConfirmStock when watchdog confirms late payment")
	}

	// Case C: PaymentService temporarily unreachable -> do NOT cancel!
	h3 := newTestHarness()
	h3.payment.getPaymentErr = status.Error(codes.Unavailable, "timeout")
	resp3, err := h3.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-exp-3",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout 3 failed: %v", err)
	}
	h3.store.SetOrderPaymentExpiresAt(resp3.Orders[0].Id, time.Now().UTC().Add(-time.Minute))

	n3, _ := h3.svc.ScanExpiredPendingOrders(context.Background())
	if n3 != 0 {
		t.Fatalf("expected 0 handled when payment service unreachable, got %d", n3)
	}
	got3, _ := h3.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp3.Orders[0].Id})
	if got3.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PENDING {
		t.Fatalf("expected order to remain PENDING when payment service unreachable, got %v", got3.Order.Status)
	}
}

func TestPaymentRefundedEvent_Idempotent(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-ref-ev",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	orderID := resp.Orders[0].Id

	payload := events.PaymentRefundedPayload{
		RefundID:  "ref-1",
		PaymentID: resp.PaymentId,
		OrderID:   orderID,
		AmountVND: resp.Orders[0].TotalPayableVnd,
		Reason:    "buyer cancelled",
	}
	if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-ref-1", payload); err != nil {
		t.Fatalf("HandlePaymentRefunded failed: %v", err)
	}
	if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-ref-1", payload); err != nil {
		t.Fatalf("duplicate HandlePaymentRefunded failed: %v", err)
	}
	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: orderID})
	if got.Order.PaymentStatus != "REFUNDED" {
		t.Fatalf("expected payment_status REFUNDED, got %s", got.Order.PaymentStatus)
	}
	if len(got.Order.Timeline) != 2 { // create + 1 refund event (not 2)
		t.Fatalf("expected 2 timeline events, got %d", len(got.Order.Timeline))
	}
}

func TestListOrders_RoleScoping(t *testing.T) {
	h := newTestHarness()
	b1 := ctxWithUser("buyer-1", "buyer", "")
	s1 := ctxWithUser("seller-1", "seller", "shop-1")
	s2 := ctxWithUser("seller-2", "seller", "shop-2")
	admin := ctxWithUser("admin-1", "admin", "")

	_, err := h.svc.CreateCheckout(b1, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-list-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	buyerList, err := h.svc.ListOrders(b1, &orderv1.ListOrdersRequest{RoleScope: "buyer"})
	if err != nil || buyerList.Total != 2 {
		t.Fatalf("expected buyer total=2, got total=%d err=%v", buyerList.Total, err)
	}

	seller1List, err := h.svc.ListOrders(s1, &orderv1.ListOrdersRequest{RoleScope: "seller"})
	if err != nil || seller1List.Total != 1 || seller1List.Orders[0].ShopId != "shop-1" {
		t.Fatalf("expected seller1 total=1 shop-1, got %+v err=%v", seller1List, err)
	}

	if _, err := h.svc.ListOrders(s2, &orderv1.ListOrdersRequest{RoleScope: "seller", ShopId: "shop-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied when seller2 queries shop-1, got %v", err)
	}

	adminList, err := h.svc.ListOrders(admin, &orderv1.ListOrdersRequest{RoleScope: "admin", ShopId: "shop-2"})
	if err != nil || adminList.Total != 1 || adminList.Orders[0].ShopId != "shop-2" {
		t.Fatalf("expected admin filtered total=1 shop-2, got %+v err=%v", adminList, err)
	}
}

func TestCreateCheckout_EmptyIdempotencyKeyRejected(t *testing.T) {
	h := newTestHarness()
	_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		AddressId:     "addr-buyer-1",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for missing idempotency_key, got %v", err)
	}
}

func TestCancelPendingVietQROrder_CompensatesGroup(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-cancel-pending-multi",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		FromCart:          true,
		PlatformVoucherId: "v-1",
		CoinsToUse:        2000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil || len(resp.Orders) != 2 {
		t.Fatalf("expected 2 PENDING VietQR orders, got %+v err=%v", resp, err)
	}

	cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: resp.Orders[0].Id,
		Reason:  "changed payment plan",
	})
	if err != nil || cancelResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		t.Fatalf("CancelOrder failed: %v", err)
	}

	// Both sibling PENDING orders in the unpaid group should be cancelled, and stock+promo+payment compensated.
	aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), resp.CheckoutGroupId)
	for _, a := range aggs {
		if a.Order.Status != "CANCELLED" {
			t.Fatalf("expected sibling order %s to be CANCELLED, got %s", a.Order.ID, a.Order.Status)
		}
	}
	if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 || len(h.payment.cancelCalls) != 1 {
		t.Fatalf("expected stock/promo/payment release on pending cancel, got stock=%d promo=%d pay=%d",
			len(h.product.releaseCalls), len(h.promo.releaseCalls), len(h.payment.cancelCalls))
	}
}

func TestHandlePaymentExpiredEvent_CompensatesStockAndPromotions(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-pay-expired-ev",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-1",
		CoinsToUse:        500,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	if err := h.svc.HandlePaymentExpired(context.Background(), "evt-pay-exp-1", events.PaymentFailedPayload{
		PaymentID:    resp.PaymentId,
		OrderGroupID: resp.CheckoutGroupId,
		Reason:       "QR timed out",
	}); err != nil {
		t.Fatalf("HandlePaymentExpired failed: %v", err)
	}

	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
	if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || got.Order.PaymentStatus != "EXPIRED" {
		t.Fatalf("expected CANCELLED/EXPIRED, got status=%v pay_status=%s", got.Order.Status, got.Order.PaymentStatus)
	}
	if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
		t.Fatalf("expected stock & promo release on payment.expired")
	}
}

func TestCreateCheckout_FallbackProportionalDiscountBreakdown(t *testing.T) {
	h := newTestHarness()
	// PromotionService returns aggregate totals without per-shop breakdown.
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		TotalPlatformVoucherDiscountVnd: 100000,
		TotalFreeshipDiscountVnd:        20000,
		TotalCoinsDiscountVnd:           10000,
	}

	resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-fallback-breakdown",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		FromCart:          true,
		PlatformVoucherId: "v-plat",
		FreeshipVoucherId: "v-free",
		CoinsToUse:        10000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	var sumPlat, sumFree, sumCoins int64
	for _, o := range resp.Orders {
		sumPlat += o.PlatformVoucherDiscountVnd
		sumFree += o.FreeshipDiscountVnd
		sumCoins += o.CoinsDiscountVnd
	}
	if sumPlat != 100000 || sumFree != 20000 || sumCoins != 10000 {
		t.Fatalf("expected exact proportional sum preservation (100000, 20000, 10000), got (%d, %d, %d)", sumPlat, sumFree, sumCoins)
	}
}

func TestUnauthenticatedDirectGRPCRejected(t *testing.T) {
	h := newTestHarness()
	unauth := context.Background()

	if _, err := h.svc.CreateCheckout(unauth, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k1", BuyerId: "buyer-1", AddressId: "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("CreateCheckout unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.GetOrder(unauth, &orderv1.GetOrderRequest{OrderId: "o1", ActorRole: "admin"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("GetOrder unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.GetOrderTimeline(unauth, &orderv1.GetOrderTimelineRequest{OrderId: "o1", ActorRole: "admin"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("GetOrderTimeline unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.ListOrders(unauth, &orderv1.ListOrdersRequest{RoleScope: "admin"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ListOrders unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.CancelOrder(unauth, &orderv1.CancelOrderRequest{OrderId: "o1", ActorRole: "admin"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("CancelOrder unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.SellerConfirmOrder(unauth, &orderv1.SellerConfirmOrderRequest{OrderId: "o1", ShopId: "shop-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("SellerConfirmOrder unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.SellerShipOrder(unauth, &orderv1.SellerShipOrderRequest{OrderId: "o1", ShopId: "shop-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("SellerShipOrder unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.BuyerConfirmReceived(unauth, &orderv1.BuyerConfirmReceivedRequest{OrderId: "o1", BuyerId: "buyer-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("BuyerConfirmReceived unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.RequestReturn(unauth, &orderv1.RequestReturnRequest{OrderId: "o1", BuyerId: "buyer-1", Reason: "x"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("RequestReturn unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.MarkPaymentResult(unauth, &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "g1", Status: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("MarkPaymentResult unauth = %v, want Unauthenticated", err)
	}
	if _, err := h.svc.MarkPaymentResult(ctxWithUser("buyer-1", "buyer", ""), &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "g1", Status: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("MarkPaymentResult buyer = %v, want PermissionDenied", err)
	}
}

func TestCreateCheckout_InactiveSKURejected(t *testing.T) {
	h := newTestHarness()
	h.product.skus["sku-1"].IsActive = false

	_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-inactive-sku",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for inactive SKU, got %v", err)
	}
	if len(h.product.reserveCalls) != 0 {
		t.Fatalf("should not reserve stock for inactive SKU")
	}
}

func TestInternalAdminCalls_StripBuyerJWTForPickupAddressAndRestock(t *testing.T) {
	h := newTestHarness()
	h.product.releaseReturnsOk = false // Force restockOrderItems -> Product.UpdateStock path

	// Attach a raw buyer Bearer token to incoming context (mirroring Gateway -> Order gRPC call).
	buyerCtx := auth.WithRawToken(ctxWithUser("buyer-1", "buyer", ""), "Bearer buyer-jwt-token")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-internal-ctx-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	// Verify resolveShopPickupCodes fetched shop-1's pickup address using internal admin context (no raw buyer JWT)
	// and passed shop-1's Ha Noi province ("01") instead of falling back to buyer's HCMC ("79")!
	shopAddrAuth, ok := h.user.getAddressAuth["addr-shop-1"]
	if !ok {
		t.Fatalf("expected GetAddress call for shop-1 pickup address addr-shop-1")
	}
	if shopAddrAuth.role != "admin" || shopAddrAuth.rawToken != "" {
		t.Fatalf("pickup address lookup must use internal admin without raw buyer token, got %+v", shopAddrAuth)
	}
	if len(h.shipping.calculateFeeReqs) != 1 || h.shipping.calculateFeeReqs[0].GetFromProvinceCode() != "01" {
		t.Fatalf("expected CalculateFee from_province_code=01, got %+v", h.shipping.calculateFeeReqs)
	}

	// Buyer cancels the CONFIRMED COD order -> triggers restockOrderItems -> ProductService.UpdateStock.
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: resp.Orders[0].Id,
		Reason:  "buyer changed mind",
	}); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}

	if len(h.product.updateStockAuth) != 1 {
		t.Fatalf("expected 1 UpdateStock restock call, got %d", len(h.product.updateStockAuth))
	}
	if got := h.product.updateStockAuth[0]; got.role != "admin" || got.rawToken != "" {
		t.Fatalf("restock UpdateStock must use internal admin without raw buyer JWT, got %+v", got)
	}
}

func TestPaymentRefundedEvent_GroupLevelFallbackUpdatesAllSubOrders(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-group-refund-ev",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil || len(resp.Orders) != 2 {
		t.Fatalf("expected 2 sub-orders in checkout group, got %+v err=%v", resp, err)
	}

	// Admin refunds entire checkout group (OrderID == CheckoutGroupId).
	payload := events.PaymentRefundedPayload{
		RefundID:  "ref-group-1",
		PaymentID: resp.PaymentId,
		OrderID:   resp.CheckoutGroupId,
		AmountVND: resp.TotalPayableVnd,
		Reason:    "full group refund",
	}
	if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-grp-ref-1", payload); err != nil {
		t.Fatalf("HandlePaymentRefunded failed: %v", err)
	}
	// Duplicate delivery must be strictly idempotent.
	if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-grp-ref-1", payload); err != nil {
		t.Fatalf("duplicate HandlePaymentRefunded failed: %v", err)
	}

	for _, o := range resp.Orders {
		got, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: o.Id})
		if err != nil {
			t.Fatalf("GetOrder(%s) failed: %v", o.Id, err)
		}
		if got.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("order %s payment_status = %q, want REFUNDED", o.Id, got.Order.PaymentStatus)
		}
		if len(got.Order.Timeline) != 2 {
			t.Fatalf("order %s expected 2 timeline events (create + 1 refund), got %d", o.Id, len(got.Order.Timeline))
		}
	}
}
