import type { Metadata } from "next";
import React from "react";
import { Toaster } from "@/components/ui/sonner";
import "@/features/platform/home/home-keyframes.css";
import "@/features/platform/home/home-tokens.css";
import { PlatformFooter } from "@/features/platform/PlatformFooter";
import { PlatformHeader } from "@/features/platform/PlatformHeader";
import { platformInter } from "@/features/platform/platformFont";
import { PLATFORM_SHELL_ROOT } from "@/features/platform/platformStyles";

export const metadata: Metadata = {
  title: {
    default: "Nền tảng cửa hàng độc lập",
    template: "%s | NexusMart",
  },
  description:
    "Nền tảng thương mại điện tử giúp mỗi thương hiệu và xưởng sản xuất tự vận hành cửa hàng trực tuyến trên tên miền phụ riêng, tích hợp thanh toán COD, VietQR và đối soát minh bạch.",
};

export default function ShopRootLayout({ children }: { children: React.ReactNode }) {
  return (
    <div
      data-platform-public="true"
      className={`${platformInter.variable} ${PLATFORM_SHELL_ROOT} [&>.nm-page-enter]:flex [&>.nm-page-enter]:min-w-0 [&>.nm-page-enter]:flex-1 [&>.nm-page-enter]:flex-col`}
    >
      <PlatformHeader />
      {children}
      <PlatformFooter />
      <Toaster />
    </div>
  );
}
