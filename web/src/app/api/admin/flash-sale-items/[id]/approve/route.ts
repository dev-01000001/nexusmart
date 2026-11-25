import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";
import type { GwAdminFlashSaleItem } from "@/features/admin/adminApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ApproveFlashItemBody {
  approve?: boolean;
  reason?: string;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("flash_sales.items.approve");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_FLASH_ITEM_ID");
    }
    const raw = await readJson<ApproveFlashItemBody>(req);
    const data = await gw<{ item: GwAdminFlashSaleItem }>(
      `/api/v1/admin/flash-sale-items/${encodeURIComponent(cleanId)}/approve`,
      {
        method: "POST",
        body: {
          approve: Boolean(raw.approve),
          reason: typeof raw.reason === "string" ? raw.reason.trim() : "",
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_FLASH_ITEM_ID: "Mã sản phẩm Flash sale không hợp lệ.",
    },
  }
);
