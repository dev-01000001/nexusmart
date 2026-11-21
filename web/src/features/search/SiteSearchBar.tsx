"use client";

import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { IconClock, IconClose, IconSearch } from "@/components";
import { formatVnd } from "@/lib/format";
import {
  addRecentSearch,
  normalizeSuggestResponse,
  parseRecentSearches,
  recentSearchStorageKey,
  type SearchSuggestion,
  type SiteSearchBarProps,
} from "./searchTypes";
import styles from "./SiteSearchBar.module.css";

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

  // Close dropdown when clicking outside.
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

  // Debounced live suggestions with AbortController.
  useEffect(() => {
    const trimmed = value.trim();
    if (!open || !trimmed) {
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
  }, [value, open]);

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

  const showDropdown = open && (options.length > 0 || (!isShowingRecent && loading));

  const executeSearch = useCallback(
    (rawQuery: string) => {
      const clean = rawQuery.trim();
      setValue(clean);
      setOpen(false);
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

  const handleClearHistory = (event: React.MouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    persistRecent([]);
    setActiveIndex(-1);
    inputRef.current?.focus();
  };

  const activeOptionId =
    showDropdown && activeIndex >= 0 && options[activeIndex] ? options[activeIndex].id : undefined;

  const rootClass = [styles.root, compact ? styles.compact : "", className ?? ""]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={rootRef} className={rootClass}>
      <form
        className={styles.form}
        role="search"
        aria-label={`Tìm kiếm tại ${brandName}`}
        onSubmit={handleSubmit}
      >
        <label htmlFor={inputId} className="sr-only">
          Tìm kiếm sản phẩm tại {brandName}
        </label>
        <div className={styles.inputWrap}>
          <span className={styles.leadingIcon} aria-hidden="true">
            <IconSearch size={compact ? 15 : 17} />
          </span>
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
            className={styles.searchInput}
            placeholder={placeholder ?? `Tìm sản phẩm tại ${brandName}...`}
            value={value}
            onFocus={() => setOpen(true)}
            onChange={(e) => {
              setValue(e.target.value);
              if (!open) setOpen(true);
            }}
            onKeyDown={handleKeyDown}
          />
          {value.length > 0 && (
            <button
              type="button"
              className={styles.clearBtn}
              onClick={handleClearInput}
              aria-label="Xóa từ khóa"
            >
              <IconClose size={14} />
            </button>
          )}
        </div>
        <button type="submit" className={styles.submitBtn}>
          {compact ? "Tìm" : "Tìm kiếm"}
        </button>
      </form>

      {showDropdown && (
        <div className={styles.dropdown} data-ui="search-suggest">
          {isShowingRecent ? (
            <div className={styles.dropdownHead}>
              <span className={styles.dropdownTitle}>Tìm kiếm gần đây</span>
              <button
                type="button"
                className={styles.clearHistoryBtn}
                onMouseDown={(e) => e.preventDefault()}
                onClick={handleClearHistory}
              >
                Xóa lịch sử
              </button>
            </div>
          ) : (
            <div className={styles.dropdownHead}>
              <span className={styles.dropdownTitle}>
                {loading && options.length === 0 ? "Đang tìm gợi ý..." : "Gợi ý tìm kiếm"}
              </span>
            </div>
          )}

          {options.length > 0 && (
            <ul
              id={listboxId}
              role="listbox"
              aria-label={isShowingRecent ? "Tìm kiếm gần đây" : "Gợi ý tìm kiếm"}
              className={styles.optionList}
            >
              {options.map((opt, idx) => {
                const isActive = idx === activeIndex;
                return (
                  <li
                    key={opt.id}
                    id={opt.id}
                    role="option"
                    aria-selected={isActive}
                    className={`${styles.optionRow} ${isActive ? styles.optionRowActive : ""}`}
                    onMouseEnter={() => setActiveIndex(idx)}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      executeSearch(opt.text);
                    }}
                  >
                    <span className={styles.optionMain}>
                      <span className={styles.optionIcon} aria-hidden="true">
                        {opt.kind === "recent" ? <IconClock size={14} /> : <IconSearch size={14} />}
                      </span>
                      <span className={styles.optionText}>{opt.text}</span>
                    </span>
                    <span className={styles.optionAside}>
                      {opt.minPriceVnd && opt.minPriceVnd > 0 ? (
                        <span className={`${styles.optionPrice} num`}>
                          {formatVnd(opt.minPriceVnd)}
                        </span>
                      ) : null}
                      {opt.kind !== "recent" && (
                        <span className={styles.optionKind}>{KIND_LABELS[opt.kind]}</span>
                      )}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
