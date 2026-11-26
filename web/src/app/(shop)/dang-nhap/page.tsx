import type { Metadata } from "next";
import React from "react";
import { LoginForm } from "@/features/account/AuthForms";
import { PlatformAuthFrame } from "@/features/platform/auth/PlatformAuthFrame";

export const metadata: Metadata = {
  title: "Đăng nhập",
  description:
    "Đăng nhập Kênh người bán hoặc Cổng quản trị trên nền tảng NexusMart. Khách mua hàng sẽ được dẫn tới danh bạ cửa hàng.",
};

export default function PlatformLoginPage() {
  return (
    <PlatformAuthFrame mode="login" title="Đăng nhập" initialRole="seller">
      <LoginForm
        variant="platform"
        buyerHref="/#danh-ba-gian-hang"
        registerHref="/dang-ky?role=seller"
      />
    </PlatformAuthFrame>
  );
}
