"use client";

import React from "react";
import { ArrowRight, Clock, Search, Tag, Trash2, Zap } from "lucide-react";
import { Money } from "@/components/app/Money";
import { Badge } from "@/components/ui/badge";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import type { SearchSuggestion } from "./searchTypes";

export interface SiteSearchCommandDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  brandName: string;
  query: string;
  onQueryChange: (next: string) => void;
  suggestions: SearchSuggestion[];
  loading: boolean;
  recentQueries: string[];
  onSelectQuery: (query: string) => void;
  onClearHistory: () => void;
  onQuickLink: (href: string) => void;
}

const KIND_LABELS: Record<SearchSuggestion["kind"], string> = {
  query: "Gợi ý",
  product: "Sản phẩm",
  category: "Danh mục",
};

function foldVietnamese(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/g, "d")
    .replace(/Đ/g, "D")
    .toLowerCase()
    .trim();
}

export function SiteSearchCommandDialog({
  open,
  onOpenChange,
  brandName,
  query,
  onQueryChange,
  suggestions,
  loading,
  recentQueries,
  onSelectQuery,
  onClearHistory,
  onQuickLink,
}: SiteSearchCommandDialogProps) {
  const trimmed = query.trim();
  const isShowingRecent = trimmed.length === 0;

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Tìm sản phẩm tại ${brandName}`}
      description="Nhập tên sản phẩm, chất liệu hoặc chọn gợi ý bên dưới."
    >
      <CommandInput
        placeholder={`Tìm sản phẩm tại ${brandName}...`}
        value={query}
        onValueChange={onQueryChange}
      />
      <CommandList>
        <CommandEmpty>
          {loading ? "Đang tìm gợi ý..." : "Không có gợi ý khớp. Nhấn Enter để tìm trong cửa hàng."}
        </CommandEmpty>

        {!isShowingRecent && (
          <CommandGroup
            heading={loading && suggestions.length === 0 ? "Đang tìm gợi ý..." : "Gợi ý tìm kiếm"}
          >
            <CommandItem
              value={`__search__${trimmed}`}
              keywords={[trimmed, foldVietnamese(trimmed)]}
              onSelect={() => onSelectQuery(trimmed)}
            >
              <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span className="truncate font-medium">Tìm &ldquo;{trimmed}&rdquo;</span>
              <ArrowRight
                className="ml-auto size-4 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
            </CommandItem>

            {suggestions.map((item, idx) => (
              <CommandItem
                key={`${item.kind}-${item.productId ?? item.slug ?? item.text}-${idx}`}
                value={`${item.text}-${idx}`}
                keywords={[trimmed, foldVietnamese(trimmed), item.text, foldVietnamese(item.text)]}
                onSelect={() => onSelectQuery(item.text)}
              >
                <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate">{item.text}</span>
                <span className="ml-auto flex shrink-0 items-center gap-2">
                  {item.minPriceVnd && item.minPriceVnd > 0 ? (
                    <Money vnd={item.minPriceVnd} size="sm" />
                  ) : null}
                  <Badge variant="secondary" className="rounded-sm px-1.5 py-0 text-[11px]">
                    {KIND_LABELS[item.kind]}
                  </Badge>
                </span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {isShowingRecent && recentQueries.length > 0 && (
          <CommandGroup heading="Tìm kiếm gần đây">
            {recentQueries.map((recent) => (
              <CommandItem
                key={recent}
                value={`recent-${recent}`}
                keywords={[recent, foldVietnamese(recent)]}
                onSelect={() => onSelectQuery(recent)}
              >
                <Clock className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate">{recent}</span>
              </CommandItem>
            ))}
            <CommandItem
              value="xoa-lich-su-tim-kiem"
              keywords={["xoa lich su", "xóa lịch sử"]}
              onSelect={onClearHistory}
            >
              <Trash2 className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span className="text-muted-foreground">Xóa lịch sử tìm kiếm</span>
            </CommandItem>
          </CommandGroup>
        )}

        <CommandSeparator />

        <CommandGroup heading="Lối tắt cửa hàng">
          <CommandItem
            value="tat-ca-san-pham"
            keywords={["san pham", "sản phẩm", "danh muc", trimmed]}
            onSelect={() => onQuickLink("/san-pham")}
          >
            <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>Tất cả sản phẩm</span>
          </CommandItem>
          <CommandItem
            value="flash-sale"
            keywords={["flash sale", "uu dai", "khuyen mai", trimmed]}
            onSelect={() => onQuickLink("/flash-sale")}
          >
            <Zap className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>Flash sale</span>
          </CommandItem>
          <CommandItem
            value="ma-giam-gia"
            keywords={["voucher", "ma giam gia", "mã giảm giá", trimmed]}
            onSelect={() => onQuickLink("/voucher")}
          >
            <Tag className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span>Mã giảm giá</span>
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
