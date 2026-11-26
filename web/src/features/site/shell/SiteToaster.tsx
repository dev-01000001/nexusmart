"use client";

import dynamic from "next/dynamic";

const SiteToasterInner = dynamic(
  () => import("./SiteToasterInner").then((mod) => mod.SiteToasterInner),
  { ssr: false }
);

export function SiteToaster() {
  return <SiteToasterInner />;
}
