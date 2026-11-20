"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  CARE_STANDARD_PRESETS,
  CATEGORIES,
  FABRIC_FEATURE_OPTIONS,
  FIT_OPTIONS,
  MATERIAL_OPTIONS,
  OCCASION_OPTIONS,
  PRODUCTS,
  SPORT_OPTIONS,
  TAXONOMY_L1_L3,
  getShopById,
} from "@/data";
import type {
  CategorySlug,
  GenderCode,
  Product,
  ProductColor,
  ProductSku,
  SellerProductKind,
  SizeCode,
} from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, IconCheck, IconClose, IconPlus, StateView } from "@/components";
import { formatInt, formatVnd } from "@/lib/format";
import {
  EMPTY_DASH,
  getProductByIdWithSession,
  getProductExtendedMetaWithSession,
  getShopFactoryProfileWithSession,
  saveSellerProduct,
  saveSellerProductMeta,
  useSellerSession,
} from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "./ProductFormView.module.css";

const ALL_SIZE_OPTIONS: SizeCode[] = ["S", "M", "L", "XL", "2XL"];

const PRESET_SWATCHES = [
  { name: "Trắng tinh", hex: "#f4f6f7" },
  { name: "Xám đá", hex: "#9aa5ac" },
  { name: "Xanh than", hex: "#283947" },
  { name: "Xanh cổ vịt", hex: "#0d7480" },
  { name: "Be cát", hex: "#d6cec1" },
  { name: "Đen than", hex: "#20282e" },
];

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

function slugifyCode(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .replace(/[^a-zA-Z0-9]+/g, "")
    .toLowerCase()
    .slice(0, 10);
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

export function ProductFormView({ mode, productId }: ProductFormViewProps) {
  const router = useRouter();
  const { activeSellerShopId, showToast } = useStore();
  const session = useSellerSession();

  const existing =
    mode === "edit" && productId ? getProductByIdWithSession(productId, session) : undefined;
  const shopId = existing?.shopId ?? activeSellerShopId;
  const activeShop = getShopById(shopId);
  const factoryProfile = getShopFactoryProfileWithSession(shopId, session);

  const initialMeta = getProductExtendedMetaWithSession(
    existing?.id ?? "new-product",
    existing?.categorySlug ?? "ao-thun",
    session
  );

  // Basic info
  const [name, setName] = useState(existing?.name ?? "");
  const [shortName, setShortName] = useState(existing?.shortName ?? "");
  const [categorySlug, setCategorySlug] = useState<CategorySlug>(
    existing?.categorySlug ?? "ao-thun"
  );
  const [taxonomyIndex, setTaxonomyIndex] = useState(() => {
    const paths = TAXONOMY_L1_L3[existing?.categorySlug ?? "ao-thun"] ?? [];
    const hit = paths.findIndex((p) => p[2] === initialMeta.categoryPath[2]);
    return hit >= 0 ? hit : 0;
  });
  const [shopCollection, setShopCollection] = useState(
    initialMeta.shopCollection || factoryProfile.collections[0]?.name || "Bộ sưu tập cơ bản"
  );
  const [productKind, setProductKind] = useState<SellerProductKind>(initialMeta.productKind);
  const [comboSlotsText, setComboSlotsText] = useState(
    initialMeta.comboSlots.length > 0
      ? initialMeta.comboSlots.join("\n")
      : "Món 1 (chọn màu & size)\nMón 2 (chọn màu & size)"
  );
  const [gender, setGender] = useState<GenderCode>(existing?.gender ?? "unisex");
  const [priceStr, setPriceStr] = useState(String(existing?.price ?? 189000));
  const [origPriceStr, setOrigPriceStr] = useState(
    existing?.originalPrice ? String(existing.originalPrice) : ""
  );
  const [origin, setOrigin] = useState(existing?.origin ?? activeShop?.city ?? "Việt Nam");

  // Industry attributes
  const [materialGroup, setMaterialGroup] = useState(initialMeta.materialGroup);
  const [gsmStr, setGsmStr] = useState(String(existing?.gsm ?? 220));
  const [fabric, setFabric] = useState(
    existing?.fabric ?? "95% Cotton chải kỹ Compact, 5% Spandex"
  );
  const [fitType, setFitType] = useState(initialMeta.fitType);
  const [sport, setSport] = useState(initialMeta.sport);
  const [occasions, setOccasions] = useState<string[]>(initialMeta.occasions);
  const [fabricFeatures, setFabricFeatures] = useState<string[]>(initialMeta.fabricFeatures);

  // Model measurements
  const [modelGender, setModelGender] = useState(initialMeta.modelInfo.genderLabel);
  const [modelHeightStr, setModelHeightStr] = useState(String(initialMeta.modelInfo.heightCm));
  const [modelWeightStr, setModelWeightStr] = useState(String(initialMeta.modelInfo.weightKg));
  const [modelMeasures, setModelMeasures] = useState(initialMeta.modelInfo.measurements);
  const [modelSize, setModelSize] = useState(initialMeta.modelInfo.wearingSize);
  const [secondaryModel, setSecondaryModel] = useState(initialMeta.modelInfo.secondaryModel ?? "");

  // Care presets & description
  const [carePresetIds, setCarePresetIds] = useState<string[]>(initialMeta.carePresetIds);
  const [careText, setCareText] = useState(
    existing?.careGuide.join("\n") ??
      CARE_STANDARD_PRESETS.filter((c) => initialMeta.carePresetIds.includes(c.id))
        .map((c) => c.detail)
        .join("\n")
  );
  const [description, setDescription] = useState(
    existing?.description ??
      "Sản phẩm đồ mặc hằng ngày hoàn thiện tại xưởng, kiểm soát độ co rút và giữ phom sau nhiều lần giặt."
  );

  // Policies & purchase limits
  const [maxQtyStr, setMaxQtyStr] = useState(String(initialMeta.maxQtyPerOrder));
  const [isPreOrder, setIsPreOrder] = useState(initialMeta.isPreOrder);
  const [preOrderDate, setPreOrderDate] = useState(initialMeta.preOrderShipDate ?? "28/11/2026");
  const [nonReturnable, setNonReturnable] = useState(initialMeta.nonReturnable);
  const [nonReturnReason, setNonReturnReason] = useState(
    initialMeta.nonReturnReason ?? "Hàng Outlet hoặc pack đồ lót niêm phong không hỗ trợ đổi trả"
  );

  // Media & SKU matrix
  const [selectedImage, setSelectedImage] = useState(existing?.image ?? GALLERY_CHOICES[0].path);
  const [colors, setColors] = useState<ProductColor[]>(
    existing?.colors ?? [
      {
        code: "trang",
        name: "Trắng tinh",
        hex: "#f4f6f7",
        image: GALLERY_CHOICES[0].path,
      },
      {
        code: "xamda",
        name: "Xám đá",
        hex: "#9aa5ac",
        image: GALLERY_CHOICES[1].path,
      },
    ]
  );
  const [sizes, setSizes] = useState<SizeCode[]>(existing?.sizes ?? ["S", "M", "L", "XL"]);
  const [skuMap, setSkuMap] = useState<Record<string, ProductSku>>(() => {
    const initial: Record<string, ProductSku> = {};
    if (existing) {
      for (const s of existing.skus) {
        initial[`${s.colorCode}__${s.size}`] = { ...s };
      }
    } else {
      const defaults: Array<[string, SizeCode, number]> = [
        ["trang", "S", 14],
        ["trang", "M", 26],
        ["trang", "L", 22],
        ["trang", "XL", 12],
        ["xamda", "S", 10],
        ["xamda", "M", 18],
        ["xamda", "L", 16],
        ["xamda", "XL", 8],
      ];
      for (const [c, sz, st] of defaults) {
        initial[`${c}__${sz}`] = {
          skuCode: `NM-NEW-${c.toUpperCase()}-${sz}`,
          colorCode: c,
          size: sz,
          stock: st,
          price: 189000,
        };
      }
    }
    return initial;
  });

  const [newColorName, setNewColorName] = useState("");
  const [newColorHex, setNewColorHex] = useState(PRESET_SWATCHES[2].hex);
  const [newColorImg, setNewColorImg] = useState(GALLERY_CHOICES[2].path);
  const [bulkStockStr, setBulkStockStr] = useState("20");
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [savedNotice, setSavedNotice] = useState<string | null>(null);

  const basePrice = Math.max(0, Math.trunc(Number(priceStr) || 0));
  const taxonomyOptions = TAXONOMY_L1_L3[categorySlug] ?? TAXONOMY_L1_L3["ao-thun"];
  const activeTaxonomyPath = taxonomyOptions[taxonomyIndex] ?? taxonomyOptions[0];

  const activeSkus = useMemo<ProductSku[]>(() => {
    const prefix = existing?.skus[0]?.skuCode.split("-").slice(0, 2).join("-") ?? "NM-SKU";
    const list: ProductSku[] = [];
    for (const c of colors) {
      for (const sz of sizes) {
        const key = `${c.code}__${sz}`;
        const cur = skuMap[key];
        list.push(
          cur ?? {
            skuCode: `${prefix}-${c.code.toUpperCase()}-${sz}`,
            colorCode: c.code,
            size: sz,
            stock: 10,
            price: basePrice || 189000,
          }
        );
      }
    }
    return list;
  }, [colors, sizes, skuMap, existing, basePrice]);

  const totalStock = useMemo(
    () => activeSkus.reduce((s, item) => s + Math.max(0, Math.trunc(item.stock)), 0),
    [activeSkus]
  );
  const lowSkuCount = useMemo(() => activeSkus.filter((s) => s.stock <= 5).length, [activeSkus]);
  const waitingTotal = useMemo(
    () => Object.values(initialMeta.backInStockSubscribers).reduce((s, n) => s + n, 0),
    [initialMeta.backInStockSubscribers]
  );
  const priceRangeLabel = useMemo(() => {
    if (!activeSkus.length) return EMPTY_DASH;
    const minP = Math.min(...activeSkus.map((s) => s.price));
    const maxP = Math.max(...activeSkus.map((s) => s.price));
    return minP === maxP ? formatVnd(minP) : `${formatVnd(minP)} - ${formatVnd(maxP)}`;
  }, [activeSkus]);

  if (mode === "edit" && !existing) {
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
          message="Sản phẩm có thể đã bị gỡ hoặc thuộc một gian hàng khác."
          actionLabel="Về danh sách sản phẩm"
          actionHref="/seller/san-pham"
        />
      </div>
    );
  }

  const updateSkuField = (
    colorCode: string,
    size: SizeCode,
    patch: Partial<ProductSku>,
    fallbackSku: ProductSku
  ) => {
    const key = `${colorCode}__${size}`;
    setSkuMap((prev) => ({
      ...prev,
      [key]: {
        ...(prev[key] ?? fallbackSku),
        ...patch,
      },
    }));
  };

  const handleToggleSize = (size: SizeCode) => {
    setSizes((prev) => {
      if (prev.includes(size)) {
        if (prev.length <= 1) return prev;
        return prev.filter((s) => s !== size);
      }
      return ALL_SIZE_OPTIONS.filter((s) => prev.includes(s) || s === size);
    });
  };

  const toggleMultiValue = (list: string[], value: string, setter: (next: string[]) => void) => {
    if (list.includes(value)) {
      if (list.length <= 1) return;
      setter(list.filter((item) => item !== value));
    } else {
      setter([...list, value]);
    }
  };

  const toggleCarePreset = (presetId: string) => {
    setCarePresetIds((prev) => {
      const next = prev.includes(presetId)
        ? prev.filter((id) => id !== presetId)
        : [...prev, presetId];
      const lines = CARE_STANDARD_PRESETS.filter((p) => next.includes(p.id)).map((p) => p.detail);
      if (lines.length > 0) {
        setCareText(lines.join("\n"));
      }
      return next;
    });
  };

  const handleAddColor = () => {
    const label = newColorName.trim();
    if (!label) return;
    const code = slugifyCode(label) || `mau${colors.length + 1}`;
    if (colors.some((c) => c.code === code)) {
      setErrorMsg("Màu sắc này đã tồn tại trong ma trận biến thể.");
      return;
    }
    setErrorMsg(null);
    setColors((prev) => [
      ...prev,
      {
        code,
        name: label,
        hex: newColorHex,
        image: newColorImg,
      },
    ]);
    setNewColorName("");
  };

  const handleUpdateColorImage = (code: string, imagePath: string) => {
    setColors((prev) => prev.map((c) => (c.code === code ? { ...c, image: imagePath } : c)));
  };

  const handleRemoveColor = (code: string) => {
    if (colors.length <= 1) return;
    setColors((prev) => prev.filter((c) => c.code !== code));
  };

  const handleBulkStock = () => {
    const val = Math.max(0, Math.trunc(Number(bulkStockStr) || 0));
    setSkuMap((prev) => {
      const next = { ...prev };
      for (const sku of activeSkus) {
        next[`${sku.colorCode}__${sku.size}`] = { ...sku, stock: val };
      }
      return next;
    });
    showToast(`Đã đặt tồn kho ${formatInt(val)} chiếc cho ${activeSkus.length} biến thể SKU.`);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = name.trim();
    if (!cleanName) {
      setErrorMsg("Vui lòng nhập tên sản phẩm.");
      return;
    }
    if (basePrice < 10000) {
      setErrorMsg("Giá bán phải là số nguyên tối thiểu 10.000₫.");
      return;
    }
    if (colors.length === 0 || sizes.length === 0) {
      setErrorMsg("Cần ít nhất 1 màu sắc và 1 kích cỡ để tạo biến thể SKU.");
      return;
    }
    const maxQtyPerOrder = Math.max(1, Math.trunc(Number(maxQtyStr) || 1));
    setErrorMsg(null);

    const categoryObj = CATEGORIES.find((c) => c.slug === categorySlug) ?? CATEGORIES[0];
    const templateProduct = PRODUCTS[0];
    const cleanOrig = Math.trunc(Number(origPriceStr) || 0);
    const id = existing?.id ?? `prod-custom-${Date.now()}`;
    const slug = existing?.slug ?? `${slugifyUrl(cleanName)}-${String(Date.now()).slice(-4)}`;

    const colorImages = Array.from(new Set([selectedImage, ...colors.map((c) => c.image)]));

    const savedProduct: Product = {
      id,
      slug,
      name: cleanName,
      shortName: shortName.trim() || cleanName.slice(0, 28),
      categorySlug,
      categoryName: categoryObj.name,
      shopId,
      gender,
      price: basePrice,
      originalPrice: cleanOrig > basePrice ? cleanOrig : undefined,
      image: selectedImage,
      gallery: colorImages,
      colors,
      sizes,
      skus: activeSkus.map((s) => ({
        ...s,
        skuCode: s.skuCode.trim() || `SKU-${s.colorCode.toUpperCase()}-${s.size}`,
        stock: Math.max(0, Math.trunc(s.stock)),
        price: Math.max(10000, Math.trunc(s.price)),
      })),
      fabric: fabric.trim() || materialGroup,
      gsm: Math.max(80, Math.trunc(Number(gsmStr) || 200)),
      fit: fitType,
      origin: origin.trim() || "Việt Nam",
      careGuide: careText
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean),
      highlights: existing?.highlights ?? [
        `Định lượng ${Math.trunc(Number(gsmStr) || 200)}gsm · ${materialGroup} · ${fabricFeatures.slice(0, 3).join(", ")}.`,
      ],
      description: description.trim() || cleanName,
      sizeChart: existing?.sizeChart ?? templateProduct.sizeChart,
      isFlashSale: existing?.isFlashSale,
      isNew: mode === "create" ? true : existing?.isNew,
    };

    saveSellerProduct(savedProduct, mode === "create");
    saveSellerProductMeta(id, {
      productId: id,
      categoryPath: activeTaxonomyPath,
      shopCollection: shopCollection.trim() || "Bộ sưu tập cơ bản",
      materialGroup,
      fitType,
      occasions,
      sport,
      fabricFeatures,
      modelInfo: {
        genderLabel: modelGender.trim() || "Mẫu nam",
        heightCm: Math.max(140, Math.trunc(Number(modelHeightStr) || 176)),
        weightKg: Math.max(38, Math.trunc(Number(modelWeightStr) || 70)),
        measurements: modelMeasures.trim() || "95-78-93 cm",
        wearingSize: modelSize.trim() || "L",
        secondaryModel: secondaryModel.trim() || undefined,
      },
      carePresetIds,
      productKind,
      comboSlots:
        productKind === "combo_multi_size"
          ? comboSlotsText
              .split("\n")
              .map((s) => s.trim())
              .filter(Boolean)
          : [],
      maxQtyPerOrder,
      isPreOrder,
      preOrderShipDate: isPreOrder ? preOrderDate.trim() || "28/11/2026" : undefined,
      nonReturnable,
      nonReturnReason: nonReturnable ? nonReturnReason.trim() : undefined,
      wishlistCount: initialMeta.wishlistCount,
      backInStockSubscribers: initialMeta.backInStockSubscribers,
    });

    const msg =
      mode === "create"
        ? `Đã tạo sản phẩm "${savedProduct.shortName}" (${activeSkus.length} SKU, ${fabricFeatures.length} tính năng vải).`
        : `Đã lưu cập nhật "${savedProduct.shortName}" (${formatInt(totalStock)} sản phẩm tồn kho).`;
    showToast(msg);
    setSavedNotice(msg);
  };

  return (
    <form className={common.page} onSubmit={handleSubmit}>
      <div className={common.pageHeader}>
        <div className={common.headerText}>
          <Link href="/seller/san-pham" className={styles.backLink}>
            Quay lại danh sách sản phẩm
          </Link>
          <h1 className={common.title}>
            {mode === "create" ? "Thêm sản phẩm mới" : existing?.shortName}
          </h1>
          <p className={common.finding}>
            {activeShop?.name} · {formatInt(activeSkus.length)} biến thể SKU · Tổng tồn{" "}
            {formatInt(totalStock)} chiếc ·{" "}
            {productKind === "combo_multi_size" ? "Combo đa size" : "Sản phẩm đơn / Pack"}
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
          <Button type="submit" variant="ink" size="sm" shape="app">
            <IconCheck size={14} />
            <span>{mode === "create" ? "Đăng bán sản phẩm" : "Lưu thay đổi"}</span>
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

      {/* Row 1: Basic info paired with Image preview & stock summary (matched heights, zero ragged-row) */}
      <div className={styles.topRow}>
        <section className={common.card} aria-labelledby="basic-info-heading">
          <div className={common.cardHeader}>
            <h2 id="basic-info-heading" className={common.cardTitle}>
              Phân loại sàn, BST gian hàng và giá bán
            </h2>
            <span className={common.cardMeta}>Đồng bộ trực tiếp với bộ lọc và Mega-menu</span>
          </div>

          <div className={styles.fieldStack}>
            <div className={styles.fields2}>
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
              <div className={common.field}>
                <label htmlFor="prod-short-name" className={common.fieldLabel}>
                  Tên ngắn cho bảng quản trị
                </label>
                <input
                  id="prod-short-name"
                  type="text"
                  className={common.textInput}
                  value={shortName}
                  onChange={(e) => setShortName(e.target.value)}
                  placeholder="Ví dụ: Áo thun Cotton Xanh đá"
                />
              </div>
            </div>

            <div className={styles.fields3}>
              <div className={common.field}>
                <label htmlFor="prod-category" className={common.fieldLabel}>
                  Ngành hàng chính (Cấp 1-2)
                </label>
                <select
                  id="prod-category"
                  className={common.textInput}
                  value={categorySlug}
                  onChange={(e) => {
                    setCategorySlug(e.target.value as CategorySlug);
                    setTaxonomyIndex(0);
                  }}
                >
                  {CATEGORIES.map((cat) => (
                    <option key={cat.slug} value={cat.slug}>
                      {cat.name}
                    </option>
                  ))}
                </select>
              </div>

              <div className={common.field}>
                <label htmlFor="prod-tax-l3" className={common.fieldLabel}>
                  Nhánh danh mục cấp 3 sàn
                </label>
                <select
                  id="prod-tax-l3"
                  className={common.textInput}
                  value={taxonomyIndex}
                  onChange={(e) => setTaxonomyIndex(Number(e.target.value))}
                >
                  {taxonomyOptions.map((path, idx) => (
                    <option key={path[2]} value={idx}>
                      {path[2]}
                    </option>
                  ))}
                </select>
              </div>

              <div className={common.field}>
                <label htmlFor="prod-shop-col" className={common.fieldLabel}>
                  BST riêng của gian hàng
                </label>
                <select
                  id="prod-shop-col"
                  className={common.textInput}
                  value={shopCollection}
                  onChange={(e) => setShopCollection(e.target.value)}
                >
                  {factoryProfile.collections.map((col) => (
                    <option key={col.id} value={col.name}>
                      {col.name}
                    </option>
                  ))}
                  {!factoryProfile.collections.some((c) => c.name === shopCollection) && (
                    <option value={shopCollection}>{shopCollection}</option>
                  )}
                </select>
              </div>
            </div>

            <div className={styles.taxonomyTrail} aria-label="Đường dẫn danh mục 3 cấp">
              <span>Danh mục sàn:</span>
              <span>{activeTaxonomyPath[0]}</span>
              <span>/</span>
              <span>{activeTaxonomyPath[1]}</span>
              <span>/</span>
              <span className={styles.taxonomyLeaf}>{activeTaxonomyPath[2]}</span>
              <span>·</span>
              <span>BST shop: {shopCollection}</span>
            </div>

            <div className={styles.fields3}>
              <div className={common.field}>
                <label htmlFor="prod-price" className={common.fieldLabel}>
                  Giá bán chuẩn (VND)
                </label>
                <input
                  id="prod-price"
                  type="number"
                  min={10000}
                  step={1000}
                  className={common.textInput}
                  value={priceStr}
                  onChange={(e) => setPriceStr(e.target.value)}
                />
                <span className={common.fieldHint}>Hiển thị: {formatVnd(basePrice)}</span>
              </div>

              <div className={common.field}>
                <label htmlFor="prod-orig-price" className={common.fieldLabel}>
                  Giá niêm yết gốc (tùy chọn)
                </label>
                <input
                  id="prod-orig-price"
                  type="number"
                  min={0}
                  step={1000}
                  className={common.textInput}
                  value={origPriceStr}
                  onChange={(e) => setOrigPriceStr(e.target.value)}
                  placeholder="Bỏ trống nếu không giảm"
                />
              </div>

              <div className={common.field}>
                <label htmlFor="prod-gender" className={common.fieldLabel}>
                  Đối tượng mặc
                </label>
                <select
                  id="prod-gender"
                  className={common.textInput}
                  value={gender}
                  onChange={(e) => setGender(e.target.value as GenderCode)}
                >
                  <option value="unisex">Unisex</option>
                  <option value="nam">Nam</option>
                  <option value="nu">Nữ</option>
                </select>
              </div>
            </div>

            <div className={common.field}>
              <span className={common.fieldLabel}>Loại sản phẩm</span>
              <div className={common.segmented} role="radiogroup" aria-label="Loại sản phẩm">
                <button
                  type="button"
                  role="radio"
                  aria-checked={productKind === "single_pack"}
                  className={`${common.segmentedBtn} ${productKind === "single_pack" ? common.segmentedBtnActive : ""}`}
                  onClick={() => setProductKind("single_pack")}
                >
                  Đơn / Pack
                </button>
                <button
                  type="button"
                  role="radio"
                  aria-checked={productKind === "combo_multi_size"}
                  className={`${common.segmentedBtn} ${productKind === "combo_multi_size" ? common.segmentedBtnActive : ""}`}
                  onClick={() => setProductKind("combo_multi_size")}
                >
                  Combo đa size
                </button>
              </div>
              <span className={common.fieldHint}>
                Đơn / Pack hỗ trợ thêm nhanh 1 chạm; Combo đa size tự chuyển sang trang sản phẩm để
                chọn cỡ từng món.
              </span>
            </div>

            {productKind === "combo_multi_size" && (
              <div className={styles.comboSlotBox}>
                <label htmlFor="prod-combo-slots" className={common.fieldLabel}>
                  Các món trong Combo đa size (mỗi dòng một vị trí chọn cỡ)
                </label>
                <textarea
                  id="prod-combo-slots"
                  className={common.textArea}
                  value={comboSlotsText}
                  onChange={(e) => setComboSlotsText(e.target.value)}
                />
                <span className={common.fieldHint}>
                  Khi khách bấm tại danh sách sản phẩm, thẻ tự chuyển sang Xem chi tiết để chọn màu
                  và size từng món.
                </span>
              </div>
            )}
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

          <div className={styles.summaryList}>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Tổng biến thể SKU</span>
              <span className={styles.summaryVal}>{formatInt(activeSkus.length)}</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Tổng tồn kho sẵn sàng</span>
              <span className={styles.summaryVal}>{formatInt(totalStock)} chiếc</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>SKU tồn dưới ngưỡng (≤ 5)</span>
              <span
                className={`${styles.summaryVal} ${lowSkuCount > 0 ? styles.summaryValWarn : ""}`}
              >
                {formatInt(lowSkuCount)} SKU
              </span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Khoảng giá bán</span>
              <span className={styles.summaryVal}>{priceRangeLabel}</span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Yêu thích / Chờ có hàng</span>
              <span className={styles.summaryVal}>
                {formatInt(initialMeta.wishlistCount)} / {formatInt(waitingTotal)} khách
              </span>
            </div>
            <div className={styles.summaryRow}>
              <span className={styles.summaryKey}>Phí sàn dự kiến</span>
              <span className={styles.summaryVal}>
                {((activeShop?.commissionRate ?? 0.05) * 100).toFixed(1).replace(".", ",")}%
              </span>
            </div>
          </div>
        </section>
      </div>

      {/* Row 2: Full-width Industry attributes, model measurements & care standards */}
      <section className={common.card} aria-labelledby="attributes-heading">
        <div className={common.cardHeader}>
          <h2 id="attributes-heading" className={common.cardTitle}>
            Thuộc tính vải, thông tin mẫu mặc và quy chuẩn bảo quản
          </h2>
          <span className={common.cardMeta}>Chuẩn từ điển sàn NexusMart</span>
        </div>

        <div className={styles.fieldStack}>
          <div className={styles.fields3}>
            <div className={common.field}>
              <label htmlFor="prod-mat-group" className={common.fieldLabel}>
                Nhóm chất liệu chuẩn
              </label>
              <select
                id="prod-mat-group"
                className={common.textInput}
                value={materialGroup}
                onChange={(e) => setMaterialGroup(e.target.value)}
              >
                {MATERIAL_OPTIONS.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </div>

            <div className={common.field}>
              <label htmlFor="prod-fit-type" className={common.fieldLabel}>
                Phom dáng (Fit)
              </label>
              <select
                id="prod-fit-type"
                className={common.textInput}
                value={fitType}
                onChange={(e) => setFitType(e.target.value)}
              >
                {FIT_OPTIONS.map((f) => (
                  <option key={f} value={f}>
                    {f}
                  </option>
                ))}
              </select>
            </div>

            <div className={common.field}>
              <label htmlFor="prod-sport" className={common.fieldLabel}>
                Môn thể thao
              </label>
              <select
                id="prod-sport"
                className={common.textInput}
                value={sport}
                onChange={(e) => setSport(e.target.value)}
              >
                {SPORT_OPTIONS.map((sp) => (
                  <option key={sp} value={sp}>
                    {sp}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className={styles.fields3}>
            <div className={common.field}>
              <label htmlFor="prod-fabric" className={common.fieldLabel}>
                Tỷ lệ sợi chi tiết
              </label>
              <input
                id="prod-fabric"
                type="text"
                className={common.textInput}
                value={fabric}
                onChange={(e) => setFabric(e.target.value)}
              />
            </div>
            <div className={common.field}>
              <label htmlFor="prod-gsm" className={common.fieldLabel}>
                Định lượng vải (gsm)
              </label>
              <input
                id="prod-gsm"
                type="number"
                min={60}
                max={500}
                className={common.textInput}
                value={gsmStr}
                onChange={(e) => setGsmStr(e.target.value)}
              />
            </div>
            <div className={common.field}>
              <label htmlFor="prod-origin" className={common.fieldLabel}>
                Xưởng / Nơi sản xuất
              </label>
              <input
                id="prod-origin"
                type="text"
                className={common.textInput}
                value={origin}
                onChange={(e) => setOrigin(e.target.value)}
              />
            </div>
          </div>

          <div className={common.field}>
            <span className={common.fieldLabel}>Hoàn cảnh mặc (chọn một hoặc nhiều)</span>
            <div className={styles.chipGrid}>
              {OCCASION_OPTIONS.map((occ) => {
                const active = occasions.includes(occ);
                return (
                  <button
                    key={occ}
                    type="button"
                    className={`${styles.attrToggle} ${active ? styles.attrToggleActive : ""}`}
                    aria-pressed={active}
                    onClick={() => toggleMultiValue(occasions, occ, setOccasions)}
                  >
                    {occ}
                  </button>
                );
              })}
            </div>
          </div>

          <div className={common.field}>
            <span className={common.fieldLabel}>
              Tính năng vải (hiển thị nhãn công nghệ trên trang sản phẩm và bộ lọc)
            </span>
            <div className={styles.chipGrid}>
              {FABRIC_FEATURE_OPTIONS.map((feat) => {
                const active = fabricFeatures.includes(feat);
                return (
                  <button
                    key={feat}
                    type="button"
                    className={`${styles.attrToggle} ${active ? styles.attrToggleActive : ""}`}
                    aria-pressed={active}
                    onClick={() => toggleMultiValue(fabricFeatures, feat, setFabricFeatures)}
                  >
                    {feat}
                  </button>
                );
              })}
            </div>
          </div>

          <div className={styles.fields3}>
            <div className={common.field}>
              <label htmlFor="model-gender" className={common.fieldLabel}>
                Người mẫu mặc (nhóm mẫu)
              </label>
              <input
                id="model-gender"
                type="text"
                className={common.textInput}
                value={modelGender}
                onChange={(e) => setModelGender(e.target.value)}
                placeholder="Mẫu nam & nữ (Unisex)"
              />
            </div>
            <div className={common.field}>
              <label htmlFor="model-height" className={common.fieldLabel}>
                Chiều cao mẫu (cm)
              </label>
              <input
                id="model-height"
                type="number"
                min={140}
                max={205}
                className={common.textInput}
                value={modelHeightStr}
                onChange={(e) => setModelHeightStr(e.target.value)}
              />
            </div>
            <div className={common.field}>
              <label htmlFor="model-weight" className={common.fieldLabel}>
                Cân nặng mẫu (kg)
              </label>
              <input
                id="model-weight"
                type="number"
                min={38}
                max={130}
                className={common.textInput}
                value={modelWeightStr}
                onChange={(e) => setModelWeightStr(e.target.value)}
              />
            </div>
          </div>

          <div className={styles.fields3}>
            <div className={common.field}>
              <label htmlFor="model-measures" className={common.fieldLabel}>
                Số đo 3 vòng / eo-mông
              </label>
              <input
                id="model-measures"
                type="text"
                className={common.textInput}
                value={modelMeasures}
                onChange={(e) => setModelMeasures(e.target.value)}
                placeholder="96-78-94 cm"
              />
            </div>
            <div className={common.field}>
              <label htmlFor="model-size" className={common.fieldLabel}>
                Size mẫu đang mặc
              </label>
              <select
                id="model-size"
                className={common.textInput}
                value={modelSize}
                onChange={(e) => setModelSize(e.target.value)}
              >
                {ALL_SIZE_OPTIONS.map((sz) => (
                  <option key={sz} value={sz}>
                    Size {sz}
                  </option>
                ))}
              </select>
            </div>
            <div className={common.field}>
              <label htmlFor="model-secondary" className={common.fieldLabel}>
                Mẫu phụ (cho đồ Unisex)
              </label>
              <input
                id="model-secondary"
                type="text"
                className={common.textInput}
                value={secondaryModel}
                onChange={(e) => setSecondaryModel(e.target.value)}
                placeholder="Mẫu nữ 1m64 · 52kg · mặc M"
              />
            </div>
          </div>

          <div className={common.field}>
            <span className={common.fieldLabel}>
              Bộ quy chuẩn bảo quản theo vải (chọn để tự điền hướng dẫn trên trang sản phẩm)
            </span>
            <div className={styles.careGrid}>
              {CARE_STANDARD_PRESETS.map((preset) => {
                const active = carePresetIds.includes(preset.id);
                return (
                  <button
                    key={preset.id}
                    type="button"
                    className={`${styles.careCard} ${active ? styles.careCardActive : ""}`}
                    aria-pressed={active}
                    onClick={() => toggleCarePreset(preset.id)}
                  >
                    <span className={styles.careCode}>{preset.code}</span>
                    <span className={styles.careLabel}>{preset.label}</span>
                  </button>
                );
              })}
            </div>
          </div>

          <div className={common.field}>
            <label htmlFor="prod-care" className={common.fieldLabel}>
              Hướng dẫn giặt bảo quản chi tiết (mỗi dòng một mục)
            </label>
            <textarea
              id="prod-care"
              className={common.textArea}
              value={careText}
              onChange={(e) => setCareText(e.target.value)}
            />
          </div>

          <div className={common.field}>
            <label htmlFor="prod-desc" className={common.fieldLabel}>
              Mô tả ngắn gọn
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

      {/* Card 3: SKU Variant Matrix + Per-color media/swatch binding */}
      <section className={common.card} aria-labelledby="sku-matrix-heading">
        <div className={common.cardHeader}>
          <h2 id="sku-matrix-heading" className={common.cardTitle}>
            Ma trận biến thể SKU và ảnh theo màu
          </h2>
          <span className={common.cardMeta}>
            {colors.length} màu × {sizes.length} kích cỡ = {activeSkus.length} SKU
          </span>
        </div>

        <div className={styles.variantControls}>
          <div className={styles.controlGroup}>
            <span className={styles.controlLabel}>Kích cỡ áp dụng</span>
            <div className={styles.sizeRow}>
              {ALL_SIZE_OPTIONS.map((sz) => {
                const active = sizes.includes(sz);
                return (
                  <button
                    key={sz}
                    type="button"
                    className={`${styles.sizeToggle} ${active ? styles.sizeToggleActive : ""}`}
                    onClick={() => handleToggleSize(sz)}
                    aria-pressed={active}
                  >
                    {sz}
                  </button>
                );
              })}
            </div>
          </div>

          <div className={styles.controlGroup}>
            <span className={styles.controlLabel}>
              Gắn swatch màu và ảnh đại diện theo từng màu SKU
            </span>
            <div className={styles.colorMediaList}>
              {colors.map((c) => (
                <div key={c.code} className={styles.colorMediaRow}>
                  <div className={styles.colorMediaLeft}>
                    <img
                      src={c.image}
                      alt={c.name}
                      className={styles.colorMediaThumb}
                      width={30}
                      height={40}
                    />
                    <span
                      className={styles.swatch}
                      style={{ backgroundColor: c.hex }}
                      aria-hidden="true"
                    />
                    <span>
                      <strong>{c.name}</strong> ({c.hex})
                    </span>
                  </div>
                  <div className={styles.colorMediaRight}>
                    <select
                      className={styles.colorMediaSelect}
                      value={c.image}
                      onChange={(e) => handleUpdateColorImage(c.code, e.target.value)}
                      aria-label={`Chọn ảnh đại diện cho màu ${c.name}`}
                    >
                      {GALLERY_CHOICES.map((g) => (
                        <option key={g.path} value={g.path}>
                          Ảnh: {g.label}
                        </option>
                      ))}
                    </select>
                    {colors.length > 1 && (
                      <button
                        type="button"
                        className={styles.removeColorBtn}
                        onClick={() => handleRemoveColor(c.code)}
                        aria-label={`Xóa màu ${c.name}`}
                      >
                        <IconClose size={13} />
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>

            <div className={styles.addColorRow}>
              <input
                type="text"
                className={styles.colorNameInput}
                value={newColorName}
                onChange={(e) => setNewColorName(e.target.value)}
                placeholder="Tên màu mới (vd: Rêu nhạt)"
                aria-label="Tên màu biến thể mới"
              />
              <div className={styles.swatchPicker} role="group" aria-label="Chọn sắc độ màu">
                {PRESET_SWATCHES.map((sw) => (
                  <button
                    key={sw.hex}
                    type="button"
                    className={`${styles.swatchOption} ${newColorHex === sw.hex ? styles.swatchOptionActive : ""}`}
                    style={{ backgroundColor: sw.hex }}
                    onClick={() => setNewColorHex(sw.hex)}
                    title={sw.name}
                    aria-label={`Sắc độ ${sw.name}`}
                  />
                ))}
              </div>
              <select
                className={styles.colorMediaSelect}
                value={newColorImg}
                onChange={(e) => setNewColorImg(e.target.value)}
                aria-label="Ảnh cho màu mới"
              >
                {GALLERY_CHOICES.map((g) => (
                  <option key={g.path} value={g.path}>
                    {g.label}
                  </option>
                ))}
              </select>
              <Button variant="outline" size="sm" shape="app" onClick={handleAddColor}>
                <IconPlus size={14} />
                <span>Thêm màu</span>
              </Button>
            </div>
          </div>
        </div>

        <div className={styles.bulkRow}>
          <span className={common.cardMeta}>
            Chỉnh trực tiếp mã SKU, đơn giá VND và tồn kho từng biến thể
          </span>
          <div className={styles.bulkControls}>
            <input
              type="number"
              min={0}
              className={styles.bulkInput}
              value={bulkStockStr}
              onChange={(e) => setBulkStockStr(e.target.value)}
              aria-label="Số lượng tồn kho áp dụng chung"
            />
            <Button variant="secondary" size="sm" shape="app" onClick={handleBulkStock}>
              Đặt tồn chung
            </Button>
          </div>
        </div>

        <div className={`${common.tableWrap} ${styles.skuTableDesktop}`}>
          <table className={common.dataTable}>
            <thead>
              <tr>
                <th>Màu sắc</th>
                <th>Kích cỡ</th>
                <th>Mã SKU</th>
                <th className={common.numCol}>Giá bán (VND)</th>
                <th className={common.numCol}>Tồn kho</th>
              </tr>
            </thead>
            <tbody>
              {activeSkus.map((sku) => {
                const colorObj = colors.find((c) => c.code === sku.colorCode);
                const key = `${sku.colorCode}__${sku.size}`;
                return (
                  <tr key={key}>
                    <td>
                      <span className={styles.colorTag}>
                        <span
                          className={styles.swatch}
                          style={{ backgroundColor: colorObj?.hex ?? "#9aa5ac" }}
                        />
                        <span>{colorObj?.name ?? sku.colorCode}</span>
                      </span>
                    </td>
                    <td>{sku.size}</td>
                    <td>
                      <input
                        type="text"
                        className={styles.skuCodeInput}
                        value={sku.skuCode}
                        onChange={(e) =>
                          updateSkuField(sku.colorCode, sku.size, { skuCode: e.target.value }, sku)
                        }
                        aria-label={`Mã SKU ${colorObj?.name} size ${sku.size}`}
                      />
                    </td>
                    <td className={common.numCol}>
                      <input
                        type="number"
                        min={10000}
                        step={1000}
                        className={styles.numCellInput}
                        value={sku.price}
                        onChange={(e) =>
                          updateSkuField(
                            sku.colorCode,
                            sku.size,
                            { price: Math.max(0, Math.trunc(Number(e.target.value) || 0)) },
                            sku
                          )
                        }
                        aria-label={`Giá bán ${sku.skuCode}`}
                      />
                    </td>
                    <td className={common.numCol}>
                      <input
                        type="number"
                        min={0}
                        className={`${styles.stockCellInput} ${sku.stock === 0 ? styles.stockZero : ""}`}
                        value={sku.stock}
                        onChange={(e) =>
                          updateSkuField(
                            sku.colorCode,
                            sku.size,
                            { stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)) },
                            sku
                          )
                        }
                        aria-label={`Tồn kho ${sku.skuCode}`}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        <div className={styles.mobileSkuList}>
          {activeSkus.map((sku) => {
            const colorObj = colors.find((c) => c.code === sku.colorCode);
            const key = `${sku.colorCode}__${sku.size}`;
            return (
              <div key={key} className={styles.mobileSkuItem}>
                <div className={styles.mobileSkuHead}>
                  <span>
                    {colorObj?.name ?? sku.colorCode} · Size {sku.size}
                  </span>
                  <span className={common.cardMeta}>{sku.skuCode}</span>
                </div>
                <div className={styles.mobileSkuInputs}>
                  <div className={common.field}>
                    <label className={common.fieldHint}>Giá bán (VND)</label>
                    <input
                      type="number"
                      min={10000}
                      step={1000}
                      className={styles.numCellInput}
                      value={sku.price}
                      onChange={(e) =>
                        updateSkuField(
                          sku.colorCode,
                          sku.size,
                          { price: Math.max(0, Math.trunc(Number(e.target.value) || 0)) },
                          sku
                        )
                      }
                    />
                  </div>
                  <div className={common.field}>
                    <label className={common.fieldHint}>Tồn kho</label>
                    <input
                      type="number"
                      min={0}
                      className={`${styles.stockCellInput} ${sku.stock === 0 ? styles.stockZero : ""}`}
                      value={sku.stock}
                      onChange={(e) =>
                        updateSkuField(
                          sku.colorCode,
                          sku.size,
                          { stock: Math.max(0, Math.trunc(Number(e.target.value) || 0)) },
                          sku
                        )
                      }
                    />
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </section>

      {/* Row 3: Full-width Sales limits & Pre-order/Return flags */}
      <section className={common.card} aria-labelledby="policy-limits-heading">
        <div className={common.cardHeader}>
          <h2 id="policy-limits-heading" className={common.cardTitle}>
            Hạn mức mua và trạng thái đổi trả
          </h2>
          <span className={common.cardMeta}>
            Áp dụng đồng bộ trên trang sản phẩm, giỏ hàng và bước thanh toán
          </span>
        </div>

        <div className={styles.policyGrid}>
          <div className={styles.fieldStack}>
            <div className={common.field}>
              <label htmlFor="prod-max-qty" className={common.fieldLabel}>
                Hạn mức tối đa mỗi đơn (sản phẩm)
              </label>
              <input
                id="prod-max-qty"
                type="number"
                min={1}
                max={50}
                className={common.textInput}
                value={maxQtyStr}
                onChange={(e) => setMaxQtyStr(e.target.value)}
              />
              <span className={common.fieldHint}>
                Giới hạn chống gom hàng, tự động đối chiếu theo tồn kho thực tế.
              </span>
            </div>

            {isPreOrder && (
              <div className={common.field}>
                <label htmlFor="prod-preorder-date" className={common.fieldLabel}>
                  Ngày dự kiến bàn giao ĐVVC
                </label>
                <input
                  id="prod-preorder-date"
                  type="text"
                  className={common.textInput}
                  value={preOrderDate}
                  onChange={(e) => setPreOrderDate(e.target.value)}
                  placeholder="28/11/2026"
                />
              </div>
            )}

            {nonReturnable && (
              <div className={common.field}>
                <label htmlFor="prod-nonreturn-reason" className={common.fieldLabel}>
                  Lý do hiển thị trên trang sản phẩm
                </label>
                <input
                  id="prod-nonreturn-reason"
                  type="text"
                  className={common.textInput}
                  value={nonReturnReason}
                  onChange={(e) => setNonReturnReason(e.target.value)}
                />
              </div>
            )}
          </div>

          <div>
            <div className={styles.switchRow}>
              <div className={styles.switchText}>
                <span className={styles.switchTitle}>Trạng thái Pre-order (Đặt trước)</span>
                <span className={styles.switchDesc}>
                  Hiển thị nhãn Đặt trước và ngày dự kiến xuất xưởng (cần Admin duyệt nhãn).
                </span>
              </div>
              <input
                type="checkbox"
                className={common.toggleSwitch}
                checked={isPreOrder}
                onChange={(e) => setIsPreOrder(e.target.checked)}
                aria-label="Bật trạng thái Pre-order"
              />
            </div>

            <div className={styles.switchRow}>
              <div className={styles.switchText}>
                <span className={styles.switchTitle}>Không áp dụng đổi trả 45 ngày</span>
                <span className={styles.switchDesc}>
                  Bật cho hàng Outlet, deal sốc hoặc pack đồ lót niêm phong theo ngoại lệ sàn.
                </span>
              </div>
              <input
                type="checkbox"
                className={common.toggleSwitch}
                checked={nonReturnable}
                onChange={(e) => setNonReturnable(e.target.checked)}
                aria-label="Đánh dấu sản phẩm không áp dụng đổi trả"
              />
            </div>
          </div>
        </div>
      </section>
    </form>
  );
}
