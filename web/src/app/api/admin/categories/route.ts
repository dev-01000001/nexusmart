import type { GatewayCategory } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parseEntityId,
  validateAdminCategoryPayload,
} from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("categories.read");
    const rawParentId = (req.nextUrl.searchParams.get("parent_id") ?? "").trim();
    let parentId: string | undefined;
    if (rawParentId) {
      const valid = parseEntityId(rawParentId);
      if (!valid) throw new GatewayError("invalid", "INVALID_PARENT_ID");
      parentId = valid;
    }

    const data = await gw<{ categories: GatewayCategory[] }>("/api/v1/admin/categories", {
      query: { parent_id: parentId },
    });
    return Response.json(
      { categories: data.categories ?? [] },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_PARENT_ID: "Mã danh mục cha không hợp lệ.",
    },
  }
);

export const POST = apiRoute(
  async (req) => {
    await assertCapability("categories.write");
    const raw = await readJson<unknown>(req);
    const parsed = validateAdminCategoryPayload(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<{ category: GatewayCategory }>("/api/v1/admin/categories", {
      method: "POST",
      body: {
        name: parsed.value.name,
        slug: parsed.value.slug,
        parent_id: parsed.value.parent_id,
        commission_rate_bps: parsed.value.commission_rate_bps,
        sort_order: parsed.value.sort_order,
        is_prohibited: parsed.value.is_prohibited,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CATEGORY_NAME: "Tên danh mục cần từ 2 đến 120 ký tự.",
      INVALID_CATEGORY_SLUG: "Slug danh mục chỉ gồm chữ thường không dấu, số và dấu gạch ngang.",
      INVALID_PARENT_ID: "Mã danh mục cha không hợp lệ.",
      INVALID_COMMISSION_BPS: "Phí hoa hồng phải là số nguyên từ 0 đến 5.000 bps (0% đến 50%).",
      INVALID_SORT_ORDER: "Thứ tự hiển thị phải là số nguyên không âm.",
      INVALID_IS_PROHIBITED: "Trạng thái danh mục hàng cấm không hợp lệ.",
    },
  }
);
