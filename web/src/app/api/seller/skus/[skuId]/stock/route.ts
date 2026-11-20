import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerSku } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ skuId: string }>;
}

interface UpdateStockBody {
  new_stock?: number;
  expected_version?: number;
}

export const PATCH = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { skuId } = await ctx.params;
    const raw = await readJson<UpdateStockBody>(req);
    const newStock = Math.trunc(Number(raw.new_stock));
    const expectedVersion = Math.trunc(Number(raw.expected_version));
    if (
      !Number.isFinite(newStock) ||
      newStock < 0 ||
      !Number.isFinite(expectedVersion) ||
      expectedVersion <= 0
    ) {
      throw new GatewayError("invalid", "INVALID_STOCK");
    }

    const data = await gw<{ sku: GwSellerSku }>(
      `/api/v1/seller/skus/${encodeURIComponent(skuId)}/stock`,
      {
        method: "PATCH",
        body: {
          new_stock: newStock,
          expected_version: expectedVersion,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STOCK: "Số lượng tồn kho phải là số nguyên không âm.",
      ABORTED: "Tồn kho SKU vừa thay đổi từ giao dịch khác. Vui lòng tải lại trang và thử lại.",
      CONFLICT: "Tồn kho SKU vừa thay đổi từ giao dịch khác. Vui lòng tải lại trang và thử lại.",
      FAILED_PRECONDITION: "Tồn kho mới không được nhỏ hơn số lượng đang giữ cho đơn hàng.",
    },
  }
);
