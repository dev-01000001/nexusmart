import {
  FileText,
  Flag,
  Globe,
  LayoutDashboard,
  ShieldCheck,
  SlidersHorizontal,
  Store,
  Wallet,
  Zap,
} from "lucide-react";
import type { AppCommandItem, AppNavGroup, AppNavItem } from "@/components/app";
import { canAccessAdminPath, roleHasCapability } from "@/lib/admin-rbac";

export const ADMIN_SIDEBAR_STORAGE_KEY = "nm.admin.sidebar.v1";

export interface AdminNavCounts {
  pendingShopApprovals: number;
  pendingProductReviews: number;
  openComplaintsCount: number;
  ledgerUnbalanced: boolean;
  pendingApprovalsCount: number;
}

interface AdminNavSpec {
  groupTitle: string;
  items: Array<
    AppNavItem & {
      keywords: string[];
    }
  >;
}

function getAdminNavSpecs(role: string, counts: AdminNavCounts): AdminNavSpec[] {
  const canReadApprovals = roleHasCapability(role, "approvals.read");
  const approvalsBadge =
    canReadApprovals && counts.pendingApprovalsCount > 0 ? counts.pendingApprovalsCount : undefined;

  const rawGroups: AdminNavSpec[] = [
    {
      groupTitle: "Điều hành",
      items: [
        {
          href: "/admin",
          label: "Tổng quan",
          icon: LayoutDashboard,
          match: "exact",
          keywords: ["tong quan", "overview", "dashboard", "hang doi", "suc khoe", "gmv"],
        },
        {
          href: "/admin/gian-hang",
          label: "Duyệt gian hàng",
          icon: Store,
          badge: counts.pendingShopApprovals > 0 ? counts.pendingShopApprovals : undefined,
          keywords: ["duyet gian hang", "shop", "ho so", "gpkd", "ngan hang", "tam khoa"],
        },
        {
          href: "/admin/kiem-duyet",
          label: "Duyệt sản phẩm",
          icon: ShieldCheck,
          badge: counts.pendingProductReviews > 0 ? counts.pendingProductReviews : undefined,
          keywords: ["duyet san pham", "kiem duyet", "moderation", "sku", "tu khoa cam"],
        },
        {
          href: "/admin/khieu-nai",
          label: "Khiếu nại",
          icon: Flag,
          badge: counts.openComplaintsCount > 0 ? counts.openComplaintsCount : undefined,
          keywords: ["khieu nai", "tranh chap", "hoan tien", "tra hang", "returns"],
        },
      ],
    },
    {
      groupTitle: "Kinh doanh",
      items: [
        {
          href: "/admin/sites",
          label: "Site gian hàng",
          icon: Globe,
          keywords: ["site gian hang", "ten mien", "domain", "subdomain", "storefront"],
        },
        {
          href: "/admin/flash-sale",
          label: "Flash sale",
          icon: Zap,
          keywords: ["flash sale", "khung gio", "suat", "giam gia", "khuyen mai"],
        },
      ],
    },
    {
      groupTitle: "Hệ thống",
      items: [
        {
          href: "/admin/doi-soat",
          label: "Đối soát & sổ cái",
          icon: Wallet,
          badge: counts.ledgerUnbalanced ? 1 : undefined,
          keywords: [
            "doi soat",
            "so cai",
            "ghi kep",
            "thanh toan",
            "hoan tien",
            "ledger",
            "reconcile",
          ],
        },
        {
          href: "/admin/cau-hinh",
          label: "Cấu hình sàn",
          icon: SlidersHorizontal,
          badge: approvalsBadge,
          keywords: [
            "cau hinh san",
            "danh muc",
            "hoa hong",
            "nguoi dung",
            "nhan vien",
            "phan quyen",
            "rbac",
            "duyet hai nguoi",
            "cong thanh toan",
            "hang van chuyen",
          ],
        },
        {
          href: "/admin/audit-log",
          label: "Nhật ký kiểm toán",
          icon: FileText,
          keywords: ["nhat ky kiem toan", "audit log", "lich su"],
        },
      ],
    },
  ];

  return rawGroups
    .map((group) => ({
      groupTitle: group.groupTitle,
      items: group.items.filter((item) => canAccessAdminPath(role, item.href)),
    }))
    .filter((group) => group.items.length > 0);
}

export function buildAdminNavGroups(role: string, counts: AdminNavCounts): AppNavGroup[] {
  return getAdminNavSpecs(role, counts).map((group) => ({
    title: group.groupTitle,
    items: group.items.map((item) => ({
      href: item.href,
      label: item.label,
      icon: item.icon,
      badge: item.badge,
      external: item.external,
      children: item.children,
      match: item.match,
    })),
  }));
}

export function buildAdminCommandItems(role: string): AppCommandItem[] {
  const emptyCounts: AdminNavCounts = {
    pendingShopApprovals: 0,
    pendingProductReviews: 0,
    openComplaintsCount: 0,
    ledgerUnbalanced: false,
    pendingApprovalsCount: 0,
  };

  const navCommands: AppCommandItem[] = getAdminNavSpecs(role, emptyCounts).flatMap((group) =>
    group.items.map((item) => ({
      label: item.label,
      group: group.groupTitle,
      href: item.href,
      keywords: [item.href, group.groupTitle, ...item.keywords],
    }))
  );

  navCommands.push({
    label: "Trang chủ NexusMart",
    group: "Chuyển khu vực",
    href: "/",
    keywords: ["trang chu", "nexusmart", "home", "san"],
  });

  return navCommands;
}
