import { randomUUID } from "node:crypto";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { mapCheckoutError } from "@/features/commerce/checkoutErrors";
import type { GwCheckoutResponse } from "@/features/commerce/types";

interface CheckoutItemBody {
  sku_id?: string;
  quantity?: number;
  flash_sale_item_id?: string;
}

interface ShippingChoiceBody {
  shop_id?: string;
  carrier_code?: string;
  service_code?: string;
}

interface CheckoutBody {
  address_id?: string;
  from_cart?: boolean;
  items?: CheckoutItemBody[];
  shop_voucher_ids?: Record<string, string | null | undefined>;
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  payment_method?: string;
  shipping_choices?: ShippingChoiceBody[];
  idempotency_key?: string;
}

function normalizePaymentMethod(raw: string | undefined): "COD" | "VIETQR" | null {
  const upper = (raw ?? "").trim().toUpperCase();
  if (upper === "COD") return "COD";
  if (upper === "VIETQR") return "VIETQR";
  return null;
}

export const POST = apiRoute(
  async (req) => {
    await assertSession();
    const body = await readJson<CheckoutBody>(req);

    const addressId = (body.address_id ?? "").trim();
    if (!addressId) {
      throw new GatewayError("invalid", "MISSING_ADDRESS");
    }

    const paymentMethod = normalizePaymentMethod(body.payment_method);
    if (!paymentMethod) {
      throw new GatewayError("invalid", "INVALID_PAYMENT_METHOD");
    }

    const headerKey = (req.headers.get("idempotency-key") ?? "").trim();
    const bodyKey = (body.idempotency_key ?? "").trim();
    const idempotencyKey = headerKey || bodyKey || randomUUID();

    const items = Array.isArray(body.items)
      ? body.items
          .map((it) => ({
            sku_id: (it.sku_id ?? "").trim(),
            quantity: Math.trunc(Number(it.quantity ?? 0)),
            flash_sale_item_id: (it.flash_sale_item_id ?? "").trim(),
          }))
          .filter((it) => it.sku_id.length > 0 && it.quantity > 0)
      : [];

    const fromCart = typeof body.from_cart === "boolean" ? body.from_cart : items.length === 0;

    const shopVoucherIds: Record<string, string> = {};
    for (const [shopId, voucherVal] of Object.entries(body.shop_voucher_ids ?? {})) {
      const cleanShop = shopId.trim();
      const cleanVoucher = typeof voucherVal === "string" ? voucherVal.trim() : "";
      if (cleanShop && cleanVoucher) shopVoucherIds[cleanShop] = cleanVoucher;
    }

    const shippingChoices = Array.isArray(body.shipping_choices)
      ? body.shipping_choices
          .map((c) => ({
            shop_id: (c.shop_id ?? "").trim(),
            carrier_code: (c.carrier_code ?? "").trim(),
            service_code: (c.service_code ?? "").trim(),
          }))
          .filter((c) => c.shop_id.length > 0)
      : [];

    const coinsToUse = Math.max(0, Math.trunc(Number(body.coins_to_use ?? 0) || 0));

    try {
      const data = await gw<GwCheckoutResponse>("/api/v1/checkout", {
        method: "POST",
        idempotencyKey,
        body: {
          address_id: addressId,
          from_cart: fromCart,
          items,
          shop_voucher_ids: shopVoucherIds,
          platform_voucher_id: (body.platform_voucher_id ?? "").trim(),
          freeship_voucher_id: (body.freeship_voucher_id ?? "").trim(),
          coins_to_use: coinsToUse,
          payment_method: paymentMethod,
          shipping_choices: shippingChoices,
        },
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
    },
  }
);
