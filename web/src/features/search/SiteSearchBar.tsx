"use client";

import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
import { Clock, Command, Search, X } from "lucide-react";
import { Money } from "@/components/app/Money";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  addRecentSearch,
  normalizeSuggestResponse,
  parseRecentSearches,
  recentSearchStorageKey,
  type SearchSuggestion,
  type SiteSearchBarProps,
} from "./searchTypes";

const SiteSearchCommandDialog = dynamic(
  () => import("./SiteSearchCommandDialog").then((mod) => mod.SiteSearchCommandDialog),
  { ssr: false }
);

interface DropdownOption {
  id: string;
  text: string;
  kind: "recent" | "query" | "product" | "category";
  minPriceVnd?: number;
}

const KIND_LABELS: Record<Exclude<DropdownOption["kind"], "recent">, string> = {
  query: "Gợi ý",
  product: "Sản phẩm",
  category: "Danh mục",
};

export function SiteSearchBar({
  brandName,
  shopId,
  initialQuery = "",
  placeholder,
  compact = false,
  onNavigate,
  className,
}: SiteSearchBarProps) {
  const router = useRouter();
  const listboxId = useId();
  const inputId = useId();
  const rootRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const storageKey = useMemo(() => recentSearchStorageKey(shopId, brandName), [shopId, brandName]);

  const [value, setValue] = useState(initialQuery);
  const [open, setOpen] = useState(false);
  const [commandMounted, setCommandMounted] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [suggestions, setSuggestions] = useState<SearchSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [recentQueries, setRecentQueries] = useState<string[]>([]);
  const [activeIndex, setActiveIndex] = useState(-1);

  useEffect(() => {
    setValue(initialQuery);
  }, [initialQuery]);

  // Load recent search history from sessionStorage safely on mount / key change.
  useEffect(() => {
    try {
      const raw = window.sessionStorage.getItem(storageKey);
      setRecentQueries(parseRecentSearches(raw));
    } catch {
      setRecentQueries([]);
    }
  }, [storageKey]);

  const persistRecent = useCallback(
    (nextList: string[]) => {
      setRecentQueries(nextList);
      try {
        if (nextList.length === 0) {
          window.sessionStorage.removeItem(storageKey);
        } else {
          window.sessionStorage.setItem(storageKey, JSON.stringify(nextList));
        }
      } catch {
        // Storage may be unavailable in private browsing or restricted contexts.
      }
    },
    [storageKey]
  );

  // Close inline dropdown when clicking outside.
  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
        setActiveIndex(-1);
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, []);

  // Keyboard shortcut Cmd/Ctrl+K opens lazy CommandDialog when bar is in main page flow.
  useEffect(() => {
    const handleGlobalKey = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") return;
      if (rootRef.current?.closest('[role="dialog"]')) return;
      event.preventDefault();
      setOpen(false);
      setCommandMounted(true);
      setCommandOpen((prev) => !prev);
    };
    window.addEventListener("keydown", handleGlobalKey);
    return () => window.removeEventListener("keydown", handleGlobalKey);
  }, []);

  // Debounced live suggestions with AbortController (active for either inline dropdown or CommandDialog).
  useEffect(() => {
    const trimmed = value.trim();
    if ((!open && !commandOpen) || !trimmed) {
      setSuggestions([]);
      setLoading(false);
      return;
    }

    const controller = new AbortController();
    setLoading(true);
    const timer = window.setTimeout(async () => {
      try {
        const res = await fetch(`/api/search/suggest?q=${encodeURIComponent(trimmed)}`, {
          method: "GET",
          headers: { Accept: "application/json" },
          cache: "no-store",
          signal: controller.signal,
        });
        if (!res.ok) {
          setSuggestions([]);
          return;
        }
        const raw = (await res.json().catch(() => null)) as unknown;
        if (!controller.signal.aborted) {
          setSuggestions(normalizeSuggestResponse(raw).suggestions);
        }
      } catch {
        if (!controller.signal.aborted) {
          setSuggestions([]);
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    }, 200);

    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [value, open, commandOpen]);

  const trimmedValue = value.trim();
  const isShowingRecent = trimmedValue.length === 0;

  const options: DropdownOption[] = useMemo(() => {
    if (isShowingRecent) {
      return recentQueries.map((q, idx) => ({
        id: `${listboxId}-recent-${idx}`,
        text: q,
        kind: "recent",
      }));
    }
    return suggestions.map((item, idx) => ({
      id: `${listboxId}-suggest-${idx}`,
      text: item.text,
      kind: item.kind,
      minPriceVnd: item.minPriceVnd,
    }));
  }, [isShowingRecent, recentQueries, suggestions, listboxId]);

  useEffect(() => {
    setActiveIndex(-1);
  }, [options.length, trimmedValue]);

  const showDropdown =
    open && !commandOpen && (options.length > 0 || (!isShowingRecent && loading));

  const executeSearch = useCallback(
    (rawQuery: string) => {
      const clean = rawQuery.trim();
      setValue(clean);
      setOpen(false);
      setCommandOpen(false);
      setActiveIndex(-1);

      if (clean) {
        persistRecent(addRecentSearch(recentQueries, clean));
      }

      const target = clean ? `/tim-kiem?q=${encodeURIComponent(clean)}` : "/tim-kiem";
      router.push(target);
      onNavigate?.(clean);
    },
    [onNavigate, persistRecent, recentQueries, router]
  );

  const handleQuickLink = useCallback(
    (href: string) => {
      setOpen(false);
      setCommandOpen(false);
      router.push(href);
      onNavigate?.("");
    },
    [onNavigate, router]
  );

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (showDropdown && activeIndex >= 0 && options[activeIndex]) {
      executeSearch(options[activeIndex].text);
      return;
    }
    executeSearch(value);
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      if (options.length > 0) {
        setActiveIndex((prev) => (prev + 1) % options.length);
      }
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      if (options.length > 0) {
        setActiveIndex((prev) => (prev <= 0 ? options.length - 1 : prev - 1));
      }
    } else if (event.key === "Escape") {
      if (open) {
        event.preventDefault();
        setOpen(false);
        setActiveIndex(-1);
      }
    } else if (event.key === "Enter" && showDropdown && activeIndex >= 0 && options[activeIndex]) {
      event.preventDefault();
      executeSearch(options[activeIndex].text);
    }
  };

  const handleClearInput = () => {
    setValue("");
    setSuggestions([]);
    setActiveIndex(-1);
    setOpen(true);
    inputRef.current?.focus();
  };

  const handleClearHistory = (event?: React.MouseEvent<HTMLButtonElement>) => {
    event?.preventDefault();
    persistRecent([]);
    setActiveIndex(-1);
    inputRef.current?.focus();
  };

  const handleOpenCommand = () => {
    setOpen(false);
    setCommandMounted(true);
    setCommandOpen(true);
  };

  const activeOptionId =
    showDropdown && activeIndex >= 0 && options[activeIndex] ? options[activeIndex].id : undefined;

  return (
    <div ref={rootRef} className={cn("relative w-full min-w-0", className)}>
      <form
        className="flex w-full min-w-0 items-stretch gap-2"
        role="search"
        aria-label={`Tìm kiếm tại ${brandName}`}
        onSubmit={handleSubmit}
      >
        <label htmlFor={inputId} className="sr-only">
          Tìm kiếm sản phẩm tại {brandName}
        </label>

        <div className="relative flex min-w-0 flex-1 items-center">
          <Search
            className={cn(
              "pointer-events-none absolute left-3 text-muted-foreground",
              compact ? "size-4" : "size-[18px]"
            )}
            aria-hidden="true"
          />

          <input
            ref={inputRef}
            id={inputId}
            type="search"
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={showDropdown}
            aria-controls={listboxId}
            aria-activedescendant={activeOptionId}
            autoComplete="off"
            spellCheck={false}
            maxLength={200}
            className={cn(
              "w-full min-w-0 rounded-md border border-input bg-card text-foreground transition-colors placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:outline-none [&::-webkit-search-cancel-button]:appearance-none",
              compact ? "h-10 pr-20 pl-9 text-sm" : "h-11 pr-24 pl-10 text-[15px]"
            )}
            placeholder={placeholder ?? `Tìm sản phẩm tại ${brandName}...`}
            value={value}
            onFocus={() => setOpen(true)}
            onChange={(e) => {
              setValue(e.target.value);
              if (!open) setOpen(true);
            }}
            onKeyDown={handleKeyDown}
          />

          <div className="absolute right-1.5 flex items-center gap-0.5">
            {value.length > 0 && (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="size-8 text-muted-foreground hover:text-foreground"
                onClick={handleClearInput}
                aria-label="Xóa từ khóa"
              >
                <X className="size-4" aria-hidden="true" />
              </Button>
            )}

            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-8 gap-1 px-2 text-xs text-muted-foreground hover:text-foreground"
              onClick={handleOpenCommand}
              aria-label={`Mở hộp thoại tìm nhanh tại ${brandName}`}
              title="Tìm nhanh (⌘K)"
            >
              <Command className="size-3.5" aria-hidden="true" />
              <span className="hidden sm:inline">K</span>
            </Button>
          </div>
        </div>

        <Button
          type="submit"
          className={cn(
            "shrink-0 font-semibold whitespace-nowrap shadow-none",
            compact ? "h-10 px-4 text-sm" : "h-11 px-5 text-[15px]"
          )}
        >
          {compact ? "Tìm" : "Tìm kiếm"}
        </Button>
      </form>

      {showDropdown && (
        <div
          className="absolute top-[calc(100%+6px)] right-0 left-0 z-50 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-overlay"
          data-slot="search-suggest"
        >
          {isShowingRecent ? (
            <div className="flex items-center justify-between gap-3 border-b border-border bg-muted/60 px-3.5 py-2.5">
              <span className="text-[13px] font-semibold text-muted-foreground">
                Tìm kiếm gần đây
              </span>
              <Button
                type="button"
                variant="link"
                size="sm"
                className="h-auto p-0 text-[13px] font-semibold text-brand"
                onMouseDown={(e) => e.preventDefault()}
                onClick={handleClearHistory}
              >
                Xóa lịch sử
              </Button>
            </div>
          ) : (
            <div className="flex items-center justify-between gap-3 border-b border-border bg-muted/60 px-3.5 py-2.5">
              <span className="text-[13px] font-semibold text-muted-foreground">
                {loading && options.length === 0 ? "Đang tìm gợi ý..." : "Gợi ý tìm kiếm"}
              </span>
            </div>
          )}

          {options.length > 0 && (
            <ul
              id={listboxId}
              role="listbox"
              aria-label={isShowingRecent ? "Tìm kiếm gần đây" : "Gợi ý tìm kiếm"}
              className="max-h-80 overflow-y-auto py-1"
            >
              {options.map((opt, idx) => {
                const isActive = idx === activeIndex;
                return (
                  <li
                    key={opt.id}
                    id={opt.id}
                    role="option"
                    aria-selected={isActive}
                    className={cn(
                      "flex min-h-11 cursor-pointer items-center justify-between gap-3 px-3.5 py-2.5 text-sm transition-colors",
                      isActive
                        ? "bg-accent text-accent-foreground"
                        : "text-foreground hover:bg-accent/70 hover:text-accent-foreground"
                    )}
                    onMouseEnter={() => setActiveIndex(idx)}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      executeSearch(opt.text);
                    }}
                  >
                    <span className="flex min-w-0 flex-1 items-center gap-2.5">
                      <span className="shrink-0 text-muted-foreground" aria-hidden="true">
                        {opt.kind === "recent" ? (
                          <Clock className="size-4" />
                        ) : (
                          <Search className="size-4" />
                        )}
                      </span>
                      <span className="truncate text-[15px]">{opt.text}</span>
                    </span>
                    <span className="flex shrink-0 items-center gap-2">
                      {opt.minPriceVnd && opt.minPriceVnd > 0 ? (
                        <Money vnd={opt.minPriceVnd} size="sm" />
                      ) : null}
                      {opt.kind !== "recent" && (
                        <Badge variant="secondary" className="rounded-sm px-1.5 py-0 text-[11px]">
                          {KIND_LABELS[opt.kind]}
                        </Badge>
                      )}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}

      {commandMounted && (
        <SiteSearchCommandDialog
          open={commandOpen}
          onOpenChange={setCommandOpen}
          brandName={brandName}
          query={value}
          onQueryChange={setValue}
          suggestions={suggestions}
          loading={loading}
          recentQueries={recentQueries}
          onSelectQuery={executeSearch}
          onClearHistory={() => handleClearHistory()}
          onQuickLink={handleQuickLink}
        />
      )}
    </div>
  );
}
