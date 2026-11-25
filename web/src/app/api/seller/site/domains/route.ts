import { getSiteRootDomain } from "@/lib/server/config";
import { domainErrorResponse, revalidateDomainCaches } from "@/lib/server/domains";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import {
  normalizeSiteDomain,
  normalizeSiteDomains,
  validateCustomHostname,
  type RawSiteDomain,
} from "@/lib/site/domains";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface AddCustomDomainBody {
  hostname?: unknown;
}

export const GET = apiRoute(async () => {
  await assertSellerSession();

  try {
    const data = await gw<{ domains?: RawSiteDomain[] }>("/api/v1/seller/site/domains");
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
});

export const POST = apiRoute(async (req) => {
  const session = await assertSellerSession();
  const raw = await readJson<AddCustomDomainBody>(req);
  const rawHostname =
    raw && typeof raw === "object" && typeof raw.hostname === "string" ? raw.hostname : "";

  const validated = validateCustomHostname(rawHostname, getSiteRootDomain());
  if (!validated.ok) {
    return Response.json(
      { error: { code: "invalid_hostname", message: validated.message } },
      { status: 400, headers: { "Cache-Control": "no-store" } }
    );
  }

  // Forward strictly { hostname }; shop identity always comes from the seller JWT on the gateway.
  try {
    const data = await gw<{ domain?: RawSiteDomain | null }>("/api/v1/seller/site/domains", {
      method: "POST",
      body: { hostname: validated.hostname },
    });
    const domain = normalizeSiteDomain(data?.domain);
    revalidateDomainCaches({
      shopId: session.user.shopId,
      hosts: domain ? [domain.host] : [validated.hostname],
      clearHostCache: domain?.status === "ACTIVE" || Boolean(domain?.is_primary),
    });
    return Response.json({ domain }, { status: 201, headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    return domainErrorResponse(err);
  }
});
