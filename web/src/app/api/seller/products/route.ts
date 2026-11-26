import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession, loadSellerProductTabCounts } from "@/features/seller/sellerServer";
import type { GwSellerProduct } from "@/features/seller/sellerApiTypes";
import { isValidResourceId } from "@/features/seller/sellerValidation";

interface SkuBody {
  sku_code?: unknown;
  name?: unknown;
  attributes?: unknown;
  price_vnd?: unknown;
  stock?: unknown;
  weight_grams?: unknown;
  image_url?: unknown;
}

interface CreateProductBody {
  category_id?: unknown;
  name?: unknown;
  slug?: unknown;
  description?: unknown;
  images?: unknown;
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

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const q = req.nextUrl.searchParams;
  if (q.get("tab_counts_only") === "true") {
    const tabCounts = await loadSellerProductTabCounts();
    return Response.json({ tab_counts: tabCounts }, { headers: { "Cache-Control": "no-store" } });
  }
  const rawCat = (q.get("category_id") ?? "").trim();
  const categoryId = rawCat && isValidResourceId(rawCat) ? rawCat : undefined;
  const rawLowStock = (q.get("low_stock") ?? "").trim().toLowerCase();
  const lowStock = rawLowStock === "true" || rawLowStock === "false" ? rawLowStock : undefined;

  // Never forward any client-supplied shop_id; gateway scopes strictly by JWT shop_id.
  const data = await gw<{
    items: GwSellerProduct[];
    page: number;
    page_size: number;
    total: number;
  }>("/api/v1/seller/products", {
    query: {
      category_id: categoryId,
      keyword: q.get("keyword"),
      status: q.get("status"),
      low_stock: lowStock,
      min_price_vnd: q.get("min_price_vnd"),
      max_price_vnd: q.get("max_price_vnd"),
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
    const name = typeof raw?.name === "string" ? raw.name.trim() : "";
    const categoryId = typeof raw?.category_id === "string" ? raw.category_id.trim() : "";
    const slug = typeof raw?.slug === "string" ? raw.slug.trim().slice(0, 120) : "";

    if (
      !name ||
      !isValidResourceId(categoryId) ||
      !Array.isArray(raw?.skus) ||
      raw.skus.length === 0
    ) {
      throw new GatewayError("invalid", "INVALID_PRODUCT");
    }

    const skus = (raw.skus as SkuBody[]).map((s) => ({
      sku_code: typeof s?.sku_code === "string" ? s.sku_code.trim() : "",
      name: typeof s?.name === "string" ? s.name.trim() : "",
      attributes: sanitizeAttributes(s?.attributes),
      price_vnd: Math.trunc(Number(s?.price_vnd) || 0),
      stock: Math.max(0, Math.trunc(Number(s?.stock) || 0)),
      weight_grams: Math.max(1, Math.trunc(Number(s?.weight_grams) || 250)),
      image_url: typeof s?.image_url === "string" ? s.image_url.trim() : "",
    }));

    // Shop ID is derived on gateway from the authenticated seller JWT, never from client input.
    const data = await gw<{ product: GwSellerProduct }>("/api/v1/seller/products", {
      method: "POST",
      body: {
        category_id: categoryId,
        name,
        ...(slug ? { slug } : {}),
        description: typeof raw?.description === "string" ? raw.description.trim() : "",
        images: Array.isArray(raw?.images)
          ? raw.images.filter((x): x is string => typeof x === "string")
          : [],
        skus,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PRODUCT: "Vui lòng nhập đầy đủ tên sản phẩm, danh mục và ít nhất một biến thể SKU.",
      ALREADY_EXISTS: "Mã SKU hoặc đường dẫn sản phẩm bị trùng. Vui lòng kiểm tra lại.",
      NO_SELLER_SHOP: "Tài khoản người bán chưa có gian hàng hoạt động.",
    },
  }
);
