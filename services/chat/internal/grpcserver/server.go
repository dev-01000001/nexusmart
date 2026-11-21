package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/chat/internal/service"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/nexusmart/core/services/chat/internal/validator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements chatv1.ChatServiceServer.
type Server struct {
	chatv1.UnimplementedChatServiceServer
	svc *service.ChatService
}

// New creates a gRPC ChatService server.
func New(svc *service.ChatService) *Server {
	return &Server{svc: svc}
}

func (s *Server) CreateOrGetConversation(ctx context.Context, req *chatv1.CreateOrGetConversationRequest) (*chatv1.CreateOrGetConversationResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	conv, created, initMsg, err := s.svc.CreateOrGetConversation(ctx, claims, req.GetShopId(), req.GetSiteHost(), req.GetInitialMessage())
	if err != nil {
		return nil, mapError(err)
	}
	resp := &chatv1.CreateOrGetConversationResponse{
		Conversation: toProtoConversation(conv),
		Created:      created,
	}
	if initMsg != nil {
		resp.InitialMessage = toProtoMessage(*initMsg)
	}
	return resp, nil
}

func (s *Server) GetConversation(ctx context.Context, req *chatv1.GetConversationRequest) (*chatv1.GetConversationResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	conv, err := s.svc.GetConversation(ctx, claims, req.GetConversationId())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.GetConversationResponse{
		Conversation: toProtoConversation(conv),
	}, nil
}

func (s *Server) ListConversations(ctx context.Context, req *chatv1.ListConversationsRequest) (*chatv1.ListConversationsResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.ListConversations(
		ctx,
		claims,
		req.GetScope(),
		req.GetShopId(),
		req.GetUnrepliedOnly(),
		req.GetPage(),
		req.GetPageSize(),
	)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]*chatv1.Conversation, 0, len(res.Conversations))
	for _, c := range res.Conversations {
		items = append(items, toProtoConversation(c))
	}
	return &chatv1.ListConversationsResponse{
		Conversations: items,
		Total:         res.Total,
		TotalUnread:   res.TotalUnread,
	}, nil
}

func (s *Server) ListMessages(ctx context.Context, req *chatv1.ListMessagesRequest) (*chatv1.ListMessagesResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	conv, res, err := s.svc.ListMessages(
		ctx,
		claims,
		req.GetConversationId(),
		req.GetBefore(),
		req.GetAfter(),
		req.GetLimit(),
	)
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]*chatv1.ChatMessage, 0, len(res.Messages))
	for _, m := range res.Messages {
		items = append(items, toProtoMessage(m))
	}
	return &chatv1.ListMessagesResponse{
		Conversation: toProtoConversation(conv),
		Messages:     items,
		NextBefore:   res.NextBefore,
		LatestCursor: res.LatestCursor,
		HasMore:      res.HasMore,
	}, nil
}

func (s *Server) SendMessage(ctx context.Context, req *chatv1.SendMessageRequest) (*chatv1.SendMessageResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.SendMessage(ctx, claims, service.SendMessageInput{
		ConversationID: req.GetConversationId(),
		Kind:           fromProtoKind(req.GetKind()),
		Body:           req.GetBody(),
		RefID:          req.GetRefId(),
		MetadataJSON:   req.GetMetadataJson(),
		ClientMsgID:    req.GetClientMsgId(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := &chatv1.SendMessageResponse{
		Message: toProtoMessage(out.Message),
	}
	if out.AutoReply != nil {
		resp.AutoReply = toProtoMessage(*out.AutoReply)
	}
	return resp, nil
}

func (s *Server) MarkRead(ctx context.Context, req *chatv1.MarkReadRequest) (*chatv1.MarkReadResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.MarkRead(ctx, claims, req.GetConversationId(), req.GetLastReadSeq(), req.GetMessageId())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.MarkReadResponse{
		ConversationId: res.ConversationID,
		LastReadSeq:    res.LastReadSeq,
		UnreadCount:    res.UnreadCount,
	}, nil
}

func (s *Server) CreateWSTicket(ctx context.Context, _ *chatv1.CreateWSTicketRequest) (*chatv1.CreateWSTicketResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	rawTicket, expiresAt, err := s.svc.CreateWSTicket(ctx, claims)
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.CreateWSTicketResponse{
		Ticket:    rawTicket,
		ExpiresAt: timestamppb.New(expiresAt),
	}, nil
}

// ExchangeWSTicket is the ONLY Chat RPC that does not require JWT claims in context:
// the 256-bit random single-use ticket itself is the credential, atomically consumed via Redis GETDEL.
func (s *Server) ExchangeWSTicket(ctx context.Context, req *chatv1.ExchangeWSTicketRequest) (*chatv1.ExchangeWSTicketResponse, error) {
	if strings.TrimSpace(req.GetTicket()) == "" {
		return nil, status.Error(codes.InvalidArgument, "ticket is required")
	}
	res, err := s.svc.ExchangeWSTicket(ctx, req.GetTicket())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.ExchangeWSTicketResponse{
		UserId:  res.UserID,
		Role:    res.Role,
		ShopId:  res.ShopID,
		Jti:     res.JTI,
		WsToken: res.WSToken,
	}, nil
}

func (s *Server) GetAutoReplyConfig(ctx context.Context, req *chatv1.GetAutoReplyConfigRequest) (*chatv1.GetAutoReplyConfigResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.svc.GetAutoReplyConfig(ctx, claims, req.GetShopId())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.GetAutoReplyConfigResponse{
		Config: toProtoAutoReplyConfig(cfg),
	}, nil
}

func (s *Server) UpsertAutoReplyConfig(ctx context.Context, req *chatv1.UpsertAutoReplyConfigRequest) (*chatv1.UpsertAutoReplyConfigResponse, error) {
	claims, err := requireUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetConfig() == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}
	saved, err := s.svc.UpsertAutoReplyConfig(ctx, claims, fromProtoAutoReplyConfig(req.GetConfig()))
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.UpsertAutoReplyConfigResponse{
		Config: toProtoAutoReplyConfig(saved),
	}, nil
}

func requireUserClaims(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || strings.TrimSpace(claims.UserID()) == "" || claims.Service != "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return claims, nil
}

func toProtoConversation(c store.Conversation) *chatv1.Conversation {
	out := &chatv1.Conversation{
		Id:                    c.ID,
		BuyerId:               c.BuyerID,
		ShopId:                c.ShopID,
		SiteHost:              c.SiteHost,
		LastMessageId:         c.LastMessageID,
		LastMessageSeq:        c.LastMessageSeq,
		LastMessagePreview:    c.LastMessagePreview,
		LastMessageSenderRole: c.LastMessageSenderRole,
		BuyerLastReadSeq:      c.BuyerLastReadSeq,
		SellerLastReadSeq:     c.SellerLastReadSeq,
		UnreadCount:           c.UnreadCount,
		CreatedAt:             timestamppb.New(c.CreatedAt),
		UpdatedAt:             timestamppb.New(c.UpdatedAt),
	}
	if c.LastMessageAt != nil {
		out.LastMessageAt = timestamppb.New(*c.LastMessageAt)
	}
	return out
}

func toProtoMessage(m store.Message) *chatv1.ChatMessage {
	metaJSON := ""
	if len(m.Metadata) > 0 {
		if b, err := json.Marshal(m.Metadata); err == nil {
			metaJSON = string(b)
		}
	}
	return &chatv1.ChatMessage{
		Id:             m.ID,
		Seq:            m.Seq,
		ConversationId: m.ConversationID,
		SenderId:       m.SenderID,
		SenderRole:     m.SenderRole,
		Kind:           toProtoKind(m.Kind),
		Body:           m.Body,
		RefId:          m.RefID,
		MetadataJson:   metaJSON,
		IsAutoReply:    m.IsAutoReply,
		ClientMsgId:    m.ClientMsgID,
		CreatedAt:      timestamppb.New(m.CreatedAt),
	}
}

func toProtoKind(k string) chatv1.MessageKind {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case store.KindText:
		return chatv1.MessageKind_MESSAGE_KIND_TEXT
	case store.KindImage:
		return chatv1.MessageKind_MESSAGE_KIND_IMAGE
	case store.KindProduct:
		return chatv1.MessageKind_MESSAGE_KIND_PRODUCT
	case store.KindOrder:
		return chatv1.MessageKind_MESSAGE_KIND_ORDER
	default:
		return chatv1.MessageKind_MESSAGE_KIND_UNSPECIFIED
	}
}

func fromProtoKind(k chatv1.MessageKind) string {
	switch k {
	case chatv1.MessageKind_MESSAGE_KIND_TEXT:
		return store.KindText
	case chatv1.MessageKind_MESSAGE_KIND_IMAGE:
		return store.KindImage
	case chatv1.MessageKind_MESSAGE_KIND_PRODUCT:
		return store.KindProduct
	case chatv1.MessageKind_MESSAGE_KIND_ORDER:
		return store.KindOrder
	default:
		return ""
	}
}

func toProtoAutoReplyConfig(cfg store.AutoReplyConfig) *chatv1.AutoReplyConfig {
	rules := make([]*chatv1.KeywordRule, 0, len(cfg.KeywordRules))
	for _, r := range cfg.KeywordRules {
		kws := make([]string, len(r.Keywords))
		copy(kws, r.Keywords)
		rules = append(rules, &chatv1.KeywordRule{
			Keywords: kws,
			Reply:    r.Reply,
		})
	}
	out := &chatv1.AutoReplyConfig{
		ShopId:          cfg.ShopID,
		Enabled:         cfg.Enabled,
		GreetingMessage: cfg.GreetingMessage,
		OfflineEnabled:  cfg.OfflineEnabled,
		OfflineMessage:  cfg.OfflineMessage,
		WorkStartHour:   cfg.WorkStartHour,
		WorkEndHour:     cfg.WorkEndHour,
		Timezone:        cfg.Timezone,
		CooldownMinutes: cfg.CooldownMinutes,
		KeywordRules:    rules,
	}
	if !cfg.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(cfg.UpdatedAt)
	}
	return out
}

func fromProtoAutoReplyConfig(p *chatv1.AutoReplyConfig) store.AutoReplyConfig {
	rules := make([]store.KeywordRule, 0, len(p.GetKeywordRules()))
	for _, r := range p.GetKeywordRules() {
		kws := make([]string, len(r.GetKeywords()))
		copy(kws, r.GetKeywords())
		rules = append(rules, store.KeywordRule{
			Keywords: kws,
			Reply:    r.GetReply(),
		})
	}
	return store.AutoReplyConfig{
		ShopID:          p.GetShopId(),
		Enabled:         p.GetEnabled(),
		GreetingMessage: p.GetGreetingMessage(),
		OfflineEnabled:  p.GetOfflineEnabled(),
		OfflineMessage:  p.GetOfflineMessage(),
		WorkStartHour:   p.GetWorkStartHour(),
		WorkEndHour:     p.GetWorkEndHour(),
		Timezone:        p.GetTimezone(),
		CooldownMinutes: p.GetCooldownMinutes(),
		KeywordRules:    rules,
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, service.ErrUnauthenticated),
		errors.Is(err, ticket.ErrInvalidTicket),
		errors.Is(err, ticket.ErrRevokedToken):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, service.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, service.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, service.ErrInvalidArgument),
		errors.Is(err, store.ErrInvalidArgument),
		errors.Is(err, store.ErrInvalidCursor),
		errors.Is(err, validator.ErrInvalidImage):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, service.ErrShopInactive):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
