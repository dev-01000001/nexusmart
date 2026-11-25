"use client";

import React, { Suspense, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnIsoDate, formatVnd } from "@/lib/format";
import { getSalesChannelMeta, type SalesChannelFilter } from "@/lib/salesChannel";
import { EMPTY_DASH } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { AdminDrawer, SlidingTabBar } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import {
  getAccountTypeLabelVi,
  getReconItemStatusMeta,
  type GwAccountBalance,
  type GwLedgerSummary,
  type GwReconciliationItem,
  type GwReconciliationReport,
} from "./adminApiTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/doi-soat/page.module.css";

const CHANNEL_TABS: Array<{ id: SalesChannelFilter; label: string; dataChannelTab: string }> = [
  { id: "", label: "Tất cả kênh", dataChannelTab: "ALL" },
  { id: "MARKETPLACE", label: "Sàn NexusMart", dataChannelTab: "MARKETPLACE" },
  { id: "STOREFRONT", label: "Site riêng", dataChannelTab: "STOREFRONT" },
];

function channelScopeLabel(channel: SalesChannelFilter): string {
  if (channel === "MARKETPLACE") return "Sàn NexusMart";
  if (channel === "STOREFRONT") return "Site riêng";
  return "tất cả kênh";
}

function ledgerUrlFor(channel: SalesChannelFilter): string {
  return channel
    ? `/api/admin/ledger?sales_channel=${encodeURIComponent(channel)}`
    : "/api/admin/ledger";
}

type DrawerTarget =
  | { kind: "account"; account: GwAccountBalance }
  | { kind: "recon"; item: GwReconciliationItem; runId: string };

export interface AdminReconciliationClientProps {
  initialLedger: GwLedgerSummary | null;
  loadError: string | null;
  serverNowMs?: number;
}

function ReconciliationContent({
  initialLedger,
  loadError,
  serverNowMs,
  initialQuery,
}: {
  initialLedger: GwLedgerSummary | null;
  loadError: string | null;
  serverNowMs?: number;
  initialQuery: string;
}) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [ledger, setLedger] = useState<GwLedgerSummary | null>(initialLedger);
  const [channelFilter, setChannelFilter] = useState<SalesChannelFilter>("");
  const [loadingChannel, setLoadingChannel] = useState(false);
  const [channelError, setChannelError] = useState<string | null>(null);
  const channelReqSeq = useRef(0);
  const [query, setQuery] = useState(initialQuery);
  const [accountTypeFilter, setAccountTypeFilter] = useState<string>("all");
  const [runDate, setRunDate] = useState<string>(() => formatVnIsoDate(serverNowMs ?? Date.now()));
  const [reportDate, setReportDate] = useState<string>("");
  const [running, setRunning] = useState(false);
  const [report, setReport] = useState<GwReconciliationReport | null>(null);
  const [runError, setRunError] = useState<string | null>(null);
  const [drawerTarget, setDrawerTarget] = useState<DrawerTarget | null>(null);

  const handleSelectChannel = async (nextChannel: SalesChannelFilter) => {
    if (nextChannel === channelFilter && !channelError) return;
    setChannelFilter(nextChannel);
    setChannelError(null);
    setRunError(null);
    setReport(null);
    setReportDate("");
    setRunning(false);
    setLoadingChannel(true);
    const reqId = ++channelReqSeq.current;
    try {
      const fresh = await api<GwLedgerSummary>(ledgerUrlFor(nextChannel));
      if (channelReqSeq.current !== reqId) return;
      setLedger(fresh);
    } catch (err) {
      if (channelReqSeq.current !== reqId) return;
      const msg =
        err instanceof ApiError ? err.message : "Không thể tải số liệu sổ cái cho kênh bán này.";
      setChannelError(msg);
      showToast(msg);
    } finally {
      if (channelReqSeq.current === reqId) {
        setLoadingChannel(false);
      }
    }
  };

  const filteredAccounts = useMemo(() => {
    const accounts = ledger?.accounts ?? [];
    const q = query.trim().toLowerCase();
    return accounts.filter((acc) => {
      if (accountTypeFilter !== "all" && acc.account_type.toUpperCase() !== accountTypeFilter) {
        return false;
      }
      if (!q) return true;
      return (
        acc.account_code.toLowerCase().includes(q) || acc.account_type.toLowerCase().includes(q)
      );
    });
  }, [ledger, query, accountTypeFilter]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sổ cái đối soát"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const totalDebits = ledger?.total_debits_vnd ?? 0;
  const totalCredits = ledger?.total_credits_vnd ?? 0;
  const diff = totalDebits - totalCredits;
  const isBalanced = Boolean(ledger?.is_balanced && diff === 0);

  const handleRunReconcile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (running) return;
    const targetDate = runDate;
    const activeChannel = channelFilter;
    const reqId = ++channelReqSeq.current;
    setLoadingChannel(false);
    setRunning(true);
    setRunError(null);
    try {
      const [reconRes, freshLedger] = await Promise.all([
        api<GwReconciliationReport>("/api/admin/reconcile", {
          method: "POST",
          body: activeChannel
            ? { date: targetDate, sales_channel: activeChannel }
            : { date: targetDate },
        }),
        api<GwLedgerSummary>(ledgerUrlFor(activeChannel)),
      ]);
      if (channelReqSeq.current !== reqId) return;
      setReport(reconRes);
      setReportDate(targetDate);
      setLedger(freshLedger);
      const issues =
        reconRes.missing_local_count +
        reconRes.missing_provider_count +
        reconRes.amount_mismatch_count;
      const scopeSuffix = activeChannel ? ` (${channelScopeLabel(activeChannel)})` : "";
      showToast(
        issues === 0
          ? `Đối soát ${targetDate}${scopeSuffix}: ${formatInt(reconRes.matched_count)} giao dịch khớp.`
          : `Đối soát ${targetDate}${scopeSuffix}: phát hiện ${formatInt(issues)} mục lệch.`
      );
    } catch (err) {
      if (channelReqSeq.current !== reqId) return;
      const msg = err instanceof ApiError ? err.message : "Không thể chạy phiên đối soát lúc này.";
      setRunError(msg);
      showToast(msg);
    } finally {
      if (channelReqSeq.current === reqId) {
        setRunning(false);
      }
    }
  };

  const activeScopeLabel = channelScopeLabel(channelFilter);
  const reportIssuesCount = report
    ? report.amount_mismatch_count + report.missing_local_count + report.missing_provider_count
    : 0;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Đối soát &amp; sổ cái</h1>
        </div>
        <form className={styles.runFormInline} onSubmit={handleRunReconcile}>
          <label htmlFor="recon-date" className="sr-only">
            Ngày đối soát
          </label>
          <input
            id="recon-date"
            type="date"
            className={shared.toolbarDate}
            value={runDate}
            onChange={(e) => setRunDate(e.target.value)}
            aria-label="Chọn ngày đối soát"
          />
          <Button type="submit" variant="primary" size="sm" shape="app" loading={running}>
            Chạy đối soát ngay
          </Button>
        </form>
      </header>

      <SlidingTabBar
        tabs={CHANNEL_TABS}
        activeId={channelFilter}
        onChange={(id) => void handleSelectChannel(id)}
        ariaLabel="Lọc sổ cái và đối soát theo kênh bán"
        rightSlot={
          <span className={shared.resultCount}>
            {loadingChannel ? "Đang tải..." : `Phạm vi: ${activeScopeLabel}`}
          </span>
        }
      />

      {channelError && <p className={shared.validationError}>{channelError}</p>}
      {runError && <p className={shared.validationError}>{runError}</p>}

      <section className={shared.kpiStrip} aria-label="Tổng hợp sổ cái ghi kép">
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng Ghi có</span>
          <span className={shared.kpiValue}>{formatVnd(totalCredits)}</span>
          <span className={shared.kpiCaption}>Số nguyên VND</span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng Ghi nợ</span>
          <span className={shared.kpiValue}>{formatVnd(totalDebits)}</span>
          <span className={shared.kpiCaption}>Đối ứng ghi kép</span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chênh lệch Nợ và Có</span>
          <span
            className={`${shared.kpiValue} ${!isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive}`}
          >
            {isBalanced ? "0₫" : formatSignedVnd(diff)}
          </span>
          <span className={shared.kpiCaption}>
            {isBalanced ? "Cân đối tuyệt đối" : "Cần kiểm tra lệch"}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tài khoản sổ cái</span>
          <span className={shared.kpiValue}>{formatInt(ledger?.accounts.length ?? 0)}</span>
          <span className={shared.kpiCaption}>Escrow, gian hàng &amp; sàn</span>
        </div>
      </section>

      {report && (
        <section className={shared.panel} aria-labelledby="recon-result-heading">
          <div className={shared.panelHeader}>
            <h2 id="recon-result-heading" className={shared.panelTitle}>
              Kết quả đối soát {reportDate}
            </h2>
            <span className={shared.panelMeta}>
              Phiên {report.run_id.slice(0, 12)}, khớp {formatInt(report.matched_count)}, lệch{" "}
              {formatInt(reportIssuesCount)}
            </span>
          </div>

          {report.items.length === 0 ? (
            <p className={shared.emptyRow}>Không có giao dịch phát sinh ngày {reportDate}.</p>
          ) : (
            <>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã giao dịch nội bộ</th>
                      <th>Mã tham chiếu cổng</th>
                      <th>Kênh bán</th>
                      <th className={shared.colNum}>Nội bộ</th>
                      <th className={shared.colNum}>Sao kê</th>
                      <th>Trạng thái</th>
                      <th>Ghi chú</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.items.map((item, idx) => {
                      const meta = getReconItemStatusMeta(item.status);
                      const channelMeta = getSalesChannelMeta(item.sales_channel);
                      return (
                        <tr
                          key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}
                          className={shared.rowInteractive}
                          onClick={() =>
                            setDrawerTarget({ kind: "recon", item, runId: report.run_id })
                          }
                        >
                          <td className={shared.cellPrimary}>
                            <button
                              type="button"
                              className={shared.rowTriggerBtn}
                              onClick={(e) => {
                                e.stopPropagation();
                                setDrawerTarget({ kind: "recon", item, runId: report.run_id });
                              }}
                            >
                              {item.payment_id || EMPTY_DASH}
                            </button>
                          </td>
                          <td>{item.provider_tx_id || EMPTY_DASH}</td>
                          <td>
                            <span
                              className={`${shared.channelBadge} ${channelMeta.isStorefront ? shared.channelBadgeStorefront : ""}`}
                              data-sales-channel={channelMeta.value}
                            >
                              {channelMeta.label}
                            </span>
                          </td>
                          <td className={shared.colNum}>{formatVnd(item.local_amount_vnd)}</td>
                          <td className={shared.colNum}>{formatVnd(item.provider_amount_vnd)}</td>
                          <td>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </td>
                          <td>{item.note || EMPTY_DASH}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {report.items.map((item, idx) => {
                  const meta = getReconItemStatusMeta(item.status);
                  const channelMeta = getSalesChannelMeta(item.sales_channel);
                  const itemDiff = item.local_amount_vnd - item.provider_amount_vnd;
                  return (
                    <div
                      key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}
                      className={shared.mobileItem}
                    >
                      <button
                        type="button"
                        className={shared.mobileItemBtn}
                        onClick={() =>
                          setDrawerTarget({ kind: "recon", item, runId: report.run_id })
                        }
                      >
                        <span className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>
                            {item.payment_id || item.provider_tx_id || EMPTY_DASH}
                          </span>
                          <span
                            className={`${shared.mobileMetric} ${itemDiff !== 0 ? shared.numNegative : ""}`}
                          >
                            {formatVnd(item.local_amount_vnd)}
                          </span>
                        </span>
                        <span className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {channelMeta.label}, sao kê {formatVnd(item.provider_amount_vnd)}
                            {itemDiff !== 0 ? ` (${formatSignedVnd(itemDiff)})` : ""}
                          </span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </span>
                      </button>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </section>
      )}

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
              placeholder="Tìm mã tài khoản sổ cái..."
              aria-label="Tìm kiếm tài khoản sổ cái"
            />
          </div>

          <select
            className={shared.toolbarSelect}
            value={accountTypeFilter}
            onChange={(e) => setAccountTypeFilter(e.target.value)}
            aria-label="Lọc theo nhóm tài khoản"
          >
            <option value="all">Tất cả nhóm tài khoản</option>
            <option value="ASSET">Tài sản</option>
            <option value="LIABILITY">Công nợ phải trả</option>
            <option value="REVENUE">Doanh thu sàn</option>
            <option value="EXPENSE">Chi phí / hoàn trả</option>
          </select>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filteredAccounts.length)} tài khoản</span>
        </div>
      </div>

      {filteredAccounts.length === 0 ? (
        <p className={shared.emptyRow}>Chưa có tài khoản sổ cái khớp bộ lọc.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Mã tài khoản</th>
                  <th>Nhóm tài khoản</th>
                  <th className={shared.colNum}>Tổng Ghi nợ</th>
                  <th className={shared.colNum}>Tổng Ghi có</th>
                  <th className={shared.colNum}>Số dư hiện tại</th>
                </tr>
              </thead>
              <tbody>
                {filteredAccounts.map((acc) => {
                  const selected =
                    drawerTarget?.kind === "account" &&
                    drawerTarget.account.account_code === acc.account_code;
                  return (
                    <tr
                      key={acc.account_code}
                      className={`${shared.rowInteractive} ${selected ? shared.rowSelected : ""}`}
                      onClick={() => setDrawerTarget({ kind: "account", account: acc })}
                    >
                      <td className={shared.cellPrimary}>
                        <button
                          type="button"
                          className={shared.rowTriggerBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setDrawerTarget({ kind: "account", account: acc });
                          }}
                        >
                          {acc.account_code}
                        </button>
                      </td>
                      <td>{getAccountTypeLabelVi(acc.account_type)}</td>
                      <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                      <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                      <td className={shared.colNum}>
                        <strong>{formatVnd(acc.balance_vnd)}</strong>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filteredAccounts.map((acc) => (
              <div key={acc.account_code} className={shared.mobileItem}>
                <button
                  type="button"
                  className={shared.mobileItemBtn}
                  onClick={() => setDrawerTarget({ kind: "account", account: acc })}
                >
                  <span className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{acc.account_code}</span>
                    <span className={shared.mobileMetric}>{formatVnd(acc.balance_vnd)}</span>
                  </span>
                  <span className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {getAccountTypeLabelVi(acc.account_type)}, Nợ {formatVnd(acc.debit_total_vnd)}
                      , Có {formatVnd(acc.credit_total_vnd)}
                    </span>
                  </span>
                </button>
              </div>
            ))}
          </div>
        </>
      )}

      <AdminDrawer
        open={Boolean(drawerTarget)}
        onClose={() => setDrawerTarget(null)}
        title={
          drawerTarget?.kind === "account"
            ? drawerTarget.account.account_code
            : drawerTarget?.kind === "recon"
              ? drawerTarget.item.payment_id || "Chi tiết đối soát"
              : ""
        }
        subtitle={
          drawerTarget?.kind === "account"
            ? getAccountTypeLabelVi(drawerTarget.account.account_type)
            : drawerTarget?.kind === "recon"
              ? `Phiên ${drawerTarget.runId.slice(0, 12)}`
              : undefined
        }
        headerBadge={
          drawerTarget?.kind === "recon" ? (
            <StatusPill
              label={getReconItemStatusMeta(drawerTarget.item.status).label}
              tone={getReconItemStatusMeta(drawerTarget.item.status).tone}
            />
          ) : drawerTarget?.kind === "account" ? (
            <StatusPill
              label={isBalanced ? "Cân đối" : "Có chênh lệch"}
              tone={isBalanced ? "positive" : "negative"}
            />
          ) : undefined
        }
      >
        {drawerTarget?.kind === "account" && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã tài khoản</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerTarget.account.account_code}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Nhóm tài khoản</span>
                <span className={shared.detailVal}>
                  {getAccountTypeLabelVi(drawerTarget.account.account_type)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Tổng Ghi nợ</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.account.debit_total_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Tổng Ghi có</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.account.credit_total_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số dư ròng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.account.balance_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Phạm vi lọc</span>
                <span className={shared.detailValPlain}>{activeScopeLabel}</span>
              </div>
            </div>
          </div>
        )}

        {drawerTarget?.kind === "recon" && (
          <div className={shared.detailWell}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã giao dịch nội bộ</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerTarget.item.payment_id || EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã tham chiếu cổng</span>
                <span className={`${shared.detailVal} num`}>
                  {drawerTarget.item.provider_tx_id || EMPTY_DASH}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số tiền nội bộ</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.item.local_amount_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số tiền sao kê</span>
                <span className={`${shared.detailVal} num`}>
                  {formatVnd(drawerTarget.item.provider_amount_vnd)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Chênh lệch</span>
                <span
                  className={`${shared.detailVal} num ${
                    drawerTarget.item.local_amount_vnd !== drawerTarget.item.provider_amount_vnd
                      ? shared.numNegative
                      : shared.numPositive
                  }`}
                >
                  {formatSignedVnd(
                    drawerTarget.item.local_amount_vnd - drawerTarget.item.provider_amount_vnd
                  )}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Kênh bán</span>
                <span className={shared.detailValPlain}>
                  {getSalesChannelMeta(drawerTarget.item.sales_channel).label}
                </span>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Ghi chú</span>
                <span className={shared.detailValPlain}>
                  {drawerTarget.item.note || EMPTY_DASH}
                </span>
              </div>
            </div>
          </div>
        )}
      </AdminDrawer>
    </div>
  );
}

export function AdminReconciliationClient(props: AdminReconciliationClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Đối soát & sổ cái" />}>
      <AdminStateGate
        title="Đối soát & sổ cái"
        emptyTitle="Chưa có dữ liệu đối soát"
        emptyMessage="Sổ cái sẽ hiển thị ngay khi phát sinh giao dịch."
        errorTitle="Không thể tải sổ đối soát"
        errorMessage="Dịch vụ sổ cái thanh toán tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery }) => (
          <ReconciliationContent
            initialLedger={props.initialLedger}
            loadError={props.loadError}
            serverNowMs={props.serverNowMs}
            initialQuery={initialQuery}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
