import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { verifySiteRefundAccess } from "@/lib/server/storefront";
import { validateBankAccountNumber } from "@/features/account/orderMeta";
import type { GwOrder } from "@/features/account/orderView";
import type { GatewayRefund } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

type RouteCtx = { params: Promise<{ id: string }> };

export const POST = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const body = await readJson<{
      order_id?: unknown;
      bank_code?: unknown;
      bank_account_number?: unknown;
      bank_account_name?: unknown;
    }>(req);

    const bankCode = typeof body?.bank_code === "string" ? body.bank_code.trim() : "";
    const bankAccountNumber =
      typeof body?.bank_account_number === "string"
        ? body.bank_account_number.replace(/\s+/g, "").trim()
        : "";
    const bankAccountName =
      typeof body?.bank_account_name === "string" ? body.bank_account_name.trim() : "";

    if (!bankCode || !validateBankAccountNumber(bankAccountNumber) || bankAccountName.length < 2) {
      throw new GatewayError("invalid", "INVALID_BANK_ACCOUNT");
    }

    const orderId = typeof body?.order_id === "string" ? body.order_id.trim() : "";
    await verifySiteRefundAccess({
      refundId: cleanId,
      orderId,
      shopId: site.shopId,
      session,
      fetchOrder: (oid) =>
        gw<{ order?: GwOrder | null }>(`/api/v1/orders/${encodeURIComponent(oid)}`),
      fetchOrderRefunds: async (oid) => {
        const collected: GatewayRefund[] = [];
        const pageSize = 100;
        for (let page = 1; page <= 5; page++) {
          const res = await gw<{
            items?: GatewayRefund[];
            refunds?: GatewayRefund[];
            total?: number;
          }>(`/api/v1/orders/${encodeURIComponent(oid)}/refunds`, {
            query: { page, page_size: pageSize },
          });
          const batch = res.items ?? res.refunds ?? [];
          collected.push(...batch);
          const total = typeof res.total === "number" ? res.total : batch.length;
          if (batch.length < pageSize || collected.length >= total) break;
        }
        return { items: collected };
      },
    });

    const data = await gw<{ refund: GatewayRefund }>(
      `/api/v1/refunds/${encodeURIComponent(cleanId)}/bank-account`,
      {
        method: "POST",
        body: {
          bank_code: bankCode,
          bank_account_number: bankAccountNumber,
          bank_account_name: bankAccountName,
        },
      }
    );

    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      ORDER_ID_REQUIRED: "Thiếu mã đơn hàng tương ứng với yêu cầu hoàn tiền.",
      INVALID_BANK_ACCOUNT:
        "Thông tin tài khoản ngân hàng chưa hợp lệ (số tài khoản gồm 6–19 chữ số và đầy đủ tên chủ tài khoản).",
    },
  }
);
