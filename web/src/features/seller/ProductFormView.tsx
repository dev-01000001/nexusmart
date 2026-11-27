"use client";

import React, { useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Image as ImageIcon,
  Package,
  Plus,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import {
  DetailLayout,
  EmptyState,
  ErrorState,
  MediaUploader,
  PageHeader,
  SaveBar,
  StatusBadge,
  productStatusTone,
} from "@/components/app";
import { MEDIA_MAX_PRODUCT_IMAGES, appendUniqueMediaUrl } from "@/components/app/mediaUploadUtils";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError, getErrorMessage } from "@/lib/api";
import { formatInt, formatVnd } from "@/lib/format";
import { formatVariantLabel, normalizeVariantEntries } from "@/lib/formatVariant";
import { cn } from "@/lib/utils";
import {
  normalizeDescribeProduct,
  type DescribeProductResult,
} from "@/features/assistant/assistantTypes";
import { useSellerContext } from "./SellerShell";
import {
  buildSkuPayloadName,
  clampWeightGrams,
  DEFAULT_VARIANT_NAME,
  generateVariantCombinations,
  MAX_VARIANT_COMBINATIONS,
  MAX_VARIANT_OPTIONS,
  OPTION_NAME_SUGGESTIONS,
  parseOptionValuesInput,
  toEditableFormState,
  type EditableSkuRow,
  type VariantOptionDraft,
} from "./ProductFormView.variants";
import {
  getSellerProductStatusMeta,
  type GwCategory,
  type GwSellerProduct,
  type GwSellerSku,
} from "./sellerApiTypes";
import { EMPTY_DASH, getAllowedSellerStatuses } from "./types";

const SAMPLE_GALLERY_CHOICES = [
  { label: "Mẫu minh họa 1", path: "/products/ao-thun-cotton-trang.png" },
  { label: "Mẫu minh họa 2", path: "/products/ao-thun-cotton-xam-da.png" },
  { label: "Mẫu minh họa 3", path: "/products/ao-thun-cotton-xanh-than.png" },
  { label: "Mẫu minh họa 4", path: "/products/ao-thun-dryfit-co-vit.png" },
  { label: "Mẫu minh họa 5", path: "/products/ao-polo-pique-kem.png" },
  { label: "Mẫu minh họa 6", path: "/products/ao-polo-pique-xanh-da.png" },
  { label: "Mẫu minh họa 7", path: "/products/quan-short-daily-be.png" },
  { label: "Mẫu minh họa 8", path: "/products/quan-short-linen-xam-da.png" },
  { label: "Mẫu minh họa 9", path: "/products/quan-jogger-terry-xam.png" },
  { label: "Mẫu minh họa 10", path: "/products/do-lot-boxer-modal-3.png" },
  { label: "Mẫu minh họa 11", path: "/products/tat-cotton-co-trung-5.png" },
  { label: "Mẫu minh họa 12", path: "/products/ao-khoac-gio-xam-tro.png" },
];

const AI_TONE_OPTIONS = [
  { value: "practical", label: "Gọn gàng, thực tế" },
  { value: "material", label: "Chi tiết chất liệu & thông số" },
  { value: "active", label: "Nổi bật công dụng & trải nghiệm" },
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

const SKU_IMAGE_PRIMARY_VALUE = "__primary__";

interface ProductFormSnapshot {
  productStatus: string;
  name: string;
  slug: string;
  categoryId: string;
  description: string;
  images: string[];
  options: VariantOptionDraft[];
  skuRows: EditableSkuRow[];
}

function serializeSnapshot(snap: ProductFormSnapshot): string {
  return JSON.stringify({
    productStatus: snap.productStatus,
    name: snap.name,
    slug: snap.slug,
    categoryId: snap.categoryId,
    description: snap.description,
    images: snap.images,
    options: snap.options.map((o) => ({
      name: o.name,
      storageKey: o.storageKey ?? "",
      values: o.values,
    })),
    skuRows: snap.skuRows.map((r) => ({
      id: r.id ?? "",
      skuCode: r.skuCode,
      name: r.name,
      attributes: r.attributes,
      priceVnd: r.priceVnd,
      stock: r.stock,
      weightGrams: r.weightGrams,
      imageUrl: r.imageUrl,
    })),
  });
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
  const formRef = useRef<HTMLFormElement | null>(null);
  const skuStamp = defaultSkuStamp?.trim() || "1001";

  const initialCategoryId = useMemo(() => {
    if (initialProduct?.category_id) return initialProduct.category_id;
    const firstAllowed = categories.find((c) => !c.is_prohibited);
    return firstAllowed?.id ?? categories[0]?.id ?? "";
  }, [initialProduct?.category_id, categories]);

  const initialSnapshot = useMemo<ProductFormSnapshot>(() => {
    const { options, skuRows } = toEditableFormState(initialProduct, skuStamp);
    const initialImages = Array.isArray(initialProduct?.images)
      ? Array.from(
          new Set(
            initialProduct.images
              .filter((u): u is string => typeof u === "string" && u.trim() !== "")
              .map((u) => u.trim())
          )
        ).slice(0, MEDIA_MAX_PRODUCT_IMAGES)
      : [];
    return {
      productStatus: initialProduct?.status ?? "PENDING",
      name: initialProduct?.name ?? "",
      slug: initialProduct?.slug ?? "",
      categoryId: initialCategoryId,
      description: initialProduct?.description ?? "",
      images: initialImages,
      options,
      skuRows,
    };
  }, [initialProduct, initialCategoryId, skuStamp]);

  const [savedSnapshot, setSavedSnapshot] = useState<ProductFormSnapshot>(initialSnapshot);
  const [currentProduct, setCurrentProduct] = useState<GwSellerProduct | null>(
    initialProduct ?? null
  );
  const [productStatus, setProductStatus] = useState(initialSnapshot.productStatus);
  const [name, setName] = useState(initialSnapshot.name);
  const [slug, setSlug] = useState(initialSnapshot.slug);
  const [categoryId, setCategoryId] = useState(initialSnapshot.categoryId);
  const [description, setDescription] = useState(initialSnapshot.description);
  const [images, setImages] = useState<string[]>(initialSnapshot.images);
  const primaryImage = images[0] ?? "";
  const [showSampleGallery, setShowSampleGallery] = useState(false);
  const [options, setOptions] = useState<VariantOptionDraft[]>(initialSnapshot.options);
  const [optionValueInputs, setOptionValueInputs] = useState<Record<string, string>>({});
  const [skuRows, setSkuRows] = useState<EditableSkuRow[]>(initialSnapshot.skuRows);
  const [combinationsTruncated, setCombinationsTruncated] = useState(false);

  const [bulkPriceStr, setBulkPriceStr] = useState(() => {
    const firstPrice = initialSnapshot.skuRows[0]?.priceVnd ?? 0;
    return firstPrice > 0 ? String(firstPrice) : "";
  });
  const [bulkStockStr, setBulkStockStr] = useState("");
  const [saving, setSaving] = useState(false);
  const [busyStockId, setBusyStockId] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [savedNotice, setSavedNotice] = useState<string | null>(null);

  // AI description helper state
  const [aiTone, setAiTone] = useState<string>("practical");
  const [aiLoading, setAiLoading] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  const [aiDraft, setAiDraft] = useState<DescribeProductResult | null>(null);

  const isDirty = useMemo(() => {
    const current: ProductFormSnapshot = {
      productStatus,
      name,
      slug,
      categoryId,
      description,
      images,
      options,
      skuRows,
    };
    return serializeSnapshot(current) !== serializeSnapshot(savedSnapshot);
  }, [productStatus, name, slug, categoryId, description, images, options, skuRows, savedSnapshot]);

  const totalStock = useMemo(
    () => skuRows.reduce((s, r) => s + Math.max(0, Math.trunc(r.stock)), 0),
    [skuRows]
  );
  const lowSkuCount = useMemo(
    () => skuRows.filter((r) => Math.max(0, r.stock - r.reservedStock) <= 5).length,
    [skuRows]
  );
  const priceRangeLabel = useMemo(() => {
    const validPrices = skuRows.map((r) => r.priceVnd).filter((p) => p > 0);
    if (validPrices.length === 0) return EMPTY_DASH;
    const minP = Math.min(...validPrices);
    const maxP = Math.max(...validPrices);
    return minP === maxP ? formatVnd(minP) : `${formatVnd(minP)} đến ${formatVnd(maxP)}`;
  }, [skuRows]);

  const activeCategoryName = useMemo(
    () => categories.find((c) => c.id === categoryId)?.name ?? "Chưa chọn ngành hàng",
    [categories, categoryId]
  );

  const activeImageLabel = useMemo(() => {
    if (images.length === 0) return "Tải tối đa 9 ảnh (JPEG, PNG, WebP ≤ 5 MB)";
    return `${formatInt(images.length)}/${MEDIA_MAX_PRODUCT_IMAGES} ảnh · Ảnh đầu tiên là ảnh chính hiển thị trên gian hàng`;
  }, [images]);

  const previewOptionSummary = useMemo(() => {
    const byLabel = new Map<string, Set<string>>();
    for (const row of skuRows) {
      for (const entry of normalizeVariantEntries(row.attributes)) {
        let vals = byLabel.get(entry.label);
        if (!vals) {
          vals = new Set<string>();
          byLabel.set(entry.label, vals);
        }
        vals.add(entry.value);
      }
    }
    return Array.from(byLabel.entries()).map(([label, vals]) => ({
      label,
      values: Array.from(vals),
    }));
  }, [skuRows]);

  const allowedStatuses = useMemo(
    () => getAllowedSellerStatuses(currentProduct?.status ?? productStatus),
    [currentProduct?.status, productStatus]
  );

  const clearFieldError = (key: string) => {
    setFieldErrors((prev) => {
      if (!(key in prev)) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const focusErrorField = (key: string) => {
    window.requestAnimationFrame(() => {
      const root = formRef.current ?? document;
      const candidates = root.querySelectorAll<HTMLElement>(`[data-error-key="${key}"]`);
      for (const el of Array.from(candidates)) {
        if (el.offsetParent !== null) {
          el.focus();
          el.scrollIntoView({ block: "center", behavior: "smooth" });
          return;
        }
      }
      const fallback = candidates[0];
      if (fallback) {
        fallback.focus();
        fallback.scrollIntoView({ block: "center", behavior: "smooth" });
      }
    });
  };

  if (loadError) {
    return (
      <div className="space-y-4">
        <PageHeader
          title={mode === "create" ? "Thêm sản phẩm mới" : "Chỉnh sửa sản phẩm"}
          backHref="/seller/san-pham"
        />
        <ErrorState
          title="Không thể tải thông tin sản phẩm"
          description={loadError}
          onRetry={() => router.refresh()}
        />
      </div>
    );
  }

  if (mode === "edit" && !currentProduct) {
    return (
      <div className="space-y-4">
        <PageHeader title="Không tìm thấy sản phẩm" backHref="/seller/san-pham" />
        <EmptyState
          icon={Package}
          title="Mã sản phẩm không tồn tại trong gian hàng"
          description="Sản phẩm có thể đã bị gỡ hoặc không thuộc gian hàng của bạn."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/san-pham">Về danh sách sản phẩm</Link>
            </Button>
          }
        />
      </div>
    );
  }

  const handleImagesChange = (nextImages: string[]) => {
    const kept = new Set(nextImages);
    setImages(nextImages);
    setSkuRows((prev) =>
      prev.map((row) => (row.imageUrl && !kept.has(row.imageUrl) ? { ...row, imageUrl: "" } : row))
    );
  };

  const handleToggleSampleImage = (samplePath: string) => {
    if (images.includes(samplePath)) {
      handleImagesChange(images.filter((u) => u !== samplePath));
      return;
    }
    if (images.length >= MEDIA_MAX_PRODUCT_IMAGES) {
      showToast(`Tối đa ${MEDIA_MAX_PRODUCT_IMAGES} ảnh cho mỗi sản phẩm.`);
      return;
    }
    handleImagesChange(appendUniqueMediaUrl(images, samplePath, MEDIA_MAX_PRODUCT_IMAGES));
  };

  const regenerateRows = (nextOptions: VariantOptionDraft[], currentRows: EditableSkuRow[]) => {
    const result = generateVariantCombinations({
      options: nextOptions,
      existingRows: currentRows,
      skuStamp,
      defaultImageUrl: primaryImage,
    });
    setSkuRows(result.rows);
    setCombinationsTruncated(result.truncated);
    clearFieldError("skus");
  };

  const handleAddOption = (presetName = "") => {
    if (options.length >= MAX_VARIANT_OPTIONS) return;
    const newId = `opt-${Date.now()}-${options.length}`;
    const nextOptions: VariantOptionDraft[] = [
      ...options,
      { id: newId, name: presetName, values: [] },
    ];
    setOptions(nextOptions);
  };

  const handleUpdateOptionName = (optId: string, nextName: string) => {
    const nextOptions = options.map((opt) => (opt.id === optId ? { ...opt, name: nextName } : opt));
    setOptions(nextOptions);
    if (nextOptions.some((o) => o.id === optId && o.values.length > 0)) {
      regenerateRows(nextOptions, skuRows);
    }
  };

  const handleAddOptionValues = (optId: string) => {
    const raw = (optionValueInputs[optId] ?? "").trim();
    if (!raw) return;
    const nextOptions = options.map((opt) => {
      if (opt.id !== optId) return opt;
      return {
        ...opt,
        values: parseOptionValuesInput(raw, opt.values),
      };
    });
    setOptions(nextOptions);
    setOptionValueInputs((prev) => ({ ...prev, [optId]: "" }));
    regenerateRows(nextOptions, skuRows);
  };

  const handleRemoveOptionValue = (optId: string, valueToRemove: string) => {
    const nextOptions = options.map((opt) =>
      opt.id === optId ? { ...opt, values: opt.values.filter((v) => v !== valueToRemove) } : opt
    );
    setOptions(nextOptions);
    regenerateRows(nextOptions, skuRows);
  };

  const handleRemoveOption = (optId: string) => {
    const nextOptions = options.filter((opt) => opt.id !== optId);
    setOptions(nextOptions);
    setOptionValueInputs((prev) => {
      const copy = { ...prev };
      delete copy[optId];
      return copy;
    });
    regenerateRows(nextOptions, skuRows);
  };

  const updateRow = (localKey: string, patch: Partial<EditableSkuRow>) => {
    setSkuRows((prev) => prev.map((r) => (r.localKey === localKey ? { ...r, ...patch } : r)));
  };

  const handleDiscard = () => {
    setProductStatus(savedSnapshot.productStatus);
    setName(savedSnapshot.name);
    setSlug(savedSnapshot.slug);
    setCategoryId(savedSnapshot.categoryId);
    setDescription(savedSnapshot.description);
    setImages(savedSnapshot.images);
    setOptions(savedSnapshot.options);
    setSkuRows(savedSnapshot.skuRows);
    setOptionValueInputs({});
    setCombinationsTruncated(false);
    setFieldErrors({});
    setErrorMsg(null);
    showToast("Đã hoàn tác các thay đổi chưa lưu.");
  };

  const handleRemoveRow = (localKey: string) => {
    if (skuRows.length <= 1) return;
    setSkuRows((prev) => prev.filter((r) => r.localKey !== localKey));
  };

  const handleBulkPrice = () => {
    const val = Math.trunc(Number(bulkPriceStr) || 0);
    if (val < 1000) {
      setFieldErrors((prev) => ({
        ...prev,
        bulkPrice: "Đơn giá áp dụng hàng loạt tối thiểu 1.000₫.",
      }));
      focusErrorField("bulkPrice");
      return;
    }
    clearFieldError("bulkPrice");
    setSkuRows((prev) => prev.map((r) => ({ ...r, priceVnd: val })));
    setFieldErrors((prev) => {
      const next = { ...prev };
      for (const k of Object.keys(next)) {
        if (k.startsWith("sku-price-")) delete next[k];
      }
      return next;
    });
    showToast(`Đã đặt giá ${formatVnd(val)} cho ${skuRows.length} biến thể SKU.`);
  };

  const handleBulkStock = () => {
    const val = Math.max(0, Math.trunc(Number(bulkStockStr) || 0));
    setSkuRows((prev) => prev.map((r) => ({ ...r, stock: Math.max(r.reservedStock, val) })));
    showToast(`Đã đặt tồn kho ${formatInt(val)} sản phẩm cho ${skuRows.length} biến thể SKU.`);
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
      setSavedSnapshot((prev) => ({
        ...prev,
        skuRows: prev.skuRows.map((r) =>
          r.localKey === row.localKey
            ? {
                ...r,
                stock: updated.stock,
                reservedStock: updated.reserved_stock,
                version: updated.version,
              }
            : r
        ),
      }));
      showToast(
        `Đã lưu tồn kho ${updated.sku_code}: ${formatInt(updated.available_stock)} sản phẩm khả dụng.`
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
      focusErrorField("name");
      return;
    }
    setAiError(null);
    setAiLoading(true);
    try {
      const colors: string[] = [];
      const sizes: string[] = [];
      const attrSummary: Record<string, string> = { sku_count: String(skuRows.length) };

      for (const opt of previewOptionSummary) {
        attrSummary[opt.label] = opt.values.join(", ");
        if (opt.label === "Màu sắc") colors.push(...opt.values);
        if (opt.label === "Kích cỡ") sizes.push(...opt.values);
      }

      const prices = skuRows.map((r) => Math.trunc(r.priceVnd)).filter((p) => p > 0);
      const minPrice = prices.length > 0 ? Math.min(...prices) : 0;

      const raw = await api<unknown>("/api/seller/assistant/describe-product", {
        method: "POST",
        body: {
          name: cleanName,
          category_name: categories.find((c) => c.id === categoryId)?.name ?? "",
          attributes: attrSummary,
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

  const submitProduct = async () => {
    if (saving) return;
    const normalizedRows = skuRows.map((r) => ({
      ...r,
      weightGrams: String(clampWeightGrams(r.weightGrams)),
    }));
    if (normalizedRows.some((r, idx) => r.weightGrams !== skuRows[idx].weightGrams)) {
      setSkuRows(normalizedRows);
    }

    const nextErrors: Record<string, string> = {};
    const errorOrder: string[] = [];
    const pushErr = (key: string, msg: string) => {
      if (!nextErrors[key]) {
        nextErrors[key] = msg;
        errorOrder.push(key);
      }
    };

    const cleanName = name.trim();
    if (cleanName.length < 3) {
      pushErr("name", "Tên sản phẩm cần tối thiểu 3 ký tự.");
    }
    if (!categoryId) {
      pushErr("categoryId", "Vui lòng chọn danh mục sản phẩm.");
    } else {
      const chosenCat = categories.find((c) => c.id === categoryId);
      if (chosenCat?.is_prohibited) {
        pushErr("categoryId", "Danh mục này thuộc danh mục cấm kinh doanh trên sàn.");
      }
    }
    if (normalizedRows.length === 0) {
      pushErr("skus", "Cần ít nhất 1 biến thể SKU để lưu sản phẩm.");
    }

    const seenCodes = new Set<string>();
    normalizedRows.forEach((r, idx) => {
      const code = r.skuCode.trim();
      if (!code) {
        pushErr(`sku-code-${idx}`, "Mã SKU không được để trống.");
      } else if (seenCodes.has(code.toUpperCase())) {
        pushErr(`sku-code-${idx}`, `Mã SKU "${code}" bị trùng lặp.`);
      } else {
        seenCodes.add(code.toUpperCase());
      }
      if (r.priceVnd < 1000) {
        pushErr(`sku-price-${idx}`, "Đơn giá tối thiểu 1.000₫.");
      }
      if (r.stock < r.reservedStock) {
        pushErr(
          `sku-stock-${idx}`,
          `Tồn kho không nhỏ hơn đang giữ (${formatInt(r.reservedStock)}).`
        );
      }
    });

    if (errorOrder.length > 0) {
      setFieldErrors(nextErrors);
      setErrorMsg(nextErrors[errorOrder[0]]);
      focusErrorField(errorOrder[0]);
      return;
    }

    setFieldErrors({});
    setErrorMsg(null);
    setSaving(true);
    try {
      const cleanSlug = slug.trim();
      const imageList = Array.from(
        new Set([...images, ...normalizedRows.map((r) => r.imageUrl)].filter(Boolean))
      );
      const payload = {
        category_id: categoryId,
        name: cleanName,
        ...(cleanSlug ? { slug: cleanSlug } : {}),
        description: description.trim(),
        images: imageList,
        status: mode === "edit" ? productStatus : undefined,
        skus: normalizedRows.map((r) => ({
          id: r.id,
          sku_code: r.skuCode.trim(),
          name: buildSkuPayloadName(r.attributes, r.name),
          attributes: r.attributes,
          price_vnd: Math.trunc(r.priceVnd),
          stock: Math.max(0, Math.trunc(r.stock)),
          weight_grams: clampWeightGrams(r.weightGrams),
          image_url: r.imageUrl || primaryImage || "",
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
        const hydrated = toEditableFormState(res.product, skuStamp);
        const savedImages = Array.isArray(res.product.images)
          ? res.product.images.filter((u): u is string => typeof u === "string" && u.trim() !== "")
          : [];
        const nextSnap: ProductFormSnapshot = {
          productStatus: res.product.status,
          name: res.product.name,
          slug: res.product.slug ?? "",
          categoryId: res.product.category_id,
          description: res.product.description ?? "",
          images: savedImages,
          options: hydrated.options,
          skuRows: hydrated.skuRows,
        };
        setCurrentProduct(res.product);
        setSavedSnapshot(nextSnap);
        setOptions(nextSnap.options);
        setSkuRows(nextSnap.skuRows);
        setProductStatus(nextSnap.productStatus);
        setName(nextSnap.name);
        setSlug(nextSnap.slug);
        setCategoryId(nextSnap.categoryId);
        setDescription(nextSnap.description);
        setImages(nextSnap.images);
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

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    await submitProduct();
  };

  const statusMeta = getSellerProductStatusMeta(productStatus);
  const usedOptionNamesLower = new Set(options.map((o) => o.name.trim().toLowerCase()));

  return (
    <form ref={formRef} className="space-y-4" onSubmit={(e) => void handleSubmit(e)} noValidate>
      <SaveBar
        visible={isDirty}
        saving={saving}
        message={mode === "create" ? "Sản phẩm mới chưa lưu" : "Thay đổi chưa lưu"}
        onDiscard={handleDiscard}
        onSave={() => void submitProduct()}
      />

      <PageHeader
        title={mode === "create" ? "Thêm sản phẩm mới" : currentProduct?.name || "Sản phẩm"}
        backHref="/seller/san-pham"
        badges={
          <StatusBadge tone={productStatusTone(productStatus)}>{statusMeta.label}</StatusBadge>
        }
        subtitle={`${activeCategoryName} · ${formatInt(skuRows.length)} SKU · ${formatInt(totalStock)} tồn kho`}
        secondaryActions={
          mode === "edit" && currentProduct ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={() => setConfirmDelete(true)}
            >
              Xóa sản phẩm
            </Button>
          ) : undefined
        }
        primaryAction={
          <Button type="submit" size="sm" disabled={saving}>
            <Check className="size-4" aria-hidden="true" />
            <span>
              {saving ? "Đang lưu..." : mode === "create" ? "Đăng bán sản phẩm" : "Lưu thay đổi"}
            </span>
          </Button>
        }
      />

      {savedNotice && (
        <Alert role="status">
          <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2 text-foreground">
            <span>{savedNotice}</span>
            <Link href="/seller/san-pham" className="font-medium text-brand hover:underline">
              Về danh sách sản phẩm
            </Link>
          </AlertDescription>
        </Alert>
      )}

      {currentProduct?.status === "REJECTED" && currentProduct.rejection_reason && (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertTitle>Sản phẩm bị từ chối</AlertTitle>
          <AlertDescription>
            <p>
              <strong>{currentProduct.rejection_reason}</strong>. Chỉnh sửa nội dung và chuyển trạng
              thái sang &ldquo;Gửi duyệt lại&rdquo;.
            </p>
          </AlertDescription>
        </Alert>
      )}

      {Array.isArray(currentProduct?.flagged_keywords) &&
        currentProduct.flagged_keywords.length > 0 && (
          <Alert role="status">
            <AlertTriangle className="size-4" aria-hidden="true" />
            <AlertTitle>Cần rà soát nội dung kiểm duyệt</AlertTitle>
            <AlertDescription>
              <p>
                Sản phẩm đang được gắn cờ kiểm duyệt thủ công do chứa từ khóa:{" "}
                <strong className="text-foreground">
                  {currentProduct.flagged_keywords.join(", ")}
                </strong>
                . Bạn nên rà soát lại tên và mô tả sản phẩm.
              </p>
            </AlertDescription>
          </Alert>
        )}

      {errorMsg && (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <DetailLayout
        main={
          <>
            {/* Card 1: Thông tin */}
            <Card aria-labelledby="prod-info-heading">
              <CardHeader>
                <CardTitle id="prod-info-heading">Thông tin</CardTitle>
                <CardDescription>
                  Tên hiển thị và mô tả chi tiết sản phẩm trên site gian hàng.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <FieldGroup className="gap-4">
                  <Field data-invalid={Boolean(fieldErrors.name) || undefined}>
                    <FieldLabel htmlFor="prod-name">Tên sản phẩm</FieldLabel>
                    <Input
                      id="prod-name"
                      type="text"
                      data-error-key="name"
                      value={name}
                      onChange={(e) => {
                        setName(e.target.value);
                        clearFieldError("name");
                        if (errorMsg) setErrorMsg(null);
                      }}
                      placeholder="Ví dụ: Bộ ấm trà gốm men lam, Kem dưỡng ẩm, Cà phê rang xay..."
                      aria-invalid={Boolean(fieldErrors.name)}
                    />
                    {fieldErrors.name ? <FieldError>{fieldErrors.name}</FieldError> : null}
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="prod-desc">Mô tả sản phẩm</FieldLabel>

                    <div className="space-y-2.5 rounded-lg border border-border bg-muted/40 p-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-foreground">
                          <Sparkles className="size-3.5 text-brand" aria-hidden="true" />
                          <span>Gợi ý mô tả AI</span>
                        </span>
                        <div className="flex flex-wrap items-center gap-2">
                          <Select value={aiTone} onValueChange={setAiTone} disabled={aiLoading}>
                            <SelectTrigger
                              size="sm"
                              aria-label="Chọn văn phong mô tả AI"
                              className="w-52 bg-card"
                            >
                              <SelectValue>
                                {AI_TONE_OPTIONS.find((opt) => opt.value === aiTone)?.label ??
                                  aiTone}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent>
                              {AI_TONE_OPTIONS.map((opt) => (
                                <SelectItem key={opt.value} value={opt.value}>
                                  {opt.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={aiLoading}
                            onClick={() => void handleGenerateDescription()}
                          >
                            <Sparkles className="size-3.5" aria-hidden="true" />
                            <span>{aiLoading ? "Đang tạo..." : "Tạo mô tả bằng AI"}</span>
                          </Button>
                        </div>
                      </div>

                      {aiError ? <FieldError>{aiError}</FieldError> : null}

                      {aiDraft && (
                        <div
                          className="space-y-2.5 rounded-lg border border-border bg-card p-3"
                          role="region"
                          aria-label="Bản nháp mô tả từ AI"
                        >
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="text-xs font-semibold text-foreground">
                              Bản nháp mô tả từ AI
                            </span>
                            <span className="text-xs text-muted-foreground">
                              Kiểm tra lại trước khi lưu
                            </span>
                          </div>
                          {aiDraft.titleSuggestion && (
                            <p className="text-xs font-medium text-brand">
                              Gợi ý tiêu đề: {aiDraft.titleSuggestion}
                            </p>
                          )}
                          {aiDraft.description && (
                            <p className="text-[13px] leading-5 text-foreground">
                              {aiDraft.description}
                            </p>
                          )}
                          {aiDraft.highlights.length > 0 && (
                            <ul className="list-disc space-y-1 pl-4 text-[13px] text-muted-foreground">
                              {aiDraft.highlights.map((item) => (
                                <li key={item}>{item}</li>
                              ))}
                            </ul>
                          )}
                          <div className="flex flex-wrap items-center gap-2 pt-1">
                            <Button
                              type="button"
                              size="xs"
                              onClick={() => handleApplyAiDraft("replace")}
                            >
                              Áp dụng vào mô tả
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              onClick={() => handleApplyAiDraft("append")}
                            >
                              Chèn thêm
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="xs"
                              onClick={() => setAiDraft(null)}
                            >
                              Đóng
                            </Button>
                          </div>
                        </div>
                      )}
                    </div>

                    <Textarea
                      id="prod-desc"
                      rows={4}
                      value={description}
                      onChange={(e) => setDescription(e.target.value)}
                      placeholder="Nhập thông tin chi tiết về chất liệu, kích thước, công dụng và hướng dẫn sử dụng hoặc bảo quản..."
                    />
                  </Field>
                </FieldGroup>
              </CardContent>
            </Card>

            {/* Card 2: Hình ảnh */}
            <Card aria-labelledby="prod-images-heading">
              <CardHeader>
                <CardTitle id="prod-images-heading">Hình ảnh</CardTitle>
                <CardDescription>{activeImageLabel}</CardDescription>
                <CardAction>
                  <div className="flex items-center gap-1.5">
                    {images.length > 0 && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        disabled={saving}
                        onClick={() => handleImagesChange([])}
                      >
                        Xóa tất cả
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      onClick={() => setShowSampleGallery((v) => !v)}
                    >
                      {showSampleGallery ? (
                        <>
                          <ChevronUp className="size-3.5" aria-hidden="true" />
                          <span>Thu gọn ảnh mẫu</span>
                        </>
                      ) : (
                        <>
                          <ChevronDown className="size-3.5" aria-hidden="true" />
                          <span>Thư viện ảnh mẫu</span>
                        </>
                      )}
                    </Button>
                  </div>
                </CardAction>
              </CardHeader>
              <CardContent className="space-y-4">
                <MediaUploader
                  value={images}
                  onChange={handleImagesChange}
                  maxFiles={MEDIA_MAX_PRODUCT_IMAGES}
                  disabled={saving}
                />

                {showSampleGallery && (
                  <div className="space-y-2.5 border-t border-border pt-3">
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-xs font-medium text-foreground">
                        Thư viện ảnh mẫu (tùy chọn phụ)
                      </span>
                      <span className="text-[11px] text-muted-foreground">
                        Bấm để thêm hoặc bỏ khỏi danh sách ảnh
                      </span>
                    </div>
                    <div
                      className="grid grid-cols-3 gap-2.5 sm:grid-cols-4 md:grid-cols-6"
                      role="group"
                      aria-label="Chọn ảnh mẫu minh họa (tùy chọn phụ)"
                    >
                      {SAMPLE_GALLERY_CHOICES.map((img) => {
                        const active = images.includes(img.path);
                        return (
                          <button
                            key={img.path}
                            type="button"
                            aria-pressed={active}
                            onClick={() => handleToggleSampleImage(img.path)}
                            title={img.label}
                            aria-label={active ? `Bỏ chọn ${img.label}` : `Thêm ${img.label}`}
                            className={cn(
                              "group relative aspect-[3/4] overflow-hidden rounded-lg border bg-muted transition-all focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                              active
                                ? "border-brand ring-2 ring-brand"
                                : "border-border hover:border-foreground/40"
                            )}
                          >
                            <img
                              src={img.path}
                              alt={img.label}
                              className="size-full object-cover"
                              width={96}
                              height={128}
                            />
                            {active && (
                              <span
                                className="absolute top-1.5 right-1.5 flex size-5 items-center justify-center rounded-full bg-brand text-white"
                                aria-hidden="true"
                              >
                                <Check className="size-3" />
                              </span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>

            {/* Card 3: Giá */}
            <Card aria-labelledby="prod-pricing-heading">
              <CardHeader>
                <CardTitle id="prod-pricing-heading">Giá</CardTitle>
                <CardDescription>
                  Khoảng giá hiện tại:{" "}
                  <strong className="text-foreground tabular-nums">{priceRangeLabel}</strong>
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
                  <Field
                    className="flex-1"
                    data-invalid={Boolean(fieldErrors.bulkPrice) || undefined}
                  >
                    <FieldLabel htmlFor="bulk-price-input">
                      Đặt nhanh đơn giá đồng loạt (VND)
                    </FieldLabel>
                    <Input
                      id="bulk-price-input"
                      data-error-key="bulkPrice"
                      type="number"
                      min={1000}
                      step={1}
                      placeholder="Ví dụ: 250000"
                      value={bulkPriceStr}
                      onChange={(e) => {
                        setBulkPriceStr(e.target.value);
                        clearFieldError("bulkPrice");
                      }}
                      className="tabular-nums"
                      aria-invalid={Boolean(fieldErrors.bulkPrice)}
                    />
                  </Field>
                  <Button type="button" variant="outline" size="default" onClick={handleBulkPrice}>
                    Áp dụng cho {formatInt(skuRows.length)} SKU
                  </Button>
                </div>
                {fieldErrors.bulkPrice ? <FieldError>{fieldErrors.bulkPrice}</FieldError> : null}
                <p className="text-xs text-muted-foreground">
                  Mỗi biến thể SKU vẫn có thể chỉnh đơn giá riêng trong bảng bên dưới. Đơn giá tính
                  bằng số nguyên VND (tối thiểu 1.000₫).
                </p>
              </CardContent>
            </Card>

            {/* Card 4: Tùy chọn & Biến thể SKU (Shopify-style) */}
            <Card aria-labelledby="prod-variants-heading">
              <CardHeader>
                <CardTitle id="prod-variants-heading">Biến thể &amp; SKU</CardTitle>
                <CardDescription className="tabular-nums">
                  {formatInt(skuRows.length)} SKU · {formatInt(totalStock)} tồn kho
                </CardDescription>
                <CardAction>
                  <div className="flex items-center gap-1.5">
                    <Input
                      type="number"
                      min={0}
                      step={1}
                      placeholder="SL"
                      value={bulkStockStr}
                      onChange={(e) => setBulkStockStr(e.target.value)}
                      aria-label="Số lượng tồn kho áp dụng hàng loạt"
                      className="h-8 w-20 text-right tabular-nums"
                    />
                    <Button type="button" variant="outline" size="sm" onClick={handleBulkStock}>
                      Đặt tồn đồng loạt
                    </Button>
                  </div>
                </CardAction>
              </CardHeader>
              <CardContent className="space-y-4">
                {/* Shopify-style Option Builder (0..3 seller-defined options) */}
                <div className="space-y-3 rounded-lg border border-border bg-muted/30 p-3.5">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div>
                      <h3 className="text-[13px] font-semibold text-foreground">
                        Tùy chọn phân loại (tối đa {MAX_VARIANT_OPTIONS})
                      </h3>
                      <p className="text-xs text-muted-foreground">
                        Để trống nếu sản phẩm chỉ có 1 biến thể mặc định, hoặc tự đặt tên tùy chọn
                        như &ldquo;Màu men&rdquo;, &ldquo;Quy cách&rdquo;, &ldquo;Dung tích&rdquo;.
                      </p>
                    </div>
                    {options.length < MAX_VARIANT_OPTIONS && (
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        onClick={() => handleAddOption("")}
                      >
                        <Plus className="size-3.5" aria-hidden="true" />
                        <span>Thêm tùy chọn</span>
                      </Button>
                    )}
                  </div>

                  {options.length < MAX_VARIANT_OPTIONS && (
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-[11px] text-muted-foreground">Gợi ý nhanh:</span>
                      {OPTION_NAME_SUGGESTIONS.filter(
                        (s) => !usedOptionNamesLower.has(s.toLowerCase())
                      ).map((suggestion) => (
                        <Button
                          key={suggestion}
                          type="button"
                          variant="secondary"
                          size="xs"
                          className="h-6 rounded-sm px-2 text-xs font-normal"
                          onClick={() => handleAddOption(suggestion)}
                        >
                          + {suggestion}
                        </Button>
                      ))}
                    </div>
                  )}

                  {options.length > 0 && (
                    <div className="space-y-3">
                      {options.map((opt, optIdx) => (
                        <div
                          key={opt.id}
                          className="space-y-2.5 rounded-lg border border-border bg-card p-3"
                        >
                          <div className="flex items-end gap-2">
                            <Field className="flex-1">
                              <FieldLabel htmlFor={`opt-name-${opt.id}`} className="text-xs">
                                Tên tùy chọn {optIdx + 1}
                              </FieldLabel>
                              <Input
                                id={`opt-name-${opt.id}`}
                                type="text"
                                value={opt.name}
                                onChange={(e) => handleUpdateOptionName(opt.id, e.target.value)}
                                placeholder="Ví dụ: Màu men, Quy cách, Dung tích..."
                                className="h-8 text-xs"
                              />
                            </Field>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-xs"
                              aria-label={`Xóa tùy chọn ${opt.name || optIdx + 1}`}
                              className="mb-0.5 text-muted-foreground hover:text-destructive"
                              onClick={() => handleRemoveOption(opt.id)}
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                            </Button>
                          </div>

                          <div className="space-y-2">
                            <div className="flex gap-2">
                              <Input
                                type="text"
                                value={optionValueInputs[opt.id] ?? ""}
                                onChange={(e) =>
                                  setOptionValueInputs((prev) => ({
                                    ...prev,
                                    [opt.id]: e.target.value,
                                  }))
                                }
                                onKeyDown={(e) => {
                                  if (e.key === "Enter") {
                                    e.preventDefault();
                                    handleAddOptionValues(opt.id);
                                  }
                                }}
                                placeholder="Nhập giá trị (có thể ngăn bằng dấu phẩy, vd: Men lam, Men ngọc)"
                                aria-label={`Giá trị cho tùy chọn ${opt.name || optIdx + 1}`}
                                className="h-8 flex-1 text-xs"
                              />
                              <Button
                                type="button"
                                variant="outline"
                                size="xs"
                                className="h-8 px-2.5"
                                onClick={() => handleAddOptionValues(opt.id)}
                              >
                                Thêm giá trị
                              </Button>
                            </div>

                            {opt.values.length > 0 && (
                              <div className="flex flex-wrap gap-1.5">
                                {opt.values.map((val) => (
                                  <Badge
                                    key={val}
                                    variant="secondary"
                                    className="gap-1 rounded-sm pr-1 pl-2 text-xs font-normal"
                                  >
                                    <span>{val}</span>
                                    <button
                                      type="button"
                                      aria-label={`Bỏ giá trị ${val}`}
                                      className="rounded-sm p-0.5 text-muted-foreground hover:text-foreground"
                                      onClick={() => handleRemoveOptionValue(opt.id, val)}
                                    >
                                      <X className="size-3" aria-hidden="true" />
                                    </button>
                                  </Badge>
                                ))}
                              </div>
                            )}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {combinationsTruncated && (
                    <p className="text-xs font-medium text-badge-warning-ink" role="status">
                      Hệ thống giới hạn tối đa {formatInt(MAX_VARIANT_COMBINATIONS)} tổ hợp biến thể
                      mỗi sản phẩm.
                    </p>
                  )}
                </div>

                {fieldErrors.skus ? <FieldError>{fieldErrors.skus}</FieldError> : null}

                {/* Desktop SKU Table */}
                <div className="hidden overflow-hidden rounded-lg border border-border lg:block">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="px-2">Biến thể &amp; Mã SKU</TableHead>
                        <TableHead className="px-2 text-right">Giá (VND)</TableHead>
                        <TableHead className="px-2 text-right">Tồn kho</TableHead>
                        <TableHead className="px-2 text-right">Gram</TableHead>
                        <TableHead className="px-2 text-right">Thao tác</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {skuRows.map((row, idx) => {
                        const lowAvail = Math.max(0, row.stock - row.reservedStock) <= 5;
                        const codeErr = fieldErrors[`sku-code-${idx}`];
                        const priceErr = fieldErrors[`sku-price-${idx}`];
                        const stockErr = fieldErrors[`sku-stock-${idx}`];
                        const variantDisplay = formatVariantLabel({
                          attributes: row.attributes,
                          skuName: row.name,
                          fallback: DEFAULT_VARIANT_NAME,
                        });
                        const attrEntries = normalizeVariantEntries(row.attributes);
                        const colorEntry = attrEntries.find((e) => e.hex);
                        return (
                          <TableRow key={row.localKey}>
                            <TableCell className="px-2 align-top whitespace-normal">
                              <div className="space-y-1.5">
                                <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                                  {colorEntry?.hex ? (
                                    <span
                                      className="size-3 shrink-0 rounded-full border border-border"
                                      style={{ backgroundColor: colorEntry.hex }}
                                      aria-hidden="true"
                                    />
                                  ) : null}
                                  <span className="[overflow-wrap:anywhere]">{variantDisplay}</span>
                                </div>
                                <Input
                                  type="text"
                                  data-error-key={`sku-code-${idx}`}
                                  value={row.skuCode}
                                  onChange={(e) => {
                                    updateRow(row.localKey, { skuCode: e.target.value });
                                    clearFieldError(`sku-code-${idx}`);
                                  }}
                                  aria-label={`Mã SKU ${variantDisplay}`}
                                  aria-invalid={Boolean(codeErr)}
                                  className="h-8 w-full min-w-[140px] px-2 font-mono text-xs"
                                />
                                {codeErr ? (
                                  <FieldError className="text-xs">{codeErr}</FieldError>
                                ) : null}
                                {images.length > 0 ? (
                                  <div className="flex items-center gap-1.5">
                                    <img
                                      src={row.imageUrl || primaryImage}
                                      alt={`Ảnh biến thể ${variantDisplay}`}
                                      width={28}
                                      height={28}
                                      className="size-7 shrink-0 rounded-sm border border-border bg-muted object-cover"
                                    />
                                    <Select
                                      value={
                                        row.imageUrl && images.includes(row.imageUrl)
                                          ? row.imageUrl
                                          : SKU_IMAGE_PRIMARY_VALUE
                                      }
                                      onValueChange={(v) =>
                                        updateRow(row.localKey, {
                                          imageUrl: v === SKU_IMAGE_PRIMARY_VALUE ? "" : v,
                                        })
                                      }
                                    >
                                      <SelectTrigger
                                        size="sm"
                                        aria-label={`Chọn ảnh cho biến thể ${variantDisplay}`}
                                        className="h-7 min-w-[120px] flex-1 bg-card px-2 text-xs"
                                      >
                                        <SelectValue />
                                      </SelectTrigger>
                                      <SelectContent>
                                        <SelectItem value={SKU_IMAGE_PRIMARY_VALUE}>
                                          Theo ảnh chính
                                        </SelectItem>
                                        {images.map((imgUrl, imgIdx) => (
                                          <SelectItem key={imgUrl} value={imgUrl}>
                                            {imgIdx === 0
                                              ? "Ảnh 1 (Ảnh chính)"
                                              : `Ảnh ${imgIdx + 1}`}
                                          </SelectItem>
                                        ))}
                                      </SelectContent>
                                    </Select>
                                  </div>
                                ) : null}
                              </div>
                            </TableCell>
                            <TableCell className="px-2 align-top text-right">
                              <Input
                                type="number"
                                min={1000}
                                step={1}
                                placeholder="150000"
                                data-error-key={`sku-price-${idx}`}
                                value={row.priceVnd > 0 ? row.priceVnd : ""}
                                onChange={(e) => {
                                  const rawVal = e.target.value.trim();
                                  updateRow(row.localKey, {
                                    priceVnd: rawVal
                                      ? Math.max(0, Math.trunc(Number(rawVal) || 0))
                                      : 0,
                                  });
                                  clearFieldError(`sku-price-${idx}`);
                                }}
                                aria-label={`Đơn giá ${row.skuCode}`}
                                aria-invalid={Boolean(priceErr)}
                                className="ml-auto h-8 w-[92px] px-2 text-right text-xs tabular-nums"
                              />
                              {priceErr ? (
                                <FieldError className="mt-1 text-right text-xs">
                                  {priceErr}
                                </FieldError>
                              ) : null}
                            </TableCell>
                            <TableCell className="px-2 align-top text-right">
                              <Input
                                type="number"
                                min={row.reservedStock}
                                step={1}
                                data-error-key={`sku-stock-${idx}`}
                                value={row.stock}
                                onChange={(e) => {
                                  updateRow(row.localKey, {
                                    stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                                  });
                                  clearFieldError(`sku-stock-${idx}`);
                                }}
                                aria-label={`Tồn kho ${row.skuCode}`}
                                aria-invalid={Boolean(stockErr)}
                                className={cn(
                                  "ml-auto h-8 w-[68px] px-1.5 text-right text-xs tabular-nums",
                                  lowAvail && "border-destructive/60 text-destructive"
                                )}
                              />
                              <div className="mt-1 text-[11px] text-muted-foreground tabular-nums">
                                Giữ: {formatInt(row.reservedStock)}
                              </div>
                              {stockErr ? (
                                <FieldError className="mt-1 text-right text-xs">
                                  {stockErr}
                                </FieldError>
                              ) : null}
                            </TableCell>
                            <TableCell className="px-2 align-top text-right">
                              <Input
                                type="number"
                                min={1}
                                step={1}
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
                                className="ml-auto h-8 w-[64px] px-1.5 text-right text-xs tabular-nums"
                              />
                            </TableCell>
                            <TableCell className="px-2 align-top text-right">
                              <div className="inline-flex items-center justify-end gap-1">
                                {mode === "edit" && row.id && row.version ? (
                                  <Button
                                    type="button"
                                    variant="outline"
                                    size="xs"
                                    className="px-2"
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
                                    size="icon-xs"
                                    aria-label={`Xóa SKU ${row.skuCode}`}
                                    className="text-muted-foreground hover:text-destructive"
                                    onClick={() => handleRemoveRow(row.localKey)}
                                  >
                                    <Trash2 className="size-3.5" aria-hidden="true" />
                                  </Button>
                                )}
                              </div>
                            </TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                {/* Mobile & Tablet SKU Editor Cards */}
                <div className="space-y-3 lg:hidden">
                  {skuRows.map((row, idx) => {
                    const lowAvail = Math.max(0, row.stock - row.reservedStock) <= 5;
                    const codeErr = fieldErrors[`sku-code-${idx}`];
                    const priceErr = fieldErrors[`sku-price-${idx}`];
                    const stockErr = fieldErrors[`sku-stock-${idx}`];
                    const variantDisplay = formatVariantLabel({
                      attributes: row.attributes,
                      skuName: row.name,
                      fallback: DEFAULT_VARIANT_NAME,
                    });
                    const colorEntry = normalizeVariantEntries(row.attributes).find((e) => e.hex);
                    return (
                      <div
                        key={row.localKey}
                        className="space-y-3 rounded-lg border border-border bg-muted/35 p-3"
                      >
                        <div className="flex items-center justify-between gap-2">
                          <div className="flex min-w-0 items-center gap-2 text-sm font-semibold text-foreground">
                            {colorEntry?.hex ? (
                              <span
                                className="size-3.5 shrink-0 rounded-full border border-border"
                                style={{ backgroundColor: colorEntry.hex }}
                                aria-hidden="true"
                              />
                            ) : null}
                            <span className="truncate">{variantDisplay}</span>
                          </div>
                          <div className="flex shrink-0 items-center gap-2">
                            {mode === "edit" && row.id && row.version ? (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="min-h-10"
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
                                className="min-h-10 text-destructive"
                                onClick={() => handleRemoveRow(row.localKey)}
                              >
                                Xóa
                              </Button>
                            )}
                          </div>
                        </div>

                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                          <Field data-invalid={Boolean(codeErr) || undefined}>
                            <FieldLabel>Mã SKU</FieldLabel>
                            <Input
                              type="text"
                              data-error-key={`sku-code-${idx}`}
                              value={row.skuCode}
                              onChange={(e) => {
                                updateRow(row.localKey, { skuCode: e.target.value });
                                clearFieldError(`sku-code-${idx}`);
                              }}
                              aria-invalid={Boolean(codeErr)}
                              className="bg-card font-mono text-xs"
                            />
                            {codeErr ? <FieldError>{codeErr}</FieldError> : null}
                          </Field>

                          <Field data-invalid={Boolean(priceErr) || undefined}>
                            <FieldLabel>Giá (VND)</FieldLabel>
                            <Input
                              type="number"
                              min={1000}
                              step={1}
                              placeholder="150000"
                              data-error-key={`sku-price-${idx}`}
                              value={row.priceVnd > 0 ? row.priceVnd : ""}
                              onChange={(e) => {
                                const rawVal = e.target.value.trim();
                                updateRow(row.localKey, {
                                  priceVnd: rawVal
                                    ? Math.max(0, Math.trunc(Number(rawVal) || 0))
                                    : 0,
                                });
                                clearFieldError(`sku-price-${idx}`);
                              }}
                              aria-invalid={Boolean(priceErr)}
                              className="bg-card tabular-nums"
                            />
                            {priceErr ? <FieldError>{priceErr}</FieldError> : null}
                          </Field>

                          <div className="grid grid-cols-2 gap-2 sm:col-span-2">
                            <Field data-invalid={Boolean(stockErr) || undefined}>
                              <FieldLabel>Tồn (giữ: {formatInt(row.reservedStock)})</FieldLabel>
                              <Input
                                type="number"
                                min={row.reservedStock}
                                step={1}
                                data-error-key={`sku-stock-${idx}`}
                                value={row.stock}
                                onChange={(e) => {
                                  updateRow(row.localKey, {
                                    stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                                  });
                                  clearFieldError(`sku-stock-${idx}`);
                                }}
                                aria-invalid={Boolean(stockErr)}
                                className={cn(
                                  "bg-card tabular-nums",
                                  lowAvail && "border-destructive/60 text-destructive"
                                )}
                              />
                              {stockErr ? <FieldError>{stockErr}</FieldError> : null}
                            </Field>
                            <Field>
                              <FieldLabel>Khối lượng (g)</FieldLabel>
                              <Input
                                type="number"
                                min={1}
                                step={1}
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
                                className="bg-card tabular-nums"
                              />
                            </Field>
                          </div>

                          {images.length > 0 ? (
                            <Field className="sm:col-span-2">
                              <FieldLabel>Ảnh biến thể</FieldLabel>
                              <div className="flex items-center gap-2">
                                <img
                                  src={row.imageUrl || primaryImage}
                                  alt={`Ảnh biến thể ${variantDisplay}`}
                                  width={36}
                                  height={36}
                                  className="size-9 shrink-0 rounded-md border border-border bg-muted object-cover"
                                />
                                <Select
                                  value={
                                    row.imageUrl && images.includes(row.imageUrl)
                                      ? row.imageUrl
                                      : SKU_IMAGE_PRIMARY_VALUE
                                  }
                                  onValueChange={(v) =>
                                    updateRow(row.localKey, {
                                      imageUrl: v === SKU_IMAGE_PRIMARY_VALUE ? "" : v,
                                    })
                                  }
                                >
                                  <SelectTrigger
                                    aria-label={`Chọn ảnh cho biến thể ${variantDisplay}`}
                                    className="min-h-10 flex-1 bg-card text-xs"
                                  >
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    <SelectItem value={SKU_IMAGE_PRIMARY_VALUE}>
                                      Theo ảnh chính
                                    </SelectItem>
                                    {images.map((imgUrl, imgIdx) => (
                                      <SelectItem key={imgUrl} value={imgUrl}>
                                        {imgIdx === 0 ? "Ảnh 1 (Ảnh chính)" : `Ảnh ${imgIdx + 1}`}
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                              </div>
                            </Field>
                          ) : null}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </CardContent>
              <CardFooter className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3">
                <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground tabular-nums">
                  <span>
                    {formatInt(skuRows.length)} SKU · {formatInt(totalStock)} tồn
                  </span>
                  {lowSkuCount > 0 && (
                    <Badge variant="warning">{formatInt(lowSkuCount)} SKU sắp hết</Badge>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => router.push("/seller/san-pham")}
                  >
                    Hủy
                  </Button>
                  <Button type="submit" size="sm" disabled={saving}>
                    <Check className="size-4" aria-hidden="true" />
                    <span>
                      {saving
                        ? "Đang lưu..."
                        : mode === "create"
                          ? "Đăng bán sản phẩm"
                          : "Lưu thay đổi"}
                    </span>
                  </Button>
                </div>
              </CardFooter>
            </Card>
          </>
        }
        aside={
          <>
            {/* Aside Card 1: Trạng thái */}
            <Card aria-labelledby="prod-status-heading">
              <CardHeader>
                <CardTitle id="prod-status-heading">Trạng thái</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {mode === "edit" ? (
                  <Field>
                    <FieldLabel htmlFor="prod-status">Trạng thái hiển thị</FieldLabel>
                    <Select
                      value={productStatus}
                      disabled={allowedStatuses.length <= 1}
                      onValueChange={setProductStatus}
                    >
                      <SelectTrigger id="prod-status" className="w-full bg-card">
                        <SelectValue>
                          {allowedStatuses.find((opt) => opt.value === productStatus)?.label ??
                            statusMeta.label}
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        {allowedStatuses.map((opt) => (
                          <SelectItem key={opt.value} value={opt.value}>
                            {opt.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FieldDescription className="text-xs">
                      Có thể ẩn/hiện sản phẩm đã duyệt hoặc gửi duyệt lại sản phẩm bị từ chối.
                    </FieldDescription>
                  </Field>
                ) : (
                  <div className="space-y-2">
                    <StatusBadge tone={productStatusTone(productStatus)}>
                      {statusMeta.label}
                    </StatusBadge>
                    <p className="text-xs text-muted-foreground">
                      Sản phẩm mới sau khi đăng bán sẽ vào danh sách Chờ duyệt trước khi hiển thị
                      trên site gian hàng.
                    </p>
                  </div>
                )}
              </CardContent>
            </Card>

            {/* Aside Card 2: Phân loại */}
            <Card aria-labelledby="prod-category-heading">
              <CardHeader>
                <CardTitle id="prod-category-heading">Phân loại</CardTitle>
              </CardHeader>
              <CardContent>
                <FieldGroup className="gap-4">
                  <Field data-invalid={Boolean(fieldErrors.categoryId) || undefined}>
                    <FieldLabel htmlFor="prod-category">Ngành hàng</FieldLabel>
                    <Select
                      value={categoryId || undefined}
                      onValueChange={(v) => {
                        setCategoryId(v);
                        clearFieldError("categoryId");
                      }}
                    >
                      <SelectTrigger
                        id="prod-category"
                        data-error-key="categoryId"
                        aria-invalid={Boolean(fieldErrors.categoryId)}
                        className="w-full bg-card"
                      >
                        <SelectValue placeholder="Chọn ngành hàng">
                          {(() => {
                            const selCat = categories.find((cat) => cat.id === categoryId);
                            if (!selCat) return null;
                            const base =
                              typeof selCat.commission_rate_bps === "number" &&
                              Number.isFinite(selCat.commission_rate_bps)
                                ? `${selCat.name} (Hoa hồng ${(selCat.commission_rate_bps / 100).toFixed(1)}%)`
                                : selCat.name;
                            return `${base}${selCat.is_prohibited ? " — Danh mục cấm" : ""}`;
                          })()}
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        {categories.map((cat) => (
                          <SelectItem
                            key={cat.id}
                            value={cat.id}
                            disabled={Boolean(cat.is_prohibited)}
                          >
                            {typeof cat.commission_rate_bps === "number" &&
                            Number.isFinite(cat.commission_rate_bps)
                              ? `${cat.name} (Hoa hồng ${(cat.commission_rate_bps / 100).toFixed(1)}%)`
                              : cat.name}
                            {cat.is_prohibited ? " — Danh mục cấm" : ""}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {fieldErrors.categoryId ? (
                      <FieldError>{fieldErrors.categoryId}</FieldError>
                    ) : null}
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="prod-slug">Đường dẫn tùy chỉnh (tùy chọn)</FieldLabel>
                    <Input
                      id="prod-slug"
                      type="text"
                      value={slug}
                      onChange={(e) => setSlug(e.target.value)}
                      placeholder={name ? slugifyUrl(name) : "tu-dong-tao-tu-ten-san-pham"}
                    />
                  </Field>
                </FieldGroup>
              </CardContent>
            </Card>

            {/* Aside Card 3: Xem trước thẻ sản phẩm */}
            <Card aria-label="Xem trước thẻ sản phẩm">
              <CardHeader>
                <CardTitle>Xem trước thẻ sản phẩm</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <article className="overflow-hidden rounded-lg border border-border bg-card">
                  <div className="relative flex aspect-[3/4] items-center justify-center bg-muted">
                    {primaryImage ? (
                      <img
                        src={primaryImage}
                        alt={name.trim() || "Ảnh sản phẩm"}
                        className="size-full object-cover"
                        width={320}
                        height={426}
                      />
                    ) : (
                      <div className="flex flex-col items-center gap-2 px-4 text-center text-muted-foreground">
                        <ImageIcon className="size-8 opacity-50" aria-hidden="true" />
                        <span className="text-xs">Chưa có ảnh sản phẩm</span>
                      </div>
                    )}
                    {totalStock === 0 ? (
                      <Badge variant="critical" className="absolute top-2 left-2">
                        Hết hàng
                      </Badge>
                    ) : totalStock <= 10 ? (
                      <Badge variant="warning" className="absolute top-2 left-2 tabular-nums">
                        Còn {formatInt(totalStock)}
                      </Badge>
                    ) : null}
                  </div>

                  <div className="space-y-2 p-3">
                    <div className="text-xs text-muted-foreground">{activeCategoryName}</div>
                    <h2 className="line-clamp-2 text-sm font-semibold text-foreground">
                      {name.trim() || "Tên sản phẩm hiển thị tại gian hàng"}
                    </h2>
                    <div className="text-sm font-semibold text-foreground tabular-nums">
                      {priceRangeLabel}
                    </div>

                    {previewOptionSummary.length > 0 && (
                      <div className="space-y-1">
                        {previewOptionSummary.map((opt) => (
                          <div key={opt.label} className="flex flex-wrap items-center gap-1">
                            <span className="text-[11px] font-medium text-muted-foreground">
                              {opt.label}:
                            </span>
                            {opt.values.slice(0, 5).map((v) => (
                              <Badge
                                key={v}
                                variant="secondary"
                                className="rounded-sm px-1.5 py-0 text-[11px] font-normal"
                              >
                                {v}
                              </Badge>
                            ))}
                            {opt.values.length > 5 && (
                              <span className="text-[11px] text-muted-foreground tabular-nums">
                                +{opt.values.length - 5}
                              </span>
                            )}
                          </div>
                        ))}
                      </div>
                    )}

                    <div className="border-t border-border pt-2 text-xs text-muted-foreground">
                      {shop?.name ?? "Gian hàng NexusMart"}
                    </div>
                  </div>
                </article>

                <Separator />

                <dl className="space-y-2 text-[13px] tabular-nums">
                  <div className="flex items-center justify-between gap-2">
                    <dt className="text-muted-foreground">Biến thể SKU</dt>
                    <dd className="font-medium text-foreground">{formatInt(skuRows.length)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-2">
                    <dt className="text-muted-foreground">Tổng tồn kho</dt>
                    <dd className="font-medium text-foreground">
                      {formatInt(totalStock)} sản phẩm
                    </dd>
                  </div>
                  <div className="flex items-center justify-between gap-2">
                    <dt className="text-muted-foreground">SKU dưới ngưỡng 5</dt>
                    <dd
                      className={cn(
                        "font-medium",
                        lowSkuCount > 0 ? "text-destructive" : "text-foreground"
                      )}
                    >
                      {formatInt(lowSkuCount)}
                    </dd>
                  </div>
                </dl>
              </CardContent>
            </Card>
          </>
        }
      />

      {/* Delete Product Confirmation Dialog */}
      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Xóa sản phẩm &ldquo;{currentProduct?.name}&rdquo;?</AlertDialogTitle>
            <AlertDialogDescription>
              Sản phẩm sẽ bị gỡ khỏi gian hàng và không thể khôi phục sau khi xóa.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Giữ lại</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleting}
              onClick={() => void handleDeleteProduct()}
            >
              {deleting ? "Đang xóa..." : "Xác nhận xóa"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </form>
  );
}
