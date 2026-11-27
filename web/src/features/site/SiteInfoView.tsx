import React from "react";
import Link from "next/link";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { Voucher } from "@/data/vouchers";
import type { PolicyItem, SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { CollapsibleProse } from "./CollapsibleProse";
import { SiteProductCard } from "./SiteProductCard";
import { SiteRenderer } from "./SiteRenderer";
import {
  SITE_PRIMARY_BTN_CLS,
  SITE_SECONDARY_BTN_CLS,
  SITE_SECTION_LINK_CLS,
} from "./sections/SiteSectionBlocks";
import { archetypeTitleTone, containerWidthClass, SITE_PX } from "./shell/siteStyleVars";
import type { InfoPageConfig, SiteCardStyle, SiteTemplateConfig } from "./templateTypes";
import { sanitizeSafeImageUrl } from "./templateValidator";

export interface SiteInfoViewProps {
  mode: "about" | "policies";
  templateConfig: SiteTemplateConfig;
  shop: SitePublicShopDTO;
  shopProfile: Shop | null;
  products: Product[];
  vouchers?: Voucher[];
  previewMode?: boolean;
}

export function SiteInfoView({
  mode,
  templateConfig,
  shop,
  shopProfile,
  products,
  vouchers = [],
  previewMode = false,
}: SiteInfoViewProps) {
  const brandName = templateConfig.theme.brand_name?.trim() || shop.name;
  const infoConfig: InfoPageConfig = templateConfig.pages.info;
  const layout = infoConfig.layout;
  const archetype = templateConfig.layout.archetype;
  const cardStyle: SiteCardStyle = templateConfig.layout.card_style;
  const containerCls = containerWidthClass(templateConfig.layout.container_width);
  const titleTone = archetypeTitleTone(archetype);

  const textSections = templateConfig.sections.filter(
    (s): s is Extract<(typeof templateConfig.sections)[number], { type: "text" }> =>
      s.visible !== false && s.type === "text"
  );

  const configuredPolicies: PolicyItem[] = templateConfig.sections
    .filter(
      (s): s is Extract<(typeof templateConfig.sections)[number], { type: "policies" }> =>
        s.visible !== false && s.type === "policies"
    )
    .flatMap((s) => s.props.items);

  const fallbackPolicies: PolicyItem[] = [
    {
      heading: "Đổi trả và hoàn tiền",
      detail:
        shopProfile?.returnPolicy ||
        "Hỗ trợ đổi trả trong thời hạn quy định với sản phẩm còn nguyên vẹn bao bì và phụ kiện.",
    },
    {
      heading: "Giao hàng và đồng kiểm",
      detail:
        shopProfile?.fastDeliveryNote ||
        "Giao hàng toàn quốc qua đối tác vận chuyển NexusMart, đồng kiểm khi nhận.",
    },
    {
      heading: "Thanh toán",
      detail: "Hỗ trợ thanh toán khi nhận hàng (COD) hoặc chuyển khoản VietQR.",
    },
    {
      heading: "Kho xuất hàng",
      detail: shopProfile?.warehouseAddress || `Xuất trực tiếp từ kho chính hãng ${brandName}.`,
    },
  ];

  const policyItems = configuredPolicies.length > 0 ? configuredPolicies : fallbackPolicies;
  const bannerImg =
    sanitizeSafeImageUrl(templateConfig.theme.banner_url) ||
    sanitizeSafeImageUrl(products[0]?.onModelImage) ||
    "/banners/vietnam-workshop.jpg";
  const showcaseProducts = products.slice(0, 4);

  const leadCopy =
    mode === "about"
      ? templateConfig.theme.tagline?.trim() ||
        shop.description ||
        shopProfile?.story ||
        `${brandName} cung cấp sản phẩm chính hãng, minh bạch thông tin và tồn kho.`
      : `Giao nhận, đổi trả và bảo vệ đơn hàng tại ${brandName}.`;

  const titleCopy = mode === "about" ? `Về ${brandName}` : `Chính sách ${brandName}`;

  const factoryEntries = shopProfile
    ? [
        shopProfile.city
          ? {
              heading: "Kho xuất hàng",
              detail: shopProfile.warehouseAddress || shopProfile.city,
            }
          : null,
        shopProfile.specialty ? { heading: "Thế mạnh", detail: shopProfile.specialty } : null,
        shopProfile.factoryProfile?.fiberOrigin
          ? { heading: "Nguồn nguyên liệu", detail: shopProfile.factoryProfile.fiberOrigin }
          : null,
        shopProfile.returnPolicy ? { heading: "Hậu mãi", detail: shopProfile.returnPolicy } : null,
      ].filter((x): x is { heading: string; detail: string } => x !== null)
    : [];

  const tocEntries = [
    { id: "info-overview", label: mode === "about" ? "Tổng quan" : "Quy định chung" },
    ...(mode === "about" && textSections.length > 0
      ? [{ id: "info-story", label: "Câu chuyện" }]
      : []),
    ...(mode === "policies" ? [{ id: "info-policies", label: "Chi tiết" }] : []),
    ...(infoConfig.show_factory_profile && factoryEntries.length > 0
      ? [{ id: "info-factory", label: "Thông tin cửa hàng" }]
      : []),
    ...(infoConfig.show_showcase && showcaseProducts.length > 0
      ? [{ id: "info-showcase", label: "Tiêu biểu" }]
      : []),
  ];

  const showcaseGridClass =
    showcaseProducts.length <= 1
      ? "grid grid-cols-1"
      : showcaseProducts.length === 2
        ? "grid grid-cols-1 gap-[clamp(20px,3vw,36px)] sm:grid-cols-2"
        : showcaseProducts.length === 3
          ? "grid grid-cols-2 gap-3 lg:grid-cols-3 lg:gap-[var(--site-gutter,24px)]"
          : "grid grid-cols-2 gap-3 lg:grid-cols-4 lg:gap-[var(--site-gutter,24px)]";

  const renderHeaderHero = () => {
    if (layout === "athletic-manifesto") {
      return (
        <section
          id="info-overview"
          className={cn("w-full pt-[clamp(36px,4.5vw,64px)] pb-4", SITE_PX)}
        >
          <div className={containerCls}>
            <div className="flex flex-col gap-4 rounded-lg bg-[var(--site-band-bg)] p-[clamp(28px,4.5vw,56px)] text-[var(--site-band-ink)]">
              <h1
                className={cn(
                  "text-[clamp(34px,4.6vw,64px)] leading-[1.06] text-inherit",
                  titleTone
                )}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className="max-w-[720px] text-[17px] leading-[1.55] text-inherit opacity-90"
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
              <div className="mt-1 flex flex-wrap gap-3">
                {previewMode ? (
                  <span className={SITE_PRIMARY_BTN_CLS}>Xem sản phẩm</span>
                ) : (
                  <Link href="/san-pham" className={SITE_PRIMARY_BTN_CLS}>
                    Xem sản phẩm
                  </Link>
                )}
              </div>
            </div>
          </div>
        </section>
      );
    }

    if (layout === "luxury-maison") {
      return (
        <section id="info-overview" className={cn("w-full py-[clamp(40px,5vw,76px)]", SITE_PX)}>
          <div className={containerCls}>
            <div className="mx-auto flex max-w-[720px] flex-col items-center gap-5 text-center">
              <h1
                className="max-w-[24ch] text-[clamp(26px,3vw,42px)] leading-[1.14] font-normal tracking-[0.02em] text-foreground"
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className="max-w-[46ch] text-[15px] leading-[1.6] text-muted-foreground"
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
              {mode === "about" && (
                <figure
                  className="aspect-[4/5] h-[clamp(300px,52svh,480px)] w-auto max-w-[min(440px,78vw)] overflow-hidden rounded-lg bg-card p-[clamp(10px,1.4vw,18px)]"
                  data-reveal="scale"
                >
                  <img
                    src={bannerImg}
                    alt={`Không gian ${brandName}`}
                    width={1365}
                    height={768}
                    className="block size-full object-cover object-[center_22%]"
                  />
                </figure>
              )}
            </div>
          </div>
        </section>
      );
    }

    if (mode === "policies" || layout === "compact-faq" || layout === "b2b-catalog-sheet") {
      return (
        <section
          id="info-overview"
          className={cn("w-full pt-[clamp(28px,3.8vw,48px)] pb-[clamp(20px,2.5vw,30px)]", SITE_PX)}
        >
          <div className={containerCls}>
            <div className="max-w-[720px]">
              <h1
                className={cn("text-[clamp(32px,4.4vw,56px)] text-foreground", titleTone)}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className="mt-2.5 text-[17px] leading-[1.55] text-muted-foreground"
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
            </div>
          </div>
        </section>
      );
    }

    // Large statement + single clean photograph
    return (
      <section id="info-overview" className={cn("w-full py-[clamp(40px,5vw,76px)]", SITE_PX)}>
        <div className={containerCls}>
          <div className="grid grid-cols-1 items-center gap-[clamp(24px,4vw,56px)] lg:grid-cols-2">
            <div className="flex min-w-0 max-w-[720px] flex-col gap-4">
              <h1
                className={cn(
                  "text-[clamp(34px,4.6vw,60px)] leading-[1.06] text-foreground",
                  titleTone
                )}
                data-rise=""
                style={{ "--i": 0 } as React.CSSProperties}
              >
                {titleCopy}
              </h1>
              <p
                className="text-[17px] leading-[1.55] text-muted-foreground"
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {leadCopy}
              </p>
              <div
                className="mt-1 flex flex-wrap gap-3"
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {previewMode ? (
                  <span className={SITE_PRIMARY_BTN_CLS}>Xem sản phẩm</span>
                ) : (
                  <>
                    <Link href="/san-pham" className={SITE_PRIMARY_BTN_CLS}>
                      Xem sản phẩm
                    </Link>
                    <Link href="/chinh-sach" className={SITE_SECONDARY_BTN_CLS}>
                      Chính sách
                    </Link>
                  </>
                )}
              </div>
            </div>

            <figure
              className="aspect-[4/3] w-full overflow-hidden rounded-lg bg-card"
              data-reveal="clip"
            >
              <img
                src={bannerImg}
                alt={`Không gian ${brandName}`}
                width={1365}
                height={768}
                className="block size-full object-cover object-[center_22%]"
              />
            </figure>
          </div>
        </div>
      </section>
    );
  };

  const renderBodyBlocks = () => (
    <div className="flex min-w-0 max-w-[720px] flex-col gap-[clamp(24px,3vw,40px)]">
      {mode === "policies" && (
        <div id="info-policies" data-reveal="rows">
          <Accordion type="multiple" defaultValue={["policy-0"]} className="flex flex-col gap-2">
            {policyItems.map((item, idx) => (
              <AccordionItem
                key={idx}
                value={`policy-${idx}`}
                className="rounded-lg border border-border bg-card px-[clamp(18px,2.5vw,28px)] text-card-foreground last:border-b"
              >
                <AccordionTrigger className="min-h-[56px] py-4 text-[17px] font-semibold text-foreground hover:no-underline">
                  {item.heading}
                </AccordionTrigger>
                <AccordionContent className="pb-5 text-[15px] leading-[1.6] text-foreground">
                  <CollapsibleProse paragraphs={[item.detail]} previewChars={220} />
                </AccordionContent>
              </AccordionItem>
            ))}
          </Accordion>
        </div>
      )}

      {mode === "about" && textSections.length > 0 && (
        <div id="info-story" className="flex flex-col gap-6" data-reveal="rows">
          {textSections.map((sec) => {
            const paragraphs = sec.props.body
              .split(/\n{2,}/)
              .map((p) => p.trim())
              .filter(Boolean);
            return (
              <article
                key={sec.id}
                className="flex flex-col gap-3.5 rounded-lg border border-border bg-card p-[clamp(22px,3vw,36px)] text-card-foreground"
              >
                <h2 className={cn("text-[clamp(22px,2.6vw,34px)] text-foreground", titleTone)}>
                  {sec.props.title}
                </h2>
                <CollapsibleProse paragraphs={paragraphs} previewChars={200} />
              </article>
            );
          })}
        </div>
      )}

      {infoConfig.show_factory_profile && factoryEntries.length > 0 && (
        <div id="info-factory">
          <div className="mb-5">
            <h2 className={cn("text-[clamp(22px,2.6vw,34px)] text-foreground", titleTone)}>
              Thông tin cửa hàng
            </h2>
          </div>

          <div
            className={
              layout === "b2b-catalog-sheet"
                ? "grid grid-cols-1 divide-y divide-border overflow-hidden rounded-lg border border-border bg-card sm:grid-cols-2 sm:divide-x"
                : "grid grid-cols-1 gap-4 sm:grid-cols-2"
            }
            data-reveal="rows"
          >
            {factoryEntries.map((item, idx) => (
              <article
                key={idx}
                className={
                  layout === "b2b-catalog-sheet"
                    ? "flex min-w-0 flex-col gap-2 bg-card p-5 text-card-foreground"
                    : "flex min-w-0 flex-col gap-2 rounded-lg border border-border bg-card p-[clamp(20px,2.5vw,28px)] text-card-foreground"
                }
              >
                <h3 className="text-lg font-semibold text-foreground">{item.heading}</h3>
                <CollapsibleProse paragraphs={[item.detail]} previewChars={160} />
              </article>
            ))}
          </div>
        </div>
      )}
    </div>
  );

  return (
    <div data-info-layout={layout}>
      {renderHeaderHero()}

      <section className={cn("w-full pt-3 pb-[clamp(40px,5vw,76px)]", SITE_PX)}>
        <div className={containerCls}>
          {infoConfig.show_toc && tocEntries.length > 1 ? (
            <div className="grid grid-cols-1 items-start gap-[clamp(24px,3.5vw,48px)] lg:grid-cols-[minmax(190px,230px)_minmax(0,1fr)]">
              <nav
                className="flex flex-col gap-2.5 rounded-lg border border-border bg-card p-5 text-card-foreground lg:sticky lg:top-[84px]"
                aria-label="Mục lục"
              >
                <span className="text-[13px] font-semibold text-muted-foreground">Mục lục</span>
                {tocEntries.map((entry) =>
                  previewMode ? (
                    <span key={entry.id} className="text-sm font-medium text-foreground">
                      {entry.label}
                    </span>
                  ) : (
                    <a
                      key={entry.id}
                      href={`#${entry.id}`}
                      className="inline-flex min-h-8 items-center text-sm font-medium text-foreground transition-colors hover:text-brand"
                    >
                      {entry.label}
                    </a>
                  )
                )}
              </nav>
              {renderBodyBlocks()}
            </div>
          ) : (
            renderBodyBlocks()
          )}
        </div>
      </section>

      {infoConfig.sections && infoConfig.sections.length > 0 && (
        <SiteRenderer
          config={templateConfig}
          shop={shop}
          products={products}
          vouchers={vouchers}
          previewMode={previewMode}
          sectionsOverride={infoConfig.sections}
        />
      )}

      {infoConfig.show_showcase && showcaseProducts.length > 0 && (
        <section
          id="info-showcase"
          className={cn("w-full bg-card py-[clamp(40px,5vw,76px)] text-card-foreground", SITE_PX)}
        >
          <div className={containerCls}>
            <div className="mb-[clamp(24px,3vw,38px)] flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
              <h2 className={cn("text-[clamp(24px,2.8vw,38px)] text-foreground", titleTone)}>
                Sản phẩm tiêu biểu
              </h2>
              {previewMode ? (
                <span className={SITE_SECTION_LINK_CLS}>Xem tất cả</span>
              ) : (
                <Link href="/san-pham" className={SITE_SECTION_LINK_CLS}>
                  Xem tất cả
                </Link>
              )}
            </div>
            <div className={showcaseGridClass} data-reveal="rows">
              {showcaseProducts.map((product, idx) => (
                <SiteProductCard
                  key={product.id}
                  product={product}
                  index={idx}
                  previewMode={previewMode}
                  cardStyle={cardStyle}
                  layoutMode={showcaseProducts.length === 1 ? "single-wide" : "default"}
                />
              ))}
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
