import type { GatewayCategory } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parseEntityId,
  validateAdminCategoryPayload,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("categories.write");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_CATEGORY_ID");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateAdminCategoryPayload(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }
    if (parsed.value.parent_id && parsed.value.parent_id === cleanId) {
      throw new GatewayError("invalid", "SELF_PARENT_CATEGORY");
    }

    const data = await gw<{ category: GatewayCategory }>(
      `/api/v1/admin/categories/${encodeURIComponent(cleanId)}`,
      {
        method: "PUT",
        body: {
          name: parsed.value.name,
          slug: parsed.value.slug,
          parent_id: parsed.value.parent_id,
          commission_rate_bps: parsed.value.commission_rate_bps,
          sort_order: parsed.value.sort_order,
          is_prohibited: parsed.value.is_prohibited,
        },
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
      INVALID_PARENT_ID: "Mã danh mục cha không hợp lệ.",
      INVALID_COMMISSION_BPS: "Phí hoa hồng phải là số nguyên từ 0 đến 5.000 bps (0% đến 50%).",
      INVALID_SORT_ORDER: "Thứ tự hiển thị phải là số nguyên không âm.",
      INVALID_IS_PROHIBITED: "Trạng thái danh mục hàng cấm không hợp lệ.",
    },
  }
);

export const DELETE = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("categories.write");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
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
