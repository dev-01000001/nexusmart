import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  normalizeGwSite,
  resolveSiteErrorMessage,
  revalidateSiteCache,
  type RawGwSite,
} from "@/features/seller/site/siteServer";

interface ApplyTemplateBody {
  template_id?: unknown;
  subdomain?: unknown;
}

const TEMPLATE_SLUG_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;

export const POST = apiRoute(async (req) => {
  // Strictly server-authenticated seller session; NEVER trust or accept client shop_id.
  await assertSellerSession();
  const raw = await readJson<ApplyTemplateBody>(req);

  const templateId =
    typeof raw.template_id === "string" ? raw.template_id.trim().toLowerCase() : "";
  if (!templateId || !TEMPLATE_SLUG_RE.test(templateId)) {
    return Response.json(
      {
        error: {
          code: "INVALID_TEMPLATE_ID",
          message: "Mã mẫu giao diện không hợp lệ. Vui lòng chọn một mẫu từ thư viện.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  const subdomain = typeof raw.subdomain === "string" ? raw.subdomain.trim().toLowerCase() : "";

  try {
    const data = await gw<{ site?: RawGwSite | null; template_id?: string }>(
      "/api/v1/seller/site/apply-template",
      {
        method: "POST",
        body: {
          template_id: templateId,
          ...(subdomain ? { subdomain } : {}),
        },
      }
    );
    const site = normalizeGwSite(data.site);
    revalidateSiteCache(site);
    return Response.json(
      {
        site,
        template_id: data.template_id || templateId,
      },
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
