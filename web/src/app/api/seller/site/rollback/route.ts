import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  normalizeGwSite,
  normalizeGwSiteVersion,
  resolveSiteErrorMessage,
  revalidateSiteCache,
  type RawGwSite,
  type RawGwSiteVersion,
} from "@/features/seller/site/siteServer";

interface RollbackBody {
  version_id?: unknown;
  note?: unknown;
}

export const POST = apiRoute(async (req) => {
  await assertSellerSession();
  const raw = await readJson<RollbackBody>(req);
  const versionId = typeof raw.version_id === "string" ? raw.version_id.trim() : "";
  const note = typeof raw.note === "string" ? raw.note.trim().slice(0, 300) : "";

  if (!versionId) {
    return Response.json(
      {
        error: {
          code: "INVALID_VERSION",
          message: "Vui lòng chọn phiên bản cần khôi phục.",
        },
      },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  try {
    const data = await gw<{ site?: RawGwSite | null; version?: RawGwSiteVersion | null }>(
      "/api/v1/seller/site/rollback",
      {
        method: "POST",
        body: { version_id: versionId, note },
      }
    );
    const site = normalizeGwSite(data.site);
    const version = data.version ? normalizeGwSiteVersion(data.version) : null;
    revalidateSiteCache(site);
    return Response.json({ site, version }, { headers: { "Cache-Control": "no-store" } });
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
