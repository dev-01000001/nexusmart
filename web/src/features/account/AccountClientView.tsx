"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ORDER_STATUS_META, type StatusTone } from "@/data/orders";
import type { Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { MODEL_SHOTS } from "@/data/media";
import { ApiError, api } from "@/lib/api";
import { getProvinceByCode } from "@/lib/provinces";
import { formatDateVi, formatVnd } from "@/lib/format";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { Button, Price, ProductCard, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
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
  { key: "tong-quan", label: "Hồ sơ & Đơn gần đây" },
  { key: "dia-chi", label: "Sổ địa chỉ nhận hàng" },
  { key: "voucher", label: "Mã giảm giá cửa hàng" },
  { key: "yeu-thich", label: "Sản phẩm yêu thích" },
];

const VALID_TABS = new Set<AccountTabKey>(ACCOUNT_TABS.map((t) => t.key));

function toneClass(tone: StatusTone): string {
  if (tone === "pos") return shared.tonePos;
  if (tone === "warn") return shared.toneWarn;
  if (tone === "neg") return shared.toneNeg;
  return shared.toneNeutral;
}

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
  suggestedProducts: Product[];
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
  const [showAddAddress, setShowAddAddress] = useState(false);
  const [busyAddressId, setBusyAddressId] = useState<string | null>(null);
  const [addressError, setAddressError] = useState<string | null>(null);

  const [vouchers, setVouchers] = useState<Voucher[]>(
    initialVouchers.filter((v) => v.scope === "shop")
  );
  const [savingVoucherCode, setSavingVoucherCode] = useState<string | null>(null);

  useRevealOnScroll(
    `${viewState ?? "live"}-${activeTab}-${addresses.length}-${wishlist.length}-${initialOrders.length}`
  );

  const productById = useMemo(() => {
    const map = new Map<string, Product>();
    for (const p of catalogProducts) {
      map.set(p.id, p);
      if (p.backendId) map.set(p.backendId, p);
    }
    return map;
  }, [catalogProducts]);

  const inProgressOrders = useMemo(
    () =>
      initialOrders.filter(
        (o) =>
          o.status === "cho_xac_nhan" || o.status === "dang_chuan_bi" || o.status === "dang_giao"
      ).length,
    [initialOrders]
  );

  const recentOrders = useMemo(() => initialOrders.slice(0, 5), [initialOrders]);

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
    setSelectedVoucher(voucher.id ?? voucher.code);
    showToast(`Đã chọn mã ${voucher.code} cho giỏ hàng tại ${brandName}.`);
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

  const renderSuggestedShelf = (title: string, subtitle?: string) => {
    if (suggestedProducts.length === 0) return null;
    return (
      <section className={`${shared.section} ${shared.sectionGap}`} data-reveal="">
        <div className={shared.sectionHead}>
          <h2 className={shared.sectionTitle}>{title}</h2>
          {subtitle && <p className={styles.shelfSub}>{subtitle}</p>}
        </div>
        <div className={styles.productGrid}>
          {suggestedProducts.map((product) => (
            <div key={product.id} data-reveal="">
              <ProductCard product={product} />
            </div>
          ))}
        </div>
      </section>
    );
  };

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={shared.headerLeadGroup}>
            <h1 className={shared.pageTitle}>Tài khoản của tôi</h1>
            <p className={styles.leadCopy}>
              Đang tải thông tin tài khoản, sổ địa chỉ và đơn hàng gần đây tại {brandName}.
            </p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Hồ sơ người mua</h2>
          <StateView kind="loading" title="Đang đồng bộ tài khoản..." rows={4} />
        </section>
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
                Tài khoản · {brandName}
              </span>
            </span>
          </h1>
          <p className={styles.leadCopy} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {profile.fullName || profile.email} · Quản lý hồ sơ, sổ địa chỉ nhận hàng và theo dõi
            đơn hàng đã đặt tại {brandName}.
          </p>
        </div>

        <div
          className={shared.headerAside}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <Button variant="secondary" size="md" href="/don-hang">
            Đơn hàng ({totalOrders})
          </Button>
          <Button variant="danger" size="md" onClick={() => void logout("/dang-nhap")}>
            Đăng xuất
          </Button>
        </div>
      </section>

      {/* Account activity summary at THIS shop */}
      <section className={shared.section}>
        <h2 className="sr-only">Tổng quan tài khoản tại {brandName}</h2>
        <div
          className={styles.activityCard}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.activityStats}>
            <div className={styles.activityItem}>
              <span className={`${styles.activityNum} num`}>{totalOrders}</span>
              <span className={styles.activityLabel}>
                Đơn tại {brandName} ({inProgressOrders} đang xử lý)
              </span>
            </div>
            <div className={styles.activityItem}>
              <span className={`${styles.activityNum} num`}>{addresses.length}</span>
              <span className={styles.activityLabel}>Địa chỉ nhận hàng đã lưu</span>
            </div>
            <div className={styles.activityItem}>
              <span className={`${styles.activityNum} num`}>{vouchers.length}</span>
              <span className={styles.activityLabel}>Mã ưu đãi đang mở</span>
            </div>
          </div>
        </div>
      </section>

      <section className={shared.section} style={{ marginTop: 28 }}>
        <div
          className={styles.tabBar}
          role="tablist"
          aria-label="Danh mục quản lý tài khoản"
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          {ACCOUNT_TABS.map((tab) => {
            const active = activeTab === tab.key;
            const badge =
              tab.key === "dia-chi"
                ? addresses.length
                : tab.key === "yeu-thich"
                  ? wishlistedProducts.length
                  : tab.key === "voucher"
                    ? vouchers.length
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

        {/* TAB 1: Profile & Recent Orders at THIS shop */}
        {activeTab === "tong-quan" && (
          <div className={styles.accountGrid}>
            <div className={styles.colStack}>
              <div className={styles.unboxedSection} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Thông tin tài khoản</h2>
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
            </div>

            <div className={styles.colStack}>
              <div className={styles.unboxedSectionLast} data-reveal="">
                <div className={shared.sectionHead}>
                  <h2 className={shared.sectionTitle}>Đơn hàng gần đây tại {brandName}</h2>
                  <Link href="/don-hang" className={shared.breadcrumbLink}>
                    Xem tất cả ({totalOrders})
                  </Link>
                </div>

                {recentOrders.length === 0 ? (
                  <StateView
                    kind="empty"
                    title={`Bạn chưa có đơn hàng nào tại ${brandName}`}
                    message={`Các đơn bạn đặt tại ${brandName} sẽ hiển thị tại đây để tiện theo dõi.`}
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
                              <span className="num">{order.items.length}</span> sản phẩm
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
          </div>
        )}

        {/* TAB 2: Address Book (real backend CRUD) */}
        {activeTab === "dia-chi" && (
          <div className={styles.colStack}>
            <div className={shared.sectionHead}>
              <h2 className={shared.sectionTitle}>Sổ địa chỉ nhận hàng</h2>
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
                        Giao dự kiến {previewProv?.etaDays ?? "1–3 ngày"}
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

        {/* TAB 3: Shop Vouchers of THIS shop only */}
        {activeTab === "voucher" && (
          <div className={styles.colStack}>
            <div className={shared.sectionHead}>
              <h2 className={shared.sectionTitle}>Mã giảm giá của {brandName}</h2>
              <span className={`${shared.sectionNote} num`}>
                Đã lưu {savedVoucherCount} / {vouchers.length} mã
              </span>
            </div>

            {vouchers.length === 0 ? (
              <StateView
                kind="empty"
                title={`${brandName} hiện chưa có mã ưu đãi khả dụng`}
                message="Quay lại sau hoặc theo dõi trang sản phẩm để cập nhật ưu đãi mới."
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
                            <span className={`${shared.statusTag} ${shared.tonePos}`}>Đã lưu</span>
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
                              · HSD <span className="num">{formatDateVi(voucher.expiresAt)}</span>
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
        )}

        {/* TAB 4: Wishlisted & Recently Viewed at THIS shop */}
        {activeTab === "yeu-thich" && (
          <div className={styles.colStack}>
            <div>
              <div className={shared.sectionHead}>
                <h2 className={shared.sectionTitle}>
                  Sản phẩm yêu thích tại {brandName} ({wishlistedProducts.length})
                </h2>
                <Link href="/san-pham" className={shared.breadcrumbLink}>
                  Xem thêm sản phẩm
                </Link>
              </div>

              {wishlistedProducts.length === 0 ? (
                <StateView
                  kind="empty"
                  title="Chưa có sản phẩm nào trong mục yêu thích"
                  message={`Bấm biểu tượng trái tim trên sản phẩm của ${brandName} để lưu lại tại đây.`}
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
                            {brandName}
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
      </section>

      {renderSuggestedShelf(`Sản phẩm nổi bật tại ${brandName}`, "Sẵn hàng tại kho của cửa hàng")}
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
