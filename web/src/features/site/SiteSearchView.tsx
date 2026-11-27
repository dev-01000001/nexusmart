"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { SearchX } from "lucide-react";
import type { CategoryInfo, Product } from "@/data/products";
import { EmptyState } from "@/components/app/EmptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { searchProducts, suggestSpelling } from "@/lib/search";
import { cn } from "@/lib/utils";
import { SiteProductCard } from "./SiteProductCard";

export interface SiteSearchViewProps {
  brandName: string;
  products: Product[];
  categories: CategoryInfo[];
  initialQuery?: string;
  initialCategory?: string;
  initialSort?: string;
}

type SortKey = "relevance" | "price_asc" | "price_desc";
const PAGE_SIZE = 12;

export function SiteSearchView({
  brandName,
  products,
  categories,
  initialQuery = "",
  initialCategory = "",
  initialSort = "relevance",
}: SiteSearchViewProps) {
  const router = useRouter();
  const [queryInput, setQueryInput] = useState(initialQuery);
  const [appliedQuery, setAppliedQuery] = useState(initialQuery);
  const [category, setCategory] = useState(initialCategory);
  const [selectedSize, setSelectedSize] = useState<string>("");
  const [sort, setSort] = useState<SortKey>(
    initialSort === "price_asc" || initialSort === "price_desc" ? initialSort : "relevance"
  );
  const [page, setPage] = useState(1);

  const availableSizes = useMemo<string[]>(() => {
    const found: string[] = [];
    const seen = new Set<string>();
    for (const p of products) {
      const hasRealColor = p.colors.some((c) => c.code !== "mac-dinh" && c.name !== "Mặc định");
      if (!hasRealColor) continue;
      for (const sz of p.sizes) {
        const clean = String(sz ?? "").trim();
        if (!clean || clean === "Mặc định" || seen.has(clean)) continue;
        seen.add(clean);
        found.push(clean);
      }
    }
    return found;
  }, [products]);

  const matched = useMemo(() => {
    const byQuery = appliedQuery.trim() ? searchProducts(appliedQuery, products) : products;
    const filtered = byQuery.filter((p) => {
      if (category && p.categorySlug !== category) return false;
      if (selectedSize) {
        const hasStock = p.skus.some((s) => s.size === selectedSize && s.stock > 0);
        if (!hasStock) return false;
      }
      return true;
    });
    const out = [...filtered];
    if (sort === "price_asc") out.sort((a, b) => a.price - b.price);
    else if (sort === "price_desc") out.sort((a, b) => b.price - a.price);
    return out;
  }, [products, appliedQuery, category, selectedSize, sort]);

  const suggestion = useMemo(() => {
    if (!appliedQuery.trim() || matched.length > 0) return null;
    return suggestSpelling(appliedQuery);
  }, [appliedQuery, matched.length]);

  const totalPages = Math.max(1, Math.ceil(matched.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const paged = useMemo(() => {
    const start = (safePage - 1) * PAGE_SIZE;
    return matched.slice(start, start + PAGE_SIZE);
  }, [matched, safePage]);

  const submitQuery = (nextQ: string) => {
    const clean = nextQ.trim();
    setQueryInput(clean);
    setAppliedQuery(clean);
    setPage(1);
    const params = new URLSearchParams();
    if (clean) params.set("q", clean);
    if (category) params.set("cat", category);
    const qs = params.toString();
    router.replace(qs ? `/tim-kiem?${qs}` : "/tim-kiem", { scroll: false });
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    submitQuery(queryInput);
  };

  const gridClass =
    paged.length === 3
      ? "grid grid-cols-2 gap-4 lg:grid-cols-3 lg:gap-6"
      : "grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4 lg:gap-6";

  return (
    <div className="mx-auto flex w-full max-w-[var(--site-max-w,1260px)] flex-col gap-6 px-[clamp(1rem,2.2vw,2rem)] pt-[clamp(24px,3.2vw,44px)] pb-[clamp(48px,6vw,80px)] text-foreground">
      <section className="flex flex-col gap-2" aria-labelledby="site-search-view-title">
        <h1
          id="site-search-view-title"
          className="text-[clamp(26px,3.6vw,42px)] leading-[1.1] font-semibold tracking-[-0.025em] text-foreground"
        >
          {appliedQuery.trim()
            ? `Kết quả cho “${appliedQuery.trim()}”`
            : `Tìm kiếm tại ${brandName}`}
        </h1>
        <p className="max-w-[64ch] text-[15px] text-muted-foreground">
          {appliedQuery.trim()
            ? `Tìm thấy ${matched.length} sản phẩm đang bán tại ${brandName}.`
            : `Tìm nhanh theo tên sản phẩm, chất liệu hoặc kiểu dáng trong ${products.length} mẫu của ${brandName}.`}
        </p>
      </section>

      <section className="flex flex-col gap-5">
        <form
          className="flex flex-wrap items-center justify-between gap-3"
          onSubmit={handleSubmit}
          role="search"
        >
          <div className="flex min-w-0 flex-1 basis-[280px] items-center gap-2">
            <label htmlFor="site-search-input" className="sr-only">
              Từ khóa tìm kiếm
            </label>
            <Input
              id="site-search-input"
              type="search"
              className="h-10 min-w-0 flex-1 bg-card"
              placeholder={`Tìm sản phẩm của ${brandName}...`}
              value={queryInput}
              onChange={(e) => setQueryInput(e.target.value)}
            />
            <Button type="submit" className="min-h-10 shrink-0 px-5 shadow-none">
              Tìm kiếm
            </Button>
          </div>

          <div className="flex items-center gap-2">
            <label htmlFor="site-search-sort" className="sr-only">
              Sắp xếp kết quả
            </label>
            <Select
              value={sort}
              onValueChange={(v) => {
                setSort(v as SortKey);
                setPage(1);
              }}
            >
              <SelectTrigger
                id="site-search-sort"
                aria-label="Sắp xếp kết quả"
                className="h-10 min-w-[170px] bg-card shadow-none"
              >
                <SelectValue placeholder="Sắp xếp" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="relevance">Phù hợp nhất</SelectItem>
                <SelectItem value="price_asc">Giá tăng dần</SelectItem>
                <SelectItem value="price_desc">Giá giảm dần</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </form>

        <div className="flex flex-col gap-3">
          <div
            className="flex flex-wrap items-center gap-2"
            role="group"
            aria-label="Lọc theo danh mục"
          >
            <Button
              type="button"
              size="sm"
              variant={category === "" ? "default" : "outline"}
              className="min-h-9 shadow-none"
              onClick={() => {
                setCategory("");
                setPage(1);
              }}
            >
              Tất cả danh mục
            </Button>
            {categories.map((cat) => (
              <Button
                key={cat.slug}
                type="button"
                size="sm"
                variant={category === cat.slug ? "default" : "outline"}
                className="min-h-9 shadow-none"
                onClick={() => {
                  setCategory(cat.slug);
                  setPage(1);
                }}
              >
                {cat.name}
              </Button>
            ))}
          </div>

          {availableSizes.length > 0 && (
            <div
              className="flex flex-wrap items-center gap-2"
              role="group"
              aria-label="Lọc theo phân loại sẵn hàng"
            >
              <Button
                type="button"
                size="sm"
                variant={selectedSize === "" ? "default" : "outline"}
                className="min-h-9 shadow-none"
                onClick={() => {
                  setSelectedSize("");
                  setPage(1);
                }}
              >
                Tất cả phân loại
              </Button>
              {availableSizes.map((sz) => (
                <Button
                  key={sz}
                  type="button"
                  size="sm"
                  variant={selectedSize === sz ? "default" : "outline"}
                  className="min-h-9 min-w-10 shadow-none"
                  onClick={() => {
                    setSelectedSize(selectedSize === sz ? "" : sz);
                    setPage(1);
                  }}
                >
                  {sz}
                </Button>
              ))}
            </div>
          )}
        </div>

        {suggestion && (
          <p
            className="flex flex-wrap items-center gap-1.5 rounded-lg border border-border bg-muted px-4 py-3 text-sm text-foreground"
            role="status"
          >
            <span>Có phải bạn muốn tìm</span>
            <Button
              type="button"
              variant="link"
              size="sm"
              className="h-auto p-0 text-sm font-semibold text-brand underline"
              onClick={() => submitQuery(suggestion)}
            >
              {suggestion}
            </Button>
            <span>?</span>
          </p>
        )}

        {paged.length > 0 ? (
          <>
            <div className={gridClass}>
              {paged.map((product, idx) => (
                <SiteProductCard key={product.id} product={product} index={idx} />
              ))}
            </div>

            {totalPages > 1 && (
              <div className="flex flex-col items-center justify-between gap-3 border-t border-border pt-5 sm:flex-row">
                <span className="text-sm text-muted-foreground tabular-nums">
                  Trang {safePage} / {totalPages}
                </span>
                <Pagination className="mx-0 w-auto">
                  <PaginationContent>
                    <PaginationItem>
                      <PaginationPrevious
                        href="#prev"
                        aria-disabled={safePage <= 1}
                        tabIndex={safePage <= 1 ? -1 : undefined}
                        className={cn(
                          "min-h-10",
                          safePage <= 1 && "pointer-events-none opacity-45"
                        )}
                        onClick={(e) => {
                          e.preventDefault();
                          if (safePage > 1) setPage((p) => Math.max(1, p - 1));
                        }}
                      />
                    </PaginationItem>
                    <PaginationItem>
                      <PaginationNext
                        href="#next"
                        aria-disabled={safePage >= totalPages}
                        tabIndex={safePage >= totalPages ? -1 : undefined}
                        className={cn(
                          "min-h-10",
                          safePage >= totalPages && "pointer-events-none opacity-45"
                        )}
                        onClick={(e) => {
                          e.preventDefault();
                          if (safePage < totalPages) setPage((p) => Math.min(totalPages, p + 1));
                        }}
                      />
                    </PaginationItem>
                  </PaginationContent>
                </Pagination>
              </div>
            )}
          </>
        ) : (
          <EmptyState
            icon={SearchX}
            title="Chưa tìm thấy sản phẩm khớp yêu cầu"
            description={`Thử đổi từ khóa ngắn hơn hoặc bỏ bớt bộ lọc để xem toàn bộ sản phẩm của ${brandName}.`}
            className="rounded-lg border border-border shadow-none"
            action={
              <div className="flex flex-wrap items-center justify-center gap-2.5">
                <Button
                  type="button"
                  className="min-h-10 shadow-none"
                  onClick={() => {
                    setCategory("");
                    setSelectedSize("");
                    submitQuery("");
                  }}
                >
                  Xóa bộ lọc tìm kiếm
                </Button>
                <Button variant="outline" className="min-h-10 shadow-none" asChild>
                  <Link href="/san-pham">Xem tất cả sản phẩm</Link>
                </Button>
              </div>
            }
          />
        )}
      </section>
    </div>
  );
}
