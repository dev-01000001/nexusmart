/** Shared dark editorial Tailwind recipes matching PlatformHome (`/`). */

export const PLATFORM_SHELL_ROOT =
  "flex min-h-svh flex-col bg-[var(--home-bg)] font-[family-name:var(--home-font)] text-[var(--home-ink)] [html:has(&)]:bg-[#000000] [html:has(&)]:text-[#ffffff] [html:has(&)]:[color-scheme:dark] [html:has(&)_body]:bg-[#000000] [html:has(&)_body]:text-[#ffffff] [&_:focus-visible]:[outline-offset:2px]! [&_:focus-visible]:[outline:2px_solid_var(--home-accent)]!";

export const PLATFORM_CONTAINER =
  "mx-auto w-[min(var(--home-max),calc(100%-2*var(--home-gutter)))]";

export const PLATFORM_DISPLAY_H1 =
  "font-[family-name:var(--home-font)] text-[length:var(--home-fs-display)] font-light leading-[1.08] tracking-[-0.025em] text-[var(--home-ink)] [text-wrap:balance]";

export const PLATFORM_PAGE_H1 =
  "font-[family-name:var(--home-font)] text-[length:var(--home-fs-h2)] font-[330] leading-[1.08] tracking-[-0.03em] text-[var(--home-ink)] [text-wrap:balance]";

export const PLATFORM_SECTION_H2 =
  "font-[family-name:var(--home-font)] text-[length:var(--home-fs-h2)] font-[330] leading-[1.08] tracking-[-0.03em] text-[var(--home-ink)] [text-wrap:balance]";

export const PLATFORM_SUB_H2 =
  "font-[family-name:var(--home-font)] text-[length:var(--home-fs-sub)] font-[330] leading-[1.12] tracking-[-0.02em] text-[var(--home-ink)] [text-wrap:balance]";

export const PLATFORM_LEAD =
  "text-[length:var(--home-fs-lead)] font-normal leading-[1.5] text-[var(--home-ink-2)]";

export const PLATFORM_BTN_PRIMARY =
  "inline-flex min-h-[var(--home-btn-mid-h)] items-center justify-center gap-2 whitespace-nowrap rounded-full bg-[var(--home-ink)] px-7 py-3 text-[length:var(--home-fs-body)] font-[550] text-[var(--home-bg)] transition-[background-color,transform] duration-200 ease-[cubic-bezier(0.22,1,0.36,1)] hover:bg-[#eff3f5] active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100";

export const PLATFORM_BTN_OUTLINE =
  "inline-flex min-h-[var(--home-btn-mid-h)] items-center justify-center gap-2 whitespace-nowrap rounded-full border-[1.5px] border-white/80 bg-[rgba(2,9,10,0.28)] px-7 py-3 text-[length:var(--home-fs-body)] font-medium text-[var(--home-ink)] transition-[background-color,border-color,transform] duration-200 ease-[cubic-bezier(0.22,1,0.36,1)] hover:border-[var(--home-ink)] hover:bg-[var(--home-glass-strong)] active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100";

export const PLATFORM_BTN_GLASS =
  "inline-flex min-h-[var(--home-btn-mid-h)] items-center justify-center gap-2 whitespace-nowrap rounded-full border border-[var(--home-border-strong)] bg-[var(--home-glass)] px-6 py-3 text-[length:var(--home-fs-body)] font-medium text-[var(--home-ink)] transition-[background-color,border-color,transform] duration-200 ease-[cubic-bezier(0.22,1,0.36,1)] hover:border-[var(--home-ink)] hover:bg-[var(--home-glass-strong)] active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100";

export const PLATFORM_OVERLAP_BAND =
  "relative overflow-hidden rounded-t-[var(--home-radius-band)] -mt-[var(--home-radius-band)] [@media(max-width:960px)]:-mt-[var(--home-radius-band-sm)] [@media(max-width:960px)]:rounded-t-[var(--home-radius-band-sm)]";
