package router

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

const (
	maxReviewCommentRunes = 2000
	maxReviewImageURLs    = 5
	maxReviewImageURLLen  = 500
)

// publicProductReviewDTO omits internal identifiers (buyer_id, order_id, order_item_id, sku_id)
// per E6-bis so public product review responses never leak private order/buyer linkage.
type publicProductReviewDTO struct {
	ID        string   `json:"id"`
	ProductID string   `json:"product_id"`
	SKUName   string   `json:"sku_name"`
	BuyerName string   `json:"buyer_name"`
	Rating    int32    `json:"rating"`
	Comment   string   `json:"comment"`
	ImageURLs []string `json:"image_urls"`
	CreatedAt string   `json:"created_at,omitempty"`
}

// orderReviewDTO carries full review fields for authenticated order-owner routes (E6-bis).
type orderReviewDTO struct {
	ID          string   `json:"id"`
	ProductID   string   `json:"product_id"`
	ShopID      string   `json:"shop_id"`
	OrderID     string   `json:"order_id"`
	OrderItemID string   `json:"order_item_id"`
	SKUID       string   `json:"sku_id"`
	SKUName     string   `json:"sku_name"`
	BuyerID     string   `json:"buyer_id"`
	BuyerName   string   `json:"buyer_name"`
	Rating      int32    `json:"rating"`
	Comment     string   `json:"comment"`
	ImageURLs   []string `json:"image_urls"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

func reviewStatusString(st productv1.ReviewStatus) string {
	switch st {
	case productv1.ReviewStatus_REVIEW_STATUS_PUBLISHED:
		return "PUBLISHED"
	case productv1.ReviewStatus_REVIEW_STATUS_HIDDEN:
		return "HIDDEN"
	default:
		return ""
	}
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func mapPublicProductReview(r *productv1.ProductReview) *publicProductReviewDTO {
	if r == nil {
		return nil
	}
	return &publicProductReviewDTO{
		ID:        r.GetId(),
		ProductID: r.GetProductId(),
		SKUName:   r.GetSkuName(),
		BuyerName: r.GetBuyerName(),
		Rating:    r.GetRating(),
		Comment:   r.GetComment(),
		ImageURLs: nonNilStrings(r.GetImageUrls()),
		CreatedAt: httpx.FormatTimestamp(r.GetCreatedAt()),
	}
}

func mapOrderReview(r *productv1.ProductReview) *orderReviewDTO {
	if r == nil {
		return nil
	}
	return &orderReviewDTO{
		ID:          r.GetId(),
		ProductID:   r.GetProductId(),
		ShopID:      r.GetShopId(),
		OrderID:     r.GetOrderId(),
		OrderItemID: r.GetOrderItemId(),
		SKUID:       r.GetSkuId(),
		SKUName:     r.GetSkuName(),
		BuyerID:     r.GetBuyerId(),
		BuyerName:   r.GetBuyerName(),
		Rating:      r.GetRating(),
		Comment:     r.GetComment(),
		ImageURLs:   nonNilStrings(r.GetImageUrls()),
		Status:      reviewStatusString(r.GetStatus()),
		CreatedAt:   httpx.FormatTimestamp(r.GetCreatedAt()),
		UpdatedAt:   httpx.FormatTimestamp(r.GetUpdatedAt()),
	}
}

// validateReviewImageURL validates E7 constraints: <= 500 chars, no control/whitespace chars,
// strictly https:// with non-empty host or internal /products/... path.
func validateReviewImageURL(raw string) (string, bool) {
	u := strings.TrimSpace(raw)
	if u == "" || len(u) > maxReviewImageURLLen {
		return "", false
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return "", false
		}
	}
	if strings.HasPrefix(u, "/products/") {
		if strings.Contains(u, "..") || strings.Contains(u, `\`) {
			return "", false
		}
		return u, true
	}
	parsed, err := url.Parse(u)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return "", false
	}
	return u, true
}

func (h *handler) handleListProductReviews(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	var ratingFilter int32
	if rawRating := strings.TrimSpace(q.Get("rating")); rawRating != "" {
		n, err := strconv.ParseInt(rawRating, 10, 32)
		if err != nil || n < 1 || n > 5 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "rating must be an integer between 1 and 5")
			return
		}
		ratingFilter = int32(n)
	}

	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListProductReviews(ctx, &productv1.ListProductReviewsRequest{
		ProductId:    chi.URLParam(r, "id"),
		RatingFilter: ratingFilter,
		HasImages:    httpx.ParseBoolQuery(r, "has_images"),
		Page:         page,
		PageSize:     pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*publicProductReviewDTO, 0, len(resp.GetReviews()))
	for _, rev := range resp.GetReviews() {
		items = append(items, mapPublicProductReview(rev))
	}
	ratingCounts := map[string]int32{
		"1": 0,
		"2": 0,
		"3": 0,
		"4": 0,
		"5": 0,
	}
	for star, cnt := range resp.GetRatingCounts() {
		if star >= 1 && star <= 5 {
			ratingCounts[strconv.Itoa(int(star))] = cnt
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":           items,
		"rating_avg_x100": resp.GetRatingAvgX100(),
		"review_count":    resp.GetReviewCount(),
		"rating_counts":   ratingCounts,
		"page":            page,
		"page_size":       pageSize,
		"total":           resp.GetTotal(),
	})
}

func (h *handler) handleCreateOrderReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderItemID string   `json:"order_item_id"`
		Rating      int32    `json:"rating"`
		Comment     string   `json:"comment"`
		ImageURLs   []string `json:"image_urls"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	orderItemID := strings.TrimSpace(body.OrderItemID)
	if orderItemID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "order_item_id is required")
		return
	}
	if body.Rating < 1 || body.Rating > 5 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "rating must be between 1 and 5")
		return
	}
	if utf8.RuneCountInString(body.Comment) > maxReviewCommentRunes {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "comment exceeds maximum length of 2000 characters")
		return
	}
	if len(body.ImageURLs) > maxReviewImageURLs {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "image_urls exceeds maximum limit of 5")
		return
	}
	cleanURLs := make([]string, 0, len(body.ImageURLs))
	for _, raw := range body.ImageURLs {
		u, ok := validateReviewImageURL(raw)
		if !ok {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "image_urls must contain valid https:// or /products/... URLs up to 500 characters")
			return
		}
		cleanURLs = append(cleanURLs, u)
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.CreateReview(ctx, &productv1.CreateReviewRequest{
		OrderId:     chi.URLParam(r, "id"),
		OrderItemId: orderItemID,
		Rating:      body.Rating,
		Comment:     body.Comment,
		ImageUrls:   cleanURLs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"review": mapOrderReview(resp.GetReview()),
	})
}

func (h *handler) handleListOrderReviews(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListOrderReviews(ctx, &productv1.ListOrderReviewsRequest{
		OrderId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	reviews := make([]*orderReviewDTO, 0, len(resp.GetReviews()))
	for _, rev := range resp.GetReviews() {
		reviews = append(reviews, mapOrderReview(rev))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"reviews": reviews,
	})
}
