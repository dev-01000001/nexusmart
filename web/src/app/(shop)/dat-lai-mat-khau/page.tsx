"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { PRODUCTS } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import type { ResetPasswordResponse } from "@/lib/gateway-types";
import { useSession } from "@/context/SessionContext";
import { authApi, getErrorMessage } from "@/lib/api";
import { Button, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/dang-nhap/page.module.css";

const PASSWORD_TIPS = [
  {
    title: "Tối thiểu 8 ký tự",
    detail: "Nên kết hợp chữ hoa, chữ thường và chữ số để tăng độ an toàn cho tài khoản.",
  },
  {
    title: "Thu hồi phiên đăng nhập cũ",
    detail:
      "Sau khi hoàn tất đặt lại mật khẩu, mọi thiết bị đang đăng nhập trước đó sẽ phải đăng nhập lại.",
  },
  {
    title: "Liên kết chỉ dùng một lần",
    detail: "Nếu liên kết đã quá 15 phút hoặc đã được sử dụng, hãy gửi yêu cầu nhận liên kết mới.",
  },
];

function ResetPasswordContent() {
  const searchParams = useSearchParams();
  const viewState = usePageStateParam();
  const { setUser } = useSession();

  const urlToken = (searchParams.get("token") ?? "").trim();
  const [token, setToken] = useState<string>(() => urlToken);

  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [completed, setCompleted] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  useRevealOnScroll(`${viewState ?? "live"}-${completed ? "done" : "form"}`);

  const showcaseProducts = useMemo(
    () =>
      PRODUCTS.filter((p) =>
        ["prod-at-dryfit", "prod-polo-kem", "prod-short-linen", "prod-aokhoac-gio"].includes(p.id)
      ),
    []
  );

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!token) {
      setError("Liên kết đặt lại mật khẩu không hợp lệ hoặc thiếu mã xác thực.");
      return;
    }
    if (newPassword.length < 8) {
      setError("Mật khẩu mới cần có tối thiểu 8 ký tự.");
      return;
    }
    if (newPassword !== confirmPassword) {
      setError("Mật khẩu xác nhận chưa khớp với mật khẩu mới.");
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await authApi<ResetPasswordResponse>("reset-password", {
        method: "POST",
        json: {
          token,
          new_password: newPassword,
        },
      });
      setUser(null);
      setToken("");
      setCompleted(true);
    } catch (err) {
      setError(
        getErrorMessage(
          err,
          "Liên kết đặt lại mật khẩu không hợp lệ hoặc đã hết hạn. Vui lòng yêu cầu liên kết mới."
        )
      );
    } finally {
      setSubmitting(false);
    }
  };

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Đặt lại mật khẩu</h1>
            <p className={styles.authLead}>Đang kiểm tra thông tin khôi phục tài khoản.</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView kind="loading" title="Đang mở biểu mẫu đặt lại mật khẩu..." rows={3} />
        </section>
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Đặt lại mật khẩu</h1>
            <p className={styles.authLead}>Liên kết đặt lại mật khẩu không còn hiệu lực.</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView
            kind="error"
            title="Không thể đặt lại mật khẩu"
            message="Liên kết có thể đã hết hạn sau 15 phút hoặc đã được sử dụng trước đó."
            actionLabel="Yêu cầu liên kết mới"
            actionHref="/quen-mat-khau"
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
            <h1 className={shared.pageTitle}>Đặt lại mật khẩu</h1>
            <p className={styles.authLead}>Thiếu mã xác thực trong đường dẫn đặt lại mật khẩu.</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Chưa có liên kết đặt lại mật khẩu"
            message="Hãy nhập email tại trang Quên mật khẩu để nhận đường dẫn xác thực hợp lệ."
            actionLabel="Tới trang Quên mật khẩu"
            actionHref="/quen-mat-khau"
          />
        </section>
      </div>
    );
  }

  return (
    <div className={shared.page}>
      <section className={shared.section}>
        <div className={styles.authGrid}>
          <div className={styles.formCol}>
            <div className={styles.authHeader}>
              <h1 className={`${shared.pageTitle} lines`}>
                <span className="line">
                  <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                    Đặt lại mật khẩu
                  </span>
                </span>
              </h1>
              <p
                className={styles.authLead}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                Tạo mật khẩu mới cho tài khoản NexusMart của bạn. Mọi phiên đăng nhập cũ sẽ tự động
                đăng xuất để bảo mật.
              </p>
            </div>

            {completed ? (
              <div className={styles.authPanel} role="status" aria-live="polite">
                <div className={styles.sessionBar}>
                  <p className={styles.bodyText}>
                    Mật khẩu đã được đặt lại thành công. Bạn có thể đăng nhập bằng mật khẩu mới ngay
                    bây giờ.
                  </p>
                </div>
                <div className={shared.formActions}>
                  <Button variant="primary" size="lg" href="/dang-nhap?reset=1">
                    Đăng nhập ngay
                  </Button>
                </div>
              </div>
            ) : !token ? (
              <div className={styles.authPanel}>
                <p className={shared.fieldError} role="alert">
                  Liên kết đặt lại mật khẩu thiếu mã xác thực hoặc chưa đầy đủ. Vui lòng mở lại đúng
                  liên kết trong email hoặc yêu cầu gửi liên kết mới.
                </p>
                <div className={shared.formActions}>
                  <Button variant="primary" size="md" href="/quen-mat-khau">
                    Yêu cầu liên kết mới
                  </Button>
                  <Button variant="secondary" size="md" href="/dang-nhap">
                    Quay lại đăng nhập
                  </Button>
                </div>
              </div>
            ) : (
              <form
                className={styles.authPanel}
                onSubmit={(e) => void handleSubmit(e)}
                noValidate
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div className={shared.field}>
                  <label htmlFor="reset-new-password" className={shared.fieldLabel}>
                    Mật khẩu mới (tối thiểu 8 ký tự)
                  </label>
                  <div className={styles.passwordWrap}>
                    <input
                      id="reset-new-password"
                      type={showPassword ? "text" : "password"}
                      autoComplete="new-password"
                      className={shared.input}
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      placeholder="Nhập mật khẩu mới"
                      disabled={submitting}
                      aria-invalid={Boolean(error)}
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

                <div className={shared.field}>
                  <label htmlFor="reset-confirm-password" className={shared.fieldLabel}>
                    Xác nhận mật khẩu mới
                  </label>
                  <input
                    id="reset-confirm-password"
                    type={showPassword ? "text" : "password"}
                    autoComplete="new-password"
                    className={shared.input}
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    placeholder="Nhập lại mật khẩu mới"
                    disabled={submitting}
                    aria-invalid={Boolean(error)}
                    required
                  />
                </div>

                {error && (
                  <div role="alert">
                    <p className={shared.fieldError}>{error}</p>
                    <p className={styles.switchLine} style={{ marginTop: 8 }}>
                      <Link href="/quen-mat-khau" className={styles.switchLink}>
                        Yêu cầu liên kết đặt lại mới
                      </Link>
                    </p>
                  </div>
                )}

                <div>
                  <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
                    {submitting ? "Đang cập nhật mật khẩu..." : "Lưu mật khẩu mới"}
                  </Button>
                </div>

                <p className={styles.switchLine}>
                  <Link href="/dang-nhap" className={styles.switchLink}>
                    Quay lại trang đăng nhập
                  </Link>
                </p>
              </form>
            )}
          </div>

          <div className={styles.showcaseCol}>
            <div
              className={styles.perksBand}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <h2 className={styles.perksHeading}>Bảo mật tài khoản NexusMart</h2>
              <ul className={styles.perkList}>
                {PASSWORD_TIPS.map((item, idx) => (
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

export default function ResetPasswordPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang mở trang đặt lại mật khẩu..." rows={3} />
        </div>
      }
    >
      <ResetPasswordContent />
    </Suspense>
  );
}
