"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle, ChevronLeft, ChevronRight, PackageSearch, Search } from "lucide-react";
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

type ModerationSection = "products" | "banned_keywords";
type ProductStatusFilter = "all" | "PENDING" | "APPROVED" | "REJECTED" | "HIDDEN" | "OUT_OF_STOCK";

const PRODUCTS_PAGE_SIZE = 15;

export interface AdminModerationClientProps {
  initialProducts: GwAdminProductExtended[];
  initialBannedCount?: number;
  loadError: string | null;
}

type PendingProductDecisionConfirm = {
  item: GwAdminProductExtended;
  decision: "APPROVE" | "REJECT";
};

function ModerationContent({
  initialProducts,
  initialBannedCount = 0,
  loadError,
  initialQuery,
  initialSelected,
}: {
  initialProducts: GwAdminProductExtended[];
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
  const [productStatusFilter, setProductStatusFilter] = useState<ProductStatusFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [page, setPage] = useState(1);
  const [selectedProductId, setSelectedProductId] = useState<string | null>(initialSelected);
  const [drawerOpen, setDrawerOpen] = useState(
    Boolean(initialSelected && initialProducts.some((p) => p.id === initialSelected))
  );
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});
  const [productErrorId, setProductErrorId] = useState<string | null>(null);
  const [busyProductId, setBusyProductId] = useState<string | null>(null);
  const [busyDecision, setBusyDecision] = useState<"APPROVE" | "REJECT" | null>(null);
  const [confirmDecision, setConfirmDecision] = useState<PendingProductDecisionConfirm | null>(
    null
  );

  const fetchedDetailIdsRef = useRef<Set<string>>(new Set());

  const reloadProducts = useCallback(async () => {
    if (!canReadProducts) return;
    try {
      const res = await api<PaginatedResponse<GwAdminProductExtended>>(
        "/api/admin/products?page=1&page_size=100"
      );
      setProducts(res.items ?? []);
    } catch {
      // Non-fatal refresh after keyword mutation.
    }
  }, [canReadProducts]);

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

  const counts = useMemo(() => {
    const map: Record<ProductStatusFilter, number> = {
      all: products.length,
      PENDING: 0,
      APPROVED: 0,
      REJECTED: 0,
      OUT_OF_STOCK: 0,
      HIDDEN: 0,
    };
    for (const p of products) {
      const st = p.status.toUpperCase() as ProductStatusFilter;
      if (st in map) map[st] += 1;
    }
    return map;
  }, [products]);

  const filteredProducts = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = products.filter((item) => {
      if (productStatusFilter !== "all" && item.status.toUpperCase() !== productStatusFilter) {
        return false;
      }
      if (!q) return true;
      return (
        item.name.toLowerCase().includes(q) ||
        item.id.toLowerCase().includes(q) ||
        item.shop_id.toLowerCase().includes(q) ||
        (item.flagged_keywords ?? []).some((kw) => kw.toLowerCase().includes(q)) ||
        item.skus.some(
          (sku) => sku.sku_code.toLowerCase().includes(q) || sku.name.toLowerCase().includes(q)
        )
      );
    });

    const rank = (status: string) => {
      const st = status.toUpperCase();
      if (st === "PENDING") return 0;
      if (st === "REJECTED") return 1;
      if (st === "APPROVED") return 2;
      return 3;
    };
    return [...list].sort((a, b) => rank(a.status) - rank(b.status));
  }, [products, productStatusFilter, query]);

  const totalPages = Math.max(1, Math.ceil(filteredProducts.length / PRODUCTS_PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pagedProducts = useMemo(() => {
    const start = (safePage - 1) * PRODUCTS_PAGE_SIZE;
    return filteredProducts.slice(start, start + PRODUCTS_PAGE_SIZE);
  }, [filteredProducts, safePage]);

  const activeIndex = useMemo(() => {
    if (!selectedProductId) return -1;
    return filteredProducts.findIndex((p) => p.id === selectedProductId);
  }, [filteredProducts, selectedProductId]);

  const activeProduct = useMemo(() => {
    if (!selectedProductId) return null;
    return (
      filteredProducts.find((p) => p.id === selectedProductId) ??
      products.find((p) => p.id === selectedProductId) ??
      null
    );
  }, [filteredProducts, products, selectedProductId]);

  useEffect(() => {
    if (!canReadProducts || !drawerOpen || !activeProduct) return;
    if (fetchedDetailIdsRef.current.has(activeProduct.id)) return;
    fetchedDetailIdsRef.current.add(activeProduct.id);
    void api<{ product: GwAdminProductExtended }>(
      `/api/admin/products/${encodeURIComponent(activeProduct.id)}`
    )
      .then((res) => {
        if (!res?.product) return;
        setProducts((prev) =>
          prev.map((p) => (p.id === activeProduct.id ? { ...p, ...res.product } : p))
        );
      })
      .catch(() => {
        // Keep list-level product payload.
      });
  }, [activeProduct, drawerOpen, canReadProducts]);

  useEffect(() => {
    if (!activeProduct && drawerOpen) {
      setDrawerOpen(false);
    }
  }, [activeProduct, drawerOpen]);

  const openProductSheet = useCallback((item: GwAdminProductExtended) => {
    setProductErrorId(null);
    setSelectedProductId(item.id);
    setDrawerOpen(true);
  }, []);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (section !== "products" || filteredProducts.length === 0) return;
      const cur = activeIndex >= 0 ? activeIndex : 0;
      const nextIdx = Math.max(0, Math.min(filteredProducts.length - 1, cur + delta));
      const nextItem = filteredProducts[nextIdx];
      if (!nextItem) return;
      setProductErrorId(null);
      setSelectedProductId(nextItem.id);
      setPage(Math.floor(nextIdx / PRODUCTS_PAGE_SIZE) + 1);
    },
    [section, filteredProducts, activeIndex]
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

  const pickNextAfterDecision = (
    currentId: string,
    updatedProduct?: GwAdminProductExtended
  ): string | null => {
    const currentIdx = filteredProducts.findIndex((p) => p.id === currentId);
    const resolveItem = (p: GwAdminProductExtended) =>
      updatedProduct && p.id === updatedProduct.id ? updatedProduct : p;
    const remaining = filteredProducts
      .map(resolveItem)
      .filter(
        (p) => productStatusFilter === "all" || p.status.toUpperCase() === productStatusFilter
      );

    if (remaining.length === 0) return null;

    for (let i = Math.max(0, currentIdx + 1); i < filteredProducts.length; i++) {
      const cand = resolveItem(filteredProducts[i]);
      if (cand.id !== currentId && cand.status.toUpperCase() === "PENDING") {
        return cand.id;
      }
    }
    const afterSame = filteredProducts[currentIdx + 1];
    if (afterSame && remaining.some((p) => p.id === afterSame.id)) {
      return afterSame.id;
    }
    const other = remaining.find((p) => p.id !== currentId);
    return other?.id ?? remaining[0].id;
  };

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
      setProducts((prev) => prev.map((p) => (p.id === item.id ? mergedItem : p)));
      setNoteDrafts((prev) => ({ ...prev, [item.id]: "" }));
      setConfirmDecision(null);
      const nextId = pickNextAfterDecision(item.id, mergedItem);
      if (nextId) {
        setSelectedProductId(nextId);
      }
      showToast(
        decision === "APPROVE"
          ? `Đã duyệt đăng bán "${item.name}".`
          : `Đã từ chối sản phẩm "${item.name}".`
      );
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
    const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";
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
          <img
            src={thumb}
            alt={item.name}
            width={96}
            height={96}
            className="size-24 shrink-0 rounded-lg border border-border bg-muted object-cover"
          />
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
                      <TableCell>{sku.name}</TableCell>
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
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
    { key: "APPROVED", label: "Đã duyệt bán", count: counts.APPROVED },
    { key: "REJECTED", label: "Đã từ chối", count: counts.REJECTED },
    { key: "OUT_OF_STOCK", label: "Hết hàng", count: counts.OUT_OF_STOCK },
    { key: "HIDDEN", label: "Đang ẩn", count: counts.HIDDEN },
  ];

  const renderProductsPanel = () => (
    <>
      {loadError ? (
        <ErrorState
          title="Không thể tải danh sách sản phẩm"
          description={loadError}
          onRetry={() => router.refresh()}
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
              <span className="text-xs text-muted-foreground tabular-nums">
                {formatInt(filteredProducts.length)} sản phẩm
              </span>
            </div>
          }
          footer={
            filteredProducts.length > 0 ? (
              <AdminPaginationBar
                page={safePage}
                totalPages={totalPages}
                totalItems={filteredProducts.length}
                itemLabel="sản phẩm"
                onPageChange={setPage}
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
                  setPage(1);
                }}
                placeholder="Tìm tên sản phẩm, SKU, từ khoá gắn cờ, mã shop..."
                aria-label="Tìm kiếm trong hàng đợi duyệt sản phẩm"
                className="h-9 bg-card pl-8"
              />
            </div>
          </div>

          {filteredProducts.length === 0 ? (
            <div className="p-4">
              <EmptyState
                icon={PackageSearch}
                title="Không có sản phẩm nào khớp với bộ lọc"
                description="Thử đổi trạng thái kiểm duyệt hoặc xóa từ khóa tìm kiếm."
                className="shadow-none"
              />
            </div>
          ) : (
            <>
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
                    {pagedProducts.map((item) => {
                      const meta = getProductReviewStatusMeta(item.status);
                      const isSelected = drawerOpen && activeProduct?.id === item.id;
                      const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                      const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";
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
                              <img
                                src={thumb}
                                alt=""
                                width={44}
                                height={44}
                                className="size-11 shrink-0 rounded-md border border-border bg-muted object-cover"
                              />
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
                {pagedProducts.map((item) => {
                  const meta = getProductReviewStatusMeta(item.status);
                  const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                  const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";
                  const flagged = item.flagged_keywords ?? [];
                  return (
                    <IndexRowCard
                      key={item.id}
                      media={
                        <img
                          src={thumb}
                          alt=""
                          width={44}
                          height={44}
                          className="size-11 rounded-md border border-border bg-muted object-cover"
                        />
                      }
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
            </>
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
                  {activeIndex >= 0 && (
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={activeIndex <= 0}
                        onClick={() => stepSelection(-1)}
                        aria-label="Sản phẩm trước (K)"
                      >
                        <ChevronLeft className="size-3.5" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-xs"
                        disabled={activeIndex >= filteredProducts.length - 1}
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
                {formatInt(counts.PENDING)}
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
                void reloadProducts();
              }}
            />
          </TabsContent>
        </Tabs>
      ) : canReadBanned && !canReadProducts ? (
        <AdminBannedKeywordsTab
          onMutated={() => {
            void reloadBannedCount();
            void reloadProducts();
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
  initialBannedCount,
  loadError,
}: AdminModerationClientProps) {
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
