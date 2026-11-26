"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Bookmark, BookmarkCheck, Check, Copy, Loader2, ShoppingBag, Ticket } from "lucide-react";
import type { Voucher } from "@/data/vouchers";
import { useStore } from "@/context/StoreContext";
import { formatDateVi, formatVnd } from "@/lib/format";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Badge } from "@/components/ui/badge";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { SitePageFrame, SitePageHeader } from "@/features/commerce/ui/SitePageFrame";

export interface VoucherClientProps {
  brandName: string;
  vouchers: Voucher[];
  loadError: string | null;
}

export function VoucherClient({ brandName, vouchers, loadError }: VoucherClientProps) {
  const router = useRouter();
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

  if (loadError) {
    return (
      <SitePageFrame>
        <SitePageHeader
          title={`Mã giảm giá ${brandName}`}
          subtitle={`Lưu ưu đãi riêng của ${brandName} và áp dụng ngay khi thanh toán.`}
        />
        <ErrorState
          title="Không thể tải mã ưu đãi lúc này"
          description={loadError}
          onRetry={() => router.refresh()}
          className="border border-border shadow-none"
        />
      </SitePageFrame>
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
    <SitePageFrame className="space-y-6">
      <Breadcrumb aria-label="Đường dẫn">
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink asChild>
              <Link href="/">Trang chủ</Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage>Mã giảm giá</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <SitePageHeader
        title={`Mã giảm giá ${brandName}`}
        subtitle={`Lưu ưu đãi riêng của ${brandName} và áp dụng ngay khi thanh toán.`}
        className="mb-0"
        action={
          <>
            <Button asChild className="min-h-10 shadow-none">
              <Link href="/gio-hang">
                <ShoppingBag className="size-4" aria-hidden="true" />
                <span>Mở giỏ hàng</span>
              </Link>
            </Button>
            <Button asChild variant="outline" className="min-h-10 shadow-none">
              <Link href="/san-pham">Xem sản phẩm</Link>
            </Button>
          </>
        }
      />

      <section className="space-y-4" aria-labelledby="voucher-catalog-heading">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 id="voucher-catalog-heading" className="text-lg font-semibold text-foreground">
            Ưu đãi đang phát hành (<span className="tabular-nums">{shopVouchers.length}</span>)
          </h2>
        </div>

        {shopVouchers.length === 0 ? (
          <EmptyState
            icon={Ticket}
            title={`${brandName} chưa có mã giảm giá mới`}
            description="Xem toàn bộ sản phẩm đang bán hoặc quay lại vào đợt ưu đãi tới."
            className="border border-border shadow-none"
            action={
              <Button asChild className="min-h-10 shadow-none">
                <Link href="/san-pham">Xem sản phẩm</Link>
              </Button>
            }
          />
        ) : (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {shopVouchers.map((v) => {
              const isSaved = Boolean(savedKeys[v.code] || (v.id && savedKeys[v.id]));
              const applied = isApplied(v);
              const exhausted = v.remainingUses <= 0;
              const usedRatio =
                v.totalUses > 0
                  ? Math.min(100, Math.round(((v.totalUses - v.remainingUses) / v.totalUses) * 100))
                  : 100;

              return (
                <Card
                  key={v.id ?? v.code}
                  className="grid grid-cols-1 gap-0 overflow-hidden border border-border py-0 shadow-none sm:grid-cols-[172px_minmax(0,1fr)]"
                >
                  {/* Ticket stub */}
                  <div className="flex flex-col items-start justify-between gap-2 border-b border-dashed border-border bg-muted/45 p-4 sm:border-r sm:border-b-0">
                    <div className="space-y-2">
                      <span className="block text-2xl leading-none font-bold tracking-tight text-foreground tabular-nums">
                        {v.discountType === "percent"
                          ? `-${v.discountValue}%`
                          : `-${formatVnd(v.discountValue)}`}
                      </span>
                      <Badge
                        variant="outline"
                        className="rounded-sm border-border bg-card px-2 py-0.5 font-mono text-xs font-semibold tracking-wide text-foreground"
                      >
                        {v.code}
                      </Badge>
                    </div>
                    <span className="line-clamp-1 text-xs text-muted-foreground">{brandName}</span>
                  </div>

                  {/* Ticket body + actions */}
                  <div className="flex min-w-0 flex-col justify-between gap-4 p-4">
                    <div className="space-y-3">
                      <div className="space-y-1">
                        <h3 className="text-base leading-snug font-semibold text-foreground">
                          {v.title}
                        </h3>
                        {v.description ? (
                          <p className="text-xs leading-relaxed text-muted-foreground">
                            {v.description}
                          </p>
                        ) : null}
                      </div>

                      <div className="flex flex-wrap items-center gap-1.5">
                        <Badge variant="secondary" className="rounded-sm font-normal tabular-nums">
                          Đơn từ {formatVnd(v.minOrderValue)}
                        </Badge>
                        {v.maxDiscount ? (
                          <Badge
                            variant="secondary"
                            className="rounded-sm font-normal tabular-nums"
                          >
                            Tối đa {formatVnd(v.maxDiscount)}
                          </Badge>
                        ) : null}
                        {v.expiresAt ? (
                          <Badge variant="outline" className="rounded-sm font-normal tabular-nums">
                            HSD {formatDateVi(v.expiresAt)}
                          </Badge>
                        ) : null}
                      </div>

                      <div className="space-y-1.5">
                        <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
                          <span>
                            Còn{" "}
                            <strong className="font-semibold text-foreground">
                              {v.remainingUses}
                            </strong>{" "}
                            / {v.totalUses} lượt
                          </span>
                          <span>Đã dùng {usedRatio}%</span>
                        </div>
                        <Progress
                          value={usedRatio}
                          aria-label={`Lượt còn lại của mã ${v.code}`}
                          className="h-2"
                        />
                      </div>
                    </div>

                    <div className="space-y-2 pt-1">
                      <Button
                        type="button"
                        variant={applied ? "secondary" : "default"}
                        className="min-h-10 w-full shadow-none"
                        disabled={exhausted || savingCode === v.code}
                        onClick={() => void handleApplyToCart(v)}
                      >
                        {exhausted ? "Đã hết lượt" : applied ? "Đã chọn cho giỏ" : "Dùng cho giỏ"}
                      </Button>

                      <div className="grid grid-cols-2 gap-2">
                        <Button
                          type="button"
                          variant={isSaved ? "secondary" : "outline"}
                          size="sm"
                          className="min-h-10 shadow-none sm:min-h-9"
                          disabled={exhausted || isSaved || savingCode === v.code}
                          onClick={() => void handleSave(v)}
                        >
                          {savingCode === v.code ? (
                            <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                          ) : isSaved ? (
                            <BookmarkCheck className="size-3.5" aria-hidden="true" />
                          ) : (
                            <Bookmark className="size-3.5" aria-hidden="true" />
                          )}
                          <span>
                            {isSaved ? "Đã lưu" : savingCode === v.code ? "Đang lưu..." : "Lưu mã"}
                          </span>
                        </Button>

                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          className="min-h-10 shadow-none sm:min-h-9"
                          onClick={() => handleCopy(v.code)}
                        >
                          {copiedCode === v.code ? (
                            <Check className="size-3.5" aria-hidden="true" />
                          ) : (
                            <Copy className="size-3.5" aria-hidden="true" />
                          )}
                          <span>{copiedCode === v.code ? "Đã chép" : "Sao chép"}</span>
                        </Button>
                      </div>
                    </div>
                  </div>
                </Card>
              );
            })}
          </div>
        )}
      </section>
    </SitePageFrame>
  );
}
