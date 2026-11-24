"use client";

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  GatewayCart,
  GatewayCartResponse,
  GatewaySaveVoucherResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { shouldClearUserClientState } from "@/features/checkout/helpers";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import {
  type CartItem,
  type CartShopGroup,
  computeCartSummary,
  mapGatewayCartGroups,
} from "@/features/cart/cartHelpers";

export type { CartItem, CartShopGroup };

export interface AddToCartInput {
  skuId: string;
  quantity: number;
}

export interface AddToCartResult {
  ok: boolean;
  clampedToStock?: boolean;
  message?: string;
}

export interface CartContextValue {
  // Real backend cart state
  cartItems: CartItem[];
  shopGroups: CartShopGroup[];
  cartCount: number;
  totalVnd: number;
  selectedItems: CartItem[];
  selectedShopGroups: CartShopGroup[];
  selectedCount: number;
  selectedSubtotalVnd: number;
  cartLoading: boolean;
  cartInitialized: boolean;
  cartError: string | null;
  clampedSkuNotices: Record<string, string>;

  // Backend cart mutations
  refreshCart: () => Promise<void>;
  addToCart: (input: AddToCartInput) => Promise<AddToCartResult>;
  updateCartQuantity: (skuId: string, quantity: number) => Promise<AddToCartResult>;
  removeFromCart: (skuId: string) => Promise<boolean>;
  clearCart: (skuIds?: string[]) => Promise<boolean>;

  // In-memory selection (not persisted; backend cart does not store item selection)
  toggleCartItemSelected: (skuId: string) => void;
  toggleShopSelected: (shopId: string, selected: boolean) => void;
  toggleSelectAllCart: (selected: boolean) => void;

  // In-memory voucher & coin choices shared between /gio-hang and /thanh-toan
  selectedShopVouchers: Record<string, string | null>;
  platformVoucherCode: string | null;
  freeshipVoucherCode: string | null;
  useCoins: boolean;
  setShopVoucher: (shopId: string, code: string | null) => void;
  setPlatformVoucher: (code: string | null) => void;
  setFreeshipVoucher: (code: string | null) => void;
  setUseCoins: (enabled: boolean) => void;
  resetPromotions: () => void;

  // In-memory opt-out of Flash Sale per SKU (explicit "Mua giá thường" choice shared between /gio-hang and /thanh-toan)
  optedOutFlashSkuIds: Set<string>;
  optOutFlashForSku: (skuId: string) => void;
  restoreFlashForSku: (skuId: string) => void;

  // Voucher saving via real backend POST /vouchers/{id}/save
  savedVoucherCodes: string[];
  saveVoucherCode: (codeOrId: string) => Promise<boolean>;
}

const CartContext = createContext<CartContextValue | null>(null);

export function CartProvider({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, loading: sessionLoading, user } = useSession();
  const { showToast } = useMockStore();

  const [rawCart, setRawCart] = useState<GatewayCart | null>(null);
  const [cartLoading, setCartLoading] = useState<boolean>(false);
  const [cartInitialized, setCartInitialized] = useState<boolean>(false);
  const [cartError, setCartError] = useState<string | null>(null);
  const [deselectedSkuIds, setDeselectedSkuIds] = useState<Set<string>>(() => new Set());
  const [clampedSkuNotices, setClampedSkuNotices] = useState<Record<string, string>>({});

  // Promotion selections live strictly in memory (not persisted in browser storage)
  const [selectedShopVouchers, setSelectedShopVouchers] = useState<Record<string, string | null>>(
    {}
  );
  const [platformVoucherCode, setPlatformVoucherCode] = useState<string | null>(null);
  const [freeshipVoucherCode, setFreeshipVoucherCode] = useState<string | null>(null);
  const [useCoins, setUseCoins] = useState<boolean>(false);
  const [optedOutFlashSkuIds, setOptedOutFlashSkuIds] = useState<Set<string>>(() => new Set());
  const [savedVoucherCodes, setSavedVoucherCodes] = useState<string[]>([]);
  const prevUserIdRef = useRef<string | null>(null);

  const applyCartSnapshot = useCallback((nextCart: GatewayCart | null) => {
    setRawCart(nextCart);
    const validIds = new Set<string>();
    for (const group of nextCart?.shop_groups ?? []) {
      for (const item of group.items ?? []) {
        validIds.add(item.sku_id);
      }
    }
    setDeselectedSkuIds((prev) => {
      if (prev.size === 0 || !nextCart) return prev.size === 0 ? prev : new Set();
      const next = new Set<string>();
      for (const id of prev) {
        if (validIds.has(id)) next.add(id);
      }
      return next.size === prev.size ? prev : next;
    });
    setOptedOutFlashSkuIds((prev) => {
      if (prev.size === 0 || !nextCart) return prev.size === 0 ? prev : new Set();
      const next = new Set<string>();
      for (const id of prev) {
        if (validIds.has(id)) next.add(id);
      }
      return next.size === prev.size ? prev : next;
    });
  }, []);

  const refreshCart = useCallback(async () => {
    if (!isAuthenticated) {
      applyCartSnapshot(null);
      setCartError(null);
      setCartLoading(false);
      setCartInitialized(true);
      return;
    }

    setCartLoading(true);
    setCartError(null);
    try {
      const data = await api<{ cart: GatewayCart }>("cart");
      applyCartSnapshot(data?.cart ?? null);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        applyCartSnapshot(null);
        setCartError(null);
      } else {
        setCartError(getErrorMessage(err, "Chưa thể tải giỏ hàng lúc này. Vui lòng thử lại."));
      }
    } finally {
      setCartLoading(false);
      setCartInitialized(true);
    }
  }, [applyCartSnapshot, isAuthenticated]);

  useEffect(() => {
    if (sessionLoading) return;
    const nextUserId = isAuthenticated ? (user?.id ?? null) : null;
    const mustClear = shouldClearUserClientState(prevUserIdRef.current, nextUserId);
    prevUserIdRef.current = nextUserId;

    if (!isAuthenticated || mustClear) {
      setClampedSkuNotices({});
      setSelectedShopVouchers({});
      setPlatformVoucherCode(null);
      setFreeshipVoucherCode(null);
      setUseCoins(false);
      setOptedOutFlashSkuIds(new Set());
      setSavedVoucherCodes([]);
      setDeselectedSkuIds(new Set());
    }

    if (!isAuthenticated) {
      applyCartSnapshot(null);
      setCartError(null);
      setCartLoading(false);
      setCartInitialized(true);
      return;
    }
    setCartInitialized(false);
    void refreshCart();
  }, [sessionLoading, isAuthenticated, user?.id, refreshCart, applyCartSnapshot]);

  const shopGroups = useMemo<CartShopGroup[]>(() => {
    if (!rawCart?.shop_groups) return [];
    const prevSelected: Record<string, boolean> = {};
    for (const g of rawCart.shop_groups) {
      for (const it of g.items ?? []) {
        prevSelected[it.sku_id] = !deselectedSkuIds.has(it.sku_id);
      }
    }
    return mapGatewayCartGroups(rawCart.shop_groups, prevSelected);
  }, [rawCart, deselectedSkuIds]);

  const cartSummary = useMemo(() => computeCartSummary(shopGroups), [shopGroups]);
  const cartItems = cartSummary.items;
  const selectedItems = cartSummary.selectedItems;
  const selectedShopGroups = cartSummary.selectedShopGroups;
  const cartCount = cartSummary.availableItemCount;
  const totalVnd = useMemo(
    () =>
      cartItems
        .filter((it) => !it.unavailable)
        .reduce((sum, item) => sum + item.priceVnd * item.quantity, 0),
    [cartItems]
  );
  const selectedCount = cartSummary.selectedCount;
  const selectedSubtotalVnd = cartSummary.selectedSubtotalVnd;

  const addToCart = useCallback(
    async (input: AddToCartInput): Promise<AddToCartResult> => {
      if (!isAuthenticated) {
        return {
          ok: false,
          message: "Vui lòng đăng nhập để thêm sản phẩm vào giỏ hàng.",
        };
      }

      const skuId = input.skuId.trim();
      if (!skuId) {
        return { ok: false, message: "Không xác định được phân loại sản phẩm." };
      }

      const qty = Math.max(1, Math.trunc(input.quantity || 1));
      try {
        const res = await api<GatewayCartResponse>("cart/items", {
          method: "POST",
          json: { sku_id: skuId, quantity: qty },
        });
        applyCartSnapshot(res.cart ?? null);
        setCartError(null);
        setDeselectedSkuIds((prev) => {
          if (!prev.has(skuId)) return prev;
          const next = new Set(prev);
          next.delete(skuId);
          return next;
        });

        if (res.clamped_to_stock) {
          const msg = "Số lượng đã được điều chỉnh theo tồn kho còn lại.";
          setClampedSkuNotices((prev) => ({ ...prev, [skuId]: msg }));
          showToast(msg);
          return { ok: true, clampedToStock: true, message: msg };
        }

        setClampedSkuNotices((prev) => {
          if (!(skuId in prev)) return prev;
          const next = { ...prev };
          delete next[skuId];
          return next;
        });
        showToast("Đã thêm sản phẩm vào giỏ hàng.");
        return { ok: true, clampedToStock: false };
      } catch (err) {
        const msg = getErrorMessage(err, "Không thể thêm sản phẩm vào giỏ hàng.");
        showToast(msg);
        return { ok: false, message: msg };
      }
    },
    [applyCartSnapshot, isAuthenticated, showToast]
  );

  const updateCartQuantity = useCallback(
    async (skuId: string, quantity: number): Promise<AddToCartResult> => {
      if (!isAuthenticated) {
        return { ok: false, message: "Vui lòng đăng nhập để cập nhật giỏ hàng." };
      }

      const nextQty = Math.trunc(quantity);
      if (nextQty <= 0) {
        try {
          const res = await api<{ cart: GatewayCart }>(`cart/items/${encodeURIComponent(skuId)}`, {
            method: "DELETE",
          });
          applyCartSnapshot(res.cart ?? null);
          setClampedSkuNotices((prev) => {
            if (!(skuId in prev)) return prev;
            const next = { ...prev };
            delete next[skuId];
            return next;
          });
          return { ok: true };
        } catch (err) {
          const msg = getErrorMessage(err, "Không thể xóa sản phẩm khỏi giỏ hàng.");
          showToast(msg);
          return { ok: false, message: msg };
        }
      }

      try {
        const res = await api<GatewayCartResponse>(`cart/items/${encodeURIComponent(skuId)}`, {
          method: "PATCH",
          json: { quantity: nextQty },
        });
        applyCartSnapshot(res.cart ?? null);
        if (res.clamped_to_stock) {
          const msg = "Số lượng đã được giới hạn theo tồn kho hiện còn.";
          setClampedSkuNotices((prev) => ({ ...prev, [skuId]: msg }));
          showToast(msg);
          return { ok: true, clampedToStock: true, message: msg };
        }
        setClampedSkuNotices((prev) => {
          if (!(skuId in prev)) return prev;
          const next = { ...prev };
          delete next[skuId];
          return next;
        });
        return { ok: true, clampedToStock: false };
      } catch (err) {
        const msg = getErrorMessage(err, "Không thể cập nhật số lượng sản phẩm.");
        showToast(msg);
        return { ok: false, message: msg };
      }
    },
    [applyCartSnapshot, isAuthenticated, showToast]
  );

  const removeFromCart = useCallback(
    async (skuId: string): Promise<boolean> => {
      if (!isAuthenticated) return false;
      try {
        const res = await api<{ cart: GatewayCart }>(`cart/items/${encodeURIComponent(skuId)}`, {
          method: "DELETE",
        });
        applyCartSnapshot(res.cart ?? null);
        setClampedSkuNotices((prev) => {
          if (!(skuId in prev)) return prev;
          const next = { ...prev };
          delete next[skuId];
          return next;
        });
        showToast("Đã bỏ sản phẩm khỏi giỏ hàng.");
        return true;
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể xóa sản phẩm khỏi giỏ hàng."));
        return false;
      }
    },
    [applyCartSnapshot, isAuthenticated, showToast]
  );

  const clearCart = useCallback(
    async (skuIds?: string[]): Promise<boolean> => {
      if (!isAuthenticated) return false;
      try {
        await api<{ ok: boolean }>("cart", {
          method: "DELETE",
          ...(skuIds && skuIds.length > 0 ? { json: { sku_ids: skuIds } } : {}),
        });
        if (!skuIds || skuIds.length === 0) {
          applyCartSnapshot({
            user_id: user?.id ?? "",
            shop_groups: [],
            total_items: 0,
            total_vnd: 0,
          });
          setClampedSkuNotices({});
        } else {
          await refreshCart();
        }
        return true;
      } catch (err) {
        showToast(getErrorMessage(err, "Không thể xóa giỏ hàng lúc này."));
        return false;
      }
    },
    [applyCartSnapshot, isAuthenticated, refreshCart, showToast, user?.id]
  );

  const toggleCartItemSelected = useCallback(
    (skuId: string) => {
      const target = cartItems.find((it) => it.skuId === skuId);
      if (!target || target.unavailable || target.availableStock <= 0) return;
      setDeselectedSkuIds((prev) => {
        const next = new Set(prev);
        if (next.has(skuId)) {
          next.delete(skuId);
        } else {
          next.add(skuId);
        }
        return next;
      });
    },
    [cartItems]
  );

  const toggleShopSelected = useCallback(
    (shopId: string, selected: boolean) => {
      const group = shopGroups.find((g) => g.shopId === shopId);
      if (!group) return;
      setDeselectedSkuIds((prev) => {
        const next = new Set(prev);
        for (const item of group.items) {
          if (selected && !item.unavailable && item.availableStock > 0) {
            next.delete(item.skuId);
          } else if (!selected) {
            next.add(item.skuId);
          }
        }
        return next;
      });
    },
    [shopGroups]
  );

  const toggleSelectAllCart = useCallback(
    (selected: boolean) => {
      if (selected) {
        setDeselectedSkuIds(new Set());
        return;
      }
      setDeselectedSkuIds(new Set(cartItems.map((it) => it.skuId)));
    },
    [cartItems]
  );

  const setShopVoucher = useCallback((shopId: string, code: string | null) => {
    setSelectedShopVouchers((prev) => {
      if (!code) {
        if (!(shopId in prev)) return prev;
        const next = { ...prev };
        delete next[shopId];
        return next;
      }
      return { ...prev, [shopId]: code };
    });
  }, []);

  const optOutFlashForSku = useCallback((skuId: string) => {
    const clean = skuId.trim();
    if (!clean) return;
    setOptedOutFlashSkuIds((prev) => {
      if (prev.has(clean)) return prev;
      const next = new Set(prev);
      next.add(clean);
      return next;
    });
  }, []);

  const restoreFlashForSku = useCallback((skuId: string) => {
    const clean = skuId.trim();
    if (!clean) return;
    setOptedOutFlashSkuIds((prev) => {
      if (!prev.has(clean)) return prev;
      const next = new Set(prev);
      next.delete(clean);
      return next;
    });
  }, []);

  const resetPromotions = useCallback(() => {
    setSelectedShopVouchers({});
    setPlatformVoucherCode(null);
    setFreeshipVoucherCode(null);
    setUseCoins(false);
    setOptedOutFlashSkuIds(new Set());
  }, []);

  const saveVoucherCode = useCallback(
    async (codeOrId: string): Promise<boolean> => {
      const clean = codeOrId.trim();
      if (!clean) return false;
      if (!isAuthenticated) {
        showToast("Vui lòng đăng nhập để lưu mã ưu đãi.");
        return false;
      }
      try {
        const res = await api<GatewaySaveVoucherResponse>(
          `vouchers/${encodeURIComponent(clean)}/save`,
          { method: "POST", json: { code: clean } }
        );
        const savedCode = (res.voucher?.code ?? clean).toUpperCase();
        setSavedVoucherCodes((prev) => (prev.includes(savedCode) ? prev : [savedCode, ...prev]));
        showToast(`Đã lưu mã ưu đãi ${savedCode}.`);
        return true;
      } catch (err) {
        showToast(formatPromotionErrorMessage(err, "save_voucher", "Không thể lưu mã ưu đãi này."));
        return false;
      }
    },
    [isAuthenticated, showToast]
  );

  const value = useMemo<CartContextValue>(
    () => ({
      cartItems,
      shopGroups,
      cartCount,
      totalVnd,
      selectedItems,
      selectedShopGroups,
      selectedCount,
      selectedSubtotalVnd,
      cartLoading,
      cartInitialized,
      cartError,
      clampedSkuNotices,
      refreshCart,
      addToCart,
      updateCartQuantity,
      removeFromCart,
      clearCart,
      toggleCartItemSelected,
      toggleShopSelected,
      toggleSelectAllCart,
      selectedShopVouchers,
      platformVoucherCode,
      freeshipVoucherCode,
      useCoins,
      setShopVoucher,
      setPlatformVoucher: setPlatformVoucherCode,
      setFreeshipVoucher: setFreeshipVoucherCode,
      setUseCoins,
      resetPromotions,
      optedOutFlashSkuIds,
      optOutFlashForSku,
      restoreFlashForSku,
      savedVoucherCodes,
      saveVoucherCode,
    }),
    [
      cartItems,
      shopGroups,
      cartCount,
      totalVnd,
      selectedItems,
      selectedShopGroups,
      selectedCount,
      selectedSubtotalVnd,
      cartLoading,
      cartInitialized,
      cartError,
      clampedSkuNotices,
      refreshCart,
      addToCart,
      updateCartQuantity,
      removeFromCart,
      clearCart,
      toggleCartItemSelected,
      toggleShopSelected,
      toggleSelectAllCart,
      selectedShopVouchers,
      platformVoucherCode,
      freeshipVoucherCode,
      useCoins,
      setShopVoucher,
      resetPromotions,
      optedOutFlashSkuIds,
      optOutFlashForSku,
      restoreFlashForSku,
      savedVoucherCodes,
      saveVoucherCode,
    ]
  );

  return <CartContext.Provider value={value}>{children}</CartContext.Provider>;
}

export function useCart(): CartContextValue {
  const ctx = useContext(CartContext);
  if (!ctx) {
    throw new Error("useCart phải được gọi bên trong CartProvider.");
  }
  return ctx;
}
