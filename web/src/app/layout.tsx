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
    default: "NexusMart · Đồ basic đa gian hàng Việt Nam",
    template: "%s · NexusMart",
  },
  description:
    "Sàn thương mại điện tử đa gian hàng đồ basic nam nữ Việt Nam, minh bạch chất liệu và bảng size, giao hàng 34 tỉnh thành.",
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
