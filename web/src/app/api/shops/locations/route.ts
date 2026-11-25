import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GwProvince, GwWard } from "@/features/commerce/types";
import { assertValidId } from "@/features/seller/sellerServer";

export const GET = apiRoute(async (req) => {
  await assertSession();
  const q = req.nextUrl.searchParams;
  const rawProvinceCode = (q.get("province_code") ?? "").trim();
  const query = (q.get("query") ?? "").trim() || undefined;

  if (rawProvinceCode) {
    const provinceCode = assertValidId(rawProvinceCode, "INVALID_PROVINCE_CODE");
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
