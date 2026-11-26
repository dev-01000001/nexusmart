import { Inter } from "next/font/google";
import React from "react";
import { PlatformFooter } from "@/features/platform/PlatformFooter";
import { PlatformHeader } from "@/features/platform/PlatformHeader";

// Keep `axes: ["opsz"]` until `web/src/lib/fonts.ts` adds it to the root Inter declaration,
// otherwise Inter Display optical sizing (>14px) shifts `/` hero & heading widths.
const homeInter = Inter({
  subsets: ["latin", "vietnamese"],
  axes: ["opsz"],
  variable: "--font-inter",
  display: "swap",
});

export default function PlatformPublicLayout({ children }: { children: React.ReactNode }) {
  return (
    <div
      className={`${homeInter.variable} flex min-h-svh flex-col bg-background text-foreground`}
      style={
        {
          "--home-font": "var(--font-inter), system-ui, -apple-system, sans-serif",
        } as React.CSSProperties
      }
    >
      <PlatformHeader />
      <main id="main-content" tabIndex={-1} className="w-full min-w-0 flex-1 focus:outline-none">
        {children}
      </main>
      <PlatformFooter />
    </div>
  );
}
