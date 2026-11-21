import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface MarkReadBody {
  notification_ids?: unknown;
  mark_all?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const body = await readJson<MarkReadBody>(req);
    const markAll = Boolean(body?.mark_all);
    const notificationIds = Array.isArray(body?.notification_ids)
      ? body.notification_ids
          .filter((item): item is string => typeof item === "string" && item.trim().length > 0)
          .map((item) => item.trim())
          .slice(0, 100)
      : [];

    if (!markAll && notificationIds.length === 0) {
      throw new GatewayError("invalid", "INVALID_NOTIFICATION_IDS");
    }

    const res = await gw<{ updated_count?: number; unread_count?: number }>(
      "/api/v1/notifications/read",
      {
        method: "POST",
        body: {
          notification_ids: notificationIds,
          mark_all: markAll,
        },
      }
    );

    return Response.json(
      {
        updated_count: Number(res?.updated_count) || 0,
        unread_count: Number(res?.unread_count) || 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_NOTIFICATION_IDS: "Vui lòng chọn ít nhất một thông báo để đánh dấu đã đọc.",
    },
  }
);
