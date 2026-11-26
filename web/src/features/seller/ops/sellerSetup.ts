import type { SetupStepStatus, StatusTone } from "@/components/app";
import type { SiteDomainDTO } from "@/lib/site/domains";
import type { GwSellerBankAccount } from "../sellerApiTypes";
import type { SellerSiteSummary } from "../site/siteSummary";

export interface SellerSetupInput {
  productsCount: number;
  productsError: string | null;
  bankAccount: GwSellerBankAccount | null;
  bankAccountError: string | null;
  site: SellerSiteSummary;
  domains: SiteDomainDTO[];
  domainsError: string | null;
}

export interface SellerSetupStepDescriptor {
  id: "product" | "bank" | "site" | "domain";
  status: SetupStepStatus;
  optional?: boolean;
  title: string;
  description: string;
  badge?: { tone: StatusTone; label: string };
}

export function deriveSellerSetupSteps(input: SellerSetupInput): SellerSetupStepDescriptor[] {
  // 1. First product: done when shop has >= 1 product regardless of approval status
  let productStatus: SetupStepStatus = "todo";
  let productDesc =
    "Thêm sản phẩm kèm hình ảnh, giá bán và tồn kho SKU để bắt đầu mở bán trên site gian hàng.";
  if (input.productsError) {
    productStatus = "unknown";
    productDesc = "Chưa tải được danh sách sản phẩm. Tải lại trang để kiểm tra tiến độ.";
  } else if (input.productsCount >= 1) {
    productStatus = "done";
    productDesc = `Gian hàng đã có ${input.productsCount} sản phẩm trong danh mục.`;
  }

  // 2. Bank account: done when PENDING_ADMIN_REVIEW or VERIFIED
  let bankStatus: SetupStepStatus = "todo";
  let bankBadge: { tone: StatusTone; label: string } | undefined;
  let bankDesc = "Thêm và xác minh OTP tài khoản ngân hàng để nhận đối soát VietQR và thu hộ COD.";
  if (input.bankAccountError) {
    bankStatus = "unknown";
    bankDesc = "Chưa kiểm tra được trạng thái tài khoản ngân hàng. Vào mục Tài chính để xem.";
  } else if (input.bankAccount) {
    const st = (input.bankAccount.status ?? "").trim().toUpperCase();
    if (st === "VERIFIED") {
      bankStatus = "done";
      bankDesc = `${input.bankAccount.bank_name} (${input.bankAccount.account_number_masked}) đã được xác minh.`;
    } else if (st === "PENDING_ADMIN_REVIEW") {
      bankStatus = "done";
      bankBadge = { tone: "attention", label: "Chờ duyệt" };
      bankDesc = `${input.bankAccount.bank_name} (${input.bankAccount.account_number_masked}) đang chờ quản trị viên phê duyệt.`;
    } else if (st === "PENDING_OTP") {
      bankStatus = "todo";
      bankBadge = { tone: "attention", label: "Chờ xác thực OTP" };
      bankDesc = "Hoàn tất bước nhập mã OTP để gửi tài khoản ngân hàng đi duyệt.";
    } else if (st === "REJECTED") {
      bankStatus = "todo";
      bankBadge = { tone: "critical", label: "Bị từ chối" };
      bankDesc = input.bankAccount.review_note
        ? `Hồ sơ ngân hàng cần cập nhật lại: ${input.bankAccount.review_note}`
        : "Tài khoản ngân hàng trước đó chưa được duyệt. Vui lòng cập nhật lại thông tin.";
    }
  }

  // 3. Choose template & publish site: done when site is PUBLISHED
  let siteStatus: SetupStepStatus = "todo";
  let siteBadge: { tone: StatusTone; label: string } | undefined;
  let siteDesc = "Chọn mẫu giao diện phù hợp trong Site Studio và xuất bản để nhận đơn hàng.";
  if (input.site.state === "published") {
    siteStatus = "done";
    siteDesc = `Site gian hàng đang mở bán tại ${input.site.host}.`;
  } else if (input.site.state === "suspended") {
    siteStatus = "todo";
    siteBadge = { tone: "critical", label: "Tạm khóa" };
    siteDesc = input.site.suspendedReason
      ? `Site đang tạm khóa: ${input.site.suspendedReason}`
      : "Site bán hàng đang tạm khóa. Kiểm tra chi tiết trong Site Studio.";
  } else if (input.site.state === "draft") {
    siteStatus = "todo";
    siteBadge = { tone: "info", label: "Bản nháp" };
    siteDesc =
      "Bản nháp site đã sẵn sàng. Hoàn tất tuỳ chỉnh và bấm Xuất bản để khách hàng đặt mua.";
  }

  // Optional: Custom domain (does NOT count into setupProgress total)
  let domainStatus: SetupStepStatus = "todo";
  let domainBadge: { tone: StatusTone; label: string } | undefined;
  let domainDesc = "Kết nối tên miền thương hiệu riêng qua bản ghi DNS mà không mất phí nền tảng.";
  if (input.domainsError) {
    domainStatus = "unknown";
    domainDesc = "Chưa kiểm tra được danh sách tên miền. Mở trang Tên miền riêng để xem.";
  } else {
    const customDomains = input.domains.filter((d) => d.kind === "CUSTOM");
    const activeCustom = customDomains.find((d) => d.status === "ACTIVE");
    const pendingCustom = customDomains.find((d) => d.status === "PENDING");
    const failedCustom = customDomains.find((d) => d.status === "FAILED");
    if (activeCustom) {
      domainStatus = "done";
      domainDesc = `Đã kết nối tên miền riêng ${activeCustom.host}.`;
    } else if (pendingCustom) {
      domainStatus = "todo";
      domainBadge = { tone: "attention", label: "Đang xác minh" };
      domainDesc = `Tên miền ${pendingCustom.host} đang chờ xác minh bản ghi DNS.`;
    } else if (failedCustom) {
      domainStatus = "todo";
      domainBadge = { tone: "critical", label: "Lỗi DNS" };
      domainDesc = `Cấu hình DNS cho ${failedCustom.host} chưa khớp. Kiểm tra lại hướng dẫn.`;
    }
  }

  return [
    {
      id: "product",
      status: productStatus,
      title: "Thêm sản phẩm đầu tiên",
      description: productDesc,
    },
    {
      id: "bank",
      status: bankStatus,
      title: "Thêm tài khoản ngân hàng nhận tiền",
      description: bankDesc,
      badge: bankBadge,
    },
    {
      id: "site",
      status: siteStatus,
      title: "Chọn mẫu và xuất bản site",
      description: siteDesc,
      badge: siteBadge,
    },
    {
      id: "domain",
      status: domainStatus,
      optional: true,
      title: "Gắn tên miền riêng",
      description: domainDesc,
      badge: domainBadge,
    },
  ];
}
