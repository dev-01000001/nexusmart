import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwSellerVoucher } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface UpdateVoucherStatusBody {
  status?: unknown;
}

export const PATCH = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const voucherId = assertValidId(id, "INVALID_VOUCHER_ID");

    const raw = await readJson<UpdateVoucherStatusBody>(req);
    const status = typeof raw?.status === "string" ? raw.status.trim().toUpperCase() : "";
    if (status !== "ACTIVE" && status !== "PAUSED") {
      throw new GatewayError("invalid", "INVALID_VOUCHER_STATUS");
    }

    const data = await gw<{ voucher: GwSellerVoucher }>(
      `/api/v1/seller/vouchers/${encodeURIComponent(voucherId)}/status`,
      {
        method: "PATCH",
        body: { status },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_VOUCHER_ID: "Mã định danh voucher không hợp lệ.",
      INVALID_VOUCHER_STATUS: "Trạng thái voucher chỉ hỗ trợ bật (ACTIVE) hoặc tạm dừng (PAUSED).",
    },
  }
);
