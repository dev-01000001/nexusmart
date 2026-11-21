import type { Metadata } from "next";
import { Work_Sans } from "next/font/google";
import { SessionProvider } from "@/components/SessionProvider";
import { StoreProvider } from "@/context/StoreContext";
import { getSession, type SessionUser } from "@/lib/server/session";
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
    default: "NexusMart · Nền tảng tạo cửa hàng trực tuyến cho gian hàng Việt Nam",
    template: "%s · NexusMart",
  },
  description:
    "Nền tảng vận hành cửa hàng trực tuyến theo tên miền riêng cho gian hàng Việt Nam: giao diện tùy biến, tồn kho thời gian thực, thanh toán COD & VietQR, đối soát minh bạch.",
};

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  let initialUser: SessionUser | null = null;
  try {
    const session = await getSession();
    initialUser = session?.user ?? null;
  } catch {
    // Public storefront stays accessible when gateway is temporarily unavailable;
    // protected pages enforce real session/role via requireSession()/requireRole().
  }

  return (
    <html lang="vi" className={workSans.variable}>
      <body>
        <SessionProvider initialUser={initialUser}>
          <StoreProvider>{children}</StoreProvider>
        </SessionProvider>
      </body>
    </html>
  );
}
