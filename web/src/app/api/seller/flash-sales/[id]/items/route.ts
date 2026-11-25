import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import type { GwFlashSaleItem } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface RegisterFlashBody {
  sku_id?: unknown;
  flash_price_vnd?: unknown;
  total_slots?: unknown;
  per_user_limit?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const flashSaleId = assertValidId(id, "INVALID_FLASH_SALE_ID");

    const raw = await readJson<RegisterFlashBody>(req);
    const skuId = assertValidId(
      typeof raw?.sku_id === "string" ? raw.sku_id : "",
      "INVALID_FLASH_ITEM"
    );
    const flashPrice = Math.trunc(Number(raw?.flash_price_vnd) || 0);
    const totalSlots = Math.trunc(Number(raw?.total_slots) || 0);
    const perUserLimit = Math.max(1, Math.trunc(Number(raw?.per_user_limit) || 1));

    if (flashPrice <= 0 || totalSlots <= 0) {
      throw new GatewayError("invalid", "INVALID_FLASH_ITEM");
    }

    const data = await gw<{ item: GwFlashSaleItem }>(
      `/api/v1/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`,
      {
        method: "POST",
        body: {
          sku_id: skuId,
          flash_price_vnd: flashPrice,
          total_slots: totalSlots,
          per_user_limit: perUserLimit,
        },
      }
    );
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_FLASH_SALE_ID: "Mã khung giờ Flash sale không hợp lệ.",
      INVALID_FLASH_ITEM: "Vui lòng chọn biến thể SKU, giá ưu đãi và số suất hợp lệ.",
      ALREADY_EXISTS: "Biến thể SKU này đã đăng ký trong khung giờ Flash sale trùng lịch.",
    },
  }
);
