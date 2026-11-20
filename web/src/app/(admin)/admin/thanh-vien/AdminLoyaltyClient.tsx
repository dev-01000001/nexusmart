"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { computeGiftCardSummary } from "@/data";
import type {
  AdminGiftCardRecord,
  AdminLoyaltyTierConfig,
  AdminStudentVerificationItem,
} from "@/data";
import { Button, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatDateVi, formatInt, formatPercent, formatVnd } from "@/lib/format";
import {
  formatNowHHMM,
  formatShortTimeDateVi,
  GIFT_CARD_STATUS_META,
  STUDENT_VERIF_STATUS_META,
  type PillTone,
} from "@/features/admin/adminMeta";
import type {
  GwAdminFlashSale,
  GwAdminFlashSaleItem,
  GwAdminVoucher,
} from "@/features/admin/adminApiTypes";
import {
  appendAdminAuditLog,
  updateAdminLoyaltyState,
  useAdminLoyaltyState,
} from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { StatusPill } from "@/features/admin/StatusPill";
import { ToggleSwitch } from "@/features/admin/ToggleSwitch";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type LoyaltyTab = "flash_voucher" | "hoi_vien" | "the_qua_tang";

export interface AdminLoyaltyClientProps {
  initialVouchers: GwAdminVoucher[];
  initialFlashSales: GwAdminFlashSale[];
  vouchersError: string | null;
  flashSalesError: string | null;
}

function defaultFlashDateRange(): { startLocal: string; endLocal: string } {
  const vnNow = new Date(Date.now() + 7 * 3600_000);
  const tomorrow = new Date(vnNow.getTime() + 24 * 3600_000);
  const datePart = tomorrow.toISOString().slice(0, 10);
  return {
    startLocal: `${datePart}T12:00`,
    endLocal: `${datePart}T14:00`,
  };
}

function localVnInputToIso(value: string): string {
  if (!value) return "";
  const normalized = value.length === 16 ? `${value}:00+07:00` : `${value}+07:00`;
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
}

function getFlashItemStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "APPROVED":
      return { label: "Đã duyệt suất", tone: "positive" };
    case "REJECTED":
      return { label: "Đã từ chối", tone: "negative" };
    case "PENDING":
      return { label: "Chờ duyệt", tone: "warning" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

function getVoucherScopeLabel(scope: string): string {
  switch (scope.toUpperCase()) {
    case "PLATFORM":
      return "Giảm đơn sàn";
    case "FREESHIP":
      return "Freeship sàn";
    case "SHOP":
      return "Mã gian hàng";
    default:
      return scope || "Khuyến mãi";
  }
}

function resolveInitialTab(initialQuery: string): LoyaltyTab {
  const upper = initialQuery.trim().toUpperCase();
  if (upper.startsWith("NXGIFT")) return "the_qua_tang";
  if (upper.startsWith("SV-")) return "hoi_vien";
  return "flash_voucher";
}

function AdminLoyaltyWorkspace({
  initialVouchers,
  initialFlashSales,
  vouchersError,
  flashSalesError,
  initialQuery,
}: AdminLoyaltyClientProps & { initialQuery: string }) {
  const router = useRouter();
  const loyalty = useAdminLoyaltyState();
  const { showToast } = useAdminContext();

  const [activeTab, setActiveTab] = useState<LoyaltyTab>(() => resolveInitialTab(initialQuery));
  const [savedNotice, setSavedNotice] = useState<Record<string, string>>({});

  // Real backend state: vouchers & flash sales
  const [vouchers, setVouchers] = useState<GwAdminVoucher[]>(initialVouchers);
  const [flashSales, setFlashSales] = useState<GwAdminFlashSale[]>(initialFlashSales);

  // Real platform voucher creation form
  const [vcCode, setVcCode] = useState("");
  const [vcTitle, setVcTitle] = useState("");
  const [vcScope, setVcScope] = useState<"PLATFORM" | "FREESHIP">("PLATFORM");
  const [vcDiscountType, setVcDiscountType] = useState<"FIXED" | "PERCENT">("FIXED");
  const [vcDiscount, setVcDiscount] = useState(25000);
  const [vcPercent, setVcPercent] = useState(10);
  const [vcMaxDiscount, setVcMaxDiscount] = useState(50000);
  const [vcMinOrder, setVcMinOrder] = useState(299000);
  const [vcQuota, setVcQuota] = useState(500);
  const [vcBusy, setVcBusy] = useState(false);
  const [vcError, setVcError] = useState<string | null>(null);

  // Real flash sale window creation form
  const [fsName, setFsName] = useState("");
  const [fsStartLocal, setFsStartLocal] = useState(() => defaultFlashDateRange().startLocal);
  const [fsEndLocal, setFsEndLocal] = useState(() => defaultFlashDateRange().endLocal);
  const [fsBusy, setFsBusy] = useState(false);
  const [fsError, setFsError] = useState<string | null>(null);
  const [busyFlashItemId, setBusyFlashItemId] = useState<string | null>(null);

  // Tab 1 (mock) local drafts
  const [tiersDraft, setTiersDraft] = useState<AdminLoyaltyTierConfig[]>(loyalty.tiers);
  const [fridayDouble, setFridayDouble] = useState(loyalty.fridayDoubleEnabled);
  const [coinExpiryMonths, setCoinExpiryMonths] = useState(loyalty.coinExpiryMonths);
  const [clawbackEnabled, setClawbackEnabled] = useState(loyalty.clawbackOnReturnEnabled);
  const [referralDraft, setReferralDraft] = useState(loyalty.referral);
  const [studentDraft, setStudentDraft] = useState(loyalty.student);

  // Tab 2 (mock) Gift card issuer form
  const [gcCode, setGcCode] = useState("");
  const [gcAmount, setGcAmount] = useState(500000);
  const [gcPurpose, setGcPurpose] = useState("");
  const [gcMonths, setGcMonths] = useState(11);
  const [gcError, setGcError] = useState("");

  const markSaved = (key: string) => {
    setSavedNotice((prev) => ({ ...prev, [key]: `Đã lưu lúc ${formatNowHHMM()}` }));
  };

  const giftCardSummary = useMemo(
    () => computeGiftCardSummary(loyalty.giftCards, loyalty.giftCardLedger),
    [loyalty.giftCards, loyalty.giftCardLedger]
  );

  const flatFlashItems = useMemo(
    () =>
      flashSales.flatMap((fs) =>
        (fs.items ?? []).map((item) => ({
          flashSale: fs,
          item,
        }))
      ),
    [flashSales]
  );

  const pendingFlashCount = useMemo(
    () => flatFlashItems.filter((entry) => entry.item.status.toUpperCase() === "PENDING").length,
    [flatFlashItems]
  );

  const platformVouchers = useMemo(
    () =>
      [...vouchers].sort((a, b) => {
        const rank = (s: string) => (s.toUpperCase() === "SHOP" ? 1 : 0);
        return rank(a.scope) - rank(b.scope);
      }),
    [vouchers]
  );

  const pendingEduCount = useMemo(
    () => loyalty.student.verifications.filter((v) => v.status === "cho_duyet").length,
    [loyalty.student.verifications]
  );

  const totalMembers = useMemo(
    () => loyalty.tiers.reduce((sum, t) => sum + t.memberCount, 0),
    [loyalty.tiers]
  );

  // --- REAL Backend Tab handlers: Flash sales & Platform vouchers ---
  const handleCreateFlashSale = async (e: React.FormEvent) => {
    e.preventDefault();
    if (fsBusy) return;
    const cleanName = fsName.trim();
    const startsIso = localVnInputToIso(fsStartLocal);
    const endsIso = localVnInputToIso(fsEndLocal);
    if (!cleanName || !startsIso || !endsIso) {
      setFsError("Vui lòng nhập tên khung giờ và thời gian bắt đầu / kết thúc hợp lệ.");
      return;
    }
    if (new Date(endsIso).getTime() <= new Date(startsIso).getTime()) {
      setFsError("Thời gian kết thúc phải sau thời gian bắt đầu.");
      return;
    }
    setFsError(null);
    setFsBusy(true);
    try {
      const res = await api<{ flash_sale: GwAdminFlashSale }>("/api/admin/flash-sales", {
        method: "POST",
        body: {
          name: cleanName,
          starts_at: startsIso,
          ends_at: endsIso,
        },
      });
      const created: GwAdminFlashSale = {
        ...res.flash_sale,
        items: res.flash_sale.items ?? [],
      };
      setFlashSales((prev) => [created, ...prev]);
      setFsName("");
      showToast(`Đã mở khung giờ Flash sale "${created.name}".`);
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể tạo khung giờ Flash sale lúc này.";
      setFsError(msg);
      showToast(msg);
    } finally {
      setFsBusy(false);
    }
  };

  const handleFlashItemDecision = async (item: GwAdminFlashSaleItem, approve: boolean) => {
    if (busyFlashItemId) return;
    setBusyFlashItemId(item.id);
    try {
      const res = await api<{ item: GwAdminFlashSaleItem }>(
        `/api/admin/flash-sale-items/${encodeURIComponent(item.id)}/approve`,
        {
          method: "POST",
          body: {
            approve,
            reason: approve ? "" : "Không đạt tiêu chuẩn mức giá khuyến mãi khung giờ",
          },
        }
      );
      setFlashSales((prev) =>
        prev.map((fs) => ({
          ...fs,
          items: (fs.items ?? []).map((it) => (it.id === item.id ? { ...it, ...res.item } : it)),
        }))
      );
      showToast(
        approve
          ? `Đã duyệt suất Flash sale cho SKU ${item.sku_id.slice(0, 8)}.`
          : `Đã từ chối đề xuất Flash sale cho SKU ${item.sku_id.slice(0, 8)}.`
      );
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể cập nhật trạng thái suất Flash sale.";
      showToast(msg);
    } finally {
      setBusyFlashItemId(null);
    }
  };

  const handleAddPlatformVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    if (vcBusy) return;
    const code = vcCode.trim().toUpperCase().replace(/\s+/g, "");
    const title = vcTitle.trim() || code;
    const cleanQuota = Math.trunc(vcQuota);
    if (!code) {
      setVcError("Vui lòng nhập mã giảm giá cấp sàn (VD: NEXUS40).");
      return;
    }
    if (cleanQuota <= 0) {
      setVcError("Tổng số suất phát hành phải lớn hơn 0.");
      return;
    }
    if (vcDiscountType === "FIXED" && Math.trunc(vcDiscount) < 1000) {
      setVcError("Mức giảm cố định tối thiểu là 1.000₫.");
      return;
    }
    if (vcDiscountType === "PERCENT" && (vcPercent < 1 || vcPercent > 90)) {
      setVcError("Phần trăm giảm giá hợp lệ từ 1% đến 90%.");
      return;
    }

    setVcError(null);
    setVcBusy(true);
    try {
      const res = await api<{ voucher: GwAdminVoucher }>("/api/admin/vouchers", {
        method: "POST",
        body: {
          code,
          name: title,
          scope: vcScope,
          discount_type: vcDiscountType,
          value_vnd: vcDiscountType === "FIXED" ? Math.trunc(vcDiscount) : 0,
          discount_percent: vcDiscountType === "PERCENT" ? Math.trunc(vcPercent) : 0,
          max_discount_vnd:
            vcDiscountType === "PERCENT" ? Math.max(0, Math.trunc(vcMaxDiscount)) : 0,
          min_order_vnd: Math.max(0, Math.trunc(vcMinOrder)),
          quota: cleanQuota,
          per_user_limit: 1,
        },
      });
      setVouchers((prev) => [res.voucher, ...prev]);
      setVcCode("");
      setVcTitle("");
      showToast(`Đã tạo mã sàn ${res.voucher.code} (${formatInt(res.voucher.quota)} suất).`);
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể tạo mã giảm giá cấp sàn lúc này.";
      setVcError(msg);
      showToast(msg);
    } finally {
      setVcBusy(false);
    }
  };

  // --- Mock Tab 1 handlers ---
  const handleTierFieldChange = (
    id: string,
    field: "minSpend4QuartersVnd" | "platformCoFundPercent" | "shopCoFundPercent",
    value: number
  ) => {
    setTiersDraft((prev) =>
      prev.map((tier) => {
        if (tier.id !== id) return tier;
        const cleanVal = Math.max(0, value);
        if (field === "minSpend4QuartersVnd") {
          return { ...tier, minSpend4QuartersVnd: Math.trunc(cleanVal) };
        }
        const nextPlatform =
          field === "platformCoFundPercent" ? cleanVal : tier.platformCoFundPercent;
        const nextShop = field === "shopCoFundPercent" ? cleanVal : tier.shopCoFundPercent;
        const totalCashback = Math.round((nextPlatform + nextShop) * 10) / 10;
        return {
          ...tier,
          platformCoFundPercent: nextPlatform,
          shopCoFundPercent: nextShop,
          cashbackPercent: totalCashback,
        };
      })
    );
  };

  const handleSaveTiers = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanExpiry = Math.max(3, Math.min(24, Math.trunc(coinExpiryMonths)));
    updateAdminLoyaltyState((prev) => ({
      ...prev,
      tiers: tiersDraft,
      fridayDoubleEnabled: fridayDouble,
      coinExpiryMonths: cleanExpiry,
      clawbackOnReturnEnabled: clawbackEnabled,
    }));
    appendAdminAuditLog({
      role: "Quản trị viên",
      action: "Cập nhật bậc hạng Hội viên Nexus & tỷ lệ đồng tài trợ Xu",
      module: "Hội viên",
      target: "NEXUS-LOYALTY-TIERS",
      changeSummary: `Cập nhật 4 hạng xét 4 quý, x2 Thứ Sáu: ${fridayDouble ? "Bật" : "Tắt"}, hạn Xu ${cleanExpiry} tháng.`,
    });
    markSaved("tiers");
    showToast("Đã lưu cấu hình 4 hạng Hội viên Nexus (mẫu cục bộ).");
  };

  const handleSaveReferral = (e: React.FormEvent) => {
    e.preventDefault();
    updateAdminLoyaltyState((prev) => ({
      ...prev,
      referral: {
        ...referralDraft,
        rewardPercent: Math.max(1, Math.min(20, Math.trunc(referralDraft.rewardPercent))),
        maxRewardPerOrderVnd: Math.max(10000, Math.trunc(referralDraft.maxRewardPerOrderVnd)),
        maxReferralsPerMonth: Math.max(1, Math.trunc(referralDraft.maxReferralsPerMonth)),
        minInviteeOrderVnd: Math.max(0, Math.trunc(referralDraft.minInviteeOrderVnd)),
      },
    }));
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: "Cập nhật hạn mức Giới thiệu bạn bè chống trục lợi",
      module: "Hội viên",
      target: "REFERRAL-POLICY",
      changeSummary: `Hoàn ${referralDraft.rewardPercent}% (tối đa ${formatVnd(referralDraft.maxRewardPerOrderVnd)}/đơn), trần ${referralDraft.maxReferralsPerMonth} lượt/tháng.`,
    });
    markSaved("referral");
    showToast("Đã lưu hạn mức Giới thiệu bạn bè (mẫu cục bộ).");
  };

  const handleSaveStudent = (e: React.FormEvent) => {
    e.preventDefault();
    updateAdminLoyaltyState((prev) => ({
      ...prev,
      student: {
        ...prev.student,
        enabled: studentDraft.enabled,
        allowedDomains: studentDraft.allowedDomains.trim() || ".edu.vn, .edu",
        discountPercent: Math.max(1, Math.min(25, Math.trunc(studentDraft.discountPercent))),
        maxDiscountVnd: Math.max(10000, Math.trunc(studentDraft.maxDiscountVnd)),
        monthlyOrderLimit: Math.max(1, Math.trunc(studentDraft.monthlyOrderLimit)),
        reverifyMonths: Math.max(3, Math.trunc(studentDraft.reverifyMonths)),
      },
    }));
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: "Cập nhật chương trình Ưu đãi sinh viên .edu",
      module: "Hội viên",
      target: "STUDENT-EDU",
      changeSummary: `${studentDraft.enabled ? "Bật" : "Tắt"} ưu đãi ${studentDraft.discountPercent}% (tối đa ${formatVnd(studentDraft.maxDiscountVnd)}), tên miền ${studentDraft.allowedDomains}.`,
    });
    markSaved("student");
    showToast("Đã lưu cấu hình Ưu đãi sinh viên .edu (mẫu cục bộ).");
  };

  const handleStudentVerifDecision = (
    item: AdminStudentVerificationItem,
    nextStatus: "da_xac_thuc" | "tu_choi"
  ) => {
    updateAdminLoyaltyState((prev) => ({
      ...prev,
      student: {
        ...prev.student,
        verifications: prev.student.verifications.map((v) =>
          v.id === item.id ? { ...v, status: nextStatus } : v
        ),
      },
    }));
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action:
        nextStatus === "da_xac_thuc"
          ? "Xác thực tài khoản sinh viên .edu"
          : "Từ chối xác thực email sinh viên",
      module: "Hội viên",
      target: item.code,
      changeSummary: `${item.studentName} (${item.eduEmail} · ${item.university}).`,
    });
    showToast(
      nextStatus === "da_xac_thuc"
        ? `Đã xác thực sinh viên ${item.studentName}.`
        : `Đã từ chối hồ sơ ${item.code}.`
    );
  };

  // --- Mock Tab 2 handlers: Gift card double-entry VND ---
  const handleIssueGiftCard = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = gcCode.trim().toUpperCase().replace(/\s+/g, "-");
    const cleanAmount = Math.trunc(gcAmount);
    const cleanMonths = Math.max(1, Math.min(11, Math.trunc(gcMonths)));
    if (!cleanCode) {
      setGcError("Vui lòng nhập mã Thẻ quà tặng tùy biến (VD: NXGIFT-NOEL-07).");
      return;
    }
    if (loyalty.giftCards.some((c) => c.code === cleanCode)) {
      setGcError(`Mã thẻ ${cleanCode} đã tồn tại trên sổ cái.`);
      return;
    }
    if (cleanAmount < 50000) {
      setGcError("Mệnh giá phát hành tối thiểu là 50.000₫ (số nguyên VND).");
      return;
    }
    setGcError("");

    const issueDate = "2026-11-20";
    const expDate = new Date("2026-11-20T00:00:00+07:00");
    expDate.setMonth(expDate.getMonth() + cleanMonths);
    const yyyy = expDate.getFullYear();
    const mm =
      expDate.getMonth() + 1 < 10 ? `0${expDate.getMonth() + 1}` : `${expDate.getMonth() + 1}`;
    const dd = expDate.getDate() < 10 ? `0${expDate.getDate()}` : `${expDate.getDate()}`;
    const expiresAt = `${yyyy}-${mm}-${dd}`;
    const purpose =
      gcPurpose.trim() || `Phát hành thẻ quà tặng tiêu toàn sàn (hạn ${cleanMonths} tháng)`;
    const entrySeq = loyalty.giftCardLedger.length + 1;
    const entryCode = `BT-GC-${entrySeq < 10 ? `00${entrySeq}` : entrySeq < 100 ? `0${entrySeq}` : String(entrySeq)}`;

    updateAdminLoyaltyState((prev) => ({
      ...prev,
      giftCards: [
        {
          id: `gc-${Date.now()}`,
          code: cleanCode,
          purpose,
          issuedAt: issueDate,
          expiresAt,
          issuedAmount: cleanAmount,
          redeemedAmount: 0,
          expiredAmount: 0,
          remainingAmount: cleanAmount,
          status: "dang_luu_hanh",
        },
        ...prev.giftCards,
      ],
      giftCardLedger: [
        {
          id: `gcl-${Date.now()}`,
          entryCode,
          date: issueDate,
          cardCode: cleanCode,
          entryType: "phat_hanh",
          debitAccount: "TK 112 Tiền thu trước",
          creditAccount: "TK 3387 Quỹ Thẻ quà tặng",
          creditVnd: cleanAmount,
          debitVnd: 0,
          note: `${purpose} · Hạn đến ${formatDateVi(expiresAt)}`,
        },
        ...prev.giftCardLedger,
      ],
    }));

    appendAdminAuditLog({
      role: "Đối soát tài chính",
      action: "Phát hành mã Thẻ quà tặng toàn sàn",
      module: "Hội viên",
      target: cleanCode,
      changeSummary: `Phát hành ${formatVnd(cleanAmount)} (hạn ${cleanMonths} tháng), ghi Có TK 3387 (${entryCode}).`,
    });

    setGcCode("");
    setGcPurpose("");
    showToast(`Đã phát hành thẻ ${cleanCode} (${formatVnd(cleanAmount)}) trong sổ mẫu.`);
  };

  const handleExpireGiftCard = (card: AdminGiftCardRecord) => {
    if (card.remainingAmount <= 0 || card.status !== "dang_luu_hanh") return;
    const settleAmount = card.remainingAmount;
    const entrySeq = loyalty.giftCardLedger.length + 1;
    const entryCode = `BT-GC-${entrySeq < 10 ? `00${entrySeq}` : entrySeq < 100 ? `0${entrySeq}` : String(entrySeq)}`;

    updateAdminLoyaltyState((prev) => ({
      ...prev,
      giftCards: prev.giftCards.map((c) =>
        c.id === card.id
          ? {
              ...c,
              expiredAmount: c.expiredAmount + settleAmount,
              remainingAmount: 0,
              status: "het_han",
            }
          : c
      ),
      giftCardLedger: [
        {
          id: `gcl-exp-${Date.now()}`,
          entryCode,
          date: "2026-11-20",
          cardCode: card.code,
          entryType: "het_han",
          debitAccount: "TK 3387 Quỹ Thẻ quà tặng",
          creditAccount: "TK 711 Thu nhập thẻ hết hạn",
          creditVnd: 0,
          debitVnd: settleAmount,
          note: `Tất toán số dư hết hạn ${formatVnd(settleAmount)} cho mã ${card.code}`,
        },
        ...prev.giftCardLedger,
      ],
    }));

    appendAdminAuditLog({
      role: "Đối soát tài chính",
      action: "Tất toán hết hạn Thẻ quà tặng",
      module: "Hội viên",
      target: card.code,
      changeSummary: `Ghi Nợ TK 3387 số dư ${formatVnd(settleAmount)} (${entryCode}), chuyển trạng thái Đã hết hạn.`,
    });
    showToast(`Đã tất toán hết hạn mã ${card.code} (${formatVnd(settleAmount)}).`);
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Hội viên, khuyến mãi sàn & thẻ quà tặng</h1>
          <p className={shared.pageSubtitle}>
            Quản trị {formatInt(platformVouchers.length)} mã giảm giá,{" "}
            {formatInt(flashSales.length)} khung giờ Flash sale ({formatInt(pendingFlashCount)} suất
            chờ duyệt) trên hệ thống thực, cùng cấu hình mẫu Hội viên Nexus (
            {formatInt(totalMembers)} tài khoản) và Thẻ quà tặng.
          </p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phân mục hội viên và khuyến mãi">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "flash_voucher"}
            className={`${shared.tabBtn} ${activeTab === "flash_voucher" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("flash_voucher")}
          >
            <span>Mã sàn & Flash sale</span>
            {pendingFlashCount > 0 && <span className={shared.tabCount}>{pendingFlashCount}</span>}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "hoi_vien"}
            className={`${shared.tabBtn} ${activeTab === "hoi_vien" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("hoi_vien")}
          >
            <span>Hội viên Nexus (mẫu)</span>
            {pendingEduCount > 0 && <span className={shared.tabCount}>{pendingEduCount}</span>}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "the_qua_tang"}
            className={`${shared.tabBtn} ${activeTab === "the_qua_tang" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("the_qua_tang")}
          >
            <span>Thẻ quà tặng (mẫu)</span>
            <span className={shared.tabCount}>{loyalty.giftCards.length}</span>
          </button>
        </div>
      </div>

      {/* TAB 1 (REAL): MÃ SÀN & FLASH SALE */}
      {activeTab === "flash_voucher" && (
        <div className={styles.stack}>
          {/* 3A: Tạo & duyệt khung giờ Flash sale */}
          <section className={shared.panel} aria-labelledby="tv-flash-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-flash-heading" className={shared.panelTitle}>
                Khung giờ Flash sale & duyệt suất gian hàng
              </h2>
              <span className={shared.panelMeta}>
                {formatInt(flashSales.length)} khung giờ · {formatInt(pendingFlashCount)} suất chờ
                duyệt
              </span>
            </div>

            {flashSalesError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải danh sách khung giờ Flash sale"
                message={flashSalesError}
                actionLabel="Thử tải lại"
                onAction={() => router.refresh()}
              />
            ) : (
              <>
                <form className={styles.issueForm} onSubmit={handleCreateFlashSale}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="fs-name" className={shared.fieldLabel}>
                      Tên khung giờ Flash sale
                    </label>
                    <input
                      id="fs-name"
                      type="text"
                      className={shared.textInput}
                      value={fsName}
                      onChange={(e) => {
                        setFsName(e.target.value);
                        if (fsError) setFsError(null);
                      }}
                      placeholder="VD: Flash sale trưa 12H-14H"
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="fs-start" className={shared.fieldLabel}>
                      Bắt đầu (giờ VN)
                    </label>
                    <input
                      id="fs-start"
                      type="datetime-local"
                      className={shared.textInput}
                      value={fsStartLocal}
                      onChange={(e) => setFsStartLocal(e.target.value)}
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="fs-end" className={shared.fieldLabel}>
                      Kết thúc (giờ VN)
                    </label>
                    <input
                      id="fs-end"
                      type="datetime-local"
                      className={shared.textInput}
                      value={fsEndLocal}
                      onChange={(e) => setFsEndLocal(e.target.value)}
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <span className={shared.fieldLabel}>Trạng thái</span>
                    <span className={shared.cellSub}>Mở cho gian hàng đăng ký SKU</span>
                  </div>
                  <Button type="submit" variant="primary" size="sm" shape="app" disabled={fsBusy}>
                    {fsBusy ? "Đang tạo..." : "Mở khung giờ"}
                  </Button>
                </form>
                {fsError && <p className={shared.validationError}>{fsError}</p>}

                {flashSales.length === 0 ? (
                  <p className={shared.emptyRow}>
                    Chưa có khung giờ Flash sale nào đang chạy hoặc sắp diễn ra.
                  </p>
                ) : (
                  <>
                    <div className={shared.tableWrap}>
                      <table className={shared.dataTable}>
                        <thead>
                          <tr>
                            <th>Khung giờ Flash sale</th>
                            <th>Thời gian áp dụng</th>
                            <th className={shared.colNum}>SKU đã đăng ký</th>
                            <th className={shared.colNum}>Đã duyệt</th>
                          </tr>
                        </thead>
                        <tbody>
                          {flashSales.map((fs) => {
                            const items = fs.items ?? [];
                            const approvedCount = items.filter(
                              (it) => it.status.toUpperCase() === "APPROVED"
                            ).length;
                            return (
                              <tr key={fs.id}>
                                <td>
                                  <div className={shared.cellPrimary}>{fs.name}</div>
                                  <div className={shared.cellSub}>ID {fs.id.slice(0, 8)}</div>
                                </td>
                                <td>
                                  <div>
                                    {fs.starts_at ? formatDateTimeVi(fs.starts_at) : "Chưa đặt"}
                                  </div>
                                  <div className={shared.cellSub}>
                                    đến {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "Chưa đặt"}
                                  </div>
                                </td>
                                <td className={shared.colNum}>{formatInt(items.length)}</td>
                                <td className={shared.colNum}>{formatInt(approvedCount)}</td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>

                    {flatFlashItems.length === 0 ? (
                      <p className={shared.emptyRow}>
                        Chưa có gian hàng nào gửi đề xuất SKU vào các khung giờ trên.
                      </p>
                    ) : (
                      <>
                        <div className={shared.tableWrap}>
                          <table className={shared.dataTable}>
                            <thead>
                              <tr>
                                <th>Khung giờ & SKU đăng ký</th>
                                <th>Gian hàng</th>
                                <th className={shared.colNum}>Giá Flash</th>
                                <th className={shared.colNum}>Suất còn / Tổng</th>
                                <th>Trạng thái</th>
                                <th className={shared.colAction}>Thao tác</th>
                              </tr>
                            </thead>
                            <tbody>
                              {flatFlashItems.map(({ flashSale, item }) => {
                                const meta = getFlashItemStatusMeta(item.status);
                                const isBusy = busyFlashItemId === item.id;
                                return (
                                  <tr key={item.id}>
                                    <td>
                                      <div className={shared.cellPrimary}>{flashSale.name}</div>
                                      <div className={shared.cellSub}>
                                        SKU {item.sku_id.slice(0, 8)} · tối đa {item.per_user_limit}{" "}
                                        SP/người
                                      </div>
                                    </td>
                                    <td>Shop {item.shop_id.slice(0, 8)}</td>
                                    <td className={shared.colNum}>
                                      <span className={shared.cellPrimary}>
                                        {formatVnd(item.flash_price_vnd)}
                                      </span>
                                    </td>
                                    <td className={shared.colNum}>
                                      {formatInt(item.remaining_slots)} /{" "}
                                      {formatInt(item.total_slots)}
                                    </td>
                                    <td>
                                      <StatusPill label={meta.label} tone={meta.tone} />
                                      {item.rejection_reason && (
                                        <div className={shared.cellSub}>
                                          {item.rejection_reason}
                                        </div>
                                      )}
                                    </td>
                                    <td className={shared.colAction}>
                                      <div className={styles.actionInline}>
                                        <button
                                          type="button"
                                          className={shared.rowActionBtn}
                                          disabled={
                                            isBusy || item.status.toUpperCase() === "APPROVED"
                                          }
                                          onClick={() => void handleFlashItemDecision(item, true)}
                                        >
                                          Duyệt suất
                                        </button>
                                        <button
                                          type="button"
                                          className={shared.rowActionBtn}
                                          disabled={
                                            isBusy || item.status.toUpperCase() === "REJECTED"
                                          }
                                          onClick={() => void handleFlashItemDecision(item, false)}
                                        >
                                          Từ chối
                                        </button>
                                      </div>
                                    </td>
                                  </tr>
                                );
                              })}
                            </tbody>
                          </table>
                        </div>

                        <div className={shared.mobileList}>
                          {flatFlashItems.map(({ flashSale, item }) => {
                            const meta = getFlashItemStatusMeta(item.status);
                            const isBusy = busyFlashItemId === item.id;
                            return (
                              <div key={item.id} className={shared.mobileItem}>
                                <div className={shared.mobileLine1}>
                                  <span className={shared.mobileIdentity}>{flashSale.name}</span>
                                  <span className={shared.mobileMetric}>
                                    {formatVnd(item.flash_price_vnd)}
                                  </span>
                                </div>
                                <div className={shared.mobileLine2}>
                                  <span className={shared.mobileMetaText}>
                                    SKU {item.sku_id.slice(0, 8)} ·{" "}
                                    {formatInt(item.remaining_slots)}/{formatInt(item.total_slots)}{" "}
                                    suất
                                  </span>
                                  <StatusPill label={meta.label} tone={meta.tone} />
                                </div>
                                {item.status.toUpperCase() === "PENDING" && (
                                  <div className={shared.mobileLine2}>
                                    <button
                                      type="button"
                                      className={shared.rowActionBtn}
                                      disabled={isBusy}
                                      onClick={() => void handleFlashItemDecision(item, true)}
                                    >
                                      Duyệt suất
                                    </button>
                                    <button
                                      type="button"
                                      className={shared.rowActionBtn}
                                      disabled={isBusy}
                                      onClick={() => void handleFlashItemDecision(item, false)}
                                    >
                                      Từ chối
                                    </button>
                                  </div>
                                )}
                              </div>
                            );
                          })}
                        </div>
                      </>
                    )}
                  </>
                )}
              </>
            )}
          </section>

          {/* 3B: Quản trị mã giảm giá cấp sàn */}
          <section className={shared.panel} aria-labelledby="tv-vc-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-vc-heading" className={shared.panelTitle}>
                Mã giảm giá cấp sàn (chồng được 1 mã gian hàng)
              </h2>
              <span className={shared.panelMeta}>
                {formatInt(platformVouchers.filter((v) => v.is_active).length)}/
                {formatInt(platformVouchers.length)} mã đang hoạt động
              </span>
            </div>

            {vouchersError ? (
              <StateView
                kind="error"
                surface="app"
                title="Không thể tải danh sách mã giảm giá"
                message={vouchersError}
                actionLabel="Thử tải lại"
                onAction={() => router.refresh()}
              />
            ) : (
              <>
                <form className={styles.voucherForm} onSubmit={handleAddPlatformVoucher}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="vc-code" className={shared.fieldLabel}>
                      Mã sàn
                    </label>
                    <input
                      id="vc-code"
                      type="text"
                      className={shared.textInput}
                      value={vcCode}
                      onChange={(e) => {
                        setVcCode(e.target.value);
                        if (vcError) setVcError(null);
                      }}
                      placeholder="VD: NEXUS40"
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="vc-title" className={shared.fieldLabel}>
                      Mô tả ngắn
                    </label>
                    <input
                      id="vc-title"
                      type="text"
                      className={shared.textInput}
                      value={vcTitle}
                      onChange={(e) => setVcTitle(e.target.value)}
                      placeholder="Giảm 40.000₫ đơn cơ bản"
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="vc-scope" className={shared.fieldLabel}>
                      Phạm vi
                    </label>
                    <select
                      id="vc-scope"
                      className={shared.toolbarSelect}
                      value={vcScope}
                      onChange={(e) => setVcScope(e.target.value as "PLATFORM" | "FREESHIP")}
                    >
                      <option value="PLATFORM">Giảm đơn sàn</option>
                      <option value="FREESHIP">Freeship sàn</option>
                    </select>
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="vc-type" className={shared.fieldLabel}>
                      Loại giảm
                    </label>
                    <select
                      id="vc-type"
                      className={shared.toolbarSelect}
                      value={vcDiscountType}
                      onChange={(e) => setVcDiscountType(e.target.value as "FIXED" | "PERCENT")}
                    >
                      <option value="FIXED">Giảm tiền (VND)</option>
                      <option value="PERCENT">Giảm theo %</option>
                    </select>
                  </div>
                  {vcDiscountType === "FIXED" ? (
                    <div className={styles.fieldGroup}>
                      <label htmlFor="vc-disc" className={shared.fieldLabel}>
                        Mức giảm (VND)
                      </label>
                      <input
                        id="vc-disc"
                        type="number"
                        min={1000}
                        step={5000}
                        className={shared.textInput}
                        value={vcDiscount}
                        onChange={(e) => setVcDiscount(Number(e.target.value) || 0)}
                      />
                    </div>
                  ) : (
                    <div className={styles.fieldGroup}>
                      <label htmlFor="vc-pct" className={shared.fieldLabel}>
                        Giảm (%) · trần {formatVnd(vcMaxDiscount)}
                      </label>
                      <input
                        id="vc-pct"
                        type="number"
                        min={1}
                        max={90}
                        className={shared.textInput}
                        value={vcPercent}
                        onChange={(e) => {
                          setVcPercent(Number(e.target.value) || 0);
                          if (!vcMaxDiscount) setVcMaxDiscount(50000);
                        }}
                      />
                    </div>
                  )}
                  <Button type="submit" variant="primary" size="sm" shape="app" disabled={vcBusy}>
                    {vcBusy ? "Đang tạo..." : "Tạo mã sàn"}
                  </Button>
                </form>

                <div className={styles.formGrid} style={{ marginBottom: 14 }}>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="vc-min" className={shared.fieldLabel}>
                      Đơn tối thiểu (VND)
                    </label>
                    <input
                      id="vc-min"
                      type="number"
                      min={0}
                      step={50000}
                      className={shared.textInput}
                      value={vcMinOrder}
                      onChange={(e) => setVcMinOrder(Number(e.target.value) || 0)}
                    />
                  </div>
                  <div className={styles.fieldGroup}>
                    <label htmlFor="vc-quota" className={shared.fieldLabel}>
                      Tổng số suất phát hành
                    </label>
                    <input
                      id="vc-quota"
                      type="number"
                      min={1}
                      step={50}
                      className={shared.textInput}
                      value={vcQuota}
                      onChange={(e) => setVcQuota(Number(e.target.value) || 100)}
                    />
                  </div>
                  {vcDiscountType === "PERCENT" && (
                    <div className={styles.fieldGroup}>
                      <label htmlFor="vc-max" className={shared.fieldLabel}>
                        Giảm tối đa (VND)
                      </label>
                      <input
                        id="vc-max"
                        type="number"
                        min={0}
                        step={10000}
                        className={shared.textInput}
                        value={vcMaxDiscount}
                        onChange={(e) => setVcMaxDiscount(Number(e.target.value) || 0)}
                      />
                    </div>
                  )}
                </div>
                {vcError && <p className={shared.validationError}>{vcError}</p>}

                {platformVouchers.length === 0 ? (
                  <p className={shared.emptyRow}>Chưa có mã giảm giá nào đang phát hành.</p>
                ) : (
                  <>
                    <div className={shared.tableWrap}>
                      <table className={shared.dataTable}>
                        <thead>
                          <tr>
                            <th>Mã & mô tả</th>
                            <th>Phạm vi</th>
                            <th className={shared.colNum}>Mức giảm / Đơn tối thiểu</th>
                            <th className={shared.colNum}>Đã dùng / Tổng suất</th>
                            <th>Trạng thái</th>
                          </tr>
                        </thead>
                        <tbody>
                          {platformVouchers.map((v) => {
                            const discountLabel =
                              v.discount_type.toUpperCase() === "PERCENT"
                                ? `Giảm ${v.discount_percent}%${v.max_discount_vnd > 0 ? ` (tối đa ${formatVnd(v.max_discount_vnd)})` : ""}`
                                : formatVnd(v.value_vnd);
                            return (
                              <tr key={v.id}>
                                <td>
                                  <div className={shared.cellPrimary}>{v.code}</div>
                                  <div className={shared.cellSub}>{v.name || v.code}</div>
                                </td>
                                <td>{getVoucherScopeLabel(v.scope)}</td>
                                <td className={shared.colNum}>
                                  <div className={shared.cellPrimary}>{discountLabel}</div>
                                  <div className={shared.cellSub}>
                                    Đơn từ {formatVnd(v.min_order_vnd)}
                                  </div>
                                </td>
                                <td className={shared.colNum}>
                                  <div>
                                    {formatInt(v.used_count)} / {formatInt(v.quota)}
                                  </div>
                                  <div className={shared.cellSub}>
                                    Còn {formatInt(v.remaining_quota)} suất
                                  </div>
                                </td>
                                <td>
                                  <StatusPill
                                    label={v.is_active ? "Đang chạy" : "Ngưng hoạt động"}
                                    tone={v.is_active ? "positive" : "neutral"}
                                  />
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>

                    <div className={shared.mobileList}>
                      {platformVouchers.map((v) => {
                        const discountLabel =
                          v.discount_type.toUpperCase() === "PERCENT"
                            ? `Giảm ${v.discount_percent}%`
                            : formatVnd(v.value_vnd);
                        return (
                          <div key={v.id} className={shared.mobileItem}>
                            <div className={shared.mobileLine1}>
                              <span className={shared.mobileIdentity}>
                                {v.code} · {discountLabel}
                              </span>
                              <StatusPill
                                label={v.is_active ? "Đang chạy" : "Ngưng"}
                                tone={v.is_active ? "positive" : "neutral"}
                              />
                            </div>
                            <div className={shared.mobileLine2}>
                              <span className={shared.mobileMetaText}>
                                {getVoucherScopeLabel(v.scope)} · {formatInt(v.used_count)}/
                                {formatInt(v.quota)} suất
                              </span>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </>
                )}
              </>
            )}
          </section>
        </div>
      )}

      {/* TAB 2 (MOCK): HỘI VIÊN NEXUS, GIỚI THIỆU & SINH VIÊN .EDU */}
      {activeTab === "hoi_vien" && (
        <div className={styles.stack}>
          {/* 1A: 4 hạng Hội viên Nexus */}
          <section className={shared.panel} aria-labelledby="tv-tiers-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-tiers-heading" className={shared.panelTitle}>
                Ngưỡng 4 hạng Hội viên Nexus (xét 4 quý) & đồng tài trợ Xu
              </h2>
              <span className={shared.panelMeta}>
                Dữ liệu mẫu cục bộ · 1 Xu = 1₫ · tiêu chéo mọi gian hàng
              </span>
            </div>

            <form onSubmit={handleSaveTiers}>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Hạng thành viên</th>
                      <th className={shared.colNum}>Chi tiêu 4 quý (VND)</th>
                      <th className={shared.colNum}>Sàn tài trợ (%)</th>
                      <th className={shared.colNum}>Shop đồng tài trợ (%)</th>
                      <th className={shared.colNum}>Tổng hoàn Xu</th>
                      <th className={shared.colNum}>Hội viên</th>
                    </tr>
                  </thead>
                  <tbody>
                    {tiersDraft.map((tier) => (
                      <tr key={tier.id}>
                        <td>
                          <div className={shared.cellPrimary}>{tier.tierName}</div>
                          <div className={shared.cellSub}>{tier.perksSummary}</div>
                        </td>
                        <td className={shared.colNum}>
                          <input
                            type="number"
                            min={0}
                            step={500000}
                            className={styles.compactNum}
                            value={tier.minSpend4QuartersVnd}
                            onChange={(e) =>
                              handleTierFieldChange(
                                tier.id,
                                "minSpend4QuartersVnd",
                                Number(e.target.value) || 0
                              )
                            }
                            aria-label={`Ngưỡng chi tiêu 4 quý hạng ${tier.tierName}`}
                          />
                        </td>
                        <td className={shared.colNum}>
                          <input
                            type="number"
                            min={0}
                            max={10}
                            step={0.1}
                            className={styles.compactPct}
                            value={tier.platformCoFundPercent}
                            onChange={(e) =>
                              handleTierFieldChange(
                                tier.id,
                                "platformCoFundPercent",
                                Number(e.target.value) || 0
                              )
                            }
                            aria-label={`Tỷ lệ sàn tài trợ hạng ${tier.tierName}`}
                          />
                        </td>
                        <td className={shared.colNum}>
                          <input
                            type="number"
                            min={0}
                            max={10}
                            step={0.1}
                            className={styles.compactPct}
                            value={tier.shopCoFundPercent}
                            onChange={(e) =>
                              handleTierFieldChange(
                                tier.id,
                                "shopCoFundPercent",
                                Number(e.target.value) || 0
                              )
                            }
                            aria-label={`Tỷ lệ gian hàng đồng tài trợ hạng ${tier.tierName}`}
                          />
                        </td>
                        <td className={shared.colNum}>
                          <span className={shared.cellPrimary}>
                            {formatPercent(tier.cashbackPercent, 1, false)}
                          </span>
                        </td>
                        <td className={shared.colNum}>{formatInt(tier.memberCount)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {tiersDraft.map((tier) => (
                  <div key={tier.id} className={styles.tierMobileCard}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{tier.tierName}</span>
                      <span className={shared.mobileMetric}>
                        Hoàn {formatPercent(tier.cashbackPercent, 1, false)} Xu
                      </span>
                    </div>
                    <div className={styles.tierMobileInputs}>
                      <div className={styles.fieldGroup}>
                        <label htmlFor={`mt-sp-${tier.id}`} className={shared.detailKey}>
                          Ngưỡng 4 quý (₫)
                        </label>
                        <input
                          id={`mt-sp-${tier.id}`}
                          type="number"
                          min={0}
                          step={500000}
                          className={shared.textInput}
                          value={tier.minSpend4QuartersVnd}
                          onChange={(e) =>
                            handleTierFieldChange(
                              tier.id,
                              "minSpend4QuartersVnd",
                              Number(e.target.value) || 0
                            )
                          }
                        />
                      </div>
                      <div className={styles.fieldGroup}>
                        <label htmlFor={`mt-pl-${tier.id}`} className={shared.detailKey}>
                          Sàn (%)
                        </label>
                        <input
                          id={`mt-pl-${tier.id}`}
                          type="number"
                          min={0}
                          max={10}
                          step={0.1}
                          className={shared.textInput}
                          value={tier.platformCoFundPercent}
                          onChange={(e) =>
                            handleTierFieldChange(
                              tier.id,
                              "platformCoFundPercent",
                              Number(e.target.value) || 0
                            )
                          }
                        />
                      </div>
                      <div className={styles.fieldGroup}>
                        <label htmlFor={`mt-sh-${tier.id}`} className={shared.detailKey}>
                          Shop (%)
                        </label>
                        <input
                          id={`mt-sh-${tier.id}`}
                          type="number"
                          min={0}
                          max={10}
                          step={0.1}
                          className={shared.textInput}
                          value={tier.shopCoFundPercent}
                          onChange={(e) =>
                            handleTierFieldChange(
                              tier.id,
                              "shopCoFundPercent",
                              Number(e.target.value) || 0
                            )
                          }
                        />
                      </div>
                    </div>
                  </div>
                ))}
              </div>

              <div className={styles.switchList}>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <span className={styles.switchTitle}>Nhân đôi hoàn Xu ngày Thứ Sáu (x2)</span>
                    <span className={styles.switchDesc}>
                      Tự động nhân đôi tỷ lệ tích Xu cho mọi đơn hợp lệ đặt vào Thứ Sáu hằng tuần.
                    </span>
                  </div>
                  <ToggleSwitch
                    id="sw-friday-x2"
                    checked={fridayDouble}
                    label="Bật hoặc tắt nhân đôi Xu Thứ Sáu"
                    onChange={setFridayDouble}
                  />
                </div>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <span className={styles.switchTitle}>Tự động thu hồi Xu khi đơn hoàn trả</span>
                    <span className={styles.switchDesc}>
                      Khấu trừ lại số Xu đã cộng tương ứng nếu đơn hàng phát sinh hoàn tiền toàn
                      phần hoặc một phần.
                    </span>
                  </div>
                  <ToggleSwitch
                    id="sw-clawback"
                    checked={clawbackEnabled}
                    label="Bật hoặc tắt thu hồi Xu khi hoàn trả"
                    onChange={setClawbackEnabled}
                  />
                </div>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <label htmlFor="coin-exp-months" className={styles.switchTitle}>
                      Thời hạn sử dụng Xu (tháng)
                    </label>
                    <span className={styles.switchDesc}>
                      Mặc định 12 tháng kể từ ngày ghi có vào ví Hội viên Nexus.
                    </span>
                  </div>
                  <input
                    id="coin-exp-months"
                    type="number"
                    min={3}
                    max={24}
                    className={styles.compactPct}
                    value={coinExpiryMonths}
                    onChange={(e) => setCoinExpiryMonths(Number(e.target.value) || 12)}
                  />
                </div>
              </div>

              <div className={styles.formFooter}>
                <span className={shared.savedStamp}>
                  {savedNotice.tiers ?? "Quà sinh nhật tự động kích hoạt từ hạng Bạc trở lên."}
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app">
                  Lưu cấu hình Hội viên
                </Button>
              </div>
            </form>
          </section>

          {/* 1B: Hạn mức Giới thiệu bạn bè chống trục lợi */}
          <section className={shared.panel} aria-labelledby="tv-ref-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-ref-heading" className={shared.panelTitle}>
                Giới thiệu bạn bè & hạn mức chống trục lợi
              </h2>
              <ToggleSwitch
                id="sw-ref-enabled"
                checked={referralDraft.enabled}
                label="Bật hoặc tắt chương trình Giới thiệu bạn bè"
                onChange={(next) => setReferralDraft((p) => ({ ...p, enabled: next }))}
              />
            </div>

            <form onSubmit={handleSaveReferral}>
              <div className={styles.formGridFour}>
                <div className={styles.fieldGroup}>
                  <label htmlFor="ref-pct" className={shared.fieldLabel}>
                    Hoàn Xu đơn đầu (%)
                  </label>
                  <input
                    id="ref-pct"
                    type="number"
                    min={1}
                    max={20}
                    className={shared.textInput}
                    value={referralDraft.rewardPercent}
                    onChange={(e) =>
                      setReferralDraft((p) => ({
                        ...p,
                        rewardPercent: Number(e.target.value) || 0,
                      }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="ref-cap" className={shared.fieldLabel}>
                    Trần thưởng / đơn (VND)
                  </label>
                  <input
                    id="ref-cap"
                    type="number"
                    min={10000}
                    step={10000}
                    className={shared.textInput}
                    value={referralDraft.maxRewardPerOrderVnd}
                    onChange={(e) =>
                      setReferralDraft((p) => ({
                        ...p,
                        maxRewardPerOrderVnd: Number(e.target.value) || 0,
                      }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="ref-mo" className={shared.fieldLabel}>
                    Tối đa lượt / tháng
                  </label>
                  <input
                    id="ref-mo"
                    type="number"
                    min={1}
                    max={50}
                    className={shared.textInput}
                    value={referralDraft.maxReferralsPerMonth}
                    onChange={(e) =>
                      setReferralDraft((p) => ({
                        ...p,
                        maxReferralsPerMonth: Number(e.target.value) || 1,
                      }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="ref-min" className={shared.fieldLabel}>
                    Đơn tối thiểu bạn mời (VND)
                  </label>
                  <input
                    id="ref-min"
                    type="number"
                    min={0}
                    step={10000}
                    className={shared.textInput}
                    value={referralDraft.minInviteeOrderVnd}
                    onChange={(e) =>
                      setReferralDraft((p) => ({
                        ...p,
                        minInviteeOrderVnd: Number(e.target.value) || 0,
                      }))
                    }
                  />
                </div>
              </div>

              <div className={styles.switchList}>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <span className={styles.switchTitle}>
                      Chỉ giải ngân Xu thưởng sau khi đơn con hết hạn đổi trả
                    </span>
                    <span className={styles.switchDesc}>
                      Ngăn hành vi đặt đơn nhận thưởng rồi hoàn hàng ngay sau đó.
                    </span>
                  </div>
                  <ToggleSwitch
                    id="sw-ref-hold"
                    checked={referralDraft.unlockAfterReturnWindow}
                    label="Bật khóa thưởng đến khi hết hạn đổi trả"
                    onChange={(next) =>
                      setReferralDraft((p) => ({ ...p, unlockAfterReturnWindow: next }))
                    }
                  />
                </div>
                <div className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <span className={styles.switchTitle}>
                      Chặn tự giới thiệu trùng thiết bị, số điện thoại hoặc địa chỉ nhận
                    </span>
                    <span className={styles.switchDesc}>
                      Tự động hủy thưởng nếu phát hiện cùng mã thiết bị hoặc cùng địa chỉ giao hàng.
                    </span>
                  </div>
                  <ToggleSwitch
                    id="sw-ref-abuse"
                    checked={referralDraft.antiAbuseSameDeviceOrPhone}
                    label="Bật chặn trùng thiết bị và SĐT giới thiệu"
                    onChange={(next) =>
                      setReferralDraft((p) => ({ ...p, antiAbuseSameDeviceOrPhone: next }))
                    }
                  />
                </div>
              </div>

              <div className={styles.formFooter}>
                <span className={shared.savedStamp}>
                  {savedNotice.referral ?? "Áp dụng theo dõi mã mời toàn sàn."}
                </span>
                <Button type="submit" variant="ink" size="sm" shape="app">
                  Lưu hạn mức giới thiệu
                </Button>
              </div>
            </form>
          </section>

          {/* 1C: Ưu đãi sinh viên .edu */}
          <section className={shared.panel} aria-labelledby="tv-edu-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-edu-heading" className={shared.panelTitle}>
                Ưu đãi sinh viên xác thực email .edu
              </h2>
              <ToggleSwitch
                id="sw-edu-enabled"
                checked={studentDraft.enabled}
                label="Bật hoặc tắt ưu đãi sinh viên .edu"
                onChange={(next) => setStudentDraft((p) => ({ ...p, enabled: next }))}
              />
            </div>

            <form onSubmit={handleSaveStudent}>
              <div className={styles.formGridFour}>
                <div className={styles.fieldGroup}>
                  <label htmlFor="edu-domains" className={shared.fieldLabel}>
                    Hậu tố email chấp nhận
                  </label>
                  <input
                    id="edu-domains"
                    type="text"
                    className={shared.textInput}
                    value={studentDraft.allowedDomains}
                    onChange={(e) =>
                      setStudentDraft((p) => ({ ...p, allowedDomains: e.target.value }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="edu-pct" className={shared.fieldLabel}>
                    Mức giảm sinh viên (%)
                  </label>
                  <input
                    id="edu-pct"
                    type="number"
                    min={1}
                    max={25}
                    className={shared.textInput}
                    value={studentDraft.discountPercent}
                    onChange={(e) =>
                      setStudentDraft((p) => ({
                        ...p,
                        discountPercent: Number(e.target.value) || 0,
                      }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="edu-cap" className={shared.fieldLabel}>
                    Giảm tối đa / đơn (VND)
                  </label>
                  <input
                    id="edu-cap"
                    type="number"
                    min={10000}
                    step={5000}
                    className={shared.textInput}
                    value={studentDraft.maxDiscountVnd}
                    onChange={(e) =>
                      setStudentDraft((p) => ({
                        ...p,
                        maxDiscountVnd: Number(e.target.value) || 0,
                      }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="edu-limit" className={shared.fieldLabel}>
                    Giới hạn đơn / tháng
                  </label>
                  <input
                    id="edu-limit"
                    type="number"
                    min={1}
                    max={10}
                    className={shared.textInput}
                    value={studentDraft.monthlyOrderLimit}
                    onChange={(e) =>
                      setStudentDraft((p) => ({
                        ...p,
                        monthlyOrderLimit: Number(e.target.value) || 1,
                      }))
                    }
                  />
                </div>
              </div>

              <div className={styles.formFooter}>
                <span className={shared.savedStamp}>
                  {savedNotice.student ??
                    `Chu kỳ xác thực lại mỗi ${studentDraft.reverifyMonths} tháng.`}
                </span>
                <Button type="submit" variant="ink" size="sm" shape="app">
                  Lưu cấu hình sinh viên
                </Button>
              </div>
            </form>

            <div className={styles.subHeadingSpacer}>
              <div className={shared.panelHeader}>
                <h3 className={shared.panelTitle}>Hàng đợi xác thực email .edu</h3>
                <span className={shared.panelMeta}>{pendingEduCount} hồ sơ chờ</span>
              </div>

              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Sinh viên & email .edu</th>
                      <th>Trường đại học</th>
                      <th>Thời điểm gửi</th>
                      <th>Trạng thái</th>
                      <th className={shared.colAction}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {loyalty.student.verifications.map((item) => {
                      const meta = STUDENT_VERIF_STATUS_META[item.status];
                      return (
                        <tr key={item.id}>
                          <td>
                            <div className={shared.cellPrimary}>
                              {item.studentName} ({item.code})
                            </div>
                            <div className={shared.cellSub}>{item.eduEmail}</div>
                          </td>
                          <td>{item.university}</td>
                          <td>{formatShortTimeDateVi(item.submittedAt)}</td>
                          <td>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </td>
                          <td className={shared.colAction}>
                            <div className={styles.actionInline}>
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                disabled={item.status === "da_xac_thuc"}
                                onClick={() => handleStudentVerifDecision(item, "da_xac_thuc")}
                              >
                                Xác thực
                              </button>
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                disabled={item.status === "tu_choi"}
                                onClick={() => handleStudentVerifDecision(item, "tu_choi")}
                              >
                                Từ chối
                              </button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {loyalty.student.verifications.map((item) => {
                  const meta = STUDENT_VERIF_STATUS_META[item.status];
                  return (
                    <div key={item.id} className={shared.mobileItem}>
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>{item.studentName}</span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>{item.eduEmail}</span>
                        {item.status === "cho_duyet" && (
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={() => handleStudentVerifDecision(item, "da_xac_thuc")}
                          >
                            Xác thực
                          </button>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          </section>
        </div>
      )}

      {/* TAB 3 (MOCK): THẺ QUÀ TẶNG & SỔ CÁI GHI KÉP VND */}
      {activeTab === "the_qua_tang" && (
        <div className={styles.stack}>
          <section className={shared.kpiStrip} aria-label="Tổng hợp sổ cái Thẻ quà tặng">
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Tổng phát hành (mẫu)</span>
              <span className={shared.kpiValue}>{formatVnd(giftCardSummary.totalIssued)}</span>
              <span className={shared.kpiCaption}>
                {loyalty.giftCards.length} lô mã · Ghi Có TK 3387
              </span>
            </div>
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Đã quy đổi</span>
              <span className={shared.kpiValue}>{formatVnd(giftCardSummary.totalRedeemed)}</span>
              <span className={shared.kpiCaption}>Đã cấn trừ đơn đa gian hàng</span>
            </div>
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Hết hạn</span>
              <span className={shared.kpiValue}>{formatVnd(giftCardSummary.totalExpired)}</span>
              <span className={shared.kpiCaption}>Đã tất toán về TK 711</span>
            </div>
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Số dư đang lưu hành</span>
              <span className={`${shared.kpiValue} ${shared.kpiValuePositive}`}>
                {formatVnd(giftCardSummary.totalRemaining)}
              </span>
              <span className={shared.kpiCaption}>
                {giftCardSummary.isBalanced ? "Cân đối sổ cái kép 0₫ lệch" : "Cần kiểm tra sổ cái"}
              </span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="tv-gc-issue-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-gc-issue-heading" className={shared.panelTitle}>
                Phát hành & quản lý mã Thẻ quà tặng toàn sàn (dữ liệu mẫu)
              </h2>
              <span className={shared.panelMeta}>Hạn tối đa dưới 12 tháng · số nguyên VND</span>
            </div>

            <form className={styles.issueForm} onSubmit={handleIssueGiftCard}>
              <div className={styles.fieldGroup}>
                <label htmlFor="gc-code" className={shared.fieldLabel}>
                  Mã thẻ tùy biến
                </label>
                <input
                  id="gc-code"
                  type="text"
                  className={shared.textInput}
                  value={gcCode}
                  onChange={(e) => {
                    setGcCode(e.target.value);
                    if (gcError) setGcError("");
                  }}
                  placeholder="VD: NXGIFT-NOEL-07"
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="gc-vnd" className={shared.fieldLabel}>
                  Mệnh giá phát hành (VND)
                </label>
                <input
                  id="gc-vnd"
                  type="number"
                  min={50000}
                  step={50000}
                  className={shared.textInput}
                  value={gcAmount}
                  onChange={(e) => setGcAmount(Number(e.target.value) || 0)}
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="gc-purpose" className={shared.fieldLabel}>
                  Mục đích / chương trình
                </label>
                <input
                  id="gc-purpose"
                  type="text"
                  className={shared.textInput}
                  value={gcPurpose}
                  onChange={(e) => setGcPurpose(e.target.value)}
                  placeholder="VD: Gói thẻ quà doanh nghiệp Q4"
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="gc-mo" className={shared.fieldLabel}>
                  Hạn dùng (tháng)
                </label>
                <input
                  id="gc-mo"
                  type="number"
                  min={1}
                  max={11}
                  className={shared.textInput}
                  value={gcMonths}
                  onChange={(e) => setGcMonths(Number(e.target.value) || 11)}
                />
              </div>
              <Button type="submit" variant="primary" size="sm" shape="app">
                Phát hành thẻ
              </Button>
            </form>
            {gcError && <p className={shared.validationError}>{gcError}</p>}

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Mã thẻ & mục đích phát hành</th>
                    <th className={shared.colNum}>Phát hành</th>
                    <th className={shared.colNum}>Đã quy đổi</th>
                    <th className={shared.colNum}>Hết hạn</th>
                    <th className={shared.colNum}>Còn lưu hành</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {loyalty.giftCards.map((card) => {
                    const meta = GIFT_CARD_STATUS_META[card.status];
                    return (
                      <tr key={card.id}>
                        <td>
                          <div className={shared.cellPrimary}>{card.code}</div>
                          <div className={shared.cellSub}>
                            {card.purpose} · Hạn {formatDateVi(card.expiresAt)}
                          </div>
                        </td>
                        <td className={shared.colNum}>{formatVnd(card.issuedAmount)}</td>
                        <td className={shared.colNum}>{formatVnd(card.redeemedAmount)}</td>
                        <td className={`${shared.colNum} ${shared.numMuted}`}>
                          {formatVnd(card.expiredAmount)}
                        </td>
                        <td className={shared.colNum}>
                          <span className={shared.cellPrimary}>
                            {formatVnd(card.remainingAmount)}
                          </span>
                        </td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                        <td className={shared.colAction}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            disabled={card.status !== "dang_luu_hanh" || card.remainingAmount <= 0}
                            onClick={() => handleExpireGiftCard(card)}
                          >
                            Tất toán hết hạn
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
                <tfoot>
                  <tr className={styles.tfootRow}>
                    <td>Tổng kiểm đối chiếu thẻ quà tặng</td>
                    <td className={shared.colNum}>{formatVnd(giftCardSummary.totalIssued)}</td>
                    <td className={shared.colNum}>{formatVnd(giftCardSummary.totalRedeemed)}</td>
                    <td className={shared.colNum}>{formatVnd(giftCardSummary.totalExpired)}</td>
                    <td className={shared.colNum}>{formatVnd(giftCardSummary.totalRemaining)}</td>
                    <td colSpan={2} />
                  </tr>
                </tfoot>
              </table>
            </div>

            <div className={shared.mobileList}>
              {loyalty.giftCards.map((card) => {
                const meta = GIFT_CARD_STATUS_META[card.status];
                return (
                  <div key={card.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{card.code}</span>
                      <span className={shared.mobileMetric}>{formatVnd(card.remainingAmount)}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        Phát hành {formatVnd(card.issuedAmount)} · Quy đổi{" "}
                        {formatVnd(card.redeemedAmount)}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                  </div>
                );
              })}
            </div>
          </section>

          {/* Sổ cái ghi kép VND mẫu */}
          <section className={shared.panel} aria-labelledby="tv-gc-ledger-heading">
            <div className={shared.panelHeader}>
              <h2 id="tv-gc-ledger-heading" className={shared.panelTitle}>
                Sổ cái ghi kép VND · TK 3387 Quỹ Thẻ quà tặng (mẫu)
              </h2>
              <StatusPill
                label={giftCardSummary.isBalanced ? "Khớp cân đối kép" : "Lệch số học"}
                tone={giftCardSummary.isBalanced ? "positive" : "negative"}
              />
            </div>

            <div className={styles.ledgerFormula}>
              <span>
                Phương trình cân đối: {formatVnd(giftCardSummary.totalIssued)} (Phát hành) −{" "}
                {formatVnd(giftCardSummary.totalRedeemed)} (Quy đổi) −{" "}
                {formatVnd(giftCardSummary.totalExpired)} (Hết hạn) ={" "}
                {formatVnd(giftCardSummary.totalRemaining)} (Đang lưu hành)
              </span>
              <span>{loyalty.giftCardLedger.length} bút toán kép</span>
            </div>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Mã bút toán & ngày</th>
                    <th>Mã thẻ & diễn giải</th>
                    <th>Định khoản Nợ / Có</th>
                    <th className={shared.colNum}>Phát sinh Có (Phát hành)</th>
                    <th className={shared.colNum}>Phát sinh Nợ (Quy đổi/Hết hạn)</th>
                  </tr>
                </thead>
                <tbody>
                  {loyalty.giftCardLedger.map((entry) => (
                    <tr key={entry.id}>
                      <td>
                        <div className={shared.cellPrimary}>{entry.entryCode}</div>
                        <div className={shared.cellSub}>{formatDateVi(entry.date)}</div>
                      </td>
                      <td>
                        <div className={shared.cellPrimary}>{entry.cardCode}</div>
                        <div className={shared.cellSub}>{entry.note}</div>
                      </td>
                      <td>
                        <div>Nợ: {entry.debitAccount}</div>
                        <div className={shared.cellSub}>Có: {entry.creditAccount}</div>
                      </td>
                      <td
                        className={`${shared.colNum} ${entry.creditVnd > 0 ? shared.numPositive : shared.numMuted}`}
                      >
                        {entry.creditVnd > 0 ? formatVnd(entry.creditVnd) : "0₫"}
                      </td>
                      <td
                        className={`${shared.colNum} ${entry.debitVnd === 0 ? shared.numMuted : ""}`}
                      >
                        {entry.debitVnd > 0 ? formatVnd(entry.debitVnd) : "0₫"}
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr className={styles.tfootRow}>
                    <td colSpan={3}>
                      Tổng cộng sổ cái (Chênh lệch Có − Nợ ={" "}
                      {formatVnd(
                        giftCardSummary.ledgerCreditTotal - giftCardSummary.ledgerDebitTotal
                      )}
                      )
                    </td>
                    <td className={shared.colNum}>
                      {formatVnd(giftCardSummary.ledgerCreditTotal)}
                    </td>
                    <td className={shared.colNum}>{formatVnd(giftCardSummary.ledgerDebitTotal)}</td>
                  </tr>
                </tfoot>
              </table>
            </div>

            <div className={shared.mobileList}>
              {loyalty.giftCardLedger.map((entry) => (
                <div key={entry.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>
                      {entry.entryCode} · {entry.cardCode}
                    </span>
                    <span className={shared.mobileMetric}>
                      {entry.creditVnd > 0
                        ? `+${formatVnd(entry.creditVnd)}`
                        : `-${formatVnd(entry.debitVnd)}`}
                    </span>
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {formatDateVi(entry.date)} · {entry.note}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

function AdminLoyaltyRoute(props: AdminLoyaltyClientProps) {
  return (
    <AdminStateGate
      title="Hội viên, khuyến mãi sàn & thẻ quà tặng"
      subtitle="Quản trị mã giảm giá cấp sàn, khung giờ Flash sale thực trên hệ thống, cùng cấu hình Hội viên Nexus và Thẻ quà tặng."
      emptyTitle="Chưa có chương trình hội viên hoặc khuyến mãi nào"
      emptyMessage="Không có bản ghi hội viên, thẻ quà tặng hay khung giờ Flash sale nào trong hệ thống."
      errorTitle="Không thể tải dữ liệu Hội viên & khuyến mãi"
      errorMessage="Dịch vụ khuyến mãi và hội viên tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery }) => <AdminLoyaltyWorkspace {...props} initialQuery={initialQuery} />}
    </AdminStateGate>
  );
}

export default function AdminLoyaltyClient(props: AdminLoyaltyClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Hội viên, khuyến mãi sàn & thẻ quà tặng" />}>
      <AdminLoyaltyRoute {...props} />
    </Suspense>
  );
}
