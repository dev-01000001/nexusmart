"use client";

import React from "react";
import { normalizeHexColor, sanitizeSafeUrl } from "@/lib/site/theme";
import styles from "./StorefrontBanner.module.css";

export interface StorefrontCheckoutContext {
  shopId: string;
  shopName: string;
  brandName: string;
  logoUrl: string;
  accentColor: string;
  accentTextColor: string;
  siteHost: string;
  siteOrigin: string;
}

export interface StorefrontBannerProps {
  context: StorefrontCheckoutContext;
  mode: "cart" | "checkout";
}

export function StorefrontBanner({ context, mode }: StorefrontBannerProps) {
  const accent = normalizeHexColor(context.accentColor, "#0D7480");
  const accentInk = normalizeHexColor(context.accentTextColor, "#FFFFFF");
  const logoSrc = sanitizeSafeUrl(context.logoUrl);
  const monogram = (context.brandName || context.shopName).slice(0, 1).toUpperCase();

  const subtitle =
    mode === "cart"
      ? `Giỏ hàng riêng từ website của ${context.brandName}. Chỉ hiển thị sản phẩm và mã ưu đãi của cửa hàng này.`
      : `Đang hoàn tất đơn hàng từ website riêng của ${context.brandName}. Áp dụng mã ưu đãi riêng của cửa hàng.`;

  return (
    <section
      className={styles.banner}
      aria-label={`Thông tin cửa hàng ${context.brandName}`}
      data-storefront-shop={context.shopId}
      data-storefront-host={context.siteHost}
      style={
        {
          "--sf-accent": accent,
          "--sf-accent-ink": accentInk,
        } as React.CSSProperties
      }
    >
      <div className={styles.identity}>
        {logoSrc ? (
          <span className={styles.logoFrame}>
            <img
              src={logoSrc}
              alt={`Logo ${context.brandName}`}
              width={44}
              height={44}
              className={styles.logoImg}
            />
          </span>
        ) : (
          <span className={styles.monogram} aria-hidden="true">
            {monogram}
          </span>
        )}
        <div className={styles.copy}>
          <p className={styles.brandLine}>{context.brandName}</p>
          <p className={styles.subLine}>{subtitle}</p>
        </div>
      </div>

      {context.siteOrigin && (
        <div className={styles.actions}>
          <a href={context.siteOrigin} className={styles.backLink}>
            Quay lại cửa hàng {context.brandName}
          </a>
        </div>
      )}
    </section>
  );
}
