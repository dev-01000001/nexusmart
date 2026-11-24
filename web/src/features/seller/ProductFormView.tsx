"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type {
  GatewayCategory,
  GatewayProduct,
  GatewaySellerSKUInput,
  GatewayShop,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconCheck, IconClose, IconPlus, StateView } from "@/components";
import { formatInt, formatVnd } from "@/lib/format";
import { SellerShopGate } from "./SellerContext";
import { getAllowedSellerStatuses, getProductStatusMeta } from "./types";
import common from "./SellerCommon.module.css";
import styles from "./ProductFormView.module.css";

const ALL_SIZE_OPTIONS = ["S", "M", "L", "XL", "2XL"];

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
  sku_code: string;
  name: string;
  color: string;
  size: string;
  priceStr: string;
  stockStr: string;
  weightStr: string;
  image_url: string;
  reservedStock: number;
  availableStock: number;
}

function slugifyCode(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .replace(/[^a-zA-Z0-9]+/g, "")
    .toUpperCase()
    .slice(0, 8);
}

function slugifyUrl(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .replace(/[^a-zA-Z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .toLowerCase();
}

export interface ProductFormViewProps {
  mode: "create" | "edit";
  productId?: string;
}

function ProductFormInner({ mode, productId, shop }: ProductFormViewProps & { shop: GatewayShop }) {
  const router = useRouter();
  const { showToast } = useMockStore();

  const [categories, setCategories] = useState<GatewayCategory[]>([]);
  const [loadedProduct, setLoadedProduct] = useState<GatewayProduct | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);

  // Core product fields
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [description, setDescription] = useState(
    "Sản phẩm mặc hằng ngày hoàn thiện kỹ đường may, kiểm soát độ co rút và giữ phom sau nhiều lần giặt."
  );
  const [status, setStatus] = useState("PENDING");
  const [selectedImage, setSelectedImage] = useState(GALLERY_CHOICES[0].path);
  const [customImageUrl, setCustomImageUrl] = useState("");
  const [extraImages, setExtraImages] = useState<string[]>([]);

  // SKU rows
  const [skuRows, setSkuRows] = useState<EditableSkuRow[]>(() => [
    {
      localKey: "init-1",
      sku_code: "NM-M-TRANG",
      name: "Trắng tinh / Size M",
      color: "Trắng tinh",
      size: "M",
      priceStr: "199000",
      stockStr: "20",
      weightStr: "250",
      image_url: GALLERY_CHOICES[0].path,
      reservedStock: 0,
      availableStock: 20,
    },
    {
      localKey: "init-2",
      sku_code: "NM-L-TRANG",
      name: "Trắng tinh / Size L",
      color: "Trắng tinh",
      size: "L",
      priceStr: "199000",
      stockStr: "20",
      weightStr: "260",
      image_url: GALLERY_CHOICES[0].path,
      reservedStock: 0,
      availableStock: 20,
    },
  ]);

  // Quick matrix generator controls
  const [genColor, setGenColor] = useState("Xám đá");
  const [genSizes, setGenSizes] = useState<string[]>(["M", "L"]);
  const [genPriceStr, setGenPriceStr] = useState("199000");
  const [genStockStr, setGenStockStr] = useState("15");
  const genWeightStr = "250";
  const [bulkStockStr, setBulkStockStr] = useState("20");

  // Submission / delete state
  const [submitting, setSubmitting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [savedNotice, setSavedNotice] = useState<{ text: string; createdId?: string } | null>(null);

  const hydrateFromProduct = useCallback((prod: GatewayProduct) => {
    setLoadedProduct(prod);
    setName(prod.name);
    setSlug(prod.slug ?? "");
    setCategoryId(prod.category_id);
    setDescription(prod.description ?? "");
    setStatus(prod.status);
    const cover = prod.images?.[0] || GALLERY_CHOICES[0].path;
    setSelectedImage(cover);
    setExtraImages((prod.images ?? []).slice(1));
    const activeSkus = (prod.skus ?? []).filter((s) => s.is_active);
    const sourceSkus = activeSkus.length > 0 ? activeSkus : (prod.skus ?? []);
    setSkuRows(
      sourceSkus.map((s, idx) => ({
        localKey: s.id || `sku-${idx}`,
        id: s.id,
        sku_code: s.sku_code,
        name: s.name,
        color: s.attributes?.color ?? "",
        size: s.attributes?.size ?? "",
        priceStr: String(s.price_vnd),
        stockStr: String(s.stock),
        weightStr: String(s.weight_grams || 250),
        image_url: s.image_url || cover,
        reservedStock: s.reserved_stock,
        availableStock: s.available_stock,
      }))
    );
  }, []);

  const loadInitial = useCallback(async () => {
    setLoading(true);
    setLoadError(null);
    setNotFound(false);
    try {
      const catRes = await api<{ categories: GatewayCategory[] }>("seller/categories");
      const cats = catRes.categories ?? [];
      setCategories(cats);

      if (mode === "edit" && productId) {
        try {
          const prodRes = await api<{ product: GatewayProduct }>(`seller/products/${productId}`);
          if (!prodRes.product) {
            setNotFound(true);
          } else {
            hydrateFromProduct(prodRes.product);
          }
        } catch (err) {
          if (err instanceof ApiError && (err.status === 404 || err.status === 403)) {
            setNotFound(true);
          } else {
            throw err;
          }
        }
      } else if (cats.length > 0) {
        const firstAllowed = cats.find((c) => !c.is_prohibited) ?? cats[0];
        setCategoryId((prev) => prev || firstAllowed.id);
      }
    } catch (err) {
      setLoadError(getErrorMessage(err, "Không thể tải danh mục và thông tin sản phẩm."));
    } finally {
      setLoading(false);
    }
  }, [mode, productId, hydrateFromProduct]);

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  const totalStock = useMemo(
    () => skuRows.reduce((s, r) => s + Math.max(0, Math.trunc(Number(r.stockStr) || 0)), 0),
    [skuRows]
  );

  const priceRangeLabel = useMemo(() => {
    const prices = skuRows.map((r) => Math.trunc(Number(r.priceStr) || 0)).filter((v) => v > 0);
    if (prices.length === 0) return "—";
    const minP = Math.min(...prices);
    const maxP = Math.max(...prices);
    return minP === maxP ? formatVnd(minP) : `${formatVnd(minP)} - ${formatVnd(maxP)}`;
  }, [skuRows]);

  const allowedStatuses = useMemo(
    () => (loadedProduct ? getAllowedSellerStatuses(loadedProduct.status) : []),
    [loadedProduct]
  );

  const updateSkuRow = (localKey: string, patch: Partial<EditableSkuRow>) => {
    setSkuRows((prev) =>
      prev.map((row) => (row.localKey === localKey ? { ...row, ...patch } : row))
    );
  };

  const removeSkuRow = (localKey: string) => {
    setSkuRows((prev) => {
      if (prev.length <= 1) return prev;
      return prev.filter((row) => row.localKey !== localKey);
    });
  };

  const handleAddEmptySku = () => {
    const idx = skuRows.length + 1;
    const prefix = slugifyCode(name || "NM") || "NM";
    setSkuRows((prev) => [
      ...prev,
      {
        localKey: `custom-${Date.now()}-${idx}`,
        sku_code: `${prefix}-SKU-${idx}`,
        name: `Biến thể ${idx}`,
        color: "",
        size: "",
        priceStr: genPriceStr || "199000",
        stockStr: genStockStr || "10",
        weightStr: genWeightStr || "250",
        image_url: selectedImage,
        reservedStock: 0,
        availableStock: Math.max(0, Math.trunc(Number(genStockStr) || 10)),
      },
    ]);
  };

  const handleToggleGenSize = (sz: string) => {
    setGenSizes((prev) =>
      prev.includes(sz) ? (prev.length > 1 ? prev.filter((x) => x !== sz) : prev) : [...prev, sz]
    );
  };

  const handleGenerateMatrix = () => {
    const colorLabel = genColor.trim() || "Mặc định";
    const colorCode = slugifyCode(colorLabel) || "MAU";
    const prefix = slugifyCode(name || "NM") || "NM";
    const existingCodes = new Set(skuRows.map((r) => r.sku_code.trim().toUpperCase()));
    const created: EditableSkuRow[] = [];

    for (const sz of genSizes) {
      let code = `${prefix}-${colorCode}-${sz}`;
      if (existingCodes.has(code.toUpperCase())) {
        code = `${code}-${String(Date.now()).slice(-3)}`;
      }
      existingCodes.add(code.toUpperCase());
      created.push({
        localKey: `gen-${Date.now()}-${sz}-${Math.random().toString(36).slice(2, 6)}`,
        sku_code: code,
        name: `${colorLabel} / Size ${sz}`,
        color: colorLabel,
        size: sz,
        priceStr: genPriceStr || "199000",
        stockStr: genStockStr || "15",
        weightStr: genWeightStr || "250",
        image_url: selectedImage,
        reservedStock: 0,
        availableStock: Math.max(0, Math.trunc(Number(genStockStr) || 15)),
      });
    }

    setSkuRows((prev) => [...prev, ...created]);
    showToast(`Đã bổ sung ${formatInt(created.length)} biến thể SKU cho màu ${colorLabel}.`);
  };

  const handleBulkStock = () => {
    const val = Math.max(0, Math.trunc(Number(bulkStockStr) || 0));
    setSkuRows((prev) =>
      prev.map((row) => ({
        ...row,
        stockStr: String(Math.max(val, row.reservedStock)),
      }))
    );
    showToast(`Đã áp dụng tồn kho ${formatInt(val)} chiếc cho tất cả biến thể.`);
  };

  const handleAddCustomImage = () => {
    const clean = customImageUrl.trim();
    if (!clean) return;
    setSelectedImage(clean);
    setExtraImages((prev) => (prev.includes(clean) ? prev : [...prev, clean]));
    setCustomImageUrl("");
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = name.trim();
    if (cleanName.length < 3) {
      setErrorMsg("Tên sản phẩm cần tối thiểu 3 ký tự.");
      return;
    }
    if (!categoryId) {
      setErrorMsg("Vui lòng chọn danh mục ngành hàng.");
      return;
    }
    if (skuRows.length === 0) {
      setErrorMsg("Cần tối thiểu 1 biến thể SKU để lưu sản phẩm.");
      return;
    }

    const seenCodes = new Set<string>();
    const parsedSkus: GatewaySellerSKUInput[] = [];
    for (const row of skuRows) {
      const code = row.sku_code.trim();
      const skuName = row.name.trim();
      const priceVnd = Math.trunc(Number(row.priceStr) || 0);
      const stock = Math.trunc(Number(row.stockStr));
      const weightGrams = Math.trunc(Number(row.weightStr) || 0);

      if (!code || !skuName) {
        setErrorMsg("Mỗi biến thể SKU cần có đầy đủ mã SKU và tên biến thể.");
        return;
      }
      if (seenCodes.has(code.toUpperCase())) {
        setErrorMsg(`Mã SKU "${code}" bị trùng lặp trong danh sách.`);
        return;
      }
      seenCodes.add(code.toUpperCase());

      if (priceVnd < 1000) {
        setErrorMsg(`Giá bán của SKU ${code} phải là số nguyên tối thiểu 1.000₫.`);
        return;
      }
      if (!Number.isFinite(stock) || stock < 0) {
        setErrorMsg(`Tồn kho của SKU ${code} phải là số nguyên không âm.`);
        return;
      }
      if (stock < row.reservedStock) {
        setErrorMsg(
          `Tồn kho của SKU ${code} không được nhỏ hơn số đang giữ (${formatInt(row.reservedStock)}).`
        );
        return;
      }
      if (weightGrams <= 0) {
        setErrorMsg(`Khối lượng của SKU ${code} phải lớn hơn 0 gram.`);
        return;
      }

      const attributes: Record<string, string> = {};
      if (row.color.trim()) attributes.color = row.color.trim();
      if (row.size.trim()) attributes.size = row.size.trim();

      parsedSkus.push({
        ...(row.id ? { id: row.id } : {}),
        sku_code: code,
        name: skuName,
        attributes,
        price_vnd: priceVnd,
        stock,
        weight_grams: weightGrams,
        image_url: row.image_url.trim() || selectedImage,
      });
    }

    const images = Array.from(
      new Set([selectedImage, ...extraImages].map((u) => u.trim()).filter(Boolean))
    );

    setErrorMsg(null);
    setSubmitting(true);

    try {
      if (mode === "create") {
        const res = await api<{ product: GatewayProduct }>("seller/products", {
          method: "POST",
          json: {
            category_id: categoryId,
            name: cleanName,
            slug: slug.trim() || undefined,
            description: description.trim() || cleanName,
            images,
            skus: parsedSkus,
          },
        });
        const created = res.product;
        const msg = `Đã tạo sản phẩm "${created.name}" (${formatInt(parsedSkus.length)} SKU) ở trạng thái Chờ duyệt.`;
        setSavedNotice({ text: msg, createdId: created.id });
        showToast(msg);
      } else if (productId) {
        const res = await api<{ product: GatewayProduct }>(`seller/products/${productId}`, {
          method: "PUT",
          json: {
            category_id: categoryId,
            name: cleanName,
            slug: slug.trim() || undefined,
            description: description.trim() || cleanName,
            images,
            status,
            skus: parsedSkus,
          },
        });
        if (res.product) {
          hydrateFromProduct(res.product);
        }
        const msg = `Đã lưu thay đổi sản phẩm "${cleanName}" (${formatInt(totalStock)} chiếc tồn kho).`;
        setSavedNotice({ text: msg });
        showToast(msg);
      }
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể lưu sản phẩm lúc này."));
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!productId) return;
    setDeleting(true);
    try {
      await api<{ ok: boolean }>(`seller/products/${productId}`, { method: "DELETE" });
      showToast(`Đã xóa sản phẩm "${name}".`);
      router.push("/seller/san-pham");
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể xóa sản phẩm lúc này."));
    } finally {
      setDeleting(false);
    }
  };

  if (loading) {
    return (
      <StateView
        kind="loading"
        surface="app"
        title="Đang tải danh mục ngành hàng và cấu hình sản phẩm..."
        rows={5}
      />
    );
  }

  if (loadError) {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể tải biểu mẫu sản phẩm"
        message={loadError}
        actionLabel="Thử lại"
        onAction={() => void loadInitial()}
      />
    );
  }

  if (mode === "edit" && notFound) {
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
          title="Sản phẩm không tồn tại trong gian hàng"
          message="Sản phẩm có thể đã bị xóa hoặc thuộc gian hàng khác."
          actionLabel="Về danh sách sản phẩm"
          actionHref="/seller/san-pham"
        />
      </div>
    );
  }

  const currentStatusMeta = loadedProduct ? getProductStatusMeta(loadedProduct.status) : null;

  return (
    <form className={common.page} onSubmit={(e) => void handleSubmit(e)} noValidate>
      <div className={common.pageHeader}>
        <div className={common.headerText}>
          <Link href="/seller/san-pham" className={styles.backLink}>
            Quay lại danh sách sản phẩm
          </Link>
          <h1 className={common.title}>
            {mode === "create" ? "Thêm sản phẩm mới" : loadedProduct?.name || "Chỉnh sửa sản phẩm"}
          </h1>
          <p className={common.finding}>
            {shop.name} · {formatInt(skuRows.length)} biến thể SKU · Tổng tồn{" "}
            {formatInt(totalStock)} chiếc
            {currentStatusMeta
              ? ` · Trạng thái: ${currentStatusMeta.label}`
              : " · Gửi duyệt ngay sau khi tạo"}
          </p>
        </div>
        <div className={common.headerActions}>
          {mode === "edit" && (
            <>
              {confirmDelete ? (
                <>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={deleting}
                    onClick={() => void handleDelete()}
                  >
                    {deleting ? "Đang xóa..." : "Xác nhận xóa"}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    shape="app"
                    onClick={() => setConfirmDelete(false)}
                  >
                    Giữ lại
                  </Button>
                </>
              ) : (
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={() => setConfirmDelete(true)}
                >
                  Xóa sản phẩm
                </Button>
              )}
            </>
          )}
          <Button
            variant="outline"
            size="sm"
            shape="app"
            onClick={() => router.push("/seller/san-pham")}
          >
            Quay lại
          </Button>
          <Button type="submit" variant="ink" size="sm" shape="app" disabled={submitting}>
            <IconCheck size={14} />
            <span>
              {submitting
                ? "Đang lưu..."
                : mode === "create"
                  ? "Tạo sản phẩm & gửi duyệt"
                  : "Lưu thay đổi"}
            </span>
          </Button>
        </div>
      </div>

      {savedNotice && (
        <div className={styles.noticeBar} role="status">
          <span>{savedNotice.text}</span>
          <div style={{ display: "inline-flex", gap: 12 }}>
            {savedNotice.createdId && (
              <Link
                href={`/seller/san-pham/${savedNotice.createdId}`}
                className={styles.noticeLink}
              >
                Mở trang sửa
              </Link>
            )}
            <Link href="/seller/san-pham" className={styles.noticeLink}>
              Về danh sách sản phẩm
            </Link>
          </div>
        </div>
      )}

      {loadedProduct?.status === "REJECTED" && loadedProduct.rejection_reason && (
        <div className={common.card} role="alert">
          <p className={common.fieldError}>
            Sản phẩm bị quản trị viên từ chối: {loadedProduct.rejection_reason}. Hãy chỉnh sửa nội
            dung và chuyển trạng thái sang &ldquo;Gửi duyệt lại&rdquo;.
          </p>
        </div>
      )}

      {Array.isArray(loadedProduct?.flagged_keywords) &&
        loadedProduct.flagged_keywords.length > 0 && (
          <div className={common.card} role="status">
            <p className={common.fieldHint}>
              Sản phẩm đang được gắn cờ kiểm duyệt thủ công do chứa từ khoá:{" "}
              <strong>{loadedProduct.flagged_keywords.join(", ")}</strong>. Bạn nên rà soát lại tên
              và mô tả sản phẩm.
            </p>
          </div>
        )}

      {errorMsg && (
        <div className={common.card} role="alert">
          <p className={common.fieldError}>{errorMsg}</p>
        </div>
      )}

      {/* Row 1: Basic Info + Cover Image & Summary */}
      <div className={styles.topRow}>
        <section className={common.card} aria-labelledby="basic-info-heading">
          <div className={common.cardHeader}>
            <h2 id="basic-info-heading" className={common.cardTitle}>
              Thông tin cơ bản sản phẩm
            </h2>
            <span className={common.cardMeta}>
              Sản phẩm tạo mới vào trạng thái Chờ duyệt trước khi mở bán công khai
            </span>
          </div>

          <div className={styles.fieldStack}>
            <div className={common.field}>
              <label htmlFor="prod-name" className={common.fieldLabel}>
                Tên sản phẩm
              </label>
              <input
                id="prod-name"
                type="text"
                className={common.textInput}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Ví dụ: Áo thun Cotton Compact 220gsm Xanh đá"
                required
              />
            </div>

            <div className={styles.fields2}>
              <div className={common.field}>
                <label htmlFor="prod-category" className={common.fieldLabel}>
                  Danh mục ngành hàng
                </label>
                <select
                  id="prod-category"
                  className={common.textInput}
                  value={categoryId}
                  onChange={(e) => setCategoryId(e.target.value)}
                  required
                >
                  {categories.map((cat) => (
                    <option key={cat.id} value={cat.id} disabled={Boolean(cat.is_prohibited)}>
                      {typeof cat.commission_rate_bps === "number" &&
                      Number.isFinite(cat.commission_rate_bps)
                        ? `${cat.name} (Hoa hồng ${(cat.commission_rate_bps / 100).toFixed(1)}%)`
                        : cat.name}
                      {cat.is_prohibited ? " — Danh mục cấm" : ""}
                    </option>
                  ))}
                </select>
              </div>

              <div className={common.field}>
                <label htmlFor="prod-slug" className={common.fieldLabel}>
                  Đường dẫn tùy chỉnh (tùy chọn)
                </label>
                <input
                  id="prod-slug"
                  type="text"
                  className={common.textInput}
                  value={slug}
                  onChange={(e) => setSlug(e.target.value)}
                  placeholder={name ? slugifyUrl(name) : "tu-dong-tao-tu-ten-san-pham"}
                />
              </div>
            </div>

            {mode === "edit" && loadedProduct && (
              <div className={common.field}>
                <label htmlFor="prod-status" className={common.fieldLabel}>
                  Trạng thái hiển thị
                </label>
                <select
                  id="prod-status"
                  className={common.textInput}
                  value={status}
                  disabled={allowedStatuses.length <= 1}
                  onChange={(e) => setStatus(e.target.value)}
                >
                  {allowedStatuses.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                <span className={common.fieldHint}>
                  Người bán chỉ có thể ẩn/hiện sản phẩm đã duyệt hoặc gửi duyệt lại sản phẩm bị từ
                  chối.
                </span>
              </div>
            )}

            <div className={common.field}>
              <label htmlFor="prod-desc" className={common.fieldLabel}>
                Mô tả chi tiết sản phẩm
              </label>
              <textarea
                id="prod-desc"
                className={common.textArea}
                rows={4}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Mô tả chất liệu, định lượng vải, phom dáng và hướng dẫn bảo quản..."
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
                alt={name || "Ảnh sản phẩm mẫu"}
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

          <div className={styles.addColorRow} style={{ marginBottom: 14 }}>
            <input
              type="text"
              className={common.textInput}
              style={{ flex: 1, height: 34, fontSize: 13 }}
              value={customImageUrl}
              onChange={(e) => setCustomImageUrl(e.target.value)}
              placeholder="Hoặc dán đường dẫn ảnh (/products/...)"
              aria-label="Đường dẫn ảnh sản phẩm tùy chỉnh"
            />
            <Button variant="outline" size="sm" shape="app" onClick={handleAddCustomImage}>
              Dùng ảnh này
            </Button>
          </div>

          <div className={styles.summaryList}>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Tổng biến thể SKU</span>
              <span className={styles.summaryVal}>{formatInt(skuRows.length)}</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Khoảng giá bán</span>
              <span className={styles.summaryVal}>{priceRangeLabel}</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Tổng tồn kho khai báo</span>
              <span className={styles.summaryVal}>{formatInt(totalStock)} chiếc</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Gian hàng sở hữu</span>
              <span className={styles.summaryVal}>{shop.name}</span>
            </div>
          </div>
        </section>
      </div>

      {/* Row 2: SKU Matrix Editor */}
      <section className={common.card} aria-labelledby="sku-matrix-heading">
        <div className={common.cardHeader}>
          <div>
            <h2 id="sku-matrix-heading" className={common.cardTitle}>
              Danh sách biến thể SKU, giá bán và tồn kho
            </h2>
            <span className={common.cardMeta}>
              Mọi giá bán là số nguyên VND; khối lượng tính theo gram để tính phí vận chuyển
            </span>
          </div>
          <Button variant="outline" size="sm" shape="app" onClick={handleAddEmptySku}>
            <IconPlus size={14} />
            <span>Thêm dòng SKU</span>
          </Button>
        </div>

        {/* Quick generator for Color x Size */}
        <div className={styles.variantControls}>
          <div className={styles.controlGroup}>
            <span className={styles.controlLabel}>Tạo nhanh theo màu sắc & kích cỡ</span>
            <div className={styles.addColorRow}>
              <input
                type="text"
                className={styles.colorNameInput}
                value={genColor}
                onChange={(e) => setGenColor(e.target.value)}
                placeholder="Tên màu (VD: Xanh than)"
                aria-label="Tên màu cho biến thể mới"
              />
              <div className={styles.sizeRow}>
                {ALL_SIZE_OPTIONS.map((sz) => {
                  const active = genSizes.includes(sz);
                  return (
                    <button
                      key={sz}
                      type="button"
                      className={`${styles.sizeToggle} ${active ? styles.sizeToggleActive : ""}`}
                      onClick={() => handleToggleGenSize(sz)}
                    >
                      {sz}
                    </button>
                  );
                })}
              </div>
              <input
                type="number"
                className={styles.numCellInput}
                value={genPriceStr}
                onChange={(e) => setGenPriceStr(e.target.value)}
                placeholder="Giá VND"
                aria-label="Giá mặc định khi tạo nhanh SKU"
              />
              <input
                type="number"
                className={styles.stockCellInput}
                value={genStockStr}
                onChange={(e) => setGenStockStr(e.target.value)}
                placeholder="Tồn"
                aria-label="Tồn kho mặc định khi tạo nhanh SKU"
              />
              <Button variant="outline" size="sm" shape="app" onClick={handleGenerateMatrix}>
                <IconPlus size={14} />
                <span>Tạo tổ hợp SKU</span>
              </Button>
            </div>
          </div>

          <div className={styles.bulkRow}>
            <span className={common.cardMeta}>
              Đặt nhanh cùng mức tồn kho cho toàn bộ {formatInt(skuRows.length)} dòng SKU bên dưới
            </span>
            <div className={styles.bulkControls}>
              <input
                type="number"
                min={0}
                step={1}
                className={styles.bulkInput}
                value={bulkStockStr}
                onChange={(e) => setBulkStockStr(e.target.value)}
                aria-label="Số tồn kho áp dụng hàng loạt"
              />
              <Button variant="outline" size="sm" shape="app" onClick={handleBulkStock}>
                Áp dụng toàn bộ
              </Button>
            </div>
          </div>
        </div>

        {/* Desktop editable table */}
        <div className={`${common.tableWrap} ${styles.skuTableDesktop}`}>
          <table className={common.dataTable}>
            <thead>
              <tr>
                <th>Mã SKU</th>
                <th>Tên biến thể</th>
                <th>Màu / Size</th>
                <th className={common.numCol}>Giá bán (VND)</th>
                <th className={common.numCol}>Tổng tồn</th>
                <th className={common.numCol}>Khối lượng (g)</th>
                <th className={common.numCol}>Xóa</th>
              </tr>
            </thead>
            <tbody>
              {skuRows.map((row) => (
                <tr key={row.localKey}>
                  <td>
                    <input
                      type="text"
                      className={styles.skuCodeInput}
                      value={row.sku_code}
                      onChange={(e) => updateSkuRow(row.localKey, { sku_code: e.target.value })}
                      aria-label="Mã SKU"
                    />
                  </td>
                  <td>
                    <input
                      type="text"
                      className={styles.skuCodeInput}
                      value={row.name}
                      onChange={(e) => updateSkuRow(row.localKey, { name: e.target.value })}
                      aria-label="Tên biến thể SKU"
                    />
                  </td>
                  <td>
                    <div style={{ display: "flex", gap: 6 }}>
                      <input
                        type="text"
                        className={styles.stockCellInput}
                        style={{ width: 88, textAlign: "left" }}
                        value={row.color}
                        onChange={(e) => updateSkuRow(row.localKey, { color: e.target.value })}
                        placeholder="Màu"
                        aria-label="Màu sắc"
                      />
                      <input
                        type="text"
                        className={styles.stockCellInput}
                        style={{ width: 60, textAlign: "left" }}
                        value={row.size}
                        onChange={(e) => updateSkuRow(row.localKey, { size: e.target.value })}
                        placeholder="Size"
                        aria-label="Kích cỡ"
                      />
                    </div>
                  </td>
                  <td className={common.numCol}>
                    <input
                      type="number"
                      min={1000}
                      step={1}
                      className={styles.numCellInput}
                      value={row.priceStr}
                      onChange={(e) => updateSkuRow(row.localKey, { priceStr: e.target.value })}
                      aria-label={`Giá bán của ${row.sku_code}`}
                    />
                  </td>
                  <td className={common.numCol}>
                    <input
                      type="number"
                      min={row.reservedStock}
                      step={1}
                      className={`${styles.stockCellInput} ${Number(row.stockStr) === 0 ? styles.stockZero : ""}`}
                      value={row.stockStr}
                      onChange={(e) => updateSkuRow(row.localKey, { stockStr: e.target.value })}
                      aria-label={`Tồn kho của ${row.sku_code}`}
                    />
                    {row.reservedStock > 0 && (
                      <span className={common.cellSub}>Đang giữ: {row.reservedStock}</span>
                    )}
                  </td>
                  <td className={common.numCol}>
                    <input
                      type="number"
                      min={1}
                      step={1}
                      className={styles.stockCellInput}
                      value={row.weightStr}
                      onChange={(e) => updateSkuRow(row.localKey, { weightStr: e.target.value })}
                      aria-label={`Khối lượng của ${row.sku_code}`}
                    />
                  </td>
                  <td className={common.numCol}>
                    <button
                      type="button"
                      className={styles.removeColorBtn}
                      disabled={skuRows.length <= 1}
                      onClick={() => removeSkuRow(row.localKey)}
                      aria-label={`Xóa dòng ${row.sku_code}`}
                    >
                      <IconClose size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* Mobile SKU editor list */}
        <div className={styles.mobileSkuList}>
          {skuRows.map((row) => (
            <div key={row.localKey} className={styles.mobileSkuItem}>
              <div className={styles.mobileSkuHead}>
                <span>{row.sku_code || "SKU mới"}</span>
                {skuRows.length > 1 && (
                  <button
                    type="button"
                    className={styles.removeColorBtn}
                    onClick={() => removeSkuRow(row.localKey)}
                    aria-label="Xóa dòng SKU"
                  >
                    <IconClose size={14} />
                  </button>
                )}
              </div>
              <input
                type="text"
                className={styles.skuCodeInput}
                value={row.sku_code}
                onChange={(e) => updateSkuRow(row.localKey, { sku_code: e.target.value })}
                placeholder="Mã SKU"
              />
              <input
                type="text"
                className={styles.skuCodeInput}
                value={row.name}
                onChange={(e) => updateSkuRow(row.localKey, { name: e.target.value })}
                placeholder="Tên biến thể"
              />
              <div className={styles.mobileSkuInputs}>
                <div className={common.field}>
                  <label className={common.fieldHint}>Giá bán (VND)</label>
                  <input
                    type="number"
                    min={1000}
                    step={1}
                    className={styles.numCellInput}
                    value={row.priceStr}
                    onChange={(e) => updateSkuRow(row.localKey, { priceStr: e.target.value })}
                  />
                </div>
                <div className={common.field}>
                  <label className={common.fieldHint}>Tồn kho</label>
                  <input
                    type="number"
                    min={row.reservedStock}
                    step={1}
                    className={styles.stockCellInput}
                    value={row.stockStr}
                    onChange={(e) => updateSkuRow(row.localKey, { stockStr: e.target.value })}
                  />
                </div>
                <div className={common.field}>
                  <label className={common.fieldHint}>Màu sắc</label>
                  <input
                    type="text"
                    className={styles.skuCodeInput}
                    value={row.color}
                    onChange={(e) => updateSkuRow(row.localKey, { color: e.target.value })}
                  />
                </div>
                <div className={common.field}>
                  <label className={common.fieldHint}>Khối lượng (g)</label>
                  <input
                    type="number"
                    min={1}
                    step={1}
                    className={styles.stockCellInput}
                    value={row.weightStr}
                    onChange={(e) => updateSkuRow(row.localKey, { weightStr: e.target.value })}
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
      </section>
    </form>
  );
}

export function ProductFormView({ mode, productId }: ProductFormViewProps) {
  return (
    <SellerShopGate pageTitle={mode === "create" ? "Thêm sản phẩm mới" : "Chỉnh sửa sản phẩm"}>
      {(shop) => <ProductFormInner mode={mode} productId={productId} shop={shop} />}
    </SellerShopGate>
  );
}
