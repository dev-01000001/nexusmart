import { randomUUID } from "node:crypto";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { mapCheckoutError } from "@/features/commerce/checkoutErrors";
import {
  buildStorefrontCheckoutPayload,
  type CheckoutBody,
} from "@/features/commerce/checkoutPayload";
import type { GwCheckoutResponse } from "@/features/commerce/types";

export const POST = apiRoute(
  async (req) => {
    // Fresh lookup: a site suspended a moment ago can no longer take orders.
    const site = await requireRequestSite(req, { fresh: true });
    await assertSession();
    const body = await readJson<CheckoutBody>(req);

    // STOREFRONT + storefront_shop_id of the Host's site; client channel/shop fields are ignored.
    const built = buildStorefrontCheckoutPayload(body, site.shopId);
    if (!built.ok) {
      throw new GatewayError("invalid", built.code);
    }

    const headerKey = (req.headers.get("idempotency-key") ?? "").trim();
    const bodyKey = typeof body.idempotency_key === "string" ? body.idempotency_key.trim() : "";
    const idempotencyKey = headerKey || bodyKey || randomUUID();

    try {
      const data = await gw<GwCheckoutResponse>("/api/v1/checkout", {
        method: "POST",
        idempotencyKey,
        body: built.payload,
      });

      const status = data.idempotent_replay ? 200 : 201;
      return Response.json(
        { ...data, idempotency_key: idempotencyKey },
        { status, headers: { "Cache-Control": "no-store" } }
      );
    } catch (err) {
      if (
        err instanceof GatewayError &&
        (err.kind === "conflict" ||
          err.kind === "invalid" ||
          err.kind === "not_found" ||
          err.kind === "rate_limited")
      ) {
        const mapped = mapCheckoutError(err.code, err.detail);
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
      MISSING_ADDRESS: "Vui lòng chọn hoặc thêm địa chỉ nhận hàng trước khi đặt hàng.",
      INVALID_PAYMENT_METHOD: "Phương thức thanh toán hiện hỗ trợ COD hoặc chuyển khoản VietQR.",
      SITE_NOT_PUBLISHED: "Cửa hàng này hiện chưa nhận đơn trực tuyến.",
      PLATFORM_PROMO_NOT_ALLOWED:
        "Đơn hàng tại cửa hàng này chỉ hỗ trợ mã ưu đãi riêng của cửa hàng.",
      FOREIGN_SHOP_VOUCHER: "Mã ưu đãi không thuộc cửa hàng bạn đang đặt hàng.",
    },
  }
);
