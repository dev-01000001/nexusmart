import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { mapCartError } from "@/features/commerce/checkoutErrors";
import type { GwCart } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ skuId: string }> };

interface UpdateQtyBody {
  quantity?: number;
}

async function handleUpdateQty(req: Request, { params }: RouteCtx): Promise<Response> {
  await assertSession();
  const { skuId } = await params;
  const cleanSkuId = skuId.trim();
  if (!cleanSkuId) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const body = await readJson<UpdateQtyBody>(req);
  const quantity = Math.trunc(Number(body.quantity));
  if (!Number.isFinite(quantity) || quantity < 0 || quantity > 999) {
    throw new GatewayError("invalid", "INVALID_QUANTITY");
  }

  try {
    const data = await gw<{ cart: GwCart; clamped_to_stock: boolean }>(
      `/api/v1/cart/items/${encodeURIComponent(cleanSkuId)}`,
      {
        method: "PATCH",
        body: { quantity },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "conflict" || err.kind === "not_found")) {
      const mapped = mapCartError(err.code, err.detail);
      return Response.json(
        { error: { code: mapped.code, message: mapped.message } },
        { status: mapped.status ?? err.status, headers: { "Cache-Control": "no-store" } }
      );
    }
    throw err;
  }
}

export const PATCH = apiRoute<RouteCtx>(handleUpdateQty, {
  messages: {
    INVALID_QUANTITY: "Số lượng sản phẩm không hợp lệ.",
  },
});

export const PUT = apiRoute<RouteCtx>(handleUpdateQty, {
  messages: {
    INVALID_QUANTITY: "Số lượng sản phẩm không hợp lệ.",
  },
});

export const DELETE = apiRoute<RouteCtx>(async (_req, { params }) => {
  await assertSession();
  const { skuId } = await params;
  const cleanSkuId = skuId.trim();
  if (!cleanSkuId) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const data = await gw<{ cart: GwCart }>(`/api/v1/cart/items/${encodeURIComponent(cleanSkuId)}`, {
    method: "DELETE",
  });
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
