import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GatewayRequestPhoneOTPResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  await assertSession();
  const body = await readJson<{ phone?: unknown }>(req);
  const phone = typeof body?.phone === "string" ? body.phone.trim() : "";
  if (!phone) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const data = await gw<GatewayRequestPhoneOTPResponse>("/api/v1/users/me/phone/otp", {
    method: "POST",
    body: { phone },
  });

  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
