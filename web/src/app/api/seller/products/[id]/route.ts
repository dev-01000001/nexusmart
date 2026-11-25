import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import type { GwSellerProduct } from "@/features/seller/sellerApiTypes";
import { isValidResourceId } from "@/features/seller/sellerValidation";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface SkuBody {
  id?: unknown;
  sku_code?: unknown;
  name?: unknown;
  attributes?: unknown;
  price_vnd?: unknown;
  stock?: unknown;
  weight_grams?: unknown;
  image_url?: unknown;
}

interface UpdateProductBody {
  category_id?: unknown;
  name?: unknown;
  slug?: unknown;
  description?: unknown;
  images?: unknown;
  status?: unknown;
  skus?: unknown;
}

function sanitizeAttributes(raw: unknown): Record<string, string> {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof v === "string") {
      out[k.slice(0, 64)] = v.trim().slice(0, 128);
    }
  }
  return out;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    const session = await assertSellerSession();
    const { id } = await ctx.params;
    const productId = assertValidId(id, "INVALID_PRODUCT_ID");

    const data = await gw<{ product: GwSellerProduct | null }>(
      `/api/v1/seller/products/${encodeURIComponent(productId)}`
    );
    if (!data.product || data.product.shop_id !== session.user.shopId) {
      throw new GatewayError("not_found");
    }
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PRODUCT_ID: "Mã sản phẩm không hợp lệ.",
    },
  }
);

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const productId = assertValidId(id, "INVALID_PRODUCT_ID");

    const raw = await readJson<UpdateProductBody>(req);
    const categoryId = typeof raw?.category_id === "string" ? raw.category_id.trim() : "";
    if (categoryId && !isValidResourceId(categoryId)) {
      throw new GatewayError("invalid", "INVALID_PRODUCT");
    }
    const slug = typeof raw?.slug === "string" ? raw.slug.trim().slice(0, 120) : "";

    const skus = Array.isArray(raw?.skus)
      ? (raw.skus as SkuBody[]).map((s) => ({
          id: typeof s?.id === "string" && isValidResourceId(s.id) ? s.id.trim() : "",
          sku_code: typeof s?.sku_code === "string" ? s.sku_code.trim() : "",
          name: typeof s?.name === "string" ? s.name.trim() : "",
          attributes: sanitizeAttributes(s?.attributes),
          price_vnd: Math.trunc(Number(s?.price_vnd) || 0),
          stock: Math.max(0, Math.trunc(Number(s?.stock) || 0)),
          weight_grams: Math.max(1, Math.trunc(Number(s?.weight_grams) || 250)),
          image_url: typeof s?.image_url === "string" ? s.image_url.trim() : "",
        }))
      : undefined;

    const data = await gw<{ product: GwSellerProduct }>(
      `/api/v1/seller/products/${encodeURIComponent(productId)}`,
      {
        method: "PUT",
        body: {
          category_id: categoryId,
          name: typeof raw?.name === "string" ? raw.name.trim() : "",
          ...(slug ? { slug } : {}),
          description: typeof raw?.description === "string" ? raw.description.trim() : "",
          images: Array.isArray(raw?.images)
            ? raw.images.filter((x): x is string => typeof x === "string")
            : [],
          status: typeof raw?.status === "string" ? raw.status.trim() : "",
          skus,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PRODUCT_ID: "Mã sản phẩm không hợp lệ.",
      INVALID_PRODUCT: "Thông tin cập nhật sản phẩm chưa hợp lệ.",
      ALREADY_EXISTS: "Mã SKU hoặc đường dẫn sản phẩm bị trùng. Vui lòng kiểm tra lại.",
    },
  }
);

export const DELETE = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const productId = assertValidId(id, "INVALID_PRODUCT_ID");

    const data = await gw<{ ok: boolean }>(
      `/api/v1/seller/products/${encodeURIComponent(productId)}`,
      { method: "DELETE" }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PRODUCT_ID: "Mã sản phẩm không hợp lệ.",
    },
  }
);
