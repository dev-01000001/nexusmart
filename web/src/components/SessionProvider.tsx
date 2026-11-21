"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { api } from "@/lib/api";
import type { Role, SessionUser } from "@/lib/server/session";

export type { Role, SessionUser };

export interface SessionContextValue {
  user: SessionUser | null;
  isAuthenticated: boolean;
  loading: boolean;
  /** Re-sync session from GET /api/auth/me (same-origin BFF, HttpOnly cookies). */
  refreshSession: () => Promise<SessionUser | null>;
  /** Update client session immediately after login / profile PATCH without extra round-trip. */
  setUser: (user: SessionUser | null) => void;
  /** Revoke session on backend via POST /api/auth/logout, clear cookies, and refresh router. */
  logout: (redirectTo?: string) => Promise<void>;
}

const SessionContext = createContext<SessionContextValue | null>(null);

const PROTECTED_PREFIXES = ["/tai-khoan", "/don-hang", "/thanh-toan/cho", "/seller", "/admin"];

function isProtectedRoute(pathname: string | null): boolean {
  if (!pathname) return false;
  return PROTECTED_PREFIXES.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}

export function SessionProvider({
  initialUser,
  children,
}: {
  initialUser: SessionUser | null;
  children: React.ReactNode;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const [user, setUser] = useState<SessionUser | null>(initialUser);
  const [loading, setLoading] = useState(false);

  // Sync client state whenever the server re-renders RootLayout (e.g. after router.refresh()).
  useEffect(() => {
    setUser(initialUser);
  }, [initialUser]);

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
      try {
        await api<{ ok: boolean }>("/api/auth/logout", { method: "POST" });
      } catch {
        // Still clear local session UI state if offline; cookies are cleared server-side.
      } finally {
        setUser(null);
        setLoading(false);
        const target = redirectTo ?? (isProtectedRoute(pathname) ? "/dang-nhap" : undefined);
        if (target) {
          router.push(target);
        }
        router.refresh();
      }
    },
    [pathname, router]
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
