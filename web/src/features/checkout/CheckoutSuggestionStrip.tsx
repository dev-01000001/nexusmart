"use client";

import React, { useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { GatewayProduct } from "@/lib/gateway-types";
import { formatVnd } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useSession } from "@/context/SessionContext";
import { Button } from "@/components/Button";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./CheckoutSuggestionStrip.module.css";

export interface CheckoutSuggestionStripProps {
  title: string;
  subtitle?: string;
  products: GatewayProduct[];
  shopNameById?: Record<string, string>;
  showQuickAdd?: boolean;
}

function pickProductImage(product: GatewayProduct): string {
  const imgs = product.images ?? [];
  const onModel = imgs.find((src) => src.includes("/models/"));
  if (onModel) return onModel;
  const firstImg = imgs[0];
  if (firstImg) return firstImg;
  const skuImg = product.skus?.find((s) => s.image_url)?.image_url;
  return skuImg || "/products/ao-thun-cotton-trang.png";
}

function pickFirstAvailableSku(product: GatewayProduct) {
  return (product.skus ?? []).find((s) => s.is_active && s.available_stock > 0) ?? null;
}

export function CheckoutSuggestionStrip({
  title,
  subtitle,
  products,
  shopNameById,
  showQuickAdd = false,
}: CheckoutSuggestionStripProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { isAuthenticated } = useSession();
  const { addToCart } = useCart();
  const [addingId, setAddingId] = useState<string | null>(null);
  const [addedId, setAddedId] = useState<string | null>(null);

  const visible = products.slice(0, 4);
  if (visible.length === 0) return null;

  const handleQuickAdd = async (product: GatewayProduct) => {
    const sku = pickFirstAvailableSku(product);
    if (!sku) return;
    if (!isAuthenticated) {
      const next = pathname || "/gio-hang";
      router.push(`/dang-nhap?next=${encodeURIComponent(next)}`);
      return;
    }
    setAddingId(product.id);
    const res = await addToCart({ skuId: sku.id, quantity: 1 });
    setAddingId(null);
    if (res.ok) {
      setAddedId(product.id);
      window.setTimeout(() => {
        setAddedId((cur) => (cur === product.id ? null : cur));
      }, 2200);
    }
  };

  return (
    <section className={`${shared.section} ${shared.sectionGap}`} aria-label={title}>
      <div className={shared.sectionHead}>
        <h2 className={shared.sectionTitle}>{title}</h2>
        {subtitle && <p className={shared.sectionNote}>{subtitle}</p>}
      </div>

      <div className={styles.grid}>
        {visible.map((product) => {
          const href = product.slug ? `/san-pham/${encodeURIComponent(product.slug)}` : "/danh-muc";
          const imageSrc = pickProductImage(product);
          const availableSku = pickFirstAvailableSku(product);
          const displayPrice =
            (product.min_price_vnd ?? 0) > 0
              ? (product.min_price_vnd ?? 0)
              : (availableSku?.price_vnd ?? 0);
          const shopName = shopNameById?.[product.shop_id] ?? "Xưởng dệt NexusMart";
          const isAdding = addingId === product.id;
          const isAdded = addedId === product.id;

          return (
            <article key={product.id} className={styles.card}>
              <Link href={href} className={styles.mediaLink}>
                <img
                  src={imageSrc}
                  alt={product.name}
                  width={896}
                  height={1200}
                  className={styles.thumb}
                />
              </Link>
              <div className={styles.body}>
                <span className={styles.shop}>{shopName}</span>
                <Link href={href} className={styles.name}>
                  {product.name}
                </Link>
                <div className={styles.foot}>
                  <span className={`${styles.price} num`}>{formatVnd(displayPrice)}</span>
                  {showQuickAdd && availableSku ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={isAdding}
                      onClick={() => void handleQuickAdd(product)}
                    >
                      {isAdding ? "Đang thêm..." : isAdded ? "Đã thêm" : "Thêm vào giỏ"}
                    </Button>
                  ) : (
                    <Button variant="secondary" size="sm" href={href}>
                      Xem sản phẩm
                    </Button>
                  )}
                </div>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
