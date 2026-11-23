export interface ChatMessage {
  id: string;
  sender: "buyer" | "seller";
  senderName: string;
  text: string;
  timestamp: string;
}

export interface ChatThread {
  id: string;
  shopId: string;
  shopName: string;
  buyerName: string;
  productRef?: {
    slug: string;
    name: string;
    price: number;
    image: string;
  };
  unreadByBuyer: number;
  unreadBySeller: number;
  messages: ChatMessage[];
}

export interface SupportChannel {
  id: string;
  title: string;
  value: string;
  hours: string;
  note: string;
  actionType: "callback" | "chat" | "ai" | "email" | "zalo";
}

export const SUPPORT_CHANNELS: SupportChannel[] = [
  {
    id: "ch-hotline",
    title: "Tổng đài CSKH NexusMart",
    value: "1900 6845",
    hours: "8:30 - 22:00 tất cả các ngày (kể cả Thứ 7 & Chủ nhật)",
    note: "Cam kết chủ động gọi lại cuộc gọi nhỡ trong vòng 15 phút.",
    actionType: "callback",
  },
  {
    id: "ch-shop-chat",
    title: "Chat trực tiếp 1-1 với xưởng",
    value: "Phản hồi trung bình 15 phút",
    hours: "Kết nối trực tiếp với 4 xưởng dệt đối tác",
    note: "Hỏi trực tiếp số đo, màu & size còn sẵn, giao nhanh hoặc hóa đơn VAT.",
    actionType: "chat",
  },
  {
    id: "ch-ai",
    title: "Trợ lý AI chọn size & tra đơn",
    value: "Hỗ trợ 24/7 theo tài khoản của bạn",
    hours: "Chỉ đọc số đo và đơn hàng của chính tài khoản đang đăng nhập",
    note: "Gợi ý cỡ theo chiều cao/cân nặng/dáng người và kiểm tra trạng thái đơn gần nhất.",
    actionType: "ai",
  },
  {
    id: "ch-zalo",
    title: "Zalo OA NexusMart",
    value: "zalo.me/nexusmart-official",
    hours: "Cập nhật hành trình vận đơn tự động qua Zalo OA",
    note: "Nhận nhắc duyệt nháp hóa đơn VAT và lịch hoàn tiền Thứ 2, Thứ 5.",
    actionType: "zalo",
  },
  {
    id: "ch-email",
    title: "Email hỗ trợ & khiếu nại",
    value: "hotro@nexusmart.vn",
    hours: "Xác nhận tiếp nhận trong 24 giờ · Xử lý 5-7 ngày làm việc",
    note: "Tiếp nhận chứng từ hóa đơn VAT, tranh chấp cấp 2 và ưu tiên nhóm khách yếu thế.",
    actionType: "email",
  },
];

export const INITIAL_CHAT_THREADS: ChatThread[] = [
  {
    id: "thr-bach-dang",
    shopId: "shop-bach-dang",
    shopName: "Xưởng Dệt Bạch Đằng",
    buyerName: "Nguyễn Minh Quân",
    productRef: {
      slug: "ao-thun-cotton-compact-trang",
      name: "Áo thun Cotton Compact 220gsm Trắng",
      price: 179000,
      image: "/products/ao-thun-cotton-trang.png",
    },
    unreadByBuyer: 1,
    unreadBySeller: 0,
    messages: [
      {
        id: "msg-1",
        sender: "buyer",
        senderName: "Nguyễn Minh Quân",
        text: "Chào shop, mình cao 1m74 nặng 71 kg thì mặc áo thun Cotton 220gsm size L có bị rộng vai không?",
        timestamp: "2026-11-20T01:12:00+07:00",
      },
      {
        id: "msg-2",
        sender: "seller",
        senderName: "Xưởng Dệt Bạch Đằng",
        text: "Chào anh Quân, thông số 1m74 và 71 kg mặc size L vừa gọn vai, chiều dài áo 71 cm. Nếu thích mặc ôm sát hơn thì mới cần xuống size M ạ.",
        timestamp: "2026-11-20T01:18:00+07:00",
      },
    ],
  },
  {
    id: "thr-da-nang",
    shopId: "shop-da-nang",
    shopName: "Sợi Tự Nhiên Đà Nẵng",
    buyerName: "Nguyễn Minh Quân",
    productRef: {
      slug: "ao-polo-pique-cotton-kem-nhat",
      name: "Áo Polo Pique Cotton Kem nhạt",
      price: 289000,
      image: "/products/ao-polo-pique-kem.png",
    },
    unreadByBuyer: 0,
    unreadBySeller: 1,
    messages: [
      {
        id: "msg-3",
        sender: "buyer",
        senderName: "Nguyễn Minh Quân",
        text: "Đơn NM-261119-098 của mình dự kiến khi nào bàn giao đơn vị vận chuyển vậy shop?",
        timestamp: "2026-11-19T19:40:00+07:00",
      },
    ],
  },
  {
    id: "thr-sai-gon",
    shopId: "shop-sai-gon",
    shopName: "Kho Basic Sài Gòn",
    buyerName: "Trần Hải Đăng",
    productRef: {
      slug: "ao-thun-the-thao-dryfit-xanh-co-vit",
      name: "Áo thun Thể thao Dry-Fit Xanh cổ vịt",
      price: 159000,
      image: "/products/ao-thun-dryfit-co-vit.png",
    },
    unreadByBuyer: 0,
    unreadBySeller: 1,
    messages: [
      {
        id: "msg-4",
        sender: "buyer",
        senderName: "Trần Hải Đăng",
        text: "Áo Dry-Fit xanh cổ vịt giao về Phường Đống Đa, Hà Nội mất mấy ngày ạ?",
        timestamp: "2026-11-19T21:05:00+07:00",
      },
      {
        id: "msg-5",
        sender: "seller",
        senderName: "Kho Basic Sài Gòn",
        text: "Đơn đi Thành phố Hà Nội giao tiêu chuẩn trong 1-2 ngày, phí vận chuyển 22.000₫ hoặc miễn phí khi đạt từ 199.000₫ tại shop hoặc áp mã FREESHIP25 ạ.",
        timestamp: "2026-11-19T21:14:00+07:00",
      },
    ],
  },
];

export interface AiPromptSuggestion {
  id: string;
  label: string;
  prompt: string;
  response: string;
}

export const AI_SUGGESTED_PROMPTS: AiPromptSuggestion[] = [
  {
    id: "ai-size",
    label: "Tư vấn size 1m73 · 70 kg",
    prompt: "Mình cao 1m73, nặng 70 kg thì chọn size nào cho áo thun và quần short?",
    response:
      "Với chiều cao 1m73 và cân nặng 70 kg, size L là lựa chọn vừa vặn nhất cho cả áo thun Regular fit (ngực 106 cm, dài 71 cm) và quần short 7 inch (eo 82-86 cm). Nếu bạn mua Áo thun Oversize 260gsm Than chì và muốn phom gọn hơn một chút, có thể chọn size M.",
  },
  {
    id: "ai-combo",
    label: "Gợi ý combo đi làm dưới 600.000₫",
    prompt: "Gợi ý giúp mình 1 bộ đi làm gọn gàng dưới 600.000₫.",
    response:
      "Bạn có thể kết hợp Áo Polo Pique Cotton Kem nhạt (289.000₫) và Quần Short Linen pha Cotton Xám đá (279.000₫) cùng thuộc Sợi Tự Nhiên Đà Nẵng. Tổng hàng 568.000₫, áp mã DANANG30 giảm 30.000₫ và NEXUS30 giảm 30.000₫, còn 508.000₫ chưa gồm phí giao hàng.",
  },
  {
    id: "ai-fabric",
    label: "So sánh Cotton 220gsm và Dry-Fit",
    prompt: "Khác nhau giữa áo thun Cotton 220gsm và áo thun Dry-Fit là gì?",
    response:
      "Áo thun Cotton Compact 220gsm (95% cotton, 5% spandex) dày dặn, đứng phom, hợp mặc hằng ngày và đi làm. Áo thun Dry-Fit (145gsm, polyester micro-mesh) nhẹ hơn gần 35%, khô sau khoảng 45 phút phơi gió, tối ưu cho chạy bộ hoặc tập gym.",
  },
  {
    id: "ai-order",
    label: "Kiểm tra đơn đang giao gần nhất",
    prompt: "Kiểm tra giúp mình đơn hàng đang giao gần nhất.",
    response:
      "Đơn hàng NM-261117-082 từ Dệt Kim Hà Nội (Quần Jogger Terry Xám + Combo 5 Tất cổ trung, tổng thanh toán 433.000₫ COD) hiện ở trạng thái Đang giao hàng, đã tới kho trung chuyển TP. Hồ Chí Minh lúc 08:30 ngày 19/11.",
  },
  {
    id: "ai-return",
    label: "Chính sách đổi trả 7 ngày",
    prompt: "NexusMart hỗ trợ đổi size và hoàn tiền như thế nào?",
    response:
      "NexusMart hỗ trợ đổi trả miễn phí trong 7 ngày (tối đa 3 lần), bưu tá đổi size 2 chiều tận nơi và hoàn chuyển khoản vào Thứ 2 & Thứ 5 hằng tuần. Riêng hàng Outlet, săn deal sốc và pack niêm phong không áp dụng đổi trả.",
  },
  {
    id: "ai-membership",
    label: "Quyền lợi Hội viên Nexus",
    prompt: "Hội viên Nexus tích và tiêu Xu như thế nào?",
    response:
      "Hội viên Nexus có 4 hạng theo chi tiêu 4 quý: Thành viên (1%), Bạc từ 1.000.000₫ (2%), Vàng từ 3.000.000₫ (4%) và Bạch Kim từ 6.000.000₫ (6%). 1 Xu = 1₫, hạn dùng 12 tháng, nhân đôi vào Thứ Sáu và dùng chéo mọi gian hàng.",
  },
];
