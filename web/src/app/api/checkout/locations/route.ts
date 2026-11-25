import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import type { GwProvince, GwWard } from "@/features/commerce/types";

export const GET = apiRoute(async (req) => {
  await requireRequestSite(req);
  const { searchParams } = new URL(req.url);
  const provinceCode = (searchParams.get("province_code") ?? "").trim();
  const query = (searchParams.get("query") ?? "").trim() || undefined;

  if (provinceCode) {
    const data = await gw<{ wards: GwWard[] }>(
      `/api/v1/shipping/provinces/${encodeURIComponent(provinceCode)}/wards`,
      { auth: "none", query: { query } }
    );
    return Response.json(
      { wards: data.wards ?? [] },
      { headers: { "Cache-Control": "public, max-age=300" } }
    );
  }

  const data = await gw<{ provinces: GwProvince[] }>("/api/v1/shipping/provinces", {
    auth: "none",
    query: { query },
  });
  return Response.json(
    { provinces: data.provinces ?? [] },
    { headers: { "Cache-Control": "public, max-age=300" } }
  );
});
