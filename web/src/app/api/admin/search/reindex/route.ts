import type { GatewaySearchReindexResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { MAX_BFF_BODY_BYTES, readBoundedBody } from "@/lib/server/proxy-security";
import { assertCapability } from "@/lib/server/session";
import { validateSearchReindexInput } from "@/features/admin/AdminConfigValidators";

export const POST = apiRoute(
  async (req) => {
    await assertCapability("search.reindex");

    let rawBody: unknown = undefined;
    const bounded = await readBoundedBody(req, MAX_BFF_BODY_BYTES);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }
    if (bounded.body.byteLength > 0) {
      try {
        rawBody = JSON.parse(new TextDecoder().decode(bounded.body)) as unknown;
      } catch {
        throw new GatewayError("invalid", "INVALID_JSON");
      }
    }

    const validated = validateSearchReindexInput(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<GatewaySearchReindexResponse>("/api/v1/admin/search/reindex", {
      method: "POST",
      body: validated.value,
    });
    return Response.json(data, { status: 202, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ARGUMENT: "Tham số dựng lại chỉ mục không hợp lệ.",
      REINDEX_ALREADY_RUNNING:
        "Đang có tiến trình dựng lại chỉ mục tìm kiếm. Vui lòng chờ hoàn tất.",
    },
  }
);
