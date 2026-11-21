import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import {
  validateCategoryInput,
  type AdminCategory,
} from "@/features/admin/adminComplaintsTypes";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const parentId = req.nextUrl.searchParams.get("parent_id") || undefined;
  const data = await gw<{ categories: AdminCategory[] }>("/api/v1/categories", {
    query: { parent_id: parentId },
  });
  return Response.json(
    { categories: data.categories ?? [] },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const POST = apiRoute(
  async (req) => {
    await assertAdminSession();
    const raw = await readJson<unknown>(req);
    const parsed = validateCategoryInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code);
    }

    const data = await gw<{ category: AdminCategory }>("/api/v1/admin/categories", {
      method: "POST",
      body: parsed.value,
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CATEGORY_NAME: "Tên danh mục cần từ 2 đến 120 ký tự.",
      INVALID_CATEGORY_SLUG: "Slug danh mục chỉ gồm chữ thường không dấu, số và dấu gạch ngang.",
      INVALID_COMMISSION_BPS: "Phí hoa hồng phải là số nguyên từ 0 đến 5.000 bps (0% đến 50%).",
      INVALID_SORT_ORDER: "Thứ tự hiển thị phải là số nguyên không âm.",
    },
  }
);
