"use client";

import Link from "next/link";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { SiteSearchBar } from "@/features/search/SiteSearchBar";

export interface SiteSearchModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  brandName: string;
  shopId?: string;
  showSearchSlot: boolean;
}

export function SiteSearchModal({
  open,
  onOpenChange,
  brandName,
  shopId,
  showSearchSlot,
}: SiteSearchModalProps) {
  const close = () => onOpenChange(false);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="top-[18%] translate-y-0 gap-5 rounded-xl border border-border bg-card p-6 text-card-foreground sm:max-w-2xl"
        aria-label={`Tìm kiếm sản phẩm tại ${brandName}`}
      >
        <DialogHeader className="text-left">
          <DialogTitle className="text-xl font-semibold tracking-tight text-foreground">
            Tìm trong {brandName}
          </DialogTitle>
          <DialogDescription className="text-sm text-muted-foreground">
            Nhập tên mẫu, chất liệu hoặc màu sắc cần tìm.
          </DialogDescription>
        </DialogHeader>

        {showSearchSlot ? (
          <SiteSearchBar
            brandName={brandName}
            shopId={shopId}
            placeholder="Tên mẫu, chất liệu, màu sắc..."
            onNavigate={close}
          />
        ) : (
          <form
            action="/tim-kiem"
            method="get"
            role="search"
            className="flex gap-2"
            onSubmit={close}
          >
            <label htmlFor="site-overlay-search" className="sr-only">
              Tìm sản phẩm trong {brandName}
            </label>
            <Input
              id="site-overlay-search"
              name="q"
              type="search"
              placeholder="Tên mẫu, chất liệu..."
              className="min-h-11 flex-1"
              enterKeyHint="search"
              autoFocus
            />
            <Button type="submit" className="min-h-11 px-5 shadow-none">
              Tìm
            </Button>
          </form>
        )}

        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-border pt-3 text-sm">
          <Link
            href="/san-pham"
            onClick={close}
            className="py-1 font-medium text-muted-foreground transition-colors hover:text-brand"
          >
            Tất cả sản phẩm
          </Link>
          <Link
            href="/flash-sale"
            onClick={close}
            className="py-1 font-medium text-muted-foreground transition-colors hover:text-brand"
          >
            Flash sale
          </Link>
          <Link
            href="/voucher"
            onClick={close}
            className="py-1 font-medium text-muted-foreground transition-colors hover:text-brand"
          >
            Mã giảm giá
          </Link>
        </div>
      </DialogContent>
    </Dialog>
  );
}
