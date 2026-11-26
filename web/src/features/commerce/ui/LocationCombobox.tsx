"use client";

import React, { useMemo, useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { filterPreparedWards, prepareSearchableWards } from "../addressHelpers";
import type { LocationOption } from "../AddressFieldsForm";

export interface LocationComboboxProps {
  id: string;
  options: LocationOption[];
  value: string;
  onChange: (code: string) => void;
  placeholder: string;
  searchPlaceholder: string;
  emptyMessage: string;
  ariaLabel: string;
  ariaInvalid?: boolean;
  ariaDescribedBy?: string;
  disabled?: boolean;
  loading?: boolean;
  loadingLabel?: string;
  searchInputId?: string;
  query?: string;
  onQueryChange?: (q: string) => void;
}

const MAX_RENDERED_ITEMS = 220;

export function LocationCombobox({
  id,
  options,
  value,
  onChange,
  placeholder,
  searchPlaceholder,
  emptyMessage,
  ariaLabel,
  ariaInvalid,
  ariaDescribedBy,
  disabled = false,
  loading = false,
  loadingLabel = "Đang tải...",
  searchInputId,
  query,
  onQueryChange,
}: LocationComboboxProps) {
  const [open, setOpen] = useState(false);
  const [internalQuery, setInternalQuery] = useState("");

  const activeQuery = query !== undefined ? query : internalQuery;
  const setQuery = (next: string) => {
    if (onQueryChange) {
      onQueryChange(next);
    } else {
      setInternalQuery(next);
    }
  };

  const prepared = useMemo(() => prepareSearchableWards(options), [options]);
  const filtered = useMemo(
    () => filterPreparedWards(prepared, activeQuery),
    [prepared, activeQuery]
  );
  const selected = useMemo(() => options.find((opt) => opt.code === value), [options, value]);

  const visibleOptions =
    filtered.length > MAX_RENDERED_ITEMS ? filtered.slice(0, MAX_RENDERED_ITEMS) : filtered;
  const truncatedCount = filtered.length - visibleOptions.length;

  const displayText = loading
    ? loadingLabel
    : selected
      ? selected.name
      : options.length === 0
        ? "Chưa có dữ liệu"
        : placeholder;

  return (
    <Popover
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) {
          setQuery("");
        }
      }}
    >
      <PopoverTrigger asChild>
        <Button
          id={id}
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-label={ariaLabel}
          aria-invalid={ariaInvalid || undefined}
          aria-describedby={ariaDescribedBy}
          disabled={disabled || loading || options.length === 0}
          className={cn(
            "min-h-10 w-full justify-between border-input bg-card px-3 font-normal shadow-none hover:bg-accent/60",
            !selected && "text-muted-foreground",
            ariaInvalid && "border-destructive ring-destructive/20"
          )}
        >
          <span className="truncate text-left">{displayText}</span>
          <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-[var(--radix-popover-trigger-width)] min-w-[260px] max-w-[calc(100vw-2rem)] p-0"
      >
        <Command shouldFilter={false}>
          <CommandInput
            id={searchInputId}
            value={activeQuery}
            onValueChange={setQuery}
            placeholder={searchPlaceholder}
            aria-label={searchPlaceholder}
          />
          <CommandList className="max-h-64">
            <CommandEmpty>{emptyMessage}</CommandEmpty>
            {visibleOptions.length > 0 && (
              <CommandGroup>
                {visibleOptions.map((opt) => {
                  const isSelected = opt.code === value;
                  return (
                    <CommandItem
                      key={opt.code}
                      value={`${opt.name} ${opt.code}`}
                      onSelect={() => {
                        onChange(opt.code);
                        setQuery("");
                        setOpen(false);
                      }}
                      className="min-h-9 cursor-pointer"
                    >
                      <Check
                        className={cn(
                          "size-4 shrink-0 text-brand",
                          isSelected ? "opacity-100" : "opacity-0"
                        )}
                        aria-hidden="true"
                      />
                      <span className="truncate">{opt.name}</span>
                    </CommandItem>
                  );
                })}
                {truncatedCount > 0 && (
                  <div className="px-2 py-1.5 text-center text-xs text-muted-foreground">
                    Còn {truncatedCount} kết quả nữa — gõ tên để lọc nhanh hơn.
                  </div>
                )}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
