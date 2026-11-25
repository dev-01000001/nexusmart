"use client";

import React from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { StateView } from "@/components";
import { parseStateParam } from "./adminMeta";
import shared from "./adminShared.module.css";

export interface AdminStateGateProps {
  title: string;
  emptyTitle: string;
  emptyMessage: string;
  errorTitle: string;
  errorMessage: string;
  loadingRows?: number;
  children: (params: { initialQuery: string; initialSelected: string | null }) => React.ReactNode;
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
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>{title}</h1>
          </div>
        </header>

        <div className={shared.stateWrap}>
          {stateParam === "loading" && (
            <StateView
              kind="loading"
              surface="app"
              skeletonVariant="table"
              title="Đang đồng bộ dữ liệu..."
              rows={loadingRows}
            />
          )}
          {stateParam === "empty" && (
            <StateView
              kind="empty"
              surface="app"
              title={emptyTitle}
              message={emptyMessage}
              actionLabel="Quay lại danh sách"
              onAction={handleResetState}
            />
          )}
          {stateParam === "error" && (
            <StateView
              kind="error"
              surface="app"
              title={errorTitle}
              message={errorMessage}
              actionLabel="Thử lại"
              onAction={handleResetState}
            />
          )}
        </div>
      </div>
    );
  }

  return <>{children({ initialQuery, initialSelected })}</>;
}

export function AdminSuspenseFallback({ title }: { title: string }) {
  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>{title}</h1>
        </div>
      </header>
      <div className={shared.stateWrap}>
        <StateView kind="loading" surface="app" skeletonVariant="table" rows={4} />
      </div>
    </div>
  );
}
