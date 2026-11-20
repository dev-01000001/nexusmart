import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAdminFlashSaleItem } from "@/features/admin/adminApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ApproveFlashItemBody {
  approve?: boolean;
  reason?: string;
}

export const POST = apiRoute<RouteCtx>(async (req, ctx) => {
  await assertAdminSession();
  const { id } = await ctx.params;
  const raw = await readJson<ApproveFlashItemBody>(req);
  const data = await gw<{ item: GwAdminFlashSaleItem }>(
    `/api/v1/admin/flash-sale-items/${encodeURIComponent(id)}/approve`,
    {
      method: "POST",
      body: {
        approve: Boolean(raw.approve),
        reason: (raw.reason ?? "").trim(),
      },
    }
  );
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
