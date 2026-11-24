package service

import (
	"context"
	"sort"
	"strings"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxReportRangeDays    = 366
	defaultReportDays     = 30
	defaultReportTopLimit = 10
	maxReportTopLimit     = 50
)

// vietnamReportLocation returns Asia/Ho_Chi_Minh (fixed UTC+7, no DST).
// Uses time.FixedZone fallback so minimal container images without tzdata never fail.
func vietnamReportLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Ho_Chi_Minh"); err == nil && loc != nil {
		return loc
	}
	return time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
}

type reportDateRange struct {
	fromDateStr string
	toDateStr   string
	fromVN      time.Time
	toVN        time.Time
	startTS     time.Time // inclusive UTC timestamp of from_date 00:00:00 +07:00
	endTS       time.Time // exclusive UTC timestamp of (to_date + 1 day) 00:00:00 +07:00
	days        int
}

func parseReportDateRange(fromRaw, toRaw string, now time.Time) (reportDateRange, error) {
	loc := vietnamReportLocation()
	nowVN := now.In(loc)
	todayVN := time.Date(nowVN.Year(), nowVN.Month(), nowVN.Day(), 0, 0, 0, 0, loc)

	fromStr := strings.TrimSpace(fromRaw)
	toStr := strings.TrimSpace(toRaw)

	var fromVN, toVN time.Time
	var err error
	if toStr == "" {
		toVN = todayVN
		toStr = toVN.Format("2006-01-02")
	} else {
		toVN, err = time.ParseInLocation("2006-01-02", toStr, loc)
		if err != nil {
			return reportDateRange{}, status.Errorf(codes.InvalidArgument, "invalid to_date %q (expected YYYY-MM-DD)", toStr)
		}
	}

	if fromStr == "" {
		fromVN = toVN.AddDate(0, 0, -(defaultReportDays - 1))
		fromStr = fromVN.Format("2006-01-02")
	} else {
		fromVN, err = time.ParseInLocation("2006-01-02", fromStr, loc)
		if err != nil {
			return reportDateRange{}, status.Errorf(codes.InvalidArgument, "invalid from_date %q (expected YYYY-MM-DD)", fromStr)
		}
	}

	if toVN.Before(fromVN) {
		return reportDateRange{}, status.Error(codes.InvalidArgument, "to_date must be on or after from_date")
	}

	days := int(toVN.Sub(fromVN)/(24*time.Hour)) + 1
	if days > maxReportRangeDays {
		return reportDateRange{}, status.Errorf(codes.InvalidArgument, "date range cannot exceed %d days (requested %d days)", maxReportRangeDays, days)
	}

	endExclusiveVN := toVN.AddDate(0, 0, 1)
	return reportDateRange{
		fromDateStr: fromStr,
		toDateStr:   toStr,
		fromVN:      fromVN,
		toVN:        toVN,
		startTS:     fromVN.UTC(),
		endTS:       endExclusiveVN.UTC(),
		days:        days,
	}, nil
}

func normalizeTopLimit(limit int32) int32 {
	if limit <= 0 {
		return defaultReportTopLimit
	}
	if limit > maxReportTopLimit {
		return maxReportTopLimit
	}
	return limit
}

// Report metric definitions (Asia/Ho_Chi_Minh calendar days, inclusive [from_date, to_date]):
//   - Created cohort (orders.created_at in range):
//   - total_orders: total sub-orders created in range across all statuses.
//   - orders_by_status: breakdown of created orders by current order status.
//   - cancelled_orders: created orders in status CANCELLED.
//   - returned_orders: created orders in status RETURNED.
//   - gmv_vnd: sum of (P + D) = total_payable_vnd + platform_voucher_discount_vnd + freeship_discount_vnd + coins_discount_vnd
//     for created orders NOT in (PENDING, CANCELLED, RETURNED).
//   - cancel_rate_bps: cancelled_orders * 10000 / total_orders (0 when total_orders == 0).
//   - return_rate_bps: returned_orders * 10000 / total_orders (0 when total_orders == 0).
//   - Completed cohort (orders.status = COMPLETED and orders.completed_at in range):
//   - completed_orders: count of orders completed in range (grouped by VN completion date).
//   - Aligned with payment's compute_settlement_split waterfall using pre = min(settlement_refunded_vnd, refunded_vnd),
//     post = refunded_vnd - pre, raw_net = R - C_total (C_cat capped at R, C_pay capped at R - C_cat):
//   - net_revenue_vnd: sum of max(0, raw_net - pre) - post.
//   - fee excess rem = max(0, pre - raw_net) reduces effective fees in deterministic order payment_fee -> commission
//     so eff_commission + eff_payment_fee == C_total - min(rem, C_total) matches payment ledger platform_fee_revenue.
//   - platform_subsidy_vnd (platform report, summary & daily): sum of D = max(0, R + Ship - P) for COMPLETED orders;
//     always 0 on GetSellerReport.
//   - top_products: top products ranked by SUM(order_items.line_total_vnd) DESC, units_sold DESC
//     across COMPLETED orders in range.
type assembledReport struct {
	gmvVND             int64
	netRevenueVND      int64
	commissionVND      int64
	paymentFeeVND      int64
	platformSubsidyVND int64
	totalOrders        int64
	completedOrders    int64
	cancelledOrders    int64
	returnedOrders     int64
	cancelRateBPS      int32
	returnRateBPS      int32
	ordersByStatus     []*orderv1.OrderStatusCount
	daily              []*orderv1.DailyReportPoint
	topProducts        []*orderv1.TopProductStat
}

func assembleOrderReport(dr reportDateRange, raw *store.OrderReportData, includeDailySubsidy bool) assembledReport {
	var res assembledReport

	createdByDate := make(map[string]store.CreatedDailyStat, len(raw.CreatedDaily))
	for _, r := range raw.CreatedDaily {
		createdByDate[r.Date] = r
	}
	completedByDate := make(map[string]store.CompletedDailyStat, len(raw.CompletedDaily))
	for _, r := range raw.CompletedDaily {
		completedByDate[r.Date] = r
	}

	res.daily = make([]*orderv1.DailyReportPoint, 0, dr.days)
	for i := 0; i < dr.days; i++ {
		dateStr := dr.fromVN.AddDate(0, 0, i).Format("2006-01-02")
		cr := createdByDate[dateStr]
		cp := completedByDate[dateStr]

		res.totalOrders += cr.OrderCount
		res.cancelledOrders += cr.CancelledCount
		res.returnedOrders += cr.ReturnedCount
		res.gmvVND += cr.GMVVND

		res.completedOrders += cp.CompletedCount
		res.netRevenueVND += cp.NetRevenueVND
		res.commissionVND += cp.CommissionVND
		res.paymentFeeVND += cp.PaymentFeeVND
		res.platformSubsidyVND += cp.PlatformSubsidyVND

		var dailySubsidy int64
		if includeDailySubsidy {
			dailySubsidy = cp.PlatformSubsidyVND
		}
		res.daily = append(res.daily, &orderv1.DailyReportPoint{
			Date:               dateStr,
			OrderCount:         cr.OrderCount,
			CompletedCount:     cp.CompletedCount,
			CancelledCount:     cr.CancelledCount,
			ReturnedCount:      cr.ReturnedCount,
			GmvVnd:             cr.GMVVND,
			NetRevenueVnd:      cp.NetRevenueVND,
			CommissionVnd:      cp.CommissionVND,
			PaymentFeeVnd:      cp.PaymentFeeVND,
			PlatformSubsidyVnd: dailySubsidy,
		})
	}

	if res.totalOrders > 0 {
		res.cancelRateBPS = int32((res.cancelledOrders * 10000) / res.totalOrders)
		res.returnRateBPS = int32((res.returnedOrders * 10000) / res.totalOrders)
	}

	res.ordersByStatus = make([]*orderv1.OrderStatusCount, 0, len(raw.StatusCounts))
	for stStr, cnt := range raw.StatusCounts {
		res.ordersByStatus = append(res.ordersByStatus, &orderv1.OrderStatusCount{
			Status: statemachine.StringToStatus(stStr),
			Count:  cnt,
		})
	}
	sort.Slice(res.ordersByStatus, func(i, j int) bool {
		return res.ordersByStatus[i].Status < res.ordersByStatus[j].Status
	})

	res.topProducts = make([]*orderv1.TopProductStat, 0, len(raw.TopProducts))
	for _, tp := range raw.TopProducts {
		res.topProducts = append(res.topProducts, &orderv1.TopProductStat{
			ProductId:   tp.ProductID,
			ProductName: tp.ProductName,
			UnitsSold:   tp.UnitsSold,
			RevenueVnd:  tp.RevenueVND,
			OrderCount:  tp.OrderCount,
		})
	}

	return res
}

// GetSellerReport returns server-side financial and operational metrics for a single shop (UC-S06).
// Sellers are strictly scoped to their verified JWT shop_id; admins may query any shop_id.
func (s *OrderService) GetSellerReport(ctx context.Context, req *orderv1.GetSellerReportRequest) (*orderv1.GetSellerReportResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}

	var shopID string
	if caller.IsFullAdmin() {
		shopID = strings.TrimSpace(req.GetShopId())
		if shopID == "" {
			return nil, status.Error(codes.InvalidArgument, "shop_id is required")
		}
	} else if caller.Role == auth.RoleSeller && caller.ShopID != "" {
		if reqShop := strings.TrimSpace(req.GetShopId()); reqShop != "" && reqShop != caller.ShopID {
			return nil, status.Error(codes.PermissionDenied, "cannot view report for another shop")
		}
		shopID = caller.ShopID
	} else {
		return nil, status.Error(codes.PermissionDenied, "seller or admin role required")
	}

	dr, err := parseReportDateRange(req.GetFromDate(), req.GetToDate(), s.nowFn())
	if err != nil {
		return nil, err
	}
	topLimit := normalizeTopLimit(req.GetTopLimit())

	raw, err := s.store.QueryOrderReport(ctx, store.OrderReportParams{
		ShopID:   shopID,
		StartTS:  dr.startTS,
		EndTS:    dr.endTS,
		TopLimit: topLimit,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	rep := assembleOrderReport(dr, raw, false)
	return &orderv1.GetSellerReportResponse{
		ShopId:          shopID,
		FromDate:        dr.fromDateStr,
		ToDate:          dr.toDateStr,
		GmvVnd:          rep.gmvVND,
		NetRevenueVnd:   rep.netRevenueVND,
		CommissionVnd:   rep.commissionVND,
		PaymentFeeVnd:   rep.paymentFeeVND,
		TotalOrders:     rep.totalOrders,
		CompletedOrders: rep.completedOrders,
		CancelledOrders: rep.cancelledOrders,
		ReturnedOrders:  rep.returnedOrders,
		CancelRateBps:   rep.cancelRateBPS,
		ReturnRateBps:   rep.returnRateBPS,
		OrdersByStatus:  rep.ordersByStatus,
		Daily:           rep.daily,
		TopProducts:     rep.topProducts,
	}, nil
}

// GetPlatformReport returns platform-wide GMV, net revenue, fees, platform subsidy, and top products (UC-A06).
// Restricted to admin/super_admin callers.
func (s *OrderService) GetPlatformReport(ctx context.Context, req *orderv1.GetPlatformReportRequest) (*orderv1.GetPlatformReportResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.HasCapability(auth.CapReportsRead) {
		return nil, status.Error(codes.PermissionDenied, "reports.read capability required to view platform report")
	}

	dr, err := parseReportDateRange(req.GetFromDate(), req.GetToDate(), s.nowFn())
	if err != nil {
		return nil, err
	}
	topLimit := normalizeTopLimit(req.GetTopLimit())

	raw, err := s.store.QueryOrderReport(ctx, store.OrderReportParams{
		ShopID:   "",
		StartTS:  dr.startTS,
		EndTS:    dr.endTS,
		TopLimit: topLimit,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	rep := assembleOrderReport(dr, raw, true)
	return &orderv1.GetPlatformReportResponse{
		FromDate:           dr.fromDateStr,
		ToDate:             dr.toDateStr,
		GmvVnd:             rep.gmvVND,
		NetRevenueVnd:      rep.netRevenueVND,
		CommissionVnd:      rep.commissionVND,
		PaymentFeeVnd:      rep.paymentFeeVND,
		PlatformSubsidyVnd: rep.platformSubsidyVND,
		TotalOrders:        rep.totalOrders,
		CompletedOrders:    rep.completedOrders,
		CancelledOrders:    rep.cancelledOrders,
		ReturnedOrders:     rep.returnedOrders,
		CancelRateBps:      rep.cancelRateBPS,
		ReturnRateBps:      rep.returnRateBPS,
		OrdersByStatus:     rep.ordersByStatus,
		Daily:              rep.daily,
		TopProducts:        rep.topProducts,
	}, nil
}

// ListOrdersNeedingReview lists orders flagged with orphan_shipment_needs_manual_review = true (admin only).
func (s *OrderService) ListOrdersNeedingReview(ctx context.Context, req *orderv1.ListOrdersNeedingReviewRequest) (*orderv1.ListOrdersNeedingReviewResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.HasCapability(auth.CapOrdersRead) {
		return nil, status.Error(codes.PermissionDenied, "orders.read capability required to list orders needing manual review")
	}

	page := req.GetPage()
	if page < 1 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	aggs, total, err := s.store.ListOrdersNeedingManualReview(ctx, pageSize, offset)
	if err != nil {
		return nil, mapStoreError(err)
	}

	orders := make([]*orderv1.Order, 0, len(aggs))
	for _, agg := range aggs {
		orders = append(orders, aggregateToProtoForCaller(agg, caller))
	}
	return &orderv1.ListOrdersNeedingReviewResponse{
		Orders: orders,
		Total:  total,
	}, nil
}

// ResolveOrderReview clears orphan_shipment_needs_manual_review on a flagged order and appends a timeline audit note (admin only).
func (s *OrderService) ResolveOrderReview(ctx context.Context, req *orderv1.ResolveOrderReviewRequest) (*orderv1.ResolveOrderReviewResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.HasCapability(auth.CapOrdersReviewResolve) {
		return nil, status.Error(codes.PermissionDenied, "orders.review.resolve capability required to resolve order manual review")
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	note := strings.TrimSpace(req.GetNote())
	if note == "" {
		note = "MANUAL_REVIEW_RESOLVED: admin marked orphan shipment issue resolved"
	} else {
		note = "MANUAL_REVIEW_RESOLVED: " + note
	}

	agg, err := s.store.ResolveOrderManualReview(ctx, orderID, caller.UserID, fallbackStr(caller.Role, "admin"), note, s.nowFn())
	if err != nil {
		return nil, mapStoreError(err)
	}
	return &orderv1.ResolveOrderReviewResponse{
		Order: aggregateToProtoForCaller(*agg, caller),
	}, nil
}
