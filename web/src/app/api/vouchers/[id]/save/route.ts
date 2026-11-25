import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { STOREFRONT_CHANNEL, isSiteShopVoucher } from "@/lib/server/storefront";
import { mapVoucherError } from "@/features/commerce/checkoutErrors";
import type { GwVoucher } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ id: string }> };

export const POST = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const site = await requireRequestSite(req);
    await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    let code: string | undefined;
    const raw = await req.text();
    if (raw.trim()) {
      try {
        const parsed = JSON.parse(raw) as { code?: unknown };
        if (typeof parsed.code === "string" && parsed.code.trim()) {
          code = parsed.code.trim();
        }
      } catch {
        throw new GatewayError("invalid", "INVALID_JSON");
      }
    }

    // Reject platform, freeship, and other-shop vouchers before calling SaveVoucher.
    const { vouchers } = await gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
      query: {
        shop_id: site.shopId,
        scope: "SHOP",
        sales_channel: STOREFRONT_CHANNEL,
        storefront_shop_id: site.shopId,
      },
    });
    const matched = (vouchers ?? [])
      .filter((v) => isSiteShopVoucher(v, site.shopId))
      .find(
        (v) =>
          v.id === cleanId || (code !== undefined && v.code.toUpperCase() === code.toUpperCase())
      );
    if (!matched || (code && matched.code.toUpperCase() !== code.toUpperCase())) {
      throw new GatewayError("not_found", "VOUCHER_NOT_FOUND");
    }

    try {
      const data = await gw<{ saved: boolean; voucher: GwVoucher }>(
        `/api/v1/vouchers/${encodeURIComponent(matched.id)}/save`,
        {
          method: "POST",
          body: { code: matched.code },
        }
      );
      return Response.json(data, { headers: { "Cache-Control": "no-store" } });
    } catch (err) {
      if (
        err instanceof GatewayError &&
        (err.kind === "conflict" || err.kind === "not_found" || err.kind === "rate_limited")
      ) {
        const mapped = mapVoucherError(err.code, err.detail);
        return Response.json(
          { error: { code: mapped.code, message: mapped.message } },
          { status: mapped.status ?? err.status, headers: { "Cache-Control": "no-store" } }
        );
      }
      throw err;
    }
  },
  {
    messages: {
      VOUCHER_NOT_FOUND: "Mã ưu đãi không tồn tại hoặc không áp dụng tại cửa hàng này.",
    },
  }
);
