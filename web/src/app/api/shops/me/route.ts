import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GwSellerShop } from "@/features/seller/sellerApiTypes";

export const GET = apiRoute(async () => {
  await assertSession();
  try {
    const data = await gw<{ shop: GwSellerShop | null }>("/api/v1/shops/me");
    return Response.json({ shop: data.shop ?? null }, { headers: { "Cache-Control": "no-store" } });
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return Response.json(
        { error: { code: "NOT_FOUND", message: "Tài khoản chưa đăng ký gian hàng." } },
        { status: 404, headers: { "Cache-Control": "no-store" } }
      );
    }
    throw err;
  }
});
