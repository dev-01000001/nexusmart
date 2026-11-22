"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import type {
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
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { StatusPill } from "./StatusPill";
import shared from "./adminShared.module.css";

function toDatetimeLocalValue(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function AdminPromoCoinsPanel({ mode }: { mode: "promo" | "coins" }) {
  const { showToast } = useMockStore();

  const [vouchers, setVouchers] = useState<GatewayVoucher[]>([]);
  const [flashSales, setFlashSales] = useState<GatewayFlashSale[]>([]);
  const [users, setUsers] = useState<GatewayUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Create platform voucher
  const defaultStart = useMemo(() => toDatetimeLocalValue(new Date()), []);
  const defaultEnd = useMemo(
    () => toDatetimeLocalValue(new Date(Date.now() + 14 * 24 * 3600 * 1000)),
    []
  );
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
  const [vBusy, setVBusy] = useState(false);

  // Create flash sale window
  const [fsName, setFsName] = useState("");
  const [fsStartsAt, setFsStartsAt] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 3600 * 1000))
  );
  const [fsEndsAt, setFsEndsAt] = useState(() =>
    toDatetimeLocalValue(new Date(Date.now() + 4 * 3600 * 1000))
  );
  const [fsBusy, setFsBusy] = useState(false);

  // Approve / reject flash sale item by item_id
  const [itemIdInput, setItemIdInput] = useState("");
  const [itemReason, setItemReason] = useState("");
  const [itemBusy, setItemBusy] = useState(false);

  // Credit coins form
  const [coinUserId, setCoinUserId] = useState("");
  const [coinAmount, setCoinAmount] = useState("10000");
  const [coinReason, setCoinReason] = useState("");
  const [coinRefId, setCoinRefId] = useState("");
  const [coinBusy, setCoinBusy] = useState(false);
  const [coinResult, setCoinResult] = useState<GatewayCreditCoinsResponse | null>(null);

  const loadData = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      if (mode === "promo") {
        const [vRes, fsRes] = await Promise.all([
          api<{ vouchers: GatewayVoucher[] }>("vouchers"),
          api<{ flash_sales: GatewayFlashSale[] }>("flash-sales?include_upcoming=true"),
        ]);
        setVouchers(vRes.vouchers ?? []);
        setFlashSales(fsRes.flash_sales ?? []);
      } else {
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
  }, [mode]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const handleCreateVoucher = async (e: React.FormEvent) => {
    e.preventDefault();
    const code = vCode.trim().toUpperCase().replace(/\s+/g, "");
    const rawVal = Math.trunc(Number(vValue) || 0);
    const minOrder = Math.max(0, Math.trunc(Number(vMinOrder) || 0));
    const quota = Math.max(1, Math.trunc(Number(vQuota) || 1));
    const perUser = Math.max(1, Math.trunc(Number(vPerUser) || 1));
    const budget = Math.max(0, Math.trunc(Number(vBudget) || 0));
    const maxDiscount =
      vType === "PERCENT" ? Math.max(0, Math.trunc(Number(vMaxDiscount) || 0)) : 0;

    if (code.length < 3) {
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
      await api<{ voucher: GatewayVoucher }>("admin/vouchers", {
        method: "POST",
        json: {
          code,
          name:
            vName.trim() ||
            `Mã ${vScope === "FREESHIP" ? "miễn ship" : "toàn sàn"} giảm ${vType === "FIXED" ? formatVnd(rawVal) : `${rawVal}%`}`,
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
        },
      });
      setVCode("");
      setVName("");
      await loadData();
      showToast(`Đã tạo mã giảm giá cấp sàn ${code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo voucher cấp sàn."));
    } finally {
      setVBusy(false);
    }
  };

  const handleCreateFlashSale = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fsName.trim();
    const startDt = new Date(fsStartsAt);
    const endDt = new Date(fsEndsAt);
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
        },
      });
      setFsName("");
      await loadData();
      showToast(`Đã mở khung giờ Flash sale "${cleanName}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo khung giờ Flash sale."));
    } finally {
      setFsBusy(false);
    }
  };

  const handleReviewFlashItem = async (approve: boolean) => {
    const cleanId = itemIdInput.trim();
    if (!cleanId) {
      showToast("Vui lòng nhập mã đăng ký suất Flash sale (item ID).");
      return;
    }
    if (!approve && itemReason.trim().length < 3) {
      showToast("Vui lòng nhập lý do từ chối suất Flash sale.");
      return;
    }
    setItemBusy(true);
    try {
      await api<{ item: GatewayFlashSaleItem }>(
        `admin/flash-sale-items/${encodeURIComponent(cleanId)}/approve`,
        {
          method: "POST",
          json: {
            approve,
            reason: itemReason.trim(),
          },
        }
      );
      setItemIdInput("");
      setItemReason("");
      await loadData();
      showToast(
        approve
          ? `Đã phê duyệt suất Flash sale ${cleanId}.`
          : `Đã từ chối suất Flash sale ${cleanId}.`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xử lý suất Flash sale này."));
    } finally {
      setItemBusy(false);
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
      const refId = coinRefId.trim() || `admin-credit-${Date.now()}`;
      const res = await api<GatewayCreditCoinsResponse>("admin/coins/credit", {
        method: "POST",
        json: {
          user_id: uid,
          amount,
          reason,
          reference_id: refId,
        },
      });
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
                  min={100}
                  step={100}
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
      <section className={shared.panel} aria-labelledby="admin-voucher-heading">
        <div className={shared.panelHeader}>
          <h2 id="admin-voucher-heading" className={shared.panelTitle}>
            Tạo & quản lý Voucher cấp sàn ({formatInt(vouchers.length)})
          </h2>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadData()}>
            Làm mới
          </Button>
        </div>

        <form onSubmit={(e) => void handleCreateVoucher(e)} className={shared.detailWell}>
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
              />
            </div>
            <div className={shared.detailField}>
              <label htmlFor="av-quota" className={shared.fieldLabel}>
                Tổng lượt phát hành
              </label>
              <input
                id="av-quota"
                type="number"
                min={1}
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
          </div>
          <div className={shared.detailActionBar}>
            <span className={shared.savedStamp}>
              Voucher sàn có thể gộp cùng voucher gian hàng.
            </span>
            <Button type="submit" variant="primary" size="sm" shape="app" disabled={vBusy}>
              {vBusy ? "Đang tạo..." : "Phát hành voucher sàn"}
            </Button>
          </div>
        </form>

        <div className={shared.tableWrap} style={{ marginTop: 14 }}>
          <table className={shared.dataTable}>
            <thead>
              <tr>
                <th>Mã & tên</th>
                <th>Phạm vi</th>
                <th className={shared.colNum}>Mức giảm</th>
                <th className={shared.colNum}>Đã dùng / Tổng suất</th>
                <th>Trạng thái</th>
              </tr>
            </thead>
            <tbody>
              {vouchers.map((v) => (
                <tr key={v.id}>
                  <td>
                    <div className={shared.cellPrimary}>{v.code}</div>
                    <div className={shared.cellSub}>{v.name}</div>
                  </td>
                  <td>
                    <StatusPill label={v.scope} tone="neutral" />
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
                    <StatusPill
                      label={v.is_active ? "Đang hoạt động" : "Tạm dừng"}
                      tone={v.is_active ? "positive" : "neutral"}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className={shared.panel} aria-labelledby="admin-fs-heading">
        <div className={shared.panelHeader}>
          <h2 id="admin-fs-heading" className={shared.panelTitle}>
            Khung giờ Flash sale ({formatInt(flashSales.length)}) & duyệt suất SKU
          </h2>
        </div>

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
          </div>
          <div className={shared.detailActionBar}>
            <span className={shared.savedStamp}>
              Gian hàng có thể đăng ký SKU ngay sau khi khung giờ được tạo.
            </span>
            <Button type="submit" variant="primary" size="sm" shape="app" disabled={fsBusy}>
              {fsBusy ? "Đang tạo..." : "Mở khung Flash sale"}
            </Button>
          </div>
        </form>

        <div className={shared.detailWell} style={{ marginTop: 14 }}>
          <div className={shared.detailGrid}>
            <div className={shared.detailField}>
              <label htmlFor="fs-item-id" className={shared.fieldLabel}>
                Duyệt / từ chối suất đăng ký theo mã (Item ID)
              </label>
              <input
                id="fs-item-id"
                type="text"
                className={shared.textInput}
                value={itemIdInput}
                onChange={(e) => setItemIdInput(e.target.value)}
                placeholder="Dán mã đăng ký Flash sale từ gian hàng..."
              />
            </div>
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <label htmlFor="fs-item-reason" className={shared.fieldLabel}>
                Ghi chú duyệt hoặc lý do từ chối
              </label>
              <input
                id="fs-item-reason"
                type="text"
                className={shared.textInput}
                value={itemReason}
                onChange={(e) => setItemReason(e.target.value)}
                placeholder="Nhập lý do khi từ chối suất đăng ký..."
              />
            </div>
          </div>
          <div className={shared.detailActionBar}>
            <span className={shared.savedStamp}>
              Suất được duyệt sẽ hiển thị ngay trong khung giờ Flash sale công khai.
            </span>
            <div className={shared.detailButtons}>
              <Button
                variant="primary"
                size="sm"
                shape="app"
                disabled={itemBusy}
                onClick={() => void handleReviewFlashItem(true)}
              >
                Phê duyệt suất
              </Button>
              <Button
                variant="danger"
                size="sm"
                shape="app"
                disabled={itemBusy}
                onClick={() => void handleReviewFlashItem(false)}
              >
                Từ chối suất
              </Button>
            </div>
          </div>
        </div>

        <div className={shared.tableWrap} style={{ marginTop: 14 }}>
          <table className={shared.dataTable}>
            <thead>
              <tr>
                <th>Khung giờ Flash sale</th>
                <th>Thời gian mở bán</th>
                <th className={shared.colNum}>Số SKU đã duyệt</th>
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
                  <td className={shared.colNum}>{formatInt(fs.items?.length ?? 0)} SKU</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
