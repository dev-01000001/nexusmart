export interface AnnouncementItem {
  id: string;
  text: string;
  href: string;
  ctaLabel: string;
}

export interface MegaMenuLeaf {
  label: string;
  href: string;
  note?: string;
}

export interface MegaMenuColumn {
  id: string;
  title: string;
  items: MegaMenuLeaf[];
}

export interface MegaMenuGroup {
  id: "loai-do" | "hoan-canh" | "the-thao" | "bo-suu-tap";
  label: string;
  href: string;
  summary: string;
  columns: MegaMenuColumn[];
  featuredCollectionSlug?: string;
}

export interface TrendingKeyword {
  query: string;
  label: string;
  tag?: string;
}

export interface CollectionSubTab {
  id: string;
  label: string;
  description: string;
  productIds: string[];
}

export interface CollectionItem {
  slug: string;
  title: string;
  subtitle: string;
  seasonLabel: string;
  heroImage: string;
  fabricHighlight: string;
  benefitPoints: string[];
  productIds: string[];
  subTabs: CollectionSubTab[];
  careTips: string[];
}

export type JournalCategory = "phoi-do" | "chat-lieu" | "the-thao" | "bao-quan";

export interface JournalSection {
  heading: string;
  paragraphs: string[];
  featuredProductIds?: string[];
}

export interface JournalArticle {
  slug: string;
  title: string;
  category: JournalCategory;
  categoryLabel: string;
  summary: string;
  publishedAt: string;
  readMinutes: number;
  sampleViews: number; // dữ liệu mẫu giao diện
  coverImage: string;
  author: string;
  embeddedProductIds: string[];
  sections: JournalSection[];
}

export interface BrandMilestone {
  period: string;
  title: string;
  detail: string;
}

export interface PlatformChangelogEntry {
  version: string;
  date: string;
  title: string;
  changes: string[];
}

export interface CampaignStatementRow {
  id: string;
  date: string;
  description: string;
  shopName: string;
  ordersCount: number;
  amountVnd: number; // integer VND
}

export interface FabricTechCard {
  id: string;
  name: string;
  gsmRange: string;
  composition: string;
  benefit: string;
  productIds: string[];
}

export interface CampaignItem {
  slug: string;
  kind: "fabric-tech" | "csr";
  title: string;
  subtitle: string;
  heroImage: string;
  intro: string;
  productIds: string[];
  subTabs: CollectionSubTab[];
  fabricTechCards?: FabricTechCard[];
  csrProgress?: {
    targetVnd: number; // integer VND
    currentRaisedVnd: number; // integer VND, equals sum of statementRows
    ordersContributed: number;
    contributionRule: string;
    beneficiary: string;
    isSampleData: true;
    statementRows: CampaignStatementRow[];
  };
}

export type HelpTopicKey =
  "giao-hang" | "doi-tra-45-ngay" | "thanh-toan-vat" | "hoi-vien-xu" | "chon-size" | "khieu-nai";

export interface HelpFaqItem {
  id: string;
  topic: HelpTopicKey;
  scope: "san" | "gian-hang";
  question: string;
  answer: string;
}

export interface CareGuideEntry {
  id: string;
  fabricGroup: string;
  tempRule: string;
  dryingRule: string;
  avoidRule: string;
  tip: string;
}

export const ANNOUNCEMENT_BAR: AnnouncementItem[] = [
  {
    id: "ann-freeship",
    text: "Miễn phí vận chuyển toàn sàn từ 249.000₫ (gian hàng từ 199.000₫)",
    href: "/danh-muc",
    ctaLabel: "Mua sắm ngay",
  },
  {
    id: "ann-return45",
    text: "Đổi trả miễn phí 45 ngày, bưu tá đổi size 2 chiều tận nơi",
    href: "/tro-giup",
    ctaLabel: "Xem chính sách",
  },
  {
    id: "ann-friday",
    text: "Hội viên Nexus: hoàn tới 6% Xu, nhân đôi Xu vào mỗi Thứ Sáu",
    href: "/hoi-vien",
    ctaLabel: "Xem hạng hội viên",
  },
];

export const MEGA_MENU_GROUPS: MegaMenuGroup[] = [
  {
    id: "loai-do",
    label: "Loại đồ",
    href: "/danh-muc",
    summary: "Đồ basic may trực tiếp tại 4 xưởng dệt Việt Nam theo định lượng vải minh bạch.",
    featuredCollectionSlug: "co-ban-hang-ngay",
    columns: [
      {
        id: "col-ao",
        title: "Áo cơ bản",
        items: [
          { label: "Áo thun Cotton 220gsm", href: "/danh-muc?cat=ao-thun&material=cotton-compact" },
          {
            label: "Áo thun Thể thao Dry-Fit",
            href: "/danh-muc?cat=ao-thun&material=polyester-dryfit",
          },
          { label: "Áo thun Oversize 260gsm", href: "/danh-muc?cat=ao-thun&fit=oversize" },
          { label: "Áo Polo Pique & Dệt kim", href: "/danh-muc?cat=ao-polo" },
          {
            label: "Áo khoác gió gấp gọn",
            href: "/danh-muc?cat=tat-phu-kien&material=nylon-ripstop",
          },
        ],
      },
      {
        id: "col-quan",
        title: "Quần mặc ngày & tập",
        items: [
          { label: "Quần Short Daily 7 inch", href: "/danh-muc?cat=quan-short&occasion=hang-ngay" },
          { label: "Quần Short Chạy bộ 5 inch", href: "/danh-muc?cat=quan-short&sport=chay-bo" },
          {
            label: "Quần Short Linen pha Cotton",
            href: "/danh-muc?cat=quan-short&material=linen-cotton",
          },
          {
            label: "Quần Jogger Nỉ chân cua",
            href: "/danh-muc?cat=quan-jogger&material=french-terry",
          },
          {
            label: "Quần Jogger Tech 4 chiều",
            href: "/danh-muc?cat=quan-jogger&material=nylon-tech",
          },
        ],
      },
      {
        id: "col-dolot",
        title: "Đồ lót & Tất",
        items: [
          { label: "Combo 3 Boxer Sợi Modal", href: "/san-pham/combo-3-quan-lot-boxer-modal" },
          { label: "Combo 3 Brief Sợi tre", href: "/san-pham/combo-3-quan-lot-brief-soi-tre" },
          { label: "Combo 5 Tất cổ trung", href: "/san-pham/combo-5-doi-tat-cotton-gan-co-trung" },
          { label: "Combo 4 Tất thể thao", href: "/san-pham/combo-4-doi-tat-the-thao-co-thap" },
        ],
      },
    ],
  },
  {
    id: "hoan-canh",
    label: "Hoàn cảnh mặc",
    href: "/danh-muc?occasion=hang-ngay",
    summary: "Chọn nhanh chất liệu và phom dáng phù hợp từng lịch trình trong tuần.",
    featuredCollectionSlug: "cong-so-gon-gang",
    columns: [
      {
        id: "col-nhucau",
        title: "Theo lịch trình",
        items: [
          {
            label: "Mặc hằng ngày",
            href: "/danh-muc?occasion=hang-ngay",
            note: "Cotton 220-260gsm",
          },
          {
            label: "Công sở gọn gàng",
            href: "/danh-muc?occasion=cong-so",
            note: "Polo Pique & Dệt kim",
          },
          {
            label: "Du lịch & dã ngoại",
            href: "/danh-muc?occasion=du-lich",
            note: "Linen & Ripstop nhẹ",
          },
          {
            label: "Mặc nhà thoáng mát",
            href: "/danh-muc?occasion=mac-nha",
            note: "Modal & French Terry",
          },
        ],
      },
      {
        id: "col-gioitinh",
        title: "Theo đối tượng",
        items: [
          { label: "Tủ đồ Nam", href: "/danh-muc?gender=nam" },
          { label: "Tủ đồ Nữ & Unisex", href: "/danh-muc?gender=nu" },
          { label: "Dáng Unisex dễ phối", href: "/danh-muc?gender=unisex" },
          { label: "Hàng Outlet giá tốt", href: "/bo-suu-tap/san-outlet" },
        ],
      },
    ],
  },
  {
    id: "the-thao",
    label: "Thể thao",
    href: "/bo-suu-tap/chay-bo-tap-luyen",
    summary: "Sợi nhẹ 118-145gsm thoát ẩm nhanh, co giãn 4 chiều và may phẳng chống cọ xát.",
    featuredCollectionSlug: "chay-bo-tap-luyen",
    columns: [
      {
        id: "col-mon",
        title: "Theo môn vận động",
        items: [
          { label: "Chạy bộ ngoài trời", href: "/danh-muc?sport=chay-bo" },
          { label: "Tập gym cường độ cao", href: "/danh-muc?sport=gym" },
          { label: "Cầu lông & sân trong nhà", href: "/danh-muc?sport=cau-long" },
          { label: "Đạp xe & chắn gió", href: "/danh-muc?sport=dap-xe" },
          { label: "Đi bộ hằng ngày", href: "/danh-muc?sport=di-bo" },
        ],
      },
      {
        id: "col-congnghe",
        title: "Tính năng vải",
        items: [
          { label: "Nhanh khô thoát ẩm", href: "/danh-muc?feature=nhanh-kho" },
          { label: "Co giãn 4 chiều", href: "/danh-muc?feature=co-gian-4-chieu" },
          { label: "Trượt nước nhẹ PFC-free", href: "/danh-muc?feature=truot-nuoc" },
          { label: "Khám phá Công nghệ vải", href: "/chien-dich/cong-nghe-vai" },
        ],
      },
    ],
  },
  {
    id: "bo-suu-tap",
    label: "Bộ sưu tập & Tạp chí",
    href: "/bo-suu-tap",
    summary: "Bộ sưu tập theo mùa, chiến dịch cộng đồng Sợi Xanh Nexus và Sổ tay vải.",
    featuredCollectionSlug: "soi-xanh-nexus",
    columns: [
      {
        id: "col-bst",
        title: "Bộ sưu tập nổi bật",
        items: [
          { label: "Cơ bản hằng ngày", href: "/bo-suu-tap/co-ban-hang-ngay" },
          { label: "Công sở gọn gàng", href: "/bo-suu-tap/cong-so-gon-gang" },
          { label: "Chạy bộ & Tập luyện", href: "/bo-suu-tap/chay-bo-tap-luyen" },
          { label: "Sợi Xanh Nexus (CSR)", href: "/chien-dich/soi-xanh-nexus" },
          { label: "Góc săn Outlet", href: "/bo-suu-tap/san-outlet" },
        ],
      },
      {
        id: "col-noidung",
        title: "Khám phá & Dịch vụ",
        items: [
          { label: "Sổ tay vải & Phối đồ", href: "/tap-chi" },
          { label: "Về NexusMart & Xưởng dệt", href: "/ve-nexusmart" },
          { label: "Hội viên Nexus", href: "/hoi-vien" },
          { label: "Thẻ quà tặng & Ưu đãi .edu", href: "/the-qua-tang" },
          { label: "Tra cứu đơn hàng", href: "/tra-cuu-don-hang" },
          { label: "Trung tâm trợ giúp", href: "/tro-giup" },
        ],
      },
    ],
  },
];

export const TRENDING_KEYWORDS: TrendingKeyword[] = [
  { query: "ao thun 220gsm", label: "Áo thun 220gsm", tag: "Bán chạy" },
  { query: "ao phong", label: "Áo phông cotton", tag: "Tìm nhiều" },
  { query: "quan short chay bo", label: "Quần short chạy bộ" },
  { query: "polo pique", label: "Áo polo pique" },
  { query: "do lot modal", label: "Đồ lót sợi modal" },
  { query: "jogger chan cua", label: "Jogger nỉ chân cua" },
  { query: "tat cotton", label: "Combo tất cotton" },
  { query: "ao khoac gio", label: "Áo khoác gió trượt nước", tag: "Mới" },
];

export const COLLECTIONS: CollectionItem[] = [
  {
    slug: "co-ban-hang-ngay",
    title: "Cơ bản hằng ngày",
    subtitle:
      "Những món đồ mặc lặp lại mỗi tuần từ Cotton Compact 220gsm, Heavyweight 260gsm và Modal.",
    seasonLabel: "BST Thường nhật 2026",
    heroImage: "/banners/campaign-everyday.jpg",
    fabricHighlight:
      "Cotton chải kỹ Compact 220-260gsm, chống co rút trước cắt rập và giữ cổ áo phẳng.",
    benefitPoints: [
      "Bảng màu trung tính dễ phối lẫn giữa áo thun, quần short và jogger.",
      "Áp dụng giảm thêm theo tầng: mua 2 giảm 10%, mua 3 giảm 15%.",
      "Hỗ trợ đổi size 2 chiều tận nơi miễn phí trong 45 ngày.",
    ],
    productIds: [
      "prod-at-trang",
      "prod-at-xamda",
      "prod-at-xanhthan",
      "prod-at-oversize",
      "prod-short-daily",
      "prod-jogger-terry",
      "prod-dolot-boxer",
      "prod-tat-cotrung",
    ],
    subTabs: [
      {
        id: "all",
        label: "Tất cả mẫu",
        description: "Đủ bộ áo, quần, đồ lót và tất cho lịch trình 7 ngày gọn gàng.",
        productIds: [
          "prod-at-trang",
          "prod-at-xamda",
          "prod-at-xanhthan",
          "prod-at-oversize",
          "prod-short-daily",
          "prod-jogger-terry",
          "prod-dolot-boxer",
          "prod-tat-cotrung",
        ],
      },
      {
        id: "cotton-220",
        label: "Cotton 220-260gsm",
        description: "Dày dặn không lộ da, bề mặt xử lý enzyme giảm xù lông.",
        productIds: ["prod-at-trang", "prod-at-xamda", "prod-at-xanhthan", "prod-at-oversize"],
      },
      {
        id: "essentials",
        label: "Quần, Đồ lót & Tất",
        description: "Quần short twill, jogger nỉ chân cua, đồ lót Modal và tất nối mũi phẳng.",
        productIds: [
          "prod-short-daily",
          "prod-jogger-terry",
          "prod-dolot-boxer",
          "prod-tat-cotrung",
        ],
      },
    ],
    careTips: [
      "Lộn trái áo cotton trước khi giặt máy chế độ dưới 30 độ C để giữ bề mặt vải mịn.",
      "Phơi ngang thân áo heavyweight 260gsm thay vì treo móc vai mỏng để tránh chảy vai.",
      "Cho đồ lót sợi Modal vào túi giặt lưới để kéo dài tuổi thọ cạp chun.",
    ],
  },
  {
    slug: "cong-so-gon-gang",
    title: "Công sở gọn gàng",
    subtitle:
      "Thay thế sơ mi cứng nhắc bằng áo Polo Pique mắt chim, Polo dệt kim mịn và quần ít nhăn.",
    seasonLabel: "BST Công sở 2026",
    heroImage: "/banners/campaign-office.jpg",
    fabricHighlight: "Dệt mắt chim Pique 215gsm thoáng khí kết hợp Viscose dệt kim mịn đứng cổ.",
    benefitPoints: [
      "Ít nhăn suốt 8 tiếng ngồi làm việc và di chuyển giữa các cuộc họp.",
      "Phom Regular và Tailored tôn vai, không lộ đường chỉ cộm.",
      "Hỗ trợ xuất hóa đơn VAT điện tử cho đơn đồng phục nhóm hoặc công ty.",
    ],
    productIds: [
      "prod-polo-kem",
      "prod-polo-xanhda",
      "prod-polo-detkim",
      "prod-at-trang",
      "prod-short-linen",
      "prod-jogger-tech",
      "prod-tat-cotrung",
      "prod-aokhoac-gio",
    ],
    subTabs: [
      {
        id: "all",
        label: "Trọn bộ công sở",
        description: "8 thiết kế chủ lực cân bằng lịch sự và sự thoải mái suốt tuần làm việc.",
        productIds: [
          "prod-polo-kem",
          "prod-polo-xanhda",
          "prod-polo-detkim",
          "prod-at-trang",
          "prod-short-linen",
          "prod-jogger-tech",
          "prod-tat-cotrung",
          "prod-aokhoac-gio",
        ],
      },
      {
        id: "polo",
        label: "Áo Polo & Áo lót trong",
        description:
          "Cổ rib 1:1 không quăn mép, polo dệt kim máy phẳng và áo thun 220gsm mặc trong blazer.",
        productIds: ["prod-polo-kem", "prod-polo-xanhda", "prod-polo-detkim", "prod-at-trang"],
      },
      {
        id: "bottoms",
        label: "Quần, Tất & Áo khoác",
        description:
          "Jogger Tech bề mặt lì, short Linen cuối tuần, tất cổ trung và áo gió nhẹ đi đường.",
        productIds: [
          "prod-short-linen",
          "prod-jogger-tech",
          "prod-tat-cotrung",
          "prod-aokhoac-gio",
        ],
      },
    ],
    careTips: [
      "Cài nút cổ áo polo trước khi giặt để nẹp cúc thẳng và không bị vặn.",
      "Phơi phẳng áo polo dệt kim trên lưới thay vì treo móc đứng.",
      "Kéo kín khóa túi quần Jogger Tech trước khi cho vào lồng giặt.",
    ],
  },
  {
    slug: "chay-bo-tap-luyen",
    title: "Chạy bộ & Tập luyện",
    subtitle: "Hệ đồ thể thao siêu nhẹ 95-145gsm từ sợi tái chế Micro-mesh và Polyamide 4 chiều.",
    seasonLabel: "BST Vận động 2026",
    heroImage: "/banners/campaign-running.jpg",
    fabricHighlight:
      "Micro-mesh 145gsm khô nhanh sau 45 phút phơi gió cùng đường may chập phẳng chống cọ xát.",
    benefitPoints: [
      "Thoát mồ hôi tức thời cho chạy bộ sáng, tập gym và cầu lông.",
      "Áo khoác Ripstop 95gsm gấp gọn vào túi áo mang theo mọi buổi chạy.",
      "Giao nhanh 2-4 giờ nội thành TP.HCM và Hà Nội cho đồ tập cần gấp.",
    ],
    productIds: [
      "prod-at-dryfit",
      "prod-short-run",
      "prod-aokhoac-gio",
      "prod-jogger-tech",
      "prod-jogger-terry",
      "prod-tat-cothap",
      "prod-short-daily",
      "prod-dolot-brief",
    ],
    subTabs: [
      {
        id: "all",
        label: "Tất cả đồ tập",
        description: "Trang phục đầy đủ từ lớp sát da thoáng khí đến áo khoác chắn gió nhẹ.",
        productIds: [
          "prod-at-dryfit",
          "prod-short-run",
          "prod-aokhoac-gio",
          "prod-jogger-tech",
          "prod-jogger-terry",
          "prod-tat-cothap",
          "prod-short-daily",
          "prod-dolot-brief",
        ],
      },
      {
        id: "running",
        label: "Chạy bộ 5K-21K",
        description: "Áo 145gsm, quần 118gsm xẻ tà, tất cổ thấp có gờ bảo vệ gót và áo gió nhẹ.",
        productIds: ["prod-at-dryfit", "prod-short-run", "prod-tat-cothap", "prod-aokhoac-gio"],
      },
      {
        id: "outerwear",
        label: "Khởi động & Hồi phục",
        description:
          "Quần jogger co giãn 4 chiều, nỉ chân cua 320gsm, short mặc sau tập và đồ lót sợi tre.",
        productIds: [
          "prod-jogger-tech",
          "prod-jogger-terry",
          "prod-short-daily",
          "prod-dolot-brief",
        ],
      },
    ],
    careTips: [
      "Không dùng nước xả vải cho đồ Dry-Fit và Ripstop để giữ cấu trúc thoát ẩm và trượt nước.",
      "Giặt nước lạnh ngay sau buổi chạy giúp hạn chế mùi và tăng độ bền sợi elastane.",
    ],
  },
  {
    slug: "soi-xanh-nexus",
    title: "Sợi Xanh Nexus",
    subtitle:
      "Chất liệu sợi thực vật và sợi tái chế, đóng góp 10% doanh thu cho tủ áo đồng phục vùng cao.",
    seasonLabel: "Chiến dịch Cộng đồng",
    heroImage: "/banners/campaign-green-fiber.jpg",
    fabricHighlight:
      "Sợi tre Bamboo, sợi gỗ sồi Modal, lanh Linen và Polyester tái chế truy xuất lô.",
    benefitPoints: [
      "Mỗi sản phẩm trong BST tự động trích 10% giá trị vào quỹ Sợi Xanh Nexus.",
      "Bảng tổng hợp đóng góp công khai theo từng đợt và từng xưởng dệt tham gia.",
      "Đóng gói bằng túi giấy tái chế và túi phân hủy sinh học.",
    ],
    productIds: [
      "prod-at-trang",
      "prod-at-xamda",
      "prod-at-dryfit",
      "prod-polo-kem",
      "prod-short-linen",
      "prod-dolot-boxer",
      "prod-dolot-brief",
      "prod-aokhoac-gio",
    ],
    subTabs: [
      {
        id: "all",
        label: "Toàn bộ BST Sợi Xanh",
        description: "8 sản phẩm trích 10% doanh thu cho chương trình áo ấm và đồng phục vùng cao.",
        productIds: [
          "prod-at-trang",
          "prod-at-xamda",
          "prod-at-dryfit",
          "prod-polo-kem",
          "prod-short-linen",
          "prod-dolot-boxer",
          "prod-dolot-brief",
          "prod-aokhoac-gio",
        ],
      },
      {
        id: "plant-fiber",
        label: "Sợi thực vật (Lanh, Tre, Modal)",
        description: "Mát tự nhiên, tiết kiệm nước nhuộm và êm dịu với da nhạy cảm.",
        productIds: ["prod-short-linen", "prod-dolot-boxer", "prod-dolot-brief", "prod-polo-kem"],
      },
      {
        id: "recycled",
        label: "Cotton chải kỹ & Sợi tái chế",
        description:
          "Cotton Compact 220gsm, Polyester tái chế Micro-mesh và lớp trượt nước không PFC.",
        productIds: ["prod-at-trang", "prod-at-xamda", "prod-at-dryfit", "prod-aokhoac-gio"],
      },
    ],
    careTips: [
      "Giặt nước mát thay vì nước nóng giúp tiết kiệm điện năng và bảo vệ sợi thực vật.",
      "Phơi gió tự nhiên giảm 70% lượng phát thải so với sấy máy.",
    ],
  },
  {
    slug: "san-outlet",
    title: "Góc săn Outlet",
    subtitle: "Các mẫu lẻ màu và sản phẩm cuối đợt sản xuất với mức giá xưởng tiết kiệm tới 22%.",
    seasonLabel: "Ưu đãi lẻ size",
    heroImage: "/banners/fabric-closeup.jpg",
    fabricHighlight:
      "Giữ nguyên chuẩn kiểm vải 3 bước của xưởng dệt, số lượng có hạn theo từng màu và size.",
    benefitPoints: [
      "Giá giảm sâu trực tiếp trên từng màu và size, không cần sưu tầm voucher phức tạp.",
      "Hiển thị rõ quy định sản phẩm Outlet không áp dụng đổi trả.",
    ],
    productIds: ["prod-short-run", "prod-at-xanhthan"],
    subTabs: [
      {
        id: "all",
        label: "Tất cả mẫu Outlet",
        description: "Kiểm tra kỹ bảng size theo xưởng trước khi chốt đơn Outlet.",
        productIds: ["prod-short-run", "prod-at-xanhthan"],
      },
    ],
    careTips: [
      "Kiểm tra số đo chiều cao, cân nặng bằng công cụ gợi ý size trước khi đặt mẫu Outlet.",
    ],
  },
];

export const JOURNAL_ARTICLES: JournalArticle[] = [
  {
    slug: "cach-chon-dinh-luong-gsm-ao-thun-theo-mua",
    title: "Cách chọn định lượng 145gsm, 220gsm và 260gsm",
    category: "chat-lieu",
    categoryLabel: "Chất liệu vải",
    summary:
      "Hiểu đúng con số GSM trên nhãn áo giúp bạn chọn đúng chiếc áo mát khi chạy bộ, đứng dáng khi đi làm và không bị lộ da với màu trắng.",
    publishedAt: "2026-11-16T09:00:00+07:00",
    readMinutes: 4,
    sampleViews: 1420,
    coverImage: "/banners/fabric-closeup.jpg",
    author: "Ban biên tập Sổ tay vải NexusMart",
    embeddedProductIds: ["prod-at-dryfit", "prod-at-trang", "prod-at-oversize"],
    sections: [
      {
        heading: "GSM là gì và vì sao quan trọng hơn cảm giác dày mỏng bằng mắt?",
        paragraphs: [
          "GSM (Gram per Square Meter) đo khối lượng tính bằng gram trên mỗi mét vuông vải. Hai chiếc áo nhìn giống nhau nhưng khác định lượng sẽ cho trải nghiệm thoáng khí, độ đứng vai và độ bền cổ áo rất khác nhau.",
          "Với thời tiết nóng ẩm ở Việt Nam, mức 140-155gsm phù hợp vận động đổ mồ hôi nhiều, mức 210-230gsm tối ưu cho áo thun cơ bản mặc đi làm, còn từ 250gsm trở lên dành cho người thích phom áo oversize đứng dáng.",
        ],
        featuredProductIds: ["prod-at-dryfit"],
      },
      {
        heading: "Ba mức GSM thực tế từ xưởng dệt NexusMart",
        paragraphs: [
          "Nhóm 145gsm Dry-Fit Micro-mesh từ Kho Basic Sài Gòn ưu tiên thoát hơi nhanh và khô sau 45 phút phơi gió. Nhóm 220gsm Cotton Compact của Xưởng Dệt Bạch Đằng giải quyết triệt để nỗi lo áo trắng bị mỏng lộ nội y.",
          "Nếu bạn thích chiếc áo có độ rủ nặng tay, che khuyết điểm phần thân trên tốt, bản Heavyweight 260gsm dệt sợi se đôi sẽ giữ đường vai rõ nét suốt cả ngày.",
        ],
        featuredProductIds: ["prod-at-trang", "prod-at-oversize"],
      },
    ],
  },
  {
    slug: "huong-dan-chon-size-theo-3-dang-nguoi-va-form-xuong",
    title: "Chọn size chuẩn theo 3 dáng người và 4 xưởng",
    category: "phoi-do",
    categoryLabel: "Phối đồ & Size",
    summary:
      "Cùng một cân nặng 70 kg nhưng người dáng gọn, cân đối và đầy đặn sẽ cần cỡ áo khác nhau giữa xưởng Bạch Đằng và Dệt Kim Hà Nội.",
    publishedAt: "2026-11-12T10:30:00+07:00",
    readMinutes: 5,
    sampleViews: 2180,
    coverImage: "/banners/campaign-everyday.jpg",
    author: "Tổ kiểm định rập NexusMart",
    embeddedProductIds: ["prod-polo-kem", "prod-polo-detkim", "prod-jogger-terry"],
    sections: [
      {
        heading: "Đừng chỉ dựa vào cân nặng: 3 dáng người quyết định biên độ chọn cỡ",
        paragraphs: [
          "Khi lưu hồ sơ số đo trên NexusMart (chiều cao, cân nặng và dáng người Gầy / Vừa / Đầy), hệ thống sẽ tự đối chiếu vào bảng thông số rập riêng của từng gian hàng thay vì dùng một bảng chung.",
          "Người có vòng vai rộng hoặc phần bụng đầy nên ưu tiên chọn theo số đo vòng ngực/eo và cộng thêm 1 bậc size đối với dòng dệt kim ôm gọn như Dệt Kim Hà Nội.",
        ],
        featuredProductIds: ["prod-polo-detkim"],
      },
      {
        heading: "Đổi size 2 chiều tận nơi nếu vẫn chưa ưng phom",
        paragraphs: [
          "Mọi sản phẩm nguyên giá đều được đổi trả miễn phí trong 45 ngày. Khi tạo yêu cầu đổi size trên trang đơn hàng, bưu tá sẽ mang cỡ mới đến đổi trực tiếp tại địa chỉ của bạn.",
        ],
        featuredProductIds: ["prod-polo-kem", "prod-jogger-terry"],
      },
    ],
  },
  {
    slug: "bo-do-chay-bo-5k-khong-co-xat-va-nhanh-kho",
    title: "Bộ đồ chạy bộ 5K gọn nhẹ, nhanh khô",
    category: "the-thao",
    categoryLabel: "Thể thao",
    summary:
      "Ba món đồ dưới 500 gram tổng trọng lượng giúp buổi chạy sáng thoải mái từ km đầu tiên đến khi về tới nhà.",
    publishedAt: "2026-11-08T07:15:00+07:00",
    readMinutes: 3,
    sampleViews: 965,
    coverImage: "/banners/campaign-running.jpg",
    author: "Câu lạc bộ Chạy bộ Nexus",
    embeddedProductIds: ["prod-at-dryfit", "prod-short-run", "prod-tat-cothap", "prod-aokhoac-gio"],
    sections: [
      {
        heading: "Chọn đường may phẳng và vải dưới 150gsm",
        paragraphs: [
          "Cọ xát vùng nách và mặt trong đùi là nguyên nhân phổ biến nhất gây rát da khi chạy quá 5 km. Áo Dry-Fit 145gsm may chập phẳng kết hợp quần short 5 inch 118gsm xẻ tà giải phóng hoàn toàn bước sải chân.",
          "Đừng bỏ qua đôi tất cổ thấp có gờ đệm sau gót để ngăn trượt tất và phồng rộp mu bàn chân.",
        ],
        featuredProductIds: ["prod-at-dryfit", "prod-short-run", "prod-tat-cothap"],
      },
      {
        heading: "Lớp khoác gió 210 gram cho sáng sớm sương lạnh",
        paragraphs: [
          "Áo khoác gió Ripstop Xám tro có thể gấp gọn ngay vào túi sườn khi mặt trời lên cao, đồng thời trượt nước mưa phùn nhẹ.",
        ],
        featuredProductIds: ["prod-aokhoac-gio"],
      },
    ],
  },
  {
    slug: "meo-giat-ui-giu-co-ao-cotton-va-soi-linen-ben-dep",
    title: "Mẹo giặt ủi giữ áo cotton và linen bền màu",
    category: "bao-quan",
    categoryLabel: "Bảo quản",
    summary:
      "Bốn thói quen nhỏ khi giặt máy, phơi và dùng túi giặt lưới giúp quần áo cơ bản giữ đúng số đo rập như ngày đầu.",
    publishedAt: "2026-11-02T14:00:00+07:00",
    readMinutes: 4,
    sampleViews: 1310,
    coverImage: "/banners/campaign-green-fiber.jpg",
    author: "Ban biên tập Sổ tay vải NexusMart",
    embeddedProductIds: ["prod-short-linen", "prod-dolot-boxer", "prod-tat-cotrung"],
    sections: [
      {
        heading: "Vì sao nước xả vải không tốt cho mọi loại sợi?",
        paragraphs: [
          "Nước xả vải phủ một lớp làm mềm lên bề mặt sợi, rất có hại với vải thể thao Dry-Fit và lớp trượt nước Ripstop vì làm bít lỗ thoáng khí. Với cotton và linen, chỉ cần giặt nước mát dưới 30 độ C và giũ phẳng ngay sau khi giặt.",
        ],
        featuredProductIds: ["prod-short-linen"],
      },
      {
        heading: "Túi giặt lưới cho đồ lót Modal và tất cotton",
        paragraphs: [
          "Cho đồ lót sợi gỗ sồi Modal, sợi tre và tất vào túi giặt lưới giúp cạp chun không bị kéo giãn bởi lực xoáy của quần áo dày như quần jeans hay jogger.",
        ],
        featuredProductIds: ["prod-dolot-boxer", "prod-tat-cotrung"],
      },
    ],
  },
];

export const BRAND_STORY_PILLARS = [
  {
    id: "pillar-direct",
    title: "Kết nối thẳng 4 xưởng dệt Việt Nam",
    detail:
      "Mỗi gian hàng trên NexusMart là một xưởng dệt may có địa chỉ thực, công khai nguồn sợi, định lượng GSM và quy trình kiểm tra chất lượng trước khi xuất xưởng.",
  },
  {
    id: "pillar-price",
    title: "Giá niêm yết rõ ràng và đổi trả miễn phí 45 ngày",
    detail:
      "Không nâng giá ảo trước khuyến mãi, cập nhật còn hàng theo từng màu và size, cùng bưu tá đổi size 2 chiều tận nơi trong 45 ngày.",
  },
  {
    id: "pillar-fit",
    title: "Hồ sơ số đo chung cho nhiều gian hàng",
    detail:
      "Chỉ cần lưu chiều cao, cân nặng và dáng người một lần; hệ thống tự quy đổi theo bảng thông số rập riêng của từng xưởng để giảm lệch cỡ.",
  },
];

export const BRAND_TIMELINE: BrandMilestone[] = [
  {
    period: "Quý 2 / 2023",
    title: "Khởi động mạng lưới 2 xưởng dệt đầu tiên",
    detail:
      "Hợp tác cùng Xưởng Dệt Bạch Đằng (Hải Phòng) và Dệt Kim Hà Nội xây dựng tiêu chuẩn định lượng vải 220gsm và nối mũi tất liền mạch.",
  },
  {
    period: "Quý 1 / 2024",
    title: "Mở rộng kho miền Trung và miền Nam",
    detail:
      "Đón Sợi Tự Nhiên Đà Nẵng và Kho Basic Sài Gòn tham gia, hình thành mạng lưới giao hàng từ 3 miền phủ đủ 34 tỉnh thành Việt Nam.",
  },
  {
    period: "Quý 3 / 2025",
    title: "Ra mắt chương trình Hội viên Nexus và Sợi Xanh Nexus",
    detail:
      "Áp dụng cơ chế tích Xu dùng chéo mọi gian hàng và trích 10% doanh thu BST Sợi Xanh cho quỹ đồng phục học sinh vùng cao.",
  },
  {
    period: "Quý 4 / 2026",
    title: "Chuẩn hóa đổi trả 45 ngày và giao hàng 34 tỉnh thành",
    detail:
      "Triển khai đổi size 2 chiều tận nơi, xuất hóa đơn VAT điện tử theo từng xưởng và trợ lý chọn size theo số đo cá nhân.",
  },
];

export const PLATFORM_CHANGELOG: PlatformChangelogEntry[] = [
  {
    version: "v2.4",
    date: "2026-11-20",
    title: "Mở rộng tiện ích chọn đồ cơ bản & Hội viên Nexus",
    changes: [
      "Phân nhóm danh mục theo nhu cầu mặc và nâng cấp tìm kiếm hỗ trợ gợi ý sản phẩm tức thời.",
      "Bổ sung chọn màu và thêm nhanh vào giỏ ngay trên thẻ sản phẩm, hỗ trợ chọn cỡ riêng từng món cho combo.",
      "Ra mắt chương trình Hội viên Nexus 4 hạng, Thẻ quà tặng dùng toàn sàn, ưu đãi sinh viên .edu và Tra cứu đơn hàng nhanh.",
    ],
  },
  {
    version: "v2.1",
    date: "2026-10-28",
    title: "Nâng chuẩn đổi trả 45 ngày & giao nhanh nội thành",
    changes: [
      "Nâng thời hạn đổi trả tiêu chuẩn lên 45 ngày (tối đa 3 lần), hỗ trợ bưu tá đổi size 2 chiều tại nhà.",
      "Thêm tùy chọn xuất hóa đơn VAT duyệt bản nháp 24 giờ và ghi chú viết thiệp/túi quà tại bước thanh toán.",
    ],
  },
  {
    version: "v2.0",
    date: "2026-09-15",
    title: "Mở rộng giao hàng toàn quốc đủ 34 tỉnh thành",
    changes: [
      "Cập nhật đầy đủ danh sách 34 tỉnh, thành phố và phường, xã giúp nhập địa chỉ giao hàng nhanh hơn.",
      "Cập nhật tình trạng còn hàng theo màu và size tại từng kho xưởng cùng 3 hình thức thanh toán: COD, chuyển khoản VietQR và Ví NexusPay.",
    ],
  },
];

export const CAMPAIGNS: CampaignItem[] = [
  {
    slug: "cong-nghe-vai",
    kind: "fabric-tech",
    title: "Công nghệ vải NexusMart",
    subtitle:
      "Bốn nền tảng chất liệu được 4 xưởng dệt chọn lọc cho khí hậu nhiệt đới ẩm và nhịp sinh hoạt hằng ngày.",
    heroImage: "/banners/fabric-closeup.jpg",
    intro:
      "Thay vì gọi tên mỹ miều, NexusMart công bố thẳng thành phần sợi, định lượng GSM và tính năng đo được của từng dòng vải để bạn chọn đúng chiếc áo mình cần.",
    productIds: [
      "prod-at-trang",
      "prod-at-dryfit",
      "prod-polo-kem",
      "prod-short-linen",
      "prod-jogger-tech",
      "prod-dolot-boxer",
      "prod-dolot-brief",
      "prod-aokhoac-gio",
    ],
    subTabs: [
      {
        id: "cotton-compact",
        label: "Cotton Compact 220gsm",
        description: "Sợi chải kỹ xử lý enzyme, mịn bề mặt và chống co rút.",
        productIds: ["prod-at-trang", "prod-at-xamda", "prod-at-xanhthan", "prod-at-oversize"],
      },
      {
        id: "dryfit-tech",
        label: "Dry-Fit & Tech 4 chiều",
        description: "Micro-mesh 145gsm thoát ẩm nhanh, Ripstop PFC-free và Nylon Tech trượt nước.",
        productIds: ["prod-at-dryfit", "prod-short-run", "prod-jogger-tech", "prod-aokhoac-gio"],
      },
      {
        id: "plant-fiber",
        label: "Sợi thực vật (Modal, Tre, Lanh)",
        description: "Mát dịu trên da, thấm hút tốt và hạn chế mùi tự nhiên.",
        productIds: ["prod-short-linen", "prod-dolot-boxer", "prod-dolot-brief", "prod-polo-kem"],
      },
    ],
    fabricTechCards: [
      {
        id: "tech-compact",
        name: "Cotton Compact 220gsm",
        gsmRange: "220-260 gsm",
        composition: "95% Cotton chải kỹ Compact, 5% Spandex",
        benefit: "Đứng phom vai, không lộ da với màu trắng, hạn chế xù lông sau 40 lần giặt.",
        productIds: ["prod-at-trang", "prod-at-xamda", "prod-at-oversize"],
      },
      {
        id: "tech-dryfit",
        name: "Micro-mesh Dry-Fit tái chế",
        gsmRange: "118-145 gsm",
        composition: "88% Polyester tái chế, 12% Elastane",
        benefit: "Tản nhiệt nhanh qua lỗ dệt siêu nhỏ, tự khô sau 45 phút phơi gió.",
        productIds: ["prod-at-dryfit", "prod-short-run"],
      },
      {
        id: "tech-modal-bamboo",
        name: "Sợi gỗ sồi Modal & Sợi tre Bamboo",
        gsmRange: "175-180 gsm",
        composition: "93-95% Sợi gỗ sồi / Sợi tre, 5-7% Spandex",
        benefit: "Hút ẩm cao hơn cotton thường, hạn chế mùi và không gây hằn khi may Flatlock.",
        productIds: ["prod-dolot-boxer", "prod-dolot-brief"],
      },
      {
        id: "tech-ripstop",
        name: "Nylon Ripstop PFC-free",
        gsmRange: "95-185 gsm",
        composition: "100% Nylon Ripstop mờ / 86% Nylon Tech",
        benefit: "Trượt nước nhẹ không dùng hóa chất PFC, chắn gió và gấp gọn chỉ 210g.",
        productIds: ["prod-aokhoac-gio", "prod-jogger-tech"],
      },
    ],
  },
  {
    slug: "soi-xanh-nexus",
    kind: "csr",
    title: "Sợi Xanh Nexus",
    subtitle:
      "10% doanh thu từng sản phẩm trong BST Sợi Xanh được đóng góp trực tiếp vào quỹ áo ấm và đồng phục học sinh vùng cao.",
    heroImage: "/banners/campaign-green-fiber.jpg",
    intro:
      "Mỗi đơn hàng chọn chất liệu bền vững từ 4 xưởng dệt đều được ghi nhận minh bạch theo từng đợt. Bảng tổng hợp bên dưới là số liệu mẫu minh họa tiến độ đóng góp công khai.",
    productIds: [
      "prod-at-trang",
      "prod-at-xamda",
      "prod-at-dryfit",
      "prod-polo-kem",
      "prod-short-linen",
      "prod-dolot-boxer",
      "prod-dolot-brief",
      "prod-aokhoac-gio",
    ],
    subTabs: [
      {
        id: "all",
        label: "Tất cả sản phẩm Sợi Xanh",
        description: "Đóng góp 10% giá trị bán lẻ trên từng đơn hoàn tất.",
        productIds: [
          "prod-at-trang",
          "prod-at-xamda",
          "prod-at-dryfit",
          "prod-polo-kem",
          "prod-short-linen",
          "prod-dolot-boxer",
          "prod-dolot-brief",
          "prod-aokhoac-gio",
        ],
      },
      {
        id: "plant",
        label: "Sợi tre, Modal & Lanh",
        description: "Nhóm sợi nguồn gốc thực vật thân thiện làn da.",
        productIds: ["prod-short-linen", "prod-dolot-boxer", "prod-dolot-brief", "prod-polo-kem"],
      },
      {
        id: "recycled",
        label: "Cotton chải kỹ & Sợi tái chế",
        description: "Áo cotton 220gsm, áo thể thao sợi tái chế và áo khoác trượt nước PFC-free.",
        productIds: ["prod-at-trang", "prod-at-xamda", "prod-at-dryfit", "prod-aokhoac-gio"],
      },
    ],
    csrProgress: {
      targetVnd: 150000000,
      currentRaisedVnd: 96400000,
      ordersContributed: 2840,
      contributionRule: "Trích 10% doanh thu thuần từ sản phẩm gắn nhãn Sợi Xanh Nexus",
      beneficiary:
        "Quỹ đồng phục và áo gió học sinh tiểu học tỉnh Lai Châu & Cao Bằng (đợt mẫu Quý 4/2026)",
      isSampleData: true,
      statementRows: [
        {
          id: "csr-2611-04",
          date: "2026-11-18",
          description: "Tổng hợp 10% doanh thu tuần 3 tháng 11 (Cotton 220gsm & Modal)",
          shopName: "Xưởng Dệt Bạch Đằng",
          ordersCount: 910,
          amountVnd: 31200000,
        },
        {
          id: "csr-2611-03",
          date: "2026-11-14",
          description: "Tổng hợp 10% doanh thu dòng Dry-Fit tái chế & Áo khoác Ripstop",
          shopName: "Kho Basic Sài Gòn",
          ordersCount: 840,
          amountVnd: 28900000,
        },
        {
          id: "csr-2611-02",
          date: "2026-11-09",
          description: "Tổng hợp 10% doanh thu Short Linen & Đồ lót sợi tre Bamboo",
          shopName: "Sợi Tự Nhiên Đà Nẵng",
          ordersCount: 620,
          amountVnd: 21500000,
        },
        {
          id: "csr-2611-01",
          date: "2026-11-03",
          description: "Đóng góp mở đợt Quý 4 từ chương trình đổi vải vụn tái chế",
          shopName: "Dệt Kim Hà Nội",
          ordersCount: 470,
          amountVnd: 14800000,
        },
      ],
    },
  },
];

export const HELP_TOPIC_LABELS: Record<HelpTopicKey, string> = {
  "giao-hang": "Vận chuyển & Giao nhanh",
  "doi-tra-45-ngay": "Đổi trả 45 ngày & Đổi size",
  "thanh-toan-vat": "Thanh toán & Hóa đơn VAT",
  "hoi-vien-xu": "Hội viên Nexus & Xu",
  "chon-size": "Chọn size theo xưởng",
  "khieu-nai": "Khiếu nại & Bảo vệ người mua",
};

export const HELP_FAQ_ITEMS: HelpFaqItem[] = [
  {
    id: "faq-ship-1",
    topic: "giao-hang",
    scope: "san",
    question: "Ngưỡng miễn phí vận chuyển trên NexusMart được tính như thế nào?",
    answer:
      "Toàn sàn miễn phí vận chuyển cho đơn từ 249.000₫ khi áp mã FREESHIP25 (hoặc từ 499.000₫ với FREESHIP40). Ngoài ra, từng gian hàng có ngưỡng hỗ trợ vận chuyển riêng từ 199.000₫ hiển thị ngay trên thanh tiến độ trong giỏ hàng.",
  },
  {
    id: "faq-ship-2",
    topic: "giao-hang",
    scope: "gian-hang",
    question: "Tôi có được đồng kiểm khi nhận hàng và chọn giao nhanh nội thành không?",
    answer:
      "Tất cả đơn giao tiêu chuẩn qua SPX và GHN đều hỗ trợ đồng kiểm ngoại quan trước khi thanh toán COD. Các xưởng Kho Basic Sài Gòn, Dệt Kim Hà Nội và Sợi Tự Nhiên Đà Nẵng hỗ trợ thêm giao nhanh nội thành 2-4 giờ khi đặt trước 17:00.",
  },
  {
    id: "faq-ret-1",
    topic: "doi-tra-45-ngay",
    scope: "san",
    question: "Chính sách đổi trả 45 ngày và đổi size 2 chiều tận nơi áp dụng ra sao?",
    answer:
      "Bạn được đổi trả miễn phí trong 45 ngày (tối đa 3 lần cho mỗi đơn hàng). Khi chọn đổi size 2 chiều tại trang Chi tiết đơn, bưu tá sẽ giao sản phẩm cỡ mới và nhận lại sản phẩm cũ ngay tại nhà. Các khoản hoàn chuyển khoản được thực hiện định kỳ vào Thứ 2 và Thứ 5 hằng tuần.",
  },
  {
    id: "faq-ret-2",
    topic: "doi-tra-45-ngay",
    scope: "san",
    question: "Những sản phẩm nào không áp dụng đổi trả 45 ngày?",
    answer:
      "Sản phẩm gắn nhãn Outlet xả kho, suất Săn deal sốc và pack đồ lót niêm phong đã mở túi không áp dụng đổi trả (trừ trường hợp lỗi sản xuất hoặc giao sai mẫu).",
  },
  {
    id: "faq-ret-3",
    topic: "doi-tra-45-ngay",
    scope: "gian-hang",
    question: "Chính sách bù lẻ chiếc tất thất lạc hoạt động như thế nào?",
    answer:
      "Với các combo tất từ Dệt Kim Hà Nội, nếu bạn vô tình thất lạc 1 chiếc tất trong vòng 45 ngày kể từ khi nhận hàng, hãy gửi mã đơn ở mục Hỗ trợ để xưởng gửi kèm 1 chiếc đồng màu miễn phí trong đơn kế tiếp.",
  },
  {
    id: "faq-pay-1",
    topic: "thanh-toan-vat",
    scope: "san",
    question: "Nếu thanh toán VietQR hoặc Ví NexusPay bị gián đoạn thì đơn hàng có bị hủy không?",
    answer:
      "Không. Hệ thống giữ nguyên đơn ở trạng thái Chờ thanh toán để bạn bấm Thanh toán lại hoặc chuyển sang phương thức thanh toán khác (như COD) ngay trong Lịch sử đơn hàng mà không cần chọn lại sản phẩm.",
  },
  {
    id: "faq-pay-2",
    topic: "thanh-toan-vat",
    scope: "gian-hang",
    question: "Tôi cần xuất hóa đơn VAT điện tử cho công ty thì làm thế nào?",
    answer:
      "Bạn có thể điền Tên công ty, Mã số thuế và Email ngay tại bước thanh toán hoặc gửi bổ sung tại trang Chi tiết đơn sau khi nhận hàng. Gian hàng sẽ gửi bản nháp duyệt trong 24 giờ trước khi phát hành hóa đơn điện tử chính thức.",
  },
  {
    id: "faq-mem-1",
    topic: "hoi-vien-xu",
    scope: "san",
    question: "Điểm Xu Hội viên Nexus quy đổi như thế nào và có hết hạn không?",
    answer:
      "1 Xu tương đương 1₫, được hoàn 1% đến 6% tùy 4 hạng (Thành viên, Bạc, Vàng, Bạch Kim) và nhân đôi vào mỗi Thứ Sáu. Xu có hạn sử dụng 12 tháng kể từ ngày cộng và dùng được trên mọi gian hàng của NexusMart.",
  },
  {
    id: "faq-size-1",
    topic: "chon-size",
    scope: "gian-hang",
    question: "Vì sao cùng chiều cao cân nặng nhưng hai gian hàng lại gợi ý cỡ khác nhau?",
    answer:
      "Mỗi xưởng may theo một bộ rập riêng (ví dụ Dệt Kim Hà Nội nghiêng về phom ôm gọn, còn Kho Basic Sài Gòn thiên về phom vận động thoải mái). Hồ sơ số đo của bạn sẽ tự quy đổi theo đúng bảng rập của từng xưởng.",
  },
  {
    id: "faq-cmp-1",
    topic: "khieu-nai",
    scope: "san",
    question: "Thời gian tiếp nhận và giải quyết khiếu nại trên NexusMart là bao lâu?",
    answer:
      "Mọi khiếu nại được xác nhận tiếp nhận trong vòng 24 giờ và giải quyết dứt điểm trong 5-7 ngày làm việc. Các yêu cầu từ nhóm khách hàng yếu thế (người cao tuổi, phụ nữ mang thai, người khuyết tật) được gắn cờ xử lý ưu tiên ngay lập tức.",
  },
];

export const COMPLAINT_SLA = {
  ackHours: 24,
  resolveDaysLabel: "5-7 ngày làm việc",
  hotlineHours: "8:30 - 22:00 (Thứ 2 đến Chủ nhật)",
  callbackMinutes: 15,
  vulnerablePriorityNote:
    "Ưu tiên tiếp nhận và giải quyết trước cho người cao tuổi, phụ nữ mang thai và người khuyết tật.",
  commitments: [
    "Cam kết hài lòng 100%: đổi trả miễn phí 45 ngày, tối đa 3 lần đổi size.",
    "Hoàn tiền đơn trả trước hủy sớm trong 24 giờ; hoàn trả hàng định kỳ Thứ 2 & Thứ 5.",
    "Gian hàng phản hồi khiếu nại trong 24 giờ; nếu quá hạn, NexusMart trực tiếp tiếp nhận xử lý.",
  ],
} as const;

export const CARE_GUIDE_LIBRARY: CareGuideEntry[] = [
  {
    id: "care-cotton",
    fabricGroup: "Cotton Compact 220gsm & Heavyweight 260gsm",
    tempRule: "Giặt máy nhẹ <= 30°C, lộn trái mặt vải",
    dryingRule: "Phơi râm thoáng gió, phơi ngang với áo 260gsm",
    avoidRule: "Không dùng thuốc tẩy clo, không sấy nóng > 60°C",
    tip: "Giũ phẳng ngay sau khi vắt giúp cổ áo bo rib giữ dáng không cần ủi mỗi sáng.",
  },
  {
    id: "care-sport",
    fabricGroup: "Thể thao Dry-Fit, Polyamide & Ripstop trượt nước",
    tempRule: "Giặt nước lạnh ngay sau buổi tập",
    dryingRule: "Phơi gió tự khô trong 45 phút",
    avoidRule: "Tuyệt đối không dùng nước xả vải hoặc bàn là nóng",
    tip: "Nước xả vải phủ lớp sáp làm bít lỗ thoáng micro-mesh và giảm lớp trượt nước PFC-free.",
  },
  {
    id: "care-natural",
    fabricGroup: "Sợi tre Bamboo, gỗ sồi Modal & Lanh Linen",
    tempRule: "Cho vào túi giặt lưới, nước mát chế độ nhẹ",
    dryingRule: "Phơi nơi thoáng mát tránh nắng gắt trực tiếp",
    avoidRule: "Không vắt xoắn mạnh tay, không ngâm bột giặt quá 15 phút",
    tip: "Là hơi nước khi quần linen còn ẩm nhẹ giúp sợi lanh mềm và ít nhăn rõ rệt.",
  },
];

export function getCollectionBySlug(slug: string): CollectionItem | undefined {
  return COLLECTIONS.find((c) => c.slug === slug);
}

export function getJournalBySlug(slug: string): JournalArticle | undefined {
  return JOURNAL_ARTICLES.find((a) => a.slug === slug);
}

export function getCampaignBySlug(slug: string): CampaignItem | undefined {
  return CAMPAIGNS.find((c) => c.slug === slug);
}
