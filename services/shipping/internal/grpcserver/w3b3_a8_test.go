package grpcserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type countingCreateCarrier struct {
	carrier.Carrier
	createCalls atomic.Int32
}

func (c *countingCreateCarrier) CreateShipment(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
	c.createCalls.Add(1)
	return c.Carrier.CreateShipment(ctx, req)
}

func TestShippingGRPC_W3B3_G3_CreateShipmentServiceTokenAuth(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		carrier.NewMockExpressAdapter(),
	)
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	userMgr, err := auth.NewManager("test-w3b3-g3-user-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-w3b3-g3-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	shippingv1.RegisterShippingServiceServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(func() {
		grpcSrv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := shippingv1.NewShippingServiceClient(conn)

	signSvc := func(name string) context.Context {
		tok, sErr := svcMgr.SignServiceToken(name)
		if sErr != nil {
			t.Fatalf("SignServiceToken(%s): %v", name, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}
	signUser := func(uid, role, shopID string) context.Context {
		tok, _, _, sErr := userMgr.SignAccessToken(uid, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken(%s): %v", role, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}

	// Attack: non-order-service tokens MUST be rejected with PermissionDenied.
	for _, svcName := range []string{"product-service", "payment-service", "user-service", "ORDER-SERVICE", "order-service "} {
		_, err := client.CreateShipment(signSvc(svcName), &shippingv1.CreateShipmentRequest{
			OrderId:     "ord-g3-deny-" + svcName,
			ShopId:      "shop-1",
			CarrierCode: carrier.CodeMockExpress,
			WeightGrams: 500,
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("G3 attack: service %q CreateShipment code=%v, want PermissionDenied (err=%v)", svcName, status.Code(err), err)
		}
	}

	// Allowed: order-service token succeeds through full gRPC auth interceptor + handler.
	resp, err := client.CreateShipment(signSvc("order-service"), &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-g3-order-svc",
		ShopId:      "shop-1",
		BuyerId:     "buyer-1",
		CarrierCode: carrier.CodeMockExpress,
		ServiceCode: "STANDARD",
		WeightGrams: 750,
		FromAddress: &shippingv1.ShipmentAddress{
			Name:          "Kho Áo Thun Sài Gòn",
			Phone:         "0901234567",
			ProvinceCode:  "79",
			WardCode:      "26740",
			StreetAddress: "12 Nguyễn Huệ",
		},
		ToAddress: &shippingv1.ShipmentAddress{
			Name:          "Nguyễn Thị Ánh",
			Phone:         "0987654321",
			ProvinceCode:  "01",
			WardCode:      "00070",
			StreetAddress: "88 Phố Huế",
		},
	})
	if err != nil {
		t.Fatalf("G3: order-service CreateShipment failed: %v", err)
	}
	if resp.GetShipment() == nil || resp.GetShipment().GetTrackingCode() == "" {
		t.Fatalf("G3: expected created shipment, got %+v", resp)
	}
	savedSh, err := repo.GetShipmentByOrderID(context.Background(), "ord-g3-order-svc", store.DirectionForward)
	if err != nil {
		t.Fatalf("G3: load saved shipment: %v", err)
	}
	if savedSh.FromName != "Kho Áo Thun Sài Gòn" || savedSh.FromPhone != "0901234567" || savedSh.FromStreetAddress != "12 Nguyễn Huệ" ||
		savedSh.ToName != "Nguyễn Thị Ánh" || savedSh.ToPhone != "0987654321" || savedSh.ToStreetAddress != "88 Phố Huế" {
		t.Fatalf("G3: shipment address fields not persisted: %+v", savedSh)
	}

	// Seller wrong shop & unauthorized staff/buyer remain denied.
	if _, err := client.CreateShipment(signUser("seller-2", auth.RoleSeller, "shop-2"), &shippingv1.CreateShipmentRequest{
		OrderId: "ord-g3-wrong-shop", ShopId: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller other shop code=%v, want PermissionDenied", status.Code(err))
	}
	if _, err := client.CreateShipment(signUser("cskh-1", auth.RoleCSKH, ""), &shippingv1.CreateShipmentRequest{
		OrderId: "ord-g3-cskh", ShopId: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cskh code=%v, want PermissionDenied", status.Code(err))
	}
	if _, err := client.CreateShipment(signUser("ops-1", auth.RoleOps, ""), &shippingv1.CreateShipmentRequest{
		OrderId: "ord-g3-ops", ShopId: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
	}); err != nil {
		t.Fatalf("ops (orders.review.resolve) expected allow, got %v", err)
	}
}

func TestShippingGRPC_W3B3_G4_CreateShipmentEnqueueOnly(t *testing.T) {
	sellerCtx := ctxWithRole("seller-1", auth.RoleSeller, "shop-1")

	t.Run("MockCarrier_EnqueuesWithoutSyncCarrierCall_AndIdempotentAndAlreadyExisted", func(t *testing.T) {
		repo := newStubRepo()
		counting := &countingCreateCarrier{Carrier: carrier.NewMockExpressAdapter()}
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), counting), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)

		req := &shippingv1.CreateShipmentRequest{
			OrderId:      "ord-g4-enq-1",
			ShopId:       "shop-1",
			BuyerId:      "buyer-1",
			CarrierCode:  carrier.CodeMockExpress,
			ServiceCode:  "STANDARD",
			CodAmountVnd: 150000,
			WeightGrams:  600,
			EnqueueOnly:  true,
			FromAddress: &shippingv1.ShipmentAddress{
				Name: "Xưởng May Việt", Phone: "0909000111", ProvinceCode: "79", WardCode: "26740", StreetAddress: "10 Lê Lợi",
			},
			ToAddress: &shippingv1.ShipmentAddress{
				Name: "Trần Minh Đức", Phone: "0912345678", ProvinceCode: "01", WardCode: "00070", StreetAddress: "25 Hàng Bài",
			},
		}

		resp1, err := srv.CreateShipment(sellerCtx, req)
		if err != nil {
			t.Fatalf("G4 enqueue_only CreateShipment failed: %v", err)
		}
		if !resp1.GetQueued() || resp1.GetJobId() == "" || resp1.GetShipment() != nil || resp1.GetAlreadyExisted() {
			t.Fatalf("G4 enqueue_only want queued=true job_id!=empty shipment=nil, got %+v", resp1)
		}
		if counting.createCalls.Load() != 0 {
			t.Fatalf("G4 enqueue_only MUST NOT call carrier synchronously, got %d calls", counting.createCalls.Load())
		}
		// Verify full from_address / to_address persisted in shipment_create_jobs.request_json.
		savedJob, jobErr := repo.GetShipmentCreateJobByID(context.Background(), resp1.GetJobId())
		if jobErr != nil {
			t.Fatalf("load queued job: %v", jobErr)
		}
		var jobIn service.CreateShipmentInput
		if err := json.Unmarshal(savedJob.RequestJSON, &jobIn); err != nil {
			t.Fatalf("unmarshal job RequestJSON: %v", err)
		}
		if jobIn.FromAddress.Name != "Xưởng May Việt" || jobIn.FromAddress.Phone != "0909000111" || jobIn.FromAddress.StreetAddress != "10 Lê Lợi" ||
			jobIn.ToAddress.Name != "Trần Minh Đức" || jobIn.ToAddress.Phone != "0912345678" || jobIn.ToAddress.StreetAddress != "25 Hàng Bài" {
			t.Fatalf("G3/G4: job request_json missing full addresses: %+v", jobIn)
		}

		// Idempotent replay while queued returns same job_id without calling carrier.
		resp2, err := srv.CreateShipment(sellerCtx, req)
		if err != nil || !resp2.GetQueued() || resp2.GetJobId() != resp1.GetJobId() {
			t.Fatalf("G4 enqueue_only replay want same job_id=%s, got resp=%+v err=%v", resp1.GetJobId(), resp2, err)
		}
		if counting.createCalls.Load() != 0 {
			t.Fatalf("G4 replay MUST NOT call carrier synchronously, got %d calls", counting.createCalls.Load())
		}

		// When an active shipment already exists, enqueue_only returns already_existed=true and 0 new jobs.
		syncResp, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId:     "ord-g4-existing",
			ShopId:      "shop-1",
			CarrierCode: carrier.CodeMockExpress,
			WeightGrams: 500,
		})
		if err != nil || syncResp.GetShipment() == nil {
			t.Fatalf("seed existing shipment: resp=%+v err=%v", syncResp, err)
		}
		jobsBefore := len(repo.jobs)
		enqExisting, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId:     "ord-g4-existing",
			ShopId:      "shop-1",
			CarrierCode: carrier.CodeMockExpress,
			WeightGrams: 500,
			EnqueueOnly: true,
		})
		if err != nil {
			t.Fatalf("enqueue_only on existing shipment failed: %v", err)
		}
		if !enqExisting.GetAlreadyExisted() || enqExisting.GetQueued() || enqExisting.GetShipment() == nil {
			t.Fatalf("G4 existing shipment want already_existed=true queued=false, got %+v", enqExisting)
		}
		if len(repo.jobs) != jobsBefore {
			t.Fatalf("G4 existing shipment must create 0 new jobs, got %d -> %d", jobsBefore, len(repo.jobs))
		}
	})

	t.Run("GHNSandbox_MissingWardMapping_FailsPreconditionWithZeroJobsAndZeroHTTP", func(t *testing.T) {
		var httpHits atomic.Int32
		ghnSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httpHits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(ghnSrv.Close)

		repo := newStubRepo()
		ghn := carrier.NewGHNAdapter(carrier.GHNConfig{
			Mode:       "sandbox",
			BaseURL:    ghnSrv.URL,
			Token:      "sandbox-token",
			ShopID:     12345,
			HTTPClient: ghnSrv.Client(),
		})
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)

		// Missing GHN mapping (unmapped / unknown ward code) must fail immediately with FailedPrecondition, 0 jobs, 0 HTTP calls.
		_, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId:     "ord-g4-ghn-nomap",
			ShopId:      "shop-1",
			CarrierCode: carrier.CodeGHN,
			WeightGrams: 500,
			EnqueueOnly: true,
			ToAddress: &shippingv1.ShipmentAddress{
				Name: "Khách", Phone: "0901112223", ProvinceCode: "04", WardCode: "01273", StreetAddress: "Cao Bằng",
			},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("G4 GHN sandbox missing ward mapping want FailedPrecondition, got %v", err)
		}
		if len(repo.jobs) != 0 {
			t.Fatalf("G4 GHN sandbox missing ward mapping must persist 0 jobs, got %d", len(repo.jobs))
		}
		if httpHits.Load() != 0 {
			t.Fatalf("G4 GHN sandbox missing ward mapping must make 0 HTTP calls, got %d", httpHits.Load())
		}

		// Valid GHN-mapped ward (26740 Ben Nghe, HCMC) in GHN sandbox enqueues with 0 HTTP calls synchronously.
		respOK, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId:     "ord-g4-ghn-mapped",
			ShopId:      "shop-1",
			CarrierCode: carrier.CodeGHN,
			WeightGrams: 500,
			EnqueueOnly: true,
			ToAddress: &shippingv1.ShipmentAddress{
				Name: "Khách HCM", Phone: "0901112223", ProvinceCode: "79", WardCode: "26740", StreetAddress: "10 Đồng Khởi",
			},
		})
		if err != nil {
			t.Fatalf("G4 GHN sandbox valid mapped ward enqueue_only failed: %v", err)
		}
		if !respOK.GetQueued() || respOK.GetJobId() == "" {
			t.Fatalf("G4 GHN sandbox valid mapped ward want queued=true, got %+v", respOK)
		}
		if httpHits.Load() != 0 {
			t.Fatalf("G4 GHN sandbox enqueue_only must make 0 sync HTTP calls, got %d", httpHits.Load())
		}
	})
}
