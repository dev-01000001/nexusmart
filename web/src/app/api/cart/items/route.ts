import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { assertSkuOfShop, requireRequestSite } from "@/lib/server/sites";
import { scopeCartToShop } from "@/lib/server/storefront";
import { mapCartError } from "@/features/commerce/checkoutErrors";
import type { GwCart } from "@/features/commerce/types";

interface AddCartBody {
  sku_id?: string;
  /** Optional lookup hint; ownership is still verified against the backend product. */
  product_id?: string;
  quantity?: number;
}

export const POST = apiRoute(
  async (req) => {
    const site = await requireRequestSite(req);
    await assertSession();
    const body = await readJson<AddCartBody>(req);
    const skuId = (typeof body.sku_id === "string" ? body.sku_id : "").trim();
    const quantity = body.quantity === undefined ? 1 : Math.trunc(Number(body.quantity));

    if (!skuId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }
    if (!Number.isFinite(quantity) || quantity <= 0 || quantity > 999) {
      throw new GatewayError("invalid", "INVALID_QUANTITY");
    }
    // A SKU of another shop is "not found" on this site.
    await assertSkuOfShop(site.shopId, skuId, body.product_id);

    try {
      const data = await gw<{ cart: GwCart; clamped_to_stock: boolean }>("/api/v1/cart/items", {
        method: "POST",
        body: { sku_id: skuId, quantity },
      });
      return Response.json(
        { cart: scopeCartToShop(data.cart, site.shopId), clamped_to_stock: data.clamped_to_stock },
        { headers: { "Cache-Control": "no-store" } }
      );
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
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng chọn đúng phân loại sản phẩm trước khi thêm vào giỏ.",
      INVALID_QUANTITY: "Số lượng sản phẩm cần lớn hơn 0.",
      not_found: "Sản phẩm này không thuộc cửa hàng bạn đang xem.",
    },
  }
);
