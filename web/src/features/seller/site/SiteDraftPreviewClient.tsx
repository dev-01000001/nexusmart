"use client";

import React, { useState } from "react";
import Link from "next/link";
import { PageHeader, StatusBadge, siteStatusTone } from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import type { SitePublicShopDTO } from "@/lib/site/types";
import type { SiteTemplateConfig, TemplatePageKey } from "@/features/site/templateTypes";
import { PreviewToolbar } from "./PreviewToolbar";
import { SitePreviewContent } from "./SitePreviewContent";
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";

export interface SiteDraftPreviewClientProps {
  status: string;
  statusLabel: string;
  hostDisplay: string;
  activeView: TemplatePageKey;
  draftConfig: SiteTemplateConfig;
  shop: SitePublicShopDTO;
  products: Product[];
  categories: CategoryInfo[];
  vouchers: Voucher[];
  platformOrigin: string;
}

export function SiteDraftPreviewClient({
  status,
  statusLabel,
  hostDisplay,
  activeView,
  draftConfig,
  shop,
  products,
  categories,
  vouchers,
  platformOrigin,
}: SiteDraftPreviewClientProps) {
  const [device, setDevice] = useState<DevicePreviewWidth>("1440");

  return (
    <div className="space-y-4">
      <PageHeader
        title="Xem trước bản nháp"
        backHref="/seller/site"
        badges={
          <>
            <StatusBadge tone={siteStatusTone(status)}>{statusLabel}</StatusBadge>
            <Badge variant="neutral" className="font-mono text-xs">
              {draftConfig.template_id}
            </Badge>
          </>
        }
        subtitle={`Chế độ xem trước bản nháp, chưa hiển thị công khai · ${hostDisplay}`}
        primaryAction={
          <Button variant="outline" size="sm" asChild>
            <Link href="/seller/site">Quay lại trình chỉnh sửa</Link>
          </Button>
        }
      />

      <Card aria-label="Khung xem trước bản nháp site riêng" className="gap-0 overflow-hidden py-0">
        <PreviewToolbar
          activePage={activeView}
          getPageHref={(page) =>
            page === "home" ? "/seller/site/xem-truoc" : `/seller/site/xem-truoc?view=${page}`
          }
          pageNavAriaLabel="Chuyển trang xem trước bản nháp"
          device={device}
          onDeviceChange={setDevice}
        />

        <div className="overflow-hidden bg-muted/60 p-3 sm:p-4">
          <SitePreviewFrame
            device={device}
            title={`Xem trước bản nháp site riêng (${device}px)`}
            hostLabel={hostDisplay}
            showChromeBar={true}
            resetScrollKey={`${draftConfig.template_id}:${activeView}:${device}`}
            viewportHeight={680}
          >
            <SitePreviewContent
              config={draftConfig}
              page={activeView}
              shop={shop}
              products={products}
              categories={categories}
              vouchers={vouchers}
              platformOrigin={platformOrigin}
            />
          </SitePreviewFrame>
        </div>
      </Card>
    </div>
  );
}
