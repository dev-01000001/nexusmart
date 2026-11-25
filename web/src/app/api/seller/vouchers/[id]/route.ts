import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwSellerVoucher } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface UpdateVoucherBody {
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

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const voucherId = assertValidId(id, "INVALID_VOUCHER_ID");

    const raw = await readJson<UpdateVoucherBody>(req);
    const body: Record<string, unknown> = {};

    if (typeof raw?.name === "string" && raw.name.trim()) {
      body.name = raw.name.trim().slice(0, 120);
    }
    if (typeof raw?.discount_type === "string" && raw.discount_type.trim()) {
      const dt = raw.discount_type.trim().toUpperCase();
      if (!["FIXED", "PERCENT"].includes(dt)) {
        throw new GatewayError("invalid", "INVALID_VOUCHER");
      }
      body.discount_type = dt;
    }
    if (raw?.value_vnd !== undefined) {
      body.value_vnd = Math.max(0, Math.trunc(Number(raw.value_vnd) || 0));
    }
    if (raw?.discount_percent !== undefined) {
      body.discount_percent = Math.max(0, Math.trunc(Number(raw.discount_percent) || 0));
    }
    if (raw?.max_discount_vnd !== undefined) {
      body.max_discount_vnd = Math.max(0, Math.trunc(Number(raw.max_discount_vnd) || 0));
    }
    if (raw?.min_order_vnd !== undefined) {
      body.min_order_vnd = Math.max(0, Math.trunc(Number(raw.min_order_vnd) || 0));
    }
    if (raw?.quota !== undefined) {
      const quota = Math.trunc(Number(raw.quota) || 0);
      if (quota < 1) throw new GatewayError("invalid", "INVALID_VOUCHER");
      body.quota = quota;
    }
    if (raw?.per_user_limit !== undefined) {
      body.per_user_limit = Math.max(1, Math.trunc(Number(raw.per_user_limit) || 1));
    }
    if (raw?.budget_vnd !== undefined) {
      body.budget_vnd = Math.max(0, Math.trunc(Number(raw.budget_vnd) || 0));
    }
    if (typeof raw?.starts_at === "string" && raw.starts_at.trim()) {
      body.starts_at = raw.starts_at.trim();
    }
    if (typeof raw?.ends_at === "string" && raw.ends_at.trim()) {
      body.ends_at = raw.ends_at.trim();
    }

    const data = await gw<{ voucher: GwSellerVoucher }>(
      `/api/v1/seller/vouchers/${encodeURIComponent(voucherId)}`,
      {
        method: "PUT",
        body,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_VOUCHER_ID: "Mã định danh voucher không hợp lệ.",
      INVALID_VOUCHER: "Thông tin cập nhật voucher chưa hợp lệ.",
    },
  }
);

export const DELETE = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const voucherId = assertValidId(id, "INVALID_VOUCHER_ID");

    const data = await gw<{ ok?: boolean }>(
      `/api/v1/seller/vouchers/${encodeURIComponent(voucherId)}`,
      { method: "DELETE" }
    );
    return Response.json(data ?? { ok: true }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_VOUCHER_ID: "Mã định danh voucher không hợp lệ.",
    },
  }
);
