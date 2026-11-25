import type { GatewayCarrierConfig } from "@/lib/gateway-types";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";

export const GET = apiRoute(async () => {
  await assertCapability("carriers.read");
  const data = await gw<{ carriers: GatewayCarrierConfig[] }>("/api/v1/admin/carriers");
  return Response.json(
    { carriers: data.carriers ?? [] },
    { headers: { "Cache-Control": "no-store" } }
  );
});
