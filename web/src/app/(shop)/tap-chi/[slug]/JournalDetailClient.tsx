"use client";

import React, { useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  CARE_GUIDE_LIBRARY,
  JOURNAL_ARTICLES,
  getJournalBySlug,
  getProductById,
  getShopById,
  getSku,
  type Product,
  type SizeCode,
} from "@/data";
import { useSession } from "@/context/SessionContext";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { resolveGatewaySkuId } from "@/lib/api";
import { formatDateVi, formatInt } from "@/lib/format";
import { Button, Price, StateView } from "@/components";
import { SizeFinder } from "@/features/catalog/SizeFinder";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

const SHORT_HEADLINE_BY_SLUG: Record<string, string> = {
  "cach-chon-dinh-luong-gsm-ao-thun-theo-mua": "Chọn định lượng GSM",
  "huong-dan-chon-size-theo-3-dang-nguoi-va-form-xuong": "Chọn size theo 3 dáng",
  "bo-do-chay-bo-5k-khong-co-xat-va-nhanh-kho": "Bộ đồ chạy bộ 5K",
  "meo-giat-ui-giu-co-ao-cotton-va-soi-linen-ben-dep": "Mẹo giặt ủi bền vải",
};

function EmbeddedProductBuyCard({ product }: { product: Product }) {
  const router = useRouter();
  const { isAuthenticated } = useSession();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();
  const [submitting, setSubmitting] = useState(false);
  const shop = getShopById(product.shopId);

  const [colorCode, setColorCode] = useState<string>(product.colors[0]?.code ?? "");
  const activeColor = useMemo(
    () => product.colors.find((c) => c.code === colorCode) ?? product.colors[0],
    [colorCode, product.colors]
  );

  const firstAvailableSize = useMemo(() => {
    const code = activeColor?.code ?? product.colors[0]?.code ?? "";
    return (
      product.sizes.find((sz) => (getSku(product, code, sz)?.stock ?? 0) > 0) ?? product.sizes[0]
    );
  }, [activeColor, product]);

  const [selectedSize, setSelectedSize] = useState<SizeCode>(firstAvailableSize);

  const activeSku = activeColor ? getSku(product, activeColor.code, selectedSize) : undefined;
  const resolvedSize: SizeCode =
    activeSku && activeSku.stock > 0 ? selectedSize : firstAvailableSize;
  const resolvedSku = activeColor ? getSku(product, activeColor.code, resolvedSize) : undefined;
  const outOfStock = !resolvedSku || resolvedSku.stock <= 0;

  const isDefaultColor = !activeColor || activeColor.code === product.colors[0]?.code;
  const previewSrc =
    isDefaultColor && product.onModelImage
      ? product.onModelImage
      : (activeColor?.image ?? product.image);
  const isModelImg = previewSrc.startsWith("/models/");

  const handleAdd = async (goCheckout: boolean) => {
    if (!activeColor || outOfStock || !resolvedSku || submitting) return;
    if (!isAuthenticated) {
      const nextPath =
        typeof window !== "undefined"
          ? `${window.location.pathname}${window.location.search}`
          : "/tap-chi";
      router.push(`/dang-nhap?next=${encodeURIComponent(nextPath)}`);
      return;
    }
    setSubmitting(true);
    try {
      const realSkuId = await resolveGatewaySkuId(product.slug, {
        skuCode: resolvedSku.skuCode,
        colorCode: activeColor.code,
        size: resolvedSize,
      });
      if (!realSkuId) {
        showToast("Phân loại sản phẩm này hiện đã hết hàng.");
        return;
      }
      const res = await addToCart({ skuId: realSkuId, quantity: 1 });
      if (res.ok && goCheckout) {
        router.push("/thanh-toan");
      }
    } catch {
      showToast("Không thể thêm sản phẩm vào giỏ hàng lúc này.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className={styles.embedCard}>
      <Link href={`/san-pham/${product.slug}`} className={styles.embedMedia}>
        <Image
          src={previewSrc}
          alt={activeColor ? `${product.name} - ${activeColor.name}` : product.name}
          fill
          sizes="160px"
          className={isModelImg ? styles.embedImgModel : styles.embedImg}
        />
      </Link>

      <div className={styles.embedBody}>
        <div className={styles.embedTop}>
          <span className={styles.embedShop}>
            {shop?.shortName ?? "NexusMart"} · {product.gsm}gsm · {product.fit}
          </span>
          <Link href={`/san-pham/${product.slug}`} className={styles.embedName}>
            {product.name}
          </Link>
          <Price
            amount={product.price}
            originalAmount={product.originalPrice}
            size="md"
            showDiscount={Boolean(product.originalPrice && product.originalPrice > product.price)}
          />
        </div>

        <div className={styles.embedPickers}>
          {product.colors.length > 0 && (
            <div className={styles.pickerRow}>
              <span className={styles.pickerLabel}>Màu: {activeColor?.name}</span>
              <div
                className={styles.swatchRow}
                role="group"
                aria-label={`Chọn màu ${product.shortName}`}
              >
                {product.colors.map((c) => {
                  const active = c.code === activeColor?.code;
                  return (
                    <button
                      key={c.code}
                      type="button"
                      className={`${styles.swatchBtn} ${active ? styles.swatchBtnActive : ""}`}
                      onClick={() => setColorCode(c.code)}
                      aria-label={`Chọn màu ${c.name}`}
                      aria-pressed={active}
                      title={c.name}
                    >
                      <span className={styles.swatchDot} style={{ backgroundColor: c.hex }} />
                    </button>
                  );
                })}
              </div>
            </div>
          )}

          <div className={styles.pickerRow}>
            <span className={styles.pickerLabel}>
              Size: {resolvedSize}
              {resolvedSku ? ` (còn ${resolvedSku.stock})` : ""}
            </span>
            <div
              className={styles.sizeRow}
              role="group"
              aria-label={`Chọn size ${product.shortName}`}
            >
              {product.sizes.map((sz) => {
                const sku = activeColor ? getSku(product, activeColor.code, sz) : undefined;
                const noStock = !sku || sku.stock <= 0;
                const active = resolvedSize === sz;
                return (
                  <button
                    key={sz}
                    type="button"
                    disabled={noStock}
                    className={`${styles.sizeBtn} ${active ? styles.sizeBtnActive : ""}`}
                    onClick={() => setSelectedSize(sz)}
                    aria-pressed={active}
                  >
                    {sz}
                  </button>
                );
              })}
            </div>
          </div>
        </div>

        <div className={styles.embedActions}>
          <Button variant="primary" size="md" disabled={outOfStock} onClick={() => handleAdd(true)}>
            Mua ngay
          </Button>
          <Button
            variant="secondary"
            size="md"
            disabled={outOfStock}
            onClick={() => handleAdd(false)}
          >
            Thêm vào giỏ
          </Button>
        </div>
      </div>
    </div>
  );
}

export function JournalDetailClient({ slug }: { slug: string }) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);

  const stateParam = searchParams.get("state");
  const article = useMemo(() => getJournalBySlug(slug), [slug]);

  useRevealObserver(rootRef, `${slug}|${stateParam ?? ""}`);

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải nội dung bài viết..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải bài viết này"
          message="Kết nối đến kho nội dung tạm thời gián đoạn. Vui lòng tải lại."
          actionLabel="Tải lại bài viết"
          onAction={() => router.replace(`/tap-chi/${slug}`)}
        />
      </div>
    );
  }

  if (stateParam === "empty" || !article) {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy bài viết trong Sổ tay vải"
          message="Bài viết có thể đã chuyển chuyên mục. Bạn có thể đọc toàn bộ hướng dẫn đang mở."
          actionLabel="Về trang Sổ tay vải"
          actionHref="/tap-chi"
        />
      </div>
    );
  }

  const shortH1 = SHORT_HEADLINE_BY_SLUG[article.slug] ?? article.categoryLabel;
  const relatedArticles = JOURNAL_ARTICLES.filter((a) => a.slug !== article.slug);
  const allEmbeddedProducts = article.embeddedProductIds
    .map((id) => getProductById(id))
    .filter((p): p is Product => Boolean(p));

  const isSizeGuideArticle = article.slug === "huong-dan-chon-size-theo-3-dang-nguoi-va-form-xuong";
  const isCareGuideArticle = article.slug === "meo-giat-ui-giu-co-ao-cotton-va-soi-linen-ben-dep";

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <Link href="/tap-chi" className={styles.crumbLink}>
          Sổ tay vải &amp; Phối đồ
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>{article.categoryLabel}</span>
      </nav>

      <section className={styles.heroGrid} aria-label="Tiêu đề bài viết">
        <div className={styles.heroInfo}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {shortH1}
              </span>
            </span>
          </h1>
          <p
            className={styles.fullArticleTitle}
            data-rise=""
            style={{ "--i": 1 } as React.CSSProperties}
          >
            {article.title}
          </p>
          <p className={styles.summary} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            {article.summary}
          </p>
          <p className={styles.metaLine} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            {article.author} · {formatDateVi(article.publishedAt)} · {article.readMinutes} phút đọc
            · {formatInt(article.sampleViews)} lượt xem (mẫu)
          </p>
          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href="#embedded-products-heading">
              Chọn mua {allEmbeddedProducts.length} mẫu trong bài
            </Button>
            <Button variant="secondary" size="lg" href="/tap-chi">
              Tất cả bài viết
            </Button>
          </div>
        </div>

        <div className={styles.heroMedia} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
          <Image
            src={article.coverImage}
            alt={article.title}
            fill
            priority
            sizes="(max-width: 1080px) 100vw, 50vw"
            className={styles.coverImg}
          />
        </div>
      </section>

      <article className={styles.articleBody}>
        {article.sections.map((sec, sIdx) => {
          const sectionProducts = (sec.featuredProductIds ?? [])
            .map((id) => getProductById(id))
            .filter((p): p is Product => Boolean(p));

          return (
            <section
              key={sec.heading}
              className={styles.contentSection}
              aria-labelledby={`journal-sec-${sIdx}`}
            >
              <div
                className={styles.proseCol}
                data-cat-reveal=""
                style={{ "--stagger": 0 } as React.CSSProperties}
              >
                <h2 id={`journal-sec-${sIdx}`} className={styles.sectionHeading}>
                  {sec.heading}
                </h2>
                {sec.paragraphs.map((para) => (
                  <p key={para} className={styles.paragraph}>
                    {para}
                  </p>
                ))}
              </div>

              {sectionProducts.length > 0 && (
                <div className={styles.embedStack}>
                  {sectionProducts.map((prod, pIdx) => (
                    <div
                      key={prod.id}
                      data-cat-reveal=""
                      style={{ "--stagger": pIdx + 1 } as React.CSSProperties}
                    >
                      <EmbeddedProductBuyCard product={prod} />
                    </div>
                  ))}
                </div>
              )}
            </section>
          );
        })}
      </article>

      {isSizeGuideArticle && (
        <section className={styles.toolSection} aria-labelledby="journal-size-finder-heading">
          <h2 id="journal-size-finder-heading" className={styles.toolHeading}>
            Thử ngay bộ gợi ý size theo 3 dáng người
          </h2>
          <SizeFinder />
        </section>
      )}

      {isCareGuideArticle && (
        <section className={styles.toolSection} aria-labelledby="journal-care-table-heading">
          <h2 id="journal-care-table-heading" className={styles.toolHeading}>
            Bảng tra cứu nhanh chế độ giặt &amp; phơi
          </h2>
          <div className={styles.careTableWrap}>
            <table className={styles.careTable}>
              <thead>
                <tr>
                  <th>Nhóm chất liệu</th>
                  <th>Chế độ giặt</th>
                  <th>Phơi khô</th>
                  <th>Cần tránh</th>
                </tr>
              </thead>
              <tbody>
                {CARE_GUIDE_LIBRARY.map((entry) => (
                  <tr key={entry.id}>
                    <td>{entry.fabricGroup}</td>
                    <td>{entry.tempRule}</td>
                    <td>{entry.dryingRule}</td>
                    <td>{entry.avoidRule}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      <section className={styles.summaryBuySection} aria-labelledby="embedded-products-heading">
        <div
          className={styles.sectionHeader}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="embedded-products-heading" className={styles.toolHeading}>
            Sản phẩm được nhắc đến trong bài
          </h2>
          <Link href="/danh-muc" className={styles.allLink}>
            Xem toàn bộ 18 mẫu cơ bản →
          </Link>
        </div>

        <div className={styles.embeddedGrid}>
          {allEmbeddedProducts.map((prod, idx) => (
            <div
              key={prod.id}
              data-cat-reveal=""
              style={{ "--stagger": idx % 3 } as React.CSSProperties}
            >
              <EmbeddedProductBuyCard product={prod} />
            </div>
          ))}
        </div>
      </section>

      {/* Unboxed related articles strip */}
      <section className={styles.relatedSection} aria-labelledby="related-journal-heading">
        <h2
          id="related-journal-heading"
          className={styles.toolHeading}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Bài viết khác trong Sổ tay vải
        </h2>
        <div className={styles.relatedGrid}>
          {relatedArticles.map((rel, idx) => (
            <Link
              key={rel.slug}
              href={`/tap-chi/${rel.slug}`}
              className={styles.relatedItem}
              data-cat-reveal=""
              style={{ "--stagger": idx + 1 } as React.CSSProperties}
            >
              <div className={styles.relatedMedia}>
                <Image
                  src={rel.coverImage}
                  alt={rel.title}
                  fill
                  sizes="(max-width: 1080px) 100vw, 31vw"
                  className={styles.coverImg}
                />
              </div>
              <span className={styles.metaLine}>
                {rel.categoryLabel} · {rel.readMinutes} phút đọc
              </span>
              <span className={styles.relatedTitle}>{rel.title}</span>
            </Link>
          ))}
        </div>
      </section>
    </div>
  );
}
