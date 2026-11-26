"use client";

import React, { useEffect, useState } from "react";
import { AlertCircle, Check, Sparkles, Star } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  normalizeReviewSummary,
  shouldDisplayReviewSummary,
  type ProductReviewSummaryProps,
  type ReviewSummaryDTO,
} from "./assistantTypes";

export function ProductReviewSummary({
  productId,
  productName,
  className,
}: ProductReviewSummaryProps) {
  const [summary, setSummary] = useState<ReviewSummaryDTO | null>(null);

  useEffect(() => {
    const cleanId = productId.trim();
    if (!cleanId) {
      setSummary(null);
      return;
    }

    const controller = new AbortController();
    let active = true;

    api<unknown>(`/api/products/${encodeURIComponent(cleanId)}/review-summary`, {
      signal: controller.signal,
    })
      .then((raw) => {
        if (!active) return;
        const normalized = normalizeReviewSummary(raw, cleanId);
        setSummary(shouldDisplayReviewSummary(normalized) ? normalized : null);
      })
      .catch(() => {
        // Hide quietly on 404, 503, insufficient reviews, or network error.
        if (active) setSummary(null);
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [productId]);

  if (!shouldDisplayReviewSummary(summary) || !summary) {
    return null;
  }

  const hasPros = summary.pros.length > 0;
  const hasCons = summary.cons.length > 0;

  return (
    <Card
      className={cn(
        "gap-3.5 rounded-lg border border-border bg-muted/50 py-4 shadow-none",
        className
      )}
      data-slot="review-summary"
      aria-label={
        productName ? `Tóm tắt đánh giá cho ${productName}` : "Tóm tắt đánh giá do AI tổng hợp"
      }
    >
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2.5 px-4 pb-0 md:px-5">
        <div className="flex items-center gap-2">
          <span
            className="inline-flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/12 text-brand"
            aria-hidden="true"
          >
            <Sparkles className="size-4" />
          </span>
          <CardTitle className="text-[15px] leading-snug font-semibold text-foreground">
            Tóm tắt đánh giá do AI tổng hợp
          </CardTitle>
        </div>

        <div className="flex flex-wrap items-center gap-2 tabular-nums">
          {summary.averageRating > 0 && (
            <Badge
              variant="secondary"
              className="gap-1 rounded-sm px-2 py-0.5 text-xs font-semibold"
            >
              <Star className="size-3.5 fill-current text-brand" aria-hidden="true" />
              <span>{summary.averageRating.toFixed(1).replace(".", ",")}/5</span>
            </Badge>
          )}
          <span className="text-[13px] text-muted-foreground">
            Tổng hợp từ {formatInt(summary.reviewCount)} đánh giá
          </span>
        </div>
      </CardHeader>

      <CardContent className="space-y-3.5 px-4 md:px-5">
        {summary.summary && (
          <p className="max-w-[72ch] text-sm leading-6 text-foreground">{summary.summary}</p>
        )}

        {(hasPros || hasCons) && (
          <div className={cn("grid grid-cols-1 gap-3", hasPros && hasCons && "sm:grid-cols-2")}>
            {hasPros && (
              <div className="flex flex-col gap-2 rounded-lg border border-border bg-card p-3.5">
                <h4 className="inline-flex items-center gap-1.5 text-sm font-semibold text-badge-success-ink">
                  <span
                    className="inline-flex size-5 items-center justify-center rounded-full bg-badge-success-bg text-badge-success-ink"
                    aria-hidden="true"
                  >
                    <Check className="size-3" />
                  </span>
                  <span>Ưu điểm nổi bật</span>
                </h4>
                <ul className="flex flex-col gap-1.5">
                  {summary.pros.map((item) => (
                    <li
                      key={item}
                      className="flex items-start gap-2 text-sm leading-5 text-foreground"
                    >
                      <Check
                        className="mt-0.5 size-3.5 shrink-0 text-badge-success-ink"
                        aria-hidden="true"
                      />
                      <span>{item}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {hasCons && (
              <div className="flex flex-col gap-2 rounded-lg border border-border bg-card p-3.5">
                <h4 className="inline-flex items-center gap-1.5 text-sm font-semibold text-badge-warning-ink">
                  <span
                    className="inline-flex size-5 items-center justify-center rounded-full bg-badge-warning-bg text-badge-warning-ink"
                    aria-hidden="true"
                  >
                    <AlertCircle className="size-3" />
                  </span>
                  <span>Điểm cần lưu ý</span>
                </h4>
                <ul className="flex flex-col gap-1.5">
                  {summary.cons.map((item) => (
                    <li
                      key={item}
                      className="flex items-start gap-2 text-sm leading-5 text-foreground"
                    >
                      <span
                        className="mt-2 size-1.5 shrink-0 rounded-full bg-badge-warning-ink"
                        aria-hidden="true"
                      />
                      <span>{item}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}

        <p className="text-[13px] leading-[18px] text-muted-foreground">
          Nội dung được tổng hợp tự động từ nhận xét thực tế của người mua trên cửa hàng.
        </p>
      </CardContent>
    </Card>
  );
}
