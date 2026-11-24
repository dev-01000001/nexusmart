package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/migrations"
)

func openTempPostgresStore(t *testing.T) (*PostgresStore, *pgxpool.Pool) {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres SQL tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse TEST_ORDER_DB_URL: %v", err)
	}

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}

	tempDBName := "order_r4_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+tempDBName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create temp database %s: %v", tempDBName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *parsed
	testURL.Path = "/" + tempDBName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropTempDatabase(adminDSN, tempDBName)
		t.Fatalf("open temp db pool %s: %v", tempDBName, err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropTempDatabase(adminDSN, tempDBName)
	})

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations on %s: %v", tempDBName, err)
	}

	return NewPostgresStore(pool), pool
}

func dropTempDatabase(adminDSN, dbName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
}

func pgComputeSettlementSplit(r, cTotal, ship, alreadyRefunded int64) (sellerNet, effComm, effShip int64) {
	rawNet := r - cTotal
	sellerDeduct := min64(alreadyRefunded, rawNet)
	sellerNet = rawNet - sellerDeduct
	remAfterSeller := alreadyRefunded - sellerDeduct

	commDeduct := min64(remAfterSeller, cTotal)
	effComm = cTotal - commDeduct
	remAfterComm := remAfterSeller - commDeduct

	shipDeduct := min64(remAfterComm, ship)
	effShip = ship - shipDeduct
	return sellerNet, effComm, effShip
}

func seedPGCheckoutGroupWithOrder(
	t *testing.T,
	st *PostgresStore,
	groupID, buyerID, shopID, idemKey string,
	ordParam db.InsertOrderParams,
	now time.Time,
) OrderAggregate {
	t.Helper()
	ctx := context.Background()
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID:              groupID,
		BuyerID:         buyerID,
		IdempotencyKey:  idemKey,
		RequestHash:     "hash-" + idemKey,
		Status:          "IN_PROGRESS",
		PaymentMethod:   ordParam.PaymentMethod,
		TotalPayableVND: ordParam.TotalPayableVND,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup: %v", err)
	}
	ordParam.CheckoutGroupID = groupID
	ordParam.BuyerID = buyerID
	ordParam.ShopID = shopID
	if len(ordParam.ShippingAddressJSON) == 0 {
		ordParam.ShippingAddressJSON = []byte(`{"recipient_name":"Test"}`)
	}
	aggs, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: groupID,
		Now:             now,
		Orders: []NewOrderRecord{
			{
				Order: ordParam,
				Items: []db.InsertOrderItemParams{
					{
						ID:                    uuid.NewString(),
						OrderID:               ordParam.ID,
						SKUID:                 "sku-" + shopID,
						ProductID:             "prod-" + shopID,
						ProductName:           "Product " + shopID,
						SKUName:               "Default",
						UnitPriceVND:          ordParam.SubtotalVND,
						EffectiveUnitPriceVND: ordParam.SubtotalVND,
						Quantity:              1,
						LineTotalVND:          ordParam.SubtotalVND,
						WeightGrams:           500,
						CommissionRateBPS:     500,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PersistCheckoutBatch: %v", err)
	}
	return aggs[0]
}

// TestPostgresR4_A_CompleteCheckoutRaceAndRecovery exercises item A SQL on real Postgres:
//   - Non-downgrading CompleteCheckoutWithPayment + fresh response_snapshot_json when SUCCEEDED/CANCELLED raced ahead.
//   - Concurrent CompleteCheckoutWithPayment vs TransitionOrdersPaymentResult with identical lock order (zero deadlocks).
//   - FindIncompleteCheckoutGroupsWithOrders + MarkCheckoutGroupNeedsCleanup / FindStaleInProgressCheckoutGroups.
func TestPostgresR4_A_CompleteCheckoutRaceAndRecovery(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("NoDowngradeWhenPaymentSucceededRacesAhead", func(t *testing.T) {
		groupID := uuid.NewString()
		ordID := uuid.NewString()
		exp := now.Add(15 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, groupID, "buyer-pg-1", "shop-pg-1", "idem-pg-succ", db.InsertOrderParams{
			ID:               ordID,
			OrderCode:        "NM-PG-SUCC-1",
			Status:           "PENDING",
			PaymentMethod:    "VIETQR",
			PaymentID:        "",
			PaymentStatus:    "PENDING",
			SubtotalVND:      100000,
			ShippingFeeVND:   30000,
			CommissionVND:    5000,
			PaymentFeeVND:    2600,
			PaymentFeeBPS:    200,
			TotalPayableVND:  130000,
			PaymentExpiresAt: &exp,
		}, now)

		// Racing payment.succeeded commits BEFORE CompleteCheckoutWithPayment.
		if _, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
			EventID:         "evt-pg-race-succ",
			EventType:       "payment.succeeded",
			CheckoutGroupID: groupID,
			PaymentID:       "pay-pg-1",
			ResultStatus:    orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
			Reason:          "webhook raced ahead",
			ActorID:         "payment-service",
			ActorRole:       "system",
			Now:             now,
		}); err != nil {
			t.Fatalf("TransitionOrdersPaymentResult: %v", err)
		}

		// Now CompleteCheckoutWithPayment runs with stale PENDING from CreatePayment response.
		err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID:  groupID,
			PaymentID:        "pay-pg-1",
			PaymentStatus:    "PENDING",
			QRURL:            "https://qr.example/1",
			QRCode:           "QR1",
			PaymentExpiresAt: &exp,
			BuildSnapshot: func(grp db.CheckoutGroup, aggs []OrderAggregate) ([]byte, error) {
				return json.Marshal(map[string]any{
					"group_payment_status": grp.PaymentStatus,
					"order_status":         aggs[0].Order.Status,
					"order_payment_status": aggs[0].Order.PaymentStatus,
				})
			},
		})
		if err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}

		agg, err := st.GetOrderWithDetails(ctx, ordID)
		if err != nil {
			t.Fatalf("GetOrderWithDetails: %v", err)
		}
		if agg.Order.Status != "CONFIRMED" || agg.Order.PaymentStatus != "SUCCEEDED" || agg.Order.PaymentID != "pay-pg-1" {
			t.Fatalf("order downgraded: status=%s payStatus=%s payID=%s", agg.Order.Status, agg.Order.PaymentStatus, agg.Order.PaymentID)
		}
		grp, err := st.GetCheckoutGroupByID(ctx, groupID)
		if err != nil {
			t.Fatalf("GetCheckoutGroupByID: %v", err)
		}
		if grp.Status != "COMPLETED" || grp.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("group downgraded: status=%s payStatus=%s", grp.Status, grp.PaymentStatus)
		}
		var snap map[string]string
		if err := json.Unmarshal(grp.ResponseSnapshotJSON, &snap); err != nil {
			t.Fatalf("unmarshal snapshot: %v", err)
		}
		if snap["group_payment_status"] != "SUCCEEDED" || snap["order_status"] != "CONFIRMED" || snap["order_payment_status"] != "SUCCEEDED" {
			t.Fatalf("snapshot did not reflect SUCCEEDED/CONFIRMED: %+v", snap)
		}
	})

	t.Run("CancelledOrderUntouchedAndSnapshotReflectsCancelled", func(t *testing.T) {
		// Case 1 (D11 extended / P1-1): When ALL orders in group are CANCELLED, CompleteCheckoutWithPayment
		// leaves the cancelled order untouched and closes the group FAILED with no COMPLETED snapshot and
		// pending_stock_confirm = FALSE.
		groupID := uuid.NewString()
		ordID := uuid.NewString()
		exp := now.Add(15 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, groupID, "buyer-pg-2", "shop-pg-1", "idem-pg-cancel", db.InsertOrderParams{
			ID:               ordID,
			OrderCode:        "NM-PG-CAN-1",
			Status:           "PENDING",
			PaymentMethod:    "VIETQR",
			PaymentID:        "",
			PaymentStatus:    "PENDING",
			SubtotalVND:      100000,
			ShippingFeeVND:   30000,
			TotalPayableVND:  130000,
			PaymentExpiresAt: &exp,
		}, now)

		if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
			OrderID:       ordID,
			TargetStatus:  orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			CancelReason:  "buyer cancelled mid-checkout",
			PaymentStatus: "CANCELLED",
			ActorID:       "buyer-pg-2",
			ActorRole:     "buyer",
			Note:          "cancelled",
			Now:           now,
		}); err != nil {
			t.Fatalf("TransitionOrderStatus cancel: %v", err)
		}

		var observedGrp db.CheckoutGroup
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID:  groupID,
			PaymentID:        "pay-pg-can",
			PaymentStatus:    "PENDING",
			PaymentExpiresAt: &exp,
			BuildSnapshot: func(grp db.CheckoutGroup, aggs []OrderAggregate) ([]byte, error) {
				observedGrp = grp
				return json.Marshal(map[string]string{
					"group_payment_status": grp.PaymentStatus,
					"order_status":         aggs[0].Order.Status,
					"order_payment_status": aggs[0].Order.PaymentStatus,
				})
			},
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}

		agg, _ := st.GetOrderWithDetails(ctx, ordID)
		if agg.Order.Status != "CANCELLED" || agg.Order.PaymentStatus != "CANCELLED" || agg.Order.PaymentID != "" {
			t.Fatalf("cancelled order was mutated: status=%s payStatus=%s payID=%q", agg.Order.Status, agg.Order.PaymentStatus, agg.Order.PaymentID)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, groupID)
		if grp.Status != "FAILED" || observedGrp.Status != "FAILED" || grp.PaymentStatus != "CANCELLED" || grp.PendingStockConfirm || len(grp.ResponseSnapshotJSON) != 0 {
			t.Fatalf("D11 extended violation: want FAILED/CANCELLED/pending_stock=false/empty snapshot, got status=%q obs=%q pay=%q pendStock=%v snap=%q",
				grp.Status, observedGrp.Status, grp.PaymentStatus, grp.PendingStockConfirm, string(grp.ResponseSnapshotJSON))
		}

		// Case 2 (partial cancel in multi-order group): 1 cancelled + 1 pending still closes COMPLETED
		// with a fresh snapshot reflecting CANCELLED on order 1 and PENDING on order 2.
		group2 := uuid.NewString()
		ordA := uuid.NewString()
		ordB := uuid.NewString()
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: group2, BuyerID: "buyer-pg-2", IdempotencyKey: "idem-pg-partial-cancel",
			RequestHash: "h-partial", Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 260000,
		})
		if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: group2,
			Orders: []NewOrderRecord{
				{
					Order: db.InsertOrderParams{
						ID: ordA, OrderCode: "NM-PG-PC-A", CheckoutGroupID: group2, BuyerID: "buyer-pg-2", ShopID: "shop-pg-1",
						Status: "PENDING", PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
						SubtotalVND: 100000, ShippingFeeVND: 30000, TotalPayableVND: 130000, PaymentExpiresAt: &exp,
						ShippingAddressJSON: []byte(`{"province_code":"79"}`),
					},
					Items: []db.InsertOrderItemParams{{ID: uuid.NewString(), OrderID: ordA, SKUID: "sku-a", ProductID: "p-a", ProductName: "A", SKUName: "Std", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, Quantity: 1, LineTotalVND: 100000}},
				},
				{
					Order: db.InsertOrderParams{
						ID: ordB, OrderCode: "NM-PG-PC-B", CheckoutGroupID: group2, BuyerID: "buyer-pg-2", ShopID: "shop-pg-2",
						Status: "PENDING", PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
						SubtotalVND: 100000, ShippingFeeVND: 30000, TotalPayableVND: 130000, PaymentExpiresAt: &exp,
						ShippingAddressJSON: []byte(`{"province_code":"79"}`),
					},
					Items: []db.InsertOrderItemParams{{ID: uuid.NewString(), OrderID: ordB, SKUID: "sku-b", ProductID: "p-b", ProductName: "B", SKUName: "Std", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, Quantity: 1, LineTotalVND: 100000}},
				},
			},
		}); err != nil {
			t.Fatalf("PersistCheckoutBatch(partial): %v", err)
		}
		if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
			OrderID: ordA, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			CancelReason: "partial cancel", PaymentStatus: "CANCELLED",
			ActorID: "buyer-pg-2", ActorRole: "buyer", Note: "cancelled", Now: now,
		}); err != nil {
			t.Fatalf("TransitionOrderStatus cancel ordA: %v", err)
		}
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: group2, PaymentID: "pay-pg-partial", PaymentStatus: "PENDING",
			PaymentExpiresAt: &exp,
			BuildSnapshot: func(_ db.CheckoutGroup, aggs []OrderAggregate) ([]byte, error) {
				statuses := map[string]string{}
				for _, a := range aggs {
					statuses[a.Order.ID] = a.Order.Status
				}
				return json.Marshal(statuses)
			},
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment(partial): %v", err)
		}
		grp2, _ := st.GetCheckoutGroupByID(ctx, group2)
		if grp2.Status != "COMPLETED" {
			t.Fatalf("partial cancel must stay COMPLETED, got %q", grp2.Status)
		}
		var snap2 map[string]string
		_ = json.Unmarshal(grp2.ResponseSnapshotJSON, &snap2)
		if snap2[ordA] != "CANCELLED" || snap2[ordB] != "PENDING" {
			t.Fatalf("partial cancel snapshot mismatch: %+v", snap2)
		}
	})

	t.Run("ConcurrentCompleteAndPaymentResult_ZeroDeadlocks", func(t *testing.T) {
		// Item 9 fake-test fix #2: seed >= 2 sub-orders in reverse shop order inside the same checkout_group
		// and race CompleteCheckoutWithPayment against TransitionOrdersPaymentResult (and a third concurrent
		// reader/writer) so multi-row AB-BA deadlocks would genuinely trigger if lock orders diverged.
		for iter := 0; iter < 10; iter++ {
			groupID := uuid.NewString()
			payID := fmt.Sprintf("pay-race-%d", iter)
			exp := now.Add(15 * time.Minute)

			if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
				ID:              groupID,
				BuyerID:         "buyer-race",
				IdempotencyKey:  fmt.Sprintf("idem-race-%d", iter),
				RequestHash:     fmt.Sprintf("hash-race-%d", iter),
				Status:          "IN_PROGRESS",
				PaymentMethod:   "VIETQR",
				TotalPayableVND: 360000,
			}); err != nil {
				t.Fatalf("iter %d CreateCheckoutGroup: %v", iter, err)
			}

			// Insert 3 sub-orders in reverse shop_id order (shop-c, shop-b, shop-a) to stress canonical lock sorting.
			ordIDs := make([]string, 0, 3)
			var recs []NewOrderRecord
			for idx, shopID := range []string{"shop-c", "shop-b", "shop-a"} {
				oid := uuid.NewString()
				ordIDs = append(ordIDs, oid)
				recs = append(recs, NewOrderRecord{
					Order: db.InsertOrderParams{
						ID:                  oid,
						OrderCode:           fmt.Sprintf("NM-RACE-%02d-%d", iter, idx),
						CheckoutGroupID:     groupID,
						BuyerID:             "buyer-race",
						ShopID:              shopID,
						Status:              "PENDING",
						PaymentMethod:       "VIETQR",
						PaymentID:           "",
						PaymentStatus:       "PENDING",
						SubtotalVND:         100000,
						ShippingFeeVND:      20000,
						TotalPayableVND:     120000,
						ShippingAddressJSON: []byte(`{"recipient_name":"Race"}`),
						PaymentExpiresAt:    &exp,
					},
					Items: []db.InsertOrderItemParams{{
						ID: uuid.NewString(), OrderID: oid, SKUID: "sku-" + shopID, ProductID: "prod-" + shopID,
						ProductName: "P", SKUName: "S", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000,
						Quantity: 1, LineTotalVND: 100000, WeightGrams: 500, CommissionRateBPS: 500,
					}},
				})
			}
			if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
				CheckoutGroupID: groupID,
				Orders:          recs,
				Now:             now,
			}); err != nil {
				t.Fatalf("iter %d PersistCheckoutBatch: %v", iter, err)
			}

			var wg sync.WaitGroup
			start := make(chan struct{})
			errCh := make(chan error, 3)

			wg.Add(3)
			go func() {
				defer wg.Done()
				<-start
				errCh <- st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
					CheckoutGroupID:  groupID,
					PaymentID:        payID,
					PaymentStatus:    "PENDING",
					PaymentExpiresAt: &exp,
					BuildSnapshot: func(grp db.CheckoutGroup, aggs []OrderAggregate) ([]byte, error) {
						return json.Marshal(map[string]string{"status": grp.PaymentStatus})
					},
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				_, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
					EventID:         fmt.Sprintf("evt-conc-%d", iter),
					EventType:       "payment.succeeded",
					CheckoutGroupID: groupID,
					PaymentID:       payID,
					ResultStatus:    orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
					Reason:          "concurrent webhook",
					ActorID:         "payment-service",
					ActorRole:       "system",
					Now:             now,
				})
				errCh <- err
			}()
			// Third goroutine concurrently locks in reverse-order pressure via CancelCheckoutGroupOrders
			// (which also locks orders -> checkout_groups and skips paid orders).
			go func() {
				defer wg.Done()
				<-start
				_, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
					CheckoutGroupID: groupID,
					Reason:          "concurrent cancel probe",
					ActorID:         "test",
					ActorRole:       "system",
					PaymentStatus:   "FAILED",
					OnlyPending:     true,
				})
				errCh <- err
			}()

			close(start)
			wg.Wait()
			close(errCh)
			for err := range errCh {
				if err != nil {
					t.Fatalf("iter %d concurrent error (possible AB-BA deadlock across 3 sub-orders): %v", iter, err)
				}
			}

			// Verify all 3 sub-orders converged cleanly and remain strictly consistent with checkout_groups.status
			// (Gap 5c: catches M4 where COMPLETED group had its orders cancelled).
			grpFinal, gErr := st.GetCheckoutGroupByID(ctx, groupID)
			if gErr != nil {
				t.Fatalf("iter %d GetCheckoutGroupByID: %v", iter, gErr)
			}
			var firstStatus string
			for _, oid := range ordIDs {
				agg, _ := st.GetOrderWithDetails(ctx, oid)
				if agg.Order.Status != "CONFIRMED" && agg.Order.Status != "CANCELLED" {
					t.Fatalf("iter %d order %s unexpected status %s", iter, oid, agg.Order.Status)
				}
				if firstStatus == "" {
					firstStatus = agg.Order.Status
				} else if agg.Order.Status != firstStatus {
					t.Fatalf("iter %d split-brain across sub-orders: %s vs %s", iter, firstStatus, agg.Order.Status)
				}
			}
			if grpFinal.Status == "COMPLETED" && firstStatus != "CONFIRMED" {
				t.Fatalf("iter %d M4 consistency violation: checkout_group is COMPLETED but sub-orders are %s", iter, firstStatus)
			}
			if grpFinal.Status == "FAILED" && firstStatus != "CANCELLED" {
				t.Fatalf("iter %d consistency violation: checkout_group is FAILED but sub-orders are %s", iter, firstStatus)
			}
		}
	})

	t.Run("WatchdogIncompleteAndNeedsCleanupQueries", func(t *testing.T) {
		// 1. Group with orders stuck IN_PROGRESS past stale cutoff -> returned by FindIncompleteCheckoutGroupsWithOrders.
		incGroupID := uuid.NewString()
		seedPGCheckoutGroupWithOrder(t, st, incGroupID, "buyer-inc", "shop-inc", "idem-inc", db.InsertOrderParams{
			ID:              uuid.NewString(),
			OrderCode:       "NM-INC-1",
			Status:          "PENDING",
			PaymentMethod:   "VIETQR",
			PaymentStatus:   "PENDING",
			SubtotalVND:     100000,
			ShippingFeeVND:  20000,
			TotalPayableVND: 120000,
		}, now)
		if _, err := pool.Exec(ctx, `UPDATE checkout_groups SET updated_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, incGroupID); err != nil {
			t.Fatalf("backdate updated_at: %v", err)
		}

		foundInc, err := st.FindIncompleteCheckoutGroupsWithOrders(ctx, 5*time.Minute, 20)
		if err != nil {
			t.Fatalf("FindIncompleteCheckoutGroupsWithOrders: %v", err)
		}
		if len(foundInc) != 1 || foundInc[0] != incGroupID {
			t.Fatalf("FindIncompleteCheckoutGroupsWithOrders = %v, want [%s]", foundInc, incGroupID)
		}

		// 2. Group without orders flagged needs_cleanup = TRUE -> immediately returned by FindStaleInProgressCheckoutGroups.
		cleanupGroupID := uuid.NewString()
		if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID:              cleanupGroupID,
			BuyerID:         "buyer-clean",
			IdempotencyKey:  "idem-clean",
			RequestHash:     "hash-clean",
			Status:          "IN_PROGRESS",
			PaymentMethod:   "VIETQR",
			TotalPayableVND: 0,
		}); err != nil {
			t.Fatalf("CreateCheckoutGroup cleanup: %v", err)
		}
		if err := st.MarkCheckoutGroupNeedsCleanup(ctx, cleanupGroupID, "PROMOTION_RESERVE_FAILED"); err != nil {
			t.Fatalf("MarkCheckoutGroupNeedsCleanup: %v", err)
		}
		staleIDs, err := st.FindStaleInProgressCheckoutGroups(ctx, 5*time.Minute, 20)
		if err != nil {
			t.Fatalf("FindStaleInProgressCheckoutGroups: %v", err)
		}
		if len(staleIDs) != 1 || staleIDs[0] != cleanupGroupID {
			t.Fatalf("FindStaleInProgressCheckoutGroups = %v, want [%s]", staleIDs, cleanupGroupID)
		}
		if err := st.FailCheckoutGroup(ctx, cleanupGroupID, "STALE_CHECKOUT_EXPIRED"); err != nil {
			t.Fatalf("FailCheckoutGroup: %v", err)
		}
		staleAfter, err := st.FindStaleInProgressCheckoutGroups(ctx, 5*time.Minute, 20)
		if err != nil || len(staleAfter) != 0 {
			t.Fatalf("expected 0 stale groups after FailCheckoutGroup, got %v, err=%v", staleAfter, err)
		}
	})
}

// TestPostgresR4_DE_ReportWaterfallGMVAndBackfill exercises items D & E SQL directly on real Postgres:
//   - UpdateOrderCompleted snapshots settlement_refunded_vnd = refunded_vnd atomically.
//   - ReportSellerCompletedDaily / ReportPlatformCompletedDaily waterfall matches compute_settlement_split across all 5 cases.
//   - ReportSellerCreatedDaily / ReportPlatformCreatedDaily gmv_vnd = SUM(P + D) excluding PENDING/CANCELLED/RETURNED.
func TestPostgresR4_DE_ReportWaterfallGMVAndBackfill(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()

	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	dayStart := time.Date(2026, 11, 23, 0, 0, 0, 0, loc).UTC()
	dayEnd := dayStart.Add(24 * time.Hour)
	dayNoon := dayStart.Add(12 * time.Hour)

	t.Run("SettlementWaterfallMatchesComputeSettlementSplit", func(t *testing.T) {
		cases := []struct {
			name          string
			preRefundVND  int64
			postRefundVND int64
			wantNetVND    int64
			wantCommVND   int64
			wantPayFeeVND int64
		}{
			{"pre_below_net", 40000, 0, 52400, 5000, 2600},
			{"pre_eats_fees", 96000, 0, 0, 4000, 0},
			{"pre_touches_ship", 110000, 0, 0, 0, 0},
			{"pre_and_post", 40000, 30000, 22400, 5000, 2600},
			{"post_full_refund", 0, 100000, -7600, 5000, 2600},
		}

		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				shopID := fmt.Sprintf("shop-wf-%d", i)
				groupID := uuid.NewString()
				ordID := uuid.NewString()
				payID := fmt.Sprintf("pay-wf-%d", i)

				// Base order: G=R=100,000, Ship=30,000, V_plat=10,000, S_free=5,000, X=5,000 (D=20,000), P=110,000.
				// C_cat=5,000, C_pay=2,600 => C_total=7,600, raw_net=92,400.
				seedPGCheckoutGroupWithOrder(t, st, groupID, "buyer-wf", shopID, "idem-wf-"+tc.name, db.InsertOrderParams{
					ID:                         ordID,
					OrderCode:                  fmt.Sprintf("NM-WF-%02d", i),
					Status:                     "DELIVERED",
					PaymentMethod:              "VIETQR",
					PaymentID:                  payID,
					PaymentStatus:              "SUCCEEDED",
					SubtotalVND:                100000,
					PlatformVoucherDiscountVND: 10000,
					FreeshipDiscountVND:        5000,
					CoinsDiscountVND:           5000,
					ShippingFeeVND:             30000,
					CommissionVND:              5000,
					PaymentFeeVND:              2600,
					PaymentFeeBPS:              200,
					TotalPayableVND:            110000,
					ConfirmedAt:                &dayNoon,
				}, dayNoon)

				// Record pre-settlement refund before COMPLETED.
				if tc.preRefundVND > 0 {
					if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
						EventID:   "evt-pre-" + tc.name,
						EventType: "payment.refunded",
						PaymentID: payID,
						OrderID:   ordID,
						RefundID:  "ref-pre-" + tc.name,
						AmountVND: tc.preRefundVND,
						Reason:    "pre-settlement partial refund",
					}); err != nil {
						t.Fatalf("pre RecordPaymentRefunded: %v", err)
					}
				}

				// Complete order -> SQL UpdateOrderCompleted snapshots settlement_refunded_vnd = refunded_vnd.
				completedAgg, err := st.TransitionOrderCompleted(ctx, CompleteOrderParams{
					OrderID:   ordID,
					ActorID:   "buyer-wf",
					ActorRole: "buyer",
					Note:      "confirmed received",
					OutboxEvent: db.InsertOutboxEventParams{
						ID:          uuid.NewString(),
						EventType:   "order.completed",
						PayloadJSON: []byte(`{}`),
					},
					Now: dayNoon,
				})
				if err != nil {
					t.Fatalf("complete order: %v", err)
				}
				if completedAgg.Order.SettlementRefundedVND != tc.preRefundVND {
					t.Fatalf("settlement_refunded_vnd = %d, want %d", completedAgg.Order.SettlementRefundedVND, tc.preRefundVND)
				}

				// Record post-settlement refund AFTER COMPLETED.
				if tc.postRefundVND > 0 {
					if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
						EventID:   "evt-post-" + tc.name,
						EventType: "payment.refunded",
						PaymentID: payID,
						OrderID:   ordID,
						RefundID:  "ref-post-" + tc.name,
						AmountVND: tc.postRefundVND,
						Reason:    "post-settlement refund",
					}); err != nil {
						t.Fatalf("post RecordPaymentRefunded: %v", err)
					}
				}

				// Cross-check against payment's compute_settlement_split formula.
				refSeller, refEffComm, _ := pgComputeSettlementSplit(100000, 7600, 30000, tc.preRefundVND)
				if refSeller-tc.postRefundVND != tc.wantNetVND || tc.wantCommVND+tc.wantPayFeeVND != refEffComm {
					t.Fatalf("reference mismatch: refNet=%d refEffComm=%d", refSeller-tc.postRefundVND, refEffComm)
				}

				// Pin created_at & completed_at inside dayStart..dayEnd for deterministic report query.
				if _, err := pool.Exec(ctx, `UPDATE orders SET created_at = $2, completed_at = $2 WHERE id = $1`, ordID, dayNoon); err != nil {
					t.Fatalf("pin timestamps: %v", err)
				}

				rep, err := st.QueryOrderReport(ctx, OrderReportParams{
					ShopID:   shopID,
					StartTS:  dayStart,
					EndTS:    dayEnd,
					TopLimit: 5,
				})
				if err != nil {
					t.Fatalf("QueryOrderReport(seller): %v", err)
				}
				if len(rep.CompletedDaily) != 1 {
					t.Fatalf("expected 1 CompletedDaily row, got %d", len(rep.CompletedDaily))
				}
				row := rep.CompletedDaily[0]
				if row.NetRevenueVND != tc.wantNetVND || row.CommissionVND != tc.wantCommVND || row.PaymentFeeVND != tc.wantPayFeeVND {
					t.Fatalf("Postgres seller report got net=%d comm=%d payFee=%d, want %d/%d/%d",
						row.NetRevenueVND, row.CommissionVND, row.PaymentFeeVND, tc.wantNetVND, tc.wantCommVND, tc.wantPayFeeVND)
				}
				if row.PlatformSubsidyVND != 0 {
					t.Fatalf("seller report PlatformSubsidyVND = %d, want 0 (platform-only metric)", row.PlatformSubsidyVND)
				}
			})
		}

		// Verify platform aggregate across all 5 cases equals the exact sum of the 5 seller reports.
		platRep, err := st.QueryOrderReport(ctx, OrderReportParams{
			ShopID:   "",
			StartTS:  dayStart,
			EndTS:    dayEnd,
			TopLimit: 10,
		})
		if err != nil {
			t.Fatalf("QueryOrderReport(platform): %v", err)
		}
		if len(platRep.CompletedDaily) != 1 {
			t.Fatalf("expected 1 platform CompletedDaily row, got %d", len(platRep.CompletedDaily))
		}
		pr := platRep.CompletedDaily[0]
		// Sums across 5 cases: net = 52400+0+0+22400-7600 = 67200; comm = 5000+4000+0+5000+5000 = 19000; payFee = 2600+0+0+2600+2600 = 7800; subsidy = 5*20000 = 100000
		if pr.NetRevenueVND != 67200 || pr.CommissionVND != 19000 || pr.PaymentFeeVND != 7800 || pr.PlatformSubsidyVND != 100000 {
			t.Fatalf("Postgres platform report got net=%d comm=%d payFee=%d subsidy=%d, want 67200/19000/7800/100000",
				pr.NetRevenueVND, pr.CommissionVND, pr.PaymentFeeVND, pr.PlatformSubsidyVND)
		}
	})

	t.Run("GMVExcludesPendingCancelledReturnedAndIncludesPlatformSubsidy", func(t *testing.T) {
		gmvDayStart := time.Date(2026, 11, 24, 0, 0, 0, 0, loc).UTC()
		gmvDayEnd := gmvDayStart.Add(24 * time.Hour)
		gmvNoon := gmvDayStart.Add(12 * time.Hour)
		shopID := "shop-gmv-pg"

		entries := []struct {
			code       string
			status     string
			payableVND int64
			vPlat      int64
			sFree      int64
			coins      int64
		}{
			{"NM-GMV-PEND", "PENDING", 230000, 0, 0, 0},               // excluded
			{"NM-GMV-CANC", "CANCELLED", 230000, 0, 0, 0},             // excluded
			{"NM-GMV-RET", "RETURNED", 230000, 0, 0, 0},               // excluded
			{"NM-GMV-CONF", "CONFIRMED", 180000, 30000, 10000, 10000}, // P+D = 230,000
			{"NM-GMV-ZERO", "COMPLETED", 0, 150000, 30000, 50000},     // P+D = 230,000
		}

		for _, e := range entries {
			gid := uuid.NewString()
			oid := uuid.NewString()
			seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-gmv", shopID, "idem-"+e.code, db.InsertOrderParams{
				ID:                         oid,
				OrderCode:                  e.code,
				Status:                     e.status,
				PaymentMethod:              "VIETQR",
				PaymentStatus:              "SUCCEEDED",
				SubtotalVND:                200000,
				PlatformVoucherDiscountVND: e.vPlat,
				FreeshipDiscountVND:        e.sFree,
				CoinsDiscountVND:           e.coins,
				ShippingFeeVND:             30000,
				TotalPayableVND:            e.payableVND,
			}, gmvNoon)
			if _, err := pool.Exec(ctx, `UPDATE orders SET created_at = $2 WHERE id = $1`, oid, gmvNoon); err != nil {
				t.Fatalf("pin created_at for %s: %v", e.code, err)
			}
		}

		sRep, err := st.QueryOrderReport(ctx, OrderReportParams{
			ShopID:   shopID,
			StartTS:  gmvDayStart,
			EndTS:    gmvDayEnd,
			TopLimit: 5,
		})
		if err != nil {
			t.Fatalf("QueryOrderReport(seller gmv): %v", err)
		}
		if len(sRep.CreatedDaily) != 1 || sRep.CreatedDaily[0].GMVVND != 460000 {
			t.Fatalf("seller GMV = %+v, want 460000 (CONFIRMED 230k + COMPLETED 0-VND 230k)", sRep.CreatedDaily)
		}

		pRep, err := st.QueryOrderReport(ctx, OrderReportParams{
			ShopID:   "",
			StartTS:  gmvDayStart,
			EndTS:    gmvDayEnd,
			TopLimit: 5,
		})
		if err != nil {
			t.Fatalf("QueryOrderReport(platform gmv): %v", err)
		}
		if len(pRep.CreatedDaily) != 1 || pRep.CreatedDaily[0].GMVVND != 460000 {
			t.Fatalf("platform GMV = %+v, want 460000", pRep.CreatedDaily)
		}
	})

	t.Run("Migration0011_BackfillsLegacyCompletedOrder", func(t *testing.T) {
		// Verify applying 0011 over an existing 0010 database backfills settlement_refunded_vnd = refunded_vnd
		// ONLY for legacy COMPLETED orders and preserves non-COMPLETED rows (DELIVERED, CANCELLED) at 0 (Item 9 fix #4).
		if _, err := pool.Exec(ctx, `
			ALTER TABLE orders DROP COLUMN settlement_refunded_vnd;
			ALTER TABLE checkout_groups DROP COLUMN needs_cleanup;
			DROP INDEX IF EXISTS idx_checkout_groups_in_progress_updated_at;
		`); err != nil {
			t.Fatalf("rewind 0011 columns: %v", err)
		}

		legacyID := uuid.NewString()
		deliveredID := uuid.NewString()
		cancelledID := uuid.NewString()
		legacyGrp := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO checkout_groups (id, buyer_id, idempotency_key, request_hash, status, payment_method, total_payable_vnd)
			VALUES ($1, 'buyer-leg', 'idem-leg', 'hash-leg', 'COMPLETED', 'VIETQR', 130000)
		`, legacyGrp); err != nil {
			t.Fatalf("insert legacy checkout group: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (
				id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_status,
				subtotal_vnd, shipping_fee_vnd, commission_vnd, payment_fee_vnd, total_payable_vnd, refunded_vnd,
				shipping_address_json, completed_at
			) VALUES (
				$2, 'NM-LEGACY-1', $1, 'buyer-leg', 'shop-leg', 'COMPLETED', 'VIETQR', 'SUCCEEDED',
				100000, 30000, 5000, 2600, 130000, 40000, '{}'::jsonb, $3
			)
		`, legacyGrp, legacyID, dayNoon); err != nil {
			t.Fatalf("insert legacy completed order: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (
				id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_status,
				subtotal_vnd, shipping_fee_vnd, commission_vnd, payment_fee_vnd, total_payable_vnd, refunded_vnd,
				shipping_address_json, delivered_at
			) VALUES (
				$2, 'NM-LEGACY-DEL', $1, 'buyer-leg', 'shop-leg', 'DELIVERED', 'VIETQR', 'SUCCEEDED',
				100000, 30000, 5000, 2600, 130000, 25000, '{}'::jsonb, $3
			)
		`, legacyGrp, deliveredID, dayNoon); err != nil {
			t.Fatalf("insert legacy delivered order: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (
				id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_status,
				subtotal_vnd, shipping_fee_vnd, commission_vnd, payment_fee_vnd, total_payable_vnd, refunded_vnd,
				shipping_address_json
			) VALUES (
				$2, 'NM-LEGACY-CAN', $1, 'buyer-leg', 'shop-leg', 'CANCELLED', 'VIETQR', 'REFUNDED',
				100000, 30000, 5000, 2600, 130000, 50000, '{}'::jsonb
			)
		`, legacyGrp, cancelledID); err != nil {
			t.Fatalf("insert legacy cancelled order: %v", err)
		}

		m0011, err := migrations.Files.ReadFile("0011_w2b_order_round4.sql")
		if err != nil {
			t.Fatalf("read 0011 migration: %v", err)
		}
		if _, err := pool.Exec(ctx, string(m0011)); err != nil {
			t.Fatalf("apply 0011 over legacy data: %v", err)
		}

		var settledComp, settledDel, settledCan int64
		if err := pool.QueryRow(ctx, `SELECT settlement_refunded_vnd FROM orders WHERE id = $1`, legacyID).Scan(&settledComp); err != nil {
			t.Fatalf("query backfilled completed settlement_refunded_vnd: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT settlement_refunded_vnd FROM orders WHERE id = $1`, deliveredID).Scan(&settledDel); err != nil {
			t.Fatalf("query non-completed delivered settlement_refunded_vnd: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT settlement_refunded_vnd FROM orders WHERE id = $1`, cancelledID).Scan(&settledCan); err != nil {
			t.Fatalf("query non-completed cancelled settlement_refunded_vnd: %v", err)
		}
		if settledComp != 40000 {
			t.Fatalf("legacy COMPLETED settlement_refunded_vnd = %d, want 40000", settledComp)
		}
		if settledDel != 0 || settledCan != 0 {
			t.Fatalf("non-COMPLETED rows must stay at 0, got DELIVERED=%d CANCELLED=%d", settledDel, settledCan)
		}
	})
}

// TestPostgresR5_LocksSQLAndMigrations exercises Round 5 SQL/locking invariants on real Postgres:
//   - N1: PersistCheckoutBatch locks checkout_groups FOR UPDATE & rejects non-IN_PROGRESS;
//     InspectCheckoutGroupOrdersLocked waits behind open FOR UPDATE TX until commit.
//   - N2: CancelCheckoutGroupOrders protects paid/tracked orders, preserves SUCCEEDED/REFUNDED payment_status,
//     and skips FailCheckoutGroup while live orders remain.
//   - N4: CompleteCheckoutWithPayment + SQL guards (AND status='IN_PROGRESS', AND payment_id IN (”, $2))
//     preserve RetryPayment P2 expires & NULL snapshot.
//   - N7: Migration 0012 persists checkout_groups.warnings_json atomically in PersistCheckoutBatch.
//   - N8: Multi-shop COD partial paid keeps checkout_groups.payment_status = 'PENDING'.
func TestPostgresR5_LocksSQLAndMigrations(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("N1_PersistRejectsNonInProgressAndInspectSerializesBehindLock", func(t *testing.T) {
		failedGrp := uuid.NewString()
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: failedGrp, BuyerID: "b-n1", IdempotencyKey: "idem-n1-fail",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD",
		})
		_ = st.FailCheckoutGroup(ctx, failedGrp, "STALE")

		_, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: failedGrp,
			Orders: []NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: uuid.NewString(), OrderCode: "NM-N1-NO", CheckoutGroupID: failedGrp,
					BuyerID: "b-n1", ShopID: "shop-1", Status: "CONFIRMED", PaymentMethod: "COD",
					PaymentStatus: "PENDING", ShippingAddressJSON: []byte(`{}`),
				},
			}},
		})
		if err == nil {
			t.Fatalf("N1: expected PersistCheckoutBatch to fail on FAILED group")
		}

		// Verify InspectCheckoutGroupOrdersLocked blocks behind a FOR UPDATE lock on the group row
		// until the holder commits its order insert, then observes the committed order!
		lockGrp := uuid.NewString()
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: lockGrp, BuyerID: "b-n1", IdempotencyKey: "idem-n1-lock",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD",
		})

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin holder tx: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM checkout_groups WHERE id = $1 FOR UPDATE`, lockGrp); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("lock group in holder tx: %v", err)
		}

		doneCh := make(chan int, 1)
		errCh := make(chan error, 1)
		go func() {
			_, aggs, iErr := st.InspectCheckoutGroupOrdersLocked(ctx, lockGrp)
			if iErr != nil {
				errCh <- iErr
				return
			}
			doneCh <- len(aggs)
		}()

		// Confirm inspect is blocked while holder TX holds FOR UPDATE.
		select {
		case cnt := <-doneCh:
			_ = tx.Rollback(ctx)
			t.Fatalf("N1: InspectCheckoutGroupOrdersLocked returned early (%d) without waiting on FOR UPDATE!", cnt)
		case <-time.After(120 * time.Millisecond):
		}

		// Holder inserts an order and commits -> InspectCheckoutGroupOrdersLocked immediately unblocks and sees 1 order!
		if _, err := tx.Exec(ctx, `
			INSERT INTO orders (id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_status, subtotal_vnd, shipping_fee_vnd, total_payable_vnd, shipping_address_json)
			VALUES ($1, 'NM-N1-LOCK', $2, 'b-n1', 'shop-1', 'CONFIRMED', 'COD', 'PENDING', 100000, 20000, 120000, '{}'::jsonb)
		`, uuid.NewString(), lockGrp); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("insert in holder tx: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit holder tx: %v", err)
		}

		select {
		case iErr := <-errCh:
			t.Fatalf("InspectCheckoutGroupOrdersLocked error: %v", iErr)
		case cnt := <-doneCh:
			if cnt != 1 {
				t.Fatalf("InspectCheckoutGroupOrdersLocked saw %d orders after commit, want 1", cnt)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("InspectCheckoutGroupOrdersLocked timed out after holder commit")
		}
	})

	t.Run("N2_CancelCheckoutGroupOrdersProtectsPaidAndTrackedOrders", func(t *testing.T) {
		grpID := uuid.NewString()
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: grpID, BuyerID: "b-n2", IdempotencyKey: "idem-n2-pg",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 300000,
		})
		paidOrdID := uuid.NewString()
		trackedOrdID := uuid.NewString()
		pendingOrdID := uuid.NewString()
		_, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: grpID,
			WarningsJSON:    []byte(`["warn-pg-1"]`),
			Orders: []NewOrderRecord{
				{Order: db.InsertOrderParams{
					ID: paidOrdID, OrderCode: "NM-N2-PAID", CheckoutGroupID: grpID,
					BuyerID: "b-n2", ShopID: "shop-1", Status: "CONFIRMED",
					PaymentMethod: "VIETQR", PaymentID: "pay-1", PaymentStatus: "SUCCEEDED",
					SubtotalVND: 100000, TotalPayableVND: 100000, ShippingAddressJSON: []byte(`{}`),
				}},
				{Order: db.InsertOrderParams{
					ID: trackedOrdID, OrderCode: "NM-N2-TRK", CheckoutGroupID: grpID,
					BuyerID: "b-n2", ShopID: "shop-2", Status: "PREPARING",
					PaymentMethod: "COD", PaymentStatus: "PENDING", TrackingCode: "GHN-12345",
					SubtotalVND: 100000, TotalPayableVND: 100000, ShippingAddressJSON: []byte(`{}`),
				}},
				{Order: db.InsertOrderParams{
					ID: pendingOrdID, OrderCode: "NM-N2-PEND", CheckoutGroupID: grpID,
					BuyerID: "b-n2", ShopID: "shop-3", Status: "PENDING",
					PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
					SubtotalVND: 100000, TotalPayableVND: 100000, ShippingAddressJSON: []byte(`{}`),
				}},
			},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}

		// Verify N7 warnings_json persisted atomically on checkout_groups!
		grpBefore, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if !strings.Contains(string(grpBefore.WarningsJSON), "warn-pg-1") {
			t.Fatalf("N7: warnings_json not persisted in Postgres: %s", string(grpBefore.WarningsJSON))
		}

		res, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
			CheckoutGroupID: grpID,
			Reason:          "test cancel guard",
			ActorID:         "sys",
			ActorRole:       "system",
			PaymentStatus:   "FAILED",
		})
		if err != nil {
			t.Fatalf("CancelCheckoutGroupOrders: %v", err)
		}
		if !res.HasLiveOrders || len(res.Cancelled) != 1 || res.Cancelled[0].ID != pendingOrdID {
			t.Fatalf("N2: unexpected cancel result: hasLive=%v cancelled=%d", res.HasLiveOrders, len(res.Cancelled))
		}
		paidAgg, _ := st.GetOrderWithDetails(ctx, paidOrdID)
		if paidAgg.Order.Status != "CONFIRMED" || paidAgg.Order.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("N2 violation: paid order mutated: %s/%s", paidAgg.Order.Status, paidAgg.Order.PaymentStatus)
		}
		trkAgg, _ := st.GetOrderWithDetails(ctx, trackedOrdID)
		if trkAgg.Order.Status != "PREPARING" {
			t.Fatalf("N2 violation: tracked order cancelled: %s", trkAgg.Order.Status)
		}
		grpAfter, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grpAfter.Status != "IN_PROGRESS" {
			t.Fatalf("N2 violation: group marked %q even though live orders remain", grpAfter.Status)
		}
	})

	t.Run("N4_LateP1CompleteCheckoutPreservesRetryPaymentP2", func(t *testing.T) {
		grpID := uuid.NewString()
		ordID := uuid.NewString()
		exp1 := now.Add(5 * time.Minute).Truncate(time.Microsecond)
		exp2 := now.Add(15 * time.Minute).Truncate(time.Microsecond)
		seedPGCheckoutGroupWithOrder(t, st, grpID, "b-n4", "shop-1", "idem-n4-pg", db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-N4-1", Status: "PENDING",
			PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
			SubtotalVND: 100000, ShippingFeeVND: 20000, TotalPayableVND: 120000,
			PaymentExpiresAt: &exp1,
		}, now)

		// First completion attaches P1, then RetryPayment supersedes with P2 (exp2, snapshot=NULL).
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: grpID, PaymentID: "pay-p1", PaymentStatus: "PENDING",
			PaymentExpiresAt: &exp1, ResponseSnapshotJSON: []byte(`{"p":"p1"}`),
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment(P1): %v", err)
		}
		if err := st.UpdateGroupPaymentOnRetry(ctx, RetryPaymentUpdateParams{
			CheckoutGroupID: grpID, ExpectedPaymentID: "pay-p1", NewPaymentID: "pay-p2",
			PaymentStatus: "PENDING", PaymentExpiresAt: &exp2, RetryIdempotencyKey: "rk-p2", Now: now,
		}); err != nil {
			t.Fatalf("UpdateGroupPaymentOnRetry(P2): %v", err)
		}

		// Late retry of CompleteCheckoutWithPayment(P1, exp1) MUST NOT touch DB!
		var seenGrp db.CheckoutGroup
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: grpID, PaymentID: "pay-p1", PaymentStatus: "PENDING",
			PaymentExpiresAt: &exp1,
			BuildSnapshot: func(grp db.CheckoutGroup, aggs []OrderAggregate) ([]byte, error) {
				seenGrp = grp
				return []byte(`{"stale":"p1"}`), nil
			},
		}); err != nil {
			t.Fatalf("late CompleteCheckoutWithPayment(P1): %v", err)
		}

		grp, _ := st.GetCheckoutGroupByID(ctx, grpID)
		agg, _ := st.GetOrderWithDetails(ctx, ordID)
		if grp.PaymentID != "pay-p2" || seenGrp.PaymentID != "pay-p2" {
			t.Fatalf("N4: group payment_id=%q seen=%q, want pay-p2", grp.PaymentID, seenGrp.PaymentID)
		}
		if grp.PaymentExpiresAt == nil || !grp.PaymentExpiresAt.Equal(exp2) {
			t.Fatalf("N4: group expires=%v, want %v", grp.PaymentExpiresAt, exp2)
		}
		if len(grp.ResponseSnapshotJSON) != 0 {
			t.Fatalf("N4: snapshot clobbered to %s, want NULL", string(grp.ResponseSnapshotJSON))
		}
		if agg.Order.PaymentID != "pay-p2" || agg.Order.PaymentExpiresAt == nil || !agg.Order.PaymentExpiresAt.Equal(exp2) {
			t.Fatalf("N4: order clobbered: id=%q exp=%v", agg.Order.PaymentID, agg.Order.PaymentExpiresAt)
		}

		// Item 9 fix #1: Exercise BOTH SQL guards directly against Postgres (bypassing Go-layer grp.Status check):
		//  1) AttachCheckoutPaymentToOrders: AND (payment_id = '' OR payment_id = $2) must reject P1 overwriting P2.
		//  2) CompleteCheckoutGroup: AND status = 'IN_PROGRESS' must reject updating COMPLETED group.
		directQ := db.New(pool)
		if err := directQ.AttachCheckoutPaymentToOrders(ctx, db.AttachCheckoutPaymentToOrdersParams{
			CheckoutGroupID:  grpID,
			PaymentID:        "pay-p1",
			PaymentStatus:    "PENDING",
			PaymentExpiresAt: &exp1,
		}); err != nil {
			t.Fatalf("direct AttachCheckoutPaymentToOrders(P1): %v", err)
		}
		aggAfterSQL, _ := st.GetOrderWithDetails(ctx, ordID)
		if aggAfterSQL.Order.PaymentID != "pay-p2" || aggAfterSQL.Order.PaymentExpiresAt == nil || !aggAfterSQL.Order.PaymentExpiresAt.Equal(exp2) {
			t.Fatalf("N4 SQL guard AttachCheckoutPaymentToOrders failed: got id=%q exp=%v, want pay-p2 / %v",
				aggAfterSQL.Order.PaymentID, aggAfterSQL.Order.PaymentExpiresAt, exp2)
		}

		_, cErr := directQ.CompleteCheckoutGroup(ctx, db.CompleteCheckoutGroupParams{
			ID:                   grpID,
			PaymentID:            "pay-p1",
			PaymentStatus:        "PENDING",
			PaymentExpiresAt:     &exp1,
			TotalPayableVND:      999999,
			ResponseSnapshotJSON: []byte(`{"clobbered":"sql"}`),
		})
		if !errors.Is(cErr, pgx.ErrNoRows) {
			t.Fatalf("N4 SQL guard CompleteCheckoutGroup must match 0 rows on COMPLETED group (ErrNoRows), got %v", cErr)
		}
		grpAfterSQL, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grpAfterSQL.PaymentID != "pay-p2" || grpAfterSQL.PaymentExpiresAt == nil || !grpAfterSQL.PaymentExpiresAt.Equal(exp2) || len(grpAfterSQL.ResponseSnapshotJSON) != 0 {
			t.Fatalf("N4 SQL guard CompleteCheckoutGroup mutated COMPLETED group: id=%q exp=%v snap=%s",
				grpAfterSQL.PaymentID, grpAfterSQL.PaymentExpiresAt, string(grpAfterSQL.ResponseSnapshotJSON))
		}
	})

	t.Run("N8_MultiShopCODPartialPaidStaysPending", func(t *testing.T) {
		grpID := uuid.NewString()
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: grpID, BuyerID: "b-n8", IdempotencyKey: "idem-n8-pg",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 200000,
		})
		_, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: grpID,
			Orders: []NewOrderRecord{
				{Order: db.InsertOrderParams{
					ID: uuid.NewString(), OrderCode: "NM-N8-PG1", CheckoutGroupID: grpID,
					BuyerID: "b-n8", ShopID: "shop-1", Status: "DELIVERED",
					PaymentMethod: "COD", PaymentStatus: "SUCCEEDED", TotalPayableVND: 100000,
					ShippingAddressJSON: []byte(`{}`),
				}},
				{Order: db.InsertOrderParams{
					ID: uuid.NewString(), OrderCode: "NM-N8-PG2", CheckoutGroupID: grpID,
					BuyerID: "b-n8", ShopID: "shop-2", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING", TotalPayableVND: 100000,
					ShippingAddressJSON: []byte(`{}`),
				}},
			},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: grpID, PaymentID: "pay-n8-pg", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grp.PaymentStatus != "PENDING" {
			t.Fatalf("N8: Postgres group payment_status = %q, want PENDING", grp.PaymentStatus)
		}
	})
}

// TestPostgresR6_M3_M4_Races verifies Round 6 M3, M4, and P3(a) invariants on real Postgres:
//   - M3 Race 1: PersistCheckoutBatch holds FOR UPDATE -> ClaimStaleCheckoutGroupForCleanup blocks ->
//     Persist commits orders -> Claim's 2nd statement fresh READ COMMITTED snapshot sees orders -> returns false, group stays IN_PROGRESS!
//   - M3 Race 2: ClaimStaleCheckoutGroupForCleanup claims FAILED + needs_cleanup=TRUE first ->
//     PersistCheckoutBatch rejected (ErrCheckoutGroupNotInProgress), 0 orders inserted;
//     FindStaleInProgressCheckoutGroups continues returning FAILED+needs_cleanup=TRUE until ClearCheckoutGroupNeedsCleanup.
//   - M3 FailCheckoutGroup refuses to flip IN_PROGRESS group with live orders.
//   - M4 CancelCheckoutGroupOrders refuses COMPLETED group (HasLiveOrders=true, 0 cancelled, PENDING order untouched).
//   - P3(a) CancelCheckoutGroupOrders on 0-VND order sets payment_status = CANCELLED.
func TestPostgresR6_M3_M4_Races(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("M3_Race1_PersistHoldsLock_CleanupWaits_PersistCommits_CleanupDoesNothing", func(t *testing.T) {
		grpID := uuid.NewString()
		if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: grpID, BuyerID: "b-m3-r1", IdempotencyKey: "idem-m3-r1",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD",
		}); err != nil {
			t.Fatalf("CreateCheckoutGroup: %v", err)
		}

		// Tx1 simulates PersistCheckoutBatch acquiring FOR UPDATE on checkout_groups before inserting orders.
		tx1, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx1: %v", err)
		}
		if _, err := tx1.Exec(ctx, `SELECT id FROM checkout_groups WHERE id = $1 FOR UPDATE`, grpID); err != nil {
			_ = tx1.Rollback(ctx)
			t.Fatalf("tx1 lock group: %v", err)
		}

		type claimRes struct {
			claimed bool
			err     error
		}
		resCh := make(chan claimRes, 1)
		go func() {
			claimed, cErr := st.ClaimStaleCheckoutGroupForCleanup(ctx, grpID, "STALE_CHECKOUT_EXPIRED")
			resCh <- claimRes{claimed: claimed, err: cErr}
		}()

		// Confirm cleanup TX blocks behind tx1's FOR UPDATE lock.
		select {
		case r := <-resCh:
			_ = tx1.Rollback(ctx)
			t.Fatalf("M3 Race 1: ClaimStaleCheckoutGroupForCleanup returned early (%+v) without blocking on FOR UPDATE!", r)
		case <-time.After(120 * time.Millisecond):
		}

		// tx1 inserts a live order and commits!
		if _, err := tx1.Exec(ctx, `
			INSERT INTO orders (id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_status, subtotal_vnd, shipping_fee_vnd, total_payable_vnd, shipping_address_json)
			VALUES ($1, 'NM-M3-RACE1', $2, 'b-m3-r1', 'shop-1', 'CONFIRMED', 'COD', 'PENDING', 100000, 20000, 120000, '{}'::jsonb)
		`, uuid.NewString(), grpID); err != nil {
			_ = tx1.Rollback(ctx)
			t.Fatalf("tx1 insert order: %v", err)
		}
		if err := tx1.Commit(ctx); err != nil {
			t.Fatalf("tx1 commit: %v", err)
		}

		// Cleanup unblocks, executes statement 2 (fresh READ COMMITTED snapshot), sees the order, and MUST NOT claim or fail!
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("M3 Race 1: ClaimStaleCheckoutGroupForCleanup error: %v", r.err)
			}
			if r.claimed {
				t.Fatalf("M3 Race 1 violation: cleanup claimed group AFTER PersistCheckoutBatch committed an order!")
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("M3 Race 1: ClaimStaleCheckoutGroupForCleanup timed out")
		}

		grp, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grp.Status != "IN_PROGRESS" || grp.NeedsCleanup {
			t.Fatalf("M3 Race 1 violation: group mutated to status=%q needs_cleanup=%v, want IN_PROGRESS/false", grp.Status, grp.NeedsCleanup)
		}
	})

	t.Run("M3_Race2_CleanupClaimsFirst_PersistRejected_AndRetryClearsFlag", func(t *testing.T) {
		grpID := uuid.NewString()
		if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: grpID, BuyerID: "b-m3-r2", IdempotencyKey: "idem-m3-r2",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD",
		}); err != nil {
			t.Fatalf("CreateCheckoutGroup: %v", err)
		}

		claimed, err := st.ClaimStaleCheckoutGroupForCleanup(ctx, grpID, "STALE_CHECKOUT_EXPIRED")
		if err != nil || !claimed {
			t.Fatalf("M3 Race 2: expected claimed=true, got %v err=%v", claimed, err)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grp.Status != "FAILED" || !grp.NeedsCleanup {
			t.Fatalf("M3 Race 2: after claim expected FAILED + needs_cleanup=TRUE, got %+v", grp)
		}

		// Racing PersistCheckoutBatch MUST be rejected with ErrCheckoutGroupNotInProgress and insert 0 orders.
		_, pErr := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: grpID,
			Orders: []NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: uuid.NewString(), OrderCode: "NM-M3-REJECT", CheckoutGroupID: grpID,
					BuyerID: "b-m3-r2", ShopID: "shop-1", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING", ShippingAddressJSON: []byte(`{}`),
				},
			}},
		})
		if !errors.Is(pErr, ErrCheckoutGroupNotInProgress) {
			t.Fatalf("M3 Race 2 violation: PersistCheckoutBatch err = %v, want ErrCheckoutGroupNotInProgress", pErr)
		}
		aggs, _ := st.ListOrdersByCheckoutGroup(ctx, grpID)
		if len(aggs) != 0 {
			t.Fatalf("M3 Race 2 violation: expected 0 orders inserted, got %d", len(aggs))
		}

		// FindStaleInProgressCheckoutGroups MUST pick up status='FAILED' AND needs_cleanup=TRUE for retry!
		found, err := st.FindStaleInProgressCheckoutGroups(ctx, 5*time.Minute, 20)
		if err != nil {
			t.Fatalf("FindStaleInProgressCheckoutGroups: %v", err)
		}
		var saw bool
		for _, id := range found {
			if id == grpID {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("M3 Race 2: FindStaleInProgressCheckoutGroups did not return FAILED+needs_cleanup=TRUE group %s; got %v", grpID, found)
		}

		// Calling ClaimStaleCheckoutGroupForCleanup on retry returns claimed=true; clearing flag removes from sweep!
		claimedRetry, err := st.ClaimStaleCheckoutGroupForCleanup(ctx, grpID, "STALE_CHECKOUT_EXPIRED")
		if err != nil || !claimedRetry {
			t.Fatalf("M3 Race 2: retry claim expected true, got %v err=%v", claimedRetry, err)
		}
		if err := st.ClearCheckoutGroupNeedsCleanup(ctx, grpID); err != nil {
			t.Fatalf("ClearCheckoutGroupNeedsCleanup: %v", err)
		}
		grpAfter, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grpAfter.Status != "FAILED" || grpAfter.NeedsCleanup {
			t.Fatalf("M3 Race 2: after clear expected FAILED + needs_cleanup=FALSE, got %+v", grpAfter)
		}
	})

	t.Run("M3_FailCheckoutGroupRefusesLiveOrders", func(t *testing.T) {
		grpID := uuid.NewString()
		seedPGCheckoutGroupWithOrder(t, st, grpID, "b-m3-live", "shop-1", "idem-m3-live", db.InsertOrderParams{
			ID: uuid.NewString(), OrderCode: "NM-M3-LIVE", Status: "CONFIRMED",
			PaymentMethod: "COD", PaymentStatus: "PENDING", SubtotalVND: 100000, TotalPayableVND: 100000,
		}, now)
		if err := st.FailCheckoutGroup(ctx, grpID, "SHOULD_NOT_FLIP"); err != nil {
			t.Fatalf("FailCheckoutGroup: %v", err)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grp.Status != "IN_PROGRESS" {
			t.Fatalf("M3 violation: FailCheckoutGroup flipped group with live order to %q", grp.Status)
		}
	})

	t.Run("M4_CancelCheckoutGroupOrdersRefusesCompletedGroup", func(t *testing.T) {
		grpID := uuid.NewString()
		ordID := uuid.NewString()
		exp := now.Add(15 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, grpID, "b-m4-pg", "shop-1", "idem-m4-pg", db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-M4-PG", Status: "PENDING",
			PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
			SubtotalVND: 100000, TotalPayableVND: 100000, PaymentExpiresAt: &exp,
		}, now)
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: grpID, PaymentID: "pay-m4-pg", PaymentStatus: "PENDING",
			PaymentExpiresAt: &exp,
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}

		res, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
			CheckoutGroupID: grpID, Reason: "late watchdog race",
			ActorID: "watchdog", ActorRole: "system", PaymentStatus: "FAILED", OnlyPending: true,
		})
		if err != nil {
			t.Fatalf("CancelCheckoutGroupOrders: %v", err)
		}
		if !res.HasLiveOrders || len(res.Cancelled) != 0 {
			t.Fatalf("M4 violation on Postgres: COMPLETED group cancelled %d orders (hasLive=%v)", len(res.Cancelled), res.HasLiveOrders)
		}
		agg, _ := st.GetOrderWithDetails(ctx, ordID)
		if agg.Order.Status != "PENDING" || agg.Order.PaymentID != "pay-m4-pg" {
			t.Fatalf("M4 violation on Postgres: order mutated to status=%q payID=%q", agg.Order.Status, agg.Order.PaymentID)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, grpID)
		if grp.Status != "COMPLETED" {
			t.Fatalf("M4 violation on Postgres: group status=%q, want COMPLETED", grp.Status)
		}

		// Also verify OnlyPending=false on a COMPLETED COD group (where canCancelGroupOrder alone would allow cancel
		// if the grp.Status != "IN_PROGRESS" guard were missing!).
		codGrpID := uuid.NewString()
		codOrdID := uuid.NewString()
		seedPGCheckoutGroupWithOrder(t, st, codGrpID, "b-m4-cod", "shop-1", "idem-m4-cod", db.InsertOrderParams{
			ID: codOrdID, OrderCode: "NM-M4-COD", Status: "CONFIRMED",
			PaymentMethod: "COD", PaymentStatus: "PENDING",
			SubtotalVND: 100000, TotalPayableVND: 100000,
		}, now)
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: codGrpID, PaymentID: "pay-m4-cod", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment(COD): %v", err)
		}
		resCOD, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
			CheckoutGroupID: codGrpID, Reason: "late cancel on completed COD",
			ActorID: "sys", ActorRole: "system", PaymentStatus: "FAILED", OnlyPending: false,
		})
		if err != nil {
			t.Fatalf("CancelCheckoutGroupOrders(COD): %v", err)
		}
		if !resCOD.HasLiveOrders || len(resCOD.Cancelled) != 0 {
			t.Fatalf("M4 violation on Postgres (OnlyPending=false): COMPLETED COD group cancelled %d orders (hasLive=%v)", len(resCOD.Cancelled), resCOD.HasLiveOrders)
		}
		codAgg, _ := st.GetOrderWithDetails(ctx, codOrdID)
		if codAgg.Order.Status != "CONFIRMED" {
			t.Fatalf("M4 violation on Postgres: COMPLETED COD order mutated to %q", codAgg.Order.Status)
		}
	})

	t.Run("P3a_CancelCheckoutGroupOrdersZeroVNDOrderSetsPaymentStatusCancelled", func(t *testing.T) {
		grpID := uuid.NewString()
		ordID := uuid.NewString()
		seedPGCheckoutGroupWithOrder(t, st, grpID, "b-p3a-pg", "shop-1", "idem-p3a-pg", db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-P3A-PG", Status: "CONFIRMED",
			PaymentMethod: "VIETQR", PaymentStatus: "SUCCEEDED",
			SubtotalVND: 100000, PlatformVoucherDiscountVND: 100000, TotalPayableVND: 0,
		}, now)
		res, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
			CheckoutGroupID: grpID, Reason: "create payment rejected",
			ActorID: "sys", ActorRole: "system", PaymentStatus: "FAILED",
		})
		if err != nil || res.HasLiveOrders || len(res.Cancelled) != 1 {
			t.Fatalf("CancelCheckoutGroupOrders(0-VND): err=%v res=%+v", err, res)
		}
		agg, _ := st.GetOrderWithDetails(ctx, ordID)
		if agg.Order.PaymentStatus != "CANCELLED" {
			t.Fatalf("P3(a) Postgres: 0-VND cancelled order payment_status=%q, want CANCELLED", agg.Order.PaymentStatus)
		}
	})
}
