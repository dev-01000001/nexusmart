import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  normalizeGwSite,
  resolveSiteErrorMessage,
  type RawGwSite,
} from "@/features/seller/site/siteServer";

interface SaveDraftBody {
  subdomain?: unknown;
  config?: unknown;
}

export const PUT = apiRoute(async (req) => {
  await assertSellerSession();
  const raw = await readJson<SaveDraftBody>(req);

  const subdomain = typeof raw.subdomain === "string" ? raw.subdomain.trim().toLowerCase() : "";
  if (!subdomain) {
    return Response.json(
      {
        error: {
          code: "INVALID_SUBDOMAIN",
          message: "Vui lòng nhập tên miền phụ hợp lệ cho gian hàng.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }
  if (!raw.config || typeof raw.config !== "object" || Array.isArray(raw.config)) {
    return Response.json(
      {
        error: {
          code: "INVALID_CONFIG",
          message: "Cấu hình giao diện site chưa đúng định dạng.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  // Forward strictly { subdomain, config }; NEVER trust or pass client shop_id.
  try {
    const data = await gw<{ site?: RawGwSite | null }>("/api/v1/seller/site/draft", {
      method: "PUT",
      body: {
        subdomain,
        config: raw.config,
      },
    });
    return Response.json(
      { site: normalizeGwSite(data.site) },
      { headers: { "Cache-Control": "no-store" } }
    );
  } catch (err) {
    if (err instanceof GatewayError) {
      const mapped = resolveSiteErrorMessage(err);
      return Response.json(
        { error: { code: mapped.code, message: mapped.message } },
        { status: mapped.status, headers: { "Cache-Control": "no-store" } }
      );
    }
    throw err;
  }
});
