"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayCategory,
  GatewayProduct,
  GatewayShop,
  PaginatedResponse,
  ProductStatus,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getAdminProductStatusMeta } from "./types";
import { StatusPill } from "./StatusPill";
import shared from "./adminShared.module.css";

interface Props {
  initialQuery?: string;
  onPendingCountChange?: (pendingCount: number) => void;
}

type ProductStatusFilter = "PENDING" | "all" | ProductStatus;

function getCoverImg(p: GatewayProduct): string {
  return (
    p.images?.[0] ||
    p.skus?.find((s) => Boolean(s.image_url))?.image_url ||
    "/products/ao-thun-cotton-trang.png"
  );
}

export function AdminProductModerationTab({ initialQuery = "", onPendingCountChange }: Props) {
  const { showToast } = useMockStore();

  const [products, setProducts] = useState<GatewayProduct[]>([]);
  const [shops, setShops] = useState<GatewayShop[]>([]);
  const [categories, setCategories] = useState<GatewayCategory[]>([]);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [statusFilter, setStatusFilter] = useState<ProductStatusFilter>("PENDING");
  const [query, setQuery] = useState(initialQuery);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [detailProduct, setDetailProduct] = useState<GatewayProduct | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const [reasonDrafts, setReasonDrafts] = useState<Record<string, string>>({});
  const [reasonErrorId, setReasonErrorId] = useState<string | null>(null);
  const [submittingId, setSubmittingId] = useState<string | null>(null);

  const loadProducts = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const [prodRes, shopsRes, catsRes] = await Promise.all([
        api<PaginatedResponse<GatewayProduct>>("admin/products?page=1&page_size=100"),
        api<PaginatedResponse<GatewayShop>>("admin/shops?page=1&page_size=100").catch(() => ({
          items: [],
          page: 1,
          page_size: 100,
          total: 0,
        })),
        api<{ categories: GatewayCategory[] }>("admin/categories").catch(() => ({
          categories: [],
        })),
      ]);
      const items = prodRes.items ?? [];
      setProducts(items);
      setShops(shopsRes.items ?? []);
      setCategories(catsRes.categories ?? []);
      const pendingCount = items.filter((p) => p.status === "PENDING").length;
      onPendingCountChange?.(pendingCount);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách sản phẩm kiểm duyệt lúc này."));
    } finally {
      setLoading(false);
    }
  }, [onPendingCountChange]);

  useEffect(() => {
    void loadProducts();
  }, [loadProducts]);

  const shopNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const s of shops) map.set(s.id, s.name);
    return map;
  }, [shops]);

  const categoryNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) map.set(c.id, c.name);
    return map;
  }, [categories]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return products.filter((p) => {
      if (statusFilter !== "all" && p.status !== statusFilter) return false;
      if (!q) return true;
      const shopName = (shopNameById.get(p.shop_id) ?? "").toLowerCase();
      return (
        p.name.toLowerCase().includes(q) ||
        p.id.toLowerCase().includes(q) ||
        shopName.includes(q) ||
        (p.skus ?? []).some((s) => s.sku_code.toLowerCase().includes(q))
      );
    });
  }, [products, statusFilter, query, shopNameById]);

  const activeProductSummary = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((p) => p.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  useEffect(() => {
    const targetId = activeProductSummary?.id;
    if (!targetId) {
      setDetailProduct(null);
      return;
    }
    let cancelled = false;
    setDetailLoading(true);
    api<{ product: GatewayProduct }>(`admin/products/${targetId}`)
      .then((res) => {
        if (!cancelled) {
          setDetailProduct(res.product ?? activeProductSummary);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setDetailProduct(activeProductSummary);
        }
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [activeProductSummary]);

  const handleReview = async (product: GatewayProduct, decision: "APPROVE" | "REJECT") => {
    const reason = (reasonDrafts[product.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setReasonErrorId(product.id);
      return;
    }
    setReasonErrorId(null);
    setSubmittingId(product.id);
    try {
      const res = await api<{ product: GatewayProduct }>(`admin/products/${product.id}/review`, {
        method: "POST",
        json: {
          decision,
          reason:
            reason ||
            (decision === "APPROVE" ? "Sản phẩm đạt chuẩn danh mục, phê duyệt mở bán." : ""),
        },
      });
      const updated = res.product;
      setProducts((prev) => {
        const next = prev.map((item) => (item.id === product.id && updated ? updated : item));
        onPendingCountChange?.(next.filter((x) => x.status === "PENDING").length);
        return next;
      });
      if (updated) {
        setDetailProduct(updated);
      }
      setReasonDrafts((prev) => ({ ...prev, [product.id]: "" }));
      showToast(
        decision === "APPROVE"
          ? `Đã phê duyệt sản phẩm "${product.name}" hiển thị công khai trên sàn.`
          : `Đã từ chối sản phẩm "${product.name}".`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể gửi quyết định kiểm duyệt sản phẩm."));
    } finally {
      setSubmittingId(null);
    }
  };

  if (loading) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="loading"
          surface="app"
          title="Đang tải hàng đợi sản phẩm kiểm duyệt..."
          rows={5}
        />
      </div>
    );
  }

  if (errorMsg) {
    return (
      <div className={shared.stateWrap}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách sản phẩm kiểm duyệt"
          message={errorMsg}
          actionLabel="Thử lại"
          onAction={() => void loadProducts()}
        />
      </div>
    );
  }

  const inspected = detailProduct ?? activeProductSummary;

  return (
    <>
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
              placeholder="Tìm tên sản phẩm, mã SKU, gian hàng..."
              aria-label="Tìm sản phẩm kiểm duyệt"
            />
          </div>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadProducts()}>
            Làm mới
          </Button>
        </div>

        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} sản phẩm</span>
          <select
            className={shared.toolbarSelect}
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as ProductStatusFilter)}
            aria-label="Lọc trạng thái sản phẩm"
          >
            <option value="PENDING">Chờ duyệt</option>
            <option value="all">Tất cả trạng thái</option>
            <option value="APPROVED">Đã duyệt</option>
            <option value="REJECTED">Đã từ chối</option>
            <option value="HIDDEN">Đang ẩn</option>
            <option value="OUT_OF_STOCK">Hết hàng</option>
          </select>
        </div>
      </div>

      {filtered.length === 0 ? (
        <p className={shared.emptyRow}>
          {statusFilter === "PENDING"
            ? "Hiện không có sản phẩm nào chờ duyệt."
            : "Không tìm thấy sản phẩm khớp bộ lọc hiện tại."}
        </p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Sản phẩm</th>
                  <th>Gian hàng & danh mục</th>
                  <th className={shared.colNum}>Giá bán</th>
                  <th className={shared.colNum}>Biến thể</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((prod) => {
                  const meta = getAdminProductStatusMeta(prod.status);
                  const isSelected = activeProductSummary?.id === prod.id;
                  const shopName = shopNameById.get(prod.shop_id) ?? prod.shop_id;
                  const catName = categoryNameById.get(prod.category_id) ?? prod.category_id;
                  return (
                    <tr
                      key={prod.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => {
                        setReasonErrorId(null);
                        setSelectedId(prod.id);
                      }}
                    >
                      <td>
                        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                          <img
                            src={getCoverImg(prod)}
                            alt={prod.name}
                            width={40}
                            height={40}
                            style={{
                              width: 40,
                              height: 40,
                              borderRadius: 6,
                              objectFit: "cover",
                              flexShrink: 0,
                            }}
                          />
                          <div>
                            <div className={shared.cellPrimary}>{prod.name}</div>
                            <div className={shared.cellSub}>
                              {prod.created_at ? formatDateTimeVi(prod.created_at) : prod.id}
                            </div>
                            {Array.isArray(prod.flagged_keywords) &&
                              prod.flagged_keywords.length > 0 && (
                                <div className={shared.priorityText}>
                                  Từ khoá gắn cờ: {prod.flagged_keywords.join(", ")}
                                </div>
                              )}
                          </div>
                        </div>
                      </td>
                      <td>
                        <div>{shopName}</div>
                        <div className={shared.cellSub}>{catName}</div>
                      </td>
                      <td className={shared.colNum}>{formatVnd(prod.min_price_vnd)}</td>
                      <td className={shared.colNum}>{formatInt(prod.skus?.length ?? 0)} SKU</td>
                      <td>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </td>
                      <td className={shared.colAction}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setReasonErrorId(null);
                            setSelectedId(prod.id);
                          }}
                        >
                          {isSelected ? "Đang xem" : "Xem & duyệt"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className={shared.mobileList}>
            {filtered.map((prod) => {
              const meta = getAdminProductStatusMeta(prod.status);
              const isSelected = activeProductSummary?.id === prod.id;
              const shopName = shopNameById.get(prod.shop_id) ?? prod.shop_id;
              const catName = categoryNameById.get(prod.category_id) ?? prod.category_id;
              return (
                <article key={prod.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{prod.name}</span>
                    <StatusPill label={meta.label} tone={meta.tone} />
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {shopName} · {catName}
                    </span>
                    <span className={shared.mobileMetric}>{formatVnd(prod.min_price_vnd)}</span>
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {formatInt(prod.skus?.length ?? 0)} SKU
                      {Array.isArray(prod.flagged_keywords) && prod.flagged_keywords.length > 0
                        ? ` · Gắn cờ: ${prod.flagged_keywords.join(", ")}`
                        : ""}
                    </span>
                    <button
                      type="button"
                      className={shared.rowActionBtn}
                      onClick={() => {
                        setReasonErrorId(null);
                        setSelectedId(prod.id);
                      }}
                    >
                      {isSelected ? "Đang xem" : "Xem & duyệt"}
                    </button>
                  </div>
                </article>
              );
            })}
          </div>

          {inspected && (
            <section
              className={shared.desktopDetailSection}
              style={{ display: "block" }}
              aria-label="Chi tiết kiểm duyệt sản phẩm"
            >
              <div className={shared.detailWell}>
                <div className={shared.detailHeader}>
                  <h2 className={shared.detailHeading}>
                    Chi tiết sản phẩm: {inspected.name}
                    {detailLoading ? " (đang tải...)" : ""}
                  </h2>
                  <div className={shared.detailHeaderMeta}>
                    <StatusPill
                      label={getAdminProductStatusMeta(inspected.status).label}
                      tone={getAdminProductStatusMeta(inspected.status).tone}
                    />
                  </div>
                </div>

                <div className={shared.detailGrid}>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Gian hàng</span>
                    <span className={shared.detailVal}>
                      {shopNameById.get(inspected.shop_id) ?? inspected.shop_id}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Danh mục</span>
                    <span className={shared.detailVal}>
                      {categoryNameById.get(inspected.category_id) ?? inspected.category_id}
                    </span>
                  </div>
                  <div className={shared.detailField}>
                    <span className={shared.detailKey}>Khoảng giá bán</span>
                    <span className={shared.detailVal}>
                      {inspected.min_price_vnd === inspected.max_price_vnd
                        ? formatVnd(inspected.min_price_vnd)
                        : `${formatVnd(inspected.min_price_vnd)} - ${formatVnd(inspected.max_price_vnd)}`}
                    </span>
                  </div>
                  <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                    <span className={shared.detailKey}>Mô tả sản phẩm</span>
                    <span className={shared.detailValPlain}>
                      {inspected.description || "Không có mô tả."}
                    </span>
                  </div>
                  {Array.isArray(inspected.flagged_keywords) &&
                    inspected.flagged_keywords.length > 0 && (
                      <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                        <span className={shared.detailKey}>Từ khoá bị gắn cờ cần rà soát</span>
                        <span className={shared.priorityText}>
                          {inspected.flagged_keywords.join(", ")}
                        </span>
                      </div>
                    )}
                  {inspected.rejection_reason && (
                    <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                      <span className={shared.detailKey}>Lý do từ chối gần nhất</span>
                      <span className={shared.detailValPlain}>{inspected.rejection_reason}</span>
                    </div>
                  )}
                </div>

                {(inspected.skus ?? []).length > 0 && (
                  <>
                    <div className={shared.tableWrap}>
                      <table className={shared.dataTable}>
                        <thead>
                          <tr>
                            <th>Mã SKU</th>
                            <th>Biến thể</th>
                            <th className={shared.colNum}>Giá bán</th>
                            <th className={shared.colNum}>Tồn kho</th>
                            <th className={shared.colNum}>Khối lượng</th>
                          </tr>
                        </thead>
                        <tbody>
                          {inspected.skus.map((sku) => (
                            <tr key={sku.id}>
                              <td>{sku.sku_code}</td>
                              <td>{sku.name}</td>
                              <td className={shared.colNum}>{formatVnd(sku.price_vnd)}</td>
                              <td className={shared.colNum}>
                                {formatInt(sku.available_stock)} / {formatInt(sku.stock)}
                              </td>
                              <td className={shared.colNum}>{formatInt(sku.weight_grams)}g</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>

                    <div className={shared.mobileList}>
                      {inspected.skus.map((sku) => (
                        <div key={sku.id} className={shared.mobileItem}>
                          <div className={shared.mobileLine1}>
                            <span className={shared.mobileIdentity}>
                              {sku.sku_code} · {sku.name}
                            </span>
                            <span className={shared.mobileMetric}>{formatVnd(sku.price_vnd)}</span>
                          </div>
                          <div className={shared.mobileLine2}>
                            <span className={shared.mobileMetaText}>
                              Tồn: {formatInt(sku.available_stock)} / {formatInt(sku.stock)}
                            </span>
                            <span className={shared.mobileMetaText}>
                              {formatInt(sku.weight_grams)}g
                            </span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </>
                )}

                <div className={shared.detailActionBar}>
                  <div className={shared.detailNoteGroup}>
                    <label htmlFor={`prod-note-${inspected.id}`} className={shared.fieldLabel}>
                      Ghi chú kiểm duyệt hoặc lý do từ chối
                    </label>
                    <input
                      id={`prod-note-${inspected.id}`}
                      type="text"
                      className={shared.textInput}
                      value={reasonDrafts[inspected.id] ?? ""}
                      onChange={(e) => {
                        setReasonDrafts((prev) => ({ ...prev, [inspected.id]: e.target.value }));
                        if (reasonErrorId === inspected.id) setReasonErrorId(null);
                      }}
                      placeholder="Nhập lý do cụ thể khi từ chối sản phẩm..."
                    />
                    {reasonErrorId === inspected.id && (
                      <span className={shared.validationError}>
                        Vui lòng nhập lý do từ chối để người bán biết cần sửa nội dung nào.
                      </span>
                    )}
                  </div>

                  <div className={shared.detailButtons}>
                    <Button
                      variant="primary"
                      size="sm"
                      shape="app"
                      disabled={submittingId === inspected.id || inspected.status === "APPROVED"}
                      onClick={() => void handleReview(inspected, "APPROVE")}
                    >
                      {submittingId === inspected.id ? "Đang xử lý..." : "Phê duyệt sản phẩm"}
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      shape="app"
                      disabled={submittingId === inspected.id || inspected.status === "REJECTED"}
                      onClick={() => void handleReview(inspected, "REJECT")}
                    >
                      Từ chối
                    </Button>
                  </div>
                </div>
              </div>
            </section>
          )}
        </>
      )}
    </>
  );
}
