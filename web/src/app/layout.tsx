import type { Metadata } from "next";
import { Work_Sans } from "next/font/google";
import { SessionProvider } from "@/context/SessionContext";
import { MockStoreProvider } from "@/mock/MockStoreContext";
import { CartProvider } from "@/context/CartContext";
import "@/styles/tokens.css";
import "@/styles/globals.css";

const workSans = Work_Sans({
  subsets: ["latin", "vietnamese"],
  weight: ["300", "400", "500", "600"],
  variable: "--font-work-sans",
  display: "swap",
});

export const metadata: Metadata = {
  title: {
    default: "NexusMart · Đồ basic đa gian hàng Việt Nam",
    template: "%s · NexusMart",
  },
  description:
    "Sàn thương mại điện tử đa gian hàng đồ basic nam nữ Việt Nam, minh bạch chất liệu và bảng size, giao hàng 34 tỉnh thành.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="vi" className={workSans.variable}>
      <body>
        <SessionProvider>
          <MockStoreProvider>
            <CartProvider>{children}</CartProvider>
          </MockStoreProvider>
        </SessionProvider>
      </body>
    </html>
  );
}
