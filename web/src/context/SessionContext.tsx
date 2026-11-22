"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { GatewayUser, SessionResponse } from "@/lib/gateway-types";
import { SESSION_EXPIRED_EVENT, authApi, refreshSessionOnce } from "@/lib/api";

export interface SessionContextValue {
  user: GatewayUser | null;
  isAuthenticated: boolean;
  loading: boolean;
  refreshSession: () => Promise<GatewayUser | null>;
  login: (email: string, password: string) => Promise<GatewayUser>;
  logout: () => Promise<void>;
  setUser: (user: GatewayUser | null) => void;
}

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<GatewayUser | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchSessionProbe = useCallback(async (): Promise<SessionResponse | null> => {
    const res = await fetch("/api/auth/session", {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) {
      return null;
    }
    return (await res.json()) as SessionResponse;
  }, []);

  const refreshSession = useCallback(async (): Promise<GatewayUser | null> => {
    try {
      const data = await fetchSessionProbe();
      if (!data) {
        setUser(null);
        return null;
      }
      if (data.user) {
        setUser(data.user);
        return data.user;
      }
      if (data.can_refresh) {
        const refreshed = await refreshSessionOnce();
        if (refreshed) {
          const retry = await fetchSessionProbe();
          const retryUser = retry?.user ?? null;
          setUser(retryUser);
          return retryUser;
        }
      }
      setUser(null);
      return null;
    } catch {
      return null;
    } finally {
      setLoading(false);
    }
  }, [fetchSessionProbe]);

  useEffect(() => {
    void refreshSession();
  }, [refreshSession]);

  useEffect(() => {
    const onExpired = () => {
      setUser(null);
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, []);

  const login = useCallback(async (email: string, password: string): Promise<GatewayUser> => {
    const data = await authApi<{ user: GatewayUser }>("login", {
      method: "POST",
      json: { email, password },
    });
    setUser(data.user);
    setLoading(false);
    return data.user;
  }, []);

  const logout = useCallback(async (): Promise<void> => {
    try {
      await authApi<{ ok: boolean }>("logout", { method: "POST" });
    } finally {
      setUser(null);
      setLoading(false);
    }
  }, []);

  const value = useMemo<SessionContextValue>(
    () => ({
      user,
      isAuthenticated: Boolean(user),
      loading,
      refreshSession,
      login,
      logout,
      setUser,
    }),
    [user, loading, refreshSession, login, logout]
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const ctx = useContext(SessionContext);
  if (!ctx) {
    throw new Error("useSession must be used within SessionProvider");
  }
  return ctx;
}
