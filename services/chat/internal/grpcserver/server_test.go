package grpcserver

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/hub"
	"github.com/nexusmart/core/services/chat/internal/service"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ctxWithClaims(userID, role, shopID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
			ID:      "jti-" + userID,
		},
	})
}

func TestGRPCServerAuthPermissionsAndTicketExchange(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	ticketMgr, err := ticket.NewManager(rdb, "grpc-test-secret", 60*time.Second)
	if err != nil {
		t.Fatalf("ticket manager: %v", err)
	}
	st := store.NewMemStore()
	h := hub.New(nil, "inst-grpc", nil, nil)
	svc := service.NewChatService(st, h, ticketMgr, nil, nil)
	srv := New(svc)

	// Unauthenticated calls rejected on every RPC except ExchangeWSTicket.
	if _, err := srv.CreateOrGetConversation(context.Background(), &chatv1.CreateOrGetConversationRequest{ShopId: "shop-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
	if _, err := srv.CreateWSTicket(context.Background(), &chatv1.CreateWSTicketRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated on CreateWSTicket, got %v", err)
	}

	// Service tokens must also be rejected.
	svcCtx := auth.WithClaims(context.Background(), &auth.Claims{
		Role:    "admin",
		Service: "order-service",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "order-service",
		},
	})
	if _, err := srv.ListConversations(svcCtx, &chatv1.ListConversationsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected service token rejected, got %v", err)
	}

	buyerCtx := ctxWithClaims("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithClaims("buyer-2", "buyer", "")
	sellerCtx := ctxWithClaims("seller-1", "seller", "shop-1")

	// Create conversation with initial message.
	createResp, err := srv.CreateOrGetConversation(buyerCtx, &chatv1.CreateOrGetConversationRequest{
		ShopId:         "shop-1",
		SiteHost:       "shop1.nexusmart.localhost",
		InitialMessage: "Chao shop!",
	})
	if err != nil || !createResp.GetCreated() || createResp.GetInitialMessage() == nil {
		t.Fatalf("create conversation failed: resp=%+v err=%v", createResp, err)
	}
	convID := createResp.GetConversation().GetId()

	// Unrelated buyer denied with PermissionDenied.
	if _, err := srv.GetConversation(otherBuyerCtx, &chatv1.GetConversationRequest{ConversationId: convID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer, got %v", err)
	}

	// Seller sends reply & buyer reads it.
	sendResp, err := srv.SendMessage(sellerCtx, &chatv1.SendMessageRequest{
		ConversationId: convID,
		Kind:           chatv1.MessageKind_MESSAGE_KIND_TEXT,
		Body:           "Shop nghe a!",
		ClientMsgId:    "seller-m1",
	})
	if err != nil || sendResp.GetMessage().GetSeq() <= 0 {
		t.Fatalf("seller send failed: resp=%+v err=%v", sendResp, err)
	}

	listResp, err := srv.ListMessages(buyerCtx, &chatv1.ListMessagesRequest{ConversationId: convID, Limit: 10})
	if err != nil || len(listResp.GetMessages()) != 2 {
		t.Fatalf("list messages failed: resp=%+v err=%v", listResp, err)
	}

	readResp, err := srv.MarkRead(buyerCtx, &chatv1.MarkReadRequest{ConversationId: convID})
	if err != nil || readResp.GetUnreadCount() != 0 || readResp.GetLastReadSeq() != sendResp.GetMessage().GetSeq() {
		t.Fatalf("mark read failed: resp=%+v err=%v", readResp, err)
	}

	// Ticket create (authenticated) + exchange (unauthenticated, single-use!).
	tResp, err := srv.CreateWSTicket(buyerCtx, &chatv1.CreateWSTicketRequest{})
	if err != nil || tResp.GetTicket() == "" {
		t.Fatalf("create ws ticket failed: resp=%+v err=%v", tResp, err)
	}
	exResp, err := srv.ExchangeWSTicket(context.Background(), &chatv1.ExchangeWSTicketRequest{Ticket: tResp.GetTicket()})
	if err != nil || exResp.GetUserId() != "buyer-1" || exResp.GetWsToken() == "" {
		t.Fatalf("exchange ws ticket failed: resp=%+v err=%v", exResp, err)
	}
	if _, err := srv.ExchangeWSTicket(context.Background(), &chatv1.ExchangeWSTicketRequest{Ticket: tResp.GetTicket()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected replay to fail with Unauthenticated, got %v", err)
	}

	// Auto-reply config restricted to own shop.
	if _, err := srv.GetAutoReplyConfig(buyerCtx, &chatv1.GetAutoReplyConfigRequest{ShopId: "shop-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer auto-reply get, got %v", err)
	}
	upResp, err := srv.UpsertAutoReplyConfig(sellerCtx, &chatv1.UpsertAutoReplyConfigRequest{
		Config: &chatv1.AutoReplyConfig{
			ShopId:          "shop-1",
			Enabled:         true,
			GreetingMessage: "Xin chao!",
			WorkStartHour:   8,
			WorkEndHour:     22,
			Timezone:        "Asia/Ho_Chi_Minh",
			CooldownMinutes: 15,
		},
	})
	if err != nil || !upResp.GetConfig().GetEnabled() {
		t.Fatalf("upsert auto reply config failed: resp=%+v err=%v", upResp, err)
	}
}
