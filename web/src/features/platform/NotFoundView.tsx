import React from "react";
import Link from "next/link";
import { NexusMark } from "@/components/Icons";
import { Button } from "@/components/ui/button";
import { UiScope } from "@/components/ui/ui-scope";

export function NotFoundView() {
  return (
    <UiScope className="flex min-h-svh flex-col bg-background px-4 pt-5 pb-12 text-foreground sm:px-6 lg:px-8">
      <header className="flex min-h-11 items-center">
        <Link
          href="/"
          className="inline-flex min-h-10 items-center gap-2.5 text-foreground transition-opacity hover:opacity-80"
          aria-label="NexusMart trang chủ"
        >
          <NexusMark size={20} accentColor="var(--ui-brand)" />
          <span className="text-base font-semibold tracking-tight">NexusMart</span>
        </Link>
      </header>

      <main className="flex flex-1 flex-col items-center justify-center gap-5 py-12 text-center">
        <p
          className="text-[clamp(6.5rem,22vw,16rem)] leading-[0.94] font-bold tracking-[-0.04em] text-foreground tabular-nums"
          aria-hidden="true"
        >
          404
        </p>

        <h1 className="text-xl leading-7 font-semibold tracking-tight text-muted-foreground sm:text-2xl">
          Trang này không tồn tại.
        </h1>

        <div className="pt-2">
          <Button size="lg" className="min-h-11 px-6" asChild>
            <Link href="/">Về trang chủ</Link>
          </Button>
        </div>
      </main>
    </UiScope>
  );
}
