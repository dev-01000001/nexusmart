"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  CAMPAIGNS,
  CARE_GUIDE_LIBRARY,
  getCampaignBySlug,
  getProductById,
  type Product,
} from "@/data";
import { WORKSHOP_MEDIA } from "@/data/media";
import { formatDateVi, formatInt, formatPercent, formatVnd } from "@/lib/format";
import { Button, ProductCard, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

const TECH_TO_SUBTAB: Record<string, string> = {
  "tech-compact": "cotton-compact",
  "tech-dryfit": "dryfit-tech",
  "tech-modal-bamboo": "plant-fiber",
  "tech-ripstop": "dryfit-tech",
};

const TECH_MEDIA: Record<string, { src: string; isModel?: boolean }> = {
  "tech-compact": { src: "/banners/fabric-closeup.jpg" },
  "tech-dryfit": { src: "/banners/campaign-running.jpg" },
  "tech-modal-bamboo": { src: "/banners/campaign-green-fiber.jpg" },
  "tech-ripstop": { src: "/models/model-ao-khoac-gio.jpg", isModel: true },
};

const HERO_HEADLINE_LINES: Record<string, [string, string]> = {
  "cong-nghe-vai": ["Công nghệ vải", "đo kiểm định lượng"],
  "soi-xanh-nexus": ["Sợi Xanh Nexus", "góp áo ấm vùng cao"],
};

export function CampaignDetailClient({ slug }: { slug: string }) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  const stateParam = searchParams.get("state");
  const campaign = useMemo(() => getCampaignBySlug(slug), [slug]);

  const [activeSubTabId, setActiveSubTabId] = useState<string>(campaign?.subTabs[0]?.id ?? "all");
  const [statementShopFilter, setStatementShopFilter] = useState<string>("all");

  useEffect(() => {
    if (campaign?.subTabs[0]?.id) {
      setActiveSubTabId(campaign.subTabs[0].id);
    }
    setStatementShopFilter("all");
  }, [campaign]);

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
  }, [slug]);

  const activeSubTab = useMemo(() => {
    if (!campaign) return undefined;
    return campaign.subTabs.find((t) => t.id === activeSubTabId) ?? campaign.subTabs[0];
  }, [activeSubTabId, campaign]);

  const displayedProducts = useMemo<Product[]>(() => {
    if (!campaign) return [];
    const ids = activeSubTab?.productIds ?? campaign.productIds;
    return ids.map((id) => getProductById(id)).filter((p): p is Product => Boolean(p));
  }, [activeSubTab, campaign]);

  const filteredStatementRows = useMemo(() => {
    const rows = campaign?.csrProgress?.statementRows ?? [];
    if (statementShopFilter === "all") return rows;
    return rows.filter((r) => r.shopName === statementShopFilter);
  }, [campaign, statementShopFilter]);

  const statementSumVnd = useMemo(
    () => filteredStatementRows.reduce((sum, r) => sum + r.amountVnd, 0),
    [filteredStatementRows]
  );

  const statementOrdersSum = useMemo(
    () => filteredStatementRows.reduce((sum, r) => sum + r.ordersCount, 0),
    [filteredStatementRows]
  );

  useRevealObserver(
    rootRef,
    `${slug}|${activeSubTabId}|${statementShopFilter}|${stateParam ?? ""}`
  );

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải dữ liệu chiến dịch NexusMart..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải trang chiến dịch lúc này"
          message="Dữ liệu chiến dịch tạm thời gián đoạn. Vui lòng thử tải lại."
          actionLabel="Tải lại chiến dịch"
          onAction={() => router.replace(`/chien-dich/${slug}`)}
        />
      </div>
    );
  }

  if (stateParam === "empty" || !campaign) {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy chiến dịch này"
          message="Bạn có thể xem chiến dịch Công nghệ vải hoặc Sợi Xanh Nexus đang diễn ra."
          actionLabel="Xem bộ sưu tập NexusMart"
          actionHref="/bo-suu-tap"
        />
      </div>
    );
  }

  const otherCampaign = CAMPAIGNS.find((c) => c.slug !== campaign.slug);
  const csr = campaign.csrProgress;
  const csrRatio = csr ? Math.min(1, csr.currentRaisedVnd / Math.max(1, csr.targetVnd)) : 0;
  const statementShops = csr ? Array.from(new Set(csr.statementRows.map((r) => r.shopName))) : [];
  const heroLines = HERO_HEADLINE_LINES[campaign.slug] ?? [campaign.title, ""];

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <Link href="/bo-suu-tap" className={styles.crumbLink}>
          Bộ sưu tập
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>{campaign.title}</span>
      </nav>

      {/* Campaign Hero */}
      <section className={styles.heroGrid} aria-label={`Chiến dịch ${campaign.title}`}>
        <div className={styles.heroInfo}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {heroLines[0]}
              </span>
            </span>
            {heroLines[1] && (
              <span className="line">
                <span data-line="" style={{ "--i": 1 } as React.CSSProperties}>
                  {heroLines[1]}
                </span>
              </span>
            )}
          </h1>
          <p className={styles.subtitle} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            {campaign.subtitle}
          </p>
          <p className={styles.intro} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            {campaign.intro}
          </p>

          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href="#campaign-products-heading">
              Xem {campaign.productIds.length} sản phẩm áp dụng
            </Button>
            {csr ? (
              <Button variant="secondary" size="lg" href="#csr-statement-heading">
                Xem bảng tổng hợp đóng góp
              </Button>
            ) : (
              <Button variant="secondary" size="lg" href="#fabric-tech-heading">
                Xem 4 nền tảng sợi
              </Button>
            )}
          </div>
        </div>

        <div
          className={styles.heroMediaStack}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.heroMainMedia}>
            <Image
              src={campaign.heroImage}
              alt={campaign.title}
              fill
              priority
              sizes="(max-width: 1080px) 100vw, 50vw"
              className={styles.mediaImg}
            />
          </div>

          {campaign.kind === "fabric-tech" ? (
            <div className={styles.heroSubMedia}>
              <video
                ref={videoRef}
                className={styles.mediaImg}
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
          ) : (
            <div className={styles.heroSubMedia}>
              <Image
                src={WORKSHOP_MEDIA.src}
                alt={WORKSHOP_MEDIA.alt}
                fill
                priority
                sizes="(max-width: 1080px) 100vw, 45vw"
                className={styles.mediaImg}
              />
            </div>
          )}
        </div>
      </section>

      {/* Fabric Tech Showcase (cong-nghe-vai) — unboxed alternating editorial rows (zero card-rows) */}
      {campaign.kind === "fabric-tech" && campaign.fabricTechCards && (
        <section className={styles.techSection} aria-labelledby="fabric-tech-heading">
          <h2
            id="fabric-tech-heading"
            className={styles.sectionTitle}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            Bốn nền tảng chất liệu đo kiểm GSM
          </h2>

          <div className={styles.techList}>
            {campaign.fabricTechCards.map((card, idx) => {
              const mediaInfo = TECH_MEDIA[card.id] ?? { src: "/banners/fabric-closeup.jpg" };
              const reverse = idx % 2 === 1;
              return (
                <article
                  key={card.id}
                  className={`${styles.techRow} ${reverse ? styles.techRowReverse : ""}`}
                >
                  <div
                    className={styles.techMedia}
                    data-cat-reveal=""
                    style={{ "--stagger": 0 } as React.CSSProperties}
                  >
                    <Image
                      src={mediaInfo.src}
                      alt={card.name}
                      fill
                      sizes="(max-width: 1080px) 100vw, 45vw"
                      className={mediaInfo.isModel ? styles.mediaImgModel : styles.mediaImg}
                    />
                  </div>
                  <div
                    className={styles.techBody}
                    data-cat-reveal=""
                    style={{ "--stagger": 1 } as React.CSSProperties}
                  >
                    <h3 className={styles.techTitle}>{card.name}</h3>
                    <span className={styles.techMeta}>
                      Định lượng {card.gsmRange} · {card.productIds.length} mẫu áp dụng
                    </span>
                    <p className={styles.techComp}>{card.composition}</p>
                    <p className={styles.techBenefit}>{card.benefit}</p>
                    <div className={styles.techAction}>
                      <button
                        type="button"
                        className={styles.jumpFilterBtn}
                        onClick={() => {
                          const targetSub = TECH_TO_SUBTAB[card.id];
                          if (targetSub) setActiveSubTabId(targetSub);
                          const el = document.getElementById("campaign-products-heading");
                          el?.scrollIntoView({ behavior: "smooth", block: "start" });
                        }}
                      >
                        Lọc lưới mua nhanh bên dưới →
                      </button>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}

      {/* CSR Progress Band (soi-xanh-nexus) — bold dark slate palette field */}
      {campaign.kind === "csr" && csr && (
        <section
          className={styles.csrSection}
          aria-labelledby="csr-progress-heading"
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <div className={styles.csrTop}>
            <div className={styles.csrHeadingBlock}>
              <h2 id="csr-progress-heading" className={styles.csrTitle}>
                Tiến độ đóng góp Sợi Xanh Nexus
              </h2>
              <p className={styles.sampleNotice}>
                Số liệu mẫu minh họa tiến độ đóng góp công khai theo từng đợt trên NexusMart.
              </p>
            </div>
            <div className={styles.csrNumbers}>
              <span className={styles.csrRaised}>{formatVnd(csr.currentRaisedVnd)}</span>
              <span className={styles.csrTarget}>
                trên mục tiêu {formatVnd(csr.targetVnd)} ({formatPercent(csrRatio, 1, true)})
              </span>
            </div>
          </div>

          <div
            className={styles.progressTrack}
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={csr.targetVnd}
            aria-valuenow={csr.currentRaisedVnd}
            aria-label="Tiến độ đóng góp quỹ Sợi Xanh Nexus"
          >
            <div
              className={styles.progressFill}
              style={{ width: `${Math.round(csrRatio * 100)}%` }}
            />
          </div>

          <dl className={styles.csrFacts}>
            <div
              className={styles.csrFactItem}
              data-cat-reveal=""
              style={{ "--stagger": 1 } as React.CSSProperties}
            >
              <dt className={styles.csrFactKey}>Cơ chế đóng góp</dt>
              <dd className={styles.csrFactVal}>{csr.contributionRule}</dd>
            </div>
            <div
              className={styles.csrFactItem}
              data-cat-reveal=""
              style={{ "--stagger": 2 } as React.CSSProperties}
            >
              <dt className={styles.csrFactKey}>Đơn hàng ghi nhận (mẫu)</dt>
              <dd className={styles.csrFactVal}>{formatInt(csr.ordersContributed)} đơn hoàn tất</dd>
            </div>
            <div
              className={styles.csrFactItem}
              data-cat-reveal=""
              style={{ "--stagger": 3 } as React.CSSProperties}
            >
              <dt className={styles.csrFactKey}>Mục đích thụ hưởng</dt>
              <dd className={styles.csrFactVal}>{csr.beneficiary}</dd>
            </div>
          </dl>
        </section>
      )}

      {/* Sub-tabs & Quick-Buy Product Grid */}
      <section className={styles.productsSection} aria-labelledby="campaign-products-heading">
        <div
          className={styles.sectionHeader}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="campaign-products-heading" className={styles.sectionTitle}>
            {campaign.kind === "csr"
              ? "Bộ sưu tập Sợi Xanh góp 10% doanh thu"
              : "Lưới mua nhanh theo nhóm công nghệ sợi"}
          </h2>
          <span className={styles.countMeta}>{displayedProducts.length} sản phẩm</span>
        </div>

        <div className={styles.subTabsBar} role="tablist" aria-label="Tab phân nhóm chiến dịch">
          {campaign.subTabs.map((tab) => {
            const selected = activeSubTab?.id === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={selected}
                className={`${styles.tabBtn} ${selected ? styles.tabBtnActive : ""}`}
                onClick={() => setActiveSubTabId(tab.id)}
              >
                {tab.label} ({tab.productIds.length})
              </button>
            );
          })}
        </div>

        {activeSubTab && <p className={styles.subTabDesc}>{activeSubTab.description}</p>}

        <div className={styles.productGrid}>
          {displayedProducts.map((product, idx) => (
            <div
              key={product.id}
              data-cat-reveal=""
              style={{ "--stagger": idx % 4 } as React.CSSProperties}
            >
              <ProductCard
                product={product}
                showSpec={true}
                showSla={true}
                showBadges={true}
                showWishlist={true}
                enableQuickAdd={true}
                enableInteractiveSwatches={true}
                priority={idx < 4}
              />
            </div>
          ))}
        </div>
      </section>

      {/* CSR Statement Table (soi-xanh-nexus) */}
      {campaign.kind === "csr" && csr && (
        <section className={styles.statementSection} aria-labelledby="csr-statement-heading">
          <div
            className={styles.sectionHeader}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            <div>
              <h2 id="csr-statement-heading" className={styles.sectionTitle}>
                Bảng tổng hợp đóng góp minh bạch (dữ liệu mẫu)
              </h2>
              <p className={styles.statementNote}>
                Tổng hợp từng đợt đóng góp từ 4 xưởng dệt, cộng khớp toàn bộ{" "}
                {formatVnd(csr.currentRaisedVnd)}.
              </p>
            </div>

            <div className={styles.subTabsBar} role="group" aria-label="Lọc tổng hợp theo xưởng">
              <button
                type="button"
                className={`${styles.tabBtn} ${statementShopFilter === "all" ? styles.tabBtnActive : ""}`}
                onClick={() => setStatementShopFilter("all")}
                aria-pressed={statementShopFilter === "all"}
              >
                Tất cả 4 xưởng
              </button>
              {statementShops.map((shopName) => (
                <button
                  key={shopName}
                  type="button"
                  className={`${styles.tabBtn} ${
                    statementShopFilter === shopName ? styles.tabBtnActive : ""
                  }`}
                  onClick={() => setStatementShopFilter(shopName)}
                  aria-pressed={statementShopFilter === shopName}
                >
                  {shopName}
                </button>
              ))}
            </div>
          </div>

          <div
            className={styles.tableWrap}
            data-cat-reveal=""
            style={{ "--stagger": 1 } as React.CSSProperties}
          >
            <table className={styles.statementTable}>
              <thead>
                <tr>
                  <th>Ngày ghi nhận</th>
                  <th>Mã đợt</th>
                  <th>Nội dung đợt đóng góp</th>
                  <th>Gian hàng xưởng dệt</th>
                  <th className={styles.numRight}>Số đơn</th>
                  <th className={styles.numRight}>Giá trị đóng góp</th>
                </tr>
              </thead>
              <tbody>
                {filteredStatementRows.map((row) => (
                  <tr key={row.id}>
                    <td data-label="Ngày ghi nhận">{formatDateVi(row.date)}</td>
                    <td data-label="Mã đợt">{row.id.toUpperCase()}</td>
                    <td data-label="Nội dung">{row.description}</td>
                    <td data-label="Xưởng dệt">{row.shopName}</td>
                    <td data-label="Số đơn" className={styles.numRight}>
                      {formatInt(row.ordersCount)}
                    </td>
                    <td data-label="Đóng góp" className={styles.numRight}>
                      {formatVnd(row.amountVnd)}
                    </td>
                  </tr>
                ))}
              </tbody>
              <tfoot>
                <tr className={styles.totalRow}>
                  <td colSpan={4} data-label="Tổng hợp">
                    Tổng cộng{" "}
                    {statementShopFilter === "all"
                      ? "(khớp tiến độ quỹ Sợi Xanh)"
                      : `(${statementShopFilter})`}
                  </td>
                  <td data-label="Tổng số đơn" className={styles.numRight}>
                    {formatInt(statementOrdersSum)}
                  </td>
                  <td data-label="Tổng đóng góp" className={styles.numRight}>
                    {formatVnd(statementSumVnd)}
                  </td>
                </tr>
              </tfoot>
            </table>
          </div>
        </section>
      )}

      {/* Fabric Care Library for fabric-tech campaign — unified accent-tint band, unboxed columns */}
      {campaign.kind === "fabric-tech" && (
        <section className={styles.careBand} aria-labelledby="campaign-care-heading">
          <h2
            id="campaign-care-heading"
            className={styles.sectionTitle}
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            Bảo quản đúng cách theo công nghệ sợi
          </h2>
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
      )}

      {/* Link to companion campaign */}
      {otherCampaign && (
        <section
          className={styles.otherSection}
          aria-labelledby="other-campaign-heading"
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="other-campaign-heading" className={styles.otherHeading}>
            Chiến dịch chuyên đề khác
          </h2>
          <Link href={`/chien-dich/${otherCampaign.slug}`} className={styles.otherBanner}>
            <div className={styles.otherInfo}>
              <span className={styles.otherTitle}>{otherCampaign.title}</span>
              <p className={styles.otherSub}>{otherCampaign.subtitle}</p>
              <span className={styles.jumpFilterBtn}>Xem chiến dịch →</span>
            </div>
            <div className={styles.otherMedia}>
              <Image
                src={otherCampaign.heroImage}
                alt={otherCampaign.title}
                fill
                sizes="(max-width: 1080px) 100vw, 42vw"
                className={styles.mediaImg}
              />
            </div>
          </Link>
        </section>
      )}
    </div>
  );
}
