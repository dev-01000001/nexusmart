import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { fetchBuyerOrders } from "@/features/account/orders.server";

export const GET = apiRoute(async (req) => {
  await assertSession();
  const { searchParams } = req.nextUrl;
  const status = searchParams.get("status")?.trim() || undefined;
  const page = Number(searchParams.get("page")) || 1;
  const pageSize = Number(searchParams.get("page_size") ?? searchParams.get("pageSize")) || 50;

  const {
    items,
    total,
    page: curPage,
    pageSize: curSize,
  } = await fetchBuyerOrders({
    status,
    page,
    pageSize,
    includeExtras: false,
  });

  return Response.json(
    { items, page: curPage, pageSize: curSize, total },
    { headers: { "Cache-Control": "no-store" } }
  );
});
