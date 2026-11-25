import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertRole } from "@/lib/server/session";
import type { GwSellerShop } from "@/features/seller/sellerApiTypes";
import { validateShopRegisterInput } from "@/features/seller/sellerValidation";

export const POST = apiRoute(
  async (req) => {
    await assertRole("buyer", "seller", "admin", "super_admin");
    const raw = await readJson<unknown>(req);
    const validated = validateShopRegisterInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    // Never forward client user_id or shop_id; gateway derives owner_user_id from verified JWT.
    const data = await gw<{ shop: GwSellerShop }>("/api/v1/shops/register", {
      method: "POST",
      body: validated.payload,
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      ALREADY_EXISTS: "Tài khoản này đã đăng ký gian hàng hoặc tên/đường dẫn gian hàng đã tồn tại.",
    },
  }
);
