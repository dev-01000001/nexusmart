package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RequestReturn transitions DELIVERED -> RETURN_REQUESTED, persists an extended return_requests record,
// and emits order.return_requested outbox event atomically.
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

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canActAsBuyer(caller, agg.Order, req.GetBuyerId()) {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order can request a return")
	}

	if agg.Order.DeliveredAt != nil && s.nowFn().Sub(*agg.Order.DeliveredAt) > returnWindowDuration {
		return nil, status.Error(codes.FailedPrecondition, "return request window (7 days) has expired")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	normalizedItems, refundAmountVND, err := resolveReturnItemsAndAmount(agg, req.GetItems())
	if err != nil {
		return nil, err
	}

	itemsJSON, err := json.Marshal(normalizedItems)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal return items: %v", err)
	}

	evidence := req.GetEvidenceUrls()
	if evidence == nil {
		evidence = []string{}
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal evidence_urls: %v", err)
	}

	returnID := uuid.NewString()
	traceID, traceparent := traceMetaFromContext(ctx)
	outboxPayload, err := json.Marshal(events.OrderReturnRequestedPayload{
		OrderID:          agg.Order.ID,
		OrderCode:        agg.Order.OrderCode,
		BuyerID:          agg.Order.BuyerID,
		ShopID:           agg.Order.ShopID,
		StorefrontShopID: agg.Order.StorefrontShopID,
		ReturnRequestID:  returnID,
		RefundAmountVND:  refundAmountVND,
		Reason:           reason,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal order.return_requested event: %v", err)
	}

	updatedAgg, rr, err := s.store.TransitionOrderReturnRequested(ctx, store.ReturnRequestParams{
		OrderID:          agg.Order.ID,
		BuyerID:          agg.Order.BuyerID,
		ShopID:           agg.Order.ShopID,
		ReturnRequestID:  returnID,
		ItemsJSON:        itemsJSON,
		Reason:           reason,
		EvidenceURLsJSON: evidenceJSON,
		RefundAmountVND:  refundAmountVND,
		ActorID:          fallbackStr(caller.UserID, agg.Order.BuyerID),
		ActorRole:        fallbackStr(caller.Role, "buyer"),
		Note:             "Return requested: " + reason,
		OutboxEvent: db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderReturnRequested,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: outboxPayload,
		},
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.RequestReturnResponse{
		Order:           aggregateToProto(*updatedAgg),
		ReturnRequestId: returnID,
		ReturnRequest:   returnRequestToProto(*rr),
	}, nil
}

// ListOrderReturns lists return requests with RBAC enforcement (buyer owns order, seller owns shop, or admin).
func (s *OrderService) ListOrderReturns(ctx context.Context, req *orderv1.ListOrderReturnsRequest) (*orderv1.ListOrderReturnsResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}

	orderFilter := strings.TrimSpace(req.GetOrderId())
	shopFilter := strings.TrimSpace(req.GetShopId())
	buyerFilter := ""

	if orderFilter != "" {
		agg, err := s.store.GetOrderWithDetails(ctx, orderFilter)
		if err != nil {
			return nil, mapStoreError(err)
		}
		if !canViewOrCancelOrder(caller, agg.Order) {
			return nil, status.Error(codes.PermissionDenied, "access denied to returns for this order")
		}
		if !caller.IsPrivileged && shopFilter != "" && shopFilter != agg.Order.ShopID {
			return nil, status.Error(codes.PermissionDenied, "shop_id does not match order shop")
		}
	}

	if !caller.IsPrivileged {
		switch caller.Role {
		case "seller":
			if shopFilter != "" && shopFilter != caller.ShopID {
				return nil, status.Error(codes.PermissionDenied, "cannot list returns for another shop")
			}
			if orderFilter == "" {
				if caller.ShopID == "" {
					return nil, status.Error(codes.PermissionDenied, "seller shop identity is required")
				}
				shopFilter = caller.ShopID
			}
		default:
			if orderFilter == "" {
				if shopFilter != "" {
					return nil, status.Error(codes.PermissionDenied, "only shop owner or admin can list returns by shop")
				}
				buyerFilter = caller.UserID
			}
		}
	}

	var statusFilter string
	if req.GetStatus() != orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_UNSPECIFIED {
		statusFilter = statemachine.ReturnRequestStatusToString(req.GetStatus())
	}

	page, pageSize, offset := normalizePagination(req.GetPage(), req.GetPageSize())
	_ = page

	rows, total, err := s.store.ListReturnRequestsFiltered(ctx, orderFilter, buyerFilter, shopFilter, statusFilter, pageSize, offset)
	if err != nil {
		return nil, mapStoreError(err)
	}

	out := make([]*orderv1.ReturnRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, returnRequestToProto(r))
	}
	return &orderv1.ListOrderReturnsResponse{
		Returns: out,
		Total:   total,
	}, nil
}

// ApproveReturn approves a buyer's return request (seller owning shop or admin).
// If immediate_refund is true, runs the idempotent Refund Saga immediately -> REFUNDED.
// Otherwise creates a reverse shipment via ShippingService -> RETURNING.
func (s *OrderService) ApproveReturn(ctx context.Context, req *orderv1.ApproveReturnRequest) (*orderv1.ApproveReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	rrID := strings.TrimSpace(req.GetReturnRequestId())
	if rrID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}

	rr, err := s.store.GetReturnRequestByID(ctx, rrID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	agg, err := s.store.GetOrderWithDetails(ctx, rr.OrderID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canManageShopOrder(caller, agg.Order, req.GetShopId()) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller or admin can approve returns")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	actorID := fallbackStr(caller.UserID, req.GetSellerUserId())
	actorRole := fallbackStr(caller.Role, "seller")
	sellerNote := strings.TrimSpace(req.GetNote())
	traceID, traceparent := traceMetaFromContext(ctx)

	if req.GetImmediateRefund() {
		refundAmount := effectiveRefundAmount(agg.Order, rr)
		approvedPayload, err := json.Marshal(events.OrderReturnApprovedPayload{
			OrderID:          agg.Order.ID,
			OrderCode:        agg.Order.OrderCode,
			BuyerID:          agg.Order.BuyerID,
			ShopID:           agg.Order.ShopID,
			StorefrontShopID: agg.Order.StorefrontShopID,
			ReturnRequestID:  rr.ID,
			ImmediateRefund:  true,
			RefundAmountVND:  refundAmount,
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal order.return_approved event: %v", err)
		}
		extraOutbox := []db.InsertOutboxEventParams{
			{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderReturnApproved,
				TraceID:     traceID,
				Traceparent: traceparent,
				PayloadJSON: approvedPayload,
			},
		}

		approveNote := "Return approved with immediate refund"
		if sellerNote != "" {
			approveNote = "Return approved (immediate refund): " + sellerNote
		}
		outcome, err := s.executeReturnRefundSaga(ctx, refundSagaInput{
			Order:                 agg,
			ReturnRequest:         rr,
			SellerNote:            sellerNote,
			Reason:                fallbackStr(sellerNote, rr.Reason),
			IntermediateStatus:    orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED,
			IntermediateActorID:   actorID,
			IntermediateActorRole: actorRole,
			IntermediateNote:      approveNote,
			ActorID:               actorID,
			ActorRole:             actorRole,
			Note:                  "Immediate return refund completed",
			ExtraOutbox:           extraOutbox,
		})
		if err != nil {
			return nil, err
		}
		return &orderv1.ApproveReturnResponse{
			Order:         aggregateToProto(*outcome.Order),
			ReturnRequest: returnRequestToProto(*outcome.ReturnRequest),
		}, nil
	}

	// Default path: create reverse shipment (buyer shipping address -> shop pickup address) and transition to RETURNING.
	shipment, err := s.createReturnShipmentForOrder(ctx, agg, rr, req.GetCarrierCode(), req.GetServiceCode())
	if err != nil {
		return nil, err
	}

	trackingCode := shipment.GetTrackingCode()
	labelURL := shipment.GetLabelUrl()
	refundAmount := effectiveRefundAmount(agg.Order, rr)

	approvedPayload, err := json.Marshal(events.OrderReturnApprovedPayload{
		OrderID:             agg.Order.ID,
		OrderCode:           agg.Order.OrderCode,
		BuyerID:             agg.Order.BuyerID,
		ShopID:              agg.Order.ShopID,
		StorefrontShopID:    agg.Order.StorefrontShopID,
		ReturnRequestID:     rr.ID,
		ImmediateRefund:     false,
		ReverseTrackingCode: trackingCode,
		ReverseLabelURL:     labelURL,
		RefundAmountVND:     refundAmount,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal order.return_approved event: %v", err)
	}

	approveNote := "Return approved by seller"
	if sellerNote != "" {
		approveNote = "Return approved: " + sellerNote
	}
	returningNote := fmt.Sprintf("Reverse shipment created (tracking: %s)", trackingCode)

	updatedAgg, updatedRR, _, err := s.store.ApproveReturnReturning(ctx, store.ApproveReturnReturningParams{
		ReturnRequestID:     rr.ID,
		OrderID:             agg.Order.ID,
		SellerNote:          sellerNote,
		ReverseTrackingCode: trackingCode,
		ReverseLabelURL:     labelURL,
		ActorID:             actorID,
		ActorRole:           actorRole,
		ApproveNote:         approveNote,
		ReturningNote:       returningNote,
		OutboxEvents: []db.InsertOutboxEventParams{
			{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderReturnApproved,
				TraceID:     traceID,
				Traceparent: traceparent,
				PayloadJSON: approvedPayload,
			},
		},
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.ApproveReturnResponse{
		Order:         aggregateToProto(*updatedAgg),
		ReturnRequest: returnRequestToProto(*updatedRR),
	}, nil
}

// RejectReturn rejects a buyer's return request (seller owning shop or admin)
// and automatically escalates an OPEN complaint (UC-S10 -> UC-A05).
func (s *OrderService) RejectReturn(ctx context.Context, req *orderv1.RejectReturnRequest) (*orderv1.RejectReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	rrID := strings.TrimSpace(req.GetReturnRequestId())
	if rrID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "rejection reason is required")
	}

	rr, err := s.store.GetReturnRequestByID(ctx, rrID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	agg, err := s.store.GetOrderWithDetails(ctx, rr.OrderID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canManageShopOrder(caller, agg.Order, req.GetShopId()) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller or admin can reject returns")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	actorID := fallbackStr(caller.UserID, req.GetSellerUserId())
	actorRole := fallbackStr(caller.Role, "seller")

	updatedAgg, updatedRR, complaint, err := s.store.RejectReturnAndCreateComplaint(ctx, store.RejectReturnParams{
		ReturnRequestID:  rr.ID,
		OrderID:          agg.Order.ID,
		Reason:           reason,
		ComplaintID:      uuid.NewString(),
		ComplaintReason:  "Seller rejected return request: " + reason,
		EvidenceURLsJSON: rr.EvidenceURLsJSON,
		ActorID:          actorID,
		ActorRole:        actorRole,
		Note:             "Return rejected: " + reason,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	return &orderv1.RejectReturnResponse{
		Order:         aggregateToProto(*updatedAgg),
		ReturnRequest: returnRequestToProto(*updatedRR),
		Complaint:     complaintToProto(*complaint),
	}, nil
}

// ConfirmReturnReceived confirms receipt of returned goods (seller owning shop or admin)
// and executes the full idempotent Refund Saga -> REFUNDED.
func (s *OrderService) ConfirmReturnReceived(ctx context.Context, req *orderv1.ConfirmReturnReceivedRequest) (*orderv1.ConfirmReturnReceivedResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	rrID := strings.TrimSpace(req.GetReturnRequestId())
	if rrID == "" {
		return nil, status.Error(codes.InvalidArgument, "return_request_id is required")
	}

	rr, err := s.store.GetReturnRequestByID(ctx, rrID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	agg, err := s.store.GetOrderWithDetails(ctx, rr.OrderID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if !canManageShopOrder(caller, agg.Order, req.GetShopId()) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller or admin can confirm return receipt")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_REFUNDED {
		return &orderv1.ConfirmReturnReceivedResponse{
			Order:         aggregateToProto(*agg),
			ReturnRequest: returnRequestToProto(*rr),
		}, nil
	}
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_RETURNING &&
		curStatus != orderv1.OrderStatus_ORDER_STATUS_RETURNED &&
		curStatus != orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot confirm return receipt in order status %s", agg.Order.Status)
	}

	note := strings.TrimSpace(req.GetNote())
	tlNote := "Seller confirmed returned goods received and completed refund"
	if note != "" {
		tlNote = "Return received: " + note
	}

	outcome, err := s.executeReturnRefundSaga(ctx, refundSagaInput{
		Order:         agg,
		ReturnRequest: rr,
		SellerNote:    note,
		Reason:        fallbackStr(note, "Seller confirmed return received"),
		ActorID:       fallbackStr(caller.UserID, req.GetSellerUserId()),
		ActorRole:     fallbackStr(caller.Role, "seller"),
		Note:          tlNote,
	})
	if err != nil {
		return nil, err
	}

	return &orderv1.ConfirmReturnReceivedResponse{
		Order:         aggregateToProto(*outcome.Order),
		ReturnRequest: returnRequestToProto(*outcome.ReturnRequest),
	}, nil
}

// CreateComplaint allows a buyer (owning order) or admin to file or update evidence/reason on an OPEN complaint
// for an order whose return was rejected.
func (s *OrderService) CreateComplaint(ctx context.Context, req *orderv1.CreateComplaintRequest) (*orderv1.CreateComplaintResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}

	orderID := strings.TrimSpace(req.GetOrderId())
	rrID := strings.TrimSpace(req.GetReturnRequestId())
	if orderID == "" && rrID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id or return_request_id is required")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "complaint reason is required")
	}

	var rr *db.ReturnRequest
	var agg *store.OrderAggregate

	if rrID != "" {
		rr, err = s.store.GetReturnRequestByID(ctx, rrID)
		if err != nil {
			return nil, mapStoreError(err)
		}
		if orderID != "" && orderID != rr.OrderID {
			return nil, status.Error(codes.InvalidArgument, "order_id does not match return_request_id")
		}
		agg, err = s.store.GetOrderWithDetails(ctx, rr.OrderID)
		if err != nil {
			return nil, mapStoreError(err)
		}
	} else {
		agg, err = s.store.GetOrderWithDetails(ctx, orderID)
		if err != nil {
			return nil, mapStoreError(err)
		}
		rr, err = s.store.GetLatestReturnRequestByOrderID(ctx, orderID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Error(codes.FailedPrecondition, "order has no rejected return request")
			}
			return nil, mapStoreError(err)
		}
	}

	if !canActAsBuyer(caller, agg.Order, req.GetBuyerId()) {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order or admin can file a complaint")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED && rr.Status != "REJECTED" {
		return nil, status.Error(codes.FailedPrecondition, "complaints can only be created when a return request has been rejected")
	}

	evidence := req.GetEvidenceUrls()
	updateEvidence := len(evidence) > 0
	if evidence == nil {
		evidence = []string{}
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal complaint evidence_urls: %v", err)
	}

	cp, err := s.store.UpsertOpenComplaint(ctx, store.UpsertComplaintParams{
		ComplaintID:      uuid.NewString(),
		ReturnRequestID:  rr.ID,
		OrderID:          agg.Order.ID,
		BuyerID:          agg.Order.BuyerID,
		ShopID:           agg.Order.ShopID,
		Reason:           reason,
		EvidenceURLsJSON: evidenceJSON,
		UpdateEvidence:   updateEvidence,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	return &orderv1.CreateComplaintResponse{
		Complaint: complaintToProto(*cp),
	}, nil
}

// ListComplaints lists complaints for admin / super_admin only (403 for buyer/seller).
func (s *OrderService) ListComplaints(ctx context.Context, req *orderv1.ListComplaintsRequest) (*orderv1.ListComplaintsResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsPrivileged {
		return nil, status.Error(codes.PermissionDenied, "admin role required to list complaints")
	}

	var statusFilter string
	if req.GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_UNSPECIFIED {
		statusFilter = statemachine.ComplaintStatusToString(req.GetStatus())
	}

	_, pageSize, offset := normalizePagination(req.GetPage(), req.GetPageSize())
	rows, total, err := s.store.ListComplaintsFiltered(
		ctx,
		statusFilter,
		strings.TrimSpace(req.GetOrderId()),
		strings.TrimSpace(req.GetShopId()),
		pageSize,
		offset,
	)
	if err != nil {
		return nil, mapStoreError(err)
	}

	out := make([]*orderv1.Complaint, 0, len(rows))
	for _, cp := range rows {
		out = append(out, complaintToProto(cp))
	}
	return &orderv1.ListComplaintsResponse{
		Complaints: out,
		Total:      total,
	}, nil
}

// ResolveComplaint allows admin / super_admin to resolve an OPEN complaint with REFUND, REPLACE, or DISMISS.
func (s *OrderService) ResolveComplaint(ctx context.Context, req *orderv1.ResolveComplaintRequest) (*orderv1.ResolveComplaintResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsPrivileged {
		return nil, status.Error(codes.PermissionDenied, "admin role required to resolve complaints")
	}

	complaintID := strings.TrimSpace(req.GetComplaintId())
	if complaintID == "" {
		return nil, status.Error(codes.InvalidArgument, "complaint_id is required")
	}

	resolution := req.GetResolution()
	if resolution != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND &&
		resolution != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE &&
		resolution != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS {
		return nil, status.Error(codes.InvalidArgument, "resolution must be REFUND, REPLACE, or DISMISS")
	}

	cp, err := s.store.GetComplaintByID(ctx, complaintID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if cp.Status != "OPEN" {
		return nil, status.Error(codes.FailedPrecondition, "complaint is already closed")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, cp.OrderID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	rr, err := s.store.GetReturnRequestByID(ctx, cp.ReturnRequestID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	adminNote := strings.TrimSpace(req.GetAdminNote())
	traceID, traceparent := traceMetaFromContext(ctx)
	actorRole := fallbackStr(caller.Role, "admin")

	switch resolution {
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND:
		curStatus := statemachine.StringToStatus(agg.Order.Status)
		if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}

		refundAmount := effectiveRefundAmount(agg.Order, rr)
		if req.GetRequireReturnShipping() {
			shipment, err := s.createReturnShipmentForOrder(ctx, agg, rr, "", "")
			if err != nil {
				return nil, err
			}
			approvedPayload, err := json.Marshal(events.OrderReturnApprovedPayload{
				OrderID:             agg.Order.ID,
				OrderCode:           agg.Order.OrderCode,
				BuyerID:             agg.Order.BuyerID,
				ShopID:              agg.Order.ShopID,
				StorefrontShopID:    agg.Order.StorefrontShopID,
				ReturnRequestID:     rr.ID,
				ImmediateRefund:     false,
				ReverseTrackingCode: shipment.GetTrackingCode(),
				ReverseLabelURL:     shipment.GetLabelUrl(),
				RefundAmountVND:     refundAmount,
			})
			if err != nil {
				return nil, status.Errorf(codes.Internal, "marshal order.return_approved event: %v", err)
			}

			updatedAgg, updatedRR, updatedCP, err := s.store.ApproveReturnReturning(ctx, store.ApproveReturnReturningParams{
				ReturnRequestID:     rr.ID,
				OrderID:             agg.Order.ID,
				ReverseTrackingCode: shipment.GetTrackingCode(),
				ReverseLabelURL:     shipment.GetLabelUrl(),
				ActorID:             caller.UserID,
				ActorRole:           actorRole,
				ApproveNote:         fallbackStr(adminNote, "Admin resolved complaint in favor of refund (return shipping required)"),
				ReturningNote:       fmt.Sprintf("Reverse shipment created (tracking: %s)", shipment.GetTrackingCode()),
				OutboxEvents: []db.InsertOutboxEventParams{
					{
						ID:          uuid.NewString(),
						EventType:   events.SubjectOrderReturnApproved,
						TraceID:     traceID,
						Traceparent: traceparent,
						PayloadJSON: approvedPayload,
					},
				},
				ComplaintID:         cp.ID,
				ComplaintStatus:     "RESOLVED",
				ComplaintResolution: "REFUND",
				AdminNote:           adminNote,
				ResolvedBy:          caller.UserID,
			})
			if err != nil {
				return nil, mapStoreError(err)
			}
			s.notifyOutbox()
			return &orderv1.ResolveComplaintResponse{
				Complaint:     complaintToProto(*updatedCP),
				Order:         aggregateToProto(*updatedAgg),
				ReturnRequest: returnRequestToProto(*updatedRR),
			}, nil
		}

		// Immediate refund without return shipping: RETURN_REJECTED -> RETURN_APPROVED -> REFUNDED.
		approvedPayload, err := json.Marshal(events.OrderReturnApprovedPayload{
			OrderID:          agg.Order.ID,
			OrderCode:        agg.Order.OrderCode,
			BuyerID:          agg.Order.BuyerID,
			ShopID:           agg.Order.ShopID,
			StorefrontShopID: agg.Order.StorefrontShopID,
			ReturnRequestID:  rr.ID,
			ImmediateRefund:  true,
			RefundAmountVND:  refundAmount,
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal order.return_approved event: %v", err)
		}

		outcome, err := s.executeReturnRefundSaga(ctx, refundSagaInput{
			Order:                 agg,
			ReturnRequest:         rr,
			Reason:                fallbackStr(adminNote, "Admin resolved complaint with immediate refund"),
			IntermediateStatus:    orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED,
			IntermediateActorID:   caller.UserID,
			IntermediateActorRole: actorRole,
			IntermediateNote:      fallbackStr(adminNote, "Admin approved return via complaint resolution"),
			ActorID:               caller.UserID,
			ActorRole:             actorRole,
			Note:                  "Complaint resolved with immediate refund",
			ExtraOutbox: []db.InsertOutboxEventParams{
				{
					ID:          uuid.NewString(),
					EventType:   events.SubjectOrderReturnApproved,
					TraceID:     traceID,
					Traceparent: traceparent,
					PayloadJSON: approvedPayload,
				},
			},
			ComplaintID:         cp.ID,
			ComplaintStatus:     "RESOLVED",
			ComplaintResolution: "REFUND",
			AdminNote:           adminNote,
			ResolvedBy:          caller.UserID,
		})
		if err != nil {
			return nil, err
		}
		return &orderv1.ResolveComplaintResponse{
			Complaint:     complaintToProto(*outcome.Complaint),
			Order:         aggregateToProto(*outcome.Order),
			ReturnRequest: returnRequestToProto(*outcome.ReturnRequest),
		}, nil

	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE:
		curStatus := statemachine.StringToStatus(agg.Order.Status)
		if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}

		shipment, err := s.createReturnShipmentForOrder(ctx, agg, rr, "", "")
		if err != nil {
			return nil, err
		}
		refundAmount := effectiveRefundAmount(agg.Order, rr)
		approvedPayload, err := json.Marshal(events.OrderReturnApprovedPayload{
			OrderID:             agg.Order.ID,
			OrderCode:           agg.Order.OrderCode,
			BuyerID:             agg.Order.BuyerID,
			ShopID:              agg.Order.ShopID,
			StorefrontShopID:    agg.Order.StorefrontShopID,
			ReturnRequestID:     rr.ID,
			ImmediateRefund:     false,
			ReverseTrackingCode: shipment.GetTrackingCode(),
			ReverseLabelURL:     shipment.GetLabelUrl(),
			RefundAmountVND:     refundAmount,
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal order.return_approved event: %v", err)
		}

		updatedAgg, updatedRR, updatedCP, err := s.store.ApproveReturnReturning(ctx, store.ApproveReturnReturningParams{
			ReturnRequestID:     rr.ID,
			OrderID:             agg.Order.ID,
			ReverseTrackingCode: shipment.GetTrackingCode(),
			ReverseLabelURL:     shipment.GetLabelUrl(),
			ActorID:             caller.UserID,
			ActorRole:           actorRole,
			ApproveNote:         fallbackStr(adminNote, "Admin resolved complaint with replacement"),
			ReturningNote:       fmt.Sprintf("Reverse shipment created for replacement (tracking: %s)", shipment.GetTrackingCode()),
			OutboxEvents: []db.InsertOutboxEventParams{
				{
					ID:          uuid.NewString(),
					EventType:   events.SubjectOrderReturnApproved,
					TraceID:     traceID,
					Traceparent: traceparent,
					PayloadJSON: approvedPayload,
				},
			},
			ComplaintID:         cp.ID,
			ComplaintStatus:     "RESOLVED",
			ComplaintResolution: "REPLACE",
			AdminNote:           adminNote,
			ResolvedBy:          caller.UserID,
		})
		if err != nil {
			return nil, mapStoreError(err)
		}
		s.notifyOutbox()
		return &orderv1.ResolveComplaintResponse{
			Complaint:     complaintToProto(*updatedCP),
			Order:         aggregateToProto(*updatedAgg),
			ReturnRequest: returnRequestToProto(*updatedRR),
		}, nil

	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS:
		curStatus := statemachine.StringToStatus(agg.Order.Status)
		if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}

		coinsReward := ComputeRewardCoins(agg.Order.SubtotalVND, s.cfg.RewardCoinsBPS)
		completedPayload, err := json.Marshal(events.OrderCompletedPayload{
			OrderID:          agg.Order.ID,
			OrderCode:        agg.Order.OrderCode,
			BuyerID:          agg.Order.BuyerID,
			ShopID:           agg.Order.ShopID,
			StorefrontShopID: agg.Order.StorefrontShopID,
			SubtotalVND:      agg.Order.SubtotalVND,
			CommissionVND:    agg.Order.CommissionVND,
			TotalPayableVND:  agg.Order.TotalPayableVND,
			CoinsReward:      coinsReward,
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal order.completed event: %v", err)
		}

		updatedAgg, updatedRR, updatedCP, err := s.store.DismissComplaintAndCompleteOrder(ctx, store.DismissComplaintParams{
			ComplaintID: cp.ID,
			OrderID:     agg.Order.ID,
			AdminNote:   adminNote,
			ResolvedBy:  caller.UserID,
			ActorRole:   actorRole,
			Note:        fallbackStr(adminNote, "Admin dismissed complaint and completed order"),
			OutboxEvent: db.InsertOutboxEventParams{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderCompleted,
				TraceID:     traceID,
				Traceparent: traceparent,
				PayloadJSON: completedPayload,
			},
		})
		if err != nil {
			return nil, mapStoreError(err)
		}

		// Release payment escrow (best-effort with retry alongside idempotent order.completed outbox).
		if s.clients.Payment != nil {
			relCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			defer cancel()
			if rErr := clients.RetryIdempotent(relCtx, 3, func(c context.Context) error {
				_, e := s.clients.Payment.ReleaseEscrow(c, &paymentv1.ReleaseEscrowRequest{
					OrderId: agg.Order.ID,
					ShopId:  agg.Order.ShopID,
				})
				return e
			}); rErr != nil {
				s.logger.WarnContext(ctx, "direct ReleaseEscrow failed on complaint dismissal",
					"order_id", agg.Order.ID, "complaint_id", cp.ID, "error", rErr)
			}
		}

		s.notifyOutbox()
		var protoRR *orderv1.ReturnRequest
		if updatedRR != nil {
			protoRR = returnRequestToProto(*updatedRR)
		}
		return &orderv1.ResolveComplaintResponse{
			Complaint:     complaintToProto(*updatedCP),
			Order:         aggregateToProto(*updatedAgg),
			ReturnRequest: protoRR,
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "unsupported complaint resolution")
}

// --- Return / Refund Saga Helpers ---

type refundSagaInput struct {
	EventID               string
	EventType             string
	Order                 *store.OrderAggregate
	ReturnRequest         *db.ReturnRequest
	SellerNote            string
	Reason                string
	IntermediateStatus    orderv1.OrderStatus
	IntermediateActorID   string
	IntermediateActorRole string
	IntermediateNote      string
	ActorID               string
	ActorRole             string
	Note                  string
	ExtraOutbox           []db.InsertOutboxEventParams
	ComplaintID           string
	ComplaintStatus       string
	ComplaintResolution   string
	AdminNote             string
	ResolvedBy            string
}

func (s *OrderService) executeReturnRefundSaga(ctx context.Context, in refundSagaInput) (*store.CompleteReturnRefundOutcome, error) {
	ord := in.Order.Order
	rr := in.ReturnRequest

	refundKeyID := ord.ID
	rrID := ""
	if rr != nil {
		rrID = rr.ID
		refundKeyID = rr.ID
	}

	refundAmount := effectiveRefundAmount(ord, rr)
	reason := fallbackStr(in.Reason, "Order return refunded")

	// Step 1: Call PaymentService.CreateRefund idempotently when order was paid online (VietQR or ZaloPay).
	wasPaidOnline := statemachine.IsOnlinePaymentMethodString(ord.PaymentMethod) &&
		strings.EqualFold(ord.PaymentStatus, "SUCCEEDED")
	refundID := ""
	if rr != nil {
		refundID = rr.RefundID
	}
	paymentStatusUpdate := ""

	if wasPaidOnline && ord.PaymentID != "" && s.clients.Payment != nil && refundAmount > 0 {
		compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()

		var refundResp *paymentv1.CreateRefundResponse
		err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			var rErr error
			refundResp, rErr = s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
				PaymentId:      ord.PaymentID,
				OrderId:        ord.ID,
				AmountVnd:      refundAmount,
				Reason:         reason,
				IdempotencyKey: "order-return-refund:" + refundKeyID,
			})
			return rErr
		})
		if err != nil {
			return nil, fmt.Errorf("create refund for order %s: %w", ord.ID, err)
		}
		if refundResp != nil && refundResp.GetRefund() != nil {
			refundID = refundResp.GetRefund().GetRefundId()
		}
		paymentStatusUpdate = "REFUNDED"
	}

	// Step 2: Check whether any active sibling sub-order remains in the checkout group.
	allGroupClosed := s.areAllSiblingOrdersClosed(ctx, ord.CheckoutGroupID, ord.ID)

	// Step 3: Build order.refunded outbox event payload.
	traceID, traceparent := traceMetaFromContext(ctx)
	refundedPayload, err := json.Marshal(events.OrderRefundedPayload{
		OrderID:              ord.ID,
		OrderCode:            ord.OrderCode,
		CheckoutGroupID:      ord.CheckoutGroupID,
		BuyerID:              ord.BuyerID,
		ShopID:               ord.ShopID,
		StorefrontShopID:     ord.StorefrontShopID,
		ReturnRequestID:      rrID,
		RefundID:             refundID,
		RefundAmountVND:      refundAmount,
		CoinsDiscountVND:     ord.CoinsDiscountVND,
		AllGroupOrdersClosed: allGroupClosed,
		Reason:               reason,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal order.refunded event: %v", err)
	}

	outboxEvents := append([]db.InsertOutboxEventParams(nil), in.ExtraOutbox...)
	outboxEvents = append(outboxEvents, db.InsertOutboxEventParams{
		ID:          uuid.NewString(),
		EventType:   events.SubjectOrderRefunded,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: refundedPayload,
	})

	// Step 4: Atomically transition order -> REFUNDED (+ return request, complaint, timeline, outbox, processed_events).
	outcome, err := s.store.CompleteReturnRefund(ctx, store.CompleteReturnRefundParams{
		EventID:               in.EventID,
		EventType:             in.EventType,
		ReturnRequestID:       rrID,
		OrderID:               ord.ID,
		RefundID:              refundID,
		RefundAmountVND:       refundAmount,
		PaymentStatus:         paymentStatusUpdate,
		SellerNote:            in.SellerNote,
		IntermediateStatus:    in.IntermediateStatus,
		IntermediateActorID:   in.IntermediateActorID,
		IntermediateActorRole: in.IntermediateActorRole,
		IntermediateNote:      in.IntermediateNote,
		ActorID:               in.ActorID,
		ActorRole:             in.ActorRole,
		Note:                  in.Note,
		OutboxEvents:          outboxEvents,
		ComplaintID:           in.ComplaintID,
		ComplaintStatus:       in.ComplaintStatus,
		ComplaintResolution:   in.ComplaintResolution,
		AdminNote:             in.AdminNote,
		ResolvedBy:            in.ResolvedBy,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	// Step 5: Side effects executed ONCE only when this call actually transitioned the order to REFUNDED.
	if !outcome.AlreadyProcessed && !outcome.AlreadyRefunded {
		compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()

		itemsToRestock := itemsToRestockForReturn(in.Order.Items, rr)
		s.restockOrderItems(compCtx, ord.ShopID, itemsToRestock)

		if allGroupClosed && ord.CheckoutGroupID != "" {
			s.compensateReleasePromotion(ctx, ord.CheckoutGroupID)
		}
		s.notifyOutbox()
	}

	return outcome, nil
}

func (s *OrderService) handleReverseShipmentReturned(
	ctx context.Context,
	eventID string,
	agg *store.OrderAggregate,
	payload events.ShipmentUpdatedPayload,
) error {
	var rr *db.ReturnRequest
	var err error
	if rrID := strings.TrimSpace(payload.ReturnRequestID); rrID != "" {
		rr, err = s.store.GetReturnRequestByID(ctx, rrID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	if rr == nil {
		rr, err = s.store.GetLatestReturnRequestByOrderID(ctx, agg.Order.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}

	shipNote := strings.TrimSpace(payload.Description)
	if shipNote == "" {
		shipNote = "Reverse shipment returned to seller"
	}

	_, err = s.executeReturnRefundSaga(ctx, refundSagaInput{
		EventID:               eventID,
		EventType:             events.SubjectShipmentUpdated,
		Order:                 agg,
		ReturnRequest:         rr,
		Reason:                shipNote,
		IntermediateStatus:    orderv1.OrderStatus_ORDER_STATUS_RETURNED,
		IntermediateActorID:   "shipping-service",
		IntermediateActorRole: "system",
		IntermediateNote:      shipNote,
		ActorID:               "order-service",
		ActorRole:             "system",
		Note:                  "Refund saga completed after reverse shipment returned",
	})
	return err
}

func (s *OrderService) createReturnShipmentForOrder(
	ctx context.Context,
	agg *store.OrderAggregate,
	rr *db.ReturnRequest,
	reqCarrierCode, reqServiceCode string,
) (*shippingv1.Shipment, error) {
	if s.clients.Shipping == nil {
		return nil, status.Error(codes.FailedPrecondition, "shipping client is not configured")
	}

	var buyerSnapshot orderv1.OrderAddressSnapshot
	_ = json.Unmarshal(agg.Order.ShippingAddressJSON, &buyerSnapshot)
	fromBuyerAddr := &shippingv1.ShipmentAddress{
		Name:          buyerSnapshot.GetRecipientName(),
		Phone:         buyerSnapshot.GetRecipientPhone(),
		ProvinceCode:  buyerSnapshot.GetProvinceCode(),
		WardCode:      buyerSnapshot.GetWardCode(),
		StreetAddress: buyerSnapshot.GetStreetAddress(),
	}
	toShopAddr := s.resolveShopPickupAddress(ctx, agg.Order.ShopID, fromBuyerAddr)

	carrierCode := fallbackStr(reqCarrierCode, agg.Order.CarrierCode)
	serviceCode := fallbackStr(reqServiceCode, agg.Order.ServiceCode)

	returnedItems := itemsToRestockForReturn(agg.Items, rr)
	var totalWeight int32
	summaryParts := make([]string, 0, len(returnedItems))
	for _, it := range returnedItems {
		w := it.WeightGrams
		if w <= 0 {
			w = 500
		}
		totalWeight += w * it.Quantity
		summaryParts = append(summaryParts, fmt.Sprintf("%s x%d", fallbackStr(it.ProductName, it.SKUID), it.Quantity))
	}
	if totalWeight <= 0 {
		totalWeight = 500
	}

	shipResp, err := s.clients.Shipping.CreateReturnShipment(ctx, &shippingv1.CreateReturnShipmentRequest{
		OrderId:         agg.Order.ID,
		ReturnRequestId: rr.ID,
		ShopId:          agg.Order.ShopID,
		CarrierCode:     carrierCode,
		ServiceCode:     serviceCode,
		WeightGrams:     totalWeight,
		FromAddress:     fromBuyerAddr,
		ToAddress:       toShopAddr,
		ItemsSummary:    strings.Join(summaryParts, ", "),
	})
	if err != nil {
		return nil, err
	}
	return shipResp.GetShipment(), nil
}

func (s *OrderService) areAllSiblingOrdersClosed(ctx context.Context, checkoutGroupID, excludeOrderID string) bool {
	if strings.TrimSpace(checkoutGroupID) == "" {
		return true
	}
	siblings, err := s.store.ListOrdersByCheckoutGroup(ctx, checkoutGroupID)
	if err != nil {
		return false
	}
	for _, sib := range siblings {
		if sib.Order.ID == excludeOrderID {
			continue
		}
		st := statemachine.StringToStatus(sib.Order.Status)
		if st != orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
			st != orderv1.OrderStatus_ORDER_STATUS_REFUNDED &&
			st != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			return false
		}
	}
	return true
}

func resolveReturnItemsAndAmount(agg *store.OrderAggregate, reqItems []*orderv1.ReturnItemInput) ([]*orderv1.ReturnItemInput, int64, error) {
	if len(reqItems) == 0 {
		full := make([]*orderv1.ReturnItemInput, 0, len(agg.Items))
		for _, it := range agg.Items {
			full = append(full, &orderv1.ReturnItemInput{
				OrderItemId: it.ID,
				SkuId:       it.SKUID,
				Quantity:    it.Quantity,
			})
		}
		return full, agg.Order.TotalPayableVND, nil
	}

	byItemID := make(map[string]db.OrderItem, len(agg.Items))
	bySKUID := make(map[string]db.OrderItem, len(agg.Items))
	for _, it := range agg.Items {
		byItemID[it.ID] = it
		bySKUID[it.SKUID] = it
	}

	requestedQtyByItemID := make(map[string]int32, len(reqItems))
	for _, ri := range reqItems {
		if ri == nil {
			return nil, 0, status.Error(codes.InvalidArgument, "return item cannot be nil")
		}
		if ri.GetQuantity() <= 0 {
			return nil, 0, status.Error(codes.InvalidArgument, "return item quantity must be positive")
		}

		var ordItem db.OrderItem
		var found bool
		itemID := strings.TrimSpace(ri.GetOrderItemId())
		skuID := strings.TrimSpace(ri.GetSkuId())

		if itemID != "" {
			ordItem, found = byItemID[itemID]
			if !found {
				return nil, 0, status.Errorf(codes.InvalidArgument, "order_item_id %s does not belong to order %s", itemID, agg.Order.ID)
			}
			if skuID != "" && skuID != ordItem.SKUID {
				return nil, 0, status.Errorf(codes.InvalidArgument, "sku_id %s does not match order_item_id %s", skuID, itemID)
			}
		} else if skuID != "" {
			ordItem, found = bySKUID[skuID]
			if !found {
				return nil, 0, status.Errorf(codes.InvalidArgument, "sku_id %s does not belong to order %s", skuID, agg.Order.ID)
			}
		} else {
			return nil, 0, status.Error(codes.InvalidArgument, "each return item must specify order_item_id or sku_id")
		}

		requestedQtyByItemID[ordItem.ID] += ri.GetQuantity()
		if requestedQtyByItemID[ordItem.ID] > ordItem.Quantity {
			return nil, 0, status.Errorf(
				codes.InvalidArgument,
				"return quantity %d for SKU %s exceeds ordered quantity %d",
				requestedQtyByItemID[ordItem.ID],
				ordItem.SKUID,
				ordItem.Quantity,
			)
		}
	}

	normalized := make([]*orderv1.ReturnItemInput, 0, len(requestedQtyByItemID))
	isFullOrder := len(agg.Items) > 0
	var returnedValue int64
	var totalOrderValue int64

	for _, it := range agg.Items {
		unitPrice := it.EffectiveUnitPriceVND
		if unitPrice <= 0 && it.Quantity > 0 {
			unitPrice = it.LineTotalVND / int64(it.Quantity)
		}
		totalOrderValue += unitPrice * int64(it.Quantity)

		qty := requestedQtyByItemID[it.ID]
		if qty != it.Quantity {
			isFullOrder = false
		}
		if qty > 0 {
			normalized = append(normalized, &orderv1.ReturnItemInput{
				OrderItemId: it.ID,
				SkuId:       it.SKUID,
				Quantity:    qty,
			})
			returnedValue += unitPrice * int64(qty)
		}
	}

	totalPayable := agg.Order.TotalPayableVND
	if totalPayable <= 0 {
		return normalized, 0, nil
	}
	if isFullOrder || totalOrderValue <= 0 || returnedValue >= totalOrderValue {
		return normalized, totalPayable, nil
	}

	// Exact overflow-safe integer VND proportional calculation capped at TotalPayableVND.
	num := new(big.Int).Mul(big.NewInt(totalPayable), big.NewInt(returnedValue))
	quot := new(big.Int).Quo(num, big.NewInt(totalOrderValue)).Int64()
	if quot > totalPayable {
		quot = totalPayable
	}
	if quot < 0 {
		quot = 0
	}
	return normalized, quot, nil
}

func effectiveRefundAmount(ord db.Order, rr *db.ReturnRequest) int64 {
	if rr != nil && rr.RefundAmountVND > 0 {
		if ord.TotalPayableVND > 0 && rr.RefundAmountVND > ord.TotalPayableVND {
			return ord.TotalPayableVND
		}
		return rr.RefundAmountVND
	}
	return ord.TotalPayableVND
}

func itemsToRestockForReturn(orderItems []db.OrderItem, rr *db.ReturnRequest) []db.OrderItem {
	if rr == nil || len(rr.ItemsJSON) <= 2 {
		return orderItems
	}
	var retInputs []*orderv1.ReturnItemInput
	if err := json.Unmarshal(rr.ItemsJSON, &retInputs); err != nil || len(retInputs) == 0 {
		return orderItems
	}

	byItemID := make(map[string]db.OrderItem, len(orderItems))
	bySKUID := make(map[string]db.OrderItem, len(orderItems))
	for _, it := range orderItems {
		byItemID[it.ID] = it
		bySKUID[it.SKUID] = it
	}

	out := make([]db.OrderItem, 0, len(retInputs))
	for _, ri := range retInputs {
		if ri == nil || ri.GetQuantity() <= 0 {
			continue
		}
		var base db.OrderItem
		if it, ok := byItemID[ri.GetOrderItemId()]; ok {
			base = it
		} else if it, ok := bySKUID[ri.GetSkuId()]; ok {
			base = it
		} else {
			base = db.OrderItem{SKUID: ri.GetSkuId()}
		}
		base.Quantity = ri.GetQuantity()
		out = append(out, base)
	}
	if len(out) == 0 {
		return orderItems
	}
	return out
}

func normalizePagination(page, pageSize int32) (int32, int32, int32) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize, (page - 1) * pageSize
}

func returnRequestToProto(rr db.ReturnRequest) *orderv1.ReturnRequest {
	var items []*orderv1.ReturnItemInput
	if len(rr.ItemsJSON) > 0 {
		_ = json.Unmarshal(rr.ItemsJSON, &items)
	}
	var photos []string
	if len(rr.EvidenceURLsJSON) > 0 {
		_ = json.Unmarshal(rr.EvidenceURLsJSON, &photos)
	}
	return &orderv1.ReturnRequest{
		Id:                  rr.ID,
		OrderId:             rr.OrderID,
		BuyerId:             rr.BuyerID,
		ShopId:              rr.ShopID,
		Items:               items,
		Reason:              rr.Reason,
		Photos:              photos,
		Status:              statemachine.StringToReturnRequestStatus(rr.Status),
		SellerNote:          rr.SellerNote,
		ReverseTrackingCode: rr.ReverseTrackingCode,
		ReverseLabelUrl:     rr.ReverseLabelURL,
		RefundAmountVnd:     rr.RefundAmountVND,
		RefundId:            rr.RefundID,
		CreatedAt:           timestamppb.New(rr.CreatedAt),
		UpdatedAt:           timestamppb.New(rr.UpdatedAt),
	}
}

func complaintToProto(cp db.Complaint) *orderv1.Complaint {
	var evidence []string
	if len(cp.EvidenceURLsJSON) > 0 {
		_ = json.Unmarshal(cp.EvidenceURLsJSON, &evidence)
	}
	out := &orderv1.Complaint{
		Id:              cp.ID,
		ReturnRequestId: cp.ReturnRequestID,
		OrderId:         cp.OrderID,
		BuyerId:         cp.BuyerID,
		ShopId:          cp.ShopID,
		Reason:          cp.Reason,
		EvidenceUrls:    evidence,
		Status:          statemachine.StringToComplaintStatus(cp.Status),
		Resolution:      statemachine.StringToComplaintResolution(cp.Resolution),
		AdminNote:       cp.AdminNote,
		ResolvedBy:      cp.ResolvedBy,
		CreatedAt:       timestamppb.New(cp.CreatedAt),
	}
	if cp.ResolvedAt != nil {
		out.ResolvedAt = timestamppb.New(*cp.ResolvedAt)
	}
	return out
}
