import {
  FileText,
  Home,
  LayoutTemplate,
  MessageSquare,
  Package,
  RotateCcw,
  Settings,
  ShoppingBag,
  Sparkles,
  Store,
  Tag,
  Wallet,
} from "lucide-react";
import type { AppCommandItem, AppNavGroup } from "@/components/app";
import type { SellerSiteSummary } from "../site/siteSummary";

export const SELLER_SIDEBAR_STORAGE_KEY = "nm.seller.sidebarCollapsed";

export interface SellerNavBuildParams {
  hasOperationalShop: boolean;
  pendingOrdersCount: number;
  pendingReturnsCount: number;
  unreadChatCount: number;
  site: SellerSiteSummary;
}

export function buildSellerNavGroups({
  hasOperationalShop,
  pendingOrdersCount,
  pendingReturnsCount,
  unreadChatCount,
  site,
}: SellerNavBuildParams): AppNavGroup[] {
  if (!hasOperationalShop) {
    return [
      {
        title: "Đăng ký gian hàng",
        items: [
          {
            href: "/seller",
            label: "Mở gian hàng",
            icon: Store,
            match: "exact",
          },
        ],
      },
    ];
  }

  const storeItems: AppNavGroup["items"] = [
    {
      href: "/seller/site",
      label: "Site bán hàng",
      icon: LayoutTemplate,
      children: [
        { href: "/seller/site/ten-mien", label: "Tên miền riêng" },
        { href: "/seller/site/xem-truoc", label: "Xem trước" },
      ],
    },
  ];

  if (site.url) {
    storeItems.push({
      href: site.url,
      label: "Xem site công khai",
      icon: Store,
      external: true,
    });
  }

  storeItems.push(
    {
      href: "/seller/tai-chinh",
      label: "Tài chính",
      icon: Wallet,
    },
    {
      href: "/seller/bao-cao-ai",
      label: "Báo cáo AI",
      icon: Sparkles,
    },
    {
      href: "/seller/ho-so",
      label: "Hồ sơ",
      icon: Settings,
    }
  );

  return [
    {
      title: "Vận hành",
      items: [
        {
          href: "/seller",
          label: "Tổng quan",
          icon: Home,
          match: "exact",
        },
        {
          href: "/seller/don-hang",
          label: "Đơn hàng",
          icon: ShoppingBag,
          badge: pendingOrdersCount > 0 ? pendingOrdersCount : undefined,
        },
        {
          href: "/seller/tra-hang",
          label: "Trả hàng",
          icon: RotateCcw,
          badge: pendingReturnsCount > 0 ? pendingReturnsCount : undefined,
        },
        {
          href: "/seller/san-pham",
          label: "Sản phẩm",
          icon: Package,
          children: [{ href: "/seller/san-pham/moi", label: "Thêm sản phẩm mới" }],
        },
        {
          href: "/seller/khuyen-mai",
          label: "Khuyến mãi",
          icon: Tag,
        },
      ],
    },
    {
      title: "Khách hàng",
      items: [
        {
          href: "/seller/chat",
          label: "Tin nhắn",
          icon: MessageSquare,
          badge: unreadChatCount > 0 ? unreadChatCount : undefined,
        },
        {
          href: "/seller/tuong-tac",
          label: "Tương tác",
          icon: FileText,
        },
      ],
    },
    {
      title: "Cửa hàng",
      items: storeItems,
    },
  ];
}

export function buildSellerCommandItems({
  hasOperationalShop,
}: {
  hasOperationalShop: boolean;
}): AppCommandItem[] {
  if (!hasOperationalShop) {
    return [
      {
        label: "Mở gian hàng",
        group: "Đăng ký gian hàng",
        href: "/seller",
        keywords: ["mo gian hang", "dang ky shop", "onboarding"],
      },
      {
        label: "Về trang chủ NexusMart",
        group: "Nền tảng",
        href: "/",
        keywords: ["trang chu", "nexusmart"],
      },
    ];
  }

  return [
    {
      label: "Tổng quan",
      group: "Vận hành",
      href: "/seller",
      keywords: ["tong quan", "hom nay", "dashboard", "bieu do", "doanh thu", "bao cao"],
    },
    {
      label: "Đơn hàng",
      group: "Vận hành",
      href: "/seller/don-hang",
      keywords: ["don hang", "xac nhan", "van chuyen", "giao hang", "cod", "vietqr"],
    },
    {
      label: "Trả hàng",
      group: "Vận hành",
      href: "/seller/tra-hang",
      keywords: ["tra hang", "hoan tien", "doi tra", "tranh chap", "return"],
    },
    {
      label: "Sản phẩm",
      group: "Vận hành",
      href: "/seller/san-pham",
      keywords: ["san pham", "sku", "ton kho", "nhap hang", "bien the", "csv"],
    },
    {
      label: "Khuyến mãi",
      group: "Vận hành",
      href: "/seller/khuyen-mai",
      keywords: ["khuyen mai", "voucher", "ma giam gia", "flash sale", "chuong trinh"],
    },
    {
      label: "Tin nhắn",
      group: "Khách hàng",
      href: "/seller/chat",
      keywords: ["tin nhan", "chat", "hop thu", "khach hang", "hoi thoai"],
    },
    {
      label: "Tương tác",
      group: "Khách hàng",
      href: "/seller/tuong-tac",
      keywords: ["tuong tac", "danh gia", "ai", "thong bao"],
    },
    {
      label: "Site bán hàng",
      group: "Cửa hàng",
      href: "/seller/site",
      keywords: ["site ban hang", "template", "mau giao dien", "ten mien", "xuat ban"],
    },
    {
      label: "Tài chính",
      group: "Cửa hàng",
      href: "/seller/tai-chinh",
      keywords: ["tai chinh", "so du", "doi soat", "so cai", "hoa hong", "ngan hang"],
    },
    {
      label: "Báo cáo AI",
      group: "Cửa hàng",
      href: "/seller/bao-cao-ai",
      keywords: ["bao cao ai", "phan tich", "doanh thu", "tro ly"],
    },
    {
      label: "Hồ sơ",
      group: "Cửa hàng",
      href: "/seller/ho-so",
      keywords: ["ho so", "kho lay hang", "dia chi", "van chuyen", "freeship", "tra loi tu dong"],
    },
    {
      label: "Thêm sản phẩm mới",
      group: "Thao tác nhanh",
      href: "/seller/san-pham/moi",
      keywords: ["them san pham moi", "tao sku", "dang ban"],
    },
    {
      label: "Đơn chờ xác nhận",
      group: "Thao tác nhanh",
      href: "/seller/don-hang?status=CONFIRMED",
      keywords: ["don cho xac nhan", "confirmed", "xu ly"],
    },
    {
      label: "SKU sắp hết hàng",
      group: "Thao tác nhanh",
      href: "/seller/san-pham?filter=LOW_OOS",
      keywords: ["sku sap het hang", "ton kho thap", "nhap ton"],
    },
    {
      label: "Xử lý yêu cầu trả hàng",
      group: "Thao tác nhanh",
      href: "/seller/tra-hang",
      keywords: ["duyet yeu cau doi tra", "tra hang", "hoan tien", "return"],
    },
    {
      label: "Quản lý tên miền riêng",
      group: "Thao tác nhanh",
      href: "/seller/site/ten-mien",
      keywords: ["ten mien rieng", "dns", "domain", "site"],
    },
    {
      label: "Xem trước bản nháp site",
      group: "Thao tác nhanh",
      href: "/seller/site/xem-truoc",
      keywords: ["xem truoc ban nhap", "preview site"],
    },
  ];
}
