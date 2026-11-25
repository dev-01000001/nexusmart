import React from "react";
import type { StatusTone } from "@/data/orders";
import { formatDateTimeVi } from "@/lib/format";
import shared from "./commerce.module.css";

export function statusToneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

export function renderOrderDateTime(iso: string): React.ReactNode {
  const formatted = formatDateTimeVi(iso);
  const parts = formatted.includes(", ") ? formatted.split(", ") : formatted.split(" · ");
  if (parts.length !== 2) return formatted;
  return (
    <span className={`${shared.dateParts} num`}>
      <span>{parts[0]}</span>
      <span>{parts[1]}</span>
    </span>
  );
}
