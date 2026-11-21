import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import {
  validateCategoryInput,
  type AdminCategory,
} from "@/features/admin/adminComplaintsTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertAdminSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_CATEGORY_ID");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateCategoryInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code);
    }
    if (parsed.value.parent_id && parsed.value.parent_id === cleanId) {
      throw new GatewayError("invalid", "SELF_PARENT_CATEGORY");
    }

    const data = await gw<{ category: AdminCategory }>(
      `/api/v1/admin/categories/${encodeURIComponent(cleanId)}`,
      {
        method: "PUT",
        body: parsed.value,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CATEGORY_ID: "Mã danh mục không hợp lệ.",
      SELF_PARENT_CATEGORY: "Danh mục không thể chọn chính nó làm danh mục cha.",
      INVALID_CATEGORY_NAME: "Tên danh mục cần từ 2 đến 120 ký tự.",
      INVALID_CATEGORY_SLUG: "Slug danh mục chỉ gồm chữ thường không dấu, số và dấu gạch ngang.",
      INVALID_COMMISSION_BPS: "Phí hoa hồng phải là số nguyên từ 0 đến 5.000 bps (0% đến 50%).",
      INVALID_SORT_ORDER: "Thứ tự hiển thị phải là số nguyên không âm.",
    },
  }
);

export const DELETE = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertAdminSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_CATEGORY_ID");
    }

    const data = await gw<{ ok: boolean }>(
      `/api/v1/admin/categories/${encodeURIComponent(cleanId)}`,
      {
        method: "DELETE",
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CATEGORY_ID: "Mã danh mục không hợp lệ.",
      FAILED_PRECONDITION: "Không thể xóa danh mục đang có sản phẩm hoặc danh mục con.",
    },
  }
);
