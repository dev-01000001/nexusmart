import type { StatusTone } from "@/components/app/statusTone";

export const SAMPLE_SHOP_NAME = "Xưởng may Áo Đẹp Sài Gòn chi nhánh Thủ Đức (mẫu)";

export interface PaletteSwatch {
  token: string;
  hex: string;
  usage: string;
  contrast: string;
  darkText?: boolean;
}

export const APP_PALETTE_SWATCHES: readonly PaletteSwatch[] = [
  { token: "--ui-background", hex: "#EFF3F5", usage: "Nền vùng nội dung", contrast: "12.2:1" },
  {
    token: "--ui-foreground",
    hex: "#1F3038",
    usage: "Chữ chính",
    contrast: "12.2:1",
    darkText: false,
  },
  { token: "--ui-card", hex: "#FFFFFF", usage: "Thẻ, menu, hộp thoại", contrast: "13.6:1" },
  {
    token: "--ui-primary",
    hex: "#1F3038",
    usage: "Nút hành động chính",
    contrast: "13.6:1",
    darkText: false,
  },
  { token: "--ui-secondary", hex: "#E4EAEE", usage: "Nút phụ xám, tab chọn", contrast: "11.2:1" },
  {
    token: "--ui-muted-foreground",
    hex: "#52626A",
    usage: "Chữ phụ, đầu cột bảng",
    contrast: "6.3 / 5.7",
    darkText: false,
  },
  {
    token: "--ui-brand",
    hex: "#0D7480",
    usage: "Link, focus, tiến độ, biểu đồ",
    contrast: "5.5 / 4.9",
    darkText: false,
  },
  {
    token: "--ui-destructive",
    hex: "#B33A3A",
    usage: "Xoá, hết hàng, cảnh báo",
    contrast: "5.9:1",
    darkText: false,
  },
  { token: "--ui-border", hex: "#DDE4E8", usage: "Viền thẻ, vạch ngăn bảng", contrast: "—" },
  {
    token: "--ui-input",
    hex: "#7D8E97",
    usage: "Viền ô nhập, checkbox",
    contrast: "3.4:1",
    darkText: false,
  },
  {
    token: "--ui-topbar",
    hex: "#152229",
    usage: "Thanh trên 56px, SaveBar",
    contrast: "11.8:1",
    darkText: false,
  },
  {
    token: "--ui-topbar-input",
    hex: "#293E48",
    usage: "Ô tìm ⌘K trên thanh trên",
    contrast: "5.0:1",
    darkText: false,
  },
  { token: "--ui-sidebar", hex: "#E6ECEF", usage: "Menu trái 240px", contrast: "11.5 / 5.3" },
];

export interface BadgeToneSwatch {
  tone: StatusTone;
  label: string;
  lightBg: string;
  lightInk: string;
  lightContrast: string;
  darkBg: string;
  darkInk: string;
  darkContrast: string;
}

export const BADGE_TONE_SWATCHES: readonly BadgeToneSwatch[] = [
  {
    tone: "success",
    label: "Hoàn tất / Đang bán",
    lightBg: "#DDF3E6",
    lightInk: "#145338",
    lightContrast: "7.8:1",
    darkBg: "#16352A",
    darkInk: "#8EE0B5",
    darkContrast: "8.6:1",
  },
  {
    tone: "attention",
    label: "Chờ xử lý / Chờ duyệt",
    lightBg: "#FBEFB5",
    lightInk: "#5C4A00",
    lightContrast: "7.4:1",
    darkBg: "#3A3210",
    darkInk: "#F2DE7A",
    darkContrast: "9.4:1",
  },
  {
    tone: "warning",
    label: "Chờ thanh toán / Hết hàng",
    lightBg: "#FCDDB8",
    lightInk: "#6B3D00",
    lightContrast: "7.1:1",
    darkBg: "#3D2A12",
    darkInk: "#F5B97A",
    darkContrast: "7.9:1",
  },
  {
    tone: "info",
    label: "Đang giao / Bản nháp",
    lightBg: "#DCEBFA",
    lightInk: "#0B4A7A",
    lightContrast: "7.6:1",
    darkBg: "#132C44",
    darkInk: "#8CC4F5",
    darkContrast: "7.7:1",
  },
  {
    tone: "critical",
    label: "Đã huỷ / Bị từ chối",
    lightBg: "#F8E2E2",
    lightInk: "#8C2626",
    lightContrast: "7.0:1",
    darkBg: "#3D1A1A",
    darkInk: "#F5A3A3",
    darkContrast: "7.8:1",
  },
  {
    tone: "neutral",
    label: "Đã ẩn / Đã hoàn",
    lightBg: "#E6ECEF",
    lightInk: "#2C3E47",
    lightContrast: "9.3:1",
    darkBg: "#262C33",
    darkInk: "#D3DDE2",
    darkContrast: "10.2:1",
  },
];

export const TYPE_SCALE_SAMPLES = [
  {
    role: "Tiêu đề trang",
    spec: "20px / 28px / 650",
    className: "text-[20px] leading-[28px] font-semibold tracking-tight",
    sample: "Quản lý đơn hàng tháng 11 (mẫu)",
  },
  {
    role: "Tiêu đề thẻ",
    spec: "14px / 20px / 650",
    className: "text-sm leading-5 font-semibold",
    sample: "Thông tin thanh toán và vận chuyển (mẫu)",
  },
  {
    role: "Chữ thân (mặc định)",
    spec: "14px / 20px / 450",
    className: "text-sm leading-5 font-normal",
    sample: "Áo thun cotton chải kỹ 220gsm giữ phom cổ phẳng sau nhiều lần giặt (mẫu).",
  },
  {
    role: "Chữ phụ, đầu cột bảng, nhãn",
    spec: "13px / 18px / 450–550",
    className: "text-[13px] leading-[18px] font-medium text-muted-foreground",
    sample: "Cập nhật lần cuối lúc 09:48 · 26/11/2026 (mẫu)",
  },
  {
    role: "Nhãn trạng thái",
    spec: "12px / 16px / 550",
    className: "text-xs leading-4 font-medium",
    sample: "Đang giao hàng (mẫu)",
  },
  {
    role: "Số KPI",
    spec: "24px / 32px / 650 · tabular-nums",
    className: "text-2xl leading-8 font-semibold tabular-nums",
    sample: "12.450.000₫",
  },
] as const;

export interface SampleProductSku {
  sku: string;
  label: string;
  stock: number;
  priceVnd: number;
}

export interface SampleProduct {
  id: string;
  name: string;
  category: string;
  status: "APPROVED" | "PENDING" | "OUT_OF_STOCK" | "HIDDEN";
  statusLabel: string;
  priceVnd: number;
  compareAtVnd?: number;
  image: string;
  skus: SampleProductSku[];
}

export const SAMPLE_PRODUCTS: readonly SampleProduct[] = [
  {
    id: "sp-1",
    name: "Áo polo dệt kim cổ bẻ sợi bông chải kỹ thoáng khí phiên bản Thu Đông (mẫu)",
    category: "Áo polo",
    status: "OUT_OF_STOCK",
    statusLabel: "Hết hàng",
    priceVnd: 289000,
    compareAtVnd: 349000,
    image: "/products/ao-polo-det-kim-den.png",
    skus: [
      { sku: "POLO-DEN-M", label: "Đen / M", stock: 0, priceVnd: 289000 },
      { sku: "POLO-DEN-L", label: "Đen / L", stock: 0, priceVnd: 289000 },
    ],
  },
  {
    id: "sp-2",
    name: "Áo thun Cotton Compact 220gsm đứng phom vai mặc hằng ngày (mẫu)",
    category: "Áo thun",
    status: "APPROVED",
    statusLabel: "Đang bán",
    priceVnd: 199000,
    compareAtVnd: 249000,
    image: "/products/ao-thun-cotton-trang.png",
    skus: [
      { sku: "TEE-TRANG-M", label: "Trắng / M", stock: 24, priceVnd: 199000 },
      { sku: "TEE-TRANG-L", label: "Trắng / L", stock: 12, priceVnd: 199000 },
      { sku: "TEE-TRANG-XL", label: "Trắng / XL", stock: 0, priceVnd: 199000 },
    ],
  },
  {
    id: "sp-3",
    name: "Quần short linen pha cotton ống suông co giãn nhẹ hai túi sâu (mẫu)",
    category: "Quần short",
    status: "PENDING",
    statusLabel: "Chờ duyệt",
    priceVnd: 319000,
    image: "/products/quan-short-linen-xam-da.png",
    skus: [
      { sku: "SH-XAM-M", label: "Xám đá / M", stock: 18, priceVnd: 319000 },
      { sku: "SH-XAM-L", label: "Xám đá / L", stock: 9, priceVnd: 319000 },
    ],
  },
  {
    id: "sp-4",
    name: "Áo khoác gió chống trượt nước nhẹ hai lớp cổ đứng đi mưa nhỏ (mẫu)",
    category: "Áo khoác",
    status: "APPROVED",
    statusLabel: "Đang bán",
    priceVnd: 459000,
    compareAtVnd: 549000,
    image: "/products/ao-khoac-gio-xam-tro.png",
    skus: [
      { sku: "JK-XAM-L", label: "Xám tro / L", stock: 15, priceVnd: 459000 },
      { sku: "JK-XAM-XL", label: "Xám tro / XL", stock: 7, priceVnd: 459000 },
    ],
  },
];

export interface SampleOrder {
  id: string;
  code: string;
  customer: string;
  createdAt: string;
  paymentMethod: "COD" | "VIETQR";
  status: string;
  statusLabel: string;
  shipmentJob: string;
  shipmentJobLabel: string;
  totalVnd: number;
  itemsCount: number;
}

export const SAMPLE_ORDERS: readonly SampleOrder[] = [
  {
    id: "ord-1",
    code: "#NM-10293",
    customer: "Nguyễn Minh Hoàng (mẫu)",
    createdAt: "09:48 26/11",
    paymentMethod: "COD",
    status: "CONFIRMED",
    statusLabel: "Đã xác nhận",
    shipmentJob: "QUEUED",
    shipmentJobLabel: "Đang tạo vận đơn",
    totalVnd: 518000,
    itemsCount: 2,
  },
  {
    id: "ord-2",
    code: "#NM-10292",
    customer: "Trần Thị Lan Anh (mẫu)",
    createdAt: "09:15 26/11",
    paymentMethod: "VIETQR",
    status: "PENDING",
    statusLabel: "Chờ thanh toán",
    shipmentJob: "",
    shipmentJobLabel: "Chưa lên đơn",
    totalVnd: 229000,
    itemsCount: 1,
  },
  {
    id: "ord-3",
    code: "#NM-10291",
    customer: "Lê Quốc Bảo (mẫu)",
    createdAt: "18:30 25/11",
    paymentMethod: "VIETQR",
    status: "SHIPPING",
    statusLabel: "Đang giao",
    shipmentJob: "",
    shipmentJobLabel: "GHN-882910",
    totalVnd: 748000,
    itemsCount: 3,
  },
  {
    id: "ord-4",
    code: "#NM-10290",
    customer: "Phạm Thuỳ Dương (mẫu)",
    createdAt: "14:10 25/11",
    paymentMethod: "COD",
    status: "COMPLETED",
    statusLabel: "Hoàn tất",
    shipmentJob: "",
    shipmentJobLabel: "GHN-882401",
    totalVnd: 319000,
    itemsCount: 1,
  },
];

export const SAMPLE_ANALYTICS_LABELS = [
  "20/11",
  "21/11",
  "22/11",
  "23/11",
  "24/11",
  "25/11",
  "26/11",
];
export const SAMPLE_REVENUE_VALUES = [
  1250000, 1680000, 1420000, 2150000, 1890000, 2410000, 1650000,
];
export const SAMPLE_ORDERS_VALUES = [5, 7, 6, 9, 8, 11, 8];
export const SAMPLE_AOV_VALUES = [250000, 240000, 236667, 238889, 236250, 219091, 206250];

export const SAMPLE_CONVERSATIONS = [
  {
    id: "c-1",
    customer: "Nguyễn Minh Hoàng (mẫu)",
    preview: "Shop ơi áo polo đen size L còn hàng không ạ?",
    at: "09:42",
    unread: true,
    messages: [
      {
        id: "m-1",
        from: "buyer" as const,
        at: "09:40",
        text: "Chào shop, mình cao 1m72 nặng 68kg mặc áo polo size nào vừa? (mẫu)",
      },
      {
        id: "m-2",
        from: "seller" as const,
        at: "09:41",
        text: "Chào anh Hoàng, dáng 1m72 68kg mặc size L thoải mái vai và dài áo vừa đẹp ạ (mẫu).",
      },
      {
        id: "m-3",
        from: "buyer" as const,
        at: "09:42",
        text: "Shop ơi áo polo đen size L còn hàng không ạ? (mẫu)",
      },
    ],
  },
  {
    id: "c-2",
    customer: "Trần Thị Lan Anh (mẫu)",
    preview: "Mình vừa chuyển khoản VietQR đơn #NM-10292 rồi nha.",
    at: "09:18",
    unread: false,
    messages: [
      {
        id: "m-4",
        from: "buyer" as const,
        at: "09:18",
        text: "Mình vừa chuyển khoản VietQR đơn #NM-10292 rồi nha (mẫu).",
      },
      {
        id: "m-5",
        from: "seller" as const,
        at: "09:20",
        text: "Hệ thống đã nhận giao dịch, shop đang đóng gói để bàn giao GHN chiều nay ạ (mẫu).",
      },
    ],
  },
  {
    id: "c-3",
    customer: "Lê Quốc Bảo (mẫu)",
    preview: "Nhờ shop đổi địa chỉ nhận sang phường Linh Trung giúp mình.",
    at: "Hôm qua",
    unread: false,
    messages: [
      {
        id: "m-6",
        from: "buyer" as const,
        at: "18:05",
        text: "Nhờ shop đổi địa chỉ nhận sang phường Linh Trung giúp mình (mẫu).",
      },
    ],
  },
];

export const SAMPLE_SITE_PRODUCTS = [
  {
    id: "site-p1",
    name: "Áo thun Cotton Compact 220gsm cổ tròn đứng phom (mẫu)",
    image: "/models/model-ao-thun-trang.jpg",
    priceVnd: 199000,
    compareAtVnd: 259000,
    discount: "-23%",
  },
  {
    id: "site-p2",
    name: "Áo polo dệt kim thoáng khí sợi bông dài (mẫu)",
    image: "/models/model-polo-det-kim-den.jpg",
    priceVnd: 289000,
    compareAtVnd: 349000,
    discount: "-17%",
  },
  {
    id: "site-p3",
    name: "Áo khoác gió trượt nước siêu nhẹ hai lớp (mẫu)",
    image: "/models/model-ao-khoac-gio.jpg",
    priceVnd: 459000,
    compareAtVnd: 549000,
    discount: "-16%",
  },
  {
    id: "site-p4",
    name: "Quần jogger kỹ thuật co giãn bốn chiều vận động (mẫu)",
    image: "/models/model-jogger-tech-den.jpg",
    priceVnd: 369000,
    compareAtVnd: 429000,
    discount: "-14%",
  },
] as const;
