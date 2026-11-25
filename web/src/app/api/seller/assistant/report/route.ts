import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSellerSession } from "@/features/seller/sellerServer";
import {
  normalizeSellerReport,
  validateSellerReportInput,
} from "@/features/assistant/assistantTypes";

export const dynamic = "force-dynamic";

export const POST = apiRoute(
  async (req) => {
    const session = await assertSellerSession();
    const rawBody = await readJson<unknown>(req);
    const validated = validateSellerReportInput(rawBody);
    if (!validated.ok) {
      throw new GatewayError("invalid", "INVALID_REPORT_QUESTION", validated.reason);
    }

    const upstream = await gw<unknown>("/api/v1/seller/assistant/report", {
      method: "POST",
      body: {
        question: validated.data.question,
        period: validated.data.period,
        shop_id: session.user.shopId,
      },
      timeoutMs: 45_000,
    });

    const normalized = normalizeSellerReport(upstream, {
      period: validated.data.period,
      question: validated.data.question,
    });

    return Response.json(normalized, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_REPORT_QUESTION: "Vui lòng nhập câu hỏi phân tích từ 3 đến 500 ký tự.",
      rate_limited: "Bạn đã dùng hết lượt phân tích báo cáo AI trong ngày. Vui lòng thử lại sau.",
      unavailable: "Dịch vụ báo cáo AI tạm thời chưa sẵn sàng. Bạn vui lòng thử lại sau.",
    },
  }
);
