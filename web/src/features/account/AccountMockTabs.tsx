"use client";

// Các khu vực tính năng chưa có backend (hạng hội viên, hồ sơ số đo, yêu thích, thẻ quà tặng, giới thiệu bạn bè)
// đọc riêng từ useMockStore() và dữ liệu mẫu, tách biệt hoàn toàn với dữ liệu tài khoản thật.

import React, { useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { REFERRAL_CONFIG, SAMPLE_DATA_NOTICE, SHOPS, getProductById, getShopById } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { BODY_SHAPE_LABELS, recommendSize, type BodyShape, type FitPreference } from "@/lib/sizing";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, Price, ProductCard, StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/tai-khoan/page.module.css";

const FIT_PREF_LABELS: Record<FitPreference, string> = {
  om: "Ôm gọn",
  vua: "Vừa vặn chuẩn",
  rong: "Thoải mái / Rộng nhẹ",
};

export function AccountMockOverviewPanels() {
  const router = useRouter();
  const { membership, membershipTier, updateBirthday, claimBirthdayGift, showToast } =
    useMockStore();
  const [birthdayDraft, setBirthdayDraft] = useState(membership.birthday || "1996-11-24");

  const handleCopyVipCode = async () => {
    try {
      await navigator.clipboard.writeText("NEXUSVIP12");
      showToast("Đã sao chép mã NEXUSVIP12.");
    } catch {
      showToast("Mã tri ân của bạn: NEXUSVIP12.");
    }
  };

  return (
    <>
      <div className={styles.birthdayBand} data-reveal="">
        <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
          <h2 className={shared.sectionTitle}>Ưu đãi sinh nhật Hội viên</h2>
          <span className={`${shared.statusTag} ${shared.tonePos}`}>
            Hạng {membershipTier.name}
          </span>
        </div>
        <p className={styles.bodyCopy}>
          Cập nhật ngày sinh để nhận quà sinh nhật Hội viên Nexus cùng mã giảm{" "}
          <strong>SINHNHAT50</strong> (50.000₫).
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
            <Button variant="secondary" size="md" onClick={() => updateBirthday(birthdayDraft)}>
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
              router.push("/gio-hang");
            }}
          >
            {membership.birthdayGiftClaimed
              ? "Đã nhận quà sinh nhật"
              : "Nhận quà sinh nhật vào giỏ"}
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
          Chất liệu chọn nhiều nhất: <strong>{membership.yearInReview.favoriteFabric}</strong> ·
          Xưởng yêu thích: <strong>{membership.yearInReview.favoriteShopName}</strong>.
        </p>

        <div className={styles.yirPromoBar}>
          <p className={styles.yirPromoText}>
            Mã tri ân Tổng kết năm: <strong>NEXUSVIP12</strong> (giảm 12% tối đa 90.000₫ cho hạng
            Vàng trở lên).
          </p>
          <Button type="button" variant="ink" size="sm" onClick={() => void handleCopyVipCode()}>
            Sao chép mã
          </Button>
        </div>
      </div>
    </>
  );
}

export function AccountMockBodyProfileTab() {
  const { bodyProfile, updateBodyProfile, openAi } = useMockStore();
  const [heightCm, setHeightCm] = useState(bodyProfile.heightCm);
  const [weightKg, setWeightKg] = useState(bodyProfile.weightKg);
  const [shape, setShape] = useState<BodyShape>(bodyProfile.shape);
  const [preferredFit, setPreferredFit] = useState<FitPreference>(
    bodyProfile.preferredFit ?? "vua"
  );

  const shopRecommendations = useMemo(
    () =>
      SHOPS.map((shop) => ({
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
    [heightCm, weightKg, shape, preferredFit]
  );

  const handleSaveBodyProfile = (e: React.FormEvent) => {
    e.preventDefault();
    const safeH = Math.max(145, Math.min(205, Math.round(heightCm)));
    const safeW = Math.max(38, Math.min(130, Math.round(weightKg)));
    setHeightCm(safeH);
    setWeightKg(safeW);
    updateBodyProfile({ heightCm: safeH, weightKg: safeW, shape, preferredFit });
  };

  return (
    <div className={styles.colStack}>
      <form className={styles.yirPanel} onSubmit={handleSaveBodyProfile} noValidate>
        <div className={shared.sectionHead}>
          <h2 className={shared.sectionTitle}>Hồ sơ số đo cá nhân</h2>
          <span className={shared.sectionNote}>
            Lưu trên trình duyệt để đối chiếu bảng size cả 4 xưởng dệt
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
            Lưu hồ sơ số đo
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
                  {top.measurements.shoulderCm ? ` · Vai ${top.measurements.shoulderCm} cm` : ""}
                </p>
              )}
              <p className={styles.bodyCopy}>{top.shopSizeNote}</p>
            </article>
          ))}
        </div>
      </div>
    </div>
  );
}

export function AccountMockWishlistTab() {
  const { wishlist, toggleWishlist, recentlyViewed, clearRecentlyViewed } = useMockStore();

  const wishlistedProducts = useMemo(
    () =>
      wishlist
        .map((entry) => {
          const prod = getProductById(entry.productId);
          return prod ? { entry, prod } : null;
        })
        .filter((x): x is NonNullable<typeof x> => x !== null),
    [wishlist]
  );

  const recentlyViewedProducts = useMemo(
    () =>
      recentlyViewed
        .map((id) => getProductById(id))
        .filter((p): p is NonNullable<typeof p> => Boolean(p))
        .slice(0, 4),
    [recentlyViewed]
  );

  return (
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
              const shop = getShopById(prod.shopId);
              const isDiscounted = prod.originalPrice != null && prod.originalPrice > prod.price;
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
                      {shop?.shortName} · {prod.gsm}gsm
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
                      {isDiscounted && (
                        <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                          Đang giảm giá
                        </span>
                      )}
                    </div>
                    <div className={styles.addressActions}>
                      <Button variant="ink" size="sm" href={`/san-pham/${prod.slug}`}>
                        Chọn màu & size
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => toggleWishlist(prod.id)}>
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
  );
}

export function AccountMockGiftCardsPanel() {
  const router = useRouter();
  const { membership, applyGiftCardCode } = useMockStore();

  return (
    <div>
      <div className={shared.sectionHead}>
        <h2 className={shared.sectionTitle}>Thẻ quà tặng & Ưu đãi .edu</h2>
        <Button variant="ink" size="sm" href="/the-qua-tang">
          Phát hành thêm thẻ
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
                  if (!isApplied) router.push("/gio-hang");
                }}
              >
                {isApplied ? "Đang áp dụng" : "Áp vào giỏ"}
              </Button>
            </div>
          );
        })}
      </div>

      <div className={styles.birthdayBand} style={{ marginTop: 20 }}>
        <p className={styles.addressRecipient}>
          Ưu đãi sinh viên (.edu / .edu.vn):{" "}
          {membership.eduVerified ? "Đã xác thực" : "Chưa xác thực"}
        </p>
        <p className={styles.bodyCopy}>
          {membership.eduVerified
            ? `Email ${membership.eduEmail} đã mở khóa mã EDU15 giảm 15% (tối đa 50.000₫).`
            : "Xác thực email sinh viên để nhận ngay mã EDU15 giảm 15% vào ví ưu đãi."}
        </p>
        <div className={shared.formActions}>
          <Button variant="primary" size="sm" href="/the-qua-tang">
            {membership.eduVerified ? "Quản lý ưu đãi .edu" : "Xác thực email .edu ngay"}
          </Button>
        </div>
      </div>
    </div>
  );
}

export function AccountMockReferralTab() {
  const { membership, inviteFriendMock, showToast } = useMockStore();
  const [friendName, setFriendName] = useState("");

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

  return (
    <div className={styles.accountGrid}>
      <div className={styles.colStack}>
        <div className={styles.referralBanner}>
          <span className={styles.xuCaption}>Mã mời cá nhân của bạn</span>
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
          <h2 className={shared.sectionTitle}>Lịch sử giới thiệu bạn bè</h2>
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
  );
}
