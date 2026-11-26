import React from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import { Money } from "@/components/app/Money";
import { Badge } from "@/components/ui/badge";
import { formatDiscountPercent, formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SiteCardStyle } from "./templateTypes";
import { sanitizeSafeImageUrl } from "./templateValidator";

export interface SiteProductCardProps {
  product: Product;
  previewMode?: boolean;
  index?: number;
  cardStyle?: SiteCardStyle;
  /** Shows a short description snippet on editorial / magazine cards. */
  showExcerpt?: boolean;
  /** Expands to a full-row horizontal layout when a section only contains 1 product (zero orphan). */
  layoutMode?: "default" | "single-wide";
}

export function SiteProductCard({
  product,
  previewMode = false,
  index = 0,
  cardStyle = "minimal",
  showExcerpt = false,
  layoutMode = "default",
}: SiteProductCardProps) {
  const candidates = Array.from(
    new Set(
      [
        product.onModelImage,
        product.image,
        ...(product.gallery ?? []),
        ...product.colors.map((c) => c.image),
      ]
        .map((u) => sanitizeSafeImageUrl(u))
        .filter(Boolean)
    )
  );
  const primaryImg = candidates[0] || "/banners/fabric-closeup.jpg";
  const secondaryImg = candidates.length > 1 ? candidates[1] : null;

  const displayPrice = product.price;
  const comparePrice = product.originalPrice;
  const hasDiscount = typeof comparePrice === "number" && comparePrice > displayPrice;
  const discountLabel =
    hasDiscount && comparePrice ? formatDiscountPercent(comparePrice, displayPrice) : "";

  const totalStock = product.skus.reduce((sum, sku) => sum + Math.max(0, sku.stock), 0);
  const stockNotice =
    totalStock <= 0 ? "Tạm hết hàng" : totalStock <= 5 ? `Còn ${formatInt(totalStock)}` : "";

  const availableSizes = Array.from(
    new Set(product.skus.filter((s) => s.stock > 0).map((s) => s.size))
  );
  const href = `/san-pham/${encodeURIComponent(product.slug)}`;
  const isSingleWide = layoutMode === "single-wide";
  const isCompactRow = !isSingleWide && cardStyle === "compact-row";
  const isLuxury = !isSingleWide && cardStyle === "luxury";
  const isDense = !isSingleWide && cardStyle === "dense";
  const isAtelier = !isSingleWide && cardStyle === "atelier";
  const isSport = !isSingleWide && cardStyle === "sport";
  const isEditorial = !isSingleWide && cardStyle === "editorial";

  const promoBadge = product.isFlashSale ? (
    <Badge variant="warning" className="rounded-sm px-2 py-0.5 text-[11px] font-semibold">
      Flash sale
    </Badge>
  ) : discountLabel ? (
    <Badge
      variant="default"
      className="rounded-sm px-2 py-0.5 text-[11px] font-semibold tabular-nums"
    >
      {discountLabel}
    </Badge>
  ) : null;

  const showOverlayBadge = Boolean(promoBadge) && !isCompactRow;

  const articleClass = cn(
    "group relative min-w-0",
    isSingleWide
      ? "grid grid-cols-1 items-center gap-5 rounded-lg border border-border bg-card p-4 md:grid-cols-2 md:gap-8 md:p-6"
      : isCompactRow
        ? "flex flex-col overflow-hidden rounded-lg border border-border bg-card sm:flex-row sm:items-stretch"
        : isLuxury
          ? "flex flex-col bg-transparent"
          : isEditorial
            ? "flex flex-col overflow-hidden rounded-lg border border-border bg-card"
            : isDense
              ? "flex flex-col overflow-hidden rounded-lg border border-border bg-card"
              : isAtelier
                ? "flex flex-col overflow-hidden rounded-lg border border-border bg-card p-2"
                : isSport
                  ? "flex flex-col overflow-hidden rounded-lg border border-border bg-card"
                  : "flex flex-col bg-transparent"
  );

  const mediaWrapClass = cn(
    "relative block overflow-hidden bg-muted",
    isSingleWide
      ? "aspect-[4/3] w-full rounded-lg"
      : isCompactRow
        ? "aspect-[4/3] w-full shrink-0 sm:aspect-[3/4] sm:w-28"
        : isLuxury
          ? "aspect-[3/4] w-full rounded-lg border border-border/70 bg-card p-3"
          : isDense
            ? "aspect-[4/5] w-full"
            : isAtelier
              ? "aspect-[3/4] w-full rounded-md"
              : isEditorial || isSport
                ? "aspect-[3/4] w-full"
                : "aspect-[3/4] w-full rounded-lg"
  );

  const mediaInner = (
    <>
      <img
        src={primaryImg}
        alt={product.name}
        width={896}
        height={1200}
        loading={index < 4 ? "eager" : "lazy"}
        className={cn(
          "absolute inset-0 block size-full object-cover object-[center_16%] transition-all duration-300 ease-out",
          secondaryImg
            ? "group-hover:opacity-0 group-focus-within:opacity-0"
            : "group-hover:scale-[1.035]"
        )}
      />
      {secondaryImg && (
        <img
          src={secondaryImg}
          alt=""
          aria-hidden="true"
          width={896}
          height={1200}
          loading="lazy"
          className="absolute inset-0 block size-full scale-[1.035] object-cover object-[center_16%] opacity-0 transition-all duration-300 ease-out group-hover:scale-[1.015] group-hover:opacity-100 group-focus-within:scale-[1.015] group-focus-within:opacity-100"
        />
      )}
      {showOverlayBadge && (
        <div className="pointer-events-none absolute top-2.5 left-2.5 z-10">{promoBadge}</div>
      )}
    </>
  );

  const mediaContent = isLuxury ? (
    <div className="relative size-full overflow-hidden rounded-sm bg-muted">{mediaInner}</div>
  ) : (
    mediaInner
  );

  const bodyClass = cn(
    "flex min-w-0 flex-1 flex-col",
    isSingleWide
      ? "gap-3 p-1"
      : isCompactRow
        ? "justify-center gap-2 p-3.5"
        : isLuxury
          ? "items-center gap-2 px-2 pt-4 pb-1 text-center"
          : isDense
            ? "gap-1.5 p-2.5"
            : isEditorial
              ? "gap-2 p-4"
              : isAtelier
                ? "gap-2 px-2 pt-3 pb-2"
                : isSport
                  ? "gap-2 p-3.5"
                  : "gap-1.5 px-0.5 pt-3 pb-1"
  );

  const titleClass = cn(
    "order-first line-clamp-2 text-foreground transition-colors hover:text-brand focus-visible:text-brand",
    isSingleWide
      ? "text-lg leading-snug font-semibold tracking-tight"
      : isDense
        ? "text-sm leading-5 font-medium"
        : isLuxury
          ? "text-[15px] leading-snug font-normal tracking-[0.02em]"
          : isSport
            ? "text-[15px] leading-snug font-bold tracking-tight"
            : isEditorial
              ? "text-[15px] leading-snug font-semibold tracking-tight"
              : "text-[15px] leading-snug font-medium"
  );

  const hasMetaRow = product.colors.length > 0 || (isCompactRow && Boolean(promoBadge));

  return (
    <article
      className={articleClass}
      data-card-style={cardStyle}
      data-has-hover={secondaryImg ? "true" : "false"}
    >
      {previewMode ? (
        <div className={mediaWrapClass}>{mediaContent}</div>
      ) : (
        <Link href={href} className={mediaWrapClass} tabIndex={-1} aria-hidden="true">
          {mediaContent}
        </Link>
      )}

      <div className={bodyClass}>
        {hasMetaRow && (
          <div
            className={cn(
              "flex min-h-[18px] flex-wrap items-center gap-2",
              isLuxury ? "justify-center" : "justify-between"
            )}
          >
            {product.colors.length > 0 && (
              <div
                className="inline-flex items-center gap-1.5"
                aria-label={`${product.colors.length} màu`}
              >
                {product.colors.slice(0, 5).map((c) => (
                  <span
                    key={c.code}
                    className="size-2.5 shrink-0 rounded-full border border-foreground/25"
                    style={{ backgroundColor: c.hex }}
                    title={c.name}
                  />
                ))}
                {product.colors.length > 5 && (
                  <span className="text-xs text-muted-foreground tabular-nums">
                    +{product.colors.length - 5}
                  </span>
                )}
              </div>
            )}
            {isCompactRow && promoBadge}
          </div>
        )}

        {(showExcerpt || isSingleWide) && product.description && (
          <p className="line-clamp-2 text-sm leading-5 text-muted-foreground">
            {product.description}
          </p>
        )}

        {cardStyle === "compact-row" && availableSizes.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {availableSizes.slice(0, 5).map((sz) => (
              <Badge
                key={sz}
                variant="secondary"
                className="rounded-sm px-2 py-0.5 text-xs font-medium"
              >
                {sz}
              </Badge>
            ))}
          </div>
        )}

        <div
          className={cn(
            "mt-auto flex flex-wrap items-baseline gap-x-2.5 gap-y-1 pt-0.5",
            isLuxury ? "justify-center" : "justify-between"
          )}
        >
          <Money
            vnd={displayPrice}
            compareAtVnd={hasDiscount ? comparePrice : undefined}
            size={isDense ? "sm" : isSingleWide ? "lg" : "md"}
          />
          {stockNotice && (
            <span
              className={cn(
                "text-xs font-medium tabular-nums",
                totalStock <= 0 ? "text-destructive" : "text-brand"
              )}
            >
              {stockNotice}
            </span>
          )}
        </div>

        {/* Keep product title link as the LAST a[href^="/san-pham/"] inside article (buyer-flow E2E invariant). */}
        {previewMode ? (
          <h3 className={titleClass}>{product.name}</h3>
        ) : (
          <Link href={href} className={titleClass}>
            {product.name}
          </Link>
        )}
      </div>
    </article>
  );
}
