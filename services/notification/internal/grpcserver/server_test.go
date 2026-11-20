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
	prefs map[string]store.Preferences
}

func (r *stubRepo) ListNotifications(_ context.Context, _, _ string, _ bool, _, _ int32) ([]store.Notification, int64, int64, error) {
	return nil, 0, 0, nil
}
func (r *stubRepo) MarkRead(_ context.Context, _ string, _ []string, _ bool) (int32, int64, error) {
	return 0, 0, nil
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
}
