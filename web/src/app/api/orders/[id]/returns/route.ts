import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import { mapGatewayOrder, type GwOrder, type GwOrderItem } from "@/features/account/orderView";
import {
  normalizeReturnRequest,
  normalizeReturnRequests,
  validateReturnPayload,
  type ReturnRequestItem,
  type ReturnRequestView,
} from "@/features/account/returnsTypes";

type Params = { params: Promise<{ id: string }> };

const RETURN_ERROR_MESSAGES: Record<string, string> = {
  INVALID_PAYLOAD: "Dữ liệu yêu cầu đổi trả chưa hợp lệ.",
  INVALID_REASON: "Vui lòng nhập lý do đổi trả rõ ràng (từ 5 đến 500 ký tự).",
  INVALID_RETURN_ITEMS: "Vui lòng chọn ít nhất một sản phẩm thuộc đơn hàng này để đổi trả.",
  INVALID_RETURN_QTY: "Số lượng sản phẩm đổi trả vượt quá số lượng đã đặt hoặc không hợp lệ.",
  TOO_MANY_PHOTOS: "Chỉ được đính kèm tối đa 5 ảnh minh chứng.",
  INVALID_PHOTO_URL:
    "Đường dẫn ảnh minh chứng không hợp lệ. Chỉ chấp nhận ảnh nội bộ (/...) hoặc liên kết https://.",
  not_found: "Không tìm thấy đơn hàng này.",
  forbidden: "Bạn không có quyền truy cập yêu cầu đổi trả của đơn hàng này.",
  FAILED_PRECONDITION: "Đơn hàng hiện chưa đủ điều kiện hoặc đã quá thời hạn yêu cầu đổi trả.",
  conflict: "Không thể gửi yêu cầu đổi trả ở trạng thái hiện tại của đơn hàng.",
};

function resolveReturnItems(
  requested: ReturnRequestItem[],
  orderItems: GwOrderItem[] | null | undefined
): ReturnRequestItem[] {
  const existing = orderItems ?? [];
  if (existing.length === 0) return requested;

  const byOrderItemId = new Map(existing.map((it) => [it.id, it]));
  const bySkuId = new Map(existing.map((it) => [it.sku_id, it]));
  const resolved: ReturnRequestItem[] = [];

  for (const reqItem of requested) {
    const matched =
      (reqItem.order_item_id ? byOrderItemId.get(reqItem.order_item_id) : undefined) ??
      (reqItem.sku_id ? bySkuId.get(reqItem.sku_id) : undefined);

    if (!matched) {
      throw new GatewayError("invalid", "INVALID_RETURN_ITEMS");
    }
    if (reqItem.quantity > matched.quantity) {
      throw new GatewayError("invalid", "INVALID_RETURN_QTY");
    }
    resolved.push({
      order_item_id: matched.id,
      sku_id: matched.sku_id,
      quantity: reqItem.quantity,
    });
  }

  return resolved;
}

export const GET = apiRoute<Params>(
  async (req, ctx) => {
    const site = await requireRequestSite(req);
    const session = await assertSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    const scoped = assertInStorefront(existing.order, site.shopId);
    assertBuyerOrderOwnership(scoped, session);

    const raw = await gw<unknown>(`/api/v1/orders/${encodeURIComponent(cleanId)}/returns`);
    const returns = normalizeReturnRequests(raw);

    return Response.json(
      { returns },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  { messages: RETURN_ERROR_MESSAGES }
);

export const POST = apiRoute<Params>(
  async (req, ctx) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const rawBody = await readJson<unknown>(req);
    const validation = validateReturnPayload(rawBody);
    if (!validation.ok) {
      throw new GatewayError("invalid", validation.code);
    }

    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    const scoped = assertInStorefront(existing.order, site.shopId);
    assertBuyerOrderOwnership(scoped, session);

    const resolvedItems = resolveReturnItems(validation.data.items, scoped.items);

    const res = await gw<{
      return_request?: unknown;
      return_request_id?: string;
      order?: GwOrder | null;
    }>(`/api/v1/orders/${encodeURIComponent(cleanId)}/returns`, {
      method: "POST",
      body: {
        items: resolvedItems,
        reason: validation.data.reason,
        photos: validation.data.photos,
      },
    });

    const normalizedReturn: ReturnRequestView | null =
      normalizeReturnRequest(res.return_request) ??
      (res.return_request_id
        ? {
            id: res.return_request_id,
            orderId: scoped.id,
            shopId: scoped.shop_id,
            status: "PENDING",
            items: resolvedItems,
            reason: validation.data.reason,
            photos: validation.data.photos,
            sellerNote: "",
            reverseTrackingCode: "",
            reverseLabelUrl: "",
            refundAmountVnd: 0,
            refundId: "",
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
          }
        : null);

    let mappedOrder = null;
    if (res.order) {
      const catalog = await loadCatalogLookups();
      mappedOrder = mapGatewayOrder(res.order, catalog.lookups);
    }

    return Response.json(
      {
        return_request: normalizedReturn,
        order: mappedOrder,
      },
      {
        status: 201,
        headers: { "Cache-Control": "no-store" },
      }
    );
  },
  { messages: RETURN_ERROR_MESSAGES }
);
