import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { MAX_BFF_BODY_BYTES, readBoundedBody } from "@/lib/server/proxy-security";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import { normalizeSellerOrder, type GwSellerOrder } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ShipBody {
  carrier_code?: unknown;
  service_code?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const orderId = assertValidId(id, "INVALID_ORDER_ID");

    const bounded = await readBoundedBody(req, MAX_BFF_BODY_BYTES);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }

    let body: ShipBody = {};
    if (bounded.body.byteLength > 0) {
      const text = new TextDecoder().decode(bounded.body).trim();
      if (text) {
        let parsed: unknown;
        try {
          parsed = JSON.parse(text);
        } catch {
          throw new GatewayError("invalid", "INVALID_JSON");
        }
        if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
          body = parsed as ShipBody;
        }
      }
    }

    const carrierCode =
      typeof body.carrier_code === "string" ? body.carrier_code.trim().slice(0, 64) : "";
    const serviceCode =
      typeof body.service_code === "string" ? body.service_code.trim().slice(0, 64) : "";

    const data = await gw<{
      order: GwSellerOrder;
      tracking_code?: string;
      label_url?: string;
      queued?: boolean;
      shipment_job_id?: string;
    }>(`/api/v1/seller/orders/${encodeURIComponent(orderId)}/ship`, {
      method: "POST",
      body: {
        carrier_code: carrierCode,
        service_code: serviceCode,
      },
    });
    return Response.json(
      {
        ...data,
        order: data.order ? normalizeSellerOrder(data.order) : data.order,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ORDER_ID: "Mã đơn hàng không hợp lệ.",
    },
  }
);
