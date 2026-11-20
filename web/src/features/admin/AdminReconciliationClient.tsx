"use client";

import React, { Suspense, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { getSalesChannelMeta, type SalesChannelFilter } from "@/lib/salesChannel";
import { EMPTY_DASH } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { StatusPill } from "./StatusPill";
import {
  getAccountTypeLabelVi,
  getReconItemStatusMeta,
  type GwLedgerSummary,
  type GwReconciliationReport,
} from "./adminApiTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/doi-soat/page.module.css";

const CHANNEL_TABS: Array<{ id: SalesChannelFilter; label: string }> = [
  { id: "", label: "Tất cả kênh" },
  { id: "MARKETPLACE", label: "Sàn NexusMart" },
  { id: "STOREFRONT", label: "Site riêng" },
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

function todayDateString(): string {
  const vnNow = new Date(Date.now() + 7 * 3600_000);
  return vnNow.toISOString().slice(0, 10);
}

export interface AdminReconciliationClientProps {
  initialLedger: GwLedgerSummary | null;
  loadError: string | null;
}

function ReconciliationContent({
  initialLedger,
  loadError,
  initialQuery,
}: {
  initialLedger: GwLedgerSummary | null;
  loadError: string | null;
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
  const [runDate, setRunDate] = useState<string>(todayDateString);
  const [running, setRunning] = useState(false);
  const [report, setReport] = useState<GwReconciliationReport | null>(null);
  const [runError, setRunError] = useState<string | null>(null);

  const handleSelectChannel = async (nextChannel: SalesChannelFilter) => {
    if (nextChannel === channelFilter && !channelError) return;
    setChannelFilter(nextChannel);
    setChannelError(null);
    setRunError(null);
    setReport(null);
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
          title="Không thể tải sổ cái đối soát thanh toán"
          message={loadError}
          actionLabel="Thử tải lại"
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
    setRunning(true);
    setRunError(null);
    const activeChannel = channelFilter;
    try {
      const [reconRes, freshLedger] = await Promise.all([
        api<GwReconciliationReport>("/api/admin/reconcile", {
          method: "POST",
          body: activeChannel ? { date: runDate, sales_channel: activeChannel } : { date: runDate },
        }),
        api<GwLedgerSummary>(ledgerUrlFor(activeChannel)),
      ]);
      setReport(reconRes);
      setLedger(freshLedger);
      const issues =
        reconRes.missing_local_count +
        reconRes.missing_provider_count +
        reconRes.amount_mismatch_count;
      const scopeSuffix = activeChannel ? ` (${channelScopeLabel(activeChannel)})` : "";
      showToast(
        issues === 0
          ? `Đối soát ngày ${runDate}${scopeSuffix} hoàn tất: ${formatInt(reconRes.matched_count)} giao dịch khớp tuyệt đối.`
          : `Đối soát ngày ${runDate}${scopeSuffix} phát hiện ${formatInt(issues)} mục cần kiểm tra.`
      );
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Không thể chạy phiên đối soát lúc này.";
      setRunError(msg);
      showToast(msg);
    } finally {
      setRunning(false);
    }
  };

  const activeScopeLabel = channelScopeLabel(channelFilter);
  const subtitle = isBalanced
    ? `Sổ cái ghi kép (${activeScopeLabel}) cân đối tuyệt đối (Tổng Nợ = Tổng Có = ${formatVnd(totalCredits)}) trên ${formatInt(ledger?.accounts.length ?? 0)} tài khoản.`
    : `Phát hiện chênh lệch sổ cái (${activeScopeLabel}) ${formatSignedVnd(diff)}: chạy đối soát theo ngày để kiểm tra giao dịch lệch.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Đối soát thanh toán & sổ cái</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div
          className={shared.tabList}
          role="tablist"
          aria-label="Lọc sổ cái và đối soát theo kênh bán"
        >
          {CHANNEL_TABS.map((tab) => {
            const active = channelFilter === tab.id;
            return (
              <button
                key={tab.id || "ALL"}
                type="button"
                role="tab"
                aria-selected={active}
                data-channel-tab={tab.id || "ALL"}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => void handleSelectChannel(tab.id)}
              >
                {tab.label}
              </button>
            );
          })}
        </div>
        <div className={shared.tabsMeta}>
          <span className={shared.resultCount}>
            {loadingChannel ? "Đang tải sổ cái..." : `Phạm vi: ${activeScopeLabel}`}
          </span>
        </div>
      </div>

      {channelError && (
        <p className={shared.validationError} style={{ marginTop: 10 }}>
          {channelError}
        </p>
      )}

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp sổ cái ghi kép"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng Ghi có sổ cái</span>
          <span className={shared.kpiValue}>{formatVnd(totalCredits)}</span>
          <span className={shared.kpiCaption}>Đơn vị số nguyên VND</span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng Ghi nợ sổ cái</span>
          <span className={shared.kpiValue}>{formatVnd(totalDebits)}</span>
          <span className={shared.kpiCaption}>Đối ứng bút toán ghi kép</span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chênh lệch Nợ - Có</span>
          <span
            className={`${shared.kpiValue} ${!isBalanced ? shared.kpiValueNegative : shared.kpiValuePositive}`}
          >
            {isBalanced ? "0₫" : formatSignedVnd(diff)}
          </span>
          <span className={shared.kpiCaption}>
            {isBalanced ? "Cân đối tuyệt đối" : "Cần kiểm tra lệch bút toán"}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tài khoản sổ cái</span>
          <span className={shared.kpiValue}>{formatInt(ledger?.accounts.length ?? 0)}</span>
          <span className={shared.kpiCaption}>Escrow, gian hàng & doanh thu sàn</span>
        </div>
      </section>

      <section className={shared.panel} aria-labelledby="run-recon-heading">
        <div className={shared.panelHeader}>
          <h2 id="run-recon-heading" className={shared.panelTitle}>
            Chạy đối soát phiên giao dịch theo ngày
          </h2>
          <span className={shared.panelMeta}>
            Đối chiếu bản ghi thanh toán nội bộ với sao kê cổng thanh toán
          </span>
        </div>

        <form className={shared.toolbar} style={{ marginTop: 8 }} onSubmit={handleRunReconcile}>
          <div className={shared.toolbarLeft}>
            <label htmlFor="recon-date" className={shared.fieldLabel}>
              Ngày đối soát
            </label>
            <input
              id="recon-date"
              type="date"
              className={shared.toolbarSelect}
              value={runDate}
              onChange={(e) => setRunDate(e.target.value)}
              aria-label="Chọn ngày đối soát"
            />
            <Button type="submit" variant="primary" size="sm" shape="app" disabled={running}>
              {running ? "Đang đối soát..." : "Chạy đối soát ngay"}
            </Button>
          </div>
          {report && (
            <span className={shared.resultCount}>
              Mã phiên: {report.run_id.slice(0, 12)} · Khớp {formatInt(report.matched_count)} · Lệch{" "}
              {formatInt(
                report.amount_mismatch_count +
                  report.missing_local_count +
                  report.missing_provider_count
              )}
            </span>
          )}
        </form>

        {runError && (
          <p className={shared.validationError} style={{ marginTop: 8 }}>
            {runError}
          </p>
        )}

        {report && (
          <div style={{ marginTop: 14 }}>
            {report.items.length === 0 ? (
              <p className={shared.emptyRow}>
                Không có giao dịch nào phát sinh trong ngày {runDate} để đối soát.
              </p>
            ) : (
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã giao dịch nội bộ</th>
                      <th>Mã tham chiếu cổng</th>
                      <th>Kênh bán</th>
                      <th className={shared.colNum}>Số tiền nội bộ</th>
                      <th className={shared.colNum}>Số tiền sao kê</th>
                      <th>Trạng thái</th>
                      <th>Ghi chú</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.items.map((item, idx) => {
                      const meta = getReconItemStatusMeta(item.status);
                      const channelMeta = getSalesChannelMeta(item.sales_channel);
                      return (
                        <tr key={`${item.payment_id}-${item.provider_tx_id}-${idx}`}>
                          <td className={shared.cellPrimary}>{item.payment_id || EMPTY_DASH}</td>
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
            )}
          </div>
        )}
      </section>

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
        <p className={shared.emptyRow}>Chưa có tài khoản sổ cái nào khớp với bộ lọc hiện tại.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Mã tài khoản sổ cái</th>
                  <th>Nhóm tài khoản</th>
                  <th className={shared.colNum}>Tổng Ghi nợ</th>
                  <th className={shared.colNum}>Tổng Ghi có</th>
                  <th className={shared.colNum}>Số dư hiện tại</th>
                </tr>
              </thead>
              <tbody>
                {filteredAccounts.map((acc) => (
                  <tr key={acc.account_code}>
                    <td className={shared.cellPrimary}>{acc.account_code}</td>
                    <td>{getAccountTypeLabelVi(acc.account_type)}</td>
                    <td className={shared.colNum}>{formatVnd(acc.debit_total_vnd)}</td>
                    <td className={shared.colNum}>{formatVnd(acc.credit_total_vnd)}</td>
                    <td className={shared.colNum}>
                      <strong>{formatVnd(acc.balance_vnd)}</strong>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filteredAccounts.map((acc) => (
              <div key={acc.account_code} className={shared.mobileItem}>
                <div className={shared.mobileLine1}>
                  <span className={shared.mobileIdentity}>{acc.account_code}</span>
                  <span className={shared.mobileMetric}>{formatVnd(acc.balance_vnd)}</span>
                </div>
                <div className={shared.mobileLine2}>
                  <span className={shared.mobileMetaText}>
                    {getAccountTypeLabelVi(acc.account_type)} · Nợ {formatVnd(acc.debit_total_vnd)}{" "}
                    · Có {formatVnd(acc.credit_total_vnd)}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

export function AdminReconciliationClient(props: AdminReconciliationClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Đối soát thanh toán" />}>
      <AdminStateGate
        title="Đối soát thanh toán"
        subtitle="Đối chiếu số thu giữa các cổng thanh toán với sổ cái ghi kép nội bộ."
        emptyTitle="Chưa có dữ liệu đối soát"
        emptyMessage="Sổ cái sẽ hiển thị số liệu ngay khi hệ thống phát sinh giao dịch thanh toán."
        errorTitle="Không thể tải sổ đối soát thanh toán"
        errorMessage="Kết nối tới dịch vụ sổ cái thanh toán tạm gián đoạn. Vui lòng thử tải lại."
        loadingRows={5}
      >
        {({ initialQuery }) => (
          <ReconciliationContent
            initialLedger={props.initialLedger}
            loadError={props.loadError}
            initialQuery={initialQuery}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
