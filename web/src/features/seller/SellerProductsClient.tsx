"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  Button,
  IconArrowUpRight,
  IconPlus,
  IconRefresh,
  IconSearch,
  StateView,
} from "@/components";
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
import { useSellerContext } from "./SellerShell";
import { SellerDrawer, statusPillClass } from "./SellerUiPrimitives";
import {
  getSellerProductStatusMeta,
  type GwCategory,
  type GwSellerProduct,
  type GwSellerSku,
} from "./sellerApiTypes";
import { parseUiState } from "./types";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/san-pham/page.module.css";

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

  const [expandedProductIds, setExpandedProductIds] = useState<Record<string, boolean>>({});
  const [stockDrafts, setStockDrafts] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    for (const p of initialProducts) {
      for (const s of p.skus ?? []) init[s.id] = String(s.stock);
    }
    return init;
  });
  const [savingSkuId, setSavingSkuId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [drawerProductId, setDrawerProductId] = useState<string | null>(null);

  // S03 Stock History Modal state
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

  // UC-S13 2-Step CSV Import Modal state
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

  useEffect(() => {
    if (!historyTarget && !importModalOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (historyTarget) {
        historyLoadSeqRef.current += 1;
        setHistoryTarget(null);
      } else if (importModalOpen && !importUploading && !confirmingImport) {
        setImportModalOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [historyTarget, importModalOpen, importUploading, confirmingImport]);

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
      setConfirmDeleteId(null);
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
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Sản phẩm</h1>
          <p className={common.cardMeta}>
            {formatSellerProductsHeaderSummary(shop?.name || "Gian hàng", tabCounts, formatInt)}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" onClick={openImportModal}>
            Nhập file CSV
          </Button>
          <Button variant="outline" size="sm" shape="app" onClick={() => void refreshAll()}>
            <IconRefresh size={14} />
            <span>Làm mới</span>
          </Button>
          <Button variant="primary" size="sm" shape="app" href="/seller/san-pham/moi">
            <IconPlus size={14} />
            <span>Thêm sản phẩm</span>
          </Button>
        </div>
      </header>

      {conflictNotice && (
        <div className={styles.conflictBanner} role="alert">
          <span>{conflictNotice}</span>
          <button type="button" className={styles.editLink} onClick={() => setConflictNotice(null)}>
            Đã hiểu
          </button>
        </div>
      )}

      {(uiState === "loading" || (!uiState && loading && products.length === 0)) && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải sản phẩm..."
          rows={6}
        />
      )}

      {(uiState === "error" || (errorMsg && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sản phẩm"
          message={errorMsg ?? "Dịch vụ kho hàng chưa phản hồi."}
          actionLabel="Thử lại"
          onAction={() => void refreshAll()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa có sản phẩm nào."
          actionLabel="Thêm sản phẩm"
          actionHref="/seller/san-pham/moi"
        />
      )}

      {!uiState && !(loading && products.length === 0) && !errorMsg && (
        <>
          <div className={common.countCards6} role="tablist" aria-label="Lọc trạng thái sản phẩm">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.countCard} ${selected ? common.countCardSelected : ""}`}
                  onClick={() => handleSelectFilter(c.key)}
                >
                  <span className={common.countCardLabel}>{c.label}</span>
                  {c.count !== null && (
                    <span className={common.countCardValue}>{formatInt(c.count)}</span>
                  )}
                </button>
              );
            })}
          </div>

          <div className={common.chipScroller} role="tablist" aria-label="Lọc trạng thái sản phẩm">
            {countCards.map((c) => {
              const selected = filter === c.key;
              return (
                <button
                  key={c.key}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${common.chipBtn} ${selected ? common.chipBtnActive : ""}`}
                  onClick={() => handleSelectFilter(c.key)}
                >
                  <span>{c.label}</span>
                  {c.count !== null && (
                    <span className={common.chipCount}>{formatInt(c.count)}</span>
                  )}
                </button>
              );
            })}
          </div>

          <section className={common.card} aria-label="Danh sách sản phẩm">
            <div className={common.toolbar} style={{ marginBottom: 14 }}>
              <div className={common.toolbarLeft}>
                <div className={common.searchWrap}>
                  <span className={common.searchIcon}>
                    <IconSearch size={14} />
                  </span>
                  <input
                    type="search"
                    className={common.searchInput}
                    value={queryInput}
                    onChange={(e) => {
                      setQueryInput(e.target.value);
                      setPage(1);
                    }}
                    placeholder="Tìm tên sản phẩm hoặc mã SKU..."
                    aria-label="Tìm sản phẩm hoặc mã SKU"
                  />
                </div>
                <select
                  className={common.filterSelect}
                  value={categoryFilter}
                  onChange={(e) => {
                    setCategoryFilter(e.target.value);
                    setPage(1);
                  }}
                  aria-label="Lọc theo danh mục"
                >
                  <option value="all">Mọi danh mục</option>
                  {categories.map((cat) => (
                    <option key={cat.id} value={cat.id}>
                      {cat.name}
                    </option>
                  ))}
                </select>
                {hasActiveFilters && (
                  <button
                    type="button"
                    className={styles.editLink}
                    onClick={() => {
                      setFilter("all");
                      setCategoryFilter("all");
                      setQueryInput("");
                      setDebouncedQuery("");
                      setPage(1);
                    }}
                  >
                    Xóa bộ lọc
                  </button>
                )}
              </div>
              <span className={common.toolbarMeta}>
                {loading
                  ? "Đang cập nhật..."
                  : totalProducts === 0
                    ? "0 sản phẩm"
                    : `Hiển thị ${formatInt(rangeStart)}–${formatInt(rangeEnd)} / ${formatInt(totalProducts)} sản phẩm`}
              </span>
            </div>

            {products.length === 0 ? (
              <div className={common.inlineEmpty}>
                {!hasActiveFilters && tabCounts.all === 0
                  ? "Gian hàng chưa có sản phẩm nào. Nhấn Thêm sản phẩm hoặc Nhập file CSV để bắt đầu."
                  : "Không có sản phẩm khớp bộ lọc hiện tại."}
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Sản phẩm</th>
                        <th>Danh mục</th>
                        <th className={common.numCol}>Giá bán</th>
                        <th className={common.numCol}>Khả dụng / Tổng tồn</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
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
                            <tr>
                              <td>
                                <div className={styles.prodCell}>
                                  <img
                                    src={cover}
                                    alt={p.name}
                                    className={styles.prodThumb}
                                    width={36}
                                    height={46}
                                  />
                                  <div className={styles.prodText}>
                                    <button
                                      type="button"
                                      className={common.rowTriggerBtn}
                                      title={p.name}
                                      onClick={() => setDrawerProductId(p.id)}
                                    >
                                      {p.name}
                                    </button>
                                    <span className={styles.prodSub}>
                                      {formatInt(allSkus.length)} SKU
                                      {p.slug ? ` · /${p.slug}` : ""}
                                    </span>
                                    {p.status === "REJECTED" && p.rejection_reason && (
                                      <span className={styles.rejectionNote}>
                                        Từ chối: {p.rejection_reason}
                                      </span>
                                    )}
                                  </div>
                                </div>
                              </td>
                              <td>
                                <span className={styles.catCell} title={catName}>
                                  {catName}
                                </span>
                              </td>
                              <td className={common.numCol}>
                                <strong>{formatPriceRange(p)}</strong>
                              </td>
                              <td className={common.numCol}>
                                <span className={availStock === 0 ? styles.stockWarn : ""}>
                                  {formatInt(availStock)}
                                </span>{" "}
                                / {formatInt(totalStock)}
                              </td>
                              <td>
                                <span className={statusPillClass(stMeta.tone)}>{stMeta.label}</span>
                              </td>
                              <td className={common.numCol}>
                                <div className={styles.actionRow}>
                                  <button
                                    type="button"
                                    className={styles.restockBtn}
                                    onClick={() => toggleExpand(p.id)}
                                  >
                                    {isExpanded ? "Đóng tồn" : "Sửa tồn"}
                                  </button>
                                  <Link
                                    href={`/seller/san-pham/${p.id}`}
                                    className={styles.editLink}
                                  >
                                    Sửa
                                  </Link>
                                  {confirmDeleteId === p.id ? (
                                    <>
                                      <button
                                        type="button"
                                        className={styles.deleteBtn}
                                        disabled={deletingId === p.id}
                                        onClick={() => void handleDeleteProduct(p)}
                                      >
                                        {deletingId === p.id ? "Đang xóa..." : "Xác nhận xóa"}
                                      </button>
                                      <button
                                        type="button"
                                        className={styles.editLink}
                                        onClick={() => setConfirmDeleteId(null)}
                                      >
                                        Hủy
                                      </button>
                                    </>
                                  ) : (
                                    <button
                                      type="button"
                                      className={styles.editLink}
                                      onClick={() => setConfirmDeleteId(p.id)}
                                    >
                                      Xóa
                                    </button>
                                  )}
                                </div>
                              </td>
                            </tr>
                            {isExpanded && (
                              <tr>
                                <td colSpan={6} className={styles.skuDrawerCell}>
                                  <div className={styles.skuDrawerList}>
                                    {allSkus.length === 0 ? (
                                      <span className={styles.prodSub}>
                                        Sản phẩm chưa có biến thể SKU.
                                      </span>
                                    ) : (
                                      allSkus.map((sku) => {
                                        const draftVal = stockDrafts[sku.id] ?? String(sku.stock);
                                        const isSaving = savingSkuId === sku.id;
                                        return (
                                          <div key={sku.id} className={styles.skuDrawerRow}>
                                            <div className={styles.skuDrawerInfo}>
                                              <span className={styles.skuDrawerCode}>
                                                {sku.sku_code} · {sku.name}
                                              </span>
                                              <span className={styles.skuDrawerMeta}>
                                                Giá {formatVnd(sku.price_vnd)} · Khả dụng{" "}
                                                {formatInt(sku.available_stock)} · Giữ{" "}
                                                {formatInt(sku.reserved_stock)} · Tổng{" "}
                                                {formatInt(sku.stock)}
                                              </span>
                                            </div>
                                            <div className={styles.skuStockEditor}>
                                              <input
                                                type="number"
                                                min={sku.reserved_stock}
                                                step={1}
                                                className={styles.skuStockInput}
                                                value={draftVal}
                                                onChange={(e) =>
                                                  setStockDrafts((prev) => ({
                                                    ...prev,
                                                    [sku.id]: e.target.value,
                                                  }))
                                                }
                                                aria-label={`Tồn kho tổng của ${sku.sku_code}`}
                                              />
                                              <button
                                                type="button"
                                                className={styles.restockBtn}
                                                disabled={isSaving}
                                                onClick={() =>
                                                  void handleUpdateSkuStock(sku, Number(draftVal))
                                                }
                                              >
                                                {isSaving ? "Đang lưu..." : "Lưu tồn"}
                                              </button>
                                              <button
                                                type="button"
                                                className={styles.editLink}
                                                onClick={() => openStockHistory(p, sku)}
                                              >
                                                Lịch sử tồn
                                              </button>
                                            </div>
                                          </div>
                                        );
                                      })
                                    )}
                                  </div>
                                </td>
                              </tr>
                            )}
                          </React.Fragment>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
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
                      <div key={p.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div className={styles.mobileProdTop}>
                            <img
                              src={cover}
                              alt={p.name}
                              className={styles.prodThumb}
                              width={36}
                              height={46}
                            />
                            <div className={styles.prodText}>
                              <Link href={`/seller/san-pham/${p.id}`} className={styles.prodName}>
                                {p.name}
                              </Link>
                              <span className={styles.prodSub}>
                                Khả dụng {formatInt(availStock)} · {formatInt(allSkus.length)} SKU
                              </span>
                              {p.status === "REJECTED" && p.rejection_reason && (
                                <span className={styles.rejectionNote}>
                                  Từ chối: {p.rejection_reason}
                                </span>
                              )}
                            </div>
                          </div>
                          <span className={styles.mobilePrice}>{formatVnd(p.min_price_vnd)}</span>
                        </div>
                        <div className={common.mobileRowMeta}>
                          <span className={statusPillClass(stMeta.tone)}>{stMeta.label}</span>
                          <div className={styles.actionRow}>
                            <button
                              type="button"
                              className={styles.restockBtn}
                              onClick={() => toggleExpand(p.id)}
                            >
                              {isExpanded ? "Đóng tồn" : "Sửa tồn"}
                            </button>
                            <Link href={`/seller/san-pham/${p.id}`} className={styles.editLink}>
                              Sửa
                            </Link>
                          </div>
                        </div>
                        {isExpanded && allSkus.length > 0 && (
                          <div className={styles.mobileSkuBox}>
                            {allSkus.map((sku) => {
                              const draftVal = stockDrafts[sku.id] ?? String(sku.stock);
                              const isSaving = savingSkuId === sku.id;
                              return (
                                <div key={sku.id} className={styles.skuDrawerRow}>
                                  <div className={styles.skuDrawerInfo}>
                                    <span className={styles.skuDrawerCode}>{sku.sku_code}</span>
                                    <span className={styles.skuDrawerMeta}>
                                      {sku.name} · Khả dụng {formatInt(sku.available_stock)}
                                    </span>
                                  </div>
                                  <div className={styles.skuStockEditor}>
                                    <input
                                      type="number"
                                      min={sku.reserved_stock}
                                      step={1}
                                      className={styles.skuStockInput}
                                      value={draftVal}
                                      onChange={(e) =>
                                        setStockDrafts((prev) => ({
                                          ...prev,
                                          [sku.id]: e.target.value,
                                        }))
                                      }
                                      aria-label={`Tồn kho của ${sku.sku_code}`}
                                    />
                                    <button
                                      type="button"
                                      className={styles.restockBtn}
                                      disabled={isSaving}
                                      onClick={() =>
                                        void handleUpdateSkuStock(sku, Number(draftVal))
                                      }
                                    >
                                      {isSaving ? "..." : "Lưu"}
                                    </button>
                                    <button
                                      type="button"
                                      className={styles.editLink}
                                      onClick={() => openStockHistory(p, sku)}
                                    >
                                      Lịch sử tồn
                                    </button>
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

                {totalPages > 1 && (
                  <nav className={styles.paginationBar} aria-label="Phân trang sản phẩm">
                    <span className={common.cardMeta}>
                      Trang {formatInt(safePage)} / {formatInt(totalPages)} · Tổng{" "}
                      {formatInt(totalProducts)} sản phẩm
                    </span>
                    <div className={styles.paginationControls}>
                      <button
                        type="button"
                        className={styles.pageBtn}
                        disabled={safePage <= 1 || loading}
                        onClick={() => setPage((p) => Math.max(1, p - 1))}
                      >
                        Trước
                      </button>
                      {pageNumbers.map((item, idx) =>
                        item === "ellipsis" ? (
                          <span key={`e-${idx}`} className={styles.pageEllipsis}>
                            …
                          </span>
                        ) : (
                          <button
                            key={item}
                            type="button"
                            className={`${styles.pageBtn} ${item === safePage ? styles.pageBtnActive : ""}`}
                            aria-current={item === safePage ? "page" : undefined}
                            disabled={loading}
                            onClick={() => setPage(item)}
                          >
                            {item}
                          </button>
                        )
                      )}
                      <button
                        type="button"
                        className={styles.pageBtn}
                        disabled={safePage >= totalPages || loading}
                        onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                      >
                        Sau
                      </button>
                    </div>
                  </nav>
                )}
              </>
            )}
          </section>

          {lowStockRows.length > 0 && (
            <section className={common.card} aria-labelledby="restock-heading">
              <div className={common.cardHeader}>
                <h2 id="restock-heading" className={common.cardTitle}>
                  SKU cần nhập kho ({formatInt(lowStockRows.length)})
                </h2>
                <span className={common.cardMeta}>Tồn khả dụng ≤ 5 trên trang hiện tại</span>
              </div>

              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm &amp; biến thể</th>
                      <th>Mã SKU</th>
                      <th className={common.numCol}>Đơn giá</th>
                      <th className={common.numCol}>Tồn khả dụng</th>
                      <th className={common.numCol}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {lowStockRows.map(({ product, sku }) => {
                      const isSaving = savingSkuId === sku.id;
                      return (
                        <tr key={sku.id}>
                          <td>
                            <div className={styles.prodCell}>
                              <img
                                src={sku.image_url || getCoverImage(product)}
                                alt={product.name}
                                className={styles.skuThumbSm}
                                width={32}
                                height={42}
                              />
                              <div className={styles.prodText}>
                                <Link
                                  href={`/seller/san-pham/${product.id}`}
                                  className={styles.prodName}
                                >
                                  {product.name}
                                </Link>
                                <span className={styles.prodSub}>{sku.name || sku.sku_code}</span>
                              </div>
                            </div>
                          </td>
                          <td>{sku.sku_code}</td>
                          <td className={common.numCol}>{formatVnd(sku.price_vnd)}</td>
                          <td className={common.numCol}>
                            <span className={sku.available_stock <= 0 ? styles.stockWarn : ""}>
                              {sku.available_stock <= 0
                                ? "Hết hàng"
                                : `Còn ${formatInt(sku.available_stock)}`}
                            </span>
                          </td>
                          <td className={common.numCol}>
                            <div className={styles.actionRow}>
                              <button
                                type="button"
                                className={styles.restockBtn}
                                disabled={isSaving}
                                onClick={() => void handleUpdateSkuStock(sku, sku.stock + 10)}
                              >
                                {isSaving ? "Đang lưu..." : "+10 tồn"}
                              </button>
                              <button
                                type="button"
                                className={styles.editLink}
                                onClick={() => openStockHistory(product, sku)}
                              >
                                Lịch sử tồn
                              </button>
                              <Link
                                href={`/seller/san-pham/${product.id}`}
                                className={styles.editLink}
                              >
                                Sửa
                              </Link>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={common.mobileList}>
                {lowStockRows.map(({ product, sku }) => {
                  const isSaving = savingSkuId === sku.id;
                  return (
                    <div key={sku.id} className={common.mobileRow}>
                      <div className={common.mobileRowTop}>
                        <div className={styles.mobileSkuInfo}>
                          <Link href={`/seller/san-pham/${product.id}`} className={styles.prodName}>
                            {product.name} ({sku.name || sku.sku_code})
                          </Link>
                          <span className={styles.prodSub}>{sku.sku_code}</span>
                        </div>
                        <span
                          className={
                            sku.available_stock <= 0 ? styles.stockWarn : styles.mobilePrice
                          }
                        >
                          {sku.available_stock <= 0
                            ? "Hết hàng"
                            : `Còn ${formatInt(sku.available_stock)}`}
                        </span>
                      </div>
                      <div className={common.mobileRowMeta}>
                        <span className="num">{formatVnd(sku.price_vnd)}</span>
                        <div className={styles.actionRow}>
                          <button
                            type="button"
                            className={styles.restockBtn}
                            disabled={isSaving}
                            onClick={() => void handleUpdateSkuStock(sku, sku.stock + 10)}
                          >
                            {isSaving ? "..." : "+10 tồn"}
                          </button>
                          <button
                            type="button"
                            className={styles.editLink}
                            onClick={() => openStockHistory(product, sku)}
                          >
                            Lịch sử tồn
                          </button>
                          <Link href={`/seller/san-pham/${product.id}`} className={styles.editLink}>
                            Sửa
                          </Link>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>
          )}
        </>
      )}

      {/* Slide-over Quick SKU Inventory Drawer */}
      <SellerDrawer
        open={Boolean(drawerProduct)}
        onClose={() => setDrawerProductId(null)}
        title={drawerProduct?.name ?? ""}
        subtitle={
          drawerProduct
            ? `${categoryNameById.get(drawerProduct.category_id) ?? "Chưa phân loại"}, ${formatInt(
                (drawerProduct.skus ?? []).filter((s) => s.is_active).length
              )} SKU`
            : ""
        }
        badge={
          drawerProduct ? (
            <span
              className={statusPillClass(getSellerProductStatusMeta(drawerProduct.status).tone)}
            >
              {getSellerProductStatusMeta(drawerProduct.status).label}
            </span>
          ) : null
        }
        footer={
          drawerProduct ? (
            <>
              <span className={common.cardMeta}>
                Tổng khả dụng: {formatInt(totalAvailable(drawerProduct))}
              </span>
              <Button
                variant="primary"
                size="sm"
                shape="app"
                href={`/seller/san-pham/${drawerProduct.id}`}
              >
                <span>Chỉnh sửa sản phẩm</span>
                <IconArrowUpRight size={14} />
              </Button>
            </>
          ) : null
        }
      >
        {drawerProduct && (
          <div className={styles.drawerSkuList}>
            {(drawerProduct.skus ?? [])
              .filter((s) => s.is_active)
              .map((sku) => (
                <div key={sku.id} className={styles.drawerSkuRow}>
                  <div className={styles.drawerSkuMain}>
                    <strong className={styles.drawerSkuName}>{sku.name || sku.sku_code}</strong>
                    <span className={common.cellSub}>
                      {sku.sku_code}, {formatVnd(sku.price_vnd)}
                    </span>
                  </div>
                  <div className={styles.drawerSkuRight}>
                    <span className={sku.available_stock <= 5 ? styles.stockWarn : "num"}>
                      {sku.available_stock <= 0
                        ? "Hết hàng"
                        : `Còn ${formatInt(sku.available_stock)}`}
                    </span>
                    <button
                      type="button"
                      className={styles.editLink}
                      onClick={() => openStockHistory(drawerProduct, sku)}
                    >
                      Lịch sử
                    </button>
                  </div>
                </div>
              ))}
          </div>
        )}
      </SellerDrawer>

      {/* S03 Stock History Modal */}
      {historyTarget && (
        <div
          className={styles.modalBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeStockHistory();
          }}
        >
          <div
            className={`${styles.modalDialog} ${styles.modalDialogWide}`}
            role="dialog"
            aria-modal="true"
            aria-labelledby="stock-history-title"
          >
            <div className={styles.modalHeader}>
              <div>
                <h2 id="stock-history-title" className={styles.modalTitle}>
                  Lịch sử tồn kho SKU
                </h2>
                <p className={styles.modalSub}>
                  {historyTarget.sku.sku_code} · {historyTarget.sku.name} —{" "}
                  {historyTarget.product.name}
                </p>
                <p className={styles.modalSub}>
                  Hiện tại: Tổng {formatInt(historyTarget.sku.stock)} · Khả dụng{" "}
                  {formatInt(historyTarget.sku.available_stock)} · Đang giữ{" "}
                  {formatInt(historyTarget.sku.reserved_stock)}
                </p>
              </div>
              <button type="button" className={styles.editLink} onClick={closeStockHistory}>
                Đóng
              </button>
            </div>

            <div className={styles.drawerFilters}>
              <select
                className={common.filterSelect}
                value={historyChangeType}
                onChange={(e) => {
                  setHistoryChangeType(e.target.value);
                  setHistoryPage(1);
                }}
                aria-label="Lọc loại biến động tồn kho"
              >
                <option value="all">Mọi loại biến động</option>
                <option value="MANUAL_UPDATE">Cập nhật thủ công</option>
                <option value="RESERVE">Giữ tồn đặt đơn</option>
                <option value="RELEASE">Hoàn tồn hủy đơn</option>
                <option value="CONFIRM">Xuất kho xác nhận</option>
                <option value="RESTOCK">Nhập lại kho</option>
              </select>
              <input
                type="date"
                className={styles.dateInput}
                value={historyFromDate}
                onChange={(e) => {
                  setHistoryFromDate(e.target.value);
                  setHistoryPage(1);
                }}
                aria-label="Từ ngày"
              />
              <input
                type="date"
                className={styles.dateInput}
                value={historyToDate}
                onChange={(e) => {
                  setHistoryToDate(e.target.value);
                  setHistoryPage(1);
                }}
                aria-label="Đến ngày"
              />
              {(historyChangeType !== "all" || historyFromDate || historyToDate) && (
                <button
                  type="button"
                  className={styles.editLink}
                  onClick={() => {
                    setHistoryChangeType("all");
                    setHistoryFromDate("");
                    setHistoryToDate("");
                    setHistoryPage(1);
                  }}
                >
                  Đặt lại
                </button>
              )}
              <button
                type="button"
                className={styles.editLink}
                disabled={historyLoading}
                onClick={() => void loadStockHistory()}
              >
                Làm mới
              </button>
            </div>

            {historyError && (
              <div className={`${styles.noticeBox} ${styles.noticeNeg}`} role="alert">
                {historyError}
              </div>
            )}

            {historyLoading ? (
              <div className={common.inlineEmpty}>Đang tải lịch sử biến động tồn kho...</div>
            ) : historyItems.length === 0 ? (
              <div className={common.inlineEmpty}>
                Chưa có bản ghi biến động tồn kho khớp bộ lọc.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Thời điểm</th>
                        <th>Loại biến động</th>
                        <th className={common.numCol}>Thay đổi</th>
                        <th className={common.numCol}>Tổng tồn</th>
                        <th className={common.numCol}>Đang giữ</th>
                        <th>Lý do / Đơn hàng</th>
                      </tr>
                    </thead>
                    <tbody>
                      {historyItems.map((item) => {
                        const meta = STOCK_HISTORY_CHANGE_META[item.change_type] ?? {
                          label: item.change_type,
                          tone: "neutral" as const,
                        };
                        return (
                          <tr key={item.id}>
                            <td className={styles.nowrapCell}>
                              {item.created_at ? formatDateTimeVi(item.created_at) : "—"}
                            </td>
                            <td>
                              <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                            </td>
                            <td className={common.numCol}>
                              <strong
                                className={
                                  item.delta > 0
                                    ? styles.deltaPos
                                    : item.delta < 0
                                      ? styles.deltaNeg
                                      : ""
                                }
                              >
                                {formatSignedInt(item.delta)}
                              </strong>
                            </td>
                            <td className={common.numCol}>
                              {formatInt(item.stock_before)} → {formatInt(item.stock_after)}
                            </td>
                            <td className={common.numCol}>
                              {formatInt(item.reserved_before)} → {formatInt(item.reserved_after)}
                            </td>
                            <td>
                              <span>{formatStockHistoryReason(item.change_type, item.reason)}</span>
                              {item.order_id && (
                                <>
                                  {" · "}
                                  <Link
                                    href={`/seller/don-hang/${item.order_id}`}
                                    className={styles.orderRefLink}
                                    onClick={closeStockHistory}
                                  >
                                    Xem đơn
                                  </Link>
                                </>
                              )}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                <div className={common.mobileList}>
                  {historyItems.map((item) => {
                    const meta = STOCK_HISTORY_CHANGE_META[item.change_type] ?? {
                      label: item.change_type,
                      tone: "neutral" as const,
                    };
                    return (
                      <div key={item.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          <strong
                            className={
                              item.delta > 0
                                ? styles.deltaPos
                                : item.delta < 0
                                  ? styles.deltaNeg
                                  : styles.mobilePrice
                            }
                          >
                            {formatSignedInt(item.delta)}
                          </strong>
                        </div>
                        <span className={styles.prodSub}>
                          {item.created_at ? formatDateTimeVi(item.created_at) : "—"} · Tổng{" "}
                          {formatInt(item.stock_before)} → {formatInt(item.stock_after)}
                        </span>
                        <div className={common.mobileRowMeta}>
                          <span>{formatStockHistoryReason(item.change_type, item.reason)}</span>
                          {item.order_id && (
                            <Link
                              href={`/seller/don-hang/${item.order_id}`}
                              className={styles.orderRefLink}
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
                  <div className={styles.paginationBar}>
                    <span className={common.cardMeta}>
                      Trang {formatInt(historyPage)} / {formatInt(historyTotalPages)} ·{" "}
                      {formatInt(historyTotal)} bản ghi
                    </span>
                    <div className={styles.paginationControls}>
                      <button
                        type="button"
                        className={styles.pageBtn}
                        disabled={historyPage <= 1 || historyLoading}
                        onClick={() => setHistoryPage((p) => Math.max(1, p - 1))}
                      >
                        Trước
                      </button>
                      <button
                        type="button"
                        className={styles.pageBtn}
                        disabled={historyPage >= historyTotalPages || historyLoading}
                        onClick={() => setHistoryPage((p) => Math.min(historyTotalPages, p + 1))}
                      >
                        Sau
                      </button>
                    </div>
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      )}

      {/* UC-S13 2-Step CSV Import Modal */}
      {importModalOpen && (
        <div
          className={styles.modalBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget && !importUploading && !confirmingImport) {
              setImportModalOpen(false);
            }
          }}
        >
          <div
            className={`${styles.modalDialog} ${styles.modalDialogWide}`}
            role="dialog"
            aria-modal="true"
            aria-labelledby="csv-import-title"
          >
            <div className={styles.modalHeader}>
              <div>
                <h2 id="csv-import-title" className={styles.modalTitle}>
                  Nhập sản phẩm hàng loạt bằng file CSV
                </h2>
                <p className={styles.modalSub}>
                  Tải tệp lên để kiểm tra từng dòng, sau đó xác nhận nhập các dòng hợp lệ.
                </p>
              </div>
              <button
                type="button"
                className={styles.editLink}
                disabled={importUploading || confirmingImport}
                onClick={() => setImportModalOpen(false)}
              >
                Đóng
              </button>
            </div>

            <div className={`${styles.noticeBox} ${styles.noticeWarn}`}>
              Chỉ hỗ trợ <strong>tạo mới sản phẩm</strong>. Tệp CSV UTF-8 tối đa{" "}
              <strong>1 MB</strong> và{" "}
              <strong>{formatInt(MAX_IMPORT_CSV_ROWS)} dòng dữ liệu</strong>. Sản phẩm tạo thành
              công vào tab <strong>Chờ duyệt</strong>.
            </div>

            {importActionError && (
              <div className={`${styles.noticeBox} ${styles.noticeNeg}`} role="alert">
                {importActionError}
              </div>
            )}

            {!importJob ? (
              <>
                <div className={styles.stepSection}>
                  <h3 className={styles.stepTitle}>Bước 1: Tải tệp mẫu CSV</h3>
                  <div className={styles.drawerFilters}>
                    <select
                      className={common.filterSelect}
                      value={importCategoryId}
                      onChange={(e) => setImportCategoryId(e.target.value)}
                      aria-label="Chọn danh mục điền sẵn trong mẫu CSV"
                    >
                      <option value="all">Không điền sẵn danh mục</option>
                      {categories
                        .filter((c) => !c.is_prohibited)
                        .map((cat) => (
                          <option key={cat.id} value={cat.id}>
                            {cat.name}
                          </option>
                        ))}
                    </select>
                    <button
                      type="button"
                      className={styles.editLink}
                      disabled={downloadingTemplate}
                      onClick={() => void handleDownloadTemplate()}
                    >
                      {downloadingTemplate ? "Đang tải mẫu..." : "Tải mẫu CSV"}
                    </button>
                  </div>
                </div>

                <div className={styles.stepSection}>
                  <h3 className={styles.stepTitle}>Bước 2: Chọn tệp CSV và kiểm tra dữ liệu</h3>
                  <div className={styles.uploadBox}>
                    <input
                      ref={importFileInputRef}
                      type="file"
                      accept=".csv,text/csv"
                      className={styles.fileInput}
                      onChange={handleSelectCsvFile}
                      aria-label="Chọn tệp CSV sản phẩm"
                    />
                    {importFile && (
                      <span className={styles.fileMeta}>
                        Đã chọn: <strong>{importFile.name}</strong> (
                        {Math.max(1, Math.round(importFile.size / 1024))} KB)
                      </span>
                    )}
                    {importFileError && (
                      <span className={styles.rejectionNote} role="alert">
                        {importFileError}
                      </span>
                    )}
                  </div>
                </div>

                <div className={styles.modalFooter}>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={importUploading}
                    onClick={() => setImportModalOpen(false)}
                  >
                    Hủy
                  </Button>
                  <Button
                    type="button"
                    variant="primary"
                    size="sm"
                    shape="app"
                    disabled={!importFile || importUploading}
                    onClick={() => void handleUploadCsv()}
                  >
                    {importUploading ? "Đang tải lên..." : "Tải lên & Kiểm tra"}
                  </Button>
                </div>
              </>
            ) : (
              <>
                <div className={styles.jobHeaderRow}>
                  <div className={styles.prodText}>
                    <span className={styles.skuDrawerCode}>
                      {importJob.filename || "Tệp CSV đã tải lên"}
                    </span>
                    <span className={styles.prodSub}>Mã đợt nhập: {importJob.id}</span>
                  </div>
                  <div className={styles.actionRow}>
                    <span
                      className={statusPillClass(
                        (IMPORT_JOB_STATUS_META[importJob.status] ?? { tone: "neutral" as const })
                          .tone
                      )}
                    >
                      {
                        (IMPORT_JOB_STATUS_META[importJob.status] ?? { label: importJob.status })
                          .label
                      }
                    </span>
                    {!isImportJobPollingStatus(importJob.status) && (
                      <button
                        type="button"
                        className={styles.editLink}
                        onClick={resetImportJobState}
                      >
                        Tải tệp khác
                      </button>
                    )}
                  </div>
                </div>

                <div className={styles.statGrid}>
                  <div className={styles.statBox}>
                    <span className={styles.statLabel}>Tổng dòng</span>
                    <strong className={styles.statValue}>{formatInt(importJob.total_rows)}</strong>
                  </div>
                  <div className={styles.statBox}>
                    <span className={styles.statLabel}>Hợp lệ</span>
                    <strong className={`${styles.statValue} ${styles.deltaPos}`}>
                      {formatInt(importJob.valid_rows)}
                    </strong>
                  </div>
                  <div className={styles.statBox}>
                    <span className={styles.statLabel}>Lỗi kiểm tra</span>
                    <strong
                      className={`${styles.statValue} ${importJob.invalid_rows > 0 ? styles.deltaNeg : ""}`}
                    >
                      {formatInt(importJob.invalid_rows)}
                    </strong>
                  </div>
                  <div className={styles.statBox}>
                    <span className={styles.statLabel}>Đã tạo mới</span>
                    <strong className={`${styles.statValue} ${styles.deltaPos}`}>
                      {formatInt(importJob.created_rows)}
                    </strong>
                  </div>
                  <div className={styles.statBox}>
                    <span className={styles.statLabel}>Tạo thất bại</span>
                    <strong
                      className={`${styles.statValue} ${importJob.failed_rows > 0 ? styles.deltaNeg : ""}`}
                    >
                      {formatInt(importJob.failed_rows)}
                    </strong>
                  </div>
                </div>

                {isImportJobPollingStatus(importJob.status) && (
                  <div className={styles.noticeBox}>
                    Hệ thống đang xử lý tệp CSV, trạng thái sẽ cập nhật tự động...
                  </div>
                )}

                {importJob.error_message && (
                  <div className={`${styles.noticeBox} ${styles.noticeNeg}`} role="alert">
                    {translateProductImportJobError(importJob.error_message)}
                  </div>
                )}

                {importJob.status === "READY" && (
                  <div
                    className={`${styles.noticeBox} ${importJob.valid_rows > 0 ? styles.noticePos : styles.noticeWarn}`}
                  >
                    {importJob.valid_rows > 0
                      ? `Có ${formatInt(importJob.valid_rows)} dòng hợp lệ sẵn sàng tạo mới (vào tab Chờ duyệt).`
                      : "Không có dòng hợp lệ nào để nhập. Tải tệp danh sách lỗi để chỉnh sửa rồi tải lên lại."}
                  </div>
                )}

                {importJob.status === "COMPLETED" && (
                  <div className={`${styles.noticeBox} ${styles.noticePos}`}>
                    Hoàn tất nhập CSV: đã tạo thành công {formatInt(importJob.created_rows)} sản
                    phẩm mới ở tab <strong>Chờ duyệt</strong>.
                  </div>
                )}

                {hasImportRowErrors && (
                  <div className={styles.stepSection}>
                    <div className={styles.jobHeaderRow}>
                      <h3 className={styles.stepTitle}>
                        Danh sách dòng lỗi (
                        {formatInt(importJob.invalid_rows + importJob.failed_rows)})
                      </h3>
                      <button
                        type="button"
                        className={styles.editLink}
                        disabled={downloadingErrors}
                        onClick={() => void handleDownloadImportErrors()}
                      >
                        {downloadingErrors ? "Đang tải..." : "Tải tệp danh sách lỗi"}
                      </button>
                    </div>

                    {importErrorLoading ? (
                      <div className={common.inlineEmpty}>Đang tải danh sách dòng lỗi...</div>
                    ) : importErrorRows.length === 0 ? (
                      <div className={common.inlineEmpty}>
                        Không tải được chi tiết dòng lỗi, vui lòng tải tệp danh sách lỗi.
                      </div>
                    ) : (
                      <>
                        <div className={common.tableWrap}>
                          <table className={common.dataTable}>
                            <thead>
                              <tr>
                                <th>Dòng dữ liệu</th>
                                <th>Tên sản phẩm</th>
                                <th>Mã SKU</th>
                                <th>Trạng thái</th>
                                <th>Chi tiết lỗi</th>
                              </tr>
                            </thead>
                            <tbody>
                              {importErrorRows.map((row) => {
                                const rowMeta = IMPORT_ROW_STATUS_META[row.status] ?? {
                                  label: row.status,
                                  tone: "neg" as const,
                                };
                                return (
                                  <tr key={row.id || `${row.row_number}-${row.sku_code}`}>
                                    <td>{formatInt(row.row_number)}</td>
                                    <td>{row.product_name || "—"}</td>
                                    <td>{row.sku_code || "—"}</td>
                                    <td>
                                      <span className={statusPillClass(rowMeta.tone)}>
                                        {rowMeta.label}
                                      </span>
                                    </td>
                                    <td className={styles.errorCell}>
                                      {translateProductImportRowError(
                                        row.error_code,
                                        row.error_message
                                      )}
                                    </td>
                                  </tr>
                                );
                              })}
                            </tbody>
                          </table>
                        </div>
                        {importErrorTotalPages > 1 && (
                          <div className={styles.paginationBar}>
                            <span className={common.cardMeta}>
                              Trang {formatInt(importErrorPage)} /{" "}
                              {formatInt(importErrorTotalPages)}
                            </span>
                            <div className={styles.paginationControls}>
                              <button
                                type="button"
                                className={styles.pageBtn}
                                disabled={importErrorPage <= 1 || importErrorLoading}
                                onClick={() => setImportErrorPage((p) => Math.max(1, p - 1))}
                              >
                                Trước
                              </button>
                              <button
                                type="button"
                                className={styles.pageBtn}
                                disabled={
                                  importErrorPage >= importErrorTotalPages || importErrorLoading
                                }
                                onClick={() =>
                                  setImportErrorPage((p) => Math.min(importErrorTotalPages, p + 1))
                                }
                              >
                                Sau
                              </button>
                            </div>
                          </div>
                        )}
                      </>
                    )}
                  </div>
                )}

                <div className={styles.modalFooter}>
                  {importJob.status === "READY" && importJob.valid_rows > 0 && (
                    <Button
                      type="button"
                      variant="primary"
                      size="sm"
                      shape="app"
                      disabled={confirmingImport}
                      onClick={() => void handleConfirmImport()}
                    >
                      {confirmingImport ? "Đang xác nhận..." : "Xác nhận nhập dòng hợp lệ"}
                    </Button>
                  )}
                  {importJob.status === "COMPLETED" && (
                    <Button
                      type="button"
                      variant="primary"
                      size="sm"
                      shape="app"
                      onClick={handleViewPendingTabAfterImport}
                    >
                      Xem tab Chờ duyệt
                    </Button>
                  )}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={confirmingImport}
                    onClick={() => setImportModalOpen(false)}
                  >
                    Đóng
                  </Button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
