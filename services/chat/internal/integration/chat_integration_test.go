package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/db"
	"github.com/nexusmart/core/services/chat/internal/grpcserver"
	"github.com/nexusmart/core/services/chat/internal/hub"
	chatmetrics "github.com/nexusmart/core/services/chat/internal/metrics"
	"github.com/nexusmart/core/services/chat/internal/service"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/nexusmart/core/services/chat/internal/validator"
	"github.com/nexusmart/core/services/chat/internal/ws"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type chatInstance struct {
	id     string
	hub    *hub.Hub
	svc    *service.ChatService
	grpc   *grpcserver.Server
	wsSrv  *httptest.Server
	wsURL  string
	cancel context.CancelFunc
}

func makePNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{G: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func dialWS(ctx context.Context, t *testing.T, targetURL, bearer string) (*websocket.Conn, map[string]any) {
	t.Helper()
	opts := &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled}
	if bearer != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + bearer}}
	}
	conn, _, err := websocket.Dial(ctx, targetURL, opts)
	if err != nil {
		t.Fatalf("dial %s: %v", targetURL, err)
	}
	frame := readFrame(ctx, t, conn)
	if frame["type"] != "connected" {
		t.Fatalf("expected connected frame, got %+v", frame)
	}
	return conn, frame
}

func readFrame(ctx context.Context, t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	rCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(rCtx)
	if err != nil {
		t.Fatalf("read ws frame: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal ws frame %q: %v", string(raw), err)
	}
	return m
}

func writeFrame(ctx context.Context, t *testing.T, conn *websocket.Conn, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	wCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(wCtx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write ws frame: %v", err)
	}
}

func TestChatIntegrationPostgresRedisAndTwoInstances(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CHAT_TEST_DATABASE_URL"))
	redisAddr := strings.TrimSpace(os.Getenv("CHAT_TEST_REDIS_ADDR"))
	if dsn == "" || redisAddr == "" {
		t.Skip("skipping integration test: CHAT_TEST_DATABASE_URL and CHAT_TEST_REDIS_ADDR must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// 1. EnsureDatabase + Migrate on real Postgres.
	if err := db.EnsureDatabase(ctx, dsn); err != nil {
		t.Fatalf("EnsureDatabase failed: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE chat_messages, chat_conversations, chat_shop_auto_replies RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate chat tables: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("CHAT_TEST_REDIS_PASSWORD"),
	})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping failed: %v", err)
	}

	// Local image server to test strict/hybrid probing + size/MIME enforcement alongside SSRF blocking.
	pngBytes := makePNGBytes(t)
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/valid.png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(len(pngBytes)))
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write(pngBytes)
		case "/oversized.png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(6*1024*1024))
			w.WriteHeader(http.StatusOK)
		case "/badmime.png":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>nope</html>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer imgSrv.Close()
	imgURL, _ := url.Parse(imgSrv.URL)

	const jwtSecret = "integration-chat-secret"
	jwtMgr, err := auth.NewManager(jwtSecret, auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("jwt manager: %v", err)
	}
	ticketMgr, err := ticket.NewManager(rdb, jwtSecret, 60*time.Second)
	if err != nil {
		t.Fatalf("ticket manager: %v", err)
	}

	pgStore := store.NewPGStore(pool)

	// Shared mutable clock for deterministic auto-reply testing.
	var clockMu sync.Mutex
	curTime := time.Date(2026, 11, 21, 16, 30, 0, 0, time.UTC) // 23:30 ICT (offline)
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curTime
	}

	startInstance := func(id string) *chatInstance {
		instCtx, instCancel := context.WithCancel(ctx)
		h := hub.New(rdb, id, chatmetrics.New(nil), nil)
		go h.Run(instCtx)
		waitCtx, waitCancel := context.WithTimeout(instCtx, 5*time.Second)
		if err := h.WaitSubscribed(waitCtx); err != nil {
			t.Fatalf("instance %s subscribe failed: %v", id, err)
		}
		waitCancel()

		imgVal := validator.NewImageValidator(validator.ImageValidatorConfig{
			Mode:         validator.ModeHybrid,
			MaxBytes:     5 * 1024 * 1024,
			AllowedHosts: []string{imgURL.Host},
		})
		svc := service.NewChatService(pgStore, h, ticketMgr, imgVal, nil,
			service.WithSiteRootDomain("nexusmart.localhost"),
			service.WithNowFunc(nowFn),
		)
		wsH := ws.NewHandler(svc, h, jwtMgr, ticketMgr, ws.Config{
			SiteRootDomain: "nexusmart.localhost",
			ShutdownCtx:    instCtx,
		}, nil)
		srv := httptest.NewServer(wsH)
		return &chatInstance{
			id:     id,
			hub:    h,
			svc:    svc,
			grpc:   grpcserver.New(svc),
			wsSrv:  srv,
			wsURL:  "ws" + strings.TrimPrefix(srv.URL, "http"),
			cancel: instCancel,
		}
	}

	instA := startInstance("chat-inst-a")
	defer func() { instA.cancel(); instA.wsSrv.Close() }()
	instB := startInstance("chat-inst-b")
	defer func() { instB.cancel(); instB.wsSrv.Close() }()

	// Mint JWTs.
	buyerTok, _, _, _ := jwtMgr.SignAccessToken("buyer-1", "buyer", "")
	otherBuyerTok, _, _, _ := jwtMgr.SignAccessToken("buyer-2", "buyer", "")
	sellerTok, _, _, _ := jwtMgr.SignAccessToken("seller-1", "seller", "shop-1")
	staffTok, _, _, _ := jwtMgr.SignAccessToken("staff-1", "seller", "shop-1")
	otherSellerTok, _, _, _ := jwtMgr.SignAccessToken("seller-2", "seller", "shop-2")

	buyerClaims, _ := jwtMgr.VerifyAccessToken(buyerTok)
	otherBuyerClaims, _ := jwtMgr.VerifyAccessToken(otherBuyerTok)
	sellerClaims, _ := jwtMgr.VerifyAccessToken(sellerTok)
	staffClaims, _ := jwtMgr.VerifyAccessToken(staffTok)
	otherSellerClaims, _ := jwtMgr.VerifyAccessToken(otherSellerTok)

	buyerCtx := auth.WithClaims(ctx, buyerClaims)
	otherBuyerCtx := auth.WithClaims(ctx, otherBuyerClaims)
	sellerCtx := auth.WithClaims(ctx, sellerClaims)
	staffCtx := auth.WithClaims(ctx, staffClaims)
	otherSellerCtx := auth.WithClaims(ctx, otherSellerClaims)

	// 2. Create conversation idempotently on instA.
	cResp, err := instA.grpc.CreateOrGetConversation(buyerCtx, &chatv1.CreateOrGetConversationRequest{
		ShopId:   "shop-1",
		SiteHost: "shop1.nexusmart.localhost",
	})
	if err != nil || !cResp.GetCreated() {
		t.Fatalf("create conversation failed: resp=%+v err=%v", cResp, err)
	}
	convID := cResp.GetConversation().GetId()

	// 3. Configure auto-reply for shop-1 (greeting + offline + keyword + cooldown).
	if _, err := instB.grpc.UpsertAutoReplyConfig(otherSellerCtx, &chatv1.UpsertAutoReplyConfigRequest{
		Config: &chatv1.AutoReplyConfig{ShopId: "shop-1", Enabled: true},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller must be denied auto-reply config update, got %v", err)
	}
	if _, err := instB.grpc.UpsertAutoReplyConfig(sellerCtx, &chatv1.UpsertAutoReplyConfigRequest{
		Config: &chatv1.AutoReplyConfig{
			ShopId:          "shop-1",
			Enabled:         true,
			GreetingMessage: "Chao mung den shop-1!",
			OfflineEnabled:  true,
			OfflineMessage:  "Shop dang ngoai gio lam viec.",
			WorkStartHour:   8,
			WorkEndHour:     22,
			Timezone:        "Asia/Ho_Chi_Minh",
			CooldownMinutes: 30,
			KeywordRules: []*chatv1.KeywordRule{
				{Keywords: []string{"bao hanh", "bảo hành"}, Reply: "San pham bao hanh 12 thang a!"},
			},
		},
	}); err != nil {
		t.Fatalf("upsert auto reply config: %v", err)
	}

	// 4. Test all 4 message kinds + image validation (valid / bad-mime / oversized / SSRF) + idempotency.
	textResp, err := instA.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
		Body:           "San pham nay bao hanh sao? Goi 0901234567",
		ClientMsgId:    "idem-1",
	})
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if !strings.Contains(textResp.GetMessage().GetMetadataJson(), "external_transaction_warning") {
		t.Fatalf("expected external_transaction_warning metadata, got %s", textResp.GetMessage().GetMetadataJson())
	}
	if textResp.GetAutoReply() == nil || textResp.GetAutoReply().GetBody() != "San pham bao hanh 12 thang a!" {
		t.Fatalf("expected keyword auto-reply, got %+v", textResp.GetAutoReply())
	}

	// Idempotent retry returns exact same message without duplicate auto-reply.
	dupResp, err := instB.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
		Body:           "San pham nay bao hanh sao? Goi 0901234567",
		ClientMsgId:    "idem-1",
	})
	if err != nil || dupResp.GetMessage().GetId() != textResp.GetMessage().GetId() || dupResp.GetAutoReply() != nil {
		t.Fatalf("idempotent retry mismatch: dup=%+v orig=%+v err=%v", dupResp, textResp, err)
	}

	// Image: valid probed PNG succeeds; bad-mime, oversized, and SSRF fail with InvalidArgument.
	imgResp, err := instA.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_IMAGE,
		Body:           imgSrv.URL + "/valid.png",
	})
	if err != nil || !strings.Contains(imgResp.GetMessage().GetMetadataJson(), "image/png") {
		t.Fatalf("valid image message failed: resp=%+v err=%v", imgResp, err)
	}
	for name, badURL := range map[string]string{
		"oversized": imgSrv.URL + "/oversized.png",
		"badmime":   imgSrv.URL + "/badmime.png",
		"ssrf":      "http://169.254.169.254/latest/meta-data/pic.png",
		"svg":       "https://cdn.example.com/x.svg",
	} {
		if _, err := instA.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
			ConversationId: convID,
			Kind:           chatv1.MessageKind_MESSAGE_KIND_IMAGE,
			Body:           badURL,
		}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("image case %s: expected InvalidArgument, got %v", name, err)
		}
	}

	// Product card (from shop staff) & Order reference (from buyer).
	prodResp, err := instB.grpc.SendMessage(staffCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_PRODUCT,
		RefId:          "prod-100",
		Body:           "Dien thoai XYZ",
	})
	if err != nil || prodResp.GetMessage().GetSenderRole() != store.RoleSeller {
		t.Fatalf("staff product card failed: resp=%+v err=%v", prodResp, err)
	}
	ordResp, err := instA.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_ORDER,
		RefId:          "ORD-2026-999",
		Body:           "Check don nay giup minh",
	})
	if err != nil || ordResp.GetMessage().GetRefId() != "ORD-2026-999" {
		t.Fatalf("order ref message failed: resp=%+v err=%v", ordResp, err)
	}

	// 5. Auto-reply offline after cooldown & greeting during business hours.
	clockMu.Lock()
	curTime = curTime.Add(35 * time.Minute) // 00:05 ICT -> still offline, cooldown elapsed
	clockMu.Unlock()
	offlineResp, err := instA.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
		Body:           "Alo shop",
	})
	if err != nil || offlineResp.GetAutoReply() == nil || offlineResp.GetAutoReply().GetBody() != "Shop dang ngoai gio lam viec." {
		t.Fatalf("expected offline auto-reply, got %+v err=%v", offlineResp, err)
	}

	clockMu.Lock()
	curTime = curTime.Add(10 * time.Hour) // 10:05 ICT -> work hours, cooldown elapsed
	clockMu.Unlock()
	greetResp, err := instB.grpc.SendMessage(buyerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
		Body:           "Xin chao",
	})
	if err != nil || greetResp.GetAutoReply() == nil || greetResp.GetAutoReply().GetBody() != "Chao mung den shop-1!" {
		t.Fatalf("expected greeting auto-reply, got %+v err=%v", greetResp, err)
	}

	// 6. Strict permissions: unrelated buyer and other shop seller rejected on gRPC.
	for name, cCtx := range map[string]context.Context{"otherBuyer": otherBuyerCtx, "otherSeller": otherSellerCtx} {
		if _, err := instA.grpc.ListMessages(cCtx, &chatv1.ListMessagesRequest{ConversationId: convID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s ListMessages: expected PermissionDenied, got %v", name, err)
		}
		if _, err := instB.grpc.SendMessage(cCtx, &chatv1.SendMessageRequest{ConversationId: convID, Kind: chatv1.MessageKind_MESSAGE_KIND_TEXT, Body: "nope"}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s SendMessage: expected PermissionDenied, got %v", name, err)
		}
	}

	// 7. Cursor pagination (before & after, numeric seq & UUID).
	pageLatest, err := instA.grpc.ListMessages(buyerCtx, &chatv1.ListMessagesRequest{ConversationId: convID, Limit: 3})
	if err != nil || len(pageLatest.GetMessages()) != 3 || !pageLatest.GetHasMore() {
		t.Fatalf("list latest page failed: resp=%+v err=%v", pageLatest, err)
	}
	pageOlder, err := instA.grpc.ListMessages(buyerCtx, &chatv1.ListMessagesRequest{
		ConversationId: convID,
		Before:         pageLatest.GetNextBefore(),
		Limit:          3,
	})
	if err != nil || len(pageOlder.GetMessages()) != 3 {
		t.Fatalf("list older page failed: resp=%+v err=%v", pageOlder, err)
	}
	// Sync using UUID cursor.
	syncAfterUUID, err := instB.grpc.ListMessages(sellerCtx, &chatv1.ListMessagesRequest{
		ConversationId: convID,
		After:          pageOlder.GetMessages()[2].GetId(),
		Limit:          10,
	})
	if err != nil || len(syncAfterUUID.GetMessages()) != 3 {
		t.Fatalf("sync after uuid failed: len=%d err=%v", len(syncAfterUUID.GetMessages()), err)
	}
	lastKnownSeq := pageLatest.GetMessages()[2].GetSeq()

	// 8. Two real Chat instances sharing Redis Pub/Sub over live WebSockets + cross-instance WS ticket!
	tResp, err := instA.grpc.CreateWSTicket(buyerCtx, &chatv1.CreateWSTicketRequest{})
	if err != nil {
		t.Fatalf("create ticket on instA: %v", err)
	}
	// Redeem ticket on instA WS, connect seller to instB WS.
	buyerWS, bConnFrame := dialWS(ctx, t, instA.wsURL+"?ticket="+tResp.GetTicket(), "")
	defer buyerWS.CloseNow()
	if bConnFrame["instance_id"] != "chat-inst-a" {
		t.Fatalf("expected buyer on chat-inst-a, got %+v", bConnFrame)
	}

	// Ticket replay on instB MUST fail!
	if _, err := instB.grpc.ExchangeWSTicket(ctx, &chatv1.ExchangeWSTicketRequest{Ticket: tResp.GetTicket()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected ticket replay on instB to fail with Unauthenticated, got %v", err)
	}

	sellerWS, sConnFrame := dialWS(ctx, t, instB.wsURL, sellerTok)
	defer sellerWS.CloseNow()
	if sConnFrame["instance_id"] != "chat-inst-b" {
		t.Fatalf("expected seller on chat-inst-b, got %+v", sConnFrame)
	}

	// Buyer sends live message on instA -> Seller receives on instB in real time!
	writeFrame(ctx, t, buyerWS, map[string]any{
		"type":            "send",
		"request_id":      "rt-1",
		"conversation_id": convID,
		"kind":            "text",
		"body":            "Realtime from instA to instB!",
		"client_msg_id":   "rt-msg-1",
	})

	sellerRecv := readFrame(ctx, t, sellerWS)
	if sellerRecv["type"] != "message" || sellerRecv["instance_id"] != "chat-inst-a" {
		t.Fatalf("expected realtime message from chat-inst-a on sellerWS, got %+v", sellerRecv)
	}
	rtMsg := sellerRecv["message"].(map[string]any)
	rtSeq := int64(rtMsg["seq"].(float64))
	if rtMsg["body"] != "Realtime from instA to instB!" {
		t.Fatalf("unexpected realtime body: %+v", rtMsg)
	}

	// Drain buyer ack + self-broadcast.
	for i := 0; i < 2; i++ {
		_ = readFrame(ctx, t, buyerWS)
	}

	// Seller marks read on instB -> Buyer receives read_receipt on instA + unread drops to 0!
	writeFrame(ctx, t, sellerWS, map[string]any{
		"type":            "read",
		"request_id":      "read-1",
		"conversation_id": convID,
		"last_read_seq":   rtSeq,
	})
	buyerReceipt := readFrame(ctx, t, buyerWS)
	if buyerReceipt["type"] != "read_receipt" || buyerReceipt["instance_id"] != "chat-inst-b" || int64(buyerReceipt["last_read_seq"].(float64)) != rtSeq {
		t.Fatalf("expected cross-instance read_receipt on buyerWS, got %+v", buyerReceipt)
	}
	// Drain seller's read_ack + read_receipt.
	for i := 0; i < 2; i++ {
		_ = readFrame(ctx, t, sellerWS)
	}

	// Seller replies on instB -> Buyer receives on instA!
	writeFrame(ctx, t, sellerWS, map[string]any{
		"type":            "send",
		"request_id":      "rt-2",
		"conversation_id": convID,
		"kind":            "text",
		"body":            "Reply from instB to instA!",
	})
	buyerRecv := readFrame(ctx, t, buyerWS)
	if buyerRecv["type"] != "message" || buyerRecv["instance_id"] != "chat-inst-b" {
		t.Fatalf("expected realtime reply from chat-inst-b on buyerWS, got %+v", buyerRecv)
	}

	// 9. Reconnect catch-up over WS ?after=<lastKnownSeq> fetches BOTH missed cross-instance messages in seq ASC order!
	reconnWS, _ := dialWS(ctx, t, instA.wsURL+"?after="+strconv.FormatInt(lastKnownSeq, 10), buyerTok)
	defer reconnWS.CloseNow()
	syncRes := readFrame(ctx, t, reconnWS)
	if syncRes["type"] != "sync_result" {
		t.Fatalf("expected sync_result on reconnect, got %+v", syncRes)
	}
	syncedMsgs := syncRes["messages"].([]any)
	if len(syncedMsgs) != 2 {
		t.Fatalf("expected 2 missed messages since seq %d, got %d (%+v)", lastKnownSeq, len(syncedMsgs), syncedMsgs)
	}
	if syncedMsgs[0].(map[string]any)["body"] != "Realtime from instA to instB!" ||
		syncedMsgs[1].(map[string]any)["body"] != "Reply from instB to instA!" {
		t.Fatalf("unexpected reconnect sync order/bodies: %+v", syncedMsgs)
	}
}
