"use client";

import React, { Suspense, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ApiError, api } from "@/lib/api";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { Button, StateView } from "@/components";
import { focusAndShakeInvalid } from "@/features/commerce/formFeedback";
import { safeNextPath } from "./accountTypes";
import { resolvePostLoginPath, type AuthVariant } from "./authRedirect";
import styles from "./AuthForms.module.css";

export type { AuthVariant };

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const OTP_SLOTS = [0, 1, 2, 3, 4, 5] as const;

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
  /** Notifies wrapper shells (e.g. PlatformAuthShell) when switching between register and OTP steps. */
  onStepChange?: (step: "register" | "otp") => void;
}

function withNext(href: string, nextParam: string | null): string {
  if (!nextParam) return href;
  const sep = href.includes("?") ? "&" : "?";
  return `${href}${sep}next=${encodeURIComponent(nextParam)}`;
}

function FormFallback({ title }: { title: string }) {
  return (
    <div className={styles.panel}>
      <StateView kind="loading" skeletonVariant="detail" title={title} rows={3} />
    </div>
  );
}

type LoginFieldKey = "email" | "password";
type RegFieldKey = "fullName" | "email" | "phone" | "password" | "confirmPassword" | "otp";

function LoginFormInner({ variant, buyerHref = "/", registerHref = "/dang-ky" }: LoginFormProps) {
  const searchParams = useSearchParams();
  const { user, isAuthenticated, loading, setUser, logout } = useSession();

  const nextParam = searchParams.get("next");
  const verifiedNotice = searchParams.get("verified") === "1";
  const resetNotice = searchParams.get("reset") === "1";
  const expiredParam = searchParams.get("expired") === "1";

  useEffect(() => {
    if (!expiredParam || loading || isAuthenticated) return;
    fetch("/api/auth/logout", { method: "POST" }).catch(() => {});
  }, [expiredParam, loading, isAuthenticated]);

  const formRef = useRef<HTMLFormElement | null>(null);
  const [email, setEmail] = useState(searchParams.get("email") ?? "");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<LoginFieldKey, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);
  const [needsVerificationEmail, setNeedsVerificationEmail] = useState<string | null>(null);

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(formRef.current);
    }
  }, [errorPulse]);

  const destinationFor = (u: SessionUser) =>
    resolvePostLoginPath(variant, u.role, nextParam, buyerHref);

  const clearFieldError = (key: LoginFieldKey) => {
    setFieldErrors((prev) => {
      if (!prev[key]) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    const nextErrors: Partial<Record<LoginFieldKey, string>> = {};

    if (!EMAIL_RE.test(cleanEmail)) {
      nextErrors.email = "Nhập email hợp lệ, ví dụ tenban@gmail.com.";
    }
    if (password.length < 6) {
      nextErrors.password = "Mật khẩu cần tối thiểu 6 ký tự.";
    }

    if (Object.keys(nextErrors).length > 0) {
      setFieldErrors(nextErrors);
      setFormError(null);
      setErrorPulse((n) => n + 1);
      return;
    }

    setFieldErrors({});
    setFormError(null);
    setNeedsVerificationEmail(null);
    setSubmitting(true);
    let navigating = false;
    try {
      const res = await api<{ user: SessionUser }>("/api/auth/login", {
        method: "POST",
        body: { email: cleanEmail, password },
      });
      setUser(res.user);
      const dest = safeNextPath(destinationFor(res.user), "/");
      navigating = true;
      window.location.assign(dest);
      return;
    } catch (err) {
      if (err instanceof ApiError) {
        setFormError(err.message);
        if (err.code === "FAILED_PRECONDITION") setNeedsVerificationEmail(cleanEmail);
      } else {
        setFormError("Chưa đăng nhập được lúc này. Thử lại sau.");
      }
      setErrorPulse((n) => n + 1);
    } finally {
      if (!navigating) setSubmitting(false);
    }
  };

  return (
    <form ref={formRef} className={styles.panel} onSubmit={handleSubmit} noValidate>
      {verifiedNotice && (
        <p className={styles.note} role="status">
          Email đã xác thực. Nhập mật khẩu để đăng nhập.
        </p>
      )}
      {resetNotice && (
        <p className={styles.note} role="status">
          Mật khẩu đã được đặt lại thành công. Nhập mật khẩu mới để đăng nhập.
        </p>
      )}
      {expiredParam && !isAuthenticated && (
        <p className={styles.note} role="status">
          Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.
        </p>
      )}

      {isAuthenticated && user && (
        <div className={`${styles.note} ${styles.sessionBar}`}>
          <p>
            Đang đăng nhập bằng <strong>{user.email}</strong>.
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

      <div className={styles.field} data-shake-target="">
        <div className={styles.floatingWrap}>
          <input
            id="login-email"
            name="email"
            type="email"
            autoComplete="email"
            placeholder=" "
            className={`${styles.floatingInput} ${
              fieldErrors.email ? styles.floatingInputInvalid : ""
            }`}
            aria-invalid={Boolean(fieldErrors.email) || undefined}
            aria-describedby={fieldErrors.email ? "login-email-error" : undefined}
            value={email}
            onChange={(e) => {
              setEmail(e.target.value);
              clearFieldError("email");
            }}
            disabled={submitting}
            required
          />
          <label
            htmlFor="login-email"
            className={`${styles.floatingLabel} ${email ? styles.floatingLabelUp : ""}`}
          >
            Email
          </label>
        </div>
        {fieldErrors.email && (
          <p id="login-email-error" className={styles.error} role="alert">
            {fieldErrors.email}
          </p>
        )}
      </div>

      <div className={styles.field} data-shake-target="">
        <div className={`${styles.floatingWrap} ${styles.floatingWrapPassword}`}>
          <input
            id="login-password"
            name="password"
            type={showPassword ? "text" : "password"}
            autoComplete="current-password"
            placeholder=" "
            className={`${styles.floatingInput} ${styles.floatingInputPassword} ${
              fieldErrors.password ? styles.floatingInputInvalid : ""
            }`}
            aria-invalid={Boolean(fieldErrors.password) || undefined}
            aria-describedby={fieldErrors.password ? "login-password-error" : undefined}
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
              clearFieldError("password");
            }}
            disabled={submitting}
            required
          />
          <label
            htmlFor="login-password"
            className={`${styles.floatingLabel} ${password ? styles.floatingLabelUp : ""}`}
          >
            Mật khẩu
          </label>
          <button
            type="button"
            className={styles.toggleBtn}
            aria-pressed={showPassword}
            aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
            onClick={() => setShowPassword((v) => !v)}
          >
            {showPassword ? "Ẩn" : "Hiện"}
          </button>
        </div>
        {fieldErrors.password && (
          <p id="login-password-error" className={styles.error} role="alert">
            {fieldErrors.password}
          </p>
        )}
      </div>

      {formError && (
        <div role="alert" data-shake-alert="" className={styles.errorStack}>
          <p className={styles.error}>{formError}</p>
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

      <Button
        type="submit"
        variant="primary"
        size="lg"
        fullWidth
        loading={submitting}
        loadingLabel="Đang đăng nhập"
      >
        Đăng nhập
      </Button>

      {variant === "site" ? (
        <div className={styles.switchLine}>
          <span>
            Chưa có tài khoản?{" "}
            <Link href={withNext(registerHref, nextParam)} className={styles.switchLink}>
              Tạo tài khoản
            </Link>
          </span>
          <span>
            <Link href="/quen-mat-khau" className={styles.switchLink}>
              Quên mật khẩu?
            </Link>{" "}
            ·{" "}
            <Link href="/tra-cuu-don-hang" className={styles.switchLink}>
              Tra cứu đơn
            </Link>
          </span>
        </div>
      ) : (
        <div className={styles.switchLine}>
          <span>
            Chưa có tài khoản?{" "}
            <Link href={withNext(registerHref, nextParam)} className={styles.switchLink}>
              Đăng ký mở gian hàng
            </Link>
          </span>
          <Link href="/quen-mat-khau" className={styles.switchLink}>
            Quên mật khẩu?
          </Link>
        </div>
      )}
    </form>
  );
}

function RegisterFormInner({
  variant,
  buyerHref = "/",
  loginHref = "/dang-nhap",
  afterRegisterHref,
  onStepChange,
}: RegisterFormProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { setUser } = useSession();

  const nextParam = searchParams.get("next");
  const initialEmail = searchParams.get("email") ?? "";
  const initialStep = searchParams.get("step") === "otp" && initialEmail ? "otp" : "register";

  const [step, setStep] = useState<"register" | "otp">(initialStep);
  const onStepChangeRef = useRef(onStepChange);
  onStepChangeRef.current = onStepChange;
  const lastReportedStepRef = useRef<"register" | "otp">(initialStep);
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState(initialEmail);
  const [phone, setPhone] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);

  const [otp, setOtp] = useState("");
  const [otpFocused, setOtpFocused] = useState(false);
  const [otpCursor, setOtpCursor] = useState(0);
  const otpInputRef = useRef<HTMLInputElement | null>(null);

  const formRef = useRef<HTMLFormElement | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [resending, setResending] = useState(false);
  const [resendCooldown, setResendCooldown] = useState(0);
  const [notice, setNotice] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<RegFieldKey, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(formRef.current);
    }
  }, [errorPulse]);

  const cooldownActive = resendCooldown > 0;
  useEffect(() => {
    if (!cooldownActive) return;
    const timer = window.setInterval(() => {
      setResendCooldown((prev) => (prev > 1 ? prev - 1 : 0));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [cooldownActive]);

  useEffect(() => {
    if (lastReportedStepRef.current !== step) {
      lastReportedStepRef.current = step;
      onStepChangeRef.current?.(step);
    }
    if (step === "otp") {
      otpInputRef.current?.focus();
    }
  }, [step]);

  const clearFieldError = (key: RegFieldKey) => {
    setFieldErrors((prev) => {
      if (!prev[key]) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const syncOtpCursor = (el: HTMLInputElement | null, nextVal?: string) => {
    const val = nextVal ?? el?.value ?? "";
    const pos = el?.selectionStart ?? val.length;
    setOtpCursor(Math.min(5, Math.max(0, Math.min(pos, val.length))));
  };

  const handleRegisterSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanName = fullName.trim();
    const cleanEmail = email.trim();
    const cleanPhone = phone.replace(/\s+/g, "").trim();

    const nextErrors: Partial<Record<RegFieldKey, string>> = {};
    if (cleanName.length < 2) {
      nextErrors.fullName = "Nhập họ và tên đầy đủ.";
    }
    if (!EMAIL_RE.test(cleanEmail)) {
      nextErrors.email = "Email chưa đúng định dạng.";
    }
    if (cleanPhone && !/^0\d{9,10}$/.test(cleanPhone)) {
      nextErrors.phone = "Số điện thoại cần 10 hoặc 11 chữ số, bắt đầu bằng 0.";
    }
    if (password.length < 8) {
      nextErrors.password = "Mật khẩu cần tối thiểu 8 ký tự.";
    }
    if (password !== confirmPassword) {
      nextErrors.confirmPassword = "Mật khẩu nhập lại chưa khớp.";
    }

    if (Object.keys(nextErrors).length > 0) {
      setFieldErrors(nextErrors);
      setFormError(null);
      setErrorPulse((n) => n + 1);
      return;
    }

    setFieldErrors({});
    setFormError(null);
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
      setOtpCursor(0);
      setErrorPulse(0);
      setStep("otp");
      setResendCooldown(30);
      setNotice("Mã 6 số đang được gửi tới email của bạn.");
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Chưa tạo được tài khoản. Thử lại sau.");
      setErrorPulse((n) => n + 1);
    } finally {
      setSubmitting(false);
    }
  };

  const handleVerifyOtpSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    const cleanOtp = otp.replace(/\s+/g, "").trim();

    if (!EMAIL_RE.test(cleanEmail)) {
      setFormError("Email xác thực chưa hợp lệ.");
      setErrorPulse((n) => n + 1);
      return;
    }
    if (!/^\d{6}$/.test(cleanOtp)) {
      setFieldErrors({ otp: "Nhập đủ 6 chữ số trong email xác thực." });
      setFormError(null);
      setErrorPulse((n) => n + 1);
      return;
    }

    setFieldErrors({});
    setFormError(null);
    setNotice(null);
    setSubmitting(true);
    let navigating = false;
    try {
      await api<{ verified: boolean }>("/api/auth/verify-otp", {
        method: "POST",
        body: { email: cleanEmail, otp: cleanOtp },
      });

      if (password) {
        try {
          const loginRes = await api<{ user: SessionUser }>("/api/auth/login", {
            method: "POST",
            body: { email: cleanEmail, password },
          });
          setUser(loginRes.user);
          const fallbackDest = resolvePostLoginPath(
            variant,
            loginRes.user.role,
            nextParam,
            buyerHref
          );
          const dest = safeNextPath(afterRegisterHref, fallbackDest);
          navigating = true;
          window.location.assign(dest);
          return;
        } catch {
          // Verified already; fall back to sign-in.
        }
      }

      const params = new URLSearchParams({ verified: "1", email: cleanEmail });
      if (nextParam) params.set("next", nextParam);
      router.push(`${loginHref}?${params.toString()}`);
    } catch (err) {
      setFieldErrors({
        otp: err instanceof ApiError ? err.message : "Mã xác thực chưa đúng hoặc đã hết hạn.",
      });
      setErrorPulse((n) => n + 1);
    } finally {
      if (!navigating) setSubmitting(false);
    }
  };

  const handleResendOtp = async () => {
    if (resendCooldown > 0 || resending) return;
    const cleanEmail = email.trim();
    if (!cleanEmail) return;

    setFieldErrors({});
    setFormError(null);
    setNotice(null);
    setResending(true);
    try {
      await api<{ sent: boolean }>("/api/auth/resend-otp", {
        method: "POST",
        body: { email: cleanEmail },
      });
      setResendCooldown(30);
      setNotice(
        "Mã mới đang được gửi tới email của bạn. Chưa thấy sau 1–2 phút: xem thư rác hoặc gửi lại."
      );
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : "Chưa gửi lại được mã.");
      setErrorPulse((n) => n + 1);
    } finally {
      setResending(false);
    }
  };

  if (step === "otp") {
    const activeCellIndex = Math.min(
      5,
      otp.length < 6 ? Math.min(otpCursor, otp.length) : otpCursor
    );
    return (
      <form ref={formRef} className={styles.panel} onSubmit={handleVerifyOtpSubmit} noValidate>
        <p className={styles.note} role="status">
          Mã 6 số đang được gửi tới <strong>{email}</strong>, hiệu lực 10 phút. Chưa thấy sau 1–2
          phút: xem thư rác hoặc gửi lại.
        </p>

        <div className={styles.otpField} data-shake-target="">
          <label htmlFor="otp-code" className={styles.otpLabel}>
            Mã xác thực 6 chữ số
          </label>
          <div className={styles.otpStage}>
            {OTP_SLOTS.map((idx) => {
              const digit = otp[idx] ?? "";
              const isActive = otpFocused && activeCellIndex === idx;
              return (
                <div
                  key={idx}
                  aria-hidden="true"
                  className={`${styles.otpCell} ${digit ? styles.otpCellFilled : ""} ${
                    isActive ? styles.otpCellActive : ""
                  } ${fieldErrors.otp ? styles.otpCellInvalid : ""}`}
                >
                  {digit ? (
                    <span>{digit}</span>
                  ) : isActive ? (
                    <span className={styles.otpCaret} />
                  ) : null}
                </div>
              );
            })}
            <input
              ref={otpInputRef}
              id="otp-code"
              name="otp"
              type="text"
              inputMode="numeric"
              pattern="[0-9]*"
              autoComplete="one-time-code"
              maxLength={6}
              className={styles.otpOverlayInput}
              aria-invalid={Boolean(fieldErrors.otp) || undefined}
              aria-describedby={fieldErrors.otp ? "otp-code-error" : undefined}
              value={otp}
              onFocus={(e) => {
                setOtpFocused(true);
                syncOtpCursor(e.currentTarget);
              }}
              onBlur={() => setOtpFocused(false)}
              onKeyUp={(e) => syncOtpCursor(e.currentTarget)}
              onClick={(e) => syncOtpCursor(e.currentTarget)}
              onChange={(e) => {
                const digits = e.target.value.replace(/\D/g, "").slice(0, 6);
                setOtp(digits);
                setOtpCursor(Math.min(5, digits.length));
                clearFieldError("otp");
              }}
              disabled={submitting}
              required
            />
          </div>
          {fieldErrors.otp && (
            <p id="otp-code-error" className={styles.error} role="alert">
              {fieldErrors.otp}
            </p>
          )}
        </div>

        {notice && (
          <p className={styles.notice} role="status">
            {notice}
          </p>
        )}
        {formError && (
          <p className={styles.error} role="alert">
            {formError}
          </p>
        )}

        <Button
          type="submit"
          variant="primary"
          size="lg"
          fullWidth
          loading={submitting}
          loadingLabel="Đang xác thực"
        >
          Xác thực và hoàn tất
        </Button>

        <div className={styles.otpActions}>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            loading={resending}
            loadingLabel="Đang gửi lại"
            disabled={resendCooldown > 0}
            onClick={() => void handleResendOtp()}
          >
            {resendCooldown > 0 ? `Gửi lại sau ${resendCooldown}s` : "Gửi lại mã"}
          </Button>
          <button
            type="button"
            className={styles.textBtn}
            onClick={() => {
              setStep("register");
              setErrorPulse(0);
              setFieldErrors({});
              setFormError(null);
              setNotice(null);
            }}
          >
            Sửa thông tin
          </button>
        </div>

        <p className={styles.switchLine}>
          Đã xác thực?{" "}
          <Link href={withNext(loginHref, nextParam)} className={styles.switchLink}>
            Đăng nhập
          </Link>
        </p>
      </form>
    );
  }

  return (
    <form ref={formRef} className={styles.panel} onSubmit={handleRegisterSubmit} noValidate>
      <div className={styles.field} data-shake-target="">
        <div className={styles.floatingWrap}>
          <input
            id="reg-name"
            name="fullName"
            type="text"
            autoComplete="name"
            placeholder=" "
            className={`${styles.floatingInput} ${
              fieldErrors.fullName ? styles.floatingInputInvalid : ""
            }`}
            aria-invalid={Boolean(fieldErrors.fullName) || undefined}
            aria-describedby={fieldErrors.fullName ? "reg-name-error" : undefined}
            value={fullName}
            onChange={(e) => {
              setFullName(e.target.value);
              clearFieldError("fullName");
            }}
            disabled={submitting}
            required
          />
          <label
            htmlFor="reg-name"
            className={`${styles.floatingLabel} ${fullName ? styles.floatingLabelUp : ""}`}
          >
            Họ và tên
          </label>
        </div>
        {fieldErrors.fullName && (
          <p id="reg-name-error" className={styles.error} role="alert">
            {fieldErrors.fullName}
          </p>
        )}
      </div>

      <div className={styles.fieldRow}>
        <div className={styles.field} data-shake-target="">
          <div className={styles.floatingWrap}>
            <input
              id="reg-email"
              name="email"
              type="email"
              autoComplete="email"
              placeholder=" "
              className={`${styles.floatingInput} ${
                fieldErrors.email ? styles.floatingInputInvalid : ""
              }`}
              aria-invalid={Boolean(fieldErrors.email) || undefined}
              aria-describedby={fieldErrors.email ? "reg-email-error" : undefined}
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                clearFieldError("email");
              }}
              disabled={submitting}
              required
            />
            <label
              htmlFor="reg-email"
              className={`${styles.floatingLabel} ${email ? styles.floatingLabelUp : ""}`}
            >
              Email
            </label>
          </div>
          {fieldErrors.email && (
            <p id="reg-email-error" className={styles.error} role="alert">
              {fieldErrors.email}
            </p>
          )}
        </div>

        <div className={styles.field} data-shake-target="">
          <div className={styles.floatingWrap}>
            <input
              id="reg-phone"
              name="phone"
              type="tel"
              autoComplete="tel"
              placeholder=" "
              className={`${styles.floatingInput} ${
                fieldErrors.phone ? styles.floatingInputInvalid : ""
              }`}
              aria-invalid={Boolean(fieldErrors.phone) || undefined}
              aria-describedby={fieldErrors.phone ? "reg-phone-error" : undefined}
              value={phone}
              onChange={(e) => {
                setPhone(e.target.value);
                clearFieldError("phone");
              }}
              disabled={submitting}
            />
            <label
              htmlFor="reg-phone"
              className={`${styles.floatingLabel} ${phone ? styles.floatingLabelUp : ""}`}
            >
              Số điện thoại (tùy chọn)
            </label>
          </div>
          {fieldErrors.phone && (
            <p id="reg-phone-error" className={styles.error} role="alert">
              {fieldErrors.phone}
            </p>
          )}
        </div>
      </div>

      <div className={styles.fieldRow}>
        <div className={styles.field} data-shake-target="">
          <div className={`${styles.floatingWrap} ${styles.floatingWrapPassword}`}>
            <input
              id="reg-pass"
              name="password"
              type={showPassword ? "text" : "password"}
              autoComplete="new-password"
              placeholder=" "
              className={`${styles.floatingInput} ${styles.floatingInputPassword} ${
                fieldErrors.password ? styles.floatingInputInvalid : ""
              }`}
              aria-invalid={Boolean(fieldErrors.password) || undefined}
              aria-describedby={fieldErrors.password ? "reg-pass-error" : undefined}
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                clearFieldError("password");
              }}
              disabled={submitting}
              required
            />
            <label
              htmlFor="reg-pass"
              className={`${styles.floatingLabel} ${password ? styles.floatingLabelUp : ""}`}
            >
              Mật khẩu (≥ 8 ký tự)
            </label>
            <button
              type="button"
              className={styles.toggleBtn}
              aria-pressed={showPassword}
              aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
              onClick={() => setShowPassword((v) => !v)}
            >
              {showPassword ? "Ẩn" : "Hiện"}
            </button>
          </div>
          {fieldErrors.password && (
            <p id="reg-pass-error" className={styles.error} role="alert">
              {fieldErrors.password}
            </p>
          )}
        </div>

        <div className={styles.field} data-shake-target="">
          <div className={`${styles.floatingWrap} ${styles.floatingWrapPassword}`}>
            <input
              id="reg-confirm"
              name="confirmPassword"
              type={showConfirm ? "text" : "password"}
              autoComplete="new-password"
              placeholder=" "
              className={`${styles.floatingInput} ${styles.floatingInputPassword} ${
                fieldErrors.confirmPassword ? styles.floatingInputInvalid : ""
              }`}
              aria-invalid={Boolean(fieldErrors.confirmPassword) || undefined}
              aria-describedby={fieldErrors.confirmPassword ? "reg-confirm-error" : undefined}
              value={confirmPassword}
              onChange={(e) => {
                setConfirmPassword(e.target.value);
                clearFieldError("confirmPassword");
              }}
              disabled={submitting}
              required
            />
            <label
              htmlFor="reg-confirm"
              className={`${styles.floatingLabel} ${confirmPassword ? styles.floatingLabelUp : ""}`}
            >
              Nhập lại mật khẩu
            </label>
            <button
              type="button"
              className={styles.toggleBtn}
              aria-pressed={showConfirm}
              aria-label={showConfirm ? "Ẩn mật khẩu nhập lại" : "Hiện mật khẩu nhập lại"}
              onClick={() => setShowConfirm((v) => !v)}
            >
              {showConfirm ? "Ẩn" : "Hiện"}
            </button>
          </div>
          {fieldErrors.confirmPassword && (
            <p id="reg-confirm-error" className={styles.error} role="alert">
              {fieldErrors.confirmPassword}
            </p>
          )}
        </div>
      </div>

      {formError && (
        <div data-shake-alert="" className={styles.errorStack} role="alert">
          <p className={styles.error}>{formError}</p>
        </div>
      )}

      <Button
        type="submit"
        variant="primary"
        size="lg"
        fullWidth
        loading={submitting}
        loadingLabel="Đang tạo tài khoản"
      >
        Tạo tài khoản
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

export interface ForgotPasswordFormProps {
  loginHref?: string;
  registerHref?: string;
}

function ForgotPasswordFormInner({
  loginHref = "/dang-nhap",
  registerHref = "/dang-ky",
}: ForgotPasswordFormProps) {
  const formRef = useRef<HTMLFormElement | null>(null);
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(formRef.current);
    }
  }, [errorPulse]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    if (!EMAIL_RE.test(cleanEmail)) {
      setError("Vui lòng nhập địa chỉ email hợp lệ (ví dụ: tenban@nexusmart.vn).");
      setErrorPulse((n) => n + 1);
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await api<{ ok?: boolean }>("/api/auth/forgot-password", {
        method: "POST",
        body: { email: cleanEmail },
      });
      setSubmitted(true);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Chưa thể gửi yêu cầu lúc này. Vui lòng thử lại sau ít phút."
      );
      setErrorPulse((n) => n + 1);
    } finally {
      setSubmitting(false);
    }
  };

  if (submitted) {
    return (
      <div className={styles.panel} role="status" aria-live="polite">
        <p className={styles.note}>
          Nếu địa chỉ email <strong>{email.trim()}</strong> đã đăng ký trên hệ thống, chúng tôi đã
          gửi liên kết đặt lại mật khẩu có hiệu lực trong 15 phút. Vui lòng kiểm tra hộp thư đến
          hoặc thư rác.
        </p>
        <div className={styles.sessionActions}>
          <Button variant="primary" size="md" href={loginHref}>
            Quay lại đăng nhập
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="md"
            onClick={() => {
              setSubmitted(false);
              setError(null);
            }}
          >
            Gửi lại với email khác
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form ref={formRef} className={styles.panel} onSubmit={handleSubmit} noValidate>
      <p className={styles.note}>
        Nhập địa chỉ email đã đăng ký để nhận liên kết đặt lại mật khẩu (hiệu lực 15 phút, dùng một
        lần).
      </p>

      <div className={styles.field} data-shake-target="">
        <div className={styles.floatingWrap}>
          <input
            id="forgot-email"
            name="email"
            type="email"
            autoComplete="email"
            placeholder=" "
            className={`${styles.floatingInput} ${error ? styles.floatingInputInvalid : ""}`}
            aria-invalid={Boolean(error) || undefined}
            aria-describedby={error ? "forgot-email-error" : undefined}
            value={email}
            onChange={(e) => {
              setEmail(e.target.value);
              if (error) setError(null);
            }}
            disabled={submitting}
            required
          />
          <label
            htmlFor="forgot-email"
            className={`${styles.floatingLabel} ${email ? styles.floatingLabelUp : ""}`}
          >
            Địa chỉ email đã đăng ký
          </label>
        </div>
        {error && (
          <p id="forgot-email-error" className={styles.error} role="alert">
            {error}
          </p>
        )}
      </div>

      <Button
        type="submit"
        variant="primary"
        size="lg"
        fullWidth
        loading={submitting}
        loadingLabel="Đang gửi liên kết"
      >
        Gửi liên kết đặt lại mật khẩu
      </Button>

      <div className={styles.switchLine}>
        <span>
          Nhớ mật khẩu rồi?{" "}
          <Link href={loginHref} className={styles.switchLink}>
            Quay lại đăng nhập
          </Link>
        </span>
        <Link href={registerHref} className={styles.switchLink}>
          Tạo tài khoản
        </Link>
      </div>
    </form>
  );
}

/** Password reset request form. Neutral response prevents email enumeration. */
export function ForgotPasswordForm(props: ForgotPasswordFormProps) {
  return (
    <Suspense fallback={<FormFallback title="Đang mở biểu mẫu quên mật khẩu..." />}>
      <ForgotPasswordFormInner {...props} />
    </Suspense>
  );
}

export interface ResetPasswordFormProps {
  loginHref?: string;
  forgotHref?: string;
}

function ResetPasswordFormInner({
  loginHref = "/dang-nhap",
  forgotHref = "/quen-mat-khau",
}: ResetPasswordFormProps) {
  const searchParams = useSearchParams();
  const { setUser } = useSession();

  const urlToken = (searchParams.get("token") ?? "").trim();
  const [token, setToken] = useState<string>(() => urlToken);
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [completed, setCompleted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [errorPulse, setErrorPulse] = useState(0);
  const formRef = useRef<HTMLFormElement | null>(null);

  useEffect(() => {
    if (!urlToken) return;
    setToken(urlToken);
    if (typeof window !== "undefined") {
      const cleanUrl = new URL(window.location.href);
      if (cleanUrl.searchParams.has("token")) {
        cleanUrl.searchParams.delete("token");
        const nextSearch = cleanUrl.searchParams.toString();
        const nextPath = `${cleanUrl.pathname}${nextSearch ? `?${nextSearch}` : ""}${cleanUrl.hash}`;
        window.history.replaceState(window.history.state, "", nextPath);
      }
    }
  }, [urlToken]);

  useEffect(() => {
    if (errorPulse > 0) {
      focusAndShakeInvalid(formRef.current);
    }
  }, [errorPulse]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!token) {
      setError("Liên kết đặt lại mật khẩu không hợp lệ hoặc thiếu mã xác thực.");
      setErrorPulse((n) => n + 1);
      return;
    }
    if (newPassword.length < 8) {
      setError("Mật khẩu mới cần có tối thiểu 8 ký tự.");
      setErrorPulse((n) => n + 1);
      return;
    }
    if (newPassword !== confirmPassword) {
      setError("Mật khẩu xác nhận chưa khớp với mật khẩu mới.");
      setErrorPulse((n) => n + 1);
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await api<{ ok?: boolean }>("/api/auth/reset-password", {
        method: "POST",
        body: { token, new_password: newPassword },
      });
      setUser(null);
      setToken("");
      setCompleted(true);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "Liên kết đặt lại mật khẩu không hợp lệ hoặc đã hết hạn. Vui lòng yêu cầu liên kết mới."
      );
      setErrorPulse((n) => n + 1);
    } finally {
      setSubmitting(false);
    }
  };

  if (completed) {
    return (
      <div className={styles.panel} role="status" aria-live="polite">
        <p className={styles.note}>
          Mật khẩu đã được đặt lại thành công. Toàn bộ phiên đăng nhập cũ đã được đăng xuất để bảo
          mật. Bạn có thể đăng nhập bằng mật khẩu mới ngay bây giờ.
        </p>
        <Button variant="primary" size="lg" fullWidth href={`${loginHref}?reset=1`}>
          Đăng nhập ngay
        </Button>
      </div>
    );
  }

  if (!token) {
    return (
      <div className={styles.panel}>
        <p className={styles.error} role="alert">
          Liên kết đặt lại mật khẩu thiếu mã xác thực hoặc chưa đầy đủ. Vui lòng mở lại đúng liên
          kết trong email hoặc yêu cầu gửi liên kết mới.
        </p>
        <div className={styles.sessionActions}>
          <Button variant="primary" size="md" href={forgotHref}>
            Yêu cầu liên kết mới
          </Button>
          <Button variant="secondary" size="md" href={loginHref}>
            Quay lại đăng nhập
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form ref={formRef} className={styles.panel} onSubmit={handleSubmit} noValidate>
      <p className={styles.note}>
        Tạo mật khẩu mới (tối thiểu 8 ký tự). Mọi phiên đăng nhập trước đó sẽ tự động đăng xuất sau
        khi đổi thành công.
      </p>

      <div className={styles.field} data-shake-target="">
        <div className={`${styles.floatingWrap} ${styles.floatingWrapPassword}`}>
          <input
            id="reset-new-password"
            name="newPassword"
            type={showPassword ? "text" : "password"}
            autoComplete="new-password"
            placeholder=" "
            className={`${styles.floatingInput} ${styles.floatingInputPassword} ${
              error ? styles.floatingInputInvalid : ""
            }`}
            aria-invalid={Boolean(error) || undefined}
            value={newPassword}
            onChange={(e) => {
              setNewPassword(e.target.value);
              if (error) setError(null);
            }}
            disabled={submitting}
            required
          />
          <label
            htmlFor="reset-new-password"
            className={`${styles.floatingLabel} ${newPassword ? styles.floatingLabelUp : ""}`}
          >
            Mật khẩu mới (≥ 8 ký tự)
          </label>
          <button
            type="button"
            className={styles.toggleBtn}
            aria-pressed={showPassword}
            aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
            onClick={() => setShowPassword((v) => !v)}
          >
            {showPassword ? "Ẩn" : "Hiện"}
          </button>
        </div>
      </div>

      <div className={styles.field} data-shake-target="">
        <div className={styles.floatingWrap}>
          <input
            id="reset-confirm-password"
            name="confirmPassword"
            type={showPassword ? "text" : "password"}
            autoComplete="new-password"
            placeholder=" "
            className={`${styles.floatingInput} ${error ? styles.floatingInputInvalid : ""}`}
            aria-invalid={Boolean(error) || undefined}
            value={confirmPassword}
            onChange={(e) => {
              setConfirmPassword(e.target.value);
              if (error) setError(null);
            }}
            disabled={submitting}
            required
          />
          <label
            htmlFor="reset-confirm-password"
            className={`${styles.floatingLabel} ${confirmPassword ? styles.floatingLabelUp : ""}`}
          >
            Xác nhận mật khẩu mới
          </label>
        </div>
      </div>

      {error && (
        <div role="alert" data-shake-alert="" className={styles.errorStack}>
          <p className={styles.error}>{error}</p>
          <Link href={forgotHref} className={styles.switchLink}>
            Yêu cầu liên kết đặt lại mới
          </Link>
        </div>
      )}

      <Button
        type="submit"
        variant="primary"
        size="lg"
        fullWidth
        loading={submitting}
        loadingLabel="Đang cập nhật mật khẩu"
      >
        Lưu mật khẩu mới
      </Button>

      <p className={styles.switchLine}>
        <Link href={loginHref} className={styles.switchLink}>
          Quay lại trang đăng nhập
        </Link>
      </p>
    </form>
  );
}

/** Password reset completion form. Immediately scrubs `?token=` from browser history. */
export function ResetPasswordForm(props: ResetPasswordFormProps) {
  return (
    <Suspense fallback={<FormFallback title="Đang mở biểu mẫu đặt lại mật khẩu..." />}>
      <ResetPasswordFormInner {...props} />
    </Suspense>
  );
}
