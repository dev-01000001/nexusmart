import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gwRaw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayRescanProductsResponse } from "@/lib/gateway-types";
import { validateRescanBannedKeywordsInput } from "@/features/admin/bannedKeywordsHelper";

export const POST = apiRoute(
  async (req) => {
    await assertCapability("banned_keywords.write");
    const raw = await readJson<unknown>(req);
    const validated = validateRescanBannedKeywordsInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const { keyword_ids: keywordIds, dry_run: dryRun, cursor, limit } = validated.value;
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
      INVALID_ARGUMENT: "Chế độ chạy thử quét lại phải là giá trị đúng/sai hợp lệ.",
    },
  }
);
