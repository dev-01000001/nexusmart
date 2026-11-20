import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerVoucher } from "@/features/seller/sellerApiTypes";

interface CreateVoucherBody {
  code?: string;
  name?: string;
  discount_type?: string;
  value_vnd?: number;
  discount_percent?: number;
  max_discount_vnd?: number;
  min_order_vnd?: number;
  quota?: number;
  per_user_limit?: number;
  budget_vnd?: number;
  starts_at?: string;
  ends_at?: string;
}

export const GET = apiRoute(async () => {
  await assertSellerSession();
  // Gateway scopes strictly by JWT shop_id; ignore any client query params.
  const data = await gw<{ vouchers: GwSellerVoucher[] }>("/api/v1/seller/vouchers");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<CreateVoucherBody>(req);
    const code = (raw.code ?? "").trim().toUpperCase();
    const discountType = (raw.discount_type ?? "").trim().toUpperCase();
    const quota = Math.trunc(Number(raw.quota) || 0);
    if (!code || !["FIXED", "PERCENT"].includes(discountType) || quota <= 0) {
      throw new GatewayError("invalid", "INVALID_VOUCHER");
    }

    const now = new Date();
    const startsAt = raw.starts_at?.trim() || now.toISOString();
    const endsAt =
      raw.ends_at?.trim() || new Date(now.getTime() + 14 * 24 * 3600_000).toISOString();

    const data = await gw<{ voucher: GwSellerVoucher }>("/api/v1/seller/vouchers", {
      method: "POST",
      body: {
        code,
        name: (raw.name ?? code).trim(),
        discount_type: discountType,
        value_vnd: discountType === "FIXED" ? Math.trunc(Number(raw.value_vnd) || 0) : 0,
        discount_percent:
          discountType === "PERCENT" ? Math.trunc(Number(raw.discount_percent) || 0) : 0,
        max_discount_vnd: Math.max(0, Math.trunc(Number(raw.max_discount_vnd) || 0)),
        min_order_vnd: Math.max(0, Math.trunc(Number(raw.min_order_vnd) || 0)),
        quota,
        per_user_limit: Math.max(1, Math.trunc(Number(raw.per_user_limit) || 1)),
        budget_vnd: Math.max(0, Math.trunc(Number(raw.budget_vnd) || 0)),
        starts_at: startsAt,
        ends_at: endsAt,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_VOUCHER: "Vui lòng kiểm tra mã voucher, kiểu giảm giá và số lượt phát hành.",
      ALREADY_EXISTS: "Mã voucher này đã tồn tại. Vui lòng chọn mã khác.",
    },
  }
);
