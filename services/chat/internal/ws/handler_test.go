package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/hub"
	"github.com/nexusmart/core/services/chat/internal/service"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/redis/go-redis/v9"
)

func readJSONFrame(ctx context.Context, t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, raw, err := conn.Read(rCtx)
	if err != nil {
		t.Fatalf("read ws frame: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal ws frame %q: %v", string(raw), err)
	}
	return out
}

func writeJSONFrame(ctx context.Context, t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	wCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := conn.Write(wCtx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write ws frame: %v", err)
	}
}

func TestWebSocketAuthFramesSyncPermissionsAndMultiInstance(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	const secret = "ws-unit-test-secret"
	jwtMgr, err := auth.NewManager(secret, auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("jwt manager: %v", err)
	}
	ticketMgr, err := ticket.NewManager(rdb, secret, 60*time.Second)
	if err != nil {
		t.Fatalf("ticket manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := store.NewMemStore()
	hubA := hub.New(rdb, "ws-inst-a", nil, nil)
	hubB := hub.New(rdb, "ws-inst-b", nil, nil)
	go hubA.Run(ctx)
	go hubB.Run(ctx)

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	if err := hubA.WaitSubscribed(waitCtx); err != nil {
		t.Fatalf("hubA subscribe: %v", err)
	}
	if err := hubB.WaitSubscribed(waitCtx); err != nil {
		t.Fatalf("hubB subscribe: %v", err)
	}

	svcA := service.NewChatService(st, hubA, ticketMgr, nil, nil)
	svcB := service.NewChatService(st, hubB, ticketMgr, nil, nil)

	hA := NewHandler(svcA, hubA, jwtMgr, ticketMgr, Config{SiteRootDomain: "nexusmart.localhost", PingInterval: 2 * time.Second, ShutdownCtx: ctx}, nil)
	hB := NewHandler(svcB, hubB, jwtMgr, ticketMgr, Config{SiteRootDomain: "nexusmart.localhost", PingInterval: 2 * time.Second, ShutdownCtx: ctx}, nil)

	srvA := httptest.NewServer(hA)
	defer srvA.Close()
	srvB := httptest.NewServer(hB)
	defer srvB.Close()

	wsURLA := "ws" + strings.TrimPrefix(srvA.URL, "http")
	wsURLB := "ws" + strings.TrimPrefix(srvB.URL, "http")

	// Mint tokens for buyer, seller, and unrelated buyer.
	buyerTok, buyerJTI, _, _ := jwtMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := jwtMgr.SignAccessToken("seller-1", "seller", "shop-1")
	intruderTok, _, _, _ := jwtMgr.SignAccessToken("buyer-99", "buyer", "")

	// 1. Missing credentials -> 401 before upgrade.
	if _, resp, err := websocket.Dial(ctx, wsURLA, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without creds, got resp=%v err=%v", resp, err)
	}

	// 2. Revoked JTI -> 401 before upgrade.
	revokedTok, revokedJTI, _, _ := jwtMgr.SignAccessToken("buyer-rev", "buyer", "")
	_ = mr.Set("user:revoked_jti:"+revokedJTI, "1")
	if _, resp, err := websocket.Dial(ctx, wsURLA, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + revokedTok}},
	}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked JTI, got resp=%v err=%v", resp, err)
	}

	// Create conversation via service.
	buyerClaims, _ := jwtMgr.VerifyAccessToken(buyerTok)
	conv, _, _, err := svcA.CreateOrGetConversation(ctx, buyerClaims, "shop-1", "", "")
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}

	// 3. Buyer connects to Instance A using single-use ?ticket=<t> (and replay MUST fail!).
	rawTicket, _, err := svcA.CreateWSTicket(ctx, buyerClaims)
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	buyerConn, _, err := websocket.Dial(ctx, wsURLA+"?ticket="+rawTicket, nil)
	if err != nil {
		t.Fatalf("buyer dial via ticket: %v", err)
	}
	defer buyerConn.CloseNow()

	// Replaying the same ticket fails with 401.
	if _, resp, err := websocket.Dial(ctx, wsURLA+"?ticket="+rawTicket, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on ticket replay, got resp=%v err=%v", resp, err)
	}

	connFrame := readJSONFrame(ctx, t, buyerConn)
	if connFrame["type"] != "connected" || connFrame["user_id"] != "buyer-1" || connFrame["instance_id"] != "ws-inst-a" {
		t.Fatalf("unexpected buyer connected frame: %+v", connFrame)
	}

	// 4. Seller connects to Instance B using BFF cookie `nm_at`.
	sellerConn, _, err := websocket.Dial(ctx, wsURLB, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{"nm_at=" + sellerTok}},
	})
	if err != nil {
		t.Fatalf("seller dial via cookie: %v", err)
	}
	defer sellerConn.CloseNow()

	sellerConnFrame := readJSONFrame(ctx, t, sellerConn)
	if sellerConnFrame["type"] != "connected" || sellerConnFrame["shop_id"] != "shop-1" || sellerConnFrame["instance_id"] != "ws-inst-b" {
		t.Fatalf("unexpected seller connected frame: %+v", sellerConnFrame)
	}

	// 5. Ping/pong works.
	writeJSONFrame(ctx, t, buyerConn, map[string]any{"type": "ping", "request_id": "p1"})
	pong := readJSONFrame(ctx, t, buyerConn)
	if pong["type"] != "pong" || pong["request_id"] != "p1" {
		t.Fatalf("unexpected pong: %+v", pong)
	}

	// 6. Buyer sends message on Instance A -> Seller on Instance B receives real-time message frame via Redis Pub/Sub!
	writeJSONFrame(ctx, t, buyerConn, map[string]any{
		"type":            "send",
		"request_id":      "req-1",
		"conversation_id": conv.ID,
		"kind":            "text",
		"body":            "Hello across instances!",
		"client_msg_id":   "c-1",
	})

	// Seller receives real-time message from inst-a!
	sellerMsg := readJSONFrame(ctx, t, sellerConn)
	if sellerMsg["type"] != "message" || sellerMsg["instance_id"] != "ws-inst-a" {
		t.Fatalf("expected cross-instance message frame on seller, got %+v", sellerMsg)
	}
	msgPayload := sellerMsg["message"].(map[string]any)
	msgSeq := int64(msgPayload["seq"].(float64))
	if msgPayload["body"] != "Hello across instances!" {
		t.Fatalf("unexpected message body: %+v", msgPayload)
	}

	// Drain buyer's ack + broadcast echo (order between ack & pubsub message may vary).
	gotAck := false
	for i := 0; i < 2; i++ {
		f := readJSONFrame(ctx, t, buyerConn)
		if f["type"] == "ack" && f["request_id"] == "req-1" {
			gotAck = true
		}
	}
	if !gotAck {
		t.Fatal("buyer did not receive send ack")
	}

	// 7. Seller marks read on Instance B -> Buyer on Instance A receives real-time read_receipt!
	writeJSONFrame(ctx, t, sellerConn, map[string]any{
		"type":            "read",
		"request_id":      "r-1",
		"conversation_id": conv.ID,
		"last_read_seq":   msgSeq,
	})
	receipt := readJSONFrame(ctx, t, buyerConn)
	if receipt["type"] != "read_receipt" || receipt["reader_role"] != "seller" || int64(receipt["last_read_seq"].(float64)) != msgSeq {
		t.Fatalf("expected read_receipt on buyer, got %+v", receipt)
	}

	// 8. Intruder sends frame on conv.ID -> receives structured FORBIDDEN error frame without dropping socket!
	intruderConn, _, err := websocket.Dial(ctx, wsURLA, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + intruderTok}},
	})
	if err != nil {
		t.Fatalf("intruder dial: %v", err)
	}
	defer intruderConn.CloseNow()
	_ = readJSONFrame(ctx, t, intruderConn) // connected

	writeJSONFrame(ctx, t, intruderConn, map[string]any{
		"type":            "send",
		"request_id":      "bad-1",
		"conversation_id": conv.ID,
		"kind":            "text",
		"body":            "intrusion",
	})
	errFrame := readJSONFrame(ctx, t, intruderConn)
	if errFrame["type"] != "error" || errFrame["code"] != "FORBIDDEN" || errFrame["request_id"] != "bad-1" {
		t.Fatalf("expected FORBIDDEN error frame, got %+v", errFrame)
	}
	// Socket must remain alive after error!
	writeJSONFrame(ctx, t, intruderConn, map[string]any{"type": "ping", "request_id": "alive"})
	if f := readJSONFrame(ctx, t, intruderConn); f["type"] != "pong" {
		t.Fatalf("expected socket to stay alive after error frame, got %+v", f)
	}

	// 9. Reconnect catch-up via ?after=<cursor> returns missed messages immediately!
	sellerClaims, _ := jwtMgr.VerifyAccessToken(sellerTok)
	_, err = svcB.SendMessage(ctx, sellerClaims, service.SendMessageInput{
		ConversationID: conv.ID,
		Kind:           store.KindText,
		Body:           "Missed while offline",
	})
	if err != nil {
		t.Fatalf("send missed msg: %v", err)
	}

	// Reconnect buyer with ?after=<msgSeq> using Bearer JWT.
	_ = buyerJTI
	reconnectConn, _, err := websocket.Dial(ctx, wsURLA+"?after="+strconv.FormatInt(msgSeq, 10), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + buyerTok}},
	})
	if err != nil {
		t.Fatalf("reconnect dial: %v", err)
	}
	defer reconnectConn.CloseNow()

	_ = readJSONFrame(ctx, t, reconnectConn) // connected
	syncFrame := readJSONFrame(ctx, t, reconnectConn)
	if syncFrame["type"] != "sync_result" {
		t.Fatalf("expected sync_result after ?after= connect, got %+v", syncFrame)
	}
	items := syncFrame["messages"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["body"] != "Missed while offline" {
		t.Fatalf("expected 1 missed message, got %+v", items)
	}
}

func TestWebSocketOriginPolicyTicketVsCookie(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	const secret = "ws-origin-test-secret"
	jwtMgr, err := auth.NewManager(secret, auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("jwt manager: %v", err)
	}
	ticketMgr, err := ticket.NewManager(rdb, secret, 60*time.Second)
	if err != nil {
		t.Fatalf("ticket manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := store.NewMemStore()
	h := hub.New(rdb, "ws-origin-inst", nil, nil)
	go h.Run(ctx)
	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	if err := h.WaitSubscribed(waitCtx); err != nil {
		t.Fatalf("hub subscribe: %v", err)
	}

	svc := service.NewChatService(st, h, ticketMgr, nil, nil)
	handler := NewHandler(svc, h, jwtMgr, ticketMgr, Config{
		SiteRootDomain: "nexusmart.localhost",
		PingInterval:   2 * time.Second,
		ShutdownCtx:    ctx,
	}, nil)

	srv := httptest.NewServer(handler)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	buyerTok, _, _, _ := jwtMgr.SignAccessToken("buyer-origin", "buyer", "")
	buyerClaims, _ := jwtMgr.VerifyAccessToken(buyerTok)

	// 1. Direct ticket auth allows custom shop domain Origin (not in *.SITE_ROOT_DOMAIN).
	t1, _, err := svc.CreateWSTicket(ctx, buyerClaims)
	if err != nil {
		t.Fatalf("create ticket 1: %v", err)
	}
	conn1, _, err := websocket.Dial(ctx, wsURL+"?ticket="+t1, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://custom-brand.vn"}},
	})
	if err != nil {
		t.Fatalf("ticket auth with custom domain Origin should succeed, got err=%v", err)
	}
	_ = readJSONFrame(ctx, t, conn1)
	_ = conn1.CloseNow()

	// 2. Ticket auth with custom domain Origin still succeeds even when an ambient cookie is also sent.
	t2, _, err := svc.CreateWSTicket(ctx, buyerClaims)
	if err != nil {
		t.Fatalf("create ticket 2: %v", err)
	}
	conn2, _, err := websocket.Dial(ctx, wsURL+"?ticket="+t2, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{"https://another-custom-shop.example"},
			"Cookie": []string{"nm_at=" + buyerTok},
		},
	})
	if err != nil {
		t.Fatalf("ticket auth with ambient cookie and custom Origin should succeed, got err=%v", err)
	}
	_ = readJSONFrame(ctx, t, conn2)
	_ = conn2.CloseNow()

	// 3. Invalid ticket + valid cookie fails with 401 and must NOT fall back to cookie.
	if _, resp, err := websocket.Dial(ctx, wsURL+"?ticket=invalid-ticket", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{"http://shop.nexusmart.localhost:3300"},
			"Cookie": []string{"nm_at=" + buyerTok},
		},
	}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid ticket with valid cookie must return 401, got resp=%v err=%v", resp, err)
	}

	// 4. Cookie auth MUST reject untrusted / custom domain Origins with 403.
	for _, tc := range []struct {
		name   string
		cookie string
		origin string
	}{
		{name: "dev_cookie_evil_origin", cookie: "nm_at=" + buyerTok, origin: "https://evil.example"},
		{name: "host_cookie_custom_origin", cookie: "__Host-nm_at=" + buyerTok, origin: "https://custom-brand.vn"},
	} {
		if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Origin": []string{tc.origin},
				"Cookie": []string{tc.cookie},
			},
		}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: expected 403 Forbidden for untrusted Origin, got resp=%v err=%v", tc.name, resp, err)
		}
	}

	// 5. Cookie auth allows trusted *.SITE_ROOT_DOMAIN Origin.
	connCookieOK, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{"http://bach-dang.nexusmart.localhost:3300"},
			"Cookie": []string{"nm_at=" + buyerTok},
		},
	})
	if err != nil {
		t.Fatalf("cookie auth with *.SITE_ROOT_DOMAIN Origin should succeed, got err=%v", err)
	}
	_ = readJSONFrame(ctx, t, connCookieOK)
	_ = connCookieOK.CloseNow()

	// 6. Gateway-proxied cookie auth (Bearer + X-Chat-Auth-Method: cookie) enforces strict Origin check,
	// whereas gateway-proxied ticket auth (Bearer without cookie marker) allows custom domains.
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":         []string{"https://evil.example"},
			"Authorization":  []string{"Bearer " + buyerTok},
			authMethodHeader: []string{"cookie"},
		},
	}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("gateway-proxied cookie auth with evil Origin must return 403, got resp=%v err=%v", resp, err)
	}

	connProxyTicket, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":        []string{"https://custom-brand.vn"},
			"Authorization": []string{"Bearer " + buyerTok},
		},
	})
	if err != nil {
		t.Fatalf("gateway-proxied ticket/bearer auth with custom domain Origin should succeed, got err=%v", err)
	}
	_ = readJSONFrame(ctx, t, connProxyTicket)
	_ = connProxyTicket.CloseNow()
}

func TestWebSocketRevokedBeforeBlocksOldTokensAndAllowsFreshTokens(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	const secret = "ws-revoked-before-secret"
	jwtMgr, err := auth.NewManager(secret, auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("jwt manager: %v", err)
	}
	ticketMgr, err := ticket.NewManager(rdb, secret, 60*time.Second)
	if err != nil {
		t.Fatalf("ticket manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := store.NewMemStore()
	h := hub.New(rdb, "ws-rev-inst", nil, nil)
	go h.Run(ctx)
	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	if err := h.WaitSubscribed(waitCtx); err != nil {
		t.Fatalf("hub subscribe: %v", err)
	}

	svc := service.NewChatService(st, h, ticketMgr, nil, nil)
	handler := NewHandler(svc, h, jwtMgr, ticketMgr, Config{
		SiteRootDomain: "nexusmart.localhost",
		PingInterval:   2 * time.Second,
		ShutdownCtx:    ctx,
	}, nil)
	srv := httptest.NewServer(handler)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	oldTok, _, _, err := jwtMgr.SignAccessToken("buyer-locked", "buyer", "")
	if err != nil {
		t.Fatalf("sign old token: %v", err)
	}
	oldClaims, err := jwtMgr.VerifyAccessToken(oldTok)
	if err != nil {
		t.Fatalf("verify old token: %v", err)
	}
	oldTicket, _, err := svc.CreateWSTicket(ctx, oldClaims)
	if err != nil {
		t.Fatalf("create ticket before lock: %v", err)
	}

	// Simulate user lock / role change / password reset setting user:revoked_before:<uid> = iat.
	_ = mr.Set("user:revoked_before:buyer-locked", strconv.FormatInt(oldClaims.IssuedAt.Unix(), 10))

	// 1. Direct Bearer auth rejected with 401.
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + oldTok}},
	}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for Bearer JWT revoked via revoked_before, got resp=%v err=%v", resp, err)
	}

	// 2. Cookie auth rejected with 401.
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{"nm_at=" + oldTok}},
	}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for Cookie JWT revoked via revoked_before, got resp=%v err=%v", resp, err)
	}

	// 3. Previously minted WS ticket rejected with 401 on redeem.
	if _, resp, err := websocket.Dial(ctx, wsURL+"?ticket="+oldTicket, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for WS ticket revoked via revoked_before, got resp=%v err=%v", resp, err)
	}

	// 4. Token issued strictly AFTER revoked_before succeeds.
	_ = mr.Set("user:revoked_before:buyer-locked", strconv.FormatInt(oldClaims.IssuedAt.Unix()-10, 10))
	connOK, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + oldTok}},
	})
	if err != nil {
		t.Fatalf("expected token issued after revoked_before to connect, got %v", err)
	}
	_ = readJSONFrame(ctx, t, connOK)
	_ = connOK.CloseNow()
}
