import type { GatewayShopBankAccountResponse } from "@/lib/gateway-types";
import { GatewayError, isShopBankAccountNotFoundError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("shops.bank.read");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    try {
      const data = await gw<GatewayShopBankAccountResponse>(
        `/api/v1/admin/shops/${encodeURIComponent(cleanId)}/bank-account`
      );
      return Response.json(
        { bank_account: data.bank_account ?? null },
        { headers: { "Cache-Control": "no-store" } }
      );
    } catch (err) {
      if (isShopBankAccountNotFoundError(err)) {
        return Response.json({ bank_account: null }, { headers: { "Cache-Control": "no-store" } });
      }
      throw err;
    }
  },
  {
    messages: {
      INVALID_SHOP_ID: "Mã gian hàng không hợp lệ.",
    },
  }
);
