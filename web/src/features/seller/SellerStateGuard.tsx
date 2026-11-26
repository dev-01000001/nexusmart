"use client";

import React, { Suspense } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

export function SellerSuspense({ children }: { children: React.ReactNode }) {
  return (
    <Suspense
      fallback={
        <Card role="status" aria-label="Đang tải dữ liệu...">
          <CardContent className="space-y-3 py-5">
            <Skeleton className="h-5 w-48" />
            <div className="space-y-2.5 pt-2">
              {Array.from({ length: 5 }).map((_, idx) => (
                <Skeleton key={idx} className="h-10 w-full" />
              ))}
            </div>
          </CardContent>
        </Card>
      }
    >
      {children}
    </Suspense>
  );
}
