import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAdminFlashSaleExtended } from "@/features/admin/adminApiTypes";
import { validateCreateFlashSaleInput } from "@/features/admin/AdminFlashSaleHelpers";

const ALLOWED_FLASH_SALE_STATUSES = new Set(["UPCOMING", "ACTIVE", "ENDED"]);

export const GET = apiRoute(
  async (req) => {
    await assertCapability("flash_sales.read");
    const q = req.nextUrl.searchParams;
    const status = (q.get("status") ?? "").trim().toUpperCase();
    if (status && !ALLOWED_FLASH_SALE_STATUSES.has(status)) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }

    const data = await gw<{
      items: GwAdminFlashSaleExtended[];
      page: number;
      page_size: number;
      total: number;
    }>("/api/v1/admin/flash-sales", {
      query: {
        status: status || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STATUS: "Trạng thái khung giờ Flash sale không hợp lệ.",
    },
  }
);

export const POST = apiRoute(async (req) => {
  await assertCapability("flash_sales.write");
  const raw = await readJson<unknown>(req);
  const validated = validateCreateFlashSaleInput(raw);
  if (!validated.ok) {
    throw new GatewayError("invalid", validated.code, "", "", {
      safeMessage: validated.message,
    });
  }

  const data = await gw<{ flash_sale: GwAdminFlashSaleExtended }>("/api/v1/admin/flash-sales", {
    method: "POST",
    body: validated.value,
  });
  return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
});
