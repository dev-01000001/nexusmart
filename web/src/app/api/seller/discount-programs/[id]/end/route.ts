import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwShopDiscountProgram } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const programId = assertValidId(id, "INVALID_PROGRAM_ID");

    const data = await gw<{ program: GwShopDiscountProgram }>(
      `/api/v1/seller/discount-programs/${encodeURIComponent(programId)}/end`,
      { method: "POST" }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PROGRAM_ID: "Mã chương trình giảm giá không hợp lệ.",
    },
  }
);
