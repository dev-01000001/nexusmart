import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayBannedKeyword } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

const ALLOWED_ACTIONS = new Set(["BLOCK", "REVIEW"]);
const ALLOWED_CATEGORIES = new Set(["COUNTERFEIT", "PROHIBITED", "OFF_PLATFORM", "SPAM", "OTHER"]);

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface UpdateBannedKeywordBody {
  term?: unknown;
  category?: unknown;
  action?: unknown;
  is_active?: unknown;
}

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("banned_keywords.write");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const raw = await readJson<UpdateBannedKeywordBody>(req);
    const term = typeof raw.term === "string" ? raw.term.trim() : "";
    const category = typeof raw.category === "string" ? raw.category.trim().toUpperCase() : "OTHER";
    const action = typeof raw.action === "string" ? raw.action.trim().toUpperCase() : "";

    if (!term || term.length > 120) {
      throw new GatewayError("invalid", "INVALID_TERM");
    }
    if (!ALLOWED_ACTIONS.has(action)) {
      throw new GatewayError("invalid", "INVALID_ACTION");
    }
    if (category && !ALLOWED_CATEGORIES.has(category)) {
      throw new GatewayError("invalid", "INVALID_CATEGORY");
    }
    if (typeof raw.is_active !== "boolean") {
      throw new GatewayError("invalid", "INVALID_ACTIVE_FLAG");
    }

    const data = await gw<{ keyword: GatewayBannedKeyword }>(
      `/api/v1/admin/banned-keywords/${encodeURIComponent(id.trim())}`,
      {
        method: "PUT",
        body: {
          term,
          category: category || "OTHER",
          action,
          is_active: raw.is_active,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã từ khoá cấm không hợp lệ.",
      INVALID_TERM: "Cụm từ cấm không được để trống và tối đa 120 ký tự.",
      INVALID_ACTION: "Hành động áp dụng chỉ hỗ trợ BLOCK hoặc REVIEW.",
      INVALID_CATEGORY: "Nhóm vi phạm không hợp lệ.",
      INVALID_ACTIVE_FLAG: "Trạng thái bật/tắt quy tắc không hợp lệ.",
    },
  }
);

export const DELETE = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("banned_keywords.write");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const data = await gw<{ ok: boolean }>(
      `/api/v1/admin/banned-keywords/${encodeURIComponent(id.trim())}`,
      { method: "DELETE" }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã từ khoá cấm không hợp lệ.",
    },
  }
);
