"use client";

import React, { Suspense, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { AlertCircle, CheckCircle2, Eye, EyeOff, Info, Loader2 } from "lucide-react";
import { useSession, type SessionUser } from "@/components/SessionProvider";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { focusAndShakeInvalid } from "@/features/commerce/formFeedback";
import { ApiError, api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { safeNextPath } from "./accountTypes";
import { resolvePostLoginPath, type AuthVariant } from "./authRedirect";

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
  /** Notifies wrapper shells when switching between register and OTP steps. */
  onStepChange?: (step: "register" | "otp") => void;
}

interface PasswordStrength {
  score: number;
  label: string;
  indicatorClass: string;
  textClass: string;
}

function calcPasswordStrength(pw: string): PasswordStrength {
  if (!pw) {
    return {
      score: 0,
      label: "Chưa nhập",
      indicatorClass: "[&>[data-slot=progress-indicator]]:bg-muted-foreground",
      textClass: "text-muted-foreground",
    };
  }
  let points = 0;
  if (pw.length >= 8) points += 25;
  if (pw.length >= 12) points += 15;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) points += 20;
  if (/\d/.test(pw)) points += 20;
  if (/[^a-zA-Z0-9]/.test(pw)) points += 20;
  if (points >= 80) {
    return {
      score: 100,
      label: "Mạnh",
      indicatorClass: "[&>[data-slot=progress-indicator]]:bg-badge-success-ink",
      textClass: "text-badge-success-ink",
    };
  }
  if (points >= 55) {
    return {
      score: 65,
      label: "Tốt",
      indicatorClass: "[&>[data-slot=progress-indicator]]:bg-brand",
      textClass: "text-brand",
    };
  }
  if (points >= 35) {
    return {
      score: 40,
      label: "Trung bình",
      indicatorClass: "[&>[data-slot=progress-indicator]]:bg-badge-warning-ink",
      textClass: "text-badge-warning-ink",
    };
  }
  return {
    score: 20,
    label: "Yếu",
    indicatorClass: "[&>[data-slot=progress-indicator]]:bg-destructive",
    textClass: "text-destructive",
  };
}

function withNext(href: string, nextParam: string | null): string {
  if (!nextParam) return href;
  const sep = href.includes("?") ? "&" : "?";
  return `${href}${sep}next=${encodeURIComponent(nextParam)}`;
}

function FormFallback({ title }: { title: string }) {
  return (
    <div className="space-y-4" role="status" aria-label={title}>
      <div className="space-y-2">
        <Skeleton className="h-4 w-20" />
        <Skeleton className="h-10 w-full" />
      </div>
      <div className="space-y-2">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-10 w-full" />
      </div>
      <Skeleton className="h-10 w-full" />
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
    <form ref={formRef} className="space-y-4" onSubmit={handleSubmit} noValidate>
      {verifiedNotice && (
        <Alert role="status" className="border-border bg-badge-success-bg/40 shadow-none">
          <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
          <AlertDescription className="text-foreground">
            Email đã xác thực. Nhập mật khẩu để đăng nhập.
          </AlertDescription>
        </Alert>
      )}
      {resetNotice && (
        <Alert role="status" className="border-border bg-badge-success-bg/40 shadow-none">
          <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
          <AlertDescription className="text-foreground">
            Mật khẩu đã được đặt lại thành công. Nhập mật khẩu mới để đăng nhập.
          </AlertDescription>
        </Alert>
      )}
      {expiredParam && !isAuthenticated && (
        <Alert role="status" className="border-border bg-badge-warning-bg/50 shadow-none">
          <Info className="size-4 text-badge-warning-ink" aria-hidden="true" />
          <AlertDescription className="text-foreground">
            Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.
          </AlertDescription>
        </Alert>
      )}

      {isAuthenticated && user && (
        <div className="space-y-3 rounded-xl border border-border bg-muted/60 p-4 text-sm text-foreground">
          <p>
            Đang đăng nhập bằng <strong className="font-semibold">{user.email}</strong>.
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" asChild>
              <Link href={destinationFor(user)}>Tiếp tục</Link>
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

      <FieldGroup className="gap-4">
        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.email) || undefined}>
          <FieldLabel htmlFor="login-email">Email</FieldLabel>
          <Input
            id="login-email"
            name="email"
            type="email"
            autoComplete="email"
            placeholder="tenban@nexusmart.vn"
            className="h-10 bg-card"
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
          {fieldErrors.email && <FieldError id="login-email-error">{fieldErrors.email}</FieldError>}
        </Field>

        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.password) || undefined}>
          <div className="flex items-center justify-between gap-2">
            <FieldLabel htmlFor="login-password">Mật khẩu</FieldLabel>
            <Link
              href="/quen-mat-khau"
              className="text-xs font-medium text-brand underline-offset-4 hover:underline"
            >
              Quên mật khẩu?
            </Link>
          </div>
          <div className="relative">
            <Input
              id="login-password"
              name="password"
              type={showPassword ? "text" : "password"}
              autoComplete="current-password"
              placeholder="Nhập mật khẩu"
              className="h-10 bg-card pr-11"
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
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-pressed={showPassword}
              aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
              onClick={() => setShowPassword((v) => !v)}
              className="absolute top-0 right-0 size-10 text-muted-foreground hover:bg-transparent hover:text-foreground"
            >
              {showPassword ? (
                <EyeOff className="size-4" aria-hidden="true" />
              ) : (
                <Eye className="size-4" aria-hidden="true" />
              )}
            </Button>
          </div>
          {fieldErrors.password && (
            <FieldError id="login-password-error">{fieldErrors.password}</FieldError>
          )}
        </Field>
      </FieldGroup>

      {formError && (
        <Alert variant="destructive" data-shake-alert="" className="shadow-none">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription className="space-y-1.5">
            <p>{formError}</p>
            {needsVerificationEmail && (
              <Link
                href={withNext(
                  `/dang-ky?step=otp&email=${encodeURIComponent(needsVerificationEmail)}`,
                  nextParam
                )}
                className="inline-block font-medium text-brand underline underline-offset-4"
              >
                Nhập mã xác thực email
              </Link>
            )}
          </AlertDescription>
        </Alert>
      )}

      <Button type="submit" size="lg" className="w-full" disabled={submitting}>
        {submitting ? (
          <>
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            <span>Đang đăng nhập</span>
          </>
        ) : (
          "Đăng nhập"
        )}
      </Button>

      {variant === "site" ? (
        <div className="flex flex-wrap items-center justify-between gap-2 pt-1 text-sm text-muted-foreground">
          <span>
            Chưa có tài khoản?{" "}
            <Link
              href={withNext(registerHref, nextParam)}
              className="font-medium text-brand underline-offset-4 hover:underline"
            >
              Tạo tài khoản
            </Link>
          </span>
          <Link
            href="/tra-cuu-don-hang"
            className="font-medium text-brand underline-offset-4 hover:underline"
          >
            Tra cứu đơn
          </Link>
        </div>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-2 pt-1 text-sm text-muted-foreground">
          <span>
            Chưa có tài khoản?{" "}
            <Link
              href={withNext(registerHref, nextParam)}
              className="font-medium text-brand underline-offset-4 hover:underline"
            >
              Đăng ký mở gian hàng
            </Link>
          </span>
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

  const strength = calcPasswordStrength(password);

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
      <form ref={formRef} className="space-y-4" onSubmit={handleVerifyOtpSubmit} noValidate>
        <Alert role="status" className="border-border bg-muted/60 shadow-none">
          <Info className="size-4 text-brand" aria-hidden="true" />
          <AlertDescription className="text-foreground">
            <span>
              Mã 6 số đang được gửi tới <strong className="font-semibold">{email}</strong>, hiệu lực
              10 phút. Chưa thấy sau 1–2 phút: xem thư rác hoặc gửi lại.
            </span>
          </AlertDescription>
        </Alert>

        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.otp) || undefined}>
          <FieldLabel htmlFor="otp-code">Mã xác thực 6 chữ số</FieldLabel>
          <div className="relative grid grid-cols-6 gap-2 sm:gap-2.5">
            {OTP_SLOTS.map((idx) => {
              const digit = otp[idx] ?? "";
              const isActive = otpFocused && activeCellIndex === idx;
              return (
                <div
                  key={idx}
                  aria-hidden="true"
                  className={cn(
                    "relative flex h-12 items-center justify-center rounded-md border bg-card text-xl font-semibold text-foreground tabular-nums transition-colors sm:h-14 sm:text-2xl",
                    digit ? "border-foreground bg-muted/40" : "border-input",
                    isActive && "border-2 border-brand bg-card ring-2 ring-ring/25",
                    fieldErrors.otp && "border-destructive"
                  )}
                >
                  {digit ? (
                    <span>{digit}</span>
                  ) : isActive ? (
                    <span className="h-5 w-0.5 animate-pulse bg-brand" />
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
              className="absolute inset-0 z-10 h-full w-full cursor-text border-0 bg-transparent text-base tracking-[0.2em] text-transparent caret-transparent outline-none selection:bg-transparent selection:text-transparent"
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
          {fieldErrors.otp && <FieldError id="otp-code-error">{fieldErrors.otp}</FieldError>}
        </Field>

        {notice && (
          <p className="text-xs font-medium text-badge-success-ink" role="status">
            {notice}
          </p>
        )}
        {formError && (
          <Alert variant="destructive" data-shake-alert="" className="shadow-none">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertDescription>{formError}</AlertDescription>
          </Alert>
        )}

        <Button type="submit" size="lg" className="w-full" disabled={submitting}>
          {submitting ? (
            <>
              <Loader2 className="size-4 animate-spin" aria-hidden="true" />
              <span>Đang xác thực</span>
            </>
          ) : (
            "Xác thực và hoàn tất"
          )}
        </Button>

        <div className="flex flex-wrap items-center justify-between gap-2">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className="min-h-10"
            disabled={resendCooldown > 0 || resending}
            onClick={() => void handleResendOtp()}
          >
            {resending ? (
              <>
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                <span>Đang gửi lại</span>
              </>
            ) : resendCooldown > 0 ? (
              `Gửi lại sau ${resendCooldown}s`
            ) : (
              "Gửi lại mã"
            )}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="min-h-10 text-muted-foreground hover:text-foreground"
            onClick={() => {
              setStep("register");
              setErrorPulse(0);
              setFieldErrors({});
              setFormError(null);
              setNotice(null);
            }}
          >
            Sửa thông tin
          </Button>
        </div>

        <p className="pt-1 text-sm text-muted-foreground">
          Đã xác thực?{" "}
          <Link
            href={withNext(loginHref, nextParam)}
            className="font-medium text-brand underline-offset-4 hover:underline"
          >
            Đăng nhập
          </Link>
        </p>
      </form>
    );
  }

  return (
    <form ref={formRef} className="space-y-4" onSubmit={handleRegisterSubmit} noValidate>
      <FieldGroup className="gap-3.5">
        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.fullName) || undefined}>
          <FieldLabel htmlFor="reg-name">Họ và tên</FieldLabel>
          <Input
            id="reg-name"
            name="fullName"
            type="text"
            autoComplete="name"
            placeholder="Nguyễn Văn An"
            className="h-10 bg-card"
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
          {fieldErrors.fullName && (
            <FieldError id="reg-name-error">{fieldErrors.fullName}</FieldError>
          )}
        </Field>

        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.email) || undefined}>
          <FieldLabel htmlFor="reg-email">Email</FieldLabel>
          <Input
            id="reg-email"
            name="email"
            type="email"
            autoComplete="email"
            placeholder="tenban@nexusmart.vn"
            className="h-10 bg-card"
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
          {fieldErrors.email && <FieldError id="reg-email-error">{fieldErrors.email}</FieldError>}
        </Field>

        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.phone) || undefined}>
          <FieldLabel htmlFor="reg-phone">Số điện thoại (tùy chọn)</FieldLabel>
          <Input
            id="reg-phone"
            name="phone"
            type="tel"
            autoComplete="tel"
            placeholder="0901234567"
            className="h-10 bg-card"
            aria-invalid={Boolean(fieldErrors.phone) || undefined}
            aria-describedby={fieldErrors.phone ? "reg-phone-error" : undefined}
            value={phone}
            onChange={(e) => {
              setPhone(e.target.value);
              clearFieldError("phone");
            }}
            disabled={submitting}
          />
          {fieldErrors.phone && <FieldError id="reg-phone-error">{fieldErrors.phone}</FieldError>}
        </Field>

        <Field data-shake-target="" data-invalid={Boolean(fieldErrors.password) || undefined}>
          <FieldLabel htmlFor="reg-pass">Mật khẩu (≥ 8 ký tự)</FieldLabel>
          <div className="relative">
            <Input
              id="reg-pass"
              name="password"
              type={showPassword ? "text" : "password"}
              autoComplete="new-password"
              placeholder="Tối thiểu 8 ký tự"
              className="h-10 bg-card pr-11"
              aria-invalid={Boolean(fieldErrors.password) || undefined}
              aria-describedby={fieldErrors.password ? "reg-pass-error" : "reg-pass-strength"}
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                clearFieldError("password");
              }}
              disabled={submitting}
              required
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-pressed={showPassword}
              aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
              onClick={() => setShowPassword((v) => !v)}
              className="absolute top-0 right-0 size-10 text-muted-foreground hover:bg-transparent hover:text-foreground"
            >
              {showPassword ? (
                <EyeOff className="size-4" aria-hidden="true" />
              ) : (
                <Eye className="size-4" aria-hidden="true" />
              )}
            </Button>
          </div>
          <div id="reg-pass-strength" className="space-y-1 pt-0.5">
            <Progress
              value={strength.score}
              className={cn("h-1.5", strength.indicatorClass)}
              aria-label="Độ mạnh mật khẩu"
            />
            <p className="text-xs text-muted-foreground">
              Độ mạnh mật khẩu:{" "}
              <span className={cn("font-medium", strength.textClass)}>{strength.label}</span>
            </p>
          </div>
          {fieldErrors.password && (
            <FieldError id="reg-pass-error">{fieldErrors.password}</FieldError>
          )}
        </Field>

        <Field
          data-shake-target=""
          data-invalid={Boolean(fieldErrors.confirmPassword) || undefined}
        >
          <FieldLabel htmlFor="reg-confirm">Nhập lại mật khẩu</FieldLabel>
          <div className="relative">
            <Input
              id="reg-confirm"
              name="confirmPassword"
              type={showConfirm ? "text" : "password"}
              autoComplete="new-password"
              placeholder="Nhập lại mật khẩu"
              className="h-10 bg-card pr-11"
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
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-pressed={showConfirm}
              aria-label={showConfirm ? "Ẩn mật khẩu nhập lại" : "Hiện mật khẩu nhập lại"}
              onClick={() => setShowConfirm((v) => !v)}
              className="absolute top-0 right-0 size-10 text-muted-foreground hover:bg-transparent hover:text-foreground"
            >
              {showConfirm ? (
                <EyeOff className="size-4" aria-hidden="true" />
              ) : (
                <Eye className="size-4" aria-hidden="true" />
              )}
            </Button>
          </div>
          {fieldErrors.confirmPassword && (
            <FieldError id="reg-confirm-error">{fieldErrors.confirmPassword}</FieldError>
          )}
        </Field>
      </FieldGroup>

      {formError && (
        <Alert variant="destructive" data-shake-alert="" className="shadow-none">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      <Button type="submit" size="lg" className="w-full" disabled={submitting}>
        {submitting ? (
          <>
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            <span>Đang tạo tài khoản</span>
          </>
        ) : (
          "Tạo tài khoản"
        )}
      </Button>

      <p className="pt-1 text-sm text-muted-foreground">
        Đã có tài khoản?{" "}
        <Link
          href={withNext(loginHref, nextParam)}
          className="font-medium text-brand underline-offset-4 hover:underline"
        >
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
      <div className="space-y-4" role="status" aria-live="polite">
        <Alert role="status" className="border-border bg-badge-success-bg/40 shadow-none">
          <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
          <AlertDescription className="text-foreground">
            <span>
              Nếu địa chỉ email <strong className="font-semibold">{email.trim()}</strong> đã đăng ký
              trên hệ thống, chúng tôi đã gửi liên kết đặt lại mật khẩu có hiệu lực trong 15 phút.
              Vui lòng kiểm tra hộp thư đến hoặc thư rác.
            </span>
          </AlertDescription>
        </Alert>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="lg" asChild>
            <Link href={loginHref}>Quay lại đăng nhập</Link>
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="lg"
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
    <form ref={formRef} className="space-y-4" onSubmit={handleSubmit} noValidate>
      <Alert role="status" className="border-border bg-muted/60 shadow-none">
        <Info className="size-4 text-brand" aria-hidden="true" />
        <AlertDescription className="text-foreground">
          Nhập địa chỉ email đã đăng ký để nhận liên kết đặt lại mật khẩu (hiệu lực 15 phút, dùng
          một lần).
        </AlertDescription>
      </Alert>

      <Field data-shake-target="" data-invalid={Boolean(error) || undefined}>
        <FieldLabel htmlFor="forgot-email">Địa chỉ email đã đăng ký</FieldLabel>
        <Input
          id="forgot-email"
          name="email"
          type="email"
          autoComplete="email"
          placeholder="tenban@nexusmart.vn"
          className="h-10 bg-card"
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
        {error && <FieldError id="forgot-email-error">{error}</FieldError>}
      </Field>

      <Button type="submit" size="lg" className="w-full" disabled={submitting}>
        {submitting ? (
          <>
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            <span>Đang gửi liên kết</span>
          </>
        ) : (
          "Gửi liên kết đặt lại mật khẩu"
        )}
      </Button>

      <div className="flex flex-wrap items-center justify-between gap-2 pt-1 text-sm text-muted-foreground">
        <span>
          Nhớ mật khẩu rồi?{" "}
          <Link
            href={loginHref}
            className="font-medium text-brand underline-offset-4 hover:underline"
          >
            Quay lại đăng nhập
          </Link>
        </span>
        <Link
          href={registerHref}
          className="font-medium text-brand underline-offset-4 hover:underline"
        >
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

  const strength = calcPasswordStrength(newPassword);

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
      <div className="space-y-4" role="status" aria-live="polite">
        <Alert role="status" className="border-border bg-badge-success-bg/40 shadow-none">
          <CheckCircle2 className="size-4 text-badge-success-ink" aria-hidden="true" />
          <AlertDescription className="text-foreground">
            Mật khẩu đã được đặt lại thành công. Toàn bộ phiên đăng nhập cũ đã được đăng xuất để bảo
            mật. Bạn có thể đăng nhập bằng mật khẩu mới ngay bây giờ.
          </AlertDescription>
        </Alert>
        <Button size="lg" className="w-full" asChild>
          <Link href={`${loginHref}?reset=1`}>Đăng nhập ngay</Link>
        </Button>
      </div>
    );
  }

  if (!token) {
    return (
      <div className="space-y-4">
        <Alert variant="destructive" className="shadow-none">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription>
            Liên kết đặt lại mật khẩu thiếu mã xác thực hoặc chưa đầy đủ. Vui lòng mở lại đúng liên
            kết trong email hoặc yêu cầu gửi liên kết mới.
          </AlertDescription>
        </Alert>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="lg" asChild>
            <Link href={forgotHref}>Yêu cầu liên kết mới</Link>
          </Button>
          <Button variant="secondary" size="lg" asChild>
            <Link href={loginHref}>Quay lại đăng nhập</Link>
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form ref={formRef} className="space-y-4" onSubmit={handleSubmit} noValidate>
      <Alert role="status" className="border-border bg-muted/60 shadow-none">
        <Info className="size-4 text-brand" aria-hidden="true" />
        <AlertDescription className="text-foreground">
          Tạo mật khẩu mới (tối thiểu 8 ký tự). Mọi phiên đăng nhập trước đó sẽ tự động đăng xuất
          sau khi đổi thành công.
        </AlertDescription>
      </Alert>

      <FieldGroup className="gap-4">
        <Field data-shake-target="" data-invalid={Boolean(error) || undefined}>
          <FieldLabel htmlFor="reset-new-password">Mật khẩu mới (≥ 8 ký tự)</FieldLabel>
          <div className="relative">
            <Input
              id="reset-new-password"
              name="newPassword"
              type={showPassword ? "text" : "password"}
              autoComplete="new-password"
              placeholder="Tối thiểu 8 ký tự"
              className="h-10 bg-card pr-11"
              aria-invalid={Boolean(error) || undefined}
              value={newPassword}
              onChange={(e) => {
                setNewPassword(e.target.value);
                if (error) setError(null);
              }}
              disabled={submitting}
              required
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-pressed={showPassword}
              aria-label={showPassword ? "Ẩn mật khẩu" : "Hiện mật khẩu"}
              onClick={() => setShowPassword((v) => !v)}
              className="absolute top-0 right-0 size-10 text-muted-foreground hover:bg-transparent hover:text-foreground"
            >
              {showPassword ? (
                <EyeOff className="size-4" aria-hidden="true" />
              ) : (
                <Eye className="size-4" aria-hidden="true" />
              )}
            </Button>
          </div>
          <div className="space-y-1 pt-0.5">
            <Progress
              value={strength.score}
              className={cn("h-1.5", strength.indicatorClass)}
              aria-label="Độ mạnh mật khẩu"
            />
            <p className="text-xs text-muted-foreground">
              Độ mạnh mật khẩu:{" "}
              <span className={cn("font-medium", strength.textClass)}>{strength.label}</span>
            </p>
          </div>
        </Field>

        <Field data-shake-target="" data-invalid={Boolean(error) || undefined}>
          <FieldLabel htmlFor="reset-confirm-password">Xác nhận mật khẩu mới</FieldLabel>
          <Input
            id="reset-confirm-password"
            name="confirmPassword"
            type={showPassword ? "text" : "password"}
            autoComplete="new-password"
            placeholder="Nhập lại mật khẩu mới"
            className="h-10 bg-card"
            aria-invalid={Boolean(error) || undefined}
            value={confirmPassword}
            onChange={(e) => {
              setConfirmPassword(e.target.value);
              if (error) setError(null);
            }}
            disabled={submitting}
            required
          />
        </Field>
      </FieldGroup>

      {error && (
        <Alert variant="destructive" data-shake-alert="" className="shadow-none">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription className="space-y-1.5">
            <p>{error}</p>
            <Link
              href={forgotHref}
              className="inline-block font-medium text-brand underline underline-offset-4"
            >
              Yêu cầu liên kết đặt lại mới
            </Link>
          </AlertDescription>
        </Alert>
      )}

      <Button type="submit" size="lg" className="w-full" disabled={submitting}>
        {submitting ? (
          <>
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            <span>Đang cập nhật mật khẩu</span>
          </>
        ) : (
          "Lưu mật khẩu mới"
        )}
      </Button>

      <p className="pt-1 text-sm text-muted-foreground">
        <Link
          href={loginHref}
          className="font-medium text-brand underline-offset-4 hover:underline"
        >
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
