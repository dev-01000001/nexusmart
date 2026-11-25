"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconChevronLeft, IconChevronRight, IconSearch, StateView } from "@/components";
import { hasOpenModal } from "@/components/modalFocus";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getProductReviewStatusMeta, type GwAdminProduct } from "@/features/admin/adminApiTypes";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
import { AdminDrawer, SlidingTabBar, isEditableElement } from "@/features/admin/AdminUi";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ProductStatusFilter = "all" | "PENDING" | "APPROVED" | "REJECTED" | "HIDDEN" | "OUT_OF_STOCK";

export interface AdminModerationClientProps {
  initialProducts: GwAdminProduct[];
  loadError: string | null;
}

function ModerationContent({
  initialProducts,
  loadError,
  initialQuery,
  initialSelected,
}: {
  initialProducts: GwAdminProduct[];
  loadError: string | null;
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [products, setProducts] = useState<GwAdminProduct[]>(initialProducts);
  const [productStatusFilter, setProductStatusFilter] = useState<ProductStatusFilter>("all");
  const [query, setQuery] = useState(initialQuery);
  const [selectedProductId, setSelectedProductId] = useState<string | null>(
    initialSelected ?? initialProducts[0]?.id ?? null
  );
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});
  const [productErrorId, setProductErrorId] = useState<string | null>(null);
  const [busyProductId, setBusyProductId] = useState<string | null>(null);
  const [busyDecision, setBusyDecision] = useState<"APPROVE" | "REJECT" | null>(null);

  const rowRefs = useRef<Map<string, HTMLButtonElement>>(new Map());
  const deepLinkHandledRef = useRef(false);

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

  const activeIndex = useMemo(() => {
    if (filteredProducts.length === 0) return -1;
    if (selectedProductId) {
      const idx = filteredProducts.findIndex((p) => p.id === selectedProductId);
      if (idx >= 0) return idx;
    }
    return 0;
  }, [filteredProducts, selectedProductId]);

  const activeProduct = activeIndex >= 0 ? filteredProducts[activeIndex] : null;

  useEffect(() => {
    if (deepLinkHandledRef.current) return;
    deepLinkHandledRef.current = true;
    if (
      initialSelected &&
      typeof window !== "undefined" &&
      window.innerWidth <= 960 &&
      initialProducts.some((p) => p.id === initialSelected)
    ) {
      setDrawerOpen(true);
    }
  }, [initialSelected, initialProducts]);

  useEffect(() => {
    if (!activeProduct && drawerOpen) {
      setDrawerOpen(false);
    }
  }, [activeProduct, drawerOpen]);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (filteredProducts.length === 0) return;
      const cur = activeIndex >= 0 ? activeIndex : 0;
      const nextIdx = Math.max(0, Math.min(filteredProducts.length - 1, cur + delta));
      const nextItem = filteredProducts[nextIdx];
      if (!nextItem) return;
      setProductErrorId(null);
      setSelectedProductId(nextItem.id);
      rowRefs.current.get(nextItem.id)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    },
    [filteredProducts, activeIndex]
  );

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || isEditableElement(e.target) || hasOpenModal()) {
        return;
      }
      const key = e.key.toLowerCase();
      if (key === "j") {
        e.preventDefault();
        stepSelection(1);
      } else if (key === "k") {
        e.preventDefault();
        stepSelection(-1);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [stepSelection]);

  const pickNextAfterDecision = (
    currentId: string,
    updatedProduct?: GwAdminProduct
  ): string | null => {
    const currentIdx = filteredProducts.findIndex((p) => p.id === currentId);
    const resolveItem = (p: GwAdminProduct) =>
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

  const handleProductDecision = async (item: GwAdminProduct, decision: "APPROVE" | "REJECT") => {
    if (busyProductId) return;
    const reason = (noteDrafts[item.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setProductErrorId(item.id);
      return;
    }
    setProductErrorId(null);
    setBusyProductId(item.id);
    setBusyDecision(decision);
    try {
      const res = await api<{ product: GwAdminProduct }>(
        `/api/admin/products/${encodeURIComponent(item.id)}/review`,
        {
          method: "POST",
          body: { decision, reason },
        }
      );
      const mergedItem = { ...item, ...res.product };
      setProducts((prev) => prev.map((p) => (p.id === item.id ? { ...p, ...res.product } : p)));
      setNoteDrafts((prev) => ({ ...prev, [item.id]: "" }));
      const nextId = pickNextAfterDecision(item.id, mergedItem);
      if (nextId) {
        setSelectedProductId(nextId);
        window.requestAnimationFrame(() => {
          rowRefs.current.get(nextId)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
        });
      }
      showToast(
        decision === "APPROVE"
          ? `Đã duyệt đăng bán "${item.name}".`
          : `Đã từ chối sản phẩm "${item.name}".`
      );
      router.refresh();
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể cập nhật kiểm duyệt sản phẩm lúc này.";
      showToast(msg);
    } finally {
      setBusyProductId(null);
      setBusyDecision(null);
    }
  };

  const renderProductDetail = (item: GwAdminProduct, inDrawer = false) => {
    const meta = getProductReviewStatusMeta(item.status);
    const anyBusy = Boolean(busyProductId);
    const isThisBusy = busyProductId === item.id;
    const noteInputId = `prod-note-${inDrawer ? "drawer" : "pane"}-${item.id}`;
    const totalStock = item.skus.reduce((sum, sku) => sum + sku.available_stock, 0);
    const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";

    return (
      <div
        key={item.id}
        className={`${inDrawer ? styles.drawerContent : shared.detailWellCard} ${shared.inspectorSlide}`}
      >
        {!inDrawer && (
          <div className={shared.detailHeader}>
            <div>
              <h2 className={shared.detailHeading}>{item.name}</h2>
              <span className={shared.cellSub}>
                Mã {item.id.slice(0, 8)}, Gian hàng {item.shop_id.slice(0, 8)}
              </span>
            </div>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
              <div className={styles.stepperBtns}>
                <button
                  type="button"
                  className={styles.stepperBtn}
                  disabled={activeIndex <= 0}
                  onClick={() => stepSelection(-1)}
                  aria-label="Sản phẩm trước (K)"
                  title="Sản phẩm trước (K)"
                >
                  <IconChevronLeft size={14} />
                </button>
                <button
                  type="button"
                  className={styles.stepperBtn}
                  disabled={activeIndex >= filteredProducts.length - 1}
                  onClick={() => stepSelection(1)}
                  aria-label="Sản phẩm tiếp theo (J)"
                  title="Sản phẩm tiếp theo (J)"
                >
                  <IconChevronRight size={14} />
                </button>
              </div>
            </div>
          </div>
        )}

        <div className={styles.previewRow}>
          <img src={thumb} alt={item.name} className={styles.thumbLg} width={104} height={104} />
          <div className={styles.previewMeta}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Khoảng giá bán</span>
                <span className={`${shared.detailVal} num`}>
                  {item.min_price_vnd === item.max_price_vnd
                    ? formatVnd(item.min_price_vnd)
                    : `${formatVnd(item.min_price_vnd)} đến ${formatVnd(item.max_price_vnd)}`}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Biến thể &amp; tồn khả dụng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(item.skus.length)} SKU, tồn {formatInt(totalStock)} sản phẩm
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã gian hàng</span>
                <span className={`${shared.detailValPlain} num`}>{item.shop_id}</span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Ngày tạo</span>
                <span className={`${shared.detailValPlain} num`}>
                  {item.created_at ? formatDateTimeVi(item.created_at) : "Mới cập nhật"}
                </span>
              </div>
            </div>
          </div>
        </div>

        <div className={shared.detailField}>
          <span className={shared.detailKey}>Mô tả sản phẩm</span>
          <span className={shared.detailValPlain}>
            {item.description || "Gian hàng chưa nhập mô tả."}
          </span>
        </div>

        {item.skus.length > 0 && (
          <div className={shared.detailField}>
            <span className={shared.detailKey}>
              Danh sách phân loại ({formatInt(item.skus.length)} SKU)
            </span>
            <div className={styles.skuTableWrap}>
              <table className={styles.skuTable}>
                <thead>
                  <tr>
                    <th>Mã SKU</th>
                    <th>Phân loại</th>
                    <th className={shared.colNum}>Giá bán</th>
                    <th className={shared.colNum}>Tồn khả dụng</th>
                  </tr>
                </thead>
                <tbody>
                  {item.skus.map((sku) => (
                    <tr key={sku.id}>
                      <td className="num">{sku.sku_code}</td>
                      <td>{sku.name}</td>
                      <td className={shared.colNum}>{formatVnd(sku.price_vnd)}</td>
                      <td className={shared.colNum}>{formatInt(sku.available_stock)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {item.rejection_reason && (
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Lý do từ chối gần nhất</span>
            <span className={shared.priorityText}>{item.rejection_reason}</span>
          </div>
        )}

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={noteInputId} className={shared.fieldLabel}>
              Lý do kiểm duyệt (bắt buộc khi Từ chối)
            </label>
            <input
              id={noteInputId}
              type="text"
              className={shared.textInput}
              value={noteDrafts[item.id] ?? ""}
              onChange={(e) => {
                setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }));
                if (productErrorId === item.id) setProductErrorId(null);
              }}
              placeholder="Nhập ghi chú duyệt hoặc lý do từ chối gửi gian hàng..."
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
              loading={isThisBusy && busyDecision === "APPROVE"}
              disabled={anyBusy || item.status.toUpperCase() === "APPROVED"}
              onClick={() => void handleProductDecision(item, "APPROVE")}
            >
              Duyệt đăng bán
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              loading={isThisBusy && busyDecision === "REJECT"}
              disabled={anyBusy || item.status.toUpperCase() === "REJECTED"}
              onClick={() => void handleProductDecision(item, "REJECT")}
            >
              Từ chối
            </Button>
          </div>
        </div>
      </div>
    );
  };

  const tabs: Array<{ id: ProductStatusFilter; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: counts.all },
    { id: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
    { id: "APPROVED", label: "Đã duyệt bán", count: counts.APPROVED },
    { id: "REJECTED", label: "Đã từ chối", count: counts.REJECTED },
    { id: "OUT_OF_STOCK", label: "Hết hàng", count: counts.OUT_OF_STOCK },
    { id: "HIDDEN", label: "Đang ẩn", count: counts.HIDDEN },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Duyệt sản phẩm</h1>
        </div>
        <div className={shared.titleActions}>
          <span className={shared.kbdHint} aria-label="Phím tắt chuyển sản phẩm">
            <kbd className={shared.kbdKey}>J</kbd>
            <kbd className={shared.kbdKey}>K</kbd>
            <span>chuyển sản phẩm</span>
          </span>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={productStatusFilter}
        onChange={setProductStatusFilter}
        ariaLabel="Lọc trạng thái kiểm duyệt sản phẩm"
      />

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
              placeholder="Tìm tên sản phẩm, mã SKU, mã gian hàng..."
              aria-label="Tìm kiếm trong hàng đợi duyệt sản phẩm"
            />
          </div>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filteredProducts.length)} sản phẩm</span>
        </div>
      </div>

      {loadError ? (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách sản phẩm"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      ) : filteredProducts.length === 0 ? (
        <p className={shared.emptyRow}>Không có sản phẩm nào khớp với bộ lọc.</p>
      ) : (
        <div className={shared.reviewSplit}>
          <div className={shared.queueColumn}>
            <div className={shared.queueHead}>
              <span>Hàng đợi kiểm duyệt</span>
              <span>
                {activeIndex + 1} / {formatInt(filteredProducts.length)}
              </span>
            </div>
            <ul className={shared.queueList} aria-label="Danh sách sản phẩm kiểm duyệt">
              {filteredProducts.map((item) => {
                const meta = getProductReviewStatusMeta(item.status);
                const isSelected = activeProduct?.id === item.id;
                const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";
                return (
                  <li key={item.id} className={shared.queueItem}>
                    <button
                      ref={(node) => {
                        if (node) rowRefs.current.set(item.id, node);
                        else rowRefs.current.delete(item.id);
                      }}
                      type="button"
                      className={`${shared.queueItemBtn} ${isSelected ? shared.queueItemActive : ""}`}
                      aria-current={isSelected ? "true" : undefined}
                      onClick={() => {
                        setProductErrorId(null);
                        setSelectedProductId(item.id);
                        if (typeof window !== "undefined" && window.innerWidth <= 960) {
                          setDrawerOpen(true);
                        }
                      }}
                    >
                      <img src={thumb} alt="" className={styles.thumbSm} width={46} height={46} />
                      <span className={shared.queueItemBody}>
                        <span className={shared.queueItemTop}>
                          <span className={shared.queueItemTitle}>{item.name}</span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </span>
                        <span className={shared.queueItemMeta}>
                          {item.min_price_vnd === item.max_price_vnd
                            ? formatVnd(item.min_price_vnd)
                            : `${formatVnd(item.min_price_vnd)} đến ${formatVnd(item.max_price_vnd)}`}
                          , {formatInt(item.skus.length)} SKU, tồn {formatInt(totalStock)}
                        </span>
                        <span className={shared.queueItemMeta}>
                          Shop {item.shop_id.slice(0, 8)}
                        </span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>

          {activeProduct && (
            <section className={shared.inspectorPane} aria-label="Chi tiết kiểm duyệt sản phẩm">
              {renderProductDetail(activeProduct, false)}
            </section>
          )}
        </div>
      )}

      <AdminDrawer
        open={drawerOpen && Boolean(activeProduct)}
        onClose={() => setDrawerOpen(false)}
        title={activeProduct?.name ?? ""}
        subtitle={activeProduct ? `Shop ${activeProduct.shop_id.slice(0, 8)}` : undefined}
        headerBadge={
          activeProduct ? (
            <StatusPill
              label={getProductReviewStatusMeta(activeProduct.status).label}
              tone={getProductReviewStatusMeta(activeProduct.status).tone}
            />
          ) : undefined
        }
        width="lg"
      >
        {activeProduct && renderProductDetail(activeProduct, true)}
      </AdminDrawer>
    </div>
  );
}

function AdminModerationRoute({ initialProducts, loadError }: AdminModerationClientProps) {
  return (
    <AdminStateGate
      title="Duyệt sản phẩm"
      emptyTitle="Hàng đợi duyệt sản phẩm đang trống"
      emptyMessage="Tất cả sản phẩm gửi lên đã được xử lý."
      errorTitle="Không thể tải hàng đợi duyệt sản phẩm"
      errorMessage="Dịch vụ duyệt sản phẩm tạm gián đoạn. Vui lòng thử lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <ModerationContent
          initialProducts={initialProducts}
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
    <Suspense fallback={<AdminSuspenseFallback title="Duyệt sản phẩm" />}>
      <AdminModerationRoute {...props} />
    </Suspense>
  );
}
