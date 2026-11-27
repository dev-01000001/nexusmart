package e2e

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type prodW2BListResponse struct {
	Items    []ProductDTO `json:"items"`
	Page     int32        `json:"page"`
	PageSize int32        `json:"page_size"`
	Total    int64        `json:"total"`
}

type prodW2BUpdateCategoryReq struct {
	Name              string `json:"name,omitempty"`
	Slug              string `json:"slug,omitempty"`
	ParentID          string `json:"parent_id,omitempty"`
	CommissionRateBps *int32 `json:"commission_rate_bps,omitempty"`
	SortOrder         *int32 `json:"sort_order,omitempty"`
}

type prodW2BUpdateSKUInput struct {
	ID          string            `json:"id,omitempty"`
	SKUCode     string            `json:"sku_code"`
	Name        string            `json:"name"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	PriceVND    int64             `json:"price_vnd"`
	Stock       int32             `json:"stock"`
	WeightGrams int32             `json:"weight_grams"`
	ImageURL    string            `json:"image_url,omitempty"`
}

type prodW2BUpdateProductReq struct {
	CategoryID  string                  `json:"category_id,omitempty"`
	Name        string                  `json:"name,omitempty"`
	Description string                  `json:"description,omitempty"`
	Status      string                  `json:"status,omitempty"`
	SKUs        []prodW2BUpdateSKUInput `json:"skus,omitempty"`
}

type prodW2BNATSEnvelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

type prodW2BProductReviewedPayload struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	ShopID      string `json:"shop_id"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
	OccurredAt  string `json:"occurred_at"`
}

type prodW2BJSGetMsgResp struct {
	Type  string `json:"type"`
	Error *struct {
		Code        int    `json:"code"`
		ErrCode     int    `json:"err_code"`
		Description string `json:"description"`
	} `json:"error,omitempty"`
	Message *struct {
		Subject string `json:"subject"`
		Seq     uint64 `json:"seq"`
		Data    string `json:"data"`
		Hdrs    string `json:"hdrs"`
		Time    string `json:"time"`
	} `json:"message,omitempty"`
}

func prodW2BDialProduct(t *testing.T, env *testEnv) productv1.ProductServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.ProductGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial ProductService gRPC %s: %v", env.cfg.ProductGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return productv1.NewProductServiceClient(conn)
}

func prodW2BAuthCtx(t *testing.T, bearerToken string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearerToken))
}

func prodW2BMintServiceToken(serviceName string) string {
	secret := envOr("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me")
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":     serviceName,
		"role":    "admin",
		"service": serviceName,
		"iss":     "nexusmart",
		"jti":     uuid.NewString(),
		"iat":     now,
		"exp":     now + 300,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func prodW2BHasProductID(items []ProductDTO, targetID string) bool {
	for _, it := range items {
		if it.ID == targetID {
			return true
		}
	}
	return false
}

// prodW2BFetchLastNATSMessage fetches the last message for subject from the NEXUSMART
// JetStream stream using NATS's native TCP wire protocol (stdlib only, no extra deps).
func prodW2BFetchLastNATSMessage(subject string) (*prodW2BNATSEnvelope, string, error) {
	natsAddr := envOr("NATS_ADDR", "nats:4222")
	conn, err := net.DialTimeout("tcp", natsAddr, 5*time.Second)
	if err != nil {
		return nil, "", fmt.Errorf("dial nats %s: %w", natsAddr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	br := bufio.NewReader(conn)
	// Read server INFO line.
	if _, err := br.ReadString('\n'); err != nil {
		return nil, "", fmt.Errorf("read INFO: %w", err)
	}

	inbox := "_INBOX.prodw2b." + strings.ReplaceAll(uuid.NewString(), "-", "")
	reqBody, _ := json.Marshal(map[string]string{"last_by_subj": subject})
	cmd := fmt.Sprintf("CONNECT {\"verbose\":false,\"pedantic\":false}\r\nSUB %s 1\r\nPUB $JS.API.STREAM.MSG.GET.NEXUSMART %s %d\r\n%s\r\n",
		inbox, inbox, len(reqBody), string(reqBody))
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return nil, "", fmt.Errorf("write nats command: %w", err)
	}

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, "", fmt.Errorf("read nats line: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "PING") {
			_, _ = conn.Write([]byte("PONG\r\n"))
			continue
		}
		if !strings.HasPrefix(line, "MSG ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			return nil, "", fmt.Errorf("malformed MSG header: %q", line)
		}
		byteLen, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			return nil, "", fmt.Errorf("parse MSG byte length %q: %w", parts[len(parts)-1], err)
		}
		payload := make([]byte, byteLen+2)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, "", fmt.Errorf("read MSG payload: %w", err)
		}
		var jsResp prodW2BJSGetMsgResp
		if err := json.Unmarshal(payload[:byteLen], &jsResp); err != nil {
			return nil, "", fmt.Errorf("decode JS msg response: %w", err)
		}
		if jsResp.Error != nil {
			return nil, "", fmt.Errorf("JS error %d: %s", jsResp.Error.Code, jsResp.Error.Description)
		}
		if jsResp.Message == nil {
			return nil, "", fmt.Errorf("empty JS message")
		}
		rawData, err := base64.StdEncoding.DecodeString(jsResp.Message.Data)
		if err != nil {
			return nil, "", fmt.Errorf("decode base64 message data: %w", err)
		}
		var rawHdrs string
		if jsResp.Message.Hdrs != "" {
			if hb, hErr := base64.StdEncoding.DecodeString(jsResp.Message.Hdrs); hErr == nil {
				rawHdrs = string(hb)
			}
		}
		var env prodW2BNATSEnvelope
		if err := json.Unmarshal(rawData, &env); err != nil {
			return nil, rawHdrs, fmt.Errorf("unmarshal event envelope: %w (raw=%s)", err, string(rawData))
		}
		return &env, rawHdrs, nil
	}
}

func prodW2BCreateCategoryREST(t *testing.T, env *testEnv, adminTok string, req CreateCategoryRequest) CategoryDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, req, http.StatusCreated, http.StatusOK)
	var out CategoryResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode create category: %v", err)
	}
	if out.Category.ID == "" {
		t.Fatalf("create category returned empty id: %s", string(resp.Body))
	}
	return out.Category
}

func prodW2BCreateCODOrder(t *testing.T, env *testEnv, buyer *userFixture, seller *sellerFixture, sku SKUDTO) OrderDTO {
	t.Helper()
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, sku.WeightGrams, sku.PriceVND)
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "prod-w2b-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel: "MARKETPLACE",
		AddressID:    buyer.AddressID,
		FromCart:     false,
		Items: []CheckoutItemInputDTO{{
			SKUID:    sku.ID,
			Quantity: 1,
		}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{{
			ShopID:      seller.ShopID,
			CarrierCode: quote.CarrierCode,
			ServiceCode: quote.ServiceCode,
		}},
	}, http.StatusCreated, http.StatusOK)

	var out CheckoutResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode checkout: %v", err)
	}
	if len(out.Orders) != 1 {
		t.Fatalf("expected 1 order from checkout, got %d", len(out.Orders))
	}
	return out.Orders[0]
}

func TestE2E_ProductW2B_OutboxCategoriesPriceHistoryAndFeeSchedule(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	_, seller1, _ := env.ensureBaseMarketplace(t)
	prodClient := prodW2BDialProduct(t, env)

	// --- 1. Outbox: AdminReviewProduct publishes product.reviewed to NATS JetStream ---
	t.Run("AdminReviewProduct_PublishesProductReviewedEvent", func(t *testing.T) {
		cat := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B Outbox Cat " + env.runID,
			Slug:              strings.ToLower(env.uniqueCode("w2b-ob-")),
			CommissionRateBps: 500,
			SortOrder:         1,
		})

		prodName := "W2B Outbox Product " + env.uniqueCode("OBP")
		createResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
			CategoryID:  cat.ID,
			Name:        prodName,
			Description: "Test product.reviewed outbox relay",
			Images:      []string{"/products/ao-thun-cotton-trang.png"},
			SKUs:        []SKUInputDTO{{SKUCode: env.uniqueCode("OBSKU"), Name: "Default", PriceVND: 150000, Stock: 10, WeightGrams: 200}},
		}, http.StatusCreated, http.StatusOK)
		var created ProductResponse
		if err := DecodeJSON(createResp, &created); err != nil {
			t.Fatalf("decode create product: %v", err)
		}
		prodID := created.Product.ID
		reviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(prodID))

		// REJECT -> product.reviewed event published to NATS NEXUSMART stream.
		rejectReason := "E2E thiếu hình ảnh sản phẩm"
		env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
			Decision: "REJECT",
			Reason:   rejectReason,
		}, http.StatusOK)

		env.pollUntil(t, 10*time.Second, 200*time.Millisecond, "NATS product.reviewed REJECT event", func() (bool, string) {
			ev, hdrs, err := prodW2BFetchLastNATSMessage("product.reviewed")
			if err != nil {
				return false, err.Error()
			}
			var p prodW2BProductReviewedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return false, fmt.Sprintf("unmarshal payload: %v", err)
			}
			if p.ProductID != prodID || p.Decision != "REJECT" {
				return false, fmt.Sprintf("last event product_id=%s decision=%s", p.ProductID, p.Decision)
			}
			if ev.EventType != "product.reviewed" || ev.EventID == "" || p.ShopID != seller1.ShopID || p.ProductName != prodName || p.Reason != rejectReason || p.OccurredAt == "" {
				return false, fmt.Sprintf("payload mismatch: env=%+v payload=%+v", ev, p)
			}
			if !strings.Contains(hdrs, "Nats-Msg-Id: "+ev.EventID) {
				return false, fmt.Sprintf("missing Nats-Msg-Id header in %q", hdrs)
			}
			return true, ""
		})

		// APPROVE -> product.reviewed event published to NATS NEXUSMART stream.
		env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
			Decision: "APPROVE",
		}, http.StatusOK)

		env.pollUntil(t, 10*time.Second, 200*time.Millisecond, "NATS product.reviewed APPROVE event", func() (bool, string) {
			ev, _, err := prodW2BFetchLastNATSMessage("product.reviewed")
			if err != nil {
				return false, err.Error()
			}
			var p prodW2BProductReviewedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				return false, fmt.Sprintf("unmarshal payload: %v", err)
			}
			if p.ProductID != prodID || p.Decision != "APPROVE" {
				return false, fmt.Sprintf("last event product_id=%s decision=%s", p.ProductID, p.Decision)
			}
			if p.ShopID != seller1.ShopID || p.Reason != "" {
				return false, fmt.Sprintf("unexpected approve payload: %+v", p)
			}
			return true, ""
		})
	})

	// --- 2. CAT-B8 & CAT-B9: Category cycle, 3-level parent filter, slug normalization, presence flags, GetCategoryDescendants, soft-delete guard ---
	t.Run("CategoryTree_Cycle_3LevelFilter_Slug_PresenceFlags_And_DeleteGuard", func(t *testing.T) {
		tag := strings.ToLower(env.uniqueCode("tree"))

		// Slug normalization on create.
		dirtyCat := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "Danh Mục Dấu " + tag,
			Slug:              "W2B-Cat / Có Dấu! " + tag,
			CommissionRateBps: 550,
			SortOrder:         12,
		})
		wantSlug := "w2b-cat-co-dau-" + tag
		if dirtyCat.Slug != wantSlug {
			t.Fatalf("expected normalized slug %q, got %q", wantSlug, dirtyCat.Slug)
		}

		// Create 3-level hierarchy: Cha -> Con -> Chau.
		cha := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B Cha " + tag,
			Slug:              "w2b-cha-" + tag,
			CommissionRateBps: 500,
			SortOrder:         1,
		})
		con := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B Con " + tag,
			Slug:              "w2b-con-" + tag,
			ParentID:          cha.ID,
			CommissionRateBps: 600,
			SortOrder:         2,
		})
		chau := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B Cháu " + tag,
			Slug:              "w2b-chau-" + tag,
			ParentID:          con.ID,
			CommissionRateBps: 700,
			SortOrder:         3,
		})

		// Cycle prevention via REST PUT: Cha.parent = Con & Cha.parent = Chau -> HTTP 400.
		chaPutPath := fmt.Sprintf("/api/v1/admin/categories/%s", url.PathEscape(cha.ID))
		env.mustDoJSON(t, http.MethodPut, chaPutPath, adminTok, nil, prodW2BUpdateCategoryReq{
			Name:     cha.Name,
			ParentID: con.ID,
		}, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodPut, chaPutPath, adminTok, nil, prodW2BUpdateCategoryReq{
			Name:     cha.Name,
			ParentID: chau.ID,
		}, http.StatusBadRequest)

		// Presence flags: partial UpdateCategory omitting commission_rate_bps/sort_order/parent_id does NOT reset them.
		partialResp, err := prodClient.UpdateCategory(prodW2BAuthCtx(t, adminTok), &productv1.UpdateCategoryRequest{
			Id:   chau.ID,
			Name: "W2B Cháu Đổi Tên " + tag,
		})
		if err != nil {
			t.Fatalf("gRPC partial UpdateCategory: %v", err)
		}
		gotChau := partialResp.GetCategory()
		if gotChau.GetCommissionRateBps() != 700 || gotChau.GetSortOrder() != 3 || gotChau.GetParentId() != con.ID {
			t.Fatalf("partial UpdateCategory reset fields: bps=%d sort=%d parent=%q, want 700/3/%q",
				gotChau.GetCommissionRateBps(), gotChau.GetSortOrder(), gotChau.GetParentId(), con.ID)
		}

		// GetCategoryDescendants via gRPC with service token returns all 3 levels.
		svcTok := prodW2BMintServiceToken("e2e-promotion-test")
		descResp, err := prodClient.GetCategoryDescendants(prodW2BAuthCtx(t, svcTok), &productv1.GetCategoryDescendantsRequest{
			CategoryIds: []string{cha.ID},
		})
		if err != nil {
			t.Fatalf("GetCategoryDescendants: %v", err)
		}
		gotSet := make(map[string]bool, len(descResp.GetCategoryIds()))
		for _, id := range descResp.GetCategoryIds() {
			gotSet[id] = true
		}
		if len(gotSet) != 3 || !gotSet[cha.ID] || !gotSet[con.ID] || !gotSet[chau.ID] {
			t.Fatalf("GetCategoryDescendants(%s) = %v, want {%s, %s, %s}", cha.ID, descResp.GetCategoryIds(), cha.ID, con.ID, chau.ID)
		}

		// 3-level parent filter: product created in Chau MUST be returned when filtering by Cha.
		chauProd, _ := env.createApprovedProduct(t, seller1, chau.ID, "SP Cấp Cháu "+tag, 199000, 10, 250)

		for label, catID := range map[string]string{"Cha": cha.ID, "Con": con.ID, "Chau": chau.ID} {
			q := url.Values{"category_id": {catID}, "page": {"1"}, "page_size": {"100"}}
			resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?"+q.Encode(), "", nil, nil, http.StatusOK)
			var list prodW2BListResponse
			if err := DecodeJSON(resp, &list); err != nil {
				t.Fatalf("decode products by %s: %v", label, err)
			}
			if !prodW2BHasProductID(list.Items, chauProd.ID) {
				t.Fatalf("filter by %s (%s) missing 3rd-level product %s (total=%d)", label, catID, chauProd.ID, list.Total)
			}
		}

		// CAT-B8: deleting category with a soft-deleted product returns HTTP 409 (not 500); empty category deletes 200.
		softCat := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B SoftDel Cat " + tag,
			Slug:              "w2b-softdel-" + tag,
			CommissionRateBps: 500,
		})
		softProd, _ := env.createApprovedProduct(t, seller1, softCat.ID, "SP SoftDel "+tag, 120000, 5, 200)
		env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(softProd.ID)),
			seller1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/admin/categories/%s", url.PathEscape(softCat.ID)),
			adminTok, nil, nil, http.StatusConflict)

		emptyCat := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B Empty Cat " + tag,
			Slug:              "w2b-empty-" + tag,
			CommissionRateBps: 500,
		})
		env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/admin/categories/%s", url.PathEscape(emptyCat.ID)),
			adminTok, nil, nil, http.StatusOK)
	})

	// --- 3. Price history (min_price_30d_vnd) + Category fee schedule effective_from changes new orders without altering old orders ---
	t.Run("PriceHistory_And_FeeScheduleEffectiveFrom_DoesNotAlterOldOrder", func(t *testing.T) {
		tag := strings.ToLower(env.uniqueCode("fee"))
		feeCat := prodW2BCreateCategoryREST(t, env, adminTok, CreateCategoryRequest{
			Name:              "W2B Fee Cat " + tag,
			Slug:              "w2b-fee-" + tag,
			CommissionRateBps: 500, // 5%
			SortOrder:         5,
		})

		prod, sku := env.createApprovedProduct(t, seller1, feeCat.ID, "SP Lịch Phí "+tag, 200000, 20, 300)

		// Raise price 200,000 -> 250,000 -> min_price_30d_vnd must remain 200,000 in GetSKUsBatch & GetProduct.
		env.mustDoJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prod.ID)),
			seller1.AccessToken, nil, prodW2BUpdateProductReq{
				SKUs: []prodW2BUpdateSKUInput{{
					ID:          sku.ID,
					SKUCode:     sku.SKUCode,
					Name:        sku.Name,
					PriceVND:    250000,
					Stock:       20,
					WeightGrams: 300,
				}},
			}, http.StatusOK)
		sku.PriceVND = 250000

		batch1, err := prodClient.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{sku.ID}})
		if err != nil {
			t.Fatalf("GetSKUsBatch before schedule: %v", err)
		}
		if len(batch1.GetSkus()) != 1 {
			t.Fatalf("expected 1 SKU in batch, got %d", len(batch1.GetSkus()))
		}
		if batch1.GetSkus()[0].GetMinPrice_30DVnd() != 200000 {
			t.Fatalf("GetSKUsBatch min_price_30d_vnd = %d, want 200000", batch1.GetSkus()[0].GetMinPrice_30DVnd())
		}
		if batch1.GetSkus()[0].GetCommissionRateBps() != 500 {
			t.Fatalf("GetSKUsBatch initial commission_rate_bps = %d, want 500", batch1.GetSkus()[0].GetCommissionRateBps())
		}

		gpResp, err := prodClient.GetProduct(context.Background(), &productv1.GetProductRequest{Id: prod.ID})
		if err != nil {
			t.Fatalf("GetProduct: %v", err)
		}
		if len(gpResp.GetProduct().GetSkus()) != 1 || gpResp.GetProduct().GetSkus()[0].GetMinPrice_30DVnd() != 200000 {
			t.Fatalf("GetProduct min_price_30d_vnd mismatch: %+v", gpResp.GetProduct().GetSkus())
		}

		// Place COD Order 1 while commission is 500 bps -> commission_vnd = 250,000 * 500 / 10000 = 12,500.
		buyer := env.registerVerifyAndLogin(t, "prodw2b-buyer")
		env.verifyBuyerPhone(t, buyer)
		env.addAddress(t, buyer)
		order1 := prodW2BCreateCODOrder(t, env, buyer, seller1, sku)
		// Wave 2b buyer fee privacy: buyer checkout masks fees to 0; read seller order for authoritative commission.
		if order1.CommissionVND != 0 || order1.PaymentFeeVND != 0 || order1.PaymentFeeBps != 0 {
			t.Fatalf("buyer checkout must mask fees to 0, got comm=%d pay_fee=%d pay_fee_bps=%d",
				order1.CommissionVND, order1.PaymentFeeVND, order1.PaymentFeeBps)
		}
		for _, it := range order1.Items {
			if it.CommissionBps != 0 {
				t.Fatalf("buyer checkout must mask item commission_bps to 0, got %d", it.CommissionBps)
			}
		}
		order1Seller := env.getOrder(t, seller1.AccessToken, order1.ID)
		if order1Seller.CommissionVND != 12500 {
			t.Fatalf("order1 commission_vnd = %d, want 12500 (5%% of 250000)", order1Seller.CommissionVND)
		}

		// Past effective_from must be rejected with InvalidArgument.
		if _, err := prodClient.SetCategoryFeeSchedule(prodW2BAuthCtx(t, adminTok), &productv1.SetCategoryFeeScheduleRequest{
			CategoryId:        feeCat.ID,
			CommissionRateBps: 900,
			EffectiveFrom:     timestamppb.New(time.Now().UTC().Add(-2 * time.Minute)),
			Note:              "past rejected",
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SetCategoryFeeSchedule past effective_from: want InvalidArgument, got %v", err)
		}

		// Schedule commission change to 800 bps (8%) effective in +2 seconds.
		effAt := time.Now().UTC().Add(2 * time.Second)
		schedResp, err := prodClient.SetCategoryFeeSchedule(prodW2BAuthCtx(t, adminTok), &productv1.SetCategoryFeeScheduleRequest{
			CategoryId:        feeCat.ID,
			CommissionRateBps: 800,
			EffectiveFrom:     timestamppb.New(effAt),
			Note:              "E2E scheduled fee increase to 8%",
		})
		if err != nil {
			t.Fatalf("SetCategoryFeeSchedule future: %v", err)
		}
		if schedResp.GetSchedule().GetCommissionRateBps() != 800 {
			t.Fatalf("schedule bps = %d, want 800", schedResp.GetSchedule().GetCommissionRateBps())
		}

		// ListCategoryFeeSchedules returns both initial (500) and scheduled (800) entries.
		listResp, err := prodClient.ListCategoryFeeSchedules(prodW2BAuthCtx(t, adminTok), &productv1.ListCategoryFeeSchedulesRequest{
			CategoryId: feeCat.ID,
			Page:       1,
			PageSize:   10,
		})
		if err != nil {
			t.Fatalf("ListCategoryFeeSchedules: %v", err)
		}
		if listResp.GetTotal() < 2 || len(listResp.GetSchedules()) < 2 {
			t.Fatalf("expected >= 2 fee schedules, got total=%d len=%d", listResp.GetTotal(), len(listResp.GetSchedules()))
		}

		// Poll until effective_from passes and SKU.commission_rate_bps switches to 800.
		env.pollUntil(t, 10*time.Second, 250*time.Millisecond, "SKU commission_rate_bps to switch to 800", func() (bool, string) {
			b, gErr := prodClient.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{sku.ID}})
			if gErr != nil || len(b.GetSkus()) != 1 {
				return false, fmt.Sprintf("err=%v len=%d", gErr, len(b.GetSkus()))
			}
			got := b.GetSkus()[0].GetCommissionRateBps()
			return got == 800, fmt.Sprintf("commission_rate_bps=%d", got)
		})

		// Place COD Order 2 after effective_from -> commission_vnd = 250,000 * 800 / 10000 = 20,000.
		order2 := prodW2BCreateCODOrder(t, env, buyer, seller1, sku)
		order2Seller := env.getOrder(t, seller1.AccessToken, order2.ID)
		if order2Seller.CommissionVND != 20000 {
			t.Fatalf("order2 commission_vnd = %d, want 20000 (8%% of 250000)", order2Seller.CommissionVND)
		}

		// Existing Order 1 MUST remain unchanged at 12,500!
		order1Reloaded := env.getOrder(t, seller1.AccessToken, order1.ID)
		if order1Reloaded.CommissionVND != 12500 {
			t.Fatalf("existing order1 commission_vnd changed after fee schedule: got %d, want 12500", order1Reloaded.CommissionVND)
		}
	})

	// --- 4. ReleaseStock & ConfirmStock RBAC: buyer/seller JWT -> PermissionDenied, anonymous -> Unauthenticated ---
	t.Run("StockCompensationRBAC_BuyerAndSellerDenied", func(t *testing.T) {
		buyer := env.registerVerifyAndLogin(t, "prodw2b-stockrbac")
		probeOrderID := "ord-stock-rbac-" + uuid.NewString()

		if _, err := prodClient.ReleaseStock(context.Background(), &productv1.ReleaseStockRequest{OrderId: probeOrderID}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anonymous ReleaseStock: want Unauthenticated, got %v", err)
		}
		if _, err := prodClient.ConfirmStock(context.Background(), &productv1.ConfirmStockRequest{OrderId: probeOrderID}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anonymous ConfirmStock: want Unauthenticated, got %v", err)
		}
		for role, tok := range map[string]string{"buyer": buyer.AccessToken, "seller": seller1.AccessToken} {
			if _, err := prodClient.ReleaseStock(prodW2BAuthCtx(t, tok), &productv1.ReleaseStockRequest{OrderId: probeOrderID}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("%s ReleaseStock: want PermissionDenied, got %v", role, err)
			}
			if _, err := prodClient.ConfirmStock(prodW2BAuthCtx(t, tok), &productv1.ConfirmStockRequest{OrderId: probeOrderID}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("%s ConfirmStock: want PermissionDenied, got %v", role, err)
			}
		}
	})
}
