"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import type {
  AdminBannedKeyword,
  AdminMediaModerationItem,
  AdminQaModerationItem,
  AdminReviewModerationItem,
  ModerationStatus,
} from "@/data";
import { Button, IconAlert, IconCheck, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { formatShortTimeDateVi, MODERATION_STATUS_META } from "@/features/admin/adminMeta";
import { getProductReviewStatusMeta, type GwAdminProduct } from "@/features/admin/adminApiTypes";
import {
  appendAdminAuditLog,
  updateAdminModerationState,
  useAdminModerationState,
} from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { StatusPill } from "@/features/admin/StatusPill";
import { ToggleSwitch } from "@/features/admin/ToggleSwitch";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ModTab = "products" | "reviews" | "qa" | "media";
type StatusFilter = "all" | ModerationStatus;
type ProductStatusFilter = "all" | "PENDING" | "APPROVED" | "REJECTED" | "HIDDEN" | "OUT_OF_STOCK";

export interface AdminModerationClientProps {
  initialProducts: GwAdminProduct[];
  loadError: string | null;
}

function resolveInitialTab(initialQuery: string): ModTab {
  const upper = initialQuery.trim().toUpperCase();
  if (upper.startsWith("DG-")) return "reviews";
  if (upper.startsWith("QA-")) return "qa";
  if (upper.startsWith("MD-")) return "media";
  return "products";
}

function ModerationContent({
  initialProducts,
  loadError,
  initialQuery,
}: {
  initialProducts: GwAdminProduct[];
  loadError: string | null;
  initialQuery: string;
}) {
  const router = useRouter();
  const mod = useAdminModerationState();
  const { showToast } = useAdminContext();

  const [products, setProducts] = useState<GwAdminProduct[]>(initialProducts);
  const [activeTab, setActiveTab] = useState<ModTab>(() => resolveInitialTab(initialQuery));
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [productStatusFilter, setProductStatusFilter] = useState<ProductStatusFilter>("all");
  const [query, setQuery] = useState(initialQuery);

  const [selectedProductId, setSelectedProductId] = useState<string | null>(
    initialProducts[0]?.id ?? null
  );
  const [selectedReviewId, setSelectedReviewId] = useState<string | null>("rev-2");
  const [selectedQaId, setSelectedQaId] = useState<string | null>("qa-1");
  const [selectedMediaId, setSelectedMediaId] = useState<string | null>("md-3");
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(null);
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});
  const [productErrorId, setProductErrorId] = useState<string | null>(null);
  const [busyProductId, setBusyProductId] = useState<string | null>(null);

  // Banned keyword form (mock Q&A filter)
  const [newBannedTerm, setNewBannedTerm] = useState("");
  const [newBannedCategory, setNewBannedCategory] =
    useState<AdminBannedKeyword["category"]>("Link ngoài sàn");
  const [newBannedMode, setNewBannedMode] =
    useState<AdminBannedKeyword["actionMode"]>("chan_tu_dong");

  const counts = useMemo(() => {
    const productPending = products.filter((p) => p.status.toUpperCase() === "PENDING").length;
    const revPending = mod.reviews.filter((r) => r.status === "cho_duyet").length;
    const qaPending = mod.qaItems.filter((q) => q.status === "cho_duyet").length;
    const mediaPending = mod.mediaItems.filter((m) => m.status === "cho_duyet").length;
    const activeBanned = mod.bannedKeywords.filter((b) => b.active).length;
    return {
      productPending,
      totalPending: productPending + revPending + qaPending + mediaPending,
      revPending,
      qaPending,
      mediaPending,
      activeBanned,
    };
  }, [products, mod]);

  const filteredProducts = useMemo(() => {
    const q = query.trim().toLowerCase();
    return products.filter((item) => {
      if (productStatusFilter !== "all" && item.status.toUpperCase() !== productStatusFilter) {
        return false;
      }
      if (!q) return true;
      return (
        item.name.toLowerCase().includes(q) ||
        item.id.toLowerCase().includes(q) ||
        item.shop_id.toLowerCase().includes(q) ||
        item.skus.some(
          (sku) => sku.sku_code.toLowerCase().includes(q) || sku.name.toLowerCase().includes(q)
        )
      );
    });
  }, [products, productStatusFilter, query]);

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

  const activeProduct = useMemo(() => {
    if (filteredProducts.length === 0) return null;
    return filteredProducts.find((p) => p.id === selectedProductId) ?? filteredProducts[0];
  }, [filteredProducts, selectedProductId]);

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

  // Real backend product review handler
  const handleProductDecision = async (item: GwAdminProduct, decision: "APPROVE" | "REJECT") => {
    if (busyProductId) return;
    const reason = (noteDrafts[item.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setProductErrorId(item.id);
      return;
    }
    setProductErrorId(null);
    setBusyProductId(item.id);
    try {
      const res = await api<{ product: GwAdminProduct }>(
        `/api/admin/products/${encodeURIComponent(item.id)}/review`,
        {
          method: "POST",
          body: { decision, reason },
        }
      );
      setProducts((prev) => prev.map((p) => (p.id === item.id ? { ...p, ...res.product } : p)));
      setNoteDrafts((prev) => ({ ...prev, [item.id]: "" }));
      showToast(
        decision === "APPROVE"
          ? `Đã duyệt đăng bán sản phẩm "${item.name}".`
          : `Đã từ chối sản phẩm "${item.name}".`
      );
      router.refresh();
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể cập nhật kiểm duyệt sản phẩm lúc này.";
      showToast(msg);
    } finally {
      setBusyProductId(null);
    }
  };

  // Mock handlers for Reviews / Q&A / Media (no backend RPC yet)
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

    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: actionText,
      module: "Kiểm duyệt",
      target: item.code,
      changeSummary: `${item.skuCode} (${item.shopName}): ${note}`,
    });
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

    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: actionText,
      module: "Kiểm duyệt",
      target: item.code,
      changeSummary: `${item.productName} (${item.shopName}): ${note}`,
    });
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

    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: actionText,
      module: "Kiểm duyệt",
      target: item.code,
      changeSummary: `${item.skuCode} (${item.colorName} · ${item.shopName}): ${note}`,
    });
    showToast(`${actionText} ${item.code}.`);
  };

  const handleAddBannedKeyword = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = newBannedTerm.trim();
    if (!clean) return;
    updateAdminModerationState((prev) => ({
      ...prev,
      bannedKeywords: [
        {
          id: `bk-${Date.now()}`,
          term: clean,
          category: newBannedCategory,
          actionMode: newBannedMode,
          hitCount30d: 0,
          active: true,
        },
        ...prev.bannedKeywords,
      ],
    }));
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: "Thêm cụm từ cấm vào bộ lọc Q&A & Đánh giá",
      module: "Kiểm duyệt",
      target: "BANNED-FILTER",
      changeSummary: `Thêm "${clean}" (${newBannedCategory}, ${newBannedMode === "chan_tu_dong" ? "Chặn tự động" : "Gắn cờ duyệt"}).`,
    });
    setNewBannedTerm("");
    showToast(`Đã thêm cụm từ cấm "${clean}".`);
  };

  const handleToggleBannedKeyword = (id: string) => {
    const item = mod.bannedKeywords.find((b) => b.id === id);
    if (!item) return;
    const nextActive = !item.active;
    updateAdminModerationState((prev) => ({
      ...prev,
      bannedKeywords: prev.bannedKeywords.map((b) =>
        b.id === id ? { ...b, active: nextActive } : b
      ),
    }));
    appendAdminAuditLog({
      role: "Vận hành sàn",
      action: nextActive ? "Bật quy tắc bộ lọc từ cấm" : "Tạm tắt quy tắc bộ lọc từ cấm",
      module: "Kiểm duyệt",
      target: item.id,
      changeSummary: `${nextActive ? "Kích hoạt" : "Tắt"} cụm từ "${item.term}".`,
    });
  };

  const renderProductDetail = (item: GwAdminProduct, showHeading = false) => {
    const meta = getProductReviewStatusMeta(item.status);
    const isBusy = busyProductId === item.id;
    const totalStock = item.skus.reduce((sum, sku) => sum + sku.available_stock, 0);
    const thumb = item.images[0] || "/images/placeholder-product.svg";

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>Kiểm duyệt sản phẩm: {item.name}</h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}

        <div className={styles.previewRow}>
          <img src={thumb} alt={item.name} className={styles.thumbLg} width={96} height={96} />
          <div className={styles.previewMeta}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã sản phẩm & gian hàng</span>
                <span className={shared.detailVal}>
                  {item.id.slice(0, 8)} · Shop {item.shop_id.slice(0, 8)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Khoảng giá bán</span>
                <span className={`${shared.detailVal} num`}>
                  {item.min_price_vnd === item.max_price_vnd
                    ? formatVnd(item.min_price_vnd)
                    : `${formatVnd(item.min_price_vnd)} - ${formatVnd(item.max_price_vnd)}`}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Biến thể & tồn kho khả dụng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(item.skus.length)} biến thể · {formatInt(totalStock)} sản phẩm
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Ngày tạo</span>
                <span className={shared.detailValPlain}>
                  {item.created_at ? formatDateTimeVi(item.created_at) : "Mới cập nhật"}
                </span>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Mô tả sản phẩm</span>
                <span className={shared.detailValPlain}>
                  {item.description || "Gian hàng chưa nhập mô tả chi tiết."}
                </span>
              </div>
              {item.skus.length > 0 && (
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Danh sách phân loại (SKU)</span>
                  <span className={shared.detailValPlain}>
                    {item.skus
                      .map(
                        (sku) =>
                          `${sku.sku_code} (${sku.name}: ${formatVnd(sku.price_vnd)}, tồn ${formatInt(sku.available_stock)})`
                      )
                      .join(" · ")}
                  </span>
                </div>
              )}
              {item.rejection_reason && (
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Lý do từ chối gần nhất</span>
                  <span className={shared.priorityText}>{item.rejection_reason}</span>
                </div>
              )}
            </div>
          </div>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`prod-note-${item.id}`} className={shared.fieldLabel}>
              Lý do kiểm duyệt (bắt buộc khi Từ chối)
            </label>
            <input
              id={`prod-note-${item.id}`}
              type="text"
              className={shared.textInput}
              value={noteDrafts[item.id] ?? ""}
              onChange={(e) => {
                setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }));
                if (productErrorId === item.id) setProductErrorId(null);
              }}
              placeholder="Nhập ghi chú duyệt hoặc lý do từ chối gửi tới gian hàng..."
            />
            {productErrorId === item.id && (
              <span className={shared.validationError}>
                Vui lòng nhập lý do cụ thể trước khi từ chối sản phẩm.
              </span>
            )}
          </div>
          <div className={shared.detailButtons}>
            <Button
              variant="primary"
              size="sm"
              shape="app"
              disabled={isBusy || item.status.toUpperCase() === "APPROVED"}
              onClick={() => void handleProductDecision(item, "APPROVE")}
            >
              {isBusy ? "Đang xử lý..." : "Duyệt đăng bán"}
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={isBusy || item.status.toUpperCase() === "REJECTED"}
              onClick={() => void handleProductDecision(item, "REJECT")}
            >
              Từ chối
            </Button>
          </div>
        </div>
      </div>
    );
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
              Ghi chú kiểm duyệt (dữ liệu mẫu cục bộ)
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
              Ghi chú kiểm duyệt Hỏi đáp (dữ liệu mẫu cục bộ)
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
              Ghi chú kiểm duyệt Media biến thể (dữ liệu mẫu cục bộ)
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
    activeTab === "products"
      ? filteredProducts.length
      : activeTab === "reviews"
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
            Có {formatInt(counts.productPending)} sản phẩm gian hàng chờ duyệt (dữ liệu thực từ hệ
            thống) cùng {formatInt(counts.revPending + counts.qaPending + counts.mediaPending)} mục
            kiểm duyệt đánh giá, hỏi đáp và media mẫu.
          </p>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp hàng đợi kiểm duyệt"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Sản phẩm chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.productPending)}</span>
          <span className={shared.kpiCaption}>
            Trong {formatInt(products.length)} sản phẩm trên sàn
          </span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Đánh giá bị gắn cờ</span>
          <span className={shared.kpiValue}>{formatInt(counts.revPending)}</span>
          <span className={shared.kpiCaption}>
            Dữ liệu mẫu ({formatInt(mod.reviews.length)} đánh giá)
          </span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Hỏi đáp chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.qaPending)}</span>
          <span className={shared.kpiCaption}>
            Dữ liệu mẫu · {formatInt(counts.activeBanned)} từ cấm bật
          </span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Media biến thể chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(counts.mediaPending)}</span>
          <span className={shared.kpiCaption}>Dữ liệu mẫu cục bộ</span>
        </div>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Hàng đợi kiểm duyệt theo loại">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "products"}
            className={`${shared.tabBtn} ${activeTab === "products" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("products")}
          >
            <span>Sản phẩm chờ duyệt</span>
            <span className={shared.tabCount}>{counts.productPending}</span>
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
            <span>Hỏi đáp & Từ cấm (mẫu)</span>
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
              placeholder={
                activeTab === "products"
                  ? "Tìm tên sản phẩm, mã SKU, mã gian hàng..."
                  : "Tìm mã DG-, QA-, MD-, SKU, gian hàng..."
              }
              aria-label="Tìm kiếm trong hàng đợi kiểm duyệt"
            />
          </div>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(currentResultCount)} mục</span>
          {activeTab === "products" ? (
            <select
              className={shared.toolbarSelect}
              value={productStatusFilter}
              onChange={(e) => setProductStatusFilter(e.target.value as ProductStatusFilter)}
              aria-label="Lọc trạng thái kiểm duyệt sản phẩm"
            >
              <option value="all">Tất cả trạng thái</option>
              <option value="PENDING">Chờ kiểm duyệt</option>
              <option value="APPROVED">Đã duyệt bán</option>
              <option value="REJECTED">Đã từ chối</option>
              <option value="OUT_OF_STOCK">Hết hàng</option>
              <option value="HIDDEN">Đang ẩn</option>
            </select>
          ) : (
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
          )}
        </div>
      </div>

      {/* TAB 0 (REAL): SẢN PHẨM CHỜ DUYỆT */}
      {activeTab === "products" &&
        (loadError ? (
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải danh sách sản phẩm kiểm duyệt"
            message={loadError}
            actionLabel="Thử tải lại"
            onAction={() => router.refresh()}
          />
        ) : filteredProducts.length === 0 ? (
          <p className={shared.emptyRow}>Không có sản phẩm nào khớp với bộ lọc hiện tại.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Sản phẩm & gian hàng</th>
                    <th className={shared.colNum}>Giá niêm yết</th>
                    <th className={shared.colNum}>Biến thể / Tồn kho</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredProducts.map((item) => {
                    const meta = getProductReviewStatusMeta(item.status);
                    const isSelected = activeProduct?.id === item.id;
                    const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                    const thumb = item.images[0] || "/images/placeholder-product.svg";
                    return (
                      <tr
                        key={item.id}
                        className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                        onClick={() => setSelectedProductId(item.id)}
                      >
                        <td>
                          <div className={styles.mediaThumbCell}>
                            <img
                              src={thumb}
                              alt={item.name}
                              className={styles.thumbSm}
                              width={44}
                              height={44}
                            />
                            <div>
                              <div className={shared.cellPrimary}>{item.name}</div>
                              <div className={shared.cellSub}>
                                ID {item.id.slice(0, 8)} · Shop {item.shop_id.slice(0, 8)}
                              </div>
                            </div>
                          </div>
                        </td>
                        <td className={shared.colNum}>
                          {item.min_price_vnd === item.max_price_vnd
                            ? formatVnd(item.min_price_vnd)
                            : `${formatVnd(item.min_price_vnd)} - ${formatVnd(item.max_price_vnd)}`}
                        </td>
                        <td className={shared.colNum}>
                          <div>{formatInt(item.skus.length)} SKU</div>
                          <div className={shared.cellSub}>Tồn {formatInt(totalStock)}</div>
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
                              setSelectedProductId(item.id);
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

            {activeProduct && (
              <section
                className={shared.desktopDetailSection}
                aria-label="Chi tiết kiểm duyệt sản phẩm"
              >
                {renderProductDetail(activeProduct, true)}
              </section>
            )}

            <div className={shared.mobileList}>
              {filteredProducts.map((item) => {
                const meta = getProductReviewStatusMeta(item.status);
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
                        <span className={shared.mobileIdentity}>{item.name}</span>
                        <span className={shared.mobileMetric}>{formatVnd(item.min_price_vnd)}</span>
                      </div>
                      <div className={shared.mobileLine2}>
                        <span className={shared.mobileMetaText}>
                          Shop {item.shop_id.slice(0, 8)} · {formatInt(item.skus.length)} SKU
                        </span>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </div>
                    </button>
                    {isOpen && (
                      <div className={shared.mobileDetailWrap}>{renderProductDetail(item)}</div>
                    )}
                  </div>
                );
              })}
            </div>
          </>
        ))}

      {/* TAB 1 (MOCK): ĐÁNH GIÁ SẢN PHẨM */}
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

      {/* TAB 2 (MOCK): HỎI ĐÁP & BỘ LỌC TỪ CẤM */}
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

          <section
            className={`${shared.panel} ${styles.bannedSection}`}
            aria-labelledby="kd-banned-heading"
          >
            <div className={shared.panelHeader}>
              <h2 id="kd-banned-heading" className={shared.panelTitle}>
                Bộ lọc từ cấm cho Đánh giá & Hỏi đáp (mẫu)
              </h2>
              <span className={shared.panelMeta}>
                {formatInt(counts.activeBanned)}/{mod.bannedKeywords.length} quy tắc đang bật
              </span>
            </div>

            <form className={styles.bannedForm} onSubmit={handleAddBannedKeyword}>
              <div className={styles.fieldGroup}>
                <label htmlFor="bk-term" className={shared.fieldLabel}>
                  Cụm từ cần lọc
                </label>
                <input
                  id="bk-term"
                  type="text"
                  className={shared.textInput}
                  value={newBannedTerm}
                  onChange={(e) => setNewBannedTerm(e.target.value)}
                  placeholder="VD: chuyển khoản ngoài sàn, telegram..."
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="bk-cat" className={shared.fieldLabel}>
                  Nhóm vi phạm
                </label>
                <select
                  id="bk-cat"
                  className={shared.toolbarSelect}
                  value={newBannedCategory}
                  onChange={(e) =>
                    setNewBannedCategory(e.target.value as AdminBannedKeyword["category"])
                  }
                >
                  <option value="Link ngoài sàn">Link ngoài sàn</option>
                  <option value="Ngôn từ công kích">Ngôn từ công kích</option>
                  <option value="Cam kết sai lệch">Cam kết sai lệch</option>
                  <option value="Nhãn hiệu cấm">Nhãn hiệu cấm</option>
                </select>
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="bk-mode" className={shared.fieldLabel}>
                  Hành động tự động
                </label>
                <select
                  id="bk-mode"
                  className={shared.toolbarSelect}
                  value={newBannedMode}
                  onChange={(e) =>
                    setNewBannedMode(e.target.value as AdminBannedKeyword["actionMode"])
                  }
                >
                  <option value="chan_tu_dong">Chặn tự động</option>
                  <option value="gan_co_duyet">Gắn cờ chờ duyệt</option>
                </select>
              </div>
              <Button type="submit" variant="ink" size="sm" shape="app">
                Thêm từ cấm
              </Button>
            </form>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Cụm từ lọc</th>
                    <th>Nhóm vi phạm</th>
                    <th>Xử lý tự động</th>
                    <th className={shared.colNum}>Lượt chặn 30 ngày</th>
                    <th className={shared.colAction}>Kích hoạt</th>
                  </tr>
                </thead>
                <tbody>
                  {mod.bannedKeywords.map((bk) => (
                    <tr key={bk.id}>
                      <td className={shared.cellPrimary}>{bk.term}</td>
                      <td>{bk.category}</td>
                      <td>
                        <StatusPill
                          label={
                            bk.actionMode === "chan_tu_dong" ? "Chặn tự động" : "Gắn cờ chờ duyệt"
                          }
                          tone={bk.actionMode === "chan_tu_dong" ? "negative" : "warning"}
                        />
                      </td>
                      <td className={shared.colNum}>{formatInt(bk.hitCount30d)}</td>
                      <td className={shared.colAction}>
                        <ToggleSwitch
                          id={`sw-bk-${bk.id}`}
                          checked={bk.active}
                          label={`Bật hoặc tắt cụm từ cấm ${bk.term}`}
                          onChange={() => handleToggleBannedKeyword(bk.id)}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {mod.bannedKeywords.map((bk) => (
                <div key={bk.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{bk.term}</span>
                    <ToggleSwitch
                      id={`sw-bkm-${bk.id}`}
                      checked={bk.active}
                      label={`Bật hoặc tắt từ cấm ${bk.term}`}
                      onChange={() => handleToggleBannedKeyword(bk.id)}
                    />
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {bk.category} · {formatInt(bk.hitCount30d)} lượt/30 ngày
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </section>
        </>
      )}

      {/* TAB 3 (MOCK): MEDIA BIẾN THỂ SẢN PHẨM */}
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

function AdminModerationRoute({ initialProducts, loadError }: AdminModerationClientProps) {
  return (
    <AdminStateGate
      title="Kiểm duyệt sản phẩm & nội dung"
      subtitle="Duyệt sản phẩm gian hàng mới gửi lên sàn và kiểm soát nội dung đánh giá, hỏi đáp, media biến thể."
      emptyTitle="Hàng đợi kiểm duyệt đang trống"
      emptyMessage="Tất cả sản phẩm, đánh giá, câu hỏi và media biến thể đã được kiểm duyệt hoàn tất."
      errorTitle="Không thể tải hàng đợi kiểm duyệt"
      errorMessage="Hệ thống kiểm duyệt nội dung tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery }) => (
        <ModerationContent
          initialProducts={initialProducts}
          loadError={loadError}
          initialQuery={initialQuery}
        />
      )}
    </AdminStateGate>
  );
}

export default function AdminModerationClient(props: AdminModerationClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Kiểm duyệt sản phẩm & nội dung" />}>
      <AdminModerationRoute {...props} />
    </Suspense>
  );
}
