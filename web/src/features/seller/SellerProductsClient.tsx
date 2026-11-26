"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  AlertTriangle,
  ArrowUpRight,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Download,
  History,
  Info,
  Package,
  Plus,
  RefreshCw,
  Search,
  SlidersHorizontal,
  Upload,
} from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexTable,
  MetricStrip,
  PageHeader,
  StatusBadge,
  productStatusTone,
  type StatusTone,
} from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, apiBlob, ApiError, getErrorMessage } from "@/lib/api";
import {
  IMPORT_JOB_STATUS_META,
  IMPORT_ROW_STATUS_META,
  MAX_IMPORT_CSV_ROWS,
  SELLER_PRODUCTS_PAGE_SIZE,
  SELLER_PRODUCT_TAB_FILTER_KEYS,
  STOCK_HISTORY_CHANGE_META,
  STOCK_HISTORY_PAGE_SIZE,
  buildSellerProductsQuery,
  buildStockHistoryQuery,
  createInitialSellerProductTabCounts,
  deriveSellerProductTabCountsFromCompleteList,
  formatSellerProductsHeaderSummary,
  formatStockHistoryReason,
  isImportJobPollingStatus,
  resolveClampedPageOnEmpty,
  settleSellerProductTabCounts,
  shouldResetImportJobOnModalOpen,
  translateProductImportError,
  translateProductImportJobError,
  translateProductImportRowError,
  validateImportCsvFile,
  type ConfirmProductImportJobResponse,
  type ProductImportJob,
  type ProductImportJobResponse,
  type ProductImportRow,
  type SellerProductFilterKey,
  type SellerProductTabCounts,
  type StockHistoryItem,
} from "@/lib/a8-types";
import { formatDateTimeVi, formatInt, formatSignedInt, formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useSellerContext } from "./SellerShell";
import {
  getSellerProductStatusMeta,
  type GwCategory,
  type GwSellerProduct,
  type GwSellerSku,
} from "./sellerApiTypes";
import { parseUiState } from "./types";

function formatPriceRange(p: GwSellerProduct): string {
  const minP = p.min_price_vnd;
  const maxP = p.max_price_vnd;
  if (minP === maxP || maxP <= 0) return formatVnd(minP);
  return `${formatVnd(minP)} – ${formatVnd(maxP)}`;
}

function getCoverImage(p: GwSellerProduct): string {
  return (
    p.images?.[0] ||
    p.skus?.find((s) => Boolean(s.image_url))?.image_url ||
    "/products/ao-thun-cotton-trang.png"
  );
}

function totalAvailable(p: GwSellerProduct): number {
  return (p.skus ?? [])
    .filter((s) => s.is_active)
    .reduce((sum, s) => sum + Math.max(0, s.available_stock), 0);
}

function buildPageNumbers(current: number, totalPages: number): Array<number | "ellipsis"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages: Array<number | "ellipsis"> = [1];
  const start = Math.max(2, current - 1);
  const end = Math.min(totalPages - 1, current + 1);
  if (start > 2) pages.push("ellipsis");
  for (let p = start; p <= end; p += 1) pages.push(p);
  if (end < totalPages - 1) pages.push("ellipsis");
  pages.push(totalPages);
  return pages;
}

function triggerBlobDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function normalizeInitialFilter(raw: string | null): SellerProductFilterKey {
  if (raw === "low" || raw === "oos" || raw === "LOW_OOS" || raw === "OUT_OF_STOCK") {
    return "LOW_OOS";
  }
  if (raw === "active") return "APPROVED";
  if (raw && ["all", "APPROVED", "PENDING", "REJECTED", "HIDDEN"].includes(raw)) {
    return raw as SellerProductFilterKey;
  }
  return "all";
}

function mapLegacyTone(tone: string): StatusTone {
  switch (tone) {
    case "pos":
      return "success";
    case "neg":
      return "critical";
    case "warn":
      return "warning";
    default:
      return "neutral";
  }
}

export interface SellerProductsClientProps {
  initialProducts: GwSellerProduct[];
  initialTotal: number;
  initialTabCounts?: SellerProductTabCounts;
  categories: GwCategory[];
  loadError: string | null;
}

export function SellerProductsClient({
  initialProducts,
  initialTotal,
  initialTabCounts,
  categories,
  loadError,
}: SellerProductsClientProps) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialFilter = normalizeInitialFilter(searchParams.get("filter"));
  const { shop, showToast } = useSellerContext();

  const [products, setProducts] = useState<GwSellerProduct[]>(initialProducts);
  const [totalProducts, setTotalProducts] = useState(initialTotal);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(loadError);
  const [conflictNotice, setConflictNotice] = useState<string | null>(null);

  const [tabCounts, setTabCounts] = useState<SellerProductTabCounts>(
    () =>
      initialTabCounts ?? {
        ...createInitialSellerProductTabCounts(),
        all: initialTotal,
      }
  );

  const [filter, setFilter] = useState<SellerProductFilterKey>(initialFilter);
  const [queryInput, setQueryInput] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("all");
  const [showFilters, setShowFilters] = useState(true);

  const [expandedProductIds, setExpandedProductIds] = useState<Record<string, boolean>>({});
  const [stockDrafts, setStockDrafts] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const p of initialProducts) {
      for (const s of p.skus ?? []) init[s.id] = String(s.stock);
    }
    return init;
  });
  const [savingSkuId, setSavingSkuId] = useState<string | null>(null);
  const [confirmDeleteProduct, setConfirmDeleteProduct] = useState<GwSellerProduct | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [drawerProductId, setDrawerProductId] = useState<string | null>(null);

  // S03 Stock History Dialog state
  const [historyTarget, setHistoryTarget] = useState<{
    product: GwSellerProduct;
    sku: GwSellerSku;
  } | null>(null);
  const [historyItems, setHistoryItems] = useState<StockHistoryItem[]>([]);
  const [historyTotal, setHistoryTotal] = useState(0);
  const [historyPage, setHistoryPage] = useState(1);
  const [historyChangeType, setHistoryChangeType] = useState("all");
  const [historyFromDate, setHistoryFromDate] = useState("");
  const [historyToDate, setHistoryToDate] = useState("");
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);

  // UC-S13 2-Step CSV Import Dialog state
  const [importModalOpen, setImportModalOpen] = useState(false);
  const [importCategoryId, setImportCategoryId] = useState("all");
  const [importFile, setImportFile] = useState<File | null>(null);
  const [importFileError, setImportFileError] = useState<string | null>(null);
  const [importUploading, setImportUploading] = useState(false);
  const [downloadingTemplate, setDownloadingTemplate] = useState(false);
  const [downloadingErrors, setDownloadingErrors] = useState(false);
  const [confirmingImport, setConfirmingImport] = useState(false);
  const [importJob, setImportJob] = useState<ProductImportJob | null>(null);
  const [importActionError, setImportActionError] = useState<string | null>(null);
  const [importErrorRows, setImportErrorRows] = useState<ProductImportRow[]>([]);
  const [importErrorTotal, setImportErrorTotal] = useState(0);
  const [importErrorPage, setImportErrorPage] = useState(1);
  const [importErrorLoading, setImportErrorLoading] = useState(false);
  const importFileInputRef = useRef<HTMLInputElement | null>(null);
  const completedRefreshedJobIdRef = useRef<string | null>(null);

  const productsLoadSeqRef = useRef(0);
  const historyLoadSeqRef = useRef(0);
  const tabCountsLoadSeqRef = useRef(0);
  const skippedInitialProductsFetchRef = useRef(initialFilter === "all");

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(queryInput.trim());
    }, 250);
    return () => window.clearTimeout(timer);
  }, [queryInput]);

  const loadTabCounts = useCallback(async () => {
    const seq = ++tabCountsLoadSeqRef.current;
    try {
      const res = await api<{ tab_counts?: Partial<SellerProductTabCounts> }>(
        "/api/seller/products?tab_counts_only=true"
      );
      if (seq !== tabCountsLoadSeqRef.current) return;
      const raw = res.tab_counts ?? {};
      setTabCounts(
        settleSellerProductTabCounts(
          SELLER_PRODUCT_TAB_FILTER_KEYS.map((key) => {
            const val = raw[key];
            return {
              key,
              result:
                typeof val === "number" && Number.isFinite(val)
                  ? { status: "fulfilled" as const, value: val }
                  : { status: "rejected" as const, reason: new Error("unavailable") },
            };
          })
        )
      );
    } catch {
      if (seq !== tabCountsLoadSeqRef.current) return;
      setTabCounts(createInitialSellerProductTabCounts());
    }
  }, []);

  const loadProducts = useCallback(
    async (opts?: { silent?: boolean }): Promise<boolean> => {
      const seq = ++productsLoadSeqRef.current;
      if (!opts?.silent) setLoading(true);
      setErrorMsg(null);
      try {
        const qs = buildSellerProductsQuery({
          page,
          pageSize: SELLER_PRODUCTS_PAGE_SIZE,
          filter,
          categoryId: categoryFilter,
          keyword: debouncedQuery,
        });
        const prodRes = await api<{ items?: GwSellerProduct[]; total?: number }>(
          `/api/seller/products?${qs}`
        );
        if (seq !== productsLoadSeqRef.current) return false;
        const items = prodRes.items ?? [];
        const resolvedTotal = Math.max(0, Math.trunc(prodRes.total ?? items.length));
        const clampedPage = resolveClampedPageOnEmpty(
          page,
          items.length,
          resolvedTotal,
          SELLER_PRODUCTS_PAGE_SIZE
        );
        if (clampedPage !== null) {
          setTotalProducts(resolvedTotal);
          setPage(clampedPage);
          return false;
        }
        setProducts(items);
        setTotalProducts(resolvedTotal);
        let derivedCompleteCounts = false;
        if (categoryFilter === "all" && !debouncedQuery) {
          const derived =
            page === 1 && filter === "all"
              ? deriveSellerProductTabCountsFromCompleteList(items, resolvedTotal)
              : null;
          if (derived) {
            setTabCounts(derived);
            derivedCompleteCounts = true;
          } else {
            setTabCounts((prev) => ({ ...prev, [filter]: resolvedTotal }));
          }
        }
        setStockDrafts((prev) => {
          const next = { ...prev };
          for (const p of items) {
            for (const s of p.skus ?? []) next[s.id] = String(s.stock);
          }
          return next;
        });
        return derivedCompleteCounts;
      } catch (err) {
        if (seq !== productsLoadSeqRef.current) return false;
        setErrorMsg(getErrorMessage(err, "Không thể tải danh sách sản phẩm lúc này."));
        return false;
      } finally {
        if (!opts?.silent && seq === productsLoadSeqRef.current) {
          setLoading(false);
        }
      }
    },
    [page, filter, categoryFilter, debouncedQuery]
  );

  useEffect(() => {
    if (uiState) return;
    if (skippedInitialProductsFetchRef.current) {
      skippedInitialProductsFetchRef.current = false;
      return;
    }
    void loadProducts();
  }, [uiState, loadProducts]);

  useEffect(() => {
    if (!uiState && !loadError && !initialTabCounts) {
      void loadTabCounts();
    }
  }, [uiState, loadError, initialTabCounts, loadTabCounts]);

  const refreshAll = useCallback(async () => {
    const derived = await loadProducts();
    if (!derived) {
      await loadTabCounts();
    }
  }, [loadProducts, loadTabCounts]);

  const loadStockHistory = useCallback(async () => {
    if (!historyTarget) return;
    const seq = ++historyLoadSeqRef.current;
    setHistoryLoading(true);
    setHistoryError(null);
    try {
      const qs = buildStockHistoryQuery({
        page: historyPage,
        pageSize: STOCK_HISTORY_PAGE_SIZE,
        changeType: historyChangeType,
        fromDate: historyFromDate,
        toDate: historyToDate,
      });
      const res = await api<{ items?: StockHistoryItem[]; total?: number }>(
        `/api/seller/skus/${encodeURIComponent(historyTarget.sku.id)}/stock-history?${qs}`
      );
      if (seq !== historyLoadSeqRef.current) return;
      const items = res.items ?? [];
      const resolvedTotal = Math.max(0, Math.trunc(res.total ?? items.length));
      const clampedPage = resolveClampedPageOnEmpty(
        historyPage,
        items.length,
        resolvedTotal,
        STOCK_HISTORY_PAGE_SIZE
      );
      if (clampedPage !== null) {
        setHistoryTotal(resolvedTotal);
        setHistoryPage(clampedPage);
        return;
      }
      setHistoryItems(items);
      setHistoryTotal(resolvedTotal);
    } catch (err) {
      if (seq !== historyLoadSeqRef.current) return;
      setHistoryError(getErrorMessage(err, "Không thể tải lịch sử biến động tồn kho SKU."));
    } finally {
      if (seq === historyLoadSeqRef.current) {
        setHistoryLoading(false);
      }
    }
  }, [historyTarget, historyPage, historyChangeType, historyFromDate, historyToDate]);

  useEffect(() => {
    if (historyTarget) {
      void loadStockHistory();
    }
  }, [historyTarget, loadStockHistory]);

  // CSV Import polling while QUEUED / VALIDATING / CONFIRMING
  const activeImportJobId = importJob?.id ?? null;
  const activeImportStatus = importJob?.status ?? "";
  useEffect(() => {
    if (!importModalOpen || !activeImportJobId || !isImportJobPollingStatus(activeImportStatus)) {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(() => {
      void api<ProductImportJobResponse>(
        `/api/seller/products/import/${encodeURIComponent(activeImportJobId)}`
      )
        .then((res) => {
          if (!cancelled && res.job) setImportJob(res.job);
        })
        .catch(() => {});
    }, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [importModalOpen, activeImportJobId, activeImportStatus]);

  const hasImportRowErrors = Boolean(
    importJob &&
    !isImportJobPollingStatus(importJob.status) &&
    (importJob.invalid_rows > 0 || importJob.failed_rows > 0)
  );

  useEffect(() => {
    if (!importModalOpen || !activeImportJobId || !hasImportRowErrors) return;
    let cancelled = false;
    setImportErrorLoading(true);
    void api<{ items?: ProductImportRow[]; total?: number }>(
      `/api/seller/products/import/${encodeURIComponent(activeImportJobId)}/rows?errors_only=true&page=${importErrorPage}&page_size=20`
    )
      .then((res) => {
        if (cancelled) return;
        const items = res.items ?? [];
        setImportErrorRows(items);
        setImportErrorTotal(Math.max(0, Math.trunc(res.total ?? items.length)));
      })
      .catch(() => {
        if (!cancelled) {
          setImportErrorRows([]);
          setImportErrorTotal(0);
        }
      })
      .finally(() => {
        if (!cancelled) setImportErrorLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [importModalOpen, activeImportJobId, activeImportStatus, hasImportRowErrors, importErrorPage]);

  useEffect(() => {
    if (
      importJob &&
      importJob.status === "COMPLETED" &&
      completedRefreshedJobIdRef.current !== importJob.id
    ) {
      completedRefreshedJobIdRef.current = importJob.id;
      void loadProducts({ silent: true }).then((derived) => {
        if (!derived) void loadTabCounts();
      });
    }
  }, [importJob, loadProducts, loadTabCounts]);

  const categoryNameById = useMemo(
    () => new Map(categories.map((c) => [c.id, c.name])),
    [categories]
  );

  const lowStockRows = useMemo(() => {
    const rows: Array<{ product: GwSellerProduct; sku: GwSellerSku }> = [];
    for (const product of products) {
      for (const sku of product.skus ?? []) {
        if (sku.is_active && sku.available_stock <= 5) {
          rows.push({ product, sku });
        }
      }
    }
    return rows.sort((a, b) => a.sku.available_stock - b.sku.available_stock).slice(0, 8);
  }, [products]);

  const drawerProduct = useMemo(
    () => (drawerProductId ? (products.find((p) => p.id === drawerProductId) ?? null) : null),
    [products, drawerProductId]
  );

  const countCards: Array<{ key: SellerProductFilterKey; label: string; count: number | null }> = [
    { key: "all", label: "Tất cả", count: tabCounts.all },
    { key: "APPROVED", label: "Đang bán", count: tabCounts.APPROVED },
    { key: "PENDING", label: "Chờ duyệt", count: tabCounts.PENDING },
    { key: "REJECTED", label: "Bị từ chối", count: tabCounts.REJECTED },
    { key: "HIDDEN", label: "Đang ẩn", count: tabCounts.HIDDEN },
    { key: "LOW_OOS", label: "Hết / Sắp hết", count: tabCounts.LOW_OOS },
  ];

  const indexTabs = countCards.map((c) => ({
    key: c.key,
    label: c.label,
    count: c.count !== null ? c.count : undefined,
  }));

  const totalPages = Math.max(1, Math.ceil(totalProducts / SELLER_PRODUCTS_PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const rangeStart = totalProducts === 0 ? 0 : (safePage - 1) * SELLER_PRODUCTS_PAGE_SIZE + 1;
  const rangeEnd = Math.min(totalProducts, safePage * SELLER_PRODUCTS_PAGE_SIZE);
  const pageNumbers = useMemo(() => buildPageNumbers(safePage, totalPages), [safePage, totalPages]);

  const historyTotalPages = Math.max(1, Math.ceil(historyTotal / STOCK_HISTORY_PAGE_SIZE));
  const importErrorTotalPages = Math.max(1, Math.ceil(importErrorTotal / 20));

  const handleSelectFilter = (next: SellerProductFilterKey) => {
    setFilter(next);
    setPage(1);
  };

  const toggleExpand = (productId: string) => {
    setExpandedProductIds((prev) => ({ ...prev, [productId]: !prev[productId] }));
  };

  const openStockHistory = (product: GwSellerProduct, sku: GwSellerSku) => {
    setDrawerProductId(null);
    setHistoryTarget({ product, sku });
    setHistoryPage(1);
    setHistoryChangeType("all");
    setHistoryFromDate("");
    setHistoryToDate("");
    setHistoryItems([]);
    setHistoryTotal(0);
    setHistoryError(null);
  };

  const closeStockHistory = () => {
    historyLoadSeqRef.current += 1;
    setHistoryTarget(null);
  };

  const handleUpdateSkuStock = async (sku: GwSellerSku, targetStock: number) => {
    const newStock = Math.trunc(targetStock);
    if (!Number.isFinite(newStock) || newStock < 0) {
      showToast("Số lượng tồn kho phải là số nguyên không âm.");
      return;
    }
    if (newStock < sku.reserved_stock) {
      showToast(`Tồn kho không được nhỏ hơn số lượng đang giữ (${formatInt(sku.reserved_stock)}).`);
      return;
    }
    setSavingSkuId(sku.id);
    setConflictNotice(null);
    try {
      const res = await api<{ sku?: GwSellerSku }>(
        `/api/seller/skus/${encodeURIComponent(sku.id)}/stock`,
        {
          method: "PATCH",
          body: {
            new_stock: newStock,
            expected_version: sku.version,
          },
        }
      );
      if (res.sku) {
        const updated = res.sku;
        const nextProducts = products.map((p) => ({
          ...p,
          skus: (p.skus ?? []).map((item) => (item.id === sku.id ? updated : item)),
        }));
        setProducts(nextProducts);
        setStockDrafts((prev) => ({ ...prev, [sku.id]: String(updated.stock) }));
        if (historyTarget && historyTarget.sku.id === sku.id) {
          setHistoryTarget((prev) => (prev ? { ...prev, sku: updated } : prev));
        }
        const derived =
          page === 1 && filter === "all" && categoryFilter === "all" && !debouncedQuery
            ? deriveSellerProductTabCountsFromCompleteList(nextProducts, totalProducts)
            : null;
        if (derived) {
          setTabCounts(derived);
        } else {
          void loadTabCounts();
        }
      } else {
        await refreshAll();
      }
      showToast(`Đã cập nhật tồn kho ${sku.sku_code} thành ${formatInt(newStock)} chiếc.`);
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.code === "ABORTED")) {
        const msg =
          "Dữ liệu vừa thay đổi ở phiên khác, hệ thống đã tải lại tồn kho mới nhất. Vui lòng kiểm tra và lưu lại.";
        setConflictNotice(msg);
        showToast("Dữ liệu vừa thay đổi, đang tải lại tồn kho mới nhất.");
        await refreshAll();
      } else {
        showToast(getErrorMessage(err, "Không thể cập nhật tồn kho SKU lúc này."));
      }
    } finally {
      setSavingSkuId(null);
    }
  };

  const handleDeleteProduct = async (product: GwSellerProduct) => {
    setDeletingId(product.id);
    try {
      await api(`/api/seller/products/${encodeURIComponent(product.id)}`, { method: "DELETE" });
      setConfirmDeleteProduct(null);
      showToast(`Đã xóa sản phẩm "${product.name}".`);
      if (products.length <= 1 && page > 1) {
        setPage((p) => Math.max(1, p - 1));
        void loadTabCounts();
      } else {
        await refreshAll();
      }
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xóa sản phẩm lúc này."));
    } finally {
      setDeletingId(null);
    }
  };

  // CSV Import handlers
  const resetImportJobState = () => {
    setImportJob(null);
    setImportFile(null);
    setImportFileError(null);
    setImportActionError(null);
    setImportErrorRows([]);
    setImportErrorTotal(0);
    setImportErrorPage(1);
    completedRefreshedJobIdRef.current = null;
    if (importFileInputRef.current) {
      importFileInputRef.current.value = "";
    }
  };

  const openImportModal = () => {
    if (importJob && shouldResetImportJobOnModalOpen(importJob.status)) {
      resetImportJobState();
    } else {
      setImportActionError(null);
    }
    setImportModalOpen(true);
  };

  const handleDownloadTemplate = async () => {
    setDownloadingTemplate(true);
    setImportActionError(null);
    try {
      const templatePath =
        importCategoryId !== "all"
          ? `/api/seller/products/import/template?category_id=${encodeURIComponent(importCategoryId)}`
          : "/api/seller/products/import/template";
      const blob = await apiBlob(templatePath, { headers: { Accept: "text/csv" } });
      triggerBlobDownload(blob, "nexusmart-mau-nhap-san-pham.csv");
      showToast("Đã tải tệp mẫu CSV.");
    } catch (err) {
      setImportActionError(translateProductImportError(err, "Không thể tải tệp mẫu CSV lúc này."));
    } finally {
      setDownloadingTemplate(false);
    }
  };

  const handleSelectCsvFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    setImportActionError(null);
    const chosen = e.target.files?.[0] ?? null;
    if (!chosen) {
      setImportFile(null);
      setImportFileError(null);
      return;
    }
    const validation = validateImportCsvFile(chosen);
    if (!validation.ok) {
      setImportFile(null);
      setImportFileError(validation.message);
      return;
    }
    setImportFile(chosen);
    setImportFileError(null);
  };

  const handleUploadCsv = async () => {
    if (!importFile) {
      setImportFileError("Vui lòng chọn tệp CSV trước khi tải lên.");
      return;
    }
    const validation = validateImportCsvFile(importFile);
    if (!validation.ok) {
      setImportFileError(validation.message);
      return;
    }
    setImportUploading(true);
    setImportActionError(null);
    setImportErrorRows([]);
    setImportErrorTotal(0);
    setImportErrorPage(1);
    try {
      const form = new FormData();
      form.append("file", importFile);
      const res = await api<ProductImportJobResponse>("/api/seller/products/import", {
        method: "POST",
        body: form,
      });
      if (res.job) {
        setImportJob(res.job);
        showToast("Đã tải tệp CSV lên hệ thống. Đang kiểm tra dữ liệu từng dòng.");
      }
    } catch (err) {
      setImportActionError(
        translateProductImportError(
          err,
          "Không thể tải lên tệp CSV. Kiểm tra mã hóa UTF-8, cột tiêu đề và giới hạn 1.000 dòng."
        )
      );
    } finally {
      setImportUploading(false);
    }
  };

  const handleConfirmImport = async () => {
    if (!importJob) return;
    setConfirmingImport(true);
    setImportActionError(null);
    try {
      const res = await api<ConfirmProductImportJobResponse>(
        `/api/seller/products/import/${encodeURIComponent(importJob.id)}/confirm`,
        { method: "POST" }
      );
      if (res.job) setImportJob(res.job);
      if (res.already_confirmed) {
        showToast("Đợt nhập này đã được xác nhận trước đó, hệ thống đang tiếp tục xử lý.");
      } else {
        showToast("Đã xác nhận nhập các dòng hợp lệ. Sản phẩm mới vào mục Chờ duyệt.");
      }
    } catch (err) {
      setImportActionError(translateProductImportError(err, "Chưa thể xác nhận nhập CSV lúc này."));
    } finally {
      setConfirmingImport(false);
    }
  };

  const handleDownloadImportErrors = async () => {
    if (!importJob) return;
    setDownloadingErrors(true);
    setImportActionError(null);
    try {
      const blob = await apiBlob(
        `/api/seller/products/import/${encodeURIComponent(importJob.id)}/errors.csv`,
        { headers: { Accept: "text/csv" } }
      );
      triggerBlobDownload(blob, `nexusmart-loi-nhap-csv-${importJob.id.slice(0, 8)}.csv`);
    } catch (err) {
      setImportActionError(
        translateProductImportError(err, "Không thể tải tệp danh sách lỗi lúc này.")
      );
    } finally {
      setDownloadingErrors(false);
    }
  };

  const handleViewPendingTabAfterImport = () => {
    setImportModalOpen(false);
    setFilter("PENDING");
    setCategoryFilter("all");
    setQueryInput("");
    setDebouncedQuery("");
    setPage(1);
    void loadTabCounts();
  };

  const hasActiveFilters = filter !== "all" || categoryFilter !== "all" || debouncedQuery !== "";

  return (
    <div className="space-y-4">
      <PageHeader
        title="Sản phẩm"
        subtitle={formatSellerProductsHeaderSummary(
          shop?.name || "Gian hàng",
          tabCounts,
          formatInt
        )}
        secondaryActions={
          <>
            <Button type="button" variant="secondary" size="sm" onClick={openImportModal}>
              <Upload className="size-3.5" aria-hidden="true" />
              <span>Nhập file CSV</span>
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={loading}
              onClick={() => void refreshAll()}
            >
              <RefreshCw className={cn("size-3.5", loading && "animate-spin")} aria-hidden="true" />
              <span>Làm mới</span>
            </Button>
          </>
        }
        primaryAction={
          <Button size="sm" asChild>
            <Link href="/seller/san-pham/moi">
              <Plus className="size-4" aria-hidden="true" />
              <span>Thêm sản phẩm</span>
            </Link>
          </Button>
        }
      />

      {conflictNotice && (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>{conflictNotice}</span>
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => setConflictNotice(null)}
            >
              Đã hiểu
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {(uiState === "loading" || (!uiState && loading && products.length === 0)) && (
        <Card role="status" aria-label="Đang tải sản phẩm...">
          <CardContent className="space-y-3 py-6">
            <Skeleton className="h-6 w-48" />
            {Array.from({ length: 5 }).map((_, idx) => (
              <Skeleton key={idx} className="h-11 w-full" />
            ))}
          </CardContent>
        </Card>
      )}

      {(uiState === "error" || (errorMsg && !uiState)) && (
        <ErrorState
          title="Không thể tải sản phẩm"
          description={errorMsg ?? "Dịch vụ kho hàng chưa phản hồi."}
          onRetry={() => void refreshAll()}
        />
      )}

      {uiState === "empty" && (
        <EmptyState
          icon={Package}
          title="Gian hàng chưa có sản phẩm nào."
          description="Nhấn Thêm sản phẩm hoặc Nhập file CSV để đăng bán sản phẩm đầu tiên."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/san-pham/moi">Thêm sản phẩm</Link>
            </Button>
          }
        />
      )}

      {!uiState && !(loading && products.length === 0) && !errorMsg && (
        <>
          <MetricStrip
            items={[
              {
                label: "Đang bán",
                value: tabCounts.APPROVED !== null ? formatInt(tabCounts.APPROVED) : "—",
              },
              {
                label: "Hết / Sắp hết",
                value: tabCounts.LOW_OOS !== null ? formatInt(tabCounts.LOW_OOS) : "—",
                hint: "Tồn khả dụng ≤ 5",
              },
              {
                label: "Chờ duyệt",
                value: tabCounts.PENDING !== null ? formatInt(tabCounts.PENDING) : "—",
              },
            ]}
          />

          <section aria-label="Danh sách sản phẩm">
            <IndexTable
              tabs={indexTabs}
              activeTab={filter}
              onTabChange={(k) => handleSelectFilter(k as SellerProductFilterKey)}
              toolbar={
                <Button
                  type="button"
                  variant={showFilters ? "secondary" : "outline"}
                  size="icon-sm"
                  aria-label="Mở hoặc ẩn thanh tìm kiếm và bộ lọc"
                  aria-pressed={showFilters}
                  onClick={() => setShowFilters((prev) => !prev)}
                >
                  <SlidersHorizontal className="size-4" aria-hidden="true" />
                </Button>
              }
              footer={
                <>
                  <span className="tabular-nums">
                    {loading
                      ? "Đang cập nhật..."
                      : totalProducts === 0
                        ? "0 sản phẩm"
                        : `Hiển thị ${formatInt(rangeStart)}–${formatInt(rangeEnd)} / ${formatInt(totalProducts)} sản phẩm`}
                  </span>
                  {totalPages > 1 && (
                    <nav
                      className="flex flex-wrap items-center gap-1.5"
                      aria-label="Phân trang sản phẩm"
                    >
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        disabled={safePage <= 1 || loading}
                        onClick={() => setPage((p) => Math.max(1, p - 1))}
                      >
                        Trước
                      </Button>
                      {pageNumbers.map((item, idx) =>
                        item === "ellipsis" ? (
                          <span key={`e-${idx}`} className="px-1 text-xs text-muted-foreground">
                            …
                          </span>
                        ) : (
                          <Button
                            key={item}
                            type="button"
                            variant={item === safePage ? "secondary" : "outline"}
                            size="xs"
                            aria-current={item === safePage ? "page" : undefined}
                            disabled={loading}
                            onClick={() => setPage(item)}
                            className="min-w-7 tabular-nums"
                          >
                            {item}
                          </Button>
                        )
                      )}
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        disabled={safePage >= totalPages || loading}
                        onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                      >
                        Sau
                      </Button>
                    </nav>
                  )}
                </>
              }
            >
              {showFilters && (
                <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-3 py-2.5 md:px-4">
                  <div className="relative min-w-[200px] flex-1 sm:max-w-xs">
                    <Search
                      className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
                      aria-hidden="true"
                    />
                    <Input
                      type="search"
                      value={queryInput}
                      onChange={(e) => {
                        setQueryInput(e.target.value);
                        setPage(1);
                      }}
                      placeholder="Tìm tên sản phẩm hoặc mã SKU..."
                      aria-label="Tìm sản phẩm hoặc mã SKU"
                      className="h-8 bg-card pl-8"
                    />
                  </div>
                  <Select
                    value={categoryFilter}
                    onValueChange={(v) => {
                      setCategoryFilter(v);
                      setPage(1);
                    }}
                  >
                    <SelectTrigger
                      size="sm"
                      aria-label="Lọc theo danh mục"
                      className="w-full bg-card sm:w-52"
                    >
                      <SelectValue placeholder="Mọi danh mục">
                        {categoryFilter === "all"
                          ? "Mọi danh mục"
                          : (categories.find((cat) => cat.id === categoryFilter)?.name ??
                            "Mọi danh mục")}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">Mọi danh mục</SelectItem>
                      {categories.map((cat) => (
                        <SelectItem key={cat.id} value={cat.id}>
                          {cat.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {hasActiveFilters && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setFilter("all");
                        setCategoryFilter("all");
                        setQueryInput("");
                        setDebouncedQuery("");
                        setPage(1);
                      }}
                    >
                      Xóa bộ lọc
                    </Button>
                  )}
                </div>
              )}

              {products.length === 0 ? (
                <div className="p-4">
                  <EmptyState
                    icon={Package}
                    title={
                      !hasActiveFilters && tabCounts.all === 0
                        ? "Gian hàng chưa có sản phẩm nào"
                        : "Không có sản phẩm khớp bộ lọc hiện tại"
                    }
                    description={
                      !hasActiveFilters && tabCounts.all === 0
                        ? "Nhấn Thêm sản phẩm hoặc Nhập file CSV để bắt đầu."
                        : "Thử thay đổi từ khóa tìm kiếm, danh mục hoặc chuyển tab trạng thái."
                    }
                    action={
                      !hasActiveFilters && tabCounts.all === 0 ? (
                        <div className="flex flex-wrap items-center justify-center gap-2">
                          <Button size="sm" asChild>
                            <Link href="/seller/san-pham/moi">Thêm sản phẩm</Link>
                          </Button>
                          <Button size="sm" variant="secondary" onClick={openImportModal}>
                            Nhập file CSV
                          </Button>
                        </div>
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setFilter("all");
                            setCategoryFilter("all");
                            setQueryInput("");
                            setDebouncedQuery("");
                            setPage(1);
                          }}
                        >
                          Xóa bộ lọc
                        </Button>
                      )
                    }
                    className="shadow-none"
                  />
                </div>
              ) : (
                <>
                  {/* Desktop Table */}
                  <div className="hidden lg:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Sản phẩm</TableHead>
                          <TableHead>Trạng thái</TableHead>
                          <TableHead>Tồn kho</TableHead>
                          <TableHead>Danh mục</TableHead>
                          <TableHead className="text-right">Giá bán</TableHead>
                          <TableHead className="text-right">Thao tác</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {products.map((p) => {
                          const allSkus = p.skus ?? [];
                          const availStock = allSkus.reduce(
                            (sum, s) => sum + Math.max(0, s.available_stock),
                            0
                          );
                          const totalStock = allSkus.reduce(
                            (sum, s) => sum + Math.max(0, s.stock),
                            0
                          );
                          const stMeta = getSellerProductStatusMeta(p.status);
                          const isExpanded = Boolean(expandedProductIds[p.id]);
                          const catName = categoryNameById.get(p.category_id) ?? "Chưa phân loại";
                          const cover = getCoverImage(p);

                          return (
                            <React.Fragment key={p.id}>
                              <TableRow
                                className="cursor-pointer"
                                onClick={() => setDrawerProductId(p.id)}
                              >
                                <TableCell className="max-w-[300px] whitespace-normal">
                                  <div className="flex items-center gap-3">
                                    <img
                                      src={cover}
                                      alt={p.name}
                                      className="size-10 shrink-0 rounded-md border border-border bg-muted object-cover"
                                      width={40}
                                      height={40}
                                    />
                                    <div className="min-w-0">
                                      <button
                                        type="button"
                                        className="line-clamp-2 text-left font-medium text-foreground hover:text-brand hover:underline"
                                        title={p.name}
                                        onClick={(e) => {
                                          e.stopPropagation();
                                          setDrawerProductId(p.id);
                                        }}
                                      >
                                        {p.name}
                                      </button>
                                      <div className="truncate text-xs text-muted-foreground">
                                        {formatInt(allSkus.length)} SKU
                                        {p.slug ? ` · /${p.slug}` : ""}
                                      </div>
                                      {p.status === "REJECTED" && p.rejection_reason && (
                                        <div className="mt-0.5 text-xs text-destructive">
                                          Từ chối: {p.rejection_reason}
                                        </div>
                                      )}
                                    </div>
                                  </div>
                                </TableCell>
                                <TableCell>
                                  <StatusBadge tone={productStatusTone(p.status)}>
                                    {stMeta.label}
                                  </StatusBadge>
                                </TableCell>
                                <TableCell onClick={(e) => e.stopPropagation()}>
                                  <div className="flex items-center gap-2">
                                    <span
                                      className={cn(
                                        "tabular-nums",
                                        availStock === 0
                                          ? "font-semibold text-destructive"
                                          : "text-foreground"
                                      )}
                                    >
                                      {availStock === 0
                                        ? `0 còn / ${formatInt(totalStock)}`
                                        : `${formatInt(availStock)} còn / ${formatInt(totalStock)}`}
                                    </span>
                                    <Button
                                      type="button"
                                      variant="ghost"
                                      size="xs"
                                      aria-expanded={isExpanded}
                                      onClick={() => toggleExpand(p.id)}
                                    >
                                      <span>{isExpanded ? "Đóng tồn" : "Sửa tồn"}</span>
                                      {isExpanded ? (
                                        <ChevronUp className="size-3.5" aria-hidden="true" />
                                      ) : (
                                        <ChevronDown className="size-3.5" aria-hidden="true" />
                                      )}
                                    </Button>
                                  </div>
                                </TableCell>
                                <TableCell className="max-w-[160px] truncate text-muted-foreground">
                                  <span title={catName}>{catName}</span>
                                </TableCell>
                                <TableCell className="text-right font-medium tabular-nums">
                                  {formatPriceRange(p)}
                                </TableCell>
                                <TableCell
                                  className="text-right"
                                  onClick={(e) => e.stopPropagation()}
                                >
                                  <div className="inline-flex items-center justify-end gap-1">
                                    <Button variant="ghost" size="xs" asChild>
                                      <Link href={`/seller/san-pham/${p.id}`}>Sửa</Link>
                                    </Button>
                                    <Button
                                      type="button"
                                      variant="ghost"
                                      size="xs"
                                      className="text-destructive hover:text-destructive"
                                      onClick={() => setConfirmDeleteProduct(p)}
                                    >
                                      Xóa
                                    </Button>
                                  </div>
                                </TableCell>
                              </TableRow>

                              {isExpanded && (
                                <TableRow className="bg-muted/45 hover:bg-muted/45">
                                  <TableCell colSpan={6} className="py-3 whitespace-normal">
                                    <div className="space-y-2 pl-2 lg:pl-12">
                                      {allSkus.length === 0 ? (
                                        <span className="text-xs text-muted-foreground">
                                          Sản phẩm chưa có biến thể SKU.
                                        </span>
                                      ) : (
                                        allSkus.map((sku) => {
                                          const draftVal = stockDrafts[sku.id] ?? String(sku.stock);
                                          const isSaving = savingSkuId === sku.id;
                                          return (
                                            <div
                                              key={sku.id}
                                              className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-card px-3 py-2"
                                            >
                                              <div className="min-w-0">
                                                <div className="flex flex-wrap items-center gap-2 text-xs">
                                                  <Badge
                                                    variant="secondary"
                                                    className="rounded-md font-mono"
                                                  >
                                                    {sku.sku_code}
                                                  </Badge>
                                                  <span className="font-medium text-foreground">
                                                    {sku.name}
                                                  </span>
                                                </div>
                                                <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                                                  Giá {formatVnd(sku.price_vnd)} · Khả dụng{" "}
                                                  <span
                                                    className={cn(
                                                      sku.available_stock <= 0 &&
                                                        "font-semibold text-destructive"
                                                    )}
                                                  >
                                                    {formatInt(sku.available_stock)}
                                                  </span>{" "}
                                                  · Giữ {formatInt(sku.reserved_stock)} · Tổng{" "}
                                                  {formatInt(sku.stock)}
                                                </div>
                                              </div>
                                              <div className="flex flex-wrap items-center gap-2">
                                                <Input
                                                  type="number"
                                                  min={sku.reserved_stock}
                                                  step={1}
                                                  value={draftVal}
                                                  onChange={(e) =>
                                                    setStockDrafts((prev) => ({
                                                      ...prev,
                                                      [sku.id]: e.target.value,
                                                    }))
                                                  }
                                                  aria-label={`Tồn kho tổng của ${sku.sku_code}`}
                                                  className="h-8 w-20 bg-card text-right tabular-nums"
                                                />
                                                <Button
                                                  type="button"
                                                  variant="outline"
                                                  size="xs"
                                                  disabled={isSaving}
                                                  onClick={() =>
                                                    void handleUpdateSkuStock(sku, Number(draftVal))
                                                  }
                                                >
                                                  {isSaving ? "Đang lưu..." : "Lưu"}
                                                </Button>
                                                <Button
                                                  type="button"
                                                  variant="ghost"
                                                  size="xs"
                                                  onClick={() => openStockHistory(p, sku)}
                                                >
                                                  <History
                                                    className="size-3.5"
                                                    aria-hidden="true"
                                                  />
                                                  <span>Lịch sử tồn</span>
                                                </Button>
                                              </div>
                                            </div>
                                          );
                                        })
                                      )}
                                    </div>
                                  </TableCell>
                                </TableRow>
                              )}
                            </React.Fragment>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  {/* Mobile Product Cards */}
                  <div className="divide-y divide-border lg:hidden">
                    {products.map((p) => {
                      const allSkus = p.skus ?? [];
                      const availStock = allSkus.reduce(
                        (sum, s) => sum + Math.max(0, s.available_stock),
                        0
                      );
                      const stMeta = getSellerProductStatusMeta(p.status);
                      const isExpanded = Boolean(expandedProductIds[p.id]);
                      const cover = getCoverImage(p);
                      return (
                        <div key={p.id} className="space-y-2.5 px-4 py-3">
                          <div className="flex items-start gap-3">
                            <img
                              src={cover}
                              alt={p.name}
                              className="size-11 shrink-0 rounded-md border border-border bg-muted object-cover"
                              width={44}
                              height={44}
                            />
                            <div className="min-w-0 flex-1">
                              <div className="flex items-start justify-between gap-2">
                                <button
                                  type="button"
                                  onClick={() => setDrawerProductId(p.id)}
                                  className="line-clamp-2 text-left text-sm font-medium text-foreground"
                                >
                                  {p.name}
                                </button>
                                <span className="shrink-0 text-sm font-semibold tabular-nums">
                                  {formatVnd(p.min_price_vnd)}
                                </span>
                              </div>
                              <div className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground tabular-nums">
                                <span
                                  className={cn(
                                    availStock === 0 && "font-semibold text-destructive"
                                  )}
                                >
                                  Khả dụng {formatInt(availStock)}
                                </span>
                                <span>·</span>
                                <span>{formatInt(allSkus.length)} SKU</span>
                              </div>
                              {p.status === "REJECTED" && p.rejection_reason && (
                                <div className="mt-1 text-xs text-destructive">
                                  Từ chối: {p.rejection_reason}
                                </div>
                              )}
                            </div>
                          </div>

                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <StatusBadge tone={productStatusTone(p.status)}>
                              {stMeta.label}
                            </StatusBadge>
                            <div className="flex items-center gap-2">
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="min-h-10"
                                aria-expanded={isExpanded}
                                onClick={() => toggleExpand(p.id)}
                              >
                                {isExpanded ? "Đóng tồn" : "Sửa tồn"}
                              </Button>
                              <Button variant="ghost" size="sm" className="min-h-10" asChild>
                                <Link href={`/seller/san-pham/${p.id}`}>Sửa</Link>
                              </Button>
                            </div>
                          </div>

                          {isExpanded && allSkus.length > 0 && (
                            <div className="space-y-2 rounded-lg border border-border bg-muted/40 p-2.5">
                              {allSkus.map((sku) => {
                                const draftVal = stockDrafts[sku.id] ?? String(sku.stock);
                                const isSaving = savingSkuId === sku.id;
                                return (
                                  <div
                                    key={sku.id}
                                    className="space-y-2 rounded-md border border-border bg-card p-2.5"
                                  >
                                    <div>
                                      <div className="font-mono text-xs font-semibold text-foreground">
                                        {sku.sku_code}
                                      </div>
                                      <div className="text-xs text-muted-foreground tabular-nums">
                                        {sku.name} · Khả dụng{" "}
                                        <span
                                          className={cn(
                                            sku.available_stock <= 0 &&
                                              "font-semibold text-destructive"
                                          )}
                                        >
                                          {formatInt(sku.available_stock)}
                                        </span>
                                      </div>
                                    </div>
                                    <div className="flex flex-wrap items-center gap-2">
                                      <Input
                                        type="number"
                                        min={sku.reserved_stock}
                                        step={1}
                                        value={draftVal}
                                        onChange={(e) =>
                                          setStockDrafts((prev) => ({
                                            ...prev,
                                            [sku.id]: e.target.value,
                                          }))
                                        }
                                        aria-label={`Tồn kho của ${sku.sku_code}`}
                                        className="h-10 w-24 text-right tabular-nums"
                                      />
                                      <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        className="min-h-10"
                                        disabled={isSaving}
                                        onClick={() =>
                                          void handleUpdateSkuStock(sku, Number(draftVal))
                                        }
                                      >
                                        {isSaving ? "..." : "Lưu"}
                                      </Button>
                                      <Button
                                        type="button"
                                        variant="ghost"
                                        size="sm"
                                        className="min-h-10"
                                        onClick={() => openStockHistory(p, sku)}
                                      >
                                        Lịch sử tồn
                                      </Button>
                                    </div>
                                  </div>
                                );
                              })}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </IndexTable>
          </section>

          {/* Low Stock Quick Restock Card */}
          {lowStockRows.length > 0 && (
            <Card aria-labelledby="restock-heading">
              <CardHeader>
                <CardTitle id="restock-heading">
                  SKU cần nhập kho ({formatInt(lowStockRows.length)})
                </CardTitle>
                <CardDescription>Tồn khả dụng ≤ 5 trên trang hiện tại</CardDescription>
              </CardHeader>
              <CardContent className="px-0 pb-0">
                <div className="hidden lg:block">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Sản phẩm &amp; biến thể</TableHead>
                        <TableHead>Mã SKU</TableHead>
                        <TableHead className="text-right">Đơn giá</TableHead>
                        <TableHead className="text-right">Tồn khả dụng</TableHead>
                        <TableHead className="text-right">Thao tác</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {lowStockRows.map(({ product, sku }) => {
                        const isSaving = savingSkuId === sku.id;
                        return (
                          <TableRow key={sku.id}>
                            <TableCell className="max-w-[280px] whitespace-normal">
                              <div className="flex items-center gap-3">
                                <img
                                  src={sku.image_url || getCoverImage(product)}
                                  alt={product.name}
                                  className="size-9 shrink-0 rounded-md border border-border bg-muted object-cover"
                                  width={36}
                                  height={36}
                                />
                                <div className="min-w-0">
                                  <Link
                                    href={`/seller/san-pham/${product.id}`}
                                    className="line-clamp-1 font-medium text-foreground hover:text-brand hover:underline"
                                  >
                                    {product.name}
                                  </Link>
                                  <div className="text-xs text-muted-foreground">
                                    {sku.name || sku.sku_code}
                                  </div>
                                </div>
                              </div>
                            </TableCell>
                            <TableCell className="font-mono text-xs">{sku.sku_code}</TableCell>
                            <TableCell className="text-right tabular-nums">
                              {formatVnd(sku.price_vnd)}
                            </TableCell>
                            <TableCell className="text-right tabular-nums">
                              <span
                                className={cn(
                                  "font-medium",
                                  sku.available_stock <= 0 ? "text-destructive" : "text-foreground"
                                )}
                              >
                                {sku.available_stock <= 0
                                  ? "Hết hàng"
                                  : `Còn ${formatInt(sku.available_stock)}`}
                              </span>
                            </TableCell>
                            <TableCell className="text-right">
                              <div className="inline-flex items-center justify-end gap-1">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="xs"
                                  disabled={isSaving}
                                  onClick={() => void handleUpdateSkuStock(sku, sku.stock + 10)}
                                >
                                  {isSaving ? "Đang lưu..." : "+10 tồn"}
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="xs"
                                  onClick={() => openStockHistory(product, sku)}
                                >
                                  Lịch sử tồn
                                </Button>
                                <Button variant="ghost" size="xs" asChild>
                                  <Link href={`/seller/san-pham/${product.id}`}>Sửa</Link>
                                </Button>
                              </div>
                            </TableCell>
                          </TableRow>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                <div className="divide-y divide-border lg:hidden">
                  {lowStockRows.map(({ product, sku }) => {
                    const isSaving = savingSkuId === sku.id;
                    return (
                      <div key={sku.id} className="space-y-2 px-4 py-3">
                        <div className="flex items-start justify-between gap-2">
                          <div className="min-w-0">
                            <Link
                              href={`/seller/san-pham/${product.id}`}
                              className="line-clamp-1 text-sm font-medium text-foreground"
                            >
                              {product.name} ({sku.name || sku.sku_code})
                            </Link>
                            <div className="font-mono text-xs text-muted-foreground">
                              {sku.sku_code}
                            </div>
                          </div>
                          <span
                            className={cn(
                              "shrink-0 text-xs font-semibold tabular-nums",
                              sku.available_stock <= 0 ? "text-destructive" : "text-foreground"
                            )}
                          >
                            {sku.available_stock <= 0
                              ? "Hết hàng"
                              : `Còn ${formatInt(sku.available_stock)}`}
                          </span>
                        </div>
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="text-xs text-muted-foreground tabular-nums">
                            {formatVnd(sku.price_vnd)}
                          </span>
                          <div className="flex items-center gap-2">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              className="min-h-10"
                              disabled={isSaving}
                              onClick={() => void handleUpdateSkuStock(sku, sku.stock + 10)}
                            >
                              {isSaving ? "..." : "+10 tồn"}
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="min-h-10"
                              onClick={() => openStockHistory(product, sku)}
                            >
                              Lịch sử tồn
                            </Button>
                            <Button variant="ghost" size="sm" className="min-h-10" asChild>
                              <Link href={`/seller/san-pham/${product.id}`}>Sửa</Link>
                            </Button>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </CardContent>
            </Card>
          )}
        </>
      )}

      {/* Slide-over Quick SKU Inventory Sheet */}
      <Sheet
        open={Boolean(drawerProduct)}
        onOpenChange={(open) => !open && setDrawerProductId(null)}
      >
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-md">
          {drawerProduct ? (
            <>
              <SheetHeader>
                <div className="flex flex-wrap items-center gap-2 pr-6">
                  <SheetTitle className="text-base">{drawerProduct.name}</SheetTitle>
                  <StatusBadge tone={productStatusTone(drawerProduct.status)}>
                    {getSellerProductStatusMeta(drawerProduct.status).label}
                  </StatusBadge>
                </div>
                <SheetDescription>
                  {categoryNameById.get(drawerProduct.category_id) ?? "Chưa phân loại"} ·{" "}
                  {formatInt((drawerProduct.skus ?? []).filter((s) => s.is_active).length)} SKU
                </SheetDescription>
              </SheetHeader>

              <div className="flex-1 divide-y divide-border px-4">
                {(drawerProduct.skus ?? [])
                  .filter((s) => s.is_active)
                  .map((sku) => (
                    <div
                      key={sku.id}
                      className="flex items-center justify-between gap-3 py-3 text-sm"
                    >
                      <div className="min-w-0">
                        <div className="truncate font-medium text-foreground">
                          {sku.name || sku.sku_code}
                        </div>
                        <div className="text-xs text-muted-foreground tabular-nums">
                          {sku.sku_code} · {formatVnd(sku.price_vnd)}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <span
                          className={cn(
                            "text-xs font-medium tabular-nums",
                            sku.available_stock <= 0
                              ? "text-destructive"
                              : sku.available_stock <= 5
                                ? "text-badge-warning-ink"
                                : "text-foreground"
                          )}
                        >
                          {sku.available_stock <= 0
                            ? "Hết hàng"
                            : `Còn ${formatInt(sku.available_stock)}`}
                        </span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="xs"
                          onClick={() => openStockHistory(drawerProduct, sku)}
                        >
                          Lịch sử
                        </Button>
                      </div>
                    </div>
                  ))}
              </div>

              <SheetFooter className="flex-row items-center justify-between gap-2 border-t border-border">
                <span className="text-xs text-muted-foreground tabular-nums">
                  Tổng khả dụng: {formatInt(totalAvailable(drawerProduct))}
                </span>
                <Button size="sm" asChild>
                  <Link
                    href={`/seller/san-pham/${drawerProduct.id}`}
                    onClick={() => setDrawerProductId(null)}
                  >
                    <span>Chỉnh sửa sản phẩm</span>
                    <ArrowUpRight className="size-4" aria-hidden="true" />
                  </Link>
                </Button>
              </SheetFooter>
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      {/* S03 Stock History Dialog */}
      <Dialog
        open={Boolean(historyTarget)}
        onOpenChange={(open) => {
          if (!open) closeStockHistory();
        }}
      >
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
          {historyTarget ? (
            <>
              <DialogHeader>
                <DialogTitle>Lịch sử tồn kho SKU</DialogTitle>
                <DialogDescription className="space-y-0.5">
                  <span className="block">
                    {historyTarget.sku.sku_code} · {historyTarget.sku.name} —{" "}
                    {historyTarget.product.name}
                  </span>
                  <span className="block tabular-nums">
                    Hiện tại: Tổng {formatInt(historyTarget.sku.stock)} · Khả dụng{" "}
                    {formatInt(historyTarget.sku.available_stock)} · Đang giữ{" "}
                    {formatInt(historyTarget.sku.reserved_stock)}
                  </span>
                </DialogDescription>
              </DialogHeader>

              <div className="flex flex-wrap items-center gap-2">
                <Select
                  value={historyChangeType}
                  onValueChange={(v) => {
                    setHistoryChangeType(v);
                    setHistoryPage(1);
                  }}
                >
                  <SelectTrigger
                    size="sm"
                    aria-label="Lọc loại biến động tồn kho"
                    className="w-48 bg-card"
                  >
                    <SelectValue>
                      {historyChangeType === "all"
                        ? "Mọi loại biến động"
                        : historyChangeType === "MANUAL_UPDATE"
                          ? "Cập nhật thủ công"
                          : historyChangeType === "RESERVE"
                            ? "Giữ tồn đặt đơn"
                            : historyChangeType === "RELEASE"
                              ? "Hoàn tồn hủy đơn"
                              : historyChangeType === "CONFIRM"
                                ? "Xuất kho xác nhận"
                                : historyChangeType === "RESTOCK"
                                  ? "Nhập lại kho"
                                  : historyChangeType}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">Mọi loại biến động</SelectItem>
                    <SelectItem value="MANUAL_UPDATE">Cập nhật thủ công</SelectItem>
                    <SelectItem value="RESERVE">Giữ tồn đặt đơn</SelectItem>
                    <SelectItem value="RELEASE">Hoàn tồn hủy đơn</SelectItem>
                    <SelectItem value="CONFIRM">Xuất kho xác nhận</SelectItem>
                    <SelectItem value="RESTOCK">Nhập lại kho</SelectItem>
                  </SelectContent>
                </Select>

                <Input
                  type="date"
                  value={historyFromDate}
                  onChange={(e) => {
                    setHistoryFromDate(e.target.value);
                    setHistoryPage(1);
                  }}
                  aria-label="Từ ngày"
                  className="h-8 w-36"
                />
                <Input
                  type="date"
                  value={historyToDate}
                  onChange={(e) => {
                    setHistoryToDate(e.target.value);
                    setHistoryPage(1);
                  }}
                  aria-label="Đến ngày"
                  className="h-8 w-36"
                />
                {(historyChangeType !== "all" || historyFromDate || historyToDate) && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setHistoryChangeType("all");
                      setHistoryFromDate("");
                      setHistoryToDate("");
                      setHistoryPage(1);
                    }}
                  >
                    Đặt lại
                  </Button>
                )}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={historyLoading}
                  onClick={() => void loadStockHistory()}
                >
                  Làm mới
                </Button>
              </div>

              {historyError && (
                <Alert variant="destructive">
                  <AlertTriangle className="size-4" aria-hidden="true" />
                  <AlertDescription>{historyError}</AlertDescription>
                </Alert>
              )}

              {historyLoading ? (
                <div className="space-y-2 py-2" role="status">
                  {Array.from({ length: 4 }).map((_, idx) => (
                    <Skeleton key={idx} className="h-9 w-full" />
                  ))}
                </div>
              ) : historyItems.length === 0 ? (
                <p className="py-6 text-center text-sm text-muted-foreground">
                  Chưa có bản ghi biến động tồn kho khớp bộ lọc.
                </p>
              ) : (
                <>
                  <div className="hidden overflow-hidden rounded-lg border border-border md:block">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Thời điểm</TableHead>
                          <TableHead>Loại biến động</TableHead>
                          <TableHead className="text-right">Thay đổi</TableHead>
                          <TableHead className="text-right">Tổng tồn</TableHead>
                          <TableHead className="text-right">Đang giữ</TableHead>
                          <TableHead>Lý do / Đơn hàng</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {historyItems.map((item) => {
                          const meta = STOCK_HISTORY_CHANGE_META[item.change_type] ?? {
                            label: item.change_type,
                            tone: "neutral" as const,
                          };
                          return (
                            <TableRow key={item.id}>
                              <TableCell className="text-xs text-muted-foreground tabular-nums">
                                {item.created_at ? formatDateTimeVi(item.created_at) : "—"}
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={mapLegacyTone(meta.tone)}>
                                  {meta.label}
                                </StatusBadge>
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                <strong
                                  className={cn(
                                    item.delta > 0
                                      ? "text-badge-success-ink"
                                      : item.delta < 0
                                        ? "text-destructive"
                                        : "text-foreground"
                                  )}
                                >
                                  {formatSignedInt(item.delta)}
                                </strong>
                              </TableCell>
                              <TableCell className="text-right text-xs tabular-nums">
                                {formatInt(item.stock_before)} → {formatInt(item.stock_after)}
                              </TableCell>
                              <TableCell className="text-right text-xs tabular-nums">
                                {formatInt(item.reserved_before)} → {formatInt(item.reserved_after)}
                              </TableCell>
                              <TableCell className="whitespace-normal text-xs">
                                <span>
                                  {formatStockHistoryReason(item.change_type, item.reason)}
                                </span>
                                {item.order_id && (
                                  <>
                                    {" · "}
                                    <Link
                                      href={`/seller/don-hang/${item.order_id}`}
                                      className="font-medium text-brand hover:underline"
                                      onClick={closeStockHistory}
                                    >
                                      Xem đơn
                                    </Link>
                                  </>
                                )}
                              </TableCell>
                            </TableRow>
                          );
                        })}
                      </TableBody>
                    </Table>
                  </div>

                  <div className="divide-y divide-border rounded-lg border border-border md:hidden">
                    {historyItems.map((item) => {
                      const meta = STOCK_HISTORY_CHANGE_META[item.change_type] ?? {
                        label: item.change_type,
                        tone: "neutral" as const,
                      };
                      return (
                        <div key={item.id} className="space-y-1.5 p-3">
                          <div className="flex items-center justify-between gap-2">
                            <StatusBadge tone={mapLegacyTone(meta.tone)}>{meta.label}</StatusBadge>
                            <strong
                              className={cn(
                                "text-sm tabular-nums",
                                item.delta > 0
                                  ? "text-badge-success-ink"
                                  : item.delta < 0
                                    ? "text-destructive"
                                    : "text-foreground"
                              )}
                            >
                              {formatSignedInt(item.delta)}
                            </strong>
                          </div>
                          <div className="text-xs text-muted-foreground tabular-nums">
                            {item.created_at ? formatDateTimeVi(item.created_at) : "—"} · Tổng{" "}
                            {formatInt(item.stock_before)} → {formatInt(item.stock_after)}
                          </div>
                          <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                            <span>{formatStockHistoryReason(item.change_type, item.reason)}</span>
                            {item.order_id && (
                              <Link
                                href={`/seller/don-hang/${item.order_id}`}
                                className="font-medium text-brand hover:underline"
                                onClick={closeStockHistory}
                              >
                                Xem đơn
                              </Link>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>

                  {historyTotalPages > 1 && (
                    <div className="flex flex-wrap items-center justify-between gap-2 pt-2 text-xs text-muted-foreground tabular-nums">
                      <span>
                        Trang {formatInt(historyPage)} / {formatInt(historyTotalPages)} ·{" "}
                        {formatInt(historyTotal)} bản ghi
                      </span>
                      <div className="flex items-center gap-1.5">
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          disabled={historyPage <= 1 || historyLoading}
                          onClick={() => setHistoryPage((p) => Math.max(1, p - 1))}
                        >
                          Trước
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          disabled={historyPage >= historyTotalPages || historyLoading}
                          onClick={() => setHistoryPage((p) => Math.min(historyTotalPages, p + 1))}
                        >
                          Sau
                        </Button>
                      </div>
                    </div>
                  )}
                </>
              )}
            </>
          ) : null}
        </DialogContent>
      </Dialog>

      {/* UC-S13 2-Step CSV Import Dialog */}
      <Dialog
        open={importModalOpen}
        onOpenChange={(open) => {
          if (!open && !importUploading && !confirmingImport) {
            setImportModalOpen(false);
          }
        }}
      >
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>Nhập sản phẩm hàng loạt bằng file CSV</DialogTitle>
            <DialogDescription>
              Tải tệp lên để kiểm tra từng dòng, sau đó xác nhận nhập các dòng hợp lệ.
            </DialogDescription>
          </DialogHeader>

          <Alert>
            <Info className="size-4" aria-hidden="true" />
            <AlertDescription>
              <p>
                Chỉ hỗ trợ <strong>tạo mới sản phẩm</strong>. Tệp CSV UTF-8 tối đa{" "}
                <strong>1 MB</strong> và{" "}
                <strong>{formatInt(MAX_IMPORT_CSV_ROWS)} dòng dữ liệu</strong>. Sản phẩm tạo thành
                công vào tab <strong>Chờ duyệt</strong>.
              </p>
            </AlertDescription>
          </Alert>

          {importActionError && (
            <Alert variant="destructive">
              <AlertTriangle className="size-4" aria-hidden="true" />
              <AlertDescription>{importActionError}</AlertDescription>
            </Alert>
          )}

          {!importJob ? (
            <div className="space-y-4">
              <div className="space-y-2 rounded-lg border border-border bg-muted/35 p-3.5">
                <h3 className="text-sm font-semibold text-foreground">Bước 1: Tải tệp mẫu CSV</h3>
                <div className="flex flex-wrap items-center gap-2">
                  <Select value={importCategoryId} onValueChange={setImportCategoryId}>
                    <SelectTrigger
                      size="sm"
                      aria-label="Chọn danh mục điền sẵn trong mẫu CSV"
                      className="w-full bg-card sm:w-64"
                    >
                      <SelectValue>
                        {importCategoryId === "all"
                          ? "Không điền sẵn danh mục"
                          : (categories.find((cat) => cat.id === importCategoryId)?.name ??
                            "Không điền sẵn danh mục")}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">Không điền sẵn danh mục</SelectItem>
                      {categories
                        .filter((c) => !c.is_prohibited)
                        .map((cat) => (
                          <SelectItem key={cat.id} value={cat.id}>
                            {cat.name}
                          </SelectItem>
                        ))}
                    </SelectContent>
                  </Select>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={downloadingTemplate}
                    onClick={() => void handleDownloadTemplate()}
                  >
                    <Download className="size-3.5" aria-hidden="true" />
                    <span>{downloadingTemplate ? "Đang tải mẫu..." : "Tải mẫu CSV"}</span>
                  </Button>
                </div>
              </div>

              <div className="space-y-2 rounded-lg border border-border bg-muted/35 p-3.5">
                <h3 className="text-sm font-semibold text-foreground">
                  Bước 2: Chọn tệp CSV và kiểm tra dữ liệu
                </h3>
                <div className="space-y-2 rounded-lg border border-dashed border-input bg-card p-4">
                  <Input
                    ref={importFileInputRef}
                    type="file"
                    accept=".csv,text/csv"
                    onChange={handleSelectCsvFile}
                    aria-label="Chọn tệp CSV sản phẩm"
                    className="cursor-pointer"
                  />
                  {importFile && (
                    <p className="text-xs text-muted-foreground">
                      Đã chọn: <strong className="text-foreground">{importFile.name}</strong> (
                      {Math.max(1, Math.round(importFile.size / 1024))} KB)
                    </p>
                  )}
                  {importFileError && (
                    <p className="text-xs text-destructive" role="alert">
                      {importFileError}
                    </p>
                  )}
                </div>
              </div>

              <DialogFooter>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={importUploading}
                  onClick={() => setImportModalOpen(false)}
                >
                  Hủy
                </Button>
                <Button
                  type="button"
                  size="sm"
                  disabled={!importFile || importUploading}
                  onClick={() => void handleUploadCsv()}
                >
                  {importUploading ? "Đang tải lên..." : "Tải lên & Kiểm tra"}
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="flex flex-wrap items-start justify-between gap-2 rounded-lg border border-border bg-muted/35 p-3">
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold text-foreground">
                    {importJob.filename || "Tệp CSV đã tải lên"}
                  </div>
                  <div className="text-xs text-muted-foreground">Mã đợt nhập: {importJob.id}</div>
                </div>
                <div className="flex items-center gap-2">
                  <StatusBadge
                    tone={mapLegacyTone(
                      (IMPORT_JOB_STATUS_META[importJob.status] ?? { tone: "neutral" as const })
                        .tone
                    )}
                  >
                    {
                      (IMPORT_JOB_STATUS_META[importJob.status] ?? { label: importJob.status })
                        .label
                    }
                  </StatusBadge>
                  {!isImportJobPollingStatus(importJob.status) && (
                    <Button type="button" variant="ghost" size="xs" onClick={resetImportJobState}>
                      Tải tệp khác
                    </Button>
                  )}
                </div>
              </div>

              <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
                <div className="rounded-lg border border-border bg-card p-2.5 text-center">
                  <div className="text-xs text-muted-foreground">Tổng dòng</div>
                  <div className="mt-0.5 text-base font-semibold tabular-nums">
                    {formatInt(importJob.total_rows)}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-card p-2.5 text-center">
                  <div className="text-xs text-muted-foreground">Hợp lệ</div>
                  <div className="mt-0.5 text-base font-semibold text-badge-success-ink tabular-nums">
                    {formatInt(importJob.valid_rows)}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-card p-2.5 text-center">
                  <div className="text-xs text-muted-foreground">Lỗi kiểm tra</div>
                  <div
                    className={cn(
                      "mt-0.5 text-base font-semibold tabular-nums",
                      importJob.invalid_rows > 0 && "text-destructive"
                    )}
                  >
                    {formatInt(importJob.invalid_rows)}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-card p-2.5 text-center">
                  <div className="text-xs text-muted-foreground">Đã tạo mới</div>
                  <div className="mt-0.5 text-base font-semibold text-badge-success-ink tabular-nums">
                    {formatInt(importJob.created_rows)}
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-card p-2.5 text-center">
                  <div className="text-xs text-muted-foreground">Tạo thất bại</div>
                  <div
                    className={cn(
                      "mt-0.5 text-base font-semibold tabular-nums",
                      importJob.failed_rows > 0 && "text-destructive"
                    )}
                  >
                    {formatInt(importJob.failed_rows)}
                  </div>
                </div>
              </div>

              {isImportJobPollingStatus(importJob.status) && (
                <Alert>
                  <RefreshCw className="size-4 animate-spin" aria-hidden="true" />
                  <AlertDescription>
                    Hệ thống đang xử lý tệp CSV, trạng thái sẽ cập nhật tự động...
                  </AlertDescription>
                </Alert>
              )}

              {importJob.error_message && (
                <Alert variant="destructive">
                  <AlertTriangle className="size-4" aria-hidden="true" />
                  <AlertDescription>
                    {translateProductImportJobError(importJob.error_message)}
                  </AlertDescription>
                </Alert>
              )}

              {importJob.status === "READY" && (
                <Alert>
                  {importJob.valid_rows > 0 ? (
                    <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
                  ) : (
                    <AlertTriangle className="size-4" aria-hidden="true" />
                  )}
                  <AlertDescription>
                    {importJob.valid_rows > 0
                      ? `Có ${formatInt(importJob.valid_rows)} dòng hợp lệ sẵn sàng tạo mới (vào tab Chờ duyệt).`
                      : "Không có dòng hợp lệ nào để nhập. Tải tệp danh sách lỗi để chỉnh sửa rồi tải lên lại."}
                  </AlertDescription>
                </Alert>
              )}

              {importJob.status === "COMPLETED" && (
                <Alert>
                  <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
                  <AlertDescription>
                    <p>
                      Hoàn tất nhập CSV: đã tạo thành công {formatInt(importJob.created_rows)} sản
                      phẩm mới ở tab <strong>Chờ duyệt</strong>.
                    </p>
                  </AlertDescription>
                </Alert>
              )}

              {hasImportRowErrors && (
                <div className="space-y-2.5">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h3 className="text-sm font-semibold text-foreground">
                      Danh sách dòng lỗi (
                      {formatInt(importJob.invalid_rows + importJob.failed_rows)})
                    </h3>
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      disabled={downloadingErrors}
                      onClick={() => void handleDownloadImportErrors()}
                    >
                      <Download className="size-3.5" aria-hidden="true" />
                      <span>{downloadingErrors ? "Đang tải..." : "Tải tệp danh sách lỗi"}</span>
                    </Button>
                  </div>

                  {importErrorLoading ? (
                    <div className="space-y-2">
                      <Skeleton className="h-9 w-full" />
                      <Skeleton className="h-9 w-full" />
                    </div>
                  ) : importErrorRows.length === 0 ? (
                    <p className="text-xs text-muted-foreground">
                      Không tải được chi tiết dòng lỗi, vui lòng tải tệp danh sách lỗi.
                    </p>
                  ) : (
                    <>
                      <div className="overflow-hidden rounded-lg border border-border">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Dòng dữ liệu</TableHead>
                              <TableHead>Tên sản phẩm</TableHead>
                              <TableHead>Mã SKU</TableHead>
                              <TableHead>Trạng thái</TableHead>
                              <TableHead>Chi tiết lỗi</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {importErrorRows.map((row) => {
                              const rowMeta = IMPORT_ROW_STATUS_META[row.status] ?? {
                                label: row.status,
                                tone: "neg" as const,
                              };
                              return (
                                <TableRow key={row.id || `${row.row_number}-${row.sku_code}`}>
                                  <TableCell className="tabular-nums">
                                    {formatInt(row.row_number)}
                                  </TableCell>
                                  <TableCell className="whitespace-normal">
                                    {row.product_name || "—"}
                                  </TableCell>
                                  <TableCell className="font-mono text-xs">
                                    {row.sku_code || "—"}
                                  </TableCell>
                                  <TableCell>
                                    <StatusBadge tone={mapLegacyTone(rowMeta.tone)}>
                                      {rowMeta.label}
                                    </StatusBadge>
                                  </TableCell>
                                  <TableCell className="whitespace-normal text-xs text-destructive">
                                    {translateProductImportRowError(
                                      row.error_code,
                                      row.error_message
                                    )}
                                  </TableCell>
                                </TableRow>
                              );
                            })}
                          </TableBody>
                        </Table>
                      </div>

                      {importErrorTotalPages > 1 && (
                        <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
                          <span>
                            Trang {formatInt(importErrorPage)} / {formatInt(importErrorTotalPages)}
                          </span>
                          <div className="flex items-center gap-1.5">
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              disabled={importErrorPage <= 1 || importErrorLoading}
                              onClick={() => setImportErrorPage((p) => Math.max(1, p - 1))}
                            >
                              Trước
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              disabled={
                                importErrorPage >= importErrorTotalPages || importErrorLoading
                              }
                              onClick={() =>
                                setImportErrorPage((p) => Math.min(importErrorTotalPages, p + 1))
                              }
                            >
                              Sau
                            </Button>
                          </div>
                        </div>
                      )}
                    </>
                  )}
                </div>
              )}

              <DialogFooter>
                {importJob.status === "READY" && importJob.valid_rows > 0 && (
                  <Button
                    type="button"
                    size="sm"
                    disabled={confirmingImport}
                    onClick={() => void handleConfirmImport()}
                  >
                    {confirmingImport ? "Đang xác nhận..." : "Xác nhận nhập dòng hợp lệ"}
                  </Button>
                )}
                {importJob.status === "COMPLETED" && (
                  <Button type="button" size="sm" onClick={handleViewPendingTabAfterImport}>
                    Xem tab Chờ duyệt
                  </Button>
                )}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={confirmingImport}
                  onClick={() => setImportModalOpen(false)}
                >
                  Đóng
                </Button>
              </DialogFooter>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* Delete Product Confirmation Dialog */}
      <AlertDialog
        open={Boolean(confirmDeleteProduct)}
        onOpenChange={(open) => !open && setConfirmDeleteProduct(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Xóa sản phẩm &ldquo;{confirmDeleteProduct?.name}&rdquo;?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Sản phẩm sẽ bị gỡ khỏi danh mục gian hàng và không thể khôi phục sau khi xóa.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Hủy</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={!confirmDeleteProduct || deletingId === confirmDeleteProduct.id}
              onClick={() => {
                if (confirmDeleteProduct) {
                  void handleDeleteProduct(confirmDeleteProduct);
                }
              }}
            >
              {confirmDeleteProduct && deletingId === confirmDeleteProduct.id
                ? "Đang xóa..."
                : "Xác nhận xóa"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
