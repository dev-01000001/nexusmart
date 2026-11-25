import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gwRaw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayRescanProductsResponse } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

const MAX_RESCAN_LIMIT = 500;

interface RescanBody {
  keyword_ids?: unknown;
  dry_run?: unknown;
  cursor?: unknown;
  limit?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertCapability("banned_keywords.write");
    const raw = await readJson<RescanBody>(req);

    const keywordIds: string[] = [];
    if (raw.keyword_ids !== undefined && raw.keyword_ids !== null) {
      if (!Array.isArray(raw.keyword_ids) || raw.keyword_ids.length > 100) {
        throw new GatewayError("invalid", "INVALID_KEYWORD_IDS");
      }
      for (const item of raw.keyword_ids) {
        if (typeof item !== "string" || !isValidAdminPathId(item)) {
          throw new GatewayError("invalid", "INVALID_KEYWORD_IDS");
        }
        keywordIds.push(item.trim());
      }
    }

    const dryRun = Boolean(raw.dry_run);
    const cursor = typeof raw.cursor === "string" ? raw.cursor.trim().slice(0, 256) : "";
    let limit = MAX_RESCAN_LIMIT;
    if (raw.limit !== undefined && raw.limit !== null) {
      if (typeof raw.limit !== "number" || !Number.isInteger(raw.limit) || raw.limit < 1) {
        throw new GatewayError("invalid", "INVALID_LIMIT");
      }
      limit = Math.min(MAX_RESCAN_LIMIT, raw.limit);
    }

    const res = await gwRaw("/api/v1/admin/banned-keywords/rescan", {
      method: "POST",
      accept: "application/json",
      body: {
        ...(keywordIds.length > 0 ? { keyword_ids: keywordIds } : {}),
        dry_run: dryRun,
        cursor,
        limit,
      },
      timeoutMs: 60_000,
      signal: req.signal,
    });
    const data = (await res.json()) as GatewayRescanProductsResponse;
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_KEYWORD_IDS: "Danh sách mã từ khoá cấm không hợp lệ.",
      INVALID_LIMIT: "Kích thước lô quét không hợp lệ.",
    },
  }
);
