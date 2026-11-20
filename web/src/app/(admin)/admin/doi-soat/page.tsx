"use client";

import React, { Suspense, useMemo, useState } from "react";
import type { PaymentMethod, PaymentReconciliationRow, ReconciliationStatus } from "@/data";
import { computeReconciliationSummary } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconSearch } from "@/components";
import { formatDateVi, formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { CHANNEL_META, RECON_STATUS_META } from "@/features/admin/adminMeta";
import {
  appendAdminAuditLog,
  setAdminReconNote,
  useAdminReconNotes,
} from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ReconTab = "all" | ReconciliationStatus;
type ChannelFilter = "all" | PaymentMethod;
type ReconSort = "flagged" | "newest" | "gross";

function ReconciliationContent({
  initialQuery,
  initialSelected,
}: {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const { reconciliationRows, resolveReconciliationBatch, showToast } = useStore();
  const extraNotes = useAdminReconNotes();

  const [activeTab, setActiveTab] = useState<ReconTab>("all");
  const [channelFilter, setChannelFilter] = useState<ChannelFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [sortBy, setSortBy] = useState<ReconSort>("flagged");
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});

  const summary = useMemo(
    () => computeReconciliationSummary(reconciliationRows),
    [reconciliationRows]
  );

  const counts = useMemo(() => {
    const map: Record<ReconTab, number> = {
      all: reconciliationRows.length,
      lech_can_xu_ly: 0,
      khop: 0,
      dang_doi_soat: 0,
    };
    for (const row of reconciliationRows) {
      map[row.status] += 1;
    }
    return map;
  }, [reconciliationRows]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = reconciliationRows.filter((row) => {
      if (activeTab !== "all" && row.status !== activeTab) return false;
      if (channelFilter !== "all" && row.channel !== channelFilter) return false;
      if (!q) return true;
      const effectiveNote = extraNotes[row.id] ?? row.note;
      return (
        row.batchCode.toLowerCase().includes(q) ||
        row.date.toLowerCase().includes(q) ||
        row.channelLabel.toLowerCase().includes(q) ||
        effectiveNote.toLowerCase().includes(q)
      );
    });

    return [...list].sort((a, b) => {
      if (sortBy === "gross") return b.grossCollected - a.grossCollected;
      if (sortBy === "newest") return b.date.localeCompare(a.date);
      const aFlag = a.status === "lech_can_xu_ly" ? 0 : a.status === "dang_doi_soat" ? 1 : 2;
      const bFlag = b.status === "lech_can_xu_ly" ? 0 : b.status === "dang_doi_soat" ? 1 : 2;
      if (aFlag !== bFlag) return aFlag - bFlag;
      return b.date.localeCompare(a.date);
    });
  }, [reconciliationRows, activeTab, channelFilter, query, sortBy, extraNotes]);

  const handleToggleSelect = (row: PaymentReconciliationRow) => {
    setSelectedId((prev) => (prev === row.id ? null : row.id));
    setNoteDrafts((prev) => {
      if (prev[row.id] != null) return prev;
      return { ...prev, [row.id]: extraNotes[row.id] ?? row.note };
    });
  };

  const handleSaveNote = (row: PaymentReconciliationRow) => {
    const nextNote = (noteDrafts[row.id] ?? "").trim();
    if (!nextNote) return;
    setAdminReconNote(row.id, nextNote);
    showToast(`Đã lưu ghi chú đối soát cho phiên ${row.batchCode}.`);
    appendAdminAuditLog({
      role: "Đối soát tài chính",
      action: "Cập nhật ghi chú đối soát phiên thanh toán",
      module: "Đối soát",
      target: row.batchCode,
      changeSummary: nextNote,
    });
  };

  const handleMarkResolved = (row: PaymentReconciliationRow) => {
    const draft = (noteDrafts[row.id] ?? "").trim();
    const finalNote =
      draft && draft !== row.note
        ? draft
        : `Đã đối chiếu sao kê ${CHANNEL_META[row.channel]}, xác nhận khớp chênh lệch ${formatVnd(row.discrepancy)} và khóa sổ cái.`;
    resolveReconciliationBatch(row.id, finalNote);
    setAdminReconNote(row.id, finalNote);
    appendAdminAuditLog({
      role: "Đối soát tài chính",
      action: "Xác nhận khớp phiên đối soát thanh toán",
      module: "Đối soát",
      target: row.batchCode,
      changeSummary: `Khóa chênh lệch ${formatVnd(row.discrepancy)} về 0₫. ${finalNote}`,
    });
  };

  const renderLedgerDetail = (row: PaymentReconciliationRow) => {
    const settledVnd = row.grossCollected - row.discrepancy;
    const effectiveNote = extraNotes[row.id] ?? row.note;
    const isMatched = row.status === "khop" && row.discrepancy === 0;

    return (
      <div className={shared.detailWell}>
        <div className={styles.ledgerBreakdown}>
          <div className={styles.ledgerLine}>
            <span className={shared.detailKey}>Tổng thu kỳ vọng</span>
            <span className={`${shared.detailVal} num`}>{formatVnd(row.grossCollected)}</span>
          </div>
          <div className={styles.ledgerLine}>
            <span className={shared.detailKey}>Đã về sổ cái ghi kép</span>
            <span className={`${shared.detailVal} num`}>{formatVnd(settledVnd)}</span>
          </div>
          <div className={styles.ledgerLine}>
            <span className={shared.detailKey}>Chênh lệch sao kê</span>
            <span
              className={`${shared.detailVal} num ${row.discrepancy > 0 ? shared.numNegative : ""}`}
            >
              {row.discrepancy > 0 ? formatSignedVnd(-row.discrepancy) : "0₫"}
            </span>
          </div>
          <div className={styles.ledgerLine}>
            <span className={shared.detailKey}>Phí cổng thanh toán</span>
            <span className={`${shared.detailValPlain} num`}>
              {formatSignedVnd(-row.gatewayFee)}
            </span>
          </div>
          <div className={styles.ledgerLine}>
            <span className={shared.detailKey}>Phí hoa hồng sàn (5,0%)</span>
            <span className={`${shared.detailValPlain} num`}>
              {formatSignedVnd(-row.platformCommission)}
            </span>
          </div>
          <div className={styles.ledgerLine}>
            <span className={shared.detailKey}>Hoàn tiền / Thực trả gian hàng</span>
            <span className={`${shared.detailVal} num`}>
              {row.refunded > 0 ? `${formatSignedVnd(-row.refunded)} · ` : "0₫ · "}
              {formatVnd(row.sellerPayable)}
            </span>
          </div>
        </div>

        <div className={shared.detailField}>
          <span className={shared.detailKey}>Ghi chú đối soát hiện tại</span>
          <span className={shared.detailValPlain}>{effectiveNote}</span>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`rc-note-${row.id}`} className={shared.fieldLabel}>
              Ghi chú đối soát & mã chứng từ sao kê
            </label>
            <input
              id={`rc-note-${row.id}`}
              type="text"
              className={shared.textInput}
              value={noteDrafts[row.id] ?? effectiveNote}
              onChange={(e) => setNoteDrafts((prev) => ({ ...prev, [row.id]: e.target.value }))}
              placeholder="Nhập mã vận đơn, số chứng từ ngân hàng hoặc kết quả đối chiếu..."
            />
          </div>

          <div className={shared.detailButtons}>
            <Button variant="outline" size="sm" shape="app" onClick={() => handleSaveNote(row)}>
              Lưu ghi chú đối soát
            </Button>
            {!isMatched ? (
              <Button
                variant="primary"
                size="sm"
                shape="app"
                onClick={() => handleMarkResolved(row)}
              >
                Đánh dấu đã xử lý
              </Button>
            ) : (
              <span className={styles.noteSavedBadge}>Phiên đã khớp sổ cái</span>
            )}
          </div>
        </div>
      </div>
    );
  };

  const tabs: Array<{ id: ReconTab; label: string }> = [
    { id: "all", label: "Tất cả phiên" },
    { id: "lech_can_xu_ly", label: "Có chênh lệch" },
    { id: "khop", label: "Đã khớp" },
    { id: "dang_doi_soat", label: "Đang đối chiếu" },
  ];

  const settledTotalVnd = summary.totalGross - summary.totalDiscrepancy;
  const subtitle =
    summary.flaggedBatches > 0
      ? `Đã khớp ${summary.matchedBatches}/${reconciliationRows.length} phiên (${formatVnd(settledTotalVnd)} về sổ cái); còn ${summary.flaggedBatches} phiên COD lệch ${formatSignedVnd(-summary.totalDiscrepancy)}.`
      : `Toàn bộ ${reconciliationRows.length}/${reconciliationRows.length} phiên đã khớp sổ cái ghi kép với tổng thu ${formatVnd(summary.totalGross)}, sai lệch 0₫.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Đối soát thanh toán</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp đối soát thanh toán"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng kỳ vọng</span>
          <span className={shared.kpiValue}>{formatVnd(summary.totalGross)}</span>
          <span className={shared.kpiCaption}>
            {formatInt(summary.totalTxCount)} giao dịch · {reconciliationRows.length} phiên
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Tổng đã về sổ cái</span>
          <span className={shared.kpiValue}>{formatVnd(settledTotalVnd)}</span>
          <span className={shared.kpiCaption}>
            Thực trả shop {formatVnd(summary.totalSellerPayable)}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chênh lệch ròng</span>
          <span
            className={`${shared.kpiValue} ${
              summary.totalDiscrepancy > 0 ? shared.kpiValueNegative : ""
            }`}
          >
            {summary.totalDiscrepancy > 0 ? formatSignedVnd(-summary.totalDiscrepancy) : "0₫"}
          </span>
          <span className={shared.kpiCaption}>
            {summary.totalDiscrepancy > 0
              ? "Đang tạm giữ giải ngân vận đơn lệch"
              : "Khớp tuyệt đối với sao kê"}
          </span>
        </div>

        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Phiên cần xử lý</span>
          <span className={shared.kpiValue}>{formatInt(summary.flaggedBatches)}</span>
          <span className={shared.kpiCaption}>
            {formatInt(summary.matchedBatches)} phiên đã khớp hoàn tất
          </span>
        </div>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái phiên đối soát">
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
              placeholder="Tìm mã lô DS-, vận đơn, ghi chú..."
              aria-label="Tìm kiếm phiên đối soát"
            />
          </div>

          <select
            className={shared.toolbarSelect}
            value={channelFilter}
            onChange={(e) => setChannelFilter(e.target.value as ChannelFilter)}
            aria-label="Lọc theo cổng thanh toán"
          >
            <option value="all">Tất cả cổng</option>
            <option value="cod">COD thu hộ</option>
            <option value="vietqr">VietQR</option>
            <option value="vi_sandbox">Ví NexusPay</option>
          </select>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} phiên</span>
          <select
            className={shared.toolbarSelect}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as ReconSort)}
            aria-label="Sắp xếp phiên đối soát"
          >
            <option value="flagged">Ưu tiên phiên có chênh lệch</option>
            <option value="newest">Ngày đối soát mới nhất</option>
            <option value="gross">Tổng thu cao nhất</option>
          </select>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không có phiên đối soát nào khớp với bộ lọc hiện tại.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Mã phiên & ngày</th>
                  <th className={styles.tabletHideCol}>Cổng thanh toán</th>
                  <th className={shared.colNum}>Kỳ vọng</th>
                  <th className={shared.colNum}>Đã về sổ cái</th>
                  <th className={shared.colNum}>Chênh lệch</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((row) => {
                  const meta = RECON_STATUS_META[row.status];
                  const settledVnd = row.grossCollected - row.discrepancy;
                  const isSelected = selectedId === row.id;
                  return (
                    <React.Fragment key={row.id}>
                      <tr
                        className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                        onClick={() => handleToggleSelect(row)}
                      >
                        <td>
                          <div className={shared.cellPrimary}>{row.batchCode}</div>
                          <div className={`${shared.cellSub} ${styles.batchSub}`}>
                            {formatDateVi(row.date)} · {formatInt(row.txCount)} gd
                            <span className={styles.tabletInlineChannel}>
                              {" "}
                              · {CHANNEL_META[row.channel]}
                            </span>
                          </div>
                        </td>
                        <td className={styles.tabletHideCol}>{CHANNEL_META[row.channel]}</td>
                        <td className={shared.colNum}>{formatVnd(row.grossCollected)}</td>
                        <td className={shared.colNum}>{formatVnd(settledVnd)}</td>
                        <td
                          className={`${shared.colNum} ${
                            row.discrepancy > 0 ? shared.numNegative : shared.numMuted
                          }`}
                        >
                          {row.discrepancy > 0 ? formatSignedVnd(-row.discrepancy) : "0₫"}
                        </td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                        <td className={shared.colAction}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={(e) => {
                              e.stopPropagation();
                              handleToggleSelect(row);
                            }}
                          >
                            {isSelected ? "Đóng" : row.discrepancy > 0 ? "Xử lý" : "Ghi chú"}
                          </button>
                        </td>
                      </tr>
                      {isSelected && (
                        <tr className={shared.detailRow}>
                          <td colSpan={7}>{renderLedgerDetail(row)}</td>
                        </tr>
                      )}
                    </React.Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filtered.map((row) => {
              const meta = RECON_STATUS_META[row.status];
              const isSelected = selectedId === row.id;
              return (
                <div key={row.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => handleToggleSelect(row)}
                    aria-expanded={isSelected}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>
                        {row.batchCode} · {CHANNEL_META[row.channel]}
                      </span>
                      <span
                        className={`${shared.mobileMetric} ${
                          row.discrepancy > 0 ? shared.numNegative : ""
                        }`}
                      >
                        {row.discrepancy > 0
                          ? formatSignedVnd(-row.discrepancy)
                          : formatVnd(row.grossCollected)}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {formatDateVi(row.date)} · {formatInt(row.txCount)} gd · Kỳ vọng{" "}
                        {formatVnd(row.grossCollected)}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                  </button>
                  {isSelected && (
                    <div className={shared.mobileDetailWrap}>{renderLedgerDetail(row)}</div>
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

function ReconciliationRoute() {
  return (
    <AdminStateGate
      title="Đối soát thanh toán"
      subtitle="Đối chiếu số thu kỳ vọng giữa các phương thức thanh toán (COD, VietQR, Ví NexusPay) với sổ cái ghi kép nội bộ."
      emptyTitle="Chưa có phiên đối soát trong kỳ chọn"
      emptyMessage="Các lô thanh toán cuối ngày sẽ xuất hiện tại đây sau khi đối tác hoàn tất chốt sao kê."
      errorTitle="Không thể tải sổ đối soát thanh toán"
      errorMessage="Kết nối tới dịch vụ sổ cái thanh toán tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <ReconciliationContent initialQuery={initialQuery} initialSelected={initialSelected} />
      )}
    </AdminStateGate>
  );
}

export default function AdminReconciliationPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Đối soát thanh toán" />}>
      <ReconciliationRoute />
    </Suspense>
  );
}
