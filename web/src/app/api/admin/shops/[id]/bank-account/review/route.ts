import type { GatewayShopBankAccountResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId, validateShopBankReviewInput } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("shops.bank.review");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateShopBankReviewInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<GatewayShopBankAccountResponse>(
      `/api/v1/admin/shops/${encodeURIComponent(cleanId)}/bank-account/review`,
      {
        method: "POST",
        body: {
          bank_account_id: parsed.value.bank_account_id,
          decision: parsed.value.decision,
          reason: parsed.value.reason,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_SHOP_ID: "Mã gian hàng không hợp lệ.",
      INVALID_BANK_ACCOUNT_ID: "Thiếu mã bản ghi tài khoản ngân hàng hợp lệ.",
      INVALID_DECISION: "Quyết định thẩm định tài khoản ngân hàng không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do từ chối tài khoản ngân hàng (tối thiểu 3 ký tự).",
    },
  }
);
