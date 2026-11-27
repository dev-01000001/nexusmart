import type { Metadata } from "next";
import { RevealObserver } from "@/components/Reveal";
import { SessionProvider } from "@/components/SessionProvider";
import { StoreProvider } from "@/context/StoreContext";
import { fontVariableClassNames } from "@/lib/fonts";
import { getSession, type SessionUser } from "@/lib/server/session";
import "@/styles/ui.css";

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
    <html lang="vi" className={fontVariableClassNames} suppressHydrationWarning>
      <head>
        <script
          dangerouslySetInnerHTML={{
            __html:
              "document.documentElement.classList.add('js');setTimeout(function(){if(!window.__nmRevealReady)document.documentElement.classList.remove('js');},3000);",
          }}
        />
      </head>
      <body>
        <SessionProvider initialUser={initialUser}>
          <StoreProvider>
            <RevealObserver />
            {children}
          </StoreProvider>
        </SessionProvider>
      </body>
    </html>
  );
}
