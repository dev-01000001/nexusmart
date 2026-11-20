"use client";

import React, { Suspense, useMemo, useState } from "react";
import Image from "next/image";
import { useRouter } from "next/navigation";
import {
  MEMBERSHIP_TIERS,
  PRODUCTS,
  REFERRAL_CONFIG,
  SAMPLE_DATA_NOTICE,
  XU_POLICY,
  getNextTierInfo,
} from "@/data";
import type { MembershipTierId } from "@/data";
import { MEDIA_BANNERS } from "@/data/media";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, ProductCard, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

function MembershipPageContent() {
  const router = useRouter();
  const viewState = usePageStateParam();
  const {
    user,
    membership,
    membershipTier,
    updateBirthday,
    claimBirthdayGift,
    inviteFriendMock,
    saveVoucherCode,
    showToast,
  } = useStore();

  const [birthdayDraft, setBirthdayDraft] = useState(membership.birthday || "1996-11-24");
  const [friendName, setFriendName] = useState("");
  const [selectedTierId, setSelectedTierId] = useState<MembershipTierId>(membershipTier.id);

  useRevealOnScroll(
    `${viewState ?? "live"}-${membershipTier.id}-${membership.referrals.length}-${membership.xuHistory.length}`
  );

  const nextTier = useMemo(() => getNextTierInfo(membership.spend4Q), [membership.spend4Q]);
  const suggestedProducts = useMemo(() => PRODUCTS.slice(0, 4), []);

  const selectedTier = useMemo(
    () => MEMBERSHIP_TIERS.find((t) => t.id === selectedTierId) ?? membershipTier,
    [selectedTierId, membershipTier]
  );

  const handleCopyInvite = async () => {
    const link = `https://nexusmart.vn/dang-ky?ref=${membership.referralCode}`;
    try {
      await navigator.clipboard.writeText(link);
      showToast(`Đã sao chép liên kết mời: ${link}`);
    } catch {
      showToast(`Mã mời của bạn: ${membership.referralCode}`);
    }
  };

  const handleInviteFriend = (e: React.FormEvent) => {
    e.preventDefault();
    const target = friendName.trim() || "Trần Quốc Khánh";
    inviteFriendMock(target);
    setFriendName("");
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Hội viên Nexus & Hoàn Xu
            </span>
          </span>
        </h1>
        <p className={styles.leadCopy} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
    </section>
  );

  const renderSuggestedShelf = (title: string, subtitle?: string) => (
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

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật thứ hạng 4 quý, số dư Xu và quyền lợi Hội viên Nexus.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Hạng thành viên hiện tại</h2>
          <StateView kind="loading" title="Đang tải dữ liệu Hội viên Nexus..." rows={4} />
        </section>
        {renderSuggestedShelf("Sản phẩm cơ bản tích Xu")}
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        {renderHeader("Lịch sử tích Xu và thông tin hạng thành viên đang tạm gián đoạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái kết nối</h2>
          <StateView
            kind="error"
            title="Chưa thể đồng bộ số dư Xu"
            message="Số dư Xu và lịch sử tích lũy của bạn vẫn an toàn. Vui lòng thử tải lại trang."
            actionLabel="Tải lại trang"
            actionHref="/hoi-vien"
          />
        </section>
        {renderSuggestedShelf("Sản phẩm cơ bản nổi bật")}
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        {renderHeader("Đăng ký tài khoản NexusMart để bắt đầu tích Xu trên toàn bộ 4 xưởng dệt.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Chưa có lịch sử Hội viên</h2>
          <StateView
            kind="empty"
            title="Bắt đầu hành trình Hội viên Nexus"
            message="Đặt đơn hàng đầu tiên để nhận hoàn 1% đến 6% Xu (1 Xu = 1₫), nhân đôi vào Thứ Sáu."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        {renderSuggestedShelf("Đồ cơ bản bán chạy")}
      </div>
    );
  }

  const spendDeltaToSelected = Math.max(0, selectedTier.minSpend4Q - membership.spend4Q);

  return (
    <div className={shared.page}>
      {renderHeader(
        "Hoàn 1–6% Xu cho mỗi đơn hoàn tất, nhân đôi vào Thứ Sáu và dùng trừ trực tiếp trên cả 4 xưởng dệt."
      )}

      {/* Section 1: Dark Membership Status Card + Primary White Editorial Xu Policy */}
      <section className={shared.section}>
        <div className={styles.twoColGrid}>
          <div
            className={styles.tierStatusPanel}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.tierTopRow}>
              <div>
                <h2 className={styles.tierNameBig}>Hạng {membershipTier.name}</h2>
                <p className={styles.memberSub}>Hội viên Nexus · {user.fullName}</p>
              </div>
              <span className={styles.tierRateBadge}>
                Hoàn {membershipTier.cashbackPercentLabel} Xu · Thứ Sáu x2
              </span>
            </div>

            <div className={styles.darkProgressBlock}>
              <div className={styles.darkProgressMeta}>
                <span>
                  Chi tiêu 4 quý: <strong className="num">{formatVnd(membership.spend4Q)}</strong>
                </span>
                <span className="num">
                  {nextTier.next
                    ? `Còn ${formatVnd(nextTier.remainingVnd)} lên hạng ${nextTier.next.name}`
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

            {/* Unboxed prominent Xu figures (zero nested boxes) */}
            <div className={styles.xuStatsRow}>
              <div
                className={styles.xuStatItem}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <span className={`${styles.xuStatBig} num`}>{formatInt(user.coins)} Xu</span>
                <span className={styles.xuStatLabel}>Xu khả dụng (1 Xu = 1₫)</span>
              </div>
              <div
                className={styles.xuStatItem}
                data-rise=""
                style={{ "--i": 4 } as React.CSSProperties}
              >
                <span className={`${styles.xuStatMed} num`}>
                  {formatInt(membership.pendingCoins)} Xu
                </span>
                <span className={styles.xuStatLabel}>Đang chờ duyệt</span>
              </div>
              <div
                className={styles.xuStatItem}
                data-rise=""
                style={{ "--i": 5 } as React.CSSProperties}
              >
                <span className={`${styles.xuStatMed} num`}>
                  {formatInt(membership.expiringCoins)} Xu
                </span>
                <span className={styles.xuStatLabel}>
                  Sắp hết hạn ({membership.expiringDateLabel})
                </span>
              </div>
            </div>

            <div className={styles.retentionStrip}>
              <p className={styles.retentionCopy}>
                <strong>Nhắc giữ hạng:</strong> Chi tiêu thêm{" "}
                <strong className="num">{formatVnd(membership.retentionTargetVnd)}</strong> trước{" "}
                <strong className="num">{membership.retentionDeadline}</strong> để giữ vững quyền
                lợi hạng {membershipTier.name} cho chu kỳ 4 quý kế tiếp.
              </p>
            </div>
          </div>

          <div
            className={styles.policyPanel}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Image
              src={MEDIA_BANNERS.everyday.src}
              alt={MEDIA_BANNERS.everyday.alt}
              width={MEDIA_BANNERS.everyday.width}
              height={MEDIA_BANNERS.everyday.height}
              sizes="(max-width: 1100px) 100vw, 44vw"
              priority
              className={styles.heroPhoto}
            />
            <div className={styles.policyPanelBody}>
              <h2 className={shared.sectionTitle}>Quy tắc tích và dùng Xu</h2>
              <p className={styles.bodyCopy}>{XU_POLICY.summary}</p>
              <p className={styles.bodyCopy}>
                Trừ tối đa {XU_POLICY.maxOrderRatioPercent}% tiền hàng sau voucher trên mỗi đơn. Nếu
                đơn hoàn trả, số Xu hoàn của đơn đó được điều chỉnh lại tương ứng trong ví Xu.
              </p>
              <div className={shared.formActions}>
                <Button variant="primary" size="md" href="/danh-muc">
                  Mua sắm tích Xu
                </Button>
                <Button variant="secondary" size="md" href="/tai-khoan">
                  Quản lý tài khoản
                </Button>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Section 2: Interactive Tier Explorer + Unified 4-Tier Comparison Table */}
      <section className={`${shared.section} ${shared.sectionGap}`} data-reveal="">
        <div className={shared.sectionHead}>
          <h2 className={shared.sectionTitle}>Bảng quyền lợi 4 hạng Hội viên Nexus</h2>
          <span className={shared.sectionNote}>
            Xét hạng tự động theo tổng chi tiêu 4 quý gần nhất
          </span>
        </div>

        {/* Interactive tier spotlight bar */}
        <div className={styles.tierExplorer}>
          <div className={styles.tierTabs} role="tablist" aria-label="Chọn hạng để xem chi tiết">
            {MEMBERSHIP_TIERS.map((tier) => {
              const isSelected = tier.id === selectedTier.id;
              const isCurrent = tier.id === membershipTier.id;
              return (
                <button
                  key={tier.id}
                  type="button"
                  role="tab"
                  aria-selected={isSelected}
                  className={`${styles.tierTabBtn} ${isSelected ? styles.tierTabBtnActive : ""}`}
                  onClick={() => setSelectedTierId(tier.id)}
                >
                  <span>{tier.name}</span>
                  <span className={`${styles.tierTabRate} num`}>
                    {tier.cashbackPercentLabel}
                    {isCurrent ? " · Hiện tại" : ""}
                  </span>
                </button>
              );
            })}
          </div>

          <div className={styles.tierSpotlight} data-reveal="">
            <div className={styles.tierSpotlightMeta}>
              <h3 className={styles.tierSpotlightTitle}>
                Hạng {selectedTier.name} · Hoàn {selectedTier.cashbackPercentLabel} Xu
              </h3>
              <p className={styles.bodyCopy}>
                Ngưỡng xét hạng từ{" "}
                <strong className="num">{formatVnd(selectedTier.minSpend4Q)}</strong> / 4 quý, nhân
                đôi vào Thứ Sáu lên{" "}
                <strong className="num">{Math.round(selectedTier.cashbackRate * 200)}%</strong>.{" "}
                {selectedTier.id === membershipTier.id
                  ? "Đây là hạng hiện tại của bạn."
                  : spendDeltaToSelected > 0
                    ? `Cần thêm ${formatVnd(spendDeltaToSelected)} chi tiêu để đạt hạng này.`
                    : "Bạn đã vượt qua ngưỡng chi tiêu của hạng này."}
              </p>
            </div>
            <ul className={styles.spotlightPerks}>
              {selectedTier.perks.map((perk) => (
                <li key={perk} className={styles.spotlightPerkItem}>
                  {perk}
                </li>
              ))}
            </ul>
          </div>
        </div>

        <div className={`${shared.tableWrap} ${styles.mobileCleanTable}`} data-reveal="">
          <table className={shared.dataTable}>
            <thead>
              <tr>
                <th>Hạng thành viên</th>
                <th>Chi tiêu 4 quý</th>
                <th>Hoàn thường</th>
                <th>Thứ Sáu (x2)</th>
                <th>Quyền lợi nổi bật</th>
              </tr>
            </thead>
            <tbody>
              {MEMBERSHIP_TIERS.map((tier) => {
                const isCurrent = tier.id === membershipTier.id;
                return (
                  <tr key={tier.id} className={isCurrent ? styles.currentTierRow : undefined}>
                    <td data-label="Hạng">
                      <div className={styles.tierTableName}>
                        <strong>{tier.name}</strong>
                        {isCurrent && (
                          <span className={`${shared.statusTag} ${shared.tonePos}`}>Hiện tại</span>
                        )}
                      </div>
                    </td>
                    <td data-label="Chi tiêu 4 quý" className="num">
                      {formatVnd(tier.minSpend4Q)}
                    </td>
                    <td data-label="Hoàn thường" className="num">
                      {tier.cashbackPercentLabel}
                    </td>
                    <td data-label="Thứ Sáu (x2)" className="num">
                      <strong>{Math.round(tier.cashbackRate * 200)}%</strong>
                    </td>
                    <td data-label="Quyền lợi">
                      <ul className={styles.tablePerkList}>
                        {tier.perks.map((perk) => (
                          <li key={perk}>{perk}</li>
                        ))}
                      </ul>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      {/* Section 3: Accent-tint Birthday Perks + Primary White Year in Review (same 1.2fr / 1fr grid) */}
      <section className={`${shared.section} ${shared.sectionGap}`}>
        <div className={styles.twoColGrid}>
          <div className={styles.birthdayBand} data-reveal="">
            <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
              <h2 className={shared.sectionTitle}>Ưu đãi sinh nhật thành viên</h2>
              <span className={`${shared.statusTag} ${shared.tonePos}`}>Hạng Bạc trở lên</span>
            </div>
            <p className={styles.bodyCopy}>
              Hội viên từ hạng <strong>Bạc</strong> trở lên đã cập nhật ngày sinh được nhận tự động
              túi vải rút Sợi Xanh Nexus trong giỏ hàng kèm mã giảm <strong>SINHNHAT50</strong>{" "}
              (50.000₫ cho đơn từ 299.000₫).
            </p>
            <div className={shared.formGrid}>
              <div className={shared.field}>
                <label htmlFor="hv-birthday" className={shared.fieldLabel}>
                  Ngày sinh của bạn
                </label>
                <input
                  id="hv-birthday"
                  type="date"
                  className={shared.input}
                  value={birthdayDraft}
                  onChange={(e) => setBirthdayDraft(e.target.value)}
                />
              </div>
              <div className={shared.field} style={{ justifyContent: "flex-end" }}>
                <Button variant="secondary" size="md" onClick={() => updateBirthday(birthdayDraft)}>
                  Cập nhật ngày sinh
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
                  ? "Đã kích hoạt quà sinh nhật & SINHNHAT50"
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

            {/* Unboxed prominent tabular metrics */}
            <div className={styles.yirMetrics}>
              <div className={styles.yirStat} data-reveal="">
                <span className={`${styles.yirNumber} num`}>
                  {formatVnd(membership.yearInReview.totalSpentVnd)}
                </span>
                <span className={styles.tileLabel}>Tổng chi tiêu</span>
              </div>
              <div className={styles.yirStat} data-reveal="">
                <span className={`${styles.yirNumber} num`}>
                  {membership.yearInReview.totalOrders} đơn
                </span>
                <span className={styles.tileLabel}>Tổng số đơn</span>
              </div>
              <div className={styles.yirStat} data-reveal="">
                <span className={`${styles.yirNumber} num`}>
                  {formatInt(membership.yearInReview.totalCoinsEarned)} Xu
                </span>
                <span className={styles.tileLabel}>Xu đã tích lũy</span>
              </div>
              <div className={styles.yirStat} data-reveal="">
                <span className={`${styles.yirNumber} num`}>
                  {formatVnd(membership.yearInReview.totalSavedVnd)}
                </span>
                <span className={styles.tileLabel}>Tổng tiết kiệm ưu đãi</span>
              </div>
            </div>

            <p className={styles.bodyCopy}>
              Chất liệu yêu thích: <strong>{membership.yearInReview.favoriteFabric}</strong> · Xưởng
              đồng hành nhiều nhất: <strong>{membership.yearInReview.favoriteShopName}</strong>.
            </p>
            <div className={shared.formActions}>
              <Button variant="ink" size="sm" onClick={() => saveVoucherCode("NEXUSVIP12")}>
                Lưu mã tri ân NEXUSVIP12
              </Button>
              <Button
                variant="secondary"
                size="sm"
                className={shared.wrapBtn}
                href="/chien-dich/soi-xanh-nexus"
              >
                Đóng góp Sợi Xanh ({formatVnd(membership.yearInReview.csrContributedVnd)})
              </Button>
            </div>
          </div>
        </div>
      </section>

      {/* Section 4: Unboxed Referral Program + Unboxed Xu History (same 1.2fr / 1fr grid) */}
      <section className={`${shared.section} ${shared.sectionGap}`}>
        <div className={styles.twoColGrid}>
          <div className={styles.unboxedCol} data-reveal="">
            <h2 className={shared.sectionTitle}>Giới thiệu bạn bè · Hoàn 10% Xu</h2>
            <p className={styles.bodyCopy}>{REFERRAL_CONFIG.ruleSummary}</p>

            <div className={styles.refHighlight}>
              <div>
                <p className={`${styles.refCodeBig} num`}>{membership.referralCode}</p>
                <span className={styles.refCodeLabel}>Mã mời cá nhân của bạn</span>
              </div>
              <Button variant="primary" size="md" onClick={handleCopyInvite}>
                Sao chép liên kết mời
              </Button>
            </div>

            <form onSubmit={handleInviteFriend} className={shared.codeInputRow} noValidate>
              <input
                type="text"
                className={shared.input}
                value={friendName}
                onChange={(e) => setFriendName(e.target.value)}
                placeholder="Tên người bạn được mời (ví dụ: Vũ Minh Anh)"
                aria-label="Tên người bạn được mời"
              />
              <Button type="submit" variant="ink" size="md" className={shared.wrapBtn}>
                Gửi lời mời thử
              </Button>
            </form>

            <div className={`${shared.tableWrap} ${styles.mobileCleanTable}`}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Bạn bè</th>
                    <th>Ngày mời</th>
                    <th>Thưởng 10%</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {membership.referrals.map((r) => (
                    <tr key={r.id}>
                      <td data-label="Bạn bè">{r.friendName}</td>
                      <td data-label="Ngày mời" className="num">
                        {formatDateVi(r.invitedAt)}
                      </td>
                      <td data-label="Thưởng 10%" className="num">
                        {r.rewardCoins > 0 ? `+${formatInt(r.rewardCoins)} Xu` : "Chờ đơn"}
                      </td>
                      <td data-label="Trạng thái">
                        <span
                          className={`${shared.statusTag} ${
                            r.status === "da_cong_xu"
                              ? shared.tonePos
                              : r.status === "cho_het_han_45_ngay"
                                ? shared.toneWarn
                                : shared.toneNeutral
                          }`}
                        >
                          {r.status === "da_cong_xu"
                            ? "Đã cộng Xu"
                            : r.status === "cho_het_han_45_ngay"
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

          <div className={styles.unboxedCol} data-reveal="">
            <div className={shared.sectionHead} style={{ marginBottom: 0 }}>
              <h2 className={shared.sectionTitle}>Lịch sử tích & dùng Xu</h2>
              <span className={`${shared.sectionNote} num`}>Hạn dùng 12 tháng</span>
            </div>
            <div className={`${shared.tableWrap} ${styles.mobileCleanTable}`}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Ngày</th>
                    <th>Nội dung</th>
                    <th>Biến động</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {membership.xuHistory.map((row) => (
                    <tr key={row.id}>
                      <td data-label="Ngày" className="num">
                        {formatDateVi(row.date)}
                      </td>
                      <td data-label="Nội dung">{row.title}</td>
                      <td data-label="Biến động" className="num">
                        <strong>
                          {row.deltaCoins > 0
                            ? `+${formatInt(row.deltaCoins)}`
                            : formatInt(row.deltaCoins)}{" "}
                          Xu
                        </strong>
                      </td>
                      <td data-label="Trạng thái">
                        <span
                          className={`${shared.statusTag} ${
                            row.status === "kha_dung"
                              ? shared.tonePos
                              : row.status === "cho_duyet"
                                ? shared.toneWarn
                                : shared.toneNeutral
                          }`}
                        >
                          {row.status === "kha_dung"
                            ? "Khả dụng"
                            : row.status === "cho_duyet"
                              ? "Chờ duyệt"
                              : "Đã dùng"}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </section>

      {renderSuggestedShelf(
        "Mua đồ cơ bản tích Xu nhân đôi Thứ Sáu",
        "Mọi sản phẩm từ 4 xưởng dệt đều tích và dùng chung ví Xu Hội viên Nexus"
      )}
    </div>
  );
}

export default function MembershipPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải chương trình Hội viên Nexus..." rows={4} />
        </div>
      }
    >
      <MembershipPageContent />
    </Suspense>
  );
}
