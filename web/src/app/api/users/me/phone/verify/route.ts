import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession, type GatewayUser, toSessionUser } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  await assertSession();
  const body = await readJson<{ phone?: unknown; otp?: unknown }>(req);
  const phone = typeof body?.phone === "string" ? body.phone.trim() : "";
  const otp = typeof body?.otp === "string" ? body.otp.trim() : "";
  if (!phone || !otp) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const data = await gw<{ verified?: boolean; user?: GatewayUser | null }>(
    "/api/v1/users/me/phone/verify",
    {
      method: "POST",
      body: { phone, otp },
    }
  );

  return Response.json(
    {
      verified: Boolean(data?.verified ?? true),
      user: data?.user?.id ? toSessionUser(data.user) : null,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
