"use client";

import React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";
import { SEARCH_PRICE_PRESETS, type SearchFacetBucket } from "./searchTypes";

export interface SearchFilterControlsProps {
  idPrefix: string;
  categoryFacets: SearchFacetBucket[];
  category: string;
  onSelectCategory: (next: string) => void;
  activePresetId: string;
  onSelectPricePreset: (min?: number, max?: number) => void;
  minPriceInput: string;
  maxPriceInput: string;
  onMinPriceInputChange: (val: string) => void;
  onMaxPriceInputChange: (val: string) => void;
  onApplyPrice: (event: React.FormEvent<HTMLFormElement>) => void;
  priceInputError: string | null;
}

export function SearchFilterControls({
  idPrefix,
  categoryFacets,
  category,
  onSelectCategory,
  activePresetId,
  onSelectPricePreset,
  minPriceInput,
  maxPriceInput,
  onMinPriceInputChange,
  onMaxPriceInputChange,
  onApplyPrice,
  priceInputError,
}: SearchFilterControlsProps) {
  return (
    <div className="flex flex-col gap-6">
      {categoryFacets.length > 0 && (
        <div className="flex flex-col gap-2">
          <span className="text-[13px] font-semibold text-muted-foreground">Danh mục</span>
          <div className="flex flex-col gap-1.5" role="group" aria-label="Lọc theo danh mục">
            <Button
              type="button"
              variant={category === "" ? "default" : "ghost"}
              className={cn(
                "min-h-10 w-full justify-between px-3 font-medium shadow-none",
                category !== "" && "bg-background hover:bg-accent"
              )}
              onClick={() => onSelectCategory("")}
            >
              <span>Tất cả</span>
            </Button>
            {categoryFacets.map((facet) => {
              const active = category === facet.value;
              return (
                <Button
                  key={facet.value}
                  type="button"
                  variant={active ? "default" : "ghost"}
                  className={cn(
                    "min-h-10 w-full justify-between px-3 font-medium shadow-none",
                    !active && "bg-background hover:bg-accent"
                  )}
                  onClick={() => onSelectCategory(facet.value)}
                >
                  <span className="truncate">{facet.label}</span>
                  {facet.count > 0 && (
                    <span
                      className={cn(
                        "text-xs tabular-nums",
                        active ? "text-primary-foreground/85" : "text-muted-foreground"
                      )}
                    >
                      ({formatInt(facet.count)})
                    </span>
                  )}
                </Button>
              );
            })}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-3">
        <span className="text-[13px] font-semibold text-muted-foreground">Khoảng giá</span>
        <div className="flex flex-wrap gap-1.5" role="group" aria-label="Chọn nhanh khoảng giá">
          {SEARCH_PRICE_PRESETS.map((preset) => {
            const active = activePresetId === preset.id;
            return (
              <Button
                key={preset.id}
                type="button"
                size="sm"
                variant={active ? "default" : "outline"}
                className="min-h-9 shadow-none"
                onClick={() => onSelectPricePreset(preset.minPrice, preset.maxPrice)}
              >
                {preset.label}
              </Button>
            );
          })}
        </div>

        <form className="flex flex-col gap-2" onSubmit={onApplyPrice} noValidate>
          <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-1.5">
            <div>
              <label htmlFor={`${idPrefix}-min-price`} className="sr-only">
                Giá từ (VND)
              </label>
              <Input
                id={`${idPrefix}-min-price`}
                type="text"
                inputMode="numeric"
                className="h-10 bg-background px-2.5 text-[13px] tabular-nums"
                placeholder="Từ (₫)"
                value={minPriceInput}
                onChange={(e) => onMinPriceInputChange(e.target.value)}
              />
            </div>
            <span className="text-xs text-muted-foreground" aria-hidden="true">
              –
            </span>
            <div>
              <label htmlFor={`${idPrefix}-max-price`} className="sr-only">
                Giá đến (VND)
              </label>
              <Input
                id={`${idPrefix}-max-price`}
                type="text"
                inputMode="numeric"
                className="h-10 bg-background px-2.5 text-[13px] tabular-nums"
                placeholder="Đến (₫)"
                value={maxPriceInput}
                onChange={(e) => onMaxPriceInputChange(e.target.value)}
              />
            </div>
          </div>
          <Button type="submit" variant="secondary" className="min-h-10 w-full shadow-none">
            Áp dụng
          </Button>
        </form>
        {priceInputError && (
          <p className="text-[13px] font-medium text-destructive" role="alert">
            {priceInputError}
          </p>
        )}
      </div>
    </div>
  );
}
