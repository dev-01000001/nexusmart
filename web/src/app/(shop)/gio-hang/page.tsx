"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import type {
  GatewayAddress,
  GatewayCalculateFeeResponse,
  GatewayCoinBalance,
  GatewayDiscountBreakdown,
  GatewayPreviewDiscountsResponse,
  GatewayProduct,
  GatewayPublicShop,
  GatewayVoucher,
  GatewayVouchersResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd, formatVoucherBenefit } from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import {
  Button,
  IconChat,
  IconMinus,
  IconPlus,
  IconTag,
  IconTrash,
  IconTruck,
  Price,
  StateView,
} from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import { formatVoucherCondition, translateVoucherReason } from "@/features/checkout/messages";
import { buildCalculateFeePayload } from "@/features/checkout/helpers";
import { CheckoutSuggestionStrip } from "@/features/checkout/CheckoutSuggestionStrip";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

function CartPageContent() {
  const viewState = usePageStateParam();
  const { isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cartItems,
    shopGroups,
    selectedShopGroups,
    selectedCount,
    selectedSubtotalVnd,
    cartLoading,
    cartInitialized,
    cartError,
    clampedSkuNotices,
    refreshCart,
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
    setPlatformVoucher,
    setFreeshipVoucher,
    setUseCoins,
  } = useCart();
  const { openChat, showToast } = useMockStore();

  const [busySkuIds, setBusySkuIds] = useState<Record<string, boolean>>({});
  const [confirmClear, setConfirmClear] = useState(false);
  const [clearingCart, setClearingCart] = useState(false);

  // Public metadata for links (product slug & shop slug) + real suggested products
  const [shopMetaById, setShopMetaById] = useState<Record<string, GatewayPublicShop>>({});
  const [productSlugById, setProductSlugById] = useState<Record<string, string>>({});
  const [catalogProducts, setCatalogProducts] = useState<GatewayProduct[]>([]);

  // Default address & best-effort shipping estimate for freeship preview
  const [defaultAddress, setDefaultAddress] = useState<GatewayAddress | null>(null);
  const [shippingFeeByShop, setShippingFeeByShop] = useState<Record<string, number>>({});

  // Real vouchers, coins & preview from PromotionService
  const [shopVouchersByShop, setShopVouchersByShop] = useState<Record<string, GatewayVoucher[]>>(
    {}
  );
  const [platformVouchers, setPlatformVouchers] = useState<GatewayVoucher[]>([]);
  const [freeshipVouchers, setFreeshipVouchers] = useState<GatewayVoucher[]>([]);
  const [coinBalance, setCoinBalance] = useState<number>(0);

  const [preview, setPreview] = useState<GatewayDiscountBreakdown | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);
  const [previewError, setPreviewError] = useState<string | null>(null);

  const [manualCode, setManualCode] = useState("");
  const [applyingCode, setApplyingCode] = useState(false);
  const [codeFeedback, setCodeFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const previewReqSeq = useRef(0);

  useRevealOnScroll(
    `${viewState ?? "live"}-${cartItems.length}-${shopGroups.length}-${isAuthenticated}`
  );

  // Load shop & product slugs once for storefront links (best-effort)
  useEffect(() => {
    let active = true;
    const loadMeta = async () => {
      const [shopsRes, productsRes] = await Promise.allSettled([
        api<PaginatedResponse<GatewayPublicShop>>("shops?page_size=50"),
        api<PaginatedResponse<GatewayProduct>>("products?page_size=100"),
      ]);
      if (!active) return;
      if (shopsRes.status === "fulfilled" && Array.isArray(shopsRes.value?.items)) {
        const map: Record<string, GatewayPublicShop> = {};
        for (const s of shopsRes.value.items) {
          map[s.id] = s;
        }
        setShopMetaById(map);
      }
      if (productsRes.status === "fulfilled" && Array.isArray(productsRes.value?.items)) {
        const map: Record<string, string> = {};
        for (const p of productsRes.value.items) {
          if (p.slug) map[p.id] = p.slug;
        }
        setProductSlugById(map);
        setCatalogProducts(productsRes.value.items);
      }
    };
    void loadMeta();
    return () => {
      active = false;
    };
  }, []);

  // Load real coin balance & default address when authenticated
  useEffect(() => {
    if (!isAuthenticated) {
      setCoinBalance(0);
      setDefaultAddress(null);
      return;
    }
    let active = true;
    const loadAccountPromoData = async () => {
      const [coinsRes, addrRes] = await Promise.allSettled([
        api<GatewayCoinBalance>("coins/balance"),
        api<{ addresses: GatewayAddress[] }>("users/me/addresses"),
      ]);
      if (!active) return;
      if (coinsRes.status === "fulfilled" && typeof coinsRes.value?.balance === "number") {
        setCoinBalance(Math.max(0, Math.trunc(coinsRes.value.balance)));
      }
      if (addrRes.status === "fulfilled" && Array.isArray(addrRes.value?.addresses)) {
        const list = addrRes.value.addresses;
        const preferred = list.find((a) => a.is_default) ?? list[0] ?? null;
        setDefaultAddress(preferred);
      }
    };
    void loadAccountPromoData();
    return () => {
      active = false;
    };
  }, [isAuthenticated]);

  // Estimate shipping per selected shop using default address so freeship preview is accurate
  const selectedShopShippingKey = useMemo(
    () =>
      selectedShopGroups
        .map((g) => `${g.shopId}:${g.selectedSubtotalVnd}:${g.selectedWeightGrams}`)
        .join("|"),
    [selectedShopGroups]
  );

  useEffect(() => {
    if (!isAuthenticated || !defaultAddress || selectedShopGroups.length === 0) {
      setShippingFeeByShop({});
      return;
    }
    let active = true;
    const calcFees = async () => {
      const results = await Promise.allSettled(
        selectedShopGroups.map(async (g) => {
          const res = await api<GatewayCalculateFeeResponse>("shipping/calculate-fee", {
            method: "POST",
            json: buildCalculateFeePayload({
              shopId: g.shopId,
              toProvinceCode: defaultAddress.province_code,
              toWardCode: defaultAddress.ward_code,
              weightGrams: Math.max(100, g.selectedWeightGrams),
              codAmountVnd: 0,
            }),
          });
          const quotes = res?.quotes ?? [];
          if (quotes.length === 0) return { shopId: g.shopId, feeVnd: 0 };
          const cheapest = quotes.reduce(
            (min, q) => (q.fee_vnd < min.fee_vnd ? q : min),
            quotes[0]
          );
          return { shopId: g.shopId, feeVnd: cheapest.fee_vnd };
        })
      );
      if (!active) return;
      const next: Record<string, number> = {};
      for (const r of results) {
        if (r.status === "fulfilled") {
          next[r.value.shopId] = r.value.feeVnd;
        }
      }
      setShippingFeeByShop(next);
    };
    void calcFees();
    return () => {
      active = false;
    };
  }, [isAuthenticated, defaultAddress, selectedShopShippingKey, selectedShopGroups]);

  // Load real vouchers from GET /vouchers (by scope, shop_id, order_amount_vnd)
  const voucherQueryKey = useMemo(
    () =>
      `${selectedSubtotalVnd}|${shopGroups
        .map((g) => `${g.shopId}:${g.selectedSubtotalVnd}`)
        .join(",")}`,
    [selectedSubtotalVnd, shopGroups]
  );

  useEffect(() => {
    if (!isAuthenticated || shopGroups.length === 0) {
      setPlatformVouchers([]);
      setFreeshipVouchers([]);
      setShopVouchersByShop({});
      return;
    }
    let active = true;
    const timer = window.setTimeout(() => {
      const loadVouchers = async () => {
        const orderAmountQuery =
          selectedSubtotalVnd > 0 ? `&order_amount_vnd=${selectedSubtotalVnd}` : "";
        const [platformRes, freeshipRes, ...shopResults] = await Promise.allSettled([
          api<GatewayVouchersResponse>(`vouchers?scope=PLATFORM${orderAmountQuery}`),
          api<GatewayVouchersResponse>(`vouchers?scope=FREESHIP${orderAmountQuery}`),
          ...shopGroups.map((g) => {
            const shopAmount =
              g.selectedSubtotalVnd > 0 ? `&order_amount_vnd=${g.selectedSubtotalVnd}` : "";
            return api<GatewayVouchersResponse>(
              `vouchers?scope=SHOP&shop_id=${encodeURIComponent(g.shopId)}${shopAmount}`
            ).then((res) => ({ shopId: g.shopId, vouchers: res?.vouchers ?? [] }));
          }),
        ]);
        if (!active) return;
        if (platformRes.status === "fulfilled") {
          setPlatformVouchers(platformRes.value?.vouchers ?? []);
        }
        if (freeshipRes.status === "fulfilled") {
          setFreeshipVouchers(freeshipRes.value?.vouchers ?? []);
        }
        const nextByShop: Record<string, GatewayVoucher[]> = {};
        for (const r of shopResults) {
          if (r.status === "fulfilled") {
            nextByShop[r.value.shopId] = r.value.vouchers;
          }
        }
        setShopVouchersByShop(nextByShop);
      };
      void loadVouchers();
    }, 180);

    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [isAuthenticated, voucherQueryKey, shopGroups, selectedSubtotalVnd]);

  const buildShopDiscountInputs = useCallback(() => {
    return selectedShopGroups.map((g) => ({
      shop_id: g.shopId,
      subtotal_vnd: g.selectedSubtotalVnd,
      shipping_fee_vnd: shippingFeeByShop[g.shopId] ?? 0,
      items: g.items.map((it) => ({
        sku_id: it.skuId,
        quantity: it.quantity,
        unit_price_vnd: it.priceVnd,
      })),
    }));
  }, [selectedShopGroups, shippingFeeByShop]);

  const activeShopVoucherMap = useMemo(() => {
    const out: Record<string, string> = {};
    for (const g of selectedShopGroups) {
      const code = selectedShopVouchers[g.shopId];
      if (code) out[g.shopId] = code;
    }
    return out;
  }, [selectedShopGroups, selectedShopVouchers]);

  // Debounced POST /promotions/preview whenever cart selection, vouchers, shipping, or coins change
  useEffect(() => {
    if (!isAuthenticated || selectedShopGroups.length === 0) {
      setPreview(null);
      setPreviewError(null);
      setPreviewLoading(false);
      return;
    }

    const seq = ++previewReqSeq.current;
    setPreviewLoading(true);
    setPreviewError(null);

    const timer = window.setTimeout(() => {
      const runPreview = async () => {
        try {
          const res = await api<GatewayPreviewDiscountsResponse>("promotions/preview", {
            method: "POST",
            json: {
              shop_items: buildShopDiscountInputs(),
              shop_voucher_ids: activeShopVoucherMap,
              platform_voucher_id: platformVoucherCode ?? "",
              freeship_voucher_id: freeshipVoucherCode ?? "",
              coins_to_use: useCoins ? coinBalance : 0,
            },
          });
          if (previewReqSeq.current !== seq) return;
          setPreview(res?.breakdown ?? null);
          setPreviewError(null);
        } catch (err) {
          if (previewReqSeq.current !== seq) return;
          if (err instanceof ApiError && err.status === 401) {
            setPreview(null);
          } else {
            setPreviewError(
              getErrorMessage(err, "Chưa thể tính trước ưu đãi lúc này. Vui lòng thử lại.")
            );
          }
        } finally {
          if (previewReqSeq.current === seq) {
            setPreviewLoading(false);
          }
        }
      };
      void runPreview();
    }, 240);

    return () => window.clearTimeout(timer);
  }, [
    isAuthenticated,
    selectedShopGroups,
    buildShopDiscountInputs,
    activeShopVoucherMap,
    platformVoucherCode,
    freeshipVoucherCode,
    useCoins,
    coinBalance,
  ]);

  const handleQtyChange = async (skuId: string, nextQty: number) => {
    if (busySkuIds[skuId]) return;
    setBusySkuIds((prev) => ({ ...prev, [skuId]: true }));
    await updateCartQuantity(skuId, nextQty);
    setBusySkuIds((prev) => {
      const next = { ...prev };
      delete next[skuId];
      return next;
    });
  };

  const handleRemove = async (skuId: string) => {
    if (busySkuIds[skuId]) return;
    setBusySkuIds((prev) => ({ ...prev, [skuId]: true }));
    await removeFromCart(skuId);
    setBusySkuIds((prev) => {
      const next = { ...prev };
      delete next[skuId];
      return next;
    });
  };

  const handleApplyManualCode = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = manualCode.trim().toUpperCase();
    if (!clean) {
      setCodeFeedback({ ok: false, message: "Vui lòng nhập mã ưu đãi cần áp dụng." });
      return;
    }
    if (selectedShopGroups.length === 0) {
      setCodeFeedback({
        ok: false,
        message: "Hãy chọn ít nhất 1 sản phẩm trước khi áp dụng mã ưu đãi.",
      });
      return;
    }

    const allKnownVouchers: GatewayVoucher[] = [
      ...platformVouchers,
      ...freeshipVouchers,
      ...Object.values(shopVouchersByShop).flat(),
    ];
    const matched = allKnownVouchers.find((v) => v.code.toUpperCase() === clean);

    if (matched) {
      const scope = matched.scope.toUpperCase();
      if (scope === "SHOP") {
        const shopGroup = selectedShopGroups.find((g) => g.shopId === matched.shop_id);
        if (!shopGroup) {
          setCodeFeedback({
            ok: false,
            message: "Giỏ hàng chưa có sản phẩm đang chọn thuộc gian hàng áp dụng mã này.",
          });
          return;
        }
        if (!matched.eligible) {
          setCodeFeedback({
            ok: false,
            message:
              translateVoucherReason(matched.ineligible_reason) ||
              `Mã ${matched.code} chưa đủ điều kiện áp dụng.`,
          });
          return;
        }
        setShopVoucher(matched.shop_id, matched.code);
        setCodeFeedback({ ok: true, message: `Đã chọn mã gian hàng ${matched.code}.` });
        setManualCode("");
        return;
      }

      if (!matched.eligible) {
        setCodeFeedback({
          ok: false,
          message:
            translateVoucherReason(matched.ineligible_reason) ||
            `Mã ${matched.code} chưa đủ điều kiện áp dụng.`,
        });
        return;
      }

      if (scope === "FREESHIP") {
        setFreeshipVoucher(matched.code);
        setCodeFeedback({ ok: true, message: `Đã chọn mã vận chuyển ${matched.code}.` });
      } else {
        setPlatformVoucher(matched.code);
        setCodeFeedback({ ok: true, message: `Đã chọn mã toàn sàn ${matched.code}.` });
      }
      setManualCode("");
      return;
    }

    // Unknown or expired/exhausted code: verify live via POST /promotions/preview
    setApplyingCode(true);
    setCodeFeedback(null);
    try {
      const res = await api<GatewayPreviewDiscountsResponse>("promotions/preview", {
        method: "POST",
        json: {
          shop_items: buildShopDiscountInputs(),
          shop_voucher_ids: activeShopVoucherMap,
          platform_voucher_id: clean,
          freeship_voucher_id: freeshipVoucherCode ?? "",
          coins_to_use: useCoins ? coinBalance : 0,
        },
      });
      const bd = res?.breakdown;
      if (bd && bd.total_platform_voucher_discount_vnd > 0) {
        setPlatformVoucher(clean);
        setCodeFeedback({
          ok: true,
          message: `Áp dụng thành công mã ${clean} (giảm ${formatVnd(bd.total_platform_voucher_discount_vnd)}).`,
        });
        setManualCode("");
      } else {
        const warning = bd?.warnings?.[0];
        setCodeFeedback({
          ok: false,
          message: warning
            ? translateVoucherReason(warning)
            : `Mã ${clean} không tồn tại hoặc chưa đủ điều kiện áp dụng.`,
        });
      }
    } catch (err) {
      setCodeFeedback({
        ok: false,
        message: getErrorMessage(err, `Không thể kiểm tra mã ${clean} lúc này.`),
      });
    } finally {
      setApplyingCode(false);
    }
  };

  const inStockItems = useMemo(() => cartItems.filter((it) => it.availableStock > 0), [cartItems]);
  const allSelected = inStockItems.length > 0 && inStockItems.every((i) => i.selected);

  const estimatedShippingTotal = useMemo(
    () => selectedShopGroups.reduce((sum, g) => sum + (shippingFeeByShop[g.shopId] ?? 0), 0),
    [selectedShopGroups, shippingFeeByShop]
  );

  const subtotalDisplay = preview?.total_subtotal_vnd ?? selectedSubtotalVnd;
  const shopVoucherDisplay = preview?.total_shop_voucher_discount_vnd ?? 0;
  const platformVoucherDisplay = preview?.total_platform_voucher_discount_vnd ?? 0;
  const shippingDisplay = preview?.total_shipping_fee_vnd ?? estimatedShippingTotal;
  const freeshipDisplay = preview?.total_freeship_discount_vnd ?? 0;
  const coinsDisplay = preview?.total_coins_discount_vnd ?? 0;
  const totalDiscountDisplay = preview?.total_discount_vnd ?? 0;
  const grandTotalDisplay =
    preview?.final_payable_vnd ?? Math.max(0, selectedSubtotalVnd + estimatedShippingTotal);

  const shopNameById = useMemo(() => {
    const map: Record<string, string> = {};
    for (const [id, shop] of Object.entries(shopMetaById)) {
      map[id] = shop.name;
    }
    return map;
  }, [shopMetaById]);

  const suggestedProducts = useMemo(() => {
    const inCartIds = new Set(cartItems.map((it) => it.productId));
    const fresh = catalogProducts.filter((p) => !inCartIds.has(p.id));
    return (fresh.length >= 4 ? fresh : catalogProducts).slice(0, 4);
  }, [catalogProducts, cartItems]);

  const translatedWarnings = useMemo(() => {
    if (!preview?.warnings?.length) return [];
    return Array.from(new Set(preview.warnings.map(translateVoucherReason).filter(Boolean)));
  }, [preview?.warnings]);

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Giỏ hàng của bạn
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/danh-muc">
          Tiếp tục chọn đồ
        </Button>
      </div>
    </section>
  );

  if (
    viewState === "loading" ||
    sessionLoading ||
    (isAuthenticated && (!cartInitialized || (cartLoading && cartItems.length === 0)))
  ) {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật sản phẩm, giá bán và tồn kho mới nhất trong giỏ hàng.")}
        <section className={`${shared.section} ${styles.loadingStage}`}>
          <h2 className={shared.sectionTitle}>Danh sách sản phẩm</h2>
          <StateView
            kind="loading"
            title="Đang đồng bộ giỏ hàng và ưu đãi theo gian hàng..."
            rows={4}
          />
        </section>
      </div>
    );
  }

  if (viewState === "error" || cartError) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối đến giỏ hàng lúc này. Sản phẩm bạn đã chọn vẫn được giữ.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái giỏ hàng</h2>
          <StateView
            kind="error"
            title="Chưa thể tải giỏ hàng"
            message={cartError ?? "Kết nối đang tạm gián đoạn. Vui lòng thử tải lại trang."}
            actionLabel="Tải lại giỏ hàng"
            onAction={() => void refreshCart()}
          />
        </section>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={shared.page}>
        {renderHeader(
          "Đăng nhập tài khoản NexusMart để xem giỏ hàng, giữ giá và áp dụng mã ưu đãi."
        )}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Giỏ hàng tài khoản</h2>
          <StateView
            kind="empty"
            title="Đăng nhập để sử dụng giỏ hàng"
            message="Giỏ hàng được đồng bộ trực tiếp theo tài khoản để kiểm tra số lượng còn lại và ưu đãi theo từng gian hàng."
            actionLabel="Đăng nhập ngay"
            actionHref="/dang-nhap?next=%2Fgio-hang"
          />
        </section>
        <CheckoutSuggestionStrip
          title="Đồ cơ bản thường mua kèm"
          subtitle="Sản phẩm chuẩn chất liệu đang có sẵn từ các xưởng dệt NexusMart."
          products={suggestedProducts}
          shopNameById={shopNameById}
          showQuickAdd
        />
      </div>
    );
  }

  if (viewState === "empty" || cartItems.length === 0) {
    return (
      <div className={shared.page}>
        {renderHeader(
          "Giỏ hàng của bạn hiện chưa có sản phẩm nào. Chọn đồ cơ bản chuẩn chất liệu để bắt đầu."
        )}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Sản phẩm trong giỏ</h2>
          <StateView
            kind="empty"
            title="Giỏ hàng đang trống"
            message="Khám phá áo thun cotton 220gsm, polo pique, quần short và đồ lót sợi tre từ các xưởng dệt Việt Nam."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        <CheckoutSuggestionStrip
          title="Gợi ý bắt đầu giỏ hàng"
          subtitle="Thêm nhanh các sản phẩm cơ bản đang có sẵn từ xưởng dệt NexusMart."
          products={suggestedProducts}
          shopNameById={shopNameById}
          showQuickAdd
        />
      </div>
    );
  }

  return (
    <div className={`${shared.page} ${styles.mobileSpacer}`}>
      {renderHeader("Kiểm tra số lượng, áp mã ưu đãi và chọn sản phẩm cần thanh toán.")}

      <section className={shared.section}>
        <div className={styles.cartLayout}>
          {/* Left column: Toolbar & real shop groups */}
          <div className={styles.leftCol}>
            <div
              className={styles.cartToolbar}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.selectBar}>
                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={allSelected}
                    disabled={inStockItems.length === 0}
                    onChange={(e) => toggleSelectAllCart(e.target.checked)}
                  />
                  <span>
                    Chọn tất cả (<strong className="num">{cartItems.length}</strong> mặt hàng)
                  </span>
                </label>

                {confirmClear ? (
                  <div className={shared.formActions}>
                    <Button
                      variant="danger"
                      size="sm"
                      disabled={clearingCart}
                      onClick={async () => {
                        setClearingCart(true);
                        const ok = await clearCart();
                        setClearingCart(false);
                        setConfirmClear(false);
                        if (ok) {
                          showToast("Đã xóa toàn bộ sản phẩm trong giỏ hàng.");
                        }
                      }}
                    >
                      {clearingCart ? "Đang xóa..." : "Xác nhận xóa hết"}
                    </Button>
                    <Button variant="secondary" size="sm" onClick={() => setConfirmClear(false)}>
                      Giữ lại
                    </Button>
                  </div>
                ) : (
                  <button
                    type="button"
                    className={styles.clearBtn}
                    onClick={() => setConfirmClear(true)}
                  >
                    Xóa toàn bộ giỏ
                  </button>
                )}
              </div>
            </div>

            <div className={styles.shopGroupList}>
              {shopGroups.map((group, groupIdx) => {
                const shopMeta = shopMetaById[group.shopId];
                const shopName = shopMeta?.name || group.shopName;
                const shopVouchers = shopVouchersByShop[group.shopId] ?? [];
                const selectableItems = group.items.filter((it) => it.availableStock > 0);
                const shopAllSelected =
                  selectableItems.length > 0 && selectableItems.every((it) => it.selected);
                const chosenShopCode = selectedShopVouchers[group.shopId] ?? null;
                const shopBreakdown = preview?.shops.find((s) => s.shop_id === group.shopId);

                return (
                  <article
                    key={group.shopId}
                    className={styles.shopGroup}
                    data-rise=""
                    style={{ "--i": Math.min(groupIdx + 3, 6) } as React.CSSProperties}
                  >
                    <div className={styles.shopHeader}>
                      <div className={styles.shopIdentity}>
                        <input
                          type="checkbox"
                          className={shared.checkbox}
                          checked={shopAllSelected}
                          disabled={selectableItems.length === 0}
                          onChange={(e) => toggleShopSelected(group.shopId, e.target.checked)}
                          aria-label={`Chọn toàn bộ sản phẩm của ${shopName}`}
                        />
                        {shopMeta?.slug ? (
                          <Link href={`/gian-hang/${shopMeta.slug}`} className={styles.shopLink}>
                            {shopName}
                          </Link>
                        ) : (
                          <span className={styles.shopLink}>{shopName}</span>
                        )}
                      </div>

                      <button
                        type="button"
                        className={styles.chatShopBtn}
                        onClick={() => openChat(group.shopId)}
                      >
                        <IconChat size={15} />
                        <span>Chat với xưởng</span>
                      </button>
                    </div>

                    <ul className={styles.itemList}>
                      {group.items.map((item, itemIdx) => {
                        const lineTotal = item.priceVnd * item.quantity;
                        const outOfStock = item.availableStock <= 0;
                        const isBusy = Boolean(busySkuIds[item.skuId]);
                        const clampedNote = clampedSkuNotices[item.skuId];
                        const slug = productSlugById[item.productId];
                        const productHref = slug ? `/san-pham/${slug}` : null;

                        return (
                          <li
                            key={item.skuId}
                            className={styles.itemRow}
                            data-rise=""
                            style={{ "--i": Math.min(itemIdx + 3, 6) } as React.CSSProperties}
                          >
                            <div className={styles.itemCheck}>
                              <input
                                type="checkbox"
                                className={shared.checkbox}
                                checked={item.selected}
                                disabled={outOfStock}
                                onChange={() => toggleCartItemSelected(item.skuId)}
                                aria-label={`Chọn ${item.productName}`}
                              />
                            </div>

                            {productHref ? (
                              <Link href={productHref} className={styles.itemThumbLink}>
                                <img
                                  src={item.imageUrl}
                                  alt={item.productName}
                                  width={896}
                                  height={1200}
                                  className={styles.itemThumb}
                                />
                              </Link>
                            ) : (
                              <div className={styles.itemThumbLink}>
                                <img
                                  src={item.imageUrl}
                                  alt={item.productName}
                                  width={896}
                                  height={1200}
                                  className={styles.itemThumb}
                                />
                              </div>
                            )}

                            <div className={styles.itemInfo}>
                              {productHref ? (
                                <Link href={productHref} className={styles.itemName}>
                                  {item.productName}
                                </Link>
                              ) : (
                                <span className={styles.itemName}>{item.productName}</span>
                              )}

                              {item.skuName && (
                                <p className={styles.itemVariant}>Phân loại: {item.skuName}</p>
                              )}

                              <div className={styles.itemTags}>
                                {outOfStock && (
                                  <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                                    Hết hàng
                                  </span>
                                )}
                                {item.priceChanged && (
                                  <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                    Giá vừa cập nhật (trước đó {formatVnd(item.addedPriceVnd)})
                                  </span>
                                )}
                                {clampedNote && (
                                  <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                    {clampedNote}
                                  </span>
                                )}
                              </div>

                              <div className={styles.priceChangedRow}>
                                <Price amount={item.priceVnd} size="sm" />
                                {item.priceChanged && item.addedPriceVnd > 0 && (
                                  <span className={`${styles.oldPriceStrike} num`}>
                                    {formatVnd(item.addedPriceVnd)}
                                  </span>
                                )}
                              </div>

                              <p className={styles.itemStock}>
                                {outOfStock ? (
                                  "Phân loại này tạm hết hàng, vui lòng xóa hoặc chọn phân loại khác."
                                ) : (
                                  <>
                                    Còn <span className="num">{item.availableStock}</span> sản phẩm
                                    sẵn giao
                                    {item.quantity >= item.availableStock ? " (đã đạt tối đa)" : ""}
                                  </>
                                )}
                              </p>
                            </div>

                            <div className={styles.itemActionsCol}>
                              <span className={`${styles.lineSubtotal} num`}>
                                {formatVnd(lineTotal)}
                              </span>

                              <div className={styles.stepper} aria-label="Điều chỉnh số lượng">
                                <button
                                  type="button"
                                  className={styles.stepBtn}
                                  disabled={isBusy}
                                  onClick={() =>
                                    void handleQtyChange(item.skuId, item.quantity - 1)
                                  }
                                  aria-label="Giảm 1 sản phẩm"
                                >
                                  <IconMinus size={14} />
                                </button>
                                <span className={`${styles.stepQty} num`}>{item.quantity}</span>
                                <button
                                  type="button"
                                  className={styles.stepBtn}
                                  disabled={
                                    isBusy || outOfStock || item.quantity >= item.availableStock
                                  }
                                  onClick={() =>
                                    void handleQtyChange(item.skuId, item.quantity + 1)
                                  }
                                  aria-label="Tăng 1 sản phẩm"
                                >
                                  <IconPlus size={14} />
                                </button>
                              </div>

                              <button
                                type="button"
                                className={styles.removeBtn}
                                disabled={isBusy}
                                onClick={() => void handleRemove(item.skuId)}
                              >
                                <IconTrash size={14} />
                                <span>Xóa</span>
                              </button>
                            </div>
                          </li>
                        );
                      })}
                    </ul>

                    {shopVouchers.length > 0 && (
                      <div className={styles.shopVoucherBar}>
                        <div className={styles.shopVoucherHead}>
                          <span className={styles.shopVoucherTitle}>
                            <IconTag size={15} />
                            <span>Ưu đãi của {shopName}</span>
                          </span>
                          {(shopBreakdown?.shop_voucher_discount_vnd ?? 0) > 0 && (
                            <span className={`${styles.breakdownDiscount} num`}>
                              {formatSignedVnd(-(shopBreakdown?.shop_voucher_discount_vnd ?? 0))}
                            </span>
                          )}
                        </div>

                        <div className={styles.voucherOptions}>
                          {shopVouchers.map((voucher) => {
                            const hasSelectedInShop = group.selectedSubtotalVnd > 0;
                            const eligible = hasSelectedInShop && voucher.eligible;
                            const isSelected = chosenShopCode === voucher.code;
                            const reasonText = !hasSelectedInShop
                              ? "Hãy chọn sản phẩm của gian hàng này để áp dụng mã."
                              : translateVoucherReason(voucher.ineligible_reason);

                            return (
                              <button
                                key={voucher.id}
                                type="button"
                                className={`${styles.voucherOption} ${
                                  isSelected ? styles.voucherOptionActive : ""
                                } ${!eligible && !isSelected ? styles.voucherOptionDisabled : ""}`}
                                disabled={!eligible && !isSelected}
                                onClick={() =>
                                  setShopVoucher(group.shopId, isSelected ? null : voucher.code)
                                }
                              >
                                <div className={styles.voucherCopy}>
                                  <span className={styles.voucherCodeLine}>
                                    {voucher.code} · {formatVoucherBenefit(voucher)}
                                  </span>
                                  <span className={styles.voucherDesc}>
                                    {eligible
                                      ? `${formatVoucherCondition(voucher)} · Còn ${formatInt(voucher.remaining_quota)} lượt`
                                      : reasonText || formatVoucherCondition(voucher)}
                                  </span>
                                </div>
                                <span className={styles.voucherActionLabel}>
                                  {isSelected ? "Bỏ chọn" : eligible ? "Áp dụng" : "Chưa đủ"}
                                </span>
                              </button>
                            );
                          })}
                        </div>
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
          </div>

          {/* Right column: Real promotions, Xu & integer VND breakdown */}
          <aside
            className={styles.summaryCol}
            aria-label="Tóm tắt đơn hàng và mã ưu đãi"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.summaryInner}>
              <h2 className={styles.summaryHeading}>Tóm tắt đơn hàng</h2>

              <div className={styles.destBanner}>
                <IconTruck size={18} className={styles.destIcon} />
                <div>
                  {defaultAddress ? (
                    <p>
                      Giao dự kiến tới{" "}
                      <strong>
                        {defaultAddress.ward_name}, {defaultAddress.province_name}
                      </strong>
                    </p>
                  ) : (
                    <p>Bạn sẽ chọn địa chỉ giao hàng và đơn vị vận chuyển ở bước tiếp theo.</p>
                  )}
                  <p className={styles.coinMeta}>
                    Hỗ trợ tách đơn tự động theo từng gian hàng và theo dõi hành trình giao nhận.
                  </p>
                </div>
              </div>

              {/* Manual voucher code input */}
              <form className={styles.summaryBlock} onSubmit={handleApplyManualCode} noValidate>
                <label htmlFor="cart-manual-code" className={styles.blockHeading}>
                  Nhập mã ưu đãi
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="cart-manual-code"
                    type="text"
                    className={shared.input}
                    value={manualCode}
                    onChange={(e) => setManualCode(e.target.value)}
                    placeholder="Nhập mã ưu đãi (ví dụ: NEXUS30)"
                  />
                  <Button type="submit" variant="ink" size="md" disabled={applyingCode}>
                    {applyingCode ? "Đang kiểm..." : "Áp mã"}
                  </Button>
                </div>
                {codeFeedback && (
                  <p
                    className={`${shared.feedbackBox} ${
                      codeFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                    }`}
                    role={codeFeedback.ok ? "status" : "alert"}
                  >
                    {codeFeedback.message}
                  </p>
                )}
              </form>

              {/* Platform vouchers */}
              {platformVouchers.length > 0 && (
                <div className={styles.summaryBlock}>
                  <h3 className={styles.blockHeading}>Ưu đãi toàn sàn</h3>
                  <div className={styles.voucherOptions}>
                    {platformVouchers.map((voucher) => {
                      const eligible = selectedCount > 0 && voucher.eligible;
                      const isSelected = platformVoucherCode === voucher.code;
                      const reasonText =
                        selectedCount <= 0
                          ? "Hãy chọn sản phẩm để áp dụng mã."
                          : translateVoucherReason(voucher.ineligible_reason);

                      return (
                        <button
                          key={voucher.id}
                          type="button"
                          className={`${styles.voucherOption} ${
                            isSelected ? styles.voucherOptionActive : ""
                          } ${!eligible && !isSelected ? styles.voucherOptionDisabled : ""}`}
                          disabled={!eligible && !isSelected}
                          onClick={() => setPlatformVoucher(isSelected ? null : voucher.code)}
                        >
                          <div className={styles.voucherCopy}>
                            <span className={styles.voucherCodeLine}>
                              {voucher.code} · {formatVoucherBenefit(voucher)}
                            </span>
                            <span className={styles.voucherDesc}>
                              {eligible
                                ? `${formatVoucherCondition(voucher)} · Còn ${formatInt(voucher.remaining_quota)} lượt`
                                : reasonText || formatVoucherCondition(voucher)}
                            </span>
                          </div>
                          <span className={styles.voucherActionLabel}>
                            {isSelected ? "Bỏ chọn" : eligible ? "Chọn mã" : "Chưa đủ"}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Freeship vouchers */}
              {freeshipVouchers.length > 0 && (
                <div className={styles.summaryBlock}>
                  <h3 className={styles.blockHeading}>Mã hỗ trợ vận chuyển</h3>
                  <div className={styles.voucherOptions}>
                    {freeshipVouchers.map((voucher) => {
                      const eligible = selectedCount > 0 && voucher.eligible;
                      const isSelected = freeshipVoucherCode === voucher.code;
                      const reasonText =
                        selectedCount <= 0
                          ? "Hãy chọn sản phẩm để áp dụng mã."
                          : translateVoucherReason(voucher.ineligible_reason);

                      return (
                        <button
                          key={voucher.id}
                          type="button"
                          className={`${styles.voucherOption} ${
                            isSelected ? styles.voucherOptionActive : ""
                          } ${!eligible && !isSelected ? styles.voucherOptionDisabled : ""}`}
                          disabled={!eligible && !isSelected}
                          onClick={() => setFreeshipVoucher(isSelected ? null : voucher.code)}
                        >
                          <div className={styles.voucherCopy}>
                            <span className={styles.voucherCodeLine}>
                              {voucher.code} · {formatVoucherBenefit(voucher)}
                            </span>
                            <span className={styles.voucherDesc}>
                              {eligible
                                ? `${formatVoucherCondition(voucher)} · Còn ${formatInt(voucher.remaining_quota)} lượt`
                                : reasonText || formatVoucherCondition(voucher)}
                            </span>
                          </div>
                          <span className={styles.voucherActionLabel}>
                            {isSelected ? "Bỏ chọn" : eligible ? "Chọn mã" : "Chưa đủ"}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Real NexusMart Coins */}
              <div className={styles.summaryBlock}>
                <h3 className={styles.blockHeading}>Dùng Xu NexusMart</h3>
                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={useCoins && coinBalance > 0}
                    disabled={coinBalance <= 0 || selectedCount <= 0}
                    onChange={(e) => setUseCoins(e.target.checked)}
                  />
                  <span>
                    Dùng Xu khả dụng (<strong className="num">{formatInt(coinBalance)} Xu</strong>)
                  </span>
                </label>
                <p className={styles.coinMeta}>
                  Số dư hiện tại: <strong className="num">{formatInt(coinBalance)} Xu</strong> (1 Xu
                  = 1₫).
                </p>
              </div>

              {translatedWarnings.length > 0 && (
                <div className={styles.warningStack} role="status">
                  {translatedWarnings.map((w) => (
                    <p key={w} className={`${shared.feedbackBox} ${shared.feedbackWarn}`}>
                      {w}
                    </p>
                  ))}
                </div>
              )}

              {previewError && (
                <p className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                  {previewError}
                </p>
              )}

              {/* Real integer VND breakdown from POST /promotions/preview */}
              <dl className={styles.breakdown}>
                {previewLoading && (
                  <p className={styles.previewLoadingNote}>Đang tính lại ưu đãi...</p>
                )}

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>
                    Tạm tính (<span className="num">{selectedCount}</span> sản phẩm)
                  </dt>
                  <dd className="num">{formatVnd(subtotalDisplay)}</dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Ưu đãi gian hàng</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {shopVoucherDisplay > 0 ? formatSignedVnd(-shopVoucherDisplay) : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Ưu đãi toàn sàn</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {platformVoucherDisplay > 0 ? formatSignedVnd(-platformVoucherDisplay) : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Phí vận chuyển dự kiến</dt>
                  <dd className="num">
                    {defaultAddress ? formatVnd(shippingDisplay) : "Tính ở bước thanh toán"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Giảm phí vận chuyển</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {freeshipDisplay > 0 ? formatSignedVnd(-freeshipDisplay) : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Trừ Xu NexusMart</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {coinsDisplay > 0 ? formatSignedVnd(-coinsDisplay) : "0₫"}
                  </dd>
                </div>
              </dl>
            </div>

            <div className={styles.ctaBand}>
              <div className={styles.grandRow}>
                <span className={styles.grandLabel}>Tổng tạm tính</span>
                <span className={`${styles.grandAmount} num`}>{formatVnd(grandTotalDisplay)}</span>
              </div>

              {totalDiscountDisplay > 0 && (
                <p className={styles.savingsNote}>
                  Đã tiết kiệm: <strong className="num">{formatVnd(totalDiscountDisplay)}</strong>
                </p>
              )}

              {selectedCount > 0 ? (
                <div className={styles.ctaStack}>
                  <Button
                    variant="primary"
                    size="lg"
                    fullWidth
                    className={shared.wrapBtn}
                    href="/thanh-toan"
                  >
                    Tiến hành thanh toán · {formatVnd(grandTotalDisplay)}
                  </Button>
                </div>
              ) : (
                <div className={styles.ctaStack}>
                  <Button variant="primary" size="lg" fullWidth disabled>
                    Tiến hành thanh toán
                  </Button>
                  <p className={styles.ctaHint}>
                    Hãy chọn ít nhất 1 sản phẩm còn hàng bên trái để tiếp tục thanh toán.
                  </p>
                </div>
              )}
            </div>
          </aside>
        </div>
      </section>

      <CheckoutSuggestionStrip
        title="Đồ cơ bản thường mua kèm"
        subtitle="Sản phẩm đang có sẵn từ các xưởng dệt NexusMart, thêm nhanh vào giỏ hàng."
        products={suggestedProducts}
        shopNameById={shopNameById}
        showQuickAdd
      />

      {/* Sticky mobile checkout bar */}
      <div className={styles.mobileStickyBar} aria-label="Tiến hành thanh toán">
        <div className={styles.mobileTotalWrap}>
          <span className={styles.mobileTotalLabel}>
            Đã chọn <span className="num">{selectedCount}</span> sản phẩm
          </span>
          <span className={`${styles.mobileTotalVal} num`}>{formatVnd(grandTotalDisplay)}</span>
        </div>
        {selectedCount > 0 ? (
          <Button variant="primary" size="md" href="/thanh-toan">
            Thanh toán
          </Button>
        ) : (
          <Button variant="primary" size="md" disabled>
            Chọn hàng
          </Button>
        )}
      </div>
    </div>
  );
}

export default function CartPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải giỏ hàng NexusMart..." rows={4} />
        </div>
      }
    >
      <CartPageContent />
    </Suspense>
  );
}
