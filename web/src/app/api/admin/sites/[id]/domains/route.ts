import { domainErrorResponse } from "@/lib/server/domains";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { normalizeSiteDomains, type RawSiteDomain } from "@/lib/site/domains";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("sites.read");
    const { id: rawId } = await ctx.params;
    const id = parseEntityId(rawId);

    if (!id) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    try {
      const data = await gw<{ domains?: RawSiteDomain[] }>(
        `/api/v1/admin/sites/${encodeURIComponent(id)}/domains`
      );
      return Response.json(
        { domains: normalizeSiteDomains(data?.domains) },
        { headers: { "Cache-Control": "no-store" } }
      );
    } catch (err) {
      if (err instanceof GatewayError && err.kind === "not_found") {
        return Response.json({ domains: [] }, { headers: { "Cache-Control": "no-store" } });
      }
      return domainErrorResponse(err);
    }
  },
  {
    messages: {
      INVALID_SHOP_ID: "Mã cửa hàng không hợp lệ.",
    },
  }
);
