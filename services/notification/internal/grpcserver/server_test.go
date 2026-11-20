package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
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

func TestNotificationGRPCTrust_BufconnRejectsAnonymousAndServiceTokens(t *testing.T) {
	svc := service.NewNotificationService(&stubRepo{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	users, _ := auth.NewManager("notif-test-user-key", 15*time.Minute)
	svcMgr, _ := auth.NewManager("notif-test-service-key", auth.ServiceTokenTTL)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	notificationv1.RegisterNotificationServiceServer(gs, New(svc))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := notificationv1.NewNotificationServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string) context.Context {
		return md("authorization", "Bearer "+tok)
	}

	buyer1Tok, _, _, _ := users.SignAccessToken("user-1", "buyer", "")
	buyer2Tok, _, _, _ := users.SignAccessToken("user-2", "buyer", "")
	svcTok, _ := svcMgr.SignServiceToken("order-service")
	spoof := []string{"x-user-id", "user-1", "x-user-role", "admin"}

	// Anonymous/spoofed -> Unauthenticated on all 4 RPCs.
	if _, err := client.ListNotifications(md(spoof...), &notificationv1.ListNotificationsRequest{UserId: "user-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListNotifications: want Unauthenticated, got %v", err)
	}
	if _, err := client.MarkRead(md(spoof...), &notificationv1.MarkReadRequest{UserId: "user-1", MarkAll: true}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon MarkRead: want Unauthenticated, got %v", err)
	}
	if _, err := client.GetPreferences(md(spoof...), &notificationv1.GetPreferencesRequest{UserId: "user-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon GetPreferences: want Unauthenticated, got %v", err)
	}
	if _, err := client.UpdatePreferences(md(spoof...), &notificationv1.UpdatePreferencesRequest{UserId: "user-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon UpdatePreferences: want Unauthenticated, got %v", err)
	}

	// Disallowed service token -> PermissionDenied on all 4 RPCs.
	if _, err := client.ListNotifications(bearer(svcTok), &notificationv1.ListNotificationsRequest{UserId: "user-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ListNotifications: want PermissionDenied, got %v", err)
	}
	if _, err := client.MarkRead(bearer(svcTok), &notificationv1.MarkReadRequest{UserId: "user-1", MarkAll: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token MarkRead: want PermissionDenied, got %v", err)
	}
	if _, err := client.GetPreferences(bearer(svcTok), &notificationv1.GetPreferencesRequest{UserId: "user-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token GetPreferences: want PermissionDenied, got %v", err)
	}
	if _, err := client.UpdatePreferences(bearer(svcTok), &notificationv1.UpdatePreferencesRequest{UserId: "user-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token UpdatePreferences: want PermissionDenied, got %v", err)
	}

	// Cross-user -> PermissionDenied; owner -> OK.
	if _, err := client.ListNotifications(bearer(buyer2Tok), &notificationv1.ListNotificationsRequest{UserId: "user-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ListNotifications: want PermissionDenied, got %v", err)
	}
	if _, err := client.ListNotifications(bearer(buyer1Tok), &notificationv1.ListNotificationsRequest{UserId: "user-1"}); err != nil {
		t.Fatalf("owner ListNotifications: %v", err)
	}
}
