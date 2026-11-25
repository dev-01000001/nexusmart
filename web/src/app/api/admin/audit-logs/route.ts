import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAuditLog } from "@/features/admin/adminApiTypes";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";
import { normalizeAuditTimeParam } from "@/features/admin/AdminAuditHelpers";

const TARGET_TYPE_RE = /^[a-z0-9_]{1,40}$/;

export const GET = apiRoute(
  async (req) => {
    await assertCapability("audit_logs.read");
    const q = req.nextUrl.searchParams;

    const actorId = (q.get("actor_id") ?? "").trim();
    const targetId = (q.get("target_id") ?? "").trim();
    const targetType = (q.get("target_type") ?? "").trim().toLowerCase();

    if (actorId && !isValidAdminPathId(actorId)) {
      throw new GatewayError("invalid", "INVALID_ACTOR_ID");
    }
    if (targetId && !isValidAdminPathId(targetId)) {
      throw new GatewayError("invalid", "INVALID_TARGET_ID");
    }
    if (targetType && !TARGET_TYPE_RE.test(targetType)) {
      throw new GatewayError("invalid", "INVALID_TARGET_TYPE");
    }

    const rawFrom = q.get("from_time") ?? q.get("from");
    const rawTo = q.get("to_time") ?? q.get("to");
    const fromTime = normalizeAuditTimeParam(rawFrom, "start");
    const toTime = normalizeAuditTimeParam(rawTo, "end");
    if (fromTime === null || toTime === null) {
      throw new GatewayError("invalid", "INVALID_TIME_RANGE");
    }
    if (fromTime && toTime && Date.parse(fromTime) > Date.parse(toTime)) {
      throw new GatewayError("invalid", "INVALID_TIME_RANGE");
    }

    const data = await gw<{ items: GwAuditLog[]; page: number; page_size: number; total: number }>(
      "/api/v1/admin/audit-logs",
      {
        query: {
          actor_id: actorId || undefined,
          target_type: targetType || undefined,
          target_id: targetId || undefined,
          from_time: fromTime || undefined,
          to_time: toTime || undefined,
          page: q.get("page"),
          page_size: q.get("page_size"),
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ACTOR_ID: "Mã người thực hiện không đúng định dạng.",
      INVALID_TARGET_ID: "Mã đối tượng kiểm toán không đúng định dạng.",
      INVALID_TARGET_TYPE: "Loại đối tượng kiểm toán không hợp lệ.",
      INVALID_TIME_RANGE: "Khoảng thời gian lọc nhật ký kiểm toán chưa hợp lệ.",
    },
  }
);
