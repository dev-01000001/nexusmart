"use client";

import React, { Suspense, useEffect, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  BRAND_STORY_PILLARS,
  BRAND_TIMELINE,
  PLATFORM_CHANGELOG,
  SHOPS,
  getProductsByShop,
} from "@/data";
import { FABRIC_MEDIA, WORKSHOP_MEDIA } from "@/data/media";
import { formatDateVi } from "@/lib/format";
import { Button, IconShield, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

type AboutTab = "story" | "timeline" | "factories" | "changelog";

const ABOUT_TABS: Array<{ id: AboutTab; label: string }> = [
  { id: "story", label: "Câu chuyện & Tiêu chuẩn vải" },
  { id: "timeline", label: "Cột mốc phát triển" },
  { id: "factories", label: "4 xưởng dệt đối tác" },
  { id: "changelog", label: "Nhật ký cập nhật" },
];

function AboutInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  const stateParam = searchParams.get("state");
  const tabParam = searchParams.get("tab") as AboutTab | null;

  const [activeTab, setActiveTab] = useState<AboutTab>(
    tabParam && ABOUT_TABS.some((t) => t.id === tabParam) ? tabParam : "story"
  );

  useRevealObserver(rootRef, `${activeTab}|${stateParam ?? ""}`);

  useEffect(() => {
    const vid = videoRef.current;
    if (!vid || typeof window === "undefined") return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const syncPlayback = () => {
      if (mq.matches) vid.pause();
      else vid.play().catch(() => {});
    };
    syncPlayback();
    mq.addEventListener?.("change", syncPlayback);
    return () => mq.removeEventListener?.("change", syncPlayback);
  }, [activeTab]);

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải hồ sơ NexusMart và 4 xưởng dệt..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải thông tin giới thiệu"
          message="Kết nối tạm thời gián đoạn. Vui lòng thử tải lại trang."
          actionLabel="Tải lại trang"
          onAction={() => router.replace("/ve-nexusmart")}
        />
      </div>
    );
  }

  if (stateParam === "empty") {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Chưa có mục nội dung được chọn"
          message="Bạn có thể xem toàn bộ sản phẩm từ bốn xưởng dệt trên NexusMart."
          actionLabel="Mở danh mục sản phẩm"
          actionHref="/danh-muc"
        />
      </div>
    );
  }

  return (
    <div ref={rootRef} className={styles.page}>
      <section className={styles.heroGrid} aria-label="Giới thiệu NexusMart">
        <div className={styles.heroText}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Bốn xưởng dệt Việt
              </span>
            </span>
            <span className="line">
              <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                may đồ mỗi ngày
              </span>
            </span>
          </h1>
          <p className={styles.lead} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            NexusMart kết nối trực tiếp bốn xưởng dệt tại Hải Phòng, Hà Nội, Đà Nẵng và TP. Hồ Chí
            Minh với định lượng vải GSM công khai, giá niêm yết rõ ràng và đổi trả miễn phí 45 ngày.
          </p>
          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href="/danh-muc">
              Xem 18 mẫu từ 4 xưởng
            </Button>
            <Button variant="secondary" size="lg" href="/chien-dich/soi-xanh-nexus">
              Chiến dịch Sợi Xanh Nexus
            </Button>
          </div>
        </div>

        <div className={styles.heroVisuals}>
          <figure
            className={styles.heroMainFigure}
            data-rise=""
            style={{ "--i": 1 } as React.CSSProperties}
          >
            <div className={styles.heroMainMedia}>
              <Image
                src={WORKSHOP_MEDIA.src}
                alt={WORKSHOP_MEDIA.alt}
                fill
                priority
                sizes="(max-width: 1080px) 100vw, 50vw"
                className={styles.heroPhotoImg}
              />
            </div>
            <figcaption className={styles.heroPhotoCaption}>
              Không gian xưởng dệt đối tác kiểm vải trực tiếp từng lô
            </figcaption>
          </figure>

          <div className={styles.heroSubPair}>
            <figure
              className={styles.heroSubFigure}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.heroSubMedia}>
                <Image
                  src={FABRIC_MEDIA.src}
                  alt={FABRIC_MEDIA.alt}
                  fill
                  priority
                  sizes="(max-width: 1080px) 50vw, 24vw"
                  className={styles.heroPhotoImg}
                />
              </div>
              <figcaption className={styles.heroPhotoCaption}>
                Đo kiểm sợi Cotton 220gsm &amp; Pique
              </figcaption>
            </figure>

            <figure
              className={styles.heroSubFigure}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <div className={styles.heroSubMedia}>
                <Image
                  src={WORKSHOP_MEDIA.greenFiber.src}
                  alt={WORKSHOP_MEDIA.greenFiber.alt}
                  fill
                  priority
                  sizes="(max-width: 1080px) 50vw, 24vw"
                  className={styles.heroPhotoImg}
                />
              </div>
              <figcaption className={styles.heroPhotoCaption}>
                Sợi bông, Lanh, Tre &amp; Modal
              </figcaption>
            </figure>
          </div>
        </div>
      </section>

      <section className={styles.tabsSection} aria-labelledby="about-workspace-heading">
        <div
          className={styles.tabsHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="about-workspace-heading" className={styles.sectionTitle}>
            Minh bạch từ sợi vải tới tay người mặc
          </h2>
          <div
            className={styles.tabsBar}
            role="tablist"
            aria-label="Chuyên mục giới thiệu NexusMart"
          >
            {ABOUT_TABS.map((tab) => {
              const selected = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${styles.tabBtn} ${selected ? styles.tabBtnActive : ""}`}
                  onClick={() => setActiveTab(tab.id)}
                >
                  {tab.label}
                </button>
              );
            })}
          </div>
        </div>

        {/* Tab 1: Unboxed Editorial Pillars + Loom Video + Dark Slate Standards Band */}
        {activeTab === "story" && (
          <div role="tabpanel" className={styles.panelStack}>
            <div className={styles.storyLeadGrid}>
              <div className={styles.pillarsCol}>
                {BRAND_STORY_PILLARS.map((pillar, idx) => (
                  <article
                    key={pillar.id}
                    className={styles.pillarEntry}
                    data-cat-reveal=""
                    style={{ "--stagger": idx } as React.CSSProperties}
                  >
                    <h3 className={styles.pillarTitle}>{pillar.title}</h3>
                    <p className={styles.pillarDetail}>{pillar.detail}</p>
                  </article>
                ))}
              </div>

              <div className={styles.videoCol}>
                <div
                  className={styles.videoFrame}
                  data-cat-reveal=""
                  style={{ "--stagger": 1 } as React.CSSProperties}
                >
                  <video
                    ref={videoRef}
                    className={styles.loomVideo}
                    src={WORKSHOP_MEDIA.loomVideo.src}
                    poster={WORKSHOP_MEDIA.loomVideo.poster}
                    width={WORKSHOP_MEDIA.loomVideo.width}
                    height={WORKSHOP_MEDIA.loomVideo.height}
                    autoPlay
                    muted
                    loop
                    playsInline
                    preload="metadata"
                    aria-label={WORKSHOP_MEDIA.loomVideo.alt}
                  />
                </div>
                <p
                  className={styles.videoCaption}
                  data-cat-reveal=""
                  style={{ "--stagger": 2 } as React.CSSProperties}
                >
                  Kiểm soát định lượng sợi GSM và độ co rút trước cắt rập là tiêu chuẩn bắt buộc đối
                  với mọi gian hàng tham gia NexusMart.
                </p>

                <dl
                  className={styles.standardBand}
                  data-cat-reveal=""
                  style={{ "--stagger": 3 } as React.CSSProperties}
                >
                  <div className={styles.specRow}>
                    <dt className={styles.specKey}>Chính sách đổi trả toàn sàn</dt>
                    <dd className={styles.specVal}>45 ngày miễn phí · đổi size 2 chiều tại nhà</dd>
                  </div>
                  <div className={styles.specRow}>
                    <dt className={styles.specKey}>Miễn phí vận chuyển</dt>
                    <dd className={styles.specVal}>Toàn sàn từ 249.000₫ · xưởng từ 199.000₫</dd>
                  </div>
                  <div className={styles.specRow}>
                    <dt className={styles.specKey}>Giá bán &amp; tình trạng hàng</dt>
                    <dd className={styles.specVal}>
                      Giá niêm yết rõ ràng · cập nhật còn hàng theo màu và size
                    </dd>
                  </div>
                </dl>
              </div>
            </div>
          </div>
        )}

        {/* Tab 2: Flat Milestone Timeline on single crisp white surface */}
        {activeTab === "timeline" && (
          <div role="tabpanel" className={styles.panelStack}>
            <div className={styles.timelineSurface}>
              {BRAND_TIMELINE.map((milestone, idx) => (
                <article
                  key={milestone.period}
                  className={styles.timelineRow}
                  data-cat-reveal=""
                  style={{ "--stagger": idx } as React.CSSProperties}
                >
                  <div className={styles.timelinePeriod}>{milestone.period}</div>
                  <div className={styles.timelineBody}>
                    <h3 className={styles.pillarTitle}>{milestone.title}</h3>
                    <p className={styles.pillarDetail}>{milestone.detail}</p>
                  </div>
                </article>
              ))}
            </div>
          </div>
        )}

        {/* Tab 3: 4 Factories & Quality Profiles */}
        {activeTab === "factories" && (
          <div role="tabpanel" className={styles.panelStack}>
            <div className={styles.factoriesGrid}>
              {SHOPS.map((shop, idx) => {
                const sampleProducts = getProductsByShop(shop.id).slice(0, 2);
                const fp = shop.factoryProfile;
                return (
                  <article
                    key={shop.id}
                    className={styles.factoryCard}
                    data-cat-reveal=""
                    style={{ "--stagger": idx % 2 } as React.CSSProperties}
                  >
                    <div className={styles.factoryHead}>
                      <div>
                        <h3 className={styles.factoryName}>{shop.name}</h3>
                        <p className={styles.factoryCity}>
                          {shop.city} · Thành lập năm {shop.foundedYear}
                        </p>
                      </div>
                      {shop.verifiedBadge && (
                        <span className={styles.verifiedTag}>
                          <IconShield size={14} />
                          <span>{shop.verifiedBadge}</span>
                        </span>
                      )}
                    </div>

                    {fp && (
                      <>
                        <dl className={styles.factoryMetaGrid}>
                          <div className={styles.specRow}>
                            <dt className={styles.factoryMetaKey}>Cơ sở sản xuất</dt>
                            <dd className={styles.factoryMetaVal}>{fp.factoryName}</dd>
                          </div>
                          <div className={styles.specRow}>
                            <dt className={styles.factoryMetaKey}>Công suất</dt>
                            <dd className={styles.factoryMetaVal}>{fp.monthlyCapacity}</dd>
                          </div>
                          <div className={styles.specRowFull}>
                            <dt className={styles.factoryMetaKey}>Địa chỉ nhà máy</dt>
                            <dd className={styles.factoryMetaVal}>{fp.factoryAddress}</dd>
                          </div>
                          <div className={styles.specRowFull}>
                            <dt className={styles.factoryMetaKey}>Nguồn sợi</dt>
                            <dd className={styles.factoryMetaVal}>{fp.fiberOrigin}</dd>
                          </div>
                        </dl>

                        <div className={styles.qcBox}>
                          <p className={styles.qcLabel}>Quy trình kiểm tra chất lượng tại xưởng:</p>
                          <ul className={styles.qcList}>
                            {fp.qcProcess.map((step) => (
                              <li key={step} className={styles.qcItem}>
                                {step}
                              </li>
                            ))}
                          </ul>
                        </div>
                      </>
                    )}

                    <div className={styles.factoryProductsStrip}>
                      {sampleProducts.map((prod) => {
                        const src = prod.onModelImage ?? prod.image;
                        const isModel = src.startsWith("/models/");
                        return (
                          <Link
                            key={prod.id}
                            href={`/san-pham/${prod.slug}`}
                            className={styles.factoryProdThumb}
                            title={prod.name}
                          >
                            <Image
                              src={src}
                              alt={prod.name}
                              fill
                              sizes="72px"
                              className={isModel ? styles.factoryProdModel : styles.factoryProdImg}
                            />
                          </Link>
                        );
                      })}
                      <div className={styles.factoryLinkBox}>
                        <Button variant="secondary" size="md" href={`/gian-hang/${shop.slug}`}>
                          Vào gian hàng {shop.shortName}
                        </Button>
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>
          </div>
        )}

        {/* Tab 4: Platform Changelog on single white surface */}
        {activeTab === "changelog" && (
          <div role="tabpanel" className={styles.panelStack}>
            <div className={styles.timelineSurface}>
              {PLATFORM_CHANGELOG.map((entry, idx) => (
                <article
                  key={entry.version}
                  className={styles.changelogRow}
                  data-cat-reveal=""
                  style={{ "--stagger": idx } as React.CSSProperties}
                >
                  <div className={styles.changelogMeta}>
                    <span className={styles.versionText}>Phiên bản {entry.version}</span>
                    <span className={styles.dateText}>{formatDateVi(entry.date)}</span>
                  </div>
                  <div className={styles.changelogBody}>
                    <h3 className={styles.pillarTitle}>{entry.title}</h3>
                    <ul className={styles.qcList}>
                      {entry.changes.map((chg) => (
                        <li key={chg} className={styles.qcItem}>
                          {chg}
                        </li>
                      ))}
                    </ul>
                  </div>
                </article>
              ))}
            </div>
          </div>
        )}
      </section>

      {/* Bottom accent-tint exploration band */}
      <section
        className={styles.ctaBand}
        aria-labelledby="about-explore-heading"
        data-cat-reveal=""
        style={{ "--stagger": 0 } as React.CSSProperties}
      >
        <div
          className={styles.ctaText}
          data-cat-reveal=""
          style={{ "--stagger": 1 } as React.CSSProperties}
        >
          <h2 id="about-explore-heading" className={styles.ctaHeading}>
            Khám phá chất liệu &amp; cộng đồng
          </h2>
          <p className={styles.ctaSub}>
            Tìm hiểu tiêu chuẩn sợi dệt đo kiểm GSM, hành trình Sợi Xanh Nexus và mẹo bảo quản vải.
          </p>
        </div>
        <div
          className={styles.ctaLinks}
          data-cat-reveal=""
          style={{ "--stagger": 2 } as React.CSSProperties}
        >
          <Button variant="primary" size="md" href="/chien-dich/cong-nghe-vai">
            Công nghệ vải NexusMart
          </Button>
          <Button variant="secondary" size="md" href="/chien-dich/soi-xanh-nexus">
            Chiến dịch Sợi Xanh Nexus
          </Button>
          <Button variant="secondary" size="md" href="/tap-chi">
            Sổ tay vải &amp; Phối đồ
          </Button>
        </div>
      </section>
    </div>
  );
}

export default function AboutNexusMartPage() {
  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang Về NexusMart..." rows={5} />
        </div>
      }
    >
      <AboutInner />
    </Suspense>
  );
}
