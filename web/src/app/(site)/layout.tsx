import React from "react";
import { Be_Vietnam_Pro, IBM_Plex_Sans } from "next/font/google";

const beVietnamPro = Be_Vietnam_Pro({
  subsets: ["latin", "vietnamese"],
  weight: ["400", "500", "600"],
  variable: "--font-be-vietnam-pro",
  display: "swap",
});

const ibmPlexSans = IBM_Plex_Sans({
  subsets: ["latin", "vietnamese"],
  weight: ["400", "500", "600"],
  variable: "--font-ibm-plex-sans",
  display: "swap",
});

export default function SiteGroupLayout({ children }: { children: React.ReactNode }) {
  return <div className={`${beVietnamPro.variable} ${ibmPlexSans.variable}`}>{children}</div>;
}
