import type { Metadata } from "next";
import React from "react";
import { directoryContext } from "@/features/platform/directory.server";
import { PlatformRegisterShell } from "@/features/platform/PlatformAuthShell";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Đăng ký mở gian hàng",
  description:
    "Tạo tài khoản trên nền tảng NexusMart để mở gian hàng, tùy biến giao diện và bán hàng trên tên miền phụ riêng.",
};

interface PlatformRegisterPageProps {
  searchParams?: Promise<{ role?: string | string[]; ten?: string | string[] }>;
}

function firstParam(raw: string | string[] | undefined): string {
  return (Array.isArray(raw) ? raw[0] : raw)?.trim() ?? "";
}

export default async function PlatformRegisterPage({ searchParams }: PlatformRegisterPageProps) {
  const [params, ctx] = await Promise.all([
    searchParams
      ? searchParams
      : Promise.resolve<{ role?: string | string[]; ten?: string | string[] }>({}),
    directoryContext(),
  ]);
  const roleParam = firstParam(params.role).toLowerCase();
  const isSellerIntent = roleParam !== "buyer";
  const suggestedShopName = firstParam(params.ten).slice(0, 64);

  return (
    <div className={styles.page}>
      <PlatformRegisterShell
        initialRole={isSellerIntent ? "seller" : "buyer"}
        suggestedShopName={suggestedShopName}
        rootDomain={ctx.rootDomain}
      />
    </div>
  );
}
