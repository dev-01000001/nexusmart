package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/llm"
	"github.com/nexusmart/core/services/assistant/internal/policy"
	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"github.com/nexusmart/core/services/assistant/internal/retriever"
)

// Tool names exposed by the AI Assistant registry.
const (
	ToolSearchProducts  = "search_products"
	ToolSearchPolicies  = "search_policies"
	ToolGetOrderStatus  = "get_order_status"
	ToolAddToCart       = "add_to_cart"
	ToolHandoffToAgent  = "handoff_to_agent"
	ToolGetSellerReport = "get_seller_report"
)

// Registry implements tools.Executor with strict role-based tool exposure,
// end-user JWT forwarding, storefront shop isolation, and confirmation checks.
type Registry struct {
	productRet retriever.ProductRetriever
	policyRet  policy.Retriever
	gw         GatewayClient
	guardrail  guardrail.Engine
	log        *slog.Logger
}

// NewRegistry constructs a tool Registry.
func NewRegistry(
	productRet retriever.ProductRetriever,
	policyRet policy.Retriever,
	gw GatewayClient,
	g guardrail.Engine,
	log *slog.Logger,
) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{
		productRet: productRet,
		policyRet:  policyRet,
		gw:         gw,
		guardrail:  g,
		log:        log,
	}
}

// AllowedTools returns the role-filtered set of LLM tool schemas.
// Never exposes checkout, payment, refund-execution, or SQL tools.
func (r *Registry) AllowedTools(role string) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{
		{
			Name:        ToolSearchProducts,
			Description: "Tìm kiếm sản phẩm thực tế trên NexusMart (tự động giới hạn theo đúng gian hàng đang xem).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":         map[string]any{"type": "string", "description": "Từ khoá hoặc mô tả nhu cầu sản phẩm"},
					"shop_id":       map[string]any{"type": "string", "description": "Mã gian hàng (bị ghi đè bởi ngữ cảnh gian hàng nếu có)"},
					"category":      map[string]any{"type": "string", "description": "Danh mục sản phẩm"},
					"min_price_vnd": map[string]any{"type": "integer", "description": "Giá tối thiểu (VNĐ)"},
					"max_price_vnd": map[string]any{"type": "integer", "description": "Giá tối đa (VNĐ)"},
					"sort":          map[string]any{"type": "string", "enum": []string{"relevance", "price_asc", "price_desc", "newest"}},
					"page_size":     map[string]any{"type": "integer", "description": "Số sản phẩm tối đa (mặc định 6)"},
				},
			},
		},
		{
			Name:        ToolSearchPolicies,
			Description: "Tra cứu tài liệu chính sách chính thức của NexusMart (đổi trả, vận chuyển, thanh toán, COD, phí sàn, voucher, flash sale, bảo mật).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Câu hỏi hoặc chủ đề chính sách cần tra cứu"},
					"top_k": map[string]any{"type": "integer", "description": "Số đoạn chính sách cần trả về (mặc định 4)"},
				},
			},
		},
		{
			Name:        ToolGetOrderStatus,
			Description: "Tra cứu trạng thái đơn hàng của chính người mua đang đăng nhập.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"order_id": map[string]any{"type": "string", "description": "Mã đơn hàng cụ thể (để trống để xem danh sách đơn gần đây)"},
					"status":   map[string]any{"type": "string", "description": "Lọc trạng thái đơn hàng"},
				},
			},
		},
		{
			Name:        ToolAddToCart,
			Description: "Thêm SKU sản phẩm vào giỏ hàng của người mua (yêu cầu đăng nhập và xác nhận rõ ràng trước khi thực thi).",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"sku_id", "quantity"},
				"properties": map[string]any{
					"sku_id":       map[string]any{"type": "string", "description": "Mã SKU cần thêm vào giỏ"},
					"quantity":     map[string]any{"type": "integer", "description": "Số lượng (> 0)"},
					"product_name": map[string]any{"type": "string", "description": "Tên sản phẩm để hiển thị xác nhận"},
					"confirmed":    map[string]any{"type": "boolean", "description": "True khi người mua đã xác nhận thêm vào giỏ"},
				},
			},
		},
		{
			Name:        ToolHandoffToAgent,
			Description: "Chuyển tiếp hội thoại sang nhân viên hỗ trợ hoặc tạo hội thoại chat với gian hàng.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"shop_id": map[string]any{"type": "string", "description": "Mã gian hàng cần liên hệ"},
					"reason":  map[string]any{"type": "string", "description": "Lý do chuyển tiếp"},
					"summary": map[string]any{"type": "string", "description": "Tóm tắt vấn đề của người mua"},
				},
			},
		},
	}

	if strings.EqualFold(strings.TrimSpace(role), "seller") {
		defs = append(defs, llm.ToolDefinition{
			Name:        ToolGetSellerReport,
			Description: "Xem báo cáo kinh doanh (doanh thu VNĐ, số đơn, tỷ lệ huỷ, số dư, sản phẩm bán chạy) của chính gian hàng người bán.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"period":   map[string]any{"type": "string", "description": "Khoảng thời gian báo cáo (ví dụ: 7d, 30d, this_week, this_month)"},
					"question": map[string]any{"type": "string", "description": "Câu hỏi kinh doanh của người bán"},
				},
			},
		})
	}

	return defs
}

// Execute dispatches and enforces RBAC, shop isolation, auth, confirmation, and JWT forwarding.
func (r *Registry) Execute(ctx context.Context, cc CallContext, name string, args map[string]any) Result {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case ToolSearchProducts:
		return r.execSearchProducts(ctx, cc, args)
	case ToolSearchPolicies:
		return r.execSearchPolicies(ctx, args)
	case ToolGetOrderStatus:
		return r.execGetOrderStatus(ctx, cc, args)
	case ToolAddToCart:
		return r.execAddToCart(ctx, cc, args)
	case ToolGetSellerReport:
		return r.execGetSellerReport(ctx, cc, args)
	case ToolHandoffToAgent:
		return r.execHandoffToAgent(ctx, cc, args)
	default:
		return Result{
			Name:  name,
			OK:    false,
			Error: "công cụ không được hỗ trợ hoặc không nằm trong phạm vi cho phép",
		}
	}
}

func (r *Registry) execSearchProducts(ctx context.Context, cc CallContext, args map[string]any) Result {
	if r.productRet == nil {
		return Result{Name: ToolSearchProducts, OK: false, Error: "dịch vụ tìm kiếm sản phẩm tạm thời chưa khả dụng"}
	}

	// CRITICAL TENANT ISOLATION: storefront cc.ShopID always overrides LLM-supplied shop_id.
	shopID := stringArg(args, "shop_id")
	if scoped := strings.TrimSpace(cc.ShopID); scoped != "" {
		shopID = scoped
	}

	pageSize := int32Arg(args, "page_size", 6)
	if pageSize <= 0 {
		pageSize = 6
	}
	if pageSize > 20 {
		pageSize = 20
	}

	q := retriever.SearchQuery{
		Query:       stringArg(args, "query"),
		ShopID:      shopID,
		Category:    stringArg(args, "category"),
		MinPriceVND: int64Arg(args, "min_price_vnd", 0),
		MaxPriceVND: int64Arg(args, "max_price_vnd", 0),
		Sort:        stringArg(args, "sort"),
		Page:        1,
		PageSize:    pageSize,
	}

	hits, total, err := r.productRet.Search(ctx, q)
	if err != nil {
		r.log.Warn("search_products failed", slog.String("error", err.Error()))
		return Result{Name: ToolSearchProducts, OK: false, Error: "không thể tìm kiếm sản phẩm lúc này, vui lòng thử lại sau"}
	}

	// Defense-in-depth post-filter & description sanitization even if upstream retriever is a custom stub.
	safeHits := make([]retriever.ProductHit, 0, len(hits))
	for _, h := range hits {
		if shopID != "" && h.ShopID != shopID {
			continue
		}
		if r.guardrail != nil && h.Description != "" {
			res := r.guardrail.SanitizeUntrustedData("product_description", h.Description)
			if res.InjectionFound || res.SanitizedText != "" {
				h.Description = res.SanitizedText
			}
		}
		safeHits = append(safeHits, h)
	}
	if shopID != "" && int64(len(safeHits)) < total && len(safeHits) != len(hits) {
		total = int64(len(safeHits))
	}

	return Result{
		Name:    ToolSearchProducts,
		OK:      true,
		Private: false,
		Data: map[string]any{
			"shop_id":  shopID,
			"products": safeHits,
			"total":    total,
			"count":    len(safeHits),
		},
	}
}

func (r *Registry) execSearchPolicies(ctx context.Context, args map[string]any) Result {
	if r.policyRet == nil {
		return Result{Name: ToolSearchPolicies, OK: false, Error: "kho tài liệu chính sách tạm thời chưa khả dụng"}
	}
	query := stringArg(args, "query")
	if query == "" {
		return Result{Name: ToolSearchPolicies, OK: false, Error: "vui lòng cung cấp nội dung chính sách cần tra cứu"}
	}
	topK := int(int32Arg(args, "top_k", 4))
	if topK <= 0 {
		topK = 4
	}
	if topK > 6 {
		topK = 6
	}

	chunks, err := r.policyRet.Search(ctx, query, topK)
	if err != nil {
		r.log.Warn("search_policies failed", slog.String("error", err.Error()))
		return Result{Name: ToolSearchPolicies, OK: false, Error: "không thể tra cứu chính sách lúc này"}
	}

	citations := make([]string, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for _, c := range chunks {
		if c.Citation == "" {
			continue
		}
		if _, dup := seen[c.Citation]; !dup {
			seen[c.Citation] = struct{}{}
			citations = append(citations, c.Citation)
		}
	}

	return Result{
		Name:    ToolSearchPolicies,
		OK:      true,
		Private: false,
		Data: map[string]any{
			"chunks":    chunks,
			"citations": citations,
			"count":     len(chunks),
		},
	}
}

func (r *Registry) execGetOrderStatus(ctx context.Context, cc CallContext, args map[string]any) Result {
	if sanitizeBearerToken(cc.RawBearer) == "" {
		return Result{
			Name:         ToolGetOrderStatus,
			OK:           false,
			RequiresAuth: true,
			Private:      true,
			Error:        "bạn cần đăng nhập để tra cứu trạng thái đơn hàng",
		}
	}
	if r.gw == nil {
		return Result{Name: ToolGetOrderStatus, OK: false, Private: true, Error: "dịch vụ đơn hàng tạm thời chưa khả dụng"}
	}

	orderID := stringArg(args, "order_id")
	if orderID != "" {
		order, err := r.gw.GetOrder(ctx, cc.RawBearer, orderID)
		if err != nil {
			return mapOrderError(ToolGetOrderStatus, err)
		}
		return Result{
			Name:    ToolGetOrderStatus,
			OK:      true,
			Private: true,
			Data: map[string]any{
				"order": order,
			},
		}
	}

	orders, err := r.gw.ListBuyerOrders(ctx, cc.RawBearer, cc.ShopID, normalizeOrderStatusFilter(stringArg(args, "status")))
	if err != nil {
		return mapOrderError(ToolGetOrderStatus, err)
	}
	return Result{
		Name:    ToolGetOrderStatus,
		OK:      true,
		Private: true,
		Data: map[string]any{
			"orders": orders,
			"count":  len(orders),
		},
	}
}

func (r *Registry) execAddToCart(ctx context.Context, cc CallContext, args map[string]any) Result {
	if sanitizeBearerToken(cc.RawBearer) == "" {
		return Result{
			Name:         ToolAddToCart,
			OK:           false,
			RequiresAuth: true,
			Private:      true,
			Error:        "bạn cần đăng nhập để thêm sản phẩm vào giỏ hàng",
		}
	}

	skuID := stringArg(args, "sku_id")
	if skuID == "" {
		return Result{
			Name:    ToolAddToCart,
			OK:      false,
			Private: true,
			Error:   "vui lòng chọn phân loại (SKU) cụ thể trước khi thêm vào giỏ hàng",
		}
	}

	qty := int32Arg(args, "quantity", 1)
	if _, hasQty := args["quantity"]; hasQty && qty <= 0 {
		return Result{
			Name:    ToolAddToCart,
			OK:      false,
			Private: true,
			Error:   "số lượng sản phẩm phải lớn hơn 0",
		}
	}
	if qty <= 0 {
		return Result{
			Name:    ToolAddToCart,
			OK:      false,
			Private: true,
			Error:   "số lượng sản phẩm phải lớn hơn 0",
		}
	}

	confirmed := cc.Confirmed || boolArg(args, "confirmed")
	priceVND := int64Arg(args, "price_vnd", 0)
	if !confirmed {
		productName := stringArg(args, "product_name")
		data := map[string]any{
			"action":       ToolAddToCart,
			"sku_id":       skuID,
			"quantity":     qty,
			"product_name": productName,
			"message":      fmt.Sprintf("Vui lòng xác nhận thêm %d sản phẩm (SKU %s) vào giỏ hàng của bạn.", qty, skuID),
		}
		if priceVND > 0 {
			data["price_vnd"] = priceVND
		}
		return Result{
			Name:                 ToolAddToCart,
			OK:                   true,
			RequiresConfirmation: true,
			Private:              true,
			Data:                 data,
		}
	}

	if r.gw == nil {
		return Result{Name: ToolAddToCart, OK: false, Private: true, Error: "dịch vụ giỏ hàng tạm thời chưa khả dụng"}
	}

	resp, err := r.gw.AddCartItem(ctx, cc.RawBearer, skuID, qty)
	if err != nil {
		var statusErr *resilience.HTTPStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusUnauthorized:
				return Result{Name: ToolAddToCart, OK: false, RequiresAuth: true, Private: true, Error: "phiên đăng nhập đã hết hạn, vui lòng đăng nhập lại"}
			case http.StatusConflict, http.StatusUnprocessableEntity:
				return Result{Name: ToolAddToCart, OK: false, Private: true, Error: "sản phẩm tạm hết hàng hoặc không đủ tồn kho để thêm vào giỏ"}
			case http.StatusNotFound, http.StatusBadRequest:
				return Result{Name: ToolAddToCart, OK: false, Private: true, Error: "không tìm thấy phân loại sản phẩm (SKU) hợp lệ"}
			}
		}
		return Result{Name: ToolAddToCart, OK: false, Private: true, Error: "không thể thêm sản phẩm vào giỏ hàng lúc này"}
	}

	return Result{
		Name:    ToolAddToCart,
		OK:      true,
		Private: true,
		Data: map[string]any{
			"sku_id":           skuID,
			"quantity":         qty,
			"cart":             resp["cart"],
			"clamped_to_stock": resp["clamped_to_stock"],
			"cart_url":         "/cart",
		},
	}
}

func (r *Registry) execGetSellerReport(ctx context.Context, cc CallContext, args map[string]any) Result {
	if sanitizeBearerToken(cc.RawBearer) == "" ||
		!strings.EqualFold(strings.TrimSpace(cc.UserRole), "seller") ||
		strings.TrimSpace(cc.SellerShopID) == "" {
		return Result{
			Name:    ToolGetSellerReport,
			OK:      false,
			Private: true,
			Error:   "chỉ người bán đã xác thực gian hàng mới có quyền xem báo cáo kinh doanh",
		}
	}

	if requestedShop := stringArg(args, "shop_id"); requestedShop != "" && requestedShop != cc.SellerShopID {
		return Result{
			Name:    ToolGetSellerReport,
			OK:      false,
			Private: true,
			Error:   "không có quyền xem báo cáo kinh doanh của gian hàng khác",
		}
	}

	if r.gw == nil {
		return Result{Name: ToolGetSellerReport, OK: false, Private: true, Error: "dịch vụ báo cáo người bán tạm thời chưa khả dụng"}
	}

	orders, err := r.gw.ListSellerOrders(ctx, cc.RawBearer)
	if err != nil {
		return mapSellerReportError(err)
	}
	balance, err := r.gw.GetSellerBalance(ctx, cc.RawBearer)
	if err != nil {
		return mapSellerReportError(err)
	}

	metrics := computeSellerMetrics(orders, balance)
	period := stringArg(args, "period")
	if period == "" {
		period = "all"
	}

	metricsMap := map[string]any{
		"total_orders":          metrics.totalOrders,
		"completed_orders":      metrics.completedOrders,
		"cancelled_orders":      metrics.cancelledOrders,
		"cancel_rate_pct":       metrics.cancelRatePct,
		"gross_revenue_vnd":     metrics.grossRevenueVND,
		"available_balance_vnd": metrics.availableBalanceVND,
		"pending_balance_vnd":   metrics.pendingBalanceVND,
		"top_products":          metrics.topProducts,
	}

	return Result{
		Name:    ToolGetSellerReport,
		OK:      true,
		Private: true,
		Data: map[string]any{
			"shop_id":               cc.SellerShopID,
			"period":                period,
			"metrics":               metricsMap,
			"total_orders":          metrics.totalOrders,
			"completed_orders":      metrics.completedOrders,
			"cancelled_orders":      metrics.cancelledOrders,
			"cancel_rate_pct":       metrics.cancelRatePct,
			"gross_revenue_vnd":     metrics.grossRevenueVND,
			"available_balance_vnd": metrics.availableBalanceVND,
			"pending_balance_vnd":   metrics.pendingBalanceVND,
			"top_products":          metrics.topProducts,
		},
	}
}

func (r *Registry) execHandoffToAgent(ctx context.Context, cc CallContext, args map[string]any) Result {
	if sanitizeBearerToken(cc.RawBearer) == "" {
		return Result{
			Name:         ToolHandoffToAgent,
			OK:           false,
			RequiresAuth: true,
			Private:      true,
			Error:        "bạn cần đăng nhập để kết nối với nhân viên hỗ trợ",
		}
	}

	shopID := strings.TrimSpace(cc.ShopID)
	if shopID == "" {
		shopID = stringArg(args, "shop_id")
	}
	reason := stringArg(args, "reason")
	summary := stringArg(args, "summary")

	if r.gw == nil || shopID == "" {
		return Result{
			Name:    ToolHandoffToAgent,
			OK:      true,
			Private: true,
			Data: map[string]any{
				"status":  "queued_offline",
				"shop_id": shopID,
				"reason":  reason,
				"summary": summary,
				"message": "Yêu cầu hỗ trợ của bạn đã được ghi nhận vào hàng đợi chăm sóc khách hàng.",
			},
		}
	}

	initialMsg := summary
	if initialMsg == "" {
		initialMsg = reason
	}
	var (
		resp map[string]any
		err  error
	)
	if msgClient, ok := r.gw.(interface {
		CreateChatConversationWithMessage(ctx context.Context, rawBearer, shopID, initialMessage string) (map[string]any, error)
	}); ok && initialMsg != "" {
		resp, err = msgClient.CreateChatConversationWithMessage(ctx, cc.RawBearer, shopID, initialMsg)
	} else {
		resp, err = r.gw.CreateChatConversation(ctx, cc.RawBearer, shopID)
	}
	if err != nil {
		var statusErr *resilience.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
			return Result{
				Name:         ToolHandoffToAgent,
				OK:           false,
				RequiresAuth: true,
				Private:      true,
				Error:        "phiên đăng nhập đã hết hạn, vui lòng đăng nhập lại",
			}
		}
		r.log.Warn("handoff chat service unavailable, degrading to queued_offline", slog.String("error", err.Error()))
		return Result{
			Name:    ToolHandoffToAgent,
			OK:      true,
			Private: true,
			Data: map[string]any{
				"status":  "queued_offline",
				"shop_id": shopID,
				"reason":  reason,
				"summary": summary,
				"message": "Hệ thống chat trực tiếp tạm thời bận, yêu cầu hỗ trợ đã được lưu vào hàng đợi.",
			},
		}
	}

	chatConvID := extractConversationID(resp)
	return Result{
		Name:    ToolHandoffToAgent,
		OK:      true,
		Private: true,
		Data: map[string]any{
			"status":               "connected",
			"shop_id":              shopID,
			"chat_conversation_id": chatConvID,
			"conversation_id":      chatConvID,
			"conversation":         resp,
			"reason":               reason,
			"summary":              summary,
		},
	}
}

type sellerReportMetrics struct {
	totalOrders         int
	completedOrders     int
	cancelledOrders     int
	cancelRatePct       float64
	grossRevenueVND     int64
	availableBalanceVND int64
	pendingBalanceVND   int64
	topProducts         []map[string]any
}

type productAgg struct {
	productID   string
	productName string
	unitsSold   int32
	revenueVND  int64
}

func computeSellerMetrics(orders []map[string]any, balance map[string]any) sellerReportMetrics {
	var m sellerReportMetrics
	m.totalOrders = len(orders)

	byProd := make(map[string]*productAgg)
	for _, ord := range orders {
		st := strings.ToUpper(strings.TrimSpace(stringFromAny(ord["status"])))
		switch st {
		case "COMPLETED":
			m.completedOrders++
		case "CANCELLED":
			m.cancelledOrders++
		}

		if st == "CANCELLED" || st == "RETURNED" {
			continue
		}

		orderRev := int64FromAny(ord["total_payable_vnd"])
		if orderRev <= 0 {
			orderRev = int64FromAny(ord["subtotal_vnd"])
		}

		var itemsRev int64
		if rawItems, ok := ord["items"].([]any); ok {
			for _, rawIt := range rawItems {
				it, ok := rawIt.(map[string]any)
				if !ok {
					continue
				}
				pid := strings.TrimSpace(stringFromAny(it["product_id"]))
				pname := strings.TrimSpace(stringFromAny(it["product_name"]))
				if pid == "" {
					pid = pname
				}
				qty := int32FromAny(it["quantity"])
				if qty <= 0 {
					qty = 1
				}
				line := int64FromAny(it["line_total_vnd"])
				if line <= 0 {
					unit := int64FromAny(it["effective_unit_price_vnd"])
					if unit <= 0 {
						unit = int64FromAny(it["unit_price_vnd"])
					}
					line = unit * int64(qty)
				}
				itemsRev += line
				if pid != "" {
					agg, exists := byProd[pid]
					if !exists {
						agg = &productAgg{productID: pid, productName: pname}
						byProd[pid] = agg
					}
					if agg.productName == "" && pname != "" {
						agg.productName = pname
					}
					agg.unitsSold += qty
					agg.revenueVND += line
				}
			}
		} else if typedItems, ok := ord["items"].([]map[string]any); ok {
			for _, it := range typedItems {
				pid := strings.TrimSpace(stringFromAny(it["product_id"]))
				pname := strings.TrimSpace(stringFromAny(it["product_name"]))
				if pid == "" {
					pid = pname
				}
				qty := int32FromAny(it["quantity"])
				if qty <= 0 {
					qty = 1
				}
				line := int64FromAny(it["line_total_vnd"])
				if line <= 0 {
					unit := int64FromAny(it["effective_unit_price_vnd"])
					if unit <= 0 {
						unit = int64FromAny(it["unit_price_vnd"])
					}
					line = unit * int64(qty)
				}
				itemsRev += line
				if pid != "" {
					agg, exists := byProd[pid]
					if !exists {
						agg = &productAgg{productID: pid, productName: pname}
						byProd[pid] = agg
					}
					if agg.productName == "" && pname != "" {
						agg.productName = pname
					}
					agg.unitsSold += qty
					agg.revenueVND += line
				}
			}
		}

		if orderRev <= 0 {
			orderRev = itemsRev
		}
		m.grossRevenueVND += orderRev
	}

	if m.totalOrders > 0 {
		m.cancelRatePct = math.Round((float64(m.cancelledOrders)/float64(m.totalOrders))*10000) / 100
	}

	m.availableBalanceVND = int64FromAny(balance["available_vnd"])
	if m.availableBalanceVND == 0 {
		m.availableBalanceVND = int64FromAny(balance["available_balance_vnd"])
	}
	m.pendingBalanceVND = int64FromAny(balance["pending_vnd"])
	if m.pendingBalanceVND == 0 {
		m.pendingBalanceVND = int64FromAny(balance["pending_balance_vnd"])
	}

	ranked := make([]*productAgg, 0, len(byProd))
	for _, agg := range byProd {
		ranked = append(ranked, agg)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].revenueVND != ranked[j].revenueVND {
			return ranked[i].revenueVND > ranked[j].revenueVND
		}
		if ranked[i].unitsSold != ranked[j].unitsSold {
			return ranked[i].unitsSold > ranked[j].unitsSold
		}
		return ranked[i].productID < ranked[j].productID
	})
	if len(ranked) > 5 {
		ranked = ranked[:5]
	}

	m.topProducts = make([]map[string]any, 0, len(ranked))
	for _, agg := range ranked {
		m.topProducts = append(m.topProducts, map[string]any{
			"product_id":   agg.productID,
			"product_name": agg.productName,
			"units_sold":   agg.unitsSold,
			"revenue_vnd":  agg.revenueVND,
		})
	}
	return m
}

func mapOrderError(toolName string, err error) Result {
	var statusErr *resilience.HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusUnauthorized:
			return Result{Name: toolName, OK: false, RequiresAuth: true, Private: true, Error: "phiên đăng nhập đã hết hạn, vui lòng đăng nhập lại"}
		case http.StatusForbidden, http.StatusNotFound:
			return Result{Name: toolName, OK: false, Private: true, Error: "không tìm thấy đơn hàng hoặc bạn không có quyền truy cập đơn hàng này"}
		}
	}
	return Result{Name: toolName, OK: false, Private: true, Error: "không thể tra cứu đơn hàng lúc này, vui lòng thử lại sau"}
}

func mapSellerReportError(err error) Result {
	var statusErr *resilience.HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusUnauthorized:
			return Result{Name: ToolGetSellerReport, OK: false, RequiresAuth: true, Private: true, Error: "phiên đăng nhập đã hết hạn, vui lòng đăng nhập lại"}
		case http.StatusForbidden:
			return Result{Name: ToolGetSellerReport, OK: false, Private: true, Error: "chỉ người bán đã xác thực gian hàng mới có quyền xem báo cáo kinh doanh"}
		}
	}
	return Result{Name: ToolGetSellerReport, OK: false, Private: true, Error: "không thể tải số liệu kinh doanh của gian hàng lúc này"}
}

func extractConversationID(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if id := strings.TrimSpace(stringFromAny(resp["id"])); id != "" {
		return id
	}
	if id := strings.TrimSpace(stringFromAny(resp["conversation_id"])); id != "" {
		return id
	}
	if conv, ok := resp["conversation"].(map[string]any); ok {
		return strings.TrimSpace(stringFromAny(conv["id"]))
	}
	return ""
}

func stringArg(args map[string]any, key string) string {
	return strings.TrimSpace(stringFromAny(args[key]))
}

func boolArg(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

func int64Arg(args map[string]any, key string, fallback int64) int64 {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	return int64FromAny(v)
}

func int32Arg(args map[string]any, key string, fallback int32) int32 {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	return int32FromAny(v)
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

func int64FromAny(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		return int64(t)
	case float32:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		if f, err := t.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func int32FromAny(v any) int32 {
	return int32(int64FromAny(v))
}

// normalizeOrderStatusFilter maps legacy phase-0b return order statuses onto the canonical main order state machine.
func normalizeOrderStatusFilter(raw string) string {
	st := strings.ToUpper(strings.TrimSpace(raw))
	switch st {
	case "RETURN_APPROVED", "RETURN_REJECTED", "RETURNING":
		return "RETURN_REQUESTED"
	case "REFUNDED":
		return "RETURNED"
	default:
		return st
	}
}
