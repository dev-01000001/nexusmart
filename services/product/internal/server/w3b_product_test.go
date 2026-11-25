package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/resilience"
	"github.com/nexusmart/core/services/product/internal/store"
)

type fakeOrderClient struct {
	mu       sync.Mutex
	calls    int
	lastAuth string
	fn       func(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error)
}

func (f *fakeOrderClient) GetOrder(ctx context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	f.mu.Lock()
	f.calls++
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get(auth.MDAuthorization); len(vals) > 0 {
			f.lastAuth = vals[0]
		}
	}
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return fn(ctx, in)
}

type fakeUserClient struct {
	mu       sync.Mutex
	calls    int
	lastAuth string
	fn       func(ctx context.Context, req *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error)
}

func (f *fakeUserClient) GetProfile(ctx context.Context, in *userv1.GetProfileRequest, _ ...grpc.CallOption) (*userv1.GetProfileResponse, error) {
	f.mu.Lock()
	f.calls++
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get(auth.MDAuthorization); len(vals) > 0 {
			f.lastAuth = vals[0]
		}
	}
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	return fn(ctx, in)
}

func ctxWithRoleAndToken(userID, role, shopID, rawBearer string) context.Context {
	ctx := ctxWithRole(userID, role, shopID)
	if rawBearer != "" {
		ctx = auth.WithRawToken(ctx, rawBearer)
	}
	return ctx
}

func TestW3B_MaskBuyerName_E5(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Nguyễn Văn Linh", "N. Linh"},
		{"  đặng   ngọc   Ánh ", "Đ. Ánh"},
		{"Trần An", "T. An"},
		{"Linh", "L***"},
		{"ân", "Â***"},
		{"buyer@example.com", "B***"},
		{"Nguyễn buyer@example.com", "N***"},
		{"", DefaultAnonymousBuyerName},
		{"   ", DefaultAnonymousBuyerName},
	}
	for _, tc := range cases {
		if got := MaskBuyerName(tc.in); got != tc.want {
			t.Errorf("MaskBuyerName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestW3B_ComputeRatingAvgX100(t *testing.T) {
	cases := []struct {
		sum   int64
		count int32
		want  int32
	}{
		{0, 0, 0},
		{5, 1, 500},
		{9, 2, 450},
		{14, 3, 467},
		{13, 3, 433},
		{19, 4, 475},
		{1, 1, 100},
		{999, 1, 500}, // clamped at 500
	}
	for _, tc := range cases {
		if got := store.ComputeRatingAvgX100(tc.sum, tc.count); got != tc.want {
			t.Errorf("ComputeRatingAvgX100(%d, %d) = %d, want %d", tc.sum, tc.count, got, tc.want)
		}
	}
}

func TestW3B_Reviews_Auth_Ownership_Status_Images_Banned_Masking_PublicRedaction(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerACtx := ctxWithRoleAndToken("buyer-a", "buyer", "", "Bearer jwt-buyer-a")
	buyerBCtx := ctxWithRoleAndToken("buyer-b", "buyer", "", "Bearer jwt-buyer-b")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Áo thun", Slug: "ao-thun", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	prodResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catResp.GetCategory().GetId(),
		Name:       "Áo thun cotton",
		Slug:       "ao-thun-cotton",
		Skus:       []*productv1.SKUInput{{SkuCode: "AT-M", Name: "Trắng / M", PriceVnd: 150000, Stock: 20}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := prodResp.GetProduct().GetId()
	skuID := prodResp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Add one BLOCK and one REVIEW banned keyword (E8: both reject reviews with BANNED_CONTENT).
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "hàng giả", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword BLOCK: %v", err)
	}
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "cam kết 100%", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword REVIEW: %v", err)
	}

	orders := map[string]*orderv1.Order{
		"ord-completed": {
			Id:      "ord-completed",
			BuyerId: "buyer-a",
			ShopId:  "shop-1",
			Status:  orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
			Items: []*orderv1.OrderItem{
				{Id: "oi-1", OrderId: "ord-completed", ProductId: prodID, SkuId: skuID, SkuName: "Trắng / M"},
				{Id: "oi-2", OrderId: "ord-completed", ProductId: prodID, SkuId: skuID, SkuName: "Trắng / M"},
			},
		},
		"ord-delivered": {
			Id:      "ord-delivered",
			BuyerId: "buyer-a",
			ShopId:  "shop-1",
			Status:  orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
			Items: []*orderv1.OrderItem{
				{Id: "oi-deliv", OrderId: "ord-delivered", ProductId: prodID, SkuId: skuID, SkuName: "Trắng / M"},
			},
		},
	}

	ordClient := &fakeOrderClient{
		fn: func(_ context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
			ord, ok := orders[req.GetOrderId()]
			if !ok {
				return nil, status.Error(codes.NotFound, "not found")
			}
			return &orderv1.GetOrderResponse{Order: ord}, nil
		},
	}
	usrClient := &fakeUserClient{
		fn: func(_ context.Context, req *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
			return &userv1.GetProfileResponse{
				User: &userv1.User{Id: req.GetUserId(), FullName: "Nguyễn Văn Linh"},
			}, nil
		},
	}
	srv.SetOrderClient(ordClient)
	srv.SetUserClient(usrClient)

	// Attack 1: Buyer B tries to review Buyer A's completed order -> PermissionDenied.
	if _, err := srv.CreateReview(buyerBCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-completed", OrderItemId: "oi-1", Rating: 5, Comment: "Tốt",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Buyer B CreateReview on Buyer A's order: want PermissionDenied, got %v", err)
	}
	// Attack 2: Admin tries to review Buyer A's order (even though OrderService returned order) -> PermissionDenied.
	if _, err := srv.CreateReview(adminCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-completed", OrderItemId: "oi-1", Rating: 5, Comment: "Admin spoof",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Admin CreateReview on Buyer A's order: want PermissionDenied, got %v", err)
	}
	// Attack 3: Buyer B calls ListOrderReviews on Buyer A's order -> PermissionDenied.
	if _, err := srv.ListOrderReviews(buyerBCtx, &productv1.ListOrderReviewsRequest{OrderId: "ord-completed"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Buyer B ListOrderReviews on Buyer A's order: want PermissionDenied, got %v", err)
	}
	// Attack 4: Non-COMPLETED order (DELIVERED) -> FailedPrecondition.
	if _, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
		OrderId: "ord-delivered", OrderItemId: "oi-deliv", Rating: 5, Comment: "Chưa hoàn tất",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("DELIVERED order CreateReview: want FailedPrecondition, got %v", err)
	}
	// Attack 5: Order item not in order -> NotFound.
	if _, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
		OrderId: "ord-completed", OrderItemId: "oi-wrong", Rating: 5,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("wrong order_item_id: want NotFound, got %v", err)
	}

	// E7: Image URL validation rejects unsafe schemes, >5 URLs, >500 chars, path traversal.
	badURLCases := [][]string{
		{"http://cdn.example.com/a.jpg"},
		{"javascript:alert(1)"},
		{"data:image/png;base64,AAAA"},
		{"//evil.com/a.jpg"},
		{"/products/../etc/passwd"},
		{"https://cdn.example.com/" + strings.Repeat("a", 500)},
		{"https://a.test/1.jpg", "https://a.test/2.jpg", "https://a.test/3.jpg", "https://a.test/4.jpg", "https://a.test/5.jpg", "https://a.test/6.jpg"},
	}
	for i, urls := range badURLCases {
		if _, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
			OrderId: "ord-completed", OrderItemId: "oi-1", Rating: 5, ImageUrls: urls,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("bad image_urls case %d (%v): want InvalidArgument, got %v", i, urls, err)
		}
	}

	// E8 + E10-bis: Both BLOCK (with leet evasion) and REVIEW keywords reject review with BANNED_CONTENT without leaking keyword text.
	for _, comment := range []string{"Shop bán h@ng gi4", "Trà này cam kết 100% trị bệnh"} {
		_, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
			OrderId: "ord-completed", OrderItemId: "oi-1", Rating: 4, Comment: comment,
		})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
			t.Fatalf("banned review comment %q: want InvalidArgument BANNED_CONTENT, got %v", comment, err)
		}
		if strings.Contains(err.Error(), "hàng giả") || strings.Contains(err.Error(), "cam kết 100%") {
			t.Fatalf("E10-bis violated: buyer review error leaked matched keyword rule: %v", err)
		}
	}

	// Valid review #1: 5 stars with https:// and /products/... images.
	rev1, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
		OrderId:     "ord-completed",
		OrderItemId: "oi-1",
		Rating:      5,
		Comment:     "Vải mát, đóng gói cẩn thận!",
		ImageUrls:   []string{"https://cdn.example.com/rev1.jpg", "/products/local-photo.png"},
	})
	if err != nil {
		t.Fatalf("CreateReview #1: %v", err)
	}
	if rev1.GetReview().GetBuyerName() != "N. Linh" {
		t.Fatalf("masked buyer_name = %q, want %q", rev1.GetReview().GetBuyerName(), "N. Linh")
	}
	if ordClient.lastAuth != "Bearer jwt-buyer-a" || usrClient.lastAuth != "Bearer jwt-buyer-a" {
		t.Fatalf("expected buyer JWT forwarded to order (%q) and user (%q)", ordClient.lastAuth, usrClient.lastAuth)
	}

	// Duplicate order_item_id -> AlreadyExists.
	if _, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
		OrderId: "ord-completed", OrderItemId: "oi-1", Rating: 3, Comment: "Đánh giá lại",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate order_item_id: want AlreadyExists, got %v", err)
	}

	// Valid review #2 when UserService fails -> falls back to DefaultAnonymousBuyerName (E5).
	usrClient.fn = func(_ context.Context, _ *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
		return nil, status.Error(codes.Unavailable, "user down")
	}
	now = now.Add(time.Minute)
	rev2, err := srv.CreateReview(buyerACtx, &productv1.CreateReviewRequest{
		OrderId:     "ord-completed",
		OrderItemId: "oi-2",
		Rating:      4,
		Comment:     "Tạm ổn",
	})
	if err != nil {
		t.Fatalf("CreateReview #2 with user down: %v", err)
	}
	if rev2.GetReview().GetBuyerName() != DefaultAnonymousBuyerName {
		t.Fatalf("fallback buyer_name = %q, want %q", rev2.GetReview().GetBuyerName(), DefaultAnonymousBuyerName)
	}

	// Verify outbox review.created events (2 total, latest carries review_count=2, rating_avg_x100=450,
	// includes comment, and omits PII/private fields buyer_id and order_id).
	var reviewOutbox []events.ReviewCreatedPayload
	for _, ev := range mem.OutboxEvents() {
		if ev.EventType == events.SubjectReviewCreated {
			var raw map[string]any
			if uErr := json.Unmarshal(ev.PayloadJSON, &raw); uErr != nil {
				t.Fatalf("unmarshal review.created raw map: %v", uErr)
			}
			if _, leaked := raw["buyer_id"]; leaked {
				t.Fatalf("review.created payload leaked buyer_id: %s", string(ev.PayloadJSON))
			}
			if _, leaked := raw["order_id"]; leaked {
				t.Fatalf("review.created payload leaked order_id: %s", string(ev.PayloadJSON))
			}
			var p events.ReviewCreatedPayload
			if uErr := json.Unmarshal(ev.PayloadJSON, &p); uErr != nil {
				t.Fatalf("unmarshal review.created: %v", uErr)
			}
			reviewOutbox = append(reviewOutbox, p)
		}
	}
	if len(reviewOutbox) != 2 || reviewOutbox[1].ReviewCount != 2 || reviewOutbox[1].RatingAvgX100 != 450 {
		t.Fatalf("review.created outbox = %+v, want 2 events with final count=2 avg=450", reviewOutbox)
	}
	if reviewOutbox[1].Comment != "Tạm ổn" || reviewOutbox[0].Comment == "" {
		t.Fatalf("review.created outbox comments = [%q, %q], want non-empty and %q", reviewOutbox[0].Comment, reviewOutbox[1].Comment, "Tạm ổn")
	}

	// E6-bis: Public ListProductReviews omits buyer_id, order_id, order_item_id, sku_id; returns rating_avg_x100=450, review_count=2.
	pubList, err := srv.ListProductReviews(context.Background(), &productv1.ListProductReviewsRequest{
		ProductId: prodID, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListProductReviews: %v", err)
	}
	if pubList.GetTotal() != 2 || pubList.GetReviewCount() != 2 || pubList.GetRatingAvgX100() != 450 {
		t.Fatalf("ListProductReviews summary = total:%d count:%d avg:%d, want 2/2/450",
			pubList.GetTotal(), pubList.GetReviewCount(), pubList.GetRatingAvgX100())
	}
	if pubList.GetRatingCounts()[5] != 1 || pubList.GetRatingCounts()[4] != 1 || pubList.GetRatingCounts()[3] != 0 {
		t.Fatalf("rating_counts = %v, want {5:1, 4:1}", pubList.GetRatingCounts())
	}
	for _, r := range pubList.GetReviews() {
		if r.GetBuyerId() != "" || r.GetOrderId() != "" || r.GetOrderItemId() != "" || r.GetSkuId() != "" {
			t.Fatalf("E6-bis violated: public ListProductReviews leaked private fields: %+v", r)
		}
		if r.GetSkuName() == "" || r.GetBuyerName() == "" {
			t.Fatalf("public ListProductReviews missing display fields: %+v", r)
		}
	}
	// Filter by has_images=true returns only rev1.
	imgList, err := srv.ListProductReviews(context.Background(), &productv1.ListProductReviewsRequest{
		ProductId: prodID, HasImages: true,
	})
	if err != nil || imgList.GetTotal() != 1 || imgList.GetReviews()[0].GetId() != rev1.GetReview().GetId() {
		t.Fatalf("ListProductReviews(has_images=true) = %+v err=%v", imgList, err)
	}

	// E6-bis: ListOrderReviews for buyer owner returns full fields (buyer_id, order_id, order_item_id, sku_id).
	ordRevs, err := srv.ListOrderReviews(buyerACtx, &productv1.ListOrderReviewsRequest{OrderId: "ord-completed"})
	if err != nil || len(ordRevs.GetReviews()) != 2 {
		t.Fatalf("ListOrderReviews = %+v err=%v", ordRevs, err)
	}
	for _, r := range ordRevs.GetReviews() {
		if r.GetBuyerId() != "buyer-a" || r.GetOrderId() != "ord-completed" || r.GetOrderItemId() == "" || r.GetSkuId() != skuID {
			t.Fatalf("ListOrderReviews missing private fields: %+v", r)
		}
	}

	// Brief #4: Every public Product endpoint returns identical rating_avg_x100=450, review_count=2.
	gpByID, err := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Id: prodID})
	if err != nil || gpByID.GetProduct().GetRatingAvgX100() != 450 || gpByID.GetProduct().GetReviewCount() != 2 {
		t.Fatalf("GetProduct(id) rating = %d/%d err=%v", gpByID.GetProduct().GetRatingAvgX100(), gpByID.GetProduct().GetReviewCount(), err)
	}
	gpBySlug, err := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Slug: "ao-thun-cotton"})
	if err != nil || gpBySlug.GetProduct().GetRatingAvgX100() != 450 || gpBySlug.GetProduct().GetReviewCount() != 2 {
		t.Fatalf("GetProduct(slug) rating = %d/%d err=%v", gpBySlug.GetProduct().GetRatingAvgX100(), gpBySlug.GetProduct().GetReviewCount(), err)
	}
	lpAll, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{})
	if err != nil || len(lpAll.GetProducts()) != 1 || lpAll.GetProducts()[0].GetRatingAvgX100() != 450 || lpAll.GetProducts()[0].GetReviewCount() != 2 {
		t.Fatalf("ListProducts rating = %+v err=%v", lpAll.GetProducts(), err)
	}
	lpSKU, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: []string{skuID}})
	if err != nil || len(lpSKU.GetProducts()) != 1 || lpSKU.GetProducts()[0].GetRatingAvgX100() != 450 || lpSKU.GetProducts()[0].GetReviewCount() != 2 {
		t.Fatalf("ListProducts(sku_ids) rating = %+v err=%v", lpSKU.GetProducts(), err)
	}
}

func TestW3B_OrderClientFailureTimeoutAndCircuitBreaker_NoWrite(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	srv.SetOrderBreaker(resilience.NewCircuitBreaker(2, time.Minute))
	srv.SetUpstreamTimeout(40 * time.Millisecond)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRoleAndToken("buyer-a", "buyer", "", "Bearer tok")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "C", Slug: "c-cb", CommissionRateBps: 500})
	prod, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(),
		Name:       "Prod CB",
		Skus:       []*productv1.SKUInput{{SkuCode: "CB-1", PriceVnd: 100000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prod.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})

	// 1. Nil order client -> Unavailable, nothing written.
	if _, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-1", OrderItemId: "oi-1", Rating: 5,
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("nil orderClient: want Unavailable, got %v", err)
	}

	// 2. Slow order server exceeding timeout -> trips circuit breaker, returns Unavailable, nothing written.
	var calls atomic.Int32
	srv.SetOrderClient(&fakeOrderClient{
		fn: func(ctx context.Context, _ *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
			calls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if _, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-1", OrderItemId: "oi-1", Rating: 5,
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("timeout orderClient: want Unavailable, got %v", err)
	}
	callsAfterTrip := calls.Load()
	if callsAfterTrip != 2 {
		t.Fatalf("expected 2 attempts to trip breaker(max=2), got %d", callsAfterTrip)
	}

	// 3. Breaker is now OPEN -> fast-fails with Unavailable without calling orderClient again!
	if _, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-1", OrderItemId: "oi-1", Rating: 5,
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("open breaker CreateReview: want Unavailable, got %v", err)
	}
	if calls.Load() != callsAfterTrip {
		t.Fatalf("open breaker still invoked orderClient (%d vs %d)", calls.Load(), callsAfterTrip)
	}

	// Verify nothing was written to reviews or product rating counters.
	gp, _ := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Id: prod.GetProduct().GetId()})
	if gp.GetProduct().GetReviewCount() != 0 || gp.GetProduct().GetRatingAvgX100() != 0 {
		t.Fatalf("product rating mutated on order error: count=%d avg=%d", gp.GetProduct().GetReviewCount(), gp.GetProduct().GetRatingAvgX100())
	}
}

func TestW3B_Moderation_ProhibitedCategory_BannedKeywords_Preview_Rescan_E10_E11(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	otherSellerCtx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Category hierarchy: normalRoot, prohibitedParent -> prohibitedChild -> prohibitedGrandchild.
	normalCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Đồ gia dụng", Slug: "do-gia-dung", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory normal: %v", err)
	}
	prohibParent, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Hàng hạn chế", Slug: "hang-han-che", CommissionRateBps: 500, IsProhibited: true,
	})
	if err != nil || !prohibParent.GetCategory().GetIsProhibited() {
		t.Fatalf("CreateCategory prohibited parent: resp=%+v err=%v", prohibParent, err)
	}
	// Partial UpdateCategory without has_is_prohibited must preserve IsProhibited=true!
	updKeep, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id: prohibParent.GetCategory().GetId(), Name: "Hàng cấm",
	})
	if err != nil || !updKeep.GetCategory().GetIsProhibited() {
		t.Fatalf("partial UpdateCategory reset is_prohibited: %+v err=%v", updKeep, err)
	}
	prohibChild, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Con cấm", Slug: "con-cam", ParentId: prohibParent.GetCategory().GetId(), CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory child: %v", err)
	}
	prohibGrand, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Cháu cấm", Slug: "chau-cam", ParentId: prohibChild.GetCategory().GetId(), CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory grandchild: %v", err)
	}

	// E10: CreateProduct in prohibited parent OR descendant -> InvalidArgument PROHIBITED_CATEGORY.
	for _, catID := range []string{prohibParent.GetCategory().GetId(), prohibChild.GetCategory().GetId(), prohibGrand.GetCategory().GetId()} {
		_, cErr := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "Sản phẩm thường",
			Skus:       []*productv1.SKUInput{{SkuCode: "P-1", PriceVnd: 100000, Stock: 5}},
		})
		if status.Code(cErr) != codes.InvalidArgument || !strings.Contains(cErr.Error(), "PROHIBITED_CATEGORY") {
			t.Fatalf("CreateProduct in prohibited cat %s: want PROHIBITED_CATEGORY, got %v", catID, cErr)
		}
	}

	// Banned keywords CRUD + normalization duplicate check.
	kwBlock, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "ma túy", Category: "ILLEGAL", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	})
	if err != nil {
		t.Fatalf("CreateBannedKeyword BLOCK: %v", err)
	}
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "  МА   TÚY!! ", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate normalized banned keyword: want AlreadyExists, got %v", err)
	}
	kwReview, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "cam kết 100%", Category: "CLAIMS", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})
	if err != nil {
		t.Fatalf("CreateBannedKeyword REVIEW: %v", err)
	}
	// Non-admin rejected.
	if _, err := srv.ListBannedKeywords(sellerCtx, &productv1.ListBannedKeywordsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ListBannedKeywords: want PermissionDenied, got %v", err)
	}

	// E10: CreateProduct with BLOCK keyword (evasion "m.a t.u.y") -> InvalidArgument BANNED_CONTENT + matched term.
	_, err = srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: normalCat.GetCategory().GetId(),
		Name:       "Thuốc m.a t.u.y giá rẻ",
		Skus:       []*productv1.SKUInput{{SkuCode: "B-1", PriceVnd: 100000, Stock: 5}},
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") || !strings.Contains(err.Error(), "ma túy") {
		t.Fatalf("CreateProduct BLOCK evasion: want BANNED_CONTENT with term, got %v", err)
	}

	// E10: CreateProduct with REVIEW keyword -> created as PENDING with flagged_keywords.
	revProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  normalCat.GetCategory().GetId(),
		Name:        "Trà thảo mộc",
		Description: "Cam kết 100% thiên nhiên",
		Slug:        "tra-thao-moc",
		Skus:        []*productv1.SKUInput{{SkuCode: "R-1", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct REVIEW: %v", err)
	}
	if revProd.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(revProd.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
		t.Fatalf("CreateProduct REVIEW: status=%v flagged=%v", revProd.GetProduct().GetStatus(), revProd.GetProduct().GetFlaggedKeywords())
	}

	// E10: AdminReviewProduct(APPROVE) MUST allow REVIEW-flagged products!
	approvedRev, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: revProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || approvedRev.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("AdminReviewProduct(APPROVE) on REVIEW-flagged product: resp=%+v err=%v", approvedRev, err)
	}

	// E10-bis: flagged_keywords visible ONLY to admin and owning seller; empty for public, buyer, and other seller!
	for label, caller := range map[string]context.Context{
		"anon":         context.Background(),
		"buyer":        buyerCtx,
		"other_seller": otherSellerCtx,
	} {
		gp, gErr := srv.GetProduct(caller, &productv1.GetProductRequest{Id: revProd.GetProduct().GetId()})
		if gErr != nil || len(gp.GetProduct().GetFlaggedKeywords()) != 0 {
			t.Fatalf("E10-bis %s GetProduct leaked flagged_keywords: %v err=%v", label, gp.GetProduct().GetFlaggedKeywords(), gErr)
		}
		lp, lErr := srv.ListProducts(caller, &productv1.ListProductsRequest{})
		if lErr != nil || len(lp.GetProducts()) == 0 || len(lp.GetProducts()[0].GetFlaggedKeywords()) != 0 {
			t.Fatalf("E10-bis %s ListProducts leaked flagged_keywords: %+v err=%v", label, lp.GetProducts(), lErr)
		}
	}
	for label, caller := range map[string]context.Context{
		"owner_seller": sellerCtx,
		"admin":        adminCtx,
	} {
		gp, gErr := srv.GetProduct(caller, &productv1.GetProductRequest{Id: revProd.GetProduct().GetId()})
		if gErr != nil || !reflect.DeepEqual(gp.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
			t.Fatalf("E10-bis %s GetProduct missing flagged_keywords: %v err=%v", label, gp.GetProduct().GetFlaggedKeywords(), gErr)
		}
	}

	// E10: Editing an APPROVED clean product to add a REVIEW keyword demotes it back to PENDING!
	cleanProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: normalCat.GetCategory().GetId(),
		Name:       "Bình giữ nhiệt",
		Slug:       "binh-giu-nhiet",
		Skus:       []*productv1.SKUInput{{SkuCode: "CL-1", PriceVnd: 180000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct clean: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: cleanProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve clean: %v", err)
	}
	// Editing into prohibited child category -> rejected!
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: cleanProd.GetProduct().GetId(), CategoryId: prohibGrand.GetCategory().GetId(),
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROHIBITED_CATEGORY") {
		t.Fatalf("UpdateProduct into prohibited descendant: want PROHIBITED_CATEGORY, got %v", err)
	}
	// Editing to add REVIEW keyword -> demoted to PENDING + disappears from public GetProduct!
	demoted, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: cleanProd.GetProduct().GetId(), Description: "Cam kết 100% giữ nhiệt 48h",
	})
	if err != nil {
		t.Fatalf("UpdateProduct REVIEW demote: %v", err)
	}
	if demoted.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(demoted.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
		t.Fatalf("expected demotion to PENDING with flagged_keywords, got status=%v flagged=%v",
			demoted.GetProduct().GetStatus(), demoted.GetProduct().GetFlaggedKeywords())
	}
	if _, err := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Id: cleanProd.GetProduct().GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("demoted PENDING product still visible publicly: %v", err)
	}

	// E10: AdminReviewProduct(APPROVE) blocks newly-added BLOCK keyword or newly-prohibited category!
	lateCat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Later", Slug: "later-prohib", CommissionRateBps: 500})
	lateProd, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: lateCat.GetCategory().GetId(),
		Name:       "Hàng xịn siêu cấp",
		Skus:       []*productv1.SKUInput{{SkuCode: "L-1", PriceVnd: 100000, Stock: 5}},
	})
	// Mark category prohibited after product creation -> Admin APPROVE must reject!
	if _, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id: lateCat.GetCategory().GetId(), IsProhibited: true, HasIsProhibited: true,
	}); err != nil {
		t.Fatalf("UpdateCategory is_prohibited=true: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: lateProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROHIBITED_CATEGORY") {
		t.Fatalf("AdminReviewProduct(APPROVE) prohibited category: want PROHIBITED_CATEGORY, got %v", err)
	}
	// Clear prohibited flag, add BLOCK keyword matching product -> Admin APPROVE must reject BANNED_CONTENT!
	_, _ = srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id: lateCat.GetCategory().GetId(), IsProhibited: false, HasIsProhibited: true,
	})
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "siêu cấp", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword siêu cấp: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: lateProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("AdminReviewProduct(APPROVE) BLOCK keyword: want BANNED_CONTENT, got %v", err)
	}

	// E11: PreviewBannedKeyword counts all matching products and caps samples at 20.
	for i := 0; i < 23; i++ {
		_, pErr := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: normalCat.GetCategory().GetId(),
			Name:       fmt.Sprintf("Sản phẩm preview đặc biệt %02d", i),
			Skus:       []*productv1.SKUInput{{SkuCode: fmt.Sprintf("PRV-%02d", i), PriceVnd: 50000, Stock: 3}},
		})
		if pErr != nil {
			t.Fatalf("seed preview product %d: %v", i, pErr)
		}
	}
	prevResp, err := srv.PreviewBannedKeyword(adminCtx, &productv1.PreviewBannedKeywordRequest{Term: "preview đặc biệt"})
	if err != nil {
		t.Fatalf("PreviewBannedKeyword: %v", err)
	}
	if prevResp.GetAffectedCount() != 23 || len(prevResp.GetSamples()) != 20 {
		t.Fatalf("PreviewBannedKeyword: affected=%d samples=%d, want 23 and 20", prevResp.GetAffectedCount(), len(prevResp.GetSamples()))
	}

	// E11: RescanProductsForBannedContent with cursor pagination, dry_run, BLOCK->REJECTED+outbox,
	// new REVIEW->PENDING (while admin-approved revProd with existing flagged_keywords stays APPROVED per Item 3), and idempotent re-run!
	// Seed an APPROVED product before adding a brand-new REVIEW keyword so Rescan has a fresh REVIEW match to demote.
	freshRevProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  normalCat.GetCategory().GetId(),
		Name:        "Trà thảo dược tươi",
		Description: "Hiệu quả thần kỳ ngay lần đầu",
		Skus:        []*productv1.SKUInput{{SkuCode: "FR-1", PriceVnd: 80000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct freshRevProd: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: freshRevProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve freshRevProd: %v", err)
	}
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "thần kỳ ngay lần đầu", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword fresh REVIEW: %v", err)
	}

	// First dry_run across paginated batches (limit=5).
	var dryScanned, dryBlocked, dryFlagged int32
	cursor := ""
	batches := 0
	for {
		r, rErr := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{
			DryRun: true, Cursor: cursor, Limit: 5,
		})
		if rErr != nil {
			t.Fatalf("Rescan dry_run batch %d: %v", batches, rErr)
		}
		batches++
		dryScanned += r.GetScannedCount()
		dryBlocked += r.GetBlockedCount()
		dryFlagged += r.GetFlaggedCount()
		if !r.GetHasMore() {
			break
		}
		cursor = r.GetNextCursor()
	}
	if batches < 2 || dryBlocked != 1 || dryFlagged != 1 {
		t.Fatalf("dry_run rescan: batches=%d scanned=%d blocked=%d flagged=%d, want batches>=2 blocked=1 flagged=1",
			batches, dryScanned, dryBlocked, dryFlagged)
	}
	// Verify dry_run mutated nothing.
	stillApproved, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: freshRevProd.GetProduct().GetId()})
	if stillApproved.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("dry_run mutated product status to %v", stillApproved.GetProduct().GetStatus())
	}

	outboxBefore := len(mem.OutboxEvents())
	// Real rescan across batches.
	var realBlocked, realFlagged int32
	cursor = ""
	for {
		r, rErr := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{
			DryRun: false, Cursor: cursor, Limit: 5,
		})
		if rErr != nil {
			t.Fatalf("Rescan real: %v", rErr)
		}
		realBlocked += r.GetBlockedCount()
		realFlagged += r.GetFlaggedCount()
		if !r.GetHasMore() {
			break
		}
		cursor = r.GetNextCursor()
	}
	if realBlocked != 1 || realFlagged != 1 {
		t.Fatalf("real rescan: blocked=%d flagged=%d, want 1 and 1", realBlocked, realFlagged)
	}
	// lateProd -> REJECTED + 1 product.reviewed outbox event; freshRevProd -> PENDING; admin-approved revProd stays APPROVED!
	lateAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: lateProd.GetProduct().GetId()})
	if lateAfter.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("lateProd status = %v, want REJECTED", lateAfter.GetProduct().GetStatus())
	}
	freshAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: freshRevProd.GetProduct().GetId()})
	if freshAfter.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING {
		t.Fatalf("freshRevProd status = %v, want PENDING", freshAfter.GetProduct().GetStatus())
	}
	revAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: revProd.GetProduct().GetId()})
	if revAfter.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("admin-approved revProd must stay APPROVED, got %v", revAfter.GetProduct().GetStatus())
	}
	outboxAfterFirst := len(mem.OutboxEvents())
	reviewedFromRescan := 0
	for _, ev := range mem.OutboxEvents()[outboxBefore:] {
		if ev.EventType == events.SubjectProductReviewed {
			reviewedFromRescan++
		}
	}
	if reviewedFromRescan != 1 {
		t.Fatalf("expected exactly 1 product.reviewed outbox event from rescan, got %d", reviewedFromRescan)
	}

	// Re-run rescan -> 0 newly blocked, 0 newly flagged, 0 duplicate outbox events (E11)!
	rerun, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan rerun: %v", err)
	}
	if rerun.GetBlockedCount() != 0 || rerun.GetFlaggedCount() != 0 || len(mem.OutboxEvents()) != outboxAfterFirst {
		t.Fatalf("rescan rerun was not idempotent: blocked=%d flagged=%d outbox=%d (was %d)",
			rerun.GetBlockedCount(), rerun.GetFlaggedCount(), len(mem.OutboxEvents()), outboxAfterFirst)
	}

	// UpdateBannedKeyword partial update preserves is_active, then disables with has_is_active=true, then DeleteBannedKeyword.
	updKW, err := srv.UpdateBannedKeyword(adminCtx, &productv1.UpdateBannedKeywordRequest{
		Id: kwBlock.GetKeyword().GetId(), Category: "NARCOTICS",
	})
	if err != nil || !updKW.GetKeyword().GetIsActive() || updKW.GetKeyword().GetCategory() != "NARCOTICS" {
		t.Fatalf("UpdateBannedKeyword partial: %+v err=%v", updKW, err)
	}
	deactKW, err := srv.UpdateBannedKeyword(adminCtx, &productv1.UpdateBannedKeywordRequest{
		Id: kwBlock.GetKeyword().GetId(), IsActive: false, HasIsActive: true,
	})
	if err != nil || deactKW.GetKeyword().GetIsActive() {
		t.Fatalf("UpdateBannedKeyword deactivate: %+v err=%v", deactKW, err)
	}
	if _, err := srv.DeleteBannedKeyword(adminCtx, &productv1.DeleteBannedKeywordRequest{Id: kwReview.GetKeyword().GetId()}); err != nil {
		t.Fatalf("DeleteBannedKeyword: %v", err)
	}
	if _, err := srv.DeleteBannedKeyword(adminCtx, &productv1.DeleteBannedKeywordRequest{Id: kwReview.GetKeyword().GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("DeleteBannedKeyword missing: want NotFound, got %v", err)
	}
}

func TestW3B_DiscontinuedStub_E13(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	pubCtx := context.Background()

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Giày", Slug: "giay", CommissionRateBps: 500})
	catID := cat.GetCategory().GetId()

	// 1. Unknown slug -> 404 even with include_discontinued_stub=true.
	if _, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "khong-ton-tai", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown slug: want NotFound, got %v", err)
	}

	// 2. Never-approved PENDING, never-approved REJECTED, never-approved soft-deleted -> ALL 404 (no leak!).
	pendingP, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Draft Pending", Slug: "draft-pending",
		Skus: []*productv1.SKUInput{{SkuCode: "DP-1", PriceVnd: 100000, Stock: 5}},
	})
	if _, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "draft-pending", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("never-approved PENDING: want NotFound, got %v", err)
	}
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pendingP.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT, Reason: "bad",
	})
	if _, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "draft-pending", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("never-approved REJECTED: want NotFound, got %v", err)
	}
	_, _ = srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: pendingP.GetProduct().GetId()})
	if _, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "draft-pending", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("never-approved deleted: want NotFound, got %v", err)
	}

	// 3. Previously APPROVED -> HIDDEN: returns discontinued=true, category_id, name=product.Name, product=nil.
	hidP, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Giày Chạy Bộ", Slug: "giay-chay-bo",
		Skus: []*productv1.SKUInput{{SkuCode: "GCB-1", PriceVnd: 500000, Stock: 10}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: hidP.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: hidP.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	// Without include_discontinued_stub -> 404.
	if _, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "giay-chay-bo"}); status.Code(err) != codes.NotFound {
		t.Fatalf("HIDDEN without stub flag: want NotFound, got %v", err)
	}
	// With include_discontinued_stub -> 200 discontinued stub WITH name, NO Product/SKUs.
	stubHid, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "giay-chay-bo", IncludeDiscontinuedStub: true})
	if err != nil {
		t.Fatalf("HIDDEN with stub flag: %v", err)
	}
	if !stubHid.GetDiscontinued() || stubHid.GetProduct() != nil || stubHid.GetDiscontinuedCategoryId() != catID || stubHid.GetDiscontinuedName() != "Giày Chạy Bộ" {
		t.Fatalf("HIDDEN stub = %+v, want discontinued=true cat=%s name=Giày Chạy Bộ product=nil", stubHid, catID)
	}

	// 4. Previously APPROVED -> REJECTED (admin takedown): discontinued=true, category_id, EMPTY name!
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: hidP.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT, Reason: "policy",
	})
	stubRej, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "giay-chay-bo", IncludeDiscontinuedStub: true})
	if err != nil {
		t.Fatalf("REJECTED with stub flag: %v", err)
	}
	if !stubRej.GetDiscontinued() || stubRej.GetProduct() != nil || stubRej.GetDiscontinuedCategoryId() != catID || stubRej.GetDiscontinuedName() != "" {
		t.Fatalf("REJECTED stub = %+v, want discontinued=true cat=%s name='' product=nil", stubRej, catID)
	}

	// 5. Previously APPROVED -> soft-deleted: discontinued=true, category_id, EMPTY name!
	_, _ = srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: hidP.GetProduct().GetId()})
	stubDel, err := srv.GetProduct(pubCtx, &productv1.GetProductRequest{Slug: "giay-chay-bo", IncludeDiscontinuedStub: true})
	if err != nil {
		t.Fatalf("soft-deleted with stub flag: %v", err)
	}
	if !stubDel.GetDiscontinued() || stubDel.GetProduct() != nil || stubDel.GetDiscontinuedCategoryId() != catID || stubDel.GetDiscontinuedName() != "" {
		t.Fatalf("soft-deleted stub = %+v, want discontinued=true cat=%s name='' product=nil", stubDel, catID)
	}
}

func TestW3B_GRPCTrust_NewRPCs(t *testing.T) {
	users, _ := auth.NewManager("w3b-user-key", 15*time.Minute)
	svc, _ := auth.NewManager("w3b-service-key", auth.ServiceTokenTTL)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svc)))
	productSrv := New(store.NewMemoryStore(), nil)
	productv1.RegisterProductServiceServer(gs, productSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := productv1.NewProductServiceClient(conn)

	withTok := func(tok string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tok))
	}

	svcTok, _ := svc.SignServiceToken("order-service")
	buyerTok, _ := func() (string, error) { s, _, _, e := users.SignAccessToken("b1", "buyer", ""); return s, e }()
	adminTok, _ := func() (string, error) { s, _, _, e := users.SignAccessToken("a1", "admin", ""); return s, e }()

	// Anonymous blocked on private new RPCs.
	if _, err := client.CreateReview(context.Background(), &productv1.CreateReviewRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon CreateReview: want Unauthenticated, got %v", err)
	}
	if _, err := client.ListOrderReviews(context.Background(), &productv1.ListOrderReviewsRequest{OrderId: "o1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListOrderReviews: want Unauthenticated, got %v", err)
	}
	if _, err := client.CreateBannedKeyword(context.Background(), &productv1.CreateBannedKeywordRequest{Term: "abc", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon CreateBannedKeyword: want Unauthenticated, got %v", err)
	}

	// Service tokens rejected on all new RPCs (internalRPCs=false and publicRPCs reject service tokens).
	if _, err := client.CreateReview(withTok(svcTok), &productv1.CreateReviewRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token CreateReview: want PermissionDenied, got %v", err)
	}
	if _, err := client.ListProductReviews(withTok(svcTok), &productv1.ListProductReviewsRequest{ProductId: "p1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ListProductReviews: want PermissionDenied, got %v", err)
	}
	if _, err := client.RescanProductsForBannedContent(withTok(svcTok), &productv1.RescanProductsForBannedContentRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token Rescan: want PermissionDenied, got %v", err)
	}

	// Buyer rejected on admin moderation RPCs; admin allowed.
	if _, err := client.CreateBannedKeyword(withTok(buyerTok), &productv1.CreateBannedKeywordRequest{Term: "abc", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer CreateBannedKeyword: want PermissionDenied, got %v", err)
	}
	if _, err := client.CreateBannedKeyword(withTok(adminTok), &productv1.CreateBannedKeywordRequest{Term: "abc", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK}); err != nil {
		t.Fatalf("admin CreateBannedKeyword: %v", err)
	}

	// Anonymous allowed on public ListProductReviews (returns NotFound for missing product, NOT Unauthenticated).
	if _, err := client.ListProductReviews(context.Background(), &productv1.ListProductReviewsRequest{ProductId: "missing-prod"}); status.Code(err) != codes.NotFound {
		t.Fatalf("anon ListProductReviews: want NotFound, got %v", err)
	}
}
