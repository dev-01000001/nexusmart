import type { GatewayCategoryFeeSchedule, PaginatedResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId, parseOptionalPositiveInt } from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("fees.category.read");
    const params = req.nextUrl.searchParams;
    const rawCatId = (params.get("category_id") ?? "").trim();
    let categoryId: string | undefined;
    if (rawCatId) {
      const valid = parseEntityId(rawCatId);
      if (!valid) throw new GatewayError("invalid", "INVALID_CATEGORY_ID");
      categoryId = valid;
    }

    const data = await gw<PaginatedResponse<GatewayCategoryFeeSchedule>>(
      "/api/v1/admin/fees/categories",
      {
        query: {
          category_id: categoryId,
          page: parseOptionalPositiveInt(params.get("page"), 10000),
          page_size: parseOptionalPositiveInt(params.get("page_size"), 100),
        },
      }
    );
    return Response.json(
      {
        items: data.items ?? [],
        page: data.page ?? 1,
        page_size: data.page_size ?? 20,
        total: data.total ?? 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_CATEGORY_ID: "Mã danh mục không hợp lệ.",
    },
  }
);
