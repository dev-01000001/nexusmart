import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { headers } from "next/headers";
import { StateView } from "@/components";
import { APP_ORIGINS } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { loadShopSiteRenderData } from "@/lib/server/sites";
import { deriveMarketplaceOrigin } from "@/lib/site/host";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { SiteRenderer, SiteShell } from "@/features/site";
import { requireSellerPage } from "@/features/seller/sellerServer";
import { loadSellerSiteServer } from "@/features/seller/site/siteServer";
import { createDefaultSiteConfig, getSiteStatusMeta } from "@/features/seller/site/siteEditorUtils";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Xem trước bản nháp site riêng",
  robots: {
    index: false,
    follow: false,
  },
};

export default async function SellerSiteDraftPreviewPage() {
  // Strictly server-protected: only the authenticated shop owner can view their own shop's draft.
  // Never reads or accepts any `shop_id` query parameter.
  const session = await requireSellerPage();
  const siteRes = await loadSellerSiteServer();

  if (siteRes.error) {
    return (
      <div className={styles.previewWrap}>
        <div className={styles.previewBanner}>
          <div className={styles.bannerLeft}>
            <span className={styles.bannerTitle}>
              Chế độ xem trước bản nháp — chưa hiển thị công khai
            </span>
          </div>
          <div className={styles.bannerActions}>
            <Link href="/seller/site" className={styles.backLink}>
              Quay lại trình chỉnh sửa
            </Link>
          </div>
        </div>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải bản nháp site riêng"
          message={siteRes.error}
          actionLabel="Quay lại trình chỉnh sửa"
          actionHref="/seller/site"
        />
      </div>
    );
  }

  const { site, shop, rootDomain } = siteRes;

  // Defense in depth: ensure returned site strictly matches authenticated seller's shop_id.
  if (!site || !session.user.shopId || site.shop_id !== session.user.shopId) {
    return (
      <div className={styles.previewWrap}>
        <div className={styles.previewBanner}>
          <div className={styles.bannerLeft}>
            <span className={styles.bannerTitle}>
              Chế độ xem trước bản nháp — chưa hiển thị công khai
            </span>
          </div>
          <div className={styles.bannerActions}>
            <Link href="/seller/site" className={styles.backLink}>
              Quay lại trình chỉnh sửa
            </Link>
          </div>
        </div>
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có bản nháp site riêng"
          message="Hãy chọn tên miền phụ, chỉnh bảng màu và bấm Lưu nháp trong mục Site riêng trước khi xem trước."
          actionLabel="Mở trình chỉnh sửa Site riêng"
          actionHref="/seller/site"
        />
      </div>
    );
  }

  const shopName = shop?.name || site.shop_name || "Gian hàng của bạn";
  const publicShop: SitePublicShopDTO = {
    id: site.shop_id,
    name: shopName,
    description: shop?.description || "",
    status: shop?.status || "ACTIVE",
  };

  const draftConfig = site.draft_config ?? createDefaultSiteConfig(shopName, shop?.description);
  const renderData = await loadShopSiteRenderData(publicShop, { uncached: true });

  const hdrs = await headers();
  const rawHost = resolveRequestHost(hdrs, `${rootDomain}`);
  const proto = resolveRequestProto(hdrs, `http://${rawHost}`);
  const marketplaceOrigin = deriveMarketplaceOrigin({
    rawHost,
    proto,
    rootDomain,
    appOrigin: APP_ORIGINS[0] || "",
  });

  const statusMeta = getSiteStatusMeta(site.status);
  const hostDisplay = site.primary_host || `${site.subdomain}.${rootDomain}`;

  return (
    <div className={styles.previewWrap}>
      <div className={styles.previewBanner} role="region" aria-label="Thanh xem trước bản nháp">
        <div className={styles.bannerLeft}>
          <span className={styles.bannerBadge}>{statusMeta.label}</span>
          <span className={styles.bannerTitle}>
            Chế độ xem trước bản nháp — chưa hiển thị công khai
          </span>
          <span className={styles.bannerHost}>({hostDisplay})</span>
        </div>
        <div className={styles.bannerActions}>
          <Link href="/seller/site" className={styles.backLink}>
            Quay lại trình chỉnh sửa
          </Link>
        </div>
      </div>

      <div className={styles.previewFrame}>
        <SiteShell
          config={draftConfig}
          shopName={shopName}
          shopId={publicShop.id}
          siteHost={hostDisplay}
          activeNav="home"
          marketplaceOrigin={marketplaceOrigin}
          previewMode={true}
        >
          <SiteRenderer
            config={draftConfig}
            shop={publicShop}
            products={renderData.products}
            vouchers={renderData.vouchers}
            categories={renderData.categories}
            marketplaceOrigin={marketplaceOrigin}
            siteHost={hostDisplay}
            previewMode={true}
          />
        </SiteShell>
      </div>
    </div>
  );
}
