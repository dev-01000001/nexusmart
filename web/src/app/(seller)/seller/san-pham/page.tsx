"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { CATEGORIES, SHOPS, getShopById, getTotalStock } from "@/data";
import type { Product } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconPlus, IconSearch, StateView } from "@/components";
import { formatInt, formatVnd } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import {
  getBackInStockRowsWithSession,
  getProductExtendedMetaWithSession,
  getShopProductsWithSession,
  getTotalWaitingSubscribers,
  parseUiState,
  quickRestockSellerSku,
  useSellerSession,
} from "@/features/seller/sellerStore";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

type ProductFilterKey = "all" | "active" | "low" | "oos";

function hasLowSku(p: Product) {
  return p.skus.some((s) => s.stock > 0 && s.stock <= 5);
}

function hasOutSku(p: Product) {
  return p.skus.some((s) => s.stock === 0);
}

function SellerProductsContent() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialFilter = (searchParams.get("filter") as ProductFilterKey | null) ?? "all";

  const { activeSellerShopId, showToast } = useStore();
  const session = useSellerSession();
  const activeShop = getShopById(activeSellerShopId) ?? SHOPS[0];

  const [filter, setFilter] = useState<ProductFilterKey>(
    ["all", "active", "low", "oos"].includes(initialFilter) ? initialFilter : "all"
  );
  const [query, setQuery] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("all");

  const products = useMemo(
    () => getShopProductsWithSession(activeShop.id, session),
    [activeShop.id, session]
  );

  const counts = useMemo(() => {
    return {
      all: products.length,
      active: products.filter((p) => getTotalStock(p) > 0).length,
      low: products.filter((p) => hasLowSku(p)).length,
      oos: products.filter((p) => hasOutSku(p)).length,
    };
  }, [products]);

  const totalSkuCount = useMemo(() => products.reduce((s, p) => s + p.skus.length, 0), [products]);
  const lowSkuTotal = useMemo(
    () => products.reduce((s, p) => s + p.skus.filter((k) => k.stock <= 5).length, 0),
    [products]
  );
  const totalWaitingAll = useMemo(
    () => products.reduce((s, p) => s + getTotalWaitingSubscribers(p, session), 0),
    [products, session]
  );
  const restockQueue = useMemo(
    () => getBackInStockRowsWithSession(activeShop.id, session).slice(0, 6),
    [activeShop.id, session]
  );

  const filteredProducts = useMemo(() => {
    const q = query.trim().toLowerCase();
    return products.filter((p) => {
      if (filter === "active" && getTotalStock(p) <= 0) return false;
      if (filter === "low" && !hasLowSku(p)) return false;
      if (filter === "oos" && !hasOutSku(p)) return false;
      if (categoryFilter !== "all" && p.categorySlug !== categoryFilter) return false;
      if (q) {
        const hitName = p.name.toLowerCase().includes(q) || p.shortName.toLowerCase().includes(q);
        const hitSku = p.skus.some((s) => s.skuCode.toLowerCase().includes(q));
        if (!hitName && !hitSku) return false;
      }
      return true;
    });
  }, [products, filter, categoryFilter, query]);

  const countCards: Array<{ key: ProductFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "active", label: "Đang bán", count: counts.active },
    { key: "low", label: "Sắp hết hàng", count: counts.low },
    { key: "oos", label: "Hết biến thể", count: counts.oos },
  ];

  const getProductStatus = (p: Product) => {
    const total = getTotalStock(p);
    if (total === 0) return { label: "Tạm hết hàng", cls: `${common.pill} ${common.pillNeg}` };
    if (hasOutSku(p)) return { label: "Thiếu biến thể", cls: `${common.pill} ${common.pillWarn}` };
    if (hasLowSku(p)) return { label: "Sắp hết size", cls: `${common.pill} ${common.pillWarn}` };
    return { label: "Đang bán", cls: `${common.pill} ${common.pillPos}` };
  };

  const handleQuickRestockProduct = (product: Product) => {
    const meta = getProductExtendedMetaWithSession(product.id, product.categorySlug, session);
    const targetSku =
      product.skus.find((s) => (meta.backInStockSubscribers[s.skuCode] ?? 0) > 0) ??
      [...product.skus].sort((a, b) => a.stock - b.stock)[0];
    if (!targetSku) return;
    const res = quickRestockSellerSku(product, targetSku.skuCode, 20);
    if (res.notifiedCount > 0) {
      showToast(
        `Đã nhập +20 chiếc cho ${targetSku.skuCode} (tồn mới: ${formatInt(res.newStock)}) và gửi báo có hàng tới ${formatInt(res.notifiedCount)} khách đang chờ.`
      );
    } else {
      showToast(
        `Đã nhập +20 chiếc cho ${targetSku.skuCode} (tồn kho mới: ${formatInt(res.newStock)} chiếc).`
      );
    }
  };

  const handleQuickRestockSkuCode = (productId: string, skuCode: string) => {
    const product = products.find((p) => p.id === productId);
    if (!product) return;
    const res = quickRestockSellerSku(product, skuCode, 20);
    if (res.notifiedCount > 0) {
      showToast(
        `Đã nhập +20 tồn cho ${skuCode} (tồn mới: ${formatInt(res.newStock)}) và gửi thông báo có hàng đến ${formatInt(res.notifiedCount)} khách.`
      );
    } else {
      showToast(`Đã cập nhật nhanh +20 tồn cho ${skuCode} (tồn mới: ${formatInt(res.newStock)}).`);
    }
  };

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Sản phẩm gian hàng</h1>
          <p className={common.finding}>
            {activeShop.name} · {formatInt(products.length)} mẫu niêm yết,{" "}
            {formatInt(totalSkuCount)} SKU · {formatInt(lowSkuTotal)} SKU dưới ngưỡng 5 ·{" "}
            {formatInt(totalWaitingAll)} khách đăng ký báo có hàng
          </p>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/tuong-tac?tab=waiting">
            Xem khách chờ hàng
          </Button>
          <Button variant="ink" size="sm" shape="app" href="/seller/san-pham/moi">
            <IconPlus size={14} />
            <span>Thêm sản phẩm</span>
          </Button>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải danh mục sản phẩm và tồn kho SKU..."
          rows={5}
        />
      )}

      {uiState === "error" && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách sản phẩm"
          message="Dịch vụ kho hàng chưa phản hồi. Dữ liệu tồn kho gần nhất đã được giữ nguyên."
          lastUpdated="02:15 · 20/11/2026"
          actionLabel="Thử lại"
          actionHref="/seller/san-pham"
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

      {!uiState && (
        <>
          {/* Count cards filter (desktop/tablet) */}
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

          {/* Horizontal scrolling chips on phone (<= 640px) */}
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

          {/* Product table card */}
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
                  {CATEGORIES.map((cat) => (
                    <option key={cat.slug} value={cat.slug}>
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
                Không có sản phẩm nào khớp bộ lọc hiện tại. Hãy đổi bộ lọc hoặc tạo sản phẩm mới.
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Sản phẩm</th>
                        <th>Danh mục & BST</th>
                        <th className={common.numCol}>Giá bán</th>
                        <th className={common.numCol}>Tổng tồn</th>
                        <th className={common.numCol}>Yêu thích</th>
                        <th className={common.numCol}>Chờ có hàng</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredProducts.map((p) => {
                        const total = getTotalStock(p);
                        const oosSkus = p.skus.filter((s) => s.stock === 0).length;
                        const lowSkus = p.skus.filter((s) => s.stock > 0 && s.stock <= 5).length;
                        const st = getProductStatus(p);
                        const meta = getProductExtendedMetaWithSession(
                          p.id,
                          p.categorySlug,
                          session
                        );
                        const waitingCount = getTotalWaitingSubscribers(p, session);
                        const flags = [
                          meta.productKind === "combo_multi_size" ? "Combo đa size" : null,
                          meta.isPreOrder ? "Pre-order" : null,
                          meta.nonReturnable ? "Không đổi trả" : null,
                        ].filter(Boolean);

                        return (
                          <tr key={p.id}>
                            <td>
                              <div className={styles.prodCell}>
                                <img
                                  src={p.image}
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
                                    {p.gsm}gsm · {p.colors.length} màu · {formatInt(p.skus.length)}{" "}
                                    SKU{flags.length > 0 ? ` · ${flags.join(", ")}` : ""}
                                  </span>
                                </div>
                              </div>
                            </td>
                            <td>
                              <span>{p.categoryName}</span>
                              <span className={common.cellSub}>{meta.shopCollection}</span>
                            </td>
                            <td className={common.numCol}>
                              <strong>{formatVnd(p.price)}</strong>
                              {p.originalPrice && (
                                <span className={common.cellSub}>
                                  Gốc {formatVnd(p.originalPrice)}
                                </span>
                              )}
                            </td>
                            <td className={common.numCol}>
                              <span className={total === 0 ? styles.stockWarn : ""}>
                                {formatInt(total)}
                              </span>
                              {(oosSkus > 0 || lowSkus > 0) && (
                                <span className={common.cellSub}>
                                  {oosSkus > 0 ? `${oosSkus} hết hàng` : `${lowSkus} sắp hết`}
                                </span>
                              )}
                            </td>
                            <td className={common.numCol}>{formatInt(meta.wishlistCount)}</td>
                            <td className={common.numCol}>
                              <span
                                className={
                                  waitingCount > 0 ? styles.waitHighlight : styles.waitZero
                                }
                              >
                                {formatInt(waitingCount)}
                              </span>
                              {waitingCount > 0 && (
                                <span className={common.cellSub}>chờ báo tồn</span>
                              )}
                            </td>
                            <td>
                              <span className={st.cls}>{st.label}</span>
                            </td>
                            <td className={common.numCol}>
                              <div className={styles.actionRow}>
                                {(waitingCount > 0 || oosSkus > 0 || lowSkus > 0) && (
                                  <button
                                    type="button"
                                    className={styles.restockBtn}
                                    onClick={() => handleQuickRestockProduct(p)}
                                  >
                                    +20 tồn
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

                {/* Distinct 2-line phone rows */}
                <div className={common.mobileList}>
                  {filteredProducts.map((p) => {
                    const total = getTotalStock(p);
                    const st = getProductStatus(p);
                    const meta = getProductExtendedMetaWithSession(p.id, p.categorySlug, session);
                    const waitingCount = getTotalWaitingSubscribers(p, session);
                    return (
                      <div key={p.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div className={styles.mobileProdTop}>
                            <img
                              src={p.image}
                              alt={p.name}
                              className={styles.prodThumb}
                              width={40}
                              height={53}
                            />
                            <div className={styles.prodText}>
                              <Link href={`/seller/san-pham/${p.id}`} className={styles.prodName}>
                                {p.shortName}
                              </Link>
                              <span className={styles.prodSub}>
                                Tồn {formatInt(total)} · Thích {formatInt(meta.wishlistCount)} · Chờ
                                hàng {formatInt(waitingCount)}
                              </span>
                            </div>
                          </div>
                          <span className={styles.mobilePrice}>{formatVnd(p.price)}</span>
                        </div>
                        <div className={common.mobileRowMeta}>
                          <span className={st.cls}>{st.label}</span>
                          <div className={styles.actionRow}>
                            {(waitingCount > 0 || hasOutSku(p) || hasLowSku(p)) && (
                              <button
                                type="button"
                                className={styles.restockBtn}
                                onClick={() => handleQuickRestockProduct(p)}
                              >
                                +20 tồn
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

          {/* Actionable restock & back-in-stock trigger queue */}
          {restockQueue.length > 0 && (
            <section className={common.card} aria-labelledby="restock-heading">
              <div className={common.cardHeader}>
                <h2 id="restock-heading" className={common.cardTitle}>
                  Biến thể cần nhập kho và kích hoạt báo có hàng
                </h2>
                <span className={common.cardMeta}>
                  Nhập tồn &gt; 0 để tự động gửi thông báo có hàng tới khách đã đăng ký
                </span>
              </div>

              <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                <table className={common.dataTable}>
                  <thead>
                    <tr>
                      <th>Sản phẩm & biến thể</th>
                      <th>Mã SKU</th>
                      <th className={common.numCol}>Đơn giá</th>
                      <th className={common.numCol}>Tồn hiện tại</th>
                      <th className={common.numCol}>Khách chờ hàng</th>
                      <th className={common.numCol}>Thao tác</th>
                    </tr>
                  </thead>
                  <tbody>
                    {restockQueue.map((row) => (
                      <tr key={row.skuCode}>
                        <td>
                          <div className={styles.prodCell}>
                            <img
                              src={row.productImage}
                              alt={row.productName}
                              className={styles.skuThumbSm}
                              width={32}
                              height={42}
                            />
                            <div className={styles.prodText}>
                              <Link
                                href={`/seller/san-pham/${row.productId}`}
                                className={styles.prodName}
                              >
                                {row.productName}
                              </Link>
                              <span className={styles.prodSub}>
                                {row.colorName} · Size {row.size}
                              </span>
                            </div>
                          </div>
                        </td>
                        <td>{row.skuCode}</td>
                        <td className={common.numCol}>{formatVnd(row.price)}</td>
                        <td className={common.numCol}>
                          <span className={row.stock === 0 ? styles.stockWarn : ""}>
                            {row.stock === 0 ? "0 (Hết hàng)" : `${formatInt(row.stock)} chiếc`}
                          </span>
                        </td>
                        <td className={common.numCol}>
                          <span
                            className={
                              row.waitingCount > 0 ? styles.waitHighlight : styles.waitZero
                            }
                          >
                            {formatInt(row.waitingCount)} khách
                          </span>
                        </td>
                        <td className={common.numCol}>
                          <div className={styles.actionRow}>
                            <button
                              type="button"
                              className={styles.restockBtn}
                              onClick={() => handleQuickRestockSkuCode(row.productId, row.skuCode)}
                            >
                              {row.waitingCount > 0 ? "Nhập +20 & báo khách" : "Nhập +20 tồn"}
                            </button>
                            <Link
                              href={`/seller/san-pham/${row.productId}`}
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
                {restockQueue.map((row) => (
                  <div key={row.skuCode} className={common.mobileRow}>
                    <div className={common.mobileRowTop}>
                      <div>
                        <Link
                          href={`/seller/san-pham/${row.productId}`}
                          className={styles.prodName}
                        >
                          {row.productName} · {row.colorName} / {row.size}
                        </Link>
                        <span className={styles.prodSub}>
                          {row.skuCode} · {formatInt(row.waitingCount)} khách chờ
                        </span>
                      </div>
                      <span
                        className={row.stock === 0 ? styles.stockWarn : styles.mobilePrice}
                        style={{ fontVariantNumeric: "tabular-nums" }}
                      >
                        {row.stock === 0 ? "0 chiếc" : `Còn ${formatInt(row.stock)}`}
                      </span>
                    </div>
                    <div className={common.mobileRowMeta}>
                      <span>{formatVnd(row.price)}</span>
                      <div className={styles.actionRow}>
                        <button
                          type="button"
                          className={styles.restockBtn}
                          onClick={() => handleQuickRestockSkuCode(row.productId, row.skuCode)}
                        >
                          Nhập +20 & báo
                        </button>
                        <Link
                          href={`/seller/san-pham/${row.productId}`}
                          className={styles.editLink}
                        >
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
    </div>
  );
}

export default function SellerProductsPage() {
  return (
    <SellerSuspense>
      <SellerProductsContent />
    </SellerSuspense>
  );
}
