"use client";

import React, { useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { Shop, Voucher, VoucherScope } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import { formatDateVi, formatVnd } from "@/lib/format";
import styles from "./page.module.css";

export interface VoucherClientProps {
  vouchers: Voucher[];
  shops: Shop[];
  loadError: string | null;
}

type ScopeFilter = "all" | VoucherScope;

const SCOPE_TABS: Array<{ id: ScopeFilter; label: string }> = [
  { id: "all", label: "Tất cả mã" },
  { id: "platform", label: "Mã toàn sàn" },
  { id: "freeship", label: "Miễn phí vận chuyển" },
  { id: "shop", label: "Mã gian hàng" },
];

export function VoucherClient({ vouchers, shops, loadError }: VoucherClientProps) {
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);
  const {
    saveVoucher,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    selectedShopVouchers,
    platformVoucherId,
    freeshipVoucherId,
    showToast,
  } = useStore();

  const [scope, setScope] = useState<ScopeFilter>("all");
  const [copiedCode, setCopiedCode] = useState<string | null>(null);
  const [savingCode, setSavingCode] = useState<string | null>(null);
  const [savedKeys, setSavedKeys] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {};
    for (const v of vouchers) {
      if (v.saved) {
        init[v.code] = true;
        if (v.id) init[v.id] = true;
      }
    }
    return init;
  });

  const shopById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

  const filtered = useMemo(() => {
    if (scope === "all") return vouchers;
    return vouchers.filter((v) => v.scope === scope);
  }, [scope, vouchers]);

  useRevealObserver(rootRef, scope);

  if (loadError) {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải kho mã ưu đãi lúc này"
          message={loadError}
          actionLabel="Tải lại trang"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const handleSave = async (v: Voucher) => {
    if (savingCode) return;
    const idOrCode = v.id ?? v.code;
    setSavingCode(v.code);
    try {
      const res = await saveVoucher(idOrCode);
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent("/voucher")}`);
        return;
      }
      if (res.ok) {
        setSavedKeys((prev) => ({
          ...prev,
          [v.code]: true,
          ...(v.id ? { [v.id]: true } : {}),
        }));
      }
    } finally {
      setSavingCode(null);
    }
  };

  const handleApplyToCart = async (v: Voucher) => {
    const idOrCode = v.id ?? v.code;
    const isAlreadySaved = Boolean(savedKeys[v.code] || (v.id && savedKeys[v.id]));
    if (!isAlreadySaved) {
      const res = await saveVoucher(idOrCode);
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent("/voucher")}`);
        return;
      }
      if (res.ok) {
        setSavedKeys((prev) => ({
          ...prev,
          [v.code]: true,
          ...(v.id ? { [v.id]: true } : {}),
        }));
      }
    }
    if (v.scope === "shop" && v.shopId) {
      const shop = shopById.get(v.shopId);
      setShopVoucher(shop?.backendId ?? v.shopId, idOrCode);
    } else if (v.scope === "freeship") {
      setFreeshipVoucher(idOrCode);
    } else {
      setPlatformVoucher(idOrCode);
    }
    showToast(`Đã chọn mã ${v.code} cho giỏ hàng hiện tại.`);
  };

  const handleCopy = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    setCopiedCode(code);
    showToast(`Đã sao chép mã ${code}.`);
  };

  const isApplied = (v: Voucher): boolean => {
    const idOrCode = v.id ?? v.code;
    if (v.scope === "platform") {
      return platformVoucherId === idOrCode || platformVoucherId === v.code;
    }
    if (v.scope === "freeship") {
      return freeshipVoucherId === idOrCode || freeshipVoucherId === v.code;
    }
    if (v.scope === "shop" && v.shopId) {
      const shop = shopById.get(v.shopId);
      const key = shop?.backendId ?? v.shopId;
      return selectedShopVouchers[key] === idOrCode || selectedShopVouchers[key] === v.code;
    }
    return false;
  };

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>Kho mã ưu đãi</span>
      </nav>

      <section className={styles.heroBand} aria-label="Tổng quan kho voucher">
        <div className={styles.heroText}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Kho mã ưu đãi NexusMart
              </span>
            </span>
          </h1>
          <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Lưu trước mã giảm giá toàn sàn, ưu đãi vận chuyển và mã riêng từ từng xưởng dệt. Bạn có
            thể kết hợp đồng thời 1 mã xưởng, 1 mã sàn và 1 mã freeship khi đặt hàng.
          </p>
        </div>
        <div
          className={styles.heroActions}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <Button variant="primary" size="lg" href="/gio-hang">
            Mở giỏ hàng áp mã
          </Button>
          <Button variant="secondary" size="lg" href="/flash-sale">
            Xem Flash sale hôm nay
          </Button>
        </div>
      </section>

      <section className={styles.listSection} aria-labelledby="voucher-catalog-heading">
        <div className={styles.toolbar}>
          <h2 id="voucher-catalog-heading" className={styles.sectionTitle}>
            Danh sách mã đang phát hành ({filtered.length})
          </h2>
          <div className={styles.tabs} role="tablist" aria-label="Lọc phạm vi mã ưu đãi">
            {SCOPE_TABS.map((tab) => {
              const active = scope === tab.id;
              const count =
                tab.id === "all"
                  ? vouchers.length
                  : vouchers.filter((v) => v.scope === tab.id).length;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  className={`${styles.tabBtn} ${active ? styles.tabBtnActive : ""}`}
                  onClick={() => setScope(tab.id)}
                >
                  {tab.label} ({count})
                </button>
              );
            })}
          </div>
        </div>

        {filtered.length === 0 ? (
          <StateView
            kind="empty"
            title="Hiện chưa có mã ưu đãi trong nhóm này"
            message="Bạn có thể xem toàn bộ sản phẩm giá xưởng hoặc quay lại sau khi đợt mã mới mở."
            actionLabel="Xem toàn bộ danh mục"
            actionHref="/danh-muc"
          />
        ) : (
          <div className={styles.voucherGrid}>
            {filtered.map((v, idx) => {
              const shop = v.shopId ? shopById.get(v.shopId) : undefined;
              const isSaved = Boolean(savedKeys[v.code] || (v.id && savedKeys[v.id]));
              const applied = isApplied(v);
              const exhausted = v.remainingUses <= 0;
              const usedRatio =
                v.totalUses > 0
                  ? Math.min(100, Math.round(((v.totalUses - v.remainingUses) / v.totalUses) * 100))
                  : 100;
              const scopeLabel =
                v.scope === "platform"
                  ? "Toàn sàn NexusMart"
                  : v.scope === "freeship"
                    ? "Miễn phí vận chuyển"
                    : (shop?.name ?? "Mã gian hàng");

              return (
                <article
                  key={v.id ?? v.code}
                  className={styles.voucherCard}
                  data-cat-reveal=""
                  style={{ "--stagger": idx % 2 } as React.CSSProperties}
                >
                  <div className={styles.voucherHeader}>
                    <div className={styles.codeGroup}>
                      <span className={styles.codeBadge}>{v.code}</span>
                      <span className={styles.scopeLabel}>{scopeLabel}</span>
                    </div>
                    <span className={`${styles.discountSummary} num`}>
                      {v.discountType === "percent"
                        ? `Giảm ${v.discountValue}%`
                        : `Giảm ${formatVnd(v.discountValue)}`}
                    </span>
                  </div>

                  <h3 className={styles.voucherTitle}>{v.title}</h3>
                  <p className={styles.voucherDesc}>{v.description}</p>

                  <div className={styles.metaRow}>
                    <span>Đơn tối thiểu {formatVnd(v.minOrderValue)}</span>
                    {v.maxDiscount ? <span>Giảm tối đa {formatVnd(v.maxDiscount)}</span> : null}
                    {v.expiresAt ? <span>HSD {formatDateVi(v.expiresAt)}</span> : null}
                  </div>

                  <div className={styles.quotaWrap}>
                    <div className={styles.quotaTop}>
                      <span>
                        Còn <strong className="num">{v.remainingUses}</strong> / {v.totalUses} lượt
                      </span>
                      <span className="num">Đã dùng {usedRatio}%</span>
                    </div>
                    <div
                      className={styles.quotaTrack}
                      role="progressbar"
                      aria-valuemin={0}
                      aria-valuemax={v.totalUses}
                      aria-valuenow={v.totalUses - v.remainingUses}
                      aria-label={`Lượt còn lại của mã ${v.code}`}
                    >
                      <div className={styles.quotaFill} style={{ width: `${usedRatio}%` }} />
                    </div>
                  </div>

                  <div className={styles.actions}>
                    <Button
                      variant={applied ? "ink" : "primary"}
                      size="sm"
                      disabled={exhausted || savingCode === v.code}
                      onClick={() => handleApplyToCart(v)}
                    >
                      {exhausted ? "Đã hết lượt" : applied ? "Đã chọn cho giỏ" : "Dùng cho giỏ"}
                    </Button>
                    <Button
                      variant={isSaved ? "ink" : "secondary"}
                      size="sm"
                      disabled={exhausted || isSaved || savingCode === v.code}
                      onClick={() => handleSave(v)}
                    >
                      {isSaved ? "Đã lưu ví" : savingCode === v.code ? "Đang lưu..." : "Lưu mã"}
                    </Button>
                    <Button variant="secondary" size="sm" onClick={() => handleCopy(v.code)}>
                      {copiedCode === v.code ? "Đã chép" : "Sao chép"}
                    </Button>
                    {shop && (
                      <Link href={`/gian-hang/${shop.slug}`} className={styles.shopLink}>
                        Xem {shop.shortName}
                      </Link>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
