"use client";

import React, { Suspense, useMemo, useState } from "react";
import type { AdminDispute, DisputeStatus, VulnerablePriorityGroup } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconSearch } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import {
  DISPUTE_STATUS_META,
  DISPUTE_TIER_LABELS,
  EMPTY_DASH,
  formatShortTimeDateVi,
  VULNERABLE_GROUP_LABELS,
} from "@/features/admin/adminMeta";
import {
  appendAdminAuditLog,
  confirmDisputeAckSla,
  updateDisputePriorityGroup,
  useAdminDisputeOverrides,
} from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type DisputeTab = "all" | DisputeStatus;
type DisputeSort = "priority_sla" | "amount" | "newest";
type DisputeScopeFilter = "all" | "vulnerable" | "cap_2_tu_gian_hang" | "cap_2_san_thu_ly";

function DisputesContent({
  initialQuery,
  initialSelected,
}: {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const { disputes, updateDisputeStatus, showToast } = useStore();
  const overrides = useAdminDisputeOverrides();

  const [activeTab, setActiveTab] = useState<DisputeTab>("all");
  const [scopeFilter, setScopeFilter] = useState<DisputeScopeFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [sortBy, setSortBy] = useState<DisputeSort>("priority_sla");
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected ?? "dsp-1");
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(initialSelected);
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});
  const [partialDrafts, setPartialDrafts] = useState<Record<string, number>>({});
  const [errorByCase, setErrorByCase] = useState<Record<string, string>>({});

  const enrichedDisputes = useMemo(() => {
    return disputes.map((d) => {
      const ov = overrides[d.id];
      const vulnerableGroup =
        ov && ov.vulnerableGroup !== undefined
          ? (ov.vulnerableGroup ?? undefined)
          : d.vulnerableGroup;
      const ackConfirmedAt = ov?.ackConfirmedAt ?? d.ackConfirmedAt;
      const ackSlaHoursLeft =
        ov?.ackSlaHoursLeft !== undefined ? ov.ackSlaHoursLeft : (d.ackSlaHoursLeft ?? 0);
      return {
        ...d,
        tier: d.tier ?? ("cap_2_tu_gian_hang" as const),
        vulnerableGroup,
        ackConfirmedAt,
        ackSlaHoursLeft,
        resolveWorkingDaysLeft: d.resolveWorkingDaysLeft ?? (d.status === "da_xu_ly" ? 0 : 4),
        resolveWorkingDaysTotal: d.resolveWorkingDaysTotal ?? 5,
      };
    });
  }, [disputes, overrides]);

  const counts = useMemo(() => {
    const map: Record<DisputeTab, number> = {
      all: enrichedDisputes.length,
      moi_tiep_nhan: 0,
      dang_xac_minh: 0,
      cho_phan_hoi: 0,
      da_xu_ly: 0,
    };
    for (const d of enrichedDisputes) {
      map[d.status] += 1;
    }
    return map;
  }, [enrichedDisputes]);

  const openStats = useMemo(() => {
    const open = enrichedDisputes.filter((d) => d.status !== "da_xu_ly");
    const vulnerableOpen = open.filter((d) => Boolean(d.vulnerableGroup)).length;
    const escalatedFromShopOpen = open.filter((d) => d.tier === "cap_2_tu_gian_hang").length;
    const ackPendingOpen = open.filter((d) => !d.ackConfirmedAt && d.ackSlaHoursLeft > 0).length;
    return {
      count: open.length,
      heldVnd: open.reduce((s, d) => s + d.amount, 0),
      vulnerableOpen,
      escalatedFromShopOpen,
      ackPendingOpen,
    };
  }, [enrichedDisputes]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = enrichedDisputes.filter((d) => {
      if (activeTab !== "all" && d.status !== activeTab) return false;
      if (scopeFilter === "vulnerable" && !d.vulnerableGroup) return false;
      if (
        (scopeFilter === "cap_2_tu_gian_hang" || scopeFilter === "cap_2_san_thu_ly") &&
        d.tier !== scopeFilter
      ) {
        return false;
      }
      if (!q) return true;
      return (
        d.caseCode.toLowerCase().includes(q) ||
        d.orderCode.toLowerCase().includes(q) ||
        d.buyerName.toLowerCase().includes(q) ||
        d.shopName.toLowerCase().includes(q) ||
        d.reason.toLowerCase().includes(q)
      );
    });

    return [...list].sort((a, b) => {
      if (sortBy === "amount") return b.amount - a.amount;
      if (sortBy === "newest") return b.openedAt.localeCompare(a.openedAt);
      const aDone = a.status === "da_xu_ly" ? 1 : 0;
      const bDone = b.status === "da_xu_ly" ? 1 : 0;
      if (aDone !== bDone) return aDone - bDone;
      const aVul = a.vulnerableGroup ? 0 : 1;
      const bVul = b.vulnerableGroup ? 0 : 1;
      if (aVul !== bVul) return aVul - bVul;
      return a.slaDeadline.localeCompare(b.slaDeadline);
    });
  }, [enrichedDisputes, activeTab, scopeFilter, query, sortBy]);

  const activeDesktopDispute = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((d) => d.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  const ensurePartialDraft = (dispute: AdminDispute) => {
    setPartialDrafts((prev) => {
      if (prev[dispute.id] != null) return prev;
      const half = Math.max(1000, Math.round(dispute.amount / 2000) * 1000);
      return { ...prev, [dispute.id]: half };
    });
  };

  const handleSelectDesktop = (dispute: AdminDispute) => {
    setSelectedId(dispute.id);
    ensurePartialDraft(dispute);
  };

  const handleToggleMobile = (dispute: AdminDispute) => {
    setMobileOpenId((prev) => (prev === dispute.id ? null : dispute.id));
    ensurePartialDraft(dispute);
  };

  const handleConfirmAck24h = (dispute: AdminDispute) => {
    confirmDisputeAckSla(dispute.id);
    showToast(`Đã xác nhận tiếp nhận hồ sơ ${dispute.caseCode} trong hạn SLA 24h.`);
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: "Xác nhận tiếp nhận khiếu nại trong SLA 24h",
      module: "Khiếu nại",
      target: dispute.caseCode,
      changeSummary: `Đơn ${dispute.orderCode} (${dispute.buyerName}) đã xác nhận tiếp nhận, chuyển sang chu kỳ xử lý 5–7 ngày làm việc.`,
    });
  };

  const handlePriorityChange = (dispute: AdminDispute, rawValue: string) => {
    const nextGroup = rawValue ? (rawValue as VulnerablePriorityGroup) : null;
    updateDisputePriorityGroup(dispute.id, nextGroup);
    const label = nextGroup ? VULNERABLE_GROUP_LABELS[nextGroup] : "Bỏ nhãn ưu tiên";
    showToast(`Cập nhật nhãn ưu tiên nhóm yếu thế cho ${dispute.caseCode}: ${label}.`);
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: "Cập nhật nhãn Ưu tiên nhóm yếu thế",
      module: "Khiếu nại",
      target: dispute.caseCode,
      changeSummary: `Đơn ${dispute.orderCode}: ${label}.`,
    });
  };

  const handleResolve = (
    dispute: AdminDispute,
    mode: "refund_buyer" | "release_seller" | "partial_refund" | "verify"
  ) => {
    const customNote = (noteDrafts[dispute.id] ?? "").trim();

    if (mode === "verify") {
      const nextNote =
        customNote ||
        `${dispute.evidenceNote} Đã xác nhận tiếp nhận và chuyển bước đối chiếu chứng từ vận đơn.`;
      confirmDisputeAckSla(dispute.id);
      updateDisputeStatus(dispute.id, "dang_xac_minh", nextNote);
      appendAdminAuditLog({
        role: "Vận hành sàn",
        action: "Chuyển khiếu nại sang Đang xác minh",
        module: "Khiếu nại",
        target: dispute.caseCode,
        changeSummary: `Đơn ${dispute.orderCode} (${formatVnd(dispute.amount)}): ${nextNote}`,
      });
      return;
    }

    if (mode === "partial_refund") {
      const partialAmount = Math.trunc(partialDrafts[dispute.id] ?? 0);
      if (partialAmount <= 0 || partialAmount >= dispute.amount) {
        setErrorByCase((prev) => ({
          ...prev,
          [dispute.id]: `Số tiền hoàn một phần phải lớn hơn 0₫ và nhỏ hơn ${formatVnd(dispute.amount)}.`,
        }));
        return;
      }
      setErrorByCase((prev) => ({ ...prev, [dispute.id]: "" }));
      const sellerShare = dispute.amount - partialAmount;
      const summary =
        customNote ||
        `Hoàn ${formatVnd(partialAmount)} về Ví NexusPay người mua ${dispute.buyerName} và giải ngân ${formatVnd(sellerShare)} cho ${dispute.shopName}.`;
      confirmDisputeAckSla(dispute.id);
      updateDisputeStatus(dispute.id, "da_xu_ly", summary);
      appendAdminAuditLog({
        role: "Vận hành sàn",
        action: "Phân xử hoàn tiền một phần",
        module: "Khiếu nại",
        target: dispute.caseCode,
        changeSummary: summary,
      });
      return;
    }

    setErrorByCase((prev) => ({ ...prev, [dispute.id]: "" }));
    confirmDisputeAckSla(dispute.id);

    if (mode === "refund_buyer") {
      const summary =
        customNote ||
        `Đã hoàn toàn bộ ${formatVnd(dispute.amount)} về Ví NexusPay người mua ${dispute.buyerName} và ghi nợ gian hàng ${dispute.shopName}.`;
      updateDisputeStatus(dispute.id, "da_xu_ly", summary);
      appendAdminAuditLog({
        role: "Vận hành sàn",
        action: "Hoàn tiền 100% cho người mua",
        module: "Khiếu nại",
        target: dispute.caseCode,
        changeSummary: summary,
      });
      return;
    }

    const summary =
      customNote ||
      `Đã giải ngân toàn bộ ${formatVnd(dispute.amount)} cho gian hàng ${dispute.shopName} sau khi xác minh chứng từ đóng gói.`;
    updateDisputeStatus(dispute.id, "da_xu_ly", summary);
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: "Giải ngân khiếu nại cho gian hàng",
      module: "Khiếu nại",
      target: dispute.caseCode,
      changeSummary: summary,
    });
  };

  const renderDetailPanel = (dispute: (typeof enrichedDisputes)[number], showHeading = false) => {
    const isResolved = dispute.status === "da_xu_ly";
    const partialVal =
      partialDrafts[dispute.id] ?? Math.max(1000, Math.round(dispute.amount / 2000) * 1000);
    const errorMsg = errorByCase[dispute.id];
    const meta = DISPUTE_STATUS_META[dispute.status];
    const ackPending = !isResolved && !dispute.ackConfirmedAt && dispute.ackSlaHoursLeft > 0;

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Hồ sơ phân xử: {dispute.caseCode} · {dispute.orderCode}
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}
        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Cấp thụ lý & nguồn chuyển</span>
            <span className={shared.detailVal}>{DISPUTE_TIER_LABELS[dispute.tier]}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Người mua / Gian hàng</span>
            <span className={shared.detailVal}>
              {dispute.buyerName} / {dispute.shopName}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Giá trị tranh chấp</span>
            <span className={`${shared.detailVal} num`}>{formatVnd(dispute.amount)}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>SLA xác nhận tiếp nhận (24h)</span>
            <span className={ackPending ? styles.slaAckUrgent : styles.slaAckDone}>
              {ackPending
                ? `Chưa xác nhận · Còn ${dispute.ackSlaHoursLeft}h / 24h`
                : dispute.ackConfirmedAt
                  ? `Đã xác nhận lúc ${formatShortTimeDateVi(dispute.ackConfirmedAt)}`
                  : "Đã xác nhận trong hạn 24h"}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>SLA xử lý (5–7 ngày làm việc)</span>
            <span className={`${shared.detailValPlain} num`}>
              {isResolved
                ? "Đã đóng đúng hạn"
                : `Còn ${dispute.resolveWorkingDaysLeft}/${dispute.resolveWorkingDaysTotal} ngày LV · Hạn ${formatDateTimeVi(dispute.slaDeadline)}`}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Ưu tiên nhóm yếu thế</span>
            <div className={styles.prioritySelectRow}>
              <select
                className={shared.toolbarSelect}
                value={dispute.vulnerableGroup ?? ""}
                onChange={(e) => handlePriorityChange(dispute, e.target.value)}
                aria-label="Chọn nhãn ưu tiên nhóm yếu thế"
              >
                <option value="">Tiêu chuẩn</option>
                <option value="nguoi_cao_tuoi">Ưu tiên · Người cao tuổi</option>
                <option value="phu_nu_mang_thai">Ưu tiên · Phụ nữ mang thai</option>
                <option value="nguoi_khuyet_tat">Ưu tiên · Người khuyết tật</option>
              </select>
            </div>
          </div>
          {dispute.escalatedReason && (
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <span className={shared.detailKey}>Lý do chuyển tranh chấp cấp 2</span>
              <span className={shared.detailValPlain}>{dispute.escalatedReason}</span>
            </div>
          )}
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Yêu cầu từ người mua</span>
            <span className={shared.detailValPlain}>{dispute.reason}</span>
          </div>
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Phản hồi gian hàng & tóm tắt bằng chứng</span>
            <span className={shared.detailValPlain}>{dispute.evidenceNote}</span>
          </div>
        </div>

        {isResolved ? (
          <div className={shared.detailActionBar}>
            <span className={styles.resolvedNote}>
              Hồ sơ đã hoàn tất phân xử và đồng bộ bút toán trên sổ cái thanh toán.
            </span>
          </div>
        ) : (
          <div className={shared.detailActionBar}>
            <div className={shared.detailNoteGroup}>
              <label htmlFor={`dsp-note-${dispute.id}`} className={shared.fieldLabel}>
                Kết luận phân xử (tùy chọn)
              </label>
              <input
                id={`dsp-note-${dispute.id}`}
                type="text"
                className={shared.textInput}
                value={noteDrafts[dispute.id] ?? ""}
                onChange={(e) =>
                  setNoteDrafts((prev) => ({ ...prev, [dispute.id]: e.target.value }))
                }
                placeholder="Ghi chú căn cứ đối chiếu ảnh, cân nặng vận đơn hoặc thỏa thuận..."
              />
              {errorMsg && <span className={styles.validationError}>{errorMsg}</span>}
            </div>

            <div className={styles.partialField}>
              <label htmlFor={`dsp-partial-${dispute.id}`} className={shared.fieldLabel}>
                Mức hoàn một phần (VND)
              </label>
              <input
                id={`dsp-partial-${dispute.id}`}
                type="number"
                min={1000}
                max={Math.max(1000, dispute.amount - 1000)}
                step={1000}
                className={`${shared.textInput} num`}
                value={partialVal}
                onChange={(e) => {
                  setPartialDrafts((prev) => ({
                    ...prev,
                    [dispute.id]: Number(e.target.value) || 0,
                  }));
                  if (errorMsg) {
                    setErrorByCase((prev) => ({ ...prev, [dispute.id]: "" }));
                  }
                }}
              />
            </div>

            <div className={shared.detailButtons}>
              {ackPending && (
                <Button
                  variant="ghost"
                  size="sm"
                  shape="app"
                  onClick={() => handleConfirmAck24h(dispute)}
                >
                  Xác nhận SLA 24h
                </Button>
              )}
              {dispute.status === "moi_tiep_nhan" && (
                <Button
                  variant="ghost"
                  size="sm"
                  shape="app"
                  onClick={() => handleResolve(dispute, "verify")}
                >
                  Đang xác minh
                </Button>
              )}
              <Button
                variant="primary"
                size="sm"
                shape="app"
                onClick={() => handleResolve(dispute, "refund_buyer")}
              >
                Hoàn tiền người mua
              </Button>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => handleResolve(dispute, "release_seller")}
              >
                Giải ngân người bán
              </Button>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => handleResolve(dispute, "partial_refund")}
              >
                Hoàn tiền một phần
              </Button>
            </div>
          </div>
        )}
      </div>
    );
  };

  const tabs: Array<{ id: DisputeTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "moi_tiep_nhan", label: "Chờ phân xử" },
    { id: "dang_xac_minh", label: "Đang xác minh" },
    { id: "cho_phan_hoi", label: "Chờ phản hồi" },
    { id: "da_xu_ly", label: "Đã giải quyết" },
  ];

  const subtitle =
    openStats.count > 0
      ? `${openStats.count} khiếu nại mở (tạm giữ ${formatVnd(openStats.heldVnd)}), gồm ${openStats.vulnerableOpen} hồ sơ ưu tiên nhóm yếu thế và ${openStats.escalatedFromShopOpen} vụ cấp 2 từ gian hàng chuyển lên.`
      : `Tất cả ${enrichedDisputes.length} hồ sơ khiếu nại đã được giải quyết xong, không còn tiền tạm giữ.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Khiếu nại & tranh chấp cấp 2</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp SLA và phân loại tranh chấp"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Khiếu nại đang mở</span>
          <span className={shared.kpiValue}>{formatInt(openStats.count)}</span>
          <span className={shared.kpiCaption}>Tạm giữ {formatVnd(openStats.heldVnd)}</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Ưu tiên nhóm yếu thế</span>
          <span
            className={`${shared.kpiValue} ${openStats.vulnerableOpen > 0 ? shared.kpiValueNegative : ""}`}
          >
            {formatInt(openStats.vulnerableOpen)}
          </span>
          <span className={shared.kpiCaption}>Xếp đầu hàng đợi phân xử</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Cấp 2 từ gian hàng</span>
          <span className={shared.kpiValue}>{formatInt(openStats.escalatedFromShopOpen)}</span>
          <span className={shared.kpiCaption}>
            {formatInt(openStats.count - openStats.escalatedFromShopOpen)} vụ sàn thụ lý trực tiếp
          </span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chờ xác nhận SLA 24h</span>
          <span
            className={`${shared.kpiValue} ${openStats.ackPendingOpen > 0 ? shared.kpiValueNegative : ""}`}
          >
            {formatInt(openStats.ackPendingOpen)}
          </span>
          <span className={shared.kpiCaption}>Hạn xử lý tổng 5–7 ngày làm việc</span>
        </div>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái khiếu nại">
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
              placeholder="Tìm mã KN-, mã đơn NM-, người mua, shop..."
              aria-label="Tìm kiếm hồ sơ khiếu nại"
            />
          </div>

          <select
            className={shared.toolbarSelect}
            value={scopeFilter}
            onChange={(e) => setScopeFilter(e.target.value as DisputeScopeFilter)}
            aria-label="Lọc cấp xử lý và nhóm ưu tiên"
          >
            <option value="all">Tất cả cấp xử lý</option>
            <option value="vulnerable">Chỉ ưu tiên nhóm yếu thế</option>
            <option value="cap_2_tu_gian_hang">Cấp 2 · Từ gian hàng chuyển lên</option>
            <option value="cap_2_san_thu_ly">Cấp 2 · Sàn thụ lý trực tiếp</option>
          </select>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} khiếu nại</span>
          <select
            className={shared.toolbarSelect}
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as DisputeSort)}
            aria-label="Sắp xếp danh sách khiếu nại"
          >
            <option value="priority_sla">Ưu tiên yếu thế & SLA gần nhất</option>
            <option value="amount">Giá trị cao nhất</option>
            <option value="newest">Mới mở gần nhất</option>
          </select>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không có hồ sơ khiếu nại nào khớp với bộ lọc hiện tại.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Hồ sơ, cấp tranh chấp & nội dung</th>
                  <th>Người mua & gian hàng</th>
                  <th className={shared.colNum}>Giá trị</th>
                  <th className={`${shared.colNum} ${styles.tabletHideCol}`}>
                    SLA 24h / 5–7 ngày LV
                  </th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => {
                  const meta = DISPUTE_STATUS_META[d.status];
                  const isSelected = activeDesktopDispute?.id === d.id;
                  const ackPending =
                    d.status !== "da_xu_ly" && !d.ackConfirmedAt && d.ackSlaHoursLeft > 0;
                  return (
                    <tr
                      key={d.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => handleSelectDesktop(d)}
                    >
                      <td>
                        <div className={shared.cellPrimary}>
                          {d.caseCode} · {d.orderCode}
                        </div>
                        <div className={`${shared.cellSub} ${styles.reasonCell}`}>{d.reason}</div>
                        <div className={styles.metaTagLine}>
                          <span className={shared.tierText}>{DISPUTE_TIER_LABELS[d.tier]}</span>
                          {d.vulnerableGroup && (
                            <span className={shared.priorityText}>
                              · Ưu tiên: {VULNERABLE_GROUP_LABELS[d.vulnerableGroup]}
                            </span>
                          )}
                        </div>
                      </td>
                      <td>
                        <div>{d.buyerName}</div>
                        <div className={shared.cellSub}>{d.shopName}</div>
                      </td>
                      <td className={shared.colNum}>
                        <div>{formatVnd(d.amount)}</div>
                        <div className={styles.tabletSubSla}>
                          {d.status === "da_xu_ly"
                            ? EMPTY_DASH
                            : ackPending
                              ? `XN còn ${d.ackSlaHoursLeft}h`
                              : `Còn ${d.resolveWorkingDaysLeft}/${d.resolveWorkingDaysTotal} ngày LV`}
                        </div>
                      </td>
                      <td className={`${shared.colNum} ${styles.tabletHideCol}`}>
                        {d.status === "da_xu_ly" ? (
                          <span className={shared.numMuted}>{EMPTY_DASH}</span>
                        ) : (
                          <div className={styles.slaStack}>
                            <span className={ackPending ? styles.slaAckUrgent : styles.slaAckDone}>
                              {ackPending
                                ? `Xác nhận: còn ${d.ackSlaHoursLeft}h/24h`
                                : "Đã xác nhận 24h"}
                            </span>
                            <span className={styles.slaWorkDays}>
                              Xử lý: còn {d.resolveWorkingDaysLeft}/{d.resolveWorkingDaysTotal} ngày
                              LV
                            </span>
                          </div>
                        )}
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
                            handleSelectDesktop(d);
                          }}
                        >
                          {isSelected ? "Đang xem" : d.status === "da_xu_ly" ? "Xem" : "Phân xử"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {activeDesktopDispute && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Bảng chi tiết và phân xử khiếu nại"
            >
              {renderDetailPanel(activeDesktopDispute, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filtered.map((d) => {
              const meta = DISPUTE_STATUS_META[d.status];
              const isSelected = mobileOpenId === d.id;
              return (
                <div key={d.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => handleToggleMobile(d)}
                    aria-expanded={isSelected}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>
                        {d.caseCode} · {d.buyerName}
                      </span>
                      <span className={shared.mobileMetric}>{formatVnd(d.amount)}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {d.vulnerableGroup ? "Ưu tiên yếu thế · " : ""}
                        {d.tier === "cap_2_tu_gian_hang" ? "Cấp 2 shop" : "Sàn thụ lý"} ·{" "}
                        {d.status === "da_xu_ly"
                          ? "Đã đóng"
                          : `Còn ${d.resolveWorkingDaysLeft}/${d.resolveWorkingDaysTotal} ngày LV`}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                  </button>
                  {isSelected && (
                    <div className={shared.mobileDetailWrap}>{renderDetailPanel(d)}</div>
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

function DisputesRoute() {
  return (
    <AdminStateGate
      title="Khiếu nại & tranh chấp cấp 2"
      subtitle="Phân tách tranh chấp cấp 2 từ gian hàng chuyển lên, ưu tiên nhóm yếu thế và theo dõi đồng hồ SLA 24h / 5–7 ngày làm việc."
      emptyTitle="Không có khiếu nại nào cần xử lý"
      emptyMessage="Tất cả tranh chấp đơn hàng đã được xử lý và đóng hồ sơ."
      errorTitle="Không thể tải danh sách khiếu nại"
      errorMessage="Hệ thống quản lý tranh chấp tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <DisputesContent initialQuery={initialQuery} initialSelected={initialSelected} />
      )}
    </AdminStateGate>
  );
}

export default function AdminDisputesPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Khiếu nại & tranh chấp cấp 2" />}>
      <DisputesRoute />
    </Suspense>
  );
}
