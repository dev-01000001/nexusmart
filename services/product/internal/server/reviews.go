package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/moderation"
	"github.com/nexusmart/core/services/product/internal/resilience"
	"github.com/nexusmart/core/services/product/internal/store"
)

const (
	// DefaultAnonymousBuyerName is the fallback reviewer display name when UserService.GetProfile fails or full_name is blank (E5).
	DefaultAnonymousBuyerName = "Người mua đã xác thực"

	maxReviewCommentRunes = 2000
	maxReviewImageURLs    = 5
	maxReviewImageURLLen  = 500

	defaultUpstreamTimeout = 3 * time.Second
	orderClientAttempts    = 2
	userClientAttempts     = 1
	upstreamBaseBackoff    = 50 * time.Millisecond
)

// OrderClient is the minimal OrderService gRPC client needed for buyer review verification (E6).
type OrderClient interface {
	GetOrder(ctx context.Context, in *orderv1.GetOrderRequest, opts ...grpc.CallOption) (*orderv1.GetOrderResponse, error)
}

// UserClient is the minimal UserService gRPC client needed for masked reviewer display names (E5).
type UserClient interface {
	GetProfile(ctx context.Context, in *userv1.GetProfileRequest, opts ...grpc.CallOption) (*userv1.GetProfileResponse, error)
}

// SetOrderClient attaches the lazy-dialed OrderService client used by CreateReview and ListOrderReviews.
func (s *Server) SetOrderClient(c OrderClient) {
	s.orderClient = c
}

// SetUserClient attaches the lazy-dialed UserService client used to resolve masked reviewer display names.
func (s *Server) SetUserClient(c UserClient) {
	s.userClient = c
}

// SetOrderBreaker overrides the OrderService circuit breaker (useful in tests).
func (s *Server) SetOrderBreaker(cb *resilience.CircuitBreaker) {
	if cb != nil {
		s.orderBreaker = cb
	}
}

// SetUserBreaker overrides the UserService circuit breaker (useful in tests).
func (s *Server) SetUserBreaker(cb *resilience.CircuitBreaker) {
	if cb != nil {
		s.userBreaker = cb
	}
}

// SetUpstreamTimeout overrides the per-attempt timeout for OrderService and UserService calls (default 3s).
func (s *Server) SetUpstreamTimeout(d time.Duration) {
	if d > 0 {
		s.upstreamTimeout = d
	}
}

func (s *Server) upstreamCallTimeout() time.Duration {
	if s.upstreamTimeout > 0 {
		return s.upstreamTimeout
	}
	return defaultUpstreamTimeout
}

// MaskBuyerName masks a full name per E5:
//   - Multiple words: first uppercase rune of the first word + ". " + last word (e.g. "Nguyễn Văn Linh" -> "N. Linh").
//   - Single word (or email-like last word): first uppercase rune + "***" (e.g. "Linh" -> "L***", "a@b.com" -> "A***").
//   - Blank input: DefaultAnonymousBuyerName ("Người mua đã xác thực").
func MaskBuyerName(fullName string) string {
	trimmed := strings.TrimSpace(fullName)
	if trimmed == "" {
		return DefaultAnonymousBuyerName
	}
	words := strings.Fields(trimmed)
	if len(words) == 0 {
		return DefaultAnonymousBuyerName
	}
	firstRunes := []rune(words[0])
	if len(firstRunes) == 0 {
		return DefaultAnonymousBuyerName
	}
	firstInitial := string(unicode.ToUpper(firstRunes[0]))
	if len(words) == 1 {
		return firstInitial + "***"
	}
	last := words[len(words)-1]
	if strings.Contains(last, "@") {
		return firstInitial + "***"
	}
	return firstInitial + ". " + last
}

// validateReviewImageURLs validates review image URLs per E7:
// at most 5 URLs, each <= 500 characters, strictly https:// (valid host, no credentials)
// or internal /products/... relative path (no traversal or whitespace).
func validateReviewImageURLs(raw []string) ([]string, error) {
	if len(raw) > maxReviewImageURLs {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d image URLs are allowed", maxReviewImageURLs)
	}
	if len(raw) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		u := strings.TrimSpace(item)
		if u == "" {
			return nil, status.Errorf(codes.InvalidArgument, "image_urls[%d] must not be empty", i)
		}
		if len(u) > maxReviewImageURLLen || utf8.RuneCountInString(u) > maxReviewImageURLLen {
			return nil, status.Errorf(codes.InvalidArgument, "image_urls[%d] exceeds maximum length of %d characters", i, maxReviewImageURLLen)
		}
		if hasControlOrWhitespace(u) {
			return nil, status.Errorf(codes.InvalidArgument, "image_urls[%d] contains invalid whitespace or control characters", i)
		}
		if strings.HasPrefix(u, "/products/") {
			if len(u) <= len("/products/") || strings.Contains(u, "..") || strings.Contains(u, "\\") || strings.HasPrefix(u, "//") {
				return nil, status.Errorf(codes.InvalidArgument, "image_urls[%d] is not a valid internal /products/ path", i)
			}
			out = append(out, u)
			continue
		}
		parsed, err := url.Parse(u)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil {
			return nil, status.Errorf(codes.InvalidArgument, "image_urls[%d] must be an https:// URL or /products/... path", i)
		}
		out = append(out, u)
	}
	return out, nil
}

func hasControlOrWhitespace(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func requireEndUser(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if strings.TrimSpace(claims.Service) != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for this RPC")
	}
	if strings.TrimSpace(claims.UserID()) == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return claims, nil
}

func (s *Server) fetchOrderForBuyer(ctx context.Context, orderID string) (*orderv1.Order, error) {
	if s.orderClient == nil {
		return nil, status.Error(codes.Unavailable, "order service unavailable")
	}
	rawToken := auth.RawTokenFromContext(ctx)
	resp, err := resilience.CallWithRetry(
		ctx,
		s.orderBreaker,
		orderClientAttempts,
		upstreamBaseBackoff,
		s.upstreamCallTimeout(),
		func(attemptCtx context.Context) (*orderv1.GetOrderResponse, error) {
			outCtx := auth.InjectOutgoingGRPC(attemptCtx, rawToken)
			return s.orderClient.GetOrder(outCtx, &orderv1.GetOrderRequest{OrderId: orderID})
		},
	)
	if err != nil {
		if errors.Is(err, resilience.ErrCircuitOpen) || resilience.IsTransientGRPCError(err) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.Unavailable, "order service unavailable")
		}
		switch status.Code(err) {
		case codes.NotFound:
			return nil, status.Error(codes.NotFound, "order not found")
		case codes.PermissionDenied:
			return nil, status.Error(codes.PermissionDenied, "not authorized to access this order")
		case codes.Unauthenticated:
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		case codes.InvalidArgument:
			return nil, status.Error(codes.InvalidArgument, "invalid order_id")
		default:
			return nil, status.Errorf(codes.Unavailable, "order service unavailable: %v", err)
		}
	}
	if resp == nil || resp.GetOrder() == nil {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return resp.GetOrder(), nil
}

func (s *Server) fetchMaskedBuyerName(ctx context.Context, userID string) string {
	if s.userClient == nil {
		return DefaultAnonymousBuyerName
	}
	rawToken := auth.RawTokenFromContext(ctx)
	resp, err := resilience.CallWithRetry(
		ctx,
		s.userBreaker,
		userClientAttempts,
		upstreamBaseBackoff,
		s.upstreamCallTimeout(),
		func(attemptCtx context.Context) (*userv1.GetProfileResponse, error) {
			outCtx := auth.InjectOutgoingGRPC(attemptCtx, rawToken)
			return s.userClient.GetProfile(outCtx, &userv1.GetProfileRequest{UserId: userID})
		},
	)
	if err != nil || resp == nil || resp.GetUser() == nil {
		if err != nil {
			s.logger.WarnContext(ctx, "fallback to default buyer display name after user profile lookup failed",
				slog.String("user_id", userID),
				slog.String("error", err.Error()),
			)
		}
		return DefaultAnonymousBuyerName
	}
	return MaskBuyerName(resp.GetUser().GetFullName())
}

// CreateReview creates a verified buyer review for a completed order item (E5–E8).
func (s *Server) CreateReview(ctx context.Context, req *productv1.CreateReviewRequest) (*productv1.CreateReviewResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}

	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	orderItemID := strings.TrimSpace(req.GetOrderItemId())
	if orderItemID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_item_id is required")
	}
	rating := req.GetRating()
	if rating < 1 || rating > 5 {
		return nil, status.Error(codes.InvalidArgument, "rating must be between 1 and 5")
	}
	comment := strings.TrimSpace(req.GetComment())
	if utf8.RuneCountInString(comment) > maxReviewCommentRunes {
		return nil, status.Errorf(codes.InvalidArgument, "comment exceeds maximum length of %d characters", maxReviewCommentRunes)
	}
	imageURLs, err := validateReviewImageURLs(req.GetImageUrls())
	if err != nil {
		return nil, err
	}

	// Verify ownership, COMPLETED status, and line-item authority from OrderService (E6).
	ord, err := s.fetchOrderForBuyer(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ord.GetBuyerId()) == "" || ord.GetBuyerId() != claims.UserID() {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who placed this order can review it")
	}
	if ord.GetStatus() != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
		return nil, status.Error(codes.FailedPrecondition, "only COMPLETED orders can be reviewed")
	}

	var matchedItem *orderv1.OrderItem
	for _, it := range ord.GetItems() {
		if it.GetId() == orderItemID {
			matchedItem = it
			break
		}
	}
	if matchedItem == nil {
		return nil, status.Error(codes.NotFound, "order item not found in order")
	}

	productID := strings.TrimSpace(matchedItem.GetProductId())
	shopID := strings.TrimSpace(ord.GetShopId())
	skuID := strings.TrimSpace(matchedItem.GetSkuId())
	skuName := strings.TrimSpace(matchedItem.GetSkuName())
	if productID == "" {
		return nil, status.Error(codes.FailedPrecondition, "order item is missing product_id")
	}

	// E8: Any active banned keyword (both BLOCK and REVIEW) rejects the review with 400 BANNED_CONTENT.
	// Per E10-bis, do not expose matched moderation keyword rules to buyers.
	if comment != "" {
		activeRules, ruleErr := s.loadCompiledModerationRules(ctx, s.store)
		if ruleErr != nil {
			return nil, status.Errorf(codes.Internal, "load moderation rules: %v", ruleErr)
		}
		if res := moderation.MatchCompiled(activeRules, comment); res.Blocked || res.Flagged {
			return nil, status.Error(codes.InvalidArgument, "BANNED_CONTENT: review contains prohibited content")
		}
	}

	// E5: Best-effort masked display name from UserService.GetProfile with buyer JWT.
	buyerName := s.fetchMaskedBuyerName(ctx, claims.UserID())

	now := s.now()
	ctx = store.WithAsOf(ctx, now)
	traceID, traceparent := traceMetaFromContext(ctx)

	var created store.ProductReview
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		// E6: Lock products row first so concurrent reviews serialize rating_sum/review_count updates cleanly.
		lockedProd, lockErr := q.GetProductByIDForUpdateAny(ctx, productID)
		if lockErr != nil {
			return lockErr
		}

		reviewID := uuid.NewString()
		rev, insErr := q.CreateProductReview(ctx, store.ProductReview{
			ID:          reviewID,
			ProductID:   productID,
			ShopID:      shopID,
			OrderID:     orderID,
			OrderItemID: orderItemID,
			SKUID:       skuID,
			SKUName:     skuName,
			BuyerID:     claims.UserID(),
			BuyerName:   buyerName,
			Rating:      rating,
			Comment:     comment,
			ImageURLs:   imageURLs,
			Status:      store.ReviewStatusPublished,
			CreatedAt:   now,
		})
		if insErr != nil {
			return insErr
		}

		newSum, newCount, incErr := q.IncrementProductRating(ctx, productID, int64(rating))
		if incErr != nil {
			return incErr
		}
		avgX100 := store.ComputeRatingAvgX100(newSum, newCount)

		payloadBytes, mErr := json.Marshal(events.ReviewCreatedPayload{
			ReviewID:      rev.ID,
			ProductID:     rev.ProductID,
			ShopID:        rev.ShopID,
			OrderItemID:   rev.OrderItemID,
			Rating:        rev.Rating,
			Comment:       rev.Comment,
			ReviewCount:   newCount,
			RatingAvgX100: avgX100,
			OccurredAt:    now.Format(time.RFC3339),
		})
		if mErr != nil {
			return fmt.Errorf("marshal review.created payload: %w", mErr)
		}
		if obErr := q.InsertOutboxEvent(ctx, store.OutboxEvent{
			ID:          uuid.NewString(),
			EventType:   events.SubjectReviewCreated,
			AggregateID: productID,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: payloadBytes,
			CreatedAt:   now,
		}); obErr != nil {
			return obErr
		}

		// G9: Keep search index rating_avg_x100 & review_count in sync within the same TX without mutating products.updated_at.
		if lockedProd.DeletedAt == nil && isPublicProductStatusStr(lockedProd.Status) {
			lockedProd.RatingSum = newSum
			lockedProd.ReviewCount = newCount
			eventUpdatedAt := now
			if !eventUpdatedAt.After(lockedProd.UpdatedAt) {
				eventUpdatedAt = lockedProd.UpdatedAt.UTC().Add(time.Microsecond)
			}
			skus, skuErr := q.ListSKUsByProductIDs(ctx, []string{productID})
			if skuErr != nil {
				return skuErr
			}
			if obErr := emitProductUpsertedOutbox(ctx, q, lockedProd, skus, eventUpdatedAt, now); obErr != nil {
				return obErr
			}
		}

		created = rev
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "this order item has already been reviewed")
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.NotFound, "product not found")
		default:
			return nil, status.Errorf(codes.Internal, "create review: %v", err)
		}
	}

	s.triggerOutbox()
	return &productv1.CreateReviewResponse{
		Review: toProtoReview(created, true),
	}, nil
}

// ListProductReviews returns published reviews and aggregate rating breakdown for a product (Public — E6/E6-bis/E14).
func (s *Server) ListProductReviews(ctx context.Context, req *productv1.ListProductReviewsRequest) (*productv1.ListProductReviewsResponse, error) {
	productID := strings.TrimSpace(req.GetProductId())
	if productID == "" {
		return nil, status.Error(codes.InvalidArgument, "product_id is required")
	}
	ratingFilter := req.GetRatingFilter()
	if ratingFilter < 0 || ratingFilter > 5 {
		return nil, status.Error(codes.InvalidArgument, "rating_filter must be between 0 and 5")
	}
	page := req.GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	ctx = store.WithAsOf(ctx, s.now())
	prod, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "get product: %v", err)
	}
	if !canViewProduct(ctx, prod) {
		return nil, status.Error(codes.NotFound, "product not found")
	}

	reviews, total, err := s.store.ListProductReviews(ctx, store.ListProductReviewsFilter{
		ProductID:    productID,
		RatingFilter: ratingFilter,
		HasImages:    req.GetHasImages(),
		Limit:        pageSize,
		Offset:       (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list product reviews: %v", err)
	}
	ratingCounts, err := s.store.GetProductReviewRatingCounts(ctx, productID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get product review rating counts: %v", err)
	}

	out := make([]*productv1.ProductReview, 0, len(reviews))
	for _, r := range reviews {
		// E6-bis: public ListProductReviews MUST omit buyer_id, order_id, order_item_id, sku_id.
		out = append(out, toProtoReview(r, false))
	}

	return &productv1.ListProductReviewsResponse{
		Reviews:       out,
		Total:         total,
		RatingAvgX100: prod.RatingAvgX100(),
		ReviewCount:   prod.ReviewCount,
		RatingCounts:  ratingCounts,
	}, nil
}

// ListOrderReviews returns reviews submitted by the authenticated buyer for a given order (E6/E6-bis).
func (s *Server) ListOrderReviews(ctx context.Context, req *productv1.ListOrderReviewsRequest) (*productv1.ListOrderReviewsResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	ord, err := s.fetchOrderForBuyer(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ord.GetBuyerId()) == "" || ord.GetBuyerId() != claims.UserID() {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who placed this order can view its reviews")
	}

	reviews, err := s.store.ListReviewsByOrderAndBuyer(ctx, orderID, claims.UserID())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list order reviews: %v", err)
	}
	out := make([]*productv1.ProductReview, 0, len(reviews))
	for _, r := range reviews {
		// E6-bis: ListOrderReviews for order owner returns full fields.
		out = append(out, toProtoReview(r, true))
	}
	return &productv1.ListOrderReviewsResponse{Reviews: out}, nil
}

func toProtoReview(r store.ProductReview, includePrivate bool) *productv1.ProductReview {
	imgs := make([]string, len(r.ImageURLs))
	copy(imgs, r.ImageURLs)
	msg := &productv1.ProductReview{
		Id:        r.ID,
		ProductId: r.ProductID,
		ShopId:    r.ShopID,
		SkuName:   r.SKUName,
		BuyerName: r.BuyerName,
		Rating:    r.Rating,
		Comment:   r.Comment,
		ImageUrls: imgs,
		Status:    toProtoReviewStatus(r.Status),
		CreatedAt: timestamppb.New(r.CreatedAt),
		UpdatedAt: timestamppb.New(r.UpdatedAt),
	}
	if includePrivate {
		msg.OrderId = r.OrderID
		msg.OrderItemId = r.OrderItemID
		msg.SkuId = r.SKUID
		msg.BuyerId = r.BuyerID
	}
	return msg
}

func toProtoReviewStatus(st string) productv1.ReviewStatus {
	switch st {
	case store.ReviewStatusPublished:
		return productv1.ReviewStatus_REVIEW_STATUS_PUBLISHED
	case store.ReviewStatusHidden:
		return productv1.ReviewStatus_REVIEW_STATUS_HIDDEN
	default:
		return productv1.ReviewStatus_REVIEW_STATUS_UNSPECIFIED
	}
}
