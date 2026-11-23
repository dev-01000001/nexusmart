package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubRepo struct {
	prefs  map[string]store.Preferences
	notifs []store.Notification
}

func (r *stubRepo) ListNotifications(_ context.Context, userID, category string, unreadOnly bool, limit, offset int32) ([]store.Notification, int64, int64, error) {
	var filtered []store.Notification
	var unread int64
	for _, n := range r.notifs {
		if n.UserID != userID {
			continue
		}
		if !n.IsRead {
			unread++
		}
		if category != "" && n.Category != category {
			continue
		}
		if unreadOnly && n.IsRead {
			continue
		}
		filtered = append(filtered, n)
	}
	total := int64(len(filtered))
	start := int(offset)
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + int(limit)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], unread, total, nil
}
func (r *stubRepo) MarkRead(_ context.Context, userID string, ids []string, markAll bool) (int32, int64, error) {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var updated int32
	var unread int64
	for i := range r.notifs {
		if r.notifs[i].UserID != userID {
			continue
		}
		if !r.notifs[i].IsRead && (markAll || idSet[r.notifs[i].ID]) {
			r.notifs[i].IsRead = true
			updated++
		}
		if !r.notifs[i].IsRead {
			unread++
		}
	}
	return updated, unread, nil
}
func (r *stubRepo) GetPreferences(_ context.Context, userID string) (store.Preferences, error) {
	if p, ok := r.prefs[userID]; ok {
		return p, nil
	}
	return store.DefaultPreferences(userID), nil
}
func (r *stubRepo) UpsertPreferences(_ context.Context, prefs store.Preferences) (store.Preferences, error) {
	if r.prefs == nil {
		r.prefs = make(map[string]store.Preferences)
	}
	r.prefs[prefs.UserID] = prefs
	return prefs, nil
}
func (r *stubRepo) ProcessEventTx(_ context.Context, _, _ string, _ func(tx store.TxStore) error) (bool, error) {
	return false, nil
}
func (r *stubRepo) IsEventProcessed(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (r *stubRepo) DeleteProcessedEvent(_ context.Context, _ string) error {
	return nil
}
func (r *stubRepo) GetUserContact(_ context.Context, _ string) (store.UserContact, bool, error) {
	return store.UserContact{}, false, nil
}
func (r *stubRepo) GetOrderParticipant(_ context.Context, _ string) (store.OrderParticipant, bool, error) {
	return store.OrderParticipant{}, false, nil
}
func (r *stubRepo) ListOrderParticipantsByGroup(_ context.Context, _ string) ([]store.OrderParticipant, error) {
	return nil, nil
}
func (r *stubRepo) GetShopOwner(_ context.Context, _ string) (store.ShopOwner, bool, error) {
	return store.ShopOwner{}, false, nil
}
func (r *stubRepo) RecordEmailDedup(_ context.Context, _, _ string) error {
	return nil
}
func (r *stubRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	return nil
}

func ctxWithUser(userID, role string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	})
}

func TestNotificationGRPCAuthAndOwnershipRBAC(t *testing.T) {
	svc := service.NewNotificationService(&stubRepo{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	// 1. Unauthenticated caller must be rejected even if user_id is populated.
	if _, err := srv.ListNotifications(context.Background(), &notificationv1.ListNotificationsRequest{UserId: "user-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for ListNotifications without claims, got %v", err)
	}
	if _, err := srv.MarkRead(context.Background(), &notificationv1.MarkReadRequest{UserId: "user-1", MarkAll: true}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for MarkRead without claims, got %v", err)
	}
	if _, err := srv.UpdatePreferences(context.Background(), &notificationv1.UpdatePreferencesRequest{UserId: "user-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for UpdatePreferences without claims, got %v", err)
	}

	// 2. Cross-user access by buyer must be rejected with PermissionDenied.
	buyer2Ctx := ctxWithUser("user-2", "buyer")
	if _, err := srv.ListNotifications(buyer2Ctx, &notificationv1.ListNotificationsRequest{UserId: "user-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-user ListNotifications, got %v", err)
	}
	if _, err := srv.GetPreferences(buyer2Ctx, &notificationv1.GetPreferencesRequest{UserId: "user-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-user GetPreferences, got %v", err)
	}

	// 3. Owner access succeeds and UC-B23 preference guard is enforced over gRPC.
	buyer1Ctx := ctxWithUser("user-1", "buyer")
	upd, err := srv.UpdatePreferences(buyer1Ctx, &notificationv1.UpdatePreferencesRequest{
		UserId: "user-1",
		Preferences: &notificationv1.NotificationPreferences{
			OrderInApp:     false,
			OrderEmail:     false,
			SystemInApp:    false,
			PromotionEmail: false,
		},
	})
	if err != nil {
		t.Fatalf("owner UpdatePreferences failed: %v", err)
	}
	if !upd.GetPreferences().GetOrderInApp() || !upd.GetPreferences().GetSystemInApp() {
		t.Fatalf("expected UC-B23 guard to keep order_in_app & system_in_app true, got %+v", upd.GetPreferences())
	}

	// 4. Admin can inspect another user's notifications.
	adminCtx := ctxWithUser("admin-1", "admin")
	if _, err := srv.ListNotifications(adminCtx, &notificationv1.ListNotificationsRequest{UserId: "user-1"}); err != nil {
		t.Fatalf("admin ListNotifications failed: %v", err)
	}

	// 5. UC-B23: attempting to disable ALL notification channels still keeps order_in_app & system_in_app true on both Update & Get.
	allOff, err := srv.UpdatePreferences(buyer1Ctx, &notificationv1.UpdatePreferencesRequest{
		UserId: "user-1",
		Preferences: &notificationv1.NotificationPreferences{
			OrderInApp:     false,
			OrderEmail:     false,
			OrderPush:      false,
			PromotionInApp: false,
			PromotionEmail: false,
			PromotionPush:  false,
			SystemInApp:    false,
			SystemEmail:    false,
			SystemPush:     false,
		},
	})
	if err != nil {
		t.Fatalf("all-off UpdatePreferences failed: %v", err)
	}
	if !allOff.GetPreferences().GetOrderInApp() || !allOff.GetPreferences().GetSystemInApp() {
		t.Fatalf("UC-B23: UpdatePreferences must force order_in_app & system_in_app true, got %+v", allOff.GetPreferences())
	}
	gotPrefs, err := srv.GetPreferences(buyer1Ctx, &notificationv1.GetPreferencesRequest{UserId: "user-1"})
	if err != nil {
		t.Fatalf("GetPreferences failed: %v", err)
	}
	if !gotPrefs.GetPreferences().GetOrderInApp() || !gotPrefs.GetPreferences().GetSystemInApp() {
		t.Fatalf("UC-B23: GetPreferences must enforce order_in_app & system_in_app true, got %+v", gotPrefs.GetPreferences())
	}
}

func TestHeaderUnreadCountOverGRPC(t *testing.T) {
	repo := &stubRepo{
		notifs: []store.Notification{
			{ID: "n-1", UserID: "user-1", Category: store.CategoryOrder, Title: "Đơn mới", IsRead: false},
			{ID: "n-2", UserID: "user-1", Category: store.CategoryPromotion, Title: "Voucher sắp hết hạn", IsRead: false},
			{ID: "n-3", UserID: "user-1", Category: store.CategorySystem, Title: "Duyệt sản phẩm", IsRead: true},
			{ID: "n-other", UserID: "user-2", Category: store.CategoryOrder, Title: "Của người khác", IsRead: false},
		},
	}
	svc := service.NewNotificationService(repo, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)
	ctx := ctxWithUser("user-1", "buyer")

	// Header badge query (page=1, page_size=5) returns unread_count=2 across all categories for user-1 only.
	resp, err := srv.ListNotifications(ctx, &notificationv1.ListNotificationsRequest{
		Page:     1,
		PageSize: 5,
	})
	if err != nil {
		t.Fatalf("ListNotifications failed: %v", err)
	}
	if resp.GetUnreadCount() != 2 || resp.GetTotal() != 3 || len(resp.GetNotifications()) != 3 {
		t.Fatalf("header ListNotifications mismatch: unread=%d total=%d len=%d", resp.GetUnreadCount(), resp.GetTotal(), len(resp.GetNotifications()))
	}

	// Category-filtered query still reports global unread_count=2.
	promoResp, err := srv.ListNotifications(ctx, &notificationv1.ListNotificationsRequest{
		Category: notificationv1.NotificationCategory_NOTIFICATION_CATEGORY_PROMOTION,
		Page:     1,
		PageSize: 1,
	})
	if err != nil {
		t.Fatalf("category ListNotifications failed: %v", err)
	}
	if promoResp.GetUnreadCount() != 2 || promoResp.GetTotal() != 1 || len(promoResp.GetNotifications()) != 1 {
		t.Fatalf("category-filtered header unread mismatch: unread=%d total=%d len=%d", promoResp.GetUnreadCount(), promoResp.GetTotal(), len(promoResp.GetNotifications()))
	}

	// Mark single notification read -> unread_count decrements to 1.
	mark1, err := srv.MarkRead(ctx, &notificationv1.MarkReadRequest{
		NotificationIds: []string{"n-2"},
	})
	if err != nil {
		t.Fatalf("MarkRead single failed: %v", err)
	}
	if mark1.GetUpdatedCount() != 1 || mark1.GetUnreadCount() != 1 {
		t.Fatalf("expected updated=1 unread=1, got updated=%d unread=%d", mark1.GetUpdatedCount(), mark1.GetUnreadCount())
	}

	// Mark all read -> unread_count becomes 0.
	markAll, err := srv.MarkRead(ctx, &notificationv1.MarkReadRequest{MarkAll: true})
	if err != nil {
		t.Fatalf("MarkRead all failed: %v", err)
	}
	if markAll.GetUpdatedCount() != 1 || markAll.GetUnreadCount() != 0 {
		t.Fatalf("expected updated=1 unread=0, got updated=%d unread=%d", markAll.GetUpdatedCount(), markAll.GetUnreadCount())
	}
}
