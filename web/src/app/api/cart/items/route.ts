import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { mapCartError } from "@/features/commerce/checkoutErrors";
import type { GwCart } from "@/features/commerce/types";

interface AddCartBody {
  sku_id?: string;
  skuId?: string;
  quantity?: number;
}

export const POST = apiRoute(
  async (req) => {
    await assertSession();
    const body = await readJson<AddCartBody>(req);
    const skuId = (body.sku_id ?? body.skuId ?? "").trim();
    const quantity = body.quantity === undefined ? 1 : Math.trunc(Number(body.quantity));

    if (!skuId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }
    if (!Number.isFinite(quantity) || quantity <= 0 || quantity > 999) {
      throw new GatewayError("invalid", "INVALID_QUANTITY");
    }

    try {
      const data = await gw<{ cart: GwCart; clamped_to_stock: boolean }>("/api/v1/cart/items", {
        method: "POST",
        body: { sku_id: skuId, quantity },
      });
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
  },
  {
    messages: {
      INVALID_ARGUMENT: "Vui lòng chọn đúng phân loại sản phẩm trước khi thêm vào giỏ.",
      INVALID_QUANTITY: "Số lượng sản phẩm cần lớn hơn 0.",
    },
  }
);
