import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import {
  normalizeComplaint,
  validateComplaintResolution,
} from "@/features/admin/adminComplaintsTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertAdminSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_COMPLAINT_ID");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateComplaintResolution(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code);
    }

    const data = await gw<Record<string, unknown>>(
      `/api/v1/admin/complaints/${encodeURIComponent(cleanId)}/resolve`,
      {
        method: "POST",
        body: parsed.value,
      }
    );

    const complaint = data.complaint ? normalizeComplaint(data.complaint) : null;
    return Response.json(
      { ...data, ...(complaint ? { complaint } : {}) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_COMPLAINT_ID: "Mã khiếu nại không hợp lệ.",
      INVALID_RESOLUTION: "Quyết định phân xử không hợp lệ.",
      INVALID_RETURN_SHIPPING: "Tùy chọn yêu cầu gửi trả hàng không hợp lệ.",
      NOTE_TOO_SHORT: "Ghi chú phân xử cần tối thiểu 5 ký tự.",
      NOTE_TOO_LONG: "Ghi chú phân xử không được vượt quá 2.000 ký tự.",
    },
  }
);
