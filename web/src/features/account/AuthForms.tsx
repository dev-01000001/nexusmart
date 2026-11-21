"use client";

import React, { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ApiError, api } from "@/lib/api";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { Button, StateView } from "@/components";
import { resolvePostLoginPath, type AuthVariant } from "./authRedirect";
import styles from "./AuthForms.module.css";

export type { AuthVariant };

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

interface AuthFormBaseProps {
  /**
   * `site`: a shop's own host (buyers; afterwards go to `next` or `/tai-khoan`).
   * `platform`: the NexusMart root host (sellers and admins; buyers are sent to `buyerHref`).
   */
  variant: AuthVariant;
  /** Platform only: where a buyer account lands after signing in on the root host. */
  buyerHref?: string;
}

export interface LoginFormProps extends AuthFormBaseProps {
  registerHref?: string;
}

export interface RegisterFormProps extends AuthFormBaseProps {
  loginHref?: string;
  /** Overrides the destination after sign-up + OTP + automatic sign-in (e.g. shop opening flow). */
  afterRegisterHref?: string;
}

function withNext(href: string, nextParam: string | null): string {
  if (!nextParam) return href;
  const sep = href.includes("?") ? "&" : "?";
  return `${href}${sep}next=${encodeURIComponent(nextParam)}`;
}

function FormFallback({ title }: { title: string }) {
  return (
    <div className={styles.panel}>
      <StateView kind="loading" title={title} rows={3} />
    </div>
  );
}

function LoginFormInner({ variant, buyerHref = "/", registerHref = "/dang-ky" }: LoginFormProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { user, isAuthenticated, setUser, logout } = useSession();

  const nextParam = searchParams.get("next");
  const verifiedNotice = searchParams.get("verified") === "1";

  const [email, setEmail] = useState(searchParams.get("email") ?? "");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [needsVerificationEmail, setNeedsVerificationEmail] = useState<string | null>(null);

  const destinationFor = (u: SessionUser) =>
    resolvePostLoginPath(variant, u.role, nextParam, buyerHref);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    if (!EMAIL_RE.test(cleanEmail)) {
      setError("Nhập địa chỉ email hợp lệ, ví dụ tenban@gmail.com.");
      return;
    }
    if (password.length < 6) {
      setError("Mật khẩu cần tối thiểu 6 ký tự.");
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
      router.push(destinationFor(res.user));
      router.refresh();
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
        if (err.code === "FAILED_PRECONDITION") setNeedsVerificationEmail(cleanEmail);
      } else {
        setError("Chưa đăng nhập được lúc này. Bạn thử lại sau ít phút.");
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form className={styles.panel} onSubmit={handleSubmit} noValidate>
      {verifiedNotice && (
        <p className={styles.note} role="status">
          Email đã được xác thực. Nhập mật khẩu để đăng nhập.
        </p>
      )}

      {isAuthenticated && user && (
        <div className={`${styles.note} ${styles.sessionBar}`}>
          <p>
            Bạn đang đăng nhập bằng <strong>{user.email}</strong>.
          </p>
          <div className={styles.sessionActions}>
            <Button variant="ink" size="sm" href={destinationFor(user)}>
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

      <div className={styles.field}>
        <label htmlFor="login-email" className={styles.label}>
          Email
        </label>
        <input
          id="login-email"
          type="email"
          autoComplete="email"
          className={styles.input}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="tenban@gmail.com"
          disabled={submitting}
          required
        />
      </div>

      <div className={styles.field}>
        <label htmlFor="login-password" className={styles.label}>
          Mật khẩu
        </label>
        <div className={styles.passwordWrap}>
          <input
            id="login-password"
            type={showPassword ? "text" : "password"}
            autoComplete="current-password"
            className={styles.input}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={submitting}
            required
          />
          <button
            type="button"
            className={styles.toggleBtn}
            aria-pressed={showPassword}
            onClick={() => setShowPassword((v) => !v)}
          >
            {showPassword ? "Ẩn" : "Hiện"}
          </button>
        </div>
      </div>

      {error && (
        <div role="alert" className={styles.errorStack}>
          <p className={styles.error}>{error}</p>
          {needsVerificationEmail && (
            <Link
              href={withNext(
                `/dang-ky?step=otp&email=${encodeURIComponent(needsVerificationEmail)}`,
                nextParam
              )}
              className={styles.switchLink}
            >
              Nhập mã xác thực email
            </Link>
          )}
        </div>
      )}

      <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
        {submitting ? "Đang đăng nhập..." : "Đăng nhập"}
      </Button>

      {variant === "site" ? (
        <p className={styles.switchLine}>
          Chưa có tài khoản?{" "}
          <Link href={withNext(registerHref, nextParam)} className={styles.switchLink}>
            Tạo tài khoản
          </Link>
          . Đặt hàng không cần nhớ mật khẩu?{" "}
          <Link href="/tra-cuu-don-hang" className={styles.switchLink}>
            Tra cứu đơn bằng mã đơn
          </Link>
          .
        </p>
      ) : (
        <p className={styles.switchLine}>
          Chưa có tài khoản người bán?{" "}
          <Link href={withNext(registerHref, nextParam)} className={styles.switchLink}>
            Đăng ký để mở gian hàng
          </Link>
          .
        </p>
      )}
    </form>
  );
}

function RegisterFormInner({
  variant,
  buyerHref = "/",
  loginHref = "/dang-nhap",
  afterRegisterHref,
}: RegisterFormProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { setUser } = useSession();

  const nextParam = searchParams.get("next");
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

  useEffect(() => {
    if (resendCooldown <= 0) return;
    const timer = window.setInterval(() => {
      setResendCooldown((prev) => (prev > 1 ? prev - 1 : 0));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [resendCooldown]);

  const handleRegisterSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fullName.trim();
    const cleanEmail = email.trim();
    const cleanPhone = phone.replace(/\s+/g, "").trim();

    if (cleanName.length < 2) {
      setError("Nhập họ và tên đầy đủ.");
      return;
    }
    if (!EMAIL_RE.test(cleanEmail)) {
      setError("Địa chỉ email chưa đúng định dạng.");
      return;
    }
    if (cleanPhone && !/^0\d{9,10}$/.test(cleanPhone)) {
      setError("Số điện thoại cần 10 hoặc 11 chữ số, bắt đầu bằng số 0.");
      return;
    }
    if (password.length < 8) {
      setError("Mật khẩu cần tối thiểu 8 ký tự.");
      return;
    }
    if (password !== confirmPassword) {
      setError("Mật khẩu nhập lại chưa khớp.");
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
          body: { fullName: cleanName, email: cleanEmail, phone: cleanPhone, password },
        }
      );
      setEmail(res.email || cleanEmail);
      setOtp("");
      setStep("otp");
      setResendCooldown(30);
      setNotice("Đã gửi mã 6 chữ số tới email của bạn.");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Chưa tạo được tài khoản. Bạn thử lại sau ít phút."
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleVerifyOtpSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    const cleanOtp = otp.replace(/\s+/g, "").trim();

    if (!EMAIL_RE.test(cleanEmail)) {
      setError("Địa chỉ email cần xác thực chưa hợp lệ.");
      return;
    }
    if (!/^\d{6}$/.test(cleanOtp)) {
      setError("Nhập đủ 6 chữ số trong email xác thực.");
      return;
    }

    setError(null);
    setNotice(null);
    setSubmitting(true);
    try {
      await api<{ verified: boolean }>("/api/auth/verify-otp", {
        method: "POST",
        body: { email: cleanEmail, otp: cleanOtp },
      });

      // Sign in right away when the password was typed in step 1 of this visit.
      if (password) {
        try {
          const loginRes = await api<{ user: SessionUser }>("/api/auth/login", {
            method: "POST",
            body: { email: cleanEmail, password },
          });
          setUser(loginRes.user);
          router.push(
            afterRegisterHref ||
              resolvePostLoginPath(variant, loginRes.user.role, nextParam, buyerHref)
          );
          router.refresh();
          return;
        } catch {
          // The account is verified already; fall back to the login screen below.
        }
      }

      const params = new URLSearchParams({ verified: "1", email: cleanEmail });
      if (nextParam) params.set("next", nextParam);
      router.push(`${loginHref}?${params.toString()}`);
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Chưa xác thực được mã. Bạn thử lại sau ít phút."
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
      setNotice("Đã gửi lại mã mới tới email của bạn.");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Chưa gửi lại được mã. Bạn thử lại sau ít phút."
      );
    } finally {
      setResending(false);
    }
  };

  if (step === "otp") {
    return (
      <form className={styles.panel} onSubmit={handleVerifyOtpSubmit} noValidate>
        <p className={styles.note} role="status">
          Mã xác thực 6 chữ số đã được gửi tới <strong>{email}</strong>. Mã có hiệu lực trong 10
          phút.
        </p>

        <div className={styles.field}>
          <label htmlFor="otp-code" className={styles.label}>
            Mã xác thực
          </label>
          <input
            id="otp-code"
            type="text"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            className={`${styles.input} ${styles.otp}`}
            value={otp}
            onChange={(e) => setOtp(e.target.value.replace(/\D/g, "").slice(0, 6))}
            placeholder="6 chữ số"
            disabled={submitting}
            required
          />
        </div>

        {notice && (
          <p className={styles.notice} role="status">
            {notice}
          </p>
        )}
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}

        <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
          {submitting ? "Đang xác thực..." : "Xác thực và hoàn tất"}
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
                ? `Gửi lại mã sau ${resendCooldown} giây`
                : "Gửi lại mã"}
          </Button>
          <button
            type="button"
            className={styles.textBtn}
            onClick={() => {
              setStep("register");
              setError(null);
              setNotice(null);
            }}
          >
            Sửa thông tin đăng ký
          </button>
        </div>

        <p className={styles.switchLine}>
          Đã xác thực xong?{" "}
          <Link href={withNext(loginHref, nextParam)} className={styles.switchLink}>
            Đăng nhập
          </Link>
        </p>
      </form>
    );
  }

  return (
    <form className={styles.panel} onSubmit={handleRegisterSubmit} noValidate>
      <div className={styles.field}>
        <label htmlFor="reg-name" className={styles.label}>
          Họ và tên
        </label>
        <input
          id="reg-name"
          type="text"
          autoComplete="name"
          className={styles.input}
          value={fullName}
          onChange={(e) => setFullName(e.target.value)}
          disabled={submitting}
          required
        />
      </div>

      <div className={styles.fieldRow}>
        <div className={styles.field}>
          <label htmlFor="reg-email" className={styles.label}>
            Email
          </label>
          <input
            id="reg-email"
            type="email"
            autoComplete="email"
            className={styles.input}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="tenban@gmail.com"
            disabled={submitting}
            required
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="reg-phone" className={styles.label}>
            Số điện thoại (không bắt buộc)
          </label>
          <input
            id="reg-phone"
            type="tel"
            autoComplete="tel"
            className={styles.input}
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            placeholder="0908 321 654"
            disabled={submitting}
          />
        </div>
      </div>

      <div className={styles.fieldRow}>
        <div className={styles.field}>
          <label htmlFor="reg-pass" className={styles.label}>
            Mật khẩu (tối thiểu 8 ký tự)
          </label>
          <input
            id="reg-pass"
            type="password"
            autoComplete="new-password"
            className={styles.input}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={submitting}
            required
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="reg-confirm" className={styles.label}>
            Nhập lại mật khẩu
          </label>
          <input
            id="reg-confirm"
            type="password"
            autoComplete="new-password"
            className={styles.input}
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            disabled={submitting}
            required
          />
        </div>
      </div>

      <p className={styles.note}>
        {variant === "site"
          ? "Sau khi bấm Tạo tài khoản, bạn nhận mã 6 chữ số qua email để kích hoạt. Tài khoản này dùng được ở mọi cửa hàng chạy trên NexusMart; mỗi cửa hàng bạn đăng nhập riêng."
          : "Sau khi bấm Tạo tài khoản, bạn nhận mã 6 chữ số qua email để kích hoạt."}
      </p>

      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}

      <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
        {submitting ? "Đang tạo tài khoản..." : "Tạo tài khoản"}
      </Button>

      <p className={styles.switchLine}>
        Đã có tài khoản?{" "}
        <Link href={withNext(loginHref, nextParam)} className={styles.switchLink}>
          Đăng nhập
        </Link>
      </p>
    </form>
  );
}

/** Email + password login panel. Reads `next`, `email`, `verified` from the URL. */
export function LoginForm(props: LoginFormProps) {
  return (
    <Suspense fallback={<FormFallback title="Đang mở biểu mẫu đăng nhập..." />}>
      <LoginFormInner {...props} />
    </Suspense>
  );
}

/** Sign-up panel with the email OTP step. Reads `next`, `email`, `step=otp` from the URL. */
export function RegisterForm(props: RegisterFormProps) {
  return (
    <Suspense fallback={<FormFallback title="Đang mở biểu mẫu đăng ký..." />}>
      <RegisterFormInner {...props} />
    </Suspense>
  );
}
