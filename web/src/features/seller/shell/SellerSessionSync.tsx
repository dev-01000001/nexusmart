"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { CheckCircle2, Loader2, RefreshCw } from "lucide-react";
import { useSession } from "@/components/SessionProvider";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api, getErrorMessage } from "@/lib/api";
import {
  shouldAttemptClientSessionSync,
  type ClientSessionSyncAttempt,
} from "@/lib/server/sessionRefresh";

const SYNC_STORAGE_KEY = "nm.seller.sessionSyncAttempt";

function readLastSyncAttempt(): ClientSessionSyncAttempt | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(SYNC_STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as ClientSessionSyncAttempt;
  } catch {
    return null;
  }
}

function writeSyncAttempt(userId: string, atMs: number) {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(SYNC_STORAGE_KEY, JSON.stringify({ userId, atMs }));
  } catch {
    // Ignore storage quota/privacy restrictions.
  }
}

function clearSyncAttempt() {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(SYNC_STORAGE_KEY);
  } catch {
    // Ignore storage quota/privacy restrictions.
  }
}

export interface SellerSessionSyncProps {
  userId: string;
  shopName?: string;
}

/**
 * Safety-net client bridge rendered by `SellerLayout` when the backend confirms the user has an
 * ACTIVE seller shop while the browser's HttpOnly access cookie still carries pre-approval claims.
 * Rotates the refresh token ONCE via `POST /api/auth/refresh` (CSRF-protected) and reloads cleanly.
 */
export function SellerSessionSync({ userId, shopName }: SellerSessionSyncProps) {
  const pathname = usePathname() || "/seller";
  const { refreshSession, logout } = useSession();
  const [status, setStatus] = useState<"syncing" | "error">("syncing");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const startedRef = useRef(false);

  const runSync = useCallback(
    async (manual: boolean) => {
      const nowMs = Date.now();
      if (!manual && !shouldAttemptClientSessionSync(readLastSyncAttempt(), userId, nowMs)) {
        setStatus("error");
        setErrorMessage(
          "Phiên người bán chưa đồng bộ được tự động. Vui lòng thử đồng bộ lại hoặc đăng nhập lại."
        );
        return;
      }

      writeSyncAttempt(userId, nowMs);
      setStatus("syncing");
      setErrorMessage(null);
      try {
        await api<{ ok: boolean }>("/api/auth/refresh", { method: "POST" });
        await refreshSession().catch(() => null);
        if (typeof window !== "undefined") {
          const isOverviewRoot = pathname === "/seller" && !window.location.search;
          const target = isOverviewRoot ? "/seller?approved=1" : window.location.href;
          window.location.replace(target);
        }
      } catch (err) {
        setStatus("error");
        setErrorMessage(
          getErrorMessage(err, "Chưa thể làm mới phiên người bán lúc này. Vui lòng thử lại.")
        );
      }
    },
    [pathname, refreshSession, userId]
  );

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    void runSync(false);
  }, [runSync]);

  return (
    <main className="mx-auto flex min-h-svh w-full max-w-md items-center justify-center p-6">
      <Card className="w-full">
        <CardHeader className="space-y-2 text-center">
          <div className="mx-auto flex size-10 items-center justify-center rounded-full bg-badge-success-bg text-badge-success-ink">
            {status === "syncing" ? (
              <Loader2 className="size-5 animate-spin" aria-hidden="true" />
            ) : (
              <CheckCircle2 className="size-5" aria-hidden="true" />
            )}
          </div>
          <CardTitle className="text-base">
            {status === "syncing" ? "Đang đồng bộ quyền gian hàng" : "Gian hàng đã được duyệt"}
          </CardTitle>
          <CardDescription>
            {status === "syncing"
              ? shopName
                ? `Gian hàng "${shopName}" đã được duyệt. Hệ thống đang cập nhật phiên người bán...`
                : "Gian hàng của bạn đã được duyệt. Hệ thống đang cập nhật phiên người bán..."
              : errorMessage || "Nhấn nút bên dưới để làm mới phiên và bắt đầu bán hàng."}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center justify-center gap-2">
          {status === "error" ? (
            <>
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  clearSyncAttempt();
                  void runSync(true);
                }}
              >
                <RefreshCw className="size-4" aria-hidden="true" />
                <span>Đồng bộ lại phiên</span>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void logout("/dang-nhap")}
              >
                Đăng nhập lại
              </Button>
              <Button type="button" variant="ghost" size="sm" asChild>
                <Link href="/">Về trang chủ</Link>
              </Button>
            </>
          ) : (
            <p className="text-xs text-muted-foreground" role="status">
              Trang sẽ tự động tải lại sau giây lát...
            </p>
          )}
        </CardContent>
      </Card>
    </main>
  );
}
