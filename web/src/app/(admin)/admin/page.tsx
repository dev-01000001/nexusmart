"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import type {
  GatewayAuditLog,
  GatewayLedgerSummary,
  GatewayOrder,
  GatewayProduct,
  GatewayShop,
  GatewayUser,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { BarChart, Button, LineChart, StateView } from "@/components";
import {
  formatCompactVnd,
  formatDateTimeVi,
  formatDayMonthVi,
  formatInt,
  formatVnDayKey,
  formatVnd,
} from "@/lib/format";
import { getAdminProductStatusMeta, getAdminShopStatusMeta } from "@/features/admin/types";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type OverviewScope = "toan_san" | "thanh_toan" | "van_hanh";

const SCOPE_TABS: Array<{ id: OverviewScope; label: string }> = [
  { id: "toan_san", label: "Toàn sàn" },
  { id: "thanh_toan", label: "Sổ cái" },
  { id: "van_hanh", label: "Đơn hàng" },
];

const DAY_MS = 24 * 60 * 60 * 1000;

function AdminOverviewContent() {
  const [scope, setScope] = useState<OverviewScope>("toan_san");
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [pendingShops, setPendingShops] = useState<GatewayShop[]>([]);
  const [pendingShopsTotal, setPendingShopsTotal] = useState(0);
  const [pendingProducts, setPendingProducts] = useState<GatewayProduct[]>([]);
  const [pendingProductsTotal, setPendingProductsTotal] = useState(0);
  const [ledger, setLedger] = useState<GatewayLedgerSummary | null>(null);
  const [orders, setOrders] = useState<GatewayOrder[]>([]);
  const [ordersTotal, setOrdersTotal] = useState(0);
  const [usersTotal, setUsersTotal] = useState(0);
  const [auditLogs, setAuditLogs] = useState<GatewayAuditLog[]>([]);
  const [auditTotal, setAuditTotal] = useState(0);

  const loadOverview = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [shopsRes, prodsRes, ledgerRes, ordersRes, usersRes, auditRes] = await Promise.all([
        api<PaginatedResponse<GatewayShop>>("admin/shops?status=PENDING&page=1&page_size=5"),
        api<PaginatedResponse<GatewayProduct>>("admin/products?status=PENDING&page=1&page_size=5"),
        api<GatewayLedgerSummary>("admin/ledger/summary"),
        api<PaginatedResponse<GatewayOrder>>("admin/orders?page=1&page_size=50"),
        api<PaginatedResponse<GatewayUser>>("admin/users?page=1&page_size=1"),
        api<PaginatedResponse<GatewayAuditLog>>("admin/audit-logs?page=1&page_size=5"),
      ]);
      setPendingShops(shopsRes.items ?? []);
      setPendingShopsTotal(shopsRes.total ?? (shopsRes.items ?? []).length);
      setPendingProducts(prodsRes.items ?? []);
      setPendingProductsTotal(prodsRes.total ?? (prodsRes.items ?? []).length);
      setLedger(ledgerRes);
      setOrders(ordersRes.items ?? []);
      setOrdersTotal(ordersRes.total ?? (ordersRes.items ?? []).length);
      setUsersTotal(usersRes.total ?? 0);
      setAuditLogs(auditRes.items ?? []);
      setAuditTotal(auditRes.total ?? (auditRes.items ?? []).length);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải số liệu tổng quan quản trị lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadOverview();
  }, [loadOverview]);

  const activeOrders = useMemo(
    () => orders.filter((o) => o.status !== "CANCELLED" && o.status !== "RETURNED"),
    [orders]
  );

  const totalGmvVnd = useMemo(
    () => activeOrders.reduce((sum, o) => sum + o.subtotal_vnd, 0),
    [activeOrders]
  );
  const totalCommissionVnd = useMemo(
    () => activeOrders.reduce((sum, o) => sum + o.commission_vnd, 0),
    [activeOrders]
  );

  const dailySeries = useMemo(() => {
    const nowMs = Date.now();
    const days: Array<{ key: string; label: string; gmv: number; orders: number }> = [];
    const idxMap = new Map<string, number>();

    for (let offset = 6; offset >= 0; offset--) {
      const ts = nowMs - offset * DAY_MS;
      const key = formatVnDayKey(ts);
      idxMap.set(key, days.length);
      days.push({ key, label: formatDayMonthVi(new Date(ts).toISOString()), gmv: 0, orders: 0 });
    }

    for (const o of activeOrders) {
      if (!o.created_at) continue;
      const idx = idxMap.get(formatVnDayKey(o.created_at));
      if (idx !== undefined) {
        days[idx].gmv += o.subtotal_vnd;
        days[idx].orders += 1;
      }
    }
    return days;
  }, [activeOrders]);

  if (loading) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Tổng quan sàn</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tổng hợp số liệu vận hành toàn sàn..."
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
            <h1 className={shared.pageTitle}>Tổng quan sàn</h1>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải số liệu tổng quan quản trị"
            message={errorMsg}
            actionLabel="Tải lại"
            onAction={() => void loadOverview()}
          />
        </div>
      </div>
    );
  }

  const isBalanced = ledger ? ledger.is_balanced : true;
  const ledgerDiff = ledger ? ledger.total_debits_vnd - ledger.total_credits_vnd : 0;

  const findingLine = `Hiện có ${formatInt(pendingShopsTotal)} gian hàng chờ duyệt, ${formatInt(pendingProductsTotal)} sản phẩm chờ kiểm duyệt, ${formatInt(ordersTotal)} đơn hàng và ${formatInt(usersTotal)} tài khoản người dùng trên sàn.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Tổng quan sàn</h1>
          <p className={shared.pageSubtitle}>{findingLine}</p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadOverview()}>
            Làm mới
          </Button>
          <Button variant="primary" size="sm" shape="app" href="/admin/kiem-duyet">
            Kiểm duyệt sản phẩm
          </Button>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phạm vi theo dõi tổng quan">
          {SCOPE_TABS.map((tab) => {
            const active = scope === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => setScope(tab.id)}
              >
                {tab.label}
              </button>
            );
          })}
        </div>
        <div className={shared.tabsMeta}>
          <span className={styles.freshness}>
            Sổ cái: {isBalanced ? "Cân bằng" : "Có chênh lệch"}
          </span>
        </div>
      </div>

      <div className={styles.overviewGrid}>
        <div className={styles.mainCol}>
          <section className={shared.kpiStrip} aria-label="Chỉ số vận hành chính">
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>GMV đơn gần nhất</span>
              <span className={shared.kpiValue}>{formatVnd(totalGmvVnd)}</span>
              <span className={shared.kpiCaption}>
                {formatInt(ordersTotal)} đơn · {formatInt(usersTotal)} người dùng
              </span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Gian hàng chờ duyệt</span>
              <span className={shared.kpiValue}>{formatInt(pendingShopsTotal)}</span>
              <span className={shared.kpiCaption}>
                {pendingShopsTotal > 0 ? "Cần thẩm định hồ sơ" : "Đã xử lý toàn bộ hàng đợi"}
              </span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Sản phẩm chờ duyệt</span>
              <span className={shared.kpiValue}>{formatInt(pendingProductsTotal)}</span>
              <span className={shared.kpiCaption}>
                {pendingProductsTotal > 0
                  ? "Chờ duyệt hiển thị công khai"
                  : "Không có sản phẩm tồn đọng"}
              </span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Trạng thái sổ cái</span>
              <span
                className={`${shared.kpiValue} ${
                  !isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive
                }`}
              >
                {isBalanced ? "Cân bằng" : "Lệch sổ"}
              </span>
              <span className={shared.kpiCaption}>
                Nợ {formatCompactVnd(ledger?.total_debits_vnd ?? 0)} · Có{" "}
                {formatCompactVnd(ledger?.total_credits_vnd ?? 0)}
              </span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="admin-chart-heading">
            <div className={shared.panelHeader}>
              <h2 id="admin-chart-heading" className={shared.panelTitle}>
                {scope === "toan_san"
                  ? "Tổng giá trị đơn hàng 7 ngày gần nhất"
                  : scope === "thanh_toan"
                    ? "Số dư các tài khoản sổ cái"
                    : "Số đơn hàng hợp lệ 7 ngày gần nhất"}
              </h2>
              <span className={shared.panelMeta}>
                Hoa hồng ghi nhận: {formatVnd(totalCommissionVnd)}
              </span>
            </div>

            {scope === "toan_san" && (
              <LineChart
                labels={dailySeries.map((p) => p.label)}
                series={[
                  {
                    name: "GMV ghi nhận",
                    values: dailySeries.map((p) => p.gmv),
                    color: "var(--chart-1)",
                  },
                ]}
                height={208}
                showArea
                formatValue={formatVnd}
                formatTick={formatCompactVnd}
                ariaLabel="Biểu đồ GMV toàn sàn 7 ngày gần nhất"
              />
            )}

            {scope === "thanh_toan" && (
              <BarChart
                labels={(ledger?.accounts ?? []).map((a) => a.account_code)}
                values={(ledger?.accounts ?? []).map((a) => Math.abs(a.balance_vnd))}
                height={208}
                formatValue={formatVnd}
                formatTick={formatCompactVnd}
                ariaLabel="Biểu đồ số dư tài khoản sổ cái"
              />
            )}

            {scope === "van_hanh" && (
              <BarChart
                labels={dailySeries.map((p) => p.label)}
                values={dailySeries.map((p) => p.orders)}
                height={208}
                formatValue={(v) => `${formatInt(v)} đơn`}
                formatTick={(v) => formatInt(v)}
                ariaLabel="Biểu đồ số đơn hàng 7 ngày gần nhất"
              />
            )}

            <div className={styles.chartFooter}>
              <span>Tổng Ghi nợ: {formatVnd(ledger?.total_debits_vnd ?? 0)}</span>
              <span>
                Tổng Ghi có: {formatVnd(ledger?.total_credits_vnd ?? 0)} · Chênh lệch:{" "}
                {formatVnd(ledgerDiff)}
              </span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="ledger-summary-heading">
            <div className={shared.panelHeader}>
              <h2 id="ledger-summary-heading" className={shared.panelTitle}>
                Tóm tắt tài khoản sổ cái ghi kép
              </h2>
              <Link href="/admin/doi-soat" className={shared.panelLink}>
                Mở sổ đối soát
              </Link>
            </div>

            {!ledger || ledger.accounts.length === 0 ? (
              <p className={shared.emptyRow}>Chưa có bút toán nào phát sinh trên sổ cái.</p>
            ) : (
              <>
                <div className={styles.channelTableWrap}>
                  <table className={styles.channelTable}>
                    <thead>
                      <tr>
                        <th>Tài khoản</th>
                        <th className={shared.colNum}>Tổng Nợ</th>
                        <th className={shared.colNum}>Tổng Có</th>
                        <th className={shared.colNum}>Số dư</th>
                      </tr>
                    </thead>
                    <tbody>
                      {ledger.accounts.map((acc) => (
                        <tr key={acc.account_code}>
                          <td>
                            <span>{acc.account_code}</span>
                            <span className={styles.channelMeta}>{acc.account_type}</span>
                          </td>
                          <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.balance_vnd)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className={styles.channelMobileList}>
                  {ledger.accounts.map((acc) => (
                    <div key={acc.account_code} className={styles.channelMobileRow}>
                      <div className={styles.channelMobileLine}>
                        <span className={shared.cellPrimary}>{acc.account_code}</span>
                        <span className={shared.cellPrimary}>{formatVnd(acc.balance_vnd)}</span>
                      </div>
                      <div className={styles.channelMobileLine}>
                        <span className={shared.cellSub}>{acc.account_type}</span>
                        <span className={shared.cellSub}>
                          Nợ {formatVnd(acc.debit_total_vnd)} · Có {formatVnd(acc.credit_total_vnd)}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </>
            )}
          </section>
        </div>

        <aside className={styles.rightRail} aria-label="Hàng đợi và nhật ký gần đây">
          <section className={styles.railSection} aria-labelledby="rail-shops-heading">
            <h2 id="rail-shops-heading" className={styles.railHeading}>
              Gian hàng chờ duyệt ({formatInt(pendingShopsTotal)})
            </h2>
            <div className={styles.railCard}>
              {pendingShops.length === 0 ? (
                <p className={shared.emptyRow}>Không có gian hàng nào chờ duyệt.</p>
              ) : (
                <ul className={styles.railList}>
                  {pendingShops.map((shop) => {
                    const meta = getAdminShopStatusMeta(shop.status);
                    return (
                      <li key={shop.id} className={styles.railRow}>
                        <Link
                          href={`/admin/gian-hang?selected=${encodeURIComponent(shop.id)}`}
                          className={styles.railItemLink}
                        >
                          <div className={styles.railTopLine}>
                            <span className={styles.railPrimary}>{shop.name}</span>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </div>
                          <span className={styles.railSub}>
                            {shop.contact_phone || "Chưa có SĐT"} ·{" "}
                            {shop.created_at ? formatDateTimeVi(shop.created_at) : ""}
                          </span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
              <Link href="/admin/gian-hang" className={shared.viewAllBtn}>
                Xem hàng đợi gian hàng
              </Link>
            </div>
          </section>

          <section className={styles.railSection} aria-labelledby="rail-products-heading">
            <h2 id="rail-products-heading" className={styles.railHeading}>
              Sản phẩm chờ duyệt ({formatInt(pendingProductsTotal)})
            </h2>
            <div className={styles.railCard}>
              {pendingProducts.length === 0 ? (
                <p className={shared.emptyRow}>Không có sản phẩm nào chờ duyệt.</p>
              ) : (
                <ul className={styles.railList}>
                  {pendingProducts.map((prod) => {
                    const meta = getAdminProductStatusMeta(prod.status);
                    return (
                      <li key={prod.id} className={styles.railRow}>
                        <Link href="/admin/kiem-duyet" className={styles.railItemLink}>
                          <div className={styles.railTopLine}>
                            <span className={styles.railPrimary}>{prod.name}</span>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </div>
                          <span className={styles.railSub}>
                            {formatVnd(prod.min_price_vnd)} · {formatInt(prod.skus?.length ?? 0)}{" "}
                            SKU
                          </span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
              <Link href="/admin/kiem-duyet" className={shared.viewAllBtn}>
                Mở hàng đợi kiểm duyệt
              </Link>
            </div>
          </section>

          <section className={styles.railSection} aria-labelledby="rail-audit-heading">
            <h2 id="rail-audit-heading" className={styles.railHeading}>
              Nhật ký kiểm toán gần đây
            </h2>
            <div className={styles.railCard}>
              {auditLogs.length === 0 ? (
                <p className={shared.emptyRow}>Chưa có bản ghi kiểm toán nào.</p>
              ) : (
                <ul className={styles.railList}>
                  {auditLogs.map((log) => (
                    <li key={log.id} className={styles.railRow}>
                      <div className={styles.railItemStatic}>
                        <div className={styles.railTopLine}>
                          <span className={styles.railPrimary}>{log.action}</span>
                          <span className={styles.railTime}>
                            {log.created_at ? formatDateTimeVi(log.created_at) : ""}
                          </span>
                        </div>
                        <span className={styles.railSub}>
                          {log.actor_role} · {log.target_type} · {log.reason || log.target_id}
                        </span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <Link href="/admin/audit-log" className={shared.viewAllBtn}>
                Xem tất cả ({formatInt(auditTotal)})
              </Link>
            </div>
          </section>
        </aside>
      </div>
    </div>
  );
}

function AdminOverviewRoute() {
  return (
    <AdminStateGate
      title="Tổng quan sàn"
      subtitle="Theo dõi GMV, sổ cái ghi kép, hàng đợi duyệt gian hàng/sản phẩm và nhật ký kiểm toán."
      emptyTitle="Chưa có dữ liệu tổng quan trong kỳ chọn"
      emptyMessage="Không có giao dịch hoặc hàng đợi nào được ghi nhận trong khoảng thời gian này."
      errorTitle="Không thể tải số liệu tổng quan quản trị"
      errorMessage="Dịch vụ đối soát và thống kê tạm thời gián đoạn. Thử tải lại để đồng bộ bản ghi mới nhất."
      loadingRows={5}
    >
      {() => <AdminOverviewContent />}
    </AdminStateGate>
  );
}

export default function AdminOverviewPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Tổng quan sàn" />}>
      <AdminOverviewRoute />
    </Suspense>
  );
}
