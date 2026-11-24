"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  GatewayCategory,
  GatewayCreditCoinsResponse,
  GatewayFlashSale,
  GatewayFlashSaleItem,
  GatewayUser,
  GatewayVoucher,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, StateView } from "@/components";
import { formatDateTimeVi, formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { canEditVoucherField } from "@/features/seller/types";
import { type StableReferenceCache, resolveStableReferenceKey } from "@/features/checkout/helpers";
import type { PillTone } from "./adminMeta";
import { StatusPill } from "./StatusPill";
import { useAdminUser } from "./AdminUserContext";
import { roleHasCapability } from "@/lib/admin-rbac";
import shared from "./adminShared.module.css";

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function getVoucherStatusKey(v: GatewayVoucher): string {
  if (v.status) return v.status.toUpperCase();
  return v.is_active ? "ACTIVE" : "PAUSED";
}

function getVoucherStatusPill(statusKey: string): { label: string; tone: PillTone } {
  if (statusKey === "ACTIVE") return { label: "Đang bật", tone: "positive" };
  if (statusKey === "PAUSED") return { label: "Tạm dừng", tone: "warning" };
  if (statusKey === "DELETED") return { label: "Đã xóa", tone: "negative" };
  return { label: statusKey, tone: "neutral" };
}

function getFlashItemStatusPill(status: string): { label: string; tone: PillTone } {
  const key = status.toUpperCase();
  if (key === "APPROVED") return { label: "Đã duyệt", tone: "positive" };
  if (key === "PENDING") return { label: "Chờ duyệt", tone: "warning" };
  if (key === "REJECTED") return { label: "Từ chối", tone: "negative" };
  return { label: status, tone: "neutral" };
}

export function AdminPromoCoinsPanel({ mode }: { mode: "promo" | "coins" }) {
  const { showToast } = useMockStore();
  const adminUser = useAdminUser();
  const role = adminUser?.role ?? "";

  const canReadVouchers = roleHasCapability(role, "vouchers.read");
  const canWriteVouchers = roleHasCapability(role, "vouchers.write");
  const canReadFlashSales = roleHasCapability(role, "flash_sales.read");
  const canWriteFlashSales = roleHasCapability(role, "flash_sales.write");
  const canApproveFlashItems = roleHasCapability(role, "flash_sales.items.approve");
  const canReadCategories = roleHasCapability(role, "categories.read");
  const canCreditCoins = roleHasCapability(role, "coins.credit");

  const [vouchers, setVouchers] = useState<GatewayVoucher[]>([]);
  const [categories, setCategories] = useState<GatewayCategory[]>([]);
  const [flashSales, setFlashSales] = useState<GatewayFlashSale[]>([]);
  const [flashItems, setFlashItems] = useState<GatewayFlashSaleItem[]>([]);
  const [flashItemStatusFilter, setFlashItemStatusFilter] = useState<
    "PENDING" | "APPROVED" | "REJECTED" | "ALL"
  >("PENDING");
  const [users, setUsers] = useState<GatewayUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Create / edit platform voucher
  const defaultStart = useMemo(() => toDatetimeLocalValue(new Date()), []);
  const defaultEnd = useMemo(
    () => toDatetimeLocalValue(new Date(Date.now() + 14 * 24 * 3600 * 1000)),
    []
  );
  const [editingVoucher, setEditingVoucher] = useState<GatewayVoucher | null>(null);
  const [vCode, setVCode] = useState("");
  const [vName, setVName] = useState("");
  const [vScope, setVScope] = useState<"PLATFORM" | "FREESHIP">("PLATFORM");
  const [vType, setVType] = useState<"FIXED" | "PERCENT">("FIXED");
  const [vValue, setVValue] = useState("30000");
  const [vMaxDiscount, setVMaxDiscount] = useState("60000");
  const [vMinOrder, setVMinOrder] = useState("299000");
  const [vQuota, setVQuota] = useState("500");
  const [vPerUser, setVPerUser] = useState("1");
  const [vBudget, setVBudget] = useState("15000000");
  const [vStartsAt, setVStartsAt] = useState(defaultStart);
  const [vEndsAt, setVEndsAt] = useState(defaultEnd);
  const [vCategoryIds, setVCategoryIds] = useState<string[]>([]);
  const [vBusy, setVBusy] = useState(false);
  const [voucherActionBusyId, setVoucherActionBusyId] = useState<string | null>(null);
  const [confirmDeleteVoucherId, setConfirmDeleteVoucherId] = useState<string | null>(null);

  // Create flash sale window
  const [fsName, setFsName] = useState("");
  const [fsStartsAt, setFsStartsAt] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 3600 * 1000))
  );
  const [fsEndsAt, setFsEndsAt] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 4 * 3600 * 1000))
  );
  const [fsMinDiscountStr, setFsMinDiscountStr] = useState("10");
  const [fsCategoryIds, setFsCategoryIds] = useState<string[]>([]);
  const [fsBusy, setFsBusy] = useState(false);

  // Approve / reject flash sale item inline queue
  const [itemReasons, setItemReasons] = useState<Record<string, string>>({});
  const [itemBusyId, setItemBusyId] = useState<string | null>(null);

  // Credit coins form
  const [coinUserId, setCoinUserId] = useState("");
  const [coinAmount, setCoinAmount] = useState("10000");
  const [coinReason, setCoinReason] = useState("");
  const [coinRefId, setCoinRefId] = useState("");
  const [coinBusy, setCoinBusy] = useState(false);
  const [coinResult, setCoinResult] = useState<GatewayCreditCoinsResponse | null>(null);
  const pendingCreditRef = useRef<StableReferenceCache | null>(null);

  const loadData = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      if (mode === "promo") {
        const [vRes, catsRes, fsRes, fsItemsRes] = await Promise.all([
          canReadVouchers
            ? api<{ vouchers: GatewayVoucher[] }>("admin/vouchers?include_inactive=true").catch(
                () => api<{ vouchers: GatewayVoucher[] }>("vouchers")
              )
            : Promise.resolve({ vouchers: [] as GatewayVoucher[] }),
          canReadCategories
            ? api<{ categories: GatewayCategory[] }>("admin/categories").catch(() => ({
                categories: [] as GatewayCategory[],
              }))
            : Promise.resolve({ categories: [] as GatewayCategory[] }),
          canReadFlashSales
            ? api<{ flash_sales?: GatewayFlashSale[]; items?: GatewayFlashSale[] }>(
                "admin/flash-sales?page=1&page_size=50"
              ).catch(() =>
                api<{ flash_sales: GatewayFlashSale[] }>("flash-sales?include_upcoming=true")
              )
            : Promise.resolve({ flash_sales: [] as GatewayFlashSale[] }),
          canReadFlashSales
            ? api<{ items?: GatewayFlashSaleItem[] }>(
                "admin/flash-sale-items?page=1&page_size=100"
              ).catch(() => ({ items: [] }))
            : Promise.resolve({ items: [] as GatewayFlashSaleItem[] }),
        ]);
        setVouchers(vRes.vouchers ?? []);
        setCategories(catsRes.categories ?? []);
        const nextFs =
          ("flash_sales" in fsRes ? fsRes.flash_sales : undefined) ??
          ("items" in fsRes ? fsRes.items : undefined) ??
          [];
        setFlashSales(nextFs);
        setFlashItems(fsItemsRes.items ?? []);
      } else if (canCreditCoins) {
        const uRes = await api<PaginatedResponse<GatewayUser>>("admin/users?page=1&page_size=50");
        const list = uRes.items ?? [];
        setUsers(list);
        if (list.length > 0) {
          setCoinUserId((prev) => prev || list[0].id);
        }
      }
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải dữ liệu khuyến mãi hoặc thành viên."));
    } finally {
      setLoading(false);
    }
  }, [mode, canReadVouchers, canReadCategories, canReadFlashSales, canCreditCoins]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) {
      map.set(c.id, c.name);
    }
    return map;
  }, [categories]);

  const flashWindowNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const fs of flashSales) {
      map.set(fs.id, fs.name);
    }
    return map;
  }, [flashSales]);

  const filteredFlashItems = useMemo(() => {
    if (flashItemStatusFilter === "ALL") return flashItems;
    return flashItems.filter((it) => it.status.toUpperCase() === flashItemStatusFilter);
  }, [flashItems, flashItemStatusFilter]);

  const flashItemCounts = useMemo(() => {
    const counts = { ALL: flashItems.length, PENDING: 0, APPROVED: 0, REJECTED: 0 };
    for (const it of flashItems) {
      const st = it.status.toUpperCase();
      if (st === "PENDING") counts.PENDING += 1;
      else if (st === "APPROVED") counts.APPROVED += 1;
      else if (st === "REJECTED") counts.REJECTED += 1;
    }
    return counts;
  }, [flashItems]);

  const toggleCategoryId = (setter: React.Dispatch<React.SetStateAction<string[]>>, id: string) => {
    setter((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  };

  const resetVoucherForm = () => {
    setEditingVoucher(null);
    setVCode("");
    setVName("");
    setVScope("PLATFORM");
    setVType("FIXED");
    setVValue("30000");
    setVMaxDiscount("60000");
    setVMinOrder("299000");
    setVQuota("500");
    setVPerUser("1");
    setVBudget("15000000");
    setVStartsAt(defaultStart);
    setVEndsAt(defaultEnd);
    setVCategoryIds([]);
  };

  const handleStartEditVoucher = (v: GatewayVoucher) => {
    if (v.scope.toUpperCase() === "SHOP") return;
    setEditingVoucher(v);
    setVCode(v.code);
    setVName(v.name);
    setVScope(v.scope === "FREESHIP" ? "FREESHIP" : "PLATFORM");
    setVType(v.discount_type === "PERCENT" ? "PERCENT" : "FIXED");
    setVValue(String(v.discount_type === "PERCENT" ? v.discount_percent : v.value_vnd));
    setVMaxDiscount(String(v.max_discount_vnd ?? 0));
    setVMinOrder(String(v.min_order_vnd ?? 0));
    setVQuota(String(v.quota));
    setVPerUser(String(v.per_user_limit || 1));
    setVBudget(String(v.budget_vnd ?? 0));
    setVStartsAt(v.starts_at ? toDatetimeLocalValue(new Date(v.starts_at)) : defaultStart);
    setVEndsAt(v.ends_at ? toDatetimeLocalValue(new Date(v.ends_at)) : defaultEnd);
    setVCategoryIds(v.category_ids ?? []);
  };

  const handleSaveVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const rawVal = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.max(0, Math.trunc(Number(vMinOrder) || 0));
    const quota = Math.max(1, Math.trunc(Number(vQuota) || 1));
    const perUser = Math.max(1, Math.trunc(Number(vPerUser) || 1));
    const budget = Math.max(0, Math.trunc(Number(vBudget) || 0));
    const maxDiscount =
      vType === "PERCENT" ? Math.max(0, Math.trunc(Number(vMaxDiscount) || 0)) : 0;

    if (!editingVoucher && code.length < 3) {
      showToast("Mã voucher cần tối thiểu 3 ký tự.");
      return;
    }
    const startDt = new Date(vStartsAt);
    const endDt = new Date(vEndsAt);
    if (Number.isNaN(startDt.getTime()) || Number.isNaN(endDt.getTime()) || endDt <= startDt) {
      showToast("Thời điểm kết thúc phải sau thời điểm bắt đầu.");
      return;
    }

    setVBusy(true);
    try {
      const displayName =
        vName.trim() ||
        `Mã ${vScope === "FREESHIP" ? "miễn ship" : "toàn sàn"} giảm ${vType === "FIXED" ? formatVnd(rawVal) : `${rawVal}%`}`;

      if (editingVoucher) {
        const payload: Record<string, unknown> = {
          name: displayName,
          quota,
          budget_vnd: budget,
          ends_at: endDt.toISOString(),
        };
        if (editingVoucher.used_count === 0) {
          payload.discount_type = vType;
          payload.value_vnd = vType === "FIXED" ? rawVal : 0;
          payload.discount_percent = vType === "PERCENT" ? rawVal : 0;
          payload.max_discount_vnd = maxDiscount;
          payload.min_order_vnd = minOrder;
          payload.per_user_limit = perUser;
          payload.starts_at = startDt.toISOString();
          payload.category_ids = vCategoryIds;
          payload.replace_category_ids = true;
        }
        await api<{ voucher: GatewayVoucher }>(
          `admin/vouchers/${encodeURIComponent(editingVoucher.id)}`,
          {
            method: "PUT",
            json: payload,
          }
        );
        resetVoucherForm();
        await loadData();
        showToast(`Đã cập nhật voucher sàn ${editingVoucher.code}.`);
      } else {
        await api<{ voucher: GatewayVoucher }>("admin/vouchers", {
          method: "POST",
          json: {
            code,
            name: displayName,
            scope: vScope,
            discount_type: vType,
            value_vnd: vType === "FIXED" ? rawVal : 0,
            discount_percent: vType === "PERCENT" ? rawVal : 0,
            max_discount_vnd: maxDiscount,
            min_order_vnd: minOrder,
            quota,
            per_user_limit: perUser,
            budget_vnd: budget,
            starts_at: startDt.toISOString(),
            ends_at: endDt.toISOString(),
            category_ids: vCategoryIds,
          },
        });
        resetVoucherForm();
        await loadData();
        showToast(`Đã tạo mã giảm giá cấp sàn ${code}.`);
      }
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể lưu voucher cấp sàn."));
    } finally {
      setVBusy(false);
    }
  };

  const handleToggleVoucherStatus = async (v: GatewayVoucher) => {
    if (v.scope.toUpperCase() === "SHOP") return;
    const key = getVoucherStatusKey(v);
    if (key === "DELETED") return;
    const nextStatus = key === "ACTIVE" ? "PAUSED" : "ACTIVE";
    setVoucherActionBusyId(v.id);
    try {
      await api<{ voucher: GatewayVoucher }>(`admin/vouchers/${encodeURIComponent(v.id)}/status`, {
        method: "PATCH",
        json: { status: nextStatus },
      });
      await loadData();
      showToast(
        nextStatus === "PAUSED"
          ? `Đã tạm dừng voucher ${v.code}.`
          : `Đã kích hoạt lại voucher ${v.code}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể đổi trạng thái voucher."));
    } finally {
      setVoucherActionBusyId(null);
    }
  };

  const handleDeleteVoucher = async (v: GatewayVoucher) => {
    if (v.scope.toUpperCase() === "SHOP") return;
    setVoucherActionBusyId(v.id);
    try {
      await api(`admin/vouchers/${encodeURIComponent(v.id)}`, { method: "DELETE" });
      setConfirmDeleteVoucherId(null);
      await loadData();
      showToast(`Đã xóa mềm voucher ${v.code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xóa voucher lúc này."));
    } finally {
      setVoucherActionBusyId(null);
    }
  };

  const handleCreateFlashSale = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fsName.trim();
    const startDt = new Date(fsStartsAt);
    const endDt = new Date(fsEndsAt);
    const minDiscountPercent = Math.max(0, Math.min(90, Math.trunc(Number(fsMinDiscountStr) || 0)));
    if (cleanName.length < 3) {
      showToast("Tên khung giờ Flash sale cần tối thiểu 3 ký tự.");
      return;
    }
    if (Number.isNaN(startDt.getTime()) || Number.isNaN(endDt.getTime()) || endDt <= startDt) {
      showToast("Thời điểm kết thúc khung Flash sale phải sau thời điểm bắt đầu.");
      return;
    }
    setFsBusy(true);
    try {
      await api<{ flash_sale: GatewayFlashSale }>("admin/flash-sales", {
        method: "POST",
        json: {
          name: cleanName,
          starts_at: startDt.toISOString(),
          ends_at: endDt.toISOString(),
          min_discount_percent: minDiscountPercent,
          category_ids: fsCategoryIds,
        },
      });
      setFsName("");
      setFsCategoryIds([]);
      await loadData();
      showToast(`Đã mở khung giờ Flash sale "${cleanName}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo khung giờ Flash sale."));
    } finally {
      setFsBusy(false);
    }
  };

  const handleReviewFlashItem = async (item: GatewayFlashSaleItem, approve: boolean) => {
    const reason = (itemReasons[item.id] ?? "").trim();
    if (!approve && reason.length < 3) {
      showToast("Vui lòng nhập lý do từ chối suất Flash sale (tối thiểu 3 ký tự).");
      return;
    }
    setItemBusyId(item.id);
    try {
      await api<{ item: GatewayFlashSaleItem }>(
        `admin/flash-sale-items/${encodeURIComponent(item.id)}/approve`,
        {
          method: "POST",
          json: {
            approve,
            reason,
          },
        }
      );
      setItemReasons((prev) => ({ ...prev, [item.id]: "" }));
      await loadData();
      showToast(
        approve
          ? `Đã phê duyệt suất Flash sale ${item.id.slice(0, 8)}.`
          : `Đã từ chối suất Flash sale ${item.id.slice(0, 8)}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xử lý suất Flash sale này."));
    } finally {
      setItemBusyId(null);
    }
  };

  const handleCreditCoins = async (e: React.FormEvent) => {
    e.preventDefault();
    const uid = coinUserId.trim();
    const amount = Math.trunc(Number(coinAmount) || 0);
    const reason = coinReason.trim();
    if (!uid) {
      showToast("Vui lòng chọn người dùng nhận Xu.");
      return;
    }
    if (amount <= 0) {
      showToast("Số Xu cộng thêm phải là số nguyên dương.");
      return;
    }
    if (reason.length < 3) {
      showToast("Vui lòng nhập lý do cộng Xu.");
      return;
    }
    setCoinBusy(true);
    try {
      const resolvedRef = resolveStableReferenceKey({
        prefix: "admin-credit",
        manualRef: coinRefId,
        fingerprint: `${uid}|${amount}|${reason}`,
        cache: pendingCreditRef.current,
      });
      pendingCreditRef.current = resolvedRef.cache;
      const res = await api<GatewayCreditCoinsResponse>("admin/coins/credit", {
        method: "POST",
        json: {
          user_id: uid,
          amount,
          reason,
          reference_id: resolvedRef.key,
        },
      });
      pendingCreditRef.current = null;
      setCoinResult(res);
      setCoinReason("");
      setCoinRefId("");
      showToast(
        res.already_credited
          ? "Mã tham chiếu này đã được cộng Xu trước đó."
          : `Đã cộng ${formatInt(amount)} Xu thành công. Số dư mới: ${formatInt(res.balance)} Xu.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cộng Xu lúc này."));
    } finally {
      setCoinBusy(false);
    }
  };

  if (loading) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải dữ liệu khuyến mãi & Xu..."
          rows={4}
        />
      </div>
    );
  }

  if (errorMsg) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải dữ liệu"
          message={errorMsg}
          actionLabel="Thử lại"
          onAction={() => void loadData()}
        />
      </div>
    );
  }

  if (mode === "coins") {
    return (
      <div className={shared.sectionStack}>
        <section className={shared.panel} aria-labelledby="credit-coins-heading">
          <div className={shared.panelHeader}>
            <h2 id="credit-coins-heading" className={shared.panelTitle}>
              Cộng Xu thưởng cho người dùng
            </h2>
            <span className={shared.panelMeta}>
              1 Xu = 1₫ · Ghi có trực tiếp vào ví Xu thành viên
            </span>
          </div>

          <form onSubmit={(e) => void handleCreditCoins(e)} className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <label htmlFor="coin-user" className={shared.fieldLabel}>
                  Tài khoản người nhận
                </label>
                <select
                  id="coin-user"
                  className={shared.toolbarSelect}
                  style={{ height: 38 }}
                  value={coinUserId}
                  onChange={(e) => setCoinUserId(e.target.value)}
                >
                  {users.map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.full_name} ({u.email})
                    </option>
                  ))}
                </select>
              </div>
              <div className={shared.detailField}>
                <label htmlFor="coin-amount" className={shared.fieldLabel}>
                  Số Xu cộng thêm (số nguyên)
                </label>
                <input
                  id="coin-amount"
                  type="number"
                  min={1}
                  step={1}
                  className={shared.textInput}
                  value={coinAmount}
                  onChange={(e) => setCoinAmount(e.target.value)}
                  required
                />
              </div>
              <div className={shared.detailField}>
                <label htmlFor="coin-ref" className={shared.fieldLabel}>
                  Mã tham chiếu (chống cộng trùng)
                </label>
                <input
                  id="coin-ref"
                  type="text"
                  className={shared.textInput}
                  value={coinRefId}
                  onChange={(e) => setCoinRefId(e.target.value)}
                  placeholder="Để trống để tự sinh mã mới"
                />
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <label htmlFor="coin-reason" className={shared.fieldLabel}>
                  Lý do cộng Xu
                </label>
                <input
                  id="coin-reason"
                  type="text"
                  className={shared.textInput}
                  value={coinReason}
                  onChange={(e) => setCoinReason(e.target.value)}
                  placeholder="Ví dụ: Thưởng tri ân hội viên hoặc bồi hoàn chậm giao hàng..."
                  required
                />
              </div>
            </div>

            <div className={shared.detailActionBar}>
              <span className={shared.savedStamp}>
                {coinResult
                  ? `Số dư mới của ${coinResult.user_id}: ${formatInt(coinResult.balance)} Xu`
                  : "Thao tác cộng Xu có hiệu lực ngay lập tức."}
              </span>
              <Button type="submit" variant="primary" size="sm" shape="app" disabled={coinBusy}>
                {coinBusy ? "Đang cộng Xu..." : "Xác nhận cộng Xu"}
              </Button>
            </div>
          </form>
        </section>
      </div>
    );
  }

  return (
    <div className={shared.sectionStack}>
      {/* PLATFORM / FREESHIP VOUCHERS */}
      {canReadVouchers && (
        <section className={shared.panel} aria-labelledby="admin-voucher-heading">
          <div className={shared.panelHeader}>
            <h2 id="admin-voucher-heading" className={shared.panelTitle}>
              Tạo & quản lý Voucher cấp sàn ({formatInt(vouchers.length)})
            </h2>
            <Button variant="outline" size="sm" shape="app" onClick={() => void loadData()}>
              Làm mới
            </Button>
          </div>

          {canWriteVouchers && (
            <form onSubmit={(e) => void handleSaveVoucher(e)} className={shared.detailWell}>
              <div className={shared.detailHeader}>
                <h3 className={shared.detailHeading}>
                  {editingVoucher
                    ? `Chỉnh sửa voucher ${editingVoucher.code}`
                    : "Phát hành voucher sàn / miễn phí vận chuyển"}
                </h3>
                {editingVoucher && (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={resetVoucherForm}
                  >
                    Hủy chỉnh sửa
                  </Button>
                )}
              </div>

              {editingVoucher && editingVoucher.used_count > 0 && (
                <p className={shared.savedStamp}>
                  Voucher đã có {formatInt(editingVoucher.used_count)} lượt sử dụng — chỉ được chỉnh
                  tên, tăng lượt/ngân sách và gia hạn thời điểm kết thúc.
                </p>
              )}

              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <label htmlFor="av-code" className={shared.fieldLabel}>
                    Mã voucher sàn
                  </label>
                  <input
                    id="av-code"
                    type="text"
                    className={shared.textInput}
                    value={vCode}
                    onChange={(e) => setVCode(e.target.value)}
                    placeholder="VD: NEXUS30K"
                    disabled={Boolean(editingVoucher)}
                    required
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-name" className={shared.fieldLabel}>
                    Tên chương trình
                  </label>
                  <input
                    id="av-name"
                    type="text"
                    className={shared.textInput}
                    value={vName}
                    onChange={(e) => setVName(e.target.value)}
                    placeholder="Giảm 30.000₫ toàn sàn"
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-scope" className={shared.fieldLabel}>
                    Phạm vi áp dụng
                  </label>
                  <select
                    id="av-scope"
                    className={shared.toolbarSelect}
                    style={{ height: 38 }}
                    value={vScope}
                    onChange={(e) => setVScope(e.target.value as "PLATFORM" | "FREESHIP")}
                    disabled={Boolean(editingVoucher)}
                  >
                    <option value="PLATFORM">Toàn sàn (PLATFORM)</option>
                    <option value="FREESHIP">Miễn phí vận chuyển (FREESHIP)</option>
                  </select>
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-type" className={shared.fieldLabel}>
                    Loại giảm giá
                  </label>
                  <select
                    id="av-type"
                    className={shared.toolbarSelect}
                    style={{ height: 38 }}
                    value={vType}
                    onChange={(e) => setVType(e.target.value as "FIXED" | "PERCENT")}
                    disabled={
                      Boolean(editingVoucher) &&
                      !canEditVoucherField(editingVoucher!, "discount_type")
                    }
                  >
                    <option value="FIXED">Số tiền cố định (VND)</option>
                    <option value="PERCENT">Theo phần trăm (%)</option>
                  </select>
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-val" className={shared.fieldLabel}>
                    {vType === "FIXED" ? "Mức giảm (VND)" : "Phần trăm giảm (%)"}
                  </label>
                  <input
                    id="av-val"
                    type="number"
                    className={shared.textInput}
                    value={vValue}
                    onChange={(e) => setVValue(e.target.value)}
                    disabled={
                      Boolean(editingVoucher) &&
                      !canEditVoucherField(
                        editingVoucher!,
                        vType === "FIXED" ? "value_vnd" : "discount_percent"
                      )
                    }
                  />
                </div>
                {vType === "PERCENT" && (
                  <div className={shared.detailField}>
                    <label htmlFor="av-max" className={shared.fieldLabel}>
                      Giảm tối đa (VND)
                    </label>
                    <input
                      id="av-max"
                      type="number"
                      className={shared.textInput}
                      value={vMaxDiscount}
                      onChange={(e) => setVMaxDiscount(e.target.value)}
                      disabled={
                        Boolean(editingVoucher) &&
                        !canEditVoucherField(editingVoucher!, "max_discount_vnd")
                      }
                    />
                  </div>
                )}
                <div className={shared.detailField}>
                  <label htmlFor="av-min" className={shared.fieldLabel}>
                    Đơn tối thiểu (VND)
                  </label>
                  <input
                    id="av-min"
                    type="number"
                    className={shared.textInput}
                    value={vMinOrder}
                    onChange={(e) => setVMinOrder(e.target.value)}
                    disabled={
                      Boolean(editingVoucher) &&
                      !canEditVoucherField(editingVoucher!, "min_order_vnd")
                    }
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-quota" className={shared.fieldLabel}>
                    Tổng lượt phát hành
                  </label>
                  <input
                    id="av-quota"
                    type="number"
                    min={editingVoucher ? Math.max(1, editingVoucher.used_count) : 1}
                    className={shared.textInput}
                    value={vQuota}
                    onChange={(e) => setVQuota(e.target.value)}
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-peruser" className={shared.fieldLabel}>
                    Giới hạn / khách
                  </label>
                  <input
                    id="av-peruser"
                    type="number"
                    min={1}
                    className={shared.textInput}
                    value={vPerUser}
                    onChange={(e) => setVPerUser(e.target.value)}
                    disabled={
                      Boolean(editingVoucher) &&
                      !canEditVoucherField(editingVoucher!, "per_user_limit")
                    }
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-budget" className={shared.fieldLabel}>
                    Ngân sách tối đa (VND)
                  </label>
                  <input
                    id="av-budget"
                    type="number"
                    className={shared.textInput}
                    value={vBudget}
                    onChange={(e) => setVBudget(e.target.value)}
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-start" className={shared.fieldLabel}>
                    Bắt đầu
                  </label>
                  <input
                    id="av-start"
                    type="datetime-local"
                    className={shared.textInput}
                    value={vStartsAt}
                    onChange={(e) => setVStartsAt(e.target.value)}
                    disabled={
                      Boolean(editingVoucher) && !canEditVoucherField(editingVoucher!, "starts_at")
                    }
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="av-end" className={shared.fieldLabel}>
                    Kết thúc
                  </label>
                  <input
                    id="av-end"
                    type="datetime-local"
                    className={shared.textInput}
                    value={vEndsAt}
                    onChange={(e) => setVEndsAt(e.target.value)}
                  />
                </div>
                {categories.length > 0 && (
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.fieldLabel}>
                      Phạm vi danh mục áp dụng (để trống = áp dụng toàn sàn)
                    </span>
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 4 }}>
                      {categories.map((c) => {
                        const checked = vCategoryIds.includes(c.id);
                        const disabled =
                          Boolean(editingVoucher) &&
                          !canEditVoucherField(editingVoucher!, "category_ids");
                        return (
                          <label
                            key={c.id}
                            style={{
                              display: "inline-flex",
                              alignItems: "center",
                              gap: 6,
                              fontSize: 13,
                              padding: "4px 10px",
                              borderRadius: 6,
                              border: "1px solid var(--control)",
                              background: checked ? "var(--active)" : "var(--card)",
                              cursor: disabled ? "not-allowed" : "pointer",
                            }}
                          >
                            <input
                              type="checkbox"
                              checked={checked}
                              disabled={disabled}
                              onChange={() => toggleCategoryId(setVCategoryIds, c.id)}
                            />
                            <span>{c.name}</span>
                          </label>
                        );
                      })}
                    </div>
                  </div>
                )}
              </div>
              <div className={shared.detailActionBar}>
                <span className={shared.savedStamp}>
                  Voucher sàn có thể gộp cùng voucher gian hàng và hỗ trợ giới hạn theo danh mục.
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app" disabled={vBusy}>
                  {vBusy
                    ? "Đang lưu..."
                    : editingVoucher
                      ? "Lưu thay đổi voucher"
                      : "Phát hành voucher sàn"}
                </Button>
              </div>
            </form>
          )}

          <div className={shared.tableWrap} style={{ marginTop: 14 }}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Mã & tên</th>
                  <th>Phạm vi & danh mục</th>
                  <th className={shared.colNum}>Mức giảm</th>
                  <th className={shared.colNum}>Đã dùng / Tổng suất</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {vouchers.map((v) => {
                  const stKey = getVoucherStatusKey(v);
                  const stPill = getVoucherStatusPill(stKey);
                  const isDeleted = stKey === "DELETED";
                  const isBusy = voucherActionBusyId === v.id;
                  const catNames = (v.category_ids ?? [])
                    .map((id) => categoryNameById.get(id) ?? id.slice(0, 8))
                    .join(", ");
                  return (
                    <tr key={v.id}>
                      <td>
                        <div className={shared.cellPrimary}>{v.code}</div>
                        <div className={shared.cellSub}>{v.name}</div>
                      </td>
                      <td>
                        <StatusPill label={v.scope} tone="neutral" />
                        {catNames && <div className={shared.cellSub}>Danh mục: {catNames}</div>}
                      </td>
                      <td className={shared.colNum}>
                        {v.discount_type === "FIXED"
                          ? formatVnd(v.value_vnd)
                          : `${v.discount_percent}%`}
                      </td>
                      <td className={shared.colNum}>
                        {formatInt(v.used_count)} / {formatInt(v.quota)}
                      </td>
                      <td>
                        <StatusPill label={stPill.label} tone={stPill.tone} />
                      </td>
                      <td className={shared.colAction}>
                        {isDeleted ? (
                          <span className={shared.numMuted}>—</span>
                        ) : v.scope.toUpperCase() === "SHOP" ? (
                          <span className={shared.numMuted}>Voucher gian hàng (chỉ xem)</span>
                        ) : (
                          <div
                            className={shared.detailButtons}
                            style={{ justifyContent: "flex-end" }}
                          >
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={isBusy}
                              onClick={() => handleStartEditVoucher(v)}
                            >
                              Sửa
                            </button>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={isBusy}
                              onClick={() => void handleToggleVoucherStatus(v)}
                            >
                              {stKey === "ACTIVE" ? "Tạm dừng" : "Bật lại"}
                            </button>
                            {confirmDeleteVoucherId === v.id ? (
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                disabled={isBusy}
                                onClick={() => void handleDeleteVoucher(v)}
                              >
                                Xác nhận xóa
                              </button>
                            ) : (
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                disabled={isBusy}
                                onClick={() => setConfirmDeleteVoucherId(v.id)}
                              >
                                Xóa
                              </button>
                            )}
                          </div>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {vouchers.map((v) => {
              const stKey = getVoucherStatusKey(v);
              const stPill = getVoucherStatusPill(stKey);
              const isDeleted = stKey === "DELETED";
              const isShopScope = v.scope.toUpperCase() === "SHOP";
              return (
                <div key={v.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>
                      {v.code} · {v.scope}
                    </span>
                    <StatusPill label={stPill.label} tone={stPill.tone} />
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {formatInt(v.used_count)}/{formatInt(v.quota)} lượt ·{" "}
                      {v.discount_type === "FIXED"
                        ? formatVnd(v.value_vnd)
                        : `${v.discount_percent}%`}
                    </span>
                    {!isDeleted && isShopScope ? (
                      <span className={shared.numMuted}>Voucher gian hàng (chỉ xem)</span>
                    ) : !isDeleted ? (
                      <div className={shared.detailButtons}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={() => handleStartEditVoucher(v)}
                        >
                          Sửa
                        </button>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={() => void handleToggleVoucherStatus(v)}
                        >
                          {stKey === "ACTIVE" ? "Dừng" : "Bật"}
                        </button>
                      </div>
                    ) : null}
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      )}

      {/* FLASH SALES WINDOWS + INTERACTIVE PENDING ITEMS QUEUE */}
      {canReadFlashSales && (
        <section className={shared.panel} aria-labelledby="admin-fs-heading">
          <div className={shared.panelHeader}>
            <h2 id="admin-fs-heading" className={shared.panelTitle}>
              Khung giờ Flash sale ({formatInt(flashSales.length)}) & Hàng đợi duyệt suất SKU (
              {formatInt(flashItemCounts.PENDING)} chờ duyệt)
            </h2>
          </div>

          {canWriteFlashSales && (
            <form onSubmit={(e) => void handleCreateFlashSale(e)} className={shared.detailWell}>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <label htmlFor="fs-name" className={shared.fieldLabel}>
                    Tên khung giờ Flash sale mới
                  </label>
                  <input
                    id="fs-name"
                    type="text"
                    className={shared.textInput}
                    value={fsName}
                    onChange={(e) => setFsName(e.target.value)}
                    placeholder="VD: Flash Sale Tối 20:00"
                    required
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="fs-start" className={shared.fieldLabel}>
                    Thời điểm bắt đầu
                  </label>
                  <input
                    id="fs-start"
                    type="datetime-local"
                    className={shared.textInput}
                    value={fsStartsAt}
                    onChange={(e) => setFsStartsAt(e.target.value)}
                    required
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="fs-end" className={shared.fieldLabel}>
                    Thời điểm kết thúc
                  </label>
                  <input
                    id="fs-end"
                    type="datetime-local"
                    className={shared.textInput}
                    value={fsEndsAt}
                    onChange={(e) => setFsEndsAt(e.target.value)}
                    required
                  />
                </div>
                <div className={shared.detailField}>
                  <label htmlFor="fs-min-disc" className={shared.fieldLabel}>
                    Mức giảm tối thiểu (%)
                  </label>
                  <input
                    id="fs-min-disc"
                    type="number"
                    min={0}
                    max={90}
                    className={shared.textInput}
                    value={fsMinDiscountStr}
                    onChange={(e) => setFsMinDiscountStr(e.target.value)}
                  />
                </div>
                {categories.length > 0 && (
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.fieldLabel}>
                      Danh mục được phép đăng ký (để trống = tất cả danh mục)
                    </span>
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 4 }}>
                      {categories.map((c) => {
                        const checked = fsCategoryIds.includes(c.id);
                        return (
                          <label
                            key={c.id}
                            style={{
                              display: "inline-flex",
                              alignItems: "center",
                              gap: 6,
                              fontSize: 13,
                              padding: "4px 10px",
                              borderRadius: 6,
                              border: "1px solid var(--control)",
                              background: checked ? "var(--active)" : "var(--card)",
                              cursor: "pointer",
                            }}
                          >
                            <input
                              type="checkbox"
                              checked={checked}
                              onChange={() => toggleCategoryId(setFsCategoryIds, c.id)}
                            />
                            <span>{c.name}</span>
                          </label>
                        );
                      })}
                    </div>
                  </div>
                )}
              </div>
              <div className={shared.detailActionBar}>
                <span className={shared.savedStamp}>
                  Gian hàng gửi đăng ký SKU sẽ vào hàng đợi duyệt bên dưới.
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app" disabled={fsBusy}>
                  {fsBusy ? "Đang tạo..." : "Mở khung Flash sale"}
                </Button>
              </div>
            </form>
          )}

          {/* Interactive Flash Sale Items Queue */}
          <div style={{ marginTop: 18 }}>
            <div className={shared.panelHeader}>
              <div>
                <h3 className={shared.panelTitle}>Hàng đợi duyệt suất Flash sale</h3>
                <span className={shared.panelMeta}>
                  Kiểm tra giá Flash sale so với giá gốc và duyệt / từ chối trực tiếp
                </span>
              </div>
              <select
                className={shared.toolbarSelect}
                value={flashItemStatusFilter}
                onChange={(e) =>
                  setFlashItemStatusFilter(
                    e.target.value as "PENDING" | "APPROVED" | "REJECTED" | "ALL"
                  )
                }
                aria-label="Lọc trạng thái suất Flash sale"
              >
                <option value="PENDING">Chờ duyệt ({formatInt(flashItemCounts.PENDING)})</option>
                <option value="APPROVED">Đã duyệt ({formatInt(flashItemCounts.APPROVED)})</option>
                <option value="REJECTED">Từ chối ({formatInt(flashItemCounts.REJECTED)})</option>
                <option value="ALL">Tất cả ({formatInt(flashItemCounts.ALL)})</option>
              </select>
            </div>

            {filteredFlashItems.length === 0 ? (
              <p className={shared.emptyRow}>
                Không có suất Flash sale nào trong trạng thái đã chọn.
              </p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Suất / SKU & Shop</th>
                        <th>Khung giờ</th>
                        <th className={shared.colNum}>Giá Flash / Gốc</th>
                        <th className={shared.colNum}>Suất</th>
                        <th>Trạng thái</th>
                        <th>Lý do / Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredFlashItems.map((it) => {
                        const pill = getFlashItemStatusPill(it.status);
                        const isPending = it.status.toUpperCase() === "PENDING";
                        const busy = itemBusyId === it.id;
                        const winName =
                          flashWindowNameById.get(it.flash_sale_id) ?? it.flash_sale_id.slice(0, 8);
                        return (
                          <tr key={it.id}>
                            <td>
                              <div className={shared.cellPrimary}>SKU: {it.sku_id}</div>
                              <div className={shared.cellSub}>
                                Shop: {it.shop_id.slice(0, 8)} · ID: {it.id.slice(0, 8)}
                              </div>
                            </td>
                            <td>{winName}</td>
                            <td className={shared.colNum}>
                              <strong>{formatVnd(it.flash_price_vnd)}</strong>
                              {it.original_price_vnd &&
                                it.original_price_vnd > it.flash_price_vnd && (
                                  <div className={shared.cellSub}>
                                    Gốc {formatVnd(it.original_price_vnd)} (
                                    {formatDiscountPercent(
                                      it.original_price_vnd,
                                      it.flash_price_vnd
                                    )}
                                    )
                                  </div>
                                )}
                            </td>
                            <td className={shared.colNum}>
                              {formatInt(it.remaining_slots)} / {formatInt(it.total_slots)}
                              <div className={shared.cellSub}>
                                Tối đa {formatInt(it.per_user_limit)}/khách
                              </div>
                            </td>
                            <td>
                              <StatusPill label={pill.label} tone={pill.tone} />
                              {it.rejection_reason && (
                                <div className={shared.cellSub}>{it.rejection_reason}</div>
                              )}
                            </td>
                            <td>
                              {isPending && canApproveFlashItems ? (
                                <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                                  <input
                                    type="text"
                                    className={shared.textInput}
                                    style={{ height: 32, minWidth: 180 }}
                                    value={itemReasons[it.id] ?? ""}
                                    onChange={(e) =>
                                      setItemReasons((prev) => ({
                                        ...prev,
                                        [it.id]: e.target.value,
                                      }))
                                    }
                                    placeholder="Ghi chú duyệt / lý do từ chối..."
                                    aria-label={`Lý do duyệt suất ${it.id}`}
                                  />
                                  <div className={shared.detailButtons}>
                                    <Button
                                      type="button"
                                      variant="primary"
                                      size="sm"
                                      shape="app"
                                      disabled={busy}
                                      onClick={() => void handleReviewFlashItem(it, true)}
                                    >
                                      {busy ? "Đang xử lý..." : "Duyệt"}
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="danger"
                                      size="sm"
                                      shape="app"
                                      disabled={busy}
                                      onClick={() => void handleReviewFlashItem(it, false)}
                                    >
                                      Từ chối
                                    </Button>
                                  </div>
                                </div>
                              ) : (
                                <span className={shared.numMuted}>—</span>
                              )}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={shared.mobileList}>
                  {filteredFlashItems.map((it) => {
                    const pill = getFlashItemStatusPill(it.status);
                    const isPending = it.status.toUpperCase() === "PENDING";
                    const busy = itemBusyId === it.id;
                    return (
                      <div key={it.id} className={shared.mobileItem}>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>
                            SKU {it.sku_id.slice(0, 12)}
                          </span>
                          <span className={shared.mobileMetric}>
                            {formatVnd(it.flash_price_vnd)}
                          </span>
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {flashWindowNameById.get(it.flash_sale_id) ??
                              it.flash_sale_id.slice(0, 8)}{" "}
                            · {formatInt(it.remaining_slots)}/{formatInt(it.total_slots)} suất
                          </span>
                          <StatusPill label={pill.label} tone={pill.tone} />
                        </div>
                        {isPending && (
                          <div
                            style={{
                              marginTop: 8,
                              display: "flex",
                              flexDirection: "column",
                              gap: 6,
                            }}
                          >
                            <input
                              type="text"
                              className={shared.textInput}
                              value={itemReasons[it.id] ?? ""}
                              onChange={(e) =>
                                setItemReasons((prev) => ({ ...prev, [it.id]: e.target.value }))
                              }
                              placeholder="Nhập lý do khi từ chối..."
                            />
                            <div className={shared.detailButtons}>
                              <Button
                                type="button"
                                variant="primary"
                                size="sm"
                                shape="app"
                                disabled={busy}
                                onClick={() => void handleReviewFlashItem(it, true)}
                              >
                                Duyệt
                              </Button>
                              <Button
                                type="button"
                                variant="danger"
                                size="sm"
                                shape="app"
                                disabled={busy}
                                onClick={() => void handleReviewFlashItem(it, false)}
                              >
                                Từ chối
                              </Button>
                            </div>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </div>

          {/* Flash sale windows list */}
          <div className={shared.tableWrap} style={{ marginTop: 18 }}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Khung giờ Flash sale</th>
                  <th>Thời gian mở bán</th>
                  <th className={shared.colNum}>Giảm tối thiểu</th>
                  <th className={shared.colNum}>SKU đã duyệt</th>
                </tr>
              </thead>
              <tbody>
                {flashSales.map((fs) => (
                  <tr key={fs.id}>
                    <td>
                      <div className={shared.cellPrimary}>{fs.name}</div>
                      <div className={shared.cellSub}>{fs.id}</div>
                    </td>
                    <td>
                      {fs.starts_at ? formatDateTimeVi(fs.starts_at) : "—"} →{" "}
                      {fs.ends_at ? formatDateTimeVi(fs.ends_at) : "—"}
                    </td>
                    <td className={shared.colNum}>
                      {fs.min_discount_percent ? `${fs.min_discount_percent}%` : "Không giới hạn"}
                    </td>
                    <td className={shared.colNum}>{formatInt(fs.items?.length ?? 0)} SKU</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {flashSales.map((fs) => (
              <div key={fs.id} className={shared.mobileItem}>
                <div className={shared.mobileLine1}>
                  <span className={shared.mobileIdentity}>{fs.name}</span>
                  <span className={shared.mobileMetric}>
                    {formatInt(fs.items?.length ?? 0)} SKU
                  </span>
                </div>
                <div className={shared.mobileLine2}>
                  <span className={shared.mobileMetaText}>
                    {fs.starts_at ? formatDateTimeVi(fs.starts_at) : "—"}
                  </span>
                  <span>
                    {fs.min_discount_percent ? `Giảm ≥${fs.min_discount_percent}%` : "Mọi mức giảm"}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
