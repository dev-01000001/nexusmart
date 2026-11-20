import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAdminFlashSale } from "@/features/admin/adminApiTypes";

interface CreateFlashSaleBody {
  name?: string;
  starts_at?: string;
  ends_at?: string;
}

export const POST = apiRoute(
  async (req) => {
    await assertAdminSession();
    const raw = await readJson<CreateFlashSaleBody>(req);
    const name = (raw.name ?? "").trim();
    const startsAt = (raw.starts_at ?? "").trim();
    const endsAt = (raw.ends_at ?? "").trim();
    if (!name || !startsAt || !endsAt) {
      throw new GatewayError("invalid", "INVALID_FLASH_SALE");
    }

    const data = await gw<{ flash_sale: GwAdminFlashSale }>("/api/v1/admin/flash-sales", {
      method: "POST",
      body: { name, starts_at: startsAt, ends_at: endsAt },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_FLASH_SALE: "Vui lòng nhập tên khung giờ và thời gian bắt đầu / kết thúc hợp lệ.",
    },
  }
);
