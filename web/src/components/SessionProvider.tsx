"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { usePathname } from "next/navigation";
import { SESSION_EXPIRED_EVENT, api } from "@/lib/api";
import type { Role, SessionUser } from "@/lib/server/session";
import { safeInternalPath } from "@/features/account/accountTypes";

export type { Role, SessionUser };

export interface SessionContextValue {
  user: SessionUser | null;
  isAuthenticated: boolean;
  loading: boolean;
  /** Re-sync session from GET /api/auth/me (same-origin BFF, HttpOnly cookies). */
  refreshSession: () => Promise<SessionUser | null>;
  /** Update client session immediately after login / profile PATCH without extra round-trip. */
  setUser: (user: SessionUser | null) => void;
  /** Revoke session on backend via POST /api/auth/logout, clear cookies, and hard-navigate to drop router cache. */
  logout: (redirectTo?: string) => Promise<void>;
}

const SessionContext = createContext<SessionContextValue | null>(null);

const PROTECTED_PREFIXES = ["/tai-khoan", "/don-hang", "/thanh-toan/cho", "/seller", "/admin"];

function normalizeRoutePathname(pathname: string | null): string {
  if (!pathname) return "/";
  const stripped = pathname.replace(/^\/s\/[^/]+(?=\/|$)/, "");
  return stripped || "/";
}

function isProtectedRoute(pathname: string | null): boolean {
  if (!pathname) return false;
  const clean = normalizeRoutePathname(pathname);
  return PROTECTED_PREFIXES.some((p) => clean === p || clean.startsWith(`${p}/`));
}

export function SessionProvider({
  initialUser,
  children,
}: {
  initialUser: SessionUser | null;
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const [user, setUser] = useState<SessionUser | null>(initialUser);
  const [loading, setLoading] = useState(false);

  // Sync client state whenever the server re-renders RootLayout (e.g. after router.refresh()).
  useEffect(() => {
    setUser(initialUser);
  }, [initialUser]);

  // Clear local session state when an authenticated API call fails after single-flight refresh.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onExpired = () => {
      setUser(null);
      void fetch("/api/auth/logout", { method: "POST" }).catch(() => {});
      const cleanPath = normalizeRoutePathname(pathname);
      if (isProtectedRoute(cleanPath)) {
        const nextPath = safeInternalPath(`${cleanPath}${window.location.search}`, "/");
        window.location.assign(`/dang-nhap?expired=1&next=${encodeURIComponent(nextPath)}`);
      }
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, [pathname]);

  const refreshSession = useCallback(async (): Promise<SessionUser | null> => {
    setLoading(true);
    try {
      const res = await api<{ user: SessionUser | null }>("/api/auth/me");
      const next = res.user ?? null;
      setUser(next);
      return next;
    } catch {
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  const logout = useCallback(
    async (redirectTo?: string) => {
      setLoading(true);
      let navigating = false;
      try {
        await api<{ ok: boolean }>("/api/auth/logout", { method: "POST" });
      } catch {
        // Still clear local session UI state if offline; cookies are cleared server-side.
      } finally {
        setUser(null);
        const cleanPath = normalizeRoutePathname(pathname);
        const currentSearch = typeof window !== "undefined" ? window.location.search : "";
        const fallback = isProtectedRoute(cleanPath)
          ? "/dang-nhap"
          : safeInternalPath(`${cleanPath}${currentSearch}`, "/");
        const target = safeInternalPath(redirectTo, fallback);
        if (typeof window !== "undefined") {
          navigating = true;
          const nextUrl = new URL(target, window.location.href);
          const sameDocumentHash =
            nextUrl.origin === window.location.origin &&
            nextUrl.pathname === window.location.pathname &&
            nextUrl.search === window.location.search &&
            (nextUrl.hash !== "" || target.includes("#"));
          window.location.assign(target);
          if (sameDocumentHash) {
            window.location.reload();
          }
        }
        if (!navigating) {
          setLoading(false);
        }
      }
    },
    [pathname]
  );

  const value = useMemo<SessionContextValue>(
    () => ({
      user,
      isAuthenticated: Boolean(user?.id),
      loading,
      refreshSession,
      setUser,
      logout,
    }),
    [user, loading, refreshSession, logout]
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const ctx = useContext(SessionContext);
  if (!ctx) {
    throw new Error("useSession phải được gọi bên trong SessionProvider.");
  }
  return ctx;
}
