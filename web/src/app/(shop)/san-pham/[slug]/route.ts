import { resolveLegacyProductUrl } from "@/features/platform/legacy.server";
import { legacyNotFound, legacyRedirect, safeDecode } from "@/features/platform/legacyResponse";

export const dynamic = "force-dynamic";

/** Old marketplace product page: 308 to the product on its shop's PUBLISHED site, otherwise 404. */
export async function GET(req: Request, { params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const target = await resolveLegacyProductUrl(safeDecode(slug), req).catch(() => null);
  return target ? legacyRedirect(target) : legacyNotFound();
}
