import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { AlertTriangle } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { SitePageFrame, SitePageHeader } from "@/features/commerce/ui/SitePageFrame";
import { loadSiteContext } from "@/features/site/siteContext.server";

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
    <SitePageFrame narrow>
      <SitePageHeader title="Cổng thanh toán mô phỏng" subtitle={ctx.brandName} />

      <Card className="border border-border shadow-none" role="status">
        <CardContent className="space-y-4">
          <Alert className="border-badge-warning-ink/25 bg-badge-warning-bg text-badge-warning-ink shadow-none">
            <AlertTriangle className="size-4" aria-hidden="true" />
            <AlertDescription className="text-badge-warning-ink">
              <strong className="font-semibold">
                Trang mô phỏng, không phải trang ZaloPay thật.
              </strong>{" "}
              Không nhập mật khẩu, mã OTP hay thông tin thẻ ngân hàng trên trang này.
            </AlertDescription>
          </Alert>

          <p className="text-sm leading-relaxed text-muted-foreground">
            Trạng thái thanh toán thực tế được xác nhận tự động qua hệ thống sau khi giao dịch được
            ghi nhận. Bạn có thể quay lại trang chờ thanh toán hoặc kiểm tra mục Đơn hàng của tôi.
          </p>

          <div className="flex flex-wrap items-center gap-2.5 pt-1">
            <Button asChild className="min-h-10 shadow-none">
              <Link href="/don-hang">Xem đơn hàng của tôi</Link>
            </Button>
            <Button asChild variant="outline" className="min-h-10 shadow-none">
              <Link href="/san-pham">Quay lại cửa hàng</Link>
            </Button>
          </div>
        </CardContent>
      </Card>
    </SitePageFrame>
  );
}
