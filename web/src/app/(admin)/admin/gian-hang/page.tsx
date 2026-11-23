"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayShop,
  GatewayShopBankAccount,
  GatewayShopBankAccountResponse,
  PaginatedResponse,
  ShopStatus,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { EMPTY_DASH } from "@/features/admin/adminMeta";
import { getAdminShopStatusMeta } from "@/features/admin/types";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ShopFilterTab = "all" | ShopStatus | "BANK_REVIEW_PENDING";
type ShopSortKey = "pending_first" | "newest" | "name";

const STATUS_PRIORITY: Record<string, number> = {
  PENDING: 0,
  REJECTED: 1,
  SUSPENDED: 2,
  ACTIVE: 3,
};

function ShopApprovalContent({
  initialQuery,
  initialSelected,
}: {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const { showToast } = useMockStore();
  const [shops, setShops] = useState<GatewayShop[]>([]);
  const [bankPendingShops, setBankPendingShops] = useState<GatewayShop[]>([]);
  const [bankPendingTotal, setBankPendingTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [activeTab, setActiveTab] = useState<ShopFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [sortBy, setSortBy] = useState<ShopSortKey>("pending_first");
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(initialSelected);
  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [submittingId, setSubmittingId] = useState<string | null>(null);

  // Shop bank account review state
  const [bankByShopId, setBankByShopId] = useState<
    Record<string, { loading: boolean; account: GatewayShopBankAccount | null; error?: string }>
  >({});
  const [bankReasonDrafts, setBankReasonDrafts] = useState<Record<string, string>>({});
  const [bankSubmittingShopId, setBankSubmittingShopId] = useState<string | null>(null);

  const loadShops = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [allRes, bankRes] = await Promise.all([
        api<PaginatedResponse<GatewayShop>>("admin/shops?page=1&page_size=100"),
        api<PaginatedResponse<GatewayShop>>(
          "admin/shops?bank_review_pending=true&page=1&page_size=100"
        ),
      ]);
      setShops(allRes.items ?? []);
      setBankPendingShops(bankRes.items ?? []);
      setBankPendingTotal(bankRes.total ?? (bankRes.items ?? []).length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách hồ sơ gian hàng lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadShops();
  }, [loadShops]);

  const counts = useMemo(() => {
    const map: Record<ShopFilterTab, number> = {
      all: shops.length,
      PENDING: 0,
      ACTIVE: 0,
      REJECTED: 0,
      SUSPENDED: 0,
      BANK_REVIEW_PENDING: bankPendingTotal,
    };
    for (const shop of shops) {
      if (shop.status in map) {
        map[shop.status as ShopStatus] += 1;
      }
    }
    return map;
  }, [shops, bankPendingTotal]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const source = activeTab === "BANK_REVIEW_PENDING" ? bankPendingShops : shops;
    const list = source.filter((shop) => {
      if (activeTab !== "all" && activeTab !== "BANK_REVIEW_PENDING" && shop.status !== activeTab) {
        return false;
      }
      if (!q) return true;
      return (
        shop.name.toLowerCase().includes(q) ||
        (shop.slug ?? "").toLowerCase().includes(q) ||
        shop.contact_phone.toLowerCase().includes(q) ||
        shop.business_license_doc.toLowerCase().includes(q) ||
        shop.description.toLowerCase().includes(q) ||
        shop.id.toLowerCase().includes(q)
      );
    });

    return [...list].sort((a, b) => {
      if (sortBy === "newest") {
        return (b.created_at ?? "").localeCompare(a.created_at ?? "");
      }
      if (sortBy === "name") {
        return a.name.localeCompare(b.name, "vi");
      }
      const rankDiff = (STATUS_PRIORITY[a.status] ?? 9) - (STATUS_PRIORITY[b.status] ?? 9);
      if (rankDiff !== 0) return rankDiff;
      return (b.created_at ?? "").localeCompare(a.created_at ?? "");
    });
  }, [shops, bankPendingShops, activeTab, query, sortBy]);

  const activeDesktopShop = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((s) => s.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  const loadShopBankAccount = useCallback(async (shopId: string) => {
    setBankByShopId((prev) => ({
      ...prev,
      [shopId]: { loading: true, account: prev[shopId]?.account ?? null },
    }));
    try {
      const res = await api<GatewayShopBankAccountResponse>(
        `admin/shops/${encodeURIComponent(shopId)}/bank-account`
      );
      setBankByShopId((prev) => ({
        ...prev,
        [shopId]: { loading: false, account: res?.bank_account ?? null },
      }));
    } catch (err) {
      setBankByShopId((prev) => ({
        ...prev,
        [shopId]: {
          loading: false,
          account: null,
          error: getErrorMessage(err, "Chưa thể tải thông tin ngân hàng gian hàng."),
        },
      }));
    }
  }, []);

  const inspectedShopId = mobileOpenId ?? activeDesktopShop?.id;

  useEffect(() => {
    if (!inspectedShopId) return;
    if (bankByShopId[inspectedShopId]) return;
    void loadShopBankAccount(inspectedShopId);
  }, [inspectedShopId, bankByShopId, loadShopBankAccount]);

  const handleBankReview = async (
    shop: GatewayShop,
    bankAccount: GatewayShopBankAccount,
    decision: "APPROVE" | "REJECT"
  ) => {
    const reason = (bankReasonDrafts[shop.id] ?? "").trim();
    if (decision === "REJECT" && reason.length < 3) {
      showToast("Vui lòng nhập lý do từ chối tài khoản ngân hàng (tối thiểu 3 ký tự).");
      return;
    }
    setBankSubmittingShopId(shop.id);
    try {
      const res = await api<GatewayShopBankAccountResponse>(
        `admin/shops/${encodeURIComponent(shop.id)}/bank-account/review`,
        {
          method: "POST",
          json: {
            bank_account_id: bankAccount.id,
            decision,
            reason:
              reason ||
              (decision === "APPROVE" ? "Đã đối chiếu giấy tờ chủ tài khoản ngân hàng." : ""),
          },
        }
      );
      setBankByShopId((prev) => ({
        ...prev,
        [shop.id]: { loading: false, account: res?.bank_account ?? null },
      }));
      setBankReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      await loadShops();
      showToast(
        decision === "APPROVE"
          ? `Đã phê duyệt tài khoản ngân hàng của "${shop.name}".`
          : `Đã từ chối tài khoản ngân hàng của "${shop.name}".`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xử lý thẩm định tài khoản ngân hàng."));
    } finally {
      setBankSubmittingShopId(null);
    }
  };

  const handleDecision = async (
    shop: GatewayShop,
    decision: "APPROVE" | "REQUEST_INFO" | "REJECT"
  ) => {
    const noteText = (reasonDrafts[shop.id] ?? "").trim();
    if ((decision === "REJECT" || decision === "REQUEST_INFO") && !noteText) {
      setReasonErrorId(shop.id);
      return;
    }
    setReasonErrorId(null);
    setSubmittingId(shop.id);
    try {
      const res = await api<{ shop: GatewayShop }>(`admin/shops/${shop.id}/review`, {
        method: "POST",
        json: {
          decision,
          reason:
            noteText ||
            (decision === "APPROVE" ? "Hồ sơ gian hàng đầy đủ, đã phê duyệt hoạt động." : ""),
        },
      });
      if (res.shop) {
        setShops((prev) => prev.map((item) => (item.id === shop.id ? res.shop : item)));
      } else {
        await loadShops();
      }
      setReasonDrafts((prev) => ({ ...prev, [shop.id]: "" }));
      const label =
        decision === "APPROVE"
          ? `Đã phê duyệt gian hàng "${shop.name}". Chủ gian hàng có thể truy cập Kênh người bán.`
          : decision === "REQUEST_INFO"
            ? `Đã gửi yêu cầu bổ sung hồ sơ cho "${shop.name}".`
            : `Đã từ chối hồ sơ gian hàng "${shop.name}".`;
      showToast(label);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể cập nhật quyết định thẩm định gian hàng."));
    } finally {
      setSubmittingId(null);
    }
  };

  const renderInspectionPanel = (shop: GatewayShop, showHeading = false) => {
    const draftValue = reasonDrafts[shop.id] ?? "";
    const showReasonError = reasonErrorId === shop.id;
    const meta = getAdminShopStatusMeta(shop.status);
    const isBusy = submittingId === shop.id;
    const bankState = bankByShopId[shop.id];
    const bankAcc = bankState?.account ?? null;
    const bankBusy = bankSubmittingShopId === shop.id;
    const bankStatusTone =
      bankAcc?.status === "VERIFIED"
        ? ("positive" as const)
        : bankAcc?.status === "REJECTED"
          ? ("negative" as const)
          : ("warning" as const);
    const bankStatusLabel =
      bankAcc?.status === "VERIFIED"
        ? "Đã xác minh"
        : bankAcc?.status === "PENDING_ADMIN_REVIEW"
          ? "Chờ sàn duyệt"
          : bankAcc?.status === "PENDING_OTP"
            ? "Chờ shop nhập OTP"
            : bankAcc?.status === "REJECTED"
              ? "Bị từ chối"
              : (bankAcc?.status ?? "");

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Chi tiết hồ sơ: {shop.name}
              {shop.slug ? ` (/${shop.slug})` : ""}
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}
        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã gian hàng</span>
            <span className={shared.detailVal}>{shop.id}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã chủ sở hữu</span>
            <span className={shared.detailVal}>{shop.owner_user_id}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Điện thoại liên hệ</span>
            <span className={shared.detailVal}>{shop.contact_phone || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Giấy phép kinh doanh / Tài liệu</span>
            <span className={shared.detailValPlain}>{shop.business_license_doc || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Ngày đăng ký</span>
            <span className={shared.detailValPlain}>
              {shop.created_at ? formatDateTimeVi(shop.created_at) : EMPTY_DASH}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Cập nhật gần nhất</span>
            <span className={shared.detailValPlain}>
              {shop.updated_at ? formatDateTimeVi(shop.updated_at) : EMPTY_DASH}
            </span>
          </div>
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Mô tả gian hàng</span>
            <span className={shared.detailValPlain}>{shop.description || EMPTY_DASH}</span>
          </div>
          {shop.rejection_reason && (
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <span className={shared.detailKey}>Ghi chú / Lý do thẩm định trước đó</span>
              <span className={shared.detailValPlain}>{shop.rejection_reason}</span>
            </div>
          )}
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`shop-note-${shop.id}`} className={shared.fieldLabel}>
              Ghi chú thẩm định hoặc lý do từ chối
            </label>
            <input
              id={`shop-note-${shop.id}`}
              type="text"
              className={shared.textInput}
              value={draftValue}
              onChange={(e) => {
                setReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }));
                if (showReasonError) setReasonErrorId(null);
              }}
              placeholder="Nhập ghi chú gửi chủ gian hàng (bắt buộc khi Từ chối hoặc Yêu cầu bổ sung)..."
            />
            {showReasonError && (
              <span className={styles.validationError}>
                Vui lòng nhập lý do cụ thể trước khi từ chối hoặc yêu cầu bổ sung.
              </span>
            )}
          </div>

          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={isBusy || shop.status === "ACTIVE"}
              onClick={() => void handleDecision(shop, "APPROVE")}
            >
              {isBusy ? "Đang xử lý..." : "Phê duyệt"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={isBusy || shop.status === "ACTIVE"}
              onClick={() => void handleDecision(shop, "REQUEST_INFO")}
            >
              Yêu cầu bổ sung
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={isBusy || shop.status === "REJECTED"}
              onClick={() => void handleDecision(shop, "REJECT")}
            >
              Từ chối
            </Button>
          </div>
        </div>

        {/* Shop Bank Account Inspection & Approval */}
        <div
          style={{
            paddingTop: 14,
            borderTop: "1px solid #e3e8eb",
            display: "flex",
            flexDirection: "column",
            gap: 10,
          }}
        >
          <div className={shared.detailHeader} style={{ paddingBottom: 8 }}>
            <h3 className={shared.detailHeading}>Tài khoản ngân hàng nhận giải ngân</h3>
            <div className={shared.detailHeaderMeta}>
              {bankAcc && <StatusPill label={bankStatusLabel} tone={bankStatusTone} />}
              <button
                type="button"
                className={shared.rowActionBtn}
                onClick={() => void loadShopBankAccount(shop.id)}
              >
                Tải lại
              </button>
            </div>
          </div>

          {bankState?.loading ? (
            <span className={shared.savedStamp}>Đang tải thông tin tài khoản ngân hàng...</span>
          ) : bankState?.error ? (
            <span className={shared.validationError}>{bankState.error}</span>
          ) : !bankAcc ? (
            <span className={shared.savedStamp}>
              Gian hàng chưa đăng ký tài khoản ngân hàng nhận giải ngân.
            </span>
          ) : (
            <>
              <div className={shared.detailGrid}>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Ngân hàng</span>
                  <span className={shared.detailVal}>
                    {bankAcc.bank_code} · {bankAcc.bank_name}
                  </span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Số tài khoản</span>
                  <span className={`${shared.detailVal} num`}>{bankAcc.account_number_masked}</span>
                </div>
                <div className={shared.detailField}>
                  <span className={shared.detailKey}>Chủ tài khoản</span>
                  <span className={shared.detailVal}>
                    {bankAcc.account_holder_name} (
                    {bankAcc.holder_name_matched ? "Khớp tên chủ shop" : "Khác tên chủ shop"})
                  </span>
                </div>
                {bankAcc.payout_locked_until && (
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Khóa giải ngân an toàn đến</span>
                    <span className={shared.detailValPlain}>
                      {formatDateTimeVi(bankAcc.payout_locked_until)}
                    </span>
                  </div>
                )}
                {bankAcc.review_note && (
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.detailKey}>
                      {bankAcc.status === "REJECTED"
                        ? "Lý do từ chối ngân hàng"
                        : "Ghi chú thẩm định ngân hàng"}
                    </span>
                    <span
                      className={
                        bankAcc.status === "REJECTED"
                          ? shared.validationError
                          : shared.detailValPlain
                      }
                    >
                      {bankAcc.review_note}
                    </span>
                  </div>
                )}
              </div>

              {bankAcc.status === "PENDING_ADMIN_REVIEW" && (
                <div className={shared.detailActionBar}>
                  <div className={shared.detailNoteGroup}>
                    <label htmlFor={`bank-note-${shop.id}`} className={shared.fieldLabel}>
                      Ghi chú thẩm định tài khoản ngân hàng
                    </label>
                    <input
                      id={`bank-note-${shop.id}`}
                      type="text"
                      className={shared.textInput}
                      value={bankReasonDrafts[shop.id] ?? ""}
                      onChange={(e) =>
                        setBankReasonDrafts((prev) => ({ ...prev, [shop.id]: e.target.value }))
                      }
                      placeholder="Nhập lý do (bắt buộc khi từ chối tài khoản ngân hàng)..."
                    />
                  </div>
                  <div className={shared.detailButtons}>
                    <Button
                      variant="primary"
                      size="sm"
                      shape="app"
                      disabled={bankBusy}
                      onClick={() => void handleBankReview(shop, bankAcc, "APPROVE")}
                    >
                      {bankBusy ? "Đang xử lý..." : "Duyệt tài khoản ngân hàng"}
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      shape="app"
                      disabled={bankBusy}
                      onClick={() => void handleBankReview(shop, bankAcc, "REJECT")}
                    >
                      Từ chối ngân hàng
                    </Button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    );
  };

  const tabs: Array<{ id: ShopFilterTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "PENDING", label: "Chờ duyệt" },
    { id: "BANK_REVIEW_PENDING", label: "Chờ duyệt tài khoản ngân hàng" },
    { id: "ACTIVE", label: "Đang hoạt động" },
    { id: "REJECTED", label: "Từ chối" },
    { id: "SUSPENDED", label: "Tạm ngưng" },
  ];

  const subtitle =
    counts.PENDING > 0
      ? `Hiện có ${formatInt(counts.PENDING)} hồ sơ gian hàng chờ duyệt mới và ${formatInt(counts.ACTIVE)} gian hàng đang hoạt động.`
      : `Không còn hồ sơ chờ duyệt mới; ${formatInt(counts.ACTIVE)} gian hàng đã được phê duyệt hoạt động.`;

  if (loading) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Duyệt gian hàng</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tải danh sách hồ sơ gian hàng..."
            rows={5}
          />
        </div>
      </div>
    );
  }

  if (errorMsg) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Duyệt gian hàng</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải danh sách hồ sơ gian hàng"
            message={errorMsg}
            actionLabel="Thử lại"
            onAction={() => void loadShops()}
          />
        </div>
      </div>
    );
  }

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Duyệt gian hàng</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadShops()}>
            Làm mới
          </Button>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái hồ sơ gian hàng">
          {tabs.map((tab) => {
            const active = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.id)}
              >
                <span>{tab.label}</span>
                <span className={shared.tabCount}>{counts[tab.id]}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className={shared.toolbar}>
        <div className={shared.toolbarLeft}>
          <div className={shared.searchWrap}>
            <span className={shared.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              type="search"
              className={shared.toolbarSearch}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm tên gian hàng, SĐT, giấy phép..."
              aria-label="Tìm kiếm hồ sơ gian hàng"
            />
          </div>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} gian hàng</span>
          <select
            className={shared.toolbarSelect}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as ShopSortKey)}
            aria-label="Sắp xếp hồ sơ gian hàng"
          >
            <option value="pending_first">Ưu tiên chờ duyệt</option>
            <option value="newest">Mới đăng ký gần nhất</option>
            <option value="name">Tên gian hàng (A-Z)</option>
          </select>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>
          Không tìm thấy hồ sơ gian hàng phù hợp với bộ lọc hiện tại.
        </p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Gian hàng</th>
                  <th>Liên hệ & giấy phép</th>
                  <th>Ngày đăng ký</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((shop) => {
                  const meta = getAdminShopStatusMeta(shop.status);
                  const isSelected = activeDesktopShop?.id === shop.id;
                  return (
                    <tr
                      key={shop.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => {
                        setReasonErrorId(null);
                        setSelectedId(shop.id);
                      }}
                    >
                      <td>
                        <div className={shared.cellPrimary}>{shop.name}</div>
                        <div className={shared.cellSub}>
                          {shop.slug ? `/${shop.slug}` : shop.id}
                        </div>
                      </td>
                      <td>
                        <div>{shop.contact_phone || EMPTY_DASH}</div>
                        <div className={shared.cellSub}>
                          {shop.business_license_doc || "Chưa đính kèm GPKD"}
                        </div>
                      </td>
                      <td>{shop.created_at ? formatDateTimeVi(shop.created_at) : EMPTY_DASH}</td>
                      <td>
                        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                          <StatusPill label={meta.label} tone={meta.tone} />
                          {shop.bank_review_pending && (
                            <StatusPill label="Chờ duyệt TK ngân hàng" tone="warning" />
                          )}
                        </div>
                      </td>
                      <td className={shared.colAction}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setReasonErrorId(null);
                            setSelectedId(shop.id);
                          }}
                        >
                          {isSelected ? "Đang xem" : "Thẩm định"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {activeDesktopShop && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Bảng thẩm định chi tiết hồ sơ gian hàng"
            >
              {renderInspectionPanel(activeDesktopShop, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filtered.map((shop) => {
              const meta = getAdminShopStatusMeta(shop.status);
              const isSelected = mobileOpenId === shop.id;
              return (
                <div key={shop.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => {
                      setReasonErrorId(null);
                      setMobileOpenId((prev) => (prev === shop.id ? null : shop.id));
                    }}
                    aria-expanded={isSelected}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{shop.name}</span>
                      <span className={shared.mobileMetric}>{shop.contact_phone}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {shop.business_license_doc || shop.id}
                      </span>
                      <span className={styles.mobilePillRow}>
                        <StatusPill label={meta.label} tone={meta.tone} />
                        {shop.bank_review_pending && (
                          <StatusPill label="Chờ duyệt TK ngân hàng" tone="warning" />
                        )}
                      </span>
                    </div>
                  </button>
                  {isSelected && (
                    <div className={shared.mobileDetailWrap}>{renderInspectionPanel(shop)}</div>
                  )}
                </div>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}

function ShopApprovalRoute() {
  return (
    <AdminStateGate
      title="Duyệt gian hàng"
      subtitle="Thẩm định giấy phép kinh doanh, thông tin liên hệ và kích hoạt tài khoản người bán."
      emptyTitle="Hàng đợi duyệt gian hàng đang trống"
      emptyMessage="Tất cả hồ sơ đăng ký mở gian hàng mới đã được xử lý hoàn tất."
      errorTitle="Không thể tải danh sách hồ sơ gian hàng"
      errorMessage="Không thể kết nối tới hàng đợi thẩm định lúc này. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <ShopApprovalContent initialQuery={initialQuery} initialSelected={initialSelected} />
      )}
    </AdminStateGate>
  );
}

export default function AdminShopApprovalsPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Duyệt gian hàng" />}>
      <ShopApprovalRoute />
    </Suspense>
  );
}
