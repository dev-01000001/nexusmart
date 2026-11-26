"use client";

import React, { useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface CollapsibleProseProps {
  paragraphs: string[];
  previewChars?: number;
}

/**
 * Keeps 100% of seller-authored prose in SSR HTML while visually collapsing long copy
 * behind a clean "Xem thêm" / "Thu gọn" toggle.
 */
export function CollapsibleProse({ paragraphs, previewChars = 180 }: CollapsibleProseProps) {
  const totalLen = paragraphs.reduce((sum, p) => sum + p.length, 0);
  const needsToggle = paragraphs.length > 1 || totalLen > previewChars;
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="flex flex-col items-start gap-2.5">
      <div className={cn("flex flex-col gap-2.5", needsToggle && !expanded && "line-clamp-3")}>
        {paragraphs.map((para, idx) => (
          <p key={idx} className="max-w-[68ch] text-sm leading-6 text-foreground/90">
            {para}
          </p>
        ))}
      </div>
      {needsToggle && (
        <Button
          type="button"
          variant="link"
          size="sm"
          className="h-auto min-h-10 gap-1 px-0 text-sm font-semibold text-brand"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
        >
          <span>{expanded ? "Thu gọn" : "Xem thêm"}</span>
          {expanded ? (
            <ChevronUp className="size-4" aria-hidden="true" />
          ) : (
            <ChevronDown className="size-4" aria-hidden="true" />
          )}
        </Button>
      )}
    </div>
  );
}
