"use client";

import React, { Suspense, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { CARE_GUIDE_LIBRARY, JOURNAL_ARTICLES, getProductById, type JournalCategory } from "@/data";
import { formatDateVi, formatInt } from "@/lib/format";
import { Button, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

const CATEGORY_TABS: Array<{ id: "all" | JournalCategory; label: string }> = [
  { id: "all", label: "Tất cả bài viết" },
  { id: "chat-lieu", label: "Chất liệu vải" },
  { id: "phoi-do", label: "Phối đồ & Size" },
  { id: "the-thao", label: "Thể thao" },
  { id: "bao-quan", label: "Bảo quản" },
];

function JournalIndexInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);

  const stateParam = searchParams.get("state");
  const catQuery = (searchParams.get("cat") ?? "all") as "all" | JournalCategory;
  const [activeCat, setActiveCat] = useState<"all" | JournalCategory>(
    CATEGORY_TABS.some((t) => t.id === catQuery) ? catQuery : "all"
  );

  const filteredArticles = useMemo(() => {
    if (activeCat === "all") return JOURNAL_ARTICLES;
    return JOURNAL_ARTICLES.filter((a) => a.category === activeCat);
  }, [activeCat]);

  const leadArticle = JOURNAL_ARTICLES[0];

  useRevealObserver(rootRef, `${activeCat}|${stateParam ?? ""}`);

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải bài viết Sổ tay vải & Phối đồ..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải tạp chí lúc này"
          message="Hệ thống bài viết tạm thời gián đoạn. Vui lòng thử tải lại."
          actionLabel="Tải lại tạp chí"
          onAction={() => router.replace("/tap-chi")}
        />
      </div>
    );
  }

  if (stateParam === "empty") {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Chưa có bài viết trong chuyên mục này"
          message="Bạn có thể xem toàn bộ hướng dẫn định lượng vải, chọn size và phối đồ cơ bản."
          actionLabel="Xem tất cả bài viết"
          actionHref="/tap-chi"
        />
      </div>
    );
  }

  return (
    <div ref={rootRef} className={styles.page}>
      <section className={styles.heroGrid} aria-label="Giới thiệu Sổ tay vải và Phối đồ">
        <div className={styles.heroText}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Sổ tay vải
              </span>
            </span>
            <span className="line">
              <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                &amp; Phối đồ
              </span>
            </span>
          </h1>
          <p className={styles.lead} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            Ghi chép thực tế về định lượng GSM, cách chọn size theo 3 dáng người ở 4 xưởng dệt và
            mẹo giặt ủi giữ đồ basic bền dáng nhiều mùa.
          </p>
          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href={`/tap-chi/${leadArticle.slug}`}>
              Đọc bài mới nhất
            </Button>
            <Button
              variant="secondary"
              size="lg"
              href="/tap-chi/huong-dan-chon-size-theo-3-dang-nguoi-va-form-xuong"
            >
              Hướng dẫn chọn size
            </Button>
          </div>
        </div>

        <Link
          href={`/tap-chi/${leadArticle.slug}`}
          className={styles.heroFeatureCard}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.heroFeatureMedia}>
            <Image
              src={leadArticle.coverImage}
              alt={leadArticle.title}
              fill
              priority
              sizes="(max-width: 1080px) 100vw, 50vw"
              className={styles.coverImg}
            />
          </div>
          <div className={styles.heroFeatureCaption}>
            <span className={styles.articleMeta}>
              {leadArticle.categoryLabel} · {formatDateVi(leadArticle.publishedAt)} ·{" "}
              {formatInt(leadArticle.sampleViews)} lượt xem (mẫu)
            </span>
            <span className={styles.heroFeatureTitle}>{leadArticle.title}</span>
          </div>
        </Link>
      </section>

      {/* Unboxed editorial magazine grid (zero card-rows) */}
      <section className={styles.articlesSection} aria-labelledby="journal-list-heading">
        <div
          className={styles.sectionHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="journal-list-heading" className={styles.sectionTitle}>
            Bài viết theo chuyên mục
          </h2>
          <div className={styles.tabs} role="tablist" aria-label="Lọc chuyên mục tạp chí">
            {CATEGORY_TABS.map((tab) => {
              const active = activeCat === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  className={`${styles.tabBtn} ${active ? styles.tabBtnActive : ""}`}
                  onClick={() => setActiveCat(tab.id)}
                >
                  {tab.label}
                </button>
              );
            })}
          </div>
        </div>

        <div className={styles.articlesGrid}>
          {filteredArticles.map((article, idx) => {
            const embedded = article.embeddedProductIds
              .map((id) => getProductById(id))
              .filter((p): p is NonNullable<typeof p> => Boolean(p))
              .slice(0, 3);

            return (
              <article key={article.slug} className={styles.articleEntry}>
                <Link
                  href={`/tap-chi/${article.slug}`}
                  className={styles.cardMedia}
                  data-cat-reveal=""
                  style={{ "--stagger": idx % 2 } as React.CSSProperties}
                >
                  <Image
                    src={article.coverImage}
                    alt={article.title}
                    fill
                    priority={idx < 2}
                    sizes="(max-width: 768px) 100vw, 48vw"
                    className={styles.coverImg}
                  />
                </Link>

                <div
                  className={styles.cardBody}
                  data-cat-reveal=""
                  style={{ "--stagger": (idx % 2) + 1 } as React.CSSProperties}
                >
                  <p className={styles.articleMeta}>
                    {article.categoryLabel} · {article.readMinutes} phút đọc ·{" "}
                    {formatInt(article.sampleViews)} lượt xem (mẫu)
                  </p>

                  <h3 className={styles.cardTitle}>
                    <Link href={`/tap-chi/${article.slug}`} className={styles.cardTitleLink}>
                      {article.title}
                    </Link>
                  </h3>

                  <p className={styles.cardSummary}>{article.summary}</p>

                  {embedded.length > 0 && (
                    <div className={styles.embeddedPreview}>
                      <span className={styles.embeddedLabel}>
                        {article.embeddedProductIds.length} sản phẩm nhắc trong bài:
                      </span>
                      <div className={styles.embeddedThumbs}>
                        {embedded.map((prod) => {
                          const thumbSrc = prod.onModelImage ?? prod.image;
                          const isModel = thumbSrc.startsWith("/models/");
                          return (
                            <Link
                              key={prod.id}
                              href={`/san-pham/${prod.slug}`}
                              className={styles.miniThumbLink}
                              title={prod.name}
                            >
                              <Image
                                src={thumbSrc}
                                alt={prod.shortName}
                                fill
                                sizes="64px"
                                className={isModel ? styles.miniThumbModel : styles.miniThumbImg}
                              />
                            </Link>
                          );
                        })}
                      </div>
                    </div>
                  )}

                  <div className={styles.cardFoot}>
                    <span className={styles.authorMeta}>
                      {article.author} · {formatDateVi(article.publishedAt)}
                    </span>
                    <Link href={`/tap-chi/${article.slug}`} className={styles.readLink}>
                      Đọc bài &amp; chọn mua →
                    </Link>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      </section>

      {/* Care guide — unified deep slate band with 3 unboxed reading columns (zero card-rows) */}
      <section className={styles.careBand} aria-labelledby="journal-care-heading">
        <div
          className={styles.sectionHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="journal-care-heading" className={styles.careBandTitle}>
            Tóm tắt mẹo bảo quản sợi vải
          </h2>
          <Link href="/#chon-size" className={styles.careBandLink}>
            Mở công cụ chọn size 3 dáng người →
          </Link>
        </div>

        <div className={styles.careCols}>
          {CARE_GUIDE_LIBRARY.map((entry, idx) => (
            <div
              key={entry.id}
              className={styles.careCol}
              data-cat-reveal=""
              style={{ "--stagger": idx + 1 } as React.CSSProperties}
            >
              <h3 className={styles.careTitle}>{entry.fabricGroup}</h3>
              <p className={styles.careLine}>{entry.tempRule}</p>
              <p className={styles.careLine}>{entry.dryingRule}</p>
              <p className={styles.careLine}>{entry.avoidRule}</p>
              <p className={styles.careTip}>{entry.tip}</p>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

export default function JournalPage() {
  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải Sổ tay vải & Phối đồ..." rows={5} />
        </div>
      }
    >
      <JournalIndexInner />
    </Suspense>
  );
}
