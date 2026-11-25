import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { normalizeGwSiteVersion, type RawGwSiteVersion } from "@/features/seller/site/siteServer";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;

  try {
    const data = await gw<{
      versions?: RawGwSiteVersion[];
      items?: RawGwSiteVersion[];
      total?: number;
      page?: number;
      page_size?: number;
    }>("/api/v1/seller/site/versions", {
      query: {
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    const versions = (data.versions ?? data.items ?? []).map(normalizeGwSiteVersion);
    return Response.json(
      {
        versions,
        total: data.total ?? versions.length,
        page: data.page ?? 1,
        page_size: data.page_size ?? versions.length,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return Response.json(
        { versions: [], total: 0, page: 1, page_size: 20 },
        { headers: { "Cache-Control": "no-store" } }
      );
    }
    throw err;
  }
});
