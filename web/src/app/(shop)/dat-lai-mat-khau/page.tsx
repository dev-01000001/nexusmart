import React from "react";
import type { Metadata } from "next";
import { ResetPasswordForm } from "@/features/account/AuthForms";
import { PlatformAuthShell } from "@/features/platform/PlatformAuthShell";

export const metadata: Metadata = {
  title: "Đặt lại mật khẩu — NexusMart",
  description: "Tạo mật khẩu mới cho tài khoản NexusMart.",
  robots: { index: false, follow: false },
};

export default function PlatformResetPasswordPage() {
  return (
    <PlatformAuthShell mode="recovery" title="Đặt lại mật khẩu" initialRole="seller">
      <ResetPasswordForm loginHref="/dang-nhap" forgotHref="/quen-mat-khau" />
    </PlatformAuthShell>
  );
}
