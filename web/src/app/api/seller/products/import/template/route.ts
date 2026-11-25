import { gwRaw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { applyProxySecurityHeaders } from "@/lib/server/proxy-security";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidResourceId } from "@/features/seller/sellerValidation";

export const dynamic = "force-dynamic";

export const GET = apiRoute(async (req) => {
  await assertSellerSession();
  const rawCategoryId = (req.nextUrl.searchParams.get("category_id") ?? "").trim();
  const categoryId = rawCategoryId && isValidResourceId(rawCategoryId) ? rawCategoryId : undefined;

  const upstream = await gwRaw("/api/v1/seller/products/import/template", {
    method: "GET",
    query: { category_id: categoryId },
    accept: "text/csv, */*",
  });

  const contentType = upstream.headers.get("content-type") || "text/csv; charset=utf-8";
  const headers = new Headers({
    "Content-Type": contentType,
    "Cache-Control": "no-store",
  });
  const disposition = upstream.headers.get("content-disposition");
  headers.set(
    "Content-Disposition",
    disposition || 'attachment; filename="product-import-template.csv"'
  );
  applyProxySecurityHeaders(headers, upstream.headers, contentType);

  return new Response(upstream.body, {
    status: upstream.status,
    headers,
  });
});
