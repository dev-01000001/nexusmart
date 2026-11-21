import React, { Suspense } from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { StateView } from "@/components";
import { buildSiteMetadata } from "@/lib/site/seo";
import { loadSiteContext, loadSiteVouchers } from "@/features/site/siteContext.server";
import { VoucherClient } from "./VoucherClient";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host } = await params;
  const ctx = await loadSiteContext(host);
  if (!ctx) {
    return {
      title: "Không tìm thấy cửa hàng",
      robots: { index: false, follow: false },
    };
  }
  return buildSiteMetadata({
    config: ctx.payload.config,
    pageTitle: "Mã giảm giá",
    description: `Mã ưu đãi riêng đang phát hành tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/voucher",
  });
}

export default async function SiteVoucherPage({ params }: PageProps) {
  const { host } = await params;
  const data = await loadSiteVouchers(host);
  if (!data) notFound();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải mã giảm giá của cửa hàng..." rows={5} />
        </div>
      }
    >
      <VoucherClient brandName={data.brandName} vouchers={data.vouchers} loadError={data.error} />
    </Suspense>
  );
}
