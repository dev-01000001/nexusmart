import type { GatewayCategoryFeeSchedule } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parseEntityId,
  validateCategoryFeeScheduleInput,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("fees.category.write");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_CATEGORY_ID");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateCategoryFeeScheduleInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<{ schedule: GatewayCategoryFeeSchedule }>(
      `/api/v1/admin/fees/categories/${encodeURIComponent(cleanId)}`,
      {
        method: "POST",
        body: {
          commission_rate_bps: parsed.value.commission_rate_bps,
          effective_from: parsed.value.effective_from,
          note: parsed.value.note,
        },
      }
    );
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CATEGORY_ID: "Mã danh mục không hợp lệ.",
    },
  }
);
