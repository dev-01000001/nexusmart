package clients

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRetryIdempotent_RetriesTransientAndStopsOnPermanent(t *testing.T) {
	attempts := 0
	err := RetryIdempotent(context.Background(), 3, func(_ context.Context) error {
		attempts++
		if attempts < 3 {
			return status.Error(codes.Unavailable, "temporary outage")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("expected success on 3rd attempt, got err=%v attempts=%d", err, attempts)
	}

	permAttempts := 0
	err = RetryIdempotent(context.Background(), 3, func(_ context.Context) error {
		permAttempts++
		return status.Error(codes.InvalidArgument, "bad request")
	})
	if status.Code(err) != codes.InvalidArgument || permAttempts != 1 {
		t.Fatalf("expected immediate stop on non-retryable error, got err=%v attempts=%d", err, permAttempts)
	}
}

func TestWithInternalAdminContext_SendsServiceTokenNotBuyerJWT(t *testing.T) {
	svc, _ := auth.NewManager("order-test-service-key", auth.ServiceTokenTTL)
	buyerCtx := auth.WithClaims(context.Background(), &auth.Claims{
		Role:             "buyer",
		ShopID:           "shop-buyer",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "buyer-1"},
	})
	buyerCtx = auth.WithRawToken(buyerCtx, "Bearer buyer-jwt-secret")

	capture := func(ctx context.Context) metadata.MD {
		t.Helper()
		var md metadata.MD
		err := timeoutAndAuthClientInterceptor(2*time.Second, svc)(ctx, "/nexusmart.product.v1.ProductService/ReleaseStock", nil, nil, nil,
			func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				md, _ = metadata.FromOutgoingContext(ctx)
				return nil
			})
		if err != nil {
			t.Fatalf("interceptor returned error: %v", err)
		}
		return md
	}

	// User-initiated call: forward the buyer JWT only, no plain identity metadata.
	md := capture(buyerCtx)
	if got := md.Get(auth.MDAuthorization); len(got) != 1 || got[0] != "Bearer buyer-jwt-secret" {
		t.Fatalf("buyer call authorization = %v", got)
	}
	for _, k := range []string{"x-user-id", "x-user-role", "x-shop-id"} {
		if len(md.Get(k)) != 0 {
			t.Fatalf("%s must not be sent, got %v", k, md.Get(k))
		}
	}

	// Internal call: buyer JWT replaced by a service token signed with the internal key.
	md = capture(WithInternalAdminContext(buyerCtx))
	got := md.Get(auth.MDAuthorization)
	if len(got) != 1 {
		t.Fatalf("internal call authorization = %v", got)
	}
	claims, err := svc.VerifyServiceToken(got[0])
	if err != nil || claims.Service != InternalServiceName || claims.Role != auth.ServiceRole {
		t.Fatalf("expected valid order-service token, claims=%+v err=%v", claims, err)
	}
}
