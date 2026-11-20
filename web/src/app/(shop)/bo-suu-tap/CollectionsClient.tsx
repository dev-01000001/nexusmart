"use client";

import React, { useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { CAMPAIGNS, COLLECTIONS, type CollectionItem, type Product } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { Button, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface CollectionsClientProps {
  products: Product[];
  loadError: string | null;
}

type ThemeFilter = "all" | "daily" | "active" | "community";

const THEME_TABS: Array<{ id: ThemeFilter; label: string }> = [
  { id: "all", label: "Tất cả bộ sưu tập" },
  { id: "daily", label: "Thường nhật & Công sở" },
  { id: "active", label: "Thể thao & Vận động" },
  { id: "community", label: "Cộng đồng & Outlet" },
];

const HERO_COLLAGE_ITEMS = [
  {
    href: "/bo-suu-tap/co-ban-hang-ngay",
    asset: MODEL_SHOTS["prod-at-trang"],
    label: "Cơ bản hằng ngày",
  },
  {
    href: "/bo-suu-tap/chay-bo-tap-luyen",
    asset: MODEL_SHOTS["prod-at-dryfit"],
    label: "Chạy bộ & Tập luyện",
  },
  {
    href: "/bo-suu-tap/cong-so-gon-gang",
    asset: MODEL_SHOTS["prod-polo-xanhda"],
    label: "Công sở gọn gàng",
  },
];

function matchesTheme(col: CollectionItem, theme: ThemeFilter): boolean {
  if (theme === "all") return true;
  if (theme === "daily") {
    return col.slug === "co-ban-hang-ngay" || col.slug === "cong-so-gon-gang";
  }
  if (theme === "active") return col.slug === "chay-bo-tap-luyen";
  return col.slug === "soi-xanh-nexus" || col.slug === "san-outlet";
}

export function CollectionsClient({ products, loadError }: CollectionsClientProps) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);

  const stateParam = searchParams.get("state");
  const [theme, setTheme] = useState<ThemeFilter>("all");

  const productById = useMemo(() => new Map(products.map((p) => [p.id, p])), [products]);

  const visibleCollections = useMemo(
    () => COLLECTIONS.filter((col) => matchesTheme(col, theme)),
    [theme]
  );

  useRevealObserver(rootRef, `${theme}|${stateParam ?? ""}`);

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải danh sách bộ sưu tập theo nhu cầu..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error" || (loadError && products.length === 0)) {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải bộ sưu tập lúc này"
          message={loadError ?? "Hệ thống nội dung bộ sưu tập tạm gián đoạn. Vui lòng thử tải lại."}
          actionLabel="Tải lại bộ sưu tập"
          onAction={() => {
            if (stateParam === "error") {
              router.replace("/bo-suu-tap");
            } else {
              router.refresh();
            }
          }}
        />
      </div>
    );
  }

  if (stateParam === "empty") {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Chưa có bộ sưu tập trong nhóm này"
          message="Bạn có thể xem toàn bộ sản phẩm cơ bản từ các xưởng dệt tại danh mục chính."
          actionLabel="Mở danh mục sản phẩm"
          actionHref="/danh-muc"
        />
      </div>
    );
  }

  return (
    <div ref={rootRef} className={styles.page}>
      <section className={styles.heroGrid} aria-label="Tổng quan bộ sưu tập NexusMart">
        <div className={styles.heroText}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Bộ sưu tập
              </span>
            </span>
            <span className="line">
              <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                theo nhu cầu mặc
              </span>
            </span>
          </h1>
          <p className={styles.lead} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            Năm tuyển tập đồ basic từ bốn xưởng dệt Việt Nam, chọn lọc theo từng hoàn cảnh mặc hằng
            ngày, công sở và vận động.
          </p>
          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href="/bo-suu-tap/co-ban-hang-ngay">
              Xem BST Cơ bản hằng ngày
            </Button>
            <Button variant="secondary" size="lg" href="/danh-muc">
              {products.length > 0 ? `Xem toàn bộ ${products.length} mẫu` : "Xem danh mục sản phẩm"}
            </Button>
          </div>
        </div>

        <div className={styles.heroCollage}>
          {HERO_COLLAGE_ITEMS.map((item, idx) => (
            <Link
              key={item.href}
              href={item.href}
              className={styles.collageTile}
              data-rise=""
              style={{ "--i": idx + 1 } as React.CSSProperties}
            >
              <Image
                src={item.asset.src}
                alt={item.asset.alt}
                fill
                priority
                sizes="(max-width: 1080px) 33vw, 18vw"
                className={styles.collageImg}
              />
            </Link>
          ))}
        </div>
      </section>

      <section className={styles.listSection} aria-labelledby="collections-list-heading">
        <div
          className={styles.sectionHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="collections-list-heading" className={styles.sectionTitle}>
            Duyệt theo chủ đề và chất liệu
          </h2>
          <div className={styles.themeTabs} role="tablist" aria-label="Lọc chủ đề bộ sưu tập">
            {THEME_TABS.map((tab) => (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={theme === tab.id}
                className={`${styles.themeBtn} ${theme === tab.id ? styles.themeBtnActive : ""}`}
                onClick={() => setTheme(tab.id)}
              >
                {tab.label}
              </button>
            ))}
          </div>
        </div>

        <div className={styles.collectionRows}>
          {visibleCollections.map((col, idx) => {
            const matchedProducts = col.productIds
              .map((id) => productById.get(id))
              .filter((p): p is Product => Boolean(p));
            const previewProducts = matchedProducts.slice(0, 2);

            return (
              <article key={col.slug} className={styles.collectionRow}>
                <div
                  className={styles.rowInfo}
                  data-cat-reveal=""
                  style={{ "--stagger": 0 } as React.CSSProperties}
                >
                  <h3 className={styles.rowTitle}>
                    <Link href={`/bo-suu-tap/${col.slug}`} className={styles.rowTitleLink}>
                      {col.title}
                    </Link>
                  </h3>
                  <p className={styles.seasonMeta}>
                    {col.seasonLabel} · {matchedProducts.length || col.productIds.length} sản phẩm
                  </p>
                  <p className={styles.rowSubtitle}>{col.subtitle}</p>
                  <p className={styles.rowFabric}>{col.fabricHighlight}</p>

                  <ul className={styles.benefitList}>
                    {col.benefitPoints.map((pt) => (
                      <li key={pt} className={styles.benefitItem}>
                        {pt}
                      </li>
                    ))}
                  </ul>

                  <div className={styles.rowActions}>
                    <Button variant="primary" size="md" href={`/bo-suu-tap/${col.slug}`}>
                      Xem bộ sưu tập
                    </Button>
                    {col.slug === "soi-xanh-nexus" && (
                      <Button variant="secondary" size="md" href="/chien-dich/soi-xanh-nexus">
                        Xem tiến độ quỹ Sợi Xanh
                      </Button>
                    )}
                  </div>
                </div>

                <div
                  className={styles.rowGallery}
                  data-cat-reveal=""
                  style={{ "--stagger": 1 } as React.CSSProperties}
                >
                  <Link href={`/bo-suu-tap/${col.slug}`} className={styles.rowBannerFrame}>
                    <Image
                      src={col.heroImage}
                      alt={col.title}
                      fill
                      priority={idx === 0}
                      sizes="(max-width: 1080px) 100vw, 45vw"
                      className={styles.rowBannerImg}
                    />
                  </Link>
                  {previewProducts.length > 0 && (
                    <div className={styles.rowModelPair}>
                      {previewProducts.map((prod) => {
                        const imgSrc = prod.onModelImage ?? prod.image;
                        const isModel = imgSrc.startsWith("/models/");
                        return (
                          <Link
                            key={prod.id}
                            href={`/san-pham/${prod.slug}`}
                            className={styles.rowMiniThumb}
                            title={prod.name}
                          >
                            <Image
                              src={imgSrc}
                              alt={MODEL_SHOTS[prod.id]?.alt ?? prod.name}
                              fill
                              sizes="(max-width: 1080px) 50vw, 22vw"
                              className={isModel ? styles.rowModelImg : styles.rowBannerImg}
                            />
                          </Link>
                        );
                      })}
                    </div>
                  )}
                </div>
              </article>
            );
          })}
        </div>
      </section>

      <section className={styles.campaignBand} aria-labelledby="collections-campaigns-heading">
        <div
          className={styles.campaignBandHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="collections-campaigns-heading" className={styles.campaignBandTitle}>
            Chiến dịch chuyên đề &amp; Công nghệ sợi
          </h2>
          <p className={styles.campaignBandLead}>
            Tìm hiểu định lượng vải đo kiểm tại xưởng và hành trình đóng góp áo ấm vùng cao cùng Sợi
            Xanh Nexus.
          </p>
        </div>

        <div className={styles.campaignGrid}>
          {CAMPAIGNS.map((camp, idx) => (
            <Link
              key={camp.slug}
              href={`/chien-dich/${camp.slug}`}
              className={styles.campaignPanel}
              data-cat-reveal=""
              style={{ "--stagger": idx + 1 } as React.CSSProperties}
            >
              <div className={styles.campaignMedia}>
                <Image
                  src={camp.heroImage}
                  alt={camp.title}
                  fill
                  sizes="(max-width: 768px) 100vw, 48vw"
                  className={styles.campaignImg}
                />
              </div>
              <div className={styles.campaignBody}>
                <h3 className={styles.campaignTitle}>{camp.title}</h3>
                <p className={styles.campaignDesc}>{camp.subtitle}</p>
                <span className={styles.campaignCta}>Khám phá chiến dịch →</span>
              </div>
            </Link>
          ))}
        </div>
      </section>
    </div>
  );
}
