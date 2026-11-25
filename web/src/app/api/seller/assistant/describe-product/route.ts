import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  normalizeDescribeProduct,
  validateDescribeProductInput,
} from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

export const POST = apiRoute(
  async (req) => {
    const session = await assertSellerSession();
    const rawBody = await readJson<unknown>(req);
    const validated = validateDescribeProductInput(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", "INVALID_PRODUCT_INPUT", validated.reason);
    }

    const upstream = await gw<unknown>("/api/v1/seller/assistant/describe-product", {
      method: "POST",
      body: {
        ...validated.data,
        category: validated.data.category_name,
        shop_id: session.user.shopId,
      },
      timeoutMs: 45_000,
    });

    const normalized = normalizeDescribeProduct(upstream);
    return Response.json(normalized, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PRODUCT_INPUT: "Tên sản phẩm cần tối thiểu 3 ký tự để AI tạo mô tả.",
      rate_limited: "Bạn đã dùng hết lượt tạo mô tả AI trong ngày. Vui lòng thử lại vào ngày mai.",
      unavailable: "Trợ lý viết mô tả sản phẩm đang tạm gián đoạn. Bạn vui lòng thử lại sau.",
    },
  }
);
