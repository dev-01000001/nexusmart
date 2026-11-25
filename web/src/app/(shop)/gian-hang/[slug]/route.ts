import { resolveLegacyShopUrl } from "@/features/platform/legacy.server";
import { legacyNotFound, legacyRedirect, safeDecode } from "@/features/platform/legacyResponse";

export const dynamic = "force-dynamic";

/** Old marketplace shop page: 308 to the shop's own PUBLISHED site, otherwise 404. */
export async function GET(req: Request, { params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const target = await resolveLegacyShopUrl(safeDecode(slug), req).catch(() => null);
  return target ? legacyRedirect(target) : legacyNotFound();
}
