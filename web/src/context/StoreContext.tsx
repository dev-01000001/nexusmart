"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { ApiError, api } from "@/lib/api";
import { DEFAULT_BODY_PROFILE, type BodyProfile } from "@/lib/sizing";
import { useSession } from "@/components/SessionProvider";
import { MockStoreProvider } from "@/mock/MockStoreContext";
import type { GwCart, GwCartItem } from "@/features/commerce/types";

/**
 * Viewer-only browser convenience preferences.
 * STRICT RULE: Never store cart items, orders, auth tokens, addresses, or PII here.
 * Only 4 non-sensitive viewer preferences live here:
 * - bodyProfile: height/weight/shape for client-side size recommendation
 * - wishlist: local product bookmarks (no backend wishlist service in Phase 0b)
 * - recentlyViewed: last 12 visited product IDs
 * - cookiePrefs: cookie consent banner toggles
 */
const VIEWER_PREFS_STORAGE_KEY = "nexusmart_viewer_prefs_v1";

export interface WishlistEntry {
  productId: string;
  skuCode?: string;
  addedAt: string;
}

export interface CookiePreferences {
  essential: true;
  analytics: boolean;
  personalization: boolean;
  updatedAt?: string;
}

export interface AddToCartInput {
  /** Real backend SKU UUID (`ProductSku.skuId`). */
  skuId: string;
  quantity?: number;
  /** Optional active backend FlashSaleItem UUID so checkout reserves flash price/slot. */
  flashSaleItemId?: string;
  /** Optional display metadata for confirmation toast. */
  productName?: string;
  size?: string;
}

export interface CartMutationResult {
  ok: boolean;
  unauthenticated?: boolean;
  clampedToStock?: boolean;
  message?: string;
  cart?: GwCart | null;
}

export interface SaveVoucherResult {
  ok: boolean;
  unauthenticated?: boolean;
  message?: string;
}

export interface StoreContextValue {
  // Real backend cart (single source of truth = /api/cart, never localStorage)
  cart: GwCart | null;
  cartItems: GwCartItem[];
  cartCount: number;
  cartLoading: boolean;
  /** null = initial load pending, false = 401 unauthenticated, true = authenticated */
  cartAuthenticated: boolean | null;
  cartError: string | null;
  refreshCart: () => Promise<GwCart | null>;
  addToCart: (input: AddToCartInput) => Promise<CartMutationResult>;
  updateCartItemQty: (skuId: string, quantity: number) => Promise<CartMutationResult>;
  removeCartItem: (skuId: string) => Promise<CartMutationResult>;
  clearCart: (skuIds?: string[]) => Promise<boolean>;

  // In-memory checkout promotion selections (validated & priced by backend /api/vouchers/preview)
  selectedShopVouchers: Record<string, string | null>;
  platformVoucherId: string | null;
  freeshipVoucherId: string | null;
  useCoins: boolean;
  flashSaleItemBySku: Record<string, string>;
  setShopVoucher: (shopId: string, idOrCode: string | null) => void;
  setPlatformVoucher: (idOrCode: string | null) => void;
  setFreeshipVoucher: (idOrCode: string | null) => void;
  setUseCoins: (enabled: boolean) => void;
  saveVoucher: (idOrCode: string) => Promise<SaveVoucherResult>;
  resetVoucherSelection: () => void;

  // Viewer-only convenience preferences (persisted in localStorage, zero PII/cart/orders/tokens)
  bodyProfile: BodyProfile;
  updateBodyProfile: (next: BodyProfile) => void;
  wishlist: WishlistEntry[];
  toggleWishlist: (productId: string, skuCode?: string) => boolean;
  isWishlisted: (productId: string, skuCode?: string) => boolean;
  recentlyViewed: string[];
  recordProductView: (productId: string) => void;
  clearRecentlyViewed: () => void;
  cookiePrefs: CookiePreferences;
  updateCookiePrefs: (next: Partial<CookiePreferences>) => void;

  // UI shell overlays & toasts
  cookieModalOpen: boolean;
  openCookieModal: () => void;
  closeCookieModal: () => void;
  chatOpen: boolean;
  chatShopId: string | null;
  openChat: (shopId?: string) => void;
  closeChat: () => void;
  aiOpen: boolean;
  openAi: () => void;
  closeAi: () => void;
  toastMessage: string | null;
  showToast: (message: string) => void;
  dismissToast: () => void;
}

const INITIAL_COOKIE_PREFS: CookiePreferences = {
  essential: true,
  analytics: true,
  personalization: true,
};

const StoreContext = createContext<StoreContextValue | null>(null);

export function StoreProvider({ children }: { children: React.ReactNode }) {
  const { user: sessionUser, isAuthenticated: sessionAuthed } = useSession();
  const [hydrated, setHydrated] = useState(false);

  // Real backend cart state
  const [cart, setCart] = useState<GwCart | null>(null);
  const [cartLoading, setCartLoading] = useState<boolean>(sessionAuthed);
  const [cartAuthenticated, setCartAuthenticated] = useState<boolean | null>(
    sessionAuthed ? true : false
  );
  const [cartError, setCartError] = useState<string | null>(null);

  // In-memory voucher/coin/flash selection for active cart & checkout
  const [selectedShopVouchers, setSelectedShopVouchers] = useState<Record<string, string | null>>(
    {}
  );
  const [platformVoucherId, setPlatformVoucherId] = useState<string | null>(null);
  const [freeshipVoucherId, setFreeshipVoucherId] = useState<string | null>(null);
  const [useCoins, setUseCoins] = useState<boolean>(false);
  const [flashSaleItemBySku, setFlashSaleItemBySku] = useState<Record<string, string>>({});

  // Viewer-only convenience preferences
  const [bodyProfile, setBodyProfile] = useState<BodyProfile>(DEFAULT_BODY_PROFILE);
  const [wishlist, setWishlist] = useState<WishlistEntry[]>([]);
  const [recentlyViewed, setRecentlyViewed] = useState<string[]>([]);
  const [cookiePrefs, setCookiePrefs] = useState<CookiePreferences>(INITIAL_COOKIE_PREFS);

  // UI overlays & toast
  const [cookieModalOpen, setCookieModalOpen] = useState(false);
  const [chatOpen, setChatOpen] = useState(false);
  const [chatShopId, setChatShopId] = useState<string | null>(null);
  const [aiOpen, setAiOpen] = useState(false);
  const [toastMessage, setToastMessage] = useState<string | null>(null);

  const showToast = useCallback((message: string) => {
    setToastMessage(message);
  }, []);

  const dismissToast = useCallback(() => {
    setToastMessage(null);
  }, []);

  // Load viewer-only preferences from localStorage after mount (zero cart/orders/tokens/PII)
  useEffect(() => {
    try {
      const raw = window.localStorage.getItem(VIEWER_PREFS_STORAGE_KEY);
      if (raw) {
        const parsed = JSON.parse(raw) as Partial<{
          bodyProfile: BodyProfile;
          wishlist: WishlistEntry[];
          recentlyViewed: string[];
          cookiePrefs: CookiePreferences;
        }>;
        if (parsed.bodyProfile && typeof parsed.bodyProfile.heightCm === "number") {
          setBodyProfile(parsed.bodyProfile);
        }
        if (Array.isArray(parsed.wishlist)) {
          setWishlist(
            parsed.wishlist.filter(
              (w): w is WishlistEntry =>
                Boolean(w) && typeof w.productId === "string" && typeof w.addedAt === "string"
            )
          );
        }
        if (Array.isArray(parsed.recentlyViewed)) {
          setRecentlyViewed(
            parsed.recentlyViewed.filter((id): id is string => typeof id === "string").slice(0, 12)
          );
        }
        if (parsed.cookiePrefs && typeof parsed.cookiePrefs.analytics === "boolean") {
          setCookiePrefs({
            essential: true,
            analytics: parsed.cookiePrefs.analytics,
            personalization: Boolean(parsed.cookiePrefs.personalization),
            updatedAt: parsed.cookiePrefs.updatedAt,
          });
        }
      }
      // Clean up legacy mock store key if present from pre-integration visits
      window.localStorage.removeItem("nexusmart_store_v2");
    } catch {
      // Ignore storage access errors in private browsing / restricted environments
    }
    setHydrated(true);
  }, []);

  // Persist viewer-only preferences after hydration (zero cart/orders/tokens/PII)
  useEffect(() => {
    if (!hydrated) return;
    try {
      window.localStorage.setItem(
        VIEWER_PREFS_STORAGE_KEY,
        JSON.stringify({
          bodyProfile,
          wishlist,
          recentlyViewed,
          cookiePrefs,
        })
      );
    } catch {
      // Ignore storage quota errors
    }
  }, [hydrated, bodyProfile, wishlist, recentlyViewed, cookiePrefs]);

  const refreshCart = useCallback(async (): Promise<GwCart | null> => {
    setCartLoading(true);
    setCartError(null);
    try {
      const data = await api<{ cart: GwCart }>("/api/cart");
      const nextCart = data.cart ?? null;
      setCart(nextCart);
      setCartAuthenticated(true);
      return nextCart;
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setCart(null);
        setCartAuthenticated(false);
        setCartError(null);
        return null;
      }
      const message =
        err instanceof ApiError
          ? err.message
          : "Chưa thể tải giỏ hàng lúc này. Bạn vui lòng thử lại sau.";
      setCartError(message);
      return null;
    } finally {
      setCartLoading(false);
    }
  }, []);

  // Sync real backend cart automatically whenever authenticated session changes
  useEffect(() => {
    if (!sessionUser?.id) {
      setCart(null);
      setCartAuthenticated(false);
      setCartError(null);
      setCartLoading(false);
      setFlashSaleItemBySku({});
      setSelectedShopVouchers({});
      setPlatformVoucherId(null);
      setFreeshipVoucherId(null);
      setUseCoins(false);
      return;
    }
    void refreshCart();
  }, [sessionUser?.id, refreshCart]);

  const cartItems = useMemo<GwCartItem[]>(
    () => (cart?.shop_groups ?? []).flatMap((g) => g.items ?? []),
    [cart]
  );

  const cartCount = useMemo<number>(() => {
    if (!cart) return 0;
    if (typeof cart.total_items === "number") return cart.total_items;
    return cartItems.reduce((sum, it) => sum + it.quantity, 0);
  }, [cart, cartItems]);

  const addToCart = useCallback(
    async (input: AddToCartInput): Promise<CartMutationResult> => {
      const skuId = input.skuId.trim();
      if (!skuId) {
        const message = "Sản phẩm này chưa sẵn sàng để thêm vào giỏ.";
        showToast(message);
        return { ok: false, message };
      }
      const quantity = Math.max(1, Math.trunc(input.quantity ?? 1));
      try {
        const data = await api<{ cart: GwCart; clamped_to_stock?: boolean }>("/api/cart/items", {
          method: "POST",
          body: { sku_id: skuId, quantity },
        });
        setCart(data.cart ?? null);
        setCartAuthenticated(true);
        setCartError(null);
        if (input.flashSaleItemId) {
          setFlashSaleItemBySku((prev) => ({
            ...prev,
            [skuId]: input.flashSaleItemId as string,
          }));
        }
        const label = input.productName
          ? input.size
            ? `${input.productName} (${input.size})`
            : input.productName
          : "sản phẩm";
        const message = data.clamped_to_stock
          ? `Đã thêm ${label} vào giỏ (đã điều chỉnh theo số lượng còn lại trong kho).`
          : `Đã thêm ${label} vào giỏ hàng.`;
        showToast(message);
        return {
          ok: true,
          clampedToStock: Boolean(data.clamped_to_stock),
          message,
          cart: data.cart ?? null,
        };
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          setCart(null);
          setCartAuthenticated(false);
          const message = "Vui lòng đăng nhập để thêm sản phẩm vào giỏ hàng.";
          showToast(message);
          return { ok: false, unauthenticated: true, message };
        }
        const message =
          err instanceof ApiError
            ? err.message
            : "Chưa thể thêm vào giỏ hàng lúc này. Bạn thử lại nhé.";
        showToast(message);
        return { ok: false, message };
      }
    },
    [showToast]
  );

  const updateCartItemQty = useCallback(
    async (skuId: string, quantity: number): Promise<CartMutationResult> => {
      const cleanSkuId = skuId.trim();
      const nextQty = Math.max(0, Math.trunc(quantity));
      try {
        const data = await api<{ cart: GwCart; clamped_to_stock?: boolean }>(
          `/api/cart/items/${encodeURIComponent(cleanSkuId)}`,
          {
            method: "PATCH",
            body: { quantity: nextQty },
          }
        );
        setCart(data.cart ?? null);
        setCartAuthenticated(true);
        if (nextQty === 0) {
          setFlashSaleItemBySku((prev) => {
            if (!(cleanSkuId in prev)) return prev;
            const copy = { ...prev };
            delete copy[cleanSkuId];
            return copy;
          });
        }
        if (data.clamped_to_stock) {
          showToast("Số lượng đã được điều chỉnh theo tồn kho hiện tại.");
        }
        return {
          ok: true,
          clampedToStock: Boolean(data.clamped_to_stock),
          cart: data.cart ?? null,
        };
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          setCart(null);
          setCartAuthenticated(false);
          const message = "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.";
          showToast(message);
          return { ok: false, unauthenticated: true, message };
        }
        const message =
          err instanceof ApiError ? err.message : "Chưa thể cập nhật số lượng lúc này.";
        showToast(message);
        return { ok: false, message };
      }
    },
    [showToast]
  );

  const removeCartItem = useCallback(
    async (skuId: string): Promise<CartMutationResult> => {
      const cleanSkuId = skuId.trim();
      try {
        const data = await api<{ cart: GwCart }>(
          `/api/cart/items/${encodeURIComponent(cleanSkuId)}`,
          { method: "DELETE" }
        );
        setCart(data.cart ?? null);
        setCartAuthenticated(true);
        setFlashSaleItemBySku((prev) => {
          if (!(cleanSkuId in prev)) return prev;
          const copy = { ...prev };
          delete copy[cleanSkuId];
          return copy;
        });
        return { ok: true, cart: data.cart ?? null };
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          setCart(null);
          setCartAuthenticated(false);
          const message = "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.";
          showToast(message);
          return { ok: false, unauthenticated: true, message };
        }
        const message =
          err instanceof ApiError ? err.message : "Chưa thể xóa sản phẩm khỏi giỏ hàng.";
        showToast(message);
        return { ok: false, message };
      }
    },
    [showToast]
  );

  const resetVoucherSelection = useCallback(() => {
    setSelectedShopVouchers({});
    setPlatformVoucherId(null);
    setFreeshipVoucherId(null);
    setUseCoins(false);
  }, []);

  const clearCart = useCallback(
    async (skuIds?: string[]): Promise<boolean> => {
      try {
        const data = await api<{ ok: boolean; cart?: GwCart }>("/api/cart", {
          method: "DELETE",
          body: skuIds && skuIds.length > 0 ? { sku_ids: skuIds } : undefined,
        });
        setCart(data.cart ?? null);
        if (!skuIds || skuIds.length === 0) {
          setFlashSaleItemBySku({});
          resetVoucherSelection();
        }
        return true;
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          setCart(null);
          setCartAuthenticated(false);
        }
        const message =
          err instanceof ApiError ? err.message : "Chưa thể làm trống giỏ hàng lúc này.";
        showToast(message);
        return false;
      }
    },
    [resetVoucherSelection, showToast]
  );

  const setShopVoucher = useCallback((shopId: string, idOrCode: string | null) => {
    setSelectedShopVouchers((prev) => ({ ...prev, [shopId]: idOrCode }));
  }, []);

  const saveVoucher = useCallback(
    async (idOrCode: string): Promise<SaveVoucherResult> => {
      const clean = idOrCode.trim();
      if (!clean) return { ok: false, message: "Mã ưu đãi không hợp lệ." };
      try {
        const data = await api<{ saved: boolean; voucher?: { code?: string } }>(
          `/api/vouchers/${encodeURIComponent(clean)}/save`,
          { method: "POST", body: {} }
        );
        const label = data.voucher?.code || clean;
        const message = `Đã lưu mã ${label} vào ví voucher của bạn.`;
        showToast(message);
        return { ok: true, message };
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          const message = "Vui lòng đăng nhập để lưu mã ưu đãi vào ví của bạn.";
          showToast(message);
          return { ok: false, unauthenticated: true, message };
        }
        const message = err instanceof ApiError ? err.message : "Chưa thể lưu mã ưu đãi lúc này.";
        showToast(message);
        return { ok: false, message };
      }
    },
    [showToast]
  );

  // Viewer-only preference actions
  const updateBodyProfile = useCallback(
    (next: BodyProfile) => {
      setBodyProfile(next);
      showToast(`Đã lưu hồ sơ số đo (${next.heightCm} cm · ${next.weightKg} kg) cho toàn sàn.`);
    },
    [showToast]
  );

  const toggleWishlist = useCallback(
    (productId: string, skuCode?: string): boolean => {
      let added = false;
      setWishlist((prev) => {
        const exists = prev.some(
          (w) => w.productId === productId && (skuCode ? w.skuCode === skuCode : true)
        );
        if (exists) {
          added = false;
          return prev.filter(
            (w) => !(w.productId === productId && (skuCode ? w.skuCode === skuCode : true))
          );
        }
        added = true;
        return [{ productId, skuCode, addedAt: new Date().toISOString() }, ...prev];
      });
      showToast(
        added ? "Đã lưu sản phẩm vào danh sách yêu thích." : "Đã bỏ khỏi danh sách yêu thích."
      );
      return added;
    },
    [showToast]
  );

  const isWishlisted = useCallback(
    (productId: string, skuCode?: string): boolean =>
      wishlist.some((w) => w.productId === productId && (skuCode ? w.skuCode === skuCode : true)),
    [wishlist]
  );

  const recordProductView = useCallback((productId: string) => {
    setRecentlyViewed((prev) => {
      if (prev[0] === productId) return prev;
      return [productId, ...prev.filter((id) => id !== productId)].slice(0, 12);
    });
  }, []);

  const clearRecentlyViewed = useCallback(() => {
    setRecentlyViewed([]);
  }, []);

  const updateCookiePrefs = useCallback(
    (next: Partial<CookiePreferences>) => {
      setCookiePrefs((prev) => ({
        ...prev,
        ...next,
        essential: true,
        updatedAt: new Date().toISOString(),
      }));
      showToast("Đã lưu tùy chọn cookie của bạn.");
    },
    [showToast]
  );

  const openCookieModal = useCallback(() => setCookieModalOpen(true), []);
  const closeCookieModal = useCallback(() => setCookieModalOpen(false), []);

  const openChat = useCallback((shopId?: string) => {
    setAiOpen(false);
    if (shopId) setChatShopId(shopId);
    setChatOpen(true);
  }, []);

  const closeChat = useCallback(() => setChatOpen(false), []);

  const openAi = useCallback(() => {
    setChatOpen(false);
    setAiOpen(true);
  }, []);

  const closeAi = useCallback(() => setAiOpen(false), []);

  const value = useMemo<StoreContextValue>(
    () => ({
      cart,
      cartItems,
      cartCount,
      cartLoading,
      cartAuthenticated,
      cartError,
      refreshCart,
      addToCart,
      updateCartItemQty,
      removeCartItem,
      clearCart,
      selectedShopVouchers,
      platformVoucherId,
      freeshipVoucherId,
      useCoins,
      flashSaleItemBySku,
      setShopVoucher,
      setPlatformVoucher: setPlatformVoucherId,
      setFreeshipVoucher: setFreeshipVoucherId,
      setUseCoins,
      saveVoucher,
      resetVoucherSelection,
      bodyProfile,
      updateBodyProfile,
      wishlist,
      toggleWishlist,
      isWishlisted,
      recentlyViewed,
      recordProductView,
      clearRecentlyViewed,
      cookiePrefs,
      updateCookiePrefs,
      cookieModalOpen,
      openCookieModal,
      closeCookieModal,
      chatOpen,
      chatShopId,
      openChat,
      closeChat,
      aiOpen,
      openAi,
      closeAi,
      toastMessage,
      showToast,
      dismissToast,
    }),
    [
      cart,
      cartItems,
      cartCount,
      cartLoading,
      cartAuthenticated,
      cartError,
      refreshCart,
      addToCart,
      updateCartItemQty,
      removeCartItem,
      clearCart,
      selectedShopVouchers,
      platformVoucherId,
      freeshipVoucherId,
      useCoins,
      flashSaleItemBySku,
      setShopVoucher,
      saveVoucher,
      resetVoucherSelection,
      bodyProfile,
      updateBodyProfile,
      wishlist,
      toggleWishlist,
      isWishlisted,
      recentlyViewed,
      recordProductView,
      clearRecentlyViewed,
      cookiePrefs,
      updateCookiePrefs,
      cookieModalOpen,
      openCookieModal,
      closeCookieModal,
      chatOpen,
      chatShopId,
      openChat,
      closeChat,
      aiOpen,
      openAi,
      closeAi,
      toastMessage,
      showToast,
      dismissToast,
    ]
  );

  return (
    <StoreContext.Provider value={value}>
      <MockStoreProvider notify={showToast}>{children}</MockStoreProvider>
    </StoreContext.Provider>
  );
}

export function useStore(): StoreContextValue {
  const ctx = useContext(StoreContext);
  if (!ctx) {
    throw new Error("useStore phải được gọi bên trong StoreProvider.");
  }
  return ctx;
}
