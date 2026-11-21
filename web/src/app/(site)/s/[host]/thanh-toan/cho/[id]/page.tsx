import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { ADMIN_ROLES, requireSession } from "@/lib/server/session";
import { belongsToStorefront } from "@/lib/server/storefront";
import { buildSiteMetadata } from "@/lib/site/seo";
import type { GwPayment } from "@/features/commerce/types";
import { loadSiteContext } from "@/features/site/siteContext.server";
import { PaymentWaitingClient } from "./PaymentWaitingClient";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string; id: string }>;
  searchParams: Promise<{ order?: string }>;
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
    pageTitle: "Thanh toán VietQR",
    siteOrigin: ctx.siteOrigin,
  });
}

export default async function SitePaymentWaitingPage({ params, searchParams }: PageProps) {
  const [{ host, id }, { order }] = await Promise.all([params, searchParams]);
  const ctx = await loadSiteContext(host);
  if (!ctx) notFound();

  const session = await requireSession(`/thanh-toan/cho/${encodeURIComponent(id)}`);
  const shopId = ctx.payload.site.shop_id;

  let initialPayment: GwPayment | null = null;
  let initialError: string | null = null;

  try {
    const res = await gw<{ payment: GwPayment }>(`/api/v1/payments/${encodeURIComponent(id)}`);
    const payment = res.payment ?? null;
    if (!payment || !belongsToStorefront(payment, shopId)) {
      notFound();
    }
    if (
      payment.user_id &&
      payment.user_id !== session.user.id &&
      !ADMIN_ROLES.includes(session.user.role)
    ) {
      notFound();
    }
    initialPayment = payment;
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.kind === "not_found" || err.kind === "forbidden") {
        notFound();
      }
      initialError = SAFE_MESSAGES[err.kind];
    } else {
      initialError = SAFE_MESSAGES.unavailable;
    }
  }

  return (
    <PaymentWaitingClient
      brandName={ctx.brandName}
      paymentId={id}
      initialPayment={initialPayment}
      fallbackOrderId={order ?? ""}
      initialError={initialError}
    />
  );
}
