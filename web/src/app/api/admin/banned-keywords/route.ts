import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayBannedKeyword, PaginatedResponse } from "@/lib/gateway-types";

const ALLOWED_ACTIONS = new Set(["BLOCK", "REVIEW"]);
const ALLOWED_CATEGORIES = new Set(["COUNTERFEIT", "PROHIBITED", "OFF_PLATFORM", "SPAM", "OTHER"]);

interface CreateBannedKeywordBody {
  term?: unknown;
  category?: unknown;
  action?: unknown;
}

export const GET = apiRoute(
  async (req) => {
    await assertCapability("banned_keywords.read");
    const q = req.nextUrl.searchParams;
    const action = (q.get("action") ?? "").trim().toUpperCase();
    if (action && !ALLOWED_ACTIONS.has(action)) {
      throw new GatewayError("invalid", "INVALID_ACTION");
    }
    const category = (q.get("category") ?? "").trim().toUpperCase();
    const includeInactive = (q.get("include_inactive") ?? "").trim().toLowerCase();

    const data = await gw<PaginatedResponse<GatewayBannedKeyword>>(
      "/api/v1/admin/banned-keywords",
      {
        query: {
          query: (q.get("query") ?? "").trim().slice(0, 120) || undefined,
          category: category || undefined,
          action: action || undefined,
          include_inactive:
            includeInactive === "true" || includeInactive === "1" ? "true" : undefined,
          page: q.get("page"),
          page_size: q.get("page_size"),
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ACTION: "Hành động lọc từ khoá cấm không hợp lệ.",
    },
  }
);

export const POST = apiRoute(
  async (req) => {
    await assertCapability("banned_keywords.write");
    const raw = await readJson<CreateBannedKeywordBody>(req);
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

    const data = await gw<{ keyword: GatewayBannedKeyword }>("/api/v1/admin/banned-keywords", {
      method: "POST",
      body: {
        term,
        category: category || "OTHER",
        action,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_TERM: "Cụm từ cấm không được để trống và tối đa 120 ký tự.",
      INVALID_ACTION: "Hành động áp dụng chỉ hỗ trợ BLOCK hoặc REVIEW.",
      INVALID_CATEGORY: "Nhóm vi phạm không hợp lệ.",
    },
  }
);
