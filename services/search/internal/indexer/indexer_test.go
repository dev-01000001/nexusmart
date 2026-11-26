package indexer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/search/internal/store"
	"google.golang.org/grpc"
)

type recordingShopDBTX struct {
	mu    sync.Mutex
	shops map[string]string
}

func newRecordingShopDBTX() *recordingShopDBTX {
	return &recordingShopDBTX{shops: make(map[string]string)}
}

func (r *recordingShopDBTX) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	if len(args) >= 2 {
		if sid, ok := args[0].(string); ok {
			name, _ := args[1].(string)
			r.mu.Lock()
			r.shops[sid] = name
			r.mu.Unlock()
		}
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (r *recordingShopDBTX) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (r *recordingShopDBTX) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	return nil
}

type pagedUserClient struct {
	mu                   sync.Mutex
	shops                []*userv1.Shop
	scriptedPages        [][]*userv1.Shop
	scriptedTotal        int64
	peerCap              int32
	omitTotal            bool
	repeatPage1          bool
	infiniteMode         bool
	calls                int32
	maxCalls             int32
	getShopInternalCalls int32
}

func (c *pagedUserClient) GetShopInternal(_ context.Context, _ *userv1.GetShopInternalRequest, _ ...grpc.CallOption) (*userv1.GetShopInternalResponse, error) {
	c.mu.Lock()
	c.getShopInternalCalls++
	c.mu.Unlock()
	return &userv1.GetShopInternalResponse{}, nil
}

func (c *pagedUserClient) ListPublicShops(_ context.Context, in *userv1.ListPublicShopsRequest, _ ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.maxCalls > 0 && c.calls > c.maxCalls {
		return nil, fmt.Errorf("safety abort: exceeded %d calls", c.maxCalls)
	}
	if c.infiniteMode {
		return &userv1.ListPublicShopsResponse{
			Shops: []*userv1.Shop{{
				Id:     fmt.Sprintf("shop-inf-%d", c.calls),
				Name:   fmt.Sprintf("Inf Shop %d", c.calls),
				Slug:   fmt.Sprintf("inf-%d", c.calls),
				Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			}},
			Total: 0,
		}, nil
	}

	if len(c.scriptedPages) > 0 {
		idx := int(in.GetPage() - 1)
		if idx < 0 || idx >= len(c.scriptedPages) {
			return &userv1.ListPublicShopsResponse{Total: c.scriptedTotal}, nil
		}
		return &userv1.ListPublicShopsResponse{
			Shops: c.scriptedPages[idx],
			Total: c.scriptedTotal,
		}, nil
	}

	page := in.GetPage()
	if page <= 0 {
		page = 1
	}
	if c.repeatPage1 {
		page = 1
	}
	ps := in.GetPageSize()
	if ps <= 0 {
		ps = 20
	}
	if c.peerCap > 0 && ps > c.peerCap {
		ps = c.peerCap
	}

	start := int((page - 1) * ps)
	if start > len(c.shops) {
		start = len(c.shops)
	}
	end := start + int(ps)
	if end > len(c.shops) {
		end = len(c.shops)
	}
	total := int64(len(c.shops))
	if c.omitTotal {
		total = 0
	}
	return &userv1.ListPublicShopsResponse{
		Shops: c.shops[start:end],
		Total: total,
	}, nil
}

func makeTestShops(n int) []*userv1.Shop {
	out := make([]*userv1.Shop, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, &userv1.Shop{
			Id:     fmt.Sprintf("shop-%04d", i),
			Name:   fmt.Sprintf("Shop %04d", i),
			Slug:   fmt.Sprintf("shop-%04d", i),
			Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
		})
	}
	return out
}

func TestSyncShops_IndependentOfPeerPageSizeCeiling(t *testing.T) {
	t.Run("user service ceiling 100 with 250 shops syncs all 250", func(t *testing.T) {
		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			shops:   makeTestShops(250),
			peerCap: 100,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err != nil {
			t.Fatalf("syncShops failed: %v", err)
		}
		if len(rec.shops) != 250 {
			t.Fatalf("synced %d shops, want 250 (truncated by peer ceiling 100)", len(rec.shops))
		}
		if client.calls != 3 {
			t.Fatalf("ListPublicShops calls = %d, want 3", client.calls)
		}
	})

	t.Run("smaller peer ceiling 30 with 135 shops syncs all 135", func(t *testing.T) {
		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			shops:   makeTestShops(135),
			peerCap: 30,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err != nil {
			t.Fatalf("syncShops failed: %v", err)
		}
		if len(rec.shops) != 135 {
			t.Fatalf("synced %d shops, want 135 (truncated by peer ceiling 30)", len(rec.shops))
		}
		if client.calls != 5 {
			t.Fatalf("ListPublicShops calls = %d, want 5", client.calls)
		}
	})

	t.Run("stops on empty page when peer omits total", func(t *testing.T) {
		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			shops:     makeTestShops(65),
			peerCap:   30,
			omitTotal: true,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err != nil {
			t.Fatalf("syncShops failed: %v", err)
		}
		if len(rec.shops) != 65 {
			t.Fatalf("synced %d shops, want 65 when total is omitted", len(rec.shops))
		}
		if client.calls != 4 {
			t.Fatalf("ListPublicShops calls = %d, want 4 (3 non-empty + 1 empty terminator)", client.calls)
		}
	})

	t.Run("guards against infinite loop when peer repeats same page", func(t *testing.T) {
		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			shops:       makeTestShops(200),
			peerCap:     100,
			repeatPage1: true,
			maxCalls:    10,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err == nil {
			t.Fatalf("expected error when peer repeats identical page without forward progress")
		}
		if client.calls != 2 {
			t.Fatalf("expected syncShops to stop on page 2 after detecting zero progress, got %d calls", client.calls)
		}
	})

	t.Run("guards against infinite loop when peer keeps yielding new shops beyond maxShopSyncPages", func(t *testing.T) {
		origMax := maxShopSyncPages
		maxShopSyncPages = 5
		t.Cleanup(func() { maxShopSyncPages = origMax })

		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			infiniteMode: true,
			maxCalls:     20,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err == nil {
			t.Fatalf("expected error when peer exceeds maxShopSyncPages")
		}
		if client.calls != 5 {
			t.Fatalf("expected syncShops to stop at maxShopSyncPages=5, got %d calls", client.calls)
		}
	})

	t.Run("empty shop catalog with total=0 completes cleanly", func(t *testing.T) {
		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			shops:    nil,
			peerCap:  100,
			maxCalls: 5,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err != nil {
			t.Fatalf("syncShops on empty catalog failed: %v", err)
		}
		if len(rec.shops) != 0 {
			t.Fatalf("expected 0 synced shops, got %d", len(rec.shops))
		}
		if client.calls != 1 {
			t.Fatalf("expected 1 ListPublicShops call for empty catalog, got %d", client.calls)
		}
	})

	t.Run("overlapping pages stop by unique seen count instead of raw fetched count", func(t *testing.T) {
		all := makeTestShops(5)
		rec := newRecordingShopDBTX()
		client := &pagedUserClient{
			scriptedPages: [][]*userv1.Shop{
				{all[0], all[1]},
				{all[1], all[2]},
				{all[2], all[3]},
				{all[4]},
			},
			scriptedTotal: 5,
			maxCalls:      10,
		}
		idx := New(context.Background(), store.NewWithDBTX(rec), nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer idx.Stop()

		if err := idx.syncShops(context.Background()); err != nil {
			t.Fatalf("syncShops with overlapping pages failed: %v", err)
		}
		if len(rec.shops) != 5 {
			t.Fatalf("synced %d unique shops, want 5 (stopped early on raw fetched count)", len(rec.shops))
		}
		if client.calls != 4 {
			t.Fatalf("ListPublicShops calls = %d, want 4", client.calls)
		}
	})
}

func TestHandleShopReviewed_RequiresServiceTokenManager(t *testing.T) {
	client := &pagedUserClient{}
	idx := New(context.Background(), nil, nil, nil, nil, client, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer idx.Stop()

	if err := idx.HandleShopReviewed(context.Background(), "evt-approve-no-svc-auth", ShopReviewedInput{
		ShopID:   "shop-susp-7",
		ShopName: "Fallback Name",
		Decision: "APPROVE",
	}); err == nil {
		t.Fatalf("expected fail-closed error when svcAuth is nil")
	}

	client.mu.Lock()
	calls := client.getShopInternalCalls
	client.mu.Unlock()
	if calls != 0 {
		t.Fatalf("must not call GetShopInternal when svcAuth is nil, got %d calls", calls)
	}
}
