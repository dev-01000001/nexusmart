package router

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeChatClient struct {
	chatv1.ChatServiceClient

	mu sync.Mutex

	conversations     map[string]*chatv1.Conversation
	messages          map[string][]*chatv1.ChatMessage
	tickets           map[string]*chatv1.ExchangeWSTicketResponse
	autoReplies       map[string]*chatv1.AutoReplyConfig
	createConvCreated bool
	sendAutoReply     *chatv1.ChatMessage

	lastMD                metadata.MD
	lastListConvReq       *chatv1.ListConversationsRequest
	lastCreateConvReq     *chatv1.CreateOrGetConversationRequest
	lastGetConvReq        *chatv1.GetConversationRequest
	lastListMsgReq        *chatv1.ListMessagesRequest
	lastSendMsgReq        *chatv1.SendMessageRequest
	lastMarkReadReq       *chatv1.MarkReadRequest
	lastExchangeTicketReq *chatv1.ExchangeWSTicketRequest
	lastGetAutoReplyReq   *chatv1.GetAutoReplyConfigRequest
	lastUpsertAutoReq     *chatv1.UpsertAutoReplyConfigRequest

	sendMessageCalls    int
	markReadCalls       int
	exchangeTicketCalls int
}

func (f *fakeChatClient) recordMD(ctx context.Context) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
}

func (f *fakeChatClient) CreateOrGetConversation(ctx context.Context, in *chatv1.CreateOrGetConversationRequest, _ ...grpc.CallOption) (*chatv1.CreateOrGetConversationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.lastCreateConvReq = in
	now := timestamppb.New(time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC))
	conv := &chatv1.Conversation{
		Id:        "conv-created",
		BuyerId:   "buyer-1",
		ShopId:    in.GetShopId(),
		SiteHost:  in.GetSiteHost(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	resp := &chatv1.CreateOrGetConversationResponse{
		Conversation: conv,
		Created:      f.createConvCreated,
	}
	if in.GetInitialMessage() != "" {
		resp.InitialMessage = &chatv1.ChatMessage{
			Id:             "msg-init",
			Seq:            1,
			ConversationId: conv.GetId(),
			SenderId:       "buyer-1",
			SenderRole:     "buyer",
			Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
			Body:           in.GetInitialMessage(),
			CreatedAt:      now,
		}
	}
	return resp, nil
}

func (f *fakeChatClient) GetConversation(ctx context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.GetConversationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.lastGetConvReq = in
	conv, ok := f.conversations[in.GetConversationId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "conversation not found")
	}
	return &chatv1.GetConversationResponse{Conversation: conv}, nil
}

func (f *fakeChatClient) ListConversations(ctx context.Context, in *chatv1.ListConversationsRequest, _ ...grpc.CallOption) (*chatv1.ListConversationsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.lastListConvReq = in
	items := make([]*chatv1.Conversation, 0, len(f.conversations))
	var totalUnread int64
	for _, c := range f.conversations {
		if in.GetShopId() != "" && c.GetShopId() != in.GetShopId() {
			continue
		}
		items = append(items, c)
		totalUnread += c.GetUnreadCount()
	}
	return &chatv1.ListConversationsResponse{
		Conversations: items,
		Total:         int64(len(items)),
		TotalUnread:   totalUnread,
	}, nil
}

func (f *fakeChatClient) ListMessages(ctx context.Context, in *chatv1.ListMessagesRequest, _ ...grpc.CallOption) (*chatv1.ListMessagesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.lastListMsgReq = in
	// Deliberately return conversation & messages without backend authorization checks
	// so tests prove that the gateway itself enforces object-level access!
	conv, ok := f.conversations[in.GetConversationId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "conversation not found")
	}
	msgs := f.messages[in.GetConversationId()]
	return &chatv1.ListMessagesResponse{
		Conversation: conv,
		Messages:     msgs,
		NextBefore:   "1",
		LatestCursor: "4",
		HasMore:      true,
	}, nil
}

func (f *fakeChatClient) SendMessage(ctx context.Context, in *chatv1.SendMessageRequest, _ ...grpc.CallOption) (*chatv1.SendMessageResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.sendMessageCalls++
	f.lastSendMsgReq = in
	now := timestamppb.New(time.Date(2026, 11, 21, 8, 5, 0, 0, time.UTC))
	return &chatv1.SendMessageResponse{
		Message: &chatv1.ChatMessage{
			Id:             "msg-new",
			Seq:            10,
			ConversationId: in.GetConversationId(),
			SenderId:       "buyer-1",
			SenderRole:     "buyer",
			Kind:           in.GetKind(),
			Body:           in.GetBody(),
			RefId:          in.GetRefId(),
			MetadataJson:   in.GetMetadataJson(),
			ClientMsgId:    in.GetClientMsgId(),
			CreatedAt:      now,
		},
		AutoReply: f.sendAutoReply,
	}, nil
}

func (f *fakeChatClient) MarkRead(ctx context.Context, in *chatv1.MarkReadRequest, _ ...grpc.CallOption) (*chatv1.MarkReadResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.markReadCalls++
	f.lastMarkReadReq = in
	seq := in.GetLastReadSeq()
	if seq == 0 {
		seq = 10
	}
	return &chatv1.MarkReadResponse{
		ConversationId: in.GetConversationId(),
		LastReadSeq:    seq,
		UnreadCount:    0,
	}, nil
}

func (f *fakeChatClient) CreateWSTicket(ctx context.Context, _ *chatv1.CreateWSTicketRequest, _ ...grpc.CallOption) (*chatv1.CreateWSTicketResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	return &chatv1.CreateWSTicketResponse{
		Ticket:    "ticket-abc-123",
		ExpiresAt: timestamppb.New(time.Date(2026, 11, 21, 8, 1, 0, 0, time.UTC)),
	}, nil
}

func (f *fakeChatClient) ExchangeWSTicket(ctx context.Context, in *chatv1.ExchangeWSTicketRequest, _ ...grpc.CallOption) (*chatv1.ExchangeWSTicketResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.exchangeTicketCalls++
	f.lastExchangeTicketReq = in
	resp, ok := f.tickets[in.GetTicket()]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired ticket")
	}
	return resp, nil
}

func (f *fakeChatClient) GetAutoReplyConfig(ctx context.Context, in *chatv1.GetAutoReplyConfigRequest, _ ...grpc.CallOption) (*chatv1.GetAutoReplyConfigResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.lastGetAutoReplyReq = in
	cfg, ok := f.autoReplies[in.GetShopId()]
	if !ok {
		cfg = &chatv1.AutoReplyConfig{
			ShopId:          in.GetShopId(),
			Enabled:         true,
			GreetingMessage: "Xin chao!",
			Timezone:        "Asia/Ho_Chi_Minh",
			CooldownMinutes: 30,
		}
	}
	return &chatv1.GetAutoReplyConfigResponse{Config: cfg}, nil
}

func (f *fakeChatClient) UpsertAutoReplyConfig(ctx context.Context, in *chatv1.UpsertAutoReplyConfigRequest, _ ...grpc.CallOption) (*chatv1.UpsertAutoReplyConfigResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordMD(ctx)
	f.lastUpsertAutoReq = in
	cfg := in.GetConfig()
	if f.autoReplies == nil {
		f.autoReplies = make(map[string]*chatv1.AutoReplyConfig)
	}
	f.autoReplies[cfg.GetShopId()] = cfg
	return &chatv1.UpsertAutoReplyConfigResponse{Config: cfg}, nil
}

type fakeRevocationChecker struct {
	mu      sync.RWMutex
	revoked map[string]bool
}

func (r *fakeRevocationChecker) IsJTIRevoked(_ context.Context, jti string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revoked[jti], nil
}

func TestChatRESTEndpointsAndObjectPermissions(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-chat-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	authMgr, err := auth.NewManager("chat-router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	otherBuyerTok, _, _, _ := authMgr.SignAccessToken("buyer-2", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-42")
	otherSellerTok, _, _, _ := authMgr.SignAccessToken("seller-2", "seller", "shop-99")
	sellerNoShopTok, _, _, _ := authMgr.SignAccessToken("seller-empty", "seller", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	now := timestamppb.New(time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC))
	chatMock := &fakeChatClient{
		conversations: map[string]*chatv1.Conversation{
			"conv-1": {
				Id:                    "conv-1",
				BuyerId:               "buyer-1",
				ShopId:                "shop-42",
				SiteHost:              "shop42.nexusmart.local",
				LastMessageId:         "msg-4",
				LastMessageSeq:        4,
				LastMessagePreview:    "Order #NM-100",
				LastMessageSenderRole: "buyer",
				LastMessageAt:         now,
				BuyerLastReadSeq:      4,
				SellerLastReadSeq:     2,
				UnreadCount:           2,
				CreatedAt:             now,
				UpdatedAt:             now,
			},
			"conv-other": {
				Id:        "conv-other",
				BuyerId:   "buyer-2",
				ShopId:    "shop-99",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		messages: map[string][]*chatv1.ChatMessage{
			"conv-1": {
				{
					Id:             "msg-1",
					Seq:            1,
					ConversationId: "conv-1",
					SenderId:       "buyer-1",
					SenderRole:     "buyer",
					Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
					Body:           "Hello shop",
					ClientMsgId:    "c-1",
					CreatedAt:      now,
				},
				{
					Id:             "msg-2",
					Seq:            2,
					ConversationId: "conv-1",
					SenderId:       "buyer-1",
					SenderRole:     "buyer",
					Kind:           chatv1.MessageKind_MESSAGE_KIND_IMAGE,
					Body:           "https://cdn.example.com/img.png",
					MetadataJson:   `{"content_type":"image/png","size_bytes":1024}`,
					CreatedAt:      now,
				},
				{
					Id:             "msg-3",
					Seq:            3,
					ConversationId: "conv-1",
					SenderId:       "seller-1",
					SenderRole:     "seller",
					Kind:           chatv1.MessageKind_MESSAGE_KIND_PRODUCT,
					Body:           "Check this product",
					RefId:          "prod-123",
					CreatedAt:      now,
				},
				{
					Id:             "msg-4",
					Seq:            4,
					ConversationId: "conv-1",
					SenderId:       "buyer-1",
					SenderRole:     "buyer",
					Kind:           chatv1.MessageKind_MESSAGE_KIND_ORDER,
					Body:           "Order #NM-100",
					RefId:          "ord-100",
					CreatedAt:      now,
				},
			},
		},
		sendAutoReply: &chatv1.ChatMessage{
			Id:             "msg-auto",
			Seq:            11,
			ConversationId: "conv-1",
			SenderId:       "shop:shop-42",
			SenderRole:     "seller",
			Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
			Body:           "Thanks for reaching out!",
			IsAutoReply:    true,
			CreatedAt:      now,
		},
	}

	restore := setChatTestUpstream(chatMock, "")
	t.Cleanup(restore)

	log := logger.New("gateway-chat-test", logger.WithWriter(io.Discard))
	reg := prometheus.NewRegistry()
	red := metrics.NewRED("gateway-chat-test", reg)
	ops := metrics.NewOpsHandler("gateway-chat-test", nil, reg)
	productMock := &fakeProductClient{}
	paymentMock := &fakePaymentClient{
		payments: map[string]*paymentv1.Payment{
			"pay-1": {PaymentId: "pay-1", UserId: "buyer-1", AmountVnd: 100000},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-chat-test",
		Clients: &clients.Clients{
			Product: productMock,
			Payment: paymentMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		RED:         red,
		OpsHandler:  ops,
		Logger:      log,
	})

	// 1. Unauthenticated REST request -> 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", rec.Code)
	}

	// 2. GET /api/v1/chat/conversations forwards query params & Bearer JWT.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations?scope=buyer&shop_id=shop-42&unreplied_only=true&page=2&page_size=15", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list conversations, got %d (%s)", rec.Code, rec.Body.String())
	}
	var listConvResp struct {
		Items       []chatConversationDTO `json:"items"`
		Total       int64                 `json:"total"`
		TotalUnread int64                 `json:"total_unread"`
		Page        int32                 `json:"page"`
		PageSize    int32                 `json:"page_size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listConvResp); err != nil {
		t.Fatalf("unmarshal list conversations: %v", err)
	}
	if len(listConvResp.Items) != 1 || listConvResp.Total != 1 || listConvResp.TotalUnread != 2 || listConvResp.Page != 2 || listConvResp.PageSize != 15 {
		t.Fatalf("unexpected list conversations payload: %+v", listConvResp)
	}
	if chatMock.lastListConvReq.GetScope() != "buyer" || !chatMock.lastListConvReq.GetUnrepliedOnly() {
		t.Fatalf("unexpected gRPC ListConversations request: %+v", chatMock.lastListConvReq)
	}
	if vals := chatMock.lastMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+buyerTok {
		t.Fatalf("expected Bearer JWT forwarded to chat gRPC, got %+v", chatMock.lastMD)
	}

	// 2b. ListConversations defense-in-depth:
	// - unfiltered query strips unauthorized conversations (`conv-other`) returned by backend mock.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on unfiltered list conversations, got %d (%s)", rec.Code, rec.Body.String())
	}
	var filteredResp struct {
		Items []chatConversationDTO `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &filteredResp); err != nil {
		t.Fatalf("unmarshal filtered list conversations: %v", err)
	}
	if len(filteredResp.Items) != 1 || filteredResp.Items[0].ID != "conv-1" {
		t.Fatalf("expected gateway to filter out unauthorized conv-other, got %+v", filteredResp.Items)
	}

	// - scope=seller blocked with 403 for buyer (no shop_id) and for seller requesting another shop_id.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations?scope=seller", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
		t.Fatalf("expected 403 for buyer requesting scope=seller, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations?scope=seller&shop_id=shop-99", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
		t.Fatalf("expected 403 for seller requesting another shop_id with scope=seller, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. POST /api/v1/chat/conversations validates shop_id and returns 201 vs 200.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations", strings.NewReader(`{"shop_id":"   "}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on empty shop_id, got %d", rec.Code)
	}

	chatMock.createConvCreated = true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations", strings.NewReader(`{"shop_id":"shop-42","site_host":"shop42.nexusmart.local","initial_message":"Hi there"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when conversation created, got %d (%s)", rec.Code, rec.Body.String())
	}
	var createResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create conv: %v", err)
	}
	if createResp["created"] != true || createResp["initial_message"] == nil {
		t.Fatalf("expected created=true and initial_message present, got %+v", createResp)
	}

	chatMock.createConvCreated = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations", strings.NewReader(`{"shop_id":"shop-42"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when existing conversation returned, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 4. GET /api/v1/chat/conversations/{id}/messages: buyer & matching shop allowed; others 403 at gateway!
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations/conv-1/messages?before=10&after=0&limit=25", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for buyer listing messages, got %d (%s)", rec.Code, rec.Body.String())
	}
	var listMsgResp struct {
		Conversation *chatConversationDTO `json:"conversation"`
		Items        []map[string]any     `json:"items"`
		NextBefore   string               `json:"next_before"`
		LatestCursor string               `json:"latest_cursor"`
		HasMore      bool                 `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listMsgResp); err != nil {
		t.Fatalf("unmarshal list messages: %v", err)
	}
	if len(listMsgResp.Items) != 4 || listMsgResp.NextBefore != "1" || listMsgResp.LatestCursor != "4" || !listMsgResp.HasMore {
		t.Fatalf("unexpected list messages payload: %+v", listMsgResp)
	}
	wantKinds := []string{"text", "image", "product", "order"}
	for i, wantKind := range wantKinds {
		gotKind, _ := listMsgResp.Items[i]["kind"].(string)
		if gotKind != wantKind {
			t.Fatalf("message[%d] kind=%q, want %q", i, gotKind, wantKind)
		}
		for _, field := range []string{"id", "seq", "conversation_id", "sender_id", "sender_role", "kind", "body", "ref_id", "metadata", "is_auto_reply", "client_msg_id", "created_at"} {
			if _, exists := listMsgResp.Items[i][field]; !exists {
				t.Fatalf("message[%d] missing required contract field %q: %+v", i, field, listMsgResp.Items[i])
			}
		}
	}

	// Matching seller/staff succeeds.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations/conv-1/messages", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for matching shop seller listing messages, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Unrelated buyer, different-shop seller, and unrelated admin MUST get 403 PERMISSION_DENIED at gateway.
	for name, tok := range map[string]string{
		"unrelated_buyer": otherBuyerTok,
		"other_shop":      otherSellerTok,
		"unrelated_admin": adminTok,
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations/conv-1/messages", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
			t.Fatalf("%s: expected 403 PERMISSION_DENIED on ListMessages, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// 5. POST /api/v1/chat/conversations/{id}/messages: gateway enforces access BEFORE SendMessage.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/messages", strings.NewReader(`{"kind":"image","body":"https://cdn.example.com/a.jpg","metadata":{"content_type":"image/jpeg","size_bytes":2048},"client_msg_id":"cm-1"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for buyer sending message, got %d (%s)", rec.Code, rec.Body.String())
	}
	if chatMock.lastSendMsgReq.GetKind() != chatv1.MessageKind_MESSAGE_KIND_IMAGE || !strings.Contains(chatMock.lastSendMsgReq.GetMetadataJson(), `"image/jpeg"`) {
		t.Fatalf("unexpected SendMessage gRPC request: %+v", chatMock.lastSendMsgReq)
	}
	if !strings.Contains(rec.Body.String(), `"auto_reply"`) || !strings.Contains(rec.Body.String(), `"is_auto_reply":true`) {
		t.Fatalf("expected auto_reply in response, got %s", rec.Body.String())
	}

	// Matching seller sending product card succeeds.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/messages", strings.NewReader(`{"kind":"product","body":"Hot deal","ref_id":"prod-9"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || chatMock.lastSendMsgReq.GetKind() != chatv1.MessageKind_MESSAGE_KIND_PRODUCT {
		t.Fatalf("expected 201 for matching shop seller sending message, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Invalid kind and invalid metadata rejected with 400.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/messages", strings.NewReader(`{"kind":"video","body":"nope"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid message kind, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/messages", strings.NewReader(`{"kind":"text","body":"hi","metadata":[1,2]}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on non-object metadata, got %d", rec.Code)
	}

	// Unauthorized callers blocked at gateway BEFORE SendMessage is ever invoked!
	sendCountBefore := chatMock.sendMessageCalls
	for name, tok := range map[string]string{
		"unrelated_buyer": otherBuyerTok,
		"other_shop":      otherSellerTok,
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/messages", strings.NewReader(`{"kind":"text","body":"intrusion"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
			t.Fatalf("%s: expected 403 PERMISSION_DENIED on SendMessage, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	if chatMock.sendMessageCalls != sendCountBefore {
		t.Fatalf("SendMessage must not be invoked when gateway permission check fails; calls before=%d after=%d", sendCountBefore, chatMock.sendMessageCalls)
	}

	// 6. POST /api/v1/chat/conversations/{id}/read: buyer & seller allowed; others 403 BEFORE MarkRead.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/read", strings.NewReader(`{"last_read_seq":4,"message_id":"msg-4"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"last_read_seq":4`) {
		t.Fatalf("expected 200 on MarkRead, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Empty body allowed on MarkRead.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/read", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for seller MarkRead with empty body, got %d (%s)", rec.Code, rec.Body.String())
	}

	markReadCountBefore := chatMock.markReadCalls
	for name, tok := range map[string]string{
		"unrelated_buyer": otherBuyerTok,
		"other_shop":      otherSellerTok,
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/conv-1/read", strings.NewReader(`{"last_read_seq":4}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
			t.Fatalf("%s: expected 403 PERMISSION_DENIED on MarkRead, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	if chatMock.markReadCalls != markReadCountBefore {
		t.Fatalf("MarkRead must not be invoked when gateway permission check fails; calls before=%d after=%d", markReadCountBefore, chatMock.markReadCalls)
	}

	// 7. POST /api/v1/chat/ws-ticket -> 201 {ticket, expires_at}.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat/ws-ticket", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"ticket":"ticket-abc-123"`) || !strings.Contains(rec.Body.String(), `"expires_at":"2026-11-21T08:01:00Z"`) {
		t.Fatalf("expected 201 ws-ticket response, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 8. Seller chat routes enforce RequireRoles("seller") + RequireSellerShop() + JWT shop_id binding.
	for _, ep := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/seller/chat/conversations", ""},
		{http.MethodGet, "/api/v1/seller/chat/auto-reply", ""},
		{http.MethodPut, "/api/v1/seller/chat/auto-reply", `{"enabled":true}`},
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("buyer calling %s %s: expected 403, got %d", ep.method, ep.path, rec.Code)
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
		req.Header.Set("Authorization", "Bearer "+sellerNoShopTok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("seller without shop calling %s %s: expected 403, got %d", ep.method, ep.path, rec.Code)
		}
	}

	// Seller conversations forces scope="seller" and shop_id from JWT even if query attempts spoofing.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/seller/chat/conversations?scope=buyer&shop_id=shop-999&unreplied_only=true", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on seller chat conversations, got %d (%s)", rec.Code, rec.Body.String())
	}
	if chatMock.lastListConvReq.GetScope() != "seller" || chatMock.lastListConvReq.GetShopId() != "shop-42" || !chatMock.lastListConvReq.GetUnrepliedOnly() {
		t.Fatalf("expected seller scope & JWT shop_id=shop-42, got %+v", chatMock.lastListConvReq)
	}

	// Seller GET & PUT auto-reply bind shop_id from JWT, ignoring body shop_id.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/seller/chat/auto-reply", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || chatMock.lastGetAutoReplyReq.GetShopId() != "shop-42" {
		t.Fatalf("expected 200 and shop_id=shop-42 on get auto-reply, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/seller/chat/auto-reply", strings.NewReader(`{
		"shop_id": "shop-spoofed",
		"enabled": true,
		"greeting_message": "Welcome to Shop 42!",
		"offline_enabled": true,
		"offline_message": "We are offline",
		"work_start_hour": 8,
		"work_end_hour": 22,
		"timezone": "Asia/Ho_Chi_Minh",
		"cooldown_minutes": 15,
		"keyword_rules": [{"keywords":["ship","gia"],"reply":"Free shipping over 200k"}]
	}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on put auto-reply, got %d (%s)", rec.Code, rec.Body.String())
	}
	if chatMock.lastUpsertAutoReq.GetConfig().GetShopId() != "shop-42" {
		t.Fatalf("expected JWT shop_id=shop-42 forced on UpsertAutoReplyConfig, got %q", chatMock.lastUpsertAutoReq.GetConfig().GetShopId())
	}
	if len(chatMock.lastUpsertAutoReq.GetConfig().GetKeywordRules()) != 1 {
		t.Fatalf("expected 1 keyword rule forwarded, got %+v", chatMock.lastUpsertAutoReq.GetConfig().GetKeywordRules())
	}

	// 9. Verify existing gateway routes are NOT shadowed by mountChatRoutes.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/products", strings.NewReader(`{"name":"Phone","category_id":"cat-1"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || productMock.lastCreateReq.GetShopId() != "shop-42" {
		t.Fatalf("existing /api/v1/seller/products shadowed/broken: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-1", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("existing /api/v1/payments/{id} shadowed/broken: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

type capturedWSUpgrade struct {
	Path        string
	RawQuery    string
	AuthHeader  string
	AuthMethod  string
	Origin      string
	Cookie      string
	Upgrade     string
	Connection  string
	WSKey       string
	WSVersion   string
	Traceparent string
}

func TestChatWebSocketAuthAnd101Tunnel(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-chat-ws-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	authMgr, err := auth.NewManager("chat-ws-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-42")
	revokedTok, revokedJTI, _, _ := authMgr.SignAccessToken("buyer-revoked", "buyer", "")
	wsTicketTok, _, _, _ := authMgr.SignAccessToken("buyer-ticket", "buyer", "")

	revoker := &fakeRevocationChecker{
		revoked: map[string]bool{
			revokedJTI:           true,
			"revoked-ticket-jti": true,
		},
	}

	chatMock := &fakeChatClient{
		tickets: map[string]*chatv1.ExchangeWSTicketResponse{
			"valid-ticket": {
				UserId:  "buyer-ticket",
				Role:    "buyer",
				Jti:     "active-ticket-jti",
				WsToken: wsTicketTok,
			},
			"revoked-ticket": {
				UserId:  "buyer-revoked",
				Role:    "buyer",
				Jti:     "revoked-ticket-jti",
				WsToken: wsTicketTok,
			},
		},
	}

	var capMu sync.Mutex
	var captured []capturedWSUpgrade

	// Real upstream HTTP server that hijacks HTTP 101 Switching Protocols and echoes lines back.
	wsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capMu.Lock()
		captured = append(captured, capturedWSUpgrade{
			Path:        r.URL.Path,
			RawQuery:    r.URL.RawQuery,
			AuthHeader:  r.Header.Get("Authorization"),
			AuthMethod:  r.Header.Get(chatWSAuthMethodHeader),
			Origin:      r.Header.Get("Origin"),
			Cookie:      r.Header.Get("Cookie"),
			Upgrade:     r.Header.Get("Upgrade"),
			Connection:  r.Header.Get("Connection"),
			WSKey:       r.Header.Get("Sec-WebSocket-Key"),
			WSVersion:   r.Header.Get("Sec-WebSocket-Version"),
			Traceparent: r.Header.Get("traceparent"),
		})
		capMu.Unlock()

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s-ok\r\n\r\n", r.Header.Get("Sec-WebSocket-Key"))
		if err := rw.Flush(); err != nil {
			return
		}

		scanner := bufio.NewScanner(rw)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "QUIT" {
				return
			}
			_, _ = fmt.Fprintf(rw, "echo:%s\n", line)
			if err := rw.Flush(); err != nil {
				return
			}
		}
	}))
	defer wsUpstream.Close()

	restore := setChatTestUpstream(chatMock, wsUpstream.URL)
	t.Cleanup(restore)

	log := logger.New("gateway-chat-ws-test", logger.WithWriter(io.Discard))
	reg := prometheus.NewRegistry()
	red := metrics.NewRED("gateway-chat-ws-test", reg)

	gw := httptest.NewServer(New(Dependencies{
		ServiceName:       "gateway-chat-ws-test",
		AuthManager:       authMgr,
		RevocationChecker: revoker,
		RED:               red,
		Logger:            log,
	}))
	defer gw.Close()

	gwURL, err := url.Parse(gw.URL)
	if err != nil {
		t.Fatalf("parse gw url: %v", err)
	}

	dialUpgrade := func(t *testing.T, targetPath string, headers map[string]string) (*http.Response, net.Conn, *bufio.Reader) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", gwURL.Host, 3*time.Second)
		if err != nil {
			t.Fatalf("dial gateway: %v", err)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		var b strings.Builder
		fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", targetPath)
		fmt.Fprintf(&b, "Host: %s\r\n", gwURL.Host)
		b.WriteString("Connection: Upgrade\r\n")
		b.WriteString("Upgrade: websocket\r\n")
		b.WriteString("Sec-WebSocket-Version: 13\r\n")
		b.WriteString("Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")
		for k, v := range headers {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
		b.WriteString("\r\n")
		if _, err := io.WriteString(conn, b.String()); err != nil {
			_ = conn.Close()
			t.Fatalf("write upgrade request: %v", err)
		}

		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
		if err != nil {
			_ = conn.Close()
			t.Fatalf("read upgrade response: %v", err)
		}
		return resp, conn, br
	}

	assertEcho := func(t *testing.T, conn net.Conn, br *bufio.Reader, msg string) {
		t.Helper()
		if _, err := fmt.Fprintf(conn, "%s\n", msg); err != nil {
			t.Fatalf("write tunnel message: %v", err)
		}
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read tunnel echo: %v", err)
		}
		want := "echo:" + msg + "\n"
		if line != want {
			t.Fatalf("tunnel echo = %q, want %q", line, want)
		}
	}

	// 1. Bearer JWT -> 101 + bidirectional echo + query params & traceparent forwarded.
	resp, conn, br := dialUpgrade(t, "/api/v1/chat/ws?after=42&conversation_id=conv-1", map[string]string{
		"Authorization": "Bearer " + buyerTok,
	})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		_ = conn.Close()
		t.Fatalf("bearer auth: expected 101, got %d (%s)", resp.StatusCode, string(body))
	}
	assertEcho(t, conn, br, "hello-bearer")
	assertEcho(t, conn, br, "second-frame")
	_, _ = io.WriteString(conn, "QUIT\n")
	_ = conn.Close()

	capMu.Lock()
	c0 := captured[len(captured)-1]
	capMu.Unlock()
	if c0.Path != "/ws" || c0.AuthHeader != "Bearer "+buyerTok || !strings.EqualFold(c0.Upgrade, "websocket") || c0.WSKey == "" || c0.Traceparent == "" {
		t.Fatalf("unexpected upstream headers for bearer auth: %+v", c0)
	}
	q0, _ := url.ParseQuery(c0.RawQuery)
	if q0.Get("after") != "42" || q0.Get("conversation_id") != "conv-1" {
		t.Fatalf("expected query params preserved, got %q", c0.RawQuery)
	}

	// 2. BFF cookie `nm_at` -> 101 + Authorization forwarded + bidirectional echo.
	resp, conn, br = dialUpgrade(t, "/api/v1/chat/ws?after=7", map[string]string{
		"Cookie": "nm_at=" + buyerTok,
	})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		_ = conn.Close()
		t.Fatalf("nm_at cookie auth: expected 101, got %d (%s)", resp.StatusCode, string(body))
	}
	assertEcho(t, conn, br, "hello-nm-at")
	_, _ = io.WriteString(conn, "QUIT\n")
	_ = conn.Close()

	capMu.Lock()
	c1 := captured[len(captured)-1]
	capMu.Unlock()
	if c1.AuthHeader != "Bearer "+buyerTok || c1.Cookie != "" || c1.AuthMethod != "cookie" {
		t.Fatalf("expected nm_at forwarded as Bearer Authorization, Cookie stripped, and auth-method=cookie, got %+v", c1)
	}

	// 3. BFF cookie `__Host-nm_at` -> 101 + Authorization forwarded + Cookie stripped + auth-method=cookie (spoofed header overwritten).
	resp, conn, br = dialUpgrade(t, "/api/v1/chat/ws", map[string]string{
		"Cookie":             "__Host-nm_at=" + sellerTok,
		"X-Chat-Auth-Method": "ticket",
	})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		_ = conn.Close()
		t.Fatalf("__Host-nm_at cookie auth: expected 101, got %d (%s)", resp.StatusCode, string(body))
	}
	assertEcho(t, conn, br, "hello-host-cookie")
	_, _ = io.WriteString(conn, "QUIT\n")
	_ = conn.Close()

	capMu.Lock()
	c2 := captured[len(captured)-1]
	capMu.Unlock()
	if c2.AuthHeader != "Bearer "+sellerTok || c2.Cookie != "" || c2.AuthMethod != "cookie" {
		t.Fatalf("expected __Host-nm_at forwarded as Bearer Authorization, Cookie stripped, and auth-method=cookie, got %+v", c2)
	}

	// 4. `?ticket=valid-ticket&after=99` from a custom shop domain (even when an ambient host cookie is present)
	// -> calls ExchangeWSTicket, strips `ticket` & `Cookie`, forwards Origin, and leaves auth-method empty (not cookie)!
	resp, conn, br = dialUpgrade(t, "/api/v1/chat/ws?ticket=valid-ticket&after=99", map[string]string{
		"Origin":             "https://shop-custom.example",
		"Cookie":             "nm_at=" + buyerTok,
		"X-Chat-Auth-Method": "cookie",
	})
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		_ = conn.Close()
		t.Fatalf("valid ticket auth: expected 101, got %d (%s)", resp.StatusCode, string(body))
	}
	assertEcho(t, conn, br, "hello-ticket")
	_, _ = io.WriteString(conn, "QUIT\n")
	_ = conn.Close()

	capMu.Lock()
	c3 := captured[len(captured)-1]
	capMu.Unlock()
	if c3.Path != "/ws" || c3.AuthHeader != "Bearer "+wsTicketTok || c3.AuthMethod != "" || c3.Origin != "https://shop-custom.example" || c3.Cookie != "" {
		t.Fatalf("expected ws_token forwarded to /ws without cookie auth-method on ticket path, got %+v", c3)
	}
	if strings.Contains(c3.RawQuery, "ticket") {
		t.Fatalf("ticket query param MUST be stripped before proxying upstream, got rawQuery=%q", c3.RawQuery)
	}
	q3, _ := url.ParseQuery(c3.RawQuery)
	if q3.Get("after") != "99" {
		t.Fatalf("expected non-ticket query param after=99 preserved, got %q", c3.RawQuery)
	}

	// 5. Rejected auth cases MUST return 401 UNAUTHENTICATED JSON error envelope BEFORE upgrade (never hitting upstream!).
	capMu.Lock()
	upstreamHitsBefore := len(captured)
	capMu.Unlock()

	rejectedCases := []struct {
		name    string
		path    string
		headers map[string]string
	}{
		{name: "no_credentials", path: "/api/v1/chat/ws"},
		{name: "invalid_bearer", path: "/api/v1/chat/ws", headers: map[string]string{"Authorization": "Bearer invalid.jwt.token"}},
		{name: "revoked_bearer", path: "/api/v1/chat/ws", headers: map[string]string{"Authorization": "Bearer " + revokedTok}},
		{name: "revoked_cookie", path: "/api/v1/chat/ws", headers: map[string]string{"Cookie": "nm_at=" + revokedTok}},
		{name: "revoked_ticket_jti", path: "/api/v1/chat/ws?ticket=revoked-ticket"},
		{name: "unknown_ticket", path: "/api/v1/chat/ws?ticket=does-not-exist"},
	}

	for _, tc := range rejectedCases {
		resp, conn, _ := dialUpgrade(t, tc.path, tc.headers)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		_ = conn.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d (%s)", tc.name, resp.StatusCode, string(body))
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				TraceID string `json:"trace_id"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("%s: expected JSON error envelope, decode failed: %v (body=%s)", tc.name, err, string(body))
		}
		if env.Error.Code != "UNAUTHENTICATED" || env.Error.TraceID == "" {
			t.Fatalf("%s: unexpected error envelope: %+v", tc.name, env)
		}
	}

	capMu.Lock()
	upstreamHitsAfter := len(captured)
	capMu.Unlock()
	if upstreamHitsAfter != upstreamHitsBefore {
		t.Fatalf("rejected WS auth requests must never reach upstream; hits before=%d after=%d", upstreamHitsBefore, upstreamHitsAfter)
	}
}
