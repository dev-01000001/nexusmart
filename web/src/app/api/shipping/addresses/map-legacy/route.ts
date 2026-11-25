import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import type { GatewayMapLegacyAddressResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  await requireRequestSite(req);
  const body = await readJson<{
    legacy_province?: unknown;
    legacy_district?: unknown;
    legacy_ward?: unknown;
  }>(req);

  const legacyProvince =
    typeof body?.legacy_province === "string" ? body.legacy_province.trim() : "";
  const legacyDistrict =
    typeof body?.legacy_district === "string" ? body.legacy_district.trim() : "";
  const legacyWard = typeof body?.legacy_ward === "string" ? body.legacy_ward.trim() : "";

  if (!legacyProvince) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const data = await gw<GatewayMapLegacyAddressResponse>("/api/v1/shipping/addresses/map-legacy", {
    method: "POST",
    auth: "none",
    body: {
      legacy_province: legacyProvince,
      legacy_district: legacyDistrict,
      legacy_ward: legacyWard,
    },
  });

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
