import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwSellerBankAccount } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface UpsertBankAccountBody {
  bank_code?: unknown;
  bank_name?: unknown;
  account_number?: unknown;
  account_holder_name?: unknown;
}

export const GET = apiRoute(async () => {
  await assertSellerSession();
  const data = await gw<{ bank_account: GwSellerBankAccount | null }>(
    "/api/v1/seller/bank-account"
  );
  return Response.json(
    { bank_account: data.bank_account ?? null },
    { headers: { "Cache-Control": "no-store" } }
  );
});

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<UpsertBankAccountBody>(req);
    const bankCode = typeof raw?.bank_code === "string" ? raw.bank_code.trim().toUpperCase() : "";
    const bankName = typeof raw?.bank_name === "string" ? raw.bank_name.trim() : "";
    const accountNumber =
      typeof raw?.account_number === "string" ? raw.account_number.replace(/\s+/g, "") : "";
    const accountHolderName =
      typeof raw?.account_holder_name === "string" ? raw.account_holder_name.trim() : "";

    if (
      !bankCode ||
      !bankName ||
      !/^\d{6,19}$/.test(accountNumber) ||
      accountHolderName.length < 2
    ) {
      throw new GatewayError("invalid", "INVALID_BANK_ACCOUNT");
    }

    const data = await gw<{ bank_account: GwSellerBankAccount; otp_sent?: boolean }>(
      "/api/v1/seller/bank-account",
      {
        method: "POST",
        body: {
          bank_code: bankCode.slice(0, 32),
          bank_name: bankName.slice(0, 120),
          account_number: accountNumber,
          account_holder_name: accountHolderName.slice(0, 120),
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_BANK_ACCOUNT:
        "Thông tin tài khoản ngân hàng chưa hợp lệ (số tài khoản gồm 6–19 chữ số và tên chủ tài khoản).",
    },
  }
);
