"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconArrowUpRight, IconPlus, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatInt, formatVnd } from "@/lib/format";
import { useSellerContext } from "./SellerShell";
import { SellerDrawer, statusPillClass } from "./SellerUiPrimitives";
import {
  getSellerProductStatusMeta,
  type GwCategory,
  type GwSellerProduct,
  type GwSellerSku,
} from "./sellerApiTypes";
import { parseUiState } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/san-pham/page.module.css";

type ProductFilterKey = "all" | "active" | "low" | "oos";

function totalAvailable(p: GwSellerProduct): number {
  return (p.skus ?? [])
    .filter((s) => s.is_active)
    .reduce((sum, s) => sum + Math.max(0, s.available_stock), 0);
}

function hasLowSku(p: GwSellerProduct): boolean {
  return (p.skus ?? []).some((s) => s.is_active && s.available_stock > 0 && s.available_stock <= 5);
}

function hasOutSku(p: GwSellerProduct): boolean {
  return (p.skus ?? []).some((s) => s.is_active && s.available_stock <= 0);
}

export interface SellerProductsClientProps {
  initialProducts: GwSellerProduct[];
  categories: GwCategory[];
  loadError: string | null;
}

export function SellerProductsClient({
  initialProducts,
  categories,
  loadError,
}: SellerProductsClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialFilter = (searchParams.get("filter") as ProductFilterKey | null) ?? "all";
  const { showToast } = useSellerContext();

  const [products, setProducts] = useState<GwSellerProduct[]>(initialProducts);
  const [busySkuId, setBusySkuId] = useState<string | null>(null);
  const [drawerProductId, setDrawerProductId] = useState<string | null>(null);

  const [filter, setFilter] = useState<ProductFilterKey>(
    ["all", "active", "low", "oos"].includes(initialFilter) ? initialFilter : "all"
  );
  const [query, setQuery] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("all");

  const categoryNameById = useMemo(
    () => new Map(categories.map((c) => [c.id, c.name])),
    [categories]
  );

  const counts = useMemo(
    () => ({
      all: products.length,
      active: products.filter((p) => p.status.toUpperCase() === "APPROVED" && totalAvailable(p) > 0)
        .length,
      low: products.filter((p) => hasLowSku(p)).length,
      oos: products.filter((p) => hasOutSku(p)).length,
    }),
    [products]
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
    return rows.sort((a, b) => a.sku.available_stock - b.sku.available_stock);
  }, [products]);

  const filteredProducts = useMemo(() => {
    const q = query.trim().toLowerCase();
    return products.filter((p) => {
      if (
        filter === "active" &&
        (p.status.toUpperCase() !== "APPROVED" || totalAvailable(p) <= 0)
      ) {
        return false;
      }
      if (filter === "low" && !hasLowSku(p)) return false;
      if (filter === "oos" && !hasOutSku(p)) return false;
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

  const drawerProduct = useMemo(
    () => (drawerProductId ? (products.find((p) => p.id === drawerProductId) ?? null) : null),
    [products, drawerProductId]
  );

  const handleQuickRestockSku = async (product: GwSellerProduct, sku: GwSellerSku) => {
    if (busySkuId) return;
    setBusySkuId(sku.id);
    try {
      const nextStock = sku.stock + 20;
      const res = await api<{ sku: GwSellerSku }>(
        `/api/seller/skus/${encodeURIComponent(sku.id)}/stock`,
        {
          method: "PATCH",
          body: {
            new_stock: nextStock,
            expected_version: sku.version,
          },
        }
      );
      const updated = res.sku;
      setProducts((prev) =>
        prev.map((p) =>
          p.id === product.id
            ? {
                ...p,
                status:
                  p.status === "OUT_OF_STOCK" && updated.available_stock > 0
                    ? "APPROVED"
                    : p.status,
                skus: p.skus.map((s) => (s.id === updated.id ? updated : s)),
              }
            : p
        )
      );
      showToast(
        `Đã nhập +20 cho ${updated.sku_code} (khả dụng: ${formatInt(updated.available_stock)}).`
      );
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : "Không thể cập nhật tồn kho SKU.");
    } finally {
      setBusySkuId(null);
    }
  };

  const handleQuickRestockProduct = (product: GwSellerProduct) => {
    const activeSkus = (product.skus ?? []).filter((s) => s.is_active);
    const lowest = [...activeSkus].sort((a, b) => a.available_stock - b.available_stock)[0];
    if (!lowest) return;
    void handleQuickRestockSku(product, lowest);
  };

  const countCards: Array<{ key: ProductFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "active", label: "Đang bán", count: counts.active },
    { key: "low", label: "Sắp hết hàng", count: counts.low },
    { key: "oos", label: "Hết biến thể", count: counts.oos },
  ];

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <h1 className={common.title}>Sản phẩm</h1>
        <div className={common.headerActions}>
          <Button variant="primary" size="sm" shape="app" href="/seller/san-pham/moi">
            <IconPlus size={14} />
            <span>Thêm sản phẩm</span>
          </Button>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải sản phẩm..."
          rows={6}
        />
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải sản phẩm"
          message={loadError ?? "Dịch vụ kho hàng chưa phản hồi."}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      )}

      {(uiState === "empty" || (!uiState && !loadError && products.length === 0)) && (
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa có sản phẩm nào."
          actionLabel="Thêm sản phẩm"
          actionHref="/seller/san-pham/moi"
        />
      )}

      {!uiState && !loadError && products.length > 0 && (
        <>
          <div className={common.countCards4} role="tablist" aria-label="Lọc trạng thái kho">
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

          <div className={common.chipScroller} role="tablist" aria-label="Lọc trạng thái kho">
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
              <span className={common.toolbarMeta}>
                {formatInt(filteredProducts.length)} / {formatInt(products.length)} sản phẩm
              </span>
            </div>

            {filteredProducts.length === 0 ? (
              <div className={common.inlineEmpty}>Không có sản phẩm khớp bộ lọc.</div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Sản phẩm</th>
                        <th>Danh mục</th>
                        <th className={common.numCol}>Giá bán</th>
                        <th className={common.numCol}>Tồn khả dụng</th>
                        <th className={common.numCol}>Đang giữ</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredProducts.map((p) => {
                        const activeSkus = (p.skus ?? []).filter((s) => s.is_active);
                        const avail = activeSkus.reduce(
                          (sum, s) => sum + Math.max(0, s.available_stock),
                          0
                        );
                        const reserved = activeSkus.reduce(
                          (sum, s) => sum + Math.max(0, s.reserved_stock),
                          0
                        );
                        const oosSkus = activeSkus.filter((s) => s.available_stock <= 0).length;
                        const lowSkus = activeSkus.filter(
                          (s) => s.available_stock > 0 && s.available_stock <= 5
                        ).length;
                        const st = getSellerProductStatusMeta(p.status);
                        const thumb = p.images?.[0] || "/products/ao-thun-cotton-trang.png";
                        const lowestSku = [...activeSkus].sort(
                          (a, b) => a.available_stock - b.available_stock
                        )[0];

                        return (
                          <tr key={p.id}>
                            <td>
                              <div className={styles.prodCell}>
                                <img
                                  src={thumb}
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
                                    {formatInt(activeSkus.length)} SKU
                                    {p.rejection_reason ? `, ${p.rejection_reason}` : ""}
                                  </span>
                                </div>
                              </div>
                            </td>
                            <td>
                              <span
                                className={styles.catCell}
                                title={categoryNameById.get(p.category_id) ?? "Chung"}
                              >
                                {categoryNameById.get(p.category_id) ?? "Chung"}
                              </span>
                            </td>
                            <td className={common.numCol}>
                              <strong>{formatVnd(p.min_price_vnd)}</strong>
                              {p.max_price_vnd > p.min_price_vnd && (
                                <span className={common.cellSub}>
                                  đến {formatVnd(p.max_price_vnd)}
                                </span>
                              )}
                            </td>
                            <td className={common.numCol}>
                              <span className={avail === 0 ? styles.stockWarn : ""}>
                                {formatInt(avail)}
                              </span>
                              {(oosSkus > 0 || lowSkus > 0) && (
                                <span className={common.cellSub}>
                                  {oosSkus > 0 ? `${oosSkus} hết` : `${lowSkus} sắp hết`}
                                </span>
                              )}
                            </td>
                            <td className={common.numCol}>{formatInt(reserved)}</td>
                            <td>
                              <span className={statusPillClass(st.tone)}>{st.label}</span>
                            </td>
                            <td className={common.numCol}>
                              <div className={common.rowHoverActions}>
                                {lowestSku && (oosSkus > 0 || lowSkus > 0) && (
                                  <button
                                    type="button"
                                    className={styles.restockBtn}
                                    disabled={busySkuId === lowestSku.id}
                                    onClick={() => handleQuickRestockProduct(p)}
                                  >
                                    {busySkuId === lowestSku.id ? "Đang lưu..." : "+20 tồn"}
                                  </button>
                                )}
                                <Link href={`/seller/san-pham/${p.id}`} className={styles.editLink}>
                                  Sửa SKU
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
                  {filteredProducts.map((p) => {
                    const activeSkus = (p.skus ?? []).filter((s) => s.is_active);
                    const avail = activeSkus.reduce(
                      (sum, s) => sum + Math.max(0, s.available_stock),
                      0
                    );
                    const st = getSellerProductStatusMeta(p.status);
                    const thumb = p.images?.[0] || "/products/ao-thun-cotton-trang.png";
                    const lowestSku = [...activeSkus].sort(
                      (a, b) => a.available_stock - b.available_stock
                    )[0];
                    return (
                      <div key={p.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div className={styles.mobileProdTop}>
                            <img
                              src={thumb}
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
                                Tồn {formatInt(avail)}, {formatInt(activeSkus.length)} SKU
                              </span>
                            </div>
                          </div>
                          <span className={styles.mobilePrice}>{formatVnd(p.min_price_vnd)}</span>
                        </div>
                        <div className={common.mobileRowMeta}>
                          <span className={statusPillClass(st.tone)}>{st.label}</span>
                          <div className={styles.actionRow}>
                            {lowestSku && (hasOutSku(p) || hasLowSku(p)) && (
                              <button
                                type="button"
                                className={styles.restockBtn}
                                disabled={busySkuId === lowestSku.id}
                                onClick={() => handleQuickRestockProduct(p)}
                              >
                                {busySkuId === lowestSku.id ? "Đang lưu..." : "+20 tồn"}
                              </button>
                            )}
                            <Link href={`/seller/san-pham/${p.id}`} className={styles.editLink}>
                              Sửa SKU
                            </Link>
                          </div>
                        </div>
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
                  SKU cần nhập kho ({formatInt(lowStockRows.length)})
                </h2>
              </div>

              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm &amp; biến thể</th>
                      <th>Mã SKU</th>
                      <th className={common.numCol}>Đơn giá</th>
                      <th className={common.numCol}>Tồn khả dụng</th>
                      <th className={common.numCol}>Đang giữ</th>
                      <th className={common.numCol}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {lowStockRows.slice(0, 8).map(({ product, sku }) => (
                      <tr key={sku.id}>
                        <td>
                          <div className={styles.prodCell}>
                            <img
                              src={
                                sku.image_url ||
                                product.images?.[0] ||
                                "/products/ao-thun-cotton-trang.png"
                              }
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
                        <td className={common.numCol}>{formatInt(sku.reserved_stock)}</td>
                        <td className={common.numCol}>
                          <div className={common.rowHoverActions}>
                            <button
                              type="button"
                              className={styles.restockBtn}
                              disabled={busySkuId === sku.id}
                              onClick={() => void handleQuickRestockSku(product, sku)}
                            >
                              {busySkuId === sku.id ? "Đang lưu..." : "+20 tồn"}
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
                    ))}
                  </tbody>
                </table>
              </div>

              <div className={common.mobileList}>
                {lowStockRows.slice(0, 8).map(({ product, sku }) => (
                  <div key={sku.id} className={common.mobileRow}>
                    <div className={common.mobileRowTop}>
                      <div className={styles.mobileSkuInfo}>
                        <Link href={`/seller/san-pham/${product.id}`} className={styles.prodName}>
                          {product.name} ({sku.name || sku.sku_code})
                        </Link>
                        <span className={styles.prodSub}>{sku.sku_code}</span>
                      </div>
                      <span
                        className={sku.available_stock <= 0 ? styles.stockWarn : styles.mobilePrice}
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
                          disabled={busySkuId === sku.id}
                          onClick={() => void handleQuickRestockSku(product, sku)}
                        >
                          {busySkuId === sku.id ? "Đang lưu..." : "+20 tồn"}
                        </button>
                        <Link href={`/seller/san-pham/${product.id}`} className={styles.editLink}>
                          Sửa
                        </Link>
                      </div>
                    </div>
                  </div>
                ))}
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
            ? `${categoryNameById.get(drawerProduct.category_id) ?? "Chung"}, ${formatInt(
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
                  </div>
                </div>
              ))}
          </div>
        )}
      </SellerDrawer>
    </div>
  );
}
