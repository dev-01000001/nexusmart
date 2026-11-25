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
  primaryAlt: string;
  secondaryImg: string;
  secondaryAlt: string;
  headline: string;
  chips: readonly string[];
}

const SHOWCASE_MEDIA: Record<
  ShowcaseTemplateKey,
  {
    label: string;
    primaryImg: string;
    primaryAlt: string;
    secondaryImg: string;
    secondaryAlt: string;
    headline: string;
    chips: readonly string[];
  }
> = {
  "sport-active": {
    label: "Thể thao",
    primaryImg: "/banners/campaign-running.jpg",
    primaryAlt: "Cửa hàng thời trang thể thao vận động ngoài trời",
    secondaryImg: "/models/model-ao-thun-dryfit.jpg",
    secondaryAlt: "Mẫu áo thun Dry-Fit thoáng khí",
    headline: "Nhẹ bước chạy nắng sớm",
    chips: ["Dry-Fit 145gsm", "Co giãn 4 chiều"],
  },
  "luxury-maison": {
    label: "Maison",
    primaryImg: "/models/model-polo-det-kim-den.jpg",
    primaryAlt: "Mẫu áo polo dệt kim tông trầm tinh giản",
    secondaryImg: "/banners/fabric-closeup.jpg",
    secondaryAlt: "Cận cảnh mặt vải dệt tinh chọn",
    headline: "Tĩnh lặng của chất liệu",
    chips: ["Phiên bản giới hạn", "May đo lô nhỏ"],
  },
  "craft-atelier": {
    label: "Xưởng dệt",
    primaryImg: "/banners/vietnam-workshop.jpg",
    primaryAlt: "Không gian xưởng dệt may Việt Nam sáng tự nhiên",
    secondaryImg: "/models/model-ao-thun-trang.jpg",
    secondaryAlt: "Mẫu áo thun cotton trắng tinh giản",
    headline: "Từ xưởng dệt đến tủ đồ",
    chips: ["Cotton 220gsm", "Kiểm vải 3 bước"],
  },
  editorial: {
    label: "Tạp chí",
    primaryImg: "/banners/campaign-office.jpg",
    primaryAlt: "Bộ sưu tập công sở đương đại tối giản",
    secondaryImg: "/models/model-polo-kem.jpg",
    secondaryAlt: "Mẫu áo polo pique màu kem",
    headline: "Đường cắt nhẹ vai đô thị",
    chips: ["Bộ sưu tập Thu Đông", "Phối theo bộ"],
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
    primaryAlt: media.primaryAlt,
    secondaryImg: media.secondaryImg,
    secondaryAlt: media.secondaryAlt,
    headline: media.headline,
    chips: media.chips,
  };
});
