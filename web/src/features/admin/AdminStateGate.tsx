"use client";

import React from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { StateView } from "@/components";
import { parseStateParam } from "./adminMeta";
import shared from "./adminShared.module.css";

export interface AdminStateGateProps {
  title: string;
  subtitle?: string;
  emptyTitle: string;
  emptyMessage: string;
  errorTitle: string;
  errorMessage: string;
  lastUpdated?: string;
  loadingRows?: number;
  children: (params: { initialQuery: string; initialSelected: string | null }) => React.ReactNode;
}

export function AdminStateGate({
  title,
  subtitle,
  emptyTitle,
  emptyMessage,
  errorTitle,
  errorMessage,
  lastUpdated = "20/11/2026 · 01:12",
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
            {subtitle && <p className={shared.pageSubtitle}>{subtitle}</p>}
          </div>
        </header>

        <div className={shared.stateWrap}>
          {stateParam === "loading" && (
            <StateView
              kind="loading"
              surface="app"
              title="Đang đồng bộ dữ liệu quản trị..."
              rows={loadingRows}
            />
          )}
          {stateParam === "empty" && (
            <StateView
              kind="empty"
              surface="app"
              title={emptyTitle}
              message={emptyMessage}
              actionLabel="Quay lại danh sách mặc định"
              onAction={handleResetState}
            />
          )}
          {stateParam === "error" && (
            <StateView
              kind="error"
              surface="app"
              title={errorTitle}
              message={errorMessage}
              lastUpdated={lastUpdated}
              actionLabel="Thử tải lại"
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
        <StateView kind="loading" surface="app" rows={4} />
      </div>
    </div>
  );
}
