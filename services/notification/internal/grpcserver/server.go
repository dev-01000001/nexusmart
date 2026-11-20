package grpcserver

import (
	"context"
	"errors"
	"strings"

	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements notificationv1.NotificationServiceServer.
type Server struct {
	notificationv1.UnimplementedNotificationServiceServer
	svc *service.NotificationService
}

// New constructs a gRPC NotificationService server.
func New(svc *service.NotificationService) *Server {
	return &Server{svc: svc}
}

func (s *Server) ListNotifications(ctx context.Context, req *notificationv1.ListNotificationsRequest) (*notificationv1.ListNotificationsResponse, error) {
	userID, err := resolveAuthorizedUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	items, unread, total, err := s.svc.ListNotifications(
		ctx,
		userID,
		fromProtoCategory(req.GetCategory()),
		req.GetUnreadOnly(),
		req.GetPage(),
		req.GetPageSize(),
	)
	if err != nil {
		return nil, mapError(err)
	}

	protoItems := make([]*notificationv1.Notification, 0, len(items))
	for _, n := range items {
		protoItems = append(protoItems, &notificationv1.Notification{
			Id:        n.ID,
			UserId:    n.UserID,
			Category:  toProtoCategory(n.Category),
			Title:     n.Title,
			Body:      n.Body,
			Link:      n.Link,
			EventId:   n.EventID,
			IsRead:    n.IsRead,
			CreatedAt: timestamppb.New(n.CreatedAt),
		})
	}

	return &notificationv1.ListNotificationsResponse{
		Notifications: protoItems,
		UnreadCount:   unread,
		Total:         total,
	}, nil
}

func (s *Server) MarkRead(ctx context.Context, req *notificationv1.MarkReadRequest) (*notificationv1.MarkReadResponse, error) {
	userID, err := resolveAuthorizedUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	updated, unread, err := s.svc.MarkRead(ctx, userID, req.GetNotificationIds(), req.GetMarkAll())
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.MarkReadResponse{
		UpdatedCount: updated,
		UnreadCount:  unread,
	}, nil
}

func (s *Server) GetPreferences(ctx context.Context, req *notificationv1.GetPreferencesRequest) (*notificationv1.GetPreferencesResponse, error) {
	userID, err := resolveAuthorizedUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	prefs, err := s.svc.GetPreferences(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.GetPreferencesResponse{
		Preferences: toProtoPreferences(prefs),
	}, nil
}

func (s *Server) UpdatePreferences(ctx context.Context, req *notificationv1.UpdatePreferencesRequest) (*notificationv1.UpdatePreferencesResponse, error) {
	requestedID := req.GetUserId()
	if requestedID == "" && req.GetPreferences() != nil {
		requestedID = req.GetPreferences().GetUserId()
	}
	userID, err := resolveAuthorizedUserID(ctx, requestedID)
	if err != nil {
		return nil, err
	}

	in := store.DefaultPreferences(userID)
	if p := req.GetPreferences(); p != nil {
		in = store.Preferences{
			UserID:         userID,
			OrderInApp:     p.GetOrderInApp(),
			OrderEmail:     p.GetOrderEmail(),
			OrderPush:      p.GetOrderPush(),
			PromotionInApp: p.GetPromotionInApp(),
			PromotionEmail: p.GetPromotionEmail(),
			PromotionPush:  p.GetPromotionPush(),
			SystemInApp:    p.GetSystemInApp(),
			SystemEmail:    p.GetSystemEmail(),
			SystemPush:     p.GetSystemPush(),
		}
	}

	saved, err := s.svc.UpdatePreferences(ctx, in)
	if err != nil {
		return nil, mapError(err)
	}
	return &notificationv1.UpdatePreferencesResponse{
		Preferences: toProtoPreferences(saved),
	}, nil
}

// resolveAuthorizedUserID enforces authentication and owner-only access (admins may inspect any user).
func resolveAuthorizedUserID(ctx context.Context, requestedUserID string) (string, error) {
	requestedUserID = strings.TrimSpace(requestedUserID)
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.UserID() == "" {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	if requestedUserID == "" {
		return claims.UserID(), nil
	}
	if requestedUserID != claims.UserID() && !auth.HasRole(claims, "admin", "super_admin") {
		return "", status.Error(codes.PermissionDenied, "cannot access another user's notifications")
	}
	return requestedUserID, nil
}

func toProtoPreferences(p store.Preferences) *notificationv1.NotificationPreferences {
	return &notificationv1.NotificationPreferences{
		UserId:         p.UserID,
		OrderInApp:     p.OrderInApp,
		OrderEmail:     p.OrderEmail,
		OrderPush:      p.OrderPush,
		PromotionInApp: p.PromotionInApp,
		PromotionEmail: p.PromotionEmail,
		PromotionPush:  p.PromotionPush,
		SystemInApp:    p.SystemInApp,
		SystemEmail:    p.SystemEmail,
		SystemPush:     p.SystemPush,
	}
}

func fromProtoCategory(c notificationv1.NotificationCategory) string {
	switch c {
	case notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_ORDER:
		return store.CategoryOrder
	case notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_PROMOTION:
		return store.CategoryPromotion
	case notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_SYSTEM:
		return store.CategorySystem
	default:
		return ""
	}
}

func toProtoCategory(c string) notificationv1.NotificationCategory {
	switch strings.ToUpper(c) {
	case store.CategoryOrder:
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_ORDER
	case store.CategoryPromotion:
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_PROMOTION
	case store.CategorySystem:
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_SYSTEM
	default:
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_UNSPECIFIED
	}
}

func mapError(err error) error {
	if errors.Is(err, service.ErrInvalidArgument) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
