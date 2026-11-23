"use client";

import React, { Suspense, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { PRODUCTS } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import type { ForgotPasswordResponse } from "@/lib/gateway-types";
import { authApi, getErrorMessage } from "@/lib/api";
import { Button, StateView } from "@/components";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "@/app/(shop)/dang-nhap/page.module.css";

const RESET_GUIDES = [
  {
    title: "Liên kết đặt lại hiệu lực 15 phút",
    detail:
      "Mỗi yêu cầu tạo một liên kết bảo mật dùng một lần. Yêu cầu mới sẽ tự động thay thế liên kết cũ.",
  },
  {
    title: "Bảo vệ tài khoản toàn diện",
    detail:
      "Ngay sau khi đổi mật khẩu thành công, toàn bộ phiên đăng nhập cũ trên mọi thiết bị sẽ tự động đăng xuất.",
  },
  {
    title: "Hỗ trợ khi không nhận được thư",
    detail:
      "Kiểm tra mục Thư rác hoặc Quảng cáo, đảm bảo nhập đúng email đã đăng ký tài khoản NexusMart.",
  },
];

function ForgotPasswordContent() {
  const viewState = usePageStateParam();
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useRevealOnScroll(`${viewState ?? "live"}-${submitted ? "sent" : "form"}`);

  const showcaseProducts = useMemo(
    () =>
      PRODUCTS.filter((p) =>
        ["prod-at-trang", "prod-polo-xanhda", "prod-short-daily", "prod-jogger-terry"].includes(
          p.id
        )
      ),
    []
  );

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const cleanEmail = email.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(cleanEmail)) {
      setError("Vui lòng nhập địa chỉ email hợp lệ (ví dụ: tenban@nexusmart.vn).");
      return;
    }

    setError(null);
    setSubmitting(true);
    try {
      await authApi<ForgotPasswordResponse>("forgot-password", {
        method: "POST",
        json: { email: cleanEmail },
      });
      setSubmitted(true);
    } catch (err) {
      setError(getErrorMessage(err, "Chưa thể gửi yêu cầu lúc này. Vui lòng thử lại sau ít phút."));
    } finally {
      setSubmitting(false);
    }
  };

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Quên mật khẩu</h1>
            <p className={styles.authLead}>Đang chuẩn bị biểu mẫu khôi phục tài khoản.</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView kind="loading" title="Đang tải trang khôi phục mật khẩu..." rows={3} />
        </section>
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        <section className={shared.pageHeader}>
          <div className={styles.authHeader}>
            <h1 className={shared.pageTitle}>Quên mật khẩu</h1>
            <p className={styles.authLead}>Dịch vụ gửi thư khôi phục đang tạm gián đoạn.</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView
            kind="error"
            title="Chưa thể kết nối lúc này"
            message="Vui lòng thử lại sau ít phút hoặc quay lại trang đăng nhập."
            actionLabel="Quay lại đăng nhập"
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
            <h1 className={shared.pageTitle}>Quên mật khẩu</h1>
            <p className={styles.authLead}>Nhập email đã đăng ký để nhận liên kết đặt lại.</p>
          </div>
        </section>
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Chưa có yêu cầu đặt lại mật khẩu nào"
            message="Mở biểu mẫu để gửi liên kết đặt lại mật khẩu về email tài khoản của bạn."
            actionLabel="Mở biểu mẫu"
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
                    Quên mật khẩu
                  </span>
                </span>
              </h1>
              <p
                className={styles.authLead}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                Nhập địa chỉ email đăng ký tài khoản NexusMart để nhận liên kết đặt lại mật khẩu có
                hiệu lực trong 15 phút.
              </p>
            </div>

            {submitted ? (
              <div className={styles.authPanel} role="status" aria-live="polite">
                <div className={styles.sessionBar}>
                  <p className={styles.bodyText}>
                    Nếu địa chỉ email <strong>{email.trim()}</strong> đã đăng ký trên NexusMart,
                    chúng tôi đã gửi liên kết đặt lại mật khẩu có hiệu lực trong 15 phút. Vui lòng
                    kiểm tra hộp thư đến hoặc thư rác.
                  </p>
                </div>
                <div className={shared.formActions}>
                  <Button variant="primary" size="md" href="/dang-nhap">
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
            ) : (
              <form
                className={styles.authPanel}
                onSubmit={(e) => void handleSubmit(e)}
                noValidate
                data-rise=""
                style={{ "--i": 2 } as React.CSSProperties}
              >
                <div className={shared.field}>
                  <label htmlFor="forgot-email" className={shared.fieldLabel}>
                    Địa chỉ email đã đăng ký
                  </label>
                  <input
                    id="forgot-email"
                    type="email"
                    autoComplete="email"
                    className={shared.input}
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="tenban@nexusmart.vn"
                    disabled={submitting}
                    aria-invalid={Boolean(error)}
                    aria-describedby={error ? "forgot-email-error" : undefined}
                    required
                  />
                </div>

                {error && (
                  <p id="forgot-email-error" className={shared.fieldError} role="alert">
                    {error}
                  </p>
                )}

                <div>
                  <Button type="submit" variant="primary" size="lg" fullWidth disabled={submitting}>
                    {submitting ? "Đang gửi liên kết..." : "Gửi liên kết đặt lại mật khẩu"}
                  </Button>
                </div>

                <p className={styles.switchLine}>
                  Nhớ mật khẩu rồi?{" "}
                  <Link href="/dang-nhap" className={styles.switchLink}>
                    Quay lại đăng nhập
                  </Link>{" "}
                  ·{" "}
                  <Link href="/dang-ky" className={styles.switchLink}>
                    Tạo tài khoản mới
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
              <h2 className={styles.perksHeading}>Lưu ý khôi phục tài khoản</h2>
              <ul className={styles.perkList}>
                {RESET_GUIDES.map((item, idx) => (
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

export default function ForgotPasswordPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang mở trang quên mật khẩu..." rows={3} />
        </div>
      }
    >
      <ForgotPasswordContent />
    </Suspense>
  );
}
