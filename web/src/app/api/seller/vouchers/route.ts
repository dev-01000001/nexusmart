import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerVoucher } from "@/features/seller/sellerApiTypes";

interface CreateVoucherBody {
  code?: unknown;
  name?: unknown;
  discount_type?: unknown;
  value_vnd?: unknown;
  discount_percent?: unknown;
  max_discount_vnd?: unknown;
  min_order_vnd?: unknown;
  quota?: unknown;
  per_user_limit?: unknown;
  budget_vnd?: unknown;
  starts_at?: unknown;
  ends_at?: unknown;
}

const VALID_VOUCHER_STATUS = new Set(["ACTIVE", "PAUSED", "DELETED"]);

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const rawStatus = (q.get("status") ?? "").trim().toUpperCase();
  const status = VALID_VOUCHER_STATUS.has(rawStatus) ? rawStatus : undefined;
  const includeInactive =
    q.get("include_inactive") === "true" || q.get("include_inactive") === "1" ? "true" : undefined;

  // Gateway scopes strictly by JWT shop_id; never forward client shop_id.
  const data = await gw<{ vouchers: GwSellerVoucher[] }>("/api/v1/seller/vouchers", {
    query: {
      include_inactive: includeInactive,
      status,
    },
  });
  return Response.json(
    { vouchers: data.vouchers ?? [] },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<CreateVoucherBody>(req);
    const code = typeof raw?.code === "string" ? raw.code.trim().toUpperCase() : "";
    const discountType =
      typeof raw?.discount_type === "string" ? raw.discount_type.trim().toUpperCase() : "";
    const quota = Math.trunc(Number(raw?.quota) || 0);
    if (!code || code.length > 64 || !["FIXED", "PERCENT"].includes(discountType) || quota <= 0) {
      throw new GatewayError("invalid", "INVALID_VOUCHER");
    }

    const now = new Date();
    const startsAt =
      typeof raw?.starts_at === "string" && raw.starts_at.trim()
        ? raw.starts_at.trim()
        : now.toISOString();
    const endsAt =
      typeof raw?.ends_at === "string" && raw.ends_at.trim()
        ? raw.ends_at.trim()
        : new Date(now.getTime() + 14 * 24 * 3600_000).toISOString();

    const data = await gw<{ voucher: GwSellerVoucher }>("/api/v1/seller/vouchers", {
      method: "POST",
      body: {
        code,
        name: (typeof raw?.name === "string" ? raw.name : code).trim().slice(0, 120),
        discount_type: discountType,
        value_vnd:
          discountType === "FIXED" ? Math.max(0, Math.trunc(Number(raw?.value_vnd) || 0)) : 0,
        discount_percent:
          discountType === "PERCENT"
            ? Math.max(0, Math.trunc(Number(raw?.discount_percent) || 0))
            : 0,
        max_discount_vnd: Math.max(0, Math.trunc(Number(raw?.max_discount_vnd) || 0)),
        min_order_vnd: Math.max(0, Math.trunc(Number(raw?.min_order_vnd) || 0)),
        quota,
        per_user_limit: Math.max(1, Math.trunc(Number(raw?.per_user_limit) || 1)),
        budget_vnd: Math.max(0, Math.trunc(Number(raw?.budget_vnd) || 0)),
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
