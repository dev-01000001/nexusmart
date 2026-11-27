import React from "react";
import Link from "next/link";
import { NexusMark } from "@/components/Icons";
import { UiScope } from "@/components/ui/ui-scope";

const LINK_CLASS =
  "inline-flex min-h-6 items-center text-[length:var(--home-fs-small)] leading-[1.4] font-[420] [overflow-wrap:anywhere] text-[var(--home-ink-3)] transition-colors duration-200 hover:text-[var(--home-ink)]";

const COL_TITLE_CLASS =
  "text-[length:var(--home-fs-h3)] leading-[1.3] font-[450] tracking-[-0.01em] text-[var(--home-ink)]";

export function PlatformFooter() {
  return (
    <UiScope>
      <footer className="w-full bg-[var(--home-bg)] pt-[72px] pb-8 font-[family-name:var(--home-font)] text-[var(--home-ink)] min-[601px]:pt-[var(--home-space-section)] min-[601px]:pb-12 [&_:focus-visible]:outline-2 [&_:focus-visible]:outline-offset-2 [&_:focus-visible]:outline-[var(--home-accent)]">
        <div className="mx-auto flex w-[min(var(--home-max),calc(100%-2*var(--home-gutter)))] flex-col gap-12 min-[601px]:gap-[var(--home-space-head)]">
          <div className="grid grid-cols-1 items-start gap-12 min-[901px]:grid-cols-[minmax(120px,1.15fr)_minmax(0,3.85fr)] min-[901px]:gap-x-[var(--home-gap)] min-[901px]:gap-y-12">
            <div className="flex items-start">
              <Link
                href="/"
                className="inline-flex min-h-12 min-w-12 items-center justify-center text-[var(--home-ink)] transition-opacity duration-200 hover:opacity-[0.78]"
                aria-label="NexusMart trang chủ"
              >
                <NexusMark size={32} accentColor="var(--home-ink)" />
              </Link>
            </div>

            <nav
              className="grid grid-cols-2 gap-x-4 gap-y-8 min-[601px]:gap-x-5 min-[901px]:grid-cols-4 min-[901px]:gap-x-[var(--home-gap)]"
              aria-label="Liên kết chân trang"
            >
              <div className="flex min-w-0 flex-col gap-4">
                <p className={COL_TITLE_CLASS}>NexusMart</p>
                <ul className="flex flex-col gap-3">
                  <li>
                    <Link href="/ve-nexusmart" className={LINK_CLASS}>
                      Về NexusMart
                    </Link>
                  </li>
                  <li>
                    <Link href="/tro-giup" className={LINK_CLASS}>
                      Trợ giúp
                    </Link>
                  </li>
                </ul>
              </div>

              <div className="flex min-w-0 flex-col gap-4">
                <p className={COL_TITLE_CLASS}>Người bán</p>
                <ul className="flex flex-col gap-3">
                  <li>
                    <Link href="/dang-ky?role=seller" className={LINK_CLASS}>
                      Mở gian hàng
                    </Link>
                  </li>
                  <li>
                    <Link href="/seller" className={LINK_CLASS}>
                      Kênh người bán
                    </Link>
                  </li>
                  <li>
                    <Link href="/dang-nhap" className={LINK_CLASS}>
                      Đăng nhập
                    </Link>
                  </li>
                </ul>
              </div>

              <div className="flex min-w-0 flex-col gap-4">
                <p className={COL_TITLE_CLASS}>Mua sắm</p>
                <ul className="flex flex-col gap-3">
                  <li>
                    <Link href="/#danh-ba-gian-hang" className={LINK_CLASS}>
                      Các cửa hàng
                    </Link>
                  </li>
                </ul>
              </div>

              <div className="flex min-w-0 flex-col gap-4">
                <p className={COL_TITLE_CLASS}>Liên hệ</p>
                <ul className="flex flex-col gap-3">
                  <li>
                    <a href="mailto:hotro@nexusmart.vn" className={LINK_CLASS}>
                      hotro@nexusmart.vn
                    </a>
                  </li>
                </ul>
              </div>
            </nav>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-t border-[var(--home-border)] pt-8">
            <p className="text-[length:var(--home-fs-small)] leading-[1.5] font-[420] text-[var(--home-ink-3)] tabular-nums">
              © 2026 NexusMart · Nền tảng cửa hàng độc lập
            </p>
          </div>
        </div>
      </footer>
    </UiScope>
  );
}
