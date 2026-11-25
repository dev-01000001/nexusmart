import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { normalizeSellerNotifications } from "@/features/seller/sellerReturns";
import { assertSellerSession } from "@/features/seller/sellerServer";

const VALID_CATEGORIES = new Set(["ORDER", "PROMOTION", "SYSTEM"]);

export const GET = apiRoute(async (req) => {
  await assertSellerSession();

  const params = req.nextUrl.searchParams;
  const rawCategory = (params.get("category") ?? "").trim().toUpperCase();
  const category = VALID_CATEGORIES.has(rawCategory) ? rawCategory : undefined;
  const unreadOnlyRaw = (params.get("unread_only") ?? "").trim().toLowerCase();
  const unreadOnly = unreadOnlyRaw === "true" || unreadOnlyRaw === "1" ? true : undefined;

  const parsedPage = Number.parseInt(params.get("page") ?? "1", 10);
  const page = Number.isFinite(parsedPage) && parsedPage >= 1 ? parsedPage : 1;
  const parsedSize = Number.parseInt(params.get("page_size") ?? "50", 10);
  const pageSize = Number.isFinite(parsedSize) && parsedSize >= 1 ? Math.min(100, parsedSize) : 50;

  const raw = await gw<unknown>("/api/v1/notifications", {
    query: {
      category,
      unread_only: unreadOnly,
      page,
      page_size: pageSize,
    },
  });

  const normalized = normalizeSellerNotifications(raw);
  return Response.json(
    {
      items: normalized.items,
      unread_count: normalized.unreadCount,
      total: normalized.total,
      page,
      page_size: pageSize,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
