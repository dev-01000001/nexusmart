import type { Metadata } from "next";
import React from "react";
import { LoginForm } from "@/features/account/AuthForms";
import { PlatformAuthShell } from "@/features/platform/PlatformAuthShell";
import styles from "./page.module.css";

export const metadata: Metadata = {
  title: "Đăng nhập",
  description:
    "Đăng nhập Kênh người bán hoặc Cổng quản trị trên nền tảng NexusMart. Khách mua hàng sẽ được dẫn tới danh bạ cửa hàng.",
};

export default function PlatformLoginPage() {
  return (
    <div className={styles.page}>
      <PlatformAuthShell mode="login" title="Đăng nhập" initialRole="seller">
        <LoginForm
          variant="platform"
          buyerHref="/#danh-ba-gian-hang"
          registerHref="/dang-ky?role=seller"
        />
      </PlatformAuthShell>
    </div>
  );
}
