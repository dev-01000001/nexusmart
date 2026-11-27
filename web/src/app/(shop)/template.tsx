import React from "react";
import { PageTransition } from "@/components/PageTransition";

export default function ShopTemplate({ children }: { children: React.ReactNode }) {
  return (
    <PageTransition>
      <main id="main-content" tabIndex={-1} className="w-full min-w-0 flex-1 focus:outline-none">
        {children}
      </main>
    </PageTransition>
  );
}
