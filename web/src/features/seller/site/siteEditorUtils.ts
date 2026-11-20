// Pure client & server utilities for Seller Site Editor:
// WCAG 2.1 contrast calculation, subdomain rules, SafeURL/SafeHref checks, presets, and section defaults.

import type {
  SiteConfig,
  SiteFontFamily,
  SiteSection,
  SiteSectionType,
  SiteTheme,
} from "./siteEditorTypes";

export const MAX_SECTIONS = 20;
export const MIN_WCAG_CONTRAST = 4.5;

export const ALLOWED_FONTS: ReadonlyArray<{
  value: SiteFontFamily;
  label: string;
  hint: string;
}> = [
  { value: "work-sans", label: "Work Sans", hint: "Gọn nét, đồng bộ sàn NexusMart" },
  { value: "be-vietnam-pro", label: "Be Vietnam Pro", hint: "Hỗ trợ dấu tiếng Việt sắc nét" },
  { value: "inter", label: "Inter", hint: "Hiện đại, dễ đọc cho mô tả sản phẩm" },
  { value: "ibm-plex-sans", label: "IBM Plex Sans", hint: "Chính xác, phong cách xưởng kỹ thuật" },
  {
    value: "plus-jakarta-sans",
    label: "Plus Jakarta Sans",
    hint: "Mềm mại cho thương hiệu thời trang",
  },
];

export interface ThemePalettePreset {
  id: string;
  name: string;
  description: string;
  colors: Pick<
    SiteTheme,
    "bg_color" | "surface_color" | "text_color" | "accent_color" | "accent_text_color"
  >;
}

/** All presets satisfy WCAG 2.1 >= 4.5:1 on every required pair. */
export const THEME_PRESETS: readonly ThemePalettePreset[] = [
  {
    id: "frost-slate",
    name: "Sương & Đá",
    description: "Nền sáng tinh gọn, nhấn xanh ngọc đậm.",
    colors: {
      bg_color: "#F7F9FA",
      surface_color: "#FFFFFF",
      text_color: "#1F3038",
      accent_color: "#0D7480",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "pine-linen",
    name: "Xưởng Mộc Xanh",
    description: "Nền đá sáng ấm, nhấn xanh lá rừng.",
    colors: {
      bg_color: "#F4F5F2",
      surface_color: "#FFFFFF",
      text_color: "#1F2622",
      accent_color: "#1E5C45",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "harbor-navy",
    name: "Hải Cảng Xanh Than",
    description: "Nền xanh sương nhẹ, chữ xanh than đậm.",
    colors: {
      bg_color: "#F2F5F9",
      surface_color: "#FFFFFF",
      text_color: "#17263B",
      accent_color: "#1E4B7A",
      accent_text_color: "#FFFFFF",
    },
  },
  {
    id: "studio-charcoal",
    name: "Studio Than Đêm",
    description: "Nền tối sâu, điểm nhấn xanh biển sáng.",
    colors: {
      bg_color: "#14191F",
      surface_color: "#1E252D",
      text_color: "#F2F5F8",
      accent_color: "#5CC6D0",
      accent_text_color: "#10161B",
    },
  },
];

export const QUICK_MEDIA_ASSETS: ReadonlyArray<{ label: string; url: string }> = [
  { label: "Xưởng dệt Việt Nam", url: "/banners/vietnam-workshop.jpg" },
  { label: "Cơ bản hằng ngày", url: "/banners/campaign-everyday.jpg" },
  { label: "Công sở gọn gàng", url: "/banners/campaign-office.jpg" },
  { label: "Chạy bộ & Tập luyện", url: "/banners/campaign-running.jpg" },
  { label: "Sợi Xanh Nexus", url: "/banners/campaign-green-fiber.jpg" },
  { label: "Cận cảnh chất vải", url: "/banners/fabric-closeup.jpg" },
];

export const SECTION_TYPE_META: Record<SiteSectionType, { label: string; description: string }> = {
  hero: {
    label: "Banner mở đầu",
    description: "Tiêu đề lớn, ảnh chủ đạo và nút điều hướng đầu trang.",
  },
  featured_products: {
    label: "Sản phẩm nổi bật",
    description: "Hiển thị sản phẩm tiêu biểu của riêng gian hàng.",
  },
  collection: {
    label: "Bộ sưu tập / Danh mục",
    description: "Giới thiệu một dòng sản phẩm hoặc danh mục cụ thể.",
  },
  text: {
    label: "Khối câu chuyện",
    description: "Giới thiệu xưởng may, chất liệu hoặc thông điệp thương hiệu.",
  },
  banner: {
    label: "Dải quảng bá",
    description: "Khối nhấn ngang kèm ảnh và nút xem ưu đãi.",
  },
  voucher_strip: {
    label: "Mã giảm giá gian hàng",
    description: "Hiển thị các mã giảm giá riêng của gian hàng để khách lưu.",
  },
  policies: {
    label: "Cam kết & Chính sách",
    description: "Danh sách chính sách đổi trả, kiểm hàng và giao nhận.",
  },
};

export const SECTION_TYPE_ORDER: readonly SiteSectionType[] = [
  "hero",
  "featured_products",
  "collection",
  "voucher_strip",
  "banner",
  "text",
  "policies",
];

const SUBDOMAIN_RE = /^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$/;

const RESERVED_SUBDOMAINS = new Set([
  "www",
  "api",
  "admin",
  "seller",
  "app",
  "mail",
  "smtp",
  "imap",
  "pop",
  "static",
  "cdn",
  "assets",
  "media",
  "img",
  "images",
  "files",
  "auth",
  "login",
  "sso",
  "oauth",
  "account",
  "accounts",
  "checkout",
  "cart",
  "pay",
  "payment",
  "payments",
  "billing",
  "shop",
  "store",
  "storefront",
  "s",
  "status",
  "health",
  "docs",
  "help",
  "support",
  "blog",
  "news",
  "portal",
  "dashboard",
  "console",
  "internal",
  "staging",
  "dev",
  "test",
  "demo",
  "localhost",
  "nexus",
  "nexusmart",
  "nexuspay",
  "vietcombank",
  "techcombank",
  "bidv",
  "agribank",
  "momo",
  "zalopay",
  "vnpay",
  "shopee",
  "lazada",
  "tiki",
  "gov",
  "chinhphu",
]);

const FORBIDDEN_BRAND_PREFIXES = [
  "nexus-",
  "nexusmart-",
  "nexuspay-",
  "vietcombank-",
  "techcombank-",
  "bidv-",
  "agribank-",
  "momo-",
  "zalopay-",
  "vnpay-",
  "shopee-",
  "lazada-",
  "tiki-",
  "gov-",
  "chinhphu-",
];

export function normalizeSubdomainInput(raw: string): string {
  return raw.trim().toLowerCase();
}

export function validateSubdomainClient(raw: string): string | null {
  const s = normalizeSubdomainInput(raw);
  if (!s) return "Vui lòng nhập tên miền phụ cho gian hàng.";
  if (s.length < 3 || s.length > 40) {
    return "Tên miền phụ cần dài từ 3 đến 40 ký tự.";
  }
  if (!SUBDOMAIN_RE.test(s)) {
    return "Chỉ dùng chữ thường (a-z), số (0-9) và dấu gạch nối (-), không mở đầu hoặc kết thúc bằng dấu gạch nối.";
  }
  if (RESERVED_SUBDOMAINS.has(s) || FORBIDDEN_BRAND_PREFIXES.some((p) => s.startsWith(p))) {
    return "Tên miền phụ này thuộc danh sách bảo vệ hệ thống hoặc nhãn hiệu. Vui lòng chọn tên khác.";
  }
  return null;
}

const HEX_COLOR_RE = /^#[0-9a-fA-F]{6}$/;

export function isValidHexColor(value: string): boolean {
  return HEX_COLOR_RE.test(value.trim());
}

export function normalizeHexColor(value: string): string {
  const trimmed = value.trim();
  return trimmed.startsWith("#") ? trimmed.toUpperCase() : `#${trimmed.toUpperCase()}`;
}

function srgbChannelToLinear(c: number): number {
  const s = c / 255;
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

export function relativeLuminance(hex: string): number | null {
  if (!isValidHexColor(hex)) return null;
  const clean = hex.trim().slice(1);
  const r = parseInt(clean.slice(0, 2), 16);
  const g = parseInt(clean.slice(2, 4), 16);
  const b = parseInt(clean.slice(4, 6), 16);
  return (
    0.2126 * srgbChannelToLinear(r) +
    0.7152 * srgbChannelToLinear(g) +
    0.0722 * srgbChannelToLinear(b)
  );
}

/** Real WCAG 2.1 contrast ratio between two #RRGGBB hex colors. Returns null if either is invalid. */
export function contrastRatio(hexA: string, hexB: string): number | null {
  const l1 = relativeLuminance(hexA);
  const l2 = relativeLuminance(hexB);
  if (l1 === null || l2 === null) return null;
  const lighter = Math.max(l1, l2);
  const darker = Math.min(l1, l2);
  return (lighter + 0.05) / (darker + 0.05);
}

export interface ContrastPairCheck {
  id: "bg-text" | "surface-text" | "accent-text";
  label: string;
  bg: string;
  fg: string;
  ratio: number | null;
  passes: boolean;
}

export function checkThemeContrast(theme: SiteTheme): ContrastPairCheck[] {
  const pairs: Array<{ id: ContrastPairCheck["id"]; label: string; bg: string; fg: string }> = [
    { id: "bg-text", label: "Nền trang / Chữ chính", bg: theme.bg_color, fg: theme.text_color },
    {
      id: "surface-text",
      label: "Bề mặt thẻ / Chữ chính",
      bg: theme.surface_color,
      fg: theme.text_color,
    },
    {
      id: "accent-text",
      label: "Màu nhấn / Chữ nút",
      bg: theme.accent_color,
      fg: theme.accent_text_color,
    },
  ];
  return pairs.map((p) => {
    const ratio = contrastRatio(p.bg, p.fg);
    return {
      ...p,
      ratio,
      passes: ratio !== null && ratio >= MIN_WCAG_CONTRAST,
    };
  });
}

export function formatContrastRatio(ratio: number | null): string {
  if (ratio === null) return "Chưa hợp lệ";
  return `${ratio.toFixed(2).replace(".", ",")}:1`;
}

/** SafeURL / SafeHref check matching backend Go rules. Empty is valid for optional fields. */
export function validateSafeUrlClient(raw: string | undefined): string | null {
  const trimmed = (raw ?? "").trim();
  if (!trimmed) return null;
  if (trimmed.length > 500) return "Đường dẫn tối đa 500 ký tự.";
  if (/[\x00-\x1f\x7f\\]/.test(trimmed)) {
    return "Đường dẫn chứa ký tự không được phép.";
  }
  const lower = trimmed.toLowerCase();
  if (
    lower.startsWith("javascript:") ||
    lower.startsWith("data:") ||
    lower.startsWith("vbscript:") ||
    lower.startsWith("http:")
  ) {
    return "Chỉ chấp nhận đường dẫn https:// hoặc đường dẫn nội bộ bắt đầu bằng /.";
  }
  if (trimmed.startsWith("/")) {
    if (trimmed.startsWith("//")) {
      return "Đường dẫn nội bộ chỉ bắt đầu bằng một dấu /.";
    }
    if (/\s/.test(trimmed)) {
      return "Đường dẫn nội bộ không chứa khoảng trắng.";
    }
    return null;
  }
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:" || !parsed.hostname) {
      return "Chỉ chấp nhận đường dẫn https:// hợp lệ hoặc đường dẫn nội bộ bắt đầu bằng /.";
    }
    return null;
  } catch {
    return "Đường dẫn chưa đúng định dạng (ví dụ /banners/campaign-everyday.jpg hoặc https://...).";
  }
}

export function createDefaultSiteConfig(shopName: string, shopDescription = ""): SiteConfig {
  const brand = shopName.trim() || "Gian hàng NexusMart";
  const tagline =
    shopDescription.trim().slice(0, 150) ||
    "Sản phẩm cơ bản may trực tiếp tại xưởng Việt Nam, minh bạch chất liệu và bảng size.";
  const preset = THEME_PRESETS[0].colors;

  return {
    theme: {
      ...preset,
      font_family: "work-sans",
      logo_url: "/banners/vietnam-workshop.jpg",
      banner_url: "/banners/campaign-everyday.jpg",
      brand_name: brand.slice(0, 80),
      tagline,
    },
    sections: [
      {
        id: "hero-main",
        type: "hero",
        visible: true,
        props: {
          eyebrow: "Cửa hàng chính thức",
          title: brand.slice(0, 120),
          subtitle: tagline,
          image_url: "/banners/campaign-everyday.jpg",
          cta_label: "Xem tất cả sản phẩm",
          cta_href: "/san-pham",
        },
      },
      {
        id: "featured-main",
        type: "featured_products",
        visible: true,
        props: {
          title: "Sản phẩm nổi bật",
          subtitle: "Những mẫu bán chạy và sẵn đủ size trực tiếp từ kho xưởng.",
          product_ids: [],
          limit: 8,
        },
      },
      {
        id: "vouchers-main",
        type: "voucher_strip",
        visible: true,
        props: {
          title: "Ưu đãi riêng của gian hàng",
          subtitle: "Lưu mã giảm giá trực tiếp từ gian hàng trước khi đặt mua.",
          limit: 4,
        },
      },
      {
        id: "policies-main",
        type: "policies",
        visible: true,
        props: {
          title: "Cam kết phục vụ từ gian hàng",
          items: [
            {
              heading: "Đổi trả linh hoạt 45 ngày",
              detail: "Hỗ trợ đổi size nhanh khi sản phẩm còn nguyên tem mác và chưa qua sử dụng.",
            },
            {
              heading: "Kiểm tra hàng khi nhận",
              detail:
                "Cho phép đồng kiểm đúng mẫu, đúng cỡ và đúng chất liệu vải trước khi thanh toán.",
            },
            {
              heading: "Giao hàng toàn quốc",
              detail: "Đóng gói chuẩn xưởng, bàn giao vận chuyển trong ngày làm việc.",
            },
          ],
        },
      },
    ],
  };
}

export function createSectionTemplate(type: SiteSectionType, existingCount: number): SiteSection {
  const id = `${type.replace(/_/g, "-")}-${Date.now().toString(36).slice(-4)}-${existingCount + 1}`;
  switch (type) {
    case "hero":
      return {
        id,
        type: "hero",
        visible: true,
        props: {
          eyebrow: "Bộ sưu tập mới",
          title: "Trang phục cơ bản chuẩn phom xưởng Việt",
          subtitle: "Chọn lọc chất liệu thoáng mát, bền màu sau nhiều lần giặt.",
          image_url: "/banners/campaign-everyday.jpg",
          cta_label: "Khám phá ngay",
          cta_href: "/san-pham",
        },
      };
    case "featured_products":
      return {
        id,
        type: "featured_products",
        visible: true,
        props: {
          title: "Sản phẩm tiêu biểu",
          subtitle: "Các thiết kế được người mua chọn nhiều nhất tuần này.",
          product_ids: [],
          limit: 8,
        },
      };
    case "collection":
      return {
        id,
        type: "collection",
        visible: true,
        props: {
          title: "Dòng sản phẩm chủ lực",
          subtitle: "Nhóm sản phẩm cùng chất liệu, dễ phối mặc mỗi ngày.",
          collection_slug: "co-ban-hang-ngay",
          category_slug: "",
          image_url: "/banners/fabric-closeup.jpg",
          limit: 4,
        },
      };
    case "text":
      return {
        id,
        type: "text",
        visible: true,
        props: {
          title: "Câu chuyện từ xưởng may",
          body: "Từng lô vải được kiểm định định lượng GSM và độ co rút trước khi lên rập, giúp người mặc luôn thoải mái và giữ phom ổn định qua thời gian.",
          align: "left",
        },
      };
    case "banner":
      return {
        id,
        type: "banner",
        visible: true,
        props: {
          title: "Ưu đãi mua theo bộ tiết kiệm hơn",
          subtitle:
            "Kết hợp áo thun, polo và quần short từ cùng một xưởng để tối ưu phí giao hàng.",
          image_url: "/banners/vietnam-workshop.jpg",
          cta_label: "Xem danh mục",
          cta_href: "/san-pham",
        },
      };
    case "voucher_strip":
      return {
        id,
        type: "voucher_strip",
        visible: true,
        props: {
          title: "Mã giảm giá của gian hàng",
          subtitle: "Áp dụng ngay khi thanh toán đơn hàng của gian hàng.",
          limit: 4,
        },
      };
    case "policies":
      return {
        id,
        type: "policies",
        visible: true,
        props: {
          title: "Chính sách gian hàng",
          items: [
            {
              heading: "Đổi size tận nơi",
              detail: "Hỗ trợ đổi cỡ nhanh chóng trong vòng 45 ngày kể từ khi nhận hàng.",
            },
            {
              heading: "Bảo hành đường may",
              detail: "Kiểm tra QC kỹ lưỡng từng sản phẩm trước khi đóng gói.",
            },
          ],
        },
      };
  }
}

export function getSiteStatusMeta(status: string | null | undefined): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
  adminTone: "positive" | "warning" | "negative" | "neutral";
} {
  switch ((status ?? "").toUpperCase()) {
    case "PUBLISHED":
      return { label: "Đang hoạt động", tone: "pos", adminTone: "positive" };
    case "DRAFT":
      return { label: "Bản nháp", tone: "warn", adminTone: "warning" };
    case "SUSPENDED":
      return { label: "Tạm khóa", tone: "neg", adminTone: "negative" };
    default:
      return { label: "Chưa thiết lập", tone: "neutral", adminTone: "neutral" };
  }
}

export function getSectionHeadingPreview(section: SiteSection): string {
  const title = section.props.title?.trim();
  if (title) return title;
  return SECTION_TYPE_META[section.type].label;
}

/** Suggest a valid subdomain from a Vietnamese shop name (for first-time setup). */
export function suggestSubdomainFromShopName(shopName: string): string {
  const base = shopName
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 36);
  if (base.length >= 3 && !validateSubdomainClient(base)) return base;
  return "cua-hang-rieng";
}

/** Clean payload before sending to strict DisallowUnknownFields backend validator. */
export function buildCleanSiteConfigPayload(config: SiteConfig): SiteConfig {
  return {
    theme: {
      bg_color: normalizeHexColor(config.theme.bg_color),
      surface_color: normalizeHexColor(config.theme.surface_color),
      text_color: normalizeHexColor(config.theme.text_color),
      accent_color: normalizeHexColor(config.theme.accent_color),
      accent_text_color: normalizeHexColor(config.theme.accent_text_color),
      font_family: config.theme.font_family,
      logo_url: (config.theme.logo_url ?? "").trim(),
      banner_url: (config.theme.banner_url ?? "").trim(),
      brand_name: config.theme.brand_name.trim(),
      tagline: (config.theme.tagline ?? "").trim(),
    },
    sections: config.sections.map((sec): SiteSection => {
      switch (sec.type) {
        case "hero":
          return {
            id: sec.id.trim(),
            type: "hero",
            visible: sec.visible,
            props: {
              eyebrow: (sec.props.eyebrow ?? "").trim(),
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              image_url: (sec.props.image_url ?? "").trim(),
              cta_label: (sec.props.cta_label ?? "").trim(),
              cta_href: (sec.props.cta_href ?? "").trim(),
            },
          };
        case "featured_products":
          return {
            id: sec.id.trim(),
            type: "featured_products",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              product_ids: (sec.props.product_ids ?? []).map((x) => x.trim()).filter(Boolean),
              limit: Math.max(0, Math.min(12, Math.trunc(sec.props.limit ?? 8))),
            },
          };
        case "collection":
          return {
            id: sec.id.trim(),
            type: "collection",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              collection_slug: (sec.props.collection_slug ?? "").trim(),
              category_slug: (sec.props.category_slug ?? "").trim(),
              image_url: (sec.props.image_url ?? "").trim(),
              limit: Math.max(0, Math.min(12, Math.trunc(sec.props.limit ?? 4))),
            },
          };
        case "text":
          return {
            id: sec.id.trim(),
            type: "text",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              body: sec.props.body.trim(),
              align:
                sec.props.align === "center" ? "center" : sec.props.align === "left" ? "left" : "",
            },
          };
        case "banner":
          return {
            id: sec.id.trim(),
            type: "banner",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              image_url: (sec.props.image_url ?? "").trim(),
              cta_label: (sec.props.cta_label ?? "").trim(),
              cta_href: (sec.props.cta_href ?? "").trim(),
            },
          };
        case "voucher_strip":
          return {
            id: sec.id.trim(),
            type: "voucher_strip",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              subtitle: (sec.props.subtitle ?? "").trim(),
              limit: Math.max(0, Math.min(6, Math.trunc(sec.props.limit ?? 4))),
            },
          };
        case "policies":
          return {
            id: sec.id.trim(),
            type: "policies",
            visible: sec.visible,
            props: {
              title: sec.props.title.trim(),
              items: sec.props.items.map((it) => ({
                heading: it.heading.trim(),
                detail: it.detail.trim(),
              })),
            },
          };
      }
    }),
  };
}

/** Validate the entire draft payload on the client before submitting to BFF. */
export function validateSiteDraftClient(subdomain: string, config: SiteConfig): string | null {
  const subErr = validateSubdomainClient(subdomain);
  if (subErr) return subErr;

  const { theme, sections } = config;
  if (!theme.brand_name.trim() || theme.brand_name.trim().length > 80) {
    return "Tên thương hiệu cần từ 1 đến 80 ký tự.";
  }
  if ((theme.tagline ?? "").length > 160) {
    return "Khẩu hiệu thương hiệu tối đa 160 ký tự.";
  }
  const colorFields: Array<[string, string]> = [
    ["Màu nền trang", theme.bg_color],
    ["Màu bề mặt thẻ", theme.surface_color],
    ["Màu chữ chính", theme.text_color],
    ["Màu nhấn", theme.accent_color],
    ["Màu chữ trên nút", theme.accent_text_color],
  ];
  for (const [label, hex] of colorFields) {
    if (!isValidHexColor(hex)) {
      return `${label} cần là mã màu 6 ký tự dạng #RRGGBB.`;
    }
  }
  const failedContrast = checkThemeContrast(theme).find((c) => !c.passes);
  if (failedContrast) {
    return `Độ tương phản "${failedContrast.label}" đang là ${formatContrastRatio(failedContrast.ratio)}, cần tối thiểu 4,5:1.`;
  }
  const logoErr = validateSafeUrlClient(theme.logo_url);
  if (logoErr) return `Logo thương hiệu: ${logoErr}`;
  const bannerErr = validateSafeUrlClient(theme.banner_url);
  if (bannerErr) return `Ảnh bìa thương hiệu: ${bannerErr}`;

  if (sections.length > MAX_SECTIONS) {
    return `Site riêng hỗ trợ tối đa ${MAX_SECTIONS} khối nội dung.`;
  }

  for (let i = 0; i < sections.length; i++) {
    const sec = sections[i];
    const label = `Khối ${i + 1} (${SECTION_TYPE_META[sec.type].label})`;
    if (!sec.id.trim() || sec.id.length > 64) {
      return `${label}: mã khối không hợp lệ.`;
    }
    if (!sec.props.title.trim() || sec.props.title.trim().length > 120) {
      return `${label}: tiêu đề cần từ 1 đến 120 ký tự.`;
    }
    switch (sec.type) {
      case "hero": {
        if ((sec.props.eyebrow ?? "").length > 60) return `${label}: nhãn phụ tối đa 60 ký tự.`;
        if ((sec.props.subtitle ?? "").length > 400) return `${label}: mô tả tối đa 400 ký tự.`;
        if ((sec.props.cta_label ?? "").length > 40) return `${label}: nhãn nút tối đa 40 ký tự.`;
        const imgErr = validateSafeUrlClient(sec.props.image_url);
        if (imgErr) return `${label} (ảnh): ${imgErr}`;
        const hrefErr = validateSafeUrlClient(sec.props.cta_href);
        if (hrefErr) return `${label} (liên kết nút): ${hrefErr}`;
        break;
      }
      case "featured_products": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        if ((sec.props.product_ids ?? []).length > 12) return `${label}: chọn tối đa 12 sản phẩm.`;
        const limit = sec.props.limit ?? 8;
        if (limit < 0 || limit > 12) {
          return `${label}: số sản phẩm hiển thị từ 0 đến 12.`;
        }
        break;
      }
      case "collection": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        if ((sec.props.collection_slug ?? "").length > 80)
          return `${label}: mã bộ sưu tập tối đa 80 ký tự.`;
        if ((sec.props.category_slug ?? "").length > 80)
          return `${label}: mã danh mục tối đa 80 ký tự.`;
        const imgErr = validateSafeUrlClient(sec.props.image_url);
        if (imgErr) return `${label} (ảnh): ${imgErr}`;
        const limit = sec.props.limit ?? 4;
        if (limit < 0 || limit > 12) {
          return `${label}: số sản phẩm hiển thị từ 0 đến 12.`;
        }
        break;
      }
      case "text": {
        if (!sec.props.body.trim() || sec.props.body.trim().length > 2000) {
          return `${label}: nội dung cần từ 1 đến 2000 ký tự.`;
        }
        break;
      }
      case "banner": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        if ((sec.props.cta_label ?? "").length > 40) return `${label}: nhãn nút tối đa 40 ký tự.`;
        const imgErr = validateSafeUrlClient(sec.props.image_url);
        if (imgErr) return `${label} (ảnh): ${imgErr}`;
        const hrefErr = validateSafeUrlClient(sec.props.cta_href);
        if (hrefErr) return `${label} (liên kết nút): ${hrefErr}`;
        break;
      }
      case "voucher_strip": {
        if ((sec.props.subtitle ?? "").length > 300) return `${label}: mô tả tối đa 300 ký tự.`;
        const limit = sec.props.limit ?? 4;
        if (limit < 0 || limit > 6) {
          return `${label}: số mã hiển thị từ 0 đến 6.`;
        }
        break;
      }
      case "policies": {
        if (sec.props.items.length < 1 || sec.props.items.length > 8) {
          return `${label}: cần từ 1 đến 8 mục chính sách.`;
        }
        for (let j = 0; j < sec.props.items.length; j++) {
          const item = sec.props.items[j];
          if (!item.heading.trim() || item.heading.trim().length > 80) {
            return `${label}, mục ${j + 1}: tiêu đề cần từ 1 đến 80 ký tự.`;
          }
          if (!item.detail.trim() || item.detail.trim().length > 400) {
            return `${label}, mục ${j + 1}: mô tả cần từ 1 đến 400 ký tự.`;
          }
        }
        break;
      }
    }
  }
  return null;
}
