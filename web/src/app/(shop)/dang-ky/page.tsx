"use client";

import React, { Suspense, useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCTS } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { useStore } from "@/context/StoreContext";
import { Button, StateView } from "@/components";
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
  const viewState = usePageStateParam();
  const { registerMock } = useStore();

  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);

  useRevealOnScroll(viewState ?? "live");

  const showcaseProducts = useMemo(
    () =>
      PRODUCTS.filter((p) =>
        ["prod-at-dryfit", "prod-polo-kem", "prod-short-linen", "prod-aokhoac-gio"].includes(p.id)
      ),
    []
  );

  const handleSubmit = (e: React.FormEvent) => {
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
    if (!/^0\d{9,10}$/.test(cleanPhone)) {
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
    registerMock(cleanName, cleanEmail, phone.trim());
    router.push("/tai-khoan");
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
            message="Mở biểu mẫu đăng ký để nhận ngay 15.000 Xu và lưu địa chỉ nhận hàng."
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
          {/* Left column: heading + registration form immediately above the fold */}
          <div className={styles.formCol}>
            <div className={styles.authHeader}>
              <h1 className={`${shared.pageTitle} lines`}>
                <span className="line">
                  <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                    Tạo tài khoản mới
                  </span>
                </span>
              </h1>
              <p
                className={styles.authLead}
                data-rise=""
                style={{ "--i": 1 } as React.CSSProperties}
              >
                Đăng ký một lần để mua đồ cơ bản từ 4 xưởng dệt Việt Nam, nhận ngay 15.000 Xu và đổi
                trả miễn phí 45 ngày.
              </p>
            </div>

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
                    required
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
                  Thành viên mới nhận ngay <strong className="num">15.000 Xu</strong> (tương đương{" "}
                  <strong className="num">15.000₫</strong>), hoàn Xu 1–6% nhân đôi Thứ Sáu và không
                  cần liên kết thẻ ngân hàng.
                </p>
              </div>

              {error && (
                <p className={shared.fieldError} role="alert">
                  {error}
                </p>
              )}

              <div data-rise="" style={{ "--i": 7 } as React.CSSProperties}>
                <Button type="submit" variant="primary" size="lg" fullWidth>
                  Tạo tài khoản NexusMart
                </Button>
              </div>

              <p
                className={styles.switchLine}
                data-rise=""
                style={{ "--i": 8 } as React.CSSProperties}
              >
                Đã có tài khoản thành viên?{" "}
                <Link href="/dang-nhap" className={styles.switchLink}>
                  Đăng nhập ngay
                </Link>
              </p>
            </form>
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
