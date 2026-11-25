import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwShopShippingSettings } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface UpdateShippingSettingsBody {
  enabled_carrier_codes?: unknown;
  default_weight_grams?: unknown;
  default_length_cm?: unknown;
  default_width_cm?: unknown;
  default_height_cm?: unknown;
  free_ship_enabled?: unknown;
  free_ship_min_order_vnd?: unknown;
  free_ship_max_vnd?: unknown;
}

export const GET = apiRoute(async () => {
  await assertSellerSession();
  const data = await gw<{ settings: GwShopShippingSettings }>("/api/v1/seller/shipping-settings");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const PUT = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<UpdateShippingSettingsBody>(req);
    const carriers = Array.isArray(raw?.enabled_carrier_codes)
      ? Array.from(
          new Set(
            raw.enabled_carrier_codes
              .filter((c): c is string => typeof c === "string")
              .map((c) => c.trim().toUpperCase())
              .filter((c) => /^[A-Z0-9_-]{2,32}$/.test(c))
          )
        )
      : [];

    if (carriers.length === 0) {
      throw new GatewayError("invalid", "INVALID_CARRIERS");
    }

    const weight = Math.trunc(Number(raw?.default_weight_grams));
    const length = Math.trunc(Number(raw?.default_length_cm));
    const width = Math.trunc(Number(raw?.default_width_cm));
    const height = Math.trunc(Number(raw?.default_height_cm));
    if (
      !Number.isFinite(weight) ||
      weight < 1 ||
      weight > 50_000 ||
      !Number.isFinite(length) ||
      length < 1 ||
      length > 200 ||
      !Number.isFinite(width) ||
      width < 1 ||
      width > 200 ||
      !Number.isFinite(height) ||
      height < 1 ||
      height > 200
    ) {
      throw new GatewayError("invalid", "INVALID_PACKAGE_DIMENSIONS");
    }

    const freeShipEnabled = Boolean(raw?.free_ship_enabled);
    const freeShipMin = Math.trunc(Number(raw?.free_ship_min_order_vnd ?? 0));
    const freeShipMax = Math.trunc(Number(raw?.free_ship_max_vnd ?? 0));
    if (
      !Number.isFinite(freeShipMin) ||
      freeShipMin < 0 ||
      !Number.isFinite(freeShipMax) ||
      freeShipMax < 0
    ) {
      throw new GatewayError("invalid", "INVALID_FREESHIP_CONFIG");
    }
    if (freeShipEnabled && freeShipMax <= 0) {
      throw new GatewayError("invalid", "INVALID_FREESHIP_CONFIG");
    }

    const data = await gw<{ settings: GwShopShippingSettings }>(
      "/api/v1/seller/shipping-settings",
      {
        method: "PUT",
        body: {
          enabled_carrier_codes: carriers,
          default_weight_grams: weight,
          default_length_cm: length,
          default_width_cm: width,
          default_height_cm: height,
          free_ship_enabled: freeShipEnabled,
          free_ship_min_order_vnd: freeShipEnabled ? freeShipMin : 0,
          free_ship_max_vnd: freeShipEnabled ? freeShipMax : 0,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CARRIERS: "Gian hàng cần bật ít nhất một hãng vận chuyển.",
      INVALID_PACKAGE_DIMENSIONS:
        "Khối lượng mặc định (1–50.000g) và kích thước kiện (1–200cm) phải là số nguyên hợp lệ.",
      INVALID_FREESHIP_CONFIG:
        "Mức đơn tối thiểu (≥ 0₫) và mức hỗ trợ Freeship tối đa (> 0₫ khi bật) chưa hợp lệ.",
    },
  }
);
