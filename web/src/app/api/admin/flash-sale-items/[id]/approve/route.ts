import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";
import { validateApproveFlashSaleItemInput } from "@/features/admin/AdminFlashSaleHelpers";
import type { GwAdminFlashSaleItem } from "@/features/admin/adminApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("flash_sales.items.approve");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_FLASH_ITEM_ID");
    }
    const raw = await readJson<unknown>(req);
    const validated = validateApproveFlashSaleItemInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }
    const data = await gw<{ item: GwAdminFlashSaleItem }>(
      `/api/v1/admin/flash-sale-items/${encodeURIComponent(cleanId)}/approve`,
      {
        method: "POST",
        body: {
          approve: validated.value.approve,
          reason: validated.value.reason,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_FLASH_ITEM_ID: "Mã sản phẩm Flash sale không hợp lệ.",
      INVALID_ARGUMENT: "Quyết định duyệt sản phẩm Flash sale phải là giá trị đúng/sai hợp lệ.",
    },
  }
);
