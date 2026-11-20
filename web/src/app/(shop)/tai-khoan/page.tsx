import React from "react";
import { redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { loginPath, requireSession } from "@/lib/server/session";
import { fetchAccountPageData } from "@/features/account/account.server";
import { AccountClientView } from "@/features/account/AccountClientView";

export const dynamic = "force-dynamic";

export default async function AccountPage() {
  const session = await requireSession("/tai-khoan");

  try {
    const data = await fetchAccountPageData(session);
    return (
      <AccountClientView
        initialProfile={data.profile}
        initialAddresses={data.addresses}
        initialOrders={data.orders}
        totalOrders={data.totalOrders}
        initialVouchers={data.vouchers}
        provinces={data.provinces}
        initialWards={data.initialWards}
        initialProvinceCode={data.initialProvinceCode}
        shops={data.shops}
        catalogProducts={data.catalogProducts}
        suggestedProducts={data.suggestedProducts}
      />
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      redirect(loginPath("/tai-khoan"));
    }
    const msg = err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return (
      <AccountClientView
        initialProfile={{
          id: session.user.id,
          fullName: session.user.fullName,
          email: session.user.email,
          phone: session.user.phone,
          role: session.user.role,
          shopId: session.user.shopId,
          avatarUrl: session.user.avatarUrl,
          phoneVerified: session.user.phoneVerified,
          memberSince: "",
          coins: 0,
        }}
        initialAddresses={[]}
        initialOrders={[]}
        totalOrders={0}
        initialVouchers={[]}
        provinces={[]}
        initialWards={[]}
        initialProvinceCode="79"
        shops={[]}
        catalogProducts={[]}
        suggestedProducts={[]}
        initialError={msg}
      />
    );
  }
}
