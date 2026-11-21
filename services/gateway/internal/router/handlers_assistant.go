package router

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	assistantv1 "github.com/nexusmart/core/gen/go/nexusmart/assistant/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultAssistantGRPCAddr = "assistant:50061"
	defaultAssistantHTTPURL  = "http://assistant:8091"
	defaultAssistantTimeout  = 30 * time.Second
)

type assistantUpstreamOverride struct {
	client assistantv1.AssistantServiceClient
	sseURL string
}

var (
	assistantTestMu       sync.RWMutex
	assistantTestUpstream *assistantUpstreamOverride
)

// setAssistantTestUpstream installs a test AssistantServiceClient and/or SSE upstream URL
// for subsequent router.New calls, returning a restore function. Only intended for unit tests.
func setAssistantTestUpstream(client assistantv1.AssistantServiceClient, sseURL string) func() {
	assistantTestMu.Lock()
	prev := assistantTestUpstream
	assistantTestUpstream = &assistantUpstreamOverride{
		client: client,
		sseURL: sseURL,
	}
	assistantTestMu.Unlock()
	return func() {
		assistantTestMu.Lock()
		assistantTestUpstream = prev
		assistantTestMu.Unlock()
	}
}

type assistantHandler struct {
	serviceName    string
	timeout        time.Duration
	trustedProxies *middleware.TrustedProxies
	log            *slog.Logger

	overrideClient assistantv1.AssistantServiceClient
	overrideSSEURL string

	grpcOnce   sync.Once
	grpcClient assistantv1.AssistantServiceClient
	grpcErr    error

	sseOnce  sync.Once
	sseProxy http.Handler
	sseErr   error
}

type assistantUsageDTO struct {
	InputTokens  int32 `json:"input_tokens"`
	OutputTokens int32 `json:"output_tokens"`
	TotalTokens  int32 `json:"total_tokens"`
	CostVND      int64 `json:"cost_vnd"`
}

// mountAssistantRoutes attaches self-contained AI Assistant SSE and REST routes onto r.
// Explicit paths on r.Group are used rather than r.Route("/api/v1", ...) so existing API
// mounts in router.go are never shadowed.
func mountAssistantRoutes(r chi.Router, deps Dependencies) {
	serviceName := deps.ServiceName
	if serviceName == "" {
		serviceName = "gateway"
	}

	timeout := defaultAssistantTimeout
	if deps.Clients != nil && deps.Clients.Timeout > timeout {
		timeout = deps.Clients.Timeout
	}

	ah := &assistantHandler{
		serviceName:    serviceName,
		timeout:        timeout,
		trustedProxies: deps.TrustedProxies,
		log:            deps.Logger,
	}

	assistantTestMu.RLock()
	if assistantTestUpstream != nil {
		ah.overrideClient = assistantTestUpstream.client
		ah.overrideSSEURL = assistantTestUpstream.sseURL
	}
	assistantTestMu.RUnlock()

	// SSE chat route preserves http.Flusher, clears write deadline, and forwards end-user JWT when present.
	r.Group(func(sse chi.Router) {
		sse.Use(middleware.FlusherTraceMiddleware(serviceName))
		sse.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			sse.Use(deps.RED.HTTPMiddleware())
		}
		sse.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		sse.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))
		sse.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
		sse.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

		sse.Post("/api/v1/assistant/chat", ah.handleAssistantChatSSE)
	})

	// Public / optional-auth product review summary route.
	r.Group(func(pub chi.Router) {
		pub.Use(telemetry.HTTPMiddleware(serviceName))
		pub.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			pub.Use(deps.RED.HTTPMiddleware())
		}
		pub.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		pub.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))
		pub.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))

		pub.Get("/api/v1/products/{id}/review-summary", ah.handleGetReviewSummary)
	})

	// Authenticated buyer & seller assistant routes.
	r.Group(func(authed chi.Router) {
		authed.Use(telemetry.HTTPMiddleware(serviceName))
		authed.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			authed.Use(deps.RED.HTTPMiddleware())
		}
		authed.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		authed.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))
		authed.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, true))
		authed.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

		authed.Post("/api/v1/assistant/handoff", ah.handleAssistantHandoff)
		authed.Post("/api/v1/assistant/feedback", ah.handleAssistantFeedback)

		authed.Group(func(seller chi.Router) {
			seller.Use(middleware.RequireRoles("seller"))
			seller.Use(middleware.RequireSellerShop())

			seller.Post("/api/v1/seller/assistant/report", ah.handleSellerAssistantReport)
			seller.Post("/api/v1/seller/assistant/describe-product", ah.handleSellerDescribeProduct)
			seller.Post("/api/v1/seller/assistant/suggest-reply", ah.handleSellerSuggestReply)
		})
	})
}

func (h *assistantHandler) client() (assistantv1.AssistantServiceClient, error) {
	if h.overrideClient != nil {
		return h.overrideClient, nil
	}
	h.grpcOnce.Do(func() {
		addr := env.EnvString("ASSISTANT_GRPC_ADDR", defaultAssistantGRPCAddr)
		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(h.serviceName)),
		)
		if err != nil {
			h.grpcErr = fmt.Errorf("init assistant grpc client (%s): %w", addr, err)
			return
		}
		h.grpcClient = assistantv1.NewAssistantServiceClient(conn)
	})
	return h.grpcClient, h.grpcErr
}

func (h *assistantHandler) outgoingCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := h.timeout
	if timeout <= 0 {
		timeout = defaultAssistantTimeout
	}
	outCtx, cancel := context.WithTimeout(ctx, timeout)
	outCtx = auth.InjectOutgoingGRPC(outCtx, auth.RawTokenFromContext(ctx))
	return outCtx, cancel
}

func (h *assistantHandler) sseReverseProxy() (http.Handler, error) {
	h.sseOnce.Do(func() {
		rawURL := strings.TrimSpace(h.overrideSSEURL)
		if rawURL == "" {
			rawURL = env.EnvString("ASSISTANT_HTTP_URL", defaultAssistantHTTPURL)
		}
		target, err := url.Parse(rawURL)
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			h.sseErr = fmt.Errorf("invalid assistant HTTP upstream url %q", rawURL)
			return
		}

		rp := &httputil.ReverseProxy{
			FlushInterval: -1,
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = "/assistant/chat"
				pr.Out.URL.RawPath = ""
				pr.Out.URL.RawQuery = pr.In.URL.RawQuery
				pr.Out.Host = target.Host
				pr.SetXForwarded()
				if clientIP := h.trustedProxies.ClientIP(pr.In); clientIP != "" && clientIP != "unknown" {
					pr.Out.Header.Set("X-Forwarded-For", clientIP)
				} else {
					pr.Out.Header.Del("X-Forwarded-For")
				}
				pr.Out.Header.Del("X-Real-IP")

				// Identity travels ONLY inside the verified end-user Bearer JWT.
				pr.Out.Header.Del("X-User-Id")
				pr.Out.Header.Del("X-User-Role")
				pr.Out.Header.Del("X-Shop-Id")
				if rawAuth := auth.RawTokenFromContext(pr.In.Context()); rawAuth != "" {
					pr.Out.Header.Set("Authorization", rawAuth)
				} else {
					pr.Out.Header.Del("Authorization")
				}
				otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
			},
			ModifyResponse: func(resp *http.Response) error {
				resp.Header.Set("X-Accel-Buffering", "no")
				if resp.Header.Get("Cache-Control") == "" {
					resp.Header.Set("Cache-Control", "no-cache")
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
				if h.log != nil {
					h.log.ErrorContext(r.Context(), "assistant SSE proxy error",
						slog.String("path", r.URL.Path),
						slog.String("error", proxyErr.Error()),
					)
				}
				httpx.WriteError(w, r, http.StatusBadGateway, "UPSTREAM_ERROR", "assistant stream unavailable")
			},
		}

		h.sseProxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clearAssistantWriteDeadline(w)
			rp.ServeHTTP(w, r)
		})
	})
	return h.sseProxy, h.sseErr
}

func clearAssistantWriteDeadline(w http.ResponseWriter) {
	type unwrapper interface {
		Unwrap() http.ResponseWriter
	}
	type writeDeadliner interface {
		SetWriteDeadline(time.Time) error
	}
	cur := w
	for cur != nil {
		if wd, ok := cur.(writeDeadliner); ok {
			_ = wd.SetWriteDeadline(time.Time{})
			return
		}
		if u, ok := cur.(unwrapper); ok {
			cur = u.Unwrap()
			continue
		}
		return
	}
}

func (h *assistantHandler) handleAssistantChatSSE(w http.ResponseWriter, r *http.Request) {
	p, err := h.sseReverseProxy()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant SSE upstream not configured")
		return
	}
	p.ServeHTTP(w, r)
}

func (h *assistantHandler) handleAssistantHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ShopID         string `json:"shop_id"`
		ConversationID string `json:"conversation_id"`
		Reason         string `json:"reason"`
		Messages       []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	c, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant service unavailable")
		return
	}

	msgs := make([]*assistantv1.ChatMessage, 0, len(body.Messages))
	for _, m := range body.Messages {
		msgs = append(msgs, &assistantv1.ChatMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	ctx, cancel := h.outgoingCtx(r.Context())
	defer cancel()

	resp, err := c.Handoff(ctx, &assistantv1.HandoffRequest{
		ShopId:         strings.TrimSpace(body.ShopID),
		ConversationId: strings.TrimSpace(body.ConversationID),
		Reason:         strings.TrimSpace(body.Reason),
		Messages:       msgs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"ticket_id":             resp.GetTicketId(),
		"conversation_id":       resp.GetConversationId(),
		"shop_id":               resp.GetShopId(),
		"summary":               resp.GetSummary(),
		"status":                resp.GetStatus(),
		"within_business_hours": resp.GetWithinBusinessHours(),
		"estimated_response":    resp.GetEstimatedResponse(),
	})
}

func (h *assistantHandler) handleAssistantFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversation_id"`
		MessageID      string `json:"message_id"`
		Helpful        bool   `json:"helpful"`
		Comment        string `json:"comment"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	c, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant service unavailable")
		return
	}

	ctx, cancel := h.outgoingCtx(r.Context())
	defer cancel()

	resp, err := c.SubmitFeedback(ctx, &assistantv1.SubmitFeedbackRequest{
		ConversationId: strings.TrimSpace(body.ConversationID),
		MessageId:      strings.TrimSpace(body.MessageID),
		Helpful:        body.Helpful,
		Comment:        strings.TrimSpace(body.Comment),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *assistantHandler) handleSellerAssistantReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSellerShopID(w, r); !ok {
		return
	}
	var body struct {
		Question string `json:"question"`
		Period   string `json:"period"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	c, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant service unavailable")
		return
	}

	ctx, cancel := h.outgoingCtx(r.Context())
	defer cancel()

	resp, err := c.SellerReport(ctx, &assistantv1.SellerReportRequest{
		Question: strings.TrimSpace(body.Question),
		Period:   strings.TrimSpace(body.Period),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	topProducts := make([]map[string]any, 0, len(resp.GetMetrics().GetTopProducts()))
	for _, tp := range resp.GetMetrics().GetTopProducts() {
		topProducts = append(topProducts, map[string]any{
			"product_id":   tp.GetProductId(),
			"product_name": tp.GetProductName(),
			"units_sold":   tp.GetUnitsSold(),
			"revenue_vnd":  tp.GetRevenueVnd(),
		})
	}

	m := resp.GetMetrics()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shop_id": resp.GetShopId(),
		"period":  resp.GetPeriod(),
		"answer":  resp.GetAnswer(),
		"metrics": map[string]any{
			"total_orders":          m.GetTotalOrders(),
			"completed_orders":      m.GetCompletedOrders(),
			"cancelled_orders":      m.GetCancelledOrders(),
			"cancel_rate_pct":       m.GetCancelRatePct(),
			"gross_revenue_vnd":     m.GetGrossRevenueVnd(),
			"available_balance_vnd": m.GetAvailableBalanceVnd(),
			"pending_balance_vnd":   m.GetPendingBalanceVnd(),
			"top_products":          topProducts,
		},
		"usage": mapAssistantUsage(resp.GetUsage()),
	})
}

func (h *assistantHandler) handleSellerDescribeProduct(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSellerShopID(w, r); !ok {
		return
	}
	var body struct {
		Name       string            `json:"name"`
		Category   string            `json:"category"`
		Attributes map[string]string `json:"attributes"`
		PriceVND   int64             `json:"price_vnd"`
		Tone       string            `json:"tone"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	c, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant service unavailable")
		return
	}

	ctx, cancel := h.outgoingCtx(r.Context())
	defer cancel()

	resp, err := c.DescribeProduct(ctx, &assistantv1.DescribeProductRequest{
		Name:       strings.TrimSpace(body.Name),
		Category:   strings.TrimSpace(body.Category),
		Attributes: body.Attributes,
		PriceVnd:   body.PriceVND,
		Tone:       strings.TrimSpace(body.Tone),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	highlights := resp.GetHighlights()
	if highlights == nil {
		highlights = []string{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"title_suggestion": resp.GetTitleSuggestion(),
		"description":      resp.GetDescription(),
		"highlights":       highlights,
		"ai_generated":     resp.GetAiGenerated(),
		"usage":            mapAssistantUsage(resp.GetUsage()),
	})
}

func (h *assistantHandler) handleSellerSuggestReply(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireSellerShopID(w, r); !ok {
		return
	}
	var body struct {
		ConversationID  string `json:"conversation_id"`
		ProductID       string `json:"product_id"`
		CustomerMessage string `json:"customer_message"`
		History         []struct {
			Sender string `json:"sender"`
			Body   string `json:"body"`
		} `json:"history"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	c, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant service unavailable")
		return
	}

	history := make([]*assistantv1.ChatHistoryMessage, 0, len(body.History))
	for _, msg := range body.History {
		history = append(history, &assistantv1.ChatHistoryMessage{
			Sender: msg.Sender,
			Body:   msg.Body,
		})
	}

	ctx, cancel := h.outgoingCtx(r.Context())
	defer cancel()

	resp, err := c.SuggestReply(ctx, &assistantv1.SuggestReplyRequest{
		ConversationId:  strings.TrimSpace(body.ConversationID),
		ProductId:       strings.TrimSpace(body.ProductID),
		CustomerMessage: strings.TrimSpace(body.CustomerMessage),
		History:         history,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	suggestions := make([]map[string]any, 0, len(resp.GetSuggestions()))
	for _, s := range resp.GetSuggestions() {
		suggestions = append(suggestions, map[string]any{
			"id":   s.GetId(),
			"text": s.GetText(),
			"tone": s.GetTone(),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"suggestions":        suggestions,
		"needs_more_info":    resp.GetNeedsMoreInfo(),
		"injection_detected": resp.GetInjectionDetected(),
		"ai_generated":       resp.GetAiGenerated(),
		"usage":              mapAssistantUsage(resp.GetUsage()),
	})
}

func (h *assistantHandler) handleGetReviewSummary(w http.ResponseWriter, r *http.Request) {
	productID := strings.TrimSpace(chi.URLParam(r, "id"))
	if productID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "product id is required")
		return
	}

	c, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "assistant service unavailable")
		return
	}

	ctx, cancel := h.outgoingCtx(r.Context())
	defer cancel()

	resp, err := c.GetReviewSummary(ctx, &assistantv1.GetReviewSummaryRequest{
		ProductId: productID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	pros := resp.GetPros()
	if pros == nil {
		pros = []string{}
	}
	cons := resp.GetCons()
	if cons == nil {
		cons = []string{}
	}

	out := map[string]any{
		"product_id":           resp.GetProductId(),
		"available":            resp.GetAvailable(),
		"review_count":         resp.GetReviewCount(),
		"min_reviews_required": resp.GetMinReviewsRequired(),
		"average_rating":       resp.GetAverageRating(),
		"summary":              resp.GetSummary(),
		"pros":                 pros,
		"cons":                 cons,
		"ai_generated":         resp.GetAiGenerated(),
	}
	if ts := httpx.FormatTimestamp(resp.GetUpdatedAt()); ts != "" {
		out["updated_at"] = ts
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func mapAssistantUsage(u *assistantv1.UsageMetrics) assistantUsageDTO {
	if u == nil {
		return assistantUsageDTO{}
	}
	return assistantUsageDTO{
		InputTokens:  u.GetInputTokens(),
		OutputTokens: u.GetOutputTokens(),
		TotalTokens:  u.GetTotalTokens(),
		CostVND:      u.GetCostVnd(),
	}
}
