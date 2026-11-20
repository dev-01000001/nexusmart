"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Button, IconCheck, IconPlus, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatInt, formatVnd } from "@/lib/format";
import { useSellerContext } from "./SellerShell";
import {
  getSellerProductStatusMeta,
  type GwCategory,
  type GwSellerProduct,
  type GwSellerSku,
} from "./sellerApiTypes";
import { EMPTY_DASH } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "./ProductFormView.module.css";

const GALLERY_CHOICES = [
  { label: "Áo thun Trắng", path: "/products/ao-thun-cotton-trang.png" },
  { label: "Áo thun Xám đá", path: "/products/ao-thun-cotton-xam-da.png" },
  { label: "Áo thun Xanh than", path: "/products/ao-thun-cotton-xanh-than.png" },
  { label: "Áo thun Dry-Fit", path: "/products/ao-thun-dryfit-co-vit.png" },
  { label: "Áo Polo Kem", path: "/products/ao-polo-pique-kem.png" },
  { label: "Áo Polo Xanh đá", path: "/products/ao-polo-pique-xanh-da.png" },
  { label: "Quần Short Be", path: "/products/quan-short-daily-be.png" },
  { label: "Quần Short Linen", path: "/products/quan-short-linen-xam-da.png" },
  { label: "Jogger Xám", path: "/products/quan-jogger-terry-xam.png" },
  { label: "Boxer Modal", path: "/products/do-lot-boxer-modal-3.png" },
  { label: "Tất cổ trung", path: "/products/tat-cotton-co-trung-5.png" },
  { label: "Áo khoác Gió", path: "/products/ao-khoac-gio-xam-tro.png" },
];

interface EditableSkuRow {
  localKey: string;
  id?: string;
  version?: number;
  reservedStock: number;
  skuCode: string;
  name: string;
  color: string;
  colorName: string;
  colorHex: string;
  size: string;
  priceVnd: number;
  stock: number;
  weightGrams: number;
  imageUrl: string;
}

function toEditableRows(product?: GwSellerProduct | null): EditableSkuRow[] {
  if (product && product.skus?.length > 0) {
    return product.skus
      .filter((s) => s.is_active)
      .map((s) => ({
        localKey: s.id,
        id: s.id,
        version: s.version,
        reservedStock: s.reserved_stock,
        skuCode: s.sku_code,
        name: s.name || `${s.attributes?.color_name ?? "Mặc định"} / ${s.attributes?.size ?? "L"}`,
        color: s.attributes?.color ?? "mac-dinh",
        colorName: s.attributes?.color_name ?? s.attributes?.color ?? "Tiêu chuẩn",
        colorHex: s.attributes?.color_hex ?? "#9aa5ac",
        size: s.attributes?.size ?? "L",
        priceVnd: s.price_vnd,
        stock: s.stock,
        weightGrams: s.weight_grams || 250,
        imageUrl: s.image_url || product.images?.[0] || GALLERY_CHOICES[0].path,
      }));
  }
  const sizes = ["S", "M", "L", "XL"];
  const stamp = String(Date.now()).slice(-4);
  return sizes.map((sz) => ({
    localKey: `new-trang-${sz}`,
    reservedStock: 0,
    skuCode: `NM-${stamp}-TRANG-${sz}`,
    name: `Trắng tinh / ${sz}`,
    color: "trang",
    colorName: "Trắng tinh",
    colorHex: "#f4f6f7",
    size: sz,
    priceVnd: 189000,
    stock: 20,
    weightGrams: 250,
    imageUrl: GALLERY_CHOICES[0].path,
  }));
}

export interface ProductFormViewProps {
  mode: "create" | "edit";
  initialProduct?: GwSellerProduct | null;
  categories: GwCategory[];
  loadError?: string | null;
}

export function ProductFormView({
  mode,
  initialProduct,
  categories,
  loadError,
}: ProductFormViewProps) {
  const router = useRouter();
  const { shop, showToast } = useSellerContext();

  const [productStatus, setProductStatus] = useState(initialProduct?.status ?? "PENDING");
  const [name, setName] = useState(initialProduct?.name ?? "");
  const [categoryId, setCategoryId] = useState(
    initialProduct?.category_id ?? categories[0]?.id ?? ""
  );
  const [description, setDescription] = useState(
    initialProduct?.description ??
      "Sản phẩm đồ mặc hằng ngày hoàn thiện tại xưởng, kiểm soát độ co rút và giữ phom sau nhiều lần giặt."
  );
  const [selectedImage, setSelectedImage] = useState(
    initialProduct?.images?.[0] ?? GALLERY_CHOICES[0].path
  );
  const [skuRows, setSkuRows] = useState<EditableSkuRow[]>(() => toEditableRows(initialProduct));
  const [bulkStockStr, setBulkStockStr] = useState("20");
  const [newSkuSize, setNewSkuSize] = useState("2XL");
  const [newSkuColorName, setNewSkuColorName] = useState("Xám đá");
  const [saving, setSaving] = useState(false);
  const [busyStockId, setBusyStockId] = useState<string | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [savedNotice, setSavedNotice] = useState<string | null>(null);

  const totalStock = useMemo(
    () => skuRows.reduce((s, r) => s + Math.max(0, Math.trunc(r.stock)), 0),
    [skuRows]
  );
  const lowSkuCount = useMemo(
    () => skuRows.filter((r) => Math.max(0, r.stock - r.reservedStock) <= 5).length,
    [skuRows]
  );
  const priceRangeLabel = useMemo(() => {
    if (skuRows.length === 0) return EMPTY_DASH;
    const minP = Math.min(...skuRows.map((r) => r.priceVnd));
    const maxP = Math.max(...skuRows.map((r) => r.priceVnd));
    return minP === maxP ? formatVnd(minP) : `${formatVnd(minP)} - ${formatVnd(maxP)}`;
  }, [skuRows]);

  if (loadError) {
    return (
      <div className={common.page}>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải thông tin sản phẩm"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  if (mode === "edit" && !initialProduct) {
    return (
      <div className={common.page}>
        <div className={common.pageHeader}>
          <div className={common.headerText}>
            <Link href="/seller/san-pham" className={styles.backLink}>
              Quay lại danh sách sản phẩm
            </Link>
            <h1 className={common.title}>Không tìm thấy sản phẩm</h1>
          </div>
        </div>
        <StateView
          kind="empty"
          surface="app"
          title="Mã sản phẩm không tồn tại trong gian hàng"
          message="Sản phẩm có thể đã bị gỡ hoặc không thuộc gian hàng của bạn."
          actionLabel="Về danh sách sản phẩm"
          actionHref="/seller/san-pham"
        />
      </div>
    );
  }

  const updateRow = (localKey: string, patch: Partial<EditableSkuRow>) => {
    setSkuRows((prev) => prev.map((r) => (r.localKey === localKey ? { ...r, ...patch } : r)));
  };

  const handleAddSkuRow = () => {
    const colorClean = newSkuColorName.trim() || "Mặc định";
    const sizeClean = newSkuSize.trim().toUpperCase() || "L";
    const colorSlug =
      colorClean
        .normalize("NFD")
        .replace(/[\u0300-\u036f]/g, "")
        .replace(/đ/gi, "d")
        .replace(/[^a-zA-Z0-9]+/g, "")
        .toLowerCase()
        .slice(0, 8) || "mau";
    const baseCode = `NM-${String(Date.now()).slice(-4)}-${colorSlug.toUpperCase()}-${sizeClean}`;
    const basePrice = skuRows[0]?.priceVnd ?? 189000;
    setSkuRows((prev) => [
      ...prev,
      {
        localKey: `new-${Date.now()}-${prev.length}`,
        reservedStock: 0,
        skuCode: baseCode,
        name: `${colorClean} / ${sizeClean}`,
        color: colorSlug,
        colorName: colorClean,
        colorHex: "#9aa5ac",
        size: sizeClean,
        priceVnd: basePrice,
        stock: 15,
        weightGrams: 250,
        imageUrl: selectedImage,
      },
    ]);
  };

  const handleRemoveRow = (localKey: string) => {
    if (skuRows.length <= 1) return;
    setSkuRows((prev) => prev.filter((r) => r.localKey !== localKey));
  };

  const handleBulkStock = () => {
    const val = Math.max(0, Math.trunc(Number(bulkStockStr) || 0));
    setSkuRows((prev) => prev.map((r) => ({ ...r, stock: Math.max(r.reservedStock, val) })));
    showToast(`Đã đặt tồn kho ${formatInt(val)} chiếc cho ${skuRows.length} biến thể SKU.`);
  };

  const handleQuickSaveSkuStock = async (row: EditableSkuRow) => {
    if (!row.id || !row.version || busyStockId) return;
    setBusyStockId(row.id);
    setErrorMsg(null);
    try {
      const res = await api<{ sku: GwSellerSku }>(
        `/api/seller/skus/${encodeURIComponent(row.id)}/stock`,
        {
          method: "PATCH",
          body: {
            new_stock: Math.max(0, Math.trunc(row.stock)),
            expected_version: row.version,
          },
        }
      );
      const updated = res.sku;
      updateRow(row.localKey, {
        stock: updated.stock,
        reservedStock: updated.reserved_stock,
        version: updated.version,
      });
      showToast(
        `Đã lưu tồn kho ${updated.sku_code}: ${formatInt(updated.available_stock)} chiếc khả dụng.`
      );
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Không thể cập nhật tồn kho SKU.";
      setErrorMsg(msg);
      showToast(msg);
    } finally {
      setBusyStockId(null);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = name.trim();
    if (!cleanName) {
      setErrorMsg("Vui lòng nhập tên sản phẩm.");
      return;
    }
    if (!categoryId) {
      setErrorMsg("Vui lòng chọn danh mục sản phẩm.");
      return;
    }
    if (skuRows.length === 0) {
      setErrorMsg("Cần ít nhất 1 biến thể SKU để lưu sản phẩm.");
      return;
    }
    for (const r of skuRows) {
      if (!r.skuCode.trim()) {
        setErrorMsg("Mỗi biến thể cần có mã SKU không rỗng.");
        return;
      }
      if (r.priceVnd < 1000) {
        setErrorMsg(`Đơn giá của SKU ${r.skuCode} phải là số nguyên tối thiểu 1.000₫.`);
        return;
      }
      if (r.stock < r.reservedStock) {
        setErrorMsg(
          `Tồn kho của SKU ${r.skuCode} không được nhỏ hơn số lượng đang giữ (${r.reservedStock}).`
        );
        return;
      }
    }

    setErrorMsg(null);
    setSaving(true);
    try {
      const payload = {
        category_id: categoryId,
        name: cleanName,
        description: description.trim() || cleanName,
        images: Array.from(
          new Set([selectedImage, ...skuRows.map((r) => r.imageUrl).filter(Boolean)])
        ),
        status: mode === "edit" ? productStatus : undefined,
        skus: skuRows.map((r) => ({
          id: r.id,
          sku_code: r.skuCode.trim(),
          name: r.name.trim() || `${r.colorName} / ${r.size}`,
          attributes: {
            color: r.color,
            color_name: r.colorName,
            color_hex: r.colorHex,
            size: r.size,
          },
          price_vnd: Math.trunc(r.priceVnd),
          stock: Math.max(0, Math.trunc(r.stock)),
          weight_grams: Math.max(10, Math.trunc(r.weightGrams || 250)),
          image_url: r.imageUrl || selectedImage,
        })),
      };

      if (mode === "create") {
        const res = await api<{ product: GwSellerProduct }>("/api/seller/products", {
          method: "POST",
          body: payload,
        });
        const msg = `Đã tạo sản phẩm "${res.product.name}" (${res.product.skus.length} SKU). Sản phẩm đang chờ quản trị duyệt hiển thị.`;
        showToast(msg);
        setSavedNotice(msg);
        router.push(`/seller/san-pham/${res.product.id}`);
      } else if (initialProduct) {
        const res = await api<{ product: GwSellerProduct }>(
          `/api/seller/products/${encodeURIComponent(initialProduct.id)}`,
          {
            method: "PUT",
            body: payload,
          }
        );
        setSkuRows(toEditableRows(res.product));
        setProductStatus(res.product.status);
        const msg = `Đã lưu cập nhật "${res.product.name}" (${formatInt(totalStock)} tồn kho).`;
        showToast(msg);
        setSavedNotice(msg);
        router.refresh();
      }
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Không thể lưu sản phẩm lúc này.";
      setErrorMsg(msg);
      showToast(msg);
    } finally {
      setSaving(false);
    }
  };

  const statusMeta = getSellerProductStatusMeta(productStatus);

  return (
    <form className={common.page} onSubmit={handleSubmit}>
      <div className={common.pageHeader}>
        <div className={common.headerText}>
          <Link href="/seller/san-pham" className={styles.backLink}>
            Quay lại danh sách sản phẩm
          </Link>
          <h1 className={common.title}>
            {mode === "create" ? "Thêm sản phẩm mới" : initialProduct?.name}
          </h1>
          <p className={common.finding}>
            {shop?.name ?? "Gian hàng"} · {formatInt(skuRows.length)} biến thể SKU · Tổng tồn{" "}
            {formatInt(totalStock)} chiếc · {statusMeta.label}
          </p>
        </div>
        <div className={common.headerActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => router.push("/seller/san-pham")}
          >
            Hủy
          </Button>
          <Button type="submit" variant="ink" size="sm" shape="app" disabled={saving}>
            <IconCheck size={14} />
            <span>
              {saving ? "Đang lưu..." : mode === "create" ? "Đăng bán sản phẩm" : "Lưu thay đổi"}
            </span>
          </Button>
        </div>
      </div>

      {savedNotice && (
        <div className={styles.noticeBar} role="status">
          <span>{savedNotice}</span>
          <Link href="/seller/san-pham" className={styles.noticeLink}>
            Xem trong danh sách
          </Link>
        </div>
      )}

      {errorMsg && (
        <div className={common.card} role="alert">
          <p className={common.fieldError}>{errorMsg}</p>
        </div>
      )}

      <div className={styles.topRow}>
        <section className={common.card} aria-labelledby="basic-info-heading">
          <div className={common.cardHeader}>
            <h2 id="basic-info-heading" className={common.cardTitle}>
              Thông tin sản phẩm & danh mục
            </h2>
            <span className={common.cardMeta}>Đồng bộ trực tiếp với danh mục sàn</span>
          </div>

          <div className={styles.fieldStack}>
            <div className={common.field}>
              <label htmlFor="prod-name" className={common.fieldLabel}>
                Tên sản phẩm đầy đủ
              </label>
              <input
                id="prod-name"
                type="text"
                className={common.textInput}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Ví dụ: Áo thun Cotton Compact 220gsm Xanh đá"
              />
            </div>

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="prod-category" className={common.fieldLabel}>
                  Ngành hàng chính
                </label>
                <select
                  id="prod-category"
                  className={common.textInput}
                  value={categoryId}
                  onChange={(e) => setCategoryId(e.target.value)}
                >
                  {categories.map((cat) => (
                    <option key={cat.id} value={cat.id}>
                      {cat.name}
                    </option>
                  ))}
                </select>
              </div>

              {mode === "edit" && (
                <div className={common.field}>
                  <label htmlFor="prod-status" className={common.fieldLabel}>
                    Trạng thái hiển thị
                  </label>
                  <select
                    id="prod-status"
                    className={common.textInput}
                    value={productStatus}
                    onChange={(e) => setProductStatus(e.target.value)}
                  >
                    <option value={productStatus}>{statusMeta.label}</option>
                    {productStatus === "APPROVED" && <option value="HIDDEN">Đang ẩn</option>}
                    {productStatus === "HIDDEN" && <option value="APPROVED">Đang bán</option>}
                  </select>
                </div>
              )}
            </div>

            <div className={common.field}>
              <label htmlFor="prod-desc" className={common.fieldLabel}>
                Mô tả chất liệu & hoàn thiện
              </label>
              <textarea
                id="prod-desc"
                className={common.textArea}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>
          </div>
        </section>

        <section className={common.card} aria-label="Ảnh đại diện và tổng hợp kho">
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>Ảnh đại diện và tổng hợp kho</h2>
          </div>

          <div className={styles.mediaPickerRow}>
            <div className={styles.previewMedia}>
              <img
                src={selectedImage}
                alt={name || "Ảnh sản phẩm"}
                className={styles.previewImg}
                width={144}
                height={192}
              />
            </div>

            <div className={styles.thumbGrid}>
              {GALLERY_CHOICES.slice(0, 6).map((img) => (
                <button
                  key={img.path}
                  type="button"
                  className={`${styles.thumbBtn} ${selectedImage === img.path ? styles.thumbBtnActive : ""}`}
                  onClick={() => setSelectedImage(img.path)}
                  title={img.label}
                  aria-label={`Chọn ảnh ${img.label}`}
                >
                  <img
                    src={img.path}
                    alt={img.label}
                    className={styles.thumbImg}
                    width={64}
                    height={85}
                  />
                </button>
              ))}
            </div>
          </div>

          <div className={styles.summaryList}>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Tổng biến thể SKU</span>
              <span className={styles.summaryVal}>{formatInt(skuRows.length)}</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Tổng tồn kho</span>
              <span className={styles.summaryVal}>{formatInt(totalStock)} chiếc</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Khoảng giá bán</span>
              <span className={styles.summaryVal}>{priceRangeLabel}</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>SKU dưới ngưỡng 5</span>
              <span className={styles.summaryVal}>{formatInt(lowSkuCount)}</span>
            </div>
          </div>
        </section>
      </div>

      <section className={common.card} aria-labelledby="sku-matrix-heading">
        <div className={common.cardHeader}>
          <div>
            <h2 id="sku-matrix-heading" className={common.cardTitle}>
              Bảng biến thể SKU, giá nguyên VND và tồn kho
            </h2>
            <span className={common.cardMeta}>
              Cập nhật từng dòng hoặc lưu toàn bộ sản phẩm; tồn kho mới không nhỏ hơn số lượng đang
              giữ cho đơn hàng
            </span>
          </div>
          <div className={common.headerActions}>
            <input
              type="number"
              min={0}
              className={common.textInput}
              style={{ width: 92, height: 34 }}
              value={bulkStockStr}
              onChange={(e) => setBulkStockStr(e.target.value)}
              aria-label="Số lượng áp dụng hàng loạt"
            />
            <Button variant="outline" size="sm" shape="app" onClick={handleBulkStock}>
              Đặt tồn đồng loạt
            </Button>
          </div>
        </div>

        <div className={common.tableWrap}>
          <table className={common.dataTable}>
            <thead>
              <tr>
                <th>Mã SKU</th>
                <th>Màu sắc</th>
                <th>Kích cỡ</th>
                <th className={common.numCol}>Đơn giá (VND)</th>
                <th className={common.numCol}>Tổng tồn</th>
                <th className={common.numCol}>Đang giữ</th>
                <th className={common.numCol}>Khối lượng (g)</th>
                <th className={common.numCol}>Thao tác</th>
              </tr>
            </thead>
            <tbody>
              {skuRows.map((row) => (
                <tr key={row.localKey}>
                  <td>
                    <input
                      type="text"
                      className={common.textInput}
                      style={{ minWidth: 150, height: 34 }}
                      value={row.skuCode}
                      onChange={(e) => updateRow(row.localKey, { skuCode: e.target.value })}
                      aria-label={`Mã SKU ${row.name}`}
                    />
                  </td>
                  <td>
                    <input
                      type="text"
                      className={common.textInput}
                      style={{ minWidth: 110, height: 34 }}
                      value={row.colorName}
                      onChange={(e) =>
                        updateRow(row.localKey, {
                          colorName: e.target.value,
                          name: `${e.target.value} / ${row.size}`,
                        })
                      }
                      aria-label={`Màu sắc ${row.skuCode}`}
                    />
                  </td>
                  <td>
                    <input
                      type="text"
                      className={common.textInput}
                      style={{ width: 72, height: 34 }}
                      value={row.size}
                      onChange={(e) =>
                        updateRow(row.localKey, {
                          size: e.target.value.toUpperCase(),
                          name: `${row.colorName} / ${e.target.value.toUpperCase()}`,
                        })
                      }
                      aria-label={`Kích cỡ ${row.skuCode}`}
                    />
                  </td>
                  <td className={common.numCol}>
                    <input
                      type="number"
                      min={1000}
                      step={1000}
                      className={common.textInput}
                      style={{ width: 112, height: 34, textAlign: "right" }}
                      value={row.priceVnd}
                      onChange={(e) =>
                        updateRow(row.localKey, {
                          priceVnd: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                        })
                      }
                      aria-label={`Đơn giá ${row.skuCode}`}
                    />
                  </td>
                  <td className={common.numCol}>
                    <input
                      type="number"
                      min={row.reservedStock}
                      className={common.textInput}
                      style={{ width: 84, height: 34, textAlign: "right" }}
                      value={row.stock}
                      onChange={(e) =>
                        updateRow(row.localKey, {
                          stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                        })
                      }
                      aria-label={`Tồn kho ${row.skuCode}`}
                    />
                  </td>
                  <td className={common.numCol}>{formatInt(row.reservedStock)}</td>
                  <td className={common.numCol}>
                    <input
                      type="number"
                      min={10}
                      step={10}
                      className={common.textInput}
                      style={{ width: 80, height: 34, textAlign: "right" }}
                      value={row.weightGrams}
                      onChange={(e) =>
                        updateRow(row.localKey, {
                          weightGrams: Math.max(10, Math.trunc(Number(e.target.value) || 250)),
                        })
                      }
                      aria-label={`Khối lượng ${row.skuCode}`}
                    />
                  </td>
                  <td className={common.numCol}>
                    <div className={common.headerActions} style={{ justifyContent: "flex-end" }}>
                      {mode === "edit" && row.id && row.version ? (
                        <Button
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={busyStockId === row.id}
                          onClick={() => void handleQuickSaveSkuStock(row)}
                        >
                          {busyStockId === row.id ? "Đang lưu..." : "Lưu tồn"}
                        </Button>
                      ) : null}
                      {skuRows.length > 1 && (
                        <Button
                          variant="outline"
                          size="sm"
                          shape="app"
                          onClick={() => handleRemoveRow(row.localKey)}
                        >
                          Xóa
                        </Button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div
          className={common.toolbar}
          style={{ marginTop: 14, paddingTop: 14, borderTop: "1px solid var(--border)" }}
        >
          <div className={common.toolbarLeft}>
            <input
              type="text"
              className={common.textInput}
              style={{ width: 160, height: 34 }}
              value={newSkuColorName}
              onChange={(e) => setNewSkuColorName(e.target.value)}
              placeholder="Tên màu mới"
              aria-label="Tên màu cho SKU mới"
            />
            <select
              className={common.filterSelect}
              value={newSkuSize}
              onChange={(e) => setNewSkuSize(e.target.value)}
              aria-label="Chọn kích cỡ cho SKU mới"
            >
              {["S", "M", "L", "XL", "2XL"].map((sz) => (
                <option key={sz} value={sz}>
                  Size {sz}
                </option>
              ))}
            </select>
            <Button variant="outline" size="sm" shape="app" onClick={handleAddSkuRow}>
              <IconPlus size={14} />
              <span>Thêm biến thể SKU</span>
            </Button>
          </div>
        </div>
      </section>
    </form>
  );
}
