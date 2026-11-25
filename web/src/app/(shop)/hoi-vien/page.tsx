"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import Image from "next/image";
import { MEMBERSHIP_TIERS, PRODUCTS, REFERRAL_CONFIG, SAMPLE_DATA_NOTICE, XU_POLICY } from "@/data";
import type { MembershipTierId } from "@/data";
import { MEDIA_BANNERS } from "@/data/media";
import { api } from "@/lib/api";
import type { GatewayCoinBalance } from "@/lib/gateway-types";
import { formatDateVi, formatInt, formatVnd } from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, ProductCard, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

function MembershipPageContent() {
  const viewState = usePageStateParam();
  const { user, isAuthenticated } = useSession();
  const {
    membership,
    membershipTier,
    updateBirthday,
    claimBirthdayGift,
    inviteFriendMock,
    showToast,
  } = useMockStore();
  const [realCoinBalance, setRealCoinBalance] = useState<number | null>(null);

  useEffect(() => {
    if (!isAuthenticated) {
      setRealCoinBalance(null);
      return;
    }
    let active = true;
    void api<GatewayCoinBalance>("/coins/balance")
      .then((res) => {
        if (active && typeof res?.balance === "number") {
          setRealCoinBalance(Math.max(0, Math.trunc(res.balance)));
        }
      })
      .catch(() => {
        if (active) setRealCoinBalance(0);
      });
    return () => {
      active = false;
    };
  }, [isAuthenticated]);

  const [birthdayDraft, setBirthdayDraft] = useState(membership.birthday || "1996-11-24");
  const [friendName, setFriendName] = useState("");
  const [selectedTierId, setSelectedTierId] = useState<MembershipTierId>(membershipTier.id);

  useRevealOnScroll(
    `${viewState ?? "live"}-${membershipTier.id}-${membership.referrals.length}-${membership.xuHistory.length}`
  );

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

  return (
    <div className={shared.page}>
      {renderHeader(
        "Bản xem trước, dữ liệu mẫu cho chương trình phân hạng Hội viên Nexus. Số dư Xu của tài khoản đăng nhập được đọc trực tiếp từ hệ thống."
      )}

      <section className={shared.section}>
        <p className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="note">
          <strong>Bản xem trước, dữ liệu mẫu:</strong> Hệ thống phân hạng 4 quý, quà sinh nhật và
          giới thiệu bạn bè trên trang này đang dùng dữ liệu minh họa, không phản ánh chi tiêu thật
          của tài khoản. Số dư Xu của bạn được lấy trực tiếp từ ví Xu NexusMart.
        </p>
      </section>

      {/* Section 1: Real Xu Balance Card + Primary White Editorial Xu Policy */}
      <section className={shared.section}>
        <div className={styles.twoColGrid}>
          <div
            className={styles.tierStatusPanel}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.tierTopRow}>
              <div>
                <h2 className={styles.tierNameBig}>Ví Xu NexusMart</h2>
                <p className={styles.memberSub}>
                  {isAuthenticated && user
                    ? `Tài khoản: ${user.full_name}`
                    : "Đăng nhập để xem số dư Xu thật của tài khoản"}
                </p>
              </div>
              <span className={styles.tierRateBadge}>1 Xu = 1₫</span>
            </div>

            <div className={styles.xuStatsRow}>
              <div
                className={styles.xuStatItem}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <span className={`${styles.xuStatBig} num`}>
                  {isAuthenticated ? `${formatInt(realCoinBalance ?? 0)} Xu` : "0 Xu"}
                </span>
                <span className={styles.xuStatLabel}>
                  {isAuthenticated ? "Số dư Xu thật khả dụng" : "Chưa đăng nhập"}
                </span>
              </div>
              <div
                className={styles.xuStatItem}
                data-rise=""
                style={{ "--i": 4 } as React.CSSProperties}
              >
                <span className={`${styles.xuStatMed} num`}>Chưa mở</span>
                <span className={styles.xuStatLabel}>Phân hạng 4 quý (xem trước)</span>
              </div>
            </div>

            <div className={styles.retentionStrip}>
              <p className={styles.retentionCopy}>
                Số dư Xu khả dụng có thể áp dụng trực tiếp tại giỏ hàng và màn hình thanh toán. Các
                bảng hạng thành viên và lịch sử mẫu bên dưới chỉ phục vụ xem trước giao diện.
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
                Ngưỡng xét hạng dự kiến từ{" "}
                <strong className="num">{formatVnd(selectedTier.minSpend4Q)}</strong> / 4 quý, nhân
                đôi vào Thứ Sáu lên{" "}
                <strong className="num">{Math.round(selectedTier.cashbackRate * 200)}%</strong> (dữ
                liệu minh họa).
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
              Bản xem trước minh họa quyền lợi quà sinh nhật cho Hội viên từ hạng{" "}
              <strong>Bạc</strong> trở lên đã cập nhật ngày sinh (chức năng hiện chưa phát hành mã
              giảm giá thanh toán trên sàn).
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
                variant="secondary"
                size="md"
                disabled={membership.birthdayGiftClaimed}
                onClick={() => claimBirthdayGift()}
              >
                {membership.birthdayGiftClaimed
                  ? "Đã xem thử ưu đãi sinh nhật"
                  : "Xem thử ưu đãi sinh nhật"}
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
              <Button
                variant="secondary"
                size="sm"
                className={shared.wrapBtn}
                href="/chien-dich/soi-xanh-nexus"
              >
                Xem chiến dịch Sợi Xanh
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
                              ? "Chờ hết hạn 7 ngày"
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
          <StateView
            kind="loading"
            title="Đang tải chương trình Hội viên Nexus (Bản xem trước, dữ liệu mẫu)..."
            rows={4}
          />
        </div>
      }
    >
      <MembershipPageContent />
    </Suspense>
  );
}
