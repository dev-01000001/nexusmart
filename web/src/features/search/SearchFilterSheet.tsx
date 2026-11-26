"use client";

import React from "react";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { formatInt } from "@/lib/format";
import { SearchFilterControls, type SearchFilterControlsProps } from "./SearchFilterControls";

export interface SearchFilterSheetProps extends Omit<SearchFilterControlsProps, "idPrefix"> {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sheetFilterCount: number;
  totalResults: number;
  onResetSheetFilters: () => void;
}

export function SearchFilterSheet({
  open,
  onOpenChange,
  sheetFilterCount,
  totalResults,
  onResetSheetFilters,
  ...controls
}: SearchFilterSheetProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="z-[75] w-[min(380px,92vw)] gap-0 p-0 sm:max-w-sm"
        aria-label="Bộ lọc kết quả tìm kiếm"
      >
        <SheetHeader className="border-b border-border bg-muted/40 px-5 py-4">
          <SheetTitle className="text-base font-semibold">Bộ lọc</SheetTitle>
          <SheetDescription className="sr-only">
            Chọn danh mục và khoảng giá để thu hẹp kết quả tìm kiếm.
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto px-5 py-5">
          <SearchFilterControls idPrefix="search-sheet" {...controls} />
        </div>

        <SheetFooter className="flex-row justify-end gap-2 border-t border-border bg-muted/40 px-5 py-4">
          {sheetFilterCount > 0 && (
            <Button
              type="button"
              variant="outline"
              className="min-h-10 shadow-none"
              onClick={() => {
                onResetSheetFilters();
                onOpenChange(false);
              }}
            >
              Xóa lọc
            </Button>
          )}
          <Button
            type="button"
            className="min-h-10 shadow-none"
            onClick={() => onOpenChange(false)}
          >
            Xem {formatInt(totalResults)} kết quả
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
