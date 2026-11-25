import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import type { GwOrder } from "@/features/account/orderView";
import type { UpdateOrderAddressRequest, UpdateOrderAddressResponse } from "@/lib/a8-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const PATCH = apiRoute<RouteCtx>(async (req, { params }) => {
  const site = await requireRequestSite(req, { fresh: true });
  const session = await assertSession();
  const { id } = await params;
  const cleanId = id.trim();
  if (!cleanId) throw new GatewayError("not_found");

  const body = await readJson<Partial<UpdateOrderAddressRequest>>(req);
  const addressId = typeof body?.address_id === "string" ? body.address_id.trim() : "";
  if (!addressId) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const existing = await gw<{ order?: GwOrder | null }>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}`
  );
  const scoped = assertInStorefront(existing.order, site.shopId);
  assertBuyerOrderOwnership(scoped, session);

  const idempotencyKey =
    (req.headers.get("idempotency-key") ?? "").trim() ||
    (typeof body.idempotency_key === "string" ? body.idempotency_key.trim() : "") ||
    undefined;

  const data = await gw<UpdateOrderAddressResponse>(
    `/api/v1/orders/${encodeURIComponent(cleanId)}/address`,
    {
      method: "PATCH",
      idempotencyKey,
      body: {
        address_id: addressId,
        preview_only: Boolean(body.preview_only),
        accept_fee_increase: Boolean(body.accept_fee_increase),
        expected_new_shipping_fee_vnd: Math.max(
          0,
          Math.trunc(Number(body.expected_new_shipping_fee_vnd ?? 0) || 0)
        ),
        idempotency_key: idempotencyKey ?? "",
      },
    }
  );

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
