import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwShopDiscountProgram } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidResourceId } from "@/features/seller/sellerValidation";

interface DiscountItemBody {
  sku_id?: unknown;
  discounted_price_vnd?: unknown;
  per_user_limit?: unknown;
}

interface CreateDiscountProgramBody {
  name?: unknown;
  starts_at?: unknown;
  ends_at?: unknown;
  items?: unknown;
}

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  const data = await gw<{
    items: GwShopDiscountProgram[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/discount-programs", {
    query: {
      status: q.get("status"),
      page: q.get("page"),
      page_size: q.get("page_size"),
    },
  });
  return Response.json(
    {
      items: data.items ?? [],
      page: data.page ?? 1,
      page_size: data.page_size ?? 50,
      total: data.total ?? 0,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<CreateDiscountProgramBody>(req);
    const name = typeof raw?.name === "string" ? raw.name.trim() : "";
    const startsAt = typeof raw?.starts_at === "string" ? raw.starts_at.trim() : "";
    const endsAt = typeof raw?.ends_at === "string" ? raw.ends_at.trim() : "";
    if (
      name.length < 3 ||
      name.length > 150 ||
      !startsAt ||
      !endsAt ||
      !Array.isArray(raw?.items) ||
      raw.items.length === 0
    ) {
      throw new GatewayError("invalid", "INVALID_DISCOUNT_PROGRAM");
    }

    const items = (raw.items as DiscountItemBody[]).map((it) => {
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
    });

    const data = await gw<{ program: GwShopDiscountProgram; warnings?: string[] }>(
      "/api/v1/seller/discount-programs",
      {
        method: "POST",
        body: {
          name,
          starts_at: startsAt,
          ends_at: endsAt,
          items,
        },
      }
    );
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_DISCOUNT_PROGRAM:
        "Vui lòng nhập tên chương trình (tối thiểu 3 ký tự), thời gian và danh sách SKU hợp lệ.",
    },
  }
);
