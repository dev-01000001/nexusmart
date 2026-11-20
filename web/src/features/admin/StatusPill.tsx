import React from "react";
import type { PillTone } from "./adminMeta";
import shared from "./adminShared.module.css";

const TONE_CLASS: Record<PillTone, string> = {
  positive: shared.pillPositive,
  negative: shared.pillNegative,
  warning: shared.pillWarning,
  neutral: shared.pillNeutral,
};

export interface StatusPillProps {
  label: string;
  tone: PillTone;
}

export function StatusPill({ label, tone }: StatusPillProps) {
  return <span className={`${shared.pill} ${TONE_CLASS[tone]}`}>{label}</span>;
}
