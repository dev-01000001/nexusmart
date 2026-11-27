"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Loader2,
  Package,
  PackageSearch,
  Search,
} from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  Money,
  PageHeader,
  StatusBadge,
  productStatusTone,
} from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { formatVariantLabel } from "@/lib/formatVariant";
import type { PaginatedResponse } from "@/lib/gateway-types";
import {
  getProductReviewStatusMeta,
  type GwAdminProductExtended,
} from "@/features/admin/adminApiTypes";
import { AdminBannedKeywordsTab } from "@/features/admin/AdminBannedKeywordsTab";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import {
  AdminConfirmActionDialog,
  AdminDetailField,
  AdminDetailGrid,
  AdminKbdHint,
  AdminPaginationBar,
  hasOpenRadixDialog,
  isEditableTarget,
} from "@/features/admin/ops/AdminOpsPrimitives";
import {
  ADMIN_MODERATION_PAGE_SIZE,
  buildAdminProductsQuery,
  clampModerationPage,
  createEmptyProductTabCounts,
  normalizeProductTabCounts,
  pickNextProductIdOnPage,
  type ProductStatusFilter,
  type ProductTabCounts,
} from "./moderationHelpers";

type ModerationSection = "products" | "banned_keywords";

export interface AdminModerationClientProps {
  initialProducts: GwAdminProductExtended[];
  initialTotal?: number;
  initialTabCounts?: ProductTabCounts;
  initialBannedCount?: number;
  loadError: string | null;
}

type PendingProductDecisionConfirm = {
  item: GwAdminProductExtended;
  decision: "APPROVE" | "REJECT";
};

function ProductThumb({ src, alt, size }: { src?: string; alt: string; size: "sm" | "lg" }) {
  const cleanSrc = (src ?? "").trim();
  const dimCls = size === "lg" ? "size-24 rounded-lg" : "size-11 rounded-md";
  if (!cleanSrc) {
    return (
      <div
        className={`flex shrink-0 items-center justify-center border border-border bg-muted text-muted-foreground ${dimCls}`}
        aria-hidden="true"
      >
        <Package className={size === "lg" ? "size-8" : "size-5"} />
      </div>
    );
  }
  return (
    <img
      src={cleanSrc}
      alt={alt}
      width={size === "lg" ? 96 : 44}
      height={size === "lg" ? 96 : 44}
      className={`shrink-0 border border-border bg-muted object-cover ${dimCls}`}
    />
  );
}

function ModerationContent({
  initialProducts,
  initialTotal,
  initialTabCounts,
  initialBannedCount = 0,
  loadError,
  initialQuery,
  initialSelected,
}: {
  initialProducts: GwAdminProductExtended[];
  initialTotal: number;
  initialTabCounts: ProductTabCounts;
  initialBannedCount: number;
  loadError: string | null;
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canReadProducts = roleHasCapability(user.role, "products.read");
  const canReviewProducts = roleHasCapability(user.role, "products.review");
  const canReadBanned = roleHasCapability(user.role, "banned_keywords.read");

  const defaultSection: ModerationSection = canReadProducts ? "products" : "banned_keywords";
  const [section, setSection] = useState<ModerationSection>(defaultSection);
  const [bannedCount, setBannedCount] = useState(initialBannedCount);

  const [products, setProducts] = useState<GwAdminProductExtended[]>(initialProducts);
  const [totalItems, setTotalItems] = useState<number>(initialTotal);
  const [tabCounts, setTabCounts] = useState<ProductTabCounts>(initialTabCounts);
  const [productStatusFilter, setProductStatusFilter] = useState<ProductStatusFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [debouncedQuery, setDebouncedQuery] = useState(initialQuery.trim());
  const [page, setPage] = useState(1);
  const [loadingProducts, setLoadingProducts] = useState(false);
  const [listError, setListError] = useState<string | null>(loadError);

  const [selectedProductId, setSelectedProductId] = useState<string | null>(initialSelected);
  const [selectedProductSnapshot, setSelectedProductSnapshot] =
    useState<GwAdminProductExtended | null>(() =>
      initialSelected ? (initialProducts.find((p) => p.id === initialSelected) ?? null) : null
    );
  const [drawerOpen, setDrawerOpen] = useState(Boolean(initialSelected));
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});
  const [productErrorId, setProductErrorId] = useState<string | null>(null);
  const [busyProductId, setBusyProductId] = useState<string | null>(null);
  const [busyDecision, setBusyDecision] = useState<"APPROVE" | "REJECT" | null>(null);
  const [confirmDecision, setConfirmDecision] = useState<PendingProductDecisionConfirm | null>(
    null
  );

  const fetchedDetailIdsRef = useRef<Set<string>>(new Set());
  const fetchSeqRef = useRef(0);
  const abortControllerRef = useRef<AbortController | null>(null);
  const didSkipInitialFetchRef = useRef(false);
  const pendingEdgeSelectRef = useRef<"first" | "last" | null>(null);

  // Debounce server-side keyword search to avoid excessive gateway requests while typing.
  useEffect(() => {
    const clean = query.trim();
    if (clean === debouncedQuery) return;
    const timer = window.setTimeout(() => {
      setDebouncedQuery(clean);
      setPage(1);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [query, debouncedQuery]);

  const reloadTabCounts = useCallback(async () => {
    if (!canReadProducts) return;
    try {
      const res = await api<{ tab_counts?: Partial<ProductTabCounts> }>(
        "/api/admin/products?tab_counts_only=true"
      );
      if (res?.tab_counts) {
        setTabCounts((prev) => normalizeProductTabCounts(res.tab_counts, prev));
      }
    } catch {
      // Non-fatal tab badge refresh failure.
    }
  }, [canReadProducts]);

  const fetchProductsPage = useCallback(
    async (opts: {
      status: ProductStatusFilter;
      keyword: string;
      page: number;
      selectEdge?: "first" | "last" | null;
    }): Promise<GwAdminProductExtended[] | null> => {
      if (!canReadProducts) return null;
      abortControllerRef.current?.abort();
      const controller = new AbortController();
      abortControllerRef.current = controller;
      const seq = ++fetchSeqRef.current;

      setLoadingProducts(true);
      setListError(null);
      try {
        const qs = buildAdminProductsQuery({
          status: opts.status,
          keyword: opts.keyword,
          page: opts.page,
          pageSize: ADMIN_MODERATION_PAGE_SIZE,
        });
        const res = await api<PaginatedResponse<GwAdminProductExtended>>(
          `/api/admin/products?${qs}`,
          { signal: controller.signal }
        );
        if (seq !== fetchSeqRef.current) return null;

        const nextItems = res.items ?? [];
        const nextTotal = typeof res.total === "number" ? Math.max(0, res.total) : nextItems.length;
        setProducts(nextItems);
        setTotalItems(nextTotal);
        if (!opts.keyword) {
          setTabCounts((prev) => ({ ...prev, [opts.status]: nextTotal }));
        }

        const edge = opts.selectEdge ?? pendingEdgeSelectRef.current;
        if (edge) {
          pendingEdgeSelectRef.current = null;
          if (nextItems.length > 0) {
            const target = edge === "first" ? nextItems[0] : nextItems[nextItems.length - 1];
            setSelectedProductId(target.id);
            setSelectedProductSnapshot(target);
          }
        }
        return nextItems;
      } catch (err) {
        if (controller.signal.aborted || seq !== fetchSeqRef.current) return null;
        setListError(getErrorMessage(err, "Không thể tải danh sách sản phẩm kiểm duyệt lúc này."));
        return null;
      } finally {
        if (seq === fetchSeqRef.current) {
          setLoadingProducts(false);
        }
      }
    },
    [canReadProducts]
  );

  // Trigger server fetch whenever status tab, debounced search keyword, or page changes.
  // Skip redundant mount fetch when default state matches SSR payload.
  useEffect(() => {
    if (!canReadProducts) return;
    if (
      !didSkipInitialFetchRef.current &&
      productStatusFilter === "all" &&
      debouncedQuery === "" &&
      page === 1
    ) {
      didSkipInitialFetchRef.current = true;
      return;
    }
    didSkipInitialFetchRef.current = true;
    void fetchProductsPage({
      status: productStatusFilter,
      keyword: debouncedQuery,
      page,
    });
  }, [canReadProducts, productStatusFilter, debouncedQuery, page, fetchProductsPage]);

  useEffect(() => {
    return () => {
      abortControllerRef.current?.abort();
    };
  }, []);

  const reloadBannedCount = useCallback(async () => {
    if (!canReadBanned) return;
    try {
      const res = await api<PaginatedResponse<{ id: string }>>(
        "/api/admin/banned-keywords?page=1&page_size=1"
      );
      setBannedCount(res.total ?? 0);
    } catch {
      // Ignore badge refresh error.
    }
  }, [canReadBanned]);

  const { page: safePage, totalPages } = useMemo(
    () => clampModerationPage(page, totalItems, ADMIN_MODERATION_PAGE_SIZE),
    [page, totalItems]
  );

  const activeIndex = useMemo(() => {
    if (!selectedProductId) return -1;
    return products.findIndex((p) => p.id === selectedProductId);
  }, [products, selectedProductId]);

  const activeProduct = useMemo(() => {
    if (!selectedProductId) return null;
    const fromPage = products.find((p) => p.id === selectedProductId);
    if (fromPage) return fromPage;
    if (selectedProductSnapshot?.id === selectedProductId) return selectedProductSnapshot;
    return null;
  }, [products, selectedProductId, selectedProductSnapshot]);

  // Fetch full product details when drawer opens (or when deep-linked ?selected=<id> isn't on page 1).
  useEffect(() => {
    if (!canReadProducts || !drawerOpen || !selectedProductId) return;
    if (fetchedDetailIdsRef.current.has(selectedProductId) && activeProduct) return;
    fetchedDetailIdsRef.current.add(selectedProductId);
    void api<{ product: GwAdminProductExtended }>(
      `/api/admin/products/${encodeURIComponent(selectedProductId)}`
    )
      .then((res) => {
        if (!res?.product) return;
        setSelectedProductSnapshot(res.product);
        setProducts((prev) =>
          prev.map((p) => (p.id === selectedProductId ? { ...p, ...res.product } : p))
        );
      })
      .catch(() => {
        // Keep list-level product payload or close drawer if deep-linked ID not found.
      });
  }, [selectedProductId, activeProduct, drawerOpen, canReadProducts]);

  const openProductSheet = useCallback((item: GwAdminProductExtended) => {
    setProductErrorId(null);
    setSelectedProductId(item.id);
    setSelectedProductSnapshot(item);
    setDrawerOpen(true);
  }, []);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (section !== "products" || products.length === 0) return;
      if (activeIndex < 0) {
        const first = products[0];
        if (!first) return;
        setProductErrorId(null);
        setSelectedProductId(first.id);
        setSelectedProductSnapshot(first);
        return;
      }
      const nextIdx = activeIndex + delta;
      if (nextIdx >= 0 && nextIdx < products.length) {
        const nextItem = products[nextIdx];
        setProductErrorId(null);
        setSelectedProductId(nextItem.id);
        setSelectedProductSnapshot(nextItem);
        return;
      }
      if (delta === 1 && safePage < totalPages) {
        pendingEdgeSelectRef.current = "first";
        setProductErrorId(null);
        setPage(safePage + 1);
      } else if (delta === -1 && safePage > 1) {
        pendingEdgeSelectRef.current = "last";
        setProductErrorId(null);
        setPage(safePage - 1);
      }
    },
    [section, products, activeIndex, safePage, totalPages]
  );

  useEffect(() => {
    if (section !== "products") return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        isEditableTarget(e.target) ||
        hasOpenRadixDialog()
      ) {
        return;
      }
      const key = e.key.toLowerCase();
      if (key === "j") {
        e.preventDefault();
        stepSelection(1);
        setDrawerOpen(true);
      } else if (key === "k") {
        e.preventDefault();
        stepSelection(-1);
        setDrawerOpen(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [section, stepSelection]);

  const handleRequestDecision = (item: GwAdminProductExtended, decision: "APPROVE" | "REJECT") => {
    if (busyProductId || !canReviewProducts) return;
    const reason = (noteDrafts[item.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setProductErrorId(item.id);
      return;
    }
    setProductErrorId(null);
    setConfirmDecision({ item, decision });
  };

  const handleProductDecision = async (
    item: GwAdminProductExtended,
    decision: "APPROVE" | "REJECT"
  ) => {
    if (busyProductId || !canReviewProducts) return;
    const reason = (noteDrafts[item.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setProductErrorId(item.id);
      setConfirmDecision(null);
      return;
    }
    setProductErrorId(null);
    setBusyProductId(item.id);
    setBusyDecision(decision);
    try {
      const res = await api<{ product: GwAdminProductExtended }>(
        `/api/admin/products/${encodeURIComponent(item.id)}/review`,
        {
          method: "POST",
          body: { decision, reason },
        }
      );
      const mergedItem = { ...item, ...res.product };
      const optimisticItems = products.map((p) => (p.id === item.id ? mergedItem : p));
      setProducts(optimisticItems);
      setSelectedProductSnapshot(mergedItem);
      setNoteDrafts((prev) => ({ ...prev, [item.id]: "" }));
      setConfirmDecision(null);

      showToast(
        decision === "APPROVE"
          ? `Đã duyệt đăng bán "${item.name}".`
          : `Đã từ chối sản phẩm "${item.name}".`
      );

      const [refreshedItems] = await Promise.all([
        fetchProductsPage({
          status: productStatusFilter,
          keyword: debouncedQuery,
          page: safePage,
        }),
        reloadTabCounts(),
      ]);

      const sourceList = refreshedItems ?? optimisticItems;
      const nextId = pickNextProductIdOnPage(sourceList, item.id, productStatusFilter);
      if (nextId) {
        const nextItem = sourceList.find((p) => p.id === nextId) ?? null;
        setSelectedProductId(nextId);
        if (nextItem) setSelectedProductSnapshot(nextItem);
      } else if (sourceList.length === 0 && safePage > 1) {
        pendingEdgeSelectRef.current = "last";
        setPage(safePage - 1);
      } else if (productStatusFilter !== "all" && sourceList.length === 0) {
        setDrawerOpen(false);
        setSelectedProductId(null);
        setSelectedProductSnapshot(null);
      }

      // Sync AdminLayout sidebar PENDING badge with the backend.
      router.refresh();
    } catch (err) {
      setConfirmDecision(null);
      showToast(getErrorMessage(err, "Không thể cập nhật kiểm duyệt sản phẩm lúc này."));
    } finally {
      setBusyProductId(null);
      setBusyDecision(null);
    }
  };

  const renderProductDetail = (item: GwAdminProductExtended) => {
    const anyBusy = Boolean(busyProductId);
    const isThisBusy = busyProductId === item.id;
    const noteInputId = `prod-note-drawer-${item.id}`;
    const totalStock = item.skus.reduce((sum, sku) => sum + sku.available_stock, 0);
    const flaggedKeywords = item.flagged_keywords ?? [];

    return (
      <div key={item.id} className="space-y-5 px-4 pb-6">
        {flaggedKeywords.length > 0 && (
          <Alert>
            <AlertTriangle className="size-4 text-badge-warning-ink" aria-hidden="true" />
            <AlertTitle>Từ khoá cần kiểm tra ({formatInt(flaggedKeywords.length)})</AlertTitle>
            <AlertDescription>
              <div className="mt-1.5 flex flex-wrap gap-1.5">
                {flaggedKeywords.map((kw) => (
                  <Badge key={kw} variant="warning">
                    {kw}
                  </Badge>
                ))}
              </div>
            </AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
          <ProductThumb src={item.images[0]} alt={item.name} size="lg" />
          <AdminDetailGrid className="min-w-0 flex-1">
            <AdminDetailField label="Khoảng giá bán" mono>
              {item.min_price_vnd === item.max_price_vnd
                ? formatVnd(item.min_price_vnd)
                : `${formatVnd(item.min_price_vnd)} đến ${formatVnd(item.max_price_vnd)}`}
            </AdminDetailField>
            <AdminDetailField label="Biến thể & tồn khả dụng" mono>
              {formatInt(item.skus.length)} SKU, tồn {formatInt(totalStock)} sản phẩm
            </AdminDetailField>
            <AdminDetailField label="Mã gian hàng" mono>
              {item.shop_id}
            </AdminDetailField>
            <AdminDetailField label="Ngày tạo" mono>
              {item.created_at ? formatDateTimeVi(item.created_at) : "Mới cập nhật"}
            </AdminDetailField>
          </AdminDetailGrid>
        </div>

        <div className="space-y-1">
          <div className="text-xs font-medium text-muted-foreground">Mô tả sản phẩm</div>
          <p className="rounded-lg border border-border bg-muted/30 p-3 text-[13px] leading-5 text-foreground [overflow-wrap:anywhere]">
            {item.description || "Gian hàng chưa nhập mô tả."}
          </p>
        </div>

        {item.skus.length > 0 && (
          <div className="space-y-2">
            <div className="text-xs font-medium text-muted-foreground">
              Danh sách phân loại ({formatInt(item.skus.length)} SKU)
            </div>
            <div className="overflow-x-auto rounded-lg border border-border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Mã SKU</TableHead>
                    <TableHead>Phân loại</TableHead>
                    <TableHead className="text-right">Giá bán</TableHead>
                    <TableHead className="text-right">Tồn khả dụng</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {item.skus.map((sku) => (
                    <TableRow key={sku.id}>
                      <TableCell className="font-mono text-xs">{sku.sku_code}</TableCell>
                      <TableCell>
                        {formatVariantLabel({
                          attributes: (sku as { attributes?: Record<string, string> | null })
                            .attributes,
                          skuName: sku.name,
                          fallback: "Mặc định",
                        })}
                      </TableCell>
                      <TableCell className="text-right">
                        <Money vnd={sku.price_vnd} size="sm" />
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {formatInt(sku.available_stock)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}

        {item.rejection_reason && (
          <div className="rounded-lg border border-border bg-badge-warning-bg/40 p-3">
            <div className="text-xs font-medium text-muted-foreground">Lý do từ chối gần nhất</div>
            <p className="mt-0.5 text-[13px] font-medium text-badge-warning-ink">
              {item.rejection_reason}
            </p>
          </div>
        )}

        {canReviewProducts ? (
          <div className="space-y-3 rounded-lg border border-border bg-card p-4">
            <Field>
              <FieldLabel htmlFor={noteInputId}>Lý do kiểm duyệt (bắt buộc khi Từ chối)</FieldLabel>
              <Input
                id={noteInputId}
                type="text"
                value={noteDrafts[item.id] ?? ""}
                onChange={(e) => {
                  setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }));
                  if (productErrorId === item.id) setProductErrorId(null);
                }}
                placeholder="Nhập ghi chú duyệt hoặc lý do từ chối gửi gian hàng..."
              />
              {productErrorId === item.id && (
                <FieldError>Vui lòng nhập lý do cụ thể trước khi từ chối sản phẩm.</FieldError>
              )}
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                disabled={anyBusy || item.status.toUpperCase() === "APPROVED"}
                onClick={() => handleRequestDecision(item, "APPROVE")}
              >
                {isThisBusy && busyDecision === "APPROVE" ? "Đang duyệt..." : "Duyệt đăng bán"}
              </Button>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={anyBusy || item.status.toUpperCase() === "REJECTED"}
                onClick={() => handleRequestDecision(item, "REJECT")}
              >
                {isThisBusy && busyDecision === "REJECT" ? "Đang từ chối..." : "Từ chối"}
              </Button>
            </div>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            Tài khoản hiện tại chỉ có quyền xem danh sách sản phẩm kiểm duyệt.
          </p>
        )}
      </div>
    );
  };

  const statusTabs = [
    { key: "all", label: "Tất cả", count: tabCounts.all },
    { key: "PENDING", label: "Chờ duyệt", count: tabCounts.PENDING },
    { key: "APPROVED", label: "Đã duyệt bán", count: tabCounts.APPROVED },
    { key: "REJECTED", label: "Đã từ chối", count: tabCounts.REJECTED },
    { key: "OUT_OF_STOCK", label: "Hết hàng", count: tabCounts.OUT_OF_STOCK },
    { key: "HIDDEN", label: "Đang ẩn", count: tabCounts.HIDDEN },
  ];

  const canStepPrev = activeIndex > 0 || safePage > 1;
  const canStepNext =
    (activeIndex >= 0 && activeIndex < products.length - 1) || safePage < totalPages;

  const renderProductsPanel = () => (
    <>
      {listError && products.length === 0 ? (
        <ErrorState
          title="Không thể tải danh sách sản phẩm"
          description={listError}
          onRetry={() => {
            void fetchProductsPage({
              status: productStatusFilter,
              keyword: debouncedQuery,
              page: safePage,
            });
            void reloadTabCounts();
          }}
        />
      ) : (
        <IndexTable
          tabs={statusTabs}
          activeTab={productStatusFilter}
          onTabChange={(next) => {
            setProductStatusFilter(next as ProductStatusFilter);
            setPage(1);
          }}
          toolbar={
            <div className="flex items-center gap-3">
              <AdminKbdHint label="chuyển sản phẩm" />
              <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground tabular-nums">
                {loadingProducts && (
                  <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden="true" />
                )}
                <span>{formatInt(totalItems)} sản phẩm</span>
              </span>
            </div>
          }
          footer={
            totalItems > 0 ? (
              <AdminPaginationBar
                page={safePage}
                totalPages={totalPages}
                totalItems={totalItems}
                itemLabel="sản phẩm"
                onPageChange={(nextPage) => setPage(nextPage)}
              />
            ) : undefined
          }
        >
          <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/35 px-4 py-2.5">
            <div className="relative min-w-0 flex-1 sm:max-w-md">
              <Search
                className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                type="search"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                }}
                placeholder="Tìm tên sản phẩm, mô tả..."
                aria-label="Tìm kiếm trong hàng đợi duyệt sản phẩm"
                className="h-9 bg-card pl-8"
              />
            </div>
            {listError && products.length > 0 && (
              <span className="text-xs text-destructive" role="alert">
                {listError}
              </span>
            )}
          </div>

          {products.length === 0 ? (
            <div className="p-4">
              {loadingProducts ? (
                <div
                  className="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground"
                  role="status"
                >
                  <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                  <span>Đang tải danh sách sản phẩm...</span>
                </div>
              ) : (
                <EmptyState
                  icon={PackageSearch}
                  title="Không có sản phẩm nào khớp với bộ lọc"
                  description="Thử đổi trạng thái kiểm duyệt hoặc xóa từ khóa tìm kiếm."
                  className="shadow-none"
                />
              )}
            </div>
          ) : (
            <div className={loadingProducts ? "opacity-60 transition-opacity" : undefined}>
              <div className="hidden md:block">
                <Table aria-label="Danh sách sản phẩm kiểm duyệt">
                  <TableHeader>
                    <TableRow>
                      <TableHead>Sản phẩm</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead className="hidden lg:table-cell">Tồn kho &amp; SKU</TableHead>
                      <TableHead className="text-right">Giá bán</TableHead>
                      <TableHead className="text-right">Thao tác</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {products.map((item) => {
                      const meta = getProductReviewStatusMeta(item.status);
                      const isSelected = drawerOpen && activeProduct?.id === item.id;
                      const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                      const flagged = item.flagged_keywords ?? [];
                      return (
                        <TableRow
                          key={item.id}
                          data-state={isSelected ? "selected" : undefined}
                          className="cursor-pointer"
                          onClick={() => openProductSheet(item)}
                        >
                          <TableCell className="max-w-[340px] whitespace-normal">
                            <div className="flex items-center gap-3">
                              <ProductThumb src={item.images[0]} alt="" size="sm" />
                              <div className="min-w-0 flex-1">
                                <div className="line-clamp-2 font-medium text-foreground">
                                  {item.name}
                                </div>
                                <div className="mt-0.5 text-xs text-muted-foreground tabular-nums">
                                  Shop {item.shop_id.slice(0, 8)}
                                  {flagged.length > 0 && (
                                    <span className="font-medium text-badge-warning-ink">
                                      {" · "}Gắn cờ: {flagged.join(", ")}
                                    </span>
                                  )}
                                </div>
                              </div>
                            </div>
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={productStatusTone(item.status)}>
                              {meta.label}
                            </StatusBadge>
                          </TableCell>
                          <TableCell className="hidden text-[13px] tabular-nums lg:table-cell">
                            <span
                              className={
                                totalStock === 0
                                  ? "font-medium text-destructive"
                                  : "text-foreground"
                              }
                            >
                              {formatInt(totalStock)} còn
                            </span>
                            <span className="text-muted-foreground">
                              {" · "}
                              {formatInt(item.skus.length)} SKU
                            </span>
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            {item.min_price_vnd === item.max_price_vnd ? (
                              <Money vnd={item.min_price_vnd} />
                            ) : (
                              <span className="text-[13px] font-medium">
                                {formatVnd(item.min_price_vnd)} – {formatVnd(item.max_price_vnd)}
                              </span>
                            )}
                          </TableCell>
                          <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              onClick={() => openProductSheet(item)}
                            >
                              Kiểm duyệt
                            </Button>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              <div className="md:hidden">
                {products.map((item) => {
                  const meta = getProductReviewStatusMeta(item.status);
                  const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                  const flagged = item.flagged_keywords ?? [];
                  return (
                    <IndexRowCard
                      key={item.id}
                      media={<ProductThumb src={item.images[0]} alt="" size="sm" />}
                      title={item.name}
                      badge={
                        <StatusBadge tone={productStatusTone(item.status)}>
                          {meta.label}
                        </StatusBadge>
                      }
                      meta={
                        <>
                          <span className="tabular-nums">{formatVnd(item.min_price_vnd)}</span>
                          <span>·</span>
                          <span className="tabular-nums">
                            {formatInt(item.skus.length)} SKU, tồn {formatInt(totalStock)}
                          </span>
                          {flagged.length > 0 && (
                            <span className="font-medium text-badge-warning-ink">
                              · Gắn cờ: {flagged.join(", ")}
                            </span>
                          )}
                        </>
                      }
                      onClick={() => openProductSheet(item)}
                    />
                  );
                })}
              </div>
            </div>
          )}
        </IndexTable>
      )}

      <Sheet open={drawerOpen && Boolean(activeProduct)} onOpenChange={setDrawerOpen}>
        <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-xl">
          {activeProduct ? (
            <>
              <SheetHeader className="border-b border-border pr-12">
                <div className="flex flex-wrap items-center gap-2">
                  <SheetTitle>{activeProduct.name}</SheetTitle>
                  <StatusBadge tone={productStatusTone(activeProduct.status)}>
                    {getProductReviewStatusMeta(activeProduct.status).label}
                  </StatusBadge>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
                  <SheetDescription className="tabular-nums">
                    Mã {activeProduct.id.slice(0, 8)} · Shop {activeProduct.shop_id.slice(0, 8)}
                  </SheetDescription>
                  {products.length > 0 && (
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={!canStepPrev || loadingProducts}
                        onClick={() => stepSelection(-1)}
                        aria-label="Sản phẩm trước (K)"
                      >
                        <ChevronLeft className="size-3.5" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={!canStepNext || loadingProducts}
                        onClick={() => stepSelection(1)}
                        aria-label="Sản phẩm tiếp theo (J)"
                      >
                        <ChevronRight className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  )}
                </div>
              </SheetHeader>
              {renderProductDetail(activeProduct)}
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      <AdminConfirmActionDialog
        open={Boolean(confirmDecision)}
        title={
          confirmDecision?.decision === "APPROVE"
            ? "Xác nhận duyệt đăng bán sản phẩm"
            : "Xác nhận từ chối sản phẩm"
        }
        description={
          confirmDecision
            ? confirmDecision.decision === "APPROVE"
              ? `Phê duyệt sản phẩm "${confirmDecision.item.name}" để hiển thị trên site gian hàng?`
              : `Từ chối sản phẩm "${confirmDecision.item.name}" và gửi lý do về người bán?`
            : ""
        }
        confirmLabel={
          confirmDecision?.decision === "APPROVE" ? "Xác nhận duyệt" : "Xác nhận từ chối"
        }
        confirmVariant={confirmDecision?.decision === "APPROVE" ? "default" : "destructive"}
        busy={Boolean(busyProductId)}
        onCancel={() => setConfirmDecision(null)}
        onConfirm={() => {
          if (confirmDecision) {
            void handleProductDecision(confirmDecision.item, confirmDecision.decision);
          }
        }}
      >
        {confirmDecision && noteDrafts[confirmDecision.item.id]?.trim() ? (
          <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs">
            Ghi chú: {noteDrafts[confirmDecision.item.id].trim()}
          </div>
        ) : null}
      </AdminConfirmActionDialog>
    </>
  );

  const showBothTabs = canReadProducts && canReadBanned;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Kiểm duyệt nội dung"
        subtitle="Duyệt sản phẩm gian hàng đăng bán và quản lý quy tắc từ khoá cấm."
      />

      {showBothTabs ? (
        <Tabs
          value={section}
          onValueChange={(val) => setSection(val as ModerationSection)}
          className="space-y-4"
        >
          <TabsList aria-label="Chuyển khu vực kiểm duyệt">
            <TabsTrigger value="products" className="gap-1.5">
              <span>Sản phẩm</span>
              <Badge variant="neutral" className="px-1.5 py-0 text-[11px] tabular-nums">
                {formatInt(tabCounts.PENDING)}
              </Badge>
            </TabsTrigger>
            <TabsTrigger value="banned_keywords" className="gap-1.5">
              <span>Từ khoá cấm</span>
              <Badge variant="neutral" className="px-1.5 py-0 text-[11px] tabular-nums">
                {formatInt(bannedCount)}
              </Badge>
            </TabsTrigger>
          </TabsList>

          <TabsContent value="products" className="space-y-4">
            {renderProductsPanel()}
          </TabsContent>

          <TabsContent value="banned_keywords" className="space-y-4">
            <AdminBannedKeywordsTab
              onMutated={() => {
                void reloadBannedCount();
                void fetchProductsPage({
                  status: productStatusFilter,
                  keyword: debouncedQuery,
                  page: safePage,
                });
                void reloadTabCounts();
                router.refresh();
              }}
            />
          </TabsContent>
        </Tabs>
      ) : canReadBanned && !canReadProducts ? (
        <AdminBannedKeywordsTab
          onMutated={() => {
            void reloadBannedCount();
            void fetchProductsPage({
              status: productStatusFilter,
              keyword: debouncedQuery,
              page: safePage,
            });
            void reloadTabCounts();
            router.refresh();
          }}
        />
      ) : (
        renderProductsPanel()
      )}
    </div>
  );
}

function AdminModerationRoute({
  initialProducts,
  initialTotal,
  initialTabCounts,
  initialBannedCount,
  loadError,
}: AdminModerationClientProps) {
  const resolvedTotal = initialTotal ?? initialProducts.length;
  const resolvedTabCounts = useMemo(
    () =>
      initialTabCounts ?? {
        ...createEmptyProductTabCounts(),
        all: resolvedTotal,
      },
    [initialTabCounts, resolvedTotal]
  );

  return (
    <AdminStateGate
      title="Kiểm duyệt nội dung"
      emptyTitle="Hàng đợi duyệt sản phẩm đang trống"
      emptyMessage="Tất cả sản phẩm gửi lên đã được xử lý."
      errorTitle="Không thể tải hàng đợi duyệt sản phẩm"
      errorMessage="Dịch vụ duyệt sản phẩm tạm gián đoạn. Vui lòng thử lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <ModerationContent
          initialProducts={initialProducts}
          initialTotal={resolvedTotal}
          initialTabCounts={resolvedTabCounts}
          initialBannedCount={initialBannedCount ?? 0}
          loadError={loadError}
          initialQuery={initialQuery}
          initialSelected={initialSelected}
        />
      )}
    </AdminStateGate>
  );
}

export default function AdminModerationClient(props: AdminModerationClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Kiểm duyệt nội dung" />}>
      <AdminModerationRoute {...props} />
    </Suspense>
  );
}
