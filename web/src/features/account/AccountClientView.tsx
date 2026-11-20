"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  ORDER_STATUS_META,
  PRODUCTS,
  REFERRAL_CONFIG,
  SAMPLE_DATA_NOTICE,
  SHOPS,
  getNextTierInfo,
} from "@/data";
import type { Product, Shop, StatusTone, Voucher } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { ApiError, api } from "@/lib/api";
import { getProvinceByCode } from "@/lib/provinces";
import { BODY_SHAPE_LABELS, recommendSize, type BodyShape, type FitPreference } from "@/lib/sizing";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, Price, ProductCard, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/tai-khoan/page.module.css";
import { AccountAddressForm } from "./AccountAddressForm";
import {
  toStorefrontProvinceCode,
  type AccountProfile,
  type AddressView,
  type GwProvince,
  type GwWard,
} from "./accountTypes";
import type { OrderView } from "./orderView";

type AccountTabKey = "tong-quan" | "dia-chi" | "so-do" | "yeu-thich" | "voucher" | "gioi-thieu";

const ACCOUNT_TABS: Array<{ key: AccountTabKey; label: string }> = [
  { key: "tong-quan", label: "Tổng quan & Hồ sơ" },
  { key: "dia-chi", label: "Địa chỉ nhận hàng" },
  { key: "so-do", label: "Hồ sơ số đo" },
  { key: "yeu-thich", label: "Sản phẩm yêu thích" },
  { key: "voucher", label: "Ví voucher & Thẻ quà tặng" },
  { key: "gioi-thieu", label: "Giới thiệu bạn bè" },
];

const VALID_TABS = new Set<AccountTabKey>(ACCOUNT_TABS.map((t) => t.key));

const FIT_PREF_LABELS: Record<FitPreference, string> = {
  om: "Ôm gọn",
  vua: "Vừa vặn chuẩn",
  rong: "Thoải mái / Rộng nhẹ",
};

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

export interface AccountClientViewProps {
  initialProfile: AccountProfile;
  initialAddresses: AddressView[];
  initialOrders: OrderView[];
  totalOrders: number;
  initialVouchers: Voucher[];
  provinces: GwProvince[];
  initialWards: GwWard[];
  initialProvinceCode: string;
  shops: Shop[];
  catalogProducts: Product[];
  suggestedProducts: Product[];
  initialError?: string | null;
}

function AccountClientInner({
  initialProfile,
  initialAddresses,
  initialOrders,
  totalOrders,
  initialVouchers,
  provinces,
  initialWards,
  initialProvinceCode,
  shops,
  catalogProducts,
  suggestedProducts,
  initialError = null,
}: AccountClientViewProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const viewState = usePageStateParam();
  const { setUser, logout } = useSession();
  const {
    addToCart,
    saveVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    setShopVoucher,
    bodyProfile,
    updateBodyProfile,
    wishlist,
    toggleWishlist,
    recentlyViewed,
    clearRecentlyViewed,
    openChat,
    openAi,
    showToast,
  } = useStore();
  const {
    membership,
    membershipTier,
    updateBirthday,
    claimBirthdayGift,
    inviteFriendMock,
    applyGiftCardCode,
  } = useMockStore();

  const tabParam = searchParams.get("tab") as AccountTabKey | null;
  const [activeTab, setActiveTab] = useState<AccountTabKey>(
    tabParam && VALID_TABS.has(tabParam) ? tabParam : "tong-quan"
  );

  useEffect(() => {
    if (tabParam && VALID_TABS.has(tabParam)) {
      setActiveTab(tabParam);
    }
  }, [tabParam]);

  const [profile, setProfile] = useState<AccountProfile>(initialProfile);
  const [editingProfile, setEditingProfile] = useState(false);
  const [fullNameDraft, setFullNameDraft] = useState(initialProfile.fullName);
  const [phoneDraft, setPhoneDraft] = useState(initialProfile.phone);
  const [savingProfile, setSavingProfile] = useState(false);
  const [profileError, setProfileError] = useState<string | null>(null);

  const [addresses, setAddresses] = useState<AddressView[]>(initialAddresses);
  const [showAddAddress, setShowAddAddress] = useState(false);
  const [busyAddressId, setBusyAddressId] = useState<string | null>(null);
  const [addressError, setAddressError] = useState<string | null>(null);

  const [vouchers, setVouchers] = useState<Voucher[]>(initialVouchers);
  const [savingVoucherCode, setSavingVoucherCode] = useState<string | null>(null);

  // Body profile draft state (viewer-only)
  const [heightCm, setHeightCm] = useState(bodyProfile.heightCm);
  const [weightKg, setWeightKg] = useState(bodyProfile.weightKg);
  const [shape, setShape] = useState<BodyShape>(bodyProfile.shape);
  const [preferredFit, setPreferredFit] = useState<FitPreference>(
    bodyProfile.preferredFit ?? "vua"
  );

  // Birthday & referral draft state (mock-only sub-tabs)
  const [birthdayDraft, setBirthdayDraft] = useState(membership.birthday || "1996-11-24");
  const [friendName, setFriendName] = useState("");

  useRevealOnScroll(
    `${viewState ?? "live"}-${activeTab}-${addresses.length}-${wishlist.length}-${initialOrders.length}`
  );

  const activeShops = shops.length > 0 ? shops : SHOPS;
  const activeProducts = catalogProducts.length > 0 ? catalogProducts : PRODUCTS;
  const shelfProducts = suggestedProducts.length > 0 ? suggestedProducts : PRODUCTS.slice(4, 8);

  const productById = useMemo(() => {
    const map = new Map<string, Product>();
    for (const p of activeProducts) {
      map.set(p.id, p);
      if (p.backendId) map.set(p.backendId, p);
    }
    return map;
  }, [activeProducts]);

  const shopById = useMemo(() => {
    const map = new Map<string, Shop>();
    for (const s of activeShops) {
      map.set(s.id, s);
      if (s.backendId) map.set(s.backendId, s);
    }
    return map;
  }, [activeShops]);

  const nextTier = useMemo(() => getNextTierInfo(membership.spend4Q), [membership.spend4Q]);

  const inProgressOrders = useMemo(
    () =>
      initialOrders.filter(
        (o) =>
          o.status === "cho_xac_nhan" || o.status === "dang_chuan_bi" || o.status === "dang_giao"
      ).length,
    [initialOrders]
  );

  const recentOrders = useMemo(() => initialOrders.slice(0, 3), [initialOrders]);

  const wishlistedProducts = useMemo(
    () =>
      wishlist
        .map((entry) => {
          const prod = productById.get(entry.productId);
          return prod ? { entry, prod } : null;
        })
        .filter((x): x is NonNullable<typeof x> => x !== null),
    [wishlist, productById]
  );

  const recentlyViewedProducts = useMemo(
    () =>
      recentlyViewed
        .map((id) => productById.get(id))
        .filter((p): p is NonNullable<typeof p> => Boolean(p))
        .slice(0, 4),
    [recentlyViewed, productById]
  );

  const shopRecommendations = useMemo(
    () =>
      activeShops.map((shop) => ({
        shop,
        top: recommendSize({
          heightCm,
          weightKg,
          shape,
          preferredFit,
          garmentGroup: "top",
          shopId: shop.id,
        }),
        bottom: recommendSize({
          heightCm,
          weightKg,
          shape,
          preferredFit,
          garmentGroup: "bottom",
          shopId: shop.id,
        }),
      })),
    [activeShops, heightCm, weightKg, shape, preferredFit]
  );

  const savedVoucherCount = useMemo(() => vouchers.filter((v) => v.saved).length, [vouchers]);

  const handleSaveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fullNameDraft.trim();
    const cleanPhone = phoneDraft.replace(/\s+/g, "").trim();

    if (cleanName.length < 2) {
      setProfileError("Vui lòng nhập họ và tên đầy đủ (tối thiểu 2 ký tự).");
      return;
    }
    if (cleanPhone && !/^0\d{9,10}$/.test(cleanPhone)) {
      setProfileError("Số điện thoại Việt Nam cần có 10 hoặc 11 chữ số, bắt đầu bằng số 0.");
      return;
    }

    setProfileError(null);
    setSavingProfile(true);
    try {
      const res = await api<{ user: SessionUser }>("/api/auth/me", {
        method: "PATCH",
        body: { fullName: cleanName, phone: cleanPhone },
      });
      setProfile((prev) => ({
        ...prev,
        fullName: res.user.fullName || cleanName,
        phone: res.user.phone || cleanPhone,
      }));
      setUser(res.user);
      setEditingProfile(false);
      showToast("Đã cập nhật thông tin tài khoản.");
      router.refresh();
    } catch (err) {
      setProfileError(
        err instanceof ApiError
          ? err.message
          : "Chưa cập nhật được thông tin lúc này. Vui lòng thử lại."
      );
    } finally {
      setSavingProfile(false);
    }
  };

  const handleSetDefaultAddress = async (addr: AddressView) => {
    setAddressError(null);
    setBusyAddressId(addr.id);
    try {
      await api<{ address: { id: string } }>(`/api/auth/addresses/${encodeURIComponent(addr.id)}`, {
        method: "PUT",
        body: {
          recipientName: addr.recipientName,
          recipientPhone: addr.phone,
          provinceCode: addr.provinceCode,
          provinceName: addr.provinceName,
          wardCode: addr.wardCode,
          wardName: addr.wardName,
          streetAddress: addr.streetDetail,
          isDefault: true,
        },
      });
      setAddresses((prev) =>
        prev.map((item) => ({
          ...item,
          isDefault: item.id === addr.id,
        }))
      );
      showToast("Đã đặt làm địa chỉ nhận hàng mặc định.");
      router.refresh();
    } catch (err) {
      setAddressError(
        err instanceof ApiError
          ? err.message
          : "Chưa cập nhật được địa chỉ mặc định. Vui lòng thử lại."
      );
    } finally {
      setBusyAddressId(null);
    }
  };

  const handleDeleteAddress = async (addrId: string) => {
    setAddressError(null);
    setBusyAddressId(addrId);
    try {
      await api<{ ok: boolean }>(`/api/auth/addresses/${encodeURIComponent(addrId)}`, {
        method: "DELETE",
      });
      setAddresses((prev) => prev.filter((item) => item.id !== addrId));
      showToast("Đã xóa địa chỉ nhận hàng.");
      router.refresh();
    } catch (err) {
      setAddressError(
        err instanceof ApiError ? err.message : "Chưa xóa được địa chỉ lúc này. Vui lòng thử lại."
      );
    } finally {
      setBusyAddressId(null);
    }
  };

  const handleSaveVoucher = async (voucher: Voucher) => {
    setSavingVoucherCode(voucher.code);
    const res = await saveVoucher(voucher.id ?? voucher.code);
    setSavingVoucherCode(null);
    if (res.ok) {
      setVouchers((prev) => prev.map((v) => (v.code === voucher.code ? { ...v, saved: true } : v)));
    }
  };

  const handleApplyVoucher = (voucher: Voucher) => {
    const voucherKey = voucher.id ?? voucher.code;
    if (voucher.scope === "platform") {
      setPlatformVoucher(voucherKey);
    } else if (voucher.scope === "freeship") {
      setFreeshipVoucher(voucherKey);
    } else if (voucher.scope === "shop" && voucher.shopId) {
      const shop = shopById.get(voucher.shopId);
      setShopVoucher(shop?.backendId ?? voucher.shopId, voucherKey);
    }
    showToast(`Đã chọn mã ${voucher.code} cho giỏ hàng hiện tại.`);
    router.push("/gio-hang");
  };

  const handleAddWishlistToCart = async (prod: Product) => {
    const defaultVariant =
      prod.skus.find((s) => s.size === "L" && s.stock > 0 && s.skuId) ??
      prod.skus.find((s) => s.stock > 0 && s.skuId);
    if (!defaultVariant?.skuId) {
      router.push(`/san-pham/${prod.slug}`);
      return;
    }
    const res = await addToCart({
      skuId: defaultVariant.skuId,
      quantity: 1,
      productName: prod.name,
      size: defaultVariant.size,
    });
    if (res.unauthenticated) {
      router.push("/dang-nhap?next=%2Ftai-khoan%3Ftab%3Dyeu-thich");
    }
  };

  const handleSaveBodyProfile = (e: React.FormEvent) => {
    e.preventDefault();
    const safeH = Math.max(145, Math.min(205, Math.round(heightCm)));
    const safeW = Math.max(38, Math.min(130, Math.round(weightKg)));
    setHeightCm(safeH);
    setWeightKg(safeW);
    updateBodyProfile({ heightCm: safeH, weightKg: safeW, shape, preferredFit });
  };

  const handleCopyReferral = async () => {
    const inviteUrl = `https://nexusmart.vn/dang-ky?ref=${membership.referralCode}`;
    try {
      await navigator.clipboard.writeText(inviteUrl);
      showToast(`Đã sao chép liên kết giới thiệu: ${membership.referralCode}`);
    } catch {
      showToast(`Mã mời của bạn: ${membership.referralCode}`);
    }
  };

  const handleInviteSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = friendName.trim() || "Nguyễn Hải Đăng";
    inviteFriendMock(clean);
    setFriendName("");
  };

  const renderSuggestedShelf = (title: string, subtitle?: string) => (
    <section className={`${shared.section} ${shared.sectionGap}`} data-reveal="">
      <div className={shared.sectionHead}>
        <h2 className={shared.sectionTitle}>{title}</h2>
        {subtitle && <p className={styles.shelfSub}>{subtitle}</p>}
      </div>
      <div className={styles.productGrid}>
        {shelfProducts.map((product) => (
          <div key={product.id} data-reveal="">
            <ProductCard product={product} />
          </div>
        ))}
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Đang tải thông tin tài khoản, địa chỉ nhận hàng và đơn hàng gần đây của bạn.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Hồ sơ thành viên</h2>
          <StateView kind="loading" title="Đang đồng bộ tài khoản NexusMart..." rows={4} />
        </section>
        {renderSuggestedShelf("Gợi ý cho tủ đồ cơ bản")}
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Không thể đồng bộ thông tin tài khoản lúc này. Vui lòng thử lại.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
          <StateView
            kind="error"
            title="Chưa thể tải hồ sơ tài khoản"
            message={
              initialError ??
              "Không thể đọc thông tin tài khoản và sổ địa chỉ lúc này. Vui lòng tải lại trang."
            }
            actionLabel="Tải lại"
            actionHref="/tai-khoan"
          />
        </section>
        {renderSuggestedShelf("Sản phẩm nổi bật")}
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Tài khoản chưa có địa chỉ giao hàng hoặc đơn hàng nào được lưu.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Bắt đầu mua sắm</h2>
          <StateView
            kind="empty"
            title="Tài khoản chưa có hoạt động mua sắm"
            message="Thêm địa chỉ nhận hàng hoặc khám phá các sản phẩm từ 4 xưởng dệt Việt Nam."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        {renderSuggestedShelf("Khám phá đồ cơ bản trên NexusMart")}
      </div>
    );
  }

  return (
    <div className={shared.page}>
      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={`${shared.pageTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                Tài khoản của tôi
              </span>
            </span>
          </h1>
          <p className={styles.leadCopy} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {profile.fullName || profile.email} · Quản lý hồ sơ, địa chỉ nhận hàng và theo dõi đơn
            hàng từ 4 xưởng dệt.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          {profile.role === "seller" && (
            <Button variant="secondary" size="md" href="/seller">
              Kênh Người bán
            </Button>
          )}
          {(profile.role === "admin" || profile.role === "super_admin") && (
            <Button variant="secondary" size="md" href="/admin">
              Quản trị sàn
            </Button>
          )}
          <Button variant="secondary" size="md" href="/don-hang">
            Đơn hàng ({totalOrders})
          </Button>
          <Button variant="danger" size="md" onClick={() => void logout("/dang-nhap")}>
            Đăng xuất
          </Button>
        </div>
      </section>

      {/* Asymmetric Hero Summary: Dark Xu & Member Band + Crisp White Activity Panel */}
      <section className={shared.section}>
        <h2 className="sr-only">Tổng quan tài khoản và ví Xu</h2>
        <div className={styles.heroBand}>
          <div
            className={styles.memberDarkCard}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.tierHeadRow}>
              <div>
                <p className={styles.tierName}>Ví Xu & Hội viên Nexus</p>
                <p className={styles.tierSub}>1 Xu = 1₫ · Hạng tham khảo: {membershipTier.name}</p>
              </div>
              <span className={styles.tierBadge}>
                Hoàn {membershipTier.cashbackPercentLabel} Xu · Thứ Sáu x2
              </span>
            </div>

            <div className={styles.tierProgress}>
              <div className={styles.tierProgressMeta}>
                <span>
                  Chi tiêu 4 quý (xem thử):{" "}
                  <strong className="num">{formatVnd(membership.spend4Q)}</strong>
                </span>
                <span className="num">
                  {nextTier.next
                    ? `Còn ${formatVnd(nextTier.remainingVnd)} lên ${nextTier.next.name}`
                    : "Đã đạt hạng cao nhất"}
                </span>
              </div>
              <div className={styles.darkTrack} aria-hidden="true">
                <div
                  className={styles.darkFill}
                  style={{ width: `${nextTier.progressPercent}%` }}
                />
              </div>
            </div>

            <div className={styles.xuRow}>
              <div
                className={styles.xuStat}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <span className={`${styles.xuBig} num`}>{formatInt(profile.coins)} Xu</span>
                <span className={styles.xuCaption}>Xu khả dụng hiện tại</span>
              </div>
              <div
                className={styles.xuStat}
                data-rise=""
                style={{ "--i": 4 } as React.CSSProperties}
              >
                <span className={`${styles.xuMed} num`}>{totalOrders} đơn</span>
                <span className={styles.xuCaption}>Tổng đơn đã đặt</span>
              </div>
              <div
                className={styles.xuStat}
                data-rise=""
                style={{ "--i": 5 } as React.CSSProperties}
              >
                <span className={`${styles.xuMed} num`}>{addresses.length}</span>
                <span className={styles.xuCaption}>Địa chỉ đã lưu</span>
              </div>
            </div>
          </div>

          <div
            className={styles.activityCard}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.activityStats}>
              <div className={styles.activityItem}>
                <span className={`${styles.activityNum} num`}>{totalOrders}</span>
                <span className={styles.activityLabel}>
                  Đơn hàng ({inProgressOrders} đang xử lý)
                </span>
              </div>
              <div className={styles.activityItem}>
                <span className={`${styles.activityNum} num`}>{wishlist.length}</span>
                <span className={styles.activityLabel}>Sản phẩm yêu thích</span>
              </div>
              <div className={styles.activityItem}>
                <span className={`${styles.activityNum} num`}>{addresses.length}</span>
                <span className={styles.activityLabel}>Địa chỉ nhận hàng</span>
              </div>
            </div>

            <div className={styles.retentionBlock}>
              <p className={styles.retentionTitle}>Quyền lợi đổi trả 45 ngày & Ưu đãi thành viên</p>
              <p className={styles.bodyCopy}>
                Số dư Xu được trừ trực tiếp khi thanh toán đơn hàng trên cả 4 xưởng dệt. Chương
                trình hạng hội viên và thẻ quà tặng hiển thị riêng ở mục xem thử.
              </p>
              <div className={shared.formActions}>
                <Button variant="ink" size="sm" href="/don-hang">
                  Xem tất cả đơn hàng
                </Button>
                <Button variant="secondary" size="sm" href="/hoi-vien">
                  Xem bảng 4 hạng
                </Button>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Compact 6-tab bar */}
      <section className={shared.section}>
        <div
          className={styles.tabBar}
          role="tablist"
          aria-label="Danh mục quản lý tài khoản"
          data-rise=""
          style={{ "--i": 6 } as React.CSSProperties}
        >
          {ACCOUNT_TABS.map((tab) => {
            const active = activeTab === tab.key;
            const badge =
              tab.key === "dia-chi"
                ? addresses.length
                : tab.key === "yeu-thich"
                  ? wishlist.length
                  : tab.key === "voucher"
                    ? vouchers.length + membership.giftCards.length
                    : tab.key === "gioi-thieu"
                      ? membership.referrals.length
                      : null;
            return (
              <button
                key={tab.key}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${styles.tabBtn} ${active ? styles.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.key)}
              >
                <span>{tab.label}</span>
                {badge !== null && <span className={`${styles.tabCount} num`}>{badge}</span>}
              </button>
            );
          })}
        </div>

        {/* TAB 1: Tổng quan & Hồ sơ */}
        {activeTab === "tong-quan" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              <div className={styles.unboxedSection} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Thông tin tài khoản</h2>
                  <div className={shared.formActions}>
                    <Button
                      variant="ink"
                      size="sm"
                      onClick={() => {
                        setFullNameDraft(profile.fullName);
                        setPhoneDraft(profile.phone);
                        setProfileError(null);
                        setEditingProfile((v) => !v);
                      }}
                    >
                      {editingProfile ? "Đóng chỉnh sửa" : "Chỉnh sửa hồ sơ"}
                    </Button>
                    <Button variant="secondary" size="sm" onClick={() => openChat()}>
                      Nhắn tin gian hàng
                    </Button>
                    <Button variant="secondary" size="sm" onClick={openAi}>
                      Mở Trợ lý AI
                    </Button>
                  </div>
                </div>

                {editingProfile ? (
                  <form className={styles.yirPanel} onSubmit={handleSaveProfile} noValidate>
                    <div className={shared.formGrid}>
                      <div className={shared.field}>
                        <label htmlFor="acc-profile-name" className={shared.fieldLabel}>
                          Họ và tên
                        </label>
                        <input
                          id="acc-profile-name"
                          type="text"
                          className={shared.input}
                          value={fullNameDraft}
                          onChange={(e) => setFullNameDraft(e.target.value)}
                          disabled={savingProfile}
                          required
                        />
                      </div>
                      <div className={shared.field}>
                        <label htmlFor="acc-profile-phone" className={shared.fieldLabel}>
                          Số điện thoại
                        </label>
                        <input
                          id="acc-profile-phone"
                          type="tel"
                          className={shared.input}
                          value={phoneDraft}
                          onChange={(e) => setPhoneDraft(e.target.value)}
                          placeholder="0912486305"
                          disabled={savingProfile}
                        />
                      </div>
                    </div>
                    {profileError && (
                      <p className={shared.fieldError} role="alert">
                        {profileError}
                      </p>
                    )}
                    <div className={shared.formActions}>
                      <Button type="submit" variant="primary" size="sm" disabled={savingProfile}>
                        {savingProfile ? "Đang lưu..." : "Lưu thay đổi"}
                      </Button>
                      <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        onClick={() => setEditingProfile(false)}
                      >
                        Hủy
                      </Button>
                    </div>
                  </form>
                ) : (
                  <dl className={styles.profileGrid}>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Họ và tên</dt>
                      <dd className={styles.profileVal}>{profile.fullName || "Chưa cập nhật"}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Địa chỉ email</dt>
                      <dd className={styles.profileVal}>{profile.email}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Số điện thoại</dt>
                      <dd className={`${styles.profileVal} num`}>
                        {profile.phone || "Chưa cập nhật"}
                      </dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Ngày tham gia</dt>
                      <dd className={`${styles.profileVal} num`}>
                        {profile.memberSince ? formatDateVi(profile.memberSince) : "Đã kích hoạt"}
                      </dd>
                    </div>
                  </dl>
                )}
              </div>

              {/* Real recent orders */}
              <div className={styles.unboxedSectionLast} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Đơn hàng gần đây</h2>
                  <Link href="/don-hang" className={shared.breadcrumbLink}>
                    Xem tất cả ({totalOrders})
                  </Link>
                </div>

                {recentOrders.length === 0 ? (
                  <StateView
                    kind="empty"
                    title="Bạn chưa có đơn hàng nào"
                    message="Các đơn đặt từ 4 xưởng dệt sẽ hiển thị tại đây để bạn theo dõi giao nhận."
                    actionLabel="Khám phá danh mục"
                    actionHref="/danh-muc"
                  />
                ) : (
                  <div className={styles.orderList}>
                    {recentOrders.map((order) => {
                      const meta = ORDER_STATUS_META[order.status];
                      const firstItem = order.items[0];
                      return (
                        <Link
                          key={order.id}
                          href={`/don-hang/${order.id}`}
                          className={styles.orderRow}
                          data-reveal=""
                        >
                          <Image
                            src={firstItem?.image || "/products/ao-thun-cotton-trang.png"}
                            alt={firstItem?.productName ?? order.orderCode}
                            width={56}
                            height={75}
                            className={styles.orderThumb}
                          />
                          <div className={styles.orderInfo}>
                            <span className={`${styles.orderCode} num`}>{order.orderCode}</span>
                            <span className={styles.orderSub}>
                              {order.shopName} · <span className="num">{order.items.length}</span>{" "}
                              sản phẩm
                              {order.createdAt ? (
                                <>
                                  {" "}
                                  · <span className="num">{formatDateVi(order.createdAt)}</span>
                                </>
                              ) : null}
                            </span>
                          </div>
                          <div className={styles.orderRight}>
                            <span className={`${styles.orderTotal} num`}>
                              {formatVnd(order.total)}
                            </span>
                            <span className={`${shared.statusTag} ${toneClass(meta.tone)}`}>
                              {meta.label}
                            </span>
                          </div>
                        </Link>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            {/* Right column: Clearly separated mock-only membership preview blocks */}
            <div className={styles.colStack}>
              <div className={styles.birthdayBand} data-reveal="">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Ưu đãi sinh nhật Hội viên</h2>
                  <span className={`${shared.statusTag} ${shared.toneNeutral}`}>Xem thử</span>
                </div>
                <p className={styles.bodyCopy}>
                  Cập nhật ngày sinh để nhận tự động túi vải rút Sợi Xanh Nexus kèm mã giảm{" "}
                  <strong>SINHNHAT50</strong> (50.000₫) khi chương trình Hội viên mở chính thức.
                </p>
                <div className={shared.formGrid}>
                  <div className={shared.field}>
                    <label htmlFor="acc-birthday" className={shared.fieldLabel}>
                      Ngày sinh của bạn
                    </label>
                    <input
                      id="acc-birthday"
                      type="date"
                      className={shared.input}
                      value={birthdayDraft}
                      onChange={(e) => setBirthdayDraft(e.target.value)}
                    />
                  </div>
                  <div className={shared.field} style={{ justifyContent: "flex-end" }}>
                    <Button
                      variant="secondary"
                      size="md"
                      onClick={() => updateBirthday(birthdayDraft)}
                    >
                      Lưu ngày sinh
                    </Button>
                  </div>
                </div>
                <div className={shared.formActions}>
                  <Button
                    variant="primary"
                    size="md"
                    disabled={membershipTier.id === "thanh_vien" || membership.birthdayGiftClaimed}
                    onClick={() => {
                      claimBirthdayGift();
                    }}
                  >
                    {membership.birthdayGiftClaimed
                      ? "Đã ghi nhận quà sinh nhật"
                      : "Nhận quà sinh nhật (xem thử)"}
                  </Button>
                </div>
              </div>

              <div className={styles.yirPanel} data-reveal="">
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Tổng kết năm 2026</h2>
                  <span className={`${shared.statusTag} ${shared.toneNeutral}`}>Xem thử</span>
                </div>
                <p className={styles.bodyCopy}>{SAMPLE_DATA_NOTICE}</p>

                <div className={styles.yirGrid}>
                  <div className={styles.yirStat} data-reveal="">
                    <span className={`${styles.yirVal} num`}>
                      {formatVnd(membership.yearInReview.totalSpentVnd)}
                    </span>
                    <span className={styles.profileKey}>Tổng chi tiêu 2026</span>
                  </div>
                  <div className={styles.yirStat} data-reveal="">
                    <span className={`${styles.yirVal} num`}>
                      {membership.yearInReview.totalOrders} đơn
                    </span>
                    <span className={styles.profileKey}>Số đơn đã hoàn tất</span>
                  </div>
                  <div className={styles.yirStat} data-reveal="">
                    <span className={`${styles.yirVal} num`}>
                      {formatInt(membership.yearInReview.totalCoinsEarned)} Xu
                    </span>
                    <span className={styles.profileKey}>Xu tích lũy trong năm</span>
                  </div>
                  <div className={styles.yirStat} data-reveal="">
                    <span className={`${styles.yirVal} num`}>
                      {formatVnd(membership.yearInReview.csrContributedVnd)}
                    </span>
                    <span className={styles.profileKey}>Đóng góp Sợi Xanh</span>
                  </div>
                </div>

                <p className={styles.bodyCopy}>
                  Chất liệu chọn nhiều nhất:{" "}
                  <strong>{membership.yearInReview.favoriteFabric}</strong> · Xưởng yêu thích:{" "}
                  <strong>{membership.yearInReview.favoriteShopName}</strong>.
                </p>
              </div>
            </div>
          </div>
        )}

        {/* TAB 2: Địa chỉ nhận hàng (REAL backend CRUD) */}
        {activeTab === "dia-chi" && (
          <div className={styles.colStack}>
            <div className={shared.sectionHead}>
              <h2 className={shared.sectionTitle}>Địa chỉ nhận hàng</h2>
              <Button variant="ink" size="sm" onClick={() => setShowAddAddress((prev) => !prev)}>
                {showAddAddress ? "Đóng biểu mẫu" : "Thêm địa chỉ mới"}
              </Button>
            </div>

            {addressError && (
              <p className={shared.fieldError} role="alert">
                {addressError}
              </p>
            )}

            {showAddAddress && (
              <AccountAddressForm
                defaultRecipientName={profile.fullName}
                defaultPhone={profile.phone}
                provinces={provinces}
                initialProvinceCode={initialProvinceCode}
                initialWards={initialWards}
                submitLabel="Lưu địa chỉ nhận hàng"
                onSaved={(created) => {
                  setAddresses((prev) => {
                    const updated = created.isDefault
                      ? prev.map((a) => ({ ...a, isDefault: false }))
                      : prev;
                    return [created, ...updated];
                  });
                  setShowAddAddress(false);
                  showToast(`Đã lưu địa chỉ tại ${created.wardName}, ${created.provinceName}.`);
                  router.refresh();
                }}
                onCancel={() => setShowAddAddress(false)}
              />
            )}

            {addresses.length === 0 ? (
              <StateView
                kind="empty"
                title="Bạn chưa lưu địa chỉ nhận hàng nào"
                message="Thêm địa chỉ Tỉnh/Thành phố và Phường/Xã để thanh toán nhanh chóng hơn."
                actionLabel="Thêm địa chỉ đầu tiên"
                onAction={() => setShowAddAddress(true)}
              />
            ) : (
              <div className={styles.addressStack}>
                {addresses.map((addr) => {
                  const previewProv = getProvinceByCode(
                    toStorefrontProvinceCode(addr.provinceCode)
                  );
                  const isBusy = busyAddressId === addr.id;
                  return (
                    <article
                      key={addr.id}
                      className={`${styles.addressRow} ${
                        addr.isDefault ? styles.addressDefault : ""
                      }`}
                    >
                      <div className={styles.addressHead}>
                        <span className={styles.addressRecipient}>
                          {addr.recipientName} · <span className="num">{addr.phone}</span>
                        </span>
                        {addr.isDefault && (
                          <span className={`${shared.statusTag} ${shared.tonePos}`}>Mặc định</span>
                        )}
                      </div>
                      <p className={styles.bodyCopy}>
                        {[addr.streetDetail, addr.wardName, addr.provinceName]
                          .filter(Boolean)
                          .join(", ")}
                      </p>
                      <p className={styles.metaNote}>
                        Giao dự kiến {previewProv?.etaDays ?? "1–3 ngày"} · Phí chuẩn tham khảo{" "}
                        <span className="num">{formatVnd(previewProv?.shippingFee ?? 28000)}</span>
                      </p>
                      <div className={styles.addressActions}>
                        {!addr.isDefault && (
                          <Button
                            variant="secondary"
                            size="sm"
                            disabled={isBusy}
                            onClick={() => void handleSetDefaultAddress(addr)}
                          >
                            {isBusy ? "Đang cập nhật..." : "Đặt mặc định"}
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={isBusy}
                          onClick={() => void handleDeleteAddress(addr.id)}
                        >
                          Xóa địa chỉ
                        </Button>
                      </div>
                    </article>
                  );
                })}
              </div>
            )}
          </div>
        )}

        {/* TAB 3: Hồ sơ số đo & Đề xuất size cho 4 xưởng (viewer preference) */}
        {activeTab === "so-do" && (
          <div className={styles.colStack}>
            <form className={styles.yirPanel} onSubmit={handleSaveBodyProfile} noValidate>
              <div className={shared.sectionHead}>
                <h2 className={shared.sectionTitle}>Hồ sơ số đo cá nhân</h2>
                <span className={shared.sectionNote}>
                  Lưu trên trình duyệt để tự đối chiếu bảng size cả 4 xưởng dệt
                </span>
              </div>

              <div className={shared.formGrid}>
                <div className={shared.field}>
                  <label htmlFor="bp-height" className={shared.fieldLabel}>
                    Chiều cao (cm)
                  </label>
                  <input
                    id="bp-height"
                    type="number"
                    min={145}
                    max={205}
                    className={shared.input}
                    value={heightCm}
                    onChange={(e) => setHeightCm(Number(e.target.value) || 170)}
                  />
                </div>
                <div className={shared.field}>
                  <label htmlFor="bp-weight" className={shared.fieldLabel}>
                    Cân nặng (kg)
                  </label>
                  <input
                    id="bp-weight"
                    type="number"
                    min={38}
                    max={130}
                    className={shared.input}
                    value={weightKg}
                    onChange={(e) => setWeightKg(Number(e.target.value) || 65)}
                  />
                </div>

                <div className={`${shared.field} ${shared.formFull}`}>
                  <span className={shared.fieldLabel}>Chọn 1 trong 3 dáng người</span>
                  <div className={styles.shapeGrid} role="radiogroup" aria-label="Dáng người">
                    {(["gay", "vua", "day"] as BodyShape[]).map((key) => {
                      const active = shape === key;
                      return (
                        <button
                          key={key}
                          type="button"
                          role="radio"
                          aria-checked={active}
                          className={`${styles.shapeBtn} ${active ? styles.shapeBtnActive : ""}`}
                          onClick={() => setShape(key)}
                        >
                          <strong>{BODY_SHAPE_LABELS[key].label}</strong>
                          <span className={styles.shapeHint}>{BODY_SHAPE_LABELS[key].hint}</span>
                        </button>
                      );
                    })}
                  </div>
                </div>

                <div className={`${shared.field} ${shared.formFull}`}>
                  <span className={shared.fieldLabel}>Gu mặc ưu tiên</span>
                  <div className={shared.modeSwitch}>
                    {(["om", "vua", "rong"] as FitPreference[]).map((pref) => (
                      <button
                        key={pref}
                        type="button"
                        className={`${shared.modeBtn} ${
                          preferredFit === pref ? shared.modeBtnActive : ""
                        }`}
                        onClick={() => setPreferredFit(pref)}
                      >
                        {FIT_PREF_LABELS[pref]}
                      </button>
                    ))}
                  </div>
                </div>
              </div>

              <div className={shared.formActions}>
                <Button type="submit" variant="primary" size="md">
                  Lưu hồ sơ số đo toàn sàn
                </Button>
                <Button type="button" variant="secondary" size="md" onClick={openAi}>
                  Hỏi Trợ lý AI về số đo này
                </Button>
              </div>
            </form>

            <div>
              <div className={shared.sectionHead}>
                <h2 className={shared.sectionTitle}>Size đề xuất theo rập riêng của 4 xưởng dệt</h2>
                <span className={`${shared.sectionNote} num`}>
                  {heightCm} cm · {weightKg} kg · {BODY_SHAPE_LABELS[shape].label}
                </span>
              </div>

              <div className={styles.shopSizeGrid}>
                {shopRecommendations.map(({ shop, top, bottom }) => (
                  <article key={shop.id} className={styles.shopSizeRow}>
                    <div className={styles.addressHead}>
                      <Link href={`/gian-hang/${shop.slug}`} className={styles.addressRecipient}>
                        {shop.name}
                      </Link>
                      <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                        {shop.sizeCharts?.fitBias === "slim"
                          ? "Phom ôm gọn"
                          : shop.sizeCharts?.fitBias === "relaxed"
                            ? "Phom thoáng"
                            : "Phom chuẩn"}
                      </span>
                    </div>

                    <div className={styles.sizeBadgeRow}>
                      <span className={styles.sizePill}>
                        Áo: <strong>Size {top.size}</strong>
                        {top.alternativeSize ? ` (hoặc ${top.alternativeSize})` : ""}
                      </span>
                      <span className={styles.sizePill}>
                        Quần: <strong>Size {bottom.size}</strong>
                        {bottom.alternativeSize ? ` (hoặc ${bottom.alternativeSize})` : ""}
                      </span>
                    </div>

                    {top.measurements && (
                      <p className={styles.bodyCopy}>
                        Thông số áo {top.size}: Ngực {top.measurements.chestOrWaistCm} cm · Dài{" "}
                        {top.measurements.lengthCm} cm
                        {top.measurements.shoulderCm
                          ? ` · Vai ${top.measurements.shoulderCm} cm`
                          : ""}
                      </p>
                    )}
                    <p className={styles.bodyCopy}>{top.shopSizeNote}</p>
                  </article>
                ))}
              </div>
            </div>
          </div>
        )}

        {/* TAB 4: Sản phẩm yêu thích & Đã xem gần đây */}
        {activeTab === "yeu-thich" && (
          <div className={styles.colStack}>
            <div>
              <div className={shared.sectionHead}>
                <h2 className={shared.sectionTitle}>
                  Danh sách sản phẩm yêu thích ({wishlistedProducts.length})
                </h2>
                <Link href="/danh-muc" className={shared.breadcrumbLink}>
                  Xem thêm sản phẩm
                </Link>
              </div>

              {wishlistedProducts.length === 0 ? (
                <StateView
                  kind="empty"
                  title="Chưa có sản phẩm nào trong mục yêu thích"
                  message="Bấm biểu tượng trái tim trên bất kỳ sản phẩm nào để theo dõi giá ưu đãi và tình trạng còn hàng."
                  actionLabel="Khám phá danh mục"
                  actionHref="/danh-muc"
                />
              ) : (
                <div className={styles.wishlistGrid}>
                  {wishlistedProducts.map(({ entry, prod }) => {
                    const shop = shopById.get(prod.shopId);
                    const totalStock = prod.skus.reduce((s, sk) => s + sk.stock, 0);
                    const defaultVariant =
                      prod.skus.find((s) => s.size === "L" && s.stock > 0) ??
                      prod.skus.find((s) => s.stock > 0);
                    const isDiscounted =
                      prod.originalPrice != null && prod.originalPrice > prod.price;
                    const shot = MODEL_SHOTS[prod.id];

                    return (
                      <article key={entry.productId} className={styles.wishlistCard}>
                        <Link href={`/san-pham/${prod.slug}`} className={styles.wishlistThumbLink}>
                          <Image
                            src={shot?.src ?? prod.image}
                            alt={prod.name}
                            width={92}
                            height={123}
                            className={styles.wishlistImg}
                          />
                        </Link>
                        <div className={styles.wishlistBody}>
                          <span className={styles.metaNote}>
                            {shop?.shortName ?? "NexusMart"}
                            {prod.gsm > 0 ? ` · ${prod.gsm}gsm` : ""}
                          </span>
                          <Link href={`/san-pham/${prod.slug}`} className={styles.addressRecipient}>
                            {prod.name}
                          </Link>
                          <Price
                            amount={prod.price}
                            originalAmount={prod.originalPrice}
                            showDiscount
                            size="sm"
                          />
                          <div className={styles.sizeBadgeRow}>
                            <span
                              className={`${shared.statusTag} ${
                                totalStock > 15 ? shared.tonePos : shared.toneWarn
                              }`}
                            >
                              {totalStock > 0 ? `Còn ${totalStock} SP` : "Tạm hết hàng"}
                            </span>
                            {isDiscounted && (
                              <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                                Đang giảm giá
                              </span>
                            )}
                          </div>
                          <div className={styles.addressActions}>
                            {defaultVariant && (
                              <Button
                                variant="ink"
                                size="sm"
                                onClick={() => void handleAddWishlistToCart(prod)}
                              >
                                Thêm vào giỏ ({defaultVariant.size})
                              </Button>
                            )}
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => toggleWishlist(prod.id)}
                            >
                              Bỏ yêu thích
                            </Button>
                          </div>
                        </div>
                      </article>
                    );
                  })}
                </div>
              )}
            </div>

            {recentlyViewedProducts.length > 0 && (
              <div>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Sản phẩm bạn đã xem gần đây</h2>
                  <Button variant="ghost" size="sm" onClick={clearRecentlyViewed}>
                    Xóa lịch sử xem
                  </Button>
                </div>
                <div className={styles.productGrid}>
                  {recentlyViewedProducts.map((product) => (
                    <ProductCard key={product.id} product={product} />
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {/* TAB 5: Ví voucher (REAL) & Thẻ quà tặng (mock) */}
        {activeTab === "voucher" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              <div>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Ví voucher NexusMart</h2>
                  <span className={`${shared.sectionNote} num`}>
                    Đã lưu {savedVoucherCount} / {vouchers.length} mã
                  </span>
                </div>

                {vouchers.length === 0 ? (
                  <StateView
                    kind="empty"
                    title="Hiện chưa có mã ưu đãi khả dụng"
                    message="Quay lại sau hoặc theo dõi trang Flash sale để săn ưu đãi mới."
                    actionLabel="Xem Flash sale"
                    actionHref="/flash-sale"
                  />
                ) : (
                  <div className={styles.voucherList}>
                    {vouchers.map((voucher) => {
                      const shop = voucher.shopId ? shopById.get(voucher.shopId) : undefined;
                      const isSaved = Boolean(voucher.saved);
                      const scopeLabel =
                        voucher.scope === "platform"
                          ? "Toàn sàn"
                          : voucher.scope === "freeship"
                            ? "Vận chuyển"
                            : (shop?.shortName ?? "Gian hàng");
                      const remaining = voucher.remainingUses ?? 0;
                      const exhausted = remaining <= 0;
                      return (
                        <div key={voucher.code} className={styles.voucherRow}>
                          <div className={styles.voucherInfo}>
                            <div className={styles.voucherHeadLine}>
                              <span className={styles.voucherCode}>{voucher.code}</span>
                              <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                                {scopeLabel}
                              </span>
                              {isSaved && (
                                <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                  Đã lưu
                                </span>
                              )}
                              {exhausted && (
                                <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                                  Hết lượt
                                </span>
                              )}
                            </div>
                            <span className={styles.voucherTitle}>{voucher.title}</span>
                            <span className={styles.metaNote}>
                              Đơn tối thiểu{" "}
                              <span className="num">{formatVnd(voucher.minOrderValue)}</span>
                              {voucher.expiresAt ? (
                                <>
                                  {" "}
                                  · HSD{" "}
                                  <span className="num">{formatDateVi(voucher.expiresAt)}</span>
                                </>
                              ) : null}
                            </span>
                          </div>
                          <div className={shared.formActions}>
                            {!isSaved && !exhausted && (
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={savingVoucherCode === voucher.code}
                                onClick={() => void handleSaveVoucher(voucher)}
                              >
                                {savingVoucherCode === voucher.code ? "Đang lưu..." : "Lưu mã"}
                              </Button>
                            )}
                            <Button
                              variant="secondary"
                              size="sm"
                              disabled={exhausted}
                              onClick={() => handleApplyVoucher(voucher)}
                            >
                              Dùng trong giỏ
                            </Button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            <div className={styles.colStack}>
              <div>
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Thẻ quà tặng & Ưu đãi .edu (xem thử)</h2>
                  <Button variant="ink" size="sm" href="/the-qua-tang">
                    Xem trang thẻ quà tặng
                  </Button>
                </div>

                <div className={styles.voucherList}>
                  {membership.giftCards.map((gc) => {
                    const isApplied = membership.appliedGiftCardCode === gc.code;
                    return (
                      <div key={gc.id} className={styles.voucherRow}>
                        <div className={styles.voucherInfo}>
                          <div className={styles.voucherHeadLine}>
                            <span className={`${styles.voucherCode} num`}>{gc.code}</span>
                            <span
                              className={`${shared.statusTag} ${
                                gc.remainingVnd > 0 ? shared.tonePos : shared.toneNeutral
                              }`}
                            >
                              Còn {formatVnd(gc.remainingVnd)}
                            </span>
                          </div>
                          <span className={styles.metaNote}>
                            Mệnh giá {formatVnd(gc.amountVnd)} · HSD{" "}
                            <span className="num">{formatDateVi(gc.expiresAt)}</span>
                          </span>
                        </div>
                        <Button
                          variant={isApplied ? "primary" : "secondary"}
                          size="sm"
                          disabled={gc.remainingVnd <= 0}
                          onClick={() => {
                            applyGiftCardCode(isApplied ? null : gc.code);
                          }}
                        >
                          {isApplied ? "Đang ghi nhận" : "Chọn thẻ"}
                        </Button>
                      </div>
                    );
                  })}
                </div>

                <div className={styles.birthdayBand} style={{ marginTop: 20 }}>
                  <p className={styles.addressRecipient}>
                    Ưu đãi sinh viên (.edu / .edu.vn):{" "}
                    {membership.eduVerified ? "Đã xác thực (xem thử)" : "Chưa xác thực"}
                  </p>
                  <p className={styles.bodyCopy}>
                    {membership.eduVerified
                      ? `Email ${membership.eduEmail} đã mở khóa ưu đãi sinh viên EDU15 (xem thử).`
                      : "Khám phá chương trình xác thực email sinh viên để nhận ưu đãi 15%."}
                  </p>
                  <div className={shared.formActions}>
                    <Button variant="primary" size="sm" href="/the-qua-tang">
                      {membership.eduVerified ? "Quản lý ưu đãi .edu" : "Xem ưu đãi .edu"}
                    </Button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* TAB 6: Giới thiệu bạn bè (mock-only sub-tab) */}
        {activeTab === "gioi-thieu" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              <div className={styles.referralBanner}>
                <span className={styles.xuCaption}>Mã mời cá nhân của bạn (xem thử)</span>
                <p className={`${styles.referralCodeDisplay} num`}>{membership.referralCode}</p>
                <p className={styles.referralBody}>{REFERRAL_CONFIG.ruleSummary}</p>
                <div className={shared.formActions}>
                  <Button variant="primary" size="md" onClick={() => void handleCopyReferral()}>
                    Sao chép liên kết mời
                  </Button>
                  <Button variant="secondary" size="md" href="/hoi-vien">
                    Xem chi tiết tại trang Hội viên
                  </Button>
                </div>
              </div>

              <form className={styles.yirPanel} onSubmit={handleInviteSubmit} noValidate>
                <h2 className={shared.sectionTitle}>Thử gửi lời mời bạn bè</h2>
                <div className={shared.codeInputRow}>
                  <input
                    type="text"
                    className={shared.input}
                    value={friendName}
                    onChange={(e) => setFriendName(e.target.value)}
                    placeholder="Nhập tên người bạn muốn mời (ví dụ: Phạm Minh Khôi)"
                    aria-label="Tên người bạn được mời"
                  />
                  <Button type="submit" variant="ink" size="md" className={shared.wrapBtn}>
                    Gửi lời mời thử
                  </Button>
                </div>
              </form>
            </div>

            <div className={styles.colStack}>
              <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                <h2 className={shared.sectionTitle}>Lịch sử giới thiệu bạn bè (xem thử)</h2>
                <span className={`${shared.sectionNote} num`}>
                  {membership.referrals.length} lượt mời
                </span>
              </div>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Bạn được mời</th>
                      <th>Ngày mời</th>
                      <th>Thưởng 10%</th>
                      <th>Trạng thái</th>
                    </tr>
                  </thead>
                  <tbody>
                    {membership.referrals.map((ref) => (
                      <tr key={ref.id}>
                        <td data-label="Bạn được mời">
                          <strong>{ref.friendName}</strong>
                          {ref.orderCode && (
                            <div className={`${styles.orderSub} num`}>Đơn {ref.orderCode}</div>
                          )}
                        </td>
                        <td data-label="Ngày mời" className="num">
                          {formatDateVi(ref.invitedAt)}
                        </td>
                        <td data-label="Thưởng 10%" className="num">
                          {ref.rewardCoins > 0 ? `+${formatInt(ref.rewardCoins)} Xu` : "Chờ đơn"}
                        </td>
                        <td data-label="Trạng thái">
                          <span
                            className={`${shared.statusTag} ${
                              ref.status === "da_cong_xu"
                                ? shared.tonePos
                                : ref.status === "cho_het_han_45_ngay"
                                  ? shared.toneWarn
                                  : shared.toneNeutral
                            }`}
                          >
                            {ref.status === "da_cong_xu"
                              ? "Đã cộng Xu"
                              : ref.status === "cho_het_han_45_ngay"
                                ? "Chờ hết hạn 45 ngày"
                                : "Chờ đặt đơn"}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        )}
      </section>

      {renderSuggestedShelf(
        "Đồ cơ bản gợi ý cho tủ đồ của bạn",
        "Gợi ý đối chiếu trực tiếp từ hồ sơ số đo của bạn"
      )}
    </div>
  );
}

export function AccountClientView(props: AccountClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải trang tài khoản..." rows={4} />
        </div>
      }
    >
      <AccountClientInner {...props} />
    </Suspense>
  );
}
