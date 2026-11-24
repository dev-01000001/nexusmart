"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import type {
  AdminMediaModerationItem,
  AdminQaModerationItem,
  AdminReviewModerationItem,
  ModerationStatus,
} from "@/data/admin";
import type { GatewayBannedKeyword, PaginatedResponse } from "@/lib/gateway-types";
import { api } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconAlert, IconCheck, IconSearch } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import { formatShortTimeDateVi, MODERATION_STATUS_META } from "@/features/admin/adminMeta";
import { updateAdminModerationState, useAdminModerationState } from "@/features/admin/auditStore";
import { AdminBannedKeywordsTab } from "@/features/admin/AdminBannedKeywordsTab";
import { AdminProductModerationTab } from "@/features/admin/AdminProductModerationTab";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ModTab = "san_pham" | "tu_khoa_cam" | "reviews" | "qa" | "media";
type StatusFilter = "all" | ModerationStatus;

function ModerationContent({ initialQuery }: { initialQuery: string }) {
  const mod = useAdminModerationState();
  const { showToast } = useMockStore();

  const initialTab: ModTab = initialQuery.toUpperCase().startsWith("QA-")
    ? "qa"
    : initialQuery.toUpperCase().startsWith("MD-")
      ? "media"
      : initialQuery.toUpperCase().startsWith("DG-")
        ? "reviews"
        : "san_pham";

  const [activeTab, setActiveTab] = useState<ModTab>(initialTab);
  const [pendingProductCount, setPendingProductCount] = useState(0);
  const [activeBannedCount, setActiveBannedCount] = useState(0);

  const loadActiveBannedCount = useCallback(async () => {
    try {
      const res = await api<PaginatedResponse<GatewayBannedKeyword>>(
        "admin/banned-keywords?page=1&page_size=1"
      );
      setActiveBannedCount(res?.total ?? 0);
    } catch {
      // Keep previous KPI count on transient error
    }
  }, []);

  useEffect(() => {
    void loadActiveBannedCount();
  }, [loadActiveBannedCount]);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [selectedReviewId, setSelectedReviewId] = useState<string | null>("rev-2");
  const [selectedQaId, setSelectedQaId] = useState<string | null>("qa-1");
  const [selectedMediaId, setSelectedMediaId] = useState<string | null>("md-3");
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(null);
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});

  const counts = useMemo(() => {
    const revPending = mod.reviews.filter((r) => r.status === "cho_duyet").length;
    const qaPending = mod.qaItems.filter((q) => q.status === "cho_duyet").length;
    const mediaPending = mod.mediaItems.filter((m) => m.status === "cho_duyet").length;
    return {
      totalPending: revPending + qaPending + mediaPending,
      revPending,
      qaPending,
      mediaPending,
    };
  }, [mod]);

  const filteredReviews = useMemo(() => {
    const q = query.trim().toLowerCase();
    return mod.reviews.filter((item) => {
      if (statusFilter !== "all" && item.status !== statusFilter) return false;
      if (!q) return true;
      return (
        item.code.toLowerCase().includes(q) ||
        item.productName.toLowerCase().includes(q) ||
        item.shopName.toLowerCase().includes(q) ||
        item.buyerName.toLowerCase().includes(q) ||
        item.content.toLowerCase().includes(q) ||
        item.flagReason.toLowerCase().includes(q)
      );
    });
  }, [mod.reviews, statusFilter, query]);

  const filteredQa = useMemo(() => {
    const q = query.trim().toLowerCase();
    return mod.qaItems.filter((item) => {
      if (statusFilter !== "all" && item.status !== statusFilter) return false;
      if (!q) return true;
      return (
        item.code.toLowerCase().includes(q) ||
        item.productName.toLowerCase().includes(q) ||
        item.shopName.toLowerCase().includes(q) ||
        item.question.toLowerCase().includes(q) ||
        item.sellerReply.toLowerCase().includes(q)
      );
    });
  }, [mod.qaItems, statusFilter, query]);

  const filteredMedia = useMemo(() => {
    const q = query.trim().toLowerCase();
    return mod.mediaItems.filter((item) => {
      if (statusFilter !== "all" && item.status !== statusFilter) return false;
      if (!q) return true;
      return (
        item.code.toLowerCase().includes(q) ||
        item.productName.toLowerCase().includes(q) ||
        item.skuCode.toLowerCase().includes(q) ||
        item.shopName.toLowerCase().includes(q) ||
        item.flagReason.toLowerCase().includes(q)
      );
    });
  }, [mod.mediaItems, statusFilter, query]);

  const activeReview = useMemo(() => {
    if (filteredReviews.length === 0) return null;
    return filteredReviews.find((r) => r.id === selectedReviewId) ?? filteredReviews[0];
  }, [filteredReviews, selectedReviewId]);

  const activeQa = useMemo(() => {
    if (filteredQa.length === 0) return null;
    return filteredQa.find((q) => q.id === selectedQaId) ?? filteredQa[0];
  }, [filteredQa, selectedQaId]);

  const activeMedia = useMemo(() => {
    if (filteredMedia.length === 0) return null;
    return filteredMedia.find((m) => m.id === selectedMediaId) ?? filteredMedia[0];
  }, [filteredMedia, selectedMediaId]);

  const handleReviewDecision = (
    item: AdminReviewModerationItem,
    nextStatus: "da_duyet" | "da_an" | "canh_bao"
  ) => {
    const draft = (noteDrafts[item.id] ?? "").trim();
    const note =
      draft ||
      (nextStatus === "da_duyet"
        ? "Đã duyệt hiển thị đánh giá và dữ liệu độ vừa trên trang sản phẩm."
        : nextStatus === "da_an"
          ? "Đã ẩn đánh giá do vi phạm chính sách spam / dẫn liên kết ngoài sàn."
          : "Đã ẩn đánh giá và gửi cảnh báo vi phạm tới tài khoản liên quan.");

    updateAdminModerationState((prev) => ({
      ...prev,
      reviews: prev.reviews.map((r) =>
        r.id === item.id ? { ...r, status: nextStatus, moderatorNote: note } : r
      ),
    }));

    const actionText =
      nextStatus === "da_duyet"
        ? "Duyệt đánh giá sản phẩm"
        : nextStatus === "da_an"
          ? "Ẩn đánh giá vi phạm"
          : "Cảnh báo đánh giá spam / vi phạm";

    showToast(`${actionText} ${item.code}.`);
  };

  const handleQaDecision = (
    item: AdminQaModerationItem,
    nextStatus: "da_duyet" | "da_an" | "canh_bao"
  ) => {
    const draft = (noteDrafts[item.id] ?? "").trim();
    const note =
      draft ||
      (nextStatus === "da_duyet"
        ? "Đã duyệt hiển thị Hỏi đáp công khai trên trang sản phẩm."
        : nextStatus === "da_an"
          ? "Đã ẩn câu trả lời chứa từ cấm / dẫn giao dịch ngoài sàn."
          : "Đã ẩn nội dung và ghi nhận 1 điểm cảnh báo cho gian hàng.");

    updateAdminModerationState((prev) => ({
      ...prev,
      qaItems: prev.qaItems.map((q) =>
        q.id === item.id ? { ...q, status: nextStatus, moderatorNote: note } : q
      ),
    }));

    const actionText =
      nextStatus === "da_duyet"
        ? "Duyệt nội dung Hỏi đáp (Q&A)"
        : nextStatus === "da_an"
          ? "Ẩn Hỏi đáp dính từ cấm"
          : "Cảnh báo gian hàng vi phạm bộ lọc Q&A";

    showToast(`${actionText} ${item.code}.`);
  };

  const handleMediaDecision = (
    item: AdminMediaModerationItem,
    nextStatus: "da_duyet" | "da_an" | "canh_bao"
  ) => {
    const draft = (noteDrafts[item.id] ?? "").trim();
    const note =
      draft ||
      (nextStatus === "da_duyet"
        ? "Đã duyệt media biến thể đạt chuẩn nền phẳng và đồng bộ màu swatch."
        : nextStatus === "da_an"
          ? "Đã ẩn media không đạt tiêu chuẩn ánh sáng / nền phẳng sàn."
          : "Đã từ chối media và gửi cảnh báo yêu cầu gian hàng chụp lại đúng chuẩn.");

    updateAdminModerationState((prev) => ({
      ...prev,
      mediaItems: prev.mediaItems.map((m) =>
        m.id === item.id ? { ...m, status: nextStatus, moderatorNote: note } : m
      ),
    }));

    const actionText =
      nextStatus === "da_duyet"
        ? "Duyệt media biến thể sản phẩm"
        : nextStatus === "da_an"
          ? "Ẩn media không đạt chuẩn"
          : "Cảnh báo media biến thể sản phẩm";

    showToast(`${actionText} ${item.code}.`);
  };

  const renderReviewDetail = (item: AdminReviewModerationItem, showHeading = false) => {
    const meta = MODERATION_STATUS_META[item.status];
    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Kiểm duyệt đánh giá: {item.code} · {item.skuCode}
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}

        <div className={styles.previewRow}>
          <img
            src={item.thumbnail}
            alt={item.productName}
            className={styles.thumbLg}
            width={96}
            height={96}
          />
          <div className={styles.previewMeta}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Sản phẩm & gian hàng</span>
                <span className={shared.detailVal}>
                  {item.productName} ({item.shopName})
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Người mua & đơn hoàn tất</span>
                <span className={shared.detailVal}>
                  {item.buyerName} · {item.orderCode}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Số sao & độ vừa thực tế</span>
                <span className={`${shared.detailVal} num`}>
                  {item.rating}/5 sao · {item.fitFeedback} ({item.buyerMeasurements})
                </span>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Nội dung đánh giá</span>
                <span className={shared.detailValPlain}>{item.content}</span>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Cảnh báo bộ lọc & ghi chú</span>
                <span className={shared.detailValPlain}>
                  {item.flagReason}. {item.moderatorNote}
                </span>
              </div>
            </div>
          </div>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`mod-note-${item.id}`} className={shared.fieldLabel}>
              Ghi chú kiểm duyệt (ghi vào Nhật ký hệ thống)
            </label>
            <input
              id={`mod-note-${item.id}`}
              type="text"
              className={shared.textInput}
              value={noteDrafts[item.id] ?? ""}
              onChange={(e) => setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }))}
              placeholder="Nhập lý do duyệt, ẩn spam hoặc cảnh báo..."
            />
          </div>
          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={item.status === "da_duyet"}
              onClick={() => handleReviewDecision(item, "da_duyet")}
            >
              Duyệt hiển thị
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={item.status === "da_an"}
              onClick={() => handleReviewDecision(item, "da_an")}
            >
              Ẩn đánh giá
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={item.status === "canh_bao"}
              onClick={() => handleReviewDecision(item, "canh_bao")}
            >
              Cảnh báo
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const renderQaDetail = (item: AdminQaModerationItem, showHeading = false) => {
    const meta = MODERATION_STATUS_META[item.status];
    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Kiểm duyệt Hỏi đáp: {item.code} · {item.shopName}
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}

        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Sản phẩm</span>
            <span className={shared.detailVal}>{item.productName}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Người hỏi & thời điểm</span>
            <span className={shared.detailValPlain}>
              {item.askerName} · {formatDateTimeVi(item.submittedAt)}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Từ cấm phát hiện</span>
            <span
              className={
                item.matchedBannedTerms.length > 0 ? shared.priorityText : shared.detailValPlain
              }
            >
              {item.matchedBannedTerms.length > 0
                ? item.matchedBannedTerms.join(", ")
                : "Không phát hiện từ cấm"}
            </span>
          </div>
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Câu hỏi người mua</span>
            <span className={shared.detailValPlain}>{item.question}</span>
          </div>
          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <span className={shared.detailKey}>Phản hồi từ {item.shopName}</span>
            <span className={shared.detailValPlain}>{item.sellerReply}</span>
          </div>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`qa-note-${item.id}`} className={shared.fieldLabel}>
              Ghi chú kiểm duyệt Hỏi đáp
            </label>
            <input
              id={`qa-note-${item.id}`}
              type="text"
              className={shared.textInput}
              value={noteDrafts[item.id] ?? ""}
              onChange={(e) => setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }))}
              placeholder="Ghi lý do duyệt hoặc cảnh báo vi phạm dẫn ngoài sàn..."
            />
          </div>
          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={item.status === "da_duyet"}
              onClick={() => handleQaDecision(item, "da_duyet")}
            >
              Duyệt đăng
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={item.status === "da_an"}
              onClick={() => handleQaDecision(item, "da_an")}
            >
              Ẩn nội dung
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={item.status === "canh_bao"}
              onClick={() => handleQaDecision(item, "canh_bao")}
            >
              Cảnh báo shop
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const renderMediaDetail = (item: AdminMediaModerationItem, showHeading = false) => {
    const meta = MODERATION_STATUS_META[item.status];
    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Kiểm duyệt Media: {item.code} · {item.skuCode}
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}

        <div className={styles.previewRow}>
          <img
            src={item.previewUrl}
            alt={`${item.productName} - ${item.colorName}`}
            className={styles.thumbLg}
            width={96}
            height={96}
          />
          <div className={styles.previewMeta}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Sản phẩm & màu biến thể</span>
                <span className={shared.detailVal}>
                  {item.productName} · {item.colorName}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Gian hàng & định dạng</span>
                <span className={shared.detailValPlain}>
                  {item.shopName} · {item.mediaType} ({item.resolution})
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Kiểm tra kỹ thuật tự động</span>
                <div className={styles.checklistRow}>
                  <span className={item.flatBgPassed ? styles.checkOk : styles.checkWarn}>
                    {item.flatBgPassed ? <IconCheck size={13} /> : <IconAlert size={13} />}
                    {item.flatBgPassed ? "Nền phẳng đạt chuẩn" : "Ánh sáng nền lệch chuẩn"}
                  </span>
                  <span className={item.noExternalLogoPassed ? styles.checkOk : styles.checkWarn}>
                    {item.noExternalLogoPassed ? <IconCheck size={13} /> : <IconAlert size={13} />}
                    {item.noExternalLogoPassed ? "Không logo ngoài" : "Nghi vấn logo ngoài"}
                  </span>
                </div>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Ghi chú kiểm tra</span>
                <span className={shared.detailValPlain}>
                  {item.flagReason}. {item.moderatorNote}
                </span>
              </div>
            </div>
          </div>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`md-note-${item.id}`} className={shared.fieldLabel}>
              Ghi chú kiểm duyệt Media biến thể
            </label>
            <input
              id={`md-note-${item.id}`}
              type="text"
              className={shared.textInput}
              value={noteDrafts[item.id] ?? ""}
              onChange={(e) => setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }))}
              placeholder="Ghi nhận kết quả đối chiếu màu sắc, nền phẳng hoặc yêu cầu chụp lại..."
            />
          </div>
          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={item.status === "da_duyet"}
              onClick={() => handleMediaDecision(item, "da_duyet")}
            >
              Duyệt media
            </Button>
            <Button
              variant="outline"
              size="sm"
              shape="app"
              disabled={item.status === "da_an"}
              onClick={() => handleMediaDecision(item, "da_an")}
            >
              Ẩn media
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={item.status === "canh_bao"}
              onClick={() => handleMediaDecision(item, "canh_bao")}
            >
              Cảnh báo shop
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const currentResultCount =
    activeTab === "reviews"
      ? filteredReviews.length
      : activeTab === "qa"
        ? filteredQa.length
        : filteredMedia.length;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Kiểm duyệt sản phẩm & nội dung</h1>
          <p className={shared.pageSubtitle}>
            Hiện có {formatInt(pendingProductCount)} sản phẩm mới chờ duyệt hiển thị công khai trên
            sàn cùng các mục kiểm duyệt đánh giá, hỏi đáp và media mẫu.
          </p>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp hàng đợi kiểm duyệt"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Sản phẩm chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(pendingProductCount)}</span>
          <span className={shared.kpiCaption}>Hàng đợi thực từ gian hàng đăng bán</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Từ khoá cấm đang bật</span>
          <span className={shared.kpiValue}>{formatInt(activeBannedCount)}</span>
          <span className={shared.kpiCaption}>Đồng bộ trực tiếp với máy chủ</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Hỏi đáp mẫu</span>
          <span className={shared.kpiValue}>{formatInt(counts.qaPending)}</span>
          <span className={shared.kpiCaption}>
            Trong {formatInt(mod.qaItems.length)} câu hỏi mẫu
          </span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Media mẫu</span>
          <span className={shared.kpiValue}>{formatInt(counts.mediaPending)}</span>
          <span className={shared.kpiCaption}>Ảnh nền phẳng & video tham khảo</span>
        </div>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Hàng đợi kiểm duyệt theo loại">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "san_pham"}
            className={`${shared.tabBtn} ${activeTab === "san_pham" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("san_pham")}
          >
            <span>Sản phẩm chờ duyệt</span>
            <span className={shared.tabCount}>{pendingProductCount}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "tu_khoa_cam"}
            className={`${shared.tabBtn} ${activeTab === "tu_khoa_cam" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("tu_khoa_cam")}
          >
            <span>Từ khoá cấm</span>
            {activeBannedCount > 0 && <span className={shared.tabCount}>{activeBannedCount}</span>}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "reviews"}
            className={`${shared.tabBtn} ${activeTab === "reviews" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("reviews")}
          >
            <span>Đánh giá sản phẩm (mẫu)</span>
            <span className={shared.tabCount}>{counts.revPending}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "qa"}
            className={`${shared.tabBtn} ${activeTab === "qa" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("qa")}
          >
            <span>Hỏi đáp mẫu</span>
            <span className={shared.tabCount}>{counts.qaPending}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "media"}
            className={`${shared.tabBtn} ${activeTab === "media" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("media")}
          >
            <span>Media biến thể (mẫu)</span>
            <span className={shared.tabCount}>{counts.mediaPending}</span>
          </button>
        </div>
      </div>

      {activeTab === "san_pham" && (
        <AdminProductModerationTab
          initialQuery={initialQuery}
          onPendingCountChange={setPendingProductCount}
        />
      )}

      {activeTab === "tu_khoa_cam" && (
        <AdminBannedKeywordsTab onMutated={() => void loadActiveBannedCount()} />
      )}

      {activeTab !== "san_pham" && activeTab !== "tu_khoa_cam" && (
        <>
          <section className={shared.panel} role="note" style={{ marginTop: 16 }}>
            <StatusPill label="Bản xem trước, dữ liệu mẫu" tone="warning" />
            <p className={shared.pageSubtitle} style={{ marginTop: 8 }}>
              Hàng đợi đánh giá, hỏi đáp và media biến thể bên dưới là dữ liệu mẫu minh họa. Tab Sản
              phẩm chờ duyệt và Từ khoá cấm đang đồng bộ trực tiếp với máy chủ.
            </p>
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
                  placeholder="Tìm mã DG-, QA-, MD-, SKU, gian hàng..."
                  aria-label="Tìm kiếm trong hàng đợi kiểm duyệt"
                />
              </div>
            </div>

            <div className={shared.toolbarRight}>
              <span className={shared.resultCount}>{formatInt(currentResultCount)} mục</span>
              <select
                className={shared.toolbarSelect}
                value={statusFilter}
                onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
                aria-label="Lọc trạng thái kiểm duyệt"
              >
                <option value="all">Tất cả trạng thái</option>
                <option value="cho_duyet">Chờ kiểm duyệt</option>
                <option value="da_duyet">Đã duyệt</option>
                <option value="da_an">Đã ẩn</option>
                <option value="canh_bao">Đã cảnh báo</option>
              </select>
            </div>
          </div>
        </>
      )}

      {/* TAB 1: ĐÁNH GIÁ SẢN PHẨM */}
      {activeTab === "reviews" &&
        (filteredReviews.length === 0 ? (
          <p className={shared.emptyRow}>Không có đánh giá nào khớp với bộ lọc hiện tại.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Sản phẩm & nội dung đánh giá</th>
                    <th>Người mua & độ vừa</th>
                    <th>Cảnh báo tự động</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredReviews.map((item) => {
                    const meta = MODERATION_STATUS_META[item.status];
                    const isSelected = activeReview?.id === item.id;
                    return (
                      <tr
                        key={item.id}
                        className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                        onClick={() => setSelectedReviewId(item.id)}
                      >
                        <td>
                          <div className={styles.mediaThumbCell}>
                            <img
                              src={item.thumbnail}
                              alt={item.productName}
                              className={styles.thumbSm}
                              width={44}
                              height={44}
                            />
                            <div>
                              <div className={shared.cellPrimary}>
                                {item.code} · {item.productName}
                              </div>
                              <div className={`${shared.cellSub} ${styles.excerptCell}`}>
                                {item.content}
                              </div>
                            </div>
                          </div>
                        </td>
                        <td>
                          <div>
                            {item.buyerName} · {item.rating}/5 sao
                          </div>
                          <div className={shared.cellSub}>
                            {item.fitFeedback} · {item.shopName}
                          </div>
                        </td>
                        <td>
                          <div className={styles.excerptCell}>{item.flagReason}</div>
                          <div className={shared.cellSub}>
                            {formatShortTimeDateVi(item.submittedAt)}
                          </div>
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
                              setSelectedReviewId(item.id);
                            }}
                          >
                            {isSelected ? "Đang xem" : "Kiểm duyệt"}
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            {activeReview && (
              <section
                className={shared.desktopDetailSection}
                aria-label="Chi tiết kiểm duyệt đánh giá"
              >
                {renderReviewDetail(activeReview, true)}
              </section>
            )}

            <div className={shared.mobileList}>
              {filteredReviews.map((item) => {
                const meta = MODERATION_STATUS_META[item.status];
                const isOpen = mobileOpenId === item.id;
                return (
                  <div key={item.id} className={shared.mobileItem}>
                    <button
                      type="button"
                      className={shared.mobileItemBtn}
                      onClick={() => setMobileOpenId((p) => (p === item.id ? null : item.id))}
                      aria-expanded={isOpen}
                    >
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>
                          {item.code} · {item.buyerName}
                        </span>
                        <span className={shared.mobileMetric}>{item.rating}/5 sao</span>
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {item.shopName} · {item.fitFeedback}
                        </span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </div>
                    </button>
                    {isOpen && (
                      <div className={shared.mobileDetailWrap}>{renderReviewDetail(item)}</div>
                    )}
                  </div>
                );
              })}
            </div>
          </>
        ))}

      {/* TAB 2: HỎI ĐÁP & BỘ LỌC TỪ CẤM */}
      {activeTab === "qa" && (
        <>
          {filteredQa.length === 0 ? (
            <p className={shared.emptyRow}>Không có mục Hỏi đáp nào khớp với bộ lọc hiện tại.</p>
          ) : (
            <>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã & câu hỏi người mua</th>
                      <th>Phản hồi gian hàng</th>
                      <th>Bộ lọc từ cấm</th>
                      <th>Trạng thái</th>
                      <th className={shared.colAction}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredQa.map((item) => {
                      const meta = MODERATION_STATUS_META[item.status];
                      const isSelected = activeQa?.id === item.id;
                      return (
                        <tr
                          key={item.id}
                          className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                          onClick={() => setSelectedQaId(item.id)}
                        >
                          <td>
                            <div className={shared.cellPrimary}>
                              {item.code} · {item.productName}
                            </div>
                            <div className={`${shared.cellSub} ${styles.excerptCell}`}>
                              {item.question}
                            </div>
                          </td>
                          <td>
                            <div className={styles.excerptCell}>{item.sellerReply}</div>
                            <div className={shared.cellSub}>{item.shopName}</div>
                          </td>
                          <td>
                            {item.matchedBannedTerms.length > 0 ? (
                              <span className={shared.priorityText}>
                                Dính: {item.matchedBannedTerms.join(", ")}
                              </span>
                            ) : (
                              <span className={shared.tierText}>Sạch</span>
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
                                setSelectedQaId(item.id);
                              }}
                            >
                              {isSelected ? "Đang xem" : "Kiểm duyệt"}
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {activeQa && (
                <section
                  className={shared.desktopDetailSection}
                  aria-label="Chi tiết kiểm duyệt Hỏi đáp"
                >
                  {renderQaDetail(activeQa, true)}
                </section>
              )}

              <div className={shared.mobileList}>
                {filteredQa.map((item) => {
                  const meta = MODERATION_STATUS_META[item.status];
                  const isOpen = mobileOpenId === item.id;
                  return (
                    <div key={item.id} className={shared.mobileItem}>
                      <button
                        type="button"
                        className={shared.mobileItemBtn}
                        onClick={() => setMobileOpenId((p) => (p === item.id ? null : item.id))}
                        aria-expanded={isOpen}
                      >
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>
                            {item.code} · {item.shopName}
                          </span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {item.productName}
                            {item.matchedBannedTerms.length > 0 ? " · Dính từ cấm" : ""}
                          </span>
                        </div>
                      </button>
                      {isOpen && (
                        <div className={shared.mobileDetailWrap}>{renderQaDetail(item)}</div>
                      )}
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </>
      )}

      {/* TAB 3: MEDIA BIẾN THỂ SẢN PHẨM */}
      {activeTab === "media" &&
        (filteredMedia.length === 0 ? (
          <p className={shared.emptyRow}>Không có ảnh/video biến thể nào khớp bộ lọc hiện tại.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Ảnh / Video & biến thể màu</th>
                    <th>Gian hàng & định dạng</th>
                    <th>Kiểm tra chuẩn ảnh sàn</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredMedia.map((item) => {
                    const meta = MODERATION_STATUS_META[item.status];
                    const isSelected = activeMedia?.id === item.id;
                    return (
                      <tr
                        key={item.id}
                        className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                        onClick={() => setSelectedMediaId(item.id)}
                      >
                        <td>
                          <div className={styles.mediaThumbCell}>
                            <img
                              src={item.previewUrl}
                              alt={item.productName}
                              className={styles.thumbSm}
                              width={44}
                              height={44}
                            />
                            <div>
                              <div className={shared.cellPrimary}>
                                {item.code} · {item.productName}
                              </div>
                              <div className={shared.cellSub}>
                                {item.skuCode} · Màu {item.colorName}
                              </div>
                            </div>
                          </div>
                        </td>
                        <td>
                          <div>{item.shopName}</div>
                          <div className={shared.cellSub}>
                            {item.mediaType} · {item.resolution}
                          </div>
                        </td>
                        <td>
                          <div className={styles.excerptCell}>{item.flagReason}</div>
                          <div className={shared.cellSub}>
                            {item.flatBgPassed ? "Nền phẳng đạt" : "Cần chỉnh sáng nền"}
                          </div>
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
                              setSelectedMediaId(item.id);
                            }}
                          >
                            {isSelected ? "Đang xem" : "Xem media"}
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            {activeMedia && (
              <section
                className={shared.desktopDetailSection}
                aria-label="Chi tiết kiểm duyệt Media biến thể"
              >
                {renderMediaDetail(activeMedia, true)}
              </section>
            )}

            <div className={shared.mobileList}>
              {filteredMedia.map((item) => {
                const meta = MODERATION_STATUS_META[item.status];
                const isOpen = mobileOpenId === item.id;
                return (
                  <div key={item.id} className={shared.mobileItem}>
                    <button
                      type="button"
                      className={shared.mobileItemBtn}
                      onClick={() => setMobileOpenId((p) => (p === item.id ? null : item.id))}
                      aria-expanded={isOpen}
                    >
                      <div className={shared.mobileLine1}>
                        <span className={shared.mobileIdentity}>
                          {item.code} · {item.productName}
                        </span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          {item.shopName} · {item.mediaType}
                        </span>
                      </div>
                    </button>
                    {isOpen && (
                      <div className={shared.mobileDetailWrap}>{renderMediaDetail(item)}</div>
                    )}
                  </div>
                );
              })}
            </div>
          </>
        ))}
    </div>
  );
}

function AdminModerationRoute() {
  return (
    <AdminStateGate
      title="Kiểm duyệt sản phẩm & nội dung"
      subtitle="Phê duyệt sản phẩm từ gian hàng đăng bán cùng các hàng đợi kiểm duyệt đánh giá, hỏi đáp và media mẫu."
      emptyTitle="Hàng đợi kiểm duyệt đang trống"
      emptyMessage="Tất cả sản phẩm và nội dung đã được kiểm duyệt hoàn tất."
      errorTitle="Không thể tải hàng đợi kiểm duyệt"
      errorMessage="Hệ thống kiểm duyệt nội dung tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery }) => <ModerationContent initialQuery={initialQuery} />}
    </AdminStateGate>
  );
}

export default function AdminModerationPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Kiểm duyệt sản phẩm & nội dung" />}>
      <AdminModerationRoute />
    </Suspense>
  );
}
