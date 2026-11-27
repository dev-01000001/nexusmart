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

export interface PlatformHomeProps {
  directory: DirectoryResult;
  rootDomain: string;
}

const CONTAINER_CLS =
  "mx-auto flex w-[min(var(--home-max),100%_-_2_*_var(--home-gutter))] flex-col gap-[var(--home-space-head)]";

const SECTION_HEADING_CLS =
  "font-[family-name:var(--home-font)] text-[length:var(--home-fs-h2)] font-[330] leading-[1.08] tracking-[-0.03em] text-[var(--home-ink)] [text-wrap:balance]";

const CENTER_HEADING_CLS =
  "mx-auto max-w-[820px] text-center font-[family-name:var(--home-font)] text-[length:var(--home-fs-center)] font-[330] leading-[1.08] tracking-[-0.02em] text-[var(--home-ink)] [text-wrap:balance]";

const SUB_HEADING_CLS =
  "font-[family-name:var(--home-font)] text-[length:var(--home-fs-sub)] font-[330] leading-[1.1] tracking-[-0.01em] text-[var(--home-ink)] [text-wrap:balance]";

const SECTION_LEAD_BASE =
  "text-[length:var(--home-fs-lead)] font-normal leading-[1.45] text-[var(--home-ink-2)]";

const CARD_COPY_CLS = "flex flex-col gap-2";

const CARD_TITLE_CLS =
  "text-[length:var(--home-fs-h3)] font-[450] leading-[1.3] tracking-[-0.01em] text-[var(--home-ink)]";

const CARD_DESC_CLS =
  "text-[length:var(--home-fs-body)] font-normal leading-[1.48] text-[var(--home-ink-2)]";

const COVER_TOP_CLS = "object-cover object-[center_16%]";
const COVER_CENTER_CLS = "object-cover object-center";
const CONTAIN_IMG_CLS = "object-contain";

const BTN_HERO_BASE =
  "inline-flex min-h-[var(--home-btn-hero-h)] items-center justify-center whitespace-nowrap rounded-full px-6 py-3 text-[length:var(--home-fs-lead)] font-[550] [transition:background-color_200ms_cubic-bezier(0.22,1,0.36,1),border-color_200ms_cubic-bezier(0.22,1,0.36,1),transform_140ms_cubic-bezier(0.22,1,0.36,1)] active:scale-[0.98] [@media(max-width:640px)]:w-full";

const BENTO_FRAME_BASE =
  "relative h-[clamp(340px,31vw,440px)] w-full overflow-hidden rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card)] [@media(max-width:1080px)]:h-[360px]";

const ORBITAL_RINGS_CLS =
  "pointer-events-none absolute inset-0 [background:radial-gradient(circle_at_50%_50%,var(--home-glow-soft)_0%,transparent_56%),repeating-radial-gradient(circle_at_50%_50%,transparent_0,transparent_66px,rgba(102,204,215,0.13)_66px,rgba(102,204,215,0.13)_67px)]";

const ORBIT_PILL_BASE =
  "absolute z-[2] whitespace-nowrap rounded-full px-4 py-2 text-[length:var(--home-fs-small)] font-[450]";

const ORBIT_PILL_IDLE = `${ORBIT_PILL_BASE} [border:1px_solid_rgba(255,255,255,0.12)] bg-[rgba(6,18,20,0.78)] text-[rgba(255,255,255,0.45)]`;

const GLASS_TILE_BASE =
  "absolute flex flex-col items-center justify-center gap-2 rounded-[var(--home-radius-tile)] text-center text-[length:var(--home-fs-caption)] font-medium text-[var(--home-ink)]";

const STUDIO_FLOAT_PILL_CLS =
  "absolute z-[4] inline-flex items-center gap-2 whitespace-nowrap rounded-[var(--home-radius-card)] [border:1px_solid_rgba(138,176,238,0.28)] bg-[var(--home-pill-bg)] px-4 py-2 text-[length:var(--home-fs-small)] font-[450] text-[var(--home-ink)] [box-shadow:var(--home-shadow-pill)] [@media(max-width:1080px)]:hidden";

const STUDIO_PILL_DOT_CLS =
  "h-[7px] w-[7px] flex-none rounded-full [border:1.5px_solid_var(--home-accent)]";

const TOOL_TILE_CLS =
  "inline-flex h-[54px] w-[54px] items-center justify-center rounded-[var(--home-radius-tile)] [border:1px_solid_rgba(255,255,255,0.14)] text-[var(--home-ink)] [box-shadow:var(--home-shadow-pill)] [@media(max-width:960px)]:h-[44px] [@media(max-width:960px)]:w-[44px]";

const FLOAT_PROD_BASE =
  "absolute z-[1] overflow-hidden rounded-[var(--home-radius-media)] bg-[#eff3f5] p-1 [box-shadow:var(--home-shadow-float)] motion-reduce:animate-none";

const HERO_PHRASES = [
  {
    text: "mang tên bạn",
    cls: "opacity-100 animate-[nm-home-hero-cycle-1_9s_cubic-bezier(0.22,1,0.36,1)_infinite] motion-reduce:animate-none motion-reduce:opacity-100 motion-reduce:[transform:none]",
  },
  {
    text: "trên tên miền riêng",
    cls: "opacity-0 animate-[nm-home-hero-cycle-2_9s_cubic-bezier(0.22,1,0.36,1)_infinite] motion-reduce:hidden motion-reduce:animate-none motion-reduce:opacity-0",
  },
  {
    text: "nhận COD và VietQR",
    cls: "opacity-0 animate-[nm-home-hero-cycle-3_9s_cubic-bezier(0.22,1,0.36,1)_infinite] motion-reduce:hidden motion-reduce:animate-none motion-reduce:opacity-0",
  },
] as const;

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
  {
    src: "/products/ao-thun-cotton-trang.png",
    slot: "float1",
    cls: "left-[6%] top-[4px] h-[72px] w-[72px] [transform:rotate(-6deg)] animate-[nm-home-prod-float-a_6.4s_ease-in-out_infinite]",
  },
  {
    src: "/products/ao-polo-pique-kem.png",
    slot: "float2",
    cls: "left-[44%] -top-[6px] h-[64px] w-[64px] [transform:rotate(4deg)] animate-[nm-home-prod-float-b_7.2s_ease-in-out_infinite]",
  },
  {
    src: "/products/quan-short-daily-be.png",
    slot: "float3",
    cls: "right-[4%] top-[12px] h-[82px] w-[82px] [transform:rotate(7deg)] animate-[nm-home-prod-float-a_7.8s_ease-in-out_infinite]",
  },
  {
    src: "/products/ao-khoac-gio-xam-tro.png",
    slot: "float4",
    cls: "left-0 top-[42%] h-[64px] w-[64px] [transform:rotate(-8deg)] animate-[nm-home-prod-float-b_6.8s_ease-in-out_infinite] [@media(max-width:960px)]:hidden",
  },
  {
    src: "/products/ao-thun-dryfit-co-vit.png",
    slot: "float5",
    cls: "right-0 top-[46%] h-[68px] w-[68px] [transform:rotate(6deg)] animate-[nm-home-prod-float-a_6.2s_ease-in-out_infinite] [@media(max-width:960px)]:hidden",
  },
  {
    src: "/products/quan-short-linen-xam-da.png",
    slot: "float6",
    cls: "bottom-[4px] left-[8%] h-[74px] w-[74px] [transform:rotate(5deg)] animate-[nm-home-prod-float-b_7.4s_ease-in-out_infinite] [@media(max-width:960px)]:hidden",
  },
  {
    src: "/products/ao-polo-det-kim-den.png",
    slot: "float7",
    cls: "-bottom-[4px] left-[46%] h-[62px] w-[62px] [transform:rotate(-4deg)] animate-[nm-home-prod-float-a_6.6s_ease-in-out_infinite] [@media(max-width:960px)]:hidden",
  },
  {
    src: "/products/ao-thun-oversize-than-chi.png",
    slot: "float8",
    cls: "bottom-[6px] right-[6%] h-[76px] w-[76px] [transform:rotate(-5deg)] animate-[nm-home-prod-float-b_7s_ease-in-out_infinite]",
  },
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
    <div
      className="w-full overflow-x-clip bg-[var(--home-bg)] font-[family-name:var(--home-font)] text-[var(--home-ink)] [html:has(&)]:bg-[#000000] [html:has(&)]:text-[#ffffff] [html:has(&)]:[color-scheme:dark] [html:has(&)_body]:bg-[#000000] [html:has(&)_body]:text-[#ffffff] [&_:focus-visible]:[outline-offset:2px]! [&_:focus-visible]:[outline:2px_solid_var(--home-accent)]!"
      data-platform-home="true"
    >
      {/* S1. Hero */}
      <section
        className="relative flex h-[clamp(640px,95svh,860px)] min-h-[640px] w-full items-end overflow-hidden bg-[var(--home-band)] [@media(max-width:640px)]:h-[clamp(620px,88svh,760px)] [@media(max-width:640px)]:min-h-[620px]"
        aria-labelledby="platform-hero-heading"
      >
        <div className="absolute inset-0 overflow-hidden">
          <Image
            src="/banners/campaign-office.jpg"
            alt="Chủ thương hiệu Việt trao đổi kế hoạch vận hành cửa hàng trực tuyến"
            fill
            priority
            sizes="100vw"
            className="object-cover object-[78%_22%] [@media(max-width:640px)]:object-[62%_20%]"
          />
          <div
            className="absolute inset-0 [background:radial-gradient(ellipse_72%_54%_at_26%_62%,rgba(2,9,10,0.62)_0%,rgba(2,9,10,0.46)_66%,transparent_94%),linear-gradient(90deg,rgba(2,9,10,0.8)_0%,rgba(2,9,10,0.56)_36%,rgba(2,9,10,0.28)_60%,transparent_78%),linear-gradient(180deg,rgba(2,9,10,0.62)_0%,rgba(2,9,10,0.22)_16%,transparent_30%),linear-gradient(0deg,rgba(2,9,10,0.72)_0%,rgba(2,9,10,0.26)_24%,transparent_46%)] [@media(max-width:640px)]:[background:linear-gradient(180deg,rgba(2,9,10,0.66)_0%,rgba(2,9,10,0.48)_38%,rgba(2,9,10,0.88)_100%),linear-gradient(90deg,rgba(2,9,10,0.44)_0%,transparent_72%)]"
            aria-hidden="true"
          />
        </div>

        <div className="relative z-[1] mx-auto w-[min(var(--home-max),100%_-_2_*_var(--home-gutter))] pb-[calc(var(--home-radius-band)_+_clamp(48px,6.5vw,84px))] pt-[calc(var(--home-header-h)_+_48px)] [@media(max-width:640px)]:pb-[calc(var(--home-radius-band-sm)_+_48px)]">
          <div className="flex max-w-[1060px] flex-col items-start gap-6">
            <h1
              id="platform-hero-heading"
              className="flex flex-col font-[family-name:var(--home-font)] text-[length:var(--home-fs-display)] font-light leading-[1.08] tracking-[-0.02em] text-[var(--home-ink)] [text-wrap:initial]"
            >
              <span className="sr-only">Cửa hàng mang tên bạn</span>
              <span className="block" aria-hidden="true">
                Cửa hàng
              </span>
              <span className="my-[-0.14em] grid overflow-hidden py-[0.14em]" aria-hidden="true">
                {HERO_PHRASES.map((phrase) => (
                  <span
                    key={phrase.text}
                    className={`block whitespace-nowrap [grid-area:1/1] [@media(max-width:640px)]:whitespace-normal ${phrase.cls}`}
                  >
                    {phrase.text}
                  </span>
                ))}
              </span>
            </h1>

            <p className="max-w-[440px] text-[length:var(--home-fs-hero-lead)] font-normal leading-[1.3] tracking-[-0.02em] text-[var(--home-ink)]">
              Gõ tên thương hiệu, có ngay cửa hàng riêng. Giao diện riêng, khách riêng, thanh toán
              COD và VietQR.
            </p>

            <div className="flex flex-wrap items-center gap-4 pt-1 [@media(max-width:640px)]:w-full [@media(max-width:640px)]:flex-col [@media(max-width:640px)]:items-stretch">
              <Link
                href="/dang-ky?role=seller"
                className={`${BTN_HERO_BASE} bg-[var(--home-ink)] text-[var(--home-bg)] hover:bg-[#eff3f5]`}
              >
                Mở gian hàng
              </Link>
              <a
                href="#danh-ba-gian-hang"
                className={`${BTN_HERO_BASE} [border:1.5px_solid_rgba(255,255,255,0.85)] bg-[rgba(2,9,10,0.24)] text-[var(--home-ink)] hover:border-[color:var(--home-ink)] hover:bg-[var(--home-glass-strong)]`}
              >
                Xem các cửa hàng
              </a>
            </div>
          </div>
        </div>
      </section>

      {/* S2. Danh bạ gian hàng (overlapping 48px rounded band on --home-band) */}
      <SiteDirectorySection directory={directory} />

      {/* S3–S6 share the same --home-band canvas */}
      <div className="relative z-[2] bg-[var(--home-band)]">
        {/* S3. Thẻ Trợ lý AI */}
        <section className="pt-[var(--home-space-section)]" aria-labelledby="platform-ai-heading">
          <div className={CONTAINER_CLS}>
            <div
              className="relative grid min-h-[450px] w-full grid-cols-[minmax(0,1.08fr)_minmax(0,0.92fr)] items-center gap-12 overflow-hidden rounded-[var(--home-radius-card)] [border:1px_solid_rgba(102,204,215,0.16)] p-[clamp(28px,3.8vw,48px)] [background:radial-gradient(ellipse_56%_78%_at_76%_42%,var(--home-glow),transparent_62%),linear-gradient(158deg,var(--home-card-ai-start)_0%,var(--home-card-ai-end)_100%)] [@media(max-width:960px)]:grid-cols-[1fr] [@media(max-width:960px)]:gap-8 [@media(max-width:640px)]:px-5 [@media(max-width:640px)]:py-6"
              data-reveal=""
            >
              <div className="flex min-w-0 flex-col items-start gap-5">
                <span className="inline-flex items-center gap-2 rounded-full bg-[var(--home-ink)] px-4 py-2 text-[length:var(--home-fs-small)] font-semibold leading-[1.2] text-[var(--home-ink-dark)]">
                  <IconSpark size={15} className="flex-none text-[var(--home-accent-solid)]" />
                  <span>Trợ lý cửa hàng</span>
                </span>

                <h2 id="platform-ai-heading" className={SECTION_HEADING_CLS}>
                  Trợ lý AI trả lời khách thay bạn
                </h2>

                <p className={`${SECTION_LEAD_BASE} max-w-[44ch]`}>
                  Khách hỏi size, chất liệu, tình trạng đơn ngay trên cửa hàng. Trợ lý chỉ xem được
                  những gì khách đó được phép xem.
                </p>
              </div>

              <div
                className="relative flex min-h-[356px] flex-col items-center justify-center px-3 py-5 [@media(max-width:640px)]:min-h-[310px] [@media(max-width:640px)]:px-0 [@media(max-width:640px)]:py-3"
                role="img"
                aria-label="Minh họa trợ lý AI trả lời khách hỏi size và chất liệu áo polo ngay trên cửa hàng"
              >
                <div
                  className="relative z-[3] -mb-5 ml-2 max-w-[260px] self-start [border-radius:var(--home-radius-bubble)_var(--home-radius-bubble)_var(--home-radius-bubble)_4px] bg-[var(--home-ink)] px-4 py-3 text-[length:var(--home-fs-small)] font-medium leading-[1.4] text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-pill)] [@media(max-width:640px)]:ml-0"
                  aria-hidden="true"
                >
                  Áo polo này có size L không?
                </div>

                <div className="relative z-[2] flex flex-col items-start gap-2" aria-hidden="true">
                  <div className="relative h-[227px] w-[204px] overflow-hidden rounded-[4px] bg-[var(--home-card)] [box-shadow:var(--home-shadow-card)]">
                    <Image
                      src="/models/model-polo-xanh-da.jpg"
                      alt=""
                      fill
                      sizes="204px"
                      className={COVER_TOP_CLS}
                    />
                  </div>
                  <p className="text-[length:var(--home-fs-caption)] font-[450] text-[var(--home-ink-2)]">
                    Áo Polo Pique Xanh Đá
                  </p>
                </div>

                <div
                  className="relative z-[3] mt-3 max-w-[340px] self-end [border-radius:var(--home-radius-bubble)_var(--home-radius-bubble)_4px_var(--home-radius-bubble)] [border:1px_solid_rgba(102,204,215,0.28)] bg-[rgba(4,16,18,0.88)] px-5 py-3 text-[length:var(--home-fs-small)] leading-[1.48] text-[var(--home-ink)] [box-shadow:var(--home-shadow-float)] [@media(max-width:640px)]:max-w-full"
                  aria-hidden="true"
                >
                  Còn size L màu xanh đá. Vải cotton pique, hợp người 68–74 kg.
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* S4. Một nơi cho cả cửa hàng (3 cards: 1 : 2 : 1) */}
        <section
          className="pt-[var(--home-space-section)]"
          aria-labelledby="platform-all-in-one-heading"
        >
          <div className={CONTAINER_CLS}>
            <h2 id="platform-all-in-one-heading" className={SECTION_HEADING_CLS} data-reveal="">
              Một nơi cho cả cửa hàng.
              <br />
              Không cần ghép nhiều công cụ.
            </h2>

            <div
              className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)_minmax(0,1fr)] gap-[var(--home-gap)] [@media(max-width:1080px)]:grid-cols-[1fr] [@media(max-width:1080px)]:gap-8"
              data-reveal="rows"
            >
              {/* Card 1 (narrow): Storefront bleeding past right edge with Banner outline */}
              <article className="flex min-w-0 flex-col gap-5">
                <div
                  className={`${BENTO_FRAME_BASE} flex items-end`}
                  role="img"
                  aria-label="Minh họa trình chỉnh giao diện cửa hàng với khối Banner đang được chọn"
                >
                  <div className={ORBITAL_RINGS_CLS} aria-hidden="true" />
                  <div
                    className="relative z-[2] ml-5 h-[86%] w-[126%] overflow-hidden rounded-tl-[var(--home-radius-card)] [box-shadow:var(--home-shadow-card)]"
                    aria-hidden="true"
                  >
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
                <div className={CARD_COPY_CLS}>
                  <h3 className={CARD_TITLE_CLS}>Giao diện mang dấu ấn riêng</h3>
                  <p className={CARD_DESC_CLS}>
                    8 mẫu giao diện trong Site Studio, xem trước rồi mới xuất bản.
                  </p>
                </div>
              </article>

              {/* Card 2 (wide): Order confirmation mint card + 6 orbital pills */}
              <article className="flex min-w-0 flex-col gap-5">
                <div
                  className={`${BENTO_FRAME_BASE} flex items-center justify-center p-6`}
                  role="img"
                  aria-label="Minh họa thẻ xác nhận đơn hàng hoàn tất ở trung tâm cùng sáu phân hệ vận hành của Kênh người bán"
                >
                  <div
                    className="pointer-events-none absolute inset-0 [background:radial-gradient(circle_at_50%_52%,var(--home-glow)_0%,transparent_52%),repeating-radial-gradient(circle_at_50%_52%,transparent_0,transparent_82px,rgba(102,204,215,0.16)_82px,rgba(102,204,215,0.16)_83px)]"
                    aria-hidden="true"
                  />

                  <span
                    className={`${ORBIT_PILL_BASE} left-1/2 top-6 [border:1px_solid_rgba(255,255,255,0.72)] bg-[rgba(8,26,28,0.92)] text-[var(--home-ink)] [box-shadow:0_0_24px_var(--home-glow-soft)] [transform:translateX(-50%)]`}
                    aria-hidden="true"
                  >
                    Thanh toán
                  </span>
                  <span
                    className={`${ORBIT_PILL_IDLE} left-6 top-[28%] [@media(max-width:640px)]:hidden`}
                    aria-hidden="true"
                  >
                    Vận chuyển
                  </span>
                  <span
                    className={`${ORBIT_PILL_IDLE} right-6 top-[28%] [@media(max-width:640px)]:hidden`}
                    aria-hidden="true"
                  >
                    Sản phẩm
                  </span>
                  <span
                    className={`${ORBIT_PILL_IDLE} bottom-[24%] left-8 [@media(max-width:640px)]:hidden`}
                    aria-hidden="true"
                  >
                    Khách hàng
                  </span>
                  <span
                    className={`${ORBIT_PILL_IDLE} bottom-[22%] right-8 [@media(max-width:640px)]:hidden`}
                    aria-hidden="true"
                  >
                    Tồn kho
                  </span>
                  <span
                    className={`${ORBIT_PILL_IDLE} bottom-5 left-1/2 [transform:translateX(-50%)]`}
                    aria-hidden="true"
                  >
                    Khuyến mãi
                  </span>

                  <div
                    className="relative z-[3] flex w-[min(100%,272px)] flex-col gap-4 rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-mint-border)] bg-[var(--home-mint)] p-5 text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-card)]"
                    aria-hidden="true"
                  >
                    <div className="flex items-center gap-3">
                      <span className="inline-flex h-8 w-8 flex-none items-center justify-center rounded-full bg-[var(--home-accent-solid)] text-[var(--home-ink)]">
                        <IconCheck size={14} />
                      </span>
                      <div>
                        <p className="text-[length:var(--home-fs-small)] font-semibold leading-[1.25] text-[var(--home-ink-dark)]">
                          Cảm ơn bạn, Minh!
                        </p>
                        <p className="text-[length:var(--home-fs-caption)] text-[var(--home-ink-dark-2)]">
                          Chi tiết đơn hàng
                        </p>
                      </div>
                    </div>

                    <div className="flex items-center gap-3 rounded-[var(--home-radius-media)] bg-[var(--home-ink)] p-2">
                      <div className="relative h-12 w-12 flex-none overflow-hidden rounded-[4px] bg-[#eff3f5]">
                        <Image
                          src="/products/ao-polo-pique-xanh-da.png"
                          alt=""
                          fill
                          sizes="52px"
                          className={CONTAIN_IMG_CLS}
                        />
                      </div>
                      <div className="flex min-w-0 flex-col gap-[2px]">
                        <p className="text-[length:var(--home-fs-caption)] font-medium leading-[1.3] text-[var(--home-ink-dark)]">
                          Áo Polo Pique Xanh Đá · Size L
                        </p>
                        <p
                          className="tabular-nums text-[length:var(--home-fs-caption)] font-semibold text-[var(--home-accent-solid)]"
                          data-demo-price=""
                        >
                          {formatVnd(299000)}
                        </p>
                      </div>
                    </div>

                    <div className="rounded-full [border:1px_solid_var(--home-ink-dark)] px-3 py-2 text-center text-[length:var(--home-fs-caption)] font-semibold text-[var(--home-ink-dark)]">
                      Đơn hàng đã hoàn tất
                    </div>
                  </div>
                </div>
                <div className={CARD_COPY_CLS}>
                  <h3 className={CARD_TITLE_CLS}>Điều hành mọi thứ ở một nơi</h3>
                  <p className={CARD_DESC_CLS}>
                    Đơn hàng, tồn kho, thanh toán, khuyến mãi. Tất cả trong Kênh người bán.
                  </p>
                </div>
              </article>

              {/* Card 3 (narrow): Floating dark glass domain tiles */}
              <article className="flex min-w-0 flex-col gap-5">
                <div
                  className={`${BENTO_FRAME_BASE} flex items-center justify-center`}
                  role="img"
                  aria-label="Minh họa tên miền phụ và tên miền riêng của cửa hàng độc lập"
                >
                  <div className={ORBITAL_RINGS_CLS} aria-hidden="true" />
                  <div className="relative z-[2] h-full w-full" aria-hidden="true">
                    <div
                      className={`${GLASS_TILE_BASE} left-[10%] top-[14%] h-[98px] w-[112px] [border:1px_solid_var(--home-border-strong)] bg-[rgba(14,30,32,0.84)] p-4 [box-shadow:var(--home-shadow-float)]`}
                    >
                      <IconStore size={22} />
                      <span>Cửa hàng</span>
                    </div>
                    <div
                      className={`${GLASS_TILE_BASE} right-[8%] top-[38%] h-[108px] w-[132px] [border:1px_solid_var(--home-accent)] bg-[rgba(12,38,42,0.92)] p-4 [box-shadow:var(--home-shadow-float),0_0_28px_var(--home-glow-soft)]`}
                    >
                      <IconGlobe size={22} />
                      <span>Tên miền riêng</span>
                    </div>
                    <div
                      className={`${GLASS_TILE_BASE} bottom-[14%] left-[10%] max-w-[78%] [border:1px_solid_var(--home-border-strong)] bg-[rgba(14,30,32,0.84)] px-4 py-3 [box-shadow:var(--home-shadow-float)]`}
                    >
                      <span className="max-w-full overflow-hidden text-ellipsis whitespace-nowrap text-[length:var(--home-fs-small)] font-medium text-[var(--home-accent)]">
                        .{rootDomain}
                      </span>
                    </div>
                    <div
                      className={`${GLASS_TILE_BASE} right-[12%] top-[10%] h-[82px] w-[86px] [border:1px_solid_var(--home-border-strong)] bg-[rgba(14,30,32,0.84)] p-4 opacity-[0.72] [box-shadow:var(--home-shadow-float)]`}
                    >
                      <IconShield size={20} />
                      <span>HTTPS</span>
                    </div>
                  </div>
                </div>
                <div className={CARD_COPY_CLS}>
                  <h3 className={CARD_TITLE_CLS}>Địa chỉ của riêng bạn</h3>
                  <p className={CARD_DESC_CLS}>
                    Có sẵn địa chỉ ten-shop.{rootDomain}, hoặc gắn tên miền riêng.
                  </p>
                </div>
              </article>
            </div>
          </div>
        </section>

        {/* S5. Giao khắp 34 tỉnh thành */}
        <section
          className="pt-[var(--home-space-section)]"
          aria-labelledby="platform-shipping-heading"
        >
          <div className={CONTAINER_CLS}>
            <h2 id="platform-shipping-heading" className={SECTION_HEADING_CLS} data-reveal="">
              Giao khắp 34 tỉnh thành
            </h2>

            <div
              className="relative flex min-h-[540px] w-full flex-col justify-between gap-8 overflow-hidden rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] p-[clamp(24px,3.6vw,48px)] [background:radial-gradient(ellipse_65%_65%_at_50%_42%,var(--home-glow-soft),transparent_64%),linear-gradient(180deg,var(--home-card-ship-start)_0%,var(--home-card-ship-end)_100%)] [@media(max-width:640px)]:min-h-0 [@media(max-width:640px)]:px-5 [@media(max-width:640px)]:py-6"
              data-reveal=""
            >
              <div
                className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,rgba(102,204,215,0.34)_1.5px,transparent_1.6px)] [background-size:18px_18px] [-webkit-mask-image:radial-gradient(ellipse_72%_72%_at_54%_46%,#000_32%,transparent_82%)] [mask-image:radial-gradient(ellipse_72%_72%_at_54%_46%,#000_32%,transparent_82%)]"
                aria-hidden="true"
              />

              <div
                className="relative z-[2] grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-8 pt-3 [@media(max-width:1080px)]:grid-cols-[1fr] [@media(max-width:1080px)]:gap-6"
                role="img"
                aria-label="Minh họa đơn hàng giao tới Đà Nẵng, danh sách tỉnh thành, bảng phí vận chuyển theo phường và trạng thái đang giao"
              >
                {/* Left: 5 province pills */}
                <div
                  className="flex flex-col gap-2 [@media(max-width:1080px)]:flex-row [@media(max-width:1080px)]:flex-wrap [@media(max-width:1080px)]:justify-center [@media(max-width:640px)]:hidden"
                  aria-hidden="true"
                >
                  {SHIPPING_PROVINCES.map((prov) => (
                    <span
                      key={prov.name}
                      className={`inline-flex items-center gap-2 whitespace-nowrap rounded-[var(--home-radius-card)] px-4 py-2 text-[length:var(--home-fs-small)] font-[450] ${
                        prov.active
                          ? "[border:1px_solid_rgba(255,255,255,0.56)] bg-[rgba(255,255,255,0.14)] text-[var(--home-ink)] [box-shadow:var(--home-shadow-pill)]"
                          : "[border:1px_solid_rgba(255,255,255,0.08)] bg-[rgba(255,255,255,0.04)] text-[rgba(255,255,255,0.45)]"
                      }`}
                    >
                      {prov.active ? (
                        <span className="h-2 w-2 flex-none rounded-full bg-[var(--home-accent)]" />
                      ) : null}
                      <span>{prov.name}</span>
                    </span>
                  ))}
                </div>

                {/* Center: product card + floating order pill */}
                <div
                  className="relative flex min-h-[310px] items-center justify-center"
                  aria-hidden="true"
                >
                  <div className="absolute h-[244px] w-[176px] rounded-[var(--home-radius-card)] [border:1px_solid_rgba(255,255,255,0.1)] bg-[rgba(255,255,255,0.08)] [transform:translateX(-118px)_scale(0.92)] [@media(max-width:640px)]:hidden" />
                  <div className="absolute h-[244px] w-[176px] rounded-[var(--home-radius-card)] [border:1px_solid_rgba(255,255,255,0.1)] bg-[rgba(255,255,255,0.08)] [transform:translateX(118px)_scale(0.92)] [@media(max-width:640px)]:hidden" />

                  <div className="relative z-[2] flex w-[228px] flex-col gap-3 rounded-[var(--home-radius-card)] bg-[var(--home-ink)] p-3 [box-shadow:var(--home-shadow-card)]">
                    <div className="relative aspect-[4/5] w-full overflow-hidden rounded-[var(--home-radius-media)] bg-[#eff3f5]">
                      <Image
                        src="/models/model-short-linen-xam.jpg"
                        alt=""
                        fill
                        sizes="240px"
                        className={COVER_TOP_CLS}
                      />
                    </div>
                    <span className="flex h-9 items-center justify-center rounded-[var(--home-radius-media)] bg-[var(--home-ink-dark)] text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink)]">
                      Mua ngay
                    </span>
                  </div>

                  <div className="absolute bottom-[72px] right-[max(0px,calc(50%_-_216px))] z-[3] inline-flex items-center gap-2 whitespace-nowrap rounded-full bg-[var(--home-ink)] px-4 py-2 text-[length:var(--home-fs-small)] font-semibold text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-pill)] [@media(max-width:640px)]:bottom-[68px] [@media(max-width:640px)]:right-0 [@media(max-width:640px)]:text-[length:var(--home-fs-caption)]">
                    <span className="h-2 w-2 flex-none rounded-full bg-[var(--home-accent-solid)]" />
                    <span className="tabular-nums" data-demo-price="">
                      Đơn hàng {formatVnd(349000)} · Đà Nẵng
                    </span>
                  </div>
                </div>

                {/* Right: white shipping fees sheet + 3 delivery statuses */}
                <div
                  className="flex w-[288px] flex-col gap-3 rounded-[var(--home-radius-card)] bg-[var(--home-ink)] p-4 text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-card)] [@media(max-width:1080px)]:mx-auto [@media(max-width:640px)]:hidden"
                  aria-hidden="true"
                >
                  <div className="inline-flex items-center gap-2 text-[length:var(--home-fs-small)] font-semibold text-[var(--home-ink-dark)]">
                    <IconTruck size={15} />
                    <span>Phí giao hàng</span>
                  </div>

                  <ul className="flex flex-col gap-2">
                    {SHIPPING_FEE_ROWS.map((row) => (
                      <li
                        key={row.place}
                        className={`flex items-center justify-between gap-2 rounded-[var(--home-radius-media)] px-3 py-2 text-[length:var(--home-fs-caption)] ${
                          row.active
                            ? "bg-[#e4f1f3] font-semibold text-[var(--home-ink-dark)]"
                            : "bg-[#f7f9fa] text-[var(--home-ink-dark-2)]"
                        }`}
                      >
                        <span className="overflow-hidden text-ellipsis whitespace-nowrap">
                          {row.place}
                        </span>
                        <span
                          className="tabular-nums flex-none font-semibold text-[var(--home-ink-dark)]"
                          data-demo-price=""
                        >
                          {formatVnd(row.feeVnd)}
                        </span>
                      </li>
                    ))}
                  </ul>

                  <div className="flex items-center justify-between gap-1 [border-top:1px_solid_var(--ui-border)] pt-2 text-[length:var(--home-fs-caption)] text-[var(--home-ink-dark-2)]">
                    <span className="inline-flex items-center gap-1 whitespace-nowrap">
                      Đã lấy hàng
                    </span>
                    <span className="text-[#c5cfd5]">·</span>
                    <span className="inline-flex items-center gap-1 whitespace-nowrap font-semibold text-[var(--home-accent-solid)]">
                      <span className="h-[6px] w-[6px] rounded-full bg-[var(--home-accent-solid)]" />
                      Đang giao
                    </span>
                    <span className="text-[#c5cfd5]">·</span>
                    <span className="inline-flex items-center gap-1 whitespace-nowrap">
                      Đã giao
                    </span>
                  </div>
                </div>
              </div>

              <div className="relative z-[2] flex max-w-[540px] flex-col gap-2">
                <h3 className={CARD_TITLE_CLS}>Phí ship theo tỉnh và phường</h3>
                <p className="text-[length:var(--home-fs-body)] leading-[1.5] text-[var(--home-ink-2)]">
                  Khách thấy phí giao hàng ngay khi nhập địa chỉ. Bạn in nhãn và theo dõi đơn đang
                  giao trong Kênh người bán.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* S6. Dành cho mọi quy mô */}
        <section
          className="pb-[calc(var(--home-space-section)_+_var(--home-radius-band))] pt-[var(--home-space-section)] [@media(max-width:960px)]:pb-[calc(var(--home-space-section)_+_var(--home-radius-band-sm))]"
          aria-labelledby="platform-scale-heading"
        >
          <div className={CONTAINER_CLS}>
            <h2
              id="platform-scale-heading"
              className={`${SECTION_HEADING_CLS} max-w-[724px]`}
              data-reveal=""
            >
              Dành cho mọi quy mô, từ shop mới mở đến xưởng sản xuất
            </h2>

            <div
              className="grid grid-cols-[repeat(3,minmax(0,1fr))] gap-[var(--home-gap)] [@media(max-width:768px)]:mr-[calc(var(--home-gutter)*-1)] [@media(max-width:768px)]:flex [@media(max-width:768px)]:gap-4 [@media(max-width:768px)]:overflow-x-auto [@media(max-width:768px)]:pb-1 [@media(max-width:768px)]:pr-[var(--home-gutter)] [@media(max-width:768px)]:[scroll-snap-type:x_mandatory] [@media(max-width:768px)]:[scrollbar-width:none] [@media(max-width:768px)]:[&::-webkit-scrollbar]:hidden"
              data-reveal="rows"
            >
              {SCALE_CARDS.map((card) => (
                <article
                  key={card.id}
                  className="flex min-w-0 flex-col gap-4 [@media(max-width:768px)]:flex-[0_0_78%] [@media(max-width:768px)]:[scroll-snap-align:start]"
                >
                  <div className="relative aspect-[4/3] w-full overflow-hidden rounded-[var(--home-radius-media)] bg-[var(--home-card)]">
                    <Image
                      src={card.image}
                      alt={card.alt}
                      fill
                      sizes="(max-width: 768px) 84vw, 404px"
                      className={COVER_CENTER_CLS}
                    />
                  </div>
                  <div className={CARD_COPY_CLS}>
                    <h3 className={CARD_TITLE_CLS}>{card.title}</h3>
                    <p className={CARD_DESC_CLS}>{card.description}</p>
                  </div>
                </article>
              ))}
            </div>

            <div className="flex justify-center pt-2">
              <Link
                href="/dang-ky?role=seller"
                className="inline-flex min-h-[var(--home-btn-mid-h)] items-center justify-center whitespace-nowrap rounded-full [border:1.5px_solid_rgba(255,255,255,0.8)] bg-transparent px-8 py-3 text-[length:var(--home-fs-body)] font-medium text-[var(--home-ink)] [transition:background-color_200ms_cubic-bezier(0.22,1,0.36,1),border-color_200ms_cubic-bezier(0.22,1,0.36,1),transform_140ms_cubic-bezier(0.22,1,0.36,1)] hover:border-[color:var(--home-ink)] hover:bg-[var(--home-glass-strong)] active:scale-[0.98] [@media(max-width:640px)]:w-full"
              >
                Mở gian hàng
              </Link>
            </div>
          </div>
        </section>
      </div>

      {/* S7–S9 Band: Teal gradient flowing seamlessly into Navy */}
      <div className="relative z-[3] mt-[calc(var(--home-radius-band)*-1)] overflow-hidden rounded-t-[var(--home-radius-band)] bg-[var(--home-navy)] [@media(max-width:960px)]:mt-[calc(var(--home-radius-band-sm)*-1)] [@media(max-width:960px)]:rounded-t-[var(--home-radius-band-sm)]">
        {/* S7. Kênh người bán, có AI làm cùng */}
        <section
          className="pb-[var(--home-space-band-gap)] pt-[var(--home-space-section)] [background:linear-gradient(in_oklab,var(--home-teal-deep)_42%,var(--home-navy)_100%)]"
          aria-labelledby="platform-seller-ai-heading"
        >
          <div className={CONTAINER_CLS}>
            <h2 id="platform-seller-ai-heading" className={SECTION_HEADING_CLS} data-reveal="">
              Kênh người bán, có AI làm cùng
            </h2>

            <div
              className="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)] gap-[var(--home-gap)] [@media(max-width:1080px)]:grid-cols-[1fr]"
              data-reveal="rows"
            >
              {/* Wide card (2fr): Báo cáo AI */}
              <article className="flex min-w-0 flex-col overflow-hidden rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card-dark)]">
                <div
                  className="relative flex min-h-[320px] items-center justify-center overflow-hidden p-6 [background:radial-gradient(ellipse_65%_65%_at_50%_45%,var(--home-glow-soft),transparent_68%),rgb(2,8,12)] [@media(max-width:640px)]:min-h-[250px] [@media(max-width:640px)]:px-4 [@media(max-width:640px)]:py-5"
                  role="img"
                  aria-label="Minh họa ô hỏi Báo cáo AI bằng tiếng Việt về tỷ lệ hoàn hủy trong 30 ngày qua"
                >
                  <div
                    className="absolute left-6 top-4 h-[205px] w-[150px] overflow-hidden rounded-[var(--home-radius-media)] opacity-[0.56]"
                    aria-hidden="true"
                  >
                    <Image
                      src="/models/model-ao-thun-oversize.jpg"
                      alt=""
                      fill
                      sizes="180px"
                      className={COVER_TOP_CLS}
                    />
                  </div>

                  <div
                    className="absolute left-[196px] top-6 h-[124px] w-[96px] overflow-hidden rounded-[var(--home-radius-media)] opacity-[0.45] [@media(max-width:640px)]:hidden"
                    aria-hidden="true"
                  >
                    <Image
                      src="/models/model-polo-kem.jpg"
                      alt=""
                      fill
                      sizes="120px"
                      className={COVER_TOP_CLS}
                    />
                  </div>

                  <div
                    className="absolute bottom-4 right-8 h-[82px] w-[82px] overflow-hidden rounded-[var(--home-radius-media)] bg-[rgba(255,255,255,0.14)] opacity-[0.65] [@media(max-width:640px)]:hidden"
                    aria-hidden="true"
                  >
                    <Image
                      src="/products/quan-jogger-tech-den.png"
                      alt=""
                      fill
                      sizes="96px"
                      className={CONTAIN_IMG_CLS}
                    />
                  </div>

                  <div
                    className="absolute right-6 top-5 flex flex-col gap-2 rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[rgba(255,255,255,0.1)] px-4 py-3 [@media(max-width:640px)]:hidden"
                    aria-hidden="true"
                  >
                    <span className="text-[length:var(--home-fs-caption)] font-medium text-[var(--home-ink-2)]">
                      Doanh thu 30 ngày
                    </span>
                    <div className="flex h-6 items-end gap-1">
                      <span className="h-[40%] w-[10px] rounded-[2px] bg-[var(--home-accent)] opacity-[0.72]" />
                      <span className="h-[62%] w-[10px] rounded-[2px] bg-[var(--home-accent)] opacity-[0.72]" />
                      <span className="h-[48%] w-[10px] rounded-[2px] bg-[var(--home-accent)] opacity-[0.72]" />
                      <span className="h-[82%] w-[10px] rounded-[2px] bg-[var(--home-accent)] opacity-[0.72]" />
                      <span className="h-full w-[10px] rounded-[2px] bg-[var(--home-accent)] opacity-[0.72]" />
                    </div>
                  </div>

                  <div
                    className="relative z-[2] flex w-[min(100%,490px)] flex-col gap-5 rounded-[var(--home-radius-tile)] bg-[var(--home-ink)] px-5 pb-4 pt-5 text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-card)]"
                    aria-hidden="true"
                  >
                    <p className="text-[length:var(--home-fs-body)] font-[450] leading-[1.4] text-[var(--home-ink-dark)]">
                      Sản phẩm nào có tỷ lệ hoàn hủy cao nhất trong 30 ngày qua?
                    </p>
                    <div className="flex items-center justify-between">
                      <span className="inline-flex h-8 w-8 items-center justify-center rounded-full bg-[#e4f1f3] text-[var(--home-accent-solid)]">
                        <IconSpark size={14} />
                      </span>
                      <span className="inline-flex h-9 w-9 items-center justify-center rounded-full bg-[var(--home-accent-solid)] text-[var(--home-ink)]">
                        <IconSend size={14} />
                      </span>
                    </div>
                  </div>
                </div>

                <div className="flex flex-col gap-2 px-8 py-6 [@media(max-width:640px)]:p-5">
                  <h3 className={`${CARD_TITLE_CLS} inline-flex items-center gap-2`}>
                    <span>Báo cáo AI cho người bán</span>
                    <IconSpark size={17} className="flex-none text-[var(--home-accent)]" />
                  </h3>
                  <p className={CARD_DESC_CLS}>
                    Hỏi bằng tiếng Việt về doanh thu, đơn hàng, hoàn hủy. AI trả lời từ số liệu cửa
                    hàng của bạn.
                  </p>
                </div>
              </article>

              {/* Narrow card (1fr): Chat với khách */}
              <article className="flex min-w-0 flex-col overflow-hidden rounded-[var(--home-radius-card)] [border:1px_solid_var(--home-border)] bg-[var(--home-card-dark)]">
                <div
                  className="flex min-h-[320px] items-center justify-center p-6 [background:radial-gradient(ellipse_70%_60%_at_50%_35%,var(--home-glow-soft),transparent_68%),rgb(3,10,18)]"
                  role="img"
                  aria-label="Minh họa khung chat hai chiều giữa khách hàng và người bán trong Kênh người bán"
                >
                  <div className="flex w-full flex-col gap-3" aria-hidden="true">
                    <div className="max-w-[86%] self-start [border-radius:var(--home-radius-bubble)_var(--home-radius-bubble)_var(--home-radius-bubble)_4px] [border:1px_solid_var(--home-border)] bg-[rgba(255,255,255,0.12)] px-4 py-3 text-[length:var(--home-fs-small)] leading-[1.45] text-[var(--home-ink)]">
                      Shop ơi, đơn áo polo của mình đã gửi đi chưa ạ?
                    </div>
                    <div className="max-w-[86%] self-end [border-radius:var(--home-radius-bubble)_var(--home-radius-bubble)_4px_var(--home-radius-bubble)] bg-[var(--home-accent-solid)] px-4 py-3 text-[length:var(--home-fs-small)] leading-[1.45] text-[var(--home-ink)] [box-shadow:var(--home-shadow-pill)]">
                      Đã bàn giao vận chuyển lúc 14h, dự kiến mai bạn nhận được nhé.
                    </div>
                    <div className="max-w-[86%] self-start [border-radius:var(--home-radius-bubble)_var(--home-radius-bubble)_var(--home-radius-bubble)_4px] [border:1px_solid_var(--home-border)] bg-[rgba(255,255,255,0.12)] px-4 py-3 text-[length:var(--home-fs-small)] leading-[1.45] text-[var(--home-ink)]">
                      Cảm ơn shop nhiều!
                    </div>
                  </div>
                </div>

                <div className="flex flex-col gap-2 px-8 py-6 [@media(max-width:640px)]:p-5">
                  <h3 className={CARD_TITLE_CLS}>Chat với khách</h3>
                  <p className={CARD_DESC_CLS}>
                    Trả lời tin nhắn của khách ngay trong Kênh người bán.
                  </p>
                </div>
              </article>
            </div>
          </div>
        </section>

        {/* S8. Tường công cụ (Navy, seamless from S7; top rows start right of copy, bottom rows span full width) */}
        <section
          className="relative flex flex-col gap-[14px] overflow-hidden bg-[var(--home-navy)] py-[var(--home-space-band-gap)] [@media(max-width:960px)]:gap-6"
          aria-labelledby="platform-tools-heading"
        >
          <svg
            className="pointer-events-none absolute h-0 w-0 overflow-hidden"
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

          <div className="grid w-full grid-cols-[minmax(0,520px)_minmax(0,1fr)] items-end gap-12 pl-[max(var(--home-gutter),calc((100%_-_var(--home-max))_/_2))] [@media(max-width:960px)]:grid-cols-[1fr] [@media(max-width:960px)]:gap-0 [@media(max-width:960px)]:px-[var(--home-gutter)]">
            <div
              className="flex min-w-0 flex-col gap-5 pb-5 [@media(max-width:960px)]:pb-0"
              data-reveal=""
            >
              <h2 id="platform-tools-heading" className={SECTION_HEADING_CLS}>
                Đủ công cụ để vận hành cửa hàng
              </h2>
              <p className={`${SECTION_LEAD_BASE} max-w-[44ch]`}>
                Sản phẩm, nhập CSV, tồn kho, đơn hàng, in nhãn, đổi trả, voucher, flash sale, báo
                cáo. Có sẵn trong Kênh người bán, không phải cài thêm.
              </p>
            </div>

            <div
              className="pointer-events-none flex w-full justify-end overflow-hidden [-webkit-mask-composite:source-in] [-webkit-mask-image:linear-gradient(90deg,transparent_0%,rgba(0,0,0,0.42)_24%,#000_54%),linear-gradient(180deg,transparent_0%,rgba(0,0,0,0.42)_30%,#000_72%)] [mask-composite:intersect] [mask-image:linear-gradient(90deg,transparent_0%,rgba(0,0,0,0.42)_24%,#000_54%),linear-gradient(180deg,transparent_0%,rgba(0,0,0,0.42)_30%,#000_72%)] [@media(max-width:960px)]:hidden"
              aria-hidden="true"
            >
              <div className="grid flex-none grid-cols-[repeat(12,54px)] gap-[14px] pr-[14px]">
                {WALL_TOP_TILES.map(({ key, iconIdx, bg }) => (
                  <span key={key} className={TOOL_TILE_CLS} style={{ background: bg }}>
                    <svg width={22} height={22}>
                      <use href={`#home-wall-icon-${iconIdx}`} />
                    </svg>
                  </span>
                ))}
              </div>
            </div>
          </div>

          <div
            className="pointer-events-none flex w-full justify-end overflow-hidden [-webkit-mask-image:linear-gradient(180deg,#000_0%,#000_56%,rgba(0,0,0,0.14)_100%)] [mask-image:linear-gradient(180deg,#000_0%,#000_56%,rgba(0,0,0,0.14)_100%)] [@media(max-width:960px)]:justify-center [@media(max-width:960px)]:[-webkit-mask-image:linear-gradient(180deg,transparent_0%,#000_18%,#000_80%,transparent_100%)] [@media(max-width:960px)]:[mask-image:linear-gradient(180deg,transparent_0%,#000_18%,#000_80%,transparent_100%)]"
            aria-hidden="true"
          >
            <div className="grid flex-none grid-cols-[repeat(24,54px)] gap-[14px] pr-[14px] [@media(max-width:960px)]:grid-cols-[repeat(9,44px)] [@media(max-width:960px)]:gap-[10px] [@media(max-width:960px)]:px-0 [@media(max-width:960px)]:py-2">
              {WALL_BOTTOM_TILES.map(({ key, iconIdx, bg }) => (
                <span key={key} className={TOOL_TILE_CLS} style={{ background: bg }}>
                  <svg width={22} height={22}>
                    <use href={`#home-wall-icon-${iconIdx}`} />
                  </svg>
                </span>
              ))}
            </div>
          </div>
        </section>

        {/* S9. Site Studio (Navy) */}
        <section
          className="bg-[var(--home-navy)] pb-[calc(var(--home-space-section)_+_var(--home-radius-band))] pt-[var(--home-space-band-gap)] [@media(max-width:960px)]:pb-[calc(var(--home-space-section)_+_var(--home-radius-band-sm))]"
          aria-labelledby="platform-studio-heading"
        >
          <div className={CONTAINER_CLS}>
            <div className="flex flex-col gap-5" data-reveal="">
              <h2 id="platform-studio-heading" className={SECTION_HEADING_CLS}>
                Tùy chỉnh giao diện.
                <br />
                Không cần viết code.
              </h2>
              <p className={`${SECTION_LEAD_BASE} max-w-[60ch]`}>
                Site Studio có 8 mẫu giao diện. Chỉnh bản nháp, xem trước, xuất bản khi ưng và khôi
                phục phiên bản cũ khi cần. Gắn tên miền riêng ngay trong đó.
              </p>
            </div>

            <div
              className="relative flex min-h-[500px] w-full flex-col items-center justify-end overflow-hidden rounded-[var(--home-radius-card)] [border:1px_solid_rgba(102,204,215,0.16)] px-[clamp(16px,3vw,40px)] pb-0 pt-[clamp(28px,4vw,56px)] [background:radial-gradient(ellipse_68%_60%_at_50%_42%,var(--home-glow-navy),transparent_68%),radial-gradient(ellipse_46%_45%_at_72%_30%,var(--home-glow-soft),transparent_65%),var(--home-card-navy)]"
              role="img"
              aria-label="Minh họa cửa sổ Site Studio bản nháp gồm danh sách khối bên trái và khung xem trước cửa hàng bên phải"
              data-reveal="scale"
            >
              <div
                className="pointer-events-none absolute inset-0 [background-image:linear-gradient(rgba(102,204,215,0.06)_1px,transparent_1px),linear-gradient(90deg,rgba(102,204,215,0.06)_1px,transparent_1px)] [background-size:40px_40px] [-webkit-mask-image:radial-gradient(ellipse_80%_75%_at_50%_50%,#000_35%,transparent_88%)] [mask-image:radial-gradient(ellipse_80%_75%_at_50%_50%,#000_35%,transparent_88%)]"
                aria-hidden="true"
              />

              {/* 3 floating feature pills */}
              <span
                className={`${STUDIO_FLOAT_PILL_CLS} left-1/2 top-6 [transform:translateX(-50%)]`}
                aria-hidden="true"
              >
                <span className={STUDIO_PILL_DOT_CLS} />
                Chọn mẫu giao diện
              </span>
              <span className={`${STUDIO_FLOAT_PILL_CLS} left-6 top-[26%]`} aria-hidden="true">
                <span className={STUDIO_PILL_DOT_CLS} />
                Xem trước rồi xuất bản
              </span>
              <span className={`${STUDIO_FLOAT_PILL_CLS} bottom-[24%] right-6`} aria-hidden="true">
                <span className={STUDIO_PILL_DOT_CLS} />
                Khôi phục phiên bản cũ
              </span>

              {/* Site Studio window */}
              <div className="relative z-[2] mt-12 w-[min(100%,840px)]" aria-hidden="true">
                <span className="absolute -right-[22px] -top-[26px] z-[5] inline-flex h-[56px] w-[56px] items-center justify-center rounded-full bg-[var(--home-accent)] text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-float),0_0_32px_var(--home-glow)] [@media(max-width:960px)]:-top-[18px] [@media(max-width:960px)]:right-3 [@media(max-width:960px)]:h-[44px] [@media(max-width:960px)]:w-[44px]">
                  <IconSliders size={24} />
                </span>

                <div className="w-full overflow-hidden rounded-t-[var(--home-radius-tile)] border-solid border-[rgba(255,255,255,0.16)] [border-width:1px_1px_0_1px] bg-[rgb(5,10,22)] [box-shadow:var(--home-shadow-card)]">
                  <div className="flex items-center justify-between [border-bottom:1px_solid_var(--home-border)] bg-[rgb(11,18,34)] px-4 py-3">
                    <div className="inline-flex items-center gap-[7px]">
                      <span className="h-[10px] w-[10px] rounded-full bg-[var(--home-window-dot-1)]" />
                      <span className="h-[10px] w-[10px] rounded-full bg-[var(--home-window-dot-2)]" />
                      <span className="h-[10px] w-[10px] rounded-full bg-[var(--home-window-dot-3)]" />
                    </div>
                    <span className="text-[length:var(--home-fs-caption)] font-medium text-[var(--home-ink-2)]">
                      Site Studio · Bản nháp
                    </span>
                    <span className="w-[44px]" />
                  </div>

                  <div className="grid min-h-[360px] grid-cols-[210px_minmax(0,1fr)] [@media(max-width:960px)]:min-h-0 [@media(max-width:960px)]:grid-cols-[1fr]">
                    <div className="flex flex-col gap-3 [border-right:1px_solid_var(--home-border)] bg-[rgb(8,14,28)] p-4 [@media(max-width:960px)]:border-r-0 [@media(max-width:960px)]:[border-bottom:1px_solid_var(--home-border)]">
                      <p className="text-[length:var(--home-fs-caption)] font-medium text-[var(--home-ink-3)]">
                        Bố cục trang chủ
                      </p>
                      <ul className="flex flex-col gap-1 [@media(max-width:960px)]:flex-row [@media(max-width:960px)]:flex-wrap">
                        {STUDIO_SECTIONS.map((item) => (
                          <li
                            key={item.label}
                            className={`flex items-center gap-2 rounded-[var(--home-radius-media)] px-3 py-2 text-[length:var(--home-fs-small)] ${
                              item.active
                                ? "[border:1px_solid_rgba(102,204,215,0.4)] bg-[rgba(102,204,215,0.14)] font-medium text-[var(--home-ink)]"
                                : "text-[var(--home-ink-2)]"
                            }`}
                          >
                            <span
                              className={`h-[6px] w-[6px] flex-none rounded-full ${
                                item.active
                                  ? "bg-[var(--home-accent)] opacity-100"
                                  : "bg-current opacity-60"
                              }`}
                            />
                            <span>{item.label}</span>
                          </li>
                        ))}
                      </ul>
                    </div>

                    <div className="flex bg-[rgb(4,9,18)] p-4 [@media(max-width:960px)]:min-h-[260px]">
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
      <div className="relative z-[4] mt-[calc(var(--home-radius-band)*-1)] overflow-hidden rounded-t-[var(--home-radius-band)] [background:radial-gradient(ellipse_52%_42%_at_85%_18%,var(--home-glow-soft),transparent_64%),radial-gradient(ellipse_46%_36%_at_15%_78%,rgba(102,204,215,0.12),transparent_66%),var(--home-pine)] [@media(max-width:960px)]:mt-[calc(var(--home-radius-band-sm)*-1)] [@media(max-width:960px)]:rounded-t-[var(--home-radius-band-sm)]">
        {/* S10. Thanh toán */}
        <section
          className="pb-[var(--home-space-band-gap)] pt-[var(--home-space-section)]"
          aria-labelledby="platform-payment-heading"
        >
          <div className={CONTAINER_CLS}>
            <h2 id="platform-payment-heading" className={CENTER_HEADING_CLS} data-reveal="">
              Bán cho khách Việt, theo cách khách Việt mua
            </h2>

            <div className="grid grid-cols-[minmax(0,0.94fr)_minmax(0,1.06fr)] items-center gap-12 [@media(max-width:960px)]:grid-cols-[1fr] [@media(max-width:960px)]:gap-8">
              <div className="flex min-w-0 flex-col gap-8" data-reveal="">
                <h3 className={SUB_HEADING_CLS}>Thanh toán quen thuộc với khách Việt</h3>

                <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] items-stretch gap-6 [border-top:1px_solid_var(--home-border)] pt-5">
                  <div className="flex flex-col justify-between gap-3">
                    <span className="inline-flex items-start gap-2 text-[length:var(--home-fs-caption)] font-semibold uppercase leading-[1.35] tracking-[0.04em] text-[var(--home-accent)] [&>svg]:mt-px [&>svg]:flex-none">
                      <IconTruck size={14} />
                      <span>THU TIỀN KHI GIAO</span>
                    </span>
                    <span className="text-[length:var(--home-fs-h2)] font-light leading-[1.05] tracking-[-0.025em] text-[var(--home-ink)]">
                      COD
                    </span>
                  </div>

                  <div className="flex flex-col justify-between gap-3">
                    <span className="inline-flex items-start gap-2 text-[length:var(--home-fs-caption)] font-semibold uppercase leading-[1.35] tracking-[0.04em] text-[var(--home-accent)] [&>svg]:mt-px [&>svg]:flex-none">
                      <IconCheck size={14} />
                      <span>TỰ XÁC NHẬN KHI TIỀN VỀ</span>
                    </span>
                    <span className="text-[length:var(--home-fs-h2)] font-light leading-[1.05] tracking-[-0.025em] text-[var(--home-ink)]">
                      VietQR
                    </span>
                  </div>
                </div>

                <p className="max-w-[44ch] [border-left:2px_solid_var(--home-accent)] pl-5 text-[length:var(--home-fs-lead)] leading-[1.5] text-[var(--home-ink-2)]">
                  Khách chọn thanh toán khi nhận hàng hoặc quét mã VietQR. Đơn chuyển khoản tự xác
                  nhận khi tiền về, bạn không phải đối soát tay.
                </p>
              </div>

              <div
                className="relative flex items-center justify-center px-8 py-[72px] [@media(max-width:960px)]:px-2 [@media(max-width:960px)]:py-12"
                role="img"
                aria-label="Minh họa trang thanh toán tóm tắt đơn hàng với lựa chọn COD và mã VietQR"
                data-reveal="scale"
              >
                {FLOATING_PRODUCTS.map((item) => (
                  <div
                    key={item.slot}
                    className={`${FLOAT_PROD_BASE} ${item.cls}`}
                    aria-hidden="true"
                  >
                    <Image src={item.src} alt="" fill sizes="84px" className={CONTAIN_IMG_CLS} />
                  </div>
                ))}

                <div
                  className="relative z-[2] flex w-[min(100%,480px)] flex-col gap-4 rounded-[var(--home-radius-card)] bg-[var(--home-ink)] p-6 text-[var(--home-ink-dark)] [box-shadow:var(--home-shadow-card)] [@media(max-width:640px)]:p-5"
                  aria-hidden="true"
                >
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="text-[length:var(--home-fs-body)] font-semibold text-[var(--home-ink-dark)]">
                      Tóm tắt đơn hàng
                    </span>
                    <span className="tabular-nums text-[length:var(--home-fs-small)] text-[var(--home-ink-dark-2)]">
                      2 sản phẩm
                    </span>
                  </div>

                  <ul className="flex flex-col gap-2">
                    <li className="flex items-baseline justify-between gap-3 text-[length:var(--home-fs-small)] leading-[1.4] text-[var(--home-ink-dark)]">
                      <span>Áo Polo Pique Xanh Đá · Size L</span>
                      <span className="tabular-nums" data-demo-price="">
                        {formatVnd(299000)}
                      </span>
                    </li>
                    <li className="flex items-baseline justify-between gap-3 text-[length:var(--home-fs-small)] leading-[1.4] text-[var(--home-ink-dark)]">
                      <span>Quần Short Daily Be · Size 31</span>
                      <span className="tabular-nums" data-demo-price="">
                        {formatVnd(249000)}
                      </span>
                    </li>
                    <li className="flex items-baseline justify-between gap-3 text-[length:var(--home-fs-small)] leading-[1.4] text-[var(--home-ink-dark-2)]">
                      <span>Phí vận chuyển (Hà Nội)</span>
                      <span className="tabular-nums" data-demo-price="">
                        {formatVnd(22000)}
                      </span>
                    </li>
                  </ul>

                  <div className="flex items-baseline justify-between gap-3 rounded-[var(--home-radius-media)] bg-[#eff3f5] px-4 py-3 text-[length:var(--home-fs-small)] font-semibold text-[var(--home-ink-dark)]">
                    <span>Tổng thanh toán</span>
                    <span
                      className="tabular-nums text-[length:var(--home-fs-body)] font-bold text-[var(--home-ink-dark)]"
                      data-demo-price=""
                    >
                      {formatVnd(570000)}
                    </span>
                  </div>

                  <div className="flex flex-col gap-2">
                    <div className="flex items-center gap-3 rounded-[var(--home-radius-media)] [border:1px_solid_var(--ui-border)] bg-[var(--home-ink)] px-4 py-3 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink-dark-2)]">
                      <span className="inline-block h-4 w-4 flex-none rounded-full [border:2px_solid_#c5cfd5]" />
                      <span>COD · Thu tiền khi giao hàng</span>
                    </div>
                    <div className="flex items-center gap-3 rounded-[var(--home-radius-media)] [border:1px_solid_var(--home-accent-solid)] bg-[#e4f1f3] px-4 py-3 text-[length:var(--home-fs-small)] font-medium text-[var(--home-ink-dark)]">
                      <span className="inline-block h-4 w-4 flex-none rounded-full [border:5px_solid_var(--home-accent-solid)] bg-[var(--home-ink)]" />
                      <span>VietQR · Chuyển khoản quét mã</span>
                    </div>
                  </div>

                  <div className="flex items-center gap-4 rounded-[var(--home-radius-media)] bg-[#f7f9fa] p-3 [@media(max-width:640px)]:flex-col [@media(max-width:640px)]:items-start">
                    <div className="flex-none rounded-[var(--home-radius-media)] [border:1px_solid_var(--ui-border)] bg-[var(--home-ink)] p-2">
                      <VietQrSvg size={96} className="block h-[88px] w-[88px]" decorative />
                    </div>
                    <div className="flex min-w-0 flex-col items-start gap-2">
                      <span className="inline-flex items-center gap-1 rounded-full bg-[#e3f2eb] px-3 py-1 text-[length:var(--home-fs-caption)] font-semibold text-[#145338]">
                        <IconCheck size={14} />
                        <span>Tự động xác nhận</span>
                      </span>
                      <p className="text-[length:var(--home-fs-small)] leading-[1.45] text-[var(--home-ink-dark-2)]">
                        Quét mã bằng ứng dụng ngân hàng để hoàn tất đơn{" "}
                        <span className="tabular-nums" data-demo-price="">
                          {formatVnd(570000)}
                        </span>
                        .
                      </p>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* S10b. Quả địa cầu (same Pine band, between S10 Thanh toán and S11 FAQ) */}
        <section
          className="relative overflow-hidden py-[var(--home-space-band-gap)]"
          aria-labelledby="platform-globe-heading"
        >
          <div className="pointer-events-none absolute inset-0 z-0" aria-hidden="true">
            {GLOBE_SECTION_STARS.map((star, idx) => (
              <span
                key={`star-${idx}`}
                className={`absolute rounded-full bg-[rgba(236,252,252,0.85)] ${
                  star.size === "halo"
                    ? "h-[4px] w-[4px] opacity-90 [box-shadow:0_0_12px_3px_rgba(206,246,248,0.5),0_0_28px_8px_rgba(102,204,215,0.22)]"
                    : star.size === "md"
                      ? "h-[3px] w-[3px] opacity-[0.68] [box-shadow:0_0_8px_rgba(206,246,248,0.55)]"
                      : "h-[2px] w-[2px] opacity-[0.48]"
                }`}
                style={{ left: star.left, top: star.top }}
              />
            ))}
          </div>

          <div className={CONTAINER_CLS}>
            <div className="relative z-[1] grid grid-cols-[minmax(0,58fr)_minmax(0,42fr)] items-center gap-[clamp(28px,4.5vw,68px)] [@media(max-width:960px)]:grid-cols-[1fr] [@media(max-width:960px)]:gap-8">
              <div className="min-w-0">
                <PlatformGlobe className="w-full" />
              </div>

              <div
                className="flex min-w-0 flex-col gap-5 pt-[clamp(72px,7vw,96px)] [@media(max-width:960px)]:order-[-1] [@media(max-width:960px)]:pt-0"
                data-reveal=""
              >
                <h2 id="platform-globe-heading" className={SUB_HEADING_CLS}>
                  Vững khi đông khách.
                  <br />
                  Chính xác từng đơn.
                </h2>
                <p className="max-w-[44ch] text-[length:var(--home-fs-lead)] font-normal leading-[1.5] text-[var(--home-ink-2)]">
                  Flash sale và voucher trừ suất ngay khi khách đặt, tồn kho giữ theo từng SKU, nên
                  không bán quá số lượng kể cả khi nhiều người mua cùng lúc.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* S11. FAQ (still on Pine band) */}
        <section
          className="pb-[calc(var(--home-space-section)_+_var(--home-radius-band))] pt-[var(--home-space-band-gap)] [@media(max-width:960px)]:pb-[calc(var(--home-space-section)_+_var(--home-radius-band-sm))]"
          aria-labelledby="platform-faq-heading"
        >
          <div className={CONTAINER_CLS}>
            <div className="grid grid-cols-[minmax(0,4.2fr)_minmax(0,7.8fr)] items-start gap-[clamp(32px,5vw,72px)] [@media(max-width:960px)]:grid-cols-[1fr] [@media(max-width:960px)]:gap-8">
              <div className="sticky top-[calc(var(--home-header-h)_+_24px)] [@media(max-width:960px)]:static">
                <h2 id="platform-faq-heading" className={SUB_HEADING_CLS} data-reveal="">
                  Câu hỏi thường gặp
                </h2>
              </div>

              <div
                className="flex flex-col [border-top:1px_solid_var(--home-border)]"
                data-reveal="rows"
              >
                {PLATFORM_FAQS.map((item) => (
                  <details
                    key={item.id}
                    name="platform-faq"
                    className="group [border-bottom:1px_solid_var(--home-border)] bg-transparent"
                  >
                    <summary className="flex min-h-[72px] cursor-pointer list-none items-center justify-between gap-5 py-5 [&::-webkit-details-marker]:hidden">
                      <span className="text-[length:var(--home-fs-h3)] font-[450] leading-[1.35] text-[var(--home-ink)]">
                        {item.question}
                      </span>
                      <span
                        className="relative h-4 w-4 flex-none text-[var(--home-accent)] before:absolute before:left-0 before:top-1/2 before:h-[1.5px] before:w-4 before:[transform:translateY(-50%)] before:bg-current before:[transition:transform_280ms_cubic-bezier(0.16,1,0.3,1)] before:content-[''] after:absolute after:left-0 after:top-1/2 after:h-[1.5px] after:w-4 after:[transform:translateY(-50%)_rotate(90deg)] after:bg-current after:[transition:transform_280ms_cubic-bezier(0.16,1,0.3,1)] after:content-[''] group-open:after:[transform:translateY(-50%)_rotate(0deg)]"
                        aria-hidden="true"
                      />
                    </summary>
                    <div className="animate-[nm-home-faq-reveal_280ms_cubic-bezier(0.22,1,0.36,1)_both] pb-6 pl-0 pr-8 pt-0 [@media(max-width:640px)]:pr-0">
                      <p className="max-w-[58ch] text-[length:var(--home-fs-body)] leading-[1.6] text-[var(--home-ink-2)]">
                        {item.answer}
                      </p>
                    </div>
                  </details>
                ))}
              </div>
            </div>
          </div>
        </section>
      </div>

      {/* S12. Lên sóng trong ba bước (48px rounded top overlapping S11) */}
      <section
        className="relative z-[5] mt-[calc(var(--home-radius-band)*-1)] rounded-t-[var(--home-radius-band)] py-[var(--home-space-section)] [background:linear-gradient(180deg,rgba(2,8,8,0.5)_0%,transparent_20%,transparent_88%,rgba(2,8,8,0.72)_100%),radial-gradient(ellipse_56%_52%_at_88%_85%,rgba(102,204,215,0.2),transparent_64%),radial-gradient(ellipse_40%_42%_at_72%_46%,rgba(255,255,255,0.06),transparent_60%),var(--home-steps-bg)] [@media(max-width:960px)]:mt-[calc(var(--home-radius-band-sm)*-1)] [@media(max-width:960px)]:rounded-t-[var(--home-radius-band-sm)]"
        aria-labelledby="platform-steps-heading"
      >
        <div className={CONTAINER_CLS}>
          <h2 id="platform-steps-heading" className={CENTER_HEADING_CLS} data-reveal="">
            Lên sóng trong ba bước
          </h2>

          <div className="grid grid-cols-[minmax(0,0.96fr)_minmax(0,1.04fr)] items-center gap-[clamp(32px,5vw,72px)] [@media(max-width:960px)]:grid-cols-[1fr] [@media(max-width:960px)]:gap-8">
            <div
              className="grid grid-cols-[repeat(2,minmax(0,1fr))] items-start gap-[var(--home-gap-sm)] [@media(max-width:640px)]:gap-3"
              data-reveal="rows"
            >
              <div className="relative aspect-[3/4] w-full overflow-hidden rounded-[var(--home-radius-media)] bg-[var(--home-card)]">
                <Image
                  src="/models/model-ao-thun-trang.jpg"
                  alt="Sản phẩm áo thun cotton trưng bày trên cửa hàng trực tuyến"
                  fill
                  sizes="(max-width: 900px) 46vw, 280px"
                  className={COVER_TOP_CLS}
                />
              </div>
              <div className="relative mt-[clamp(32px,5vw,72px)] aspect-[3/4] w-full overflow-hidden rounded-[var(--home-radius-media)] bg-[var(--home-card)] [@media(max-width:640px)]:mt-8">
                <Image
                  src="/banners/fabric-closeup.jpg"
                  alt="Bề mặt vải dệt tinh chọn của sản phẩm Việt"
                  fill
                  sizes="(max-width: 900px) 46vw, 280px"
                  className={COVER_CENTER_CLS}
                />
              </div>
            </div>

            <div className="flex min-w-0 flex-col gap-12">
              <ol className="flex flex-col" data-reveal="rows">
                {LAUNCH_STEPS.map((step) => (
                  <li
                    key={step.num}
                    className="flex items-baseline gap-6 [border-bottom:1px_solid_var(--home-rule)] py-6 first:pt-0"
                  >
                    <span className="tabular-nums min-w-[2.2ch] flex-none text-[length:var(--home-fs-body)] font-medium text-[var(--home-accent)]">
                      {step.num}
                    </span>
                    <span className="text-[length:var(--home-fs-step)] font-[330] leading-[1.15] tracking-[-0.02em] text-[var(--home-ink)]">
                      {step.title}
                    </span>
                  </li>
                ))}
              </ol>

              <div className="w-full max-w-[560px]">
                <SubdomainPreview rootDomain={rootDomain} />
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
