package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type cartItemDTO struct {
	SKUID             string `json:"sku_id"`
	ProductID         string `json:"product_id"`
	ShopID            string `json:"shop_id"`
	ProductName       string `json:"product_name"`
	SKUName           string `json:"sku_name"`
	ImageURL          string `json:"image_url"`
	PriceVND          int64  `json:"price_vnd"`
	AddedPriceVND     int64  `json:"added_price_vnd"`
	PriceChanged      bool   `json:"price_changed"`
	Quantity          int32  `json:"quantity"`
	AvailableStock    int32  `json:"available_stock"`
	WeightGrams       int32  `json:"weight_grams"`
	Selected          bool   `json:"selected"`
	Unavailable       bool   `json:"unavailable"`
	UnavailableReason string `json:"unavailable_reason"`
}

type cartShopGroupDTO struct {
	ShopID           string         `json:"shop_id"`
	ShopName         string         `json:"shop_name"`
	Items            []*cartItemDTO `json:"items"`
	SubtotalVND      int64          `json:"subtotal_vnd"`
	TotalWeightGrams int32          `json:"total_weight_grams"`
}

type cartDTO struct {
	UserID     string              `json:"user_id"`
	ShopGroups []*cartShopGroupDTO `json:"shop_groups"`
	TotalItems int32               `json:"total_items"`
	TotalVND   int64               `json:"total_vnd"`
}

func mapCart(c *cartv1.Cart) *cartDTO {
	if c == nil {
		return &cartDTO{ShopGroups: []*cartShopGroupDTO{}}
	}
	groups := make([]*cartShopGroupDTO, 0, len(c.GetShopGroups()))
	for _, g := range c.GetShopGroups() {
		items := make([]*cartItemDTO, 0, len(g.GetItems()))
		for _, it := range g.GetItems() {
			items = append(items, &cartItemDTO{
				SKUID:             it.GetSkuId(),
				ProductID:         it.GetProductId(),
				ShopID:            it.GetShopId(),
				ProductName:       it.GetProductName(),
				SKUName:           it.GetSkuName(),
				ImageURL:          it.GetImageUrl(),
				PriceVND:          it.GetPriceVnd(),
				AddedPriceVND:     it.GetAddedPriceVnd(),
				PriceChanged:      it.GetPriceChanged(),
				Quantity:          it.GetQuantity(),
				AvailableStock:    it.GetAvailableStock(),
				WeightGrams:       it.GetWeightGrams(),
				Selected:          it.GetSelected(),
				Unavailable:       it.GetUnavailable(),
				UnavailableReason: it.GetUnavailableReason(),
			})
		}
		groups = append(groups, &cartShopGroupDTO{
			ShopID:           g.GetShopId(),
			ShopName:         g.GetShopName(),
			Items:            items,
			SubtotalVND:      g.GetSubtotalVnd(),
			TotalWeightGrams: g.GetTotalWeightGrams(),
		})
	}
	return &cartDTO{
		UserID:     c.GetUserId(),
		ShopGroups: groups,
		TotalItems: c.GetTotalItems(),
		TotalVND:   c.GetTotalVnd(),
	}
}

func (h *handler) handleGetCart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Cart.GetCart(ctx, &cartv1.GetCartRequest{
		UserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cart": mapCart(resp.GetCart())})
}

func (h *handler) handleAddCartItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SKUID    string `json:"sku_id"`
		Quantity int32  `json:"quantity"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Cart.AddItem(ctx, &cartv1.AddItemRequest{
		UserId:   auth.UserIDFromContext(r.Context()),
		SkuId:    body.SKUID,
		Quantity: body.Quantity,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"cart":             mapCart(resp.GetCart()),
		"clamped_to_stock": resp.GetClampedToStock(),
	})
}

func (h *handler) handleUpdateCartItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Quantity int32 `json:"quantity"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Cart.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{
		UserId:   auth.UserIDFromContext(r.Context()),
		SkuId:    chi.URLParam(r, "sku_id"),
		Quantity: body.Quantity,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"cart":             mapCart(resp.GetCart()),
		"clamped_to_stock": resp.GetClampedToStock(),
	})
}

func (h *handler) handleRemoveCartItem(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Cart.RemoveItem(ctx, &cartv1.RemoveItemRequest{
		UserId: auth.UserIDFromContext(r.Context()),
		SkuId:  chi.URLParam(r, "sku_id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cart": mapCart(resp.GetCart())})
}

func (h *handler) handleClearCart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SKUIDs []string `json:"sku_ids"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Cart.ClearItems(ctx, &cartv1.ClearItemsRequest{
		UserId: auth.UserIDFromContext(r.Context()),
		SkuIds: body.SKUIDs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}
