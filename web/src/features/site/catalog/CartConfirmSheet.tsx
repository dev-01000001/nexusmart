"use client";

import React from "react";
import Link from "next/link";
import { CheckCircle2 } from "lucide-react";
import { Money } from "@/components/app/Money";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";

export interface CartConfirmSheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  brandName: string;
  productName: string;
  imageSrc: string;
  variantLabel: string;
  subtotalVnd: number;
  addedNotice: string | null;
}

export function CartConfirmSheet({
  open,
  onOpenChange,
  brandName,
  productName,
  imageSrc,
  variantLabel,
  subtotalVnd,
  addedNotice,
}: CartConfirmSheetProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="z-[75] w-[min(400px,92vw)] gap-0 p-0 sm:max-w-sm"
        aria-label="Đã thêm vào giỏ hàng"
      >
        <SheetHeader className="border-b border-border bg-muted/40 px-5 py-4">
          <SheetTitle className="flex items-center gap-2 text-base font-semibold text-brand">
            <CheckCircle2 className="size-5 shrink-0" aria-hidden="true" />
            <span>Đã thêm vào giỏ</span>
          </SheetTitle>
          <SheetDescription className="text-xs text-muted-foreground">{brandName}</SheetDescription>
        </SheetHeader>

        <div className="flex flex-1 flex-col gap-4 overflow-y-auto px-5 py-5">
          <div className="flex items-start gap-3.5 rounded-lg border border-border bg-background p-3.5">
            <img
              src={imageSrc}
              alt={productName}
              width={72}
              height={96}
              className="h-24 w-[72px] shrink-0 rounded-md bg-muted object-cover"
            />
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <p className="line-clamp-2 text-[15px] leading-snug font-semibold text-foreground">
                {productName}
              </p>
              {variantLabel && <p className="text-[13px] text-muted-foreground">{variantLabel}</p>}
              <div className="pt-1">
                <Money vnd={subtotalVnd} size="md" />
              </div>
            </div>
          </div>

          {addedNotice && (
            <p className="text-[13px] leading-5 text-muted-foreground" role="status">
              {addedNotice}
            </p>
          )}

          <div className="mt-auto space-y-3 pt-2">
            <Separator />
            <div className="flex items-baseline justify-between gap-3">
              <span className="text-sm text-muted-foreground">Tạm tính</span>
              <Money vnd={subtotalVnd} size="lg" />
            </div>
          </div>
        </div>

        <SheetFooter className="flex-col gap-2.5 border-t border-border bg-muted/40 px-5 py-4">
          <Button
            className="min-h-11 w-full font-semibold shadow-none"
            asChild
            onClick={() => onOpenChange(false)}
          >
            <Link href="/gio-hang">Xem giỏ hàng</Link>
          </Button>
          <Button
            variant="outline"
            className="min-h-11 w-full font-semibold shadow-none"
            asChild
            onClick={() => onOpenChange(false)}
          >
            <Link href="/thanh-toan">Thanh toán</Link>
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="min-h-10 w-full"
            onClick={() => onOpenChange(false)}
          >
            Tiếp tục xem
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
