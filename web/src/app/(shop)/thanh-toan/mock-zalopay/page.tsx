import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { Button } from "@/components/Button";
import shared from "@/features/commerce/commerce.module.css";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Trang mô phỏng ZaloPay · NexusMart",
  robots: { index: false, follow: false },
};

function isMockZaloPayDisabledByEnv(): boolean {
  const explicitFlag = (process.env.ENABLE_MOCK_ZALOPAY ?? "").trim().toLowerCase();
  if (explicitFlag === "false" || explicitFlag === "0") {
    return true;
  }
  const mode = (process.env.ZALOPAY_MODE ?? "").trim().toLowerCase();
  if (mode !== "" && mode !== "mock") {
    return true;
  }
  return false;
}

export default function MockZaloPayPage() {
  if (isMockZaloPayDisabledByEnv()) {
    notFound();
  }

  return (
    <div className={shared.page}>
      <section className={shared.pageHeader}>
        <div className={shared.headerLeadGroup}>
          <h1 className={shared.pageTitle}>Trang mô phỏng ZaloPay</h1>
          <p className={shared.pageLead}>
            Trang mô phỏng, không phải trang ZaloPay thật. Chỉ dùng cho môi trường phát triển và
            kiểm thử nội bộ.
          </p>
        </div>
      </section>

      <section className={shared.section}>
        <div className={shared.surfacePanel}>
          <h2 className={shared.sectionTitle}>Lưu ý môi trường mô phỏng</h2>
          <p className={shared.fieldHint} style={{ marginTop: 12 }}>
            Trang này không thu thập thông tin thẻ, mật khẩu hay mã OTP và không hiển thị số tiền
            hoặc mã giao dịch từ liên kết bên ngoài như thông tin đã xác nhận.
          </p>
          <p className={shared.fieldHint} style={{ marginTop: 10 }}>
            Để xem số tiền chính xác và trạng thái thanh toán thực tế của đơn hàng, vui lòng kiểm
            tra trực tiếp tại trang quản lý đơn hàng của bạn.
          </p>
          <div className={shared.formActions} style={{ marginTop: 20 }}>
            <Button variant="primary" size="md" href="/don-hang">
              Xem đơn hàng của tôi
            </Button>
            <Button variant="secondary" size="md" href="/">
              Về trang chủ
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}
