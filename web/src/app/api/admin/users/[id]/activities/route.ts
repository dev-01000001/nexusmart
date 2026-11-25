import type { GatewayUserActivitiesResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  ALLOWED_ACTIVITY_CATEGORIES,
  parseEntityId,
  parseOptionalPositiveInt,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

function parseOptionalIso(raw: string | null): string | undefined {
  if (!raw) return undefined;
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  const ms = Date.parse(trimmed);
  if (Number.isNaN(ms)) {
    throw new GatewayError("invalid", "INVALID_TIMESTAMP");
  }
  return new Date(ms).toISOString();
}

export const GET = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("users.read");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_USER_ID");
    }

    const params = req.nextUrl.searchParams;
    const rawCategory = (params.get("category") ?? "").trim().toLowerCase();
    let category: string | undefined;
    if (rawCategory) {
      if (!(ALLOWED_ACTIVITY_CATEGORIES as readonly string[]).includes(rawCategory)) {
        throw new GatewayError("invalid", "INVALID_CATEGORY");
      }
      category = rawCategory;
    }

    const data = await gw<GatewayUserActivitiesResponse>(
      `/api/v1/admin/users/${encodeURIComponent(cleanId)}/activities`,
      {
        query: {
          from_time: parseOptionalIso(params.get("from_time")),
          to_time: parseOptionalIso(params.get("to_time")),
          category,
          page: parseOptionalPositiveInt(params.get("page"), 10000),
          page_size: parseOptionalPositiveInt(params.get("page_size"), 100),
        },
      }
    );
    return Response.json(
      {
        items: data.items ?? [],
        cod_rejected_count: data.cod_rejected_count ?? 0,
        recent_orders: data.recent_orders ?? [],
        page: data.page ?? 1,
        page_size: data.page_size ?? 20,
        total: data.total ?? 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_USER_ID: "Mã người dùng không hợp lệ.",
      INVALID_CATEGORY: "Nhóm hoạt động không hợp lệ.",
      INVALID_TIMESTAMP: "Khoảng thời gian lọc không hợp lệ.",
    },
  }
);
