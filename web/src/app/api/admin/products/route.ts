import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAdminProductExtended } from "@/features/admin/adminApiTypes";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("products.read");
    const q = req.nextUrl.searchParams;
    const categoryId = (q.get("category_id") ?? "").trim();
    const shopId = (q.get("shop_id") ?? "").trim();
    if (categoryId && !isValidAdminPathId(categoryId)) {
      throw new GatewayError("invalid", "INVALID_FILTER");
    }
    if (shopId && !isValidAdminPathId(shopId)) {
      throw new GatewayError("invalid", "INVALID_FILTER");
    }

    const data = await gw<{
      items: GwAdminProductExtended[];
      page: number;
      page_size: number;
      total: number;
    }>("/api/v1/admin/products", {
      query: {
        status: (q.get("status") ?? "").trim() || undefined,
        keyword: (q.get("keyword") ?? "").trim().slice(0, 120) || undefined,
        category_id: categoryId || undefined,
        shop_id: shopId || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_FILTER: "Bộ lọc tìm kiếm sản phẩm không hợp lệ.",
    },
  }
);
