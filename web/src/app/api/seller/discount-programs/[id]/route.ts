import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwShopDiscountProgram } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import { isValidResourceId } from "@/features/seller/sellerValidation";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface DiscountItemBody {
  sku_id?: unknown;
  discounted_price_vnd?: unknown;
  per_user_limit?: unknown;
}

interface UpdateDiscountProgramBody {
  name?: unknown;
  starts_at?: unknown;
  ends_at?: unknown;
  items?: unknown;
  replace_items?: unknown;
}

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const programId = assertValidId(id, "INVALID_PROGRAM_ID");

    const raw = await readJson<UpdateDiscountProgramBody>(req);
    const name = typeof raw?.name === "string" ? raw.name.trim() : "";
    const startsAt = typeof raw?.starts_at === "string" ? raw.starts_at.trim() : "";
    const endsAt = typeof raw?.ends_at === "string" ? raw.ends_at.trim() : "";
    if (name.length < 3 || name.length > 150 || !startsAt || !endsAt) {
      throw new GatewayError("invalid", "INVALID_DISCOUNT_PROGRAM");
    }

    const replaceItems = Boolean(raw?.replace_items);
    const items = Array.isArray(raw?.items)
      ? (raw.items as DiscountItemBody[]).map((it) => {
          const skuId = typeof it?.sku_id === "string" ? it.sku_id.trim() : "";
          const price = Math.trunc(Number(it?.discounted_price_vnd) || 0);
          const limit = Math.max(0, Math.trunc(Number(it?.per_user_limit) || 0));
          if (!isValidResourceId(skuId) || price <= 0) {
            throw new GatewayError("invalid", "INVALID_DISCOUNT_PROGRAM");
          }
          return {
            sku_id: skuId,
            discounted_price_vnd: price,
            per_user_limit: limit,
          };
        })
      : undefined;

    const data = await gw<{ program: GwShopDiscountProgram; warnings?: string[] }>(
      `/api/v1/seller/discount-programs/${encodeURIComponent(programId)}`,
      {
        method: "PUT",
        body: {
          name,
          starts_at: startsAt,
          ends_at: endsAt,
          items,
          replace_items: replaceItems,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PROGRAM_ID: "Mã chương trình giảm giá không hợp lệ.",
      INVALID_DISCOUNT_PROGRAM: "Thông tin cập nhật chương trình giảm giá chưa hợp lệ.",
    },
  }
);
