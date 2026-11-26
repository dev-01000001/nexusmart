"use client";

import React from "react";
import type { CategoryInfo } from "@/data/products";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

export type CatalogSortKey = "newest" | "price_asc" | "price_desc";

export interface CatalogFilterControlsProps {
  idPrefix: string;
  showFilters: boolean;
  showSearch: boolean;
  categories: CategoryInfo[];
  totalProductCount: number;
  selectedCategory: string;
  onSelectCategory: (slug: string) => void;
  query: string;
  onQueryChange: (value: string) => void;
  sort: CatalogSortKey;
  onSortChange: (next: CatalogSortKey) => void;
}

export function CatalogFilterControls({
  idPrefix,
  showFilters,
  showSearch,
  categories,
  totalProductCount,
  selectedCategory,
  onSelectCategory,
  query,
  onQueryChange,
  sort,
  onSortChange,
}: CatalogFilterControlsProps) {
  return (
    <div className="flex flex-col gap-5">
      {showFilters && (
        <div className="flex flex-col gap-2">
          <span className="text-[13px] font-semibold text-muted-foreground">Danh mục</span>
          <div className="flex flex-col gap-1.5" role="group" aria-label="Lọc theo danh mục">
            <Button
              type="button"
              variant={selectedCategory === "" ? "default" : "ghost"}
              className={cn(
                "min-h-10 w-full justify-between px-3 font-medium shadow-none",
                selectedCategory !== "" && "bg-background hover:bg-accent"
              )}
              onClick={() => onSelectCategory("")}
            >
              <span>Tất cả ({totalProductCount})</span>
            </Button>
            {categories.map((cat) => {
              const active = selectedCategory === cat.slug;
              return (
                <Button
                  key={cat.slug}
                  type="button"
                  variant={active ? "default" : "ghost"}
                  className={cn(
                    "min-h-10 w-full justify-between px-3 font-medium shadow-none",
                    !active && "bg-background hover:bg-accent"
                  )}
                  onClick={() => onSelectCategory(cat.slug)}
                >
                  <span className="truncate">{cat.name}</span>
                </Button>
              );
            })}
          </div>
        </div>
      )}

      {showSearch && (
        <div className="flex flex-col gap-2">
          <label
            htmlFor={`${idPrefix}-search`}
            className="text-[13px] font-semibold text-muted-foreground"
          >
            Lọc từ khóa
          </label>
          <Input
            id={`${idPrefix}-search`}
            type="search"
            className="h-10 w-full bg-background"
            placeholder="Tên, chất liệu..."
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
          />
        </div>
      )}

      <div className="flex flex-col gap-2">
        <label
          htmlFor={`${idPrefix}-sort`}
          className="text-[13px] font-semibold text-muted-foreground"
        >
          Sắp xếp
        </label>
        <Select value={sort} onValueChange={(v) => onSortChange(v as CatalogSortKey)}>
          <SelectTrigger
            id={`${idPrefix}-sort`}
            aria-label="Sắp xếp sản phẩm"
            className="h-10 w-full bg-background shadow-none"
          >
            <SelectValue placeholder="Sắp xếp" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="newest">Mới nhất</SelectItem>
            <SelectItem value="price_asc">Giá tăng dần</SelectItem>
            <SelectItem value="price_desc">Giá giảm dần</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  );
}
