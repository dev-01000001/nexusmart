"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { BarChart, Button, LineChart, StateView } from "@/components";
import { formatCompactVnd, formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { StatusPill } from "./StatusPill";
import {
  getAccountTypeLabelVi,
  getAdminShopStatusMeta,
  getAuditActionLabelVi,
  getAuditTargetLabelVi,
  type GwAdminOrder,
  type GwAdminShop,
  type GwAuditLog,
  type GwLedgerSummary,
} from "./adminApiTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/page.module.css";

type OverviewScope = "toan_san" | "thanh_toan" | "van_hanh";

const SCOPE_TABS: Array<{ id: OverviewScope; label: string }> = [
  { id: "toan_san", label: "Toàn sàn" },
  { id: "thanh_toan", label: "Thanh toán" },
  { id: "van_hanh", label: "Vận hành" },
];

interface DailyPoint {
  label: string;
  gmv: number;
  orderCount: number;
}

function buildDailySeries(orders: GwAdminOrder[]): DailyPoint[] {
  const vnOffsetMs = 7 * 3600_000;
  const nowVn = new Date(Date.now() + vnOffsetMs);
  const todayUtcDay = Date.UTC(nowVn.getUTCFullYear(), nowVn.getUTCMonth(), nowVn.getUTCDate());
  const dayMs = 24 * 3600_000;

  const points: Array<DailyPoint & { key: string }> = [];
  const byKey = new Map<string, DailyPoint & { key: string }>();
  for (let i = 6; i >= 0; i--) {
    const d = new Date(todayUtcDay - i * dayMs);
    const key = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-${String(d.getUTCDate()).padStart(2, "0")}`;
    const label = `${String(d.getUTCDate()).padStart(2, "0")}/${String(d.getUTCMonth() + 1).padStart(2, "0")}`;
    const pt = { key, label, gmv: 0, orderCount: 0 };
    points.push(pt);
    byKey.set(key, pt);
  }

  for (const o of orders) {
    if (o.status.toUpperCase() === "CANCELLED") continue;
    const ts = o.created_at ? Date.parse(o.created_at) : Number.NaN;
    if (Number.isNaN(ts)) continue;
    const d = new Date(ts + vnOffsetMs);
    const key = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-${String(d.getUTCDate()).padStart(2, "0")}`;
    const pt = byKey.get(key);
    if (pt) {
      pt.gmv += o.total_payable_vnd;
      pt.orderCount += 1;
    }
  }
  return points;
}

export interface AdminOverviewClientProps {
  shops: GwAdminShop[];
  orders: GwAdminOrder[];
  ledger: GwLedgerSummary | null;
  auditLogs: GwAuditLog[];
  loadError: string | null;
}

function AdminOverviewContent({
  shops,
  orders,
  ledger,
  auditLogs,
  loadError,
}: AdminOverviewClientProps) {
  const router = useRouter();
  const [scope, setScope] = useState<OverviewScope>("toan_san");

  const validOrders = useMemo(
    () => orders.filter((o) => o.status.toUpperCase() !== "CANCELLED"),
    [orders]
  );
  const totalGmv = useMemo(
    () => validOrders.reduce((s, o) => s + o.total_payable_vnd, 0),
    [validOrders]
  );
  const totalCommission = useMemo(
    () => validOrders.reduce((s, o) => s + o.commission_vnd, 0),
    [validOrders]
  );
  const dailySeries = useMemo(() => buildDailySeries(validOrders), [validOrders]);

  const pendingShops = useMemo(
    () => shops.filter((s) => s.status.toUpperCase() === "PENDING"),
    [shops]
  );
  const activeShops = useMemo(
    () => shops.filter((s) => s.status.toUpperCase() === "ACTIVE"),
    [shops]
  );

  const priorityShopQueue = useMemo(() => {
    const rank = (status: string) => {
      const s = status.toUpperCase();
      if (s === "PENDING") return 0;
      if (s === "ACTIVE") return 1;
      return 2;
    };
    return [...shops].sort((a, b) => rank(a.status) - rank(b.status)).slice(0, 4);
  }, [shops]);

  const recentAuditLogs = useMemo(() => auditLogs.slice(0, 4), [auditLogs]);

  const ledgerDiff = ledger ? ledger.total_debits_vnd - ledger.total_credits_vnd : 0;
  const isBalanced = ledger ? ledger.is_balanced && ledgerDiff === 0 : true;

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải số liệu tổng quan quản trị"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const findingLine = !isBalanced
    ? `Sổ cái ghi kép đang lệch ${formatSignedVnd(ledgerDiff)} cần đối soát ngay · ${pendingShops.length} gian hàng chờ duyệt và ${formatInt(validOrders.length)} đơn hợp lệ.`
    : `Sổ cái ghi kép cân đối (0₫ chênh lệch) · ${formatInt(activeShops.length)} gian hàng đang hoạt động, ${formatInt(pendingShops.length)} gian hàng chờ duyệt.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Tổng quan sàn</h1>
          <p className={shared.pageSubtitle}>{findingLine}</p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="primary" size="sm" shape="app" href="/admin/gian-hang">
            Duyệt gian hàng
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
          <span className={styles.freshness}>Dữ liệu trực tiếp từ hệ thống</span>
        </div>
      </div>

      <div className={styles.overviewGrid}>
        <div className={styles.mainCol}>
          <section className={shared.kpiStrip} aria-label="Chỉ số vận hành chính">
            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>GMV đơn hàng</span>
              <span className={shared.kpiValue}>{formatVnd(totalGmv)}</span>
              <span className={shared.kpiCaption}>{formatInt(validOrders.length)} đơn hợp lệ</span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Gian hàng chờ duyệt</span>
              <span className={shared.kpiValue}>{formatInt(pendingShops.length)}</span>
              <span className={shared.kpiCaption}>
                {formatInt(activeShops.length)} gian hàng đang hoạt động
              </span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Hoa hồng sàn ghi nhận</span>
              <span className={shared.kpiValue}>{formatVnd(totalCommission)}</span>
              <span className={shared.kpiCaption}>Tính trên đơn hàng không bị hủy</span>
            </div>

            <div className={shared.kpiCell}>
              <span className={shared.kpiLabel}>Trạng thái sổ cái</span>
              <span
                className={`${shared.kpiValue} ${!isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive}`}
              >
                {isBalanced ? "0₫ lệch" : formatSignedVnd(ledgerDiff)}
              </span>
              <span className={shared.kpiCaption}>
                {isBalanced ? "Tổng Nợ = Tổng Có" : "Cần chạy đối soát ngay"}
              </span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="admin-chart-heading">
            <div className={shared.panelHeader}>
              <h2 id="admin-chart-heading" className={shared.panelTitle}>
                {scope === "toan_san"
                  ? "Tổng thu GMV 7 ngày gần nhất"
                  : scope === "thanh_toan"
                    ? "Doanh số theo ngày (7 ngày)"
                    : "Số đơn hàng theo ngày (7 ngày)"}
              </h2>
              <span className={shared.panelMeta}>
                Tổng {formatInt(validOrders.length)} đơn hàng
              </span>
            </div>

            {scope === "toan_san" && (
              <LineChart
                labels={dailySeries.map((p) => p.label)}
                series={[
                  {
                    name: "GMV đã ghi nhận",
                    values: dailySeries.map((p) => p.gmv),
                    color: "var(--chart-1)",
                  },
                ]}
                height={208}
                showArea
                formatValue={formatVnd}
                formatTick={formatCompactVnd}
                ariaLabel="Biểu đồ tổng thu GMV toàn sàn theo ngày"
              />
            )}

            {scope === "thanh_toan" && (
              <BarChart
                labels={dailySeries.map((p) => p.label)}
                values={dailySeries.map((p) => p.gmv)}
                height={208}
                formatValue={formatVnd}
                formatTick={formatCompactVnd}
                ariaLabel="Biểu đồ tổng thu thanh toán theo ngày"
              />
            )}

            {scope === "van_hanh" && (
              <BarChart
                labels={dailySeries.map((p) => p.label)}
                values={dailySeries.map((p) => p.orderCount)}
                height={208}
                formatValue={(v) => `${formatInt(v)} đơn`}
                formatTick={(v) => formatInt(v)}
                ariaLabel="Biểu đồ số đơn hàng theo ngày"
              />
            )}

            <div className={styles.chartFooter}>
              <span>
                Tổng Ghi có sổ cái: {formatVnd(ledger?.total_credits_vnd ?? 0)} · Tổng Ghi nợ:{" "}
                {formatVnd(ledger?.total_debits_vnd ?? 0)}
              </span>
              <span>{formatInt(ledger?.accounts.length ?? 0)} tài khoản sổ cái đang theo dõi</span>
            </div>
          </section>

          <section className={shared.panel} aria-labelledby="channel-recon-heading">
            <div className={shared.panelHeader}>
              <h2 id="channel-recon-heading" className={shared.panelTitle}>
                Tổng hợp tài khoản sổ cái ghi kép
              </h2>
              <Link href="/admin/doi-soat" className={shared.panelLink}>
                Mở đối soát & sổ cái
              </Link>
            </div>

            {!ledger || ledger.accounts.length === 0 ? (
              <p className={shared.emptyRow}>Chưa phát sinh bút toán nào trên sổ cái thanh toán.</p>
            ) : (
              <>
                <div className={styles.channelTableWrap}>
                  <table className={styles.channelTable}>
                    <thead>
                      <tr>
                        <th>Tài khoản sổ cái</th>
                        <th className={shared.colNum}>Tổng Ghi nợ</th>
                        <th className={shared.colNum}>Tổng Ghi có</th>
                        <th className={shared.colNum}>Số dư ròng</th>
                      </tr>
                    </thead>
                    <tbody>
                      {ledger.accounts.slice(0, 8).map((acc) => (
                        <tr key={acc.account_code}>
                          <td>
                            <span>{acc.account_code}</span>
                            <span className={styles.channelMeta}>
                              {getAccountTypeLabelVi(acc.account_type)}
                            </span>
                          </td>
                          <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(acc.balance_vnd)}</td>
                        </tr>
                      ))}
                    </tbody>
                    <tfoot>
                      <tr>
                        <td>Tổng toàn sổ cái</td>
                        <td className={shared.colNum}>{formatVnd(ledger.total_debits_vnd)}</td>
                        <td className={shared.colNum}>{formatVnd(ledger.total_credits_vnd)}</td>
                        <td className={`${shared.colNum} ${!isBalanced ? shared.numNegative : ""}`}>
                          {isBalanced ? "Khớp 0₫" : formatSignedVnd(ledgerDiff)}
                        </td>
                      </tr>
                    </tfoot>
                  </table>
                </div>

                <div className={styles.channelMobileList}>
                  {ledger.accounts.slice(0, 6).map((acc) => (
                    <div key={acc.account_code} className={styles.channelMobileRow}>
                      <div className={styles.channelMobileLine}>
                        <span className={shared.cellPrimary}>{acc.account_code}</span>
                        <span className={shared.cellPrimary}>{formatVnd(acc.balance_vnd)}</span>
                      </div>
                      <div className={styles.channelMobileLine}>
                        <span className={shared.cellSub}>
                          {getAccountTypeLabelVi(acc.account_type)}
                        </span>
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
              Hàng đợi gian hàng
            </h2>
            <div className={styles.railCard}>
              {priorityShopQueue.length === 0 ? (
                <p className={shared.emptyRow}>Chưa có gian hàng nào.</p>
              ) : (
                <ul className={styles.railList}>
                  {priorityShopQueue.map((shop) => {
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
                            {shop.contact_phone || shop.id.slice(0, 8)}
                            {shop.created_at ? ` · ${formatShortTimeDateVi(shop.created_at)}` : ""}
                          </span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
              <Link href="/admin/gian-hang" className={shared.viewAllBtn}>
                Xem tất cả ({formatInt(shops.length)})
              </Link>
            </div>
          </section>

          <section className={styles.railSection} aria-labelledby="rail-audit-heading">
            <h2 id="rail-audit-heading" className={styles.railHeading}>
              Nhật ký kiểm toán gần đây
            </h2>
            <div className={styles.railCard}>
              {recentAuditLogs.length === 0 ? (
                <p className={shared.emptyRow}>Chưa có bản ghi kiểm toán nào.</p>
              ) : (
                <ul className={styles.railList}>
                  {recentAuditLogs.map((log) => (
                    <li key={log.id} className={styles.railRow}>
                      <div className={styles.railItemStatic}>
                        <div className={styles.railTopLine}>
                          <span className={styles.railPrimary}>
                            {getAuditActionLabelVi(log.action)}
                          </span>
                          <span className={styles.railTime}>
                            {log.created_at ? formatShortTimeDateVi(log.created_at) : ""}
                          </span>
                        </div>
                        <span className={styles.railSub}>
                          {getAuditTargetLabelVi(log.target_type)} · {log.target_id.slice(0, 8)}
                          {log.reason ? ` · ${log.reason}` : ""}
                        </span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <Link href="/admin/audit-log" className={shared.viewAllBtn}>
                Xem tất cả ({formatInt(auditLogs.length)})
              </Link>
            </div>
          </section>
        </aside>
      </div>
    </div>
  );
}

export function AdminOverviewClient(props: AdminOverviewClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Tổng quan sàn" />}>
      <AdminStateGate
        title="Tổng quan sàn"
        subtitle="Theo dõi GMV, đối soát sổ cái ghi kép, hàng đợi duyệt gian hàng và nhật ký kiểm toán."
        emptyTitle="Chưa có dữ liệu tổng quan"
        emptyMessage="Không có giao dịch hoặc gian hàng nào được ghi nhận trong hệ thống."
        errorTitle="Không thể tải số liệu tổng quan quản trị"
        errorMessage="Dịch vụ đối soát và thống kê tạm thời gián đoạn. Thử tải lại để đồng bộ bản ghi mới nhất."
        loadingRows={5}
      >
        {() => <AdminOverviewContent {...props} />}
      </AdminStateGate>
    </Suspense>
  );
}
