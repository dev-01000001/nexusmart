import { roleHasCapability } from "@/lib/admin-rbac";
import type { GatewayShop } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  ALLOWED_SHOP_STATUSES,
  parseOptionalPositiveInt,
} from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(async (req) => {
  const session = await assertCapability("shops.read");
  const q = req.nextUrl.searchParams;
  const rawStatus = (q.get("status") ?? "").trim().toUpperCase();
  const status = (ALLOWED_SHOP_STATUSES as readonly string[]).includes(rawStatus)
    ? rawStatus
    : undefined;

  const rawBankPending = (q.get("bank_review_pending") ?? "").trim().toLowerCase();
  const bankReviewPending =
    rawBankPending === "true" || rawBankPending === "1" ? "true" : undefined;
  if (bankReviewPending && !roleHasCapability(session.user.role, "shops.bank.read")) {
    throw new GatewayError("forbidden");
  }

  const data = await gw<{ items: GatewayShop[]; page: number; page_size: number; total: number }>(
    "/api/v1/admin/shops",
    {
      query: {
        status,
        bank_review_pending: bankReviewPending,
        page: parseOptionalPositiveInt(q.get("page"), 10000),
        page_size: parseOptionalPositiveInt(q.get("page_size"), 100),
      },
    }
  );
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
