package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxEvidenceURLs             = 5
	maxEvidenceURLLength        = 500
	returnRejectWindow          = 3 * 24 * time.Hour
	returnEvidenceWindow        = 3 * 24 * time.Hour
	returnTransitWindow         = 14 * 24 * time.Hour
	returnPickupGraceWindow     = 24 * time.Hour
	maxReturnDeadlineExtensions = 3
	feeBearerSeller             = "SELLER"
	feeBearerBuyer              = "BUYER"
	feeBearerPlatform           = "PLATFORM"
)

func validateEvidenceURLs(urls []string) ([]string, error) {
	if len(urls) > maxEvidenceURLs {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d evidence URLs are allowed", maxEvidenceURLs)
	}
	cleaned := make([]string, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			return nil, status.Error(codes.InvalidArgument, "evidence URL must not be empty")
		}
		if len(u) > maxEvidenceURLLength {
			return nil, status.Errorf(codes.InvalidArgument, "evidence URL exceeds maximum length of %d characters", maxEvidenceURLLength)
		}
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, status.Errorf(codes.InvalidArgument, "evidence URL must be a valid http(s) URL: %q", u)
		}
		if _, dup := seen[u]; !dup {
			seen[u] = struct{}{}
			cleaned = append(cleaned, u)
		}
	}
	return cleaned, nil
}

func mergeEvidenceURLs(existingJSON []byte, incoming []string) ([]byte, error) {
	validNew, err := validateEvidenceURLs(incoming)
	if err != nil {
		return nil, err
	}
	var existing []string
	if len(existingJSON) > 0 {
		_ = json.Unmarshal(existingJSON, &existing)
	}
	merged := make([]string, 0, len(existing)+len(validNew))
	seen := make(map[string]struct{}, len(existing)+len(validNew))
	for _, u := range existing {
		u = strings.TrimSpace(u)
		if u != "" {
			if _, ok := seen[u]; !ok {
				seen[u] = struct{}{}
				merged = append(merged, u)
			}
		}
	}
	for _, u := range validNew {
		if _, ok := seen[u]; !ok {
			seen[u] = struct{}{}
			merged = append(merged, u)
		}
	}
	if len(merged) > maxEvidenceURLs {
		return nil, status.Errorf(codes.InvalidArgument, "total evidence URLs cannot exceed %d", maxEvidenceURLs)
	}
	return json.Marshal(merged)
}

func normalizeFeeBearer(raw string, defaultVal string) (string, error) {
	v := strings.ToUpper(strings.TrimSpace(raw))
	if v == "" {
		return defaultVal, nil
	}
	switch v {
	case feeBearerSeller, feeBearerBuyer, feeBearerPlatform:
		return v, nil
	default:
		return "", status.Errorf(codes.InvalidArgument, "invalid return_fee_bearer %q (must be SELLER, BUYER, or PLATFORM)", raw)
	}
}

func canViewReturnRequest(caller callerIdentity, rr db.ReturnRequest) bool {
	if caller.IsPrivileged {
		return true
	}
	if caller.UserID != "" && caller.UserID == rr.BuyerID {
		return true
	}
	if caller.Role == "seller" && caller.ShopID != "" && caller.ShopID == rr.ShopID {
		return true
	}
	return false
}

func canActAsReturnBuyer(caller callerIdentity, rr db.ReturnRequest) bool {
	return caller.UserID != "" && caller.UserID == rr.BuyerID
}

func canActAsReturnSeller(caller callerIdentity, rr db.ReturnRequest) bool {
	return caller.Role == "seller" && caller.ShopID != "" && caller.ShopID == rr.ShopID
}

// RequestReturn transitions DELIVERED -> RETURN_REQUESTED and creates a unique PENDING ReturnRequest.
func (s *OrderService) RequestReturn(ctx context.Context, req *orderv1.RequestReturnRequest) (*orderv1.RequestReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "return reason is required")
	}
	evidence, err := validateEvidenceURLs(req.GetEvidenceUrls())
	if err != nil {
		return nil, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal evidence_urls: %v", err)
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canActAsBuyer(caller, agg.Order, req.GetBuyerId()) {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order can request a return")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	now := s.nowFn()
	if agg.Order.DeliveredAt != nil && now.Sub(*agg.Order.DeliveredAt) > s.autoCompleteDuration() {
		return nil, status.Error(codes.FailedPrecondition, "return request window (7 days) has expired")
	}

	deadline := now.Add(s.returnSellerResponseDuration())
	returnID := uuid.NewString()
	refundAmt := maxInt64(0, agg.Order.TotalPayableVND-agg.Order.RefundedVND)
	traceID, traceparent := traceMetaFromContext(ctx)
	actorRole := fallbackStr(caller.Role, "buyer")

	retUpdatedPayload, err := json.Marshal(events.OrderReturnUpdatedPayload{
		ReturnRequestID:    returnID,
		OrderID:            agg.Order.ID,
		OrderCode:          agg.Order.OrderCode,
		CheckoutGroupID:    agg.Order.CheckoutGroupID,
		BuyerID:            agg.Order.BuyerID,
		ShopID:             agg.Order.ShopID,
		Status:             statemachine.ReturnStatusToString(orderv1.ReturnStatus_RETURN_STATUS_PENDING),
		PreviousStatus:     "",
		Reason:             reason,
		ReturnTrackingCode: "",
		DeadlineAt:         deadline.UTC().Format(time.RFC3339),
		ActorRole:          actorRole,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal order.return_updated: %v", err)
	}

	updatedAgg, _, err := s.store.TransitionOrderReturnRequested(ctx, store.ReturnRequestParams{
		OrderID:          agg.Order.ID,
		OrderCode:        agg.Order.OrderCode,
		BuyerID:          agg.Order.BuyerID,
		ShopID:           agg.Order.ShopID,
		ReturnRequestID:  returnID,
		Reason:           reason,
		EvidenceURLsJSON: evidenceJSON,
		RefundAmountVND:  refundAmt,
		DeadlineAt:       &deadline,
		ActorID:          fallbackStr(caller.UserID, agg.Order.BuyerID),
		ActorRole:        actorRole,
		Note:             "Return requested: " + reason,
		OutboxEvent: db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderReturnUpdated,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: retUpdatedPayload,
		},
		Now: now,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.RequestReturnResponse{
		Order:           aggregateToProto(*updatedAgg),
		ReturnRequestId: returnID,
	}, nil
}

// GetReturnRequest returns a single return request (by return_request_id or order_id) with object-level RBAC.
func (s *OrderService) GetReturnRequest(ctx context.Context, req *orderv1.GetReturnRequestRequest) (*orderv1.GetReturnRequestResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	ordID := strings.TrimSpace(req.GetOrderId())
	if retID == "" && ordID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id or order_id is required")
	}

	agg, err := s.store.GetReturnRequest(ctx, retID, ordID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canViewReturnRequest(caller, agg.Request) {
		return nil, status.Error(codes.PermissionDenied, "access denied to this return request")
	}
	return &orderv1.GetReturnRequestResponse{
		ReturnRequest: returnAggregateToProto(*agg),
	}, nil
}

// ListReturnRequests returns paginated return requests scoped to buyer, seller shop, or admin.
func (s *OrderService) ListReturnRequests(ctx context.Context, req *orderv1.ListReturnRequestsRequest) (*orderv1.ListReturnRequestsResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(req.GetRoleScope()))
	var buyerFilter, shopFilter string

	switch scope {
	case "admin":
		if !caller.IsPrivileged {
			return nil, status.Error(codes.PermissionDenied, "admin role required to list all return requests")
		}
	case "seller":
		if caller.IsPrivileged {
			shopFilter = caller.ShopID
		} else {
			if caller.Role != "seller" || caller.ShopID == "" {
				return nil, status.Error(codes.PermissionDenied, "seller shop identity is required")
			}
			shopFilter = caller.ShopID
		}
	default:
		if caller.IsPrivileged && scope == "" {
			buyerFilter = ""
		} else {
			buyerFilter = caller.UserID
		}
	}

	var statusFilter string
	if req.GetStatus() != orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED {
		statusFilter = statemachine.ReturnStatusToString(req.GetStatus())
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

	aggs, total, err := s.store.ListReturnRequestsFiltered(ctx, buyerFilter, shopFilter, statusFilter, pageSize, offset)
	if err != nil {
		return nil, mapStoreError(err)
	}

	items := make([]*orderv1.ReturnRequest, 0, len(aggs))
	for _, agg := range aggs {
		items = append(items, returnAggregateToProto(agg))
	}
	return &orderv1.ListReturnRequestsResponse{
		ReturnRequests: items,
		Total:          total,
	}, nil
}

// RespondReturn lets the owning shop seller ACCEPT_REFUND, ACCEPT_RETURN, or REJECT a PENDING return request.
func (s *OrderService) RespondReturn(ctx context.Context, req *orderv1.RespondReturnRequest) (*orderv1.RespondReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	if retID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}

	agg, err := s.store.GetReturnRequest(ctx, retID, "")
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canActAsReturnSeller(caller, agg.Request) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller can respond to this return request")
	}

	note := strings.TrimSpace(req.GetNote())
	sellerEvJSON, err := mergeEvidenceURLs(agg.Request.SellerEvidenceURLsJSON, req.GetEvidenceUrls())
	if err != nil {
		return nil, err
	}

	now := s.nowFn()
	traceID, traceparent := traceMetaFromContext(ctx)
	params := store.ReturnTransitionParams{
		ReturnRequestID:        retID,
		ExpectedStatus:         orderv1.ReturnStatus_RETURN_STATUS_PENDING,
		ActorID:                caller.UserID,
		ActorRole:              "seller",
		SellerResponse:         &note,
		SellerEvidenceURLsJSON: sellerEvJSON,
		UpdateDeadline:         true,
		TraceID:                traceID,
		Traceparent:            traceparent,
		Now:                    now,
	}

	switch req.GetDecision() {
	case orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND:
		shouldRestock := false
		bearer := feeBearerSeller
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_REFUNDING
		params.ShouldRestock = &shouldRestock
		params.ReturnFeeBearer = &bearer
		params.DeadlineAt = nil
		params.Note = fallbackStr(note, "Seller accepted refund without return shipment")
	case orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN:
		bearer := feeBearerSeller
		dl := now.Add(s.returnShipDuration())
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT
		params.ReturnFeeBearer = &bearer
		params.DeadlineAt = &dl // Refreshed once tracking attaches; if never created, escalates on expiry.
		params.Note = fallbackStr(note, "Seller accepted return; awaiting buyer return shipment")
	case orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT:
		if note == "" {
			return nil, status.Error(codes.InvalidArgument, "rejection note is required")
		}
		dl := now.Add(returnRejectWindow)
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_REJECTED
		params.DeadlineAt = &dl
		params.Note = note
	default:
		return nil, status.Error(codes.InvalidArgument, "valid seller decision is required")
	}

	res, err := s.store.TransitionReturnRequest(ctx, params)
	if err != nil {
		return nil, mapStoreError(err)
	}
	s.notifyOutbox()

	finalRet := res.Return
	switch params.TargetStatus {
	case orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:
		if updated, sErr := s.ensureReturnShipment(ctx, res.Return.Request, res.Order); sErr != nil {
			s.logger.WarnContext(ctx, "post-commit CreateReturnShipment failed; watchdog will retry",
				"return_request_id", retID, "order_id", res.Return.Request.OrderID, "error", sErr)
		} else if updated != nil {
			finalRet = updated
		}
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:
		if updated, rErr := s.executeRefundingSideEffects(ctx, res.Return.Request, res.Order); rErr != nil {
			s.logger.WarnContext(ctx, "post-commit REFUNDING side effects incomplete; watchdog will retry",
				"return_request_id", retID, "order_id", res.Return.Request.OrderID, "error", rErr)
		} else if updated != nil {
			finalRet = updated
		}
	}

	return &orderv1.RespondReturnResponse{
		ReturnRequest: returnAggregateToProto(*finalRet),
	}, nil
}

// EscalateReturn lets the owning buyer escalate a REJECTED return request to admin review (ESCALATED).
func (s *OrderService) EscalateReturn(ctx context.Context, req *orderv1.EscalateReturnRequest) (*orderv1.EscalateReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	if retID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "escalation reason is required")
	}

	agg, err := s.store.GetReturnRequest(ctx, retID, "")
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canActAsReturnBuyer(caller, agg.Request) {
		return nil, status.Error(codes.PermissionDenied, "only the owning buyer can escalate this return request")
	}

	evJSON, err := mergeEvidenceURLs(agg.Request.EvidenceURLsJSON, req.GetEvidenceUrls())
	if err != nil {
		return nil, err
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	res, err := s.store.TransitionReturnRequest(ctx, store.ReturnTransitionParams{
		ReturnRequestID:  retID,
		ExpectedStatus:   orderv1.ReturnStatus_RETURN_STATUS_REJECTED,
		TargetStatus:     orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		ActorID:          caller.UserID,
		ActorRole:        "buyer",
		Note:             reason,
		EvidenceURLsJSON: evJSON,
		DisputeReason:    &reason,
		UpdateDeadline:   true,
		DeadlineAt:       nil,
		TraceID:          traceID,
		Traceparent:      traceparent,
		Now:              s.nowFn(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.EscalateReturnResponse{
		ReturnRequest: returnAggregateToProto(*res.Return),
	}, nil
}

// AddReturnEvidence lets the owning buyer submit requested evidence (EVIDENCE_REQUESTED -> ESCALATED).
func (s *OrderService) AddReturnEvidence(ctx context.Context, req *orderv1.AddReturnEvidenceRequest) (*orderv1.AddReturnEvidenceResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	if retID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}
	note := strings.TrimSpace(req.GetNote())
	if note == "" && len(req.GetEvidenceUrls()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "evidence note or evidence_urls is required")
	}

	agg, err := s.store.GetReturnRequest(ctx, retID, "")
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canActAsReturnBuyer(caller, agg.Request) {
		return nil, status.Error(codes.PermissionDenied, "only the owning buyer can submit return evidence")
	}

	evJSON, err := mergeEvidenceURLs(agg.Request.EvidenceURLsJSON, req.GetEvidenceUrls())
	if err != nil {
		return nil, err
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	res, err := s.store.TransitionReturnRequest(ctx, store.ReturnTransitionParams{
		ReturnRequestID:  retID,
		ExpectedStatus:   orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED,
		TargetStatus:     orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		ActorID:          caller.UserID,
		ActorRole:        "buyer",
		Note:             fallbackStr(note, "Buyer submitted additional evidence"),
		EvidenceURLsJSON: evJSON,
		UpdateDeadline:   true,
		DeadlineAt:       nil,
		TraceID:          traceID,
		Traceparent:      traceparent,
		Now:              s.nowFn(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.AddReturnEvidenceResponse{
		ReturnRequest: returnAggregateToProto(*res.Return),
	}, nil
}

// ConfirmReturnReceived lets the owning shop seller confirm received return goods (RECEIVED -> REFUNDING -> REFUNDED).
func (s *OrderService) ConfirmReturnReceived(ctx context.Context, req *orderv1.ConfirmReturnReceivedRequest) (*orderv1.ConfirmReturnReceivedResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	if retID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}

	agg, err := s.store.GetReturnRequest(ctx, retID, "")
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canActAsReturnSeller(caller, agg.Request) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller can confirm returned goods")
	}

	shouldRestock := true
	traceID, traceparent := traceMetaFromContext(ctx)
	res, err := s.store.TransitionReturnRequest(ctx, store.ReturnTransitionParams{
		ReturnRequestID: retID,
		ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_RECEIVED,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		ActorID:         caller.UserID,
		ActorRole:       "seller",
		Note:            "Seller confirmed returned goods received in good condition",
		ShouldRestock:   &shouldRestock,
		UpdateDeadline:  true,
		DeadlineAt:      nil,
		TraceID:         traceID,
		Traceparent:     traceparent,
		Now:             s.nowFn(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}
	s.notifyOutbox()

	finalRet := res.Return
	if updated, rErr := s.executeRefundingSideEffects(ctx, res.Return.Request, res.Order); rErr != nil {
		s.logger.WarnContext(ctx, "post-commit REFUNDING side effects incomplete; watchdog will retry",
			"return_request_id", retID, "order_id", res.Return.Request.OrderID, "error", rErr)
	} else if updated != nil {
		finalRet = updated
	}

	return &orderv1.ConfirmReturnReceivedResponse{
		ReturnRequest: returnAggregateToProto(*finalRet),
	}, nil
}

// DisputeReturnGoods lets the owning shop seller dispute wrong/damaged returned goods (RECEIVED -> ESCALATED).
func (s *OrderService) DisputeReturnGoods(ctx context.Context, req *orderv1.DisputeReturnGoodsRequest) (*orderv1.DisputeReturnGoodsResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	if retID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "dispute reason is required")
	}

	agg, err := s.store.GetReturnRequest(ctx, retID, "")
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canActAsReturnSeller(caller, agg.Request) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller can dispute returned goods")
	}

	sellerEvJSON, err := mergeEvidenceURLs(agg.Request.SellerEvidenceURLsJSON, req.GetEvidenceUrls())
	if err != nil {
		return nil, err
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	res, err := s.store.TransitionReturnRequest(ctx, store.ReturnTransitionParams{
		ReturnRequestID:        retID,
		ExpectedStatus:         orderv1.ReturnStatus_RETURN_STATUS_RECEIVED,
		TargetStatus:           orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		ActorID:                caller.UserID,
		ActorRole:              "seller",
		Note:                   reason,
		SellerEvidenceURLsJSON: sellerEvJSON,
		DisputeReason:          &reason,
		UpdateDeadline:         true,
		DeadlineAt:             nil,
		TraceID:                traceID,
		Traceparent:            traceparent,
		Now:                    s.nowFn(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.DisputeReturnGoodsResponse{
		ReturnRequest: returnAggregateToProto(*res.Return),
	}, nil
}

// AdminResolveReturn lets admin/super_admin arbitrate an ESCALATED return request.
func (s *OrderService) AdminResolveReturn(ctx context.Context, req *orderv1.AdminResolveReturnRequest) (*orderv1.AdminResolveReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsPrivileged {
		return nil, status.Error(codes.PermissionDenied, "admin role required to resolve return disputes")
	}
	retID := strings.TrimSpace(req.GetReturnRequestId())
	if retID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}

	defaultBearer := ""
	if req.GetDecision() == orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND {
		defaultBearer = feeBearerSeller
	}
	bearer, err := normalizeFeeBearer(req.GetReturnFeeBearer(), defaultBearer)
	if err != nil {
		return nil, err
	}

	note := strings.TrimSpace(req.GetNote())
	now := s.nowFn()
	traceID, traceparent := traceMetaFromContext(ctx)

	params := store.ReturnTransitionParams{
		ReturnRequestID: retID,
		ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		ActorID:         caller.UserID,
		ActorRole:       fallbackStr(caller.Role, "admin"),
		AdminNote:       &note,
		UpdateDeadline:  true,
		TraceID:         traceID,
		Traceparent:     traceparent,
		Now:             now,
	}
	if bearer != "" {
		params.ReturnFeeBearer = &bearer
	}

	switch req.GetDecision() {
	case orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REQUEST_EVIDENCE:
		dl := now.Add(returnEvidenceWindow)
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED
		params.DeadlineAt = &dl
		params.Note = fallbackStr(note, "Admin requested additional evidence from buyer")
	case orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND:
		// Never restock on admin REFUND branch (goods were either not returned or disputed as wrong/damaged).
		shouldRestock := false
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_REFUNDING
		params.ShouldRestock = &shouldRestock
		params.DeadlineAt = nil
		params.Note = fallbackStr(note, "Admin approved refund")
	case orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND:
		agg, gErr := s.store.GetReturnRequest(ctx, retID, "")
		if gErr != nil {
			return nil, mapStoreError(gErr)
		}
		if statemachine.StringToReturnStatus(agg.Request.Status) != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			return nil, status.Errorf(codes.FailedPrecondition,
				"invalid return status transition: expected ESCALATED, got %s", agg.Request.Status)
		}
		if agg.Request.GoodsReceived {
			return nil, status.Error(codes.FailedPrecondition, "Hàng hoàn trả đã được giao về shop, không thể yêu cầu gửi trả lại")
		}
		// P1-3: Block RETURN_AND_REFUND when the previous RETURN shipment is still delivering/returning
		// or failed (lost/damaged), or escalated from IN_TRANSIT timeout; admin must choose REFUND or REJECT.
		if strings.TrimSpace(agg.Request.ReturnTrackingCode) != "" {
			shResp, shErr := s.getReturnShipment(ctx, agg.Request.OrderID, agg.Request.ReturnTrackingCode)
			if shErr != nil {
				if status.Code(shErr) != codes.NotFound {
					return nil, status.Errorf(codes.Unavailable,
						"Không thể kiểm tra trạng thái vận đơn hoàn trả cũ, vui lòng thử lại sau: %v", shErr)
				}
			} else if shResp.GetShipment() != nil {
				switch shResp.GetShipment().GetStatus() {
				case shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
					shippingv1.ShipmentStatus_SHIPMENT_STATUS_PICKING:
					return nil, status.Error(codes.FailedPrecondition,
						"Vận đơn hoàn trả cũ vẫn đang hoạt động ở đơn vị vận chuyển, không thể tạo vận đơn mới; vui lòng chờ xử lý hoặc chọn hoàn tiền (REFUND) / từ chối (REJECT)")
				case shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING,
					shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNING:
					return nil, status.Error(codes.FailedPrecondition,
						"Vận đơn hoàn trả cũ vẫn đang vận chuyển, không thể tạo vận đơn mới; vui lòng chọn hoàn tiền (REFUND) hoặc từ chối (REJECT)")
				case shippingv1.ShipmentStatus_SHIPMENT_STATUS_FAILED:
					return nil, status.Error(codes.FailedPrecondition,
						"Vận đơn hoàn trả cũ gặp sự cố mất/hỏng ở đơn vị vận chuyển, không thể tạo vận đơn mới; vui lòng chọn hoàn tiền (REFUND) hoặc từ chối (REJECT)")
				case shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED:
					return nil, status.Error(codes.FailedPrecondition,
						"Hàng hoàn trả đã được giao về shop, không thể tạo vận đơn mới; vui lòng chọn hoàn tiền (REFUND) hoặc từ chối (REJECT)")
				}
			}
		}
		if agg.Request.EscalatedFromTransitTimeout {
			return nil, status.Error(codes.FailedPrecondition,
				"Vận đơn hoàn trả cũ đã quá hạn vận chuyển 14 ngày, không thể tạo vận đơn mới; vui lòng chọn hoàn tiền (REFUND) hoặc từ chối (REJECT)")
		}
		dl := now.Add(s.returnShipDuration())
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT
		params.RequireNotGoodsRecv = true
		params.DeadlineAt = &dl // Refreshed once tracking attaches; if never created, escalates on expiry.
		params.Note = fallbackStr(note, "Admin approved return and refund")
	case orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT:
		agg, gErr := s.store.GetReturnRequest(ctx, retID, "")
		if gErr != nil {
			return nil, mapStoreError(gErr)
		}
		if statemachine.StringToReturnStatus(agg.Request.Status) != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			return nil, status.Errorf(codes.FailedPrecondition,
				"invalid return status transition: expected ESCALATED, got %s", agg.Request.Status)
		}
		rejectNote := fallbackStr(note, "Admin rejected return dispute")
		// P1-3 + P1-2(b): Allow admin REJECT with audit note when auto-escalated from 14-day IN_TRANSIT timeout
		// or after 3 AWAITING_SHIPMENT deadline extensions because the return shipment could not be cancelled.
		if agg.Request.EscalatedFromTransitTimeout {
			rejectNote += " (vận đơn hoàn trả quá hạn vận chuyển 14 ngày, đã đóng mà không huỷ vận đơn đã lấy hàng)"
		} else if agg.Request.EscalatedFromCancelFailure {
			rejectNote += fmt.Sprintf(" (vận đơn hoàn trả không thể huỷ sau %d lần gia hạn, đã đóng mà không huỷ vận đơn)", maxReturnDeadlineExtensions)
		} else if strings.TrimSpace(agg.Request.ReturnTrackingCode) != "" &&
			!agg.Request.GoodsReceived &&
			!agg.Request.ReturnShipmentTerminal {
			// Cancel any still-live RETURN shipment BEFORE closing so we never close & pay seller
			// while the carrier has already picked up the buyer's parcel.
			if cErr := s.cancelReturnShipment(ctx, agg.Request.OrderID, rejectNote); cErr != nil {
				return nil, cErr
			}
		}
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_CLOSED
		params.DeadlineAt = nil
		params.Note = rejectNote
		params.BuildCompletedOutboxFn = func(ord db.Order) (db.InsertOutboxEventParams, error) {
			return s.buildOrderCompletedOutbox(ord, traceID, traceparent)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "valid admin decision is required")
	}

	res, err := s.store.TransitionReturnRequest(ctx, params)
	if err != nil {
		return nil, mapStoreError(err)
	}
	s.notifyOutbox()

	finalRet := res.Return
	switch params.TargetStatus {
	case orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:
		if updated, sErr := s.ensureReturnShipment(ctx, res.Return.Request, res.Order); sErr != nil {
			s.logger.WarnContext(ctx, "post-commit CreateReturnShipment failed; watchdog will retry",
				"return_request_id", retID, "order_id", res.Return.Request.OrderID, "error", sErr)
		} else if updated != nil {
			finalRet = updated
		}
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:
		if updated, rErr := s.executeRefundingSideEffects(ctx, res.Return.Request, res.Order); rErr != nil {
			s.logger.WarnContext(ctx, "post-commit REFUNDING side effects incomplete; watchdog will retry",
				"return_request_id", retID, "order_id", res.Return.Request.OrderID, "error", rErr)
		} else if updated != nil {
			finalRet = updated
		}
	}

	return &orderv1.AdminResolveReturnResponse{
		ReturnRequest: returnAggregateToProto(*finalRet),
	}, nil
}

// ensureReturnShipment creates the RETURN shipment post-commit using a service token and sets the 5-day ship deadline.
func (s *OrderService) ensureReturnShipment(ctx context.Context, rr db.ReturnRequest, ordAgg *store.OrderAggregate) (*store.ReturnAggregate, error) {
	unlock := s.idemLocks.Lock("retship:" + rr.ID)
	defer unlock()

	latest, err := s.store.GetReturnRequest(ctx, rr.ID, "")
	if err != nil {
		return nil, err
	}
	if statemachine.StringToReturnStatus(latest.Request.Status) != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
		strings.TrimSpace(latest.Request.ReturnTrackingCode) != "" {
		return latest, nil
	}
	if s.clients.Shipping == nil {
		return latest, nil
	}
	if ordAgg == nil {
		ordAgg, err = s.store.GetOrderWithDetails(ctx, latest.Request.OrderID)
		if err != nil {
			return nil, err
		}
	}

	ord := ordAgg.Order
	totalWeight, itemsSummary := summarizeOrderParcel(ordAgg.Items)
	buyerAddr := orderAddressToShipmentAddress(ord.ShippingAddressJSON)
	svcCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	shopAddr := s.resolveShopPickupAddress(svcCtx, ord.ShopID, buyerAddr)

	createShip := func(carrierCode, serviceCode string) (*shippingv1.CreateReturnShipmentResponse, error) {
		var respOut *shippingv1.CreateReturnShipmentResponse
		callErr := clients.RetryIdempotent(svcCtx, 3, func(c context.Context) error {
			resp, cErr := s.clients.Shipping.CreateReturnShipment(c, &shippingv1.CreateReturnShipmentRequest{
				OrderId:         ord.ID,
				ReturnRequestId: latest.Request.ID,
				ShopId:          ord.ShopID,
				BuyerId:         ord.BuyerID,
				FromAddress:     buyerAddr,
				ToAddress:       shopAddr,
				WeightGrams:     totalWeight,
				CarrierCode:     carrierCode,
				ServiceCode:     serviceCode,
				ItemsSummary:    itemsSummary,
			})
			if cErr != nil {
				return cErr
			}
			respOut = resp
			return nil
		})
		return respOut, callErr
	}

	shipResp, err := createShip(ord.CarrierCode, ord.ServiceCode)
	if err != nil &&
		(strings.TrimSpace(ord.CarrierCode) != "" || strings.TrimSpace(ord.ServiceCode) != "") &&
		(status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.FailedPrecondition) {
		// Original carrier/service is disabled or does not serve return route: let shipping auto-select.
		s.logger.WarnContext(ctx, "CreateReturnShipment failed for order carrier/service; retrying with auto-selection",
			"return_request_id", latest.Request.ID, "order_id", ord.ID,
			"carrier_code", ord.CarrierCode, "service_code", ord.ServiceCode, "error", err)
		shipResp, err = createShip("", "")
	}
	if err != nil {
		return nil, fmt.Errorf("CreateReturnShipment(%s): %w", latest.Request.ID, err)
	}

	ship := shipResp.GetShipment()
	if ship == nil || strings.TrimSpace(ship.GetTrackingCode()) == "" {
		return nil, fmt.Errorf("CreateReturnShipment(%s) returned empty tracking code", latest.Request.ID)
	}

	now := s.nowFn()
	deadline := now.Add(s.returnShipDuration())
	traceID, traceparent := traceMetaFromContext(ctx)
	updated, didUpdate, err := s.store.SaveReturnShipmentInfo(ctx, store.SaveReturnShipmentParams{
		ReturnRequestID: latest.Request.ID,
		TrackingCode:    ship.GetTrackingCode(),
		LabelURL:        ship.GetLabelUrl(),
		FeeVND:          maxInt64(0, ship.GetFeeVnd()),
		DeadlineAt:      &deadline,
		TraceID:         traceID,
		Traceparent:     traceparent,
		Now:             now,
	})
	if err != nil {
		return nil, err
	}
	if didUpdate {
		s.notifyOutbox()
	}
	// P3-1(d): If shipping returned an already-existing RETURN shipment that is already past pickup
	// (delivering/returning/delivered/terminal), sync return status immediately instead of waiting 5 days.
	if shipResp.GetAlreadyExisted() && protoShipmentStatusToString(ship.GetStatus()) != "" {
		if s.syncReturnFromShipping(ctx, updated.Request, traceID, traceparent, now) {
			if reloaded, rErr := s.store.GetReturnRequest(ctx, updated.Request.ID, ""); rErr == nil {
				updated = reloaded
			}
		}
	}
	return updated, nil
}

// executeRefundingSideEffects runs all idempotent post-commit side effects for a REFUNDING return
// (CreateRefund, ReleasePromotionShop, and conditional RestockOrderItems) and marks it REFUNDED once all succeed.
func (s *OrderService) executeRefundingSideEffects(ctx context.Context, rr db.ReturnRequest, ordAgg *store.OrderAggregate) (*store.ReturnAggregate, error) {
	unlock := s.idemLocks.Lock("refund:" + rr.ID)
	defer unlock()

	latest, err := s.store.GetReturnRequest(ctx, rr.ID, "")
	if err != nil {
		return nil, err
	}
	curRR := latest.Request
	if statemachine.StringToReturnStatus(curRR.Status) != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		return latest, nil
	}
	if ordAgg == nil {
		ordAgg, err = s.store.GetOrderWithDetails(ctx, curRR.OrderID)
		if err != nil {
			return nil, err
		}
	}
	ord := ordAgg.Order
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	// 1. Refund remaining unrefunded balance P - refunded_vnd (applies to both VIETQR and delivered COD).
	if !curRR.RefundDone {
		remaining := maxInt64(0, ord.TotalPayableVND-ord.RefundedVND)
		var refundAmtToSave *int64
		if remaining > 0 {
			if s.clients.Payment == nil {
				return nil, fmt.Errorf("CreateRefund for return %s (order %s): payment client is not configured", curRR.ID, ord.ID)
			}
			if strings.TrimSpace(ord.PaymentID) == "" {
				return nil, fmt.Errorf("CreateRefund for return %s (order %s): order payment_id is empty for remaining %d VND", curRR.ID, ord.ID, remaining)
			}
			actualRefunded := remaining
			if rErr := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
				resp, err := s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
					PaymentId:      ord.PaymentID,
					OrderId:        ord.ID,
					AmountVnd:      remaining,
					Reason:         "Order return refund",
					IdempotencyKey: "order-return-refund:" + ord.ID,
				})
				if err != nil {
					return err
				}
				if resp.GetRefund() != nil && resp.GetRefund().GetAmountVnd() > 0 {
					actualRefunded = resp.GetRefund().GetAmountVnd()
				}
				return nil
			}); rErr != nil {
				return nil, fmt.Errorf("CreateRefund for return %s (order %s): %w", curRR.ID, ord.ID, rErr)
			}
			refundAmtToSave = &actualRefunded
		} else if ord.TotalPayableVND == 0 {
			zero := int64(0)
			refundAmtToSave = &zero
		}
		// P2-6 + P2-2: only update refund_amount_vnd when a refund was issued (or 0-VND order);
		// never overwrite with 0 when recovering after CreateRefund + payment.refunded already settled the full amount.
		if updated, uErr := s.store.UpdateReturnSideEffectProgress(compCtx, curRR.ID, true, false, false, refundAmtToSave); uErr == nil && updated != nil {
			curRR = *updated
		}
	}

	// 2. Release shop-scoped promotions (coins, shop voucher, flash slots).
	if !curRR.PromoReleased {
		if pErr := s.releasePromotionShopErr(compCtx, ord.CheckoutGroupID, ord.ShopID, "order_returned"); pErr != nil {
			return nil, fmt.Errorf("ReleasePromotionShop for return %s (order %s): %w", curRR.ID, ord.ID, pErr)
		}
		if updated, uErr := s.store.UpdateReturnSideEffectProgress(compCtx, curRR.ID, false, true, false, nil); uErr == nil && updated != nil {
			curRR = *updated
		}
	}

	// 3. Restock items ONLY when goods physically reached the seller without a wrong-goods dispute.
	// P0-2: ConfirmStock(checkout_group_id) runs before RestockOrderItems so stock is never double-added.
	if curRR.ShouldRestock && !curRR.RestockDone {
		if sErr := s.restockOrderItemsWithConfirmErr(compCtx, ord.CheckoutGroupID, ord.ID, ordAgg.Items); sErr != nil {
			return nil, fmt.Errorf("RestockOrderItems for return %s (order %s): %w", curRR.ID, ord.ID, sErr)
		}
		if updated, uErr := s.store.UpdateReturnSideEffectProgress(compCtx, curRR.ID, false, false, true, nil); uErr == nil && updated != nil {
			curRR = *updated
		}
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	res, err := s.store.TransitionReturnRequest(compCtx, store.ReturnTransitionParams{
		ReturnRequestID: curRR.ID,
		ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		ActorID:         "order-service",
		ActorRole:       "system",
		Note:            "Return refund and compensations completed",
		UpdateDeadline:  true,
		DeadlineAt:      nil,
		TraceID:         traceID,
		Traceparent:     traceparent,
		Now:             s.nowFn(),
	})
	if err != nil {
		if errors.Is(err, statemachine.ErrInvalidReturnTransition) {
			return s.store.GetReturnRequest(ctx, curRR.ID, "")
		}
		return nil, err
	}
	s.notifyOutbox()
	return res.Return, nil
}

func (s *OrderService) getReturnShipment(ctx context.Context, orderID, trackingCode string) (*shippingv1.GetShipmentResponse, error) {
	if s.clients.Shipping == nil || strings.TrimSpace(orderID) == "" {
		return nil, status.Error(codes.NotFound, "shipping client unavailable")
	}
	// ShippingService.GetShipment is registered in internalRPCs=true (anonymous calls return Unauthenticated);
	// always mint a service token via detachedCompensationContext like CancelShipment so watchdogs and handlers both succeed.
	callCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	return s.clients.Shipping.GetShipment(callCtx, &shippingv1.GetShipmentRequest{
		OrderId:      orderID,
		TrackingCode: strings.TrimSpace(trackingCode),
		Direction:    shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
	})
}

func protoShipmentStatusToString(st shippingv1.ShipmentStatus) string {
	switch st {
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING:
		return "delivering"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNING:
		return "returning"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED:
		return "delivered"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNED:
		return "returned"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED:
		return "cancelled"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_FAILED:
		return "failed"
	default:
		return ""
	}
}

func (s *OrderService) syncReturnFromShipping(ctx context.Context, rr db.ReturnRequest, traceID, traceparent string, now time.Time) bool {
	shResp, err := s.getReturnShipment(ctx, rr.OrderID, rr.ReturnTrackingCode)
	if err != nil || shResp.GetShipment() == nil {
		return false
	}
	sh := shResp.GetShipment()
	shipStatusStr := protoShipmentStatusToString(sh.GetStatus())
	if shipStatusStr == "" {
		return false
	}
	var rawStatus, desc string
	if evs := shResp.GetEvents(); len(evs) > 0 {
		last := evs[len(evs)-1]
		rawStatus = last.GetRawStatus()
		desc = last.GetDescription()
	}
	if desc == "" {
		desc = "Synced return shipment status from shipping service: " + shipStatusStr
	}
	trk := sh.GetTrackingCode()
	if trk == "" {
		trk = rr.ReturnTrackingCode
	}
	outcome, aErr := s.store.ApplyReturnShipmentUpdate(ctx, store.ReturnShipmentUpdateParams{
		EventID:         "",
		EventType:       events.SubjectShipmentUpdated,
		OrderID:         rr.OrderID,
		ReturnRequestID: rr.ID,
		ShipmentStatus:  shipStatusStr,
		RawStatus:       rawStatus,
		TrackingCode:    trk,
		Description:     desc,
		InspectDuration: s.returnInspectDuration(),
		TransitDuration: returnTransitWindow,
		TraceID:         traceID,
		Traceparent:     traceparent,
		Now:             now,
	})
	if aErr != nil {
		s.logger.WarnContext(ctx, "syncReturnFromShipping failed", "return_request_id", rr.ID, "error", aErr)
		return false
	}
	if outcome.Transitioned {
		s.notifyOutbox()
		return true
	}
	return false
}

func (s *OrderService) cancelReturnShipment(ctx context.Context, orderID, reason string) error {
	if s.clients.Shipping == nil || strings.TrimSpace(orderID) == "" {
		return nil
	}
	svcCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	err := clients.RetryIdempotent(svcCtx, 3, func(c context.Context) error {
		_, cErr := s.clients.Shipping.CancelShipment(c, &shippingv1.CancelShipmentRequest{
			OrderId:   orderID,
			Reason:    reason,
			Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
		})
		return cErr
	})
	if err != nil && status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

// ScanReturnDeadlines processes expired return request deadlines
// (PENDING, AWAITING_SHIPMENT, IN_TRANSIT, REJECTED, RECEIVED, EVIDENCE_REQUESTED).
func (s *OrderService) ScanReturnDeadlines(ctx context.Context) (int, error) {
	now := s.nowFn()
	ids, err := s.store.FindExpiredReturnRequests(ctx, now, 20)
	if err != nil {
		return 0, err
	}

	handled := 0
	for _, id := range ids {
		if err := s.processExpiredReturnDeadline(ctx, id, now); err != nil {
			if errors.Is(err, statemachine.ErrInvalidReturnTransition) || errors.Is(err, store.ErrReturnDeadlineNotReached) {
				continue
			}
			s.logger.WarnContext(ctx, "return deadline sweep skipped item due to error", "return_request_id", id, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

func (s *OrderService) processExpiredReturnDeadline(ctx context.Context, returnID string, now time.Time) error {
	agg, err := s.store.GetReturnRequest(ctx, returnID, "")
	if err != nil {
		return err
	}
	rr := agg.Request
	curStatus := statemachine.StringToReturnStatus(rr.Status)
	traceID, traceparent := traceMetaFromContext(ctx)

	params := store.ReturnTransitionParams{
		ReturnRequestID:        rr.ID,
		ExpectedStatus:         curStatus,
		RequireDeadlineExpired: true,
		ActorID:                "order-return-watchdog",
		ActorRole:              "system",
		UpdateDeadline:         true,
		DeadlineAt:             nil,
		TraceID:                traceID,
		Traceparent:            traceparent,
		Now:                    now,
	}

	switch curStatus {
	case orderv1.ReturnStatus_RETURN_STATUS_PENDING:
		bearer := feeBearerSeller
		dl := now.Add(s.returnShipDuration())
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT
		params.ReturnFeeBearer = &bearer
		params.DeadlineAt = &dl
		params.Note = "Auto-accepted return after seller response deadline expired"
	case orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:
		if rr.DeadlineAt == nil || rr.DeadlineAt.After(now) {
			return store.ErrReturnDeadlineNotReached
		}
		if strings.TrimSpace(rr.ReturnTrackingCode) == "" {
			// Return shipment could never be created: escalate to admin rather than blaming/closing on buyer.
			reason := "Escalated automatically: unable to create return shipment before deadline"
			params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
			params.RequireNoTracking = true
			params.DisputeReason = &reason
			params.Note = reason
		} else {
			closeNote := "Closed automatically: buyer did not ship return within deadline"
			// Cancel RETURN shipment BEFORE closing so an already-picked-up parcel never causes buyer to lose goods + money.
			if cErr := s.cancelReturnShipment(ctx, rr.OrderID, closeNote); cErr != nil {
				if status.Code(cErr) == codes.FailedPrecondition {
					// P1-2: First sync live status via GetShipment(RETURN) in case delivering/delivered webhook was missed.
					if s.syncReturnFromShipping(ctx, rr, traceID, traceparent, now) {
						return nil
					}
					// Otherwise allow up to maxReturnDeadlineExtensions (+24h each), then auto-escalate to admin.
					extended, extErr := s.store.ExtendReturnDeadline(
						ctx,
						rr.ID,
						orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
						now.Add(returnPickupGraceWindow),
						now,
						maxReturnDeadlineExtensions,
					)
					if extErr != nil {
						return extErr
					}
					if extended {
						s.logger.WarnContext(ctx, "extended AWAITING_SHIPMENT deadline after CancelShipment FailedPrecondition",
							"return_request_id", rr.ID, "extension_count", rr.DeadlineExtensionCount+1)
						return nil
					}
					reason := fmt.Sprintf("Escalated automatically: return shipment could not be cancelled and remained unsynced after %d deadline extensions", maxReturnDeadlineExtensions)
					escCancelFail := true
					params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
					params.EscalatedFromCancelFailure = &escCancelFail
					params.DisputeReason = &reason
					params.Note = reason
				} else {
					return cErr
				}
			} else {
				params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_CLOSED
				params.RequireHasTracking = true
				params.Note = closeNote
				params.BuildCompletedOutboxFn = func(ord db.Order) (db.InsertOutboxEventParams, error) {
					return s.buildOrderCompletedOutbox(ord, traceID, traceparent)
				}
			}
		}
	case orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT:
		reason := "Escalated automatically: return shipment exceeded transit deadline (14 days)"
		escTransit := true
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
		params.EscalatedFromTransitTimeout = &escTransit
		params.DisputeReason = &reason
		params.Note = reason
	case orderv1.ReturnStatus_RETURN_STATUS_REJECTED:
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_CLOSED
		params.Note = "Closed automatically: buyer did not escalate rejection within deadline"
		params.BuildCompletedOutboxFn = func(ord db.Order) (db.InsertOutboxEventParams, error) {
			return s.buildOrderCompletedOutbox(ord, traceID, traceparent)
		}
	case orderv1.ReturnStatus_RETURN_STATUS_RECEIVED:
		shouldRestock := true
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_REFUNDING
		params.ShouldRestock = &shouldRestock
		params.Note = "Auto-confirmed returned goods after seller inspection deadline expired"
	case orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED:
		params.TargetStatus = orderv1.ReturnStatus_RETURN_STATUS_CLOSED
		params.Note = "Closed automatically: buyer did not provide requested evidence within deadline"
		params.BuildCompletedOutboxFn = func(ord db.Order) (db.InsertOutboxEventParams, error) {
			return s.buildOrderCompletedOutbox(ord, traceID, traceparent)
		}
	default:
		return nil
	}

	res, err := s.store.TransitionReturnRequest(ctx, params)
	if err != nil {
		return err
	}
	s.notifyOutbox()

	switch params.TargetStatus {
	case orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:
		if _, sErr := s.ensureReturnShipment(ctx, res.Return.Request, res.Order); sErr != nil {
			s.logger.WarnContext(ctx, "deadline auto-accept CreateReturnShipment failed; pending-shipment watchdog will retry",
				"return_request_id", rr.ID, "error", sErr)
		}
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:
		if _, rErr := s.executeRefundingSideEffects(ctx, res.Return.Request, res.Order); rErr != nil {
			s.logger.WarnContext(ctx, "deadline auto-confirm REFUNDING side effects incomplete; refunding watchdog will retry",
				"return_request_id", rr.ID, "error", rErr)
		}
	}
	return nil
}

// ScanReturnPendingShipments retries CreateReturnShipment for AWAITING_SHIPMENT returns that do not yet have a tracking code.
func (s *OrderService) ScanReturnPendingShipments(ctx context.Context) (int, error) {
	ids, err := s.store.FindAwaitingShipmentReturnsWithoutTracking(ctx, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, id := range ids {
		agg, gErr := s.store.GetReturnRequest(ctx, id, "")
		if gErr != nil {
			continue
		}
		if _, sErr := s.ensureReturnShipment(ctx, agg.Request, nil); sErr != nil {
			s.logger.WarnContext(ctx, "retry CreateReturnShipment failed, continuing batch", "return_request_id", id, "error", sErr)
			continue
		}
		handled++
	}
	return handled, nil
}

// ScanRefundingReturns retries post-commit refund/promotion/restock side effects for returns stuck in REFUNDING.
func (s *OrderService) ScanRefundingReturns(ctx context.Context) (int, error) {
	ids, err := s.store.FindRefundingReturnRequests(ctx, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, id := range ids {
		agg, gErr := s.store.GetReturnRequest(ctx, id, "")
		if gErr != nil {
			continue
		}
		if _, rErr := s.executeRefundingSideEffects(ctx, agg.Request, nil); rErr != nil {
			s.logger.WarnContext(ctx, "retry REFUNDING side effects failed, continuing batch", "return_request_id", id, "error", rErr)
			continue
		}
		handled++
	}
	return handled, nil
}
