import React from "react";
import Link from "next/link";
import { FileQuestion } from "lucide-react";
import { EmptyState } from "@/components/app/EmptyState";
import { Button } from "@/components/ui/button";
import { SITE_PX } from "@/features/site/shell/siteStyleVars";
import { cn } from "@/lib/utils";

/**
 * Storefront-scoped 404 rendered inside SiteShell's `<main>` (avoids nesting root NotFoundView's
 * `<header>` and `<main>` when a storefront product/page calls `notFound()`).
 */
export default function SiteNotFound() {
  return (
    <section
      className={cn("w-full py-[clamp(48px,7vw,96px)]", SITE_PX)}
      aria-labelledby="site-not-found-title"
    >
      <div className="mx-auto max-w-[600px]">
        <h1
          id="site-not-found-title"
          className="mb-4 text-center text-2xl font-semibold tracking-tight text-foreground"
        >
          Trang này không tồn tại.
        </h1>
        <EmptyState
          icon={FileQuestion}
          title="Không tìm thấy nội dung (404)"
          description="Liên kết bạn truy cập có thể đã thay đổi hoặc sản phẩm hiện không còn hiển thị tại cửa hàng."
          className="border border-border shadow-none"
          action={
            <div className="flex flex-wrap items-center justify-center gap-3">
              <Button asChild size="lg" className="min-h-11 shadow-none">
                <Link href="/">Về trang chủ</Link>
              </Button>
              <Button asChild variant="outline" size="lg" className="min-h-11 shadow-none">
                <Link href="/san-pham">Xem sản phẩm</Link>
              </Button>
            </div>
          }
        />
      </div>
    </section>
  );
}
