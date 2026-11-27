import type { Metadata } from "next";
import React from "react";
import { redirect } from "next/navigation";
import { gw } from "@/lib/server/gateway";
import { getSession } from "@/lib/server/session";
import { LoginForm } from "@/features/account/AuthForms";
import { resolvePostLoginPath } from "@/features/account/authRedirect";
import { PlatformAuthFrame } from "@/features/platform/auth/PlatformAuthFrame";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Đăng nhập",
  description: "Đăng nhập Kênh người bán, Cổng quản trị hoặc tài khoản trên nền tảng NexusMart.",
};

export default async function PlatformLoginPage({
  searchParams,
}: {
  searchParams?: Promise<{ next?: string | string[]; expired?: string | string[] }>;
}) {
  const [session, sp] = await Promise.all([
    getSession().catch(() => null),
    searchParams ?? Promise.resolve<{ next?: string | string[]; expired?: string | string[] }>({}),
  ]);

  const expiredParam = Array.isArray(sp?.expired) ? sp.expired[0] : sp?.expired;
  if (session && expiredParam !== "1") {
    const rawNext = Array.isArray(sp?.next) ? sp.next[0] : sp?.next;
    let hasShop = Boolean(session.user.shopId);
    if (!hasShop && session.user.role === "buyer") {
      const myShop = await gw<{ shop?: { id?: string } | null }>("/api/v1/shops/me")
        .then((res) => res.shop ?? null)
        .catch(() => null);
      hasShop = Boolean(myShop?.id);
    }
    redirect(resolvePostLoginPath("platform", session.user.role, rawNext, "/", { hasShop }));
  }

  return (
    <PlatformAuthFrame mode="login" title="Đăng nhập" initialRole="seller">
      <LoginForm variant="platform" buyerHref="/" registerHref="/dang-ky?role=seller" />
    </PlatformAuthFrame>
  );
}
