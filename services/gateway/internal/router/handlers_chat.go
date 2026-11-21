package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	defaultChatGRPCAddr = "chat:50060"
	defaultChatHTTPURL  = "http://chat:8090"
	defaultChatTimeout  = 10 * time.Second

	chatWSAuthMethodHeader = "X-Chat-Auth-Method"
)

type chatWSAuthCookieKey struct{}

func withChatWSCookieAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, chatWSAuthCookieKey{}, true)
}

func isChatWSCookieAuth(ctx context.Context) bool {
	v, _ := ctx.Value(chatWSAuthCookieKey{}).(bool)
	return v
}

type chatUpstreamOverride struct {
	client chatv1.ChatServiceClient
	wsURL  string
}

var (
	chatTestMu       sync.RWMutex
	chatTestUpstream *chatUpstreamOverride
)

// setChatTestUpstream installs a test ChatServiceClient and/or WS upstream URL for subsequent
// router.New calls, returning a restore function. Only intended for unit tests.
func setChatTestUpstream(client chatv1.ChatServiceClient, wsURL string) func() {
	chatTestMu.Lock()
	prev := chatTestUpstream
	chatTestUpstream = &chatUpstreamOverride{
		client: client,
		wsURL:  wsURL,
	}
	chatTestMu.Unlock()
	return func() {
		chatTestMu.Lock()
		chatTestUpstream = prev
		chatTestMu.Unlock()
	}
}

type chatHandler struct {
	serviceName string
	timeout     time.Duration
	log         *slog.Logger

	overrideClient chatv1.ChatServiceClient
	overrideWSURL  string

	grpcOnce   sync.Once
	grpcClient chatv1.ChatServiceClient
	grpcErr    error

	wsOnce  sync.Once
	wsProxy http.Handler
	wsErr   error
}

type chatConversationDTO struct {
	ID                    string `json:"id"`
	BuyerID               string `json:"buyer_id"`
	ShopID                string `json:"shop_id"`
	SiteHost              string `json:"site_host"`
	LastMessageID         string `json:"last_message_id"`
	LastMessageSeq        int64  `json:"last_message_seq"`
	LastMessagePreview    string `json:"last_message_preview"`
	LastMessageSenderRole string `json:"last_message_sender_role"`
	LastMessageAt         string `json:"last_message_at,omitempty"`
	BuyerLastReadSeq      int64  `json:"buyer_last_read_seq"`
	SellerLastReadSeq     int64  `json:"seller_last_read_seq"`
	UnreadCount           int64  `json:"unread_count"`
	CreatedAt             string `json:"created_at,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
}

type chatMessageDTO struct {
	ID             string          `json:"id"`
	Seq            int64           `json:"seq"`
	ConversationID string          `json:"conversation_id"`
	SenderID       string          `json:"sender_id"`
	SenderRole     string          `json:"sender_role"`
	Kind           string          `json:"kind"`
	Body           string          `json:"body"`
	RefID          string          `json:"ref_id"`
	Metadata       json.RawMessage `json:"metadata"`
	IsAutoReply    bool            `json:"is_auto_reply"`
	ClientMsgID    string          `json:"client_msg_id"`
	CreatedAt      string          `json:"created_at,omitempty"`
}

type chatKeywordRuleDTO struct {
	Keywords []string `json:"keywords"`
	Reply    string   `json:"reply"`
}

type chatAutoReplyConfigDTO struct {
	ShopID          string                `json:"shop_id"`
	Enabled         bool                  `json:"enabled"`
	GreetingMessage string                `json:"greeting_message"`
	OfflineEnabled  bool                  `json:"offline_enabled"`
	OfflineMessage  string                `json:"offline_message"`
	WorkStartHour   int32                 `json:"work_start_hour"`
	WorkEndHour     int32                 `json:"work_end_hour"`
	Timezone        string                `json:"timezone"`
	CooldownMinutes int32                 `json:"cooldown_minutes"`
	KeywordRules    []*chatKeywordRuleDTO `json:"keyword_rules"`
	UpdatedAt       string                `json:"updated_at,omitempty"`
}

// mountChatRoutes attaches self-contained Chat REST endpoints and WebSocket proxy onto r.
// Explicit paths on r.Group are used rather than r.Route("/api/v1", ...) so existing API mounts
// in router.go are never shadowed.
func mountChatRoutes(r chi.Router, deps Dependencies) {
	serviceName := deps.ServiceName
	if serviceName == "" {
		serviceName = "gateway"
	}

	timeout := defaultChatTimeout
	if deps.Clients != nil && deps.Clients.Timeout > 0 {
		timeout = deps.Clients.Timeout
	}

	ch := &chatHandler{
		serviceName: serviceName,
		timeout:     timeout,
		log:         deps.Logger,
	}

	chatTestMu.RLock()
	if chatTestUpstream != nil {
		ch.overrideClient = chatTestUpstream.client
		ch.overrideWSURL = chatTestUpstream.wsURL
	}
	chatTestMu.RUnlock()

	// WebSocket endpoint mounted in a hijacker- and flusher-preserving group.
	r.Group(func(ws chi.Router) {
		ws.Use(middleware.FlusherTraceMiddleware(serviceName))
		ws.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			ws.Use(deps.RED.HTTPMiddleware())
		}
		ws.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		ws.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))
		ws.Use(ch.chatWSAuthMiddleware(deps))
		ws.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

		ws.Get("/api/v1/chat/ws", ch.handleChatWS)
	})

	// Chat REST endpoints share standard OTel trace, RED metrics, logging, rate-limiting, and JWT auth.
	r.Group(func(rest chi.Router) {
		rest.Use(telemetry.HTTPMiddleware(serviceName))
		rest.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			rest.Use(deps.RED.HTTPMiddleware())
		}
		rest.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		rest.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))
		rest.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, true))
		rest.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

		rest.Get("/api/v1/chat/conversations", ch.handleListConversations)
		rest.Post("/api/v1/chat/conversations", ch.handleCreateOrGetConversation)
		rest.Get("/api/v1/chat/conversations/{id}/messages", ch.handleListMessages)
		rest.Post("/api/v1/chat/conversations/{id}/messages", ch.handleSendMessage)
		rest.Post("/api/v1/chat/conversations/{id}/read", ch.handleMarkRead)
		rest.Post("/api/v1/chat/ws-ticket", ch.handleCreateWSTicket)

		rest.Group(func(seller chi.Router) {
			seller.Use(middleware.RequireRoles("seller"))
			seller.Use(middleware.RequireSellerShop())

			seller.Get("/api/v1/seller/chat/conversations", ch.handleSellerListConversations)
			seller.Get("/api/v1/seller/chat/auto-reply", ch.handleSellerGetAutoReply)
			seller.Put("/api/v1/seller/chat/auto-reply", ch.handleSellerPutAutoReply)
		})
	})
}

func (h *chatHandler) client() (chatv1.ChatServiceClient, error) {
	if h.overrideClient != nil {
		return h.overrideClient, nil
	}
	h.grpcOnce.Do(func() {
		addr := env.EnvString("CHAT_GRPC_ADDR", defaultChatGRPCAddr)
		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(h.serviceName)),
		)
		if err != nil {
			h.grpcErr = fmt.Errorf("init chat grpc client (%s): %w", addr, err)
			return
		}
		h.grpcClient = chatv1.NewChatServiceClient(conn)
	})
	return h.grpcClient, h.grpcErr
}

func (h *chatHandler) wsReverseProxy() (http.Handler, error) {
	h.wsOnce.Do(func() {
		rawURL := strings.TrimSpace(h.overrideWSURL)
		if rawURL == "" {
			rawURL = env.EnvString("CHAT_HTTP_URL", defaultChatHTTPURL)
		}
		target, err := url.Parse(rawURL)
		if err != nil || target.Scheme == "" || target.Host == "" {
			h.wsErr = fmt.Errorf("invalid chat WS upstream url %q: %w", rawURL, err)
			return
		}

		var transport http.RoundTripper
		if baseTransport, ok := http.DefaultTransport.(*http.Transport); ok && baseTransport != nil {
			tr := baseTransport.Clone()
			tr.ForceAttemptHTTP2 = false
			tr.ResponseHeaderTimeout = 15 * time.Second
			transport = tr
		}

		rp := &httputil.ReverseProxy{
			Transport:     transport,
			FlushInterval: -1,
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = "/ws"
				pr.Out.URL.RawPath = ""

				// Preserve caller query parameters (e.g. after, conversation_id) while stripping
				// the single-use ticket so it is never forwarded or leaked upstream.
				q := pr.In.URL.Query()
				q.Del("ticket")
				pr.Out.URL.RawQuery = q.Encode()

				pr.Out.Host = target.Host
				pr.SetXForwarded()

				if rawAuth := auth.RawTokenFromContext(pr.In.Context()); rawAuth != "" {
					pr.Out.Header.Set("Authorization", ensureBearerPrefix(rawAuth))
				}
				// Strip Cookie after injecting the gateway-verified Bearer token so upstream
				// always authenticates strictly from Authorization and never a stale cookie.
				pr.Out.Header.Del("Cookie")
				// Signal cookie-authenticated upgrades to upstream chat WS handler so it enforces
				// strict Origin validation on cookie auth while allowing custom domains on ticket auth.
				if isChatWSCookieAuth(pr.In.Context()) {
					pr.Out.Header.Set(chatWSAuthMethodHeader, "cookie")
				} else {
					pr.Out.Header.Del(chatWSAuthMethodHeader)
				}
				if tp := pr.In.Header.Get("traceparent"); tp != "" {
					pr.Out.Header.Set("traceparent", tp)
				}
				otel.GetTextMapPropagator().Inject(pr.In.Context(), propagation.HeaderCarrier(pr.Out.Header))
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
				if h.log != nil {
					h.log.ErrorContext(r.Context(), "chat ws proxy error",
						slog.String("path", r.URL.Path),
						slog.String("error", proxyErr.Error()),
					)
				}
				httpx.WriteError(w, r, http.StatusBadGateway, "UPSTREAM_ERROR", "chat websocket upstream unavailable")
			},
		}
		h.wsProxy = rp
	})
	return h.wsProxy, h.wsErr
}

func (h *chatHandler) outgoingContext(ctx context.Context, extraPairs ...string) (context.Context, context.CancelFunc) {
	timeout := h.timeout
	if timeout <= 0 {
		timeout = defaultChatTimeout
	}
	outCtx, cancel := context.WithTimeout(ctx, timeout)
	outCtx = auth.InjectOutgoingGRPC(outCtx, auth.RawTokenFromContext(ctx))
	if len(extraPairs) >= 2 {
		outCtx = metadata.AppendToOutgoingContext(outCtx, extraPairs...)
	}
	return outCtx, cancel
}

// canAccessChatConversation strictly enforces object-level chat access:
// only the buyer in the conversation or staff/seller belonging to the conversation's shop.
func canAccessChatConversation(claims *auth.Claims, conv *chatv1.Conversation) bool {
	if claims == nil || conv == nil {
		return false
	}
	userID := strings.TrimSpace(claims.UserID())
	buyerID := strings.TrimSpace(conv.GetBuyerId())
	if userID != "" && buyerID != "" && userID == buyerID {
		return true
	}
	shopID := strings.TrimSpace(claims.ShopID)
	convShopID := strings.TrimSpace(conv.GetShopId())
	if shopID != "" && convShopID != "" && shopID == convShopID {
		return true
	}
	return false
}

func (h *chatHandler) chatWSAuthMiddleware(deps Dependencies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if deps.AuthManager == nil {
				httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "authentication service unavailable")
				return
			}

			// 1. Authorization: Bearer <jwt> header.
			if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
				claims, ok := h.verifyTokenAndRevocation(w, r, deps, authHeader)
				if !ok {
					return
				}
				ctx := auth.WithClaims(r.Context(), claims)
				ctx = auth.WithRawToken(ctx, ensureBearerPrefix(authHeader))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2. One-time query ticket (?ticket=<t>) exchanged via ChatService.ExchangeWSTicket.
			// Checked before Cookie so explicit tickets from custom shop domains are never
			// overshadowed by an ambient host cookie.
			if ticket := strings.TrimSpace(r.URL.Query().Get("ticket")); ticket != "" {
				cli, err := h.client()
				if err != nil {
					if h.log != nil {
						h.log.ErrorContext(r.Context(), "chat grpc client unavailable", slog.String("error", err.Error()))
					}
					httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
					return
				}
				outCtx, cancel := h.outgoingContext(r.Context())
				resp, err := cli.ExchangeWSTicket(outCtx, &chatv1.ExchangeWSTicketRequest{Ticket: ticket})
				cancel()
				if err != nil {
					h.writeWSTicketError(w, r, err)
					return
				}

				ticketJTI := strings.TrimSpace(resp.GetJti())
				if !h.checkJTIRevocation(w, r, deps.RevocationChecker, ticketJTI) {
					return
				}

				wsToken := strings.TrimSpace(resp.GetWsToken())
				claims, err := deps.AuthManager.VerifyAccessToken(wsToken)
				if err != nil {
					httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired websocket token")
					return
				}
				if claims.JTI() != "" && claims.JTI() != ticketJTI {
					if !h.checkJTIRevocation(w, r, deps.RevocationChecker, claims.JTI()) {
						return
					}
				}

				ctx := auth.WithClaims(r.Context(), claims)
				ctx = auth.WithRawToken(ctx, ensureBearerPrefix(wsToken))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 3. BFF cookie (__Host-nm_at or nm_at); marked for strict upstream Origin validation.
			if cookieTok := extractChatCookieToken(r); cookieTok != "" {
				claims, ok := h.verifyTokenAndRevocation(w, r, deps, cookieTok)
				if !ok {
					return
				}
				ctx := auth.WithClaims(r.Context(), claims)
				ctx = auth.WithRawToken(ctx, ensureBearerPrefix(cookieTok))
				ctx = withChatWSCookieAuth(ctx)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "missing authentication credentials")
		})
	}
}

func (h *chatHandler) verifyTokenAndRevocation(
	w http.ResponseWriter,
	r *http.Request,
	deps Dependencies,
	rawToken string,
) (*auth.Claims, bool) {
	claims, err := deps.AuthManager.VerifyAccessToken(rawToken)
	if err != nil {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired access token")
		return nil, false
	}
	if !h.checkJTIRevocation(w, r, deps.RevocationChecker, claims.JTI()) {
		return nil, false
	}
	return claims, true
}

func (h *chatHandler) checkJTIRevocation(
	w http.ResponseWriter,
	r *http.Request,
	revoker middleware.JTIRevocationChecker,
	jti string,
) bool {
	if revoker == nil || strings.TrimSpace(jti) == "" {
		return true
	}
	revoked, err := revoker.IsJTIRevoked(r.Context(), jti)
	if err != nil {
		if h.log != nil {
			h.log.ErrorContext(r.Context(), "jti revocation check failed", slog.String("error", err.Error()))
		}
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "authentication service unavailable")
		return false
	}
	if revoked {
		httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "access token has been revoked")
		return false
	}
	return true
}

func (h *chatHandler) writeWSTicketError(w http.ResponseWriter, r *http.Request, err error) {
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded:
			httpx.WriteGRPCError(w, r, err)
			return
		}
	}
	httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired websocket ticket")
}

func (h *chatHandler) handleChatWS(w http.ResponseWriter, r *http.Request) {
	wsProxy, err := h.wsReverseProxy()
	if err != nil {
		if h.log != nil {
			h.log.ErrorContext(r.Context(), "chat ws proxy unavailable", slog.String("error", err.Error()))
		}
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat websocket proxy unavailable")
		return
	}

	// Clear both read and write deadlines before WebSocket upgrade so long-lived connections never time out.
	rc := http.NewResponseController(w) //nolint:bodyclose // ResponseController wraps ResponseWriter, not an http.Response body.
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	wsProxy.ServeHTTP(w, r)
}

func (h *chatHandler) handleListConversations(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	q := r.URL.Query()
	scope := strings.TrimSpace(q.Get("scope"))
	reqShopID := strings.TrimSpace(q.Get("shop_id"))
	if strings.EqualFold(scope, "seller") {
		callerShopID := ""
		if claims != nil {
			callerShopID = strings.TrimSpace(claims.ShopID)
		}
		if callerShopID == "" || (reqShopID != "" && reqShopID != callerShopID) {
			httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot list seller conversations for this shop")
			return
		}
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}
	page, pageSize := httpx.ParsePagination(r)

	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	resp, err := cli.ListConversations(ctx, &chatv1.ListConversationsRequest{
		Scope:         scope,
		ShopId:        reqShopID,
		UnrepliedOnly: httpx.ParseBoolQuery(r, "unreplied_only"),
		Page:          page,
		PageSize:      pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*chatConversationDTO, 0, len(resp.GetConversations()))
	for _, c := range resp.GetConversations() {
		if !canAccessChatConversation(claims, c) {
			continue
		}
		items = append(items, mapChatConversation(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":        items,
		"total":        resp.GetTotal(),
		"total_unread": resp.GetTotalUnread(),
		"page":         page,
		"page_size":    pageSize,
	})
}

func (h *chatHandler) handleCreateOrGetConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ShopID         string `json:"shop_id"`
		SiteHost       string `json:"site_host"`
		InitialMessage string `json:"initial_message"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	shopID := strings.TrimSpace(body.ShopID)
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_id is required")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	resp, err := cli.CreateOrGetConversation(ctx, &chatv1.CreateOrGetConversationRequest{
		ShopId:         shopID,
		SiteHost:       strings.TrimSpace(body.SiteHost),
		InitialMessage: body.InitialMessage,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	statusCode := http.StatusOK
	if resp.GetCreated() {
		statusCode = http.StatusCreated
	}
	payload := map[string]any{
		"conversation": mapChatConversation(resp.GetConversation()),
		"created":      resp.GetCreated(),
	}
	if resp.GetInitialMessage() != nil {
		payload["initial_message"] = mapChatMessage(resp.GetInitialMessage())
	}
	httpx.WriteJSON(w, statusCode, payload)
}

func (h *chatHandler) handleListMessages(w http.ResponseWriter, r *http.Request) {
	convID := strings.TrimSpace(chi.URLParam(r, "id"))
	if convID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "conversation id is required")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	q := r.URL.Query()
	limit := httpx.ParseInt32Query(r, "limit", 0)
	if limit < 0 {
		limit = 0
	}
	if limit > 100 {
		limit = 100
	}

	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	resp, err := cli.ListMessages(ctx, &chatv1.ListMessagesRequest{
		ConversationId: convID,
		Before:         strings.TrimSpace(q.Get("before")),
		After:          strings.TrimSpace(q.Get("after")),
		Limit:          limit,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	// Enforce strict object-level access at the gateway before returning any messages.
	claims, _ := auth.ClaimsFromContext(r.Context())
	if !canAccessChatConversation(claims, resp.GetConversation()) {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access messages in this conversation")
		return
	}

	items := make([]*chatMessageDTO, 0, len(resp.GetMessages()))
	for _, m := range resp.GetMessages() {
		items = append(items, mapChatMessage(m))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"conversation":  mapChatConversation(resp.GetConversation()),
		"items":         items,
		"next_before":   resp.GetNextBefore(),
		"latest_cursor": resp.GetLatestCursor(),
		"has_more":      resp.GetHasMore(),
	})
}

func (h *chatHandler) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	convID := strings.TrimSpace(chi.URLParam(r, "id"))
	if convID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "conversation id is required")
		return
	}

	var body struct {
		Kind        string          `json:"kind"`
		Body        string          `json:"body"`
		RefID       string          `json:"ref_id"`
		Metadata    json.RawMessage `json:"metadata"`
		ClientMsgID string          `json:"client_msg_id"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	kind, ok := parseChatMessageKind(body.Kind)
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid message kind; expected text, image, product, or order")
		return
	}

	metadataJSON, err := normalizeChatMetadataJSON(body.Metadata)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "metadata must be a JSON object")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	// Enforce strict object-level access at the gateway before invoking SendMessage.
	getCtx, getCancel := h.outgoingContext(r.Context())
	convResp, err := cli.GetConversation(getCtx, &chatv1.GetConversationRequest{ConversationId: convID})
	getCancel()
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	if !canAccessChatConversation(claims, convResp.GetConversation()) {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot send messages to this conversation")
		return
	}

	sendCtx, sendCancel := h.outgoingContext(r.Context())
	defer sendCancel()

	resp, err := cli.SendMessage(sendCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           kind,
		Body:           body.Body,
		RefId:          strings.TrimSpace(body.RefID),
		MetadataJson:   metadataJSON,
		ClientMsgId:    strings.TrimSpace(body.ClientMsgID),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	payload := map[string]any{
		"message": mapChatMessage(resp.GetMessage()),
	}
	if resp.GetAutoReply() != nil {
		payload["auto_reply"] = mapChatMessage(resp.GetAutoReply())
	}
	httpx.WriteJSON(w, http.StatusCreated, payload)
}

func (h *chatHandler) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	convID := strings.TrimSpace(chi.URLParam(r, "id"))
	if convID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "conversation id is required")
		return
	}

	var body struct {
		LastReadSeq int64  `json:"last_read_seq"`
		MessageID   string `json:"message_id"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	// Enforce strict object-level access at the gateway before invoking MarkRead.
	getCtx, getCancel := h.outgoingContext(r.Context())
	convResp, err := cli.GetConversation(getCtx, &chatv1.GetConversationRequest{ConversationId: convID})
	getCancel()
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	if !canAccessChatConversation(claims, convResp.GetConversation()) {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot mark read on this conversation")
		return
	}

	markCtx, markCancel := h.outgoingContext(r.Context())
	defer markCancel()

	resp, err := cli.MarkRead(markCtx, &chatv1.MarkReadRequest{
		ConversationId: convID,
		LastReadSeq:    body.LastReadSeq,
		MessageId:      strings.TrimSpace(body.MessageID),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"conversation_id": resp.GetConversationId(),
		"last_read_seq":   resp.GetLastReadSeq(),
		"unread_count":    resp.GetUnreadCount(),
	})
}

func (h *chatHandler) handleCreateWSTicket(w http.ResponseWriter, r *http.Request) {
	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	resp, err := cli.CreateWSTicket(ctx, &chatv1.CreateWSTicketRequest{})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"ticket":     resp.GetTicket(),
		"expires_at": httpx.FormatTimestamp(resp.GetExpiresAt()),
	})
}

func (h *chatHandler) handleSellerListConversations(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(auth.ShopIDFromContext(r.Context()))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "seller account has no active shop")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	page, pageSize := httpx.ParsePagination(r)
	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	resp, err := cli.ListConversations(ctx, &chatv1.ListConversationsRequest{
		Scope:         "seller",
		ShopId:        shopID,
		UnrepliedOnly: httpx.ParseBoolQuery(r, "unreplied_only"),
		Page:          page,
		PageSize:      pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	claims, _ := auth.ClaimsFromContext(r.Context())
	items := make([]*chatConversationDTO, 0, len(resp.GetConversations()))
	for _, c := range resp.GetConversations() {
		if !canAccessChatConversation(claims, c) {
			continue
		}
		items = append(items, mapChatConversation(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":        items,
		"total":        resp.GetTotal(),
		"total_unread": resp.GetTotalUnread(),
		"page":         page,
		"page_size":    pageSize,
	})
}

func (h *chatHandler) handleSellerGetAutoReply(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(auth.ShopIDFromContext(r.Context()))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "seller account has no active shop")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	resp, err := cli.GetAutoReplyConfig(ctx, &chatv1.GetAutoReplyConfigRequest{
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"config": mapChatAutoReplyConfig(resp.GetConfig()),
	})
}

func (h *chatHandler) handleSellerPutAutoReply(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(auth.ShopIDFromContext(r.Context()))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "seller account has no active shop")
		return
	}

	var body struct {
		Enabled         bool                 `json:"enabled"`
		GreetingMessage string               `json:"greeting_message"`
		OfflineEnabled  bool                 `json:"offline_enabled"`
		OfflineMessage  string               `json:"offline_message"`
		WorkStartHour   int32                `json:"work_start_hour"`
		WorkEndHour     int32                `json:"work_end_hour"`
		Timezone        string               `json:"timezone"`
		CooldownMinutes int32                `json:"cooldown_minutes"`
		KeywordRules    []chatKeywordRuleDTO `json:"keyword_rules"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	cli, err := h.client()
	if err != nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "chat service unavailable")
		return
	}

	rules := make([]*chatv1.KeywordRule, 0, len(body.KeywordRules))
	for _, rule := range body.KeywordRules {
		rules = append(rules, &chatv1.KeywordRule{
			Keywords: rule.Keywords,
			Reply:    rule.Reply,
		})
	}

	ctx, cancel := h.outgoingContext(r.Context())
	defer cancel()

	// Always bind shop_id from the verified seller JWT claim; never trust a request body shop_id.
	resp, err := cli.UpsertAutoReplyConfig(ctx, &chatv1.UpsertAutoReplyConfigRequest{
		Config: &chatv1.AutoReplyConfig{
			ShopId:          shopID,
			Enabled:         body.Enabled,
			GreetingMessage: body.GreetingMessage,
			OfflineEnabled:  body.OfflineEnabled,
			OfflineMessage:  body.OfflineMessage,
			WorkStartHour:   body.WorkStartHour,
			WorkEndHour:     body.WorkEndHour,
			Timezone:        strings.TrimSpace(body.Timezone),
			CooldownMinutes: body.CooldownMinutes,
			KeywordRules:    rules,
		},
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"config": mapChatAutoReplyConfig(resp.GetConfig()),
	})
}

func mapChatConversation(c *chatv1.Conversation) *chatConversationDTO {
	if c == nil {
		return nil
	}
	return &chatConversationDTO{
		ID:                    c.GetId(),
		BuyerID:               c.GetBuyerId(),
		ShopID:                c.GetShopId(),
		SiteHost:              c.GetSiteHost(),
		LastMessageID:         c.GetLastMessageId(),
		LastMessageSeq:        c.GetLastMessageSeq(),
		LastMessagePreview:    c.GetLastMessagePreview(),
		LastMessageSenderRole: c.GetLastMessageSenderRole(),
		LastMessageAt:         httpx.FormatTimestamp(c.GetLastMessageAt()),
		BuyerLastReadSeq:      c.GetBuyerLastReadSeq(),
		SellerLastReadSeq:     c.GetSellerLastReadSeq(),
		UnreadCount:           c.GetUnreadCount(),
		CreatedAt:             httpx.FormatTimestamp(c.GetCreatedAt()),
		UpdatedAt:             httpx.FormatTimestamp(c.GetUpdatedAt()),
	}
}

func mapChatMessage(m *chatv1.ChatMessage) *chatMessageDTO {
	if m == nil {
		return nil
	}
	meta := json.RawMessage("{}")
	if raw := strings.TrimSpace(m.GetMetadataJson()); raw != "" && json.Valid([]byte(raw)) {
		meta = json.RawMessage(raw)
	}
	return &chatMessageDTO{
		ID:             m.GetId(),
		Seq:            m.GetSeq(),
		ConversationID: m.GetConversationId(),
		SenderID:       m.GetSenderId(),
		SenderRole:     m.GetSenderRole(),
		Kind:           chatMessageKindString(m.GetKind()),
		Body:           m.GetBody(),
		RefID:          m.GetRefId(),
		Metadata:       meta,
		IsAutoReply:    m.GetIsAutoReply(),
		ClientMsgID:    m.GetClientMsgId(),
		CreatedAt:      httpx.FormatTimestamp(m.GetCreatedAt()),
	}
}

func mapChatAutoReplyConfig(cfg *chatv1.AutoReplyConfig) *chatAutoReplyConfigDTO {
	if cfg == nil {
		return nil
	}
	rules := make([]*chatKeywordRuleDTO, 0, len(cfg.GetKeywordRules()))
	for _, r := range cfg.GetKeywordRules() {
		kws := make([]string, 0, len(r.GetKeywords()))
		kws = append(kws, r.GetKeywords()...)
		rules = append(rules, &chatKeywordRuleDTO{
			Keywords: kws,
			Reply:    r.GetReply(),
		})
	}
	return &chatAutoReplyConfigDTO{
		ShopID:          cfg.GetShopId(),
		Enabled:         cfg.GetEnabled(),
		GreetingMessage: cfg.GetGreetingMessage(),
		OfflineEnabled:  cfg.GetOfflineEnabled(),
		OfflineMessage:  cfg.GetOfflineMessage(),
		WorkStartHour:   cfg.GetWorkStartHour(),
		WorkEndHour:     cfg.GetWorkEndHour(),
		Timezone:        cfg.GetTimezone(),
		CooldownMinutes: cfg.GetCooldownMinutes(),
		KeywordRules:    rules,
		UpdatedAt:       httpx.FormatTimestamp(cfg.GetUpdatedAt()),
	}
}

func chatMessageKindString(k chatv1.MessageKind) string {
	switch k {
	case chatv1.MessageKind_MESSAGE_KIND_TEXT:
		return "text"
	case chatv1.MessageKind_MESSAGE_KIND_IMAGE:
		return "image"
	case chatv1.MessageKind_MESSAGE_KIND_PRODUCT:
		return "product"
	case chatv1.MessageKind_MESSAGE_KIND_ORDER:
		return "order"
	default:
		return ""
	}
}

func parseChatMessageKind(s string) (chatv1.MessageKind, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text", "message_kind_text":
		return chatv1.MessageKind_MESSAGE_KIND_TEXT, true
	case "image", "message_kind_image":
		return chatv1.MessageKind_MESSAGE_KIND_IMAGE, true
	case "product", "message_kind_product":
		return chatv1.MessageKind_MESSAGE_KIND_PRODUCT, true
	case "order", "message_kind_order":
		return chatv1.MessageKind_MESSAGE_KIND_ORDER, true
	default:
		return chatv1.MessageKind_MESSAGE_KIND_UNSPECIFIED, false
	}
}

func normalizeChatMetadataJSON(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return "", fmt.Errorf("metadata must be a valid JSON object")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func extractChatCookieToken(r *http.Request) string {
	for _, name := range []string{"__Host-nm_at", "nm_at"} {
		if c, err := r.Cookie(name); err == nil {
			if val := strings.TrimSpace(c.Value); val != "" {
				return val
			}
		}
	}
	return ""
}

func ensureBearerPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return "Bearer " + strings.TrimSpace(raw[len("bearer "):])
	}
	return "Bearer " + raw
}
