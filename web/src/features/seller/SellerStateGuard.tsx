"use client";

import React, { Suspense } from "react";
import { StateView } from "@/components";

export function SellerSuspense({ children }: { children: React.ReactNode }) {
  return (
    <Suspense
      fallback={
        <StateView kind="loading" surface="app" title="Đang tải dữ liệu gian hàng..." rows={5} />
      }
    >
      {children}
    </Suspense>
  );
}
