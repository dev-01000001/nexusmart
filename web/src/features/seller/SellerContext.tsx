"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type {
  GatewayOrder,
  GatewayReturnRequest,
  GatewayShop,
  GatewayUser,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage, refreshSessionOnce } from "@/lib/api";
import { formatDateTimeVi } from "@/lib/format";
import { StateView } from "@/components";
import { mapShopSlugToMockId } from "./types";
import common from "./SellerCommon.module.css";

export type SellerShopState =
  "loading" | "ready" | "no_shop" | "pending_shop" | "rejected_shop" | "suspended_shop" | "error";

export interface SellerContextValue {
  user: GatewayUser;
  shop: GatewayShop | null;
  shopState: SellerShopState;
  shopError: string | null;
  pendingOrdersCount: number;
  pendingReturnsCount: number;
  mockShopKey: string;
  reloadShop: () => Promise<void>;
  refreshPendingOrders: () => Promise<void>;
  refreshPendingReturns: () => Promise<void>;
  setPendingOrdersCount: React.Dispatch<React.SetStateAction<number>>;
}

const SellerContext = createContext<SellerContextValue | null>(null);

function classifyShopState(shop: GatewayShop): SellerShopState {
  switch (shop.status) {
    case "ACTIVE":
      return "ready";
    case "PENDING":
      return "pending_shop";
    case "REJECTED":
      return "rejected_shop";
    case "SUSPENDED":
      return "suspended_shop";
    default:
      return "ready";
  }
}

export function SellerProvider({
  user,
  children,
}: {
  user: GatewayUser;
  children: React.ReactNode;
}) {
  const [shop, setShop] = useState<GatewayShop | null>(null);
  const [shopState, setShopState] = useState<SellerShopState>("loading");
  const [shopError, setShopError] = useState<string | null>(null);
  const [pendingOrdersCount, setPendingOrdersCount] = useState(0);
  const [pendingReturnsCount, setPendingReturnsCount] = useState(0);

  const refreshPendingOrders = useCallback(async () => {
    try {
      const res = await api<PaginatedResponse<GatewayOrder>>(
        "seller/orders?status=CONFIRMED&page=1&page_size=1"
      );
      setPendingOrdersCount(typeof res.total === "number" ? res.total : res.items.length);
    } catch {
      // Keep previous badge count when background badge poll fails
    }
  }, []);

  const refreshPendingReturns = useCallback(async () => {
    try {
      const res = await api<PaginatedResponse<GatewayReturnRequest>>(
        "seller/returns?status=PENDING&page=1&page_size=1"
      );
      setPendingReturnsCount(typeof res.total === "number" ? res.total : res.items.length);
    } catch {
      // Keep previous badge count when returns endpoint is unavailable
    }
  }, []);

  const loadShop = useCallback(async () => {
    setShopState("loading");
    setShopError(null);

    const fetchSellerShop = async (): Promise<GatewayShop | null> => {
      try {
        const res = await api<{ shop: GatewayShop | null }>("seller/shop");
        return res?.shop ?? null;
      } catch (err) {
        if (err instanceof ApiError && err.status === 403 && user.shop_id) {
          // Handle recently-approved seller whose access token still carries old buyer claims
          const refreshed = await refreshSessionOnce();
          if (refreshed) {
            const retryRes = await api<{ shop: GatewayShop | null }>("seller/shop");
            return retryRes?.shop ?? null;
          }
        }
        throw err;
      }
    };

    if (user.shop_id) {
      try {
        const fetched = await fetchSellerShop();
        if (fetched) {
          setShop(fetched);
          const nextState = classifyShopState(fetched);
          setShopState(nextState);
          if (nextState === "ready" || nextState === "suspended_shop") {
            void refreshPendingOrders();
            void refreshPendingReturns();
          }
          return;
        }
      } catch (err) {
        if (!(err instanceof ApiError && (err.status === 403 || err.status === 404))) {
          setShop(null);
          setShopState("error");
          setShopError(getErrorMessage(err, "Không thể tải thông tin gian hàng lúc này."));
          return;
        }
      }
    }

    // Fallback to caller's own shop in any status (handles seller without active shop_id)
    try {
      const res = await api<{ shop: GatewayShop | null }>("shops/me");
      if (res?.shop) {
        setShop(res.shop);
        const nextState = classifyShopState(res.shop);
        setShopState(nextState);
        if (nextState === "ready" || nextState === "suspended_shop") {
          void refreshPendingOrders();
          void refreshPendingReturns();
        }
        return;
      }
      setShop(null);
      setShopState("no_shop");
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setShop(null);
        setShopState("no_shop");
        return;
      }
      setShop(null);
      setShopState("error");
      setShopError(getErrorMessage(err, "Không thể tải thông tin gian hàng lúc này."));
    }
  }, [user.shop_id, refreshPendingOrders, refreshPendingReturns]);

  useEffect(() => {
    void loadShop();
  }, [loadShop]);

  const mockShopKey = useMemo(() => mapShopSlugToMockId(shop?.slug), [shop?.slug]);

  const value = useMemo<SellerContextValue>(
    () => ({
      user,
      shop,
      shopState,
      shopError,
      pendingOrdersCount,
      pendingReturnsCount,
      mockShopKey,
      reloadShop: loadShop,
      refreshPendingOrders,
      refreshPendingReturns,
      setPendingOrdersCount,
    }),
    [
      user,
      shop,
      shopState,
      shopError,
      pendingOrdersCount,
      pendingReturnsCount,
      mockShopKey,
      loadShop,
      refreshPendingOrders,
      refreshPendingReturns,
    ]
  );

  return <SellerContext.Provider value={value}>{children}</SellerContext.Provider>;
}

export function useSellerContext(): SellerContextValue {
  const ctx = useContext(SellerContext);
  if (!ctx) {
    throw new Error("useSellerContext phải được gọi trong SellerProvider.");
  }
  return ctx;
}

/**
 * Renders a clear non-error status view when a seller user does not yet have an active shop.
 */
export function SellerShopGate({
  pageTitle,
  allowSuspended = false,
  children,
}: {
  pageTitle: string;
  allowSuspended?: boolean;
  children: (shop: GatewayShop) => React.ReactNode;
}) {
  const { shop, shopState, shopError, reloadShop } = useSellerContext();

  if (shopState === "loading") {
    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>{pageTitle}</h1>
          </div>
        </header>
        <StateView kind="loading" surface="app" title="Đang tải thông tin gian hàng..." rows={4} />
      </div>
    );
  }

  if (shopState === "error") {
    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>{pageTitle}</h1>
          </div>
        </header>
        <StateView
          kind="error"
          surface="app"
          title="Không thể kết nối tới hồ sơ gian hàng"
          message={shopError ?? "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."}
          actionLabel="Thử lại"
          onAction={() => void reloadShop()}
        />
      </div>
    );
  }

  if (shopState === "no_shop" || !shop) {
    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>{pageTitle}</h1>
            <p className={common.finding}>
              Tài khoản của bạn chưa liên kết gian hàng đang hoạt động.
            </p>
          </div>
        </header>
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có gian hàng được kích hoạt"
          message="Vui lòng hoàn tất đăng ký gian hàng trong trang Tài khoản và chờ quản trị viên phê duyệt để mở toàn bộ tính năng bán hàng."
          actionLabel="Đi tới trang Tài khoản"
          actionHref="/tai-khoan"
        />
      </div>
    );
  }

  if (shopState === "pending_shop") {
    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>{pageTitle}</h1>
            <p className={common.finding}>{shop.name} · Hồ sơ đang chờ quản trị viên xét duyệt</p>
          </div>
        </header>
        <StateView
          kind="empty"
          surface="app"
          title={`Gian hàng "${shop.name}" đang chờ duyệt`}
          message="Hồ sơ mở gian hàng của bạn đã được tiếp nhận. Ngay khi quản trị viên phê duyệt, bạn có thể đăng sản phẩm và xử lý đơn hàng tại đây."
          actionLabel="Kiểm tra lại trạng thái"
          onAction={() => void reloadShop()}
        />
      </div>
    );
  }

  if (shopState === "rejected_shop") {
    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>{pageTitle}</h1>
            <p className={common.finding}>{shop.name} · Hồ sơ bị từ chối</p>
          </div>
        </header>
        <StateView
          kind="empty"
          surface="app"
          title={`Hồ sơ gian hàng "${shop.name}" chưa được duyệt`}
          message={
            shop.rejection_reason
              ? `Lý do từ quản trị viên: ${shop.rejection_reason}`
              : "Vui lòng kiểm tra lại hồ sơ gian hàng hoặc liên hệ bộ phận hỗ trợ NexusMart."
          }
          actionLabel="Về trang Tài khoản"
          actionHref="/tai-khoan"
        />
      </div>
    );
  }

  if (shopState === "suspended_shop") {
    const suspendedReason = (shop.suspended_reason ?? "").trim();
    const suspendedAtLabel = shop.suspended_at ? formatDateTimeVi(shop.suspended_at) : "";
    if (allowSuspended) {
      return (
        <div className={common.page}>
          <div className={common.suspendedBanner} role="status">
            <strong className={common.suspendedBannerTitle}>
              Gian hàng &ldquo;{shop.name}&rdquo; đang tạm ngưng hoạt động
            </strong>
            <p className={common.suspendedBannerText}>
              {suspendedReason
                ? `Lý do tạm ngưng: ${suspendedReason}. `
                : "Gian hàng đang tạm ngưng theo quyết định quản trị viên. "}
              Bạn vẫn cần xử lý các đơn đã đặt, yêu cầu trả hàng và theo dõi tài chính bình thường
              trong thời gian tạm ngưng.
            </p>
            {suspendedAtLabel && (
              <p className={`${common.suspendedBannerMeta} num`}>
                Thời điểm tạm ngưng: {suspendedAtLabel}
              </p>
            )}
          </div>
          {children(shop)}
        </div>
      );
    }

    const detailParts: string[] = [];
    if (suspendedReason) {
      detailParts.push(`Lý do tạm ngưng: ${suspendedReason}.`);
    }
    if (suspendedAtLabel) {
      detailParts.push(`Thời điểm tạm ngưng: ${suspendedAtLabel}.`);
    }
    detailParts.push(
      "Tính năng này tạm khóa trong thời gian gian hàng tạm ngưng, nhưng bạn vẫn cần xử lý các đơn đã đặt tại mục Đơn hàng."
    );

    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>{pageTitle}</h1>
            <p className={common.finding}>{shop.name} · Gian hàng đang tạm ngưng</p>
          </div>
        </header>
        <StateView
          kind="empty"
          surface="app"
          title={`Gian hàng "${shop.name}" đang tạm ngưng hoạt động`}
          message={detailParts.join(" ")}
          actionLabel="Xử lý đơn hàng đã đặt"
          actionHref="/seller/don-hang"
        />
      </div>
    );
  }

  return <>{children(shop)}</>;
}
