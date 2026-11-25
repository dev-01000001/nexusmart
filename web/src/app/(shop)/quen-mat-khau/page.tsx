import React from "react";
import type { Metadata } from "next";
import { ForgotPasswordForm } from "@/features/account/AuthForms";
import { PlatformAuthShell } from "@/features/platform/PlatformAuthShell";

export const metadata: Metadata = {
  title: "Quên mật khẩu — NexusMart",
  description: "Nhận liên kết đặt lại mật khẩu qua email đã đăng ký tài khoản NexusMart.",
};

export default function PlatformForgotPasswordPage() {
  return (
    <PlatformAuthShell mode="recovery" title="Quên mật khẩu" initialRole="seller">
      <ForgotPasswordForm loginHref="/dang-nhap" registerHref="/dang-ky" />
    </PlatformAuthShell>
  );
}
