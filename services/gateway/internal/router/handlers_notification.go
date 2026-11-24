package router

import (
	"net/http"
	"strings"

	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type notificationDTO struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	EventID   string `json:"event_id"`
	IsRead    bool   `json:"is_read"`
	CreatedAt string `json:"created_at,omitempty"`
}

type notificationPreferencesDTO struct {
	UserID         string `json:"user_id"`
	OrderInApp     bool   `json:"order_in_app"`
	OrderEmail     bool   `json:"order_email"`
	OrderPush      bool   `json:"order_push"`
	PromotionInApp bool   `json:"promotion_in_app"`
	PromotionEmail bool   `json:"promotion_email"`
	PromotionPush  bool   `json:"promotion_push"`
	SystemInApp    bool   `json:"system_in_app"`
	SystemEmail    bool   `json:"system_email"`
	SystemPush     bool   `json:"system_push"`
}

func notificationCategoryString(c notificationv1.NotificationCategory) string {
	switch c {
	case notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_ORDER:
		return "ORDER"
	case notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_PROMOTION:
		return "PROMOTION"
	case notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_SYSTEM:
		return "SYSTEM"
	default:
		return ""
	}
}

func parseNotificationCategory(s string) notificationv1.NotificationCategory {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ORDER", "NOTIFICATION_CATEGORY_ORDER":
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_ORDER
	case "PROMOTION", "NOTIFICATION_CATEGORY_PROMOTION":
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_PROMOTION
	case "SYSTEM", "NOTIFICATION_CATEGORY_SYSTEM":
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_SYSTEM
	default:
		return notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_UNSPECIFIED
	}
}

func mapNotificationPreferences(p *notificationv1.NotificationPreferences) *notificationPreferencesDTO {
	if p == nil {
		return nil
	}
	return &notificationPreferencesDTO{
		UserID:         p.GetUserId(),
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

func (h *handler) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Notification.ListNotifications(ctx, &notificationv1.ListNotificationsRequest{
		UserId:     auth.UserIDFromContext(r.Context()),
		Category:   parseNotificationCategory(q.Get("category")),
		UnreadOnly: httpx.ParseBoolQuery(r, "unread_only"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*notificationDTO, 0, len(resp.GetNotifications()))
	for _, n := range resp.GetNotifications() {
		items = append(items, &notificationDTO{
			ID:        n.GetId(),
			UserID:    n.GetUserId(),
			Category:  notificationCategoryString(n.GetCategory()),
			Title:     n.GetTitle(),
			Body:      n.GetBody(),
			Link:      n.GetLink(),
			EventID:   n.GetEventId(),
			IsRead:    n.GetIsRead(),
			CreatedAt: httpx.FormatTimestamp(n.GetCreatedAt()),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":        items,
		"unread_count": resp.GetUnreadCount(),
		"page":         page,
		"page_size":    pageSize,
		"total":        resp.GetTotal(),
	})
}

func (h *handler) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NotificationIDs []string `json:"notification_ids"`
		MarkAll         bool     `json:"mark_all"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Notification.MarkRead(ctx, &notificationv1.MarkReadRequest{
		UserId:          auth.UserIDFromContext(r.Context()),
		NotificationIds: body.NotificationIDs,
		MarkAll:         body.MarkAll,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"updated_count": resp.GetUpdatedCount(),
		"unread_count":  resp.GetUnreadCount(),
	})
}

func (h *handler) handleGetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Notification.GetPreferences(ctx, &notificationv1.GetPreferencesRequest{
		UserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"preferences": mapNotificationPreferences(resp.GetPreferences()),
	})
}

func (h *handler) handleUpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderInApp     bool `json:"order_in_app"`
		OrderEmail     bool `json:"order_email"`
		OrderPush      bool `json:"order_push"`
		PromotionInApp bool `json:"promotion_in_app"`
		PromotionEmail bool `json:"promotion_email"`
		PromotionPush  bool `json:"promotion_push"`
		SystemInApp    bool `json:"system_in_app"`
		SystemEmail    bool `json:"system_email"`
		SystemPush     bool `json:"system_push"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	uid := auth.UserIDFromContext(r.Context())
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Notification.UpdatePreferences(ctx, &notificationv1.UpdatePreferencesRequest{
		UserId: uid,
		Preferences: &notificationv1.NotificationPreferences{
			UserId:         uid,
			OrderInApp:     body.OrderInApp,
			OrderEmail:     body.OrderEmail,
			OrderPush:      body.OrderPush,
			PromotionInApp: body.PromotionInApp,
			PromotionEmail: body.PromotionEmail,
			PromotionPush:  body.PromotionPush,
			SystemInApp:    body.SystemInApp,
			SystemEmail:    body.SystemEmail,
			SystemPush:     body.SystemPush,
		},
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"preferences": mapNotificationPreferences(resp.GetPreferences()),
	})
}
