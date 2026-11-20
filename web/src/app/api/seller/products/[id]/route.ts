import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerProduct } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface SkuBody {
  id?: string;
  sku_code?: string;
  name?: string;
  attributes?: Record<string, string>;
  price_vnd?: number;
  stock?: number;
  weight_grams?: number;
  image_url?: string;
}

interface UpdateProductBody {
  category_id?: string;
  name?: string;
  description?: string;
  images?: string[];
  status?: string;
  skus?: SkuBody[];
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const data = await gw<{ product: GwSellerProduct | null }>(
    `/api/v1/products/${encodeURIComponent(id)}`
  );
  if (!data.product || data.product.shop_id !== session.user.shopId) {
    throw new GatewayError("not_found");
  }
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const raw = await readJson<UpdateProductBody>(req);
    const skus = Array.isArray(raw.skus)
      ? raw.skus.map((s) => ({
          id: s.id ? String(s.id).trim() : "",
          sku_code: (s.sku_code ?? "").trim(),
          name: (s.name ?? "").trim(),
          attributes: s.attributes ?? {},
          price_vnd: Math.trunc(Number(s.price_vnd) || 0),
          stock: Math.max(0, Math.trunc(Number(s.stock) || 0)),
          weight_grams: Math.max(1, Math.trunc(Number(s.weight_grams) || 250)),
          image_url: (s.image_url ?? "").trim(),
        }))
      : undefined;

    const data = await gw<{ product: GwSellerProduct }>(
      `/api/v1/seller/products/${encodeURIComponent(id)}`,
      {
        method: "PUT",
        body: {
          category_id: (raw.category_id ?? "").trim(),
          name: (raw.name ?? "").trim(),
          description: (raw.description ?? "").trim(),
          images: Array.isArray(raw.images) ? raw.images.filter((x) => typeof x === "string") : [],
          status: raw.status ? String(raw.status).trim() : "",
          skus,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      FAILED_PRECONDITION: "Tồn kho mới không được nhỏ hơn số lượng đang giữ cho đơn hàng.",
      ALREADY_EXISTS: "Mã SKU bị trùng trong sản phẩm. Vui lòng kiểm tra lại.",
    },
  }
);

export const DELETE = apiRoute<RouteCtx>(async (_req, ctx) => {
  await assertSellerSession();
  const { id } = await ctx.params;
  const data = await gw<{ ok: boolean }>(`/api/v1/seller/products/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
