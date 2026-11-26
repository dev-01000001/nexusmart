import type { Metadata } from "next";
import React from "react";
import { Toaster } from "@/components/ui/sonner";
import "@/styles/ui.css";

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
    <>
      {children}
      <Toaster />
    </>
  );
}
