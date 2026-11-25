import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwSellerBankAccount } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface VerifyOtpBody {
  otp?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<VerifyOtpBody>(req);
    const otp = typeof raw?.otp === "string" ? raw.otp.replace(/\D+/g, "") : "";
    if (!/^\d{6}$/.test(otp)) {
      throw new GatewayError("invalid", "INVALID_BANK_OTP");
    }

    const data = await gw<{ bank_account: GwSellerBankAccount }>(
      "/api/v1/seller/bank-account/verify-otp",
      {
        method: "POST",
        body: { otp },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_BANK_OTP: "Vui lòng nhập mã OTP gồm 6 chữ số đã gửi về email của bạn.",
    },
  }
);
