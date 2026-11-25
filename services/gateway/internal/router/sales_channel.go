package router

import (
	"net/http"
	"strings"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

const (
	salesChannelMarketplace = "MARKETPLACE"
	salesChannelStorefront  = "STOREFRONT"
)

// validateCheckoutSalesChannelInput validates sales_channel & storefront_shop_id for POST /checkout.
// Per DECISIONS.md (20:25): omitting sales_channel defaults to STOREFRONT (requiring a non-empty
// storefront_shop_id); explicit MARKETPLACE is accepted only when storefront_shop_id is empty.
func validateCheckoutSalesChannelInput(rawChannel, rawShopID string) (channel, storefrontShopID, errMsg string) {
	ch := strings.ToUpper(strings.TrimSpace(rawChannel))
	sid := strings.TrimSpace(rawShopID)
	if ch == "" {
		ch = salesChannelStorefront
	}
	switch ch {
	case salesChannelStorefront:
		if sid == "" {
			return "", "", "storefront_shop_id is required when sales_channel is STOREFRONT"
		}
		return salesChannelStorefront, sid, ""
	case salesChannelMarketplace:
		if sid != "" {
			return "", "", "storefront_shop_id must be empty when sales_channel is MARKETPLACE"
		}
		return salesChannelMarketplace, "", ""
	default:
		return "", "", "sales_channel must be MARKETPLACE or STOREFRONT"
	}
}

// validateSalesChannelInput validates sales_channel & storefront_shop_id for read/preview requests.
// Empty sales_channel defaults to MARKETPLACE; STOREFRONT requires a non-empty storefront_shop_id;
// MARKETPLACE requires an empty storefront_shop_id.
func validateSalesChannelInput(rawChannel, rawShopID string) (channel, storefrontShopID, errMsg string) {
	ch := strings.ToUpper(strings.TrimSpace(rawChannel))
	sid := strings.TrimSpace(rawShopID)
	if ch == "" {
		ch = salesChannelMarketplace
	}
	switch ch {
	case salesChannelMarketplace:
		if sid != "" {
			return "", "", "storefront_shop_id must be empty when sales_channel is MARKETPLACE"
		}
		return salesChannelMarketplace, "", ""
	case salesChannelStorefront:
		if sid == "" {
			return "", "", "storefront_shop_id is required when sales_channel is STOREFRONT"
		}
		return salesChannelStorefront, sid, ""
	default:
		return "", "", "sales_channel must be MARKETPLACE or STOREFRONT"
	}
}

// validateSalesChannelFilter validates an optional sales_channel filter on read/summary/reconcile APIs.
// Empty means unfiltered (all channels); non-empty must be MARKETPLACE or STOREFRONT.
func validateSalesChannelFilter(rawChannel string) (channel, errMsg string) {
	ch := strings.ToUpper(strings.TrimSpace(rawChannel))
	switch ch {
	case "":
		return "", ""
	case salesChannelMarketplace, salesChannelStorefront:
		return ch, ""
	default:
		return "", "sales_channel must be MARKETPLACE or STOREFRONT"
	}
}

// normalizeSalesChannelOutput ensures legacy/empty proto sales_channel values serialize as MARKETPLACE.
func normalizeSalesChannelOutput(raw string) string {
	if ch := strings.TrimSpace(raw); ch != "" {
		return ch
	}
	return salesChannelMarketplace
}

// requireSellerShopID returns the authenticated seller's non-empty JWT shop_id or writes 403.
func requireSellerShopID(w http.ResponseWriter, r *http.Request) (string, bool) {
	shopID := strings.TrimSpace(auth.ShopIDFromContext(r.Context()))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "seller account has no active shop")
		return "", false
	}
	return shopID, true
}
