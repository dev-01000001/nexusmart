package e2e

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const wsAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// --- Compact RFC 6455 WebSocket Client (Go stdlib only) ---

type wsClient struct {
	conn net.Conn
	br   *bufio.Reader
}

func dialWS(gatewayURL, pathAndQuery string, extraHeaders map[string]string, timeout time.Duration) (*wsClient, int, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil {
		return nil, 0, fmt.Errorf("parse gateway url %q: %w", gatewayURL, err)
	}
	host := u.Host
	if _, _, splitErr := net.SplitHostPort(host); splitErr != nil {
		if strings.EqualFold(u.Scheme, "https") {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}

	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if strings.EqualFold(u.Scheme, "https") {
		conn, err = tls.DialWithDialer(dialer, "tcp", host, &tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", host)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("dial ws %s: %w", host, err)
	}

	_ = conn.SetDeadline(time.Now().Add(timeout))

	keyBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, keyBytes); err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("generate ws key: %w", err)
	}
	wsKey := base64.StdEncoding.EncodeToString(keyBytes)

	var reqBuf strings.Builder
	reqBuf.WriteString("GET ")
	reqBuf.WriteString(pathAndQuery)
	reqBuf.WriteString(" HTTP/1.1\r\n")
	reqBuf.WriteString("Host: ")
	reqBuf.WriteString(u.Host)
	reqBuf.WriteString("\r\n")
	reqBuf.WriteString("Upgrade: websocket\r\n")
	reqBuf.WriteString("Connection: Upgrade\r\n")
	reqBuf.WriteString("Sec-WebSocket-Key: ")
	reqBuf.WriteString(wsKey)
	reqBuf.WriteString("\r\n")
	reqBuf.WriteString("Sec-WebSocket-Version: 13\r\n")
	if _, hasOrigin := extraHeaders["Origin"]; !hasOrigin {
		reqBuf.WriteString("Origin: http://localhost:3300\r\n")
	}
	for k, v := range extraHeaders {
		reqBuf.WriteString(k)
		reqBuf.WriteString(": ")
		reqBuf.WriteString(v)
		reqBuf.WriteString("\r\n")
	}
	reqBuf.WriteString("\r\n")

	if _, err := io.WriteString(conn, reqBuf.String()); err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("write ws handshake: %w", err)
	}

	br := bufio.NewReader(conn)
	tp := textproto.NewReader(br)
	statusLine, err := tp.ReadLine()
	if err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("read ws status line: %w", err)
	}
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("malformed http status line %q", statusLine)
	}
	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("parse http status %q: %w", statusLine, err)
	}
	hdrs, err := tp.ReadMIMEHeader()
	if err != nil {
		_ = conn.Close()
		return nil, statusCode, fmt.Errorf("read ws headers: %w", err)
	}

	if statusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, statusCode, fmt.Errorf("ws upgrade rejected with HTTP %d", statusCode)
	}

	h := sha1.New()
	_, _ = h.Write([]byte(wsKey + wsAcceptGUID))
	wantAccept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	gotAccept := strings.TrimSpace(hdrs.Get("Sec-WebSocket-Accept"))
	if gotAccept != wantAccept {
		_ = conn.Close()
		return nil, statusCode, fmt.Errorf("invalid Sec-WebSocket-Accept: got %q want %q", gotAccept, wantAccept)
	}

	_ = conn.SetDeadline(time.Time{})
	return &wsClient{conn: conn, br: br}, statusCode, nil
}

func (c *wsClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_ = c.writeFrame(0x8, nil)
	return c.conn.Close()
}

func (c *wsClient) WriteJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(0x1, raw)
}

func (c *wsClient) writeFrame(opcode byte, payload []byte) error {
	var mask [4]byte
	if _, err := io.ReadFull(rand.Reader, mask[:]); err != nil {
		return err
	}

	header := make([]byte, 0, 14)
	header = append(header, 0x80|(opcode&0x0F))

	n := len(payload)
	switch {
	case n <= 125:
		header = append(header, 0x80|byte(n))
	case n <= 65535:
		header = append(header, 0x80|126)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(n))
		header = append(header, b[:]...)
	default:
		header = append(header, 0x80|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		header = append(header, b[:]...)
	}
	header = append(header, mask[:]...)

	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}

	frame := append(header, masked...)
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(frame)
	return err
}

func (c *wsClient) ReadTextFrame(timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var assembled []byte

	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		var hdr [2]byte
		if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
			return nil, err
		}
		fin := (hdr[0] & 0x80) != 0
		opcode := hdr[0] & 0x0F
		masked := (hdr[1] & 0x80) != 0
		length := uint64(hdr[1] & 0x7F)

		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(ext[:])
		}
		if length > 4<<20 {
			return nil, fmt.Errorf("ws frame too large: %d bytes", length)
		}

		var maskKey [4]byte
		if masked {
			if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
				return nil, err
			}
		}

		payload := make([]byte, length)
		if length > 0 {
			if _, err := io.ReadFull(c.br, payload); err != nil {
				return nil, err
			}
		}
		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}

		switch opcode {
		case 0x9: // Ping -> reply Pong
			_ = c.writeFrame(0xA, payload)
			continue
		case 0xA: // Pong
			continue
		case 0x8: // Close
			return nil, io.EOF
		case 0x1, 0x0: // Text or continuation
			assembled = append(assembled, payload...)
			if fin {
				return assembled, nil
			}
		default:
			continue
		}
	}
}

// --- Chat DTOs ---

type e2eChatKeywordRuleDTO struct {
	Keywords []string `json:"keywords"`
	Reply    string   `json:"reply"`
}

type e2eChatAutoReplyConfigDTO struct {
	ShopID          string                  `json:"shop_id"`
	Enabled         bool                    `json:"enabled"`
	GreetingMessage string                  `json:"greeting_message"`
	OfflineEnabled  bool                    `json:"offline_enabled"`
	OfflineMessage  string                  `json:"offline_message"`
	WorkStartHour   int32                   `json:"work_start_hour"`
	WorkEndHour     int32                   `json:"work_end_hour"`
	Timezone        string                  `json:"timezone"`
	CooldownMinutes int32                   `json:"cooldown_minutes"`
	KeywordRules    []e2eChatKeywordRuleDTO `json:"keyword_rules"`
}

type e2eChatAutoReplyResponse struct {
	Config e2eChatAutoReplyConfigDTO `json:"config"`
}

type e2eChatConversationDTO struct {
	ID                    string `json:"id"`
	BuyerID               string `json:"buyer_id"`
	ShopID                string `json:"shop_id"`
	SiteHost              string `json:"site_host"`
	LastMessageID         string `json:"last_message_id"`
	LastMessageSeq        int64  `json:"last_message_seq"`
	LastMessagePreview    string `json:"last_message_preview"`
	LastMessageSenderRole string `json:"last_message_sender_role"`
	BuyerLastReadSeq      int64  `json:"buyer_last_read_seq"`
	SellerLastReadSeq     int64  `json:"seller_last_read_seq"`
	UnreadCount           int64  `json:"unread_count"`
}

type e2eChatMessageDTO struct {
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
}

type e2eCreateConversationResponse struct {
	Conversation   e2eChatConversationDTO `json:"conversation"`
	Created        bool                   `json:"created"`
	InitialMessage *e2eChatMessageDTO     `json:"initial_message"`
}

type e2eListConversationsResponse struct {
	Items       []e2eChatConversationDTO `json:"items"`
	Total       int64                    `json:"total"`
	TotalUnread int64                    `json:"total_unread"`
}

type e2eListMessagesResponse struct {
	Conversation e2eChatConversationDTO `json:"conversation"`
	Items        []e2eChatMessageDTO    `json:"items"`
	NextBefore   string                 `json:"next_before"`
	LatestCursor string                 `json:"latest_cursor"`
	HasMore      bool                   `json:"has_more"`
}

type e2eSendMessageResponse struct {
	Message   e2eChatMessageDTO  `json:"message"`
	AutoReply *e2eChatMessageDTO `json:"auto_reply"`
}

type e2eMarkReadResponse struct {
	ConversationID string `json:"conversation_id"`
	LastReadSeq    int64  `json:"last_read_seq"`
	UnreadCount    int64  `json:"unread_count"`
}

type e2eWSTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
}

type e2eWSFrame struct {
	Type           string              `json:"type"`
	RequestID      string              `json:"request_id"`
	UserID         string              `json:"user_id"`
	ShopID         string              `json:"shop_id"`
	ConversationID string              `json:"conversation_id"`
	Message        *e2eChatMessageDTO  `json:"message"`
	AutoReply      *e2eChatMessageDTO  `json:"auto_reply"`
	Messages       []e2eChatMessageDTO `json:"messages"`
	LatestCursor   string              `json:"latest_cursor"`
	Code           string              `json:"code"`
}

func waitForWSFrame(t *testing.T, ws *wsClient, timeout time.Duration, desc string, match func(e2eWSFrame) bool) e2eWSFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var seen []string
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for WS frame (%s); seen=%v", desc, seen)
		}
		raw, err := ws.ReadTextFrame(remaining)
		if err != nil {
			t.Fatalf("read WS frame while waiting for %s: %v (seen=%v)", desc, err, seen)
		}
		var frame e2eWSFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("decode WS frame (%s): %v; raw=%s", desc, err, string(raw))
		}
		seen = append(seen, frame.Type)
		if match(frame) {
			return frame
		}
	}
}

func TestE2E_Chat_RESTAutoReplyWebSocketSyncAndRBAC(t *testing.T) {
	env := getEnv()
	registerRateLimitResetCleanup(t, env)

	chatHTTPURL := resolveServiceHTTPURL(env, "CHAT_HTTP_URL", "chat", "8090")

	// 1. Verify chat HTTP ops endpoints (/healthz, /readyz, /metrics).
	t.Run("ServiceOps_HealthReadyMetrics", func(t *testing.T) {
		verifyServiceOpsEndpoints(t, env, "chat", chatHTTPURL)
	})

	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer1 := env.registerVerifyAndLogin(t, "buyer-chat-1")
	buyer2 := env.registerVerifyAndLogin(t, "buyer-chat-2")
	prod1, _ := env.createApprovedProduct(t, seller1, cat.ID, "Áo Len Chat E2E "+env.runID, 195000, 20, 400)

	// 2. Seller configures auto-reply (PUT + GET) and buyer is forbidden.
	const keywordReply = "Shop bảo hành chính hãng 12 tháng và hỗ trợ đổi size miễn phí."
	t.Run("SellerAutoReply_PutGetAndRBAC", func(t *testing.T) {
		putPayload := map[string]any{
			"enabled":          true,
			"greeting_message": "Xin chào! Cảm ơn bạn đã nhắn tin cho gian hàng.",
			"offline_enabled":  false,
			"offline_message":  "Shop đang nghỉ, sẽ phản hồi sớm nhất.",
			"work_start_hour":  0,
			"work_end_hour":    23,
			"timezone":         "Asia/Ho_Chi_Minh",
			"cooldown_minutes": 0,
			"keyword_rules": []map[string]any{
				{
					"keywords": []string{"bảo hành", "đổi size"},
					"reply":    keywordReply,
				},
			},
		}
		putResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/chat/auto-reply", seller1.AccessToken, nil, putPayload, http.StatusOK)
		var putOut e2eChatAutoReplyResponse
		if err := DecodeJSON(putResp, &putOut); err != nil {
			t.Fatalf("decode put auto-reply: %v", err)
		}
		if !putOut.Config.Enabled || putOut.Config.ShopID != seller1.ShopID || len(putOut.Config.KeywordRules) != 1 {
			t.Fatalf("unexpected put auto-reply response: %+v", putOut.Config)
		}

		getResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/chat/auto-reply", seller1.AccessToken, nil, nil, http.StatusOK)
		var getOut e2eChatAutoReplyResponse
		if err := DecodeJSON(getResp, &getOut); err != nil {
			t.Fatalf("decode get auto-reply: %v", err)
		}
		if !getOut.Config.Enabled || getOut.Config.KeywordRules[0].Reply != keywordReply {
			t.Fatalf("GET auto-reply mismatch: %+v", getOut.Config)
		}

		// Buyer MUST get 403 on seller auto-reply & seller inbox routes.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/chat/auto-reply", buyer1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/chat/auto-reply", buyer1.AccessToken, nil, putPayload, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/chat/conversations", buyer1.AccessToken, nil, nil, http.StatusForbidden)
	})

	// 3. Buyer creates/gets conversation and both buyer & seller list it.
	var convID string
	t.Run("CreateAndListConversations", func(t *testing.T) {
		createResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/chat/conversations", buyer1.AccessToken, nil, map[string]any{
			"shop_id":   seller1.ShopID,
			"site_host": "e2e-chat." + env.cfg.SiteRootDomain,
		}, http.StatusCreated, http.StatusOK)
		var createOut e2eCreateConversationResponse
		if err := DecodeJSON(createResp, &createOut); err != nil {
			t.Fatalf("decode create conversation: %v", err)
		}
		if createOut.Conversation.ID == "" || createOut.Conversation.ShopID != seller1.ShopID || createOut.Conversation.BuyerID != buyer1.UserID {
			t.Fatalf("unexpected conversation created: %+v", createOut.Conversation)
		}
		convID = createOut.Conversation.ID

		// Calling POST /api/v1/chat/conversations again returns the same conversation idempotently.
		againResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/chat/conversations", buyer1.AccessToken, nil, map[string]any{
			"shop_id": seller1.ShopID,
		}, http.StatusOK)
		var againOut e2eCreateConversationResponse
		if err := DecodeJSON(againResp, &againOut); err != nil {
			t.Fatalf("decode idempotent get conversation: %v", err)
		}
		if againOut.Conversation.ID != convID || againOut.Created {
			t.Fatalf("expected existing conversation %s with created=false, got %+v", convID, againOut)
		}

		// Buyer lists conversations.
		bListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/chat/conversations?page=1&page_size=20", buyer1.AccessToken, nil, nil, http.StatusOK)
		var bList e2eListConversationsResponse
		if err := DecodeJSON(bListResp, &bList); err != nil {
			t.Fatalf("decode buyer list conversations: %v", err)
		}
		foundBuyer := false
		for _, c := range bList.Items {
			if c.ID == convID {
				foundBuyer = true
				break
			}
		}
		if !foundBuyer {
			t.Fatalf("buyer conversation list missing %s: %+v", convID, bList.Items)
		}

		// Seller1 lists conversations.
		sListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/chat/conversations?page=1&page_size=20", seller1.AccessToken, nil, nil, http.StatusOK)
		var sList e2eListConversationsResponse
		if err := DecodeJSON(sListResp, &sList); err != nil {
			t.Fatalf("decode seller list conversations: %v", err)
		}
		foundSeller := false
		for _, c := range sList.Items {
			if c.ID == convID {
				foundSeller = true
				break
			}
		}
		if !foundSeller {
			t.Fatalf("seller1 conversation list missing %s: %+v", convID, sList.Items)
		}
	})

	msgBasePath := "/api/v1/chat/conversations/" + url.PathEscape(convID)

	// 4. Send text (triggers auto-reply), product, order, safe image, and reject bad/SSRF/SVG images.
	t.Run("SendMessages_AllKindsAutoReplyAndImageSecurity", func(t *testing.T) {
		// Text message triggering keyword auto-reply.
		txtResp := env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", buyer1.AccessToken, nil, map[string]any{
			"kind":          "text",
			"body":          "Shop ơi cho mình hỏi chính sách bảo hành sản phẩm này nhé",
			"client_msg_id": "cli-txt-" + uuid.NewString(),
		}, http.StatusCreated)
		var txtOut e2eSendMessageResponse
		if err := DecodeJSON(txtResp, &txtOut); err != nil {
			t.Fatalf("decode send text: %v", err)
		}
		if txtOut.Message.Kind != "text" || txtOut.Message.SenderRole != "buyer" {
			t.Fatalf("unexpected text message: %+v", txtOut.Message)
		}
		if txtOut.AutoReply == nil || !txtOut.AutoReply.IsAutoReply || txtOut.AutoReply.Body != keywordReply {
			t.Fatalf("expected keyword auto-reply %q, got %+v", keywordReply, txtOut.AutoReply)
		}

		// Product card message.
		prodResp := env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", buyer1.AccessToken, nil, map[string]any{
			"kind":          "product",
			"ref_id":        prod1.ID,
			"body":          prod1.Name,
			"metadata":      map[string]any{"price_vnd": 195000},
			"client_msg_id": "cli-prod-" + uuid.NewString(),
		}, http.StatusCreated)
		var prodOut e2eSendMessageResponse
		if err := DecodeJSON(prodResp, &prodOut); err != nil {
			t.Fatalf("decode send product msg: %v", err)
		}
		if prodOut.Message.Kind != "product" || prodOut.Message.RefID != prod1.ID {
			t.Fatalf("unexpected product message: %+v", prodOut.Message)
		}

		// Order card message.
		orderRef := "NM-20261121-CHAT" + env.runID
		ordResp := env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", buyer1.AccessToken, nil, map[string]any{
			"kind":          "order",
			"ref_id":        orderRef,
			"body":          "Kiểm tra giúp mình đơn hàng " + orderRef,
			"client_msg_id": "cli-ord-" + uuid.NewString(),
		}, http.StatusCreated)
		var ordOut e2eSendMessageResponse
		if err := DecodeJSON(ordResp, &ordOut); err != nil {
			t.Fatalf("decode send order msg: %v", err)
		}
		if ordOut.Message.Kind != "order" || ordOut.Message.RefID != orderRef {
			t.Fatalf("unexpected order message: %+v", ordOut.Message)
		}

		// Safe image message (hybrid validation passes with valid public HTTPS URL + allowed image MIME & size).
		imgResp := env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", buyer1.AccessToken, nil, map[string]any{
			"kind":          "image",
			"body":          "https://cdn.example.com/uploads/e2e-chat-photo.png",
			"metadata":      map[string]any{"content_type": "image/png", "size_bytes": 24576},
			"client_msg_id": "cli-img-" + uuid.NewString(),
		}, http.StatusCreated)
		var imgOut e2eSendMessageResponse
		if err := DecodeJSON(imgResp, &imgOut); err != nil {
			t.Fatalf("decode send image msg: %v", err)
		}
		if imgOut.Message.Kind != "image" {
			t.Fatalf("unexpected image message: %+v", imgOut.Message)
		}

		// Reject bad / SSRF / SVG / javascript image URLs -> 400 Bad Request.
		badImages := []struct {
			name string
			body string
			meta map[string]any
		}{
			{"svg extension", "https://cdn.example.com/payload.svg", map[string]any{"content_type": "image/png", "size_bytes": 512}},
			{"svg mime", "https://cdn.example.com/payload.png", map[string]any{"content_type": "image/svg+xml", "size_bytes": 512}},
			{"ssrf loopback", "http://127.0.0.1/secret.png", map[string]any{"content_type": "image/png", "size_bytes": 512}},
			{"ssrf metadata ip", "http://169.254.169.254/latest/meta-data/a.png", map[string]any{"content_type": "image/png", "size_bytes": 512}},
			{"ssrf internal host", "https://metadata.google.internal/img.png", map[string]any{"content_type": "image/png", "size_bytes": 512}},
			{"javascript scheme", "javascript:alert(1)", map[string]any{"content_type": "image/png", "size_bytes": 512}},
			{"oversized >5MB", "https://cdn.example.com/huge.jpg", map[string]any{"content_type": "image/jpeg", "size_bytes": 6291456}},
		}
		for _, tc := range badImages {
			env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", buyer1.AccessToken, nil, map[string]any{
				"kind":     "image",
				"body":     tc.body,
				"metadata": tc.meta,
			}, http.StatusBadRequest)
		}
	})

	// 5. Mark read advances seller read cursor and clears unread count.
	t.Run("ListMessagesAndMarkRead", func(t *testing.T) {
		listResp := env.mustDoJSON(t, http.MethodGet, msgBasePath+"/messages?limit=50", seller1.AccessToken, nil, nil, http.StatusOK)
		var listOut e2eListMessagesResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode list messages: %v", err)
		}
		if len(listOut.Items) < 5 {
			t.Fatalf("expected >= 5 messages (4 buyer + auto-reply), got %d", len(listOut.Items))
		}
		maxSeq := listOut.Items[len(listOut.Items)-1].Seq

		readResp := env.mustDoJSON(t, http.MethodPost, msgBasePath+"/read", seller1.AccessToken, nil, map[string]any{
			"last_read_seq": maxSeq,
		}, http.StatusOK)
		var readOut e2eMarkReadResponse
		if err := DecodeJSON(readResp, &readOut); err != nil {
			t.Fatalf("decode mark read: %v", err)
		}
		if readOut.ConversationID != convID || readOut.LastReadSeq < maxSeq || readOut.UnreadCount != 0 {
			t.Fatalf("unexpected mark-read response: %+v (want seq>=%d unread=0)", readOut, maxSeq)
		}
	})

	// 6. One-time WS ticket, single-use rejection, real-time buyer<->seller delivery, and reconnect cursor sync.
	t.Run("WebSocket_TicketSingleUseRealtimeDeliveryAndReconnectSync", func(t *testing.T) {
		// Issue one-time ticket for buyer1.
		tktBuyerResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/chat/ws-ticket", buyer1.AccessToken, nil, nil, http.StatusCreated, http.StatusOK)
		var tktBuyer e2eWSTicketResponse
		if err := DecodeJSON(tktBuyerResp, &tktBuyer); err != nil {
			t.Fatalf("decode buyer ws-ticket: %v", err)
		}
		if tktBuyer.Ticket == "" {
			t.Fatalf("buyer ws-ticket returned empty ticket")
		}

		// Connect buyer1 via WebSocket using one-time ticket.
		buyerWS, statusCode, err := dialWS(env.cfg.GatewayURL, "/api/v1/chat/ws?ticket="+url.QueryEscape(tktBuyer.Ticket), nil, 10*time.Second)
		if err != nil {
			t.Fatalf("buyer1 WS dial failed (HTTP %d): %v", statusCode, err)
		}
		defer func() { _ = buyerWS.Close() }()

		// Single-use ticket MUST be rejected (HTTP 401) on immediate reuse.
		reuseWS, reuseStatus, reuseErr := dialWS(env.cfg.GatewayURL, "/api/v1/chat/ws?ticket="+url.QueryEscape(tktBuyer.Ticket), nil, 10*time.Second)
		if reuseErr == nil {
			_ = reuseWS.Close()
			t.Fatalf("expected reused WS ticket to be rejected, but upgrade succeeded")
		}
		if reuseStatus != http.StatusUnauthorized {
			t.Fatalf("expected HTTP 401 on reused WS ticket, got HTTP %d (%v)", reuseStatus, reuseErr)
		}

		// Verify buyer1 receives initial `connected` frame.
		connBuyer := waitForWSFrame(t, buyerWS, 10*time.Second, "buyer connected frame", func(f e2eWSFrame) bool {
			return f.Type == "connected"
		})
		if connBuyer.UserID != buyer1.UserID {
			t.Fatalf("expected connected frame user_id=%s, got %+v", buyer1.UserID, connBuyer)
		}

		// Issue one-time ticket and connect seller1 over WebSocket.
		tktSellerResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/chat/ws-ticket", seller1.AccessToken, nil, nil, http.StatusCreated, http.StatusOK)
		var tktSeller e2eWSTicketResponse
		if err := DecodeJSON(tktSellerResp, &tktSeller); err != nil {
			t.Fatalf("decode seller ws-ticket: %v", err)
		}
		sellerWS, sStatus, sErr := dialWS(env.cfg.GatewayURL, "/api/v1/chat/ws?ticket="+url.QueryEscape(tktSeller.Ticket), nil, 10*time.Second)
		if sErr != nil {
			t.Fatalf("seller1 WS dial failed (HTTP %d): %v", sStatus, sErr)
		}
		defer func() { _ = sellerWS.Close() }()

		connSeller := waitForWSFrame(t, sellerWS, 10*time.Second, "seller connected frame", func(f e2eWSFrame) bool {
			return f.Type == "connected"
		})
		if connSeller.ShopID != seller1.ShopID {
			t.Fatalf("expected seller connected frame shop_id=%s, got %+v", seller1.ShopID, connSeller)
		}

		// Buyer1 sends a real-time message over WS -> Seller1 receives it over WS.
		buyerLiveText := "Tin nhắn realtime từ buyer " + env.runID
		if err := buyerWS.WriteJSON(map[string]any{
			"type":            "send",
			"request_id":      "ws-b1-" + uuid.NewString(),
			"conversation_id": convID,
			"kind":            "text",
			"body":            buyerLiveText,
			"client_msg_id":   "ws-cli-b-" + uuid.NewString(),
		}); err != nil {
			t.Fatalf("buyerWS send frame: %v", err)
		}

		waitForWSFrame(t, buyerWS, 10*time.Second, "buyer send ack", func(f e2eWSFrame) bool {
			return f.Type == "ack" && f.Message != nil && f.Message.Body == buyerLiveText
		})
		sellerGot := waitForWSFrame(t, sellerWS, 10*time.Second, "seller receives buyer realtime message", func(f e2eWSFrame) bool {
			return f.Type == "message" && f.ConversationID == convID && f.Message != nil && f.Message.Body == buyerLiveText
		})
		if sellerGot.Message == nil || sellerGot.Message.SenderRole != "buyer" {
			t.Fatalf("unexpected message delivered to seller: %+v", sellerGot)
		}

		// Seller1 replies over WS -> Buyer1 receives it in real time; record cursor.
		sellerLiveText := "Shop phản hồi realtime " + env.runID
		if err := sellerWS.WriteJSON(map[string]any{
			"type":            "send",
			"request_id":      "ws-s1-" + uuid.NewString(),
			"conversation_id": convID,
			"kind":            "text",
			"body":            sellerLiveText,
			"client_msg_id":   "ws-cli-s-" + uuid.NewString(),
		}); err != nil {
			t.Fatalf("sellerWS send frame: %v", err)
		}

		buyerGot := waitForWSFrame(t, buyerWS, 10*time.Second, "buyer receives seller realtime reply", func(f e2eWSFrame) bool {
			return f.Type == "message" && f.ConversationID == convID && f.Message != nil && f.Message.Body == sellerLiveText
		})
		if buyerGot.Message == nil || buyerGot.Message.Seq <= 0 {
			t.Fatalf("expected positive seq on delivered seller reply: %+v", buyerGot)
		}
		cursorBeforeDisconnect := strconv.FormatInt(buyerGot.Message.Seq, 10)

		// Close buyer1 WS, send a message while buyer1 is offline, then reconnect with ?after=<cursor>.
		_ = buyerWS.Close()

		missedText := "Tin nhắn gửi lúc buyer mất kết nối " + env.runID
		env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", seller1.AccessToken, nil, map[string]any{
			"kind":          "text",
			"body":          missedText,
			"client_msg_id": "offline-miss-" + uuid.NewString(),
		}, http.StatusCreated)

		tktReconnectResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/chat/ws-ticket", buyer1.AccessToken, nil, nil, http.StatusCreated, http.StatusOK)
		var tktReconnect e2eWSTicketResponse
		if err := DecodeJSON(tktReconnectResp, &tktReconnect); err != nil {
			t.Fatalf("decode reconnect ws-ticket: %v", err)
		}

		reconnectPath := fmt.Sprintf("/api/v1/chat/ws?ticket=%s&conversation_id=%s&after=%s",
			url.QueryEscape(tktReconnect.Ticket),
			url.QueryEscape(convID),
			url.QueryEscape(cursorBeforeDisconnect),
		)
		buyerReWS, recStatus, recErr := dialWS(env.cfg.GatewayURL, reconnectPath, nil, 10*time.Second)
		if recErr != nil {
			t.Fatalf("buyer1 reconnect WS failed (HTTP %d): %v", recStatus, recErr)
		}
		defer func() { _ = buyerReWS.Close() }()

		syncFrame := waitForWSFrame(t, buyerReWS, 10*time.Second, "sync_result after reconnect", func(f e2eWSFrame) bool {
			return f.Type == "sync_result"
		})
		foundMissed := false
		for _, m := range syncFrame.Messages {
			if m.Body == missedText {
				foundMissed = true
				break
			}
		}
		if !foundMissed {
			t.Fatalf("reconnect sync (?after=%s) missing offline message %q; got %+v",
				cursorBeforeDisconnect, missedText, syncFrame.Messages)
		}
	})

	// 7. Strict object-level RBAC: unrelated buyer2 & seller2 get 403/404 on list/send/mark-read.
	t.Run("StrictRBAC_UnrelatedBuyer2AndSeller2Denied", func(t *testing.T) {
		intruders := []struct {
			name  string
			token string
		}{
			{"buyer2", buyer2.AccessToken},
			{"seller2", seller2.AccessToken},
		}
		for _, intr := range intruders {
			env.mustDoJSON(t, http.MethodGet, msgBasePath+"/messages", intr.token, nil, nil, http.StatusForbidden, http.StatusNotFound)
			env.mustDoJSON(t, http.MethodPost, msgBasePath+"/messages", intr.token, nil, map[string]any{
				"kind": "text",
				"body": "unauthorized injection by " + intr.name,
			}, http.StatusForbidden, http.StatusNotFound)
			env.mustDoJSON(t, http.MethodPost, msgBasePath+"/read", intr.token, nil, map[string]any{
				"last_read_seq": 1,
			}, http.StatusForbidden, http.StatusNotFound)
		}

		// Anonymous WS without credentials/ticket MUST be rejected with 401.
		anonWS, anonStatus, anonErr := dialWS(env.cfg.GatewayURL, "/api/v1/chat/ws", nil, 10*time.Second)
		if anonErr == nil {
			_ = anonWS.Close()
			t.Fatalf("expected anonymous WS connection to fail")
		}
		if !errors.Is(anonErr, io.EOF) && anonStatus != http.StatusUnauthorized {
			t.Fatalf("expected HTTP 401 for anonymous WS, got HTTP %d (%v)", anonStatus, anonErr)
		}
	})
}
