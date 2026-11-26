import type { PaginatedResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAdminFlashSaleItemExtended } from "@/features/admin/adminApiTypes";
import { validateFlashSaleItemStatusFilter } from "@/features/admin/AdminFlashSaleHelpers";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("flash_sales.read");
    const q = req.nextUrl.searchParams;

    const status = validateFlashSaleItemStatusFilter(q.get("status"));
    if (status === null) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }

    const flashSaleId = (q.get("flash_sale_id") ?? "").trim();
    if (flashSaleId && !isValidAdminPathId(flashSaleId)) {
      throw new GatewayError("invalid", "INVALID_FLASH_SALE_ID");
    }

    const shopId = (q.get("shop_id") ?? "").trim();
    if (shopId && !isValidAdminPathId(shopId)) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    const data = await gw<PaginatedResponse<GwAdminFlashSaleItemExtended>>(
      "/api/v1/admin/flash-sale-items",
      {
        query: {
          status: status || undefined,
          flash_sale_id: flashSaleId || undefined,
          shop_id: shopId || undefined,
          page: q.get("page"),
          page_size: q.get("page_size"),
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STATUS: "Trạng thái suất Flash sale không hợp lệ.",
      INVALID_FLASH_SALE_ID: "Mã khung giờ Flash sale không hợp lệ.",
      INVALID_SHOP_ID: "Mã gian hàng không hợp lệ.",
    },
  }
);
