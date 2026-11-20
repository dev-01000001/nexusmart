"use client";

import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";

export function SiteMotion({ depKey }: { depKey?: string | number }) {
  useRevealOnScroll(depKey);
  return null;
}
