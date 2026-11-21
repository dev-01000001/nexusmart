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
import type { TemplatePageKey } from "@/features/site/templateTypes";
import { requireSellerPage } from "@/features/seller/sellerServer";
import { loadSellerSiteServer } from "@/features/seller/site/siteServer";
import {
  TEMPLATE_PAGE_TABS,
  createDefaultTemplateConfig,
  getSiteStatusMeta,
} from "@/features/seller/site/siteEditorUtils";
import { SitePreviewContent } from "@/features/seller/site/SitePreviewContent";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Xem trước bản nháp site riêng",
  robots: {
    index: false,
    follow: false,
  },
};

const VALID_VIEWS = new Set<TemplatePageKey>(["home", "catalog", "pdp", "cart", "info"]);

interface SellerSiteDraftPreviewPageProps {
  searchParams?: Promise<{ view?: string | string[] }>;
}

export default async function SellerSiteDraftPreviewPage({
  searchParams,
}: SellerSiteDraftPreviewPageProps) {
  // Strictly server-protected: only the authenticated shop owner can view their own shop's draft.
  // Never reads or accepts any `shop_id` query parameter.
  const session = await requireSellerPage();
  const [siteRes, sp] = await Promise.all([
    loadSellerSiteServer(),
    searchParams ?? Promise.resolve<{ view?: string | string[] }>({}),
  ]);

  const rawView = Array.isArray(sp.view) ? sp.view[0] : sp.view;
  const activeView: TemplatePageKey =
    rawView && VALID_VIEWS.has(rawView as TemplatePageKey) ? (rawView as TemplatePageKey) : "home";

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
          message="Hãy chọn mẫu giao diện, tên miền phụ, chỉnh bảng màu và bấm Lưu nháp trong mục Site riêng trước khi xem trước."
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

  const draftConfig = site.draft_config ?? createDefaultTemplateConfig(shopName, shop?.description);
  const renderData = await loadShopSiteRenderData(publicShop, { uncached: true });

  const hdrs = await headers();
  const rawHost = resolveRequestHost(hdrs, `${rootDomain}`);
  const proto = resolveRequestProto(hdrs, `http://${rawHost}`);
  const platformOrigin = deriveMarketplaceOrigin({
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
          <span className={styles.bannerHost}>
            ({hostDisplay} · Mẫu: {draftConfig.template_id})
          </span>
        </div>

        <nav className={styles.viewSwitcher} aria-label="Chuyển trang xem trước bản nháp">
          {TEMPLATE_PAGE_TABS.map((tab) => {
            const href =
              tab.key === "home"
                ? "/seller/site/xem-truoc"
                : `/seller/site/xem-truoc?view=${tab.key}`;
            const isActive = activeView === tab.key;
            return (
              <Link
                key={tab.key}
                href={href}
                className={`${styles.viewLink} ${isActive ? styles.viewLinkActive : ""}`}
                aria-current={isActive ? "page" : undefined}
              >
                {tab.shortLabel}
              </Link>
            );
          })}
        </nav>

        <div className={styles.bannerActions}>
          <Link href="/seller/site" className={styles.backLink}>
            Quay lại trình chỉnh sửa
          </Link>
        </div>
      </div>

      <div className={styles.previewFrame}>
        <SitePreviewContent
          config={draftConfig}
          page={activeView}
          shop={publicShop}
          products={renderData.products}
          categories={renderData.categories}
          vouchers={renderData.vouchers}
          platformOrigin={platformOrigin}
        />
      </div>
    </div>
  );
}
