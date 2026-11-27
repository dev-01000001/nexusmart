import React from "react";
import Link from "next/link";
import "@/features/platform/home/home-tokens.css";
import { PlatformFooter } from "./PlatformFooter";
import { PlatformHeader } from "./PlatformHeader";
import { platformInter } from "./platformFont";
import { PLATFORM_BTN_PRIMARY, PLATFORM_SHELL_ROOT, PLATFORM_SUB_H2 } from "./platformStyles";

export function NotFoundView() {
  return (
    <div data-platform-public="true" className={`${platformInter.variable} ${PLATFORM_SHELL_ROOT}`}>
      <PlatformHeader />

      <main
        id="main-content"
        tabIndex={-1}
        className="relative flex w-full min-w-0 flex-1 flex-col items-center justify-center overflow-hidden bg-[var(--home-band)] px-[var(--home-gutter)] pt-[calc(var(--home-header-h)+56px)] pb-24 text-center focus:outline-none"
      >
        <div
          className="pointer-events-none absolute inset-0 [background:radial-gradient(circle_at_50%_46%,var(--home-glow-soft)_0%,transparent_54%),repeating-radial-gradient(circle_at_50%_46%,transparent_0,transparent_86px,rgba(102,204,215,0.09)_86px,rgba(102,204,215,0.09)_87px)]"
          aria-hidden="true"
        />

        <div className="relative z-[1] flex max-w-[560px] flex-col items-center gap-6">
          <p
            className="font-[family-name:var(--home-font)] text-[clamp(6.5rem,20vw,15rem)] leading-[0.9] font-extralight tracking-[-0.05em] text-[var(--home-ink)] tabular-nums"
            aria-hidden="true"
          >
            404
          </p>

          <h1 className={`${PLATFORM_SUB_H2} text-[var(--home-ink-2)]`}>
            Trang này không tồn tại.
          </h1>

          <div className="pt-2">
            <Link href="/" className={PLATFORM_BTN_PRIMARY}>
              Về trang chủ
            </Link>
          </div>
        </div>
      </main>

      <PlatformFooter />
    </div>
  );
}
