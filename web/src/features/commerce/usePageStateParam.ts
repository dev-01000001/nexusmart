"use client";

import { useSearchParams } from "next/navigation";

export type DemoViewState = "loading" | "empty" | "error" | null;

/**
 * Reads optional `?state=loading|empty|error` query parameter for QA and state previews.
 */
export function usePageStateParam(): DemoViewState {
  const params = useSearchParams();
  const raw = params.get("state");
  if (raw === "loading" || raw === "empty" || raw === "error") {
    return raw;
  }
  return null;
}
