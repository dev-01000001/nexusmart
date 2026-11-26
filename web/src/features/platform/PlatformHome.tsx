import React from "react";
import Image from "next/image";
import Link from "next/link";
import {
  IconChat,
  IconCheck,
  IconClock,
  IconDashboard,
  IconFileText,
  IconGift,
  IconGlobe,
  IconHeart,
  IconPackage,
  IconRefresh,
  IconSearch,
  IconSend,
  IconShield,
  IconSliders,
  IconSpark,
  IconStar,
  IconStore,
  IconTag,
  IconTruck,
  IconWallet,
  type IconProps,
} from "@/components/Icons";
import { formatVnd } from "@/lib/format";
import type { DirectoryResult } from "./directory.server";
import { PlatformGlobe } from "./PlatformGlobe";
import { SHOWCASE_TEMPLATES, TEMPLATE_WALL_SWATCHES } from "./showcaseTemplates";
import { SiteDirectorySection } from "./SiteDirectorySection";
import { StorefrontStageMini } from "./StorefrontStageMini";
import { SubdomainPreview } from "./SubdomainPreview";
import { VietQrSvg } from "./VietQrSvg";
import styles from "./PlatformHome.module.css";

export interface PlatformHomeProps {
  directory: DirectoryResult;
  rootDomain: string;
}

const HERO_PHRASES = ["mang tên bạn", "trên tên miền riêng", "nhận COD và VietQR"] as const;

const SHIPPING_PROVINCES = [
  { name: "Hà Nội", active: false },
  { name: "Hải Phòng", active: false },
  { name: "Đà Nẵng", active: true },
  { name: "TP.HCM", active: false },
  { name: "Cần Thơ", active: false },
] as const;

const SHIPPING_FEE_ROWS = [
  { place: "Hà Nội · P. Hoàn Kiếm", feeVnd: 22000, active: false },
  { place: "Đà Nẵng · P. Hải Châu", feeVnd: 30000, active: true },
  { place: "TP.HCM · P. Bến Nghé", feeVnd: 22000, active: false },
  { place: "Cần Thơ · P. Ninh Kiều", feeVnd: 35000, active: false },
] as const;

const SCALE_CARDS = [
  {
    id: "starter-shop",
    title: "Shop mới bắt đầu",
    description: "Bắt đầu với vài sản phẩm, mở rộng khi đơn tăng.",
    image: "/banners/campaign-green-fiber.jpg",
    alt: "Cuộn vải tự nhiên và mẫu sợi chuẩn bị cho cửa hàng mới bắt đầu",
  },
  {
    id: "fashion-brand",
    title: "Thương hiệu thời trang",
    description: "Giữ nhận diện thương hiệu trên từng trang.",
    image: "/banners/campaign-everyday.jpg",
    alt: "Trang phục thường ngày của thương hiệu thời trang Việt",
  },
  {
    id: "workshop",
    title: "Xưởng may",
    description: "Bán thẳng sản phẩm của xưởng tới người mua.",
    image: "/banners/vietnam-workshop.jpg",
    alt: "Xưởng may Việt Nam kiểm tra chất liệu vải trên bàn cắt",
  },
] as const;

const STUDIO_SECTIONS = [
  { label: "Đầu trang", active: false },
  { label: "Banner", active: true },
  { label: "Bộ sưu tập", active: false },
  { label: "Sản phẩm nổi bật", active: false },
  { label: "Chân trang", active: false },
] as const;

const WALL_ICONS: ReadonlyArray<React.ComponentType<IconProps>> = [
  IconPackage,
  IconTag,
  IconTruck,
  IconWallet,
  IconChat,
  IconGift,
  IconFileText,
  IconRefresh,
  IconDashboard,
  IconStore,
  IconShield,
  IconClock,
  IconSpark,
  IconSearch,
  IconStar,
  IconHeart,
];

const WALL_TOP_ROWS = 4;
const WALL_TOP_COLS = 12;
const WALL_BOTTOM_ROWS = 3;
const WALL_BOTTOM_COLS = 24;

function buildWallTiles(rows: number, cols: number, rowOffset: number) {
  return Array.from({ length: rows * cols }, (_, idx) => {
    const r = Math.floor(idx / cols);
    const c = idx % cols;
    const gr = r + rowOffset;
    const iconIdx = (gr * 5 + c * 3) % WALL_ICONS.length;
    const colorIdx = (gr * 7 + c * 2 + 1) % TEMPLATE_WALL_SWATCHES.length;
    return {
      key: `${gr}-${c}`,
      iconIdx,
      bg: TEMPLATE_WALL_SWATCHES[colorIdx],
    };
  });
}

const WALL_TOP_TILES = buildWallTiles(WALL_TOP_ROWS, WALL_TOP_COLS, 0);
const WALL_BOTTOM_TILES = buildWallTiles(WALL_BOTTOM_ROWS, WALL_BOTTOM_COLS, WALL_TOP_ROWS);

/** Full-width cosmic star accents for the globe band (positioned in % across the section). */
const GLOBE_SECTION_STARS: ReadonlyArray<{
  left: string;
  top: string;
  size: "sm" | "md" | "halo";
}> = [
  { left: "6%", top: "14%", size: "sm" },
  { left: "14%", top: "24%", size: "halo" },
  { left: "22%", top: "10%", size: "sm" },
  { left: "58%", top: "16%", size: "sm" },
  { left: "66%", top: "28%", size: "md" },
  { left: "73%", top: "12%", size: "sm" },
  { left: "82%", top: "20%", size: "halo" },
  { left: "90%", top: "14%", size: "sm" },
  { left: "94%", top: "30%", size: "md" },
  { left: "60%", top: "46%", size: "sm" },
  { left: "78%", top: "40%", size: "halo" },
  { left: "92%", top: "52%", size: "sm" },
  { left: "64%", top: "68%", size: "md" },
  { left: "76%", top: "74%", size: "halo" },
  { left: "86%", top: "66%", size: "sm" },
  { left: "95%", top: "78%", size: "sm" },
  { left: "54%", top: "82%", size: "halo" },
  { left: "68%", top: "88%", size: "sm" },
  { left: "83%", top: "86%", size: "md" },
  { left: "10%", top: "84%", size: "halo" },
  { left: "34%", top: "90%", size: "sm" },
  { left: "48%", top: "12%", size: "md" },
];

const FLOATING_PRODUCTS = [
  { src: "/products/ao-thun-cotton-trang.png", slot: "float1" },
  { src: "/products/ao-polo-pique-kem.png", slot: "float2" },
  { src: "/products/quan-short-daily-be.png", slot: "float3" },
  { src: "/products/ao-khoac-gio-xam-tro.png", slot: "float4" },
  { src: "/products/ao-thun-dryfit-co-vit.png", slot: "float5" },
  { src: "/products/quan-short-linen-xam-da.png", slot: "float6" },
  { src: "/products/ao-polo-det-kim-den.png", slot: "float7" },
  { src: "/products/ao-thun-oversize-than-chi.png", slot: "float8" },
] as const;

const PLATFORM_FAQS = [
  {
    id: "diff",
    question: "NexusMart khác gì sàn thương mại điện tử?",
    answer:
      "Mỗi thương hiệu có địa chỉ riêng, giao diện riêng và tệp khách riêng, không chung kệ hay bị gợi ý sang đối thủ.",
  },
  {
    id: "buyer",
    question: "Khách mua hàng và xem đơn ở đâu?",
    answer:
      "Khách vào thẳng địa chỉ từng cửa hàng từ danh bạ. Giỏ hàng, thanh toán và tra cứu đơn tách riêng theo từng shop.",
  },
  {
    id: "theme",
    question: "Có đổi giao diện sau khi xuất bản không?",
    answer:
      "Có. Bạn chỉnh bản nháp, xem trước rồi xuất bản mới, và khôi phục bản cũ bất cứ lúc nào.",
  },
  {
    id: "payment",
    question: "Cửa hàng hỗ trợ thanh toán nào?",
    answer:
      "Mỗi cửa hàng tích hợp sẵn thanh toán khi nhận hàng (COD) và chuyển khoản VietQR xác nhận trạng thái tự động.",
  },
  {
    id: "directory",
    question: "Khi nào cửa hàng hiện trong danh bạ?",
    answer:
      "Ngay khi hồ sơ được duyệt và bạn bấm Xuất bản trong Kênh người bán. Bản nháp hoặc shop tạm khóa sẽ tự ẩn.",
  },
] as const;

const LAUNCH_STEPS = [
  { num: "01", title: "Chọn giao diện riêng" },
  { num: "02", title: "Đăng sản phẩm lên kệ" },
  { num: "03", title: "Nhận đơn, quét VietQR" },
] as const;

export function PlatformHome({ directory, rootDomain }: PlatformHomeProps) {
  return (
    <div className={styles.page} data-platform-home="true">
      {/* S1. Hero */}
      <section className={styles.hero} aria-labelledby="platform-hero-heading">
        <div className={styles.heroMedia}>
          <Image
            src="/banners/campaign-office.jpg"
            alt="Chủ thương hiệu Việt trao đổi kế hoạch vận hành cửa hàng trực tuyến"
            fill
            priority
            sizes="100vw"
            className={styles.heroBgImg}
          />
          <div className={styles.heroScrim} aria-hidden="true" />
        </div>

        <div className={styles.heroInner}>
          <div className={styles.heroContent}>
            <h1 id="platform-hero-heading" className={styles.heroTitle}>
              <span className="sr-only">Cửa hàng mang tên bạn</span>
              <span className={styles.heroLineFixed} aria-hidden="true">
                Cửa hàng
              </span>
              <span className={styles.heroRotateGrid} aria-hidden="true">
                {HERO_PHRASES.map((phrase, idx) => (
                  <span
                    key={phrase}
                    className={`${styles.heroPhrase} ${
                      idx === 0
                        ? styles.heroPhrase1
                        : idx === 1
                          ? styles.heroPhrase2
                          : styles.heroPhrase3
                    }`}
                  >
                    {phrase}
                  </span>
                ))}
              </span>
            </h1>

            <p className={styles.heroSubtitle}>
              Gõ tên thương hiệu, có ngay cửa hàng riêng. Giao diện riêng, khách riêng, thanh toán
              COD và VietQR.
            </p>

            <div className={styles.heroActions}>
              <Link href="/dang-ky?role=seller" className={styles.btnHeroPrimary}>
                Mở gian hàng
              </Link>
              <a href="#danh-ba-gian-hang" className={styles.btnHeroOutline}>
                Xem các cửa hàng
              </a>
            </div>
          </div>
        </div>
      </section>

      {/* S2. Danh bạ gian hàng (overlapping 48px rounded band on --home-band) */}
      <SiteDirectorySection directory={directory} />

      {/* S3–S6 share the same --home-band canvas */}
      <div className={styles.darkBandGroup}>
        {/* S3. Thẻ Trợ lý AI */}
        <section className={styles.sectionBlock} aria-labelledby="platform-ai-heading">
          <div className={styles.container}>
            <div className={styles.aiCard} data-reveal="">
              <div className={styles.aiCopyCol}>
                <span className={styles.aiBadge}>
                  <IconSpark size={15} className={styles.aiBadgeIcon} />
                  <span>Trợ lý cửa hàng</span>
                </span>

                <h2 id="platform-ai-heading" className={styles.sectionHeading}>
                  Trợ lý AI trả lời khách thay bạn
                </h2>

                <p className={styles.sectionLead}>
                  Khách hỏi size, chất liệu, tình trạng đơn ngay trên cửa hàng. Trợ lý chỉ xem được
                  những gì khách đó được phép xem.
                </p>
              </div>

              <div
                className={styles.aiStage}
                role="img"
                aria-label="Minh họa trợ lý AI trả lời khách hỏi size và chất liệu áo polo ngay trên cửa hàng"
              >
                <div className={styles.aiCustomerBubble} aria-hidden="true">
                  Áo polo này có size L không?
                </div>

                <div className={styles.aiProductWrap} aria-hidden="true">
                  <div className={styles.aiProductPhoto}>
                    <Image
                      src="/models/model-polo-xanh-da.jpg"
                      alt=""
                      fill
                      sizes="204px"
                      className={styles.coverTop}
                    />
                  </div>
                  <p className={styles.aiProductCaption}>Áo Polo Pique Xanh Đá</p>
                </div>

                <div className={styles.aiReplyBubble} aria-hidden="true">
                  Còn size L màu xanh đá. Vải cotton pique, hợp người 68–74 kg.
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* S4. Một nơi cho cả cửa hàng (3 cards: 1 : 2 : 1) */}
        <section className={styles.sectionBlock} aria-labelledby="platform-all-in-one-heading">
          <div className={styles.container}>
            <h2 id="platform-all-in-one-heading" className={styles.sectionHeading} data-reveal="">
              Một nơi cho cả cửa hàng.
              <br />
              Không cần ghép nhiều công cụ.
            </h2>

            <div className={styles.bentoGrid} data-reveal="rows">
              {/* Card 1 (narrow): Storefront bleeding past right edge with Banner outline */}
              <article className={styles.bentoItem}>
                <div
                  className={`${styles.bentoFrame} ${styles.bentoFrameStudio}`}
                  role="img"
                  aria-label="Minh họa trình chỉnh giao diện cửa hàng với khối Banner đang được chọn"
                >
                  <div className={styles.orbitalRings} aria-hidden="true" />
                  <div className={styles.studioBleed} aria-hidden="true">
                    <StorefrontStageMini
                      template={SHOWCASE_TEMPLATES[0]}
                      variant="studio"
                      brandName="Phố Thời Trang"
                      headline="Áo khoác nhẹ giữa phố"
                      imageSrc="/models/model-ao-khoac-gio.jpg"
                      selectedLabel="Banner"
                      decorative
                    />
                  </div>
                </div>
                <div className={styles.cardCopy}>
                  <h3 className={styles.cardTitle}>Giao diện mang dấu ấn riêng</h3>
                  <p className={styles.cardDesc}>
                    8 mẫu giao diện trong Site Studio, xem trước rồi mới xuất bản.
                  </p>
                </div>
              </article>

              {/* Card 2 (wide): Order confirmation mint card + 6 orbital pills */}
              <article className={styles.bentoItem}>
                <div
                  className={`${styles.bentoFrame} ${styles.bentoFrameHub}`}
                  role="img"
                  aria-label="Minh họa thẻ xác nhận đơn hàng hoàn tất ở trung tâm cùng sáu phân hệ vận hành của Kênh người bán"
                >
                  <div className={styles.orbitalRingsWide} aria-hidden="true" />

                  <span
                    className={`${styles.orbitPill} ${styles.orbitTop} ${styles.orbitActive}`}
                    aria-hidden="true"
                  >
                    Thanh toán
                  </span>
                  <span className={`${styles.orbitPill} ${styles.orbitLeftTop}`} aria-hidden="true">
                    Vận chuyển
                  </span>
                  <span
                    className={`${styles.orbitPill} ${styles.orbitRightTop}`}
                    aria-hidden="true"
                  >
                    Sản phẩm
                  </span>
                  <span className={`${styles.orbitPill} ${styles.orbitLeftBot}`} aria-hidden="true">
                    Khách hàng
                  </span>
                  <span
                    className={`${styles.orbitPill} ${styles.orbitRightBot}`}
                    aria-hidden="true"
                  >
                    Tồn kho
                  </span>
                  <span className={`${styles.orbitPill} ${styles.orbitBottom}`} aria-hidden="true">
                    Khuyến mãi
                  </span>

                  <div className={styles.mintOrderCard} aria-hidden="true">
                    <div className={styles.mintOrderHead}>
                      <span className={styles.mintCheckIcon}>
                        <IconCheck size={14} />
                      </span>
                      <div>
                        <p className={styles.mintThanks}>Cảm ơn bạn, Minh!</p>
                        <p className={styles.mintSub}>Chi tiết đơn hàng</p>
                      </div>
                    </div>

                    <div className={styles.mintItemRow}>
                      <div className={styles.mintThumb}>
                        <Image
                          src="/products/ao-polo-pique-xanh-da.png"
                          alt=""
                          fill
                          sizes="52px"
                          className={styles.containImg}
                        />
                      </div>
                      <div className={styles.mintItemInfo}>
                        <p className={styles.mintItemName}>Áo Polo Pique Xanh Đá · Size L</p>
                        <p className={`${styles.mintItemPrice} num`}>{formatVnd(299000)}</p>
                      </div>
                    </div>

                    <div className={styles.mintStatusBtn}>Đơn hàng đã hoàn tất</div>
                  </div>
                </div>
                <div className={styles.cardCopy}>
                  <h3 className={styles.cardTitle}>Điều hành mọi thứ ở một nơi</h3>
                  <p className={styles.cardDesc}>
                    Đơn hàng, tồn kho, thanh toán, khuyến mãi. Tất cả trong Kênh người bán.
                  </p>
                </div>
              </article>

              {/* Card 3 (narrow): Floating dark glass domain tiles */}
              <article className={styles.bentoItem}>
                <div
                  className={`${styles.bentoFrame} ${styles.bentoFrameDomain}`}
                  role="img"
                  aria-label="Minh họa tên miền phụ và tên miền riêng của cửa hàng độc lập"
                >
                  <div className={styles.orbitalRings} aria-hidden="true" />
                  <div className={styles.domainTiles} aria-hidden="true">
                    <div className={`${styles.glassTile} ${styles.glassTile1}`}>
                      <IconStore size={22} />
                      <span>Cửa hàng</span>
                    </div>
                    <div
                      className={`${styles.glassTile} ${styles.glassTile2} ${styles.glassTileHighlight}`}
                    >
                      <IconGlobe size={22} />
                      <span>Tên miền riêng</span>
                    </div>
                    <div className={`${styles.glassTile} ${styles.glassTile3}`}>
                      <span className={styles.domainCode}>.{rootDomain}</span>
                    </div>
                    <div className={`${styles.glassTile} ${styles.glassTile4}`}>
                      <IconShield size={20} />
                      <span>HTTPS</span>
                    </div>
                  </div>
                </div>
                <div className={styles.cardCopy}>
                  <h3 className={styles.cardTitle}>Địa chỉ của riêng bạn</h3>
                  <p className={styles.cardDesc}>
                    Có sẵn địa chỉ ten-shop.{rootDomain}, hoặc gắn tên miền riêng.
                  </p>
                </div>
              </article>
            </div>
          </div>
        </section>

        {/* S5. Giao khắp 34 tỉnh thành */}
        <section className={styles.sectionBlock} aria-labelledby="platform-shipping-heading">
          <div className={styles.container}>
            <h2 id="platform-shipping-heading" className={styles.sectionHeading} data-reveal="">
              Giao khắp 34 tỉnh thành
            </h2>

            <div className={styles.shippingCard} data-reveal="">
              <div className={styles.shippingDots} aria-hidden="true" />

              <div
                className={styles.shippingStage}
                role="img"
                aria-label="Minh họa đơn hàng giao tới Đà Nẵng, danh sách tỉnh thành, bảng phí vận chuyển theo phường và trạng thái đang giao"
              >
                {/* Left: 5 province pills */}
                <div className={styles.provinceCol} aria-hidden="true">
                  {SHIPPING_PROVINCES.map((prov) => (
                    <span
                      key={prov.name}
                      className={`${styles.provincePill} ${prov.active ? styles.provincePillActive : ""}`}
                    >
                      {prov.active ? <span className={styles.provinceDot} /> : null}
                      <span>{prov.name}</span>
                    </span>
                  ))}
                </div>

                {/* Center: product card + floating order pill */}
                <div className={styles.shipCenterWrap} aria-hidden="true">
                  <div className={styles.shipGhostLeft} />
                  <div className={styles.shipGhostRight} />

                  <div className={styles.shipProductCard}>
                    <div className={styles.shipProductPhoto}>
                      <Image
                        src="/models/model-short-linen-xam.jpg"
                        alt=""
                        fill
                        sizes="240px"
                        className={styles.coverTop}
                      />
                    </div>
                    <span className={styles.shipBuyBtn}>Mua ngay</span>
                  </div>

                  <div className={styles.shipOrderPill}>
                    <span className={styles.shipOrderDot} />
                    <span className="num">Đơn hàng {formatVnd(349000)} · Đà Nẵng</span>
                  </div>
                </div>

                {/* Right: white shipping fees sheet + 3 delivery statuses */}
                <div className={styles.shipFeeSheet} aria-hidden="true">
                  <div className={styles.shipFeeHead}>
                    <IconTruck size={15} />
                    <span>Phí giao hàng</span>
                  </div>

                  <ul className={styles.shipFeeList}>
                    {SHIPPING_FEE_ROWS.map((row) => (
                      <li
                        key={row.place}
                        className={`${styles.shipFeeRow} ${row.active ? styles.shipFeeRowActive : ""}`}
                      >
                        <span className={styles.shipFeePlace}>{row.place}</span>
                        <span className={`${styles.shipFeeAmt} num`}>{formatVnd(row.feeVnd)}</span>
                      </li>
                    ))}
                  </ul>

                  <div className={styles.shipTrackRow}>
                    <span className={styles.shipTrackStep}>Đã lấy hàng</span>
                    <span className={styles.shipTrackSep}>·</span>
                    <span className={`${styles.shipTrackStep} ${styles.shipTrackActive}`}>
                      <span className={styles.shipTrackDot} />
                      Đang giao
                    </span>
                    <span className={styles.shipTrackSep}>·</span>
                    <span className={styles.shipTrackStep}>Đã giao</span>
                  </div>
                </div>
              </div>

              <div className={styles.shippingFooter}>
                <h3 className={styles.cardTitle}>Phí ship theo tỉnh và phường</h3>
                <p className={styles.shippingDesc}>
                  Khách thấy phí giao hàng ngay khi nhập địa chỉ. Bạn in nhãn và theo dõi đơn đang
                  giao trong Kênh người bán.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* S6. Dành cho mọi quy mô */}
        <section className={styles.sectionBlockLast} aria-labelledby="platform-scale-heading">
          <div className={styles.container}>
            <h2
              id="platform-scale-heading"
              className={`${styles.sectionHeading} ${styles.scaleHeading}`}
              data-reveal=""
            >
              Dành cho mọi quy mô, từ shop mới mở đến xưởng sản xuất
            </h2>

            <div className={styles.scaleGrid} data-reveal="rows">
              {SCALE_CARDS.map((card) => (
                <article key={card.id} className={styles.scaleCard}>
                  <div className={styles.scalePhoto}>
                    <Image
                      src={card.image}
                      alt={card.alt}
                      fill
                      sizes="(max-width: 768px) 84vw, 404px"
                      className={styles.coverCenter}
                    />
                  </div>
                  <div className={styles.cardCopy}>
                    <h3 className={styles.cardTitle}>{card.title}</h3>
                    <p className={styles.cardDesc}>{card.description}</p>
                  </div>
                </article>
              ))}
            </div>

            <div className={styles.scaleCtaWrap}>
              <Link href="/dang-ky?role=seller" className={styles.btnMidOutline}>
                Mở gian hàng
              </Link>
            </div>
          </div>
        </section>
      </div>

      {/* S7–S9 Band: Teal gradient flowing seamlessly into Navy */}
      <div className={styles.tealNavyBand}>
        {/* S7. Kênh người bán, có AI làm cùng */}
        <section className={styles.sidekickSection} aria-labelledby="platform-seller-ai-heading">
          <div className={styles.container}>
            <h2 id="platform-seller-ai-heading" className={styles.sectionHeading} data-reveal="">
              Kênh người bán, có AI làm cùng
            </h2>

            <div className={styles.sidekickGrid} data-reveal="rows">
              {/* Wide card (2fr): Báo cáo AI */}
              <article className={styles.sidekickCard}>
                <div
                  className={styles.reportStage}
                  role="img"
                  aria-label="Minh họa ô hỏi Báo cáo AI bằng tiếng Việt về tỷ lệ hoàn hủy trong 30 ngày qua"
                >
                  <div className={styles.reportPhotoLeft} aria-hidden="true">
                    <Image
                      src="/models/model-ao-thun-oversize.jpg"
                      alt=""
                      fill
                      sizes="180px"
                      className={styles.coverTop}
                    />
                  </div>

                  <div className={styles.reportPhotoMid} aria-hidden="true">
                    <Image
                      src="/models/model-polo-kem.jpg"
                      alt=""
                      fill
                      sizes="120px"
                      className={styles.coverTop}
                    />
                  </div>

                  <div className={styles.reportPhotoProd} aria-hidden="true">
                    <Image
                      src="/products/quan-jogger-tech-den.png"
                      alt=""
                      fill
                      sizes="96px"
                      className={styles.containImg}
                    />
                  </div>

                  <div className={styles.reportMetricGhost} aria-hidden="true">
                    <span className={styles.reportMetricLabel}>Doanh thu 30 ngày</span>
                    <div className={styles.reportMetricBars}>
                      <span className={styles.bar1} />
                      <span className={styles.bar2} />
                      <span className={styles.bar3} />
                      <span className={styles.bar4} />
                      <span className={styles.bar5} />
                    </div>
                  </div>

                  <div className={styles.reportPromptBox} aria-hidden="true">
                    <p className={styles.reportPromptText}>
                      Sản phẩm nào có tỷ lệ hoàn hủy cao nhất trong 30 ngày qua?
                    </p>
                    <div className={styles.reportPromptFoot}>
                      <span className={styles.reportSparkChip}>
                        <IconSpark size={14} />
                      </span>
                      <span className={styles.reportSendBtn}>
                        <IconSend size={14} />
                      </span>
                    </div>
                  </div>
                </div>

                <div className={styles.sidekickCopy}>
                  <h3 className={styles.cardTitleIcon}>
                    <span>Báo cáo AI cho người bán</span>
                    <IconSpark size={17} className={styles.titleSpark} />
                  </h3>
                  <p className={styles.cardDesc}>
                    Hỏi bằng tiếng Việt về doanh thu, đơn hàng, hoàn hủy. AI trả lời từ số liệu cửa
                    hàng của bạn.
                  </p>
                </div>
              </article>

              {/* Narrow card (1fr): Chat với khách */}
              <article className={styles.sidekickCard}>
                <div
                  className={styles.chatStage}
                  role="img"
                  aria-label="Minh họa khung chat hai chiều giữa khách hàng và người bán trong Kênh người bán"
                >
                  <div className={styles.chatThread} aria-hidden="true">
                    <div className={styles.chatMsgCustomer}>
                      Shop ơi, đơn áo polo của mình đã gửi đi chưa ạ?
                    </div>
                    <div className={styles.chatMsgShop}>
                      Đã bàn giao vận chuyển lúc 14h, dự kiến mai bạn nhận được nhé.
                    </div>
                    <div className={styles.chatMsgCustomer}>Cảm ơn shop nhiều!</div>
                  </div>
                </div>

                <div className={styles.sidekickCopy}>
                  <h3 className={styles.cardTitle}>Chat với khách</h3>
                  <p className={styles.cardDesc}>
                    Trả lời tin nhắn của khách ngay trong Kênh người bán.
                  </p>
                </div>
              </article>
            </div>
          </div>
        </section>

        {/* S8. Tường công cụ (Navy, seamless from S7; top rows start right of copy, bottom rows span full width) */}
        <section className={styles.toolsSection} aria-labelledby="platform-tools-heading">
          <svg
            className={styles.toolsSprite}
            width="0"
            height="0"
            aria-hidden="true"
            focusable="false"
          >
            {WALL_ICONS.map((Icon, idx) => (
              <symbol key={idx} id={`home-wall-icon-${idx}`} viewBox="0 0 22 22">
                <Icon size={22} />
              </symbol>
            ))}
          </svg>

          <div className={styles.toolsTopGrid}>
            <div className={styles.toolsCopyCol} data-reveal="">
              <h2 id="platform-tools-heading" className={styles.sectionHeading}>
                Đủ công cụ để vận hành cửa hàng
              </h2>
              <p className={styles.sectionLead}>
                Sản phẩm, nhập CSV, tồn kho, đơn hàng, in nhãn, đổi trả, voucher, flash sale, báo
                cáo. Có sẵn trong Kênh người bán, không phải cài thêm.
              </p>
            </div>

            <div className={styles.toolsTopWall} aria-hidden="true">
              <div className={styles.toolsTopWallGrid}>
                {WALL_TOP_TILES.map(({ key, iconIdx, bg }) => (
                  <span key={key} className={styles.toolTile} style={{ background: bg }}>
                    <svg width={22} height={22}>
                      <use href={`#home-wall-icon-${iconIdx}`} />
                    </svg>
                  </span>
                ))}
              </div>
            </div>
          </div>

          <div className={styles.toolsBottomWall} aria-hidden="true">
            <div className={styles.toolsBottomWallGrid}>
              {WALL_BOTTOM_TILES.map(({ key, iconIdx, bg }) => (
                <span key={key} className={styles.toolTile} style={{ background: bg }}>
                  <svg width={22} height={22}>
                    <use href={`#home-wall-icon-${iconIdx}`} />
                  </svg>
                </span>
              ))}
            </div>
          </div>
        </section>

        {/* S9. Site Studio (Navy) */}
        <section className={styles.studioSection} aria-labelledby="platform-studio-heading">
          <div className={styles.container}>
            <div className={styles.studioHead} data-reveal="">
              <h2 id="platform-studio-heading" className={styles.sectionHeading}>
                Tùy chỉnh giao diện.
                <br />
                Không cần viết code.
              </h2>
              <p className={styles.sectionLeadWide}>
                Site Studio có 8 mẫu giao diện. Chỉnh bản nháp, xem trước, xuất bản khi ưng và khôi
                phục phiên bản cũ khi cần. Gắn tên miền riêng ngay trong đó.
              </p>
            </div>

            <div
              className={styles.studioShowcase}
              role="img"
              aria-label="Minh họa cửa sổ Site Studio bản nháp gồm danh sách khối bên trái và khung xem trước cửa hàng bên phải"
              data-reveal="scale"
            >
              <div className={styles.studioBlueprint} aria-hidden="true" />

              {/* 3 floating feature pills */}
              <span
                className={`${styles.studioFloatPill} ${styles.studioPillTop}`}
                aria-hidden="true"
              >
                <span className={styles.studioPillDot} />
                Chọn mẫu giao diện
              </span>
              <span
                className={`${styles.studioFloatPill} ${styles.studioPillLeft}`}
                aria-hidden="true"
              >
                <span className={styles.studioPillDot} />
                Xem trước rồi xuất bản
              </span>
              <span
                className={`${styles.studioFloatPill} ${styles.studioPillRight}`}
                aria-hidden="true"
              >
                <span className={styles.studioPillDot} />
                Khôi phục phiên bản cũ
              </span>

              {/* Site Studio window */}
              <div className={styles.studioWindowWrap} aria-hidden="true">
                <span className={styles.studioTealBadge}>
                  <IconSliders size={24} />
                </span>

                <div className={styles.studioWindow}>
                  <div className={styles.studioTitleBar}>
                    <div className={styles.windowDots}>
                      <span className={styles.winDot1} />
                      <span className={styles.winDot2} />
                      <span className={styles.winDot3} />
                    </div>
                    <span className={styles.studioWinTitle}>Site Studio · Bản nháp</span>
                    <span className={styles.studioWinSpacer} />
                  </div>

                  <div className={styles.studioBody}>
                    <div className={styles.studioSidebar}>
                      <p className={styles.studioSideLabel}>Bố cục trang chủ</p>
                      <ul className={styles.studioSectionList}>
                        {STUDIO_SECTIONS.map((item) => (
                          <li
                            key={item.label}
                            className={`${styles.studioSectionItem} ${
                              item.active ? styles.studioSectionActive : ""
                            }`}
                          >
                            <span className={styles.studioItemDot} />
                            <span>{item.label}</span>
                          </li>
                        ))}
                      </ul>
                    </div>

                    <div className={styles.studioPreviewPane}>
                      <StorefrontStageMini
                        template={SHOWCASE_TEMPLATES[3]}
                        variant="studio"
                        selectedLabel="Banner"
                        decorative
                      />
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>

      {/* S10–S11 Band: Pine (48px rounded top overlapping S9) */}
      <div className={styles.pineBand}>
        {/* S10. Thanh toán */}
        <section className={styles.paymentSection} aria-labelledby="platform-payment-heading">
          <div className={styles.container}>
            <h2 id="platform-payment-heading" className={styles.centerHeading} data-reveal="">
              Bán cho khách Việt, theo cách khách Việt mua
            </h2>

            <div className={styles.paymentGrid}>
              <div className={styles.paymentCopyCol} data-reveal="">
                <h3 className={styles.subHeading}>Thanh toán quen thuộc với khách Việt</h3>

                <div className={styles.paymentMetrics}>
                  <div className={styles.paymentMetric}>
                    <span className={styles.paymentMetricLabel}>
                      <IconTruck size={14} />
                      <span>THU TIỀN KHI GIAO</span>
                    </span>
                    <span className={styles.paymentMetricValue}>COD</span>
                  </div>

                  <div className={styles.paymentMetric}>
                    <span className={styles.paymentMetricLabel}>
                      <IconCheck size={14} />
                      <span>TỰ XÁC NHẬN KHI TIỀN VỀ</span>
                    </span>
                    <span className={styles.paymentMetricValue}>VietQR</span>
                  </div>
                </div>

                <p className={styles.paymentCallout}>
                  Khách chọn thanh toán khi nhận hàng hoặc quét mã VietQR. Đơn chuyển khoản tự xác
                  nhận khi tiền về, bạn không phải đối soát tay.
                </p>
              </div>

              <div
                className={styles.checkoutOrbit}
                role="img"
                aria-label="Minh họa trang thanh toán tóm tắt đơn hàng với lựa chọn COD và mã VietQR"
                data-reveal="scale"
              >
                {FLOATING_PRODUCTS.map((item) => (
                  <div
                    key={item.slot}
                    className={`${styles.floatProd} ${styles[item.slot]}`}
                    aria-hidden="true"
                  >
                    <Image src={item.src} alt="" fill sizes="84px" className={styles.containImg} />
                  </div>
                ))}

                <div className={styles.checkoutSheet} aria-hidden="true">
                  <div className={styles.checkoutTop}>
                    <span className={styles.checkoutTitle}>Tóm tắt đơn hàng</span>
                    <span className={`${styles.checkoutCount} num`}>2 sản phẩm</span>
                  </div>

                  <ul className={styles.checkoutItems}>
                    <li className={styles.checkoutItem}>
                      <span>Áo Polo Pique Xanh Đá · Size L</span>
                      <span className="num">{formatVnd(299000)}</span>
                    </li>
                    <li className={styles.checkoutItem}>
                      <span>Quần Short Daily Be · Size 31</span>
                      <span className="num">{formatVnd(249000)}</span>
                    </li>
                    <li className={styles.checkoutItemMuted}>
                      <span>Phí vận chuyển (Hà Nội)</span>
                      <span className="num">{formatVnd(22000)}</span>
                    </li>
                  </ul>

                  <div className={styles.checkoutTotalRow}>
                    <span>Tổng thanh toán</span>
                    <span className={`${styles.checkoutTotalVal} num`}>{formatVnd(570000)}</span>
                  </div>

                  <div className={styles.checkoutOptions}>
                    <div className={styles.checkoutOption}>
                      <span className={styles.radioCircle} />
                      <span>COD · Thu tiền khi giao hàng</span>
                    </div>
                    <div className={`${styles.checkoutOption} ${styles.checkoutOptionSelected}`}>
                      <span className={styles.radioCircleSelected} />
                      <span>VietQR · Chuyển khoản quét mã</span>
                    </div>
                  </div>

                  <div className={styles.checkoutQrRow}>
                    <div className={styles.checkoutQrBox}>
                      <VietQrSvg size={96} className={styles.checkoutQrSvg} decorative />
                    </div>
                    <div className={styles.checkoutQrMeta}>
                      <span className={styles.checkoutConfirmBadge}>
                        <IconCheck size={14} />
                        <span>Tự động xác nhận</span>
                      </span>
                      <p className={styles.checkoutQrHint}>
                        Quét mã bằng ứng dụng ngân hàng để hoàn tất đơn{" "}
                        <span className="num">{formatVnd(570000)}</span>.
                      </p>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* S10b. Quả địa cầu (same Pine band, between S10 Thanh toán and S11 FAQ) */}
        <section className={styles.globeSection} aria-labelledby="platform-globe-heading">
          <div className={styles.globeStars} aria-hidden="true">
            {GLOBE_SECTION_STARS.map((star, idx) => (
              <span
                key={`star-${idx}`}
                className={`${styles.globeStar} ${
                  star.size === "halo"
                    ? styles.globeStarHalo
                    : star.size === "md"
                      ? styles.globeStarMd
                      : styles.globeStarSm
                }`}
                style={{ left: star.left, top: star.top }}
              />
            ))}
          </div>

          <div className={styles.container}>
            <div className={styles.globeGrid}>
              <div className={styles.globeCol}>
                <PlatformGlobe className={styles.globeVisual} />
              </div>

              <div className={styles.globeCopyCol} data-reveal="">
                <h2 id="platform-globe-heading" className={styles.subHeading}>
                  Vững khi đông khách.
                  <br />
                  Chính xác từng đơn.
                </h2>
                <p className={styles.globeDesc}>
                  Flash sale và voucher trừ suất ngay khi khách đặt, tồn kho giữ theo từng SKU, nên
                  không bán quá số lượng kể cả khi nhiều người mua cùng lúc.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* S11. FAQ (still on Pine band) */}
        <section className={styles.faqSection} aria-labelledby="platform-faq-heading">
          <div className={styles.container}>
            <div className={styles.faqGrid}>
              <div className={styles.faqLeadCol}>
                <h2 id="platform-faq-heading" className={styles.subHeading} data-reveal="">
                  Câu hỏi thường gặp
                </h2>
              </div>

              <div className={styles.faqList} data-reveal="rows">
                {PLATFORM_FAQS.map((item) => (
                  <details key={item.id} name="platform-faq" className={styles.faqItem}>
                    <summary className={styles.faqSummary}>
                      <span className={styles.faqQuestion}>{item.question}</span>
                      <span className={styles.faqPlus} aria-hidden="true" />
                    </summary>
                    <div className={styles.faqAnswerWrap}>
                      <p className={styles.faqAnswer}>{item.answer}</p>
                    </div>
                  </details>
                ))}
              </div>
            </div>
          </div>
        </section>
      </div>

      {/* S12. Lên sóng trong ba bước (48px rounded top overlapping S11) */}
      <section className={styles.stepsBand} aria-labelledby="platform-steps-heading">
        <div className={styles.container}>
          <h2 id="platform-steps-heading" className={styles.centerHeading} data-reveal="">
            Lên sóng trong ba bước
          </h2>

          <div className={styles.stepsGrid}>
            <div className={styles.stepsGallery} data-reveal="rows">
              <div className={styles.stepsPhotoPrimary}>
                <Image
                  src="/models/model-ao-thun-trang.jpg"
                  alt="Sản phẩm áo thun cotton trưng bày trên cửa hàng trực tuyến"
                  fill
                  sizes="(max-width: 900px) 46vw, 280px"
                  className={styles.coverTop}
                />
              </div>
              <div className={styles.stepsPhotoSecondary}>
                <Image
                  src="/banners/fabric-closeup.jpg"
                  alt="Bề mặt vải dệt tinh chọn của sản phẩm Việt"
                  fill
                  sizes="(max-width: 900px) 46vw, 280px"
                  className={styles.coverCenter}
                />
              </div>
            </div>

            <div className={styles.stepsRightCol}>
              <ol className={styles.stepsList} data-reveal="rows">
                {LAUNCH_STEPS.map((step) => (
                  <li key={step.num} className={styles.stepItem}>
                    <span className={`${styles.stepNum} num`}>{step.num}</span>
                    <span className={styles.stepTitle}>{step.title}</span>
                  </li>
                ))}
              </ol>

              <div className={styles.subdomainWrap}>
                <SubdomainPreview rootDomain={rootDomain} />
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
