"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Button,
  IconArrowLeft,
  IconCheck,
  IconChevronDown,
  IconPlus,
  IconSpark,
  StateView,
} from "@/components";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import { formatInt, formatVnd } from "@/lib/format";
import {
  normalizeDescribeProduct,
  type DescribeProductResult,
} from "@/features/assistant/assistantTypes";
import { useSellerContext } from "./SellerShell";
import { statusPillClass } from "./SellerUiPrimitives";
import {
  getSellerProductStatusMeta,
  type GwCategory,
  type GwSellerProduct,
  type GwSellerSku,
} from "./sellerApiTypes";
import { EMPTY_DASH, getAllowedSellerStatuses } from "./types";
import common from "./SellerCommon.module.css";
import styles from "./ProductFormView.module.css";

const GALLERY_CHOICES = [
  { label: "Áo thun Trắng", path: "/products/ao-thun-cotton-trang.png" },
  { label: "Áo thun Xám đá", path: "/products/ao-thun-cotton-xam-da.png" },
  { label: "Áo thun Xanh than", path: "/products/ao-thun-cotton-xanh-than.png" },
  { label: "Áo thun Dry-Fit", path: "/products/ao-thun-dryfit-co-vit.png" },
  { label: "Áo Polo Kem", path: "/products/ao-polo-pique-kem.png" },
  { label: "Áo Polo Xanh đá", path: "/products/ao-polo-pique-xanh-da.png" },
  { label: "Quần Short Be", path: "/products/quan-short-daily-be.png" },
  { label: "Quần Short Linen", path: "/products/quan-short-linen-xam-da.png" },
  { label: "Jogger Xám", path: "/products/quan-jogger-terry-xam.png" },
  { label: "Boxer Modal", path: "/products/do-lot-boxer-modal-3.png" },
  { label: "Tất cổ trung", path: "/products/tat-cotton-co-trung-5.png" },
  { label: "Áo khoác Gió", path: "/products/ao-khoac-gio-xam-tro.png" },
];

const AI_TONE_OPTIONS = [
  { value: "practical", label: "Gọn gàng, thực tế" },
  { value: "material", label: "Chi tiết chất liệu" },
  { value: "active", label: "Năng động, thể thao" },
] as const;

function buildDraftCopy(draft: DescribeProductResult): string {
  const parts: string[] = [];
  if (draft.description.trim()) {
    parts.push(draft.description.trim());
  }
  if (draft.highlights.length > 0) {
    const bulletBlock = draft.highlights.map((item) => `- ${item}`).join("\n");
    parts.push(`Điểm nổi bật:\n${bulletBlock}`);
  }
  return parts.join("\n\n");
}

function slugifyUrl(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .replace(/[^a-zA-Z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .toLowerCase();
}

const DEFAULT_WEIGHT_GRAMS = 250;
const MIN_WEIGHT_GRAMS = 1;

function clampWeightGrams(raw: string | number): number {
  const trimmed = typeof raw === "number" ? String(raw) : raw.trim();
  if (!trimmed) return DEFAULT_WEIGHT_GRAMS;
  const parsed = Number(trimmed);
  if (!Number.isFinite(parsed)) return DEFAULT_WEIGHT_GRAMS;
  return Math.max(MIN_WEIGHT_GRAMS, Math.trunc(parsed));
}

interface EditableSkuRow {
  localKey: string;
  id?: string;
  version?: number;
  reservedStock: number;
  skuCode: string;
  name: string;
  color: string;
  colorName: string;
  colorHex: string;
  size: string;
  priceVnd: number;
  stock: number;
  weightGrams: string;
  imageUrl: string;
}

function toEditableRows(
  product?: GwSellerProduct | null,
  defaultSkuStamp?: string
): EditableSkuRow[] {
  if (product && product.skus?.length > 0) {
    const activeSkus = product.skus.filter((s) => s.is_active);
    const sourceSkus = activeSkus.length > 0 ? activeSkus : product.skus;
    return sourceSkus.map((s) => ({
      localKey: s.id,
      id: s.id,
      version: s.version,
      reservedStock: s.reserved_stock,
      skuCode: s.sku_code,
      name: s.name || `${s.attributes?.color_name ?? "Mặc định"} / ${s.attributes?.size ?? "L"}`,
      color: s.attributes?.color ?? "mac-dinh",
      colorName: s.attributes?.color_name ?? s.attributes?.color ?? "Tiêu chuẩn",
      colorHex: s.attributes?.color_hex ?? "#9aa5ac",
      size: s.attributes?.size ?? "L",
      priceVnd: s.price_vnd,
      stock: s.stock,
      weightGrams: String(
        s.weight_grams > 0 ? clampWeightGrams(s.weight_grams) : DEFAULT_WEIGHT_GRAMS
      ),
      imageUrl: s.image_url || product.images?.[0] || GALLERY_CHOICES[0].path,
    }));
  }
  const sizes = ["S", "M", "L", "XL"];
  const stamp = defaultSkuStamp?.trim() || "1001";
  return sizes.map((sz) => ({
    localKey: `new-trang-${sz}`,
    reservedStock: 0,
    skuCode: `NM-${stamp}-TRANG-${sz}`,
    name: `Trắng tinh / ${sz}`,
    color: "trang",
    colorName: "Trắng tinh",
    colorHex: "#f4f6f7",
    size: sz,
    priceVnd: 189000,
    stock: 20,
    weightGrams: String(DEFAULT_WEIGHT_GRAMS),
    imageUrl: GALLERY_CHOICES[0].path,
  }));
}

export interface ProductFormViewProps {
  mode: "create" | "edit";
  initialProduct?: GwSellerProduct | null;
  categories: GwCategory[];
  loadError?: string | null;
  defaultSkuStamp?: string;
}

export function ProductFormView({
  mode,
  initialProduct,
  categories,
  loadError,
  defaultSkuStamp,
}: ProductFormViewProps) {
  const router = useRouter();
  const { shop, showToast } = useSellerContext();

  const [currentProduct, setCurrentProduct] = useState<GwSellerProduct | null>(
    initialProduct ?? null
  );
  const [productStatus, setProductStatus] = useState(initialProduct?.status ?? "PENDING");
  const [name, setName] = useState(initialProduct?.name ?? "");
  const [slug, setSlug] = useState(initialProduct?.slug ?? "");
  const [categoryId, setCategoryId] = useState(() => {
    if (initialProduct?.category_id) return initialProduct.category_id;
    const firstAllowed = categories.find((c) => !c.is_prohibited);
    return firstAllowed?.id ?? categories[0]?.id ?? "";
  });
  const [description, setDescription] = useState(
    initialProduct?.description ??
      "Sản phẩm đồ mặc hằng ngày hoàn thiện tại xưởng, kiểm soát độ co rút và giữ phom sau nhiều lần giặt."
  );
  const [selectedImage, setSelectedImage] = useState(
    initialProduct?.images?.[0] ?? GALLERY_CHOICES[0].path
  );
  const [skuRows, setSkuRows] = useState<EditableSkuRow[]>(() =>
    toEditableRows(initialProduct, defaultSkuStamp)
  );
  const [bulkStockStr, setBulkStockStr] = useState("20");
  const [newSkuSize, setNewSkuSize] = useState("2XL");
  const [newSkuColorName, setNewSkuColorName] = useState("Xám đá");
  const [saving, setSaving] = useState(false);
  const [busyStockId, setBusyStockId] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [savedNotice, setSavedNotice] = useState<string | null>(null);

  // AI description helper state (phase-0b)
  const [aiTone, setAiTone] = useState<string>("practical");
  const [aiLoading, setAiLoading] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  const [aiDraft, setAiDraft] = useState<DescribeProductResult | null>(null);

  const totalStock = useMemo(
    () => skuRows.reduce((s, r) => s + Math.max(0, Math.trunc(r.stock)), 0),
    [skuRows]
  );
  const lowSkuCount = useMemo(
    () => skuRows.filter((r) => Math.max(0, r.stock - r.reservedStock) <= 5).length,
    [skuRows]
  );
  const priceRangeLabel = useMemo(() => {
    if (skuRows.length === 0) return EMPTY_DASH;
    const minP = Math.min(...skuRows.map((r) => r.priceVnd));
    const maxP = Math.max(...skuRows.map((r) => r.priceVnd));
    return minP === maxP ? formatVnd(minP) : `${formatVnd(minP)} đến ${formatVnd(maxP)}`;
  }, [skuRows]);

  const activeCategoryName = useMemo(
    () => categories.find((c) => c.id === categoryId)?.name ?? "Chưa chọn ngành hàng",
    [categories, categoryId]
  );

  const activeImageLabel = useMemo(
    () => GALLERY_CHOICES.find((g) => g.path === selectedImage)?.label ?? "Ảnh sản phẩm",
    [selectedImage]
  );

  const previewColors = useMemo(() => {
    const map = new Map<string, string>();
    for (const row of skuRows) {
      const label = row.colorName.trim();
      if (label && !map.has(label)) {
        map.set(label, row.colorHex || "#9aa5ac");
      }
    }
    return Array.from(map.entries()).map(([label, hex]) => ({ label, hex }));
  }, [skuRows]);

  const previewSizes = useMemo(
    () => Array.from(new Set(skuRows.map((r) => r.size.trim()).filter(Boolean))),
    [skuRows]
  );

  const allowedStatuses = useMemo(
    () => getAllowedSellerStatuses(currentProduct?.status ?? productStatus),
    [currentProduct?.status, productStatus]
  );

  if (loadError) {
    return (
      <div className={common.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải thông tin sản phẩm"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  if (mode === "edit" && !currentProduct) {
    return (
      <div className={common.page}>
        <div className={common.pageHeader}>
          <div className={common.headerText}>
            <Link href="/seller/san-pham" className={styles.backLink}>
              <IconArrowLeft size={14} />
              <span>Sản phẩm</span>
            </Link>
            <h1 className={common.title}>Không tìm thấy sản phẩm</h1>
          </div>
        </div>
        <StateView
          kind="empty"
          surface="app"
          title="Mã sản phẩm không tồn tại trong gian hàng"
          message="Sản phẩm có thể đã bị gỡ hoặc không thuộc gian hàng của bạn."
          actionLabel="Về danh sách sản phẩm"
          actionHref="/seller/san-pham"
        />
      </div>
    );
  }

  const updateRow = (localKey: string, patch: Partial<EditableSkuRow>) => {
    setSkuRows((prev) => prev.map((r) => (r.localKey === localKey ? { ...r, ...patch } : r)));
  };

  const handleAddSkuRow = () => {
    const colorClean = newSkuColorName.trim() || "Mặc định";
    const sizeClean = newSkuSize.trim().toUpperCase() || "L";
    const colorSlug =
      colorClean
        .normalize("NFD")
        .replace(/[\u0300-\u036f]/g, "")
        .replace(/đ/gi, "d")
        .replace(/[^a-zA-Z0-9]+/g, "")
        .toLowerCase()
        .slice(0, 8) || "mau";
    const baseCode = `NM-${String(Date.now()).slice(-4)}-${colorSlug.toUpperCase()}-${sizeClean}`;
    const basePrice = skuRows[0]?.priceVnd ?? 189000;
    setSkuRows((prev) => [
      ...prev,
      {
        localKey: `new-${Date.now()}-${prev.length}`,
        reservedStock: 0,
        skuCode: baseCode,
        name: `${colorClean} / ${sizeClean}`,
        color: colorSlug,
        colorName: colorClean,
        colorHex: "#9aa5ac",
        size: sizeClean,
        priceVnd: basePrice,
        stock: 15,
        weightGrams: String(DEFAULT_WEIGHT_GRAMS),
        imageUrl: selectedImage,
      },
    ]);
  };

  const handleRemoveRow = (localKey: string) => {
    if (skuRows.length <= 1) return;
    setSkuRows((prev) => prev.filter((r) => r.localKey !== localKey));
  };

  const handleBulkStock = () => {
    const val = Math.max(0, Math.trunc(Number(bulkStockStr) || 0));
    setSkuRows((prev) => prev.map((r) => ({ ...r, stock: Math.max(r.reservedStock, val) })));
    showToast(`Đã đặt tồn kho ${formatInt(val)} chiếc cho ${skuRows.length} biến thể SKU.`);
  };

  const handleQuickSaveSkuStock = async (row: EditableSkuRow) => {
    if (!row.id || !row.version || busyStockId) return;
    setBusyStockId(row.id);
    setErrorMsg(null);
    try {
      const res = await api<{ sku: GwSellerSku }>(
        `/api/seller/skus/${encodeURIComponent(row.id)}/stock`,
        {
          method: "PATCH",
          body: {
            new_stock: Math.max(0, Math.trunc(row.stock)),
            expected_version: row.version,
          },
        }
      );
      const updated = res.sku;
      updateRow(row.localKey, {
        stock: updated.stock,
        reservedStock: updated.reserved_stock,
        version: updated.version,
      });
      showToast(
        `Đã lưu tồn kho ${updated.sku_code}: ${formatInt(updated.available_stock)} chiếc khả dụng.`
      );
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể cập nhật tồn kho SKU.");
      setErrorMsg(msg);
      showToast(msg);
    } finally {
      setBusyStockId(null);
    }
  };

  const handleGenerateDescription = async () => {
    const cleanName = name.trim();
    if (cleanName.length < 3) {
      setAiError("Tên sản phẩm cần tối thiểu 3 ký tự trước khi tạo mô tả bằng AI.");
      return;
    }
    setAiError(null);
    setAiLoading(true);
    try {
      const colors = Array.from(new Set(skuRows.map((r) => r.colorName.trim()).filter(Boolean)));
      const sizes = Array.from(new Set(skuRows.map((r) => r.size.trim()).filter(Boolean)));
      const prices = skuRows.map((r) => Math.trunc(r.priceVnd)).filter((p) => p > 0);
      const minPrice = prices.length > 0 ? Math.min(...prices) : 0;

      const raw = await api<unknown>("/api/seller/assistant/describe-product", {
        method: "POST",
        body: {
          name: cleanName,
          category_name: categories.find((c) => c.id === categoryId)?.name ?? "",
          attributes: {
            sku_count: String(skuRows.length),
          },
          colors,
          sizes,
          price_vnd: minPrice,
          tone: aiTone,
        },
      });
      const draft = normalizeDescribeProduct(raw);
      if (!draft.description && draft.highlights.length === 0) {
        setAiError(
          "AI chưa trả về nội dung mô tả phù hợp. Bạn thử bổ sung tên sản phẩm chi tiết hơn."
        );
        return;
      }
      setAiDraft(draft);
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setAiError(
          err.message ||
            "Bạn đã dùng hết lượt tạo mô tả AI trong ngày. Vui lòng thử lại vào ngày mai."
        );
      } else {
        setAiError(
          getErrorMessage(
            err,
            "Trợ lý viết mô tả sản phẩm đang tạm gián đoạn. Bạn vui lòng thử lại sau."
          )
        );
      }
    } finally {
      setAiLoading(false);
    }
  };

  const handleApplyAiDraft = (applyMode: "replace" | "append") => {
    if (!aiDraft) return;
    const text = buildDraftCopy(aiDraft);
    if (!text) return;
    if (applyMode === "replace") {
      setDescription(text);
      showToast("Đã áp dụng bản nháp AI vào mô tả.");
    } else {
      setDescription((prev) => (prev.trim() ? `${prev.trim()}\n\n${text}` : text));
      showToast("Đã chèn thêm bản nháp AI vào cuối mô tả.");
    }
    setAiDraft(null);
  };

  const handleDeleteProduct = async () => {
    if (!currentProduct || deleting) return;
    setDeleting(true);
    setErrorMsg(null);
    try {
      await api(`/api/seller/products/${encodeURIComponent(currentProduct.id)}`, {
        method: "DELETE",
      });
      showToast(`Đã xóa sản phẩm "${currentProduct.name}".`);
      router.push("/seller/san-pham");
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể xóa sản phẩm lúc này."));
    } finally {
      setDeleting(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const normalizedRows = skuRows.map((r) => ({
      ...r,
      weightGrams: String(clampWeightGrams(r.weightGrams)),
    }));
    if (normalizedRows.some((r, idx) => r.weightGrams !== skuRows[idx].weightGrams)) {
      setSkuRows(normalizedRows);
    }

    const cleanName = name.trim();
    if (cleanName.length < 3) {
      setErrorMsg("Tên sản phẩm cần tối thiểu 3 ký tự.");
      return;
    }
    if (!categoryId) {
      setErrorMsg("Vui lòng chọn danh mục sản phẩm.");
      return;
    }
    const chosenCat = categories.find((c) => c.id === categoryId);
    if (chosenCat?.is_prohibited) {
      setErrorMsg("Danh mục này thuộc danh mục cấm kinh doanh trên sàn.");
      return;
    }
    if (normalizedRows.length === 0) {
      setErrorMsg("Cần ít nhất 1 biến thể SKU để lưu sản phẩm.");
      return;
    }
    const seenCodes = new Set<string>();
    for (const r of normalizedRows) {
      const code = r.skuCode.trim();
      if (!code) {
        setErrorMsg("Mỗi biến thể cần có mã SKU không rỗng.");
        return;
      }
      if (seenCodes.has(code.toUpperCase())) {
        setErrorMsg(`Mã SKU "${code}" bị trùng lặp.`);
        return;
      }
      seenCodes.add(code.toUpperCase());
      if (r.priceVnd < 1000) {
        setErrorMsg(`Đơn giá của SKU ${code} phải là số nguyên tối thiểu 1.000₫.`);
        return;
      }
      if (r.stock < r.reservedStock) {
        setErrorMsg(
          `Tồn kho của SKU ${code} không được nhỏ hơn số lượng đang giữ (${r.reservedStock}).`
        );
        return;
      }
    }

    setErrorMsg(null);
    setSaving(true);
    try {
      const cleanSlug = slug.trim();
      const payload = {
        category_id: categoryId,
        name: cleanName,
        ...(cleanSlug ? { slug: cleanSlug } : {}),
        description: description.trim() || cleanName,
        images: Array.from(
          new Set([selectedImage, ...normalizedRows.map((r) => r.imageUrl).filter(Boolean)])
        ),
        status: mode === "edit" ? productStatus : undefined,
        skus: normalizedRows.map((r) => ({
          id: r.id,
          sku_code: r.skuCode.trim(),
          name: r.name.trim() || `${r.colorName} / ${r.size}`,
          attributes: {
            color: r.color,
            color_name: r.colorName,
            color_hex: r.colorHex,
            size: r.size,
          },
          price_vnd: Math.trunc(r.priceVnd),
          stock: Math.max(0, Math.trunc(r.stock)),
          weight_grams: clampWeightGrams(r.weightGrams),
          image_url: r.imageUrl || selectedImage,
        })),
      };

      if (mode === "create") {
        const res = await api<{ product: GwSellerProduct }>("/api/seller/products", {
          method: "POST",
          body: payload,
        });
        const msg = `Đã tạo sản phẩm "${res.product.name}" (${res.product.skus.length} SKU). Đang chờ quản trị duyệt hiển thị.`;
        showToast(msg);
        setSavedNotice(msg);
        router.push(`/seller/san-pham/${res.product.id}`);
      } else if (currentProduct) {
        const res = await api<{ product: GwSellerProduct }>(
          `/api/seller/products/${encodeURIComponent(currentProduct.id)}`,
          {
            method: "PUT",
            body: payload,
          }
        );
        setCurrentProduct(res.product);
        setSkuRows(toEditableRows(res.product));
        setProductStatus(res.product.status);
        setSlug(res.product.slug ?? "");
        const msg = `Đã lưu cập nhật "${res.product.name}" (${formatInt(totalStock)} tồn kho).`;
        showToast(msg);
        setSavedNotice(msg);
        router.refresh();
      }
    } catch (err) {
      // Preserves sanitized BFF messages for BANNED_CONTENT / PROHIBITED_CATEGORY
      const msg = getErrorMessage(err, "Không thể lưu sản phẩm lúc này.");
      setErrorMsg(msg);
      showToast(msg);
    } finally {
      setSaving(false);
    }
  };

  const statusMeta = getSellerProductStatusMeta(productStatus);

  return (
    <form className={common.page} onSubmit={(e) => void handleSubmit(e)} noValidate>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <Link href="/seller/san-pham" className={styles.backLink}>
            <IconArrowLeft size={14} />
            <span>Sản phẩm</span>
          </Link>
          <div className={styles.titleRow}>
            <h1 className={common.title}>
              {mode === "create" ? "Thêm sản phẩm mới" : currentProduct?.name}
            </h1>
            <span className={statusPillClass(statusMeta.tone)}>{statusMeta.label}</span>
          </div>
        </div>
        {mode === "edit" && currentProduct && (
          <div className={common.headerActions}>
            {confirmDelete ? (
              <>
                <Button
                  type="button"
                  variant="danger"
                  size="sm"
                  shape="app"
                  disabled={deleting}
                  onClick={() => void handleDeleteProduct()}
                >
                  {deleting ? "Đang xóa..." : "Xác nhận xóa"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={() => setConfirmDelete(false)}
                >
                  Giữ lại
                </Button>
              </>
            ) : (
              <Button
                type="button"
                variant="outline"
                size="sm"
                shape="app"
                onClick={() => setConfirmDelete(true)}
              >
                Xóa sản phẩm
              </Button>
            )}
          </div>
        )}
      </header>

      {savedNotice && (
        <div className={styles.noticeBar} role="status">
          <span>{savedNotice}</span>
          <Link href="/seller/san-pham" className={styles.noticeLink}>
            Về danh sách sản phẩm
          </Link>
        </div>
      )}

      {currentProduct?.status === "REJECTED" && currentProduct.rejection_reason && (
        <div className={styles.errorBar} role="alert">
          Sản phẩm bị từ chối: <strong>{currentProduct.rejection_reason}</strong>. Chỉnh sửa nội
          dung và chuyển trạng thái sang &ldquo;Gửi duyệt lại&rdquo;.
        </div>
      )}

      {Array.isArray(currentProduct?.flagged_keywords) &&
        currentProduct.flagged_keywords.length > 0 && (
          <div className={common.card} role="status">
            <p className={common.fieldHint}>
              Sản phẩm đang được gắn cờ kiểm duyệt thủ công do chứa từ khóa:{" "}
              <strong>{currentProduct.flagged_keywords.join(", ")}</strong>. Bạn nên rà soát lại tên
              và mô tả sản phẩm.
            </p>
          </div>
        )}

      {errorMsg && (
        <div className={styles.errorBar} role="alert">
          {errorMsg}
        </div>
      )}

      <div className={styles.workspace}>
        {/* Left Column: Collapsible Form Groups */}
        <div className={styles.formCol}>
          <details className={common.accordion} open>
            <summary className={common.accordionSummary}>
              <span className={styles.accordionTitleGroup}>
                <span>Thông tin &amp; Mô tả</span>
                <span className={common.accordionMeta}>{activeCategoryName}</span>
              </span>
              <span className={common.accordionChevron} aria-hidden="true">
                <IconChevronDown size={16} />
              </span>
            </summary>

            <div className={common.accordionBody}>
              <div className={common.field}>
                <label htmlFor="prod-name" className={common.fieldLabel}>
                  Tên sản phẩm
                </label>
                <input
                  id="prod-name"
                  type="text"
                  className={common.textInput}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Ví dụ: Áo thun Cotton Compact 220gsm Xanh đá"
                />
              </div>

              <div className={styles.fields2}>
                <div className={common.field}>
                  <label htmlFor="prod-category" className={common.fieldLabel}>
                    Ngành hàng
                  </label>
                  <select
                    id="prod-category"
                    className={common.textInput}
                    value={categoryId}
                    onChange={(e) => setCategoryId(e.target.value)}
                  >
                    {categories.map((cat) => (
                      <option key={cat.id} value={cat.id} disabled={Boolean(cat.is_prohibited)}>
                        {typeof cat.commission_rate_bps === "number" &&
                        Number.isFinite(cat.commission_rate_bps)
                          ? `${cat.name} (Hoa hồng ${(cat.commission_rate_bps / 100).toFixed(1)}%)`
                          : cat.name}
                        {cat.is_prohibited ? " — Danh mục cấm" : ""}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={common.field}>
                  <label htmlFor="prod-slug" className={common.fieldLabel}>
                    Đường dẫn tùy chỉnh (tùy chọn)
                  </label>
                  <input
                    id="prod-slug"
                    type="text"
                    className={common.textInput}
                    value={slug}
                    onChange={(e) => setSlug(e.target.value)}
                    placeholder={name ? slugifyUrl(name) : "tu-dong-tao-tu-ten-san-pham"}
                  />
                </div>
              </div>

              {mode === "edit" && (
                <div className={common.field}>
                  <label htmlFor="prod-status" className={common.fieldLabel}>
                    Trạng thái hiển thị
                  </label>
                  <select
                    id="prod-status"
                    className={common.textInput}
                    value={productStatus}
                    disabled={allowedStatuses.length <= 1}
                    onChange={(e) => setProductStatus(e.target.value)}
                  >
                    {allowedStatuses.map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                  <span className={common.fieldHint}>
                    Có thể ẩn/hiện sản phẩm đã duyệt hoặc gửi duyệt lại sản phẩm bị từ chối.
                  </span>
                </div>
              )}

              <div className={common.field}>
                <label htmlFor="prod-desc" className={common.fieldLabel}>
                  Mô tả chất liệu &amp; hoàn thiện
                </label>

                <div data-ui="ai-desc-bar" className={styles.fieldStack}>
                  <div className={styles.aiDescBar}>
                    <span className={styles.aiDescLead}>
                      <IconSpark size={14} />
                      <span>Gợi ý mô tả AI</span>
                    </span>
                    <div className={styles.aiDescControls}>
                      <select
                        className={styles.aiToneSelect}
                        value={aiTone}
                        onChange={(e) => setAiTone(e.target.value)}
                        disabled={aiLoading}
                        aria-label="Chọn văn phong mô tả AI"
                      >
                        {AI_TONE_OPTIONS.map((opt) => (
                          <option key={opt.value} value={opt.value}>
                            {opt.label}
                          </option>
                        ))}
                      </select>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        shape="app"
                        disabled={aiLoading}
                        onClick={() => void handleGenerateDescription()}
                      >
                        <IconSpark size={14} />
                        <span>{aiLoading ? "Đang tạo..." : "Tạo mô tả bằng AI"}</span>
                      </Button>
                    </div>
                  </div>

                  {aiError && (
                    <p className={styles.aiError} role="alert">
                      {aiError}
                    </p>
                  )}

                  {aiDraft && (
                    <div
                      className={styles.aiPreviewCard}
                      role="region"
                      aria-label="Bản nháp mô tả từ AI"
                    >
                      <div className={styles.aiPreviewHead}>
                        <span className={styles.aiPreviewTitle}>Bản nháp mô tả từ AI</span>
                        <span className={styles.aiPreviewNote}>Kiểm tra lại trước khi lưu</span>
                      </div>
                      {aiDraft.titleSuggestion && (
                        <p className={styles.aiPreviewSuggestionTitle}>
                          Gợi ý tiêu đề: {aiDraft.titleSuggestion}
                        </p>
                      )}
                      {aiDraft.description && (
                        <p className={styles.aiPreviewText}>{aiDraft.description}</p>
                      )}
                      {aiDraft.highlights.length > 0 && (
                        <ul className={styles.aiHighlightList}>
                          {aiDraft.highlights.map((item) => (
                            <li key={item}>{item}</li>
                          ))}
                        </ul>
                      )}
                      <div className={styles.aiPreviewActions}>
                        <Button
                          type="button"
                          variant="ink"
                          size="sm"
                          shape="app"
                          onClick={() => handleApplyAiDraft("replace")}
                        >
                          Áp dụng vào mô tả
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          shape="app"
                          onClick={() => handleApplyAiDraft("append")}
                        >
                          Chèn thêm
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          shape="app"
                          onClick={() => setAiDraft(null)}
                        >
                          Đóng
                        </Button>
                      </div>
                    </div>
                  )}
                </div>

                <textarea
                  id="prod-desc"
                  className={common.textArea}
                  rows={4}
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </div>
            </div>
          </details>

          <details className={common.accordion} open>
            <summary className={common.accordionSummary}>
              <span className={styles.accordionTitleGroup}>
                <span>Hình ảnh sản phẩm</span>
                <span className={common.accordionMeta}>{activeImageLabel}</span>
              </span>
              <span className={common.accordionChevron} aria-hidden="true">
                <IconChevronDown size={16} />
              </span>
            </summary>

            <div className={common.accordionBody}>
              <div className={styles.galleryGrid} role="radiogroup" aria-label="Chọn ảnh đại diện">
                {GALLERY_CHOICES.map((img) => {
                  const active = selectedImage === img.path;
                  return (
                    <button
                      key={img.path}
                      type="button"
                      role="radio"
                      aria-checked={active}
                      className={`${styles.thumbBtn} ${active ? styles.thumbBtnActive : ""}`}
                      onClick={() => setSelectedImage(img.path)}
                      title={img.label}
                      aria-label={`Chọn ảnh ${img.label}`}
                    >
                      <img
                        src={img.path}
                        alt={img.label}
                        className={styles.thumbImg}
                        width={96}
                        height={128}
                      />
                      {active && (
                        <span className={styles.thumbCheck} aria-hidden="true">
                          <IconCheck size={12} />
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
          </details>

          <details className={common.accordion} open>
            <summary className={common.accordionSummary}>
              <span className={styles.accordionTitleGroup}>
                <span>Ma trận biến thể SKU</span>
                <span className={common.accordionMeta}>
                  {formatInt(skuRows.length)} SKU, {formatInt(totalStock)} tồn
                </span>
              </span>
              <span className={common.accordionChevron} aria-hidden="true">
                <IconChevronDown size={16} />
              </span>
            </summary>

            <div className={common.accordionBody}>
              <div className={styles.bulkBarRow}>
                <span className={styles.bulkHint}>Đặt nhanh tồn kho đồng loạt</span>
                <div className={styles.bulkControls}>
                  <input
                    type="number"
                    min={0}
                    step={1}
                    className={styles.bulkInput}
                    value={bulkStockStr}
                    onChange={(e) => setBulkStockStr(e.target.value)}
                    aria-label="Số lượng áp dụng hàng loạt"
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={handleBulkStock}
                  >
                    Đặt tồn đồng loạt
                  </Button>
                </div>
              </div>

              <div className={`${common.tableWrap} ${styles.skuTableDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Mã SKU</th>
                      <th>Màu sắc</th>
                      <th>Cỡ</th>
                      <th className={common.numCol}>Giá (VND)</th>
                      <th className={common.numCol}>Tổng tồn</th>
                      <th className={common.numCol}>Giữ</th>
                      <th className={common.numCol}>Gram</th>
                      <th className={common.numCol}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {skuRows.map((row) => {
                      const lowAvail = Math.max(0, row.stock - row.reservedStock) <= 5;
                      return (
                        <tr key={row.localKey}>
                          <td>
                            <input
                              type="text"
                              className={styles.skuCodeInput}
                              value={row.skuCode}
                              onChange={(e) => updateRow(row.localKey, { skuCode: e.target.value })}
                              aria-label={`Mã SKU ${row.name}`}
                            />
                          </td>
                          <td>
                            <div className={styles.colorCellWrap}>
                              <span
                                className={styles.swatchDot}
                                style={{ backgroundColor: row.colorHex }}
                                aria-hidden="true"
                              />
                              <input
                                type="text"
                                className={styles.colorCellInput}
                                value={row.colorName}
                                onChange={(e) =>
                                  updateRow(row.localKey, {
                                    colorName: e.target.value,
                                    name: `${e.target.value} / ${row.size}`,
                                  })
                                }
                                aria-label={`Màu sắc ${row.skuCode}`}
                              />
                            </div>
                          </td>
                          <td>
                            <input
                              type="text"
                              className={styles.sizeCellInput}
                              value={row.size}
                              onChange={(e) =>
                                updateRow(row.localKey, {
                                  size: e.target.value.toUpperCase(),
                                  name: `${row.colorName} / ${e.target.value.toUpperCase()}`,
                                })
                              }
                              aria-label={`Kích cỡ ${row.skuCode}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="number"
                              min={1000}
                              step={1}
                              className={styles.priceCellInput}
                              value={row.priceVnd}
                              onChange={(e) =>
                                updateRow(row.localKey, {
                                  priceVnd: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                                })
                              }
                              aria-label={`Đơn giá ${row.skuCode}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <input
                              type="number"
                              min={row.reservedStock}
                              step={1}
                              className={`${styles.stockCellInput} ${lowAvail ? styles.stockLow : ""}`}
                              value={row.stock}
                              onChange={(e) =>
                                updateRow(row.localKey, {
                                  stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                                })
                              }
                              aria-label={`Tồn kho ${row.skuCode}`}
                            />
                          </td>
                          <td className={common.numCol}>{formatInt(row.reservedStock)}</td>
                          <td className={common.numCol}>
                            <input
                              type="number"
                              min={1}
                              step={1}
                              className={styles.weightCellInput}
                              value={row.weightGrams}
                              onChange={(e) =>
                                updateRow(row.localKey, { weightGrams: e.target.value })
                              }
                              onBlur={(e) =>
                                updateRow(row.localKey, {
                                  weightGrams: String(clampWeightGrams(e.currentTarget.value)),
                                })
                              }
                              aria-label={`Khối lượng ${row.skuCode}`}
                            />
                          </td>
                          <td className={common.numCol}>
                            <div className={styles.rowBtns}>
                              {mode === "edit" && row.id && row.version ? (
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  disabled={busyStockId === row.id}
                                  onClick={() => void handleQuickSaveSkuStock(row)}
                                >
                                  {busyStockId === row.id ? "Lưu..." : "Lưu tồn"}
                                </Button>
                              ) : null}
                              {skuRows.length > 1 && (
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="sm"
                                  shape="app"
                                  onClick={() => handleRemoveRow(row.localKey)}
                                >
                                  Xóa
                                </Button>
                              )}
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {/* Mobile SKU Editor Cards */}
              <div className={styles.mobileSkuList}>
                {skuRows.map((row) => {
                  const lowAvail = Math.max(0, row.stock - row.reservedStock) <= 5;
                  return (
                    <div key={row.localKey} className={styles.mobileSkuCard}>
                      <div className={styles.mobileSkuHead}>
                        <span className={styles.colorCellWrap}>
                          <span
                            className={styles.swatchDot}
                            style={{ backgroundColor: row.colorHex }}
                            aria-hidden="true"
                          />
                          <strong>
                            {row.colorName} / {row.size}
                          </strong>
                        </span>
                        <div className={styles.rowBtns}>
                          {mode === "edit" && row.id && row.version ? (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              shape="app"
                              disabled={busyStockId === row.id}
                              onClick={() => void handleQuickSaveSkuStock(row)}
                            >
                              {busyStockId === row.id ? "Lưu..." : "Lưu tồn"}
                            </Button>
                          ) : null}
                          {skuRows.length > 1 && (
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              shape="app"
                              onClick={() => handleRemoveRow(row.localKey)}
                            >
                              Xóa
                            </Button>
                          )}
                        </div>
                      </div>
                      <div className={styles.mobileSkuGrid}>
                        <label className={common.field}>
                          <span className={common.fieldLabel}>Mã SKU</span>
                          <input
                            type="text"
                            className={common.textInput}
                            value={row.skuCode}
                            onChange={(e) => updateRow(row.localKey, { skuCode: e.target.value })}
                          />
                        </label>
                        <div className={common.field}>
                          <span className={common.fieldLabel}>Màu / Cỡ</span>
                          <div className={styles.mobileSplit2}>
                            <input
                              type="text"
                              className={common.textInput}
                              value={row.colorName}
                              onChange={(e) =>
                                updateRow(row.localKey, {
                                  colorName: e.target.value,
                                  name: `${e.target.value} / ${row.size}`,
                                })
                              }
                              aria-label={`Màu sắc ${row.skuCode}`}
                            />
                            <input
                              type="text"
                              className={common.textInput}
                              value={row.size}
                              onChange={(e) =>
                                updateRow(row.localKey, {
                                  size: e.target.value.toUpperCase(),
                                  name: `${row.colorName} / ${e.target.value.toUpperCase()}`,
                                })
                              }
                              aria-label={`Kích cỡ ${row.skuCode}`}
                            />
                          </div>
                        </div>
                        <label className={common.field}>
                          <span className={common.fieldLabel}>Giá (VND)</span>
                          <input
                            type="number"
                            min={1000}
                            step={1}
                            className={common.textInput}
                            value={row.priceVnd}
                            onChange={(e) =>
                              updateRow(row.localKey, {
                                priceVnd: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                              })
                            }
                          />
                        </label>
                        <label className={common.field}>
                          <span className={common.fieldLabel}>
                            Tồn kho (giữ: {formatInt(row.reservedStock)})
                          </span>
                          <input
                            type="number"
                            min={row.reservedStock}
                            step={1}
                            className={`${common.textInput} ${lowAvail ? styles.stockLow : ""}`}
                            value={row.stock}
                            onChange={(e) =>
                              updateRow(row.localKey, {
                                stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                              })
                            }
                          />
                        </label>
                        <label className={common.field}>
                          <span className={common.fieldLabel}>Khối lượng (g)</span>
                          <input
                            type="number"
                            min={1}
                            step={1}
                            className={common.textInput}
                            value={row.weightGrams}
                            onChange={(e) =>
                              updateRow(row.localKey, { weightGrams: e.target.value })
                            }
                            onBlur={(e) =>
                              updateRow(row.localKey, {
                                weightGrams: String(clampWeightGrams(e.currentTarget.value)),
                              })
                            }
                            aria-label={`Khối lượng ${row.skuCode}`}
                          />
                        </label>
                      </div>
                    </div>
                  );
                })}
              </div>

              <div className={styles.addSkuRow}>
                <input
                  type="text"
                  className={styles.addSkuInput}
                  value={newSkuColorName}
                  onChange={(e) => setNewSkuColorName(e.target.value)}
                  placeholder="Tên màu mới"
                  aria-label="Tên màu cho SKU mới"
                />
                <select
                  className={common.filterSelect}
                  value={newSkuSize}
                  onChange={(e) => setNewSkuSize(e.target.value)}
                  aria-label="Chọn kích cỡ cho SKU mới"
                >
                  {["S", "M", "L", "XL", "2XL"].map((sz) => (
                    <option key={sz} value={sz}>
                      Size {sz}
                    </option>
                  ))}
                </select>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={handleAddSkuRow}
                >
                  <IconPlus size={14} />
                  <span>Thêm biến thể SKU</span>
                </Button>
              </div>
            </div>
          </details>
        </div>

        {/* Right Column: Sticky Storefront Product Card Preview */}
        <aside className={styles.previewCol} aria-label="Xem trước thẻ sản phẩm">
          <div className={styles.previewSticky}>
            <div className={styles.previewHeading}>Xem trước thẻ sản phẩm</div>

            <article className={styles.storeCard}>
              <div className={styles.storeCardMedia}>
                <img
                  src={selectedImage}
                  alt={name.trim() || "Ảnh sản phẩm"}
                  className={styles.storeCardImg}
                  width={320}
                  height={426}
                />
                {totalStock === 0 ? (
                  <span className={`${styles.storeStockBadge} ${styles.storeStockOut}`}>
                    Hết hàng
                  </span>
                ) : totalStock <= 10 ? (
                  <span className={styles.storeStockBadge}>Còn {formatInt(totalStock)}</span>
                ) : null}
              </div>

              <div className={styles.storeCardBody}>
                <span className={styles.storeCardCat}>{activeCategoryName}</span>
                <h2 className={styles.storeCardTitle}>
                  {name.trim() || "Tên sản phẩm hiển thị tại gian hàng"}
                </h2>
                <div className={`${styles.storeCardPrice} num`}>{priceRangeLabel}</div>

                {previewColors.length > 0 && (
                  <div className={styles.storeSwatches} aria-label="Màu sắc hiển thị">
                    {previewColors.slice(0, 6).map((c) => (
                      <span
                        key={c.label}
                        className={styles.storeSwatchItem}
                        title={c.label}
                        style={{ backgroundColor: c.hex }}
                      />
                    ))}
                    <span className={styles.storeSwatchCount}>
                      {formatInt(previewColors.length)} màu
                    </span>
                  </div>
                )}

                {previewSizes.length > 0 && (
                  <div className={styles.storeSizePills}>
                    {previewSizes.slice(0, 6).map((sz) => (
                      <span key={sz} className={styles.storeSizePill}>
                        {sz}
                      </span>
                    ))}
                  </div>
                )}

                <div className={styles.storeShopFoot}>{shop?.name ?? "Gian hàng NexusMart"}</div>
              </div>
            </article>

            <div className={styles.summaryCard}>
              <div className={styles.summaryRow}>
                <span className={styles.summaryKey}>Biến thể SKU</span>
                <span className={styles.summaryVal}>{formatInt(skuRows.length)}</span>
              </div>
              <div className={styles.summaryRow}>
                <span className={styles.summaryKey}>Tổng tồn kho</span>
                <span className={styles.summaryVal}>{formatInt(totalStock)} chiếc</span>
              </div>
              <div className={styles.summaryRow}>
                <span className={styles.summaryKey}>Khoảng giá</span>
                <span className={styles.summaryVal}>{priceRangeLabel}</span>
              </div>
              <div className={styles.summaryRow}>
                <span className={styles.summaryKey}>SKU dưới ngưỡng 5</span>
                <span
                  className={`${styles.summaryVal} ${lowSkuCount > 0 ? styles.summaryValWarn : ""}`}
                >
                  {formatInt(lowSkuCount)}
                </span>
              </div>
            </div>
          </div>
        </aside>
      </div>

      {/* Sticky Bottom Save Bar */}
      <div className={styles.stickyBar} role="region" aria-label="Thanh lưu sản phẩm">
        <div className={styles.stickySummary}>
          <span className="num">
            {formatInt(skuRows.length)} SKU, {formatInt(totalStock)} tồn
          </span>
          <span className="num">{priceRangeLabel}</span>
          {lowSkuCount > 0 && (
            <span className={styles.stickyWarn}>{formatInt(lowSkuCount)} SKU sắp hết</span>
          )}
        </div>
        <div className={styles.stickyActions}>
          <Button
            type="button"
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => router.push("/seller/san-pham")}
          >
            Hủy
          </Button>
          <Button type="submit" variant="ink" size="sm" shape="app" disabled={saving}>
            <IconCheck size={14} />
            <span>
              {saving ? "Đang lưu..." : mode === "create" ? "Đăng bán sản phẩm" : "Lưu thay đổi"}
            </span>
          </Button>
        </div>
      </div>
    </form>
  );
}
