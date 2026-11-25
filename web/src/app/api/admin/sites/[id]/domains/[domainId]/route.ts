import { domainErrorResponse, revalidateDomainCaches } from "@/lib/server/domains";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string; domainId: string }>;
}

export const DELETE = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("sites.domains.write");
    const { id: rawId, domainId: rawDomainId } = await ctx.params;
    const id = parseEntityId(rawId);
    const domainId = parseEntityId(rawDomainId);

    if (!id || !domainId) {
      throw new GatewayError("invalid", "INVALID_DOMAIN_PARAMS");
    }

    try {
      await gw<void>(
        `/api/v1/admin/sites/${encodeURIComponent(id)}/domains/${encodeURIComponent(domainId)}`,
        {
          method: "DELETE",
        }
      );
      revalidateDomainCaches({ shopId: id, clearHostCache: true });
      return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
    } catch (err) {
      return domainErrorResponse(err);
    }
  },
  {
    messages: {
      INVALID_DOMAIN_PARAMS: "Mã cửa hàng hoặc mã tên miền không hợp lệ.",
    },
  }
);
