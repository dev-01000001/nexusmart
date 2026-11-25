import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
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

interface PublishBody {
  note?: unknown;
}

export const POST = apiRoute(async (req) => {
  await assertSellerSession();

  let note = "";
  const rawText = await req.text();
  if (rawText.trim()) {
    let parsed: PublishBody;
    try {
      parsed = JSON.parse(rawText) as PublishBody;
    } catch {
      throw new GatewayError("invalid", "INVALID_JSON");
    }
    if (typeof parsed.note === "string") {
      note = parsed.note.trim().slice(0, 300);
    }
  }

  try {
    const data = await gw<{ site?: RawGwSite | null; version?: RawGwSiteVersion | null }>(
      "/api/v1/seller/site/publish",
      {
        method: "POST",
        body: { note },
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
