import type { CategorySlug } from "./products";

export interface MediaAsset {
  src: string;
  alt: string;
  width: number;
  height: number;
}

export interface BannerMediaAsset extends MediaAsset {
  slug: string;
  title: string;
  caption: string;
  href: string;
}

export interface CategoryTileMedia extends MediaAsset {
  slug: CategorySlug | "ao-khoac";
  label: string;
  subtitle: string;
  href: string;
}

export interface VideoMediaAsset {
  src: string;
  poster: string;
  alt: string;
  width: number;
  height: number;
}

export const MEDIA_BANNERS: Record<
  "everyday" | "office" | "running" | "greenFiber" | "fabricCloseup" | "workshop",
  BannerMediaAsset
> = {
  everyday: {
    slug: "co-ban-hang-ngay",
    title: "Cơ bản hằng ngày",
    caption: "Áo thun cotton 220gsm, quần short cotton twill và đồ mặc mỗi ngày thoáng nhẹ.",
    href: "/#danh-ba-gian-hang",
    src: "/banners/campaign-everyday.jpg",
    alt: "Hai người mẫu nam nữ Việt Nam dạo phố nắng sáng trong trang phục áo thun cotton cơ bản trắng và xám đá",
    width: 1365,
    height: 768,
  },
  office: {
    slug: "cong-so-gon-gang",
    title: "Công sở gọn gàng",
    caption: "Áo polo pique cổ đứng, polo dệt kim mịn và quần jogger tech cho cả ngày làm việc.",
    href: "/#danh-ba-gian-hang",
    src: "/banners/campaign-office.jpg",
    alt: "Người mẫu nam mặc áo polo pique kem và người mẫu nữ mặc polo dệt kim đen trao đổi công việc tại văn phòng sáng",
    width: 1365,
    height: 768,
  },
  running: {
    slug: "chay-bo-tap-luyen",
    title: "Chạy bộ & Tập luyện",
    caption: "Áo Dry-Fit 145gsm siêu nhẹ, quần short 5 inch xẻ tà và tất thể thao khô nhanh.",
    href: "/#danh-ba-gian-hang",
    src: "/banners/campaign-running.jpg",
    alt: "Hai vận động viên nam nữ Việt Nam chạy bộ sáng sớm ven sông trong trang phục áo Dry-Fit xanh cổ vịt và quần short chạy bộ",
    width: 1365,
    height: 768,
  },
  greenFiber: {
    slug: "soi-xanh-nexus",
    title: "Sợi Xanh Nexus",
    caption: "Sợi bông chải kỹ, lanh tự nhiên, gỗ sồi Modal và sợi tre thân thiện làn da.",
    href: "/ve-nexusmart",
    src: "/banners/campaign-green-fiber.jpg",
    alt: "Quả bông cotton tự nhiên, cuộn chỉ thô và các cuộn vải lanh bông nhuộm màu trung tính trên bàn xưởng sáng nắng",
    width: 1365,
    height: 768,
  },
  fabricCloseup: {
    slug: "cong-nghe-vai",
    title: "Cận cảnh kết cấu sợi dệt",
    caption: "Soi rõ độ mịn mặt vải cotton compact 220gsm, pique mắt chim thoáng khí và dệt kim.",
    href: "/ve-nexusmart",
    src: "/banners/fabric-closeup.jpg",
    alt: "Cận cảnh cấu trúc sợi dệt vải cotton chải kỹ 220gsm trắng, vải pique mắt chim xanh đá và vải dệt kim than chì",
    width: 1365,
    height: 768,
  },
  workshop: {
    slug: "xuong-det-viet-nam",
    title: "Trực tiếp từ xưởng dệt Việt Nam",
    caption: "Bốn xưởng dệt đối tác tại Hải Phòng, Hà Nội, Đà Nẵng và TP.HCM kiểm vải từng lô.",
    href: "/ve-nexusmart",
    src: "/banners/vietnam-workshop.jpg",
    alt: "Không gian xưởng dệt may Việt Nam rộng thoáng đầy ánh sáng tự nhiên với hai thợ dệt kiểm tra chất lượng cuộn vải cotton",
    width: 1365,
    height: 768,
  },
};

export const CAMPAIGN_BANNERS: BannerMediaAsset[] = [
  MEDIA_BANNERS.everyday,
  MEDIA_BANNERS.office,
  MEDIA_BANNERS.running,
  MEDIA_BANNERS.greenFiber,
];

export const CATEGORY_TILES_MEDIA: Record<CategorySlug | "ao-khoac", CategoryTileMedia> = {
  "ao-thun": {
    slug: "ao-thun",
    label: "Áo thun",
    subtitle: "Cotton 220gsm · Dry-Fit · Oversize",
    href: "/san-pham?cat=ao-thun",
    src: "/models/model-ao-thun-trang.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo thun Cotton Compact 220gsm màu trắng tinh cổ tròn",
    width: 896,
    height: 1200,
  },
  "ao-polo": {
    slug: "ao-polo",
    label: "Áo polo",
    subtitle: "Pique mắt chim · Dệt kim công sở",
    href: "/san-pham?cat=ao-polo",
    src: "/models/model-polo-xanh-da.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo Polo Pique Cotton màu xanh đá đứng cạnh cửa kính sáng",
    width: 896,
    height: 1200,
  },
  "quan-short": {
    slug: "quan-short",
    label: "Quần short",
    subtitle: "Daily 7 inch · Chạy bộ · Linen",
    href: "/san-pham?cat=quan-short",
    src: "/models/model-short-daily-be.jpg",
    alt: "Người mẫu nam Việt Nam mặc quần Short Daily Cotton 7 inch màu be cát phối áo thun trắng",
    width: 896,
    height: 1200,
  },
  "quan-jogger": {
    slug: "quan-jogger",
    label: "Quần jogger",
    subtitle: "Nỉ chân cua 320gsm · Vải Tech",
    href: "/san-pham?cat=quan-jogger",
    src: "/models/model-jogger-terry-xam.jpg",
    alt: "Người mẫu nam Việt Nam mặc quần Jogger nỉ chân cua 320gsm màu xám muối tiêu tại phòng khách sáng",
    width: 896,
    height: 1200,
  },
  "do-lot": {
    slug: "do-lot",
    label: "Đồ lót",
    subtitle: "Combo 3 sợi Modal · Sợi tre",
    href: "/san-pham?cat=do-lot",
    src: "/models/model-mac-nha-modal.jpg",
    alt: "Người mẫu nam Việt Nam mặc đồ mặc nhà gọn gàng trong phòng ngủ sáng bên hộp combo 3 quần lót boxer sợi Modal",
    width: 896,
    height: 1200,
  },
  "tat-phu-kien": {
    slug: "tat-phu-kien",
    label: "Tất",
    subtitle: "Combo tất cổ trung · Tất chạy bộ",
    href: "/san-pham?cat=tat-phu-kien",
    src: "/models/model-tat-co-trung.jpg",
    alt: "Cận cảnh đôi tất cotton gân cổ trung màu xám đá phối giày sneaker trắng trên bậc bê tông sáng",
    width: 896,
    height: 1200,
  },
  "ao-khoac": {
    slug: "ao-khoac",
    label: "Áo khoác gió",
    subtitle: "Ripstop trượt nước 210g gấp gọn",
    href: "/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro",
    src: "/models/model-ao-khoac-gio.jpg",
    alt: "Người mẫu nam Việt Nam khoác áo gió nhẹ gấp gọn màu xám tro ngoài áo thun trắng trên phố",
    width: 896,
    height: 1200,
  },
};

export const CATEGORY_TILE_LIST: CategoryTileMedia[] = [
  CATEGORY_TILES_MEDIA["ao-thun"],
  CATEGORY_TILES_MEDIA["ao-polo"],
  CATEGORY_TILES_MEDIA["quan-short"],
  CATEGORY_TILES_MEDIA["quan-jogger"],
  CATEGORY_TILES_MEDIA["do-lot"],
  CATEGORY_TILES_MEDIA["tat-phu-kien"],
  CATEGORY_TILES_MEDIA["ao-khoac"],
];

export const WORKSHOP_MEDIA: MediaAsset & {
  greenFiber: MediaAsset;
  gallery: MediaAsset[];
  loomVideo: VideoMediaAsset;
} = {
  src: MEDIA_BANNERS.workshop.src,
  alt: MEDIA_BANNERS.workshop.alt,
  width: MEDIA_BANNERS.workshop.width,
  height: MEDIA_BANNERS.workshop.height,
  greenFiber: {
    src: MEDIA_BANNERS.greenFiber.src,
    alt: MEDIA_BANNERS.greenFiber.alt,
    width: MEDIA_BANNERS.greenFiber.width,
    height: MEDIA_BANNERS.greenFiber.height,
  },
  gallery: [
    {
      src: MEDIA_BANNERS.workshop.src,
      alt: MEDIA_BANNERS.workshop.alt,
      width: MEDIA_BANNERS.workshop.width,
      height: MEDIA_BANNERS.workshop.height,
    },
    {
      src: MEDIA_BANNERS.greenFiber.src,
      alt: MEDIA_BANNERS.greenFiber.alt,
      width: MEDIA_BANNERS.greenFiber.width,
      height: MEDIA_BANNERS.greenFiber.height,
    },
    {
      src: MEDIA_BANNERS.fabricCloseup.src,
      alt: MEDIA_BANNERS.fabricCloseup.alt,
      width: MEDIA_BANNERS.fabricCloseup.width,
      height: MEDIA_BANNERS.fabricCloseup.height,
    },
  ],
  loomVideo: {
    src: "/atmosphere/jacquard-loom-loop.webm",
    poster: "/atmosphere/jacquard-loom-poster.jpg",
    alt: "Cận cảnh khung dệt Jacquard đang đan sợi vải nhịp nhàng",
    width: 854,
    height: 480,
  },
};

export const FABRIC_MEDIA: MediaAsset & {
  closeups: MediaAsset[];
} = {
  src: MEDIA_BANNERS.fabricCloseup.src,
  alt: MEDIA_BANNERS.fabricCloseup.alt,
  width: MEDIA_BANNERS.fabricCloseup.width,
  height: MEDIA_BANNERS.fabricCloseup.height,
  closeups: [
    {
      src: "/banners/fabric-closeup.jpg",
      alt: "Cận cảnh cấu trúc sợi dệt vải cotton chải kỹ 220gsm trắng, vải pique mắt chim xanh đá và vải dệt kim than chì",
      width: 1365,
      height: 768,
    },
    {
      src: "/banners/campaign-green-fiber.jpg",
      alt: "Quả bông cotton tự nhiên, cuộn chỉ thô và các cuộn vải lanh bông nhuộm màu trung tính trên bàn xưởng sáng nắng",
      width: 1365,
      height: 768,
    },
    {
      src: "/atmosphere/cotton-twill-slate.jpg",
      alt: "Cận cảnh bề mặt vải cotton dệt chéo màu xanh đá sẫm",
      width: 1920,
      height: 1280,
    },
    {
      src: "/atmosphere/cotton-jersey-drape.webp",
      alt: "Nếp gấp mềm tự nhiên của tấm vải thun cotton jersey màu trung tính",
      width: 1520,
      height: 760,
    },
    {
      src: "/atmosphere/cotton-jersey-weave.webp",
      alt: "Bề mặt sợi dệt phẳng cận cảnh của vải cotton jersey",
      width: 1520,
      height: 760,
    },
    {
      src: "/atmosphere/cotton-boll-harvest.jpg",
      alt: "Quả bông cotton tự nhiên nở trắng trước khi thu hoạch",
      width: 1920,
      height: 1280,
    },
  ],
};

export const MODEL_SHOTS: Record<string, MediaAsset> = {
  "prod-at-trang": {
    src: "/models/model-ao-thun-trang.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo thun Cotton Compact 220gsm màu trắng tinh cổ tròn phối quần tây xám đậm",
    width: 896,
    height: 1200,
  },
  "prod-at-xamda": {
    src: "/models/model-ao-thun-xam-da.jpg",
    alt: "Người mẫu nữ Việt Nam mặc áo thun Cotton Compact 220gsm màu xám đá giữa sân gạch kiến trúc sáng",
    width: 896,
    height: 1200,
  },
  "prod-at-xanhthan": {
    src: "/models/model-ao-thun-xanh-than.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo thun Cotton Compact 220gsm màu xanh than phối quần kaki be trên phố",
    width: 896,
    height: 1200,
  },
  "prod-at-dryfit": {
    src: "/models/model-ao-thun-dryfit.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo thun thể thao Dry-Fit màu xanh cổ vịt thoáng khí ven hồ buổi sáng",
    width: 896,
    height: 1200,
  },
  "prod-at-oversize": {
    src: "/models/model-ao-thun-oversize.jpg",
    alt: "Người mẫu nữ Việt Nam mặc áo thun Oversize 260gsm màu than chì dáng rộng vai xuôi phối quần short sáng",
    width: 896,
    height: 1200,
  },
  "prod-polo-kem": {
    src: "/models/model-polo-kem.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo Polo Pique Cotton màu kem nhạt tại không gian văn phòng hiện đại",
    width: 896,
    height: 1200,
  },
  "prod-polo-xanhda": {
    src: "/models/model-polo-xanh-da.jpg",
    alt: "Người mẫu nam Việt Nam mặc áo Polo Pique Cotton màu xanh đá đứng cạnh cửa kính sáng",
    width: 896,
    height: 1200,
  },
  "prod-polo-detkim": {
    src: "/models/model-polo-det-kim-den.jpg",
    alt: "Người mẫu nữ Việt Nam mặc áo Polo dệt kim mịn cổ mở màu đen than phối quần tây xám nhạt",
    width: 896,
    height: 1200,
  },
  "prod-short-daily": {
    src: "/models/model-short-daily-be.jpg",
    alt: "Người mẫu nam Việt Nam mặc quần Short Daily Cotton 7 inch màu be cát phối áo thun trắng",
    width: 896,
    height: 1200,
  },
  "prod-short-run": {
    src: "/models/model-short-run-xam.jpg",
    alt: "Người mẫu nam chạy bộ buổi sáng mặc quần Short chạy bộ 5 inch màu xám đậm xẻ tà",
    width: 896,
    height: 1200,
  },
  "prod-short-linen": {
    src: "/models/model-short-linen-xam.jpg",
    alt: "Người mẫu nữ Việt Nam mặc quần Short Linen pha Cotton màu xám đá dây rút trên hiên đón nắng",
    width: 896,
    height: 1200,
  },
  "prod-jogger-terry": {
    src: "/models/model-jogger-terry-xam.jpg",
    alt: "Người mẫu nam Việt Nam mặc quần Jogger nỉ chân cua 320gsm màu xám muối tiêu tại phòng khách sáng",
    width: 896,
    height: 1200,
  },
  "prod-jogger-tech": {
    src: "/models/model-jogger-tech-den.jpg",
    alt: "Người mẫu nam Việt Nam sải bước ngoài phố mặc quần Jogger co giãn Tech màu đen than dáng gọn",
    width: 896,
    height: 1200,
  },
  "prod-dolot-boxer": {
    src: "/models/model-mac-nha-modal.jpg",
    alt: "Người mẫu nam Việt Nam mặc đồ mặc nhà gọn gàng trong phòng ngủ sáng bên hộp combo 3 quần lót boxer sợi Modal",
    width: 896,
    height: 1200,
  },
  "prod-dolot-brief": {
    src: "/models/model-mac-nha-bamboo.jpg",
    alt: "Bộ 3 quần lót brief sợi tre màu xanh đá, xám nhạt và xám đậm gấp gọn cạnh hộp tối giản và mẫu vải sợi tre",
    width: 896,
    height: 1200,
  },
  "prod-tat-cotrung": {
    src: "/models/model-tat-co-trung.jpg",
    alt: "Cận cảnh đôi tất cotton gân cổ trung màu xám đá phối giày sneaker trắng trên bậc bê tông sáng",
    width: 896,
    height: 1200,
  },
  "prod-tat-cothap": {
    src: "/models/model-tat-the-thao.jpg",
    alt: "Cận cảnh đôi tất thể thao cổ thấp màu trắng xám phối giày chạy bộ trên đường chạy buổi sáng",
    width: 896,
    height: 1200,
  },
  "prod-aokhoac-gio": {
    src: "/models/model-ao-khoac-gio.jpg",
    alt: "Người mẫu nam Việt Nam khoác áo gió nhẹ gấp gọn màu xám tro ngoài áo thun trắng trên phố",
    width: 896,
    height: 1200,
  },
};
