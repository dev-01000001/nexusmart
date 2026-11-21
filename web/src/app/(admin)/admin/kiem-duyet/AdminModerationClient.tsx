"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { getProductReviewStatusMeta, type GwAdminProduct } from "@/features/admin/adminApiTypes";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminContext } from "@/features/admin/AdminShell";
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
}: {
  initialProducts: GwAdminProduct[];
  loadError: string | null;
  initialQuery: string;
}) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [products, setProducts] = useState<GwAdminProduct[]>(initialProducts);
  const [productStatusFilter, setProductStatusFilter] = useState<ProductStatusFilter>("all");
  const [query, setQuery] = useState(initialQuery);

  const [selectedProductId, setSelectedProductId] = useState<string | null>(
    initialProducts[0]?.id ?? null
  );
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(null);
  const [noteDrafts, setNoteDrafts] = useState<Record<string, string>>({});
  const [productErrorId, setProductErrorId] = useState<string | null>(null);
  const [busyProductId, setBusyProductId] = useState<string | null>(null);

  const productPending = useMemo(
    () => products.filter((p) => p.status.toUpperCase() === "PENDING").length,
    [products]
  );
  const productApproved = useMemo(
    () => products.filter((p) => p.status.toUpperCase() === "APPROVED").length,
    [products]
  );
  const productRejected = useMemo(
    () => products.filter((p) => p.status.toUpperCase() === "REJECTED").length,
    [products]
  );

  const filteredProducts = useMemo(() => {
    const q = query.trim().toLowerCase();
    return products.filter((item) => {
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
  }, [products, productStatusFilter, query]);

  const activeProduct = useMemo(() => {
    if (filteredProducts.length === 0) return null;
    return filteredProducts.find((p) => p.id === selectedProductId) ?? filteredProducts[0];
  }, [filteredProducts, selectedProductId]);

  const handleProductDecision = async (item: GwAdminProduct, decision: "APPROVE" | "REJECT") => {
    if (busyProductId) return;
    const reason = (noteDrafts[item.id] ?? "").trim();
    if (decision === "REJECT" && !reason) {
      setProductErrorId(item.id);
      return;
    }
    setProductErrorId(null);
    setBusyProductId(item.id);
    try {
      const res = await api<{ product: GwAdminProduct }>(
        `/api/admin/products/${encodeURIComponent(item.id)}/review`,
        {
          method: "POST",
          body: { decision, reason },
        }
      );
      setProducts((prev) => prev.map((p) => (p.id === item.id ? { ...p, ...res.product } : p)));
      setNoteDrafts((prev) => ({ ...prev, [item.id]: "" }));
      showToast(
        decision === "APPROVE"
          ? `Đã duyệt đăng bán sản phẩm "${item.name}".`
          : `Đã từ chối sản phẩm "${item.name}".`
      );
      router.refresh();
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể cập nhật kiểm duyệt sản phẩm lúc này.";
      showToast(msg);
    } finally {
      setBusyProductId(null);
    }
  };

  const renderProductDetail = (item: GwAdminProduct, showHeading = false) => {
    const meta = getProductReviewStatusMeta(item.status);
    const isBusy = busyProductId === item.id;
    const totalStock = item.skus.reduce((sum, sku) => sum + sku.available_stock, 0);
    const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>Kiểm duyệt sản phẩm: {item.name}</h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}

        <div className={styles.previewRow}>
          <img src={thumb} alt={item.name} className={styles.thumbLg} width={96} height={96} />
          <div className={styles.previewMeta}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Mã sản phẩm & gian hàng</span>
                <span className={shared.detailVal}>
                  {item.id.slice(0, 8)} · Shop {item.shop_id.slice(0, 8)}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Khoảng giá bán</span>
                <span className={`${shared.detailVal} num`}>
                  {item.min_price_vnd === item.max_price_vnd
                    ? formatVnd(item.min_price_vnd)
                    : `${formatVnd(item.min_price_vnd)} - ${formatVnd(item.max_price_vnd)}`}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Biến thể & tồn kho khả dụng</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(item.skus.length)} biến thể · {formatInt(totalStock)} sản phẩm
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Ngày tạo</span>
                <span className={shared.detailValPlain}>
                  {item.created_at ? formatDateTimeVi(item.created_at) : "Mới cập nhật"}
                </span>
              </div>
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Mô tả sản phẩm</span>
                <span className={shared.detailValPlain}>
                  {item.description || "Gian hàng chưa nhập mô tả chi tiết."}
                </span>
              </div>
              {item.skus.length > 0 && (
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Danh sách phân loại (SKU)</span>
                  <span className={shared.detailValPlain}>
                    {item.skus
                      .map(
                        (sku) =>
                          `${sku.sku_code} (${sku.name}: ${formatVnd(sku.price_vnd)}, tồn ${formatInt(sku.available_stock)})`
                      )
                      .join(" · ")}
                  </span>
                </div>
              )}
              {item.rejection_reason && (
                <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                  <span className={shared.detailKey}>Lý do từ chối gần nhất</span>
                  <span className={shared.priorityText}>{item.rejection_reason}</span>
                </div>
              )}
            </div>
          </div>
        </div>

        <div className={shared.detailActionBar}>
          <div className={shared.detailNoteGroup}>
            <label htmlFor={`prod-note-${item.id}`} className={shared.fieldLabel}>
              Lý do kiểm duyệt (bắt buộc khi Từ chối)
            </label>
            <input
              id={`prod-note-${item.id}`}
              type="text"
              className={shared.textInput}
              value={noteDrafts[item.id] ?? ""}
              onChange={(e) => {
                setNoteDrafts((prev) => ({ ...prev, [item.id]: e.target.value }));
                if (productErrorId === item.id) setProductErrorId(null);
              }}
              placeholder="Nhập ghi chú duyệt hoặc lý do từ chối gửi tới gian hàng..."
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
              disabled={isBusy || item.status.toUpperCase() === "APPROVED"}
              onClick={() => void handleProductDecision(item, "APPROVE")}
            >
              {isBusy ? "Đang xử lý..." : "Duyệt đăng bán"}
            </Button>
            <Button
              variant="danger"
              size="sm"
              shape="app"
              disabled={isBusy || item.status.toUpperCase() === "REJECTED"}
              onClick={() => void handleProductDecision(item, "REJECT")}
            >
              Từ chối
            </Button>
          </div>
        </div>
      </div>
    );
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Duyệt sản phẩm</h1>
          <p className={shared.pageSubtitle}>
            Có {formatInt(productPending)} sản phẩm chờ duyệt. Sản phẩm được duyệt sẽ bán trên site
            riêng của gian hàng.
          </p>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp hàng đợi duyệt sản phẩm"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chờ duyệt</span>
          <span className={shared.kpiValue}>{formatInt(productPending)}</span>
          <span className={shared.kpiCaption}>
            Trong {formatInt(products.length)} sản phẩm của các gian hàng
          </span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Đã duyệt bán</span>
          <span className={shared.kpiValue}>{formatInt(productApproved)}</span>
          <span className={shared.kpiCaption}>Đang hiển thị trên site gian hàng</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Đã từ chối</span>
          <span className={shared.kpiValue}>{formatInt(productRejected)}</span>
          <span className={shared.kpiCaption}>Gian hàng cần sửa và gửi lại</span>
        </div>
      </section>

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
          <span className={shared.resultCount}>{formatInt(filteredProducts.length)} mục</span>
          <select
            className={shared.toolbarSelect}
            value={productStatusFilter}
            onChange={(e) => setProductStatusFilter(e.target.value as ProductStatusFilter)}
            aria-label="Lọc trạng thái kiểm duyệt sản phẩm"
          >
            <option value="all">Tất cả trạng thái</option>
            <option value="PENDING">Chờ kiểm duyệt</option>
            <option value="APPROVED">Đã duyệt bán</option>
            <option value="REJECTED">Đã từ chối</option>
            <option value="OUT_OF_STOCK">Hết hàng</option>
            <option value="HIDDEN">Đang ẩn</option>
          </select>
        </div>
      </div>

      {loadError ? (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách sản phẩm kiểm duyệt"
          message={loadError}
          actionLabel="Thử tải lại"
          onAction={() => router.refresh()}
        />
      ) : filteredProducts.length === 0 ? (
        <p className={shared.emptyRow}>Không có sản phẩm nào khớp với bộ lọc hiện tại.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Sản phẩm & gian hàng</th>
                  <th className={shared.colNum}>Giá niêm yết</th>
                  <th className={shared.colNum}>Biến thể / Tồn kho</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filteredProducts.map((item) => {
                  const meta = getProductReviewStatusMeta(item.status);
                  const isSelected = activeProduct?.id === item.id;
                  const totalStock = item.skus.reduce((sum, s) => sum + s.available_stock, 0);
                  const thumb = item.images[0] || "/products/ao-thun-cotton-trang.png";
                  return (
                    <tr
                      key={item.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => setSelectedProductId(item.id)}
                    >
                      <td>
                        <div className={styles.mediaThumbCell}>
                          <img
                            src={thumb}
                            alt={item.name}
                            className={styles.thumbSm}
                            width={44}
                            height={44}
                          />
                          <div>
                            <div className={shared.cellPrimary}>{item.name}</div>
                            <div className={shared.cellSub}>
                              ID {item.id.slice(0, 8)} · Shop {item.shop_id.slice(0, 8)}
                            </div>
                          </div>
                        </div>
                      </td>
                      <td className={shared.colNum}>
                        {item.min_price_vnd === item.max_price_vnd
                          ? formatVnd(item.min_price_vnd)
                          : `${formatVnd(item.min_price_vnd)} - ${formatVnd(item.max_price_vnd)}`}
                      </td>
                      <td className={shared.colNum}>
                        <div>{formatInt(item.skus.length)} SKU</div>
                        <div className={shared.cellSub}>Tồn {formatInt(totalStock)}</div>
                      </td>
                      <td>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </td>
                      <td className={shared.colAction}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            setSelectedProductId(item.id);
                          }}
                        >
                          {isSelected ? "Đang xem" : "Kiểm duyệt"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {activeProduct && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Chi tiết kiểm duyệt sản phẩm"
            >
              {renderProductDetail(activeProduct, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filteredProducts.map((item) => {
              const meta = getProductReviewStatusMeta(item.status);
              const isOpen = mobileOpenId === item.id;
              return (
                <div key={item.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => setMobileOpenId((p) => (p === item.id ? null : item.id))}
                    aria-expanded={isOpen}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{item.name}</span>
                      <span className={shared.mobileMetric}>{formatVnd(item.min_price_vnd)}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        Shop {item.shop_id.slice(0, 8)} · {formatInt(item.skus.length)} SKU
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                  </button>
                  {isOpen && (
                    <div className={shared.mobileDetailWrap}>{renderProductDetail(item)}</div>
                  )}
                </div>
              );
            })}
          </div>
        </>
      )}
    </div>
  );
}

function AdminModerationRoute({ initialProducts, loadError }: AdminModerationClientProps) {
  return (
    <AdminStateGate
      title="Duyệt sản phẩm"
      subtitle="Duyệt sản phẩm gian hàng gửi lên trước khi bán trên site riêng của gian hàng."
      emptyTitle="Hàng đợi duyệt sản phẩm đang trống"
      emptyMessage="Tất cả sản phẩm gian hàng gửi lên đã được duyệt."
      errorTitle="Không thể tải hàng đợi duyệt sản phẩm"
      errorMessage="Dịch vụ duyệt sản phẩm tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery }) => (
        <ModerationContent
          initialProducts={initialProducts}
          loadError={loadError}
          initialQuery={initialQuery}
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
