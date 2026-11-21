package userclient

import (
	"context"
	"net"
	"testing"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeSiteServer struct {
	userv1.UnimplementedUserServiceServer
}

func (fakeSiteServer) GetSiteByShop(_ context.Context, req *userv1.GetSiteByShopRequest) (*userv1.GetSiteByShopResponse, error) {
	switch req.GetShopId() {
	case "published":
		return &userv1.GetSiteByShopResponse{Site: &userv1.Site{
			PrimaryHost: " bach-dang.nexusmart.vn ",
			Status:      userv1.SiteStatus_SITE_STATUS_PUBLISHED,
		}}, nil
	case "draft":
		return &userv1.GetSiteByShopResponse{Site: &userv1.Site{
			PrimaryHost: "draft.nexusmart.vn",
			Status:      userv1.SiteStatus_SITE_STATUS_DRAFT,
		}}, nil
	case "broken":
		return nil, status.Error(codes.Unavailable, "down")
	default:
		return nil, status.Error(codes.NotFound, "site not found")
	}
}

func TestGetSitePrimaryHost(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	userv1.RegisterUserServiceServer(srv, fakeSiteServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &Client{conn: conn, client: userv1.NewUserServiceClient(conn), timeout: time.Second}

	cases := []struct {
		shop    string
		want    string
		wantErr bool
	}{
		{"published", "bach-dang.nexusmart.vn", false},
		{"draft", "", false},
		{"missing", "", false},
		{"", "", false},
		{"broken", "", true},
	}
	for _, tc := range cases {
		got, err := c.GetSitePrimaryHost(context.Background(), tc.shop)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("shop %q: got (%q, %v), want (%q, err=%v)", tc.shop, got, err, tc.want, tc.wantErr)
		}
	}
}
