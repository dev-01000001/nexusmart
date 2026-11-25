import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwSellerLedgerEntry } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";

const VALID_ACCOUNT_FILTERS = new Set(["", "pending", "available"]);

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const rawFilter = (q.get("account_filter") ?? "").trim().toLowerCase();
  const accountFilter = VALID_ACCOUNT_FILTERS.has(rawFilter) ? rawFilter : "";

  const data = await gw<{
    items: GwSellerLedgerEntry[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/ledger", {
    query: {
      account_filter: accountFilter || undefined,
      reference_type: q.get("reference_type"),
      page: q.get("page"),
      page_size: q.get("page_size"),
    },
  });
  return Response.json(
    {
      items: data.items ?? [],
      page: data.page ?? 1,
      page_size: data.page_size ?? 20,
      total: data.total ?? 0,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
