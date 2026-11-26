import React from "react";
import type { Metadata } from "next";
import { ResetPasswordForm } from "@/features/account/AuthForms";
import { PlatformAuthFrame } from "@/features/platform/auth/PlatformAuthFrame";

export const metadata: Metadata = {
  title: "Đặt lại mật khẩu — NexusMart",
  description: "Tạo mật khẩu mới cho tài khoản NexusMart.",
  robots: { index: false, follow: false },
};

export default function PlatformResetPasswordPage() {
  return (
    <PlatformAuthFrame
      mode="recovery"
      title="Đặt lại mật khẩu"
      subtitle="Tạo mật khẩu mới cho tài khoản NexusMart."
      initialRole="seller"
    >
      <ResetPasswordForm loginHref="/dang-nhap" forgotHref="/quen-mat-khau" />
    </PlatformAuthFrame>
  );
}
