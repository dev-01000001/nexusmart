package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/hub"
	"github.com/nexusmart/core/services/chat/internal/service"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/nexusmart/core/services/chat/internal/validator"
)

const (
	maxReadBytes        = 64 << 10 // 64 KiB
	defaultPingInterval = 30 * time.Second
	defaultWriteTimeout = 10 * time.Second
	opTimeout           = 10 * time.Second

	cookieSecureAT   = "__Host-nm_at"
	cookieDevAT      = "nm_at"
	authMethodHeader = "X-Chat-Auth-Method"
)

// RevocationChecker checks whether a JWT has been revoked individually (by JTI)
// or globally for its subject (user:revoked_before:<uid>) in Redis.
type RevocationChecker interface {
	IsTokenRevoked(ctx context.Context, claims *auth.Claims) (bool, error)
}

// Config configures the WebSocket handler.
type Config struct {
	SiteRootDomain string
	AllowedOrigins []string
	PingInterval   time.Duration
	WriteTimeout   time.Duration
	ShutdownCtx    context.Context
}

// ClientFrame is the JSON protocol sent by WebSocket clients.
type ClientFrame struct {
	Type           string         `json:"type"`
	RequestID      string         `json:"request_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Kind           string         `json:"kind,omitempty"`
	Body           string         `json:"body,omitempty"`
	RefID          string         `json:"ref_id,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ClientMsgID    string         `json:"client_msg_id,omitempty"`
	LastReadSeq    int64          `json:"last_read_seq,omitempty"`
	MessageID      string         `json:"message_id,omitempty"`
	After          string         `json:"after,omitempty"`
	Limit          int32          `json:"limit,omitempty"`
}

// Handler upgrades authenticated HTTP requests to WebSocket and bridges them to ChatService + Hub.
type Handler struct {
	svc            *service.ChatService
	hub            *hub.Hub
	jwtMgr         *auth.Manager
	revoker        RevocationChecker
	originPatterns []string
	pingInterval   time.Duration
	writeTimeout   time.Duration
	shutdownCtx    context.Context
	log            *slog.Logger
}

// NewHandler creates a Chat WebSocket HTTP handler.
func NewHandler(
	svc *service.ChatService,
	h *hub.Hub,
	jwtMgr *auth.Manager,
	revoker RevocationChecker,
	cfg Config,
	log *slog.Logger,
) *Handler {
	if log == nil {
		log = slog.Default()
	}
	pingInterval := cfg.PingInterval
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteTimeout
	}
	shutdownCtx := cfg.ShutdownCtx
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}

	return &Handler{
		svc:            svc,
		hub:            h,
		jwtMgr:         jwtMgr,
		revoker:        revoker,
		originPatterns: buildOriginPatterns(cfg.SiteRootDomain, cfg.AllowedOrigins),
		pingInterval:   pingInterval,
		writeTimeout:   writeTimeout,
		shutdownCtx:    shutdownCtx,
		log:            log,
	}
}

// ServeHTTP authenticates before upgrading, registers with the Hub, handles optional ?after= sync,
// and runs concurrent reader/writer loops with keepalive pings and structured error frames.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, usedCookie, statusCode, errCode, errMsg := h.authenticate(r)
	if claims == nil {
		writeHTTPError(w, statusCode, errCode, errMsg)
		return
	}

	// CompressionDisabled avoids per-connection flate buffers (~30-64 KB each) for 1,000+ sockets.
	// Single-use ticket / Bearer credentials are not auto-sent by browsers, so custom shop domains
	// do not require Origin allowlisting; cookie-authenticated upgrades enforce strict OriginPatterns.
	acceptOpts := &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	}
	if usedCookie || strings.EqualFold(strings.TrimSpace(r.Header.Get(authMethodHeader)), "cookie") {
		acceptOpts.OriginPatterns = h.originPatterns
	} else {
		acceptOpts.InsecureSkipVerify = true
	}
	conn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		h.log.WarnContext(r.Context(), "websocket accept failed", slog.String("error", err.Error()))
		return
	}
	conn.SetReadLimit(maxReadBytes)

	connCtx, connCancel := context.WithCancel(r.Context())
	defer connCancel()

	// Tie connection lifetime to graceful server shutdown context as well.
	stopShutdownWatch := context.AfterFunc(h.shutdownCtx, func() {
		connCancel()
	})
	defer stopShutdownWatch()

	client := hub.NewClient(uuid.NewString(), claims.UserID(), claims.ShopID)
	h.hub.Register(client)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		h.writeLoop(connCtx, connCancel, conn, client)
	}()

	// Emit initial connected handshake frame.
	enqueueJSON(client, map[string]any{
		"type":        "connected",
		"user_id":     claims.UserID(),
		"shop_id":     claims.ShopID,
		"instance_id": h.hub.InstanceID(),
	})

	// Optional immediate reconnect catch-up via ?after=<cursor> (& optional ?conversation_id=<id>).
	if after := strings.TrimSpace(r.URL.Query().Get("after")); after != "" {
		convID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
		h.handleSync(connCtx, claims, client, "", convID, after, 100)
	}

	// Read loop processes client frames until disconnect or shutdown.
	for {
		msgType, raw, readErr := conn.Read(connCtx)
		if readErr != nil {
			break
		}
		if msgType != websocket.MessageText {
			h.sendError(client, "", "INVALID_ARGUMENT", "only text JSON frames are supported")
			continue
		}
		var frame ClientFrame
		if unmarshalErr := json.Unmarshal(raw, &frame); unmarshalErr != nil {
			h.sendError(client, "", "INVALID_ARGUMENT", "malformed JSON frame")
			continue
		}
		h.dispatchClientFrame(connCtx, claims, client, frame)
	}

	connCancel()
	h.hub.Unregister(client)
	<-writerDone
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func (h *Handler) authenticate(r *http.Request) (claims *auth.Claims, usedCookie bool, httpStatus int, errCode, errMsg string) {
	// 1. Authorization: Bearer <jwt>
	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
		c, st, code, msg := h.verifyJWTAndRevocation(r.Context(), authHeader)
		return c, false, st, code, msg
	}

	// 2. One-time ?ticket=<t> query parameter (never logged; checked before Cookie so explicit
	// tickets from custom domains are never overshadowed by an ambient host cookie).
	if rawTicket := strings.TrimSpace(r.URL.Query().Get("ticket")); rawTicket != "" {
		redeemed, err := h.svc.ExchangeWSTicket(r.Context(), rawTicket)
		if err != nil {
			if errors.Is(err, ticket.ErrInvalidTicket) || errors.Is(err, ticket.ErrRevokedToken) {
				return nil, false, http.StatusUnauthorized, "UNAUTHENTICATED", err.Error()
			}
			h.log.ErrorContext(r.Context(), "ws ticket exchange error", slog.String("error", err.Error()))
			return nil, false, http.StatusServiceUnavailable, "UNAVAILABLE", "authentication service unavailable"
		}
		return &auth.Claims{
			Role:   redeemed.Role,
			ShopID: redeemed.ShopID,
			RegisteredClaims: jwt.RegisteredClaims{
				Subject: redeemed.UserID,
				ID:      redeemed.JTI,
			},
		}, false, http.StatusOK, "", ""
	}

	// 3. BFF access-token cookies (__Host-nm_at preferred, fallback nm_at)
	if c, err := r.Cookie(cookieSecureAT); err == nil && strings.TrimSpace(c.Value) != "" {
		cl, st, code, msg := h.verifyJWTAndRevocation(r.Context(), c.Value)
		return cl, true, st, code, msg
	}
	if c, err := r.Cookie(cookieDevAT); err == nil && strings.TrimSpace(c.Value) != "" {
		cl, st, code, msg := h.verifyJWTAndRevocation(r.Context(), c.Value)
		return cl, true, st, code, msg
	}

	return nil, false, http.StatusUnauthorized, "UNAUTHENTICATED", "missing websocket credentials"
}

func (h *Handler) verifyJWTAndRevocation(ctx context.Context, rawToken string) (*auth.Claims, int, string, string) {
	if h.jwtMgr == nil {
		return nil, http.StatusServiceUnavailable, "UNAVAILABLE", "authentication manager unavailable"
	}
	claims, err := h.jwtMgr.VerifyAccessToken(rawToken)
	if err != nil {
		return nil, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired access token"
	}
	if h.revoker != nil {
		revoked, revErr := h.revoker.IsTokenRevoked(ctx, claims)
		if revErr != nil {
			h.log.ErrorContext(ctx, "ws token revocation check failed", slog.String("error", revErr.Error()))
			return nil, http.StatusServiceUnavailable, "UNAVAILABLE", "authentication service unavailable"
		}
		if revoked {
			return nil, http.StatusUnauthorized, "UNAUTHENTICATED", "access token has been revoked"
		}
	}
	return claims, http.StatusOK, "", ""
}

func (h *Handler) writeLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, client *hub.Client) {
	ticker := time.NewTicker(h.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-client.Send:
			wCtx, wCancel := context.WithTimeout(ctx, h.writeTimeout)
			err := conn.Write(wCtx, websocket.MessageText, payload)
			wCancel()
			if err != nil {
				cancel()
				return
			}
		case <-ticker.C:
			pCtx, pCancel := context.WithTimeout(ctx, h.writeTimeout)
			err := conn.Ping(pCtx)
			pCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

func (h *Handler) dispatchClientFrame(ctx context.Context, claims *auth.Claims, client *hub.Client, frame ClientFrame) {
	opCtx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	switch strings.ToLower(strings.TrimSpace(frame.Type)) {
	case "ping":
		enqueueJSON(client, map[string]any{
			"type":       "pong",
			"request_id": frame.RequestID,
			"ts":         time.Now().UTC().Format(time.RFC3339Nano),
		})

	case "send":
		out, err := h.svc.SendMessage(opCtx, claims, service.SendMessageInput{
			ConversationID: frame.ConversationID,
			Kind:           frame.Kind,
			Body:           frame.Body,
			RefID:          frame.RefID,
			Metadata:       frame.Metadata,
			ClientMsgID:    frame.ClientMsgID,
		})
		if err != nil {
			code, msg := classifyFrameError(err)
			h.sendError(client, frame.RequestID, code, msg)
			return
		}
		ack := map[string]any{
			"type":       "ack",
			"request_id": frame.RequestID,
			"message":    service.ToWireMessage(out.Message),
		}
		if out.AutoReply != nil {
			ack["auto_reply"] = service.ToWireMessage(*out.AutoReply)
		}
		enqueueJSON(client, ack)

	case "read":
		res, err := h.svc.MarkRead(opCtx, claims, frame.ConversationID, frame.LastReadSeq, frame.MessageID)
		if err != nil {
			code, msg := classifyFrameError(err)
			h.sendError(client, frame.RequestID, code, msg)
			return
		}
		enqueueJSON(client, map[string]any{
			"type":            "read_ack",
			"request_id":      frame.RequestID,
			"conversation_id": res.ConversationID,
			"last_read_seq":   res.LastReadSeq,
			"unread_count":    res.UnreadCount,
		})

	case "sync":
		h.handleSync(opCtx, claims, client, frame.RequestID, frame.ConversationID, frame.After, frame.Limit)

	default:
		h.sendError(client, frame.RequestID, "INVALID_ARGUMENT", "unsupported frame type")
	}
}

func (h *Handler) handleSync(
	ctx context.Context,
	claims *auth.Claims,
	client *hub.Client,
	requestID, conversationID, after string,
	limit int32,
) {
	res, err := h.svc.ListMissedMessages(ctx, claims, conversationID, after, limit)
	if err != nil {
		code, msg := classifyFrameError(err)
		h.sendError(client, requestID, code, msg)
		return
	}
	wireMsgs := make([]service.WireMessage, 0, len(res.Messages))
	for _, m := range res.Messages {
		wireMsgs = append(wireMsgs, service.ToWireMessage(m))
	}
	enqueueJSON(client, map[string]any{
		"type":            "sync_result",
		"request_id":      requestID,
		"conversation_id": conversationID,
		"messages":        wireMsgs,
		"latest_cursor":   res.LatestCursor,
		"has_more":        res.HasMore,
	})
}

func (h *Handler) sendError(client *hub.Client, requestID, code, message string) {
	enqueueJSON(client, map[string]any{
		"type":       "error",
		"request_id": requestID,
		"code":       code,
		"message":    message,
	})
}

func enqueueJSON(client *hub.Client, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case client.Send <- raw:
	default:
	}
}

func classifyFrameError(err error) (string, string) {
	switch {
	case errors.Is(err, service.ErrUnauthenticated):
		return "UNAUTHENTICATED", err.Error()
	case errors.Is(err, service.ErrForbidden):
		return "FORBIDDEN", err.Error()
	case errors.Is(err, service.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return "NOT_FOUND", err.Error()
	case errors.Is(err, service.ErrInvalidArgument),
		errors.Is(err, store.ErrInvalidArgument),
		errors.Is(err, store.ErrInvalidCursor),
		errors.Is(err, validator.ErrInvalidImage):
		return "INVALID_ARGUMENT", err.Error()
	default:
		return "INTERNAL", "internal server error"
	}
}

func writeHTTPError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func buildOriginPatterns(siteRootDomain string, extra []string) []string {
	patterns := []string{
		"localhost",
		"localhost:*",
		"127.0.0.1",
		"127.0.0.1:*",
		"[::1]",
		"[::1]:*",
	}
	if root := strings.TrimSpace(strings.ToLower(siteRootDomain)); root != "" {
		root = stripOriginScheme(root)
		patterns = append(patterns, root, root+":*", "*."+root, "*."+root+":*")
	}
	for _, raw := range extra {
		if cleaned := stripOriginScheme(raw); cleaned != "" {
			patterns = append(patterns, cleaned)
		}
	}
	return patterns
}

func stripOriginScheme(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimRight(s, "/")
	return s
}
