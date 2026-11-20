import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
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

export const POST = apiRoute<RouteCtx>(async (req, ctx) => {
  await assertAdminSession();
  const { id } = await ctx.params;
  const raw = await readJson<SuspendSiteBody>(req);

  if (typeof raw.suspend !== "boolean") {
    return Response.json(
      {
        error: {
          code: "INVALID_SUSPEND_FLAG",
          message: "Trạng thái khóa hoặc mở khóa site không hợp lệ.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  const reason = typeof raw.reason === "string" ? raw.reason.trim().slice(0, 300) : "";

  try {
    const data = await gw<{ site?: RawGwSite | null }>(
      `/api/v1/admin/sites/${encodeURIComponent(id)}/suspend`,
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
});
