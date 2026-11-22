"use client";

import React, { useMemo, useState } from "react";
import type { Product, ProductSku, SizeCode } from "@/data";
import type { CatalogProduct, CatalogSku } from "@/lib/catalog-types";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconCheck } from "@/components";
import styles from "./PdpBackInStock.module.css";

export interface PdpBackInStockProps {
  product: Product | CatalogProduct;
  activeSku: (ProductSku | CatalogSku) | undefined;
  colorName: string;
  size: SizeCode;
  isOpen: boolean;
  onClose?: () => void;
}

export function PdpBackInStock({
  product,
  activeSku,
  colorName,
  size,
  isOpen,
  onClose,
}: PdpBackInStockProps) {
  const { user, isAuthenticated } = useSession();
  const { subscribeBackInStock, hasBackInStockAlert, backInStockAlerts } = useMockStore();

  const outOfStockSkus = useMemo(() => product.skus.filter((s) => s.stock <= 0), [product.skus]);

  const [pickedSkuCode, setPickedSkuCode] = useState<string>("");
  const [contact, setContact] = useState<string>(() =>
    isAuthenticated && user ? user.phone || user.email : ""
  );
  const [error, setError] = useState<string>("");

  if (!isOpen) return null;

  const isActiveOut = !activeSku || activeSku.stock <= 0;
  const fallbackSku =
    (pickedSkuCode && product.skus.find((s) => s.skuCode === pickedSkuCode)) ||
    outOfStockSkus[0] ||
    activeSku;
  const targetSku = isActiveOut ? activeSku : fallbackSku;

  if (!targetSku) return null;

  const targetColorName =
    product.colors.find((c) => c.code === targetSku.colorCode)?.name ?? colorName;
  const targetSize = targetSku.size ?? size;
  const alreadySubscribed = hasBackInStockAlert(targetSku.skuCode);
  const existingAlert = backInStockAlerts.find((a) => a.skuCode === targetSku.skuCode);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const cleaned = contact.trim();
    const digits = cleaned.replace(/\D/g, "");
    const looksLikeEmail = cleaned.includes("@") && cleaned.includes(".");
    const looksLikePhone = digits.length >= 9 && digits.length <= 12;

    if (!looksLikeEmail && !looksLikePhone) {
      setError("Nhập số điện thoại (9-12 số) hoặc email hợp lệ để nhận thông báo.");
      return;
    }

    setError("");
    subscribeBackInStock({
      productId: product.id,
      skuCode: targetSku.skuCode,
      colorName: targetColorName,
      size: targetSize,
      contact: cleaned,
    });
  };

  return (
    <div className={styles.box} aria-live="polite">
      <div className={styles.head}>
        <p className={styles.title}>
          {isActiveOut ? "Đăng ký báo khi mẫu này có hàng lại" : "Đăng ký nhận báo khi có hàng"}
        </p>
        {onClose && !isActiveOut && (
          <button type="button" className={styles.closeText} onClick={onClose}>
            Thu gọn
          </button>
        )}
      </div>

      {!isActiveOut && outOfStockSkus.length > 0 && (
        <div className={styles.skuChips} role="group" aria-label="Chọn phân loại đang hết hàng">
          {outOfStockSkus.map((sku) => {
            const cName =
              product.colors.find((c) => c.code === sku.colorCode)?.name ?? sku.colorCode;
            const active = sku.skuCode === targetSku.skuCode;
            return (
              <button
                key={sku.skuCode}
                type="button"
                className={`${styles.skuChip} ${active ? styles.skuChipActive : ""}`}
                onClick={() => setPickedSkuCode(sku.skuCode)}
                aria-pressed={active}
              >
                {cName} · Size {sku.size}
              </button>
            );
          })}
        </div>
      )}

      <p className={styles.meta}>
        Phân loại theo dõi:{" "}
        <strong>
          {targetColorName} · Size {targetSize}
        </strong>
      </p>

      {alreadySubscribed ? (
        <div className={styles.confirmed}>
          <span className={styles.confirmedIcon}>
            <IconCheck size={15} />
          </span>
          <div className={styles.confirmedText}>
            <p className={styles.confirmedTitle}>
              Đã đăng ký báo có hàng cho {targetColorName} · Size {targetSize}
            </p>
            <p className={styles.confirmedDesc}>
              Thông báo sẽ gửi tới <strong>{existingAlert?.contact ?? contact}</strong> ngay khi
              xưởng có hàng trở lại.
            </p>
          </div>
        </div>
      ) : (
        <form className={styles.form} onSubmit={handleSubmit} noValidate>
          <label htmlFor={`bis-input-${targetSku.skuCode}`} className="sr-only">
            Số điện thoại hoặc email nhận báo có hàng
          </label>
          <input
            id={`bis-input-${targetSku.skuCode}`}
            type="text"
            value={contact}
            onChange={(e) => {
              setContact(e.target.value);
              if (error) setError("");
            }}
            placeholder="Nhập SĐT hoặc email nhận tin..."
            className={styles.input}
          />
          <Button type="submit" variant="ink" size="md">
            Báo tôi khi có hàng
          </Button>
        </form>
      )}

      {error && <p className={styles.errorText}>{error}</p>}
    </div>
  );
}
