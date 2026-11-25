import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { Button } from "@/components";
import { loadSiteContext } from "@/features/site/siteContext.server";
import shared from "@/features/commerce/commerce.module.css";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
}

function isMockZaloPayEnabled(): boolean {
  const enableFlag = (process.env.ENABLE_MOCK_ZALOPAY ?? "").trim().toLowerCase();
  if (enableFlag === "false" || enableFlag === "0") {
    return false;
  }
  const zaloMode = (process.env.ZALOPAY_MODE ?? "").trim().toLowerCase();
  if (zaloMode && zaloMode !== "mock") {
    return false;
  }
  return true;
}

export async function generateMetadata(): Promise<Metadata> {
  return {
    title: "Trang mô phỏng thanh toán",
    robots: { index: false, follow: false },
  };
}

/**
 * Neutral payment simulation notice page for local/staging testing.
 * Strictly never reflects URL query parameters and returns 404 in production/live ZaloPay mode.
 */
export default async function SiteMockZaloPayPage({ params }: PageProps) {
  if (!isMockZaloPayEnabled()) {
    notFound();
  }
  const { host } = await params;
  const ctx = await loadSiteContext(host);
  if (!ctx) {
    notFound();
  }

  return (
    <div className={shared.page}>
      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={shared.pageTitle}>Cổng thanh toán mô phỏng</h1>
          <p className={shared.pageLead}>{ctx.brandName}</p>
        </div>
      </section>

      <section className={shared.section}>
        <div className={shared.surfacePanel} role="status">
          <p className={`${shared.feedbackBox} ${shared.feedbackWarn}`}>
            <strong>Trang mô phỏng, không phải trang ZaloPay thật.</strong> Không nhập mật khẩu, mã
            OTP hay thông tin thẻ ngân hàng trên trang này.
          </p>
          <p className={shared.fieldHint} style={{ marginTop: 12 }}>
            Trạng thái thanh toán thực tế được xác nhận tự động qua hệ thống sau khi giao dịch được
            ghi nhận. Bạn có thể quay lại trang chờ thanh toán hoặc kiểm tra mục Đơn hàng của tôi.
          </p>
          <div className={shared.formActions} style={{ marginTop: 16 }}>
            <Button variant="primary" size="md" href="/don-hang">
              Xem đơn hàng của tôi
            </Button>
            <Button variant="secondary" size="md" href="/san-pham">
              Quay lại cửa hàng
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}
