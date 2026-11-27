import type {
  GatewaySellerPayoutListResponse,
  GatewaySellerPayoutResponse,
} from "@/lib/gateway-types";
import { GatewayError, errorResponse } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { extractTraceId } from "@/lib/server/trace";
import { assertSellerSession } from "@/features/seller/sellerServer";

const IDEMPOTENCY_KEY_RE = /^[a-zA-Z0-9._:-]{4,128}$/;
const ALLOWED_STATUSES = new Set(["PENDING", "COMPLETED", "REJECTED"]);

export const GET = apiRoute(
  async (req) => {
    await assertSellerSession();
    const q = req.nextUrl.searchParams;
    const status = (q.get("status") ?? "").trim().toUpperCase();
    if (status && !ALLOWED_STATUSES.has(status)) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }

    const data = await gw<GatewaySellerPayoutListResponse>("/api/v1/seller/payouts", {
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
      INVALID_STATUS: "Trạng thái yêu cầu rút tiền không hợp lệ.",
    },
  }
);

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const rawKey = (req.headers.get("idempotency-key") ?? "").trim();
    if (!rawKey || !IDEMPOTENCY_KEY_RE.test(rawKey)) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT", "", "", {
        safeMessage:
          "Thiếu mã chống trùng lặp yêu cầu rút tiền. Vui lòng mở lại hộp thoại rút tiền.",
      });
    }

    const body = await readJson<{ amount_vnd?: unknown }>(req);
    const amount = body?.amount_vnd;
    if (typeof amount !== "number" || !Number.isSafeInteger(amount) || amount <= 0) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT", "", "", {
        safeMessage: "Số tiền rút phải là số nguyên VND lớn hơn 0.",
      });
    }

    try {
      const data = await gw<GatewaySellerPayoutResponse>("/api/v1/seller/payouts", {
        method: "POST",
        idempotencyKey: rawKey,
        body: { amount_vnd: amount },
      });
      return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
    } catch (err) {
      if (err instanceof GatewayError && err.code === "BANK_ACCOUNT_NOT_READY") {
        const resp = errorResponse(
          err,
          {
            BANK_ACCOUNT_NOT_READY:
              "Tài khoản ngân hàng nhận tiền chưa được xác minh hoặc đang trong thời gian khóa bảo mật 24 giờ.",
          },
          extractTraceId(req.headers.get("traceparent"))
        );
        const payload = await resp.json();
        return Response.json(payload, {
          status: 412,
          headers: { "Cache-Control": "no-store" },
        });
      }
      throw err;
    }
  },
  {
    messages: {
      INSUFFICIENT_BALANCE: "Số dư khả dụng hiện không đủ cho số tiền rút bạn yêu cầu.",
      PAYOUT_PENDING_EXISTS:
        "Gian hàng đang có một yêu cầu rút tiền chờ duyệt. Vui lòng chờ xử lý xong trước khi tạo yêu cầu mới.",
      BANK_ACCOUNT_NOT_READY:
        "Tài khoản ngân hàng nhận tiền chưa được xác minh hoặc đang trong thời gian khóa bảo mật 24 giờ.",
    },
  }
);
