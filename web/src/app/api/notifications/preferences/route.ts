import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GatewayNotificationPreferences } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

export const GET = apiRoute(async () => {
  await assertSession();
  const data = await gw<{ preferences: GatewayNotificationPreferences }>(
    "/api/v1/notifications/preferences"
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const PUT = apiRoute(async (req) => {
  await assertSession();
  const body = await readJson<Partial<GatewayNotificationPreferences>>(req);
  const data = await gw<{ preferences: GatewayNotificationPreferences }>(
    "/api/v1/notifications/preferences",
    {
      method: "PUT",
      body: {
        order_in_app: Boolean(body.order_in_app),
        order_email: Boolean(body.order_email),
        order_push: Boolean(body.order_push),
        promotion_in_app: Boolean(body.promotion_in_app),
        promotion_email: Boolean(body.promotion_email),
        promotion_push: Boolean(body.promotion_push),
        system_in_app: Boolean(body.system_in_app),
        system_email: Boolean(body.system_email),
        system_push: Boolean(body.system_push),
      },
    }
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
