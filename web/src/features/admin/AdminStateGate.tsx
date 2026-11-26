"use client";

import React from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Inbox } from "lucide-react";
import { EmptyState, ErrorState, PageHeader } from "@/components/app";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { parseStateParam } from "./adminMeta";

export interface AdminStateGateProps {
  title: string;
  emptyTitle: string;
  emptyMessage: string;
  errorTitle: string;
  errorMessage: string;
  loadingRows?: number;
  children: (params: { initialQuery: string; initialSelected: string | null }) => React.ReactNode;
}

function TableLoadingSkeleton({ rows }: { rows: number }) {
  const count = Math.max(1, Math.min(rows, 10));
  return (
    <div role="status" aria-live="polite" className="space-y-3 rounded-xl bg-card p-5 shadow-card">
      <p className="text-[13px] text-muted-foreground">Đang đồng bộ dữ liệu...</p>
      <div className="space-y-2.5">
        {Array.from({ length: count }).map((_, idx) => (
          <div key={idx} className="flex items-center gap-3">
            <Skeleton className="h-9 flex-1 rounded-md" />
            <Skeleton className="h-9 w-28 rounded-md" />
            <Skeleton className="hidden h-9 w-24 rounded-md sm:block" />
          </div>
        ))}
      </div>
    </div>
  );
}

export function AdminStateGate({
  title,
  emptyTitle,
  emptyMessage,
  errorTitle,
  errorMessage,
  loadingRows = 5,
  children,
}: AdminStateGateProps) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  const stateParam = parseStateParam(searchParams.get("state"));
  const initialQuery = searchParams.get("q") ?? "";
  const initialSelected = searchParams.get("selected");

  const handleResetState = () => {
    router.replace(pathname);
  };

  if (stateParam) {
    return (
      <div className="space-y-5">
        <PageHeader title={title} />

        <div>
          {stateParam === "loading" && <TableLoadingSkeleton rows={loadingRows} />}
          {stateParam === "empty" && (
            <EmptyState
              icon={Inbox}
              title={emptyTitle}
              description={emptyMessage}
              action={
                <Button type="button" variant="outline" size="sm" onClick={handleResetState}>
                  Quay lại danh sách
                </Button>
              }
            />
          )}
          {stateParam === "error" && (
            <ErrorState title={errorTitle} description={errorMessage} onRetry={handleResetState} />
          )}
        </div>
      </div>
    );
  }

  return <>{children({ initialQuery, initialSelected })}</>;
}

export function AdminSuspenseFallback({ title }: { title: string }) {
  return (
    <div className="space-y-5">
      <PageHeader title={title} />
      <TableLoadingSkeleton rows={4} />
    </div>
  );
}
