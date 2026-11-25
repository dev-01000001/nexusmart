"use client";

import React, { Suspense } from "react";
import { StateView } from "@/components";

export function SellerSuspense({ children }: { children: React.ReactNode }) {
  return (
    <Suspense
      fallback={
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải dữ liệu..."
          rows={5}
        />
      }
    >
      {children}
    </Suspense>
  );
}
