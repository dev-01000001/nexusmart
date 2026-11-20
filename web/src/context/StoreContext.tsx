"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import {
  ADMIN_DISPUTES,
  ADMIN_RECONCILIATION,
  ADMIN_SHOP_APPROVALS,
  COMPLAINT_SLA,
  DEMO_USER,
  EDU_DISCOUNT_CONFIG,
  FLASH_SALE_RULES,
  GIFT_CARD_POLICY,
  INITIAL_ADDRESSES,
  INITIAL_CHAT_THREADS,
  INITIAL_MEMBERSHIP_STATE,
  INITIAL_ORDERS,
  INITIAL_PLATFORM_CONFIG,
  INITIAL_USER_COINS,
  PRODUCTS,
  SAMPLE_QAS,
  SAMPLE_REVIEWS,
  SHOPS,
  calculateCartSummary,
  computeOrderTotals,
  getShopById,
  getTierBySpend,
} from "@/data";
import type {
  AdminDispute,
  CartComboSelection,
  CartItem,
  CartSummary,
  ChatThread,
  DeliveryMethod,
  DemoUser,
  FitFeedback,
  GiftCardItem,
  MembershipProfileState,
  MembershipTier,
  Order,
  OrderExchangeDetail,
  OrderStatus,
  OrderVatInvoice,
  PaymentMethod,
  PaymentReconciliationRow,
  PlatformConfigState,
  ProductQA,
  ProductReview,
  ShopApprovalApplication,
  SizeCode,
  UserAddress,
} from "@/data";
import { getProvinceByCode } from "@/lib/provinces";
import { DEFAULT_BODY_PROFILE, type BodyProfile } from "@/lib/sizing";

const STORAGE_KEY = "nexusmart_store_v2";

export interface WishlistEntry {
  productId: string;
  skuCode?: string;
  addedAt: string;
}

export interface BackInStockAlert {
  id: string;
  productId: string;
  skuCode: string;
  colorName: string;
  size: string;
  contact: string;
  createdAt: string;
}

export interface CookiePreferences {
  essential: true;
  analytics: boolean;
  personalization: boolean;
  updatedAt?: string;
}

export interface GuestCheckoutDraft {
  enabled: boolean;
  fullName: string;
  phone: string;
  email: string;
  provinceCode: string;
  wardName: string;
  streetDetail: string;
}

export interface VatInvoicePayload {
  companyName: string;
  taxCode: string;
  email: string;
  companyAddress?: string;
}

export interface ExchangeOrderPayload {
  type: OrderExchangeDetail["type"];
  reason: string;
  fromSize?: string;
  toSize?: string;
  pickupAddress?: string;
}

export interface SupportTicket {
  id: string;
  kind: "khieu_nai" | "goi_lai" | "bu_le_tat";
  orderCode?: string;
  fullName: string;
  phone: string;
  email?: string;
  subject: string;
  detail: string;
  isVulnerablePriority: boolean;
  slaLabel: string;
  status: "da_tiep_nhan" | "dang_xu_ly";
  createdAt: string;
}

export interface SubmitSupportTicketInput {
  kind: SupportTicket["kind"];
  orderCode?: string;
  fullName: string;
  phone: string;
  email?: string;
  subject: string;
  detail: string;
  isVulnerablePriority?: boolean;
}

export interface SubmitReviewInput {
  productId: string;
  authorName?: string;
  rating: number;
  fitFeedback: FitFeedback;
  buyerHeightCm: number;
  buyerWeightKg: number;
  purchasedSize: SizeCode;
  colorName: string;
  comment: string;
  images?: string[];
}

export interface SubmitQAInput {
  productId: string;
  askerName?: string;
  question: string;
}

export interface PlaceOrderInput {
  addressId?: string;
  paymentMethod: PaymentMethod;
  note?: string;
  // Wave 2 optional checkout fields
  guestAddress?: {
    recipientName: string;
    phone: string;
    email?: string;
    provinceCode: string;
    provinceName: string;
    wardName: string;
    streetDetail: string;
  };
  deliveryMethod?: DeliveryMethod;
  giftWrap?: boolean;
  giftCardMessage?: string;
  vatInfo?: VatInvoicePayload;
  simulatePaymentPending?: boolean;
}

export interface AddToCartInput {
  productId: string;
  colorCode: string;
  size: string;
  quantity?: number;
  overridePrice?: number;
  // Wave 2 optional flags
  isShockDeal?: boolean;
  isFlashSaleItem?: boolean;
  comboSelections?: CartComboSelection[];
}

export interface StoreContextValue {
  // Auth & user profile (mock)
  user: DemoUser;
  isAuthenticated: boolean;
  loginMock: (email: string, fullName?: string) => void;
  registerMock: (fullName: string, email: string, phone: string) => void;
  logoutMock: () => void;

  // 2-level address book (34 provinces -> ward/commune)
  addresses: UserAddress[];
  selectedAddressId: string;
  selectedAddress: UserAddress | undefined;
  selectAddress: (id: string) => void;
  addAddress: (addr: Omit<UserAddress, "id">) => UserAddress;
  setDefaultAddress: (id: string) => void;
  deleteAddress: (id: string) => void;

  // Cart & voucher/coin/tier/gift-card pricing
  cartItems: CartItem[];
  cartCount: number;
  cartSummary: CartSummary;
  selectedShopVouchers: Record<string, string | null>;
  platformVoucherCode: string | null;
  freeshipVoucherCode: string | null;
  savedVoucherCodes: string[];
  saveVoucherCode: (code: string) => void;
  useCoins: boolean;
  deliveryMethod: DeliveryMethod;
  setDeliveryMethod: (method: DeliveryMethod) => void;
  addToCart: (input: AddToCartInput) => boolean;
  updateCartQuantity: (skuCode: string, quantity: number) => void;
  removeFromCart: (skuCode: string) => void;
  toggleCartItemSelected: (skuCode: string) => void;
  toggleShopSelected: (shopId: string, selected: boolean) => void;
  toggleSelectAllCart: (selected: boolean) => void;
  setShopVoucher: (shopId: string, code: string | null) => void;
  setPlatformVoucher: (code: string | null) => void;
  setFreeshipVoucher: (code: string | null) => void;
  setUseCoins: (enabled: boolean) => void;
  clearCart: () => void;

  // Guest checkout & Orders & Post-purchase flows
  guestCheckout: GuestCheckoutDraft;
  setGuestCheckout: (patch: Partial<GuestCheckoutDraft>) => void;
  orders: Order[];
  placeOrder: (input: PlaceOrderInput) => Order[];
  cancelOrder: (orderId: string, reason: string) => void;
  requestReturnOrder: (orderId: string, reason: string) => void;
  updateOrderStatus: (orderId: string, nextStatus: OrderStatus, note?: string) => void;
  lookupOrder: (code: string, phone: string) => Order | undefined;
  retryOrderPayment: (orderId: string, method: PaymentMethod) => boolean;
  requestEditOrderAddress: (orderId: string, address: string, reason?: string) => boolean;
  requestExchangeOrder: (orderId: string, payload: ExchangeOrderPayload) => boolean;
  requestVatInvoice: (orderId: string, payload: VatInvoicePayload) => boolean;

  // Body profile, wishlist, recently viewed, back-in-stock
  bodyProfile: BodyProfile;
  updateBodyProfile: (next: BodyProfile) => void;
  wishlist: WishlistEntry[];
  toggleWishlist: (productId: string, skuCode?: string) => boolean;
  isWishlisted: (productId: string, skuCode?: string) => boolean;
  recentlyViewed: string[];
  recordProductView: (productId: string) => void;
  clearRecentlyViewed: () => void;
  backInStockAlerts: BackInStockAlert[];
  subscribeBackInStock: (input: Omit<BackInStockAlert, "id" | "createdAt">) => BackInStockAlert;
  hasBackInStockAlert: (skuCode: string) => boolean;

  // Reviews, Q&As & Support tickets
  reviews: ProductReview[];
  submitReview: (input: SubmitReviewInput) => ProductReview;
  productQAs: ProductQA[];
  submitProductQA: (input: SubmitQAInput) => ProductQA;
  supportTickets: SupportTicket[];
  submitSupportTicket: (input: SubmitSupportTicketInput) => SupportTicket;

  // Cookie preferences & modal
  cookiePrefs: CookiePreferences;
  updateCookiePrefs: (next: Partial<CookiePreferences>) => void;
  cookieModalOpen: boolean;
  openCookieModal: () => void;
  closeCookieModal: () => void;

  // Membership (Hội viên Nexus), Referrals, Birthday, Gift Cards & .edu
  membership: MembershipProfileState;
  membershipTier: MembershipTier;
  updateBirthday: (birthday: string) => void;
  claimBirthdayGift: () => void;
  inviteFriendMock: (friendName: string) => void;
  purchaseGiftCard: (input: {
    amountVnd: number;
    customCode?: string;
    recipientEmail?: string;
    message?: string;
  }) => GiftCardItem;
  applyGiftCardCode: (code: string | null) => { ok: boolean; message: string };
  verifyEduEmail: (email: string) => { ok: boolean; message: string };

  // Buyer-seller chat & AI assistant overlays
  chatOpen: boolean;
  chatShopId: string | null;
  openChat: (shopId?: string) => void;
  closeChat: () => void;
  aiOpen: boolean;
  openAi: () => void;
  closeAi: () => void;
  chatThreads: ChatThread[];
  sendChatMessage: (threadId: string, sender: "buyer" | "seller", text: string) => void;

  // Seller workspace selection
  activeSellerShopId: string;
  setActiveSellerShopId: (shopId: string) => void;

  // Admin interactive queues (shared so sidebar badges stay live)
  shopApprovals: ShopApprovalApplication[];
  updateShopApprovalStatus: (
    id: string,
    status: ShopApprovalApplication["status"],
    note?: string
  ) => void;
  disputes: AdminDispute[];
  updateDisputeStatus: (id: string, status: AdminDispute["status"], note?: string) => void;
  reconciliationRows: PaymentReconciliationRow[];
  resolveReconciliationBatch: (id: string, note?: string) => void;
  platformConfig: PlatformConfigState;
  updatePlatformConfig: (next: PlatformConfigState) => void;

  // Lightweight toast notification
  toastMessage: string | null;
  showToast: (message: string) => void;
  dismissToast: () => void;
}

const INITIAL_CART_ITEMS: CartItem[] = [
  {
    skuCode: "BD-AT01-TRANG-L",
    productId: "prod-at-trang",
    productSlug: "ao-thun-cotton-compact-trang",
    productName: "Áo thun Cotton Compact 220gsm Trắng",
    shopId: "shop-bach-dang",
    colorCode: "trang",
    colorName: "Trắng tinh",
    size: "L",
    image: "/products/ao-thun-cotton-trang.png",
    unitPrice: 179000,
    originalPrice: 219000,
    quantity: 2,
    maxStock: 26,
    selected: true,
  },
  {
    skuCode: "DN-PL06-KEM-L",
    productId: "prod-polo-kem",
    productSlug: "ao-polo-pique-cotton-kem-nhat",
    productName: "Áo Polo Pique Cotton Kem nhạt",
    shopId: "shop-da-nang",
    colorCode: "kem",
    colorName: "Kem nhạt",
    size: "L",
    image: "/products/ao-polo-pique-kem.png",
    unitPrice: 289000,
    originalPrice: 339000,
    quantity: 1,
    maxStock: 23,
    selected: true,
  },
];

const INITIAL_COOKIE_PREFS: CookiePreferences = {
  essential: true,
  analytics: true,
  personalization: true,
};

const INITIAL_GUEST_DRAFT: GuestCheckoutDraft = {
  enabled: false,
  fullName: "",
  phone: "",
  email: "",
  provinceCode: "HCM",
  wardName: "Phường Xuân Hòa",
  streetDetail: "",
};

const INITIAL_WISHLIST: WishlistEntry[] = [
  { productId: "prod-at-dryfit", addedAt: "2026-11-18T10:00:00+07:00" },
  { productId: "prod-polo-detkim", addedAt: "2026-11-19T15:30:00+07:00" },
];

const INITIAL_RECENTLY_VIEWED: string[] = [
  "prod-at-trang",
  "prod-polo-kem",
  "prod-short-daily",
  "prod-jogger-terry",
];

const INITIAL_SUPPORT_TICKETS: SupportTicket[] = [
  {
    id: "tk-261119-01",
    kind: "bu_le_tat",
    orderCode: "NM-261117-082",
    fullName: DEMO_USER.fullName,
    phone: DEMO_USER.phone,
    email: DEMO_USER.email,
    subject: "Đề nghị bù lẻ 1 chiếc tất màu xám sáng",
    detail:
      "Bị thất lạc 1 chiếc tất sau lần giặt đầu, đề nghị gửi kèm đơn kế tiếp từ Dệt Kim Hà Nội.",
    isVulnerablePriority: false,
    slaLabel: "Xác nhận trong 24h · Xử lý 5-7 ngày làm việc",
    status: "dang_xu_ly",
    createdAt: "2026-11-19T10:30:00+07:00",
  },
];

function digitsOnly(phone: string): string {
  return phone.replace(/\D/g, "");
}

const StoreContext = createContext<StoreContextValue | null>(null);

export function StoreProvider({ children }: { children: React.ReactNode }) {
  const [hydrated, setHydrated] = useState(false);

  const [user, setUser] = useState<DemoUser>({ ...DEMO_USER, coins: INITIAL_USER_COINS });
  const [isAuthenticated, setIsAuthenticated] = useState(true);

  const [addresses, setAddresses] = useState<UserAddress[]>(INITIAL_ADDRESSES);
  const [selectedAddressId, setSelectedAddressId] = useState<string>(
    INITIAL_ADDRESSES.find((a) => a.isDefault)?.id ?? INITIAL_ADDRESSES[0].id
  );

  const [cartItems, setCartItems] = useState<CartItem[]>(INITIAL_CART_ITEMS);
  const [selectedShopVouchers, setSelectedShopVouchers] = useState<Record<string, string | null>>({
    "shop-bach-dang": "BACHDANG20",
  });
  const [platformVoucherCode, setPlatformVoucherCode] = useState<string | null>("NEXUS30");
  const [freeshipVoucherCode, setFreeshipVoucherCode] = useState<string | null>("FREESHIP25");
  const [savedVoucherCodes, setSavedVoucherCodes] = useState<string[]>([
    "NEXUS30",
    "FREESHIP25",
    "BACHDANG20",
    "DANANG30",
  ]);
  const [useCoins, setUseCoins] = useState<boolean>(false);
  const [deliveryMethod, setDeliveryMethod] = useState<DeliveryMethod>("standard");
  const [shockDealsClaimedToday, setShockDealsClaimedToday] = useState<number>(0);

  const [guestCheckout, setGuestCheckoutState] = useState<GuestCheckoutDraft>(INITIAL_GUEST_DRAFT);
  const [orders, setOrders] = useState<Order[]>(INITIAL_ORDERS);

  const [bodyProfile, setBodyProfile] = useState<BodyProfile>(DEFAULT_BODY_PROFILE);
  const [wishlist, setWishlist] = useState<WishlistEntry[]>(INITIAL_WISHLIST);
  const [recentlyViewed, setRecentlyViewed] = useState<string[]>(INITIAL_RECENTLY_VIEWED);
  const [backInStockAlerts, setBackInStockAlerts] = useState<BackInStockAlert[]>([]);
  const [reviews, setReviews] = useState<ProductReview[]>(SAMPLE_REVIEWS);
  const [productQAs, setProductQAs] = useState<ProductQA[]>(SAMPLE_QAS);
  const [supportTickets, setSupportTickets] = useState<SupportTicket[]>(INITIAL_SUPPORT_TICKETS);

  const [cookiePrefs, setCookiePrefs] = useState<CookiePreferences>(INITIAL_COOKIE_PREFS);
  const [cookieModalOpen, setCookieModalOpen] = useState(false);

  const [membership, setMembership] = useState<MembershipProfileState>(INITIAL_MEMBERSHIP_STATE);

  const [chatOpen, setChatOpen] = useState(false);
  const [chatShopId, setChatShopId] = useState<string | null>("shop-bach-dang");
  const [aiOpen, setAiOpen] = useState(false);
  const [chatThreads, setChatThreads] = useState<ChatThread[]>(INITIAL_CHAT_THREADS);

  const [activeSellerShopId, setActiveSellerShopId] = useState<string>(SHOPS[0].id);

  const [shopApprovals, setShopApprovals] =
    useState<ShopApprovalApplication[]>(ADMIN_SHOP_APPROVALS);
  const [disputes, setDisputes] = useState<AdminDispute[]>(ADMIN_DISPUTES);
  const [reconciliationRows, setReconciliationRows] =
    useState<PaymentReconciliationRow[]>(ADMIN_RECONCILIATION);
  const [platformConfig, setPlatformConfig] =
    useState<PlatformConfigState>(INITIAL_PLATFORM_CONFIG);

  const [toastMessage, setToastMessage] = useState<string | null>(null);

  // Hydration-safe load from localStorage after mount
  useEffect(() => {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY);
      if (raw) {
        const parsed = JSON.parse(raw) as Partial<{
          bodyProfile: BodyProfile;
          wishlist: WishlistEntry[];
          recentlyViewed: string[];
          backInStockAlerts: BackInStockAlert[];
          cookiePrefs: CookiePreferences;
          savedVoucherCodes: string[];
          membership: MembershipProfileState;
        }>;
        if (parsed.bodyProfile && typeof parsed.bodyProfile.heightCm === "number") {
          setBodyProfile(parsed.bodyProfile);
        }
        if (Array.isArray(parsed.wishlist)) {
          setWishlist(parsed.wishlist);
        }
        if (Array.isArray(parsed.recentlyViewed)) {
          setRecentlyViewed(parsed.recentlyViewed);
        }
        if (Array.isArray(parsed.backInStockAlerts)) {
          setBackInStockAlerts(parsed.backInStockAlerts);
        }
        if (parsed.cookiePrefs && typeof parsed.cookiePrefs.analytics === "boolean") {
          setCookiePrefs({
            essential: true,
            analytics: parsed.cookiePrefs.analytics,
            personalization: parsed.cookiePrefs.personalization,
            updatedAt: parsed.cookiePrefs.updatedAt,
          });
        }
        if (Array.isArray(parsed.savedVoucherCodes)) {
          setSavedVoucherCodes(parsed.savedVoucherCodes);
        }
        if (parsed.membership && typeof parsed.membership.spend4Q === "number") {
          setMembership((prev) => ({
            ...prev,
            ...parsed.membership,
          }));
        }
      }
    } catch {
      // Ignore storage read errors in restricted environments
    }
    setHydrated(true);
  }, []);

  // Persist Wave 2 user preference slices to localStorage after hydration
  useEffect(() => {
    if (!hydrated) return;
    try {
      window.localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({
          bodyProfile,
          wishlist,
          recentlyViewed,
          backInStockAlerts,
          cookiePrefs,
          savedVoucherCodes,
          membership,
        })
      );
    } catch {
      // Ignore quota / privacy mode errors
    }
  }, [
    hydrated,
    bodyProfile,
    wishlist,
    recentlyViewed,
    backInStockAlerts,
    cookiePrefs,
    savedVoucherCodes,
    membership,
  ]);

  const showToast = useCallback((message: string) => {
    setToastMessage(message);
  }, []);

  const dismissToast = useCallback(() => {
    setToastMessage(null);
  }, []);

  const membershipTier = useMemo(() => getTierBySpend(membership.spend4Q), [membership.spend4Q]);

  const activeGiftCard = useMemo(() => {
    if (!membership.appliedGiftCardCode) return undefined;
    return membership.giftCards.find(
      (gc) =>
        gc.code.toUpperCase() === membership.appliedGiftCardCode?.toUpperCase() &&
        gc.remainingVnd > 0
    );
  }, [membership.appliedGiftCardCode, membership.giftCards]);

  const birthdayGiftEligible = useMemo(() => {
    return (
      isAuthenticated &&
      membershipTier.id !== "thanh_vien" &&
      Boolean(membership.birthday) &&
      !membership.birthdayGiftClaimed
    );
  }, [isAuthenticated, membershipTier.id, membership.birthday, membership.birthdayGiftClaimed]);

  const selectedAddress = useMemo(
    () => addresses.find((a) => a.id === selectedAddressId) ?? addresses[0],
    [addresses, selectedAddressId]
  );

  const effectiveProvinceCode = guestCheckout.enabled
    ? guestCheckout.provinceCode || "HCM"
    : (selectedAddress?.provinceCode ?? "HCM");

  const cartSummary = useMemo(
    () =>
      calculateCartSummary({
        items: cartItems,
        provinceCode: effectiveProvinceCode,
        selectedShopVouchers,
        platformVoucherCode,
        freeshipVoucherCode,
        useCoins,
        availableCoins: user.coins,
        applyTierDiscounts: true,
        giftCardDiscount: activeGiftCard?.remainingVnd ?? 0,
        deliveryMethod,
        birthdayGiftEligible,
        cashbackRate: membershipTier.cashbackRate,
        eduVerified: membership.eduVerified,
        memberTier: membershipTier.id,
      }),
    [
      cartItems,
      effectiveProvinceCode,
      selectedShopVouchers,
      platformVoucherCode,
      freeshipVoucherCode,
      useCoins,
      user.coins,
      activeGiftCard?.remainingVnd,
      deliveryMethod,
      birthdayGiftEligible,
      membershipTier.cashbackRate,
      membershipTier.id,
      membership.eduVerified,
    ]
  );

  const cartCount = useMemo(
    () => cartItems.reduce((sum, item) => sum + item.quantity, 0),
    [cartItems]
  );

  const addToCart = useCallback(
    (input: AddToCartInput): boolean => {
      const product = PRODUCTS.find((p) => p.id === input.productId);
      if (!product) return false;
      const sku = product.skus.find(
        (s) => s.colorCode === input.colorCode && s.size === input.size
      );
      if (!sku || sku.stock <= 0) {
        showToast("Phân loại sản phẩm này hiện đã hết hàng.");
        return false;
      }

      if (input.isShockDeal) {
        if (shockDealsClaimedToday >= FLASH_SALE_RULES.shockDealMaxPerDay) {
          showToast(
            `Bạn đã dùng hết hạn mức ${FLASH_SALE_RULES.shockDealMaxPerDay} suất Deal sốc trong hôm nay.`
          );
          return false;
        }
        const alreadyHasShock = cartItems.some((it) => it.isShockDeal);
        if (alreadyHasShock) {
          showToast("Mỗi đơn hàng chỉ áp dụng tối đa 1 sản phẩm Deal sốc.");
          return false;
        }
      }

      if (input.isFlashSaleItem) {
        const currentFlashCount = cartItems
          .filter((it) => it.isFlashSaleItem)
          .reduce((s, it) => s + it.quantity, 0);
        if (currentFlashCount >= FLASH_SALE_RULES.maxItemsPerOrder) {
          showToast(
            `Flash sale giới hạn tối đa ${FLASH_SALE_RULES.maxItemsPerOrder} sản phẩm ưu đãi mỗi đơn.`
          );
          return false;
        }
      }

      const color = product.colors.find((c) => c.code === input.colorCode);
      const maxAllowed = Math.min(
        sku.stock,
        input.isShockDeal
          ? FLASH_SALE_RULES.shockDealMaxPerOrder
          : (product.maxPerOrder ?? sku.stock)
      );
      const qtyToAdd = Math.max(1, Math.min(maxAllowed, Math.trunc(input.quantity ?? 1)));
      const unitPrice = input.overridePrice ?? sku.price;

      const comboSuffix =
        input.comboSelections && input.comboSelections.length > 0
          ? `-${input.comboSelections.map((c) => c.size).join("")}`
          : "";
      const cartSkuKey = `${sku.skuCode}${comboSuffix}`;
      const sizeLabel =
        input.comboSelections && input.comboSelections.length > 0
          ? input.comboSelections.map((c) => c.size).join(" / ")
          : sku.size;

      setCartItems((prev) => {
        const existing = prev.find((it) => it.skuCode === cartSkuKey);
        if (existing) {
          return prev.map((it) =>
            it.skuCode === cartSkuKey
              ? {
                  ...it,
                  unitPrice,
                  quantity: Math.min(maxAllowed, it.quantity + qtyToAdd),
                  selected: true,
                }
              : it
          );
        }
        return [
          ...prev,
          {
            skuCode: cartSkuKey,
            productId: product.id,
            productSlug: product.slug,
            productName: product.name,
            shopId: product.shopId,
            colorCode: sku.colorCode,
            colorName: color?.name ?? sku.colorCode,
            size: sizeLabel,
            image: color?.image ?? product.image,
            unitPrice,
            originalPrice: product.originalPrice,
            quantity: Math.min(maxAllowed, qtyToAdd),
            maxStock: maxAllowed,
            selected: true,
            isShockDeal: input.isShockDeal,
            isFlashSaleItem: input.isFlashSaleItem,
            comboSelections: input.comboSelections,
          },
        ];
      });
      if (input.isShockDeal) {
        setShockDealsClaimedToday((n) => n + 1);
      }
      showToast(`Đã thêm ${product.shortName} (${sizeLabel}) vào giỏ hàng.`);
      return true;
    },
    [cartItems, shockDealsClaimedToday, showToast]
  );

  const updateCartQuantity = useCallback((skuCode: string, quantity: number) => {
    const nextQty = Math.trunc(quantity);
    if (nextQty <= 0) {
      setCartItems((prev) => prev.filter((it) => it.skuCode !== skuCode));
      return;
    }
    setCartItems((prev) =>
      prev.map((it) =>
        it.skuCode === skuCode ? { ...it, quantity: Math.min(it.maxStock, nextQty) } : it
      )
    );
  }, []);

  const removeFromCart = useCallback((skuCode: string) => {
    setCartItems((prev) => prev.filter((it) => it.skuCode !== skuCode));
  }, []);

  const toggleCartItemSelected = useCallback((skuCode: string) => {
    setCartItems((prev) =>
      prev.map((it) => (it.skuCode === skuCode ? { ...it, selected: !it.selected } : it))
    );
  }, []);

  const toggleShopSelected = useCallback((shopId: string, selected: boolean) => {
    setCartItems((prev) => prev.map((it) => (it.shopId === shopId ? { ...it, selected } : it)));
  }, []);

  const toggleSelectAllCart = useCallback((selected: boolean) => {
    setCartItems((prev) => prev.map((it) => ({ ...it, selected })));
  }, []);

  const setShopVoucher = useCallback((shopId: string, code: string | null) => {
    setSelectedShopVouchers((prev) => ({ ...prev, [shopId]: code }));
  }, []);

  const saveVoucherCode = useCallback(
    (code: string) => {
      const clean = code.trim().toUpperCase();
      if (!clean) return;
      setSavedVoucherCodes((prev) => (prev.includes(clean) ? prev : [clean, ...prev]));
      showToast(`Đã lưu mã ${clean} vào ví voucher.`);
    },
    [showToast]
  );

  const clearCart = useCallback(() => {
    setCartItems([]);
  }, []);

  const addAddress = useCallback((addr: Omit<UserAddress, "id">): UserAddress => {
    const created: UserAddress = {
      ...addr,
      id: `addr-${Date.now()}`,
    };
    setAddresses((prev) => {
      const normalized = created.isDefault
        ? prev.map((item) => ({ ...item, isDefault: false }))
        : prev;
      return [created, ...normalized];
    });
    setSelectedAddressId(created.id);
    return created;
  }, []);

  const setDefaultAddress = useCallback((id: string) => {
    setAddresses((prev) => prev.map((a) => ({ ...a, isDefault: a.id === id })));
    setSelectedAddressId(id);
  }, []);

  const deleteAddress = useCallback((id: string) => {
    setAddresses((prev) => {
      const next = prev.filter((a) => a.id !== id);
      if (next.length > 0 && !next.some((a) => a.isDefault)) {
        next[0] = { ...next[0], isDefault: true };
      }
      return next;
    });
  }, []);

  const setGuestCheckout = useCallback((patch: Partial<GuestCheckoutDraft>) => {
    setGuestCheckoutState((prev) => ({ ...prev, ...patch }));
  }, []);

  const placeOrder = useCallback(
    (input: PlaceOrderInput): Order[] => {
      const resolvedAddr = input.guestAddress
        ? {
            recipientName: input.guestAddress.recipientName,
            phone: input.guestAddress.phone,
            email: input.guestAddress.email,
            provinceCode: input.guestAddress.provinceCode,
            provinceName: input.guestAddress.provinceName,
            wardName: input.guestAddress.wardName,
            streetDetail: input.guestAddress.streetDetail,
            isGuest: true,
          }
        : (() => {
            const found = addresses.find((a) => a.id === input.addressId) ?? selectedAddress;
            if (!found) return null;
            return {
              recipientName: found.recipientName,
              phone: found.phone,
              email: user.email,
              provinceCode: found.provinceCode,
              provinceName: found.provinceName,
              wardName: found.wardName,
              streetDetail: found.streetDetail,
              isGuest: !isAuthenticated,
            };
          })();

      if (!resolvedAddr) return [];

      const activeGroups = cartSummary.groups.filter((g) => g.selectedSubtotal > 0);
      if (activeGroups.length === 0) return [];

      const nowIso = new Date().toISOString();
      const province = getProvinceByCode(resolvedAddr.provinceCode);
      const totalSelectedMerch = activeGroups.reduce((s, g) => s + g.selectedSubtotal, 0);
      const activeDelivery = input.deliveryMethod ?? deliveryMethod;
      const parentCode = `NMP-261120-${Math.floor(10 + Math.random() * 89)}`;

      let remainingPlatform = cartSummary.platformVoucherDiscount;
      let remainingFreeship = cartSummary.freeshipDiscount;
      let remainingCoins = cartSummary.coinDiscount;
      let remainingGiftCard = cartSummary.giftCardDiscount ?? 0;
      const baseShip = (province?.shippingFee ?? 28000) + (activeDelivery === "fast" ? 15000 : 0);

      const createdOrders: Order[] = activeGroups.map((group, idx) => {
        const shop = getShopById(group.shopId);
        const isLast = idx === activeGroups.length - 1;
        const share = totalSelectedMerch > 0 ? group.selectedSubtotal / totalSelectedMerch : 1;

        const groupShip = idx === 0 ? baseShip : 12000;
        const groupPlatform = isLast
          ? remainingPlatform
          : Math.min(remainingPlatform, Math.round(cartSummary.platformVoucherDiscount * share));
        remainingPlatform -= groupPlatform;

        const groupFreeship = Math.min(groupShip, remainingFreeship);
        remainingFreeship -= groupFreeship;

        const groupCoin = isLast
          ? remainingCoins
          : Math.min(remainingCoins, Math.round(cartSummary.coinDiscount * share));
        remainingCoins -= groupCoin;

        const groupGiftCard = isLast
          ? remainingGiftCard
          : Math.min(remainingGiftCard, Math.round((cartSummary.giftCardDiscount ?? 0) * share));
        remainingGiftCard -= groupGiftCard;

        const selectedItems = group.items.filter((it) => it.selected && it.quantity > 0);
        const totals = computeOrderTotals({
          items: selectedItems.map((it) => ({
            skuCode: it.skuCode,
            productId: it.productId,
            productSlug: it.productSlug,
            productName: it.productName,
            colorName: it.colorName,
            size: it.size,
            image: it.image,
            unitPrice: it.unitPrice,
            quantity: it.quantity,
          })),
          shippingFee: groupShip,
          shopVoucherDiscount: group.shopVoucherDiscount,
          platformVoucherDiscount: groupPlatform,
          freeshipDiscount: groupFreeship,
          coinDiscount: groupCoin,
          tierDiscount: group.tierDiscount ?? 0,
          giftCardDiscount: groupGiftCard,
        });

        const seq = Math.floor(200 + Math.random() * 700);
        const code = `NM-261120-${seq + idx}`;
        const isPendingPay = Boolean(input.simulatePaymentPending && input.paymentMethod !== "cod");
        const paidNow = input.paymentMethod !== "cod" && !isPendingPay;

        const vatInvoice: OrderVatInvoice | undefined = input.vatInfo
          ? {
              requested: true,
              companyName: input.vatInfo.companyName.trim(),
              taxCode: input.vatInfo.taxCode.trim(),
              email: input.vatInfo.email.trim(),
              companyAddress: input.vatInfo.companyAddress?.trim() || undefined,
              status: "cho_duyet_nhap_24h",
              requestedAt: nowIso,
            }
          : undefined;

        return {
          id: `ord-${Date.now()}-${idx}`,
          orderCode: code,
          parentOrderCode: parentCode,
          shopId: group.shopId,
          shopName: shop?.name ?? "Gian hàng NexusMart",
          buyerName: resolvedAddr.recipientName,
          buyerPhone: resolvedAddr.phone,
          buyerEmail: resolvedAddr.email,
          isGuestOrder: resolvedAddr.isGuest,
          shippingAddress: `${resolvedAddr.streetDetail}, ${resolvedAddr.wardName}, ${resolvedAddr.provinceName}`,
          provinceCode: resolvedAddr.provinceCode,
          status: "cho_xac_nhan",
          paymentStatus: isPendingPay
            ? "cho_thanh_toan"
            : paidNow
              ? "da_thanh_toan"
              : "cod_cho_thu",
          paymentMethod: input.paymentMethod,
          paymentDueAt: isPendingPay
            ? new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString()
            : undefined,
          isPaid: paidNow,
          deliveryMethod: activeDelivery,
          carrier: activeDelivery === "fast" ? "NexusFast" : "SPX",
          coInspectionEnabled: true,
          createdAt: nowIso,
          updatedAt: nowIso,
          note: input.note?.trim() || undefined,
          giftWrap: Boolean(input.giftWrap),
          giftCardMessage: input.giftCardMessage?.trim() || undefined,
          vatInvoice,
          notificationChannels: ["Email xác nhận", "Zalo ZNS"],
          ...totals,
          timeline: [
            {
              timestamp: nowIso,
              status: "cho_xac_nhan",
              title: isPendingPay ? "Giữ đơn chờ thanh toán lại" : "Đặt hàng thành công",
              note: isPendingPay
                ? "Đơn hàng giữ sẵn sản phẩm đúng màu, size trong 2 giờ để bạn hoàn tất thanh toán hoặc đổi phương thức."
                : paidNow
                  ? "Đã ghi nhận thanh toán và giữ sẵn sản phẩm đúng màu, size."
                  : "Đơn hàng COD đã tạo (hỗ trợ đồng kiểm), chờ gian hàng xác nhận.",
            },
          ],
        };
      });

      setOrders((prev) => [...createdOrders, ...prev]);
      setCartItems((prev) => prev.filter((it) => !it.selected));

      if (cartSummary.coinDiscount > 0) {
        setUser((prev) => ({
          ...prev,
          coins: Math.max(0, prev.coins - cartSummary.coinDiscount),
        }));
      }
      if ((cartSummary.giftCardDiscount ?? 0) > 0 && membership.appliedGiftCardCode) {
        const usedAmount = cartSummary.giftCardDiscount ?? 0;
        const appliedCode = membership.appliedGiftCardCode;
        setMembership((prev) => ({
          ...prev,
          appliedGiftCardCode: null,
          giftCards: prev.giftCards.map((gc) => {
            if (gc.code.toUpperCase() !== appliedCode.toUpperCase()) return gc;
            const nextRemaining = Math.max(0, gc.remainingVnd - usedAmount);
            return {
              ...gc,
              remainingVnd: nextRemaining,
              status:
                nextRemaining === 0
                  ? "da_dung_het"
                  : nextRemaining < gc.amountVnd
                    ? "da_dung_mot_phan"
                    : "kha_dung",
            };
          }),
        }));
      }
      if ((cartSummary.estimatedCashbackCoins ?? 0) > 0 && isAuthenticated) {
        const earned = cartSummary.estimatedCashbackCoins ?? 0;
        setMembership((prev) => ({
          ...prev,
          pendingCoins: prev.pendingCoins + earned,
          birthdayGiftClaimed: birthdayGiftEligible ? true : prev.birthdayGiftClaimed,
          xuHistory: [
            {
              id: `xu-${Date.now()}`,
              date: nowIso.slice(0, 10),
              title: `Xu chờ duyệt từ đơn ${createdOrders[0]?.orderCode ?? ""}`,
              orderCode: createdOrders[0]?.orderCode,
              deltaCoins: earned,
              status: "cho_duyet",
            },
            ...prev.xuHistory,
          ],
        }));
      }

      setUseCoins(false);
      showToast(`Đã tạo ${createdOrders.length} đơn hàng thành công.`);
      return createdOrders;
    },
    [
      addresses,
      birthdayGiftEligible,
      cartSummary,
      deliveryMethod,
      isAuthenticated,
      membership.appliedGiftCardCode,
      selectedAddress,
      showToast,
      user.email,
    ]
  );

  const cancelOrder = useCallback(
    (orderId: string, reason: string) => {
      const nowIso = new Date().toISOString();
      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                status: "da_huy",
                paymentStatus: o.isPaid ? "da_hoan_tien" : o.paymentStatus,
                cancelReason: reason,
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: "da_huy",
                    title: "Đơn hàng đã hủy",
                    note: o.isPaid
                      ? `${reason} (Hoàn tiền tự động trong 24 giờ qua kênh thanh toán gốc).`
                      : reason,
                  },
                ],
              }
            : o
        )
      );
      showToast("Đã hủy đơn hàng và giải phóng giữ tồn kho.");
    },
    [showToast]
  );

  const requestReturnOrder = useCallback(
    (orderId: string, reason: string) => {
      const nowIso = new Date().toISOString();
      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                status: "yeu_cau_tra_hang",
                returnReason: reason,
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: "yeu_cau_tra_hang",
                    title: "Gửi yêu cầu đổi trả 45 ngày",
                    note: reason,
                  },
                ],
              }
            : o
        )
      );
      showToast("Đã gửi yêu cầu đổi trả 45 ngày đến gian hàng.");
    },
    [showToast]
  );

  const updateOrderStatus = useCallback(
    (orderId: string, nextStatus: OrderStatus, note?: string) => {
      const nowIso = new Date().toISOString();
      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                status: nextStatus,
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: nextStatus,
                    title: "Cập nhật trạng thái đơn hàng",
                    note: note ?? "Trạng thái đã được cập nhật.",
                  },
                ],
              }
            : o
        )
      );
    },
    []
  );

  const lookupOrder = useCallback(
    (code: string, phone: string): Order | undefined => {
      const cleanCode = code.trim().toUpperCase();
      const cleanPhone = digitsOnly(phone);
      if (!cleanCode || cleanPhone.length < 8) return undefined;
      return orders.find(
        (o) =>
          (o.orderCode.toUpperCase() === cleanCode ||
            o.parentOrderCode?.toUpperCase() === cleanCode) &&
          digitsOnly(o.buyerPhone).endsWith(cleanPhone.slice(-8))
      );
    },
    [orders]
  );

  const retryOrderPayment = useCallback(
    (orderId: string, method: PaymentMethod): boolean => {
      const exists = orders.some((o) => o.id === orderId);
      if (!exists) return false;
      const nowIso = new Date().toISOString();
      const paidNow = method !== "cod";
      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                paymentMethod: method,
                isPaid: paidNow,
                paymentStatus: paidNow ? "da_thanh_toan" : "cod_cho_thu",
                paymentDueAt: undefined,
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: o.status,
                    title: paidNow ? "Thanh toán lại thành công" : "Chuyển sang thanh toán COD",
                    note: paidNow
                      ? `Đã thanh toán lại qua ${method === "vietqr" ? "VietQR" : "Ví điện tử NexusPay"} và chuyển đơn tới xưởng.`
                      : "Đã chuyển sang hình thức thu hộ COD (hỗ trợ đồng kiểm).",
                  },
                ],
              }
            : o
        )
      );
      showToast(
        paidNow
          ? "Thanh toán lại thành công. Đơn hàng đang chờ xưởng xác nhận."
          : "Đã chuyển đơn hàng sang thanh toán khi nhận hàng (COD)."
      );
      return true;
    },
    [orders, showToast]
  );

  const requestEditOrderAddress = useCallback(
    (orderId: string, address: string, reason = "Cập nhật địa chỉ nhận hàng"): boolean => {
      const cleanAddr = address.trim();
      if (!cleanAddr) return false;
      const nowIso = new Date().toISOString();
      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                shippingAddress: o.status === "cho_xac_nhan" ? cleanAddr : o.shippingAddress,
                addressEditRequest: {
                  requestedAddress: cleanAddr,
                  reason,
                  status: o.status === "cho_xac_nhan" ? "da_cap_nhat" : "cho_shop_duyet",
                  requestedAt: nowIso,
                },
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: o.status,
                    title:
                      o.status === "cho_xac_nhan"
                        ? "Đã cập nhật địa chỉ nhận hàng"
                        : "Gửi yêu cầu đổi địa chỉ tới gian hàng",
                    note: `Địa chỉ mới: ${cleanAddr}`,
                  },
                ],
              }
            : o
        )
      );
      showToast("Đã ghi nhận thay đổi địa chỉ giao hàng.");
      return true;
    },
    [showToast]
  );

  const requestExchangeOrder = useCallback(
    (orderId: string, payload: ExchangeOrderPayload): boolean => {
      const nowIso = new Date().toISOString();
      const target = orders.find((o) => o.id === orderId);
      if (!target) return false;

      const scheduleNote =
        payload.type === "doi_size_2_chieu"
          ? "Bưu tá đổi size 2 chiều miễn phí tại nhà trong 45 ngày."
          : payload.type === "bu_le_tat"
            ? "Xưởng xác nhận gửi kèm 1 chiếc tất đồng màu miễn phí ở đơn kế tiếp."
            : "Hoàn chuyển khoản định kỳ vào Thứ 2 và Thứ 5 hằng tuần sau khi nhận lại hàng.";

      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                status: payload.type === "bu_le_tat" ? o.status : "yeu_cau_tra_hang",
                returnReason: payload.reason,
                exchangeRequest: {
                  type: payload.type,
                  reason: payload.reason,
                  fromSize: payload.fromSize,
                  toSize: payload.toSize,
                  pickupAddress: payload.pickupAddress ?? o.shippingAddress,
                  refundScheduleNote: scheduleNote,
                  status: "da_tiep_nhan",
                  requestedAt: nowIso,
                },
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: payload.type === "bu_le_tat" ? o.status : "yeu_cau_tra_hang",
                    title:
                      payload.type === "doi_size_2_chieu"
                        ? `Yêu cầu đổi size 2 chiều (${payload.fromSize ?? ""} -> ${payload.toSize ?? ""})`
                        : payload.type === "bu_le_tat"
                          ? "Yêu cầu bù lẻ chiếc tất thất lạc"
                          : "Yêu cầu trả hàng hoàn tiền (45 ngày)",
                    note: `${payload.reason}. ${scheduleNote}`,
                  },
                ],
              }
            : o
        )
      );
      showToast(
        payload.type === "doi_size_2_chieu"
          ? "Đã tạo yêu cầu bưu tá đổi size 2 chiều tận nơi."
          : payload.type === "bu_le_tat"
            ? "Đã gửi yêu cầu bù lẻ chiếc tất thất lạc tới xưởng."
            : "Đã gửi yêu cầu trả hàng hoàn tiền 45 ngày."
      );
      return true;
    },
    [orders, showToast]
  );

  const requestVatInvoice = useCallback(
    (orderId: string, payload: VatInvoicePayload): boolean => {
      const nowIso = new Date().toISOString();
      if (!payload.companyName.trim() || !payload.taxCode.trim() || !payload.email.trim()) {
        return false;
      }
      setOrders((prev) =>
        prev.map((o) =>
          o.id === orderId
            ? {
                ...o,
                vatInvoice: {
                  requested: true,
                  companyName: payload.companyName.trim(),
                  taxCode: payload.taxCode.trim(),
                  email: payload.email.trim(),
                  companyAddress: payload.companyAddress?.trim() || undefined,
                  status: "cho_duyet_nhap_24h",
                  requestedAt: nowIso,
                },
                updatedAt: nowIso,
                timeline: [
                  ...o.timeline,
                  {
                    timestamp: nowIso,
                    status: o.status,
                    title: "Yêu cầu xuất hóa đơn VAT điện tử",
                    note: `MST ${payload.taxCode.trim()} · ${payload.companyName.trim()}. Bản nháp sẽ gửi qua ${payload.email.trim()} trong 24 giờ.`,
                  },
                ],
              }
            : o
        )
      );
      showToast("Đã gửi yêu cầu xuất hóa đơn VAT (duyệt bản nháp trong 24h).");
      return true;
    },
    [showToast]
  );

  // Body profile, wishlist, recently viewed, back-in-stock
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
    (productId: string, skuCode?: string): boolean => {
      return wishlist.some(
        (w) => w.productId === productId && (skuCode ? w.skuCode === skuCode : true)
      );
    },
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

  const subscribeBackInStock = useCallback(
    (input: Omit<BackInStockAlert, "id" | "createdAt">): BackInStockAlert => {
      const created: BackInStockAlert = {
        ...input,
        id: `bis-${Date.now()}`,
        createdAt: new Date().toISOString(),
      };
      setBackInStockAlerts((prev) => [created, ...prev.filter((a) => a.skuCode !== input.skuCode)]);
      showToast(`Đã đăng ký báo khi có hàng cho mã ${input.skuCode}.`);
      return created;
    },
    [showToast]
  );

  const hasBackInStockAlert = useCallback(
    (skuCode: string): boolean => backInStockAlerts.some((a) => a.skuCode === skuCode),
    [backInStockAlerts]
  );

  // Reviews, Q&A & Support tickets
  const submitReview = useCallback(
    (input: SubmitReviewInput): ProductReview => {
      const created: ProductReview = {
        id: `rev-${Date.now()}`,
        productId: input.productId,
        authorName: input.authorName?.trim() || user.fullName,
        rating: Math.max(1, Math.min(5, Math.round(input.rating))),
        fitFeedback: input.fitFeedback,
        buyerHeightCm: input.buyerHeightCm,
        buyerWeightKg: input.buyerWeightKg,
        purchasedSize: input.purchasedSize,
        colorName: input.colorName,
        comment: input.comment.trim(),
        createdAt: new Date().toISOString(),
        images: input.images ?? [],
        verifiedPurchase: true,
        isSampleData: false,
      };
      setReviews((prev) => [created, ...prev]);
      showToast("Đã gửi đánh giá sản phẩm thành công.");
      return created;
    },
    [showToast, user.fullName]
  );

  const submitProductQA = useCallback(
    (input: SubmitQAInput): ProductQA => {
      const created: ProductQA = {
        id: `qa-${Date.now()}`,
        productId: input.productId,
        askerName: input.askerName?.trim() || user.fullName,
        question: input.question.trim(),
        askedAt: new Date().toISOString(),
        isSampleData: false,
      };
      setProductQAs((prev) => [created, ...prev]);
      showToast("Đã gửi câu hỏi công khai tới gian hàng.");
      return created;
    },
    [showToast, user.fullName]
  );

  const submitSupportTicket = useCallback(
    (input: SubmitSupportTicketInput): SupportTicket => {
      const slaLabel =
        input.kind === "goi_lai"
          ? `Gọi lại trong vòng ${COMPLAINT_SLA.callbackMinutes} phút (${COMPLAINT_SLA.hotlineHours})`
          : input.isVulnerablePriority
            ? "Ưu tiên nhóm yếu thế · Phản hồi trong 4 giờ"
            : `Xác nhận trong ${COMPLAINT_SLA.ackHours}h · Xử lý ${COMPLAINT_SLA.resolveDaysLabel}`;
      const created: SupportTicket = {
        id: `tk-${Date.now()}`,
        kind: input.kind,
        orderCode: input.orderCode?.trim() || undefined,
        fullName: input.fullName.trim(),
        phone: input.phone.trim(),
        email: input.email?.trim() || undefined,
        subject: input.subject.trim(),
        detail: input.detail.trim(),
        isVulnerablePriority: Boolean(input.isVulnerablePriority),
        slaLabel,
        status: "da_tiep_nhan",
        createdAt: new Date().toISOString(),
      };
      setSupportTickets((prev) => [created, ...prev]);
      showToast(
        input.kind === "goi_lai"
          ? "Đã nhận yêu cầu gọi lại. Tổng đài sẽ liên hệ trong 15 phút."
          : "Đã tiếp nhận phiếu hỗ trợ. Xác nhận phản hồi trong 24 giờ."
      );
      return created;
    },
    [showToast]
  );

  // Cookie prefs
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

  // Membership actions
  const updateBirthday = useCallback(
    (birthday: string) => {
      setMembership((prev) => ({ ...prev, birthday }));
      setUser((prev) => ({ ...prev, birthday }));
      showToast("Đã lưu ngày sinh để nhận quà sinh nhật Hội viên Nexus.");
    },
    [showToast]
  );

  const claimBirthdayGift = useCallback(() => {
    setMembership((prev) => ({ ...prev, birthdayGiftClaimed: true }));
    setPlatformVoucherCode("SINHNHAT50");
    showToast("Đã kích hoạt quà sinh nhật và mã SINHNHAT50 vào giỏ hàng.");
  }, [showToast]);

  const inviteFriendMock = useCallback(
    (friendName: string) => {
      const clean = friendName.trim();
      if (!clean) return;
      setMembership((prev) => ({
        ...prev,
        referrals: [
          {
            id: `ref-${Date.now()}`,
            friendName: clean,
            invitedAt: new Date().toISOString().slice(0, 10),
            rewardCoins: 0,
            status: "cho_dat_don",
          },
          ...prev.referrals,
        ],
      }));
      showToast(`Đã tạo lời mời giới thiệu cho ${clean}.`);
    },
    [showToast]
  );

  const purchaseGiftCard = useCallback(
    (input: {
      amountVnd: number;
      customCode?: string;
      recipientEmail?: string;
      message?: string;
    }): GiftCardItem => {
      const amount = Math.max(100000, Math.trunc(input.amountVnd));
      const cleanCustom = (input.customCode ?? "")
        .toUpperCase()
        .replace(/[^A-Z0-9-]/g, "")
        .slice(0, GIFT_CARD_POLICY.customCodeMaxLen);
      const code =
        cleanCustom.length >= GIFT_CARD_POLICY.customCodeMinLen
          ? `NEXUS-${cleanCustom}`
          : `NEXUS-GC${Math.floor(1000 + Math.random() * 8999)}`;
      const issued = new Date();
      const expires = new Date(
        issued.getTime() + GIFT_CARD_POLICY.validityDays * 24 * 60 * 60 * 1000
      );
      const card: GiftCardItem = {
        id: `gc-${Date.now()}`,
        code,
        amountVnd: amount,
        remainingVnd: amount,
        recipientEmail: input.recipientEmail?.trim() || undefined,
        message: input.message?.trim() || undefined,
        issuedAt: issued.toISOString(),
        expiresAt: expires.toISOString(),
        status: "kha_dung",
      };
      setMembership((prev) => ({
        ...prev,
        giftCards: [card, ...prev.giftCards],
      }));
      showToast(`Đã phát hành thẻ quà tặng ${code} trị giá ${amount.toLocaleString("vi-VN")}₫.`);
      return card;
    },
    [showToast]
  );

  const applyGiftCardCode = useCallback(
    (code: string | null): { ok: boolean; message: string } => {
      if (!code) {
        setMembership((prev) => ({ ...prev, appliedGiftCardCode: null }));
        return { ok: true, message: "Đã gỡ mã thẻ quà tặng." };
      }
      const clean = code.trim().toUpperCase();
      const found = membership.giftCards.find((gc) => gc.code.toUpperCase() === clean);
      if (!found) {
        return { ok: false, message: `Không tìm thấy thẻ quà tặng ${clean}.` };
      }
      if (found.remainingVnd <= 0) {
        return { ok: false, message: `Thẻ quà tặng ${clean} đã sử dụng hết số dư.` };
      }
      setMembership((prev) => ({ ...prev, appliedGiftCardCode: found.code }));
      showToast(`Đã áp dụng thẻ quà tặng ${found.code} vào giỏ hàng.`);
      return { ok: true, message: `Áp dụng thành công thẻ ${found.code}.` };
    },
    [membership.giftCards, showToast]
  );

  const verifyEduEmail = useCallback(
    (email: string): { ok: boolean; message: string } => {
      const clean = email.trim().toLowerCase();
      const validDomain = EDU_DISCOUNT_CONFIG.allowedDomains.some((suffix) =>
        clean.endsWith(suffix)
      );
      if (!clean.includes("@") || !validDomain) {
        return {
          ok: false,
          message: "Vui lòng nhập email sinh viên có đuôi .edu hoặc .edu.vn hợp lệ.",
        };
      }
      setMembership((prev) => ({
        ...prev,
        eduEmail: clean,
        eduVerified: true,
      }));
      setSavedVoucherCodes((prev) => (prev.includes("EDU15") ? prev : ["EDU15", ...prev]));
      showToast("Xác thực email sinh viên thành công. Đã mở khóa mã EDU15 (giảm 15%).");
      return { ok: true, message: "Xác thực thành công và đã thêm mã EDU15 vào ví voucher." };
    },
    [showToast]
  );

  const openChat = useCallback((shopId?: string) => {
    setAiOpen(false);
    if (shopId) setChatShopId(shopId);
    setChatOpen(true);
    setChatThreads((prev) =>
      prev.map((t) => (shopId && t.shopId === shopId ? { ...t, unreadByBuyer: 0 } : t))
    );
  }, []);

  const closeChat = useCallback(() => setChatOpen(false), []);

  const openAi = useCallback(() => {
    setChatOpen(false);
    setAiOpen(true);
  }, []);

  const closeAi = useCallback(() => setAiOpen(false), []);

  const sendChatMessage = useCallback(
    (threadId: string, sender: "buyer" | "seller", text: string) => {
      const clean = text.trim();
      if (!clean) return;
      const nowIso = new Date().toISOString();
      setChatThreads((prev) =>
        prev.map((thread) => {
          if (thread.id !== threadId) return thread;
          const senderName = sender === "buyer" ? user.fullName : thread.shopName;
          return {
            ...thread,
            unreadByBuyer: sender === "seller" ? thread.unreadByBuyer + 1 : 0,
            unreadBySeller: sender === "buyer" ? thread.unreadBySeller + 1 : 0,
            messages: [
              ...thread.messages,
              {
                id: `msg-${Date.now()}`,
                sender,
                senderName,
                text: clean,
                timestamp: nowIso,
              },
            ],
          };
        })
      );
    },
    [user.fullName]
  );

  const loginMock = useCallback(
    (email: string, fullName?: string) => {
      setUser((prev) => ({
        ...prev,
        email: email.trim() || prev.email,
        fullName: fullName?.trim() || prev.fullName,
      }));
      setIsAuthenticated(true);
      showToast("Đăng nhập tài khoản NexusMart thành công.");
    },
    [showToast]
  );

  const registerMock = useCallback(
    (fullName: string, email: string, phone: string) => {
      setUser({
        id: `usr-${Date.now()}`,
        fullName: fullName.trim() || DEMO_USER.fullName,
        email: email.trim() || DEMO_USER.email,
        phone: phone.trim() || DEMO_USER.phone,
        memberSince: "2026-11-20",
        coins: 15000,
      });
      setIsAuthenticated(true);
      showToast("Tạo tài khoản thành công, tặng 15.000 xu thành viên mới.");
    },
    [showToast]
  );

  const logoutMock = useCallback(() => {
    setIsAuthenticated(false);
    showToast("Đã đăng xuất khỏi phiên hiện tại.");
  }, [showToast]);

  const updateShopApprovalStatus = useCallback(
    (id: string, status: ShopApprovalApplication["status"], note?: string) => {
      setShopApprovals((prev) =>
        prev.map((app) =>
          app.id === id
            ? {
                ...app,
                status,
                slaHoursLeft: status === "da_duyet" || status === "tu_choi" ? 0 : app.slaHoursLeft,
                note: note ?? app.note,
              }
            : app
        )
      );
      showToast("Đã cập nhật trạng thái hồ sơ gian hàng.");
    },
    [showToast]
  );

  const updateDisputeStatus = useCallback(
    (id: string, status: AdminDispute["status"], note?: string) => {
      setDisputes((prev) =>
        prev.map((d) => (d.id === id ? { ...d, status, evidenceNote: note ?? d.evidenceNote } : d))
      );
      showToast("Đã cập nhật tiến độ xử lý khiếu nại.");
    },
    [showToast]
  );

  const resolveReconciliationBatch = useCallback(
    (id: string, note?: string) => {
      setReconciliationRows((prev) =>
        prev.map((row) =>
          row.id === id
            ? {
                ...row,
                discrepancy: 0,
                status: "khop",
                note: note ?? "Đã đối chiếu thủ công và xác nhận khớp số liệu sao kê.",
              }
            : row
        )
      );
      showToast("Đã xác nhận khớp lô đối soát thanh toán.");
    },
    [showToast]
  );

  const updatePlatformConfig = useCallback(
    (next: PlatformConfigState) => {
      setPlatformConfig(next);
      showToast("Đã lưu cấu hình vận hành sàn.");
    },
    [showToast]
  );

  const value: StoreContextValue = {
    user,
    isAuthenticated,
    loginMock,
    registerMock,
    logoutMock,
    addresses,
    selectedAddressId,
    selectedAddress,
    selectAddress: setSelectedAddressId,
    addAddress,
    setDefaultAddress,
    deleteAddress,
    cartItems,
    cartCount,
    cartSummary,
    selectedShopVouchers,
    platformVoucherCode,
    freeshipVoucherCode,
    savedVoucherCodes,
    saveVoucherCode,
    useCoins,
    deliveryMethod,
    setDeliveryMethod,
    addToCart,
    updateCartQuantity,
    removeFromCart,
    toggleCartItemSelected,
    toggleShopSelected,
    toggleSelectAllCart,
    setShopVoucher,
    setPlatformVoucher: setPlatformVoucherCode,
    setFreeshipVoucher: setFreeshipVoucherCode,
    setUseCoins,
    clearCart,
    guestCheckout,
    setGuestCheckout,
    orders,
    placeOrder,
    cancelOrder,
    requestReturnOrder,
    updateOrderStatus,
    lookupOrder,
    retryOrderPayment,
    requestEditOrderAddress,
    requestExchangeOrder,
    requestVatInvoice,
    bodyProfile,
    updateBodyProfile,
    wishlist,
    toggleWishlist,
    isWishlisted,
    recentlyViewed,
    recordProductView,
    clearRecentlyViewed,
    backInStockAlerts,
    subscribeBackInStock,
    hasBackInStockAlert,
    reviews,
    submitReview,
    productQAs,
    submitProductQA,
    supportTickets,
    submitSupportTicket,
    cookiePrefs,
    updateCookiePrefs,
    cookieModalOpen,
    openCookieModal,
    closeCookieModal,
    membership,
    membershipTier,
    updateBirthday,
    claimBirthdayGift,
    inviteFriendMock,
    purchaseGiftCard,
    applyGiftCardCode,
    verifyEduEmail,
    chatOpen,
    chatShopId,
    openChat,
    closeChat,
    aiOpen,
    openAi,
    closeAi,
    chatThreads,
    sendChatMessage,
    activeSellerShopId,
    setActiveSellerShopId,
    shopApprovals,
    updateShopApprovalStatus,
    disputes,
    updateDisputeStatus,
    reconciliationRows,
    resolveReconciliationBatch,
    platformConfig,
    updatePlatformConfig,
    toastMessage,
    showToast,
    dismissToast,
  };

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}

export function useStore(): StoreContextValue {
  const ctx = useContext(StoreContext);
  if (!ctx) {
    throw new Error("useStore phải được gọi bên trong StoreProvider.");
  }
  return ctx;
}
