export type CategorySlug =
  "ao-thun" | "ao-polo" | "quan-short" | "quan-jogger" | "do-lot" | "tat-phu-kien";

export type SizeCode = "S" | "M" | "L" | "XL" | "2XL";
export type GenderCode = "nam" | "nu" | "unisex";

export type MaterialCode =
  | "cotton-compact"
  | "cotton-heavy"
  | "polyester-dryfit"
  | "pique-cotton"
  | "viscose-knit"
  | "cotton-twill"
  | "polyamide-active"
  | "linen-cotton"
  | "french-terry"
  | "nylon-tech"
  | "modal"
  | "bamboo"
  | "nylon-ripstop";

export type FitCode = "slim" | "regular" | "relaxed" | "oversize" | "athletic";

export type OccasionCode = "hang-ngay" | "cong-so" | "the-thao" | "du-lich" | "mac-nha";

export type SportCode = "chay-bo" | "gym" | "cau-long" | "di-bo" | "dap-xe";

export type FabricFeatureCode =
  | "tham-hut"
  | "nhanh-kho"
  | "co-gian-4-chieu"
  | "it-nhan"
  | "khang-mui"
  | "truot-nuoc"
  | "thoang-khi"
  | "giu-form";

export type PatternCode = "tron" | "gan" | "det-lo" | "phoi-mau";

export type ProductBadge = "moi" | "ban-chay" | "pre-order" | "outlet";

export type ShopSizeChartKind = "top" | "bottom" | "jogger" | "underwear" | "sock";

export interface CategoryInfo {
  slug: CategorySlug;
  name: string;
  description: string;
  featuredImage: string;
  benefitSummary?: string;
  subCategories?: Array<{ slug: string; name: string; benefit: string }>;
}

export interface ProductColor {
  code: string;
  name: string;
  hex: string;
  image: string;
}

export interface ProductSku {
  skuCode: string;
  colorCode: string;
  size: SizeCode;
  stock: number; // integer >= 0
  price: number; // integer VND
}

export interface SizeMeasurement {
  size: SizeCode;
  heightCm: string;
  weightKg: string;
  chestOrWaistCm: string;
  lengthCm: string;
}

export interface ComboSlot {
  slotId: string;
  label: string;
  colorCode: string;
  colorName: string;
  availableSizes: SizeCode[];
  defaultSize: SizeCode;
}

export interface TierDiscount {
  minQty: number;
  discountPercent: number; // integer percent
  label: string;
}

export interface ProductGiftThreshold {
  minAmount: number; // integer VND
  giftName: string;
  giftImage: string;
  giftValue: number; // integer VND
}

export interface ModelInfo {
  gender: "nam" | "nu";
  heightCm: number;
  weightKg: number;
  measurements: string;
  wearingSize: SizeCode;
  note?: string;
}

export interface CareInstruction {
  code: "wash" | "bleach" | "dry" | "iron" | "softener";
  label: string;
  detail: string;
}

export interface ProductFaqItem {
  question: string;
  answer: string;
}

export interface Product {
  id: string;
  slug: string;
  name: string;
  shortName: string;
  categorySlug: CategorySlug;
  categoryName: string;
  shopId: string;
  gender: GenderCode;
  price: number; // integer VND
  originalPrice?: number; // integer VND when discounted
  image: string;
  onModelImage?: string;
  gallery: string[];
  colors: ProductColor[];
  sizes: SizeCode[];
  skus: ProductSku[];
  fabric: string;
  gsm: number;
  fit: string;
  origin: string;
  careGuide: string[];
  highlights: string[];
  description: string;
  sizeChart: SizeMeasurement[];
  isFlashSale?: boolean;
  isNew?: boolean;
  // Wave 2 rich metadata (optional for backward compatibility with Seller product creation)
  material?: MaterialCode;
  materialLabel?: string;
  fitType?: FitCode;
  occasion?: OccasionCode[];
  sport?: SportCode[];
  fabricFeatures?: FabricFeatureCode[];
  pattern?: PatternCode;
  badges?: ProductBadge[];
  isCombo?: boolean;
  isPack?: boolean;
  comboSlots?: ComboSlot[];
  tierDiscounts?: TierDiscount[];
  bundleIds?: string[];
  giftThreshold?: ProductGiftThreshold;
  returnEligible?: boolean;
  returnExclusionReason?: string;
  slaText?: string;
  maxPerOrder?: number;
  modelInfo?: ModelInfo[];
  careInstructions?: CareInstruction[];
  hasVideo?: boolean;
  videoUrl?: string;
  videoPoster?: string;
  faqs?: ProductFaqItem[];
  wishlistCount?: number;
  salesRank?: number;
  createdAt?: string;
  shopSizeChart?: ShopSizeChartKind;
  collectionSlugs?: string[];
}

export const GENDER_LABELS: Record<GenderCode, string> = {
  nam: "Nam",
  nu: "Nữ",
  unisex: "Unisex",
};

export const MATERIAL_LABELS: Record<MaterialCode, string> = {
  "cotton-compact": "Cotton Compact 220gsm",
  "cotton-heavy": "Cotton Heavyweight 260gsm",
  "polyester-dryfit": "Polyester Dry-Fit tái chế",
  "pique-cotton": "Cotton Pique mắt chim",
  "viscose-knit": "Viscose dệt kim mịn",
  "cotton-twill": "Cotton Twill co giãn",
  "polyamide-active": "Polyamide 4 chiều",
  "linen-cotton": "Linen pha Cotton",
  "french-terry": "Nỉ chân cua 320gsm",
  "nylon-tech": "Nylon Tech trượt nước",
  modal: "Sợi gỗ sồi Modal",
  bamboo: "Sợi tre Bamboo",
  "nylon-ripstop": "Nylon Ripstop chắn gió",
};

export const FIT_LABELS: Record<FitCode, string> = {
  slim: "Ôm gọn (Slim / Tapered)",
  regular: "Vừa vặn (Regular)",
  relaxed: "Thoải mái (Relaxed)",
  oversize: "Rộng vai (Oversize)",
  athletic: "Vận động (Athletic)",
};

export const OCCASION_LABELS: Record<OccasionCode, string> = {
  "hang-ngay": "Mặc hằng ngày",
  "cong-so": "Công sở gọn gàng",
  "the-thao": "Thể thao & vận động",
  "du-lich": "Du lịch & dã ngoại",
  "mac-nha": "Mặc nhà thoải mái",
};

export const SPORT_LABELS: Record<SportCode, string> = {
  "chay-bo": "Chạy bộ",
  gym: "Tập gym",
  "cau-long": "Cầu lông",
  "di-bo": "Đi bộ hằng ngày",
  "dap-xe": "Đạp xe",
};

export const FABRIC_FEATURE_LABELS: Record<FabricFeatureCode, string> = {
  "tham-hut": "Thấm hút mồ hôi",
  "nhanh-kho": "Nhanh khô",
  "co-gian-4-chieu": "Co giãn 4 chiều",
  "it-nhan": "Ít nhăn dễ ủi",
  "khang-mui": "Hạn chế mùi",
  "truot-nuoc": "Trượt nước nhẹ",
  "thoang-khi": "Thoáng khí",
  "giu-form": "Giữ form sau giặt",
};

export const PATTERN_LABELS: Record<PatternCode, string> = {
  tron: "Trơn tối giản",
  gan: "Dệt gân nổi",
  "det-lo": "Dệt lỗ thoáng",
  "phoi-mau": "Bộ nhiều tông",
};

export const BADGE_LABELS: Record<ProductBadge, string> = {
  moi: "Mới",
  "ban-chay": "Bán chạy",
  "pre-order": "Pre-order",
  outlet: "Outlet",
};

export const CATEGORIES: CategoryInfo[] = [
  {
    slug: "ao-thun",
    name: "Áo thun",
    description: "Cotton chải kỹ 220gsm, dry-fit thể thao và dáng rộng ít nhăn.",
    featuredImage: "/models/model-ao-thun-trang.jpg",
    benefitSummary:
      "Từ 145gsm siêu nhẹ tập luyện tới 260gsm đứng vai, cổ bo giữ dáng sau 40 lần giặt.",
    subCategories: [
      {
        slug: "cotton-compact",
        name: "Cotton Compact 220gsm",
        benefit: "Mặt vải mịn, ít xù lông, màu trắng không lộ nội y.",
      },
      {
        slug: "the-thao-dryfit",
        name: "Thể thao Dry-Fit",
        benefit: "Siêu nhẹ 145gsm, khô nhanh trong 45 phút phơi gió.",
      },
      {
        slug: "oversize-260gsm",
        name: "Oversize 260gsm",
        benefit: "Sợi se đôi đứng phom vai xuôi, mặc đẹp cho cả nam và nữ.",
      },
    ],
  },
  {
    slug: "ao-polo",
    name: "Áo polo",
    description: "Cổ dệt giữ form, vải pique thoáng khí và dệt kim mịn công sở.",
    featuredImage: "/models/model-polo-xanh-da.jpg",
    benefitSummary: "Cổ dệt rib 1:1 không quăn mép, thoáng khí cả ngày làm việc và gặp gỡ đối tác.",
    subCategories: [
      {
        slug: "polo-pique",
        name: "Polo Pique mắt chim",
        benefit: "Rãnh dệt thoáng khí, ít nhăn khi ngồi văn phòng nhiều giờ.",
      },
      {
        slug: "polo-det-kim",
        name: "Polo dệt kim mịn",
        benefit: "Sợi Viscose rủ nhẹ, thay thế sơ mi gọn gàng cho tiết trời mát.",
      },
    ],
  },
  {
    slug: "quan-short",
    name: "Quần short",
    description: "Short mặc hằng ngày, chạy bộ 5 inch và lanh pha mát nhẹ.",
    featuredImage: "/models/model-short-daily-be.jpg",
    benefitSummary: "Ba độ dài 5-7 inch chuẩn tỷ lệ chân, cạp giấu dây rút và túi sâu an toàn.",
    subCategories: [
      {
        slug: "short-daily",
        name: "Short Daily 7 inch",
        benefit: "Cotton twill co giãn nhẹ, túi sâu giữ điện thoại chắc chắn.",
      },
      {
        slug: "short-chay-bo",
        name: "Short chạy bộ 5 inch",
        benefit: "Vải polyamide 118gsm xẻ tà, túi khóa kéo sau lưng.",
      },
      {
        slug: "short-linen",
        name: "Short Linen pha Cotton",
        benefit: "Vân lanh mát nhẹ cho ngày nắng trên 35 độ và du lịch.",
      },
    ],
  },
  {
    slug: "quan-jogger",
    name: "Quần jogger",
    description: "Nỉ chân cua 320gsm và vải co giãn 4 chiều cho di chuyển hằng ngày.",
    featuredImage: "/models/model-jogger-terry-xam.jpg",
    benefitSummary: "Giữ phom gối không phồng, bo cổ chân gọn gàng và túi khóa kéo chìm.",
    subCategories: [
      {
        slug: "jogger-terry",
        name: "Jogger Nỉ chân cua",
        benefit: "320gsm dệt vòng thoáng khí, không bí nóng như nỉ cào bông.",
      },
      {
        slug: "jogger-tech",
        name: "Jogger vải kỹ thuật",
        benefit: "Trượt nước nhẹ, co giãn 4 chiều gọn gàng khi đi công tác.",
      },
    ],
  },
  {
    slug: "do-lot",
    name: "Đồ lót",
    description: "Combo 3 chiếc sợi modal và sợi tre kháng mùi, cạp phẳng không hằn.",
    featuredImage: "/models/model-mac-nha-modal.jpg",
    benefitSummary: "Đường may phẳng Flatlock không cộm, tem ép nhiệt và sợi gỗ/tre thoáng mát.",
    subCategories: [
      {
        slug: "boxer-modal",
        name: "Boxer sợi Modal",
        benefit: "Hút ẩm tốt hơn 50% so với cotton thường, không cuộn ống.",
      },
      {
        slug: "brief-tre",
        name: "Brief sợi tre",
        benefit: "Nhẹ mát thoáng đùi, hạn chế mùi suốt ngày vận động.",
      },
    ],
  },
  {
    slug: "tat-phu-kien",
    name: "Tất & phụ kiện",
    description: "Combo tất cotton gân, tất cổ thấp vận động và áo khoác gió gấp gọn.",
    featuredImage: "/models/model-tat-co-trung.jpg",
    benefitSummary:
      "Tất nối mũi liền mạch kèm chính sách bù lẻ chiếc thất lạc, áo khoác 210g gấp gọn.",
    subCategories: [
      {
        slug: "tat-co-trung",
        name: "Tất cổ trung công sở",
        benefit: "Đệm gót dày hơn 20%, giữ cổ chắc không tụt gót.",
      },
      {
        slug: "tat-the-thao",
        name: "Tất thể thao cổ thấp",
        benefit: "Lưới thoáng mu bàn chân, có gờ bảo vệ gót giày.",
      },
      {
        slug: "ao-khoac-gio",
        name: "Áo khoác gió gấp gọn",
        benefit: "Ripstop trượt nước PFC-free, nặng chỉ 210g mang gọn trong balo.",
      },
    ],
  },
];

const TOP_SIZE_CHART: SizeMeasurement[] = [
  { size: "S", heightCm: "160-166", weightKg: "52-59", chestOrWaistCm: "94", lengthCm: "67" },
  { size: "M", heightCm: "166-172", weightKg: "60-67", chestOrWaistCm: "100", lengthCm: "69" },
  { size: "L", heightCm: "172-177", weightKg: "68-75", chestOrWaistCm: "106", lengthCm: "71" },
  { size: "XL", heightCm: "177-182", weightKg: "76-83", chestOrWaistCm: "112", lengthCm: "73" },
  { size: "2XL", heightCm: "182-188", weightKg: "84-92", chestOrWaistCm: "118", lengthCm: "75" },
];

const BOTTOM_SIZE_CHART: SizeMeasurement[] = [
  { size: "S", heightCm: "160-166", weightKg: "52-59", chestOrWaistCm: "72-76", lengthCm: "44" },
  { size: "M", heightCm: "166-172", weightKg: "60-67", chestOrWaistCm: "77-81", lengthCm: "46" },
  { size: "L", heightCm: "172-177", weightKg: "68-75", chestOrWaistCm: "82-86", lengthCm: "48" },
  { size: "XL", heightCm: "177-182", weightKg: "76-83", chestOrWaistCm: "87-92", lengthCm: "50" },
  { size: "2XL", heightCm: "182-188", weightKg: "84-92", chestOrWaistCm: "93-98", lengthCm: "51" },
];

const JOGGER_SIZE_CHART: SizeMeasurement[] = [
  { size: "S", heightCm: "160-166", weightKg: "52-59", chestOrWaistCm: "72-76", lengthCm: "94" },
  { size: "M", heightCm: "166-172", weightKg: "60-67", chestOrWaistCm: "77-81", lengthCm: "96" },
  { size: "L", heightCm: "172-177", weightKg: "68-75", chestOrWaistCm: "82-86", lengthCm: "98" },
  { size: "XL", heightCm: "177-182", weightKg: "76-83", chestOrWaistCm: "87-92", lengthCm: "100" },
  { size: "2XL", heightCm: "182-188", weightKg: "84-92", chestOrWaistCm: "93-98", lengthCm: "102" },
];

const UNDERWEAR_SIZE_CHART: SizeMeasurement[] = [
  { size: "S", heightCm: "158-166", weightKg: "50-58", chestOrWaistCm: "70-75", lengthCm: "24" },
  { size: "M", heightCm: "166-172", weightKg: "59-67", chestOrWaistCm: "76-82", lengthCm: "25" },
  { size: "L", heightCm: "172-178", weightKg: "68-76", chestOrWaistCm: "83-89", lengthCm: "26" },
  { size: "XL", heightCm: "178-184", weightKg: "77-85", chestOrWaistCm: "90-96", lengthCm: "27" },
  { size: "2XL", heightCm: "184-190", weightKg: "86-95", chestOrWaistCm: "97-104", lengthCm: "28" },
];

const SOCK_SIZE_CHART: SizeMeasurement[] = [
  {
    size: "M",
    heightCm: "Cỡ giày 38-41",
    weightKg: "24-26 cm bàn chân",
    chestOrWaistCm: "Co giãn 4 chiều",
    lengthCm: "16",
  },
  {
    size: "L",
    heightCm: "Cỡ giày 42-45",
    weightKg: "26,5-29 cm bàn chân",
    chestOrWaistCm: "Co giãn 4 chiều",
    lengthCm: "18",
  },
];

const STANDARD_GIFT_THRESHOLD: ProductGiftThreshold = {
  minAmount: 499000,
  giftName: "Tặng 1 đôi Tất Cotton gân cổ trung (mẫu)",
  giftImage: "/products/tat-cotton-co-trung-5.png",
  giftValue: 39000,
};

const BASIC_TIER_DISCOUNTS: TierDiscount[] = [
  { minQty: 2, discountPercent: 10, label: "Mua 2 giảm thêm 10%" },
  { minQty: 3, discountPercent: 15, label: "Mua 3 giảm thêm 15%" },
];

const PACK_TIER_DISCOUNTS: TierDiscount[] = [
  { minQty: 2, discountPercent: 8, label: "Mua 2 pack giảm thêm 8%" },
  { minQty: 3, discountPercent: 12, label: "Mua 3 pack giảm thêm 12%" },
];

const COTTON_CARE: CareInstruction[] = [
  {
    code: "wash",
    label: "Giặt mát <= 30°C",
    detail: "Lộn trái sản phẩm, chọn chu trình giặt nhẹ.",
  },
  { code: "bleach", label: "Không tẩy clo", detail: "Tránh chất tẩy mạnh làm xơ bề mặt sợi bông." },
  {
    code: "dry",
    label: "Phơi râm thoáng gió",
    detail: "Tránh sấy nóng trên 60°C để giữ độ đứng cổ.",
  },
  { code: "iron", label: "Là hơi mức vừa", detail: "Ủi mặt trái dưới 150°C nếu cần phẳng nhanh." },
];

const SPORT_CARE: CareInstruction[] = [
  { code: "wash", label: "Giặt nước lạnh", detail: "Giặt sớm sau buổi tập để giữ sợi thoáng khí." },
  {
    code: "softener",
    label: "Không dùng nước xả",
    detail: "Nước xả vải làm giảm tốc độ thoát mồ hôi.",
  },
  {
    code: "bleach",
    label: "Không dùng thuốc tẩy",
    detail: "Bảo vệ cấu trúc elastane co giãn 4 chiều.",
  },
  {
    code: "dry",
    label: "Phơi gió tự khô",
    detail: "Vải khô sau 45 phút, không sấy nhiệt hay là trực tiếp.",
  },
];

const KNIT_CARE: CareInstruction[] = [
  {
    code: "wash",
    label: "Cho vào túi giặt lưới",
    detail: "Giặt chế độ len/mỏng với nước mát dưới 30°C.",
  },
  {
    code: "bleach",
    label: "Không vắt xoắn mạnh",
    detail: "Ép nhẹ nước để sợi dệt kim không giãn dài.",
  },
  { code: "dry", label: "Phơi phẳng trên lưới", detail: "Giữ đúng chiều dài thân áo và bờ vai." },
  {
    code: "iron",
    label: "Hấp hơi nhiệt thấp",
    detail: "Chỉ dùng bàn là hơi nước cách mặt vải 2 cm.",
  },
];

function buildSkus(
  prefix: string,
  colors: ProductColor[],
  sizes: SizeCode[],
  price: number,
  stockMatrix: Record<string, Record<string, number>>
): ProductSku[] {
  const skus: ProductSku[] = [];
  for (const color of colors) {
    for (const size of sizes) {
      const stock = stockMatrix[color.code]?.[size] ?? 12;
      skus.push({
        skuCode: `${prefix}-${color.code.toUpperCase()}-${size}`,
        colorCode: color.code,
        size,
        stock,
        price,
      });
    }
  }
  return skus;
}

const ALL_SIZES: SizeCode[] = ["S", "M", "L", "XL", "2XL"];

export const PRODUCTS: Product[] = [
  // 1. Áo thun cotton trắng
  {
    id: "prod-at-trang",
    slug: "ao-thun-cotton-compact-trang",
    name: "Áo thun Cotton Compact 220gsm Trắng",
    shortName: "Áo thun Cotton Trắng",
    categorySlug: "ao-thun",
    categoryName: "Áo thun",
    shopId: "shop-bach-dang",
    gender: "unisex",
    price: 179000,
    originalPrice: 219000,
    image: "/products/ao-thun-cotton-trang.png",
    onModelImage: "/models/model-ao-thun-trang.jpg",
    gallery: [
      "/products/ao-thun-cotton-trang.png",
      "/models/model-ao-thun-trang.jpg",
      "/products/ao-thun-cotton-xam-da.png",
      "/products/ao-thun-cotton-xanh-than.png",
      "/atmosphere/cotton-jersey-drape.webp",
    ],
    colors: [
      {
        code: "trang",
        name: "Trắng tinh",
        hex: "#f4f6f7",
        image: "/products/ao-thun-cotton-trang.png",
      },
      {
        code: "xamda",
        name: "Xám đá",
        hex: "#9aa5ac",
        image: "/products/ao-thun-cotton-xam-da.png",
      },
      {
        code: "xanhthan",
        name: "Xanh than",
        hex: "#283947",
        image: "/products/ao-thun-cotton-xanh-than.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "BD-AT01",
      [
        {
          code: "trang",
          name: "Trắng tinh",
          hex: "#f4f6f7",
          image: "/products/ao-thun-cotton-trang.png",
        },
        {
          code: "xamda",
          name: "Xám đá",
          hex: "#9aa5ac",
          image: "/products/ao-thun-cotton-xam-da.png",
        },
        {
          code: "xanhthan",
          name: "Xanh than",
          hex: "#283947",
          image: "/products/ao-thun-cotton-xanh-than.png",
        },
      ],
      ALL_SIZES,
      179000,
      {
        trang: { S: 18, M: 34, L: 26, XL: 14, "2XL": 3 },
        xamda: { S: 12, M: 22, L: 19, XL: 9, "2XL": 4 },
        xanhthan: { S: 10, M: 25, L: 15, XL: 7, "2XL": 0 },
      }
    ),
    fabric: "95% Cotton chải kỹ Compact, 5% Spandex",
    gsm: 220,
    fit: "Regular fit gọn vai",
    origin: "Hải Phòng, Việt Nam",
    careGuide: [
      "Giặt máy chế độ nhẹ dưới 30 độ C, lộn trái áo trước khi giặt.",
      "Không dùng chất tẩy có clo để giữ sợi bông mềm và bền màu.",
      "Phơi nơi thoáng mát, tránh sấy nhiệt cao trên 60 độ C.",
    ],
    highlights: [
      "Sợi cotton chải kỹ hạn chế xù lông sau 40 lần giặt.",
      "Bo cổ bản 2,2 cm pha spandex giữ cổ áo không bai giãn.",
      "Định lượng 220gsm đủ dày để không lộ nội y với màu trắng.",
    ],
    description:
      "Mẫu áo thun cơ bản được Xưởng Dệt Bạch Đằng hoàn thiện định lượng 220gsm, cân bằng giữa độ đứng dáng và sự thoáng mát cho thời tiết Việt Nam.",
    sizeChart: TOP_SIZE_CHART,
    isFlashSale: true,
    material: "cotton-compact",
    materialLabel: MATERIAL_LABELS["cotton-compact"],
    fitType: "regular",
    occasion: ["hang-ngay", "cong-so", "du-lich"],
    sport: ["di-bo"],
    fabricFeatures: ["tham-hut", "thoang-khi", "giu-form", "it-nhan"],
    pattern: "tron",
    badges: ["ban-chay"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-at-xamda", "prod-dolot-boxer", "prod-short-daily"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao 1-2 ngày · Kho Hải Phòng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 176,
        weightKg: 71,
        measurements: "Ngực 96 · Eo 80 · Mông 96 cm",
        wearingSize: "L",
        note: "Mặc vừa gọn vai, dài ngang hông",
      },
      {
        gender: "nu",
        heightCm: 163,
        weightKg: 52,
        measurements: "Ngực 82 · Eo 64 · Mông 90 cm",
        wearingSize: "S",
        note: "Mặc thoải mái vừa vai, dễ sơ vin",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: true,
    videoUrl: "/atmosphere/jacquard-loom-loop.webm",
    videoPoster: "/atmosphere/jacquard-loom-poster.jpg",
    faqs: [
      {
        question: "Áo trắng 220gsm có bị mỏng lộ bên trong không?",
        answer:
          "Vải dệt Compact 220gsm dày hơn khoảng 25% so với áo thun 175gsm thông thường, giữ độ kín đáo tốt ngay cả dưới ánh nắng mạnh.",
      },
      {
        question: "Giặt máy nhiều lần cổ áo có bị giãn không?",
        answer:
          "Bo cổ bản 2,2 cm pha 5% spandex và may dây xương cá sau gáy giúp ổn định vòng cổ sau hơn 40 chu kỳ giặt.",
      },
    ],
    wishlistCount: 318,
    salesRank: 1,
    createdAt: "2026-09-12T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["co-ban-hang-ngay", "soi-xanh-nexus"],
  },

  // 2. Áo thun cotton xám đá
  {
    id: "prod-at-xamda",
    slug: "ao-thun-cotton-compact-xam-da",
    name: "Áo thun Cotton Compact 220gsm Xám đá",
    shortName: "Áo thun Cotton Xám đá",
    categorySlug: "ao-thun",
    categoryName: "Áo thun",
    shopId: "shop-bach-dang",
    gender: "nu",
    price: 189000,
    image: "/products/ao-thun-cotton-xam-da.png",
    onModelImage: "/models/model-ao-thun-xam-da.jpg",
    gallery: [
      "/products/ao-thun-cotton-xam-da.png",
      "/models/model-ao-thun-xam-da.jpg",
      "/products/ao-thun-cotton-trang.png",
      "/products/ao-thun-cotton-xanh-than.png",
      "/atmosphere/cotton-jersey-weave.webp",
    ],
    colors: [
      {
        code: "xamda",
        name: "Xám đá",
        hex: "#9aa5ac",
        image: "/products/ao-thun-cotton-xam-da.png",
      },
      {
        code: "trang",
        name: "Trắng tinh",
        hex: "#f4f6f7",
        image: "/products/ao-thun-cotton-trang.png",
      },
      {
        code: "xanhthan",
        name: "Xanh than",
        hex: "#283947",
        image: "/products/ao-thun-cotton-xanh-than.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "BD-AT02",
      [
        {
          code: "xamda",
          name: "Xám đá",
          hex: "#9aa5ac",
          image: "/products/ao-thun-cotton-xam-da.png",
        },
        {
          code: "trang",
          name: "Trắng tinh",
          hex: "#f4f6f7",
          image: "/products/ao-thun-cotton-trang.png",
        },
        {
          code: "xanhthan",
          name: "Xanh than",
          hex: "#283947",
          image: "/products/ao-thun-cotton-xanh-than.png",
        },
      ],
      ALL_SIZES,
      189000,
      {
        xamda: { S: 15, M: 28, L: 24, XL: 11, "2XL": 6 },
        trang: { S: 14, M: 20, L: 18, XL: 8, "2XL": 5 },
        xanhthan: { S: 9, M: 17, L: 14, XL: 6, "2XL": 2 },
      }
    ),
    fabric: "95% Cotton chải kỹ Compact, 5% Spandex",
    gsm: 220,
    fit: "Regular fit",
    origin: "Hải Phòng, Việt Nam",
    careGuide: [
      "Giặt cùng quần áo sáng hoặc trung tính dưới 30 độ C.",
      "Là hơi ở nhiệt độ trung bình, không là trực tiếp quá lâu trên cổ áo.",
    ],
    highlights: [
      "Tông xám đá trung tính ít lộ vết mồ hôi và dễ phối quần short, jogger.",
      "Đường may vai gia cố dây xương cá chống dão vai khi treo móc.",
    ],
    description:
      "Phiên bản màu xám đá lạnh nhuộm hoạt tính bền màu, phù hợp mặc đi làm thường nhật hoặc làm lớp áo lót dưới áo khoác gió.",
    sizeChart: TOP_SIZE_CHART,
    material: "cotton-compact",
    materialLabel: MATERIAL_LABELS["cotton-compact"],
    fitType: "regular",
    occasion: ["hang-ngay", "cong-so", "mac-nha"],
    sport: ["di-bo"],
    fabricFeatures: ["tham-hut", "thoang-khi", "giu-form"],
    pattern: "tron",
    badges: ["ban-chay"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-at-trang", "prod-short-linen", "prod-aokhoac-gio"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao 1-2 ngày · Kho Hải Phòng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nu",
        heightCm: 164,
        weightKg: 53,
        measurements: "Ngực 83 · Eo 65 · Mông 91 cm",
        wearingSize: "S",
        note: "Dáng vừa vặn tôn vai, chất mềm thoáng",
      },
      {
        gender: "nam",
        heightCm: 172,
        weightKg: 66,
        measurements: "Ngực 94 · Eo 78 · Mông 93 cm",
        wearingSize: "M",
        note: "Phom gọn gàng mặc hằng ngày",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Màu xám đá có bị phai màu khi giặt chung đồ trắng không?",
        answer:
          "Vải được nhuộm hoạt tính và cố định màu tại xưởng nên rất bền sắc, vẫn khuyến nghị lộn trái khi giặt máy.",
      },
    ],
    wishlistCount: 214,
    salesRank: 5,
    createdAt: "2026-09-20T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["co-ban-hang-ngay", "soi-xanh-nexus"],
  },

  // 3. Áo thun cotton xanh than
  {
    id: "prod-at-xanhthan",
    slug: "ao-thun-cotton-compact-xanh-than",
    name: "Áo thun Cotton Compact 220gsm Xanh than",
    shortName: "Áo thun Xanh than",
    categorySlug: "ao-thun",
    categoryName: "Áo thun",
    shopId: "shop-bach-dang",
    gender: "nam",
    price: 189000,
    originalPrice: 229000,
    image: "/products/ao-thun-cotton-xanh-than.png",
    onModelImage: "/models/model-ao-thun-xanh-than.jpg",
    gallery: [
      "/products/ao-thun-cotton-xanh-than.png",
      "/models/model-ao-thun-xanh-than.jpg",
      "/products/ao-thun-cotton-xam-da.png",
      "/products/ao-thun-cotton-trang.png",
      "/atmosphere/cotton-twill-slate.jpg",
    ],
    colors: [
      {
        code: "xanhthan",
        name: "Xanh than",
        hex: "#283947",
        image: "/products/ao-thun-cotton-xanh-than.png",
      },
      {
        code: "xamda",
        name: "Xám đá",
        hex: "#9aa5ac",
        image: "/products/ao-thun-cotton-xam-da.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "BD-AT03",
      [
        {
          code: "xanhthan",
          name: "Xanh than",
          hex: "#283947",
          image: "/products/ao-thun-cotton-xanh-than.png",
        },
        {
          code: "xamda",
          name: "Xám đá",
          hex: "#9aa5ac",
          image: "/products/ao-thun-cotton-xam-da.png",
        },
      ],
      ALL_SIZES,
      189000,
      {
        xanhthan: { S: 11, M: 21, L: 29, XL: 16, "2XL": 5 },
        xamda: { S: 8, M: 16, L: 14, XL: 7, "2XL": 4 },
      }
    ),
    fabric: "95% Cotton chải kỹ Compact, 5% Spandex",
    gsm: 220,
    fit: "Regular fit",
    origin: "Hải Phòng, Việt Nam",
    careGuide: [
      "Giặt riêng ở lần đầu để ổn định sắc nhuộm đậm.",
      "Phơi mặt trái nơi râm mát tránh bạc màu do nắng gắt.",
    ],
    highlights: [
      "Màu xanh than sâu tạo cảm giác gọn người, phù hợp nhiều vóc dáng.",
      "Xử lý enzyme làm mượt bề mặt vải, chạm mát ngay khi mặc.",
    ],
    description:
      "Áo thun cổ tròn màu xanh than sẫm giữ độ sắc nét sau nhiều chu kỳ giặt, phối gọn với quần short be hoặc jogger xám.",
    sizeChart: TOP_SIZE_CHART,
    material: "cotton-compact",
    materialLabel: MATERIAL_LABELS["cotton-compact"],
    fitType: "regular",
    occasion: ["hang-ngay", "cong-so"],
    sport: ["di-bo"],
    fabricFeatures: ["tham-hut", "giu-form", "thoang-khi"],
    pattern: "tron",
    badges: ["outlet"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-short-daily", "prod-jogger-terry", "prod-dolot-boxer"],
    returnEligible: true,
    slaText: "Giao 1-2 ngày · Kho Hải Phòng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 178,
        weightKg: 74,
        measurements: "Ngực 98 · Eo 82 · Mông 97 cm",
        wearingSize: "L",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Áo màu tối có dễ bám bụi vải không?",
        answer:
          "Vải cotton chải kỹ đã cắt lông thừa bằng xử lý enzyme nên bề mặt mịn và ít bám xơ hơn cotton thường.",
      },
    ],
    wishlistCount: 156,
    salesRank: 9,
    createdAt: "2026-08-18T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["co-ban-hang-ngay", "san-outlet"],
  },

  // 4. Áo thun dry-fit cổ vịt
  {
    id: "prod-at-dryfit",
    slug: "ao-thun-the-thao-dryfit-xanh-co-vit",
    name: "Áo thun Thể thao Dry-Fit Xanh cổ vịt",
    shortName: "Áo thun Dry-Fit Cổ vịt",
    categorySlug: "ao-thun",
    categoryName: "Áo thun",
    shopId: "shop-sai-gon",
    gender: "nam",
    price: 159000,
    originalPrice: 199000,
    image: "/products/ao-thun-dryfit-co-vit.png",
    onModelImage: "/models/model-ao-thun-dryfit.jpg",
    gallery: [
      "/products/ao-thun-dryfit-co-vit.png",
      "/models/model-ao-thun-dryfit.jpg",
      "/products/ao-thun-oversize-than-chi.png",
      "/products/quan-short-run-xam-dam.png",
    ],
    colors: [
      {
        code: "covit",
        name: "Xanh cổ vịt",
        hex: "#0d7480",
        image: "/products/ao-thun-dryfit-co-vit.png",
      },
      {
        code: "thanchi",
        name: "Than chì",
        hex: "#353d42",
        image: "/products/ao-thun-oversize-than-chi.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "SG-DF04",
      [
        {
          code: "covit",
          name: "Xanh cổ vịt",
          hex: "#0d7480",
          image: "/products/ao-thun-dryfit-co-vit.png",
        },
        {
          code: "thanchi",
          name: "Than chì",
          hex: "#353d42",
          image: "/products/ao-thun-oversize-than-chi.png",
        },
      ],
      ALL_SIZES,
      159000,
      {
        covit: { S: 16, M: 30, L: 28, XL: 18, "2XL": 7 },
        thanchi: { S: 0, M: 19, L: 20, XL: 12, "2XL": 4 },
      }
    ),
    fabric: "88% Polyester tái chế Micro-mesh, 12% Elastane",
    gsm: 145,
    fit: "Athletic fit ôm vừa",
    origin: "TP. Hồ Chí Minh, Việt Nam",
    careGuide: [
      "Giặt nước lạnh ngay sau buổi tập, không dùng nước xả làm bít sợi thoáng khí.",
      "Vải tự khô sau khoảng 45 phút phơi gió.",
    ],
    highlights: [
      "Cấu trúc dệt lỗ thoáng siêu nhỏ tản nhiệt nhanh khi chạy bộ và tập gym.",
      "Đường may chập phẳng ở nách hạn chế cọ xát da khi vận động dài.",
    ],
    description:
      "Áo thun vận động siêu nhẹ 145gsm từ Kho Basic Sài Gòn, thấm hút và khô nhanh cho các buổi chạy bộ sáng hoặc tập cường độ cao.",
    sizeChart: TOP_SIZE_CHART,
    isFlashSale: true,
    isNew: true,
    material: "polyester-dryfit",
    materialLabel: MATERIAL_LABELS["polyester-dryfit"],
    fitType: "athletic",
    occasion: ["the-thao", "du-lich"],
    sport: ["chay-bo", "gym", "cau-long", "dap-xe"],
    fabricFeatures: ["nhanh-kho", "thoang-khi", "co-gian-4-chieu", "khang-mui"],
    pattern: "det-lo",
    badges: ["moi", "ban-chay"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-short-run", "prod-tat-cothap", "prod-aokhoac-gio"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho TP.HCM",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 175,
        weightKg: 69,
        measurements: "Ngực 96 · Eo 79 · Mông 95 cm",
        wearingSize: "L",
        note: "Ôm gọn cơ thể khi chạy bộ",
      },
    ],
    careInstructions: SPORT_CARE,
    hasVideo: true,
    videoUrl: "/atmosphere/jacquard-loom-loop.webm",
    videoPoster: "/atmosphere/jacquard-loom-poster.jpg",
    faqs: [
      {
        question: "Áo chạy bộ 145gsm có bị dính lưng khi ra nhiều mồ hôi không?",
        answer:
          "Bề mặt dệt micro-mesh tạo khoảng hở nhỏ giúp thoát hơi ẩm nhanh, giảm hẳn cảm giác bết dính khi chạy ngoài trời.",
      },
    ],
    wishlistCount: 274,
    salesRank: 2,
    createdAt: "2026-11-08T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["chay-bo-tap-luyen", "soi-xanh-nexus"],
  },

  // 5. Áo thun oversize than chì
  {
    id: "prod-at-oversize",
    slug: "ao-thun-oversize-heavyweight-than-chi",
    name: "Áo thun Oversize 260gsm Than chì",
    shortName: "Áo thun Oversize Than chì",
    categorySlug: "ao-thun",
    categoryName: "Áo thun",
    shopId: "shop-sai-gon",
    gender: "unisex",
    price: 239000,
    image: "/products/ao-thun-oversize-than-chi.png",
    onModelImage: "/models/model-ao-thun-oversize.jpg",
    gallery: [
      "/products/ao-thun-oversize-than-chi.png",
      "/models/model-ao-thun-oversize.jpg",
      "/products/ao-thun-cotton-trang.png",
      "/atmosphere/cotton-jersey-weave.webp",
    ],
    colors: [
      {
        code: "thanchi",
        name: "Than chì",
        hex: "#353d42",
        image: "/products/ao-thun-oversize-than-chi.png",
      },
      {
        code: "trang",
        name: "Trắng ngà lạnh",
        hex: "#f4f6f7",
        image: "/products/ao-thun-cotton-trang.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "SG-OS05",
      [
        {
          code: "thanchi",
          name: "Than chì",
          hex: "#353d42",
          image: "/products/ao-thun-oversize-than-chi.png",
        },
        {
          code: "trang",
          name: "Trắng ngà lạnh",
          hex: "#f4f6f7",
          image: "/products/ao-thun-cotton-trang.png",
        },
      ],
      ALL_SIZES,
      239000,
      {
        thanchi: { S: 14, M: 24, L: 21, XL: 13, "2XL": 5 },
        trang: { S: 9, M: 15, L: 12, XL: 6, "2XL": 0 },
      }
    ),
    fabric: "100% Cotton sợi se đôi Heavyweight",
    gsm: 260,
    fit: "Oversize vai xuôi thoải mái",
    origin: "TP. Hồ Chí Minh, Việt Nam",
    careGuide: [
      "Giặt máy vòng quay thấp, phơi ngang thân áo để giữ dáng vai rộng.",
      "Không sấy khô nhiệt cao để giữ độ dày và cấu trúc sợi bông.",
    ],
    highlights: [
      "Vải 260gsm đứng phom rõ rệt nhưng mặt trong vẫn thoáng da.",
      "Dáng unisex cho cả nam và nữ mặc rộng thoải mái.",
    ],
    description:
      "Chiếc áo thun phom rộng dày dặn dành cho người thích độ rủ chắc tay, cổ tròn cao 2,5 cm giúp tổng thể gọn gàng không luộm thuộm.",
    sizeChart: TOP_SIZE_CHART,
    isNew: true,
    material: "cotton-heavy",
    materialLabel: MATERIAL_LABELS["cotton-heavy"],
    fitType: "oversize",
    occasion: ["hang-ngay", "du-lich"],
    sport: ["di-bo"],
    fabricFeatures: ["giu-form", "tham-hut", "it-nhan"],
    pattern: "tron",
    badges: ["moi"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-short-daily", "prod-jogger-terry"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho TP.HCM",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 177,
        weightKg: 72,
        measurements: "Ngực 96 · Eo 81 · Mông 96 cm",
        wearingSize: "L",
        note: "Vai xuôi rộng thoải mái đúng chuẩn oversize",
      },
      {
        gender: "nu",
        heightCm: 162,
        weightKg: 50,
        measurements: "Ngực 81 · Eo 63 · Mông 89 cm",
        wearingSize: "S",
        note: "Mặc rộng giấu quần nhẹ hoặc phối quần short",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Vải 260gsm mặc mùa hè miền Nam có nóng không?",
        answer:
          "Áo dệt 100% cotton sợi se đôi thoáng khí, phom rộng giúp gió lưu thông tốt quanh thân áo nên vẫn dễ chịu.",
      },
    ],
    wishlistCount: 192,
    salesRank: 7,
    createdAt: "2026-11-12T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["co-ban-hang-ngay"],
  },

  // 6. Áo polo pique kem
  {
    id: "prod-polo-kem",
    slug: "ao-polo-pique-cotton-kem-nhat",
    name: "Áo Polo Pique Cotton Kem nhạt",
    shortName: "Áo Polo Pique Kem",
    categorySlug: "ao-polo",
    categoryName: "Áo polo",
    shopId: "shop-da-nang",
    gender: "nam",
    price: 289000,
    originalPrice: 339000,
    image: "/products/ao-polo-pique-kem.png",
    onModelImage: "/models/model-polo-kem.jpg",
    gallery: [
      "/products/ao-polo-pique-kem.png",
      "/models/model-polo-kem.jpg",
      "/products/ao-polo-pique-xanh-da.png",
      "/products/ao-polo-det-kim-den.png",
      "/atmosphere/cotton-jersey-drape.webp",
    ],
    colors: [
      { code: "kem", name: "Kem nhạt", hex: "#e5e3dc", image: "/products/ao-polo-pique-kem.png" },
      {
        code: "xanhda",
        name: "Xanh đá",
        hex: "#627d8b",
        image: "/products/ao-polo-pique-xanh-da.png",
      },
      { code: "den", name: "Đen than", hex: "#20282e", image: "/products/ao-polo-det-kim-den.png" },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "DN-PL06",
      [
        { code: "kem", name: "Kem nhạt", hex: "#e5e3dc", image: "/products/ao-polo-pique-kem.png" },
        {
          code: "xanhda",
          name: "Xanh đá",
          hex: "#627d8b",
          image: "/products/ao-polo-pique-xanh-da.png",
        },
        {
          code: "den",
          name: "Đen than",
          hex: "#20282e",
          image: "/products/ao-polo-det-kim-den.png",
        },
      ],
      ALL_SIZES,
      289000,
      {
        kem: { S: 9, M: 19, L: 23, XL: 11, "2XL": 4 },
        xanhda: { S: 8, M: 16, L: 18, XL: 9, "2XL": 3 },
        den: { S: 7, M: 14, L: 12, XL: 8, "2XL": 2 },
      }
    ),
    fabric: "62% Cotton chải kỹ, 35% Polyester thoáng khí, 3% Spandex",
    gsm: 215,
    fit: "Regular fit công sở",
    origin: "Đà Nẵng, Việt Nam",
    careGuide: [
      "Cài cúc áo trước khi giặt máy để giữ nẹp cổ thẳng.",
      "Phơi trên móc vai bản rộng, tránh vắt xoắn mạnh.",
    ],
    highlights: [
      "Dệt mắt chim pique tạo rãnh lưu thông khí trên bề mặt da.",
      "Cổ dệt rib 1:1 chống quăn mép sau nhiều lần giặt.",
    ],
    description:
      "Áo polo công sở lịch sự với hai nút đồng màu tối giản, màu kem nhạt sáng da dễ phối quần tây hoặc quần short linen.",
    sizeChart: TOP_SIZE_CHART,
    isFlashSale: true,
    material: "pique-cotton",
    materialLabel: MATERIAL_LABELS["pique-cotton"],
    fitType: "regular",
    occasion: ["cong-so", "hang-ngay", "du-lich"],
    sport: ["di-bo"],
    fabricFeatures: ["thoang-khi", "it-nhan", "giu-form", "tham-hut"],
    pattern: "det-lo",
    badges: ["ban-chay"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-short-linen", "prod-polo-xanhda", "prod-dolot-brief"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 3h nội thành · Kho Đà Nẵng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 175,
        weightKg: 70,
        measurements: "Ngực 96 · Eo 80 · Mông 95 cm",
        wearingSize: "L",
        note: "Vừa vai công sở, nẹp cổ đứng phẳng",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: true,
    videoUrl: "/atmosphere/jacquard-loom-loop.webm",
    videoPoster: "/atmosphere/jacquard-loom-poster.jpg",
    faqs: [
      {
        question: "Nẹp cổ áo polo pique có cần ủi mỗi sáng không?",
        answer:
          "Vải pha 35% sợi thoáng khí giảm nhăn tốt; chỉ cần giũ phẳng và cài nút trên cùng khi phơi là cổ áo giữ nếp gọn.",
      },
    ],
    wishlistCount: 249,
    salesRank: 3,
    createdAt: "2026-10-02T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["cong-so-gon-gang", "co-ban-hang-ngay"],
  },

  // 7. Áo polo pique xanh đá
  {
    id: "prod-polo-xanhda",
    slug: "ao-polo-pique-cotton-xanh-da",
    name: "Áo Polo Pique Cotton Xanh đá",
    shortName: "Áo Polo Xanh đá",
    categorySlug: "ao-polo",
    categoryName: "Áo polo",
    shopId: "shop-da-nang",
    gender: "nam",
    price: 299000,
    image: "/products/ao-polo-pique-xanh-da.png",
    onModelImage: "/models/model-polo-xanh-da.jpg",
    gallery: [
      "/products/ao-polo-pique-xanh-da.png",
      "/models/model-polo-xanh-da.jpg",
      "/products/ao-polo-pique-kem.png",
      "/atmosphere/cotton-twill-slate.jpg",
    ],
    colors: [
      {
        code: "xanhda",
        name: "Xanh đá",
        hex: "#627d8b",
        image: "/products/ao-polo-pique-xanh-da.png",
      },
      { code: "kem", name: "Kem nhạt", hex: "#e5e3dc", image: "/products/ao-polo-pique-kem.png" },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "DN-PL07",
      [
        {
          code: "xanhda",
          name: "Xanh đá",
          hex: "#627d8b",
          image: "/products/ao-polo-pique-xanh-da.png",
        },
        { code: "kem", name: "Kem nhạt", hex: "#e5e3dc", image: "/products/ao-polo-pique-kem.png" },
      ],
      ALL_SIZES,
      299000,
      {
        xanhda: { S: 11, M: 24, L: 20, XL: 15, "2XL": 5 },
        kem: { S: 8, M: 13, L: 14, XL: 7, "2XL": 3 },
      }
    ),
    fabric: "62% Cotton chải kỹ, 35% Polyester thoáng khí, 3% Spandex",
    gsm: 215,
    fit: "Regular fit",
    origin: "Đà Nẵng, Việt Nam",
    careGuide: [
      "Giặt nước mát dưới 30 độ C, lộn trái sản phẩm.",
      "Là hơi ở mức nhiệt vừa để nẹp cúc phẳng gọn.",
    ],
    highlights: [
      "Tông xanh đá đặc trưng dịu mắt, ít nhăn khi ngồi làm việc cả ngày.",
      "Xẻ tà hai bên lót băng cotton mềm không cấn hông.",
    ],
    description:
      "Mẫu polo sắc xanh đá trầm của Sợi Tự Nhiên Đà Nẵng, đáp ứng gọn gàng cả ngày làm việc văn phòng lẫn buổi gặp gỡ cuối tuần.",
    sizeChart: TOP_SIZE_CHART,
    material: "pique-cotton",
    materialLabel: MATERIAL_LABELS["pique-cotton"],
    fitType: "regular",
    occasion: ["cong-so", "hang-ngay"],
    sport: [],
    fabricFeatures: ["thoang-khi", "it-nhan", "giu-form"],
    pattern: "det-lo",
    badges: [],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-short-linen", "prod-polo-kem"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 3h nội thành · Kho Đà Nẵng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 176,
        weightKg: 72,
        measurements: "Ngực 97 · Eo 81 · Mông 96 cm",
        wearingSize: "L",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Độ dày 215gsm có thích hợp mặc quanh năm không?",
        answer:
          "Định lượng 215gsm dệt mắt chim thoáng khí phù hợp khí hậu nóng ẩm lẫn văn phòng máy lạnh.",
      },
    ],
    wishlistCount: 143,
    salesRank: 10,
    createdAt: "2026-10-10T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["cong-so-gon-gang"],
  },

  // 8. Áo polo dệt kim đen
  {
    id: "prod-polo-detkim",
    slug: "ao-polo-det-kim-min-den-than",
    name: "Áo Polo Dệt kim mịn Đen than",
    shortName: "Áo Polo Dệt kim Đen",
    categorySlug: "ao-polo",
    categoryName: "Áo polo",
    shopId: "shop-ha-noi",
    gender: "nu",
    price: 369000,
    image: "/products/ao-polo-det-kim-den.png",
    onModelImage: "/models/model-polo-det-kim-den.jpg",
    gallery: [
      "/products/ao-polo-det-kim-den.png",
      "/models/model-polo-det-kim-den.jpg",
      "/products/ao-polo-pique-xanh-da.png",
      "/atmosphere/cotton-jersey-weave.webp",
    ],
    colors: [
      { code: "den", name: "Đen than", hex: "#20282e", image: "/products/ao-polo-det-kim-den.png" },
      {
        code: "xanhda",
        name: "Xanh đá",
        hex: "#627d8b",
        image: "/products/ao-polo-pique-xanh-da.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "HN-PK08",
      [
        {
          code: "den",
          name: "Đen than",
          hex: "#20282e",
          image: "/products/ao-polo-det-kim-den.png",
        },
        {
          code: "xanhda",
          name: "Xanh đá",
          hex: "#627d8b",
          image: "/products/ao-polo-pique-xanh-da.png",
        },
      ],
      ALL_SIZES,
      369000,
      {
        den: { S: 6, M: 16, L: 18, XL: 10, "2XL": 3 },
        xanhda: { S: 5, M: 10, L: 11, XL: 0, "2XL": 2 },
      }
    ),
    fabric: "70% Viscose sợi mảnh, 30% Nylon chống nhăn",
    gsm: 245,
    fit: "Tailored fit ôm gọn vai",
    origin: "Hà Nội, Việt Nam",
    careGuide: [
      "Cho vào túi giặt lưới khi giặt máy chế độ len/mỏng.",
      "Phơi phẳng trên mặt lưới để sợi dệt kim không bị kéo dài.",
    ],
    highlights: [
      "Mặt sợi dệt kim mịn rủ nhẹ, tạo vẻ chỉn chu thay thế sơ mi mùa thu đông.",
      "Cổ mở không cúc thoáng cổ, bo tay và bo gấu dệt liền thân.",
    ],
    description:
      "Dòng polo dệt kim máy phẳng từ Dệt Kim Hà Nội, mềm mịn trên da và giữ độ đứng cổ tốt khi mặc độc lập hoặc mặc trong áo khoác.",
    sizeChart: TOP_SIZE_CHART,
    isNew: true,
    material: "viscose-knit",
    materialLabel: MATERIAL_LABELS["viscose-knit"],
    fitType: "slim",
    occasion: ["cong-so", "du-lich"],
    sport: [],
    fabricFeatures: ["it-nhan", "giu-form", "thoang-khi"],
    pattern: "gan",
    badges: ["moi", "pre-order"],
    isCombo: false,
    tierDiscounts: [{ minQty: 2, discountPercent: 10, label: "Mua 2 giảm thêm 10%" }],
    bundleIds: ["prod-jogger-tech", "prod-tat-cotrung"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Pre-order giao 3-5 ngày · Kho Hà Nội",
    maxPerOrder: 2,
    modelInfo: [
      {
        gender: "nu",
        heightCm: 165,
        weightKg: 54,
        measurements: "Ngực 84 · Eo 65 · Mông 92 cm",
        wearingSize: "S",
        note: "Ôm nhẹ vai, cổ mở gọn thanh lịch",
      },
      {
        gender: "nam",
        heightCm: 174,
        weightKg: 68,
        measurements: "Ngực 95 · Eo 79 · Mông 94 cm",
        wearingSize: "L",
        note: "Form dệt kim ôm gọn nên tăng 1 cỡ nếu thích rộng",
      },
    ],
    careInstructions: KNIT_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Sản phẩm Pre-order mất bao lâu để giao hàng?",
        answer:
          "Lô dệt kim mới xuất xưởng Dệt Kim Hà Nội sau 2 ngày và giao tới khách trong 3-5 ngày làm việc, vẫn áp dụng đổi trả 7 ngày.",
      },
    ],
    wishlistCount: 188,
    salesRank: 11,
    createdAt: "2026-11-16T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["cong-so-gon-gang"],
  },

  // 9. Quần short daily be
  {
    id: "prod-short-daily",
    slug: "quan-short-daily-cotton-7-inch-be",
    name: "Quần Short Daily Cotton 7 inch Be cát",
    shortName: "Quần Short Daily Be",
    categorySlug: "quan-short",
    categoryName: "Quần short",
    shopId: "shop-sai-gon",
    gender: "nam",
    price: 249000,
    originalPrice: 289000,
    image: "/products/quan-short-daily-be.png",
    onModelImage: "/models/model-short-daily-be.jpg",
    gallery: [
      "/products/quan-short-daily-be.png",
      "/models/model-short-daily-be.jpg",
      "/products/quan-short-linen-xam-da.png",
      "/atmosphere/cotton-twill-slate.jpg",
    ],
    colors: [
      { code: "be", name: "Be cát", hex: "#d6cec1", image: "/products/quan-short-daily-be.png" },
      {
        code: "xamda",
        name: "Xám đá",
        hex: "#949fa6",
        image: "/products/quan-short-linen-xam-da.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "SG-SD09",
      [
        { code: "be", name: "Be cát", hex: "#d6cec1", image: "/products/quan-short-daily-be.png" },
        {
          code: "xamda",
          name: "Xám đá",
          hex: "#949fa6",
          image: "/products/quan-short-linen-xam-da.png",
        },
      ],
      ALL_SIZES,
      249000,
      {
        be: { S: 13, M: 26, L: 22, XL: 14, "2XL": 6 },
        xamda: { S: 9, M: 17, L: 15, XL: 8, "2XL": 3 },
      }
    ),
    fabric: "97% Cotton Twill chải kỹ, 3% Elastane",
    gsm: 235,
    fit: "Ống đứng 7 inch trên gối",
    origin: "TP. Hồ Chí Minh, Việt Nam",
    careGuide: [
      "Giặt máy nước mát, kéo khóa túi trước khi giặt.",
      "Phơi kẹp cạp quần để ống quần thẳng tự nhiên.",
    ],
    highlights: [
      "Cạp chun giấu dây rút bên trong gọn gàng khi sơ vin hoặc thả áo.",
      "Hai túi hông sâu giữ điện thoại không rơi khi ngồi xe.",
    ],
    description:
      "Chiếc quần short kaki cotton co giãn nhẹ dài 7 inch trên gối, mặc thoải mái suốt ngày từ đi cà phê đến di chuyển ngoài trời.",
    sizeChart: BOTTOM_SIZE_CHART,
    isFlashSale: true,
    material: "cotton-twill",
    materialLabel: MATERIAL_LABELS["cotton-twill"],
    fitType: "regular",
    occasion: ["hang-ngay", "du-lich"],
    sport: ["di-bo"],
    fabricFeatures: ["giu-form", "tham-hut", "it-nhan"],
    pattern: "tron",
    badges: ["ban-chay"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-at-oversize", "prod-at-dryfit", "prod-aokhoac-gio"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho TP.HCM",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 176,
        weightKg: 71,
        measurements: "Eo 80 · Mông 96 cm",
        wearingSize: "L",
        note: "Ống dài trên gối 3 cm, gọn đùi",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Túi quần short Daily có đựng vừa điện thoại 6,7 inch không?",
        answer:
          "Lòng túi sâu 19 cm có đường may chặn góc giữ điện thoại chắc ngay cả khi ngồi xe máy.",
      },
    ],
    wishlistCount: 203,
    salesRank: 4,
    createdAt: "2026-09-28T08:00:00+07:00",
    shopSizeChart: "bottom",
    collectionSlugs: ["co-ban-hang-ngay"],
  },

  // 10. Quần short run xám đậm (Outlet, không đổi trả)
  {
    id: "prod-short-run",
    slug: "quan-short-chay-bo-5-inch-xam-dam",
    name: "Quần Short Chạy bộ 5 inch Xám đậm",
    shortName: "Quần Short Run Xám đậm",
    categorySlug: "quan-short",
    categoryName: "Quần short",
    shopId: "shop-sai-gon",
    gender: "nam",
    price: 219000,
    originalPrice: 279000,
    image: "/products/quan-short-run-xam-dam.png",
    onModelImage: "/models/model-short-run-xam.jpg",
    gallery: [
      "/products/quan-short-run-xam-dam.png",
      "/models/model-short-run-xam.jpg",
      "/products/quan-short-daily-be.png",
      "/products/ao-thun-dryfit-co-vit.png",
    ],
    colors: [
      {
        code: "xamdam",
        name: "Xám đậm",
        hex: "#434c52",
        image: "/products/quan-short-run-xam-dam.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "SG-SR10",
      [
        {
          code: "xamdam",
          name: "Xám đậm",
          hex: "#434c52",
          image: "/products/quan-short-run-xam-dam.png",
        },
      ],
      ALL_SIZES,
      219000,
      {
        xamdam: { S: 18, M: 31, L: 27, XL: 16, "2XL": 8 },
      }
    ),
    fabric: "90% Polyamide co giãn 4 chiều, 10% Elastane",
    gsm: 118,
    fit: "Active fit 5 inch xẻ tà",
    origin: "TP. Hồ Chí Minh, Việt Nam",
    careGuide: [
      "Giặt nước lạnh, không dùng chất làm mềm vải.",
      "Không là ủi trực tiếp lên vải kỹ thuật.",
    ],
    highlights: [
      "Trọng lượng siêu nhẹ 118gsm, xẻ gấu hai bên cho bước sải chân rộng.",
      "Túi khóa kéo sau lưng đựng vừa chìa khóa và thẻ gửi xe.",
    ],
    description:
      "Quần short chạy bộ 5 inch khô nhanh, bề mặt trơn mát không bết dính khi đổ mồ hôi nhiều.",
    sizeChart: BOTTOM_SIZE_CHART,
    material: "polyamide-active",
    materialLabel: MATERIAL_LABELS["polyamide-active"],
    fitType: "athletic",
    occasion: ["the-thao"],
    sport: ["chay-bo", "gym", "cau-long", "dap-xe"],
    fabricFeatures: ["nhanh-kho", "co-gian-4-chieu", "thoang-khi", "truot-nuoc"],
    pattern: "tron",
    badges: ["outlet"],
    isCombo: false,
    tierDiscounts: [{ minQty: 2, discountPercent: 10, label: "Mua 2 giảm thêm 10%" }],
    bundleIds: ["prod-at-dryfit", "prod-tat-cothap"],
    returnEligible: false,
    returnExclusionReason: "Sản phẩm thuộc đợt giá Outlet xả kho mùa trước, không áp dụng đổi trả.",
    slaText: "Giao nhanh 2-4h nội thành · Kho TP.HCM",
    maxPerOrder: 3,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 175,
        weightKg: 68,
        measurements: "Eo 78 · Mông 94 cm",
        wearingSize: "L",
      },
    ],
    careInstructions: SPORT_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Quần chạy bộ 5 inch có quần lót lưới bên trong không?",
        answer:
          "Mẫu này là quần 1 lớp không lót để người chạy chủ động kết hợp cùng quần lót thể thao yêu thích.",
      },
    ],
    wishlistCount: 164,
    salesRank: 12,
    createdAt: "2026-08-05T08:00:00+07:00",
    shopSizeChart: "bottom",
    collectionSlugs: ["chay-bo-tap-luyen", "san-outlet"],
  },

  // 11. Quần short linen xám đá
  {
    id: "prod-short-linen",
    slug: "quan-short-linen-pha-cotton-xam-da",
    name: "Quần Short Linen pha Cotton Xám đá",
    shortName: "Quần Short Linen Xám đá",
    categorySlug: "quan-short",
    categoryName: "Quần short",
    shopId: "shop-da-nang",
    gender: "unisex",
    price: 279000,
    image: "/products/quan-short-linen-xam-da.png",
    onModelImage: "/models/model-short-linen-xam.jpg",
    gallery: [
      "/products/quan-short-linen-xam-da.png",
      "/models/model-short-linen-xam.jpg",
      "/products/quan-short-daily-be.png",
      "/atmosphere/cotton-boll-harvest.jpg",
    ],
    colors: [
      {
        code: "xamda",
        name: "Xám đá",
        hex: "#949fa6",
        image: "/products/quan-short-linen-xam-da.png",
      },
      { code: "be", name: "Be cát", hex: "#d6cec1", image: "/products/quan-short-daily-be.png" },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "DN-SL11",
      [
        {
          code: "xamda",
          name: "Xám đá",
          hex: "#949fa6",
          image: "/products/quan-short-linen-xam-da.png",
        },
        { code: "be", name: "Be cát", hex: "#d6cec1", image: "/products/quan-short-daily-be.png" },
      ],
      ALL_SIZES,
      279000,
      {
        xamda: { S: 10, M: 18, L: 16, XL: 9, "2XL": 2 },
        be: { S: 7, M: 14, L: 12, XL: 6, "2XL": 3 },
      }
    ),
    fabric: "55% Sợi lanh tự nhiên (Linen), 45% Cotton",
    gsm: 195,
    fit: "Relaxed fit ống thoáng",
    origin: "Đà Nẵng, Việt Nam",
    careGuide: [
      "Giặt nhẹ nước lạnh và giũ phẳng ngay khi lấy khỏi máy giặt.",
      "Là hơi nước khi vải còn hơi ẩm để sợi lanh mềm đẹp.",
    ],
    highlights: [
      "Vân sợi lanh tự nhiên thoáng gió, pha 45% cotton giúp giảm nhăn rõ rệt.",
      "Phom rộng vừa phải phù hợp cả nam và nữ mặc dạo phố hoặc du lịch.",
    ],
    description:
      "Quần short lanh pha bông của Sợi Tự Nhiên Đà Nẵng mang lại cảm giác mát nhẹ ngay cả những ngày nắng trên 35 độ.",
    sizeChart: BOTTOM_SIZE_CHART,
    material: "linen-cotton",
    materialLabel: MATERIAL_LABELS["linen-cotton"],
    fitType: "relaxed",
    occasion: ["du-lich", "hang-ngay", "mac-nha"],
    sport: ["di-bo"],
    fabricFeatures: ["thoang-khi", "tham-hut", "it-nhan"],
    pattern: "tron",
    badges: ["moi"],
    isCombo: false,
    tierDiscounts: BASIC_TIER_DISCOUNTS,
    bundleIds: ["prod-polo-kem", "prod-polo-xanhda"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 3h nội thành · Kho Đà Nẵng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 175,
        weightKg: 70,
        measurements: "Eo 80 · Mông 95 cm",
        wearingSize: "L",
      },
      {
        gender: "nu",
        heightCm: 164,
        weightKg: 52,
        measurements: "Eo 64 · Mông 91 cm",
        wearingSize: "S",
        note: "Cạp dây rút chỉnh gọn eo dễ mặc",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Quần linen pha cotton có bị nhăn nhiều như linen 100% không?",
        answer: "Tỷ lệ 45% cotton giúp sợi mềm nhanh và hạn chế gãy nếp mạnh khi ngồi lâu.",
      },
    ],
    wishlistCount: 175,
    salesRank: 13,
    createdAt: "2026-11-04T08:00:00+07:00",
    shopSizeChart: "bottom",
    collectionSlugs: ["soi-xanh-nexus", "cong-so-gon-gang"],
  },

  // 12. Quần jogger terry xám
  {
    id: "prod-jogger-terry",
    slug: "quan-jogger-ni-chan-cua-xam-muoi-tieu",
    name: "Quần Jogger Nỉ chân cua 320gsm Xám tiêu",
    shortName: "Quần Jogger Terry Xám",
    categorySlug: "quan-jogger",
    categoryName: "Quần jogger",
    shopId: "shop-ha-noi",
    gender: "unisex",
    price: 349000,
    originalPrice: 399000,
    image: "/products/quan-jogger-terry-xam.png",
    onModelImage: "/models/model-jogger-terry-xam.jpg",
    gallery: [
      "/products/quan-jogger-terry-xam.png",
      "/models/model-jogger-terry-xam.jpg",
      "/products/quan-jogger-tech-den.png",
      "/atmosphere/cotton-jersey-drape.webp",
    ],
    colors: [
      {
        code: "xam",
        name: "Xám muối tiêu",
        hex: "#a2acb3",
        image: "/products/quan-jogger-terry-xam.png",
      },
      {
        code: "den",
        name: "Than đen",
        hex: "#273036",
        image: "/products/quan-jogger-tech-den.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "HN-JT12",
      [
        {
          code: "xam",
          name: "Xám muối tiêu",
          hex: "#a2acb3",
          image: "/products/quan-jogger-terry-xam.png",
        },
        {
          code: "den",
          name: "Than đen",
          hex: "#273036",
          image: "/products/quan-jogger-tech-den.png",
        },
      ],
      ALL_SIZES,
      349000,
      {
        xam: { S: 11, M: 22, L: 25, XL: 13, "2XL": 5 },
        den: { S: 8, M: 16, L: 19, XL: 10, "2XL": 4 },
      }
    ),
    fabric: "82% Cotton French Terry, 18% Polyester giữ form",
    gsm: 320,
    fit: "Tapered fit thu gọn cổ chân",
    origin: "Hà Nội, Việt Nam",
    careGuide: [
      "Lộn trái khi giặt máy để giữ mặt vải mịn, không xù lông.",
      "Phơi kẹp ngang cạp quần ở nơi thoáng gió.",
    ],
    highlights: [
      "Mặt trong dệt vòng chân cua thoáng khí, không gây bí như nỉ cào bông.",
      "Bo cổ chân bản 5 cm ôm gọn cổ giày mà không bó chặt.",
    ],
    description:
      "Quần jogger nỉ chân cua dày 320gsm giữ phom gối không bị phồng sau thời gian dài sử dụng.",
    sizeChart: JOGGER_SIZE_CHART,
    isFlashSale: true,
    material: "french-terry",
    materialLabel: MATERIAL_LABELS["french-terry"],
    fitType: "regular",
    occasion: ["hang-ngay", "the-thao", "du-lich", "mac-nha"],
    sport: ["di-bo", "gym"],
    fabricFeatures: ["giu-form", "tham-hut", "thoang-khi"],
    pattern: "tron",
    badges: ["ban-chay"],
    isCombo: false,
    tierDiscounts: [{ minQty: 2, discountPercent: 10, label: "Mua 2 giảm thêm 10%" }],
    bundleIds: ["prod-tat-cotrung", "prod-polo-detkim"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho Hà Nội",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 176,
        weightKg: 72,
        measurements: "Eo 81 · Mông 96 cm",
        wearingSize: "L",
      },
      {
        gender: "nu",
        heightCm: 164,
        weightKg: 53,
        measurements: "Eo 65 · Mông 92 cm",
        wearingSize: "S",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: true,
    videoUrl: "/atmosphere/jacquard-loom-loop.webm",
    videoPoster: "/atmosphere/jacquard-loom-poster.jpg",
    faqs: [
      {
        question: "Quần nỉ chân cua 320gsm có bị đổ lông mặt trong không?",
        answer: "Vải dệt vòng French Terry không cào bông nên không rụng lông vào tất hay đồ lót.",
      },
    ],
    wishlistCount: 231,
    salesRank: 6,
    createdAt: "2026-10-15T08:00:00+07:00",
    shopSizeChart: "jogger",
    collectionSlugs: ["co-ban-hang-ngay", "chay-bo-tap-luyen"],
  },

  // 13. Quần jogger tech đen
  {
    id: "prod-jogger-tech",
    slug: "quan-jogger-co-gian-tech-den-than",
    name: "Quần Jogger Co giãn Tech Đen than",
    shortName: "Quần Jogger Tech Đen",
    categorySlug: "quan-jogger",
    categoryName: "Quần jogger",
    shopId: "shop-ha-noi",
    gender: "nam",
    price: 389000,
    image: "/products/quan-jogger-tech-den.png",
    onModelImage: "/models/model-jogger-tech-den.jpg",
    gallery: [
      "/products/quan-jogger-tech-den.png",
      "/models/model-jogger-tech-den.jpg",
      "/products/quan-jogger-terry-xam.png",
      "/products/ao-polo-det-kim-den.png",
    ],
    colors: [
      {
        code: "den",
        name: "Đen than",
        hex: "#273036",
        image: "/products/quan-jogger-tech-den.png",
      },
      {
        code: "xam",
        name: "Xám muối tiêu",
        hex: "#a2acb3",
        image: "/products/quan-jogger-terry-xam.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "HN-JK13",
      [
        {
          code: "den",
          name: "Đen than",
          hex: "#273036",
          image: "/products/quan-jogger-tech-den.png",
        },
        {
          code: "xam",
          name: "Xám muối tiêu",
          hex: "#a2acb3",
          image: "/products/quan-jogger-terry-xam.png",
        },
      ],
      ALL_SIZES,
      389000,
      {
        den: { S: 9, M: 19, L: 21, XL: 12, "2XL": 4 },
        xam: { S: 6, M: 12, L: 11, XL: 7, "2XL": 0 },
      }
    ),
    fabric: "86% Nylon dệt thoi trượt nước nhẹ, 14% Spandex",
    gsm: 185,
    fit: "Slim tapered gọn gàng",
    origin: "Hà Nội, Việt Nam",
    careGuide: [
      "Giặt nước lạnh, kéo kín khóa túi trước khi giặt.",
      "Không dùng chất tẩy và không sấy nhiệt cao.",
    ],
    highlights: [
      "Co giãn 4 chiều thoải mái khi di chuyển, lái xe hoặc đi công tác.",
      "Hai túi khóa kéo chìm giữ vật dụng an toàn, bề mặt hạn chế bám bụi.",
    ],
    description:
      "Quần jogger chất liệu kỹ thuật bề mặt lì sang trọng, có thể mặc đi làm môi trường sáng tạo hoặc di chuyển đường dài.",
    sizeChart: JOGGER_SIZE_CHART,
    material: "nylon-tech",
    materialLabel: MATERIAL_LABELS["nylon-tech"],
    fitType: "slim",
    occasion: ["cong-so", "du-lich", "the-thao"],
    sport: ["di-bo", "dap-xe"],
    fabricFeatures: ["co-gian-4-chieu", "truot-nuoc", "it-nhan", "nhanh-kho"],
    pattern: "tron",
    badges: [],
    isCombo: false,
    tierDiscounts: [{ minQty: 2, discountPercent: 10, label: "Mua 2 giảm thêm 10%" }],
    bundleIds: ["prod-polo-detkim", "prod-tat-cotrung"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho Hà Nội",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 178,
        weightKg: 73,
        measurements: "Eo 81 · Mông 96 cm",
        wearingSize: "L",
      },
    ],
    careInstructions: SPORT_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Vải Nylon Tech khi đi lại có phát ra tiếng sột soạt không?",
        answer:
          "Bề mặt được xử lý mờ và pha 14% spandex nên chuyển động êm, không sột soạt như vải gió cứng.",
      },
    ],
    wishlistCount: 169,
    salesRank: 14,
    createdAt: "2026-10-22T08:00:00+07:00",
    shopSizeChart: "jogger",
    collectionSlugs: ["cong-so-gon-gang", "chay-bo-tap-luyen"],
  },

  // 14. Đồ lót boxer modal combo 3 (Combo chọn cỡ từng món)
  {
    id: "prod-dolot-boxer",
    slug: "combo-3-quan-lot-boxer-modal",
    name: "Combo 3 Quần lót Boxer Sợi Modal",
    shortName: "Combo 3 Boxer Modal",
    categorySlug: "do-lot",
    categoryName: "Đồ lót",
    shopId: "shop-bach-dang",
    gender: "nam",
    price: 229000,
    originalPrice: 269000,
    image: "/products/do-lot-boxer-modal-3.png",
    onModelImage: "/models/model-mac-nha-modal.jpg",
    gallery: [
      "/products/do-lot-boxer-modal-3.png",
      "/models/model-mac-nha-modal.jpg",
      "/products/do-lot-brief-tre-3.png",
      "/atmosphere/cotton-jersey-drape.webp",
    ],
    colors: [
      {
        code: "combo-trungtinh",
        name: "Combo Xám / Than / Đen",
        hex: "#4a565e",
        image: "/products/do-lot-boxer-modal-3.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "BD-DL14",
      [
        {
          code: "combo-trungtinh",
          name: "Combo Xám / Than / Đen",
          hex: "#4a565e",
          image: "/products/do-lot-boxer-modal-3.png",
        },
      ],
      ALL_SIZES,
      229000,
      {
        "combo-trungtinh": { S: 14, M: 32, L: 35, XL: 24, "2XL": 11 },
      }
    ),
    fabric: "93% Sợi gỗ sồi Modal, 7% Spandex",
    gsm: 180,
    fit: "Boxer brief ôm nâng đỡ không cuộn ống",
    origin: "Hải Phòng, Việt Nam",
    careGuide: [
      "Giặt trong túi lưới với nước mát dưới 30 độ C.",
      "Nên thay mới đồ lót định kỳ sau 6 tháng sử dụng.",
    ],
    highlights: [
      "Cạp chun dệt mịn không in chữ, không gây hằn đỏ quanh bụng.",
      "Công nghệ may phẳng Flatlock hạn chế cộm đường chỉ vùng đùi.",
    ],
    description:
      "Bộ 3 chiếc quần lót boxer làm từ sợi gỗ sồi modal siêu mềm mát, hút ẩm tốt hơn 50% so với cotton thông thường.",
    sizeChart: UNDERWEAR_SIZE_CHART,
    isFlashSale: true,
    material: "modal",
    materialLabel: MATERIAL_LABELS.modal,
    fitType: "slim",
    occasion: ["hang-ngay", "the-thao", "mac-nha"],
    sport: ["chay-bo", "gym"],
    fabricFeatures: ["tham-hut", "thoang-khi", "khang-mui", "co-gian-4-chieu"],
    pattern: "phoi-mau",
    badges: ["ban-chay"],
    isCombo: true,
    comboSlots: [
      {
        slotId: "boxer-1",
        label: "Chiếc 1 · Xám nhạt",
        colorCode: "xam-nhat",
        colorName: "Xám nhạt",
        availableSizes: ALL_SIZES,
        defaultSize: "L",
      },
      {
        slotId: "boxer-2",
        label: "Chiếc 2 · Xám chì",
        colorCode: "xam-chi",
        colorName: "Xám chì",
        availableSizes: ALL_SIZES,
        defaultSize: "L",
      },
      {
        slotId: "boxer-3",
        label: "Chiếc 3 · Đen than",
        colorCode: "den-than",
        colorName: "Đen than",
        availableSizes: ALL_SIZES,
        defaultSize: "L",
      },
    ],
    tierDiscounts: PACK_TIER_DISCOUNTS,
    bundleIds: ["prod-at-trang", "prod-tat-cotrung"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Giao 1-2 ngày · Kho Hải Phòng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 175,
        weightKg: 70,
        measurements: "Eo 80 · Mông 95 cm",
        wearingSize: "L",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Hộp 3 quần lót boxer sợi Modal gồm những màu nào và chọn cỡ ra sao?",
        answer:
          "Mỗi hộp gồm 3 màu trung tính (Xám nhạt, Xám chì, Đen than) đồng nhất theo kích cỡ bạn đã chọn trên trang sản phẩm.",
      },
    ],
    wishlistCount: 268,
    salesRank: 8,
    createdAt: "2026-09-15T08:00:00+07:00",
    shopSizeChart: "underwear",
    collectionSlugs: ["co-ban-hang-ngay", "soi-xanh-nexus"],
  },

  // 15. Đồ lót brief sợi tre combo 3 (Pack niêm phong không đổi trả + Combo chọn cỡ)
  {
    id: "prod-dolot-brief",
    slug: "combo-3-quan-lot-brief-soi-tre",
    name: "Combo 3 Quần lót Brief Sợi tre",
    shortName: "Combo 3 Brief Sợi tre",
    categorySlug: "do-lot",
    categoryName: "Đồ lót",
    shopId: "shop-da-nang",
    gender: "nam",
    price: 209000,
    image: "/products/do-lot-brief-tre-3.png",
    onModelImage: "/models/model-mac-nha-bamboo.jpg",
    gallery: [
      "/products/do-lot-brief-tre-3.png",
      "/models/model-mac-nha-bamboo.jpg",
      "/products/do-lot-boxer-modal-3.png",
      "/atmosphere/cotton-boll-harvest.jpg",
    ],
    colors: [
      {
        code: "combo-xanhxam",
        name: "Combo Xanh đá / Xám / Đậm",
        hex: "#546f7d",
        image: "/products/do-lot-brief-tre-3.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "DN-DL15",
      [
        {
          code: "combo-xanhxam",
          name: "Combo Xanh đá / Xám / Đậm",
          hex: "#546f7d",
          image: "/products/do-lot-brief-tre-3.png",
        },
      ],
      ALL_SIZES,
      209000,
      {
        "combo-xanhxam": { S: 12, M: 26, L: 29, XL: 18, "2XL": 9 },
      }
    ),
    fabric: "95% Sợi tre tự nhiên (Bamboo), 5% Spandex",
    gsm: 175,
    fit: "Classic brief thoáng đùi",
    origin: "Đà Nẵng, Việt Nam",
    careGuide: [
      "Giặt máy nhẹ trong túi giặt, phơi nơi thoáng gió.",
      "Tránh ngâm chất tẩy mạnh để bảo toàn độ mềm sợi tre.",
    ],
    highlights: [
      "Sợi tre tự nhiên hạn chế mùi khó chịu suốt ngày dài.",
      "Nhãn ép nhiệt trong lưng quần, không có tem vải gây ngứa.",
    ],
    description:
      "Bộ 3 quần lót tam giác sợi tre thoáng nhẹ, dành cho người ưu tiên sự gọn gàng khi mặc quần tây hoặc quần short thể thao.",
    sizeChart: UNDERWEAR_SIZE_CHART,
    material: "bamboo",
    materialLabel: MATERIAL_LABELS.bamboo,
    fitType: "slim",
    occasion: ["hang-ngay", "cong-so", "the-thao"],
    sport: ["chay-bo", "di-bo"],
    fabricFeatures: ["khang-mui", "thoang-khi", "tham-hut"],
    pattern: "phoi-mau",
    badges: [],
    isCombo: true,
    comboSlots: [
      {
        slotId: "brief-1",
        label: "Chiếc 1 · Xanh đá",
        colorCode: "xanh-da",
        colorName: "Xanh đá",
        availableSizes: ALL_SIZES,
        defaultSize: "L",
      },
      {
        slotId: "brief-2",
        label: "Chiếc 2 · Xám nhạt",
        colorCode: "xam-nhat",
        colorName: "Xám nhạt",
        availableSizes: ALL_SIZES,
        defaultSize: "L",
      },
      {
        slotId: "brief-3",
        label: "Chiếc 3 · Xám đậm",
        colorCode: "xam-dam",
        colorName: "Xám đậm",
        availableSizes: ALL_SIZES,
        defaultSize: "L",
      },
    ],
    tierDiscounts: PACK_TIER_DISCOUNTS,
    bundleIds: ["prod-polo-kem", "prod-short-linen"],
    returnEligible: false,
    returnExclusionReason:
      "Pack đồ lót niêm phong đã mở túi không áp dụng đổi trả để đảm bảo vệ sinh cá nhân.",
    slaText: "Giao nhanh 3h nội thành · Kho Đà Nẵng",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 174,
        weightKg: 68,
        measurements: "Eo 79 · Mông 94 cm",
        wearingSize: "L",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Tại sao pack đồ lót niêm phong không áp dụng đổi trả sau khi bóc?",
        answer:
          "Để bảo đảm tiêu chuẩn vệ sinh cho mọi khách hàng, pack đồ lót niêm phong chỉ hỗ trợ đổi nếu lỗi từ nhà sản xuất khi nhận hàng.",
      },
    ],
    wishlistCount: 129,
    salesRank: 16,
    createdAt: "2026-09-25T08:00:00+07:00",
    shopSizeChart: "underwear",
    collectionSlugs: ["soi-xanh-nexus"],
  },

  // 16. Tất cotton cổ trung combo 5
  {
    id: "prod-tat-cotrung",
    slug: "combo-5-doi-tat-cotton-gan-co-trung",
    name: "Combo 5 đôi Tất Cotton gân cổ trung",
    shortName: "Combo 5 Tất cổ trung",
    categorySlug: "tat-phu-kien",
    categoryName: "Tất & phụ kiện",
    shopId: "shop-ha-noi",
    gender: "unisex",
    price: 129000,
    originalPrice: 159000,
    image: "/products/tat-cotton-co-trung-5.png",
    onModelImage: "/models/model-tat-co-trung.jpg",
    gallery: [
      "/products/tat-cotton-co-trung-5.png",
      "/models/model-tat-co-trung.jpg",
      "/products/tat-the-thao-co-thap-4.png",
      "/atmosphere/cotton-jersey-weave.webp",
    ],
    colors: [
      {
        code: "combo-5mau",
        name: "5 tông Trắng đến Đen",
        hex: "#6f7b82",
        image: "/products/tat-cotton-co-trung-5.png",
      },
    ],
    sizes: ["M", "L"],
    skus: buildSkus(
      "HN-TT16",
      [
        {
          code: "combo-5mau",
          name: "5 tông Trắng đến Đen",
          hex: "#6f7b82",
          image: "/products/tat-cotton-co-trung-5.png",
        },
      ],
      ["M", "L"],
      129000,
      {
        "combo-5mau": { M: 42, L: 38 },
      }
    ),
    fabric: "80% Cotton chải kỹ, 17% Polyamide, 3% Elastane",
    gsm: 210,
    fit: "Cổ trung 16 cm dệt gân giữ cổ",
    origin: "Hà Nội, Việt Nam",
    careGuide: ["Giặt nước mát dưới 40 độ C, lộn mặt trái để hạn chế bám xơ vải."],
    highlights: [
      "Mũi tất nối liền không đường gờ gây cấn ngón chân khi đi giày da hoặc sneaker.",
      "Đệm gót dệt dày hơn 20% tăng độ bền tại vị trí chịu ma sát.",
    ],
    description:
      "Bộ 5 đôi tất cổ trung trải dài dải màu trung tính từ trắng, xám nhạt đến đen, đủ dùng trọn tuần làm việc.",
    sizeChart: SOCK_SIZE_CHART,
    isFlashSale: true,
    material: "cotton-compact",
    materialLabel: MATERIAL_LABELS["cotton-compact"],
    fitType: "regular",
    occasion: ["cong-so", "hang-ngay"],
    sport: ["di-bo"],
    fabricFeatures: ["tham-hut", "khang-mui", "giu-form"],
    pattern: "gan",
    badges: ["ban-chay"],
    isCombo: false,
    isPack: true,
    tierDiscounts: PACK_TIER_DISCOUNTS,
    bundleIds: ["prod-tat-cothap", "prod-jogger-terry"],
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho Hà Nội",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 175,
        weightKg: 70,
        measurements: "Cỡ giày 42",
        wearingSize: "L",
      },
      {
        gender: "nu",
        heightCm: 162,
        weightKg: 51,
        measurements: "Cỡ giày 38",
        wearingSize: "M",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Chính sách bù lẻ chiếc tất thất lạc áp dụng như thế nào?",
        answer:
          "Trong 7 ngày từ khi nhận đơn, nếu thất lạc 1 chiếc tất trong combo, bạn có thể gửi mã đơn tại Trợ giúp để Dệt Kim Hà Nội gửi kèm 1 chiếc đồng màu ở đơn kế tiếp.",
      },
    ],
    wishlistCount: 289,
    salesRank: 15,
    createdAt: "2026-09-10T08:00:00+07:00",
    shopSizeChart: "sock",
    collectionSlugs: ["co-ban-hang-ngay", "cong-so-gon-gang"],
  },

  // 17. Tất thể thao cổ thấp combo 4
  {
    id: "prod-tat-cothap",
    slug: "combo-4-doi-tat-the-thao-co-thap",
    name: "Combo 4 đôi Tất Thể thao cổ thấp",
    shortName: "Combo 4 Tất cổ thấp",
    categorySlug: "tat-phu-kien",
    categoryName: "Tất & phụ kiện",
    shopId: "shop-ha-noi",
    gender: "unisex",
    price: 109000,
    image: "/products/tat-the-thao-co-thap-4.png",
    onModelImage: "/models/model-tat-the-thao.jpg",
    gallery: [
      "/products/tat-the-thao-co-thap-4.png",
      "/models/model-tat-the-thao.jpg",
      "/products/tat-cotton-co-trung-5.png",
      "/products/quan-short-run-xam-dam.png",
    ],
    colors: [
      {
        code: "combo-trangxam",
        name: "Trắng & Xám sáng",
        hex: "#d9e0e4",
        image: "/products/tat-the-thao-co-thap-4.png",
      },
    ],
    sizes: ["M", "L"],
    skus: buildSkus(
      "HN-TT17",
      [
        {
          code: "combo-trangxam",
          name: "Trắng & Xám sáng",
          hex: "#d9e0e4",
          image: "/products/tat-the-thao-co-thap-4.png",
        },
      ],
      ["M", "L"],
      109000,
      {
        "combo-trangxam": { M: 36, L: 29 },
      }
    ),
    fabric: "75% Cotton thấm hút, 20% Polyester thoát ẩm, 5% Elastane",
    gsm: 190,
    fit: "Cổ thấp ngang mắt cá, có gờ bảo vệ gót",
    origin: "Hà Nội, Việt Nam",
    careGuide: ["Giặt máy nước mát, phơi nơi thoáng gió."],
    highlights: [
      "Lưới thoáng khí mu bàn chân giảm hầm bí khi chạy bộ.",
      "Gờ đệm sau cổ chân ngăn trượt tất vào trong giày thể thao.",
    ],
    description:
      "Bộ 4 đôi tất cổ thấp thiết kế riêng cho giày chạy bộ và sneaker hằng ngày, có đai nén nhẹ giữa lòng bàn chân.",
    sizeChart: SOCK_SIZE_CHART,
    material: "cotton-compact",
    materialLabel: MATERIAL_LABELS["cotton-compact"],
    fitType: "athletic",
    occasion: ["the-thao", "hang-ngay"],
    sport: ["chay-bo", "gym", "cau-long", "dap-xe"],
    fabricFeatures: ["thoang-khi", "tham-hut", "nhanh-kho"],
    pattern: "det-lo",
    badges: [],
    isCombo: false,
    isPack: true,
    tierDiscounts: PACK_TIER_DISCOUNTS,
    bundleIds: ["prod-tat-cotrung", "prod-short-run"],
    returnEligible: true,
    slaText: "Giao nhanh 2-4h nội thành · Kho Hà Nội",
    maxPerOrder: 5,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 174,
        weightKg: 68,
        measurements: "Cỡ giày 42",
        wearingSize: "L",
      },
      {
        gender: "nu",
        heightCm: 161,
        weightKg: 50,
        measurements: "Cỡ giày 38",
        wearingSize: "M",
      },
    ],
    careInstructions: COTTON_CARE,
    hasVideo: false,
    faqs: [
      {
        question: "Tất cổ thấp khi chạy bộ đường dài có bị tụt gót không?",
        answer:
          "Thiết kế gờ đệm sau gót kết hợp đai nén giữa bàn chân giữ tất ôm sát, hạn chế tụt vào trong giày.",
      },
    ],
    wishlistCount: 151,
    salesRank: 17,
    createdAt: "2026-10-05T08:00:00+07:00",
    shopSizeChart: "sock",
    collectionSlugs: ["chay-bo-tap-luyen"],
  },

  // 18. Áo khoác gió xám tro
  {
    id: "prod-aokhoac-gio",
    slug: "ao-khoac-gio-nhe-gap-gon-xam-tro",
    name: "Áo khoác Gió nhẹ gấp gọn Xám tro",
    shortName: "Áo khoác Gió Xám tro",
    categorySlug: "tat-phu-kien",
    categoryName: "Tất & phụ kiện",
    shopId: "shop-sai-gon",
    gender: "unisex",
    price: 429000,
    originalPrice: 499000,
    image: "/products/ao-khoac-gio-xam-tro.png",
    onModelImage: "/models/model-ao-khoac-gio.jpg",
    gallery: [
      "/products/ao-khoac-gio-xam-tro.png",
      "/models/model-ao-khoac-gio.jpg",
      "/products/ao-thun-cotton-xam-da.png",
      "/atmosphere/cotton-twill-slate.jpg",
    ],
    colors: [
      {
        code: "xamtro",
        name: "Xám tro",
        hex: "#8e99a0",
        image: "/products/ao-khoac-gio-xam-tro.png",
      },
    ],
    sizes: ALL_SIZES,
    skus: buildSkus(
      "SG-AK18",
      [
        {
          code: "xamtro",
          name: "Xám tro",
          hex: "#8e99a0",
          image: "/products/ao-khoac-gio-xam-tro.png",
        },
      ],
      ALL_SIZES,
      429000,
      {
        xamtro: { S: 8, M: 17, L: 19, XL: 11, "2XL": 0 },
      }
    ),
    fabric: "100% Nylon Ripstop mờ trượt nước nhẹ (PFC-free)",
    gsm: 95,
    fit: "Relaxed layering mặc ngoài áo thun",
    origin: "TP. Hồ Chí Minh, Việt Nam",
    careGuide: [
      "Kéo khóa kín trước khi giặt nước lạnh chế độ nhẹ.",
      "Không dùng chất làm mềm để duy trì lớp trượt nước bề mặt.",
    ],
    highlights: [
      "Chắn gió và chống nắng nhẹ, có thể gấp gọn vào chính túi áo mang theo balo.",
      "Hai túi sườn khóa kéo giấu kín và túi trong đựng ví, điện thoại.",
    ],
    description:
      "Áo khoác gió siêu nhẹ chỉ khoảng 210 gram toàn áo, lớp vải lì không sột soạt mạnh, dùng linh hoạt cả mùa nắng lẫn mưa nhẹ.",
    sizeChart: TOP_SIZE_CHART,
    isNew: true,
    material: "nylon-ripstop",
    materialLabel: MATERIAL_LABELS["nylon-ripstop"],
    fitType: "relaxed",
    occasion: ["du-lich", "the-thao", "hang-ngay"],
    sport: ["chay-bo", "dap-xe", "di-bo"],
    fabricFeatures: ["truot-nuoc", "nhanh-kho", "thoang-khi", "it-nhan"],
    pattern: "tron",
    badges: ["moi", "pre-order"],
    isCombo: false,
    tierDiscounts: [{ minQty: 2, discountPercent: 10, label: "Mua 2 giảm thêm 10%" }],
    bundleIds: ["prod-at-dryfit", "prod-short-daily"],
    giftThreshold: STANDARD_GIFT_THRESHOLD,
    returnEligible: true,
    slaText: "Pre-order giao 3-5 ngày · Kho TP.HCM",
    maxPerOrder: 2,
    modelInfo: [
      {
        gender: "nam",
        heightCm: 177,
        weightKg: 73,
        measurements: "Ngực 97 · Eo 81 · Mông 96 cm",
        wearingSize: "L",
        note: "Khoác ngoài áo thun 220gsm thoải mái",
      },
      {
        gender: "nu",
        heightCm: 163,
        weightKg: 52,
        measurements: "Ngực 82 · Eo 64 · Mông 90 cm",
        wearingSize: "S",
        note: "Dáng rộng nhẹ dễ che nắng khi chạy xe",
      },
    ],
    careInstructions: SPORT_CARE,
    hasVideo: true,
    videoUrl: "/atmosphere/jacquard-loom-loop.webm",
    videoPoster: "/atmosphere/jacquard-loom-poster.jpg",
    faqs: [
      {
        question: "Áo khoác gió trượt nước có đi mưa lớn được không?",
        answer:
          "Lớp phủ PFC-free trượt nước mưa phùn và mưa rào nhẹ 10-15 phút, ưu tiên độ thoáng khí nên không thay thế áo mưa chuyên dụng khi mưa to kéo dài.",
      },
    ],
    wishlistCount: 241,
    salesRank: 18,
    createdAt: "2026-11-18T08:00:00+07:00",
    shopSizeChart: "top",
    collectionSlugs: ["chay-bo-tap-luyen", "soi-xanh-nexus"],
  },
];

export function getProductBySlug(slug: string): Product | undefined {
  return PRODUCTS.find((p) => p.slug === slug);
}

export function getProductById(id: string): Product | undefined {
  return PRODUCTS.find((p) => p.id === id);
}

export function getProductsByShop(shopId: string): Product[] {
  return PRODUCTS.filter((p) => p.shopId === shopId);
}

export function getProductsByCategory(categorySlug: CategorySlug): Product[] {
  return PRODUCTS.filter((p) => p.categorySlug === categorySlug);
}

export function getProductsByCollection(collectionSlug: string): Product[] {
  return PRODUCTS.filter((p) => p.collectionSlugs?.includes(collectionSlug));
}

export function matchesGenderFilter(product: Product, filter: "all" | GenderCode): boolean {
  if (filter === "all") return true;
  if (filter === "unisex") return product.gender === "unisex";
  return product.gender === filter || product.gender === "unisex";
}

export function getTotalStock(product: Product): number {
  return product.skus.reduce((sum, sku) => sum + sku.stock, 0);
}

export function getSku(
  product: Product,
  colorCode: string,
  size: SizeCode
): ProductSku | undefined {
  return product.skus.find((s) => s.colorCode === colorCode && s.size === size);
}
