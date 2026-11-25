import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";
import {
  normalizeGwSite,
  resolveSiteErrorMessage,
  revalidateSiteCache,
  type RawGwSite,
} from "@/features/seller/site/siteServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface SuspendSiteBody {
  suspend?: unknown;
  reason?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("sites.suspend");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_SITE_ID");
    }

    const raw = await readJson<SuspendSiteBody>(req);
    if (typeof raw.suspend !== "boolean") {
      throw new GatewayError("invalid", "INVALID_SUSPEND_FLAG");
    }

    const reason = typeof raw.reason === "string" ? raw.reason.trim().slice(0, 300) : "";
    if (raw.suspend && !reason) {
      throw new GatewayError("invalid", "REASON_REQUIRED");
    }

    try {
      const data = await gw<{ site?: RawGwSite | null }>(
        `/api/v1/admin/sites/${encodeURIComponent(cleanId)}/suspend`,
        {
          method: "POST",
          body: {
            suspend: raw.suspend,
            reason,
          },
        }
      );
      const site = normalizeGwSite(data.site);
      revalidateSiteCache(site);
      return Response.json({ site }, { headers: { "Cache-Control": "no-store" } });
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
  },
  {
    messages: {
      INVALID_SITE_ID: "Mã site gian hàng không hợp lệ.",
      INVALID_SUSPEND_FLAG: "Trạng thái khóa hoặc mở khóa site không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do cụ thể trước khi tạm khóa site.",
    },
  }
);
