import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite, type RequestSite } from "@/lib/server/sites";
import { cartHasSku, scopeCartToShop } from "@/lib/server/storefront";
import { mapCartError } from "@/features/commerce/checkoutErrors";
import type { GwCart } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ skuId: string }> };

interface UpdateQtyBody {
  quantity?: number;
}

const NOT_IN_SITE_CART = "Sản phẩm này không có trong giỏ hàng của cửa hàng.";

/** The line must already be in THIS site's cart; lines of other sites are invisible (404). */
async function requireSiteLine(req: Request, params: RouteCtx["params"]) {
  const site = await requireRequestSite(req);
  await assertSession();
  const { skuId } = await params;
  const cleanSkuId = skuId.trim();
  if (!cleanSkuId) throw new GatewayError("invalid", "INVALID_ARGUMENT");
  const { cart } = await gw<{ cart: GwCart }>("/api/v1/cart", {
    query: { shop_id: site.shopId },
  });
  if (!cartHasSku(scopeCartToShop(cart, site.shopId), cleanSkuId)) {
    throw new GatewayError("not_found");
  }
  return { site, skuId: cleanSkuId };
}

function cartErrorResponse(err: unknown): Response {
  if (err instanceof GatewayError && (err.kind === "conflict" || err.kind === "not_found")) {
    const mapped = mapCartError(err.code, err.detail);
    return Response.json(
      { error: { code: mapped.code, message: mapped.message } },
      { status: mapped.status ?? err.status, headers: { "Cache-Control": "no-store" } }
    );
  }
  throw err;
}

function scoped(site: RequestSite, data: { cart: GwCart; clamped_to_stock?: boolean }): Response {
  return Response.json(
    { ...data, cart: scopeCartToShop(data.cart, site.shopId) },
    { headers: { "Cache-Control": "no-store" } }
  );
}

async function handleUpdateQty(req: Request, { params }: RouteCtx): Promise<Response> {
  const { site, skuId } = await requireSiteLine(req, params);
  const body = await readJson<UpdateQtyBody>(req);
  const quantity = Math.trunc(Number(body.quantity));
  if (!Number.isFinite(quantity) || quantity < 0 || quantity > 999) {
    throw new GatewayError("invalid", "INVALID_QUANTITY");
  }
  try {
    const data = await gw<{ cart: GwCart; clamped_to_stock: boolean }>(
      `/api/v1/cart/items/${encodeURIComponent(skuId)}`,
      { method: "PATCH", body: { quantity } }
    );
    return scoped(site, data);
  } catch (err) {
    return cartErrorResponse(err);
  }
}

const messages = {
  INVALID_QUANTITY: "Số lượng sản phẩm không hợp lệ.",
  not_found: NOT_IN_SITE_CART,
};

export const PATCH = apiRoute<RouteCtx>(handleUpdateQty, { messages });
export const PUT = apiRoute<RouteCtx>(handleUpdateQty, { messages });

export const DELETE = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const { site, skuId } = await requireSiteLine(req, params);
    const data = await gw<{ cart: GwCart }>(`/api/v1/cart/items/${encodeURIComponent(skuId)}`, {
      method: "DELETE",
    });
    return scoped(site, data);
  },
  { messages }
);
