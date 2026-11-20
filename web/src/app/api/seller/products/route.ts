import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerProduct } from "@/features/seller/sellerApiTypes";

interface SkuBody {
  sku_code?: string;
  name?: string;
  attributes?: Record<string, string>;
  price_vnd?: number;
  stock?: number;
  weight_grams?: number;
  image_url?: string;
}

interface CreateProductBody {
  category_id?: string;
  name?: string;
  description?: string;
  images?: string[];
  skus?: SkuBody[];
}

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  // Never forward any client-supplied shop_id; gateway scopes strictly by JWT shop_id.
  const data = await gw<{
    items: GwSellerProduct[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/products", {
    query: {
      category_id: q.get("category_id"),
      keyword: q.get("keyword"),
      status: q.get("status"),
      sort_by: q.get("sort_by"),
      page: q.get("page"),
      page_size: q.get("page_size"),
    },
  });
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<CreateProductBody>(req);
    const name = (raw.name ?? "").trim();
    const categoryId = (raw.category_id ?? "").trim();
    if (!name || !categoryId || !Array.isArray(raw.skus) || raw.skus.length === 0) {
      throw new GatewayError("invalid", "INVALID_PRODUCT");
    }

    const skus = raw.skus.map((s) => ({
      sku_code: (s.sku_code ?? "").trim(),
      name: (s.name ?? "").trim(),
      attributes: s.attributes ?? {},
      price_vnd: Math.trunc(Number(s.price_vnd) || 0),
      stock: Math.max(0, Math.trunc(Number(s.stock) || 0)),
      weight_grams: Math.max(1, Math.trunc(Number(s.weight_grams) || 250)),
      image_url: (s.image_url ?? "").trim(),
    }));

    // Shop ID is derived on gateway from the authenticated seller JWT, never from client input.
    const data = await gw<{ product: GwSellerProduct }>("/api/v1/seller/products", {
      method: "POST",
      body: {
        category_id: categoryId,
        name,
        description: (raw.description ?? "").trim(),
        images: Array.isArray(raw.images) ? raw.images.filter((x) => typeof x === "string") : [],
        skus,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PRODUCT: "Vui lòng nhập đầy đủ tên sản phẩm, danh mục và ít nhất một biến thể SKU.",
      ALREADY_EXISTS: "Mã SKU bị trùng trong sản phẩm. Vui lòng kiểm tra lại.",
      NO_SELLER_SHOP: "Tài khoản người bán chưa có gian hàng hoạt động.",
    },
  }
);
