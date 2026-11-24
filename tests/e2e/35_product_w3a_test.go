package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type prodW3AListResponse struct {
	Items    []ProductDTO `json:"items"`
	Page     int32        `json:"page"`
	PageSize int32        `json:"page_size"`
	Total    int64        `json:"total"`
}

type prodW3AUpdateSKUInput struct {
	ID          string            `json:"id,omitempty"`
	SKUCode     string            `json:"sku_code"`
	Name        string            `json:"name"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	PriceVND    int64             `json:"price_vnd"`
	Stock       int32             `json:"stock"`
	WeightGrams int32             `json:"weight_grams"`
	ImageURL    string            `json:"image_url,omitempty"`
}

type prodW3AUpdateProductReq struct {
	Status string                  `json:"status,omitempty"`
	SKUs   []prodW3AUpdateSKUInput `json:"skus,omitempty"`
}

func prodW3ADialProduct(t *testing.T, env *testEnv) productv1.ProductServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.ProductGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial ProductService gRPC %s: %v", env.cfg.ProductGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return productv1.NewProductServiceClient(conn)
}

func prodW3AAuthCtx(t *testing.T, bearerToken string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if bearerToken == "" {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearerToken))
}

func prodW3ACreateProductREST(t *testing.T, env *testEnv, seller *sellerFixture, catID, name string, skus []SKUInputDTO) ProductDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller.AccessToken, nil, CreateProductRequest{
		CategoryID:  catID,
		Name:        name,
		Description: "W3A E2E " + name,
		Images:      []string{"https://sandbox.nexusmart.local/img/w3a.png"},
		SKUs:        skus,
	}, http.StatusCreated, http.StatusOK)
	var out ProductResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode create product %s: %v", name, err)
	}
	if out.Product.ID == "" || len(out.Product.SKUs) == 0 {
		t.Fatalf("create product %s missing id/skus: %s", name, string(resp.Body))
	}
	return out.Product
}

func prodW3AReviewProductREST(t *testing.T, env *testEnv, adminTok, prodID, decision, reason string) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(prodID))
	env.mustDoJSON(t, http.MethodPost, path, adminTok, nil, ReviewRequest{
		Decision: decision,
		Reason:   reason,
	}, http.StatusOK)
}

func TestE2E_ProductW3A_HiddenSKURedactionAndListProductsBySKUIDs(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "prodw3a-buyer")
	prodClient := prodW3ADialProduct(t, env)

	// --- 1. D2: GetSKUsBatch redacts hidden/inactive SKUs for anonymous/buyer/other-seller, keeps full for owner/admin ---
	t.Run("D2_GetSKUsBatch_HiddenSKURedaction_GRPC", func(t *testing.T) {
		tag := env.uniqueCode("D2")

		// Active product with 1 active SKU and 1 deactivated SKU.
		actProd := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A Active "+tag, []SKUInputDTO{
			{SKUCode: tag + "-ACT", Name: "Active", Attributes: map[string]string{"color": "blue"}, PriceVND: 210000, Stock: 10, WeightGrams: 250, ImageURL: "https://sandbox.nexusmart.local/img/act.png"},
			{SKUCode: tag + "-OFF", Name: "Deactivated", Attributes: map[string]string{"color": "red"}, PriceVND: 220000, Stock: 8, WeightGrams: 260, ImageURL: "https://sandbox.nexusmart.local/img/off.png"},
		})
		prodW3AReviewProductREST(t, env, adminTok, actProd.ID, "APPROVE", "")

		var skuAct, skuOff SKUDTO
		for _, sk := range actProd.SKUs {
			if sk.SKUCode == tag+"-ACT" {
				skuAct = sk
			} else if sk.SKUCode == tag+"-OFF" {
				skuOff = sk
			}
		}
		if skuAct.ID == "" || skuOff.ID == "" {
			t.Fatalf("missing created SKUs: %+v", actProd.SKUs)
		}
		// Deactivate skuOff by omitting it in PUT.
		env.mustDoJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(actProd.ID)),
			seller1.AccessToken, nil, prodW3AUpdateProductReq{
				SKUs: []prodW3AUpdateSKUInput{{
					ID: skuAct.ID, SKUCode: skuAct.SKUCode, Name: skuAct.Name, Attributes: map[string]string{"color": "blue"},
					PriceVND: 210000, Stock: 10, WeightGrams: 250, ImageURL: "https://sandbox.nexusmart.local/img/act.png",
				}},
			}, http.StatusOK)

		// PENDING product.
		pendProd := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A Pending "+tag, []SKUInputDTO{
			{SKUCode: tag + "-PEND", Name: "Pend", PriceVND: 230000, Stock: 6, WeightGrams: 200, ImageURL: "https://sandbox.nexusmart.local/img/pend.png"},
		})
		skuPend := pendProd.SKUs[0]

		// REJECTED product.
		rejProd := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A Rejected "+tag, []SKUInputDTO{
			{SKUCode: tag + "-REJ", Name: "Rej", PriceVND: 240000, Stock: 6, WeightGrams: 200, ImageURL: "https://sandbox.nexusmart.local/img/rej.png"},
		})
		prodW3AReviewProductREST(t, env, adminTok, rejProd.ID, "REJECT", "E2E D2 reject")
		skuRej := rejProd.SKUs[0]

		// HIDDEN product.
		hidProd := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A Hidden "+tag, []SKUInputDTO{
			{SKUCode: tag + "-HID", Name: "Hid", PriceVND: 250000, Stock: 6, WeightGrams: 200, ImageURL: "https://sandbox.nexusmart.local/img/hid.png"},
		})
		prodW3AReviewProductREST(t, env, adminTok, hidProd.ID, "APPROVE", "")
		env.mustDoJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(hidProd.ID)),
			seller1.AccessToken, nil, prodW3AUpdateProductReq{Status: "HIDDEN"}, http.StatusOK)
		skuHid := hidProd.SKUs[0]

		// Soft-deleted product.
		delProd := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A Deleted "+tag, []SKUInputDTO{
			{SKUCode: tag + "-DEL", Name: "Del", PriceVND: 260000, Stock: 6, WeightGrams: 200, ImageURL: "https://sandbox.nexusmart.local/img/del.png"},
		})
		prodW3AReviewProductREST(t, env, adminTok, delProd.ID, "APPROVE", "")
		env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(delProd.ID)),
			seller1.AccessToken, nil, nil, http.StatusOK)
		skuDel := delProd.SKUs[0]

		allIDs := []string{skuAct.ID, skuOff.ID, skuPend.ID, skuRej.ID, skuHid.ID, skuDel.ID}
		hiddenSet := map[string]bool{
			skuOff.ID: true, skuPend.ID: true, skuRej.ID: true, skuHid.ID: true, skuDel.ID: true,
		}

		callers := []struct {
			name           string
			token          string
			canSeeInactive bool
		}{
			{"anonymous", "", false},
			{"buyer", buyer.AccessToken, false},
			{"other_seller", seller2.AccessToken, false},
			{"owner_seller", seller1.AccessToken, true},
			{"admin", adminTok, true},
		}

		for _, caller := range callers {
			t.Run(caller.name, func(t *testing.T) {
				resp, err := prodClient.GetSKUsBatch(prodW3AAuthCtx(t, caller.token), &productv1.GetSKUsBatchRequest{SkuIds: allIDs})
				if err != nil {
					t.Fatalf("GetSKUsBatch(%s): %v", caller.name, err)
				}
				if len(resp.GetSkus()) != len(allIDs) {
					t.Fatalf("GetSKUsBatch(%s): want %d rows preserved, got %d", caller.name, len(allIDs), len(resp.GetSkus()))
				}
				for _, sk := range resp.GetSkus() {
					if sk.GetId() == skuAct.ID {
						if !sk.GetIsActive() || sk.GetPriceVnd() != 210000 || sk.GetCommissionRateBps() <= 0 {
							t.Fatalf("%s active SKU must remain unredacted with commission_rate_bps>0: %+v", caller.name, sk)
						}
						continue
					}
					if !hiddenSet[sk.GetId()] {
						t.Fatalf("unexpected SKU %s in response", sk.GetId())
					}
					if sk.GetIsActive() || sk.GetShopId() != seller1.ShopID || sk.GetProductId() == "" || sk.GetProductName() == "" || sk.GetName() == "" {
						t.Fatalf("%s hidden SKU kept fields invalid: %+v", caller.name, sk)
					}
					if caller.canSeeInactive {
						if sk.GetPriceVnd() <= 0 || sk.GetSkuCode() == "" || sk.GetCategoryId() == "" || sk.GetCommissionRateBps() <= 0 {
							t.Fatalf("%s must see full hidden SKU fields: %+v", caller.name, sk)
						}
					} else {
						if sk.GetSkuCode() != "" || len(sk.GetAttributes()) != 0 || sk.GetPriceVnd() != 0 ||
							sk.GetStock() != 0 || sk.GetReservedStock() != 0 || sk.GetAvailableStock() != 0 ||
							sk.GetVersion() != 0 || sk.GetWeightGrams() != 0 || sk.GetCategoryId() != "" ||
							sk.GetCommissionRateBps() != 0 || sk.GetMinPrice_30DVnd() != 0 {
							t.Fatalf("%s hidden SKU leaked sensitive fields: %+v", caller.name, sk)
						}
					}
				}
			})
		}
	})

	// --- 2. D8: GET /api/v1/products?sku_ids=... filters public products, supports pagination, rejects >200 & invalid ---
	t.Run("D8_ListProducts_BySKUIDs_REST", func(t *testing.T) {
		tag := env.uniqueCode("D8")

		p1 := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A D8 MultiSKU "+tag, []SKUInputDTO{
			{SKUCode: tag + "-P1A", Name: "M", PriceVND: 110000, Stock: 10, WeightGrams: 200},
			{SKUCode: tag + "-P1B", Name: "L", PriceVND: 120000, Stock: 10, WeightGrams: 200},
		})
		prodW3AReviewProductREST(t, env, adminTok, p1.ID, "APPROVE", "")

		p2, p2SKU := env.createApprovedProduct(t, seller1, cat.ID, "W3A D8 Second "+tag, 330000, 10, 200)

		// Approved with 0 stock -> OUT_OF_STOCK (still public!).
		p3 := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A D8 OOS "+tag, []SKUInputDTO{
			{SKUCode: tag + "-P3OOS", Name: "Std", PriceVND: 220000, Stock: 0, WeightGrams: 200},
		})
		prodW3AReviewProductREST(t, env, adminTok, p3.ID, "APPROVE", "")

		// Non-public products: PENDING and HIDDEN must be excluded.
		pPend := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A D8 Pending "+tag, []SKUInputDTO{
			{SKUCode: tag + "-PEND", Name: "Std", PriceVND: 150000, Stock: 5, WeightGrams: 200},
		})
		pHid := prodW3ACreateProductREST(t, env, seller1, cat.ID, "W3A D8 Hidden "+tag, []SKUInputDTO{
			{SKUCode: tag + "-HID", Name: "Std", PriceVND: 160000, Stock: 5, WeightGrams: 200},
		})
		prodW3AReviewProductREST(t, env, adminTok, pHid.ID, "APPROVE", "")
		env.mustDoJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(pHid.ID)),
			seller1.AccessToken, nil, prodW3AUpdateProductReq{Status: "HIDDEN"}, http.StatusOK)

		skuList := []string{
			p1.SKUs[0].ID,
			p1.SKUs[1].ID, // same product p1 -> deduplicated
			p2SKU.ID,
			p3.SKUs[0].ID,
			pPend.SKUs[0].ID,
			pHid.SKUs[0].ID,
		}
		q1 := url.Values{
			"sku_ids":   {strings.Join(skuList, ",")},
			"sort_by":   {"price_asc"},
			"page":      {"1"},
			"page_size": {"2"},
		}
		resp1 := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?"+q1.Encode(), "", nil, nil, http.StatusOK)
		var list1 prodW3AListResponse
		if err := DecodeJSON(resp1, &list1); err != nil {
			t.Fatalf("decode page 1: %v", err)
		}
		if list1.Total != 3 || len(list1.Items) != 2 || list1.Items[0].ID != p1.ID || list1.Items[1].ID != p3.ID {
			t.Fatalf("page 1 want total=3 [p1=%s, p3=%s], got total=%d items=%+v", p1.ID, p3.ID, list1.Total, list1.Items)
		}

		q2 := url.Values{
			"sku_ids":   {strings.Join(skuList, ",")},
			"sort_by":   {"price_asc"},
			"page":      {"2"},
			"page_size": {"2"},
		}
		resp2 := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?"+q2.Encode(), "", nil, nil, http.StatusOK)
		var list2 prodW3AListResponse
		if err := DecodeJSON(resp2, &list2); err != nil {
			t.Fatalf("decode page 2: %v", err)
		}
		if list2.Total != 3 || len(list2.Items) != 1 || list2.Items[0].ID != p2.ID {
			t.Fatalf("page 2 want total=3 [p2=%s], got total=%d items=%+v", p2.ID, list2.Total, list2.Items)
		}

		// > 200 sku_ids -> 400.
		tooMany := make([]string, 201)
		for i := range tooMany {
			tooMany[i] = fmt.Sprintf("sku-e2e-%03d", i)
		}
		qTooMany := url.Values{"sku_ids": {strings.Join(tooMany, ",")}}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/products?"+qTooMany.Encode(), "", nil, nil, http.StatusBadRequest)

		// Blank/invalid sku_ids element -> 400.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/products?sku_ids=,,", "", nil, nil, http.StatusBadRequest)
	})
}
