"use client";

import React, { useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { Voucher } from "@/data/vouchers";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import { formatDateVi, formatVnd } from "@/lib/format";
import styles from "./page.module.css";

export interface VoucherClientProps {
  brandName: string;
  vouchers: Voucher[];
  loadError: string | null;
}

export function VoucherClient({ brandName, vouchers, loadError }: VoucherClientProps) {
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);
  const { saveVoucher, setSelectedVoucher, selectedVoucherId, showToast } = useStore();

  // Defense in depth: strictly render SHOP vouchers only.
  const shopVouchers = useMemo(() => vouchers.filter((v) => v.scope === "shop"), [vouchers]);

  const [copiedCode, setCopiedCode] = useState<string | null>(null);
  const [savingCode, setSavingCode] = useState<string | null>(null);
  const [savedKeys, setSavedKeys] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {};
    for (const v of shopVouchers) {
      if (v.saved) {
        init[v.code] = true;
        if (v.id) init[v.id] = true;
      }
    }
    return init;
  });

  useRevealObserver(rootRef, String(shopVouchers.length));

  if (loadError) {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải mã ưu đãi lúc này"
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
    setSelectedVoucher(idOrCode);
    showToast(`Đã chọn mã ${v.code} cho giỏ hàng tại ${brandName}.`);
    router.push("/gio-hang");
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
    return selectedVoucherId === idOrCode || selectedVoucherId === v.code;
  };

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>Mã giảm giá</span>
      </nav>

      <section className={styles.heroBand} aria-label={`Mã giảm giá của ${brandName}`}>
        <div className={styles.heroText}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Mã giảm giá · {brandName}
              </span>
            </span>
          </h1>
          <p className={styles.lead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            Lưu trước ưu đãi riêng của {brandName} và áp dụng trực tiếp khi kiểm tra giỏ hàng hoặc
            thanh toán trên website này.
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
          <Button variant="secondary" size="lg" href="/san-pham">
            Xem sản phẩm cửa hàng
          </Button>
        </div>
      </section>

      <section className={styles.listSection} aria-labelledby="voucher-catalog-heading">
        <div className={styles.toolbar}>
          <h2 id="voucher-catalog-heading" className={styles.sectionTitle}>
            Ưu đãi đang phát hành ({shopVouchers.length})
          </h2>
        </div>

        {shopVouchers.length === 0 ? (
          <StateView
            kind="empty"
            title={`${brandName} hiện chưa có mã giảm giá mới`}
            message="Bạn có thể xem toàn bộ sản phẩm đang bán hoặc quay lại sau khi đợt ưu đãi tiếp theo mở."
            actionLabel="Xem sản phẩm"
            actionHref="/san-pham"
          />
        ) : (
          <div className={styles.voucherGrid}>
            {shopVouchers.map((v, idx) => {
              const isSaved = Boolean(savedKeys[v.code] || (v.id && savedKeys[v.id]));
              const applied = isApplied(v);
              const exhausted = v.remainingUses <= 0;
              const usedRatio =
                v.totalUses > 0
                  ? Math.min(100, Math.round(((v.totalUses - v.remainingUses) / v.totalUses) * 100))
                  : 100;

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
                      <span className={styles.scopeLabel}>{brandName}</span>
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
                      {isSaved ? "Đã lưu" : savingCode === v.code ? "Đang lưu..." : "Lưu mã"}
                    </Button>
                    <Button variant="secondary" size="sm" onClick={() => handleCopy(v.code)}>
                      {copiedCode === v.code ? "Đã chép" : "Sao chép"}
                    </Button>
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
