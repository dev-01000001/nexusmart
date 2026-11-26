import type { Metadata } from "next";
import { Inter } from "next/font/google";
import React from "react";
import { PlatformFooter } from "@/features/platform/PlatformFooter";
import { PlatformHeader } from "@/features/platform/PlatformHeader";
import styles from "./layout.module.css";

const inter = Inter({
  subsets: ["latin", "vietnamese"],
  axes: ["opsz"],
  variable: "--font-inter",
  display: "swap",
});

export const metadata: Metadata = {
  title: {
    default: "Nền tảng cửa hàng độc lập",
    template: "%s | NexusMart",
  },
  description:
    "Nền tảng thương mại điện tử giúp mỗi thương hiệu và xưởng sản xuất tự vận hành cửa hàng trực tuyến trên tên miền phụ riêng, tích hợp thanh toán COD, VietQR và đối soát minh bạch.",
};

export default function PlatformLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className={`${styles.shell} ${inter.variable}`}>
      <PlatformHeader />
      <main id="main-content" tabIndex={-1} className={styles.main}>
        {children}
      </main>
      <PlatformFooter />
    </div>
  );
}
