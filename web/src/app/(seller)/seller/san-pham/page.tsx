"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type {
  GatewayCategory,
  GatewayProduct,
  GatewayShop,
  GatewaySKU,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconPlus, IconSearch, StateView } from "@/components";
import { formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerShopGate } from "@/features/seller/SellerContext";
import { getProductCoverImage, getProductStatusMeta } from "@/features/seller/types";
import { parseUiState } from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type ProductFilterKey = "all" | "APPROVED" | "PENDING" | "REJECTED" | "HIDDEN" | "LOW_OOS";

function isLowOrOutProduct(p: GatewayProduct): boolean {
  if (p.status === "OUT_OF_STOCK") return true;
  return (p.skus ?? []).some((s) => s.is_active && s.available_stock <= 5);
}

function formatPriceRange(p: GatewayProduct): string {
  const minP = p.min_price_vnd;
  const maxP = p.max_price_vnd;
  if (minP === maxP || maxP <= 0) return formatVnd(minP);
  return `${formatVnd(minP)} - ${formatVnd(maxP)}`;
}

function SellerProductsInner({ shop }: { shop: GatewayShop }) {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const rawFilter = searchParams.get("filter");
  const normalizedInitialFilter: ProductFilterKey =
    rawFilter === "low" || rawFilter === "oos" || rawFilter === "LOW_OOS"
      ? "LOW_OOS"
      : rawFilter && ["all", "APPROVED", "PENDING", "REJECTED", "HIDDEN"].includes(rawFilter)
        ? (rawFilter as ProductFilterKey)
        : "all";

  const { showToast } = useMockStore();

  const [products, setProducts] = useState<GatewayProduct[]>([]);
  const [categories, setCategories] = useState<GatewayCategory[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [conflictNotice, setConflictNotice] = useState<string | null>(null);

  const [filter, setFilter] = useState<ProductFilterKey>(normalizedInitialFilter);
  const [query, setQuery] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("all");

  const [expandedProductIds, setExpandedProductIds] = useState<Record<string, boolean>>({});
  const [stockDrafts, setStockDrafts] = useState<Record<string, string>>({});
  const [savingSkuId, setSavingSkuId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [prodRes, catRes] = await Promise.all([
        api<PaginatedResponse<GatewayProduct>>("seller/products?page=1&page_size=100"),
        api<{ categories: GatewayCategory[] }>("categories").catch(() => ({ categories: [] })),
      ]);
      const items = prodRes.items ?? [];
      setProducts(items);
      setCategories(catRes.categories ?? []);
      setStockDrafts(() => {
        const next: Record<string, string> = {};
        for (const p of items) {
          for (const s of p.skus ?? []) {
            next[s.id] = String(s.stock);
          }
        }
        return next;
      });
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách sản phẩm lúc này."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!uiState) {
      void loadData();
    }
  }, [uiState, loadData]);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) {
      map.set(c.id, c.name);
    }
    return map;
  }, [categories]);

  const counts = useMemo(() => {
    return {
      all: products.length,
      APPROVED: products.filter((p) => p.status === "APPROVED").length,
      PENDING: products.filter((p) => p.status === "PENDING").length,
      REJECTED: products.filter((p) => p.status === "REJECTED").length,
      HIDDEN: products.filter((p) => p.status === "HIDDEN").length,
      LOW_OOS: products.filter((p) => isLowOrOutProduct(p)).length,
    };
  }, [products]);

  const totalSkuCount = useMemo(
    () => products.reduce((sum, p) => sum + (p.skus ?? []).filter((s) => s.is_active).length, 0),
    [products]
  );

  const lowStockRows = useMemo(() => {
    const rows: Array<{ product: GatewayProduct; sku: GatewaySKU }> = [];
    for (const p of products) {
      for (const s of p.skus ?? []) {
        if (s.is_active && s.available_stock <= 5) {
          rows.push({ product: p, sku: s });
        }
      }
    }
    return rows.sort((a, b) => a.sku.available_stock - b.sku.available_stock).slice(0, 8);
  }, [products]);

  const filteredProducts = useMemo(() => {
    const q = query.trim().toLowerCase();
    return products.filter((p) => {
      if (filter === "LOW_OOS") {
        if (!isLowOrOutProduct(p)) return false;
      } else if (filter !== "all" && p.status !== filter) {
        return false;
      }
      if (categoryFilter !== "all" && p.category_id !== categoryFilter) return false;
      if (q) {
        const hitName = p.name.toLowerCase().includes(q);
        const hitSku = (p.skus ?? []).some(
          (s) => s.sku_code.toLowerCase().includes(q) || s.name.toLowerCase().includes(q)
        );
        if (!hitName && !hitSku) return false;
      }
      return true;
    });
  }, [products, filter, categoryFilter, query]);

  const countCards: Array<{ key: ProductFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "APPROVED", label: "Đang bán", count: counts.APPROVED },
    { key: "PENDING", label: "Chờ duyệt", count: counts.PENDING },
    { key: "REJECTED", label: "Bị từ chối", count: counts.REJECTED },
    { key: "HIDDEN", label: "Đang ẩn", count: counts.HIDDEN },
    { key: "LOW_OOS", label: "Hết / Sắp hết", count: counts.LOW_OOS },
  ];

  const statusPillCls = (status: string) => {
    const meta = getProductStatusMeta(status);
    if (meta.tone === "pos") return `${common.pill} ${common.pillPos}`;
    if (meta.tone === "warn") return `${common.pill} ${common.pillWarn}`;
    if (meta.tone === "neg") return `${common.pill} ${common.pillNeg}`;
    return common.pill;
  };

  const toggleExpand = (productId: string) => {
    setExpandedProductIds((prev) => ({ ...prev, [productId]: !prev[productId] }));
  };

  const handleUpdateSkuStock = async (sku: GatewaySKU, targetStock: number) => {
    const newStock = Math.trunc(targetStock);
    if (!Number.isFinite(newStock) || newStock < 0) {
      showToast("Số lượng tồn kho phải là số nguyên không âm.");
      return;
    }
    if (newStock < sku.reserved_stock) {
      showToast(
        `Tồn kho không thể nhỏ hơn số lượng đang giữ cho đơn hàng (${formatInt(sku.reserved_stock)}).`
      );
      return;
    }
    setSavingSkuId(sku.id);
    setConflictNotice(null);
    try {
      const res = await api<{ sku: GatewaySKU }>(`seller/skus/${sku.id}/stock`, {
        method: "PATCH",
        json: {
          new_stock: newStock,
          expected_version: sku.version,
        },
      });
      if (res.sku) {
        setProducts((prev) =>
          prev.map((p) => ({
            ...p,
            skus: (p.skus ?? []).map((item) => (item.id === sku.id ? res.sku : item)),
          }))
        );
        setStockDrafts((prev) => ({ ...prev, [sku.id]: String(res.sku.stock) }));
      } else {
        await loadData();
      }
      showToast(`Đã cập nhật tồn kho ${sku.sku_code} thành ${formatInt(newStock)} chiếc.`);
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.code === "ABORTED")) {
        const msg =
          "Dữ liệu vừa thay đổi ở phiên khác, hệ thống đã tải lại tồn kho mới nhất. Vui lòng kiểm tra và lưu lại.";
        setConflictNotice(msg);
        showToast("Dữ liệu vừa thay đổi, đang tải lại tồn kho mới nhất.");
        await loadData();
      } else {
        showToast(getErrorMessage(err, "Không thể cập nhật tồn kho SKU lúc này."));
      }
    } finally {
      setSavingSkuId(null);
    }
  };

  const handleDeleteProduct = async (product: GatewayProduct) => {
    setDeletingId(product.id);
    try {
      await api<{ ok: boolean }>(`seller/products/${product.id}`, {
        method: "DELETE",
      });
      setProducts((prev) => prev.filter((p) => p.id !== product.id));
      setConfirmDeleteId(null);
      showToast(`Đã xóa sản phẩm "${product.name}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xóa sản phẩm lúc này."));
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Sản phẩm gian hàng</h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(products.length)} sản phẩm, {formatInt(totalSkuCount)} SKU đang
            hoạt động · {formatInt(counts.PENDING)} chờ duyệt · {formatInt(lowStockRows.length)} SKU
            tồn thấp
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadData()}>
            Làm mới
          </Button>
          <Button variant="ink" size="sm" shape="app" href="/seller/san-pham/moi">
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

      {(uiState === "loading" || (!uiState && loading)) && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải danh mục sản phẩm và tồn kho SKU..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && !loading && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách sản phẩm"
          message={errorMsg ?? "Dịch vụ sản phẩm chưa phản hồi."}
          actionLabel="Thử lại"
          onAction={() => void loadData()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa có sản phẩm nào"
          message="Tạo sản phẩm đầu tiên cùng bảng biến thể màu sắc và kích cỡ để bắt đầu mở bán."
          actionLabel="Thêm sản phẩm"
          actionHref="/seller/san-pham/moi"
        />
      )}

      {!uiState && !loading && !errorMsg && (
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
                  onClick={() => setFilter(c.key)}
                >
                  <span className={common.countCardLabel}>{c.label}</span>
                  <span className={common.countCardValue}>{formatInt(c.count)}</span>
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
                  onClick={() => setFilter(c.key)}
                >
                  <span>{c.label}</span>
                  <span className={common.chipCount}>{formatInt(c.count)}</span>
                </button>
              );
            })}
          </div>

          <section className={common.card} aria-label="Danh sách sản phẩm theo biến thể">
            <div className={common.toolbar} style={{ marginBottom: 14 }}>
              <div className={common.toolbarLeft}>
                <div className={common.searchWrap}>
                  <span className={common.searchIcon}>
                    <IconSearch size={14} />
                  </span>
                  <input
                    type="search"
                    className={common.searchInput}
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Tìm tên sản phẩm hoặc mã SKU..."
                    aria-label="Tìm sản phẩm hoặc mã SKU"
                  />
                </div>
                <select
                  className={common.filterSelect}
                  value={categoryFilter}
                  onChange={(e) => setCategoryFilter(e.target.value)}
                  aria-label="Lọc theo danh mục"
                >
                  <option value="all">Mọi danh mục</option>
                  {categories.map((cat) => (
                    <option key={cat.id} value={cat.id}>
                      {cat.name}
                    </option>
                  ))}
                </select>
              </div>
              <span className={common.cardMeta}>
                Hiển thị {formatInt(filteredProducts.length)} trên {formatInt(products.length)} sản
                phẩm
              </span>
            </div>

            {filteredProducts.length === 0 ? (
              <div className={common.inlineEmpty}>
                {products.length === 0
                  ? "Gian hàng chưa đăng sản phẩm nào. Nhấn Thêm sản phẩm để tạo sản phẩm đầu tiên."
                  : "Không có sản phẩm nào khớp bộ lọc hiện tại."}
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
                      {filteredProducts.map((p) => {
                        const allSkus = p.skus ?? [];
                        const availStock = allSkus.reduce(
                          (sum, s) => sum + Math.max(0, s.available_stock),
                          0
                        );
                        const totalStock = allSkus.reduce(
                          (sum, s) => sum + Math.max(0, s.stock),
                          0
                        );
                        const stMeta = getProductStatusMeta(p.status);
                        const isExpanded = Boolean(expandedProductIds[p.id]);
                        const catName = categoryNameById.get(p.category_id) ?? "Chưa phân loại";
                        const cover = getProductCoverImage(p);

                        return (
                          <React.Fragment key={p.id}>
                            <tr>
                              <td>
                                <div className={styles.prodCell}>
                                  <img
                                    src={cover}
                                    alt={p.name}
                                    className={styles.prodThumb}
                                    width={40}
                                    height={53}
                                  />
                                  <div className={styles.prodText}>
                                    <Link
                                      href={`/seller/san-pham/${p.id}`}
                                      className={styles.prodName}
                                    >
                                      {p.name}
                                    </Link>
                                    <span className={styles.prodSub}>
                                      {formatInt(allSkus.length)} biến thể SKU
                                      {p.slug ? ` · /${p.slug}` : ""}
                                    </span>
                                    {p.status === "REJECTED" && p.rejection_reason && (
                                      <span className={styles.rejectionNote}>
                                        Lý do từ chối: {p.rejection_reason}
                                      </span>
                                    )}
                                  </div>
                                </div>
                              </td>
                              <td>{catName}</td>
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
                                <span className={statusPillCls(p.status)}>{stMeta.label}</span>
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
                                    Chỉnh sửa
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
                                                {formatInt(sku.available_stock)} · Đang giữ{" "}
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
                  {filteredProducts.map((p) => {
                    const allSkus = p.skus ?? [];
                    const availStock = allSkus.reduce(
                      (sum, s) => sum + Math.max(0, s.available_stock),
                      0
                    );
                    const stMeta = getProductStatusMeta(p.status);
                    const isExpanded = Boolean(expandedProductIds[p.id]);
                    const cover = getProductCoverImage(p);
                    return (
                      <div key={p.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div className={styles.mobileProdTop}>
                            <img
                              src={cover}
                              alt={p.name}
                              className={styles.prodThumb}
                              width={40}
                              height={53}
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
                          <span className={statusPillCls(p.status)}>{stMeta.label}</span>
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
          </section>

          {lowStockRows.length > 0 && (
            <section className={common.card} aria-labelledby="restock-heading">
              <div className={common.cardHeader}>
                <h2 id="restock-heading" className={common.cardTitle}>
                  Biến thể cần nhập kho sớm
                </h2>
                <span className={common.cardMeta}>
                  Cập nhật nhanh +10 chiếc trực tiếp vào tồn kho hệ thống
                </span>
              </div>

              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm & biến thể</th>
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
                                src={sku.image_url || getProductCoverImage(product)}
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
                                <span className={styles.prodSub}>{sku.name}</span>
                              </div>
                            </div>
                          </td>
                          <td>{sku.sku_code}</td>
                          <td className={common.numCol}>{formatVnd(sku.price_vnd)}</td>
                          <td className={common.numCol}>
                            <span className={sku.available_stock === 0 ? styles.stockWarn : ""}>
                              {sku.available_stock === 0
                                ? "0 (Hết hàng)"
                                : `${formatInt(sku.available_stock)} chiếc`}
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
                        <div>
                          <Link href={`/seller/san-pham/${product.id}`} className={styles.prodName}>
                            {product.name} · {sku.name}
                          </Link>
                          <span className={styles.prodSub}>{sku.sku_code}</span>
                        </div>
                        <span
                          className={
                            sku.available_stock === 0 ? styles.stockWarn : styles.mobilePrice
                          }
                        >
                          {sku.available_stock === 0
                            ? "0 chiếc"
                            : `Còn ${formatInt(sku.available_stock)}`}
                        </span>
                      </div>
                      <div className={common.mobileRowMeta}>
                        <span>{formatVnd(sku.price_vnd)}</span>
                        <div className={styles.actionRow}>
                          <button
                            type="button"
                            className={styles.restockBtn}
                            disabled={isSaving}
                            onClick={() => void handleUpdateSkuStock(sku, sku.stock + 10)}
                          >
                            {isSaving ? "..." : "+10 tồn"}
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
    </div>
  );
}

function SellerProductsContent() {
  return (
    <SellerShopGate pageTitle="Sản phẩm gian hàng">
      {(shop) => <SellerProductsInner shop={shop} />}
    </SellerShopGate>
  );
}

export default function SellerProductsPage() {
  return (
    <SellerSuspense>
      <SellerProductsContent />
    </SellerSuspense>
  );
}
