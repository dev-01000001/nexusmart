"use client";

import React, { useState } from "react";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

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
    <div className={tplStyles.collapsibleWrap}>
      <div
        className={`${tplStyles.collapsibleBody} ${
          needsToggle && !expanded ? tplStyles.collapsibleClamped : ""
        }`}
      >
        {paragraphs.map((para, idx) => (
          <p key={idx} className={styles.prosePara}>
            {para}
          </p>
        ))}
      </div>
      {needsToggle && (
        <button
          type="button"
          className={tplStyles.collapsibleBtn}
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? "Thu gọn" : "Xem thêm"}
        </button>
      )}
    </div>
  );
}
