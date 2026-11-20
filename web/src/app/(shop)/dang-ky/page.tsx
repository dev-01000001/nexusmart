"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
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

const REGISTER_PERKS = [
  {
    title: "Lưu hồ sơ số đo một lần",
    detail: "Đối chiếu bảng size riêng của cả 4 xưởng dệt để chọn đúng cỡ ngay từ đơn đầu tiên.",
  },
  {
    title: "Lưu địa chỉ nhận hàng toàn quốc",
    detail: "Chọn nhanh tỉnh thành, phường xã và xem trước thời gian giao cùng phí vận chuyển.",
  },
  {
    title: "Tích Xu chéo mọi gian hàng",
    detail: "Nhận hoàn 1–6% Xu cho mỗi đơn hoàn tất, nhân đôi vào Thứ Sáu và dùng trừ trực tiếp.",
  },
];

function RegisterPageContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const viewState = usePageStateParam();
  const { setUser } = useSession();
  const { refreshCart, showToast } = useStore();

  const nextParam = searchParams.get("next");
  const nextTarget = safeNextPath(nextParam, "/tai-khoan");
  const initialEmail = searchParams.get("email") ?? "";
  const initialStep = searchParams.get("step") === "otp" && initialEmail ? "otp" : "register";

  const [step, setStep] = useState<"register" | "otp">(initialStep);
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState(initialEmail);
  const [phone, setPhone] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [otp, setOtp] = useState("");

  const [submitting, setSubmitting] = useState(false);
  const [resending, setResending] = useState(false);
  const [resendCooldown, setResendCooldown] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useRevealOnScroll(`${viewState ?? "live"}-${step}`);

  useEffect(() => {
    if (resendCooldown <= 0) return;
    const timer = window.setInterval(() => {
      setResendCooldown((prev) => (prev > 1 ? prev - 1 : 0));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [resendCooldown]);

  const showcaseProducts = useMemo(
    () =>
      PRODUCTS.filter((p) =>
        ["prod-at-dryfit", "prod-polo-kem", "prod-short-linen", "prod-aokhoac-gio"].includes(p.id)
      ),
    []
  );

  const loginHref = nextParam ? `/dang-nhap?next=${encodeURIComponent(nextTarget)}` : "/dang-nhap";

  const handleRegisterSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fullName.trim();
    const cleanEmail = email.trim();
    const cleanPhone = phone.replace(/\s+/g, "").trim();

    if (cleanName.length < 2) {
      setError("Vui lòng nhập họ và tên đầy đủ của bạn.");
      return;
    }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(cleanEmail)) {
      setError("Địa chỉ email chưa đúng định dạng.");
      return;
    }
    if (cleanPhone && !/^0\d{9,10}$/.test(cleanPhone)) {
      setError("Số điện thoại Việt Nam cần có 10 hoặc 11 chữ số, bắt đầu bằng số 0.");
      return;
    }
    if (password.length < 8) {
      setError("Mật khẩu cần tối thiểu 8 ký tự để bảo vệ tài khoản.");
      return;
    }
    if (password !== confirmPassword) {
      setError("Mật khẩu xác nhận chưa khớp với mật khẩu đã nhập.");
      return;
    }

    setError(null);
    setNotice(null);
    setSubmitting(true);
    try {
      const res = await api<{ userId: string; email: string; status: string }>(
        "/api/auth/register",
        {
          method: "POST",
          body: {
            fullName: cleanName,
            email: cleanEmail,
            phone: cleanPhone,
            password,
          },
        }
      );
      setEmail(res.email || cleanEmail);
      setOtp("");
      setStep("otp");
      setResendCooldown(30);
      setNotice("Mã xác thực 6 chữ số đã được gửi tới hộp thư của bạn.");
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Không thể tạo tài khoản lúc này. Bạn vui lòng thử lại sau."
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleVerifyOtpSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    const cleanOtp = otp.replace(/\s+/g, "").trim();

    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(cleanEmail)) {
      setError("Địa chỉ email xác thực chưa hợp lệ.");
      return;
    }
    if (!/^\d{6}$/.test(cleanOtp)) {
      setError("Vui lòng nhập đủ 6 chữ số mã xác thực trong email của bạn.");
      return;
    }

    setError(null);
    setNotice(null);
    setSubmitting(true);
    try {
      await api<{ verified: boolean; user: SessionUser | null }>("/api/auth/verify-otp", {
        method: "POST",
        body: { email: cleanEmail, otp: cleanOtp },
      });

      // Auto-login right after OTP verification when password was just entered in step 1.
      if (password) {
        try {
          const loginRes = await api<{ user: SessionUser }>("/api/auth/login", {
            method: "POST",
            body: { email: cleanEmail, password },
          });
          setUser(loginRes.user);
          void refreshCart();
          showToast("Đăng ký và xác thực tài khoản thành công.");
          router.push(nextTarget);
          router.refresh();
          return;
        } catch {
          // Account is already verified; fall back cleanly to the login screen.
        }
      }

      showToast("Xác thực email thành công. Bạn có thể đăng nhập ngay.");
      const params = new URLSearchParams({ verified: "1", email: cleanEmail });
      if (nextParam) params.set("next", nextTarget);
      router.push(`/dang-nhap?${params.toString()}`);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Không thể xác thực mã lúc này. Bạn vui lòng thử lại."
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleResendOtp = async () => {
    if (resendCooldown > 0 || resending) return;
    const cleanEmail = email.trim();
    if (!cleanEmail) return;

    setError(null);
    setNotice(null);
    setResending(true);
    try {
      await api<{ sent: boolean }>("/api/auth/resend-otp", {
        method: "POST",
        body: { email: cleanEmail },
      });
      setResendCooldown(30);
      setNotice("Đã gửi lại mã xác thực mới tới hộp thư của bạn.");
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Chưa gửi lại được mã xác thực. Bạn vui lòng thử lại sau."
      );
    } finally {
      setResending(false);
    }
  };

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Tạo tài khoản</h1>
            <p className={styles.authLead}>Đang khởi tạo biểu mẫu đăng ký thành viên NexusMart.</p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Đăng ký thành viên</h2>
          <StateView kind="loading" title="Đang tải trang đăng ký..." rows={4} />
        </section>
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Tạo tài khoản</h1>
            <p className={styles.authLead}>Không thể kết nối dịch vụ tạo tài khoản lúc này.</p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái đăng ký</h2>
          <StateView
            kind="error"
            title="Dịch vụ đăng ký tạm gián đoạn"
            message="Vui lòng thử tải lại trang sau ít phút để hoàn tất tạo tài khoản."
            actionLabel="Thử lại"
            actionHref="/dang-ky"
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
            <h1 className={shared.pageTitle}>Tạo tài khoản</h1>
            <p className={styles.authLead}>Chưa có thông tin đăng ký nào được điền.</p>
          </div>
        </section>
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Bắt đầu đăng ký</h2>
          <StateView
            kind="empty"
            title="Sẵn sàng tạo tài khoản thành viên mới"
            message="Mở biểu mẫu đăng ký để lưu địa chỉ nhận hàng và theo dõi đơn từ 4 xưởng dệt."
            actionLabel="Mở biểu mẫu đăng ký"
            actionHref="/dang-ky"
          />
        </section>
      </div>
    );
  }

  return (
    <div className={shared.page}>
      <section className={shared.section}>
        <div className={styles.authGrid}>
          {/* Left column: heading + registration / OTP verification form immediately above the fold */}
          <div className={styles.formCol}>
            <div className={styles.authHeader}>
              <h1 className={`${shared.pageTitle} lines`}>
                <span className="line">
                  <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                    {step === "otp" ? "Xác thực email" : "Tạo tài khoản mới"}
                  </span>
                </span>
              </h1>
              <p
                className={styles.authLead}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                {step === "otp"
                  ? "Nhập mã 6 chữ số vừa gửi về hộp thư để hoàn tất kích hoạt tài khoản NexusMart."
                  : "Đăng ký một lần để mua đồ cơ bản từ 4 xưởng dệt Việt Nam và đổi trả miễn phí 45 ngày."}
              </p>
            </div>

            {step === "register" ? (
              <form
                className={styles.authPanel}
                onSubmit={handleRegisterSubmit}
                noValidate
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div
                  className={shared.field}
                  data-rise=""
                  style={{ "--i": 3 } as React.CSSProperties}
                >
                  <label htmlFor="reg-name" className={shared.fieldLabel}>
                    Họ và tên
                  </label>
                  <input
                    id="reg-name"
                    type="text"
                    autoComplete="name"
                    className={shared.input}
                    value={fullName}
                    onChange={(e) => setFullName(e.target.value)}
                    placeholder="Ví dụ: Phạm Gia Huy"
                    disabled={submitting}
                    required
                  />
                </div>

                <div
                  className={shared.formGrid}
                  data-rise=""
                  style={{ "--i": 4 } as React.CSSProperties}
                >
                  <div className={shared.field}>
                    <label htmlFor="reg-email" className={shared.fieldLabel}>
                      Email
                    </label>
                    <input
                      id="reg-email"
                      type="email"
                      autoComplete="email"
                      className={shared.input}
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      placeholder="giahuy@nexusmart.vn"
                      disabled={submitting}
                      required
                    />
                  </div>

                  <div className={shared.field}>
                    <label htmlFor="reg-phone" className={shared.fieldLabel}>
                      Số điện thoại
                    </label>
                    <input
                      id="reg-phone"
                      type="tel"
                      autoComplete="tel"
                      className={shared.input}
                      value={phone}
                      onChange={(e) => setPhone(e.target.value)}
                      placeholder="0908 321 654"
                      disabled={submitting}
                    />
                  </div>
                </div>

                <div
                  className={shared.formGrid}
                  data-rise=""
                  style={{ "--i": 5 } as React.CSSProperties}
                >
                  <div className={shared.field}>
                    <label htmlFor="reg-pass" className={shared.fieldLabel}>
                      Mật khẩu (tối thiểu 8 ký tự)
                    </label>
                    <input
                      id="reg-pass"
                      type="password"
                      autoComplete="new-password"
                      className={shared.input}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      placeholder="Nhập mật khẩu"
                      disabled={submitting}
                      required
                    />
                  </div>

                  <div className={shared.field}>
                    <label htmlFor="reg-confirm" className={shared.fieldLabel}>
                      Xác nhận mật khẩu
                    </label>
                    <input
                      id="reg-confirm"
                      type="password"
                      autoComplete="new-password"
                      className={shared.input}
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      placeholder="Nhập lại mật khẩu"
                      disabled={submitting}
                      required
                    />
                  </div>
                </div>

                <div
                  className={styles.bonusBanner}
                  data-rise=""
                  style={{ "--i": 6 } as React.CSSProperties}
                >
                  <p className={styles.bonusText}>
                    Sau khi bấm tạo tài khoản, NexusMart sẽ gửi mã xác thực 6 chữ số về email của
                    bạn để kích hoạt nhanh chóng, không cần liên kết thẻ ngân hàng.
                  </p>
                </div>

                {error && (
                  <p className={shared.fieldError} role="alert">
                    {error}
                  </p>
                )}

                <div data-rise="" style={{ "--i": 7 } as React.CSSProperties}>
                  <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
                    {submitting ? "Đang tạo tài khoản..." : "Tạo tài khoản NexusMart"}
                  </Button>
                </div>

                <p
                  className={styles.switchLine}
                  data-rise=""
                  style={{ "--i": 8 } as React.CSSProperties}
                >
                  Đã có tài khoản thành viên?{" "}
                  <Link href={loginHref} className={styles.switchLink}>
                    Đăng nhập ngay
                  </Link>
                </p>
              </form>
            ) : (
              <form
                className={styles.authPanel}
                onSubmit={handleVerifyOtpSubmit}
                noValidate
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div className={styles.bonusBanner} role="status">
                  <p className={styles.bonusText}>
                    Mã xác thực 6 chữ số đã được gửi tới <strong>{email}</strong>. Mã có hiệu lực
                    trong 10 phút.
                  </p>
                </div>

                <div className={shared.field}>
                  <label htmlFor="otp-code" className={shared.fieldLabel}>
                    Mã xác thực (6 chữ số)
                  </label>
                  <input
                    id="otp-code"
                    type="text"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={6}
                    className={`${shared.input} ${styles.otpInput} num`}
                    value={otp}
                    onChange={(e) => setOtp(e.target.value.replace(/\D/g, "").slice(0, 6))}
                    placeholder="Nhập 6 chữ số"
                    disabled={submitting}
                    required
                  />
                </div>

                {notice && (
                  <p className={styles.noticeText} role="status">
                    {notice}
                  </p>
                )}

                {error && (
                  <p className={shared.fieldError} role="alert">
                    {error}
                  </p>
                )}

                <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
                  {submitting ? "Đang xác thực..." : "Xác thực & hoàn tất đăng ký"}
                </Button>

                <div className={styles.otpActions}>
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    disabled={resending || resendCooldown > 0}
                    onClick={() => void handleResendOtp()}
                  >
                    {resending
                      ? "Đang gửi lại..."
                      : resendCooldown > 0
                        ? `Gửi lại mã (${resendCooldown}s)`
                        : "Gửi lại mã xác thực"}
                  </Button>
                  <button
                    type="button"
                    className={styles.textLinkBtn}
                    onClick={() => {
                      setStep("register");
                      setError(null);
                      setNotice(null);
                    }}
                  >
                    Quay lại sửa thông tin
                  </button>
                </div>

                <p className={styles.switchLine}>
                  Đã xác thực xong?{" "}
                  <Link href={loginHref} className={styles.switchLink}>
                    Đăng nhập ngay
                  </Link>
                </p>
              </form>
            )}
          </div>

          {/* Right column: dark perks band + 4 Vietnamese editorial on-model shots */}
          <div className={styles.showcaseCol}>
            <div
              className={styles.perksBand}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <h2 className={styles.perksHeading}>Bốn xưởng dệt, một giỏ hàng</h2>
              <ul className={styles.perkList}>
                {REGISTER_PERKS.map((item, idx) => (
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

export default function RegisterPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang mở trang đăng ký..." rows={4} />
        </div>
      }
    >
      <RegisterPageContent />
    </Suspense>
  );
}
