import { PLATFORM_TEMPLATES } from "@/features/site/templateCatalog";

export type ShowcaseTemplateKey = "sport-active" | "luxury-maison" | "craft-atelier" | "editorial";

export interface ShowcaseTemplateSpec {
  key: ShowcaseTemplateKey;
  label: string;
  defaultBrand: string;
  bg: string;
  surface: string;
  text: string;
  accent: string;
  accentText: string;
  primaryImg: string;
  headline: string;
  sampleProduct: string;
  samplePriceVnd: number;
  chips: readonly string[];
}

const SHOWCASE_MEDIA: Record<
  ShowcaseTemplateKey,
  {
    label: string;
    primaryImg: string;
    headline: string;
    sampleProduct: string;
    samplePriceVnd: number;
    chips: readonly string[];
  }
> = {
  "sport-active": {
    label: "Thể thao",
    primaryImg: "/banners/campaign-running.jpg",
    headline: "Nhẹ bước chạy nắng sớm",
    sampleProduct: "Áo Dry-Fit 145gsm",
    samplePriceVnd: 249000,
    chips: ["Dry-Fit 145gsm", "Co giãn 4 chiều"],
  },
  "luxury-maison": {
    label: "Maison",
    primaryImg: "/models/model-polo-det-kim-den.jpg",
    headline: "Tĩnh lặng của chất liệu",
    sampleProduct: "Áo Polo Dệt Kim Đen",
    samplePriceVnd: 389000,
    chips: ["Lô nhỏ", "Hoàn thiện kỹ"],
  },
  "craft-atelier": {
    label: "Xưởng dệt",
    primaryImg: "/models/model-ao-thun-xam-da.jpg",
    headline: "Từ xưởng dệt đến tủ đồ",
    sampleProduct: "Áo Thun Cotton 220gsm",
    samplePriceVnd: 229000,
    chips: ["Cotton 220gsm", "Đổi size 45 ngày"],
  },
  editorial: {
    label: "Tạp chí",
    primaryImg: "/models/model-ao-thun-xanh-than.jpg",
    headline: "Đường cắt nhẹ vai đô thị",
    sampleProduct: "Áo Thun Xanh Than",
    samplePriceVnd: 259000,
    chips: ["Thu Đông", "Phối theo bộ"],
  },
};

const SHOWCASE_ORDER: readonly ShowcaseTemplateKey[] = [
  "sport-active",
  "luxury-maison",
  "craft-atelier",
  "editorial",
];

export const SHOWCASE_TEMPLATES: readonly ShowcaseTemplateSpec[] = SHOWCASE_ORDER.map((key) => {
  const tpl = PLATFORM_TEMPLATES.find((item) => item.id === key);
  const theme = tpl?.config.theme;
  const media = SHOWCASE_MEDIA[key];
  return {
    key,
    label: media.label,
    defaultBrand: theme?.brand_name || media.label,
    bg: theme?.bg_color || "#12161B",
    surface: theme?.surface_color || "#1B222A",
    text: theme?.text_color || "#F3F6F9",
    accent: theme?.accent_color || "#D97706",
    accentText: theme?.accent_text_color || "#111418",
    primaryImg: media.primaryImg,
    headline: media.headline,
    sampleProduct: media.sampleProduct,
    samplePriceVnd: media.samplePriceVnd,
    chips: media.chips,
  };
});

/** Rich dark & accent colours harvested from PLATFORM_TEMPLATES for the S8 tool wall. */
export const TEMPLATE_WALL_SWATCHES: readonly string[] = (() => {
  const raw = new Set<string>();
  for (const tpl of PLATFORM_TEMPLATES) {
    const t = tpl.config.theme;
    for (const candidate of [t.accent_color, t.bg_color, t.surface_color, t.text_color]) {
      const hex = candidate.trim().toUpperCase();
      // Keep saturated accents and deep dark surfaces so white icons inside stay crisp
      if (hex && !hex.startsWith("#F") && !hex.startsWith("#E") && hex !== "#FFFFFF") {
        raw.add(hex);
      }
    }
  }
  return Array.from(raw);
})();
