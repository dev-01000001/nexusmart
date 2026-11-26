import React from "react";
import type { CategoryInfo, Product } from "@/data/products";
import { sanitizeSafeHref } from "@/lib/site/theme";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { DenseGridHeroSearch } from "../DenseGridHeroSearch";
import { MultiSkuDirectory } from "../MultiSkuDirectory";
import { OnePageLaunchHero } from "../OnePageLaunchHero";
import { SafeActionLink } from "../SafeActionLink";
import {
  archetypeTitleTone,
  containerWidthClass,
  sectionPaddingClass,
  sectionSurfaceClass,
  SITE_PX,
} from "../shell/siteStyleVars";
import type {
  SectionSurface,
  SiteArchetype,
  SiteSectionV2,
  SiteTemplateConfig,
} from "../templateTypes";
import { sanitizeSafeImageUrl } from "../templateValidator";
import { renderInlineBlocks, renderMaskedHeading, SITE_PRIMARY_BTN_CLS } from "./SiteSectionBlocks";

/** Splits a Vietnamese headline cleanly into two balanced halves for flanking minimal-fashion portrait. */
function splitHeadlineHalves(text: string): [string, string] {
  const words = text.trim().split(/\s+/).filter(Boolean);
  if (words.length <= 1) return [text.trim(), ""];
  const mid = Math.ceil(words.length / 2);
  return [words.slice(0, mid).join(" "), words.slice(mid).join(" ")];
}

/**
 * Resolves the effective hero mode so every archetype (including legacy v1 seeded sites
 * converted with default `variant: "split"`) displays its unmistakable signature hero.
 */
function resolveEffectiveHeroMode(archetype: SiteArchetype, variant: string | undefined): string {
  const explicit = (variant || "").trim();
  if (
    archetype === "one-page-launch" &&
    (!explicit || explicit === "fullbleed" || explicit === "split")
  ) {
    return "one-page-launch";
  }
  if (
    archetype === "multi-sku-catalog" &&
    (!explicit || explicit === "compact-bar" || explicit === "split")
  ) {
    return "multi-sku-catalog";
  }
  if (
    archetype === "dense-grid" &&
    (!explicit || explicit === "compact-bar" || explicit === "split")
  ) {
    return "dense-grid";
  }
  if (!explicit || explicit === "split") {
    switch (archetype) {
      case "minimal-fashion":
        return "centered";
      case "editorial":
        return "editorial";
      case "craft-atelier":
        return "atelier";
      case "sport-active":
        return "sport-diagonal";
      case "luxury-maison":
        return "luxury-framed";
      case "classic":
      default:
        return "fullbleed";
    }
  }
  return explicit;
}

function sectionSurface(section: SiteSectionV2, fallback: SectionSurface): SectionSurface {
  return section.settings?.surface || fallback;
}

export function renderHeroSection(
  section: Extract<SiteSectionV2, { type: "hero" }>,
  tpl: SiteTemplateConfig,
  shop: SitePublicShopDTO,
  products: Product[],
  categories: CategoryInfo[],
  isFirstHeading: boolean,
  previewMode: boolean
) {
  const { props, variant, blocks } = section;
  const archetype = tpl.layout.archetype;
  const mode = resolveEffectiveHeroMode(archetype, variant);
  const brandName = tpl.theme.brand_name?.trim() || shop.name;
  const innerCls = containerWidthClass(tpl.layout.container_width);

  const primaryProductImg =
    sanitizeSafeImageUrl(products[0]?.onModelImage) || sanitizeSafeImageUrl(products[0]?.image);
  const secondaryProductImg =
    sanitizeSafeImageUrl(products[1]?.onModelImage) ||
    sanitizeSafeImageUrl(products[1]?.image) ||
    "/banners/fabric-closeup.jpg";

  const heroImg =
    sanitizeSafeImageUrl(props.image_url) ||
    sanitizeSafeImageUrl(tpl.theme.banner_url) ||
    primaryProductImg ||
    "/banners/vietnam-workshop.jpg";

  const ctaLabel = props.cta_label?.trim() || "Xem sản phẩm";
  const ctaHref = sanitizeSafeHref(props.cta_href, "/san-pham");
  const titleText = props.title.trim() || brandName;
  const subtitleText = props.subtitle?.trim() || "";
  const HeadingTag = isFirstHeading ? "h1" : "h2";

  // 1) ONE-PAGE LAUNCH: sticky center product spotlight with 3 color/angle stages + persistent buy bar
  if (mode === "one-page-launch") {
    const rawLaunchHref = props.cta_href?.trim() ? sanitizeSafeHref(props.cta_href, "") : "";
    const surf = sectionSurface(section, "bg");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn(
          "w-full pt-[clamp(20px,3vw,36px)] pb-[clamp(28px,4vw,48px)]",
          SITE_PX,
          sectionSurfaceClass(surf)
        )}
        data-section-type="hero"
        data-section-variant={variant || "fullbleed"}
        data-surface={surf}
      >
        <div className={innerCls}>
          <OnePageLaunchHero
            title={titleText}
            subtitle={subtitleText}
            ctaLabel={ctaLabel}
            ctaHref={rawLaunchHref}
            fallbackImage={heroImg}
            product={products[0]}
            isFirstHeading={isFirstHeading}
            previewMode={previewMode}
          />
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 2) MULTI-SKU CATALOG: typographic row-by-row directory with anchored hover image
  if (mode === "multi-sku-catalog") {
    const surf = sectionSurface(section, "bg");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn("w-full", SITE_PX, sectionPaddingClass("compact"), sectionSurfaceClass(surf))}
        data-section-type="hero"
        data-section-variant={variant || "compact-bar"}
        data-surface={surf}
        data-padding="compact"
      >
        <div className={innerCls}>
          <MultiSkuDirectory
            title={titleText}
            subtitle={subtitleText}
            ctaLabel={ctaLabel}
            ctaHref={ctaHref}
            products={products}
            fallbackImage={heroImg}
            isFirstHeading={isFirstHeading}
            previewMode={previewMode}
          />
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 3) DENSE GRID: zero hero image, straight into prominent search bar + category rail
  if (mode === "dense-grid" || mode === "compact-bar") {
    const surf = sectionSurface(section, "surface");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn("w-full", SITE_PX, sectionPaddingClass("compact"), sectionSurfaceClass(surf))}
        data-section-type="hero"
        data-section-variant={variant || "compact-bar"}
        data-surface={surf}
        data-padding="compact"
      >
        <div className={innerCls}>
          <DenseGridHeroSearch
            title={titleText}
            subtitle={subtitleText}
            brandName={brandName}
            shopId={shop.id}
            categories={categories}
            productCount={products.length}
            isFirstHeading={isFirstHeading}
            previewMode={previewMode}
          />
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 4) MINIMAL FASHION: centered 3:4 portrait with headline split left & right
  if (mode === "centered") {
    const portraitImg = primaryProductImg || heroImg;
    const [leftHalf, rightHalf] = splitHeadlineHalves(titleText);
    const surf = sectionSurface(section, "bg");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn(
          "w-full",
          SITE_PX,
          sectionPaddingClass("generous"),
          sectionSurfaceClass(surf)
        )}
        data-section-type="hero"
        data-section-variant={variant || "centered"}
        data-surface={surf}
        data-padding="generous"
      >
        <div className={innerCls}>
          <div className="grid grid-cols-1 items-center gap-5 py-4 text-center lg:grid-cols-[minmax(0,1fr)_minmax(260px,360px)_minmax(0,1fr)] lg:gap-[clamp(20px,3vw,40px)] lg:py-[clamp(16px,3vw,36px)]">
            <HeadingTag
              className="z-[2] block text-center text-[clamp(30px,5.2vw,48px)] leading-[1.1] font-medium tracking-[-0.025em] text-foreground lg:pointer-events-none lg:col-span-full lg:row-start-1 lg:grid lg:grid-cols-[minmax(0,1fr)_minmax(260px,360px)_minmax(0,1fr)] lg:items-center lg:gap-[clamp(20px,3vw,40px)] lg:text-[clamp(28px,3.3vw,48px)]"
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              <span className="lg:pointer-events-auto lg:col-start-1 lg:text-right">
                {leftHalf}
              </span>
              {rightHalf && (
                <span className="lg:pointer-events-auto lg:col-start-3 lg:text-left">
                  {" "}
                  {rightHalf}
                </span>
              )}
            </HeadingTag>

            <figure
              className="mx-auto aspect-[3/4] w-full max-w-[340px] overflow-hidden rounded-lg bg-muted lg:col-start-2 lg:row-start-1 lg:max-w-none"
              data-reveal="clip"
            >
              <img
                src={portraitImg}
                alt={titleText}
                width={896}
                height={1200}
                className="block size-full object-cover object-[center_22%]"
              />
            </figure>

            <div
              className="flex flex-col items-center gap-3.5 text-center lg:col-span-full lg:row-start-2"
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              {subtitleText && (
                <p className="max-w-[44ch] text-base text-muted-foreground">{subtitleText}</p>
              )}
              <SafeActionLink
                href={ctaHref}
                label={ctaLabel}
                className={SITE_PRIMARY_BTN_CLS}
                previewMode={previewMode}
              />
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        </div>
      </section>
    );
  }

  // 5) EDITORIAL: colossal tight headline beside two vertically staggered photos
  if (mode === "editorial") {
    const photoA = heroImg;
    const photoB =
      secondaryProductImg !== photoA ? secondaryProductImg : "/banners/fabric-closeup.jpg";
    const surf = sectionSurface(section, "bg");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn("w-full", SITE_PX, sectionPaddingClass("normal"), sectionSurfaceClass(surf))}
        data-section-type="hero"
        data-section-variant={variant || "editorial"}
        data-surface={surf}
        data-padding="normal"
      >
        <div className={innerCls}>
          <div className="grid grid-cols-6 items-start gap-3 md:grid-cols-12 md:gap-[clamp(14px,2vw,24px)]">
            <div className="z-[3] col-span-6 row-start-1 flex flex-col gap-5 md:col-span-5 md:row-span-2 md:pt-[clamp(8px,2vw,24px)]">
              {renderMaskedHeading(
                HeadingTag,
                titleText,
                "max-w-full text-[clamp(36px,10vw,56px)] leading-[1.04] font-bold tracking-[-0.03em] text-balance text-foreground md:text-[clamp(40px,5.4vw,82px)]",
                true
              )}
              <div
                className="flex max-w-[34ch] flex-col items-start gap-3.5"
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {subtitleText && (
                  <p className="text-base leading-[1.5] text-muted-foreground">{subtitleText}</p>
                )}
                <SafeActionLink
                  href={ctaHref}
                  label={ctaLabel}
                  className={SITE_PRIMARY_BTN_CLS}
                  previewMode={previewMode}
                />
              </div>
            </div>

            <figure
              className="z-[1] col-span-4 col-start-1 row-start-2 aspect-[3/4] overflow-hidden rounded-lg bg-card md:col-span-4 md:col-start-6 md:row-span-2 md:row-start-1"
              data-reveal="clip"
            >
              <img
                src={photoA}
                alt={titleText}
                width={1000}
                height={1333}
                className="block size-full object-cover object-[center_22%]"
              />
            </figure>

            <figure
              className="z-[2] col-span-3 col-start-4 row-start-2 mt-11 aspect-[4/5] overflow-hidden rounded-lg bg-muted md:col-span-4 md:col-start-9 md:row-span-2 md:row-start-2 md:mt-[clamp(24px,4vw,60px)]"
              data-reveal="clip"
            >
              <img
                src={photoB}
                alt={`Chi tiết chất liệu ${brandName}`}
                width={900}
                height={900}
                className="block size-full object-cover object-[center_22%]"
              />
            </figure>
          </div>
          {renderInlineBlocks(blocks, previewMode)}
        </div>
      </section>
    );
  }

  // 6) CRAFT ATELIER: full-bleed fabric photograph with wide headline anchored bottom-left
  if (mode === "atelier") {
    const craftHeroImg =
      sanitizeSafeImageUrl(props.image_url) ||
      sanitizeSafeImageUrl(tpl.theme.banner_url) ||
      primaryProductImg ||
      "/banners/fabric-closeup.jpg";
    return (
      <section
        key={section.id}
        id={section.id}
        className="w-full bg-background p-0 text-foreground"
        data-section-type="hero"
        data-section-variant={variant || "atelier"}
        data-surface="bg"
      >
        <div className="relative grid min-h-[460px] w-full items-end overflow-hidden bg-muted md:min-h-[clamp(460px,72svh,680px)]">
          <figure className="absolute inset-0 z-[1] size-full" data-reveal="clip">
            <img
              src={craftHeroImg}
              alt={titleText}
              width={1600}
              height={1000}
              className="block size-full object-cover object-[center_22%]"
            />
          </figure>
          <div className="relative z-[2] m-3 flex max-w-[calc(100%-24px)] flex-col gap-3.5 rounded-lg bg-background p-[18px] text-foreground md:m-[clamp(1rem,2.2vw,2rem)] md:max-w-[min(640px,calc(100%-4rem))] md:p-[clamp(22px,3vw,38px)]">
            {renderMaskedHeading(
              HeadingTag,
              titleText,
              "text-[clamp(28px,3.6vw,48px)] leading-[1.08] font-semibold tracking-[-0.02em] text-foreground",
              true
            )}
            {subtitleText && (
              <p
                className="max-w-[48ch] text-base leading-[1.5] text-muted-foreground"
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {subtitleText}
              </p>
            )}
            <div
              className="mt-1 flex flex-wrap gap-3"
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <SafeActionLink
                href={ctaHref}
                label={ctaLabel}
                className={SITE_PRIMARY_BTN_CLS}
                previewMode={previewMode}
              />
            </div>
            {renderInlineBlocks(blocks, previewMode)}
          </div>
        </div>
      </section>
    );
  }

  // 7) SPORT ACTIVE: dark ground, bold poster headline + diagonal clip-path action photo
  if (mode === "sport-diagonal") {
    const surf = sectionSurface(section, "bg");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn("w-full", SITE_PX, sectionPaddingClass("normal"), sectionSurfaceClass(surf))}
        data-section-type="hero"
        data-section-variant={variant || "sport-diagonal"}
        data-surface={surf}
        data-padding="normal"
      >
        <div className={innerCls}>
          <div className="flex flex-col gap-[clamp(16px,2.4vw,28px)]">
            <HeadingTag
              className="text-[clamp(40px,13vw,72px)] leading-[0.96] font-extrabold tracking-[-0.025em] text-balance text-foreground md:text-[clamp(44px,9.4vw,138px)]"
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleText}
            </HeadingTag>

            <div className="grid grid-cols-1 items-center gap-[clamp(20px,3vw,44px)] lg:grid-cols-[minmax(0,7.5fr)_minmax(0,4.5fr)]">
              <figure
                className="aspect-video max-h-[390px] w-full overflow-hidden bg-card [clip-path:polygon(5%_0,100%_0,95%_100%,0_100%)]"
                data-reveal="clip"
              >
                <img
                  src={heroImg}
                  alt={titleText}
                  width={1365}
                  height={768}
                  className="block size-full object-cover object-[center_22%]"
                />
              </figure>

              <div
                className="flex flex-col items-start gap-4"
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                {subtitleText && (
                  <p className="max-w-[34ch] text-base leading-[1.55] text-muted-foreground">
                    {subtitleText}
                  </p>
                )}
                <SafeActionLink
                  href={ctaHref}
                  label={ctaLabel}
                  className={SITE_PRIMARY_BTN_CLS}
                  previewMode={previewMode}
                />
                {renderInlineBlocks(blocks, previewMode)}
              </div>
            </div>
          </div>
        </div>
      </section>
    );
  }

  // 8) LUXURY MAISON: framed portrait centered in expansive quiet field
  if (mode === "luxury-framed") {
    const surf = sectionSurface(section, "bg");
    return (
      <section
        key={section.id}
        id={section.id}
        className={cn(
          "w-full pt-[clamp(20px,2.8vw,36px)] pb-[clamp(32px,4vw,52px)]",
          SITE_PX,
          sectionSurfaceClass(surf)
        )}
        data-section-type="hero"
        data-section-variant={variant || "luxury-framed"}
        data-surface={surf}
        data-padding="normal"
      >
        <div className={innerCls}>
          <div className="flex min-h-[clamp(520px,74svh,740px)] flex-col items-center justify-center gap-[clamp(16px,2vw,24px)] py-2 text-center">
            <HeadingTag
              className="max-w-[24ch] text-[clamp(26px,3vw,42px)] leading-[1.14] font-normal tracking-[0.02em] text-foreground"
              data-rise=""
              style={{ "--i": 0 } as React.CSSProperties}
            >
              {titleText}
            </HeadingTag>

            <figure
              className="aspect-[4/5] h-[clamp(320px,58svh,540px)] w-auto max-w-[min(440px,78vw)] overflow-hidden rounded-lg bg-card p-[clamp(10px,1.4vw,18px)]"
              data-reveal="scale"
            >
              <img
                src={heroImg}
                alt={titleText}
                width={900}
                height={1125}
                className="block size-full object-cover object-[center_22%]"
              />
            </figure>

            <div
              className="flex flex-col items-center gap-3.5"
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              {subtitleText && (
                <p className="max-w-[42ch] text-[15px] text-muted-foreground">{subtitleText}</p>
              )}
              <SafeActionLink
                href={ctaHref}
                label={ctaLabel}
                className="inline-flex min-h-11 items-center justify-center rounded-md border border-primary px-6 text-sm font-medium tracking-[0.08em] text-primary transition-colors hover:bg-primary hover:text-primary-foreground"
                previewMode={previewMode}
              />
              {renderInlineBlocks(blocks, previewMode)}
            </div>
          </div>
        </div>
      </section>
    );
  }

  // 9) CLASSIC / FULLBLEED: full-bleed photograph with clean bottom-anchored headline bar
  return (
    <section
      key={section.id}
      id={section.id}
      className="w-full bg-background p-0 text-foreground"
      data-section-type="hero"
      data-section-variant={variant || "fullbleed"}
      data-surface="bg"
    >
      <div className="relative flex min-h-[clamp(440px,68svh,640px)] w-full flex-col justify-end overflow-hidden bg-muted">
        <figure className="absolute inset-0 size-full" data-reveal="clip">
          <img
            src={heroImg}
            alt={titleText}
            width={1600}
            height={1000}
            className="block size-full object-cover object-[center_22%]"
          />
        </figure>
        <div
          className={cn(
            "relative z-[2] w-full bg-background py-[clamp(20px,3vw,34px)] text-foreground",
            SITE_PX
          )}
        >
          <div className={cn(innerCls, "flex flex-wrap items-end justify-between gap-x-8 gap-y-4")}>
            <div className="flex max-w-[56ch] flex-col gap-2">
              {renderMaskedHeading(
                HeadingTag,
                titleText,
                cn("text-[clamp(28px,4vw,52px)] text-foreground", archetypeTitleTone(archetype)),
                true
              )}
              {subtitleText && <p className="text-base text-muted-foreground">{subtitleText}</p>}
            </div>
            <SafeActionLink
              href={ctaHref}
              label={ctaLabel}
              className={SITE_PRIMARY_BTN_CLS}
              previewMode={previewMode}
            />
          </div>
          {blocks && blocks.length > 0 && (
            <div className={innerCls}>{renderInlineBlocks(blocks, previewMode)}</div>
          )}
        </div>
      </div>
    </section>
  );
}
