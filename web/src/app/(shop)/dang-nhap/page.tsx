"use client";

import React, { Suspense, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { PRODUCTS } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { ApiError, api } from "@/lib/api";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
import { safeNextPath } from "@/features/account/accountTypes";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

const MEMBER_PERKS = [
  {
    title: "Giao nhanh 34 tỉnh thành",
    detail: "Miễn phí vận chuyển toàn sàn cho đơn từ 249.000₫, theo dõi hành trình trực tiếp.",
  },
  {
    title: "Đổi trả tận nơi 45 ngày",
    detail: "Đổi size hai chiều miễn phí tại nhà, hoàn tiền nhanh vào Thứ Hai và Thứ Năm.",
  },
  {
    title: "Hoàn 1–6% Xu mọi đơn hàng",
    detail: "Nhân đôi Xu vào mỗi Thứ Sáu và dùng trừ trực tiếp khi thanh toán tại cả 4 xưởng.",
  },
];

function LoginPageContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const viewState = usePageStateParam();
  const { user, isAuthenticated, setUser, logout } = useSession();
  const { refreshCart, showToast } = useStore();

  const nextParam = searchParams.get("next");
  const nextTarget = safeNextPath(nextParam, "/tai-khoan");
  const prefilledEmail = searchParams.get("email") ?? "";
  const verifiedNotice = searchParams.get("verified") === "1";

  const [email, setEmail] = useState(prefilledEmail);
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [needsVerificationEmail, setNeedsVerificationEmail] = useState<string | null>(null);

  useRevealOnScroll(`${viewState ?? "live"}-${isAuthenticated}`);

  const showcaseProducts = useMemo(
    () =>
      PRODUCTS.filter((p) =>
        ["prod-at-trang", "prod-polo-xanhda", "prod-short-daily", "prod-jogger-terry"].includes(
          p.id
        )
      ),
    []
  );

  const registerHref = nextParam ? `/dang-ky?next=${encodeURIComponent(nextTarget)}` : "/dang-ky";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(cleanEmail)) {
      setError("Vui lòng nhập địa chỉ email hợp lệ (ví dụ: tenban@nexusmart.vn).");
      return;
    }
    if (password.length < 6) {
      setError("Mật khẩu cần có tối thiểu 6 ký tự.");
      return;
    }

    setError(null);
    setNeedsVerificationEmail(null);
    setSubmitting(true);
    try {
      const res = await api<{ user: SessionUser }>("/api/auth/login", {
        method: "POST",
        body: { email: cleanEmail, password },
      });
      setUser(res.user);
      void refreshCart();
      showToast(`Chào mừng ${res.user.fullName || res.user.email} trở lại NexusMart.`);
      router.push(nextTarget);
      router.refresh();
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
        if (err.code === "FAILED_PRECONDITION") {
          setNeedsVerificationEmail(cleanEmail);
        }
      } else {
        setError("Hệ thống đăng nhập đang bận. Bạn vui lòng thử lại sau.");
      }
    } finally {
      setSubmitting(false);
    }
  };

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Đăng nhập</h1>
            <p className={styles.authLead}>Đang kiểm tra trạng thái đăng nhập của bạn.</p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Xác thực tài khoản</h2>
          <StateView kind="loading" title="Đang đăng nhập NexusMart..." rows={3} />
        </section>
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Đăng nhập</h1>
            <p className={styles.authLead}>Dịch vụ đăng nhập đang tạm gián đoạn.</p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái đăng nhập</h2>
          <StateView
            kind="error"
            title="Chưa thể kết nối lúc này"
            message="Vui lòng thử lại sau ít phút hoặc quay về trang chủ tiếp tục xem sản phẩm."
            actionLabel="Thử lại"
            actionHref="/dang-nhap"
          />
        </section>
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Đăng nhập</h1>
            <p className={styles.authLead}>Chưa có phiên đăng nhập nào đang mở.</p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Tài khoản thành viên</h2>
          <StateView
            kind="empty"
            title="Chưa có phiên đăng nhập gần đây"
            message="Tạo tài khoản mới hoặc mở lại biểu mẫu đăng nhập để tiếp tục mua sắm."
            actionLabel="Mở biểu mẫu đăng nhập"
            actionHref="/dang-nhap"
          />
        </section>
      </div>
    );
  }

  return (
    <div className={shared.page}>
      <section className={shared.section}>
        <div className={styles.authGrid}>
          {/* Left column: heading + primary form immediately above the fold */}
          <div className={styles.formCol}>
            <div className={styles.authHeader}>
              <h1 className={`${shared.pageTitle} lines`}>
                <span className="line">
                  <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                    Đăng nhập tài khoản
                  </span>
                </span>
              </h1>
              <p
                className={styles.authLead}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                Quản lý đơn hàng, địa chỉ nhận hàng và ví Xu Hội viên Nexus trên cùng một tài khoản.
              </p>
            </div>

            {verifiedNotice && (
              <div className={styles.sessionBar} role="status">
                <p className={styles.bodyText}>
                  Tài khoản đã được xác thực thành công. Nhập mật khẩu bên dưới để đăng nhập.
                </p>
              </div>
            )}

            {isAuthenticated && user && (
              <div
                className={styles.sessionBar}
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <p className={styles.bodyText}>
                  Đang đăng nhập: <strong>{user.fullName || user.email}</strong> ({user.email}).
                </p>
                <div className={shared.formActions}>
                  <Button variant="ink" size="sm" href={nextTarget}>
                    Tiếp tục
                  </Button>
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    onClick={() => void logout("/dang-nhap")}
                  >
                    Đăng xuất
                  </Button>
                </div>
              </div>
            )}

            <form
              className={styles.authPanel}
              onSubmit={handleSubmit}
              noValidate
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div
                className={shared.field}
                data-rise=""
                style={{ "--i": 3 } as React.CSSProperties}
              >
                <label htmlFor="login-email" className={shared.fieldLabel}>
                  Địa chỉ email
                </label>
                <input
                  id="login-email"
                  type="email"
                  autoComplete="email"
                  className={shared.input}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="tenban@nexusmart.vn"
                  disabled={submitting}
                  required
                />
              </div>

              <div
                className={shared.field}
                data-rise=""
                style={{ "--i": 4 } as React.CSSProperties}
              >
                <label htmlFor="login-password" className={shared.fieldLabel}>
                  Mật khẩu
                </label>
                <div className={styles.passwordWrap}>
                  <input
                    id="login-password"
                    type={showPassword ? "text" : "password"}
                    autoComplete="current-password"
                    className={shared.input}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="Nhập mật khẩu tài khoản"
                    disabled={submitting}
                    required
                  />
                  <button
                    type="button"
                    className={styles.toggleBtn}
                    onClick={() => setShowPassword((v) => !v)}
                  >
                    {showPassword ? "Ẩn" : "Hiện"}
                  </button>
                </div>
              </div>

              {error && (
                <div role="alert" className={styles.errorStack}>
                  <p className={shared.fieldError}>{error}</p>
                  {needsVerificationEmail && (
                    <Link
                      href={`/dang-ky?step=otp&email=${encodeURIComponent(needsVerificationEmail)}${
                        nextParam ? `&next=${encodeURIComponent(nextTarget)}` : ""
                      }`}
                      className={styles.switchLink}
                    >
                      Nhập mã xác thực email ngay
                    </Link>
                  )}
                </div>
              )}

              <div data-rise="" style={{ "--i": 5 } as React.CSSProperties}>
                <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
                  {submitting ? "Đang đăng nhập..." : "Đăng nhập vào NexusMart"}
                </Button>
              </div>

              <p
                className={styles.switchLine}
                data-rise=""
                style={{ "--i": 6 } as React.CSSProperties}
              >
                Chưa có tài khoản NexusMart?{" "}
                <Link href={registerHref} className={styles.switchLink}>
                  Tạo tài khoản mới
                </Link>{" "}
                ·{" "}
                <Link href="/tra-cuu-don-hang" className={styles.switchLink}>
                  Tra cứu đơn hàng
                </Link>
              </p>
            </form>

            <div
              className={styles.quickAccess}
              data-rise=""
              style={{ "--i": 7 } as React.CSSProperties}
            >
              <p className={styles.quickTitle}>An tâm mua sắm trực tiếp từ xưởng</p>
              <p className={styles.bodyText}>
                Đơn hàng, địa chỉ giao nhận và giỏ hàng được đồng bộ an toàn giữa các thiết bị khi
                bạn đăng nhập.
              </p>
            </div>
          </div>

          {/* Right column: dark member perks band + 4 real editorial lookbook frames */}
          <div className={styles.showcaseCol}>
            <div
              className={styles.perksBand}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <h2 className={styles.perksHeading}>Quyền lợi thành viên Nexus</h2>
              <ul className={styles.perkList}>
                {MEMBER_PERKS.map((item, idx) => (
                  <li
                    key={item.title}
                    className={styles.perkRow}
                    data-rise=""
                    style={{ "--i": idx + 4 } as React.CSSProperties}
                  >
                    <span className={styles.perkTitle}>{item.title}</span>
                    <p className={styles.perkDesc}>{item.detail}</p>
                  </li>
                ))}
              </ul>
            </div>

            <div className={styles.photoGrid}>
              {showcaseProducts.map((product, idx) => {
                const shot = MODEL_SHOTS[product.id];
                return (
                  <Link
                    key={product.id}
                    href={`/san-pham/${product.slug}`}
                    className={styles.photoLink}
                    data-rise=""
                    style={{ "--i": idx + 7 } as React.CSSProperties}
                  >
                    <Image
                      src={shot?.src ?? product.image}
                      alt={shot?.alt ?? product.name}
                      width={shot?.width ?? 896}
                      height={shot?.height ?? 1200}
                      sizes="(max-width: 640px) 50vw, (max-width: 960px) 25vw, 13vw"
                      priority
                      className={styles.photoImg}
                    />
                    <span className={styles.photoCaption}>{product.shortName}</span>
                  </Link>
                );
              })}
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang mở trang đăng nhập..." rows={3} />
        </div>
      }
    >
      <LoginPageContent />
    </Suspense>
  );
}
