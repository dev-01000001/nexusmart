import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayPreviewBannedKeywordResponse } from "@/lib/gateway-types";

interface PreviewBody {
  term?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertCapability("banned_keywords.read");
    const raw = await readJson<PreviewBody>(req);
    const term = typeof raw.term === "string" ? raw.term.trim() : "";
    if (!term || term.length > 120) {
      throw new GatewayError("invalid", "INVALID_TERM");
    }

    const data = await gw<GatewayPreviewBannedKeywordResponse>(
      "/api/v1/admin/banned-keywords/preview",
      {
        method: "POST",
        body: { term },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_TERM: "Vui lòng nhập cụm từ cấm hợp lệ (tối đa 120 ký tự).",
    },
  }
);
