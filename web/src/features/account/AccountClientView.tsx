"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ORDER_STATUS_META } from "@/data/orders";
import type { Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { MODEL_SHOTS } from "@/data/media";
import { ApiError, api } from "@/lib/api";
import { getProvinceByCode } from "@/lib/provinces";
import { formatDateVi, formatVnd } from "@/lib/format";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { Button, IconClose, Price, ProductCard, StateView } from "@/components";
import { useModalFocusTrap } from "@/components/modalFocus";
import { formatAddressLine } from "@/features/commerce/cartEnrich";
import { statusToneClass } from "@/features/commerce/orderDisplay";
import { usePageStateParam } from "@/features/commerce/usePageStateParam";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./AccountClientView.module.css";
import { AccountAddressForm } from "./AccountAddressForm";
import {
  toStorefrontProvinceCode,
  type AccountProfile,
  type AddressView,
  type GwProvince,
  type GwWard,
} from "./accountTypes";
import type { OrderView } from "./orderView";

type AccountTabKey = "tong-quan" | "dia-chi" | "voucher" | "yeu-thich";

const ACCOUNT_TABS: Array<{ key: AccountTabKey; label: string }> = [
  { key: "tong-quan", label: "Hồ sơ" },
  { key: "dia-chi", label: "Sổ địa chỉ" },
  { key: "voucher", label: "Mã giảm giá" },
  { key: "yeu-thich", label: "Yêu thích" },
];

const VALID_TABS = new Set<AccountTabKey>(ACCOUNT_TABS.map((t) => t.key));

export interface AccountClientViewProps {
  brandName: string;
  initialProfile: AccountProfile;
  initialAddresses: AddressView[];
  initialOrders: OrderView[];
  totalOrders: number;
  initialVouchers: Voucher[];
  provinces: GwProvince[];
  initialWards: GwWard[];
  initialProvinceCode: string;
  catalogProducts: Product[];
  initialError?: string | null;
}

function AccountClientInner({
  brandName,
  initialProfile,
  initialAddresses,
  initialOrders,
  totalOrders,
  initialVouchers,
  provinces,
  initialWards,
  initialProvinceCode,
  catalogProducts,
  initialError = null,
}: AccountClientViewProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const viewState = usePageStateParam();
  const { setUser, logout } = useSession();
  const {
    addToCart,
    saveVoucher,
    setSelectedVoucher,
    wishlist,
    toggleWishlist,
    recentlyViewed,
    clearRecentlyViewed,
    showToast,
  } = useStore();

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
  const [addressSheetMode, setAddressSheetMode] = useState<"closed" | "add" | "edit">("closed");
  const [editingAddress, setEditingAddress] = useState<AddressView | null>(null);
  const [busyAddressId, setBusyAddressId] = useState<string | null>(null);
  const [addressError, setAddressError] = useState<string | null>(null);
  const addressSheetRef = useRef<HTMLDivElement | null>(null);

  const [vouchers, setVouchers] = useState<Voucher[]>(
    initialVouchers.filter((v) => v.scope === "shop")
  );
  const [savingVoucherCode, setSavingVoucherCode] = useState<string | null>(null);

  const closeAddressSheet = useCallback(() => {
    setAddressSheetMode("closed");
    setEditingAddress(null);
  }, []);

  useModalFocusTrap({
    open: addressSheetMode !== "closed",
    containerRef: addressSheetRef,
    onEscape: closeAddressSheet,
  });

  const productById = useMemo(() => {
    const map = new Map<string, Product>();
    for (const p of catalogProducts) {
      map.set(p.id, p);
      if (p.backendId) map.set(p.backendId, p);
    }
    return map;
  }, [catalogProducts]);

  const recentOrders = useMemo(() => initialOrders.slice(0, 4), [initialOrders]);

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

  const handleSaveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fullNameDraft.trim();
    const cleanPhone = phoneDraft.replace(/\s+/g, "").trim();

    if (cleanName.length < 2) {
      setProfileError("Nhập họ và tên tối thiểu 2 ký tự.");
      return;
    }
    if (cleanPhone && !/^0\d{9,10}$/.test(cleanPhone)) {
      setProfileError("Số điện thoại cần 10 hoặc 11 chữ số, bắt đầu bằng 0.");
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
      showToast("Đã cập nhật hồ sơ.");
      router.refresh();
    } catch (err) {
      setProfileError(err instanceof ApiError ? err.message : "Chưa cập nhật được lúc này.");
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
      setAddresses((prev) => prev.map((item) => ({ ...item, isDefault: item.id === addr.id })));
      showToast("Đã đặt làm địa chỉ mặc định.");
      router.refresh();
    } catch (err) {
      setAddressError(
        err instanceof ApiError ? err.message : "Chưa cập nhật được địa chỉ mặc định."
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
      showToast("Đã xóa địa chỉ.");
      router.refresh();
    } catch (err) {
      setAddressError(err instanceof ApiError ? err.message : "Chưa xóa được địa chỉ.");
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
    setSelectedVoucher(voucher.id ?? voucher.code);
    showToast(`Đã chọn mã ${voucher.code}.`);
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

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <h1 className={shared.pageTitle}>Tài khoản</h1>
        </section>
        <StateView kind="loading" skeletonVariant="detail" title="Đang tải tài khoản..." rows={4} />
      </div>
    );
  }

  if (viewState === "error" || initialError) {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <h1 className={shared.pageTitle}>Tài khoản</h1>
        </section>
        <StateView
          kind="error"
          title="Chưa thể tải hồ sơ tài khoản."
          message={initialError ?? undefined}
          actionLabel="Tải lại"
          actionHref="/tai-khoan"
        />
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
                Tài khoản
              </span>
            </span>
          </h1>
          <p
            className={`${shared.pageLead} ${styles.leadMetaRow}`}
            data-rise=""
            style={{ "--i": 1 } as React.CSSProperties}
          >
            <span>{profile.fullName || profile.email}</span>
            <span>{brandName}</span>
          </p>
        </div>
      </section>

      <div className={styles.shellGrid}>
        <nav className={styles.leftNav} aria-label="Danh mục tài khoản">
          <div className={styles.navList} role="tablist" aria-orientation="vertical">
            {ACCOUNT_TABS.map((tab) => {
              const active = activeTab === tab.key;
              const badge =
                tab.key === "dia-chi"
                  ? addresses.length
                  : tab.key === "voucher"
                    ? vouchers.length
                    : tab.key === "yeu-thich"
                      ? wishlistedProducts.length
                      : null;
              return (
                <button
                  key={tab.key}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  className={`${styles.navItem} ${active ? styles.navItemActive : ""}`}
                  onClick={() => setActiveTab(tab.key)}
                >
                  <span>{tab.label}</span>
                  {badge !== null && <span className={`${styles.navCount} num`}>{badge}</span>}
                </button>
              );
            })}
          </div>

          <Link href="/don-hang" className={`${styles.navItem} ${styles.navSecondaryStart}`}>
            <span>Đơn hàng</span>
            <span className={`${styles.navCount} num`}>{totalOrders}</span>
          </Link>

          <button
            type="button"
            className={`${styles.navItem} ${styles.navDanger}`}
            onClick={() => void logout("/dang-nhap")}
          >
            <span>Đăng xuất</span>
          </button>
        </nav>

        <div className={styles.pane}>
          {/* Tab 1: Hồ sơ */}
          {activeTab === "tong-quan" && (
            <>
              <section className={styles.card}>
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Hồ sơ</h2>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => {
                      setFullNameDraft(profile.fullName);
                      setPhoneDraft(profile.phone);
                      setProfileError(null);
                      setEditingProfile((v) => !v);
                    }}
                  >
                    {editingProfile ? "Đóng" : "Sửa"}
                  </Button>
                </div>

                {editingProfile ? (
                  <form onSubmit={handleSaveProfile} noValidate>
                    <div className={shared.formGrid}>
                      <div className={shared.field}>
                        <label htmlFor="acc-profile-name" className={shared.fieldLabel}>
                          Họ và tên
                        </label>
                        <input
                          id="acc-profile-name"
                          type="text"
                          autoComplete="name"
                          className={shared.input}
                          aria-describedby={profileError ? "acc-profile-err" : undefined}
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
                          autoComplete="tel"
                          className={shared.input}
                          aria-describedby={profileError ? "acc-profile-err" : undefined}
                          value={phoneDraft}
                          onChange={(e) => setPhoneDraft(e.target.value)}
                          placeholder="0912 486 305"
                          disabled={savingProfile}
                        />
                      </div>
                      {profileError && (
                        <p
                          id="acc-profile-err"
                          className={`${shared.fieldError} ${shared.formFull}`}
                          role="alert"
                        >
                          {profileError}
                        </p>
                      )}
                      <div className={`${shared.formActions} ${shared.formFull}`}>
                        <Button
                          type="submit"
                          variant="primary"
                          size="sm"
                          loading={savingProfile}
                          loadingLabel="Đang lưu"
                        >
                          Lưu thay đổi
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
                    </div>
                  </form>
                ) : (
                  <dl className={styles.profileGrid}>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Họ và tên</dt>
                      <dd className={styles.profileVal}>{profile.fullName || "Chưa cập nhật"}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Email</dt>
                      <dd className={styles.profileVal}>{profile.email}</dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Số điện thoại</dt>
                      <dd className={`${styles.profileVal} num`}>
                        {profile.phone || "Chưa cập nhật"}
                      </dd>
                    </div>
                    <div className={styles.profileItem}>
                      <dt className={styles.profileKey}>Tham gia</dt>
                      <dd className={`${styles.profileVal} num`}>
                        {profile.memberSince ? formatDateVi(profile.memberSince) : "Đã kích hoạt"}
                      </dd>
                    </div>
                  </dl>
                )}
              </section>

              <section className={styles.card}>
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Đơn gần đây</h2>
                  <Link href="/don-hang" className={shared.breadcrumbLink}>
                    Xem tất cả (<span className="num">{totalOrders}</span>)
                  </Link>
                </div>

                {recentOrders.length === 0 ? (
                  <StateView
                    kind="empty"
                    title="Chưa có đơn hàng nào."
                    actionLabel="Xem sản phẩm"
                    actionHref="/san-pham"
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
                        >
                          <img
                            src={firstItem?.image || "/products/ao-thun-cotton-trang.png"}
                            alt={firstItem?.productName ?? order.orderCode}
                            width={896}
                            height={1200}
                            loading="lazy"
                            className={styles.orderThumb}
                          />
                          <div className={styles.orderInfo}>
                            <span className={`${styles.orderCode} num`}>{order.orderCode}</span>
                            <span className={styles.orderSub}>
                              <span>
                                <span className="num">{order.items.length}</span> sản phẩm
                              </span>
                              {order.createdAt && (
                                <span className="num">{formatDateVi(order.createdAt)}</span>
                              )}
                            </span>
                          </div>
                          <div className={styles.orderRight}>
                            <span className={`${styles.orderTotal} num`}>
                              {formatVnd(order.total)}
                            </span>
                            <span className={`${shared.statusTag} ${statusToneClass(meta.tone)}`}>
                              {meta.label}
                            </span>
                          </div>
                        </Link>
                      );
                    })}
                  </div>
                )}
              </section>
            </>
          )}

          {/* Tab 2: Sổ địa chỉ (thẻ + sheet sửa/thêm) */}
          {activeTab === "dia-chi" && (
            <section className={styles.pane}>
              <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                <h2 className={shared.sectionTitle}>Sổ địa chỉ</h2>
                <Button
                  variant="ink"
                  size="sm"
                  onClick={() => {
                    setEditingAddress(null);
                    setAddressSheetMode("add");
                  }}
                >
                  Thêm địa chỉ
                </Button>
              </div>

              {addressError && (
                <p className={shared.fieldError} role="alert">
                  {addressError}
                </p>
              )}

              {addresses.length === 0 ? (
                <StateView
                  kind="empty"
                  title="Chưa có địa chỉ nhận hàng."
                  actionLabel="Thêm địa chỉ"
                  onAction={() => {
                    setEditingAddress(null);
                    setAddressSheetMode("add");
                  }}
                />
              ) : (
                <div className={styles.addressGrid} data-reveal="rows">
                  {addresses.map((addr) => {
                    const previewProv = getProvinceByCode(
                      toStorefrontProvinceCode(addr.provinceCode)
                    );
                    const isBusy = busyAddressId === addr.id;
                    return (
                      <article
                        key={addr.id}
                        className={`${styles.addressCard} ${
                          addr.isDefault ? styles.addressCardDefault : ""
                        }`}
                      >
                        <div className={styles.addressBody}>
                          <div className={styles.addressHead}>
                            <span className={styles.addressRecipient}>
                              <span>{addr.recipientName}</span>
                              <span className={`${styles.addressPhone} num`}>{addr.phone}</span>
                            </span>
                            {addr.isDefault && (
                              <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                Mặc định
                              </span>
                            )}
                          </div>
                          <p className={styles.bodyCopy}>
                            {formatAddressLine(addr.streetDetail, addr.wardName, addr.provinceName)}
                          </p>
                          <p className={styles.metaNote}>
                            Dự kiến {previewProv?.etaDays ?? "1–3 ngày"}
                          </p>
                        </div>

                        <div className={styles.addressActions}>
                          <Button
                            variant="secondary"
                            size="sm"
                            disabled={isBusy}
                            onClick={() => {
                              setEditingAddress(addr);
                              setAddressSheetMode("edit");
                            }}
                          >
                            Sửa
                          </Button>
                          {!addr.isDefault && (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={isBusy}
                              onClick={() => void handleSetDefaultAddress(addr)}
                            >
                              Đặt mặc định
                            </Button>
                          )}
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={isBusy}
                            onClick={() => void handleDeleteAddress(addr.id)}
                          >
                            Xóa
                          </Button>
                        </div>
                      </article>
                    );
                  })}
                </div>
              )}
            </section>
          )}

          {/* Tab 3: Mã giảm giá */}
          {activeTab === "voucher" && (
            <section className={styles.card}>
              <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                <h2 className={shared.sectionTitle}>Mã giảm giá</h2>
                <span className={`${shared.sectionNote} num`}>{vouchers.length} mã</span>
              </div>

              {vouchers.length === 0 ? (
                <StateView
                  kind="empty"
                  title="Chưa có mã ưu đãi khả dụng."
                  actionLabel="Xem sản phẩm"
                  actionHref="/san-pham"
                />
              ) : (
                <div className={styles.voucherList}>
                  {vouchers.map((voucher) => {
                    const isSaved = Boolean(voucher.saved);
                    const remaining = voucher.remainingUses ?? 0;
                    const exhausted = remaining <= 0;
                    return (
                      <div key={voucher.code} className={styles.voucherRow}>
                        <div className={styles.voucherInfo}>
                          <div className={styles.voucherHeadLine}>
                            <span className={styles.voucherCode}>{voucher.code}</span>
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
                            <span>
                              Tối thiểu{" "}
                              <span className="num">{formatVnd(voucher.minOrderValue)}</span>
                            </span>
                            {voucher.expiresAt && (
                              <span>
                                HSD <span className="num">{formatDateVi(voucher.expiresAt)}</span>
                              </span>
                            )}
                          </span>
                        </div>
                        <div className={shared.formActions}>
                          {!isSaved && !exhausted && (
                            <Button
                              variant="ghost"
                              size="sm"
                              loading={savingVoucherCode === voucher.code}
                              loadingLabel="Đang lưu"
                              onClick={() => void handleSaveVoucher(voucher)}
                            >
                              Lưu mã
                            </Button>
                          )}
                          <Button
                            variant="secondary"
                            size="sm"
                            disabled={exhausted}
                            onClick={() => handleApplyVoucher(voucher)}
                          >
                            Dùng ngay
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </section>
          )}

          {/* Tab 4: Yêu thích */}
          {activeTab === "yeu-thich" && (
            <div className={styles.pane}>
              <section className={styles.pane}>
                <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                  <h2 className={shared.sectionTitle}>Yêu thích</h2>
                  <Link href="/san-pham" className={shared.breadcrumbLink}>
                    Xem thêm
                  </Link>
                </div>

                {wishlistedProducts.length === 0 ? (
                  <StateView
                    kind="empty"
                    title="Chưa có sản phẩm yêu thích."
                    actionLabel="Xem sản phẩm"
                    actionHref="/san-pham"
                  />
                ) : (
                  <div className={styles.wishlistGrid}>
                    {wishlistedProducts.map(({ entry, prod }) => {
                      const totalStock = prod.skus.reduce((s, sk) => s + sk.stock, 0);
                      const defaultVariant =
                        prod.skus.find((s) => s.size === "L" && s.stock > 0) ??
                        prod.skus.find((s) => s.stock > 0);
                      const shot = MODEL_SHOTS[prod.id];

                      return (
                        <article key={entry.productId} className={styles.wishlistCard}>
                          <Link
                            href={`/san-pham/${prod.slug}`}
                            className={styles.wishlistThumbLink}
                          >
                            <img
                              src={shot?.src ?? prod.image}
                              alt={prod.name}
                              width={896}
                              height={1200}
                              loading="lazy"
                              className={styles.wishlistImg}
                            />
                          </Link>
                          <div className={styles.wishlistBody}>
                            <Link
                              href={`/san-pham/${prod.slug}`}
                              className={styles.addressRecipient}
                            >
                              {prod.name}
                            </Link>
                            <Price
                              amount={prod.price}
                              originalAmount={prod.originalPrice}
                              showDiscount
                              size="sm"
                            />
                            {totalStock === 0 ? (
                              <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                                Hết hàng
                              </span>
                            ) : totalStock <= 5 ? (
                              <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                Còn <span className="num">{totalStock}</span>
                              </span>
                            ) : null}
                            <div className={shared.formActions}>
                              {defaultVariant && (
                                <Button
                                  variant="ink"
                                  size="sm"
                                  onClick={() => void handleAddWishlistToCart(prod)}
                                >
                                  Thêm ({defaultVariant.size})
                                </Button>
                              )}
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => toggleWishlist(prod.id)}
                              >
                                Bỏ
                              </Button>
                            </div>
                          </div>
                        </article>
                      );
                    })}
                  </div>
                )}
              </section>

              {recentlyViewedProducts.length > 0 && (
                <section className={styles.pane}>
                  <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
                    <h2 className={shared.sectionTitle}>Đã xem gần đây</h2>
                    <Button variant="ghost" size="sm" onClick={clearRecentlyViewed}>
                      Xóa lịch sử
                    </Button>
                  </div>
                  <div className={styles.productGrid} data-count={recentlyViewedProducts.length}>
                    {recentlyViewedProducts.map((product) => (
                      <ProductCard key={product.id} product={product} />
                    ))}
                  </div>
                </section>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Slide-over sheet for adding or editing an address */}
      {addressSheetMode !== "closed" && (
        <div
          className={shared.sheetBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              closeAddressSheet();
            }
          }}
        >
          <div
            ref={addressSheetRef}
            tabIndex={-1}
            className={shared.sheetPanel}
            role="dialog"
            aria-modal="true"
            aria-labelledby="address-sheet-title"
          >
            <div className={shared.sheetHeader}>
              <h2 id="address-sheet-title" className={shared.sheetTitle}>
                {addressSheetMode === "edit" ? "Sửa địa chỉ" : "Thêm địa chỉ"}
              </h2>
              <button
                type="button"
                className={shared.sheetCloseBtn}
                aria-label="Đóng biểu mẫu địa chỉ"
                onClick={closeAddressSheet}
              >
                <IconClose size={18} />
              </button>
            </div>

            <div className={shared.sheetBody}>
              <AccountAddressForm
                key={editingAddress?.id ?? "new"}
                defaultRecipientName={profile.fullName}
                defaultPhone={profile.phone}
                provinces={provinces}
                initialProvinceCode={initialProvinceCode}
                initialWards={initialWards}
                editingAddress={editingAddress}
                onSaved={(saved) => {
                  setAddresses((prev) => {
                    const withoutCurrent = prev
                      .filter((a) => a.id !== saved.id)
                      .map((a) => (saved.isDefault ? { ...a, isDefault: false } : a));
                    return [saved, ...withoutCurrent];
                  });
                  setAddressSheetMode("closed");
                  setEditingAddress(null);
                  showToast(
                    addressSheetMode === "edit"
                      ? "Đã cập nhật địa chỉ."
                      : `Đã lưu địa chỉ tại ${saved.wardName}, ${saved.provinceName}.`
                  );
                  router.refresh();
                }}
                onCancel={() => {
                  setAddressSheetMode("closed");
                  setEditingAddress(null);
                }}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export function AccountClientView(props: AccountClientViewProps) {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView
            kind="loading"
            skeletonVariant="detail"
            title="Đang tải tài khoản..."
            rows={4}
          />
        </div>
      }
    >
      <AccountClientInner {...props} />
    </Suspense>
  );
}
