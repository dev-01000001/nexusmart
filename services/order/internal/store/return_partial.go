package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nexusmart/core/services/order/internal/db"
)

// ErrReturnNeedsPromoRelease signals that a REFUNDING -> REFUNDED transition for a migrated
// partial return discovered (under FOR UPDATE) that cumulative refunds reached 100% of
// total_payable_vnd while promo_released is still false, so the caller must release shop
// promotions before finalizing the order as RETURNED.
var ErrReturnNeedsPromoRelease = errors.New("return requires promotion release before finalizing as returned")

// PaymentRefundTimelinePrefix returns the exact prefix RecordPaymentRefunded writes into
// order_timeline_events.note in the same transaction that increments orders.refunded_vnd.
func PaymentRefundTimelinePrefix(refundID string) string {
	refID := strings.TrimSpace(refundID)
	if refID == "" {
		return ""
	}
	return "Refund " + refID + " processed: "
}

// IsReturnRefundRecordedOnOrder reports whether RecordPaymentRefunded has already applied
// the refund identified by refundID into orders.refunded_vnd.
func IsReturnRefundRecordedOnOrder(timeline []db.OrderTimelineEvent, refundID string) bool {
	prefix := PaymentRefundTimelinePrefix(refundID)
	if prefix == "" {
		return false
	}
	for _, ev := range timeline {
		if ev.ActorID == "payment-service" && ev.ActorRole == "system" && strings.HasPrefix(ev.Note, prefix) {
			return true
		}
	}
	return false
}

// ComputeEffectiveReturnRefunded returns the cumulative refunded amount on ord once rr's own
// refund is accounted for:
//   - If RecordPaymentRefunded has already applied rr's own refund (detected by rr.RefundID in
//     order_timeline_events written atomically with UpdateOrderRefunded), returns ord.RefundedVND.
//   - Otherwise, returns min(ord.TotalPayableVND, ord.RefundedVND + rr.RefundAmountVND).
func ComputeEffectiveReturnRefunded(ord db.Order, timeline []db.OrderTimelineEvent, rr db.ReturnRequest) int64 {
	total := max64(0, ord.TotalPayableVND)
	recorded := max64(0, ord.RefundedVND)
	if IsReturnRefundRecordedOnOrder(timeline, rr.RefundID) {
		return min(total, recorded)
	}
	return min(total, recorded+max64(0, rr.RefundAmountVND))
}

// OrderFreeshipFloorVND returns the minimum shipping fee an order may be updated to on COD address
// updates so shop_freeship_discount_vnd + freeship_discount_vnd <= shipping_fee_vnd always holds
// and buyer payable never drops below post-discount merchandise.
func OrderFreeshipFloorVND(ord db.Order) int64 {
	oldShip := max64(0, ord.ShippingFeeVND)
	appliedFreeship := max64(0, ord.ShopFreeshipDiscountVND) + max64(0, ord.FreeshipDiscountVND)
	return min(oldShip, appliedFreeship)
}

type returnItemJSONSpec struct {
	OrderItemID      string `json:"order_item_id"`
	OrderItemIDCamel string `json:"orderItemId"`
	SKUID            string `json:"sku_id"`
	SKUIDCamel       string `json:"skuId"`
	Quantity         int32  `json:"quantity"`
}

// HasRawReturnItems reports whether itemsJSON contains a non-empty JSON payload (excluding empty/[]/null).
func HasRawReturnItems(itemsJSON []byte) bool {
	trimmed := bytes.TrimSpace(itemsJSON)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("[]")) && !bytes.Equal(trimmed, []byte("null"))
}

// SelectReturnItems parses a phase-0b return_requests.items_json snapshot against the order's items.
// For main full returns (empty items_json) or phase-0b full returns whose items_json covers every
// ordered item quantity, it returns (orderItems, false).
// For migrated phase-0b partial returns (non-empty items_json not covering the full order), it returns
// only the matched returned items with their returned quantities and isPartial=true.
func SelectReturnItems(orderItems []db.OrderItem, itemsJSON []byte) ([]db.OrderItem, bool) {
	if !HasRawReturnItems(itemsJSON) || len(orderItems) == 0 {
		return orderItems, false
	}

	var specs []returnItemJSONSpec
	if err := json.Unmarshal(itemsJSON, &specs); err != nil || len(specs) == 0 {
		return orderItems, false
	}

	byItemID := make(map[string]db.OrderItem, len(orderItems))
	bySKUID := make(map[string]db.OrderItem, len(orderItems))
	for _, it := range orderItems {
		byItemID[it.ID] = it
		bySKUID[it.SKUID] = it
	}

	qtyByItemID := make(map[string]int32, len(specs))
	for _, sp := range specs {
		if sp.Quantity <= 0 {
			continue
		}
		itemID := strings.TrimSpace(sp.OrderItemID)
		if itemID == "" {
			itemID = strings.TrimSpace(sp.OrderItemIDCamel)
		}
		skuID := strings.TrimSpace(sp.SKUID)
		if skuID == "" {
			skuID = strings.TrimSpace(sp.SKUIDCamel)
		}

		var matched db.OrderItem
		var ok bool
		if itemID != "" {
			matched, ok = byItemID[itemID]
		}
		if !ok && skuID != "" {
			matched, ok = bySKUID[skuID]
		}
		if !ok {
			continue
		}
		nextQty := qtyByItemID[matched.ID] + sp.Quantity
		if nextQty > matched.Quantity {
			nextQty = matched.Quantity
		}
		qtyByItemID[matched.ID] = nextQty
	}

	if len(qtyByItemID) == 0 {
		return orderItems, false
	}

	selected := make([]db.OrderItem, 0, len(qtyByItemID))
	coversAll := true
	for _, it := range orderItems {
		if it.Quantity <= 0 {
			continue
		}
		q := qtyByItemID[it.ID]
		if q < it.Quantity {
			coversAll = false
		}
		if q > 0 {
			cp := it
			cp.Quantity = q
			selected = append(selected, cp)
		}
	}
	if len(selected) == 0 || coversAll {
		return orderItems, false
	}
	return selected, true
}

// ResolveReturnRefundTarget returns the refund amount to issue for a return request.
// Full returns (isPartial=false) always refund the remaining unrefunded balance (TotalPayableVND - RefundedVND).
// Partial returns (isPartial=true) refund min(max(0, rr.RefundAmountVND), remaining).
func ResolveReturnRefundTarget(ord db.Order, rr db.ReturnRequest, isPartial bool) int64 {
	rem := max64(0, ord.TotalPayableVND-ord.RefundedVND)
	if !isPartial {
		return rem
	}
	target := max64(0, rr.RefundAmountVND)
	if target > rem {
		target = rem
	}
	return target
}
