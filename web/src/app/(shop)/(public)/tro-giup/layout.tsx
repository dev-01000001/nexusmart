import type { Metadata } from "next";
import React from "react";

export const metadata: Metadata = {
  title: "Trợ giúp",
  description:
    "Tìm nhanh câu trả lời về mở gian hàng, giao diện, thanh toán VietQR, COD và bảo mật trên NexusMart.",
};

export default function HelpLayout({ children }: { children: React.ReactNode }) {
  return children;
}
