import type { Metadata } from "next";
import React from "react";
import { PlatformFooter } from "@/features/platform/PlatformFooter";
import { PlatformHeader } from "@/features/platform/PlatformHeader";
import styles from "./layout.module.css";

export const metadata: Metadata = {
  title: {
    default: "NexusMart · Nền tảng mở cửa hàng trực tuyến độc lập",
    template: "%s · NexusMart",
  },
  description:
    "Nền tảng thương mại điện tử giúp mỗi thương hiệu và xưởng sản xuất tự vận hành cửa hàng trực tuyến trên tên miền phụ riêng, tích hợp thanh toán COD, VietQR và đối soát minh bạch.",
};

export default function PlatformLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className={styles.shell}>
      <PlatformHeader />
      <main id="main-content" tabIndex={-1} className={styles.main}>
        {children}
      </main>
      <PlatformFooter />
    </div>
  );
}
